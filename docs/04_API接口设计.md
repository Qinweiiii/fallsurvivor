---
title: 秋招 OS｜API 接口设计
---

# 1. API 规范

Base URL：

``` text
/api
```

统一 JSON：

``` json
{
  "code": 0,
  "message": "success",
  "data": {}
}
```

错误：

``` json
{
  "code": 40001,
  "message": "参数错误",
  "data": null
}
```

------------------------------------------------------------------------

# 2. Job Profile

## GET /profile

获取求职画像。

## PUT /profile

更新：

-   求职方向
-   技术语言
-   城市优先级
-   公司偏好

------------------------------------------------------------------------

# 3. Resume

## POST /resumes

上传简历。

## GET /resumes

获取简历列表。

## PUT /resumes/:id/current

设置当前简历。

## GET /resumes/:id

查看解析结果。

------------------------------------------------------------------------

# 4. Job Search

## POST /jobs/search

触发一次岗位搜索。

Request：

``` json
{
  "force": true
}
```

Response：

``` json
{
  "task_id": "search_xxx"
}
```

------------------------------------------------------------------------

# 5. Search Task

## GET /search-tasks/:id

返回：

``` json
{
  "status": "RUNNING",
  "query_count": 8,
  "found_count": 35,
  "new_count": 18,
  "duplicate_count": 17
}
```

------------------------------------------------------------------------

# 6. Job List

## GET /jobs

Query：

``` text
page
page_size
keyword
company
title
locations[]
languages[]
min_score
status
```

Response：

``` json
{
  "items": [],
  "pagination": {
    "page": 1,
    "page_size": 20,
    "total": 128,
    "total_pages": 7
  }
}
```

------------------------------------------------------------------------

# 7. Job Detail

## GET /jobs/:id

返回完整 JD 和：

``` text
match_score
match_analysis
sources
```

------------------------------------------------------------------------

# 8. Job Cart

## GET /job-cart

获取岗位车。

## POST /job-cart

``` json
{
  "job_id": "job_xxx"
}
```

## DELETE /job-cart/:job_id

移出岗位车。

## POST /job-cart/batch

``` json
{
  "job_ids": []
}
```

------------------------------------------------------------------------

# 9. Applications

## GET /applications

支持：

``` text
status
keyword
company
page
page_size
```

## POST /applications

``` json
{
  "job_id": "job_xxx"
}
```

## POST /applications/batch

批量创建投递任务。

## GET /applications/:id

返回：

-   Job
-   Application
-   Browser Task
-   当前状态
-   表单完成度

------------------------------------------------------------------------

# 10. Browser Automation

## POST /applications/:id/browser/start

启动浏览器辅助任务。

## POST /browser-tasks/:id/resume

用户完成验证码 / 登录等操作后继续。

## POST /browser-tasks/:id/pause

暂停任务。

## GET /browser-tasks/:id

查看：

-   当前 URL
-   当前步骤
-   状态

------------------------------------------------------------------------

# 11. Final Submit

系统不要设计：

``` text
POST /applications/:id/submit
```

让后端替用户点击招聘网站提交按钮。

正确方式：

``` text
POST /applications/:id/mark-submitted
```

含义：

> 用户已经在招聘网站真实完成提交，现在把本系统状态标记为已投递。

------------------------------------------------------------------------

# 12. Application Status

## PUT /applications/:id/status

例如：

``` json
{
  "status": "WRITTEN_TEST"
}
```

允许用户手动更新：

-   已投递
-   笔试
-   一面
-   二面
-   HR 面
-   Offer
-   拒绝
-   撤回

------------------------------------------------------------------------

# 13. Dashboard

## GET /dashboard

返回：

``` json
{
  "jobs": {
    "total": 0,
    "new": 0
  },
  "cart": 0,
  "applications": 0,
  "submitted": 0,
  "written_test": 0,
  "interview": 0,
  "offer": 0
}
```

------------------------------------------------------------------------

# 14. API 原则

1.  API 简单优先。
2.  不要为了 REST 理论过度设计。
3.  所有分页接口统一格式。
4.  所有状态修改记录更新时间。
5.  外部任务使用 task_id。
6.  搜索、浏览器自动化等耗时任务不能阻塞 HTTP 请求。
