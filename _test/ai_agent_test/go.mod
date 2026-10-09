module github.com/crazy-airhead/aifei-go/_test/ai_agent_test

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v0.0.0
	github.com/crazy-airhead/aifei-go/ai/agent v0.0.0
	github.com/crazy-airhead/aifei-go/flow v0.0.0
)

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../ai
	github.com/crazy-airhead/aifei-go/ai/agent => ../../ai/agent
	github.com/crazy-airhead/aifei-go/flow => ../../flow
)
