// sandbox_test.go P5-s1 sandbox 首卡黑盒测试：glob→regex 迁移表、路径
// 规范化（~ 展开/符号链接边界/glob 基目录）、shell 引用、代理环境变量、
// 强制拒绝清单与缺省写路径、Seatbelt profile 黄金断言（读写规则次序/
// 网络分支/pty·appleEvents·弱隔离开关）、包装命令形态、Manager 门面
// （依赖检查/热更新/Reset/自定义配置合成）、域名过滤矩阵（fail-closed）、
// 主机校验/规范化、违规存储；darwin 真机 sandbox-exec 读写拒绝/放行
// 回环。
package sandbox_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/sandbox"
)

func intptr(i int) *int    { return &i }
func boolptr(b bool) *bool { return &b }

// ---- glob ----

func TestGlobToRegex(t *testing.T) {
	cases := []struct{ glob, want string }{
		{"**/*.env", `^(.*/)?[^/]*\.env$`},
		{"*.example.com", `^[^/]*\.example\.com$`},
		{"a?c", `^a[^/]c$`},
		{"data[0-9].txt", `^data[0-9]\.txt$`},
		{"foo[1", `^foo\[1$`}, // 未闭合 [ 按字面量
		{"a.b(c)", `^a\.b\(c\)$`},
		{"plain/path", `^plain/path$`},
		{"**/node_modules/**", `^(.*/)?node_modules/.*$`},
	}
	for _, c := range cases {
		if got := sandbox.GlobToRegex(c.glob); got != c.want {
			t.Errorf("GlobToRegex(%q) = %q, want %q", c.glob, got, c.want)
		}
	}
}

func TestGlobCompileMatches(t *testing.T) {
	cases := []struct {
		glob  string
		path  string
		match bool
	}{
		{"**/*.env", ".env", true},
		{"**/*.env", "a/.env", true},
		{"**/*.env", "x/y/.env", true},
		{"**/*.env", "a/b", false},
		{"*.env", "a.env", true},
		{"*.env", "a/b.env", false}, // * 不跨 /
		{"**/node_modules/**", "node_modules/x", true},
		{"**/node_modules/**", "a/node_modules/x", true},
		{"**/node_modules/**", "node_modules", false},
		{"a?c", "abc", true},
		{"a?c", "a/c", false},
	}
	for _, c := range cases {
		if got := sandbox.CompileGlobRegex(c.glob).MatchString(c.path); got != c.match {
			t.Errorf("glob %q match %q = %v, want %v", c.glob, c.path, got, c.match)
		}
	}
}

func TestRemoveTrailingGlobSuffix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/a/**", "/a"},
		{"/**", "/"}, // 剥空 → 根
		{"**", "**"}, // 裸 ** 不带 "/**" 尾——原样返回（Java replaceAll 同款）
		{"/a", "/a"},
		{"/**/**", "/**"},
	}
	for _, c := range cases {
		if got := sandbox.RemoveTrailingGlobSuffix(c.in); got != c.want {
			t.Errorf("RemoveTrailingGlobSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if !sandbox.ContainsGlobChars("a*b") || sandbox.ContainsGlobChars("abc") {
		t.Fatal("ContainsGlobChars wrong")
	}
}

func TestExpandGlobPattern(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"a/x.env": "1", "b/y.env": "2", "c/z.txt": "3",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 结果挂在规范化（符号链接解析后）的基目录下——Java getCanonicalPath 同款
	resolved, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}

	// * 不跨 /：仅匹配 tmp 直接子项（目录本身也参与——Java preVisitDirectory 同款）
	got := sandbox.ExpandGlobPattern(filepath.Join(tmp, "*"))
	want := []string{filepath.Join(resolved, "a"), filepath.Join(resolved, "b"), filepath.Join(resolved, "c")}
	if len(got) != len(want) {
		t.Fatalf("expand = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expand[%d] = %q, want %q (all: %v)", i, got[i], want[i], got)
		}
	}

	// 单层 glob 不深入子目录；跨层要显式写两级
	got = sandbox.ExpandGlobPattern(filepath.Join(tmp, "*", "*.env"))
	want = []string{filepath.Join(resolved, "a", "x.env"), filepath.Join(resolved, "b", "y.env")}
	if len(got) != len(want) {
		t.Fatalf("expand = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expand[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	if sandbox.ExpandGlobPattern("**/x") != nil {
		t.Fatal("too-broad glob must expand to empty")
	}
	if sandbox.ExpandGlobPattern(filepath.Join(tmp, "nope", "*.env")) != nil {
		t.Fatal("nonexistent base dir must expand to empty")
	}
}

// ---- 路径规范化 ----

func TestNormalizePathForSandbox(t *testing.T) {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()

	if got := sandbox.NormalizePathForSandbox("x/y.txt"); got != filepath.Join(cwd, "x/y.txt") {
		t.Errorf("relative = %q", got)
	}
	if got := sandbox.NormalizePathForSandbox("~/z"); got != filepath.Join(home, "z") {
		t.Errorf("~ = %q", got)
	}
	if got := sandbox.NormalizePathForSandbox("./a"); got != filepath.Join(cwd, "a") {
		t.Errorf("./ = %q", got)
	}

	tmp := t.TempDir()
	resolvedTmp, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}

	// 存在的路径：解析符号链接（macOS /var → /private/var 是边界内）
	if err := os.WriteFile(filepath.Join(tmp, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := sandbox.NormalizePathForSandbox(filepath.Join(tmp, "f"))
	if got != filepath.Join(resolvedTmp, "f") && got != filepath.Join(tmp, "f") {
		t.Errorf("existing file normalize = %q (tmp=%s resolved=%s)", got, tmp, resolvedTmp)
	}

	// 逃出边界的符号链接：保持原样
	if err := os.Symlink("/usr/bin", filepath.Join(tmp, "binlink")); err != nil {
		t.Fatal(err)
	}
	if got := sandbox.NormalizePathForSandbox(filepath.Join(tmp, "binlink")); got != filepath.Clean(filepath.Join(tmp, "binlink")) {
		t.Errorf("escaping symlink normalize = %q, want original", got)
	}

	// glob：静态基目录规范化、glob 后缀保留
	got = sandbox.NormalizePathForSandbox(filepath.Join(tmp, "*", ".env"))
	if !strings.HasSuffix(got, "/*/.env") || !strings.HasPrefix(got, resolvedTmp) {
		t.Errorf("glob normalize = %q, want %q prefix", got, resolvedTmp)
	}
}

// ---- shell 引用与代理环境变量 ----

func TestShellQuote(t *testing.T) {
	if got := sandbox.ShellQuoteArg("simple-1.2/PATH:x=y+z"); got != "simple-1.2/PATH:x=y+z" {
		t.Errorf("safe arg quoted: %q", got)
	}
	if got := sandbox.ShellQuoteArg("hello world"); got != `'hello world'` {
		t.Errorf("space arg = %q", got)
	}
	if got := sandbox.ShellQuoteArg("it's"); got != `'it'"'"'s'` {
		t.Errorf("single-quote arg = %q", got)
	}
	if got := sandbox.ShellQuoteArg(""); got != "''" {
		t.Errorf("empty arg = %q", got)
	}
	if got := sandbox.ShellQuote([]string{"a", "b c"}); got != `a 'b c'` {
		t.Errorf("join = %q", got)
	}
}

func TestGenerateProxyEnvVars(t *testing.T) {
	// 无端口：仅基础两变量 + CA 信任面
	vars := sandbox.GenerateProxyEnvVars(nil, nil, "/ca.pem")
	joined := strings.Join(vars, "\n")
	for _, want := range []string{
		"SANDBOX_RUNTIME=1", "TMPDIR=/tmp/claude",
		"NODE_EXTRA_CA_CERTS=/ca.pem", "SSL_CERT_FILE=/ca.pem", "DENO_CERT=/ca.pem",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("base vars missing %q: %v", want, vars)
		}
	}
	if strings.Contains(joined, "HTTP_PROXY") || strings.Contains(joined, "ALL_PROXY") {
		t.Errorf("no-port variant must not carry proxy vars: %v", vars)
	}

	// TMPDIR 环境变量覆写
	t.Setenv("CLAUDE_CODE_TMPDIR", "/custom/tmp")
	vars = sandbox.GenerateProxyEnvVars(nil, nil, "")
	if !containsExact(vars, "TMPDIR=/custom/tmp") {
		t.Errorf("TMPDIR override: %v", vars)
	}

	// HTTP 代理端口
	vars = sandbox.GenerateProxyEnvVars(intptr(8080), nil, "")
	joined = strings.Join(vars, "\n")
	for _, want := range []string{
		"HTTP_PROXY=http://localhost:8080", "HTTPS_PROXY=http://localhost:8080",
		"http_proxy=http://localhost:8080", "https_proxy=http://localhost:8080",
		"NO_PROXY=localhost,127.0.0.1,::1,*.local,.local,169.254.0.0/16,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("http proxy vars missing %q", want)
		}
	}

	// SOCKS 代理端口（+HTTP）
	vars = sandbox.GenerateProxyEnvVars(intptr(8080), intptr(1080), "")
	joined = strings.Join(vars, "\n")
	for _, want := range []string{
		"ALL_PROXY=socks5h://localhost:1080", "all_proxy=socks5h://localhost:1080",
		"FTP_PROXY=socks5h://localhost:1080", "RSYNC_PROXY=localhost:1080",
		"DOCKER_HTTP_PROXY=http://localhost:8080", "DOCKER_HTTPS_PROXY=http://localhost:8080",
		"CLOUDSDK_PROXY_TYPE=http", "CLOUDSDK_PROXY_PORT=8080",
		"GRPC_PROXY=socks5h://localhost:1080",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("socks proxy vars missing %q", want)
		}
	}
	if runtime.GOOS == "darwin" && !strings.Contains(joined, "nc -X 5 -x localhost:1080") {
		t.Errorf("macOS GIT_SSH_COMMAND must use nc -X 5: %v", vars)
	}
}

func containsExact(vars []string, want string) bool {
	for _, v := range vars {
		if v == want {
			return true
		}
	}
	return false
}

// ---- 强制拒绝清单与缺省写路径 ----

func TestMandatoryDenyPatterns(t *testing.T) {
	t.Chdir(t.TempDir())
	cwd, _ := os.Getwd()

	pats := sandbox.DarwinMandatoryDenyPatterns(false)
	joined := "\n" + strings.Join(pats, "\n") + "\n"
	for _, want := range []string{
		"\n" + filepath.Join(cwd, ".bashrc") + "\n",
		"\n**/.bashrc\n",
		"\n" + filepath.Join(cwd, ".mcp.json") + "\n",
		"\n" + filepath.Join(cwd, ".vscode") + "\n",
		"\n**/.vscode/**\n",
		"\n" + filepath.Join(cwd, ".claude/commands") + "\n",
		"\n" + filepath.Join(cwd, ".git/hooks") + "\n",
		"\n**/.git/hooks/**\n",
		"\n" + filepath.Join(cwd, ".git/config") + "\n",
		"\n**/.git/config\n",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("mandatory deny missing %q in %v", want, pats)
		}
	}

	// 放行 .git/config 后该项消失，hooks 仍拒
	pats = sandbox.DarwinMandatoryDenyPatterns(true)
	for _, p := range pats {
		if strings.Contains(p, ".git/config") {
			t.Errorf("allowGitConfig=true still denies .git/config: %v", pats)
		}
	}
	found := false
	for _, p := range pats {
		if strings.Contains(p, ".git/hooks") {
			found = true
		}
	}
	if !found {
		t.Fatal("git hooks must stay denied with allowGitConfig=true")
	}
}

func TestDefaultWritePaths(t *testing.T) {
	home, _ := os.UserHomeDir()
	paths := sandbox.DefaultWritePaths()
	joined := "\n" + strings.Join(paths, "\n") + "\n"
	for _, want := range []string{
		"\n/dev/null\n", "\n/dev/stderr\n", "\n/tmp\n", "\n/private/tmp\n",
		"\n/var/folders\n", "\n" + os.TempDir() + "\n",
		"\n" + home + "/.npm/_logs\n", "\n" + home + "/.claude/debug\n",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("default write paths missing %q", want)
		}
	}
}

// ---- Seatbelt profile ----

func baseParams() *sandbox.DarwinParams {
	return &sandbox.DarwinParams{
		Command: "cargo build",
		ReadConfig: &sandbox.ReadRestriction{
			DenyOnly:        []string{"/Users"},
			AllowWithinDeny: []string{"/Users/dev/project"},
		},
		WriteConfig: &sandbox.WriteRestriction{
			AllowOnly:       []string{"/Users/dev/project"},
			DenyWithinAllow: []string{"/Users/dev/project/target"},
		},
	}
}

func TestDarwinProfile(t *testing.T) {
	profile := sandbox.DarwinSandboxProfile(baseParams())

	if !strings.HasPrefix(profile, "(version 1)\n") {
		t.Fatal("profile must start with (version 1)")
	}
	if !strings.Contains(profile, "; LogTag: CMD64_") || !strings.Contains(profile, "_SBX") {
		t.Fatal("log tag header missing")
	}

	// 基础权限与具名 Mach 服务
	for _, want := range []string{
		"(allow process-exec)", "(allow process-fork)",
		`(global-name "com.apple.securityd.xpc")`,
		`(allow mach-lookup (global-name "com.apple.SecurityServer"))`,
		`(sysctl-name "hw.memsize")`,
		`(sysctl-name-prefix "hw.optional.arm")`,
		`(allow file-ioctl (literal "/dev/null"))`,
	} {
		if !strings.Contains(profile, want) {
			t.Errorf("profile missing %q", want)
		}
	}

	// 无网络限制 → 全放行
	if !strings.Contains(profile, "(allow network*)") {
		t.Fatal("unrestricted network rule missing")
	}

	// 读规则次序：allow 全量 → deny /Users → 重放行 → 目录元数据 → 移动封锁
	readAllow := strings.Index(profile, "; File read\n(allow file-read*)")
	denyUsers := strings.Index(profile, `(deny file-read*
  (subpath "/Users")`)
	allowBack := strings.Index(profile, `(allow file-read*
  (subpath "/Users/dev/project")`)
	meta := strings.Index(profile, "(allow file-read-metadata")
	moveBlock := strings.Index(profile, "(deny file-write-unlink")
	if !(0 <= readAllow && readAllow < denyUsers && denyUsers < allowBack && allowBack < meta && meta < moveBlock) {
		t.Fatalf("read rule order wrong: allow=%d deny=%d back=%d meta=%d move=%d",
			readAllow, denyUsers, allowBack, meta, moveBlock)
	}

	// 写规则：allow 后 deny（含强制拒绝清单的 **/.bashrc——相对 glob 先
	// 被 Join(cwd) 锚定再转 regex，断言取锚定后缀形态）
	allowWrite := strings.Index(profile, `(allow file-write*
  (subpath "/Users/dev/project")`)
	denyTarget := strings.Index(profile, `(deny file-write*
  (subpath "/Users/dev/project/target")`)
	denyBashrc := strings.Index(profile, `/(.*/)?\\.bashrc$")`)
	if !(allowWrite < denyTarget && denyTarget < denyBashrc) {
		t.Fatalf("write rule order wrong: allow=%d deny=%d mandatory=%d", allowWrite, denyTarget, denyBashrc)
	}

	// 写白名单的 unlink/create 重放行（读面移动封锁的例外）
	if !strings.Contains(profile, `(allow file-write-unlink
  (subpath "/Users/dev/project")`) {
		t.Fatal("write-allowed unlink re-allow missing")
	}

	// 可选开关默认关
	for _, absent := range []string{"(allow pseudo-tty)", "trustd.agent", "(allow appleevent-send)"} {
		if strings.Contains(profile, absent) {
			t.Errorf("optional block %q must be absent by default", absent)
		}
	}
}

func TestDarwinProfileDeniesRoot(t *testing.T) {
	profile := sandbox.DarwinSandboxProfile(&sandbox.DarwinParams{
		Command:    "ls",
		ReadConfig: &sandbox.ReadRestriction{DenyOnly: []string{"/"}},
	})
	if !strings.Contains(profile, `(allow file-read* (literal "/"))`) {
		t.Fatal("root inode re-allow missing")
	}
}

func TestDarwinProfileNetwork(t *testing.T) {
	// 无端口网络限制：无 (allow network*)，无代理行
	p := sandbox.DarwinSandboxProfile(&sandbox.DarwinParams{
		Command:                 "curl x",
		NeedsNetworkRestriction: true,
		WriteConfig:             &sandbox.WriteRestriction{},
	})
	if strings.Contains(p, "(allow network*)") {
		t.Fatal("restricted variant must not allow network*")
	}

	// 代理端口 + 本地绑定 + unix socket 双形态
	http, socks := 8080, 1080
	p = sandbox.DarwinSandboxProfile(&sandbox.DarwinParams{
		Command:                 "curl x",
		NeedsNetworkRestriction: true,
		HTTPProxyPort:           &http,
		SocksProxyPort:          &socks,
		AllowLocalBinding:       boolptr(true),
		AllowUnixSockets:        []string{"/var/run/docker.sock"},
		WriteConfig:             &sandbox.WriteRestriction{},
	})
	for _, want := range []string{
		`(allow network-bind (local ip "*:*"))`,
		`(allow network-outbound (remote ip "localhost:8080"))`,
		`(allow network-inbound (local ip "localhost:1080"))`,
		"(allow system-socket (socket-domain AF_UNIX))",
		`(subpath "/var/run/docker.sock")`,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("network profile missing %q", want)
		}
	}

	p = sandbox.DarwinSandboxProfile(&sandbox.DarwinParams{
		Command:                 "x",
		NeedsNetworkRestriction: true,
		AllowAllUnixSockets:     boolptr(true),
		WriteConfig:             &sandbox.WriteRestriction{},
	})
	if !strings.Contains(p, `(path-regex #\"^/\"`) {
		t.Fatal("allow-all unix sockets must use path-regex ^/")
	}
}

func TestDarwinProfileOptionalSwitches(t *testing.T) {
	p := sandbox.DarwinSandboxProfile(&sandbox.DarwinParams{
		Command:                      "open x",
		WriteConfig:                  &sandbox.WriteRestriction{},
		AllowPty:                     boolptr(true),
		EnableWeakerNetworkIsolation: boolptr(true),
		AllowAppleEvents:             boolptr(true),
		AllowMachLookup:              []string{"com.foo*", "com.bar.baz"},
	})
	for _, want := range []string{
		"(allow pseudo-tty)",
		`(allow mach-lookup (global-name "com.apple.trustd.agent"))`,
		"(allow appleevent-send)", "(allow lsopen)",
		`(allow mach-lookup (global-name-prefix "com.foo"))`,
		`(allow mach-lookup (global-name "com.bar.baz"))`,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("optional profile missing %q", want)
		}
	}
}

// ---- 包装命令 ----

func TestWrapCommandDarwin(t *testing.T) {
	// 无任何限制 → 原样返回
	got, err := sandbox.WrapCommandDarwin(&sandbox.DarwinParams{Command: "ls -la"})
	if err != nil || got != "ls -la" {
		t.Fatalf("passthrough = %q err=%v", got, err)
	}

	// 写限制恒在 → env ... sandbox-exec -p <profile> bash -c <cmd>
	wrapped, err := sandbox.WrapCommandDarwin(&sandbox.DarwinParams{
		Command:     "echo hi",
		WriteConfig: &sandbox.WriteRestriction{AllowOnly: []string{"/tmp/x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(wrapped, "env SANDBOX_RUNTIME=1 TMPDIR=") {
		t.Fatalf("wrapped must start with env vars: %q", wrapped[:80])
	}
	if !strings.Contains(wrapped, "/usr/bin/sandbox-exec -p '") {
		t.Fatal("sandbox-exec -p <quoted profile> missing")
	}
	if !strings.Contains(wrapped, " -c 'echo hi'") {
		t.Fatalf("command tail missing: ...%q", wrapped[len(wrapped)-40:])
	}

	// shell 缺失 → 报错
	_, err = sandbox.WrapCommandDarwin(&sandbox.DarwinParams{
		Command:     "x",
		BinShell:    "no-such-shell-xyz-123",
		WriteConfig: &sandbox.WriteRestriction{},
	})
	if err == nil || !strings.Contains(err.Error(), "not found in PATH") {
		t.Fatalf("shell-not-found error = %v", err)
	}
}

// ---- Manager ----

func TestManagerBasics(t *testing.T) {
	if !sandbox.IsSupportedPlatform() {
		t.Skip("platform without sandbox support")
	}

	m, err := sandbox.New(sandbox.RuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsSupportedPlatform() || !m.IsSandboxingEnabled() {
		t.Fatal("manager state wrong after New")
	}
	if deps := m.CheckDependencies(); deps.HasErrors() {
		t.Fatalf("darwin has no external deps, got errors %v", deps.Errors)
	}

	// 热更新 / 端口注入 / 违规面
	cfg2 := sandbox.RuntimeConfig{}
	cfg2.Filesystem = &sandbox.FilesystemConfig{DenyRead: []string{"/x"}}
	m.UpdateConfig(cfg2)
	if m.Config() == nil || m.Config().Filesystem.DenyRead[0] != "/x" {
		t.Fatal("UpdateConfig not live")
	}
	m.SetProxyPorts(8080, 1080)
	if m.ProxyPort() == nil || *m.ProxyPort() != 8080 || *m.SocksProxyPort() != 1080 {
		t.Fatal("SetProxyPorts wrong")
	}
	if m.Violations() == nil {
		t.Fatal("violation store must be built-in")
	}
	m.Violations().Record("file_read", "denied /etc/passwd")
	if !m.Violations().HasViolations() {
		t.Fatal("violation not recorded")
	}

	// Reset 清场
	m.Reset()
	if m.IsSandboxingEnabled() || m.Config() != nil || m.ProxyPort() != nil {
		t.Fatal("Reset must clear state")
	}
}

func TestManagerWrap(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin backend dispatch")
	}
	m, err := sandbox.New(sandbox.RuntimeConfig{})
	if err != nil {
		t.Fatal(err)
	}

	// 零配置也施加写限制（allow-only 至少含缺省写路径）
	wrapped, err := m.WrapWithSandbox("echo hi", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wrapped, "sandbox-exec") {
		t.Fatal("zero config must still wrap (write restrictions)")
	}

	// 自定义配置覆盖主配置的文件系统面
	custom := sandbox.RuntimeConfig{}
	custom.Filesystem = &sandbox.FilesystemConfig{DenyRead: []string{"/customsecret"}}
	m.UpdateConfig(sandbox.RuntimeConfig{})
	m.Config().Filesystem = &sandbox.FilesystemConfig{DenyRead: []string{"/mainsecret"}}
	wrapped, err = m.WrapWithSandbox("echo hi", "", &custom)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wrapped, "/customsecret") || strings.Contains(wrapped, "/mainsecret") {
		t.Fatal("custom config must win over main config")
	}

	// 布尔位逐字段 resolve：custom 未设 → 回落主配置
	main := sandbox.RuntimeConfig{}
	main.AllowPty = boolptr(true)
	m.UpdateConfig(main)
	wrapped, err = m.WrapWithSandbox("echo hi", "", &custom)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wrapped, "(allow pseudo-tty)") {
		t.Fatal("allowPty must resolve from main config")
	}

	// 网络面：allowedDomains 非 nil 即限制（空 = 全拒）
	netCfg := sandbox.RuntimeConfig{}
	netCfg.Network = &sandbox.NetworkConfig{AllowedDomains: []string{}}
	m.UpdateConfig(netCfg)
	wrapped, err = m.WrapWithSandbox("echo hi", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(wrapped, "(allow network*)") {
		t.Fatal("empty allowedDomains must restrict network")
	}

	// spawn 形态
	spec, err := m.WrapWithSandboxArgv("echo hi", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Argv) != 3 || spec.Argv[1] != "-c" || spec.Argv[0] != "/bin/bash" {
		t.Fatalf("argv = %v", spec.Argv)
	}
	if len(spec.Env) == 0 {
		t.Fatal("env must pass through")
	}
}

// ---- 域名过滤 ----

func TestMatchesDomainPattern(t *testing.T) {
	cases := []struct {
		host, pattern string
		match         bool
	}{
		{"sub.example.com", "*.example.com", true},
		{"a.b.example.com", "*.example.com", true},
		{"example.com", "*.example.com", false},
		{"notexample.com", "*.example.com", false},
		{"1.2.3.4", "*.3.4", false}, // 通配永不匹配 IP 字面量
		{"::1", "*.1", false},       // 同上
		{"EXAMPLE.com", "example.com", true},
		{"example.com", "EXAMPLE.com", true},
		{"other.com", "example.com", false},
	}
	for _, c := range cases {
		if got := sandbox.MatchesDomainPattern(c.host, c.pattern); got != c.match {
			t.Errorf("MatchesDomainPattern(%q, %q) = %v, want %v", c.host, c.pattern, got, c.match)
		}
	}
}

func TestNetworkFilter(t *testing.T) {
	// 无配置 → 拒
	if sandbox.FilterNetworkRequest(443, "example.com", nil, nil) {
		t.Fatal("nil config must deny")
	}

	cfg := &sandbox.RuntimeConfig{Network: &sandbox.NetworkConfig{
		AllowedDomains: []string{"example.com", "*.api.dev"},
		DeniedDomains:  []string{"evil.example.com"},
	}}

	// 畸形主机 → 拒
	for _, bad := range []string{"", "bad%host", strings.Repeat("a", 256)} {
		if sandbox.FilterNetworkRequest(80, bad, cfg, nil) {
			t.Errorf("malformed host %q must deny", bad)
		}
	}

	// 黑名单先于白名单
	if sandbox.FilterNetworkRequest(443, "evil.example.com", cfg, nil) {
		t.Fatal("denied domain must deny even if allowlist matches")
	}
	// 白名单放行（含规范形态：尾点/大小写）
	if !sandbox.FilterNetworkRequest(443, "example.com", cfg, nil) ||
		!sandbox.FilterNetworkRequest(443, "Example.COM.", cfg, nil) ||
		!sandbox.FilterNetworkRequest(443, "v2.api.dev", cfg, nil) {
		t.Fatal("allowlisted domains must pass")
	}
	// 通配不匹配 IP 字面量 → 落到无命中
	if sandbox.FilterNetworkRequest(80, "1.2.3.4", cfg, nil) {
		t.Fatal("unlisted host without callback must deny")
	}

	// 交互裁决：true 放行；false/出错拒绝（fail-closed）
	ask := func(allow bool, fail error) sandbox.AskCallback {
		return func(sandbox.HostPattern) (bool, error) { return allow, fail }
	}
	if !sandbox.FilterNetworkRequest(80, "unknown.io", cfg, ask(true, nil)) {
		t.Fatal("callback allow must pass")
	}
	if sandbox.FilterNetworkRequest(80, "unknown.io", cfg, ask(false, nil)) {
		t.Fatal("callback deny must deny")
	}
	if sandbox.FilterNetworkRequest(80, "unknown.io", cfg, ask(true, errBoom)) {
		t.Fatal("callback error must deny (fail-closed)")
	}
}

var errBoom = &boomError{}

type boomError struct{}

func (*boomError) Error() string { return "boom" }

// ---- 主机工具 ----

func TestHostUtils(t *testing.T) {
	// 合法主机（isValidHost 是宽松 DNS-label 字符集门——1.2.3.4.5 过门，
	// 严格拒收在 CanonicalizeHost 的 dotted-quad 校验）
	for _, ok := range []string{"example.com", "a-b.c_d", "1.2.3.4", "1.2.3.4.5", "::1", "[::1]", "0:0:0:0:0:0:0:1"} {
		if !sandbox.IsValidHost(ok) {
			t.Errorf("IsValidHost(%q) = false, want true", ok)
		}
	}
	// 非法主机
	for _, bad := range []string{"", "bad host", "bad%host", "a|b", strings.Repeat("a", 256)} {
		if sandbox.IsValidHost(bad) {
			t.Errorf("IsValidHost(%q) = true, want false", bad)
		}
	}

	// 规范化
	can := []struct{ in, want string }{
		{"1.2.3.4", "1.2.3.4"},
		{"example.com.", "example.com"}, // 尾点剥离
		{"[::1]", "::1"},
		{"0:0:0:0:0:0:0:1", "0:0:0:0:0:0:0:1"},
		{"1.2.3", ""},       // 非四段 IPv4 → 拒
		{"010.1.1.1", ""},   // 前导零（八进制形态）→ 拒
		{"1.2.3.4.5", ""},   // 五段 → 拒
		{"bad\x01host", ""}, // 控制字符 → 拒
		{"bad%host", ""},    // % → 拒
	}
	for _, c := range can {
		if got := sandbox.CanonicalizeHost(c.in); got != c.want {
			t.Errorf("CanonicalizeHost(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---- 违规存储 ----

func TestViolationStore(t *testing.T) {
	s := sandbox.NewViolationStore(map[string][]string{
		"file_read": {"ignored-path"},
	})
	s.Record("file_read", "denied ignored-path/x")
	s.Record("file_read", "denied /etc/passwd")
	s.Record("network", "blocked evil.com")

	if s.Violations("file_read")[0] != "denied /etc/passwd" {
		t.Fatalf("ignored filter wrong: %v", s.Violations("file_read"))
	}
	if len(s.Violations("network")) != 1 {
		t.Fatal("network violation missing")
	}
	if !s.HasViolations() || len(s.Categories()) != 2 {
		t.Fatalf("categories = %v", s.Categories())
	}

	s.Clear()
	if s.HasViolations() {
		t.Fatal("Clear must empty store")
	}
}

// ---- 平台 ----

func TestPlatformDetect(t *testing.T) {
	if runtime.GOOS == "darwin" {
		if sandbox.DetectPlatform() != sandbox.MacOS || !sandbox.IsSupportedPlatform() {
			t.Fatal("darwin must detect as supported MacOS")
		}
	}
	if sandbox.DetectPlatform().String() == "" {
		t.Fatal("platform name empty")
	}
	if runtime.GOOS != "linux" && sandbox.WSLVersion() != "" {
		t.Fatal("non-linux has no WSL version")
	}
}

// ---- 真 sandbox-exec 回环（darwin）----

func TestSandboxExecReal(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only real sandbox-exec roundtrip")
	}

	tmp := t.TempDir()
	secretDir := filepath.Join(tmp, "secret")
	scratch := filepath.Join(tmp, "scratch")
	blocked := filepath.Join(tmp, "blocked")
	for _, d := range []string{secretDir, scratch, blocked} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	allowedFile := filepath.Join(tmp, "allowed.txt")
	secretFile := filepath.Join(secretDir, "hidden.txt")
	if err := os.WriteFile(allowedFile, []byte("public\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretFile, []byte("top-secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := sandbox.New(sandbox.RuntimeConfig{Filesystem: &sandbox.FilesystemConfig{
		AllowWrite: []string{scratch},
		DenyWrite:  []string{blocked},
		DenyRead:   []string{secretDir},
	}})
	if err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T, cmd string) (string, error) {
		t.Helper()
		spec, err := m.WrapWithSandboxArgv(cmd, "bash", nil)
		if err != nil {
			t.Fatal(err)
		}
		c := exec.Command(spec.Argv[0], spec.Argv[1:]...)
		c.Env = spec.Env
		out, err := c.CombinedOutput()
		return string(out), err
	}

	// 放行：读公开文件
	if out, err := run(t, "cat "+sandbox.ShellQuoteArg(allowedFile)); err != nil || strings.TrimSpace(out) != "public" {
		t.Fatalf("allowed read failed: out=%q err=%v", out, err)
	}

	// 放行：写白名单目录
	outFile := filepath.Join(scratch, "f.txt")
	if out, err := run(t, "echo done > "+sandbox.ShellQuoteArg(outFile)+" && cat "+sandbox.ShellQuoteArg(outFile)); err != nil || strings.TrimSpace(out) != "done" {
		t.Fatalf("allowed write failed: out=%q err=%v", out, err)
	}

	// 拒绝：读 DenyRead 目录
	if out, err := run(t, "cat "+sandbox.ShellQuoteArg(secretFile)); err == nil {
		t.Fatalf("deny read must fail, out=%q", out)
	}

	// 拒绝：写 DenyWrite 目录（缺省写路径放行 tmp，但显式 deny 后发胜）
	if out, err := run(t, "touch "+sandbox.ShellQuoteArg(filepath.Join(blocked, "x"))); err == nil {
		t.Fatalf("deny write must fail, out=%q", out)
	}
	if _, statErr := os.Stat(filepath.Join(blocked, "x")); statErr == nil {
		t.Fatal("blocked write must not create the file")
	}

	// 拒绝：写家目录（不在白名单）
	home, _ := os.UserHomeDir()
	homeProbe := filepath.Join(home, ".sbx_probe_test")
	t.Cleanup(func() { os.Remove(homeProbe) })
	if out, err := run(t, "echo x > "+sandbox.ShellQuoteArg(homeProbe)); err == nil {
		t.Fatalf("home write must fail, out=%q", out)
	}
	if _, statErr := os.Stat(homeProbe); statErr == nil {
		t.Fatal("home write must not create the file")
	}
}
