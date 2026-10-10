module github.com/crazy-airhead/aifei-go/_test/ai_test

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v1.0.3
	github.com/crazy-airhead/aifei-go/json v1.0.3
)

require github.com/crazy-airhead/aifei-go/enjoy v1.0.3 // indirect

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../ai
	github.com/crazy-airhead/aifei-go/json => ../../json
)
