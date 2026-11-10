package sspanelv3

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

// ss-panel v3 的 datetime 字符串解析与哨兵分类。
//
// 哨兵语义（迁移方案 6.3.3）：
//   - "1989-06-04 00:05:00" 等 2000 年前的日期：空/无效/已过期，不是永久。
//   - 2099/3001 等远期日期：永久有效（面板无真永久字段，用远期哨兵表示）。

type expireClass int

const (
	expireInvalid   expireClass = iota // 哨兵/无效 → 已过期
	expireNormal                       // 正常到期日
	expirePermanent                    // 远期哨兵 → 永久
)

// parseDateTime 解析源库 datetime 字段。
// 注意：源库 DSN 开启 parseTime 时，driver 扫进字符串得到的是 RFC3339 形式
// （"2019-07-15T23:36:19+08:00"）；关闭时是原生 "2019-07-15 23:36:19"。两种都兼容，
// 带时区的按其时区归一化到 UTC。解析失败返回零值。
func parseDateTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}

// orEpoch 零值时间兜底为 1970-01-01 00:00:01。
// MySQL 严格模式拒绝零日期（0000-00-00）；timestamp 列的下界是 1970-01-01 00:00:01 UTC，
// 用 00:00:00 依然会被拒绝。
func orEpoch(t time.Time) time.Time {
	if t.IsZero() {
		return time.Date(1970, 1, 1, 0, 0, 1, 0, time.UTC)
	}
	return t
}

// classifyDateTime 解析 datetime 并分类哨兵语义。
func classifyDateTime(s string) (time.Time, expireClass) {
	t := parseDateTime(s)
	if t.IsZero() {
		return t, expireInvalid
	}
	switch {
	case t.Year() < sentinelExpiredYear:
		return t, expireInvalid
	case t.Year() >= permanentYear:
		return t, expirePermanent
	default:
		return t, expireNormal
	}
}

// effectiveExpire 计算套餐有效期的合成规则：
// classExpire（套餐等级到期）与 expireIn（账号有效期）共同限制可用性，
// 任一无效 → 已过期；否则取两者中更早的正常日期（永久视为 +∞）。
// 返回 nil 表示"源端永久"（writer 会转为锚点+1 个月，见 canonical.UserSubscription）。
func effectiveExpire(classExpire, expireIn string, now time.Time) (*time.Time, bool) {
	ceT, ceC := classifyDateTime(classExpire)
	eiT, eiC := classifyDateTime(expireIn)

	if ceC == expireInvalid || eiC == expireInvalid {
		return nil, false
	}

	var earliest *time.Time
	for _, pair := range []struct {
		t time.Time
		c expireClass
	}{{ceT, ceC}, {eiT, eiC}} {
		if pair.c != expireNormal {
			continue // 永久不参与取最小
		}
		if earliest == nil || pair.t.Before(*earliest) {
			t := pair.t
			earliest = &t
		}
	}
	if earliest == nil {
		// 两个都是永久 → 源端永久。
		return nil, true
	}
	if !earliest.After(now) {
		return earliest, false // 已到期
	}
	return earliest, true
}

// shopContent 是 shop.content JSON 的结构化表示。
// 字段单位：bandwidth GB、expire 天、class 等级、class_expire 天、
// reset/reset_value 天/GB、speedlimit Mbps、connector 设备数。
type shopContent struct {
	BandwidthGB     float64
	ExpireDays      float64
	Class           int64
	ClassExpireDays float64
	ResetDays       float64
	ResetValueGB    float64
	SpeedLimit      float64
	Connector       int64
	Extra           map[string]any
}

// parseShopContent 解析 shop.content JSON；解析失败返回零值（dry-run 会提示）。
func parseShopContent(raw string) shopContent {
	var c shopContent
	if strings.TrimSpace(raw) == "" {
		return c
	}
	var m map[string]any
	if err := jsonUnmarshal([]byte(raw), &m); err != nil {
		return c
	}
	c.BandwidthGB = jsonFloat(m, "bandwidth")
	c.ExpireDays = jsonFloat(m, "expire")
	c.Class = jsonInt(m, "class")
	c.ClassExpireDays = jsonFloat(m, "class_expire")
	c.ResetDays = jsonFloat(m, "reset")
	c.ResetValueGB = jsonFloat(m, "reset_value")
	c.SpeedLimit = jsonFloat(m, "speedlimit")
	c.Connector = jsonInt(m, "connector")
	c.Extra = m
	return c
}

// durationFromDays 把 class_expire 天数归一为 NPanel 的 DurationUnit/DurationValue。
// 30→Month/1，90→Month/3，180→Month/6，≥360→Year/N（向上取整至少 1 年），其余→Day/N。
func durationFromDays(days float64) (unit string, value int64) {
	switch {
	case days <= 0:
		return "NoLimit", 0
	case days >= 360:
		years := int64(math.Round(days / 365))
		if years < 1 {
			years = 1
		}
		return "Year", years
	case days >= 180:
		return "Month", 6
	case days >= 90:
		return "Month", 3
	case days >= 30:
		return "Month", 1
	default:
		return "Day", int64(math.Round(days))
	}
}

// ---- JSON 宽容解析（content 字段值可能是字符串或数字）----

func jsonUnmarshal(data []byte, m *map[string]any) error {
	return json.Unmarshal(data, m)
}

func jsonFloat(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	default:
		return 0
	}
}

func jsonInt(m map[string]any, key string) int64 {
	return int64(math.Round(jsonFloat(m, key)))
}
