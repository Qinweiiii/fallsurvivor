---
title: 秋招 OS｜Job Search Agent 设计
---

# 1. 目标

Job Search Agent 的职责：

> 根据用户求职画像，尽可能发现大量有价值的岗位，并将不同来源的岗位统一整理、去重、结构化和排序。

它不是一个聊天机器人。

用户点击：

> 获取岗位

Agent 才执行一次搜索任务。

------------------------------------------------------------------------

# 2. 输入

Agent 输入：

``` json
{
  "target_roles": [
    "后端",
    "AI后端",
    "AI全栈",
    "Agent"
  ],
  "preferred_languages": [
    "Go",
    "Python"
  ],
  "locations": [
    "深圳",
    "东莞",
    "广州",
    "北京",
    "上海"
  ],
  "company_preferences": [
    "大厂",
    "外企"
  ],
  "resume_profile": {}
}
```

------------------------------------------------------------------------

# 3. Query Generator

DeepSeek 负责生成搜索词。

例如：

``` text
2027 Go 后端 校招 深圳
2027 AI 后端 校招 深圳 Go
2027 Agent 后端 校招 深圳
2027 AI 全栈 校招 深圳
2027 Go 后端 校招 东莞
2027 AI Infra 校招 广州
```

注意：

> Query Generator 应该生成"搜索策略"，而不是只生成一个关键词。

------------------------------------------------------------------------

# 4. Search Sources

并行搜索：

``` text
Official Career Source
BOSS Source
Tavily Source
```

如果某个来源暂时不可用，不应导致整个搜索任务失败。

------------------------------------------------------------------------

# 5. Official Source

第一版不需要覆盖所有公司。

可以先设计为可扩展 Adapter：

``` text
CompanySource
```

每家公司以后可以有独立实现。

例如：

``` text
ByteDanceSource
TencentSource
HuaweiSource
AlibabaSource
```

如果暂时没有稳定的官方接口，可以通过 Tavily 搜索官方招聘页作为补充。

------------------------------------------------------------------------

# 6. BOSS Source

BOSS 作为独立 Source。

必须考虑：

-   登录
-   页面结构变化
-   频率限制
-   验证码
-   访问失败

第一版如果无法稳定自动抓取，不要硬做"绕过限制"。

可以先：

> 通过搜索结果发现 BOSS 岗位 URL。

------------------------------------------------------------------------

# 7. Tavily

Tavily 负责全网补充搜索。

搜索重点：

-   公司官方招聘网站
-   BOSS
-   招聘信息页
-   校招岗位页

Search 结果保存：

``` text
title
url
content / snippet
score
```

------------------------------------------------------------------------

# 8. Job Normalizer

所有 Source 的 RawJob 转成统一 Job。

例如：

``` json
{
  "company_name": "XX公司",
  "department": "AI平台",
  "title": "后端开发工程师",
  "location": ["深圳"],
  "job_type": "校园招聘",
  "graduation_year": 2027,
  "description": "...",
  "requirements": [],
  "technical_stack": []
}
```

------------------------------------------------------------------------

# 9. JD Parser

DeepSeek 负责从 JD 中抽取：

``` text
company
department
business
title
locations
job_type
graduation_year
responsibilities
requirements
languages
technical_stack
deadline
```

必须要求结构化 JSON 输出。

解析失败：

> 保存原始 JD，不要让整个任务失败。

------------------------------------------------------------------------

# 10. Job Deduplication

第一层：

``` text
official_url
```

第二层：

``` text
normalized_url
```

第三层：

``` text
company + title + location
```

第四层：

> 语义相似度

不要一开始就用 LLM 做所有去重。

------------------------------------------------------------------------

# 11. Job Matching

推荐使用：

``` text
Rule Score
+
LLM Score
```

## Rule Score

考虑：

-   岗位方向
-   技术语言
-   城市
-   毕业年份
-   公司偏好

## LLM Score

DeepSeek 判断：

-   工作内容是否匹配
-   技术栈是否匹配
-   简历经历是否匹配
-   岗位发展方向是否匹配

输出：

``` json
{
  "score": 94,
  "reasons": [
    "Go 技术栈高度匹配",
    "后端实习经历匹配",
    "Agent 项目经验匹配"
  ],
  "risks": [
    "Kubernetes 经验较少"
  ]
}
```

------------------------------------------------------------------------

# 12. 城市评分

城市是有顺序的。

默认：

``` text
深圳 100
东莞 95
广州 90
北京 80
上海 75
其他 50
```

用户以后可以调整。

------------------------------------------------------------------------

# 13. 语言匹配

例如：

岗位：

``` text
Go / Java / Python
```

用户：

``` text
Go > Python
```

应该判定为：

> 高匹配

而：

``` text
Java only
```

应该明显降低分数。

但不要直接判定为"不推荐"，因为用户仍可能愿意投。

------------------------------------------------------------------------

# 14. Agent 输出

Search Task 最终输出：

``` json
{
  "total_found": 82,
  "new_jobs": 37,
  "duplicates": 45,
  "high_match_jobs": 14
}
```

岗位全部落库。

------------------------------------------------------------------------

# 15. 错误处理

单个来源失败：

``` text
Tavily OK
Official OK
BOSS FAILED
```

整体任务：

> COMPLETED_WITH_WARNING

而不是 FAILED。

------------------------------------------------------------------------

# 16. 不要做的事情

不要：

-   自动投递
-   自动注册账号
-   绕过验证码
-   绕过反爬
-   虚构岗位
-   猜测缺失 JD
-   把推测内容当成真实 JD

任何无法确认的信息：

> 标记为未知。

------------------------------------------------------------------------

# 17. Agent 原则

这个 Agent 的核心不是"像人一样思考"。

而是：

> **稳定地把大量招聘信息转化为用户真正可以使用的岗位数据。**

因此：

确定性逻辑优先。

LLM 只解决语义问题。
