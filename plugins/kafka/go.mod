module github.com/crazy-airhead/aifei-go/plugins/kafka

go 1.26

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.17
	github.com/crazy-airhead/aifei-go/config v0.0.17
	github.com/crazy-airhead/aifei-go/log v0.0.17
	github.com/twmb/franz-go v1.21.4
)

require (
	github.com/klauspost/compress v1.18.6 // indirect
	github.com/pierrec/lz4/v4 v4.1.26 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.13.1 // indirect
	golang.org/x/crypto v0.51.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../config
	github.com/crazy-airhead/aifei-go/log => ../../log
)
