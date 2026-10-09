// repositorys.go 仓储组件（对标 components/repositorys）：P4 RAG 链迁移后
// 实现，当前注册占位（显式报错）保持 YAML 兼容——AbsRepositoryCom 的
// documentSources/splitPipeline meta 与 String/ChatMessage 查询 →
// ofUserAugment、Document/List 入库的分派契约届时照搬。
package aiflow

import (
	"errors"

	"github.com/crazy-airhead/aifei-go/flow"
)

// InMemoryRepository 内存知识库组件（P4：rag 仓储迁移后实现；当前显式报错）。
type InMemoryRepository struct{ ComponentBase }

// NewInMemoryRepository 构建内存知识库组件。
func NewInMemoryRepository() *InMemoryRepository { return &InMemoryRepository{} }

// Name 注册名。
func (c *InMemoryRepository) Name() string { return "InMemoryRepository" }

// Title 同注册名。
func (c *InMemoryRepository) Title() string { return "InMemoryRepository" }

// Description 组件描述。
func (c *InMemoryRepository) Description() string { return "内存知识库组件" }

// Run 节点事件包装。
func (c *InMemoryRepository) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

func (c *InMemoryRepository) doRun(flow.Context, *flow.Node) error {
	return errors.New("aiflow: InMemoryRepository requires P4 (rag repository not yet migrated)")
}

// RedisRepository Redis 知识库组件（P5：cache 通道；当前显式报错）。
type RedisRepository struct{ ComponentBase }

// NewRedisRepository 构建 Redis 知识库组件。
func NewRedisRepository() *RedisRepository { return &RedisRepository{} }

// Name 注册名。
func (c *RedisRepository) Name() string { return "RedisRepository" }

// Title 同注册名。
func (c *RedisRepository) Title() string { return "RedisRepository" }

// Description 组件描述。
func (c *RedisRepository) Description() string { return "Redis 知识库组件" }

// Run 节点事件包装。
func (c *RedisRepository) Run(ctx flow.Context, node *flow.Node) error {
	return nodeEvents(ctx, node, c.doRun)
}

func (c *RedisRepository) doRun(flow.Context, *flow.Node) error {
	return errors.New("aiflow: RedisRepository requires P5 (cache channel not yet migrated)")
}
