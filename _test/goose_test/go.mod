module github.com/crazy-airhead/aifei-go/_test/goose_test

go 1.26.0

require (
	github.com/crazy-airhead/aifei-go/config v0.1.3
	github.com/crazy-airhead/aifei-go/db v0.1.3
	github.com/crazy-airhead/aifei-go/log v0.1.3
	github.com/crazy-airhead/aifei-go/plugins/goose v0.1.3
	modernc.org/sqlite v1.62.0
)

require (
	github.com/crazy-airhead/aifei-go/aifei v0.1.3 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v0.1.3 // indirect
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mfridman/interpolate v0.2.0 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/pressly/goose/v3 v3.30.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/sethvargo/go-retry v0.5.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.80.2 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
)

replace github.com/crazy-airhead/aifei-go/aifei => ../../aifei

replace github.com/crazy-airhead/aifei-go/config => ../../config

replace github.com/crazy-airhead/aifei-go/db => ../../db

replace github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy

replace github.com/crazy-airhead/aifei-go/http => ../../http

replace github.com/crazy-airhead/aifei-go/log => ../../log

replace github.com/crazy-airhead/aifei-go/plugins/goose => ../../plugins/goose
