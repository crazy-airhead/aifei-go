// plugin.go aifei.Plugin 装配门面（storage.Plugin 同款式样）：Start 读
// ai.* → NewRegistry → SetDefault；Stop 关闭 MCP 客户端/服务端并清除包级
// 默认（防悬空句柄）。
package ai

import (
	"net/http"

	"github.com/crazy-airhead/aifei-go/aifei"
	"github.com/crazy-airhead/aifei-go/log"
)

// Compile-time assertion that Plugin satisfies aifei.Plugin.
var _ aifei.Plugin = (*Plugin)(nil)

// Plugin integrates the solon-ai assembly with the aifei framework. On Start
// it reads the "ai" subtree from the global config, builds a Registry, and
// installs it as the package-level default so that the top-level ai.Use/
// Default/Agent/MCP helpers work.
type Plugin struct {
	prefix string
	log    log.Logger
	reg    *Registry

	// SecurityValidator 可选 MCP 服务端安全钩子（403 拒绝；须在 Start 前设置——
	// 服务端在 Start 装配）。
	SecurityValidator func(*http.Request) error
}

// NewPlugin creates an ai Plugin that reads its configuration from the global
// config under the given prefix (empty defaults to "ai"). A nil logger falls
// back to log.Default().
func NewPlugin(logger log.Logger, prefix ...string) (*Plugin, error) {
	p := "ai"
	if len(prefix) > 0 && prefix[0] != "" {
		p = prefix[0]
	}
	if logger == nil {
		logger = log.Default()
	}
	return &Plugin{prefix: p, log: logger}, nil
}

// Start loads the ai config from the global config, builds the Registry, and
// registers it as the package default. Assembly never touches the network:
// MCP clients connect lazily on first use, chat/embedding models only build.
func (p *Plugin) Start() error {
	cfg, err := LoadConfig(p.prefix)
	if err != nil {
		return err
	}
	reg, err := NewRegistry(cfg, p.SecurityValidator, p.log)
	if err != nil {
		return err
	}
	p.reg = reg
	SetDefault(reg)
	p.log.Info("ai plugin started, default=%q", reg.defaultModel)
	return nil
}

// Stop closes every MCP client (stdio processes / HTTP sessions) and the MCP
// server if configured, then clears the package default.
func (p *Plugin) Stop() error {
	SetDefault(nil)
	if p.reg == nil {
		return nil
	}
	return p.reg.Close()
}

// Registry returns the Registry built on Start, or nil if Start has not run.
func (p *Plugin) Registry() *Registry { return p.reg }

// Handler mounts the assembled MCP server at its basePath (config
// ai.mcp.server.basePath, default /mcp); requests outside the prefix pass
// through to next. Returns nil when no ai.mcp.server section is configured
// (callers guard with a nil check before wiring server.WithHTTPHandler).
func (p *Plugin) Handler() func(http.Handler) http.Handler {
	if p.reg == nil || p.reg.Server() == nil {
		return nil
	}
	return p.reg.Server().Middleware(p.reg.ServerPath())
}
