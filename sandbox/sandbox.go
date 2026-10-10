// Package sandbox Claude Code sandbox-runtime 的 Go 移植（对标
// solon-ai-sandbox；Java 版自身是 TypeScript 的忠实移植）。会话级
// 命令沙箱：macOS Seatbelt（P5-s1）/ linux bwrap（P5-s2）/ windows
// WFP（P5-s4）三平台后端 + 域名过滤面（P5-s3 代理）。
//
// 与 Java 的偏差：
//   - SandboxManager 全静态单例 → Manager 实例（06 契约决策：便于测试
//     与多配置）；JVM 关机钩子不迁——调用方 defer Reset() 即得。
//   - ParentProxyResolver 构造与环境变量探测（HTTP_PROXY/HTTPS_PROXY）
//     留 P5-s3（代理服务器落地时一并接线）。
//   - CommandLookup.which 子进程 which → exec.LookPath（标准库原生）。
//   - SandboxException → error；SandboxLog → 内部 log 模块。
//
// 用法骨架：
//
//	m, err := sandbox.New(sandbox.RuntimeConfig{...})
//	wrapped, err := m.WrapWithSandbox("cargo build", "bash", nil)
//	// 或 spawn 形态：spec, _ := m.WrapWithSandboxArgv(cmd, "", nil)
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

	cfg    *RuntimeConfig
	ask    AskCallback
	store  *ViolationStore
	httpP  *int
	socksP *int
}

// Option Manager 构造选项。
type Option func(*Manager)

// WithAskCallback 注入未命中域名规则时的交互裁决（fail-closed）。
func WithAskCallback(cb AskCallback) Option {
	return func(m *Manager) { m.ask = cb }
}

// WithViolationStore 注入自定义违规存储（缺省内建）。
func WithViolationStore(s *ViolationStore) Option {
	return func(m *Manager) { m.store = s }
}

// New 构造并做依赖检查（缺依赖即报错——Java initialize 的 fail-fast）。
// cfg 允许为零值（最严：写仅缺省路径、网络全拒）。
func New(cfg RuntimeConfig, opts ...Option) (*Manager, error) {
	m := &Manager{cfg: &cfg, store: NewViolationStore(cfg.IgnoreViolations)}
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

// Reset 复位管理器（清配置与端口；linux 桥接清场在 P5-s2 接线）。
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = nil
	m.httpP = nil
	m.socksP = nil
	m.store = nil
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

// CheckDependencies 平台依赖检查。
func (m *Manager) CheckDependencies() *DependencyCheck {
	m.mu.RLock()
	cfg := m.cfg
	m.mu.RUnlock()
	return CheckDependencies(cfg)
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

// SetProxyPorts 注入代理端口（代理服务器由外部/P5-s3 启动后回填）。
func (m *Manager) SetProxyPorts(http, socks int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.httpP = &http
	m.socksP = &socks
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
		return "", ErrLinuxBackendPending
	case Windows:
		return "", errors.New("sandbox: wrapWithSandbox() returns a shell string and is not supported on Windows. Use WrapWithSandboxArgv() and spawn the result with {shell: false}.")
	default:
		return "", fmt.Errorf("%w: %s", ErrUnsupportedPlatform, DetectPlatform())
	}
}

// WrapWithSandboxArgv 包装为 spawn 描述（argv + env）：macOS/linux 为
// [shell, -c, <wrapped>] + 原样系统环境；windows 为专用形态（P5-s4）。
func (m *Manager) WrapWithSandboxArgv(command string, binShell string, customConfig *RuntimeConfig) (*SpawnSpec, error) {
	if DetectPlatform() == Windows {
		return nil, ErrWindowsBackendPending
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

// CleanupAfterCommand 命令结束后的轻量清场（linux bwrap 挂载点残留
// 清理；macOS no-op——P5-s2 接线，签名先行）。ctx 预留给后续卡。
func (m *Manager) CleanupAfterCommand(context.Context) error {
	// linux: LinuxSandboxBackend.cleanupBwrapMountPoints() —— P5-s2
	return nil
}

// buildDarwinParams 由主配置 + 自定义配置合成 darwin 后端参数。
func (m *Manager) buildDarwinParams(command, binShell string, customConfig, mainCfg *RuntimeConfig, httpP, socksP *int) *DarwinParams {
	writeConfig := m.buildWriteConfig(customConfig, mainCfg)
	readConfig := m.buildReadConfig(customConfig, mainCfg)

	// 网络限制：allowedDomains 已定义（即使空 = 全拒）即启用；
	// 代理端口仅在启用时透传（活切换 + 空白名单全拒都靠代理面）
	hasNetworkConfig := false
	if c := customConfig; c != nil && c.Network != nil && c.Network.AllowedDomains != nil {
		hasNetworkConfig = true
	} else if mainCfg != nil && mainCfg.Network != nil && mainCfg.Network.AllowedDomains != nil {
		hasNetworkConfig = true
	}

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
// 路径；尾 glob 后缀剥离）。darwin 保留 glob（Seatbelt 原生支持），
// linux 在此过滤 glob（P5-s2 分支位）。
func (m *Manager) buildWriteConfig(customConfig, mainCfg *RuntimeConfig) *WriteRestriction {
	fs := effectiveFilesystem(customConfig, mainCfg)

	var userAllowWrite, userDenyWrite []string
	if fs != nil {
		userAllowWrite = stripTrailingGlobs(fs.AllowWrite)
		userDenyWrite = stripTrailingGlobs(fs.DenyWrite)
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
