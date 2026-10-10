// talent_text2sql_test.go P5-t5 text2sql 能力包黑盒测试（真 SQLite 文件库）：
// 方言管理器（驱动名归一/六方言匹配/Oracle 分页与 HasLimit 覆写/自定义注册）、
// FULL 模式系统指令三段式（表结构渲染/主键/NOT NULL）、DYNAMIC 模式（目录+
// 关系线索+get_table_schema 双工具）、execute_sql 护栏（只读闸/别名转义修复/
// 自动分页注入/截断/错误文案——业务失败为普通结果串）、NewFromConfig 走 db
// 配置装配、ActiveTalents 管线端到端。MySQL/PG 元数据读取器仅编译验证（同
// generator 模块 MySQL/PG 面的测试姿态）。
package ai_plugin_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aicore "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/db"
	_ "modernc.org/sqlite"

	"github.com/crazy-airhead/aifei-go/plugins/ai/talent/text2sql"
)

// newText2SqlDB 建临时文件 SQLite 库并灌入部门/员工两表（外键关联）。
func newText2SqlDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "t2s.db")
	pool, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Close() })
	schema := `
CREATE TABLE department (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL
);
CREATE TABLE employee (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  dept_id INTEGER REFERENCES department(id),
  salary REAL
);
INSERT INTO department (id, name) VALUES (1, '研发部'), (2, '销售部');
INSERT INTO employee (id, name, dept_id, salary) VALUES (1, '张三', 1, 100.5), (2, '李四', 2, 200), (3, '王五', 1, 300);`
	if _, err := pool.Exec(schema); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return pool
}

// callSqlTool 取工具并按参数调用。
func callSqlTool(t *testing.T, tt aicore.Talent, idx int, args map[string]any) *aicore.ToolResult {
	t.Helper()
	return tt.Tools(nil)[idx].Call(context.Background(), args)
}

// ---- 方言管理器 ----

func TestText2SqlDialectSelect(t *testing.T) {
	cases := []struct {
		driver string
		want   string
	}{
		{"sqlite", "SQLite"}, {"sqlite3", "SQLite"},
		{"pgx", "PostgreSQL"}, {"pq", "PostgreSQL"}, {"postgres", "PostgreSQL"},
		{"mysql", "MySQL"}, {"mariadb", "MySQL"}, {"tidb", "MySQL"},
		{"godror", "Oracle"}, {"dm", "Oracle"}, {"dameng", "Oracle"},
		{"h2", "H2 Database"},
		{"", "Generic SQL"}, {"unknown-db", "Generic SQL"},
	}
	for _, c := range cases {
		if got := text2sql.SelectDialect(c.driver).Name(); got != c.want {
			t.Errorf("SelectDialect(%q) = %q, want %q", c.driver, got, c.want)
		}
	}

	d := text2sql.SelectDialect("mysql")
	if q := d.QuoteIdentifier("year"); q != "`year`" {
		t.Fatalf("mysql quote = %q", q)
	}
	if p := d.ApplyPagination("SELECT 1", 10); p != "SELECT 1 LIMIT 10" {
		t.Fatalf("mysql pagination = %q", p)
	}

	o := text2sql.SelectDialect("godror")
	if p := o.ApplyPagination("SELECT 1", 5); p != "SELECT * FROM (SELECT 1) WHERE ROWNUM <= 5" {
		t.Fatalf("oracle pagination = %q", p)
	}
	if !o.HasLimit("SELECT * FROM T WHERE ROWNUM <= 5") || o.HasLimit("SELECT * FROM T LIMIT 5") {
		t.Fatal("oracle HasLimit override wrong")
	}
	if g := text2sql.SelectDialect("").ApplyPagination("SELECT 1", 3); g != "SELECT 1 FETCH FIRST 3 ROWS ONLY" {
		t.Fatalf("generic pagination = %q", g)
	}
	if q := text2sql.SelectDialect("h2").QuoteIdentifier("year"); q != `"YEAR"` {
		t.Fatalf("h2 quote = %q", q)
	}

	// 自定义方言注册与同名覆写（还原 = 同名替换为不匹配变体，无注销面）
	custom := stubDialect{match: true}
	text2sql.RegisterDialect(custom)
	if text2sql.SelectDialect("anything") != custom {
		t.Fatal("custom dialect (matched=*) not selected")
	}
	text2sql.RegisterDialect(stubDialect{}) // 同名替换，Matched=false 不再命中
	if text2sql.SelectDialect("anything").Name() != "Generic SQL" {
		t.Fatal("registry restore failed")
	}
}

// stubDialect 可控匹配的自定义方言（注册/同名覆写/还原链路验证用）。
type stubDialect struct {
	text2sql.DialectBase
	match bool
}

func (stubDialect) Name() string                       { return "stub-all" }
func (s stubDialect) Matched(string) bool              { return s.match }
func (stubDialect) QuoteIdentifier(n string) string    { return "[" + n + "]" }
func (stubDialect) ApplyPagination(string, int) string { return "STUB" }
func (stubDialect) CustomInstruction() string          { return "" }
func (stubDialect) ErrorHint() string                  { return "" }

// ---- FULL 模式指令 ----

func TestText2SqlInstructionFull(t *testing.T) {
	pool := newText2SqlDB(t)
	tt := text2sql.New(pool, "sqlite", "employee", "department")

	if got := tt.Description(); got != "数据库专家：具备深厚的 SQLite 方言知识，擅长多表分析。" {
		t.Fatalf("description = %q", got)
	}
	ins := tt.Instruction(nil)
	for _, want := range []string{
		"## 1. 环境上下文\n- **数据库方言**: SQLite\n- **系统时间**: ",
		"## 2. 数据库结构 (Schema)\n",
		"* **Table: employee**\n",
		"  - id (INTEGER) [PK]\n",
		"  - name (TEXT NOT NULL)\n",
		"  - dept_id (INTEGER)\n",
		"  - salary (REAL)\n",
		"* **Table: department**\n",
		"## 3. 执行准则\n",
		"1. **权限**: 只读模式，严禁写操作。\n",
		"2. **方言特供指引**: SQLite 特别指引：",
		"3. **结果截断**: 默认必须分页，仅展示前 50 条。\n",
		"4. **纠错逻辑**: 若执行报错，请对比方言要求检查引号、函数名或保留字。",
	} {
		if !strings.Contains(ins, want) {
			t.Errorf("instruction missing %q:\n%s", want, ins)
		}
	}

	// FULL：只部署 execute_sql
	if tools := tt.Tools(nil); len(tools) != 1 || tools[0].Name() != "execute_sql" {
		t.Fatalf("FULL tools = %v", tools)
	}
}

// ---- DYNAMIC 模式 ----

func TestText2SqlInstructionDynamic(t *testing.T) {
	pool := newText2SqlDB(t)
	tt := text2sql.New(pool, "sqlite", "employee", "department").
		SchemaMode(text2sql.SchemaDynamic)

	ins := tt.Instruction(nil)
	for _, want := range []string{
		"## 2. 数据库目录与关系地图\n",
		"- **employee** [关系线索: employee.dept_id -> department.id]\n",
		"- **department**\n",
		"\n**注意**: 编写 SQL 前必须调用 `get_table_schema` 探测具体字段。\n",
	} {
		if !strings.Contains(ins, want) {
			t.Errorf("instruction missing %q:\n%s", want, ins)
		}
	}
	if strings.Contains(ins, "- id (INTEGER)") {
		t.Errorf("DYNAMIC must not inline columns:\n%s", ins)
	}

	tools := tt.Tools(nil)
	if len(tools) != 2 {
		t.Fatalf("DYNAMIC tools = %d, want 2", len(tools))
	}

	// get_table_schema：有效表 → 结构；未知表 → 文案
	res := callSqlTool(t, tt, 1, map[string]any{"tableName": "department"})
	if res.IsError || !strings.Contains(res.ContentText(), "  - name (TEXT NOT NULL)\n") {
		t.Fatalf("get_table_schema = %q err=%v", res.ContentText(), res.IsError)
	}
	res = callSqlTool(t, tt, 1, map[string]any{"tableName": "nope"})
	if res.IsError || res.ContentText() != "Error: Table not found." {
		t.Fatalf("unknown table = %q err=%v", res.ContentText(), res.IsError)
	}
}

// ---- execute_sql 护栏 ----

func TestText2SqlExecuteSql(t *testing.T) {
	pool := newText2SqlDB(t)
	tt := text2sql.New(pool, "sqlite", "employee", "department")

	// 自动分页注入：无 LIMIT 输入 + MaxRows(2) → 恰好 2 行
	tt2 := text2sql.New(pool, "sqlite", "employee").MaxRows(2)
	res := callSqlTool(t, tt2, 0, map[string]any{"sql": "SELECT * FROM employee ORDER BY id"})
	var rows []map[string]any
	if res.IsError || json.Unmarshal([]byte(res.ContentText()), &rows) != nil {
		t.Fatalf("paginated = %q err=%v", res.ContentText(), res.IsError)
	}
	if len(rows) != 2 {
		t.Fatalf("pagination rows = %d, want 2", len(rows))
	}

	// 已有 LIMIT 不重复注入
	res = callSqlTool(t, tt, 0, map[string]any{"sql": "SELECT * FROM employee LIMIT 1"})
	if res.IsError || json.Unmarshal([]byte(res.ContentText()), &rows) != nil || len(rows) != 1 {
		t.Fatalf("explicit limit = %q err=%v", res.ContentText(), res.IsError)
	}

	// AS 别名转义修复（year 是保留字倾向）+ 尾分号剥离
	res = callSqlTool(t, tt, 0, map[string]any{"sql": "SELECT dept_id AS year FROM employee ORDER BY id LIMIT 1;"})
	if res.IsError {
		t.Fatalf("alias repair = %v", res.ContentText())
	}
	if !strings.Contains(res.ContentText(), `"year"`) {
		t.Fatalf("alias key missing: %s", res.ContentText())
	}

	// 空校验 / 只读闸 / 空结果 —— 普通结果串（非 IsError）
	res = callSqlTool(t, tt, 0, map[string]any{"sql": "   "})
	if res.IsError || res.ContentText() != "Error: SQL is empty." {
		t.Fatalf("blank = %q err=%v", res.ContentText(), res.IsError)
	}
	res = callSqlTool(t, tt, 0, map[string]any{"sql": "DELETE FROM employee"})
	if res.IsError || res.ContentText() != "Error: Restricted to SELECT only." {
		t.Fatalf("readonly = %q err=%v", res.ContentText(), res.IsError)
	}
	res = callSqlTool(t, tt, 0, map[string]any{"sql": "SELECT * FROM employee WHERE id = 999"})
	if res.IsError || res.ContentText() != "Query OK. No data found." {
		t.Fatalf("empty rows = %q err=%v", res.ContentText(), res.IsError)
	}

	// SQL 错误：SQL Error + 方言 Hint（仍是普通结果串）
	res = callSqlTool(t, tt, 0, map[string]any{"sql": "SELECT * FROM nope"})
	if res.IsError || !strings.HasPrefix(res.ContentText(), "SQL Error: ") ||
		!strings.Contains(res.ContentText(), "\nHint: SQLite 报错通常是因为使用了不支持的 SQL 函数（如日期函数）或语法。") {
		t.Fatalf("sql error = %q err=%v", res.ContentText(), res.IsError)
	}

	// 截断：阈值 10 rune → 前 10 rune + 标记
	small := text2sql.New(pool, "sqlite", "employee").MaxContextLength(10)
	res = callSqlTool(t, small, 0, map[string]any{"sql": "SELECT * FROM employee LIMIT 3"})
	if res.IsError {
		t.Fatalf("truncated = %v", res.ContentText())
	}
	want := string([]rune(res.ContentText())[:10]) // 自洽：前缀即截断产物
	if !strings.HasSuffix(res.ContentText(), "... [Truncated]") || len([]rune(res.ContentText())) != 10+len("... [Truncated]") {
		t.Fatalf("truncation = %q", res.ContentText())
	}
	if want == res.ContentText() {
		t.Fatal("truncation marker sanity")
	}
}

// ReadOnly(false) 关闸：含 LIMIT 的写语句真执行。
func TestText2SqlReadOnlyOff(t *testing.T) {
	pool := newText2SqlDB(t)
	tt := text2sql.New(pool, "sqlite", "employee").ReadOnly(false)

	res := callSqlTool(t, tt, 0, map[string]any{
		"sql": "DELETE FROM employee WHERE id IN (SELECT id FROM employee LIMIT 1)"})
	if res.IsError || res.ContentText() != "Query OK. No data found." {
		t.Fatalf("delete = %q err=%v", res.ContentText(), res.IsError)
	}
	var n int
	if err := pool.QueryRow("SELECT COUNT(*) FROM employee").Scan(&n); err != nil || n != 2 {
		t.Fatalf("after delete count = %d err=%v, want 2", n, err)
	}
}

// ---- db 配置装配缝 ----

func TestText2SqlNewFromConfig(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "t2s_cfg.db")
	pool, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT);
		INSERT INTO t (id, v) VALUES (1, 'ok')`); err != nil {
		t.Fatal(err)
	}
	pool.Close()

	// 临时配置注册（不污染默认配置表）
	if err := db.InitWithID("t2s_test", "sqlite", dsn); err != nil {
		t.Fatal(err)
	}
	tt, err := text2sql.NewFromConfig(db.GetConfig("t2s_test"), "t")
	if err != nil {
		t.Fatal(err)
	}
	if got := tt.Description(); got != "数据库专家：具备深厚的 SQLite 方言知识，擅长多表分析。" {
		t.Fatalf("description = %q", got)
	}
	res, err := tt.ExecuteSql(context.Background(), "SELECT v FROM t WHERE id = 1")
	if err != nil || res != `[{"v":"ok"}]` {
		t.Fatalf("ExecuteSql = %q err=%v", res, err)
	}
	db.GetConfig("t2s_test").Close()

	if _, err := os.Stat(dsn); err != nil {
		t.Fatalf("db file: %v", err)
	}
}

// ---- 管线端到端 ----

func TestText2SqlPipeline(t *testing.T) {
	pool := newText2SqlDB(t)

	opts := &aicore.ChatOptions{}
	opts.Talents = []aicore.Talent{
		text2sql.New(pool, "sqlite", "employee", "department").SchemaMode(text2sql.SchemaDynamic),
	}
	var sb strings.Builder
	aicore.ActiveTalents(opts, nil, &sb)

	if len(opts.Tools) != 2 {
		t.Fatalf("deployed tools = %d, want 2 (DYNAMIC)", len(opts.Tools))
	}
	for _, tool := range opts.Tools {
		if name, _ := tool.Meta()["talent"].(string); name != "text2sql" {
			t.Fatalf("tool %q meta.talent = %q", tool.Name(), name)
		}
	}
	if !strings.Contains(sb.String(), "数据库专家：具备深厚的 SQLite 方言知识") {
		t.Fatalf("instruction block missing:\n%s", sb.String())
	}
}
