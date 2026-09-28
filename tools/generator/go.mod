module github.com/crazy-airhead/aifei-go/tools/generator

go 1.27

require (
	github.com/crazy-airhead/aifei-go/db v1.0.2
	github.com/crazy-airhead/aifei-go/enjoy v1.0.2
)

require github.com/crazy-airhead/aifei-go/log v1.0.2 // indirect

replace (
	github.com/crazy-airhead/aifei-go/db => ../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
)

replace github.com/crazy-airhead/aifei-go/log => ../../log
