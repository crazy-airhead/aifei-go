# Aifei-Go 存储插件：本地与 S3 统一抽象

> **本地文件系统 + S3 兼容后端，按 bucket 路由。**以插件方式（`plugins/storage`）为应用提供文件存储能力，基于 minio-go，应用代码只面向 `Client` 接口与顶层 `storage.Put/Get/...` 助手。

---

## 1. 背景与定位

文件存储是几乎所有业务系统都要面对的能力：用户上传的头像、订单附件、导出报表、日志归档……开发环境用本地磁盘最省事，生产环境用对象存储（AWS S3 / Minio / 阿里云 OSS / 腾讯云 COS）更可靠。问题在于：**两套后端的 API 完全不同**，业务代码一旦直接耦合某一家，切换成本极高。

更头疼的是多 bucket 场景：公开头像桶、私有附件桶、临时导出桶，各自有不同的后端与权限策略。业务代码里到处写 `if bucket == ...` 既丑陋又难维护。

Aifei-Go 的 `plugins/storage` 借鉴 ficus 的 `ficus-starter-storage`，提供一套**统一接口、bucket 作用域、driver 可换**的存储抽象：业务代码只写 `storage.Put(key, media)`，桶选择、driver 切换全部配置驱动。

### 定位

| 维度 | 说明 |
|------|------|
| 是什么 | `plugins/storage`：本地 + S3 兼容的统一文件存储抽象插件 |
| 解决什么 | 多 bucket 路由、driver 切换、统一 `Media` 模型、与 aifei 生命周期绑定 |
| Java 对应 | 移植自 ficu `ficus-starter-storage`（`StorageClientImpl` + `StorageClientUtils` 的职责合并） |
| 依赖 | 内部模块 `aifei` / `config` / `log`；外部库 `minio-go/v7` |
| 测试 | `_test/storage_test`（独立 Go 模块） |

> 不熟悉 aifei 插件机制的读者，可先读 [核心框架](core.md) 的 Plugin 接口一节。

---

## 2. 核心概念与总体架构

插件的核心设计思想是：**一个 `Manager` 管理多个命名 bucket，一个默认 bucket 服务顶层助手函数**。每个 bucket 对应一个 `Client`（local 或 s3），bucket 名在构造 `Client` 时固定，方法签名里只剩 key。

### 关键类型一览

| 类型 | 职责 |
|------|------|
| `Client` | 存储抽象接口（一个 bucket 作用域） |
| `LocalClient` | 文件系统后端（仅用 Go 标准库） |
| `S3Client` | S3 兼容后端（minio-go，覆盖 AWS S3/Minio/OSS/COS） |
| `Media` | 文件内容模型：`io.Reader` + content type + size |
| `PutResult` / `BatchResult` / `KeyError` | 操作结果描述 |
| `Manager` | 多 bucket 门面：按名路由 + 默认 bucket |
| `Plugin` | `aifei.Plugin` 实现：读配置 → 建 Manager → 装包级默认 |
| `Config` / `BucketConfig` | 配置模型 |
| `StorageType` | 后端类型：`local` / `s3` |

### 数据流

```mermaid
flowchart TD
    APP["应用代码"] --> HELP["顶层助手 storage.Put/Get/... ← 作用于默认 bucket<br/>storage.Use('avatar').Put() ← 切换到命名 bucket"]
    HELP -->|"defaultManager (包级)"| MGR["Manager"]
    subgraph BUCKETS["命名 bucket（各自一份 Client）"]
        D1["default"]
        D2["avatar"]
        D3["export"]
    end
    subgraph BACKENDS["driver 决定后端"]
        C1["LocalClient（磁盘）"]
        C2["S3Client（Minio）"]
        C3["S3Client（阿里云OSS）"]
    end
    MGR --> D1
    MGR --> D2
    MGR --> D3
    D1 --> C1
    D2 --> C2
    D3 --> C3
```

关键设计点：

- **bucket 在构造时固定**：`Client` 的方法签名只接收 `key`，不再带 `bucket` 参数——避免每次调用都要指定桶的样板代码。
- **driver 可显式可推断**：`driver: s3` 直接指定；省略时看 `endpoint` 的 scheme——`http(s)://` 推断为 s3，否则 local。
- **默认 bucket 兜底**：顶层 `storage.Put/Get/...` 走默认 bucket；`storage.Use(name)` 切到命名 bucket；未配置任何 bucket 时 `Manager` 自动建一个本地 bucket 兜底。

---

## 3. 关键 API：Client 接口

`Client` 定义在 `plugins/storage/client.go`，整个模块的对外契约：

```go
type Client interface {
    // Exists：对象是否存在；不存在的对象返回 (false, nil)
    Exists(key string) (bool, error)

    // TempURL：生成短期可用的下载 URL；不支持预签名的后端返回 ErrUnsupported
    TempURL(key string, ttl time.Duration) (string, error)

    // Get：取回一个 Media；对象不存在返回 (nil, nil)；调用方须 Close 返回的 Media
    Get(key string) (*Media, error)

    // Put：存入一个 Media，返回 PutResult（含 driver/bucket/key 等信息）
    Put(key string, media *Media) (*PutResult, error)

    // Delete：删除单个对象；删不存在的对象不是错误
    Delete(key string) error

    // DeleteBatch：批量删除；逐 key 失败收集到 BatchResult.Errors（err 为 nil），
    //              只有硬失败才返回 error
    DeleteBatch(keys []string) (*BatchResult, error)
}
```

结果类型：

```go
type PutResult struct {
    Driver string // "local" / "s3"
    Bucket string
    Parent string // local: 绝对父目录；s3: 空
    Key    string
}

type BatchResult struct {
    Partial bool        // 至少一个 key 失败
    Errors  []KeyError  // 逐 key 失败，保持插入序
}

type KeyError struct {
    Key string
    Err error
}
```

### 最小用法

```go
import "github.com/crazy-airhead/aifei-go/plugins/storage"

// 写
media := storage.OfFileName("report.csv", file)
res, err := storage.Put("exports/2026/07/report.csv", media)
// res.Driver == "s3", res.Bucket == "export", res.Key == "exports/.../report.csv"

// 读（务必 Close）
m, err := storage.Get("exports/2026/07/report.csv")
if m != nil {
    defer m.Close()
    b, _ := m.Bytes()
}

// 预签名下载链接（仅 s3 后端支持）
url, err := storage.TempURL("exports/2026/07/report.csv", 15*time.Minute)
// 本地后端返回 ErrUnsupported

// 批量删除
br, err := storage.DeleteBatch([]string{"a", "b", "c"})
if br != nil && br.Partial {
    for _, e := range br.Errors {
        log.Warn("delete %s: %v", e.Key, e.Err)
    }
}
```

### 切换 bucket

```go
// 操作命名 bucket（不走默认）
c := storage.Use("avatar")
c.Put("u/1001.jpg", storage.OfFileName("1001.jpg", f))
```

---

## 4. Media 模型：统一的文件载体

`Media` 是贯穿整个模块的数据单元——Java 版 `Media` 类的 Go 对应。它把「内容流 + 元信息」打包在一起，让调用方无需关心来源是 `[]byte`、`io.Reader` 还是磁盘文件：

```go
type Media struct {
    body        io.Reader
    contentType string
    size        int64 // 0 means unknown
}
```

### 构造方式

| 构造器 | 来源 | size 是否已知 |
|--------|------|--------------|
| `NewMedia(r, ct)` | 任意 `io.Reader` | 未知 |
| `NewMediaWithSize(r, ct, size)` | 任意 `io.Reader` | 已知 |
| `OfBytes(b, ct)` | 字节切片 | 已知 |
| `OfString(s, ct)` | 字符串 | 已知 |
| `OfFileName(name, r)` | `io.Reader`，按扩展名推断 ct | 未知 |
| `OfFileNameWithSize(name, r, size)` | `io.Reader`，按扩展名推断 ct | 已知 |

### 读写方法

| 方法 | 行为 |
|------|------|
| `ContentType() string` | 返回已知 ct；未知为空字符串 |
| `Size() int64` | 返回已知字节数；未知为 0 |
| `Body() io.Reader` | 底层 reader（调用方负责按需 Close） |
| `Bytes() ([]byte, error)` | 一次性读全部（不 Close body） |
| `String() (string, error)` | 一次性读全部为字符串（不 Close body） |
| `Close() error` | 若 body 实现 `io.Closer` 则关闭，否则 no-op |

### Content-Type 推断

`mimeByExt` 用 Go 标准库 `mime.TypeByExtension` 按文件扩展名查表，查不到则回退到 `text/plain; charset=utf-8`：

```go
func mimeByExt(name string) string {
    if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
        return ct
    }
    return defaultContentType
}
```

这是一个**纯 stdlib** 的实现——没有引入额外的 magic-number 探测库。S3 后端在 `Put` 时若 `media.ContentType()` 为空，也会调 `mimeByExt(key)` 再兜一次，最后才用 `defaultContentType`。

---

## 5. 两种后端实现

### 5.1 LocalClient：文件系统后端

`LocalClient` 把对象存在本地磁盘，根目录 + bucket 名组路径，**仅用 Go 标准库**：

```
<root>/<bucket>/<key>
```

| 行为 | 说明 |
|------|------|
| 路径解析 `resolve(key)` | 默认拼 `root/bucket/key`；**绝对路径 key 原样使用**（兼容 Java 老行为） |
| 目录创建 `ensureFile(key)` | `Put` 时自动 `MkdirAll` 父目录，加锁防并发竞争 |
| `Get` | `os.Stat` 判存在与目录、`os.Open` 取流；`mimeByExt` 推断 ct，`info.Size()` 填 size |
| `Put` | `os.OpenFile` 创建/截断文件，`io.Copy` 写入；返回 `PutResult{Driver:"local", Parent:绝对父目录}` |
| `Delete` / `DeleteBatch` | `os.Remove`；批量逐个删，失败收集到 `BatchResult` |
| `TempURL` | **不支持**，返回 `ErrUnsupported` |

`LocalClient` 适合开发、测试、单机部署，或作为 S3 的降级备份。但**没有预签名能力**——需要对外暴露文件时得自己写 HTTP handler。

### 5.2 S3Client：S3 兼容后端

`S3Client` 基于 minio-go v7，一套 API 覆盖所有 S3 兼容服务：

| 服务 | endpoint 示例 |
|------|--------------|
| AWS S3 | `s3.amazonaws.com` |
| Minio | `minio.example.com:9000` 或 `https://minio.example.com` |
| 阿里云 OSS | `oss-cn-hangzhou.aliyuncs.com` |
| 腾讯云 COS | `cos.ap-guangzhou.myqcloud.com` |

| 行为 | 说明 |
|------|------|
| `parseEndpoint` | 同时接受 `host:port` 与 `scheme://host:port`；后者按 scheme 推断 `secure`（https=true） |
| 认证 | `credentials.NewStaticV4(AccessKey, SecretKey, "")`（V4 签名，S3 通用） |
| `ensureBucket` | `sync.Once` 懒初始化：`AutoCreateBucket=true` 时首次操作前检查/创建 bucket |
| `Exists` | `StatObject`；`NoSuchKey`/`NoSuchObject` 视为 (false, nil) |
| `TempURL` | `PresignedGetObject` 生成短期 GET URL（这是 S3 后端独有的核心能力） |
| `Get` | `StatObject` 拿 ct/size → `GetObject` 取流，组装成 `Media` |
| `Put` | ct 三级兜底：`media.ContentType()` → `mimeByExt(key)` → `defaultContentType`；size 未知时传 -1 触发 multipart 流式上传 |
| `Delete` / `DeleteBatch` | 单删 `RemoveObject`；批删 `RemoveObjects` 通道模式，错误从结果通道收集 |

**懒建桶**是个值得注意的细节：`ensureBucket` 用 `sync.Once` 保证只执行一次，且仅当 `AutoCreateBucket=true` 才检查。这意味着：

- 首次操作有轻微延迟（一次 `BucketExists` 往返）
- `initErr` 被记录后，后续所有操作都返回该错误（fail-fast，不会每次重试建桶）

---

## 6. Manager：多 bucket 门面

`Manager` 持有所有命名 bucket 并按名路由，结构与 `cache.Manager` 同构：

```go
type Manager struct {
    clients map[string]Client
    def     Client
    mu      sync.RWMutex
    log     log.Logger
}
```

核心方法：

| 方法 | 行为 |
|------|------|
| `NewManager(cfg, logger)` | 按 `cfg.Buckets` 逐个 `newClient`；`cfg.Default` 指向的设为默认；**无 bucket 配置时自动建一个本地 bucket 兜底** |
| `Default() Client` | 返回默认 bucket 的 client |
| `Bucket(name) Client` | 按名查找；空名等价于 `Default()`；未配置返回 `nil` |
| `Buckets() []string` | 全部 bucket 名 |
| `newClient(name, bc, logger)` | 按 driver/endpoint 选 `NewS3Client` 或 `NewLocalClient`；bucket 名取 `bc.Bucket`，空则用 map key |

默认 bucket 的选择规则：`name == cfg.Default` 命中即设为默认；全程未命中时第一个被遍历到的兜底——因此**强烈建议显式写 `storage.default`** 以保证确定性。

---

## 7. 包级默认与顶层助手

插件把「默认 Manager + 默认 bucket」暴露成包级顶层函数，让大多数调用方无需持有 Manager 引用：

| 顶层助手 | 等价于 |
|---------|--------|
| `storage.Exists(key)` | `DefaultManager().Default().Exists(key)` |
| `storage.TempURL(key, ttl)` | 同上 |
| `storage.Get(key)` | 同上 |
| `storage.Put(key, media)` | 同上 |
| `storage.Delete(key)` | 同上 |
| `storage.DeleteBatch(keys)` | 同上 |
| `storage.Use(bucket) Client` | `DefaultManager().Bucket(bucket)` |
| `storage.SetDefault(mgr)` | 装包级默认（手动装配时用） |
| `storage.DefaultManager() *Manager` | 取已装的默认 Manager |

### 未配置时的优雅失败

与 cache 插件一致，未装 `Plugin`、也没调 `SetDefault` 时，顶层助手不 panic，而是返回 `ErrNoDefault`：

```go
func defaultClient() Client {
    m := DefaultManager()
    if m == nil {
        return errClient{ErrNoDefault}
    }
    c := m.Default()
    if c == nil {
        return errClient{ErrNoDefault}
    }
    return c
}
```

`errClient` 实现完整 `Client` 接口，所有方法返回 `ErrNoDefault`——「未配置」表现为普通错误分支而非崩溃。

---

## 8. 配置与集成

### 配置模型

```go
type Config struct {
    Default string                  `yaml:"default"`
    Buckets map[string]BucketConfig `yaml:"buckets"`
}

type BucketConfig struct {
    Driver           string `yaml:"driver"`           // local / s3；空则按 endpoint 推断
    Endpoint         string `yaml:"endpoint"`         // local: 根目录；s3: 服务 URL
    Region           string `yaml:"region"`           // s3 区域
    RegionID         string `yaml:"regionId"`         // Java 兼容别名（Region 空时回退）
    AccessKey        string `yaml:"accessKey"`        // s3 认证
    SecretKey        string `yaml:"secretKey"`        // s3 认证
    Bucket           string `yaml:"bucket"`           // 覆盖桶名；空则用 map key
    AutoCreateBucket bool   `yaml:"autoCreateBucket"` // s3 首次使用时建桶
}
```

`LoadConfig(prefix)` 从全局 `config.Props` 读取：`<prefix>.default` 拿默认名，`<prefix>.buckets` 子树用 `config.SubBind` 整段 YAML 反序列化。`prefix` 空 → `"storage"`。关于 `SubBind` 的细节见 [配置模块](config.md)。

### driver 推断规则

`storageTypeOf(driver, endpoint)` 的优先级：

| `driver` | `endpoint` | 结果 |
|----------|-----------|------|
| `s3` | — | `s3` |
| `local` | — | `local` |
| 空 / 未知 | `http://...` 或 `https://...` | `s3` |
| 空 / 未知 | 其他 / 空 | `local` |

显式 driver 永远赢；省略时看 endpoint 的 scheme。这种设计让本地与云端切换**只改一行 endpoint**。

### Region 的 Java 兼容别名

```go
func (b BucketConfig) resolvedRegion() string {
    if b.Region != "" {
        return b.Region
    }
    return b.RegionID
}
```

`RegionID` 是为了对齐 Java ficus 配置（那边用 `regionId`）。Go 侧推荐用 `region`，两者同时写时 `Region` 优先。

### 完整 YAML 示例

```yaml
storage:
  default: avatar

  buckets:
    # ① 本地文件系统（开发/单机）
    local:
      driver: local
      endpoint: /var/data/files       # 根目录；bucket 名默认取 map key "local"

    # ② AWS S3
    avatar:
      driver: s3
      endpoint: https://s3.us-east-1.amazonaws.com
      region: us-east-1
      accessKey: AKIAIOSFODNN7EXAMPLE
      secretKey: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
      bucket: my-avatar-prod          # 覆盖桶名（不写则用 map key "avatar"）
      autoCreateBucket: false

    # ③ Minio（自建，http + autoCreate）
    export:
      endpoint: minio.internal:9000   # 无 scheme，但 driver 可推断；这里靠 driver 显式
      driver: s3
      regionId: us-east-1             # Java 风格别名
      accessKey: minioadmin
      secretKey: minioadmin
      autoCreateBucket: true          # 首次使用自动建桶

    # ④ driver 省略，靠 endpoint scheme 推断为 s3
    backup:
      endpoint: https://oss-cn-hangzhou.aliyuncs.com
      region: cn-hangzhou
      accessKey: LTAI5t...
      secretKey: XXX
```

### 配置键速查

| 键 | 类型 | 说明 |
|----|------|------|
| `storage.default` | string | 默认 bucket 名（建议显式设置） |
| `storage.buckets.<name>.driver` | string | `local`/`s3`；空则按 endpoint scheme 推断 |
| `storage.buckets.<name>.endpoint` | string | local: 根目录；s3: 服务 URL（`host:port` 或 `scheme://host:port`） |
| `storage.buckets.<name>.region` | string | s3 区域（推荐） |
| `storage.buckets.<name>.regionId` | string | 同上的 Java 兼容别名 |
| `storage.buckets.<name>.accessKey` | string | s3 访问密钥 |
| `storage.buckets.<name>.secretKey` | string | s3 密钥 |
| `storage.buckets.<name>.bucket` | string | 覆盖桶名；空则用 map key `<name>` |
| `storage.buckets.<name>.autoCreateBucket` | bool | s3 首次使用时建桶（仅 s3 生效） |

### 代码集成

```go
func main() {
    // 1. 加载配置（app.yml + 环境变量）
    if err := config.Init(os.Args); err != nil {
        log.Fatal(err)
    }

    // 2. 创建插件（nil logger → log.Default()；prefix 空 → "storage"）
    p, err := storage.NewPlugin(nil)
    if err != nil {
        log.Fatal(err)
    }

    // 3. 注册为 aifei 插件
    app := aifei.New(aifei.WithPlugin(p))
    app.Use(server.Logger(), server.Recover())

    server.AutoRegisterServices(app)
    server.Run(app, ":8080")   // Run 会调 plugin.Start()/Stop()
}
```

`Plugin.Start()` 依次：`LoadConfig` → `NewManager` → `SetDefault` → 日志输出 bucket 列表。`Plugin.Stop()` 是 **no-op**——存储 client 无需显式释放连接（这点与 cache 插件不同，cache 可能跑后台刷新 goroutine）。

### 不用框架时：直接装配 Manager

```go
cfg, _ := storage.LoadConfig("storage")
mgr, _ := storage.NewManager(cfg, nil)
storage.SetDefault(mgr)

storage.Put("a/b.txt", storage.OfFileName("b.txt", f))
```

---

## 9. 能力矩阵：两种后端对照

| 能力 | `LocalClient` | `S3Client` |
|------|--------------|-----------|
| `Exists` | `os.Stat` | `StatObject` |
| `Get` | `os.Open` | `GetObject`（流式） |
| `Put` | `os.OpenFile` + `io.Copy` | `PutObject`（size 未知触发 multipart） |
| `Delete` | `os.Remove` | `RemoveObject` |
| `DeleteBatch` | 逐个 `os.Remove` | `RemoveObjects`（通道批删） |
| `TempURL` | **不支持**（`ErrUnsupported`） | `PresignedGetObject`（核心差异化能力） |
| 依赖 | 仅 Go stdlib | minio-go v7 |
| Content-Type | `mimeByExt`（按扩展名） | `StatObject` 返回；`Put` 时三级兜底 |
| 建桶 | 启动即 `MkdirAll` | `sync.Once` 懒建（`AutoCreateBucket=true`） |
| 绝对路径 key | 原样使用（兼容 Java） | 不适用 |

`TempURL` 是选 S3 后端最重要的理由：业务可以拿到一个 15 分钟有效的下载链接直接给前端，而无需自己的服务做代理转发——这对大文件下载尤其关键。

---

## 10. 插件结构总览

```
plugins/storage/
├── client.go     # Client 接口 + PutResult/BatchResult/KeyError + ErrUnsupported
├── config.go     # Config/BucketConfig + resolvedRegion + LoadConfig
├── local.go      # LocalClient：文件系统后端（resolve/ensureFile/Get/Put/Delete/DeleteBatch）
├── s3.go         # S3Client：minio-go 后端（parseEndpoint/ensureBucket/isNotFound/...）
├── media.go      # Media 模型 + NewMedia*/Of* 构造器 + mimeByExt
├── manager.go    # Manager：多 bucket 门面（NewManager/Default/Bucket/Buckets/newClient）
├── storage.go    # 包级默认：SetDefault/DefaultManager/Use + 顶层 Put/Get/... 助手 + errClient
├── plugin.go     # aifei.Plugin 实现（Start 装默认、Stop no-op）
└── type.go       # 常量与枚举：StorageType / storageTypeOf / defaultBucketName
```

源代码约 869 行。测试在 `_test/storage_test`（独立 Go 模块），基于 minio-go。

---

## 11. 总结

Aifei-Go 的存储插件围绕几个核心设计原则构建：

1. **接口先行**：`Client` 接口屏蔽 local/s3 差异，业务代码只面向接口；切后端只改配置
2. **bucket 作用域**：桶名在构造时固定，方法签名只剩 key，调用清爽不啰嗦
3. **driver 可推断**：显式 `driver` 永远赢；省略时看 `endpoint` scheme，本地↔云端切换一行配置
4. **统一 `Media`**：`io.Reader` + ct + size 的轻量模型，覆盖 bytes/string/file 各类来源；stdlib `mime` 推断，无额外依赖
5. **包级默认 + 助手函数**：大多数场景 `storage.Put/Get` 直达，无需传 Manager；未配置时优雅返回 `ErrNoDefault` 而非 panic
6. **能力差异化诚实暴露**：`TempURL` 在 local 后端返回 `ErrUnsupported` 而非假装支持；调用方按需选择后端

### 延伸阅读

- [核心框架 · Plugin 接口](core.md)
- [分层配置 · SubBind](config.md)
- [缓存插件 · 同构设计](cache.md)
- [数据隔离插件 · 风格范例](data-isolate.md)
