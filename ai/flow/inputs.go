// inputs.go 输入组件（对标 components/inputs）：VarInput 常量注入、VarCopy
// 变量复制、ConsoleInput 标准输入、WebInput HTTP 拉取。
package aiflow

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/crazy-airhead/aifei-go/ai/httpx"
	"github.com/crazy-airhead/aifei-go/flow"
)

// VarInput 变量输入：节点全部 meta 原样转入上下文变量。
type VarInput struct{ ComponentBase }

// NewVarInput 构建变量输入组件。
func NewVarInput() *VarInput { return &VarInput{} }

// Name 注册名。
func (c *VarInput) Name() string { return "VarInput" }

// Title 同注册名。
func (c *VarInput) Title() string { return "VarInput" }

// Description 组件描述。
func (c *VarInput) Description() string { return "变量输入组件" }

// Run 节点事件包装。
func (c *VarInput) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

func (c *VarInput) doRun(ctx flow.Context, node *flow.Node) error {
	// 所有元信息，转为上下文变量
	for k, v := range node.Metas() {
		ctx.Put(k, v)
	}
	return nil
}

// VarCopy 变量复制：meta 新键 → 旧键表达式（enjoy 求值，支持表达式引用）。
type VarCopy struct{ ComponentBase }

// NewVarCopy 构建变量复制组件。
func NewVarCopy() *VarCopy { return &VarCopy{} }

// Name 注册名。
func (c *VarCopy) Name() string { return "VarCopy" }

// Title 同注册名。
func (c *VarCopy) Title() string { return "VarCopy" }

// Description 组件描述。
func (c *VarCopy) Description() string { return "变量复制组件" }

// Run 节点事件包装。
func (c *VarCopy) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

func (c *VarCopy) doRun(ctx flow.Context, node *flow.Node) error {
	// 用元信息，复制上下文变量（值作为表达式求值）
	for newKey, old := range node.Metas() {
		v, err := evalExpr(fmt.Sprintf("%v", old), ctx.Vars())
		if err != nil {
			return err
		}
		ctx.Put(newKey, v)
	}
	return nil
}

// Stdin 控制台输入缝（缺省 os.Stdin；测试可替换注入）。
var Stdin io.Reader = os.Stdin

// ConsoleInput 控制台输入：阻塞读一行标准输入并写输出变量。
type ConsoleInput struct{ ComponentBase }

// NewConsoleInput 构建控制台输入组件。
func NewConsoleInput() *ConsoleInput { return &ConsoleInput{} }

// Name 注册名。
func (c *ConsoleInput) Name() string { return "ConsoleInput" }

// Title 同注册名。
func (c *ConsoleInput) Title() string { return "ConsoleInput" }

// Description 组件描述。
func (c *ConsoleInput) Description() string { return "控制台输入组件" }

// Run 节点事件包装。
func (c *ConsoleInput) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

// Input 覆写：读标准输入一行（EOF 返回空串——Java readLine 的 null 在 Go
// 侧无对应语义）。
func (c *ConsoleInput) Input(flow.Context, *flow.Node) any {
	line, _ := bufio.NewReader(Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func (c *ConsoleInput) doRun(ctx flow.Context, node *flow.Node) error {
	data := c.Input(ctx, node)
	c.SetOutput(ctx, node, data)
	return nil
}

// WebInput 文本输入（对标 WebInputCom）：Java 经 solon 请求上下文取参/上传
// 文件，Go 版无请求态——按契约改为 meta url（+可选 headers）的 httpx GET，
// 正文即 message；附件仅认上下文/meta 既有值。
type WebInput struct{ ComponentBase }

// NewWebInput 构建文本输入组件。
func NewWebInput() *WebInput { return &WebInput{} }

// Name 注册名。
func (c *WebInput) Name() string { return "WebInput" }

// Title 同注册名。
func (c *WebInput) Title() string { return "WebInput" }

// Description 组件描述。
func (c *WebInput) Description() string { return "文本输入组件" }

// Run 节点事件包装。
func (c *WebInput) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

func (c *WebInput) doRun(ctx flow.Context, node *flow.Node) error {
	message := c.ComponentBase.Input(ctx, node) // meta/上下文优先
	if message == nil {
		v, err := c.fetch(node)
		if err != nil {
			return err
		}
		message = v
	}
	attachment := c.ComponentBase.Attachment(ctx, node)

	if message == nil && attachment == nil {
		// Java 原条件写作 message==null && attachment!=null，与文案语义
		// 相悖（两皆空才无事可做）；按文案意图实现为两皆空报错。
		return fmt.Errorf("aiflow: WebInput: the input and attachment is null")
	}

	c.SetOutput(ctx, node, message)
	c.SetAttachment(ctx, node, attachment)
	return nil
}

// fetch httpx GET meta url（headers meta 可选；url 缺省返回 nil 走空判）。
func (c *WebInput) fetch(node *flow.Node) (any, error) {
	url := node.MetaAsString("url")
	if url == "" {
		return nil, nil
	}
	header := map[string]string{}
	if hs, ok := node.Meta("headers").(map[string]any); ok {
		for k, v := range hs {
			header[k] = fmt.Sprintf("%v", v)
		}
	}
	body, _, err := httpx.New().Get(context.Background(), url, header)
	if err != nil {
		return nil, fmt.Errorf("aiflow: WebInput: get %s: %w", url, err)
	}
	return string(body), nil
}
