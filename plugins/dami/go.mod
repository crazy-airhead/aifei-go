module github.com/crazy-airhead/aifei-go/plugins/dami

go 1.27

require (
	github.com/crazy-airhead/aifei-go/aifei v1.0.0
	github.com/crazy-airhead/aifei-go/dami v1.0.0
	github.com/crazy-airhead/aifei-go/log v1.0.0
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/log => ../../log
)
