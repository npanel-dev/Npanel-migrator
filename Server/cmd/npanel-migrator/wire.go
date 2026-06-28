//go:build wireinject
// +build wireinject

// wire.go 是 wire 依赖注入的入口声明（仅 wire 读取，不参与正常编译）。
// wire 会据此生成 wire_gen.go，把 server/service/biz/data 各层组装起来。
package main

import (
	"npanel-migrator/internal/biz"
	"npanel-migrator/internal/conf"
	"npanel-migrator/internal/data"
	"npanel-migrator/internal/server"
	"npanel-migrator/internal/service"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

// wireApp 装配 Kratos 应用。
// 实际实现在 wire 生成的 wire_gen.go 中。
func wireApp(*conf.Bootstrap, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(
		server.ProviderSet,
		data.ProviderSet,
		biz.ProviderSet,
		service.ProviderSet,
		newConfProviders,
		newApp,
	))
}

// newConfProviders 把 Bootstrap 拆成 server 层需要的 conf.Server。
func newConfProviders(bc *conf.Bootstrap) *conf.Server {
	return bc.Server
}
