package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/taktekhq/jobpile/internal/model"
	"github.com/taktekhq/jobpile/internal/store"
)

func usd(v float64) *float64 { return &v }

func TestFormatJobLineNewJob(t *testing.T) {
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	j := model.Job{
		Company: "Acme", Title: "Staff Backend Engineer",
		SalaryMinUSD: usd(160000), SalaryMaxUSD: usd(190000),
		Tier: 1, FirstSeen: now, LastSeen: now,
		ApplyURL:    "https://acme.example/apply",
		JudgeReason: "Go platform team, async, hires contractors anywhere.",
	}
	line := formatJobLine(j)
	if !strings.HasPrefix(line, "- [NEW] ") {
		t.Errorf("line should start with - [NEW], got %q", line)
	}
	for _, want := range []string{"Acme", "Staff Backend Engineer", "$160k-$190k", "tier 1", "2026-10-12", "https://acme.example/apply", "Why: Go platform team"} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q missing %q", line, want)
		}
	}
}

func TestFormatJobLineNotNew(t *testing.T) {
	j := model.Job{
		Company: "Acme", Title: "Staff Engineer",
		FirstSeen: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
		LastSeen:  time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC),
	}
	line := formatJobLine(j)
	if strings.HasPrefix(line, "- [NEW]") {
		t.Errorf("a job seen again on a later run should not be [NEW]: %q", line)
	}
}

func TestSalaryLabel(t *testing.T) {
	cases := []struct {
		job  model.Job
		want string
	}{
		{model.Job{}, "salary not listed"},
		{model.Job{SalaryMinUSD: usd(100000), SalaryMaxUSD: usd(150000)}, "$100k-$150k"},
		{model.Job{SalaryMaxUSD: usd(150000)}, "up to $150k"},
		{model.Job{SalaryMinUSD: usd(100000)}, "from $100k"},
	}
	for _, c := range cases {
		if got := salaryLabel(c.job); got != c.want {
			t.Errorf("salaryLabel(%+v) = %q, want %q", c.job, got, c.want)
		}
	}
}

func TestSortForOutputTierThenPay(t *testing.T) {
	jobs := []model.Job{
		{Company: "LowPayTier1", Tier: 1, SalaryMaxUSD: usd(90000)},
		{Company: "HighPayTier2", Tier: 2, SalaryMaxUSD: usd(500000)},
		{Company: "HighPayTier1", Tier: 1, SalaryMaxUSD: usd(200000)},
		{Company: "NoSalaryTier1", Tier: 1},
	}
	sortForOutput(jobs)
	var order []string
	for _, j := range jobs {
		order = append(order, j.Company)
	}
	want := []string{"HighPayTier1", "LowPayTier1", "NoSalaryTier1", "HighPayTier2"}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("order = %v, want %v", order, want)
			break
		}
	}
}

func TestWriteSectionedListCreatesHeadingsAndCounts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.md")
	jobs := map[string][]model.Job{
		"Apply": {{Company: "Acme", Title: "Staff Engineer", Tier: 1}},
	}
	if err := writeSectionedList(path, []string{"Apply"}, jobs); err != nil {
		t.Fatalf("writeSectionedList: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "# Apply (1)") {
		t.Errorf("missing heading with count: %q", content)
	}
	if !strings.Contains(content, "Acme") {
		t.Errorf("missing job line: %q", content)
	}
}

func TestWriteSectionedListEmptySection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.md")
	if err := writeSectionedList(path, []string{"Apply"}, map[string][]model.Job{"Apply": nil}); err != nil {
		t.Fatalf("writeSectionedList: %v", err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "Nothing here.") {
		t.Errorf("empty section should say so: %q", string(b))
	}
}

func TestRunListSeparatesRuleAndJudgeSkips(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "jobs.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	now := time.Now()

	ruleSkip := model.Job{ID: "remoet-1", Source: "remoet", SourceID: "1", Company: "A", Title: "Junior Engineer"}
	st.UpsertFetched(&ruleSkip, now)
	st.SaveMatchResult(ruleSkip.ID, "skip", "title: excluded junior", 0)

	judgeSkip := model.Job{ID: "remoet-2", Source: "remoet", SourceID: "2", Company: "B", Title: "Staff Engineer"}
	st.UpsertFetched(&judgeSkip, now)
	st.SaveMatchResult(judgeSkip.ID, "pass", "ok", 1)
	st.SaveJudgeResult(judgeSkip.ID, "skip", "requires clearance", "apply_url_html", now)

	applyJob := model.Job{ID: "remoet-3", Source: "remoet", SourceID: "3", Company: "C", Title: "Principal Engineer"}
	st.UpsertFetched(&applyJob, now)
	st.SaveMatchResult(applyJob.ID, "pass", "ok", 1)
	st.SaveJudgeResult(applyJob.ID, "apply", "great fit", "board:greenhouse", now)

	counts, err := runList(st, dir)
	if err != nil {
		t.Fatalf("runList: %v", err)
	}
	if counts[0] != 1 {
		t.Errorf("apply count = %d, want 1", counts[0])
	}
	if counts[2] != 1 {
		t.Errorf("rule skip count = %d, want 1", counts[2])
	}
	if counts[3] != 1 {
		t.Errorf("judge skip count = %d, want 1", counts[3])
	}

	skipContent, err := os.ReadFile(filepath.Join(dir, "skip.md"))
	if err != nil {
		t.Fatalf("reading skip.md: %v", err)
	}
	s := string(skipContent)
	if !strings.Contains(s, "Skipped by the rules") || !strings.Contains(s, "Skipped by the judge") {
		t.Errorf("skip.md missing separate headings: %q", s)
	}
	ruleIdx := strings.Index(s, "Skipped by the rules")
	judgeIdx := strings.Index(s, "Skipped by the judge")
	aIdx := strings.Index(s, "Junior Engineer")
	bIdx := strings.Index(s, "Staff Engineer")
	if !(ruleIdx < aIdx && aIdx < judgeIdx && judgeIdx < bIdx) {
		t.Errorf("rule-skip job should appear under the rule heading, judge-skip job under the judge heading: %q", s)
	}

	applyContent, err := os.ReadFile(filepath.Join(dir, "apply.md"))
	if err != nil {
		t.Fatalf("reading apply.md: %v", err)
	}
	if !strings.Contains(string(applyContent), "Principal Engineer") {
		t.Errorf("apply.md missing applied job: %q", string(applyContent))
	}
}

func TestWriteMaybeListNotesPendingCountWithoutListingThem(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maybe.md")
	maybeJobs := []model.Job{{Company: "Acme", Title: "Staff Engineer", Tier: 1}}
	if err := writeMaybeList(path, maybeJobs, 1208); err != nil {
		t.Fatalf("writeMaybeList: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading maybe.md: %v", err)
	}
	content := string(b)
	if !strings.Contains(content, "# Maybe (1)") {
		t.Errorf("missing maybe count heading: %q", content)
	}
	if !strings.Contains(content, "Acme") {
		t.Errorf("missing the actual maybe job: %q", content)
	}
	if !strings.Contains(content, "1208 more roles") {
		t.Errorf("missing pending count note: %q", content)
	}
	if strings.Count(content, "- ") != 1 {
		t.Errorf("pending jobs should not be listed line by line, got: %q", content)
	}
}

func TestWriteMaybeListEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "maybe.md")
	if err := writeMaybeList(path, nil, 0); err != nil {
		t.Fatalf("writeMaybeList: %v", err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "Nothing here.") {
		t.Errorf("empty maybe list should say so: %q", string(b))
	}
}
