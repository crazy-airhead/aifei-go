module github.com/crazy-airhead/aifei-go/ai/agent

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v0.0.0
	github.com/crazy-airhead/aifei-go/flow v0.0.0
)

require (
	github.com/crazy-airhead/aifei-go/dami v1.0.2 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v1.0.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../ai
	github.com/crazy-airhead/aifei-go/flow => ../../flow
)
