// Package server 装配迁移服务的 HTTP 服务。
//
// HTTP 服务同时承担两个职责：
//  1. 暴露 /api/* REST 接口（test-connection / detect / dry-run / import / progress）；
//  2. 以 embed 方式托管前端 Vue 构建产物，并提供 SPA history fallback，
//     使前端 vue-router 的 history 模式路由能正常刷新。
//
// 这样迁移服务编译为单二进制后，开箱即可访问完整 UI。
package server

import (
	"time"

	"npanel-migrator/internal/conf"
	"npanel-migrator/internal/service"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/google/wire"
)

// ProviderSet 是 server 层的 wire provider 集合。
var ProviderSet = wire.NewSet(NewHTTPServer)

// NewHTTPServer 创建 HTTP 服务。
//
// c 为服务配置，svc 为迁移业务 service。assets 由 build 脚本从
// ../Vue/dist 拷贝到 internal/server/assets 后再 embed。
func NewHTTPServer(c *conf.Server, svc *service.MigrationService) *http.Server {
	opts := []http.ServerOption{}
	if c.HTTP.Network != "" {
		opts = append(opts, http.Network(c.HTTP.Network))
	}
	if c.HTTP.Addr != "" {
		opts = append(opts, http.Address(c.HTTP.Addr))
	}
	if c.HTTP.Timeout != "" {
		if d, err := time.ParseDuration(c.HTTP.Timeout); err == nil {
			opts = append(opts, http.Timeout(d))
		}
	}
	srv := http.NewServer(opts...)

	// 1) 业务接口路由 /api/*
	registerAPIRoutes(srv, svc)

	// 2) 前端静态资源 + SPA fallback（根路径前缀兜底）
	// 用 HandlePrefix 而非 Handle：gorilla/mux 的 Handle("/") 是精确匹配，
	// 子路径（/assets/xxx、/migration）不会被捕获；HandlePrefix("/") 才能兜底所有路径。
	srv.HandlePrefix("/", newSPAHandler(embeddedAssets))

	log.Info("HTTP server listening, UI served from embedded assets")
	return srv
}
