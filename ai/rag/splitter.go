// splitter.go 切分器 SPI（对标 DocumentSplitter）。四个内置切分器与 Pipeline
// 在 splitter_impl.go（P4-b）。
package rag

import "context"

// Splitter 文档切分器 SPI。
type Splitter interface {
	// Split 切分文档集（入参 docs 的每个文档可能产出多个子文档；元数据按
	// Java 惯例由各实现决定继承面）。
	Split(ctx context.Context, docs []*Document) ([]*Document, error)
}
