package search

import (
	"testing"

	"github.com/eddiel/fallsurvivor/backend/internal/ai"
	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

func testProfile() *model.JobProfile {
	return &model.JobProfile{
		TargetRoles:        model.JSONStringArray{"后端", "AI后端", "Agent"},
		PreferredLanguages: model.JSONStringArray{"Go", "Python"},
		PreferredLocations: model.JSONStringArray{"深圳", "东莞", "广州", "北京", "上海"},
		CompanyPreferences: model.JSONStringArray{"大厂", "外企"},
		GraduationYear:     2027,
	}
}

func TestRuleScoreIdealJobScoresHigh(t *testing.T) {
	year := 2027
	job := &model.Job{
		CompanyName:    "腾讯",
		Title:          "后端开发工程师",
		Location:       "深圳",
		Locations:      model.JSONStringArray{"深圳"},
		GraduationYear: &year,
		Description:    "使用 Go 开发高并发后端服务",
		TechnicalStack: model.JSONStringArray{"Go", "MySQL"},
	}
	d := ComputeRuleScore(ScoreInput{Job: job, Profile: testProfile()})
	if d.Total < 90 {
		t.Errorf("理想岗位规则分应不低于 90，实际 %d（明细 %+v）", d.Total, d)
	}
}

func TestRuleScoreMismatchedLanguageLowersScore(t *testing.T) {
	year := 2027
	base := &model.Job{
		CompanyName:    "腾讯",
		Title:          "后端开发工程师",
		Location:       "深圳",
		Locations:      model.JSONStringArray{"深圳"},
		GraduationYear: &year,
	}

	goJob := *base
	goJob.Description = "使用 Go 开发后端服务"
	javaJob := *base
	javaJob.Description = "使用 Java 与 Spring 开发后端服务"

	goScore := ComputeRuleScore(ScoreInput{Job: &goJob, Profile: testProfile()}).Total
	javaScore := ComputeRuleScore(ScoreInput{Job: &javaJob, Profile: testProfile()}).Total

	if javaScore >= goScore {
		t.Errorf("仅要求 Java 的岗位分数应低于 Go 岗位，Go=%d Java=%d", goScore, javaScore)
	}
	// 依据文档要求：明显降分但不判定为不可投。
	if javaScore < 30 {
		t.Errorf("语言不匹配不应把分数压到 30 以下，实际 %d", javaScore)
	}
}

func TestRuleScoreCityPriorityRespected(t *testing.T) {
	year := 2027
	mk := func(city string) *model.Job {
		return &model.Job{
			CompanyName:    "腾讯",
			Title:          "后端开发工程师",
			Location:       city,
			Locations:      model.JSONStringArray{city},
			GraduationYear: &year,
			Description:    "Go 后端",
		}
	}
	sz := ComputeRuleScore(ScoreInput{Job: mk("深圳"), Profile: testProfile()}).City
	sh := ComputeRuleScore(ScoreInput{Job: mk("上海"), Profile: testProfile()}).City
	other := ComputeRuleScore(ScoreInput{Job: mk("成都"), Profile: testProfile()}).City

	if !(sz > sh && sh > other) {
		t.Errorf("城市得分应按偏好顺序递减：深圳=%d 上海=%d 成都=%d", sz, sh, other)
	}
}

func TestRuleScoreUnknownGraduationYearNotZeroed(t *testing.T) {
	job := &model.Job{
		CompanyName: "腾讯",
		Title:       "后端开发工程师",
		Location:    "深圳",
		Locations:   model.JSONStringArray{"深圳"},
		Description: "Go 后端",
		// GraduationYear 为 nil，表示未知
	}
	d := ComputeRuleScore(ScoreInput{Job: job, Profile: testProfile()})
	if d.Year == 0 {
		t.Error("届次未知时不应把该维度归零，避免误杀信息不全的岗位")
	}
}

func TestFuseScoresWithoutLLM(t *testing.T) {
	rule := RuleScoreDetail{Total: 72, Reasons: []string{"方向匹配"}}
	out := FuseScores(rule, nil)
	if out.Score != 72 {
		t.Errorf("LLM 不可用时应直接使用规则分，实际 %d", out.Score)
	}
	if out.Analyzer != "rule" {
		t.Errorf("分析器标记应为 rule，实际 %s", out.Analyzer)
	}
}

func TestFuseScoresWithLLM(t *testing.T) {
	rule := RuleScoreDetail{Total: 60, Reasons: []string{"方向匹配"}}
	llm := &ai.MatchResult{Score: 90, Reasons: []string{"Go 技术栈高度匹配"}, Risks: []string{"K8s 经验较少"}}
	out := FuseScores(rule, llm)

	// 0.4*60 + 0.6*90 = 78
	if out.Score != 78 {
		t.Errorf("融合分计算错误，期望 78，实际 %d", out.Score)
	}
	if out.Analyzer != "rule+llm" {
		t.Errorf("分析器标记应为 rule+llm，实际 %s", out.Analyzer)
	}
	if len(out.Risks) == 0 {
		t.Error("风险项应被保留")
	}
}

func TestFuseScoresClampsRange(t *testing.T) {
	out := FuseScores(RuleScoreDetail{Total: 100}, &ai.MatchResult{Score: 100})
	if out.Score > 100 {
		t.Errorf("分数不应超过 100，实际 %d", out.Score)
	}
}

func TestFallbackQueriesNotEmpty(t *testing.T) {
	qs := FallbackQueries(testProfile())
	if len(qs) == 0 {
		t.Fatal("兜底检索式不应为空")
	}
	// 确保去重生效
	seen := map[string]bool{}
	for _, q := range qs {
		if seen[q] {
			t.Errorf("兜底检索式存在重复: %q", q)
		}
		seen[q] = true
	}
}

func TestFallbackQueriesWithEmptyProfile(t *testing.T) {
	qs := FallbackQueries(&model.JobProfile{})
	if len(qs) == 0 {
		t.Error("画像为空时也应生成默认检索式")
	}
}
