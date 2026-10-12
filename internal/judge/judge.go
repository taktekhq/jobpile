// Package judge runs the cheap model over a job that already passed
// the rules and turns its answer into apply, maybe, or skip.
package judge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Verdict is the judge's answer for one job.
type Verdict struct {
	Verdict  string   `json:"verdict"` // "apply", "maybe", or "skip"
	Blockers []string `json:"blockers"`
	Reason   string   `json:"reason"`
}

// Runner calls the model once with a prompt and returns its raw text
// output.
type Runner func(ctx context.Context, prompt string) (string, error)

// SplitCommand splits a config command string into argv, the way a
// shell would for the simple cases jobpile needs: whitespace-separated
// words, with single or double quotes grouping a word (including an
// empty `""`, which the judge command relies on for `--tools ""`).
// It does not support shell operators like pipes or redirects; the
// command is just the program and its flags.
func SplitCommand(cmd string) ([]string, error) {
	var args []string
	var cur strings.Builder
	hasCur := false
	inSingle := false
	inDouble := false

	flush := func() {
		if hasCur {
			args = append(args, cur.String())
			cur.Reset()
			hasCur = false
		}
	}

	runes := []rune(cmd)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch {
		case inSingle:
			if r == '\'' {
				inSingle = false
			} else {
				cur.WriteRune(r)
			}
		case inDouble:
			if r == '"' {
				inDouble = false
			} else {
				cur.WriteRune(r)
			}
		case r == '\'':
			inSingle = true
			hasCur = true
		case r == '"':
			inDouble = true
			hasCur = true
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
			hasCur = true
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("judge: unterminated quote in command %q", cmd)
	}
	flush()
	if len(args) == 0 {
		return nil, fmt.Errorf("judge: empty command")
	}
	return args, nil
}

// NewShellRunner builds a Runner that execs command, feeding prompt on
// stdin and reading the model's answer from stdout.
func NewShellRunner(command string, timeout time.Duration) (Runner, error) {
	argv, err := SplitCommand(command)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, prompt string) (string, error) {
		runCtx := ctx
		if timeout > 0 {
			var cancel context.CancelFunc
			runCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
		cmd.Stdin = strings.NewReader(prompt)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			// Both streams matter here: claude-ledger's text mode
			// writes the model's own error message (e.g. "Prompt is
			// too long") to stdout, not stderr, so an error that only
			// showed stderr hid the real cause during a live run on
			// 12 Oct. Root cause there was running several of these
			// at once (see kbase: claude-p-concurrency-prompt-bloat);
			// surfacing stdout here is so the next time it is not a
			// mystery.
			return "", fmt.Errorf("judge: model command failed: %w (stdout: %s) (stderr: %s)",
				err, truncate(strings.TrimSpace(stdout.String()), 300), truncate(strings.TrimSpace(stderr.String()), 300))
		}
		return strings.TrimSpace(stdout.String()), nil
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// tierNote describes tier 1 vs tier 2 for the prompt, and for tier 2
// states the standing instruction not to treat US-employment phrasing
// as a blocker: the candidate applies to US-only roles anyway, pitching
// himself as a contractor.
func tierNote(tier int) string {
	switch tier {
	case 2:
		return "This listing is tier 2: jobpile's rules found it US-only. The candidate applies to tier 2 listings anyway, pitching himself as a contractor through an employer-of-record or his own company. Do NOT treat \"must be authorised to work in the US\", \"W-2 only\", \"US employee\", or similar US-employment phrasing as a blocker on its own. \"Full-time\" or \"full-time employee\" is also fine; that is how an EOR or contractor arrangement usually reads. Only skip for one of the profile's real hard blockers below."
	case 1:
		return "This listing is tier 1: jobpile's rules found it worldwide, or covering the candidate's GMT+3 time zone."
	default:
		return ""
	}
}

// BuildPrompt builds the judge's prompt: first what the listing
// requires, then how that compares with the profile, then the answer
// format. profileMD is the candidate's profile.md content, which is
// the authority on what counts as a real blocker (its own "hard
// blockers" / "not blockers" sections, if present, win over this
// prompt's general guidance). jobText is the listing text, and its
// source (apply_url page, an ATS's own API, or remoet's summary) does
// not change how it is judged. tier is the job's rule-assigned
// location tier (1, 2, or 0 when not applicable, e.g. Himalayas).
func BuildPrompt(profileMD string, tier int, jobTitle, jobCompany, jobText string) string {
	var b strings.Builder
	b.WriteString("You are screening one remote job listing for a candidate. It already passed jobpile's title and location rules, so only screen for blockers, do not re-decide fit.\n\n")
	b.WriteString("Default to \"apply\". A short, thin, or incomplete listing, or one with no salary stated, is NOT by itself a reason for \"maybe\" or \"skip\": treat missing information as neutral, not as a blocker. Only move off \"apply\" when the text itself states something that the candidate profile below calls a real blocker.\n\n")
	if note := tierNote(tier); note != "" {
		b.WriteString(note)
		b.WriteString("\n\n")
	}
	b.WriteString("Step 1: list anything the listing explicitly states for: security clearance, a specific country or state residency requirement, onsite days or relocation, primary tech stack, years of experience, and pay (only if a figure is actually given).\n\n")
	b.WriteString("Step 2: compare that list against the candidate profile below. The profile's own \"hard blockers\" and \"not blockers\" sections are the authority on what counts; this prompt's guidance is a default when the profile does not say.\n\n")
	b.WriteString("Step 3: answer with exactly one JSON object, nothing else, no markdown fences, in this shape:\n")
	b.WriteString(`{"verdict": "apply" | "maybe" | "skip", "blockers": ["..."], "reason": "one line"}` + "\n\n")
	b.WriteString("Use \"skip\" only when the listing clearly states one of the profile's real hard blockers, and name it in blockers. Use \"maybe\" only when the text hints at a possible blocker but is genuinely unclear about it (not simply short), for example \"remote, but must be near our London office\" with no further detail. Otherwise use \"apply\".\n\n")
	b.WriteString("--- CANDIDATE PROFILE ---\n")
	b.WriteString(profileMD)
	b.WriteString("\n\n--- JOB LISTING ---\n")
	fmt.Fprintf(&b, "Title: %s\nCompany: %s\n\n", jobTitle, jobCompany)
	b.WriteString(jobText)
	b.WriteString("\n")
	return b.String()
}

// ParseVerdict parses the model's raw output as a Verdict. It tolerates
// a response wrapped in a markdown code fence, since some models add
// one despite being asked not to.
func ParseVerdict(raw string) (Verdict, error) {
	text := strings.TrimSpace(raw)
	text = stripCodeFence(text)

	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start == -1 || end == -1 || end < start {
		return Verdict{}, fmt.Errorf("judge: no JSON object found in model output: %s", truncate(raw, 200))
	}
	text = text[start : end+1]

	var v Verdict
	if err := json.Unmarshal([]byte(text), &v); err != nil {
		return Verdict{}, fmt.Errorf("judge: invalid JSON from model: %w", err)
	}
	switch v.Verdict {
	case "apply", "maybe", "skip":
	default:
		return Verdict{}, fmt.Errorf("judge: unknown verdict %q", v.Verdict)
	}
	return v, nil
}

func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.Index(s, "\n"); i != -1 && !strings.Contains(s[:i], "{") {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	return strings.TrimSpace(s)
}

// JudgeOne runs the model on one job, retrying once if its output does
// not parse as valid JSON. If both attempts fail to parse, it returns
// the spec's fallback verdict (maybe, "judge failed") rather than an
// error: that is a normal, storable outcome. An error is only returned
// when the model command itself fails to run (both attempts), since
// that job should stay unjudged and be tried again next run.
func JudgeOne(ctx context.Context, run Runner, profileMD string, tier int, jobTitle, jobCompany, jobText string) (Verdict, error) {
	prompt := BuildPrompt(profileMD, tier, jobTitle, jobCompany, jobText)

	var lastRunErr error
	for attempt := 0; attempt < 2; attempt++ {
		out, err := run(ctx, prompt)
		if err != nil {
			lastRunErr = err
			continue
		}
		v, parseErr := ParseVerdict(out)
		if parseErr == nil {
			return v, nil
		}
		lastRunErr = nil
		if attempt == 1 {
			return Verdict{Verdict: "maybe", Reason: "judge failed"}, nil
		}
	}
	if lastRunErr != nil {
		return Verdict{}, fmt.Errorf("judge: model command failed twice: %w", lastRunErr)
	}
	return Verdict{Verdict: "maybe", Reason: "judge failed"}, nil
}
