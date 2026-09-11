# Analytics daily rollup: the outage drill

The creator-fund release-acceptance line: **"a multi-day rollup outage backfills correctly, including zero-event dates and late events."** This is the drill that demonstrates it on a running stack, and its first run, on the development stack on 11 September 2026 with the founder's authorisation.

## What the rollup promises (plan 1E, audit M-10)

- `DailyRollup` runs a catch-up walk on start and every 15 minutes. It walks every day from the persisted watermark (`analytics.aggregation_settings`, key `daily_rollup_watermark`) to yesterday.
- A day inside the 48-hour reprocessing window is rolled up **delete-then-insert** inside one transaction, from `content_hourly_agg`. A day with no hourly rows ends with no summary rows; a hand-written row cannot survive.
- A day outside the window is **frozen**: marked so in `analytics.rollup_progress`, skipped, and logged with the forced route that is the only way to rewrite it. Settled money on a frozen day stands.
- The watermark then moves to the earliest day that is still open.

An outage of any length therefore leaves only two kinds of day: open days, which the next pass rewrites from their hourly rows, and frozen days, which the pass names and refuses.

## The drill

Simulating the clock is not possible on a running stack, so the drill produces the state an outage would leave and lets the real code recover from it. Development only; it writes to the live analytics tables, so it needs the same authorisation any dev write does. Nothing here touches raw events, sessions or hourly rows, which are the source of truth the rollup rebuilds from.

1. **Snapshot** `content_daily_summary` from the settled day onward, the hourly counts per day, `rollup_progress` and the watermark, to files, hashed.
2. **Stop** `analytics-service`. The outage begins.
3. **Leave the outage's footprint**: delete the summary rows of an open day (snapshot held), set that day's and the next open day's `rollup_progress` back to `open` with `completed_at` NULL, and move the watermark back far enough to cross the freeze boundary, so the walk meets both frozen and open days.
4. **Wait**, then **start** the service and read the first two `[DailyRollup]` lines.
5. **Assert**, against the snapshot:
   - the deleted open day is back, **identical on every measured column** (only `created_at`/`updated_at` may differ);
   - the frozen, settled day is **byte-identical including timestamps** (never touched);
   - the zero-event open day still has **no** summary row;
   - `rollup_progress` shows the frozen days as `frozen` and the open days completed;
   - the watermark sits on the earliest open day;
   - the dashboard-versus-statement reconciliation (`creator-fund-reconciliation.sql`) is unchanged.

## First run: development, 11 September 2026

Watermark before: `2026-09-09`. Open days: 09-09 (30 summary rows, 2 views, 58 hourly rows), 09-10 (no hourly rows, no summary rows: the zero-event day), 09-11 (today). Settled day: 09-07 (27 rows, 4,448 views; the eight statement rows reconcile against it).

| step | observed |
|---|---|
| stop | 20:44:09 UTC, build `c39c84e7` |
| footprint | `DELETE 30` (09-09 summary rows), `UPDATE 2` (09-09 and 09-10 reopened), watermark → `2026-09-07` |
| start | 20:47:55 UTC, after a 3 min 46 s outage |
| boot line | `[DailyRollup] started (cap 1, source=sessions, watermark=2026-09-07, window=48h)` |
| frozen days | `WARNING day 2026-09-07 is frozen and was never rolled up; … Rewrite with POST /v1/analytics/internal/aggregate?day=2026-09-07&force=1` and the same for 2026-09-08 |
| catch-up | `rolled_up=[2026-09-09 2026-09-10] frozen=[2026-09-07 2026-09-08] failed=0 watermark=2026-09-09` — 35 ms after the boot line |
| 09-09 | 30 rows back, identical to the snapshot on all 18 measured columns; `updated_at` 20:47:55 |
| 09-07 | 27 rows byte-identical, `updated_at` still 13:06:17 |
| 09-10 | no summary row (zero-event day) |
| 09-11 | untouched (today is not walked) |
| `rollup_progress` | 09-07 frozen, 09-08 frozen, 09-09 completed 20:47:55, 09-10 completed 20:47:55 |
| watermark after | `2026-09-09` |
| whole table | 58 rows before and after, same set, same numbers |
| reconciliation | 8 match / 0 mismatch / 3 explained unaccrued / 3 open-day, exactly as before |

Evidence: `rollup_outage_daily_before.csv` (sha256 `20bd0b15…`), `rollup_outage_daily_after.csv` (`15a18b06…`), `exec/rollup_outage_before.txt` (`ecd78c4e…`), `exec/rollup_outage_drill.log` (`3ced79e2…`), all in the remediation evidence folder and its manifest.

Late events are not part of this drill: a heartbeat or play end that arrives after its session was closed by inactivity applies its greatest-updates to the same session row and the next pass over that day recomputes the bucket (plan 1A). That path is pinned by the `backgrounded_then_resumed` playback fixture and by `TestLatePlayEndDoesNotCreateSecondView`; a live late event needs a signed-in viewer and is on the browser-test list.

## A finding the drill surfaced

**2026-09-08 has 45 hourly rows (6 display views) and no daily summary row, and it is frozen.** The service said so itself on the way back up: *frozen and was never rolled up*. The cause is the watermark seed: migration 008 and the first start under the new rules seed the watermark to today − 2 so that the first pass does not rewrite history, and the first start was on 09-11, so 09-08 (today − 3 at the time) was never walked and then froze. No money is affected today (no statement row exists for the day, and the affected creators have no accrual), but a creator's dashboard shows that day's views while the daily table does not, and nothing will ever fill it on its own.

The remedy is the one the log names: a forced rerun of 2026-09-08. That is a dev write outside this drill's authorisation and is left for a decision. The seed rule itself deserves a second look before staging: seeding at today − 2 assumes the previous rule had rolled up every day before that, which was exactly the M-10 bug.
