package ai

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// buildInputSchema 由参数描述生成 JSON Schema：
// {"type":"object","properties":{<排序>},"required":[声明序]}（properties 恒写、required 恒写，
// Go map 序列化按键排序，等价 Java TreeMap 排序）。
func buildInputSchema(params []ParamSpec) string {
	props := map[string]any{}
	required := []string{}
	for _, p := range params {
		var node map[string]any
		if p.Schema != nil {
			node = p.Schema // 嵌套节点直通（保 properties/items 细节）
		} else {
			node = map[string]any{"type": p.Type}
		}
		if p.Desc != "" {
			node["description"] = p.Desc
		}
		if p.Format != "" {
			node["format"] = p.Format
		}
		if p.Default != nil {
			node["default"] = p.Default
		}
		if len(p.Enum) > 0 {
			node["enum"] = p.Enum
		}
		if p.Items != nil {
			node["items"] = p.Items
		}
		props[p.Name] = node
		if p.Required {
			required = append(required, p.Name)
		}
	}
	root := map[string]any{"type": "object", "properties": props, "required": required}
	data, err := json.Marshal(root)
	if err != nil {
		return `{"type":"object","properties":{},"required":[]}`
	}
	return string(data)
}

// typeSchemaNode Go 类型 → JSON Schema 节点（reflect 绑定与 OutputSchemaOf 共用）。
// string/int/float/bool → 基础类型；time.Time → string+date-time；slice → array；
// map/json.RawMessage → object；struct → object（递归展开字段，param/desc/default tag）。
func typeSchemaNode(t reflect.Type) map[string]any {
	for t != nil && (t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface) {
		if t.Kind() == reflect.Interface {
			return map[string]any{}
		}
		t = t.Elem()
	}
	if t == nil {
		return map[string]any{}
	}
	if t == reflect.TypeOf(time.Time{}) {
		return map[string]any{"type": "string", "format": "date-time"}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string"}
		}
		return map[string]any{"type": "array", "items": typeSchemaNode(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object"}
	case reflect.Struct:
		props := map[string]any{}
		required := []string{}
		for _, f := range fieldsOf(t) {
			node := typeSchemaNode(f.typ)
			if f.desc != "" {
				node["description"] = f.desc
			}
			if f.format != "" {
				node["format"] = f.format
			}
			if f.def != "" {
				node["default"] = f.def
			}
			props[f.name] = node
			if f.required {
				required = append(required, f.name)
			}
		}
		return map[string]any{"type": "object", "properties": props, "required": required}
	}
	return map[string]any{}
}

type fieldMeta struct {
	name     string
	typ      reflect.Type
	required bool
	desc     string
	def      string
	format   string
}

// fieldsOf 展开 struct 字段（含嵌入结构扁平化），读取 param/desc/default/format tag。
func fieldsOf(t reflect.Type) []fieldMeta {
	var out []fieldMeta
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			out = append(out, fieldsOf(f.Type)...)
			continue
		}
		fm := fieldMeta{typ: f.Type, name: f.Name}
		if jt, ok := f.Tag.Lookup("json"); ok && jt != "" && jt != "-" {
			fm.name = strings.Split(jt, ",")[0]
		}
		if pt, ok := f.Tag.Lookup("param"); ok && pt != "" && pt != "-" {
			parts := strings.Split(pt, ",")
			if parts[0] != "" {
				fm.name = parts[0]
			}
			for _, opt := range parts[1:] {
				if opt == "required" {
					fm.required = true
				}
			}
		}
		fm.desc, _ = f.Tag.Lookup("desc")
		fm.def, _ = f.Tag.Lookup("default")
		fm.format, _ = f.Tag.Lookup("format")
		out = append(out, fm)
	}
	return out
}

// paramsOfStruct struct 类型 → ParamSpec 列表（reflect 绑定用，等价 Java buildInputParams 的 @Body 分支）。
// 嵌套 object/array 走 Schema 全节点直通，标量拆散字段。
func paramsOfStruct(t reflect.Type) []ParamSpec {
	var out []ParamSpec
	for _, fm := range fieldsOf(t) {
		node := typeSchemaNode(fm.typ)
		p := ParamSpec{Name: fm.name, Desc: fm.desc,
			Default: typedDefault(fm.def, fm.typ), Required: fm.required}
		if _, hasProps := node["properties"]; hasProps || node["items"] != nil {
			p.Schema = node
		} else {
			p.Type = stringTypeOf(node)
			if f, ok := node["format"].(string); ok {
				p.Format = f
			}
		}
		out = append(out, p)
	}
	return out
}

// typedDefault default tag 字符串按字段类型转换（对齐 Java ONode.toBean 的 typed default）。
func typedDefault(def string, t reflect.Type) any {
	if def == "" {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if i, err := strconv.ParseInt(def, 10, 64); err == nil {
			return i
		}
	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(def, 64); err == nil {
			return f
		}
	case reflect.Bool:
		if b, err := strconv.ParseBool(def); err == nil {
			return b
		}
	}
	return def
}

func stringTypeOf(node map[string]any) string {
	if s, ok := node["type"].(string); ok {
		return s
	}
	return "object"
}

// OutputSchemaOf 由 Go 类型反推输出 schema（对标 ToolSchemaUtil.buildOutputSchema）。
func OutputSchemaOf[T any]() string {
	node := typeSchemaNode(reflect.TypeOf((*T)(nil)).Elem())
	data, err := json.Marshal(node)
	if err != nil {
		return fmt.Sprintf("ai: output schema marshal: %v", err)
	}
	return string(data)
}

// isIgnoreOutputSchema 基础返回类型不需要输出 schema（对标 isIgnoreOutputSchema）。
func isIgnoreOutputSchema(t reflect.Type) bool {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return true
	}
	if t == reflect.TypeOf(time.Time{}) {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}
