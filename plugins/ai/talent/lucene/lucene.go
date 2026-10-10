// Package lucene 本地全文检索能力包（对标 solon-ai-talent-lucene：
// `full_text_search` + `refresh_search_index` 双工具、可配忽略名与可搜索后缀、
// 索引未建时检索自动先刷新、得分/行号/预览格式化逐字对标）。
//
// Go 实现选型（用户指示替代原「不迁」决策）：**Bleve**（blevesearch/bleve/v2，
// 内存索引 + BM25 评分——Lucene 的 Go 等价物）+ **gse**（go-ego/gse 纯 Go 中文
// 分词，内嵌词典——jieba 词典系；非 gojieba，后者为 cgo 绑定无纯 Go 版本），
// 不引入 Elasticsearch（需部署服务，非等价替代——用户明确排除）。gse 分词器
// 注册为 Bleve 自定义 analyzer，索引与查询同源分词。
package lucene

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/registry"
	"github.com/go-ego/gse"
)

// 分词器与分析器注册名。
const (
	gseTokenizerName = "aifei_gse"
	gseAnalyzerName  = "aifei_gse"
	// contentField 全文字段（对标 Java "content" TextField）。
	contentField = "content"
	// maxHits 检索返回上限（Java TopDocs 20 同款）。
	maxHits = 20
)

var (
	segOnce sync.Once
	seg     *gse.Segmenter
	segErr  error
)

// segmenter 内嵌全量中文词典的单例分词器（加载秒级，全进程共享）。
func segmenter() (*gse.Segmenter, error) {
	segOnce.Do(func() {
		var s gse.Segmenter
		if err := s.LoadDictEmbed(); err != nil { // "zh" 全量内嵌词典
			segErr = err
			return
		}
		seg = &s
	})
	return seg, segErr
}

// gseTokenizer gse 分词的 Bleve Tokenizer（CutSearch 搜索引擎模式 + HMM；
// 输出词元小写化——对标 StandardAnalyzer 的 lowercase 归一）。
type gseTokenizer struct{ seg *gse.Segmenter }

// Tokenize 实现 bleve Tokenizer。
func (t *gseTokenizer) Tokenize(input []byte) analysis.TokenStream {
	text := strings.ToLower(string(input))
	words := t.seg.CutSearch(text, true) // 搜索模式 + HMM（未登录词成词）
	stream := make(analysis.TokenStream, 0, len(words))
	pos := 0
	offset := 0
	for _, w := range words {
		trimmed := strings.TrimFunc(w, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsPunct(r)
		})
		if trimmed == "" {
			continue
		}
		// 词内偏移以原文查找累积（CutSearch 的词序即原文序）
		idx := strings.Index(text[offset:], strings.ToLower(w))
		start := offset + idx
		if idx < 0 {
			start = offset
		}
		end := start + len(w)
		if end > len(text) {
			end = len(text)
		}
		offset = end
		pos++
		stream = append(stream, &analysis.Token{
			Term:     []byte(strings.ToLower(trimmed)),
			Start:    start,
			End:      end,
			Position: pos,
			Type:     analysis.AlphaNumeric,
		})
	}
	return stream
}

// registerOnce 注册到 Bleve registry（tokenizer + analyzer 二件——
// analyzer = gse 分词，索引与 MatchQuery 查询侧同源）。
var registerOnce sync.Once

func registerAnalyzer() error {
	s, err := segmenter()
	if err != nil {
		return err
	}
	registerOnce.Do(func() {
		tok := &gseTokenizer{seg: s}
		_ = registry.RegisterTokenizer(gseTokenizerName,
			func(map[string]any, *registry.Cache) (analysis.Tokenizer, error) { return tok, nil })
		_ = registry.RegisterAnalyzer(gseAnalyzerName,
			func(config map[string]any, cache *registry.Cache) (analysis.Analyzer, error) {
				tokenizer, err := cache.TokenizerNamed(gseTokenizerName)
				if err != nil {
					return nil, err
				}
				return &analysis.DefaultAnalyzer{Tokenizer: tokenizer}, nil
			})
	})
	return nil
}

// Talent 本地全文检索能力包。
type Talent struct {
	ai.TalentBase
	workDir string

	ignoreNames          map[string]struct{}
	searchableExtensions map[string]struct{}

	mu        sync.Mutex
	index     bleve.Index
	docs      map[string]string // 相对路径 → 文件内容（预览/行号源）
	refreshed bool
}

// New 构造（workDir 缺省工作目录；__cwd 工具参数优先）。
func New(workDir string) *Talent {
	return &Talent{
		workDir: workDir,
		ignoreNames: setOf(".git", ".svn", ".log", ".tmp", "node_modules",
			"target", "bin", "build", ".idea", ".vscode", ".DS_Store"),
		searchableExtensions: setOf("java", "xml", "js", "ts", "md", "properties",
			"sql", "txt", "html", "json", "yml", "yaml", "sh", "bat", "go"),
		docs: map[string]string{},
	}
}

// IgnoreNames 覆写忽略的文件/目录名集合（nil 保留缺省）。
func (t *Talent) IgnoreNames(names ...string) *Talent {
	if names != nil {
		t.ignoreNames = setOf(names...)
	}
	return t
}

// SearchableExtensions 覆写可搜索后缀集合（不带点、小写归一；nil 保留缺省）。
func (t *Talent) SearchableExtensions(exts ...string) *Talent {
	if exts != nil {
		lowered := make([]string, len(exts))
		for i, e := range exts {
			lowered[i] = strings.ToLower(e)
		}
		t.searchableExtensions = setOf(lowered...)
	}
	return t
}

// Name 能力包名。
func (t *Talent) Name() string { return "Lucene" }

// Description 描述。
func (t *Talent) Description() string { return "本地全文检索（Bleve + 中文分词）" }

// Tools 双工具。
func (t *Talent) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("full_text_search").
			Desc("在工作区内进行本地全文检索（支持代码、配置、文档等文本文件）。").
			StringParam("query", "搜索关键字或短语", true).
			Handler(t.toolSearch).
			Build(),
		ai.NewTool("refresh_search_index").
			Desc("刷新工作区内的本地全文索引。").
			Handler(t.toolRefresh).
			Build(),
	}
}

// workPath 工作目录解析（__cwd 工具参数优先，回退构造期 workDir）。
func (t *Talent) workPath(ctx context.Context) (string, error) {
	if tc := ai.ToolContextOf(ctx); tc != nil {
		if cwd, _ := tc["__cwd"].(string); cwd != "" {
			return cwd, nil
		}
	}
	if t.workDir != "" {
		return t.workDir, nil
	}
	return "", fmt.Errorf("lucene: working directory is not set")
}

// toolRefresh 索引重建（对标 refreshSearchIndex：CREATE 模式——旧索引整体替换）。
func (t *Talent) toolRefresh(ctx context.Context, _ map[string]any) (any, error) {
	workPath, err := t.workPath(ctx)
	if err != nil {
		return err.Error(), nil
	}
	start := time.Now()
	count, err := t.rebuild(workPath)
	if err != nil {
		return "刷新索引失败: " + err.Error(), nil
	}
	return fmt.Sprintf("本地索引刷新成功！已扫描并收录 %d 个文件，耗时 %dms。", count, time.Since(start).Milliseconds()), nil
}

// rebuild 扫描 + 重建（忽略目录整棵跳过；后缀过滤；单文件失败跳过计数不受影响）。
func (t *Talent) rebuild(workPath string) (int, error) {
	if err := registerAnalyzer(); err != nil {
		return 0, fmt.Errorf("lucene: gse init: %w", err)
	}
	im := bleve.NewIndexMapping()
	im.DefaultAnalyzer = gseAnalyzerName
	index, err := bleve.NewMemOnly(im)
	if err != nil {
		return 0, err
	}
	docs := map[string]string{}
	count := 0

	rootAbs, err := abs(workPath)
	if err != nil {
		return 0, err
	}
	batch := index.NewBatch()
	err = walkTextFiles(rootAbs, t.ignoreNames, t.searchableExtensions, func(rel, content string) {
		_ = batch.Index(rel, map[string]any{contentField: content})
		docs[rel] = content
		count++
	})
	if err != nil {
		index.Close()
		return 0, err
	}
	if err := index.Batch(batch); err != nil {
		index.Close()
		return 0, err
	}

	t.mu.Lock()
	old := t.index
	t.index = index
	t.docs = docs
	t.refreshed = true
	t.mu.Unlock()
	if old != nil {
		old.Close()
	}
	return count, nil
}

// toolSearch 检索（对标 full_text_search：索引未建先自动刷新；仍无 → 提示；
// 无命中 → 未找到；命中 → 得分/行号/预览格式化）。
func (t *Talent) toolSearch(ctx context.Context, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	workPath, err := t.workPath(ctx)
	if err != nil {
		return err.Error(), nil
	}

	t.mu.Lock()
	index := t.index
	refreshed := t.refreshed
	t.mu.Unlock()

	// 索引未建：先自动刷新（Java 同款兜底）
	if index == nil {
		if _, err := t.rebuild(workPath); err != nil {
			return "检查索引状态失败: " + err.Error(), nil
		}
		refreshed = true
		t.mu.Lock()
		index = t.index
		t.mu.Unlock()
	}
	if index == nil {
		if !refreshed {
			return "本地索引尚未建立。请先执行 refresh_search_index 工具以初始化搜索环境。", nil
		}
		return "未找到匹配内容。", nil
	}

	// 特殊字符安全：Bleve MatchQuery 走分析器而非查询语法解析（Lucene
	// QueryParser.escape 的 Go 对应物是「不解析」——天然免转义）
	q := bleve.NewMatchQuery(query)
	q.SetField(contentField)
	req := bleve.NewSearchRequestOptions(q, maxHits, 0, false)
	result, err := index.Search(req)
	if err != nil {
		return "搜索失败: " + err.Error(), nil
	}
	if len(result.Hits) == 0 {
		return "未找到匹配内容。", nil
	}

	t.mu.Lock()
	docs := t.docs
	t.mu.Unlock()

	var sb strings.Builder
	fmt.Fprintf(&sb, "找到 %d 个结果 (按相关性排序)：\n\n", len(result.Hits))
	for _, hit := range result.Hits {
		content := docs[hit.ID]
		lowerContent := strings.ToLower(content)
		lowerQuery := strings.ToLower(query)
		idx := strings.Index(lowerContent, lowerQuery)
		lineNum := 1
		if idx >= 0 {
			lineNum = 1 + strings.Count(content[:idx], "\n")
		}
		fmt.Fprintf(&sb, "Path: %s (Score: %.2f, Line: ~%d)\n", hit.ID, hit.Score, lineNum)
		if idx >= 0 {
			start := idx - 60
			if start < 0 {
				start = 0
			}
			end := idx + len(query) + 120
			if end > len(content) {
				end = len(content)
			}
			preview := strings.TrimSpace(strings.ReplaceAll(content[start:end], "\n", " "))
			sb.WriteString("   预览: ..." + preview + "...\n")
		} else {
			head := content
			if len(head) > 120 {
				head = head[:120]
			}
			preview := strings.TrimSpace(strings.ReplaceAll(head, "\n", " "))
			sb.WriteString("   预览: " + preview + "...\n")
		}
		sb.WriteString("\n")
	}
	return sb.String(), nil
}

// OnAttach 附加时预建索引（首搜秒回——Java 构造不建、检索兜底刷新；预建是
// Go 侧增量：词典加载秒级移出首个工具调用）。
func (t *Talent) OnAttach(p *ai.Prompt) {
	if t.workDir == "" {
		return
	}
	t.mu.Lock()
	need := !t.refreshed
	t.mu.Unlock()
	if need {
		_, _ = t.rebuild(t.workDir)
	}
}
