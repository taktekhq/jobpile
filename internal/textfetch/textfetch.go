// Package textfetch fetches a job listing's apply_url once and turns
// its HTML into plain text for the judge. remoet only gives a summary,
// not the full listing, so jobpile reads the real page for jobs that
// passed the rules.
package textfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// userAgent is a plain, current desktop browser string. Some apply
// pages block requests with no user agent or an obviously
// non-browser one.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// MaxChars caps how much text is handed to the judge. A full careers
// page can run long; the judge only needs the listing itself.
const MaxChars = 20000

// HTTPStatusError marks a 403 or 429, the signal to stop fetching from
// this source for the rest of the run.
type HTTPStatusError struct {
	StatusCode int
	URL        string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("textfetch: %s returned HTTP %d", e.URL, e.StatusCode)
}

// Fetch does one GET of url with a browser user agent and returns the
// page as plain text, HTML tags stripped and capped at MaxChars.
func Fetch(ctx context.Context, client *http.Client, url string) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("textfetch: building request for %s: %w", url, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("textfetch: fetching %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return "", &HTTPStatusError{StatusCode: resp.StatusCode, URL: url}
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("textfetch: %s returned HTTP %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return "", fmt.Errorf("textfetch: reading %s: %w", url, err)
	}

	text := StripHTML(string(body))
	if len(text) > MaxChars {
		text = text[:MaxChars]
	}
	return text, nil
}

// StripHTML removes tags, script and style content, and collapses
// whitespace, leaving plain readable text.
func StripHTML(html string) string {
	var b strings.Builder
	inTag := false
	inScript := false
	inStyle := false
	tagBuf := strings.Builder{}

	flushTag := func() string {
		s := strings.ToLower(tagBuf.String())
		tagBuf.Reset()
		return s
	}

	for _, r := range html {
		if inTag {
			if r == '>' {
				tag := flushTag()
				inTag = false
				switch {
				case strings.HasPrefix(tag, "script"):
					inScript = true
				case strings.HasPrefix(tag, "/script"):
					inScript = false
				case strings.HasPrefix(tag, "style"):
					inStyle = true
				case strings.HasPrefix(tag, "/style"):
					inStyle = false
				}
				continue
			}
			tagBuf.WriteRune(r)
			continue
		}
		if r == '<' {
			inTag = true
			continue
		}
		if inScript || inStyle {
			continue
		}
		b.WriteRune(r)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
