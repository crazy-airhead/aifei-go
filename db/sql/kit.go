package sql

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/crazy-airhead/aifei-go/enjoy"
)

// SqlKit wraps an Enjoy engine with SQL directives for Enjoy SQL template support.
type SqlKit struct {
	engine *enjoy.Engine
	cache  sync.Map // sqlID → *enjoy.Template（内联 AddSql 与文件来源共用）

	// 外部 sql 文件支持（对照 Java SqlKit.sqlFileList / sqlFromSqlFile）：
	//   sqlFileList    —— AddSqlFile/AddSqlDir 登记的文件路径，ParseSqlFile 逐个解析。
	//   sqlFromSqlFile —— 文件来源的 sqlID → body Template；热重载 reload 时据此精准移除 cache
	//                     （保留 AddSql / Db.sql 内联缓存不被清，对照 Java sqlFromSqlFile 注释 1）。
	//   fileTemplates  —— 解析路径 → 文件 Template，用于 isModified 判定（#sql 的 body Template
	//                     由 NewTemplate 创建、source 为 nil、IsModified 恒 false，故改用文件
	//                     Template 的 IsModified 判定底层文件变更）。
	//   fileMu 保护上述三者的并发读写（devMode 下 getSqlTemplate 会读 fileTemplates 触发热重载）。
	fileMu         sync.RWMutex
	sqlFileList    []string
	sqlFromSqlFile map[string]*enjoy.Template
	fileTemplates  map[string]*enjoy.Template
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

	return &SqlKit{
		engine:         engine,
		sqlFromSqlFile: map[string]*enjoy.Template{},
		fileTemplates:  map[string]*enjoy.Template{},
	}
}

// Engine returns the underlying Enjoy engine.
func (sk *SqlKit) Engine() *enjoy.Engine {
	return sk.engine
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
	sp.SetSql(tpl.RenderToString(data))
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
	sp.SetSql(tpl.RenderToString(data))
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
	sp.SetSql(tpl.RenderToString(data))
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
	sp.SetSql(tpl.RenderToString(data))
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
	tpl.RenderToString(data)

	for id, t := range sqlCache {
		sk.cache.Store(id, t)
	}
}

// getSqlTemplate retrieves a cached SQL template by ID.
//
// devMode（SetSqlFileHotReloading(true)）下，检测到任一外部 sql 文件被修改则自动 reload
// （对照 Java SqlKit.getSqlTemplate 的热重载分支）：精准移除文件来源的 sql 并重新解析，
// 不影响 AddSql / Db.sql 内联缓存。
func (sk *SqlKit) getSqlTemplate(sqlID string) *enjoy.Template {
	if v, ok := sk.cache.Load(sqlID); ok {
		tpl := v.(*enjoy.Template)
		if sk.engine.GetConfig().IsDevMode() && sk.isSqlTemplateModified() {
			sk.reloadModifiedSqlTemplate()
			if v, ok := sk.cache.Load(sqlID); ok {
				return v.(*enjoy.Template)
			}
			return nil
		}
		return tpl
	}

	// cache miss：非 devMode 直接返回 nil；devMode 下可能外部文件刚加入或内容变更，尝试 reload。
	if !sk.engine.GetConfig().IsDevMode() {
		return nil
	}
	if sk.isSqlTemplateModified() {
		sk.reloadModifiedSqlTemplate()
		if v, ok := sk.cache.Load(sqlID); ok {
			return v.(*enjoy.Template)
		}
	}
	return nil
}

// GetSql returns the raw SQL string for a template ID.
func (sk *SqlKit) GetSql(sqlID string, data map[string]interface{}) string {
	tpl := sk.getSqlTemplate(sqlID)
	if tpl == nil {
		return ""
	}
	return tpl.RenderToString(data)
}

// SetBaseSqlFilePath 配置 Enjoy sql 文件基础路径（对照 Java setBaseSqlFilePath）。
// AddSqlFile 登记的相对路径在解析时据此拼接为绝对路径。
func (sk *SqlKit) SetBaseSqlFilePath(baseSqlFilePath string) {
	sk.engine.SetBaseTemplatePath(baseSqlFilePath)
}

// SetSqlFileHotReloading 配置是否开启外部 sql 文件热重载（对照 Java setSqlFileHotReloading）。
// 仅对 AddSqlFile/AddSqlDir 添加的文件、且经 GetSqlParaByID/GetSqlParaByIDWithArgs 取用时生效：
// 开启后文件内容变更会在下次取用时自动重新解析。默认关闭。
func (sk *SqlKit) SetSqlFileHotReloading(enable bool) {
	sk.engine.SetDevMode(enable)
}

// AddSqlFile 通过外部文件添加 Enjoy sql（对照 Java addSqlFile）。
// sql 内容需包含 #sql 指令（如 `#sql("id") ... #end`），文件内按 sqlID 分段。
// 需随后调用 ParseSqlFile 解析生效。
func (sk *SqlKit) AddSqlFile(sqlFile string) {
	if strings.TrimSpace(sqlFile) == "" {
		panic("sqlFile can not be blank")
	}
	sk.fileMu.Lock()
	defer sk.fileMu.Unlock()
	sk.sqlFileList = append(sk.sqlFileList, sqlFile)
}

// AddSqlDir 扫描目录下所有 .sql 文件并登记（Go 增强，Java 无对应；满足批量加载诉求）。
// 不递归子目录。需随后调用 ParseSqlFile 解析生效。
func (sk *SqlKit) AddSqlDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	if len(paths) == 0 {
		return nil
	}
	sk.fileMu.Lock()
	defer sk.fileMu.Unlock()
	sk.sqlFileList = append(sk.sqlFileList, paths...)
	return nil
}

// ParseSqlFile 解析所有 AddSqlFile/AddSqlDir 登记的 sql 文件，按 sqlID 注册到缓存
// （对照 Java parseSqlFile）。文件内需用 #sql 指令分段。sqlID 与已有（内联或文件来源）
// 冲突时返回 error（对照 Java IllegalArgumentException("sqlId already exists")）。
// 通常初始化后调用一次；开启热重载时由 GetSqlParaByID 在文件变更时自动重新调用。
func (sk *SqlKit) ParseSqlFile() error {
	sk.fileMu.Lock()
	defer sk.fileMu.Unlock()
	return sk.parseSqlFileLocked()
}

// parseSqlFileLocked 在已持有 fileMu 的前提下解析 sqlFileList 中的全部文件。
func (sk *SqlKit) parseSqlFileLocked() error {
	for _, sqlFile := range sk.sqlFileList {
		path := sk.resolveSqlFile(sqlFile)
		tpl := sk.engine.GetTemplate(path)
		sk.fileTemplates[path] = tpl

		sqlCache := map[string]*enjoy.Template{}
		data := map[string]interface{}{SqlCacheKey: sqlCache}
		if _, err := tpl.RenderToString0(data); err != nil {
			return fmt.Errorf("parse sql file %q: %w", sqlFile, err)
		}

		for id, t := range sqlCache {
			if _, exists := sk.cache.Load(id); exists {
				return fmt.Errorf("sqlId already exists: %s", id)
			}
			sk.cache.Store(id, t)
			sk.sqlFromSqlFile[id] = t
		}
	}
	return nil
}

// reloadModifiedSqlTemplate 重新解析被修改的外部 sql 文件（对照 Java reloadModifiedSqlTemplate）。
// 调用方应已通过 isSqlTemplateModified 判定确有修改；本方法在 fileMu 下双检查以避免重复 reload。
func (sk *SqlKit) reloadModifiedSqlTemplate() {
	sk.fileMu.Lock()
	defer sk.fileMu.Unlock()
	if !sk.isSqlTemplateModifiedLocked() {
		return
	}
	// 去 engine 文件缓存，确保下次 GetTemplate 重新读盘
	sk.engine.RemoveAllTemplateCache()
	// 精准移除 cache 中来自文件的 sql（保留 AddSql / Db.sql 内联缓存，对照 Java 注释 1）
	for id := range sk.sqlFromSqlFile {
		sk.cache.Delete(id)
	}
	sk.sqlFromSqlFile = map[string]*enjoy.Template{}
	sk.fileTemplates = map[string]*enjoy.Template{}
	_ = sk.parseSqlFileLocked()
}

// isSqlTemplateModified 报告任一外部 sql 文件是否被修改（基于文件 Template 的 IsModified）。
func (sk *SqlKit) isSqlTemplateModified() bool {
	sk.fileMu.RLock()
	defer sk.fileMu.RUnlock()
	return sk.isSqlTemplateModifiedLocked()
}

// isSqlTemplateModifiedLocked 在已持有 fileMu 的前提下做实际判定。
func (sk *SqlKit) isSqlTemplateModifiedLocked() bool {
	for _, tpl := range sk.fileTemplates {
		if tpl.IsModified() {
			return true
		}
	}
	return false
}

// resolveSqlFile 把登记的 sql 文件路径解析为实际读取路径：绝对路径原样返回，
// 相对路径拼接 SetBaseSqlFilePath 设置的基础路径。
func (sk *SqlKit) resolveSqlFile(sqlFile string) string {
	if filepath.IsAbs(sqlFile) {
		return sqlFile
	}
	if base := sk.engine.GetConfig().GetBaseTemplatePath(); base != "" {
		return filepath.Join(base, sqlFile)
	}
	return sqlFile
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
