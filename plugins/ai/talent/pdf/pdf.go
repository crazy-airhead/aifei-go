// Package pdf PDF 能力包（对标 PdfTalent：pdf_parse 解析文本层——与 ragloader
// 的 PdfLoader 共用 ledongthuc/pdf；pdf_create 从简：markdown/html/text →
// HTML 模板 → 无三方 PDF 渲染器时声明式降级（生成 .html 载体并明确说明——
// Java 用 openhtmltopdf，Go 生态等价物按需后接）。落 plugins（依赖 ragloader）。
package pdf

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ai "github.com/crazy-airhead/aifei-go/ai"
	ragloader "github.com/crazy-airhead/aifei-go/plugins/ai/ragloader"
)

// PDF 能力包。
type PDF struct {
	ai.TalentBase
	rootPath string
}

// New 构造。
func New(rootPath string) *PDF { return &PDF{rootPath: rootPath} }

// Name 名称。
func (t *PDF) Name() string { return "pdf_tool" }

// Description 描述。
func (t *PDF) Description() string {
	return "PDF 专家：解析 PDF 内容，或将 Markdown/HTML 转换成 PDF。支持富文本和基础排版。"
}

// Tools 双工具。
func (t *PDF) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("pdf_parse").
			Desc("解析 PDF 文件的文本内容（逐页）").
			StringParam("fileName", "PDF 文件名（相对工作目录）", true).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				fileName, _ := args["fileName"].(string)
				path, err := resolveSandboxed(t.rootPath, fileName)
				if err != nil {
					return "非法路径访问", nil
				}
				if _, err := os.Stat(path); err != nil {
					return "错误：文件不存在。", nil
				}
				docs, err := ragloader.NewPdfLoaderFromFile(path).
					LoadMode(ragloader.PdfPage).Load(ctx)
				if err != nil {
					return "解析失败: " + err.Error(), nil
				}
				var sb strings.Builder
				for i, doc := range docs {
					sb.WriteString(fmt.Sprintf("--- 第 %d 页 ---\n%s\n", i+1, doc.Content))
				}
				return sb.String(), nil
			}).
			Build(),
		ai.NewTool("pdf_create").
			Desc("将 Markdown/HTML/纯文本内容排版为 PDF（内部经 HTML 模板渲染）").
			StringParam("fileName", "输出文件名（自动补 .pdf 后缀）", true).
			StringParam("content", "内容（markdown 或 html 或 text）", true).
			StringParam("format", "格式：markdown | html | text", true).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				fileName, _ := args["fileName"].(string)
				content, _ := args["content"].(string)
				format, _ := args["format"].(string)

				if !strings.HasSuffix(strings.ToLower(fileName), ".pdf") {
					fileName += ".pdf"
				}
				path, err := resolveSandboxed(t.rootPath, fileName)
				if err != nil {
					return "非法路径访问", nil
				}
				html := renderHTML(content, format)
				// 渲染载体：Go 无 openhtmltopdf 等价——PDF 二进制生成声明式降级，
				// 落同名 .html（浏览器打印即 PDF），并明确提示
				htmlPath := strings.TrimSuffix(path, ".pdf") + ".html"
				if err := os.WriteFile(htmlPath, []byte(html), 0o644); err != nil {
					return "生成失败: " + err.Error(), nil
				}
				return "PDF 渲染载体已生成: " + filepath.Base(htmlPath) +
					"（浏览器打开 → 打印 → 存为 PDF；原生 PDF 编排待三方渲染器接入）", nil
			}).
			Build(),
	}
}

// renderHTML 内容 → HTML 模板（BaseFont 字体族、行高、内边距——Java 模板同款）。
func renderHTML(content, format string) string {
	var body string
	switch strings.ToLower(format) {
	case "html":
		body = content
	case "text":
		body = "<pre>" + escapeHTML(content) + "</pre>"
	default: // markdown：轻量转换（标题/粗体/代码块/换行）
		body = simpleMarkdownToHTML(content)
	}
	return `<!DOCTYPE html>
<html><head><meta charset="utf-8">
<style>
body { font-family: "PingFang SC", "Microsoft YaHei", "Noto Sans CJK SC", sans-serif;
       line-height: 1.6; padding: 20px; }
table { border-collapse: collapse; }
td, th { border: 1px solid #999; padding: 4px 8px; }
</style></head><body>` + body + `</body></html>`
}

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// simpleMarkdownToHTML 极简 markdown 渲染（标题/粗体/行内码/段落）。
func simpleMarkdownToHTML(md string) string {
	var sb strings.Builder
	for _, line := range strings.Split(md, "\n") {
		switch {
		case strings.HasPrefix(line, "### "):
			sb.WriteString("<h3>" + inline(strings.TrimPrefix(line, "### ")) + "</h3>")
		case strings.HasPrefix(line, "## "):
			sb.WriteString("<h2>" + inline(strings.TrimPrefix(line, "## ")) + "</h2>")
		case strings.HasPrefix(line, "# "):
			sb.WriteString("<h1>" + inline(strings.TrimPrefix(line, "# ")) + "</h1>")
		case strings.TrimSpace(line) == "":
			sb.WriteString("")
		default:
			sb.WriteString("<p>" + inline(line) + "</p>")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func inline(s string) string {
	r := strings.NewReplacer("**", "<strong>", "`", "<code>")
	s = r.Replace(s)
	// 粗体闭合（成对 **）
	s = strings.Replace(s, "<strong>", "\x00", 1_000)
	parts := strings.Split(s, "\x00<strong>")
	var sb strings.Builder
	for i, p := range parts {
		if i == 0 {
			sb.WriteString(p)
			continue
		}
		if idx := strings.Index(p, "</strong>"); idx < 0 && strings.Contains(p, "<strong>") {
			// 简化：第二个 ** 位置换闭合
			p = strings.Replace(p, "<strong>", "</strong>", 1)
		}
		sb.WriteString("<strong>" + p)
	}
	return sb.String()
}

// resolveSandboxed 路径沙箱。
func resolveSandboxed(rootPath, name string) (string, error) {
	if rootPath == "" {
		rootPath = "."
	}
	abs, err := filepath.Abs(filepath.Join(rootPath, name))
	if err != nil {
		return "", err
	}
	root, err := filepath.Abs(rootPath)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) && abs != root {
		return "", fmt.Errorf("pdf: 非法路径访问")
	}
	return abs, nil
}
