// talent.go 子代理调度与动态创建（对标 harness.agent.TaskTalent/GenerateTalent）。
package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent"
)

// TaskTalent 子代理调度能力包：task（串行单发）/ multitask（并行多发的
// XML 聚合返回）。
type TaskTalent struct {
	ai.TalentBase
	engine       *Engine
	multitaskMax int
}

// NewTaskTalent 构造。
func NewTaskTalent(engine *Engine, multitaskMax int) *TaskTalent {
	if multitaskMax <= 0 {
		multitaskMax = 5
	}
	return &TaskTalent{engine: engine, multitaskMax: multitaskMax}
}

// Name 名称。
func (t *TaskTalent) Name() string { return "task" }

// Description 描述。
func (t *TaskTalent) Description() string { return "多任务调度专家" }

// Instruction 动态指令（可用子代理清单 + 分配策略——Java getInstruction 同款形态）。
func (t *TaskTalent) Instruction(p *ai.Prompt) string {
	var sb strings.Builder
	sb.WriteString("## 当前可用的子代理\n<available_agents>\n")
	for _, def := range t.engine.AgentManager().Agents() {
		sb.WriteString(fmt.Sprintf("  - \"%s\": %s\n", def.Name(), def.Metadata.Description))
	}
	sb.WriteString("</available_agents>\n")
	sb.WriteString(fmt.Sprintf(`## 任务分配策略：
1. 优先并行：互不干扰的独立单元必须用 multitask，单次建议 2-%d 个
2. 原子性：每个子任务边界明确
3. 上下文感知：子代理看不到主会话历史，prompt 必须自带全部背景
4. 分批执行：超过 %d 个分批调用（每批最多 %d 个），避免限流/结果过长`,
		t.multitaskMax, t.multitaskMax, t.multitaskMax))
	return sb.String()
}

// Tools 工具集。
func (t *TaskTalent) Tools(p *ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("task").
			Desc("调度一个子代理执行任务（串行）").
			StringParam("agent_name", "子代理名（可用清单见指令）", true).
			StringParam("prompt", "任务提示（自带全部背景）", true).
			StringParam("description", "任务简述", false).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				return t.taskDo(ctx, taskSpecOf(args, 1))
			}).Build(),
		ai.NewTool("multitask").
			Desc("并行调度多个子代理执行互不干扰的任务").
			StringParam("tasks", "JSON 数组：[{agent_name,prompt,description}...]", true).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				specs, err := parseTaskSpecs(args["tasks"])
				if err != nil {
					return nil, err
				}
				results := make([]string, len(specs))
				var wg sync.WaitGroup
				var mu sync.Mutex
				var firstErr error
				for i, spec := range specs {
					wg.Add(1)
					go func(i int, spec taskSpec) {
						defer wg.Done()
						out, err := t.taskDo(ctx, spec)
						if err != nil {
							mu.Lock()
							if firstErr == nil {
								firstErr = fmt.Errorf("task %d: %w", spec.Index, err)
							}
							mu.Unlock()
							return
						}
						mu.Lock()
						results[i] = fmt.Sprint(out)
						mu.Unlock()
					}(i, spec)
				}
				wg.Wait()
				if firstErr != nil {
					return fmt.Sprintf("ERROR: Multitask aggregate failed: %v", firstErr), nil
				}
				return "<multitask_results>\n" + strings.Join(nonEmpty(results), "\n") + "\n</multitask_results>", nil
			}).Build(),
	}
}

type taskSpec struct {
	AgentName   string
	Prompt      string
	Description string
	Index       int
}

func taskSpecOf(args map[string]any, index int) taskSpec {
	spec := taskSpec{Index: index}
	spec.AgentName, _ = args["agent_name"].(string)
	spec.Prompt, _ = args["prompt"].(string)
	spec.Description, _ = args["description"].(string)
	return spec
}

func parseTaskSpecs(v any) ([]taskSpec, error) {
	s, _ := v.(string)
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("tasks 参数为空")
	}
	var raw []struct {
		AgentName   string `json:"agent_name"`
		Prompt      string `json:"prompt"`
		Description string `json:"description"`
		Index       int    `json:"index"`
	}
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		return nil, fmt.Errorf("tasks 解析失败: %w", err)
	}
	out := make([]taskSpec, 0, len(raw))
	for i, r := range raw {
		spec := taskSpec{AgentName: r.AgentName, Prompt: r.Prompt, Description: r.Description, Index: r.Index}
		if spec.Index == 0 {
			spec.Index = i + 1
		}
		out = append(out, spec)
	}
	return out, nil
}

func nonEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// taskDo 单任务执行（对标 taskDo：子代理内存会话隔离 + 结果 XML 包裹）。
func (t *TaskTalent) taskDo(ctx context.Context, spec taskSpec) (string, error) {
	if spec.AgentName == "" {
		return "", fmt.Errorf("agent_name 为空")
	}
	sub, err := t.engine.GetAgentOrMain(spec.AgentName)
	if sub == nil || err != nil {
		return fmt.Sprintf("ERROR: 未知的子代理类型 '%s'。", spec.AgentName), nil
	}
	if sub.Name() == AgentMain {
		// 未知名回退主代理（Java 语义：未知名报错；Go GetAgentOrMain 回退——
		// 此处恢复报错面）
		if spec.AgentName != AgentMain {
			return fmt.Sprintf("ERROR: 未知的子代理类型 '%s'。", spec.AgentName), nil
		}
	}
	msg, err := sub.Call(ai.PromptOf(spec.Prompt), agent.NewMemorySession(sub.Name()))
	if err != nil {
		return t.wrapResult(spec, "failure", err.Error()), nil
	}
	text := ""
	if msg != nil {
		text = msg.Text
	}
	return t.wrapResult(spec, "success", text), nil
}

// wrapResult 结果包裹（Java <task_result> 形态）。
func (t *TaskTalent) wrapResult(spec taskSpec, status, content string) string {
	var sb strings.Builder
	sb.WriteString("<task_result>\n")
	if spec.Index > 1 || spec.Description != "" {
		if spec.Index > 0 && spec.Index != 1 {
			sb.WriteString(fmt.Sprintf("<index>%d</index>\n", spec.Index))
		}
	}
	if spec.Description != "" {
		sb.WriteString(fmt.Sprintf("<description>%s</description>\n", spec.Description))
	}
	if spec.AgentName != "" {
		sb.WriteString(fmt.Sprintf("<agent_name>%s</agent_name>\n", spec.AgentName))
	}
	sb.WriteString(fmt.Sprintf("<result_status>%s</result_status>\n", status))
	sb.WriteString("<result_content><![CDATA[" + content + "]]></result_content>\n")
	sb.WriteString("</task_result>")
	return sb.String()
}

// GenerateTalent 动态创建子代理能力包（对标 GenerateTalent：复制 general 定义
// 覆写，可选持久化 .soloncode/agents/{name}.md）。
type GenerateTalent struct {
	ai.TalentBase
	engine *Engine
}

// NewGenerateTalent 构造。
func NewGenerateTalent(engine *Engine) *GenerateTalent { return &GenerateTalent{engine: engine} }

// Name 名称。
func (t *GenerateTalent) Name() string { return "generate" }

// Description 描述。
func (t *GenerateTalent) Description() string { return "子代理生成器" }

// Instruction 指令。
func (t *GenerateTalent) Instruction(p *ai.Prompt) string {
	return `## 子代理创建准则
- 仅当现有代理列表无匹配时才创建新代理
- 创建前应保证 systemPrompt 专业性
- tools 严禁全选（可选值：read/write/apply-diff/glob/shell/webfetch/websearch/pi/task/*）
- 创建后用 task(agent_name=..., prompt=...) 调用`
}

var generateNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// Tools 工具集。
func (t *GenerateTalent) Tools(p *ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("generate").
			Desc("动态创建一个子代理").
			StringParam("name", "代理名（^[a-zA-Z0-9_-]+$）", true).
			StringParam("description", "描述", true).
			StringParam("systemPrompt", "系统提示", true).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				name, _ := args["name"].(string)
				desc, _ := args["description"].(string)
				sp, _ := args["systemPrompt"].(string)
				if !generateNamePattern.MatchString(name) {
					return "ERROR: 代理名只允许字母/数字/下划线/连字符。", nil
				}
				def := t.engine.AgentManager().Get("general")
				if def == nil {
					def = NewDefinitionBuilder("general").ToolsAdd("*").Build()
				}
				merged := def.Copy()
				merged.Metadata.Name = name
				merged.Metadata.Description = desc
				merged.Metadata.Enabled = true
				merged.SystemPrompt = sp
				if tools, _ := args["tools"].(string); tools != "" {
					merged.Metadata.Tools = strings.Split(tools, ",")
				}

				// 可选持久化
				if save, _ := args["save_to_file"].(bool); save {
					cwd, _ := ai.ToolContextOf(ctx)[AttrCWD].(string)
					if cwd == "" {
						cwd = "."
					}
					dir := filepath.Join(cwd, ".soloncode", "agents")
					if err := os.MkdirAll(dir, 0o755); err != nil {
						return fmt.Sprintf("ERROR: 创建目录失败: %v", err), nil
					}
					if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(merged.ToMarkdown()), 0o644); err != nil {
						return fmt.Sprintf("ERROR: 写入失败: %v", err), nil
					}
				}
				t.engine.AgentManager().AddAgentIfAbsent(merged)
				return fmt.Sprintf("[OK] 子代理创建成功！标识: %s / 描述: %s。请用 task(agent_name=%q, prompt=...) 调用。",
					name, desc, name), nil
			}).Build(),
	}
}
