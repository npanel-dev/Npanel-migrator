package xiaov2board

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

const sourceOrderLookupTimeout = 5 * time.Minute

var fastOrderLookupIndexColumns = []string{"user_id", "plan_id", "id"}

// HasFastOrderLookupIndex 检查 v2_order 是否具备按用户、套餐查询最近订单的快路径索引。
// 该检查只读 information_schema。
func HasFastOrderLookupIndex(ctx context.Context, cfg db.Config) (bool, error) {
	return db.TableHasIndexPrefix(ctx, cfg, "v2_order", fastOrderLookupIndexColumns)
}

// ActiveSubscriptionPeriodCounts 统计当前有效用户最近一次套餐订单的周期。
// 有复合索引时使用按用户点查；缺少索引时改为一次聚合扫描，避免逐用户全表扫描。
func ActiveSubscriptionPeriodCounts(
	ctx context.Context,
	cfg db.Config,
) (map[string]int64, bool, error) {
	hasFastIndex, err := HasFastOrderLookupIndex(ctx, cfg)
	if err != nil {
		return nil, false, fmt.Errorf("检查 v2_order 查询索引失败: %w", err)
	}

	counts := make(map[string]int64)
	query := fmt.Sprintf(
		`SELECT u.plan_id, o.period, COUNT(*)
		   FROM v2_user u
		   %s
		  WHERE u.banned = 0
		    AND COALESCE(u.plan_id, 0) > 0
		    AND COALESCE(u.transfer_enable, 0) > 0
		    AND (u.expired_at IS NULL OR u.expired_at > UNIX_TIMESTAMP())
		  GROUP BY u.plan_id, o.period`,
		latestRelevantOrderJoinSQL(hasFastIndex),
	)
	err = db.QueryRowsWithTimeout(
		ctx,
		cfg,
		sourceOrderLookupTimeout,
		query,
		func(rows *sql.Rows) error {
			var planID, count int64
			var period sql.NullString
			if err := rows.Scan(&planID, &period, &count); err != nil {
				return err
			}
			if period.Valid && period.String != "" {
				counts[canonical.PriceOptionMapKey(planID, period.String)] = count
			}
			return nil
		},
	)
	if err != nil {
		return nil, hasFastIndex, err
	}
	return counts, hasFastIndex, nil
}

func latestRelevantOrderJoinSQL(hasFastIndex bool) string {
	if hasFastIndex {
		return `LEFT JOIN v2_order o ON o.id = (
		       SELECT o2.id
		         FROM v2_order o2
		        WHERE o2.user_id = u.id
		          AND o2.plan_id = u.plan_id
		          AND o2.status IN (1, 3)
		          AND o2.period NOT IN ('deposit', 'reset_price')
		        ORDER BY o2.id DESC
		        LIMIT 1
		   )`
	}

	return `LEFT JOIN (
		       SELECT selected.id, selected.user_id, selected.plan_id,
		              selected.period, selected.paid_at, selected.created_at
		         FROM v2_order selected
		         JOIN (
		               SELECT user_id, plan_id, MAX(id) AS id
		                 FROM v2_order
		                WHERE status IN (1, 3)
		                  AND period NOT IN ('deposit', 'reset_price')
		                GROUP BY user_id, plan_id
		         ) latest ON latest.id = selected.id
		   ) o ON o.user_id = u.id AND o.plan_id = u.plan_id`
}
