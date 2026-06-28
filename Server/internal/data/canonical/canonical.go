// Package canonical 定义跨面板的中间数据模型（canonical model）。
//
// 迁移数据流：源端(adapter extract) → canonical model → NPanel(writer)。
// canonical 是各面板数据的"最大公约数"表示，屏蔽不同面板的字段差异，
// 让 writer 只需关心 canonical → NPanel 的映射，不必为每个面板单独适配。
//
// 对应迁移方案第 5 章。字段单位统一：
//   - 金额：分（int64），与 NPanel 一致
//   - 流量：字节（int64），与 NPanel 一致
//   - 时间：time.Time
package canonical

import "time"

// User 用户主体（对应 NPanel user 表）。
type User struct {
	SourceID        int64  // 源端 ID
	PasswordHash    string // 密码哈希
	PasswordAlgo    string // 算法标识（md5/sha256/md5salt/sha256salt/bcrypt/default）
	PasswordSalt    string // 盐
	Email           string // 登录邮箱（写入 user_auth_methods）
	EmailVerified   bool   // 邮箱是否已验证 → user.valid_email
	BalanceCents    int64  // 余额（分）
	GiftCents       int64  // 赠送余额（分）
	CommissionCents int64  // 佣金余额（分）
	TelegramID      int64  // Telegram ID（0 表示无）
	ReferCode       string // 邀请码
	RefererSourceID int64  // 邀请人源 ID（二阶段回填）
	Enabled         bool   // 是否启用（banned 反义）
	IsAdmin         bool
	Avatar          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Plan 套餐（对应 NPanel subscribe 表）。
type Plan struct {
	SourceID    int64
	Name        string
	Description string
	// TrafficBytes 流量上限（字节）。
	TrafficBytes int64
	// SpeedLimitMbps 限速（Mbps）。
	SpeedLimitMbps int32
	// DeviceLimit 设备数限制。
	DeviceLimit int32
	// Show 是否显示；Sell 是否在售。
	Show bool
	Sell bool
	// Sort 排序。
	Sort int32
	// GroupSourceID 节点分组源 ID。
	GroupSourceID int64
	// PriceOptions 价格档位。
	PriceOptions []PriceOption
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NodeGroup 节点分组（对应 NPanel node_group 表）。
type NodeGroup struct {
	SourceID  int64
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// PriceOption 套餐价格档位（对应 NPanel subscribe_price_option 表）。
type PriceOption struct {
	SourceKey     string // 源周期键（如 month_price/reset_price）
	Name          string
	OptionType    string // duration / traffic_pack / reset_pack
	DurationUnit  string // Minute/Hour/Day/Week/Month/Year/NoLimit
	DurationValue int64
	PriceCents    int64 // 价格（分）
	OriginalCents int64 // 原价（分）
	IsDefault     bool
}

// UserSubscription 用户订阅（对应 NPanel user_subscribe 表）。
type UserSubscription struct {
	SourceID      int64
	UserSourceID  int64
	PlanSourceID  int64
	OrderSourceID int64
	GroupSourceID int64
	Token         string
	UUID          string
	StartTime     time.Time
	// ExpireTime 过期时间。nil 表示永久（writer 转 Unix 0）。
	ExpireTime    *time.Time
	TrafficBytes  int64 // 流量上限（字节）
	UploadBytes   int64 // 已上传
	DownloadBytes int64 // 已下载
	// Status 源端状态（0-5 等），由 adapter 映射；writer 按方案 6.3.2 处理 status=4。
	Status int
}

// Order 订单（对应 NPanel order 表）。
type Order struct {
	SourceID      int64
	UserSourceID  int64
	PlanSourceID  int64
	OrderNo       string
	Type          int // 1新购 2续费 3流量重置 4充值
	Quantity      int32
	PriceCents    int64  // 订单金额（分）
	AmountCents   int64  // 实付金额（分）
	Status        int    // 1待支付 2已支付 3关闭 4失败 5完成
	Period        string // 源周期键（month_price 等）
	PaymentMethod string
	TradeNo       string
	PaidAt        *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// SourceMap 源 ID → 目标 ID 的映射（账本，二阶段回填用）。
type SourceMap struct {
	UserIDs      map[int64]int64 // sourceUserID -> npanelUserID
	PlanIDs      map[int64]int64 // sourcePlanID -> npanelSubscribeID
	OrderIDs     map[int64]int64 // sourceOrderID -> npanelOrderID
	NodeGroupIDs map[int64]int64 // sourceGroupID -> npanelNodeGroupID
}

// NewSourceMap 创建空的映射表。
func NewSourceMap() *SourceMap {
	return &SourceMap{
		UserIDs:      make(map[int64]int64),
		PlanIDs:      make(map[int64]int64),
		OrderIDs:     make(map[int64]int64),
		NodeGroupIDs: make(map[int64]int64),
	}
}

// Coupon 优惠券（对应 NPanel coupon 表）。
type Coupon struct {
	SourceID   int64
	Name       string
	Code       string // 唯一键
	Type       int8   // 1=百分比 2=固定金额
	Discount   int64  // 折扣值（百分比时为 1-100，固定金额时为分）
	Count      int32  // 总数量（0=不限）
	UserLimit  int64  // 每用户限用次数
	UsedCount  int8
	StartTime  int64 // 毫秒时间戳
	ExpireTime int64 // 毫秒时间戳
	Enable     bool
	Subscribe  string // 可适用套餐 ID（逗号分隔）
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Notice 公告（对应 NPanel announcement 表）。
type Notice struct {
	SourceID  int64
	Title     string
	Content   string
	Show      bool
	Pinned    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Ticket 工单（对应 NPanel ticket 表）。
type Ticket struct {
	SourceID     int64
	Title        string // 源 v2_ticket.subject
	Description  string
	UserSourceID int64
	Status       int8 // NPanel: 1=Pending 2=Waiting 3=Processed 4=Closed
	CreatedAt    time.Time
	UpdatedAt    time.Time
	// Follows 工单消息（对应 ticket_follow）。
	Follows []TicketFollow
}

// TicketFollow 工单消息（对应 NPanel ticket_follow 表）。
type TicketFollow struct {
	SourceID       int64
	TicketSourceID int64
	UserSourceID   int64
	Content        string
	Type           int8   // 1=text 2=image
	From           string // "user" / "admin"
	CreatedAt      time.Time
}

// ServerNode 节点（对应 NPanel servers + nodes 两表）。
// v2board 的节点是一张表一个协议，迁移时合并成 servers(机器) + nodes(入口)。
type ServerNode struct {
	SourceID       int64
	Protocol       string // vmess / trojan / shadowsocks / hysteria 等
	Name           string
	Host           string
	Port           string // v2board port 是 varchar
	ServerPort     int64
	Tags           string
	GroupID        int64
	GroupSourceIDs []int64
	Show           bool
	Sort           int32
	// RawProtocol 源端协议配置 JSON（network/tls/cipher 等），原样存入 servers.protocols。
	RawProtocol string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
