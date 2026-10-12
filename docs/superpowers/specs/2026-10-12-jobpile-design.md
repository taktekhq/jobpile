# jobpile: design

Date: 2026-10-12. Status: draft for review.

## Goal

Find one remote job for Nizar that pays at least $80k a year. Pull job listings from public sources onto
a local disk, filter them with plain rules, have a cheap model check the shortlist for hidden blockers,
and write three local files: apply, maybe, skip. Each job carries a reason.

jobpile never applies, never messages anyone and never posts anything. Nizar and the bot go through
`apply.md` together and apply by hand.

If it works for Nizar, the same tool runs for Gracia with a second profile and config.

## What Nizar decided

- Roles: Staff or Principal backend engineer, Architect, Tech Lead or Lead Engineer, Engineering
  Manager, Founding Engineer. All four kinds count.
- Location, tier 1: worldwide, or a region that includes Lebanon (EMEA, Middle East, or a time-zone
  window that covers GMT+3).
- Location, tier 2: US-only remote roles, on the bet that some hire a contractor through his company.
- Pay: floor $80k a year. A listing with a salary must reach the floor at the top of its range. A
  listing with no salary passes and can land in the apply pile.
- Exclusions: none (no industry is off limits).
- Delivery: local files only. No messages.
- Applying: out of scope for now.

## Sources (v1)

1. **remoet.dev MCP** (a friend's tool). `search_jobs` pages the whole public catalogue: 15,815 roles,
   317 pages of 50 on 12 Oct. The budget is 5,000 calls a day, so a full pull is about 6% of it. Fields:
   salary normalised to `{from, to, currency, period}` on about 55% of roles, `remotePolicy`,
   `remoteRestrictions` (country text), `experienceLevel`, `techStack`, `summary`, `firstSeenAt`,
   `lastVerifiedAt`, `applyUrl`. The newest roles come back with every enriched field null until remoet
   processes them, so jobpile re-reads a role while its fields are null.
2. **Himalayas** public API, `himalayas.app/jobs/api`: about 120k remote jobs with `minSalary`,
   `maxSalary`, `currency` and `locationRestrictions` (empty means worldwide). Wider than tech.

Out of v1: our own Greenhouse, Lever and Ashby crawler (remoet already reads careers pages), HN Who's
Hiring, LinkedIn, Indeed, Bandana (its robots.txt blocks ClaudeBot and it lists local hourly work).

## Shape

One Go binary, `jobpile`, with four subcommands. Each one reads and writes the data folder and can run
on its own.

```
jobpile fetch   # pull every source into jobs.db and text/
jobpile match   # apply the rules, mark each job pass or skip with a rule reason
jobpile judge   # run the model on new passing jobs, store apply/maybe/skip + reason
jobpile list    # write apply.md, maybe.md, skip.md
jobpile run     # all four in order (what the nightly job calls)
```

### Data folder

Set by `--data` or `JOBPILE_DATA`. Never inside the repo. Holds:

- `config.toml`: floor, currency rates, tier rules, title patterns, sources, model command.
- `profile.md`: the candidate's stack, years, titles and hard limits. Written from kbase, kept private.
- `jobs.db`: SQLite.
- `text/<source>-<id>.txt`: the text the judge reads, one file per job, for grep.
- `apply.md`, `maybe.md`, `skip.md`: the output.

### jobs table

`id` (source plus source id), `source`, `company`, `title`, `url`, `apply_url`, `location_text`,
`remote_policy`, `allowed_regions` (normalised list), `salary_min_usd`, `salary_max_usd`, `salary_raw`,
`seniority`, `stack`, `first_seen`, `last_seen`, `closed_at`, `rule_verdict`, `rule_reason`, `tier`,
`judge_verdict`, `judge_reason`, `judged_at`, `text_hash`.

- Same role in both sources: matched on normalised company plus title. Kept once, with both URLs.
- A job missing from two runs in a row gets `closed_at` and drops out of the lists.
- When `text_hash` changes, the judge runs again.

## Rules (`match`)

In this order. The first one that fails sets `rule_verdict = skip` and the reason.

1. **Title.** Must match a senior pattern from config (staff, principal, senior, lead, architect,
   engineering manager, head of engineering, founding engineer) and an engineering word (engineer,
   developer, architect, engineering). Out: junior, intern, frontend-only, mobile-only (iOS, Android),
   data scientist, sales engineer, support, QA-only.
2. **Location tier.** Tier 1 if worldwide, or the allowed regions include Lebanon, Middle East, MENA,
   EMEA, or a UTC window that covers +3. Tier 2 if US-only. Otherwise skip ("EU only", "India only").
   Onsite and hybrid roles skip.
3. **Pay.** Convert to yearly USD: hourly × 2,080, monthly × 12, other currencies by the fixed table in
   config. A listing with a salary skips if the top of its range is under the floor. No salary passes.

## Judge (`judge`)

Runs only on jobs that passed the rules and have no judge verdict for their current text.

- Text: remoet gives a summary, not the full listing, so for jobs that pass the rules jobpile fetches
  `apply_url` once (plain curl, browser user agent, one request at a time, stop the source at the first
  403 or 429). A failed fetch falls back to the summary and says so in the reason.
- Model: `claude -p --model haiku --tools "" --strict-mcp-config --disable-slash-commands`, one job per call, prompt plus `profile.md` plus the job
  text. The command is set in config.
- Prompt order: first list what the listing requires (work authorisation, residence, clearance, onsite
  days, travel, stack, years, pay type). Then compare that list with the profile. Then answer.
- Output: JSON `{"verdict": "apply" | "maybe" | "skip", "blockers": [...], "reason": "one line"}`. Bad
  JSON is retried once, then stored as `maybe` with reason "judge failed".
- `maybe` means the listing is unclear on a blocker, for example it never says where it hires.

## Output (`list`)

`apply.md`, `maybe.md` and `skip.md`. Tier 1 before tier 2. Inside a tier, by pay top-down, then jobs
with no salary. Each line:

```
- [NEW] Acme · Staff Backend Engineer · $160k-$190k · tier 1 (worldwide) · seen 2026-10-12 · <link>
  Why: Go platform team, async, hires contractors anywhere.
```

`skip.md` lists rule skips and judge skips under separate headings, so a wrong rule is easy to spot.

## Where it runs

The XPS, nightly, under `~/taktekhq/jobpile-data`, from a systemd user timer. The source of the run
lives in the repo (`deploy/`). The Mac is only for development.

## Errors

- A source that fails (network, 403, 429, bad JSON) is logged and skipped for that run. The other
  source still runs. Stored jobs are not marked closed when their source failed.
- remoet's daily budget: jobpile reads `get_account` first and stops before 4,000 calls.
- Every run appends one line to `runs.log`: counts fetched, new, closed, passed, judged, apply.

## Testing

- Unit tests for the rules: title patterns, region parsing, salary conversion, tiers. Fixtures are real
  listings saved from both sources, not hand-made ones.
- Source tests replay saved API responses (remoet pages, Himalayas pages), including a remoet role with
  every enriched field null.
- The judge is tested with a fake model command that returns fixed JSON, including bad JSON.
- One live smoke run against both sources before the timer is turned on.

## Repo

`taktekhq/jobpile`, public, MIT, "Taktek and Nizar Mahmoud". README a stranger can follow:
install, get a remoet key, write a profile, run. No profile, no key, no data in the repo.
`config.example.toml` and `profile.example.md` ship instead.

## Out of scope

Applying, cover letters, messages, a web page, Gracia's profile (next round), more sources.
