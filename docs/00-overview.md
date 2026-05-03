# Aifei Go - 实施方案总览

> 将 Java 版 Aifei 框架 (v1.0.0) 改写为 Go 语言版本

## 一、项目概况

### Java 版核心特征

| 特征 | 说明 |
|------|------|
| 核心代码量 | ~3333 行，零第三方依赖 |
| Java 版本 | Java 8+ |
| 架构风格 | "Just Service" — 扁平化，消除 Controller/Service/DAO 分层 |
| 请求处理 | HIO 模式 (Handler\<Input, Output\>) + 责任链 |
| 数据库 | Db + Row 模式，链式 API，Enjoy SQL 模板，多数据库方言 |
| 模块数 | 8 个 Maven 子模块 |

### Java 模块清单

| Java 模块 | 职责 | Go 处理方式 |
|-----------|------|------------|
| aifei (core) | 核心框架: Handler, Input, Output, Config, Router, AOP | 保留 → Go 核心包 |
| aifei-db | 数据库访问: Db, Row, Dao, Page, Dialect, Enjoy SQL | 保留 → Go db 子包 |
| aifei-json | JSON 处理: Json, JsonKit, FastJSON2 集成 | 保留 → Go json 子包 |
| aifei-log | 日志: SLF4J/Log4j2 抽象 | 保留 → Go log 子包 |
| aifei-proxy | AOP 动态代理: CGLIB/Javassist | **废弃** → Go 用 Middleware 替代 |
| aifei-enjoy | 模板引擎: Lexer/Parser/Directive/Expression (特色) | **保留** → Go enjoy 子包 |
| aifei-undertow | HTTP 服务器: Undertow 集成 | **废弃** → Go 内置 net/http |
| aifei-all | 聚合模块 | **废弃** → Go import 按需引入 |

### 废弃模块的替代方案

| 废弃模块 | 原因 | Go 替代 |
|----------|------|---------|
| aifei-proxy | Go 无 JVM 动态代理机制 | Middleware 函数链 |
| aifei-undertow | Go 有 net/http | 直接使用 net/http |
| aifei-all | Go 的 import 机制天然按需引入 | 不需要 |

---

## 二、Go 版目标结构

```
aifei-go/
├── go.mod                        # module: github.com/aifei/aifei
├── go.sum
├── aifei.go                      # 核心入口: Start(), Stop()
├── context.go                    # Context (合并 Java Input + Output)
├── handler.go                    # Handler 接口 + Middleware 链
├── router.go                     # 路由系统 (Radix Tree)
├── action.go                     # Action 定义
├── config.go                     # 配置系统: Config, Settings
├── plugin.go                     # Plugin 接口
├── argument.go                   # 参数解析与注入
├── server.go                     # Server 接口 + net/http 实现
├── dispatcher.go                 # 请求调度
├── util.go                       # 工具函数 (StrUtil, Prop, PathUtil)
│
├── enjoy/                        # *** Enjoy 模板引擎 (特色模块) ***
│   ├── engine.go                 # Engine 入口 + 模板缓存
│   ├── engine_config.go          # 引擎配置 (指令注册、共享函数)
│   ├── template.go               # Template 编译执行
│   ├── env.go                    # 模板执行环境
│   ├── directive.go              # Directive 基类
│   ├── scope.go                  # Scope 变量作用域
│   ├── ctrl.go                   # 执行控制 (break/continue/return)
│   │
│   ├── stat/                     # 语句层
│   │   ├── lexer.go              # 模板词法分析器 (DKFF 算法)
│   │   ├── parser.go             # 模板语法分析器 (DLRD 递归下降)
│   │   ├── token.go              # Token 定义
│   │   ├── symbol.go             # Symbol 定义
│   │   ├── location.go           # 位置信息
│   │   ├── ast.go                # Stat 抽象语句基类
│   │   ├── stat_list.go          # StatList 语句列表
│   │   ├── text.go               # Text 纯文本输出
│   │   ├── output.go             # Output 表达式输出 #()
│   │   ├── if.go                 # If/ElseIf/Else
│   │   ├── for.go                # For 循环 (#for item : list)
│   │   ├── set.go                # Set/SetLocal/SetGlobal 变量赋值
│   │   ├── define.go             # Define 模板函数定义
│   │   ├── include.go            # Include 模板包含
│   │   ├── call.go               # Call 模板函数调用
│   │   ├── switch.go             # Switch/Case/Default
│   │   ├── break_continue.go     # Break/Continue
│   │   └── return.go             # Return
│   │
│   ├── expr/                     # 表达式层
│   │   ├── expr_lexer.go         # 表达式词法分析器
│   │   ├── expr_parser.go        # 表达式语法分析器 (运算符优先级)
│   │   ├── ast.go                # Expr 抽象表达式基类
│   │   ├── id.go                 # Id 变量标识符
│   │   ├── const.go              # Const 常量 (string/number/bool/null)
│   │   ├── arith.go              # 算术运算 (+, -, *, /, %)
│   │   ├── compare.go            # 比较运算 (==, !=, <, <=, >, >=)
│   │   ├── logic.go              # 逻辑运算 (&&, ||, !)
│   │   ├── ternary.go            # 三元表达式 (cond ? a : b)
│   │   ├── null_safe.go          # 空安全操作符 (??, ?.)
│   │   ├── field.go              # 字段访问 (obj.field)
│   │   ├── method.go             # 方法调用 (obj.method())
│   │   ├── index.go              # 索引访问 (arr[i])
│   │   ├── assign.go             # 赋值表达式
│   │   ├── array.go              # 数组字面量
│   │   ├── map.go                # Map 字面量
│   │   ├── range.go              # 范围数组 [start..end]
│   │   └── shared_method.go      # 共享方法调用
│   │
│   ├── io/                       # 输出层
│   │   ├── writer.go             # Writer 接口
│   │   └── string_writer.go      # FastStringWriter
│   │
│   └── source/                   # 模板源加载
│       ├── source.go             # Source 接口
│       ├── file_source.go        # 文件系统源
│       └── string_source.go      # 字符串源
│
├── db/
│   ├── db.go                     # Db 入口 (链式 API)
│   ├── dao.go                    # Dao 数据访问对象
│   ├── row.go                    # Row 数据行 (Active Record)
│   ├── page.go                   # 分页结果
│   ├── batch.go                  # 批量操作
│   ├── operator.go               # SQL 操作符枚举
│   ├── condition.go              # 条件构建 (#where, #and 使用)
│   ├── dialect.go                # 数据库方言接口
│   ├── dialect_mysql.go          # MySQL 方言
│   ├── dialect_postgres.go       # PostgreSQL 方言
│   ├── dialect_sqlite.go         # SQLite 方言
│   ├── config.go                 # 数据库配置
│   ├── type_converter.go         # 类型转换器
│   ├── transaction.go            # 事务管理
│   │
│   └── sql/                      # Enjoy SQL (基于 enjoy 引擎)
│       ├── sql_kit.go            # SqlKit — Enjoy SQL 引擎封装
│       ├── sql_para.go           # SqlPara — SQL + 参数容器
│       ├── sql_directive.go      # #sql 指令 — 定义 SQL 片段
│       ├── para_directive.go     # #para 指令 — 参数占位 (支持 like/in)
│       ├── where_directive.go    # #where 指令 — 动态 WHERE 条件
│       ├── and_directive.go      # #and 指令 — 动态 AND 条件
│       └── orderby_directive.go  # #orderBy 指令 — 动态排序 (白名单防注入)
│
├── json/
│   └── json.go                   # JSON 工具封装
│
├── log/
│   └── log.go                    # 日志接口 + 默认实现
│
└── _example/
    └── demo/
        └── main.go               # 示例应用
```

---

## 三、实施阶段总览

| 阶段 | 文档 | 内容 | 预估代码量 |
|------|------|------|-----------|
| P1 | `01-phase1-core.md` | 核心框架 (Context, Router, Handler, Server, Middleware) | ~800 行 |
| P2 | `02-phase2-enjoy.md` | **Enjoy 模板引擎** (Lexer, Parser, Expr, Directive, Scope) | ~2500 行 |
| P3 | `03-phase3-db.md` | 数据库模块 (Db, Row, Dao, Page, Dialect, Transaction, **Enjoy SQL**) | ~1500 行 |
| P4 | `04-phase4-utils.md` | JSON/日志/工具模块 | ~300 行 |
| P5 | `05-phase5-advanced.md` | 高级特性 (参数注入、批量操作、Middleware 完善) | ~400 行 |
| P6 | `06-phase6-example.md` | 示例应用、集成测试 | ~300 行 |
| **总计** | | | **~5800 行** |

---

## 四、关键设计决策

| 决策点 | Java 方案 | Go 方案 | 理由 |
|--------|-----------|---------|------|
| 请求上下文 | Input + Output 两个接口 | 统一 Context 结构体 | Go 惯例，减少抽象 |
| AOP/拦截 | CGLIB/Javassist 动态代理 + Interceptor | Middleware 函数链 | Go 原生支持，无需运行时代理 |
| 路由注册 | @Path 注解 + 反射扫描 | 代码注册 / struct 方法注册 | Go 无注解，代码注册更清晰 |
| 泛型 | Java 泛型 `Handler<I, O>` | Go 泛型适度使用 | 简化接口，保持类型安全 |
| 错误处理 | throws Throwable | error 返回值 + panic/recover | Go 惯例 |
| 并发 | 同步阻塞 + 线程池 | goroutine 天然并发 | Go 优势 |
| 路由匹配 | HashMap + ActionGroup | Radix Tree | Go 高性能路由标准做法 |
| 依赖注入 | @Inject + 反射 | 构造函数注入 | Go 惯例 |
| 配置 | AifeiConfig 接口 + 多个 config() 方法 | Functional Options 模式 | Go 惯用的配置模式 |
| 包扫描 | ClassLoader + 文件系统/JAR 扫描 | 不需要 (Go 静态编译) | Go 编译时确定所有代码 |

---

## 五、Go 重写核心原则

1. **保持 API 风格一致** — 链式 API、Db + Row 模式、Just Service 理念
2. **Go 惯用优先** — 使用 Go 的惯用模式而非生搬 Java
3. **最小依赖** — 核心零第三方依赖 (仅标准库)
4. **AI 友好** — 保持代码量少、结构扁平、易于 AI 生成
5. **性能优先** — 利用 Go 的并发优势和高效内存模型
