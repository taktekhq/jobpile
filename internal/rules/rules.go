// Package rules holds jobpile's plain, config-driven filters: the title
// check, the location-tier check, and the pay check. match runs them in
// order and stops at the first one that fails.
package rules

import (
	"fmt"
	"strings"
)

// TitleWords is the word lists the title rule matches against.
type TitleWords struct {
	SeniorPatterns   []string
	EngineeringWords []string
	ExcludePatterns  []string
}

// RegionWords is the word and country lists the location-tier rule
// matches against.
type RegionWords struct {
	Tier1Keywords  []string
	Tier1Countries []string
	Tier2Keywords  []string
}

func containsAny(haystack string, needles []string) (string, bool) {
	lower := strings.ToLower(haystack)
	for _, n := range needles {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		if strings.Contains(lower, n) {
			return n, true
		}
	}
	return "", false
}

// MatchTitle applies the title rule: the title must contain a senior
// pattern and an engineering word, and must not contain an exclude
// pattern. It returns whether the title passes, and a short reason for
// humans either way.
func MatchTitle(title string, w TitleWords) (pass bool, reason string) {
	if hit, ok := containsAny(title, w.ExcludePatterns); ok {
		return false, fmt.Sprintf("title excluded: %q", hit)
	}
	seniorHit, hasSenior := containsAny(title, w.SeniorPatterns)
	engHit, hasEng := containsAny(title, w.EngineeringWords)
	if !hasSenior || !hasEng {
		return false, "title: no senior and engineering word match"
	}
	return true, fmt.Sprintf("title matched: %q + %q", seniorHit, engHit)
}

// ClassifyLocation applies the location-tier rule. remotePolicy is one of
// "remote", "remote-restricted", "hybrid", "onsite", or "" when unknown.
// allowedRegions is the normalised list of countries or regions the role
// restricts remote work to; empty means worldwide. It returns the tier
// (1 or 2, 0 on skip), whether the job passes, and a reason.
func ClassifyLocation(remotePolicy string, allowedRegions []string, w RegionWords) (tier int, pass bool, reason string) {
	switch strings.ToLower(strings.TrimSpace(remotePolicy)) {
	case "hybrid":
		return 0, false, "location: hybrid"
	case "onsite":
		return 0, false, "location: onsite"
	}

	if len(allowedRegions) == 0 {
		return 1, true, "location: worldwide"
	}

	joined := strings.Join(allowedRegions, ", ")

	if hit, ok := containsAny(joined, w.Tier1Keywords); ok {
		return 1, true, fmt.Sprintf("location: tier 1 keyword %q", hit)
	}
	for _, region := range allowedRegions {
		if hit, ok := matchesAnyElement(region, w.Tier1Countries); ok {
			return 1, true, fmt.Sprintf("location: tier 1 country %q", hit)
		}
	}

	if allElementsMatch(allowedRegions, w.Tier2Keywords) {
		return 2, true, "location: tier 2, US-only"
	}

	return 0, false, fmt.Sprintf("location: %s not covered by tier 1 or tier 2", joined)
}

func matchesAnyElement(element string, list []string) (string, bool) {
	e := strings.ToLower(strings.TrimSpace(element))
	for _, item := range list {
		if strings.ToLower(strings.TrimSpace(item)) == e {
			return item, true
		}
	}
	return "", false
}

func allElementsMatch(elements []string, list []string) bool {
	if len(elements) == 0 {
		return false
	}
	for _, e := range elements {
		if _, ok := matchesAnyElement(e, list); !ok {
			return false
		}
	}
	return true
}

// ConvertToYearlyUSD turns one salary figure into a yearly USD amount.
// period is "year", "month", or "hour" (case-insensitive). rates maps an
// ISO currency code to how many units of that currency equal one USD;
// USD itself should map to 1.
func ConvertToYearlyUSD(amount float64, currency string, period string, rates map[string]float64) (float64, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		currency = "USD"
	}
	rate, ok := rates[currency]
	if !ok {
		return 0, fmt.Errorf("no currency rate configured for %q", currency)
	}
	if rate <= 0 {
		return 0, fmt.Errorf("invalid currency rate for %q: %v", currency, rate)
	}
	usd := amount / rate

	switch strings.ToLower(strings.TrimSpace(period)) {
	case "", "year", "yearly", "annual", "annually":
		return usd, nil
	case "month", "monthly":
		return usd * 12, nil
	case "hour", "hourly":
		return usd * 2080, nil
	default:
		return 0, fmt.Errorf("unknown salary period %q", period)
	}
}

// CheckSalary applies the pay rule. maxUSD is the top of the role's
// yearly USD salary range, already converted; nil means the listing gave
// no salary, which always passes.
func CheckSalary(maxUSD *float64, floor float64) (pass bool, reason string) {
	if maxUSD == nil {
		return true, "salary: not listed"
	}
	if *maxUSD < floor {
		return false, fmt.Sprintf("salary: top of range $%.0f below floor $%.0f", *maxUSD, floor)
	}
	return true, fmt.Sprintf("salary: top of range $%.0f meets floor $%.0f", *maxUSD, floor)
}
