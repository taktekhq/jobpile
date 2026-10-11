package remoet

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

func TestSearchJobsParsesEnrichedFixture(t *testing.T) {
	fixture := readFixture(t, "search_jobs_enriched.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key")
	result, err := c.SearchJobs(context.Background(), 200, 50, nil)
	if err != nil {
		t.Fatalf("SearchJobs: %v", err)
	}
	if result.TotalCount != 15815 {
		t.Errorf("TotalCount = %d, want 15815", result.TotalCount)
	}
	if len(result.Jobs) != 8 {
		t.Fatalf("len(Jobs) = %d, want 8", len(result.Jobs))
	}

	byTitle := map[string]RawJob{}
	for _, j := range result.Jobs {
		byTitle[j.Title] = j
	}

	greptile, ok := byTitle["Security Engineer"]
	if !ok {
		t.Fatal("missing Security Engineer fixture job")
	}
	if greptile.Company != "Greptile" {
		t.Errorf("company = %q, want Greptile", greptile.Company)
	}
	if deref(greptile.RemotePolicy) != "remote" {
		t.Errorf("remotePolicy = %v, want remote", greptile.RemotePolicy)
	}
	if greptile.RemoteRestrictions != nil {
		t.Errorf("remoteRestrictions = %v, want nil (worldwide)", greptile.RemoteRestrictions)
	}
	if greptile.IsUnenriched() {
		t.Error("Security Engineer has remotePolicy set, should not be unenriched")
	}

	reddit, ok := byTitle["Senior Data Scientist, Ads Integrity"]
	if !ok {
		t.Fatal("missing Reddit fixture job")
	}
	if reddit.Salary == nil || reddit.Salary.From != 190800 || reddit.Salary.To != 267100 {
		t.Errorf("salary = %+v, want 190800-267100", reddit.Salary)
	}
	if deref(reddit.RemoteRestrictions) != "US only" {
		t.Errorf("remoteRestrictions = %q, want %q", deref(reddit.RemoteRestrictions), "US only")
	}
}

func TestSearchJobsParsesUnenrichedFixture(t *testing.T) {
	fixture := readFixture(t, "search_jobs_unenriched.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(fixture)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key")
	result, err := c.SearchJobs(context.Background(), 1, 5, nil)
	if err != nil {
		t.Fatalf("SearchJobs: %v", err)
	}
	if len(result.Jobs) != 3 {
		t.Fatalf("len(Jobs) = %d, want 3", len(result.Jobs))
	}
	for _, j := range result.Jobs {
		if !j.IsUnenriched() {
			t.Errorf("job %q should be unenriched (every enriched field null)", j.Title)
		}
	}

	model := ToModel(result.Jobs[0], map[string]float64{"USD": 1})
	if model.RuleVerdict != "pending" {
		t.Errorf("RuleVerdict = %q, want pending for an unenriched job", model.RuleVerdict)
	}
}

func TestSearchJobsHandlesSSEFraming(t *testing.T) {
	fixture := readFixture(t, "search_jobs_sse.txt")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write(fixture)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key")
	result, err := c.SearchJobs(context.Background(), 200, 2, nil)
	if err != nil {
		t.Fatalf("SearchJobs over SSE: %v", err)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("len(Jobs) = %d, want 2", len(result.Jobs))
	}
}

func TestInitializeCapturesSessionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("mcp-session-id", "session-abc-123")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key")
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if c.SessionID != "session-abc-123" {
		t.Errorf("SessionID = %q, want session-abc-123", c.SessionID)
	}
}

func TestInitializeToleratesMissingSessionID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key")
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize without a session id header: %v", err)
	}
	if c.SessionID != "" {
		t.Errorf("SessionID = %q, want empty", c.SessionID)
	}
}

func TestDoReturnsHTTPStatusErrorOn403And429(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		c := NewClient(srv.URL, "test-key")
		_, err := c.SearchJobs(context.Background(), 1, 5, nil)
		srv.Close()
		if err == nil {
			t.Fatalf("status %d: want error, got nil", code)
		}
		var statusErr *HTTPStatusError
		if !asHTTPStatusError(err, &statusErr) {
			t.Fatalf("status %d: want *HTTPStatusError, got %T: %v", code, err, err)
		}
		if statusErr.StatusCode != code {
			t.Errorf("StatusCode = %d, want %d", statusErr.StatusCode, code)
		}
	}
}

func asHTTPStatusError(err error, target **HTTPStatusError) bool {
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		if se, ok := err.(*HTTPStatusError); ok {
			*target = se
			return true
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestGetAccountParsesBudget(t *testing.T) {
	// Synthetic payload (shape only, not a real account snapshot): the
	// real get_account response reflects a specific account's live
	// usage, which is not job-board data and should not be committed.
	body := `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"{\"mcpRequestsToday\":{\"used\":22,\"limit\":5000,\"remaining\":4978}}"}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key")
	acc, err := c.GetAccount(context.Background())
	if err != nil {
		t.Fatalf("GetAccount: %v", err)
	}
	if acc.MCPRequestsToday.Used != 22 || acc.MCPRequestsToday.Remaining != 4978 {
		t.Errorf("account = %+v", acc)
	}
}

func TestToModelConvertsSalaryAndID(t *testing.T) {
	fixture := readFixture(t, "search_jobs_enriched.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(fixture)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-key")
	result, err := c.SearchJobs(context.Background(), 200, 50, nil)
	if err != nil {
		t.Fatalf("SearchJobs: %v", err)
	}

	var reddit RawJob
	for _, j := range result.Jobs {
		if j.Company == "Reddit" {
			reddit = j
		}
	}
	job := ToModel(reddit, map[string]float64{"USD": 1})
	if job.ID != "remoet-"+reddit.ID {
		t.Errorf("ID = %q, want prefixed with remoet-", job.ID)
	}
	if job.SalaryMaxUSD == nil || *job.SalaryMaxUSD != 267100 {
		t.Errorf("SalaryMaxUSD = %v, want 267100", job.SalaryMaxUSD)
	}
	if len(job.AllowedRegions) != 1 || job.AllowedRegions[0] != "US only" {
		t.Errorf("AllowedRegions = %v, want [\"US only\"]", job.AllowedRegions)
	}
}
