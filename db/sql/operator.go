package sql

import "fmt"

// LikeMode determines how LIKE values are wrapped.
type LikeMode int

const (
	LikeNone  LikeMode = iota
	LikeBoth           // %value%
	LikeLeft           // %value
	LikeRight          // value%
)

// SqlOperator is a SQL operator with LIKE mode support.
type SqlOperator struct {
	key      string
	sql      string
	likeMode LikeMode
}

var sqlOperators = map[string]*SqlOperator{}

func init() {
	for _, op := range []*SqlOperator{
		{key: "=", sql: "="},
		{key: "!=", sql: "!="},
		{key: "<>", sql: "!="},
		{key: ">", sql: ">"},
		{key: ">=", sql: ">="},
		{key: "<", sql: "<"},
		{key: "<=", sql: "<="},
		{key: "in", sql: "IN"},
		{key: "IN", sql: "IN"},
		{key: "not in", sql: "NOT IN"},
		{key: "NOT IN", sql: "NOT IN"},
		{key: "between", sql: "BETWEEN"},
		{key: "BETWEEN", sql: "BETWEEN"},
		{key: "not between", sql: "NOT BETWEEN"},
		{key: "NOT BETWEEN", sql: "NOT BETWEEN"},
		{key: "is null", sql: "IS NULL"},
		{key: "IS NULL", sql: "IS NULL"},
		{key: "is not null", sql: "IS NOT NULL"},
		{key: "IS NOT NULL", sql: "IS NOT NULL"},
		{key: "like", sql: "LIKE", likeMode: LikeBoth},
		{key: "LIKE", sql: "LIKE", likeMode: LikeBoth},
		{key: "not like", sql: "NOT LIKE", likeMode: LikeBoth},
		{key: "NOT LIKE", sql: "NOT LIKE", likeMode: LikeBoth},
		{key: "contains", sql: "LIKE", likeMode: LikeBoth},
		{key: "notContains", sql: "NOT LIKE", likeMode: LikeBoth},
		{key: "startsWith", sql: "LIKE", likeMode: LikeRight},
		{key: "endsWith", sql: "LIKE", likeMode: LikeLeft},
	} {
		sqlOperators[op.key] = op
	}
}

// SqlOperatorFrom returns the SqlOperator for the given key, or nil.
func SqlOperatorFrom(key string) *SqlOperator {
	return sqlOperators[key]
}

// SQL returns the SQL representation.
func (o *SqlOperator) SQL() string { return o.sql }

// ToLikeValue wraps the value according to LikeMode.
func (o *SqlOperator) ToLikeValue(value interface{}) interface{} {
	if o.likeMode == LikeNone {
		return value
	}
	s := fmt.Sprintf("%v", value)
	switch o.likeMode {
	case LikeBoth:
		return "%" + s + "%"
	case LikeLeft:
		return "%" + s
	case LikeRight:
		return s + "%"
	}
	return value
}

// IsNilOp returns true if this is IS NULL or IS NOT NULL.
func (o *SqlOperator) IsNilOp() bool {
	return o.sql == "IS NULL" || o.sql == "IS NOT NULL"
}

// LikeOp returns true if this is a LIKE-related operator.
func (o *SqlOperator) LikeOp() bool {
	return o.likeMode != LikeNone && (o.sql == "LIKE" || o.sql == "NOT LIKE")
}

// IsInOp returns true if this is IN or NOT IN.
func (o *SqlOperator) IsInOp() bool {
	return o.sql == "IN" || o.sql == "NOT IN"
}

// IsBetweenOp returns true if this is BETWEEN or NOT BETWEEN.
func (o *SqlOperator) IsBetweenOp() bool {
	return o.sql == "BETWEEN" || o.sql == "NOT BETWEEN"
}
