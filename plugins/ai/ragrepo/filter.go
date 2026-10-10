// Package ragrepo 向量仓储后端（对标 solon-ai-rag-repositorys 的 Go 首批样板：
// es + pgvector；契约 docs/arch/ai/05-rag-design.md §6）。共享件：过滤表达式
// 的 enjoy 子集解析器（==/!=/>/<>=/<=/&&/||/!/裸字段/裸布尔），后端各自把节点
// 树译为原生过滤（ES bool query / SQL WHERE）；不能翻译的形态由使用方回退
// rag.Refilter 内存过滤（Java disableRefilter=false 默认行为的对应物）。
package ragrepo

import (
	"crypto/rand"
	"fmt"
	"strconv"
	"strings"
)

// FilterNode 过滤表达式节点树。
type FilterNode interface{ filterNode() }

// FieldNode 裸字段（真值判定：ES exists；SQL 不可译）。
type FieldNode struct{ Name string }

// BoolNode 裸布尔（true → 全过；false → 全拒）。
type BoolNode struct{ Value bool }

// CmpNode 比较：field op literal（op: == != > >= < <=）。
type CmpNode struct {
	Field string
	Op    string
	Value any // string / float64 / bool
}

// NotNode 逻辑非。
type NotNode struct{ Inner FilterNode }

// AndNode 逻辑与。
type AndNode struct{ Left, Right FilterNode }

// OrNode 逻辑或。
type OrNode struct{ Left, Right FilterNode }

func (*FieldNode) filterNode() {}
func (*BoolNode) filterNode()  {}
func (*CmpNode) filterNode()   {}
func (*NotNode) filterNode()   {}
func (*AndNode) filterNode()   {}
func (*OrNode) filterNode()    {}

// ParseFilterExpr 过滤表达式解析（enjoy 判定子集）。不支持（方法调用
// contains/startsWith、in 集合、括号外语法错误等）返回 ok=false——调用方回退
// 内存过滤。
func ParseFilterExpr(expr string) (FilterNode, bool) {
	p := &filterParser{lex: lexFilter(expr)}
	node, ok := p.parseOr()
	if !ok || p.pos != len(p.lex) || p.err {
		return nil, false
	}
	return node, true
}

type filterToken struct {
	kind string // "field" "str" "num" "bool" "op" "lp" "rp" "and" "or" "not"
	text string
	val  any
}

func lexFilter(expr string) []filterToken {
	var out []filterToken
	s := strings.TrimSpace(expr)
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			out = append(out, filterToken{kind: "lp"})
			i++
		case c == ')':
			out = append(out, filterToken{kind: "rp"})
			i++
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(s) && s[j] != c {
				j++
			}
			if j >= len(s) {
				return nil // 未闭合
			}
			out = append(out, filterToken{kind: "str", val: s[i+1 : j]})
			i = j + 1
		case strings.HasPrefix(s[i:], "&&"):
			out = append(out, filterToken{kind: "and"})
			i += 2
		case strings.HasPrefix(s[i:], "||"):
			out = append(out, filterToken{kind: "or"})
			i += 2
		case strings.HasPrefix(s[i:], "=="), strings.HasPrefix(s[i:], "!="),
			strings.HasPrefix(s[i:], ">="), strings.HasPrefix(s[i:], "<="):
			out = append(out, filterToken{kind: "op", text: s[i : i+2]})
			i += 2
		case c == '!':
			out = append(out, filterToken{kind: "not"})
			i++
		case c == '>' || c == '<':
			out = append(out, filterToken{kind: "op", text: string(c)})
			i++
		case isIdentChar(c, true), c >= '0' && c <= '9':
			// 数字开头也走词元（裸数值字面量）
			j := i
			for j < len(s) && (isIdentChar(s[j], false) || s[j] == '.') {
				j++
			}
			word := s[i:j]
			switch word {
			case "true", "false":
				out = append(out, filterToken{kind: "bool", val: word == "true"})
			default:
				if n, err := strconv.ParseFloat(word, 64); err == nil {
					out = append(out, filterToken{kind: "num", val: n})
				} else {
					out = append(out, filterToken{kind: "field", text: word})
				}
			}
			i = j
		default:
			return nil // 未识别字符（方法调用 '(' 紧跟字段等）
		}
	}
	return out
}

func isIdentChar(c byte, first bool) bool {
	if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' {
		return true
	}
	return !first && c >= '0' && c <= '9'
}

type filterParser struct {
	lex []filterToken
	pos int
	err bool
}

func (p *filterParser) peek() *filterToken {
	if p.pos < len(p.lex) {
		return &p.lex[p.pos]
	}
	return nil
}

func (p *filterParser) next() *filterToken {
	t := p.peek()
	if t != nil {
		p.pos++
	}
	return t
}

// parseOr or 层（最低优先级）。
func (p *filterParser) parseOr() (FilterNode, bool) {
	left, ok := p.parseAnd()
	if !ok {
		return nil, false
	}
	for {
		t := p.peek()
		if t == nil || t.kind != "or" {
			return left, true
		}
		p.next()
		right, ok := p.parseAnd()
		if !ok {
			return nil, false
		}
		left = &OrNode{Left: left, Right: right}
	}
}

// parseAnd and 层。
func (p *filterParser) parseAnd() (FilterNode, bool) {
	left, ok := p.parseUnary()
	if !ok {
		return nil, false
	}
	for {
		t := p.peek()
		if t == nil || t.kind != "and" {
			return left, true
		}
		p.next()
		right, ok := p.parseUnary()
		if !ok {
			return nil, false
		}
		left = &AndNode{Left: left, Right: right}
	}
}

// parseUnary not 层。
func (p *filterParser) parseUnary() (FilterNode, bool) {
	if t := p.peek(); t != nil && t.kind == "not" {
		p.next()
		inner, ok := p.parseUnary()
		if !ok {
			return nil, false
		}
		return &NotNode{Inner: inner}, true
	}
	return p.parsePrimary()
}

// parsePrimary 原子层：括号 / 比较 / 裸字段 / 裸布尔。
func (p *filterParser) parsePrimary() (FilterNode, bool) {
	t := p.next()
	if t == nil {
		return nil, false
	}
	switch t.kind {
	case "lp":
		inner, ok := p.parseOr()
		if !ok {
			return nil, false
		}
		rp := p.next()
		if rp == nil || rp.kind != "rp" {
			return nil, false
		}
		return inner, true
	case "bool":
		return &BoolNode{Value: t.val.(bool)}, true
	case "field":
		// 裸字段（真值）或比较左值
		if op := p.peek(); op != nil && op.kind == "op" {
			p.next()
			v := p.next()
			if v == nil {
				return nil, false
			}
			switch v.kind {
			case "str", "num", "bool":
				return &CmpNode{Field: t.text, Op: op.text, Value: v.val}, true
			case "field": // 右侧字段名当字符串字面量（SnEL 宽松面：未加引号的词）
				return &CmpNode{Field: t.text, Op: op.text, Value: v.text}, true
			}
			return nil, false
		}
		return &FieldNode{Name: t.text}, true
	}
	return nil, false
}

// ---------------- ES 翻译 ----------------

// ToESQuery 节点树 → ES query DSL（对标 elasticsearch/FilterTransformer）。
// 字段名直接用作顶层字段（保存时 metadata 平铺顶层）。不可译（裸字段真值
// 以外的形态不存在于此树——裸字段译 exists）返回 nil。
func ToESQuery(n FilterNode) map[string]any {
	switch node := n.(type) {
	case *FieldNode:
		return map[string]any{"exists": map[string]any{"field": node.Name}}
	case *BoolNode:
		if node.Value {
			return map[string]any{"match_all": map[string]any{}}
		}
		return map[string]any{"bool": map[string]any{"must_not": []any{map[string]any{"match_all": map[string]any{}}}}}
	case *CmpNode:
		switch node.Op {
		case "==":
			return esTerm(node.Field, node.Value)
		case "!=":
			return map[string]any{"bool": map[string]any{"must_not": []any{esTerm(node.Field, node.Value)}}}
		case ">", ">=", "<", "<=":
			return map[string]any{"range": map[string]any{node.Field: map[string]any{esRangeOp(node.Op): node.Value}}}
		}
	case *NotNode:
		if inner := ToESQuery(node.Inner); inner != nil {
			return map[string]any{"bool": map[string]any{"must_not": []any{inner}}}
		}
	case *AndNode:
		l, r := ToESQuery(node.Left), ToESQuery(node.Right)
		if l != nil && r != nil {
			return map[string]any{"bool": map[string]any{"must": []any{l, r}}}
		}
	case *OrNode:
		l, r := ToESQuery(node.Left), ToESQuery(node.Right)
		if l != nil && r != nil {
			return map[string]any{"bool": map[string]any{"should": []any{l, r}, "minimum_should_match": 1}}
		}
	}
	return nil
}

func esTerm(field string, value any) map[string]any {
	return map[string]any{"term": map[string]any{field: value}}
}

func esRangeOp(op string) string {
	switch op {
	case ">":
		return "gt"
	case ">=":
		return "gte"
	case "<":
		return "lt"
	default:
		return "lte"
	}
}

// ---------------- SQL 翻译（pgvector）----------------

// ToSQLWhere 节点树 → SQL WHERE 片段 + 参数（对标 pgvector/FilterTransformer，
// 值一律参数化——Java 内联字符串的注入面在 Go 收口）。fieldCol 把表达式字段
// 映射为 SQL 列引用（顶层元数据列或 metadata JSON 提取式）；裸字段真值与
// 布尔字段比较不可译返回 ok=false。
func ToSQLWhere(n FilterNode, fieldCol func(name string) (string, bool)) (string, []any, bool) {
	var args []any
	clause, ok := toSQLWhere(n, fieldCol, &args)
	return clause, args, ok
}

func toSQLWhere(n FilterNode, fieldCol func(string) (string, bool), args *[]any) (string, bool) {
	switch node := n.(type) {
	case *BoolNode:
		if node.Value {
			return "1=1", true
		}
		return "1=0", true
	case *CmpNode:
		col, ok := fieldCol(node.Field)
		if !ok {
			return "", false
		}
		op := node.Op
		switch op {
		case "==":
			op = "="
		case "!=":
			op = "<>"
		}
		*args = append(*args, node.Value)
		return fmt.Sprintf("%s %s ?", col, op), true
	case *NotNode:
		inner, ok := toSQLWhere(node.Inner, fieldCol, args)
		if !ok {
			return "", false
		}
		return "NOT (" + inner + ")", true
	case *AndNode:
		l, ok1 := toSQLWhere(node.Left, fieldCol, args)
		r, ok2 := toSQLWhere(node.Right, fieldCol, args)
		if !ok1 || !ok2 {
			return "", false
		}
		return "(" + l + " AND " + r + ")", true
	case *OrNode:
		l, ok1 := toSQLWhere(node.Left, fieldCol, args)
		r, ok2 := toSQLWhere(node.Right, fieldCol, args)
		if !ok1 || !ok2 {
			return "", false
		}
		return "(" + l + " OR " + r + ")", true
	}
	return "", false // FieldNode：JSON 真值不可译
}

// NewUUID UUIDv4 形态（仓储公共件；对标 Java Utils.uuid——crypto/rand 无三方依赖）。
func NewUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexDigits[v>>4], hexDigits[v&0x0f])
	}
	return string(out)
}

// FieldTypeX SQL 侧元数据列类型（mysql/mariadb 模块的三值面）。
type FieldTypeX string

const (
	FieldTextX    FieldTypeX = "TEXT"
	FieldNumericX FieldTypeX = "NUMERIC"
	FieldJSONX    FieldTypeX = "JSON"
)

// MetadataFieldX SQL 侧元数据列。
type MetadataFieldX struct {
	Name string
	Type FieldTypeX
}

// VectorLiteral 向量的 SQL 字面量（'[0.1,0.2]'::vector 的参数形态）。
func VectorLiteral(vec []float32) string {
	parts := make([]string, len(vec))
	for i, v := range vec {
		parts[i] = strconv.FormatFloat(float64(v), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
