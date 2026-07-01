# xxljob Plugin

xxl-job executor plugin for aifei-go. A source-level port of [xxl-job-executor-go](https://github.com/xxl-job/xxl-job-executor-go), adapted to use aifei's config, log, and plugin systems.

## Usage

```yaml
# app.yml
xxljob:
  serverAddr: "http://127.0.0.1:8080/xxl-job-admin"
  registryKey: "my-app-jobs"
```

```go
package main

import (
    "context"
    "os"

    "github.com/crazy-airhead/aifei-go/aifei"
    "github.com/crazy-airhead/aifei-go/config"
    "github.com/crazy-airhead/aifei-go/plugins/xxljob"
    "github.com/crazy-airhead/aifei-go/server"
)

func main() {
    config.Init(os.Args)

    p, _ := xxljob.NewPlugin(nil)

    // Register task handlers
    p.RegTask("myHandler", func(ctx context.Context, param *xxljob.RunReq) string {
        // Execute your task logic here
        return "done"
    })

    app := aifei.New(aifei.WithPlugin(p))
    server.Run(app, ":8080")
}
```

## Configuration

| Key | Type | Default | Description |
|---|---|---|---|
| `xxljob.serverAddr` | string | — | Scheduling center URL (required) |
| `xxljob.accessToken` | string | — | API access token |
| `xxljob.executorIp` | string | auto-detect | Local executor IP |
| `xxljob.executorPort` | string | `9999` | Local executor port |
| `xxljob.registryKey` | string | `golang-jobs` | Executor name registered in xxl-job-admin |
| `xxljob.timeout` | duration | — | HTTP timeout for scheduler calls (e.g. `5s`) |
| `xxljob.timeoutMs` | int | — | HTTP timeout in milliseconds |
| `xxljob.logDir` | string | — | Log directory |
