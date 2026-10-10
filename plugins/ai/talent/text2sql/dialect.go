// dialect.go SQL 方言适配层（对标 solon-ai-talent-text2sql:SqlDialect 六件套）：
// 标识符转义/分页语法/方言特供指引/纠错提示的差异平替。Java 经 JDBC
// DatabaseMetaData 拿产品名；Go database/sql 无此面，改为构造器传入驱动名
// （常见 Go 驱动名先归一，pgx/pq→POSTGRESQL、godror/goracle→ORACLE），
// 亦可 Dialect() 显式覆写。findSchema/元数据行读取等 Connection 级方法
// 不迁——元数据读取见 meta.go 按方言族实现。
package text2sql

import (
	"database/sql"
	"strconv"
	"strings"
	"sync"
)

// SqlDialect SQL 方言适配接口。
type SqlDialect interface {
	// Name 方言名称（进系统提示词与 Description）。
	Name() string
	// Matched 是否匹配数据库产品名（大写形态）。
	Matched(product string) bool
	// QuoteIdentifier 包装标识符（反引号/双引号），防关键字冲突。
	QuoteIdentifier(name string) string
	// ApplyPagination 为 SQL 增加分页限制。
	ApplyPagination(sql string, maxRows int) string
	// CustomInstruction 给 AI 的方言特定提示。
	CustomInstruction() string
	// ErrorHint 执行报错时的纠错建议（Java 实现均未用异常入参，Go 收窄为无参）。
	ErrorHint() string
	// HasLimit 判断 SQL（大写形态）是否已含行数限制。
	HasLimit(upperSql string) bool
}

// DialectBase 默认实现：LIMIT/FETCH/TOP/ROWNUM 四关键字判定。
type DialectBase struct{}

func (DialectBase) HasLimit(upperSql string) bool {
	return strings.Contains(upperSql, "LIMIT") || strings.Contains(upperSql, "FETCH") ||
		strings.Contains(upperSql, "TOP ") || strings.Contains(upperSql, "ROWNUM")
}

// Adapter 连接期动态适配（Java adaptDialect 的 Go 对应物；如 H2 的 MySQL
// 兼容模式探测）。schemaInit 时若方言实现本接口则以连接池实测结果替换。
type Adapter interface {
	Adapt(pool *sql.DB) SqlDialect
}

// ---- Generic ----

// GenericDialect 标准 SQL（ANSI）缺省方言。
type GenericDialect struct{ DialectBase }

func (GenericDialect) Name() string                    { return "Generic SQL" }
func (GenericDialect) Matched(string) bool             { return false }
func (GenericDialect) QuoteIdentifier(n string) string { return "\"" + n + "\"" }
func (GenericDialect) ApplyPagination(s string, n int) string {
	return s + " FETCH FIRST " + itoa(n) + " ROWS ONLY"
}
func (GenericDialect) CustomInstruction() string { return "遵循标准 SQL 语法。" }
func (GenericDialect) ErrorHint() string         { return "请检查方言兼容性。" }

// ---- H2 ----

// H2Dialect H2 方言：保留字强制大写转义 + LIMIT 分页。
type H2Dialect struct{ DialectBase }

func (H2Dialect) Name() string          { return "H2 Database" }
func (H2Dialect) Matched(p string) bool { return strings.Contains(p, "H2") }
func (H2Dialect) QuoteIdentifier(n string) string {
	return "\"" + strings.ToUpper(n) + "\""
}
func (H2Dialect) ApplyPagination(s string, n int) string { return s + " LIMIT " + itoa(n) }
func (H2Dialect) CustomInstruction() string {
	return "H2 严格区分保留字。别名必须使用双引号(如 AS \"YEAR\")。长数字日期是序列化表现，严禁除以 1000，直接使用 YEAR() 等日期函数。"
}
func (H2Dialect) ErrorHint() string {
	return "H2 报错通常与未转义的保留字别名或非法的日期数学运算有关。"
}

// Adapt MySQL 兼容模式探测（Java 同款查询）。
func (H2Dialect) Adapt(pool *sql.DB) SqlDialect {
	var mode string
	if err := pool.QueryRow(
		`SELECT VALUE FROM INFORMATION_SCHEMA.SETTINGS WHERE NAME = 'MODE'`).Scan(&mode); err == nil &&
		strings.EqualFold(mode, "MySQL") {
		return MySQLDialect{}
	}
	return H2Dialect{}
}

// ---- MySQL ----

// MySQLDialect MySQL 系方言（MySQL/MariaDB/TiDB）：反引号 + LIMIT。
type MySQLDialect struct{ DialectBase }

func (MySQLDialect) Name() string { return "MySQL" }
func (MySQLDialect) Matched(p string) bool {
	return strings.Contains(p, "MYSQL") || strings.Contains(p, "MARIADB") || strings.Contains(p, "TIDB")
}
func (MySQLDialect) QuoteIdentifier(n string) string        { return "`" + n + "`" }
func (MySQLDialect) ApplyPagination(s string, n int) string { return s + " LIMIT " + itoa(n) }
func (MySQLDialect) CustomInstruction() string {
	return "使用反引号处理别名。支持标准的 LIMIT 分页。"
}
func (MySQLDialect) ErrorHint() string { return "请检查 SQL 语法及 MySQL 关键字冲突。" }

// ---- Oracle ----

// OracleDialect Oracle 系方言（Oracle/达梦）：大写双引号 + ROWNUM 分页。
type OracleDialect struct{ DialectBase }

func (OracleDialect) Name() string { return "Oracle" }
func (OracleDialect) Matched(p string) bool {
	return strings.Contains(p, "ORACLE") || strings.Contains(p, "DM") || strings.Contains(p, "DAMENG")
}
func (OracleDialect) QuoteIdentifier(n string) string {
	return "\"" + strings.ToUpper(n) + "\""
}
func (OracleDialect) ApplyPagination(s string, n int) string {
	return "SELECT * FROM (" + s + ") WHERE ROWNUM <= " + itoa(n)
}
func (OracleDialect) CustomInstruction() string {
	return "Oracle/达梦对别名建议使用双引号。必须通过 ROWNUM 限制行数。"
}
func (OracleDialect) ErrorHint() string {
	return "注意 Oracle 中 GROUP BY 必须包含 SELECT 中所有非聚合列。"
}

// HasLimit Oracle 覆写：ROWNUM / FETCH FIRST。
func (OracleDialect) HasLimit(upperSql string) bool {
	return strings.Contains(upperSql, "ROWNUM") || strings.Contains(upperSql, "FETCH FIRST")
}

// ---- PostgreSQL ----

// PostgreDialect PostgreSQL 系方言（PG/人大金仓/瀚高）：区分大小写双引号 + LIMIT。
type PostgreDialect struct{ DialectBase }

func (PostgreDialect) Name() string { return "PostgreSQL" }
func (PostgreDialect) Matched(p string) bool {
	return strings.Contains(p, "POSTGRE") || strings.Contains(p, "KINGBASE") || strings.Contains(p, "HIGHGO")
}
func (PostgreDialect) QuoteIdentifier(n string) string        { return "\"" + n + "\"" }
func (PostgreDialect) ApplyPagination(s string, n int) string { return s + " LIMIT " + itoa(n) }
func (PostgreDialect) CustomInstruction() string {
	return "PG/金仓/瀚高库区分大小写，别名请加双引号。支持 LIMIT 分页。"
}
func (PostgreDialect) ErrorHint() string { return "请确认字段名大小写是否匹配。" }

// ---- SQLite ----

// SQLiteDialect SQLite 方言：双引号 + LIMIT + strftime 日期指引。
type SQLiteDialect struct{ DialectBase }

func (SQLiteDialect) Name() string                           { return "SQLite" }
func (SQLiteDialect) Matched(p string) bool                  { return strings.Contains(p, "SQLITE") }
func (SQLiteDialect) QuoteIdentifier(n string) string        { return "\"" + n + "\"" }
func (SQLiteDialect) ApplyPagination(s string, n int) string { return s + " LIMIT " + itoa(n) }
func (SQLiteDialect) CustomInstruction() string {
	return "SQLite 特别指引：\n" +
		"1. 日期处理：不支持 YEAR()/MONTH() 函数，请使用 strftime('%Y', column) 或 date()。\n" +
		"2. 布尔值：使用 0 和 1 表示。\n" +
		"3. 别名：建议对所有字段别名加双引号，防止与内置函数名冲突。"
}
func (SQLiteDialect) ErrorHint() string {
	return "SQLite 报错通常是因为使用了不支持的 SQL 函数（如日期函数）或语法。"
}

// ---- 管理器 ----

var (
	dialectMu sync.RWMutex
	// dialects 注册顺序即匹配顺序（Java LinkedHashMap 同款保证）。
	dialects []SqlDialect
)

func init() {
	dialects = []SqlDialect{
		GenericDialect{}, H2Dialect{}, MySQLDialect{},
		OracleDialect{}, PostgreDialect{}, SQLiteDialect{},
	}
}

// RegisterDialect 注册/覆写方言（按 Name() 替换同名项，保序）。
func RegisterDialect(d SqlDialect) {
	if d == nil {
		return
	}
	dialectMu.Lock()
	defer dialectMu.Unlock()
	for i, old := range dialects {
		if old.Name() == d.Name() {
			dialects[i] = d
			return
		}
	}
	dialects = append(dialects, d)
}

// driverAliases 常见 Go 驱动名 → JDBC 产品名归一（database/sql 无产品名面）。
var driverAliases = map[string]string{
	"pgx": "POSTGRESQL", "pq": "POSTGRESQL",
	"godror": "ORACLE", "goracle": "ORACLE",
}

// SelectDialect 按驱动名筛选方言（未命中回落 Generic）。
func SelectDialect(driverName string) SqlDialect {
	product := driverAliases[strings.TrimSpace(driverName)]
	if product == "" {
		product = strings.ToUpper(strings.TrimSpace(driverName))
	}
	dialectMu.RLock()
	defer dialectMu.RUnlock()
	for _, d := range dialects {
		if d.Matched(product) {
			return d
		}
	}
	return GenericDialect{}
}

func itoa(n int) string { return strconv.Itoa(n) }
