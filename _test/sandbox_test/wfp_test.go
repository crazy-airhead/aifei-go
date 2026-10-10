// wfp_test.go P5-s4 windows WFP 后端黑盒测试：二进制定位（环境变量/
// 应用根 vendor·dist 候选/向上走查/落空错误面）、组与 WFP 状态 JSON 解
// 析矩阵（缺 state/未知值/port_range 三形态）、一次性 install（全参数
// argv/退出码契约 0·10·11·12·13·1/取消仍取状态）、卸载三态、组与 WFP
// 细粒度原语 argv 与提权错误面、WrapCommandWindows 形态（cmd/pwsh/
// powershell 三 shell、组引用、代理环境变量并置与 TMPDIR 剔除、同名键
// 覆写）、安装指引文案分支、依赖检查矩阵（就绪/组未就绪不重复 WFP 错/
// 组就绪 WFP 缺/二进制缺/子命令失败/sublayer 透传）。全部经假 srt-win
// 脚本回放——argv 生成与 JSON 解析纯字符串操作，全平台可跑；真 WFP 执
// 行只在 windows 有意义。
package sandbox_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/sandbox"
)

// ---- 假 srt-win 脚本 ----

const fakeSrtWinScript = `#!/bin/sh
{ echo "### $1 $2"; printf '%s\n' "$@"; } >> "$FAKE_ARGV"
if [ -n "$FAKE_FAIL" ] && [ "$1 $2" = "$FAKE_FAIL" ]; then
  printf '%s\n' "$FAKE_OUT" >&2
  exit "$FAKE_EXIT"
fi
case "$1 $2" in
  "group status") printf '%s\n' "$FAKE_GROUP_JSON"; exit 0 ;;
  "wfp status") printf '%s\n' "$FAKE_WFP_JSON"; exit 0 ;;
  *) if [ -n "$FAKE_OUT" ]; then printf '%s\n' "$FAKE_OUT" >&2; fi
     exit "${FAKE_EXIT:-0}" ;;
esac
`

// fakeSrtWin 落一个假 srt-win.exe 并接线 SRT_WIN_PATH 与回放环境变量
// （FAKE_GROUP_JSON/FAKE_WFP_JSON/FAKE_OUT/FAKE_EXIT/FAKE_FAIL）。
func fakeSrtWin(t *testing.T, groupJSON, wfpJSON string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "srt-win.exe")
	if err := os.WriteFile(path, []byte(fakeSrtWinScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SRT_WIN_PATH", path)
	t.Setenv("FAKE_ARGV", filepath.Join(dir, "argv.log"))
	t.Setenv("FAKE_GROUP_JSON", groupJSON)
	t.Setenv("FAKE_WFP_JSON", wfpJSON)
	t.Setenv("FAKE_EXIT", "0")
	return path
}

// fakeArgv 读回假件记录的每次调用参数（### 标记行开新调用）。
func fakeArgv(t *testing.T) [][]string {
	t.Helper()
	data, err := os.ReadFile(os.Getenv("FAKE_ARGV"))
	if err != nil {
		t.Fatalf("read argv log: %v", err)
	}
	var calls [][]string
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "### "):
			calls = append(calls, nil)
		case line != "" && calls != nil:
			calls[len(calls)-1] = append(calls[len(calls)-1], line)
		}
	}
	return calls
}

// ---- 二进制定位 ----

func TestSrtWinPathResolution(t *testing.T) {
	// SRT_WIN_PATH 优先（存在即用）
	tmp := t.TempDir()
	exe := filepath.Join(tmp, "anywhere-srt-win.exe")
	os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("SRT_WIN_PATH", exe)
	if got, err := sandbox.SrtWinPath(); err != nil || got != exe {
		t.Fatalf("env override = %q %v", got, err)
	}

	// 应用根 vendor 形态（t.Chdir 后 Getwd 即应用根）
	restoreSrtWinEnv(t)
	vendorExe := filepath.Join(tmp, "vendor", "srt-win", "target", "release", "srt-win.exe")
	os.MkdirAll(filepath.Dir(vendorExe), 0o755)
	os.WriteFile(vendorExe, []byte("#!/bin/sh\n"), 0o755)
	t.Chdir(tmp)
	if got, err := sandbox.SrtWinPath(); err != nil || got != vendorExe {
		t.Fatalf("vendor candidate = %q %v", got, err)
	}

	// dist 形态 + package.json 标记 + 向上走查（从子目录找到根）
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644)
	distExe := filepath.Join(root, "dist", "vendor", "srt-win", "target", "release", "srt-win.exe")
	os.MkdirAll(filepath.Dir(distExe), 0o755)
	os.WriteFile(distExe, []byte("#!/bin/sh\n"), 0o755)
	os.MkdirAll(filepath.Join(root, "a", "b"), 0o755)
	t.Chdir(filepath.Join(root, "a", "b")) // 2 层深，走查 ≤5 层
	if got, err := sandbox.SrtWinPath(); err != nil || got != distExe {
		t.Fatalf("dist walk-up candidate = %q %v", got, err)
	}

	// 全部落空：错误列出环境路径与候选
	empty := t.TempDir()
	t.Chdir(empty)
	t.Setenv("SRT_WIN_PATH", filepath.Join(empty, "missing-srt-win.exe"))
	_, err := sandbox.SrtWinPath()
	if err == nil {
		t.Fatal("expected error when nothing found")
	}
	msg := err.Error()
	if !strings.Contains(msg, "srt-win.exe not found") ||
		!strings.Contains(msg, "Looked in:") ||
		!strings.Contains(msg, "missing-srt-win.exe") ||
		!strings.Contains(msg, "vendor"+string(filepath.Separator)+"srt-win") {
		t.Errorf("error message = %q", msg)
	}
}

// restoreSrtWinEnv 清 SRT_WIN_PATH（Cleanup 恢复——t.Setenv 只会覆盖不清）。
func restoreSrtWinEnv(t *testing.T) {
	t.Helper()
	old, had := os.LookupEnv("SRT_WIN_PATH")
	os.Unsetenv("SRT_WIN_PATH")
	t.Cleanup(func() {
		if had {
			os.Setenv("SRT_WIN_PATH", old)
		} else {
			os.Unsetenv("SRT_WIN_PATH")
		}
	})
}

// ---- 状态查询与 JSON 解析 ----

func TestWindowsGroupStatus(t *testing.T) {
	fakeSrtWin(t, `{"state":"ready","sid":"S-1-5-32-999","warning":"heads up","error":"e"}`, `{}`)
	got, err := sandbox.GetWindowsGroupStatus(&sandbox.WindowsGroupRef{GroupSID: "S-1-5-32-999"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != sandbox.WindowsGroupReady || got.SID != "S-1-5-32-999" ||
		got.Warning != "heads up" || got.Error != "e" {
		t.Fatalf("group status = %+v", got)
	}
	// 组引用参数：SID 优先
	calls := fakeArgv(t)
	if len(calls) != 1 || strings.Join(calls[0], " ") != "group status --group-sid S-1-5-32-999" {
		t.Fatalf("argv = %v", calls)
	}

	// 名字引用 / nil 引用回落缺省组名
	sandbox.GetWindowsGroupStatus(&sandbox.WindowsGroupRef{GroupName: "mygrp"})
	sandbox.GetWindowsGroupStatus(nil)
	calls = fakeArgv(t)
	if strings.Join(calls[1], " ") != "group status --name mygrp" ||
		strings.Join(calls[2], " ") != "group status --name sandbox-runtime-net" {
		t.Fatalf("ref argv = %v", calls)
	}

	// created-not-on-token 状态解析
	fakeSrtWin(t, `{"state":"created-not-on-token"}`, `{}`)
	got, err = sandbox.GetWindowsGroupStatus(nil)
	if err != nil || got.State != sandbox.WindowsGroupCreatedNotOnToken || got.SID != "" {
		t.Fatalf("created-not-on-token = %+v %v", got, err)
	}

	// 缺 state 字段
	fakeSrtWin(t, `{"sid":"S-1-5"}`, `{}`)
	if _, err = sandbox.GetWindowsGroupStatus(nil); err == nil || !strings.Contains(err.Error(), "missing required field 'state'") {
		t.Fatalf("missing state err = %v", err)
	}

	// 未知状态值
	fakeSrtWin(t, `{"state":"zzz"}`, `{}`)
	if _, err = sandbox.GetWindowsGroupStatus(nil); err == nil || !strings.Contains(err.Error(), "Unknown WindowsGroupStatus: zzz") {
		t.Fatalf("unknown state err = %v", err)
	}

	// 子命令失败（exit != 0）：错误携 stderr
	fakeSrtWin(t, `{"state":"ready"}`, `{}`)
	t.Setenv("FAKE_FAIL", "group status")
	t.Setenv("FAKE_EXIT", "9")
	t.Setenv("FAKE_OUT", "boom-group")
	if _, err = sandbox.GetWindowsGroupStatus(nil); err == nil || !strings.Contains(err.Error(), "exited 9") || !strings.Contains(err.Error(), "boom-group") {
		t.Fatalf("failed subcommand err = %v", err)
	}
}

func TestWindowsWfpStatus(t *testing.T) {
	fakeSrtWin(t, `{}`, `{"state":"installed","filters":3,"port_range":[60080,60089]}`)
	got, err := sandbox.GetWindowsWfpStatus("")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != sandbox.WindowsWfpInstalled || got.Filters != 3 ||
		got.PortRange == nil || *got.PortRange != [2]int{60080, 60089} {
		t.Fatalf("wfp status = %+v", got)
	}

	// 无 port_range / null / 单元素 → nil（Java 宽容同款）
	for _, raw := range []string{
		`{"state":"absent","filters":0}`,
		`{"state":"installed","filters":1,"port_range":null}`,
		`{"state":"installed","filters":1,"port_range":[60080]}`,
	} {
		fakeSrtWin(t, `{}`, raw)
		got, err = sandbox.GetWindowsWfpStatus("")
		if err != nil || got.PortRange != nil {
			t.Fatalf("port_range nil case %s = %+v %v", raw, got, err)
		}
	}

	// sublayer 透传
	fakeSrtWin(t, `{}`, `{"state":"installed","filters":2,"port_range":[1,2]}`)
	if _, err = sandbox.GetWindowsWfpStatus("{11111111-2222-3333-4444-555555555555}"); err != nil {
		t.Fatal(err)
	}
	calls := fakeArgv(t)
	if len(calls) != 1 || strings.Join(calls[0], " ") != "wfp status --sublayer-guid {11111111-2222-3333-4444-555555555555}" {
		t.Fatalf("sublayer argv = %v", calls)
	}

	// 缺 state / 未知值
	fakeSrtWin(t, `{}`, `{"filters":1}`)
	if _, err = sandbox.GetWindowsWfpStatus(""); err == nil || !strings.Contains(err.Error(), "missing required field 'state'") {
		t.Fatalf("missing state err = %v", err)
	}
	fakeSrtWin(t, `{}`, `{"state":"maybe"}`)
	if _, err = sandbox.GetWindowsWfpStatus(""); err == nil || !strings.Contains(err.Error(), "Unknown WindowsWfpStatus: maybe") {
		t.Fatalf("unknown state err = %v", err)
	}
}

// ---- 安装 / 卸载 ----

func TestWindowsInstall(t *testing.T) {
	// 缺省选项 + 成功：argv 恰为缺省组名；随后取组 + WFP 状态
	fakeSrtWin(t, `{"state":"ready","sid":"S-1-5-32-1"}`, `{"state":"installed","filters":4,"port_range":[60080,60089]}`)
	res, err := sandbox.InstallWindowsSandbox(nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cancelled || res.Group.State != sandbox.WindowsGroupReady ||
		res.WFP.State != sandbox.WindowsWfpInstalled || res.WFP.Filters != 4 {
		t.Fatalf("install result = %+v", res)
	}
	calls := fakeArgv(t)
	if len(calls) != 3 ||
		strings.Join(calls[0], " ") != "install --name sandbox-runtime-net" ||
		strings.Join(calls[1], " ") != "group status --name sandbox-runtime-net" ||
		strings.Join(calls[2], " ") != "wfp status" {
		t.Fatalf("install argv = %v", calls)
	}

	// 全量选项：SID 优先 + user-sid + sublayer + 端口段 + force
	res, err = sandbox.InstallWindowsSandbox(&sandbox.WindowsInstallOptions{
		GroupName:      "ignored",
		GroupSID:       "S-1-5-32-9",
		UserSID:        "S-1-5-21-1",
		SublayerGUID:   "{AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE}",
		ProxyPortRange: &[2]int{70010, 70019},
		Force:          true,
	})
	if err != nil || res.Cancelled {
		t.Fatalf("full opts install = %+v %v", res, err)
	}
	wantArgs := "install --group-sid S-1-5-32-9 --user-sid S-1-5-21-1 " +
		"--sublayer-guid {AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE} " +
		"--proxy-port-range 70010-70019 --force"
	if got := strings.Join(fakeArgv(t)[3], " "); got != wantArgs {
		t.Fatalf("full opts argv =\n%s\nwant\n%s", got, wantArgs)
	}

	// 退出码契约
	cases := []struct {
		exit        int
		msgContains []string
	}{
		{10, nil}, // 取消：非错误，仍取状态
		{11, []string{"group create failed", "boom-11"}},                        // 组创建失败
		{12, []string{"WFP filter install failed"}},                             // WFP 安装失败
		{13, []string{"different configuration", "Force:true", "SublayerGUID"}}, // 配置冲突
		{1, []string{"install failed (exit 1)"}},                                // 其他
	}
	for i, c := range cases {
		fakeSrtWin(t, `{"state":"ready"}`, `{"state":"installed","filters":1}`)
		t.Setenv("FAKE_EXIT", strconv.Itoa(c.exit))
		t.Setenv("FAKE_OUT", "boom-11")
		res, err := sandbox.InstallWindowsSandbox(&sandbox.WindowsInstallOptions{GroupSID: "S-1-5-32-9"})
		if c.msgContains == nil {
			if err != nil || !res.Cancelled {
				t.Fatalf("case %d: cancelled install = %+v %v", i, res, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("case %d: expected error", i)
		}
		for _, want := range c.msgContains {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("case %d: err %q missing %q", i, err, want)
			}
		}
	}
}

func TestWindowsUninstall(t *testing.T) {
	// 成功
	fakeSrtWin(t, `{}`, `{}`)
	cancelled, err := sandbox.UninstallWindowsSandbox("{G}")
	if err != nil || cancelled {
		t.Fatalf("uninstall = %v %v", cancelled, err)
	}
	// sublayer 透传
	if got := strings.Join(fakeArgv(t)[0], " "); got != "uninstall --sublayer-guid {G}" {
		t.Fatalf("uninstall argv = %q", got)
	}

	// UAC 取消 → true 非错误
	t.Setenv("FAKE_EXIT", "10")
	cancelled, err = sandbox.UninstallWindowsSandbox("")
	if err != nil || !cancelled {
		t.Fatalf("cancelled uninstall = %v %v", cancelled, err)
	}

	// 其他失败
	t.Setenv("FAKE_EXIT", "7")
	t.Setenv("FAKE_OUT", "boom-7")
	_, err = sandbox.UninstallWindowsSandbox("")
	if err == nil || !strings.Contains(err.Error(), "uninstall failed (exit 7)") || !strings.Contains(err.Error(), "boom-7") {
		t.Fatalf("failed uninstall err = %v", err)
	}
}

// ---- 细粒度原语 ----

func TestWindowsGroupAndWfpPrimitives(t *testing.T) {
	fakeSrtWin(t, `{}`, `{}`)
	if err := sandbox.CreateWindowsGroup(&sandbox.WindowsGroupRef{GroupName: "g1"}, "S-1-5-21-9"); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.DeleteWindowsGroup(&sandbox.WindowsGroupRef{GroupSID: "S-1-5-32-8"}); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.CreateWindowsWfp(&sandbox.WindowsGroupRef{GroupSID: "S-1-5-32-8"}, "{SUB}", &[2]int{1, 2}); err != nil {
		t.Fatal(err)
	}
	calls := fakeArgv(t)
	want := []string{
		"group create --name g1 --user-sid S-1-5-21-9",
		"group delete --group-sid S-1-5-32-8",
		"wfp install --group-sid S-1-5-32-8 --sublayer-guid {SUB} --proxy-port-range 1-2",
	}
	if len(calls) != len(want) {
		t.Fatalf("primitives argv = %v", calls)
	}
	for i, w := range want {
		if got := strings.Join(calls[i], " "); got != w {
			t.Fatalf("primitive %d argv =\n%s\nwant\n%s", i, got, w)
		}
	}

	// 非 0 退出的提权错误面
	t.Setenv("FAKE_EXIT", "5")
	t.Setenv("FAKE_OUT", "denied")
	if err := sandbox.CreateWindowsGroup(nil, ""); err == nil ||
		!strings.Contains(err.Error(), "group create failed") ||
		!strings.Contains(err.Error(), "run as administrator") {
		t.Fatalf("group create err = %v", err)
	}
	if err := sandbox.DeleteWindowsGroup(nil); err == nil ||
		!strings.Contains(err.Error(), "group delete failed") ||
		!strings.Contains(err.Error(), "Requires elevation") {
		t.Fatalf("group delete err = %v", err)
	}
	if err := sandbox.CreateWindowsWfp(nil, "", nil); err == nil ||
		!strings.Contains(err.Error(), "wfp install failed") {
		t.Fatalf("wfp install err = %v", err)
	}
}

// ---- 包装命令 ----

func TestWrapCommandWindows(t *testing.T) {
	// wrap 只取二进制路径不执行——空可执行文件即可
	exe := filepath.Join(t.TempDir(), "srt-win.exe")
	os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755)
	t.Setenv("SRT_WIN_PATH", exe)
	if oldSR, hadSR := os.LookupEnv("SystemRoot"); hadSR {
		os.Unsetenv("SystemRoot")
		t.Cleanup(func() { os.Setenv("SystemRoot", oldSR) })
	}

	// cmd 形态（binShell 空；SystemRoot 缺省 C:\Windows）
	t.Setenv("TMPDIR", "/custom/tmp") // 生成集剔除 TMPDIR——宿主原值须保住
	spec, err := sandbox.WrapCommandWindows(&sandbox.WindowsParams{Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	wantArgv := []string{exe, "exec", "--name", "sandbox-runtime-net", "--",
		`C:\Windows\System32\cmd.exe`, "/d", "/s", "/c", "echo hi"}
	if strings.Join(spec.Argv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Fatalf("cmd argv = %v", spec.Argv)
	}
	assertEnv(t, spec.Env, "SANDBOX_RUNTIME", "1")
	assertEnv(t, spec.Env, "TMPDIR", "/custom/tmp") // 不被生成集的 /tmp/claude 覆写
	if _, ok := envLookup(spec.Env, "HTTP_PROXY"); ok {
		t.Error("no proxy ports → no HTTP_PROXY")
	}

	// SID 引用 + 双代理端口：代理变量集并入并覆写同名键
	t.Setenv("HTTP_PROXY", "http://stale:1")
	t.Setenv("WFP_TEST_KEEP", "yes")
	http, socks := 8080, 1080
	spec, err = sandbox.WrapCommandWindows(&sandbox.WindowsParams{
		Command:        "dir",
		Group:          &sandbox.WindowsGroupRef{GroupSID: "S-1-5-32-7"},
		HTTPProxyPort:  &http,
		SocksProxyPort: &socks,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Argv[2] != "--group-sid" || spec.Argv[3] != "S-1-5-32-7" || spec.Argv[4] != "--" {
		t.Fatalf("sid argv = %v", spec.Argv)
	}
	assertEnv(t, spec.Env, "HTTP_PROXY", "http://localhost:8080")
	assertEnv(t, spec.Env, "ALL_PROXY", "socks5h://localhost:1080")
	assertEnv(t, spec.Env, "WFP_TEST_KEEP", "yes") // 无关环境保留

	// pwsh / powershell 形态（大小写不敏感；SystemRoot 覆写生效）
	t.Setenv("SystemRoot", `W:\Win`)
	spec, _ = sandbox.WrapCommandWindows(&sandbox.WindowsParams{Command: "gci", BinShell: "PWSH"})
	if strings.Join(spec.Argv[5:9], " ") != "pwsh.exe -NoProfile -Command gci" {
		t.Fatalf("pwsh argv = %v", spec.Argv)
	}
	spec, _ = sandbox.WrapCommandWindows(&sandbox.WindowsParams{Command: "gci", BinShell: "PowerShell"})
	want := `W:\Win\System32\WindowsPowerShell\v1.0\powershell.exe -NoProfile -Command gci`
	if strings.Join(spec.Argv[5:9], " ") != want {
		t.Fatalf("powershell argv = %v", spec.Argv)
	}
	spec, _ = sandbox.WrapCommandWindows(&sandbox.WindowsParams{Command: "dir", BinShell: "cmd"})
	if spec.Argv[5] != `W:\Win\System32\cmd.exe` {
		t.Fatalf("cmd SystemRoot argv = %v", spec.Argv)
	}
}

func envLookup(env []string, key string) (string, bool) {
	for _, e := range env {
		if i := strings.IndexByte(e, '='); i >= 0 && e[:i] == key {
			return e[i+1:], true
		}
	}
	return "", false
}

func assertEnv(t *testing.T, env []string, key, want string) {
	t.Helper()
	if got, ok := envLookup(env, key); !ok || got != want {
		t.Fatalf("env[%s] = %q (found=%v), want %q", key, got, ok, want)
	}
}

// ---- 安装指引 ----

func TestWindowsInstallInstructions(t *testing.T) {
	// created-not-on-token：指引注销重登
	msg := sandbox.WindowsInstallInstructions(nil, "", sandbox.WindowsGroupCreatedNotOnToken)
	if !strings.Contains(msg, "LOG OUT") {
		t.Fatalf("logout instructions = %q", msg)
	}

	// 缺省：缺省组名 + 无 sublayer
	msg = sandbox.WindowsInstallInstructions(nil, "", sandbox.WindowsGroupAbsent)
	if !strings.Contains(msg, "srt-win.exe install --name sandbox-runtime-net") ||
		!strings.Contains(msg, "one-time install") {
		t.Fatalf("absent instructions = %q", msg)
	}

	// SID + sublayer 透传
	msg = sandbox.WindowsInstallInstructions(
		&sandbox.WindowsGroupRef{GroupSID: "S-1-5-32-6"}, "{SUB}", sandbox.WindowsGroupAbsent)
	if !strings.Contains(msg, "--group-sid S-1-5-32-6") || !strings.Contains(msg, "--sublayer-guid {SUB}") {
		t.Fatalf("sid instructions = %q", msg)
	}
}

// ---- 依赖检查 ----

func TestCheckWindowsDependencies(t *testing.T) {
	ref := &sandbox.WindowsGroupRef{GroupSID: "S-1-5-32-5"}

	// 就绪：无错误无告警
	fakeSrtWin(t, `{"state":"ready"}`, `{"state":"installed","filters":3}`)
	if d := sandbox.CheckWindowsDependencies(ref, ""); d.HasErrors() || len(d.Warnings) != 0 {
		t.Fatalf("ready deps = %+v", d)
	}

	// 组未就绪：一条带指引的错误（含 sid）；组告警进 Warnings
	fakeSrtWin(t, `{"state":"created-not-on-token","sid":"S-1-5-32-5","warning":"heads up"}`, `{"state":"installed"}`)
	d := sandbox.CheckWindowsDependencies(ref, "")
	if len(d.Errors) != 1 || !strings.Contains(d.Errors[0], "created-not-on-token") ||
		!strings.Contains(d.Errors[0], "sid=S-1-5-32-5") || !strings.Contains(d.Errors[0], "LOG OUT") {
		t.Fatalf("group-not-ready errors = %v", d.Errors)
	}
	if len(d.Warnings) != 1 || d.Warnings[0] != "heads up" {
		t.Fatalf("warnings = %v", d.Warnings)
	}

	// 组未就绪 + WFP 未装：不重复报 WFP 错
	fakeSrtWin(t, `{"state":"absent"}`, `{"state":"absent"}`)
	if d = sandbox.CheckWindowsDependencies(ref, ""); len(d.Errors) != 1 || !strings.Contains(d.Errors[0], "Discriminator group") {
		t.Fatalf("no-duplicate-wfp errors = %v", d.Errors)
	}

	// 组就绪 + WFP 未装：单独 WFP 错误（带 sublayer 名）
	fakeSrtWin(t, `{"state":"ready"}`, `{"state":"absent"}`)
	if d = sandbox.CheckWindowsDependencies(ref, "{SUB}"); len(d.Errors) != 1 ||
		!strings.Contains(d.Errors[0], "WFP filters not installed under sublayer {SUB}") {
		t.Fatalf("wfp-missing errors = %v", d.Errors)
	}

	// 二进制缺失
	t.Setenv("SRT_WIN_PATH", filepath.Join(t.TempDir(), "missing.exe"))
	if d = sandbox.CheckWindowsDependencies(ref, ""); len(d.Errors) != 1 || !strings.Contains(d.Errors[0], "srt-win.exe not found") {
		t.Fatalf("missing binary errors = %v", d.Errors)
	}

	// 子命令失败：组状态失败即返（WFP 不再查）
	fakeSrtWin(t, `{"state":"ready"}`, `{"state":"installed"}`)
	t.Setenv("FAKE_FAIL", "group status")
	t.Setenv("FAKE_EXIT", "9")
	if d = sandbox.CheckWindowsDependencies(ref, ""); len(d.Errors) != 1 || !strings.Contains(d.Errors[0], "srt-win group status failed") {
		t.Fatalf("group status failed errors = %v", d.Errors)
	}
	// WFP 状态失败
	t.Setenv("FAKE_FAIL", "wfp status")
	if d = sandbox.CheckWindowsDependencies(ref, ""); len(d.Errors) != 1 || !strings.Contains(d.Errors[0], "srt-win wfp status failed") {
		t.Fatalf("wfp status failed errors = %v", d.Errors)
	}
}
