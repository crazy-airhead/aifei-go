// sys.go 系统能力包四 talent（对标 solon-ai-talent-sys）。Shell 按 OS 探
// 测终端（Windows COMSPEC → PowerShell/CMD；Unix bash 探测失败回落
// /bin/sh）并注入 Terminal 环境状态指令（含宿主进程 PID 与 kill 禁令）；
// Python/Nodejs 探测解释器后暴露落盘执行工具；Clock 提供中文星期格式的
// 当前时间查询。工作目录延迟到执行时创建（Java 构造期 ensureDir 失败抛
// IllegalStateException——Go 侧错误面收口进执行结果串，偏离注明）。
package sys

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// processBase 进程型 talent 公共基座（Java AbsProcessTalent 对应物）。
type processBase struct {
	ai.TalentBase
	work string
	exec *Executor
}

func newProcessBase(workDir string) processBase {
	return processBase{work: absWork(workDir), exec: NewExecutor()}
}

// SetMaxOutputSize 覆写输出字节上限。
func (p *processBase) SetMaxOutputSize(n int) { p.exec.SetMaxOutputSize(n) }

// ensureWork 执行前确保工作目录存在（幂等）。
func (p *processBase) ensureWork() { _ = os.MkdirAll(p.work, 0o755) }

// absWork 绝对化工作目录。
func absWork(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return abs
}

// timeoutParam 缺省 120 秒的超时参数（三执行工具共用形状）。
func timeoutParam() ai.ParamSpec {
	return ai.ParamSpec{Name: "timeout", Type: "integer", Required: false, Default: DefaultTimeoutMS,
		Desc: "可选超时时间，单位为毫秒"}
}

// ---- Shell ----

// Shell 系统命令执行能力包（execute_shell）。
type Shell struct {
	processBase
	shellCmd string // 可含参数（空白切分后追加脚本路径）
	ext      string
	mode     string // 指令块展示的终端类型名
}

// NewShell 探测本机终端构造（Windows: COMSPEC；Unix: bash → /bin/sh）。
func NewShell(workDir string) *Shell {
	s := &Shell{processBase: newProcessBase(workDir)}
	if runtime.GOOS == "windows" {
		comspec := strings.ToLower(os.Getenv("COMSPEC"))
		if strings.Contains(comspec, "powershell") {
			s.shellCmd, s.ext, s.mode = "powershell -Command", ".ps1", "PowerShell"
		} else {
			s.shellCmd, s.ext, s.mode = "cmd /c", ".bat", "CMD"
		}
	} else if s.exec.IsCommandAvailable(context.Background(), "bash") {
		s.shellCmd, s.ext, s.mode = "bash", ".sh", "bash"
	} else {
		s.shellCmd, s.ext, s.mode = "/bin/sh", ".sh", "sh"
	}
	return s
}

func (s *Shell) Name() string { return "shell" }

func (s *Shell) Description() string { return "Shell 专家：执行系统命令" }

// Instruction Terminal 环境状态（Java 模板逐字保留；宿主进程 Java→Go）。
func (s *Shell) Instruction(*ai.Prompt) string {
	pid := os.Getpid()
	return fmt.Sprintf(`## Terminal 环境状态
- **当前时间**: %s
- **运行环境**: %s (%s)
- **终端类型**: %s
- **进程安全约束**:
  - **当前宿主进程**: Go (PID: %d)
  - **执行禁令**: 严禁执行任何可能导致宿主进程退出的命令（如 kill -9 %d、pkill java、killall java 等）。在清理进程前，必须先通过 ps 或 jps 确认目标 PID。`,
		time.Now().Format("2006-01-02 15:04:05 MST"), runtime.GOOS, runtime.GOARCH, s.mode, pid, pid)
}

func (s *Shell) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("execute_shell").
			Desc("在本地系统中执行单行指令或多行脚本，并获取标准输出。").
			StringParam("code", "Shell 脚本代码", true).
			Param(timeoutParam()).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				s.ensureWork()
				return s.exec.ExecuteCode(ctx, s.work, argCode(args), s.shellCmd, s.ext, nil, argTimeout(args)), nil
			}).Build(),
	}
}

// ---- Python ----

// Python Python 代码执行能力包（execute_python）。
type Python struct {
	processBase
	cmd string // 探测到的解释器（python3/python；空 = 本机不可用）
}

// NewPython 探测解释器构造（python3 优先）。
func NewPython(workDir string) *Python {
	p := &Python{processBase: newProcessBase(workDir)}
	p.cmd = p.exec.ProbePythonCommand(context.Background())
	return p
}

func (p *Python) Name() string { return "python" }

func (p *Python) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("execute_python").
			Desc("执行 Python 代码并获取输出。支持数学计算、数据分析等。").
			StringParam("code", "Python 代码", true).
			Param(timeoutParam()).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				p.ensureWork()
				envs := map[string]string{"PYTHONIOENCODING": "UTF-8"}
				return p.exec.ExecuteCode(ctx, p.work, argCode(args), p.cmd, ".py", envs, argTimeout(args)), nil
			}).Build(),
	}
}

// ---- Nodejs ----

// Nodejs Node.js 代码执行能力包（execute_js）。
type Nodejs struct {
	processBase
	cmd string // 探测到的解释器（node/nodejs；空 = 本机不可用）
}

// NewNodejs 探测解释器构造。
func NewNodejs(workDir string) *Nodejs {
	n := &Nodejs{processBase: newProcessBase(workDir)}
	n.cmd = n.exec.ProbeNodeCommand(context.Background())
	return n
}

func (n *Nodejs) Name() string { return "nodejs" }

func (n *Nodejs) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("execute_js").
			Desc("执行 Node.js 代码。").
			StringParam("code", "JavaScript 代码", true).
			Param(timeoutParam()).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				n.ensureWork()
				envs := map[string]string{"NODE_SKIP_PLATFORM_CHECK": "1"}
				return n.exec.ExecuteCode(ctx, n.work, argCode(args), n.cmd, ".js", envs, argTimeout(args)), nil
			}).Build(),
	}
}

// ---- Clock ----

// Clock 系统时钟能力包（get_current_time，中文星期）。
type Clock struct {
	ai.TalentBase
}

// NewClock 构造时钟能力包。
func NewClock() *Clock { return &Clock{} }

func (c *Clock) Name() string { return "clock" }

func (c *Clock) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("get_current_time").
			Desc("获取系统当前的日期、精确时间、星期及本地时区（例如：2026-01-30 21:00:00 星期五 CST）").
			Handler(func(context.Context, map[string]any) (any, error) {
				return FormatClock(time.Now()), nil
			}).Build(),
	}
}

// weekChinese 中文星期（Java Locale.CHINESE EEEEE 对应物）。
var weekChinese = []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}

// FormatClock yyyy-MM-dd HH:mm:ss EEEE z（中文星期 + 短时区）。
func FormatClock(t time.Time) string {
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d %s %s",
		t.Year(), int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(),
		weekChinese[int(t.Weekday())], t.Format("MST"))
}

// ---- 参数取值 ----

// argCode 取代码参数（缺省空串）。
func argCode(args map[string]any) string {
	if s, ok := args["code"].(string); ok {
		return s
	}
	return ""
}

// argTimeout 取超时参数（缺省 0 → Executor 内取 DefaultTimeoutMS）。
func argTimeout(args map[string]any) int {
	if v, ok := args["timeout"].(float64); ok { // JSON 数值归一 float64
		return int(v)
	}
	if v, ok := args["timeout"].(int); ok {
		return v
	}
	return 0
}
