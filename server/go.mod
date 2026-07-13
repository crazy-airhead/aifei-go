module github.com/crazy-airhead/aifei-go/server

go 1.26

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.20
	github.com/crazy-airhead/aifei-go/db v0.0.5
	github.com/crazy-airhead/aifei-go/enjoy v0.0.11
	github.com/crazy-airhead/aifei-go/http v0.0.20
	github.com/crazy-airhead/aifei-go/log v0.0.11
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../aifei
	github.com/crazy-airhead/aifei-go/enjoy => ../enjoy
	github.com/crazy-airhead/aifei-go/http => ../http
	github.com/crazy-airhead/aifei-go/log => ../log
)
