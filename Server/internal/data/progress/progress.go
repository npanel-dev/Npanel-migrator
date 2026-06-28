// Package progress 提供导入进度的线程安全状态记录。
//
// import 是长任务（可能迁移数万用户），需要实时反馈进度。
// 采用轮询模式：service 在后台 goroutine 执行导入，定期更新 Progress；
// 前端通过 GET /api/progress 轮询读取最新状态。
//
// Progress 是全局单例（同一时刻只允许一个导入任务运行）。
package progress

import (
	"sync"
	"time"
)

// Status 导入任务状态。
type Status string

const (
	StatusIdle      Status = "idle"      // 空闲（无任务）
	StatusRunning   Status = "running"   // 运行中
	StatusCompleted Status = "completed" // 成功完成
	StatusFailed    Status = "failed"    // 失败
)

// Phase 导入阶段（按依赖顺序）。
type Phase string

const (
	PhaseInit        Phase = "init"         // 初始化（建表、连接）
	PhasePlans       Phase = "plans"        // 迁移套餐
	PhasePriceOpts   Phase = "priceOptions" // 迁移价格档位
	PhaseUsers       Phase = "users"        // 迁移用户
	PhaseAuthMethods Phase = "authMethods"  // 迁移用户认证
	PhaseReferBackfill Phase = "refererBackfill" // 回填邀请关系
	PhaseSubscriptions Phase = "subscriptions"   // 迁移用户订阅
	PhaseOrders      Phase = "orders"       // 迁移订单
	PhaseDone        Phase = "done"
)

// LogEntry 单条日志。
type LogEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`   // info / warn / error
	Message string    `json:"message"`
}

// Snapshot 进度快照（GET /api/progress 返回的内容）。
type Snapshot struct {
	Status     Status     `json:"status"`
	Phase      Phase      `json:"phase"`
	PhaseLabel string     `json:"phaseLabel"`
	Total      int        `json:"total"`      // 当前阶段总数
	Done       int        `json:"done"`       // 当前阶段已完成
	Errors     int        `json:"errors"`     // 累计错误数
	StartedAt  *time.Time `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	Message    string     `json:"message"`    // 最新消息（成功/失败说明）
	Logs       []LogEntry `json:"logs"`       // 实时日志（最近 100 条）
}

// 日志环形缓冲容量。
const maxLogs = 100

// Tracker 线程安全的进度追踪器（全局单例）。
type Tracker struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

// NewTracker 创建空的追踪器。
func NewTracker() *Tracker {
	return &Tracker{
		snapshot: Snapshot{Status: StatusIdle},
	}
}

// Start 开始一个新任务。若已有任务运行中则返回 false。
func (t *Tracker) Start() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.snapshot.Status == StatusRunning {
		return false
	}
	now := time.Now()
	t.snapshot = Snapshot{
		Status:    StatusRunning,
		Phase:     PhaseInit,
		StartedAt: &now,
		Logs:      []LogEntry{},
	}
	return true
}

// Update 更新当前阶段的进度。
func (t *Tracker) Update(phase Phase, phaseLabel string, done, total, errors int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snapshot.Phase = phase
	t.snapshot.PhaseLabel = phaseLabel
	t.snapshot.Done = done
	t.snapshot.Total = total
	t.snapshot.Errors = errors
}

// IncrError 错误计数 +1。
func (t *Tracker) IncrError() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snapshot.Errors++
}

// SetMessage 设置最新消息。
func (t *Tracker) SetMessage(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.snapshot.Message = msg
}

// Log 追加一条日志（环形缓冲，保留最近 maxLogs 条）。
func (t *Tracker) Log(level, msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := LogEntry{Time: time.Now(), Level: level, Message: msg}
	t.snapshot.Logs = append(t.snapshot.Logs, entry)
	// 超过容量时丢弃最早的（环形）。
	if len(t.snapshot.Logs) > maxLogs {
		t.snapshot.Logs = t.snapshot.Logs[len(t.snapshot.Logs)-maxLogs:]
	}
}

// LogInfo 追加 info 级日志。
func (t *Tracker) LogInfo(msg string) { t.Log("info", msg) }

// LogWarn 追加 warn 级日志。
func (t *Tracker) LogWarn(msg string) { t.Log("warn", msg) }

// LogError 追加 error 级日志。
func (t *Tracker) LogError(msg string) { t.Log("error", msg) }

// Complete 标记任务成功完成。
func (t *Tracker) Complete(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.snapshot.Status = StatusCompleted
	t.snapshot.Phase = PhaseDone
	t.snapshot.FinishedAt = &now
	t.snapshot.Message = msg
}

// Fail 标记任务失败。
func (t *Tracker) Fail(msg string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.snapshot.Status = StatusFailed
	t.snapshot.FinishedAt = &now
	t.snapshot.Message = msg
}

// Snapshot 获取当前进度快照（只读副本）。
func (t *Tracker) Snapshot() Snapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshot
}

// IsRunning 当前是否有任务运行中。
func (t *Tracker) IsRunning() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshot.Status == StatusRunning
}
