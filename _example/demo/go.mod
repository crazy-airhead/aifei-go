module github.com/crazy-airhead/aifei-go/_example/demo

go 1.26

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.14
	github.com/crazy-airhead/aifei-go/db v0.0.14
	github.com/crazy-airhead/aifei-go/server v0.0.0
	github.com/crazy-airhead/aifei-go/tools/generator v0.0.0
	modernc.org/sqlite v1.53.0
)

require (
	github.com/crazy-airhead/aifei-go/enjoy v0.0.14 // indirect
	github.com/crazy-airhead/aifei-go/http v0.0.14 // indirect
	github.com/crazy-airhead/aifei-go/log v0.0.11 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.45.0 // indirect
	modernc.org/libc v1.73.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/db => ../../db
	github.com/crazy-airhead/aifei-go/http => ../../http
	github.com/crazy-airhead/aifei-go/server => ../../server
	github.com/crazy-airhead/aifei-go/tools/generator => ../../tools/generator
)

replace github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
