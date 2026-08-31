package search

import (
	"testing"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

func TestDedupInBatchMergesSameURL(t *testing.T) {
	raws := []source.RawJob{
		{URL: "https://jobs.example.com/p/1?utm_source=a", Title: "后端开发工程师", SourceType: model.SourceTavily, CompanyHint: "腾讯"},
		{URL: "https://jobs.example.com/p/1", Title: "后端开发工程师", SourceType: model.SourceTavily, CompanyHint: "腾讯"},
	}
	candidates, extras, stats := DedupInBatch(raws)
	if len(candidates) != 1 {
		t.Fatalf("期望合并为 1 条，实际 %d 条", len(candidates))
	}
	if stats.MergedInBatch != 1 {
		t.Errorf("期望合并计数为 1，实际 %d", stats.MergedInBatch)
	}
	if len(extras) != 1 {
		t.Errorf("被合并的来源应保留在 extras，实际 %d 条", len(extras))
	}
}

func TestDedupInBatchKeepsDifferentURLJobs(t *testing.T) {
	// 第三层「company+title+location 指纹」去重已按需求停用：
	// 同名但 URL 不同的岗位（如腾讯校招多 BG 发布同名岗位、不同 postid）
	// 必须各自保留，否则会造成大量记录丢失。
	raws := []source.RawJob{
		{URL: "https://boss.example.com/p/1", Title: "后端开发工程师", SourceType: model.SourceBoss,
			CompanyHint: "腾讯", LocationHint: "深圳"},
		{URL: "https://careers.tencent.com/p/1", Title: "后端开发工程师", SourceType: model.SourceOfficial,
			CompanyHint: "腾讯", LocationHint: "深圳市南山区"},
	}
	candidates, _, _ := DedupInBatch(raws)
	if len(candidates) != 2 {
		t.Fatalf("URL 不同的同名岗位应各自保留为 2 条，实际 %d 条", len(candidates))
	}
}

func TestDedupInBatchPrefersOfficialSource(t *testing.T) {
	// 仅当 normalized_url 完全相同（去掉追踪参数后一致）时才合并，
	// 且保留来源优先级更高的记录（OFFICIAL > 其他）。
	raws := []source.RawJob{
		{URL: "https://careers.tencent.com/p/1?utm_source=a", Title: "后端开发工程师", SourceType: model.SourceBoss,
			CompanyHint: "腾讯", LocationHint: "深圳"},
		{URL: "https://careers.tencent.com/p/1", Title: "后端开发工程师", SourceType: model.SourceOfficial,
			CompanyHint: "腾讯", LocationHint: "深圳市南山区"},
	}
	candidates, _, _ := DedupInBatch(raws)
	if len(candidates) != 1 {
		t.Fatalf("同 URL 岗位应合并为 1 条，实际 %d 条", len(candidates))
	}
	if candidates[0].Raw.SourceType != model.SourceOfficial {
		t.Errorf("应保留官方来源作为主记录，实际 %s", candidates[0].Raw.SourceType)
	}
}

func TestDedupInBatchDropsNoiseAndInvalid(t *testing.T) {
	raws := []source.RawJob{
		{URL: "https://zhihu.com/p/1", Title: "腾讯后端面经", SourceType: model.SourceTavily},
		{URL: "not-a-url", Title: "后端开发工程师", SourceType: model.SourceTavily},
		{URL: "https://jobs.example.com/p/2", Title: "后端开发工程师", SourceType: model.SourceTavily},
	}
	candidates, _, stats := DedupInBatch(raws)
	if stats.DroppedNoise != 1 {
		t.Errorf("期望丢弃 1 条噪声，实际 %d", stats.DroppedNoise)
	}
	if stats.DroppedInvalid != 1 {
		t.Errorf("期望丢弃 1 条非法 URL，实际 %d", stats.DroppedInvalid)
	}
	if len(candidates) != 1 {
		t.Errorf("期望剩余 1 条，实际 %d", len(candidates))
	}
}
