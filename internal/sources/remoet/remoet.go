// Package remoet talks to the remoet.dev MCP endpoint over JSON-RPC and
// turns its job records into model.Job. remoet is a friend's tool: a
// public, agent-first job catalogue.
package remoet

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/taktekhq/jobpile/internal/model"
	"github.com/taktekhq/jobpile/internal/rules"
)

const defaultBaseURL = "https://api.remoet.dev/mcp"

// Client talks to one remoet account's MCP endpoint.
type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client

	// SessionID is set from the mcp-session-id response header on
	// Initialize, when the server sends one, and is then sent back on
	// every later call. Some remoet deployments do not require it.
	SessionID string

	nextID int
}

// NewClient builds a Client for the given API key. baseURL may be empty
// to use the real remoet endpoint.
func NewClient(baseURL, apiKey string) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		nextID:     1,
	}
}

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *int        `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// do sends one JSON-RPC call (or notification, when id is nil) and
// returns the raw result field. It understands both a plain JSON body
// and an SSE-framed one ("data: {...}"), since the spec notes remoet may
// send either.
func (c *Client) do(ctx context.Context, method string, params interface{}, withID bool) (json.RawMessage, error) {
	req := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	if withID {
		id := c.nextID
		c.nextID++
		req.ID = &id
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("remoet: encoding %s request: %w", method, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("remoet: building %s request: %w", method, err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if c.SessionID != "" {
		httpReq.Header.Set("mcp-session-id", c.SessionID)
	}

	resp, err := c.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("remoet: calling %s: %w", method, err)
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("mcp-session-id"); sid != "" {
		c.SessionID = sid
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("remoet: reading %s response: %w", method, err)
	}

	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Method: method}
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remoet: %s returned HTTP %d: %s", method, resp.StatusCode, truncate(string(respBody), 300))
	}

	if !withID {
		// A notification has no response body worth parsing.
		return nil, nil
	}

	payload := extractJSONPayload(respBody)
	var rpcResp rpcResponse
	if err := json.Unmarshal(payload, &rpcResp); err != nil {
		return nil, fmt.Errorf("remoet: parsing %s response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("remoet: %s error %d: %s", method, rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

// HTTPStatusError marks a 403 or 429 from remoet, the signal the spec
// says to stop this source for the rest of the run.
type HTTPStatusError struct {
	StatusCode int
	Method     string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("remoet: %s returned HTTP %d", e.Method, e.StatusCode)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// extractJSONPayload handles both a plain JSON body and an SSE-framed
// one, where the JSON sits after a "data:" line.
func extractJSONPayload(body []byte) []byte {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		return trimmed
	}
	scanner := bufio.NewScanner(bytes.NewReader(body))
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if after, ok := strings.CutPrefix(line, "data:"); ok {
			return []byte(strings.TrimSpace(after))
		}
	}
	return trimmed
}

// Initialize performs the MCP handshake: initialize, then the
// notifications/initialized notification.
func (c *Client) Initialize(ctx context.Context) error {
	params := map[string]interface{}{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "jobpile", "version": "0.1"},
	}
	if _, err := c.do(ctx, "initialize", params, true); err != nil {
		return fmt.Errorf("remoet: initialize: %w", err)
	}
	if _, err := c.do(ctx, "notifications/initialized", map[string]interface{}{}, false); err != nil {
		return fmt.Errorf("remoet: notifications/initialized: %w", err)
	}
	return nil
}

type toolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func (c *Client) callTool(ctx context.Context, name string, arguments interface{}) (string, error) {
	params := map[string]interface{}{"name": name, "arguments": arguments}
	raw, err := c.do(ctx, "tools/call", params, true)
	if err != nil {
		return "", err
	}
	var result toolCallResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("remoet: parsing tools/call result for %s: %w", name, err)
	}
	if len(result.Content) == 0 || result.Content[0].Type != "text" {
		return "", fmt.Errorf("remoet: tools/call %s returned no text content", name)
	}
	return result.Content[0].Text, nil
}

// Account is the shape of get_account's text payload, trimmed to the
// field jobpile cares about.
type Account struct {
	MCPRequestsToday struct {
		Used      int `json:"used"`
		Limit     int `json:"limit"`
		Remaining int `json:"remaining"`
	} `json:"mcpRequestsToday"`
}

// GetAccount calls the get_account tool and returns today's request
// budget.
func (c *Client) GetAccount(ctx context.Context) (Account, error) {
	text, err := c.callTool(ctx, "get_account", map[string]interface{}{})
	if err != nil {
		return Account{}, err
	}
	var acc Account
	if err := json.Unmarshal([]byte(text), &acc); err != nil {
		return Account{}, fmt.Errorf("remoet: parsing get_account payload: %w", err)
	}
	return acc, nil
}

// RawSalary is salary as remoet returns it: usually already USD/year,
// but period and currency can vary.
type RawSalary struct {
	From     float64 `json:"from"`
	To       float64 `json:"to"`
	Currency string  `json:"currency"`
	Period   string  `json:"period"`
}

// RawJob is one job exactly as remoet's search_jobs returns it. The
// newest roles have every pointer field nil until remoet enriches them.
type RawJob struct {
	ID                 string     `json:"id"`
	Title              string     `json:"title"`
	Company            string     `json:"company"`
	CompanySlug        string     `json:"companySlug"`
	URL                string     `json:"url"`
	ApplyURL           string     `json:"applyUrl"`
	CompanyURL         string     `json:"companyUrl"`
	Location           *string    `json:"location"`
	RemotePolicy       *string    `json:"remotePolicy"`
	RemoteRestrictions *string    `json:"remoteRestrictions"`
	ExperienceLevel    *string    `json:"experienceLevel"`
	Salary             *RawSalary `json:"salary"`
	TechStack          []string   `json:"techStack"`
	Summary            *string    `json:"summary"`
	FirstSeenAt        string     `json:"firstSeenAt"`
	LastVerifiedAt     *string    `json:"lastVerifiedAt"`
	DuplicateCount     int        `json:"duplicateCount"`
}

// SearchJobsResult is the shape of search_jobs's text payload.
type SearchJobsResult struct {
	Jobs        []RawJob `json:"jobs"`
	TotalCount  int      `json:"totalCount"`
	Page        int      `json:"page"`
	PageSize    int      `json:"pageSize"`
	TotalPages  int      `json:"totalPages"`
	HasNextPage bool     `json:"hasNextPage"`
}

// SearchJobs calls search_jobs for one page. extraArgs lets the caller
// add filters such as remotePolicy; it may be nil.
func (c *Client) SearchJobs(ctx context.Context, page, pageSize int, extraArgs map[string]interface{}) (SearchJobsResult, error) {
	args := map[string]interface{}{"page": page, "pageSize": pageSize}
	for k, v := range extraArgs {
		args[k] = v
	}
	text, err := c.callTool(ctx, "search_jobs", args)
	if err != nil {
		return SearchJobsResult{}, err
	}
	var result SearchJobsResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return SearchJobsResult{}, fmt.Errorf("remoet: parsing search_jobs payload: %w", err)
	}
	return result, nil
}

// IsUnenriched reports whether remoet has not yet filled in any of this
// job's enriched fields.
func (r RawJob) IsUnenriched() bool {
	return r.RemotePolicy == nil && r.RemoteRestrictions == nil && r.ExperienceLevel == nil && r.Salary == nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ToModel converts one raw remoet job into a model.Job. rates is the
// currency table used to convert a non-USD or non-yearly salary to
// yearly USD; a conversion error is non-fatal and leaves the salary
// fields unset, since remoet's own salary is already USD/year in the
// large majority of cases and a bad or unknown unit should not block
// the rest of the job's data.
func ToModel(r RawJob, rates map[string]float64) model.Job {
	job := model.Job{
		ID:           "remoet-" + r.ID,
		Source:       "remoet",
		SourceID:     r.ID,
		Company:      r.Company,
		Title:        r.Title,
		URL:          r.URL,
		ApplyURL:     r.ApplyURL,
		LocationText: deref(r.Location),
		RemotePolicy: deref(r.RemotePolicy),
		Seniority:    deref(r.ExperienceLevel),
		Stack:        r.TechStack,
	}

	if restr := deref(r.RemoteRestrictions); restr != "" {
		job.AllowedRegions = []string{restr}
	}

	if r.Salary != nil {
		job.SalaryRaw = fmt.Sprintf("%.0f-%.0f %s/%s", r.Salary.From, r.Salary.To, r.Salary.Currency, r.Salary.Period)
		if minUSD, err := rules.ConvertToYearlyUSD(r.Salary.From, r.Salary.Currency, r.Salary.Period, rates); err == nil {
			job.SalaryMinUSD = &minUSD
		}
		if maxUSD, err := rules.ConvertToYearlyUSD(r.Salary.To, r.Salary.Currency, r.Salary.Period, rates); err == nil {
			job.SalaryMaxUSD = &maxUSD
		}
	}

	if r.IsUnenriched() {
		job.RuleVerdict = "pending"
		job.RuleReason = "remoet has not enriched this role yet"
	}

	job.TextHash = textHash(r)
	return job
}

// textHash hashes the fields that matter for the judge: if none of them
// change between fetches, the job's text has not changed and the judge
// does not need to run again.
func textHash(r RawJob) string {
	h := sha256.New()
	h.Write([]byte(r.Title))
	h.Write([]byte(r.Company))
	h.Write([]byte(deref(r.RemotePolicy)))
	h.Write([]byte(deref(r.RemoteRestrictions)))
	h.Write([]byte(deref(r.ExperienceLevel)))
	h.Write([]byte(deref(r.Summary)))
	if r.Salary != nil {
		h.Write([]byte(strconv.FormatFloat(r.Salary.From, 'f', -1, 64)))
		h.Write([]byte(strconv.FormatFloat(r.Salary.To, 'f', -1, 64)))
		h.Write([]byte(r.Salary.Currency))
		h.Write([]byte(r.Salary.Period))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Summary returns the job summary text for the judge, or "" if remoet
// gave none.
func (r RawJob) SummaryText() string {
	return deref(r.Summary)
}
