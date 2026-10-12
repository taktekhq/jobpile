package watch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shQuote wraps s in single quotes for safe use inside a `sh -c`
// command string built in a test. Production code never needs this:
// notify_command is the user's own config value, already a full
// shell command.
func shQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func TestRunNotifyDeliversStdin(t *testing.T) {
	outFile := filepath.Join(t.TempDir(), "notify-out.txt")
	command := shQuote("testdata/fake_notify_ok.sh") + " " + shQuote(outFile)

	if err := RunNotify(context.Background(), command, "Reply from Acme: hello\n", 5*time.Second); err != nil {
		t.Fatalf("RunNotify: %v", err)
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("reading notify output: %v", err)
	}
	if string(got) != "Reply from Acme: hello\n" {
		t.Errorf("notify command received %q, want %q", string(got), "Reply from Acme: hello\n")
	}
}

func TestRunNotifyPropagatesFailure(t *testing.T) {
	err := RunNotify(context.Background(), "testdata/fake_notify_fail.sh", "text", 5*time.Second)
	if err == nil {
		t.Fatal("RunNotify: want error from a failing command, got nil")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want it to include the command's stderr (boom)", err)
	}
}

func TestRunNotifyRejectsEmptyCommand(t *testing.T) {
	if err := RunNotify(context.Background(), "   ", "text", time.Second); err == nil {
		t.Error("RunNotify with a blank command: want error, got nil")
	}
}

func TestRunNotifyRespectsTimeout(t *testing.T) {
	err := RunNotify(context.Background(), "sleep 2", "text", 50*time.Millisecond)
	if err == nil {
		t.Fatal("RunNotify: want a timeout error from a slow command, got nil")
	}
}
