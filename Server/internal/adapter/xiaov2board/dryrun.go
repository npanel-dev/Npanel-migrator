package xiaov2board

import (
	"context"
	"database/sql"
	"fmt"

	"npanel-migrator/internal/data/db"
)

// IssueSeverity 问题严重级别。
type IssueSeverity string

const (
	SeverityError   IssueSeverity = "error"   // 阻断迁移，必须处理
	SeverityWarning IssueSeverity = "warning" // 警告，建议处理但不阻断
	SeverityInfo    IssueSeverity = "info"    // 提示信息
)

// Issue 单个迁移前问题。
type Issue struct {
	// Severity 严重级别。
	Severity IssueSeverity `json:"severity"`
	// Category 问题类别（duplicate_email / negative_balance / ...）。
	Category string `json:"category"`
	// Message 问题描述。
	Message string `json:"message"`
	// Count 受影响记录数（如 5 个重复邮箱）。
	Count int64 `json:"count"`
	// Sample 样本数据（前几个问题记录，便于人工核查）。
	Sample []string `json:"sample"`
}

// DryRunReport dry-run 预演报告。
type DryRunReport struct {
	// Panel 面板类型。
	Panel string `json:"panel"`
	// Issues 发现的问题列表。
	Issues []Issue `json:"issues"`
	// Summary 汇总统计。
	Summary DryRunSummary `json:"summary"`
}

// DryRunSummary 问题汇总。
type DryRunSummary struct {
	ErrorCount   int `json:"errorCount"`
	WarningCount int `json:"warningCount"`
	InfoCount    int `json:"infoCount"`
	// CanProceed 是否可以继续迁移（无 error 级问题）。
	CanProceed bool `json:"canProceed"`
}

// DryRun 执行 dry-run 预演，只读不写，检测潜在冲突。
// 对应迁移方案第 9 章"导入前报告"的冲突检测部分。
func DryRun(ctx context.Context, cfg db.Config) (*DryRunReport, error) {
	report := &DryRunReport{Panel: "xiaov2board"}

	// 逐项检测，每项独立容错（单项失败不影响其他项）。
	report.checkDuplicateEmails(ctx, cfg)
	report.checkNegativeBalance(ctx, cfg)
	report.checkZeroTransferUsers(ctx, cfg)
	report.checkPermanentSubscriptions(ctx, cfg)
	report.checkAbnormalOrders(ctx, cfg)
	report.checkMissingPlans(ctx, cfg)
	report.checkOrphanOrders(ctx, cfg)
	report.checkUnsupportedPasswordHashes(ctx, cfg)

	// 汇总。
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

// checkDuplicateEmails 检测重复邮箱（迁移后会撞 user_auth_methods.auth_identifier 唯一约束）。
func (r *DryRunReport) checkDuplicateEmails(ctx context.Context, cfg db.Config) {
	// 查找出现次数 > 1 的邮箱，通过回调逐行扫描。
	var totalCount int64
	var sample []string

	err := db.QueryRows(ctx, cfg,
		"SELECT email, COUNT(*) AS c FROM v2_user WHERE email IS NOT NULL AND email != '' "+
			"GROUP BY email HAVING c > 1 ORDER BY c DESC LIMIT 100",
		func(rows *sql.Rows) error {
			var email string
			var c int64
			if err := rows.Scan(&email, &c); err != nil {
				return err
			}
			totalCount += c
			if len(sample) < 5 {
				sample = append(sample, fmt.Sprintf("%s (×%d)", email, c))
			}
			return nil
		},
	)
	if err != nil {
		r.add(SeverityWarning, "query_error", "查询重复邮箱失败: "+err.Error(), 0, nil)
		return
	}
	if totalCount > 0 {
		r.add(SeverityError, "duplicate_email",
			"存在重复邮箱，迁移后会触发 user_auth_methods 唯一约束冲突，必须先在源库去重或合并",
			totalCount, sample)
	} else {
		r.add(SeverityInfo, "duplicate_email", "无重复邮箱", 0, nil)
	}
}

// checkNegativeBalance 检测负余额用户（可能是数据异常或透支）。
func (r *DryRunReport) checkNegativeBalance(ctx context.Context, cfg db.Config) {
	count, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM v2_user WHERE balance < 0")
	if err != nil {
		return
	}
	if count > 0 {
		r.add(SeverityWarning, "negative_balance",
			fmt.Sprintf("存在 %d 个负余额用户，请确认是否为透支或数据异常", count),
			count, nil)
	}
}

// checkZeroTransferUsers 检测流量上限为 0 但有套餐的用户（可能是配置异常）。
func (r *DryRunReport) checkZeroTransferUsers(ctx context.Context, cfg db.Config) {
	count, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM v2_user WHERE plan_id > 0 AND transfer_enable = 0")
	if err != nil {
		return
	}
	if count > 0 {
		r.add(SeverityWarning, "zero_transfer",
			fmt.Sprintf("存在 %d 个有套餐但流量上限为 0 的用户，迁移后无法使用", count),
			count, nil)
	}
}

// checkPermanentSubscriptions 检测永久订阅（expired_at=0）的数量，提示单位换算注意。
func (r *DryRunReport) checkPermanentSubscriptions(ctx context.Context, cfg db.Config) {
	count, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM v2_user WHERE plan_id > 0 AND expired_at = 0")
	if err != nil {
		return
	}
	if count > 0 {
		r.add(SeverityInfo, "permanent_subscription",
			fmt.Sprintf("存在 %d 个永久订阅用户（expired_at=0），迁移时 expire_time 须设为 Unix 0", count),
			count, nil)
	}
}

// checkAbnormalOrders 检测异常订单（金额为负或超大）。
func (r *DryRunReport) checkAbnormalOrders(ctx context.Context, cfg db.Config) {
	// 负金额订单。
	negCount, _ := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM v2_order WHERE total_amount < 0")
	if negCount > 0 {
		r.add(SeverityWarning, "negative_order",
			fmt.Sprintf("存在 %d 个负金额订单，请确认数据正确性", negCount),
			negCount, nil)
	}
	// 超大金额订单（> 100万元 = 1亿分），可能是单位错误。
	hugeCount, _ := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM v2_order WHERE total_amount > 100000000")
	if hugeCount > 0 {
		r.add(SeverityWarning, "huge_order",
			fmt.Sprintf("存在 %d 个金额超过 1 万元的订单（>1亿分），请确认金额单位是否为分", hugeCount),
			hugeCount, nil)
	}
}

// checkMissingPlans 检测引用了不存在套餐的用户（孤儿订阅）。
func (r *DryRunReport) checkMissingPlans(ctx context.Context, cfg db.Config) {
	count, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM v2_user u WHERE u.plan_id > 0 AND NOT EXISTS "+
			"(SELECT 1 FROM v2_plan p WHERE p.id = u.plan_id)")
	if err != nil {
		return
	}
	if count > 0 {
		r.add(SeverityWarning, "missing_plan",
			fmt.Sprintf("存在 %d 个用户引用了不存在的套餐 plan_id，迁移后订阅会指向空套餐", count),
			count, nil)
	}
}

// checkOrphanOrders 检测引用了不存在用户的订单（孤儿订单）。
func (r *DryRunReport) checkOrphanOrders(ctx context.Context, cfg db.Config) {
	count, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM v2_order o WHERE NOT EXISTS "+
			"(SELECT 1 FROM v2_user u WHERE u.id = o.user_id)")
	if err != nil {
		return
	}
	if count > 0 {
		r.add(SeverityWarning, "orphan_order",
			fmt.Sprintf("存在 %d 个引用了不存在用户的订单，迁移时会因外键失败", count),
			count, nil)
	}
}

// checkUnsupportedPasswordHashes 检测无法保留原密码登录的哈希。
func (r *DryRunReport) checkUnsupportedPasswordHashes(ctx context.Context, cfg db.Config) {
	argon2Count, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM v2_user WHERE (password_algo IS NULL OR password_algo = '') AND password LIKE '$argon2%'")
	if err == nil && argon2Count > 0 {
		r.add(SeverityError, "unsupported_password_hash",
			fmt.Sprintf("存在 %d 个 Argon2 密码哈希用户，NPanel 当前不支持原密码校验，必须先走重置密码方案", argon2Count),
			argon2Count, nil)
	}

	unknownAlgoCount, err := db.QueryScalar(ctx, cfg,
		"SELECT COUNT(*) FROM v2_user WHERE password_algo IS NOT NULL AND password_algo != '' "+
			"AND password_algo NOT IN ('md5','sha256','md5salt','sha256salt','bcrypt','default')")
	if err == nil && unknownAlgoCount > 0 {
		r.add(SeverityError, "unsupported_password_algo",
			fmt.Sprintf("存在 %d 个未知 password_algo 用户，无法保证迁移后原密码登录", unknownAlgoCount),
			unknownAlgoCount, nil)
	}
}

// add 添加一个问题。
func (r *DryRunReport) add(severity IssueSeverity, category, message string, count int64, sample []string) {
	r.Issues = append(r.Issues, Issue{
		Severity: severity,
		Category: category,
		Message:  message,
		Count:    count,
		Sample:   sample,
	})
}
