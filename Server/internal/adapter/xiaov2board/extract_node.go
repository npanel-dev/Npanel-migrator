package xiaov2board

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

const (
	tableServerGroup       = "v2_server_group"
	tableServerVMess       = "v2_server_vmess"
	tableServerTrojan      = "v2_server_trojan"
	tableServerShadowsocks = "v2_server_shadowsocks"
	tableServerHysteria    = "v2_server_hysteria"
	tableServerVLESS       = "v2_server_vless"
	tableServerTUIC        = "v2_server_tuic"
	tableServerAnyTLS      = "v2_server_anytls"
	tableServerV2Node      = "v2_server_v2node"
)

// ExtractNodeGroups 从 v2_server_group 读取节点分组，映射到 NPanel node_group。
func ExtractNodeGroups(ctx context.Context, cfg db.Config) ([]*canonical.NodeGroup, error) {
	exists, err := db.TableExists(ctx, cfg, tableServerGroup)
	if err != nil {
		return nil, fmt.Errorf("检查 %s 失败: %w", tableServerGroup, err)
	}
	if !exists {
		return nil, nil
	}

	var groups []*canonical.NodeGroup
	if err := db.QueryRows(ctx, cfg,
		"SELECT id, name, created_at, updated_at FROM v2_server_group ORDER BY id",
		func(rows *sql.Rows) error {
			var (
				id        int64
				name      sql.NullString
				createdAt sql.NullInt64
				updatedAt sql.NullInt64
			)
			if err := rows.Scan(&id, &name, &createdAt, &updatedAt); err != nil {
				return err
			}
			groups = append(groups, &canonical.NodeGroup{
				SourceID:  id,
				Name:      name.String,
				CreatedAt: unixToTime(createdAt.Int64),
				UpdatedAt: unixToTime(updatedAt.Int64),
			})
			return nil
		},
	); err != nil {
		return nil, fmt.Errorf("读取 v2_server_group 失败: %w", err)
	}
	return groups, nil
}

// ExtractNodes 读取 xiaov2board 各协议节点表，合并成 canonical.ServerNode。
func ExtractNodes(ctx context.Context, cfg db.Config) ([]*canonical.ServerNode, error) {
	var nodes []*canonical.ServerNode
	tables := []string{
		tableServerVMess,
		tableServerTrojan,
		tableServerShadowsocks,
		tableServerHysteria,
		tableServerVLESS,
		tableServerTUIC,
		tableServerAnyTLS,
		tableServerV2Node,
	}
	found, err := db.TableExistsBatch(ctx, cfg, tables)
	if err != nil {
		return nil, fmt.Errorf("检查节点表失败: %w", err)
	}

	readers := []struct {
		table string
		read  func(context.Context, db.Config) ([]*canonical.ServerNode, error)
	}{
		{tableServerVMess, extractVmessNodes},
		{tableServerTrojan, extractTrojanNodes},
		{tableServerShadowsocks, extractSSNodes},
		{tableServerHysteria, extractHysteriaNodes},
		{tableServerVLESS, extractVLESSNodes},
		{tableServerTUIC, extractTUICNodes},
		{tableServerAnyTLS, extractAnyTLSNodes},
		{tableServerV2Node, extractV2NodeNodes},
	}

	for _, r := range readers {
		if !found[r.table] {
			continue
		}
		part, err := r.read(ctx, cfg)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, part...)
	}
	return nodes, nil
}

func extractVmessNodes(ctx context.Context, cfg db.Config) ([]*canonical.ServerNode, error) {
	var nodes []*canonical.ServerNode
	err := db.QueryRows(ctx, cfg,
		"SELECT id, group_id, name, host, port, server_port, tls, tags, network, networkSettings, tlsSettings, `show`, sort, created_at, updated_at "+
			"FROM v2_server_vmess WHERE `show`=1 ORDER BY id",
		func(rows *sql.Rows) error {
			n, err := scanVmessNode(rows)
			if err != nil {
				return err
			}
			nodes = append(nodes, n)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_server_vmess 失败: %w", err)
	}
	return nodes, nil
}

func extractTrojanNodes(ctx context.Context, cfg db.Config) ([]*canonical.ServerNode, error) {
	var nodes []*canonical.ServerNode
	err := db.QueryRows(ctx, cfg,
		"SELECT id, group_id, name, host, port, server_port, tags, network, network_settings, server_name, allow_insecure, `show`, sort, created_at, updated_at "+
			"FROM v2_server_trojan WHERE `show`=1 ORDER BY id",
		func(rows *sql.Rows) error {
			n, err := scanTrojanNode(rows)
			if err != nil {
				return err
			}
			nodes = append(nodes, n)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_server_trojan 失败: %w", err)
	}
	return nodes, nil
}

func extractSSNodes(ctx context.Context, cfg db.Config) ([]*canonical.ServerNode, error) {
	var nodes []*canonical.ServerNode
	err := db.QueryRows(ctx, cfg,
		"SELECT id, group_id, name, host, port, server_port, tags, cipher, obfs, `show`, sort, created_at, updated_at "+
			"FROM v2_server_shadowsocks WHERE `show`=1 ORDER BY id",
		func(rows *sql.Rows) error {
			n, err := scanSSNode(rows)
			if err != nil {
				return err
			}
			nodes = append(nodes, n)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_server_shadowsocks 失败: %w", err)
	}
	return nodes, nil
}

func extractHysteriaNodes(ctx context.Context, cfg db.Config) ([]*canonical.ServerNode, error) {
	var nodes []*canonical.ServerNode
	err := db.QueryRows(ctx, cfg,
		"SELECT id, version, group_id, name, host, port, server_port, tags, up_mbps, down_mbps, obfs, obfs_password, server_name, insecure, `show`, sort, created_at, updated_at "+
			"FROM v2_server_hysteria WHERE `show`=1 ORDER BY id",
		func(rows *sql.Rows) error {
			n, err := scanHysteriaNode(rows)
			if err != nil {
				return err
			}
			nodes = append(nodes, n)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_server_hysteria 失败: %w", err)
	}
	return nodes, nil
}

func extractVLESSNodes(ctx context.Context, cfg db.Config) ([]*canonical.ServerNode, error) {
	var nodes []*canonical.ServerNode
	err := db.QueryRows(ctx, cfg,
		"SELECT id, group_id, name, host, port, server_port, tls, tls_settings, flow, network, network_settings, encryption, encryption_settings, tags, `show`, sort, created_at, updated_at "+
			"FROM v2_server_vless WHERE `show`=1 ORDER BY id",
		func(rows *sql.Rows) error {
			n, err := scanVLESSNode(rows)
			if err != nil {
				return err
			}
			nodes = append(nodes, n)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_server_vless 失败: %w", err)
	}
	return nodes, nil
}

func extractTUICNodes(ctx context.Context, cfg db.Config) ([]*canonical.ServerNode, error) {
	var nodes []*canonical.ServerNode
	err := db.QueryRows(ctx, cfg,
		"SELECT id, group_id, name, host, port, server_port, tags, server_name, insecure, disable_sni, udp_relay_mode, zero_rtt_handshake, congestion_control, `show`, sort, created_at, updated_at "+
			"FROM v2_server_tuic WHERE `show`=1 ORDER BY id",
		func(rows *sql.Rows) error {
			n, err := scanTUICNode(rows)
			if err != nil {
				return err
			}
			nodes = append(nodes, n)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_server_tuic 失败: %w", err)
	}
	return nodes, nil
}

func extractAnyTLSNodes(ctx context.Context, cfg db.Config) ([]*canonical.ServerNode, error) {
	var nodes []*canonical.ServerNode
	err := db.QueryRows(ctx, cfg,
		"SELECT id, group_id, name, host, port, server_port, tags, server_name, insecure, padding_scheme, `show`, sort, created_at, updated_at "+
			"FROM v2_server_anytls WHERE `show`=1 ORDER BY id",
		func(rows *sql.Rows) error {
			n, err := scanAnyTLSNode(rows)
			if err != nil {
				return err
			}
			nodes = append(nodes, n)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_server_anytls 失败: %w", err)
	}
	return nodes, nil
}

func extractV2NodeNodes(ctx context.Context, cfg db.Config) ([]*canonical.ServerNode, error) {
	var nodes []*canonical.ServerNode
	err := db.QueryRows(ctx, cfg,
		"SELECT id, group_id, name, host, port, server_port, tags, protocol, tls, tls_settings, flow, network, network_settings, encryption, encryption_settings, disable_sni, udp_relay_mode, zero_rtt_handshake, congestion_control, cipher, up_mbps, down_mbps, obfs, obfs_password, padding_scheme, `show`, sort, created_at, updated_at "+
			"FROM v2_server_v2node WHERE `show`=1 ORDER BY id",
		func(rows *sql.Rows) error {
			n, err := scanV2NodeNode(rows)
			if err != nil {
				return err
			}
			nodes = append(nodes, n)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_server_v2node 失败: %w", err)
	}
	return nodes, nil
}

func scanVmessNode(rows *sql.Rows) (*canonical.ServerNode, error) {
	var (
		id, serverPort, show, sort, createdAt, updatedAt sql.NullInt64
		groupID, name, host, port, tags, network         sql.NullString
		tls                                              sql.NullInt64
		networkSettings, tlsSettings                     sql.NullString
	)
	if err := rows.Scan(&id, &groupID, &name, &host, &port, &serverPort, &tls, &tags,
		&network, &networkSettings, &tlsSettings, &show, &sort, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	proto := map[string]any{
		"type":    "vmess",
		"port":    serverPort.Int64,
		"network": network.String,
		"tls":     tls.Int64,
	}
	addJSONField(proto, "network_settings", networkSettings.String)
	addJSONField(proto, "tls_settings", tlsSettings.String)
	return newServerNode(id.Int64, "vmess", name.String, host.String, port.String, serverPort.Int64, tags.String, groupID.String, show.Int64 == 1, int32(sort.Int64), proto, createdAt.Int64, updatedAt.Int64), nil
}

func scanTrojanNode(rows *sql.Rows) (*canonical.ServerNode, error) {
	var (
		id, serverPort, allowInsecure, show, sort, createdAt, updatedAt       sql.NullInt64
		groupID, name, host, port, tags, network, networkSettings, serverName sql.NullString
	)
	if err := rows.Scan(&id, &groupID, &name, &host, &port, &serverPort, &tags,
		&network, &networkSettings, &serverName, &allowInsecure, &show, &sort, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	proto := map[string]any{
		"type":           "trojan",
		"port":           serverPort.Int64,
		"network":        network.String,
		"server_name":    serverName.String,
		"allow_insecure": allowInsecure.Int64 == 1,
	}
	addJSONField(proto, "network_settings", networkSettings.String)
	return newServerNode(id.Int64, "trojan", name.String, host.String, port.String, serverPort.Int64, tags.String, groupID.String, show.Int64 == 1, int32(sort.Int64), proto, createdAt.Int64, updatedAt.Int64), nil
}

func scanSSNode(rows *sql.Rows) (*canonical.ServerNode, error) {
	var (
		id, serverPort, show, sort, createdAt, updatedAt sql.NullInt64
		groupID, name, host, port, tags, cipher, obfs    sql.NullString
	)
	if err := rows.Scan(&id, &groupID, &name, &host, &port, &serverPort, &tags,
		&cipher, &obfs, &show, &sort, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	proto := map[string]any{
		"type":   "shadowsocks",
		"port":   serverPort.Int64,
		"cipher": cipher.String,
		"obfs":   obfs.String,
	}
	return newServerNode(id.Int64, "shadowsocks", name.String, host.String, port.String, serverPort.Int64, tags.String, groupID.String, show.Int64 == 1, int32(sort.Int64), proto, createdAt.Int64, updatedAt.Int64), nil
}

func scanHysteriaNode(rows *sql.Rows) (*canonical.ServerNode, error) {
	var (
		id, version, serverPort, upMbps, downMbps, insecure, show, sort, createdAt, updatedAt sql.NullInt64
		groupID, name, host, port, tags, obfs, obfsPassword, serverName                       sql.NullString
	)
	if err := rows.Scan(&id, &version, &groupID, &name, &host, &port, &serverPort, &tags, &upMbps, &downMbps,
		&obfs, &obfsPassword, &serverName, &insecure, &show, &sort, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	protocol := "hysteria"
	if version.Int64 == 2 {
		protocol = "hysteria2"
	}
	proto := map[string]any{
		"type":          protocol,
		"version":       version.Int64,
		"port":          serverPort.Int64,
		"up_mbps":       upMbps.Int64,
		"down_mbps":     downMbps.Int64,
		"obfs":          obfs.String,
		"obfs_password": obfsPassword.String,
		"server_name":   serverName.String,
		"insecure":      insecure.Int64 == 1,
	}
	return newServerNode(id.Int64, protocol, name.String, host.String, port.String, serverPort.Int64, tags.String, groupID.String, show.Int64 == 1, int32(sort.Int64), proto, createdAt.Int64, updatedAt.Int64), nil
}

func scanVLESSNode(rows *sql.Rows) (*canonical.ServerNode, error) {
	var (
		id, serverPort, tls, show, sort, createdAt, updatedAt sql.NullInt64
		groupID, name, host, port, tags, flow, network        sql.NullString
		tlsSettings, networkSettings                          sql.NullString
		encryption, encryptionSettings                        sql.NullString
	)
	if err := rows.Scan(&id, &groupID, &name, &host, &port, &serverPort, &tls, &tlsSettings, &flow,
		&network, &networkSettings, &encryption, &encryptionSettings, &tags, &show, &sort, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	proto := map[string]any{
		"type":       "vless",
		"port":       serverPort.Int64,
		"tls":        tls.Int64,
		"flow":       flow.String,
		"network":    network.String,
		"encryption": encryption.String,
	}
	addJSONField(proto, "tls_settings", tlsSettings.String)
	addJSONField(proto, "network_settings", networkSettings.String)
	addJSONField(proto, "encryption_settings", encryptionSettings.String)
	return newServerNode(id.Int64, "vless", name.String, host.String, port.String, serverPort.Int64, tags.String, groupID.String, show.Int64 == 1, int32(sort.Int64), proto, createdAt.Int64, updatedAt.Int64), nil
}

func scanTUICNode(rows *sql.Rows) (*canonical.ServerNode, error) {
	var (
		id, serverPort, insecure, disableSNI, zeroRTT, show, sort, createdAt, updatedAt sql.NullInt64
		groupID, name, host, port, tags, serverName, udpRelayMode, congestionControl    sql.NullString
	)
	if err := rows.Scan(&id, &groupID, &name, &host, &port, &serverPort, &tags, &serverName, &insecure,
		&disableSNI, &udpRelayMode, &zeroRTT, &congestionControl, &show, &sort, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	proto := map[string]any{
		"type":               "tuic",
		"port":               serverPort.Int64,
		"server_name":        serverName.String,
		"insecure":           insecure.Int64 == 1,
		"disable_sni":        disableSNI.Int64 == 1,
		"udp_relay_mode":     udpRelayMode.String,
		"zero_rtt_handshake": zeroRTT.Int64 == 1,
		"congestion_control": congestionControl.String,
	}
	return newServerNode(id.Int64, "tuic", name.String, host.String, port.String, serverPort.Int64, tags.String, groupID.String, show.Int64 == 1, int32(sort.Int64), proto, createdAt.Int64, updatedAt.Int64), nil
}

func scanAnyTLSNode(rows *sql.Rows) (*canonical.ServerNode, error) {
	var (
		id, serverPort, insecure, show, sort, createdAt, updatedAt sql.NullInt64
		groupID, name, host, port, tags, serverName, paddingScheme sql.NullString
	)
	if err := rows.Scan(&id, &groupID, &name, &host, &port, &serverPort, &tags, &serverName,
		&insecure, &paddingScheme, &show, &sort, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	proto := map[string]any{
		"type":        "anytls",
		"port":        serverPort.Int64,
		"server_name": serverName.String,
		"insecure":    insecure.Int64 == 1,
	}
	addJSONField(proto, "padding_scheme", paddingScheme.String)
	return newServerNode(id.Int64, "anytls", name.String, host.String, port.String, serverPort.Int64, tags.String, groupID.String, show.Int64 == 1, int32(sort.Int64), proto, createdAt.Int64, updatedAt.Int64), nil
}

func scanV2NodeNode(rows *sql.Rows) (*canonical.ServerNode, error) {
	var (
		id, serverPort, tls, disableSNI, zeroRTT, upMbps, downMbps, show, sort, createdAt, updatedAt sql.NullInt64
		groupID, name, host, port, tags, protocol, flow, network, encryption                         sql.NullString
		tlsSettings, networkSettings, encryptionSettings                                             sql.NullString
		udpRelayMode, congestionControl, cipher, obfs, obfsPassword, paddingScheme                   sql.NullString
	)
	if err := rows.Scan(&id, &groupID, &name, &host, &port, &serverPort, &tags, &protocol, &tls,
		&tlsSettings, &flow, &network, &networkSettings, &encryption, &encryptionSettings,
		&disableSNI, &udpRelayMode, &zeroRTT, &congestionControl, &cipher, &upMbps, &downMbps,
		&obfs, &obfsPassword, &paddingScheme, &show, &sort, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	protocolName := strings.TrimSpace(protocol.String)
	if protocolName == "" {
		protocolName = "mixed"
	}
	proto := map[string]any{
		"type":               protocolName,
		"port":               serverPort.Int64,
		"tls":                tls.Int64,
		"flow":               flow.String,
		"network":            network.String,
		"encryption":         encryption.String,
		"disable_sni":        disableSNI.Int64 == 1,
		"udp_relay_mode":     udpRelayMode.String,
		"zero_rtt_handshake": zeroRTT.Int64 == 1,
		"congestion_control": congestionControl.String,
		"cipher":             cipher.String,
		"up_mbps":            upMbps.Int64,
		"down_mbps":          downMbps.Int64,
		"obfs":               obfs.String,
		"obfs_password":      obfsPassword.String,
	}
	addJSONField(proto, "tls_settings", tlsSettings.String)
	addJSONField(proto, "network_settings", networkSettings.String)
	addJSONField(proto, "encryption_settings", encryptionSettings.String)
	addJSONField(proto, "padding_scheme", paddingScheme.String)
	return newServerNode(id.Int64, protocolName, name.String, host.String, port.String, serverPort.Int64, tags.String, groupID.String, show.Int64 == 1, int32(sort.Int64), proto, createdAt.Int64, updatedAt.Int64), nil
}

func newServerNode(sourceID int64, protocol, name, host, port string, serverPort int64, tags, groupID string, show bool, sort int32, proto map[string]any, createdAt, updatedAt int64) *canonical.ServerNode {
	groupIDs := parseSourceIDs(groupID)
	rawJSON, _ := json.Marshal([]map[string]any{proto})
	n := &canonical.ServerNode{
		SourceID:       sourceID,
		Protocol:       protocol,
		Name:           name,
		Host:           host,
		Port:           port,
		ServerPort:     serverPort,
		Tags:           tags,
		GroupSourceIDs: groupIDs,
		Show:           show,
		Sort:           sort,
		RawProtocol:    string(rawJSON),
		CreatedAt:      unixToTime(createdAt),
		UpdatedAt:      unixToTime(updatedAt),
	}
	if len(groupIDs) > 0 {
		n.GroupID = groupIDs[0]
	}
	return n
}

func addJSONField(dst map[string]any, key, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		dst[key] = decoded
		return
	}
	dst[key] = raw
}

func parseSourceIDs(raw string) []int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}

	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		return sourceIDsFromAny(decoded)
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '|'
	})
	var ids []int64
	for _, part := range parts {
		part = strings.Trim(part, " []\"'")
		if id, err := strconv.ParseInt(part, 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return dedupeIDs(ids)
}

func sourceIDsFromAny(v any) []int64 {
	switch x := v.(type) {
	case []any:
		ids := make([]int64, 0, len(x))
		for _, item := range x {
			ids = append(ids, sourceIDsFromAny(item)...)
		}
		return dedupeIDs(ids)
	case float64:
		if x > 0 {
			return []int64{int64(x)}
		}
	case string:
		return parseSourceIDs(x)
	}
	return nil
}

func dedupeIDs(ids []int64) []int64 {
	if len(ids) < 2 {
		return ids
	}
	seen := make(map[int64]bool, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
