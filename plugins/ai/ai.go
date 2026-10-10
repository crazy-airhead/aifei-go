// Package ai 是 solon-ai 迁移的配置装配插件（P4-d）：读全局配置 `ai.*`
// 段，装配出 chat 模型注册表 / 默认模型 / embedding 模型 / simple 智能体 /
// MCP 客户端 Provider / 可选 MCP 服务端，并以包级默认暴露（Use(name) 语义，
// 对照 plugins/storage 式样）。
//
// 装配即全量方言注册（dialects.go blank import 五方言），app.yml 只写
// provider/standard 即可命中方言，业务代码无需逐包 blank import。
//
// 典型接线：
//
//	if err := config.Init(os.Args); err != nil { ... }
//	p, _ := ai.NewPlugin(nil)
//	app := aifei.New(aifei.WithPlugin(p))
//	server.Run(app, ":8080")
//	// 业务侧：m := ai.Use("")（默认模型）或 ai.Use("gpt4o")、ai.Agent("helper")
//
// 配置样例（app.yml）：
//
//	ai:
//	  default: gpt4o
//	  models:
//	    gpt4o: {provider: openai, apiUrl: https://api.openai.com, apiKey: sk-..., model: gpt-4o}
//	  embedding:
//	    default: emb
//	    models: {emb: {provider: openai, apiUrl: ..., apiKey: ..., model: text-embedding-3-small}}
//	  agents:
//	    helper: {model: gpt4o, role: 助手, instruction: ...}
//	  mcp:
//	    servers:
//	      fs: {command: [npx, -y, "@modelcontextprotocol/server-filesystem", /tmp]}
//	      remote: {url: http://localhost:8080/mcp, headers: {Authorization: Bearer x}}
//	    server: {name: my-server, version: 1.0.0, basePath: /mcp}   # 可选对外暴露
package ai

import (
	"sync"

	aicore "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent/simple"
	aiemb "github.com/crazy-airhead/aifei-go/ai/embedding"
	aimcp "github.com/crazy-airhead/aifei-go/ai/mcp"
	"github.com/crazy-airhead/aifei-go/mcp"
)

var (
	defaultReg *Registry
	regMu      sync.RWMutex // storage.defaultMu 同款式样
)

// SetDefault 安装包级默认注册表（nil 清除；Plugin.Stop 自动清除）。
func SetDefault(r *Registry) {
	regMu.Lock()
	defaultReg = r
	regMu.Unlock()
}

// DefaultRegistry 返回已安装的包级默认注册表（未装配为 nil）。
func DefaultRegistry() *Registry {
	regMu.RLock()
	defer regMu.RUnlock()
	return defaultReg
}

// Use 返回默认注册表中具名 chat 模型（空名 = 默认模型；未知名/未装配返回 nil）。
func Use(name string) *aicore.ChatModel {
	r := DefaultRegistry()
	if r == nil {
		return nil
	}
	return r.ChatModel(name)
}

// Default 返回默认 chat 模型（未配置 default 或未装配返回 nil）。
func Default() *aicore.ChatModel { return Use("") }

// ChatModel 返回具名 chat 模型（Use 的显名同义词）。
func ChatModel(name string) *aicore.ChatModel { return Use(name) }

// Embedding 返回具名 embedding 模型（空名 = embedding.default；缺为 nil）。
func Embedding(name string) *aiemb.Model {
	r := DefaultRegistry()
	if r == nil {
		return nil
	}
	return r.Embedding(name)
}

// Agent 返回具名 simple 智能体。
func Agent(name string) *simple.Agent {
	r := DefaultRegistry()
	if r == nil {
		return nil
	}
	return r.Agent(name)
}

// MCP 返回具名 MCP 客户端的工具提供者桥。
func MCP(name string) *aimcp.Provider {
	r := DefaultRegistry()
	if r == nil {
		return nil
	}
	return r.MCP(name)
}

// Server 返回 ai.mcp.server 段装配出的 MCP 服务端（未配置为 nil）。
func Server() *mcp.Server {
	r := DefaultRegistry()
	if r == nil {
		return nil
	}
	return r.Server()
}
