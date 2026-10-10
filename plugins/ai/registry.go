// registry.go 装配产物注册表：chat/embedding/agent/MCP 四表 + 可选服务端；
// NewRegistry 纯装配（不触网——MCP 客户端懒连接，chat/embedding 只构建）。
package ai

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	aicore "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent/simple"
	aiemb "github.com/crazy-airhead/aifei-go/ai/embedding"
	aimcp "github.com/crazy-airhead/aifei-go/ai/mcp"
	"github.com/crazy-airhead/aifei-go/log"
	"github.com/crazy-airhead/aifei-go/mcp"
)

// Registry ai.* 装配产物。map 键为配置段名；访问器缺名回落默认。
type Registry struct {
	chat       map[string]*aicore.ChatModel
	embeddings map[string]*aiemb.Model
	agents     map[string]*simple.Agent
	mcp        map[string]*aimcp.Provider
	clients    []*mcp.Client // Stop 时统一关闭（stdio 进程/HTTP 会话）
	server     *mcp.Server
	serverPath string // 服务端段挂载前缀（server 非空时有意义）

	defaultModel string
	defaultEmbed string
}

// NewRegistry 按配置装配注册表（validator 为 MCP 服务端安全钩子
// （mcp.WithSecurityValidator 同签名），可空）。任一条目装配失败即整体失败
// （fail-fast：配置错误在启动期暴露）。
func NewRegistry(cfg *Config, validator func(*http.Request) error, logger log.Logger) (*Registry, error) {
	if cfg == nil {
		cfg = &Config{}
	}
	if logger == nil {
		logger = log.Default()
	}
	r := &Registry{
		chat:       map[string]*aicore.ChatModel{},
		embeddings: map[string]*aiemb.Model{},
		agents:     map[string]*simple.Agent{},
		mcp:        map[string]*aimcp.Provider{},
	}

	// chat 模型注册表 + 默认名校验
	for _, name := range sortedKeys(cfg.Models) {
		m, err := buildChatModel(name, cfg.Models[name])
		if err != nil {
			return nil, err
		}
		r.chat[name] = m
	}
	if cfg.Default != "" {
		if _, ok := r.chat[cfg.Default]; !ok {
			return nil, fmt.Errorf("ai: default model %q is not defined in models", cfg.Default)
		}
		r.defaultModel = cfg.Default
	}

	// embedding 注册表
	for _, name := range sortedKeys(cfg.Embedding.Models) {
		m, err := buildEmbedding(name, cfg.Embedding.Models[name])
		if err != nil {
			return nil, err
		}
		r.embeddings[name] = m
	}
	if cfg.Embedding.Default != "" {
		if _, ok := r.embeddings[cfg.Embedding.Default]; !ok {
			return nil, fmt.Errorf("ai: default embedding %q is not defined in embedding.models", cfg.Embedding.Default)
		}
		r.defaultEmbed = cfg.Embedding.Default
	}

	// agent 段（引用 chat 模型注册表）
	for _, name := range sortedKeys(cfg.Agents) {
		a, err := buildAgent(name, cfg.Agents[name], r.chat, cfg.Default)
		if err != nil {
			return nil, err
		}
		r.agents[name] = a
	}

	// MCP 客户端段 + 可选服务端段
	providers, clients, err := buildMCPClients(cfg.MCP.Servers)
	if err != nil {
		return nil, err
	}
	r.mcp, r.clients = providers, clients
	if cfg.MCP.Server != (ServerConfig{}) {
		r.server = buildMCPServer(cfg.MCP.Server, validator)
		r.serverPath = serverBasePath(cfg.MCP.Server)
	}

	logger.Info("ai plugin assembled, models=%v embeddings=%v agents=%v mcp=%v",
		r.ChatModelNames(), r.EmbeddingNames(), r.AgentNames(), r.MCPClients())
	return r, nil
}

// ChatModel 返回具名 chat 模型（空名 = 默认模型；未知名返回 nil）。
func (r *Registry) ChatModel(name string) *aicore.ChatModel {
	if name == "" {
		name = r.defaultModel
	}
	return r.chat[name]
}

// Use ChatModel 的 storage.Use 式别名。
func (r *Registry) Use(name string) *aicore.ChatModel { return r.ChatModel(name) }

// Default 返回默认 chat 模型。
func (r *Registry) Default() *aicore.ChatModel { return r.ChatModel("") }

// Embedding 返回具名 embedding 模型（空名 = embedding.default）。
func (r *Registry) Embedding(name string) *aiemb.Model {
	if name == "" {
		name = r.defaultEmbed
	}
	return r.embeddings[name]
}

// Agent 返回具名 simple 智能体。
func (r *Registry) Agent(name string) *simple.Agent { return r.agents[name] }

// MCP 返回具名 MCP 客户端工具提供者桥。
func (r *Registry) MCP(name string) *aimcp.Provider { return r.mcp[name] }

// Server 返回服务端段装配的 MCP Server（未配置为 nil）。
func (r *Registry) Server() *mcp.Server { return r.server }

// ServerPath 返回服务端段挂载前缀（未配置为空串）。
func (r *Registry) ServerPath() string { return r.serverPath }

// ChatModelNames 已装配 chat 模型名（字典序）。
func (r *Registry) ChatModelNames() []string { return sortedKeys(r.chat) }

// EmbeddingNames 已装配 embedding 模型名（字典序）。
func (r *Registry) EmbeddingNames() []string { return sortedKeys(r.embeddings) }

// AgentNames 已装配智能体名（字典序）。
func (r *Registry) AgentNames() []string { return sortedKeys(r.agents) }

// MCPClients 已装配 MCP 客户端名（字典序）。
func (r *Registry) MCPClients() []string { return sortedKeys(r.mcp) }

// Close 关闭全部 MCP 客户端与服务端（幂等：Close 后注册表访问器仍可用，
// 但 MCP 调用会话已终结）。
func (r *Registry) Close() error {
	var errs []error
	for _, c := range r.clients {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	r.clients = nil
	if r.server != nil {
		if err := r.server.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sortedKeys map 键字典序（装配顺序确定，日志/报错可复现）。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
