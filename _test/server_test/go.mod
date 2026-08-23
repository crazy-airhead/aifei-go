module github.com/crazy-airhead/aifei-go/_test/server_test

go 1.26

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.48
	github.com/crazy-airhead/aifei-go/server v0.0.48
)

require (
	github.com/crazy-airhead/aifei-go/db v0.0.48 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v0.0.48 // indirect
	github.com/crazy-airhead/aifei-go/http v0.0.48 // indirect
	github.com/crazy-airhead/aifei-go/log v0.0.48 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/db => ../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/http => ../../http
	github.com/crazy-airhead/aifei-go/log => ../../log
	github.com/crazy-airhead/aifei-go/server => ../../server
)
