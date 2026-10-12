package watch

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// RunNotify runs command through the shell, the same way jobpile runs
// key_command and password_command elsewhere, feeding text on its
// stdin. It never prints text or the command's own output; only an
// error wraps a short, truncated piece of stderr for diagnosis.
func RunNotify(ctx context.Context, command, text string, timeout time.Duration) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("watch: no notify_command in config")
	}
	runCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(runCtx, "sh", "-c", command)
	cmd.Stdin = strings.NewReader(text)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("watch: notify_command failed: %w (stderr: %s)", err, truncate(strings.TrimSpace(stderr.String()), 300))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
