package writer

import (
	"testing"

	"github.com/npanel-dev/NPanel-backend/ent"
)

// TestEntImportable 验证 NPanel ent 包能被 migrator 引入并使用类型。
// 不连真实数据库，只验证编译期的类型可用性（ent client + 各表 builder）。
func TestEntImportable(t *testing.T) {
	// 验证 Client 类型可引用。
	var _ *ent.Client
	// 验证各表的 Create builder 方法存在（编译期检查）。
	_ = func(c *ent.Client) {
		_ = c.ProxyUser.Create()
		_ = c.ProxySubscribe.Create()
		_ = c.ProxyOrder.Create()
		_ = c.ProxyUserSubscribe.Create()
		_ = c.ProxySubscribePriceOption.Create()
		_ = c.ProxyUserAuthMethod.Create()
	}
}
