// Package watch looks for human replies, in an IMAP mailbox, to jobs
// already applied to, and hands each new one to a notify command.
// watch never applies, never sends mail, and never marks a message
// read: it only reads headers and a short body preview with
// BODY.PEEK, and only ever writes jobpile's own watch-seen.json.
package watch

import (
	"fmt"
	"strings"
)

// MatchCompany reports whether a message looks like it came from one
// of companies, checked case-insensitively as a substring against
// the From address, the From display name, and the Subject. It
// returns the first entry in companies that matched, so the config's
// own order controls which name is reported for a given message.
//
// An applicant-tracking system's own shared sending domain
// (Greenhouse, Lever, Ashby, and similar) never itself contains a
// company name, so a message from one of those domains only matches
// once the company's name shows up in the subject or display name.
// That is exactly what this same check already does; no separate
// case for ATS domains is needed.
func MatchCompany(from, fromName, subject string, companies []string) (string, bool) {
	fromL := strings.ToLower(from)
	nameL := strings.ToLower(fromName)
	subjL := strings.ToLower(subject)
	for _, c := range companies {
		cl := strings.ToLower(strings.TrimSpace(c))
		if cl == "" {
			continue
		}
		if strings.Contains(fromL, cl) || strings.Contains(nameL, cl) || strings.Contains(subjL, cl) {
			return c, true
		}
	}
	return "", false
}

// MatchIgnore reports whether subject contains one of ignorePatterns
// as a case-insensitive substring (an automated confirmation rather
// than a real reply), and which pattern matched.
func MatchIgnore(subject string, ignorePatterns []string) (string, bool) {
	subjL := strings.ToLower(subject)
	for _, p := range ignorePatterns {
		if p == "" {
			continue
		}
		if strings.Contains(subjL, strings.ToLower(p)) {
			return p, true
		}
	}
	return "", false
}

// CollapsePreview collapses every run of whitespace in s to a single
// space and truncates the result to at most maxRunes runes, so a
// reply's body preview reads as one tidy line regardless of how the
// original message wrapped it.
func CollapsePreview(s string, maxRunes int) string {
	collapsed := strings.Join(strings.Fields(s), " ")
	if maxRunes <= 0 {
		return collapsed
	}
	r := []rune(collapsed)
	if len(r) > maxRunes {
		r = r[:maxRunes]
	}
	return string(r)
}

// FormatNotification builds the text handed to notify_command on
// stdin for one new, matched, non-ignored reply. from and dateUTC are
// already formatted by the caller; bodyPreview should already be
// collapsed and truncated (see CollapsePreview). footer, from
// config's notify_footer, becomes the notification's own last line
// when non-empty; left blank, no footer line is added, so nothing
// about where this runs is hard-coded here.
func FormatNotification(company, subject, from, dateUTC, bodyPreview, footer string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Reply from %s: \"%s\" (%s, %s)\n", company, subject, from, dateUTC)
	b.WriteString(bodyPreview)
	if footer != "" {
		b.WriteString("\n")
		b.WriteString(footer)
	}
	return b.String()
}
