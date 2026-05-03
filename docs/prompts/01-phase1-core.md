# Phase 1 实施提示词: 核心框架

## 使用说明

在目标目录中依次执行以下提示词。每个提示词对应一个或多个文件的创建。

---

## Prompt 1.1: 项目初始化 + Context

```
我要用 Go 语言创建一个名为 "aifei" 的轻量级 Web 框架。请帮我完成以下工作：

1. 创建 go.mod 文件:
   module github.com/aifei/aifei
   go 1.26

2. 创建 aifei.go — 框架入口:
   - Version 常量 = "1.0.0"
   - Aifei 结构体，包含: router(*Router), config(*Config), server(*http.Server), plugins([]Plugin), middlewares([]Middleware)
   - New() 创建实例
   - Use(middlewares...Middleware) 添加全局中间件
   - GET/POST/PUT/DELETE/PATCH/Any 路由注册方法
   - Group(prefix string, middlewares...Middleware) 创建路由组
   - Register(prefix string, service interface{}, middlewares...Middleware) struct 自动注册
   - Static(prefix, dir string) 静态文件
   - Run(addr string) 启动并监听信号 (优雅关闭)
   - Start(addr string) error 启动不阻塞
   - Stop() error 优雅关闭
   - ServeHTTP 实现 http.Handler 接口 (路由匹配 → 创建 Context → 执行 handler 链)

3. 创建 context.go — 请求上下文:
   Context 结构体包含: Request(*http.Request), Writer(http.ResponseWriter), pathPara([]string), params(url.Values), form(url.Values), body([]byte), bodyRead(bool), status(int), handlers([]HandlerFunc), index(int)

   请求参数方法 (对应 Java Input 接口):
   - Has(key string) bool
   - GetStr(key string) string / GetStrDefault(key, def string) string
   - GetInt(key string) int / GetIntDefault(key string, def int) int
   - GetInt64(key string) int64 / GetInt64Default(key string, def int64) int64
   - GetFloat64(key string) float64 / GetFloat64Default(key string, def float64) float64
   - GetBool(key string) bool / GetBoolDefault(key string, def bool) bool
   - GetBean(obj interface{}) error — JSON body 绑定到 struct
   - GetMap(key string) map[string]interface{}
   - PathPara(index int) string — 路径参数
   - HasPara(index int) bool
   - Method() string
   - Path() string
   - RemoteIP() string
   - Body() []byte — 懒加载原始请求体
   - GetHeader(key string) string

   响应方法 (对应 Java Output):
   - Status(code int) *Context
   - Header(key, value string)
   - Json(data interface{})
   - JsonOK(data interface{}) — {code:0, msg:"ok", data:...}
   - JsonFail(msg string) — {code:-1, msg:..., data:null}
   - Text(format string, args ...interface{})
   - Html(html string)
   - Redirect(url string)

   链式控制:
   - Next() — 调用下一个 handler (index++ 并执行)
   - Abort() — 终止链 (index = 8999)

注意事项:
- 使用 Go 标准库，零第三方依赖
- Context 参数获取优先级: form > query
- GetBean 使用 encoding/json
- Body 懒加载且只读一次
```

---

## Prompt 1.2: Handler + Middleware

```
在 aifei 框架项目中创建 handler.go:

1. HandlerFunc 类型: type HandlerFunc func(c *Context)

2. Middleware 类型: type Middleware func(next HandlerFunc) HandlerFunc

3. ChainMiddleware(middlewares []Middleware, final HandlerFunc) HandlerFunc:
   - 从最后一个 middleware 开始反向包装
   - 返回最终的 HandlerFunc

4. 内置中间件 (在 middleware.go 中):
   - Logger() Middleware — 记录请求方法、路径、状态码、耗时
   - Recover() Middleware — panic 恢复，返回 500 JSON
   - CORS(origin string) Middleware — 设置 CORS 头，处理 OPTIONS 预检
   - BasicAuth(check func(user, pass string) bool) Middleware — HTTP Basic 认证

注意:
- 所有中间件函数签名统一为 Middleware
- Logger 使用框架的 log 包
- Recover 中记录错误日志
```

---

## Prompt 1.3: Router

```
在 aifei 框架项目中创建 router.go — Radix Tree 路由:

1. Router 结构体:
   - trees map[string]*node — HTTP method → radix tree root
   - 方法: GET, POST, PUT, DELETE, PATCH, Any, Handle(method, path, handlers)
   - Group(prefix, middlewares) *RouterGroup
   - Register(prefix string, service interface{}, middlewares ...Middleware) — struct 自动注册
   - Lookup(method, path string) (handlers []HandlerFunc, params map[string]string, found bool)

2. node 结构体 (Radix Tree 节点):
   - path string — 节点路径
   - children []*node — 子节点
   - handlers []HandlerFunc — 该节点对应的 handlers
   - wildChild bool — 是否有通配子节点
   - isParam bool — 是否为 :param 类型
   - isCatchAll bool — 是否为 *catchall 类型
   - indices string — 子节点首字符索引 (加速查找)

3. 路由支持:
   - 精确匹配: /api/user/list
   - 参数匹配: /api/user/:id (存入 params["id"])
   - 通配匹配: /static/*filepath
   - 不支持正则 (保持简洁)

4. RouterGroup:
   - prefix string
   - middlewares []Middleware
   - router *Router
   - 方法: GET, POST, PUT, DELETE, Group (嵌套)

5. Register 实现 (struct 自动注册):
   - 遍历 struct 的公开方法
   - 方法名转为路径: "List" → "/list", "FindById" → "/find-by-id"
   - 方法名前缀推断 HTTP 方法: Get* → GET, Delete* → DELETE, 其他 → POST
   - 用 reflect 包实现

注意:
- 路由注册时如果有冲突要 panic 提示
- 参数路由和精确路由可以共存: /user/list (精确) 和 /user/:id (参数)
- Radix Tree 实现参考 httprouter 的算法
```

---

## Prompt 1.4: Config + Plugin + Util

```
在 aifei 框架项目中创建以下文件:

1. config.go — 配置:
   Config 结构体: Middlewares([]Middleware), Plugins([]Plugin), OnStart(func()), OnStop(func())
   Option 类型: type Option func(*Aifei)
   选项函数: WithMiddleware, WithPlugin, WithOnStart, WithOnStop

2. plugin.go — 插件接口:
   type Plugin interface {
       Start() error
       Stop() error
   }

3. util.go — 工具函数:
   - IsBlank(s string) bool — TrimSpace 后为空
   - NotBlank(s string) bool
   - DefaultIfBlank(s, def string) string
   - FirstCharToLower(s string) string
   - FirstCharToUpper(s string) string
   - ToCamelCase(s string) string — snake_case → camelCase
   - ToSnakeCase(s string) string — camelCase → snake_case
   - Join(arr []string, sep string) string
   - CamelToPath(name string) string — "FindById" → "find-by-id"

4. prop.go — 配置文件:
   Prop 结构体: data(map[string]string)
   - NewProp() *Prop
   - LoadProp(fileName string) (*Prop, error) — 从文件加载
   - LoadPropIfExists(fileName string) *Prop — 不存在返回空 Prop
   - (p *Prop) Append(other *Prop) *Prop
   - (p *Prop) AppendFile(fileName string) error
   - (p *Prop) Get(key string) string
   - (p *Prop) GetDefault(key, def string) string
   - (p *Prop) GetInt(key string) (int, error)
   - (p *Prop) GetIntDefault(key string, def int) int
   - (p *Prop) GetInt64(key string) (int64, error)
   - (p *Prop) GetInt64Default(key string, def int64) int64
   - (p *Prop) GetBool(key string) (bool, error)
   - (p *Prop) GetBoolDefault(key string, def bool) bool
   - (p *Prop) Contains(key string) bool
   - (p *Prop) IsEmpty() bool
   配置文件格式为 key=value (无 section)

注意事项:
- 所有代码使用 Go 标准库
- Prop 支持注释行 (以 # 开头) 和空行
```

---

## 验证 Prompt

```
验证 aifei 框架 Phase 1 是否完成:

请编写一个 main.go 来测试以下功能:

1. 创建 Aifei 实例
2. 注册全局 Logger 和 Recover 中间件
3. 注册路由:
   - GET /ping → 返回 {"code":0, "msg":"ok", "data":"pong"}
   - GET /api/user/:id → 返回路径参数 id
   - POST /api/user/save → 从 body 获取 JSON 并返回
   - Group /api/v2 带 AuthMiddleware
4. struct 注册: Register("/api/order", &OrderService{})
5. 测试:
   - GET /ping → 200 pong
   - GET /api/user/123 → 200 id=123
   - POST /api/user/save body:{"name":"james"} → 200
   - GET /api/v2/user/list (无 token) → 401
   - GET /api/order/list → 200

确保所有代码可编译运行。
```
