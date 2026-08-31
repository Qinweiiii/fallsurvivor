package config

import "strings"

// DefaultCityScores 是城市偏好的默认评分，来源于设计文档第 12 节。
// 用户可以在求职画像中调整城市顺序，运行期由 BuildCityScores 重新生成。
var DefaultCityScores = map[string]int{
	"深圳": 100,
	"东莞": 95,
	"广州": 90,
	"北京": 80,
	"上海": 75,
}

// UnknownCityScore 是未列出城市的兜底分。
const UnknownCityScore = 50

// BuildCityScores 依据用户给定的城市优先级顺序生成评分表。
// 第一名 100 分，之后每位递减 5 分，最低不低于 UnknownCityScore+5。
func BuildCityScores(ordered []string) map[string]int {
	if len(ordered) == 0 {
		out := make(map[string]int, len(DefaultCityScores))
		for k, v := range DefaultCityScores {
			out[k] = v
		}
		return out
	}
	scores := make(map[string]int, len(ordered))
	score := 100
	for _, city := range ordered {
		c := strings.TrimSpace(city)
		if c == "" {
			continue
		}
		if score < UnknownCityScore+5 {
			score = UnknownCityScore + 5
		}
		scores[c] = score
		score -= 5
	}
	return scores
}

// ScoreForCity 返回某个地点字符串的偏好分。
// 采用包含匹配，以兼容“深圳市南山区”这类描述。
func ScoreForCity(scores map[string]int, location string) int {
	loc := strings.TrimSpace(location)
	if loc == "" {
		return UnknownCityScore
	}
	best := UnknownCityScore
	for city, s := range scores {
		if strings.Contains(loc, city) && s > best {
			best = s
		}
	}
	return best
}
