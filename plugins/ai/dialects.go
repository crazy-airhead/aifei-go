// dialects.go 装配即全量注册。chat 方言注册是 init 制（SelectDialect 按
// standard/provider 匹配已注册方言表）——本插件是统一引入点，业务代码
// 无需逐包 blank import 即可 `provider: anthropic` 命中方言。嵌入方言在
// embedding 包内静态注册，无需引入。
package ai

import (
	_ "github.com/crazy-airhead/aifei-go/ai/dialect/anthropic"
	_ "github.com/crazy-airhead/aifei-go/ai/dialect/dashscope"
	_ "github.com/crazy-airhead/aifei-go/ai/dialect/gemini"
	_ "github.com/crazy-airhead/aifei-go/ai/dialect/ollama"
	_ "github.com/crazy-airhead/aifei-go/ai/dialect/openai"
)
