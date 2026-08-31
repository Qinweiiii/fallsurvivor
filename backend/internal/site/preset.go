package site

import (
	"context"
	"log/slog"
)

// PresetRecipes 是系统内置的站点 Recipe。
//
// 这些站点已在代码中验证过采集链路（腾讯 / 字节的浏览器抽取），
// 此处只是把「站点元信息 + 限制」沉淀为数据，让它们与人工配置的站点
// 走同一套 Registry / Executor 流程，不再依赖硬编码分支。
//
// 注意：这些 Recipe 不覆盖用户已有的同名配置——
// SeedPresets 只在 site_key 不存在时插入，已有记录保持原样。
func PresetRecipes() []Recipe {
	return []Recipe{
		{
			SiteKey:     "tencent",
			CompanyName: "腾讯",
			Domain:      "join.qq.com",
			CampusURL:   "https://join.qq.com/post.html?query=p_1",
			// 腾讯校招是 Vue SPA：列表由 searchPosition 接口异步提供，
			// 卡片 DOM 不含 postId；详情页为 post_detail.html?postid=<id>。
			StrategyType:     StrategyBrowser,
			AdapterKey:       "tencent",
			Enabled:          true,
			MaxJobsPerSearch: 20,
			// 腾讯的 JD 在 extractJobs 阶段已由 jobDetails 接口拿到（desc/request + 部门），
			// 再打开详情页抓 DOM 反而会覆盖这些结构化内容，因此设为 0 表示不抓详情。
			MaxDetailFetches: 0,
			Notes: "校招官网 join.qq.com。Vue SPA，列表走 /api/v1/position/searchPosition，" +
				"详情走 /api/v1/jobDetails/getJobDetailsByPostId。keyword 需拼在 URL 参数中，" +
				"SPA 会先发一次无关键词请求再发过滤请求，需取最后一次响应。需浏览器登录态。",
			Source:  SourcePreset,
			Version: 1,
		},
		{
			SiteKey:          "bytedance",
			CompanyName:      "字节跳动",
			Domain:           "jobs.bytedance.com",
			CampusURL:        "https://jobs.bytedance.com/campus",
			StrategyType:     StrategyBrowser,
			AdapterKey:       "bytedance",
			Enabled:          true,
			MaxJobsPerSearch: 20,
			MaxDetailFetches: 8,
			Notes: "字节校招官网 jobs.bytedance.com/campus。" +
				"列表抽卡后逐条打开详情页抓取 JD。需浏览器登录态。",
			Source:  SourcePreset,
			Version: 1,
		},
	}
}

// SeedPresets 把内置预置 Recipe 写入数据库（仅当 site_key 不存在时）。
//
// 在系统启动时调用。幂等：重复调用不会产生重复记录，
// 也不会覆盖用户之后修改过的配置。
func (r *Repository) SeedPresets(ctx context.Context) error {
	for _, preset := range PresetRecipes() {
		existing, err := r.GetBySiteKey(ctx, preset.SiteKey)
		if err != nil {
			return err
		}
		if existing != nil {
			// 已存在（可能是用户改过的），保持原样。
			continue
		}
		toCreate := preset
		if err := r.Create(ctx, &toCreate); err != nil {
			return err
		}
		slog.Info("已写入内置站点 Recipe", "site_key", preset.SiteKey, "company", preset.CompanyName)
	}
	return nil
}
