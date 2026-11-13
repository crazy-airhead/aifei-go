module github.com/crazy-airhead/aifei-go/plugins/goose

go 1.26.0

require (
	github.com/crazy-airhead/aifei-go/aifei v0.1.3
	github.com/crazy-airhead/aifei-go/config v0.1.3
	github.com/crazy-airhead/aifei-go/db v0.1.3
	github.com/crazy-airhead/aifei-go/log v0.1.3
	github.com/pressly/goose/v3 v3.30.1
)

require (
	github.com/mfridman/interpolate v0.2.0 // indirect
	github.com/sethvargo/go-retry v0.5.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
)

replace github.com/crazy-airhead/aifei-go/aifei => ../../aifei

replace github.com/crazy-airhead/aifei-go/config => ../../config

replace github.com/crazy-airhead/aifei-go/db => ../../db

replace github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy

replace github.com/crazy-airhead/aifei-go/log => ../../log
