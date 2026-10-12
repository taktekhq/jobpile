package main

import (
	"flag"

	"github.com/taktekhq/jobpile/internal/config"
	"github.com/taktekhq/jobpile/internal/rules"
	"github.com/taktekhq/jobpile/internal/store"
)

func cmdMatch(args []string) error {
	fs := flag.NewFlagSet("match", flag.ExitOnError)
	dataFlag := fs.String("data", "", "data folder (or set JOBPILE_DATA)")
	fs.Parse(args)

	dataDir, err := resolveDataDir(*dataFlag)
	if err != nil {
		return err
	}
	cfg, st, err := openEverything(dataDir)
	if err != nil {
		return err
	}
	defer st.Close()

	passed, skipped, err := runMatch(cfg, st)
	if err != nil {
		return err
	}
	logf("match done: passed=%d skipped=%d", passed, skipped)
	return nil
}

func runMatch(cfg *config.Config, st *store.Store) (passed, skipped int, err error) {
	jobs, err := st.AllMatchable()
	if err != nil {
		return 0, 0, err
	}

	titleWords := rules.TitleWords{
		SeniorPatterns:   cfg.Title.SeniorPatterns,
		EngineeringWords: cfg.Title.EngineeringWords,
		ExcludePatterns:  cfg.Title.ExcludePatterns,
	}
	regionWords := rules.RegionWords{
		Tier1Keywords:  cfg.Region.Tier1Keywords,
		Tier1Countries: cfg.Region.Tier1Countries,
		Tier2Keywords:  cfg.Region.Tier2Keywords,
	}

	for _, job := range jobs {
		j := job
		verdict, reason := rules.Evaluate(&j, titleWords, regionWords, cfg.Floor)
		if err := st.SaveMatchResult(j.ID, verdict, reason, j.Tier); err != nil {
			logf("match: saving result for %s: %v", j.ID, err)
			continue
		}
		if verdict == "pass" {
			passed++
		} else {
			skipped++
		}
	}
	return passed, skipped, nil
}
