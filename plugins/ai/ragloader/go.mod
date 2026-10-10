module github.com/crazy-airhead/aifei-go/plugins/ai/ragloader

go 1.27

require (
	github.com/PuerkitoBio/goquery v1.10.0
	github.com/crazy-airhead/aifei-go/ai v1.0.3
	github.com/ledongthuc/pdf v0.0.0-20260907135840-6c8c28e0e8a0
	github.com/xuri/excelize/v2 v2.9.0
	github.com/yuin/goldmark v1.7.8
)

require (
	github.com/andybalholm/cascadia v1.3.2 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v1.0.3 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/richardlehane/mscfb v1.0.4 // indirect
	github.com/richardlehane/msoleps v1.0.4 // indirect
	github.com/xuri/efp v0.0.0-20240408161823-9ad904a10d6d // indirect
	github.com/xuri/nfp v0.0.0-20240318013403-ab9948c2c4a7 // indirect
	golang.org/x/crypto v0.28.0 // indirect
	golang.org/x/net v0.30.0 // indirect
	golang.org/x/text v0.19.0 // indirect
)

replace (
	github.com/crazy-airhead/aifei-go/ai => ../../../ai
	github.com/crazy-airhead/aifei-go/db => ../../../db
	github.com/crazy-airhead/aifei-go/enjoy => ../../../enjoy
	github.com/crazy-airhead/aifei-go/log => ../../../log
)
