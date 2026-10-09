// node_attachment_test.go D8 方案 A：Node 附件缓存（Attachment/SetAttachment）
// —— 运行时键值存取、并发安全、缺省 nil；不参与图序列化。
package flow_test

import (
	"sync"
	"testing"

	flow "github.com/crazy-airhead/aifei-go/flow"
)

// TestNodeAttachment: 同一节点多键独立存取；未写入键返回 nil。
func TestNodeAttachment(t *testing.T) {
	g, err := flow.Create("attach", func(s *flow.GraphSpec) {
		s.AddStart("start").LinkAdd("a1")
		s.AddActivity("a1").Task("x = 1")
	})
	if err != nil {
		t.Fatal(err)
	}
	node := g.GetStart().NextNode()

	if got := node.Attachment("model"); got != nil {
		t.Fatalf("未写入的附件应返回 nil，得到 %v", got)
	}

	node.SetAttachment("model", "gpt-4o")
	node.SetAttachment("client", 42)
	if got := node.Attachment("model"); got != "gpt-4o" {
		t.Fatalf("Attachment(model) = %v", got)
	}
	if got := node.Attachment("client"); got != 42 {
		t.Fatalf("Attachment(client) = %v", got)
	}

	// 覆写生效
	node.SetAttachment("model", "gpt-4.1")
	if got := node.Attachment("model"); got != "gpt-4.1" {
		t.Fatalf("覆写后 Attachment(model) = %v", got)
	}
}

// TestNodeAttachmentConcurrent: 并发读写不竞态（-race 下验证）。
func TestNodeAttachmentConcurrent(t *testing.T) {
	g, err := flow.Create("attach-c", func(s *flow.GraphSpec) {
		s.AddStart("start").LinkAdd("a1")
		s.AddActivity("a1").Task("x = 1")
	})
	if err != nil {
		t.Fatal(err)
	}
	node := g.GetStart().NextNode()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				node.SetAttachment("k", j)
				_ = node.Attachment("k")
			}
		}()
	}
	wg.Wait()
}
