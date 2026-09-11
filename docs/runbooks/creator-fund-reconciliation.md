# Creator fund: does the statement match the dashboard?

The last line of the creator-fund release-acceptance table (plan *Verification and release acceptance*, audit *How a view becomes a rupee*): **"public and creator counts reconcile within a published window."** This is that window, published, and the query that checks it, with what it found on the development database on 12 September 2026.

## The three numbers, and why two of them are one

A creator sees three view counts:

1. **The public counter** on a post. post-service serves it from analytics-service's `POST /v1/analytics/internal/content-views`, which reads `views_display` from `analytics.content_daily_summary` plus the open hourly buckets (plan 5B, audit M-13).
2. **The dashboard**: the same `content_daily_summary` rows, through the contract view `analytics.v_creator_daily_metrics_v1` (plan 5C, audit M-15).
3. **The statement**: `creator_fund_earnings.view_count`, which monetization-service computed from the contract view's rows for that creator and day, canonicalised to `flick` / `long_video`, and fingerprinted into `input_revision` (plan 2B).

Numbers 1 and 2 are the same rows read through two doors, so they cannot disagree except by the hourly buckets the public counter adds for today. The reconciliation is therefore between 2 and 3: **dashboard versus statement**, per creator, day and content type.

## The tolerance

| Day | Rule |
|---|---|
| **Frozen** — not in `analytics.rollup_progress` with `status='open'` (older than the 48-hour reprocessing window, plan 1E) | **Exact.** Views and watch time on the statement equal the dashboard's, or the row is a finding. A frozen day cannot be rewritten by the rollup; the only path that changes it is an admin `?force=1` rerun, which logs loudly, and the only path that changes the statement is a correction adjustment (plan 2A). |
| **Open** — inside the window | **Dashboard may run ahead**; it may never be behind. The next accrual sees `ErrInputRevisionChanged` for a day whose rows moved and the statement catches up through a correction adjustment. A statement row *above* the dashboard on an open day is a finding. |
| **No statement row** on a frozen day with dashboard views | A finding **unless explained**: the creator is `ineligible` (or has no `creator_fund_eligibility` row, which is the same thing), the content is `ineligible`/`deleted` from its effective date (plan 1F/2D), the day was budget-exhausted (then a zero row with `skip_reason` exists, so this class does not fire), or the content type is one the fund does not pay (`unknown`). |

Watch time carries the same rule as views: both sides are integer sums of the same rows.

## The query

`docs/runbooks/creator-fund-reconciliation.sql` — read-only; two result sets.

```bash
psql -d app -v ON_ERROR_STOP=1 -f docs/runbooks/creator-fund-reconciliation.sql
psql -d app -v ON_ERROR_STOP=1 -v since=2026-09-01 -f docs/runbooks/creator-fund-reconciliation.sql
```

The first result set counts rows per class:

| class | meaning | tolerance |
|---|---|---|
| `match` | views and watch time equal | — |
| `open_day_dashboard_ahead` | open day, dashboard ≥ statement | expected; catches up |
| `open_day_not_yet_accrued` | open day, no statement row yet | expected |
| `no_statement_row` | frozen day, dashboard views, no statement row | explain each (see above) |
| `statement_without_dashboard` | statement row with no dashboard rows | finding |
| `mismatch` | frozen day, both present, differ — or open day with the statement ahead | finding |

The second result set lists every frozen-day row that is not a `match`, with the creator's eligibility, for a human.

## Observed on the development database, 12 September 2026

Since 1 September:

| class | rows | dashboard views | statement views |
|---|---|---|---|
| match | 8 | 4,230 | 4,230 |
| no_statement_row | 3 | 218 | 0 |
| open_day_not_yet_accrued | 3 | 3 | 0 |

No `mismatch`, no `statement_without_dashboard`. The eight statement rows for 2026-09-07 equal the dashboard to the view and the millisecond.

The three `no_statement_row` rows are all 2026-09-07 and all explained, which also closes audit item **M-25** ("two hundred and one 2026-09-07 display views have no earnings row"):

- 200 `long_video` views: creator `2fe480e3…` is `ineligible` in `creator_fund_eligibility`.
- 1 `long_video` view: creator `97daf435…` has no eligibility row — never evaluated, so not eligible.
- 17 `flick` views (the M-21 fixture, relabelled from `reel` by migration 007): creator `ad4479d8…` has no eligibility row.

On the first run, all history added 20 more `no_statement_row` rows, on 2025-03-12, 2025-04-09, 2026-01-15 and 2026-01-16, totalling 157,000 dashboard views. These were the **M-01 fixture summary rows**: `content_daily_summary` rows with no `content_hourly_agg` beneath them. The January remediation reversed the 19 earnings that were priced from them and deleted the rate and band fixtures; it did not delete the summary rows themselves, and the rollup would not, because those days were frozen.

**Removed on the development database the same day, with the founder's authorisation**, by a `?force=1` rerun of each of the four days on `POST /v1/analytics/internal/aggregate` (analytics-service, build `c39c84e7`): the route answered 401 without the internal key and 409 `DAY_FROZEN` without `force`, then 200 for each day with `hours_rebuilt: 24, rolled_up: true, forced: true`, and the container logged the forced rewrite. Delete-then-insert over zero hourly rows, zero raw events and zero sessions ended with zero summary rows: the table went from 78 rows to 58, hourly rows were unchanged at 132, the four days are now recorded as `frozen` in `rollup_progress`, and the M-01 proof (summary rows with no hourly events anywhere) is 0. The reconciliation afterwards is the table above, exactly. Evidence and hashes are in the remediation manifest.

Staging and production: run `03_contamination_check_other_envs.sql` first; if the rows exist there, the same forced rerun removes them, and it must happen before anything accrues.

## When to run it

- Before every settlement run, for the period being settled, with `since` set to the period start: the first result set must show no `mismatch` and no `statement_without_dashboard`, and every `no_statement_row` must have one of the explanations above.
- After any `?force=1` rerun of a frozen day.
- After any correction adjustment.
