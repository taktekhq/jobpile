package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing temp config: %v", err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := writeTempConfig(t, "")
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Floor != 80000 {
		t.Errorf("Floor default = %v, want 80000", c.Floor)
	}
	if c.CurrencyRates["USD"] != 1 {
		t.Errorf("USD rate default = %v, want 1", c.CurrencyRates["USD"])
	}
	if c.Remoet.BudgetStopBefore != 4000 {
		t.Errorf("BudgetStopBefore default = %v, want 4000", c.Remoet.BudgetStopBefore)
	}
	if c.Judge.Command == "" {
		t.Error("Judge.Command default must not be empty")
	}
	if c.Judge.MaxPerRun != 150 {
		t.Errorf("Judge.MaxPerRun default = %v, want 150", c.Judge.MaxPerRun)
	}
	if c.Judge.Concurrency != 4 {
		t.Errorf("Judge.Concurrency default = %v, want 4", c.Judge.Concurrency)
	}
	if len(c.Himalayas.Queries) == 0 {
		t.Error("Himalayas.Queries default must not be empty")
	}
	if len(c.Himalayas.SeniorityFilter) == 0 {
		t.Error("Himalayas.SeniorityFilter default must not be empty")
	}
	if c.Himalayas.MaxPages != 5 {
		t.Errorf("Himalayas.MaxPages default = %v, want 5", c.Himalayas.MaxPages)
	}
	if c.Watch.Mailbox != "INBOX" {
		t.Errorf("Watch.Mailbox default = %q, want INBOX", c.Watch.Mailbox)
	}
	if c.Watch.LookbackDays != 3 {
		t.Errorf("Watch.LookbackDays default = %v, want 3", c.Watch.LookbackDays)
	}
	if len(c.Watch.IgnoreSubjects) == 0 {
		t.Error("Watch.IgnoreSubjects default must not be empty")
	}
}

func TestLoadWatchSection(t *testing.T) {
	path := writeTempConfig(t, `
[watch]
imap_host = "imap.example.com"
imap_user = "me@example.com"
password_command = "echo fake-password"
mailbox = "Replies"
companies = ["Acme", "acme.com"]
ignore_subjects = ["custom ignore"]
notify_command = "cat"
notify_footer = "footer line"
lookback_days = 7
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Watch.IMAPHost != "imap.example.com" {
		t.Errorf("Watch.IMAPHost = %q, want imap.example.com", c.Watch.IMAPHost)
	}
	if c.Watch.Mailbox != "Replies" {
		t.Errorf("Watch.Mailbox = %q, want Replies", c.Watch.Mailbox)
	}
	if len(c.Watch.Companies) != 2 {
		t.Errorf("Watch.Companies = %v, want 2 entries", c.Watch.Companies)
	}
	if len(c.Watch.IgnoreSubjects) != 1 || c.Watch.IgnoreSubjects[0] != "custom ignore" {
		t.Errorf("Watch.IgnoreSubjects = %v, want [custom ignore] (a configured list must replace the default, not merge with it)", c.Watch.IgnoreSubjects)
	}
	if c.Watch.LookbackDays != 7 {
		t.Errorf("Watch.LookbackDays = %v, want 7", c.Watch.LookbackDays)
	}
	if c.Watch.NotifyFooter != "footer line" {
		t.Errorf("Watch.NotifyFooter = %q, want %q", c.Watch.NotifyFooter, "footer line")
	}
}

func TestLoadRejectsNegativeLookbackDays(t *testing.T) {
	path := writeTempConfig(t, "[watch]\nlookback_days = -1\n")
	if _, err := Load(path); err == nil {
		t.Error("Load with negative watch.lookback_days: want error, got nil")
	}
}

func TestWatchPasswordRunsCommand(t *testing.T) {
	w := WatchConfig{PasswordCommand: "whatever"}
	password, err := w.WatchPassword(func(cmd string) (string, error) {
		if cmd != "whatever" {
			t.Errorf("command = %q, want whatever", cmd)
		}
		return "  secret-pw  \n", nil
	})
	if err != nil {
		t.Fatalf("WatchPassword: %v", err)
	}
	if password != "secret-pw" {
		t.Errorf("password = %q, want secret-pw", password)
	}
}

func TestWatchPasswordErrorsWithNoCommand(t *testing.T) {
	w := WatchConfig{}
	if _, err := w.WatchPassword(func(string) (string, error) { return "x", nil }); err == nil {
		t.Error("WatchPassword with no password_command: want error, got nil")
	}
}

func TestWatchPasswordPropagatesCommandError(t *testing.T) {
	w := WatchConfig{PasswordCommand: "whatever"}
	wantErr := errors.New("boom")
	if _, err := w.WatchPassword(func(string) (string, error) { return "", wantErr }); err == nil {
		t.Error("WatchPassword with failing command: want error, got nil")
	}
}

func TestWatchPasswordRejectsEmptyOutput(t *testing.T) {
	w := WatchConfig{PasswordCommand: "whatever"}
	if _, err := w.WatchPassword(func(string) (string, error) { return "   \n", nil }); err == nil {
		t.Error("WatchPassword with blank command output: want error, got nil")
	}
}

func TestLoadReadsValues(t *testing.T) {
	path := writeTempConfig(t, `
floor_usd = 90000

[currency_rates]
USD = 1
EUR = 0.92

[title]
senior_patterns = ["staff", "principal"]

[judge]
command = "fake-model"
max_per_run = 10
`)
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Floor != 90000 {
		t.Errorf("Floor = %v, want 90000", c.Floor)
	}
	if c.CurrencyRates["EUR"] != 0.92 {
		t.Errorf("EUR rate = %v, want 0.92", c.CurrencyRates["EUR"])
	}
	if len(c.Title.SeniorPatterns) != 2 {
		t.Errorf("SeniorPatterns = %v, want 2 entries", c.Title.SeniorPatterns)
	}
	if c.Judge.Command != "fake-model" {
		t.Errorf("Judge.Command = %q, want fake-model", c.Judge.Command)
	}
	if c.Judge.MaxPerRun != 10 {
		t.Errorf("Judge.MaxPerRun = %v, want 10", c.Judge.MaxPerRun)
	}
}

func TestLoadRejectsNegativeFloor(t *testing.T) {
	path := writeTempConfig(t, "floor_usd = -5\n")
	if _, err := Load(path); err == nil {
		t.Error("Load with negative floor_usd: want error, got nil")
	}
}

func TestRemoetAPIKeyPrefersEnv(t *testing.T) {
	t.Setenv("REMOET_API_KEY", "  env-key  ")
	r := RemoetConfig{KeyCommand: "echo should-not-run"}
	key, err := r.RemoetAPIKey(func(string) (string, error) {
		t.Fatal("key_command ran despite REMOET_API_KEY being set")
		return "", nil
	})
	if err != nil {
		t.Fatalf("RemoetAPIKey: %v", err)
	}
	if key != "env-key" {
		t.Errorf("key = %q, want env-key", key)
	}
}

func TestRemoetAPIKeyFallsBackToCommand(t *testing.T) {
	t.Setenv("REMOET_API_KEY", "")
	r := RemoetConfig{KeyCommand: "whatever"}
	key, err := r.RemoetAPIKey(func(cmd string) (string, error) {
		if cmd != "whatever" {
			t.Errorf("command = %q, want whatever", cmd)
		}
		return "cmd-key\n", nil
	})
	if err != nil {
		t.Fatalf("RemoetAPIKey: %v", err)
	}
	if key != "cmd-key" {
		t.Errorf("key = %q, want cmd-key", key)
	}
}

func TestRemoetAPIKeyErrorsWithNoSource(t *testing.T) {
	t.Setenv("REMOET_API_KEY", "")
	r := RemoetConfig{}
	if _, err := r.RemoetAPIKey(func(string) (string, error) { return "x", nil }); err == nil {
		t.Error("RemoetAPIKey with no env and no key_command: want error, got nil")
	}
}

func TestRemoetAPIKeyPropagatesCommandError(t *testing.T) {
	t.Setenv("REMOET_API_KEY", "")
	r := RemoetConfig{KeyCommand: "whatever"}
	wantErr := errors.New("boom")
	if _, err := r.RemoetAPIKey(func(string) (string, error) { return "", wantErr }); err == nil {
		t.Error("RemoetAPIKey with failing command: want error, got nil")
	}
}
