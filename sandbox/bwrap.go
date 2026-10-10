// bwrap.go linux bubblewrap 后端（对标 LinuxSandboxBackend，逐字移植
// linux-sandbox-utils.ts）。文件名刻意不带 _linux 后缀——Go 的隐式
// GOOS 构建约束会把 _linux.go 从其他平台的构建里剔除（argv 构造是纯
// 字符串生成，全平台编译可测；真 bwrap/socat 执行自然只在 linux 有
// 意义——同款决策见 seatbelt.go）。
//
// 架构（unix socket 封锁的两段式）：
//   - 外层 bwrap：--unshare-net 隔离网络命名空间 + socat 桥（宿主侧
//     UNIX-LISTEN→TCP:localhost:proxyPort；沙箱侧 3128/1080 监听回连
//     unix socket）+ 文件系统 bind/tmpfs 限制 + --unshare-pid/--proc；
//   - apply-seccomp：嵌套 userns+PID+mount 命名空间内上 seccomp 过滤，
//     封锁用户命令的 AF_UNIX socket 创建（socat 先启动故不受影响）。
//
// 与 Java 的偏差：
//   - JVM 关机钩子不迁（registerExitCleanupHandler）——挂载点清理由调用
//     方 defer Manager.Reset()/CleanupAfterCommand 负责；
//   - socat 桥的懒建：Java 仅在 initialize() 且当时端口已设置才建桥
//     （实际流程端口总是后到，桥永远建不起来）；Go 在 Manager 首次受限
//     包装时按需建桥（见 sandbox.go ensureLinuxBridge），失败非致命；
//   - 进程 isAlive 探测用 done channel（Wait 收口）替代 Java isAlive；
//   - ripgrep 子进程输出走 CombinedOutput 一次收（Java 流式逐行读，
//     结果等价）。
package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// 沙箱内 socat 监听端口（宿主代理端口的沙箱侧对应物）。
const (
	InternalHTTPPort  = 3128
	InternalSocksPort = 1080
	// DefaultMandatoryDenySearchDepth 强制拒绝清单的 ripgrep 搜索深度。
	DefaultMandatoryDenySearchDepth = 3
)

// bwrap 挂载点簿记：deny 路径不存在时 bwrap 会在宿主文件系统上落空
// 文件/目录当挂载点（--ro-bind 的 dest），命令结束后残留（幽灵点文件）。
// activeSandboxCount 计数在飞的沙箱数——清理在最后一个沙箱退出后才执行
// （删挂载点会使并发中的 bwrap 实例的 bind mount 脱钩，deny 失效）。
var (
	bwrapMu            sync.Mutex
	bwrapMountPoints   = map[string]struct{}{}
	activeSandboxCount int
)

func registerMountPoint(p string) {
	bwrapMu.Lock()
	bwrapMountPoints[p] = struct{}{}
	bwrapMu.Unlock()
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// LinuxParams linux 沙箱包装参数（对标 LinuxSandboxParams，18 字段）。
type LinuxParams struct {
	// Command 待包装的 shell 命令。
	Command string
	// NeedsNetworkRestriction 是否需要网络限制（allowedDomains 已定义）。
	NeedsNetworkRestriction bool
	// HTTPSocketPath / SocksSocketPath socat 桥的宿主侧 unix socket 路径
	// （空 = 无桥，--unshare-net 后网络全拒）。
	HTTPSocketPath  string
	SocksSocketPath string
	// HTTPProxyPort / SocksProxyPort 宿主侧代理端口（调试透明用）。
	HTTPProxyPort  *int
	SocksProxyPort *int
	// CACertPath TLS 终止 CA 证书路径（进代理环境变量）。
	CACertPath string
	// ReadConfig / WriteConfig 读写限制（write 恒非 nil——写限制恒生效）。
	ReadConfig  *ReadRestriction
	WriteConfig *WriteRestriction
	// EnableWeakerNestedSandbox 弱化嵌套沙箱（--unshare-user + --bind
	// /proc /proc 替代 --proc /proc；非特权容器兼容）。
	EnableWeakerNestedSandbox *bool
	// AllowAllUnixSockets 放行全部 unix socket（跳过 seccomp 封锁）。
	AllowAllUnixSockets *bool
	// BinShell 包装用 shell（缺省 bash）。
	BinShell string
	// RipgrepConfig ripgrep 配置（强制拒绝清单扫描）。
	RipgrepConfig *RipgrepConfig
	// MandatoryDenySearchDepth 强制拒绝搜索深度（nil = 3）。
	MandatoryDenySearchDepth *int
	// AllowGitConfig 允许写 .git/config（缺省拒绝）。
	AllowGitConfig *bool
	// SeccompConfig apply-seccomp 二进制配置。
	SeccompConfig *SeccompConfig
	// BwrapPath / SocatPath 二进制路径覆写（空 = PATH 解析）。
	BwrapPath string
	SocatPath string
}

// LinuxBridgeContext socat 桥上下文（对标 LinuxNetworkBridgeContext）。
type LinuxBridgeContext struct {
	HTTPSocketPath  string
	SocksSocketPath string
	HTTPProxyPort   int
	SocksProxyPort  int

	httpBridge  *exec.Cmd
	socksBridge *exec.Cmd
	httpDone    chan struct{}
	socksDone   chan struct{}
}

// Stop 关停桥接进程并删除 socket 文件（Java reset 的桥接清场段）。
func (c *LinuxBridgeContext) Stop() {
	for _, pair := range [][2]any{{c.httpBridge, c.httpDone}, {c.socksBridge, c.socksDone}} {
		if cmd, ok := pair[0].(*exec.Cmd); ok && cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		if done, ok := pair[1].(chan struct{}); ok && done != nil {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
			}
		}
	}
	_ = os.Remove(c.HTTPSocketPath)
	_ = os.Remove(c.SocksSocketPath)
}

// InitializeLinuxNetworkBridge 建立宿主侧 socat 桥：unix socket 监听 →
// 转发宿主代理端口（keepalive 族参数 Java 同款）。5 次就绪轮询（i*100ms），
// 任一桥进程死亡或超时即强杀双方并报错。
func InitializeLinuxNetworkBridge(httpProxyPort, socksProxyPort int, socatPath string) (*LinuxBridgeContext, error) {
	socat := socatPath
	if socat == "" {
		socat = "socat"
	}
	socketID, err := generateHexID()
	if err != nil {
		return nil, err
	}
	tmpDir := os.TempDir()
	httpSocketPath := filepath.Join(tmpDir, "claude-http-"+socketID+".sock")
	socksSocketPath := filepath.Join(tmpDir, "claude-socks-"+socketID+".sock")

	target := func(port int) string {
		return "TCP:localhost:" + strconv.Itoa(port) + ",keepalive,keepidle=10,keepintvl=5,keepcnt=3"
	}

	httpCmd := exec.Command(socat,
		"UNIX-LISTEN:"+httpSocketPath+",fork,reuseaddr", target(httpProxyPort))
	httpDone, err := startBridge(httpCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to start HTTP bridge process: %w", err)
	}

	socksCmd := exec.Command(socat,
		"UNIX-LISTEN:"+socksSocketPath+",fork,reuseaddr", target(socksProxyPort))
	socksDone, err := startBridge(socksCmd)
	if err != nil {
		killBridge(httpCmd, httpDone)
		return nil, fmt.Errorf("failed to start SOCKS bridge process: %w", err)
	}

	ctx := &LinuxBridgeContext{
		HTTPSocketPath:  httpSocketPath,
		SocksSocketPath: socksSocketPath,
		HTTPProxyPort:   httpProxyPort,
		SocksProxyPort:  socksProxyPort,
		httpBridge:      httpCmd,
		socksBridge:     socksCmd,
		httpDone:        httpDone,
		socksDone:       socksDone,
	}

	const maxAttempts = 5
	for i := 0; i < maxAttempts; i++ {
		if !bridgeAlive(httpDone) || !bridgeAlive(socksDone) {
			ctx.Stop()
			return nil, errors.New("linux bridge process died unexpectedly")
		}
		if pathExists(httpSocketPath) && pathExists(socksSocketPath) {
			log.Debug("sandbox: linux bridges ready after %d attempts", i+1)
			return ctx, nil
		}
		if i == maxAttempts-1 {
			ctx.Stop()
			return nil, fmt.Errorf("failed to create bridge sockets after %d attempts", maxAttempts)
		}
		time.Sleep(time.Duration(i) * 100 * time.Millisecond)
	}
	ctx.Stop()
	return nil, errors.New("failed to create bridge sockets")
}

// startBridge 启动桥进程并开 Wait 收口 goroutine（done 关闭 = 进程退出；
// stdout/stderr 缺省落 /dev/null——Java redirectErrorStream 后也无人读）。
func startBridge(cmd *exec.Cmd) (chan struct{}, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	return done, nil
}

func bridgeAlive(done chan struct{}) bool {
	select {
	case <-done:
		return false
	default:
		return true
	}
}

func killBridge(cmd *exec.Cmd, done chan struct{}) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
}

func generateHexID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// WrapCommandLinux 用 bwrap 包装命令（对标 wrapCommandWithSandboxLinux）。
// 无任何限制（无网络限制 + 无读 deny + 无写配置）时原样返回。
func WrapCommandLinux(params *LinuxParams) (string, error) {
	command := params.Command
	needsNetworkRestriction := params.NeedsNetworkRestriction

	hasReadRestrictions := params.ReadConfig != nil && len(params.ReadConfig.DenyOnly) > 0
	hasWriteRestrictions := params.WriteConfig != nil
	if !needsNetworkRestriction && !hasReadRestrictions && !hasWriteRestrictions {
		return command, nil
	}

	// 标记本沙箱在飞：cleanupBwrapMountPoints 的删除推迟到计数归零。
	bwrapMu.Lock()
	activeSandboxCount++
	bwrapMu.Unlock()

	wrapped, err := wrapCommandLinuxInner(params, needsNetworkRestriction, hasReadRestrictions, hasWriteRestrictions)
	if err != nil {
		// 回滚计数——抛错的包装，调用方不会再调 cleanup。
		bwrapMu.Lock()
		if activeSandboxCount > 0 {
			activeSandboxCount--
		}
		bwrapMu.Unlock()
		return "", err
	}
	return wrapped, nil
}

func wrapCommandLinuxInner(params *LinuxParams, needsNetworkRestriction, hasReadRestrictions, hasWriteRestrictions bool) (string, error) {
	bwrapArgs := []string{"--new-session", "--die-with-parent"}

	// ===== seccomp（unix socket 封锁） =====
	applySeccompPrefix := ""
	allowAllUnixSockets := params.AllowAllUnixSockets != nil && *params.AllowAllUnixSockets
	if !allowAllUnixSockets {
		var applyPath, argv0 string
		if params.SeccompConfig != nil {
			applyPath, argv0 = params.SeccompConfig.ApplyPath, params.SeccompConfig.Argv0
		}
		prefix, err := ResolveApplySeccompPrefix(applyPath, argv0)
		if err != nil {
			return "", err
		}
		applySeccompPrefix = prefix
		if prefix == "" {
			log.Debug("sandbox: apply-seccomp binary not available - unix socket blocking disabled")
		}
	} else {
		log.Debug("sandbox: skipping seccomp filter - allowAllUnixSockets is enabled")
	}

	// ===== 网络限制 =====
	if needsNetworkRestriction {
		// 网络命名空间整体隔离；桥 socket 存在则 bind 进沙箱 + 代理环境变量
		bwrapArgs = append(bwrapArgs, "--unshare-net")

		httpSocketPath, socksSocketPath := params.HTTPSocketPath, params.SocksSocketPath
		if httpSocketPath != "" && socksSocketPath != "" {
			// 先验 socket 文件仍在（桥进程可能已死）
			if !pathExists(httpSocketPath) {
				return "", fmt.Errorf(
					"linux HTTP bridge socket does not exist: %s. The bridge process may have died. Try reinitializing the sandbox.", httpSocketPath)
			}
			if !pathExists(socksSocketPath) {
				return "", fmt.Errorf(
					"linux SOCKS bridge socket does not exist: %s. The bridge process may have died. Try reinitializing the sandbox.", socksSocketPath)
			}
			bwrapArgs = append(bwrapArgs,
				"--bind", httpSocketPath, httpSocketPath,
				"--bind", socksSocketPath, socksSocketPath)

			// 代理环境变量指向沙箱内 socat 监听（3128/1080 → unix socket → 宿主代理）
			http, socks := InternalHTTPPort, InternalSocksPort
			for _, env := range GenerateProxyEnvVars(&http, &socks, params.CACertPath) {
				if eq := strings.Index(env, "="); eq > 0 {
					bwrapArgs = append(bwrapArgs, "--setenv", env[:eq], env[eq+1:])
				}
			}
			if params.HTTPProxyPort != nil {
				bwrapArgs = append(bwrapArgs, "--setenv", "CLAUDE_CODE_HOST_HTTP_PROXY_PORT", strconv.Itoa(*params.HTTPProxyPort))
			}
			if params.SocksProxyPort != nil {
				bwrapArgs = append(bwrapArgs, "--setenv", "CLAUDE_CODE_HOST_SOCKS_PROXY_PORT", strconv.Itoa(*params.SocksProxyPort))
			}
		}
		// 无桥：--unshare-net 后网络完全封锁
	}

	// ===== 文件系统限制 =====
	depth := DefaultMandatoryDenySearchDepth
	if params.MandatoryDenySearchDepth != nil {
		depth = *params.MandatoryDenySearchDepth
	}
	allowGitConfig := params.AllowGitConfig != nil && *params.AllowGitConfig
	bwrapArgs = append(bwrapArgs, GenerateLinuxFilesystemArgs(
		params.ReadConfig, params.WriteConfig, params.RipgrepConfig, depth, allowGitConfig)...)

	// /dev 恒 bind
	bwrapArgs = append(bwrapArgs, "--dev", "/dev")

	// ===== PID 命名空间隔离 =====
	// 必须在文件系统绑定之后——嵌套 bwrap 才能工作。
	bwrapArgs = append(bwrapArgs, "--unshare-pid")
	if weaker := params.EnableWeakerNestedSandbox != nil && *params.EnableWeakerNestedSandbox; !weaker {
		bwrapArgs = append(bwrapArgs, "--proc", "/proc")
	} else {
		// --unshare-user：bwrap 只在 EUID!=0 时自动加；非特权容器（EUID=0
		// 无 CAP_SYS_ADMIN）会假设有权限直接 clone 而 EPERM——强制走 userns。
		// --bind /proc /proc：apply-seccomp 嵌套 userns 路径要写
		// /proc/self/setgroups，无 --proc 时 --ro-bind / / 留下的 /proc
		// 只读会 EROFS。
		bwrapArgs = append(bwrapArgs, "--unshare-user", "--bind", "/proc", "/proc")
	}

	// ===== 命令尾部 =====
	// 用户的 shell（别名/快照可用）；bwrap 不走 $PATH，须解析全路径。
	shellName := params.BinShell
	if shellName == "" {
		shellName = "bash"
	}
	shell, err := exec.LookPath(shellName)
	if err != nil {
		return "", fmt.Errorf("sandbox: shell '%s' not found in PATH", shellName)
	}
	bwrapArgs = append(bwrapArgs, "--", shell, "-c")

	// 有网络限制走 buildSandboxCommand（socat 先于 seccomp 启动——seccomp
	// 落地后就不能再建 unix socket 了）；否则直接套 apply-seccomp 前缀。
	var tail string
	switch {
	case needsNetworkRestriction && params.HTTPSocketPath != "" && params.SocksSocketPath != "":
		tail = BuildLinuxSandboxCommand(params.HTTPSocketPath, params.SocksSocketPath,
			params.Command, applySeccompPrefix, shell, params.SocatPath)
	case applySeccompPrefix != "":
		tail = applySeccompPrefix + ShellQuote([]string{shell, "-c", params.Command})
	default:
		tail = params.Command
	}
	bwrapArgs = append(bwrapArgs, tail)

	bwrapBin := params.BwrapPath
	if bwrapBin == "" {
		bwrapBin = "bwrap"
	}
	fullCmd := append([]string{bwrapBin}, bwrapArgs...)

	var restrictions []string
	if needsNetworkRestriction {
		restrictions = append(restrictions, "network")
	}
	if hasReadRestrictions || hasWriteRestrictions {
		restrictions = append(restrictions, "filesystem")
	}
	if applySeccompPrefix != "" {
		restrictions = append(restrictions, "seccomp(unix-block)")
	}
	log.Debug("sandbox: wrapped command with bwrap (%s restrictions)", strings.Join(restrictions, ", "))

	return ShellQuote(fullCmd), nil
}

// BuildLinuxSandboxCommand 沙箱内执行的内层脚本（对标 buildSandboxCommand）：
// 3128/1080 socat 监听 → 回连 unix socket；trap 收尾杀后台 socat；
// apply-seccomp 在 socat 之后执行（seccomp 封锁 AF_UNIX 创建）。
// 纯字符串生成故导出全平台可测（同 seatbelt.go 决策）。
func BuildLinuxSandboxCommand(httpSocketPath, socksSocketPath, userCommand, applySeccompPrefix, shell, socatPath string) string {
	shellPath := shell
	if shellPath == "" {
		shellPath = "bash"
	}
	// 宿主文件系统整体 bind 进沙箱，显式 socatPath 沙箱内同路径可用。
	socatArg := socatPath
	if socatArg == "" {
		socatArg = "socat"
	}
	socat := ShellQuoteArg(socatArg)

	socatCmd1 := socat + " TCP-LISTEN:" + strconv.Itoa(InternalHTTPPort) +
		",fork,reuseaddr UNIX-CONNECT:" + httpSocketPath + " >/dev/null 2>&1 &"
	socatCmd2 := socat + " TCP-LISTEN:" + strconv.Itoa(InternalSocksPort) +
		",fork,reuseaddr UNIX-CONNECT:" + socksSocketPath + " >/dev/null 2>&1 &"
	trapCmd := `trap "kill %1 %2 2>/dev/null; exit" EXIT`

	if applySeccompPrefix != "" {
		innerScript := socatCmd1 + "\n" + socatCmd2 + "\n" + trapCmd + "\n" +
			applySeccompPrefix + ShellQuote([]string{shellPath, "-c", userCommand})
		return shellPath + " -c " + ShellQuote([]string{innerScript})
	}
	innerScript := socatCmd1 + "\n" + socatCmd2 + "\n" + trapCmd + "\n" +
		"eval " + ShellQuoteArg(userCommand)
	return shellPath + " -c " + ShellQuote([]string{innerScript})
}

// ResolveApplySeccompPrefix 解析 apply-seccomp 调用形态：独立二进制路径，
// 或按 ARGV0 分发的 multicall 二进制前缀。返回带尾随空格的 shell 前缀
// （调用方在其后接 shellquote([shell,"-c",cmd])）；不可用返回空串。
// argv0 模式下 applyPath 原样使用（不做存在性检查——由调用方保证其在
// bwrap 命名空间内可解析）。
func ResolveApplySeccompPrefix(applyPath, argv0 string) (string, error) {
	if argv0 != "" {
		if applyPath == "" {
			return "", errors.New("sandbox: seccompConfig.argv0 requires seccompConfig.applyPath")
		}
		return "ARGV0=" + ShellQuoteArg(argv0) + " " + ShellQuoteArg(applyPath) + " ", nil
	}
	binary := GetApplySeccompBinaryPath(applyPath)
	if binary == "" {
		return "", nil
	}
	return ShellQuoteArg(binary) + " ", nil
}

// GetApplySeccompBinaryPath 找 apply-seccomp 二进制：显式路径须存在且
// 可执行（否则视为不可用）；缺省走 PATH 查找。
func GetApplySeccompBinaryPath(customPath string) string {
	if customPath != "" {
		if isExecutableFile(customPath) {
			return customPath
		}
		return ""
	}
	if p, err := exec.LookPath("apply-seccomp"); err == nil {
		return p
	}
	return ""
}

// GenerateLinuxFilesystemArgs 生成 bwrap 文件系统 bind/tmpfs 参数（对标
// generateFilesystemArgs，linux-sandbox-utils.ts 逐字移植）：
//
//   - 写限制：--ro-bind / / 打底，白名单路径 --bind 放写；deny（用户
//     denyWithinAllow + 强制拒绝清单）在白名单内 --ro-bind 回只读；
//   - 读限制：deny 目录 --tmpfs 盖掉、deny 文件 --ro-bind /dev/null 掩掉，
//     allowWithinDeny 在被盖目录内 --ro-bind 放回；tmpfs 会冲掉先前的
//     写 bind——逐个补绑；
//   - denyWrite 的 ro-bind 缓冲到 denyRead 处理完再发（denyRead 的 tmpfs
//     若是祖先目录会冲掉它们），且 dest 已被 denyRead 的 /dev/null 掩掉
//     的跳过（后发的 ro-bind 会把掩掉解开）；
//   - 不存在的 deny 路径：有文件祖先（mkdir 不可能）跳过；在写白名单内
//     的，在首个不存在组件处挂 /dev/null（中间组件挂临时空目录——以
//     文件形态出现会卡住按目录遍历的工具）阻断创建，并登记挂载点供
//     cleanupBwrapMountPoints 清理。
func GenerateLinuxFilesystemArgs(
	readConfig *ReadRestriction,
	writeConfig *WriteRestriction,
	ripgrepConfig *RipgrepConfig,
	mandatoryDenySearchDepth int,
	allowGitConfig bool) []string {

	var args []string
	// 写白名单内的规范化路径：denyRead 循环里用来补绑被 tmpfs 冲掉的写。
	var allowedWritePaths []string
	// denyWrite 的 ro-bind 缓冲——denyRead 处理完再发。
	var denyWriteArgs []string

	if writeConfig != nil {
		// 写限制：只读根 + 白名单放写
		args = append(args, "--ro-bind", "/", "/")

		for _, pathPattern := range writeConfig.AllowOnly {
			normalizedPath := NormalizePathForSandbox(pathPattern)

			// /dev/* 由 --dev /dev 兜着
			if strings.HasPrefix(normalizedPath, "/dev/") {
				continue
			}
			if !pathExists(normalizedPath) {
				continue
			}
			// bwrap 会跟随符号链接——指向边界外的 symlink bind 等于把目标
			// 变可写，跳过（尾斜杠先剥——规范化路径可能带，比较会假性不等）。
			resolvedPath, err := filepath.EvalSymlinks(normalizedPath)
			if err != nil {
				continue
			}
			if resolvedPath != strings.TrimRight(normalizedPath, "/") &&
				isSymlinkOutsideBoundary(normalizedPath, resolvedPath) {
				continue
			}
			args = append(args, "--bind", normalizedPath, normalizedPath)
			allowedWritePaths = append(allowedWritePaths, normalizedPath)
		}

		// 写白名单内的 deny（用户 denyWithinAllow + 强制拒绝清单）
		denyPaths := append([]string(nil), writeConfig.DenyWithinAllow...)
		denyPaths = append(denyPaths, LinuxMandatoryDenyPaths(ripgrepConfig, mandatoryDenySearchDepth, allowGitConfig)...)

		// 规范化后去重
		seenDenyWrite := map[string]struct{}{}
		for _, pathPattern := range denyPaths {
			normalizedPath := NormalizePathForSandbox(pathPattern)
			if _, dup := seenDenyWrite[normalizedPath]; dup {
				continue
			}
			seenDenyWrite[normalizedPath] = struct{}{}

			if strings.HasPrefix(normalizedPath, "/dev/") {
				continue
			}

			// 路径中的符号链接：在符号链接处挂 /dev/null 防符号链接替换攻击
			if symlink := findSymlinkInPath(normalizedPath, allowedWritePaths); symlink != "" {
				denyWriteArgs = append(denyWriteArgs, "--ro-bind", "/dev/null", symlink)
				continue
			}

			if !pathExists(normalizedPath) {
				// 已存在的祖先组件是文件（非目录）：mkdir 不可能，deny 无意义
				if hasFileAncestor(normalizedPath) {
					continue
				}
				// 最深已存在祖先目录
				ancestorPath := filepath.Dir(normalizedPath)
				for ancestorPath != "" && ancestorPath != "/" && !pathExists(ancestorPath) {
					ancestorPath = filepath.Dir(ancestorPath)
				}
				// 只保护写白名单内的路径（白名单外的本来就只读）
				ancestorIsWithinAllowedPath := false
				if ancestorPath != "" {
					for _, allowedPath := range allowedWritePaths {
						if strings.HasPrefix(ancestorPath, allowedPath+"/") ||
							ancestorPath == allowedPath ||
							strings.HasPrefix(normalizedPath, allowedPath+"/") {
							ancestorIsWithinAllowedPath = true
							break
						}
					}
				}
				if !ancestorIsWithinAllowedPath {
					continue
				}

				firstNonExistent := findFirstNonExistentComponent(normalizedPath)
				if firstNonExistent != normalizedPath {
					// 中间组件缺失：挂只读空目录而非 /dev/null——以文件形态
					// 出现会卡住按目录遍历的工具（git worktree Fix 2）。
					if emptyDir, err := os.MkdirTemp("", "claude-empty-"); err == nil {
						denyWriteArgs = append(denyWriteArgs, "--ro-bind", emptyDir, firstNonExistent)
						registerMountPoint(firstNonExistent)
					}
				} else {
					denyWriteArgs = append(denyWriteArgs, "--ro-bind", "/dev/null", firstNonExistent)
					registerMountPoint(firstNonExistent)
				}
				continue
			}

			// 已存在路径：只在写白名单内才需要 deny（否则 --ro-bind / / 已只读）
			isWithinAllowedPath := false
			for _, allowedPath := range allowedWritePaths {
				if strings.HasPrefix(normalizedPath, allowedPath+"/") || normalizedPath == allowedPath {
					isWithinAllowedPath = true
					break
				}
			}
			if isWithinAllowedPath {
				denyWriteArgs = append(denyWriteArgs, "--ro-bind", normalizedPath, normalizedPath)
			}
		}
	} else {
		// 无写限制：全放写
		args = append(args, "--bind", "/", "/")
	}

	// ===== 读限制：deny 路径上盖 tmpfs =====
	var readDenyPaths []string
	var readAllowPaths []string
	if readConfig != nil {
		for _, p := range readConfig.AllowWithinDeny {
			readAllowPaths = append(readAllowPaths, NormalizePathForSandbox(p))
		}
	}
	// 被 --ro-bind /dev/null 掩掉的文件集：denyWrite 发射时跳过这些 dest
	// （后发 ro-bind 会把掩掉解开）。
	maskedFiles := map[string]struct{}{}

	// --tmpfs / 会冲掉之前全部挂载（ro-bind /、写 bind、deny bind）——根
	// deny 展开为直接子目录逐个盖。proc/dev/sys 跳过：本函数返回后由调用
	// 方重挂（--dev /dev、--proc /proc）；sys 是内核接口，tmpfs 会弄坏
	// 工具且宿主 /sys 经 ro-bind 已只读。
	if readConfig != nil {
		for _, p := range readConfig.DenyOnly {
			if NormalizePathForSandbox(p) == "/" {
				if entries, err := os.ReadDir("/"); err == nil {
					for _, e := range entries {
						name := e.Name()
						if name != "proc" && name != "dev" && name != "sys" {
							readDenyPaths = append(readDenyPaths, "/"+name)
						}
					}
				}
			} else {
				readDenyPaths = append(readDenyPaths, p)
			}
		}
	}

	// 恒掩 /etc/ssh/ssh_config.d：OrbStack 下沙箱内的属主/权限形态会让
	// ssh 严格校验报 "Bad owner or permissions"。
	if pathExists("/etc/ssh/ssh_config.d") {
		readDenyPaths = append(readDenyPaths, "/etc/ssh/ssh_config.d")
	}

	// 规范化后浅优先排序：祖先目录的 tmpfs 先落，后代文件的 /dev/null 掩
	// 才不会被祖先 tmpfs 冲掉。
	normalizedDenyPaths := make([]string, 0, len(readDenyPaths))
	for _, p := range readDenyPaths {
		normalizedDenyPaths = append(normalizedDenyPaths, NormalizePathForSandbox(p))
	}
	sort.SliceStable(normalizedDenyPaths, func(i, j int) bool {
		return len(strings.Split(normalizedDenyPaths[i], "/")) < len(strings.Split(normalizedDenyPaths[j], "/"))
	})

	for _, normalizedPath := range normalizedDenyPaths {
		if !pathExists(normalizedPath) {
			continue
		}
		denySep := normalizedPath + "/"
		if normalizedPath == "/" {
			denySep = "/"
		}
		fi, err := os.Stat(normalizedPath)
		if err != nil {
			continue
		}
		if fi.IsDir() {
			args = append(args, "--tmpfs", normalizedPath)

			// tmpfs 冲掉了先前落在其下的写 bind——补绑
			for _, writePath := range allowedWritePaths {
				if strings.HasPrefix(writePath, denySep) || writePath == normalizedPath {
					args = append(args, "--bind", writePath, writePath)
				}
			}

			// deny 目录内重放行（allowRead 压过 denyRead）：tmpfs 之上把
			// 允许的子路径 bind 回来
			for _, allowPath := range readAllowPaths {
				if !strings.HasPrefix(allowPath, denySep) && allowPath != normalizedPath {
					continue
				}
				if !pathExists(allowPath) {
					continue
				}
				// 刚在上面补绑过、且覆盖 allowPath 的写 bind 存在则跳过
				// （deny 目录祖先的写 bind 没被冲掉不在补绑之列，其下的
				// allowPath 仍需自己的 ro-bind）。
				coveredByWriteBind := false
				for _, w := range allowedWritePaths {
					if (strings.HasPrefix(w, denySep) || w == normalizedPath) &&
						(allowPath == w || strings.HasPrefix(allowPath, w+"/")) {
						coveredByWriteBind = true
						break
					}
				}
				if coveredByWriteBind {
					continue
				}
				args = append(args, "--ro-bind", allowPath, allowPath)
			}
		} else {
			// 文件：仅精确 allowRead 匹配可解禁——目录 allow 不能解开文件
			// 级 deny（否则 denyRead ['.env'] + allowRead ['.'] 静默丢 .env）。
			if containsStr(readAllowPaths, normalizedPath) {
				continue
			}
			args = append(args, "--ro-bind", "/dev/null", normalizedPath)
			maskedFiles[normalizedPath] = struct{}{}
		}
	}

	// denyWrite 最后发（叠在 denyRead 循环刚补的写 bind 之上），但 dest
	// 已被 denyRead 掩掉的跳过。
	for i := 0; i+2 < len(denyWriteArgs); i += 3 {
		dest := denyWriteArgs[i+2]
		if _, masked := maskedFiles[dest]; masked {
			continue
		}
		args = append(args, denyWriteArgs[i], denyWriteArgs[i+1], dest)
	}

	return args
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// findSymlinkInPath 路径组件中的符号链接若落在写白名单内，返回该链接
// 路径（防符号链接替换攻击——攻击者删链接建真目录）。
func findSymlinkInPath(targetPath string, allowedWritePaths []string) string {
	currentPath := ""
	for _, part := range strings.Split(targetPath, "/") {
		if part == "" {
			continue
		}
		nextPath := currentPath + "/" + part
		if fi, err := os.Lstat(nextPath); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			for _, allowedPath := range allowedWritePaths {
				if strings.HasPrefix(nextPath, allowedPath+"/") || nextPath == allowedPath {
					return nextPath
				}
			}
		}
		currentPath = nextPath
	}
	return ""
}

// hasFileAncestor 已存在的祖先组件是否为文件（git worktree 场景：.git
// 是文件，.git/hooks 永不可能存在——deny 无意义）。
func hasFileAncestor(targetPath string) bool {
	currentPath := ""
	for _, part := range strings.Split(targetPath, "/") {
		if part == "" {
			continue
		}
		nextPath := currentPath + "/" + part
		if !pathExists(nextPath) {
			break
		}
		resolved := nextPath
		if r, err := filepath.EvalSymlinks(nextPath); err == nil {
			resolved = r
		}
		if !pathExists(resolved) {
			break
		}
		if fi, err := os.Stat(resolved); err == nil && !fi.IsDir() {
			return true
		}
		currentPath = nextPath
	}
	return false
}

// findFirstNonExistentComponent 首个不存在的路径组件（在它上面挂
// /dev/null 阻断 mkdir 逐级创建）。
func findFirstNonExistentComponent(targetPath string) string {
	currentPath := ""
	for _, part := range strings.Split(targetPath, "/") {
		if part == "" {
			continue
		}
		nextPath := currentPath + "/" + part
		if !pathExists(nextPath) {
			return nextPath
		}
		currentPath = nextPath
	}
	return targetPath
}

// LinuxMandatoryDenyPaths 强制拒绝路径清单（对标 linuxGetMandatoryDenyPaths，
// 仅 linux）。cwd 下的危险文件/目录 + .git hooks/config（仅 .git 是目录
// 时——worktree 的 .git 是文件，deny 会让 bwrap 失败；.git 不存在时挂载
// 会阻断 git init）+ 单次 ripgrep 调用扫子目录（--max-depth 限深，无需
// 记忆化）。命中目录的归并到目录路径；.git 特例归并到 hooks/ 或 config。
func LinuxMandatoryDenyPaths(ripgrepConfig *RipgrepConfig, maxDepth int, allowGitConfig bool) []string {
	cwd, _ := os.Getwd()
	dangerousDirs := getDangerousDirectories()

	var denyPaths []string
	for _, f := range DangerousFiles {
		denyPaths = append(denyPaths, filepath.Join(cwd, f))
	}
	for _, d := range dangerousDirs {
		denyPaths = append(denyPaths, filepath.Join(cwd, d))
	}

	if fi, err := os.Stat(filepath.Join(cwd, ".git")); err == nil && fi.IsDir() {
		denyPaths = append(denyPaths, filepath.Join(cwd, ".git", "hooks"))
		if !allowGitConfig {
			denyPaths = append(denyPaths, filepath.Join(cwd, ".git", "config"))
		}
	}

	// 一次 ripgrep 调用带上全部 iglob 模式
	var iglobArgs []string
	for _, fileName := range DangerousFiles {
		iglobArgs = append(iglobArgs, "--iglob", fileName)
	}
	for _, dirName := range dangerousDirs {
		iglobArgs = append(iglobArgs, "--iglob", "**/"+dirName+"/**")
	}
	iglobArgs = append(iglobArgs, "--iglob", "**/.git/hooks/**")
	if !allowGitConfig {
		iglobArgs = append(iglobArgs, "--iglob", "**/.git/config")
	}

	rgCommand := "rg"
	if ripgrepConfig != nil && ripgrepConfig.Command != "" {
		rgCommand = ripgrepConfig.Command
	}
	rgArgs := []string{rgCommand, "--files", "--hidden", "--max-depth", strconv.Itoa(maxDepth)}
	rgArgs = append(rgArgs, iglobArgs...)
	rgArgs = append(rgArgs, "-g", "!**/node_modules/**")
	if ripgrepConfig != nil {
		rgArgs = append(rgArgs, ripgrepConfig.Args...)
	}

	cmd := exec.Command(rgArgs[0], rgArgs[1:]...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		// exit 1 = 无命中，正常
		if ee, ok := err.(*exec.ExitError); !ok || (ee.ExitCode() != 0 && ee.ExitCode() != 1) {
			log.Debug("sandbox: ripgrep scan failed: %s", err)
		}
	}
	var matches []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			matches = append(matches, line)
		}
	}

	for _, match := range matches {
		absolutePath := filepath.Join(cwd, match)

		// 命中危险目录内的 → 归并目录路径
		foundDir := false
		allDirNames := append(append([]string(nil), dangerousDirs...), ".git")
		for _, dirName := range allDirNames {
			normalizedDirName := NormalizeCaseForComparison(dirName)
			segments := strings.Split(absolutePath, "/")
			dirIndex := -1
			for i, seg := range segments {
				if NormalizeCaseForComparison(seg) == normalizedDirName {
					dirIndex = i
					break
				}
			}
			if dirIndex != -1 {
				if dirName == ".git" {
					// .git 特例：要 hooks/ 或 config，不是整个 .git 目录
					gitDir := strings.Join(segments[:dirIndex+1], "/")
					if strings.Contains(match, ".git/hooks") {
						denyPaths = append(denyPaths, gitDir+"/hooks")
					} else if strings.Contains(match, ".git/config") {
						denyPaths = append(denyPaths, gitDir+"/config")
					}
				} else {
					denyPaths = append(denyPaths, strings.Join(segments[:dirIndex+1], "/"))
				}
				foundDir = true
				break
			}
		}
		if !foundDir {
			denyPaths = append(denyPaths, absolutePath)
		}
	}

	// 去重
	seen := map[string]struct{}{}
	result := make([]string, 0, len(denyPaths))
	for _, p := range denyPaths {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		result = append(result, p)
	}
	return result
}

// CleanupBwrapMountPoints 清理 bwrap 为不存在 deny 路径落的宿主挂载点
// 残留（幽灵点文件）。每次调用对活动沙箱计数减一（wrap 时加一），
// 归零才真正删——运行中的 bwrap 实例的挂载点被删会使 bind mount 脱钩、
// deny 失效。force=true 无条件删（会话结束语义）。文件删空文件、目录
// 删空目录（非空的留着——可能已被正当使用）。
func CleanupBwrapMountPoints(force bool) {
	bwrapMu.Lock()
	defer bwrapMu.Unlock()

	if !force {
		if activeSandboxCount > 0 {
			activeSandboxCount--
		}
		if activeSandboxCount > 0 {
			log.Debug("sandbox: deferring mount point cleanup - %d sandbox(es) still active", activeSandboxCount)
			return
		}
	} else {
		activeSandboxCount = 0
	}

	for mountPoint := range bwrapMountPoints {
		fi, err := os.Stat(mountPoint)
		if err != nil {
			continue
		}
		if !fi.IsDir() && fi.Size() == 0 {
			if err := os.Remove(mountPoint); err == nil {
				log.Debug("sandbox: cleaned up bwrap mount point (file): %s", mountPoint)
			}
		} else if fi.IsDir() {
			if entries, err := os.ReadDir(mountPoint); err == nil && len(entries) == 0 {
				if err := os.Remove(mountPoint); err == nil {
					log.Debug("sandbox: cleaned up bwrap mount point (dir): %s", mountPoint)
				}
			}
		}
	}
	clear(bwrapMountPoints)
}

// ResetLinuxBackend 复位 bwrap 全局状态（测试用——Java reset() 同款）。
func ResetLinuxBackend() { CleanupBwrapMountPoints(true) }

// LinuxDependencyStatus linux 沙箱依赖明细（对标 LinuxDependencyStatus）。
type LinuxDependencyStatus struct {
	HasBwrap        bool
	HasSocat        bool
	HasSeccompApply bool
}

// GetLinuxDependencyStatus 依赖明细：argv0 模式（apply-seccomp 编进调用
// 方二进制）信任 applyPath 在 bwrap 命名空间内可解析，不做磁盘查找。
func GetLinuxDependencyStatus(cfg *RuntimeConfig) LinuxDependencyStatus {
	var seccomp *SeccompConfig
	var bwrapPath, socatPath string
	if cfg != nil {
		seccomp = cfg.Seccomp
		bwrapPath = cfg.BwrapPath
		socatPath = cfg.SocatPath
	}

	hasSeccompApply := false
	if seccomp != nil && seccomp.Argv0 != "" {
		hasSeccompApply = true
	} else {
		applyPath := ""
		if seccomp != nil {
			applyPath = seccomp.ApplyPath
		}
		hasSeccompApply = GetApplySeccompBinaryPath(applyPath) != ""
	}

	hasBwrap := bwrapPath != "" && isExecutableFile(bwrapPath)
	if bwrapPath == "" {
		_, err := exec.LookPath("bwrap")
		hasBwrap = err == nil
	}
	hasSocat := socatPath != "" && isExecutableFile(socatPath)
	if socatPath == "" {
		_, err := exec.LookPath("socat")
		hasSocat = err == nil
	}

	return LinuxDependencyStatus{HasBwrap: hasBwrap, HasSocat: hasSocat, HasSeccompApply: hasSeccompApply}
}

// StripWriteGlobsForLinux 写路径的 linux glob 语义（Java stripWriteGlobs
// 的 linux 分支）：尾 "/**" 剥离后仍含通配符的路径直接丢弃——bwrap 不支
// 持 glob。darwin 由 Seatbelt 原生支持 glob，不需要过滤（Manager 的
// buildWriteConfig 在 linux 平台自动走此语义）。
func StripWriteGlobsForLinux(paths []string) []string {
	if paths == nil {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		stripped := RemoveTrailingGlobSuffix(p)
		if ContainsGlobChars(stripped) {
			log.Debug("sandbox: skipping glob write pattern on linux: %s", p)
			continue
		}
		out = append(out, stripped)
	}
	return out
}

// LinuxExpandReadPaths 读路径的 linux glob 展开语义（Java getFsReadConfig
// 的 glob 分支）：尾 "/**" 剥离；仍含通配符 → ExpandGlobPattern 展开，
// 展开为空（过宽/基目录不存在）则保留剥尾形态。Java 的 wrapWithSandbox
// 读路径不展开（bwrap 侧按不存在路径静默跳过——上游怪癖，忠实保留）；
// 需要 glob 生效的 linux 调用方应先把 denyRead/allowRead 过此函数。
func LinuxExpandReadPaths(paths []string) []string {
	if paths == nil {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		stripped := RemoveTrailingGlobSuffix(p)
		if ContainsGlobChars(stripped) {
			expanded := ExpandGlobPattern(p)
			if len(expanded) == 0 {
				out = append(out, stripped)
			} else {
				out = append(out, expanded...)
			}
		} else {
			out = append(out, stripped)
		}
	}
	return out
}
