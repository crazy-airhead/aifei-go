module github.com/crazy-airhead/aifei-go/plugins/ai

go 1.27

require (
	github.com/blevesearch/bleve/v2 v2.6.1
	github.com/crazy-airhead/aifei-go/ai v1.0.3
	github.com/crazy-airhead/aifei-go/ai/agent v1.0.3
	github.com/crazy-airhead/aifei-go/aifei v1.0.3
	github.com/crazy-airhead/aifei-go/config v1.0.3
	github.com/crazy-airhead/aifei-go/db v1.0.3
	github.com/crazy-airhead/aifei-go/log v1.0.3
	github.com/crazy-airhead/aifei-go/mcp v1.0.3
	github.com/crazy-airhead/aifei-go/plugins/ai/ragloader v0.0.0
	github.com/go-ego/gse v1.1.0
)

require (
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.3.3 // indirect
	github.com/PuerkitoBio/goquery v1.10.0 // indirect
	github.com/RoaringBitmap/roaring/v2 v2.14.5 // indirect
	github.com/andybalholm/cascadia v1.3.3 // indirect
	github.com/bits-and-blooms/bitset v1.24.2 // indirect
	github.com/blevesearch/bleve_index_api v1.4.1 // indirect
	github.com/blevesearch/geo v0.2.6 // indirect
	github.com/blevesearch/go-faiss v1.1.5 // indirect
	github.com/blevesearch/go-porterstemmer v1.0.3 // indirect
	github.com/blevesearch/gtreap v0.1.1 // indirect
	github.com/blevesearch/mmap-go v1.2.0 // indirect
	github.com/blevesearch/scorch_segment_api/v2 v2.4.10 // indirect
	github.com/blevesearch/segment v0.9.1 // indirect
	github.com/blevesearch/snowballstem v0.9.0 // indirect
	github.com/blevesearch/upsidedown_store_api v1.0.2 // indirect
	github.com/blevesearch/vellum v1.2.0 // indirect
	github.com/blevesearch/zapx/v11 v11.4.3 // indirect
	github.com/blevesearch/zapx/v12 v12.4.3 // indirect
	github.com/blevesearch/zapx/v13 v13.4.3 // indirect
	github.com/blevesearch/zapx/v14 v14.4.3 // indirect
	github.com/blevesearch/zapx/v15 v15.4.3 // indirect
	github.com/blevesearch/zapx/v16 v16.3.4 // indirect
	github.com/blevesearch/zapx/v17 v17.2.3 // indirect
	github.com/conductor-oss/markitdown v0.0.1 // indirect
	github.com/crazy-airhead/aifei-go/dami v1.0.3 // indirect
	github.com/crazy-airhead/aifei-go/enjoy v1.0.3 // indirect
	github.com/crazy-airhead/aifei-go/flow v1.0.3 // indirect
	github.com/extrame/ole2 v0.0.0-20160812065207-d69429661ad7 // indirect
	github.com/extrame/xls v0.0.1 // indirect
	github.com/gabriel-vasile/mimetype v1.4.8 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jolestar/go-commons-pool/v2 v2.1.2 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/klippa-app/go-pdfium v1.17.3 // indirect
	github.com/mmcdole/gofeed v1.3.0 // indirect
	github.com/mmcdole/goxpp v1.1.1-0.20240225020742-a0c311522b23 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/mohae/deepcopy v0.0.0-20170929034955-c48cc78d4826 // indirect
	github.com/mschoch/smat v0.2.0 // indirect
	github.com/richardlehane/mscfb v1.0.4 // indirect
	github.com/richardlehane/msoleps v1.0.4 // indirect
	github.com/saintfish/chardet v0.0.0-20230101081208-5e3ef4b5456d // indirect
	github.com/tetratelabs/wazero v1.11.0 // indirect
	github.com/vcaesar/cedar v0.50.0 // indirect
	github.com/xuri/efp v0.0.0-20240408161823-9ad904a10d6d // indirect
	github.com/xuri/excelize/v2 v2.9.0 // indirect
	github.com/xuri/nfp v0.0.0-20240318013403-ab9948c2c4a7 // indirect
	github.com/yuin/goldmark v1.7.11 // indirect
	go.etcd.io/bbolt v1.4.0 // indirect
	golang.org/x/crypto v0.51.0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/protobuf v1.36.7 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
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
	github.com/crazy-airhead/aifei-go/plugins/ai/ragloader => ./ragloader
)
