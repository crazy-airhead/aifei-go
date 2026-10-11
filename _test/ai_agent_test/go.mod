module github.com/crazy-airhead/aifei-go/_test/ai_agent_test

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v1.2.0
	github.com/crazy-airhead/aifei-go/ai/agent v1.2.0
	github.com/crazy-airhead/aifei-go/flow v1.2.0
)

require (
	github.com/crazy-airhead/aifei-go/dami v1.2.0 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v1.2.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../ai
	github.com/crazy-airhead/aifei-go/ai/agent => ../../ai/agent
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/flow => ../../flow
	github.com/crazy-airhead/aifei-go/json => ../../json
	github.com/crazy-airhead/aifei-go/log => ../../log
	github.com/crazy-airhead/aifei-go/mcp => ../../mcp
)
