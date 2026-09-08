# fallsurvivor agent-explorer（Python 探索 Agent 微服务）

把"会自己学抓任意校招站"的探索层从 Go 后端独立出来，用 **LangGraph** 做多角色编排、
**browser-use** 控浏览器。对应设计文档：`docs/AGENT_PYTHON_DESIGN.md`。

## 当前进度

- ✅ 骨架已落地：`FastAPI` + `LangGraph` 五角色循环（guardrail → planner → actor → critic → memory）。
- ✅ **离线模式**（`OFFLINE=true`，默认）：不联网、不调浏览器、不碰生产，只对
  `backend/testdata/pages/` 的 5 份样本做回归，产出符合 Go `RecipeCandidate` 契约的配置。
- ✅ **生产路径已写好并已真机跑通**：`planner.py` 调 DeepSeek（OpenAI 兼容）决策；
  `actor.py` + `browser/controller.py` 用 **browser-use** 全自主探索站点、复用你登录过的
  profile、回传真实 `RecipeCandidate`；`memory.py` 预留向量库接口。
  - 历史备注（已解决）：本环境曾无法构建 browser-use 依赖（pydantic-core wheel 失败），
    生产路径最初仅按 browser-use 0.1.x 公开 API 编写；现依赖已可安装，生产路径已实测通过
    （快手校招 `POST /api/v1/open/positions/simple`，`status=success`、`verified=true`、约 3 万 input tokens/run）。
- ⏳ 待做：Memory 接向量库；verify 自修正循环（解析不出岗位则重来 ≤3 轮）；多步并行候选探索。

## 运行

```bash
cd agent-explorer
python -m venv .venv && source .venv/bin/activate
pip install -e ".[dev]"        # 含 fastapi/uvicorn/pydantic/langgraph/httpx/pytest
cp .env.example .env           # 默认 OFFLINE=true

# 跑离线回归（无需 key、无需网络）
pytest -q
# 或单文件运行
python tests/test_offline.py

# 起服务（offline 演示）
uvicorn app.main:app --port 8400
curl -X POST localhost:8400/explore -H 'content-type: application/json' \
  -d '{"site":"kuaishou","base_url":"https://www.kuaishou.com/campus","keyword":"算法"}'
```

## 真机验证（production：browser-use 真跑）

> 本沙箱无法构建 browser-use 依赖，以下在你本机装好依赖后执行。

```bash
cd agent-explorer && source .venv/bin/activate
pip install "browser-use>=0.1,<0.2" langchain-openai
cp .env.example .env
# 在 .env 中：OFFLINE=false、填 DEEPSEEK_API_KEY、BROWSER_WORKER_TOKEN（与 Go 后端一致）
# PROFILE_DIR 指向你手动登录过的 worker-browser/.auth（如 ../worker-browser/.auth）

# 单元级真机（browser-use 开浏览器、复用 profile、回传 record_recipe）
PYTHONPATH=. python tests/test_production_skip.py

# 端到端（经 FastAPI，验证与 Go 后端契约一致）
uvicorn app.main:app --port 8400
curl -X POST localhost:8400/explore -H 'content-type: application/json' \
  -H "X-Worker-Token: $BROWSER_WORKER_TOKEN" \
  -d '{"site":"kuaishou","base_url":"https://www.kuaishou.com/campus","keyword":"算法"}'

# 与 Go 内置 Explorer 对拍（需另起 make server + make bw，见 scripts/compare_recipes.sh）
./scripts/compare_recipes.sh https://www.kuaishou.com/campus 算法 kuaishou
```

## 与 Go 后端的契约

`POST /explore` 请求：`{site, base_url, keyword, save}`。
响应：`{status, recipe: RecipeCandidate, trace: [...], reason, verified, verify_result}`。
`RecipeCandidate` 字段逐字段对齐 `backend/internal/ai/explorer.go`，保证两端一致。
`verified: bool` 与 `verify_result: {ok, jobs_found, sample_titles, error}` 由 `critic.py`
在验证节点算出并回传——**Go 端会据 `verify_result.jobs_found` 把 `site_recipes.verified_jobs`
写成真实验证条数**（早期版本 Go 侧硬编码 `JobsFound:0`，导致落库后 `verified_jobs` 恒为 0，
要等首次 Fast Path 执行才由 `MarkSuccess` 覆盖）。坑 4 详述。
Go 侧 `ExploreSite` 在 `USE_PYTHON_AGENT=true` 时改调本服务（见设计文档第 7 节）。

## 生产路径真机验证记录（排坑备忘）

生产路径（`OFFLINE=false`，browser-use 真跑）已实测跑通。以下坑已在代码中修复并验证，
记录在此避免重复踩：

### 坑 1：`ExploreState` 漏声明 channel → 验证结果/失败原因被 LangGraph 丢弃
- **现象**：响应顶层 `verified` 恒为 `false`、`verify_result` 恒为 `null`、`reason` 恒为空，
  即使 `critic` 日志里明明 `verify ok=True jobs=10`。
- **根因**：`critic.py` 写 `state["verified"]` / `state["verify_result"]`、`actor.py` 写
  `state["abort_reason"]`，但这三个键未在 `app/graph/state.py` 的 `ExploreState` 中声明。
  LangGraph 按 schema 合并状态时**直接丢弃未知键**，所以节点写入的值从没回传到 `main.py` 的响应。
- **修复**：在 `ExploreState` 中补声明 `verified: bool`、`verify_result: dict`、`abort_reason: str`。
- **验证**：零 token 的 LangGraph 状态合并微测试（`StateGraph` 单节点写三键再 `invoke`）确认三者均回传。

### 坑 2：浏览器单例 `Event loop is closed` → browser-use 无限重试空转烧 token
- **现象**：第一个 `/explore` 成功后，后续 run 一直停在 `Step 1`，server 日志反复刷
  `Failed to create new browser session: Browser.new_context: Event loop is closed`，
  看似卡死，实际每个重试都消耗一次 LLM 调用。
- **根因**：`app/browser/controller.py` 用模块级单例 `_BROWSER` 跨轮复用浏览器。
  `run_full_explore` 用 `asyncio.run(agent.run())` 跑探索，`asyncio.run` 返回后会**关闭它创建的
  临时事件循环**；该 `Browser` 句柄绑在这个已关闭的 loop 上，第一轮能跑通但跑完即变僵尸。
  下一轮 `_ensure_browser` 看到 `_BROWSER is not None` 就复用僵尸句柄 → 报 `Event loop is closed`
  → browser-use 捕获异常后无限重启 agent 重试。
- **修复**：把 `asyncio.run(agent.run())` 包进一个在**存活 loop 内**同时关闭浏览器的协程，并在
  `finally` 中 `global _BROWSER; _BROWSER = None` 作废单例，让下一轮 `_ensure_browser` 重建新浏览器。
  在 loop 内关闭还能释放 `user_data_dir` 锁，否则残留浏览器进程会锁住 profile 目录，让下轮新浏览器也起不来。
- **验证**：修复后第二个 run 从 `Step 1` 正常推进到 `Step 8` 并 `Task completed`，`Event loop is closed`
  计数恒为 0；`/tmp/ae_listcaptured.log` 正常生成（此前因 `captured["responses"]` 空而从不生成）。

### 坑 3：`captured` 初始化漏 `"responses"` 键 → `list_captured` 恒空
- **现象**：`on_response` 监听器确实在跑（调试日志 `/tmp/ae_capture.log` 有 `positions/simple`），
  但 `list_captured` 永远返回"尚未捕获到任何响应"，`/tmp/ae_listcaptured.log` 从不生成。
- **根因**：`captured` 初始只建了 `{"requests": []}`，而 `on_response` 向 `captured["responses"]` 追加，
  该键不存在 → `KeyError`，且回调外层 `except Exception: pass` 把异常静默吞掉，响应从没进 dict。
- **修复**：初始化改为 `{"requests": [], "responses": []}`；并让 `on_response` 的 `except` 把异常写进
  调试日志，不再掩盖错误。

### 坑 4：Go 侧未解析 `verify_result` → 落库 `verified_jobs` 恒为 0
- **现象**：经 Python Agent 探索保存的 recipe，`verify_status` 是 `verified`、但 `verified_jobs` 永远
  是 `0`，前端配置页「验证时采到 N 条」显示 0，尽管 Python `critic` 日志明明 `jobs=10`。
- **根因**：Python `/explore` 实际在响应里回了 `verify_result: {ok: true, jobs_found: 10, ...}`，
  但 Go 侧 `agent.exploreResponse` 结构体只解析 `status/recipe/trace/reason`，**没声明 `verified` /
  `verify_result` 字段** → JSON 解码时这两个字段被丢弃；`agent_bridge.go` 只得合成
  `executor.VerifyResult{OK: true, JobsFound: 0}`，于是 `site_recipes.verified_jobs` 写死 0。
  （注意：Go 内置 Explorer 路径用的是 `verifyWithObservedResponse` 在探索时就地解析真实响应，
  所以那条路径 `verified_jobs` 落库即准确——差异只在 Python 路径把计数丢了。）
- **修复**（Go 侧，3 文件）：
  - `agent/types.go`：`exploreResponse` 增 `Verified bool` + `VerifyResult{OK,JobsFound,SampleTitles,Error}`；
    新增 `ExploreOutcome` 承载验证结论。
  - `agent/client.go`：`Explore` 由 `(candidate, trace, error)` 改为返回 `*ExploreOutcome`。
  - `agent_bridge.go`：落库改用 `outcome.Verified` / `outcome.JobsFound` / `outcome.SampleTitles`，
    去掉硬编码 `0`，并填 `ExploreSiteResult.VerifiedJobs / VerifySampleTitles`。
  - `pyVerifyResult` 的 JSON tag 与 Python `app/graph/verify.py` 回传键（`ok`/`jobs_found`/
    `sample_titles`/`error`）逐一对应，多出的 `browser_bound`/`used_url` 字段忽略无碍。
- **验证**：`go build` + `go vet` + `TestExploreResponseContract` 通过；真机经 `make server`（带
  `USE_PYTHON_AGENT=true`、`AGENT_EXPLORER_URL=http://127.0.0.1:8400`）删除旧 kuaishou recipe 后
  重新探索，读库确认 `site_recipes.verified_jobs = 10`（旧值 0）、`verify_status=verified`、`enabled=true`，
  时间与 Go 服务日志 `11:45:34` 完成一致。
- **语义提醒**：`verified_jobs` 在探索期记录「验证时采到的条数」；Fast Path 真正执行时仍会由
  `executor.go` 的 `MarkSuccess(ctx, siteKey, run.JobsFound)` 再核对一次并覆盖为真实数字，二者不冲突。

### 模型与 token
- 推理模型（如 `qwen3.8-max`）在 thinking 模式下禁止 browser-use 强制的 `tool_choice`，需在
  `app/llm.py` 对其加 `extra_body={"enable_thinking": False}`；**该参数仅 qwen 系列加**，
  DeepSeek 等 flash 模型无 thinking 模式冲突且兼容端点可能不支持该参数，带上反而 400。
- 各免费模型额度会耗尽：遇 `403 FreeTierOnly: Free quota exhausted` 需换有免费额度的模型
  （换模型前先与用户确认，不要自己猜）。
- 一轮成功的完整探索约 **3 万 input tokens**（browser-use 日志 `Total input tokens used`，
  仅记 input、不记 output；精确总 token 需在 `app/llm.py` 累加 `response.usage`）。

## 关于 browser-use

- browser-use 作为 **pip 依赖**装进本服务的 venv，**不**引用项目根目录的 `./browser-use`
  本地克隆（那个文件夹仅供阅读、会被删）。
- 所有 browser-use 使用代码写在 `app/browser/controller.py`（属于 fallsurvivor 自身）。
