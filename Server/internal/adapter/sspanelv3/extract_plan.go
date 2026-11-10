package sspanelv3

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// shopPriceSourceKey 是每个 shop 唯一价格档位的源周期键
// （ss-panel v3 一个商品一个价，没有 v2board 的多周期价格列）。
const shopPriceSourceKey = "shop_price"

// ExtractPlans 读取 shop → canonical.Plan。
// 字段映射：price 元→分；content.bandwidth GB→字节；content.speedlimit/connector 限速/设备数；
// content.class_expire 天 → 价格档位时长；status==1 → 显示且在售。
func ExtractPlans(ctx context.Context, cfg db.Config) ([]*canonical.Plan, error) {
	var plans []*canonical.Plan
	err := db.QueryRows(ctx, cfg,
		"SELECT id, name, price, content, status FROM shop ORDER BY id",
		func(rows *sql.Rows) error {
			p, err := scanShop(rows)
			if err != nil {
				return err
			}
			plans = append(plans, p)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 shop 失败: %w", err)
	}
	return plans, nil
}

func scanShop(rows *sql.Rows) (*canonical.Plan, error) {
	var (
		id      int64
		name    sql.NullString
		price   sql.NullFloat64
		content sql.NullString
		status  sql.NullInt64
	)
	if err := rows.Scan(&id, &name, &price, &content, &status); err != nil {
		return nil, err
	}

	c := parseShopContent(content.String)
	onSale := status.Int64 == 1

	unit, value := durationFromDays(c.ClassExpireDays)

	return &canonical.Plan{
		SourceID:       id,
		Name:           cleanShopName(name.String),
		Description:    shopDescription(content.String),
		TrafficBytes:   gbToBytes(c.BandwidthGB),
		SpeedLimitMbps: int32(c.SpeedLimit),
		DeviceLimit:    int32(c.Connector),
		Show:           onSale,
		Sell:           onSale,
		Sort:           int32(id),
		PriceOptions: []canonical.PriceOption{{
			SourceKey:     shopPriceSourceKey,
			Name:          "标准价",
			OptionType:    "duration",
			DurationUnit:  unit,
			DurationValue: value,
			PriceCents:    yuanToCents(price.Float64),
			OriginalCents: yuanToCents(price.Float64),
			IsDefault:     true,
		}},
	}, nil
}

// cleanShopName 去除源数据名称里的制表符等杂字符（dump 中存在 \t 后缀）。
func cleanShopName(name string) string {
	return strings.TrimSpace(strings.Trim(name, "\t\r\n "))
}

// shopDescription 把 content JSON 转成人类可读描述（对齐源面板 Shop::content() 的文案）。
func shopDescription(raw string) string {
	c := parseShopContent(raw)
	if c.Extra == nil {
		return ""
	}
	var parts []string
	if c.BandwidthGB > 0 {
		parts = append(parts, fmt.Sprintf("流量 %g GB", c.BandwidthGB))
	}
	if c.ExpireDays > 0 {
		parts = append(parts, fmt.Sprintf("账号有效期 +%g 天", c.ExpireDays))
	}
	if c.Class > 0 {
		parts = append(parts, fmt.Sprintf("等级 %d（%g 天）", c.Class, c.ClassExpireDays))
	}
	if c.ResetDays > 0 {
		parts = append(parts, fmt.Sprintf("每 %g 天重置流量为 %g GB", c.ResetDays, c.ResetValueGB))
	}
	return strings.Join(parts, "，")
}

// gbToBytes GB → 字节（源面板 content.bandwidth 单位是 GB，buy 逻辑 ×1024³）。
func gbToBytes(gb float64) int64 {
	return int64(gb * 1024 * 1024 * 1024)
}
