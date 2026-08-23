module github.com/crazy-airhead/aifei-go/plugins/dami

go 1.26

require (
	github.com/crazy-airhead/aifei-go/aifei v0.1.0
	github.com/crazy-airhead/aifei-go/dami v0.1.0
	github.com/crazy-airhead/aifei-go/log v0.1.0
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/log => ../../log
)
