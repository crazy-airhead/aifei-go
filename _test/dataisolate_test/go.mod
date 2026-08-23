module github.com/crazy-airhead/aifei-go/_test/dataisolate_test

go 1.26.1

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.49
	github.com/crazy-airhead/aifei-go/config v0.0.49
	github.com/crazy-airhead/aifei-go/db v0.0.49
	github.com/crazy-airhead/aifei-go/http v0.0.49
	github.com/crazy-airhead/aifei-go/plugins/dataisolate v0.0.49
	github.com/crazy-airhead/aifei-go/server v0.0.49
	modernc.org/sqlite v1.54.0
)

require (
	github.com/ajitpratap0/GoSQLX v1.14.0 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v0.0.49 // indirect
	github.com/crazy-airhead/aifei-go/log v0.0.49 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.46.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.74.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../config
	github.com/crazy-airhead/aifei-go/db => ../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/http => ../../http
	github.com/crazy-airhead/aifei-go/log => ../../log
	github.com/crazy-airhead/aifei-go/plugins/dataisolate => ../../plugins/dataisolate
	github.com/crazy-airhead/aifei-go/server => ../../server
)
