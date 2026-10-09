// events.go 节点事件（对标 events/Events + NodeEvent）：AI 组件运行前后经
// ctx.EventBus() 广播，载荷 NodeEvent。话题名与 Java 一致，跨语言标注互认。
package aiflow

import "github.com/crazy-airhead/aifei-go/flow"

// 节点事件话题。
const (
	EventFlowNodeStart = "flow.node.start" // 节点开始
	EventFlowNodeEnd   = "flow.node.end"   // 节点结束
)

// NodeEvent 节点事件载荷（发生节点 + 流上下文）。
type NodeEvent struct {
	Ctx  flow.Context
	Node *flow.Node
}
