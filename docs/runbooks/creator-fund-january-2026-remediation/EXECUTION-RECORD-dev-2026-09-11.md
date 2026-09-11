# Phase 0 evidence snapshot — creator monetization

Plan: `C:\Users\RVReddy\.claude\plans\atomic-bubbling-moler.md`, Phase 0 items 1–3.
Taken against the running dev stack, read-only. No write was made to `app`, `commerce_db` or `identity_db`.
The only database write in the session was the guard-pass proof run of one integration test in the scratch DB `monetization_it_test`, which cleans up after itself.

## Environment stamps

| Item | Value |
|---|---|
| `SELECT now()` (session TZ `Etc/UTC`) | `2026-09-10 19:00:42.561397+00` |
| `SELECT version()` | `PostgreSQL 16.4 (Debian 16.4-1.pgdg110+2) on x86_64-pc-linux-gnu, compiled by gcc (Debian 10.2.1-6) 10.2.1 20210110, 64-bit` |
| Postgres container | `atpost_stack-postgres-1` (`postgis/postgis:16-3.4`, container `9426c7386e1e`), user `postgres`, db `app`, host port 5432 |
| monetization-service image | `sha256:4f48384a21d6e9c39caa111e411c49da0d48ec81e8c843c1f82155326d8d8bb7` (`atpost_stack-monetization-service`, container `eba1e701cd8e`, created 2026-09-07T17:27:08Z) |
| analytics-service image | `sha256:fdc7d182eddb25e18e7a46a9a836d76465cbd5e240b04cf6bc4f016fd8d42e79` (`atpost_stack-analytics-service`, container `5980a9444fcc`, created 2026-09-07T07:36:52Z) |
| Export command | `docker exec -i atpost_stack-postgres-1 psql -U postgres -d app -v ON_ERROR_STOP=1 --csv -q -c "<query>" > <file>` |
| Hash tool | PowerShell `Get-FileHash -Algorithm SHA256` |

## Files

Row counts exclude the CSV header line. Table totals in `app` at snapshot time: content_daily_summary 47, content_hourly_agg 131, creator_fund_earnings 27, creator_fund_period_settlements 18, monetization_rpm_rates 27, monetization_quality_bands 27, transactions 55, ledger_entries 57. Re-counted after all work: identical.

| File | Rows | Bytes | SHA256 | Query / contents |
|---|---|---|---|---|
| `content_daily_summary.csv` | 47 | 10680 | `e9702dbea98ec7e079f3689d8da703508338335c35ff477be6c506941ba06845` | `SELECT * FROM analytics.content_daily_summary ORDER BY day_bucket, content_id` (all rows) |
| `content_hourly_agg_affected_days.csv` | 28 | 8704 | `4e607558fa3efb7b24b55cf82ce661fc467900de7893eae27c8c283b2da15809` | `SELECT h.* FROM analytics.content_hourly_agg h WHERE (h.hour_bucket AT TIME ZONE 'UTC')::date IN (SELECT DISTINCT day_bucket FROM analytics.content_daily_summary) ORDER BY h.hour_bucket, h.content_id` — every hourly row on any day that has a daily row (28 of the 131 hourly rows) |
| `creator_fund_earnings.csv` | 27 | 7164 | `cc7b4dcf6e744955972c7baf13746282f5bc5172f05b46c5d78fee096e0ecd86` | `SELECT * FROM creator_fund_earnings ORDER BY day_bucket, creator_id, content_type, region_code` (all rows) |
| `creator_fund_period_settlements.csv` | 18 | 5202 | `6c0060eb97ce972b2ad4f6e4d02dd215145f9801c5db7052c272ded98af25ee3` | `SELECT * FROM creator_fund_period_settlements ORDER BY period_key, creator_id, region_code` (all rows) |
| `monetization_rpm_rates.csv` | 27 | 4322 | `63c534ae2836ccba79a52023162a2524953939ddfae5895a911d9a2b4f132af5` | `SELECT * FROM monetization_rpm_rates ORDER BY content_type, region_code, effective_from, created_at, id` (all rows; 25 tagged `integration test window`, 2 launch baseline) |
| `monetization_quality_bands.csv` | 27 | 4916 | `cadf0c0b67269a87063dad4cf499991e290a9ac95cfe412a7642c9158998f1ac` | `SELECT * FROM monetization_quality_bands ORDER BY content_type, region_code, effective_from, created_at, id` (all rows; 25 fixture, 2 launch baseline, both baseline rows open and enabled) |
| `transactions_fund_refs.csv` | 25 | 10486 | `53a6fe056caca4e7c9a072e1554fc9990437dcba67675daca41d29d2cc4d0b18` | `SELECT t.* FROM transactions t WHERE t.reference_id IN (SELECT id::text FROM creator_fund_earnings UNION SELECT id::text FROM creator_fund_period_settlements) ORDER BY t.created_at, t.id` (`transactions.reference_id` is text) |
| `ledger_entries_fund_refs.csv` | 48 | 13646 | `826fa2b07770e7194f655d1f51ff42f5850ede082bfdd3c0d7d48b1598eb2023` | `SELECT l.* FROM ledger_entries l WHERE l.reference_id IN (SELECT id FROM creator_fund_earnings UNION SELECT id FROM creator_fund_period_settlements) ORDER BY l.created_at, l.id` (`ledger_entries.reference_id` is uuid) |
| `proof_of_absence.csv` | **20** | 3844 | `722bb777f126b08c087ace36af173ef82c2c8e9a7ee6284255b7850755f853b9` | Daily rows with no hourly row for the same content and day (query below). Totals: **20 rows, views_display 157,000, plays 144,000** — matches the plan's expectation. |
| `proof_query_fixture_selection.csv` | **19** | 6873 | `7bbcf2d3df676ce7e9efd707091e904fd10ccde14ac1546fcbf93c8a47e4b908` | Non-reversed earnings whose day resolves to a fixture rate or band (query below). **Expected 0, got 19.** See "Unexpected". |
| `01_freeze_multiplier.sql` | – | 8151 | `76258b6b9bb4ceb587ed4ee581d73d1fbab63770e03316d19a4a7dbfd81bc86a` | Prepared, NOT executed. Effective-dated freeze of the two live bands plus verification selects as comments. |
| `02_delete_test_fixtures.sql` | – | – | – | **Deliberately not written**: the proof query is non-zero. |

### Proof-of-absence query

```sql
SELECT d.content_id, d.day_bucket, d.creator_id, d.content_type, d.impressions, d.plays, d.views_display,
       d.unique_viewers, d.watch_time_total_ms, d.created_at, d.updated_at
FROM analytics.content_daily_summary d
WHERE NOT EXISTS (
  SELECT 1 FROM analytics.content_hourly_agg h
  WHERE h.content_id = d.content_id
    AND (h.hour_bucket AT TIME ZONE 'UTC')::date = d.day_bucket)
ORDER BY d.day_bucket, d.content_id;
-- count(*)=20, sum(views_display)=157000, sum(plays)=144000
-- Session TZ is Etc/UTC, so the plain h.hour_bucket::date form gives the same 20.
```

### Proof query (fixture selection)

```sql
SET TIME ZONE 'UTC';
SELECT e.id, e.creator_id, e.day_bucket, e.content_type, e.region_code, e.status, e.rpm_paise,
       r.id AS rate_id, r.rpm_paise AS rate_rpm_paise, r.notes AS rate_notes,
       b.id AS band_id, b.notes AS band_notes
FROM creator_fund_earnings e
LEFT JOIN LATERAL (
  SELECT id, rpm_paise, notes FROM monetization_rpm_rates
  WHERE content_type = e.content_type AND region_code = e.region_code
    AND effective_from <= e.day_bucket AND (effective_to IS NULL OR effective_to > e.day_bucket)
  ORDER BY effective_from DESC, created_at DESC, id DESC LIMIT 1) r ON true
LEFT JOIN LATERAL (
  SELECT id, notes FROM monetization_quality_bands
  WHERE content_type = e.content_type AND region_code = e.region_code
    AND effective_from <= e.day_bucket AND (effective_to IS NULL OR effective_to > e.day_bucket)
  ORDER BY effective_from DESC, created_at DESC, id DESC LIMIT 1) b ON true
WHERE e.status <> 'reversed'
  AND (r.notes = 'integration test window' OR b.notes = 'integration test window')
ORDER BY e.day_bucket, e.creator_id;
```

Result: 19 rows. All are `status='settled'`, `credited=t`, `content_type='long_video'`, `region_code='IN'`, `rpm_paise=5000`; 14 on 2026-01-15 and 5 on 2026-01-16. Every one resolves to fixture rate `d830ba17-f3c9-4361-9a5a-68477230e5e1` (5000 paise, window 2026-01-09 → 2026-01-23) and fixture band `b45d00ea-c53d-4e2b-9d9a-5143d15008e0` (same window). The remaining 8 earnings rows (2026-09-07) resolve to the launch-baseline rate and band.

## Unexpected

1. **The fixture-selection proof is 19, not 0.** The 19 January earnings rows are integration-test accruals that were priced by the fixture rate/band rows and then credited into the live ledger (they are the M-01 contaminated set the plan's Phase 2A reverses). Deleting the 25+25 fixture rows now would leave 19 settled, credited earnings with no resolvable rate or band. Per instruction, `02_delete_test_fixtures.sql` was not written. The deletion belongs after Phase 2A step (2) reversal, exactly where the plan's January runbook already places it.
2. **Freeze boundary is today's UTC midnight, not `NOW()`.** `AccrueCreatorFundDay` passes UTC midnight of the day as `asOf`; with a `NOW()` boundary the `CURRENT_DATE` verification could not pick the new row on the day it is run, and the freeze day itself would still price at the old band. The prepared SQL snaps to `date_trunc('day', now() AT TIME ZONE 'UTC')` and refuses to run if any earnings row already exists on or after that boundary (none does; `max(day_bucket)=2026-09-07`).
3. `ledger_launch_integration_test.go` is in `internal/http`, not `internal/service`; and a fourth DSN reader, `creator_fund_band_selection_integration_test.go`, exists. All four are guarded; the guard is duplicated into `internal/http/testdb_integration_test.go` because `_test.go` helpers do not cross packages.

## Verification selects as they stand today (before the freeze)

A (`$day = CURRENT_DATE`) and B (`$day = '2026-09-07'`) both return, for the two non-fixture pairs:

```
content_type | region_code | id                                   | enabled | effective_from                | effective_to | notes
flick        | IN          | 95311712-b987-4b96-8c80-d2dbd44ef5a1 | t       | 2026-09-06 19:21:42.125331+00 |              | launch baseline: 0.85x-1.25x quality band, neutral at CQS 0.35
long_video   | IN          | 197c6843-27da-480d-9407-abad3b4eee93 | t       | 2026-09-06 19:21:42.125331+00 |              | launch baseline: 0.85x-1.25x quality band, neutral at CQS 0.35
```

After running `01_freeze_multiplier.sql`: A must return two new ids with `enabled=f`, notes `frozen 2026-09-11 pending analytics fixes (plan Phase 0.1)`; B must return the same two ids as above, still `enabled=t`, now with `effective_to` = the boundary.

## Freeze executed — 2026-09-11 (founder-authorised in chat)

`01_freeze_multiplier.sql` run against `atpost_stack-postgres-1` / `app`. Output: `UPDATE 2`, `INSERT 0 2`, `COMMIT`. Boundary `2026-09-10 00:00:00+00`.

| Check | Result |
|---|---|
| A: selection for CURRENT_DATE | flick → `5dd81d72-6e30-428c-87b0-4622863d5b9b` enabled=f; long_video → `13990c31-0fef-42be-968b-58cfb972c9ba` enabled=f; both `effective_from` = boundary, `notes` = frozen… |
| B: selection for 2026-09-07 | flick → `95311712-…` enabled=t; long_video → `197c6843-…` enabled=t; both now `effective_to` = boundary |
| `monetization_quality_bands` count | 27 → 29 |

Fixture deletion (`02_delete_test_fixtures.sql`) **not written and not run**: the proof query returned 19 non-reversed January earnings resolving to fixture rate `d830ba17-…` and fixture band `b45d00ea-…`. Deletion is blocked until those rows are reversed in Phase 2A; re-run the proof query afterwards and expect 0.

## Wave 1 cutover gate — reconciliation run 2026-09-11 (read-only)

`analytics-service/scripts/reconcile_view_source.sql` against dev `app`, after migrations 007–011 and the 009 backfill:

| buckets compared | money-diff buckets | display views old = new | play_ends old = new |
|---|---|---|---|
| 34 | **0** | 4,439 = 4,439 | 4,441 = 4,441 |

Zero-difference on all money columns. This satisfies the plan's gate for flipping `ANALYTICS_VIEW_SOURCE=sessions` **for backfilled hours**. Live hours created between deploy and flip must be re-run through the same query immediately before the flip.

## Reviewer memo follow-up — 2026-09-11 16:42–16:56 UTC (read-only against `app`; writes only to `monetization_it_test`)

Answers to memo questions 1 and 3, the rewritten January runbook, and the test-mode transfer exercise. Table totals in `app` re-counted after this pass: creator_fund_earnings 27, transactions 55, ledger_entries 57, monetization_rpm_rates 27, monetization_quality_bands 29, monetization_audit_log 0 — unchanged.

| File | Bytes | SHA256 | Contents |
|---|---|---|---|
| `02_january_remediation.md` | 35411 | `671a9a76bcc89baececd0c823c3890b0f61fce7d017370c7ea9590aedc36491f` | **Rewritten** per memo decision 1 (Option A, completion condition). Steps 0–7 with observed outputs. Contains the stop at step 4b: two of the 17 credited rows (`e51ecb3d…`, `00b74229…`) have a credit transaction and a `cf_net` leg but **no `cf_fee` leg**; the mechanism would post an unbacked `adj_fee` leg (20,293 paise). Backed fee reversal is 208,376 over 15 rows, not 228,669 over 17. Net reversal 533,576 over 17 stands. |
| `03_contamination_check_other_envs.sql` | 8968 | `104a546211f58c4db205943db45595c559c2879eb7593c309c3ef821f2610a5a` | Read-only staging/production check: proof-of-absence and fixture-selection queries from this manifest plus fixture/earnings/settlement counts. Header records that `MONETIZATION_WRITES_ENABLED` has been `"false"` in every deploy values file since the key existed (e79674ee, e022114b). |
| `03_contamination_check_dev_app_output.txt` | 23157 | `6489d0ac6fac055775f473b677b55a18fb46f72ea0baadd3da51170bdb27642b` | The same file run against dev `app` (proves it parses; reproduces the known signature: 25/25 fixtures, 19 proof rows, 20 absence rows, cf_net 17 vs cf_fee 15, 19 credited without settlement id). |
| `04_test_mode_transfer_exercise.md` | 29367 | `8deff76cadde25d2fccb84c56c1d6100a5efaec4b97c4411c82b8802c318556c` | The controlled RazorpayX Test-Mode transfer exercise: test creator `cc5699f1-9aeb-4b85-ba0e-0b0f9bdc113d` and what it still needs, budget rows, flags on/off, the four outcomes with provider-observed vs locally injected, reconciliation and duplicate checks. No secret values. |
| `dryrun_january_remediation.log` | 5262 | `6f6be5460496c00ac34180fb83c545eccb06e775ffc793fa9e0b0d6bb2288f33` | `go test -v` output of `TestJanuaryRemediationDryRun` against `monetization_it_test`: 19 look-alike rows with the live amounts through the real admin route; every expected output in 02 is quoted from here. |

Uncommitted test files in the working tree (not committed, per instruction): `Architecture/services/monetization-service/internal/service/creator_fund_reverse_uncredited_integration_test.go` (`TestReverseUncreditedEarningMovesNoMoney`, PASS) and `Architecture/services/monetization-service/internal/http/january_remediation_dryrun_integration_test.go` (`TestJanuaryRemediationDryRun`, PASS).

Container note: `/healthz` reports `build_sha 8d470281…` while HEAD is `c39c84e7`; the live `app` already carries migration 023 (added by 74c483fe, after 8d470281). The reversal code is byte-identical between the two shas; the runbook's step 0.1 rebuilds to HEAD before anything runs.

## Reviewer corrections A–E and the TDS decision — 2026-09-11 17:00–17:50 UTC (read-only against `app`; writes only to `monetization_it_test`)

Runbook rewritten per the reviewer's verdict (yes with changes, development only) and the founder's instruction on TDS. **Nothing was executed against `app`.** Table totals in `app` re-counted at the end: creator_fund_earnings 27, transactions 55, ledger_entries 57, monetization_audit_log 0, monetization_rpm_rates 27, monetization_quality_bands 29; account sums platform_revenue −956,800, platform_revenue_fees 272,827, user_wallet 655,973 — unchanged. `identity_db` was read once (`auth.users` by email; `auth.user_roles` by id) to find the operator id; `commerce_db` was not touched.

| File | Bytes | SHA256 | Contents |
|---|---|---|---|
| `02_january_remediation.md` | 49399 | `77ee6c971afadf31ab7efa9ff025ee8a49f127d415f0429826a56ddc084c4771` | **Rewritten.** A: dry run re-run on HEAD and quoted with hash; image pinned by id, no rebuild step. B: maintenance mode + advisory locks (keys computed with `settlementLockKey`). C: loopback port, internal key on admin routes, real operator id, header = identity claim for audit. D: step 6 is one transaction that snapshots, re-proves, deletes by approved ids, checks ROW_COUNT, then commits. E: steps 3/4a/4b by script. Accepted arithmetic verbatim; the three ending aggregates derived from today's balances; owners section. |
| `remediate_january.sh` | 14674 | `883d2c6c6c62d82b41e1d59ea198dd60255998e751c1d13cdf6dfd452f0690fd` | Steps 3/4a/4b: `set -euo pipefail`, embedded 19-row expectation table, per-response status+body assertions, `exec/<id>.json`, stop on first mismatch, `--resume`, EXIT trap restores maintenance off / payouts off and verifies 503. `bash -n` clean. **Not run.** |
| `06_delete_fixtures.sql` | 9633 | `62ceae7ae6dd0dd961d05ee1325f6bbcf7b0186194209984e3070bdaa0e85cfd` | Step 6 transaction, generated from the two id lists. **Not run.** |
| `approved_fixture_rate_ids.txt` | 925 | `d0d70578649f92f215e7acff7b8ca1a899d825f13d4de0e104527774708e8a97` | `SELECT id FROM monetization_rpm_rates WHERE notes='integration test window' ORDER BY id` — 25 ids |
| `approved_fixture_band_ids.txt` | 925 | `7163203448ba0d80d2b6bf30b6fa14e0807b14b9ce3ea966aca81228b0a1727b` | same for `monetization_quality_bands` — 25 ids |
| `dryrun_january_remediation_3eb2e8b8.log` | 5967 | `455edcda069a5fc179ba04c3d099ab98f00fe6cb5128111c67aae64d66bee994` | `TestJanuaryRemediationDryRun` on HEAD `3eb2e8b8`, `-p 1 -v`, against `monetization_it_test`: `adj_fee legs: 15 (sum 208376)`, 0 unbacked legs. Replaces `dryrun_january_remediation.log` (pre-guard rehearsal, kept for history). |
| `04_test_mode_transfer_exercise.md` | 31999 | `045896e405ec0dfe11ed981212113e04efc5962b065c0072a5f2db19bd8a4914` | Updated: the full ₹100 moves (`MONETIZATION_TDS_APPLY=false`); P1 verifies the pinned image; every direct curl carries `X-Internal-Service-Key`; `127.0.0.1`. |
| `integration_suite_after_changes.log` | 825 | `3d357dee18c6960b58f22eaef8fc6c4c1d667b829d83ecd5abd83ec503fc85b4` | `go test -tags integration -count=1 -p 1 ./...` after the code changes: every package `ok`. |
| `image_build_3eb2e8b8.log` | 2816 | `bf4c3c0bcf39ae17b83ef6227e50691efba4c498bd4da27a5a2361487546bc91` | `docker compose build --build-arg BUILD_SHA=3eb2e8b8e9ac6d2fe6870234496d25d589bfc167 monetization-service` |
| `image_build_worktree_diffstat.txt` | 837 | `cbd084131bbd300378534290dd387f299c02366cc07b73f960afe11fee2f5694` | `git diff --stat` at build time: the image carries these uncommitted changes on top of `3eb2e8b8`. |
| `approved_image_id.txt` | 231 | `02c3d83081e89d6f8ead597dfc58e6718b2973a559576ff7eae2e756d342d931` | `sha256:952389c99fc18804219d7ce636e8b39dc608400f2e67d2b58a40f4eb3106e2a0`, created 2026-09-11T17:41:33Z; RepoDigest is the local tag digest only. |
| `container_maintenance_boot_check.txt` | 1360 | `37ec9d7d033bcf796aebc2a3a1f1e43b4a123e01093c3afb1b2e7a512288e012` | The approved image booted once with `MONETIZATION_MAINTENANCE=true`: `workers=false kafka_producer=false`, 0 worker-start lines, 401 without key, 403 without scope, 503 MAINTENANCE on POST /payouts, 200 on the estimate read. No reversal sent. |
| `container_final_state.txt` | 1574 | `2dfae722c69d589658d1d89b9cd38efafed5dcc8f79d828e9671f496e6dd1ada` | Container returned to defaults: image `952389c9…`, all four flags false, `8099/tcp -> 127.0.0.1:8099`, boot line `maintenance=false … payouts_enabled=false tds_apply=false workers=false`, admin route 503 MONETIZATION_NOT_LAUNCHED. |

Code (uncommitted, per instruction; `git diff --stat` in `image_build_worktree_diffstat.txt`): `internal/runmode/` (new: `Resolve`, `BootLine`, tests), `cmd/server/main.go` (flags resolved through runmode; one boot line; no Kafka/workers in maintenance), `internal/http/handler.go` (`WithMaintenance`; boundary: admin routes need the key, non-admin writes 503 MAINTENANCE), `internal/http/maintenance_mode_test.go` (`TestMaintenanceModeStartsNoWorkersAndClosesNonAdminWrites`), `internal/service/{monetization,tax,payout_rail}.go` + `internal/store/postgres/tax.go` (`MONETIZATION_TDS_APPLY`: computed and recorded, not deducted; counter-entry mirrors the priced row), `internal/service/payout_tds_apply_integration_test.go` (`TestTDSNotAppliedWhenFlagOff`, `TestTDSAppliedWhenFlagOn`, `TestTDSCounterEntryMirrorsPricedRowWhenFlagOff`), `payout_gates_integration_test.go` (`TestTDSThresholdSumsGross` with the flag on), `Architecture/docker/docker-compose.yml` (`127.0.0.1:8099:8099`; `MONETIZATION_MAINTENANCE`, `MONETIZATION_TDS_APPLY`, `INTERNAL_SERVICE_KEY` on monetization), four `deploy/services/monetization-service/values-*.yaml` (both flags `"false"`).

Operator id: `7cd6ea3a-9c80-4f20-806f-5d08de0f914b` = `<operator-email>` (`identity_db`, active; no `auth.user_roles` row). No account exists under `<operator-email>`; the founder must confirm the id before use.

Copies in `C:\Users\RVReddy\Downloads\`: `02_january_remediation.md`, `remediate_january.sh`, `04_test_mode_transfer_exercise.md`, `06_delete_fixtures.sql`.

## Re-pinned after commit 23d1d09e (12 Sep)

Corrections A–E and MONETIZATION_TDS_APPLY committed as `23d1d09eb1ac270e96f69f4d8f05ac8cce3fe657`. Image rebuilt from that commit with default flags: `sha256:73af182c2fe610d4c0cf2c661e0c7f2546781944b9add88c2e01ab6ada991df0`. Runbook step 0.1 now names this image and commit; the earlier `952389c9…` image was built from the uncommitted tree and is superseded.

## Step 1 executed 2026-09-11T18:17:52 UTC — snapshot

`january_contaminated_earnings_before.csv`: 19 records, sha256 `af4a3143446715854d1bdeebe931527f0d9c2b76f473938e53e2a953b460e132`; classes both=15 credit_no_fee=2 fee_no_credit=0 neither=2, summary_rows_with_hourly=0 on all; `monetization_audit_log` operation `january_remediation_snapshot`: 19 rows, performer `7cd6ea3a-9c80-4f20-806f-5d08de0f914b`. (A first export was discarded: a leading SET line polluted the header; nothing was inserted from it.)

## Step 2 executed 2026-09-11T18:18:26 UTC — flag clear

Guarded UPDATE matched exactly 2 (`9517e3ab…`, `bd6fa147…`): credited=false, credited_at NULL, settlement_id NULL, status settled; audit `january_remediation_flag_clear` 2 rows; re-run UPDATE 0. Locks held (3), maintenance on.

## Step 3–4 first attempt 2026-09-11T18:22:27 UTC — false start, nothing sent

`remediate_january.sh` exited rc=1 after the image check: the container was already in maintenance mode from step 0, `docker compose up` reported "Running" without a recreate, and the boot-line grep over `--since 2m` found no fresh line, so `set -o pipefail` ended the run before the key check. Trap restored maintenance=false / payouts=false and observed 503. Verified before re-run: 0 `reverse` audit rows, 0 reversed earnings, 0 adjustment transactions, locks held. Re-run as-is (the recreate is now real).

## Steps 3–7 executed 2026-09-11T18:26:07 UTC — January remediation complete (development stack)

- Steps 3/4a/4b: `remediate_january.sh` second run rc=0; 19/19 responses matched the table; `complete: rows=19 (skipped on resume: 0) net_reversed=533576 fee_reversed=208376`; trap restored maintenance=false, payouts=false, 503 observed. Responses in `exec/*.json` (19 files; combined sha256 `8d2d057f33b3cc12bfbfcaac45b75357c1cd5e98c789f46637b704e00a8e85ed`); `exec/run.log` sha256 `bc7f56f9f079ea00bed1fa887848a9cee4ae41cbec8f524e7dc77aa611234ee6`.
- Post-script SQL: adjustment tx 17 / −533,576; net legs 17 / 533,576; fee legs 15 / 208,376; reconciled 15; unbacked fee legs 0; artefact tx/legs 0; negative or frozen 0; `reverse` audit rows by operator 19.
- Step 5 proof query: 0 rows (`exec/step5_proof.txt` sha256 `5717ce1a35d7f0b9559dc1d818f9bbc431916547bbdd901880b79823892e01f6`); non-reversed earnings 8.
- Step 6: `06_delete_fixtures.sql` (sha256 verified `62ceae7a…`) committed: NOTICE "deleted 25 rates and 25 bands"; tagged after 0/0; rates flick 1 / long_video 1; bands 2+2; audit 50 (`exec/step6_delete.txt` sha256 `7e0512ebc168eb3c38c4047d45489ca9b9c9cf2484bf1b352c93de4db0d5bf97`).
- Step 7: all 19 creators balance 0, not frozen, lifetime_earnings unchanged; accounts platform_revenue −214,848 / platform_revenue_fees 64,451 / user_wallet 122,397; audit fixture_delete 50 / flag_clear 2 / snapshot 19 / reverse 19; container image `73af182c…` with maintenance=false writes=false payouts=false, boot line workers=false; admin reverse with key+scope answers 503; advisory locks released (0). After-CSV `january_contaminated_earnings_after.csv` sha256 `aeb7a1203c94861d95c6d9522c251bf4bcd8adfc6372d9488fab9aa1e1d3dc62` (19 rows, all reversed); before-CSV unchanged `af4a3143…`. `exec/step7_postcheck.txt` sha256 `a9d34d46cc34e054f5ce4618a4fdb4503e2d5aec9be0252d622e5b86568015c2`.
- Operator `7cd6ea3a-9c80-4f20-806f-5d08de0f914b`. Staging/production: not touched, not authorised.
