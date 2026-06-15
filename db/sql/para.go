package sql

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

// SetSql sets the SQL string.
func (s *SqlPara) SetSql(sql string) *SqlPara {
	s.Sql = sql
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
