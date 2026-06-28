// Package writer 把 canonical model 写入 NPanel 数据库（通过 NPanel ent client）。
//
// 这是迁移链路的最后一环：canonical → NPanel。
// 严格按迁移方案第 16.2 节：用 NPanel ent client 写入，不拼裸 SQL，
// 让 ent 自动处理 JSON 字段、指针可空字段、默认值、唯一约束。
//
// 关键映射规则（见方案第 6 章）：
//   - 永久订阅：expire_time = Unix 0（'1970-01-01'），不用 NULL
//   - status=4：被后续订阅取代则跳过，孤立则转 status=3
//   - 密码 algo：原样写入，依赖 NPanel 已补 sha256salt 分支
//   - 时间戳：Unix 秒 → time.Time
package writer

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/npanel-dev/NPanel-backend/ent"

	// ent 用 mysql 驱动
	_ "github.com/go-sql-driver/mysql"
)

// NPanelConfig NPanel 目标库连接配置。
type NPanelConfig struct {
	Host     string
	Port     int
	Database string
	Username string
	Password string
}

// DSN 构造 NPanel MySQL DSN（带连接超时，避免对不可达地址长时间阻塞）。
func (c NPanelConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=True&loc=Local&timeout=5s&readTimeout=10s&writeTimeout=10s",
		c.Username, c.Password, c.Host, c.Port, c.Database)
}

// testConnect 测试数据库连通性（5s 超时），避免 ent 连接池对不可达地址的重试阻塞。
func testConnect(cfg NPanelConfig) error {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

// Open 打开 NPanel ent client（已验证连通性）。
// 调用方负责 client.Close()。
func Open(ctx context.Context, cfg NPanelConfig) (*ent.Client, error) {
	// 先用 database/sql 快速验证连通性，避免 ent 连接池对不可达地址长时间阻塞。
	if err := testConnect(cfg); err != nil {
		return nil, fmt.Errorf("连接 NPanel 失败: %w", err)
	}
	client, err := ent.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("打开 NPanel ent client 失败: %w", err)
	}
	return client, nil
}

// EnsureSchema 确保目标库表结构存在（用 ent Schema.Create 建表）。
// 幂等：表已存在则跳过。方案 16.5 要求 migrator 用 client.Schema.Create 建表。
func EnsureSchema(ctx context.Context, cfg NPanelConfig) error {
	// 先验证连通性，快速失败。
	if err := testConnect(cfg); err != nil {
		return fmt.Errorf("连接 NPanel 失败: %w", err)
	}

	client, err := ent.Open("mysql", cfg.DSN())
	if err != nil {
		return err
	}
	defer client.Close()

	// 给建表操作设超时，避免长时间阻塞。
	schemaCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := client.Schema.Create(schemaCtx); err != nil {
		return fmt.Errorf("创建 NPanel 表结构失败: %w", err)
	}
	return nil
}
