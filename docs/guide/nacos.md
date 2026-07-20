# Aifei-Go Nacos 插件：注册中心 / 配置中心 / 服务发现三合一

> **一个插件打通 Nacos 的三个角色。**实现 `aifei.Plugin`：Start 时注册临时实例（SDK 自动心跳）、监听 DataID 推送配置变更、把服务发现桥接成 `nami.Upstream`；`init()` 还自动注册 `config.CloudLoader`，让 `config.Init()` 在配置齐备时自动从 Nacos 拉 L5 配置。

---

## 1. 背景与定位

Nacos 在国内微服务生态里同时扮演三个角色：**服务注册中心**（实例上下线）、**配置中心**（运行时配置热更新）、**服务发现**（消费方解析服务名到实例地址）。一个接 Nacos 的应用通常三个都要用，且它们的连接参数（`serverAddr` / `namespace` / `username` / `password`）是同一份。

`plugins/nacos` 把这三件事收敛到**一个插件、一份配置、一对共享的 SDK client**：

- **是什么**：实现 `aifei.Plugin` 的 Nacos 集成，基于官方 `nacos-sdk-go/v2`（`nacos-group/nacos-sdk-go/v2`）
- **解决什么**：让应用 `NewPlugin + WithPlugin` 一行接入 Nacos 的三件套，业务代码用包级 helper / `nami.Upstream` 就行
- **依赖**：内部模块 `aifei` / `config` / `log` / `nami`；外部库 `nacos-sdk-go/v2`
- **不解决什么**：不替代认证（鉴权由应用/网关负责）、不做配置加密（密文/Nacos 加解密插件职责）

> 想了解插件在整体框架中的位置，见 [core.md](core.md)；服务发现桥接的 `nami.Upstream` 详见 [nami.md](nami.md)；`config.CloudLoader` 的分层加载机制详见 [config.md](config.md)。

---

## 2. 三合一架构

插件用一对共享的 SDK client（`INamingClient` + `IConfigClient`）服务三种关注点，避免每个能力各起一条 gRPC 连接：

```
              ┌───────────────────────────┐
              │  nacos-sdk-go/v2          │
              │  INamingClient            │  ← 一对 process-wide 共享 client
              │  IConfigClient            │     按 (server, namespace, user) 缓存
              └─────────────┬─────────────┘
                            │
        ┌───────────────────┼───────────────────┐
        ▼                   ▼                   ▼
   ① 服务注册          ② 配置中心           ③ 服务发现
   registerInstance    startConfigListen    NewNamiUpstream
   (ephemeral)         ListenConfig         → nami.Upstream
   SDK 自动心跳         OnChange → 回调      Discovery.GetServer
        │                   │                   │
        ▼                   ▼                   ▼
   Stop 时注销         推送变更到回调       nami RPC 客户端解析地址
                        (+ BindProps 自动
                         更新 config.Props)

   ───────────────  另：init() 自动注册  ───────────────
   config.RegisterCloudLoader  →  config.Init() 在 L5 自动从 Nacos 拉配置
```

核心类型清单：

| 类型 | 职责 |
|------|------|
| `Config` | 单一配置结构，覆盖连接/配置中心/服务注册三类字段 |
| `Plugin` | `aifei.Plugin` 实现，串起注册、监听、发现 |
| `ConfigChangeCallback` | `func(dataID, group, content string)`，配置变更回调 |
| `nacosDiscovery` | `nami.Discovery` 实现，把 Nacos 实例解析成 URL |

---

## 3. 关键 API

### 3.1 `Config`：一份配置覆盖三件套

```go
type Config struct {
    Enabled     bool     // 总开关；false 时 Start 是 no-op

    ServerAddr  string   // host:port（必填）
    Namespace   string   // 租户/命名空间 id（空 = public）
    Group       string   // 配置 & 服务分组
    DataID      string   // 监听的配置 DataID（空 = 不监听）

    ServiceName string   // 注册的服务名
    ServiceIP   string   // 注册 IP（空 = 自动探测本机 IPv4）
    ServicePort uint64   // 注册端口（0 = 默认 8080）

    Username    string   // Nacos 2.x 鉴权（可选）
    Password    string
}
```

一份 `Config` 同时承载三类字段——这与 Nacos 三角色共用一套连接信息的事实对齐。YAML 字段名见 §7。

### 3.2 `Plugin`

```go
type Plugin struct {
    cfg    *Config
    logger log.Logger
    naming naming_client.INamingClient
    config config_client.IConfigClient

    registered bool
    listening  bool

    // 配置变更回调（启动时会先用初始值调一次）
    ConfigChangeCallback func(dataID, group, content string)
}

func NewPlugin(logger log.Logger) *Plugin
func (p *Plugin) Start() error
func (p *Plugin) Stop() error

// 配置读写
func (p *Plugin) GetConfig(dataID, group string) (string, error)
func (p *Plugin) PublishConfig(dataID, group, content string) error

// 服务发现
func (p *Plugin) NewNamiUpstream(name string) nami.Upstream

// 配置热更新
func (p *Plugin) BindProps(props *config.Props) *Plugin
```

`Start` 是「三件事一次完成」的入口：`LoadConfig` → `SetDefaultConfig` → 连 Nacos → 注册实例 → 监听配置。

---

## 4. 服务注册：临时实例 + SDK 自动心跳

### 4.1 注册参数

```go
func (p *Plugin) registerInstance() error {
    ip := p.instanceIP()
    port := p.instancePort()
    ok, err := p.naming.RegisterInstance(vo.RegisterInstanceParam{
        Ip:          ip,
        Port:        port,
        ServiceName: p.cfg.ServiceName,
        GroupName:   p.cfg.Group,
        Weight:      1.0,
        Enable:      true,
        Healthy:     true,
        Ephemeral:   true,           // ← 关键：临时实例
        ClusterName: "DEFAULT",
    })
    // ...
}
```

**`Ephemeral: true`** 是核心选择：临时实例由**客户端 SDK 内部维护心跳**（SDK 自带心跳循环，无需应用介入），服务端收不到心跳后会自动摘除。这意味着：

- 应用 crash/被 kill -9 → 心跳停 → Nacos 自动摘除 → 消费方发现
- 不需要应用在关停时显式注销（但插件仍会在 `Stop` 里 deregister，加速摘除）

> 持久实例（`Ephemeral: false`）由服务端主动探测，要求应用开放健康检查端口——除非有运维要求，默认 ephemeral 即可。

### 4.2 IP/端口自动探测

| 字段 | 缺省时行为 |
|------|-----------|
| `serviceIp` | 调 `getLocalIP()` 取首个非 loopback IPv4，找不到回退 `127.0.0.1` |
| `servicePort` | `0` → `8080` |

`getLocalIP` 用 `net.InterfaceAddrs()` 遍历，跳过 loopback，取第一个 IPv4——容器/云主机上通常就是注册可达的 IP；多网卡场景需显式设 `serviceIp`。

### 4.3 关停：先取消监听，再注销

```go
func (p *Plugin) Stop() error {
    if p.cfg == nil || !p.cfg.Enabled {
        return nil
    }
    if p.listening {
        if err := p.config.CancelListenConfig(listenParam(p.cfg)); err != nil {
            p.logger.Warn("nacos cancel listen: %v", err)
        }
        p.listening = false
    }
    if p.registered {
        if err := p.deregisterInstance(); err != nil {
            p.logger.Warn("nacos deregister: %v", err)
        } else {
            p.registered = false
        }
    }
    return nil
}
```

注意：**SDK client 本身不关闭**（`nacos-sdk-go` 的 client 没有 `Close` 方法），它们是 process-wide 缓存的（见 §6），随进程退出自然回收。

---

## 5. 配置中心：监听 + 自动 CloudLoader

配置中心有两条独立的路径：**运行时推送**（Plugin 监听）和**启动时拉取**（`config.Init` 自动走 `CloudLoader`）。

### 5.1 运行时监听：`startConfigListen`

```go
func (p *Plugin) startConfigListen() error {
    // 1. 先用当前值调一次回调，让调用方不必等变更才拿到内容
    if content, err := p.GetConfig(dataID, group); err != nil {
        p.logger.Warn("nacos fetch initial config: %v", err)
    } else if content != "" && p.ConfigChangeCallback != nil {
        p.ConfigChangeCallback(dataID, group, content)
    }

    // 2. 注册服务端推送监听
    return p.config.ListenConfig(vo.ConfigParam{
        DataId: dataID,
        Group:  group,
        OnChange: func(namespace, group, dataId, data string) {
            p.logger.Info("nacos config changed: %s/%s", group, dataId)
            if p.ConfigChangeCallback != nil {
                p.ConfigChangeCallback(dataId, group, data)
            }
        },
    })
}
```

两个关键设计：

- **启动即投递一次**：注册监听前先用 `GetConfig` 拉一次当前内容并调回调，避免调用方为了拿初值还得自己再调 `GetConfig`——同一回调拿到全部内容（初值 + 后续变更）
- **`DataID` 为空则不监听**：`Start` 里 `if p.cfg.DataID != ""` 才调 `startConfigListen`；只做注册/发现的场景不会因缺 `DataID` 报错

### 5.2 启动时拉取：`init()` 自动注册 CloudLoader

这是最巧妙的一笔。`config_loader.go` 的 `init()` 在包被导入时自动执行：

```go
func init() {
    config.RegisterCloudLoader(func(props *config.Props) ([]byte, error) {
        serverAddr := props.GetStr("nacos.serverAddr")
        if serverAddr == "" {
            return nil, nil // 未配置，跳过
        }
        dataID := props.GetStr("nacos.dataId")
        if dataID == "" {
            return nil, nil // 无 data_id，跳过
        }

        cfg := &Config{
            ServerAddr: serverAddr,
            Namespace:  props.GetStr("nacos.namespace"),
            Group:      props.GetStr("nacos.group"),
            DataID:     dataID,
            Username:   props.GetStr("nacos.username"),
            Password:   props.GetStr("nacos.password"),
        }
        content, err := FetchConfig(cfg)
        if err != nil {
            return nil, err
        }
        return []byte(content), nil
    })
}
```

这意味着应用只要 `import _ "github.com/crazy-airhead/aifei-go/plugins/nacos"`，`config.Init()` 的 L5 阶段就会**自动**从 Nacos 拉配置——前提是本地 `nacos.serverAddr` + `nacos.dataId` 已配。流程：

```
config.Init(args)
   ├── L1 app.yml + app-{env}.yml
   ├── L2 extension configs (config.include)
   ├── L3 env vars + CLI args         ← 此层提供 nacos.serverAddr / nacos.dataId
   ├── L4 programmatic Load()
   └── L5 CloudLoaders                ← nacos 的 loader 在这里读 L1-L4 的结果
                  │
                  └→ FetchConfig → YAML 字节 → MergeYAML 进全局 props
```

> **先有鸡还是先有蛋**：loader 从 `props`（L1-L4 已合并）里读 `nacos.*` 连接信息，再去 Nacos 拉远程配置——所以 Nacos 连接信息必须放在本地配置（`app.yml` / 环境变量），不能放在 Nacos 自己里。

未配置（`serverAddr` 或 `dataId` 为空）时返回 `nil, nil`——`config.Init` 见到空字节就跳过，不会报错。这让插件可以无条件 import 而不影响未接 Nacos 的环境。

> `config.CloudLoader` 的分层与注册机制详见 [config.md](config.md)。

### 5.3 主动读写配置

不依赖回调，也可主动读写：

```go
// 用插件默认 dataID/group
content, err := p.GetConfig("", "")

// 指定任意 dataID/group
content, err := p.GetConfig("feature-flags", "DEFAULT_GROUP")

// 写回（管理员场景）
err := p.PublishConfig("feature-flags", "DEFAULT_GROUP", yamlContent)
```

`FetchConfig(cfg)` 是无 Plugin 实例的静态函数——应用 bootstrap 早期（Plugin 尚未建）也能用。

---

## 6. SDK client 共享与缓存

### 6.1 按 (server, namespace, user) 缓存

```go
func clientKey(cfg *Config) string {
    return cfg.ServerAddr + "|" + cfg.Namespace + "|" + cfg.Username
}

var (
    clientMu    sync.Mutex
    clientCache = map[string]*clientEntry{}
)

type clientEntry struct {
    naming naming_client.INamingClient
    config config_client.IConfigClient
}
```

`getClients` 用 `clientKey` 做 process-wide 缓存：**同一 (server, namespace, user) 元组复用同一对 client**。因为 `nacos-sdk-go` 的 client 不暴露 `Close`，每次 new 一对会泄漏 gRPC goroutine——缓存是必然选择。

### 6.2 SDK ClientConfig 默认值

```go
func clientConfig(cfg *Config) constant.ClientConfig {
    return constant.ClientConfig{
        NamespaceId:         cfg.Namespace,
        TimeoutMs:           10000,    // 10s
        NotLoadCacheAtStart: true,     // 启动不读本地缓存文件
        LogLevel:            "warn",
        Username:            cfg.Username,
        Password:            cfg.Password,
    }
}
```

- `NotLoadCacheAtStart: true`：避免 SDK 读本地磁盘缓存（缓存与服务端不一致时会让旧配置污染启动）
- `TimeoutMs: 10000`：首次连接/请求 10s 超时，云上较慢的环境也够用
- `LogLevel: "warn"`：SDK 自带日志只打 warn 以上，不污染应用日志

### 6.3 失败不缓存

`getClients` 在 `buildClients` 失败时**不写入缓存**——Nacos 暂时不可用时，下次调用会重试创建（例如 `NewNamiUpstream` 的懒解析在服务恢复后能自愈）。

---

## 7. 服务发现：从 Nacos 到 `nami.Upstream`

### 7.1 三个构造入口

| 函数 | 配置来源 | 适用场景 |
|------|---------|---------|
| `NewNamiUpstream(name)` | 全局 `DefaultConfig()`，懒解析、配置变更时重解析 | 包级 init 时构造、配置后置注入 |
| `NewNamiUpstreamWith(server, ns, group, name)` | 显式参数 | 无 Plugin、参数来自命令行/其他源 |
| `p.NewNamiUpstream(name)` | Plugin 自身的 `cfg` | 用 Plugin 实例且保证配置已就绪 |

### 7.2 `NewNamiUpstream` 的懒解析

```go
func NewNamiUpstream(name string) nami.Upstream {
    var (
        mu           sync.Mutex
        realUpstream nami.Upstream
        lastCfg      *Config
    )
    return func() string {
        cfg := DefaultConfig()
        mu.Lock()
        if cfg != lastCfg {                 // 全局配置变了 → 重建底层 upstream
            if cfg != nil {
                realUpstream = discoveryUpstream(cfg, name)
            } else {
                realUpstream = nil
            }
            lastCfg = cfg
        }
        mu.Unlock()
        if realUpstream == nil {
            return ""                        // 未配置 → 空 URL，RPC fast-fail
        }
        return realUpstream()
    }
}
```

两个关键点：

- **懒解析**：upstream 创建时（可能早在 `package init`）`DefaultConfig()` 还是 `nil`，此时不报错；首次调用时才真正建底层 upstream。这让 `var userUp = nacos.NewNamiUpstream("user-service")` 可以放包级变量
- **配置变更重解析**：通过比对 `lastCfg` 指针，全局 `SetDefaultConfig` 换了配置后，下次调用会自动重建底层 upstream（连到新的 Nacos）

### 7.3 解析失败 fast-fail

```go
func discoveryUpstream(cfg *Config, name string) nami.Upstream {
    nc, err := namingClientFor(cfg)
    if err != nil {
        return func() string { return "" }  // ← 永远返回空
    }
    return nami.NewDiscoveryUpstream(&nacosDiscovery{naming: nc}, cfg.Group, name)
}
```

SDK client 创建失败（Nacos 不可达）时返回一个**永远返回空字符串**的 upstream——调用方的 RPC 会因无地址而 fast-fail，而不是 panic 或无限阻塞。

### 7.4 `nacosDiscovery.GetServer`

```go
func (d *nacosDiscovery) GetServer(group, name string) (string, error) {
    instances, err := d.naming.SelectInstances(vo.SelectInstancesParam{
        ServiceName: name,
        GroupName:   group,
        HealthyOnly: true,           // ← 只取健康实例
    })
    if err != nil {
        return "", fmt.Errorf("nacos discovery: %w", err)
    }
    if len(instances) == 0 {
        return "", fmt.Errorf("nacos discovery: no healthy instances for %s@%s", group, name)
    }
    inst := instances[0]             // ← 简单取第一个（SDK 已按 Weight 加权打散）
    return fmt.Sprintf("http://%s:%d", inst.Ip, inst.Port), nil
}
```

返回 `http://ip:port` 形式的 URL，符合 `nami.Upstream` 的契约（`nami` 把 URL 喂给 channel transport）。`HealthyOnly: true` 自动过滤不健康实例。

> `nami.Upstream` / `nami.Discovery` 的接口契约与 RPC 客户端用法详见 [nami.md](nami.md)。

---

## 8. BindProps：配置热更新零样板

`ConfigChangeCallback` 是通用的——应用得自己写「拿到新 YAML → 合并到运行时配置」的胶水。`BindProps` 把这步内建：

```go
func (p *Plugin) BindProps(props *config.Props) *Plugin {
    prev := p.ConfigChangeCallback
    p.ConfigChangeCallback = func(dataID, group, content string) {
        if content != "" {
            if err := props.LoadYAMLBytes([]byte(content)); err != nil {
                p.logger.Warn("nacos bindprops merge: %v", err)
            }
        }
        if prev != nil {
            prev(dataID, group, content)    // ← 保留既有回调
        }
    }
    return p
}
```

行为：

- **YAML 深合并**：调 `props.LoadYAMLBytes`，新配置深合并进既有 `Props`（覆盖同键、保留新增），而不是整体替换——本地配置/环境变量注入的字段不会丢
- **链式保留**：若 `BindProps` 之前已设过 `ConfigChangeCallback`（如应用想顺便打个日志），`prev` 会被嵌套调用，不丢逻辑
- **返回 `*Plugin`**：支持链式 `nacos.NewPlugin(nil).BindProps(config.Props())`

### 典型用法

```go
func main() {
    // 1. 初始化配置（含从 Nacos 拉 L5 —— init() 注册的 CloudLoader 生效）
    if err := config.Init(os.Args); err != nil {
        log.Fatal(err)
    }

    // 2. 创建插件，绑定全局 Props 实现热更新
    p := nacos.NewPlugin(nil).BindProps(config.Props())

    // 3. 注册插件
    app := aifei.New(aifei.WithPlugin(p))
    server.Run(app, ":8080")
}
```

启动后 Nacos 上改 `DataID` 的内容 → 推送变更 → `OnChange` 触发 → `BindProps` 包的回调把 YAML 合进 `config.Props()` → 全局 `config.GetStr("xxx")` 立即返回新值。

> **线程安全**：`config.Props` 内部用 `sync.RWMutex`，并发读 + 动态写安全。详见 [config.md](config.md)。

---

## 9. 配置

### `Config` 的 YAML 字段

```yaml
nacos:
  enabled: true                    # 总开关；false 时 Start 是 no-op（可保留插件注册但从配置切换）

  # 连接（三角色共用）
  serverAddr: "nacos.example.com:8848"   # 必填；host:port
  namespace: ""                    # 租户/命名空间 id；空 = public
  username: ""                     # Nacos 2.x 鉴权（可选）
  password: ""

  # 配置中心
  group: "DEFAULT_GROUP"           # 配置 & 服务的 group
  dataId: "order-service.yml"      # 监听的 DataID；空 = 不监听（仅注册/发现）

  # 服务注册
  serviceName: "order-service"
  serviceIp: ""                    # 空 = 自动探测本机 IPv4
  servicePort: 8080                # 0 = 默认 8080
```

`LoadConfig` 从全局配置 `nacos.` 前缀读取（`config.GetBool` / `config.GetStr` / `config.GetInt`）：

```go
cfg := &Config{
    Enabled:     config.GetBool("nacos.enabled"),
    ServerAddr:  config.GetStr("nacos.serverAddr"),
    Namespace:   config.GetStr("nacos.namespace"),
    Group:       config.GetStr("nacos.group"),
    DataID:      config.GetStr("nacos.dataId"),
    ServiceName: config.GetStr("nacos.serviceName"),
    ServiceIP:   config.GetStr("nacos.serviceIp"),
    Username:    config.GetStr("nacos.username"),
    Password:    config.GetStr("nacos.password"),
}
if port := config.GetInt("nacos.servicePort"); port > 0 {
    cfg.ServicePort = uint64(port)
}
```

### 启动时拉取的最小配置

只接启动时拉取、不做注册/监听，仅需两个键：`nacos.serverAddr` + `nacos.dataId`（`group` 未设则用 Nacos SDK 默认）。`import _ ".../plugins/nacos"` 后，`config.Init()` 自动在 L5 拉 `dataId` 内容合并进 props。

### 多环境分离

利用 `config` 的 env 变量覆盖（L3）切换环境，无需改 `app.yml`：

```bash
AIFEI_NACOS_SERVERADDR="prod-nacos:8848" AIFEI_NACOS_NAMESPACE="prod-ns" ./order-service
```

---

## 10. 集成方式

### 完整 main（注册 + 配置中心 + 发现 + 热更新）

```go
package main

import (
    "log"
    "os"

    "github.com/crazy-airhead/aifei-go"
    "github.com/crazy-airhead/aifei-go/config"
    "github.com/crazy-airhead/aifei-go/plugins/nacos"
    "github.com/crazy-airhead/aifei-go/server"
)

func main() {
    // 1. 加载配置（nacos.* 齐备时 L5 自动从 Nacos 拉）
    if err := config.Init(os.Args); err != nil {
        log.Fatal(err)
    }

    // 2. 建插件并绑定 Props → Nacos 配置变更自动合并进运行时
    p := nacos.NewPlugin(nil).BindProps(config.Props())

    // 3. 注册插件 —— server.Run 启动时调 p.Start()（注册 + 监听），关停时调 p.Stop()
    app := aifei.New(aifei.WithPlugin(p))
    app.Use(server.Logger(), server.Recover())
    server.AutoRegisterServices(app)
    server.Run(app, ":8080")
}
```

### 消费方：用 nami RPC 调注册到 Nacos 的服务

```go
userClient := nami.NewBuilder().
    Upstream(nacos.NewNamiUpstream("user-service")).
    Name("user-service").Build()
resp, err := userClient.Call(ctx, "/user/get", nami.WithParam("id", "42"))
```

`upstream()` 被调用时，`nacosDiscovery.GetServer` 向 Nacos 查询健康实例，返回 `http://ip:port`，`nami` 的 HTTP channel 据此发请求。实例上下线由 Nacos 自动同步——消费方零感知。

---

## 11. 模块结构

```
plugins/nacos/
├── plugin.go         # aifei.Plugin 实现（Start 注册+监听；Stop 注销+取消监听）+ ConfigChangeCallback
├── config.go         # Config 结构 + LoadConfig + GetConfig/PublishConfig + startConfigListen
├── config_loader.go  # init() 自动注册 config.CloudLoader；BindProps 配置热更新
├── naming.go         # registerInstance / deregisterInstance + getLocalIP 自动探测
├── nami.go           # NewNamiUpstream/NewNamiUpstreamWith/Plugin.NewNamiUpstream + nacosDiscovery
└── client.go         # SDK client 缓存（clientKey/clientCache/getClients）+ ClientConfig/ServerConfig 构建
```

源码约 646 行。依赖 `nacos-sdk-go/v2 v2.3.5`。

---

## 12. 总结

Aifei-Go 的 Nacos 插件围绕几条核心设计原则：

1. **三合一、一份配置**：注册/配置中心/发现共用一个 `Config`、一对 SDK client，与 Nacos 实际复用连接的事实对齐
2. **SDK client 进程级缓存**：按 `(server, namespace, user)` 缓存复用，规避 client 无 `Close` 的 gRPC 泄漏；失败不缓存以便自愈
3. **临时实例 + 自动心跳**：`Ephemeral: true` + SDK 内部心跳，应用异常退出也能被 Nacos 自动摘除，无需应用维关心跳循环
4. **配置中心双路径**：启动时拉取走 `init()` 注册的 `CloudLoader`（L5 自动合并）；运行时变更走 `ListenConfig` + `ConfigChangeCallback`
5. **`BindProps` 零样板热更新**：一行代码把 Nacos 推送的 YAML 深合并进 `config.Props`，全局 `config.GetStr` 立即生效
6. **懒解析 + fast-fail 的发现**：`NewNamiUpstream` 可在包 init 时构造、配置后置注入自愈；Nacos 不可达时 upstream 返回空，RPC fast-fail 而非 panic

这种设计让 Nacos 集成从「三个独立 SDK 调用 + 心跳管理 + 配置同步样板」压缩到「一个 Plugin + 一个 `import _`」，业务代码完全感知不到 Nacos 的存在。

---

### 延伸阅读

- `aifei.Plugin` 生命周期与 `Start`/`Stop` 契约：[core.md](core.md)
- `config.CloudLoader` 的分层加载（L1-L5）与 `Props` 线程安全：[config.md](config.md)
- `nami.Upstream` / `nami.Discovery` 接口与 RPC 客户端用法：[nami.md](nami.md)
- Nacos SDK 官方文档：<https://github.com/nacos-group/nacos-sdk-go>
