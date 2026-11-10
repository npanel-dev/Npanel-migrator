// Package sspanelv3 适配早期 ss-panel v3（mod 系）面板。
//
// 源端特征（与 SSPanel-UIM 的 product/order 模型不同）：
//   - user 表：class/class_expire（套餐等级与到期）+ expire_in（账号有效期）+ u/d/transfer_enable
//   - shop 表：商品，content 是 JSON（bandwidth GB/expire 天/class/class_expire 天等），一商品一价
//   - bought 表：购买历史（userid/shopid/datetime/renew/price）
//   - code 表：充值码
//   - ss_node 表：节点，无 port 列（每用户独享端口模型），sort 区分协议
//
// 密码算法是全局配置（config.php 的 pwdMethod + salt），不在数据库里：
// md5 → md5(password+salt)，sha256 → sha256(password+salt)，均映射为 NPanel 的带盐算法。
// 哈希长度可区分：32 位 hex → md5salt，64 位 hex → sha256salt。
// salt 值必须由迁移执行者从源面板 config.php 中获取并传入 Options.Salt。
package sspanelv3

import (
	"strings"
)

// Options 是 ss-panel v3 特有的迁移选项（来自源面板 config.php）。
type Options struct {
	// PwdMethod 源面板密码算法（"md5" / "sha256"，空则按哈希长度嗅探）。
	PwdMethod string
	// Salt 全局密码盐（config.php 的 $_ENV['salt']）。
	// 哈希为带盐算法时必填，否则用户迁移后无法用原密码登录。
	Salt string
}

// maxPasswordSaltLength NPanel user.salt 字段上限（v1.1.1 起为 128，旧版 20）。
const maxPasswordSaltLength = 128

// ss-panel v3 的 datetime 哨兵值（"1989-06-04 00:05:00" 表示空/无效/已过期，
// 远期年份（2099/3001 等）表示永久有效）。来源：面板注册默认值与 Cron 激活逻辑。
const (
	// sentinelExpiredYear 哨兵年份：早于该年视为"无效/已过期"。
	sentinelExpiredYear = 2000
	// permanentYear 长期年份：达到该年视为永久有效（2099/3001 哨兵）。
	permanentYear = 2099
)

// ss_node.sort 协议值（源码 app/Utils/URL.php 核实）。
const (
	sortSSR      = 0   // SS/SSR 节点
	sortMuPort   = 9   // 单端口（NPanel 无等价物，跳过）
	sortSSOnly   = 10  // 仅 SS
	sortVmess    = 11  // V2Ray
	sortVmessTLS = 12  // V2Ray + TLS
	sortSS2022   = 13  // SS（另一种客户端识别）
	sortTrojan   = 14  // Trojan
	sortRadius   = 999 // Radius/系统占位节点，跳过
)

// nodeGroupIDFromClass 由 class 等级合成节点分组 ID。
// class 值本身作为分组 SourceID（0 为基础组）。
func nodeGroupIDFromClass(class int64) int64 {
	return class
}

// nodeGroupIDFromGroup 由 user.node_group 合成节点分组 ID。
// 加偏移避免与 class 值（0-99 区间）冲突。
func nodeGroupIDFromGroup(group int64) int64 {
	return 1000 + group
}

// isNodeGroupIDGroup 判断合成分组 ID 是否来自 node_group（而非 class）。
func isNodeGroupIDGroup(id int64) bool {
	return id >= 1000
}

// sniffPassAlgo 按哈希长度/前缀识别密码算法。
// 返回 NPanel algo 值；空哈希或无法识别返回 "default"（迁移后需重置密码）。
func sniffPassAlgo(hash string) string {
	hash = strings.TrimSpace(hash)
	switch {
	case isLowerHex(hash, 64):
		return "sha256salt"
	case isLowerHex(hash, 32):
		return "md5salt"
	case strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") ||
		strings.HasPrefix(hash, "$2x$") || strings.HasPrefix(hash, "$2y$"):
		return "bcrypt"
	default:
		return "default"
	}
}

// algoNeedsSalt 判断算法是否依赖全局 salt。
func algoNeedsSalt(algo string) bool {
	return algo == "sha256salt" || algo == "md5salt"
}

// resolvePwdMethod 结合显式配置与哈希嗅探确定算法。
// pwdMethod 非空时以其为准（md5/sha256），否则按哈希长度嗅探。
func resolvePwdMethod(pwdMethod, hash string) string {
	switch strings.ToLower(strings.TrimSpace(pwdMethod)) {
	case "md5":
		return "md5salt"
	case "sha256":
		return "sha256salt"
	case "":
		return sniffPassAlgo(hash)
	default:
		// argon2i/argon2id/sha3 等源面板支持但 NPanel 不支持 → default（重置名单）。
		return "default"
	}
}

// isLowerHex 判断 s 是否为 length 位的十六进制字符串。
func isLowerHex(s string, length int) bool {
	if len(s) != length {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// isValidHashForAlgo 校验哈希与算法是否匹配（用于 dry-run）。
func isValidHashForAlgo(algo, hash string) bool {
	hash = strings.TrimSpace(hash)
	switch algo {
	case "sha256salt":
		return isLowerHex(hash, 64)
	case "md5salt":
		return isLowerHex(hash, 32)
	case "bcrypt":
		return len(hash) == 60 && (strings.HasPrefix(hash, "$2a$") ||
			strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2x$") ||
			strings.HasPrefix(hash, "$2y$"))
	default:
		return len(hash) > 0
	}
}
