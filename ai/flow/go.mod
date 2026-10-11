module github.com/crazy-airhead/aifei-go/ai/flow

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v1.2.0
	github.com/crazy-airhead/aifei-go/dami v1.2.0
	github.com/crazy-airhead/aifei-go/enjoy v1.2.0
	github.com/crazy-airhead/aifei-go/flow v1.2.0
)

require gopkg.in/yaml.v3 v3.0.1 // indirect

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../ai
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/flow => ../../flow
	github.com/crazy-airhead/aifei-go/log => ../../log
	github.com/crazy-airhead/aifei-go/mcp => ../../mcp
)
