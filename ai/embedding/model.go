package embedding

import (
	"context"
	"fmt"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

// defaultBatchSize 批次大小缺省（对标 EmbeddingConfig.batchSize=10；仅由 RAG
// 管线消费做分片，embed(List) 本身整批一次请求——与 Java 一致）。
const defaultBatchSize = 10

// Model 嵌入模型。
type Model struct {
	config    *ai.Config
	dialect   Dialect
	client    *httpx.Client
	options   map[string]any // 模型级默认选项（dimensions 等，逐请求可覆盖）
	batchSize int
}

// New 构建并选定方言（apiUrl/model 必填，与 NewChatModel 同错误面）。
func New(cfg ai.Config) (*Model, error) {
	if cfg.APIURL == "" {
		return nil, fmt.Errorf("ai: the config.apiUrl is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("ai: the config.model is required")
	}
	d := SelectEmbeddingDialect(&cfg)
	if d == nil {
		return nil, fmt.Errorf("ai: the embedding dialect(provider) no matched, check config or dependencies")
	}

	var hopts []httpx.Option
	if cfg.Proxy != "" {
		hopts = append(hopts, httpx.WithProxy(cfg.Proxy))
	}
	if cfg.Doer != nil {
		hopts = append(hopts, httpx.WithDoer(cfg.Doer))
	}
	return &Model{
		config:    &cfg,
		dialect:   d,
		client:    httpx.New(hopts...),
		options:   map[string]any{},
		batchSize: defaultBatchSize,
	}, nil
}

// Builder 链式构建（NewBuilder(apiURL).APIKey(k).Model(m).Build()）。
type Builder struct {
	cfg       ai.Config
	options   map[string]any
	batchSize int
}

func NewBuilder(apiURL string) *Builder {
	return &Builder{cfg: ai.Config{APIURL: apiURL}, options: map[string]any{}, batchSize: defaultBatchSize}
}

func (b *Builder) APIKey(k string) *Builder   { b.cfg.APIKey = k; return b }
func (b *Builder) Model(m string) *Builder    { b.cfg.Model = m; return b }
func (b *Builder) Provider(p string) *Builder { b.cfg.Provider = p; return b }
func (b *Builder) Standard(s string) *Builder { b.cfg.Standard = s; return b }
func (b *Builder) Doer(d httpx.Doer) *Builder { b.cfg.Doer = d; return b }
func (b *Builder) Name(n string) *Builder     { b.cfg.Name = n; return b }
func (b *Builder) Timeout(t time.Duration) *Builder {
	b.cfg.Timeout = t
	return b
}

// Options 模型级默认选项（dimensions / user 等；同名键逐请求可覆盖）。
func (b *Builder) Options(opts map[string]any) *Builder {
	for k, v := range opts {
		b.options[k] = v
	}
	return b
}

// BatchSize 批次大小（>0 才生效，对标 setBatchSize 守卫）。
func (b *Builder) BatchSize(n int) *Builder {
	if n > 0 {
		b.batchSize = n
	}
	return b
}

// Config 剩余字段的逃生舱配置。
func (b *Builder) Config(fn func(*ai.Config)) *Builder {
	fn(&b.cfg)
	return b
}

func (b *Builder) Build() (*Model, error) {
	m, err := New(b.cfg)
	if err != nil {
		return nil, err
	}
	m.options = b.options
	m.batchSize = b.batchSize
	return m, nil
}

func (m *Model) Dialect() Dialect { return m.dialect }
func (m *Model) Config() *ai.Config {
	return m.config
}

// BatchSize 嵌入批次大小。
func (m *Model) BatchSize() int { return m.batchSize }

// Options 追加模型级默认选项（Builder.Options 的构建后通道；同名键逐请求仍可覆盖）。
func (m *Model) Options(opts map[string]any) *Model {
	if m.options == nil {
		m.options = map[string]any{}
	}
	for k, v := range opts {
		m.options[k] = v
	}
	return m
}

// Dimensions 向量维度（对标 Java dimensions()：以一次真实嵌入探测，故需 ctx）。
func (m *Model) Dimensions(ctx context.Context) (int, error) {
	vec, err := m.Embed(ctx, "test")
	if err != nil {
		return 0, err
	}
	return len(vec), nil
}

// Embed 单文本嵌入。
func (m *Model) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := m.EmbedTexts(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("ai: embedding response has no data")
	}
	return vecs[0], nil
}

// EmbedTexts 批量嵌入（一次请求；与 Java embed(List) 一致，不按 batchSize 分片）。
func (m *Model) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	return m.EmbedTextsWith(ctx, texts, nil)
}

// EmbedTextsWith 批量嵌入 + 逐请求选项覆盖（对标 input(texts).options(o).call()）。
// 返回向量按 index 排序与 texts 下标对齐；条数不等报错（对标 EmbeddingException）。
func (m *Model) EmbedTextsWith(ctx context.Context, texts []string, extra map[string]any) ([][]float32, error) {
	opts := map[string]any{}
	for k, v := range m.options {
		opts[k] = v
	}
	for k, v := range extra {
		opts[k] = v
	}

	body, err := m.dialect.BuildRequestJSON(m.config, opts, texts)
	if err != nil {
		return nil, err
	}

	if m.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, m.config.Timeout)
		defer cancel()
	}
	respBody, _, err := m.client.PostJSON(ctx, m.config.APIURL, body, m.requestHeader())
	if err != nil {
		return nil, err
	}

	vecs, err := m.dialect.ParseResponseJSON(respBody)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("ai: the embedded data (%d) is not equal to the size of input texts (%d)",
			len(vecs), len(texts))
	}
	return vecs, nil
}

// requestHeader 鉴权与默认头（嵌入方言无 HeaderDialect 定制面，与 Java 对齐）。
func (m *Model) requestHeader() map[string]string {
	header := map[string]string{}
	for k, v := range m.config.Headers {
		header[k] = v
	}
	if m.config.APIKey != "" {
		header["Authorization"] = "Bearer " + m.config.APIKey
	}
	if m.config.UserAgent != "" {
		header["User-Agent"] = m.config.UserAgent
	}
	return header
}
