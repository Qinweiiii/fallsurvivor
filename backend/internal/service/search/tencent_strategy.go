package search

import (
	"context"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

// CrawlTencent 走腾讯校招官网（join.qq.com）浏览器抽取。
//
// 关键背景：
//   - 腾讯「社招」走 careers.tencent.com 的官方 API（见 source/tencent.go 的 TencentSource.Fetch），
//     那是给后台搜索任务用的，覆盖不了校招；
//   - 校招官网是 join.qq.com（列表页 post.html?query=...、详情页 post_detail.html?postid=<id>），
//     列表由 Vue 异步接口 searchPosition 提供，卡片 DOM 不含 postId；
//   - Worker 侧 tencentAdapter 在 extractJobs 阶段已通过
//     /api/v1/jobDetails/getJobDetailsByPostId 拿到完整 JD（desc/request）与部门信息，
//     因此这里传 maxDetailFetches=0 跳过详情页 DOM 抓取，避免覆盖结构化内容。
//   - keyword 会拼到列表页 URL 参数上，由 SPA 触发过滤后的列表请求。
//
// 本函数保留为便捷入口；新的调用方应优先使用 CrawlWithRecipe（由站点 Recipe 驱动）。
func (c *SiteCrawler) CrawlTencent(ctx context.Context, taskID, startURL, keyword string) ([]ai.NavJob, []source.RawJob, error) {
	// maxDetailFetches=0：Worker 已拿到完整 JD，无需再开详情页。
	return c.crawlExtractJobs(ctx, taskID, "tencent", startURL, "腾讯", "腾讯校招", keyword, 0)
}
