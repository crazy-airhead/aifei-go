module github.com/crazy-airhead/aifei-go/_test/flow_test

go 1.27

require (
	github.com/crazy-airhead/aifei-go/dami v1.0.0
	github.com/crazy-airhead/aifei-go/flow v1.0.0
)

require (
	github.com/crazy-airhead/aifei-go/enjoy v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/flow => ../../flow
)
