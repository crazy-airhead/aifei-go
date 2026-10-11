module github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v1.2.0
	github.com/crazy-airhead/aifei-go/plugins/elasticsearch v1.2.0
	github.com/redis/go-redis/v9 v9.21.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/crazy-airhead/aifei-go/aifei v1.2.0 // indirect
	github.com/crazy-airhead/aifei-go/config v1.2.0 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v1.2.0 // indirect
	github.com/crazy-airhead/aifei-go/log v1.2.0 // indirect
	github.com/elastic/elastic-transport-go/v8 v8.7.0 // indirect
	github.com/elastic/go-elasticsearch/v8 v8.18.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.38.0 // indirect
	go.opentelemetry.io/otel/metric v1.38.0 // indirect
	go.opentelemetry.io/otel/trace v1.38.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../../ai
	github.com/crazy-airhead/aifei-go/aifei => ../../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../../config
	github.com/crazy-airhead/aifei-go/db => ../../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../../enjoy
	github.com/crazy-airhead/aifei-go/log => ../../../log
	github.com/crazy-airhead/aifei-go/mcp => ../../../mcp
	github.com/crazy-airhead/aifei-go/plugins/elasticsearch => ../../elasticsearch
)
