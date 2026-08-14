module github.com/crazy-airhead/aifei-go/_test/flow_plugin_test

go 1.26

require (
	github.com/crazy-airhead/aifei-go/db v0.0.44
	github.com/crazy-airhead/aifei-go/flow v0.0.44
	github.com/crazy-airhead/aifei-go/plugins/flow v0.0.44
)

replace (
	github.com/crazy-airhead/aifei-go/db => ../../db
	github.com/crazy-airhead/aifei-go/flow => ../../flow
	github.com/crazy-airhead/aifei-go/plugins/flow => ../../plugins/flow
)
