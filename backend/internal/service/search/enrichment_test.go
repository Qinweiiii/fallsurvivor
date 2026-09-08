package search

import "testing"

func TestLooksLikeJobListPage(t *testing.T) {
	text := `共2个岗位
搜索词-AI安全技术工程师
AI安全技术工程师（风控算法）
工作地点：深圳总部 广州
AI安全技术工程师
工作地点：深圳总部 北京 上海`

	if !looksLikeJobListPage(text) {
		t.Fatal("expected list page to be detected")
	}
	if looksLikeJDDetailPage(text) {
		t.Fatal("list page should not be treated as JD detail")
	}
}

func TestLooksLikeJDDetailPage(t *testing.T) {
	text := `岗位职责
负责 AI 应用后端服务设计与研发。

任职要求
熟悉 Go 或 Python，理解常见分布式系统设计。`

	if !looksLikeJDDetailPage(text) {
		t.Fatal("expected JD detail page to be detected")
	}
}
