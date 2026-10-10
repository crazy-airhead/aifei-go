// mcp.go MCP 段装配：ai.mcp.servers.*（客户端 + aimcp 工具提供者桥，懒
// 连接——装配不触网）与 ai.mcp.server（可选服务端门面，04-mcp-design §8）。
package ai

import (
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"strings"
	"time"
	"unicode"

	aimcp "github.com/crazy-airhead/aifei-go/ai/mcp"
	"github.com/crazy-airhead/aifei-go/mcp"
)

// buildMCPClients 装配全部 MCP 客户端（stdio 二选一 streamable HTTP）。
// 客户端全部懒连接：装配期不拉进程不握手，错误在首个操作时上抛。
func buildMCPClients(servers map[string]MCPServerConfig) (map[string]*aimcp.Provider, []*mcp.Client, error) {
	providers := make(map[string]*aimcp.Provider, len(servers))
	clients := make([]*mcp.Client, 0, len(servers))
	for _, name := range sortedKeys(servers) {
		sc := servers[name]
		c, err := buildMCPClient(sc)
		if err != nil {
			// 已建客户端就地清场，避免悬挂进程/会话
			for _, built := range clients {
				_ = built.Close()
			}
			return nil, nil, fmt.Errorf("ai: mcp server %q: %w", name, err)
		}
		clients = append(clients, c)

		popts := make([]aimcp.ProviderOption, 0, 3)
		if len(sc.Allow) > 0 {
			popts = append(popts, aimcp.WithAllowed(sc.Allow...))
		}
		if len(sc.Deny) > 0 {
			popts = append(popts, aimcp.WithDisallowed(sc.Deny...))
		}
		if sc.CacheTTLSeconds != 0 { // 0=缺省 30s（不动），负值关闭缓存
			popts = append(popts, aimcp.WithCacheTTL(time.Duration(sc.CacheTTLSeconds)*time.Second))
		}
		providers[name] = aimcp.NewProvider(c, popts...)
	}
	return providers, clients, nil
}

// buildMCPClient 按 MCPServerConfig 建客户端（command=stdio / url=HTTP）。
func buildMCPClient(sc MCPServerConfig) (*mcp.Client, error) {
	if len(sc.Command) > 0 && sc.URL != "" {
		return nil, errors.New("command and url are mutually exclusive")
	}

	name, version := sc.ClientName, sc.ClientVersion
	if name == "" {
		name = "aifei-mcp-client"
	}
	if version == "" {
		version = "1.0.0"
	}
	copts := []mcp.ClientOption{mcp.WithClientInfo(mcp.Implementation{Name: name, Version: version})}
	if len(sc.Headers) > 0 {
		h := http.Header{}
		for k, v := range sc.Headers {
			h.Set(headerKey(k), v)
		}
		copts = append(copts, mcp.WithHeaders(h))
	}
	if sc.RequestTimeoutSeconds > 0 {
		copts = append(copts, mcp.WithRequestTimeout(time.Duration(sc.RequestTimeoutSeconds)*time.Second))
	}

	switch {
	case len(sc.Command) > 0:
		t := mcp.NewStdioTransport(sc.Command[0], sc.Command[1:]...)
		return mcp.NewClient(t.TransportFactory(), copts...), nil
	case sc.URL != "":
		return mcp.DialHTTP(sc.URL, copts...), nil
	default:
		return nil, errors.New("either command (stdio) or url (streamable http) is required")
	}
}

// buildMCPServer 装配可选服务端段（缺省名 aifei-mcp-server/1.0.0）。工具/
// 资源/提示词本体由业务代码经返回的 Server 注册表登记。
func buildMCPServer(sc ServerConfig, validator func(*http.Request) error) *mcp.Server {
	name, version := sc.Name, sc.Version
	if name == "" {
		name = "aifei-mcp-server"
	}
	if version == "" {
		version = "1.0.0"
	}
	opts := make([]mcp.ServerOption, 0, 1)
	if validator != nil {
		opts = append(opts, mcp.WithSecurityValidator(validator))
	}
	return mcp.NewServer(mcp.Implementation{Name: name, Version: version}, opts...)
}

// serverBasePath 服务端段挂载前缀（缺省 /mcp）。
func serverBasePath(sc ServerConfig) string {
	if sc.BasePath != "" {
		return sc.BasePath
	}
	return "/mcp"
}

// headerKey 配置头键 → 规范 MIME 头名。config 层把所有嵌套键归一成
// lowerCamelCase（"X-Token"→"xToken"、"Authorization"→"authorization"），
// 此处在 HTTP 边界做逆变换（驼峰边界补回连字符）再规范化：
// "xToken"→"X-Token"、"authorization"→"Authorization"。
func headerKey(k string) string {
	var b strings.Builder
	for i, r := range k {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('-')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return textproto.CanonicalMIMEHeaderKey(b.String())
}
