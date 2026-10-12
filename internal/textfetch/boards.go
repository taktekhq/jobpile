package textfetch

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Board names the ATS whose public JSON API can answer instead of
// scraping the apply page's HTML. A JS-rendered careers page (common
// on Greenhouse, Lever and Ashby) often gives textfetch.Fetch almost
// nothing to work with; these three ATSes publish the same listing as
// plain JSON with no auth needed.
type Board string

const (
	BoardGreenhouse Board = "greenhouse"
	BoardLever      Board = "lever"
	BoardAshby      Board = "ashby"
)

// DetectBoard recognises a Greenhouse, Lever or Ashby apply/job URL and
// pulls out the company slug and job id FetchBoard needs. ok is false
// for any other URL.
func DetectBoard(rawURL string) (board Board, slug string, jobID string, ok bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", "", false
	}
	host := strings.ToLower(u.Host)
	segments := splitPath(u.Path)

	switch {
	case strings.HasSuffix(host, "greenhouse.io") && len(segments) >= 3 && segments[1] == "jobs":
		// boards.greenhouse.io/<slug>/jobs/<id>
		// job-boards.greenhouse.io/<slug>/jobs/<id>
		// job-boards.eu.greenhouse.io/<slug>/jobs/<id>
		return BoardGreenhouse, segments[0], segments[2], true

	case strings.HasSuffix(host, "lever.co") && len(segments) >= 2:
		// jobs.lever.co/<slug>/<id>
		return BoardLever, segments[0], segments[1], true

	case strings.HasSuffix(host, "ashbyhq.com") && len(segments) >= 2:
		// jobs.ashbyhq.com/<slug>/<id>
		return BoardAshby, segments[0], segments[1], true
	}
	return "", "", "", false
}

func splitPath(p string) []string {
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		return nil
	}
	return parts
}

// FetchBoard fetches the listing text for a URL DetectBoard recognised.
func FetchBoard(ctx context.Context, client *http.Client, board Board, slug, jobID string) (string, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	switch board {
	case BoardGreenhouse:
		return fetchGreenhouse(ctx, client, slug, jobID)
	case BoardLever:
		return fetchLever(ctx, client, slug, jobID)
	case BoardAshby:
		return fetchAshby(ctx, client, slug, jobID)
	default:
		return "", fmt.Errorf("textfetch: unknown board %q", board)
	}
}

func getJSON(ctx context.Context, client *http.Client, url string, out interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("textfetch: building request for %s: %w", url, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("textfetch: calling %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return &HTTPStatusError{StatusCode: resp.StatusCode, URL: url}
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("textfetch: %s returned HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return fmt.Errorf("textfetch: reading %s: %w", url, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("textfetch: parsing %s: %w", url, err)
	}
	return nil
}

// greenhousePosting is the shape of boards-api.greenhouse.io's single
// job endpoint, trimmed to what jobpile reads.
type greenhousePosting struct {
	Title    string `json:"title"`
	Location struct {
		Name string `json:"name"`
	} `json:"location"`
	Content string `json:"content"`
}

func fetchGreenhouse(ctx context.Context, client *http.Client, slug, jobID string) (string, error) {
	return fetchGreenhouseAt(ctx, client, "https://boards-api.greenhouse.io", slug, jobID)
}

func fetchGreenhouseAt(ctx context.Context, client *http.Client, baseURL, slug, jobID string) (string, error) {
	apiURL := fmt.Sprintf("%s/v1/boards/%s/jobs/%s?content=true", baseURL, slug, jobID)
	var p greenhousePosting
	if err := getJSON(ctx, client, apiURL, &p); err != nil {
		return "", err
	}
	// Greenhouse's content field is HTML with entities escaped
	// (&lt;p&gt; rather than <p>), so unescape before stripping tags.
	text := StripHTML(html.UnescapeString(p.Content))
	var b strings.Builder
	fmt.Fprintf(&b, "%s", p.Title)
	if p.Location.Name != "" {
		fmt.Fprintf(&b, " (%s)", p.Location.Name)
	}
	b.WriteString("\n\n")
	b.WriteString(text)
	return b.String(), nil
}

// leverPosting is the shape of api.lever.co's posting endpoint,
// trimmed to what jobpile reads.
type leverPosting struct {
	Text             string `json:"text"`
	Opening          string `json:"opening"`
	OpeningPlain     string `json:"openingPlain"`
	Description      string `json:"description"`
	DescriptionPlain string `json:"descriptionPlain"`
	Lists            []struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	} `json:"lists"`
}

func fetchLever(ctx context.Context, client *http.Client, slug, jobID string) (string, error) {
	return fetchLeverAt(ctx, client, "https://api.lever.co", slug, jobID)
}

func fetchLeverAt(ctx context.Context, client *http.Client, baseURL, slug, jobID string) (string, error) {
	apiURL := fmt.Sprintf("%s/v0/postings/%s/%s", baseURL, slug, jobID)
	var p leverPosting
	if err := getJSON(ctx, client, apiURL, &p); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(p.Text)
	b.WriteString("\n\n")
	if p.OpeningPlain != "" {
		b.WriteString(p.OpeningPlain)
	} else if p.Opening != "" {
		b.WriteString(StripHTML(p.Opening))
	}
	b.WriteString("\n\n")
	if p.DescriptionPlain != "" {
		b.WriteString(p.DescriptionPlain)
	} else if p.Description != "" {
		b.WriteString(StripHTML(p.Description))
	}
	for _, l := range p.Lists {
		fmt.Fprintf(&b, "\n\n%s\n%s", l.Text, StripHTML(l.Content))
	}
	return b.String(), nil
}

// ashbyBoard is the shape of api.ashbyhq.com's job-board endpoint: the
// whole board, not one posting, so FetchAshby finds jobID inside it.
type ashbyBoard struct {
	Jobs []struct {
		ID               string `json:"id"`
		Title            string `json:"title"`
		DescriptionPlain string `json:"descriptionPlain"`
		DescriptionHTML  string `json:"descriptionHtml"`
	} `json:"jobs"`
}

func fetchAshby(ctx context.Context, client *http.Client, slug, jobID string) (string, error) {
	return fetchAshbyAt(ctx, client, "https://api.ashbyhq.com", slug, jobID)
}

func fetchAshbyAt(ctx context.Context, client *http.Client, baseURL, slug, jobID string) (string, error) {
	apiURL := fmt.Sprintf("%s/posting-api/job-board/%s", baseURL, slug)
	var board ashbyBoard
	if err := getJSON(ctx, client, apiURL, &board); err != nil {
		return "", err
	}
	for _, j := range board.Jobs {
		if j.ID != jobID {
			continue
		}
		text := j.DescriptionPlain
		if text == "" {
			text = StripHTML(j.DescriptionHTML)
		}
		return j.Title + "\n\n" + text, nil
	}
	return "", fmt.Errorf("textfetch: job %s not found on ashby board %s", jobID, slug)
}
