package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/taktekhq/jobpile/internal/config"
	"github.com/taktekhq/jobpile/internal/watch"
)

func loadEmptySeen(t *testing.T) *watch.SeenStore {
	t.Helper()
	seen, err := watch.LoadSeen(filepath.Join(t.TempDir(), "watch-seen.json"))
	if err != nil {
		t.Fatalf("LoadSeen: %v", err)
	}
	return seen
}

func TestDecideCandidateNoMatch(t *testing.T) {
	cand := watch.Candidate{From: "news@example.com", Subject: "Weekly digest"}
	wc := config.WatchConfig{Companies: []string{"Percona"}}
	d := decideCandidate(cand, wc, loadEmptySeen(t))
	if d.Reason != "no_match" {
		t.Errorf("Reason = %q, want no_match", d.Reason)
	}
}

func TestDecideCandidateIgnored(t *testing.T) {
	cand := watch.Candidate{From: "jobs@percona.com", Subject: "Thank you for applying to Percona"}
	wc := config.WatchConfig{
		Companies:      []string{"Percona"},
		IgnoreSubjects: []string{"Thank you for applying"},
	}
	d := decideCandidate(cand, wc, loadEmptySeen(t))
	if d.Reason != "ignored" {
		t.Errorf("Reason = %q, want ignored", d.Reason)
	}
	if d.Company != "Percona" {
		t.Errorf("Company = %q, want Percona (an ignored message still reports which company it matched)", d.Company)
	}
}

func TestDecideCandidateSeen(t *testing.T) {
	cand := watch.Candidate{MessageID: "msg-1", From: "jobs@percona.com", Subject: "Moving forward"}
	wc := config.WatchConfig{Companies: []string{"Percona"}}
	seen := loadEmptySeen(t)
	seen.Add("msg-1")
	d := decideCandidate(cand, wc, seen)
	if d.Reason != "seen" {
		t.Errorf("Reason = %q, want seen", d.Reason)
	}
}

func TestDecideCandidateNotify(t *testing.T) {
	cand := watch.Candidate{MessageID: "msg-2", From: "jobs@percona.com", Subject: "Moving forward"}
	wc := config.WatchConfig{Companies: []string{"Percona"}}
	d := decideCandidate(cand, wc, loadEmptySeen(t))
	if d.Reason != "notify" {
		t.Errorf("Reason = %q, want notify", d.Reason)
	}
	if d.Company != "Percona" {
		t.Errorf("Company = %q, want Percona", d.Company)
	}
}

func TestDecideCandidateIgnoreBeforeSeen(t *testing.T) {
	// An ignored confirmation must never consume a seen-state write:
	// it should be treated as "ignored", not "seen", even though it
	// would also be new.
	cand := watch.Candidate{MessageID: "msg-3", From: "jobs@percona.com", Subject: "Thanks for applying"}
	wc := config.WatchConfig{
		Companies:      []string{"Percona"},
		IgnoreSubjects: []string{"Thanks for applying"},
	}
	d := decideCandidate(cand, wc, loadEmptySeen(t))
	if d.Reason != "ignored" {
		t.Errorf("Reason = %q, want ignored", d.Reason)
	}
}

func TestBuildNotificationTextUsesDisplayName(t *testing.T) {
	cand := watch.Candidate{
		From:     "jane@acme.com",
		FromName: "Jane at Acme",
		Subject:  "Re: your application",
		Date:     time.Date(2026, 10, 12, 14, 30, 0, 0, time.UTC),
		Preview:  "Thanks for applying, let's talk.",
	}
	text := buildNotificationText(cand, "Acme", "In team@example.com.")
	want := "Reply from Acme: \"Re: your application\" (Jane at Acme <jane@acme.com>, 2026-10-12 14:30 UTC)\n" +
		"Thanks for applying, let's talk.\n" +
		"In team@example.com."
	if text != want {
		t.Errorf("buildNotificationText =\n%q\nwant\n%q", text, want)
	}
}

func TestBuildNotificationTextNoDisplayName(t *testing.T) {
	cand := watch.Candidate{
		From:    "jane@acme.com",
		Subject: "Re: your application",
		Date:    time.Date(2026, 10, 12, 14, 30, 0, 0, time.UTC),
		Preview: "Short reply.",
	}
	text := buildNotificationText(cand, "Acme", "")
	want := "Reply from Acme: \"Re: your application\" (jane@acme.com, 2026-10-12 14:30 UTC)\n" +
		"Short reply."
	if text != want {
		t.Errorf("buildNotificationText =\n%q\nwant\n%q", text, want)
	}
}

func TestBuildNotificationTextTruncatesLongPreview(t *testing.T) {
	long := ""
	for i := 0; i < 50; i++ {
		long += "0123456789"
	}
	cand := watch.Candidate{From: "jane@acme.com", Subject: "Re: app", Preview: long}
	text := buildNotificationText(cand, "Acme", "")
	if len(text) > 500 {
		t.Errorf("notification text is %d bytes, want the preview capped near 300 chars", len(text))
	}
}

func TestWatchSummaryString(t *testing.T) {
	s := watchSummary{Fetched: 10, Matched: 4, Notified: 2, SkippedSeen: 1, SkippedIgnore: 1, NotifyFailed: 0}
	got := s.String()
	want := "fetched=10 matched=4 notified=2 skipped_seen=1 skipped_ignore=1 notify_failed=0"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
