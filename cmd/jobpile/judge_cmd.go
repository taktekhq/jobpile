package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	rejudgeFlag := fs.Bool("rejudge", false, "clear every already-judged verdict first, so a changed prompt or profile re-judges it")
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

	max := *maxFlag
	if *rejudgeFlag {
		reset, err := st.ResetAllJudged()
		if err != nil {
			return err
		}
		logf("judge: --rejudge reset %d already-judged job(s); will judge up to %d more too", reset, max)
		max += reset
	}

	apply, maybe, skip, failed, judgeErr := runJudge(cfg, st, dataDir, profileMD, max)
	logf("judge done: apply=%d maybe=%d skip=%d judge_failed=%d", apply, maybe, skip, failed)
	return judgeErr
}

func runJudge(cfg *config.Config, st *store.Store, dataDir, profileMD string, max int) (apply, maybe, skip, failed int, err error) {
	run, err := judge.NewShellRunner(cfg.Judge.Command, 120*time.Second)
	if err != nil {
		return 0, 0, 0, 0, err
	}

	jobs, err := st.PassedUnjudged(max)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if len(jobs) == 0 {
		return 0, 0, 0, 0, nil
	}

	concurrency := cfg.Judge.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	type result struct {
		job     model.Job
		verdict judge.Verdict
		text    string
		source  string
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
				text, source := jobText(j)
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				v, jerr := judge.JudgeOne(ctx, run, profileMD, j.Tier, j.Title, j.Company, text)
				cancel()
				if jerr == nil {
					writeJobText(dataDir, j, text)
				}
				resultsCh <- result{job: j, verdict: v, text: text, source: source, err: jerr}
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
		if err := st.SaveJudgeResult(r.job.ID, r.verdict.Verdict, r.verdict.Reason, r.source, now); err != nil {
			logf("judge: saving result for %s: %v", r.job.ID, err)
			continue
		}
		switch r.verdict.Verdict {
		case "apply":
			apply++
		case "maybe":
			if r.verdict.Reason == "judge failed" {
				failed++
			}
			maybe++
		case "skip":
			skip++
		}
	}
	return apply, maybe, skip, failed, nil
}

// jobText picks the best available text for the judge and says where
// it came from, for text_source.
//
//   - Himalayas already includes the full listing, so its stripped
//     description is used as-is: no extra fetch, per spec.
//   - remoet only gives a summary. For a job whose apply_url is on
//     Greenhouse, Lever or Ashby, their own public JSON API is read
//     instead of the apply page's HTML: those pages are commonly
//     JS-rendered and a plain GET returns almost nothing (this is why
//     "listing incomplete, only title" showed up so often in the first
//     live run's maybe/skip reasons). Otherwise the apply_url's HTML is
//     fetched directly. If every fetch attempt fails or returns
//     nothing useful, remoet's own summary, tech stack and remote
//     restrictions are combined into a fallback.
func jobText(j model.Job) (text, source string) {
	if j.Source != "remoet" {
		return j.ListingText, "himalayas_description"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if j.ApplyURL != "" {
		if board, slug, jobID, ok := textfetch.DetectBoard(j.ApplyURL); ok {
			if fetched, err := textfetch.FetchBoard(ctx, nil, board, slug, jobID); err == nil && strings.TrimSpace(fetched) != "" {
				return fetched, "board:" + string(board)
			} else if err != nil {
				logf("judge: fetching %s board for %s failed (%v)", board, j.ID, err)
			}
		} else if fetched, err := textfetch.Fetch(ctx, nil, j.ApplyURL); err == nil && strings.TrimSpace(fetched) != "" {
			return fetched, "apply_url_html"
		} else if err != nil {
			logf("judge: fetching apply_url for %s failed (%v), falling back to the summary", j.ID, err)
		}
	}

	return remoetFallbackText(j), "remoet_summary_fallback"
}

// remoetFallbackText combines everything remoet gave jobpile directly
// (no extra fetch) into one block of text for the judge, so a failed
// or empty apply_url fetch still has more than a bare title to judge.
func remoetFallbackText(j model.Job) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s at %s\n", j.Title, j.Company)
	if j.Seniority != "" {
		fmt.Fprintf(&b, "Experience level: %s\n", j.Seniority)
	}
	if len(j.Stack) > 0 {
		fmt.Fprintf(&b, "Tech stack: %s\n", strings.Join(j.Stack, ", "))
	}
	if j.RemotePolicy != "" {
		fmt.Fprintf(&b, "Remote policy: %s\n", j.RemotePolicy)
	}
	if len(j.AllowedRegions) > 0 {
		fmt.Fprintf(&b, "Remote restrictions: %s\n", strings.Join(j.AllowedRegions, ", "))
	}
	if j.SalaryRaw != "" {
		fmt.Fprintf(&b, "Salary: %s\n", j.SalaryRaw)
	}
	if j.ListingText != "" {
		fmt.Fprintf(&b, "\nSummary: %s\n", j.ListingText)
	}
	if b.Len() == 0 {
		return "(no listing text available)"
	}
	return b.String()
}

func writeJobText(dataDir string, j model.Job, text string) {
	path := filepath.Join(dataDir, "text", j.ID+".txt")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		logf("judge: writing %s: %v", path, err)
	}
}
