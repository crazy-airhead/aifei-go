// restriction.go 读写/网络限制的中间结构（Manager 由用户配置派生，
// 平台后端消费）。语义注释逐字对标 Java 三件：
//
// 读 = deny-then-allow-back：nil 无限制；空 denyOnly 亦无限制；
// denyOnly=[...] 拒绝这些路径其余放行；allowWithinDeny 在拒绝区内
// 重放行（后发规则胜）。缺省最宽。
//
// 写 = allow-only：nil 无限制；allowOnly=[] 拒绝一切写；allowOnly=[...]
// 仅这些路径可写、denyWithinAllow 例外。缺省最严。
//
// 网络 = allow-only：nil 最严（全拒）；deniedHosts 先于 allowedHosts。
package sandbox

// ReadRestriction 读限制（对标 FsReadRestrictionConfig）。
// nil 与空 denyOnly 都表示「不限制读」。
type ReadRestriction struct {
	// DenyOnly 拒绝读的路径（宽区域，如 /Users）。
	DenyOnly []string
	// AllowWithinDeny 拒绝区内重放行的路径（如 CWD）。
	AllowWithinDeny []string
}

// WriteRestriction 写限制（对标 FsWriteRestrictionConfig）。
// 非 nil 即限制写：空 AllowOnly = 拒绝一切写。
type WriteRestriction struct {
	// AllowOnly 写白名单（含系统缺省写路径）。
	AllowOnly []string
	// DenyWithinAllow 白名单内的拒绝路径（叠加强制拒绝清单）。
	DenyWithinAllow []string
}

// NetworkRestriction 网络限制（对标 NetworkRestrictionConfig）。
// AllowedHosts 空 = 无放行主机。
type NetworkRestriction struct {
	AllowedHosts []string
	DeniedHosts  []string
}

// HostPattern 网络过滤的交互裁决参数（对标 NetworkHostPattern）。
type HostPattern struct {
	Host string
	Port int
}

// AskCallback 未命中域名规则时的交互裁决（fail-closed：返回 false 或
// error 一律拒绝）。对标 SandboxAskCallback。
type AskCallback func(p HostPattern) (bool, error)

// Violation 一次沙箱违规记录。
type Violation struct {
	Category string // 如 file_read / file_write / network
	Message  string
}
