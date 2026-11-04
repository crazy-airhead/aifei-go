---
name: upstream-sync
description: 分析上游仓库（Java aifei、solon、solon-ai、solon-flow）自上次核对以来的变更，逐项分类评估（已同步/建议引入/不适用/待评估），产出 docs/arch/upstream-sync/ 报告并推进基线。当用户要"同步上游"、"看看上游变了什么"、"评估上游某变更是否值得引入"时使用。
---

# 上游同步分析（aifei Java / solon 系）

对照上游仓库与 aifei-go 现状，产出**决策输入**报告：变更清单 + 逐项评级 + 建议引入排序。报告是给用户拍板的输入，不是拍板本身。

**产出物**（每次运行）：

1. `docs/arch/upstream-sync/YYYY-MM-DD.md` — 本次分析报告（发布到文档站，需同步侧边栏）
2. `docs/arch/upstream-sync/_state.md` — 基线推进（`_` 前缀被 srcExclude 排除，不发布）

## 上游仓库

基线 hash 一律读 `_state.md`，不要凭记忆。四个仓的远端/基线各有坑，见文末。

**镜像布局**：四个上游镜像统一在 `/Users/airhead/WorkSpace/aifei/upstream/`，镜像即本技能的专属工作区。

| 仓库 | 镜像路径 | 上游 ref | 对应 Go 模块 |
|------|----------|----------|--------------|
| aifei (Java) | `/Users/airhead/WorkSpace/aifei/upstream/aifei` | `origin/main`（jfinal 官方） | 见下方映射表 |
| solon | `/Users/airhead/WorkSpace/aifei/upstream/solon` | `origin/main` | 见下方映射表 |
| solon-ai | `/Users/airhead/WorkSpace/aifei/upstream/solon-ai` | `origin/main` | 见下方映射表 |
| solon-flow | `/Users/airhead/WorkSpace/aifei/upstream/solon-flow` | `origin/main` | `./flow` + `./plugins/flow` |

**观察仓**（默认不扫，用户点名才纳入——把条目加进 `_state.md` 即可）：
`goldsyear/damibus`（dami 起源）、`goldsyear/solon-plugins`。

## 子项目 → Go 模块映射

**aifei Java 模块**：core `aifei/`→`./aifei`+`./http`+`./server`；`aifei-enjoy`→`./enjoy`+`./db/sql`；`aifei-db`→`./db`+`./tools/generator`（类型映射/元数据联动）；`aifei-json`→`./json`；`aifei-log`→`./log`；`aifei-proxy`→不迁（Go 用 Handler 链/Interceptor，见 CLAUDE.md 设计决策表）；`aifei-undertow`→`./http`+`./server`。历史决定排除：`aifei-json-snack4`（json 模块已定 v2 内核方案，见 docs/arch/json-v2.md）。

**solon 主仓**：唯一移植面是 nami→`./nami`（2026-06-25 移植；⚠ 2026-10-14 上游重构把 nami 从 `solon-projects/nami` 挪到顶层 `nami-projects/`——圈范围用新路径，考古历史提交仍可用旧路径过滤）。其余子项目（solon-web/solon-data/solon-net/solon-serialization 等）是 Java aifei 核的对应物，**不逐项迁移**——只做概念级观察（重点：solon-net/remoting 形态、SnEL 演进）。仓库根另有 `UPDATE_LOG.md`（含 v1/v2/v3 分卷）。

**solon-ai 子项目**：`solon-ai-core`→`./ai`；`solon-ai-llm-dialects`→`./ai/dialect/*`；`solon-ai-agent`→`./ai/agent/{simple,react,team}`；`solon-ai-flow`→`./ai/flow`；`solon-ai-loop`→`./ai/loop`；`solon-ai-harness`→`./ai/harness`；`mcp-core`+`solon-ai-mcp`→`./mcp`+`./ai/mcp`；`solon-ai-rag-*`→`./ai/rag`+`./plugins/ai/ragloader`+`./plugins/ai/ragrepo`；`solon-ai-router`→`./ai/router`；`solon-ai-talents`→`./ai/talent`+`./plugins/ai/talent/*`；`solon-ai-ui`→`./ai/ui/*`；`solon-ai-sandbox`→`./sandbox`。**表外子目录 = 未迁移**：`acp-*`/`solon-ai-acp`/`solon-ai-anp`（Agent 通信/网络协议，新领域）、`__release`/`docs`/`*-parent`（非代码）。

## 流程

### 0. 解析参数

- 无参数 = 全部三个仓库
- `/upstream-sync aifei`（或 `solon-ai`、`solon-flow`）= 只扫指定仓库
- `/upstream-sync --since <tag|hash>` = 临时改基线重扫某区间（**不落盘**，不推进 `_state.md`）
- `/upstream-sync --full` = 忽略基线全量重扫（慎用；`_state.md` 缺基线的仓库首跑时也走这条，先问用户基线定在哪——宁可问不猜，solon-ai 可用 `v4.1.1` 迁移基线）

### 1. fetch 同步镜像

```bash
git -C <path> fetch --all --quiet
```

四个镜像仓是技能的专属工作区：可 fetch、可切分支、可快进合并（用户不会主动改上游，原工作副本不在分析路径上）；一律不 push、不改写历史。fetch 失败（离线）→ 用本地已有 refs 继续，报告头部标注"截至镜像最后 fetch"。

### 2. 圈定变更范围

```bash
# 总量与时间窗
git -C <path> log --oneline --no-merges <baseline>..<upstream> | wc -l
git -C <path> log --format='%h %ad %s' --date=short <baseline>..<upstream>
# 期间发布的版本（报告头部引用）
git -C <path> log --decorate --oneline <baseline>..<upstream> | grep 'tag: v'
# 某区域的体量
git -C <path> diff --stat <baseline>..<upstream> -- <dir>
```

solon-ai 与 solon 主仓都是 monorepo，先按子项目分组再逐组分析（solon 主仓子目录带 `solon-projects/` 前缀，唯 nami 自 2026-10-14 在顶层 `nami-projects/`；nami 面是重点，其余子项目只看总量）：

```bash
for d in <子项目目录...>; do echo "== $d: $(git -C <path> log --oneline <baseline>..<upstream> -- $d | wc -l | tr -d ' ') 提交"; done
```

同时读上游自带的 `UPDATE_LOG.md`（solon-ai 与 solon 仓库根都有，**新版本小节在上、旧在下**）：取基线版本小节之上的所有小节（含顶部未发布段），与 git log 双向核对——changelog 可能滞后或包含未合入内容，以 git 为准、以 changelog 校漏。

### 3. 逐主题分析

- 提交多时先按主题聚类（一个主题通常横跨多提交，参照 docs/arch/java-v1.1.0-sync.md 的归纳方式），`git show <hash>` / `git log -p <range> -- <dir>` 细读关键提交
- 每个主题对照 Go 侧现状：CLAUDE.md 模块表与架构段、`docs/arch/ai|flow/` 契约、代码本身
- 上游不兼容变更（UPDATE_LOG 会标注）即使不引入也要在报告中显式记录——它们改变"上游语义基准"

### 4. 分类评级

| 标记 | 含义 | 判据 |
|------|------|------|
| ✅ 已同步 | Go 侧有等价实现 | **必须指认到 Go 文件或契约章节**；指认不了就降级 ❓ |
| 🟢 建议引入 | 该跟进 | 修 Go 侧真实缺口/bug/安全，或高价值能力。标 P0（正确性/安全）/P1（重要能力）/P2（锦上添花），给 Go 落点模块 + 工作量粗估 |
| 🟡 值得注意 | 不引入但记录 | 方向信号、依赖前提未就绪、留待再评估 |
| ⚪ 不适用 | 明确不迁 | 语言/范式差异，或有意偏离（能引用 CLAUDE.md/docs 里的 deviation 说明则引用） |
| ❓ 待评估 | 拿不准 | 列出卡点，交用户拍板 |

### 5. 写报告 `docs/arch/upstream-sync/YYYY-MM-DD.md`

```markdown
# 上游同步评估 YYYY-MM-DD

> **核对日期**：YYYY-MM-DD
> **范围**：<仓库>（<基线短hash> → <新HEAD短hash>，N 提交，YYYY-MM-DD ~ YYYY-MM-DD）
> **方法**：git log/show 逐提交核对 + UPDATE_LOG 交叉 + Go 侧现状盘点
> **新基准**：aifei=<hash> / solon-ai=<hash> / solon-flow=<hash>

## 总览
| 仓库 | 提交数 | 时间窗 | 一句话主题 |

## 建议引入（按优先级）
| # | 优先级 | 变更 | 上游出处 | Go 落点 | 理由 | 粗估 |

## 分仓库详述
### <仓库>
#### <主题>
- 上游做了什么（提交号/文件）
- Go 侧现状
- 评级 + 理由

## 不引入但值得注意
## 待评估（需拍板）
```

站内引用其他文档用相对链接（如 `../java-v1.1.0-sync.md`）——死链会让 `pnpm docs:build` 直接失败。报告写完把条目加进 `docs/.vitepress/config.ts` 的 arch 侧边栏（沿用「Java v1.1.0 同步」条目的位置与格式）。

### 6. 推进基线并收尾

- 更新 `_state.md`：新 baseline = 本次分析的 `<upstream>` 实际 hash（`git -C <path> rev-parse <upstream>` 取全 hash 或固定 7 位短 hash）、`analyzed_at`、`last_report`
- `--since` 临时基线**不**写回
- 有 🟢 P0 项时提醒用户：落地时按 `docs/issues/NNNN-*.md` 建回归笔记、同步 CLAUDE.md 与 docs、发版走 scripts/tag.sh 锁步（见记忆 multi-module-tagging）

## 规则与坑

1. **aifei 镜像远端已归一为 `origin`=jfinal 官方**（2026-10-22；fork/cnb 远端移除，本地 main 重置到官方线）。分析范围 `<baseline>..origin/main`。历史教训保留：**基线绝不能取 fork 线上的提交**——曾把 fork 提交 b25c056 误记为基线（不在 jfinal main 历史里），后修正为 fork 并入点 v1.1.1（76bd72ca9）；fork 线只存在于原工作副本，镜像里没有。
2. **solon-flow 的移植源是 dev 冻结快照 `062cf2f`**（2026-03-22；dev 分支此后冻结，origin/HEAD 仍指 dev）——该提交是 origin/main 的祖先，基线因此保持 062cf2f 不变，分析范围 `062cf2f..origin/main` 自然覆盖 main 上移植后的全部变更；镜像在 main 分支（2026-10-22 起）。
3. solon-ai 出现**表外新顶层目录**时单列「新领域」小节，简述它是什么、与现有模块的关系，不急着评级。
4. 判断"已同步"最容易被骗：上游修的 bug 若 Go 实现路径不同（如 Java 异常→Go error 面）可能天然免疫，也可能 Go 侧有同源 bug——必须落到 Go 代码确认，不能只看提交标题。
5. 历史先例：`docs/arch/java-v1.1.0-sync.md`（2026-08-23，用户多轮"拍板/拍板修订"后才定稿）——评级和建议可以给，结论留给用户；拍板结果回写进报告（新增 `> **YYYY-MM-DD 拍板**` 行）。
6. 大仓库提交多时（aifei 上轮 110+ 提交归纳为 6 个主题），先聚类后细读；`--no-merges` 排掉合并提交，但 merge 提交里的冲突解决偶尔藏语义变化，主题对不上时回头查。
