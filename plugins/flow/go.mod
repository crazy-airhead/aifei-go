module github.com/crazy-airhead/aifei-go/plugins/flow

go 1.26.1

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.44
	github.com/crazy-airhead/aifei-go/config v0.0.44
	github.com/crazy-airhead/aifei-go/db v0.0.44
	github.com/crazy-airhead/aifei-go/flow v0.0.44
	github.com/crazy-airhead/aifei-go/log v0.0.44
	gopkg.in/yaml.v3 v3.0.1
)

replace (
	github.com/crazy-airhead/aifei-go => ../../aifei
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../config
	github.com/crazy-airhead/aifei-go/db => ../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/flow => ../../flow
	github.com/crazy-airhead/aifei-go/log => ../../log
)
