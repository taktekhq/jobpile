package himalayas

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func TestSearchParsesMixedFixture(t *testing.T) {
	fixture := readFixture(t, "search_jobs_mixed.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs/api/search" {
			t.Errorf("path = %q, want /jobs/api/search", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("q") != "engineer" {
			t.Errorf("q param = %q, want engineer", q.Get("q"))
		}
		if q.Get("sort") != "recent" {
			t.Errorf("sort param = %q, want recent", q.Get("sort"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	result, err := c.Search(context.Background(), SearchParams{Query: "engineer", Sort: "recent", Page: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(result.Jobs) != 6 {
		t.Fatalf("len(Jobs) = %d, want 6", len(result.Jobs))
	}

	byTitle := map[string]RawJob{}
	for _, j := range result.Jobs {
		byTitle[j.Title] = j
	}

	staff, ok := byTitle["Staff Engineer"]
	if !ok {
		t.Fatal("missing Staff Engineer fixture job")
	}
	if len(staff.LocationRestrictions) != 0 {
		t.Errorf("Staff Engineer LocationRestrictions = %v, want empty (worldwide)", staff.LocationRestrictions)
	}
	if staff.MinSalary != nil {
		t.Errorf("Staff Engineer MinSalary = %v, want nil", staff.MinSalary)
	}

	mobile, ok := byTitle["Senior Mobile Developer | Remote - Contract"]
	if !ok {
		t.Fatal("missing Senior Mobile Developer fixture job")
	}
	if mobile.SalaryPeriod != "hourly" || mobile.MinSalary == nil || *mobile.MinSalary != 50 {
		t.Errorf("mobile dev salary = %v %v %q, want 50 hourly", mobile.MinSalary, mobile.MaxSalary, mobile.SalaryPeriod)
	}
}

func TestToModelWorldwideJob(t *testing.T) {
	fixture := readFixture(t, "search_jobs_mixed.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(fixture) }))
	defer srv.Close()
	c := NewClient(srv.URL)
	result, err := c.Search(context.Background(), SearchParams{Query: "engineer"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	var staff RawJob
	for _, j := range result.Jobs {
		if j.Title == "Staff Engineer" {
			staff = j
		}
	}
	job := ToModel(staff, map[string]float64{"USD": 1})
	if job.AllowedRegions != nil {
		t.Errorf("AllowedRegions = %v, want nil (worldwide)", job.AllowedRegions)
	}
	if job.RemotePolicy != "remote" {
		t.Errorf("RemotePolicy = %q, want remote", job.RemotePolicy)
	}
	if job.Source != "himalayas" {
		t.Errorf("Source = %q, want himalayas", job.Source)
	}
	if job.URL == "" || job.ApplyURL == "" {
		t.Error("URL and ApplyURL must be set from guid/applicationLink")
	}
}

func TestToModelConvertsHourlySalary(t *testing.T) {
	fixture := readFixture(t, "search_jobs_mixed.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(fixture) }))
	defer srv.Close()
	c := NewClient(srv.URL)
	result, err := c.Search(context.Background(), SearchParams{Query: "engineer"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	var mobile RawJob
	for _, j := range result.Jobs {
		if j.Title == "Senior Mobile Developer | Remote - Contract" {
			mobile = j
		}
	}
	job := ToModel(mobile, map[string]float64{"USD": 1})
	if job.SalaryMinUSD == nil || *job.SalaryMinUSD != 50*2080 {
		t.Errorf("SalaryMinUSD = %v, want %v", job.SalaryMinUSD, 50*2080.0)
	}
	if job.SalaryMaxUSD == nil || *job.SalaryMaxUSD != 100*2080 {
		t.Errorf("SalaryMaxUSD = %v, want %v", job.SalaryMaxUSD, 100*2080.0)
	}
}

func TestToModelUSOnlyJob(t *testing.T) {
	fixture := readFixture(t, "search_jobs_mixed.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(fixture) }))
	defer srv.Close()
	c := NewClient(srv.URL)
	result, err := c.Search(context.Background(), SearchParams{Query: "engineer"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	var em RawJob
	for _, j := range result.Jobs {
		if j.Title == "Engineering Manager" {
			em = j
		}
	}
	job := ToModel(em, map[string]float64{"USD": 1})
	if len(job.AllowedRegions) != 1 || job.AllowedRegions[0] != "United States" {
		t.Errorf("AllowedRegions = %v, want [United States]", job.AllowedRegions)
	}
	if job.SalaryMaxUSD == nil || *job.SalaryMaxUSD != 234000 {
		t.Errorf("SalaryMaxUSD = %v, want 234000", job.SalaryMaxUSD)
	}
}

func TestTimezoneRestrictionsWithoutGMT3IsNotWorldwide(t *testing.T) {
	job := ToModel(RawJob{
		Title:                "Remote Engineer, Americas Only",
		CompanyName:          "Acme",
		GUID:                 "https://himalayas.app/companies/acme/jobs/remote-engineer",
		ApplicationLink:      "https://himalayas.app/companies/acme/jobs/remote-engineer",
		TimezoneRestrictions: []float64{-10, -9, -8, -7, -6, -5, 14},
	}, map[string]float64{"USD": 1})
	if job.AllowedRegions == nil {
		t.Error("AllowedRegions should not be nil when the timezone window excludes GMT+3")
	}
}

func TestTimezoneRestrictionsCoveringGMT3IsWorldwide(t *testing.T) {
	job := ToModel(RawJob{
		Title:                "Remote Engineer, Wide Window",
		CompanyName:          "Acme",
		GUID:                 "https://himalayas.app/companies/acme/jobs/remote-engineer-2",
		ApplicationLink:      "https://himalayas.app/companies/acme/jobs/remote-engineer-2",
		TimezoneRestrictions: []float64{-5, -4, -3, -2, -1, 0, 1, 2, 3, 4, 5},
	}, map[string]float64{"USD": 1})
	if job.AllowedRegions != nil {
		t.Errorf("AllowedRegions = %v, want nil when GMT+3 is within the timezone window", job.AllowedRegions)
	}
}

func TestDescriptionTextStripsHTML(t *testing.T) {
	r := RawJob{Description: "<div><p>Hello <strong>world</strong></p></div>"}
	if got := r.DescriptionText(); got != "Hello world" {
		t.Errorf("DescriptionText() = %q, want %q", got, "Hello world")
	}
}

func TestSearchReturnsHTTPStatusErrorOn403And429(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		c := NewClient(srv.URL)
		_, err := c.Search(context.Background(), SearchParams{Query: "engineer"})
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: want error, got nil", code)
		}
		statusErr, ok := err.(*HTTPStatusError)
		if !ok {
			t.Fatalf("status %d: want *HTTPStatusError, got %T: %v", code, err, err)
		}
		if statusErr.StatusCode != code {
			t.Errorf("StatusCode = %d, want %d", statusErr.StatusCode, code)
		}
	}
}
