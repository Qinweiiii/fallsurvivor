package site

import (
	"context"
	"log/slog"
	"net/url"
	"strings"
	"sync"
)

// Registry 持有全部站点 Recipe，负责「URL / 公司名 → Recipe」的解析。
//
// 它是 Fast Path 的入口：给定搜索条件，判断是否命中某个已配置的官网站点。
// 未命中则交给下游的通用搜索（Tavily / BOSS）处理。
type Registry struct {
	mu      sync.RWMutex
	byKey   map[string]*Recipe
	repo    *Repository
	loaded  bool
	onError func(msg string, args ...any)
}

// NewRegistry 构造 Registry。repo 可为 nil（纯内存模式，用于测试）。
func NewRegistry(repo *Repository) *Registry {
	return &Registry{
		byKey:   make(map[string]*Recipe),
		repo:    repo,
		onError: slog.Warn,
	}
}

// Load 从数据库装载全部启用的 Recipe 到内存。
// 采集开始前调用一次；配置变更后可再次调用刷新。
func (r *Registry) Load(ctx context.Context) error {
	if r.repo == nil {
		r.mu.Lock()
		r.loaded = true
		r.mu.Unlock()
		return nil
	}
	list, err := r.repo.ListEnabled(ctx)
	if err != nil {
		return err
	}

	next := make(map[string]*Recipe, len(list))
	for i := range list {
		next[list[i].SiteKey] = &list[i]
	}

	r.mu.Lock()
	r.byKey = next
	r.loaded = true
	r.mu.Unlock()

	slog.Info("站点 Recipe 装载完成", "count", len(next))
	return nil
}

// Reload 重新装载（配置变更后调用）。
func (r *Registry) Reload(ctx context.Context) error {
	return r.Load(ctx)
}

// Repository 返回底层仓储，供需要持久化的场景（如保存探索结果）使用。
func (r *Registry) Repository() *Repository {
	return r.repo
}

// Get 按站点标识获取 Recipe。未配置或未启用返回 nil。
func (r *Registry) Get(siteKey string) *Recipe {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rc, ok := r.byKey[siteKey]
	if !ok || rc == nil {
		return nil
	}
	cp := *rc
	return &cp
}

// List 返回全部已装载的 Recipe 副本。
func (r *Registry) List() []Recipe {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Recipe, 0, len(r.byKey))
	for _, rc := range r.byKey {
		out = append(out, *rc)
	}
	return out
}

// ResolveByURL 判断一个 URL 是否属于某个已配置站点，命中则返回对应 Recipe。
//
// 匹配规则：解析 URL 的 host，去掉 www. 前缀后，与 Recipe.Domain 做
// 精确匹配或「以 .domain 结尾」的子域匹配。
// 例如 join.qq.com 的 Recipe 可匹配 careers.join.qq.com。
func (r *Registry) ResolveByURL(rawURL string) *Recipe {
	host := hostOf(rawURL)
	if host == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, rc := range r.byKey {
		if domainMatches(host, rc.Domain) {
			cp := *rc
			return &cp
		}
	}
	return nil
}

// ResolveByCompany 按公司名匹配 Recipe（大小写不敏感、去空格后比较）。
// 用于在只有公司名、没有 URL 的场景下走官网 Fast Path。
func (r *Registry) ResolveByCompany(company string) *Recipe {
	target := normalizeName(company)
	if target == "" {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, rc := range r.byKey {
		if normalizeName(rc.CompanyName) == target || normalizeName(rc.SiteKey) == target {
			cp := *rc
			return &cp
		}
	}
	return nil
}

// Match 按站点标识 / URL / 公司名依次尝试解析，任一命中即返回。
// 这是 Fast Path 的统一入口：调用方不必关心用户给的是哪种形式。
func (r *Registry) Match(siteKey, rawURL, company string) *Recipe {
	if sk := strings.TrimSpace(siteKey); sk != "" {
		if rc := r.Get(sk); rc != nil {
			return rc
		}
	}
	if rc := r.ResolveByURL(rawURL); rc != nil {
		return rc
	}
	return r.ResolveByCompany(company)
}

// hostOf 提取 URL 的主机名（小写、去端口）。解析失败返回空串。
func hostOf(rawURL string) string {
	if strings.TrimSpace(rawURL) == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// domainMatches 判断 host 是否属于 domain（支持子域）。
func domainMatches(host, domain string) bool {
	host = strings.TrimPrefix(strings.ToLower(host), "www.")
	domain = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(domain)), "www.")
	if host == "" || domain == "" {
		return false
	}
	if host == domain {
		return true
	}
	return strings.HasSuffix(host, "."+domain)
}

// normalizeName 归一化名称用于比较：小写并去掉所有空白。
func normalizeName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), ""))
}
