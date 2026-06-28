// Package main 是 NPanel 迁移服务的入口。
//
// 启动流程（Kratos 标准模式）：
//  1. 解析 -conf 参数指向配置目录（默认 ../configs）；
//  2. 加载 config.yaml；
//  3. 通过 wire 装配 server/service/biz/data；
//  4. 启动 HTTP 服务（同时托管 embed 的前端 UI）。
//
// 构建与运行：
//
//	make build              # 编译前端 + embed + 单二进制
//	./bin/npanel-migrator -conf ./configs
//
// 单二进制部署：build 产物包含完整 UI，拷贝到任意机器 + 一个 config.yaml 即可运行。
package main

import (
	"flag"
	"os"

	"npanel-migrator/internal/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/file"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/http"

	_ "go.uber.org/automaxprocs"
)

// go build -ldflags "-X main.Version=x.y.z"
var (
	// Name 是服务名。
	Name = "npanel-migrator"
	// Version 是版本号，构建时注入。
	Version string
	// flagconf 是配置目录。
	flagconf string

	id, _ = os.Hostname()
)

func init() {
	flag.StringVar(&flagconf, "conf", "../../configs", "config path, eg: -conf config.yaml")
}

// newApp 组装 Kratos 应用。
func newApp(logger log.Logger, hs *http.Server) *kratos.App {
	return kratos.New(
		kratos.ID(id),
		kratos.Name(Name),
		kratos.Version(Version),
		kratos.Metadata(map[string]string{}),
		kratos.Logger(logger),
		kratos.Server(hs),
	)
}

func main() {
	flag.Parse()

	logger := log.NewStdLogger(os.Stdout)
	// 给 logger 加上 service 维度的字段。
	logger = log.With(logger,
		"service.id", id,
		"service.name", Name,
		"service.version", Version,
	)

	c := config.New(
		config.WithSource(
			file.NewSource(flagconf),
		),
	)
	defer c.Close()

	if err := c.Load(); err != nil {
		panic(err)
	}

	var bc conf.Bootstrap
	if err := c.Scan(&bc); err != nil {
		panic(err)
	}

	app, cleanup, err := wireApp(&bc, logger)
	if err != nil {
		panic(err)
	}
	defer cleanup()

	if err := app.Run(); err != nil {
		panic(err)
	}
}
