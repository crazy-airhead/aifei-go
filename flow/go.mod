module github.com/crazy-airhead/aifei-go/flow

go 1.26.1

require (
	github.com/crazy-airhead/aifei-go/dami v0.1.4
	github.com/crazy-airhead/aifei-go/enjoy v0.1.4
	gopkg.in/yaml.v3 v3.0.1
)

replace (
	github.com/crazy-airhead/aifei-go/dami => ../dami
	github.com/crazy-airhead/aifei-go/enjoy => ../enjoy
)
