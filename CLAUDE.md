# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
# Run all tests (workspace root is NOT a Go module, so "..." wildcards do not
# expand from the repo root — list module paths explicitly, as below).
# All _test.go files live under _test/; the library paths below (aifei, config,
# …) carry no tests and are listed only to compile-check them.
go test ./aifei ./config ./dami ./db ./enjoy ./http ./json ./log ./nami ./server \
         ./tools/generator ./tools/damigen \
         ./plugins/cache ./plugins/dami ./plugins/dataisolate ./plugins/kafka ./plugins/nacos ./plugins/storage ./plugins/swagger ./plugins/elasticsearch ./plugins/xxljob \
         ./_test/json_test ./_test/log_test ./_test/config_test ./_test/dami_test \
         ./_test/server_test ./_test/nami_test ./_test/storage_test ./_test/swagger_test \
         ./_test/nacos_test ./_test/damigen_test ./_test/generator_test \
         ./_test/dataisolate_test ./_test/db_test ./_test/cache_test ./_test/kafka_test ./_test/enjoy_test

# Run tests for a single area (tests live under _test/<area>_test)
go test ./_test/db_test
go test ./_test/enjoy_test
go test ./_test/json_test
go test ./_test/log_test
go test ./_test/nacos_test
go test ./_test/generator_test
go test ./_test/config_test
go test ./_test/dami_test

# Run db integration tests (requires sqlite)
go test ./_test/db_test

# Run cache redis integration tests (embedded miniredis; no external redis)
go test ./_test/cache_test

# Run kafka integration tests (embedded franz-go kfake broker; no external kafka)
go test ./_test/kafka_test

# Run a single test
go test ./enjoy -run TestOutputExpr

# Run the demo
go run ./_test/demo
```

## Testing Conventions

**All test code lives under `_test/` — never co-locate `_test.go` files inside the library packages** (do not add tests to `db/`, `enjoy/`, `db/sql/`, etc.). When writing a new test, add it to the matching `_test/<area>_test/` module; if no module exists for that area, create one following the rules below. This is why the `db/sql` parser and `SqlKit` unit tests live in `_test/db_test`, imported as `dbsql "github.com/crazy-airhead/aifei-go/db/sql"` rather than as a `package sql` test inside `db/sql/`.

Each test area is its own Go module:

| Module | Covers | Embedded test dependency |
|--------|--------|--------------------------|
| `_test/db_test` | `db` + `db/sql` (CRUD, pagination, transactions, batch, Enjoy SQL directives, SQL parser) | `modernc.org/sqlite` |
| `_test/cache_test` | `plugins/cache` (local + Redis two-level cache) | `miniredis` |
| `_test/kafka_test` | `plugins/kafka` (producer/consumer, at-least-once) | franz-go `kfake` broker |
| `_test/enjoy_test` | `enjoy` template engine (black-box) | — |
| `_test/json_test` | `json` marshal/unmarshal wrappers (black-box) | — |
| `_test/log_test` | `log` logger interface + levels (black-box) | — |
| `_test/config_test` | `config` layered loading, `Props`/`Store`/`Sub`/`Bind` (black-box) | — |
| `_test/dami_test` | `dami` event bus (send/call/stream/lpc) + `plugins/dami` (black-box) | — |
| `_test/server_test` | `server` bootstrap: `In`/`Out`, `IoHandler`, middleware (black-box) | — |
| `_test/nami_test` | `nami` RPC client + `util`/`coder/json`/`channel/http` subpackages (black-box) | — |
| `_test/storage_test` | `plugins/storage` local + S3 clients, `Manager` (black-box) | `minio-go` |
| `_test/swagger_test` | `plugins/swagger` config loading (black-box) | — |
| `_test/dataisolate_test` | `plugins/dataisolate` tenant/row/column isolation (rewriter unit tests + sqlite integration) (black-box) | `modernc.org/sqlite`, `vitess-sqlparser` |
| `_test/nacos_test` | `plugins/nacos` `NamiUpstream` (black-box) | — |
| `_test/damigen_test` | `tools/damigen` dami-provider codegen (black-box) | — |
| `_test/generator_test` | `tools/generator` schema→code (`MetaReader`, type mapping) (black-box) | `modernc.org/sqlite` |
| `_test/demo` | full demo app (run with `go run`, not a test suite) | `modernc.org/sqlite` |

Rules for adding tests:

- **External test package:** declare `package <area>_test` (matching the directory name) and test the **exported API only**. Import the library normally; alias sub-packages when convenient (e.g. `dbsql "github.com/crazy-airhead/aifei-go/db/sql"`). If you need to exercise unexported behavior, expose a test hook or assert via observable output/SQLite — do not drop a `package <lib>` test file into the library package.
- **Own module per area:** `module github.com/crazy-airhead/aifei-go/_test/<area>_test`, with `replace` directives pointing at the local modules it needs (`../../db`, `../../enjoy`, …), and add `use ./_test/<area>_test` to `go.work`.
- **Self-contained:** prefer embedded/in-process dependencies so `go test` needs no external services — SQLite via `modernc.org/sqlite`, `miniredis` for Redis, franz-go `kfake` for Kafka.
- **Issue regressions:** name the file `issueNNNN_test.go` inside the relevant `_test/<area>_test/` module, matching the `docs/issues/NNNN-*.md` note number.

> **Running tests:** the workspace root is not itself a Go module, so `...` wildcard patterns (e.g. `./_test/...`, `./plugins/...`) do **not** expand from the repo root. Run tests by explicit module path — `go test ./_test/db_test` — or `cd` into the module and run `go test ./...`.

## Module Structure (Go Workspace)

This project uses a Go workspace (`go.work`) of independent modules, layered by role: **Core** (the `aifei` framework itself), **Core library** (zero-external-dep primitives usable standalone), **Runtime** (the default `net/http` adapter + production bootstrap for aifei), **Standalone framework** (sibling frameworks with no dependency on aifei), **Code generation** (`tools/`), **Plugin** (optional integrations that pull in third-party libraries), and **Example** (demos/integration tests under `_test/`, not published).

| Layer | Module | Path | Dependencies |
|-------|--------|------|--------------|
| Core | `github.com/crazy-airhead/aifei-go` | `./aifei` | — |
| Core library | `…/aifei-go/enjoy` | `./enjoy` | — |
| Core library | `…/aifei-go/db` | `./db` | enjoy |
| Core library | `…/aifei-go/json` | `./json` | — |
| Core library | `…/aifei-go/log` | `./log` | — |
| Core library | `…/aifei-go/config` | `./config` | `yaml.v3` |
| Runtime | `…/aifei-go/http` | `./http` | aifei |
| Runtime | `…/aifei-go/server` | `./server` | aifei, http, db, enjoy, log |
| Standalone framework | `…/aifei-go/nami` | `./nami` | — |
| Standalone framework | `…/aifei-go/dami` | `./dami` | — |
| Code generation | `…/aifei-go/tools/generator` | `./tools/generator` | db, enjoy |
| Code generation | `…/aifei-go/tools/damigen` | `./tools/damigen` | enjoy |
| Plugin | `…/aifei-go/plugins/cache` | `./plugins/cache` | aifei, config, log + `jetcache-go`, `go-redis` |
| Plugin | `…/aifei-go/plugins/dami` | `./plugins/dami` | aifei, dami, log |
| Plugin | `…/aifei-go/plugins/kafka` | `./plugins/kafka` | aifei, config, log + `franz-go` |
| Plugin | `…/aifei-go/plugins/nacos` | `./plugins/nacos` | aifei, nami, log + `nacos-sdk-go` |
| Plugin | `…/aifei-go/plugins/storage` | `./plugins/storage` | aifei, config, log + `minio-go` |
| Plugin | `…/aifei-go/plugins/swagger` | `./plugins/swagger` | aifei, config, log + `swaggo/swag` |
| Plugin | `…/aifei-go/plugins/dataisolate` | `./plugins/dataisolate` | aifei, config, db, http, log, server + `vitess-sqlparser` |
| Example | `_test/demo` | `./_test/demo` | core + db + generator + `modernc.org/sqlite` |
| Example | `_test/db_test` | `./_test/db_test` | db + `modernc.org/sqlite` |
| Example | `_test/cache_test` | `./_test/cache_test` | `miniredis` |
| Example | `_test/kafka_test` | `./_test/kafka_test` | `franz-go/kfake` |
| Example | `_test/enjoy_test` | `./_test/enjoy_test` | enjoy |
| Example | `_test/json_test` | `./_test/json_test` | json |
| Example | `_test/log_test` | `./_test/log_test` | log |
| Example | `_test/config_test` | `./_test/config_test` | config |
| Example | `_test/dami_test` | `./_test/dami_test` | dami, plugins/dami |
| Example | `_test/server_test` | `./_test/server_test` | server, aifei |
| Example | `_test/nami_test` | `./_test/nami_test` | nami |
| Example | `_test/storage_test` | `./_test/storage_test` | plugins/storage + `minio-go` |
| Example | `_test/swagger_test` | `./_test/swagger_test` | plugins/swagger |
| Example | `_test/dataisolate_test` | `./_test/dataisolate_test` | plugins/dataisolate + `modernc.org/sqlite` |
| Example | `_test/nacos_test` | `./_test/nacos_test` | plugins/nacos |
| Example | `_test/damigen_test` | `./_test/damigen_test` | tools/damigen |
| Example | `_test/generator_test` | `./_test/generator_test` | tools/generator + `modernc.org/sqlite` |

`…` = `github.com/crazy-airhead`. In the Dependencies column, bare names are internal modules; backticked names are external libraries; `—` means no dependencies.

Users can import individual modules without pulling unwanted dependencies:
- `go get github.com/crazy-airhead/aifei-go/enjoy` — template engine only, zero external deps
- `go get github.com/crazy-airhead/aifei-go/db` — database access only, zero external deps (user provides their own driver)
- `go get github.com/crazy-airhead/aifei-go` — core web framework, zero external deps
- `go get github.com/crazy-airhead/aifei-go/nami` — HTTP RPC client framework, zero external deps
- `go get github.com/crazy-airhead/aifei-go/plugins/nacos` — Nacos plugin (service registry, config center, discovery)
- `go get github.com/crazy-airhead/aifei-go/plugins/storage` — storage plugin (local filesystem + S3-compatible backends)
- `go get github.com/crazy-airhead/aifei-go/plugins/cache` — cache plugin (local FreeCache/TinyLFU + Redis two-level cache)
- `go get github.com/crazy-airhead/aifei-go/plugins/swagger` — knife4j-vue3 OpenAPI docs plugin (embedded UI, serves spec via swaggo/swag)
- `go get github.com/crazy-airhead/aifei-go/plugins/kafka` — Kafka plugin (franz-go producer/consumer, multi-cluster, at-least-once Subscribe)
- `go get github.com/crazy-airhead/aifei-go/plugins/dataisolate` — data isolation plugin (tenant isolation + row/column isolation; AST SQL rewriting via vitess-sqlparser)

Requires Go 1.26. All library code uses only the Go standard library.

## Architecture

Aifei-Go is a lightweight Go web framework ported from [Aifei Java](https://github.com/jfinal/aifei). It follows a "Just Service" philosophy — flat architecture, no Controller/Service/DAO layers.

### Core (`./aifei`)

- **`aifei.go`** — `Aifei` struct: entry point with `New()`, `Use()`, route methods (`GET`/`POST`/`PUT`/`DELETE`/`PATCH`/`Any`), `Handle()`, `Group()`. Implements `http.Handler` via `ServeHTTP`. (Struct-method → route registration is `server.Register()`, NOT a method on `Aifei`.)
- **`input.go`** — `Input` interface: request parameter abstraction. Methods: `Has()`, `PathPara()`, `GetStr()`, `GetInt()`, `GetInt64()`, `GetFloat64()`, `GetBool()`, `GetBean()`, `Body()`, `Method()`, `Path()`, `RemoteIP()`, `Query()`.
- **`output.go`** — `Output` interface: response abstraction. Methods: `Code()`, `Msg()`, `Data()`. The `server` package provides the `Out` struct implementing this.
- **`handler.go`** — `HandlerFunc func(in Input) Output`. `ChainHandlers()` composes handler chains from `Handler` wrappers.
- **`router.go`** — Radix tree router per HTTP method. Supports `:param` and `*catchAll`. `RouterGroup` for grouped routes with prefix + handler wrappers. (Reflection-based struct-method routing lives in `server/register.go` — see server bootstrap & Naming Conventions.)
- **`interceptor.go`** — `Interceptor` interface for method-level AOP. `InterceptorFunc` adapter. `MethodInterceptors` interface for services with per-method interceptors.
- **`config.go`** — `Config` with functional options pattern (`WithHandlers`, `WithPlugin`, `WithOnStart`, `WithOnStop`). `WithHandlers` accepts `Handler` wrappers.
- **`plugin.go`** — `Plugin` interface (`Start()`/`Stop()` lifecycle).

### HTTP Adapter (`./http`)

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
- **`register.go`** — `Register(router, prefix, service, handlers...)`: reflects over a service struct's exported methods and maps each to a route via `resolveRoute()` (two rules — see Naming Conventions). This powers struct-based "Just Service" routing.
- **`service.go`** — `RegisterService()` + `AutoRegisterServices(app)`: generated `service.go` files self-register in `init()`; `AutoRegisterServices` calls `Register` for each.
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

### Code Generator (`./tools/generator`)

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
- **`./plugins/nacos`** — Nacos integration plugin built on nacos-sdk-go/v2. Implements `aifei.Plugin` for service registration (ephemeral instances with SDK heartbeats), config center (watch DataID, push changes via callback), and discovery (`NewNamiUpstream` converts Nacos discovery into `nami.Upstream`). Auto-registers a `config.CloudLoader` via `init()` so `config.Init()` automatically fetches config from Nacos at L5 when `nacos.server_addr` + `nacos.data_id` are set. `BindStore(store)` method chains `ConfigChangeCallback` to auto-update the Store on runtime config changes from Nacos.
- **`./config`** — Layered configuration loading with generic `Store` (key-value map). Supports L1-L5 loading order: `app.yml` + `app-{env}.yml` → extension configs → env vars + CLI args → programmatic `LoadInto()` → cloud loaders (e.g., Nacos). Provides `Get`/`GetStr`/`GetBool`/`GetInt` accessors, `Sub(prefix)` for scoped sub-props, `Bind(v)` for YAML round-trip to user-defined structs, and functional options (`WithEnvPrefix`, `WithEnv`, `WithConfigDir`, `WithBaseFiles`). Thread-safe (`sync.RWMutex`) — safe for concurrent reads and dynamic updates from cloud config watchers. Does NOT define application-level config structs — each app defines its own.
- **`./plugins/storage`** — Unified file-storage abstraction (ported from ficus `ficus-starter-storage`) with local filesystem and S3-compatible backends (AWS S3/Minio/OSS/COS) via minio-go. `Client` interface (`Exists`/`TempURL`/`Get`/`Put`/`Delete`/`DeleteBatch`, bucket-scoped) + `Media` model (`io.Reader` + content type/size, stdlib `mime` inference). `Manager` routes by bucket name with a default; `Plugin` (`aifei.Plugin`) reads `storage.*` from `config.Props` (`storage.default` + `storage.buckets.<name>.{driver,endpoint,regionId,accessKey,secretKey,autoCreateBucket}`) and installs the package-level default so top-level `storage.Put/Get/...` and `storage.Use(bucket)` work. Driver inferred from `driver` (`local`/`s3`) or endpoint scheme.
- **`./plugins/swagger`** — Knife4j-vue3 OpenAPI docs plugin. Implements `aifei.Plugin` to serve the compiled knife4j-vue3 UI (`web/` is embedded via `//go:embed`) plus a generated `services.json` group config and the OpenAPI spec at a configurable base path (default `/swagger`). The UI is pure static frontend (no springboot) compiled with `VITE_RELEASE_APP_TYPE=Knife4jFront`; it requests `/services.json` from the server root (hardcoded), which points it to `{basePath}/swagger.json` served via `swag.ReadDoc()`. Provides `Handler() func(http.Handler) http.Handler` middleware that intercepts matching requests to serve raw HTML/JSON/CSS/JS outside the aifei `{code, msg, data}` envelope; users wire it via `server.WithHTTPHandler(swagPlugin.Handler())`. Configured via `swagger.*` in the global config (`enabled`, `basePath`, `groupName`). Users run `swag init` to generate docs from Go comments, import the generated `docs` package (which registers the spec), and add `swagger.NewPlugin(nil)` to the app. Dependencies: `github.com/swaggo/swag`.
- **`./plugins/cache`** — Two-level (local + Redis) cache abstraction built on jetcache-go (inspired by ficus `CacheService`). `Cache` interface (`Get` returning a `found bool` distinct from miss, `Set`/`Delete`/`Exists`, and `GetOrStore` doing singleflight + cache-penetration protection) wraps jetcache-go, exposing FreeCache/TinyLFU L1 and go-redis L2; per-instance key prefixing isolates instances sharing one Redis. `Manager` routes by instance name with a default; `Plugin` (`aifei.Plugin`) reads `cache.*` from `config.Props` (`cache.default` + `cache.instances.<name>.{type,ttl,codec,keyPrefix,local,remote,refresh,syncLocal}`) and installs the package-level default so top-level `cache.Get/Set/Delete/Exists/GetOrStore` and `cache.Use(instance)` work. `Stop()` closes every instance (unlike storage, caches may run refresh goroutines). Type inferred from `type` (`local`/`remote`/`both`) or which of `local`/`remote` is configured; L1 driver `freecache`/`tinylfu`, L2 redis `addr` (single node) or `addrs` (ring). Advanced jetcache features (SetNX/Refresh/SyncLocal/...) are reachable via `Cache.JetCache()`.
- **`./plugins/kafka`** — Kafka producer/consumer abstraction built on franz-go (`twmb/franz-go`). `Client` interface (per-cluster) exposes `ProduceSync` (sync ack)/`Produce` (async w/ `Promise`)/`Flush`/`Subscribe` over `Message`/`Header` records; each `Subscribe` spawns a dedicated consumer client running an at-least-once poll loop — `AutoCommitMarks` is enabled so records are only committed once their handler returns nil (failed records are not committed and are redelivered on the next rebalance/restart); `Subscription.Close` does a final `CommitMarkedOffsets`. `Manager` routes by cluster name with a default; `Plugin` (`aifei.Plugin`) reads `kafka.*` from `config.Props` (`kafka.default` + `kafka.clusters.<name>.{brokers,clientId,sasl.{mechanism,user,password},tls.{enabled,caFile,certFile,keyFile,insecureSkipVerify},producer.{acks,compression,lingerMs,maxAttempts},consumer.{groupId,offsetReset,balancer,autoCommit.{enable,intervalMs}}}`) and installs the package-level default so top-level `kafka.ProduceSync/Produce/Flush/Subscribe` and `kafka.Use(cluster)` work. `Stop()` stops every running subscription (committing marked offsets) and closes every producer client. Defaults: acks=all, compression=snappy, offsetReset=latest, balancer=cooperativeSticky, autoCommit enable=true/5s. SASL plain/scram-sha-256/512 and TLS (incl. mTLS) supported; all built from the root franz-go module. Advanced needs (transactions, manual commits, seek, admin via `kadm`) are reachable via `Client.KgoClient()`/`Subscription.KgoClient()`. Integration tested against the in-memory `kfake` broker in `_test/kafka_test`.
- **`./plugins/dataisolate`** — Data isolation (tenant + row + column) for `db`, ported from the `docs/data-isolate.md` design. Three orthogonal policies share one mechanism (Principal from context → AST rewrite → rebuilt `?`-SQL with realigned args): `TenantPolicy` (shared-table strategy ③ injects `WHERE <alias>.tenant_id=?` and stamps INSERT rows; strategies ①/② database/schema route by `db.Config` with zero SQL rewrite via `Use(ctx)`), `DataScopePolicy` (row scope — self/dept/dept-tree/region/custom — from an app-supplied `ScopeRuleProvider`), and `FieldMaskPolicy` (column mask — NULL/constant/remove — on the outermost SELECT projection, from an app-supplied `FieldRuleProvider`). SQL parsing uses `github.com/ajitpratap0/GoSQLX`: it parses only PostgreSQL-style `$N` placeholders (the MySQL/SQLite `?` aifei renders is rejected), so the rewriter converts `?`→`$N` before parsing and `$N`→`?` after rendering, keeping placeholders and args aligned; PostgreSQL syntax (`::`, `RETURNING`, JSON operators) parses natively and is isolated (only genuinely malformed statements fail-closed via `Dao.Fail`). `Plugin` (`aifei.Plugin`) reads `dataisolate.*` from `config.Props` (`policies`, `enforce`, `allow_bypass`, `on_failure`, `configs`, `tenant.{strategy,column,scope.{mode,ignore_tables,tables},tenants}`, `scope.merge`, `field.default_mask`) and installs a `DbHookKit` (all 6 db hooks, composed with any pre-existing hook) on the listed `db.Config`s. `Middleware()` (`aifei.Handler`) resolves the `Principal` (built-in `SubdomainHeaderResolver` for tenant id; apps plug in JWT/session resolvers for the full principal) and writes it into the request context. `Bypass(ctx)`/`As(ctx, p)` escape hatches act on one call; identity columns are registered via `RegisterTableMeta` or inferred by convention. Application code stays isolation-unaware: just use `db.WithCtx(in.Context())` / `dataisolate.Use(in.Context())` (strategy ①/②) / `dataisolate.Sql(...)` (db.Sql directive path). Requires 5 backward-compatible additions to `db` (`Dao.Context()`, `Dao.SqlAndArgs()`, `Dao.Fail` veto, `db.Batch` triggering hooks, and INSERT field recompute after the stamp hook). Integration tested in `_test/dataisolate_test` against in-memory sqlite. GoSQLX requires Go 1.26.1.

### Examples

- **`./_test/demo`** — Full web app demo using core + db + generator with SQLite driver.
- **`./_test/db_test`** — Database integration tests (971 lines, ~80 test cases).

## Design Decisions (Java → Go)

| Java | Go |
|------|-----|
| Input + Output interfaces | `Input` / `Output` interfaces (preserved) |
| CGLIB/Javassist AOP proxy | `Handler` wrapper chain + `Interceptor` for method-level AOP |
| `@Path` annotation + reflection scanning | Code registration / `Register()` struct reflection |
| Undertow HTTP server | `net/http` via `http` adapter + `server` bootstrap |
| Functional options for config | Same pattern preserved |

## Project State

The framework is functionally complete (~8,700 lines of library code + ~2,100 lines of tests across 78 Go files). Design docs are in `docs/` with detailed specs per phase (`00-overview.md` through `06-phase6-example.md`).

## Naming Conventions

- API methods follow Java Aifei naming: `GetStr`, `GetInt`, `GetBean`, `FindByID`, etc.
- `server.Register()` (in `register.go`) maps a service struct's exported methods to routes via two rules in `resolveRoute()`:
  - **Default actions (exact match)**, registered at the service prefix directly: `Paginate` → GET `/prefix`, `Create` → POST `/prefix`, `List` → GET `/prefix/list`.
  - **Verb prefixes**: method name must start with (and be longer than) one of `Get`/`Post`/`Put`/`Delete`/`Update`. The verb picks the HTTP method (`Get`→GET, `Post`→POST, `Put`→PUT, `Update`→PUT, `Delete`→DELETE); the remainder becomes the path suffix via camelCase→kebab-case (e.g. `GetProfile` → GET `/prefix/profile`, `PostApprove` → POST `/prefix/approve`).
  - Methods matching **neither** rule are skipped (not routed) — safe for private helpers. Note: there is NO prefix rule for `Save`/`Remove`/`Find`; `Create` is an exact-match default action only.
- `ById` in a method name becomes `:id`: `ById` → kebab `by-id` → path param `:id`, so `GetById` = GET `/prefix/:id`.
