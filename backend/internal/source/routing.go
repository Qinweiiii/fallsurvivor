package source

import "strings"

var broadCompanyPrefs = map[string]bool{
	"大厂":  true,
	"外企":  true,
	"互联网": true,
	"国企":  true,
	"央企":  true,
	"银行":  true,
	"券商":  true,
	"不限":  true,
	"无偏好": true,
}

func hasBroadCompanyPref(prefs []string) bool {
	for _, pref := range prefs {
		if broadCompanyPrefs[normalizeCompanyName(pref)] {
			return true
		}
	}
	return false
}

func specificCompanyPrefs(prefs []string) []string {
	out := make([]string, 0, len(prefs))
	seen := map[string]bool{}
	for _, pref := range prefs {
		pref = strings.TrimSpace(pref)
		key := normalizeCompanyName(pref)
		if key == "" || broadCompanyPrefs[key] || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, pref)
	}
	return out
}

func companyMatches(pref, company string) bool {
	p := normalizeCompanyName(pref)
	c := normalizeCompanyName(company)
	if p == "" || c == "" {
		return false
	}
	return p == c || strings.Contains(p, c) || strings.Contains(c, p)
}

func adapterMatchesAny(a CompanyAdapter, companies []string) bool {
	for _, company := range companies {
		if companyMatches(company, a.Company()) {
			return true
		}
	}
	return false
}

func knownOfficialCompany(company string, adapters []CompanyAdapter) bool {
	for _, a := range adapters {
		if companyMatches(company, a.Company()) {
			return true
		}
	}
	return false
}

func unknownOfficialCompanies(prefs []string, adapters []CompanyAdapter) []string {
	specific := specificCompanyPrefs(prefs)
	out := make([]string, 0, len(specific))
	for _, company := range specific {
		if !knownOfficialCompany(company, adapters) {
			out = append(out, company)
		}
	}
	return out
}

func normalizeCompanyName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "　", "")
	return replacer.Replace(s)
}
