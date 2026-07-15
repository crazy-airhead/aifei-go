module github.com/crazy-airhead/aifei-go/plugins/xxljob

go 1.26

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.32
	github.com/crazy-airhead/aifei-go/config v0.0.32
	github.com/crazy-airhead/aifei-go/log v0.0.32
	github.com/go-basic/ipv4 v1.0.0
)

require gopkg.in/yaml.v3 v3.0.1 // indirect

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../config
	github.com/crazy-airhead/aifei-go/log => ../../log
)
