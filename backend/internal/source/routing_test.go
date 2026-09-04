package source

import (
	"context"
	"testing"
)

type fakeSource struct{}

func (fakeSource) Type() string { return "fake" }

func (fakeSource) Name() string { return "fake" }

func (fakeSource) Available() bool { return true }

func (fakeSource) Search(context.Context, SearchQuery) ([]RawJob, error) { return nil, nil }

func TestOfficialSelectAdaptersUsesSpecificKnownCompany(t *testing.T) {
	s := NewOfficialSource(fakeSource{}, DefaultCompanyAdapters, 6)

	got := s.selectAdapters([]string{"京东"})
	if len(got) != 1 || got[0].Company() != "京东" {
		t.Fatalf("selectAdapters(京东) = %#v, want only 京东", adapterNames(got))
	}
}

func TestOfficialSelectAdaptersSkipsUnknownSpecificCompany(t *testing.T) {
	s := NewOfficialSource(fakeSource{}, DefaultCompanyAdapters, 6)

	got := s.selectAdapters([]string{"某某创业公司"})
	if len(got) != 0 {
		t.Fatalf("selectAdapters(unknown) = %#v, want empty", adapterNames(got))
	}
}

func TestBossQueriesSkipSpecificKnownOfficialCompany(t *testing.T) {
	got := buildBossQueries(SearchQuery{
		Queries:            []string{"2027届 后端 深圳"},
		CompanyPreferences: []string{"腾讯"},
	})
	if len(got) != 0 {
		t.Fatalf("buildBossQueries(腾讯) = %#v, want empty", got)
	}
}

func TestBossQueriesUseUnknownSpecificCompany(t *testing.T) {
	got := buildBossQueries(SearchQuery{
		Queries:            []string{"2027届 后端 深圳"},
		CompanyPreferences: []string{"某某创业公司"},
	})
	want := "某某创业公司 2027届 后端 深圳 site:zhipin.com"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("buildBossQueries(unknown) = %#v, want %#v", got, []string{want})
	}
}

func TestBossQueriesUseGenericForBroadPreference(t *testing.T) {
	got := buildBossQueries(SearchQuery{
		Queries:            []string{"2027届 后端 深圳"},
		CompanyPreferences: []string{"大厂"},
	})
	want := "2027届 后端 深圳 site:zhipin.com"
	if len(got) != 1 || got[0] != want {
		t.Fatalf("buildBossQueries(大厂) = %#v, want %#v", got, []string{want})
	}
}

func adapterNames(adapters []CompanyAdapter) []string {
	out := make([]string, 0, len(adapters))
	for _, a := range adapters {
		out = append(out, a.Company())
	}
	return out
}
