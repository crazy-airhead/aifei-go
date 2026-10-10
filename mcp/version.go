// version.go 协议版本常量与协商（Java ProtocolVersions + LifecycleInitializer
// 的版本校验语义）。
package mcp

import "fmt"

// 协议版本（Java ProtocolVersions.MCP_*）。
const (
	Version20241105 = "2024-11-05" // 旧式 SSE 传输
	Version20250326 = "2025-03-26" // streamable HTTP、outputSchema
	Version20250618 = "2025-06-18" // 基线：elicitation、结构化工具输出
	Version20251125 = "2025-11-25" // 扩展位：Form/Url elicitation
)

// SupportedVersions 本实现全量支持表（升序——Java WebRxStreamable 同款四表；
// 客户端取末位发起握手）。旧式 SSE 传输仅 2024-11-05（P3-c 提供）。
var SupportedVersions = []string{
	Version20241105,
	Version20250326,
	Version20250618,
	Version20251125,
}

// Latest 客户端握手用版本 = 支持表末位（Java doInitialize：
// protocolVersions.get(size-1)，表升序故末位即最新）。
func Latest(versions []string) string {
	if len(versions) == 0 {
		return ""
	}
	return versions[len(versions)-1]
}

// Negotiate 客户端侧版本校验（Java LifecycleInitializer.doInitialize 同款）：
// 服务端 initialize 响应返回的版本必须落在客户端支持表内，否则
// -32602 "Unsupported protocol version"。
func Negotiate(clientVersions []string, serverVersion string) (string, error) {
	for _, v := range clientVersions {
		if v == serverVersion {
			return v, nil
		}
	}
	return "", &JSONRPCError{
		Code:    CodeInvalidParams,
		Message: "Unsupported protocol version",
		Data:    fmt.Sprintf("Unsupported protocol version from the server: %s", serverVersion),
	}
}
