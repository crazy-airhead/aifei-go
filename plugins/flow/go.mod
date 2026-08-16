module github.com/crazy-airhead/aifei-go/plugins/flow

go 1.26.1

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.46
	github.com/crazy-airhead/aifei-go/db v0.0.46
	github.com/crazy-airhead/aifei-go/flow v0.0.46
	github.com/crazy-airhead/aifei-go/log v0.0.46
)

require (
	github.com/crazy-airhead/aifei-go/dami v0.0.46 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v0.0.46 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
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
)
