package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/npanel-dev/NPanel-backend/ent"
	"github.com/npanel-dev/NPanel-backend/ent/proxyusersubscribe"

	"npanel-migrator/internal/data/canonical"
)

// unixZero 永久订阅的标准编码：Unix 0 时间戳（'1970-01-01'）。
// 方案 6.3.1：禁止用 NULL 表示永久（compat 路径会把 NULL 误判为具体过期时间）。
var unixZero = time.Unix(0, 0)

type TrialAssignment struct {
	SubscribeID   int64
	DurationUnit  string
	DurationValue int64
}

// WriteSubscriptions 批量写入用户订阅。
//
// 映射规则（方案 6.3）：
//   - 永久订阅（ExpireTime==nil）→ expire_time = Unix 0
//   - status=4（已扣除）：跳过（不写入 user_subscribe），由调用方决定是否归档流量
//   - status=5/stopped：转 status=3（过期）+ note 标记
//   - 所有 status=3 补 finished_at = expire_time，避免被 7 天过滤规则隐藏
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
			created, err := writeTrialSubscription(ctx, client, npanelUserID, trial, planCache)
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

		// 过期时间：nil → Unix 0（永久）。
		var expireTime time.Time
		if s.ExpireTime == nil {
			expireTime = unixZero
		} else {
			expireTime = *s.ExpireTime
		}

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
			SetNillableExpireTime(&expireTime).
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
		if npanelStatus == 3 {
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

func writeTrialSubscription(
	ctx context.Context,
	client *ent.Client,
	userID int64,
	trial TrialAssignment,
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

	startTime := time.Now()
	expireTime := addSubscriptionDuration(startTime, trial.DurationUnit, trial.DurationValue)
	_, err = client.ProxyUserSubscribe.Create().
		SetUserID(userID).
		SetOrderID(0).
		SetSubscribeID(targetPlan.ID).
		SetNodeGroupID(targetPlanNodeGroupID(targetPlan)).
		SetGroupLocked(false).
		SetStartTime(startTime).
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

func addSubscriptionDuration(start time.Time, unit string, value int64) time.Time {
	switch strings.ToLower(strings.TrimSpace(unit)) {
	case "year":
		return start.AddDate(int(value), 0, 0)
	case "month":
		return start.AddDate(0, int(value), 0)
	case "day":
		return start.AddDate(0, 0, int(value))
	case "week":
		return start.AddDate(0, 0, int(value)*7)
	case "hour":
		return start.Add(time.Hour * time.Duration(value))
	case "minute":
		return start.Add(time.Minute * time.Duration(value))
	case "quarter":
		return start.AddDate(0, int(value)*3, 0)
	case "half_year":
		return start.AddDate(0, int(value)*6, 0)
	case "nolimit", "no_limit":
		return unixZero
	default:
		return start
	}
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
