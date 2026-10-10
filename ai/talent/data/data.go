// Package data 持久 KV 能力包（对标 RedisTalent：redis_set/redis_get 双工具 +
// 30 天 TTL；Go 侧 KV 面抽象——cache 插件/go-redis/内存皆可注入）。
package data

import (
	"context"
	"time"

	ai "github.com/crazy-airhead/aifei-go/ai"
)

// KVStore KV 存储面。
type KVStore interface {
	Set(ctx context.Context, key string, value string, ttl time.Duration) error
	Get(ctx context.Context, key string) (string, bool, error)
}

// Data 能力包。
type Data struct {
	ai.TalentBase
	store     KVStore
	keyPrefix string
}

// New 构造。
func New(store KVStore, keyPrefix string) *Data { return &Data{store: store, keyPrefix: keyPrefix} }

// Name 名称。
func (t *Data) Name() string { return "redis_storage" }

// Description 描述。
func (t *Data) Description() string {
	return "长期记忆专家：可以将重要信息持久化存储，以便在之后的对话中找回。"
}

// finalKey 拼最终键（ai:memory:{prefix}{key}——Java 同款）。
func (t *Data) finalKey(key string) string {
	prefix := ""
	if t.keyPrefix != "" {
		prefix = t.keyPrefix + ":"
	}
	return "ai:memory:" + prefix + key
}

// Tools 双工具。
func (t *Data) Tools(*ai.Prompt) []ai.FunctionTool {
	return []ai.FunctionTool{
		ai.NewTool("redis_set").
			Desc("在持久化仓库中存入重要信息。key 应该是简短的标识符。").
			StringParam("key", "简短标识符", true).
			StringParam("value", "内容", true).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				key, _ := args["key"].(string)
				value, _ := args["value"].(string)
				if err := t.store.Set(ctx, t.finalKey(key), value, 30*24*time.Hour); err != nil {
					return "存储失败，我的记忆模块出了一点小状况。", nil
				}
				return "我已经把这件事记在我的笔记里了（Key: " + key + "）。", nil
			}).
			Build(),
		ai.NewTool("redis_get").
			Desc("从持久化仓库找回之前存入的信息。").
			StringParam("key", "标识符", true).
			Handler(func(ctx context.Context, args map[string]any) (any, error) {
				key, _ := args["key"].(string)
				value, ok, err := t.store.Get(ctx, t.finalKey(key))
				if err != nil {
					return "读取失败，我暂时想不起来了。", nil
				}
				if !ok {
					return "我在笔记里没找到关于 [" + key + "] 的内容。", nil
				}
				return "关于 [" + key + "]，我记得是：" + value, nil
			}).
			Build(),
	}
}
