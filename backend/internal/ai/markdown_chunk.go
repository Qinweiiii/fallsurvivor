package ai

import (
	"encoding/json"
	"regexp"
	"strings"
)

/**
 * Markdown 结构感知分块。
 *
 * 设计对标 browser-use 的 chunk_markdown_by_structure。
 *
 * 为什么不能按字符数硬切：
 *   页面正文经常超过单次可送入模型的规模，必须分块。但简单的
 *   `s[:24000]` 会把一条记录切成两半——前半块只剩标题、后半块只剩
 *   城市和日期，两块都抽不出完整条目。信息不是丢在模型侧，而是
 *   丢在切分这一步。
 *
 * 算法（三阶段）：
 *   1. 解析原子块：把正文按 标题/代码块/表格行/列表项/段落 分组，
 *      同一块内部永不切断；
 *   2. 贪心装配：累积原子块直到接近上限；优先在标题处切分，
 *      让每块以语义边界开头；
 *   3. 衔接上下文：为后续块补上一块的尾部若干行；
 *      若新块以表格行开头，还要把表头补回来（否则模型不知道每列是什么）。
 *
 * 这些规则与站点无关，只依赖 Markdown 自身的语法结构。
 */

// blockType 是原子块的类型。
type blockType int

const (
	blockBlank blockType = iota
	blockHeader
	blockCodeFence
	blockTable
	blockListItem
	blockParagraph
)

// atomicBlock 是不可再切分的最小单元。
type atomicBlock struct {
	kind blockType
	// lines 该块包含的原始行。
	lines []string
	// charStart / charEnd 在原文中的字符偏移（charEnd 为开区间）。
	//
	// 用它而不是行号：调用方需要用 startFromChar 续抽，
	// 字符偏移对外部是稳定标识，行号会因块重组而变化。
	charStart int
	charEnd   int
}

// MarkdownChunk 是一个可独立送入模型的正文块。
type MarkdownChunk struct {
	// Content 块正文（未含 OverlapPrefix）。
	Content string
	// Index 块序号，从 0 开始。
	Index int
	// Total 总块数。
	Total int
	// CharStart / CharEnd 在原文中的字符范围。
	CharStart int
	CharEnd   int
	// OverlapPrefix 需要拼在 Content 之前的衔接上下文
	// （上一块尾部若干行，必要时含表头）。
	OverlapPrefix string
	// HasMore 是否还有后续块。为 true 时调用方应用 CharEnd
	// 作为下次的 startFromChar 继续抽取。
	HasMore bool
}

// FullContent 返回带衔接上下文的完整块文本，可直接送入模型。
func (c MarkdownChunk) FullContent() string {
	if c.OverlapPrefix == "" {
		return c.Content
	}
	return c.OverlapPrefix + "\n" + c.Content
}

var (
	tableRowRe  = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	listItemRe  = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s`)
	listContRe  = regexp.MustCompile(`^(\s{2,}|\t)`)
	headerLineRe = regexp.MustCompile(`^\s*#`)
)

/**
 * ChunkMarkdownByStructure 把正文切成结构完整的块。
 *
 * maxChunkChars 是软上限：单个原子块超限时允许整块超出，
 * 因为切开它反而会破坏可读性（宁可让模型多读一点）。
 * overlapLines 为衔接行数，startFromChar 用于续抽。
 *
 * 返回空切片表示 startFromChar 已越过正文末尾。
 */
func ChunkMarkdownByStructure(content string, maxChunkChars, overlapLines, startFromChar int) []MarkdownChunk {
	if maxChunkChars <= 0 {
		maxChunkChars = 60_000
	}
	if overlapLines < 0 {
		overlapLines = 0
	}
	if content == "" {
		return []MarkdownChunk{{Total: 1}}
	}
	if startFromChar >= len(content) {
		return nil
	}

	blocks := parseAtomicBlocks(content)
	if len(blocks) == 0 {
		return nil
	}

	// 阶段 2：贪心装配，优先在标题处切分。
	var raw [][]atomicBlock
	var cur []atomicBlock
	curSize := 0

	for _, b := range blocks {
		size := b.charEnd - b.charStart
		if curSize+size > maxChunkChars && len(cur) > 0 {
			// 回溯找最后一个标题：若在它之前已积累足够内容，
			// 就从标题处切开，让下一块以标题开头（语义更完整）。
			split := len(cur)
			for j := len(cur) - 1; j > 0; j-- {
				if cur[j].kind != blockHeader {
					continue
				}
				prefix := 0
				for _, pb := range cur[:j] {
					prefix += pb.charEnd - pb.charStart
				}
				// 阈值 50%：避免切出一个过小的块，那会浪费一次模型调用。
				if prefix >= maxChunkChars/2 {
					split = j
					break
				}
			}
			raw = append(raw, cur[:split])
			cur = cur[split:]
			curSize = 0
			for _, cb := range cur {
				curSize += cb.charEnd - cb.charStart
			}
		}
		cur = append(cur, b)
		curSize += size
	}
	if len(cur) > 0 {
		raw = append(raw, cur)
	}

	// 阶段 3：构建对外块，补衔接上下文。
	total := len(raw)
	chunks := make([]MarkdownChunk, 0, total)
	prevTableHeader := ""

	for idx, group := range raw {
		texts := make([]string, 0, len(group))
		for _, b := range group {
			texts = append(texts, strings.Join(b.lines, "\n"))
		}
		text := strings.Join(texts, "\n")

		overlap := ""
		if idx > 0 && overlapLines > 0 {
			prevTexts := make([]string, 0, len(raw[idx-1]))
			for _, b := range raw[idx-1] {
				prevTexts = append(prevTexts, strings.Join(b.lines, "\n"))
			}
			prevLines := strings.Split(strings.Join(prevTexts, "\n"), "\n")
			tail := prevLines
			if len(tail) > overlapLines {
				tail = tail[len(tail)-overlapLines:]
			}

			// 新块以表格行开头时必须补回表头，
			// 否则模型看到的是一堆无列名的单元格。
			if group[0].kind == blockTable && prevTableHeader != "" {
				combined := strings.Split(prevTableHeader, "\n")
				for _, tl := range tail {
					if !containsLine(combined, tl) {
						combined = append(combined, tl)
					}
				}
				overlap = strings.Join(combined, "\n")
			} else {
				overlap = strings.Join(tail, "\n")
			}
		}

		// 记录本块的表头供下一块使用；没有新表头时保留旧的，
		// 这样跨 3 块以上的长表格也能一直带着表头。
		for _, b := range group {
			if b.kind != blockTable {
				continue
			}
			if h := tableHeaderOf(b); h != "" {
				prevTableHeader = h
			}
		}

		chunks = append(chunks, MarkdownChunk{
			Content:       text,
			Index:         idx,
			Total:         total,
			CharStart:     group[0].charStart,
			CharEnd:       group[len(group)-1].charEnd,
			OverlapPrefix: overlap,
			HasMore:       idx < total-1,
		})
	}

	// 续抽：返回包含 startFromChar 的那一块及其后续。
	if startFromChar > 0 {
		for i, c := range chunks {
			if c.CharEnd > startFromChar {
				return chunks[i:]
			}
		}
		return nil
	}
	return chunks
}

// containsLine 判断切片中是否已有该行，避免衔接上下文里重复表头。
func containsLine(lines []string, target string) bool {
	for _, l := range lines {
		if l == target {
			return true
		}
	}
	return false
}

// tableHeaderOf 提取表格块的「表头 + 分隔行」。非表头块返回空串。
func tableHeaderOf(b atomicBlock) string {
	if b.kind != blockTable || len(b.lines) < 2 {
		return ""
	}
	sep := b.lines[1]
	if strings.Contains(sep, "---") {
		return b.lines[0] + "\n" + b.lines[1]
	}
	return ""
}

/**
 * parseAtomicBlocks 把正文分组为不可切分的原子块。
 *
 * 表格的处理略特殊：「表头 + 分隔行」合为一块，
 * 其后每个数据行各自成块——这样长表格可以在行之间切开，
 * 而不会把表头和全部数据绑成一个巨块。
 */
func parseAtomicBlocks(content string) []atomicBlock {
	lines := strings.Split(content, "\n")
	blocks := make([]atomicBlock, 0, len(lines)/2+1)
	offset := 0
	i := 0

	for i < len(lines) {
		line := lines[i]
		lineLen := len(line) + 1 // +1 补回被 Split 去掉的换行

		// 空行
		if strings.TrimSpace(line) == "" {
			blocks = append(blocks, atomicBlock{
				kind: blockBlank, lines: []string{line},
				charStart: offset, charEnd: offset + lineLen,
			})
			offset += lineLen
			i++
			continue
		}

		// 代码块：整块不可分，直到闭合围栏或文末
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fence := []string{line}
			end := offset + lineLen
			i++
			for i < len(lines) {
				fl := lines[i]
				fence = append(fence, fl)
				end += len(fl) + 1
				i++
				if strings.HasPrefix(strings.TrimSpace(fl), "```") {
					break
				}
			}
			blocks = append(blocks, atomicBlock{
				kind: blockCodeFence, lines: fence,
				charStart: offset, charEnd: end,
			})
			offset = end
			continue
		}

		// 标题
		if headerLineRe.MatchString(line) {
			blocks = append(blocks, atomicBlock{
				kind: blockHeader, lines: []string{line},
				charStart: offset, charEnd: offset + lineLen,
			})
			offset += lineLen
			i++
			continue
		}

		// 表格
		if tableRowRe.MatchString(line) {
			head := []string{line}
			end := offset + lineLen
			i++
			if i < len(lines) && tableRowRe.MatchString(lines[i]) && strings.Contains(lines[i], "---") {
				head = append(head, lines[i])
				end += len(lines[i]) + 1
				i++
			}
			blocks = append(blocks, atomicBlock{
				kind: blockTable, lines: head,
				charStart: offset, charEnd: end,
			})
			offset = end
			// 数据行各自成块，便于在行间切分
			for i < len(lines) && tableRowRe.MatchString(lines[i]) {
				row := lines[i]
				rowLen := len(row) + 1
				blocks = append(blocks, atomicBlock{
					kind: blockTable, lines: []string{row},
					charStart: offset, charEnd: offset + rowLen,
				})
				offset += rowLen
				i++
			}
			continue
		}

		// 列表项（含缩进续行与同级后续项）
		if listItemRe.MatchString(line) {
			items := []string{line}
			end := offset + lineLen
			i++
			for i < len(lines) {
				nl := lines[i]
				if listItemRe.MatchString(nl) || (strings.TrimSpace(nl) != "" && listContRe.MatchString(nl)) {
					items = append(items, nl)
					end += len(nl) + 1
					i++
					continue
				}
				break
			}
			blocks = append(blocks, atomicBlock{
				kind: blockListItem, lines: items,
				charStart: offset, charEnd: end,
			})
			offset = end
			continue
		}

		// 段落：连续非空行，遇到其他块类型即止
		para := []string{line}
		end := offset + lineLen
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
			nl := lines[i]
			if headerLineRe.MatchString(nl) ||
				strings.HasPrefix(strings.TrimSpace(nl), "```") ||
				tableRowRe.MatchString(nl) ||
				listItemRe.MatchString(nl) {
				break
			}
			para = append(para, nl)
			end += len(nl) + 1
			i++
		}
		blocks = append(blocks, atomicBlock{
			kind: blockParagraph, lines: para,
			charStart: offset, charEnd: end,
		})
		offset = end
	}

	// 修正末块偏移：逐行 +1 的累计会超出实际长度（末行无换行符）。
	if n := len(blocks); n > 0 && blocks[n-1].charEnd > len(content) {
		blocks[n-1].charEnd = len(content)
	}
	return blocks
}

/**
 * stripJSONBlobs 去掉正文里的大段 JSON。
 *
 * SPA 常把整个页面状态以内联 JSON 塞进 DOM，动辄数万字符。
 * 它对识别列表条目毫无帮助，却会挤占上下文、把真实内容推出可见范围。
 *
 * 只删「长且能被解析为 JSON」的行——单纯按前缀判断会误伤
 * Markdown 链接与图片（它们也以 `[` 开头）。
 */
func stripJSONBlobs(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" {
			kept = append(kept, line)
			continue
		}
		if len(s) > 100 && (s[0] == '{' || s[0] == '[') {
			var probe any
			if json.Unmarshal([]byte(s), &probe) == nil {
				continue // 确认是 JSON 才丢弃
			}
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
