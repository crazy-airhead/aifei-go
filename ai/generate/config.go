// Package generate 生成模型（对标 solon-ai GenerateModel，02-chat-core §11）：
// 图像/视频等生成服务的统一抽象——「prompt（字符串或键值形态）→ 生成内容列表」。
// 与 chat/embedding/rerank 同构（Config + 方言 + httpx）；usage 不进 Go 契约。
// GeneratePrompt 在 Go 侧收敛为 map[string]any（02 §11：轻量别名即可）。
package generate

import (
	ai "github.com/crazy-airhead/aifei-go/ai"
)

// Config 生成配置（对标 GenerateConfig extends AiConfig）：在 ai.Config 之上
// 增加 TaskURL——异步任务查询地址，约定以 "/" 结尾（拼接为裸字符串相加，
// 对标 getTaskUrlAndId 的纯 + 语义，不走 path.Join 以免剥掉末尾斜杠）。
type Config struct {
	ai.Config
	TaskURL string
}

// TaskURLAndId 任务查询地址拼接（taskURL + taskId）。
func (c *Config) TaskURLAndId(taskID string) string { return c.TaskURL + taskID }
