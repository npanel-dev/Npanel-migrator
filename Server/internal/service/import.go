package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/npanel-dev/NPanel-backend/ent"

	"npanel-migrator/internal/adapter/xiaov2board"
	"npanel-migrator/internal/data/canonical"
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
}

// 全局进度追踪器（单实例，同一时刻只允许一个 import 任务）。
var globalTracker = progress.NewTracker()

// GetProgress 获取当前导入进度（供 GET /api/progress 调用）。
func (s *MigrationService) GetProgress() *progress.Snapshot {
	snap := globalTracker.Snapshot()
	return &snap
}

// StartImport 异步启动导入任务。
// 返回 (已启动?, 原因)。若已有任务运行中则拒绝。
func (s *MigrationService) StartImport(req *ImportRequest) (*ImportResponse, error) {
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
	// 加锁确保只有一个 import 任务。
	if !globalTracker.Start() {
		return &ImportResponse{
			OK:      false,
			Message: "已有迁移任务正在运行，请等待完成",
		}, nil
	}

	batchSize := req.BatchSize
	if batchSize <= 0 {
		batchSize = 500
	}

	// 后台 goroutine 执行导入。
	go s.runImport(req, batchSize)

	return &ImportResponse{
		OK:      true,
		Message: "迁移任务已启动",
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
	if !isV2boardFamily(result.Panel) {
		return fmt.Errorf("已识别为 %s 面板，但该 adapter 暂未实现（当前支持 xiaov2board/v2board）", result.Panel)
	}

	report, err := xiaov2board.DryRun(ctx, sourceCfg)
	if err != nil {
		return fmt.Errorf("迁移前预演失败: %w", err)
	}
	report.Panel = string(result.Panel)
	return dryRunBlockingError(report)
}

func dryRunBlockingError(report *xiaov2board.DryRunReport) error {
	if report == nil {
		return fmt.Errorf("迁移前预演未返回报告，已拒绝开始迁移")
	}
	var blockers []string
	for _, issue := range report.Issues {
		if issue.Severity != xiaov2board.SeverityError {
			continue
		}
		message := issue.Message
		if issue.Count > 0 {
			message = fmt.Sprintf("%s（%d 条）", message, issue.Count)
		}
		blockers = append(blockers, message)
	}
	if len(blockers) == 0 && report.Summary.CanProceed {
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
func (s *MigrationService) runImport(req *ImportRequest, batchSize int) {

	sourceCfg := db.Config{
		Host: req.SourceHost, Port: req.SourcePort, Database: req.SourceDatabase,
		Username: req.SourceUsername, Password: req.SourcePassword,
	}
	targetCfg := writer.NPanelConfig{
		Host: req.TargetHost, Port: req.TargetPort, Database: req.TargetDatabase,
		Username: req.TargetUsername, Password: req.TargetPassword,
	}

	ctx := context.Background()

	result, err := detector.Detect(ctx, sourceCfg, req.SourcePanel)
	if err != nil {
		globalTracker.Fail("面板探测失败: " + err.Error())
		globalTracker.LogError("面板探测失败: " + err.Error())
		return
	}
	if !isV2boardFamily(result.Panel) {
		globalTracker.Fail(fmt.Sprintf("已识别为 %s 面板，但该 adapter 暂未实现（当前支持 xiaov2board/v2board）", result.Panel))
		globalTracker.LogError(fmt.Sprintf("已识别为 %s 面板，但该 adapter 暂未实现（当前支持 xiaov2board/v2board）", result.Panel))
		return
	}
	globalTracker.LogInfo(fmt.Sprintf("识别为 %s 面板，使用 v2board-family adapter", result.Panel))

	// 阶段 1：初始化（建表 + 连接）。
	globalTracker.LogInfo("正在初始化目标库...")
	globalTracker.Update(progress.PhaseInit, "初始化目标库", 0, 1, 0)
	if err := writer.EnsureSchema(ctx, targetCfg); err != nil {
		globalTracker.Fail("初始化目标库失败: " + err.Error())
		globalTracker.LogError("初始化目标库失败: " + err.Error())
		return
	}
	globalTracker.LogInfo("目标库表结构就绪")
	targetClient, err := writer.Open(ctx, targetCfg)
	if err != nil {
		globalTracker.Fail("连接目标库失败: " + err.Error())
		globalTracker.LogError("连接目标库失败: " + err.Error())
		return
	}
	defer targetClient.Close()
	globalTracker.LogInfo("已连接目标库")

	sourceMap := canonical.NewSourceMap()
	modules := req.Modules
	var preparedSubscriptions []*canonical.UserSubscription

	// 阶段 2：节点分组。仅节点迁移需要创建源端节点分组；
	// 已有目标套餐继续使用它自身的节点组，不覆盖。
	if hasModule(modules, ModuleNodes) {
		globalTracker.LogInfo("正在迁移节点分组...")
		groups, err := xiaov2board.ExtractNodeGroups(ctx, sourceCfg)
		if err != nil {
			globalTracker.Fail("读取节点分组失败: " + err.Error())
			globalTracker.LogError("读取节点分组失败: " + err.Error())
			return
		}
		groupIDMap, groupWritten, err := writer.WriteNodeGroups(ctx, targetClient, groups)
		if err != nil {
			globalTracker.Fail("写入节点分组失败: " + err.Error())
			globalTracker.LogError("写入节点分组失败: " + err.Error())
			return
		}
		for src, dst := range groupIDMap {
			sourceMap.NodeGroupIDs[src] = dst
		}
		globalTracker.LogInfo(fmt.Sprintf("节点分组迁移完成：%d/%d 个", groupWritten, len(groups)))
	}

	// 阶段 3：校验用户选择的套餐映射。目标套餐只读，不创建、不覆盖。
	if hasModule(modules, ModulePlans) || hasModule(modules, ModuleOrders) || hasModule(modules, ModuleSubscriptions) {
		globalTracker.LogInfo("正在校验套餐与价格档位映射...")
		globalTracker.Update(progress.PhasePlans, "校验套餐映射", 0, len(req.PlanMappings), 0)
		if err := configurePlanMappings(ctx, targetClient, req.PlanMappings, sourceMap); err != nil {
			globalTracker.Fail("套餐映射校验失败: " + err.Error())
			globalTracker.LogError("套餐映射校验失败: " + err.Error())
			return
		}
		globalTracker.LogInfo(fmt.Sprintf("套餐映射校验完成：%d 个源套餐（未创建目标套餐）", len(req.PlanMappings)))
		globalTracker.Update(progress.PhasePlans, "校验套餐映射", len(req.PlanMappings), len(req.PlanMappings), 0)
	}
	if hasModule(modules, ModuleSubscriptions) {
		preparedSubscriptions, err = xiaov2board.ExtractSubscriptions(ctx, sourceCfg)
		if err != nil {
			globalTracker.Fail("读取订阅分配数据失败: " + err.Error())
			globalTracker.LogError("读取订阅分配数据失败: " + err.Error())
			return
		}
		if err := validateSubscriptionAssignments(ctx, targetClient, preparedSubscriptions, sourceMap, req.TrialAssignment); err != nil {
			globalTracker.Fail("订阅分配预检失败: " + err.Error())
			globalTracker.LogError("订阅分配预检失败: " + err.Error())
			return
		}
	}

	// 阶段 4：用户（分批）+ 认证。
	var processedUsers int
	totalErrors := 0
	if hasModule(modules, ModuleUsers) {
		totalUsers, _ := db.QueryScalar(ctx, sourceCfg, "SELECT COUNT(*) FROM v2_user")
		globalTracker.LogInfo(fmt.Sprintf("开始迁移用户（共 %d 个）...", totalUsers))
		globalTracker.Update(progress.PhaseUsers, "迁移用户", 0, int(totalUsers), totalErrors)
		userErr := xiaov2board.ExtractUsers(ctx, sourceCfg, batchSize, func(batch []*canonical.User) error {
			setUserSourcePanel(batch, string(result.Panel))
			idMap, errs, err := writer.WriteUsers(ctx, targetClient, batch)
			if err != nil {
				return err
			}
			for src, dst := range idMap {
				sourceMap.UserIDs[src] = dst
			}
			processedUsers += len(batch)
			totalErrors += errs
			if processedUsers%batchSize == 0 || processedUsers == int(totalUsers) {
				globalTracker.LogInfo(fmt.Sprintf("已迁移用户 %d/%d", processedUsers, totalUsers))
			}
			globalTracker.Update(progress.PhaseUsers, "迁移用户", processedUsers, int(totalUsers), totalErrors)
			return nil
		})
		if userErr != nil {
			globalTracker.Fail("迁移用户失败: " + userErr.Error())
			globalTracker.LogError("迁移用户失败: " + userErr.Error())
			return
		}
		globalTracker.LogInfo(fmt.Sprintf("用户迁移完成：%d 个（错误 %d）", processedUsers, totalErrors))

		// 阶段 5：邀请关系回填（依赖用户模块）。
		globalTracker.Update(progress.PhaseReferBackfill, "回填邀请关系", 0, 0, 0)
		var allUsers []*canonical.User
		_ = xiaov2board.ExtractUsers(ctx, sourceCfg, batchSize, func(batch []*canonical.User) error {
			setUserSourcePanel(batch, string(result.Panel))
			allUsers = append(allUsers, batch...)
			return nil
		})
		writer.BackfillReferers(ctx, targetClient, allUsers, sourceMap.UserIDs)
	} else {
		globalTracker.LogInfo("已跳过用户迁移（未勾选）")
	}

	// 阶段 6：订单（分批）。
	if hasModule(modules, ModuleOrders) {
		totalOrders, _ := db.QueryScalar(ctx, sourceCfg, "SELECT COUNT(*) FROM v2_order")
		globalTracker.LogInfo(fmt.Sprintf("开始迁移订单（共 %d 条）...", totalOrders))
		globalTracker.Update(progress.PhaseOrders, "迁移订单", 0, int(totalOrders), 0)
		var processedOrders int
		var writtenOrders int
		var orderErrors int
		var unmappedOrderPlans int
		var unmappedOrderPeriods int
		orderErr := xiaov2board.ExtractOrders(ctx, sourceCfg, batchSize, func(batch []*canonical.Order) error {
			for _, order := range batch {
				if order.PlanSourceID <= 0 {
					continue
				}
				if _, ok := sourceMap.PlanIDs[order.PlanSourceID]; !ok {
					unmappedOrderPlans++
					continue
				}
				if order.Period != "" {
					key := canonical.PriceOptionMapKey(order.PlanSourceID, order.Period)
					if _, ok := sourceMap.PriceOptionIDs[key]; !ok {
						unmappedOrderPeriods++
					}
				}
			}
			orderIDMap, batchErrors, err := writer.WriteOrders(ctx, targetClient, batch, sourceMap)
			if err != nil {
				return err
			}
			for src, dst := range orderIDMap {
				sourceMap.OrderIDs[src] = dst
			}
			processedOrders += len(batch)
			writtenOrders += len(orderIDMap)
			orderErrors += batchErrors
			if processedOrders%batchSize == 0 || processedOrders == int(totalOrders) {
				globalTracker.LogInfo(fmt.Sprintf("已迁移订单 %d/%d", processedOrders, totalOrders))
			}
			globalTracker.Update(
				progress.PhaseOrders,
				"迁移订单",
				processedOrders,
				int(totalOrders),
				totalErrors+orderErrors,
			)
			return nil
		})
		if orderErr != nil {
			globalTracker.Fail("迁移订单失败: " + orderErr.Error())
			globalTracker.LogError("迁移订单失败: " + orderErr.Error())
			return
		}
		totalErrors += orderErrors
		globalTracker.LogInfo(fmt.Sprintf(
			"订单迁移完成：成功 %d/%d 条（错误 %d）",
			writtenOrders, processedOrders, orderErrors,
		))
		if orderErrors > 0 {
			globalTracker.LogWarn(fmt.Sprintf(
				"%d 条订单因用户未成功迁移或目标写入冲突而跳过，请检查错误计数和目标数据",
				orderErrors,
			))
		}
		if unmappedOrderPlans > 0 {
			globalTracker.LogWarn(fmt.Sprintf(
				"%d 条历史订单未选择套餐映射，订单已保留但 subscribe_id 为 0",
				unmappedOrderPlans,
			))
		}
		if unmappedOrderPeriods > 0 {
			globalTracker.LogWarn(fmt.Sprintf(
				"%d 条历史订单未选择价格档位映射，订单已保留但价格档位快照为空",
				unmappedOrderPeriods,
			))
		}
	} else {
		globalTracker.LogInfo("已跳过订单迁移（未勾选）")
	}

	// 阶段 7：用户订阅。
	var subWritten int
	if hasModule(modules, ModuleSubscriptions) {
		globalTracker.LogInfo("正在迁移用户订阅...")
		globalTracker.Update(
			progress.PhaseSubscriptions,
			"迁移订阅",
			0,
			len(preparedSubscriptions),
			totalErrors,
		)
		var subErrors int
		subWritten, subErrors, err = writer.WriteSubscriptions(ctx, targetClient, preparedSubscriptions, sourceMap, writer.TrialAssignment{
			SubscribeID:   req.TrialAssignment.TargetSubscribeID,
			DurationUnit:  req.TrialAssignment.DurationUnit,
			DurationValue: req.TrialAssignment.DurationValue,
		})
		if err != nil {
			globalTracker.Fail("写入订阅失败: " + err.Error())
			globalTracker.LogError("写入订阅失败: " + err.Error())
			return
		}
		totalErrors += subErrors
		globalTracker.Update(
			progress.PhaseSubscriptions,
			"迁移订阅",
			len(preparedSubscriptions),
			len(preparedSubscriptions),
			totalErrors,
		)
		globalTracker.LogInfo(fmt.Sprintf(
			"订阅迁移完成：成功 %d/%d 条（错误 %d）",
			subWritten, len(preparedSubscriptions), subErrors,
		))
		if subErrors > 0 {
			globalTracker.LogWarn(fmt.Sprintf(
				"%d 条订阅因用户未成功迁移或目标写入冲突而跳过，请检查目标数据",
				subErrors,
			))
		}
	} else {
		globalTracker.LogInfo("已跳过订阅迁移（未勾选）")
	}

	// 阶段 8：节点。
	if hasModule(modules, ModuleNodes) {
		globalTracker.LogInfo("正在迁移节点...")
		globalTracker.Update(progress.PhaseSubscriptions, "迁移节点", 0, 0, totalErrors)
		nodes, err := xiaov2board.ExtractNodes(ctx, sourceCfg)
		if err != nil {
			globalTracker.LogWarn("读取节点失败: " + err.Error())
		} else {
			nodeWritten, _ := writer.WriteNodes(ctx, targetClient, nodes, sourceMap)
			globalTracker.LogInfo(fmt.Sprintf("节点迁移完成：%d 个", nodeWritten))
		}
	} else {
		globalTracker.LogInfo("已跳过节点迁移（未勾选）")
	}

	// 阶段 9：优惠券。
	if hasModule(modules, ModuleCoupons) {
		globalTracker.LogInfo("正在迁移优惠券...")
		coupons, err := xiaov2board.ExtractCoupons(ctx, sourceCfg)
		if err != nil {
			globalTracker.LogWarn("读取优惠券失败: " + err.Error())
		} else {
			couponWritten, _ := writer.WriteCoupons(ctx, targetClient, coupons)
			globalTracker.LogInfo(fmt.Sprintf("优惠券迁移完成：%d 个", couponWritten))
		}
	} else {
		globalTracker.LogInfo("已跳过优惠券迁移（未勾选）")
	}

	// 阶段 10：公告。
	if hasModule(modules, ModuleNotices) {
		globalTracker.LogInfo("正在迁移公告...")
		notices, err := xiaov2board.ExtractNotices(ctx, sourceCfg)
		if err != nil {
			globalTracker.LogWarn("读取公告失败: " + err.Error())
		} else {
			noticeWritten, _ := writer.WriteNotices(ctx, targetClient, notices)
			globalTracker.LogInfo(fmt.Sprintf("公告迁移完成：%d 条", noticeWritten))
		}
	} else {
		globalTracker.LogInfo("已跳过公告迁移（未勾选）")
	}

	// 阶段 11：工单 + 工单消息。
	if hasModule(modules, ModuleTickets) {
		globalTracker.LogInfo("正在迁移工单...")
		tickets, err := xiaov2board.ExtractTickets(ctx, sourceCfg)
		if err != nil {
			globalTracker.LogWarn("读取工单失败: " + err.Error())
		} else {
			ticketWritten, _ := writer.WriteTickets(ctx, targetClient, tickets, sourceMap)
			globalTracker.LogInfo(fmt.Sprintf("工单迁移完成：%d 个", ticketWritten))
		}
	} else {
		globalTracker.LogInfo("已跳过工单迁移（未勾选）")
	}

	globalTracker.Complete(fmt.Sprintf(
		"迁移完成：用户 %d、订阅 %d（详见日志）",
		processedUsers, subWritten,
	))
}

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
