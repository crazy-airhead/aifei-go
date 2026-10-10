// loader.go 加载器 SPI（对标 DocumentLoader）+ TextLoader。
package rag

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf16"
)

// Loader 文档加载器 SPI。Go 增量：Load 带 ctx（可取消 IO）。
type Loader interface {
	// AdditionalMetadata 附加元数据（加载时并入每个文档；返回接口类型——
	// Java additionalMetadata 同款，链式追加不依赖具体实现）。
	AdditionalMetadata(k string, v any) Loader
	// Load 加载文档集。
	Load(ctx context.Context) ([]*Document, error)
}

// TextLoader 文本加载器（对标同名）：string / []byte / io.Reader / 文件路径
// 全源（Java File/URI/URL/byte[]/Supplier 五构造的 Go 收敛——URL 源经
// NewTextLoader(http resp.Body) 等 io.Reader 到达）。文件与 Reader 均在
// Load 时打开/读取（构造无 IO，错误面收口在 Load）。
type TextLoader struct {
	open    func() (io.Reader, io.Closer, error)
	charset string
	meta    map[string]any
}

// NewTextLoader 从 io.Reader 构造（Load 时一次性读取）。
func NewTextLoader(r io.Reader) *TextLoader {
	return &TextLoader{open: func() (io.Reader, io.Closer, error) { return r, nil, nil }}
}

// NewTextLoaderFromString 从字符串构造。
func NewTextLoaderFromString(s string) *TextLoader {
	return NewTextLoader(strings.NewReader(s))
}

// NewTextLoaderFromBytes 从字节构造。
func NewTextLoaderFromBytes(b []byte) *TextLoader {
	return NewTextLoader(strings.NewReader(string(b)))
}

// NewTextLoaderFromFile 从文件路径构造（Load 时打开）。
func NewTextLoaderFromFile(name string) *TextLoader {
	return &TextLoader{open: func() (io.Reader, io.Closer, error) {
		f, err := os.Open(name)
		if err != nil {
			return nil, nil, err
		}
		return f, f, nil
	}}
}

// Charset 文本编码（缺省 UTF-8；零依赖支持 utf-8 与 utf-16 族，其余编码
// 显式报错——Java 全 charset 面依赖 JDK，Go 有意收窄，重编码交给 P5 插件）。
func (t *TextLoader) Charset(cs string) *TextLoader { t.charset = cs; return t }

// AdditionalMetadata 附加元数据。
func (t *TextLoader) AdditionalMetadata(k string, v any) Loader {
	if t.meta == nil {
		t.meta = map[string]any{}
	}
	t.meta[k] = v
	return t
}

// Load 读取全文为单文档（附加元数据已并入）。
func (t *TextLoader) Load(ctx context.Context) ([]*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r, closer, err := t.open()
	if err != nil {
		return nil, err
	}
	if closer != nil {
		defer closer.Close()
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	text, err := decodeText(data, t.charset)
	if err != nil {
		return nil, err
	}
	return []*Document{NewDocument(text).MetaMap(t.meta)}, nil
}

var _ Loader = (*TextLoader)(nil)

// decodeText 字节按编码解码（utf-8 缺省；utf-16 族含 BOM 嗅探）。
func decodeText(data []byte, charset string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return string(data), nil
	case "utf-16", "utf-16le", "utf-16be":
		bigEndian := charset == "utf-16be"
		if len(data) >= 2 {
			if data[0] == 0xFE && data[1] == 0xFF { // BOM: 大端
				data, bigEndian = data[2:], true
			} else if data[0] == 0xFF && data[1] == 0xFE { // BOM: 小端
				data, bigEndian = data[2:], false
			}
		}
		u16 := make([]uint16, 0, len(data)/2)
		for i := 0; i+1 < len(data); i += 2 {
			if bigEndian {
				u16 = append(u16, uint16(data[i])<<8|uint16(data[i+1]))
			} else {
				u16 = append(u16, uint16(data[i+1])<<8|uint16(data[i]))
			}
		}
		return string(utf16.Decode(u16)), nil
	default:
		return "", fmt.Errorf("rag: unsupported charset %q (utf-8/utf-16 only; zero-dep)", charset)
	}
}
