package main

import (
	"context"
	"flag"
	"fmt"
	"time"
)

// cmdRun runs fetch, match, judge and list in order: what the nightly
// job calls.
func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dataFlag := fs.String("data", "", "data folder (or set JOBPILE_DATA)")
	maxFlag := fs.Int("max", 150, "most jobs to judge in one run")
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

	ctx := context.Background()
	now := time.Now()
	var fc fetchCounts

	if err := fetchRemoet(ctx, cfg, st, now, &fc); err != nil {
		logf("remoet: %v", err)
	}
	if err := fetchHimalayas(ctx, cfg, st, now, &fc); err != nil {
		logf("himalayas: %v", err)
	}

	passed, ruleSkipped, err := runMatch(cfg, st)
	if err != nil {
		logf("match: %v", err)
	}

	apply, maybe, judgeSkip := 0, 0, 0
	profileMD, ok, perr := readProfile(dataDir)
	if perr != nil {
		logf("judge: %v", perr)
	} else if !ok {
		logf("judge: no profile.md in %s yet (written by someone else); skipping judge for this run", dataDir)
	} else {
		apply, maybe, judgeSkip, err = runJudge(cfg, st, dataDir, profileMD, *maxFlag)
		if err != nil {
			logf("judge: %v", err)
		}
	}

	listCounts, err := runList(st, dataDir)
	if err != nil {
		logf("list: %v", err)
	}

	summary, err := st.Summarize()
	if err != nil {
		logf("summarize: %v", err)
	}

	line := fmt.Sprintf(
		"fetch remoet(fetched=%d new=%d closed=%d stopped=%q) himalayas(fetched=%d new=%d stopped=%q) match(passed=%d skipped=%d) judge(apply=%d maybe=%d skip=%d) list(apply=%d maybe=%d ruleSkip=%d judgeSkip=%d) total_open=%d",
		fc.RemoetFetched, fc.RemoetNew, fc.RemoetClosed, fc.RemoetStopped,
		fc.HimalayasFetched, fc.HimalayasNew, fc.HimalayasStopped,
		passed, ruleSkipped,
		apply, maybe, judgeSkip,
		listCounts[0], listCounts[1], listCounts[2], listCounts[3],
		summary.Open,
	)
	if err := appendRunsLog(dataDir, line); err != nil {
		logf("runs.log: %v", err)
	}
	logf("run done: %s", line)
	return nil
}
