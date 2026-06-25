# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
# Run all tests (workspace mode)
go test ./aifei ./enjoy ./db ./json ./log ./generator ./_example/db_sqlite_test

# Run tests for a single module
go test ./aifei
go test ./enjoy
go test ./db
go test ./json
go test ./log
go test ./generator

# Run db integration tests (requires sqlite)
go test ./_example/db_sqlite_test

# Run a single test
go test ./enjoy -run TestOutputExpr

# Run the demo
go run ./_example/demo
```

## Module Structure (Go Workspace)

This project uses Go workspace (`go.work`) with independent modules. Each library module has zero external dependencies.

| Module | Path | External Dependencies |
|--------|------|-----------------------|
| `github.com/crazy-airhead/aifei-go` | `./aifei` | None |
| `github.com/crazy-airhead/aifei-go/enjoy` | `./enjoy` | None |
| `github.com/crazy-airhead/aifei-go/db` | `./db` | None |
| `github.com/crazy-airhead/aifei-go/json` | `./json` | None |
| `github.com/crazy-airhead/aifei-go/log` | `./log` | None |
| `github.com/crazy-airhead/aifei-go/generator` | `./generator` | db, enjoy (both zero external deps) |
| `github.com/crazy-airhead/aifei-go/go-http` | `./go-http` | aifei (zero external deps) |
| `github.com/crazy-airhead/aifei-go/server` | `./server` | aifei, go-http (zero external deps) |
| `github.com/crazy-airhead/aifei-go/nami` | `./nami` | None (HTTP RPC client framework) |
| `_example/demo` | `./_example/demo` | `modernc.org/sqlite` |
| `_example/db_sqlite_test` | `./_example/db_sqlite_test` | `modernc.org/sqlite` |

Users can import individual modules without pulling unwanted dependencies:
- `go get github.com/crazy-airhead/aifei-go/enjoy` — template engine only, zero external deps
- `go get github.com/crazy-airhead/aifei-go/db` — database access only, zero external deps (user provides their own driver)
- `go get github.com/crazy-airhead/aifei-go` — core web framework, zero external deps
- `go get github.com/crazy-airhead/aifei-go/nami` — HTTP RPC client framework, zero external deps

Requires Go 1.26. All library code uses only the Go standard library.

## Architecture

Aifei-Go is a lightweight Go web framework ported from [Aifei Java](https://github.com/jfinal/aifei). It follows a "Just Service" philosophy — flat architecture, no Controller/Service/DAO layers.

### Core (`./aifei`)

- **`aifei.go`** — `Aifei` struct: entry point with `New()`, `Use()`, route methods (`GET`/`POST`/etc.), `Register()` for struct-based routing. Implements `http.Handler` via `ServeHTTP`.
- **`input.go`** — `Input` interface: request parameter abstraction. Methods: `Has()`, `PathPara()`, `GetStr()`, `GetInt()`, `GetInt64()`, `GetFloat64()`, `GetBool()`, `GetBean()`, `Body()`, `Method()`, `Path()`, `RemoteIP()`, `Query()`.
- **`output.go`** — `Output` interface: response abstraction. Methods: `Code()`, `Msg()`, `Data()`. The `server` package provides the `Out` struct implementing this.
- **`handler.go`** — `HandlerFunc func(in Input) Output`. `ChainHandlers()` composes handler chains from `Handler` wrappers.
- **`router.go`** — Radix tree router per HTTP method. Supports `:param` and `*catchAll`. `RouterGroup` for grouped routes with prefix + handler wrappers. `Register()` uses reflection to auto-map struct methods by naming convention (e.g., `Get*` → GET, `List*` → GET, `Save*` → POST, method name `ById` → `/:id`).
- **`interceptor.go`** — `Interceptor` interface for method-level AOP. `InterceptorFunc` adapter. `MethodInterceptors` interface for services with per-method interceptors.
- **`config.go`** — `Config` with functional options pattern (`WithHandlers`, `WithPlugin`, `WithOnStart`, `WithOnStop`). `WithHandlers` accepts `Handler` wrappers.
- **`plugin.go`** — `Plugin` interface (`Start()`/`Stop()` lifecycle).

### HTTP Adapter (`./go-http`)

Bridges `net/http` to the aifei framework:
- **`context.go`** — `HttpContext` implements `aifei.Input` by wrapping `*http.Request`.
- **`handler.go`** — `HttpHandler` implements `http.Handler`, bridging to `aifei.Aifei`.
- **`server.go`** — `Server` interface and `DefaultServer` (net/http implementation).

### Server Bootstrap (`./server`)

Convenience layer for production use:
- **`in.go`** — `In` struct: full `aifei.Input` implementation (wraps `*http.Request`).
- **`out.go`** — `Out` struct: fluent `aifei.Output` builder (`Ok()`, `Fail()`, `Of()`, `OfField()`, `SetMsg()`, `SetData()`, `IsOk()`, `ShouldRollback()`).
- **`middleware.go`** — Built-in `Handler` wrappers: `Logger()`, `Recover()`, `Timeout()`, and HTTP-level wrappers: `CORS()`, `BasicAuth()`, `RequestID()`, `StaticFile()`.
- **`run.go`** — `Run(app, addr, opts...)` — starts server with graceful shutdown, plugin lifecycle, signal handling.
- **`service.go`** — `RegisterService()`, `AutoRegisterServices()` for centralized service registration.
- **`tx_interceptor.go`** — `TxInterceptor()` for automatic transaction wrapping.

### Enjoy Template Engine (`./enjoy`)

~2,500 lines, the framework's signature feature. Custom template language with its own lexer/parser:
- **DKFF algorithm** for tokenization, **DLRD recursive descent** for parsing
- Supports: `#()` expression output, `#if`/`#else`/`#elseif`, `#for`, `#set`/`#setLocal`/`#setGlobal`, `#define`/`#call`, `#include`, `#switch`/`#case`/`#default`, `#break`/`#continue`/`#return`
- Expression engine: arithmetic, comparison, logic, ternary, null-safe (`??`, `?.`), method calls, map/array literals, static access (`::`)
- `Engine` → `Template` → `Env`/`Scope` execution model
- Configured via `EngineConfig` (custom directives, shared functions/objects)
- Flat file structure (not subdirectory-based)

### Database (`./db`)

- **`db.go`** — Top-level convenience functions: `Use()`, `SQL()`, `Select()`, `Insert()`, `Update()`, `Delete()`, `FindByID()`, `FindBy()`, etc.
- **`dao.go`** — `Dao`: chainable query builder for single-table CRUD operations.
- **`row.go`** — `Row`: Active Record pattern with change tracking. `Set()` tracks changes (used for UPDATE), `Put()` does not.
- **`config.go`** — `Config`: connection management with `db.Init(driver, dsn, ...)`. Supports multiple named configs via `InitWithID()`. Lazy connection pool.
- **`batch.go`** / **`transaction.go`** — Batch operations and transaction support.
- **`dialect.go`** — Database-specific SQL generation (MySQL, PostgreSQL, SQLite).
- **`type_converter.go`** — Type conversion helpers (`ToInt`, `ToStr`, `ToTime`, etc.).
- **`table.go`** — `Table`: runtime table metadata for code generation.
- **`db/sql/`** — Enjoy SQL: `SqlKit` wrapping Enjoy engine with `#sql`, `#para`, `#where`, `#and`, `#orderBy` directives supporting 18 operators.

### Code Generator (`./generator`)

Generates type-safe per-table packages from database schema:
- **`generator.go`** — Main entry point: `New(pool, dialect, outputDir, importRoot)`.
- **`meta_reader.go`** — Reads DB metadata (table names, columns, types) via `ColumnTypes`.
- **`meta_dialect.go`** — Dialect-specific metadata queries (MySQL, PostgreSQL, SQLite).
- **`type_mapping.go`** — SQL type → Go type mapping (30+ types).
- **`base_generator.go`** — Generates `base.go` (always overwritten): `BaseXxx` struct, `Table` var, getters/setters.
- **`model_generator.go`** — Generates model file (skipped if exists).
- **`dao_generator.go`** — Generates `dao.go` (skipped if exists): type-safe `FindById`, `FindBy`, `DeleteById`, etc.
- **`service_generator.go`** — Generates `service.go`: HTTP service with method routing.
- **`tables_generator.go`** — Generates `tables.go` (always overwritten): cross-table `Tables` slice.
- **`templates/`** — Embedded Enjoy templates: `_base.af`, `_model.af`, `_dao.af`, `_service.af`, `_tables.af`.

### Other Packages

- **`./json`** — Lightweight JSON marshal/unmarshal wrappers.
- **`./log`** — Logging interface (`Logger` with 5 levels) + default implementation.
- **`./nami`** — Lightweight HTTP RPC **client** framework (ported from Java Solon Nami). Channel transport (`channel/http`), Encoder/Decoder (`coder/json`), `Filter` chain, `Upstream`/`Discovery`, fluent `Builder`/`ClientFactory`, and a `util` package (`GetJSON[T]` etc.). Server-side counterpart to aifei; zero external deps.

### Examples

- **`./_example/demo`** — Full web app demo using core + db + generator with SQLite driver.
- **`./_example/db_sqlite_test`** — Database integration tests (971 lines, ~80 test cases).

## Design Decisions (Java → Go)

| Java | Go |
|------|-----|
| Input + Output interfaces | `Input` / `Output` interfaces (preserved) |
| CGLIB/Javassist AOP proxy | `Handler` wrapper chain + `Interceptor` for method-level AOP |
| `@Path` annotation + reflection scanning | Code registration / `Register()` struct reflection |
| Undertow HTTP server | `net/http` via `go-http` adapter + `server` bootstrap |
| Functional options for config | Same pattern preserved |

## Project State

The framework is functionally complete (~8,350 lines of library code + ~2,057 lines of tests across 74 Go files). Design docs are in `docs/` with detailed specs per phase (`00-overview.md` through `06-phase6-example.md`).

## Naming Conventions

- API methods follow Java Aifei naming: `GetStr`, `GetInt`, `GetBean`, `FindByID`, etc.
- The `Register()` method maps Go struct methods to routes using naming conventions: `List*`/`Get*` → GET, `Post*`/`Save*`/`Create*` → POST, `Put*`/`Update*` → PUT, `Delete*`/`Remove*` → DELETE
- `ById` suffix in method names becomes `/:id` path parameter
