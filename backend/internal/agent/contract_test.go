package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestExploreResponseContract 是 Go 后端与 Python 探索服务的【跨语言契约验证】。
//
// 它把 Python 离线服务真实产出的 /explore 响应（agent-explorer/fixtures/explore.jsonl）
// 用本包 client.go 的 exploreResponse 结构逐字段解码，断言：
//  1. 能成功反序列化（字段名/类型一致，零额外映射）；
//  2. recipe 必填字段非空（与 backend/internal/ai/explorer.go 的 RecipeCandidate 契约对齐）。
//
// 这说明：一旦 Python 服务切到 production（真实浏览器+LLM），其产出的配置可被 Go 原样接收落库，
// 无需任何转换层。跑法：先 `PYTHONPATH=. python tests/dump_fixtures.py` 生成 fixture，再 `go test`。
func TestExploreResponseContract(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile = backend/internal/agent/contract_test.go → 上溯 4 级到仓库根
	repoRoot := filepath.Join(thisFile, "..", "..", "..", "..")
	fixture := filepath.Join(repoRoot, "agent-explorer", "tests", "fixtures", "explore.jsonl")

	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Skipf("fixture 缺失，请先在 agent-explorer 跑 `PYTHONPATH=. python tests/dump_fixtures.py`: %v", err)
		return
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("fixture 为空")
	}

	for i, line := range lines {
		var resp exploreResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("第 %d 行反序列化失败: %v\n%s", i, err, line)
		}
		if resp.Status != "success" {
			t.Fatalf("第 %d 行 status=%q (期望 success)", i, resp.Status)
		}
		if resp.Recipe == nil {
			t.Fatalf("第 %d 行 recipe 为 nil", i)
		}
		r := resp.Recipe
		// 必填字段非空（与 ai.RecipeCandidate 的业务约束一致）
		for _, field := range []struct {
			name  string
			value string
		}{
			{"list_api", r.ListAPI},
			{"method", r.Method},
			{"id_field", r.IDField},
			{"title_field", r.TitleField},
			{"list_path", r.ListPath},
		} {
			if strings.TrimSpace(field.value) == "" {
				t.Errorf("第 %d 行 recipe.%s 为空", i, field.name)
			}
		}
		if r.Confidence < 0 || r.Confidence > 100 {
			t.Errorf("第 %d 行 confidence=%d 越界", i, r.Confidence)
		}
		// 轨迹应覆盖五个角色（与后端 ExploreStepTrace 映射一致）
		roles := map[string]bool{}
		for _, tr := range resp.Trace {
			roles[tr.Role] = true
		}
		for _, want := range []string{"guardrail", "planner", "actor", "critic", "memory"} {
			if !roles[want] {
				t.Errorf("第 %d 行 轨迹缺角色 %q", i, want)
			}
		}
	}
}
