module github.com/crazy-airhead/aifei-go/server

go 1.27

require (
	github.com/crazy-airhead/aifei-go/aifei v1.0.0
	github.com/crazy-airhead/aifei-go/db v1.0.0
	github.com/crazy-airhead/aifei-go/enjoy v1.0.0
	github.com/crazy-airhead/aifei-go/http v1.0.0
	github.com/crazy-airhead/aifei-go/log v1.0.0
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../aifei
	github.com/crazy-airhead/aifei-go/db => ../db
	github.com/crazy-airhead/aifei-go/enjoy => ../enjoy
	github.com/crazy-airhead/aifei-go/http => ../http
	github.com/crazy-airhead/aifei-go/log => ../log
)
