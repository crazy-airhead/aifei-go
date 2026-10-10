// config.go 沙箱运行时配置全家（对标 config 包九件）。
// 全部字段零值即可用：布尔用 *bool 区分「未设置」与 false，
// 列表 nil 与空切片语义等价（网络 allow 列表的「空=全拒」例外
// 见 NetworkRestriction 注释——由 HasNetworkConfig 以 nil 判定）。
package sandbox

// RuntimeConfig 沙箱运行时配置（对标 SandboxRuntimeConfig）。
type RuntimeConfig struct {
	// Network 网络限制（域名白/黑名单、unix socket、本地绑定、代理位）。
	Network *NetworkConfig
	// Filesystem 文件系统限制（读=deny-then-allow-back，写=allow-only）。
	Filesystem *FilesystemConfig
	// IgnoreViolations 按类别忽略的违规消息（子串匹配）。
	IgnoreViolations map[string][]string
	// EnableWeakerNestedSandbox linux 弱化嵌套沙箱（P5-s2 位）。
	EnableWeakerNestedSandbox *bool
	// EnableWeakerNetworkIsolation 允许 trustd.agent（Go TLS 证书校验需要）。
	EnableWeakerNetworkIsolation *bool
	// AllowAppleEvents 允许 Apple Events（open/osascript 跨进程通信）。
	AllowAppleEvents *bool
	// Ripgrep ripgrep 配置（linux glob 展开用；darwin 不需要）。
	Ripgrep *RipgrepConfig
	// MandatoryDenySearchDepth 强制拒绝清单的搜索深度（缺省 3）。
	MandatoryDenySearchDepth *int
	// AllowPty 允许伪终端访问。
	AllowPty *bool
	// Seccomp linux seccomp 配置（P5-s2 位）。
	Seccomp *SeccompConfig
	// BwrapPath linux bwrap 二进制路径覆写（P5-s2 位）。
	BwrapPath string
	// SocatPath linux socat 二进制路径覆写（P5-s2 位）。
	SocatPath string
	// Windows windows 后端配置（甄别组/sublayer/代理端口段）。
	Windows *WindowsConfig
}

// NetworkConfig 网络限制配置（对标 NetworkConfig）。
type NetworkConfig struct {
	// AllowedDomains 域名白名单（*.example.com 匹配子域不匹配裸域）。
	// 非 nil 即启用网络限制：空列表 = 全拒（走代理后由过滤面放行）。
	AllowedDomains []string
	// DeniedDomains 域名黑名单（先于白名单判定）。
	DeniedDomains []string
	// AllowUnixSockets 放行的 unix socket 路径。
	AllowUnixSockets []string
	// AllowAllUnixSockets 放行全部 unix socket。
	AllowAllUnixSockets *bool
	// AllowLocalBinding 允许本地网络绑定（local ip *:*）。
	AllowLocalBinding *bool
	// AllowMachLookup 追加放行的 Mach 服务名（尾部 * 为前缀匹配）。
	AllowMachLookup []string
	// HTTPProxyPort / SocksProxyPort 代理端口（代理服务器由 P5-s3 落地，
	// 端口经 Manager.SetProxyPorts 注入）。
	HTTPProxyPort  *int
	SocksProxyPort *int
	// MitmProxy MITM 代理位（P5-s3）。
	MitmProxy *MitmProxyConfig
	// ParentProxy 父代理（上游企业代理）位（P5-s3）。
	ParentProxy *ParentProxyConfig
	// TLSTerminate TLS 终止位（P5-s3；CA 证书路径会进读白名单）。
	TLSTerminate *TLSTerminateConfig
}

// FilesystemConfig 文件系统限制配置（对标 FilesystemConfig）。
type FilesystemConfig struct {
	DenyRead       []string // 拒绝读路径（glob 支持）
	AllowRead      []string // 拒绝区内重放行的读路径
	AllowWrite     []string // 写白名单（叠加系统缺省写路径）
	DenyWrite      []string // 写白名单内的拒绝路径
	AllowGitConfig *bool    // 允许写 .git/config（缺省拒绝）
}

// SeccompConfig linux seccomp 配置（P5-s2 结构位）。
type SeccompConfig struct {
	ApplyPath string
	Argv0     string
}

// RipgrepConfig ripgrep 配置（linux glob 展开用）。
type RipgrepConfig struct {
	Command string
	Args    []string
	Argv0   string
}

// WindowsConfig windows 后端配置（对标 config/WindowsConfig；组引用与
// sublayer 供 wfp.go 的状态/安装/包装族使用）。
type WindowsConfig struct {
	GroupName       string
	GroupSID        string
	WfpSublayerGUID string
	ProxyPortRange  [2]int // [lo, hi]
}

// MitmProxyConfig MITM 代理配置（P5-s3 结构位）。
type MitmProxyConfig struct {
	SocketPath string
	Domains    []string
}

// ParentProxyConfig 父代理配置（P5-s3 结构位）。
type ParentProxyConfig struct {
	HTTP    string
	HTTPS   string
	NoProxy string
}

// TLSTerminateConfig TLS 终止配置（P5-s3 结构位；CA 证书路径进读白名单）。
type TLSTerminateConfig struct {
	CACertPath string
	CAKeyPath  string
}

// resolveBool 主值优先、缺省回落（Java resolveBoolean 同款）。
func resolveBool(primary, fallback *bool) *bool {
	if primary != nil {
		return primary
	}
	return fallback
}
