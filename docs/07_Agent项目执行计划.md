# 秋招 Agent 项目执行计划

本文记录当前阶段的工程取舍与后续执行路线，目标是把项目打磨成一个可稳定演示、可解释技术价值的秋招 Agent 项目。

## 当前定位

项目目前应定位为：

> 面向秋招岗位发现与投递准备的 Agent 系统，重点实现招聘官网动态探索、岗位列表接口自动发现、可复用采集 Recipe 记忆、JD 语义解析与匹配评分。

不要包装成“全自动投递平台”。当前主线仍是“岗位搜索 Agent”，投递自动化可作为后续扩展或裁剪项。

## 核心判断

短期不做全量 Python 重构。

原因：

- Go 后端已经承担 API、DB、Recipe、去重、入库、字段质量、验证、自修正等主流程能力。
- TypeScript Playwright Worker 已经能完成真实浏览器探索，没必要重写。
- 当前主要短板是探索稳定性、站点覆盖、Trace 展示、字段质量，不是语言本身。
- 全量重构会让项目回到基础设施重写阶段，影响主流程验证。

中期可以引入 Python，但只拆 Agent orchestration 层：

```text
Next.js Frontend
      |
Go API Server
      |
      | calls
      v
Python Agent Service  ---- calls ---- TS Playwright Worker
      |
      | returns recipe / trace / field map
      v
Go API Server persists site_recipes / jobs
```

这样可以合理使用 LangGraph / LangChain，同时保留现有业务系统。

## 阶段 1：先稳住现有 Go/TS 主链路

目标：证明“探索 -> 保存 Recipe -> 复用 Fast Path -> 入库”闭环稳定。

- [x] 京东校园招聘：稳定完成探索、保存、复用、字段质量检查。
- [x] 美团校园招聘：稳定完成探索、保存、复用、字段质量检查。
- [ ] 再补 1-3 个结构不同的大厂官网，例如快手、百度、小米、阿里。
- [ ] 每个站点都记录一次首探 Trace 与一次复用结果。
- [x] 明确字段质量门槛：title / description / location 为核心字段；department / business 先诊断，不阻断。
- [x] 减少探索空转：避免重复 wait、重复 inspect 同一低价值请求、重复搜索。
- [x] 保证用户关键词不会被 LLM 随意改写。
- [x] 保证探索超时错误可诊断，不再出现无意义的 empty reply。

验收标准：

- 至少 3 个官网站点可以演示首探与复用。
- 复用 Fast Path 应在秒级返回。
- 探索失败时能说明失败阶段：页面动作、网络观测、Recipe 生成、验证、自修正。

## 阶段 2：做 Agent Trace 与调试展示

目标：让项目“看起来像 Agent”，并且能解释 Agent 做了什么。

- [x] 在前端展示探索 Trace：step、action、target、result、confidence、reasoning。
- [x] 展示候选接口摘要：method、url、是否含 request body。
- [x] 展示 Recipe 生成结果：list_api、method、list_path、title_field、field_map。
- [x] 展示验证结果：sample titles、field_quality、refine_rounds。
- [x] 展示“首次探索”和“后续复用”的状态差异。
- [ ] 给失败场景做清楚提示：登录态、无可用请求、字段质量不足、上游 LLM 错误。

验收标准：

- 面试或演示时，可以不用看终端日志，只看页面就能讲清楚一次探索过程。
- 用户能明确知道当前是“探索保存 recipe”还是“采集入库”。

## 阶段 3：补技术文档与项目表达

目标：让项目能作为秋招作品被快速理解。

- [ ] README 增加项目定位、核心能力、演示路径。
- [ ] 架构图说明 Go / TS Worker / LLM / DB / 前端边界。
- [ ] 说明 memory 设计：
  - site_recipes：单站点长期记忆。
  - exploration_playbook：跨站点探索经验记忆。
  - job records / source records：岗位事实记忆。
  - LLM logs / trace：过程记忆与调试证据。
- [ ] 说明为什么不用 Tavily 大海捞针作为主路径。
- [ ] 说明 Recipe 验证与自修正闭环。
- [ ] 准备 2-3 个演示脚本：京东、美团、第三站点。

验收标准：

- README 能让面试官在 3 分钟内理解项目亮点。
- 文档能回答“为什么这是 Agent 项目，而不是普通爬虫/CRUD”。

## 阶段 4：渐进引入 Python Agent Service

触发条件：阶段 1 和阶段 2 基本稳定后再做。

目标：把 Agent 编排层从 Go 中抽出来，使用 LangGraph 表达状态机，但不重写业务后端。

- [ ] 先在 Go 侧抽象 Agent Engine 接口。
- [ ] 保留当前 Go Explorer 作为 `AGENT_ENGINE=go`。
- [ ] 新增 Python `agent-service`，提供 HTTP 接口。
- [ ] Python service 内部用 LangGraph 表达：
  - observe page/network
  - decide action
  - execute browser action
  - build recipe
  - verify recipe
  - refine recipe
  - finish / abort
- [ ] Python service 仍调用现有 TS Playwright Worker，不重写浏览器控制。
- [ ] Go 后端继续负责保存 recipe、入库、查询、用户状态。
- [ ] 增加配置开关：
  - `AGENT_ENGINE=go`
  - `AGENT_ENGINE=python`

验收标准：

- Python Agent Service 能完成至少一个已验证站点的首探。
- Go Engine 与 Python Engine 可切换。
- 引入 LangGraph 后，项目表达更清楚，而不是为了贴技术标签牺牲稳定性。

## 暂缓事项

这些不是不重要，而是当前阶段不应阻塞主流程：

- 大规模并发采集。
- 复杂去重策略升级。
- 多层 fallback 搜索优化。
- 全量重构为 Python。
- 完整自动投递闭环。
- 对所有站点做专门适配器。

## 当前下一步

优先继续阶段 1：

1. 用第三个结构不同的官网站点验证泛化能力。
2. 开始做 Trace 前端展示，让首探与复用过程不依赖终端日志解释。
3. 补 README / 演示脚本，把京东、美团、第三站点串成可讲述的项目故事。
