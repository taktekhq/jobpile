// Package model holds the shared Job type that every other package in
// jobpile reads or writes. One struct, one source of truth, so the store,
// the rules, the sources and the judge all agree on field names.
package model

import "time"

// Job is one row of the jobs table. Field names match the column names in
// the design doc so the mapping to SQLite stays obvious.
type Job struct {
	ID           string // source plus source id, e.g. "remoet-12345"
	Source       string // "remoet" or "himalayas"
	SourceID     string // the id as the source gave it, before the prefix
	Company      string
	Title        string
	URL          string
	ApplyURL     string
	LocationText string

	// RemotePolicy is one of "remote", "remote-restricted", "hybrid",
	// "onsite", or "" when the source did not say.
	RemotePolicy string

	// AllowedRegions is the normalised list of countries or regions the
	// listing restricts remote work to. Empty means worldwide.
	AllowedRegions []string

	SalaryMinUSD *float64
	SalaryMaxUSD *float64
	SalaryRaw    string

	Seniority string
	Stack     []string

	FirstSeen time.Time
	LastSeen  time.Time
	ClosedAt  *time.Time

	RuleVerdict string // "pass", "skip", or "" before match runs
	RuleReason  string

	// Tier is 1 (worldwide or GMT+3 region), 2 (US-only), or 0 when the
	// rules have not run yet or the job skipped on tier.
	Tier int

	JudgeVerdict string // "apply", "maybe", "skip", "pending", or ""
	JudgeReason  string
	JudgedAt     *time.Time

	TextHash string
}
