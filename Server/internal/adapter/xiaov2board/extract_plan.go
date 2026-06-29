package xiaov2board

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// ExtractPlans 从 v2_plan 读取并转换为 canonical.Plan（含价格档位拆分）。
// 价格档位按方案 6.7 拆分：8 个周期价格列 → 8 条 price option。
func ExtractPlans(ctx context.Context, cfg db.Config) ([]*canonical.Plan, error) {
	var plans []*canonical.Plan
	columns, err := db.TableColumns(ctx, cfg, "v2_plan")
	if err != nil {
		return nil, fmt.Errorf("读取 v2_plan 字段失败: %w", err)
	}
	hasDeviceLimit := columns["device_limit"]

	fields := []string{"id", "group_id", "name", "content", "transfer_enable", "speed_limit"}
	if hasDeviceLimit {
		fields = append(fields, "device_limit")
	}
	fields = append(fields,
		"`show`", "renew", "sort", "month_price", "quarter_price", "half_year_price",
		"year_price", "two_year_price", "three_year_price", "onetime_price", "reset_price",
		"created_at", "updated_at",
	)

	err = db.QueryRows(ctx, cfg,
		"SELECT "+strings.Join(fields, ", ")+" FROM v2_plan ORDER BY id",
		func(rows *sql.Rows) error {
			p, err := scanPlan(rows, hasDeviceLimit)
			if err != nil {
				return err
			}
			plans = append(plans, p)
			return nil
		},
	)
	if err != nil {
		return nil, fmt.Errorf("读取 v2_plan 失败: %w", err)
	}
	return plans, nil
}

// scanPlan 扫描单行 v2_plan → canonical.Plan（含价格档位拆分）。
func scanPlan(rows *sql.Rows, hasDeviceLimit bool) (*canonical.Plan, error) {
	var (
		id             int64
		groupID        sql.NullInt64
		name           sql.NullString
		content        sql.NullString
		transferEnable sql.NullInt64
		speedLimit     sql.NullInt64
		deviceLimit    sql.NullInt64
		show           sql.NullInt64
		renew          sql.NullInt64
		sort           sql.NullInt64
		monthPrice     sql.NullInt64
		quarterPrice   sql.NullInt64
		halfYearPrice  sql.NullInt64
		yearPrice      sql.NullInt64
		twoYearPrice   sql.NullInt64
		threeYearPrice sql.NullInt64
		onetimePrice   sql.NullInt64
		resetPrice     sql.NullInt64
		createdAt      sql.NullInt64
		updatedAt      sql.NullInt64
	)
	scanTargets := []any{&id, &groupID, &name, &content, &transferEnable, &speedLimit}
	if hasDeviceLimit {
		scanTargets = append(scanTargets, &deviceLimit)
	}
	scanTargets = append(scanTargets,
		&show, &renew, &sort, &monthPrice, &quarterPrice, &halfYearPrice,
		&yearPrice, &twoYearPrice, &threeYearPrice, &onetimePrice, &resetPrice,
		&createdAt, &updatedAt,
	)
	if err := rows.Scan(scanTargets...); err != nil {
		return nil, err
	}

	// v2_plan.transfer_enable 单位是 GB（方案 6.6），转 bytes（×1024^3）。
	const gb = int64(1073741824)
	trafficBytes := transferEnable.Int64 * gb

	p := &canonical.Plan{
		SourceID:       id,
		Name:           name.String,
		Description:    content.String,
		TrafficBytes:   trafficBytes,
		SpeedLimitMbps: int32(speedLimit.Int64),
		DeviceLimit:    int32(deviceLimit.Int64),
		GroupSourceID:  groupID.Int64,
		// show && renew 决定 sell（方案 3.1.4）。
		Show:      show.Int64 == 1,
		Sell:      show.Int64 == 1 && renew.Int64 == 1,
		Sort:      int32(sort.Int64),
		CreatedAt: unixToTime(createdAt.Int64),
		UpdatedAt: unixToTime(updatedAt.Int64),
	}

	// 拆分价格档位（方案 6.7 周期映射）。
	// 只为有价格（>0 或 ==0 表示免费）的周期创建档位；NULL 表示该周期未配置价格。
	p.PriceOptions = buildPriceOptions(
		monthPrice, quarterPrice, halfYearPrice, yearPrice,
		twoYearPrice, threeYearPrice, onetimePrice, resetPrice,
	)
	return p, nil
}

// buildPriceOptions 按 6.7 表把 8 个周期价格列拆成 price option。
// 只为非 NULL 的价格列创建档位。
func buildPriceOptions(month, quarter, halfYear, year, twoYear, threeYear, onetime, reset sql.NullInt64) []canonical.PriceOption {
	type period struct {
		key           string
		name          string
		optionType    string
		durationUnit  string
		durationValue int64
		price         sql.NullInt64
	}

	periods := []period{
		{"month_price", "月付", "duration", "Month", 1, month},
		{"quarter_price", "季付", "duration", "Month", 3, quarter},
		{"half_year_price", "半年付", "duration", "Month", 6, halfYear},
		{"year_price", "年付", "duration", "Year", 1, year},
		{"two_year_price", "两年付", "duration", "Year", 2, twoYear},
		{"three_year_price", "三年付", "duration", "Year", 3, threeYear},
		{"onetime_price", "一次性", "duration", "NoLimit", 0, onetime},
		{"reset_price", "流量重置", "reset_pack", "", 0, reset},
	}

	var options []canonical.PriceOption
	for _, p := range periods {
		if !p.price.Valid {
			continue // 该周期未配置价格
		}
		options = append(options, canonical.PriceOption{
			SourceKey:     p.key,
			Name:          p.name,
			OptionType:    p.optionType,
			DurationUnit:  p.durationUnit,
			DurationValue: p.durationValue,
			PriceCents:    p.price.Int64,
			OriginalCents: p.price.Int64,          // 原价暂等于售价（源端无原价字段）
			IsDefault:     p.key == "month_price", // 默认选月付
		})
	}
	return options
}
