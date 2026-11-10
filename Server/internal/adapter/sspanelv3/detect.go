// sspanelv3 detect 阶段：扫描源库核心表行数与关键指标，输出迁移前报告。
// 只读不写。报告结构与 xiaov2board.DetectReport 同构，由 service 层统一转换。
package sspanelv3

import (
	"context"
	"fmt"

	"npanel-migrator/internal/data/db"
)

// TableStats 单张表的统计信息。
type TableStats struct {
	Name    string `json:"name"`
	Rows    int64  `json:"rows"`
	Comment string `json:"comment"`
}

// Metrics 关键业务指标汇总。
type Metrics struct {
	UserTotal         int64 `json:"userTotal"`
	UserActive        int64 `json:"userActive"`
	UserBanned        int64 `json:"userBanned"`
	BalanceTotal      int64 `json:"balanceTotal"`
	ActiveSubscribers int64 `json:"activeSubscribers"`
	PlanTotal         int64 `json:"planTotal"`
	PlanOnSale        int64 `json:"planOnSale"`
	OrderTotal        int64 `json:"orderTotal"`
	OrderCompleted    int64 `json:"orderCompleted"`
	NodeTotal         int64 `json:"nodeTotal"`
	CouponEnable      int64 `json:"couponEnable"`
	TicketOpen        int64 `json:"ticketOpen"`
}

// DetectReport detect 阶段的完整报告。
type DetectReport struct {
	Panel     string       `json:"panel"`
	Tables    []TableStats `json:"tables"`
	Metrics   Metrics      `json:"metrics"`
	TotalRows int64        `json:"totalRows"`
}

// 核心表清单（detect 统计范围，日志类表仅提示不迁移）。
var coreTables = []struct {
	Name    string
	Comment string
}{
	{"user", "用户"},
	{"shop", "商品（套餐）"},
	{"bought", "购买记录（→订单）"},
	{"code", "充值码（→充值订单）"},
	{"ss_node", "节点"},
	{"coupon", "优惠券"},
	{"announcement", "公告"},
	{"ticket", "工单（含回复）"},
	{"ss_invite_code", "邀请码（不迁移，重新生成）"},
	{"payback", "返利记录（不迁移）"},
	{"paylist", "支付网关流水（不迁移）"},
	{"link", "分享链接（不迁移，订阅 token 重新生成）"},
}

// Detect 执行 detect 阶段。
func Detect(ctx context.Context, cfg db.Config) (*DetectReport, error) {
	report := &DetectReport{Panel: "sspanelv3"}

	found, err := db.TableExistsBatch(ctx, cfg, tableNames())
	if err != nil {
		return nil, fmt.Errorf("查询表结构失败: %w", err)
	}

	var totalRows int64
	for _, t := range coreTables {
		var rows int64
		if found[t.Name] {
			rows, err = db.CountRows(ctx, cfg, t.Name)
			if err != nil {
				return nil, fmt.Errorf("统计 %s 失败: %w", t.Name, err)
			}
		}
		report.Tables = append(report.Tables, TableStats{
			Name: t.Name, Rows: rows, Comment: t.Comment,
		})
		totalRows += rows
	}
	report.TotalRows = totalRows

	if err := collectMetrics(ctx, cfg, report); err != nil {
		return nil, err
	}
	return report, nil
}

func collectMetrics(ctx context.Context, cfg db.Config, report *DetectReport) error {
	// 用户：enable=0 视为封禁。
	report.Metrics.UserTotal = mustCount(ctx, cfg, "user", "1=1")
	report.Metrics.UserBanned = mustCount(ctx, cfg, "user", "enable=0")
	report.Metrics.UserActive = report.Metrics.UserTotal - report.Metrics.UserBanned
	// 余额总和（money 元 → 分）。
	balanceYuan, _ := db.QueryScalar(ctx, cfg,
		"SELECT COALESCE(ROUND(SUM(money)*100), 0) FROM user")
	report.Metrics.BalanceTotal = balanceYuan

	// 有效订阅：enable=1 且等级与账号都未过期（1989 哨兵视为无效，见 domain.go）。
	report.Metrics.ActiveSubscribers, _ = db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM user WHERE enable=1 AND class > 0 "+
			"AND class_expire > '2000-01-01' AND class_expire > NOW() "+
			"AND expire_in > '2000-01-01' AND expire_in > NOW()")

	// 商品。
	report.Metrics.PlanTotal = mustCount(ctx, cfg, "shop", "1=1")
	report.Metrics.PlanOnSale = mustCount(ctx, cfg, "shop", "status=1")

	// 订单：bought + 已用充值码（与 extract_order 的口径一致）。
	report.Metrics.OrderTotal = mustCount(ctx, cfg, "bought", "1=1") +
		mustCount(ctx, cfg, "code", "isused=1")
	report.Metrics.OrderCompleted = report.Metrics.OrderTotal // 历史记录均视为已完成

	// 节点：排除 999（Radius 占位）与 9（单端口）后计入。
	report.Metrics.NodeTotal = mustCount(ctx, cfg, "ss_node",
		"sort NOT IN (9, 999)")

	report.Metrics.CouponEnable = mustCount(ctx, cfg, "coupon",
		"expire = 0 OR expire > UNIX_TIMESTAMP()")
	report.Metrics.TicketOpen = mustCount(ctx, cfg, "ticket",
		"rootid = 0 AND status = 1")

	return nil
}

// mustCount 带条件的 COUNT(*)，容错处理（表缺失返回 0）。
func mustCount(ctx context.Context, cfg db.Config, table, where string) int64 {
	n, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM `"+table+"` WHERE "+where)
	if err != nil {
		return 0
	}
	return n
}

func tableNames() []string {
	out := make([]string, 0, len(coreTables))
	for _, t := range coreTables {
		out = append(out, t.Name)
	}
	return out
}
