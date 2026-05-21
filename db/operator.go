package db

// Operator is a SQL comparison operator.
type Operator string

const (
	OpEqual        Operator = "="
	OpNotEqual     Operator = "!="
	OpGreater      Operator = ">"
	OpGreaterEqual Operator = ">="
	OpLess         Operator = "<"
	OpLessEqual    Operator = "<="
	OpIn           Operator = "IN"
	OpNotIn        Operator = "NOT IN"
	OpBetween      Operator = "BETWEEN"
	OpNotBetween   Operator = "NOT BETWEEN"
	OpIsNull       Operator = "IS NULL"
	OpIsNotNull    Operator = "IS NOT NULL"
	OpLike         Operator = "LIKE"
	OpNotLike      Operator = "NOT LIKE"
)

// LikeContains returns "%value%" for LIKE queries.
func LikeContains(value string) string { return "%" + value + "%" }

// LikeStartsWith returns "value%" for LIKE queries.
func LikeStartsWith(value string) string { return value + "%" }

// LikeEndsWith returns "%value" for LIKE queries.
func LikeEndsWith(value string) string { return "%" + value }
