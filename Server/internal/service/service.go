// Package service 是迁移服务的接口层（transport 层）。
//
// service 负责把 HTTP 请求解析成 DTO、调用 biz/data 层、再把结果序列化为
// JSON 响应。实际的迁移逻辑在 biz 层，数据库连接探测在 data 层。
package service

import (
	"context"
	"fmt"

	"npanel-migrator/internal/adapter/xiaov2board"
	"npanel-migrator/internal/biz"
	"npanel-migrator/internal/data/db"
	"npanel-migrator/internal/data/detector"
)

// ProviderSet 是 service 层的 wire provider 集合。
var ProviderSet = NewMigrationService

// MigrationService 是迁移服务的入口，注入到 HTTP server。
type MigrationService struct {
	uc *biz.MigrationUsecase
}

// NewMigrationService 创建迁移服务。
func NewMigrationService(uc *biz.MigrationUsecase) *MigrationService {
	return &MigrationService{uc: uc}
}

// ---- 请求/响应 DTO（与前端 api/index.ts 对应）----

// TestConnectionRequest 测试连接请求。
type TestConnectionRequest struct {
	Side     string `json:"side"`     // "source" | "target"
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
	// Lang 当前界面语言（"zh-CN" 或 "en"），用于错误提示的本地化。
	Lang string `json:"lang"`
}

// TestConnectionResponse 测试连接响应。
type TestConnectionResponse struct {
	OK      bool            `json:"ok"`
	Message string          `json:"message"`
	Detail  *ConnectionInfo `json:"detail,omitempty"`
}

// ConnectionInfo 连接成功后附带的信息（源端含面板探测，目标端含校验）。
type ConnectionInfo struct {
	// Panel 探测出的面板类型（仅 source 有意义）。
	Panel       string   `json:"panel,omitempty"`
	Confidence  string   `json:"confidence,omitempty"`
	MatchTables []string `json:"matchTables,omitempty"`
	// IsNPanelTarget 目标端是否为有效 NPanel 库（仅 target 有意义）。
	IsNPanelTarget bool `json:"isNPanelTarget,omitempty"`
	// 数据库类型与版本（源端和目标端都显示）。
	DBType     string `json:"dbType,omitempty"`     // mysql / mariadb / percona
	DBVersion  string `json:"dbVersion,omitempty"`  // 完整版本号 "8.4.6"、"10.11.8-MariaDB"
	DBMajor    string `json:"dbMajor,omitempty"`    // 主版本号 "5.7"、"8.4"、"10.11"
}

// HealthResponse 健康检查响应。
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// ---- 业务方法 ----

// TestConnection 测试数据库连接。
// source 端：连接成功后探测面板类型；target 端：校验是否为 NPanel 库。
func (s *MigrationService) TestConnection(ctx context.Context, req *TestConnectionRequest) (*TestConnectionResponse, error) {
	// 0. 先校验 side 参数（在拨号前，避免无意义连接）。
	if req.Side != "source" && req.Side != "target" {
		return &TestConnectionResponse{
			OK:      false,
			Message: "side 参数必须为 source 或 target",
		}, nil
	}

	cfg := db.Config{
		Host:     req.Host,
		Port:     req.Port,
		Database: req.Database,
		Username: req.Username,
		Password: req.Password,
	}

	// 1. 再测连通性。
	if err := db.Ping(ctx, cfg); err != nil {
		// 把底层 MySQL 错误翻译成友好的本地化提示。
		zhMsg, enMsg := db.FriendlyError(err, req.Lang)
		msg := zhMsg
		if req.Lang == "en" {
			msg = enMsg
		}
		return &TestConnectionResponse{
			OK:      false,
			Message: msg,
		}, nil
	}

	// 1.5 查询数据库版本号并解析类型（mysql/mariadb + 主版本号）。
	// 版本查询失败不影响连接成功的结论。
	dbFullVersion, _ := db.ServerVersion(ctx, cfg)
	dbType := db.ParseDBType(dbFullVersion)

	// 2. 按端类型做进一步探测。
	switch req.Side {
	case "source":
		// 源端：探测面板类型（前端声明的 panel 暂不从请求取，靠探测）。
		result, err := detector.Detect(ctx, cfg, "")
		if err != nil {
			// 连通但探测失败：仍算连接成功，但提示探测异常。
			return &TestConnectionResponse{
				OK:       true,
				Message:  "数据库连接成功，但面板类型探测失败：" + err.Error(),
				Detail:   &ConnectionInfo{DBType: dbType.Type, DBVersion: dbType.FullVersion, DBMajor: dbType.MajorMinor},
			}, nil
		}
		return &TestConnectionResponse{
			OK:      true,
			Message: fmt.Sprintf("连接成功，识别为 %s 面板", result.Panel),
			Detail: &ConnectionInfo{
				Panel:       string(result.Panel),
				Confidence:  result.Confidence,
				MatchTables: result.MatchedTables,
				DBType:      dbType.Type,
				DBVersion:   dbType.FullVersion,
				DBMajor:     dbType.MajorMinor,
			},
		}, nil

	case "target":
		// 目标端：校验是否为 NPanel 库。
		isNPanel, err := detector.IsNPanel(ctx, cfg)
		if err != nil {
			return &TestConnectionResponse{
				OK:      true,
				Message: "数据库连接成功，但 NPanel 表结构校验失败：" + err.Error(),
				Detail:  &ConnectionInfo{DBType: dbType.Type, DBVersion: dbType.FullVersion, DBMajor: dbType.MajorMinor},
			}, nil
		}
		msg := "连接成功"
		if isNPanel {
			msg += "，已确认是 NPanel 库（核心表齐全）"
		} else {
			msg += "，⚠️ 但未检测到完整的 NPanel 核心表（user/subscribe/order/user_subscribe），请确认目标库"
		}
		return &TestConnectionResponse{
			OK:      true,
			Message: msg,
			Detail: &ConnectionInfo{
				IsNPanelTarget: isNPanel,
				DBType:         dbType.Type,
				DBVersion:      dbType.FullVersion,
				DBMajor:        dbType.MajorMinor,
			},
		}, nil
	}

	// 理论上不会走到这里（side 已在第 0 步校验），满足编译器。
	return &TestConnectionResponse{
		OK:      false,
		Message: "未知的 side 参数",
	}, nil
}

// Health 健康检查。
func (s *MigrationService) Health() *HealthResponse {
	return &HealthResponse{
		Status:  "ok",
		Version: version,
	}
}

// version 由 main 通过 ldflags 注入（-X main.Version=xxx）。
// service 包独立持有副本，避免循环引用 main。
var version = "dev"

// ---- detect 阶段 ----

// DetectRequest detect 请求（复用连接配置）。
type DetectRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
	// Panel 前端声明的面板类型（可选，用于校验；不填则自动探测）。
	Panel string `json:"panel"`
}

// DetectResponse detect 响应。
// Report 是通用结构，各 adapter 产出同构数据，前端统一渲染。
type DetectResponse struct {
	OK      bool        `json:"ok"`
	Message string      `json:"message"`
	Report  *DetectData `json:"report,omitempty"`
}

// DetectData 迁移前报告（与 adapter 的 DetectReport 字段对齐）。
// 这里用独立结构而非直接复用 adapter 类型，避免 service 直接依赖 adapter 包
// （service 只依赖 biz，adapter 通过 biz 注入更符合分层；当前骨架简化处理）。
type DetectData struct {
	Panel     string          `json:"panel"`
	Tables    []TableStat     `json:"tables"`
	Metrics   DetectMetrics   `json:"metrics"`
	TotalRows int64           `json:"totalRows"`
}

// TableStat service 层的表统计 DTO。
type TableStat struct {
	Name    string `json:"name"`
	Rows    int64  `json:"rows"`
	Comment string `json:"comment"`
}

// DetectMetrics 关键业务指标 DTO。
type DetectMetrics struct {
	UserTotal         int64 `json:"userTotal"`
	UserActive        int64 `json:"userActive"`
	UserBanned        int64 `json:"userBanned"`
	BalanceTotal      int64 `json:"balanceTotal"`
	ActiveSubscribers int64 `json:"activeSubscribers"`
	PlanTotal         int64 `json:"planTotal"`
	PlanOnSale        int64 `json:"planOnSale"`
	OrderTotal        int64 `json:"orderTotal"`
	OrderCompleted    int64 `json:"orderCompleted"`
	NodeTotal         int64 `json:"nodeTotal"`
	CouponEnable      int64 `json:"couponEnable"`
	TicketOpen        int64 `json:"ticketOpen"`
}

// Detect 执行 detect 阶段。
// 根据 panel（前端声明或自动探测结果）调用对应 adapter。
func (s *MigrationService) Detect(ctx context.Context, req *DetectRequest) (*DetectResponse, error) {
	cfg := db.Config{
		Host:     req.Host,
		Port:     req.Port,
		Database: req.Database,
		Username: req.Username,
		Password: req.Password,
	}

	// 1. 先探测面板类型（以探测结果为准，前端声明仅作校验）。
	result, err := detector.Detect(ctx, cfg, req.Panel)
	if err != nil {
		return &DetectResponse{OK: false, Message: "面板探测失败: " + err.Error()}, nil
	}
	if result.Panel == detector.PanelUnknown {
		return &DetectResponse{
			OK:      false,
			Message: "无法识别源库的面板类型，请确认数据库是否为受支持的面板",
		}, nil
	}

	// 2. 按探测到的面板类型调用对应 adapter。
	//    当前只实现 xiaov2board，其他面板待后续迭代。
	switch result.Panel {
	case detector.PanelXiaoV2board:
		report, err := xiaov2board.Detect(ctx, cfg)
		if err != nil {
			return &DetectResponse{OK: false, Message: "生成报告失败: " + err.Error()}, nil
		}
		return &DetectResponse{
			OK:      true,
			Message: "报告生成成功",
			Report:  convertReport(report),
		}, nil

	default:
		return &DetectResponse{
			OK:      false,
			Message: fmt.Sprintf("已识别为 %s 面板，但该 adapter 暂未实现（当前仅支持 xiaov2board）", result.Panel),
		}, nil
	}
}

// convertReport 把 adapter 的 DetectReport 转成 service 层 DTO。
func convertReport(r *xiaov2board.DetectReport) *DetectData {
	d := &DetectData{
		Panel:     r.Panel,
		TotalRows: r.TotalRows,
		Metrics: DetectMetrics{
			UserTotal:         r.Metrics.UserTotal,
			UserActive:        r.Metrics.UserActive,
			UserBanned:        r.Metrics.UserBanned,
			BalanceTotal:      r.Metrics.BalanceTotal,
			ActiveSubscribers: r.Metrics.ActiveSubscribers,
			PlanTotal:         r.Metrics.PlanTotal,
			PlanOnSale:        r.Metrics.PlanOnSale,
			OrderTotal:        r.Metrics.OrderTotal,
			OrderCompleted:    r.Metrics.OrderCompleted,
			NodeTotal:         r.Metrics.NodeTotal,
			CouponEnable:      r.Metrics.CouponEnable,
			TicketOpen:        r.Metrics.TicketOpen,
		},
	}
	for _, t := range r.Tables {
		d.Tables = append(d.Tables, TableStat{
			Name: t.Name, Rows: t.Rows, Comment: t.Comment,
		})
	}
	return d
}

// ---- dry-run 预演阶段 ----

// DryRunRequest dry-run 请求（复用连接配置）。
type DryRunRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
	Panel    string `json:"panel"`
}

// DryRunResponse dry-run 响应。
type DryRunResponse struct {
	OK      bool            `json:"ok"`
	Message string          `json:"message"`
	Report  *DryRunReportVO `json:"report,omitempty"`
}

// DryRunReportVO dry-run 报告 DTO（与 adapter 的 DryRunReport 字段对齐）。
type DryRunReportVO struct {
	Panel   string             `json:"panel"`
	Issues  []IssueVO          `json:"issues"`
	Summary DryRunSummaryVO    `json:"summary"`
}

// IssueVO 单个问题 DTO。
type IssueVO struct {
	Severity string   `json:"severity"` // error / warning / info
	Category string   `json:"category"`
	Message  string   `json:"message"`
	Count    int64    `json:"count"`
	Sample   []string `json:"sample"`
}

// DryRunSummaryVO 问题汇总 DTO。
type DryRunSummaryVO struct {
	ErrorCount   int  `json:"errorCount"`
	WarningCount int  `json:"warningCount"`
	InfoCount    int  `json:"infoCount"`
	CanProceed   bool `json:"canProceed"`
}

// DryRun 执行 dry-run 预演（只读不写，检测冲突）。
func (s *MigrationService) DryRun(ctx context.Context, req *DryRunRequest) (*DryRunResponse, error) {
	cfg := db.Config{
		Host:     req.Host,
		Port:     req.Port,
		Database: req.Database,
		Username: req.Username,
		Password: req.Password,
	}

	// 先探测面板类型。
	result, err := detector.Detect(ctx, cfg, req.Panel)
	if err != nil {
		return &DryRunResponse{OK: false, Message: "面板探测失败: " + err.Error()}, nil
	}
	if result.Panel == detector.PanelUnknown {
		return &DryRunResponse{
			OK:      false,
			Message: "无法识别源库的面板类型",
		}, nil
	}

	switch result.Panel {
	case detector.PanelXiaoV2board:
		report, err := xiaov2board.DryRun(ctx, cfg)
		if err != nil {
			return &DryRunResponse{OK: false, Message: "预演失败: " + err.Error()}, nil
		}
		return &DryRunResponse{
			OK:      true,
			Message: "预演完成",
			Report:  convertDryRunReport(report),
		}, nil

	default:
		return &DryRunResponse{
			OK:      false,
			Message: fmt.Sprintf("已识别为 %s 面板，但该 adapter 暂未实现（当前仅支持 xiaov2board）", result.Panel),
		}, nil
	}
}

// convertDryRunReport 把 adapter 的 DryRunReport 转成 service 层 DTO。
func convertDryRunReport(r *xiaov2board.DryRunReport) *DryRunReportVO {
	vo := &DryRunReportVO{
		Panel: r.Panel,
		Summary: DryRunSummaryVO{
			ErrorCount:   r.Summary.ErrorCount,
			WarningCount: r.Summary.WarningCount,
			InfoCount:    r.Summary.InfoCount,
			CanProceed:   r.Summary.CanProceed,
		},
	}
	for _, iss := range r.Issues {
		vo.Issues = append(vo.Issues, IssueVO{
			Severity: string(iss.Severity),
			Category: iss.Category,
			Message:  iss.Message,
			Count:    iss.Count,
			Sample:   iss.Sample,
		})
	}
	return vo
}
