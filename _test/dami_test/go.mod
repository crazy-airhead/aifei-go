module github.com/crazy-airhead/aifei-go/_test/dami_test

go 1.27

require (
	github.com/crazy-airhead/aifei-go/dami v1.0.0
	github.com/crazy-airhead/aifei-go/plugins/dami v1.0.0
)

require (
	github.com/crazy-airhead/aifei-go/aifei v1.0.0 // indirect
	github.com/crazy-airhead/aifei-go/log v1.0.0 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/log => ../../log
	github.com/crazy-airhead/aifei-go/plugins/dami => ../../plugins/dami
)
