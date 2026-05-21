package db

import (
	"fmt"
	"strings"
)

// SQLBuilder provides chainable SQL condition building.
type SQLBuilder struct {
	config      *Config
	selectPart  string
	fromPart    string
	whereParts  []string
	whereArgs   []interface{}
	joinParts   []string
	groupByPart string
	havingPart  string
	havingArgs  []interface{}
	orderByPart string
	limitVal    int
	offsetVal   int
}

// NewSQL creates a SQLBuilder from an existing SQL statement.
func NewSQL(sql string, args ...interface{}) *SQLBuilder {
	return &SQLBuilder{
		config:     GetConfig(),
		selectPart: sql,
		whereArgs:  args,
	}
}

// Where adds a WHERE condition.
func (b *SQLBuilder) Where(condition string, args ...interface{}) *SQLBuilder {
	b.whereParts = append(b.whereParts, condition)
	b.whereArgs = append(b.whereArgs, args...)
	return b
}

// WhereIf adds a WHERE condition only if apply is true.
func (b *SQLBuilder) WhereIf(condition string, arg interface{}, apply bool) *SQLBuilder {
	if apply {
		b.whereParts = append(b.whereParts, condition)
		b.whereArgs = append(b.whereArgs, arg)
	}
	return b
}

// And adds an AND condition (alias for Where).
func (b *SQLBuilder) And(condition string, args ...interface{}) *SQLBuilder {
	return b.Where(condition, args...)
}

// AndIf adds an AND condition only if apply is true.
func (b *SQLBuilder) AndIf(condition string, arg interface{}, apply bool) *SQLBuilder {
	return b.WhereIf(condition, arg, apply)
}

// OrderBy sets ORDER BY.
func (b *SQLBuilder) OrderBy(order string) *SQLBuilder {
	b.orderByPart = order
	return b
}

// Limit sets LIMIT.
func (b *SQLBuilder) Limit(limit int) *SQLBuilder {
	b.limitVal = limit
	return b
}

// Offset sets OFFSET.
func (b *SQLBuilder) Offset(offset int) *SQLBuilder {
	b.offsetVal = offset
	return b
}

// GroupBy sets GROUP BY.
func (b *SQLBuilder) GroupBy(group string) *SQLBuilder {
	b.groupByPart = group
	return b
}

// Having sets HAVING.
func (b *SQLBuilder) Having(condition string, args ...interface{}) *SQLBuilder {
	b.havingPart = condition
	b.havingArgs = args
	return b
}

// Join adds INNER JOIN.
func (b *SQLBuilder) Join(join string) *SQLBuilder {
	b.joinParts = append(b.joinParts, "INNER JOIN "+join)
	return b
}

// LeftJoin adds LEFT JOIN.
func (b *SQLBuilder) LeftJoin(join string) *SQLBuilder {
	b.joinParts = append(b.joinParts, "LEFT JOIN "+join)
	return b
}

// RightJoin adds RIGHT JOIN.
func (b *SQLBuilder) RightJoin(join string) *SQLBuilder {
	b.joinParts = append(b.joinParts, "RIGHT JOIN "+join)
	return b
}

// Build constructs the final SQL and args.
func (b *SQLBuilder) Build() (string, []interface{}) {
	var buf strings.Builder
	buf.WriteString(b.selectPart)

	for _, j := range b.joinParts {
		buf.WriteString(" ")
		buf.WriteString(j)
	}

	if len(b.whereParts) > 0 {
		buf.WriteString(" WHERE ")
		buf.WriteString(strings.Join(b.whereParts, " AND "))
	}

	if b.groupByPart != "" {
		buf.WriteString(" GROUP BY ")
		buf.WriteString(b.groupByPart)
	}

	if b.havingPart != "" {
		buf.WriteString(" HAVING ")
		buf.WriteString(b.havingPart)
	}

	if b.orderByPart != "" {
		buf.WriteString(" ORDER BY ")
		buf.WriteString(b.orderByPart)
	}

	if b.limitVal > 0 {
		buf.WriteString(fmt.Sprintf(" LIMIT %d", b.limitVal))
	}

	if b.offsetVal > 0 {
		buf.WriteString(fmt.Sprintf(" OFFSET %d", b.offsetVal))
	}

	return buf.String(), b.whereArgs
}

// Find executes the built query and returns rows.
func (b *SQLBuilder) Find() ([]*Row, error) {
	sql, args := b.Build()
	return SQL(sql, args...).Find()
}

// FindFirst executes the built query and returns the first row.
func (b *SQLBuilder) FindFirst() (*Row, error) {
	sql, args := b.Build()
	return SQL(sql, args...).FindFirst()
}

// Paginate executes a paginated query.
func (b *SQLBuilder) Paginate(pageNum, pageSize int) (*Page, error) {
	sql, args := b.Build()
	return SQL(sql, args...).Paginate(pageNum, pageSize)
}

// Count executes a COUNT query.
func (b *SQLBuilder) Count() (int64, error) {
	sql, _ := b.Build()
	countSQL := extractCountSQL(sql)
	pool, err := b.config.Pool()
	if err != nil {
		return 0, err
	}
	var count int64
	err = pool.QueryRow(countSQL, b.whereArgs...).Scan(&count)
	return count, err
}
