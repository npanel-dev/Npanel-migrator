// Package detector 探测源端数据库属于哪个面板类型。
//
// 探测策略基于"特征表"——每个面板有一组独特表名组合：
//
//	xiaov2board: v2_user + v2_server_v2node（独有的聚合节点表）
//	v2board:     v2_user + 无 v2_server_v2node（比 xiao 少扩展表）
//	Xboard:      v2_server（聚合节点表）+ v2_server_machine
//	PPanel:      servers + nodes + user（无 v2_ 前缀，Go 风格表名）
//	SSPanel:     user + product + node（有 product 商品表，无 v2_ 前缀）
//	NPanel:      user + subscribe + subscribe_price_option（ent 产物）
//
// 探测顺序按"特征越鲜明越优先"，避免误判（如 xiaov2board 必须先于 v2board 判定）。
package detector

import (
	"context"
	"fmt"

	"npanel-migrator/internal/data/db"
)

// PanelType 面板类型。
type PanelType string

const (
	PanelXiaoV2board PanelType = "xiaov2board"
	PanelV2board     PanelType = "v2board"
	PanelXboard      PanelType = "xboard"
	PanelPPanel      PanelType = "ppanel"
	PanelSSPanel     PanelType = "sspanel"
	PanelNPanel      PanelType = "npanel"
	PanelUnknown     PanelType = "unknown"
)

// Result 是探测结果。
type Result struct {
	// Panel 识别出的面板类型。
	Panel PanelType `json:"panel"`
	// Confidence 识别置信度描述（人类可读）。
	Confidence string `json:"confidence"`
	// MatchedTables 命中的特征表。
	MatchedTables []string `json:"matchedTables"`
}

// 特征表集合（一次查询拿回所有表存在性，再在内存里判定）。
var probeTables = []string{
	// v2board 系
	"v2_user",
	"v2_server_v2node", // xiaov2board 独有
	// Xboard
	"v2_server",
	"v2_server_machine",
	// PPanel / NPanel（Go 风格，无前缀）
	"user",
	"servers",
	"nodes",
	"subscribe",
	"subscribe_price_option",
	// SSPanel
	"product",
	"node",
	"link",
}

// Detect 探测源端数据库的面板类型。
// cfg 为数据库连接配置，declared 为前端声明的面板类型（用于校验一致性）。
func Detect(ctx context.Context, cfg db.Config, declared string) (*Result, error) {
	found, err := db.TableExistsBatch(ctx, cfg, probeTables)
	if err != nil {
		return nil, fmt.Errorf("查询表结构失败: %w", err)
	}
	return DetectFromTables(found, declared), nil
}

// DetectFromTables 根据已知的"表存在性 map"判定面板类型（纯逻辑，便于单元测试）。
// found 的 key 是表名，value 表示是否存在。
func DetectFromTables(found map[string]bool, declared string) *Result {
	has := func(t string) bool { return found[t] }

	r := &Result{}

	// 按特征鲜明度从高到低判定。
	switch {
	// 1. xiaov2board：v2_user + v2_server_v2node（独有聚合表）
	case has("v2_user") && has("v2_server_v2node"):
		r.Panel = PanelXiaoV2board
		r.Confidence = "高（命中 v2_server_v2node 独有聚合表）"
		r.MatchedTables = matched(found, "v2_user", "v2_server_v2node")

	// 2. Xboard：v2_server 聚合表（v2board 系没有）
	case has("v2_server"):
		r.Panel = PanelXboard
		r.Confidence = "高（命中 v2_server 聚合节点表）"
		if has("v2_server_machine") {
			r.Confidence += "，且命中 v2_server_machine"
		}
		r.MatchedTables = matched(found, "v2_server", "v2_server_machine")

	// 3. v2board（普通版）：v2_user 但无 v2_server 聚合表、无 v2_server_v2node
	case has("v2_user"):
		r.Panel = PanelV2board
		r.Confidence = "中（有 v2_user 但无 v2_server 聚合表；可能是普通 v2board）"
		r.MatchedTables = matched(found, "v2_user")

	// 4. PPanel：servers + nodes（Go 风格，且有 user 但无 product）
	case has("servers") && has("nodes"):
		r.Panel = PanelPPanel
		r.Confidence = "高（命中 servers + nodes，Go 风格表名）"
		r.MatchedTables = matched(found, "servers", "nodes", "user")

	// 5. NPanel：user + subscribe + subscribe_price_option（ent 产物）
	case has("user") && has("subscribe") && has("subscribe_price_option"):
		r.Panel = PanelNPanel
		r.Confidence = "高（命中 subscribe + subscribe_price_option，NPanel ent 产物）"
		r.MatchedTables = matched(found, "user", "subscribe", "subscribe_price_option")

	// 6. SSPanel：user + product（有商品表的只有 SSPanel）
	case has("user") && has("product"):
		r.Panel = PanelSSPanel
		r.Confidence = "高（命中 product 商品表）"
		r.MatchedTables = matched(found, "user", "product", "node", "link")

	default:
		r.Panel = PanelUnknown
		r.Confidence = "低（未命中任何已知面板特征表）"
		r.MatchedTables = []string{}
	}

	// 若前端声明了面板类型，与探测结果比对。
	if declared != "" && PanelType(declared) != r.Panel {
		r.Confidence = fmt.Sprintf("⚠️ 前端声明为 %s，但探测结果为 %s。%s",
			declared, r.Panel, r.Confidence)
	}

	return r
}

// IsNPanel 探测目标库是否为 NPanel（目标端校验用）。
func IsNPanel(ctx context.Context, cfg db.Config) (bool, error) {
	found, err := db.TableExistsBatch(ctx, cfg, []string{
		"user", "subscribe", "subscribe_price_option", "order", "user_subscribe",
	})
	if err != nil {
		return false, err
	}
	// NPanel 核心表必须齐全。
	return found["user"] && found["subscribe"] && found["order"] && found["user_subscribe"], nil
}

// matched 从 found map 里挑出实际命中的表名。
func matched(found map[string]bool, tables ...string) []string {
	out := make([]string, 0, len(tables))
	for _, t := range tables {
		if found[t] {
			out = append(out, t)
		}
	}
	return out
}
