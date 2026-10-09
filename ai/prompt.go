package ai

// Prompt 提示：消息集合 + 显式 attrs（router 路由等运行期标注）。
type Prompt struct {
	messages []*Message
	attrs    map[string]any
}

func NewPrompt(msgs ...*Message) *Prompt {
	return &Prompt{messages: msgs}
}

// PromptOf 纯文本快捷形态（单条 user 消息）。
func PromptOf(content string) *Prompt {
	return NewPrompt(OfUser(content))
}

func (p *Prompt) Messages() []*Message { return p.messages }

// Attrs 懒初始化的标注袋（显式路由 id、talent 写入位等）。
func (p *Prompt) Attrs() map[string]any {
	if p.attrs == nil {
		p.attrs = map[string]any{}
	}
	return p.attrs
}

func (p *Prompt) SetAttr(k string, v any) *Prompt {
	p.Attrs()[k] = v
	return p
}

func (p *Prompt) LastMessage() *Message {
	if len(p.messages) == 0 {
		return nil
	}
	return p.messages[len(p.messages)-1]
}

// Copy 深拷贝（协议 copy-before-mutate 规则）。
func (p *Prompt) Copy() *Prompt {
	n := &Prompt{messages: make([]*Message, len(p.messages))}
	for i, m := range p.messages {
		n.messages[i] = m.Clone()
	}
	n.attrs = copyMap(p.attrs)
	return n
}
