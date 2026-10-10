module github.com/crazy-airhead/aifei-go/_test/ai_loop_test

go 1.27

require github.com/crazy-airhead/aifei-go/ai/loop v0.0.0

require github.com/crazy-airhead/aifei-go/log v1.0.3 // indirect

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../ai
	github.com/crazy-airhead/aifei-go/ai/loop => ../../ai/loop
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/flow => ../../flow
	github.com/crazy-airhead/aifei-go/log => ../../log
)
