package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/taktekhq/jobpile/internal/model"
	"github.com/taktekhq/jobpile/internal/store"
)

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	dataFlag := fs.String("data", "", "data folder (or set JOBPILE_DATA)")
	fs.Parse(args)

	dataDir, err := resolveDataDir(*dataFlag)
	if err != nil {
		return err
	}
	_, st, err := openEverything(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	counts, err := runList(st, dataDir)
	if err != nil {
		return err
	}
	logf("list done: apply=%d maybe=%d ruleSkip=%d judgeSkip=%d", counts[0], counts[1], counts[2], counts[3])
	return nil
}

// runList writes apply.md, maybe.md and skip.md and returns
// [applyCount, maybeCount, ruleSkipCount, judgeSkipCount].
func runList(st *store.Store, dataDir string) ([4]int, error) {
	var counts [4]int

	applyJobs, err := st.ByJudgeVerdict("apply")
	if err != nil {
		return counts, err
	}
	maybeJobs, err := st.Maybe()
	if err != nil {
		return counts, err
	}
	pendingJobs, err := st.Pending()
	if err != nil {
		return counts, err
	}
	ruleSkipped, err := st.RuleSkipped()
	if err != nil {
		return counts, err
	}
	judgeSkipped, err := st.JudgeSkipped()
	if err != nil {
		return counts, err
	}

	counts[0] = len(applyJobs)
	counts[1] = len(maybeJobs)
	counts[2] = len(ruleSkipped)
	counts[3] = len(judgeSkipped)

	if err := writeSectionedList(filepath.Join(dataDir, "apply.md"), []string{"Apply"}, map[string][]model.Job{"Apply": applyJobs}); err != nil {
		return counts, err
	}

	maybeSections := map[string][]model.Job{"Maybe": maybeJobs}
	if len(pendingJobs) > 0 {
		maybeSections["Pending (remoet has not enriched these yet)"] = pendingJobs
	}
	if err := writeSectionedList(filepath.Join(dataDir, "maybe.md"), []string{"Maybe", "Pending (remoet has not enriched these yet)"}, maybeSections); err != nil {
		return counts, err
	}

	skipSections := map[string][]model.Job{
		"Skipped by the rules": ruleSkipped,
		"Skipped by the judge": judgeSkipped,
	}
	if err := writeSectionedList(filepath.Join(dataDir, "skip.md"), []string{"Skipped by the rules", "Skipped by the judge"}, skipSections); err != nil {
		return counts, err
	}

	return counts, nil
}

func writeSectionedList(path string, order []string, sections map[string][]model.Job) error {
	var b strings.Builder
	for _, heading := range order {
		jobs := sections[heading]
		fmt.Fprintf(&b, "# %s (%d)\n\n", heading, len(jobs))
		if len(jobs) == 0 {
			b.WriteString("Nothing here.\n\n")
			continue
		}
		for _, line := range formatJobLines(jobs) {
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(strings.TrimRight(b.String(), "\n")+"\n"), 0o644)
}

// sortForOutput orders tier 1 before tier 2, then by pay top-down
// within a tier, then jobs with no salary last.
func sortForOutput(jobs []model.Job) {
	sort.SliceStable(jobs, func(i, j int) bool {
		a, b := jobs[i], jobs[j]
		if a.Tier != b.Tier {
			// Tier 0 (rule/judge skips, no tier assigned) sorts after
			// tier 1 and 2, but that distinction rarely matters since
			// each list only holds one kind of job anyway.
			ta, tb := a.Tier, b.Tier
			if ta == 0 {
				ta = 99
			}
			if tb == 0 {
				tb = 99
			}
			return ta < tb
		}
		aHas, bHas := a.SalaryMaxUSD != nil, b.SalaryMaxUSD != nil
		if aHas != bHas {
			return aHas // salary listed sorts before no salary
		}
		if aHas && bHas && *a.SalaryMaxUSD != *b.SalaryMaxUSD {
			return *a.SalaryMaxUSD > *b.SalaryMaxUSD
		}
		return a.Company < b.Company
	})
}

func formatJobLines(jobs []model.Job) []string {
	sorted := append([]model.Job{}, jobs...)
	sortForOutput(sorted)
	lines := make([]string, 0, len(sorted))
	for _, j := range sorted {
		lines = append(lines, formatJobLine(j))
	}
	return lines
}

func formatJobLine(j model.Job) string {
	var b strings.Builder
	if j.FirstSeen.Equal(j.LastSeen) {
		b.WriteString("- [NEW] ")
	} else {
		b.WriteString("- ")
	}
	fmt.Fprintf(&b, "%s · %s · %s · %s · seen %s · %s",
		valueOr(j.Company, "(unknown company)"),
		valueOr(j.Title, "(untitled)"),
		salaryLabel(j),
		tierLabel(j),
		j.LastSeen.Format("2006-01-02"),
		applyLink(j),
	)
	reason := j.JudgeReason
	if reason == "" {
		reason = j.RuleReason
	}
	if reason != "" {
		fmt.Fprintf(&b, "\n  Why: %s", reason)
	}
	return b.String()
}

func valueOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func salaryLabel(j model.Job) string {
	if j.SalaryMinUSD == nil && j.SalaryMaxUSD == nil {
		return "salary not listed"
	}
	if j.SalaryMinUSD != nil && j.SalaryMaxUSD != nil {
		return fmt.Sprintf("$%s-$%s", formatK(*j.SalaryMinUSD), formatK(*j.SalaryMaxUSD))
	}
	if j.SalaryMaxUSD != nil {
		return fmt.Sprintf("up to $%s", formatK(*j.SalaryMaxUSD))
	}
	return fmt.Sprintf("from $%s", formatK(*j.SalaryMinUSD))
}

func formatK(usd float64) string {
	return fmt.Sprintf("%.0fk", usd/1000)
}

func tierLabel(j model.Job) string {
	switch j.Tier {
	case 1:
		return "tier 1 (worldwide/GMT+3)"
	case 2:
		return "tier 2 (US-only)"
	default:
		return "no tier"
	}
}

func applyLink(j model.Job) string {
	if j.ApplyURL != "" {
		return j.ApplyURL
	}
	return j.URL
}
