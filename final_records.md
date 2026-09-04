我开始改。范围按你刚确认的通用流转来，但第一版会保守：列表先落库、低质量不打正式分、异步补全先复用已有详情 URL 或 recipe 详情模板；“回列表点击归纳模板”我会先把接口结构留出来，不做盲点卡片的复杂逻辑，避免又引入不确定行为。


已读取文件


现状比预期好一点：项目已经有 asynq，但只有搜索任务；补全服务是同步接口，还没有队列任务。Pipeline 现在的问题是 listOnly 只阻止 LLM Parse/Match，但仍然会算规则分并写入分析，所以列表 brief 仍被评估了。


已读取文件


要做到“列表先返回，详情和 match 后台跑”，真正需要把 enqueue 放在调用层，而不是塞进 pipeline 内部。因为 pipeline 现在没有 asynq client，强行塞进去会扩大依赖。我先读 search handler 和服务构造，找最小接入点。


已读取文件


上下文已自动压缩


我先把当前代码边界重新确认一下，再动手改。这个改动会跨 pipeline / task / handler / enrichment 几处，但目标只围绕“列表先落库、匹配延后、详情异步补全”这一条链，不做额外优化。


已读取文件


我确认到现有代码确实是同步 IngestRawJobs 时直接算分；详情补全服务也只支持已有 source_url，没有 recipe 回列表点击路径。接下来我会先做最小闭环：入库时标记待补全并返回 job IDs，crawl 接口排异步补全任务；补全任务先用 URL/template，没 URL 时用 recipe 入口回列表搜索标题并点击卡片。


已读取文件


我会避免把“有无地点/部门”设成硬门槛。现在的门槛只会是：已经有岗位标题/公司等基本记录就能落库；只有在缺职责和要求时，把 match_analysis 标成 pending 并排补全，不在列表阶段强行判断合不合适。


已读取文件


这里还有一个必须改的小点：ListIncompleteJD 现在要求 source_url 非空，所以没有原网页 URL 的列表岗位根本进不了补全队列。我会把这个限制拆掉，但只影响补全查询，不改变 jobs 表结构。


已读取文件


我还会把异步任务接入现有 asynq，而不是另起一套队列。原因很直接：项目已经有 Redis/asynq，补全任务和搜索任务一样是长耗时浏览器任务，用同一个 worker 生命周期最少改动。


已读取文件


具体实现上，我不会把“腾讯/字节/阿里”写成特殊分支；站点归属只用来找 site_recipes 和 Worker profile。没有 URL 的补全会走 recipe 的 campus_url + browser_plan，搜索当前岗位标题，再点击标题文本，这是通用的可视路径。


已读取文件


耗时 3分钟 28秒


我准备开始编辑这几个文件：pipeline.go、service.go、handler/search.go、task.go、task/handler.go、cmd/worker/main.go、enrichment.go、job_repo.go，外加少量单元测试。范围超过 3 个文件的原因是这是一个端到端异步链路：入库结果、HTTP 排队、任务消费、补全执行、待补全查询都要对齐；只改其中一个点会继续出现“入库了但没人补”或“排了任务但查不到岗位”。