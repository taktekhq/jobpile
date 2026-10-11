package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/taktekhq/jobpile/internal/config"
	"github.com/taktekhq/jobpile/internal/judge"
	"github.com/taktekhq/jobpile/internal/model"
	"github.com/taktekhq/jobpile/internal/store"
	"github.com/taktekhq/jobpile/internal/textfetch"
)

func cmdJudge(args []string) error {
	fs := flag.NewFlagSet("judge", flag.ExitOnError)
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

	profileMD, ok, err := readProfile(dataDir)
	if err != nil {
		return err
	}
	if !ok {
		logf("judge: no profile.md in %s yet (written by someone else); skipping judge for this run", dataDir)
		return nil
	}

	apply, maybe, skip, judgeErr := runJudge(cfg, st, dataDir, profileMD, *maxFlag)
	logf("judge done: apply=%d maybe=%d skip=%d", apply, maybe, skip)
	return judgeErr
}

func runJudge(cfg *config.Config, st *store.Store, dataDir, profileMD string, max int) (apply, maybe, skip int, err error) {
	run, err := judge.NewShellRunner(cfg.Judge.Command, 120*time.Second)
	if err != nil {
		return 0, 0, 0, err
	}

	jobs, err := st.PassedUnjudged(max)
	if err != nil {
		return 0, 0, 0, err
	}
	if len(jobs) == 0 {
		return 0, 0, 0, nil
	}

	concurrency := cfg.Judge.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	type result struct {
		job     model.Job
		verdict judge.Verdict
		err     error
	}

	jobsCh := make(chan model.Job)
	resultsCh := make(chan result)
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobsCh {
				v, text, jerr := judgeOneJob(run, dataDir, profileMD, j)
				if jerr == nil {
					writeJobText(dataDir, j, text)
				}
				resultsCh <- result{job: j, verdict: v, err: jerr}
			}
		}()
	}

	go func() {
		for _, j := range jobs {
			jobsCh <- j
		}
		close(jobsCh)
	}()
	go func() {
		wg.Wait()
		close(resultsCh)
	}()

	now := time.Now()
	for r := range resultsCh {
		if r.err != nil {
			logf("judge: job %s: %v", r.job.ID, r.err)
			continue
		}
		if err := st.SaveJudgeResult(r.job.ID, r.verdict.Verdict, r.verdict.Reason, now); err != nil {
			logf("judge: saving result for %s: %v", r.job.ID, err)
			continue
		}
		switch r.verdict.Verdict {
		case "apply":
			apply++
		case "maybe":
			maybe++
		case "skip":
			skip++
		}
	}
	return apply, maybe, skip, nil
}

func judgeOneJob(run judge.Runner, dataDir, profileMD string, j model.Job) (judge.Verdict, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	text := j.ListingText
	if j.Source == "remoet" && j.ApplyURL != "" {
		fetched, err := textfetch.Fetch(ctx, nil, j.ApplyURL)
		if err != nil {
			logf("judge: fetching apply_url for %s failed (%v), falling back to the summary", j.ID, err)
		} else if fetched != "" {
			text = fetched
		}
	}
	if text == "" {
		text = "(no listing text available)"
	}

	v, err := judge.JudgeOne(ctx, run, profileMD, j.Title, j.Company, text)
	return v, text, err
}

func writeJobText(dataDir string, j model.Job, text string) {
	path := filepath.Join(dataDir, "text", j.ID+".txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		logf("judge: writing %s: %v", path, err)
	}
}
