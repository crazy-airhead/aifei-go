module github.com/crazy-airhead/aifei-go/flow

go 1.27

require (
	github.com/crazy-airhead/aifei-go/dami v1.0.0
	github.com/crazy-airhead/aifei-go/enjoy v1.0.0
	gopkg.in/yaml.v3 v3.0.1
)

replace (
	github.com/crazy-airhead/aifei-go/dami => ../dami
	github.com/crazy-airhead/aifei-go/enjoy => ../enjoy
)
