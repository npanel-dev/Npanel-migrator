package service

import (
	"context"
	"fmt"

	"npanel-migrator/internal/adapter/xiaov2board"
	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
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
}

// 迁移模块标识（前端勾选项的 value）。
const (
	ModuleUsers         = "users"         // 用户 + 认证 + 邀请关系
	ModulePlans         = "plans"         // 套餐 + 价格档位
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

	// 阶段 2：节点分组。套餐、订阅和节点权限都依赖 node_group。
	if hasModule(modules, ModulePlans) || hasModule(modules, ModuleSubscriptions) || hasModule(modules, ModuleNodes) {
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

	// 阶段 3：套餐 + 价格档位。
	if hasModule(modules, ModulePlans) {
		globalTracker.LogInfo("正在读取套餐数据...")
		globalTracker.Update(progress.PhasePlans, "迁移套餐", 0, 0, 0)
		plans, err := xiaov2board.ExtractPlans(ctx, sourceCfg)
		if err != nil {
			globalTracker.Fail("读取套餐失败: " + err.Error())
			globalTracker.LogError("读取套餐失败: " + err.Error())
			return
		}
		planIDMap, planErr, err := writer.WritePlans(ctx, targetClient, plans, sourceMap)
		if err != nil {
			globalTracker.Fail("写入套餐失败: " + err.Error())
			globalTracker.LogError("写入套餐失败: " + err.Error())
			return
		}
		sourceMap.PlanIDs = planIDMap
		globalTracker.LogInfo(fmt.Sprintf("套餐迁移完成：%d 个套餐", len(plans)))
		globalTracker.Update(progress.PhasePlans, "迁移套餐", len(plans), len(plans), planErr)
	} else {
		globalTracker.LogInfo("已跳过套餐迁移（未勾选）")
	}

	// 阶段 4：用户（分批）+ 认证。
	var processedUsers int
	totalErrors := 0
	if hasModule(modules, ModuleUsers) {
		totalUsers, _ := db.QueryScalar(ctx, sourceCfg, "SELECT COUNT(*) FROM v2_user")
		globalTracker.LogInfo(fmt.Sprintf("开始迁移用户（共 %d 个）...", totalUsers))
		globalTracker.Update(progress.PhaseUsers, "迁移用户", 0, int(totalUsers), totalErrors)
		userErr := xiaov2board.ExtractUsers(ctx, sourceCfg, batchSize, func(batch []*canonical.User) error {
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
		orderErr := xiaov2board.ExtractOrders(ctx, sourceCfg, batchSize, func(batch []*canonical.Order) error {
			orderIDMap, _, err := writer.WriteOrders(ctx, targetClient, batch, sourceMap)
			if err != nil {
				return err
			}
			for src, dst := range orderIDMap {
				sourceMap.OrderIDs[src] = dst
			}
			processedOrders += len(batch)
			if processedOrders%batchSize == 0 || processedOrders == int(totalOrders) {
				globalTracker.LogInfo(fmt.Sprintf("已迁移订单 %d/%d", processedOrders, totalOrders))
			}
			globalTracker.Update(progress.PhaseOrders, "迁移订单", processedOrders, int(totalOrders), 0)
			return nil
		})
		if orderErr != nil {
			globalTracker.Fail("迁移订单失败: " + orderErr.Error())
			globalTracker.LogError("迁移订单失败: " + orderErr.Error())
			return
		}
		globalTracker.LogInfo(fmt.Sprintf("订单迁移完成：%d 条", processedOrders))
	} else {
		globalTracker.LogInfo("已跳过订单迁移（未勾选）")
	}

	// 阶段 7：用户订阅。
	var subWritten int
	if hasModule(modules, ModuleSubscriptions) {
		globalTracker.LogInfo("正在迁移用户订阅...")
		globalTracker.Update(progress.PhaseSubscriptions, "迁移订阅", 0, 0, 0)
		subs, err := xiaov2board.ExtractSubscriptions(ctx, sourceCfg)
		if err != nil {
			globalTracker.Fail("读取订阅失败: " + err.Error())
			globalTracker.LogError("读取订阅失败: " + err.Error())
			return
		}
		subWritten, _, err = writer.WriteSubscriptions(ctx, targetClient, subs, sourceMap)
		if err != nil {
			globalTracker.Fail("写入订阅失败: " + err.Error())
			globalTracker.LogError("写入订阅失败: " + err.Error())
			return
		}
		globalTracker.LogInfo(fmt.Sprintf("订阅迁移完成：%d 条", subWritten))
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
