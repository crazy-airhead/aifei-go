module github.com/crazy-airhead/aifei-go/_test/ai_test

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v0.0.0
	github.com/crazy-airhead/aifei-go/json v1.0.2
)

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../ai
	github.com/crazy-airhead/aifei-go/json => ../../json
)
