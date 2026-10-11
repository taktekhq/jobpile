package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/taktekhq/jobpile/internal/config"
	"github.com/taktekhq/jobpile/internal/store"
)

// resolveDataDir returns the data directory from --data or the
// JOBPILE_DATA environment variable. It never defaults to a path
// inside the repo: the spec is explicit that the data folder stays out
// of git.
func resolveDataDir(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if v := os.Getenv("JOBPILE_DATA"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no data folder given: pass --data or set JOBPILE_DATA")
}

// openEverything loads config.toml and jobs.db from dataDir and makes
// sure the text/ folder exists.
func openEverything(dataDir string) (*config.Config, *store.Store, error) {
	cfgPath := filepath.Join(dataDir, "config.toml")
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "text"), 0o755); err != nil {
		return nil, nil, fmt.Errorf("creating text/ folder: %w", err)
	}
	st, err := store.Open(filepath.Join(dataDir, "jobs.db"))
	if err != nil {
		return nil, nil, err
	}
	return cfg, st, nil
}

// runKeyCommand runs a shell command and returns its stdout, for
// resolving the remoet API key. It never logs or returns stderr, so a
// key is never written anywhere by accident.
func runKeyCommand(command string) (string, error) {
	cmd := exec.Command("sh", "-c", command)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("key_command failed: %w", err)
	}
	return string(out), nil
}

// readProfile reads profile.md from the data folder. A missing
// profile is reported distinctly so judge can skip cleanly instead of
// failing, since profile.md is written by someone else per the spec.
func readProfile(dataDir string) (string, bool, error) {
	path := filepath.Join(dataDir, "profile.md")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading %s: %w", path, err)
	}
	return string(b), true, nil
}

// sleepBetweenCalls pauses ms milliseconds, or not at all for ms <= 0.
// A tiny helper so every source goes through one place for its "be
// gentle" pacing.
func sleepBetweenCalls(ms int) {
	if ms <= 0 {
		return
	}
	time.Sleep(time.Duration(ms) * time.Millisecond)
}

func logf(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "jobpile: "+format+"\n", args...)
}

// appendRunsLog appends one line to runs.log, creating it if needed.
func appendRunsLog(dataDir, line string) error {
	f, err := os.OpenFile(filepath.Join(dataDir, "runs.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening runs.log: %w", err)
	}
	defer f.Close()
	ts := time.Now().UTC().Format(time.RFC3339)
	_, err = f.WriteString(ts + " " + strings.TrimRight(line, "\n") + "\n")
	return err
}
