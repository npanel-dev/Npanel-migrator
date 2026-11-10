// sspanelv3 dry-run 预演：只读检测迁移风险。
// 核心差异点：密码算法是源面板全局配置（pwdMethod+salt）而非每用户字段，
// salt 未提供时带盐哈希用户迁移后无法登录，必须阻断。
package sspanelv3

import (
	"context"
	"database/sql"
	"fmt"

	"npanel-migrator/internal/data/db"
)

// IssueSeverity 问题严重级别。
type IssueSeverity string

const (
	SeverityError   IssueSeverity = "error"
	SeverityWarning IssueSeverity = "warning"
	SeverityInfo    IssueSeverity = "info"
)

// Issue 单个迁移前问题。
type Issue struct {
	Severity IssueSeverity `json:"severity"`
	Category string        `json:"category"`
	Message  string        `json:"message"`
	Count    int64         `json:"count"`
	Sample   []string      `json:"sample"`
}

// DryRunReport dry-run 预演报告。
type DryRunReport struct {
	Panel   string        `json:"panel"`
	Issues  []Issue       `json:"issues"`
	Summary DryRunSummary `json:"summary"`
}

// DryRunSummary 问题汇总。
type DryRunSummary struct {
	ErrorCount   int  `json:"errorCount"`
	WarningCount int  `json:"warningCount"`
	InfoCount    int  `json:"infoCount"`
	CanProceed   bool `json:"canProceed"`
}

// DryRun 执行 dry-run 预演。opts 携带源面板全局密码配置。
func DryRun(ctx context.Context, cfg db.Config, opts Options) (*DryRunReport, error) {
	report := &DryRunReport{Panel: "sspanelv3"}

	report.checkPasswordCompatibility(ctx, cfg, opts)
	report.checkMissingEmails(ctx, cfg)
	report.checkDuplicateEmails(ctx, cfg)
	report.checkBoughtShopMissing(ctx, cfg)
	report.checkShopContentInvalid(ctx, cfg)
	report.checkSkippedNodes(ctx, cfg)
	report.checkNodePortModel(ctx, cfg)
	report.checkPermanentUsers(ctx, cfg)
	report.checkUnmigratedTables(ctx, cfg)

	for _, iss := range report.Issues {
		switch iss.Severity {
		case SeverityError:
			report.Summary.ErrorCount++
		case SeverityWarning:
			report.Summary.WarningCount++
		case SeverityInfo:
			report.Summary.InfoCount++
		}
	}
	report.Summary.CanProceed = report.Summary.ErrorCount == 0
	return report, nil
}

// checkPasswordCompatibility 按哈希长度分布识别算法，校验全局 salt 是否可用。
// 源面板 pwdMethod（md5/sha256）均为 password+salt 形式；
// NPanel 侧 sha256salt 需 v1.1.0+（MultiPasswordVerify 已支持）。
func (r *DryRunReport) checkPasswordCompatibility(ctx context.Context, cfg db.Config, opts Options) {
	var lengths []struct {
		length int64
		count  int64
	}
	err := db.QueryRows(ctx, cfg,
		"SELECT CHAR_LENGTH(pass) AS l, COUNT(*) AS c FROM user GROUP BY l ORDER BY c DESC",
		func(rows *sql.Rows) error {
			var item struct {
				length int64
				count  int64
			}
			if err := rows.Scan(&item.length, &item.count); err != nil {
				return err
			}
			lengths = append(lengths, item)
			return nil
		},
	)
	if err != nil {
		r.add(SeverityError, "password_check_failed",
			"查询密码哈希分布失败: "+err.Error(), 0, nil)
		return
	}

	var saltUsers, invalidUsers, bcryptUsers int64
	for _, item := range lengths {
		switch {
		case item.length == 64:
			saltUsers += item.count // sha256(password+salt)
		case item.length == 32:
			saltUsers += item.count // md5(password+salt)
		case item.length == 60:
			bcryptUsers += item.count
		default:
			invalidUsers += item.count
		}
	}

	if invalidUsers > 0 {
		r.add(SeverityError, "invalid_password_hash",
			fmt.Sprintf("存在 %d 个无法识别算法的密码哈希（非 32/64 位 hex、非 bcrypt），迁移后这些用户需重置密码", invalidUsers),
			invalidUsers, nil)
	}
	if saltUsers > 0 && opts.Salt == "" {
		r.add(SeverityError, "missing_password_salt",
			fmt.Sprintf("存在 %d 个带盐哈希用户，但未提供源面板全局 salt（config.php 的 $_ENV['salt']）。"+
				"缺少 salt 时用户迁移后无法用原密码登录，请在连接配置中填写 salt 后重试", saltUsers),
			saltUsers, nil)
		return
	}
	if saltUsers > 0 {
		r.add(SeverityInfo, "password_salt",
			fmt.Sprintf("已提供全局 salt，%d 个用户将按带盐算法（sha256salt/md5salt）迁移，原密码可继续使用（需 NPanel v1.1.0+）", saltUsers),
			saltUsers, nil)
	}
	if bcryptUsers > 0 {
		r.add(SeverityInfo, "password_bcrypt",
			fmt.Sprintf("%d 个用户使用 bcrypt 哈希，可直接迁移", bcryptUsers),
			bcryptUsers, nil)
	}
	if opts.PwdMethod != "" && opts.PwdMethod != "md5" && opts.PwdMethod != "sha256" {
		r.add(SeverityWarning, "unsupported_pwd_method",
			fmt.Sprintf("源面板 pwdMethod=%q 不在 NPanel 支持范围（argon2i/argon2id/sha3），全部用户将进入重置名单", opts.PwdMethod),
			0, nil)
	}
}

// checkMissingEmails 空邮箱用户迁移后无法邮箱登录。
func (r *DryRunReport) checkMissingEmails(ctx context.Context, cfg db.Config) {
	count, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM user WHERE email IS NULL OR TRIM(email) = ''")
	if err != nil {
		r.add(SeverityError, "email_check_failed", "查询空邮箱用户失败: "+err.Error(), 0, nil)
		return
	}
	if count > 0 {
		r.add(SeverityError, "missing_email",
			fmt.Sprintf("存在 %d 个空邮箱用户，迁移后无法通过邮箱登录，必须先补齐邮箱", count),
			count, nil)
	}
}

// checkDuplicateEmails 重复邮箱会撞 user_auth_methods 唯一约束。
func (r *DryRunReport) checkDuplicateEmails(ctx context.Context, cfg db.Config) {
	var sample []string
	err := db.QueryRows(ctx, cfg,
		"SELECT LOWER(TRIM(email)) AS e, COUNT(*) AS c FROM user "+
			"WHERE email IS NOT NULL AND TRIM(email) != '' "+
			"GROUP BY LOWER(TRIM(email)) HAVING COUNT(*) > 1 ORDER BY c DESC LIMIT 20",
		func(rows *sql.Rows) error {
			var email string
			var c int64
			if err := rows.Scan(&email, &c); err != nil {
				return err
			}
			sample = append(sample, fmt.Sprintf("%s ×%d", email, c))
			return nil
		},
	)
	if err != nil {
		r.add(SeverityWarning, "email_check_failed", "查询重复邮箱失败: "+err.Error(), 0, nil)
		return
	}
	if len(sample) > 0 {
		r.add(SeverityError, "duplicate_email",
			fmt.Sprintf("存在 %d 组重复邮箱，迁移时会撞目标库唯一约束，需先在源库处理", len(sample)),
			int64(len(sample)), sample)
	}
}

// checkBoughtShopMissing 购买记录引用已删除商品（订单的套餐映射会缺失）。
func (r *DryRunReport) checkBoughtShopMissing(ctx context.Context, cfg db.Config) {
	count, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM bought b LEFT JOIN shop s ON s.id = b.shopid WHERE s.id IS NULL")
	if err != nil {
		r.add(SeverityWarning, "bought_check_failed", "查询失效购买记录失败: "+err.Error(), 0, nil)
		return
	}
	if count > 0 {
		r.add(SeverityWarning, "orphan_bought",
			fmt.Sprintf("存在 %d 条购买记录引用已删除的商品，对应订单将不关联套餐", count),
			count, nil)
	}
	// 源面板删除用户时保留 bought 历史，这类订单无法迁移（NPanel order 需挂用户）。
	orphanUsers, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM bought b LEFT JOIN user u ON u.id = b.userid WHERE u.id IS NULL")
	if err == nil && orphanUsers > 0 {
		r.add(SeverityWarning, "orphan_bought_users",
			fmt.Sprintf("存在 %d 条购买记录的用户已被删除（源面板保留历史），这些订单将跳过", orphanUsers),
			orphanUsers, nil)
	}
}

// checkShopContentInvalid 商品 content JSON 无法解析（流量/时长信息缺失）。
func (r *DryRunReport) checkShopContentInvalid(ctx context.Context, cfg db.Config) {
	var sample []string
	err := db.QueryRows(ctx, cfg,
		"SELECT id, content FROM shop WHERE TRIM(content) != '' AND JSON_VALID(content) = 0 LIMIT 20",
		func(rows *sql.Rows) error {
			var id int64
			var content string
			if err := rows.Scan(&id, &content); err != nil {
				return err
			}
			sample = append(sample, fmt.Sprintf("shop #%d", id))
			return nil
		},
	)
	if err != nil {
		r.add(SeverityWarning, "shop_check_failed", "查询商品 content 失败: "+err.Error(), 0, nil)
		return
	}
	if len(sample) > 0 {
		r.add(SeverityWarning, "invalid_shop_content",
			fmt.Sprintf("存在 %d 个商品 content 非合法 JSON，流量/时长将按 0 迁移", len(sample)),
			int64(len(sample)), sample)
	}
}

// checkSkippedNodes 统计不迁移的节点类型（999 Radius 占位、9 单端口）。
func (r *DryRunReport) checkSkippedNodes(ctx context.Context, cfg db.Config) {
	var radius, muPort int64
	radius = mustCount(ctx, cfg, "ss_node", "sort = 999")
	muPort = mustCount(ctx, cfg, "ss_node", "sort = 9")
	if radius > 0 || muPort > 0 {
		r.add(SeverityInfo, "skipped_nodes",
			fmt.Sprintf("跳过 %d 个系统占位节点（sort=999）与 %d 个单端口节点（sort=9，NPanel 无等价物）", radius, muPort),
			radius+muPort, nil)
	}
}

// checkNodePortModel 源面板是每用户独享端口模型，节点端口需迁移后配置。
func (r *DryRunReport) checkNodePortModel(ctx context.Context, cfg db.Config) {
	r.add(SeverityWarning, "node_port_model",
		"源面板节点是「每用户独享端口」模型（ss_node 无 port 列），NPanel 节点为固定端口模型。"+
			"节点将以 port=0 迁移（名称/地址/加密/等级保留），迁移后需在 NPanel 中为节点配置服务端口",
		0, nil)
}

// checkPermanentUsers 远期哨兵（永久）用户提示。
func (r *DryRunReport) checkPermanentUsers(ctx context.Context, cfg db.Config) {
	count, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM user WHERE class_expire >= '2099-01-01' OR expire_in >= '2099-01-01'")
	if err != nil {
		return
	}
	if count > 0 {
		r.add(SeverityInfo, "permanent_users",
			fmt.Sprintf("存在 %d 个永久用户（class_expire/expire_in ≥ 2099），迁移后订阅到期时间将保留远期日期", count),
			count, nil)
	}
}

// checkUnmigratedTables 明示不迁移的表。
func (r *DryRunReport) checkUnmigratedTables(ctx context.Context, cfg db.Config) {
	payback, _ := db.CountRows(ctx, cfg, "payback")
	paylist, _ := db.CountRows(ctx, cfg, "paylist")
	link, _ := db.CountRows(ctx, cfg, "link")
	r.add(SeverityInfo, "skipped_tables",
		fmt.Sprintf("以下表不迁移：payback 返利记录 %d 行、paylist 支付流水 %d 行、link 分享链接 %d 行（历史日志，NPanel 无对应业务）",
			payback, paylist, link),
		payback+paylist+link, nil)
}

// add 追加一条问题。
func (r *DryRunReport) add(severity IssueSeverity, category, message string, count int64, sample []string) {
	r.Issues = append(r.Issues, Issue{
		Severity: severity, Category: category, Message: message, Count: count, Sample: sample,
	})
}
