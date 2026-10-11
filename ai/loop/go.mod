module github.com/crazy-airhead/aifei-go/ai/loop

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v1.0.3
	github.com/crazy-airhead/aifei-go/ai/agent v1.0.3
	github.com/crazy-airhead/aifei-go/flow v1.0.3
	github.com/crazy-airhead/aifei-go/log v1.0.3
)

require (
	github.com/crazy-airhead/aifei-go/dami v1.0.3 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v1.0.3 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../ai
	github.com/crazy-airhead/aifei-go/ai/agent => ../../ai/agent
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/flow => ../../flow
	github.com/crazy-airhead/aifei-go/log => ../../log
)
