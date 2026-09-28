module github.com/crazy-airhead/aifei-go/_test/flow_plugin_test

go 1.27

require (
	github.com/crazy-airhead/aifei-go/db v1.0.2
	github.com/crazy-airhead/aifei-go/flow v1.0.2
	github.com/crazy-airhead/aifei-go/plugins/flow v1.0.2
)

require (
	github.com/crazy-airhead/aifei-go/aifei v1.0.2 // indirect
	github.com/crazy-airhead/aifei-go/dami v1.0.2 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v1.0.2 // indirect
	github.com/crazy-airhead/aifei-go/log v1.0.2 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../config
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/db => ../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/flow => ../../flow
	github.com/crazy-airhead/aifei-go/log => ../../log
	github.com/crazy-airhead/aifei-go/plugins/flow => ../../plugins/flow
)
