package sql

import "regexp"

// blankLineRe matches lines that are empty or contain only whitespace.
var blankLineRe = regexp.MustCompile(`\n\s*\n`)

// SqlPara holds a SQL statement and its parameters.
type SqlPara struct {
	ID    string
	Sql   string
	Paras []interface{}
	Enjoy bool
}

// NewSqlPara creates a SqlPara.
func NewSqlPara() *SqlPara {
	return &SqlPara{}
}

// SetID sets the ID.
func (s *SqlPara) SetID(id string) *SqlPara {
	s.ID = id
	return s
}

// SetSql sets the SQL string, collapsing consecutive blank lines.
func (s *SqlPara) SetSql(sql string) *SqlPara {
	s.Sql = blankLineRe.ReplaceAllString(sql, "\n")
	return s
}

// SetEnjoySql marks this as Enjoy SQL.
func (s *SqlPara) SetEnjoySql(enjoy bool) *SqlPara {
	s.Enjoy = enjoy
	return s
}

// AddPara adds a parameter.
func (s *SqlPara) AddPara(para interface{}) {
	s.Paras = append(s.Paras, para)
}

// ClearParas clears all parameters.
func (s *SqlPara) ClearParas() {
	s.Paras = nil
}
