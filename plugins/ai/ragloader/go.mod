module github.com/crazy-airhead/aifei-go/plugins/ai/ragloader

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v1.0.3
	github.com/crazy-airhead/aifei-go/db v1.0.3
	github.com/PuerkitoBio/goquery v1.10.0
	github.com/yuin/goldmark v1.7.8
)

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../../ai
	github.com/crazy-airhead/aifei-go/db => ../../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../../enjoy
	github.com/crazy-airhead/aifei-go/log => ../../../log
)
