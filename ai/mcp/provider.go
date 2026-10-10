// Package aimcp 把 mcp.Client 桥接为 ai.ToolProvider（对标 solon
// McpClientProvider）：MCP 工具列表映射为 ai.FunctionTool（schema 序列化 +
// returnDirect 注记），CallTool 透传并映射 Content 块，带游标缓存与传输错误
// 重连重试（executeWithRetry）。允许/拒绝清单在 Tools() 收口过滤（Java
// getTools/getToolsActivated 两入口在 Go 合一）。资源/提示桥随 P4 talent
// 体系补（ResourceProvider/PromptProvider 接口落地时）。
package aimcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/log"
	"github.com/crazy-airhead/aifei-go/mcp"
)

// 缓存键前缀（Java cache_prefix_*；Go 只缓存首页全量，游标位恒空串）。
const cachePrefixTools = "getTools:"

// Provider MCP→ai 工具提供者桥（实现 ai.ToolProvider）。
type Provider struct {
	client *mcp.Client

	allowed    map[string]struct{} // 空 = 全部允许
	disallowed map[string]struct{} // 空 = 无禁用

	cacheTTL time.Duration
	mu       sync.Mutex
	cache    map[string]cacheEntry
}

type cacheEntry struct {
	tools []ai.FunctionTool
	at    time.Time
}

// ProviderOption 桥选项。
type ProviderOption func(*Provider)

// WithAllowed 工具白名单（空 = 全部）。
func WithAllowed(names ...string) ProviderOption {
	return func(p *Provider) {
		for _, n := range names {
			p.allowed[n] = struct{}{}
		}
	}
}

// WithDisallowed 工具黑名单（空 = 无禁用）。
func WithDisallowed(names ...string) ProviderOption {
	return func(p *Provider) {
		for _, n := range names {
			p.disallowed[n] = struct{}{}
		}
	}
}

// WithCacheTTL 工具列表缓存时长（默认 30s；≤0 关缓存）。
func WithCacheTTL(d time.Duration) ProviderOption {
	return func(p *Provider) { p.cacheTTL = d }
}

// NewProvider 建桥。注册工具变更消费者：服务端 tools/list_changed 通知到达时
// 由 mcp.Client 自动重拉并回调，此处仅失效缓存（Java onToolsChange 同款）。
func NewProvider(c *mcp.Client, opts ...ProviderOption) *Provider {
	p := &Provider{
		client:     c,
		allowed:    map[string]struct{}{},
		disallowed: map[string]struct{}{},
		cacheTTL:   30 * time.Second,
		cache:      map[string]cacheEntry{},
	}
	for _, o := range opts {
		o(p)
	}
	c.AddToolsChangeConsumer(func([]mcp.Tool) { p.ClearCache() })
	return p
}

// ClearCache 失效全部工具列表缓存。
func (p *Provider) ClearCache() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache = map[string]cacheEntry{}
}

// Client 底层 MCP 客户端（资源/提示等直连操作用）。
func (p *Provider) Client() *mcp.Client { return p.client }

// Tools 实现 ai.ToolProvider：列表（缓存 + 白/黑名单过滤）→ FunctionTool 映射。
func (p *Provider) Tools() []ai.FunctionTool {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	key := cachePrefixTools
	if p.cacheTTL > 0 {
		p.mu.Lock()
		if e, ok := p.cache[key]; ok && time.Since(e.at) < p.cacheTTL {
			tools := e.tools
			p.mu.Unlock()
			return tools
		}
		p.mu.Unlock()
	}

	tools := p.listTools(ctx)

	if p.cacheTTL > 0 {
		p.mu.Lock()
		p.cache[key] = cacheEntry{tools: tools, at: time.Now()}
		p.mu.Unlock()
	}
	return tools
}

func (p *Provider) listTools(ctx context.Context) []ai.FunctionTool {
	list, err := executeWithRetry(p.client, func(c *mcp.Client) ([]mcp.Tool, error) {
		return c.ListAllTools(ctx)
	})
	if err != nil {
		log.Error("aimcp: list tools failed: %v", err)
		return nil
	}

	var tools []ai.FunctionTool
	for i := range list {
		t := &list[i]
		if !p.activated(t.Name) {
			continue
		}
		tools = append(tools, p.toFunctionTool(t))
	}
	return tools
}

// activated 白/黑名单判定（Java getToolsActivated 过滤器：白名单空 = 全放行；
// 黑名单空 = 不剔除）。
func (p *Provider) activated(name string) bool {
	if len(p.allowed) > 0 {
		if _, ok := p.allowed[name]; !ok {
			return false
		}
	}
	if len(p.disallowed) > 0 {
		if _, ok := p.disallowed[name]; !ok {
			return true
		}
		return false
	}
	return true
}

// toFunctionTool MCP Tool → ai.FunctionTool（Java getToolsDo 逐字段映射）。
func (p *Provider) toFunctionTool(t *mcp.Tool) ai.FunctionTool {
	returnDirect := false
	if t.Annotations != nil && t.Annotations.ReturnDirect != nil {
		returnDirect = *t.Annotations.ReturnDirect
	}

	inputSchema := marshalString(t.InputSchema)
	outputSchema := ""
	if t.OutputSchema != nil {
		outputSchema = marshalString(t.OutputSchema)
	}

	d := ai.NewTool(t.Name).
		Title(t.Title).
		Desc(t.Description).
		ReturnDirect(returnDirect).
		InputSchemaJSON(inputSchema).
		OutputSchemaJSON(outputSchema)
	for k, v := range t.Meta {
		d.MetaSet(k, v)
	}
	return d.Handler(func(ctx context.Context, args map[string]any) (any, error) {
		return p.CallTool(ctx, t.Name, args)
	}).Build()
}

// CallTool 调用 MCP 工具并映射为富 ai.ToolResult（块/IsError/Meta 经 ai 核心
// 直通——toolImpl.Call 的 *ToolResult 透传位）。
func (p *Provider) CallTool(ctx context.Context, name string, args map[string]any) (*ai.ToolResult, error) {
	result, err := executeWithRetry(p.client, func(c *mcp.Client) (*mcp.CallToolResult, error) {
		return c.CallTool(ctx, name, args)
	})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		log.Warn("aimcp: tool %q result is error: %d block(s)", name, len(result.Content))
	}
	return p.toToolResult(result), nil
}

// toToolResult MCP CallToolResult → ai.ToolResult（Java toBlock 映射 + Go 增强：
// EmbeddedResource/ResourceLink 映射为 ai.ResourceLink，Java 丢弃）。
func (p *Provider) toToolResult(r *mcp.CallToolResult) *ai.ToolResult {
	out := &ai.ToolResult{IsError: r.IsError, Meta: r.Meta}
	for _, c := range r.Content {
		switch content := c.(type) {
		case *mcp.TextContent:
			text := content.Text
			if r.IsError && !strings.HasPrefix(text, "Error: ") {
				text = "Error: " + text
			}
			out.Blocks = append(out.Blocks, ai.TextBlockOf(text))
		case *mcp.ImageContent:
			if strings.Contains(content.Data, "://") {
				out.Blocks = append(out.Blocks, ai.ImageBlockOfURL(content.Data, content.MimeType))
			} else if data, err := base64.StdEncoding.DecodeString(content.Data); err == nil {
				out.Blocks = append(out.Blocks, ai.ImageBlockOfBase64(data, content.MimeType))
			} else {
				log.Warn("aimcp: image content base64 decode failed: %v", err)
			}
		case *mcp.AudioContent:
			if strings.Contains(content.Data, "://") {
				out.Blocks = append(out.Blocks, ai.AudioBlockOfURL(content.Data, content.MimeType))
			} else if data, err := base64.StdEncoding.DecodeString(content.Data); err == nil {
				out.Blocks = append(out.Blocks, ai.AudioBlockOfBase64(data, content.MimeType))
			} else {
				log.Warn("aimcp: audio content base64 decode failed: %v", err)
			}
		case *mcp.EmbeddedResource:
			rc := content.Resource
			out.Blocks = append(out.Blocks, &ai.ResourceLink{
				Name: rc.ResourceURI(), URI: rc.ResourceURI(), Mime: rc.ResourceMime(),
			})
		case *mcp.ResourceLink:
			out.Blocks = append(out.Blocks, &ai.ResourceLink{
				Name: content.Name, URI: content.URI, Mime: content.MimeType,
			})
		}
	}
	return out
}

// executeWithRetry 传输错误 → Reset 重连一次再试（Java executeWithRetry；Go
// 方法不可带类型参数，故走包级泛型 helper）。业务错误（JSONRPCError）原样
// 上抛不重试。
func executeWithRetry[T any](c *mcp.Client, action func(*mcp.Client) (T, error)) (T, error) {
	result, err := action(c)
	if err == nil || !mcp.IsTransportError(err) {
		return result, err
	}
	log.Warn("aimcp: mcp transmission abnormal, attempt to reconnect...")
	c.Reset()
	result2, err2 := action(c)
	if err2 != nil {
		log.Error("aimcp: retry still fails after reconnecting: %v", err2)
	}
	return result2, err2
}

func marshalString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
