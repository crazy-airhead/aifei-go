// executor.go 进程执行器（对标 solon-ai-talent-sys:ProcessExecutor）。核
// 心语义：临时脚本落盘执行（POSIX 权限 600）、stdout/stderr 合流、超时
// 强杀、输出字节上限（超限即杀——内存保护，Java destroyForcibly 同款）、
// 二进制输出探测与摘要、LLM 视角的头尾截断。与 Java 的结果面一致：执
// 行失败/超时均为普通结果串（中文文案逐字保留），不向上抛错。
package sys

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultTimeoutMS 缺省超时（Java DEFAULT_TIMEOUT_MS）。
	DefaultTimeoutMS = 120_000
	// DefaultLLMOutputChars 结果字符截断上限（Java DEFAULT_LLM_OUTPUT_CHARS）。
	DefaultLLMOutputChars = 64_000
	// binaryProbeSample 二进制探测的采样上限（Java BINARY_PROBE_SAMPLE）。
	binaryProbeSample = 8_192

	maxOutputSizeDefault = 1024 * 1024
)

// Executor 进程执行器。零值可用（缺省输出上限 1MB）。
type Executor struct {
	maxOutputSize int
}

// NewExecutor 构造执行器。
func NewExecutor() *Executor { return &Executor{maxOutputSize: maxOutputSizeDefault} }

// SetMaxOutputSize 覆写输出字节上限（<=0 忽略）。
func (e *Executor) SetMaxOutputSize(n int) {
	if n > 0 {
		e.maxOutputSize = n
	}
}

// IsCommandAvailable 探测命令是否可用（which/where，2 秒窗口）。
func (e *Executor) IsCommandAvailable(ctx context.Context, cmd string) bool {
	probe := "which"
	if runtime.GOOS == "windows" {
		probe = "where"
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return exec.CommandContext(cctx, probe, cmd).Run() == nil
}

// ProbePythonCommand python3 优先、python 兜底（均不可用返回空串）。
func (e *Executor) ProbePythonCommand(ctx context.Context) string {
	for _, cmd := range []string{"python3", "python"} {
		if e.IsCommandAvailable(ctx, cmd) {
			return cmd
		}
	}
	return ""
}

// ProbeNodeCommand node 优先、nodejs 兜底。
func (e *Executor) ProbeNodeCommand(ctx context.Context) string {
	for _, cmd := range []string{"node", "nodejs"} {
		if e.IsCommandAvailable(ctx, cmd) {
			return cmd
		}
	}
	return ""
}

// ExecuteCode 代码落盘执行：临时脚本（aifei-script-* + ext）+ 命令拼接
// （cmd 可含参数，空白切分后追加脚本路径）。.bat 前置 @chcp 65001（UTF-8
// 控制台，Windows 专用）。脚本用毕即删。
func (e *Executor) ExecuteCode(ctx context.Context, root, code, cmd, ext string, envs map[string]string, timeoutMS int) string {
	if ext == ".bat" {
		code = "@chcp 65001 > nul\r\n" + code
	}
	tmp, err := os.CreateTemp("", "aifei-script-*"+ext)
	if err != nil {
		return "代码执行失败: " + err.Error()
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)
	// POSIX 收紧权限（脚本可能含敏感内容；失败忽略——Java UnsupportedOperationException 同款宽容）
	if runtime.GOOS != "windows" {
		_ = os.Chmod(path, 0o600)
	}
	if err := os.WriteFile(path, []byte(code), 0o600); err != nil {
		return "代码执行失败: " + err.Error()
	}
	argv := append(strings.Fields(cmd), path)
	return e.ExecuteCmd(ctx, root, argv, envs, timeoutMS)
}

// ExecuteCmd 命令执行：cwd=root、env 合并、stdout/stderr 合流、超时强杀、
// 输出上限截杀；退出码不参与判定（仅看输出，Java 同款）。timeoutMS<=0
// 取缺省。结果恒为字符串：超时/失败文案逐字对标 Java。
func (e *Executor) ExecuteCmd(ctx context.Context, root string, argv []string, envs map[string]string, timeoutMS int) string {
	if len(argv) == 0 {
		return "系统失败: empty command"
	}
	if timeoutMS <= 0 {
		timeoutMS = DefaultTimeoutMS
	}
	// cwd 缺失即建（Java ProcessBuilder 因缺目录抛 IOException，Go 收口为结果串）
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "系统失败: " + err.Error()
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = root
	c.Env = compactEnv(envs)
	out := &cappedOutput{max: e.maxOutputSize}
	c.Stdout, c.Stderr = out, out
	if err := c.Start(); err != nil {
		return "系统失败: " + err.Error()
	}
	out.bind(c.Process)

	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	timer := time.NewTimer(time.Duration(timeoutMS) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		_ = c.Process.Kill()
		<-done
		return fmt.Sprintf("执行超时：运行时间超过 %d 毫秒。", timeoutMS)
	case <-ctx.Done():
		_ = c.Process.Kill()
		<-done
		return "系统失败: " + ctx.Err().Error()
	}

	raw := out.bytes()
	if IsLikelyBinary(raw) {
		return SummarizeBinaryOutput(raw)
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return "执行成功"
	}
	return TruncateForLLM(text, DefaultLLMOutputChars)
}

// cappedOutput 输出累积器：到上限即丢弃后续并强杀进程（内存保护）。
type cappedOutput struct {
	mu   sync.Mutex
	max  int
	buf  []byte
	proc *os.Process
}

func (o *cappedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.buf) < o.max {
		o.buf = append(o.buf, p...)
		if len(o.buf) > o.max {
			o.buf = o.buf[:o.max]
		}
		if len(o.buf) == o.max && o.proc != nil {
			_ = o.proc.Kill()
		}
	}
	return len(p), nil
}

func (o *cappedOutput) bind(p *os.Process) {
	o.mu.Lock()
	o.proc = p
	o.mu.Unlock()
}

func (o *cappedOutput) bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf
}

// IsLikelyBinary 三门二进制探测（Java isLikelyBinary 逐门移植）：
//  1. 前 64KB 出现 NUL 即二进制；
//  2. 三区域采样（头/中/尾各 1/3，去重），统计可疑控制字符/可打印字符/
//     最长可打印游程——白名单 \n\r\t\b\f ESC 不计可疑，>=0x80（UTF-8
//     续字节）忽略，采样中撞 NUL 立即判二进制；
//  3. 自适应阈值：长游程(>120) 需可疑占比 >50% 才判二进制；可打印占比
//     >70% 时需 >45%；其余 >30%。
func IsLikelyBinary(b []byte) bool {
	for i := 0; i < len(b) && i < 64*1024; i++ {
		if b[i] == 0 {
			return true
		}
	}
	sample := len(b)
	if sample > binaryProbeSample {
		sample = binaryProbeSample
	}
	regionSize := sample / 3
	if regionSize < 1 {
		regionSize = 1
	}
	// 按绝对位置去重：len<=8192 时三段为连续三分（舍入重叠，需去重）；
	// len>8192 时三段本就不相交（0 与 len/2-rs/2 与 len-rs 互不覆盖），
	// 免去重分配。
	var visited []bool
	if len(b) <= binaryProbeSample {
		visited = make([]bool, len(b))
	}
	var suspicious, printable, checked, maxRun, run int
	regions := []int{0, len(b)/2 - regionSize/2, len(b) - regionSize}
	for _, off := range regions {
		for i := 0; i < regionSize; i++ {
			idx := off + i
			if idx < 0 || idx >= len(b) {
				continue
			}
			if visited != nil {
				if visited[idx] {
					continue
				}
				visited[idx] = true
			}
			c := b[idx]
			checked++
			switch {
			case c == 0x00:
				return true
			case c >= 0x80: // UTF-8 续字节：忽略
			case c < 0x20:
				switch c {
				case '\n', '\r', '\t', '\b', '\f', 0x1B: // 白名单
				default:
					suspicious++
				}
				run = 0
			default: // 0x20–0x7E 可打印
				printable++
				run++
				if run > maxRun {
					maxRun = run
				}
			}
		}
	}
	if checked == 0 {
		return false
	}
	suspiciousPct := suspicious * 100 / checked
	printablePct := printable * 100 / checked
	switch {
	case maxRun > 120:
		return suspiciousPct > 50
	case printablePct > 70:
		return suspiciousPct > 45
	default:
		return suspiciousPct > 30
	}
}

// SummarizeBinaryOutput 二进制输出摘要：前 1 万字符可读占比 >60% 且最
// 长游程 >50 时解码展示（>6000 字符保首尾各 3000，中间省略标注）；否则
// 魔数识别 + 前 32 字节十六进制预览 + 换用工具的提示。
func SummarizeBinaryOutput(b []byte) string {
	runes := []rune(string(b)) // UTF-8 宽容解码（非法字节替换符）
	total := len(runes)
	scan := runes
	if len(scan) > 10_000 {
		scan = scan[:10_000]
	}
	var printable, maxRun, run int
	for _, r := range scan {
		if r >= 0x20 && r != 0x7F {
			printable++
			run++
			if run > maxRun {
				maxRun = run
			}
		} else {
			run = 0
		}
	}
	if len(scan) > 0 && printable*100/len(scan) > 60 && maxRun > 50 {
		var body string
		if total > 6000 {
			body = string(runes[:3000]) +
				fmt.Sprintf("\n... [中间省略 %d 字符] ...\n", total-6000) +
				string(runes[total-3000:])
		} else {
			body = string(runes)
		}
		return "[注意：输出中检测到二进制特征，但大部分内容可读，已尝试解码显示]\n" + body +
			fmt.Sprintf("\n---\n(原始大小: %d 字节)", len(b))
	}

	ft := DetectFileType(b)
	if ft == "" {
		ft = "未知"
	}
	preview := b
	if len(preview) > 32 {
		preview = preview[:32]
	}
	hexParts := make([]string, len(preview))
	for i, c := range preview {
		hexParts[i] = fmt.Sprintf("%02X", c)
	}
	return fmt.Sprintf("[检测到二进制输出，已省略乱码正文]\n约 %d 字节，识别类型: %s，前 32 字节: %s\n"+
		"提示：该命令的输出疑似二进制内容（%s）。若需查看详细内容，请改用对应的工具"+
		"（如 jar tf、unzip -l、javap、file、xxd 等）或先做合适的解码（或解压）再输出。",
		len(b), ft, strings.Join(hexParts, " "), ft)
}

// DetectFileType 魔数识别（Java detectFileType 全表）。
func DetectFileType(b []byte) string {
	matches := func(magic ...byte) bool {
		if len(b) < len(magic) {
			return false
		}
		for i, m := range magic {
			if b[i] != m {
				return false
			}
		}
		return true
	}
	switch {
	case matches(0xCA, 0xFE, 0xBA, 0xBE):
		return "Java Class"
	case matches(0x50, 0x4B, 0x03, 0x04):
		return "ZIP/JAR/WAR"
	case matches(0x50, 0x4B, 0x05, 0x06):
		return "ZIP (空)"
	case matches(0x50, 0x4B, 0x07, 0x08):
		return "ZIP (分卷)"
	case matches(0x50, 0x4B):
		return "ZIP"
	case matches(0x7F, 0x45, 0x4C, 0x46):
		return "ELF"
	case matches(0x89, 0x50, 0x4E, 0x47):
		return "PNG"
	case matches(0xFF, 0xD8):
		return "JPEG"
	case matches(0x25, 0x50, 0x44, 0x46):
		return "PDF"
	case matches(0xD0, 0xCF):
		return "DOC/XLS (OLE2)"
	case matches(0x1F, 0x8B):
		return "GZIP"
	case matches(0x42, 0x5A, 0x68):
		return "BZIP2"
	case matches(0xFD, 0x37, 0x7A, 0x58):
		return "XZ"
	case matches(0x52, 0x61, 0x72, 0x21):
		return "RAR"
	case matches(0x00, 0x61, 0x73, 0x6D):
		return "WASM"
	}
	return ""
}

// TruncateForLLM 头尾截断（Java truncateForLlm）：保前半 + 后半，中间以
// 中文标注省略量。按 rune 计数（Java 为 UTF-16 char 计数——Go 增量偏离，
// 对 BMP 外字符计数略小）。
func TruncateForLLM(text string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = DefaultLLMOutputChars
	}
	rs := []rune(text)
	if len(rs) <= maxChars {
		return text
	}
	head := rs[:maxChars/2]
	tail := rs[len(rs)-(maxChars-len(head)):]
	omitted := len(rs) - len(head) - len(tail)
	return string(head) + fmt.Sprintf(
		"\n... [输出过大已截断：总计 %d 字符，省略中间 %d 字符，仅保留首尾。"+
			"如需完整内容，请用 read 工具分页读取，或将输出重定向到文件后再处理] ...\n",
		len(rs), omitted) + string(tail)
}

// compactEnv 构造子进程 env（os.Environ + 追加项）。测试与 talent 共用。
func compactEnv(extra map[string]string) []string {
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}
