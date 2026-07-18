package sql

import (
	"fmt"
	"sync"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// SqlKit wraps an Enjoy engine with SQL directives for Enjoy SQL template support.
type SqlKit struct {
	engine *enjoy.Engine
	cache  sync.Map // string → *enjoy.Template
}

// NewSqlKit creates a SqlKit with SQL directives registered.
func NewSqlKit(name string) *SqlKit {
	engine := enjoy.NewEngine(name)

	engine.AddDirective("sql", func() enjoy.Directive { return &SqlDirective{} })
	engine.AddDirective("where", func() enjoy.Directive { return &WhereDirective{} })
	engine.AddDirective("and", func() enjoy.Directive { return &AndDirective{} })
	engine.AddDirective("or", func() enjoy.Directive { return &OrDirective{} })
	engine.AddDirective("orderBy", func() enjoy.Directive { return &OrderByDirective{} })
	engine.AddDirective("para", func() enjoy.Directive { return &ParaDirective{} })
	engine.AddDirective("p", func() enjoy.Directive { return &ParaDirective{} })

	return &SqlKit{engine: engine}
}

// Engine returns the underlying Enjoy engine.
func (sk *SqlKit) Engine() *enjoy.Engine {
	return sk.engine
}

// renderSQL 渲染 SQL 模板，出错即 panic。
//
// SQL 模板渲染错误本质是静态代码 bug（模板写错），与 AddSql 对空 id 的 panic 一致；
// Dao 的 Sql()/SqlById() 是链式 builder（返回 *Dao 不带 error），panic 不破坏 fluent API。
func renderSQL(tpl *enjoy.Template, data map[string]interface{}) string {
	out, err := tpl.RenderToString(data)
	if err != nil {
		panic(fmt.Sprintf("render sql template: %v", err))
	}
	return out
}

// GetSqlPara gets a SqlPara from a SQL template string with named parameters.
//
//	SqlPara sp = sqlKit.GetSqlPara("select * from user where id = #para(id)", map[string]interface{}{"id": 123})
func (sk *SqlKit) GetSqlPara(sql string, data map[string]interface{}) *SqlPara {
	tpl := sk.engine.GetTemplateByString(sql)
	sp := NewSqlPara()

	// 需要放置参数，为空的时候，初始化一个
	if data == nil {
		data = map[string]interface{}{}
	}

	data[SqlParaKey] = sp
	sp.SetSql(renderSQL(tpl, data))
	delete(data, SqlParaKey)
	return sp
}

// GetSqlParaWithArgs gets a SqlPara from a SQL template string with positional arguments.
//
//	SqlPara sp = sqlKit.GetSqlParaWithArgs("select * from user where id = #para(0)", 123)
func (sk *SqlKit) GetSqlParaWithArgs(sql string, args ...interface{}) *SqlPara {
	tpl := sk.engine.GetTemplateByString(sql)
	sp := NewSqlPara()

	data := map[string]interface{}{
		SqlParaKey:   sp,
		ParaArrayKey: args,
	}
	sp.SetSql(renderSQL(tpl, data))
	return sp
}

// GetSqlParaByID gets a cached SQL template by ID with named parameters.
func (sk *SqlKit) GetSqlParaByID(sqlID string, data map[string]interface{}) *SqlPara {
	tpl := sk.getSqlTemplate(sqlID)
	if tpl == nil {
		return nil
	}

	sp := NewSqlPara().SetID(sqlID)
	data[SqlParaKey] = sp
	sp.SetSql(renderSQL(tpl, data))
	delete(data, SqlParaKey)
	return sp
}

// GetSqlParaByIDWithArgs gets a cached SQL template by ID with positional arguments.
func (sk *SqlKit) GetSqlParaByIDWithArgs(sqlID string, args ...interface{}) *SqlPara {
	tpl := sk.getSqlTemplate(sqlID)
	if tpl == nil {
		return nil
	}

	sp := NewSqlPara().SetID(sqlID)
	data := map[string]interface{}{
		SqlParaKey:   sp,
		ParaArrayKey: args,
	}
	sp.SetSql(renderSQL(tpl, data))
	return sp
}

// AddSql adds a named SQL template string.
func (sk *SqlKit) AddSql(sqlID, sql string) {
	if sqlID == "" {
		panic("sqlID can not be blank")
	}
	if sql == "" {
		panic("sql can not be blank")
	}

	// Trigger parsing and caching
	sk.addSqlTemplate(sqlID, sql)
}

// addSqlTemplate compiles and caches a SQL template.
func (sk *SqlKit) addSqlTemplate(sqlID string, sql string) {
	tpl := sk.engine.GetTemplateByString(sql)
	sqlCache := map[string]*enjoy.Template{}
	data := map[string]interface{}{
		SqlCacheKey: sqlCache,
	}
	renderSQL(tpl, data)

	for id, t := range sqlCache {
		sk.cache.Store(id, t)
	}
}

// getSqlTemplate retrieves a cached SQL template by ID.
func (sk *SqlKit) getSqlTemplate(sqlID string) *enjoy.Template {
	if v, ok := sk.cache.Load(sqlID); ok {
		return v.(*enjoy.Template)
	}
	return nil
}

// GetSql returns the raw SQL string for a template ID.
func (sk *SqlKit) GetSql(sqlID string, data map[string]interface{}) string {
	tpl := sk.getSqlTemplate(sqlID)
	if tpl == nil {
		return ""
	}
	return renderSQL(tpl, data)
}

// ParseSqlFile parses all SQL templates and populates the cache.
// Call this after adding SQL templates via AddSql.
func (sk *SqlKit) ParseSqlFile() {
	// Already parsed inline via AddSql / addSqlTemplate
}

// String returns a description.
func (sk *SqlKit) String() string {
	return fmt.Sprintf("SqlKit: %d cached templates", sk.cacheSize())
}

func (sk *SqlKit) cacheSize() int {
	count := 0
	sk.cache.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}
