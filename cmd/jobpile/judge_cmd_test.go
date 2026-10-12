package main

import (
	"strings"
	"testing"

	"github.com/taktekhq/jobpile/internal/model"
)

func TestRemoetFallbackTextCombinesWhatRemoetGaveDirectly(t *testing.T) {
	j := model.Job{
		Title: "Staff Backend Engineer", Company: "Acme",
		Seniority:      "senior",
		Stack:          []string{"Go", "Postgres"},
		RemotePolicy:   "remote-restricted",
		AllowedRegions: []string{"EMEA, LATAM, or North America"},
		SalaryRaw:      "160000-190000 USD/year",
		ListingText:    "Join our platform team building async infra.",
	}
	text := remoetFallbackText(j)
	for _, want := range []string{"Staff Backend Engineer", "Acme", "senior", "Go, Postgres", "remote-restricted", "EMEA, LATAM", "160000-190000", "async infra"} {
		if !strings.Contains(text, want) {
			t.Errorf("remoetFallbackText missing %q in: %q", want, text)
		}
	}
}

func TestRemoetFallbackTextNeverEmpty(t *testing.T) {
	text := remoetFallbackText(model.Job{})
	if strings.TrimSpace(text) == "" {
		t.Error("remoetFallbackText(empty job) should still return something, not blank text")
	}
}

func TestJobTextUsesHimalayasDescriptionDirectly(t *testing.T) {
	j := model.Job{Source: "himalayas", ListingText: "Full job description already here."}
	text, source := jobText(j)
	if text != j.ListingText {
		t.Errorf("text = %q, want the stored ListingText unchanged", text)
	}
	if source != "himalayas_description" {
		t.Errorf("source = %q, want himalayas_description", source)
	}
}

func TestJobTextFallsBackWhenRemoetHasNoApplyURL(t *testing.T) {
	j := model.Job{Source: "remoet", ApplyURL: "", ListingText: "A short summary."}
	text, source := jobText(j)
	if source != "remoet_summary_fallback" {
		t.Errorf("source = %q, want remoet_summary_fallback", source)
	}
	if !strings.Contains(text, "A short summary.") {
		t.Errorf("text = %q, want it to include the summary", text)
	}
}
