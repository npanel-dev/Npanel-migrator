package writer

import (
	"context"
	"fmt"

	"github.com/npanel-dev/NPanel-backend/ent"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/checkpoint"
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

		builder := newOrderBuilder(client, o, npanelUserID, sourceMap)

		created, err := builder.Save(ctx)
		if err != nil {
			errCount++
			continue
		}
		orderIDMap[o.SourceID] = created.ID
	}

	return orderIDMap, errCount, nil
}

func newOrderBuilder(
	client *ent.Client,
	o *canonical.Order,
	npanelUserID int64,
	sourceMap *canonical.SourceMap,
) *ent.ProxyOrderCreate {
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
	if o.PlanSourceID != 0 {
		if subID, ok := sourceMap.PlanIDs[o.PlanSourceID]; ok {
			builder.SetSubscribeID(subID)
		}
	}
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
	return builder
}

func WriteOrdersBulk(
	ctx context.Context,
	runtime *Runtime,
	store *checkpoint.Store,
	jobID, owner string,
	orders []*canonical.Order,
	sourceMap *canonical.SourceMap,
	cp *checkpoint.Checkpoint,
) (map[int64]int64, int, error) {
	result := make(map[int64]int64, len(orders))
	failed, err := executeBulkWithBisect(
		orders,
		func(batch []*canonical.Order) error {
			mappings, err := writeOrdersBulkTx(
				ctx, runtime, store, jobID, owner, batch, sourceMap, cp,
			)
			if err != nil {
				return err
			}
			for _, mapping := range mappings {
				result[mapping.SourceID] = mapping.TargetID
			}
			return nil
		},
		func(order *canonical.Order, cause error) error {
			return recordRejectedEntity(
				ctx, runtime, store, jobID, owner, "orders", "order",
				order.SourceID, cause, cp,
			)
		},
	)
	return result, failed, err
}

func writeOrdersBulkTx(
	ctx context.Context,
	runtime *Runtime,
	store *checkpoint.Store,
	jobID, owner string,
	orders []*canonical.Order,
	sourceMap *canonical.SourceMap,
	cp *checkpoint.Checkpoint,
) ([]checkpoint.EntityMapping, error) {
	tx, err := runtime.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	builders := make([]*ent.ProxyOrderCreate, 0, len(orders))
	sources := make([]*canonical.Order, 0, len(orders))
	for _, order := range orders {
		userID, ok := sourceMap.UserIDs[order.UserSourceID]
		if !ok {
			return nil, &batchDataError{message: fmt.Sprintf("订单 %d 缺少目标用户映射", order.SourceID)}
		}
		builders = append(builders, newOrderBuilder(tx.Client, order, userID, sourceMap))
		sources = append(sources, order)
	}
	created, err := tx.Client.ProxyOrder.CreateBulk(builders...).Save(ctx)
	if err != nil {
		return nil, err
	}
	if len(created) != len(sources) {
		return nil, fmt.Errorf("订单 Bulk 返回数量异常: got %d want %d", len(created), len(sources))
	}
	mappings := make([]checkpoint.EntityMapping, 0, len(sources))
	for index, order := range sources {
		mappings = append(mappings, checkpoint.EntityMapping{
			SourceID: order.SourceID,
			TargetID: created[index].ID,
		})
	}
	if err := store.PutMappingsTx(ctx, tx.SQL, jobID, "order", mappings); err != nil {
		return nil, err
	}
	next := *cp
	next.LastSourceID = orders[len(orders)-1].SourceID
	next.Done += int64(len(orders))
	if err := store.RecordBatchTx(ctx, tx.SQL, checkpoint.BatchRecord{
		JobID: jobID, Phase: "orders",
		CursorFrom: orders[0].SourceID, CursorTo: next.LastSourceID,
		Attempted: len(orders), Succeeded: len(orders), Status: "committed",
	}); err != nil {
		return nil, err
	}
	if err := store.SaveCheckpointTx(ctx, tx.SQL, next, owner); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	*cp = next
	return mappings, nil
}
