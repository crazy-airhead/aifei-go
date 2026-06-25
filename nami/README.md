# nami

> Ported from Java [Solon](https://solon.noear.org/) Nami (`github.com/crazy-airhead/nami-go`, Apache-2.0).
> Integrated into aifei-go as the `nami` module.

一个轻量级的 Go HTTP RPC **客户端**框架，是 aifei-go 服务端框架的客户端配套。

提供以下能力：

- **Channel**（通道）传输抽象（内置基于 `net/http` 的 HTTP 实现）。
- **Encoder / Decoder**（编解码器）序列化抽象（内置基于 `encoding/json` 的 JSON 实现）。
- **Filter**（过滤器）请求拦截链。
- **Upstream / Discovery**（上游 / 服务发现）集成。
- 流式 API 的 `Nami` 客户端、`Builder` 构建器，以及按路径生成客户端的 `ClientFactory`。
- 一个 `util` 包，提供最简单的基于 URL 的请求封装。

零外部依赖，仅使用 Go 标准库。

## 安装

```bash
go get github.com/crazy-airhead/aifei-go/nami
```

## 包一览

| 路径 | 用途 |
|---|---|
| `github.com/crazy-airhead/aifei-go/nami` | 核心框架：Channel、Encoder/Decoder、Filter、Upstream、Config、Nami 客户端 |
| `github.com/crazy-airhead/aifei-go/nami/channel/http` | HTTP 传输通道 |
| `github.com/crazy-airhead/aifei-go/nami/coder/json` | JSON 编解码器 |
| `github.com/crazy-airhead/aifei-go/nami/util` | 最简单的基于 URL 的请求封装 |

## 用 `util` 快速上手

```go
import (
	_ "github.com/crazy-airhead/aifei-go/nami/channel/http" // 注册 HTTP 通道
	_ "github.com/crazy-airhead/aifei-go/nami/coder/json"   // 注册 JSON 编解码器
	"github.com/crazy-airhead/aifei-go/nami/util"
)

util.SetBaseURL("http://api.example.com")
util.SetTimeout(10) // 每个请求的超时（秒，0 = 用通道默认值）

// 相对路径解析到 base URL 上
users, _ := util.GetJSON[[]User]("/users")
result, _ := util.PostJSON[Order]("/orders", newOrder)

// 完整 URL 原样使用，不走 base URL
ip, _ := util.GetJSON[IPInfo]("https://api.ipify.org?format=json")
```

## 核心 `nami` 客户端

```go
import (
	_ "github.com/crazy-airhead/aifei-go/nami/channel/http" // 注册 HTTP 通道
	_ "github.com/crazy-airhead/aifei-go/nami/coder/json"   // 注册 JSON 编解码器
	"github.com/crazy-airhead/aifei-go/nami"
)

n := nami.NewBuilder().
	Timeout(5).
	Upstream(nami.NewUpstreamFixed([]string{"http://localhost:8080"})).
	Name("user-service").
	Path("/api/users").
	Build()

// GET http://localhost:8080/api/users —— URL 由 Upstream + Path 解析得到
var users []User
if err := n.Action(nami.MethodGet).CallAndBind(nil, nil, nil, &users); err != nil {
	return err
}
```

## 许可证

[Apache-2.0](../LICENSE)
