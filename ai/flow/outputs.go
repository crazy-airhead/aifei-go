// outputs.go 输出组件（对标 components/outputs）：VarOutput 文本聚合、
// ConsoleOutput 控制台打印、WebOutput 流式/格式化写出，及 stream:true 的
// 事件流句柄（Java cold Publisher 的对应物）。
package aiflow

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/flow"
)

// MetaFormat 输出格式模板 meta 键（ConsoleOutput/WebOutput 共用）。
const MetaFormat = "format"

// ---- 事件流句柄（stream:true 的输出形态）----

// StreamHandle 事件流句柄：ChatModel stream 模式的输出（对标 Java
// chatModel...stream() 返回的 cold Flux——首个 Stream() 才发起请求，消费
// 驱动背压）。下游 VarOutput/WebOutput 经 InputAsString/逐帧写出消费。
type StreamHandle struct {
	req  ai.RequestDesc
	once sync.Once
	ch   <-chan *ai.ChatEvent
}

// NewStreamHandle 包装一次流式请求（不启动）。
func NewStreamHandle(req ai.RequestDesc) *StreamHandle {
	return &StreamHandle{req: req}
}

// Stream 启动流（仅首次调用生效）并返回事件通道；通道关闭即流终止。
func (h *StreamHandle) Stream() <-chan *ai.ChatEvent {
	h.once.Do(func() {
		// StreamChan 的 error 恒 nil（失败经终态 ERROR 事件表达）
		h.ch, _ = ai.StreamChan(context.Background(), h.req)
	})
	return h.ch
}

// Each 逐事件消费至通道关闭；ERROR 事件以其 Err 终止迭代（错误不吞）。
func (h *StreamHandle) Each(fn func(*ai.ChatEvent) error) error {
	for ev := range h.Stream() {
		if err := fn(ev); err != nil {
			return err
		}
		if ev.Type == ai.EventError && ev.Err != nil {
			return ev.Err
		}
	}
	return nil
}

// ---- 文本聚合（VarOutputCom.getInputAsString / textOf）----

// TextOf 流元素正文文本：仅 TEXT_DELTA 计入（思考/工具/生命周期跳过）。
func TextOf(ev *ai.ChatEvent) string {
	if ev != nil && ev.Type == ai.EventTextDelta {
		return ev.Text
	}
	return ""
}

// InputAsString 把组件输入聚合为字符串：流句柄聚合正文增量、*ai.Response
// 取消息正文、其余字符串化。
func InputAsString(data any) (string, error) {
	switch v := data.(type) {
	case *StreamHandle:
		var b strings.Builder
		err := v.Each(func(ev *ai.ChatEvent) error {
			b.WriteString(TextOf(ev))
			return nil
		})
		return b.String(), err
	case *ai.Response:
		return v.Message().Text, nil
	default:
		return fmt.Sprintf("%v", data), nil
	}
}

// ---- 输出组件 ----

// VarOutput 变量输出：输入聚合为文本写输出变量。
type VarOutput struct{ ComponentBase }

// NewVarOutput 构建变量输出组件。
func NewVarOutput() *VarOutput { return &VarOutput{} }

// Name 注册名。
func (c *VarOutput) Name() string { return "VarOutput" }

// Title 同注册名。
func (c *VarOutput) Title() string { return "VarOutput" }

// Description 组件描述。
func (c *VarOutput) Description() string { return "变量输出组件" }

// Run 节点事件包装。
func (c *VarOutput) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

func (c *VarOutput) doRun(ctx flow.Context, node *flow.Node) error {
	s, err := InputAsString(c.Input(ctx, node))
	if err != nil {
		return err
	}
	c.SetOutput(ctx, node, s)
	return nil
}

// ConsoleWriter 控制台输出缝（缺省 os.Stdout；测试注入）。
var ConsoleWriter io.Writer = os.Stdout

// ConsoleOutput 控制台输出（对标 ConsoleOutputCom extends VarOutputCom）：
// 写输出变量后打印；有 format meta 时以模板渲染打印（打印值不写回变量）。
type ConsoleOutput struct{ VarOutput }

// NewConsoleOutput 构建控制台输出组件。
func NewConsoleOutput() *ConsoleOutput { return &ConsoleOutput{} }

// Name 注册名。
func (c *ConsoleOutput) Name() string { return "ConsoleOutput" }

// Title 同注册名。
func (c *ConsoleOutput) Title() string { return "ConsoleOutput" }

// Description 组件描述。
func (c *ConsoleOutput) Description() string { return "控制台输出组件" }

// Run 节点事件包装。
func (c *ConsoleOutput) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

func (c *ConsoleOutput) doRun(ctx flow.Context, node *flow.Node) error {
	s, err := InputAsString(c.Input(ctx, node))
	if err != nil {
		return err
	}
	c.SetOutput(ctx, node, s)

	format := node.MetaAsString(MetaFormat)
	if format == "" {
		_, err = fmt.Fprintln(ConsoleWriter, s)
		return err
	}
	rendered, err := evalTmpl(format, ctx.Vars())
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(ConsoleWriter, rendered)
	return err
}

// WebWriter Web 输出缝（缺省 os.Stdout；实际部署注入 http.ResponseWriter——
// 契约 §9，WebOutput 不直接耦合 server 包）。
var WebWriter io.Writer = os.Stdout

// WebOutput 文本输出（对标 WebOutputCom）：无 format 时逐帧/整帧写出（流式
// 增量即写即刷，Java ctx.render + flush 的对应物）并汇总写回输出变量；有
// format 时聚合渲染一次写出。
type WebOutput struct{ ComponentBase }

// NewWebOutput 构建文本输出组件。
func NewWebOutput() *WebOutput { return &WebOutput{} }

// Name 注册名。
func (c *WebOutput) Name() string { return "WebOutput" }

// Title 同注册名。
func (c *WebOutput) Title() string { return "WebOutput" }

// Description 组件描述。
func (c *WebOutput) Description() string { return "文本输出组件" }

// Run 节点事件包装。
func (c *WebOutput) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

func (c *WebOutput) doRun(ctx flow.Context, node *flow.Node) error {
	data := c.Input(ctx, node)
	format := node.MetaAsString(MetaFormat)

	if format != "" {
		// 有格式符：聚合 → 写变量 → 模板渲染一次写出
		s, err := InputAsString(data)
		if err != nil {
			return err
		}
		c.SetOutput(ctx, node, s)
		rendered, err := evalTmpl(format, ctx.Vars())
		if err != nil {
			return err
		}
		return webWrite(rendered)
	}

	// 无格式符：逐帧/整帧写出 + 汇总写回
	var b strings.Builder
	switch v := data.(type) {
	case *StreamHandle:
		err := v.Each(func(ev *ai.ChatEvent) error {
			text := TextOf(ev)
			if text == "" {
				return nil
			}
			b.WriteString(text)
			return webWrite(text)
		})
		if err != nil {
			return err
		}
	case *ai.Response:
		s := v.Message().Text
		b.WriteString(s)
		if err := webWrite(s); err != nil {
			return err
		}
	default:
		s := fmt.Sprintf("%v", data)
		b.WriteString(s)
		if err := webWrite(s); err != nil {
			return err
		}
	}
	c.SetOutput(ctx, node, b.String())
	return nil
}

// webWrite 写一帧 + 换行 + 尽力刷新（响应链上有 Flusher 才刷）。
func webWrite(s string) error {
	if _, err := fmt.Fprintln(WebWriter, s); err != nil {
		return err
	}
	if f, ok := WebWriter.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
