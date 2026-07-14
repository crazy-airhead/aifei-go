// Package sql provides Enjoy SQL template support and a lightweight SQL parser that extracts
// table references (FROM/JOIN) and SELECT projections from a rendered SQL string.
//
// It is designed for multi-table column-to-table mapping: given a SQL query,
// Parse returns which tables are involved (with aliases) and which SELECT
// columns map to which table aliases. The parser never returns an error — on
// unrecognizable constructs it gracefully degrades by skipping them.
//
// Zero external dependencies; pure string scanning with O(n) complexity.
package sql

// TableRef represents a table reference found in a SQL FROM/JOIN clause.
type TableRef struct {
	Table        string // real table name (unquoted, schema prefix stripped)
	Alias        string // alias; equals Table when no alias present
	FromSubquery bool   // derived table (SELECT ...) AS x — cannot resolve column ownership
}

// Projection represents one item in the SELECT projection list.
type Projection struct {
	TableAlias string // "u" in u.col; empty for bare column/expression
	Column     string // column name; "*" for wildcard
	Label      string // output column label: AS alias or column name; empty for wildcard *
	Star       bool   // true for alias.* or *
}

// Result holds the full parse output.
type Result struct {
	Tables       []TableRef        // FROM/JOIN table references, in appearance order
	AliasToTable map[string]string // alias → real table name (includes table→table self-reference)
	Projections  []Projection      // SELECT projection items
}

// token kinds
type tokenKind int

const (
	tokIdent tokenKind = iota
	tokDot
	tokComma
	tokStar
	tokLParen
	tokRParen
	tokKeyword
)

type token struct {
	kind  tokenKind
	value string // text for tokIdent and tokKeyword (unquoted for ident)
}

// clause boundary keywords that end FROM/JOIN collection.
var clauseBoundaries = map[string]bool{
	"WHERE": true, "GROUP": true, "ORDER": true, "HAVING": true,
	"LIMIT": true, "UNION": true, "EXCEPT": true, "INTERSECT": true, "RETURNING": true,
}

// SQL keywords recognized by the parser.
// Only structural keywords needed for FROM/JOIN/SELECT parsing are included.
// Function names (COUNT, MAX, etc.) are intentionally NOT keywords so they
// tokenize as identifiers and can participate in "expr AS label" patterns.
var sqlKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "JOIN": true, "INNER": true, "LEFT": true,
	"RIGHT": true, "FULL": true, "OUTER": true, "CROSS": true, "NATURAL": true,
	"ON": true, "USING": true, "AS": true,
	"WHERE": true, "GROUP": true, "ORDER": true, "HAVING": true, "LIMIT": true,
	"UNION": true, "EXCEPT": true, "INTERSECT": true, "RETURNING": true,
	"DISTINCT": true, "ALL": true, "NOT": true, "AND": true, "OR": true,
	"IN": true, "EXISTS": true, "BETWEEN": true, "LIKE": true, "IS": true,
	"NULL": true, "TRUE": true, "FALSE": true,
	"CASE": true, "WHEN": true, "THEN": true, "ELSE": true, "END": true,
	"ASC": true, "DESC": true, "BY": true, "SET": true, "INTO": true, "VALUES": true,
	"INSERT": true, "UPDATE": true, "DELETE": true, "CREATE": true, "DROP": true,
	"ALTER": true, "TABLE": true, "INDEX": true, "VIEW": true,
}

// Parse parses sql and returns the parse result.
// It never returns nil; on unrecognizable constructs it gracefully degrades.
func Parse(sql string) *Result {
	tokens := tokenize(sql)
	r := &Result{
		Tables:       []TableRef{},
		AliasToTable: map[string]string{},
		Projections:  []Projection{},
	}
	// Find SELECT and FROM positions, skipping parenthesized subqueries
	selectIdx := -1
	fromIdx := -1
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if t.kind == tokKeyword && t.value == "SELECT" && selectIdx < 0 {
			selectIdx = i
		}
		if t.kind == tokLParen {
			depth := 1
			for i = i + 1; i < len(tokens) && depth > 0; i++ {
				if tokens[i].kind == tokLParen {
					depth++
				} else if tokens[i].kind == tokRParen {
					depth--
				}
			}
			i-- // compensate: outer loop's i++ will advance past close paren
		}
		if t.kind == tokKeyword && t.value == "FROM" && selectIdx >= 0 && fromIdx < 0 {
			fromIdx = i
			break
		}
	}

	if selectIdx < 0 {
		return r
	}

	// Collect SELECT projections (between SELECT and FROM)
	if fromIdx < 0 {
		collectProjections(tokens, selectIdx+1, len(tokens), r)
		return r
	}
	collectProjections(tokens, selectIdx+1, fromIdx, r)

	// Collect FROM/JOIN tables
	collectFromTables(tokens, fromIdx+1, len(tokens), r)

	return r
}

// collectProjections parses the SELECT projection list between start (inclusive)
// and end (exclusive), splitting by top-level commas.
func collectProjections(tokens []token, start, end int, r *Result) {
	if start >= end {
		return
	}
	items := splitByComma(tokens, start, end)
	for _, item := range items {
		if len(item) == 0 {
			continue
		}
		p := parseProjection(item)
		if p != nil {
			r.Projections = append(r.Projections, *p)
		}
	}
}

// splitByComma splits tokens[start:end] by top-level commas (respecting parens).
func splitByComma(tokens []token, start, end int) [][]token {
	var items [][]token
	depth := 0
	itemStart := start
	for i := start; i < end; i++ {
		switch tokens[i].kind {
		case tokLParen:
			depth++
		case tokRParen:
			depth--
		case tokComma:
			if depth == 0 {
				items = append(items, tokens[itemStart:i])
				itemStart = i + 1
			}
		}
	}
	if itemStart < end {
		items = append(items, tokens[itemStart:end])
	}
	return items
}

// parseProjection parses a single projection item (tokens between commas).
// Handles: *, u.*, u.col, u.col AS label, col, col AS label, expr AS label.
func parseProjection(tokens []token) *Projection {
	if len(tokens) == 0 {
		return nil
	}

	// Find AS alias from the end (if any). This works for any expression form.
	asIdx := -1
	for i := len(tokens) - 1; i >= 0; i-- {
		if tokens[i].kind == tokKeyword && tokens[i].value == "AS" {
			asIdx = i
			break
		}
	}
	var alias string
	if asIdx >= 0 && asIdx+1 < len(tokens) && tokens[asIdx+1].kind == tokIdent {
		alias = tokens[asIdx+1].value
	}
	// Work with tokens before AS (the expression part)
	expr := tokens
	if asIdx > 0 {
		expr = tokens[:asIdx]
	} else if asIdx == 0 {
		// "AS label" at the start — no expression, just an alias
		return &Projection{Label: alias}
	}

	if len(expr) == 0 {
		return nil
	}

	// * alone
	if len(expr) == 1 && expr[0].kind == tokStar {
		return &Projection{Star: true, Column: "*", Label: alias}
	}

	// ident.* (e.g., u.*)
	if len(expr) == 3 && expr[0].kind == tokIdent && expr[1].kind == tokDot && expr[2].kind == tokStar {
		return &Projection{TableAlias: expr[0].value, Star: true, Column: "*", Label: alias}
	}

	// ident.ident [AS label] (e.g., u.col)
	if len(expr) >= 3 && expr[0].kind == tokIdent && expr[1].kind == tokDot && expr[2].kind == tokIdent {
		col := expr[2].value
		label := col
		if alias != "" {
			label = alias
		}
		return &Projection{TableAlias: expr[0].value, Column: col, Label: label}
	}

	// ident.ident.* with extra tokens — unlikely but handle gracefully
	if len(expr) >= 4 && expr[0].kind == tokIdent && expr[1].kind == tokDot && expr[2].kind == tokIdent && expr[3].kind == tokDot {
		// schema.table.col — skip schema, take table.col
		label := expr[2].value
		if alias != "" {
			label = alias
		}
		return &Projection{TableAlias: expr[2].value, Column: expr[2].value, Label: label}
	}

	// Single ident (bare column)
	if len(expr) == 1 && expr[0].kind == tokIdent {
		col := expr[0].value
		label := col
		if alias != "" {
			label = alias
		}
		return &Projection{Column: col, Label: label}
	}

	// Expression with AS alias (e.g., COUNT(*), 1+1)
	if alias != "" {
		return &Projection{Label: alias}
	}

	// Anything else without AS — skip (no useful label)
	return nil
}

// collectFromTables parses FROM/JOIN table references from tokens[start:end].
func collectFromTables(tokens []token, start, end int, r *Result) {
	i := start
	for i < end {
		t := tokens[i]

		// Stop at clause boundary
		if t.kind == tokKeyword && clauseBoundaries[t.value] {
			return
		}

		// Stop at unmatched right paren
		if t.kind == tokRParen {
			return
		}

		// Skip commas (implicit join continuation)
		if t.kind == tokComma {
			i++
			continue
		}

		// Handle JOIN variants: skip the JOIN keyword and modifiers
		if isJoinKeyword(t) {
			i++
			for i < end && isJoinModifier(tokens[i]) {
				i++
			}
			if i < end {
				ref, next := parseTableRef(tokens, i, end)
				if ref != nil {
					r.Tables = append(r.Tables, *ref)
					registerAlias(r, *ref)
				}
				i = next
			}
			// Skip ON/USING that may follow the JOIN table ref
			i = skipOnClause(tokens, i, end)
			continue
		}

		// Skip ON/USING (may appear after the initial FROM table ref too)
		if t.kind == tokKeyword && (t.value == "ON" || t.value == "USING") {
			i = skipOnClause(tokens, i+1, end)
			continue
		}

		// Handle derived table: (SELECT ...) [AS] alias
		if t.kind == tokLParen {
			depth := 1
			i++
			for i < end && depth > 0 {
				if tokens[i].kind == tokLParen {
					depth++
				} else if tokens[i].kind == tokRParen {
					depth--
				}
				i++
			}
			alias := ""
			if i < end && tokens[i].kind == tokKeyword && tokens[i].value == "AS" {
				i++
			}
			if i < end && tokens[i].kind == tokIdent {
				alias = tokens[i].value
				i++
			}
			if alias != "" {
				ref := TableRef{Table: alias, Alias: alias, FromSubquery: true}
				r.Tables = append(r.Tables, ref)
				registerAlias(r, ref)
			}
			continue
		}

		// Skip all other keywords (INNER, OUTER, DISTINCT, etc.)
		if t.kind == tokKeyword {
			i++
			continue
		}

		// Expect a table reference (identifier)
		if t.kind == tokIdent {
			ref, next := parseTableRef(tokens, i, end)
			if ref != nil {
				r.Tables = append(r.Tables, *ref)
				registerAlias(r, *ref)
			}
			i = next
			// Skip ON/USING that may follow the FROM table ref
			i = skipOnClause(tokens, i, end)
			continue
		}

		// Skip everything else (dots, stars, etc.)
		i++
	}
}

// skipOnClause skips over ON <condition> or USING (...) until the next JOIN,
// clause boundary, or end. It also skips trailing commas and join modifiers
// to properly position at the next table reference.
func skipOnClause(tokens []token, i, end int) int {
	for i < end {
		t := tokens[i]
		if t.kind == tokKeyword && (t.value == "ON" || t.value == "USING") {
			i++
			depth := 0
			for i < end {
				if tokens[i].kind == tokLParen {
					depth++
					i++
					continue
				}
				if tokens[i].kind == tokRParen {
					if depth > 0 {
						depth--
						i++
						continue
					}
					return i
				}
				if depth == 0 && isJoinKeyword(tokens[i]) {
					return i
				}
				if depth == 0 && tokens[i].kind == tokKeyword && clauseBoundaries[tokens[i].value] {
					return i
				}
				i++
			}
			return i
		}
		// Also skip commas and join modifiers between ON clause and next JOIN
		if t.kind == tokComma || isJoinModifier(t) {
			i++
			continue
		}
		break
	}
	return i
}

// parseTableRef parses a table reference starting at tokens[pos].
// Handles: ident, ident alias, ident AS alias, schema.ident alias.
func parseTableRef(tokens []token, pos, end int) (*TableRef, int) {
	if pos >= end || tokens[pos].kind != tokIdent {
		return nil, pos + 1
	}

	tableName := tokens[pos].value
	next := pos + 1

	// Check for schema.table (ident.ident)
	if next < end && tokens[next].kind == tokDot && next+1 < end && tokens[next+1].kind == tokIdent {
		tableName = tokens[next+1].value // take leaf name
		next += 2
	}

	alias := tableName // default alias = table name

	// Check for [AS] alias
	if next < end {
		if tokens[next].kind == tokKeyword && tokens[next].value == "AS" {
			next++
			if next < end && tokens[next].kind == tokIdent {
				alias = tokens[next].value
				next++
			}
		} else if next < end && tokens[next].kind == tokIdent && !isKeyword(tokens[next].value) {
			// Implicit alias: next ident that is not a keyword
			alias = tokens[next].value
			next++
		}
	}

	return &TableRef{Table: tableName, Alias: alias}, next
}

// registerAlias adds table→table and alias→table entries to AliasToTable.
func registerAlias(r *Result, ref TableRef) {
	r.AliasToTable[ref.Table] = ref.Table
	if ref.Alias != ref.Table {
		r.AliasToTable[ref.Alias] = ref.Table
	}
}

func isJoinKeyword(t token) bool {
	return t.kind == tokKeyword && t.value == "JOIN"
}

func isJoinModifier(t token) bool {
	return t.kind == tokKeyword && (t.value == "INNER" || t.value == "LEFT" ||
		t.value == "RIGHT" || t.value == "FULL" || t.value == "OUTER" ||
		t.value == "CROSS" || t.value == "NATURAL")
}

func isKeyword(value string) bool {
	return sqlKeywords[value]
}

// tokenize scans sql and produces a token stream.
func tokenize(sql string) []token {
	var tokens []token
	i := 0
	n := len(sql)

	for i < n {
		c := sql[i]

		// Skip whitespace
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			i++
			continue
		}

		// Single-line comment: --
		if c == '-' && i+1 < n && sql[i+1] == '-' {
			i += 2
			for i < n && sql[i] != '\n' {
				i++
			}
			continue
		}

		// Block comment: /* ... */
		if c == '/' && i+1 < n && sql[i+1] == '*' {
			i += 2
			for i < n-1 && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}

		// Quoted identifier: backtick
		if c == '`' {
			i++
			start := i
			for i < n && sql[i] != '`' {
				i++
			}
			val := sql[start:i]
			if i < n {
				i++
			}
			tokens = append(tokens, token{kind: tokIdent, value: val})
			continue
		}

		// Quoted identifier: double quote
		if c == '"' {
			i++
			start := i
			for i < n && sql[i] != '"' {
				i++
			}
			val := sql[start:i]
			if i < n {
				i++
			}
			tokens = append(tokens, token{kind: tokIdent, value: val})
			continue
		}

		// Quoted identifier: square bracket
		if c == '[' {
			i++
			start := i
			for i < n && sql[i] != ']' {
				i++
			}
			val := sql[start:i]
			if i < n {
				i++
			}
			tokens = append(tokens, token{kind: tokIdent, value: val})
			continue
		}

		// String literal: skip
		if c == '\'' {
			i++
			for i < n && sql[i] != '\'' {
				if i+1 < n && sql[i+1] == '\'' {
					i++
				}
				i++
			}
			if i < n {
				i++
			}
			continue
		}

		// Punctuation
		switch c {
		case '.':
			tokens = append(tokens, token{kind: tokDot})
			i++
			continue
		case ',':
			tokens = append(tokens, token{kind: tokComma})
			i++
			continue
		case '*':
			tokens = append(tokens, token{kind: tokStar})
			i++
			continue
		case '(':
			tokens = append(tokens, token{kind: tokLParen})
			i++
			continue
		case ')':
			tokens = append(tokens, token{kind: tokRParen})
			i++
			continue
		}

		// Identifier or keyword
		if isIdentStart(c) {
			start := i
			for i < n && isIdentPart(sql[i]) {
				i++
			}
			val := sql[start:i]
			upper := toUpper(val)
			if sqlKeywords[upper] {
				tokens = append(tokens, token{kind: tokKeyword, value: upper})
			} else {
				tokens = append(tokens, token{kind: tokIdent, value: val})
			}
			continue
		}

		// Skip unknown characters (=, ;, numbers, operators, etc.)
		i++
	}

	return tokens
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func toUpper(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		} else {
			b[i] = c
		}
	}
	return string(b)
}
