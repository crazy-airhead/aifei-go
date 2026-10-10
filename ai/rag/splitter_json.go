// splitter_json.go JsonSplitter（对标同名）：JSON 数组/对象边界切分，
// 内容选择器 + 元数据映射器。数值经 encoding/json 标准解码为 float64
// （Java ONode→Long/Double；内容渲染 "1" vs "1.0" 的形态差异为已知化妆级偏离）。
package rag

import (
	"context"
	"encoding/json"
	"fmt"
)

// JsonSplitter JSON 文档切分器：数组数据切为多文档（嵌套数组递归展开，
// 对象叶子各成一文档；标量叶子丢弃——Java 同款）。
type JsonSplitter struct {
	contentSelector []string
	metadataMapper  func(map[string]any) map[string]any
}

// NewJsonSplitter 建切分器（无选择器 = 全字段平铺为内容）。
func NewJsonSplitter() *JsonSplitter { return &JsonSplitter{} }

// ContentSelector 内容选择器（只平铺选中键；缺键渲染 "key: null"——Java
// jsonData.get 缺键 null 同款）。链式。
func (s *JsonSplitter) ContentSelector(keys ...string) *JsonSplitter {
	s.contentSelector = append([]string(nil), keys...)
	return s
}

// MetadataMapper 元数据映射器（JSON 对象 → 元数据表；nil 映射结果 = 空元数据，
// 后由父文档元数据 putIfAbsent 补齐——Java 同款）。链式。
func (s *JsonSplitter) MetadataMapper(fn func(map[string]any) map[string]any) *JsonSplitter {
	s.metadataMapper = fn
	return s
}

// Split 切分（解析失败报错——Java ONode 解析异常对应物）。
func (s *JsonSplitter) Split(ctx context.Context, docs []*Document) ([]*Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var outs []*Document
	for _, doc := range docs {
		parts, err := s.splitJSON(doc.Content)
		if err != nil {
			return nil, err
		}
		for _, d := range parts {
			for k, v := range doc.Metadata { // 父元信息 putIfAbsent
				if _, ok := d.Metadata[k]; !ok {
					d.Metadata[k] = v
				}
			}
			outs = append(outs, d)
		}
	}
	return outs, nil
}

func (s *JsonSplitter) splitJSON(js string) ([]*Document, error) {
	var root any
	if err := json.Unmarshal([]byte(js), &root); err != nil {
		return nil, fmt.Errorf("rag: json splitter parse: %w", err)
	}
	var docs []*Document
	s.splitNode(root, &docs)
	return docs, nil
}

func (s *JsonSplitter) splitNode(node any, docs *[]*Document) {
	switch v := node.(type) {
	case []any:
		for _, n := range v {
			s.splitNode(n, docs)
		}
	case map[string]any:
		d := NewDocument(s.buildContent(v))
		if s.metadataMapper != nil {
			for k, val := range s.metadataMapper(v) {
				d.Metadata[k] = val // 直写：映射器产物整表并入（Java 构造器 putAll）
			}
		}
		*docs = append(*docs, d)
	}
	// 标量：丢弃（Java isObject/isArray 皆否即不产文档）
}

func (s *JsonSplitter) buildContent(data map[string]any) string {
	out := ""
	if len(s.contentSelector) == 0 {
		for k, v := range data { // map 迭代序不定——Java HashMap 同款，行序非稳定契约
			out += k + ": " + jsonValueString(v) + "\n"
		}
		return out
	}
	for _, k := range s.contentSelector {
		out += k + ": " + jsonValueString(data[k]) + "\n"
	}
	return out
}

// jsonValueString null 渲染对齐 Java（null 字面量；fmt.Sprint(nil) 是 "<nil>"）。
func jsonValueString(v any) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprint(v)
}

var _ Splitter = (*JsonSplitter)(nil)
