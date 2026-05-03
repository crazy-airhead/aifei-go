# Phase 4 实施提示词: 高级特性

## Prompt 4.1: 完善内置 Middleware

```
在 aifei 框架中完善内置中间件 (middleware.go):

1. Logger() Middleware:
   - 记录: 方法、路径、状态码、耗时
   - 格式: [AIFEI] GET /api/user 200 1.23ms
   - 在 next(c) 前后分别记录开始时间和计算耗时
   - 使用 log.Info 输出

2. Recover() Middleware:
   - 使用 defer + recover 捕获 panic
   - 记录错误日志: log.Error("panic recovered: %v\n%s", err, stack)
   - 使用 runtime.Stack 获取调用栈
   - 返回 500 JSON: {"code":500, "msg":"Internal Server Error"}

3. CORS(origin string) Middleware:
   - 设置头: Access-Control-Allow-Origin, Methods, Headers, Max-Age(86400)
   - OPTIONS 请求返回 204
   - 支持 Credentials

4. BasicAuth(check func(user, pass string) bool) Middleware:
   - 从 Request.BasicAuth() 获取用户名密码
   - 验证失败返回 401 + WWW-Authenticate 头

5. Timeout(duration time.Duration) Middleware:
   - 使用 context.WithTimeout 设置超时
   - 超时返回 504 Gateway Timeout

6. RequestID() Middleware:
   - 生成 UUID 作为 X-Request-ID
   - 同时写入请求头和响应头
   - UUID 使用 crypto/rand 生成 (无需第三方库)
```

---

## Prompt 4.2: Struct 注册路由增强

```
在 aifei 框架中增强 Router.Register 方法:

1. 支持自定义 HTTP 方法映射:
   通过可选的 interface 约定方法名前缀:
   - Get* / List* → GET
   - Post* / Save* / Create* → POST
   - Put* / Update* → PUT
   - Delete* / Remove* → DELETE

2. 支持路径参数:
   方法名中的 "ById" 后缀映射为路径参数 ":id"
   例: GetById → GET /:id, DeleteById → DELETE /:id

3. 支持忽略方法:
   以小写开头的方法自动忽略 (Go 的可见性规则天然支持)

4. Register 实现逻辑:
   func (r *Router) Register(prefix string, service interface{}, middlewares ...Middleware) {
       t := reflect.TypeOf(service)
       v := reflect.ValueOf(service)
       prefix = strings.TrimRight(prefix, "/")

       for i := 0; i < t.NumMethod(); i++ {
           method := t.Method(i)
           name := method.Name

           // 确定 HTTP 方法
           httpMethod := "POST"  // 默认
           pathSuffix := camelToPath(name)

           switch {
           case strings.HasPrefix(name, "Get"):
               httpMethod = "GET"
               pathSuffix = camelToPath(name[3:])  // 去掉 Get 前缀
           case strings.HasPrefix(name, "List"):
               httpMethod = "GET"
           case strings.HasPrefix(name, "Delete"):
               httpMethod = "DELETE"
               pathSuffix = camelToPath(name[6:])
           case strings.HasPrefix(name, "Update") || strings.HasPrefix(name, "Put"):
               httpMethod = "PUT"
               pathSuffix = camelToPath(name[6:])  // Update 去掉前缀
           }

           // "ById" → "/:id"
           pathSuffix = strings.Replace(pathSuffix, "-by-id", "/:id", 1)

           if pathSuffix == "" { pathSuffix = "" }  // 空路径即 index
           path := prefix
           if pathSuffix != "" { path = prefix + "/" + pathSuffix }

           // 创建 handler
           m := middlewares  // capture
           handler := func(c *Context) {
               v.MethodByName(method.Name).Call([]reflect.Value{reflect.ValueOf(c)})
           }
           // 应用中间件
           for i := len(m) - 1; i >= 0; i-- { handler = m[i](handler) }

           r.Handle(httpMethod, path, handler)
       }
   }
```

---

## Prompt 4.3: SQL 条件构建器完善

```
在 aifei 框架的 db/ 子包中完善 SQL 条件构建器:

1. SQLBuilder 增强:

   新增方法:
   - GroupBy(group string) *SQLBuilder — GROUP BY
   - Having(condition string, args ...interface{}) *SQLBuilder — HAVING
   - Join(join string) *SQLBuilder — INNER JOIN
   - LeftJoin(join string) *SQLBuilder — LEFT JOIN
   - RightJoin(join string) *SQLBuilder — RIGHT JOIN

   Build() 完善:
   - 拼接顺序: SELECT + FROM + JOIN + WHERE + GROUP BY + HAVING + ORDER BY + LIMIT + OFFSET
   - WHERE 部分使用 "WHERE" + strings.Join(parts, " AND ")
   - 如果没有 WHERE parts，不输出 WHERE

2. whereOrField 智能判断 (用于 FindBy, DeleteBy, CountBy 等):
   // 判断逻辑:
   // 1. 包含空格 → 当作完整 where 条件: "age > ? and name = ?"
   // 2. 不包含空格 → 当作字段名: "name" → "name = ?"
   func buildWhereOrField(table, whereOrField string, args []interface{}) (string, []interface{}) {
       if strings.Contains(whereOrField, " ") {
           return "SELECT * FROM " + table + " WHERE " + whereOrField, args
       }
       return "SELECT * FROM " + table + " WHERE " + whereOrField + " = ?", append([]interface{}{args[0]}, args[1:]...)
   }

3. Paginate 的 COUNT 查询优化:
   - 从原始 SQL 中提取 FROM 及之后的部分
   - 去掉 ORDER BY (对 COUNT 无意义)
   - 生成: SELECT COUNT(*) FROM ... (去掉 ORDER BY)
   - 使用正则或字符串操作实现 (不引入第三方库)
```

---

## 验证 Prompt

```
验证 Phase 4:

1. 测试 Logger 中间件:
   - 启动服务，发送请求，验证日志输出格式

2. 测试 Recover 中间件:
   - 注册一个会 panic 的 handler
   - 请求应返回 500 而不是崩溃

3. 测试 CORS:
   - 发送 OPTIONS 预检请求 → 204
   - 验证响应头包含 CORS 相关字段

4. 测试 Struct 注册:
   type OrderService struct{}
   func (s *OrderService) List(c *Context) { c.Text("list") }
   func (s *OrderService) GetById(c *Context) { c.Text("get by id") }
   func (s *OrderService) Save(c *Context) { c.Text("save") }
   func (s *OrderService) DeleteById(c *Context) { c.Text("delete") }
   func (s *OrderService) UpdateName(c *Context) { c.Text("update") }

   app.Register("/api/order", &OrderService{})
   验证路由:
   - GET /api/order/list → 200
   - GET /api/order/:id → 200
   - POST /api/order/save → 200
   - DELETE /api/order/:id → 200
   - PUT /api/order/update-name → 200

5. 测试 SQLBuilder 增强:
   db.Select("*").From("user").Join("order on user.id = order.user_id").Where("age > ?", 18).GroupBy("user.id").Find()
```
