module github.com/crazy-airhead/aifei-go/server

go 1.26

require (
	github.com/crazy-airhead/aifei-go v0.0.0
	github.com/crazy-airhead/aifei-go/go-http v0.0.0
)

replace (
	github.com/crazy-airhead/aifei-go => ../aifei
	github.com/crazy-airhead/aifei-go/go-http => ../go-http
)
