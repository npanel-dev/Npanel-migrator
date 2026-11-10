package sspanelv3

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// ExtractNodeGroups 由 ss_node.node_class（等级）与 user.node_group 合成节点分组。
// ss-panel v3 的节点权限模型是「等级」：node.node_class <= user.class 才可见。
// NPanel 用节点分组表达权限，因此按 class 值合成分组（class 本身作为分组 SourceID），
// 分组间用 1000+ 偏移区分 node_group 维度。
func ExtractNodeGroups(ctx context.Context, cfg db.Config) ([]*canonical.NodeGroup, error) {
	classes := make(map[int64]bool)
	err := db.QueryRows(ctx, cfg,
		"SELECT DISTINCT node_class FROM ss_node",
		func(rows *sql.Rows) error {
			var class sql.NullInt64
			if err := rows.Scan(&class); err != nil {
				return err
			}
			classes[class.Int64] = true
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取节点等级失败: %w", err)
	}
	groups := make(map[int64]bool)
	err = db.QueryRows(ctx, cfg,
		"SELECT DISTINCT node_group FROM user WHERE node_group > 0",
		func(rows *sql.Rows) error {
			var group sql.NullInt64
			if err := rows.Scan(&group); err != nil {
				return err
			}
			if group.Int64 > 0 {
				groups[group.Int64] = true
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取用户分组失败: %w", err)
	}

	now := time.Now().UTC()
	var result []*canonical.NodeGroup
	// 注意：不生成 class=0 基础组。MySQL 自增主键会把显式 id=0 改写为自增值，
	// 与真实分组撞车；且 writer 的 resolveNodeGroupIDs 本就跳过 sourceID<=0，
	// class=0 节点不带分组即"无限制"语义，无需建组。
	for class := range classes {
		if class <= 0 {
			continue
		}
		result = append(result, &canonical.NodeGroup{
			SourceID:  nodeGroupIDFromClass(class),
			Name:      fmt.Sprintf("等级 class %d", class),
			CreatedAt: now, UpdatedAt: now,
		})
	}
	for group := range groups {
		result = append(result, &canonical.NodeGroup{
			SourceID:  nodeGroupIDFromGroup(group),
			Name:      fmt.Sprintf("用户分组 %d", group),
			CreatedAt: now, UpdatedAt: now,
		})
	}
	return result, nil
}

// ExtractNodes 读取 ss_node → canonical.ServerNode。
// 跳过：sort=999（Radius/系统占位）、sort=9（单端口，NPanel 无等价物）。
// 协议映射（源码 URL.php）：0/10/13→shadowsocks，11/12→vmess，14→trojan。
// 注意：ss_node 无 port 列（每用户独享端口模型），Port 置 0，迁移后需在 NPanel 配置。
func ExtractNodes(ctx context.Context, cfg db.Config) ([]*canonical.ServerNode, error) {
	var nodes []*canonical.ServerNode
	err := db.QueryRows(ctx, cfg,
		`SELECT id, name, sort, server, method, status, type, node_class, node_group,
		        traffic_rate, node_speedlimit, custom_method, custom_rss, node_heartbeat
		   FROM ss_node ORDER BY id`,
		func(rows *sql.Rows) error {
			n, ok, err := scanNode(rows)
			if err != nil {
				return err
			}
			if ok {
				nodes = append(nodes, n)
			}
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 ss_node 失败: %w", err)
	}
	return nodes, nil
}

func scanNode(rows *sql.Rows) (*canonical.ServerNode, bool, error) {
	var (
		id           int64
		name         sql.NullString
		sort         sql.NullInt64
		server       sql.NullString
		method       sql.NullString
		status       sql.NullString
		nodeType     sql.NullInt64
		nodeClass    sql.NullInt64
		nodeGroup    sql.NullInt64
		trafficRate  sql.NullFloat64
		speedLimit   sql.NullFloat64
		customMethod sql.NullInt64
		customRSS    sql.NullInt64
		heartbeat    sql.NullInt64
	)
	if err := rows.Scan(&id, &name, &sort, &server, &method, &status, &nodeType,
		&nodeClass, &nodeGroup, &trafficRate, &speedLimit, &customMethod, &customRSS,
		&heartbeat); err != nil {
		return nil, false, err
	}

	protocol, ok := protocolForSort(sort.Int64)
	if !ok {
		return nil, false, nil // 跳过 999/9
	}

	host := parseServerHost(server.String)
	groups := []int64{nodeGroupIDFromClass(nodeClass.Int64)}
	if nodeGroup.Int64 > 0 {
		groups = append(groups, nodeGroupIDFromGroup(nodeGroup.Int64))
	}

	proto := map[string]any{
		"type":          protocol,
		"port":          0, // 源面板每用户独享端口，无固定端口
		"cipher":        method.String,
		"custom_method": customMethod.Int64 == 1,
		"traffic_rate":  trafficRate.Float64,
		"speed_limit":   speedLimit.Float64,
		"node_class":    nodeClass.Int64,
		"source_sort":   sort.Int64,
	}
	raw, err := json.Marshal(proto)
	if err != nil {
		return nil, false, err
	}

	// ss_node 无 created_at 列，用最后心跳时间；无心跳用 1970-01-01 兜底
	//（零值 time.Time{} 会被 MySQL 严格模式拒绝，见 WriteNodes）。
	timestamp := unixToTime(heartbeat.Int64)
	if timestamp.IsZero() {
		timestamp = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	}

	return &canonical.ServerNode{
		SourceID:       id,
		Protocol:       protocol,
		Name:           strings.TrimSpace(name.String),
		Host:           host,
		Port:           "0",
		ServerPort:     0,
		Tags:           strings.TrimSpace(status.String),
		GroupID:        nodeGroupIDFromClass(nodeClass.Int64),
		GroupSourceIDs: groups,
		Show:           nodeType.Int64 == 1,
		Sort:           int32(id),
		RawProtocol:    string(raw),
		CreatedAt:      timestamp,
		UpdatedAt:      timestamp,
	}, true, nil
}

// protocolForSort sort 值 → 协议名；false 表示跳过该节点。
func protocolForSort(sort int64) (string, bool) {
	switch sort {
	case sortSSR, sortSSOnly, sortSS2022:
		return "shadowsocks", true
	case sortVmess, sortVmessTLS:
		return "vmess", true
	case sortTrojan:
		return "trojan", true
	default: // 9 单端口、999 Radius 占位等
		return "", false
	}
}

// parseServerHost 解析 server 字段的中转后缀格式 "host;port=443#1666"。
func parseServerHost(server string) string {
	server = strings.TrimSpace(server)
	if idx := strings.Index(server, ";"); idx >= 0 {
		return strings.TrimSpace(server[:idx])
	}
	if idx := strings.Index(server, "#"); idx >= 0 {
		return strings.TrimSpace(server[:idx])
	}
	return server
}
