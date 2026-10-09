package ai_test

import (
	"strings"
	"testing"

	"github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/json"
)

// contentBlocks 把 Message.Content 的各种形态归一化为块列表，便于往返断言。
func contentBlocks(c any) []ai.ContentBlock {
	switch v := c.(type) {
	case string:
		return []ai.ContentBlock{ai.TextBlock{Text: v}}
	case ai.ContentBlock:
		return []ai.ContentBlock{v}
	case []ai.ContentBlock:
		return v
	case *ai.Contents:
		return v.Blocks()
	}
	return nil
}

func TestMessageRoleFactories(t *testing.T) {
	if m := ai.OfSystem("你是助手"); m.Role != ai.RoleSystem {
		t.Fatalf("OfSystem role = %q", m.Role)
	}
	sr := ai.OfSystemRole("translator", "把用户输入翻译为英文")
	if sr.Role != ai.RoleSystem {
		t.Fatalf("OfSystemRole role = %q", sr.Role)
	}
	if u := ai.OfUser("你好"); u.Role != ai.RoleUser || u.Content != "你好" {
		t.Fatalf("OfUser = %+v", u)
	}
	if a := ai.OfAssistant("好的"); a.Role != ai.RoleAssistant || a.Content != "好的" {
		t.Fatalf("OfAssistant = %+v", a)
	}
	ab := ai.OfAssistantBlocks([]ai.ContentBlock{ai.TextBlock{Text: "甲"}, ai.ImageBlockOfURL("https://x/1.png")})
	if ab.Role != ai.RoleAssistant {
		t.Fatalf("OfAssistantBlocks role = %q", ab.Role)
	}
	if len(contentBlocks(ab.Content)) != 2 {
		t.Fatalf("OfAssistantBlocks content = %#v", ab.Content)
	}
	tm := ai.OfTool(ai.ToolResultOf("42"), "queryOrder", "call_1", false)
	if tm.Role != ai.RoleTool {
		t.Fatalf("OfTool role = %q", tm.Role)
	}
}

func TestMessageRoundtrip(t *testing.T) {
	ts := int64(1700000000000)
	msgs := []*ai.Message{
		ai.OfSystem("系统指令").AddMetadata("k", "v"),
		{Role: ai.RoleUser, Content: "纯文本", CreatedAt: &ts},
		ai.OfUser([]ai.ContentBlock{
			ai.TextBlock{Text: "看图"},
			ai.ImageBlockOfURL("https://example.com/a.png"),
			ai.ImageBlockOfBase64([]byte("fake-bytes"), "image/png"),
			ai.AudioBlockOfURL("https://example.com/a.mp3", "audio/mpeg"),
			ai.TextBlock{Text: "再看"},
		}),
		ai.OfAssistant("回答"),
		ai.OfTool(ai.ToolResultOf("42"), "queryOrder", "call_1", false),
	}
	for i, m := range msgs {
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("msg %d marshal: %v", i, err)
		}
		var back ai.Message
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("msg %d unmarshal: %v\njson=%s", i, err, data)
		}
		if back.Role != m.Role {
			t.Fatalf("msg %d role = %q want %q", i, back.Role, m.Role)
		}
		if m.Role == ai.RoleTool {
			continue // 工具消息内容为 ToolResult，专项断言
		}
		want, got := contentBlocks(m.Content), contentBlocks(back.Content)
		if len(want) != len(got) {
			t.Fatalf("msg %d blocks len = %d want %d\njson=%s", i, len(got), len(want), data)
		}
		for j := range want {
			if !equalBlock(want[j], got[j]) {
				t.Fatalf("msg %d block %d = %#v want %#v\njson=%s", i, j, got[j], want[j], data)
			}
		}
	}
}

func TestToolMessageRoundtrip(t *testing.T) {
	m := ai.OfTool(ai.ToolResultOf("42"), "queryOrder", "call_1", false)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back ai.Message
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v\njson=%s", err, data)
	}
	if back.Role != ai.RoleTool {
		t.Fatalf("role = %q", back.Role)
	}
	tm, ok := back.Content.(*ai.ToolMessage)
	if !ok {
		t.Fatalf("tool content = %#v\njson=%s", back.Content, data)
	}
	if tm.Result.Text != "42" || tm.Result.IsError {
		t.Fatalf("tool result = %+v", tm.Result)
	}
	if tm.Name != "queryOrder" || tm.CallID != "call_1" {
		t.Fatalf("tool message = %+v", tm)
	}
}

func TestOfUserAugment(t *testing.T) {
	m := ai.OfUserAugment("今天是几号", "doc1\ndoc2")
	s, ok := m.Content.(string)
	if !ok {
		t.Fatalf("content = %#v", m.Content)
	}
	if !strings.HasPrefix(s, "今天是几号\n\n Now: ") {
		t.Fatalf("augment prefix = %q", s)
	}
	if !strings.Contains(s, "\n\n References: doc1\ndoc2") {
		t.Fatalf("augment references = %q", s)
	}
}

func TestMessageCloneDeep(t *testing.T) {
	ts := int64(1)
	m := ai.OfUser([]ai.ContentBlock{ai.TextBlock{Text: "原始"}, ai.ImageBlockOfURL("https://x/1.png")}).
		AddMetadata("k", "v")
	m.CreatedAt = &ts
	c := m.Clone()

	c.Metadata["k"] = "changed"
	cb := contentBlocks(c.Content)
	if tb, ok := cb[0].(ai.TextBlock); ok {
		tb.Text = "被改"
		cb[0] = tb
	} else {
		t.Fatalf("clone block 0 = %#v", cb[0])
	}
	*c.CreatedAt = 2

	if m.Metadata["k"] != "v" {
		t.Fatalf("original metadata mutated: %#v", m.Metadata)
	}
	if contentBlocks(m.Content)[0].(ai.TextBlock).Text != "原始" {
		t.Fatalf("original block mutated: %#v", m.Content)
	}
	if *m.CreatedAt != 1 {
		t.Fatalf("original createdAt mutated: %d", *m.CreatedAt)
	}
}

func TestBase64Downgrade(t *testing.T) {
	// > 64KiB base64 字符的内联数据应被降级：data 清空、metas 标记 external
	big := make([]byte, 70*1024)
	for i := range big {
		big[i] = byte(i)
	}
	m := ai.OfUser([]ai.ContentBlock{ai.ImageBlockOfBase64(big, "image/png")})
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 96*1024 { // 原始 base64 约 93K 字符，降级后应远小于此
		t.Fatalf("large base64 not compacted: %d bytes", len(data))
	}
	if !strings.Contains(string(data), `"storage":"external"`) {
		t.Fatalf("missing storage:external marker: %s", data)
	}

	// 小数据保持内联，可完整往返
	small := ai.OfUser([]ai.ContentBlock{ai.ImageBlockOfBase64([]byte("tiny"), "image/png")})
	sdata, _ := json.Marshal(small)
	var back ai.Message
	if err := json.Unmarshal(sdata, &back); err != nil {
		t.Fatal(err)
	}
	blocks := contentBlocks(back.Content)
	img, ok := blocks[0].(ai.ImageBlock)
	if !ok || string(img.Data) != "tiny" || img.Mime != "image/png" {
		t.Fatalf("small base64 roundtrip = %#v", blocks[0])
	}
}

func TestPromptCopy(t *testing.T) {
	p := ai.NewPrompt(ai.OfSystem("sys"), ai.OfUser("q"))
	c := p.Copy()
	c.Attrs()["route"] = "x"
	if p.Attrs()["route"] != nil {
		t.Fatal("original prompt attrs mutated by copy")
	}
	if p.LastMessage().Content != "q" {
		t.Fatalf("last message = %#v", p.LastMessage())
	}
	if ai.PromptOf("hi").LastMessage().Role != ai.RoleUser {
		t.Fatal("PromptOf role")
	}
}

func TestAssistantMessageBind(t *testing.T) {
	am := ai.Snapshot(`{"a":1,"b":"x"}`, "", nil)
	type out struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	got, err := ai.Bind[out](am)
	if err != nil || got.A != 1 || got.B != "x" {
		t.Fatalf("Bind = %+v, %v", got, err)
	}
}

func equalBlock(a, b ai.ContentBlock) bool {
	switch x := a.(type) {
	case ai.TextBlock:
		y, ok := b.(ai.TextBlock)
		return ok && x.Text == y.Text
	case ai.ImageBlock:
		y, ok := b.(ai.ImageBlock)
		return ok && x.URL == y.URL && string(x.Data) == string(y.Data) && x.Mime == y.Mime
	case ai.AudioBlock:
		y, ok := b.(ai.AudioBlock)
		return ok && x.URL == y.URL && string(x.Data) == string(y.Data) && x.Mime == y.Mime
	case ai.VideoBlock:
		y, ok := b.(ai.VideoBlock)
		return ok && x.URL == y.URL && string(x.Data) == string(y.Data) && x.Mime == y.Mime
	case ai.BlobBlock:
		y, ok := b.(ai.BlobBlock)
		return ok && x.URL == y.URL && string(x.Data) == string(y.Data) && x.Mime == y.Mime
	case *ai.ResourceLink:
		y, ok := b.(*ai.ResourceLink)
		return ok && x.Name == y.Name && x.URI == y.URI
	}
	return false
}
