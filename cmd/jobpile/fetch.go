package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/taktekhq/jobpile/internal/config"
	"github.com/taktekhq/jobpile/internal/sources/himalayas"
	"github.com/taktekhq/jobpile/internal/sources/remoet"
	"github.com/taktekhq/jobpile/internal/store"
)

// fetchCounts is what fetch reports to runs.log and the console.
type fetchCounts struct {
	RemoetFetched    int
	RemoetNew        int
	RemoetClosed     int
	RemoetStopped    string // reason fetching stopped early, or "" if it finished
	HimalayasFetched int
	HimalayasNew     int
	HimalayasStopped string
}

func cmdFetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
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

	ctx := context.Background()
	now := time.Now()
	var counts fetchCounts

	if err := fetchRemoet(ctx, cfg, st, now, &counts); err != nil {
		logf("remoet: %v", err)
	}
	if err := fetchHimalayas(ctx, cfg, st, now, &counts); err != nil {
		logf("himalayas: %v", err)
	}

	logf("fetch done: remoet fetched=%d new=%d closed=%d stopped=%q; himalayas fetched=%d new=%d stopped=%q",
		counts.RemoetFetched, counts.RemoetNew, counts.RemoetClosed, counts.RemoetStopped,
		counts.HimalayasFetched, counts.HimalayasNew, counts.HimalayasStopped)
	return nil
}

func fetchRemoet(ctx context.Context, cfg *config.Config, st *store.Store, now time.Time, counts *fetchCounts) error {
	apiKey, err := cfg.Remoet.RemoetAPIKey(runKeyCommand)
	if err != nil {
		counts.RemoetStopped = "no API key"
		return err
	}

	client := remoet.NewClient(cfg.Remoet.BaseURL, apiKey)
	if err := client.Initialize(ctx); err != nil {
		counts.RemoetStopped = "initialize failed"
		return err
	}

	account, err := client.GetAccount(ctx)
	if err != nil {
		logf("remoet: get_account failed, proceeding without a budget check: %v", err)
	} else if account.MCPRequestsToday.Used >= cfg.Remoet.BudgetStopBefore {
		counts.RemoetStopped = fmt.Sprintf("budget: %d calls used today, stopping before %d", account.MCPRequestsToday.Used, cfg.Remoet.BudgetStopBefore)
		logf("remoet: %s", counts.RemoetStopped)
		return nil
	}

	// A full, unfiltered crawl of every page. At today's catalogue size
	// (15,815 roles, 317 pages of 50) this is about 320 calls, well
	// inside the 5,000/day budget, and it is the only way to see every
	// currently open remoet role so MarkMissing can tell a closed job
	// from one jobpile simply did not ask about. Filtering server side
	// (e.g. remotePolicy=remote) would cut calls further but would
	// break that accounting, since a job outside the filter would look
	// "missing" forever. This also naturally picks up the newest,
	// still-unenriched roles without a separate pass.
	pageSize := 50
	seen := map[string]bool{}
	page := 1
	completed := false

	for {
		result, err := client.SearchJobs(ctx, page, pageSize, nil)
		if err != nil {
			if _, ok := err.(*remoet.HTTPStatusError); ok {
				counts.RemoetStopped = err.Error()
				logf("remoet: %v, stopping for this run", err)
				break
			}
			counts.RemoetStopped = err.Error()
			return err
		}

		for _, raw := range result.Jobs {
			job := remoet.ToModel(raw, cfg.CurrencyRates)
			seen["remoet-"+raw.ID] = true
			res, err := st.UpsertFetched(&job, now)
			if err != nil {
				logf("remoet: storing job %s: %v", raw.ID, err)
				continue
			}
			counts.RemoetFetched++
			if res.IsNew {
				counts.RemoetNew++
			}
		}

		if !result.HasNextPage || len(result.Jobs) == 0 {
			completed = true
			break
		}
		page++
		sleepBetweenCalls(cfg.Remoet.RequestDelayMS)
	}

	if completed {
		closed, err := st.MarkMissing("remoet", seen, now)
		if err != nil {
			logf("remoet: MarkMissing: %v", err)
		} else {
			counts.RemoetClosed = closed
		}
	} else {
		logf("remoet: crawl did not finish, not marking any job closed this run")
	}
	return nil
}

func fetchHimalayas(ctx context.Context, cfg *config.Config, st *store.Store, now time.Time, counts *fetchCounts) error {
	client := himalayas.NewClient(cfg.Himalayas.BaseURL)

	// jobpile does not crawl Himalayas' full ~120k-job feed. It runs a
	// handful of senior-title keyword searches, newest first, and
	// stops paginating a query the moment it sees a job it already
	// has. That means jobpile never sees the full set of currently
	// open Himalayas jobs, so unlike remoet it does not call
	// MarkMissing for this source: there is no reliable way to tell a
	// job that closed from one that simply fell outside this run's
	// keywords. A Himalayas job only closes if remoet's own
	// missing-run tracking closes its merged, canonical row, or not at
	// all. This is a deliberate v1 trade-off, documented here and in
	// the README rather than left implicit.
	for _, query := range cfg.Himalayas.Queries {
		stopped, err := fetchHimalayasQuery(ctx, cfg, st, client, now, query, counts)
		if err != nil {
			counts.HimalayasStopped = err.Error()
			return err
		}
		if stopped {
			counts.HimalayasStopped = "403/429"
			return nil
		}
	}
	return nil
}

// fetchHimalayasQuery pages through one query, newest first, stopping
// as soon as a page contains a job jobpile already has. It returns
// true if a 403/429 means the whole Himalayas source should stop for
// this run.
func fetchHimalayasQuery(ctx context.Context, cfg *config.Config, st *store.Store, client *himalayas.Client, now time.Time, query string, counts *fetchCounts) (bool, error) {
	for page := 1; page <= cfg.Himalayas.MaxPages; page++ {
		result, err := client.Search(ctx, himalayasSearchParams(cfg, query, page))
		if err != nil {
			if _, ok := err.(*himalayas.HTTPStatusError); ok {
				logf("himalayas: %v, stopping this source for the run", err)
				return true, nil
			}
			return false, err
		}
		if len(result.Jobs) == 0 {
			return false, nil
		}

		sawKnown := false
		for _, raw := range result.Jobs {
			job := himalayas.ToModel(raw, cfg.CurrencyRates)
			existedBefore := jobExists(st, job.ID)
			res, err := st.UpsertFetched(&job, now)
			if err != nil {
				logf("himalayas: storing job: %v", err)
				continue
			}
			counts.HimalayasFetched++
			if res.IsNew {
				counts.HimalayasNew++
			}
			if existedBefore {
				sawKnown = true
			}
		}
		if sawKnown {
			return false, nil
		}
		sleepBetweenCalls(cfg.Himalayas.RequestDelayMS)
	}
	return false, nil
}

func himalayasSearchParams(cfg *config.Config, query string, page int) himalayas.SearchParams {
	return himalayas.SearchParams{
		Query:     query,
		Seniority: cfg.Himalayas.SeniorityFilter,
		Sort:      "recent",
		Page:      page,
	}
}

func jobExists(st *store.Store, id string) bool {
	_, err := st.GetByID(id)
	return err == nil
}
