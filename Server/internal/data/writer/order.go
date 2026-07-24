package writer

import (
	"context"

	"github.com/npanel-dev/NPanel-backend/ent"

	"npanel-migrator/internal/data/canonical"
)

// WriteOrders 批量写入订单（历史订单，不触发支付/开通队列）。
// 返回 sourceOrderID → npanelOrderID 映射。
func WriteOrders(
	ctx context.Context,
	client *ent.Client,
	orders []*canonical.Order,
	sourceMap *canonical.SourceMap,
) (map[int64]int64, int, error) {
	orderIDMap := make(map[int64]int64, len(orders))
	errCount := 0

	for _, o := range orders {
		// 订单必须关联已写入的用户。
		npanelUserID, ok := sourceMap.UserIDs[o.UserSourceID]
		if !ok {
			errCount++
			continue
		}

		builder := client.ProxyOrder.Create().
			SetUserID(npanelUserID).
			SetOrderNo(o.OrderNo).
			SetType(int8(o.Type)).
			SetQuantity(o.Quantity).
			SetPrice(o.PriceCents).
			SetAmount(o.AmountCents).
			SetStatus(int8(o.Status)).
			SetNillableMethod(nilIfEmpty(o.PaymentMethod)).
			SetNillableTradeNo(nilIfEmpty(o.TradeNo)).
			SetCreatedAt(o.CreatedAt).
			SetUpdatedAt(o.UpdatedAt)

		// 关联套餐（若存在）。
		if o.PlanSourceID != 0 {
			if subID, ok := sourceMap.PlanIDs[o.PlanSourceID]; ok {
				builder.SetSubscribeID(subID)
			}
		}
		// 关联客户选择的目标价格档位，并写入商业版订单快照字段。
		if o.PlanSourceID != 0 && o.Period != "" {
			key := canonical.PriceOptionMapKey(o.PlanSourceID, o.Period)
			if optionID, ok := sourceMap.PriceOptionIDs[key]; ok {
				if option, exists := sourceMap.TargetPriceOptions[optionID]; exists {
					builder.
						SetPriceOptionID(option.ID).
						SetPriceOptionName(option.Name).
						SetDurationUnit(option.DurationUnit).
						SetDurationValue(option.DurationValue).
						SetOptionPrice(option.PriceCents)
				}
			}
		}

		created, err := builder.Save(ctx)
		if err != nil {
			errCount++
			continue
		}
		orderIDMap[o.SourceID] = created.ID
	}

	return orderIDMap, errCount, nil
}
