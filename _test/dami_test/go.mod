module github.com/crazy-airhead/aifei-go/_test/dami_test

go 1.26

require (
	github.com/crazy-airhead/aifei-go/dami v0.0.44
	github.com/crazy-airhead/aifei-go/plugins/dami v0.0.44
)

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.44 // indirect
	github.com/crazy-airhead/aifei-go/log v0.0.44 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/log => ../../log
	github.com/crazy-airhead/aifei-go/plugins/dami => ../../plugins/dami
)
