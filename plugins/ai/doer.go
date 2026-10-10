// doer.go 命名 httpx.Doer 注入表：app.yml 的 `doer: <name>` 键按名引用，
// 测试注桩（假 OpenAI）与生产仪表化（trace/metrics 客户端）共用一条缝。
package ai

import (
	"sync"

	"github.com/crazy-airhead/aifei-go/ai/httpx"
)

var (
	doers   = map[string]httpx.Doer{}
	doersMu sync.RWMutex
)

// RegisterDoer 注册具名 Doer（覆盖同名）。
func RegisterDoer(name string, doer httpx.Doer) {
	doersMu.Lock()
	doers[name] = doer
	doersMu.Unlock()
}

// doerOf 查名（未注册返回 nil——装配层报错指名）。
func doerOf(name string) httpx.Doer {
	doersMu.RLock()
	defer doersMu.RUnlock()
	return doers[name]
}
