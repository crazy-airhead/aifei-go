// bwrap_test.go P5-s2 linux bwrap 后端黑盒测试：文件系统 bind/tmpfs
// 参数生成（写白名单/读 deny 目录盖 tmpfs/文件掩 /dev/null/allow 放回/
// tmpfs 冲掉的写 bind 补绑/denyWrite 缓冲与掩掉跳过/不存在 deny 的
// 首缺失组件挂载/文件祖先跳过/根 deny 展开浅优先排序）、强制拒绝清单
// 的 ripgrep 扫描（假 rg 脚本回放）、seccomp 前缀两形态、包装命令全形
// 态（直通/写限制/网络桥 socket+代理环境变量/弱嵌套/allowAllUnixSockets）、
// 内层 socat 脚本、挂载点清理的计数延迟、glob 的 linux 读写语义、依赖
// 明细、socat 桥真回环（装了 socat 才跑）。argv/profile 构造是纯字符串
// 生成——全部用例在任意平台可跑（真 bwrap 执行只在 linux 有意义）。
// 断言约定：存在的路径两端同经 NormalizePathForSandbox（macOS /var→
// /private）；不存在的路径规范化返回原样，字符串两端同构造即吻合；
// bwrap 参数序用 \x00 精确匹配避免子串假命中。
package sandbox_test

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/crazy-airhead/aifei-go/sandbox"
)

// nulJoin 参数序列精确匹配用：\x00 分隔避免子串假命中（"--tmpfs /etc"
// 不命中 "--tmpfs /"）。
func nulJoin(args []string) string {
	return "\x00" + strings.Join(args, "\x00") + "\x00"
}

func n(path string) string { return sandbox.NormalizePathForSandbox(path) }

// sandboxCwd 把 cwd 切到隔离空目录（强制拒绝清单的 rg 扫描与 cwd 派生
// deny 路径不依赖宿主目录内容），返回与库同源的 cwd 字符串。
func sandboxCwd(t *testing.T) string {
	t.Helper()
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}

// ---- glob 的 linux 读写语义 ----

func TestLinuxStripWriteGlobs(t *testing.T) {
	// 尾斜杠形态 "/b/**/" 不是纯 "/**" 尾——不剥，按含 glob 丢弃（Java 怪癖）
	got := sandbox.StripWriteGlobsForLinux([]string{"/a/**", "/tmp/*", "**/x", "/plain", "/b/**/", "/b/**"})
	want := []string{"/a", "/plain", "/b"}
	if len(got) != len(want) {
		t.Fatalf("strip = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("strip[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if sandbox.StripWriteGlobsForLinux(nil) != nil {
		t.Fatal("nil in nil out")
	}
}

func TestLinuxExpandReadPaths(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	for _, d := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(tmp, "a", ".env"), []byte("1"), 0o644)

	got := sandbox.LinuxExpandReadPaths([]string{
		filepath.Join(tmp, "*", ".env"), // 展开为具体路径
		filepath.Join(tmp, "x"),         // 无 glob 原样（剥尾后）
		filepath.Join(tmp, "y", "**"),   // 尾 /** 剥离
		"**/z",                          // 过宽 → 保留剥尾形态
	})
	joined := nulJoin(got)
	// 展开结果挂在 canonical 化基目录下（/private 前缀）
	if !strings.Contains(joined, nulJoin([]string{n(filepath.Join(tmp, "a", ".env"))})) {
		t.Errorf("glob must expand to concrete path: %v", got)
	}
	// 非 glob 路径不做规范化——原样（剥尾后）返回
	if !strings.Contains(joined, nulJoin([]string{filepath.Join(tmp, "x")})) ||
		!strings.Contains(joined, nulJoin([]string{filepath.Join(tmp, "y")})) ||
		!strings.Contains(joined, nulJoin([]string{"**/z"})) {
		t.Errorf("plain/stripped/too-broad handling wrong: %v", got)
	}
	if len(got) != 4 {
		t.Errorf("single-file glob must expand to exactly one path: %v", got)
	}
}

// ---- 强制拒绝清单（假 rg 回放）----

// fakeMandatoryRg 参数感知的假 ripgrep：.git/config 命中行仅在调用方
// 传了 **/.git/config iglob（allowGitConfig=false）时回放——真 rg 只会
// 按传入模式输出，无视参数的假件会制造库并不存在的偏差。
func fakeMandatoryRg(t *testing.T) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "fake-rg.sh")
	body := `#!/bin/sh
echo sub2/.bashrc
echo deep/.vscode/x
for a in "$@"; do
  case "$a" in
  *.git/config) echo nested/.git/config ;;
  esac
done
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func TestLinuxMandatoryDenyPaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cwd, _ := os.Getwd()

	// .git 是目录 → hooks/config 都进清单（allowGitConfig=false）
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	rg := fakeMandatoryRg(t)
	pats := sandbox.LinuxMandatoryDenyPaths(&sandbox.RipgrepConfig{Command: rg}, 3, false)

	join := func(elem ...string) string { return filepath.Join(append([]string{cwd}, elem...)...) }
	want := []string{
		join(".bashrc"),                  // cwd 危险文件
		join(".vscode"),                  // cwd 危险目录
		join(".git", "hooks"),            // .git 是目录 → hooks
		join(".git", "config"),           //                              + config
		join("sub2", ".bashrc"),          // rg 危险文件命中 → 文件路径
		join("deep", ".vscode"),          // rg 危险目录命中 → 归并到目录
		join("nested", ".git", "config"), // rg .git 特例 → config
	}
	joined := nulJoin(pats)
	for _, w := range want {
		if !strings.Contains(joined, nulJoin([]string{w})) {
			t.Errorf("mandatory deny missing %q in:\n%v", w, pats)
		}
	}

	// allowGitConfig=true：cwd 与嵌套 .git/config 都不再拒，hooks 仍拒
	pats = sandbox.LinuxMandatoryDenyPaths(&sandbox.RipgrepConfig{Command: rg}, 3, true)
	foundHooks := false
	for _, p := range pats {
		if strings.HasSuffix(p, ".git"+string(filepath.Separator)+"config") || strings.HasSuffix(p, ".git/config") {
			t.Errorf("allowGitConfig=true still denies config: %v", pats)
		}
		if strings.HasSuffix(p, filepath.Join(".git", "hooks")) {
			foundHooks = true
		}
	}
	if !foundHooks {
		t.Fatal("git hooks must stay denied")
	}

	// worktree：顶层 .git 是文件 → 顶层 hooks/config 不进清单（deny 会让
	// bwrap 失败）；rg 命中的嵌套 .git/config 仍正常产生
	wt := t.TempDir()
	t.Chdir(wt)
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wtCwd, _ := os.Getwd()
	pats = sandbox.LinuxMandatoryDenyPaths(&sandbox.RipgrepConfig{Command: rg}, 3, false)
	gitPrefix := filepath.Join(wtCwd, ".git")
	for _, p := range pats {
		if p == gitPrefix || strings.HasPrefix(p, gitPrefix+string(filepath.Separator)) {
			t.Errorf("worktree top-level .git (file) must not produce denies: %v", pats)
		}
	}
}

// ---- 文件系统参数生成 ----

func TestLinuxFsArgsWriteOnly(t *testing.T) {
	sandboxCwd(t) // rg 扫描确定性
	defer sandbox.ResetLinuxBackend()
	tmp := t.TempDir()
	scratch := filepath.Join(tmp, "scratch")
	secret := filepath.Join(scratch, "secret")
	os.MkdirAll(scratch, 0o755)
	os.WriteFile(secret, []byte("x"), 0o644)

	args := sandbox.GenerateLinuxFilesystemArgs(nil,
		&sandbox.WriteRestriction{AllowOnly: []string{scratch}, DenyWithinAllow: []string{secret}},
		nil, 3, false)
	joined := nulJoin(args)

	// 只读根打底 → 写白名单放写 → denyWrite（存在且在白名单内）ro-bind 收回
	iRoot := strings.Index(joined, nulJoin([]string{"--ro-bind", "/", "/"}))
	iBind := strings.Index(joined, nulJoin([]string{"--bind", n(scratch), n(scratch)}))
	iDeny := strings.Index(joined, nulJoin([]string{"--ro-bind", n(secret), n(secret)}))
	if !(0 <= iRoot && iRoot < iBind && iBind < iDeny) {
		t.Fatalf("write-only order wrong: root=%d bind=%d deny=%d\n%s", iRoot, iBind, iDeny, joined)
	}
}

func TestLinuxFsArgsReadDenyAndAllowBack(t *testing.T) {
	sandboxCwd(t)
	tmp := t.TempDir()
	base := filepath.Join(tmp, "base")
	w := filepath.Join(base, "w")
	sub := filepath.Join(base, "sub")
	for _, d := range []string{w, sub} {
		os.MkdirAll(d, 0o755)
	}
	secretFile := filepath.Join(base, "hidden.txt")
	os.WriteFile(secretFile, []byte("x"), 0o644)

	args := sandbox.GenerateLinuxFilesystemArgs(
		&sandbox.ReadRestriction{DenyOnly: []string{base, secretFile}, AllowWithinDeny: []string{sub}},
		&sandbox.WriteRestriction{AllowOnly: []string{w}},
		nil, 3, false)
	joined := nulJoin(args)

	// deny 目录 → tmpfs 盖掉；写 bind 被 tmpfs 冲掉 → 补绑（第二次出现，
	// 第一次在写白名单段）；allow 子路径 ro-bind 放回
	iTmpfs := strings.Index(joined, nulJoin([]string{"--tmpfs", n(base)}))
	bindNeedle := nulJoin([]string{"--bind", n(w), n(w)})
	firstBind := strings.Index(joined, bindNeedle)
	secondBind := firstBind + len(bindNeedle) +
		strings.Index(joined[firstBind+len(bindNeedle):], bindNeedle)
	iAllow := strings.Index(joined, nulJoin([]string{"--ro-bind", n(sub), n(sub)}))
	if !(0 <= firstBind && firstBind < iTmpfs && iTmpfs < secondBind && secondBind < iAllow) {
		t.Fatalf("read-deny order wrong: first=%d tmpfs=%d rewrite=%d allow=%d\n%s",
			firstBind, iTmpfs, secondBind, iAllow, joined)
	}

	// 文件级 deny：文件在 DenyOnly 内而 allow-back 只是包含它的目录
	// （非精确匹配）→ 仍掩 /dev/null
	if !strings.Contains(joined, nulJoin([]string{"--ro-bind", "/dev/null", n(secretFile)})) {
		t.Errorf("file deny must mask /dev/null even with dir allow-back:\n%s", joined)
	}

	// 文件级精确 allow 解禁：hidden.txt 不再掩
	args = sandbox.GenerateLinuxFilesystemArgs(
		&sandbox.ReadRestriction{DenyOnly: []string{base, secretFile}, AllowWithinDeny: []string{sub, secretFile}},
		&sandbox.WriteRestriction{AllowOnly: []string{w}},
		nil, 3, false)
	if strings.Contains(nulJoin(args), nulJoin([]string{"--ro-bind", "/dev/null", n(secretFile)})) {
		t.Errorf("exact file allow must un-deny the file:\n%v", args)
	}
}

func TestLinuxFsArgsShallowFirstSort(t *testing.T) {
	tmp := t.TempDir()
	deep := filepath.Join(tmp, "d1", "d2")
	os.MkdirAll(deep, 0o755)
	f := filepath.Join(deep, "f.txt")
	os.WriteFile(f, []byte("x"), 0o644)

	// 文件 deny 列在目录 deny 之前也须目录 tmpfs 先落（浅优先排序）
	args := sandbox.GenerateLinuxFilesystemArgs(
		&sandbox.ReadRestriction{DenyOnly: []string{f, filepath.Join(tmp, "d1")}}, nil, nil, 3, false)
	joined := nulJoin(args)
	iTmpfs := strings.Index(joined, nulJoin([]string{"--tmpfs", n(filepath.Join(tmp, "d1"))}))
	iMask := strings.Index(joined, nulJoin([]string{"--ro-bind", "/dev/null", n(f)}))
	if !(0 <= iTmpfs && iTmpfs < iMask) {
		t.Fatalf("shallow-first order wrong: tmpfs=%d mask=%d\n%s", iTmpfs, iMask, joined)
	}
}

func TestLinuxFsArgsMaskedFilesFilterDenyWrite(t *testing.T) {
	sandboxCwd(t)
	tmp := t.TempDir()
	f2 := filepath.Join(tmp, "f2.txt")
	os.WriteFile(f2, []byte("x"), 0o644)

	args := sandbox.GenerateLinuxFilesystemArgs(
		&sandbox.ReadRestriction{DenyOnly: []string{f2}},
		&sandbox.WriteRestriction{AllowOnly: []string{tmp}, DenyWithinAllow: []string{f2}},
		nil, 3, false)
	joined := nulJoin(args)

	// denyRead 的 /dev/null 掩掉 f2；denyWrite 的 ro-bind 被跳过（后发会解掩）
	if !strings.Contains(joined, nulJoin([]string{"--ro-bind", "/dev/null", n(f2)})) {
		t.Fatalf("read deny must mask file:\n%s", joined)
	}
	if strings.Contains(joined, nulJoin([]string{"--ro-bind", n(f2), n(f2)})) {
		t.Fatalf("denyWrite must skip masked dest:\n%s", joined)
	}
}

func TestLinuxFsArgsNonExistentDeny(t *testing.T) {
	sandboxCwd(t)
	defer sandbox.ResetLinuxBackend() // 清挂载点登记与计数
	// 基目录取 canonical 形态（/private 前缀）：不存在的 deny 路径规范化
	// 原样返回，与已解析的写白名单形态一致——Linux 上两者天然相同，
	// macOS 测试须先 canonical 化避免 /var vs /private 假性不等。
	tmp := n(t.TempDir())

	// 叶子缺失 → /dev/null 挂在缺失路径上（阻断创建），登记挂载点
	leaf := filepath.Join(tmp, "newfile")
	args := sandbox.GenerateLinuxFilesystemArgs(nil,
		&sandbox.WriteRestriction{AllowOnly: []string{tmp}, DenyWithinAllow: []string{leaf}},
		nil, 3, false)
	if !strings.Contains(nulJoin(args), nulJoin([]string{"--ro-bind", "/dev/null", n(leaf)})) {
		t.Fatalf("leaf deny must mount /dev/null at first non-existent:\n%v", args)
	}

	// 中间组件缺失 → 挂临时空目录（claude-empty-*），非 /dev/null
	mid := filepath.Join(tmp, "no", "such", ".bashrc")
	args = sandbox.GenerateLinuxFilesystemArgs(nil,
		&sandbox.WriteRestriction{AllowOnly: []string{tmp}, DenyWithinAllow: []string{mid}},
		nil, 3, false)
	foundEmptyDir := false
	for i := 0; i+2 < len(args); i++ {
		if args[i] == "--ro-bind" && args[i+2] == n(filepath.Join(tmp, "no")) &&
			strings.Contains(args[i+1], "claude-empty-") {
			foundEmptyDir = true
		}
	}
	if !foundEmptyDir {
		t.Fatalf("intermediate deny must bind a claude-empty-* dir:\n%v", args)
	}

	// 已存在的祖先是文件（mkdir 不可能）→ 整条跳过
	f := filepath.Join(tmp, "plain.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	args = sandbox.GenerateLinuxFilesystemArgs(nil,
		&sandbox.WriteRestriction{AllowOnly: []string{tmp}, DenyWithinAllow: []string{filepath.Join(f, "inner"), "sub"}},
		nil, 3, false)
	joined := nulJoin(args)
	if strings.Contains(joined, n(filepath.Join(f, "inner"))) || strings.Contains(joined, n(f)) {
		t.Fatalf("file-ancestor deny must be skipped entirely:\n%v", args)
	}

	// 写白名单外的缺失 deny 跳过（本来就只读）
	outsideDir, err := os.MkdirTemp("", "sb-outside-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outsideDir)
	args = sandbox.GenerateLinuxFilesystemArgs(nil,
		&sandbox.WriteRestriction{AllowOnly: []string{tmp}, DenyWithinAllow: []string{filepath.Join(outsideDir, "x")}},
		nil, 3, false)
	if strings.Contains(nulJoin(args), n(outsideDir)) {
		t.Fatalf("deny outside allowed write paths must be skipped:\n%v", args)
	}
}

func TestLinuxFsArgsRootDenyExpansion(t *testing.T) {
	args := sandbox.GenerateLinuxFilesystemArgs(
		&sandbox.ReadRestriction{DenyOnly: []string{"/"}}, nil, nil, 3, false)
	joined := nulJoin(args)

	// 根 deny 展开为直接子目录逐个 tmpfs（--tmpfs / 会冲掉先前全部挂载）
	if strings.Contains(joined, nulJoin([]string{"--tmpfs", "/"})) {
		t.Fatalf("root deny must not tmpfs / directly:\n%v", args)
	}
	foundChild := false
	for _, child := range []string{"/etc", "/tmp", "/var", "/usr", "/bin"} {
		if strings.Contains(joined, nulJoin([]string{"--tmpfs", child})) {
			foundChild = true
			break
		}
	}
	if !foundChild {
		t.Fatalf("root deny must expand to direct children tmpfs:\n%v", args)
	}
	// proc/dev/sys 不参与（由调用方 --dev/--proc 重挂）
	for _, skip := range []string{"--tmpfs\x00/proc\x00", "--tmpfs\x00/dev\x00", "--tmpfs\x00/sys\x00"} {
		if strings.Contains(joined, "\x00"+skip) {
			t.Fatalf("proc/dev/sys must be skipped in root expansion:\n%v", args)
		}
	}
}

// ---- 包装命令 ----

func TestWrapCommandLinuxPassthrough(t *testing.T) {
	got, err := sandbox.WrapCommandLinux(&sandbox.LinuxParams{Command: "ls -la"})
	if err != nil || got != "ls -la" {
		t.Fatalf("passthrough = %q err=%v", got, err)
	}
}

func TestWrapCommandLinuxShape(t *testing.T) {
	sandboxCwd(t)
	defer sandbox.ResetLinuxBackend()
	tmp := t.TempDir()

	wrapped, err := sandbox.WrapCommandLinux(&sandbox.LinuxParams{
		Command:     "echo hi",
		WriteConfig: &sandbox.WriteRestriction{AllowOnly: []string{tmp}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := nulJoin(strings.Fields(wrapped))
	for _, want := range []string{
		nulJoin([]string{"bwrap", "--new-session"}),
		"\x00--die-with-parent\x00",
		nulJoin([]string{"--ro-bind", "/", "/"}),
		nulJoin([]string{"--bind", n(tmp), n(tmp)}),
		nulJoin([]string{"--dev", "/dev"}),
		"\x00--unshare-pid\x00--proc\x00/proc\x00",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("wrapped missing %q:\n%s", want, wrapped)
		}
	}
	if !strings.HasSuffix(wrapped, " -c 'echo hi'") {
		t.Errorf("wrapped tail wrong: ...%q", wrapped[len(wrapped)-40:])
	}
	if strings.Contains(wrapped, "--unshare-net") {
		t.Error("no network restriction must not unshare-net")
	}

	// 弱化嵌套沙箱：--unshare-user + --bind /proc /proc 替代 --proc /proc
	weaker := true
	wrapped, err = sandbox.WrapCommandLinux(&sandbox.LinuxParams{
		Command:                   "echo hi",
		WriteConfig:               &sandbox.WriteRestriction{AllowOnly: []string{tmp}},
		EnableWeakerNestedSandbox: &weaker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wrapped, "--unshare-user --bind /proc /proc") {
		t.Errorf("weaker nested branch wrong:\n%s", wrapped)
	}
	if strings.Contains(wrapped, "--proc /proc") {
		t.Errorf("weaker nested must drop --proc:\n%s", wrapped)
	}
}

func TestWrapCommandLinuxNetwork(t *testing.T) {
	sandboxCwd(t)
	defer sandbox.ResetLinuxBackend()
	tmp := t.TempDir()
	httpSock := filepath.Join(tmp, "http.sock")
	socksSock := filepath.Join(tmp, "socks.sock")
	// 存在性检查只看文件在不在——普通文件即可（bwrap 不会真的跑）
	os.WriteFile(httpSock, []byte("x"), 0o644)
	os.WriteFile(socksSock, []byte("x"), 0o644)
	http, socks := 8080, 1080

	wrapped, err := sandbox.WrapCommandLinux(&sandbox.LinuxParams{
		Command:                 "curl x",
		NeedsNetworkRestriction: true,
		HTTPSocketPath:          httpSock,
		SocksSocketPath:         socksSock,
		HTTPProxyPort:           &http,
		SocksProxyPort:          &socks,
		CACertPath:              "/ca.pem",
		WriteConfig:             &sandbox.WriteRestriction{AllowOnly: []string{tmp}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// socket 路径原样透传（不做规范化）
	for _, want := range []string{
		"--unshare-net",
		"--bind " + httpSock + " " + httpSock,
		"--bind " + socksSock + " " + socksSock,
		"--setenv HTTP_PROXY http://localhost:3128",
		"--setenv ALL_PROXY socks5h://localhost:1080",
		"--setenv NODE_EXTRA_CA_CERTS /ca.pem",
		"--setenv CLAUDE_CODE_HOST_HTTP_PROXY_PORT 8080",
		"--setenv CLAUDE_CODE_HOST_SOCKS_PROXY_PORT 1080",
		"TCP-LISTEN:3128,fork,reuseaddr UNIX-CONNECT:" + httpSock,
		"TCP-LISTEN:1080,fork,reuseaddr UNIX-CONNECT:" + socksSock,
		`trap "kill %1 %2 2>/dev/null; exit" EXIT`,
	} {
		if !strings.Contains(wrapped, want) {
			t.Errorf("networked wrap missing %q:\n%s", want, wrapped)
		}
	}

	// 桥 socket 已死 → 明确报错
	_, err = sandbox.WrapCommandLinux(&sandbox.LinuxParams{
		Command:                 "x",
		NeedsNetworkRestriction: true,
		HTTPSocketPath:          filepath.Join(tmp, "nope.sock"),
		SocksSocketPath:         socksSock,
	})
	if err == nil || !strings.Contains(err.Error(), "bridge socket does not exist") {
		t.Fatalf("dead socket error = %v", err)
	}
}

func TestWrapCommandLinuxSeccomp(t *testing.T) {
	sandboxCwd(t)
	defer sandbox.ResetLinuxBackend()
	tmp := t.TempDir()
	applyBin := filepath.Join(tmp, "apply-seccomp")
	os.WriteFile(applyBin, []byte("#!/bin/sh\nexit 0\n"), 0o755)

	// 独立二进制形态：tail = <bin> <shell> -c <cmd>（外层 ShellQuote
	// 会转义内层单引号，断言到首个内层引号为止的前缀）
	wrapped, err := sandbox.WrapCommandLinux(&sandbox.LinuxParams{
		Command:       "echo hi",
		WriteConfig:   &sandbox.WriteRestriction{AllowOnly: []string{tmp}},
		SeccompConfig: &sandbox.SeccompConfig{ApplyPath: applyBin},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wrapped, applyBin+" /bin/bash -c ") {
		t.Errorf("seccomp binary prefix wrong:\n%s", wrapped)
	}

	// ARGV0 multicall 形态
	wrapped, err = sandbox.WrapCommandLinux(&sandbox.LinuxParams{
		Command:       "echo hi",
		WriteConfig:   &sandbox.WriteRestriction{AllowOnly: []string{tmp}},
		SeccompConfig: &sandbox.SeccompConfig{ApplyPath: "/x/apply", Argv0: "v0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wrapped, "ARGV0=v0 /x/apply /bin/bash -c ") {
		t.Errorf("ARGV0 prefix wrong:\n%s", wrapped)
	}

	// allowAllUnixSockets → 跳过 seccomp（tail 是裸命令）
	all := true
	wrapped, err = sandbox.WrapCommandLinux(&sandbox.LinuxParams{
		Command:             "echo hi",
		WriteConfig:         &sandbox.WriteRestriction{AllowOnly: []string{tmp}},
		AllowAllUnixSockets: &all,
		SeccompConfig:       &sandbox.SeccompConfig{ApplyPath: applyBin},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(wrapped, " -c 'echo hi'") {
		t.Errorf("allowAllUnixSockets must skip seccomp:\n%s", wrapped)
	}
}

func TestBuildLinuxSandboxCommand(t *testing.T) {
	// 无 seccomp：socat 双监听 + trap + eval（内层单引号被外层转义）
	got := sandbox.BuildLinuxSandboxCommand("/a.sock", "/b.sock", "cargo build", "", "/bin/bash", "")
	for _, want := range []string{
		"/bin/bash -c '",
		"socat TCP-LISTEN:3128,fork,reuseaddr UNIX-CONNECT:/a.sock >/dev/null 2>&1 &",
		"socat TCP-LISTEN:1080,fork,reuseaddr UNIX-CONNECT:/b.sock >/dev/null 2>&1 &",
		`trap "kill %1 %2 2>/dev/null; exit" EXIT`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("inner script missing %q:\n%s", want, got)
		}
	}
	iEval, iCargo := strings.Index(got, "eval "), strings.Index(got, "cargo build")
	if !(0 <= iEval && iEval < iCargo) {
		t.Errorf("eval tail wrong:\n%s", got)
	}

	// 自定义 socat 路径被引用
	got = sandbox.BuildLinuxSandboxCommand("/a.sock", "/b.sock", "x", "", "/bin/bash", "/opt/socat")
	if !strings.Contains(got, "/opt/socat TCP-LISTEN:3128") {
		t.Errorf("custom socat path missing:\n%s", got)
	}

	// seccomp 前缀在 socat 与 trap 之后（内层单引号被外层 ShellQuote 转义）
	got = sandbox.BuildLinuxSandboxCommand("/a.sock", "/b.sock", "cargo build", "'/as' ", "/bin/bash", "")
	iTrap, iSeccomp := strings.Index(got, `trap "kill`), strings.Index(got, `'"'"'/as'"'"' /bin/bash -c `)
	if !(0 <= iTrap && iTrap < iSeccomp) {
		t.Errorf("seccomp must run after socat/trap:\n%s", got)
	}
}

func TestResolveApplySeccompPrefix(t *testing.T) {
	// argv0 需要 applyPath
	if _, err := sandbox.ResolveApplySeccompPrefix("", "v0"); err == nil ||
		!strings.Contains(err.Error(), "requires seccompConfig.applyPath") {
		t.Fatalf("argv0 without applyPath error = %v", err)
	}
	prefix, err := sandbox.ResolveApplySeccompPrefix("/x/apply", "v0")
	if err != nil || prefix != "ARGV0=v0 /x/apply " {
		t.Fatalf("ARGV0 prefix = %q err=%v", prefix, err)
	}

	// 显式路径不可执行 → 不可用（空）
	prefix, err = sandbox.ResolveApplySeccompPrefix("/nonexistent/apply-seccomp", "")
	if err != nil || prefix != "" {
		t.Fatalf("missing binary = %q err=%v", prefix, err)
	}

	// 显式路径可执行 → 带尾空格前缀
	bin := filepath.Join(t.TempDir(), "apply-seccomp")
	os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755)
	prefix, err = sandbox.ResolveApplySeccompPrefix(bin, "")
	if err != nil || prefix != sandbox.ShellQuoteArg(bin)+" " {
		t.Fatalf("binary prefix = %q err=%v", prefix, err)
	}
}

// ---- 挂载点清理（计数延迟）----

func TestCleanupBwrapMountPoints(t *testing.T) {
	sandboxCwd(t)
	defer sandbox.ResetLinuxBackend()
	tmp := n(t.TempDir()) // canonical 基目录（同 TestLinuxFsArgsNonExistentDeny）
	leaf := filepath.Join(tmp, "ghost.bashrc")

	params := &sandbox.LinuxParams{
		Command:     "echo hi",
		WriteConfig: &sandbox.WriteRestriction{AllowOnly: []string{tmp}, DenyWithinAllow: []string{leaf}},
	}
	// 两次包装 → 活动计数 2（挂载点登记同一 leaf）
	for i := 0; i < 2; i++ {
		if _, err := sandbox.WrapCommandLinux(params); err != nil {
			t.Fatal(err)
		}
	}

	// 模拟 bwrap 在宿主落的空挂载点文件（登记路径与断言路径同经 n()——
	// 不存在路径规范化原样返回，两端一致）
	if err := os.WriteFile(n(leaf), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// 第一次清理：还有 1 个沙箱在飞 → 延迟删除
	sandbox.CleanupBwrapMountPoints(false)
	if _, err := os.Stat(n(leaf)); err != nil {
		t.Fatal("cleanup must defer while sandboxes active")
	}

	// 第二次清理：计数归零 → 删空文件
	sandbox.CleanupBwrapMountPoints(false)
	if _, err := os.Stat(n(leaf)); !os.IsNotExist(err) {
		t.Fatalf("empty mount point file must be removed, stat err=%v", err)
	}

	// 非空文件不删（可能已被正当使用）
	if err := os.WriteFile(n(leaf), []byte("used"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.WrapCommandLinux(params); err != nil {
		t.Fatal(err)
	}
	sandbox.CleanupBwrapMountPoints(true)
	if _, err := os.Stat(n(leaf)); err != nil {
		t.Fatal("non-empty mount point must be kept")
	}
	os.Remove(n(leaf))
}

// ---- 依赖明细 ----

func TestLinuxDependencyStatus(t *testing.T) {
	// argv0 模式：信任 applyPath，不做磁盘查找
	st := sandbox.GetLinuxDependencyStatus(&sandbox.RuntimeConfig{
		Seccomp: &sandbox.SeccompConfig{ApplyPath: "/x/apply", Argv0: "v0"},
	})
	if !st.HasSeccompApply {
		t.Fatal("argv0 mode must count as seccomp available")
	}
	// 显式 bwrap 路径不可执行 → false
	st = sandbox.GetLinuxDependencyStatus(&sandbox.RuntimeConfig{BwrapPath: "/nonexistent/bwrap"})
	if st.HasBwrap {
		t.Fatal("nonexistent bwrap override must be false")
	}
}

// ---- socat 桥 ----

// TestInitializeLinuxNetworkBridgeFakeSocat 用假 socat 脚本（socatPath 缝）
// 验证我方的桥契约：就绪轮询（socket 文件出现）、Stop 清场（杀进程 +
// 删 socket）、进程即死即报错。转发本身是 socat 的职责，真回环见
// TestInitializeLinuxNetworkBridge（装了 socat 才跑）。
func TestInitializeLinuxNetworkBridgeFakeSocat(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-socat")
	body := `#!/bin/sh
for a in "$@"; do
  case "$a" in
  UNIX-LISTEN:*) p="${a#UNIX-LISTEN:}"; : > "${p%%,*}" ;;
  esac
done
sleep 30
`
	if err := os.WriteFile(fake, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, err := sandbox.InitializeLinuxNetworkBridge(18080, 18108, fake)
	if err != nil {
		t.Fatalf("bridge init with fake socat: %v", err)
	}
	if _, err := os.Stat(ctx.HTTPSocketPath); err != nil {
		t.Fatalf("http socket missing: %v", err)
	}
	if !strings.Contains(ctx.HTTPSocketPath, "claude-http-") ||
		!strings.Contains(ctx.SocksSocketPath, "claude-socks-") {
		t.Fatalf("socket naming wrong: %q %q", ctx.HTTPSocketPath, ctx.SocksSocketPath)
	}
	if ctx.HTTPProxyPort != 18080 || ctx.SocksProxyPort != 18108 {
		t.Fatalf("bridge ports wrong: %+v", ctx)
	}

	ctx.Stop()
	if _, err := os.Stat(ctx.HTTPSocketPath); !os.IsNotExist(err) {
		t.Fatalf("http socket must be removed, stat err=%v", err)
	}
	if _, err := os.Stat(ctx.SocksSocketPath); !os.IsNotExist(err) {
		t.Fatalf("socks socket must be removed, stat err=%v", err)
	}

	// 进程即死（启动成功但立刻退出）→ 明确报错
	dying := filepath.Join(dir, "dying-socat")
	if err := os.WriteFile(dying, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := sandbox.InitializeLinuxNetworkBridge(18080, 18108, dying); err == nil ||
		!strings.Contains(err.Error(), "died unexpectedly") {
		t.Fatalf("dying socat error = %v", err)
	}
}

func TestInitializeLinuxNetworkBridge(t *testing.T) {
	if _, err := exec.LookPath("socat"); err != nil {
		t.Skip("socat not installed")
	}

	// 两个本地 TCP 目标充当宿主代理
	var targets []net.Listener
	for i := 0; i < 2; i++ {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, l)
	}
	httpPort := targets[0].Addr().(*net.TCPAddr).Port
	socksPort := targets[1].Addr().(*net.TCPAddr).Port
	for _, l := range targets {
		defer l.Close()
	}

	ctx, err := sandbox.InitializeLinuxNetworkBridge(httpPort, socksPort, "")
	if err != nil {
		t.Skipf("bridge init failed (environment): %v", err)
	}

	// socket 文件就绪
	if _, err := os.Stat(ctx.HTTPSocketPath); err != nil {
		t.Fatalf("http socket missing: %v", err)
	}
	if _, err := os.Stat(ctx.SocksSocketPath); err != nil {
		t.Fatalf("socks socket missing: %v", err)
	}
	if ctx.HTTPProxyPort != httpPort || ctx.SocksProxyPort != socksPort {
		t.Fatalf("bridge ports wrong: %+v", ctx)
	}

	// unix socket → socat → TCP 转发一字节
	conn, err := net.Dial("unix", ctx.HTTPSocketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	accepted, err := targets[0].Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	accepted.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4)
	if _, err := accepted.Read(buf); err != nil || string(buf) != "ping" {
		t.Fatalf("forwarded payload = %q err=%v", buf, err)
	}

	// Stop：杀进程 + 删 socket 文件
	ctx.Stop()
	if _, err := os.Stat(ctx.HTTPSocketPath); !os.IsNotExist(err) {
		t.Fatalf("http socket must be removed, stat err=%v", err)
	}
	if _, err := os.Stat(ctx.SocksSocketPath); !os.IsNotExist(err) {
		t.Fatalf("socks socket must be removed, stat err=%v", err)
	}
}
