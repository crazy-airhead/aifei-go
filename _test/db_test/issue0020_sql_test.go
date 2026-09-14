package db_test

import (
	"strings"
	"testing"

	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
)

// ISSUE-0020: 行首独占一行的 SQL 指令（#where/#and/#or/#para/#p/#orderBy）须保留
// 行尾换行。此前换行被 lexer 吃掉，相邻片段直接拼成 "…?AND name LIKE ?" 的坏 SQL；
// 对照 Java SqlKit 的 addDirective(name, class, keepLineBlank=true) 注册，仅容器指令
// #sql 保持吃掉换行。以下用例均为反引号多行字符串写法。

// 行首 #where 后跟行首 #and：条件之间不得粘连（曾渲染成 "WHERE age > ?AND name LIKE ?"）。
func TestIssue0020LineStartWhereAndKeepNewline(t *testing.T) {
	sk := dbsql.NewSqlKit("issue0020_where_and")

	sp := sk.GetSqlPara(`select * from user
#where(age, '>', age)
#and(name, 'like', name)`, map[string]interface{}{"age": 18, "name": "a"})

	if !strings.Contains(sp.Sql, "WHERE age > ?\nAND name LIKE ?") {
		t.Fatalf("conditions must be separated by newline, got: %q", sp.Sql)
	}
}

// 行首 #or 与 #where 混排（or 为 Go 增补的同族指令，同样须保留换行）。
func TestIssue0020LineStartOrKeepsNewline(t *testing.T) {
	sk := dbsql.NewSqlKit("issue0020_or")

	sp := sk.GetSqlPara(`select * from user
#where(age, '>', age)
#or(name, '=', name)`, map[string]interface{}{"age": 18, "name": "a"})

	if !strings.Contains(sp.Sql, "WHERE age > ?\nOR name = ?") {
		t.Fatalf("conditions must be separated by newline, got: %q", sp.Sql)
	}
}

// 行首指令后跟普通 SQL 文本行：不得与后续文本粘连（曾渲染成 "…?order by id"）。
func TestIssue0020LineStartDirectiveThenText(t *testing.T) {
	sk := dbsql.NewSqlKit("issue0020_dir_then_text")

	sp := sk.GetSqlPara(`select * from user
#where(age, '>', age)
order by id`, map[string]interface{}{"age": 18})

	if !strings.Contains(sp.Sql, "WHERE age > ?\norder by id") {
		t.Fatalf("directive output must be separated from following text, got: %q", sp.Sql)
	}
}

// 行首 #para 单独成行：占位符不得与下一行文本粘连（曾渲染成 "?and status = ?"）。
func TestIssue0020LineStartParaKeepsNewline(t *testing.T) {
	sk := dbsql.NewSqlKit("issue0020_para")

	sp := sk.GetSqlPara(`select * from user where id =
#para(id)
and status = #para(st)`, map[string]interface{}{"id": 1, "st": 2})

	if !strings.Contains(sp.Sql, "where id =\n?\nand status = ?") {
		t.Fatalf("placeholder must be separated by newlines, got: %q", sp.Sql)
	}
}

// 行首 #orderBy 单独成行：与前置 SQL 之间换行保留，多字段逗号分隔不受影响。
func TestIssue0020LineStartOrderByKeepsNewline(t *testing.T) {
	sk := dbsql.NewSqlKit("issue0020_order_by")

	sp := sk.GetSqlPara(`select * from user
#orderBy(age, name)`, map[string]interface{}{
		"orderBy": []map[string]interface{}{{"field": "age", "order": "desc"}, {"field": "name", "order": "asc"}},
	})

	if !strings.Contains(sp.Sql, "select * from user\nORDER BY age DESC, name ASC") {
		t.Fatalf("orderBy must stay on its own line, got: %q", sp.Sql)
	}
}

// #sql 块内多行指令写法整体渲染（含跳过的 nil 条件），且 #end/#for 仍吃掉自身换行。
func TestIssue0020SqlBlockMultilineDirectives(t *testing.T) {
	sk := dbsql.NewSqlKit("issue0020_sql_block")

	sk.AddSql("find", `#sql("find")
select * from user
#where(age, '>', age)
#and(name, 'like', name)
#and(city, '=', city)
#end`)

	sp := sk.GetSqlParaByID("find", map[string]interface{}{"age": 18, "city": "sz"})

	// name 为 nil 被跳过后，city 条件仍与前一条件以换行分隔，不产生空洞或粘连；
	// 末尾 \n 来自最后一个 #and 保留的行尾换行（#end 只吃自身行），SQL 中无害。
	want := "select * from user\nWHERE age > ?\nAND city = ?\n"
	if sp.Sql != want {
		t.Fatalf("want %q, got %q", want, sp.Sql)
	}
}

// #for 独占一行 + 行首 #and 生成的多条件（#for 本身不在 keepLineBlank 集合，
// 换行仍被吃掉，循环体首行顶格拼接；循环产出的各 #and 条件之间换行保留）。
func TestIssue0020ForWithLineStartAnd(t *testing.T) {
	sk := dbsql.NewSqlKit("issue0020_for_and")

	sp := sk.GetSqlPara(`select * from user where 1 = 1
#for(c : conds)
#and('age', '>', c)
#end`, map[string]interface{}{"conds": []int{18, 20}})

	if !strings.Contains(sp.Sql, "AND age > ?\nAND age > ?") {
		t.Fatalf("iterated conditions must be newline-separated, got: %q", sp.Sql)
	}
}

// 行内写法（指令与 SQL 文本同行）不受影响——既有单行模板的行为回归保护。
func TestIssue0020InlineLayoutUnchanged(t *testing.T) {
	sk := dbsql.NewSqlKit("issue0020_inline")

	sp := sk.GetSqlPara("select * from user #where(age, '>', age) #and(name, 'like', name)",
		map[string]interface{}{"age": 18, "name": "a"})

	if sp.Sql != "select * from user WHERE age > ? AND name LIKE ?" {
		t.Fatalf("inline layout must render unchanged, got: %q", sp.Sql)
	}
}

// 行尾写法（指令终结一行、前面有 SQL 文本、后面还有后续行）：指令不在行首，
// lexer 的换行吃除逻辑不参与，换行属于后续 TEXT token 天然保留。钉住该行为。
func TestIssue0020LineEndDirectiveKeepsNewline(t *testing.T) {
	sk := dbsql.NewSqlKit("issue0020_line_end")

	sp := sk.GetSqlPara(`select * from user
where age > #para(age)
and name like #para(name)`, map[string]interface{}{"age": 18, "name": "a"})

	want := "select * from user\nwhere age > ?\nand name like ?"
	if sp.Sql != want {
		t.Fatalf("want %q, got %q", want, sp.Sql)
	}
}

// 行尾指令 + 下一行行首指令混排：两种布局的换行机制叠加，各行输出互不粘连。
func TestIssue0020LineEndThenLineStartDirective(t *testing.T) {
	sk := dbsql.NewSqlKit("issue0020_end_then_start")

	sp := sk.GetSqlPara(`select * from user where id = #para(id)
#and(name, 'like', name)
order by id`, map[string]interface{}{"id": 1, "name": "a"})

	want := "select * from user where id = ?\nAND name LIKE ?\norder by id"
	if sp.Sql != want {
		t.Fatalf("want %q, got %q", want, sp.Sql)
	}
}
