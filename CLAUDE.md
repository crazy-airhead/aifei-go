# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Test

```bash
# Run all tests (workspace mode)
go test ./aifei ./enjoy ./db ./json ./log ./generator ./nacos ./config ./cache ./_example/db_sqlite_test ./_example/cache_redis_test

# Run tests for a single module
go test ./aifei
go test ./enjoy
go test ./db
go test ./json
go test ./log
go test ./nacos
go test ./generator
go test ./config

# Run db integration tests (requires sqlite)
go test ./_example/db_sqlite_test

# Run cache redis integration tests (embedded miniredis; no external redis)
go test ./_example/cache_redis_test

# Run kafka integration tests (embedded franz-go kfake broker; no external kafka)
go test ./_example/kafka_test

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
| `github.com/crazy-airhead/aifei-go/nacos` | `./nacos` | aifei, nami, log, nacos-sdk-go/v2 |
| `github.com/crazy-airhead/aifei-go/storage` | `./storage` | aifei, config, log, minio-go/v7 |
| `github.com/crazy-airhead/aifei-go/cache` | `./cache` | aifei, config, log, jetcache-go, go-redis/v9 |
| `github.com/crazy-airhead/aifei-go/config` | `./config` | `gopkg.in/yaml.v3` |
| `github.com/crazy-airhead/aifei-go/swagger` | `./swagger` | aifei, config, log, swaggo/swag |
| `github.com/crazy-airhead/aifei-go/kafka` | `./kafka` | aifei, config, log, twmb/franz-go |
| `_example/demo` | `./_example/demo` | `modernc.org/sqlite` |
| `_example/db_sqlite_test` | `./_example/db_sqlite_test` | `modernc.org/sqlite` |
| `_example/cache_redis_test` | `./_example/cache_redis_test` | `github.com/alicebob/miniredis/v2` |
| `_example/kafka_test` | `./_example/kafka_test` | `github.com/twmb/franz-go/pkg/kfake` |

Users can import individual modules without pulling unwanted dependencies:
- `go get github.com/crazy-airhead/aifei-go/enjoy` — template engine only, zero external deps
- `go get github.com/crazy-airhead/aifei-go/db` — database access only, zero external deps (user provides their own driver)
- `go get github.com/crazy-airhead/aifei-go` — core web framework, zero external deps
- `go get github.com/crazy-airhead/aifei-go/nami` — HTTP RPC client framework, zero external deps
- `go get github.com/crazy-airhead/aifei-go/nacos` — Nacos plugin (service registry, config center, discovery)
- `go get github.com/crazy-airhead/aifei-go/storage` — storage plugin (local filesystem + S3-compatible backends)
- `go get github.com/crazy-airhead/aifei-go/cache` — cache plugin (local FreeCache/TinyLFU + Redis two-level cache)
- `go get github.com/crazy-airhead/aifei-go/swagger` — knife4j-vue3 OpenAPI docs plugin (embedded UI, serves spec via swaggo/swag)
- `go get github.com/crazy-airhead/aifei-go/kafka` — Kafka plugin (franz-go producer/consumer, multi-cluster, at-least-once Subscribe)

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
- **`./nacos`** — Nacos integration plugin built on nacos-sdk-go/v2. Implements `aifei.Plugin` for service registration (ephemeral instances with SDK heartbeats), config center (watch DataID, push changes via callback), and discovery (`NewNamiUpstream` converts Nacos discovery into `nami.Upstream`). Auto-registers a `config.CloudLoader` via `init()` so `config.Init()` automatically fetches config from Nacos at L5 when `nacos.server_addr` + `nacos.data_id` are set. `BindStore(store)` method chains `ConfigChangeCallback` to auto-update the Store on runtime config changes from Nacos.
- **`./config`** — Layered configuration loading with generic `Store` (key-value map). Supports L1-L5 loading order: `app.yml` + `app-{env}.yml` → extension configs → env vars + CLI args → programmatic `LoadInto()` → cloud loaders (e.g., Nacos). Provides `Get`/`GetStr`/`GetBool`/`GetInt` accessors, `Sub(prefix)` for scoped sub-props, `Bind(v)` for YAML round-trip to user-defined structs, and functional options (`WithEnvPrefix`, `WithEnv`, `WithConfigDir`, `WithBaseFiles`). Thread-safe (`sync.RWMutex`) — safe for concurrent reads and dynamic updates from cloud config watchers. Does NOT define application-level config structs — each app defines its own.
- **`./storage`** — Unified file-storage abstraction (ported from ficus `ficus-starter-storage`) with local filesystem and S3-compatible backends (AWS S3/Minio/OSS/COS) via minio-go. `Client` interface (`Exists`/`TempURL`/`Get`/`Put`/`Delete`/`DeleteBatch`, bucket-scoped) + `Media` model (`io.Reader` + content type/size, stdlib `mime` inference). `Manager` routes by bucket name with a default; `Plugin` (`aifei.Plugin`) reads `storage.*` from `config.Props` (`storage.default` + `storage.buckets.<name>.{driver,endpoint,regionId,accessKey,secretKey,autoCreateBucket}`) and installs the package-level default so top-level `storage.Put/Get/...` and `storage.Use(bucket)` work. Driver inferred from `driver` (`local`/`s3`) or endpoint scheme.
- **`./swagger`** — Knife4j-vue3 OpenAPI docs plugin. Implements `aifei.Plugin` to serve the compiled knife4j-vue3 UI (`web/` is embedded via `//go:embed`) plus a generated `services.json` group config and the OpenAPI spec at a configurable base path (default `/swagger`). The UI is pure static frontend (no springboot) compiled with `VITE_RELEASE_APP_TYPE=Knife4jFront`; it requests `/services.json` from the server root (hardcoded), which points it to `{basePath}/swagger.json` served via `swag.ReadDoc()`. Provides `Handler() func(http.Handler) http.Handler` middleware that intercepts matching requests to serve raw HTML/JSON/CSS/JS outside the aifei `{code, msg, data}` envelope; users wire it via `server.WithHTTPHandler(swagPlugin.Handler())`. Configured via `swagger.*` in the global config (`enabled`, `basePath`, `groupName`). Users run `swag init` to generate docs from Go comments, import the generated `docs` package (which registers the spec), and add `swagger.NewPlugin(nil)` to the app. Dependencies: `github.com/swaggo/swag`.
- **`./cache`** — Two-level (local + Redis) cache abstraction built on jetcache-go (inspired by ficus `CacheService`). `Cache` interface (`Get` returning a `found bool` distinct from miss, `Set`/`Delete`/`Exists`, and `GetOrStore` doing singleflight + cache-penetration protection) wraps jetcache-go, exposing FreeCache/TinyLFU L1 and go-redis L2; per-instance key prefixing isolates instances sharing one Redis. `Manager` routes by instance name with a default; `Plugin` (`aifei.Plugin`) reads `cache.*` from `config.Props` (`cache.default` + `cache.instances.<name>.{type,ttl,codec,keyPrefix,local,remote,refresh,syncLocal}`) and installs the package-level default so top-level `cache.Get/Set/Delete/Exists/GetOrStore` and `cache.Use(instance)` work. `Stop()` closes every instance (unlike storage, caches may run refresh goroutines). Type inferred from `type` (`local`/`remote`/`both`) or which of `local`/`remote` is configured; L1 driver `freecache`/`tinylfu`, L2 redis `addr` (single node) or `addrs` (ring). Advanced jetcache features (SetNX/Refresh/SyncLocal/...) are reachable via `Cache.JetCache()`.
- **`./kafka`** — Kafka producer/consumer abstraction built on franz-go (`twmb/franz-go`). `Client` interface (per-cluster) exposes `ProduceSync` (sync ack)/`Produce` (async w/ `Promise`)/`Flush`/`Subscribe` over `Message`/`Header` records; each `Subscribe` spawns a dedicated consumer client running an at-least-once poll loop — `AutoCommitMarks` is enabled so records are only committed once their handler returns nil (failed records are not committed and are redelivered on the next rebalance/restart); `Subscription.Close` does a final `CommitMarkedOffsets`. `Manager` routes by cluster name with a default; `Plugin` (`aifei.Plugin`) reads `kafka.*` from `config.Props` (`kafka.default` + `kafka.clusters.<name>.{brokers,clientId,sasl.{mechanism,user,password},tls.{enabled,caFile,certFile,keyFile,insecureSkipVerify},producer.{acks,compression,lingerMs,maxAttempts},consumer.{groupId,offsetReset,balancer,autoCommit.{enable,intervalMs}}}`) and installs the package-level default so top-level `kafka.ProduceSync/Produce/Flush/Subscribe` and `kafka.Use(cluster)` work. `Stop()` stops every running subscription (committing marked offsets) and closes every producer client. Defaults: acks=all, compression=snappy, offsetReset=latest, balancer=cooperativeSticky, autoCommit enable=true/5s. SASL plain/scram-sha-256/512 and TLS (incl. mTLS) supported; all built from the root franz-go module. Advanced needs (transactions, manual commits, seek, admin via `kadm`) are reachable via `Client.KgoClient()`/`Subscription.KgoClient()`. Integration tested against the in-memory `kfake` broker in `_example/kafka_test`.

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

The framework is functionally complete (~8,700 lines of library code + ~2,100 lines of tests across 78 Go files). Design docs are in `docs/` with detailed specs per phase (`00-overview.md` through `06-phase6-example.md`).

## Naming Conventions

- API methods follow Java Aifei naming: `GetStr`, `GetInt`, `GetBean`, `FindByID`, etc.
- `server.Register()` (in `register.go`) maps a service struct's exported methods to routes via two rules in `resolveRoute()`:
  - **Default actions (exact match)**, registered at the service prefix directly: `Paginate` → GET `/prefix`, `Create` → POST `/prefix`, `List` → GET `/prefix/list`.
  - **Verb prefixes**: method name must start with (and be longer than) one of `Get`/`Post`/`Put`/`Delete`/`Update`. The verb picks the HTTP method (`Get`→GET, `Post`→POST, `Put`→PUT, `Update`→PUT, `Delete`→DELETE); the remainder becomes the path suffix via camelCase→kebab-case (e.g. `GetProfile` → GET `/prefix/profile`, `PostApprove` → POST `/prefix/approve`).
  - Methods matching **neither** rule are skipped (not routed) — safe for private helpers. Note: there is NO prefix rule for `Save`/`Remove`/`Find`; `Create` is an exact-match default action only.
- `ById` in a method name becomes `:id`: `ById` → kebab `by-id` → path param `:id`, so `GetById` = GET `/prefix/:id`.
