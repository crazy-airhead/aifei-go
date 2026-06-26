module github.com/crazy-airhead/aifei-go/cache

go 1.26

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.12
	github.com/crazy-airhead/aifei-go/config v0.0.12
	github.com/crazy-airhead/aifei-go/log v0.0.12
	github.com/mgtv-tech/jetcache-go v1.2.1
	github.com/redis/go-redis/v9 v9.21.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/coocood/freecache v1.2.4 // indirect
	github.com/dgraph-io/ristretto v1.0.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/vmihailenco/msgpack/v5 v5.4.1 // indirect
	github.com/vmihailenco/tagparser/v2 v2.0.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/exp v0.0.0-20240904232852-e7e105dedf7e // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../aifei
	github.com/crazy-airhead/aifei-go/config => ../config
	github.com/crazy-airhead/aifei-go/log => ../log
)
