// Package ragloader 带三方解析依赖的文档加载器（对标 solon-ai-rag-loaders 的
// markdown/html/ddl 三件；契约 docs/arch/ai/05-rag-design.md）：markdown 标题
// 切分（goldmark）、HTML 正文抽取（goquery）、数据库 DDL 加载（database/sql +
// 可配 SQL 模板）。零依赖的 text 加载器在 ai/rag（P4-a）。
package ragloader

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// MarkdownOptions markdown 切分选项（对标 MarkdownLoader.Options）。
type MarkdownOptions struct {
	HorizontalLineAsNew bool // 水平线（ThematicBreak）切分
	CodeBlockAsNew      bool // 代码块前切分（代码块后恒切分——Java 语义）
	BlockquoteAsNew     bool // 块引用切分（并标 category=blockquote）
}

// MarkdownLoader markdown 标题切分加载器（对标 MarkdownLoader）：Heading 恒切分；
// 软/硬换行与列表项 → 空格；图片转 ![title](dest)；标题文本上 metadata
//（category=header_N + title）；行内码 code_inline / 围栏码 code_block+lang。
type MarkdownLoader struct {
	open    func() (io.Reader, error)
	options MarkdownOptions
	meta    map[string]any
}

// NewMarkdownLoader 从 io.Reader 构造（Load 时读取）。
func NewMarkdownLoader(r io.Reader) *MarkdownLoader {
	return &MarkdownLoader{
		open: func() (io.Reader, error) { return r, nil },
		meta: map[string]any{"type": "markdown"},
	}
}

// NewMarkdownLoaderFromFile 从文件路径构造（Load 时打开）。
func NewMarkdownLoaderFromFile(name string) *MarkdownLoader {
	return &MarkdownLoader{
		open: func() (io.Reader, error) { return os.Open(name) },
		meta: map[string]any{"type": "markdown"},
	}
}

// NewMarkdownLoaderFromString 从字符串构造。
func NewMarkdownLoaderFromString(s string) *MarkdownLoader {
	return NewMarkdownLoader(strings.NewReader(s))
}

// Options 切分选项（链式）。
func (l *MarkdownLoader) Options(fn func(*MarkdownOptions)) *MarkdownLoader {
	fn(&l.options)
	return l
}

// AdditionalMetadata 附加元数据。
func (l *MarkdownLoader) AdditionalMetadata(k string, v any) rag.Loader {
	l.meta[k] = v
	return l
}

// Load 解析并按访问器切分（对标 SplitVisitor）。
func (l *MarkdownLoader) Load(ctx context.Context) ([]*rag.Document, error) {
	r, err := l.open()
	if err != nil {
		return nil, err
	}
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}
	src, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	md := goldmark.New()
	doc := md.Parser().Parse(text.NewReader(src))

	v := &mdVisitor{loader: l, src: src, current: rag.NewDocument(""), paragraphs: []string{}}
	ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		return v.visit(node, entering)
	})
	return v.extract(), nil
}

// mdVisitor 切分访问器（对标 SplitVisitor；goldmark Walk 的 Enter/Exit 二相
// 收敛为：块节点 Enter 时处理开界，Exit 时不动作——切分点全部在块首）。
type mdVisitor struct {
	loader     *MarkdownLoader
	src        []byte
	documents  []*rag.Document
	paragraphs []string
	current    *rag.Document
}

func (v *mdVisitor) visit(node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		// FencedCodeBlock 的段落在 Enter 已收集，Exit 时恒切分（doneAndNew）
		if n, ok := node.(*ast.FencedCodeBlock); ok {
			v.current.Meta("category", "code_block")
			if lang := string(n.Language(v.src)); lang != "" {
				v.current.Meta("lang", lang)
			}
			v.doneAndNew()
		}
		return ast.WalkContinue, nil
	}

	switch n := node.(type) {
	case *ast.Document:
		v.current = rag.NewDocument("")

	case *ast.Heading:
		v.doneAndNew()
		// 标题文本在子 Text 节点访问时上 metadata（category=header_N + title）

	case *ast.Image:
		dest := string(n.Destination)
		if dest != "" {
			v.paragraphs = append(v.paragraphs, " !["+string(n.Title)+"]("+dest+") ")
		}

	case *ast.ThematicBreak:
		if v.loader.options.HorizontalLineAsNew {
			v.doneAndNew()
		}

	case *ast.Text:
		if parent := n.Parent(); parent != nil {
			if h, ok := parent.(*ast.Heading); ok {
				v.current.Meta("category", fmt.Sprintf("header_%d", h.Level))
				v.current.Meta("title", string(n.Segment.Value(v.src)))
				return ast.WalkContinue, nil
			}
		}
		v.paragraphs = append(v.paragraphs, string(n.Segment.Value(v.src)))
		// 软/硬换行在 goldmark 是 Text 节点标志（对标 visit(SoftLineBreak/HardLineBreak)
		// 的 translateLineBreakToSpace：有段落才补空格）
		if n.SoftLineBreak() || n.HardLineBreak() {
			v.lineBreakToSpace()
		}

	case *ast.String: // 行内代码等字面
		v.paragraphs = append(v.paragraphs, string(n.Value))
		if v.current != nil {
			v.current.Meta("category", "code_inline")
		}

	case *ast.CodeBlock: // 缩进代码块（无 lang）
		v.paragraphs = append(v.paragraphs, string(n.Lines().Value(v.src)))
		if v.current != nil {
			v.current.Meta("category", "code_block")
		}

	case *ast.FencedCodeBlock:
		if v.loader.options.CodeBlockAsNew {
			v.doneAndNew()
		}
		v.lineBreakToSpace()
		v.paragraphs = append(v.paragraphs, string(n.Lines().Value(v.src)))

	case *ast.Blockquote:
		if v.loader.options.BlockquoteAsNew {
			v.doneAndNew()
		}
		v.lineBreakToSpace()
		if v.current != nil {
			v.current.Meta("category", "blockquote")
		}

	case *ast.ListItem:
		v.lineBreakToSpace()
	}
	return ast.WalkContinue, nil
}

// extract 收口（对标 extract：doneAndNew 后返回）。
func (v *mdVisitor) extract() []*rag.Document {
	v.doneAndNew()
	return v.documents
}

// doneAndNew 结束当前文档并重开（有段落才产出）。
func (v *mdVisitor) doneAndNew() {
	if len(v.paragraphs) > 0 {
		v.current.Content = strings.Join(v.paragraphs, "")
		v.current.MetaMap(v.loader.meta)
		v.documents = append(v.documents, v.current)
		v.paragraphs = nil
	}
	v.current = rag.NewDocument("")
}

// lineBreakToSpace 换行/列表项 → 空格（有段落才补——Java translateLineBreakToSpace）。
func (v *mdVisitor) lineBreakToSpace() {
	if len(v.paragraphs) > 0 {
		v.paragraphs = append(v.paragraphs, " ")
	}
}

var _ rag.Loader = (*MarkdownLoader)(nil)
