package xiaov2board

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// ExtractOrders 从 v2_order 读取历史订单，转换为 canonical.Order。
// 订单状态映射见方案 6.8。
func ExtractOrders(ctx context.Context, cfg db.Config, batchSize int, onBatch func([]*canonical.Order) error) error {
	offset := 0
	for {
		var batch []*canonical.Order
		err := db.QueryRows(ctx, cfg,
			"SELECT id, user_id, plan_id, type, period, trade_no, total_amount, "+
				"status, paid_at, created_at, updated_at "+
				"FROM v2_order ORDER BY id LIMIT ? OFFSET ?",
			func(rows *sql.Rows) error {
				o, err := scanOrder(rows)
				if err != nil {
					return err
				}
				batch = append(batch, o)
				return nil
			},
			batchSize, offset,
		)
		if err != nil {
			return fmt.Errorf("读取 v2_order 失败 (offset %d): %w", offset, err)
		}
		if len(batch) == 0 {
			return nil
		}
		if err := onBatch(batch); err != nil {
			return err
		}
		if len(batch) < batchSize {
			return nil
		}
		offset += batchSize
	}
}

// scanOrder 扫描单行 v2_order → canonical.Order。
func scanOrder(rows *sql.Rows) (*canonical.Order, error) {
	var (
		id          int64
		userID      sql.NullInt64
		planID      sql.NullInt64
		orderType   sql.NullInt64
		period      sql.NullString
		tradeNo     sql.NullString
		totalAmount sql.NullInt64
		status      sql.NullInt64
		paidAt      sql.NullInt64
		createdAt   sql.NullInt64
		updatedAt   sql.NullInt64
	)
	if err := rows.Scan(&id, &userID, &planID, &orderType, &period, &tradeNo,
		&totalAmount, &status, &paidAt, &createdAt, &updatedAt); err != nil {
		return nil, err
	}

	// 订单类型映射（方案 6.8）：
	//   v2board: 1新购 2续费 3升级 4流量重置 9充值(xiao)
	//   NPanel:  1新购 2续费 3流量重置 4充值 5兑换码
	npanelType := mapOrderType(int(orderType.Int64))

	// 订单状态映射（方案 6.8）：
	//   v2board: 0待支付 1开通中 2已取消 3已完成 4已折抵
	//   NPanel:  1待支付 2已支付 3关闭 4失败 5完成
	npanelStatus := mapOrderStatus(int(status.Int64))

	var paidTime *time.Time
	if paidAt.Int64 > 0 {
		t := unixToTime(paidAt.Int64)
		paidTime = &t
	}

	return &canonical.Order{
		SourceID:      id,
		UserSourceID:  userID.Int64,
		PlanSourceID:  planID.Int64,
		OrderNo:       tradeNo.String,
		Type:          npanelType,
		Quantity:      1,
		PriceCents:    totalAmount.Int64,
		AmountCents:   totalAmount.Int64,
		Status:        npanelStatus,
		Period:        period.String,
		TradeNo:       tradeNo.String,
		PaidAt:        paidTime,
		CreatedAt:     unixToTime(createdAt.Int64),
		UpdatedAt:     unixToTime(updatedAt.Int64),
	}, nil
}

// mapOrderType v2board type → NPanel type（方案 6.8）。
func mapOrderType(t int) int {
	switch t {
	case 1:
		return 1 // 新购
	case 2:
		return 2 // 续费
	case 3:
		return 1 // 升级 → NPanel 新购（raw 标记升级）
	case 4:
		return 3 // 流量重置
	case 9:
		return 4 // 充值（xiao 独有）
	default:
		return 1
	}
}

// mapOrderStatus v2board status → NPanel status（方案 6.8）。
func mapOrderStatus(s int) int {
	switch s {
	case 0:
		return 1 // 待支付
	case 1:
		return 2 // 开通中 → 已支付
	case 2:
		return 3 // 已取消 → 关闭
	case 3:
		return 5 // 已完成
	case 4:
		return 5 // 已折抵 → 完成
	default:
		return 3
	}
}
