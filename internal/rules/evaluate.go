package rules

import "github.com/taktekhq/jobpile/internal/model"

// Evaluate runs the title, location and pay rules on job in order and
// stops at the first one that fails. It sets RuleVerdict, RuleReason and
// Tier on job and also returns them for convenience.
func Evaluate(job *model.Job, titleWords TitleWords, regionWords RegionWords, floor float64) (verdict, reason string) {
	if pass, why := MatchTitle(job.Title, titleWords); !pass {
		job.RuleVerdict = "skip"
		job.RuleReason = why
		job.Tier = 0
		return job.RuleVerdict, job.RuleReason
	}

	tier, pass, why := ClassifyLocation(job.RemotePolicy, job.AllowedRegions, regionWords)
	job.Tier = tier
	if !pass {
		job.RuleVerdict = "skip"
		job.RuleReason = why
		return job.RuleVerdict, job.RuleReason
	}

	if pass, why := CheckSalary(job.SalaryMaxUSD, floor); !pass {
		job.RuleVerdict = "skip"
		job.RuleReason = why
		return job.RuleVerdict, job.RuleReason
	}

	job.RuleVerdict = "pass"
	job.RuleReason = "passed title, location and pay"
	return job.RuleVerdict, job.RuleReason
}
