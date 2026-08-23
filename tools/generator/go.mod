module github.com/crazy-airhead/aifei-go/tools/generator

go 1.26

require (
	github.com/crazy-airhead/aifei-go/db v0.1.0
	github.com/crazy-airhead/aifei-go/enjoy v0.1.0
)

require github.com/crazy-airhead/aifei-go/log v0.1.0 // indirect

replace (
	github.com/crazy-airhead/aifei-go/db => ../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
)

replace github.com/crazy-airhead/aifei-go/log => ../../log
