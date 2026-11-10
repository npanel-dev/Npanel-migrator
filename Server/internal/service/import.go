package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/npanel-dev/NPanel-backend/ent"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/checkpoint"
	"npanel-migrator/internal/data/db"
	"npanel-migrator/internal/data/detector"
	"npanel-migrator/internal/data/progress"
	"npanel-migrator/internal/data/writer"
)

// ImportRequest import 请求。
type ImportRequest struct {
	// 源端配置
	SourceHost     string `json:"sourceHost"`
	SourcePort     int    `json:"sourcePort"`
	SourceDatabase string `json:"sourceDatabase"`
	SourceUsername string `json:"sourceUsername"`
	SourcePassword string `json:"sourcePassword"`
	SourcePanel    string `json:"sourcePanel"`
	// 目标端配置
	TargetHost     string `json:"targetHost"`
	TargetPort     int    `json:"targetPort"`
	TargetDatabase string `json:"targetDatabase"`
	TargetUsername string `json:"targetUsername"`
	TargetPassword string `json:"targetPassword"`
	// BatchSize 每批读取条数（默认 500）。
	BatchSize int `json:"batchSize"`
	// Modules 勾选的迁移模块（空数组=完整迁移，全部模块）。
	// 可选值见 ModuleXxx 常量。
	Modules []string `json:"modules"`
	// PlanMappings 把源套餐映射到目标端已经存在的套餐及价格档位。
	// 迁移器不会在目标端创建源套餐。
	PlanMappings []PlanMapping `json:"planMappings"`
	// TrialAssignment 用于无有效订阅（无套餐、已过期、流量为 0）的非封禁用户。
	TrialAssignment TrialAssignment `json:"trialAssignment"`
	// ResumeJobID 非空时恢复由新版迁移器创建的同一任务。
	ResumeJobID string `json:"resumeJobId,omitempty"`
	// SourcePwdMethod/SourcePasswordSalt 源面板全局密码配置
	//（sspanelv3 的 pwdMethod 与 $_ENV['salt']，见 SourceOptions）。
	SourcePwdMethod    string `json:"sourcePwdMethod,omitempty"`
	SourcePasswordSalt string `json:"sourcePasswordSalt,omitempty"`
}

type PlanMapping struct {
	SourcePlanID      int64           `json:"sourcePlanId"`
	TargetSubscribeID int64           `json:"targetSubscribeId"`
	PeriodMappings    []PeriodMapping `json:"periodMappings"`
}

type PeriodMapping struct {
	SourcePeriod        string `json:"sourcePeriod"`
	TargetPriceOptionID int64  `json:"targetPriceOptionId"`
}

type TrialAssignment struct {
	TargetSubscribeID int64  `json:"targetSubscribeId"`
	DurationUnit      string `json:"durationUnit"`
	DurationValue     int64  `json:"durationValue"`
}

// 迁移模块标识（前端勾选项的 value）。
const (
	ModuleUsers         = "users"         // 用户 + 认证 + 邀请关系
	ModulePlans         = "plans"         // 套餐 + 价格档位映射（不创建）
	ModuleOrders        = "orders"        // 订单
	ModuleSubscriptions = "subscriptions" // 用户订阅
	ModuleNodes         = "nodes"         // 节点（各协议表）
	ModuleCoupons       = "coupons"       // 优惠券
	ModuleNotices       = "notices"       // 公告
	ModuleTickets       = "tickets"       // 工单 + 工单消息
)

// AllModules 全部模块（完整迁移的默认勾选）。
var AllModules = []string{
	ModulePlans, ModuleUsers, ModuleOrders, ModuleSubscriptions,
	ModuleNodes, ModuleCoupons, ModuleNotices, ModuleTickets,
}

// hasModule 判断是否勾选了某模块（modules 为空时视为全选）。
func hasModule(modules []string, m string) bool {
	if len(modules) == 0 {
		return true // 完整迁移
	}
	for _, x := range modules {
		if x == m {
			return true
		}
	}
	return false
}

// ImportResponse import 响应（任务已启动的确认）。
type ImportResponse struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	JobID   string `json:"jobId,omitempty"`
}

type MigrationJobSummary struct {
	ID              string     `json:"id"`
	Status          string     `json:"status"`
	EffectiveStatus string     `json:"effectiveStatus"`
	Phase           string     `json:"phase"`
	Total           int64      `json:"total"`
	Done            int64      `json:"done"`
	Errors          int64      `json:"errors"`
	StartedAt       time.Time  `json:"startedAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	LastError       string     `json:"lastError,omitempty"`
	Resumable       bool       `json:"resumable"`
}

type MigrationJobsResponse struct {
	OK   bool                  `json:"ok"`
	Jobs []MigrationJobSummary `json:"jobs"`
}

// 全局进度追踪器（单实例，同一时刻只允许一个 import 任务）。
var globalTracker = progress.NewTracker()

var activeImport = struct {
	sync.Mutex
	jobID           string
	store           *checkpoint.Store
	cancelRequested bool
}{}

// GetProgress 获取当前导入进度（供 GET /api/progress 调用）。
func (s *MigrationService) GetProgress() *progress.Snapshot {
	snap := globalTracker.Snapshot()
	return &snap
}

// StartImport 异步启动导入任务。
// 返回 (已启动?, 原因)。若已有任务运行中则拒绝。
func (s *MigrationService) StartImport(req *ImportRequest) (*ImportResponse, error) {
	if strings.TrimSpace(req.ResumeJobID) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := hydrateResumeOptions(ctx, req); err != nil {
			return &ImportResponse{OK: false, Message: "读取恢复任务失败: " + err.Error()}, nil
		}
	}
	if err := validateModuleDependencies(req.Modules); err != nil {
		return &ImportResponse{OK: false, Message: err.Error()}, nil
	}
	if globalTracker.IsRunning() {
		return &ImportResponse{
			OK:      false,
			Message: "已有迁移任务正在运行，请等待完成",
		}, nil
	}
	if hasModule(req.Modules, ModuleUsers) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := validateImportPreflight(ctx, req); err != nil {
			return &ImportResponse{
				OK:      false,
				Message: err.Error(),
			}, nil
		}
	}
	jobID := strings.TrimSpace(req.ResumeJobID)
	if jobID == "" {
		jobID = uuid.NewString()
	}
	// 加锁确保只有一个 import 任务。
	if !globalTracker.StartJob(jobID) {
		return &ImportResponse{
			OK:      false,
			Message: "已有迁移任务正在运行，请等待完成",
		}, nil
	}

	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = 500
	}
	if batchSize > 1000 {
		batchSize = 1000
	}

	host, _ := os.Hostname()
	owner := fmt.Sprintf("%s-%s", host, uuid.NewString())
	activeImport.Lock()
	activeImport.jobID = jobID
	activeImport.store = nil
	activeImport.cancelRequested = false
	activeImport.Unlock()

	// 后台 goroutine 执行导入。
	go s.runImportFast(req, batchSize, jobID, owner)

	return &ImportResponse{
		OK:      true,
		Message: "迁移任务已启动",
		JobID:   jobID,
	}, nil
}

// ListMigrationJobs returns resumable history for the exact source and target
// databases. Passwords are used only to open the connections and are never
// included in the task summary.
func (s *MigrationService) ListMigrationJobs(
	ctx context.Context,
	req *ImportRequest,
) (*MigrationJobsResponse, error) {
	sourceCfg := db.Config{
		Host: req.SourceHost, Port: req.SourcePort, Database: req.SourceDatabase,
		Username: req.SourceUsername, Password: req.SourcePassword,
	}
	panel := strings.TrimSpace(req.SourcePanel)
	if panel == "" || panel == "auto" {
		result, err := detector.Detect(ctx, sourceCfg, panel)
		if err != nil {
			return nil, fmt.Errorf("识别源面板失败: %w", err)
		}
		panel = string(result.Panel)
	}
	targetCfg := writer.NPanelConfig{
		Host: req.TargetHost, Port: req.TargetPort, Database: req.TargetDatabase,
		Username: req.TargetUsername, Password: req.TargetPassword,
	}
	runtime, err := writer.OpenRuntime(ctx, targetCfg)
	if err != nil {
		return nil, fmt.Errorf("连接目标库失败: %w", err)
	}
	defer runtime.Close()
	store := checkpoint.New(runtime.DB)
	if err := store.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	jobs, err := store.ListJobs(
		ctx,
		configFingerprint(panel, sourceCfg.Host, sourceCfg.Port, sourceCfg.Database),
		configFingerprint("npanel", targetCfg.Host, targetCfg.Port, targetCfg.Database),
		20,
	)
	if err != nil {
		return nil, err
	}
	summaries := make([]MigrationJobSummary, 0, len(jobs))
	for _, job := range jobs {
		summaries = append(summaries, MigrationJobSummary{
			ID: job.ID, Status: job.Status, EffectiveStatus: job.EffectiveStatus,
			Phase: job.Phase, Total: job.Total, Done: job.Done, Errors: job.Errors,
			StartedAt: job.StartedAt, UpdatedAt: job.UpdatedAt,
			FinishedAt: job.FinishedAt, LastError: job.LastError,
			Resumable: job.Resumable,
		})
	}
	return &MigrationJobsResponse{OK: true, Jobs: summaries}, nil
}

type CancelImportRequest struct {
	JobID string `json:"jobId"`
}

func (s *MigrationService) CancelImport(ctx context.Context, req *CancelImportRequest) (*ImportResponse, error) {
	activeImport.Lock()
	defer activeImport.Unlock()
	if activeImport.jobID == "" || (req.JobID != "" && req.JobID != activeImport.jobID) {
		return &ImportResponse{OK: false, Message: "没有匹配的运行中迁移任务"}, nil
	}
	activeImport.cancelRequested = true
	globalTracker.SetCancelRequested(true)
	if activeImport.store != nil {
		if err := activeImport.store.RequestCancel(ctx, activeImport.jobID); err != nil {
			return nil, err
		}
	}
	return &ImportResponse{
		OK: true, JobID: activeImport.jobID,
		Message: "已请求安全取消，将在当前批次提交后停止",
	}, nil
}

// validateImportPreflight 在正式写入前重新执行服务端预演。
// 不能只依赖前端展示的 dry-run 结果，否则调用 API 或跳过预演仍可导入
// 无法使用原账号密码登录的用户。
func validateImportPreflight(ctx context.Context, req *ImportRequest) error {
	sourceCfg := db.Config{
		Host: req.SourceHost, Port: req.SourcePort, Database: req.SourceDatabase,
		Username: req.SourceUsername, Password: req.SourcePassword,
	}
	result, err := detector.Detect(ctx, sourceCfg, req.SourcePanel)
	if err != nil {
		return fmt.Errorf("迁移前面板探测失败: %w", err)
	}
	adapter, err := adapterFor(result.Panel)
	if err != nil {
		return err
	}

	report, err := adapter.DryRun(ctx, sourceCfg, SourceOptions{
		PwdMethod: req.SourcePwdMethod, Salt: req.SourcePasswordSalt,
	})
	if err != nil {
		return fmt.Errorf("迁移前预演失败: %w", err)
	}
	return dryRunBlockingError(report)
}

// dryRunBlockingError 提取 error 级问题为阻断原因（中性 dryRunOutcome）。
func dryRunBlockingError(report *dryRunOutcome) error {
	if report == nil {
		return fmt.Errorf("迁移前预演未返回报告，已拒绝开始迁移")
	}
	var blockers []string
	for _, issue := range report.Issues {
		if issue.Severity != "error" {
			continue
		}
		message := issue.Message
		if issue.Count > 0 {
			message = fmt.Sprintf("%s（%d 条）", message, issue.Count)
		}
		blockers = append(blockers, message)
	}
	if len(blockers) == 0 && report.CanProceed {
		return nil
	}
	if len(blockers) == 0 {
		return fmt.Errorf("迁移预演未通过，已拒绝开始迁移")
	}
	return fmt.Errorf("迁移预演存在阻断问题，已拒绝开始迁移: %s", strings.Join(blockers, "；"))
}

func validateModuleDependencies(modules []string) error {
	if len(modules) == 0 || hasModule(modules, ModuleUsers) {
		return nil
	}
	var dependents []string
	if hasModule(modules, ModuleOrders) {
		dependents = append(dependents, "订单")
	}
	if hasModule(modules, ModuleSubscriptions) {
		dependents = append(dependents, "订阅")
	}
	if hasModule(modules, ModuleTickets) {
		dependents = append(dependents, "工单")
	}
	if len(dependents) > 0 {
		return fmt.Errorf("%s迁移依赖用户模块，请同时勾选用户", strings.Join(dependents, "、"))
	}
	return nil
}

// runImport 实际执行导入（在 goroutine 中）。
// 按依赖顺序：建表 → 套餐 → 价格档位 → 用户 → 认证 → 邀请回填 → 订单 → 订阅。
func setUserSourcePanel(users []*canonical.User, panel string) {
	for _, u := range users {
		u.SourcePanel = panel
	}
}

func configurePlanMappings(
	ctx context.Context,
	client *ent.Client,
	mappings []PlanMapping,
	sourceMap *canonical.SourceMap,
) error {
	for _, mapping := range mappings {
		if mapping.SourcePlanID <= 0 || mapping.TargetSubscribeID <= 0 {
			return fmt.Errorf("源套餐 %d 的目标套餐无效", mapping.SourcePlanID)
		}
		if _, exists := sourceMap.PlanIDs[mapping.SourcePlanID]; exists {
			return fmt.Errorf("源套餐 %d 存在重复映射", mapping.SourcePlanID)
		}
		if _, err := client.ProxySubscribe.Get(ctx, mapping.TargetSubscribeID); err != nil {
			return fmt.Errorf("目标套餐 %d 不存在: %w", mapping.TargetSubscribeID, err)
		}
		sourceMap.PlanIDs[mapping.SourcePlanID] = mapping.TargetSubscribeID

		seenPeriods := make(map[string]struct{})
		for _, period := range mapping.PeriodMappings {
			if period.SourcePeriod == "" || period.TargetPriceOptionID <= 0 {
				continue
			}
			if _, exists := seenPeriods[period.SourcePeriod]; exists {
				return fmt.Errorf("源套餐 %d 的周期 %s 存在重复映射", mapping.SourcePlanID, period.SourcePeriod)
			}
			seenPeriods[period.SourcePeriod] = struct{}{}

			option, err := client.ProxySubscribePriceOption.Get(ctx, period.TargetPriceOptionID)
			if err != nil {
				return fmt.Errorf("目标价格档位 %d 不存在: %w", period.TargetPriceOptionID, err)
			}
			if option.SubscribeID != mapping.TargetSubscribeID {
				return fmt.Errorf(
					"目标价格档位 %d 不属于目标套餐 %d",
					period.TargetPriceOptionID, mapping.TargetSubscribeID,
				)
			}
			key := canonical.PriceOptionMapKey(mapping.SourcePlanID, period.SourcePeriod)
			sourceMap.PriceOptionIDs[key] = option.ID
			sourceMap.TargetPriceOptions[option.ID] = canonical.TargetPriceOption{
				ID: option.ID, SubscribeID: option.SubscribeID, Name: option.Name,
				DurationUnit: option.DurationUnit, DurationValue: option.DurationValue,
				PriceCents: option.Price,
			}
		}
	}
	return nil
}

func validateSubscriptionAssignments(
	ctx context.Context,
	client *ent.Client,
	subs []*canonical.UserSubscription,
	sourceMap *canonical.SourceMap,
	trial TrialAssignment,
) error {
	trialNeeded := false
	for _, sub := range subs {
		if sub.NeedsTrial {
			trialNeeded = true
			continue
		}
		if _, ok := sourceMap.PlanIDs[sub.PlanSourceID]; !ok {
			return fmt.Errorf(
				"有效用户 %d 的源套餐 %d 尚未选择目标套餐",
				sub.UserSourceID, sub.PlanSourceID,
			)
		}
		if sub.SourcePeriod != "" {
			key := canonical.PriceOptionMapKey(sub.PlanSourceID, sub.SourcePeriod)
			if _, ok := sourceMap.PriceOptionIDs[key]; !ok {
				return fmt.Errorf(
					"有效用户 %d 的源套餐 %d 周期 %s 尚未选择目标价格档位",
					sub.UserSourceID, sub.PlanSourceID, sub.SourcePeriod,
				)
			}
		}
	}
	if !trialNeeded {
		return nil
	}
	if trial.TargetSubscribeID <= 0 {
		return fmt.Errorf("存在无有效订阅用户，请选择目标体验套餐")
	}
	if _, err := client.ProxySubscribe.Get(ctx, trial.TargetSubscribeID); err != nil {
		return fmt.Errorf("目标体验套餐 %d 不存在: %w", trial.TargetSubscribeID, err)
	}
	unit := strings.ToLower(strings.TrimSpace(trial.DurationUnit))
	validUnits := map[string]bool{
		"minute": true, "hour": true, "day": true, "week": true,
		"month": true, "year": true, "quarter": true, "half_year": true,
		"nolimit": true, "no_limit": true,
	}
	if !validUnits[unit] {
		return fmt.Errorf("体验时长单位 %q 无效", trial.DurationUnit)
	}
	if unit != "nolimit" && unit != "no_limit" && trial.DurationValue <= 0 {
		return fmt.Errorf("体验时长必须大于 0")
	}
	return nil
}
