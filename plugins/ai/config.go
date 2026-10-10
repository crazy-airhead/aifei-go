// config.go ai.* 配置结构体与装载（storage.LoadConfig 同款式样：全局
// config.SubBind YAML round-trip 绑定）。
package ai

import (
	"fmt"

	"github.com/crazy-airhead/aifei-go/config"
)

// Config ai.* 配置全集。
type Config struct {
	// Default 默认 chat 模型名（引用 models 键）。
	Default string                 `yaml:"default"`
	Models  map[string]ModelConfig `yaml:"models"`
	// Embedding 嵌入模型段（独立注册表 + 独立默认名）。
	Embedding EmbeddingConfig        `yaml:"embedding"`
	Agents    map[string]AgentConfig `yaml:"agents"`
	MCP       MCPConfig              `yaml:"mcp"`
}

// ModelConfig 模型段条目（chat 与 embedding 共用形状；embedding 忽略
// defaultAutoToolCall 等 chat 专属键）。
type ModelConfig struct {
	// Provider 方言家族（"openai"/"anthropic"/"gemini"/"ollama"/"dashscope"）。
	Provider string `yaml:"provider"`
	// Standard 显式方言名（优先于 Provider；如 "openai-responses"）。
	Standard      string            `yaml:"standard"`
	APIURL        string            `yaml:"apiUrl"`
	APIKey        string            `yaml:"apiKey"`
	Model         string            `yaml:"model"`
	ContextLength int               `yaml:"contextLength"`
	Description   string            `yaml:"description"`
	UserAgent     string            `yaml:"userAgent"`
	Proxy         string            `yaml:"proxy"`
	Headers       map[string]string `yaml:"headers"`
	// TimeoutSeconds 请求超时（秒；<=0 用方言/httpx 缺省）。
	TimeoutSeconds int `yaml:"timeout"`
	// DefaultAutoToolCall 自动工具调用缺省开关（chat 专属）。
	DefaultAutoToolCall bool `yaml:"defaultAutoToolCall"`
	// Doer 命名 httpx.Doer 引用（RegisterDoer 注册表；测试注桩/生产仪表化）。
	Doer string `yaml:"doer"`
}

// EmbeddingConfig 嵌入模型段。
type EmbeddingConfig struct {
	Default string                 `yaml:"default"`
	Models  map[string]ModelConfig `yaml:"models"`
}

// AgentConfig simple 智能体段条目。
type AgentConfig struct {
	// Model 引用的 chat 模型名（ai.models 键；缺省 ai.default）。
	Model string `yaml:"model"`
	// Role/Instruction 组装系统提示词（simple.SystemPrompt 同款布局）。
	Role        string `yaml:"role"`
	Instruction string `yaml:"instruction"`

	MaxRetries        int    `yaml:"maxRetries"`
	RetryDelayMs      int64  `yaml:"retryDelayMs"`
	SessionWindowSize int    `yaml:"sessionWindowSize"`
	OutputKey         string `yaml:"outputKey"`
	OutputSchema      string `yaml:"outputSchema"`
}

// MCPConfig MCP 段：servers 客户端注册表 + 可选 server 对外暴露。
type MCPConfig struct {
	Servers map[string]MCPServerConfig `yaml:"servers"`
	Server  ServerConfig               `yaml:"server"`
}

// MCPServerConfig MCP 客户端段条目：command（stdio）与 url（streamable
// HTTP）二选一——两者同设或缺省皆报错。
type MCPServerConfig struct {
	Command []string `yaml:"command"`
	URL     string   `yaml:"url"`
	// Headers 附加请求头（url 形态；如 Authorization）。
	Headers map[string]string `yaml:"headers"`
	// RequestTimeoutSeconds 每请求超时（秒；<=0 用 Client 缺省 20s）。
	RequestTimeoutSeconds int `yaml:"requestTimeout"`
	// ClientName/ClientVersion 握手身份（缺省 aifei-mcp-client/1.0.0）。
	ClientName    string `yaml:"clientName"`
	ClientVersion string `yaml:"clientVersion"`
	// Allow/Deny 工具白/黑名单（aimcp 桥过滤）。
	Allow []string `yaml:"allow"`
	Deny  []string `yaml:"deny"`
	// CacheTTLSeconds 工具列表缓存（秒；0=缺省 30s，负值关闭缓存）。
	CacheTTLSeconds int `yaml:"cacheTtl"`
}

// ServerConfig 可选 MCP 服务端段（对外暴露工具/资源/提示词；工具本体经
// ai.Server() 的 Tool/Resource/Prompt 注册表在代码侧登记）。
type ServerConfig struct {
	Name     string `yaml:"name"`
	Version  string `yaml:"version"`
	BasePath string `yaml:"basePath"`
}

// LoadConfig 读全局配置 prefix 子树（空缺省 "ai"）。前缀不存在时返回零值
// 配置（插件装配出空注册表，不视为错误——对照 storage.LoadConfig）。
func LoadConfig(prefix string) (*Config, error) {
	if prefix == "" {
		prefix = "ai"
	}
	cfg := &Config{}
	if err := config.SubBind(prefix, cfg); err != nil {
		return nil, fmt.Errorf("ai: bind %s config: %w", prefix, err)
	}
	return cfg, nil
}
