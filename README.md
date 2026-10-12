# jobpile

Pull remote job listings onto your own disk, filter them with plain rules, let a
cheap model check the shortlist for hidden blockers, and get three local files:
apply, maybe, skip. Each job carries a reason.

jobpile never applies, never messages anyone, and never posts anything. You go
through `apply.md` yourself and apply by hand.

See `docs/superpowers/specs/2026-10-12-jobpile-design.md` for the full design.

## What it pulls from

- [remoet.dev](https://remoet.dev), a public job catalogue, over its MCP
  endpoint. You need your own remoet API key.
- [Himalayas](https://himalayas.app), a public remote-jobs API. No key needed.

## Install

Go 1.27 or later.

```
go install github.com/taktekhq/jobpile/cmd/jobpile@latest
```

Or clone the repo and run `go build ./cmd/jobpile`.

## Set up a data folder

jobpile keeps everything outside the repo, in a folder you choose with `--data`
or the `JOBPILE_DATA` environment variable. Nothing in this folder is meant to
be shared or committed anywhere.

```
mkdir -p ~/jobpile-data
cp config.example.toml ~/jobpile-data/config.toml
cp profile.example.md  ~/jobpile-data/profile.md
```

Edit `config.toml`: the pay floor, the currency table, the title and region
word lists, and the model command the judge runs. The comments in
`config.example.toml` explain each setting.

Edit `profile.md`: your own stack, years, titles, and hard limits, written as
plain sentences. The judge reads this file as-is to screen each job against
it. `profile.example.md` is a made-up example person, to show the shape.

### Get a remoet API key

Sign up at [remoet.dev](https://remoet.dev) and generate an API key from your
account. jobpile reads the key one of two ways:

- Set the `REMOET_API_KEY` environment variable, or
- Set `key_command` in `config.toml` to a shell command whose output is the
  key, for example a password manager CLI. jobpile runs that command itself
  and never prints or logs the key.

## Run it

```
export JOBPILE_DATA=~/jobpile-data
jobpile run
```

`run` does all four steps in order: `fetch`, `match`, `judge`, `list`. Each
step can also run on its own:

```
jobpile fetch   # pull remoet and Himalayas into jobs.db
jobpile match   # apply the rules, mark each job pass or skip with a reason
jobpile judge   # run the model on jobs that passed, up to --max (default 150)
jobpile list    # write apply.md, maybe.md, skip.md
```

`judge` and `run` skip judging and say so if `profile.md` does not exist yet
in the data folder.

If you edit `profile.md` or change the judge's prompt or config and want
jobs re-screened under the new rules, add `--rejudge`: it clears every
already-judged verdict first, then judges those plus up to `--max` more in
the same run, so nothing judged under the old rules sits stale.

```
jobpile judge --rejudge --max 300
```

### If judge reports a lot of "judge failed"

`judge_failed` in the run's output, or a `maybe.md` reason of exactly "judge
failed", means the model returned something that did not parse as the
expected JSON, twice in a row. A job the model command failed to even run,
twice, logs instead and is left unjudged for a later run to retry, rather
than being counted here. Read `text/<job id>.txt` for what the judge
actually saw, and check the model command's own error output (jobpile now
includes stdout in that error, not just stderr, since some wrappers put the
real error there).

On one machine this showed up as `claude -p`'s own reported prompt size
randomly ballooning past its 200k token limit on a prompt that was really a
few thousand tokens. `concurrency = 1` did not fully fix it there: the
actual cause was that machine running many other, unrelated `claude -p`
headless sessions at the same time (other bots, other tools), not jobpile's
own concurrency setting. If you see the same thing, check what else on the
machine is running headless Claude sessions concurrently; a quieter machine
or time window helps more than lowering jobpile's own concurrency alone.

## What the output means

- `apply.md`: jobs that passed the rules and the judge found nothing wrong
  with. Read this one.
- `maybe.md`: jobs the judge found unclear on something, for example a
  listing that never says where it hires. Ends with a one-line count of
  roles still pending: brand new remoet listings it has not enriched yet,
  picked up again once a later fetch sees them enriched. They are not
  listed by title, since most will not even reach the title rule once
  enriched.
- `skip.md`: jobs that failed either the rules or the judge, under separate
  headings, so a wrong rule is easy to spot and fix.

Each line looks like this:

```
- [NEW] Acme · Staff Backend Engineer · $160k-$190k · tier 1 (worldwide/GMT+3) · seen 2026-10-12 · <link>
  Why: Go platform team, async, hires contractors anywhere.
```

Tier 1 means worldwide or a region that includes a GMT+3 window; tier 2 means
US-only. `[NEW]` marks a job seen for the first time on the run that produced
this file.

`runs.log` in the data folder gets one line per `run`, with counts for
fetched, new, closed, passed, judged, and apply.

## Running it for someone else

jobpile has no idea who "you" are beyond `config.toml` and `profile.md`. Give
a second person their own data folder and their own profile, and run jobpile
against it with `--data`:

```
jobpile run --data ~/jobpile-data-gracia
```

Nothing about the first person's data folder is read or touched.

## Testing

```
go test ./...
go vet ./...
```

Rule and source tests replay real, trimmed API responses saved in each
package's `testdata/` folder. The judge is tested against small fake model
scripts, including one that returns invalid JSON, to check the retry and
fallback path works without spending anything real.

## A note on the two sources

remoet's whole catalogue (about 15,800 roles at the time this was written)
fits comfortably inside its daily call budget, so `fetch` crawls it in full
each run. That full picture is what lets jobpile tell a closed job from one
it simply did not ask about: a job missing from two runs in a row gets
closed.

Himalayas lists around 120,000 jobs, far too many to crawl nightly, so
`fetch` instead runs a handful of senior-title keyword searches against its
search API and stops paging a query once it reaches a job it already has.
That means jobpile never sees Himalayas' full feed, so it does not try to
close a Himalayas job on its own; a Himalayas job only closes if it turned
out to be the same role as a remoet listing that itself closed.

## Where the judge's text comes from

Himalayas already includes the full listing, so its description is used
as-is. remoet only gives a short summary, so for a job that passed the
rules, `judge` tries to get the real listing text before asking the model:

1. If the apply link is on Greenhouse, Lever or Ashby, their own public
   JSON API is read directly. A plain fetch of those apply pages often
   returns almost nothing useful, since they render the listing with
   JavaScript.
2. Otherwise, the apply link's HTML is fetched directly and stripped of
   tags.
3. If that fails or returns nothing, remoet's own summary, tech stack,
   seniority and remote restrictions are combined into a fallback.

Each job's `text/<job id>.txt` file is whatever text the judge actually
read, so a thin-looking verdict is easy to check by hand.

## License

MIT. See `LICENSE`.
