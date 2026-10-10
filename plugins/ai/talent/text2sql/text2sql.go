// Package text2sql 方言感知的 Text-to-SQL 能力包（对标 solon-ai-talent-text2sql:
// Text2SqlTalent）。按 06/07 契约落位 plugins/ai（db 依赖出 ai 模块）：
// 自适应方言引擎（六方言 + 驱动名归一）、分级元数据策略（FULL 全量注入 /
// DYNAMIC 目录 + get_table_schema 按需探测，>20 表自动切 DYNAMIC）、执行安全
// 护栏（只读校验/自动分页注入/AS 别名转义修复）、语义关联引导（外键关系线索
// + 表备注）。
//
// 与 Java 的偏差：
//   - schemaMode 用户显式设置后不再被表数量自动决策覆写（Java schemaInit 无条
//     件覆写，显式 DYNAMIC 在小表集下不可达——Go 修正并以此换得小表集可测）。
//   - 行集 JSON 由 map 序列化，键按字典序（Java ONode 保列序——输出面向 LLM
//     消费，无行为影响）；截断按 rune 计（Java 按 UTF-16 char 计，近似）。
//   - 业务失败一律普通结果串（Java 返回错误文案串而非抛异常——Error: 前缀文
//     案在结果体内，不占 IsError 通道）。
package text2sql

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	aicore "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/db"
	"github.com/crazy-airhead/aifei-go/log"
)

// asPattern 捕获 SQL 中的 AS 别名（Java AS_PATTERN 同款）。
var asPattern = regexp.MustCompile(`(?i)\s+AS\s+([a-zA-Z_][a-zA-Z0-9_]*)(\s+|,|$)`)

// SchemaMode 数据库结构同步模式。
type SchemaMode int

const (
	// SchemaFull 全量模式：所有表结构预加载进系统提示词（表少时更快）。
	SchemaFull SchemaMode = iota
	// SchemaDynamic 动态模式：仅注入表目录，具体结构经 get_table_schema 按需
	// 探测（大库省 Token）。
	SchemaDynamic
)

// Text2Sql 智能 Text-to-SQL 工具包。
type Text2Sql struct {
	aicore.TalentBase
	pool       *sql.DB
	driverName string
	tableNames []string

	initOnce        sync.Once
	tableRemarks    map[string]string
	globalRelations map[string][]string
	dialectSet      SqlDialect // 用户显式覆写
	dialect         SqlDialect
	reader          metaReader
	cachedSchema    string
	schemaModeSet   bool
	schemaMode      SchemaMode
	maxRows         int
	maxContextLen   int
	readOnly        bool
}

// New 构造：pool = 任意 *sql.DB（db 模块用户经 Config.Pool() 取得），
// driverName 用于方言识别（pgx/pq/godror 等常见 Go 驱动名自动归一）。
func New(pool *sql.DB, driverName string, tables ...string) *Text2Sql {
	return &Text2Sql{
		pool:          pool,
		driverName:    driverName,
		tableNames:    tables,
		maxRows:       50,
		maxContextLen: 8000,
		readOnly:      true,
	}
}

// NewFromConfig 从 db 模块配置构造（懒连接池 + 驱动名直通）。
func NewFromConfig(cfg *db.Config, tables ...string) (*Text2Sql, error) {
	pool, err := cfg.Pool()
	if err != nil {
		return nil, err
	}
	return New(pool, cfg.DriverName, tables...), nil
}

// MaxRows 覆写自动分页行数（缺省 50）。
func (t *Text2Sql) MaxRows(n int) *Text2Sql { t.maxRows = n; return t }

// MaxContextLength 覆写结果 JSON 截断阈值（缺省 8000）。
func (t *Text2Sql) MaxContextLength(n int) *Text2Sql { t.maxContextLen = n; return t }

// SchemaMode 显式指定结构模式（设置后表数量自动决策不再覆写）。
func (t *Text2Sql) SchemaMode(m SchemaMode) *Text2Sql {
	t.schemaMode, t.schemaModeSet = m, true
	return t
}

// ReadOnly 关闭只读闸（缺省开启）。
func (t *Text2Sql) ReadOnly(b bool) *Text2Sql { t.readOnly = b; return t }

// Dialect 显式覆写方言（跳过驱动名识别）。
func (t *Text2Sql) Dialect(d SqlDialect) *Text2Sql { t.dialectSet = d; return t }

func (t *Text2Sql) Name() string { return "text2sql" }

// schemaInit 双检锁懒初始化（Java schemaInit 的 sync.Once 对应物）：方言识
// 别/适配 → 表元数据加载（首错即断，Java 单 try 包全循环同款）→ 模式决策。
func (t *Text2Sql) schemaInit() {
	t.initOnce.Do(func() {
		if t.dialectSet != nil {
			t.dialect = t.dialectSet
		} else {
			t.dialect = SelectDialect(t.driverName)
		}
		if a, ok := t.dialect.(Adapter); ok {
			t.dialect = a.Adapt(t.pool)
		}
		t.reader = metaReaderFor(t.dialect)

		t.tableRemarks = map[string]string{}
		t.globalRelations = map[string][]string{}
		for _, name := range t.tableNames {
			meta, err := t.reader(t.pool, name)
			if err != nil {
				log.Warn("text2sql: meta load error on %s: %v", name, err)
				break
			}
			if meta.remark != "" {
				t.tableRemarks[name] = meta.remark
			}
			if len(meta.relations) > 0 {
				t.globalRelations[name] = append(t.globalRelations[name], meta.relations...)
			}
		}

		if !t.schemaModeSet {
			if len(t.tableNames) > 20 {
				t.schemaMode = SchemaDynamic
			} else {
				t.schemaMode = SchemaFull
			}
		}
		if t.schemaMode == SchemaFull {
			t.cachedSchema = t.ExtractSchemaInfo(t.tableNames)
		}
	})
}

func (t *Text2Sql) Description() string {
	t.schemaInit()
	return "数据库专家：具备深厚的 " + t.dialect.Name() + " 方言知识，擅长多表分析。"
}

func (t *Text2Sql) OnAttach(*aicore.Prompt) { t.schemaInit() }

// Instruction 三段式系统指令（Java getInstruction 逐字对标）。
func (t *Text2Sql) Instruction(*aicore.Prompt) string {
	t.schemaInit()
	now := time.Now().Format("2006-01-02 15:04:05")
	var sb strings.Builder

	sb.WriteString("## 1. 环境上下文\n")
	sb.WriteString("- **数据库方言**: " + t.dialect.Name() + "\n")
	sb.WriteString("- **系统时间**: " + now + "\n\n")

	if t.schemaMode == SchemaFull {
		sb.WriteString("## 2. 数据库结构 (Schema)\n")
		sb.WriteString(t.cachedSchema)
	} else {
		sb.WriteString("## 2. 数据库目录与关系地图\n")
		for _, tableName := range t.tableNames {
			sb.WriteString("- **" + tableName + "**")
			if remarks := t.tableRemarks[tableName]; remarks != "" {
				sb.WriteString(": " + remarks)
			}
			if rels := t.globalRelations[tableName]; len(rels) > 0 {
				sb.WriteString(" [关系线索: " + strings.Join(rels, ", ") + "]")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("\n**注意**: 编写 SQL 前必须调用 `get_table_schema` 探测具体字段。\n")
	}

	sb.WriteString("\n## 3. 执行准则\n")
	sb.WriteString("1. **权限**: 只读模式，严禁写操作。\n")
	sb.WriteString("2. **方言特供指引**: " + t.dialect.CustomInstruction() + "\n")
	sb.WriteString(fmt.Sprintf("3. **结果截断**: 默认必须分页，仅展示前 %d 条。\n", t.maxRows))
	sb.WriteString("4. **纠错逻辑**: 若执行报错，请对比方言要求检查引号、函数名或保留字。")
	return sb.String()
}

func (t *Text2Sql) Tools(*aicore.Prompt) []aicore.FunctionTool {
	t.schemaInit()
	exec := aicore.NewTool("execute_sql").
		Desc("执行单条 SELECT 查询语句。").
		StringParam("sql", "要执行的 SELECT 语句", true).
		Handler(func(ctx context.Context, args map[string]any) (any, error) {
			return t.ExecuteSql(ctx, argStr(args, "sql"))
		}).Build()
	if t.schemaMode == SchemaFull {
		return []aicore.FunctionTool{exec}
	}
	schema := aicore.NewTool("get_table_schema").
		Desc("获取特定表的详细列信息和 DDL").
		StringParam("tableName", "目标表名", true).
		Handler(func(_ context.Context, args map[string]any) (any, error) {
			return t.GetTableSchema(argStr(args, "tableName")), nil
		}).Build()
	return []aicore.FunctionTool{exec, schema}
}

// ExecuteSql 执行查询：空校验 → 去尾分号 → 别名转义修复 → 只读闸 → 分页补
// 全 → 行集 JSON（截断）。业务失败为普通结果串（Java 返回文案串不抛异常）。
func (t *Text2Sql) ExecuteSql(ctx context.Context, sqlText string) (string, error) {
	t.schemaInit()
	if strings.TrimSpace(sqlText) == "" {
		return "Error: SQL is empty.", nil
	}

	clean := strings.TrimSpace(sqlText)
	clean = strings.TrimSuffix(clean, ";")

	// 自动防御：为 AI 容易遗漏引号的别名加引号
	clean = t.repairAliases(clean)

	if t.readOnly && !strings.HasPrefix(strings.ToUpper(clean), "SELECT") {
		return "Error: Restricted to SELECT only.", nil
	}
	if !t.dialect.HasLimit(strings.ToUpper(clean)) {
		clean = t.dialect.ApplyPagination(clean, t.maxRows)
	}

	rows, err := queryRowsMap(ctx, t.pool, clean)
	if err != nil {
		return "SQL Error: " + err.Error() + "\nHint: " + t.dialect.ErrorHint(), nil
	}
	if len(rows) == 0 {
		return "Query OK. No data found.", nil
	}

	b, err := json.Marshal(rows)
	if err != nil {
		return "SQL Error: " + err.Error() + "\nHint: " + t.dialect.ErrorHint(), nil
	}
	js := string(b)
	if r := []rune(js); len(r) > t.maxContextLen {
		return string(r[:t.maxContextLen]) + "... [Truncated]", nil
	}
	return js, nil
}

// GetTableSchema 按需探测单表结构（DYNAMIC 模式的配套工具）。
func (t *Text2Sql) GetTableSchema(tableName string) string {
	t.schemaInit()
	for _, n := range t.tableNames {
		if n == tableName {
			return t.ExtractSchemaInfo([]string{tableName})
		}
	}
	return "Error: Table not found."
}

// ExtractSchemaInfo 渲染表结构（Java extractSchemaInfo 同款格式；任一表读取
// 失败整段返回 Metadata Error——Java 单 try 包全循环同款）。
func (t *Text2Sql) ExtractSchemaInfo(tables []string) string {
	var sb strings.Builder
	for _, tableName := range tables {
		meta, err := t.reader(t.pool, tableName)
		if err != nil {
			return "Metadata Error: " + err.Error()
		}
		sb.WriteString("* **Table: " + tableName + "**")
		if remarks := t.tableRemarks[tableName]; remarks != "" {
			sb.WriteString(" // " + remarks)
		}
		sb.WriteString("\n")

		pkSet := make(map[string]bool, len(meta.pks))
		for _, pk := range meta.pks {
			pkSet[pk] = true
		}
		for _, col := range meta.columns {
			typeDesc := col.typeName
			if col.size > 0 && col.size < 16777215 {
				typeDesc += fmt.Sprintf("(%d)", col.size)
			}
			if !col.nullable {
				typeDesc += " NOT NULL"
			}
			sb.WriteString("  - " + col.name + " (" + typeDesc + ")")
			if pkSet[col.name] {
				sb.WriteString(" [PK]")
			}
			if col.remark != "" {
				sb.WriteString(" // " + col.remark)
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// repairAliases AS 别名方言转义修复（Java repairAliases 同款替换：
// `\s+AS\s+alias` → ` AS "alias"`，分隔符保留）。
func (t *Text2Sql) repairAliases(s string) string {
	return asPattern.ReplaceAllStringFunc(s, func(m string) string {
		g := asPattern.FindStringSubmatch(m)
		if g == nil {
			return m
		}
		return " AS " + t.dialect.QuoteIdentifier(g[1]) + g[2]
	})
}

// queryRowsMap 通用行集读取（列名 → any；[]byte 转字符串，NULL 为 nil）。
func queryRowsMap(ctx context.Context, pool *sql.DB, query string) ([]map[string]any, error) {
	rows, err := pool.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			v := vals[i]
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			m[c] = v
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func argStr(args map[string]any, key string) string {
	if s, ok := args[key].(string); ok {
		return s
	}
	return ""
}
