package writer

import (
	"context"
	"fmt"
	"strings"

	"github.com/npanel-dev/NPanel-backend/ent"

	"npanel-migrator/internal/data/canonical"
)

// WriteCoupons 批量写入优惠券。
// coupon.code 是唯一键，冲突时跳过。
func WriteCoupons(ctx context.Context, client *ent.Client, coupons []*canonical.Coupon) (int, error) {
	written := 0
	for _, c := range coupons {
		_, err := client.ProxyCoupon.Create().
			SetName(c.Name).
			SetCode(c.Code). // 必填 + 唯一
			SetType(c.Type).
			SetDiscount(c.Discount).
			SetCount(c.Count).
			SetUserLimit(c.UserLimit).
			SetUsedCount(c.UsedCount).
			SetStartTime(c.StartTime).
			SetExpireTime(c.ExpireTime).
			SetEnable(c.Enable).
			SetSubscribe("").
			SetCreatedAt(c.CreatedAt).
			SetUpdatedAt(c.UpdatedAt).
			Save(ctx)
		if err != nil {
			continue // 唯一冲突等跳过
		}
		written++
	}
	return written, nil
}

// WriteNotices 批量写入公告。
func WriteNotices(ctx context.Context, client *ent.Client, notices []*canonical.Notice) (int, error) {
	written := 0
	for _, n := range notices {
		_, err := client.ProxyAnnouncement.Create().
			SetTitle(n.Title).
			SetContent(n.Content).
			SetShow(n.Show).
			SetPinned(n.Pinned).
			SetCreatedAt(n.CreatedAt).
			SetUpdatedAt(n.UpdatedAt).
			Save(ctx)
		if err != nil {
			continue
		}
		written++
	}
	return written, nil
}

// WriteTickets 批量写入工单 + 工单消息。
// ticket.id 在 NPanel 是手动设置（无自增），用源 ID；冲突时跳过。
// ticket_follow 同理。sourceMap 提供 user_id 映射。
func WriteTickets(ctx context.Context, client *ent.Client, tickets []*canonical.Ticket, sourceMap *canonical.SourceMap) (int, error) {
	written := 0
	for _, t := range tickets {
		npanelUserID, ok := sourceMap.UserIDs[t.UserSourceID]
		if !ok {
			continue // 用户未迁移，跳过工单
		}
		// 用源 ID 作为 NPanel ticket ID（NPanel ticket.id 是 Unique 手动设置）。
		_, err := client.ProxyTicket.Create().
			SetID(t.SourceID).
			SetTitle(t.Title).
			SetDescription(t.Description).
			SetUserID(npanelUserID).
			SetStatus(t.Status).
			SetCreatedAt(t.CreatedAt).
			SetUpdatedAt(t.UpdatedAt).
			Save(ctx)
		if err != nil {
			continue // ID 冲突跳过
		}
		written++

		// 写入工单消息。
		for _, f := range t.Follows {
			followID := f.SourceID
			_, err := client.ProxyTicketFollow.Create().
				SetID(followID).
				SetTicketID(t.SourceID).
				SetFrom(f.From).
				SetType(f.Type).
				SetContent(f.Content).
				SetCreatedAt(f.CreatedAt).
				Save(ctx)
			if err != nil {
				continue
			}
		}
	}
	return written, nil
}

// WriteNodeGroups 批量写入节点分组（node_group）。
// NPanel 的 node_group.id 可手动设置，这里保留源分组 ID，便于节点和套餐直接映射。
func WriteNodeGroups(ctx context.Context, client *ent.Client, groups []*canonical.NodeGroup) (map[int64]int64, int, error) {
	idMap := make(map[int64]int64, len(groups))
	written := 0
	for _, g := range groups {
		if g.SourceID <= 0 {
			continue
		}
		idMap[g.SourceID] = g.SourceID
		name := strings.TrimSpace(g.Name)
		if name == "" {
			name = fmt.Sprintf("Group %d", g.SourceID)
		}
		_, err := client.ProxyServerGroup.Create().
			SetID(g.SourceID).
			SetName(name).
			SetGroupType("common").
			SetSort(int(g.SourceID)).
			SetForCalculation(true).
			SetIsExpiredGroup(false).
			SetExpiredDaysLimit(7).
			SetSpeedLimit(0).
			SetCreatedAt(g.CreatedAt).
			SetUpdatedAt(g.UpdatedAt).
			Save(ctx)
		if err != nil {
			continue
		}
		written++
	}
	return idMap, written, nil
}

// WriteNodes 批量写入节点（servers + nodes）。
// 简化处理：每个源节点生成一个 server（含 protocols JSON）+ 一个 node（入口）。
func WriteNodes(ctx context.Context, client *ent.Client, nodes []*canonical.ServerNode, sourceMap *canonical.SourceMap) (int, error) {
	written := 0
	for _, n := range nodes {
		// 创建 server（机器端，存 protocols JSON）。
		srv, err := client.ProxyServer.Create().
			SetName(n.Name).
			SetCountry("").
			SetCity("").
			SetServerAddr(n.Host).
			SetSort(n.Sort).
			SetProtocol(n.RawProtocol).
			SetCreatedAt(n.CreatedAt).
			SetUpdatedAt(n.UpdatedAt).
			Save(ctx)
		if err != nil {
			continue
		}
		// 创建 node（用户入口端）。
		port, _ := parsePort(n.Port)
		_, err = client.ProxyNode.Create().
			SetName(n.Name).
			SetTags(n.Tags).
			SetPort(uint16(port)).
			SetAddress(n.Host).
			SetServerID(srv.ID).
			SetProtocol(n.Protocol).
			SetEnabled(n.Show).
			SetNodeGroupIds(resolveNodeGroupIDs(n.GroupSourceIDs, sourceMap)).
			SetSort(n.Sort).
			SetCreatedAt(n.CreatedAt).
			SetUpdatedAt(n.UpdatedAt).
			Save(ctx)
		if err != nil {
			continue
		}
		written++
	}
	return written, nil
}

func resolveNodeGroupIDs(sourceIDs []int64, sourceMap *canonical.SourceMap) []int64 {
	if len(sourceIDs) == 0 {
		return []int64{}
	}
	out := make([]int64, 0, len(sourceIDs))
	seen := make(map[int64]bool, len(sourceIDs))
	for _, sourceID := range sourceIDs {
		if sourceID <= 0 {
			continue
		}
		if sourceMap == nil || sourceMap.NodeGroupIDs == nil {
			continue
		}
		targetID, ok := sourceMap.NodeGroupIDs[sourceID]
		if !ok {
			continue
		}
		if targetID <= 0 || seen[targetID] {
			continue
		}
		seen[targetID] = true
		out = append(out, targetID)
	}
	return out
}

// parsePort 把字符串端口转 int（v2board 的 port 是 varchar）。
func parsePort(s string) (int, error) {
	var p int
	_, err := fmt.Sscanf(s, "%d", &p)
	return p, err
}
