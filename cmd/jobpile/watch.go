package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/taktekhq/jobpile/internal/config"
	"github.com/taktekhq/jobpile/internal/watch"
)

// watchSummary is the one-line summary printed at the end of a watch
// run, and reused by its test.
type watchSummary struct {
	Fetched       int
	Matched       int
	Notified      int
	SkippedSeen   int
	SkippedIgnore int
	NotifyFailed  int
}

func (s watchSummary) String() string {
	return fmt.Sprintf("fetched=%d matched=%d notified=%d skipped_seen=%d skipped_ignore=%d notify_failed=%d",
		s.Fetched, s.Matched, s.Notified, s.SkippedSeen, s.SkippedIgnore, s.NotifyFailed)
}

func cmdWatch(args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	dataFlag := fs.String("data", "", "data folder (or set JOBPILE_DATA)")
	lookbackFlag := fs.Int("lookback", 0, "days to search back (0 = use config.toml's watch.lookback_days)")
	dryRunFlag := fs.Bool("dry-run", false, "print what would be notified, without calling notify_command or writing watch-seen.json")
	timeoutFlag := fs.Duration("timeout", 2*time.Minute, "give up and exit after this long, so a hung mail server or notify_command cannot block the next scheduled run")
	fs.Parse(args)

	dataDir, err := resolveDataDir(*dataFlag)
	if err != nil {
		return err
	}

	cfg, err := config.Load(filepath.Join(dataDir, "config.toml"))
	if err != nil {
		return err
	}
	wc := cfg.Watch
	if wc.IMAPHost == "" {
		return fmt.Errorf("watch: config.toml has no [watch] imap_host set; see config.example.toml")
	}

	password, err := wc.WatchPassword(runKeyCommand)
	if err != nil {
		return err
	}

	lookbackDays := wc.LookbackDays
	if *lookbackFlag > 0 {
		lookbackDays = *lookbackFlag
	}
	since := time.Now().AddDate(0, 0, -lookbackDays)

	ctx, cancel := context.WithTimeout(context.Background(), *timeoutFlag)
	defer cancel()

	type outcome struct {
		summary watchSummary
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		s, err := runWatch(ctx, wc, dataDir, password, since, *dryRunFlag)
		done <- outcome{summary: s, err: err}
	}()

	select {
	case o := <-done:
		if o.err != nil {
			return o.err
		}
		logf("watch done: %s", o.summary)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("watch: timed out after %s", *timeoutFlag)
	}
}

// runWatch does the actual work: fetch candidates since 'since',
// match them against configured companies, drop ignored
// confirmations and already-seen messages, and notify on the rest.
func runWatch(ctx context.Context, wc config.WatchConfig, dataDir, password string, since time.Time, dryRun bool) (watchSummary, error) {
	var sum watchSummary

	addr := watch.AddrOrPort(wc.IMAPHost)
	candidates, err := watch.FetchCandidates(ctx, addr, wc.IMAPUser, password, wc.Mailbox, since)
	if err != nil {
		return sum, err
	}
	sum.Fetched = len(candidates)

	seenPath := filepath.Join(dataDir, "watch-seen.json")
	seen, err := watch.LoadSeen(seenPath)
	if err != nil {
		return sum, err
	}

	for _, cand := range candidates {
		d := decideCandidate(cand, wc, seen)
		switch d.Reason {
		case "no_match":
			continue
		case "ignored":
			sum.Matched++
			sum.SkippedIgnore++
			continue
		case "seen":
			sum.Matched++
			sum.SkippedSeen++
			continue
		}
		sum.Matched++

		text := buildNotificationText(cand, d.Company, wc.NotifyFooter)

		if dryRun {
			fmt.Fprintf(os.Stdout, "--- would notify (%s) ---\n%s\n\n", cand.MessageID, text)
			sum.Notified++
			continue
		}

		if err := watch.RunNotify(ctx, wc.NotifyCommand, text, 30*time.Second); err != nil {
			logf("watch: notify for %s: %v", cand.MessageID, err)
			sum.NotifyFailed++
			continue
		}

		sum.Notified++
		seen.Add(cand.MessageID)
		if err := seen.Save(); err != nil {
			// The notify already went out; losing the save just means
			// this one may be reported again next run, which is the
			// safe direction to fail in.
			logf("watch: saving %s: %v", seenPath, err)
		}
	}

	return sum, nil
}

// candidateDecision is what decideCandidate worked out for one
// fetched message: which company it matched, if any, and what
// runWatch should do about it.
type candidateDecision struct {
	Company string
	Reason  string // "no_match", "ignored", "seen", or "notify"
}

// decideCandidate applies the match, ignore, and seen rules to one
// candidate message, in that order, exactly as the README describes:
// a message is notified on only once it has matched a company, is
// not an ignored confirmation, and has not already been reported.
func decideCandidate(cand watch.Candidate, wc config.WatchConfig, seen *watch.SeenStore) candidateDecision {
	company, ok := watch.MatchCompany(cand.From, cand.FromName, cand.Subject, wc.Companies)
	if !ok {
		return candidateDecision{Reason: "no_match"}
	}
	if _, ignored := watch.MatchIgnore(cand.Subject, wc.IgnoreSubjects); ignored {
		return candidateDecision{Company: company, Reason: "ignored"}
	}
	if seen.Has(cand.MessageID) {
		return candidateDecision{Company: company, Reason: "seen"}
	}
	return candidateDecision{Company: company, Reason: "notify"}
}

// buildNotificationText assembles the text handed to notify_command
// for one matched, new reply.
func buildNotificationText(cand watch.Candidate, company, footer string) string {
	preview := watch.CollapsePreview(cand.Preview, 300)
	from := cand.From
	if cand.FromName != "" {
		from = fmt.Sprintf("%s <%s>", cand.FromName, cand.From)
	}
	dateUTC := cand.Date.UTC().Format("2006-01-02 15:04 UTC")
	return watch.FormatNotification(company, cand.Subject, from, dateUTC, preview, footer)
}
