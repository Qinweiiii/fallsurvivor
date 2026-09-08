# 探索 Agent 抽成 Python 微服务（Option B）落地设计

> 配套文档：[`ARCHITECTURE.md`](./ARCHITECTURE.md) 第 4 节（Go 内的角色化探索循环）。
>
> 目标：把"会自己学抓任意校招站"的那层 Agent（约 5–6k 行 Go + 一部分 TS Worker）抽成一个
> **独立的 Python 服务**，用 **LangGraph** 做多角色编排、用 **browser-use** 直接控浏览器、
> 用向量库做长期记忆。Go 后端原样保留，只多调一个 HTTP 接口。
>
> 顺带好处：browser-use 是 Python，能**直接替掉现在最重的 TS `worker-browser` 探索半边**
> （`/explore/*`、`/nav/act`、`/page/markdown`、`dom_serializer`、网络观测），呼应"代码太繁重"的诉求。

---

## 1. 为什么要这么做（一句话）

真正"agent"的代码只占后端 ~5–6k 行，换 Python 重写它收益最大、风险最小；
剩下 ~15k 行 Go 是纯业务逻辑（jobs CRUD、投递状态机、评分去重、GORM、队列），跟 agent 无关，
**绝不碰**。所以方案是"抽一层微服务"，不是"整库重写"。

---

## 2. 新的整体拓扑

```
前端 (3000)
   │  /api/jobs/crawl, /api/explore
   ▼
后端 Go (9090)  ──DiscoveryService.ExploreSite──▶  Python Agent 服务 (e.g. 8400)
   │  (保留 FastPath / 验证落库 / pipeline)              │ LangGraph: Guardrail→Planner→Actor(browser-use)→Critic→Memory
   │                                                    │ 自带浏览器控制，复用你登录过的 profile
   │  cmd/worker (asynq) 仍跑重活                       ▼
   └──▶ Playwright Worker (8390)  ── 仅保留：/form/fill（投递辅助）、/page/scrape（JD 补全）
```

要点：
- **Go 后端**：`ExploreSite` 变薄，只负责"调 Python 拿 RecipeCandidate → 验证落库 → 重载 Registry"。`FastPath`、pipeline、enrichment、投递状态机全部不动。
- **Python 服务**：吃下整个探索循环（含 verify 自修正 ≤3 轮），返回已验证的 `RecipeCandidate` + 轨迹。
- **TS Worker**：探索相关路由删除，只留"填表辅助"和"JD 抓取补全"两件和 agent 无关、且依赖你登录态的活。

---

## 3. 目录结构（新建 `agent-explorer/`）

```
agent-explorer/
├── pyproject.toml            # 依赖：langgraph, langchain, browser-use, fastapi, uvicorn,
│                            #       playwright, chromadb(或 pgvector), pydantic
├── .env                      # DEEPSEEK_API_KEY, BROWSER_WORKER_TOKEN(双向校验), PROFILE_DIR
├── app/
│   ├── main.py               # FastAPI：/health, /explore
│   ├── config.py             # 读 .env
│   ├── graph/
│   │   ├── state.py          # ExploreState(TypedDict)：observation/decision/trace/recipe/step
│   │   ├── guardrail.py      # 节点：执行前拦截/改写（重复 inspect、越界、优先搜词）
│   │   ├── planner.py        # 节点：观测→产出 browser-use 任务描述（含截断重试=Reflection）
│   │   ├── actor.py          # 节点：启动 browser-use Agent 执行单步，回传结果
│   │   ├── critic.py         # 节点：停滞检测、信心阈值、是否继续
│   │   └── memory.py         # 节点：Recall 读 Playbook；Learn 沉淀命中（向量库）
│   ├── browser/
│   │   ├── controller.py     # browser-use Controller + 自定义 action（observe requests / diff）
│   │   └── profile.py        # 加载你登录过的 Chromium profile（与 TS Worker 共用 .auth/）
│   ├── schemas/
│   │   └── recipe.py         # RecipeCandidate / ExploreRequestView 的 pydantic 模型
│   │                       #   —— 必须与 Go 侧 site.Repository.SaveRecipeCandidate 的 JSON 一致
│   └── prompts/
│       ├── planner.md        # 任务规划提示（含成本意识、先验证搜索路径）
│       ├── critic.md         # 进度评估提示
│       └── guardrail.md      # 风险拦截提示
└── tests/
    └── test_offline.py       # 喂 backend/testdata/pages/ 的 5 份样本做回归（不联网）
```

---

## 4. 与 Go 后端的 HTTP 契约

### 请求：`POST /explore`

```json
{
  "site": "kuaishou",
  "base_url": "https://www.kuaishou.com/campus",
  "keyword": "算法",
  "save": true
}
```

### 响应（200）：已验证通过的 RecipeCandidate + 轨迹

```json
{
  "status": "success",
  "recipe": {
    "site": "kuaishou",
    "keyword": "算法",
    "requests": [
      {
        "method": "GET",
        "url_pattern": "*/position/**/simple*",
        "list_locator": "...",
        "item_hint": "..."
      }
    ],
    "playbook_hits": ["先验证关键词搜索路径", "列表接口带 keyword 参数"]
  },
  "trace": [
    {"step": 1, "role": "planner", "action": "navigate", "...": "..."},
    {"step": 2, "role": "actor",   "action": "search",  "...": "..."},
    {"step": 3, "role": "critic",  "verdict": "continue", "...": "..."},
    {"step": 4, "role": "guardrail","decision": "allow", "...": "..."}
  ]
}
```

> `recipe` 字段的 JSON 形状**逐字段对齐** Go 的 `ai.RecipeCandidate`（`backend/internal/ai/recipe_types.go` 之类）。
> 这是两边唯一要约定死的东西，建议单独写一份 `schemas/recipe.json` 两边共用（Go 用 `jsonschema` 校验，Python 用 pydantic）。

### 错误
- `401`：令牌不符（双向校验失败）。
- `422`：`recipe` 不合法（缺必填字段）。
- `500`：探索失败（含 3 轮自修正后仍无法解析出岗位）→ Go 侧按"探索失败"处理，不落库。

---

## 5. LangGraph 编排（大白话）

状态图（`state.py` 里的 `ExploreState` 在节点间传递）：

```
      ┌─────────────────────────────────────────────────────┐
      │                      START                            │
      └───────────────────────┬─────────────────────────────┘
                              ▼
                    ① Memory.Recall（读 Playbook 经验注入 observation）
                              ▼
                    ② Guardrail.Check（拦截/改写高风险决策）
                              ▼
                    ③ Planner.Decide（产出 browser-use 任务；输出截断则精简重试=Reflection）
                              ▼
                    ④ Actor.Act（browser-use 开浏览器执行单步，回传结果）
                              ▼
                    ⑤ Critic.Hint（停滞？信心够？继续/提前结束/放弃）
                       │            │              │
                       ▼            ▼              ▼
                    continue      finish         abort
                       │            │              │
                       └─────▶ ⑥ Memory.Learn（沉淀命中）──▶ 返回 RecipeCandidate
                       ▲
                       └──────────── 回到 ①（下一步）
```

- **browser-use 在这里的角色**：它是"内层"的 Actor+Planner——负责把"打开这个页、点那个按钮"真正在浏览器里跑起来。
  我们的 LangGraph 是"外层"编排：在它之前用 Guardrail 拦风险，在它之后用 Critic 评估，
  用 Memory 注入/沉淀经验，并用 verify 自修正循环包住它（解析不出岗位就重来，≤3 轮）。
- **为什么不直接全用 browser-use**：browser-use 自己就会跑完整个任务，但我们要的是
  "可拦截、可审计、可沉淀经验、失败可自修正"的受控探索——所以把它当可控工具，外层用 LangGraph 兜住。

---

## 6. 登录态复用（关键）

你手动登录的 Chromium profile 现在在 `worker-browser/.auth/<site>/`。Python 侧让 browser-use
用同一份 profile：

- **方案 A（推荐）**：`browser/profile.py` 把 `user_data_dir` 指向 `worker-browser/.auth/<site>/`，
  browser-use 底层 Playwright 直接复用，无需重新登录。
- **方案 B**：Python 不自己开浏览器，而是连接 TS Worker 已经开好的 CDP session（Worker 留一个
  `/session/cdp` 返回 `wsEndpoint`，Python `browser-use` 用 `CDP` 连接）。耦合更紧，前期不推荐。

> 无论哪种，登录态都只在本机、不进库、不进代码（已 `.gitignore`）。

---

## 7. 增量落地步骤（不破坏现有功能）

1. **建服务**：`agent-explorer/` 跑通 `POST /explore`，先针对 **离线样本**（`backend/testdata/pages/` 那 5 份 Markdown）做回归，验证能产出合法 `RecipeCandidate`。不联网、不碰生产。
2. **接边界**：Go 新增 `agent.NewClient`（仿照现有 `browser.Client` 的 HTTP 封装），`ExploreSite` 在 `USE_PYTHON_AGENT=true` 时改调 Python；默认关，回退到现有 Go `Explorer`。**双实现并存，随时可切回。**
3. **真机验证**：开真实浏览器跑 1–2 个未知站点，对比 Go 版与 Python 版产出的 Recipe 是否一致。
4. **稳定后删代码**：确认 Python 版稳了，删除 `explorer.go` + `explorer_agents.go` + TS Worker 的 `/explore/*`、`/nav/act`、`/page/markdown`、`dom_serializer`、网络观测模块，并把这些路由从 `server.ts` 路由表(@:613)移除。
5. **减重**：此时 TS Worker 只剩 `/form/fill` 与 `/page/scrape`，文件数从 ~781 砍到个位数级别。

---

## 8. 风险与回滚

| 风险 | 缓解 |
|---|---|
| Python 服务挂了影响探索 | `USE_PYTHON_AGENT` 开关一键回退 Go 版；Go 版代码在第 4 步前一直保留 |
| `recipe` 形状两边不一致 | 共用 `schemas/recipe.json`，Go 侧 `jsonschema` 校验入参 |
| browser-use 版本变动行为漂移 | 锁版本 + 离线样本回归测试守住产出 |
| 登录态失效 | `profile.py` 复用 `.auth/`；失效时退回手动登录一次 |

---

## 9. 收益小结

- **简历话术**：直接吃 LangGraph 多角色 + browser-use + 向量记忆，和航班问答项目同款范式，且更独特（真实浏览器控招聘站）。
- **代码减重**：删掉最重的 TS 探索半边 + Go `Explorer`，呼应"太繁重"的痛点。
- **零功能回归**：第 4 步前双实现并存，可灰度切换。
- **不碰业务**：15k 行 Go 业务逻辑一行不动。

> 注：本文件是**设计**，不是实现。落地按第 7 节五步走，每步可独立验证。
