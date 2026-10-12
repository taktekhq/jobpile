package judge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{`claude -p --model haiku --tools ""`, []string{"claude", "-p", "--model", "haiku", "--tools", ""}},
		{`env CLAUDE_LEDGER_JOB=jobpile claude-ledger -p --model haiku --tools ""`,
			[]string{"env", "CLAUDE_LEDGER_JOB=jobpile", "claude-ledger", "-p", "--model", "haiku", "--tools", ""}},
		{`fake-model`, []string{"fake-model"}},
		{`  one   two  `, []string{"one", "two"}},
		{`echo 'hello world'`, []string{"echo", "hello world"}},
	}
	for _, c := range cases {
		got, err := SplitCommand(c.cmd)
		if err != nil {
			t.Fatalf("SplitCommand(%q): %v", c.cmd, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("SplitCommand(%q) = %q, want %q", c.cmd, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("SplitCommand(%q)[%d] = %q, want %q", c.cmd, i, got[i], c.want[i])
			}
		}
	}
}

func TestSplitCommandRejectsUnterminatedQuote(t *testing.T) {
	if _, err := SplitCommand(`claude --tools "`); err == nil {
		t.Error("unterminated quote: want error, got nil")
	}
}

func TestSplitCommandRejectsEmpty(t *testing.T) {
	if _, err := SplitCommand("   "); err == nil {
		t.Error("empty command: want error, got nil")
	}
}

func TestParseVerdictGoodJSON(t *testing.T) {
	v, err := ParseVerdict(`{"verdict": "apply", "blockers": [], "reason": "good fit"}`)
	if err != nil {
		t.Fatalf("ParseVerdict: %v", err)
	}
	if v.Verdict != "apply" || v.Reason != "good fit" {
		t.Errorf("v = %+v", v)
	}
}

func TestParseVerdictStripsCodeFence(t *testing.T) {
	raw := "```json\n{\"verdict\": \"skip\", \"blockers\": [\"onsite\"], \"reason\": \"onsite role\"}\n```"
	v, err := ParseVerdict(raw)
	if err != nil {
		t.Fatalf("ParseVerdict: %v", err)
	}
	if v.Verdict != "skip" || len(v.Blockers) != 1 || v.Blockers[0] != "onsite" {
		t.Errorf("v = %+v", v)
	}
}

func TestParseVerdictRejectsBadJSON(t *testing.T) {
	if _, err := ParseVerdict("not json at all"); err == nil {
		t.Error("bad JSON: want error, got nil")
	}
}

func TestParseVerdictRejectsUnknownVerdict(t *testing.T) {
	if _, err := ParseVerdict(`{"verdict": "maybe-ish", "reason": "x"}`); err == nil {
		t.Error("unknown verdict: want error, got nil")
	}
}

func TestBuildPromptIncludesProfileAndJob(t *testing.T) {
	prompt := BuildPrompt("Profile: 10 years Go.", 1, "Staff Engineer", "Acme", "Great Go role.")
	if !strings.Contains(prompt, "Profile: 10 years Go.") {
		t.Error("prompt missing profile content")
	}
	if !strings.Contains(prompt, "Staff Engineer") || !strings.Contains(prompt, "Acme") {
		t.Error("prompt missing job title/company")
	}
	if !strings.Contains(prompt, "Great Go role.") {
		t.Error("prompt missing job text")
	}
	if !strings.Contains(prompt, `"verdict"`) {
		t.Error("prompt missing output format instructions")
	}
}

func TestJudgeOneWithFakeRunnerSucceeds(t *testing.T) {
	run := func(ctx context.Context, prompt string) (string, error) {
		return `{"verdict": "apply", "blockers": [], "reason": "fits"}`, nil
	}
	v, err := JudgeOne(context.Background(), run, "profile", 1, "Title", "Co", "text")
	if err != nil {
		t.Fatalf("JudgeOne: %v", err)
	}
	if v.Verdict != "apply" {
		t.Errorf("Verdict = %q, want apply", v.Verdict)
	}
}

func TestJudgeOneRetriesOnceOnBadJSON(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, prompt string) (string, error) {
		calls++
		if calls == 1 {
			return "garbage", nil
		}
		return `{"verdict": "maybe", "blockers": [], "reason": "second try"}`, nil
	}
	v, err := JudgeOne(context.Background(), run, "profile", 1, "Title", "Co", "text")
	if err != nil {
		t.Fatalf("JudgeOne: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if v.Verdict != "maybe" || v.Reason != "second try" {
		t.Errorf("v = %+v", v)
	}
}

func TestJudgeOneFallsBackAfterTwoBadJSONResponses(t *testing.T) {
	calls := 0
	run := func(ctx context.Context, prompt string) (string, error) {
		calls++
		return "still garbage", nil
	}
	v, err := JudgeOne(context.Background(), run, "profile", 1, "Title", "Co", "text")
	if err != nil {
		t.Fatalf("JudgeOne: %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if v.Verdict != "maybe" || v.Reason != "judge failed" {
		t.Errorf("v = %+v, want maybe/judge failed", v)
	}
}

func TestJudgeOneReturnsErrorWhenRunnerFailsTwice(t *testing.T) {
	run := func(ctx context.Context, prompt string) (string, error) {
		return "", errors.New("exec failed")
	}
	_, err := JudgeOne(context.Background(), run, "profile", 1, "Title", "Co", "text")
	if err == nil {
		t.Error("JudgeOne with a failing runner: want error, got nil")
	}
}

func TestNewShellRunnerAgainstFakeModelOK(t *testing.T) {
	run, err := NewShellRunner("testdata/fake_model_ok.sh", 5*time.Second)
	if err != nil {
		t.Fatalf("NewShellRunner: %v", err)
	}
	out, err := run(context.Background(), "ignored prompt")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	v, err := ParseVerdict(out)
	if err != nil {
		t.Fatalf("ParseVerdict(%q): %v", out, err)
	}
	if v.Verdict != "apply" {
		t.Errorf("Verdict = %q, want apply", v.Verdict)
	}
}

func TestNewShellRunnerAgainstFakeModelBadJSON(t *testing.T) {
	run, err := NewShellRunner("testdata/fake_model_bad.sh", 5*time.Second)
	if err != nil {
		t.Fatalf("NewShellRunner: %v", err)
	}
	v, err := JudgeOne(context.Background(), run, "profile", 1, "Title", "Co", "text")
	if err != nil {
		t.Fatalf("JudgeOne: %v", err)
	}
	if v.Verdict != "maybe" || v.Reason != "judge failed" {
		t.Errorf("v = %+v, want maybe/judge failed", v)
	}
}

func TestNewShellRunnerAgainstFailingModel(t *testing.T) {
	run, err := NewShellRunner("testdata/fake_model_fail.sh", 5*time.Second)
	if err != nil {
		t.Fatalf("NewShellRunner: %v", err)
	}
	_, err = JudgeOne(context.Background(), run, "profile", 1, "Title", "Co", "text")
	if err == nil {
		t.Error("JudgeOne against a failing model command: want error, got nil")
	}
}

func TestBuildPromptTier2IncludesUSContractorNote(t *testing.T) {
	prompt := BuildPrompt("profile", 2, "Staff Engineer", "Acme", "Must be a US W-2 employee.")
	if !strings.Contains(prompt, "tier 2") {
		t.Error("tier 2 prompt missing tier note")
	}
	if !strings.Contains(prompt, "W-2") || !strings.Contains(prompt, "contractor") {
		t.Error("tier 2 prompt missing the US-employment-is-not-a-blocker guidance")
	}
}

func TestBuildPromptDefaultsToApplyBias(t *testing.T) {
	prompt := BuildPrompt("profile", 1, "Staff Engineer", "Acme", "Short listing.")
	if !strings.Contains(prompt, "Default to \"apply\"") {
		t.Error("prompt missing the apply-by-default instruction")
	}
	if !strings.Contains(prompt, "not by itself a reason") && !strings.Contains(prompt, "NOT by itself a reason") {
		t.Error("prompt missing the thin-listing-is-not-a-blocker instruction")
	}
}

func TestBuildPromptTier1HasNoUSContractorNote(t *testing.T) {
	prompt := BuildPrompt("profile", 1, "Staff Engineer", "Acme", "text")
	if strings.Contains(prompt, "W-2") {
		t.Error("tier 1 prompt should not mention W-2/US-employment guidance")
	}
}

// TestNewShellRunnerSurfacesStdoutOnFailure reproduces the 12 Oct live
// run's failure mode: a model wrapper (claude-ledger in text mode)
// prints its own error ("Prompt is too long ...") to stdout, not
// stderr, and exits non-zero. The first version of NewShellRunner only
// captured stderr, so every one of these failures logged as a mystery
// "exit status 1 (stderr: )" with the real cause invisible. The error
// message must include stdout now.
func TestNewShellRunnerSurfacesStdoutOnFailure(t *testing.T) {
	run, err := NewShellRunner("testdata/fake_model_stdout_error.sh", 5*time.Second)
	if err != nil {
		t.Fatalf("NewShellRunner: %v", err)
	}
	_, runErr := run(context.Background(), "prompt")
	if runErr == nil {
		t.Fatal("run: want error, got nil")
	}
	if !strings.Contains(runErr.Error(), "Prompt is too long") {
		t.Errorf("error = %q, want it to include the model's stdout message", runErr.Error())
	}
}
