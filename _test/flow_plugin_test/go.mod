module github.com/crazy-airhead/aifei-go/_test/flow_plugin_test

go 1.26.1

require (
	github.com/crazy-airhead/aifei-go/db v0.0.48
	github.com/crazy-airhead/aifei-go/flow v0.0.48
	github.com/crazy-airhead/aifei-go/plugins/flow v0.0.48
)

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.48 // indirect
	github.com/crazy-airhead/aifei-go/dami v0.0.48 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v0.0.48 // indirect
	github.com/crazy-airhead/aifei-go/log v0.0.48 // indirect
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
