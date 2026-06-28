// Package data 是迁移服务的数据访问层。
//
// data 层负责：
//   - 连接源端面板库（xiaov2board / v2board / Xboard / PPanel / SSPanel）；
//   - 连接目标端 NPanel 库（通过 NPanel ent client）；
//   - 实现 biz 层定义的 Repo 接口。
//
// 当前为骨架。具体实现见迁移方案第 4 章 adapter/writer 架构。
package data

import "github.com/google/wire"

// ProviderSet 是 data 层的 wire provider 集合。
var ProviderSet = wire.NewSet(NewData)

// Data 持有源端和目标端的连接。
type Data struct {
	// sourceDB *sql.DB        // 源端面板库（按面板类型动态连接）
	// targetClient *ent.Client // 目标端 NPanel ent client
}

// NewData 创建数据层。
// P1 阶段会从 conf 读取连接信息并建立连接；当前骨架仅返回空结构。
func NewData() (*Data, func(), error) {
	cleanup := func() {
		// 关闭源端/目标端连接
	}
	return &Data{}, cleanup, nil
}
