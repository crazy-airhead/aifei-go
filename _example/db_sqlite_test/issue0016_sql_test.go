package db_sqlite_test

import (
	"strings"
	"testing"

	dbsql "github.com/crazy-airhead/aifei-go/db/sql"
)

// ISSUE-0016: operator 全小写别名注册（对照 Java Operator.createCache 的 toLowerCase）。
func TestIssue0016OperatorLowercaseAliases(t *testing.T) {
	cases := []struct {
		key  string
		want string
	}{
		{"contains", "LIKE"},
		{"notcontains", "NOT LIKE"},
		{"startswith", "LIKE"},
		{"endswith", "LIKE"},
		// 既有的大小写形态保持不变
		{"IN", "IN"},
		{"in", "IN"},
		{"not in", "NOT IN"},
		{"=", "="},
		{"<>", "!="},
	}
	for _, c := range cases {
		op := dbsql.SqlOperatorFrom(c.key)
		if op == nil {
			t.Errorf("operator %q not registered", c.key)
			continue
		}
		if op.SQL() != c.want {
			t.Errorf("operator %q: want SQL %q, got %q", c.key, c.want, op.SQL())
		}
	}
}

// ISSUE-0016: #orderBy 运行时 field/order 带前后空格仍能命中白名单（对照 Java 的 .trim()）。
func TestIssue0016OrderByTrimsRuntimeValue(t *testing.T) {
	sk := dbsql.NewSqlKit("test_orderby_trim_runtime")

	tmpl := "select * from user #orderBy(updated_time)"
	sp := sk.GetSqlPara(tmpl, map[string]interface{}{
		"orderBy": map[string]interface{}{"field": " updated_time ", "order": " DESC "},
	})
	if !strings.Contains(sp.Sql, "ORDER BY updated_time DESC") {
		t.Fatalf("expected trimmed runtime match, got: %s", sp.Sql)
	}
}

// ISSUE-0016: #orderBy 字符串字面量白名单带空格也能解析（sqlField:clientField 映射 + 普通字段）。
func TestIssue0016OrderByTrimsLiteralWhitelist(t *testing.T) {
	sk := dbsql.NewSqlKit("test_orderby_trim_literal")

	tmpl := "select * from user #orderBy(' updated_time : updateTime ')"
	sp := sk.GetSqlPara(tmpl, map[string]interface{}{
		"orderBy": map[string]interface{}{"field": "updateTime", "order": "asc"},
	})
	if !strings.Contains(sp.Sql, "ORDER BY updated_time ASC") {
		t.Fatalf("expected trimmed whitelist match, got: %s", sp.Sql)
	}
}

// ISSUE-0016: ParaDirective 全局开关 setCheckParaAssigned。
func TestIssue0016ParaCheckSwitch(t *testing.T) {
	// 默认开启：未赋值的 #para(name) 必须抛异常。
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic when checkParaAssigned is on and param is unassigned")
			}
		}()
		sk := dbsql.NewSqlKit("test_para_check_on")
		sk.GetSqlPara("select * from user where x = #para(name)", map[string]interface{}{})
	}()

	// 关闭后：未赋值不再抛异常，按 nil 输出占位符。
	dbsql.SetCheckParaAssigned(false)
	defer dbsql.SetCheckParaAssigned(true)

	sk := dbsql.NewSqlKit("test_para_check_off")
	sp := sk.GetSqlPara("select * from user where x = #para(name)", map[string]interface{}{})
	if !strings.Contains(sp.Sql, "x = ?") {
		t.Fatalf("expected placeholder for unassigned para, got: %s", sp.Sql)
	}
	if len(sp.Paras) != 1 || sp.Paras[0] != nil {
		t.Fatalf("expected single nil para, got: %v", sp.Paras)
	}
}
