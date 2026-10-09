package ai

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"unicode"
)

var ctxType = reflect.TypeOf((*context.Context)(nil)).Elem()
var errType = reflect.TypeOf((*error)(nil)).Elem()

// ToolNamer 显式命名/描述工具方法（可选实现于 provider，优先于 kebab 化默认名）。
type ToolNamer interface {
	ToolName(method string) (name, desc string, ok bool)
}

// RegisterTools 扫描 provider 导出方法绑定为工具（@ToolMapping 等价物）。
// 方法签名（S 为参数 struct，T 为返回值）：
//
//	(ctx context.Context, a S) (T, error)
//	(a S) (T, error)
//	(ctx context.Context, a S) error
//	(a S) T
//
// 方法名 kebab 化为工具名（server.Register 路由同规则）；参数 struct 读
// param/desc/default tag 生成 schema；返回值 string 直出、其余 JSON 序列化；
// error/panic → ToolResultErr（前缀 "Error: "）。
func RegisterTools(provider any) ToolProvider {
	rv := reflect.ValueOf(provider)
	rt := rv.Type()
	var tools []FunctionTool
	for i := 0; i < rt.NumMethod(); i++ {
		if tool, ok := bindMethodTool(rv, rt.Method(i)); ok {
			tools = append(tools, tool)
		}
	}
	return &toolList{tools: tools}
}

type toolList struct{ tools []FunctionTool }

func (l *toolList) Tools() []FunctionTool { return l.tools }

func bindMethodTool(rv reflect.Value, m reflect.Method) (FunctionTool, bool) {
	if m.Name == "ToolName" { // ToolNamer 自身不绑为工具
		return nil, false
	}
	mt := m.Type // In(0) = receiver

	hasCtx := false
	var argsStruct reflect.Type
	inCount := mt.NumIn() - 1
	switch inCount {
	case 0:
	case 1:
		if mt.In(1) == ctxType {
			hasCtx = true
		} else if mt.In(1).Kind() == reflect.Struct {
			argsStruct = mt.In(1)
		} else {
			return nil, false
		}
	case 2:
		if mt.In(1) != ctxType || mt.In(2).Kind() != reflect.Struct {
			return nil, false
		}
		hasCtx, argsStruct = true, mt.In(2)
	default:
		return nil, false
	}

	var returnType reflect.Type
	switch mt.NumOut() {
	case 0:
	case 1:
		if mt.Out(0) == errType {
			// (error)
		} else {
			returnType = mt.Out(0)
		}
	case 2:
		if mt.Out(1) != errType {
			return nil, false
		}
		returnType = mt.Out(0)
	default:
		return nil, false
	}

	name, desc := kebabName(m.Name), ""
	if namer, ok := rv.Interface().(ToolNamer); ok {
		if n, d, hit := namer.ToolName(m.Name); hit {
			name, desc = n, d
		}
	}

	desc2 := NewTool(name).Desc(desc)
	if argsStruct != nil {
		for _, p := range paramsOfStruct(argsStruct) {
			desc2 = desc2.Param(p)
		}
	}
	desc2.Handler(func(ctx context.Context, args map[string]any) (any, error) {
		in := make([]reflect.Value, 0, 2)
		if hasCtx {
			in = append(in, reflect.ValueOf(ctx))
		}
		if argsStruct != nil {
			pv := reflect.New(argsStruct)
			if len(args) > 0 {
				data, err := json.Marshal(args)
				if err != nil {
					return nil, err
				}
				if err := json.Unmarshal(data, pv.Interface()); err != nil {
					return nil, err
				}
			}
			in = append(in, pv.Elem())
		}
		out := rv.Method(m.Index).Call(in)
		var result any
		idx := 0
		if len(out) > 0 && out[0].Type() != errType {
			result = out[0].Interface()
			idx = 1
		}
		if len(out) > idx {
			if e, ok := out[idx].Interface().(error); ok && e != nil {
				return nil, e
			}
		}
		return result, nil
	})
	tool := desc2.Build().(*toolImpl)
	tool.returnType = returnType
	if returnType != nil && !isIgnoreOutputSchema(returnType) {
		tool.outputSchemaStr = OutputSchemaOfType(returnType)
	}
	return tool, true
}

// OutputSchemaOfType 类型 → 输出 schema 字符串。
func OutputSchemaOfType(t reflect.Type) string {
	node := typeSchemaNode(t)
	data, err := json.Marshal(node)
	if err != nil {
		return ""
	}
	return string(data)
}

// kebabName camelCase → kebab-case（server.Register 路由同规则；缩写词不分隔成段）。
func kebabName(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) ||
				(i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
				b.WriteRune('-')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
