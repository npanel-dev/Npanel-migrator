// adapter_dispatch.go 面板适配器统一调度层。
//
// 现有 adapter 是"包级函数 + 各自的报告结构体"，没有 Go 接口。
// 这里用函数字段组成的调度结构把 xiaov2board（v2board 家族）与 sspanelv3
// 归一化到同一组签名，service/import/plan-mapping 只面向 sourceAdapter 编程。
package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"npanel-migrator/internal/adapter/sspanelv3"
	"npanel-migrator/internal/adapter/xiaov2board"
	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
	"npanel-migrator/internal/data/detector"
)

// SourceOptions 源面板级迁移选项（由请求 DTO 填充）。
type SourceOptions struct {
	// PwdMethod 源面板全局密码算法（sspanelv3 用）。
	PwdMethod string
	// Salt 源面板全局密码盐（sspanelv3 用）。
	Salt string
}

// dryRunIssue 与 adapter 无关的预演问题（中性表示）。
type dryRunIssue struct {
	Severity string
	Category string
	Message  string
	Count    int64
}

// dryRunOutcome 中性预演结果。
type dryRunOutcome struct {
	Panel        string
	Issues       []dryRunIssue
	ErrorCount   int
	WarningCount int
	InfoCount    int
	CanProceed   bool
}

// sourcePlanStats 套餐映射界面需要的源端统计（各面板口径不同）。
type sourcePlanStats struct {
	// ActiveUserCounts 源套餐 → 当前有效用户数。
	ActiveUserCounts map[int64]int64
	// ActivePeriodCounts "planID:period" → 有效用户数（v2board 多周期才有）。
	ActivePeriodCounts map[string]int64
	// OrderCounts 源套餐 → 历史订单数。
	OrderCounts map[int64]int64
	// OrderPeriodCounts "planID:period" → 历史订单数。
	OrderPeriodCounts map[string]int64
	// InactiveUserCount 无有效订阅的用户数（体验套餐候选）。
	InactiveUserCount int64
	// KnownPeriods 源端已知周期键（仅有活跃用户/订单但套餐已删除时兜底显示）。
	KnownPeriods []SourcePlanOption
	// OrderLookupIndexed 源库订单查询是否有可用索引（性能提示）。
	OrderLookupIndexed bool
}

// sourceAdapter 面板适配器的统一调度结构。
type sourceAdapter struct {
	Panel detector.PanelType

	Detect  func(ctx context.Context, cfg db.Config) (*DetectData, error)
	DryRun  func(ctx context.Context, cfg db.Config, opts SourceOptions) (*dryRunOutcome, error)
	Extract func(ctx context.Context, cfg db.Config) extractors

	// CountUsers/CountOrders/HighWaters 供断点分页与任务准备。
	CountUsers  func(ctx context.Context, pool *sql.DB, highWater int64) (int64, error)
	CountOrders func(ctx context.Context, pool *sql.DB, highWater int64) (int64, error)
	HighWaters  func(ctx context.Context, pool *sql.DB) (userHW, orderHW int64, err error)

	// PlanStats 供套餐映射界面（GetPlanMappingOptions）。
	PlanStats func(ctx context.Context, cfg db.Config) (*sourcePlanStats, error)
}

// extractors 批量抽取函数集（keyset 分页契约与 xiaov2board 一致）。
type extractors struct {
	UsersKeyset func(ctx context.Context, pool *sql.DB, opts SourceOptions,
		batchSize int, afterID, highWater int64,
		onBatch func([]*canonical.User) error) error
	OrdersKeyset func(ctx context.Context, pool *sql.DB,
		batchSize int, afterID, highWater int64,
		onBatch func([]*canonical.Order) error) error
	Subscriptions func(ctx context.Context, pool *sql.DB,
		evaluationTime time.Time, userHW, orderHW int64,
	) ([]*canonical.UserSubscription, error)
	NodeGroups func(ctx context.Context, cfg db.Config) ([]*canonical.NodeGroup, error)
	Nodes      func(ctx context.Context, cfg db.Config) ([]*canonical.ServerNode, error)
	Coupons    func(ctx context.Context, cfg db.Config) ([]*canonical.Coupon, error)
	Notices    func(ctx context.Context, cfg db.Config) ([]*canonical.Notice, error)
	Tickets    func(ctx context.Context, cfg db.Config) ([]*canonical.Ticket, error)
	Plans      func(ctx context.Context, cfg db.Config) ([]*canonical.Plan, error)
}

// adapterFor 按面板类型返回调度器。未支持的面板返回错误。
func adapterFor(panel detector.PanelType) (*sourceAdapter, error) {
	switch panel {
	case detector.PanelXiaoV2board, detector.PanelV2board:
		return v2boardFamilyAdapter(panel), nil
	case detector.PanelSSPanelV3:
		return sspanelV3Adapter(), nil
	default:
		return nil, fmt.Errorf("已识别为 %s 面板，但该 adapter 暂未实现（当前支持 xiaov2board/v2board/sspanelv3）", panel)
	}
}

// isAdapterSupported 面板是否有可用 adapter。
func isAdapterSupported(panel detector.PanelType) bool {
	_, err := adapterFor(panel)
	return err == nil
}

// ---- v2board 家族包装 ----

func v2boardFamilyAdapter(panel detector.PanelType) *sourceAdapter {
	return &sourceAdapter{
		Panel: panel,
		Detect: func(ctx context.Context, cfg db.Config) (*DetectData, error) {
			report, err := xiaov2board.Detect(ctx, cfg)
			if err != nil {
				return nil, err
			}
			report.Panel = string(panel)
			return convertReport(report), nil
		},
		DryRun: func(ctx context.Context, cfg db.Config, _ SourceOptions) (*dryRunOutcome, error) {
			report, err := xiaov2board.DryRun(ctx, cfg)
			if err != nil {
				return nil, err
			}
			report.Panel = string(panel)
			return v2boardDryRunOutcome(report), nil
		},
		Extract: func(ctx context.Context, cfg db.Config) extractors {
			return extractors{
				UsersKeyset: func(ctx context.Context, pool *sql.DB, _ SourceOptions,
					batchSize int, afterID, highWater int64,
					onBatch func([]*canonical.User) error) error {
					return xiaov2board.ExtractUsersKeyset(ctx, pool, batchSize, afterID, highWater, onBatch)
				},
				OrdersKeyset: xiaov2board.ExtractOrdersKeyset,
				Subscriptions: func(ctx context.Context, pool *sql.DB,
					evaluationTime time.Time, userHW, orderHW int64,
				) ([]*canonical.UserSubscription, error) {
					// v2board 家族需要库名做索引探测。
					return xiaov2board.ExtractSubscriptionsAtPoolUntil(
						ctx, pool, cfg.Database, evaluationTime, userHW, orderHW)
				},
				NodeGroups: xiaov2board.ExtractNodeGroups,
				Nodes:      xiaov2board.ExtractNodes,
				Coupons:    xiaov2board.ExtractCoupons,
				Notices:    xiaov2board.ExtractNotices,
				Tickets:    xiaov2board.ExtractTickets,
				Plans:      xiaov2board.ExtractPlans,
			}
		},
		CountUsers: func(ctx context.Context, pool *sql.DB, highWater int64) (int64, error) {
			return db.QueryScalarDBWithTimeout(ctx, pool, 30*time.Second,
				"SELECT COUNT(*) FROM v2_user WHERE id <= ?", highWater)
		},
		CountOrders: func(ctx context.Context, pool *sql.DB, highWater int64) (int64, error) {
			return db.QueryScalarDBWithTimeout(ctx, pool, 30*time.Second,
				"SELECT COUNT(*) FROM v2_order WHERE id <= ?", highWater)
		},
		HighWaters: v2boardHighWaters,
		PlanStats:  v2boardPlanStats,
	}
}

func v2boardHighWaters(ctx context.Context, pool *sql.DB) (int64, int64, error) {
	userHW, err := db.QueryScalarDBWithTimeout(ctx, pool, 30*time.Second,
		"SELECT COALESCE(MAX(id), 0) FROM v2_user")
	if err != nil {
		return 0, 0, err
	}
	orderHW, err := db.QueryScalarDBWithTimeout(ctx, pool, 30*time.Second,
		"SELECT COALESCE(MAX(id), 0) FROM v2_order")
	if err != nil {
		return 0, 0, err
	}
	return userHW, orderHW, nil
}

func v2boardDryRunOutcome(r *xiaov2board.DryRunReport) *dryRunOutcome {
	out := &dryRunOutcome{
		Panel:        r.Panel,
		ErrorCount:   r.Summary.ErrorCount,
		WarningCount: r.Summary.WarningCount,
		InfoCount:    r.Summary.InfoCount,
		CanProceed:   r.Summary.CanProceed,
	}
	for _, iss := range r.Issues {
		out.Issues = append(out.Issues, dryRunIssue{
			Severity: string(iss.Severity), Category: iss.Category,
			Message: iss.Message, Count: iss.Count,
		})
	}
	return out
}

func v2boardPlanStats(ctx context.Context, cfg db.Config) (*sourcePlanStats, error) {
	stats := &sourcePlanStats{
		ActiveUserCounts:   make(map[int64]int64),
		ActivePeriodCounts: make(map[string]int64),
		OrderCounts:        make(map[int64]int64),
		OrderPeriodCounts:  make(map[string]int64),
		KnownPeriods:       knownSourcePeriods(),
	}
	if err := db.QueryRowsWithTimeout(ctx, cfg, planMappingQueryTimeout,
		`SELECT plan_id, COUNT(*)
		   FROM v2_user
		  WHERE banned = 0
		    AND COALESCE(plan_id, 0) > 0
		    AND COALESCE(transfer_enable, 0) > 0
		    AND (expired_at IS NULL OR expired_at > UNIX_TIMESTAMP())
		  GROUP BY plan_id`,
		func(rows *sql.Rows) error {
			var planID, count int64
			if err := rows.Scan(&planID, &count); err != nil {
				return err
			}
			stats.ActiveUserCounts[planID] = count
			return nil
		}); err != nil {
		return nil, fmt.Errorf("统计源套餐有效用户失败: %w", err)
	}
	var err error
	stats.ActivePeriodCounts, stats.OrderLookupIndexed, err = xiaov2board.ActiveSubscriptionPeriodCounts(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("统计源套餐周期有效用户失败: %w", err)
	}
	if err := db.QueryRowsWithTimeout(ctx, cfg, planMappingQueryTimeout,
		`SELECT plan_id, period, COUNT(*)
		   FROM v2_order
		  WHERE COALESCE(plan_id, 0) > 0
		    AND period <> 'deposit'
		  GROUP BY plan_id, period`,
		func(rows *sql.Rows) error {
			var planID, count int64
			var period sql.NullString
			if err := rows.Scan(&planID, &period, &count); err != nil {
				return err
			}
			stats.OrderCounts[planID] += count
			if period.Valid && period.String != "" {
				stats.OrderPeriodCounts[fmt.Sprintf("%d:%s", planID, period.String)] = count
			}
			return nil
		}); err != nil {
		return nil, fmt.Errorf("统计源套餐历史订单失败: %w", err)
	}
	stats.InactiveUserCount, err = db.QueryScalarWithTimeout(ctx, cfg, planMappingQueryTimeout,
		`SELECT COUNT(*) FROM v2_user
		  WHERE banned = 0
		    AND NOT (
		      COALESCE(plan_id, 0) > 0
		      AND COALESCE(transfer_enable, 0) > 0
		      AND (expired_at IS NULL OR expired_at > UNIX_TIMESTAMP())
		    )`)
	if err != nil {
		return nil, fmt.Errorf("统计无有效订阅用户失败: %w", err)
	}
	return stats, nil
}

// ---- sspanelv3 ----

func sspanelV3Adapter() *sourceAdapter {
	return &sourceAdapter{
		Panel: detector.PanelSSPanelV3,
		Detect: func(ctx context.Context, cfg db.Config) (*DetectData, error) {
			report, err := sspanelv3.Detect(ctx, cfg)
			if err != nil {
				return nil, err
			}
			report.Panel = string(detector.PanelSSPanelV3)
			return convertSSPanelV3Report(report), nil
		},
		DryRun: func(ctx context.Context, cfg db.Config, opts SourceOptions) (*dryRunOutcome, error) {
			report, err := sspanelv3.DryRun(ctx, cfg, sspanelv3.Options{
				PwdMethod: opts.PwdMethod, Salt: opts.Salt,
			})
			if err != nil {
				return nil, err
			}
			out := &dryRunOutcome{
				Panel:        report.Panel,
				ErrorCount:   report.Summary.ErrorCount,
				WarningCount: report.Summary.WarningCount,
				InfoCount:    report.Summary.InfoCount,
				CanProceed:   report.Summary.CanProceed,
			}
			for _, iss := range report.Issues {
				out.Issues = append(out.Issues, dryRunIssue{
					Severity: string(iss.Severity), Category: iss.Category,
					Message: iss.Message, Count: iss.Count,
				})
			}
			return out, nil
		},
		Extract: func(ctx context.Context, cfg db.Config) extractors {
			return extractors{
				UsersKeyset: func(ctx context.Context, pool *sql.DB, opts SourceOptions,
					batchSize int, afterID, highWater int64,
					onBatch func([]*canonical.User) error) error {
					return sspanelv3.ExtractUsersKeyset(ctx, pool,
						sspanelv3.Options{PwdMethod: opts.PwdMethod, Salt: opts.Salt},
						batchSize, afterID, highWater, onBatch)
				},
				OrdersKeyset:  sspanelv3.ExtractOrdersKeyset,
				Subscriptions: sspanelv3.ExtractSubscriptionsAtPoolUntil,
				NodeGroups:    sspanelv3.ExtractNodeGroups,
				Nodes:         sspanelv3.ExtractNodes,
				Coupons:       sspanelv3.ExtractCoupons,
				Notices:       sspanelv3.ExtractNotices,
				Tickets:       sspanelv3.ExtractTickets,
				Plans:         sspanelv3.ExtractPlans,
			}
		},
		CountUsers: func(ctx context.Context, pool *sql.DB, highWater int64) (int64, error) {
			return db.QueryScalarDBWithTimeout(ctx, pool, 30*time.Second,
				"SELECT COUNT(*) FROM user WHERE id <= ?", highWater)
		},
		CountOrders: func(ctx context.Context, pool *sql.DB, highWater int64) (int64, error) {
			// bought + 已用充值码（合成游标口径）。
			bought, err := db.QueryScalarDBWithTimeout(ctx, pool, 30*time.Second,
				"SELECT COUNT(*) FROM bought WHERE id <= ?", highWater)
			if err != nil {
				return 0, err
			}
			code, err := db.QueryScalarDBWithTimeout(ctx, pool, 30*time.Second,
				"SELECT COUNT(*) FROM code WHERE isused = 1 AND id + ? <= ?",
				sspanelv3.CodeIDOffset, highWater)
			if err != nil {
				return 0, err
			}
			return bought + code, nil
		},
		HighWaters: sspanelV3HighWaters,
		PlanStats:  sspanelV3PlanStats,
	}
}

func sspanelV3HighWaters(ctx context.Context, pool *sql.DB) (int64, int64, error) {
	userHW, err := sspanelv3.UserHighWater(ctx, pool)
	if err != nil {
		return 0, 0, err
	}
	orderHW, err := sspanelv3.OrderHighWater(ctx, pool)
	if err != nil {
		return 0, 0, err
	}
	return userHW, orderHW, nil
}

func sspanelV3PlanStats(ctx context.Context, cfg db.Config) (*sourcePlanStats, error) {
	stats := &sourcePlanStats{
		ActiveUserCounts:   make(map[int64]int64),
		ActivePeriodCounts: make(map[string]int64),
		OrderCounts:        make(map[int64]int64),
		OrderPeriodCounts:  make(map[string]int64),
		KnownPeriods:       nil, // 一商品一价，无周期兜底
	}
	// 有效用户按「最近一次购买的商品」归类（与订阅推导口径一致）。
	if err := db.QueryRowsWithTimeout(ctx, cfg, planMappingQueryTimeout,
		`SELECT b.shopid, COUNT(*)
		   FROM (
		     SELECT userid, MAX(id) AS mid FROM bought GROUP BY userid
		   ) x
		   JOIN bought b ON b.id = x.mid
		   JOIN user u ON u.id = b.userid
		  WHERE u.enable = 1
		    AND u.class_expire > NOW()
		    AND u.expire_in > NOW()
		  GROUP BY b.shopid`,
		func(rows *sql.Rows) error {
			var shopID, count int64
			if err := rows.Scan(&shopID, &count); err != nil {
				return err
			}
			stats.ActiveUserCounts[shopID] = count
			return nil
		}); err != nil {
		return nil, fmt.Errorf("统计商品有效用户失败: %w", err)
	}
	if err := db.QueryRowsWithTimeout(ctx, cfg, planMappingQueryTimeout,
		`SELECT shopid, COUNT(*) FROM bought GROUP BY shopid`,
		func(rows *sql.Rows) error {
			var shopID, count int64
			if err := rows.Scan(&shopID, &count); err != nil {
				return err
			}
			stats.OrderCounts[shopID] = count
			return nil
		}); err != nil {
		return nil, fmt.Errorf("统计商品订单失败: %w", err)
	}
	var err error
	stats.InactiveUserCount, err = db.QueryScalarWithTimeout(ctx, cfg, planMappingQueryTimeout,
		`SELECT COUNT(*) FROM user
		  WHERE enable = 1
		    AND NOT (class > 0 AND class_expire > NOW() AND expire_in > NOW())`)
	if err != nil {
		return nil, fmt.Errorf("统计无有效订阅用户失败: %w", err)
	}
	return stats, nil
}

// convertSSPanelV3Report sspanelv3.DetectReport → service DTO（字段与 v2board 同构）。
func convertSSPanelV3Report(r *sspanelv3.DetectReport) *DetectData {
	d := &DetectData{
		Panel:     r.Panel,
		TotalRows: r.TotalRows,
		Metrics: DetectMetrics{
			UserTotal:         r.Metrics.UserTotal,
			UserActive:        r.Metrics.UserActive,
			UserBanned:        r.Metrics.UserBanned,
			BalanceTotal:      r.Metrics.BalanceTotal,
			ActiveSubscribers: r.Metrics.ActiveSubscribers,
			PlanTotal:         r.Metrics.PlanTotal,
			PlanOnSale:        r.Metrics.PlanOnSale,
			OrderTotal:        r.Metrics.OrderTotal,
			OrderCompleted:    r.Metrics.OrderCompleted,
			NodeTotal:         r.Metrics.NodeTotal,
			CouponEnable:      r.Metrics.CouponEnable,
			TicketOpen:        r.Metrics.TicketOpen,
		},
	}
	for _, t := range r.Tables {
		d.Tables = append(d.Tables, TableStat{
			Name: t.Name, Rows: t.Rows, Comment: t.Comment,
		})
	}
	return d
}
