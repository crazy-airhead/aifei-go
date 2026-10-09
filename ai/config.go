package ai

import (
	"time"

	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

// Config 模型接入配置（对标 ChatConfig）：方言选择与 httpx 客户端构建的输入。
type Config struct {
	Name        string
	Description string

	APIKey string
	APIURL string

	Standard string // 显式方言名（"openai-completions" 等，可选；优先于 Provider）
	Provider string // "openai"/"anthropic"/...（Standard 缺省时按此匹配方言）

	Model         string
	ContextLength int

	Headers   map[string]string
	UserAgent string
	Timeout   time.Duration
	Proxy     string

	DefaultAutoToolCall bool
	DefaultOptions      func(*ChatOptions)

	Doer httpx.Doer // 测试注入缝（httptest 假服务）；nil 时用默认 http.Client
}

// StandardOrProvider 方言选择键。
func (c *Config) StandardOrProvider() string {
	if c.Standard != "" {
		return c.Standard
	}
	return c.Provider
}
