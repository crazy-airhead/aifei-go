// excel.go Excel 文档加载器（对标 ExcelLoader，xuri/excelize/v2）：每个 sheet
// 首行为标题 → 行→map → JSON 数组文本；documentMaxRows 分批（-1 = 不分批，
// 缺省 200——Java 同款）。
package ragloader

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/xuri/excelize/v2"

	"github.com/crazy-airhead/aifei-go/ai/rag"
)

// DefaultExcelDocumentMaxRows 缺省批大小（Java documentMaxRows=200）。
const DefaultExcelDocumentMaxRows = 200

// ExcelLoader xlsx 加载器。
type ExcelLoader struct {
	open             func() (io.Reader, error)
	documentMaxRows  int
	meta             map[string]any
}

// NewExcelLoader 从 io.Reader 构造。
func NewExcelLoader(r io.Reader) *ExcelLoader {
	return &ExcelLoader{open: func() (io.Reader, error) { return r, nil },
		documentMaxRows: DefaultExcelDocumentMaxRows, meta: map[string]any{"type": "excel"}}
}

// NewExcelLoaderFromFile 从文件路径构造。
func NewExcelLoaderFromFile(name string) *ExcelLoader {
	return &ExcelLoader{open: func() (io.Reader, error) { return os.Open(name) },
		documentMaxRows: DefaultExcelDocumentMaxRows, meta: map[string]any{"type": "excel"}}
}

// DocumentMaxRows 每文档最多行数（<=0 保持缺省；显式关闭分批传 -1）。
func (l *ExcelLoader) DocumentMaxRows(n int) *ExcelLoader { l.documentMaxRows = n; return l }

// AdditionalMetadata 附加元数据。
func (l *ExcelLoader) AdditionalMetadata(k string, v any) rag.Loader {
	l.meta[k] = v
	return l
}

// Load 解析全部 sheet。
func (l *ExcelLoader) Load(ctx context.Context) ([]*rag.Document, error) {
	r, err := l.open()
	if err != nil {
		return nil, err
	}
	if c, ok := r.(io.Closer); ok {
		defer c.Close()
	}
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, fmt.Errorf("ragloader: excel open: %w", err)
	}
	defer f.Close()

	var documents []*rag.Document
	for _, sheet := range f.GetSheetList() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rows, err := f.GetRows(sheet)
		if err != nil {
			return nil, fmt.Errorf("ragloader: sheet %s: %w", sheet, err)
		}
		var titles []string
		var readAll []map[string]any
		for _, row := range rows {
			if isEmptyRow(row) {
				break // 空行终止（Java isEmpty(values) break 语义）
			}
			if titles == nil {
				titles = rowToStrings(row)
				continue
			}
			readAll = append(readAll, rowToMap(titles, rowToStrings(row)))
		}
		if len(readAll) == 0 {
			continue
		}
		if l.documentMaxRows < 0 {
			documents = append(documents, rowsDocument(readAll, l.meta, sheet))
			continue
		}
		batches := (len(readAll) + l.documentMaxRows - 1) / l.documentMaxRows
		for i := 0; i < batches; i++ {
			start := i * l.documentMaxRows
			end := start + l.documentMaxRows
			if end > len(readAll) {
				end = len(readAll)
			}
			documents = append(documents, rowsDocument(readAll[start:end], l.meta, sheet))
		}
	}
	return documents, nil
}

func rowsDocument(rows []map[string]any, meta map[string]any, sheet string) *rag.Document {
	b, err := json.Marshal(rows)
	if err != nil {
		b = []byte("[]")
	}
	return rag.NewDocument(string(b)).Meta("sheet", sheet).MetaMap(meta)
}

func rowToStrings(row []string) []string {
	out := make([]string, len(row))
	for i, v := range row {
		if v == "" {
			out[i] = "" // BLANK → ""
			continue
		}
		out[i] = v
	}
	return out
}

// rowToMap 标题行 + 值行 → map（缺列补 null 的字符串形态——Java String.valueOf(null)="null"
// 在 Go 收敛为空串，避免语料噪声）。
func rowToMap(titles []string, values []string) map[string]any {
	m := make(map[string]any, len(titles))
	for i, t := range titles {
		v := ""
		if i < len(values) {
			v = values[i]
		}
		m[t] = v
	}
	return m
}

func isEmptyRow(row []string) bool {
	for _, v := range row {
		if v != "" {
			return false
		}
	}
	return true
}

var _ rag.Loader = (*ExcelLoader)(nil)
