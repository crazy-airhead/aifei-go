package sql

import (
	"strings"
	"testing"
)

func TestGetSqlParaBasic(t *testing.T) {
	sk := NewSqlKit("test_basic")

	sp := sk.GetSqlPara("select * from user where id = #para(id)", map[string]interface{}{"id": 123})
	if !strings.Contains(sp.Sql, "select * from user where id = ?") {
		t.Fatalf("expected SQL with ?, got: %s", sp.Sql)
	}
	if len(sp.Paras) != 1 {
		t.Fatalf("expected 1 param, got: %d", len(sp.Paras))
	}
}

func TestGetSqlParaPositional(t *testing.T) {
	sk := NewSqlKit("test_pos")

	sp := sk.GetSqlParaWithArgs("select * from user where id = #para(0) and name = #para(1)", 123, "test")
	if !strings.Contains(sp.Sql, "id = ?") || !strings.Contains(sp.Sql, "name = ?") {
		t.Fatalf("expected SQL with 2 placeholders, got: %s", sp.Sql)
	}
	if len(sp.Paras) != 2 {
		t.Fatalf("expected 2 params, got: %d", len(sp.Paras))
	}
}

func TestParaLike(t *testing.T) {
	sk := NewSqlKit("test_like")

	sp := sk.GetSqlPara("select * from user where name like #para(name, 'like')", map[string]interface{}{"name": "test"})
	if !strings.Contains(sp.Sql, "name like ?") {
		t.Fatalf("expected name like ?, got: %s", sp.Sql)
	}
	if len(sp.Paras) != 1 {
		t.Fatalf("expected 1 param, got: %d", len(sp.Paras))
	}
	// Like should wrap with %%
	if sp.Paras[0] != "%test%" {
		t.Fatalf("expected %%test%%, got: %v", sp.Paras[0])
	}
}

func TestWhereEqual(t *testing.T) {
	sk := NewSqlKit("test_where_eq")

	sql := "select * from user #where(age, '=', age)"
	sp := sk.GetSqlPara(sql, map[string]interface{}{"age": 18})
	if !strings.Contains(sp.Sql, "WHERE age = ?") {
		t.Fatalf("expected 'WHERE age = ?', got: %s", sp.Sql)
	}
	if len(sp.Paras) != 1 || sp.Paras[0] != 18 {
		t.Fatalf("expected param 18, got: %v", sp.Paras)
	}
}

func TestWhereEqualNil(t *testing.T) {
	sk := NewSqlKit("test_where_nil")

	// When value is nil, no WHERE clause generated
	sql := "select * from user #where(age, '=', age)"
	sp := sk.GetSqlPara(sql, map[string]interface{}{"age": nil})
	if strings.Contains(sp.Sql, "WHERE") {
		t.Fatalf("expected no WHERE for nil value, got: %s", sp.Sql)
	}
	if len(sp.Paras) != 0 {
		t.Fatalf("expected 0 params for nil, got: %d", len(sp.Paras))
	}
}

func TestWhereIsNull(t *testing.T) {
	sk := NewSqlKit("test_where_null")

	sql := "select * from user #where(nickname, 'is null')"
	sp := sk.GetSqlPara(sql, map[string]interface{}{"nickname": true})
	if !strings.Contains(sp.Sql, "WHERE nickname IS NULL") {
		t.Fatalf("expected 'WHERE nickname IS NULL', got: %s", sp.Sql)
	}
}

func TestWhereAnd(t *testing.T) {
	sk := NewSqlKit("test_where_and")

	sql := "select * from user #where(age, '>', age) #and(name, 'like', name)"
	filter := map[string]interface{}{"age": 18, "name": "test"}
	sp := sk.GetSqlPara(sql, filter)

	if !strings.Contains(sp.Sql, "WHERE age > ? AND name LIKE ?") {
		t.Fatalf("expected 'WHERE age > ? AND name LIKE ?', got: %s", sp.Sql)
	}
	if len(sp.Paras) != 2 {
		t.Fatalf("expected 2 params, got: %d", len(sp.Paras))
	}
}

func TestWhereIn(t *testing.T) {
	sk := NewSqlKit("test_where_in")

	sql := "select * from user #where(status, 'in', status)"
	// Pass a slice
	sp := sk.GetSqlPara(sql, map[string]interface{}{
		"status": []string{"active", "pending"},
	})
	if !strings.Contains(sp.Sql, "WHERE status IN (?, ?)") {
		t.Fatalf("expected 'WHERE status IN (?, ?)', got: %s", sp.Sql)
	}
	if len(sp.Paras) != 2 {
		t.Fatalf("expected 2 params for IN, got: %d", len(sp.Paras))
	}
}

func TestWhereBetween(t *testing.T) {
	sk := NewSqlKit("test_where_between")

	sql := "select * from user #where(age, 'between', age)"
	sp := sk.GetSqlPara(sql, map[string]interface{}{
		"age": []int{18, 60},
	})
	if !strings.Contains(sp.Sql, "WHERE age BETWEEN ? AND ?") {
		t.Fatalf("expected 'WHERE age BETWEEN ? AND ?', got: %s", sp.Sql)
	}
	if len(sp.Paras) != 2 {
		t.Fatalf("expected 2 params for BETWEEN, got: %d", len(sp.Paras))
	}
}

func TestWhereContains(t *testing.T) {
	sk := NewSqlKit("test_where_contains")

	sql := "select * from user #where(name, 'contains', name)"
	sp := sk.GetSqlPara(sql, map[string]interface{}{"name": "test"})
	if !strings.Contains(sp.Sql, "WHERE name LIKE ?") {
		t.Fatalf("expected 'WHERE name LIKE ?', got: %s", sp.Sql)
	}
	if sp.Paras[0] != "%test%" {
		t.Fatalf("expected %%test%%, got: %v", sp.Paras[0])
	}
}

func TestAddSql(t *testing.T) {
	sk := NewSqlKit("test_add_sql")

	sql := `#sql("findUser")
select * from user where id = #para(0)
#end`

	sk.AddSql("findUser", sql)

	sp := sk.GetSqlParaByIDWithArgs("findUser", 123)
	if sp == nil {
		t.Fatal("expected SqlPara, got nil")
	}
	if !strings.Contains(sp.Sql, "select * from user where id = ?") {
		t.Fatalf("expected 'select * from user where id = ?', got: %s", sp.Sql)
	}
	if len(sp.Paras) != 1 || sp.Paras[0] != 123 {
		t.Fatalf("expected param 123, got: %v", sp.Paras)
	}
}

func TestAddSqlWithWhere(t *testing.T) {
	sk := NewSqlKit("test_add_sql_where")

	sql := `#sql("findByFilter")
select * from user #where(age, '>', age) #and(name, 'like', name)
#end`

	sk.AddSql("findByFilter", sql)

	sp := sk.GetSqlParaByID("findByFilter", map[string]interface{}{
		"age": 18, "name": "test",
	})
	if sp == nil {
		t.Fatal("expected SqlPara, got nil")
	}
	if !strings.Contains(sp.Sql, "WHERE age > ? AND name LIKE ?") {
		t.Fatalf("expected WHERE clause, got: %s", sp.Sql)
	}
}

func TestOrderBy(t *testing.T) {
	sk := NewSqlKit("test_orderby")

	sql := "select * from user #orderBy(updated, age)"
	sp := sk.GetSqlPara(sql, map[string]interface{}{
		"orderBy": map[string]interface{}{
			"field": "updated",
			"order": "desc",
		},
	})
	if !strings.Contains(sp.Sql, "ORDER BY updated DESC") {
		t.Fatalf("expected 'ORDER BY updated DESC', got: %s", sp.Sql)
	}
}

func TestOrderByCustomName(t *testing.T) {
	sk := NewSqlKit("test_orderby_custom")

	sql := "select * from user #orderBy($sort, updated)"
	sp := sk.GetSqlPara(sql, map[string]interface{}{
		"sort": map[string]interface{}{
			"field": "updated",
			"order": "asc",
		},
	})
	if !strings.Contains(sp.Sql, "ORDER BY updated ASC") {
		t.Fatalf("expected 'ORDER BY updated ASC', got: %s", sp.Sql)
	}
}

func TestOrderByFieldMapping(t *testing.T) {
	sk := NewSqlKit("test_orderby_mapping")

	sql := "select * from user #orderBy('updated_time:updateTime')"
	sp := sk.GetSqlPara(sql, map[string]interface{}{
		"orderBy": map[string]interface{}{
			"field": "updateTime",
			"order": "desc",
		},
	})
	if !strings.Contains(sp.Sql, "ORDER BY updated_time DESC") {
		t.Fatalf("expected 'ORDER BY updated_time DESC', got: %s", sp.Sql)
	}
}
