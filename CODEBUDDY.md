# CODEBUDDY.md This file provides guidance to CodeBuddy when working with code in this repository.

秋招 OS（fallsurvivor）是一个服务个人秋招的轻量级求职管理平台：自动发现岗位、整理、辅助填写申请表、跟踪进度。它**不是**无人值守的自动投递机器人——系统刻意不点击最终提交按钮，不填写/存储敏感信息，不绕过验证码与风控，登录态只留本地。

## 常用命令

后端配置与依赖：
```bash
make env          # 从 .env.example 生成 .env（不覆盖已有文件）
make deps         # 拉取后端 Go 依赖 (go mod tidy)
make bootstrap    # 首次初始化：env + deps + fe-install + bw-install
```

基础设施与数据库：
```bash
make infra        # 启动 PostgreSQL + Redis（优先 Docker，否则 Homebrew）
make migrate      # 执行数据库迁移（embed 进程序的 .up.sql）
make migrate-down # 回滚最后一个迁移
make seed         # 写入默认用户与求职画像（幂等）
```

本地运行（四个独立进程）：
```bash
make server   # 后端 API → http://127.0.0.1:9090
make worker   # 异步任务消费者（asynq）
make fe       # 前端 Next.js → http://localhost:3000
make bw       # Playwright Worker → http://127.0.0.1:8390（首次需 make bw-install）
```

构建与测试：
```bash
make build   # 编译后端二进制到 backend/bin
make test    # 运行后端全部单测：cd backend && go test ./... -count=1
make vet     # go vet 静态检查
make up      # docker compose 启动所有容器（Worker 刻意不进容器）
```
运行单个后端测试：
```bash
cd backend && go test ./internal/service/application/... -run TestNoAutoSubmitPath -count=1 -v
```
前端命令：
```bash
cd frontend && npm run dev      # 开发服务器
cd frontend && npm run build    # 生产构建（含类型检查）
cd frontend && npm run lint     # ESLint
```

环境变量从根目录 `.env` 读取；已在 shell 中 `export` 的变量优先于 `.env`。密钥（DEEPSEEK/QWEN/TAVILY/BROWSER_WORKER_TOKEN）留空时系统降级为纯规则模式，仍可运行。生产环境必须设置 `BROWSER_WORKER_TOKEN`。

## 高层架构

系统由**三个独立运行的程序**通过网络协作：前端 Next.js（:3000，只做展示与点击）→ 后端 Go（:9090，大脑）→ 浏览器 Worker（:8390，后端的"手"，用真实 Chromium 复用你本人在浏览器登录过的账号）。后端与 Worker 都只监听 `127.0.0.1`，靠共享令牌 `BROWSER_WORKER_TOKEN`（`X-Worker-Token` 头）互相校验。慢活（全网搜索、JD 补全）不直接在 HTTP 请求里做，而是丢进 **asynq 队列**（Redis 中转）由 `cmd/worker` 后台消费，前端轮询任务状态。

### 后端内部结构（`backend/internal/`）

- **`router/`** — 路由表 + 装配车间。`BuildServices`（router.go:58）把全部零件拼起来，包括把 Explorer / Option-B Python Agent 挂到 Discovery 服务上；`New` 造 Gin 引擎并挂中间件（请求 ID、崩溃恢复、日志、安全响应头、CORS、请求体 1 MiB 限制、限流）。
- **`handler/`** — HTTP 层，只做参数校验与转发，不含业务逻辑。
- **`service/search/`** — 最核心业务层：
  - `pipeline.go` 流水线总控（normalizer 整理 URL → deduplicator 四层去重 → scorer 按画像打分 → 写库）。
  - `discovery.go` 决定"走熟路还是探新路"。
  - `explorer.go` + `explorer_agents.go` 自动探索未知站点的 Agent。
  - `crawler.go` 浏览器导航爬虫 `SiteCrawler`；`enrichment.go` 为摘要型岗位（BOSS）在已登录浏览器上补全完整 JD 并重算分。
- **`ai/`** — 所有 LLM（千问/DeepSeek，OpenAI 兼容协议）调用收口于此：探索规划、网页岗位抽取、画像匹配、字段映射。LLM 不可用则退化为模板/规则。
- **`site/`** — 站点 Recipe 注册表（`site_recipes` 表），把"已知站点怎么抓"从硬编码 if/else 变成数据。
- **`source/`** — 岗位来源抽象：`OfficialSource` / `BossSource`（复用 `TavilySource` 做底层联网搜索）。
- **`browser/`** — 与 Playwright Worker 打交道的 HTTP 客户端 + 表单填写服务。
- **`repository/`** — **唯一**能碰 PostgreSQL（GORM）的地方。
- **`security/`** — 敏感字段黑名单 + 脱敏（带 Luhn 校验防误伤业务 ID）。
- **`task/`** — asynq 任务定义与消费 handler（注册了 `Pipeline` 与 `Enrichment` 两类任务）。
- 根 `pkg/`：`safefetch`（带开关的受控外发抓取，SSRF 防护）、`response`、`pagination`、`logger`。

### 核心设计：自动学会抓任意校招站

收到抓取请求先问"这个站点抓过吗？"
- **命中 Recipe → Fast Path**（`discovery.go` 的 `FastPath`）：直接按数据驱动配方采集，又快又稳（腾讯、字节预置）。
- **未命中 → Discovery Path**（`ExploreSite` → `Explorer.Explore`）：让 AI 开着浏览器现场摸索，成功且要求保存则写进 `site_recipes` 并重载 Registry，下次走熟路。

探索循环在 `explorer_agents.go` 抽成 5 个显式角色（实现仍在 Go，与 LangGraph 范式对齐）：`Memory.Recall → Planner.Decide（带截断重试=Reflection）→ Guardrail.Check（唯一可改写/拒绝决策）→ Actor.Act → Critic.ProgressHint → Memory.Learn`。验证用真实页面能否被解析成岗位判定成功，失败自修正最多 3 轮，仍不行就放弃且不存储跑不通的配置。Guardrail 守三条边界：拒绝重复 inspect 同一请求、候选查完改写为搜索、导航仅允许 http/https。

> 想把探索层单独抽成 Python 微服务（LangGraph + browser-use），见 `docs/AGENT_PYTHON_DESIGN.md`。当前由 `AGENT_EXPLORER_URL`（非空即装配客户端）+ `USE_PYTHON_AGENT=true`（默认 false，走 Go 内置 Explorer，可一键回退）灰度。

### 浏览器 Worker（`worker-browser/src/`，TypeScript + Playwright）

原生 HTTP 服务（`http.createServer`，无框架）。路由表（`server.ts`）暴露：会话 `/session/*`、表单 `/form/extract|fill|highlight-submit`（抽取/填表/高亮提交按钮——**只高亮不点击**）、页面 `/page/scrape|extract-jobs|markdown`、`/nav/act` 单步动作、`/explore/*` 只读观察（observe-diff / inspect-request / snapshot）。关键技巧 `common/dom_serializer.ts` 用 IIFE 脚本把渲染后的 DOM 转成 Markdown 喂给 AI。`common/browser_manager.ts` 关会话时清理 Profile 锁文件（否则残留 Chromium 进程）；`sensitive_guard.ts` 脱敏。登录态存 `.auth/<站点>/`（权限 0700，绝不入库，已 gitignore）。

### 投递状态机（硬性安全约束）

`service/application/state_machine.go` 定义状态跃迁，**禁止**在 handler 或 repository 直接写状态。图中**不存在任何"系统自动提交"路径**：`READY_TO_SUBMIT → SUBMITTED` 只能由用户在招聘网站真实提交后主动调用 `mark-submitted`（`/applications/:id/mark-submitted`）触发。改动状态机或 Worker 时绝不能引入自动点击提交的路径，相关约束由 `state_machine_test.go` 的 `TestNoAutoSubmitPath` 守护。

### 数据库（PostgreSQL + GORM 迁移）

迁移在 `backend/migrations/`（`.up.sql`/`.down.sql` 由 `embed.go` 内嵌，`make migrate` 执行）。核心表：`jobs`（抓回的标准化岗位）、`job_sources`（多来源记录）、`search_tasks`、`job_carts`、`applications`（状态机）、`browser_tasks`、`site_recipes`（探索沉淀）、`recipe_runs`、`exploration_runs`、`exploration_playbook`（探索经验）。写库只经 `repository/`。

## 关键安全边界（改动时务必保持）

- 系统不点击最终提交按钮；Worker 对提交按钮只滚动高亮。
- 敏感信息不填写、不存储（`security/sensitive.go` 黑名单 + 数据库 `NOT (is_sensitive AND is_filled)` 约束 + Worker 再校验）。
- 验证码/风控即停止并交回用户，任务标记 `BLOCKED`。
- 登录态只留本地 `.auth/`，不入库、不进日志、不发给 AI。
- 密钥只在后端环境变量，前端只有 `NEXT_PUBLIC_API_BASE_URL`。
- 所有外发请求走 `safefetch`（SSRF 防护：拒绝内网地址与非标端口），`AllowOutboundFetch=false` 时仅用搜索 snippet。

## 服从 CLAUDE.md 行为准则

本仓库 `CLAUDE.md` 规定了偏谨慎的编码准则，**改动代码时务必遵守**：动手前先陈述假设/作用域（触及 >3 文件或改 schema/API 契约要先说明为何小修不行）；最小改动、不要顺手重构无关代码；改动前先列 blast radius；切换方法要说明具体"怎么做"与"可能怎么坏"；用目标驱动（先写复现测试再修）；小修优先止血，根因修复留给用户决定。

## 想深入某块，从哪读起

- 整体装配：`backend/internal/router/router.go` 的 `BuildServices`
- 搜索全流程：`backend/internal/service/search/pipeline.go`
- 自动学新站点：`explorer.go` → `discovery.go` 的 `ExploreSite`
- 浏览器实际行为：`worker-browser/src/server.ts` 路由表 + `common/dom_serializer.ts`
- 打分去重：`service/search/` 下 `scorer.go` / `deduplicator.go` / `normalizer.go`
- 数据库：`backend/migrations/001_init.up.sql`
