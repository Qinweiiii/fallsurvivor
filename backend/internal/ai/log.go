package ai

import (
	"runtime"
	"strings"
	"sync"
	"time"
)

// LLMCallLog 是单次 LLM 调用的完整记录，用于调试观测页面展示。
type LLMCallLog struct {
	Timestamp    time.Time `json:"timestamp"`
	Model        string    `json:"model"`
	Kind         string    `json:"kind"`          // 调用类型：ParseJD / MatchJob / ClassifyPage / GenerateQueries
	SystemPrompt string    `json:"system_prompt"` // 完整 system prompt
	UserPrompt   string    `json:"user_prompt"`   // 完整 user prompt（已脱敏+截断）
	Response     string    `json:"response"`      // 模型返回的原始内容
	DurationMS   int64     `json:"duration_ms"`   // 耗时（毫秒），含重试
	Attempts     int       `json:"attempts"`      // 实际尝试次数
	Error        string    `json:"error"`         // 失败时的错误信息
}

const maxLLMLogs = 200

var (
	logsMu sync.Mutex
	logs   = make([]LLMCallLog, 0, maxLLMLogs+1)
)

// RecordLLMCall 追加一条 LLM 调用日志到内存 ring buffer（最近 200 条）。
func RecordLLMCall(entry LLMCallLog) {
	logsMu.Lock()
	defer logsMu.Unlock()
	logs = append(logs, entry)
	if len(logs) > maxLLMLogs {
		logs = logs[len(logs)-maxLLMLogs:]
	}
}

// RecentLLMCalls 返回最近 n 条 LLM 调用日志（按时间倒序）。
// n<=0 或 n>实际数量时返回全部。
func RecentLLMCalls(n int) []LLMCallLog {
	logsMu.Lock()
	defer logsMu.Unlock()
	if n <= 0 || n > len(logs) {
		n = len(logs)
	}
	out := make([]LLMCallLog, n)
	// 倒序拷贝：最新的在前。
	for i := 0; i < n; i++ {
		out[i] = logs[len(logs)-1-i]
	}
	return out
}

// callerKind 通过调用栈推断 LLM 调用类型（ParseJD / MatchJob 等）。
// skipFrames 跳过 completeJSON 自身及其调用方。
func callerKind(skipFrames int) string {
	pc, _, _, ok := runtime.Caller(skipFrames)
	if !ok {
		return "unknown"
	}
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return "unknown"
	}
	name := fn.Name()
	// 形如 github.com/eddiel/.../ai.(*Client).ParseJD → ParseJD
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		name = name[idx+1:]
	}
	// 去掉 (*Client) 前缀。
	name = strings.TrimPrefix(name, "(*Client).")
	return name
}
