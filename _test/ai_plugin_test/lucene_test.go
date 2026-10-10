// lucene_test.go 本地全文检索能力包黑盒测试（黄金场景搬译 Java
// LuceneTalentTest 六用例 + gse 中文分词专项 + 索引未建自动刷新 + 自定义配置）。
package ai_plugin_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	lucenet "github.com/crazy-airhead/aifei-go/plugins/ai/talent/lucene"
)

// newLuceneWorkspace 搬译 Java setUp 的标准工作区（src 两文件 + .git 忽略 +
// png 后缀外文件）。
func newLuceneWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	writeT(t, filepath.Join(src, "UserService.java"),
		"package com.test; \n public class UserService { \n public void login() { } \n }")
	writeT(t, filepath.Join(src, "config.xml"),
		"<config><timeout>3000</timeout></config>")
	gitDir := filepath.Join(dir, ".git")
	os.MkdirAll(gitDir, 0o755)
	writeT(t, filepath.Join(gitDir, "config"), "ignore this file")
	writeT(t, filepath.Join(dir, "image.png"), "fake binary data")
	return dir
}

func writeT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func luceneTools(t *testing.T, talent *lucenet.Talent) (search, refresh ai.FunctionTool) {
	t.Helper()
	tools := talent.Tools(nil)
	if len(tools) != 2 || tools[0].Name() != "full_text_search" || tools[1].Name() != "refresh_search_index" {
		t.Fatalf("双工具：%v", tools)
	}
	return tools[0], tools[1]
}

func callLucene(t *testing.T, tool ai.FunctionTool, args map[string]any) string {
	t.Helper()
	r := tool.Call(context.Background(), args)
	if r == nil {
		t.Fatal("空结果")
	}
	return r.ContentText()
}

func callLuceneCtx(t *testing.T, tool ai.FunctionTool, cwd string, args map[string]any) string {
	t.Helper()
	ctx := ai.ContextWithToolContext(context.Background(), map[string]any{"__cwd": cwd})
	r := tool.Call(ctx, args)
	if r == nil {
		t.Fatal("空结果")
	}
	return r.ContentText()
}

func TestLuceneRefreshAndSearch(t *testing.T) {
	dir := newLuceneWorkspace(t)
	talent := lucenet.New(dir)
	search, refresh := luceneTools(t, talent)

	// 索引刷新成功文案
	out := callLuceneCtx(t, refresh, dir, nil)
	if !strings.Contains(out, "本地索引刷新成功") || !strings.Contains(out, "已扫描并收录 2 个文件") {
		t.Fatalf("刷新文案：%q", out)
	}

	// 代码检索：命中 + 预览含关键字上下文
	out = callLucene(t, search, map[string]any{"query": "login", "__cwd": dir})
	if !strings.Contains(out, "UserService.java") || !strings.Contains(out, "public void login") {
		t.Fatalf("代码检索：%q", out)
	}
	if !strings.Contains(out, "Path: src/UserService.java") || !strings.Contains(out, "(Score: ") ||
		!strings.Contains(out, "Line: ~") {
		t.Fatalf("结果形态（得分/行号）：%q", out)
	}

	// 配置文件检索
	out = callLucene(t, search, map[string]any{"query": "timeout", "__cwd": dir})
	if !strings.Contains(out, "config.xml") {
		t.Fatalf("XML 检索：%q", out)
	}
}

func TestLuceneAutoRefreshOnFirstSearch(t *testing.T) {
	dir := newLuceneWorkspace(t)
	talent := lucenet.New(dir)
	search, _ := luceneTools(t, talent)

	// 不先 refresh：检索自动建索引（Java 同款兜底）
	out := callLucene(t, search, map[string]any{"query": "login", "__cwd": dir})
	if !strings.Contains(out, "UserService.java") {
		t.Fatalf("自动刷新后应命中：%q", out)
	}
}

func TestLuceneIgnoreAndExtensionFilter(t *testing.T) {
	dir := newLuceneWorkspace(t)
	talent := lucenet.New(dir)
	search, refresh := luceneTools(t, talent)
	callLuceneCtx(t, refresh, dir, nil)

	// .git 目录被忽略
	if out := callLucene(t, search, map[string]any{"query": "ignore", "__cwd": dir}); out != "未找到匹配内容。" {
		t.Fatalf("忽略目录：%q", out)
	}
	// .png 后缀不在白名单
	if out := callLucene(t, search, map[string]any{"query": "binary", "__cwd": dir}); out != "未找到匹配内容。" {
		t.Fatalf("后缀过滤：%q", out)
	}
}

func TestLuceneCustomConfiguration(t *testing.T) {
	dir := newLuceneWorkspace(t)
	talent := lucenet.New(dir)
	search, refresh := luceneTools(t, talent)

	// 仅 .png、不忽略任何目录（Java testCustomConfiguration 同款）
	talent.SearchableExtensions("png")
	talent.IgnoreNames() // 空集
	callLuceneCtx(t, refresh, dir, nil)
	if out := callLucene(t, search, map[string]any{"query": "binary", "__cwd": dir}); !strings.Contains(out, "image.png") {
		t.Fatalf("自定义配置：%q", out)
	}
}

func TestLuceneSpecialCharactersAndEmpty(t *testing.T) {
	dir := newLuceneWorkspace(t)
	talent := lucenet.New(dir)
	search, refresh := luceneTools(t, talent)
	callLuceneCtx(t, refresh, dir, nil)

	// 特殊字符（括号/空格）不抛错——Bleve MatchQuery 走分析器免查询语法解析
	out := callLucene(t, search, map[string]any{"query": "public void login()", "__cwd": dir})
	if out == "" {
		t.Fatal("特殊字符应有输出")
	}

	// 空目录优雅处理
	empty := t.TempDir()
	emptyTalent := lucenet.New(empty)
	eSearch, eRefresh := luceneTools(t, emptyTalent)
	callLuceneCtx(t, eRefresh, empty, nil)
	if out := callLucene(t, eSearch, map[string]any{"query": "anything", "__cwd": empty}); out != "未找到匹配内容。" {
		t.Fatalf("空目录：%q", out)
	}
}

// TestLuceneChineseSegmentation gse 中文分词专项（英文检索引擎的 bigram 语义
// 差异验证：词级分词命中、无关词不误命中）。
func TestLuceneChineseSegmentation(t *testing.T) {
	dir := t.TempDir()
	writeT(t, filepath.Join(dir, "notes.md"),
		"# 项目笔记\n\n用户登录系统采用分布式架构，登录模块负责身份认证。\n\n缓存策略为LRU淘汰。")
	writeT(t, filepath.Join(dir, "readme.txt"), "这是一个无关文件，讲的是缓存系统。")

	talent := lucenet.New(dir)
	search, refresh := luceneTools(t, talent)
	callLuceneCtx(t, refresh, dir, nil)

	// 中文词检索（"身份认证" 为分词可命中的词组）
	out := callLucene(t, search, map[string]any{"query": "身份认证", "__cwd": dir})
	if !strings.Contains(out, "notes.md") {
		t.Fatalf("中文词命中：%q", out)
	}
	// 单字组词（"登录"）
	out = callLucene(t, search, map[string]any{"query": "登录", "__cwd": dir})
	if !strings.Contains(out, "notes.md") || !strings.Contains(out, "登录模块") {
		t.Fatalf("预览上下文：%q", out)
	}
	// 未出现的词不命中
	if out := callLucene(t, search, map[string]any{"query": "不存在词", "__cwd": dir}); out != "未找到匹配内容。" {
		t.Fatalf("无关词：%q", out)
	}
	// 中英混合：登录 应只命中 notes.md（readme 无该词）
	if out := callLucene(t, search, map[string]any{"query": "分布式", "__cwd": dir}); !strings.Contains(out, "notes.md") {
		t.Fatalf("中英混合：%q", out)
	}
}
