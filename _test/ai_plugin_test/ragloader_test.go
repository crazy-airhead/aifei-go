// ragloader_test.go plugins/ai/ragloader 黑盒测试（P5-R）：markdown 标题切分
// 矩阵（选项开关/元数据染色/图片与代码块形态）、html 正文与元数据抽取、
// ddl 加载 sqlite 回环（自定义 SQL 模板 + 全库/单库/单表三形态 + 完整 DDL 重组）。
package ai_plugin_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ragloader "github.com/crazy-airhead/aifei-go/plugins/ai/ragloader"
	_ "modernc.org/sqlite"
)

// ---- markdown ----

func TestRagloaderMarkdownHeadingSplit(t *testing.T) {
	md := "# 标题一\n第一段文字。\n\n## 标题二\n第二段文字，继续。\n\n# 标题三\n第三段。"
	docs, err := ragloader.NewMarkdownLoaderFromString(md).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("标题应切 3 段，得 %d：%v", len(docs), docs)
	}
	// 首段归属：标题一 下的第一段
	if !strings.Contains(docs[0].Content, "第一段") || docs[0].Metadata["category"] != "header_1" {
		t.Fatalf("docs[0]：%+v", docs[0])
	}
	if docs[1].Metadata["category"] != "header_2" || docs[1].Metadata["title"] != "标题二" {
		t.Fatalf("docs[1] 元数据：%v", docs[1].Metadata)
	}
	if docs[2].Metadata["title"] != "标题三" {
		t.Fatalf("docs[2] 元数据：%v", docs[2].Metadata)
	}
	for _, d := range docs {
		if d.Metadata["type"] != "markdown" {
			t.Fatalf("附加元数据 type=markdown 缺失：%v", d.Metadata)
		}
	}
	// 软换行 → 空格
	if strings.Contains(docs[0].Content, "\n") {
		t.Fatalf("软换行应归一为空格：%q", docs[0].Content)
	}
}

func TestRagloaderMarkdownOptionalSplits(t *testing.T) {
	md := "段落一\n\n---\n\n段落二\n\n> 引用内容\n\n段落三\n\n```go\nfmt.Println(\"hi\")\n```\n\n段落四"

	// 缺省：水平线/块引用不切；代码块后恒切（前不切）
	docs, err := ragloader.NewMarkdownLoaderFromString(md).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("缺省应 2 段（代码块后切一次），得 %d", len(docs))
	}

	// 全开：水平线 + 块引用 + 代码块前都切
	docs, err = ragloader.NewMarkdownLoaderFromString(md).
		Options(func(o *ragloader.MarkdownOptions) {
			o.HorizontalLineAsNew = true
			o.BlockquoteAsNew = true
			o.CodeBlockAsNew = true
		}).
		Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Java 语义：块引用后的段落不另起（blockquote 切的是块首，不是块尾）——
	// 引用内容与段落三同文档
	if len(docs) != 5 {
		t.Fatalf("全开应 5 段，得 %d：%v", len(docs), docs)
	}
	// 块引用段染色
	found := false
	for _, d := range docs {
		if d.Metadata["category"] == "blockquote" {
			found = true
		}
	}
	if !found {
		t.Fatal("块引用段应有 category=blockquote")
	}
	// 代码块段染色 + lang
	found = false
	for _, d := range docs {
		if d.Metadata["category"] == "code_block" && d.Metadata["lang"] == "go" {
			found = strings.Contains(d.Content, "Println")
		}
	}
	if !found {
		t.Fatal("代码块段应有 category=code_block + lang=go + 字面内容")
	}
}

func TestRagloaderMarkdownImageAndList(t *testing.T) {
	md := "前文\n\n![图题](https://x/a.png)\n\n- 项一\n- 项二"
	docs, err := ragloader.NewMarkdownLoaderFromString(md).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, d := range docs {
		joined += d.Content
	}
	// Java 语义：图片转 ![title属性](dest)，alt 文本作为普通 Text 节点相邻保留
	if !strings.Contains(joined, "![](https://x/a.png)") || !strings.Contains(joined, "图题") {
		t.Fatalf("图片形态：%q", joined)
	}
	if !strings.Contains(joined, "项一 项二") || strings.Contains(joined, "项一\n") {
		t.Fatalf("列表项换行应归一空格：%q", joined)
	}
}

// ---- html ----

func TestRagloaderHtmlExtract(t *testing.T) {
	html := `<!DOCTYPE html><html lang="zh-CN"><head><title>页面标题</title>
<meta name="description" content="页面描述"></head>
<body><h1>大标题</h1><p>第一段。</p><p>第二段。</p></body></html>`
	docs, err := ragloader.NewHtmlSimpleLoader(strings.NewReader(html)).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("html 恒单文档，得 %d", len(docs))
	}
	d := docs[0]
	if d.Metadata["title"] != "页面标题" {
		t.Fatalf("title：%v", d.Metadata["title"])
	}
	if d.Metadata["description"] != "页面描述" {
		t.Fatalf("description：%v", d.Metadata["description"])
	}
	if d.Metadata["language"] != "zh-CN" {
		t.Fatalf("language：%v", d.Metadata["language"])
	}
	if d.Metadata["type"] != "html" {
		t.Fatalf("type：%v", d.Metadata["type"])
	}
	for _, want := range []string{"大标题", "第一段。", "第二段。"} {
		if !strings.Contains(d.Content, want) {
			t.Fatalf("正文缺 %q：%q", want, d.Content)
		}
	}
	if strings.Contains(d.Content, "<") {
		t.Fatalf("正文应为纯文本：%q", d.Content)
	}
}

func TestRagloaderHtmlFileAndMissingMeta(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.html")
	os.WriteFile(file, []byte("<html><body><p>仅正文</p></body></html>"), 0o600)
	docs, err := ragloader.NewHtmlSimpleLoaderFromFile(file).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Content != "仅正文" {
		t.Fatalf("正文：%q", docs[0].Content)
	}
	if _, has := docs[0].Metadata["title"]; has {
		t.Fatal("无 title 时不应有键")
	}
}

// ---- ddl（sqlite 回环，自定义 SQL 模板）----

func sqliteDdlConfig() ragloader.DdlLoadConfig {
	c := ragloader.DefaultMysqlDdlLoadConfig()
	c.SchemaTableQuerySql = "select 'main' as TABLE_SCHEMA, name as TABLE_NAME from sqlite_master where type='table' and name not like 'sqlite_%'"
	c.SchemaTableQueryAndSchemaTemplate = "and 'main' = '#{tableSchema}'"
	c.SchemaTableQueryAndTableTemplate = "and 'main' = '#{tableSchema}' and name = '#{tableName}'"
	c.SchemaTableQueryResultSchemaColumn = "TABLE_SCHEMA"
	c.SchemaTableQueryResultTableColumn = "TABLE_NAME"
	c.DdlQuerySqlTemplate = "select sql from sqlite_master where type='table' and name = '#{tableName}'"
	c.DdlQueryResultDdlColumn = "sql"
	c.FullDdlCreateTableSqlNeedSchema = false
	c.FullDdlCreateTableSqlLeftIdentifer = "`"
	c.FullDdlCreateTableSqlRightIdentifer = "`"
	return c
}

func newSqliteDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, ddl := range []string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)",
		"CREATE TABLE orders (id INTEGER PRIMARY KEY, user_id INTEGER)",
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestRagloaderDdlAllTables(t *testing.T) {
	db := newSqliteDB(t)
	docs, err := ragloader.NewDdlLoaderWithConfig(db, sqliteDdlConfig()).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("全库全表应 2 文档，得 %d", len(docs))
	}
	var names []string
	for _, d := range docs {
		names = append(names, d.Metadata["table"].(string))
		if !strings.HasPrefix(d.Content, "CREATE TABLE `") {
			t.Fatalf("完整 DDL 重组：%q", d.Content)
		}
		if !strings.Contains(d.Content, "(") {
			t.Fatalf("表体截断保留：%q", d.Content)
		}
	}
	// sqlite_master 按创建序（users 先建）
	if names[0] != "users" || names[1] != "orders" {
		t.Fatalf("表序：%v", names)
	}
}

func TestRagloaderDdlTargeted(t *testing.T) {
	db := newSqliteDB(t)
	l := ragloader.NewDdlLoaderWithConfig(db, sqliteDdlConfig())

	// 单库单表
	docs, err := l.LoadOptions("main", "users").Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Metadata["table"] != "users" {
		t.Fatalf("单库单表：%v", docs)
	}

	// 单库全表
	docs, err = l.LoadOptions("main", "").Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("单库全表应 2，得 %d", len(docs))
	}

	// 附加元数据
	l.AdditionalMetadata("source", "test")
	docs, err = l.LoadOptions("main", "users").Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if docs[0].Metadata["source"] != "test" {
		t.Fatalf("附加元数据：%v", docs[0].Metadata)
	}
}

// ---- 模板求值（SnEL.evalTmpl 对应物）经 ddl 配置间接验证：占位符替换正确性
// 已由上面三形态覆盖；此处补边界（无占位符原样） ----

func TestRagloaderDdlNoPlaceholderChange(t *testing.T) {
	// 直接走默认 MySQL 配置的重组逻辑（不连库）：fullDDL 经由导出面不可达，
	// 用单表 sqlite 形态验证截断与拼接
	db := newSqliteDB(t)
	c := sqliteDdlConfig()
	docs, err := ragloader.NewDdlLoaderWithConfig(db, c).LoadOptions("main", "orders").Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "CREATE TABLE `orders` (id INTEGER PRIMARY KEY, user_id INTEGER)"
	if docs[0].Content != want {
		t.Fatalf("重组形态：\n得  %q\n期望 %q", docs[0].Content, want)
	}
	fmt.Fprint(os.Stderr, "")
}
