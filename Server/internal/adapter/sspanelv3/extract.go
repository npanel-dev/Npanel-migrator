package sspanelv3

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// ExtractUsers 读取全量用户（一次性，供测试/小库使用）。
func ExtractUsers(ctx context.Context, cfg db.Config, opts Options, batchSize int, onBatch func([]*canonical.User) error) error {
	pool, err := db.OpenPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	return ExtractUsersKeyset(ctx, pool, opts, batchSize, 0, math.MaxInt64, onBatch)
}

// ExtractUsersKeyset 按 id 游标分批读取 user 表（与 xiaov2board 同契约，支持断点续传）。
func ExtractUsersKeyset(
	ctx context.Context,
	pool *sql.DB,
	opts Options,
	batchSize int,
	afterID, highWater int64,
	onBatch func([]*canonical.User) error,
) error {
	if batchSize <= 0 {
		batchSize = 500
	}
	lastID := afterID
	for {
		batch := make([]*canonical.User, 0, batchSize)
		err := db.QueryRowsDBWithTimeout(ctx, pool, 5*time.Minute,
			`SELECT id, email, pass, money, enable, is_admin, is_email_verify,
			        telegram_id, ref_by, reg_date
			   FROM user
			  WHERE id > ? AND id <= ?
			  ORDER BY id
			  LIMIT ?`,
			func(rows *sql.Rows) error {
				u, err := scanUser(rows, opts)
				if err != nil {
					return err
				}
				batch = append(batch, u)
				return nil
			},
			lastID, highWater, batchSize,
		)
		if err != nil {
			return fmt.Errorf("读取 user 失败 (after id %d): %w", lastID, err)
		}
		if len(batch) == 0 {
			return nil
		}
		if err := onBatch(batch); err != nil {
			return err
		}
		if len(batch) < batchSize {
			return nil // 已读完
		}
		lastID = batch[len(batch)-1].SourceID
	}
}

// scanUser 扫描单行 user → canonical.User。
func scanUser(rows *sql.Rows, opts Options) (*canonical.User, error) {
	var (
		id          int64
		email       sql.NullString
		pass        sql.NullString
		money       sql.NullFloat64
		enable      sql.NullInt64
		isAdmin     sql.NullInt64
		emailVerify sql.NullInt64
		telegramID  sql.NullInt64
		refBy       sql.NullInt64
		regDate     sql.NullString
	)
	if err := rows.Scan(&id, &email, &pass, &money, &enable, &isAdmin,
		&emailVerify, &telegramID, &refBy, &regDate); err != nil {
		return nil, err
	}

	algo := resolvePwdMethod(opts.PwdMethod, pass.String)
	// reg_date 经 driver parseTime 后是 RFC3339 形式，parseDateTime 已兼容两种格式。
	createdAt := orEpoch(parseDateTime(regDate.String))

	return &canonical.User{
		SourceID: id,
		Email:    email.String,
		// 密码：哈希原样迁移，算法按全局 pwdMethod/哈希长度识别，salt 写全局盐。
		PasswordHash:    pass.String,
		PasswordAlgo:    algo,
		PasswordSalt:    saltForAlgo(opts.Salt, algo),
		BalanceCents:    yuanToCents(money.Float64),
		TelegramID:      telegramID.Int64,
		RefererSourceID: refBy.Int64,
		// ss_invite_code 是 4 字符短码（必冲突），不迁移，由 writer 重新生成邀请码。
		Enabled:       enable.Int64 == 1,
		IsAdmin:       isAdmin.Int64 == 1,
		EmailVerified: emailVerify.Int64 == 1,
		CreatedAt:     createdAt,
		// 源表无更新时间列，用注册时间充当，避免 writer 写入零值时间。
		UpdatedAt: createdAt,
	}, nil
}

// saltForAlgo 仅带盐算法写入 salt，其他算法置空。
func saltForAlgo(salt, algo string) string {
	if algoNeedsSalt(algo) {
		return salt
	}
	return ""
}

// yuanToCents 元（decimal）→ 分，四舍五入避免浮点误差。
func yuanToCents(yuan float64) int64 {
	return int64(math.Round(yuan * 100))
}
