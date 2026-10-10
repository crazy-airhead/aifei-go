module github.com/crazy-airhead/aifei-go/_test/mcp_test

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v0.0.0
	github.com/crazy-airhead/aifei-go/mcp v0.0.0
)

require github.com/crazy-airhead/aifei-go/log v1.0.2 // indirect

replace github.com/crazy-airhead/aifei-go/ai => ../../ai

replace github.com/crazy-airhead/aifei-go/mcp => ../../mcp
