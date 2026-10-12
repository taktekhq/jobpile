package textfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDetectBoardGreenhouse(t *testing.T) {
	cases := []string{
		"https://boards.greenhouse.io/anduril/jobs/6205794004",
		"https://job-boards.greenhouse.io/natera/jobs/6205794004",
		"https://job-boards.eu.greenhouse.io/workable/jobs/6205794004",
	}
	for _, u := range cases {
		board, slug, jobID, ok := DetectBoard(u)
		if !ok || board != BoardGreenhouse {
			t.Fatalf("DetectBoard(%q) = %v %v %v %v, want greenhouse", u, board, slug, jobID, ok)
		}
		if jobID != "6205794004" {
			t.Errorf("DetectBoard(%q) jobID = %q, want 6205794004", u, jobID)
		}
	}
}

func TestDetectBoardLever(t *testing.T) {
	board, slug, jobID, ok := DetectBoard("https://jobs.lever.co/envato-2/6a7e1ff7-5970-4e7e-bd6e-a9f34bfe7b0a")
	if !ok || board != BoardLever {
		t.Fatalf("DetectBoard = %v %v %v %v, want lever", board, slug, jobID, ok)
	}
	if slug != "envato-2" || jobID != "6a7e1ff7-5970-4e7e-bd6e-a9f34bfe7b0a" {
		t.Errorf("slug/jobID = %q/%q", slug, jobID)
	}
}

func TestDetectBoardAshby(t *testing.T) {
	board, slug, jobID, ok := DetectBoard("https://jobs.ashbyhq.com/arena/2ae7eb71-a8d7-4923-8d06-8830ef12d429")
	if !ok || board != BoardAshby {
		t.Fatalf("DetectBoard = %v %v %v %v, want ashby", board, slug, jobID, ok)
	}
	if slug != "arena" {
		t.Errorf("slug = %q, want arena", slug)
	}
}

func TestDetectBoardRejectsOtherHosts(t *testing.T) {
	_, _, _, ok := DetectBoard("https://sofi.com/careers/job/7963969003")
	if ok {
		t.Error("a non-ATS URL should not be detected as a board")
	}
}

func readBoardFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("reading fixture %s: %v", name, err)
	}
	return b
}

func TestFetchGreenhouseUnescapesAndStripsHTML(t *testing.T) {
	fixture := readBoardFixture(t, "greenhouse_posting.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "natera") {
			t.Errorf("path = %q, missing company slug", r.URL.Path)
		}
		w.Write(fixture)
	}))
	defer srv.Close()

	text, err := fetchGreenhouseAt(context.Background(), srv.Client(), srv.URL, "natera", "6205794004")
	if err != nil {
		t.Fatalf("fetchGreenhouse: %v", err)
	}
	if strings.Contains(text, "&lt;") || strings.Contains(text, "<p>") {
		t.Errorf("text still has HTML entities or tags: %q", text)
	}
	if !strings.Contains(text, "Senior Data Analyst") {
		t.Errorf("text missing title: %q", text)
	}
	if !strings.Contains(text, "LOCATION") {
		t.Errorf("text missing body content: %q", text)
	}
}

func TestFetchLeverPrefersPlainText(t *testing.T) {
	fixture := readBoardFixture(t, "lever_posting.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(fixture) }))
	defer srv.Close()

	text, err := fetchLeverAt(context.Background(), srv.Client(), srv.URL, "envato-2", "6a7e1ff7")
	if err != nil {
		t.Fatalf("fetchLever: %v", err)
	}
	if !strings.Contains(text, "Senior Product Manager - GenAI") {
		t.Errorf("text missing job title: %q", text)
	}
	if !strings.Contains(text, "What you") {
		t.Errorf("text missing list heading: %q", text)
	}
}

func TestFetchAshbyFindsJobByID(t *testing.T) {
	fixture := readBoardFixture(t, "ashby_board.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(fixture) }))
	defer srv.Close()

	text, err := fetchAshbyAt(context.Background(), srv.Client(), srv.URL, "arena", "2ae7eb71-a8d7-4923-8d06-8830ef12d429")
	if err != nil {
		t.Fatalf("fetchAshby: %v", err)
	}
	if !strings.Contains(text, "Member of Security Staff") {
		t.Errorf("text missing job title: %q", text)
	}
	if !strings.Contains(text, "ARENA INTELLIGENCE") {
		t.Errorf("text missing description: %q", text)
	}
}

func TestFetchAshbyMissingJobErrors(t *testing.T) {
	fixture := readBoardFixture(t, "ashby_board.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(fixture) }))
	defer srv.Close()

	_, err := fetchAshbyAt(context.Background(), srv.Client(), srv.URL, "arena", "not-a-real-id")
	if err == nil {
		t.Error("fetchAshby with an unknown job id: want error, got nil")
	}
}
