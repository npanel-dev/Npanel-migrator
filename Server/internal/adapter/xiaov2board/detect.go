// Package xiaov2board 是 xiaov2board 面板的源端 adapter。
//
// detect 阶段（本文件）：连接成功后扫描源库表结构，统计各核心表行数
// 和关键业务指标（用户余额总和、有效订阅数、订单数等），生成迁移前报告。
// 对应迁移方案第 9 章"导入前报告"和第 3.1 节"识别"步骤。
//
// detect 只读不写，是迁移的第一步，用于评估数据量和发现潜在问题。
package xiaov2board

import (
	"context"
	"fmt"

	"npanel-migrator/internal/data/db"
)

// TableStats 单张表的统计信息。
type TableStats struct {
	// Name 表名。
	Name string `json:"name"`
	// Rows 行数。
	Rows int64 `json:"rows"`
	// Comment 业务含义说明（人类可读）。
	Comment string `json:"comment"`
}

// Metrics 关键业务指标汇总。
type Metrics struct {
	// UserTotal 用户总数。
	UserTotal int64 `json:"userTotal"`
	// UserActive 有效（未封禁）用户数。
	UserActive int64 `json:"userActive"`
	// UserBanned 封禁用户数。
	UserBanned int64 `json:"userBanned"`
	// BalanceTotal 用户余额总和（分）。
	BalanceTotal int64 `json:"balanceTotal"`
	// ActiveSubscribers 当前有有效套餐的用户数（plan_id > 0 且未过期）。
	ActiveSubscribers int64 `json:"activeSubscribers"`
	// PlanTotal 套餐总数。
	PlanTotal int64 `json:"planTotal"`
	// PlanOnSale 在售套餐数（show=1）。
	PlanOnSale int64 `json:"planOnSale"`
	// OrderTotal 订单总数。
	OrderTotal int64 `json:"orderTotal"`
	// OrderCompleted 已完成订单数（status=3）。
	OrderCompleted int64 `json:"orderCompleted"`
	// NodeTotal 节点总数（各协议表之和）。
	NodeTotal int64 `json:"nodeTotal"`
	// CouponEnable 有效优惠券数。
	CouponEnable int64 `json:"couponEnable"`
	// TicketOpen 未关闭工单数。
	TicketOpen int64 `json:"ticketOpen"`
}

// DetectReport detect 阶段的完整报告。
type DetectReport struct {
	// Panel 探测确认的面板类型。
	Panel string `json:"panel"`
	// Tables 各核心表的行数统计。
	Tables []TableStats `json:"tables"`
	// Metrics 关键业务指标。
	Metrics Metrics `json:"metrics"`
	// TotalRows 核心表总行数（迁移数据量粗估）。
	TotalRows int64 `json:"totalRows"`
}

// 节点协议表清单（xiaov2board 独有的扩展协议）。
var nodeTables = []string{
	"v2_server_vmess",
	"v2_server_trojan",
	"v2_server_shadowsocks",
	"v2_server_hysteria",
	"v2_server_vless",
	"v2_server_tuic",
	"v2_server_anytls",
	"v2_server_v2node",
}

// 核心表清单（detect 统计范围，不含日志表）。
var coreTables = []struct {
	Name    string
	Comment string
}{
	{"v2_user", "用户"},
	{"v2_plan", "套餐"},
	{"v2_order", "订单"},
	{"v2_server_group", "节点分组"},
	{"v2_server_vmess", "节点-VMess"},
	{"v2_server_trojan", "节点-Trojan"},
	{"v2_server_shadowsocks", "节点-Shadowsocks"},
	{"v2_server_hysteria", "节点-Hysteria"},
	{"v2_server_vless", "节点-VLESS"},
	{"v2_server_tuic", "节点-TUIC"},
	{"v2_server_anytls", "节点-AnyTLS"},
	{"v2_server_v2node", "节点-V2Node(聚合)"},
	{"v2_coupon", "优惠券"},
	{"v2_notice", "公告"},
	{"v2_ticket", "工单"},
	{"v2_ticket_message", "工单消息"},
	{"v2_payment", "支付方式"},
	{"v2_giftcard", "礼品卡"},
	{"v2_invite_code", "邀请码"},
}

// Detect 执行 detect 阶段，返回迁移前报告。
func Detect(ctx context.Context, cfg db.Config) (*DetectReport, error) {
	report := &DetectReport{Panel: "xiaov2board"}

	// 1. 统计各核心表行数。
	// 先确认表存在（部分 fork 可能缺表），缺表记 0 行并标注"缺失"。
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
			Name:    t.Name,
			Rows:    rows,
			Comment: t.Comment,
		})
		totalRows += rows
	}
	report.TotalRows = totalRows

	// 2. 统计关键业务指标。
	if err := collectMetrics(ctx, cfg, report); err != nil {
		return nil, err
	}

	return report, nil
}

// collectMetrics 收集关键业务指标。
func collectMetrics(ctx context.Context, cfg db.Config, report *DetectReport) error {
	// 用户指标。
	report.Metrics.UserTotal = mustCount(ctx, cfg, "v2_user", "1=1")
	report.Metrics.UserBanned = mustCount(ctx, cfg, "v2_user", "banned=1")
	report.Metrics.UserActive = report.Metrics.UserTotal - report.Metrics.UserBanned
	// 余额总和（分），SUM 对空表返回 NULL，由 QueryScalar 转 0。
	report.Metrics.BalanceTotal, _ = db.QueryScalar(ctx, cfg,
		"SELECT COALESCE(SUM(balance),0) FROM v2_user")

	// 有效订阅：plan_id > 0 且未过期（expired_at > 当前 Unix 时间戳，或 expired_at=0 永久）。
	// xiaov2board 的 expired_at 是 Unix 秒，0 表示一次性/永久。
	report.Metrics.ActiveSubscribers, _ = db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM v2_user WHERE plan_id > 0 AND (expired_at = 0 OR expired_at > UNIX_TIMESTAMP())")

	// 套餐。
	report.Metrics.PlanTotal = mustCount(ctx, cfg, "v2_plan", "1=1")
	report.Metrics.PlanOnSale = mustCount(ctx, cfg, "v2_plan", "`show`=1")

	// 订单。
	report.Metrics.OrderTotal = mustCount(ctx, cfg, "v2_order", "1=1")
	report.Metrics.OrderCompleted = mustCount(ctx, cfg, "v2_order", "status=3")

	// 节点（各协议表之和）。
	var nodeTotal int64
	for _, t := range nodeTables {
		nodeTotal += mustCount(ctx, cfg, t, "1=1")
	}
	report.Metrics.NodeTotal = nodeTotal

	// 优惠券（enable=1）。
	report.Metrics.CouponEnable = mustCount(ctx, cfg, "v2_coupon", "enable=1")
	// 未关闭工单（status=0 待处理）。
	report.Metrics.TicketOpen = mustCount(ctx, cfg, "v2_ticket", "status=0")

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

// tableNames 提取 coreTables 的纯表名列表。
func tableNames() []string {
	out := make([]string, 0, len(coreTables))
	for _, t := range coreTables {
		out = append(out, t.Name)
	}
	return out
}
