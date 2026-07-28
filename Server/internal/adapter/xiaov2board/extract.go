package xiaov2board

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"npanel-migrator/internal/data/canonical"
	"npanel-migrator/internal/data/db"
)

// ExtractUsers 从 v2_user 读取并转换为 canonical.User。
// 字段映射规则见方案 6.1：密码 algo 原样保留、balance 分、banned→enable 反义。
// 按 batchSize 分页读取，避免大表一次性载入内存。
func ExtractUsers(ctx context.Context, cfg db.Config, batchSize int, onBatch func([]*canonical.User) error) error {
	pool, err := db.OpenPool(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()
	highWater, err := db.QueryScalarDBWithTimeout(ctx, pool, 10*time.Second, "SELECT COALESCE(MAX(id), 0) FROM v2_user")
	if err != nil {
		return err
	}
	return ExtractUsersKeyset(ctx, pool, batchSize, 0, highWater, onBatch)
}

// ExtractUsersKeyset 使用主键游标分页，支持从 checkpoint 继续且避免 OFFSET 扫描。
func ExtractUsersKeyset(
	ctx context.Context,
	pool *sql.DB,
	batchSize int,
	afterID, highWater int64,
	onBatch func([]*canonical.User) error,
) error {
	if batchSize <= 0 {
		batchSize = 500
	}
	lastID := afterID
	for {
		var batch []*canonical.User
		err := db.QueryRowsDBWithTimeout(ctx, pool, 30*time.Second,
			"SELECT id, email, password, password_algo, password_salt, "+
				"balance, commission_balance, telegram_id, token, invite_user_id, "+
				"banned, is_admin, expired_at, created_at, updated_at "+
				"FROM v2_user WHERE id > ? AND id <= ? ORDER BY id LIMIT ?",
			func(rows *sql.Rows) error {
				u, err := scanUser(rows)
				if err != nil {
					return err
				}
				batch = append(batch, u)
				return nil
			},
			lastID, highWater, batchSize,
		)
		if err != nil {
			return fmt.Errorf("读取 v2_user 失败 (after id %d): %w", lastID, err)
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

// scanUser 扫描单行 v2_user → canonical.User。
func scanUser(rows *sql.Rows) (*canonical.User, error) {
	var (
		id                int64
		email             sql.NullString
		password          sql.NullString
		passwordAlgo      sql.NullString
		passwordSalt      sql.NullString
		balance           sql.NullInt64
		commissionBalance sql.NullInt64
		telegramID        sql.NullInt64
		token             sql.NullString
		inviteUserID      sql.NullInt64
		banned            sql.NullInt64
		isAdmin           sql.NullInt64
		expiredAt         sql.NullInt64
		createdAt         sql.NullInt64
		updatedAt         sql.NullInt64
	)
	if err := rows.Scan(&id, &email, &password, &passwordAlgo, &passwordSalt,
		&balance, &commissionBalance, &telegramID, &token, &inviteUserID,
		&banned, &isAdmin, &expiredAt, &createdAt, &updatedAt); err != nil {
		return nil, err
	}

	return &canonical.User{
		SourceID:     id,
		Email:        email.String,
		PasswordHash: password.String,
		// 密码 algo：原样保留；NULL 时按 hash 前缀识别 PHP password_hash。
		PasswordAlgo:    normalizeAlgo(passwordAlgo.String, password.String),
		PasswordSalt:    passwordSalt.String,
		BalanceCents:    balance.Int64,
		CommissionCents: commissionBalance.Int64,
		TelegramID:      telegramID.Int64,
		ReferCode:       token.String,
		RefererSourceID: inviteUserID.Int64,
		// banned 反义：banned=1 → enable=false。
		Enabled:   bannedToEnable(banned.Int64),
		IsAdmin:   isAdmin.Int64 == 1,
		CreatedAt: unixToTime(createdAt.Int64),
		UpdatedAt: unixToTime(updatedAt.Int64),
	}, nil
}

// normalizeAlgo 把 v2board 的 password_algo 归一化为 NPanel algo 值。
// 方案 6.1.1：password_algo=NULL 时按 hash 前缀区分，$2* 是 PHP bcrypt。
func normalizeAlgo(algo, hash string) string {
	algo = strings.ToLower(strings.TrimSpace(algo))
	hash = strings.TrimSpace(hash)
	switch algo {
	case "md5", "sha256", "md5salt", "sha256salt", "bcrypt":
		return algo
	case "", "default":
		// xiaov2board 的 default 语义是 PHP password_verify，不等同于
		// NPanel 的 PBKDF2 default。PHP bcrypt 必须显式映射为 bcrypt。
		if isBcryptHash(hash) {
			return "bcrypt"
		}
		return "default"
	default:
		return algo
	}
}

func isBcryptHash(hash string) bool {
	if len(hash) != 60 ||
		!(strings.HasPrefix(hash, "$2a$") ||
			strings.HasPrefix(hash, "$2b$") ||
			strings.HasPrefix(hash, "$2x$") ||
			strings.HasPrefix(hash, "$2y$")) {
		return false
	}
	if hash[4] < '0' || hash[4] > '9' ||
		hash[5] < '0' || hash[5] > '9' ||
		hash[6] != '$' {
		return false
	}
	cost := int(hash[4]-'0')*10 + int(hash[5]-'0')
	if cost < 4 || cost > 31 {
		return false
	}
	for i := 7; i < len(hash); i++ {
		c := hash[i]
		if !((c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') ||
			c == '.' || c == '/') {
			return false
		}
	}
	return true
}

// bannedToEnable v2board banned(0/1) → NPanel enable(bool)。
// banned=1 → enable=false；其他 → true。
func bannedToEnable(banned int64) bool {
	return banned != 1
}

// unixToTime Unix 秒 → time.Time（0 → 零值）。
func unixToTime(unix int64) time.Time {
	if unix <= 0 {
		return time.Time{}
	}
	return time.Unix(unix, 0)
}
