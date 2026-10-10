// excel.go Excel 文档加载器（xlsx/.xls 经 markitdown 统一引擎转 Markdown 表；
// sheet 边界由引擎输出的 `## SheetName` 标题提供——按 sheet 切分）。
// DocumentMaxRows 语义映射为「每文档最大非空行数」（表格行 + 正文行统一按行
// 计——JSON 行映射形态随统一处理取消，输出为 Markdown 表）。
package ragloader

import (
	"context"
	"io"
	"os"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// DefaultExcelDocumentMaxRows 缺省批大小（每文档非空行数上限）。
const DefaultExcelDocumentMaxRows = 200

// ExcelLoader xlsx 加载器（markitdown 引擎）。
type ExcelLoader struct {
	open            func() (io.Reader, error)
	filename        string
	documentMaxRows int
	meta            map[string]any
}

// NewExcelLoader 从 io.Reader 构造（缺省按 .xlsx 路由）。
func NewExcelLoader(r io.Reader) *ExcelLoader {
	return NewExcelLoaderNamed(r, "workbook.xlsx")
}

// NewExcelLoaderNamed 从 io.Reader + 文件名构造（.xls 旧格式 markitdown 有
// 对应转换器——可路由）。
func NewExcelLoaderNamed(r io.Reader, filename string) *ExcelLoader {
	return &ExcelLoader{open: func() (io.Reader, error) { return r, nil },
		filename: filename, documentMaxRows: DefaultExcelDocumentMaxRows,
		meta: map[string]any{"type": "excel"}}
}

// NewExcelLoaderFromFile 从文件路径构造。
func NewExcelLoaderFromFile(name string) *ExcelLoader {
	return &ExcelLoader{open: func() (io.Reader, error) { return os.Open(name) },
		filename: name, documentMaxRows: DefaultExcelDocumentMaxRows,
		meta: map[string]any{"type": "excel"}}
}

// DocumentMaxRows 每文档最多非空行数（<=0 保持缺省；-1 关闭分批）。
func (l *ExcelLoader) DocumentMaxRows(n int) *ExcelLoader { l.documentMaxRows = n; return l }

// AdditionalMetadata 附加元数据。
func (l *ExcelLoader) AdditionalMetadata(k string, v any) rag.Loader {
	l.meta[k] = v
	return l
}

// Load markitdown 转换 + sheet 切分 + 行分批。
func (l *ExcelLoader) Load(ctx context.Context) ([]*rag.Document, error) {
	data, err := readAll(l.open)
	if err != nil {
		return nil, err
	}
	markdown, _, err := convertToMarkdown(ctx, data, l.filename)
	if err != nil {
		return nil, err
	}
	return segmentsToDocs(splitMarkdownSheets(markdown), l.meta, l.documentMaxRows, false), nil
}

var _ rag.Loader = (*ExcelLoader)(nil)
