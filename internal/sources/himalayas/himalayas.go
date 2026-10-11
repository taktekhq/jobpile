// Package himalayas talks to the public, keyless himalayas.app jobs
// API and turns its records into model.Job.
//
// Himalayas lists about 120k jobs, far more than jobpile should pull
// every night. jobpile uses the search endpoint
// (himalayas.app/jobs/api/search) with a keyword and a seniority filter
// instead of crawling the full browse feed: a handful of queries for
// our senior title words ("staff engineer", "principal engineer", and
// so on) with seniority in Senior/Manager/Director/Executive, sorted
// by recency. That is a few dozen calls a night, not thousands, and it
// already excludes most of the non-engineering 120k. The browse
// endpoint (himalayas.app/jobs/api) is not used in v1; this is
// documented here rather than left implicit.
package himalayas

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/taktekhq/jobpile/internal/model"
	"github.com/taktekhq/jobpile/internal/rules"
)

const defaultBaseURL = "https://himalayas.app"

// Client talks to the Himalayas public API. No API key is needed.
type Client struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewClient builds a Client. baseURL may be empty to use the real
// Himalayas endpoint.
func NewClient(baseURL string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL:    baseURL,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// RawJob is one job exactly as Himalayas returns it.
type RawJob struct {
	Title                string    `json:"title"`
	Excerpt              string    `json:"excerpt"`
	CompanyName          string    `json:"companyName"`
	CompanySlug          string    `json:"companySlug"`
	EmploymentType       string    `json:"employmentType"`
	MinSalary            *float64  `json:"minSalary"`
	MaxSalary            *float64  `json:"maxSalary"`
	SalaryPeriod         string    `json:"salaryPeriod"`
	Currency             string    `json:"currency"`
	Seniority            []string  `json:"seniority"`
	LocationRestrictions []string  `json:"locationRestrictions"`
	TimezoneRestrictions []float64 `json:"timezoneRestrictions"`
	Categories           []string  `json:"categories"`
	ParentCategories     []string  `json:"parentCategories"`
	Description          string    `json:"description"`
	PubDate              int64     `json:"pubDate"`
	ExpiryDate           int64     `json:"expiryDate"`
	ApplicationLink      string    `json:"applicationLink"`
	GUID                 string    `json:"guid"`
}

// SearchResult is the shape of the search endpoint's response.
type SearchResult struct {
	TotalCount int      `json:"totalCount"`
	Offset     int      `json:"offset"`
	Limit      int      `json:"limit"`
	Jobs       []RawJob `json:"jobs"`
}

// SearchParams is the subset of the search endpoint's query parameters
// jobpile uses.
type SearchParams struct {
	Query     string   // q
	Seniority []string // one or more of Entry-level, Mid-level, Senior, Manager, Director, Executive
	Worldwide bool
	Sort      string // "recent" is what jobpile wants: newest first
	Page      int
}

func (p SearchParams) toValues() url.Values {
	v := url.Values{}
	if p.Query != "" {
		v.Set("q", p.Query)
	}
	for _, s := range p.Seniority {
		v.Add("seniority", s)
	}
	if p.Worldwide {
		v.Set("worldwide", "true")
	}
	if p.Sort != "" {
		v.Set("sort", p.Sort)
	}
	if p.Page > 0 {
		v.Set("page", strconv.Itoa(p.Page))
	}
	return v
}

func (c *Client) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	u := c.BaseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("himalayas: building request for %s: %w", path, err)
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("himalayas: calling %s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("himalayas: reading %s response: %w", path, err)
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Path: path}
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("himalayas: %s returned HTTP %d: %s", path, resp.StatusCode, truncate(string(body), 300))
	}
	return body, nil
}

// HTTPStatusError marks a 403 or 429 from Himalayas, the signal to stop
// this source for the rest of the run.
type HTTPStatusError struct {
	StatusCode int
	Path       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("himalayas: %s returned HTTP %d", e.Path, e.StatusCode)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Search calls the search endpoint for one page.
func (c *Client) Search(ctx context.Context, params SearchParams) (SearchResult, error) {
	body, err := c.get(ctx, "/jobs/api/search", params.toValues())
	if err != nil {
		return SearchResult{}, err
	}
	var result SearchResult
	if err := json.Unmarshal(body, &result); err != nil {
		return SearchResult{}, fmt.Errorf("himalayas: parsing search response: %w", err)
	}
	return result, nil
}

func sourceIDFromGUID(guid string) string {
	h := sha256Hex(guid)
	return h[:16]
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// gmt3 is the UTC offset jobpile's tier 1 timezone window must cover.
const gmt3 = 3.0

// ToModel converts one raw Himalayas job into a model.Job. rates is the
// currency table used to convert salary to yearly USD.
func ToModel(r RawJob, rates map[string]float64) model.Job {
	sourceID := sourceIDFromGUID(r.GUID)
	job := model.Job{
		ID:       "himalayas-" + sourceID,
		Source:   "himalayas",
		SourceID: r.GUID,
		Company:  r.CompanyName,
		Title:    r.Title,
		URL:      r.GUID,
		ApplyURL: r.ApplicationLink,
		// Himalayas only lists remote roles, so every job is "remote"
		// unless its own restrictions narrow that down.
		RemotePolicy: "remote",
		Seniority:    strings.Join(r.Seniority, ", "),
		Stack:        r.Categories,
		ListingText:  r.DescriptionText(),
	}

	switch {
	case len(r.LocationRestrictions) > 0:
		job.AllowedRegions = append([]string{}, r.LocationRestrictions...)
	case len(r.TimezoneRestrictions) > 0 && !timezonesCoverGMT3(r.TimezoneRestrictions):
		job.AllowedRegions = []string{fmt.Sprintf("timezone restricted, not GMT+3: %v", r.TimezoneRestrictions)}
	default:
		// No location restriction, and either no timezone restriction
		// or one wide enough to include GMT+3: worldwide.
		job.AllowedRegions = nil
	}

	if r.MinSalary != nil || r.MaxSalary != nil {
		min := floatOrZero(r.MinSalary)
		max := floatOrZero(r.MaxSalary)
		job.SalaryRaw = fmt.Sprintf("%.0f-%.0f %s/%s", min, max, r.Currency, r.SalaryPeriod)
		if r.MinSalary != nil {
			if v, err := rules.ConvertToYearlyUSD(*r.MinSalary, r.Currency, r.SalaryPeriod, rates); err == nil {
				job.SalaryMinUSD = &v
			}
		}
		if r.MaxSalary != nil {
			if v, err := rules.ConvertToYearlyUSD(*r.MaxSalary, r.Currency, r.SalaryPeriod, rates); err == nil {
				job.SalaryMaxUSD = &v
			}
		}
	}

	job.TextHash = textHash(r)
	return job
}

func floatOrZero(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}

func timezonesCoverGMT3(offsets []float64) bool {
	for _, o := range offsets {
		if o == gmt3 {
			return true
		}
	}
	return false
}

func textHash(r RawJob) string {
	parts := []string{
		r.Title, r.CompanyName, r.SalaryPeriod, r.Currency,
		strings.Join(r.Seniority, ","), strings.Join(r.LocationRestrictions, ","),
		r.Description,
	}
	if r.MinSalary != nil {
		parts = append(parts, strconv.FormatFloat(*r.MinSalary, 'f', -1, 64))
	}
	if r.MaxSalary != nil {
		parts = append(parts, strconv.FormatFloat(*r.MaxSalary, 'f', -1, 64))
	}
	return sha256Hex(strings.Join(parts, "|"))
}

// DescriptionText returns the job's full HTML description with the
// tags stripped, for the judge. Himalayas already includes the full
// listing, so unlike remoet, jobpile never needs to fetch anything
// else for the judge's text.
func (r RawJob) DescriptionText() string {
	return stripHTML(r.Description)
}

func stripHTML(s string) string {
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
