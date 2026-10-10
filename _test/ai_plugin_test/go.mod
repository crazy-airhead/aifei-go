module github.com/crazy-airhead/aifei-go/_test/ai_plugin_test

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v1.0.3
	github.com/crazy-airhead/aifei-go/config v1.0.3
	github.com/crazy-airhead/aifei-go/db v1.0.3
	github.com/crazy-airhead/aifei-go/mcp v1.0.3
	github.com/crazy-airhead/aifei-go/plugins/ai v1.0.3
	modernc.org/sqlite v1.53.0
)

require (
	github.com/crazy-airhead/aifei-go/ai/agent v1.0.3 // indirect
	github.com/crazy-airhead/aifei-go/aifei v1.0.3 // indirect
	github.com/crazy-airhead/aifei-go/dami v1.0.3 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v1.0.3 // indirect
	github.com/crazy-airhead/aifei-go/flow v1.0.3 // indirect
	github.com/crazy-airhead/aifei-go/log v1.0.3 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	golang.org/x/sys v0.45.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.73.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../ai
	github.com/crazy-airhead/aifei-go/ai/agent => ../../ai/agent
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../config
	github.com/crazy-airhead/aifei-go/dami => ../../dami
	github.com/crazy-airhead/aifei-go/db => ../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../enjoy
	github.com/crazy-airhead/aifei-go/flow => ../../flow
	github.com/crazy-airhead/aifei-go/log => ../../log
	github.com/crazy-airhead/aifei-go/mcp => ../../mcp
	github.com/crazy-airhead/aifei-go/plugins/ai => ../../plugins/ai
)

replace github.com/crazy-airhead/aifei-go/plugins/ai/ragloader => ../../plugins/ai/ragloader
