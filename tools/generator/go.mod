module github.com/crazy-airhead/aifei-go/tools/generator

go 1.26

require (
	github.com/crazy-airhead/aifei-go/db v0.0.41
	github.com/crazy-airhead/aifei-go/enjoy v0.0.41
)

replace (
	github.com/crazy-airhead/aifei-go/db => ../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
)
