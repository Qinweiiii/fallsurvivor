// Package search 实现岗位搜索管线。
//
// 管线严格遵循「确定性逻辑优先，LLM 只解决语义问题」：
//   - URL 规范化、去重、字段清洗、评分权重、时间计算 → 纯代码；
//   - 检索式生成、JD 结构化、语义匹配 → LLM。
package search

import (
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// trackingParams 是需要从 URL 中剔除的追踪参数。
var trackingParams = map[string]bool{
	"utm_source": true, "utm_medium": true, "utm_campaign": true,
	"utm_term": true, "utm_content": true, "utm_id": true,
	"from": true, "ref": true, "referer": true, "referrer": true,
	"spm": true, "share_id": true, "sid": true, "trackid": true,
	"gclid": true, "fbclid": true, "_hsenc": true, "_hsmi": true,
}

// NormalizeURL 生成用于去重的规范化 URL。
//
// 规则：
//   - 统一小写 scheme 与 host，去掉 www. 前缀；
//   - 去掉默认端口；
//   - 剔除追踪类 query 参数，其余参数按 key 排序；
//   - 去掉普通 fragment 与末尾斜杠；
//   - 保留系统生成的岗位身份 fragment（如 #job_id=123），避免 API 型
//     Recipe 在没有真实详情 URL 时把不同岗位归一化成同一个列表接口。
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Host == "" {
		return ""
	}

	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")

	port := u.Port()
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		u.Host = host + ":" + port
	} else {
		u.Host = host
	}

	// 过滤追踪参数。
	q := u.Query()
	for k := range q {
		if trackingParams[strings.ToLower(k)] {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode() // Encode 已按 key 排序
	fragment := normalizedIdentityFragment(u.Fragment)
	u.Fragment = fragment
	u.RawFragment = ""
	u.User = nil

	u.Path = strings.TrimRight(u.Path, "/")
	if u.Path == "" {
		u.Path = "/"
	}

	return u.String()
}

func normalizedIdentityFragment(fragment string) string {
	f := strings.TrimSpace(fragment)
	if f == "" {
		return ""
	}
	parts := strings.SplitN(f, "=", 2)
	if len(parts) != 2 {
		return ""
	}
	key := strings.ToLower(strings.TrimSpace(parts[0]))
	value := strings.TrimSpace(parts[1])
	if value == "" {
		return ""
	}
	switch key {
	case "job_id", "jobid", "position_id", "positionid", "post_id", "postid", "id":
		return key + "=" + value
	default:
		return ""
	}
}

// 公司名清洗用的后缀与噪声词。按长度从长到短排列，确保优先匹配长后缀。
var companySuffixes = []string{
	"信息技术有限公司", "网络技术有限公司", "股份有限公司", "有限责任公司",
	"科技有限公司", "集团有限公司", "技术有限公司", "有限公司",
	"科技集团", "集团", "公司", "科技",
	"co., ltd.", "co.,ltd", "co. ltd", "ltd.", "ltd", "inc.", "inc",
	"corporation", "corp.", "corp", "llc", "limited", "group",
}

// reBracketed 匹配括号及其中内容，用于生成去重指纹时剔除地域、类别等修饰。
var reBracketed = regexp.MustCompile(`[（(【\[][^）)】\]]*[）)】\]]`)

// reNonWord 匹配需要在指纹中剔除的字符。
var reNonWord = regexp.MustCompile(`[\s\-_/\\()（）【】\[\]{}·、,，.。:：;；!！?？'"“”‘’|]+`)

// NormalizeDepartment 清洗部门字符串用于去重。
// 输入形如 "腾讯金融科技 - CDG"，输出 "腾讯金融科技|cdg"。
// 仅剥离噪声字符、保留语义，部门与事业群之间的分隔符固定为 |。
func NormalizeDepartment(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	// 「X - Y」分隔符统一为 |，让 "腾讯金融科技 - CDG" 与 "腾讯金融科技-CDG" 相同。
	s = strings.ReplaceAll(s, " - ", "|")
	s = strings.ReplaceAll(s, "-", "|")
	// 其余噪声字符折叠成 | 后再压紧，避免空白差异导致去重失败。
	s = reNonWord.ReplaceAllString(s, "|")
	// 折叠连续 | 与首尾 |。
	var b strings.Builder
	prevBar := false
	for _, r := range s {
		if r == '|' {
			if !prevBar && b.Len() > 0 {
				b.WriteRune('|')
			}
			prevBar = true
			continue
		}
		b.WriteRune(r)
		prevBar = false
	}
	return strings.Trim(b.String(), "|")
}

// NormalizeCompany 清洗公司名，用于生成去重指纹。
//
// 目标：让「腾讯科技（深圳）有限公司」与「腾讯」产生相同结果，
// 从而识别出这是同一家公司。
func NormalizeCompany(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	// 先剔除括号内容，避免「科技（深圳）有限公司」这类插入式修饰打断后缀匹配。
	s = reBracketed.ReplaceAllString(s, "")
	// 反复剔除后缀，直到不再变化。
	for {
		before := s
		for _, suf := range companySuffixes {
			if strings.HasSuffix(s, suf) && len(s) > len(suf) {
				s = strings.TrimSpace(strings.TrimSuffix(s, suf))
			}
		}
		if s == before {
			break
		}
	}
	return reNonWord.ReplaceAllString(s, "")
}

// titleNoise 是岗位名中不影响语义的噪声词。
var titleNoise = []string{
	"（校招）", "(校招)", "校园招聘", "校招", "秋招", "春招",
	"2025届", "2026届", "2027届", "2028届",
	"2025", "2026", "2027", "2028",
	"急招", "急聘", "招聘", "岗位", "职位",
	"j类", "a类", "b类",
}

// NormalizeTitle 清洗岗位名，用于生成去重指纹。
func NormalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	for _, n := range titleNoise {
		s = strings.ReplaceAll(s, n, "")
	}
	return reNonWord.ReplaceAllString(s, "")
}

// citySuffixes 是需要剔除的行政区划后缀。
var citySuffixes = []string{"特别行政区", "自治区", "自治州", "地区", "城市", "市辖区", "省", "市", "区", "县"}

// citySeparators 是地点描述中的分隔符。
var citySeparators = []string{"·", "-", "/", "|", " ", "，", ",", "、"}

// NormalizeCity 从地点描述中提取规范城市名。
//
// 目标：让「深圳市南山区」「深圳·南山」「深圳」都归一为「深圳」。
func NormalizeCity(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// 第一步：按分隔符只取第一段，例如「深圳·南山」→「深圳」。
	for _, sep := range citySeparators {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
			break
		}
	}

	// 第二步：若出现「市」，直接截断到「市」之前，
	// 这样「深圳市南山区」→「深圳」，而不是「深圳市南山」。
	if i := strings.Index(s, "市"); i > 0 {
		s = s[:i]
	}

	// 第三步：剔除残留的行政区划后缀。
	for {
		before := s
		for _, suf := range citySuffixes {
			if strings.HasSuffix(s, suf) && len([]rune(s)) > len([]rune(suf)) {
				s = strings.TrimSuffix(s, suf)
			}
		}
		if s == before {
			break
		}
	}
	return strings.TrimSpace(s)
}

// Fingerprint 生成 company + title + location 的去重指纹。
//
// 公司名与岗位名任一为空则返回空串，表示该候选不参与本层去重
// （信息不足时宁可重复，也不要错误合并两个不同岗位）。
func Fingerprint(company, title, location string) string {
	return DepartmentFingerprint(company, title, location, "")
}

// DepartmentFingerprint 与 Fingerprint 相同，但额外接受 department 参数。
//
// 当 department 非空时，它会拼接进指纹末尾，让「腾讯金融科技 - CDG」
// 与「元宝 - CSIG」算作不同岗位；为腾讯校招按部门展开的多条卡片提供独立去重键。
// department 为空时退化为三要素指纹，行为与 Fingerprint 完全一致。
func DepartmentFingerprint(company, title, location, department string) string {
	c := NormalizeCompany(company)
	t := NormalizeTitle(title)
	l := reNonWord.ReplaceAllString(strings.ToLower(NormalizeCity(location)), "")
	if c == "" || t == "" {
		return ""
	}
	d := NormalizeDepartment(department)
	if d == "" {
		return c + "|" + t + "|" + l
	}
	return c + "|" + t + "|" + l + "|" + d
}

// urlIDParamNames 是 URL query 中表示「岗位唯一标识」的参数名。
// 命中即取其值作为 URLID，用于把不同 ID 的同名岗位区分为不同记录。
var urlIDParamNames = []string{
	"postid", "postId", "jobid", "jobId", "positionid", "positionId",
	"id", "pid", "recruitid", "recruitId", "job_id", "post_id",
}

// reURLPathID 匹配 URL 路径末尾的数字 ID 段，
// 例如 /job/12345、/position/98765 中的末段数字。
var reURLPathID = regexp.MustCompile(`/(\d{5,})(?:/|$|\?)`)

// ExtractURLJobID 从岗位 URL 中提取岗位唯一标识。
//
// 依次尝试：
//  1. query 参数中的岗位 ID（postid / jobid / id 等），如
//     join.qq.com/post_detail.html?postid=1282707398326592512 → "1282707398326592512"；
//  2. URL 路径末尾的纯数字段，如 /job/12345 → "12345"。
//
// 提取不到时返回空串，调用方按「无 URLID」处理（退化为 company+title+location 指纹）。
//
// 背景：腾讯校招同一次爬取中常出现多个 BG 发布同名岗位（如「AI全栈工程师」），
// 它们的 postid 不同。若指纹只算 company+title+location，这些岗位会被错误合并，
// 导致大量记录丢失（表现为「爬到了但数据库里没有」）。
func ExtractURLJobID(normURL string) string {
	if normURL == "" {
		return ""
	}
	u, err := url.Parse(normURL)
	if err != nil {
		return ""
	}

	// 1. query 参数优先。
	q := u.Query()
	for _, name := range urlIDParamNames {
		if v := strings.TrimSpace(q.Get(name)); v != "" {
			return v
		}
	}

	// 2. 路径末尾的数字段。
	if m := reURLPathID.FindStringSubmatch(u.Path); len(m) > 1 {
		return m[1]
	}
	return ""
}

// URLIDFingerprint 生成 company + title + location (+urlID) 的去重指纹。
//
// 当 urlID 非空时拼接进指纹末尾，让 URL 岗位 ID 不同的同名岗位算作不同记录。
// urlID 为空时退化为三要素指纹，行为与 Fingerprint 完全一致。
func URLIDFingerprint(company, title, location, urlID string) string {
	c := NormalizeCompany(company)
	t := NormalizeTitle(title)
	l := reNonWord.ReplaceAllString(strings.ToLower(NormalizeCity(location)), "")
	if c == "" || t == "" {
		return ""
	}
	if id := strings.TrimSpace(urlID); id != "" {
		return c + "|" + t + "|" + l + "|" + id
	}
	return c + "|" + t + "|" + l
}

// CleanText 清理网页文本中的多余空白与不可见字符。
func CleanText(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r':
			if !lastSpace {
				b.WriteByte('\n')
				lastSpace = true
			}
		case unicode.IsSpace(r):
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		case unicode.IsControl(r):
			// 丢弃控制字符
		default:
			b.WriteRune(r)
			lastSpace = false
		}
	}
	return strings.TrimSpace(b.String())
}

// TruncateRunes 按字符数截断文本。
func TruncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// jobURLHints 是岗位详情页 URL 中常见的路径特征。
var jobURLHints = []string{
	"/job", "/jobs", "/position", "/positions", "/career", "/careers",
	"/campus", "/recruit", "/zhaopin", "/vacancy", "/opening", "/apply",
	"/detail", "/joblist", "/employment",
}

// LooksLikeJobURL 粗判 URL 是否可能是岗位相关页面。
// 只做初筛以降低无效解析成本，判断错误不影响正确性。
func LooksLikeJobURL(raw string) bool {
	lower := strings.ToLower(raw)
	for _, h := range jobURLHints {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}

// nonJobTitlePatterns 是明显不是岗位页的标题特征。
var nonJobTitlePatterns = []string{
	"面经", "经验分享", "攻略", "如何准备", "怎么准备", "复盘",
	"知乎", "百度知道", "论坛", "讨论", "吐槽", "避坑",
	"教程", "学习路线", "刷题", "题解", "简历模板",
}

// LooksLikeNoise 判断标题是否属于明显噪声内容。
func LooksLikeNoise(title string) bool {
	lower := strings.ToLower(title)
	for _, p := range nonJobTitlePatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// internshipKeywords 是实习生岗位的匹配关键词（中英文）。
var internshipKeywords = []string{
	"实习", "实习生", "intern", "internship",
}

// LooksLikeInternship 判断岗位是否为实习/校招类岗位。
func LooksLikeInternship(title, description string) bool {
	text := title + " " + description
	lower := strings.ToLower(text)
	for _, kw := range internshipKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// nonJDPatterns 是明显不是 JD 正文的文本特征。
// 当 Tavily 的 raw_content 为空时，content/snippet 往往退化为网页 <title> 或 meta description，
// 表现为网站名称、导航文字等，而非岗位职责/要求。
var nonJDPatterns = []struct {
	pattern string
	isRegex bool
}{
	// 网站标题/导航类
	{`官网`, false},
	{`招聘网`, false},
	{`招聘首页`, false},
	{`登录\|注册`, false},
	{`校园招聘官网`, false},
	// 纯品牌名 + 招聘（无任何岗位信息）
	{`^[^|。\n]{2,20}\s*[|｜]\s*[^|。\n]{2,20}(校招|校园招聘|招聘)$`, true},
	// 太短且无实质内容（少于 30 字符的纯标题行）
}

// DescriptionQuality 表示岗位描述的数据质量。
type DescriptionQuality string

const (
	// DescQualityFull 有较完整的 JD 正文。
	DescQualityFull DescriptionQuality = "full"
	// DescQualitySnippet 只有搜索摘要，可能不完整。
	DescQualitySnippet DescriptionQuality = "snippet"
	// DescQualityEmpty 无可用正文（网页标题/导航文字被过滤）。
	DescQualityEmpty DescriptionQuality = "empty"
)

// ---------- JD 正文清洗 ----------
//
// 问题背景：StripHTML 把每个块级标签转成换行，导致网页的导航菜单、
// 页脚、侧边推荐等噪声被拆成独立行混入"正文"，表现为 JD 被换行切断、
// 中间夹杂「了解更多」「加入腾讯的 N 个理由」这类无关文字。
//
// 解决：在判定质量之前，先按行剔除已知噪声，再把语义上属于同一段的
// 相邻短行合并，让 JD 恢复为连贯文本。

// noiseLinePatterns 是需要整行剔除的噪声特征。
// 命中即丢弃该行，这些文字在任何岗位 JD 正文中都不该出现。
var noiseLinePatterns = []string{
	// 站点导航 / 结构化菜单
	"首页", "招聘首页", "官网首页", "返回首页", "回到顶部",
	"了解更多", "查看更多", "查看详情", "查看全部", "查看岗位", "查看更多职位",
	"职位列表", "岗位列表", "相关职位", "推荐职位", "热门职位", "相似岗位",
	"上一篇", "下一篇", "上一页", "下一页",
	"登录", "注册", "登录/注册", "登录注册", "立即登录", "请登录",
	"我的简历", "个人中心", "我的投递", "投递记录", "退出登录", "退出",
	// 站点栏目 / 导航项（常见于校招站顶部与侧边）
	"青云计划", "岗位投递", "了解腾讯", "走进腾讯", "人才招聘",
	"招聘公告", "招聘指南", "应聘流程", "面试安排", "Offer",
	"技术分享", "团队介绍", "员工福利", "办公地点",
	// 法律 / 页脚
	"隐私政策", "服务协议", "用户协议", "版权所有", "京ICP", "ICP备",
	"法律声明", "网站地图", "友情链接", "知识产权",
	// 行动号召
	"立即申请", "申请职位", "投递简历", "立即投递", "收藏职位", "分享职位",
}

// marketingLinePatterns 是雇主品牌 / 营销类文案。
// 这类内容整行出现时必定是噪声（JD 正文不会整行都是营销语），
// 因此与导航类分开处理，采用独立且更宽松的长度上限。
var marketingLinePatterns = []string{
	"加入我们", "加入腾讯", "为什么选择", "员工故事", "员工心声",
	"福利关怀", "福利待遇", "公司简介", "关于我们", "关于腾讯",
	"联系我们", "关注我们", "官方微信", "微信公众号", "官方微博",
	"校园招聘", "社会招聘", "实习生招聘", "招聘动态", "招聘流程",
	"求职攻略", "宣讲会", "FAQ", "常见问题",
	"企业文化", "发展历程", "荣誉奖项", "办公环境",
	"关心成长", "人才有活水", "鹅民公社", "腾讯文化日", "腾讯志愿者",
	"健康设施", "全面保障", "安居计划", "开工红包", "Tencent Talk",
}

// noiseLineRegexps 是需要整行剔除的噪声正则（更复杂的形态）。
var noiseLineRegexps = []*regexp.Regexp{
	// markdown 标题行（如 "# 岗位详情 | 腾讯校招"）：整行都是标题，无正文内容。
	// 必须剔除——它不以句末标点结尾，会让后续正文被误判为"续行"而全部粘连。
	regexp.MustCompile(`^#{1,6}\s+.*$`),
	regexp.MustCompile(`^\d{4}[-/年]\d{1,2}[-/月]\d{1,2}[日]?$`), // 纯日期行
	regexp.MustCompile(`^https?://\S*$`),                      // 纯 URL 行
	regexp.MustCompile(`^[\s\-—_*=+|·•]+$`),                   // 纯分隔符号行
}

// CleanJDText 把网页提取文本清洗为连贯的 JD 正文。
//
// 处理流程：
//  1. 逐行剔除噪声（导航、页脚、营销文案）；
//  2. 合并被换行切断的段落：以句末标点结尾的行保持独立，
//     其余相邻行合并为一段，修复"JD 因换行而断掉"的问题；
//  3. 丢弃过短的碎片行（通常是残留的菜单项）。
func CleanJDText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	lines := strings.Split(trimmed, "\n")
	kept := make([]string, 0, len(lines))

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || isNoiseLine(line) {
			continue
		}
		// 过短且不含 JD 实义字符的碎片行直接丢弃（多为残留菜单项）。
		if len([]rune(line)) <= 2 && !strings.ContainsAny(line, "职责要求经验负责") {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) == 0 {
		return ""
	}

	// 合并被换行切断的段落。
	//
	// 判定规则（保守合并，宁可多断行也不粘连）：
	//   - 上一行以句末标点结尾 → 语义已完整，另起一段；
	//   - 当前行是列表项 / 小节标题 → 本来就该独立成行；
	//   - 上一行以逗号、分句等未终结符号结尾，或当前行以小写字母/数字开头
	//     （典型的英文句子被硬换行切断）→ 合并；
	//   - 其余情况一律另起一段，避免把标题与正文粘连。
	var b strings.Builder
	for i, line := range kept {
		if i > 0 {
			prev := kept[i-1]
			if shouldMerge(prev, line) {
				b.WriteString(joiner(prev, line))
			} else {
				b.WriteString("\n")
			}
		}
		b.WriteString(line)
	}

	return strings.TrimSpace(b.String())
}

// shouldMerge 判断当前行是否应与上一行合并为同一段。
func shouldMerge(prev, next string) bool {
	// 上一行语义已终结，不应合并。
	if endsSentence(prev) {
		return false
	}
	// 列表项必须独立成行。
	if startsListMarker(next) {
		return false
	}
	// 上一行以逗号、顿号、分号等未终结符号结尾 → 句子被切断，合并。
	if endsWithContinuation(prev) {
		return true
	}
	// 上一行是「小节标题」（如「* 岗位描述」「岗位职责：」）→ 不应与正文合并。
	if isSectionHeading(prev) {
		return false
	}
	// 上一行以逗号结尾（中文全角）→ 合并。
	pr := []rune(strings.TrimSpace(prev))
	if len(pr) > 0 && (pr[len(pr)-1] == '，' || pr[len(pr)-1] == ',') {
		return true
	}
	// 当前行以小写字母或数字开头，通常是英文被硬换行切断的后半句。
	nr := []rune(strings.TrimSpace(next))
	if len(nr) > 0 {
		c := nr[0]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			return true
		}
	}
	// 中文硬换行切断：上一行不以任何标点结尾（无终结信号），
	// 且当前行不是列表项/新段落起始，说明上一句尚未结束，应当合并。
	if len(pr) > 0 && !isPunctuation(pr[len(pr)-1]) {
		return true
	}
	return false
}

// isSectionHeading 判断是否为小节标题行，如「* 岗位描述」「岗位职责：」「任职要求」。
// 标题语义上是段落起始，不应与后续正文合并到同一行。
func isSectionHeading(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	// markdown 列表符号开头的小节（* 岗位描述）。
	if rs := []rune(t); rs[0] == '*' || rs[0] == '#' {
		return true
	}
	// 以冒号结尾的短行（岗位职责：/ 任职要求：）。
	if len([]rune(t)) <= 12 && endsWithContinuation(t) {
		return true
	}
	// 纯小节名词（岗位描述 / 任职要求 / 工作职责 等短词）。
	if len([]rune(t)) <= 10 && !endsSentence(t) {
		for _, kw := range sectionHeadingKeywords {
			if strings.Contains(t, kw) {
				return true
			}
		}
	}
	return false
}

// sectionHeadingKeywords 是常见的小节标题关键词。
var sectionHeadingKeywords = []string{
	"岗位描述", "职位描述", "工作职责", "岗位职责", "工作内容",
	"任职要求", "职位要求", "任职资格", "岗位要求", "应聘条件",
	"加分项", "优先条件", "我们提供", "薪资福利",
}

// isPunctuation 判断字符是否为标点（含中英文常见标点）。
// 用于识别"行尾是否有语义终结信号"。
func isPunctuation(r rune) bool {
	switch r {
	case '。', '，', '、', '；', '：', '！', '？', '“', '”', '（', '）', '《', '》',
		'.', ',', ';', ':', '!', '?', '"', '\'', '(', ')', '[', ']', '{', '}':
		return true
	}
	return false
}

// endsWithContinuation 判断行尾是否为明显的续行符号（句子未完）。
func endsWithContinuation(s string) bool {
	rs := []rune(strings.TrimSpace(s))
	if len(rs) == 0 {
		return false
	}
	switch rs[len(rs)-1] {
	case '，', ',', '、', '：', ':', '；', ';', '（', '(', '和', '与', '及', '或':
		return true
	}
	return false
}

// isNoiseLine 判断某行是否为需要剔除的噪声。
func isNoiseLine(line string) bool {
	lower := strings.ToLower(line)
	runes := len([]rune(line))

	// 1. 品牌营销类噪声：整行即为营销文案（如「关心成长」「为什么选择腾讯」），
	//    无条件剔除——JD 正文中不会整行都是这类内容。
	for _, p := range marketingLinePatterns {
		if strings.Contains(lower, strings.ToLower(p)) && runes <= 40 {
			return true
		}
	}

	// 2. 导航 / 页脚 / 行动号召类：短行（导航项）直接剔除；
	//    长行可能只是"包含"该词（如 JD 里提到"查看岗位列表"），需更谨慎。
	for _, p := range noiseLinePatterns {
		if strings.Contains(lower, strings.ToLower(p)) && runes <= 30 {
			return true
		}
	}

	// 3. 结构性噪声（纯 URL、纯日期、纯分隔符等）。
	for _, re := range noiseLineRegexps {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

// endsSentence 判断文本是否以句末标点结尾（中英文）。
func endsSentence(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// 按 rune 取末字符，避免截断多字节汉字。
	rs := []rune(s)
	lastChar := rs[len(rs)-1]
	switch lastChar {
	case '。', '；', '！', '？', '.', ';', '!', '?', '：', ':':
		return true
	}
	return false
}

// startsListMarker 判断是否为列表项开头（1. / - / • 等），应另起一行。
func startsListMarker(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// 按 rune 取首字符，避免按字节索引截断多字节符号。
	rs := []rune(s)
	switch rs[0] {
	case '-', '*', '•', '·':
		return true
	}
	// 数字序号开头，如 "1." "2、" "3)"
	if rs[0] >= '0' && rs[0] <= '9' && len(rs) > 1 {
		switch rs[1] {
		case '.', '、', ')', '）':
			return true
		}
	}
	return false
}

// joiner 返回拼接两行时使用的连接符。
// 中英文相邻时补空格，中文之间不补，保持可读。
func joiner(prev, next string) string {
	// 按 rune 取首尾字符，避免截断多字节汉字。
	p := []rune(strings.TrimSpace(prev))
	n := []rune(strings.TrimSpace(next))
	if len(p) == 0 || len(n) == 0 {
		return ""
	}
	lastIsASCII := p[len(p)-1] < 128
	firstIsASCII := n[0] < 128
	// 中英文相邻时补空格；同为 ASCII（英文间）也补空格；中文之间不补。
	if lastIsASCII != firstIsASCII {
		return " "
	}
	if lastIsASCII && firstIsASCII {
		return " "
	}
	return ""
}

// DetectDescriptionQuality 判断 pageText 是否为有效的 JD 正文。
//
// 返回 (清理后的文本, 质量等级)：
//   - full:    文本足够长且不像噪声 → 原样保留
//   - snippet: 文本较短但包含一些 JD 关键词 → 保留但标记为摘要
//   - empty:   文本明显是网页标题/导航 → 清空并标记
func DetectDescriptionQuality(pageText string) (string, DescriptionQuality) {
	trimmed := strings.TrimSpace(pageText)
	if trimmed == "" {
		return "", DescQualityEmpty
	}

	runes := []rune(trimmed)

	// 足够长的文本（>200 字符）大概率是有效内容
	if len(runes) > 200 {
		return trimmed, DescQualityFull
	}

	// 检查是否匹配已知的非 JD 模式
	lower := strings.ToLower(trimmed)
	for _, rule := range nonJDPatterns {
		if rule.isRegex {
			matched, _ := regexp.MatchString(rule.pattern, lower)
			if matched {
				return "", DescQualityEmpty
			}
		} else if strings.Contains(lower, rule.pattern) {
			return "", DescQualityEmpty
		}
	}

	// 较短文本（50-200 字符）：检查是否含 JD 特征关键词
	if len(runes) > 50 {
		jdKeywords := []string{"职责", "要求", "任职", "资格", "经验", "技能", "负责",
			"responsibilities", "requirements", "qualifications", "我们希望", "你需要"}
		for _, kw := range jdKeywords {
			if strings.Contains(lower, kw) {
				return trimmed, DescQualitySnippet
			}
		}
	}

	// 50 字符以下：几乎可以确定不是 JD 正文
	if len(runes) <= 50 {
		return "", DescQualityEmpty
	}

	// 50-200 字符但不含 JD 关键词：标记为 snippet（可能是简短描述）
	return trimmed, DescQualitySnippet
}
