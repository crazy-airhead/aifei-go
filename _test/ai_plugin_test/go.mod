module github.com/crazy-airhead/aifei-go/_test/ai_plugin_test

go 1.27

require (
	github.com/crazy-airhead/aifei-go/config v1.0.2
	github.com/crazy-airhead/aifei-go/mcp v0.0.0
	github.com/crazy-airhead/aifei-go/plugins/ai v0.0.0
)

require (
	github.com/crazy-airhead/aifei-go/ai v0.0.0 // indirect
	github.com/crazy-airhead/aifei-go/ai/agent v0.0.0 // indirect
	github.com/crazy-airhead/aifei-go/aifei v1.0.2 // indirect
	github.com/crazy-airhead/aifei-go/dami v1.0.2 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v1.0.2 // indirect
	github.com/crazy-airhead/aifei-go/flow v0.0.0 // indirect
	github.com/crazy-airhead/aifei-go/log v1.0.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../ai
	github.com/crazy-airhead/aifei-go/ai/agent => ../../ai/agent
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../config
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/flow => ../../flow
	github.com/crazy-airhead/aifei-go/log => ../../log
	github.com/crazy-airhead/aifei-go/mcp => ../../mcp
	github.com/crazy-airhead/aifei-go/plugins/ai => ../../plugins/ai
)
