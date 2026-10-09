// register.go 注册入口（对标 AiFlowPlugin beanScan）：RegisterDefaults 把
// 12 个内置组件装进容器；RegisterToolProvider 是 Java toolProviders 类名
// 反射实例化的注册式对应物（Go 无类反射，按名注册）。
package aiflow

import (
	"sync"

	ai "github.com/crazy-airhead/aifei-go/ai"
	"github.com/crazy-airhead/aifei-go/flow"
)

// RegisterDefaults 把 12 个内置组件注册进容器（返回容器便于链式构建
// driver：flow.NewSimpleDriver(flow.WithContainer(aiflow.RegisterDefaults(flow.NewMapContainer())))）。
func RegisterDefaults(c *flow.MapContainer) *flow.MapContainer {
	for _, comp := range []Component{
		&VarInput{}, &VarCopy{}, &ConsoleInput{}, &WebInput{},
		&VarOutput{}, &ConsoleOutput{}, &WebOutput{},
		&ChatModel{}, &EmbeddingModel{}, &GenerateModel{},
		&InMemoryRepository{}, &RedisRepository{},
	} {
		c.PutComponent(comp.Name(), comp)
	}
	return c
}

// ---- 工具提供者注册表 ----

var (
	toolProviderMu sync.RWMutex
	toolProviders  = map[string][]ai.FunctionTool{}
)

// RegisterToolProvider 注册具名工具提供者（ChatModel 节点 meta
// toolProviders 按名取用）。同一名字重复注册后者覆盖。
func RegisterToolProvider(name string, tools ...ai.FunctionTool) {
	toolProviderMu.Lock()
	defer toolProviderMu.Unlock()
	toolProviders[name] = tools
}

// ToolProvider 取具名工具提供者的一组工具（未注册返回 nil）。
func ToolProvider(name string) []ai.FunctionTool {
	toolProviderMu.RLock()
	defer toolProviderMu.RUnlock()
	return append([]ai.FunctionTool(nil), toolProviders[name]...)
}
