# Aifei-Go Swagger 插件：内嵌 knife4j-vue3 的 OpenAPI 文档中心

> **编译期就把 knife4j-vue3 前端打进二进制**，运行时零外部文件、零前端构建，配合 `swaggo/swag` 注解生成的 OpenAPI spec，一个插件就能为 Aifei-Go 服务挂上完整的 API 文档 UI。

---

## 1. 背景与定位

### 1.1 解决什么问题

Aifei-Go 遵循 [core](core.md) 的 "Just Service" 范式，Service 方法直接作为 HTTP 端点（见 [server](server.md) 的 `Register` 反射注册）。服务一旦多了，前端联调、内部巡检、对外文档都需要一份**可交互的 OpenAPI 文档**。常见做法有两种：

- 把 knife4j/swagger-ui 当独立前端部署，每次改接口要同步维护 spec 与部署；
- 用网关聚合 spec，增加一跳运维成本。

Aifei-Go 的选择是第三条路：**把 knife4j-vue3 编译进应用二进制**（`//go:embed`），运行时自己提供 UI + 分组配置 + spec，开发者只管写 `swaggo/swag` 注解。这样：

- 零部署：`go build` 后单二进制自带文档中心，打开 `{basePath}/doc.html` 即用；
- 零信封污染：UI 的 HTML/JS/CSS/JSON 原样返回，**不**进 aifei 的 `{code,msg,data}` 响应信封；
- 配置驱动：分组、路径、开关都在 `swagger.*` 配置里，无需改代码。

### 1.2 与 Java Aifei / knife4j 的关系

knife4j 原本是 Java 生态的 swagger-ui 增强（knife4j-vue3 是其 Vue3 前端）。Java 侧靠 SpringBoot 自动装配把前端资源包随 jar 发布。本插件把同一份**编译好的 knife4j-vue3 前端产物**（`VITE_RELEASE_APP_TYPE=Knife4jFront` 构建）嵌入 Go 二进制，并用 Go 重新实现了 knife4j-vue3 启动时需要的三条数据：

- `/services.json` —— knife4j-vue3 硬编码向**服务器根**请求的分组列表（对应 Springfox `SwaggerResource` 数组）；
- `{basePath}/v3/api-docs/swagger-config` —— springdoc 风格的分组配置（`urls[]`）；
- `{basePath}/swagger.json` 及 `{basePath}/{path}/swagger.json` —— OpenAPI spec 本体。

### 1.3 依赖

| 依赖 | 角色 |
|------|------|
| [`github.com/swaggo/swag`](https://github.com/swaggo/swag) | 唯一外部依赖。读取由 `swag init` 生成、并经 `docs` 包 `init()` 注册的 OpenAPI 文档 |
| [aifei](core.md) | 提供 `aifei.Plugin` 接口（`Start`/`Stop` 生命周期） |
| [config](config.md) | 从全局 `config.Props` 读 `swagger.*` |
| [log](log.md) | 日志 |

---

## 2. 总体架构

插件的核心是一条"**启动时构建、请求时拦截**"的链：

```
   swag init 生成 docs/        ┐
   应用 import _ ".../docs"    │  编译期：docs 包 init() 注册 spec 到 swag 全局
                               ┘
          ┌────────────────────┘
          ▼
   Plugin.Start()                       运行期：加载 Config
          │
          ├── readDoc = swag.ReadDoc    读出完整 OpenAPI 文档（一次）
          ├── resolveGroups(cfg)        解析为 1..N 个分组（含 filter）
          ├── filterDocByPath(...)      按路径正则切出每个分组的子 spec（缓存）
          ├── 生成 services.json / swagger-config
          └── buildHandler()            组装 http.ServeMux + //go:embed 的 web/ 静态资源
                 │
                 ▼
   p.handler (http.Handler)             Plugin 持有
          │
          ▼
   Plugin.Handler() ── server.WithHTTPHandler(...)
   func(next) http.Handler              包装器：命中 basePath/services.json 走文档，
                                       其余 next.ServeHTTP 交给 aifei 主链
```

请求时只有一次 `strings.HasPrefix` 判断；命中后由启动期就构建好的 `http.ServeMux` 直接写出缓存字节，无运行时解析开销。

关键类型：

| 类型 | 文件 | 职责 |
|------|------|------|
| `Plugin` | `plugin.go` | 实现 `aifei.Plugin`；持有 `cfg`/`handler`；对外暴露 `Handler()` |
| `Config` / `Group` | `config.go` | `swagger.*` 配置模型（单组 legacy + 多组） |
| `swaggerResource` | `handler.go` | knife4j `services.json` 的单条记录（Springfox SwaggerResource） |
| `resolvedGroup` | `handler.go` | `Group` 解析后的运行态（`specURL` + `filter *regexp.Regexp`） |
| `webFS` | `handler.go` | `//go:embed web` 嵌入的 knife4j-vue3 前端 |

---

## 3. 关键 API

### 3.1 Plugin

```go
var _ aifei.Plugin = (*Plugin)(nil)

type Plugin struct {
    cfg     *Config
    logger  log.Logger
    started bool
    handler http.Handler
}

// NewPlugin 创建插件。logger 为 nil 时用 log.Default()。配置在 Start 时读取。
func NewPlugin(logger log.Logger) *Plugin

// Start 读取 swagger.* 配置，构建 knife4j-vue3 handler。
// 当 swagger.enabled=false 时为 no-op。
func (p *Plugin) Start() error

// Stop 标记插件停止。
func (p *Plugin) Stop() error

// Handler 返回 HTTP 中间件：命中文档请求则直接服务，绕过 aifei Input/Output 管线。
func (p *Plugin) Handler() func(http.Handler) http.Handler
```

### 3.2 Config

```go
type Config struct {
    Enabled   bool     `yaml:"enabled"`   // 默认 true
    BasePath  string   `yaml:"basePath"`  // 默认 "/swagger"
    GroupName string   `yaml:"groupName"` // 默认 "API Docs"（仅单组 legacy 模式用）
    Groups    []Group  `yaml:"groups"`    // 多组模式（为空则走单组 legacy）
}

type Group struct {
    Name   string `yaml:"name"`   // knife4j 选择器里的显示名
    Path   string `yaml:"path"`   // spec 挂载 slug：{basePath}/{path}/swagger.json
    Filter string `yaml:"filter"` // 对 spec 路径键的正则过滤（@Router 值，如 ^/oa/admin-api）
}
```

### 3.3 最小可用代码

```go
package main

import (
    _ "yourmod/docs" // swag init 生成的 docs 包，init() 里向 swag 注册 spec

    "github.com/crazy-airhead/aifei-go/aifei"
    "github.com/crazy-airhead/aifei-go/plugins/swagger"
    "github.com/crazy-airhead/aifei-go/server"
)

// @title           My API
// @version         1.0
// @BasePath        /
func main() {
    swagPlugin := swagger.NewPlugin(nil)
    app := aifei.New(aifei.WithPlugin(swagPlugin))
    server.AutoRegisterServices(app)
    server.Run(app, ":8080", server.WithHTTPHandler(swagPlugin.Handler()))
    // 打开 http://localhost:8080/swagger/doc.html
}
```

---

## 4. 核心机制一：内嵌的 knife4j-vue3 前端

`plugins/swagger/web/` 是一份**已编译的 knife4j-vue3 产物**（入口 `doc.html`，静态资源在 `webjars/`、`img/`、`oauth/`）。插件用标准库 `embed.FS` 把它编译进二进制：

```go
//go:embed web
var webFS embed.FS

rootFS, _ := fs.Sub(webFS, "web")        // 去掉一层 web/ 前缀
fileServer := http.FileServer(http.FS(rootFS))
uiFileServer := http.StripPrefix(cfg.BasePath, fileServer)
```

几个对落地很关键的细节：

- **构建配置**：这份前端以 `VITE_RELEASE_APP_TYPE=Knife4jFront` 模式构建，是纯静态产物，**没有 SpringBoot 后端依赖**——Go 侧只需提供它请求的几个 JSON 即可。
- **`/services.json` 必须挂在根**：knife4j-vue3 把请求路径硬编码为服务器根的 `/services.json`，所以插件即使在 `basePath=/swagger` 下，也会把这条路由挂到根（见下文路由表）。
- **零运行时文件依赖**：升级 knife4j 前端只需替换 `web/` 重新 `go build`，部署仍是单二进制。

---

## 5. 核心机制二：services.json 分组配置

knife4j-vue3 启动流程：先请求 `/services.json`（或 `{basePath}/v3/api-docs/swagger-config`），拿到分组清单后再请求每个分组对应的 spec URL。插件支持两种分组模式。

### 5.1 单组（legacy）

不配 `swagger.groups` 时，`resolveGroups` 退化出一个默认分组：名为 `GroupName`，spec 挂在 `{basePath}/swagger.json`，**无 filter**（完整文档原样吐出）。

```yaml
swagger:
  enabled: true
  basePath: /swagger
  groupName: AdminApi
```

### 5.2 多组（springdoc/yudao "GroupedOpenApi" 风格）

配 `swagger.groups` 后，每个 group 暴露**同一份完整 spec 的一个路径切片**：

```yaml
swagger:
  enabled: true
  basePath: /swagger
  groups:
    - name: AdminApi          # knife4j 选择器显示名
      path: admin             # spec 服务于 /swagger/admin/swagger.json
      filter: ^/oa/admin-api  # 路径键正则；只保留匹配的 operation
    - name: AppApi
      path: app
      filter: ^/oa/app-api
```

设计要点：

- **一次 `swag init`、一个 spec**：没有 Java 那种"每包一份 doc"的维护负担。完整文档在 `buildHandler` 启动时读一次，每个分组的子 spec 由 `filterDocByPath` 预先切好并缓存，请求时只做一次 `Write`。
- **正则校验前置**：`LoadConfig` 在启动期就 `regexp.Compile` 每个 `filter`，坏正则直接报错——杜绝"配错正则 → 静默吐全量文档"这类足下之坑。
- **容错降级**：`resolveGroups` 跳过名为空或重名的 group；若所有 group 都非法，退回单组 legacy。
- **filter 为空 = 全量**：某 group 不配 `filter` 即服务完整 spec，与 legacy 行为一致。

`filterDocByPath` 还会顺手把顶层 `tags` 修剪成"被保留 operation 实际引用的子集"，避免 knife4j 选择器里出现指向空 operation 的僵尸分组。

---

## 6. 核心机制三：Handler 中间件与 aifei 信封之外

Aifei-Go 的业务响应统一走 `{code,msg,data}` 信封（由 [server](server.md) 的 `IoHandler` 渲染）。文档中心的 HTML/JSON/CSS/JS 必须原样返回，不能被信封包装。插件用一条**中间件短路**实现：

```go
func (p *Plugin) Handler() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if p.handler != nil && p.matches(r.URL.Path) {
                p.handler.ServeHTTP(w, r)   // 直接服务，绕过 aifei 管线
                return
            }
            next.ServeHTTP(w, r)            // 其余请求交给 aifei 主链
        })
    }
}
```

`matches` 的判定规则与路由表完全对齐：

```go
func (p *Plugin) matches(path string) bool {
    bp := p.cfg.BasePath
    if path == "/services.json" { return true }                 // 根上的 services.json
    if path == bp || strings.HasPrefix(path, bp+"/") { return true } // basePath 及其子树
    return false
}
```

接入方式是 [server](server.md) 的 `WithHTTPHandler`（把一个 `func(http.Handler) http.Handler` 加到 HTTP 中间件链）：

```go
server.Run(app, ":8080", server.WithHTTPHandler(swagPlugin.Handler()))
```

> 为什么用 `WithHTTPHandler` 而不是 `app.Use(...)`：`app.Use` 注册的是 aifei 层的 `Handler` 包装链（作用于 `aifei.Input`/`aifei.Output`），会把响应纳入信封；而文档请求需要抢在 aifei 路由之前就直出原始字节，所以必须挂在 HTTP 层。

### 6.1 完整路由表

`buildHandler` 内部用 `http.NewServeMux` 注册的全部路由（`basePath` 默认 `/swagger`）：

| 路由 | 方法 | 行为 |
|------|------|------|
| `/services.json` | GET | knife4j-vue3 硬编码向根请求的分组清单（`SwaggerResource[]`） |
| `{basePath}/v3/api-docs/swagger-config` | GET | springdoc 风格的 `urls[]` 分组配置 |
| `{basePath}/swagger.json` | GET | 默认/legacy 分组的 spec（无 filter，完整文档） |
| `{basePath}/{path}/swagger.json` | GET | 每个命名分组的 spec（filter 切片） |
| `{basePath}` | GET | 302 重定向到 `{basePath}/doc.html` |
| `{basePath}/` | GET | 302 重定向到 `{basePath}/doc.html` |
| `{basePath}/doc.html` | GET | knife4j-vue3 入口页 |
| `{basePath}/webjars/...` | GET | JS/CSS/img 等静态资源 |

---

## 7. 集成方式：从零接入的完整步骤

### 步骤 1：在主入口写 swag 注解

```go
// @title           My API
// @version         1.0
// @description     Aifei-Go 示例服务的 API 文档
// @BasePath        /
func main() { ... }
```

业务 Service 的每个方法也可以写 `@Summary` / `@Router` / `@Param` / `@Success` 等注解，`swaggo/swag` 会扫描它们。

### 步骤 2：生成 docs 包

```bash
# 安装一次 swag CLI
go install github.com/swaggo/swag/cmd/swag@latest

# 扫描 main.go 所在目录，生成 docs/docs.go
swag init -g main.go -o docs
```

生成的 `docs` 包在 `init()` 里调用 `swag.Register(...)`，把 spec 注册到 `swag` 全局。**这一步是 `swag.ReadDoc()` 能读到文档的前提**。

### 步骤 3：import docs 包并挂插件

```go
package main

import (
    _ "yourmod/docs" // 仅靠 init() 注册副作用

    "github.com/crazy-airhead/aifei-go/aifei"
    "github.com/crazy-airhead/aifei-go/plugins/swagger"
    "github.com/crazy-airhead/aifei-go/server"
)

func main() {
    // 1. (可选) 加载配置：config.Init 会读 swagger.*
    // 2. 创建并挂插件
    swagPlugin := swagger.NewPlugin(nil)
    app := aifei.New(aifei.WithPlugin(swagPlugin))

    // 3. 注册业务 Service（其方法上的 swag 注解即文档来源）
    server.AutoRegisterServices(app)

    // 4. 启动：把 Handler() 接进 HTTP 中间件链
    server.Run(app, ":8080", server.WithHTTPHandler(swagPlugin.Handler()))
}
```

打开 `http://localhost:8080/swagger/doc.html` 即可看到 knife4j-vue3 界面，左上角分组选择器列出 `swagger.groups`（或单组的 `groupName`）。

### 步骤 4：按需配置

```yaml
swagger:
  enabled: true            # 生产环境可关
  basePath: /swagger       # 也可放 /api-docs 之类
  groups:
    - name: AdminApi
      path: admin
      filter: ^/oa/admin-api
    - name: AppApi
      path: app
      filter: ^/oa/app-api
```

`enabled: false` 时 `Start()` 仅记一条日志直接返回——插件对象可以一直挂在 `app.Plugins()` 里，开关完全由配置控制，无需改代码。

---

## 8. 模块结构

```
plugins/swagger/
├── plugin.go      # aifei.Plugin 实现：Start 读配置+构建 handler、Stop、Handler() 中间件
├── config.go      # Config/Group 模型 + LoadConfig（读 swagger.* + 正则校验）
├── handler.go     # buildHandler：//go:embed web + services.json/swagger-config + 路由过滤
├── go.mod         # 依赖 aifei/config/log + swaggo/swag
└── web/           # 编译好的 knife4j-vue3 前端（//go:embed 嵌入）
    ├── doc.html            # 入口页
    ├── favicon.ico
    ├── robots.txt
    ├── webjars/...         # JS/CSS 等
    ├── img/
    └── oauth/
```

源码约 510 行（含注释），无测试文件（前端为外部已编译产物，后端逻辑通过端到端手验）。

---

## 9. 设计要点与注意事项

1. **前端编译期嵌入**：`//go:embed web` 让单二进制自带 UI，部署无额外文件，升级 knife4j 前端只需替换 `web/` 重编。
2. **`/services.json` 必须在根**：knife4j-vue3 硬编码请求根路径，与 `basePath` 无关——这是 `matches()` 和路由表里单独处理根路径的原因。
3. **一个 spec，多份切片**：避免"每包一份 doc"的维护负担；filter 在启动期一次切好并缓存，请求期零解析。
4. **HTTP 层短路，不进信封**：通过 `server.WithHTTPHandler` 接入而非 `app.Use`，文档的 HTML/JSON 原样返回。
5. **fail-loud 配置**：坏正则在 `LoadConfig` 就报错；group 名空或重名被跳过；全部非法时降级为 legacy 单组。
6. **enabled 可配置开关**：插件对象可常驻，生产环境用 `swagger.enabled: false` 关闭即可，无需改动代码。

### 延伸阅读

- [Aifei-Go 核心框架](core.md) —— `aifei.Plugin` 接口与 Handler 链
- [Server 启动层](server.md) —— `WithHTTPHandler`、`Run` 与中间件组装
- [数据隔离插件](data-isolate.md) —— 同样是 `aifei.Plugin` 的参考实现
- [swaggo/swag](https://github.com/swaggo/swag) —— 注解扫描与 spec 生成
- [knife4j](https://doc.xiaominfo.com/) —— knife4j-vue3 前端项目
