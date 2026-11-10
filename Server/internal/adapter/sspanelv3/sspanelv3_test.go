package sspanelv3

import (
	"strings"
	"testing"
	"time"
)

// ---- 密码算法 ----

func TestSniffPassAlgo(t *testing.T) {
	sha256Hash := strings.Repeat("ab", 32) // 64 hex
	md5Hash := strings.Repeat("ab", 16)    // 32 hex
	tests := []struct {
		name string
		hash string
		want string
	}{
		{"64 位 hex → sha256salt", sha256Hash, "sha256salt"},
		{"32 位 hex → md5salt", md5Hash, "md5salt"},
		{"bcrypt $2y$", "$2y$10$" + strings.Repeat("a", 53), "bcrypt"},
		{"空", "", "default"},
		{"大写 hex 无法识别", strings.ToUpper(sha256Hash), "default"},
		{"任意串", "plaintext", "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sniffPassAlgo(tt.hash); got != tt.want {
				t.Errorf("sniffPassAlgo(%q) = %q, want %q", tt.hash, got, tt.want)
			}
		})
	}
}

func TestResolvePwdMethod(t *testing.T) {
	hash64 := strings.Repeat("ab", 32)
	tests := []struct {
		name      string
		pwdMethod string
		hash      string
		want      string
	}{
		{"显式 md5", "md5", "anything", "md5salt"},
		{"显式 sha256", "sha256", "anything", "sha256salt"},
		{"空 → 嗅探 64hex", "", hash64, "sha256salt"},
		{"argon2id 不支持", "argon2id", hash64, "default"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolvePwdMethod(tt.pwdMethod, tt.hash); got != tt.want {
				t.Errorf("resolvePwdMethod(%q, _) = %q, want %q", tt.pwdMethod, got, tt.want)
			}
		})
	}
}

func TestSaltForAlgo(t *testing.T) {
	if got := saltForAlgo("mySalt", "sha256salt"); got != "mySalt" {
		t.Errorf("sha256salt 应保留 salt, got %q", got)
	}
	if got := saltForAlgo("mySalt", "bcrypt"); got != "" {
		t.Errorf("bcrypt 应清空 salt, got %q", got)
	}
}

// ---- 时间哨兵 ----

func TestClassifyDateTime(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  expireClass
	}{
		{"1989 哨兵 → 无效", "1989-06-04 00:05:00", expireInvalid},
		{"正常日期", "2026-05-01 00:00:00", expireNormal},
		{"2099 永久", "2099-06-04 00:05:00", expirePermanent},
		{"3001 永久", "3001-08-13 16:43:58", expirePermanent},
		{"RFC3339 正常（driver parseTime 形式）", "2026-05-01T00:00:00+08:00", expireNormal},
		{"RFC3339 永久", "2099-06-04T00:05:00+08:00", expirePermanent},
		{"空", "", expireInvalid},
		{"乱串", "not-a-date", expireInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, class := classifyDateTime(tt.input)
			if class != tt.want {
				t.Errorf("classifyDateTime(%q) = %v, want %v", tt.input, class, tt.want)
			}
		})
	}
}

func TestParseDateTimeRFC3339(t *testing.T) {
	// 源库 DSN 开启 parseTime 时，datetime 扫进字符串是 RFC3339 形式且带本地时区。
	got := parseDateTime("2019-07-15T23:36:19+08:00")
	if got.IsZero() {
		t.Fatal("RFC3339 形式应解析成功")
	}
	if got.UTC().Format("2006-01-02 15:04:05") != "2019-07-15 15:36:19" {
		t.Errorf("时区归一化错误: %v", got.UTC())
	}
	// 原生空格分隔形式。
	if parseDateTime("2019-07-15 23:36:19").IsZero() {
		t.Error("原生 datetime 形式应解析成功")
	}
}

func TestOrEpoch(t *testing.T) {
	// timestamp 列下界是 1970-01-01 00:00:01，00:00:00 会被 MySQL 拒绝。
	got := orEpoch(time.Time{})
	if got.Format("2006-01-02 15:04:05") != "1970-01-01 00:00:01" {
		t.Errorf("orEpoch 零值 = %v, want 1970-01-01 00:00:01", got)
	}
	normal := time.Date(2020, 5, 1, 0, 0, 0, 0, time.UTC)
	if !orEpoch(normal).Equal(normal) {
		t.Error("非零值不应被修改")
	}
}

func TestEffectiveExpire(t *testing.T) {
	now := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	future := "2027-01-01 00:00:00"
	past := "2020-01-01 00:00:00"
	sentinel := "1989-06-04 00:05:00"
	permanent := "2099-06-04 00:05:00"

	tests := []struct {
		name          string
		classExpire   string
		expireIn      string
		wantActive    bool
		wantNilExpire bool // 期望 ExpireTime == nil（源端永久）
	}{
		{"双正常未来", future, permanent, true, false},
		{"class 过期", past, permanent, false, false},
		{"账号过期", permanent, past, false, false},
		{"class 哨兵", sentinel, permanent, false, false},
		{"双永久", permanent, "3001-08-13 00:00:00", true, true},
		{"取更早的", future, "2026-12-01 00:00:00", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expire, active := effectiveExpire(tt.classExpire, tt.expireIn, now)
			if active != tt.wantActive {
				t.Errorf("active = %v, want %v", active, tt.wantActive)
			}
			if tt.wantNilExpire && expire != nil {
				t.Errorf("expire 应为 nil（源端永久）, got %v", expire)
			}
			if !tt.wantNilExpire && tt.wantActive && expire == nil {
				t.Error("active 且非永久时 expire 不应为 nil")
			}
		})
	}

	// 取更早日期的精确性。
	expire, _ := effectiveExpire(future, "2026-12-01 00:00:00", now)
	if expire == nil || expire.Format("2006-01-02") != "2026-12-01" {
		t.Errorf("应取更早的 expire_in, got %v", expire)
	}
}

// ---- shop content ----

func TestParseShopContent(t *testing.T) {
	// 来自真实 dump 的 content（值为字符串形式）。
	raw := `{"bandwidth":"400","expire":"120","class":"33","class_expire":"90","reset":"30","reset_value":"400"}`
	c := parseShopContent(raw)
	if c.BandwidthGB != 400 {
		t.Errorf("bandwidth = %v, want 400", c.BandwidthGB)
	}
	if c.Class != 33 {
		t.Errorf("class = %v, want 33", c.Class)
	}
	if c.ClassExpireDays != 90 {
		t.Errorf("class_expire = %v, want 90", c.ClassExpireDays)
	}

	// 数字形式。
	c2 := parseShopContent(`{"bandwidth":100,"class":5,"class_expire":30,"speedlimit":50,"connector":3}`)
	if c2.BandwidthGB != 100 || c2.Class != 5 || c2.SpeedLimit != 50 || c2.Connector != 3 {
		t.Errorf("数字形式解析错误: %+v", c2)
	}

	// 非法 JSON → 零值。
	c3 := parseShopContent("not-json")
	if c3.Extra != nil {
		t.Errorf("非法 JSON 应返回零值, got %+v", c3)
	}
}

func TestDurationFromDays(t *testing.T) {
	tests := []struct {
		days      float64
		wantUnit  string
		wantValue int64
	}{
		{30, "Month", 1},
		{90, "Month", 3},
		{180, "Month", 6},
		{360, "Year", 1},
		{365, "Year", 1},
		{720, "Year", 2},
		{7, "Day", 7},
		{0, "NoLimit", 0},
	}
	for _, tt := range tests {
		unit, value := durationFromDays(tt.days)
		if unit != tt.wantUnit || value != tt.wantValue {
			t.Errorf("durationFromDays(%v) = (%q,%d), want (%q,%d)",
				tt.days, unit, value, tt.wantUnit, tt.wantValue)
		}
	}
}

func TestYuanToCents(t *testing.T) {
	tests := []struct {
		yuan float64
		want int64
	}{
		{1000.00, 100000},
		{0.01, 1},
		{12.345, 1235}, // 四舍五入
		{0, 0},
	}
	for _, tt := range tests {
		if got := yuanToCents(tt.yuan); got != tt.want {
			t.Errorf("yuanToCents(%v) = %d, want %d", tt.yuan, got, tt.want)
		}
	}
}

func TestGBToBytes(t *testing.T) {
	if got := gbToBytes(400); got != 400*1024*1024*1024 {
		t.Errorf("gbToBytes(400) = %d", got)
	}
}

func TestCleanShopName(t *testing.T) {
	if got := cleanShopName("VIP3季度订阅（按月重置）\t"); got != "VIP3季度订阅（按月重置）" {
		t.Errorf("cleanShopName = %q", got)
	}
}

// ---- 节点 ----

func TestProtocolForSort(t *testing.T) {
	tests := []struct {
		sort   int64
		want   string
		wantOK bool
	}{
		{0, "shadowsocks", true},
		{10, "shadowsocks", true},
		{13, "shadowsocks", true},
		{11, "vmess", true},
		{12, "vmess", true},
		{14, "trojan", true},
		{9, "", false},   // 单端口
		{999, "", false}, // Radius 占位
	}
	for _, tt := range tests {
		got, ok := protocolForSort(tt.sort)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("protocolForSort(%d) = (%q,%v), want (%q,%v)",
				tt.sort, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestParseServerHost(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1.2.3.4", "1.2.3.4"},
		{"no7515694731.internetworld.cloud;port=443#1666", "no7515694731.internetworld.cloud"},
		{"host#1666", "host"},
		{"  spaced.com  ", "spaced.com"},
	}
	for _, tt := range tests {
		if got := parseServerHost(tt.input); got != tt.want {
			t.Errorf("parseServerHost(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestNodeGroupIDs(t *testing.T) {
	if got := nodeGroupIDFromClass(33); got != 33 {
		t.Errorf("class 33 → %d", got)
	}
	if got := nodeGroupIDFromGroup(3); got != 1003 {
		t.Errorf("group 3 → %d", got)
	}
	if !isNodeGroupIDGroup(1003) || isNodeGroupIDGroup(99) {
		t.Error("isNodeGroupIDGroup 判定错误")
	}
}

// ---- 订阅推导 ----

func TestDeriveSubscriptionPlan(t *testing.T) {
	classShops := map[int64]int64{33: 14, 44: 15}
	tests := []struct {
		name         string
		active       bool
		class        int64
		latestShopID int64
		wantPlan     int64
		wantPeriod   string
		wantTrial    bool
	}{
		{"失效 → trial", false, 33, 14, 0, "", true},
		{"有购买 → shop+周期", true, 33, 14, 14, shopPriceSourceKey, false},
		{"无购买有匹配商品", true, 33, 0, 14, "", false},
		{"无购买无匹配 → trial", true, 55, 0, 0, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, period, trial := deriveSubscriptionPlan(tt.active, tt.class, tt.latestShopID, classShops)
			if plan != tt.wantPlan || period != tt.wantPeriod || trial != tt.wantTrial {
				t.Errorf("deriveSubscriptionPlan(%v,%d,%d) = (%d,%q,%v), want (%d,%q,%v)",
					tt.active, tt.class, tt.latestShopID,
					plan, period, trial, tt.wantPlan, tt.wantPeriod, tt.wantTrial)
			}
		})
	}
}

func TestSubscriptionGroupID(t *testing.T) {
	if got := subscriptionGroupID(0); got != 0 {
		t.Errorf("group 0 → %d", got)
	}
	if got := subscriptionGroupID(5); got != 1005 {
		t.Errorf("group 5 → %d", got)
	}
}

// ---- 杂项 ----

func TestTicketStatus(t *testing.T) {
	if ticketStatus(1) != 1 {
		t.Error("status=1 应保持开启")
	}
	if ticketStatus(0) != 4 {
		t.Error("status=0 应关闭")
	}
}

func TestTicketFollowFrom(t *testing.T) {
	if ticketFollowFrom(7, 7) != "user" {
		t.Error("同 id 应为 user")
	}
	if ticketFollowFrom(7, 1) != "admin" {
		t.Error("不同 id 应为 admin")
	}
}

func TestRandomToken(t *testing.T) {
	tok := randomToken()
	if len(tok) != 32 {
		t.Errorf("token 长度 = %d, want 32", len(tok))
	}
	if tok == randomToken() {
		t.Error("token 应随机")
	}
}

func TestIsValidHashForAlgo(t *testing.T) {
	hash64 := strings.Repeat("ab", 32)
	hash32 := strings.Repeat("ab", 16)
	bcrypt := "$2y$10$" + strings.Repeat("a", 53)
	if !isValidHashForAlgo("sha256salt", hash64) {
		t.Error("64hex 应匹配 sha256salt")
	}
	if !isValidHashForAlgo("md5salt", hash32) {
		t.Error("32hex 应匹配 md5salt")
	}
	if !isValidHashForAlgo("bcrypt", bcrypt) {
		t.Error("bcrypt 格式应匹配")
	}
	if isValidHashForAlgo("sha256salt", hash32) {
		t.Error("32hex 不应匹配 sha256salt")
	}
}
