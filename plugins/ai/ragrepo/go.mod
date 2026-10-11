module github.com/crazy-airhead/aifei-go/plugins/ai/ragrepo

go 1.27

require (
	github.com/crazy-airhead/aifei-go/ai v1.0.3
	github.com/redis/go-redis/v9 v9.21.0
	github.com/crazy-airhead/aifei-go/plugins/elasticsearch v0.0.0
)

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../../ai
	github.com/crazy-airhead/aifei-go/db => ../../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../../enjoy
	github.com/crazy-airhead/aifei-go/log => ../../../log
	github.com/crazy-airhead/aifei-go/plugins/elasticsearch => ../../elasticsearch
	github.com/crazy-airhead/aifei-go/aifei => ../../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../../config
	github.com/crazy-airhead/aifei-go/mcp => ../../../mcp
)

