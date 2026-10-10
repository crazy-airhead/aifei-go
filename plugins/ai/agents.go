// agents.go agent 段装配：AgentConfig + chat 模型引用 → simple.Agent。
package ai

import (
	"fmt"

	aicore "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/ai/agent/simple"
)

// buildAgent 装配单个 simple 智能体。model 键引用 chat 注册表（缺省
// ai.default）；未写的新参数保持 simple.NewConfig 缺省。
func buildAgent(name string, ac AgentConfig, models map[string]*aicore.ChatModel, defModel string) (*simple.Agent, error) {
	ref := ac.Model
	if ref == "" {
		ref = defModel
	}
	cm, ok := models[ref]
	if !ok {
		return nil, fmt.Errorf("ai: agent %q references unknown model %q", name, ref)
	}
	cfg := simple.NewConfig()
	cfg.Name = name
	cfg.Role = ac.Role
	cfg.Instruction = ac.Instruction
	cfg.ChatModel = cm
	cfg.OutputKey = ac.OutputKey
	cfg.OutputSchema = ac.OutputSchema
	if ac.MaxRetries > 0 || ac.RetryDelayMs > 0 {
		cfg.SetRetryConfig(ac.MaxRetries, ac.RetryDelayMs) // 内部钳制 >=1 / >=500ms
	}
	if ac.SessionWindowSize > 0 {
		cfg.SessionWindowSize = ac.SessionWindowSize
	}
	a, err := simple.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("ai: agent %q: %w", name, err)
	}
	return a, nil
}
