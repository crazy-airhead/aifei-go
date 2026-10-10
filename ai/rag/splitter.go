// splitter.go 切分器 SPI（对标 DocumentSplitter）+ 文本切分基座 + Regex/
// TokenSize 切分器 + Pipeline（对标 SplitterPipeline）。JsonSplitter 与
// SemanticSplitter 体积独立成文件（splitter_json.go / splitter_semantic.go）。
package rag

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Splitter 文档切分器 SPI。
type Splitter interface {
	// Split 切分文档集（入参 docs 的每个文档可能产出多个子文档；元数据按
	// Java 惯例由各实现决定继承面）。
	Split(ctx context.Context, docs []*Document) ([]*Document, error)
}

// chunkOf 子块构造：内容 + 父元数据整表拷贝（Java new Document(chunk, metadata)
// 构造器 putAll 拷贝同款，含 nil 值——与 Meta 的 nil 跳过语义刻意区分）。
func chunkOf(content string, parent *Document) *Document {
	d := NewDocument(content)
	for k, v := range parent.Metadata {
		d.Metadata[k] = v
	}
	return d
}

// splitDocs 文本切分基座（对标 TextSplitter 抽象类模板方法）：逐文档切文本，
// 空块跳过。
func splitDocs(docs []*Document, splitText func(string) []string) []*Document {
	var outs []*Document
	for _, doc := range docs {
		for _, chunk := range splitText(doc.Content) {
			if len(chunk) > 0 {
				outs = append(outs, chunkOf(chunk, doc))
			}
		}
	}
	return outs
}

// ---- RegexTextSplitter（对标同名）----

// RegexTextSplitter 正则文本切分器（缺省按空行 `\n\n` 切）。KeepSeparator 为
// Go 增量：把匹配到的分隔段并回前一块（契约 §4「保留分隔模式可选」）。
type RegexTextSplitter struct {
	pattern *regexp.Regexp
	keep    bool
}

// NewRegexTextSplitter 缺省空行切分。
func NewRegexTextSplitter() *RegexTextSplitter {
	s, err := NewRegexTextSplitterWith("\n\n")
	if err != nil {
		panic(err) // 固定模式，不可能失败
	}
	return s
}

// NewRegexTextSplitterWith 自定义正则（编译失败报错）。
func NewRegexTextSplitterWith(regex string) (*RegexTextSplitter, error) {
	re, err := regexp.Compile(regex)
	if err != nil {
		return nil, fmt.Errorf("rag: bad splitter regex %q: %w", regex, err)
	}
	return &RegexTextSplitter{pattern: re}, nil
}

// KeepSeparator 分隔段并回前一块（链式）。
func (s *RegexTextSplitter) KeepSeparator(b bool) *RegexTextSplitter { s.keep = b; return s }

// Split 切分。
func (s *RegexTextSplitter) Split(_ context.Context, docs []*Document) ([]*Document, error) {
	return splitDocs(docs, s.splitText), nil
}

func (s *RegexTextSplitter) splitText(text string) []string {
	if !s.keep {
		return s.pattern.Split(text, -1)
	}
	locs := s.pattern.FindAllStringIndex(text, -1)
	if len(locs) == 0 {
		return []string{text}
	}
	var out []string
	start := 0
	for _, l := range locs {
		out = append(out, text[start:l[1]]) // 分隔段随前块
		start = l[1]
	}
	if start < len(text) {
		out = append(out, text[start:])
	}
	return out
}

var _ Splitter = (*RegexTextSplitter)(nil)

// ---- TokenSizeTextSplitter（对标同名；jtokkit → Tokenizer 缝）----

// Tokenizer token 计数缝：jtokkit 无零依赖 Go 等价物，内置字符近似
// （ApproxTokenCount），插件侧可注入真实实现（tiktoken 等）。
type Tokenizer interface {
	TokenCount(s string) int
}

// TokenSizeTextSplitter 定长 token 切分（Java 同名算法的字符近似移植：块取
// 「计数不超过 chunkSize 的最长前缀」→ 优先截到最后一个句读（.?!\n）→ 过滤
// 过短块 → 按截短文本推进游标；截短恒为剩余文本前缀，推进按 rune 精确）。
// Java 的 UTF-16 length 比较在此一律按 rune 计（启发式参数，语义等价）。
type TokenSizeTextSplitter struct {
	chunkSize             int
	minChunkSizeChars     int
	minChunkLengthToEmbed int
	maxChunkCount         int
	keepSeparator         bool
	tokenizer             Tokenizer // nil → ApproxTokenCount
}

// TokenSize 缺省值（Java 构造器链同款）。
const (
	DefaultChunkSize             = 500
	DefaultMinChunkSizeChars     = 300
	DefaultMinChunkLengthToEmbed = 5
	DefaultMaxChunkCount         = 1000
)

// NewTokenSizeTextSplitter 全缺省构造（500/300/5/1000/true）。
func NewTokenSizeTextSplitter() *TokenSizeTextSplitter {
	return NewTokenSizeTextSplitterWith(DefaultChunkSize, DefaultMinChunkSizeChars,
		DefaultMinChunkLengthToEmbed, DefaultMaxChunkCount, true)
}

// NewTokenSizeTextSplitterWith 全参构造（非正参回落缺省——Java 调用方惯传
// 缺省的习惯不成立时报错不如容错，且 Refilter 同款 <=0 兜底风格）。
func NewTokenSizeTextSplitterWith(chunkSize, minChunkSizeChars, minChunkLengthToEmbed, maxChunkCount int, keepSeparator bool) *TokenSizeTextSplitter {
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}
	if minChunkSizeChars <= 0 {
		minChunkSizeChars = DefaultMinChunkSizeChars
	}
	if minChunkLengthToEmbed <= 0 {
		minChunkLengthToEmbed = DefaultMinChunkLengthToEmbed
	}
	if maxChunkCount <= 0 {
		maxChunkCount = DefaultMaxChunkCount
	}
	return &TokenSizeTextSplitter{
		chunkSize:             chunkSize,
		minChunkSizeChars:     minChunkSizeChars,
		minChunkLengthToEmbed: minChunkLengthToEmbed,
		maxChunkCount:         maxChunkCount,
		keepSeparator:         keepSeparator,
	}
}

// Tokenizer 注入计数器（nil 忽略——Java setEncodingRegistry 同款）。
func (s *TokenSizeTextSplitter) Tokenizer(t Tokenizer) *TokenSizeTextSplitter {
	if t != nil {
		s.tokenizer = t
	}
	return s
}

// Split 切分。
func (s *TokenSizeTextSplitter) Split(_ context.Context, docs []*Document) ([]*Document, error) {
	return splitDocs(docs, s.splitText), nil
}

func (s *TokenSizeTextSplitter) tokenCount(text string) int {
	if s.tokenizer != nil {
		return s.tokenizer.TokenCount(text)
	}
	return ApproxTokenCount(text)
}

func (s *TokenSizeTextSplitter) splitText(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var chunks []string
	runes := []rune(text)
	cursor, chunksCount := 0, 0
	for cursor < len(runes) && chunksCount < s.maxChunkCount {
		end := takeRunesForTokens(runes, cursor, s.chunkSize, s.tokenCount)
		chunkText := string(runes[cursor:end])
		if strings.TrimSpace(chunkText) == "" { // 纯空白块：跳过这些 rune
			cursor = end
			continue
		}
		// 优先截到最后一个句读（Java 46/63/33/10 四字符同款）
		if last := strings.LastIndexAny(chunkText, ".?!\n"); last > 0 && last > s.minChunkSizeChars {
			chunkText = chunkText[:last+1]
		}
		appended := strings.TrimSpace(chunkText)
		if !s.keepSeparator {
			appended = strings.TrimSpace(strings.ReplaceAll(chunkText, "\n", " "))
		}
		if utf8.RuneCountInString(appended) > s.minChunkLengthToEmbed {
			chunks = append(chunks, appended)
		}
		cursor += utf8.RuneCountInString(chunkText) // 截短恒为前缀，按 rune 精确推进
		chunksCount++
	}
	if cursor < len(runes) { // 剩余尾巴（Java 剩余文本无条件拍平换行）
		rest := strings.TrimSpace(strings.ReplaceAll(string(runes[cursor:]), "\n", " "))
		if utf8.RuneCountInString(rest) > s.minChunkLengthToEmbed {
			chunks = append(chunks, rest)
		}
	}
	return chunks
}

var _ Splitter = (*TokenSizeTextSplitter)(nil)

// takeRunesForTokens 取 [cursor, end) 的最长 rune 前缀使计数不超过 chunkSize
// （至少 1 个 rune 保证推进；BPE 计数对前缀非单调，故线性扫描而非二分）。
func takeRunesForTokens(runes []rune, cursor, chunkSize int, count func(string) int) int {
	for end := cursor + 1; end <= len(runes); end++ {
		if count(string(runes[cursor:end])) > chunkSize {
			if end == cursor+1 {
				return end // 单 rune 即超限，强制取 1
			}
			return end - 1
		}
	}
	return len(runes)
}

// ApproxTokenCount 字符近似 token 计数（缺省方案）：CJK 每字 1 token、其余每
// 4 字符 1 token（向上取整）——契约 §4 的经验系数（jtokkit 零依赖替代）。
func ApproxTokenCount(s string) int {
	total, cjk := utf8.RuneCountInString(s), 0
	for _, r := range s {
		if isCJK(r) {
			cjk++
		}
	}
	if others := total - cjk; others > 0 {
		return cjk + (others+3)/4
	}
	return cjk
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r)
}

// ---- SplitterPipeline（对标同名）----

// SplitterPipeline 顺序组合切分器（前级输出为后级输入）。
type SplitterPipeline struct {
	pipeline []Splitter
}

// NewSplitterPipeline 建管线（可空，Next 追加）。
func NewSplitterPipeline(splitters ...Splitter) *SplitterPipeline {
	p := &SplitterPipeline{}
	return p.Next(splitters...)
}

// Next 尾端追加（Java next() 同名同位；nil 元素忽略）。variadic 兼顾 Java 的
// 单个/List 两形态。
func (p *SplitterPipeline) Next(splitters ...Splitter) *SplitterPipeline {
	for _, s := range splitters {
		if s != nil {
			p.pipeline = append(p.pipeline, s)
		}
	}
	return p
}

// Split 逐级折叠。
func (p *SplitterPipeline) Split(ctx context.Context, docs []*Document) ([]*Document, error) {
	var err error
	for _, s := range p.pipeline {
		if docs, err = s.Split(ctx, docs); err != nil {
			return nil, err
		}
	}
	return docs, nil
}

var _ Splitter = (*SplitterPipeline)(nil)
