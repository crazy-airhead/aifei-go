package generate

import (
	"context"
	"fmt"
	"strconv"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

// Content 生成内容（对标 GenerateContent，网络字段名照抄：mime 为驼峰 mimeType）。
// Data 为纯 base64 字符串（无 data:URI 前缀）。
type Content struct {
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	URL      string `json:"url,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Value 取值优先级 url > data > text（对标 getValue）。
func (c *Content) Value() string {
	if c.URL != "" {
		return c.URL
	}
	if c.Data != "" {
		return c.Data
	}
	return c.Text
}

// Response 生成响应（对标 GenerateResponse）。
type Response struct {
	Model string
	Data  []Content
}

// HasData 是否有生成内容（nil 或空均 false，对标 hasData）。
func (r *Response) HasData() bool { return len(r.Data) > 0 }

// Content 首条内容（无数据 nil，对标 getContent）。
func (r *Response) Content() *Content {
	if !r.HasData() {
		return nil
	}
	return &r.Data[0]
}

// RequestDesc 生成请求描述（对标 GenerateRequestDesc）：prompt 二选一（str 优先），
// options 链式累积；Call 时合并模型级默认（配置形态字符串做强类型转换）。
type RequestDesc struct {
	model     *Model
	promptStr string
	promptMap map[string]any
	options   map[string]any
}

// Options 批量合并选项（可多次调用累积，对标 options(Consumer) 形态）。
func (r *RequestDesc) Options(opts map[string]any) *RequestDesc {
	for k, v := range opts {
		r.options[k] = v
	}
	return r
}

// Option 单个选项。
func (r *RequestDesc) Option(key string, val any) *RequestDesc {
	r.options[key] = val
	return r
}

// Size 尺寸（对标 GenerateOptions.size）。
func (r *RequestDesc) Size(s string) *RequestDesc { return r.Option("size", s) }

// Quality 质量。
func (r *RequestDesc) Quality(q string) *RequestDesc { return r.Option("quality", q) }

// ResponseFormat 响应格式（json / b64_json 等）。
func (r *RequestDesc) ResponseFormat(rf string) *RequestDesc { return r.Option("response_format", rf) }

// Call 发起生成请求。
func (r *RequestDesc) Call(ctx context.Context) (*Response, error) {
	opts := map[string]any{}
	coerceOptionValues(opts, r.model.defaults) // 模型级默认（配置形态强转）
	for k, v := range r.options {              // 请求级覆盖
		opts[k] = v
	}

	body, err := r.model.dialect.BuildRequestJSON(r.model.config, opts, r.promptStr, r.promptMap)
	if err != nil {
		return nil, err
	}

	if r.model.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.model.config.Timeout)
		defer cancel()
	}
	respBody, _, err := r.model.client.PostJSON(ctx, r.model.config.APIURL, body, r.model.requestHeader())
	if err != nil {
		return nil, err
	}
	return r.model.dialect.ParseResponseJSON(r.model.config, respBody)
}

// Model 生成模型。
type Model struct {
	config   *Config
	dialect  Dialect
	client   *httpx.Client
	defaults map[string]any // 模型级默认选项（配置形态，合并时强类型转换）
}

// New 构建并选定方言（apiUrl/model 必填，与 NewChatModel 同错误面）。
func New(cfg Config) (*Model, error) {
	if cfg.APIURL == "" {
		return nil, fmt.Errorf("ai: the config.apiUrl is required")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("ai: the config.model is required")
	}
	d := SelectGenerateDialect(&cfg)
	if d == nil {
		return nil, fmt.Errorf("ai: the generate dialect(provider) no matched, check config or dependencies")
	}

	var hopts []httpx.Option
	if cfg.Proxy != "" {
		hopts = append(hopts, httpx.WithProxy(cfg.Proxy))
	}
	if cfg.Doer != nil {
		hopts = append(hopts, httpx.WithDoer(cfg.Doer))
	}
	return &Model{
		config:   &cfg,
		dialect:  d,
		client:   httpx.New(hopts...),
		defaults: map[string]any{},
	}, nil
}

// Builder 链式构建（NewBuilder(apiURL).APIKey(k).Model(m).Build()）。
type Builder struct {
	cfg      Config
	defaults map[string]any
}

func NewBuilder(apiURL string) *Builder {
	return &Builder{cfg: Config{ai.Config{APIURL: apiURL}, ""}, defaults: map[string]any{}}
}

func (b *Builder) APIKey(k string) *Builder     { b.cfg.APIKey = k; return b }
func (b *Builder) Model(m string) *Builder      { b.cfg.Model = m; return b }
func (b *Builder) Provider(p string) *Builder   { b.cfg.Provider = p; return b }
func (b *Builder) Standard(s string) *Builder   { b.cfg.Standard = s; return b }
func (b *Builder) Doer(d httpx.Doer) *Builder   { b.cfg.Doer = d; return b }
func (b *Builder) Name(n string) *Builder       { b.cfg.Name = n; return b }
func (b *Builder) TaskURL(u string) *Builder    { b.cfg.TaskURL = u; return b }
func (b *Builder) Timeout(t time.Duration) *Builder {
	b.cfg.Timeout = t
	return b
}

// Options 模型级默认选项（配置文件形态：字符串 "1"/"true" 在合并进请求时做强
// 类型转换，对标 GenerateOptions.putAll）。
func (b *Builder) Options(opts map[string]any) *Builder {
	for k, v := range opts {
		b.defaults[k] = v
	}
	return b
}

// Config 剩余字段（含 Header 等）的逃生舱配置。
func (b *Builder) Config(fn func(*Config)) *Builder {
	fn(&b.cfg)
	return b
}

func (b *Builder) Build() (*Model, error) {
	m, err := New(b.cfg)
	if err != nil {
		return nil, err
	}
	m.defaults = b.defaults
	return m, nil
}

func (m *Model) Dialect() Dialect   { return m.dialect }
func (m *Model) Config() *Config    { return m.config }
func (m *Model) Defaults() map[string]any { return m.defaults }

// Prompt 字符串提示（对标 prompt(String)）。
func (m *Model) Prompt(p string) *RequestDesc {
	return &RequestDesc{model: m, promptStr: p, options: map[string]any{}}
}

// PromptMap 键值形态提示（对标 prompt(GeneratePrompt)/prompt(Map)：图生图等
// 多键场景，如 {"prompt": "...", "base_image_url": "..."}）。
func (m *Model) PromptMap(p map[string]any) *RequestDesc {
	return &RequestDesc{model: m, promptMap: p, options: map[string]any{}}
}

// requestHeader 鉴权与默认头（生成方言零 header 逻辑——DashScope 异步头
// X-DashScope-Async 由调用方经 Config.Headers 传入，对标 Java headerSet）。
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

// coerceOptionValues 配置形态字符串 → 强类型（对标 GenerateOptions.putAll）：
// "true"/"false"（大小写不敏感）→ bool；无 '.' 的数字 → int64；含 '.' → float64；
// 其余原样。仅作用于模型级默认（请求级选项是代码写就的强类型）。
func coerceOptionValues(dst map[string]any, src map[string]any) {
	for k, v := range src {
		s, ok := v.(string)
		if !ok {
			dst[k] = v
			continue
		}
		if ls := lowerASCII(s); ls == "true" || ls == "false" {
			dst[k] = ls == "true"
			continue
		}
		if isNumericLike(s) {
			if containsDot(s) {
				f, _ := strconv.ParseFloat(s, 64)
				dst[k] = f
			} else {
				n, _ := strconv.ParseInt(s, 10, 64)
				dst[k] = n
			}
			continue
		}
		dst[k] = s
	}
}

func containsDot(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return true
		}
	}
	return false
}

// isNumericLike Java Assert.isNumber 近似：可选正负号开头 + 数字/单点组成
//（strconv 已做主体校验，这里挡掉 "Inf"/"NaN" 等 ParseFloat 也接受的形态）。
func isNumericLike(s string) bool {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	if i >= len(s) {
		return false
	}
	digits, dots := 0, 0
	for ; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			digits++
		} else if c == '.' {
			dots++
			if dots > 1 {
				return false
			}
		} else {
			return false
		}
	}
	return digits > 0
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}
