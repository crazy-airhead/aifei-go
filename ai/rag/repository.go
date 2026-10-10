// repository.go 知识库 SPI（对标 Repository）+ 能力接口（Java 用接口继承
// RepositoryStorable/RepositoryLifecycle，Go 用独立能力接口——使用方以类型
// 断言探测，database/sql 风格：`if s, ok := repo.(Storer); ok { … }`）。
package rag

import "context"

// Repository 知识库主接口（只读检索面；网络搜索型仓储天然只有此面）。
type Repository interface {
	// Search 简单检索（缺省 Query 条件）。
	Search(ctx context.Context, query string) ([]*Document, error)
	// SearchQuery 条件检索。
	SearchQuery(ctx context.Context, q *Query) ([]*Document, error)
}

// Storer 可存储能力接口（Java RepositoryStorable；Java 的 asyncSave 不迁——
// Go 调用方 `go` 一句即得，不替调用方起协程）。progress 为批次粒度回调
// （done 已完成批数，total 总批数）。
type Storer interface {
	Save(ctx context.Context, docs []*Document, progress func(done, total int)) error
	DeleteByID(ctx context.Context, ids ...string) error
	ExistsByID(ctx context.Context, id string) (bool, error)
}

// Lifecycle 生命周期能力接口（Java RepositoryLifecycle；ctx 为 Go 增量）。
type Lifecycle interface {
	InitRepository(ctx context.Context) error
	DropRepository(ctx context.Context) error
}

// FilterTransformer 过滤表达式翻译能力接口（后端可选实现，Java FilterTransformer
// 对应物）：能把 enjoy 过滤表达式译成原生过滤（ES bool query、SQL WHERE…）的
// 后端返回 (native, true)；否则 (nil, false)，使用方回退内存过滤（对齐 Java
// disableRefilter=false 的默认行为）。
type FilterTransformer interface {
	TransformFilter(expr string) (native any, ok bool)
}
