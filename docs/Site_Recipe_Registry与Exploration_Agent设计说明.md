# Site Recipe Registry + Exploration Agent 设计说明

> **用途**：直接提供给代码执行
> AI，作为招聘站点自适应岗位采集模块的实现依据。\
> **核心原则**：**AI
> 处理未知，代码处理已知。第一次探索可以较贵，但成功后必须沉淀为可重复执行的确定性
> Recipe。**

## 1. 背景与目标

不同公司的校招网站获取岗位信息的方式并不统一：有的网站可以通过规律化 URL
获取详情；有的网站必须点击页面组件；有的网站页面文本不完整，但 Network
中存在干净的 JSON List/Detail
API；还有的网站需要先交互才能触发真正的数据请求。

不能为每家公司长期手工分析 URL、Network Request/Response
并维护独立脚本。系统需要实现：

**Site Recipe Registry + Recipe Executor + Site Exploration Agent**

第一次遇到未知站点时，允许使用 Playwright、Network 观察和 LLM
进行有限探索；找到稳定、低成本、可复现的信息获取路径后，将其固化为
Recipe。之后再次访问同一站点时优先执行 Recipe，不再重新让 LLM 探索。

## 2. 总体流程

``` text
用户请求搜索某公司
        ↓
识别招聘站点 / ATS
        ↓
查询 Site Recipe Registry
        ↓
    ┌── 有且仍有效 ──────────────┐
    │                            │
    ▼                            │
直接执行已验证 Recipe            │
    │                            │
    ▼                            │
获取岗位                         │
                                 │
    └── 没有 / Recipe失效 ───────┐
                                 ↓
                         Exploration Agent
                                 ↓
                     浏览网页 + 观察 Network
                                 ↓
                          尝试不同获取策略
                                 ↓
                         找到可稳定复现路径
                                 ↓
                          自动生成 Recipe
                                 ↓
                            验证 Recipe
                                 ↓
                           保存到 Registry
                                 ↓
                              执行
```

系统明确分为两条路径。

### Fast Path：已知站点

``` text
Site Identification
        ↓
Recipe Registry HIT
        ↓
Recipe Executor
        ↓
Normalized Jobs
```

特点：确定性执行、速度快、成本低、原则上不再调用 LLM 进行网页探索。

### Discovery Path：未知/失效站点

``` text
Site Identification
        ↓
Registry MISS / INVALID
        ↓
Exploration Agent
        ↓
Browser + Network Observation
        ↓
Candidate Strategy
        ↓
Validation
        ↓
Persist Recipe
        ↓
Recipe Executor
```

Explorer 的目标不是"这一次碰巧拿到岗位"，而是**生成下次仍可独立执行的
Recipe**。

## 3. 获取策略优先级

Explorer
的目标不是尽量模仿人类点击，而是寻找能够稳定获得完整岗位信息的最低成本方法：

``` text
Level 1：直接 API / XHR / Fetch
        ↓
Level 2：可推导 URL / URL Template
        ↓
Level 3：页面内嵌 JSON / hydration data
        ↓
Level 4：Playwright DOM 交互 + DOM Extraction
        ↓
Level 5：人工接管
```

如果可以直接请求结构化岗位 JSON，就不要长期依赖浏览器点击和 DOM 提取。

## 4. 核心模块

### 4.1 Site Identifier

根据公司、招聘入口 URL、Domain 和必要的页面特征识别站点。

第一版以 `domain + company` 为主要标识即可，不要求立即实现复杂 ATS
指纹识别。未来发现多家公司共用 Workday、Moka 等 ATS 后，再抽象共享
Recipe。

### 4.2 Site Recipe Registry

负责保存和查询已经验证的采集方法：

``` text
site_key / domain
        ↓
存在 VERIFIED Recipe？
   YES          NO
    ↓            ↓
返回 Recipe    Exploration
```

建议 PostgreSQL 持久化，Recipe 主体使用 JSONB。

### 4.3 Recipe Executor

只按照 Recipe 确定性执行，不做开放式推理。至少支持：

-   API Strategy：直接请求 List/Detail API；
-   URL Template Strategy：根据 job_id 等组装 URL；
-   Browser Strategy：按 Recipe 中固定步骤执行
    navigate/fill/click/wait/extract。

### 4.4 Site Exploration Agent

Registry MISS 或 Recipe INVALID 时启动。可以使用 Playwright、DOM
摘要、链接、Network Request/Response、LLM
和规则判断，但必须限制最大探索步骤。

## 5. Recipe 数据模型

Recipe 是"采集方法"，不是某一次请求的具体 URL。

API Recipe 示例：

``` json
{
  "site_key": "example-career",
  "domain": "careers.example.com",
  "strategy_type": "api",
  "list_strategy": {
    "type": "api",
    "method": "POST",
    "url_pattern": "/api/jobs/search",
    "pagination": {
      "type": "page",
      "page_param": "page",
      "size_param": "pageSize"
    },
    "search_params": {
      "keyword_param": "keyword",
      "location_param": "location"
    }
  },
  "detail_strategy": {
    "type": "api",
    "method": "GET",
    "url_pattern": "/api/jobs/{job_id}"
  },
  "field_mapping": {
    "job_id": "$.id",
    "title": "$.title",
    "department": "$.department",
    "location": "$.location",
    "description": "$.description",
    "requirements": "$.requirements"
  },
  "auth": {
    "type": "browser_session"
  }
}
```

Browser Recipe
第一版只需要表达当前已验证站点需要的动作，不必设计完整通用 DSL：

``` json
{
  "strategy_type": "browser",
  "list_strategy": {
    "type": "browser",
    "steps": [
      {"action": "navigate", "target": "https://careers.example.com"},
      {"action": "fill", "target": "岗位搜索框", "value_from": "keyword"},
      {"action": "click", "target": "搜索"},
      {"action": "extract_job_cards"}
    ]
  }
}
```

## 6. 数据库

### site_recipes

``` text
id
site_key
company_name
domain
ats_type
strategy_type
recipe_json
status
created_at
updated_at
last_verified_at
```

第一版状态：

``` text
DISCOVERED → VALIDATING → VERIFIED
                         ↘ INVALID
```

Recipe 确认失效后：

``` text
VERIFIED → INVALID → Exploration
```

第一版不要实现复杂 success rate、health score、自动 Repair
和多版本管理。

### recipe_runs

``` text
id
recipe_id
started_at
finished_at
success
jobs_found
error_type
error_message
```

日志禁止保存 Cookie、Authorization、Token 等敏感值。

## 7. Exploration Agent 详细流程

### Step 1：打开招聘入口

记录：

-   当前 URL；
-   page title；
-   主要可见文本；
-   links 摘要；
-   buttons/inputs 等可交互元素；
-   Network baseline。

### Step 2：判断当前页面

LLM 只接收压缩后的页面信息，不直接塞完整 HTML。判断：

-   是否已经处于岗位列表页；
-   是否存在"校招 / Campus / Students / Jobs"等入口；
-   是否存在搜索框和筛选器；
-   下一步最合理的单个 Action。

### Step 3：执行有限交互

Explorer 每轮只允许 LLM 选择一个明确动作，然后重新观察页面与 Network
变化。

### Step 4：寻找最低成本数据路径

优先尝试识别 API、URL Pattern、页面内嵌 JSON；只有这些方案不可行时才将
Browser DOM 操作作为长期 Recipe。

### Step 5：生成 Recipe Candidate

LLM 将已确认的请求模式、参数规律、字段映射和必要交互生成结构化
Candidate，但不能直接标记 VERIFIED。

## 8. Network Observer

重点监听：

``` text
XHR
Fetch
GraphQL
JSON Response
必要的 HTML Fragment
```

过滤图片、字体、CSS、常见埋点等无关请求。

### Network Diff

每个关键 Action 前后记录请求集合：

``` text
Baseline
↓
点击岗位 / 搜索 / 翻页
↓
After Action
↓
Diff
```

例如点击岗位后只新增：

``` text
/api/job/detail?id=123
/api/track/click
/api/recommend
```

则 Detail API 很容易成为高价值候选。

## 9. Network Candidate 预筛选

禁止把数百条 Network 请求全部发送给 LLM。

先由普通代码筛选和评分，例如：

``` text
JSON Response                 +2
XHR / Fetch                   +1
URL contains job/position     +2
contains title                +1
contains location             +1
contains description          +2
contains requirements         +2
紧随岗位点击/搜索操作出现       +2
```

只把 Top N 候选及必要的 Response Schema/样本交给 LLM。

LLM 再判断哪个最可能是：

-   Job List API；
-   Job Detail API；
-   无关埋点；
-   推荐接口；
-   其他请求。

## 10. LLM 的职责边界

LLM 负责：

1.  页面语义理解；
2.  下一步有限 Browser Action；
3.  Network Candidate 语义判断；
4.  参数规律推断；
5.  Recipe Candidate 生成；
6.  必要的字段语义映射。

LLM **不负责**：

-   已知 URL 的拼接；
-   已知 API 的每次调用；
-   JSON 确定性解析；
-   分页循环；
-   Recipe 的常规执行；
-   已有字段映射的重复推理。

原则：**LLM 解决未知；普通代码执行已知。**

## 11. Explorer Tool Interface

向 LLM 暴露有限工具：

``` text
inspect_page()
list_interactive_elements()
navigate(url)
click(element_id)
fill(element_id, value)
go_back()
list_recent_network_requests()
inspect_network_response(request_id)
extract_links()
extract_page_text()
finish_with_recipe(recipe_candidate)
request_user_assistance(reason)
```

不要允许 LLM 任意执行 JavaScript。

建议限制：

``` text
max_steps = 20
max_navigation = 8
max_network_inspections = 15
max_consecutive_failures = 3
```

超过限制返回 `NEED_USER_ASSISTANCE`。

验证码、MFA、登录风控等必须暂停，由用户人工处理后继续。

## 12. Recipe Validation

Candidate 不能直接保存为 VERIFIED。

例如发现：

``` text
GET /api/job/detail/{job_id}
```

至少选择 2～3 个不同岗位验证：

``` text
Job A → Success
Job B → Success
Job C → Success
```

验证至少检查：

-   请求/浏览器执行成功；
-   title 非空；
-   description / requirements 至少有有效正文；
-   job_id 与目标一致；
-   可用字段能够稳定解析；
-   Recipe 不依赖某次临时固定值。

验证成功：

``` text
DISCOVERED → VALIDATING → VERIFIED
```

验证失败则继续探索其他 Strategy 或返回失败。

**Explorer 自己拿到数据不算成功。生成的 Recipe 被 Recipe Executor
独立再次执行成功，才算探索成功。**

## 13. 已有 Recipe 失效

``` text
Registry HIT
↓
Recipe Executor
↓
成功？
├─ YES → 返回岗位
└─ NO
    ↓
确认 Recipe 路径失效
    ↓
标记 INVALID
    ↓
重新 Exploration
```

第一版不需要自动 Repair。重新探索即可。

## 14. 安全要求

Network 中可能包含 Cookie、Authorization、Bearer Token、CSRF
Token、Session ID 等敏感信息。

Recipe 只能保存：

-   URL Pattern；
-   HTTP Method；
-   参数名称和结构；
-   Response Schema；
-   Field Mapping；
-   是否依赖 Browser Session。

禁止保存具体 Token/Cookie。

错误：

``` json
{"Authorization": "Bearer eyJ..."}
```

正确：

``` json
{"auth": {"type": "browser_session"}}
```

执行时使用当前本地浏览器 Session。日志同样不得打印敏感 Header/Token。

## 15. 统一岗位输出

所有 Recipe 最终必须输出统一 Job Schema：

``` json
{
  "company": "",
  "department": "",
  "title": "",
  "location": [],
  "job_id": "",
  "description": "",
  "requirements": "",
  "source_url": "",
  "detail_url": "",
  "source_type": "official",
  "site_key": ""
}
```

后续去重、AI 匹配、岗位车和投递管理全部依赖 Normalized
Job，不依赖各招聘站原始结构。

## 16. 推荐代码模块

保持单体后端，不拆微服务。若现有项目已有目录规范，应融入现有结构，不要为匹配本文档强制重构。

``` text
backend/internal/
├── job/
│   ├── model.go
│   ├── service.go
│   └── repository.go
├── site/
│   ├── identifier.go
│   ├── recipe.go
│   ├── registry.go
│   └── repository.go
├── executor/
│   ├── executor.go
│   ├── api_executor.go
│   ├── url_executor.go
│   └── browser_executor.go
├── explorer/
│   ├── explorer.go
│   ├── planner.go
│   ├── tools.go
│   ├── network_observer.go
│   ├── network_ranker.go
│   ├── recipe_builder.go
│   └── validator.go
└── browser/
    └── ...
```

## 17. Job Discovery Service 伪代码

``` text
DiscoverJobs(company):

    site = SiteIdentifier.identify(company)

    recipe = RecipeRegistry.findVerified(site)

    if recipe exists:
        result = RecipeExecutor.execute(recipe)

        if result.success:
            return Normalize(result.jobs)

        if result.indicatesRecipeInvalid:
            RecipeRegistry.markInvalid(recipe)

    exploration = SiteExplorer.explore(site)

    if exploration.failed:
        return NEED_USER_ASSISTANCE

    candidate = exploration.recipe

    validation = RecipeValidator.validate(candidate)

    if validation.failed:
        return EXPLORATION_FAILED

    RecipeRegistry.saveVerified(candidate)

    result = RecipeExecutor.execute(candidate)

    return Normalize(result.jobs)
```

## 18. MVP 范围

### 必须完成

1.  将当前腾讯、字节已经验证的采集逻辑抽象成 Recipe；
2.  建立 Site Recipe Registry；
3.  搜索时优先查询 VERIFIED Recipe；
4.  Recipe Executor 能执行当前已知 Recipe；
5.  未知站点能进入 Exploration；
6.  Explorer 能读取压缩后的页面信息；
7.  能监听 XHR / Fetch / JSON；
8.  能做关键 Action 前后的 Network Diff；
9.  能用简单规则预筛选 Network Candidate；
10. LLM 能通过有限工具决定下一步；
11. 能生成 Recipe Candidate；
12. Candidate 能在多个岗位上验证；
13. 验证成功后保存；
14. 第二次访问该站点直接走 Fast Path；
15. Recipe 失效后能标记 INVALID 并重新探索；
16. 所有来源输出统一 Job Schema。

### 当前明确不做

-   Kafka；
-   Kubernetes；
-   微服务；
-   Multi-Agent；
-   Recipe 自动版本管理；
-   复杂 success rate / health score；
-   自动 Recipe Repair；
-   大规模 ATS 指纹库；
-   通用 Web Agent；
-   任意 JavaScript 执行；
-   CAPTCHA / MFA / 风控绕过；
-   无限制 Agent 自主探索。

## 19. 开发顺序

代码 AI 必须按顺序实现，不要一次性铺开：

``` text
Phase 1
腾讯 / 字节现有逻辑
↓
抽象为 Recipe

Phase 2
Recipe Registry
↓
Recipe Executor
↓
跑通 Fast Path

Phase 3
Network Observer
↓
Network Diff
↓
Candidate Filtering

Phase 4
Exploration Agent
↓
有限 Browser Tools
↓
LLM Planner

Phase 5
Recipe Builder
↓
Recipe Validator
↓
Persist

Phase 6
陌生招聘网站测试
↓
第一次 Exploration
↓
保存 Recipe
↓
第二次 Fast Path
```

## 20. 四个核心验收 Case

### Case A：腾讯/字节等已知网站

``` text
用户搜索
↓
Registry HIT
↓
直接执行 Recipe
↓
获取岗位
↓
Normalize
↓
前端展示
```

不重新启动开放式 Exploration。

### Case B：未知 Company X

``` text
Registry MISS
↓
Explorer
↓
页面观察 + 有限交互
↓
Network Diff
↓
发现 API / URL / DOM Strategy
↓
Recipe Candidate
↓
多岗位验证
↓
VERIFIED
↓
保存
↓
Recipe Executor 独立执行
↓
Normalized Jobs
```

### Case C：第二次搜索 Company X

``` text
Registry HIT
↓
直接执行上次保存的 Recipe
↓
不进行开放式 LLM 探索
↓
获取岗位
```

**这是本架构最关键的验收 Case。**

### Case D：Recipe 失效

``` text
Recipe Executor 失败
↓
确认采集路径失效
↓
Recipe → INVALID
↓
重新 Exploration
↓
新 Recipe 验证
↓
恢复 Fast Path
```

## 21. 给代码执行 AI 的硬性要求

1.  先阅读现有代码，腾讯/字节已验证逻辑不要推倒重写，优先抽象进 Recipe。
2.  不要过度设计；这是实际使用的个人秋招工具。
3.  不引入 Kafka、K8s、微服务等当前不必要组件。
4.  确定性逻辑必须由普通代码完成。
5.  LLM 只处理未知和语义判断。
6.  Exploration 必须有步数/成本上限。
7.  不保存认证 Token、Cookie 等敏感 Network 信息。
8.  不绕过 CAPTCHA、MFA 或风控，遇到时请求人工接管。
9.  Recipe 必须验证后才能 VERIFIED。
10. Explorer 获取到数据不等于成功；Recipe 必须被 Executor 独立复现成功。
11. 所有来源最终转换为统一 Job Schema。
12. 优先跑通腾讯、字节 + 一个陌生站点，不追求一次支持大量公司。
13. 开发过程中如果现有代码结构与本文档建议不同，以"最小改动完成上述能力"为优先，不为架构形式大规模重构。

## 22. 一句话架构定义

本模块不是"每次都让 AI 浏览招聘网站"。

它是：

> **以确定性 Recipe 为主路径，以 LLM Browser Exploration
> 为未知/失效站点兜底，并将一次高成本探索沉淀为长期可复用采集能力的招聘信息获取系统。**

``` text
Unknown Site
    ↓
LLM Exploration
    ↓
Verified Recipe
    ↓
Deterministic Execution
    ↓
Reusable Knowledge
```
