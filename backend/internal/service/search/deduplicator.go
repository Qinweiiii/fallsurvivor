package search

import (
	"strings"

	"github.com/eddiel/fallsurvivor/backend/internal/source"
)

// Candidate 是经过标准化的岗位候选，尚未落库。
type Candidate struct {
	Raw source.RawJob

	CompanyName   string
	Title         string
	Location      string
	NormalizedURL string
	Fingerprint   string
	// URLID 是从 URL 中提取的岗位唯一标识（如腾讯校招的 postid）。
	// 同一公司下的同名岗位可能由不同 BG 发布（postid 不同），
	// 若指纹只算 company+title+location，这些岗位会被错误合并。
	// 因此 URLID 非空时一并纳入指纹，确保不同岗位各自独立。
	URLID string
}

// DedupStats 记录去重过程的统计。
type DedupStats struct {
	// InputCount 是进入去重的候选总数。
	InputCount int
	// DroppedNoise 是被判定为噪声内容而丢弃的数量。
	DroppedNoise int
	// DroppedInvalid 是 URL 非法或缺少必要信息而丢弃的数量。
	DroppedInvalid int
	// MergedInBatch 是本批内部合并掉的重复数量。
	MergedInBatch int
	// Unique 是去重后剩余数量。
	Unique int
}

// DedupInBatch 对单次搜索得到的候选做批内去重。
//
// 去重分层（依据设计文档第 10 节，前三层全部为确定性逻辑）：
//
//	第一层：official_url 精确相同
//	第二层：normalized_url 相同
//	第三层：company + title + location 指纹相同
//	第四层（语义相似度）留待后续按需引入，第一版不启用，
//	       以避免把不同部门的同名岗位错误合并。
//
// 同一岗位在多个来源出现时，保留优先级最高的来源作为主记录，
// 其余来源在落库阶段写入 job_sources。
func DedupInBatch(raws []source.RawJob) ([]Candidate, []source.RawJob, DedupStats) {
	stats := DedupStats{InputCount: len(raws)}

	// key 为去重键，value 为该键对应的主候选下标。
	index := make(map[string]int, len(raws))
	candidates := make([]Candidate, 0, len(raws))
	// extras 保存被合并掉的来源记录，落库时作为附加 job_source。
	var extras []source.RawJob

	for _, raw := range raws {
		if LooksLikeNoise(raw.Title) {
			stats.DroppedNoise++
			continue
		}

		normURL := NormalizeURL(raw.URL)
		if normURL == "" {
			stats.DroppedInvalid++
			continue
		}

		c := Candidate{
			Raw:           raw,
			CompanyName:   strings.TrimSpace(raw.CompanyHint),
			Title:         strings.TrimSpace(raw.Title),
			Location:      NormalizeCity(raw.LocationHint),
			NormalizedURL: normURL,
			URLID:         ExtractURLJobID(normURL),
		}
		// 指纹纳入 URLID：让不同 postid 的同名岗位拥有不同指纹。
		c.Fingerprint = URLIDFingerprint(c.CompanyName, c.Title, c.Location, c.URLID)

		// 去重键：按用户要求，批内去重暂时只保留第一层（normalized_url 完全相同）。
		// 第三层「company+title+location 指纹」会把同名但不同 postid 的岗位
		// （腾讯校招多 BG 发布同名岗位）错误合并，导致记录丢失，故停用。
		keys := []string{"u:" + normURL}

		merged := false
		for _, k := range keys {
			if idx, ok := index[k]; ok {
				// 已存在：比较来源优先级决定是否替换主记录。
				if sourcePriority(raw.SourceType) < sourcePriority(candidates[idx].Raw.SourceType) {
					extras = append(extras, candidates[idx].Raw)
					candidates[idx] = c
					// 主记录变更后重建键映射。
					reindex(index, keys, idx)
				} else {
					extras = append(extras, raw)
				}
				stats.MergedInBatch++
				merged = true
				break
			}
		}
		if merged {
			continue
		}

		candidates = append(candidates, c)
		idx := len(candidates) - 1
		for _, k := range keys {
			index[k] = idx
		}
	}

	stats.Unique = len(candidates)
	return candidates, extras, stats
}

// reindex 把给定键全部指向 idx。
func reindex(index map[string]int, keys []string, idx int) {
	for _, k := range keys {
		index[k] = idx
	}
}

// sourcePriority 返回来源优先级，数值越小优先级越高。
// 官方站信息最权威，其次 BOSS，最后是全网搜索结果。
func sourcePriority(sourceType string) int {
	switch sourceType {
	case "OFFICIAL":
		return 0
	case "BOSS":
		return 1
	case "MANUAL":
		return 2
	default:
		return 3
	}
}
