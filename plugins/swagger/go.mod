module github.com/crazy-airhead/aifei-go/plugins/swagger

go 1.26

replace (
	github.com/crazy-airhead/aifei-go/aifei => ../../aifei
	github.com/crazy-airhead/aifei-go/config => ../../config
	github.com/crazy-airhead/aifei-go/log => ../../log
)

require (
	github.com/crazy-airhead/aifei-go/aifei v0.0.15
	github.com/crazy-airhead/aifei-go/config v0.0.15-00010101000000-000000000000
	github.com/crazy-airhead/aifei-go/log v0.0.15-00010101000000-000000000000
	github.com/swaggo/swag v1.16.3
)

require (
	github.com/KyleBanks/depth v1.2.1 // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.20.0 // indirect
	github.com/go-openapi/spec v0.20.6 // indirect
	github.com/go-openapi/swag v0.19.15 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/mailru/easyjson v0.7.6 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	golang.org/x/tools v0.45.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
