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
			return "", fmt.Errorf("judge: model command failed: %w (stderr: %s)", err, truncate(stderr.String(), 500))
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

// BuildPrompt builds the judge's prompt: first what the listing
// requires, then how that compares with the profile, then the answer
// format. profileMD is the candidate's profile.md content; jobText is
// the listing text (the fetched apply_url page, or remoet's summary as
// a fallback).
func BuildPrompt(profileMD, jobTitle, jobCompany, jobText string) string {
	var b strings.Builder
	b.WriteString("You are screening one remote job listing for a candidate. Work in three steps.\n\n")
	b.WriteString("Step 1: list what the listing requires or implies, if anything, for each of: work authorisation, country of residence, security clearance, onsite days, travel, tech stack, years of experience, and pay type (salary vs equity-only vs unpaid).\n\n")
	b.WriteString("Step 2: compare that list with the candidate's profile below. Note anything the listing requires that the profile does not meet, or that the listing leaves unclear.\n\n")
	b.WriteString("Step 3: answer with exactly one JSON object, nothing else, no markdown fences, in this shape:\n")
	b.WriteString(`{"verdict": "apply" | "maybe" | "skip", "blockers": ["..."], "reason": "one line"}` + "\n\n")
	b.WriteString("Use \"apply\" when nothing in the listing blocks the candidate. Use \"skip\" when something clearly does, and name it in blockers. Use \"maybe\" when the listing is unclear on a blocker, for example it never says where it hires or what it pays.\n\n")
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
func JudgeOne(ctx context.Context, run Runner, profileMD, jobTitle, jobCompany, jobText string) (Verdict, error) {
	prompt := BuildPrompt(profileMD, jobTitle, jobCompany, jobText)

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
