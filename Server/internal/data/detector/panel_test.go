package detector

import (
	"testing"
)

// set 构造一个表存在性 map（未列出的表视为不存在）。
func set(tables ...string) map[string]bool {
	m := make(map[string]bool, len(tables))
	for _, t := range tables {
		m[t] = true
	}
	return m
}

func TestDetectFromTables(t *testing.T) {
	tests := []struct {
		name     string
		found    map[string]bool
		declared string
		want     PanelType
	}{
		{
			name:  "xiaov2board: v2_user + v2_server_v2node",
			found: set("v2_user", "v2_server_v2node", "v2_order"),
			want:  PanelXiaoV2board,
		},
		{
			name:  "v2board 普通版: 仅 v2_user, 无 v2_server 聚合表",
			found: set("v2_user", "v2_order", "v2_plan"),
			want:  PanelV2board,
		},
		{
			name:  "Xboard: v2_server 聚合表 + machine",
			found: set("v2_user", "v2_server", "v2_server_machine"),
			want:  PanelXboard,
		},
		{
			name:  "PPanel: servers + nodes (Go 风格)",
			found: set("user", "servers", "nodes", "subscribe"),
			want:  PanelPPanel,
		},
		{
			name:  "NPanel: user + subscribe + subscribe_price_option",
			found: set("user", "subscribe", "subscribe_price_option", "order", "user_subscribe"),
			want:  PanelNPanel,
		},
		{
			name:  "SSPanel: user + product + node",
			found: set("user", "product", "node", "link", "order"),
			want:  PanelSSPanel,
		},
		{
			name:  "未知: 无任何特征表",
			found: set("foo", "bar"),
			want:  PanelUnknown,
		},
		{
			name:     "声明与探测不一致时应标注警告",
			found:    set("v2_user", "v2_server_v2node"),
			declared: "v2board", // 声明 v2board 但探测到 xiaov2board
			want:     PanelXiaoV2board,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := DetectFromTables(tt.found, tt.declared)
			if r.Panel != tt.want {
				t.Errorf("panel = %q, want %q (confidence: %s)", r.Panel, tt.want, r.Confidence)
			}
		})
	}
}

// TestDetectPriority 验证判定优先级：
// xiaov2board 必须先于 v2board（都有 v2_user，但 xiao 多 v2_server_v2node）。
func TestDetectPriority(t *testing.T) {
	// 同时有 v2_user 和 v2_server_v2node → 必须判定为 xiaov2board，不能是 v2board。
	r := DetectFromTables(set("v2_user", "v2_server_v2node"), "")
	if r.Panel != PanelXiaoV2board {
		t.Errorf("优先级错误: 有 v2_server_v2node 时应为 xiaov2board, 得到 %q", r.Panel)
	}
}

// TestIsNPanelLogic 验证 NPanel 识别的核心条件（通过 DetectFromTables 间接覆盖）。
func TestNPanelDetection(t *testing.T) {
	// 缺 subscribe_price_option 时不应误判为 NPanel。
	r := DetectFromTables(set("user", "subscribe", "order"), "")
	if r.Panel == PanelNPanel {
		t.Error("缺少 subscribe_price_option 时不应判定为 NPanel")
	}
	// 有 product 优先判 SSPanel 而非其他。
	r = DetectFromTables(set("user", "product"), "")
	if r.Panel != PanelSSPanel {
		t.Errorf("user+product 应为 SSPanel, 得到 %q", r.Panel)
	}
}
