package service

import (
	"context"
	"fmt"

	"npanel-migrator/internal/data/db"
	"npanel-migrator/internal/data/detector"
	"npanel-migrator/internal/data/progress"
)

// 各任务的独立进度追踪器（避免互相冲突）。
var (
	detectTracker = progress.NewTracker()
	dryRunTracker = progress.NewTracker()
)

// 任务类型，用于 GET /api/task/progress?type=xxx 区分。
const (
	TaskDetect = "detect"
	TaskDryRun = "dryrun"
)

// StartDetectAsync 异步执行 detect（带进度日志）。
// 返回 (已启动?, 原因)。
func (s *MigrationService) StartDetectAsync(req *DetectRequest) (*ImportResponse, error) {
	if !detectTracker.Start() {
		return &ImportResponse{OK: false, Message: "已有探测任务正在运行，请等待完成"}, nil
	}
	go s.runDetect(req)
	return &ImportResponse{OK: true, Message: "探测任务已启动"}, nil
}

// runDetect 实际执行 detect（在 goroutine 中），带进度日志。
func (s *MigrationService) runDetect(req *DetectRequest) {
	cfg := db.Config{
		Host: req.Host, Port: req.Port, Database: req.Database,
		Username: req.Username, Password: req.Password,
	}
	ctx := context.Background()

	detectTracker.LogInfo("开始读取源数据库...")
	detectTracker.Update(progress.PhaseInit, "正在预读数据库", 0, 1, 0)

	// 探测面板类型。
	detectTracker.LogInfo("正在识别面板类型...")
	result, err := detector.Detect(ctx, cfg, req.Panel)
	if err != nil {
		detectTracker.Fail("面板探测失败: " + err.Error())
		detectTracker.LogError("面板探测失败: " + err.Error())
		return
	}
	if result.Panel == detector.PanelUnknown {
		detectTracker.Fail("无法识别源库的面板类型")
		detectTracker.LogError("无法识别源库的面板类型")
		return
	}
	detectTracker.LogInfo(fmt.Sprintf("识别为 %s 面板", result.Panel))
	adapter, err := adapterFor(result.Panel)
	if err != nil {
		detectTracker.Fail(err.Error())
		return
	}

	// 执行 adapter 的 detect。
	detectTracker.Update(progress.PhasePlans, "正在统计表行数", 0, 19, 0)
	detectTracker.LogInfo("正在统计各表行数...")
	report, err := adapter.Detect(ctx, cfg)
	if err != nil {
		detectTracker.Fail("生成报告失败: " + err.Error())
		detectTracker.LogError("生成报告失败: " + err.Error())
		return
	}

	// 缓存结果供 GetTaskProgress 返回（detect 完成后前端轮询拿报告）。
	lastDetectReport = report

	detectTracker.Update(progress.PhaseDone, "探测完成", 19, 19, 0)
	detectTracker.LogInfo(fmt.Sprintf("探测完成：共 %d 张表，总行数 %d", len(report.Tables), report.TotalRows))
	detectTracker.Complete(fmt.Sprintf("报告生成成功（%s，总行数 %d）", result.Panel, report.TotalRows))
}

// StartDryRunAsync 异步执行 dry-run（带进度日志）。
func (s *MigrationService) StartDryRunAsync(req *DryRunRequest) (*ImportResponse, error) {
	if !dryRunTracker.Start() {
		return &ImportResponse{OK: false, Message: "已有预演任务正在运行，请等待完成"}, nil
	}
	go s.runDryRun(req)
	return &ImportResponse{OK: true, Message: "预演任务已启动"}, nil
}

// runDryRun 实际执行 dry-run（在 goroutine 中），带进度日志。
func (s *MigrationService) runDryRun(req *DryRunRequest) {
	cfg := db.Config{
		Host: req.Host, Port: req.Port, Database: req.Database,
		Username: req.Username, Password: req.Password,
	}
	ctx := context.Background()

	dryRunTracker.LogInfo("开始预读源数据库...")
	dryRunTracker.Update(progress.PhaseInit, "正在预读数据库", 0, 1, 0)

	dryRunTracker.LogInfo("正在识别面板类型...")
	result, err := detector.Detect(ctx, cfg, req.Panel)
	if err != nil {
		dryRunTracker.Fail("面板探测失败: " + err.Error())
		return
	}
	if result.Panel == detector.PanelUnknown {
		dryRunTracker.Fail("无法识别源库的面板类型")
		return
	}
	dryRunTracker.LogInfo(fmt.Sprintf("识别为 %s 面板，开始冲突检测...", result.Panel))
	adapter, err := adapterFor(result.Panel)
	if err != nil {
		dryRunTracker.Fail(err.Error())
		return
	}

	dryRunTracker.Update(progress.PhasePlans, "正在检测冲突", 0, 9, 0)
	report, err := adapter.DryRun(ctx, cfg, SourceOptions{
		PwdMethod: req.PwdMethod, Salt: req.PasswordSalt,
	})
	if err != nil {
		dryRunTracker.Fail("预演失败: " + err.Error())
		return
	}

	// 记录检测到的问题到日志。
	for _, iss := range report.Issues {
		switch iss.Severity {
		case "error":
			dryRunTracker.LogError(fmt.Sprintf("[%s] %s (受影响 %d)", iss.Category, iss.Message, iss.Count))
		case "warning":
			dryRunTracker.LogWarn(fmt.Sprintf("[%s] %s (受影响 %d)", iss.Category, iss.Message, iss.Count))
		default:
			dryRunTracker.LogInfo(fmt.Sprintf("[%s] %s", iss.Category, iss.Message))
		}
	}

	// 缓存结果。
	lastDryRunReport = report

	canProceed := "不可继续"
	if report.CanProceed {
		canProceed = "可以继续"
	}
	dryRunTracker.Update(progress.PhaseDone, "预演完成", 9, 9, report.ErrorCount)
	dryRunTracker.Complete(fmt.Sprintf("预演完成：%s（错误 %d、警告 %d）",
		canProceed, report.ErrorCount, report.WarningCount))
}

// 缓存最近一次 detect/dry-run 的结果，供完成后的轮询返回（中性类型，跨 adapter）。
var (
	lastDetectReport *DetectData
	lastDryRunReport *dryRunOutcome
)

// TaskProgressResponse 任务进度响应（含日志和完成后的报告）。
type TaskProgressResponse struct {
	progress.Snapshot
	Report any `json:"report,omitempty"` // 完成时的 detect/dryrun 报告
}

// GetTaskProgress 返回指定任务的进度。taskType: detect | dryrun。
func (s *MigrationService) GetTaskProgress(taskType string) *TaskProgressResponse {
	var tracker *progress.Tracker
	var report any
	switch taskType {
	case TaskDryRun:
		tracker = dryRunTracker
		if lastDryRunReport != nil {
			report = dryRunOutcomeToVO(lastDryRunReport)
		}
	default: // detect
		tracker = detectTracker
		if lastDetectReport != nil {
			report = lastDetectReport
		}
	}
	snap := tracker.Snapshot()
	resp := &TaskProgressResponse{Snapshot: snap}
	// 只在任务完成时返回报告，避免运行中返回空报告。
	if snap.Status == progress.StatusCompleted {
		resp.Report = report
	}
	return resp
}
