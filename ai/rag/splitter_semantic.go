// splitter_semantic.go SemanticSplitter（对标同名）：按分隔符拆句 → 滑动窗口
// 与下一句的嵌入余弦 → 低于阈值处切分 → 最小句数约束 → 超长组按 token 再切。
// 嵌入经 rag.Embedder 缝（Java 硬编码 dimensions(512) 选项不迁——缝无逐次
// 选项，维度由模型配置决定，有意偏离）。
package rag

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// 句子分隔符常量（Java 同名三表）。
var (
	EnglishDelim   = []string{". ", "! ", "? ", "\n"}
	ChineseDelim   = []string{"。", "！", "？", "；", "\n"}
	AllCommonDelim = []string{". ", "! ", "? ", "。", "！", "？", "；", "\n"}
)

// SemanticSplitter 语义切分器。
type SemanticSplitter struct {
	embedder             Embedder
	similarityThreshold  float64
	maxChunkTokenSize    int
	similarityWindow     int
	minSentencesPerChunk int
	delimiters           []string
	tokenizer            Tokenizer // nil → ApproxTokenCount（超长组再切用）
}

// NewSemanticSplitter 缺省参数构造（阈值 0.5、最大 512 token、窗口 3、最少
// 1 句、全部常用分隔符）。Java 构造器 IllegalArgumentException → Go error。
func NewSemanticSplitter(e Embedder) (*SemanticSplitter, error) {
	return NewSemanticSplitterWith(e, 0.5, 512, 3, 1, AllCommonDelim)
}

// NewSemanticSplitterWith 全参构造（校验同 Java：阈值开区间、正整数参、非空分隔符）。
func NewSemanticSplitterWith(e Embedder, similarityThreshold float64,
	maxChunkTokenSize, similarityWindow, minSentencesPerChunk int, delimiters []string) (*SemanticSplitter, error) {
	switch {
	case e == nil:
		return nil, errors.New("rag: embeddingModel must not be nil")
	case similarityThreshold <= 0 || similarityThreshold >= 1:
		return nil, errors.New("rag: similarityThreshold must be between 0 and 1 (exclusive)")
	case maxChunkTokenSize <= 0:
		return nil, errors.New("rag: maxChunkTokenSize must be positive")
	case similarityWindow <= 0:
		return nil, errors.New("rag: similarityWindow must be positive")
	case minSentencesPerChunk <= 0:
		return nil, errors.New("rag: minSentencesPerChunk must be positive")
	case len(delimiters) == 0:
		return nil, errors.New("rag: delimiters must not be empty")
	}
	return &SemanticSplitter{
		embedder:             e,
		similarityThreshold:  similarityThreshold,
		maxChunkTokenSize:    maxChunkTokenSize,
		similarityWindow:     similarityWindow,
		minSentencesPerChunk: minSentencesPerChunk,
		delimiters:           append([]string(nil), delimiters...),
	}, nil
}

// Tokenizer 注入超长组再切的计数器（nil 忽略）。
func (s *SemanticSplitter) Tokenizer(t Tokenizer) *SemanticSplitter {
	if t != nil {
		s.tokenizer = t
	}
	return s
}

// Split 切分。
func (s *SemanticSplitter) Split(ctx context.Context, docs []*Document) ([]*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []*Document
	for _, doc := range docs {
		chunks, err := s.splitDocument(ctx, doc)
		if err != nil {
			return nil, err
		}
		out = append(out, chunks...)
	}
	return out, nil
}

func (s *SemanticSplitter) splitDocument(ctx context.Context, doc *Document) ([]*Document, error) {
	text := doc.Content
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	sentences := splitSentences(text, s.delimiters)
	if len(sentences) == 0 {
		return nil, nil
	}
	// 句子数不够窗口大小：整文档单块返回（不求嵌入）
	if len(sentences) <= s.similarityWindow {
		return []*Document{chunkOf(text, doc)}, nil
	}
	sims, err := s.computeSimilarities(ctx, sentences)
	if err != nil {
		return nil, fmt.Errorf("rag: semantic split embed: %w", err)
	}
	groups := groupSentences(sentences, findSplitIndices(sims, s.similarityThreshold, s.similarityWindow, s.minSentencesPerChunk))
	groups = s.splitOversizedGroups(groups)
	return createDocuments(groups, doc), nil
}

// splitSentences 按分隔符拆句，分隔符保留在前句末尾（Java 同款字节级扫描）。
func splitSentences(text string, delims []string) []string {
	var sentences []string
	start, i := 0, 0
	for i < len(text) {
		found := ""
		for _, d := range delims {
			if strings.HasPrefix(text[i:], d) {
				found = d
				break
			}
		}
		if found != "" {
			sentence := text[start : i+len(found)]
			if strings.TrimSpace(sentence) != "" {
				sentences = append(sentences, sentence)
			}
			i += len(found)
			start = i
		} else {
			i++
		}
	}
	if start < len(text) {
		if remaining := text[start:]; strings.TrimSpace(remaining) != "" {
			sentences = append(sentences, remaining)
		}
	}
	return sentences
}

// computeSimilarities 滑动窗口（i..i+window 拼接）与下一句（i+window）的余弦；
// 全部文本一批收集后按模型批次嵌入（Java batchEmbed 同构）。
func (s *SemanticSplitter) computeSimilarities(ctx context.Context, sentences []string) ([]float64, error) {
	count := len(sentences) - s.similarityWindow
	texts := make([]string, 0, count*2)
	for i := 0; i < count; i++ {
		var windowText strings.Builder
		for j := i; j < i+s.similarityWindow; j++ {
			windowText.WriteString(sentences[j])
		}
		texts = append(texts, windowText.String())
	}
	for i := 0; i < count; i++ {
		texts = append(texts, sentences[i+s.similarityWindow])
	}

	embeddings := make([][]float32, 0, len(texts))
	for _, batch := range Partition(texts, s.embedder.BatchSize()) {
		vecs, err := s.embedder.EmbedTexts(ctx, batch)
		if err != nil {
			return nil, err
		}
		embeddings = append(embeddings, vecs...)
	}

	sims := make([]float64, count)
	for i := 0; i < count; i++ {
		sim, err := CosineSimilarity(embeddings[i], embeddings[count+i])
		if err != nil {
			return nil, err
		}
		sims[i] = sim
	}
	return sims, nil
}

// findSplitIndices 相似度低于阈值处标记切点（句子索引），保证相邻切点间满足
// 最小句数；首切点恒为 0（Java 同款）。
func findSplitIndices(sims []float64, threshold float64, window, minSentences int) []int {
	splitIndices := []int{0}
	lastSplit := 0
	for i, sim := range sims {
		sentenceIdx := i + window
		if sim < threshold && sentenceIdx-lastSplit >= minSentences {
			splitIndices = append(splitIndices, sentenceIdx)
			lastSplit = sentenceIdx
		}
	}
	return splitIndices
}

func groupSentences(sentences []string, splitIndices []int) [][]string {
	var groups [][]string
	for i, start := range splitIndices {
		end := len(sentences)
		if i+1 < len(splitIndices) {
			end = splitIndices[i+1]
		}
		if start < end {
			groups = append(groups, append([]string(nil), sentences[start:end]...))
		}
	}
	return groups
}

// splitOversizedGroups 超过最大 token 数的组逐句再分配（计数经 Tokenizer 缝）。
func (s *SemanticSplitter) splitOversizedGroups(groups [][]string) [][]string {
	count := func(text string) int {
		if s.tokenizer != nil {
			return s.tokenizer.TokenCount(text)
		}
		return ApproxTokenCount(text)
	}
	var out [][]string
	for _, group := range groups {
		total := 0
		for _, sentence := range group {
			total += count(sentence)
		}
		if total <= s.maxChunkTokenSize {
			out = append(out, group)
			continue
		}
		var current []string
		currentTokens := 0
		for _, sentence := range group {
			sentenceTokens := count(sentence)
			if currentTokens+sentenceTokens <= s.maxChunkTokenSize {
				current = append(current, sentence)
				currentTokens += sentenceTokens
			} else {
				if len(current) > 0 {
					out = append(out, current)
				}
				current = []string{sentence}
				currentTokens = sentenceTokens
			}
		}
		if len(current) > 0 {
			out = append(out, current)
		}
	}
	return out
}

func createDocuments(groups [][]string, parent *Document) []*Document {
	docs := make([]*Document, 0, len(groups))
	for _, group := range groups {
		content := strings.Join(group, "")
		if strings.TrimSpace(content) != "" {
			docs = append(docs, chunkOf(content, parent))
		}
	}
	return docs
}

var _ Splitter = (*SemanticSplitter)(nil)
