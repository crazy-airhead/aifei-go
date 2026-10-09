// component.go 组件模型（对标 AiComponent + AbsAiComponent + AiIoComponent +
// AiPropertyComponent）：Component 契约、IO/属性约定基座、节点事件包装与
// SnEL 模板求值的 enjoy 对应物。
package aiflow

import (
	"fmt"
	"strings"

	"github.com/crazy-airhead/aifei-go/dami"
	"github.com/crazy-airhead/aifei-go/enjoy"
	"github.com/crazy-airhead/aifei-go/flow"
)

// Component AI 流编排组件契约（对标 AiComponent）：具名 flow 任务组件——
// YAML task 字段 `@名称` 经 Container 派发到 Run。
type Component interface {
	flow.NamedTaskComponent
	// Description 组件描述。
	Description() string
}

// ComponentBase 组件基座：IO 约定（输入 meta 优先、缺省键 message；输出/
// 附件同名覆写）与属性累积约定（上下文键 property_<name> 的列表）。各组件
// 内嵌后按需覆写 Input/Attachment 等方法（Go 内嵌遮蔽即覆写）。节点事件
// 包装经 nodeEvents 辅助（Go 无模板方法，各组件 Run 一行委托）。
type ComponentBase struct{}

// InputName 输入变量名（meta input 覆写，缺省 message）。
func (ComponentBase) InputName(node *flow.Node) string {
	if node.HasMeta(MetaInput) {
		return node.MetaAsString(MetaInput)
	}
	return CtxMessage
}

// AttachmentName 附件变量名（meta attachment 覆写，缺省 attachment）。
func (ComponentBase) AttachmentName(node *flow.Node) string {
	if node.HasMeta(MetaAttachment) {
		return node.MetaAsString(MetaAttachment)
	}
	return CtxAttachment
}

// OutputName 输出变量名（meta output 覆写，缺省 message）。
func (ComponentBase) OutputName(node *flow.Node) string {
	if node.HasMeta(MetaOutput) {
		return node.MetaAsString(MetaOutput)
	}
	return CtxMessage
}

// Input 读取输入：按解析后的输入名先查节点 meta、再查上下文（Java meta 优先）。
func (b ComponentBase) Input(ctx flow.Context, node *flow.Node) any {
	name := b.InputName(node)
	if v := node.Meta(name); v != nil {
		return v
	}
	return ctx.Get(name)
}

// Attachment 读取附件：与输入方向相反，上下文优先、meta 兜底。
func (b ComponentBase) Attachment(ctx flow.Context, node *flow.Node) any {
	name := b.AttachmentName(node)
	if v := ctx.Get(name); v != nil {
		return v
	}
	return node.Meta(name)
}

// SetAttachment 写附件变量。
func (b ComponentBase) SetAttachment(ctx flow.Context, node *flow.Node, data any) {
	ctx.Put(b.AttachmentName(node), data)
}

// SetOutput 写输出变量。
func (b ComponentBase) SetOutput(ctx flow.Context, node *flow.Node, data any) {
	ctx.Put(b.OutputName(node), data)
}

// ---- 属性约定（AiPropertyComponent）----

// Property 属性首个值（缺 nil）。
func (b ComponentBase) Property(ctx flow.Context, name string) any {
	if l := b.PropertyAll(ctx, name); len(l) > 0 {
		return l[0]
	}
	return nil
}

// PropertyAll 属性全量列表。
func (b ComponentBase) PropertyAll(ctx flow.Context, name string) []any {
	l, _ := flow.GetAs[[]any](ctx, propKey(name))
	return l
}

// SetProperty 覆写属性（单值包一层列表，Java setProperty 同款）。
func (b ComponentBase) SetProperty(ctx flow.Context, name string, value any) {
	ctx.Put(propKey(name), []any{value})
}

// AddProperty 追加属性（Java computeIfAbsent + add 的等价实现）。
func (b ComponentBase) AddProperty(ctx flow.Context, name string, value any) {
	l := b.PropertyAll(ctx, name)
	if l == nil {
		l = []any{}
	}
	ctx.Put(propKey(name), append(l, value))
}

func propKey(name string) string { return CtxProperty + "_" + name }

// ---- 节点事件包装（AbsAiComponent.run）----

// nodeEvents 在 doRun 前后发送节点事件：doRun 出错时不发 end（Java 无
// finally，异常路径不补发）。各组件 Run 一行委托：
//
//	func (c *Xxx) Run(ctx flow.Context, node *flow.Node) error {
//	    return nodeEvents(ctx, node, c.doRun)
//	}
func nodeEvents(ctx flow.Context, node *flow.Node, doRun func(flow.Context, *flow.Node) error) error {
	_, _ = dami.SendOn(ctx.EventBus(), EventFlowNodeStart, &NodeEvent{Ctx: ctx, Node: node})
	if err := doRun(ctx, node); err != nil {
		return err
	}
	_, _ = dami.SendOn(ctx.EventBus(), EventFlowNodeEnd, &NodeEvent{Ctx: ctx, Node: node})
	return nil
}

// ---- 表达式与模板（SnEL.eval / SnEL.evalTmpl 的 enjoy 对应物）----

// evalExpr enjoy 表达式求值（对标 SnEL.eval；VarCopy 的变量引用、模板的
// #{expr} 片段共用）。
func evalExpr(code string, vars map[string]any) (any, error) {
	expr, err := enjoy.ParseExpr(code)
	if err != nil {
		return nil, fmt.Errorf("aiflow: parse expression %q: %w", code, err)
	}
	return expr.Eval(enjoy.NewScope(vars), enjoy.NewCtrl()), nil
}

// evalTmpl 渲染 SnEL 风格模板：`#{expr}` 表达式片段走 enjoy 求值、`${name}`
// 直取变量、其余原样输出；无标记时原文返回（Java hasMarker 短路同款）。
// 未闭合的标记按原文输出（容错）。
func evalTmpl(tmpl string, vars map[string]any) (string, error) {
	if !strings.Contains(tmpl, "#{") && !strings.Contains(tmpl, "${") {
		return tmpl, nil
	}
	var b strings.Builder
	for i := 0; i < len(tmpl); {
		c := tmpl[i]
		if (c == '#' || c == '$') && i+1 < len(tmpl) && tmpl[i+1] == '{' {
			rest := tmpl[i+2:]
			end := strings.IndexByte(rest, '}')
			if end < 0 {
				b.WriteString(tmpl[i:])
				break
			}
			frag := rest[:end]
			if c == '#' {
				v, err := evalExpr(frag, vars)
				if err != nil {
					return "", err
				}
				b.WriteString(valueToString(v))
			} else {
				b.WriteString(valueToString(vars[frag]))
			}
			i += 2 + end + 1
			continue
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), nil
}

// valueToString 模板值字符串化（nil → 空串——Java String.valueOf(null) 输出
// "null" 属噪音，有意偏离）。
func valueToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case fmt.Stringer:
		return t.String()
	default:
		return fmt.Sprintf("%v", t)
	}
}
