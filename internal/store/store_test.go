package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/taktekhq/jobpile/internal/model"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleJob() *model.Job {
	return &model.Job{
		ID:       "remoet-1",
		Source:   "remoet",
		SourceID: "1",
		Company:  "Acme Corp",
		Title:    "Staff Backend Engineer",
		URL:      "https://remoet.dev/jobs/1",
		ApplyURL: "https://acme.example/apply/1",
		TextHash: "hash-a",
	}
}

func TestUpsertFetchedInsertsNew(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	job := sampleJob()

	res, err := s.UpsertFetched(job, now)
	if err != nil {
		t.Fatalf("UpsertFetched: %v", err)
	}
	if !res.IsNew {
		t.Error("IsNew = false, want true for a brand new job")
	}

	got, err := s.GetByID("remoet-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Company != "Acme Corp" || got.Title != "Staff Backend Engineer" {
		t.Errorf("got %+v", got)
	}
	if !got.FirstSeen.Equal(got.LastSeen) {
		t.Error("FirstSeen and LastSeen should be equal right after insert")
	}
}

func TestUpsertFetchedUpdatesSameID(t *testing.T) {
	s := openTestStore(t)
	t0 := time.Now().Add(-24 * time.Hour)
	t1 := time.Now()

	job := sampleJob()
	if _, err := s.UpsertFetched(job, t0); err != nil {
		t.Fatalf("first UpsertFetched: %v", err)
	}

	job.Title = "Principal Backend Engineer"
	res, err := s.UpsertFetched(job, t1)
	if err != nil {
		t.Fatalf("second UpsertFetched: %v", err)
	}
	if res.IsNew {
		t.Error("IsNew = true on update, want false")
	}

	got, err := s.GetByID("remoet-1")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Title != "Principal Backend Engineer" {
		t.Errorf("Title = %q, want updated title", got.Title)
	}
	if got.FirstSeen.Equal(got.LastSeen) {
		t.Error("FirstSeen should stay at t0, LastSeen should move to t1")
	}
}

func TestUpsertFetchedResetsJudgeOnTextChange(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	job := sampleJob()
	job.RuleVerdict = "pass"

	if _, err := s.UpsertFetched(job, now); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.SaveJudgeResult(job.ID, "apply", "looks great", now); err != nil {
		t.Fatalf("SaveJudgeResult: %v", err)
	}

	job.TextHash = "hash-b"
	res, err := s.UpsertFetched(job, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("update with new text: %v", err)
	}
	if !res.TextChanged {
		t.Error("TextChanged = false, want true")
	}

	got, err := s.GetByID(job.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.JudgeVerdict != "" {
		t.Errorf("JudgeVerdict = %q, want empty after text change", got.JudgeVerdict)
	}
}

func TestUpsertFetchedMergesByCompanyAndTitle(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()

	remoetJob := sampleJob()
	if _, err := s.UpsertFetched(remoetJob, now); err != nil {
		t.Fatalf("insert remoet job: %v", err)
	}

	himalayasJob := &model.Job{
		ID:       "himalayas-9",
		Source:   "himalayas",
		SourceID: "9",
		Company:  "  acme corp  ",
		Title:    "staff   backend engineer",
		URL:      "https://himalayas.app/jobs/9",
		ApplyURL: "https://acme.example/apply/9",
		TextHash: "hash-c",
	}
	res, err := s.UpsertFetched(himalayasJob, now)
	if err != nil {
		t.Fatalf("insert himalayas job: %v", err)
	}
	if !res.Merged {
		t.Error("Merged = false, want true for same company+title from another source")
	}
	if res.CanonicalID != "remoet-1" {
		t.Errorf("CanonicalID = %q, want remoet-1", res.CanonicalID)
	}

	// Only one row should exist for this role.
	if _, err := s.GetByID("himalayas-9"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("GetByID(himalayas-9) err = %v, want sql.ErrNoRows (no separate row)", err)
	}

	canonical, err := s.GetByID("remoet-1")
	if err != nil {
		t.Fatalf("GetByID(remoet-1): %v", err)
	}
	if canonical.AltSource != "himalayas" || canonical.AltURL != "https://himalayas.app/jobs/9" {
		t.Errorf("canonical alt fields not set: %+v", canonical)
	}
}

func TestMarkMissingClosesAfterTwoRuns(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	job := sampleJob()
	if _, err := s.UpsertFetched(job, now); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// Run 1: job missing from the source's results.
	closed, err := s.MarkMissing("remoet", map[string]bool{}, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("MarkMissing run 1: %v", err)
	}
	if closed != 0 {
		t.Errorf("closed after run 1 = %d, want 0", closed)
	}
	got, _ := s.GetByID(job.ID)
	if got.ClosedAt != nil {
		t.Error("job closed after only one missed run")
	}

	// Run 2: still missing.
	closed, err = s.MarkMissing("remoet", map[string]bool{}, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("MarkMissing run 2: %v", err)
	}
	if closed != 1 {
		t.Errorf("closed after run 2 = %d, want 1", closed)
	}
	got, _ = s.GetByID(job.ID)
	if got.ClosedAt == nil {
		t.Error("job should be closed after two missed runs")
	}
}

func TestMarkMissingResetsWhenSeenAgain(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	job := sampleJob()
	if _, err := s.UpsertFetched(job, now); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if _, err := s.MarkMissing("remoet", map[string]bool{}, now.Add(time.Hour)); err != nil {
		t.Fatalf("MarkMissing run 1: %v", err)
	}
	// Seen again on run 2 via a fresh UpsertFetched (simulating a normal fetch).
	if _, err := s.UpsertFetched(job, now.Add(2*time.Hour)); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ := s.GetByID(job.ID)
	if got.MissedRuns != 0 {
		t.Errorf("MissedRuns = %d, want 0 after being seen again", got.MissedRuns)
	}
}

func TestPassedUnjudged(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()

	passed := sampleJob()
	if _, err := s.UpsertFetched(passed, now); err != nil {
		t.Fatalf("insert passed: %v", err)
	}
	if err := s.SaveMatchResult(passed.ID, "pass", "ok", 1); err != nil {
		t.Fatalf("SaveMatchResult: %v", err)
	}

	skipped := &model.Job{ID: "remoet-2", Source: "remoet", SourceID: "2", Company: "Other", Title: "Junior Engineer"}
	if _, err := s.UpsertFetched(skipped, now); err != nil {
		t.Fatalf("insert skipped: %v", err)
	}
	if err := s.SaveMatchResult(skipped.ID, "skip", "junior", 0); err != nil {
		t.Fatalf("SaveMatchResult: %v", err)
	}

	alreadyJudged := &model.Job{ID: "remoet-3", Source: "remoet", SourceID: "3", Company: "Third", Title: "Staff Engineer"}
	if _, err := s.UpsertFetched(alreadyJudged, now); err != nil {
		t.Fatalf("insert judged: %v", err)
	}
	if err := s.SaveMatchResult(alreadyJudged.ID, "pass", "ok", 1); err != nil {
		t.Fatalf("SaveMatchResult: %v", err)
	}
	if err := s.SaveJudgeResult(alreadyJudged.ID, "apply", "good fit", now); err != nil {
		t.Fatalf("SaveJudgeResult: %v", err)
	}

	jobs, err := s.PassedUnjudged(0)
	if err != nil {
		t.Fatalf("PassedUnjudged: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != passed.ID {
		t.Errorf("PassedUnjudged = %v, want just %s", jobs, passed.ID)
	}
}

func TestSummarize(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()

	j1 := sampleJob()
	s.UpsertFetched(j1, now)
	s.SaveMatchResult(j1.ID, "pass", "ok", 1)
	s.SaveJudgeResult(j1.ID, "apply", "good", now)

	j2 := &model.Job{ID: "remoet-2", Source: "remoet", SourceID: "2", Company: "B", Title: "Junior"}
	s.UpsertFetched(j2, now)
	s.SaveMatchResult(j2.ID, "skip", "junior", 0)

	c, err := s.Summarize()
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if c.Total != 2 || c.Open != 2 {
		t.Errorf("Total/Open = %d/%d, want 2/2", c.Total, c.Open)
	}
	if c.RulePassed != 1 || c.RuleSkipped != 1 {
		t.Errorf("RulePassed/RuleSkipped = %d/%d, want 1/1", c.RulePassed, c.RuleSkipped)
	}
	if c.JudgeApply != 1 {
		t.Errorf("JudgeApply = %d, want 1", c.JudgeApply)
	}
}

func TestUpsertFetchedPendingThenEnriched(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()

	pending := &model.Job{
		ID: "remoet-99", Source: "remoet", SourceID: "99",
		Company: "NewCo", Title: "Head of Support",
		RuleVerdict: "pending", RuleReason: "remoet has not enriched this role yet",
	}
	if _, err := s.UpsertFetched(pending, now); err != nil {
		t.Fatalf("insert pending: %v", err)
	}

	got, err := s.GetByID(pending.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.RuleVerdict != "pending" {
		t.Errorf("RuleVerdict = %q, want pending", got.RuleVerdict)
	}

	unmatched, err := s.AllUnmatched()
	if err != nil {
		t.Fatalf("AllUnmatched: %v", err)
	}
	if len(unmatched) != 0 {
		t.Errorf("AllUnmatched should skip pending jobs, got %d", len(unmatched))
	}
	pendingList, err := s.Pending()
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pendingList) != 1 {
		t.Errorf("Pending() = %d jobs, want 1", len(pendingList))
	}

	// Next fetch: remoet enriched it. Re-upsert without RuleVerdict set
	// (a fresh, enriched conversion), still missing on-disk text_hash so
	// textChanged doesn't matter here.
	enriched := &model.Job{
		ID: "remoet-99", Source: "remoet", SourceID: "99",
		Company: "NewCo", Title: "Head of Support", RemotePolicy: "remote",
	}
	if _, err := s.UpsertFetched(enriched, now.Add(time.Hour)); err != nil {
		t.Fatalf("re-upsert enriched: %v", err)
	}

	got2, err := s.GetByID(pending.ID)
	if err != nil {
		t.Fatalf("GetByID after enrichment: %v", err)
	}
	if got2.RuleVerdict != "" {
		t.Errorf("RuleVerdict after enrichment = %q, want empty so match runs", got2.RuleVerdict)
	}

	unmatched2, err := s.AllUnmatched()
	if err != nil {
		t.Fatalf("AllUnmatched after enrichment: %v", err)
	}
	if len(unmatched2) != 1 {
		t.Errorf("AllUnmatched after enrichment = %d, want 1", len(unmatched2))
	}
}

func TestUpsertFetchedPreservesMatchedVerdict(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	job := sampleJob()
	if _, err := s.UpsertFetched(job, now); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.SaveMatchResult(job.ID, "pass", "ok", 1); err != nil {
		t.Fatalf("SaveMatchResult: %v", err)
	}

	// A later fetch of the same, still-enriched job must not wipe the
	// verdict match already set.
	if _, err := s.UpsertFetched(job, now.Add(time.Hour)); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, err := s.GetByID(job.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.RuleVerdict != "pass" {
		t.Errorf("RuleVerdict = %q, want pass to survive a routine re-fetch", got.RuleVerdict)
	}
}
