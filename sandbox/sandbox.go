// Package sandbox Claude Code sandbox-runtime 的 Go 移植（对标
// solon-ai-sandbox；Java 版自身是 TypeScript 的忠实移植）。会话级
// 命令沙箱：macOS Seatbelt（P5-s1，seatbelt.go）/ linux bwrap（P5-s2，
// bwrap.go）/ windows WFP（P5-s4，wfp.go——srt-win.exe CLI 薄包装）三
// 平台后端 + 域名过滤面 + 代理服务器（P5-s3：HTTP CONNECT 正向代理与
// SOCKS5 + 父代理解析）。
// 后端文件名刻意不带 _darwin/_linux 后缀——Go 的隐式 GOOS 构建约束
// 会把它们从交叉构建里剔除，而 argv/profile 生成是纯字符串操作，
// 全平台编译、全平台可测。
//
// 与 Java 的偏差：
//   - SandboxManager 全静态单例 → Manager 实例（06 契约决策：便于测试
//     与多配置）；JVM 关机钩子不迁——调用方 defer Reset() 即得。
//   - 代理服务器由 Java 的「外部基础设施启动 + setProxyPorts 回填」
//     增量为 Manager.StartProxyServers 便捷接线（端口仍可经
//     SetProxyPorts 外部注入）；代理配置每请求读取（活切换）。
//   - HttpProxyServer 三处上游缺陷修正：handleClient finally 即关 client
//     掐断 CONNECT 隧道、BufferedReader 预读吞请求体、父代理应答的
//     预读字节在 pipe 时丢弃（详见 proxy_http.go 注释）。
//   - CommandLookup.which 子进程 which → exec.LookPath（标准库原生）。
//   - SandboxException → error；SandboxLog → 内部 log 模块。
//   - linux socat 桥懒建：Java 仅在 initialize() 时且端口已设置才建桥
//     （实际流程端口总是后到，桥永远建不起来）；Go 在首次受限包装时
//     按需建桥（ensureLinuxBridge），失败非致命（网络走全拒分支）。
//
// 用法骨架：
//
//	m, err := sandbox.New(sandbox.RuntimeConfig{...})
//	wrapped, err := m.WrapWithSandbox("cargo build", "bash", nil)
//	// 或 spawn 形态：spec, _ := m.WrapWithSandboxArgv(cmd, "", nil)
//	// 受限网络：启动双代理回填端口（HTTP_PROXY/ALL_PROXY 指向它们）
//	httpPort, socksPort, _ := m.StartProxyServers()
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/crazy-airhead/aifei-go/log"
)

// SpawnSpec 包装产物（对标 WindowsSandboxResult 的跨平台形态）：
// Argv 直接交给 exec.Command（shell:false 语义），Env 为完整环境。
type SpawnSpec struct {
	Argv []string
	Env  []string
}

// Manager 沙箱管理器：持有会话配置，派生读写限制结构并分发给平台
// 后端。并发安全（配置热更新走锁）。
type Manager struct {
	mu sync.RWMutex

	cfg         *RuntimeConfig
	ask         AskCallback
	filter      FilterRequestCallback
	parent      *ParentProxyResolver
	store       *ViolationStore
	httpP       *int
	socksP      *int
	httpProxy   *HTTPProxyServer
	socksProxy  *SocksProxyServer
	linuxBridge *LinuxBridgeContext
}

// Option Manager 构造选项。
type Option func(*Manager)

// WithAskCallback 注入未命中域名规则时的交互裁决（fail-closed）。
func WithAskCallback(cb AskCallback) Option {
	return func(m *Manager) { m.ask = cb }
}

// WithFilterRequest 注入代理的每请求过滤回调（StartProxyServers 启动的
// HTTP 代理生效；fail-closed——返回 false 或 panic 即拒）。
func WithFilterRequest(cb FilterRequestCallback) Option {
	return func(m *Manager) { m.filter = cb }
}

// WithViolationStore 注入自定义违规存储（缺省内建）。
func WithViolationStore(s *ViolationStore) Option {
	return func(m *Manager) { m.store = s }
}

// New 构造并做依赖检查（缺依赖即报错——Java initialize 的 fail-fast）。
// cfg 允许为零值（最严：写仅缺省路径、网络全拒）。父代理解析随构造
// 落定（显式 network.parentProxy 优先，否则环境变量存在即环境解析——
// Java initialize 同款）。
func New(cfg RuntimeConfig, opts ...Option) (*Manager, error) {
	m := &Manager{cfg: &cfg, store: NewViolationStore(cfg.IgnoreViolations), parent: resolveParentProxy(&cfg)}
	for _, opt := range opts {
		opt(m)
	}

	deps := m.CheckDependencies()
	if deps.HasErrors() {
		return nil, fmt.Errorf("sandbox: dependencies not available: %s",
			strings.Join(deps.Errors, ", "))
	}
	return m, nil
}

// Reset 复位管理器：关停代理服务器与 linux socat 桥（杀进程 + 删
// socket 文件），清配置与端口，强制清理 bwrap 挂载点残留（Java reset
// 同款）。
func (m *Manager) Reset() {
	m.StopProxyServers()
	m.mu.Lock()
	bridge := m.linuxBridge
	m.linuxBridge = nil
	m.cfg = nil
	m.parent = nil
	m.httpP = nil
	m.socksP = nil
	m.store = nil
	m.mu.Unlock()

	if bridge != nil {
		bridge.Stop()
	}
	if DetectPlatform() == Linux {
		CleanupBwrapMountPoints(true)
	}
	log.Debug("sandbox: reset")
}

// IsSupportedPlatform 当前平台是否支持沙箱。
func (m *Manager) IsSupportedPlatform() bool { return IsSupportedPlatform() }

// IsSandboxingEnabled 是否已初始化（New 后 Reset 前）。
func (m *Manager) IsSandboxingEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg != nil
}

// CheckDependencies 平台依赖检查。windows 追加 srt-win 就绪检查（组 +
// WFP 过滤器——包级 CheckDependencies 对 windows 无可查项，Java
// SandboxManager.checkDependencies 的合并分支同款；偏差：Java 计算了
// sublayerGuid 却调未传入的旧版重载——上游死变量缺陷，Go 传入配置值）。
func (m *Manager) CheckDependencies() *DependencyCheck {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	result := CheckDependencies(cfg)
	if DetectPlatform() == Windows {
		sublayer := ""
		if cfg != nil && cfg.Windows != nil {
			sublayer = cfg.Windows.WfpSublayerGUID
		}
		winDeps := CheckWindowsDependencies(windowsGroupRef(cfg), sublayer)
		result.Errors = append(result.Errors, winDeps.Errors...)
		result.Warnings = append(result.Warnings, winDeps.Warnings...)
	}
	return result
}

// Config 当前配置（可能 nil——零值 Manager 或已 Reset）。
func (m *Manager) Config() *RuntimeConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// UpdateConfig 热更新配置：网络/白名单是活切换（P5-s3 代理按请求读
// 配置，改即生效）；文件系统限制非热更新（darwin 在包装时烧进
// profile）——改 FS 限制需 Reset 后重建。
func (m *Manager) UpdateConfig(cfg RuntimeConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = &cfg
	log.Debug("sandbox: configuration updated")
}

// SetProxyPorts 注入代理端口（外部代理基础设施启动后回填；用
// StartProxyServers 自建时无需调用）。
func (m *Manager) SetProxyPorts(http, socks int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpP = &http
	m.socksP = &socks
}

// ParentProxyResolver 父代理解析器（nil = 未配置——New 时显式配置或
// 代理环境变量二选一命中才有）。
func (m *Manager) ParentProxyResolver() *ParentProxyResolver {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.parent
}

// StartProxyServers 启动 HTTP CONNECT + SOCKS5 双代理（127.0.0.1 临时
// 端口）并回填 ProxyPort/SocksProxyPort——Java 由外部代理基础设施启动
// 后调 setProxyPorts，Go 在此提供便捷接线（有意增量）。过滤面按请求
// 读当前配置与构造注入的回调（UpdateConfig 活切换）。幂等：已启动
// 直接返回现端口。
func (m *Manager) StartProxyServers() (httpPort, socksPort int, err error) {
	m.mu.RLock()
	httpSrv, socksSrv := m.httpProxy, m.socksProxy
	ask, filter, parent := m.ask, m.filter, m.parent
	hp, sp := m.httpP, m.socksP
	m.mu.RUnlock()
	if httpSrv != nil && socksSrv != nil && hp != nil && sp != nil {
		return *hp, *sp, nil
	}

	httpSrv = NewHTTPProxyServerFunc(m.currentConfig, ask, parent, filter)
	socksSrv = NewSocksProxyServerFunc(m.currentConfig, ask, parent)
	httpPort, err = httpSrv.Start(0)
	if err != nil {
		return 0, 0, err
	}
	socksPort, err = socksSrv.Start(0)
	if err != nil {
		httpSrv.Stop()
		return 0, 0, err
	}

	m.mu.Lock()
	m.httpProxy = httpSrv
	m.socksProxy = socksSrv
	m.httpP = &httpPort
	m.socksP = &socksPort
	m.mu.Unlock()
	return httpPort, socksPort, nil
}

// StopProxyServers 关停双代理并清端口（Reset 亦调用）。
func (m *Manager) StopProxyServers() {
	m.mu.Lock()
	httpSrv, socksSrv := m.httpProxy, m.socksProxy
	m.httpProxy, m.socksProxy = nil, nil
	m.httpP, m.socksP = nil, nil
	m.mu.Unlock()

	if httpSrv != nil {
		httpSrv.Stop()
	}
	if socksSrv != nil {
		socksSrv.Stop()
	}
}

// currentConfig 代理过滤面的每请求配置读取（活切换）。
func (m *Manager) currentConfig() *RuntimeConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// ProxyPort / SocksProxyPort 当前代理端口（nil = 未设）。
func (m *Manager) ProxyPort() *int      { m.mu.RLock(); defer m.mu.RUnlock(); return m.httpP }
func (m *Manager) SocksProxyPort() *int { m.mu.RLock(); defer m.mu.RUnlock(); return m.socksP }

// Violations 违规存储（nil = 已 Reset）。
func (m *Manager) Violations() *ViolationStore { return m.store }

// FilterNetwork 网络请求过滤（当前配置 + 构造注入的裁决回调）。
func (m *Manager) FilterNetwork(port int, host string) bool {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	return FilterNetworkRequest(port, host, cfg, m.ask)
}

// WrapWithSandbox 包装命令为受沙箱限制的单行 shell 命令。binShell 可
// 空（缺省 bash）；customConfig 非空时覆盖主配置（文件系统面取其一，
// 布尔面主值优先回落主配置）。写限制恒生效（allow-only 至少含系统
// 缺省写路径——Java buildWriteConfig 同款）。
func (m *Manager) WrapWithSandbox(command string, binShell string, customConfig *RuntimeConfig) (string, error) {
	m.mu.RLock()
	mainCfg := m.cfg
	httpP, socksP := m.httpP, m.socksP
	m.mu.RUnlock()

	switch DetectPlatform() {
	case MacOS:
		return WrapCommandDarwin(m.buildDarwinParams(command, binShell, customConfig, mainCfg, httpP, socksP))
	case Linux:
		return m.wrapLinux(command, binShell, customConfig, mainCfg, httpP, socksP)
	case Windows:
		return "", errors.New("sandbox: wrapWithSandbox() returns a shell string and is not supported on Windows. Use WrapWithSandboxArgv() and spawn the result with {shell: false}.")
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedPlatform, DetectPlatform())
	}
}

// WrapWithSandboxArgv 包装为 spawn 描述（argv + env）：windows 为 srt-win
// exec 专用形态（env 携带完整代理变量集，argv 须 shell:false spawn——
// 这是 windows 唯一受支持的包装方法）；macOS/linux 为 [shell, -c,
// <wrapped>] + 原样系统环境，调用方可跨平台统一 spawn。
func (m *Manager) WrapWithSandboxArgv(command string, binShell string, customConfig *RuntimeConfig) (*SpawnSpec, error) {
	if DetectPlatform() == Windows {
		return m.wrapWindows(command, binShell, customConfig)
	}

	wrapped, err := m.WrapWithSandbox(command, binShell, customConfig)
	if err != nil {
		return nil, err
	}
	shell := binShell
	if shell == "" {
		shell = "/bin/bash"
	}
	return &SpawnSpec{
		Argv: []string{shell, "-c", wrapped},
		Env:  currentEnv(),
	}, nil
}

// wrapWindows windows 分发（Java wrapWithSandboxArgv WINDOWS 分支）：组
// 引用取主配置 windows 段；仅在启用网络限制时透传代理端口（代理环境
// 变量在 WrapCommandWindows 内单一来源生成并并进完整环境）。
func (m *Manager) wrapWindows(command, binShell string, customConfig *RuntimeConfig) (*SpawnSpec, error) {
	m.mu.RLock()
	mainCfg := m.cfg
	httpP, socksP := m.httpP, m.socksP
	m.mu.RUnlock()

	params := &WindowsParams{
		Command:  command,
		Group:    windowsGroupRef(mainCfg),
		BinShell: binShell,
	}
	if hasNetworkRestriction(customConfig, mainCfg) {
		params.HTTPProxyPort = httpP
		params.SocksProxyPort = socksP
	}
	return WrapCommandWindows(params)
}

// windowsGroupRef 主配置 windows 段的组引用（段缺省即 nil 引用——
// groupRefArgs 回落缺省组名）。
func windowsGroupRef(cfg *RuntimeConfig) *WindowsGroupRef {
	if cfg == nil || cfg.Windows == nil {
		return nil
	}
	return &WindowsGroupRef{GroupName: cfg.Windows.GroupName, GroupSID: cfg.Windows.GroupSID}
}

// CleanupAfterCommand 命令结束后的轻量清场：linux 清 bwrap 挂载点残留
// （计数减一，归零才删——并发沙箱安全）；macOS no-op。ctx 预留给后续卡。
func (m *Manager) CleanupAfterCommand(context.Context) error {
	if DetectPlatform() == Linux {
		CleanupBwrapMountPoints(false)
	}
	return nil
}

// wrapLinux linux 分发：合成 LinuxParams 并交 bwrap 后端包装。网络受限
// 且桥可用时透传桥 socket 路径与端口（Java wrapWithSandbox LINUX 分支）。
func (m *Manager) wrapLinux(command, binShell string, customConfig, mainCfg *RuntimeConfig, httpP, socksP *int) (string, error) {
	writeConfig := m.buildWriteConfig(customConfig, mainCfg)
	readConfig := m.buildReadConfig(customConfig, mainCfg)

	// allowedDomains 已定义（即使空 = 全拒）即启用网络限制
	hasNetworkConfig := hasNetworkRestriction(customConfig, mainCfg)

	// 后端专属配置恒取主配置（custom 只覆盖文件系统面与布尔位——Java 同款）
	var (
		weakerNested *bool
		allowAllUnix *bool
		ripgrep      *RipgrepConfig
		depth        *int
		seccomp      *SeccompConfig
		bwrapPath    string
		socatPath    string
	)
	if mainCfg != nil {
		weakerNested = mainCfg.EnableWeakerNestedSandbox
		ripgrep = mainCfg.Ripgrep
		depth = mainCfg.MandatoryDenySearchDepth
		seccomp = mainCfg.Seccomp
		bwrapPath = mainCfg.BwrapPath
		socatPath = mainCfg.SocatPath
		if mainCfg.Network != nil {
			allowAllUnix = mainCfg.Network.AllowAllUnixSockets
		}
	}

	var customGit, mainGit *bool
	if customConfig != nil && customConfig.Filesystem != nil {
		customGit = customConfig.Filesystem.AllowGitConfig
	}
	if mainCfg != nil && mainCfg.Filesystem != nil {
		mainGit = mainCfg.Filesystem.AllowGitConfig
	}

	params := &LinuxParams{
		Command:                   command,
		NeedsNetworkRestriction:   hasNetworkConfig,
		ReadConfig:                readConfig,
		WriteConfig:               writeConfig,
		EnableWeakerNestedSandbox: weakerNested,
		AllowAllUnixSockets:       allowAllUnix,
		BinShell:                  binShell,
		RipgrepConfig:             ripgrep,
		MandatoryDenySearchDepth:  depth,
		AllowGitConfig:            resolveBool(customGit, mainGit),
		SeccompConfig:             seccomp,
		BwrapPath:                 bwrapPath,
		SocatPath:                 socatPath,
	}

	if hasNetworkConfig {
		if bridge := m.ensureLinuxBridge(mainCfg, httpP, socksP); bridge != nil {
			params.HTTPSocketPath = bridge.HTTPSocketPath
			params.SocksSocketPath = bridge.SocksSocketPath
			params.HTTPProxyPort = &bridge.HTTPProxyPort
			params.SocksProxyPort = &bridge.SocksProxyPort
		}
	}
	return WrapCommandLinux(params)
}

// ensureLinuxBridge 取或懒建 socat 桥（偏离 Java：Java 仅在 initialize()
// 且当时端口已设置才建桥——实际流程端口总是后到，桥永远建不起来；Go 在
// 首次受限包装时按需建桥，失败非致命——网络走无桥全拒分支）。
func (m *Manager) ensureLinuxBridge(mainCfg *RuntimeConfig, httpP, socksP *int) *LinuxBridgeContext {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.linuxBridge != nil {
		return m.linuxBridge
	}
	if httpP == nil || socksP == nil {
		return nil
	}
	socatPath := ""
	if mainCfg != nil {
		socatPath = mainCfg.SocatPath
	}
	bridge, err := InitializeLinuxNetworkBridge(*httpP, *socksP, socatPath)
	if err != nil {
		log.Debug("sandbox: linux network bridge init failed: %s", err)
		return nil
	}
	m.linuxBridge = bridge
	log.Debug("sandbox: linux network bridge initialized")
	return bridge
}

// buildDarwinParams 由主配置 + 自定义配置合成 darwin 后端参数。
func (m *Manager) buildDarwinParams(command, binShell string, customConfig, mainCfg *RuntimeConfig, httpP, socksP *int) *DarwinParams {
	writeConfig := m.buildWriteConfig(customConfig, mainCfg)
	readConfig := m.buildReadConfig(customConfig, mainCfg)

	// 网络限制：allowedDomains 已定义（即使空 = 全拒）即启用；
	// 代理端口仅在启用时透传（活切换 + 空白名单全拒都靠代理面）
	hasNetworkConfig := hasNetworkRestriction(customConfig, mainCfg)

	effective := customConfig
	if effective == nil {
		effective = mainCfg
	}

	// unix socket/本地绑定/mach 服务/弱隔离/apple events 恒取主配置
	// （Java wrapWithSandbox 同款——custom 只覆盖文件系统面与布尔位）；
	// allowPty/allowGitConfig 逐字段 resolve（custom 优先回落主配置）。
	var (
		allowUnixSockets    []string
		allowAllUnixSockets *bool
		allowLocalBinding   *bool
		allowMachLookup     []string
		weakerIsolation     *bool
		allowAppleEvents    *bool
	)
	if mainCfg != nil && mainCfg.Network != nil {
		allowUnixSockets = mainCfg.Network.AllowUnixSockets
		allowAllUnixSockets = mainCfg.Network.AllowAllUnixSockets
		allowLocalBinding = mainCfg.Network.AllowLocalBinding
		allowMachLookup = mainCfg.Network.AllowMachLookup
	}
	if mainCfg != nil {
		weakerIsolation = mainCfg.EnableWeakerNetworkIsolation
		allowAppleEvents = mainCfg.AllowAppleEvents
	}

	var customPty, customGit *bool
	if customConfig != nil {
		customPty = customConfig.AllowPty
		if customConfig.Filesystem != nil {
			customGit = customConfig.Filesystem.AllowGitConfig
		}
	}
	var mainPty, mainGit *bool
	if mainCfg != nil {
		mainPty = mainCfg.AllowPty
		if mainCfg.Filesystem != nil {
			mainGit = mainCfg.Filesystem.AllowGitConfig
		}
	}

	var httpPort, socksPort *int
	if hasNetworkConfig {
		httpPort, socksPort = httpP, socksP
	}

	return &DarwinParams{
		Command:                      command,
		NeedsNetworkRestriction:      hasNetworkConfig,
		HTTPProxyPort:                httpPort,
		SocksProxyPort:               socksPort,
		AllowUnixSockets:             allowUnixSockets,
		AllowAllUnixSockets:          allowAllUnixSockets,
		AllowLocalBinding:            allowLocalBinding,
		AllowMachLookup:              allowMachLookup,
		ReadConfig:                   readConfig,
		WriteConfig:                  writeConfig,
		AllowPty:                     resolveBool(customPty, mainPty),
		AllowGitConfig:               resolveBool(customGit, mainGit),
		EnableWeakerNetworkIsolation: weakerIsolation,
		AllowAppleEvents:             allowAppleEvents,
		BinShell:                     binShell,
	}
}

// buildWriteConfig 写限制合成（custom 优先于主配置；叠加系统缺省写
// 路径；尾 glob 后缀剥离）。darwin 保留 glob（Seatbelt 原生支持）；
// linux 剩余 glob 直接丢弃（bwrap 不支持 glob——Java stripWriteGlobs
// 平台分支同款）。
func (m *Manager) buildWriteConfig(customConfig, mainCfg *RuntimeConfig) *WriteRestriction {
	fs := effectiveFilesystem(customConfig, mainCfg)
	linux := DetectPlatform() == Linux

	var userAllowWrite, userDenyWrite []string
	if fs != nil {
		if linux {
			userAllowWrite = StripWriteGlobsForLinux(fs.AllowWrite)
			userDenyWrite = StripWriteGlobsForLinux(fs.DenyWrite)
		} else {
			userAllowWrite = stripTrailingGlobs(fs.AllowWrite)
			userDenyWrite = stripTrailingGlobs(fs.DenyWrite)
		}
	}

	allAllow := append([]string(nil), DefaultWritePaths()...)
	allAllow = append(allAllow, userAllowWrite...)
	return &WriteRestriction{AllowOnly: allAllow, DenyWithinAllow: userDenyWrite}
}

// buildReadConfig 读限制合成（custom 优先；TLS 终止 CA 证书路径进读
// 白名单）。
func (m *Manager) buildReadConfig(customConfig, mainCfg *RuntimeConfig) *ReadRestriction {
	fs := effectiveFilesystem(customConfig, mainCfg)

	var denyRead, allowRead []string
	if fs != nil {
		denyRead = stripTrailingGlobs(fs.DenyRead)
		allowRead = stripTrailingGlobs(fs.AllowRead)
	}

	effective := customConfig
	if effective == nil {
		effective = mainCfg
	}
	if effective != nil && effective.Network != nil && effective.Network.TLSTerminate != nil &&
		effective.Network.TLSTerminate.CACertPath != "" {
		allowRead = append(allowRead, effective.Network.TLSTerminate.CACertPath)
	}

	return &ReadRestriction{DenyOnly: denyRead, AllowWithinDeny: allowRead}
}

func effectiveFilesystem(customConfig, mainCfg *RuntimeConfig) *FilesystemConfig {
	if customConfig != nil && customConfig.Filesystem != nil {
		return customConfig.Filesystem
	}
	if mainCfg != nil && mainCfg.Filesystem != nil {
		return mainCfg.Filesystem
	}
	return nil
}

// hasNetworkRestriction 网络限制是否启用：custom 或主配置任一的
// allowedDomains 已定义（即使空 = 全拒）即启用（Java hasNetworkConfig）。
func hasNetworkRestriction(customConfig, mainCfg *RuntimeConfig) bool {
	if c := customConfig; c != nil && c.Network != nil && c.Network.AllowedDomains != nil {
		return true
	}
	return mainCfg != nil && mainCfg.Network != nil && mainCfg.Network.AllowedDomains != nil
}

// stripTrailingGlobs 尾 "/**" 剥离（子路径语义：macOS subpath 递归匹
// 配，剥离无损失；linux bwrap 需真路径）。
func stripTrailingGlobs(paths []string) []string {
	if paths == nil {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, RemoveTrailingGlobSuffix(p))
	}
	return out
}
