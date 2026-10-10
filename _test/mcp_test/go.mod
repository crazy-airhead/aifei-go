module github.com/crazy-airhead/aifei-go/_test/mcp_test

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v1.0.3
	github.com/crazy-airhead/aifei-go/aifei v1.0.3
	github.com/crazy-airhead/aifei-go/mcp v1.0.3
	github.com/crazy-airhead/aifei-go/server v1.0.3
)

require (
	github.com/crazy-airhead/aifei-go/db v1.0.3 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v1.0.3 // indirect
	github.com/crazy-airhead/aifei-go/http v1.0.3 // indirect
	github.com/crazy-airhead/aifei-go/log v1.0.3 // indirect
)

replace github.com/crazy-airhead/aifei-go/ai => ../../ai

replace github.com/crazy-airhead/aifei-go/mcp => ../../mcp

replace github.com/crazy-airhead/aifei-go/server => ../../server

replace github.com/crazy-airhead/aifei-go/aifei => ../../aifei

replace github.com/crazy-airhead/aifei-go/db => ../../db

replace github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy

replace github.com/crazy-airhead/aifei-go/http => ../../http

replace github.com/crazy-airhead/aifei-go/log => ../../log
