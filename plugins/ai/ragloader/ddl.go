// ddl.go 数据库 DDL 加载器（对标 DdlLoader + DdlLoadConfig）：经 information_schema
// 类查询枚举表，逐表取建表语句并重组完整 DDL；SQL 模板全参数化（缺省 MySQL 形态，
// #{key} 占位符替换——对标 SnEL.evalTmpl 的模板面）。
package ragloader

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// DdlLoadConfig DDL 加载 SQL 配置（对标同名；Java 字段名拼写 shcema 之源码笔误
// 在 Go 修正为 schema）。
type DdlLoadConfig struct {
	SchemaTableQuerySql                 string // 枚举库表
	SchemaTableQueryAndSchemaTemplate   string // 单库全表追加条件
	SchemaTableQueryAndTableTemplate    string // 单库单表追加条件
	SchemaTableQueryResultSchemaColumn  string // 结果集 schema 列名
	SchemaTableQueryResultTableColumn   string // 结果集表名列
	DdlQuerySqlTemplate                 string // 建表语句查询模板
	DdlQueryResultDdlColumn             string // 建表语句结果列名
	FullDdlCreateTableSqlPrefix         string // 重组前缀（CREATE TABLE）
	FullDdlCreateTableSqlNeedSchema     bool   // 重组是否带 schema 限定
	FullDdlCreateTableSqlDot            string // 分隔符
	FullDdlCreateTableSqlLeftIdentifer  string // 左界定符
	FullDdlCreateTableSqlRightIdentifer string // 右界定符
	FullDdlCreateTableSqlTruncSymbol    string // 截断符（从首个 "(" 起保留表体）
}

// DefaultMysqlDdlLoadConfig MySQL 缺省配置（对标 DdlLoader 无参构造的内置形态）。
func DefaultMysqlDdlLoadConfig() DdlLoadConfig {
	return DdlLoadConfig{
		SchemaTableQuerySql:                 "select TABLE_SCHEMA, TABLE_NAME from information_schema.TABLES where TABLE_SCHEMA not in ('mysql', 'sys', 'information_schema')",
		SchemaTableQueryAndSchemaTemplate:   "and TABLE_SCHEMA = '#{tableSchema}'",
		SchemaTableQueryAndTableTemplate:    "and TABLE_SCHEMA = '#{tableSchema}' and TABLE_NAME = '#{tableName}'",
		SchemaTableQueryResultSchemaColumn:  "TABLE_SCHEMA",
		SchemaTableQueryResultTableColumn:   "TABLE_NAME",
		DdlQuerySqlTemplate:                 "SHOW CREATE TABLE `#{tableSchema}`.`#{tableName}`",
		DdlQueryResultDdlColumn:             "Create Table",
		FullDdlCreateTableSqlPrefix:         "CREATE TABLE",
		FullDdlCreateTableSqlNeedSchema:     true,
		FullDdlCreateTableSqlDot:            ".",
		FullDdlCreateTableSqlLeftIdentifer:  "`",
		FullDdlCreateTableSqlRightIdentifer: "`",
		FullDdlCreateTableSqlTruncSymbol:    "(",
	}
}

// DdlLoader DDL 加载器。
type DdlLoader struct {
	db     *sql.DB
	config DdlLoadConfig
	meta   map[string]any

	targetSchema string
	targetTable  string
}

// NewDdlLoader MySQL 缺省配置构造。
func NewDdlLoader(db *sql.DB) *DdlLoader {
	return &DdlLoader{db: db, config: DefaultMysqlDdlLoadConfig(), meta: map[string]any{}}
}

// NewDdlLoaderWithConfig 自定义配置构造。
func NewDdlLoaderWithConfig(db *sql.DB, config DdlLoadConfig) *DdlLoader {
	return &DdlLoader{db: db, config: config, meta: map[string]any{}}
}

// AdditionalMetadata 附加元数据。
func (l *DdlLoader) AdditionalMetadata(k string, v any) rag.Loader {
	l.meta[k] = v
	return l
}

// LoadOptions 目标范围（对标 Options.loadOptions）：schema 空=全库全表；
// schema 非空 table 空=单库全表；都非空=单库单表。
func (l *DdlLoader) LoadOptions(targetSchema, targetTable string) *DdlLoader {
	l.targetSchema, l.targetTable = targetSchema, targetTable
	return l
}

// Load 枚举表 → 逐表取 DDL → 重组完整建表语句（对标 load + getFullDdl）。
func (l *DdlLoader) Load(ctx context.Context) ([]*rag.Document, error) {
	query := l.config.SchemaTableQuerySql
	switch {
	case l.targetSchema == "":
		// 全库全表
	case l.targetTable == "":
		query = evalTmpl(query+" "+l.config.SchemaTableQueryAndSchemaTemplate,
			map[string]string{"tableSchema": l.targetSchema})
	default:
		query = evalTmpl(query+" "+l.config.SchemaTableQueryAndTableTemplate,
			map[string]string{"tableSchema": l.targetSchema, "tableName": l.targetTable})
	}

	rows, err := l.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("ragloader: schema table query: %w", err)
	}
	defer rows.Close()

	var documents []*rag.Document
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var tableSchema, tableName string
		if err := rows.Scan(&tableSchema, &tableName); err != nil {
			return nil, err
		}

		showDDL := evalTmpl(l.config.DdlQuerySqlTemplate,
			map[string]string{"tableSchema": tableSchema, "tableName": tableName})
		var tableDDL string
		if err := l.db.QueryRowContext(ctx, showDDL).Scan(&tableDDL); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return nil, fmt.Errorf("ragloader: ddl query %s: %w", tableName, err)
		}

		full := l.fullDDL(tableDDL, tableSchema, tableName)
		doc := rag.NewDocument(full).
			Meta("table", tableName).
			MetaMap(l.meta)
		documents = append(documents, doc)
	}
	return documents, rows.Err()
}

// fullDDL 重组完整建表语句（截断符起保留表体 + 前缀/界定符/限定名拼接）。
func (l *DdlLoader) fullDDL(tableDDL, tableSchema, tableName string) string {
	truncation := tableDDL
	if i := strings.Index(tableDDL, l.config.FullDdlCreateTableSqlTruncSymbol); i >= 0 {
		truncation = tableDDL[i:]
	}
	c := l.config
	if c.FullDdlCreateTableSqlNeedSchema {
		return evalTmpl("#{prefix} #{left}#{tableSchema}#{right}#{dot}#{left}#{tableName}#{right} #{truncation}",
			map[string]string{
				"prefix": c.FullDdlCreateTableSqlPrefix, "left": c.FullDdlCreateTableSqlLeftIdentifer,
				"right": c.FullDdlCreateTableSqlRightIdentifer, "dot": c.FullDdlCreateTableSqlDot,
				"tableSchema": tableSchema, "tableName": tableName, "truncation": truncation,
			})
	}
	return evalTmpl("#{prefix} #{left}#{tableName}#{right} #{truncation}",
		map[string]string{
			"prefix": c.FullDdlCreateTableSqlPrefix, "left": c.FullDdlCreateTableSqlLeftIdentifer,
			"right": c.FullDdlCreateTableSqlRightIdentifer, "tableName": tableName, "truncation": truncation,
		})
}

// evalTmpl #{key} 占位符替换（对标 SnEL.evalTmpl 的字面模板面）。
func evalTmpl(tmpl string, vars map[string]string) string {
	out := tmpl
	for k, v := range vars {
		out = strings.ReplaceAll(out, "#{"+k+"}", v)
	}
	return out
}

var _ rag.Loader = (*DdlLoader)(nil)
