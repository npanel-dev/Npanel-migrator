package sspanelv3

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// CodeIDOffset 充值码订单的合成 ID 偏移：code.id + 10^12。
// bought 与 code 是两张独立自增表，合成统一游标空间后才能做 keyset 分页。
const CodeIDOffset = 1_000_000_000_000

// OrderNo 前缀：源表自增 id 与其他面板/表可能冲突，统一加前缀。
const (
	boughtOrderNoPrefix = "SSP-B-"
	codeOrderNoPrefix   = "SSP-C-"
)

// NPanel 订单类型（canonical.Order 注释）：1新购 2续费 3流量重置 4充值。
const (
	orderTypeNew   = 1
	orderTypeRenew = 2
	orderTypeTopup = 4
	orderTypeDone  = 5 // 状态：5完成（历史记录均已结算）
)

// ExtractOrdersKeyset 按 id 游标分批读取 bought + code 合成订单流。
// bought → 套餐订单（renew=0→新购，>0→续费）；code(isused=1) → 充值订单。
func ExtractOrdersKeyset(
	ctx context.Context,
	pool *sql.DB,
	batchSize int,
	afterID, highWater int64,
	onBatch func([]*canonical.Order) error,
) error {
	if batchSize <= 0 {
		batchSize = 500
	}
	lastID := afterID
	for {
		batch := make([]*canonical.Order, 0, batchSize)
		// 派生表按合成 id 排序分页：bought 用原 id，code 加偏移。
		err := db.QueryRowsDBWithTimeout(ctx, pool, 5*time.Minute,
			`SELECT sid, kind, userid, plan_id, price, paid_at, renew, coupon, usedatetime, number
			   FROM (
			     SELECT b.id AS sid, 1 AS kind, b.userid AS userid, b.shopid AS plan_id,
			            b.price AS price, b.datetime AS paid_at, b.renew AS renew,
			            b.coupon AS coupon, NULL AS usedatetime, 0 AS number
			       FROM bought b
			     UNION ALL
			     SELECT c.id + ? AS sid, 2 AS kind, c.userid AS userid, 0 AS plan_id,
			            0 AS price, 0 AS paid_at, 0 AS renew, NULL AS coupon,
			            c.usedatetime AS usedatetime, c.number AS number
			       FROM code c
			      WHERE c.isused = 1
			   ) t
			  WHERE sid > ? AND sid <= ?
			  ORDER BY sid
			  LIMIT ?`,
			func(rows *sql.Rows) error {
				o, err := scanUnionOrder(rows)
				if err != nil {
					return err
				}
				batch = append(batch, o)
				return nil
			},
			CodeIDOffset, lastID, highWater, batchSize,
		)
		if err != nil {
			return fmt.Errorf("读取订单数据失败 (after id %d): %w", lastID, err)
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
		lastID = batch[len(batch)-1].SourceID
	}
}

// ExtractOrders 读取全量订单（小库/测试用）。
func ExtractOrders(ctx context.Context, cfg db.Config) ([]*canonical.Order, error) {
	pool, err := db.OpenPool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	highWater, err := OrderHighWater(ctx, pool)
	if err != nil {
		return nil, err
	}
	var orders []*canonical.Order
	err = ExtractOrdersKeyset(ctx, pool, 1000, 0, highWater, func(batch []*canonical.Order) error {
		orders = append(orders, batch...)
		return nil
	})
	return orders, err
}

// OrderHighWater 合成订单游标的上界：max(bought.id, code.id+偏移)。
func OrderHighWater(ctx context.Context, pool *sql.DB) (int64, error) {
	boughtMax, err := db.QueryScalarDBWithTimeout(ctx, pool, 30*time.Second,
		"SELECT COALESCE(MAX(id), 0) FROM bought")
	if err != nil {
		return 0, fmt.Errorf("统计 bought 上界失败: %w", err)
	}
	codeMax, err := db.QueryScalarDBWithTimeout(ctx, pool, 30*time.Second,
		"SELECT COALESCE(MAX(id), 0) FROM code")
	if err != nil {
		return 0, fmt.Errorf("统计 code 上界失败: %w", err)
	}
	if boughtMax < codeMax+CodeIDOffset {
		return codeMax + CodeIDOffset, nil
	}
	return boughtMax, nil
}

// UserHighWater 用户表游标上界。
func UserHighWater(ctx context.Context, pool *sql.DB) (int64, error) {
	hw, err := db.QueryScalarDBWithTimeout(ctx, pool, 30*time.Second,
		"SELECT COALESCE(MAX(id), 0) FROM user")
	if err != nil {
		return 0, fmt.Errorf("统计 user 上界失败: %w", err)
	}
	return hw, nil
}

func scanUnionOrder(rows *sql.Rows) (*canonical.Order, error) {
	var (
		sid     int64
		kind    int64
		userID  sql.NullInt64
		planID  sql.NullInt64
		price   sql.NullFloat64
		paidAt  sql.NullInt64
		renew   sql.NullInt64
		coupon  sql.NullString
		useDate sql.NullString
		number  sql.NullFloat64
	)
	if err := rows.Scan(&sid, &kind, &userID, &planID, &price, &paidAt,
		&renew, &coupon, &useDate, &number); err != nil {
		return nil, err
	}

	o := &canonical.Order{
		SourceID:     sid,
		UserSourceID: userID.Int64,
		PlanSourceID: planID.Int64,
		Quantity:     1,
		Status:       orderTypeDone,
		CreatedAt:    orEpoch(unixToTime(paidAt.Int64)),
		UpdatedAt:    orEpoch(unixToTime(paidAt.Int64)),
	}

	if kind == 1 {
		// bought：套餐购买记录。renew>0 表示续费（源字段是自动续费时间戳）。
		o.OrderNo = fmt.Sprintf("%s%d", boughtOrderNoPrefix, sid)
		o.Type = orderTypeNew
		if renew.Int64 > 0 {
			o.Type = orderTypeRenew
		}
		o.PriceCents = yuanToCents(price.Float64)
		o.AmountCents = yuanToCents(price.Float64)
		o.PaymentMethod = "sspanel-bought"
		o.Period = shopPriceSourceKey
		t := unixToTime(paidAt.Int64)
		o.PaidAt = &t
		o.CreatedAt = t
		o.UpdatedAt = t
		return o, nil
	}

	// code：充值码兑换记录。
	o.OrderNo = fmt.Sprintf("%s%d", codeOrderNoPrefix, sid-CodeIDOffset)
	o.Type = orderTypeTopup
	o.AmountCents = yuanToCents(number.Float64)
	o.PaymentMethod = "sspanel-code"
	if t := parseDateTime(useDate.String); !t.IsZero() {
		o.PaidAt = &t
		o.CreatedAt = t
		o.UpdatedAt = t
	}
	return o, nil
}
