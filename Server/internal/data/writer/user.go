package writer

import (
	"context"
	"strings"

	"github.com/npanel-dev/NPanel-backend/ent"

	"npanel-migrator/internal/data/canonical"
)

// WriteUsers 批量写入用户（user + user_auth_methods）。
// 返回 sourceUserID → npanelUserID 的映射，供后续实体引用。
// batch 内逐条 Create（不批量），便于单条失败时跳过并记录错误。
func WriteUsers(ctx context.Context, client *ent.Client, users []*canonical.User) (map[int64]int64, int, error) {
	idMap := make(map[int64]int64, len(users))
	errCount := 0

	for _, u := range users {
		created, err := client.ProxyUser.Create().
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
			SetNillableReferCode(nilIfEmpty(truncateReferCode(u.ReferCode))).
			SetNillableTelegram(nilIfZero(u.TelegramID)).
			SetCreatedAt(u.CreatedAt).
			SetUpdatedAt(u.UpdatedAt).
			SetIsDel(1). // 语义反直觉：1=正常（方案 2 章）
			Save(ctx)
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
