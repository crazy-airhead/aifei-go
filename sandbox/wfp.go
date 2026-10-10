// wfp.go windows WFP 后端（对标 WindowsSandboxBackend，逐字移植
// windows-sandbox-utils.ts）。文件名刻意不带 _windows 后缀——Go 的隐式
// GOOS 构建约束会把 _windows.go 从其他平台的构建里剔除（argv/env 生成
// 与 JSON 解析是纯字符串操作，全平台编译全平台可测；真 srt-win.exe/
// WFP 调用自然只在 windows 有意义——同款决策见 seatbelt.go/bwrap.go）。
//
// 架构：网络隔离由 srt-win.exe（Rust 助手，非本库产物）执行——它管理
// 一个本地甄别组、一套按该组 SID 键控的机器级 WFP 过滤器、以及一个把
// 目标进程放进受限令牌（组翻成 deny-only）+ 加固 job 的 exec 子命令。
// 本文件只是 srt-win CLI 的薄包装：全部状态来自活枚举（组经
// LookupAccountNameW + 令牌成员检查；WFP 经 sublayer 下 providerData
// 标签枚举）。真执行前置：一次性 install（组 + 当前用户 + WFP 过滤器，
// 单个自提权进程 = 一次 UAC）+ 注销重登（组 SID 进 TokenGroups）。
//
// 与 Java 的偏差：
//   - Java 手写的 indexOf 式简陋 JSON 解析 → Go 标准库 encoding/json
//     （对良好形成的 srt-win 输出行为等价，畸形输入错误面更干净）；
//   - wrap 的组引用参数复用 groupRefArgs（Java wrapCommandWithSandbox-
//     Windows 另写了一份只判 null 不判空的近似分支——空 SID 无实际调用
//     方，统一到唯一入口）；
//   - SandboxManager.checkWindowsDependencies 计算了 sublayerGuid 却调
//     旧版重载未传入（上游死变量缺陷——配了自定义 sublayer 时依赖检查
//     会查错 sublayer）；Go 的 Manager.CheckWindowsDependencies 传入
//     配置的 sublayerGuid；
//   - 安装指引文案去掉 npx 行（Node 生态专属），API 提示改 Go 名。
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/crazy-airhead/aifei-go/log"
)

// 缺省甄别组名与回环 PERMIT 端口段（须与代理实际监听一致；Java
// WindowsConfig 与 Backend 各持一份同值常量——Go 收口一处）。
const (
	DefaultWindowsGroupName     = "sandbox-runtime-net"
	srtWinExeName               = "srt-win.exe"
	srtWinTimeout               = 15 * time.Second
	srtWinExitUserCancelled     = 10 // 用户取消 UAC 提权
	srtWinExitGroupCreateFailed = 11 // 组创建失败
	srtWinExitWfpInstallFailed  = 12 // WFP 过滤器安装失败
	srtWinExitConfigConflict    = 13 // sublayer 下已有不同配置的过滤器
	srtWinExitKilled            = -1 // 超时强杀（Java destroyForcibly 同款）
)

var DefaultWindowsProxyPortRange = [2]int{60080, 60089}

// WindowsGroupRef 甄别组引用（按名或按 SID；SID 优先）。
type WindowsGroupRef struct {
	GroupName string
	GroupSID  string
}

// WindowsGroupStatus 甄别组在 SAM 与当前进程令牌中的状态。
type WindowsGroupStatus string

const (
	// WindowsGroupAbsent 组在 SAM 中不存在。
	WindowsGroupAbsent WindowsGroupStatus = "absent"
	// WindowsGroupCreatedNotOnToken 组在 SAM 中已建但不在调用方令牌里
	//（需要注销重登）。
	WindowsGroupCreatedNotOnToken WindowsGroupStatus = "created-not-on-token"
	// WindowsGroupReady 组存在且已在令牌中启用——沙箱可用。
	WindowsGroupReady WindowsGroupStatus = "ready"
)

// windowsGroupStatusFromJSONValue 解析 srt-win JSON 值（未知值报错——
// Java fromJsonValue 的 IllegalArgumentException 对应物）。
func windowsGroupStatusFromJSONValue(v string) (WindowsGroupStatus, error) {
	switch v {
	case string(WindowsGroupAbsent), string(WindowsGroupCreatedNotOnToken), string(WindowsGroupReady):
		return WindowsGroupStatus(v), nil
	}
	return "", fmt.Errorf("Unknown WindowsGroupStatus: %s", v)
}

// WindowsGroupStatusResult 组状态查询结果。
type WindowsGroupStatusResult struct {
	State   WindowsGroupStatus
	SID     string // S-1-… 形态的组 SID（可缺省）
	Warning string // srt-win 的可选告警
	Error   string // srt-win 的可选错误
}

// WindowsWfpStatus sublayer 下 WFP 过滤器集状态。
type WindowsWfpStatus string

const (
	// WindowsWfpAbsent sublayer 下无 srt-win 标签的过滤器。
	WindowsWfpAbsent WindowsWfpStatus = "absent"
	// WindowsWfpInstalled permit-group 与 block 过滤器齐备。
	WindowsWfpInstalled WindowsWfpStatus = "installed"
)

func windowsWfpStatusFromJSONValue(v string) (WindowsWfpStatus, error) {
	switch v {
	case string(WindowsWfpAbsent), string(WindowsWfpInstalled):
		return WindowsWfpStatus(v), nil
	}
	return "", fmt.Errorf("Unknown WindowsWfpStatus: %s", v)
}

// WindowsWfpStatusResult WFP 过滤器状态查询结果。
type WindowsWfpStatusResult struct {
	State     WindowsWfpStatus
	Filters   int     // sublayer 下 srt-win 标签过滤器数
	PortRange *[2]int // 过滤器标签里的回环 PERMIT 端口段（可缺省）
}

// WindowsInstallOptions 一次性安装选项（组引用 + 安装期字段）。
type WindowsInstallOptions struct {
	// GroupName 本地或域组名；缺省 sandbox-runtime-net。
	GroupName string
	// GroupSID S-1-… 形态组 SID（优先于 GroupName）。
	GroupSID string
	// UserSID 加入组的用户（缺省当前用户）。
	UserSID string
	// SublayerGUID WFP sublayer GUID；缺省 srt-win 编译期缺省值。
	SublayerGUID string
	// ProxyPortRange 回环 PERMIT 端口段 [lo, hi]（须与代理一致；
	// 缺省 [60080,60089]）。
	ProxyPortRange *[2]int
	// Force 替换同 sublayer 下配置不同（组 SID 或端口段）的既有安装；
	// 不设时 install 以「配置不同」拒绝而非静默覆写。
	Force bool
}

// WindowsInstallResult 安装结果（安装后组 + WFP 状态）。
type WindowsInstallResult struct {
	Group     *WindowsGroupStatusResult
	WFP       *WindowsWfpStatusResult
	Cancelled bool // 用户取消了 UAC 提权——非错误，重跑即可
}

// WindowsParams 包装参数（对标 WindowsSandboxParams）。
type WindowsParams struct {
	Command        string
	Group          *WindowsGroupRef
	HTTPProxyPort  *int
	SocksProxyPort *int
	BinShell       string
}

// ---- 二进制定位 ----

// SrtWinPath 定位 srt-win.exe（对标 getSrtWinPath）：SRT_WIN_PATH 环境变量
// （须存在）→ <应用根>/vendor/srt-win/target/release/srt-win.exe（本地
// cargo 构建）→ <应用根>/dist/vendor/…（构建产物形态）。应用根 = 从 CWD
// 向上（至多 5 层）找 vendor/ 目录或 package.json 的目录，找不到回落 CWD。
// 全部落空时报错列出已搜路径。
func SrtWinPath() (string, error) {
	// 1. 环境变量覆写
	if envPath := os.Getenv("SRT_WIN_PATH"); envPath != "" {
		if _, err := os.Stat(envPath); err == nil {
			return envPath, nil
		}
	}

	// 2. 应用根下的候选路径
	root := detectAppRoot()
	candidates := []string{
		filepath.Join(root, "vendor", "srt-win", "target", "release", srtWinExeName),
		filepath.Join(root, "dist", "vendor", "srt-win", "target", "release", srtWinExeName),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			log.Debug("[Sandbox Windows] using srt-win at %s", candidate)
			return candidate, nil
		}
	}

	// 报错列出已搜路径
	var searched []string
	if envPath := os.Getenv("SRT_WIN_PATH"); envPath != "" {
		searched = append(searched, envPath)
	}
	searched = append(searched, candidates...)
	return "", fmt.Errorf(
		"srt-win.exe not found. Set SRT_WIN_PATH or build with "+
			"`cargo build --release --manifest-path vendor/srt-win/Cargo.toml`. "+
			"Looked in: %s", strings.Join(searched, ", "))
}

// detectAppRoot 从 CWD 向上找 vendor/ 目录或 package.json（至多 5 层），
// 找不到回落 CWD（Java detectAppRoot 同款——user.dir 缺位的 JVM 分支在
// Go 不存在）。
func detectAppRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for i := 0; i < 5; i++ {
		if fi, err := os.Stat(filepath.Join(dir, "vendor")); err == nil && fi.IsDir() {
			return dir
		}
		if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return dir
}

// ---- 子进程执行 ----

// srtWinRunResult srt-win 子进程结果。
type srtWinRunResult struct {
	status int
	stdout string
	stderr string
}

// groupRefArgs 组引用的 CLI 参数（--group-sid 优先，--name 回落缺省组名；
// 对标 groupRefArgs）。
func groupRefArgs(ref *WindowsGroupRef) []string {
	if ref != nil && ref.GroupSID != "" {
		return []string{"--group-sid", ref.GroupSID}
	}
	name := DefaultWindowsGroupName
	if ref != nil && ref.GroupName != "" {
		name = ref.GroupName
	}
	return []string{"--name", name}
}

// runSrtWin 跑一个 srt-win 子进程并捕获输出（对标 runSrtWin）：15 秒
// 超时强杀（exit -1）；stdout/stderr 两侧并发缓冲（Java 先读尽 stdout
// 再读 stderr——stderr 管道写满时会互锁，Go exec 天然并发拷贝）。
func runSrtWin(args []string) (srtWinRunResult, error) {
	exe, err := SrtWinPath()
	if err != nil {
		return srtWinRunResult{}, fmt.Errorf("srt-win %s: %s", srtWinFirstArg(args), err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), srtWinTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Start(); err != nil {
		return srtWinRunResult{}, fmt.Errorf("srt-win %s: spawn failed: %s", srtWinFirstArg(args), err)
	}
	waitErr := cmd.Wait()
	res := srtWinRunResult{stdout: strings.TrimSpace(stdout.String()), stderr: strings.TrimSpace(stderr.String())}
	if ctx.Err() == context.DeadlineExceeded {
		res.status = srtWinExitKilled
	} else if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.status = exitErr.ExitCode()
		} else {
			res.status = -1
		}
	}
	return res, nil
}

func srtWinFirstArg(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// runSrtWinJSON 跑子命令并要求 exit 0，返回 stdout 供 JSON 解析
// （stderr 可能带 srt-win: 诊断——解析时忽略）。
func runSrtWinJSON(args []string) (string, error) {
	r, err := runSrtWin(args)
	if err != nil {
		return "", err
	}
	if r.status != 0 {
		detail := r.stderr
		if detail == "" {
			detail = r.stdout
		}
		return "", fmt.Errorf("srt-win %s exited %d: %s", strings.Join(args, " "), r.status, detail)
	}
	return r.stdout, nil
}

// ---- JSON 解析（Java 手写解析器的 encoding/json 对应物） ----

type windowsGroupStatusJSON struct {
	State   *string `json:"state"`
	SID     *string `json:"sid"`
	Warning *string `json:"warning"`
	Error   *string `json:"error"`
}

type windowsWfpStatusJSON struct {
	State     *string `json:"state"`
	Filters   int     `json:"filters"`
	PortRange []int   `json:"port_range"`
}

// parseGroupStatusJSON 解析 `srt-win group status` 的 JSON 输出：
// {"state":"ready","sid":"S-1-5-…","warning":"…","error":"…"}——state
// 必填，其余可选。
func parseGroupStatusJSON(raw string) (*WindowsGroupStatusResult, error) {
	var j windowsGroupStatusJSON
	if err := json.Unmarshal([]byte(raw), &j); err != nil || j.State == nil {
		return nil, fmt.Errorf("srt-win JSON output missing required field 'state': %s", raw)
	}
	state, err := windowsGroupStatusFromJSONValue(*j.State)
	if err != nil {
		return nil, err
	}
	return &WindowsGroupStatusResult{
		State:   state,
		SID:     derefStr(j.SID),
		Warning: derefStr(j.Warning),
		Error:   derefStr(j.Error),
	}, nil
}

// parseWfpStatusJSON 解析 `srt-win wfp status` 的 JSON 输出：
// {"state":"installed","filters":3,"port_range":[60080,60089]}——
// port_range 可缺省/畸形（<2 元素即弃，Java 同款宽容）。
func parseWfpStatusJSON(raw string) (*WindowsWfpStatusResult, error) {
	var j windowsWfpStatusJSON
	if err := json.Unmarshal([]byte(raw), &j); err != nil || j.State == nil {
		return nil, fmt.Errorf("srt-win JSON output missing required field 'state': %s", raw)
	}
	state, err := windowsWfpStatusFromJSONValue(*j.State)
	if err != nil {
		return nil, err
	}
	var portRange *[2]int
	if len(j.PortRange) >= 2 {
		portRange = &[2]int{j.PortRange[0], j.PortRange[1]}
	}
	return &WindowsWfpStatusResult{State: state, Filters: j.Filters, PortRange: portRange}, nil
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---- 状态 / 安装 API ----

// GetWindowsGroupStatus 查询甄别组在 SAM 与当前进程令牌中的状态
// （ready = 组存在且已在调用方令牌启用，即注销重登已完成）。
func GetWindowsGroupStatus(ref *WindowsGroupRef) (*WindowsGroupStatusResult, error) {
	raw, err := runSrtWinJSON(append([]string{"group", "status"}, groupRefArgs(ref)...))
	if err != nil {
		return nil, err
	}
	return parseGroupStatusJSON(raw)
}

// GetWindowsWfpStatus 查询 sublayer 下的 WFP 过滤器集（installed =
// permit-group 与 block 双过滤器齐备；探测按 providerData 标签——其他
// 工具装的无标签过滤器不计）。sublayerGUID 空 = 编译期缺省。
func GetWindowsWfpStatus(sublayerGUID string) (*WindowsWfpStatusResult, error) {
	args := []string{"wfp", "status"}
	if sublayerGUID != "" {
		args = append(args, "--sublayer-guid", sublayerGUID)
	}
	raw, err := runSrtWinJSON(args)
	if err != nil {
		return nil, err
	}
	return parseWfpStatusJSON(raw)
}

// InstallWindowsSandbox 一次性安装：建甄别组 + 加当前用户（或 userSid）
// + 装机器级 WFP 过滤器——单个自提权进程（一次 UAC）。幂等。注销前网络
// 不受影响（组不在令牌里时 WFP 过滤器 0 PERMIT 非成员流量照常走）。
// 用户取消 UAC 时返回 Cancelled=true 而非错误——取消是用户选择。
// srt-win install 退出码契约：0 ok / 10 取消提权 / 11 组创建失败 /
// 12 WFP 安装失败 / 13 同 sublayer 已有不同配置（--force 替换）/ 1 其他。
func InstallWindowsSandbox(opts *WindowsInstallOptions) (*WindowsInstallResult, error) {
	if opts == nil {
		opts = &WindowsInstallOptions{}
	}
	ref := &WindowsGroupRef{GroupName: opts.GroupName, GroupSID: opts.GroupSID}

	args := append([]string{"install"}, groupRefArgs(ref)...)
	if opts.UserSID != "" {
		args = append(args, "--user-sid", opts.UserSID)
	}
	if opts.SublayerGUID != "" {
		args = append(args, "--sublayer-guid", opts.SublayerGUID)
	}
	if opts.ProxyPortRange != nil {
		args = append(args, "--proxy-port-range",
			strconv.Itoa(opts.ProxyPortRange[0])+"-"+strconv.Itoa(opts.ProxyPortRange[1]))
	}
	if opts.Force {
		args = append(args, "--force")
	}

	r, err := runSrtWin(args)
	if err != nil {
		return nil, err
	}
	detail := r.stderr
	if detail == "" {
		detail = r.stdout
	}
	log.Debug("[Sandbox Windows] install exit=%d: %s", r.status, detail)

	switch r.status {
	case 0: // ok
	case srtWinExitUserCancelled:
		return newWindowsInstallResult(ref, opts.SublayerGUID, true)
	case srtWinExitGroupCreateFailed:
		return nil, fmt.Errorf("srt-win install: group create failed: %s", detail)
	case srtWinExitWfpInstallFailed:
		return nil, fmt.Errorf("srt-win install: WFP filter install failed: %s", detail)
	case srtWinExitConfigConflict:
		return nil, fmt.Errorf(
			"srt-win install: filters already exist under this sublayer with "+
				"different configuration (group SID or port range). "+
				"Pass Force:true to replace, or pick a different SublayerGUID. "+
				"Output: %s", detail)
	default:
		return nil, fmt.Errorf("srt-win install failed (exit %d): %s", r.status, detail)
	}
	return newWindowsInstallResult(ref, opts.SublayerGUID, false)
}

// newWindowsInstallResult 安装后取组 + WFP 状态（Java 内联两处调用的
// 收口）。
func newWindowsInstallResult(ref *WindowsGroupRef, sublayerGUID string, cancelled bool) (*WindowsInstallResult, error) {
	group, err := GetWindowsGroupStatus(ref)
	if err != nil {
		return nil, err
	}
	wfp, err := GetWindowsWfpStatus(sublayerGUID)
	if err != nil {
		return nil, err
	}
	return &WindowsInstallResult{Group: group, WFP: wfp, Cancelled: cancelled}, nil
}

// UninstallWindowsSandbox 移除 sublayer 下的 WFP 过滤器（一次 UAC）。
// 幂等。不删甄别组——组成员是持久用户状态，删了会令每个成员重做注销
// 舞步（彻底清场另调 DeleteWindowsGroup）。返回是否用户取消 UAC。
func UninstallWindowsSandbox(sublayerGUID string) (bool, error) {
	args := []string{"uninstall"}
	if sublayerGUID != "" {
		args = append(args, "--sublayer-guid", sublayerGUID)
	}
	r, err := runSrtWin(args)
	if err != nil {
		return false, err
	}
	detail := r.stderr
	if detail == "" {
		detail = r.stdout
	}
	log.Debug("[Sandbox Windows] uninstall exit=%d: %s", r.status, detail)

	if r.status == srtWinExitUserCancelled {
		return true, nil
	}
	if r.status != 0 {
		return false, fmt.Errorf("srt-win uninstall failed (exit %d): %s", r.status, detail)
	}
	return false, nil
}

// DeleteWindowsGroup 删除甄别组（与 Uninstall 分离——卸载重装不必强迫
// 每个成员重新注销）。需提权；幂等（组不存在即空操作）。
func DeleteWindowsGroup(ref *WindowsGroupRef) error {
	return runSrtWinAction(append([]string{"group", "delete"}, groupRefArgs(ref)...),
		"srt-win group delete failed", "Requires elevation.")
}

// CreateWindowsGroup 细粒度原语：建组 + 加当前用户（或 userSid）。多数
// 调用方应走 InstallWindowsSandbox；此口为分开管理组与 WFP 的企业/CI
// 流程保留。需提权；幂等。
func CreateWindowsGroup(ref *WindowsGroupRef, userSID string) error {
	args := append([]string{"group", "create"}, groupRefArgs(ref)...)
	if userSID != "" {
		args = append(args, "--user-sid", userSID)
	}
	return runSrtWinAction(args, "srt-win group create failed",
		"This requires elevation — run as administrator.")
}

// CreateWindowsWfp 细粒度原语：装 sublayer 下按组 SID 键控的机器级 WFP
// 过滤器集。需提权；幂等（重跑替换该 sublayer 下既有 srt-win 标签过滤
// 器）。
func CreateWindowsWfp(ref *WindowsGroupRef, sublayerGUID string, proxyPortRange *[2]int) error {
	args := append([]string{"wfp", "install"}, groupRefArgs(ref)...)
	if sublayerGUID != "" {
		args = append(args, "--sublayer-guid", sublayerGUID)
	}
	if proxyPortRange != nil {
		args = append(args, "--proxy-port-range",
			strconv.Itoa(proxyPortRange[0])+"-"+strconv.Itoa(proxyPortRange[1]))
	}
	return runSrtWinAction(args, "srt-win wfp install failed",
		"This requires elevation — run as administrator.")
}

// runSrtWinAction 动作型子命令的公共收口：非 0 即错（退出码 + 提权提示
// + 输出）。
func runSrtWinAction(args []string, prefix, elevationHint string) error {
	r, err := runSrtWin(args)
	if err != nil {
		return err
	}
	detail := r.stderr
	if detail == "" {
		detail = r.stdout
	}
	if r.status != 0 {
		return fmt.Errorf("%s (exit %d). %s Output: %s", prefix, r.status, elevationHint, detail)
	}
	log.Debug("[Sandbox Windows] %s: %s", args[0], detail)
	return nil
}

// ---- 包装命令 ----

// WrapCommandWindows 构建沙箱内执行 command 的 spawn 描述（对标
// wrapCommandWithSandboxWindows）。调用方必须以 shell:false 语义 spawn
// Argv——这是把不可信字节挡在宿主 shell 之外的安全边界（内层 cmd.exe
// 在沙箱里跑）。代理环境变量单一来源于 GenerateProxyEnvVars：srt-win
// exec 原样转发自身环境给沙箱子进程，故此处把整套代理变量并进完整
// 环境再交出（TMPDIR 剔除——windows 不用）。macOS/linux 上也可调用
// （生成 argv/env 纯字符串操作），真执行自然只在 windows 有意义。
func WrapCommandWindows(params *WindowsParams) (*SpawnSpec, error) {
	exe, err := SrtWinPath()
	if err != nil {
		return nil, err
	}
	argv := []string{exe, "exec"}
	argv = append(argv, groupRefArgs(params.Group)...)
	argv = append(argv, "--")

	// shell 选择：pwsh / powershell（名字包含即命中，大小写不敏感）→
	// -NoProfile -Command；其余（含空）→ SystemRoot 的 cmd.exe /d /s /c
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	shell := strings.ToLower(params.BinShell)
	if shell == "pwsh" || strings.Contains(shell, "powershell") {
		psExe := "pwsh.exe"
		if shell != "pwsh" {
			psExe = systemRoot + `\System32\WindowsPowerShell\v1.0\powershell.exe`
		}
		argv = append(argv, psExe, "-NoProfile", "-Command", params.Command)
	} else {
		argv = append(argv, systemRoot+`\System32\cmd.exe`, "/d", "/s", "/c", params.Command)
	}

	// 代理环境变量并进当前环境（TMPDIR 从生成集剔除——windows 不用，
	// 子进程继承宿主原值；Java generated.remove("TMPDIR") 同款）
	generated := GenerateProxyEnvVars(params.HTTPProxyPort, params.SocksProxyPort, "")
	proxyVars := make([]string, 0, len(generated))
	for _, e := range generated {
		if !strings.HasPrefix(e, "TMPDIR=") {
			proxyVars = append(proxyVars, e)
		}
	}
	env := overlayEnv(currentEnv(), proxyVars)
	return &SpawnSpec{Argv: argv, Env: env}, nil
}

// overlayEnv 把 K=V 串列表并进既有环境（同名键替换，保持原相对次序，
// 新键追加）。Java 的 HashMap putAll 对应物。
func overlayEnv(base []string, overlay []string) []string {
	keys := make(map[string]bool, len(overlay))
	for _, e := range overlay {
		if i := strings.IndexByte(e, '='); i >= 0 {
			keys[e[:i]] = true
		}
	}
	out := make([]string, 0, len(base)+len(overlay))
	for _, e := range base {
		i := strings.IndexByte(e, '=')
		if i >= 0 && keys[e[:i]] {
			continue
		}
		out = append(out, e)
	}
	return append(out, overlay...)
}

// ---- 安装指引（错误信息用） ----

// WindowsInstallInstructions 按观测到的组状态定制安装指引文案。
func WindowsInstallInstructions(ref *WindowsGroupRef, sublayerGUID string, groupState WindowsGroupStatus) string {
	if groupState == WindowsGroupCreatedNotOnToken {
		return "The discriminator group exists but is not yet in this session's " +
			"token. LOG OUT and back in to pick up the new group membership " +
			"(it enters TokenGroups at logon). Network is not disrupted " +
			"meanwhile — WFP filter-0 PERMITs traffic while the group is absent " +
			"from your token."
	}

	g := strings.Join(groupRefArgs(ref), " ")
	sl := ""
	if sublayerGUID != "" {
		sl = " --sublayer-guid " + sublayerGUID
	}

	return "Windows sandbox needs a one-time install (one UAC prompt):\n" +
		"  call sandbox.InstallWindowsSandbox(), or run " +
		"`srt-win.exe install " + g + sl + "` directly —\n" +
		"then LOG OUT and back in (the group SID enters TokenGroups at logon).\n" +
		"Network is not disrupted before the logout: while the group is absent " +
		"from your token, WFP filter-0 PERMITs all traffic."
}

// ---- 依赖 / 就绪检查 ----

// CheckWindowsDependencies windows 后端就绪检查（错误阻断初始化，告警仅
// 提示）：srt-win.exe 在位 → 组 ready → sublayer 下 WFP 已装。组未就绪
// 时的组错误已带正确指引——WFP 再未装不重复报错（仅在组 ready 而过滤器
// 被单独卸掉时补一条）。
func CheckWindowsDependencies(groupRef *WindowsGroupRef, sublayerGUID string) *DependencyCheck {
	check := &DependencyCheck{}

	// 1. 二进制在位
	if _, err := SrtWinPath(); err != nil {
		check.Errors = append(check.Errors, err.Error())
		return check
	}

	// 2. 组 ready（存在且在调用方令牌中启用）
	gs, err := GetWindowsGroupStatus(groupRef)
	if err != nil {
		check.Errors = append(check.Errors, "srt-win group status failed: "+err.Error())
		return check
	}
	if gs.State != WindowsGroupReady {
		msg := "Discriminator group is " + string(gs.State)
		if gs.SID != "" {
			msg += " (sid=" + gs.SID + ")"
		}
		check.Errors = append(check.Errors, msg+". "+
			WindowsInstallInstructions(groupRef, sublayerGUID, gs.State))
	}
	if gs.Warning != "" {
		check.Warnings = append(check.Warnings, gs.Warning)
	}

	// 3. sublayer 下 WFP 过滤器已装
	ws, err := GetWindowsWfpStatus(sublayerGUID)
	if err != nil {
		check.Errors = append(check.Errors, "srt-win wfp status failed: "+err.Error())
		return check
	}
	if ws.State != WindowsWfpInstalled {
		// 组也未就绪时上面的组错误已给对指引，不重复；仅在组 ready 而
		// 过滤器被单独卸掉时另报 WFP 错误
		if gs.State == WindowsGroupReady {
			sl := "(default)"
			if sublayerGUID != "" {
				sl = sublayerGUID
			}
			check.Errors = append(check.Errors, "WFP filters not installed under sublayer "+sl+". "+
				WindowsInstallInstructions(groupRef, sublayerGUID, WindowsGroupAbsent))
		}
	} else if ws.PortRange != nil {
		log.Debug("[Sandbox Windows] WFP installed: %d filters, proxy port range %d-%d",
			ws.Filters, ws.PortRange[0], ws.PortRange[1])
	}

	return check
}
