module github.com/crazy-airhead/aifei-go/server

go 1.26

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.5
	github.com/crazy-airhead/aifei-go/go-http v0.0.5
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../aifei
	github.com/crazy-airhead/aifei-go/go-http => ../go-http
)
