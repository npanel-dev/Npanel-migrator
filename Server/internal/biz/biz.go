// Package biz 是迁移服务的业务用例层。
//
// biz 定义迁移领域的核心用例（连接测试、探测、预演、导入、进度查询），
// 以及源端/目标端的 Repo 接口约定。具体的数据访问实现放在 data 层。
//
// 当前为骨架，按迁移方案第 4 章架构：
//
//	adapter(源端) -> canonical model -> writer(目标端 NPanel)
package biz

import (
	"npanel-migrator/internal/data"

	"github.com/google/wire"
)

// ProviderSet 是 biz 层的 wire provider 集合。
var ProviderSet = wire.NewSet(NewMigrationUsecase)

// MigrationUsecase 是迁移领域用例。
type MigrationUsecase struct {
	data *data.Data
}

// NewMigrationUsecase 创建迁移用例。
// 注入 data.Data：P1 阶段会通过它访问源端/目标端数据库。
func NewMigrationUsecase(d *data.Data) *MigrationUsecase {
	return &MigrationUsecase{data: d}
}
