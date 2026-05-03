# Phase 5 实施提示词: 完整示例 + 集成测试

## Prompt 5.1: 完整示例应用

```
创建完整的示例应用 _example/demo/main.go:

使用 SQLite 数据库实现一个用户管理 CRUD 示例，包含:

1. 数据库初始化:
   - db.Init("sqlite", "./demo.db", db.WithPrinter(...))
   - 创建 user 表: id, name, age, email, created_at

2. 中间件:
   - app.Use(aifei.Logger(), aifei.Recover())
   - CORS 中间件允许所有来源

3. RESTful 路由:
   GET    /api/user/list     → 分页查询用户 (支持 name 模糊搜索)
   GET    /api/user/:id      → 根据 ID 查询用户
   POST   /api/user/save     → 新增或更新用户 (有 id 则更新)
   POST   /api/user/delete   → 根据 ID 删除用户
   GET    /api/user/count    → 用户总数

4. Struct 注册路由:
   type UserService struct{}
   func (s *UserService) List(c *Context)
   func (s *UserService) Save(c *Context)
   func (s *UserService) Delete(c *Context)

   app.Register("/api/v2/user", &UserService{})

5. 路由组 + 认证:
   admin := app.Group("/api/admin", BasicAuth(func(u, p string) bool {
       return u == "admin" && p == "123456"
   }))
   admin.GET("/dashboard", func(c *Context) { c.JsonOK("admin dashboard") })

6. 静态文件:
   app.Static("/static", "./webapp")

7. 根路径:
   app.GET("/", func(c *Context) { c.Text("Aifei Go 1.0.0") })

创建 go.mod:
module demo
go 1.26
require github.com/aifei/aifei v0.0.0
replace github.com/aifei/aifei => ../../

确保示例完整可运行。
```

---

## Prompt 5.2: 集成测试

```
为 aifei 框架编写集成测试:

1. 创建 aifei_test.go — 核心框架测试:
   - TestNewAifei — 创建实例
   - TestRouteRegistration — 注册路由并验证 Lookup
   - TestGroupRoute — 路由组测试
   - TestMiddleware — 中间件执行顺序测试
   - TestContextGetParams — 参数获取测试
   - TestContextJson — JSON 响应测试
   - TestContextPathPara — 路径参数测试
   - TestStaticFile — 静态文件测试
   - TestRecoverMiddleware — panic 恢复测试

2. 创建 db/db_test.go — 数据库测试:
   使用 SQLite 内存数据库:
   - TestDBInit — 初始化
   - TestInsert — 插入
   - TestFindById — ID 查询
   - TestFindBy — 条件查询
   - TestUpdate — 更新 (使用 change 集合)
   - TestDelete — 删除
   - TestPaginate — 分页
   - TestCount — 计数
   - TestTransaction — 事务 (正常提交和回滚)
   - TestBatchInsert — 批量插入
   - TestRowActiveRecord — Row 的 Active Record 方法
   - TestRowTypeConvert — Row 的类型转换 getter
   - TestSQLBuilder — 条件构建器
   - TestMultipleDB — 多数据源测试

3. 创建 json/json_test.go — JSON 测试:
   - TestMarshal — 序列化
   - TestUnmarshal — 反序列化
   - TestMarshalString — 字符串序列化
   - TestRowJSON — Row 的 JSON 序列化/反序列化

4. 创建 log/log_test.go — 日志测试:
   - TestDefaultLogger — 默认日志
   - TestLogLevel — 日志级别控制
   - TestCustomLogger — 自定义 logger

运行所有测试: go test ./... -v -cover
```

---

## Prompt 5.3: 最终整理

```
对 aifei 框架项目做最终整理:

1. 确保所有文件包名正确:
   - 根包: package aifei
   - db/: package db
   - json/: package json
   - log/: package log

2. 确保 go.mod 正确:
   module github.com/aifei/aifei
   go 1.26
   零第三方依赖 (仅标准库)

3. 添加 doc.go 到每个包:
   // Package aifei 是一个为 AI Coding 优化的轻量级 Go Web 框架。
   //
   // 核心设计理念:
   //   - Just Service: 扁平化架构，消除 Controller/Service/DAO 分层
   //   - 链式 API: Db + Row 数据库操作模式
   //   - 零依赖: 仅使用 Go 标准库
   //   - AI 友好: 代码量少，结构扁平
   //
   // 快速开始:
   //   app := aifei.New()
   //   app.GET("/hello", func(c *aifei.Context) {
   //       c.JsonOK("Hello, Aifei!")
   //   })
   //   app.Run(":8080")
   package aifei

4. 确保所有导出函数有注释 (Go doc 标准)

5. 运行 go vet ./... 确保无警告

6. 运行 go test ./... -v 确保所有测试通过

7. 统计总代码行数: find . -name "*.go" | xargs wc -l
```
