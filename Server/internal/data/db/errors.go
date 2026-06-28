package db

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// 常见 MySQL 错误码。
// https://dev.mysql.com/doc/mysql-errors/8.0/en/server-error-reference.html
const (
	errAccessDenied       = 1045 // 用户名/密码错误
	errBadDB              = 1049 // 数据库不存在
	errDBCreateExists     = 1007 // 数据库已存在
	errUnknownHost        = 2002 // 主机不可达 / 连接拒绝（CR_CONNECTION_ERROR）
	errConnRefused        = 2003 // 连接被拒绝（CR_CONN_HOST_ERROR）
	errHandshake          = 1043 // 握手失败
	errDBReadAccessDenied = 1227 // 权限不足
	errTooManyConnections = 1040 // 连接数过多
	errNetPacket          = 1153 // 包过大
	errMalformedPacket    = 2027 // 数据包异常（常见于认证插件不兼容）
	errAuthPlugin         = 1524 // 认证插件未加载（如 mysql_native_password）
	errWrongValue         = 1525 // 错误的字段值
)

// FriendlyError 把底层 MySQL 错误翻译成人类可读的双语提示。
//
// zh/en 为中英文友好描述。返回值形如：
//
//	"数据库不存在或名称填写错误（Error 1049: Unknown database 'xxx'）"
//
// 括号内保留原始错误信息便于排查。
func FriendlyError(err error, lang string) (zh, en string) {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		zh, en = translateMySQLErr(mysqlErr.Number, mysqlErr.Message)
		return wrapTranslate(zh, en, err)
	}

	// 非 MySQL 错误码的网络错误（dial timeout / connection refused）。
	msg := err.Error()
	zh, en = translateNetErr(msg)
	return wrapTranslate(zh, en, err)
}

// wrapTranslate 在友好提示后追加原始错误（括号内）。
func wrapTranslate(zh, en string, err error) (string, string) {
	return fmt.Sprintf("%s（%s）", zh, err.Error()), fmt.Sprintf("%s (%s)", en, err.Error())
}

// translateMySQLErr 按错误码翻译。
func translateMySQLErr(code uint16, msg string) (zh, en string) {
	switch code {
	case errAccessDenied:
		return "用户名或密码错误，请检查账号密码", "Incorrect username or password"
	case errBadDB:
		// 提取出数据库名，让提示更具体。
		dbName := extractQuoted(msg)
		if dbName != "" {
			return fmt.Sprintf("数据库 %q 不存在或名称填写错误", dbName),
				fmt.Sprintf("Database %q does not exist or name is incorrect", dbName)
		}
		return "数据库不存在或名称填写错误", "Database does not exist or name is incorrect"
	case errUnknownHost, errConnRefused:
		return "无法连接到数据库服务器，请检查地址和端口是否正确、数据库服务是否运行",
			"Cannot connect to database server, please check host/port and whether the service is running"
	case errHandshake:
		return "数据库连接握手失败，可能是网络不稳定或服务器配置异常",
			"Database connection handshake failed, possibly due to unstable network or server configuration"
	case errDBReadAccessDenied:
		return "数据库账号权限不足，请授予该账号对应库的访问权限",
			"Insufficient database privileges, please grant the account access to the database"
	case errTooManyConnections:
		return "数据库连接数已达上限，请稍后重试或联系管理员",
			"Database has reached max connections, please retry later or contact admin"
	case errAuthPlugin:
		// 如 "Plugin 'mysql_native_password' is not loaded"
		return "数据库认证插件不兼容，可能是 MySQL 8.4+ 默认禁用了旧认证插件，请检查账号认证方式",
			"Database auth plugin incompatible, MySQL 8.4+ may have disabled legacy auth plugin, please check account auth method"
	case errMalformedPacket:
		return "数据库通信数据包异常，可能是认证插件或协议版本不兼容",
			"Database communication packet error, possibly incompatible auth plugin or protocol version"
	default:
		return "数据库操作失败", "Database operation failed"
	}
}

// translateNetErr 翻译网络层错误（非 MySQL 错误码）。
func translateNetErr(msg string) (zh, en string) {
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "connection refused"):
		return "连接被拒绝，数据库服务可能未运行或端口错误",
			"Connection refused, database service may not be running or wrong port"
	case strings.Contains(low, "no such host"), strings.Contains(low, "lookup"):
		return "无法解析数据库地址，请检查主机名是否正确",
			"Cannot resolve database host, please check the hostname"
	case strings.Contains(low, "i/o timeout"), strings.Contains(low, "deadline exceeded"):
		return "连接数据库超时，请检查网络或防火墙设置",
			"Connection to database timed out, please check network or firewall"
	case strings.Contains(low, "tls"):
		return "数据库 TLS/SSL 握手失败，可能是证书或加密配置问题",
			"Database TLS/SSL handshake failed, possibly certificate or encryption issue"
	default:
		return "连接数据库失败", "Failed to connect to database"
	}
}

// extractQuoted 从错误信息里提取反引号或单引号包裹的内容。
// 如 "Unknown database 'xiaov2'" → "xiaov2"
var quotedRe = regexp.MustCompile("`[^`]+`|'[^']+'")

func extractQuoted(msg string) string {
	m := quotedRe.FindString(msg)
	if m == "" {
		return ""
	}
	return strings.Trim(m, "`'")
}

// DBType 数据库类型与版本识别结果。
type DBType struct {
	// Type 类型："mysql" | "mariadb" | "unknown"
	Type string `json:"type"`
	// MajorMinor 主版本号，如 "5.7"、"8.4"、"10.11"
	MajorMinor string `json:"majorMinor"`
	// FullVersion 完整版本字符串，如 "5.7.44"、"10.11.8-MariaDB"
	FullVersion string `json:"fullVersion"`
}

// ParseDBType 从 VERSION() 返回的字符串解析数据库类型与版本。
//
// MySQL 版本格式：   "8.4.6"、"5.7.44-log"
// MariaDB 版本格式："10.11.8-MariaDB-1:..."、"11.0.2-MariaDB"
// Percona 等：       "8.0.35-27.1-Percona"
func ParseDBType(versionStr string) DBType {
	v := strings.ToLower(versionStr)
	t := "mysql"
	if strings.Contains(v, "mariadb") {
		t = "mariadb"
	} else if strings.Contains(v, "percona") {
		t = "percona"
	}

	// 提取主版本号 x.y 或 x.y.z 的前两位。
	mm := extractMajorMinor(versionStr)

	return DBType{
		Type:        t,
		MajorMinor:  mm,
		FullVersion: versionStr,
	}
}

// extractMajorMinor 从版本字符串提取 "主.次" 形式。
var versionRe = regexp.MustCompile(`(\d+)\.(\d+)`)

func extractMajorMinor(s string) string {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1] + "." + m[2]
}
