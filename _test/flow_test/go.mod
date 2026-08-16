module github.com/crazy-airhead/aifei-go/_test/flow_test

go 1.26.1

require (
	github.com/crazy-airhead/aifei-go/dami v0.0.46
	github.com/crazy-airhead/aifei-go/flow v0.0.46
)

require (
	github.com/crazy-airhead/aifei-go/enjoy v0.0.46 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/flow => ../../flow
)
