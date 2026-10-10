// backend_darwin.go macOS sandbox-exec 后端（对标 MacOSSandboxBackend，
// 逐字移植 sandbox-runtime 的 macos-sandbox-utils.ts）：Seatbelt profile
// 生成（读写规则次序、move-blocking 防绕过、强制拒绝清单）与命令包装。
// 纯字符串生成，无 darwin 专有 API——全平台编译，运行期由 Manager 分发。
package sandbox

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/crazy-airhead/aifei-go/log"
)

// sessionSuffix 进程级随机后缀（TS module-level const 对应物），
// 形如 "_<9 位 base36>_SBX"，用于违规日志标记归因。
var sessionSuffix = func() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "_sandbox00_SBX"
	}
	s := strconv.FormatUint(binary.LittleEndian.Uint64(b[:]), 36)
	for len(s) < 9 {
		s += "0"
	}
	if len(s) > 9 {
		s = s[:9]
	}
	return "_" + s + "_SBX"
}()

// DarwinParams macOS 沙箱包装参数（对标 MacOSSandboxParams）。
type DarwinParams struct {
	// Command 待包装的 shell 命令。
	Command string
	// NeedsNetworkRestriction 是否施加网络限制。
	NeedsNetworkRestriction bool
	// HTTPProxyPort / SocksProxyPort 代理端口（nil = 不放行代理回环）。
	HTTPProxyPort  *int
	SocksProxyPort *int
	// CACertPath TLS 终止 CA 证书路径（注入信任环境变量）。
	CACertPath string
	// AllowUnixSockets / AllowAllUnixSockets unix socket 放行面。
	AllowUnixSockets    []string
	AllowAllUnixSockets *bool
	// AllowLocalBinding 允许本地网络绑定（local ip "*:*"）。
	AllowLocalBinding *bool
	// AllowMachLookup 追加放行的 Mach 服务名（尾部 * 前缀匹配）。
	AllowMachLookup []string
	// ReadConfig / WriteConfig 读写限制（nil = 该面无限制）。
	ReadConfig  *ReadRestriction
	WriteConfig *WriteRestriction
	// AllowPty 允许伪终端。
	AllowPty *bool
	// AllowGitConfig 允许写 .git/config。
	AllowGitConfig *bool
	// EnableWeakerNetworkIsolation 放行 trustd.agent。
	EnableWeakerNetworkIsolation *bool
	// AllowAppleEvents 允许 Apple Events（open/osascript）。
	AllowAppleEvents *bool
	// BinShell shell 二进制名（缺省 bash）。
	BinShell string
}

// DarwinMandatoryDenyPatterns 强制拒绝的写路径模式（不做文件系统扫
// 描——Seatbelt profile 原生支持 glob/regex）：危险文件/目录的 CWD
// 绝对路径 + **/ 递归 glob 双形态；.git/hooks 恒拒；.git/config 按
// allowGitConfig 开关。
func DarwinMandatoryDenyPatterns(allowGitConfig bool) []string {
	cwd, _ := os.Getwd()
	denySet := make([]string, 0, 32)
	seen := make(map[string]bool)
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			denySet = append(denySet, p)
		}
	}

	// 危险文件：CWD 内静态路径 + 子树 glob
	for _, fileName := range DangerousFiles {
		add(filepath.Join(cwd, fileName))
		add("**/" + fileName)
	}

	// 危险目录
	for _, dirName := range getDangerousDirectories() {
		add(filepath.Join(cwd, dirName))
		add("**/" + dirName + "/**")
	}

	// git hooks 恒拒
	add(filepath.Join(cwd, ".git/hooks"))
	add("**/.git/hooks/**")

	// git config 按开关
	if !allowGitConfig {
		add(filepath.Join(cwd, ".git/config"))
		add("**/.git/config")
	}

	return denySet
}

// WrapCommandDarwin 包装命令（对标 wrapCommandWithSandbox）：无任何限
// 制面时原样返回；否则生成 profile 并以 env ... /usr/bin/sandbox-exec
// -p <profile> <shell> -c <command> 形态返回单行 shell 命令。
func WrapCommandDarwin(params *DarwinParams) (string, error) {
	hasReadRestrictions := params.ReadConfig != nil && len(params.ReadConfig.DenyOnly) > 0
	hasWriteRestrictions := params.WriteConfig != nil

	if !params.NeedsNetworkRestriction && !hasReadRestrictions && !hasWriteRestrictions {
		return params.Command, nil
	}

	logTag := generateLogTag(params.Command)
	profile := generateSandboxProfile(params, logTag)

	// 代理环境变量经 env 命令注入（每 VAR=value 独立参数，避开 shell 引用歧义）
	proxyEnvArgs := GenerateProxyEnvVars(params.HTTPProxyPort, params.SocksProxyPort, params.CACertPath)

	// 用户 shell（zsh/bash…别名与快照可用）；缺省 bash
	shellName := params.BinShell
	if shellName == "" {
		shellName = "bash"
	}
	shell, err := exec.LookPath(shellName)
	if err != nil {
		return "", fmt.Errorf("sandbox: Shell '%s' not found in PATH", shellName)
	}

	cmdParts := []string{"env"}
	cmdParts = append(cmdParts, proxyEnvArgs...)
	cmdParts = append(cmdParts,
		"/usr/bin/sandbox-exec", "-p", profile,
		shell, "-c", params.Command)

	wrappedCommand := ShellQuote(cmdParts)

	log.Debug("sandbox darwin: applied restrictions - network: %t, read: %t, write: %t",
		params.HTTPProxyPort != nil || params.SocksProxyPort != nil,
		params.ReadConfig != nil, params.WriteConfig != nil)

	return wrappedCommand, nil
}

// DarwinSandboxProfile 生成 Seatbelt profile（WrapCommandDarwin 的
// 直通面，供审计/测试直接检视；logTag 由命令派生）。
func DarwinSandboxProfile(params *DarwinParams) string {
	return generateSandboxProfile(params, generateLogTag(params.Command))
}

// generateLogTag 违规日志标记：CMD64_<base64>_END<sessionSuffix>。
func generateLogTag(command string) string {
	return "CMD64_" + encodeSandboxedCommand(command) + "_END" + sessionSuffix
}

// getAncestorDirectories 路径的全部祖先目录（不含根）。
// /private/tmp/test/file.txt -> [/private/tmp/test /private/tmp /private]。
func getAncestorDirectories(pathStr string) []string {
	var ancestors []string
	current := filepath.Dir(pathStr)
	for current != "" && current != "/" && current != "." {
		ancestors = append(ancestors, current)
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return ancestors
}

// escapePath Seatbelt profile 的 JSON 风格转义（对标 escapePath =
// TS JSON.stringify：包双引号，转义反斜杠/引号/控制字符）。
func escapePath(pathStr string) string {
	var sb strings.Builder
	sb.Grow(len(pathStr) + 8)
	sb.WriteByte('"')
	for _, c := range pathStr {
		switch c {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if c <= 0x1f {
				sb.WriteString(fmt.Sprintf(`\u%04x`, c))
			} else {
				sb.WriteRune(c)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// generateMoveBlockingRules 文件移动/创建拒绝规则：防绕过（把受限路径
// mv/rname 出界）与防替换（用符号链接占位未存在的受保护路径）。glob
// 模式走 regex + 静态基目录祖先封锁；字面路径走 subpath + 祖先封锁。
func generateMoveBlockingRules(pathPatterns []string, logTag string) []string {
	var rules []string
	ops := []string{"file-write-unlink", "file-write-create"}

	denyMove := func(matcher string) {
		for _, op := range ops {
			rules = append(rules,
				"(deny "+op,
				"  "+matcher,
				`  (with message "`+logTag+`"))`)
		}
	}

	for _, pathPattern := range pathPatterns {
		normalizedPath := NormalizePathForSandbox(pathPattern)

		if ContainsGlobChars(normalizedPath) {
			regexPattern := GlobToRegex(normalizedPath)
			denyMove("(regex " + escapePath(regexPattern) + ")")

			// 静态前缀（去 glob 字符）→ 基目录及其祖先也封锁移动
			staticPrefix := normalizedPath
			if i := strings.IndexAny(normalizedPath, "*?[]"); i >= 0 {
				staticPrefix = normalizedPath[:i]
			}
			if staticPrefix != "" && staticPrefix != "/" {
				baseDir := staticPrefix
				if !strings.HasSuffix(baseDir, "/") {
					baseDir = filepath.Dir(baseDir)
				}
				if baseDir != "" && baseDir != "." {
					denyMove("(literal " + escapePath(baseDir) + ")")
					for _, ancestorDir := range getAncestorDirectories(baseDir) {
						denyMove("(literal " + escapePath(ancestorDir) + ")")
					}
				}
			}
		} else {
			denyMove("(subpath " + escapePath(normalizedPath) + ")")
			for _, ancestorDir := range getAncestorDirectories(normalizedPath) {
				denyMove("(literal " + escapePath(ancestorDir) + ")")
			}
		}
	}

	return rules
}

// generateReadRules 读规则：allow 全量 → deny 大区（glob/regex 或
// subpath）→ deny "/" 时重放行根 inode（dyld 需要路径遍历）→
// allowWithinDeny 重放行 → 目录元数据放行（realpath 中间分量 lstat）→
// 移动封锁 → 写白名单路径的 unlink/create 重放行（读面移动封锁的
// 例外——写面的移动封锁稍后发出，Seatbelt 后发规则胜）。
func generateReadRules(config *ReadRestriction, logTag string, writeAllowPaths []string) []string {
	if config == nil {
		return []string{"(allow file-read*)"}
	}

	var rules []string
	deniesRoot := false

	rules = append(rules, "(allow file-read*)")

	for _, pathPattern := range config.DenyOnly {
		normalizedPath := NormalizePathForSandbox(pathPattern)

		if normalizedPath == "/" {
			deniesRoot = true
		}

		if ContainsGlobChars(normalizedPath) {
			regexPattern := GlobToRegex(normalizedPath)
			rules = append(rules,
				"(deny file-read*",
				"  (regex "+escapePath(regexPattern)+")",
				`  (with message "`+logTag+`"))`)
		} else {
			rules = append(rules,
				"(deny file-read*",
				"  (subpath "+escapePath(normalizedPath)+")",
				`  (with message "`+logTag+`"))`)
		}
	}

	// (subpath "/") 连根 inode 都拒——allowWithinDeny 覆盖不了 "/"，
	// dyld 在 exec 前即中止。重放行根字面量：暴露 ls / 的目录项名，
	// 但子树内容仍拒。
	if deniesRoot {
		rules = append(rules, `(allow file-read* (literal "/"))`)
	}

	// 拒绝区内重放行（后发规则胜）
	for _, pathPattern := range config.AllowWithinDeny {
		normalizedPath := NormalizePathForSandbox(pathPattern)
		if ContainsGlobChars(normalizedPath) {
			regexPattern := GlobToRegex(normalizedPath)
			rules = append(rules,
				"(allow file-read*",
				"  (regex "+escapePath(regexPattern)+")",
				`  (with message "`+logTag+`"))`)
		} else {
			rules = append(rules,
				"(allow file-read*",
				"  (subpath "+escapePath(normalizedPath)+")",
				`  (with message "`+logTag+`"))`)
		}
	}

	// 目录元数据放行：realpath 解析符号链接要 lstat 每个中间目录。
	// 仅元数据（非 readdir/文件内容）。
	if len(config.DenyOnly) > 0 {
		rules = append(rules,
			"(allow file-read-metadata",
			"  (vnode-type DIRECTORY))")
	}

	// 移动封锁（防 mv 出界绕过读限制）
	rules = append(rules, generateMoveBlockingRules(config.DenyOnly, logTag)...)

	// 写白名单路径的 unlink/create 重放行：读面的移动封锁发出了宽泛的
	// (deny file-write-unlink (subpath ...))，而 Seatbelt 中具体操作的
	// deny 不被后续 (allow file-write*) 通配覆盖——不重放行则写白名单
	// 目录内的删除也被拒。denyWithinAllow 例外靠写面稍后的移动封锁
	// 再拒（后发胜，要求读规则先于写规则发出）。
	if len(writeAllowPaths) > 0 {
		for _, pathPattern := range writeAllowPaths {
			normalizedPath := NormalizePathForSandbox(pathPattern)
			for _, op := range []string{"file-write-unlink", "file-write-create"} {
				if ContainsGlobChars(normalizedPath) {
					regexPattern := GlobToRegex(normalizedPath)
					rules = append(rules,
						"(allow "+op,
						"  (regex "+escapePath(regexPattern)+")",
						`  (with message "`+logTag+`"))`)
				} else {
					rules = append(rules,
						"(allow "+op,
						"  (subpath "+escapePath(normalizedPath)+")",
						`  (with message "`+logTag+`"))`)
				}
			}
		}
	}

	return rules
}

// generateWriteRules 写规则：allowOnly 逐路径放行 → denyWithinAllow +
// 强制拒绝清单逐路径拒绝 → 移动封锁。
func generateWriteRules(config *WriteRestriction, logTag string, allowGitConfig bool) []string {
	if config == nil {
		return []string{"(allow file-write*)"}
	}

	var rules []string

	for _, pathPattern := range config.AllowOnly {
		normalizedPath := NormalizePathForSandbox(pathPattern)
		if ContainsGlobChars(normalizedPath) {
			regexPattern := GlobToRegex(normalizedPath)
			rules = append(rules,
				"(allow file-write*",
				"  (regex "+escapePath(regexPattern)+")",
				`  (with message "`+logTag+`"))`)
		} else {
			rules = append(rules,
				"(allow file-write*",
				"  (subpath "+escapePath(normalizedPath)+")",
				`  (with message "`+logTag+`"))`)
		}
	}

	// 用户拒绝 + 强制拒绝（darwin 无需 ripgrep——profile 原生支持 glob）
	denyPaths := make([]string, 0, len(config.DenyWithinAllow)+16)
	denyPaths = append(denyPaths, config.DenyWithinAllow...)
	denyPaths = append(denyPaths, DarwinMandatoryDenyPatterns(allowGitConfig)...)

	for _, pathPattern := range denyPaths {
		normalizedPath := NormalizePathForSandbox(pathPattern)
		if ContainsGlobChars(normalizedPath) {
			regexPattern := GlobToRegex(normalizedPath)
			rules = append(rules,
				"(deny file-write*",
				"  (regex "+escapePath(regexPattern)+")",
				`  (with message "`+logTag+`"))`)
		} else {
			rules = append(rules,
				"(deny file-write*",
				"  (subpath "+escapePath(normalizedPath)+")",
				`  (with message "`+logTag+`"))`)
		}
	}

	// 移动封锁（防 mv 绕过写限制）
	rules = append(rules, generateMoveBlockingRules(denyPaths, logTag)...)

	return rules
}

// generateSandboxProfile 生成完整 Seatbelt profile（对标
// generateSandboxProfile：版本头/进程与 IPC 权限/网络分支/读写规则/
// 可选 pty）。后发规则胜是规则次序的依据（读先于写）。
func generateSandboxProfile(params *DarwinParams, logTag string) string {
	var p []string

	// ---- 头 ----
	p = append(p,
		"(version 1)",
		`(deny default (with message "`+logTag+`"))`,
		"",
		"; LogTag: "+logTag,
		"")

	// ---- 基础权限（Chrome 沙箱策略式样）----
	p = append(p,
		"; Essential permissions - based on Chrome sandbox policy",
		"; Process permissions",
		"(allow process-exec)",
		"(allow process-fork)",
		"(allow process-info* (target same-sandbox))",
		"(allow signal (target same-sandbox))",
		"(allow mach-priv-task-port (target same-sandbox))",
		"",
		"; User preferences",
		"(allow user-preference-read)",
		"")

	// ---- Mach IPC：具名服务（无通配）----
	p = append(p,
		"; Mach IPC - specific services only (no wildcard)",
		"(allow mach-lookup",
		`  (global-name "com.apple.audio.systemsoundserver")`,
		`  (global-name "com.apple.distributed_notifications@Uv3")`,
		`  (global-name "com.apple.FontObjectsServer")`,
		`  (global-name "com.apple.fonts")`,
		`  (global-name "com.apple.logd")`,
		`  (global-name "com.apple.lsd.mapdb")`,
		`  (global-name "com.apple.PowerManagement.control")`,
		`  (global-name "com.apple.system.logger")`,
		`  (global-name "com.apple.system.notification_center")`,
		`  (global-name "com.apple.system.opendirectoryd.libinfo")`,
		`  (global-name "com.apple.system.opendirectoryd.membership")`,
		`  (global-name "com.apple.bsd.dirhelper")`,
		`  (global-name "com.apple.securityd.xpc")`,
		`  (global-name "com.apple.coreservices.launchservicesd")`,
		")",
		"")

	// ---- 弱网络隔离（trustd.agent：Go TLS 证书校验需要）----
	if boolOrFalse(params.EnableWeakerNetworkIsolation) {
		p = append(p,
			"; trustd.agent - needed for Go TLS certificate verification (weaker network isolation)",
			`(allow mach-lookup (global-name "com.apple.trustd.agent"))`,
			"")
	}

	// ---- Apple Events（open/osascript 跨进程）----
	if boolOrFalse(params.AllowAppleEvents) {
		p = append(p,
			"; Apple Events - opt-in; needed for open/osascript to talk to other apps (appleeventsd)",
			"(allow appleevent-send)",
			`(allow mach-lookup (global-name "com.apple.coreservices.appleevents"))`,
			"; Launch Services open requests need the lsopen operation plus, on",
			"; macOS 14/15, coreservicesd and the quarantine resolver - without",
			"; these open fails with -10822 kLSServerCommunicationErr or -54",
			"(allow lsopen)",
			`(allow mach-lookup (global-name "com.apple.CoreServices.coreservicesd"))`,
			`(allow mach-lookup (global-name "com.apple.coreservices.quarantine-resolver"))`,
			"")
	}

	// ---- 用户指定 XPC/Mach 服务 ----
	if len(params.AllowMachLookup) > 0 {
		p = append(p, "; User-specified XPC/Mach services")
		for _, name := range params.AllowMachLookup {
			if strings.HasSuffix(name, "*") {
				p = append(p, "(allow mach-lookup (global-name-prefix "+
					escapePath(strings.TrimSuffix(name, "*"))+"))")
			} else {
				p = append(p, "(allow mach-lookup (global-name "+escapePath(name)+"))")
			}
		}
		p = append(p, "")
	}

	// ---- POSIX IPC ----
	p = append(p,
		"; POSIX IPC - shared memory",
		"(allow ipc-posix-shm)",
		"",
		"; POSIX IPC - semaphores for Python multiprocessing",
		"(allow ipc-posix-sem)",
		"")

	// ---- IOKit ----
	p = append(p,
		"; IOKit - specific operations only",
		"(allow iokit-open",
		`  (iokit-registry-entry-class "IOSurfaceRootUserClient")`,
		`  (iokit-registry-entry-class "RootDomainUserClient")`,
		`  (iokit-user-client-class "IOSurfaceSendRight")`,
		")",
		"",
		"; IOKit properties",
		"(allow iokit-get-properties)",
		"")

	// ---- 系统 socket（无网络面）----
	p = append(p,
		"; Specific safe system-sockets, doesn't allow network access",
		"(allow system-socket (require-all (socket-domain AF_SYSTEM) (socket-protocol 2)))",
		"")

	// ---- sysctl 读（具名清单）----
	p = append(p,
		"; sysctl - specific sysctls only",
		"(allow sysctl-read")
	for _, name := range sysctlReadNames {
		p = append(p, `  (sysctl-name "`+name+`")`)
	}
	for _, prefix := range sysctlReadPrefixes {
		p = append(p, `  (sysctl-name-prefix "`+prefix+`")`)
	}
	p = append(p,
		")",
		"",
		"; V8 thread calculations",
		"(allow sysctl-write",
		`  (sysctl-name "kern.tcsm_enable")`,
		")",
		"")

	// ---- 分布式通知与安全服务 ----
	p = append(p,
		"; Distributed notifications",
		"(allow distributed-notification-post)",
		"",
		"; Specific mach-lookup permissions for security operations",
		`(allow mach-lookup (global-name "com.apple.SecurityServer"))`,
		"")

	// ---- 设备文件 IO ----
	p = append(p,
		"; File I/O on device files",
		`(allow file-ioctl (literal "/dev/null"))`,
		`(allow file-ioctl (literal "/dev/zero"))`,
		`(allow file-ioctl (literal "/dev/random"))`,
		`(allow file-ioctl (literal "/dev/urandom"))`,
		`(allow file-ioctl (literal "/dev/dtracehelper"))`,
		`(allow file-ioctl (literal "/dev/tty"))`,
		"",
		"(allow file-ioctl file-read-data file-write-data",
		"  (require-all",
		`    (literal "/dev/null")`,
		"    (vnode-type CHARACTER-DEVICE)",
		"  )",
		")",
		"")

	// ---- 网络规则 ----
	p = append(p, "; Network")
	if !params.NeedsNetworkRestriction {
		p = append(p, "(allow network*)")
	} else {
		// 本地绑定放行：用 "*:*" 而非 "localhost:*"——现代运行时默认建
		// IPv6 双栈 socket，绑 127.0.0.1 时内核表现为 ::ffff:127.0.0.1，
		// Seatbelt 的 "localhost" 过滤器不认；local ip "*:*" 只匹配本地
		// 端点，外联流量源地址非环回仍被 (deny default) 拒。
		if boolOrFalse(params.AllowLocalBinding) {
			p = append(p,
				`(allow network-bind (local ip "*:*"))`,
				`(allow network-inbound (local ip "*:*"))`,
				`(allow network-outbound (local ip "*:*"))`)
		}

		// unix socket：system-socket 建 fd（无路径上下文）+ bind/connect
		// 走路径过滤——三者缺一不可
		if boolOrFalse(params.AllowAllUnixSockets) {
			p = append(p,
				"(allow system-socket (socket-domain AF_UNIX))",
				`(allow network-bind (local unix-socket (path-regex #\"^/\")))`,
				`(allow network-outbound (remote unix-socket (path-regex #\"^/\")))`)
		} else if len(params.AllowUnixSockets) > 0 {
			p = append(p, "(allow system-socket (socket-domain AF_UNIX))")
			for _, socketPath := range params.AllowUnixSockets {
				normalizedPath := NormalizePathForSandbox(socketPath)
				p = append(p,
					"(allow network-bind (local unix-socket (subpath "+escapePath(normalizedPath)+")))",
					"(allow network-outbound (remote unix-socket (subpath "+escapePath(normalizedPath)+")))")
			}
		}
		// 两者皆未配置：unix socket 被 default deny 拒

		// 代理回环端口放行
		if params.HTTPProxyPort != nil {
			local := `(local ip "localhost:` + strconv.Itoa(*params.HTTPProxyPort) + `"))`
			remote := `(remote ip "localhost:` + strconv.Itoa(*params.HTTPProxyPort) + `"))`
			p = append(p,
				"(allow network-bind "+local,
				"(allow network-inbound "+local,
				"(allow network-outbound "+remote)
		}
		if params.SocksProxyPort != nil {
			local := `(local ip "localhost:` + strconv.Itoa(*params.SocksProxyPort) + `"))`
			remote := `(remote ip "localhost:` + strconv.Itoa(*params.SocksProxyPort) + `"))`
			p = append(p,
				"(allow network-bind "+local,
				"(allow network-inbound "+local,
				"(allow network-outbound "+remote)
		}
	}
	p = append(p, "")

	// ---- 读规则（先于写——denyWithinAllow 的移动封锁要后发覆盖读面
	// 的 unlink/create 重放行）----
	var writeAllowPaths []string
	if params.WriteConfig != nil {
		writeAllowPaths = params.WriteConfig.AllowOnly
	}
	p = append(p, "; File read")
	p = append(p, generateReadRules(params.ReadConfig, logTag, writeAllowPaths)...)
	p = append(p, "")

	// ---- 写规则 ----
	p = append(p, "; File write")
	p = append(p, generateWriteRules(params.WriteConfig, logTag, boolOrFalse(params.AllowGitConfig))...)

	// ---- 伪终端 ----
	if boolOrFalse(params.AllowPty) {
		p = append(p,
			"",
			"; Pseudo-terminal (pty) support",
			"(allow pseudo-tty)",
			"(allow file-ioctl",
			`  (literal "/dev/ptmx")`,
			`  (regex #\"^/dev/ttys\")`,
			")",
			"(allow file-read* file-write*",
			`  (literal "/dev/ptmx")`,
			`  (regex #\"^/dev/ttys\")`,
			")")
	}

	return strings.Join(p, "\n")
}

// sysctlReadNames / sysctlReadPrefixes sysctl 读放行清单（对标
// generateSandboxProfile 的具名清单）。
var sysctlReadNames = []string{
	"hw.activecpu", "hw.busfrequency_compat", "hw.byteorder",
	"hw.cacheconfig", "hw.cachelinesize_compat", "hw.cpufamily",
	"hw.cpufrequency", "hw.cpufrequency_compat", "hw.cputype",
	"hw.l1dcachesize_compat", "hw.l1icachesize_compat",
	"hw.l2cachesize_compat", "hw.l3cachesize_compat", "hw.logicalcpu",
	"hw.logicalcpu_max", "hw.machine", "hw.memsize", "hw.ncpu",
	"hw.nperflevels", "hw.packages", "hw.pagesize_compat", "hw.pagesize",
	"hw.physicalcpu", "hw.physicalcpu_max", "hw.tbfrequency_compat",
	"hw.vectorunit", "kern.argmax", "kern.bootargs", "kern.hostname",
	"kern.maxfiles", "kern.maxfilesperproc", "kern.maxproc", "kern.ngroups",
	"kern.osproductversion", "kern.osrelease", "kern.ostype",
	"kern.osvariant_status", "kern.osversion", "kern.secure_kernel",
	"kern.tcsm_available", "kern.tcsm_enable", "kern.usrstack64",
	"kern.version", "kern.willshutdown", "machdep.cpu.brand_string",
	"machdep.ptrauth_enabled", "security.mac.lockdown_mode_state",
	"sysctl.proc_cputype", "vm.loadavg",
}

var sysctlReadPrefixes = []string{
	"hw.optional.arm", "hw.optional.arm.", "hw.optional.armv8_",
	"hw.perflevel", "kern.proc.all", "kern.proc.pgrp.", "kern.proc.pid.",
	"machdep.cpu.", "net.routetable.",
}

func boolOrFalse(b *bool) bool { return b != nil && *b }
