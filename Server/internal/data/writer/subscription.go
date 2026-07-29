package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/npanel-dev/NPanel-backend/ent"
	"github.com/npanel-dev/NPanel-backend/ent/proxyusersubscribe"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/checkpoint"
)

type TrialAssignment struct {
	SubscribeID   int64
	DurationUnit  string
	DurationValue int64
}

// WriteSubscriptions 批量写入用户订阅。
//
// 映射规则（方案 6.3）：
//   - 永久订阅（ExpireTime==nil）→ expire_time = 本次写入开始时间 + 1 个月
//   - status=4（已扣除）：跳过（不写入 user_subscribe），由调用方决定是否归档流量
//   - status=5/stopped：转 status=3（过期）+ note 标记
//   - status=3 且有实际过期时间时补 finished_at，避免被 7 天过滤规则隐藏
//
// sourceMap 提供 sourceUserID/sourcePlanID → npanelID 映射。
func WriteSubscriptions(
	ctx context.Context,
	client *ent.Client,
	subs []*canonical.UserSubscription,
	sourceMap *canonical.SourceMap,
	trial TrialAssignment,
) (int, int, error) {
	errCount := 0
	written := 0
	planCache := make(map[int64]*ent.ProxySubscribe)
	migrationAnchor := time.Now()

	for _, s := range subs {
		// status=4（已扣除）：跳过（方案 6.3.2）。
		if s.Status == 4 {
			continue
		}

		// 查找用户目标 ID。
		npanelUserID, ok := sourceMap.UserIDs[s.UserSourceID]
		if !ok {
			errCount++
			continue // 用户未写入，跳过
		}

		if s.NeedsTrial {
			created, err := writeTrialSubscription(
				ctx, client, npanelUserID, trial, migrationAnchor, planCache,
			)
			if err != nil {
				return written, errCount, fmt.Errorf("为源用户 %d 创建体验订阅失败: %w", s.UserSourceID, err)
			}
			if created {
				written++
			}
			continue
		}

		npanelSubscribeID, ok := sourceMap.PlanIDs[s.PlanSourceID]
		if !ok {
			return written, errCount, fmt.Errorf(
				"源用户 %d 的套餐 %d 缺少目标映射",
				s.UserSourceID, s.PlanSourceID,
			)
		}
		targetPlan, err := getTargetPlan(ctx, client, npanelSubscribeID, planCache)
		if err != nil {
			return written, errCount, err
		}

		// 映射目标 status。
		npanelStatus := mapStatus(s.Status)
		expireTime := importedSubscriptionExpireTime(s.ExpireTime, migrationAnchor)

		// 订单关联：若存在则映射，否则填 0（NPanel order_id 非外键，允许 0）。
		var orderID int64 = 0
		if s.OrderSourceID != 0 {
			if id, ok := sourceMap.OrderIDs[s.OrderSourceID]; ok {
				orderID = id
			}
		}

		// 构造 builder。
		builder := client.ProxyUserSubscribe.Create().
			SetUserID(npanelUserID).
			SetSubscribeID(npanelSubscribeID).
			SetOrderID(orderID).
			SetNodeGroupID(targetPlanNodeGroupID(targetPlan)).
			SetGroupLocked(false).
			SetStartTime(s.StartTime).
			SetCreatedAt(s.CreatedAt).
			SetExpireTime(expireTime).
			SetTraffic(s.TrafficBytes).
			SetDownload(s.DownloadBytes).
			SetUpload(s.UploadBytes).
			SetNillableStatus(&npanelStatus)

		// token/uuid。
		if s.Token != "" {
			builder.SetToken(s.Token)
		}
		if s.UUID != "" {
			builder.SetUUID(s.UUID)
		}

		// status=3/5 转 3 时补 finished_at，避免被 NPanel 的 7 天过滤隐藏。
		if npanelStatus == 3 && s.ExpireTime != nil {
			builder.SetFinishedAt(expireTime)
		}

		// status=5（stopped）加 note 标记。
		if s.Status == 5 {
			builder.SetNote("migrated from source status=5(stopped)")
		}

		_, err = builder.Save(ctx)
		if err != nil {
			errCount++
			continue
		}
		written++
	}

	return written, errCount, nil
}

// WriteSubscriptionsBulk 批量写入普通订阅和体验订阅，并将映射与 checkpoint
// 和目标业务数据放入同一个事务。约束错误使用与用户相同的二分隔离策略。
func WriteSubscriptionsBulk(
	ctx context.Context,
	runtime *Runtime,
	store *checkpoint.Store,
	jobID, owner string,
	subs []*canonical.UserSubscription,
	sourceMap *canonical.SourceMap,
	trial TrialAssignment,
	trialAnchor time.Time,
	planCache map[int64]*ent.ProxySubscribe,
	cp *checkpoint.Checkpoint,
) (int, int, error) {
	written := 0
	failed, err := executeBulkWithBisect(
		subs,
		func(batch []*canonical.UserSubscription) error {
			created, err := writeSubscriptionsBulkTx(
				ctx, runtime, store, jobID, owner, batch, sourceMap,
				trial, trialAnchor, planCache, cp,
			)
			written += created
			return err
		},
		func(sub *canonical.UserSubscription, cause error) error {
			return recordRejectedEntity(
				ctx, runtime, store, jobID, owner, "subscriptions", "subscription",
				sub.SourceID, cause, cp,
			)
		},
	)
	return written, failed, err
}

func writeSubscriptionsBulkTx(
	ctx context.Context,
	runtime *Runtime,
	store *checkpoint.Store,
	jobID, owner string,
	subs []*canonical.UserSubscription,
	sourceMap *canonical.SourceMap,
	trial TrialAssignment,
	trialAnchor time.Time,
	planCache map[int64]*ent.ProxySubscribe,
	cp *checkpoint.Checkpoint,
) (int, error) {
	for _, sub := range subs {
		if sub.Status == 4 {
			continue
		}
		subscribeID := trial.SubscribeID
		if !sub.NeedsTrial {
			var ok bool
			subscribeID, ok = sourceMap.PlanIDs[sub.PlanSourceID]
			if !ok {
				return 0, &batchDataError{message: fmt.Sprintf(
					"源订阅 %d 的套餐 %d 缺少目标映射", sub.SourceID, sub.PlanSourceID,
				)}
			}
		}
		if _, err := getTargetPlan(ctx, runtime.Client, subscribeID, planCache); err != nil {
			return 0, &batchDataError{message: err.Error()}
		}
	}

	tx, err := runtime.BeginTx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	trialTokens := make([]string, 0, len(subs))
	for _, sub := range subs {
		if sub.Status == 4 || !sub.NeedsTrial {
			continue
		}
		userID, ok := sourceMap.UserIDs[sub.UserSourceID]
		if !ok {
			return 0, &batchDataError{message: fmt.Sprintf(
				"源订阅 %d 缺少目标用户映射", sub.SourceID,
			)}
		}
		trialTokens = append(trialTokens, trialToken(userID))
	}
	existingTrials := make(map[string]int64)
	if len(trialTokens) > 0 {
		existing, err := tx.Client.ProxyUserSubscribe.Query().
			Where(proxyusersubscribe.TokenIn(trialTokens...)).
			All(ctx)
		if err != nil {
			return 0, err
		}
		for _, item := range existing {
			if item.Token != nil {
				existingTrials[*item.Token] = item.ID
			}
		}
	}

	builders := make([]*ent.ProxyUserSubscribeCreate, 0, len(subs))
	createSources := make([]*canonical.UserSubscription, 0, len(subs))
	mappings := make([]checkpoint.EntityMapping, 0, len(subs))
	for _, sub := range subs {
		if sub.Status == 4 {
			mappings = append(mappings, checkpoint.EntityMapping{SourceID: sub.SourceID, TargetID: 0})
			continue
		}
		userID, ok := sourceMap.UserIDs[sub.UserSourceID]
		if !ok {
			return 0, &batchDataError{message: fmt.Sprintf(
				"源订阅 %d 缺少目标用户映射", sub.SourceID,
			)}
		}
		if sub.NeedsTrial {
			token := trialToken(userID)
			if existingID := existingTrials[token]; existingID > 0 {
				mappings = append(mappings, checkpoint.EntityMapping{
					SourceID: sub.SourceID,
					TargetID: existingID,
				})
				continue
			}
			plan := planCache[trial.SubscribeID]
			expireTime := subscriptionExpireTime(
				trialAnchor, trial.DurationUnit, trial.DurationValue,
			)
			builders = append(builders, tx.Client.ProxyUserSubscribe.Create().
				SetUserID(userID).
				SetOrderID(0).
				SetSubscribeID(plan.ID).
				SetNodeGroupID(targetPlanNodeGroupID(plan)).
				SetGroupLocked(false).
				SetStartTime(trialAnchor).
				SetCreatedAt(trialAnchor).
				SetExpireTime(expireTime).
				SetTraffic(plan.Traffic).
				SetDownload(0).
				SetUpload(0).
				SetToken(token).
				SetUUID(uuid.NewString()).
				SetStatus(1).
				SetNote("migration: assigned configured trial plan because source subscription was inactive"))
			createSources = append(createSources, sub)
			continue
		}

		subscribeID := sourceMap.PlanIDs[sub.PlanSourceID]
		plan := planCache[subscribeID]
		expireTime := importedSubscriptionExpireTime(sub.ExpireTime, trialAnchor)
		orderID := int64(0)
		if sub.OrderSourceID != 0 {
			orderID = sourceMap.OrderIDs[sub.OrderSourceID]
		}
		status := mapStatus(sub.Status)
		builder := tx.Client.ProxyUserSubscribe.Create().
			SetUserID(userID).
			SetSubscribeID(subscribeID).
			SetOrderID(orderID).
			SetNodeGroupID(targetPlanNodeGroupID(plan)).
			SetGroupLocked(false).
			SetStartTime(sub.StartTime).
			SetCreatedAt(sub.CreatedAt).
			SetExpireTime(expireTime).
			SetTraffic(sub.TrafficBytes).
			SetDownload(sub.DownloadBytes).
			SetUpload(sub.UploadBytes).
			SetStatus(status)
		if sub.Token != "" {
			builder.SetToken(sub.Token)
		}
		if sub.UUID != "" {
			builder.SetUUID(sub.UUID)
		}
		if status == 3 && sub.ExpireTime != nil {
			builder.SetFinishedAt(expireTime)
		}
		if sub.Status == 5 {
			builder.SetNote("migrated from source status=5(stopped)")
		}
		builders = append(builders, builder)
		createSources = append(createSources, sub)
	}

	trialTargetIDs := make([]int64, 0, len(builders))
	for _, sub := range subs {
		if !sub.NeedsTrial || sub.Status == 4 {
			continue
		}
		if existingID := existingTrials[trialToken(sourceMap.UserIDs[sub.UserSourceID])]; existingID > 0 {
			trialTargetIDs = append(trialTargetIDs, existingID)
		}
	}
	if len(builders) > 0 {
		created, err := tx.Client.ProxyUserSubscribe.CreateBulk(builders...).Save(ctx)
		if err != nil {
			return 0, err
		}
		if len(created) != len(createSources) {
			return 0, fmt.Errorf(
				"订阅 Bulk 返回数量异常: got %d want %d", len(created), len(createSources),
			)
		}
		for index, source := range createSources {
			mappings = append(mappings, checkpoint.EntityMapping{
				SourceID: source.SourceID,
				TargetID: created[index].ID,
			})
			if source.NeedsTrial {
				trialTargetIDs = append(trialTargetIDs, created[index].ID)
			}
		}
	}
	if runtime.HasTrialFlagColumn && len(trialTargetIDs) > 0 {
		if err := markTrialSubscriptions(ctx, tx.SQL, trialTargetIDs); err != nil {
			return 0, err
		}
	}
	if err := store.PutMappingsTx(ctx, tx.SQL, jobID, "subscription", mappings); err != nil {
		return 0, err
	}
	next := *cp
	next.LastSourceID = subs[len(subs)-1].SourceID
	next.Done += int64(len(subs))
	if err := store.RecordBatchTx(ctx, tx.SQL, checkpoint.BatchRecord{
		JobID: jobID, Phase: "subscriptions",
		CursorFrom: subs[0].SourceID, CursorTo: next.LastSourceID,
		Attempted: len(subs), Succeeded: len(subs), Status: "committed",
	}); err != nil {
		return 0, err
	}
	if err := store.SaveCheckpointTx(ctx, tx.SQL, next, owner); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	*cp = next
	return len(builders), nil
}

func markTrialSubscriptions(ctx context.Context, tx *sql.Tx, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	var query strings.Builder
	query.WriteString("UPDATE `user_subscribe` SET `is_trial` = 1 WHERE `id` IN (")
	args := make([]any, 0, len(ids))
	for index, id := range ids {
		if index > 0 {
			query.WriteByte(',')
		}
		query.WriteByte('?')
		args = append(args, id)
	}
	query.WriteByte(')')
	_, err := tx.ExecContext(ctx, query.String(), args...)
	return err
}

func writeTrialSubscription(
	ctx context.Context,
	client *ent.Client,
	userID int64,
	trial TrialAssignment,
	migrationAnchor time.Time,
	planCache map[int64]*ent.ProxySubscribe,
) (bool, error) {
	targetPlan, err := getTargetPlan(ctx, client, trial.SubscribeID, planCache)
	if err != nil {
		return false, err
	}
	token := trialToken(userID)
	exists, err := client.ProxyUserSubscribe.Query().
		Where(proxyusersubscribe.TokenEQ(token)).
		Exist(ctx)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	startTime := migrationAnchor
	expireTime := subscriptionExpireTime(startTime, trial.DurationUnit, trial.DurationValue)
	_, err = client.ProxyUserSubscribe.Create().
		SetUserID(userID).
		SetOrderID(0).
		SetSubscribeID(targetPlan.ID).
		SetNodeGroupID(targetPlanNodeGroupID(targetPlan)).
		SetGroupLocked(false).
		SetStartTime(startTime).
		SetCreatedAt(migrationAnchor).
		SetExpireTime(expireTime).
		SetTraffic(targetPlan.Traffic).
		SetDownload(0).
		SetUpload(0).
		SetToken(token).
		SetUUID(uuid.NewString()).
		SetStatus(1).
		SetNote("migration: assigned configured trial plan because source subscription was inactive").
		Save(ctx)
	return err == nil, err
}

func getTargetPlan(
	ctx context.Context,
	client *ent.Client,
	subscribeID int64,
	cache map[int64]*ent.ProxySubscribe,
) (*ent.ProxySubscribe, error) {
	if plan := cache[subscribeID]; plan != nil {
		return plan, nil
	}
	plan, err := client.ProxySubscribe.Get(ctx, subscribeID)
	if err != nil {
		return nil, fmt.Errorf("目标套餐 %d 不存在: %w", subscribeID, err)
	}
	cache[subscribeID] = plan
	return plan, nil
}

func targetPlanNodeGroupID(plan *ent.ProxySubscribe) int64 {
	if plan.NodeGroupID != nil && *plan.NodeGroupID > 0 {
		return *plan.NodeGroupID
	}
	for _, groupID := range plan.NodeGroupIds {
		if groupID > 0 {
			return groupID
		}
	}
	return 0
}

func trialToken(userID int64) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("Trial-%d", userID)))
	return hex.EncodeToString(hash[:16])
}

func importedSubscriptionExpireTime(sourceExpireTime *time.Time, migrationAnchor time.Time) time.Time {
	if sourceExpireTime != nil {
		return *sourceExpireTime
	}
	return migrationAnchor.AddDate(0, 1, 0)
}

func subscriptionExpireTime(start time.Time, unit string, value int64) time.Time {
	var expireTime time.Time
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "year":
		expireTime = start.AddDate(int(value), 0, 0)
	case "month":
		expireTime = start.AddDate(0, int(value), 0)
	case "day":
		expireTime = start.AddDate(0, 0, int(value))
	case "week":
		expireTime = start.AddDate(0, 0, int(value)*7)
	case "hour":
		expireTime = start.Add(time.Hour * time.Duration(value))
	case "minute":
		expireTime = start.Add(time.Minute * time.Duration(value))
	case "quarter":
		expireTime = start.AddDate(0, int(value)*3, 0)
	case "half_year":
		expireTime = start.AddDate(0, int(value)*6, 0)
	case "nolimit", "no_limit":
		expireTime = start.AddDate(0, 1, 0)
	default:
		expireTime = start
	}
	return expireTime
}

// mapStatus 源 status → NPanel status。
// NPanel: 0=Pending 1=Active 2=Finish 3=Expired 4=Deduct（运行时丢弃 4）
func mapStatus(srcStatus int) int8 {
	switch srcStatus {
	case 0:
		return 0 // Pending
	case 1:
		return 1 // Active
	case 2:
		return 2 // Finish
	case 3:
		return 3 // Expired
	case 5:
		return 3 // stopped → Expired（方案 3.4.5）
	default:
		return 3
	}
}
