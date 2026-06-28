// Package conf 定义迁移服务运行时配置结构。
//
// 不使用 protobuf 定义：迁移服务是内部运维工具，纯 Go struct + YAML
// 比 proto/buf/aip 那一套更直接、依赖更少，便于单二进制 embed 部署。
package conf

// Bootstrap 是 configs/config.yaml 的根配置。
type Bootstrap struct {
	Server *Server `yaml:"server"`
}

// Server 配置 HTTP 监听。
type Server struct {
	HTTP *HTTP `yaml:"http"`
}

// HTTP 监听参数。
//
// Timeout 用字符串表示（如 "60s"），在 server 层用 time.ParseDuration 解析，
// 避免 YAML 直接解码到 time.Duration 失败（"60s" 无法自动转 Duration）。
type HTTP struct {
	Network string `yaml:"network"`
	Addr    string `yaml:"addr"`
	Timeout string `yaml:"timeout"`
}
