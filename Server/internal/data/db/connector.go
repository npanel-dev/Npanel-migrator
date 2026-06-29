// Package db 提供迁移服务用的底层 MySQL 连接能力。
//
// 这里只做"连接 + 简单查询"，不引入 GORM/ent：
//   - 源端：各面板库结构差异大，用 database/sql 原生查询最灵活；
//   - 目标端 NPanel：写入时才用 ent client（见迁移方案第 16.2 节），
//     连接测试阶段同样用原生 SQL 探测表结构即可。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Config 是数据库连接配置（与前端 DatabaseConfig 对应）。
type Config struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// DSN 构造 MySQL DSN。
// parseTime=True 让时间字段扫描到 time.Time；loc=Local 用本地时区。
// charset=utf8mb4 显式指定连接字符集，兼容 MySQL 5.7（默认 latin1，否则读 utf8mb4 表的
// emoji/4 字节字符会乱码）。go-sql-driver 在指定 charset 后会自动 SET NAMES。
func (c Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=True&loc=Local&timeout=5s&charset=utf8mb4",
		c.Username, c.Password, c.Host, c.Port, c.Database)
}

// ServerVersion 查询数据库服务器版本字符串（如 "8.4.6"、"5.7.43"、"10.11.8-MariaDB"）。
// 用于 test-connection 显示版本号，以及判断兼容性。
func ServerVersion(ctx context.Context, cfg Config) (string, error) {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return "", err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var version string
	err = db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	return version, err
}

// Ping 测试数据库连接，返回错误（nil 表示成功）。
// 内部建临时连接、5s 超时、ping 完即关闭，不留连接池。
func Ping(ctx context.Context, cfg Config) error {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return fmt.Errorf("构建数据库连接失败: %w", err)
	}
	defer db.Close()

	// 限制整体探测时间，避免前端长时间等待。
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("连接数据库失败: %w", err)
	}
	return nil
}

// TableExists 检查指定表是否存在。
// 用 information_schema 而非 SHOW TABLES，便于跨权限场景。
func TableExists(ctx context.Context, cfg Config, table string) (bool, error) {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return false, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var name string
	// 用 database 名做 schema 限定，避免匹配到其他库的同名表。
	err = db.QueryRowContext(ctx,
		`SELECT TABLE_NAME FROM information_schema.TABLES
		 WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`,
		cfg.Database, table,
	).Scan(&name)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// TableExistsBatch 批量检查多个表是否存在，返回存在的表名集合。
// 用于面板类型探测（一次连接检查多个特征表，减少握手开销）。
func TableExistsBatch(ctx context.Context, cfg Config, tables []string) (map[string]bool, error) {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// 一次性查 information_schema，按 IN 条件匹配。
	query := `SELECT TABLE_NAME FROM information_schema.TABLES
	          WHERE TABLE_SCHEMA = ? AND TABLE_NAME IN (` + placeholders(len(tables)) + `)`
	args := make([]any, 0, len(tables)+1)
	args = append(args, cfg.Database)
	for _, t := range tables {
		args = append(args, t)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	found := make(map[string]bool, len(tables))
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		found[name] = true
	}
	return found, rows.Err()
}

// TableColumns 查询指定表的列集合。表不存在时返回空集合。
func TableColumns(ctx context.Context, cfg Config, table string) (map[string]bool, error) {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx,
		`SELECT COLUMN_NAME FROM information_schema.COLUMNS
		 WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?`,
		cfg.Database, table,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns[name] = true
	}
	return columns, rows.Err()
}

// placeholders 生成 n 个问号占位符，用于 IN 查询。
// n<=0 时返回 "NULL"，使 IN (NULL) 恒为 false（合法且不会语法错误）。
func placeholders(n int) string {
	if n <= 0 {
		return "NULL"
	}
	out := make([]byte, 0, n*2)
	for i := 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '?')
	}
	return string(out)
}

// CountRows 统计指定表的行数。
func CountRows(ctx context.Context, cfg Config, table string) (int64, error) {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return 0, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// table 来自硬编码常量，不接受用户输入，可直接拼接（表名不能用占位符）。
	var count int64
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&count)
	return count, err
}

// CountRowsBatch 批量统计多张表的行数，一次连接完成，减少握手开销。
// 返回 map[table]rows。
func CountRowsBatch(ctx context.Context, cfg Config, tables []string) (map[string]int64, error) {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return nil, err
	}
	defer db.Close()

	result := make(map[string]int64, len(tables))
	for _, table := range tables {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var count int64
		// COUNT(*) 是轻量查询（走索引），逐表查即可；information_schema 无行数。
		err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM `"+table+"`").Scan(&count)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("统计表 %s 失败: %w", table, err)
		}
		result[table] = count
	}
	return result, nil
}

// QueryScalar 执行返回单个数值的聚合查询（如 SUM/AVG/MAX）。
// 用于 detect 阶段统计金额总和、流量总和等关键指标。
// 若查询无结果（如 SUM 空表），返回 0 + nil。
func QueryScalar(ctx context.Context, cfg Config, query string, args ...any) (int64, error) {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return 0, err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// 用 sql.NullInt64 容忍 NULL（SUM 空表返回 NULL）。
	var val sql.NullInt64
	err = db.QueryRowContext(ctx, query, args...).Scan(&val)
	if err != nil {
		return 0, err
	}
	if !val.Valid {
		return 0, nil
	}
	return val.Int64, nil
}

// QueryRows 执行返回多行的查询，通过 scanFn 回调处理每一行，确保连接正确释放。
// 用于 dry-run 阶段查询冲突明细（如重复邮箱列表）。
// scanFn 返回 error 时中止扫描（非 nil 会向上传递，io.EOF 视为正常结束）。
func QueryRows(ctx context.Context, cfg Config, query string, scanFn func(rows *sql.Rows) error, args ...any) error {
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		if err := scanFn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
