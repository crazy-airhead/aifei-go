module github.com/crazy-airhead/aifei-go/server

go 1.26

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.8
	github.com/crazy-airhead/aifei-go/db v0.0.5
	github.com/crazy-airhead/aifei-go/go-http v0.0.8
)

require github.com/crazy-airhead/aifei-go/enjoy v0.0.5 // indirect

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../aifei
	github.com/crazy-airhead/aifei-go/go-http => ../go-http
)
