# fallsurvivor 项目架构说明（大白话版）

> 这个项目的目标就一句话：**自动去各家公司的校招网站，把招聘岗位抓回来，整理好，帮你投递。**
>
> 它不是一个爬虫脚本，而是一套"会自己学习怎么抓"的系统：遇到没见过的招聘网站，它能自己打开浏览器、试着搜、找到岗位列表长啥样，然后把这个"抓取方法"记下来，下次直接用。

---

## 0. 先看一下整体长什么样

整个项目由**三个独立运行的程序**组成，它们之间通过网络互相喊话：

```
┌─────────────────────────┐
│  前端 Next.js (端口3000) │   你眼睛看的界面
└───────────┬─────────────┘
            │  发 HTTP 请求（/api/...）
            ▼
┌─────────────────────────┐
│  后端 Go  (端口 9090)    │   大脑：管业务逻辑、数据库、AI 决策
└──────┬───────────┬──────┘
       │           │
       │ ① 重活进队列 │ ② 直接喊话（带令牌）
       ▼            ▼
┌──────────────┐  ┌──────────────────────────┐
│ asynq 队列    │  │ Playwright Worker(端口8390)│
│ (Redis 中转)  │  │ 真正操控浏览器的"手"        │
└──────┬───────┘  └────────────┬─────────────┘
       │ 后台 worker 消费        │ 打开真实浏览器
       ▼                        ▼
  数据库 PostgreSQL        你手动登录过的招聘网站
```

三个程序的分工：

- **前端**：纯展示 + 点按钮。不干重活。
- **后端**：总指挥。决定"去哪抓、怎么抓、抓回来怎么存、怎么评分"。
- **浏览器 Worker**：后端的"手"。后端让它"打开这个网页""把这个表单填了""把这个页面转成文字"，它就照做。它跑在真实浏览器（Chromium）上，复用**你本人在浏览器里登录过的账号**（所以不需要在代码里写账号密码）。

---

## 1. 三个程序分别怎么启动

都在项目根目录，用 `make` 命令启动。配置（数据库地址、AI 密钥等）写在根目录的 `.env` 里。

| 程序 | 入口文件 | 启动命令 | 监听端口 |
|---|---|---|---|
| 后端 API | `backend/cmd/server/main.go` | `make server` | 9090（只本机可访问）|
| 后端后台 worker | `backend/cmd/worker/main.go` | `make worker` | 不直接对外，消费队列 |
| 数据库迁移/种子 | `backend/cmd/migrate/main.go` | `make migrate` / `make seed` | 不监听 |
| Markdown 抽取小工具 | `backend/cmd/mdextract/main.go` | `go run ./cmd/mdextract ./testdata/pages/` | 不监听，离线测试用 |
| 浏览器 Worker | `worker-browser/src/server.ts` | `make bw` | 8390（只本机可访问）|
| 前端 | `frontend/src/app/layout.tsx` + `app/page.tsx` | `make fe` | 3000 |

> 安全小知识：后端和浏览器 Worker 都只监听 `127.0.0.1`（本机回环），外面的机器连不进来。两边通信靠一个共享的令牌 `BROWSER_WORKER_TOKEN`（写在 `.env`），后端每次喊话都带着它（`X-Worker-Token` 请求头），Worker 核对上了才干活。

---

## 2. 后端内部结构（Go 在 `backend/internal/`）

后端是"大脑"，代码按职责切成一堆小包。逐个说人话：

- **`router/`**：路由表 + 装配车间。
  - `router.go` 里的 `BuildServices`（@:55）把所有零件拼到一起，包括把"探索 Agent"挂到发现服务上。
  - `New`（@:129）造出 Gin 引擎，挂上所有中间件（请求 ID、崩溃恢复、日志、安全响应头、CORS、请求体大小限制）。

- **`handler/`**：HTTP 接口层。**只做两件事**：检查参数合不合法、把活转交给对应的 service。不写业务逻辑。
  - `job.go` 岗位增删改查、`search.go` 触发搜索/爬虫、`explore.go` 探索、`site_recipe.go` Recipe 配置、`enrichment.go` JD 补全、`browser.go` 浏览器填表、`application.go` 投递状态、`profile.go` 画像、`llm_logs.go` AI 日志。

- **`service/search/`**：**最核心的业务层**，下面细说（第 4 节）。
  - `pipeline.go`：搜索流水线（总控）。
  - `discovery.go`：决定"走熟路还是探新路"。
  - `explorer.go`：自动探索 Agent。
  - `crawler.go` / `visual_crawler.go`：浏览器导航爬虫 `SiteCrawler`。
  - `normalizer.go` / `deduplicator.go` / `scorer.go`：URL 整理 / 四层去重 / 匹配打分。
  - `enrichment.go`：给摘要型岗位（如 BOSS）补全文 JD。

- **`ai/`**：所有调用大模型（DeepSeek）的地方都收口在这里，避免到处乱调。负责：探索规划、从网页文字里抽岗位、和你的画像做匹配、字段映射。

- **`executor/`**：按"抓取配方（Recipe）"去执行采集，产出原始岗位列表 `[]RawJob`。

- **`site/`**：站点 Recipe 注册表。把"某个已知站点该怎么抓"从硬编码 if/else 变成**数据**（数据库里一条记录就是一套抓法）。

- **`source/`**：岗位来源抽象。三种来源：`OfficialSource`（官方校招）、`BossSource`（BOSS 直聘）、`TavilySource`（底层搜索引擎，前两者拿来搜具体网址）。

- **`browser/`**：和浏览器 Worker 打交道的 HTTP 客户端 + 表单填写服务（让后端能指挥 Worker 帮你填投递表单）。

- **`repository/`**：**唯一**能碰数据库的地方（用 GORM）。别处想存数据都得走它。

- **`security/`**：敏感字段黑名单 + 脱敏（比如把看起来像银行卡号的串挡掉，带 Luhn 校验防误伤业务 ID）。

- **`middleware/`**：鉴权、限流、日志、安全头、请求体大小限制等横切逻辑。
- **`task/`**：asynq 任务定义与消费 handler。
- **`model/` `dto/`**：数据库模型和接口请求/响应结构。

根目录还有通用工具 `pkg/`（`safefetch` 安全外发请求、`response` 统一返回、`pagination` 分页、`logger` 日志）。

---

## 3. 浏览器 Worker 内部结构（`worker-browser/src/`）

它是后端的"手"，用 TypeScript + Playwright 写，自己起一个原生 HTTP 服务（`http.createServer`，没用框架）。

- **`server.ts`**：HTTP 服务 + 路由表（@:613）。所有能力都显式列出来：
  - 会话：`/session/open`、`/session/status`、`/session/close`（开/查/关一个浏览器会话）
  - 表单：`/form/extract`、`/form/fill`、`/form/highlight-submit`（抽取页面表单字段、填表、高亮提交按钮）
  - 页面：`/page/scrape`（抓整页）、`/page/extract-jobs`（按站点适配器抽岗位）、`/page/markdown`（把渲染后的正文转成 Markdown 给 AI 读）
  - 导航：`/nav/act`（执行一步点击/输入等动作）
  - 探索（只读）：`/explore/observe-start`、`/explore/observe-diff`、`/explore/inspect-request`、`/explore/snapshot`（让探索 Agent 能"看"页面和网络请求）

- **`common/`**：干活的具体实现：
  - `dom_serializer.ts`：把浏览器里渲染出来的 DOM 转成 Markdown（关键技巧：用 IIFE 字符串脚本、深度限制 200 层、只过滤隐藏元素、去重相邻重复行）。
  - `browser_manager.ts`：管浏览器生命周期；`closeSession` 会关掉底层浏览器并清理 Profile 锁文件（之前踩过的坑：不清理会导致一堆 Chromium 进程残留）。
  - `sensitive_guard.ts`：脱敏，带 Luhn 校验。
  - 网络观测、字段抽取、探索动作等模块。

- **`sites/`**：各招聘网站的适配器（告诉 Worker 这个站点的岗位列表/详情页长啥样）。

- **`.auth/<站点>/`**：你登录后的状态，权限 `0700`，**绝不入库**（已在 `.gitignore` 忽略）。

---

## 4. 最核心的设计：怎么"自动学会抓任意校招站"

这是整个项目最聪明的地方。逻辑在 `service/search/`。

### 两种抓法：熟路 vs 探新路

后端收到一个抓取请求后，先问自己：**"这个站点我以前抓过吗？"**

- **抓过（命中 Recipe）→ 走"熟路"（Fast Path）**
  `discovery.go` 的 `FastPath`(@:94)。直接按数据库里存好的配方去抓，又快又稳。已知的腾讯、字节就是这么走的。

- **没抓过 → 走"探新路"（Discovery Path）**
  `discovery.go` 的 `ExploreSite`(@:192) 调 `explorer.go` 的 `Explorer.Explore`(@:126)，让 AI 自己开着浏览器去摸索。

### 探索 Agent 怎么工作（大白话版）

`Explorer.Explore` 的流程：

1. 后端通过 `browserClient` 喊 Worker：
   - `/session/open` 开一个浏览器会话
   - `/nav/act` 打开校招网**根域名**（不是深层链接，避免 404）
   - 在搜索框输入关键词（比如"算法"）
   - 触发搜索，等列表出来
2. `/page/markdown` 把渲染后的列表页转成纯文字 Markdown（这样 AI 不用读乱七八糟的 HTML）。
3. `ai/` 层把 Markdown 切块（`markdown_chunk.go`）→ 用岗位领域 schema（`job_schema.go`）→ 做 schema 驱动抽取（`markdown_extract.go`），得到一排结构化岗位（标题/城市/薪资/截止日期…）。
4. **验证（关键！）**：`verifyWithObservedResponse` 拿"真实页面返回的数据能不能被解析成岗位"来判定成功。解析不出就失败。
5. **失败了就自修正**：把错误信息喂回模型，让它改方案再试，**最多 3 轮**。3 轮还不行就放弃，绝不存一条跑不通的配置。
6. **成功了且要求保存** → `saveExploredRecipe` 把这套抓法写进 `site_recipes` 表，并重载 Registry。下次这个站点就直接走"熟路"了。

### 探索循环的角色化（显式 agent 拓扑）

上面这套循环在代码里已经抽成 5 个显式角色，定义在 `explorer_agents.go`，装配在 `NewExplorer`。每个角色只做一件事，和 LangGraph 的范式一一对齐（实现仍留在 Go，无需换语言）：

| 角色 | 实现委托 | 对应 agent 范式 |
|---|---|---|
| `planner` | `decideWithRetry`（带输出截断重试） | **Planner + Reflection**：观测→决策，截断时精简观测重来 |
| `actor` | `executeAction` | **Actor**：执行单步动作（点击/输入/搜索/inspect） |
| `critic` | `progressHint` + 信心阈值 | **Critic**：停滞检测、进度评估、是否值得继续 |
| `guardrail` | 独立角色（见下） | **Guardrail**：执行前拦截/改写高风险与无意义决策 |
| `memory` | `loadPlaybook` / `recordPlaybookHits` + `exploration_playbook` 表 | **长期记忆**：召回经验、沉淀本次命中 |

`Explore` 的主循环读起来就是：`Memory.Recall → Planner.Decide → Guardrail.Check → Actor.Act → Critic.ProgressHint → Memory.Learn`。

**Guardrail 是唯一会改写/拒绝决策的角色**，它守住三条边界（与既有约束一致，不新增任何越权能力）：
- 重复 `inspect` 同一请求序号 → 拒绝并要求换动作（防死循环）；
- 候选已查完且有搜索框 → 改写为关键词搜索，触发新请求；
- 导航动作仅允许 http/https（由 Worker 侧强制）。

```
site.Registry  ──(命中)──▶ FastPath（数据驱动，已知站点，快）
     ▲
     │(保存已验证的配方)
     │
DiscoveryService.ExploreSite ──(未命中)──▶ Explorer（多角色 Agent 循环）
                                              │
                                              ├─ Memory.Recall  ── 长期记忆（Playbook 经验）
                                              ├─ Planner.Decide ── 观测→决策（截断自动重试=Reflection）
                                              ├─ Guardrail.Check ─ 拦截/改写高风险决策
                                              ├─ Actor.Act      ── 喊 Worker 执行单步
                                              ├─ Critic.Hint    ── 停滞/信心评估
                                              └─ Memory.Learn   ── 沉淀本次命中经验
                                              │
                                              └─ Worker: /session/open, /nav/act, /explore/*, /page/markdown
                                                  → dom_serializer → Markdown → ai 抽取 → 结构化岗位
                                                  → verifyWithObservedResponse（失败自修正 ≤3 轮）
```

> 设计意图一句话：**不用人肉给每个站点写适配代码**。未知站点让 AI 现场学，学成了沉淀成 Recipe，以后免学费。

> 想把这层探索 Agent 单独抽成 Python（LangGraph + browser-use）微服务，见 [`docs/AGENT_PYTHON_DESIGN.md`](./AGENT_PYTHON_DESIGN.md)。

---

## 5. 一条岗位从"网页"到"你能看到"的完整旅程

以"探索一个新校招站并落库展示"为例：

1. 你在前端点"抓取" → 前端 `POST /api/jobs/crawl`（或 `/api/explore`）。
2. 后端 `searchH.Crawl` 转发给 `DiscoveryService`。
3. `DiscoveryService` 查 `SiteRegistry`：
   - 命中 → `FastPath` 直接采。
   - 没命中 → `ExploreSite` → `Explorer.Explore`（见第 4 节）。
4. `Pipeline.run`（`pipeline.go` @:131）把抓回来的原始岗位 `[]RawJob`：
   - `normalizer` 整理 URL
   - `deduplicator` 四层去重（同一岗位不重复入库）
   - `scorer` 按你的画像打分（城市、岗位方向匹配度等）
   - `IngestRawJobs`(@:391) 写库 `jobs` 表。
5. 如果是 BOSS 这种只给摘要的，`enrichment.go` 再开你登录的浏览器去读完整 JD，补全后重算分。
6. 前端轮询 `/api/search-tasks/:id` 拿进度和结果，渲染成表格。

### 重的活怎么不卡住接口

全网搜索、JD 补全这种慢活，后端不直接在 HTTP 请求里做，而是：
- 丢进 **asynq 队列**（Redis 中转）
- 由 `cmd/worker` 后台慢慢消费（`task.NewHandler` 注册了 `Pipeline` 和 `Enrichment` 两类任务）
- 前端轮询任务状态，界面不转圈卡死。

---

## 6. 数据库（PostgreSQL + GORM 迁移）

- ORM 用 **GORM**；建表语句在 `backend/migrations/`（19 组 `.up.sql`/`.down.sql`，用 `embed.go` 内嵌进程序，`make migrate` 执行）。
- 主要表（按创建顺序，见 `001_init.up.sql` 与后续迁移）：

| 表 | 干啥用 |
|---|---|
| `user_profiles` | 你的求职画像（目标城市、方向、期望薪资等）|
| `job_profiles` | 岗位维度的画像/偏好 |
| `resumes` | 简历文件（文本、当前简历标记）|
| `jobs` | **核心**：抓回来的岗位（标题/公司/城市/薪资/JD/来源/评分…）|
| `job_sources` | 岗位来源记录 |
| `search_tasks` | 每次搜索任务的状态与进度 |
| `job_carts` | 岗位车（想投递先加车）|
| `applications` | 投递记录 + 状态机 |
| `application_profiles` | 可复用的申请信息（手机号、邮箱等）|
| `browser_tasks` | 浏览器辅助填写任务 |
| `application_fields` / `application_events` / `interviews` | 申请字段、事件流水、面试记录 |
| `site_recipes` | **探索沉淀**："某站点怎么抓"的配方（含健康度）|
| `recipe_runs` | 每条 Recipe 的验证运行记录 |
| `exploration_runs` | 探索 Agent 的运行轨迹（调试用）|
| `exploration_playbook` | 探索经验提示（通用套路，让 Agent 少走弯路）|

> 写库只有 `repository/` 能碰，别处想存都得走它，避免 SQL 满天飞。

---

## 7. 安全相关的几个设计点（顺带提一句）

- 登录态只在你本地浏览器/Worker 的 `.auth/`，不进代码不进库。
- 后端↔Worker 靠共享令牌校验，且都只监听本机。
- 所有外发请求走 `safefetch`（带开关的受控抓取）。
- 敏感字段脱敏（`security/` + Worker 的 `sensitive_guard`），带 Luhn 校验避免误伤业务 ID。
- 系统**刻意不提供"代你提交投递"的接口**——只标记"我已自行提交"，避免越权操作你的账号。

---

## 8. 想深入某一块，从哪读起

| 你想搞懂 | 先读 |
|---|---|
| 整体怎么拼起来 | `backend/internal/router/router.go` 的 `BuildServices`(@:55) |
| 一次搜索的全流程 | `backend/internal/service/search/pipeline.go` |
| 怎么自动学新站点 | `backend/internal/service/search/explorer.go` → `discovery.go` 的 `ExploreSite`(@:192) |
| 浏览器到底干了啥 | `worker-browser/src/server.ts` 路由表(@:613) + `common/dom_serializer.ts` |
| 抓回来怎么打分去重 | `service/search/` 下的 `scorer.go` / `deduplicator.go` / `normalizer.go` |
| 数据库长啥样 | `backend/migrations/001_init.up.sql` |
