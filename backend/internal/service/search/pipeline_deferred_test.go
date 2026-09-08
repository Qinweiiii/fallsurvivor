package search

import (
	"testing"

	"github.com/eddiel/fallsurvivor/backend/internal/model"
)

func TestShouldDeferMatchForTrustedListWithoutJDSections(t *testing.T) {
	job := &model.Job{
		Title:            "AI 工程师",
		CompanyName:      "示例公司",
		DescQuality:      string(DescQualityFull),
		Responsibilities: model.JSONStringArray{},
		Requirements:     model.JSONStringArray{},
	}

	if !shouldDeferMatch(job, DescQualityFull, false, true) {
		t.Fatal("trusted list job without responsibilities/requirements should defer matching")
	}
}

func TestShouldNotDeferMatchWhenJDSectionsExist(t *testing.T) {
	job := &model.Job{
		Title:            "AI 工程师",
		CompanyName:      "示例公司",
		DescQuality:      string(DescQualityFull),
		Responsibilities: model.JSONStringArray{"负责模型应用开发"},
		Requirements:     model.JSONStringArray{"熟悉 Go 或 Python"},
	}

	if shouldDeferMatch(job, DescQualityFull, false, true) {
		t.Fatal("job with responsibilities and requirements should be matchable")
	}
}

func TestPendingMatchAnalysisMarksDeferredStatus(t *testing.T) {
	analysis := pendingMatchAnalysis()
	if analysis["status"] != "pending_enrichment" {
		t.Fatalf("unexpected status: %v", analysis["status"])
	}
	if analysis["analyzer"] != "deferred" {
		t.Fatalf("unexpected analyzer: %v", analysis["analyzer"])
	}
}
