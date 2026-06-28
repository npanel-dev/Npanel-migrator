package writer

import (
	"context"
	"fmt"

	"github.com/npanel-dev/NPanel-backend/ent"

	"npanel-migrator/internal/data/canonical"
)

// WritePlans 批量写入套餐 + 价格档位。
// 返回 sourcePlanID → npanelSubscribeID 映射。
func WritePlans(ctx context.Context, client *ent.Client, plans []*canonical.Plan, sourceMap *canonical.SourceMap) (map[int64]int64, int, error) {
	planIDMap := make(map[int64]int64, len(plans))
	errCount := 0

	for _, p := range plans {
		builder := client.ProxySubscribe.Create().
			SetName(p.Name).
			SetNillableDescription(nilIfEmpty(p.Description)).
			SetTraffic(p.TrafficBytes).
			SetSpeedLimit(p.SpeedLimitMbps).
			SetDeviceLimit(p.DeviceLimit).
			SetShow(p.Show).
			SetSell(p.Sell).
			SetSort(p.Sort).
			SetCreatedAt(p.CreatedAt).
			SetUpdatedAt(p.UpdatedAt)
		if groupID := resolveSingleNodeGroupID(p.GroupSourceID, sourceMap); groupID > 0 {
			builder.SetNodeGroupID(groupID)
			builder.SetNodeGroupIds([]int64{groupID})
		} else {
			builder.SetNodeGroupIds([]int64{})
		}
		created, err := builder.Save(ctx)
		if err != nil {
			errCount++
			continue
		}
		planIDMap[p.SourceID] = created.ID

		// 写入价格档位。
		for _, po := range p.PriceOptions {
			_, err := client.ProxySubscribePriceOption.Create().
				SetSubscribeID(created.ID).
				SetName(po.Name).
				SetOptionType(po.OptionType).
				SetDurationUnit(po.DurationUnit).
				SetDurationValue(po.DurationValue).
				SetPrice(po.PriceCents).
				SetOriginalPrice(po.OriginalCents).
				SetIsDefault(po.IsDefault).
				SetCode(fmt.Sprintf("src-%d-%s", p.SourceID, po.SourceKey)).
				Save(ctx)
			if err != nil {
				errCount++
			}
		}
	}

	return planIDMap, errCount, nil
}

func resolveSingleNodeGroupID(sourceID int64, sourceMap *canonical.SourceMap) int64 {
	if sourceID <= 0 {
		return 0
	}
	if sourceMap == nil || sourceMap.NodeGroupIDs == nil {
		return 0
	}
	if mapped, ok := sourceMap.NodeGroupIDs[sourceID]; ok {
		return mapped
	}
	return 0
}
