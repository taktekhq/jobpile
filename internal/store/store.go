// Package store holds jobpile's SQLite layer: the jobs table, the dedup
// logic that keeps one row per role even when both sources list it, and
// the queries fetch, match, judge and list each need.
//
// It uses modernc.org/sqlite, a pure-Go driver, so jobpile cross-compiles
// to Linux (the XPS) without cgo.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/taktekhq/jobpile/internal/model"
)

// Store wraps the jobs.db SQLite database.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id             TEXT PRIMARY KEY,
	source         TEXT NOT NULL,
	source_id      TEXT NOT NULL,
	company        TEXT NOT NULL,
	title          TEXT NOT NULL,
	url            TEXT NOT NULL DEFAULT '',
	apply_url      TEXT NOT NULL DEFAULT '',
	location_text  TEXT NOT NULL DEFAULT '',
	alt_source     TEXT NOT NULL DEFAULT '',
	alt_url        TEXT NOT NULL DEFAULT '',
	alt_apply_url  TEXT NOT NULL DEFAULT '',
	remote_policy  TEXT NOT NULL DEFAULT '',
	allowed_regions TEXT NOT NULL DEFAULT '[]',
	salary_min_usd REAL,
	salary_max_usd REAL,
	salary_raw     TEXT NOT NULL DEFAULT '',
	seniority      TEXT NOT NULL DEFAULT '',
	stack          TEXT NOT NULL DEFAULT '[]',
	first_seen     TEXT NOT NULL,
	last_seen      TEXT NOT NULL,
	closed_at      TEXT,
	missed_runs    INTEGER NOT NULL DEFAULT 0,
	rule_verdict   TEXT NOT NULL DEFAULT '',
	rule_reason    TEXT NOT NULL DEFAULT '',
	tier           INTEGER NOT NULL DEFAULT 0,
	judge_verdict  TEXT NOT NULL DEFAULT '',
	judge_reason   TEXT NOT NULL DEFAULT '',
	judged_at      TEXT,
	text_hash      TEXT NOT NULL DEFAULT '',
	dedup_key      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_jobs_dedup_key ON jobs(dedup_key);
CREATE INDEX IF NOT EXISTS idx_jobs_source ON jobs(source);
CREATE INDEX IF NOT EXISTS idx_jobs_rule_verdict ON jobs(rule_verdict);
CREATE INDEX IF NOT EXISTS idx_jobs_judge_verdict ON jobs(judge_verdict);
`

// Open opens (creating if needed) the SQLite database at path and makes
// sure the jobs table exists.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// DedupKey normalises a company and title into the key used to match the
// same role across sources: lowercased, trimmed, with runs of
// whitespace collapsed to one space.
func DedupKey(company, title string) string {
	norm := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		return strings.Join(strings.Fields(s), " ")
	}
	return norm(company) + "|" + norm(title)
}

func timeStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTimeStr(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseTimePtr(s string) *time.Time {
	if s == "" {
		return nil
	}
	t := parseTimeStr(s)
	if t.IsZero() {
		return nil
	}
	return &t
}

func timePtrStr(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return timeStr(*t)
}

func toJSONList(items []string) string {
	if items == nil {
		items = []string{}
	}
	b, _ := json.Marshal(items)
	return string(b)
}

func fromJSONList(s string) []string {
	if s == "" {
		return nil
	}
	var items []string
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return nil
	}
	return items
}

// UpsertResult describes what UpsertFetched did with one freshly fetched
// job.
type UpsertResult struct {
	IsNew       bool // no row existed for this id or this dedup key
	Merged      bool // matched an existing row by company+title from the other source
	TextChanged bool // text_hash differs from the stored row, so the judge should re-run
	CanonicalID string
}

// UpsertFetched records one job seen during a fetch run. If a row with
// the same id exists, it is updated in place. Otherwise, if a row with
// the same normalised company+title exists (the other source already
// listed this role), that row is kept and this job's source, url and
// apply_url are recorded as its alt fields. Otherwise a new row is
// inserted.
func (s *Store) UpsertFetched(job *model.Job, now time.Time) (UpsertResult, error) {
	dedupKey := DedupKey(job.Company, job.Title)

	var existingID, existingTextHash, existingRuleVerdict string
	err := s.db.QueryRow(`SELECT id, text_hash, rule_verdict FROM jobs WHERE id = ?`, job.ID).Scan(&existingID, &existingTextHash, &existingRuleVerdict)
	switch {
	case err == nil:
		textChanged := existingTextHash != job.TextHash
		if err := s.updateExisting(job, existingID, existingRuleVerdict, now, textChanged); err != nil {
			return UpsertResult{}, err
		}
		return UpsertResult{IsNew: false, TextChanged: textChanged, CanonicalID: existingID}, nil
	case err != sql.ErrNoRows:
		return UpsertResult{}, fmt.Errorf("looking up job %s: %w", job.ID, err)
	}

	// No row by id. Look for the same role from the other source.
	var canonicalID string
	err = s.db.QueryRow(`SELECT id FROM jobs WHERE dedup_key = ? AND id != ? LIMIT 1`, dedupKey, job.ID).Scan(&canonicalID)
	switch {
	case err == nil:
		if err := s.mergeIntoCanonical(canonicalID, job, now); err != nil {
			return UpsertResult{}, err
		}
		return UpsertResult{IsNew: false, Merged: true, CanonicalID: canonicalID}, nil
	case err != sql.ErrNoRows:
		return UpsertResult{}, fmt.Errorf("looking up dedup key %s: %w", dedupKey, err)
	}

	// Brand new job. job.RuleVerdict is "pending" when the source has
	// not enriched it yet (every optional field null), or "" for a
	// normal job waiting for match to run.
	_, err = s.db.Exec(`
		INSERT INTO jobs (
			id, source, source_id, company, title, url, apply_url, location_text,
			remote_policy, allowed_regions, salary_min_usd, salary_max_usd, salary_raw,
			seniority, stack, first_seen, last_seen, missed_runs, text_hash, dedup_key,
			rule_verdict, rule_reason
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)
	`,
		job.ID, job.Source, job.SourceID, job.Company, job.Title, job.URL, job.ApplyURL, job.LocationText,
		job.RemotePolicy, toJSONList(job.AllowedRegions), job.SalaryMinUSD, job.SalaryMaxUSD, job.SalaryRaw,
		job.Seniority, toJSONList(job.Stack), timeStr(now), timeStr(now), job.TextHash, dedupKey,
		job.RuleVerdict, job.RuleReason,
	)
	if err != nil {
		return UpsertResult{}, fmt.Errorf("inserting job %s: %w", job.ID, err)
	}
	return UpsertResult{IsNew: true, TextChanged: true, CanonicalID: job.ID}, nil
}

func (s *Store) updateExisting(job *model.Job, id, existingRuleVerdict string, now time.Time, textChanged bool) error {
	judgeReset := ""
	if textChanged {
		// The listing changed since we last judged it, so clear the old
		// verdict and let judge pick it up again.
		judgeReset = ", judge_verdict = '', judge_reason = '', judged_at = NULL"
	}

	// job.RuleVerdict from the freshly fetched job is only ever "" or
	// "pending" (match has not run on this fetch pass). Stay "pending"
	// while the source still has not enriched the role. The moment it
	// is enriched, drop back to "" so match picks it up fresh. Once
	// match has set "pass" or "skip", a later fetch must not stomp on
	// that, so leave rule_verdict and rule_reason alone in that case.
	ruleClause := ""
	var ruleArgs []interface{}
	switch {
	case job.RuleVerdict == "pending":
		ruleClause = ", rule_verdict = ?, rule_reason = ?"
		ruleArgs = []interface{}{job.RuleVerdict, job.RuleReason}
	case existingRuleVerdict == "pending":
		ruleClause = ", rule_verdict = '', rule_reason = ''"
	}

	args := []interface{}{
		job.Company, job.Title, job.URL, job.ApplyURL, job.LocationText,
		job.RemotePolicy, toJSONList(job.AllowedRegions), job.SalaryMinUSD, job.SalaryMaxUSD,
		job.SalaryRaw, job.Seniority, toJSONList(job.Stack), timeStr(now),
		job.TextHash, DedupKey(job.Company, job.Title),
	}
	args = append(args, ruleArgs...)
	args = append(args, id)

	_, err := s.db.Exec(`
		UPDATE jobs SET
			company = ?, title = ?, url = ?, apply_url = ?, location_text = ?,
			remote_policy = ?, allowed_regions = ?, salary_min_usd = ?, salary_max_usd = ?,
			salary_raw = ?, seniority = ?, stack = ?, last_seen = ?, missed_runs = 0,
			closed_at = NULL, text_hash = ?, dedup_key = ?`+judgeReset+ruleClause+`
		WHERE id = ?`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("updating job %s: %w", id, err)
	}
	return nil
}

func (s *Store) mergeIntoCanonical(canonicalID string, job *model.Job, now time.Time) error {
	_, err := s.db.Exec(`
		UPDATE jobs SET alt_source = ?, alt_url = ?, alt_apply_url = ?,
			last_seen = ?, missed_runs = 0, closed_at = NULL
		WHERE id = ?`,
		job.Source, job.URL, job.ApplyURL, timeStr(now), canonicalID,
	)
	if err != nil {
		return fmt.Errorf("merging job %s into %s: %w", job.ID, canonicalID, err)
	}
	return nil
}

// MarkMissing is called once per source after a fetch run, with the set
// of job IDs from that source seen in this run. Every open job from that
// source not in seenIDs gets its missed_runs counter bumped; a job
// missing for two runs in a row is closed. It returns how many jobs were
// closed.
func (s *Store) MarkMissing(source string, seenIDs map[string]bool, now time.Time) (closed int, err error) {
	rows, err := s.db.Query(`SELECT id, missed_runs FROM jobs WHERE source = ? AND closed_at IS NULL`, source)
	if err != nil {
		return 0, fmt.Errorf("listing open jobs for %s: %w", source, err)
	}
	type missRow struct {
		id   string
		miss int
	}
	var toCheck []missRow
	for rows.Next() {
		var r missRow
		if err := rows.Scan(&r.id, &r.miss); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scanning job: %w", err)
		}
		toCheck = append(toCheck, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, r := range toCheck {
		if seenIDs[r.id] {
			continue
		}
		newMiss := r.miss + 1
		if newMiss >= 2 {
			if _, err := s.db.Exec(`UPDATE jobs SET missed_runs = ?, closed_at = ? WHERE id = ?`, newMiss, timeStr(now), r.id); err != nil {
				return closed, fmt.Errorf("closing job %s: %w", r.id, err)
			}
			closed++
		} else {
			if _, err := s.db.Exec(`UPDATE jobs SET missed_runs = ? WHERE id = ?`, newMiss, r.id); err != nil {
				return closed, fmt.Errorf("marking job %s missing: %w", r.id, err)
			}
		}
	}
	return closed, nil
}

// SaveMatchResult stores the outcome of the match rules for one job.
func (s *Store) SaveMatchResult(id, verdict, reason string, tier int) error {
	_, err := s.db.Exec(`UPDATE jobs SET rule_verdict = ?, rule_reason = ?, tier = ? WHERE id = ?`, verdict, reason, tier, id)
	if err != nil {
		return fmt.Errorf("saving match result for %s: %w", id, err)
	}
	return nil
}

// SaveJudgeResult stores the outcome of the judge for one job.
func (s *Store) SaveJudgeResult(id, verdict, reason string, judgedAt time.Time) error {
	_, err := s.db.Exec(`UPDATE jobs SET judge_verdict = ?, judge_reason = ?, judged_at = ? WHERE id = ?`, verdict, reason, timeStr(judgedAt), id)
	if err != nil {
		return fmt.Errorf("saving judge result for %s: %w", id, err)
	}
	return nil
}

var jobColumns = `id, source, source_id, company, title, url, apply_url, location_text,
	alt_source, alt_url, alt_apply_url, remote_policy, allowed_regions,
	salary_min_usd, salary_max_usd, salary_raw, seniority, stack,
	first_seen, last_seen, closed_at, missed_runs,
	rule_verdict, rule_reason, tier, judge_verdict, judge_reason, judged_at, text_hash`

func scanJob(row interface{ Scan(...interface{}) error }) (model.Job, error) {
	var j model.Job
	var allowedRegions, stack string
	var closedAt sql.NullString
	var judgedAt sql.NullString
	var firstSeen, lastSeen string
	var salaryMin, salaryMax sql.NullFloat64

	err := row.Scan(
		&j.ID, &j.Source, &j.SourceID, &j.Company, &j.Title, &j.URL, &j.ApplyURL, &j.LocationText,
		&j.AltSource, &j.AltURL, &j.AltApplyURL, &j.RemotePolicy, &allowedRegions,
		&salaryMin, &salaryMax, &j.SalaryRaw, &j.Seniority, &stack,
		&firstSeen, &lastSeen, &closedAt, &j.MissedRuns,
		&j.RuleVerdict, &j.RuleReason, &j.Tier, &j.JudgeVerdict, &j.JudgeReason, &judgedAt, &j.TextHash,
	)
	if err != nil {
		return model.Job{}, err
	}
	j.AllowedRegions = fromJSONList(allowedRegions)
	j.Stack = fromJSONList(stack)
	j.FirstSeen = parseTimeStr(firstSeen)
	j.LastSeen = parseTimeStr(lastSeen)
	if closedAt.Valid {
		j.ClosedAt = parseTimePtr(closedAt.String)
	}
	if judgedAt.Valid {
		j.JudgedAt = parseTimePtr(judgedAt.String)
	}
	if salaryMin.Valid {
		v := salaryMin.Float64
		j.SalaryMinUSD = &v
	}
	if salaryMax.Valid {
		v := salaryMax.Float64
		j.SalaryMaxUSD = &v
	}
	return j, nil
}

// GetByID returns one job by its id, or sql.ErrNoRows if it does not
// exist.
func (s *Store) GetByID(id string) (model.Job, error) {
	row := s.db.QueryRow(`SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id)
	return scanJob(row)
}

func (s *Store) queryJobs(query string, args ...interface{}) ([]model.Job, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying jobs: %w", err)
	}
	defer rows.Close()
	var jobs []model.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning job row: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// PassedUnjudged returns open jobs that passed the match rules and have
// no judge verdict yet for their current text, oldest first, up to
// limit (0 means no limit).
func (s *Store) PassedUnjudged(limit int) ([]model.Job, error) {
	q := `SELECT ` + jobColumns + ` FROM jobs
		WHERE closed_at IS NULL AND rule_verdict = 'pass' AND judge_verdict = ''
		ORDER BY first_seen ASC`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	return s.queryJobs(q)
}

// AllUnmatched returns open jobs that have not been through match yet.
// It deliberately excludes "pending" jobs: remoet roles so new that
// every enriched field was still null when fetched. match leaves those
// alone; the next fetch flips a job back to "" the moment remoet
// enriches it, and only then does match pick it up.
func (s *Store) AllUnmatched() ([]model.Job, error) {
	return s.queryJobs(`SELECT ` + jobColumns + ` FROM jobs WHERE closed_at IS NULL AND rule_verdict = ''`)
}

// Pending returns open jobs still waiting on remoet to enrich them.
func (s *Store) Pending() ([]model.Job, error) {
	return s.queryJobs(`SELECT ` + jobColumns + ` FROM jobs WHERE closed_at IS NULL AND rule_verdict = 'pending'`)
}

// ByJudgeVerdict returns open jobs with the given judge verdict.
func (s *Store) ByJudgeVerdict(verdict string) ([]model.Job, error) {
	return s.queryJobs(`SELECT `+jobColumns+` FROM jobs WHERE closed_at IS NULL AND judge_verdict = ?`, verdict)
}

// RuleSkipped returns open jobs the rules skipped.
func (s *Store) RuleSkipped() ([]model.Job, error) {
	return s.queryJobs(`SELECT ` + jobColumns + ` FROM jobs WHERE closed_at IS NULL AND rule_verdict = 'skip'`)
}

// JudgeSkipped returns open jobs the judge skipped.
func (s *Store) JudgeSkipped() ([]model.Job, error) {
	return s.queryJobs(`SELECT ` + jobColumns + ` FROM jobs WHERE closed_at IS NULL AND judge_verdict = 'skip'`)
}

// Maybe returns open jobs the judge marked maybe or pending (new roles
// remoet has not enriched yet).
func (s *Store) Maybe() ([]model.Job, error) {
	return s.queryJobs(`SELECT ` + jobColumns + ` FROM jobs WHERE closed_at IS NULL AND judge_verdict IN ('maybe', 'pending')`)
}

// Counts summarises the jobs table for runs.log.
type Counts struct {
	Total        int
	Open         int
	Closed       int
	RulePassed   int
	RuleSkipped  int
	JudgeApply   int
	JudgeMaybe   int
	JudgeSkip    int
	JudgePending int
}

// Summarize returns counts of the jobs table as it stands right now.
func (s *Store) Summarize() (Counts, error) {
	var c Counts
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&c.Total); err != nil {
		return c, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE closed_at IS NULL`).Scan(&c.Open); err != nil {
		return c, err
	}
	c.Closed = c.Total - c.Open
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE closed_at IS NULL AND rule_verdict = 'pass'`).Scan(&c.RulePassed); err != nil {
		return c, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE closed_at IS NULL AND rule_verdict = 'skip'`).Scan(&c.RuleSkipped); err != nil {
		return c, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE closed_at IS NULL AND judge_verdict = 'apply'`).Scan(&c.JudgeApply); err != nil {
		return c, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE closed_at IS NULL AND judge_verdict = 'maybe'`).Scan(&c.JudgeMaybe); err != nil {
		return c, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE closed_at IS NULL AND judge_verdict = 'skip'`).Scan(&c.JudgeSkip); err != nil {
		return c, err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE closed_at IS NULL AND judge_verdict = 'pending'`).Scan(&c.JudgePending); err != nil {
		return c, err
	}
	return c, nil
}
