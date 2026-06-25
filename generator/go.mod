module github.com/crazy-airhead/aifei-go/generator

go 1.26

require (
	github.com/crazy-airhead/aifei-go/db v0.0.5
	github.com/crazy-airhead/aifei-go/enjoy v0.0.5
)

replace (
	github.com/crazy-airhead/aifei-go/db => ../db
	github.com/crazy-airhead/aifei-go/enjoy=> ../enjoy
)
