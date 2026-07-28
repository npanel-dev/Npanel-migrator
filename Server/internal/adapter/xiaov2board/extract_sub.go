package xiaov2board

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// ExtractSubscriptions 从 v2_user 读取当前订阅信息。
// 非封禁用户若无有效订阅，会返回 NeedsTrial=true，由 writer 分配目标体验套餐。
func ExtractSubscriptions(ctx context.Context, cfg db.Config) ([]*canonical.UserSubscription, error) {
	return ExtractSubscriptionsAt(ctx, cfg, time.Now())
}

// ExtractSubscriptionsAt 使用固定评估时间，确保中断恢复后体验套餐判定不漂移。
func ExtractSubscriptionsAt(
	ctx context.Context,
	cfg db.Config,
	evaluationTime time.Time,
) ([]*canonical.UserSubscription, error) {
	pool, err := db.OpenPool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	return ExtractSubscriptionsAtPoolUntil(
		ctx, pool, cfg.Database, evaluationTime, math.MaxInt64, math.MaxInt64,
	)
}

// ExtractSubscriptionsAtPool reuses the migration source pool for both the
// index capability check and the large subscription query.
func ExtractSubscriptionsAtPool(
	ctx context.Context,
	pool *sql.DB,
	database string,
	evaluationTime time.Time,
) ([]*canonical.UserSubscription, error) {
	return ExtractSubscriptionsAtPoolUntil(
		ctx, pool, database, evaluationTime, math.MaxInt64, math.MaxInt64,
	)
}

// ExtractSubscriptionsAtPoolUntil reads a stable migration snapshot bounded by
// the user and order high-water marks captured when the task was created.
func ExtractSubscriptionsAtPoolUntil(
	ctx context.Context,
	pool *sql.DB,
	database string,
	evaluationTime time.Time,
	userHighWater, orderHighWater int64,
) ([]*canonical.UserSubscription, error) {
	var subs []*canonical.UserSubscription

	hasFastIndex, err := db.TableHasIndexPrefixDB(
		ctx, pool, database, "v2_order", fastOrderLookupIndexColumns,
	)
	if err != nil {
		return nil, fmt.Errorf("检查 v2_order 查询索引失败: %w", err)
	}
	query := fmt.Sprintf(
		`SELECT u.id, u.plan_id, u.group_id, u.u, u.d, u.transfer_enable,
		        u.expired_at, u.token, u.uuid,
		        o.id, o.period, o.paid_at, o.created_at
		   FROM v2_user u
		   %s
		  WHERE u.banned = 0
		    AND u.id <= ?
		  ORDER BY u.id`,
		latestRelevantOrderJoinUntilSQL(hasFastIndex),
	)
	err = db.QueryRowsDBWithTimeout(
		ctx,
		pool,
		sourceOrderLookupTimeout,
		query,
		func(rows *sql.Rows) error {
			s, err := scanSubscriptionAt(rows, evaluationTime.Unix())
			if err != nil {
				return err
			}
			subs = append(subs, s)
			return nil
		},
		orderHighWater,
		userHighWater,
	)
	if err != nil {
		return nil, fmt.Errorf("读取订阅数据失败: %w", err)
	}
	return subs, nil
}

// scanSubscription 扫描单行 v2_user 订阅信息 → canonical.UserSubscription。
func scanSubscription(rows *sql.Rows) (*canonical.UserSubscription, error) {
	return scanSubscriptionAt(rows, nowUnix())
}

func scanSubscriptionAt(rows *sql.Rows, evaluationUnix int64) (*canonical.UserSubscription, error) {
	var (
		userID         int64
		planID         sql.NullInt64
		groupID        sql.NullInt64
		upload         sql.NullInt64 // u 字段
		download       sql.NullInt64 // d 字段
		transferLimit  sql.NullInt64
		expiredAt      sql.NullInt64
		token          sql.NullString
		uuid           sql.NullString
		recentOrderID  sql.NullInt64 // 最近完成订单 ID（关联用）
		recentPeriod   sql.NullString
		orderPaidAt    sql.NullInt64
		orderCreatedAt sql.NullInt64
	)
	if err := rows.Scan(
		&userID, &planID, &groupID, &upload, &download, &transferLimit,
		&expiredAt, &token, &uuid, &recentOrderID, &recentPeriod, &orderPaidAt, &orderCreatedAt,
	); err != nil {
		return nil, err
	}

	// xiaov2board 的有效订阅判定：
	// plan_id>0、流量>0，且 expired_at 为 NULL（不限时）或晚于当前时间。
	// 特别注意 expired_at=0 是已失效，不是永久。
	needsTrial := subscriptionNeedsTrial(
		planID.Valid, planID.Int64, transferLimit.Int64,
		expiredAt.Valid, expiredAt.Int64, evaluationUnix,
	)

	var expireTime *time.Time
	if !needsTrial && expiredAt.Valid && expiredAt.Int64 > 0 {
		t := unixToTime(expiredAt.Int64)
		expireTime = &t
	}

	// 优先使用最近一次对应套餐订单的支付/创建时间。
	startUnix := orderPaidAt.Int64
	if startUnix <= 0 {
		startUnix = orderCreatedAt.Int64
	}
	if startUnix <= 0 {
		startUnix = evaluationUnix
	}
	startTime := unixToTime(startUnix)

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
		Status:        1,
		NeedsTrial:    needsTrial,
		SourcePeriod:  recentPeriod.String,
	}, nil
}

func subscriptionNeedsTrial(
	planValid bool,
	planID int64,
	traffic int64,
	expiredValid bool,
	expiredAt int64,
	now int64,
) bool {
	if !planValid || planID <= 0 || traffic <= 0 {
		return true
	}
	if !expiredValid {
		return false
	}
	return expiredAt <= now
}

// nowUnix 当前 Unix 秒（避免每次调用 time.Now）。
func nowUnix() int64 {
	return time.Now().Unix()
}
