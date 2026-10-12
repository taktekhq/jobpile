package watch

import "testing"

func TestMatchCompanyByDomain(t *testing.T) {
	company, ok := MatchCompany("recruiting@percona.com", "Percona Recruiting", "Your application", []string{"Percona", "percona.com"})
	if !ok {
		t.Fatal("MatchCompany: want a match, got none")
	}
	if company != "Percona" {
		t.Errorf("company = %q, want Percona (first matching entry wins)", company)
	}
}

func TestMatchCompanyByDisplayName(t *testing.T) {
	_, ok := MatchCompany("jobs@greenhouse-mail.io", "Camunda via Greenhouse", "Update on your application", []string{"Camunda", "camunda.com"})
	if !ok {
		t.Fatal("MatchCompany: want a match via display name, got none")
	}
}

func TestMatchCompanyATSDomainAloneDoesNotMatch(t *testing.T) {
	// A Greenhouse-relayed message with no company name anywhere must
	// not match: the shared ATS domain itself carries no company
	// identity, so this is the case the README calls out explicitly.
	_, ok := MatchCompany("no-reply@greenhouse-mail.io", "Greenhouse", "Thanks for your interest", []string{"Camunda", "camunda.com"})
	if ok {
		t.Error("MatchCompany: want no match for a bare ATS relay with no company name, got a match")
	}
}

func TestMatchCompanyBySubject(t *testing.T) {
	_, ok := MatchCompany("notifications@ashbyhq.com", "Ashby", "Your Kraken application has moved forward", []string{"Kraken", "kraken.com"})
	if !ok {
		t.Fatal("MatchCompany: want a match via subject, got none")
	}
}

func TestMatchCompanyCaseInsensitive(t *testing.T) {
	_, ok := MatchCompany("HR@SUPABASE.COM", "", "re: application", []string{"supabase.com"})
	if !ok {
		t.Error("MatchCompany: want a case-insensitive match, got none")
	}
}

func TestMatchCompanyNoMatch(t *testing.T) {
	_, ok := MatchCompany("newsletter@example.com", "Example Weekly", "This week in tech", []string{"Percona", "percona.com"})
	if ok {
		t.Error("MatchCompany: want no match, got one")
	}
}

func TestMatchCompanySkipsBlankEntries(t *testing.T) {
	_, ok := MatchCompany("jobs@percona.com", "", "hello", []string{"", "  "})
	if ok {
		t.Error("MatchCompany: blank company entries must never match")
	}
}

func TestMatchIgnoreMatches(t *testing.T) {
	pattern, ok := MatchIgnore("Thank you for applying to Acme", []string{"Thank you for applying", "Thanks for applying"})
	if !ok {
		t.Fatal("MatchIgnore: want a match, got none")
	}
	if pattern != "Thank you for applying" {
		t.Errorf("pattern = %q, want %q", pattern, "Thank you for applying")
	}
}

func TestMatchIgnoreCaseInsensitive(t *testing.T) {
	_, ok := MatchIgnore("SECURITY CODE FOR YOUR APPLICATION", []string{"Security code for your application"})
	if !ok {
		t.Error("MatchIgnore: want a case-insensitive match, got none")
	}
}

func TestMatchIgnoreNoMatch(t *testing.T) {
	_, ok := MatchIgnore("Following up on our chat", []string{"Thank you for applying"})
	if ok {
		t.Error("MatchIgnore: want no match, got one")
	}
}

func TestCollapsePreviewCollapsesWhitespace(t *testing.T) {
	got := CollapsePreview("Hi there,\n\n   Thanks for   reaching out.\t\n", 0)
	want := "Hi there, Thanks for reaching out."
	if got != want {
		t.Errorf("CollapsePreview = %q, want %q", got, want)
	}
}

func TestCollapsePreviewTruncates(t *testing.T) {
	got := CollapsePreview("abcdefghij", 5)
	if got != "abcde" {
		t.Errorf("CollapsePreview = %q, want abcde", got)
	}
}

func TestCollapsePreviewShorterThanLimit(t *testing.T) {
	got := CollapsePreview("short", 300)
	if got != "short" {
		t.Errorf("CollapsePreview = %q, want short", got)
	}
}

func TestFormatNotificationWithFooter(t *testing.T) {
	got := FormatNotification("Acme", "Re: your application", "jane@acme.com", "2026-10-12 14:30 UTC", "Thanks for applying, let's talk.", "In team@example.com.")
	want := "Reply from Acme: \"Re: your application\" (jane@acme.com, 2026-10-12 14:30 UTC)\n" +
		"Thanks for applying, let's talk.\n" +
		"In team@example.com."
	if got != want {
		t.Errorf("FormatNotification =\n%q\nwant\n%q", got, want)
	}
}

func TestFormatNotificationNoFooter(t *testing.T) {
	got := FormatNotification("Acme", "Re: your application", "jane@acme.com", "2026-10-12 14:30 UTC", "Body text.", "")
	want := "Reply from Acme: \"Re: your application\" (jane@acme.com, 2026-10-12 14:30 UTC)\n" +
		"Body text."
	if got != want {
		t.Errorf("FormatNotification =\n%q\nwant\n%q", got, want)
	}
}
