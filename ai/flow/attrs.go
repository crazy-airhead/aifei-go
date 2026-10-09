// attrs.go 组件属性常量表（对标 Attrs：meta 键 / 上下文键 / 属性名单一来源）。
package aiflow

// 节点 meta 键（IO 约定：输入/输出/附件变量名覆写）。
const (
	MetaInput      = "input"
	MetaOutput     = "output"
	MetaAttachment = "attachment"
)

// 上下文变量键（缺省 IO 约定 + 属性累积前缀）。
const (
	CtxChatSession = "chatSession"
	CtxProperty    = "property"
	CtxMessage     = "message"
	CtxAttachment  = "attachment"
)

// 组件间经上下文属性传递的模型/仓储名单（AiPropertyComponent 约定：
// 上下文键 property_<name> 的列表，首元素即当前值）。
const (
	PropRepository     = "repository"
	PropEmbeddingModel = "embeddingModel"
	PropChatModel      = "chatModel"
	PropImageModel     = "imageModel"
	PropGenerateModel  = "generateModel"
)
