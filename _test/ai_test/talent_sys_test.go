// talent_sys_test.go P5-t2 sys 能力包黑盒测试：三门二进制探测黄金用例
// （Java ProcessExecutorOutputTest 搬译）、LLM 截断、魔数识别、二进制摘
// 要双分支、ExecuteCmd/ExecuteCode 语义（合流/退出码忽略/超时强杀/输出上
// 限截杀/cwd 自建）、Shell/Python/Nodejs/Clock 四 talent 与 ai 管线端到端。
package ai_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/talent/sys"
)

// ---- 三门二进制探测（Java 14 黄金用例搬译）----

func TestIsLikelyBinaryGolden(t *testing.T) {
	mavenLine := strings.Repeat("[INFO] Tests run: 3, Failures: 0, Errors: 0, Skipped: 0", 1)
	cases := []struct {
		name string
		in   []byte
		want bool
	}{
		{"NUL 单字节", []byte{0x00}, true},
		{"纯文本", []byte("hello world"), false},
		{"混合空白文本", []byte("line1\nline2\ttabbed\r\nline3"), false},
		{"UTF-8 中文", []byte("你好，世界！这是一段中文文本，用于验证多字节字符不被误判。"), false},
		{"ANSI 颜色码", []byte("\x1b[32mSUCCESS\x1b[0m operation completed\n"), false},
		{"Maven 构建日志", []byte(strings.Repeat(mavenLine+"\n", 100)), false},
		{"进度条覆盖符", []byte(strings.Repeat("\r[INFO] Downloading: 50%\x1b[K\n", 50)), false},
		{"63 字符游程", []byte(strings.Repeat(strings.Repeat("a", 63)+"\n", 300)), false},
		{"控制字节交替", ctrlBytes(0x01, 0x02, 1000), true},
		{"控制区 + 短文本尾", append(cycleBytes(0x01, 0x1E, 400), strings.Repeat("A", 64)...), true},
		{"长文本尾部 NUL", append([]byte(strings.Repeat("A", 100000)), 0x00), true},
		{"退格进度", []byte("progress: 50%\b\b\b\b100%\n"), false},
		{"换页符分页", []byte("\x0cPage 1\x0cPage 2\n"), false},
		{"WASM 魔数", []byte{0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00}, true},
	}
	for _, c := range cases {
		if got := sys.IsLikelyBinary(c.in); got != c.want {
			t.Errorf("%s: IsLikelyBinary = %v, want %v", c.name, got, c.want)
		}
	}
}

// ctrlBytes 交替填充两控制字节。
func ctrlBytes(a, b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		if i%2 == 0 {
			out[i] = a
		} else {
			out[i] = b
		}
	}
	return out
}

// cycleBytes 在 [from,to] 闭区间循环填充。
func cycleBytes(from, to byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = from + byte(i)%(to-from+1)
	}
	return out
}

// ---- 截断 / 魔数 / 摘要 ----

func TestTruncateForLLM(t *testing.T) {
	if got := sys.TruncateForLLM("short", 64000); got != "short" {
		t.Fatalf("short = %q", got)
	}
	long := strings.Repeat("a", 100000) + strings.Repeat("z", 100000)
	got := sys.TruncateForLLM(long, 64000)
	if !strings.HasPrefix(got, strings.Repeat("a", 100)) || !strings.HasSuffix(got, strings.Repeat("z", 100)) {
		t.Fatal("head/tail not preserved")
	}
	if !strings.Contains(got, "输出过大已截断：总计 200000 字符，省略中间 136000 字符") {
		t.Fatalf("marker missing: %q", clip(got))
	}
	// maxChars<=0 → 缺省 64000
	if g2 := sys.TruncateForLLM(long, 0); g2 == long {
		t.Fatal("default max not applied")
	}
}

func TestDetectFileType(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{0xCA, 0xFE, 0xBA, 0xBE, 0x00}, "Java Class"},
		{[]byte{0x50, 0x4B, 0x03, 0x04, 0x01}, "ZIP/JAR/WAR"},
		{[]byte{0x7F, 0x45, 0x4C, 0x46, 0x02}, "ELF"},
		{[]byte{0x89, 0x50, 0x4E, 0x47, 0x0D}, "PNG"},
		{[]byte{0xFF, 0xD8, 0xFF, 0xE0}, "JPEG"},
		{[]byte{0x1F, 0x8B, 0x08}, "GZIP"},
		{[]byte{0x00, 0x61, 0x73, 0x6D}, "WASM"},
		{[]byte("plain text"), ""},
	}
	for _, c := range cases {
		if got := sys.DetectFileType(c.in); got != c.want {
			t.Errorf("DetectFileType(% X) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSummarizeBinaryOutput(t *testing.T) {
	// 纯二进制：魔数 + 十六进制预览分支
	png := append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A}, ctrlBytes(0x01, 0x02, 64)...)
	got := sys.SummarizeBinaryOutput(png)
	for _, want := range []string{"检测到二进制输出", "识别类型: PNG", "前 32 字节: 89 50 4E 47", "jar tf"} {
		if !strings.Contains(got, want) {
			t.Fatalf("binary summary missing %q:\n%s", want, got)
		}
	}
	// 大部分可读：解码展示分支（>6000 字符触发中间省略）
	mostly := append([]byte(strings.Repeat("A", 7000)), 0x00)
	got = sys.SummarizeBinaryOutput(mostly)
	for _, want := range []string{
		"输出中检测到二进制特征，但大部分内容可读",
		"[中间省略 1001 字符]",
		"(原始大小: 7001 字节)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("readable summary missing %q:\n%s", want, clip(got))
		}
	}
}

// clip 测试失败输出截断（避免刷屏）。
func clip(s string) string {
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}

// ---- Executor 语义 ----

func TestExecutorExecuteCmd(t *testing.T) {
	dir := t.TempDir()
	e := sys.NewExecutor()
	ctx := context.Background()

	if got := e.ExecuteCmd(ctx, dir, []string{"echo", "hello"}, nil, 0); got != "hello" {
		t.Fatalf("echo = %q", got)
	}
	// 退出码忽略（仅看输出，Java 同款）
	if got := e.ExecuteCmd(ctx, dir, []string{"sh", "-c", "echo out; exit 3"}, nil, 0); got != "out" {
		t.Fatalf("exit-code-ignored = %q", got)
	}
	// stderr 合流
	if got := e.ExecuteCmd(ctx, dir, []string{"sh", "-c", "echo err 1>&2"}, nil, 0); got != "err" {
		t.Fatalf("stderr-merged = %q", got)
	}
	// 空输出 → 执行成功
	if got := e.ExecuteCmd(ctx, dir, []string{"true"}, nil, 0); got != "执行成功" {
		t.Fatalf("empty = %q", got)
	}
	// cwd 缺失自建
	nested := dir + "/nested/deep"
	if got := e.ExecuteCmd(ctx, nested, []string{"echo", "cwd"}, nil, 0); got != "cwd" {
		t.Fatalf("nested-cwd = %q", got)
	}
	// 启动失败
	if got := e.ExecuteCmd(ctx, dir, []string{"definitely-not-a-command-xyz"}, nil, 0); !strings.HasPrefix(got, "系统失败: ") {
		t.Fatalf("start-fail = %q", got)
	}
	// 超时强杀
	start := time.Now()
	got := e.ExecuteCmd(ctx, dir, []string{"sh", "-c", "sleep 5"}, nil, 300)
	if got != "执行超时：运行时间超过 300 毫秒。" {
		t.Fatalf("timeout = %q", got)
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("kill not effective, elapsed %v", elapsed)
	}
}

// 输出上限截杀：1MB→64KB 上限下 /dev/zero 流被截停 + 二进制摘要收敛。
func TestExecutorOutputCap(t *testing.T) {
	e := sys.NewExecutor()
	e.SetMaxOutputSize(64 * 1024)
	got := e.ExecuteCmd(context.Background(), t.TempDir(),
		[]string{"head", "-c", "200000", "/dev/zero"}, nil, 10_000)
	if !strings.Contains(got, "检测到二进制输出") || !strings.Contains(got, "约 65536 字节") {
		t.Fatalf("capped output = %s", clip(got))
	}
}

func TestExecutorExecuteCode(t *testing.T) {
	dir := t.TempDir()
	e := sys.NewExecutor()
	got := e.ExecuteCode(context.Background(), dir, "echo hi", "sh", ".sh", nil, 5000)
	if got != "hi" {
		t.Fatalf("ExecuteCode = %q", got)
	}
	// envs 透传
	got = e.ExecuteCode(context.Background(), dir, "echo $MARK", "sh", ".sh",
		map[string]string{"MARK": "env-ok"}, 5000)
	if got != "env-ok" {
		t.Fatalf("env passthrough = %q", got)
	}
}

// ---- talents ----

func TestShellTalent(t *testing.T) {
	dir := t.TempDir()
	s := sys.NewShell(dir)
	if s.Name() != "shell" || s.Description() != "Shell 专家：执行系统命令" {
		t.Fatalf("name/desc = %q/%q", s.Name(), s.Description())
	}
	tools := s.Tools(nil)
	if len(tools) != 1 || tools[0].Name() != "execute_shell" {
		t.Fatalf("tools = %v", tools)
	}

	res := callFileTool(t, tools[0], map[string]any{"code": "echo shell-works"})
	if res.IsError || res.ContentText() != "shell-works" {
		t.Fatalf("execute_shell = %q err=%v", res.ContentText(), res.IsError)
	}

	// 超时参数生效
	res = callFileTool(t, tools[0], map[string]any{"code": "sleep 5", "timeout": 300})
	if res.IsError || !strings.Contains(res.ContentText(), "执行超时：运行时间超过 300 毫秒。") {
		t.Fatalf("execute_shell timeout = %q err=%v", res.ContentText(), res.IsError)
	}

	// 指令块：Terminal 环境状态 + 安全禁令
	instr := s.Instruction(nil)
	for _, want := range []string{
		"## Terminal 环境状态", "**当前时间**", "**运行环境**", "**终端类型**",
		"进程安全约束", "PID", "严禁执行任何可能导致宿主进程退出的命令",
	} {
		if !strings.Contains(instr, want) {
			t.Fatalf("instruction missing %q:\n%s", want, instr)
		}
	}
}

func TestPythonNodejsTalents(t *testing.T) {
	dir := t.TempDir()

	py := sys.NewPython(dir)
	if py.Name() != "python" {
		t.Fatalf("python name = %q", py.Name())
	}
	res := callFileTool(t, py.Tools(nil)[0], map[string]any{"code": "print(1+1)"})
	if strings.HasPrefix(res.ContentText(), "系统失败: ") {
		t.Skipf("python unavailable: %s", res.ContentText())
	}
	if res.IsError || res.ContentText() != "2" {
		t.Fatalf("execute_python = %q err=%v", res.ContentText(), res.IsError)
	}

	nd := sys.NewNodejs(dir)
	if nd.Name() != "nodejs" {
		t.Fatalf("nodejs name = %q", nd.Name())
	}
	res = callFileTool(t, nd.Tools(nil)[0], map[string]any{"code": `console.log("ok")`})
	if strings.HasPrefix(res.ContentText(), "系统失败: ") {
		t.Skipf("node unavailable: %s", res.ContentText())
	}
	if res.IsError || res.ContentText() != "ok" {
		t.Fatalf("execute_js = %q err=%v", res.ContentText(), res.IsError)
	}
}

func TestClockTalent(t *testing.T) {
	c := sys.NewClock()
	if c.Name() != "clock" {
		t.Fatalf("clock name = %q", c.Name())
	}
	res := callFileTool(t, c.Tools(nil)[0], map[string]any{})
	if res.IsError {
		t.Fatalf("get_current_time err = %v", res.ContentText())
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} 星期[一二三四五六日] .+$`).
		MatchString(res.ContentText()) {
		t.Fatalf("clock format = %q", res.ContentText())
	}
	// 固定时点格式（Java yyyy-MM-dd HH:mm:ss EEEE z，中文星期）
	fixed := sys.FormatClock(time.Date(2026, 1, 30, 21, 0, 0, 0, time.UTC))
	if fixed != "2026-01-30 21:00:00 星期五 UTC" {
		t.Fatalf("FormatClock = %q", fixed)
	}
}

// ---- 管线端到端：指令块 + 工具部署 + meta 染色 ----

func TestSysTalentPipeline(t *testing.T) {
	f := newFakeOpenAI(t, respTextJSON)
	m := fakeModel(t, f)
	dir := t.TempDir()

	_, err := m.Prompt("看下时间再跑个命令").Options(func(o *ai.ChatOptions) {
		o.Talents = []ai.Talent{sys.NewShell(dir), sys.NewClock()}
	}).Call()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	// 工具部署
	body := f.request(t, 0).body
	tools, _ := body["tools"].([]any)
	got := map[string]bool{}
	for _, tv := range tools {
		tm, _ := tv.(map[string]any)
		got[tm["function"].(map[string]any)["name"].(string)] = true
	}
	if !got["execute_shell"] || !got["get_current_time"] {
		t.Fatalf("tools not deployed: %v", got)
	}

	// 指令块：shell 带描述与 Terminal 环境状态；clock 纯工具集不产块
	msgs := f.messagesOf(t, 0)
	sysPrompt, _ := msgs[0]["content"].(string)
	if !strings.Contains(sysPrompt, "# [Talent: shell] - Shell 专家：执行系统命令") ||
		!strings.Contains(sysPrompt, "## Terminal 环境状态") {
		t.Fatalf("shell instruction block missing:\n%s", sysPrompt)
	}
	if strings.Contains(sysPrompt, "# [Talent: clock]") {
		t.Fatalf("clock is a pure toolset, must not emit instruction block:\n%s", sysPrompt)
	}

	// meta 染色
	opts := &ai.ChatOptions{}
	opts.Talents = []ai.Talent{sys.NewShell(dir), sys.NewClock()}
	ai.ActiveTalents(opts, nil, new(strings.Builder))
	if len(opts.Tools) != 2 {
		t.Fatalf("deployed tools = %d", len(opts.Tools))
	}
	for _, tl := range opts.Tools {
		name, _ := tl.Meta()["talent"].(string)
		if name != "shell" && name != "clock" {
			t.Fatalf("tool %q meta.talent = %q", tl.Name(), name)
		}
	}
}
