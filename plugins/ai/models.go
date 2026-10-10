// models.go chat 与 embedding 模型装配：ModelConfig → aicore.Config →
// NewChatModel / embedding.New。方言经 dialects.go 全量注册，按
// provider/standard 命中（SelectDialect 优先级 standard > provider）。
package ai

import (
	"fmt"
	"time"

	aicore "github.com/crazy-airhead/aifei-go/ai"
	aiemb "github.com/crazy-airhead/aifei-go/ai/embedding"
)

// buildChatModel 装配单个 chat 模型。
func buildChatModel(name string, mc ModelConfig) (*aicore.ChatModel, error) {
	cfg, err := buildCoreConfig(name, mc)
	if err != nil {
		return nil, err
	}
	cfg.DefaultAutoToolCall = mc.DefaultAutoToolCall
	m, err := aicore.NewChatModel(*cfg)
	if err != nil {
		return nil, fmt.Errorf("ai: model %q: %w", name, err)
	}
	return m, nil
}

// buildEmbedding 装配单个 embedding 模型（共用 ModelConfig 形状；嵌入方言
// 在 embedding 包内静态注册，无需 blank import）。
func buildEmbedding(name string, mc ModelConfig) (*aiemb.Model, error) {
	cfg, err := buildCoreConfig(name, mc)
	if err != nil {
		return nil, err
	}
	m, err := aiemb.New(*cfg)
	if err != nil {
		return nil, fmt.Errorf("ai: embedding %q: %w", name, err)
	}
	return m, nil
}

// buildCoreConfig ModelConfig 公共部分 → aicore.Config（timeout 秒 →
// Duration；doer 名查注册表，未注册 fail-fast）。
func buildCoreConfig(name string, mc ModelConfig) (*aicore.Config, error) {
	cfg := &aicore.Config{
		Name:          name,
		Description:   mc.Description,
		APIKey:        mc.APIKey,
		APIURL:        mc.APIURL,
		Standard:      mc.Standard,
		Provider:      mc.Provider,
		Model:         mc.Model,
		ContextLength: mc.ContextLength,
		Headers:       mc.Headers,
		UserAgent:     mc.UserAgent,
		Proxy:         mc.Proxy,
	}
	if mc.TimeoutSeconds > 0 {
		cfg.Timeout = time.Duration(mc.TimeoutSeconds) * time.Second
	}
	if len(mc.Headers) > 0 {
		// config 层键归一（lowerCamelCase）在 HTTP 边界逆变换（mcp.headerKey 同款）
		headers := make(map[string]string, len(mc.Headers))
		for k, v := range mc.Headers {
			headers[headerKey(k)] = v
		}
		cfg.Headers = headers
	}
	if mc.Doer != "" {
		d := doerOf(mc.Doer)
		if d == nil {
			return nil, fmt.Errorf("ai: model %q references unknown doer %q (RegisterDoer first)", name, mc.Doer)
		}
		cfg.Doer = d
	}
	return cfg, nil
}
