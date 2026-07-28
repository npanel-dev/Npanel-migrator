package writer

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/npanel-dev/NPanel-backend/ent"
	"github.com/npanel-dev/NPanel-backend/pkg/random"
	"github.com/npanel-dev/NPanel-backend/pkg/snowflake"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/checkpoint"
)

type batchDataError struct {
	message string
}

func (e *batchDataError) Error() string { return e.message }

func isSplittableBulkError(err error) bool {
	var dataErr *batchDataError
	return ent.IsConstraintError(err) ||
		ent.IsValidationError(err) ||
		errors.As(err, &dataErr)
}

// WriteUsers 批量写入用户（user + user_auth_methods）。
// 返回 sourceUserID → npanelUserID 的映射，供后续实体引用。
// batch 内逐条 Create（不批量），便于单条失败时跳过并记录错误。
func WriteUsers(ctx context.Context, client *ent.Client, users []*canonical.User) (map[int64]int64, int, error) {
	idMap := make(map[int64]int64, len(users))
	errCount := 0

	for _, u := range users {
		created, err := newUserBuilder(client, u).Save(ctx)
		if err != nil {
			errCount++
			// 唯一冲突等错误跳过，继续下一条。
			continue
		}

		idMap[u.SourceID] = created.ID

		// 写入邮箱认证方法（auth_type=email）。
		if strings.TrimSpace(u.Email) != "" {
			_, err := client.ProxyUserAuthMethod.Create().
				SetUserID(created.ID).
				SetAuthType("email").
				SetAuthIdentifier(strings.ToLower(strings.TrimSpace(u.Email))).
				SetVerified(u.EmailVerified).
				Save(ctx)
			if err != nil {
				// 邮箱唯一冲突：记录但不算致命（用户已创建）。
				errCount++
			}
		}
	}

	return idMap, errCount, nil
}

func newUserBuilder(client *ent.Client, u *canonical.User) *ent.ProxyUserCreate {
	referCode := targetReferCode(u)
	return client.ProxyUser.Create().
		SetPassword(u.PasswordHash).
		SetAlgo(u.PasswordAlgo).
		SetSalt(u.PasswordSalt).
		SetSourcePanel(sourcePanelOrUnknown(u.SourcePanel)).
		SetNillableBalance(&u.BalanceCents).
		SetNillableCommission(&u.CommissionCents).
		SetNillableGiftAmount(&u.GiftCents).
		SetEnable(u.Enabled).
		SetIsAdmin(u.IsAdmin).
		SetValidEmail(u.EmailVerified).
		SetNillableAvatar(nilIfEmpty(u.Avatar)).
		SetNillableReferCode(nilIfEmpty(referCode)).
		SetNillableTelegram(nilIfZero(u.TelegramID)).
		SetCreatedAt(u.CreatedAt).
		SetUpdatedAt(u.UpdatedAt).
		SetIsDel(1)
}

// WriteUsersBulk 用单个目标库事务批量写入用户、邮箱认证、映射和 checkpoint。
// 约束/校验错误会递归二分；连接或事务错误会立即返回，避免把基础设施故障误记为坏数据。
func WriteUsersBulk(
	ctx context.Context,
	runtime *Runtime,
	store *checkpoint.Store,
	jobID, owner string,
	users []*canonical.User,
	cp *checkpoint.Checkpoint,
) (map[int64]int64, int, error) {
	result := make(map[int64]int64, len(users))
	failed, err := executeBulkWithBisect(
		users,
		func(batch []*canonical.User) error {
			mappings, err := writeUsersBulkTx(
				ctx, runtime, store, jobID, owner, batch, cp,
			)
			if err != nil {
				return err
			}
			for _, mapping := range mappings {
				result[mapping.SourceID] = mapping.TargetID
			}
			return nil
		},
		func(user *canonical.User, cause error) error {
			return recordRejectedEntity(
				ctx, runtime, store, jobID, owner, "users", "user",
				user.SourceID, cause, cp,
			)
		},
	)
	return result, failed, err
}

func writeUsersBulkTx(
	ctx context.Context,
	runtime *Runtime,
	store *checkpoint.Store,
	jobID, owner string,
	users []*canonical.User,
	cp *checkpoint.Checkpoint,
) ([]checkpoint.EntityMapping, error) {
	tx, err := runtime.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	builders := make([]*ent.ProxyUserCreate, 0, len(users))
	for _, user := range users {
		builders = append(builders, newUserBuilder(tx.Client, user))
	}
	created, err := tx.Client.ProxyUser.CreateBulk(builders...).Save(ctx)
	if err != nil {
		return nil, err
	}
	if len(created) != len(users) {
		return nil, fmt.Errorf("用户 Bulk 返回数量异常: got %d want %d", len(created), len(users))
	}

	authBuilders := make([]*ent.ProxyUserAuthMethodCreate, 0, len(users))
	mappings := make([]checkpoint.EntityMapping, 0, len(users))
	for index, user := range users {
		mappings = append(mappings, checkpoint.EntityMapping{
			SourceID: user.SourceID,
			TargetID: created[index].ID,
		})
		email := strings.ToLower(strings.TrimSpace(user.Email))
		if email == "" {
			continue
		}
		authBuilders = append(authBuilders, tx.Client.ProxyUserAuthMethod.Create().
			SetUserID(created[index].ID).
			SetAuthType("email").
			SetAuthIdentifier(email).
			SetVerified(user.EmailVerified))
	}
	if len(authBuilders) > 0 {
		if _, err := tx.Client.ProxyUserAuthMethod.CreateBulk(authBuilders...).Save(ctx); err != nil {
			return nil, err
		}
	}
	if err := store.PutMappingsTx(ctx, tx.SQL, jobID, "user", mappings); err != nil {
		return nil, err
	}

	next := *cp
	next.LastSourceID = users[len(users)-1].SourceID
	next.Done += int64(len(users))
	if err := store.RecordBatchTx(ctx, tx.SQL, checkpoint.BatchRecord{
		JobID: jobID, Phase: "users",
		CursorFrom: users[0].SourceID, CursorTo: next.LastSourceID,
		Attempted: len(users), Succeeded: len(users), Status: "committed",
	}); err != nil {
		return nil, err
	}
	if err := store.SaveCheckpointTx(ctx, tx.SQL, next, owner); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	*cp = next
	return mappings, nil
}

func recordRejectedEntity(
	ctx context.Context,
	runtime *Runtime,
	store *checkpoint.Store,
	jobID, owner, phase, entityType string,
	sourceID int64,
	cause error,
	cp *checkpoint.Checkpoint,
) error {
	tx, err := runtime.BeginTx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := store.RecordErrorTx(ctx, tx.SQL, jobID, phase, entityType, sourceID, cause); err != nil {
		return err
	}
	next := *cp
	next.LastSourceID = sourceID
	next.Done++
	next.Errors++
	if err := store.RecordBatchTx(ctx, tx.SQL, checkpoint.BatchRecord{
		JobID: jobID, Phase: phase,
		CursorFrom: sourceID, CursorTo: sourceID,
		Attempted: 1, Failed: 1, Status: "rejected",
	}); err != nil {
		return err
	}
	if err := store.SaveCheckpointTx(ctx, tx.SQL, next, owner); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	*cp = next
	return nil
}

// BackfillReferersBulk 使用单条参数化 CASE UPDATE 回填一个源用户批次的邀请关系。
func BackfillReferersBulk(
	ctx context.Context,
	runtime *Runtime,
	store *checkpoint.Store,
	jobID, owner string,
	users []*canonical.User,
	userMap map[int64]int64,
	cp *checkpoint.Checkpoint,
) (int, error) {
	if len(users) == 0 {
		return 0, nil
	}
	tx, err := runtime.BeginTx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	type update struct {
		userID    int64
		refererID int64
	}
	updates := make([]update, 0, len(users))
	missing := 0
	for _, user := range users {
		if user.RefererSourceID == 0 {
			continue
		}
		userID, userOK := userMap[user.SourceID]
		refererID, refererOK := userMap[user.RefererSourceID]
		if !userOK {
			continue
		}
		if !refererOK {
			missing++
			if err := store.RecordErrorTx(
				ctx, tx.SQL, jobID, "refererBackfill", "user",
				user.SourceID, fmt.Errorf("邀请人源 ID %d 未成功迁移", user.RefererSourceID),
			); err != nil {
				return 0, err
			}
			continue
		}
		updates = append(updates, update{userID: userID, refererID: refererID})
	}

	if len(updates) > 0 {
		var query strings.Builder
		query.WriteString("UPDATE `user` SET referer_id = CASE id ")
		args := make([]any, 0, len(updates)*3)
		for _, item := range updates {
			query.WriteString("WHEN ? THEN ? ")
			args = append(args, item.userID, item.refererID)
		}
		query.WriteString("ELSE referer_id END WHERE id IN (")
		for index, item := range updates {
			if index > 0 {
				query.WriteByte(',')
			}
			query.WriteByte('?')
			args = append(args, item.userID)
		}
		query.WriteByte(')')
		if _, err := tx.SQL.ExecContext(ctx, query.String(), args...); err != nil {
			return 0, err
		}
	}

	next := *cp
	next.LastSourceID = users[len(users)-1].SourceID
	next.Done += int64(len(users))
	next.Errors += int64(missing)
	if err := store.RecordBatchTx(ctx, tx.SQL, checkpoint.BatchRecord{
		JobID: jobID, Phase: "refererBackfill",
		CursorFrom: users[0].SourceID, CursorTo: next.LastSourceID,
		Attempted: len(users), Succeeded: len(users) - missing,
		Failed: missing, Status: "committed",
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
	return missing, nil
}

// BackfillReferers 回填用户邀请关系（二阶段：用户全部写入后才知道目标 ID）。
// sourceMap 提供 sourceUserID → npanelUserID 映射。
func BackfillReferers(ctx context.Context, client *ent.Client, users []*canonical.User, idMap map[int64]int64) (int, error) {
	errCount := 0
	for _, u := range users {
		if u.RefererSourceID == 0 {
			continue
		}
		npanelUserID, ok := idMap[u.SourceID]
		if !ok {
			continue // 用户未成功写入，跳过
		}
		refererID, ok := idMap[u.RefererSourceID]
		if !ok {
			continue // 邀请人未写入，跳过
		}
		_, err := client.ProxyUser.UpdateOneID(npanelUserID).
			SetRefererID(refererID).
			Save(ctx)
		if err != nil {
			errCount++
		}
	}
	return errCount, nil
}

// targetReferCode 把来源面板的邀请码转换成 NPanel 目标格式。
//
// xiaov2board 的 token 是 32 字符 MD5；直接迁移或截断后虽然能写入数据库，
// 但不符合 NPanel 当前注册流程生成的分段邀请码格式。邀请上下级关系由
// referer_id 单独回填，因此这里为每个迁移用户生成新邀请码不会破坏邀请关系。
func targetReferCode(u *canonical.User) string {
	return targetReferCodeWithGenerator(u, generateNPanelReferCode)
}

func targetReferCodeWithGenerator(u *canonical.User, generate func(int64) string) string {
	if strings.EqualFold(strings.TrimSpace(u.SourcePanel), "xiaov2board") {
		return generate(u.SourceID)
	}
	return truncateReferCode(u.ReferCode)
}

// generateNPanelReferCode 与 NPanel pkg/tool.GenerateReferCode 使用同一实现：
// Snowflake ID → NPanel 自定义 Base36 → 每 4 字符插入连字符。
func generateNPanelReferCode(_ int64) string {
	code := random.EncodeBase36(snowflake.GetID())
	return random.StrToDashedString(code)
}

// nilIfEmpty 空字符串返回 nil（用于 Nillable 字段）。
func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// truncateReferCode 截断 refer_code 到 NPanel schema 限制的长度（MaxLen 20）。
// v2board 的 token 是 32 字符 MD5，超长会导致 ent 校验失败。
func truncateReferCode(code string) string {
	const maxLen = 20
	if len(code) > maxLen {
		return code[:maxLen]
	}
	return code
}

// nilIfZero 零值返回 nil。
func nilIfZero(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

func sourcePanelOrUnknown(panel string) string {
	panel = strings.ToLower(strings.TrimSpace(panel))
	if panel == "" {
		return "unknown"
	}
	return panel
}
