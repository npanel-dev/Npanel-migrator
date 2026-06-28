package server

import (
	"encoding/json"
	"io"

	"npanel-migrator/internal/service"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// registerAPIRoutes 注册 /api/* 业务接口路由。
//
// 当前提供两个接口：
//   GET  /api/health            健康检查
//   POST /api/test-connection   测试数据库连接（源端探测面板/目标端校验 NPanel）
//
// 下一步会接入 detect / dry-run / import / progress。
func registerAPIRoutes(srv *khttp.Server, svc *service.MigrationService) {
	route := srv.Route("/api")

	// GET /api/health
	route.GET("/health", func(ctx khttp.Context) error {
		h := svc.Health()
		return ctx.JSON(200, h)
	})

	// POST /api/test-connection
	route.POST("/test-connection", func(ctx khttp.Context) error {
		body, err := io.ReadAll(ctx.Request().Body)
		if err != nil {
			return writeError(ctx, 400, "读取请求体失败: "+err.Error())
		}
		var req service.TestConnectionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return writeError(ctx, 400, "解析请求体失败: "+err.Error())
		}

		resp, err := svc.TestConnection(ctx, &req)
		if err != nil {
			return writeError(ctx, 500, err.Error())
		}
		return ctx.JSON(200, resp)
	})

	// POST /api/detect —— 扫描源库，生成迁移前报告（表行数 + 关键指标）
	route.POST("/detect", func(ctx khttp.Context) error {
		body, err := io.ReadAll(ctx.Request().Body)
		if err != nil {
			return writeError(ctx, 400, "读取请求体失败: "+err.Error())
		}
		var req service.DetectRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return writeError(ctx, 400, "解析请求体失败: "+err.Error())
		}

		resp, err := svc.Detect(ctx, &req)
		if err != nil {
			return writeError(ctx, 500, err.Error())
		}
		return ctx.JSON(200, resp)
	})

	// POST /api/detect/start —— 异步启动 detect（带进度日志）
	route.POST("/detect/start", func(ctx khttp.Context) error {
		body, err := io.ReadAll(ctx.Request().Body)
		if err != nil {
			return writeError(ctx, 400, "读取请求体失败: "+err.Error())
		}
		var req service.DetectRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return writeError(ctx, 400, "解析请求体失败: "+err.Error())
		}
		resp, err := svc.StartDetectAsync(&req)
		if err != nil {
			return writeError(ctx, 500, err.Error())
		}
		return ctx.JSON(200, resp)
	})

	// POST /api/dry-run/start —— 异步启动 dry-run（带进度日志）
	route.POST("/dry-run/start", func(ctx khttp.Context) error {
		body, err := io.ReadAll(ctx.Request().Body)
		if err != nil {
			return writeError(ctx, 400, "读取请求体失败: "+err.Error())
		}
		var req service.DryRunRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return writeError(ctx, 400, "解析请求体失败: "+err.Error())
		}
		resp, err := svc.StartDryRunAsync(&req)
		if err != nil {
			return writeError(ctx, 500, err.Error())
		}
		return ctx.JSON(200, resp)
	})

	// GET /api/task/progress?type=detect|dryrun —— 查询任务进度（含日志）
	route.GET("/task/progress", func(ctx khttp.Context) error {
		taskType := ctx.Query().Get("type")
		if taskType == "" {
			taskType = "detect"
		}
		resp := svc.GetTaskProgress(taskType)
		return ctx.JSON(200, resp)
	})

	// POST /api/dry-run —— 预演迁移（只读不写，检测冲突）
	route.POST("/dry-run", func(ctx khttp.Context) error {
		body, err := io.ReadAll(ctx.Request().Body)
		if err != nil {
			return writeError(ctx, 400, "读取请求体失败: "+err.Error())
		}
		var req service.DryRunRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return writeError(ctx, 400, "解析请求体失败: "+err.Error())
		}

		resp, err := svc.DryRun(ctx, &req)
		if err != nil {
			return writeError(ctx, 500, err.Error())
		}
		return ctx.JSON(200, resp)
	})

	// POST /api/import —— 启动迁移（异步执行）
	route.POST("/import", func(ctx khttp.Context) error {
		body, err := io.ReadAll(ctx.Request().Body)
		if err != nil {
			return writeError(ctx, 400, "读取请求体失败: "+err.Error())
		}
		var req service.ImportRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return writeError(ctx, 400, "解析请求体失败: "+err.Error())
		}

		resp, err := svc.StartImport(&req)
		if err != nil {
			return writeError(ctx, 500, err.Error())
		}
		return ctx.JSON(200, resp)
	})

	// GET /api/progress —— 查询迁移进度（轮询）
	route.GET("/progress", func(ctx khttp.Context) error {
		snap := svc.GetProgress()
		return ctx.JSON(200, snap)
	})
}

// writeError 写 JSON 错误响应。
// 迁移工具希望直接返回简单 JSON 给前端，不用 Kratos 的错误格式。
func writeError(ctx khttp.Context, code int, msg string) error {
	ctx.Response().Header().Set("Content-Type", "application/json")
	ctx.Response().WriteHeader(code)
	return json.NewEncoder(ctx.Response()).Encode(map[string]any{
		"ok":      false,
		"message": msg,
	})
}
