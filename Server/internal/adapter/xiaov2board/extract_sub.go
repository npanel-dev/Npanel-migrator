package xiaov2board

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// ExtractSubscriptions 从 v2_user 读取当前订阅信息，转换为 canonical.UserSubscription。
//
// v2board 的当前订阅压在 v2_user 上（plan_id/u/d/transfer_enable/expired_at/token/uuid），
// 需要为每个有 plan_id 的用户生成一条 user_subscribe（方案 6.3）。
func ExtractSubscriptions(ctx context.Context, cfg db.Config) ([]*canonical.UserSubscription, error) {
	var subs []*canonical.UserSubscription

	err := db.QueryRows(ctx, cfg,
		"SELECT u.id, u.plan_id, u.group_id, u.u, u.d, u.transfer_enable, u.expired_at, u.token, u.uuid, "+
			"(SELECT o.id FROM v2_order o WHERE o.user_id = u.id AND o.status IN (1,3) ORDER BY o.id DESC LIMIT 1) AS recent_order_id "+
			"FROM v2_user u WHERE u.plan_id > 0 ORDER BY u.id",
		func(rows *sql.Rows) error {
			s, err := scanSubscription(rows)
			if err != nil {
				return err
			}
			subs = append(subs, s)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取订阅数据失败: %w", err)
	}
	return subs, nil
}

// scanSubscription 扫描单行 v2_user 订阅信息 → canonical.UserSubscription。
func scanSubscription(rows *sql.Rows) (*canonical.UserSubscription, error) {
	var (
		userID        int64
		planID        sql.NullInt64
		groupID       sql.NullInt64
		upload        sql.NullInt64 // u 字段
		download      sql.NullInt64 // d 字段
		transferLimit sql.NullInt64
		expiredAt     sql.NullInt64
		token         sql.NullString
		uuid          sql.NullString
		recentOrderID sql.NullInt64 // 最近完成订单 ID（关联用）
	)
	if err := rows.Scan(&userID, &planID, &groupID, &upload, &download, &transferLimit, &expiredAt, &token, &uuid, &recentOrderID); err != nil {
		return nil, err
	}

	// expired_at 处理（方案 6.3.1）：
	//   0 → 永久（ExpireTime=nil，writer 转 Unix 0）
	//   >0 → 具体时间
	var expireTime *time.Time
	if expiredAt.Int64 > 0 {
		t := unixToTime(expiredAt.Int64)
		expireTime = &t
	}

	// status 推断：未过期 → Active(1)，已过期 → Expired(3)。
	status := 1 // Active
	if expiredAt.Int64 > 0 && expiredAt.Int64 <= nowUnix() {
		status = 3 // Expired
	}

	// StartTime：v2board 无订阅开始时间，用合理近似：
	//   有过期时间 → 过期前 30 天作为开始
	//   永久订阅 → 当前时间前 30 天
	var startTime time.Time
	if expiredAt.Int64 > 0 {
		startTime = time.Unix(expiredAt.Int64-86400*30, 0)
	} else {
		startTime = time.Unix(nowUnix()-86400*30, 0)
	}

	return &canonical.UserSubscription{
		SourceID:      userID, // 源端用 user id 作为订阅标识
		UserSourceID:  userID,
		PlanSourceID:  planID.Int64,
		OrderSourceID: recentOrderID.Int64, // 关联最近完成订单
		GroupSourceID: groupID.Int64,
		Token:         token.String,
		UUID:          uuid.String,
		StartTime:     startTime,
		ExpireTime:    expireTime,
		TrafficBytes:  transferLimit.Int64,
		UploadBytes:   upload.Int64,
		DownloadBytes: download.Int64,
		Status:        status,
	}, nil
}

// nowUnix 当前 Unix 秒（避免每次调用 time.Now）。
func nowUnix() int64 {
	return time.Now().Unix()
}
