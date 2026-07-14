package sql

import (
	"reflect"
	"testing"
)

func TestParseFromSingleTable(t *testing.T) {
	r := Parse("SELECT * FROM user")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
	assertProjections(t, r, []Projection{{Star: true, Column: "*"}})
}

func TestParseFromWithAlias(t *testing.T) {
	r := Parse("SELECT * FROM user u")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "u"}})
	assertAliasToTable(t, r, map[string]string{"user": "user", "u": "user"})
}

func TestParseFromWithAS(t *testing.T) {
	r := Parse("SELECT u.name FROM user AS u")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "u"}})
	assertProjections(t, r, []Projection{{TableAlias: "u", Column: "name", Label: "name"}})
}

func TestParseFromMultipleTablesComma(t *testing.T) {
	r := Parse("SELECT * FROM user u, dept d")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
	})
}

func TestParseJoinInner(t *testing.T) {
	r := Parse("SELECT u.name, d.name AS dept_name FROM user u INNER JOIN dept d ON u.dept_id = d.id")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
	})
	assertProjections(t, r, []Projection{
		{TableAlias: "u", Column: "name", Label: "name"},
		{TableAlias: "d", Column: "name", Label: "dept_name"},
	})
}

func TestParseJoinLeft(t *testing.T) {
	r := Parse("SELECT * FROM user u LEFT JOIN dept d ON u.dept_id = d.id")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
	})
}

func TestParseJoinRight(t *testing.T) {
	r := Parse("SELECT * FROM user u RIGHT JOIN dept d ON u.dept_id = d.id")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
	})
}

func TestParseJoinFullOuter(t *testing.T) {
	r := Parse("SELECT * FROM user u FULL OUTER JOIN dept d ON u.dept_id = d.id")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
	})
}

func TestParseJoinCross(t *testing.T) {
	r := Parse("SELECT * FROM user CROSS JOIN dept")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "user"},
		{Table: "dept", Alias: "dept"},
	})
}

func TestParseJoinNatural(t *testing.T) {
	r := Parse("SELECT * FROM user NATURAL JOIN dept")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "user"},
		{Table: "dept", Alias: "dept"},
	})
}

func TestParseJoinBare(t *testing.T) {
	r := Parse("SELECT * FROM user u JOIN dept d ON u.dept_id = d.id")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
	})
}

func TestParseJoinUsing(t *testing.T) {
	r := Parse("SELECT * FROM user u JOIN dept d USING (dept_id)")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
	})
}

func TestParseMultipleJoins(t *testing.T) {
	r := Parse("SELECT * FROM user u JOIN dept d ON u.dept_id = d.id JOIN role r ON d.role_id = r.id")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
		{Table: "role", Alias: "r"},
	})
}

func TestParseSelectStar(t *testing.T) {
	r := Parse("SELECT * FROM user")
	assertProjections(t, r, []Projection{{Star: true, Column: "*"}})
}

func TestParseSelectAliasStar(t *testing.T) {
	r := Parse("SELECT u.*, d.name FROM user u JOIN dept d ON u.dept_id = d.id")
	assertProjections(t, r, []Projection{
		{TableAlias: "u", Star: true, Column: "*"},
		{TableAlias: "d", Column: "name", Label: "name"},
	})
}

func TestParseSelectBareColumn(t *testing.T) {
	r := Parse("SELECT name, age FROM user")
	assertProjections(t, r, []Projection{
		{Column: "name", Label: "name"},
		{Column: "age", Label: "age"},
	})
}

func TestParseSelectQualifiedColumn(t *testing.T) {
	r := Parse("SELECT u.name, u.age FROM user u")
	assertProjections(t, r, []Projection{
		{TableAlias: "u", Column: "name", Label: "name"},
		{TableAlias: "u", Column: "age", Label: "age"},
	})
}

func TestParseSelectColumnAsLabel(t *testing.T) {
	r := Parse("SELECT name AS n, age AS a FROM user")
	assertProjections(t, r, []Projection{
		{Column: "name", Label: "n"},
		{Column: "age", Label: "a"},
	})
}

func TestParseSelectQualifiedColumnAsLabel(t *testing.T) {
	r := Parse("SELECT u.name AS user_name FROM user u")
	assertProjections(t, r, []Projection{
		{TableAlias: "u", Column: "name", Label: "user_name"},
	})
}

func TestParseSelectFunctionAlias(t *testing.T) {
	// COUNT is not a keyword, it's an ident. "COUNT(*)" tokenizes as
	// COUNT (ident), ( (lparen), * (star), ) (rparen). The AS label search
	// from the end finds "AS cnt".
	r := Parse("SELECT COUNT(*) AS cnt FROM user")
	assertProjections(t, r, []Projection{
		{Label: "cnt"},
	})
}

func TestParseSelectFunctionNoAlias(t *testing.T) {
	// COUNT(*) without AS — no label, so projection is skipped
	r := Parse("SELECT COUNT(*) FROM user")
	assertProjections(t, r, nil)
}

func TestParseSelectNumberAs(t *testing.T) {
	// "1" is a number, skipped by tokenizer. "SELECT 1 AS one" becomes
	// SELECT (keyword), AS (keyword), one (ident). AS is right after SELECT
	// with no expression — parseProjection handles this as Label-only.
	r := Parse("SELECT 1 AS one")
	assertProjections(t, r, []Projection{
		{Label: "one"},
	})
}

func TestParseSubqueryDerivedTable(t *testing.T) {
	r := Parse("SELECT * FROM (SELECT id, name FROM user) AS sub")
	assertTables(t, r, []TableRef{
		{Table: "sub", Alias: "sub", FromSubquery: true},
	})
}

func TestParseSubqueryDerivedTableNoAs(t *testing.T) {
	r := Parse("SELECT * FROM (SELECT id FROM user) sub")
	assertTables(t, r, []TableRef{
		{Table: "sub", Alias: "sub", FromSubquery: true},
	})
}

func TestParseSubqueryDerivedTableNoAlias(t *testing.T) {
	r := Parse("SELECT * FROM (SELECT id FROM user)")
	// No alias after closing paren — subquery not registered
	assertTables(t, r, nil)
}

func TestParseCommentsSingleLine(t *testing.T) {
	r := Parse("-- this is a comment\nSELECT * FROM user")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
}

func TestParseCommentsBlock(t *testing.T) {
	r := Parse("/* block comment */ SELECT * FROM user")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
}

func TestParseCommentsInline(t *testing.T) {
	r := Parse("SELECT * /* inline */ FROM user")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
}

func TestParseQuotedIdentifiersBacktick(t *testing.T) {
	r := Parse("SELECT * FROM `user` AS u")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "u"}})
}

func TestParseQuotedIdentifiersDoubleQuote(t *testing.T) {
	r := Parse("SELECT * FROM \"user\" AS u")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "u"}})
}

func TestParseQuotedIdentifiersBracket(t *testing.T) {
	r := Parse("SELECT * FROM [user] AS u")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "u"}})
}

func TestParseSchemaPrefix(t *testing.T) {
	r := Parse("SELECT * FROM db.user AS u")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "u"}})
}

func TestParseCaseInsensitive(t *testing.T) {
	r := Parse("select * from User u join Dept d on u.dept_id = d.id")
	assertTables(t, r, []TableRef{
		{Table: "User", Alias: "u"},
		{Table: "Dept", Alias: "d"},
	})
}

func TestParseMixedCase(t *testing.T) {
	r := Parse("Select u.Name, d.Name As dept_name From user u Join dept d On u.dept_id = d.id")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
	})
	assertProjections(t, r, []Projection{
		{TableAlias: "u", Column: "Name", Label: "Name"},
		{TableAlias: "d", Column: "Name", Label: "dept_name"},
	})
}

func TestParseUnionFirstBranch(t *testing.T) {
	r := Parse("SELECT * FROM user u UNION SELECT * FROM admin a")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "u"}})
}

func TestParseSelectNoFrom(t *testing.T) {
	r := Parse("SELECT 1")
	assertTables(t, r, nil)
	assertProjections(t, r, nil)
}

func TestParseEmptyString(t *testing.T) {
	r := Parse("")
	assertTables(t, r, nil)
	assertProjections(t, r, nil)
}

func TestParseAliasToTableSelfReference(t *testing.T) {
	r := Parse("SELECT * FROM user")
	assertAliasToTable(t, r, map[string]string{"user": "user"})
}

func TestParseAliasToTableWithAlias(t *testing.T) {
	r := Parse("SELECT * FROM user u")
	assertAliasToTable(t, r, map[string]string{"user": "user", "u": "user"})
}

func TestParseWhereStopsFrom(t *testing.T) {
	r := Parse("SELECT * FROM user WHERE age > 10")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
}

func TestParseGroupByStopsFrom(t *testing.T) {
	r := Parse("SELECT dept_id, COUNT(*) AS cnt FROM user GROUP BY dept_id")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
}

func TestParseOrderByStopsFrom(t *testing.T) {
	r := Parse("SELECT * FROM user ORDER BY age")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
}

func TestParseLimitStopsFrom(t *testing.T) {
	r := Parse("SELECT * FROM user LIMIT 10")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
}

func TestParseLeftOuterJoin(t *testing.T) {
	r := Parse("SELECT * FROM user u LEFT OUTER JOIN dept d ON u.dept_id = d.id")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
	})
}

func TestParseDistinct(t *testing.T) {
	r := Parse("SELECT DISTINCT * FROM user")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
}

func TestParseComplexExpression(t *testing.T) {
	// Subquery in SELECT should not interfere with FROM collection
	r := Parse("SELECT a.id, (SELECT MAX(x) FROM t) AS max_x FROM a")
	assertTables(t, r, []TableRef{
		{Table: "a", Alias: "a"},
	})
}

func TestParseExpressionAs(t *testing.T) {
	// 1 + 1 — "1" is skipped (number), "+" is skipped.
	// Only "AS" and "result" remain. parseProjection gets [AS, result] → label-only.
	r := Parse("SELECT 1 + 1 AS result FROM user")
	assertProjections(t, r, []Projection{{Label: "result"}})
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
}

func TestParseWithStringLiterals(t *testing.T) {
	r := Parse("SELECT * FROM user WHERE name = 'hello'")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
}

func TestParseNestedParens(t *testing.T) {
	r := Parse("SELECT * FROM user u JOIN dept d ON (u.a = d.a AND (u.b = d.b OR u.c = d.c))")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
	})
}

func TestParseWithSemicolon(t *testing.T) {
	r := Parse("SELECT * FROM user;")
	assertTables(t, r, []TableRef{{Table: "user", Alias: "user"}})
}

func TestParseNeverNil(t *testing.T) {
	r := Parse("")
	if r == nil {
		t.Fatal("Parse must never return nil")
	}
	if r.Tables == nil {
		t.Fatal("Result.Tables must never be nil")
	}
	if r.AliasToTable == nil {
		t.Fatal("Result.AliasToTable must never be nil")
	}
	if r.Projections == nil {
		t.Fatal("Result.Projections must never be nil")
	}
}

// Benchmark for performance sanity check.
func BenchmarkParse(b *testing.B) {
	sql := "SELECT u.id, u.name, d.name AS dept_name, d.config AS dept_config FROM user u LEFT JOIN dept d ON u.dept_id = d.id WHERE u.active = 1 ORDER BY u.name LIMIT 100"
	for i := 0; i < b.N; i++ {
		Parse(sql)
	}
}

func TestProjectionEquality(t *testing.T) {
	a := Projection{TableAlias: "u", Column: "name", Label: "name"}
	b := Projection{TableAlias: "u", Column: "name", Label: "name"}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("identical projections should be equal")
	}
}

// Real-world example from the design doc.
func TestRealWorldJoin(t *testing.T) {
	r := Parse("SELECT u.*, d.name AS dept_name, d.config AS dept_config FROM user u JOIN dept d ON u.dept_id = d.id")
	assertTables(t, r, []TableRef{
		{Table: "user", Alias: "u"},
		{Table: "dept", Alias: "d"},
	})
	assertProjections(t, r, []Projection{
		{TableAlias: "u", Star: true, Column: "*"},
		{TableAlias: "d", Column: "name", Label: "dept_name"},
		{TableAlias: "d", Column: "config", Label: "dept_config"},
	})
	assertAliasToTable(t, r, map[string]string{
		"user": "user", "u": "user",
		"dept": "dept", "d": "dept",
	})
}

// ---- Helpers ----

func assertTables(t *testing.T, r *Result, expected []TableRef) {
	t.Helper()
	if len(r.Tables) != len(expected) {
		t.Fatalf("expected %d tables, got %d: %v", len(expected), len(r.Tables), r.Tables)
	}
	for i, got := range r.Tables {
		if got.Table != expected[i].Table || got.Alias != expected[i].Alias || got.FromSubquery != expected[i].FromSubquery {
			t.Fatalf("table[%d]: expected %+v, got %+v", i, expected[i], got)
		}
	}
}

func assertProjections(t *testing.T, r *Result, expected []Projection) {
	t.Helper()
	if len(expected) == 0 && len(r.Projections) == 0 {
		return
	}
	if len(r.Projections) != len(expected) {
		t.Fatalf("expected %d projections, got %d: %v", len(expected), len(r.Projections), r.Projections)
	}
	for i, got := range r.Projections {
		if got != expected[i] {
			t.Fatalf("projection[%d]: expected %+v, got %+v", i, expected[i], got)
		}
	}
}

func assertAliasToTable(t *testing.T, r *Result, expected map[string]string) {
	t.Helper()
	if len(r.AliasToTable) != len(expected) {
		t.Fatalf("expected AliasToTable %v, got %v", expected, r.AliasToTable)
	}
	for k, v := range expected {
		if got, ok := r.AliasToTable[k]; !ok || got != v {
			t.Fatalf("AliasToTable[%q]: expected %q, got %q", k, v, got)
		}
	}
}
