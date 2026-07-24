package service

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"npanel-migrator/internal/adapter/xiaov2board"
	"npanel-migrator/internal/data/db"
)

const planMappingQueryTimeout = 5 * time.Minute

// PlanMappingOptionsRequest 同时读取源端套餐和目标端已有套餐。
type PlanMappingOptionsRequest struct {
	SourceHost     string `json:"sourceHost"`
	SourcePort     int    `json:"sourcePort"`
	SourceDatabase string `json:"sourceDatabase"`
	SourceUsername string `json:"sourceUsername"`
	SourcePassword string `json:"sourcePassword"`
	SourcePanel    string `json:"sourcePanel"`
	TargetHost     string `json:"targetHost"`
	TargetPort     int    `json:"targetPort"`
	TargetDatabase string `json:"targetDatabase"`
	TargetUsername string `json:"targetUsername"`
	TargetPassword string `json:"targetPassword"`
}

type SourcePlanOption struct {
	SourcePeriod    string `json:"sourcePeriod"`
	Name            string `json:"name"`
	OptionType      string `json:"optionType"`
	DurationUnit    string `json:"durationUnit"`
	DurationValue   int64  `json:"durationValue"`
	PriceCents      int64  `json:"priceCents"`
	ActiveUserCount int64  `json:"activeUserCount"`
	OrderCount      int64  `json:"orderCount"`
}

type SourcePlanMappingOption struct {
	ID              int64              `json:"id"`
	Name            string             `json:"name"`
	ActiveUserCount int64              `json:"activeUserCount"`
	OrderCount      int64              `json:"orderCount"`
	PriceOptions    []SourcePlanOption `json:"priceOptions"`
}

type TargetPriceOption struct {
	ID            int64  `json:"id"`
	SubscribeID   int64  `json:"subscribeId"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	OptionType    string `json:"optionType"`
	DurationUnit  string `json:"durationUnit"`
	DurationValue int64  `json:"durationValue"`
	PriceCents    int64  `json:"priceCents"`
	Show          bool   `json:"show"`
	Sell          bool   `json:"sell"`
}

type TargetPlanMappingOption struct {
	ID           int64               `json:"id"`
	Name         string              `json:"name"`
	TrafficBytes int64               `json:"trafficBytes"`
	Show         bool                `json:"show"`
	Sell         bool                `json:"sell"`
	PriceOptions []TargetPriceOption `json:"priceOptions"`
}

type TargetTrialDefaults struct {
	SubscribeID int64  `json:"subscribeId"`
	TimeUnit    string `json:"timeUnit"`
	TimeValue   int64  `json:"timeValue"`
}

type PlanMappingOptionsResponse struct {
	OK                       bool                      `json:"ok"`
	Message                  string                    `json:"message"`
	SourcePlans              []SourcePlanMappingOption `json:"sourcePlans"`
	TargetPlans              []TargetPlanMappingOption `json:"targetPlans"`
	InactiveUserCount        int64                     `json:"inactiveUserCount"`
	TrialDefaults            TargetTrialDefaults       `json:"trialDefaults"`
	SourceOrderLookupIndexed bool                      `json:"sourceOrderLookupIndexed"`
}

// GetPlanMappingOptions 只读源、目标数据库，为界面生成套餐映射选项。
func (s *MigrationService) GetPlanMappingOptions(ctx context.Context, req *PlanMappingOptionsRequest) (*PlanMappingOptionsResponse, error) {
	sourceCfg := db.Config{
		Host: req.SourceHost, Port: req.SourcePort, Database: req.SourceDatabase,
		Username: req.SourceUsername, Password: req.SourcePassword,
	}
	targetCfg := db.Config{
		Host: req.TargetHost, Port: req.TargetPort, Database: req.TargetDatabase,
		Username: req.TargetUsername, Password: req.TargetPassword,
	}

	// 目标库先校验，避免目标配置无效时仍在源端执行大表统计。
	targetPlans, err := readTargetPlans(ctx, targetCfg)
	if err != nil {
		return nil, err
	}
	if len(targetPlans) == 0 {
		return nil, fmt.Errorf("目标库没有可映射的套餐，请先在 NPanel 创建套餐")
	}

	sourcePlans, err := xiaov2board.ExtractPlans(ctx, sourceCfg)
	if err != nil {
		return nil, err
	}
	activeCounts := make(map[int64]int64)
	activePeriodCounts := make(map[string]int64)
	orderCounts := make(map[int64]int64)
	orderPeriodCounts := make(map[string]int64)
	if err := db.QueryRowsWithTimeout(ctx, sourceCfg, planMappingQueryTimeout,
		`SELECT plan_id, COUNT(*)
		   FROM v2_user
		  WHERE banned = 0
		    AND COALESCE(plan_id, 0) > 0
		    AND COALESCE(transfer_enable, 0) > 0
		    AND (expired_at IS NULL OR expired_at > UNIX_TIMESTAMP())
		  GROUP BY plan_id`,
		func(rows *sql.Rows) error {
			var planID, count int64
			if err := rows.Scan(&planID, &count); err != nil {
				return err
			}
			activeCounts[planID] = count
			return nil
		}); err != nil {
		return nil, fmt.Errorf("统计源套餐有效用户失败: %w", err)
	}
	activePeriodCounts, sourceOrderLookupIndexed, err := xiaov2board.ActiveSubscriptionPeriodCounts(ctx, sourceCfg)
	if err != nil {
		return nil, fmt.Errorf("统计源套餐周期有效用户失败: %w", err)
	}
	if err := db.QueryRowsWithTimeout(ctx, sourceCfg, planMappingQueryTimeout,
		`SELECT plan_id, period, COUNT(*)
		   FROM v2_order
		  WHERE COALESCE(plan_id, 0) > 0
		    AND period <> 'deposit'
		  GROUP BY plan_id, period`,
		func(rows *sql.Rows) error {
			var planID, count int64
			var period sql.NullString
			if err := rows.Scan(&planID, &period, &count); err != nil {
				return err
			}
			orderCounts[planID] += count
			if period.Valid && period.String != "" {
				orderPeriodCounts[fmt.Sprintf("%d:%s", planID, period.String)] = count
			}
			return nil
		}); err != nil {
		return nil, fmt.Errorf("统计源套餐历史订单失败: %w", err)
	}
	inactiveCount, err := db.QueryScalarWithTimeout(ctx, sourceCfg, planMappingQueryTimeout,
		`SELECT COUNT(*) FROM v2_user
		  WHERE banned = 0
		    AND NOT (
		      COALESCE(plan_id, 0) > 0
		      AND COALESCE(transfer_enable, 0) > 0
		      AND (expired_at IS NULL OR expired_at > UNIX_TIMESTAMP())
		    )`)
	if err != nil {
		return nil, fmt.Errorf("统计无有效订阅用户失败: %w", err)
	}

	sourceOptions := make([]SourcePlanMappingOption, 0, len(sourcePlans))
	seenPlanIDs := make(map[int64]bool)
	for _, plan := range sourcePlans {
		seenPlanIDs[plan.SourceID] = true
		item := SourcePlanMappingOption{
			ID:              plan.SourceID,
			Name:            plan.Name,
			ActiveUserCount: activeCounts[plan.SourceID],
			OrderCount:      orderCounts[plan.SourceID],
			PriceOptions:    make([]SourcePlanOption, 0, len(plan.PriceOptions)),
		}
		seenPeriods := make(map[string]bool)
		for _, option := range plan.PriceOptions {
			seenPeriods[option.SourceKey] = true
			item.PriceOptions = append(item.PriceOptions, SourcePlanOption{
				SourcePeriod: option.SourceKey,
				Name:         option.Name, OptionType: option.OptionType,
				DurationUnit: option.DurationUnit, DurationValue: option.DurationValue,
				PriceCents:      option.PriceCents,
				ActiveUserCount: activePeriodCounts[fmt.Sprintf("%d:%s", plan.SourceID, option.SourceKey)],
				OrderCount:      orderPeriodCounts[fmt.Sprintf("%d:%s", plan.SourceID, option.SourceKey)],
			})
		}
		for _, option := range knownSourcePeriods() {
			key := fmt.Sprintf("%d:%s", plan.SourceID, option.SourcePeriod)
			activeCount := activePeriodCounts[key]
			orderCount := orderPeriodCounts[key]
			if (activeCount <= 0 && orderCount <= 0) || seenPeriods[option.SourcePeriod] {
				continue
			}
			option.ActiveUserCount = activeCount
			option.OrderCount = orderCount
			item.PriceOptions = append(item.PriceOptions, option)
		}
		sourceOptions = append(sourceOptions, item)
	}
	referencedPlanIDs := make(map[int64]bool)
	for planID := range activeCounts {
		referencedPlanIDs[planID] = true
	}
	for planID := range orderCounts {
		referencedPlanIDs[planID] = true
	}
	for planID := range referencedPlanIDs {
		if seenPlanIDs[planID] {
			continue
		}
		item := SourcePlanMappingOption{
			ID: planID, Name: fmt.Sprintf("已删除源套餐 #%d", planID),
			ActiveUserCount: activeCounts[planID], OrderCount: orderCounts[planID],
			PriceOptions: []SourcePlanOption{},
		}
		for _, option := range knownSourcePeriods() {
			key := fmt.Sprintf("%d:%s", planID, option.SourcePeriod)
			option.ActiveUserCount = activePeriodCounts[key]
			option.OrderCount = orderPeriodCounts[key]
			if option.ActiveUserCount > 0 || option.OrderCount > 0 {
				item.PriceOptions = append(item.PriceOptions, option)
			}
		}
		sourceOptions = append(sourceOptions, item)
	}
	sort.Slice(sourceOptions, func(i, j int) bool {
		return sourceOptions[i].ID < sourceOptions[j].ID
	})

	trialDefaults := readTargetTrialDefaults(ctx, targetCfg)

	return &PlanMappingOptionsResponse{
		OK: true, Message: "套餐映射选项读取成功",
		SourcePlans: sourceOptions, TargetPlans: targetPlans,
		InactiveUserCount: inactiveCount, TrialDefaults: trialDefaults,
		SourceOrderLookupIndexed: sourceOrderLookupIndexed,
	}, nil
}

func knownSourcePeriods() []SourcePlanOption {
	return []SourcePlanOption{
		{SourcePeriod: "month_price", Name: "月付", OptionType: "duration", DurationUnit: "Month", DurationValue: 1},
		{SourcePeriod: "quarter_price", Name: "季付", OptionType: "duration", DurationUnit: "Month", DurationValue: 3},
		{SourcePeriod: "half_year_price", Name: "半年付", OptionType: "duration", DurationUnit: "Month", DurationValue: 6},
		{SourcePeriod: "year_price", Name: "年付", OptionType: "duration", DurationUnit: "Year", DurationValue: 1},
		{SourcePeriod: "two_year_price", Name: "两年付", OptionType: "duration", DurationUnit: "Year", DurationValue: 2},
		{SourcePeriod: "three_year_price", Name: "三年付", OptionType: "duration", DurationUnit: "Year", DurationValue: 3},
		{SourcePeriod: "onetime_price", Name: "一次性", OptionType: "duration", DurationUnit: "NoLimit", DurationValue: 0},
		{SourcePeriod: "reset_price", Name: "流量重置", OptionType: "reset_pack"},
	}
}

func readTargetPlans(ctx context.Context, cfg db.Config) ([]TargetPlanMappingOption, error) {
	var plans []TargetPlanMappingOption
	byID := make(map[int64]int)
	if err := db.QueryRows(ctx, cfg,
		"SELECT id, name, traffic, `show`, sell FROM subscribe ORDER BY sort, id",
		func(rows *sql.Rows) error {
			var item TargetPlanMappingOption
			if err := rows.Scan(&item.ID, &item.Name, &item.TrafficBytes, &item.Show, &item.Sell); err != nil {
				return err
			}
			item.PriceOptions = []TargetPriceOption{}
			plans = append(plans, item)
			byID[item.ID] = len(plans) - 1
			return nil
		}); err != nil {
		return nil, fmt.Errorf("读取目标套餐失败: %w", err)
	}
	if err := db.QueryRows(ctx, cfg,
		`SELECT id, subscribe_id, code, name, option_type, duration_unit,
		        duration_value, price, `+"`show`"+`, sell
		   FROM subscribe_price_option
		  ORDER BY subscribe_id, sort, id`,
		func(rows *sql.Rows) error {
			var item TargetPriceOption
			if err := rows.Scan(
				&item.ID, &item.SubscribeID, &item.Code, &item.Name, &item.OptionType,
				&item.DurationUnit, &item.DurationValue, &item.PriceCents, &item.Show, &item.Sell,
			); err != nil {
				return err
			}
			if planIndex, ok := byID[item.SubscribeID]; ok {
				plans[planIndex].PriceOptions = append(plans[planIndex].PriceOptions, item)
			}
			return nil
		}); err != nil {
		return nil, fmt.Errorf("读取目标套餐价格档位失败: %w", err)
	}
	return plans, nil
}

func readTargetTrialDefaults(ctx context.Context, cfg db.Config) TargetTrialDefaults {
	result := TargetTrialDefaults{TimeUnit: "Day", TimeValue: 7}
	values := make(map[string]string)
	_ = db.QueryRows(ctx, cfg,
		`SELECT `+"`key`"+`, value FROM system
		  WHERE category = 'register'
		    AND `+"`key`"+` IN ('TrialSubscribe', 'trial_subscribe', 'TrialTime', 'trial_time', 'TrialTimeUnit', 'trial_time_unit')`,
		func(rows *sql.Rows) error {
			var key, value string
			if err := rows.Scan(&key, &value); err != nil {
				return err
			}
			values[strings.ToLower(key)] = strings.Trim(strings.TrimSpace(value), `"`)
			return nil
		})
	if value := values["trialsubscribe"]; value != "" {
		result.SubscribeID, _ = strconv.ParseInt(value, 10, 64)
	} else if value := values["trial_subscribe"]; value != "" {
		result.SubscribeID, _ = strconv.ParseInt(value, 10, 64)
	}
	if value := values["trialtime"]; value != "" {
		result.TimeValue, _ = strconv.ParseInt(value, 10, 64)
	} else if value := values["trial_time"]; value != "" {
		result.TimeValue, _ = strconv.ParseInt(value, 10, 64)
	}
	if value := values["trialtimeunit"]; value != "" {
		result.TimeUnit = normalizeTrialTimeUnit(value)
	} else if value := values["trial_time_unit"]; value != "" {
		result.TimeUnit = normalizeTrialTimeUnit(value)
	}
	return result
}

func normalizeTrialTimeUnit(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "minute":
		return "Minute"
	case "hour":
		return "Hour"
	case "day":
		return "Day"
	case "week":
		return "Week"
	case "month":
		return "Month"
	case "year":
		return "Year"
	case "nolimit", "no_limit":
		return "NoLimit"
	default:
		return value
	}
}
