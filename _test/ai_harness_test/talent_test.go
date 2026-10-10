// talent_test.go 子代理调度与动态创建测试：task 串行（未知代理报错面）、
// multitask 并行聚合、generate 创建/校验/持久化。
package ai_harness_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/harness"
)

func engineWithTools(t *testing.T, responses ...string) (*harness.Engine, *fakeOpenAI) {
	t.Helper()
	f := newFakeOpenAI(t, responses...)
	e, err := harness.NewEngine(harness.WithModel(f.model(t)), harness.WithTools(testTools()...))
	if err != nil {
		t.Fatal(err)
	}
	return e, f
}

func TestHarnessTaskTalentSerial(t *testing.T) {
	// 子代理走假模型：general 定义（* 工具）
	e, f := engineWithTools(t)
	talent := e.TaskTalent()

	tools := talent.Tools(nil)
	if len(tools) != 2 || tools[0].Name() != "task" || tools[1].Name() != "multitask" {
		t.Fatalf("工具集：%v", tools)
	}
	// 指令含可用子代理清单与分配策略
	ins := talent.Instruction(nil)
	if !strings.Contains(ins, "<available_agents>") || !strings.Contains(ins, "general") ||
		!strings.Contains(ins, "multitask") {
		t.Fatalf("指令形态：%s", ins)
	}
	if strings.Contains(ins, "git-summary") {
		t.Fatal("hidden 代理不应出现在清单")
	}

	// 串行执行 general 子代理（独立内存会话）
	s := callTool(t, talent.Tools(nil)[0], map[string]any{
		"agent_name": "general", "prompt": "做点事", "description": "测试任务",
	})
	if !strings.Contains(s, "<task_result>") || !strings.Contains(s, "<result_status>success</result_status>") ||
		!strings.Contains(s, "<![CDATA[完成]]>") || !strings.Contains(s, "<agent_name>general</agent_name>") {
		t.Fatalf("结果包裹形态：%s", s)
	}
	if f.count() < 1 {
		t.Fatal("子代理应产生模型请求")
	}
	// 子代理请求携带 general 的系统提示正文
	req := f.request(t, 0)
	msgs, _ := req["messages"].([]any)
	sys, _ := msgs[0].(map[string]any)
	content, _ := sys["content"].(string)
	if !strings.Contains(content, "全能执行专家") {
		t.Fatalf("general 系统提示：%q", content)
	}

	// 未知子代理：ERROR 文案（不 panic）
	if out := callTool(t, talent.Tools(nil)[0], map[string]any{
		"agent_name": "nope", "prompt": "x",
	}); !strings.Contains(out, "ERROR: 未知的子代理类型 'nope'") {
		t.Fatalf("未知代理错误面：%s", out)
	}
}

func TestHarnessTaskTalentMultitask(t *testing.T) {
	e, _ := engineWithTools(t)
	talent := e.TaskTalent()

	tasks, _ := json.Marshal([]map[string]any{
		{"agent_name": "general", "prompt": "a", "description": "任务一"},
		{"agent_name": "general", "prompt": "b", "description": "任务二"},
	})
	s := callTool(t, talent.Tools(nil)[1], map[string]any{"tasks": string(tasks)})
	if !strings.Contains(s, "<multitask_results>") || strings.Count(s, "<task_result>") != 2 {
		t.Fatalf("并行聚合形态：%s", s)
	}
	if !strings.Contains(s, "<description>任务一</description>") {
		t.Fatalf("描述透传：%s", s)
	}

	// 空 tasks 报错（工具调用错误结果）
	if r := talent.Tools(nil)[1].Call(context.Background(), map[string]any{"tasks": ""}); !r.IsError {
		t.Fatal("空 tasks 应报错")
	}
}

func TestHarnessGenerateTalent(t *testing.T) {
	e, _ := engineWithTools(t)
	talent := e.GenerateTalent()

	// 非法名
	if out := callTool(t, talent.Tools(nil)[0], map[string]any{
		"name": "非法 名", "description": "d", "systemPrompt": "sp",
	}); !strings.Contains(out, "ERROR") {
		t.Fatalf("非法名错误面：%s", out)
	}

	// 创建（复制 general 覆写）
	if out := callTool(t, talent.Tools(nil)[0], map[string]any{
		"name": "reviewer", "description": "代码评审", "systemPrompt": "你是评审员",
	}); !strings.Contains(out, "[OK] 子代理创建成功") {
		t.Fatalf("创建文案：%s", out)
	}
	def := e.AgentManager().Get("reviewer")
	if def == nil || def.SystemPrompt != "你是评审员" || def.Metadata.Description != "代码评审" {
		t.Fatalf("注册的定义：%+v", def)
	}
	// general 的 * 工具集被继承
	if len(def.Metadata.Tools) == 0 || def.Metadata.Tools[0] != "*" {
		t.Fatalf("工具继承：%v", def.Metadata.Tools)
	}

	// save_to_file 持久化（__cwd 经工具上下文注入）
	dir := t.TempDir()
	callToolCtx(t, talent.Tools(nil)[0],
		ai.ContextWithToolContext(context.Background(), map[string]any{"__cwd": dir}),
		map[string]any{"name": "persisted", "description": "d2", "systemPrompt": "SP2", "save_to_file": true})
	data, err := os.ReadFile(filepath.Join(dir, ".soloncode", "agents", "persisted.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "name: persisted") || !strings.Contains(string(data), "SP2") {
		t.Fatalf("持久化形态：%s", data)
	}
}


// callTool 工具调用取文本（错误结果转 panic 语义由测试方断言 IsError）。
func callTool(t *testing.T, tool ai.FunctionTool, args map[string]any) string {
	t.Helper()
	return callToolCtx(t, tool, context.Background(), args)
}

func callToolCtx(t *testing.T, tool ai.FunctionTool, ctx context.Context, args map[string]any) string {
	t.Helper()
	r := tool.Call(ctx, args)
	if r == nil {
		t.Fatal("工具结果为空")
	}
	return r.ContentText()
}

// count 请求数（fakeOpenAI 计数）。
func (f *fakeOpenAI) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}
