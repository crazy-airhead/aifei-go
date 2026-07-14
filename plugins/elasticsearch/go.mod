module github.com/crazy-airhead/aifei-go/plugins/elasticsearch

go 1.26

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.29
	github.com/crazy-airhead/aifei-go/config v0.0.29
	github.com/crazy-airhead/aifei-go/log v0.0.29
	github.com/elastic/go-elasticsearch/v8 v8.18.0
)

require (
	github.com/elastic/elastic-transport-go/v8 v8.7.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.38.0 // indirect
	go.opentelemetry.io/otel/metric v1.38.0 // indirect
	go.opentelemetry.io/otel/trace v1.38.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../config
	github.com/crazy-airhead/aifei-go/log => ../../log
)
