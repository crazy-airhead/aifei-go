module github.com/crazy-airhead/aifei-go/plugins/dataisolate

go 1.27

require (
	github.com/ajitpratap0/GoSQLX v1.14.0
	github.com/crazy-airhead/aifei-go/aifei v1.0.0
	github.com/crazy-airhead/aifei-go/config v1.0.0
	github.com/crazy-airhead/aifei-go/db v1.0.0
	github.com/crazy-airhead/aifei-go/log v1.0.0
	github.com/crazy-airhead/aifei-go/server v1.0.0
)

require (
	github.com/crazy-airhead/aifei-go/enjoy v1.0.0 // indirect
	github.com/crazy-airhead/aifei-go/http v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../config
	github.com/crazy-airhead/aifei-go/db => ../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/http => ../../http
	github.com/crazy-airhead/aifei-go/log => ../../log
	github.com/crazy-airhead/aifei-go/server => ../../server
)
