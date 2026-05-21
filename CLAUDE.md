# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
# Run all tests
/usr/local/go/bin/go test ./...

# Run tests for a single package
/usr/local/go/bin/go test ./enjoy
/usr/local/go/bin/go test ./db
/usr/local/go/bin/go test ./json
/usr/local/go/bin/go test ./log

# Run a single test
/usr/local/go/bin/go test ./enjoy -run TestOutputExpr

# Run the demo
/usr/local/go/bin/go run ./_example/demo
```

Go is at `/usr/local/go/bin/go` (not on default PATH).

## Architecture

Aifei-Go is a lightweight Go web framework ported from a Java version (`/Users/airhead/WorkSpace/aifei/aifei`). It follows a "Just Service" philosophy — flat architecture, no Controller/Service/DAO layers.

Module: `github.com/aifei/aifei`, requires Go 1.26. Only external dependency: `modernc.org/sqlite` (pure-Go SQLite driver). Everything else uses the Go standard library.

### Core (`/` root package)

- **`aifei.go`** — `Aifei` struct: entry point with `New()`, `Use()`, route methods (`GET`/`POST`/etc.), `Register()` for struct-based routing, `Run()`/`Start()`/`Stop()`. Implements `http.Handler`.
- **`context.go`** — `Context`: unified request/response object (merges Java's Input + Output). Lazy body reading, chain control via `Next()`/`Abort()`. JSON/text/HTML response helpers.
- **`router.go`** — Radix tree router per HTTP method. Supports `:param` and `*catchAll`. `RouterGroup` for grouped routes with prefix + middleware. `Register()` uses reflection to auto-map struct methods by naming convention (e.g., `Get*` → GET, `List*` → GET, `Save*` → POST, method name `ById` → `/:id`).
- **`handler.go`** — `HandlerFunc func(c *Context)`, `Middleware func(next HandlerFunc) HandlerFunc`.
- **`middleware.go`** — Built-in middleware: `Logger`, `Recover`, `CORS`, `BasicAuth`, `RequestID`, `Timeout`, `Static`.
- **`config.go`** — `Config` with functional options pattern.

### Enjoy Template Engine (`/enjoy`)

~2600 lines, the framework's signature feature. Custom template language with its own lexer/parser:
- **DKFF algorithm** for tokenization, **DLRD recursive descent** for parsing
- Supports: `#()` expression output, `#if`/`#else`/`#elseif`, `#for`, `#set`, `#define`/`#call`, `#include`, `#switch`
- Expression engine: arithmetic, comparison, logic, ternary, null-safe (`??`, `?.`), method calls, map/array literals
- `Engine` → `Template` → `Env`/`Scope` execution model
- Configured via `EngineConfig` (custom directives, shared functions/objects)

### Database (`/db`)

- **`Db`** (`db.go`) — Top-level convenience functions: `Use()`, `SQL()`, `Select()`, `Insert()`, `Update()`, `Delete()`, `FindByID()`, `FindBy()`, etc.
- **`Dao`** (`dao.go`) — Chainable query builder for single-table CRUD operations
- **`SQLBuilder`** (`sql_builder.go`) — Chainable SQL builder for complex queries: `NewSQL()`.Where().OrderBy().Paginate()
- **`Row`** (`row.go`) — Active Record pattern with change tracking. `Set()` tracks changes (used for UPDATE), `Put()` does not.
- **`Config`** (`config.go`) — Connection management with `db.Init(driver, dsn, ...)`. Supports multiple named configs via `InitWithID()`. Lazy connection pool.
- **`Batch`** / **`Transaction`** — Batch operations and transaction support
- **`Dialect`** — Database-specific SQL generation (MySQL, PostgreSQL, SQLite)

### Other Packages

- **`/json`** — Lightweight JSON marshal/unmarshal wrappers
- **`/log`** — Logging interface with default implementation

## Design Decisions (Java → Go)

| Java | Go |
|------|-----|
| Input + Output interfaces | Unified `Context` struct |
| CGLIB/Javassist AOP proxy | `Middleware` function chain |
| `@Path` annotation + reflection scanning | Code registration / `Register()` struct reflection |
| Undertow HTTP server | `net/http` |
| Functional options for config | Same pattern preserved |

## Project State

The framework is functionally complete (~6300 lines across 41 files). Design docs are in `docs/` with detailed specs per phase (`00-overview.md` through `06-phase6-example.md`). Prompt templates for AI-assisted development are in `docs/prompts/`.

## Naming Conventions

- API methods follow Java Aifei naming: `GetStr`, `GetInt`, `GetBean`, `JsonOK`, `JsonFail`, `FindByID`, etc.
- The `Register()` method maps Go struct methods to routes using naming conventions: `List*`/`Get*` → GET, `Post*`/`Save*`/`Create*` → POST, `Put*`/`Update*` → PUT, `Delete*`/`Remove*` → DELETE
- `ById` suffix in method names becomes `/:id` path parameter
