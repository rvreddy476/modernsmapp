# January remediation runbook — creator fund (plan Phase 2A; reviewer memo decision 1, Option A)

Rewritten 12 September 2026 after the reviewer's verdict (**yes with changes, development only**): the five corrections A–E below are applied, plus the founder's decision on TDS. **Not executed.** Every step is the founder's to run after review. Every expected output was **observed** in the dry run this runbook is checked against (`dryrun_january_remediation_3eb2e8b8.log`, run on HEAD `3eb2e8b8`; the reversal path is byte-identical in `23d1d09e`, which adds only the run mode, the port binding and the tax flag, sha256 `455edcda069a5fc179ba04c3d099ab98f00fe6cb5128111c67aae64d66bee994`; quoted in full at the end), not predicted.

Target: the dev stack's live `app` database (`atpost_stack-postgres-1`) and the monetization container (`atpost_stack-monetization-service-1`, now bound to `127.0.0.1:8099` only).

## The numbers (reviewer-accepted arithmetic, verbatim)

15 rows net 486,224 / fee 208,376; 2 mixed net 47,352 / fee 0; 2 artefacts 0 / 0; totals net 533,576 (₹5,335.76) over 17, fee 208,376 (₹2,083.76) over 15.

Expected ending aggregates, derived read-only from today's balances (`SELECT account_type, sum(balance_paise) FROM accounts GROUP BY 1`, 2026-09-11 17:29 UTC: `platform_revenue` −956,800, `platform_revenue_fees` 272,827, `user_wallet` 655,973):

| account | today | movement | ending |
|---|---|---|---|
| `platform_revenue` | −956,800 | + 533,576 (17 net reversals, `user_wallet` → `platform_revenue`) + 208,376 (15 fee reversals, `platform_revenue_fees` → `platform_revenue`) = + 741,952 | **−214,848** |
| `platform_revenue_fees` | 272,827 | − 208,376 (15 fee reversals out) | **64,451** |
| `user_wallet` (sum over creators) | 655,973 | − 533,576 (17 net reversals out) | **122,397** |

Cross-check: 272,827 = 208,376 (the 15 January `cf_fee` legs) + 64,451 (September period fees); 655,973 = 533,576 (17 January `cf_net` legs) + 122,397 (September period credits).

Invariant at the end: **no creator balance becomes negative and no new freeze occurs; `platform_revenue` is already negative and stays so.**

## Read this first: what the 19 rows are

The memo expected 17 rows with a credit transaction **and** a fee leg, and 2 with neither. The database says otherwise (read-only join on `transactions.reference_id` = earning id and on the settlement's per-row idempotency keys `cf_net:<id>:long_video` / `cf_fee:<id>:long_video`; `settlement_id` is NULL on all 19):

| class | rows | net_paise | fee reversed | what exists |
|---|---|---|---|---|
| credit transaction + `cf_net` leg + `cf_fee` leg | **15** | 486,224 | 208,376 | everything |
| credit transaction + `cf_net` leg, **no `cf_fee` leg** | **2** | 47,352 | **0** | `e51ecb3d-53e5-42b0-96f5-d750c5e551c4` (29,852) and `00b74229-63af-4c41-8a6b-4bc3b848abc6` (17,500); both credited 2026-09-06 19:42:00 UTC, before the fee leg was written. `platform_revenue_fees` never received their fee, so nothing is taken back out of it |
| nothing (credited=true from migration 017 only) | **2** | 47,352 | 0 | `9517e3ab-f5c4-49d3-b25f-b560a10c4015`, `bd6fa147-55a8-4559-9bb8-b8c57fdc63ad`; net reversed 0 |

`ReverseFundEarning` posts the fee leg only where an original `cf_fee` (or period `cfp_fee`) leg exists and reports `fee_leg_absent` otherwise — commit `3eb2e8b8` (M-31), proven by `TestReverseCreditedEarningWithoutFeeLegReversesNetOnly` and observed in the dry run below (`adj_fee legs: 15 (sum 208376)`, zero unbacked legs).

Full per-row table, as queried on 2026-09-11 (`credited=t`, `status='settled'`, `settlement_id=NULL` on all 19; no `adj:`/`adj_fee:` key exists yet for any of them). The last three columns are what the script in step 3–4 asserts per id:

| earning id | day | net | fee col. | credit txn | `cf_net` | `cf_fee` | expect net_reversed | expect fee_reversed | expect fee_leg_absent |
|---|---|---|---|---|---|---|---|---|---|
| cb506055-60e1-44ad-afba-e269bcbbc549 | 01-15 | 35,000 | 15,000 | t | t | t | 35,000 | 15,000 | false |
| 37feeea7-fb77-4b9c-b20f-30f35280b83c | 01-15 | 29,852 | 12,793 | t | t | t | 29,852 | 12,793 | false |
| f875006e-ea59-4630-b55f-0b84be3c48c4 | 01-15 | 29,852 | 12,793 | t | t | t | 29,852 | 12,793 | false |
| **e51ecb3d-53e5-42b0-96f5-d750c5e551c4** | 01-15 | 29,852 | 12,793 | t | t | **f** | 29,852 | **0** | **true** |
| 1a595a21-7aa4-470a-85c6-274fd655913c | 01-15 | 43,579 | 18,676 | t | t | t | 43,579 | 18,676 | false |
| 05c25f5c-4852-4dbe-bc55-12c4981c2ab4 | 01-15 | 35,000 | 15,000 | t | t | t | 35,000 | 15,000 | false |
| **9517e3ab-f5c4-49d3-b25f-b560a10c4015** | 01-15 | 29,852 | 12,793 | **f** | **f** | **f** | **0** | **0** | false |
| 0e8b1cbc-376e-4c81-b5de-56d047d6d00f | 01-15 | 35,000 | 15,000 | t | t | t | 35,000 | 15,000 | false |
| f1cc0a32-af03-4b80-913a-4c1c12ffcc43 | 01-15 | 43,579 | 18,676 | t | t | t | 43,579 | 18,676 | false |
| e407b673-4bcf-41cc-b29e-b27ce3eaf270 | 01-15 | 29,852 | 12,793 | t | t | t | 29,852 | 12,793 | false |
| df3f1a4f-1541-4ca2-9df0-0df902839a17 | 01-15 | 35,000 | 15,000 | t | t | t | 35,000 | 15,000 | false |
| ec3efbf5-60a1-4d5a-bc66-ec86b292d5fa | 01-15 | 43,579 | 18,676 | t | t | t | 43,579 | 18,676 | false |
| 15cad66a-50a5-4548-b26e-4f6816e1989d | 01-15 | 29,852 | 12,793 | t | t | t | 29,852 | 12,793 | false |
| 14b8d184-9f81-4d44-adb6-2dfafc32c6a1 | 01-15 | 43,579 | 18,676 | t | t | t | 43,579 | 18,676 | false |
| 920501ec-577b-439b-98ba-9c0e81481fc8 | 01-16 | 17,500 | 7,500 | t | t | t | 17,500 | 7,500 | false |
| **00b74229-63af-4c41-8a6b-4bc3b848abc6** | 01-16 | 17,500 | 7,500 | t | t | **f** | 17,500 | **0** | **true** |
| 8ebee831-d4d3-495b-b1a6-ba78dd183d67 | 01-16 | 17,500 | 7,500 | t | t | t | 17,500 | 7,500 | false |
| 2e4232a3-3ea8-48e9-928c-5fd9beeaf4fe | 01-16 | 17,500 | 7,500 | t | t | t | 17,500 | 7,500 | false |
| **bd6fa147-55a8-4559-9bb8-b8c57fdc63ad** | 01-16 | 17,500 | 7,500 | **f** | **f** | **f** | **0** | **0** | false |

(The artefact rows report `fee_leg_absent=false` because the mechanism never reaches the fee check on an uncredited row; it is a status-only change.)

## What the mechanism does (question 1, answered from the code and proven)

`ReverseFundEarning` (`internal/service/creator_fund_adjustments.go`) in one transaction: locks the row; if already `reversed` returns it; otherwise, **only if `credited && net_paise > 0`**, posts the negative adjustment (`transactions` keyed `adj:creator_fund_earning_reversal:<id>`, wallet `balance -= net`, one `ledger_entries` leg user_wallet → platform_revenue keyed the same) and, **only if an original fee leg exists**, one fee leg platform_revenue_fees → platform_revenue keyed `adj_fee:…`; freezes the ledger if the balance went negative. Then `MarkCreatorFundEarningReversedTx` sets `status='reversed', reversed_at, reversal_reason, reversal_transaction_id`.

For a row with `credited=false`: **status change only.** No adjustment, no wallet movement, no leg, no freeze; `reversal_transaction_id` stays NULL; the period claim (`ClaimAndCreditPeriodAccruals`) filters on `status='settled' AND credited=false`, so the row can never be paid later. Proven by `TestReverseUncreditedEarningMovesNoMoney` (`internal/service/creator_fund_reverse_uncredited_integration_test.go`).

The route `POST /v1/monetization/admin/creator-fund/earnings/:id/reverse` (`internal/http/creator_fund_handler.go`) wraps it: 403 without `X-Scopes: admin|superadmin`, 401 without a UUID `X-User-Id`, 400 without a reason, 404 for an unknown id; writes one `monetization_audit_log` row (`operation='reverse'`, `performer_id` = the `X-User-Id`) per first-time reversal. Idempotent: a repeat answers `already_reversed:true` and moves nothing (observed on all 19 in the dry run). In maintenance mode (step 0.2) the route additionally answers **401 without `X-Internal-Service-Key`**, whatever the scope header says.

## Step 0 — preconditions

### 0.1 The approved image (correction A)

There is **no rebuild instruction in this runbook.** The approved image was built once, on 12 September 2026, with

```bash
cd /c/workspace/modernsmapp/Architecture/docker
docker compose build --build-arg BUILD_SHA=23d1d09eb1ac270e96f69f4d8f05ac8cce3fe657 monetization-service
```

and recorded as:

| | |
|---|---|
| image id (`docker image inspect --format '{{.Id}}' atpost_stack-monetization-service`) | `sha256:73af182c2fe610d4c0cf2c661e0c7f2546781944b9add88c2e01ab6ada991df0` |
| RepoDigest | `atpost_stack-monetization-service@sha256:73af182c2fe610d4c0cf2c661e0c7f2546781944b9add88c2e01ab6ada991df0` (local tag digest only; the image was never pushed to a registry) |
| built from | commit `23d1d09eb1ac270e96f69f4d8f05ac8cce3fe657` with default flags, no uncommitted changes; the earlier `952389c9…` image built from an uncommitted tree is superseded. Re-pinned 12 Sep after the commit; the reviewer verifies this row before execution. |

Verify, do not build:

```bash
docker inspect atpost_stack-monetization-service-1 --format '{{.Image}}'   # must equal the image id above
curl -sS http://127.0.0.1:8099/healthz                                     # {"build_sha":"23d1d09eb1ac270e96f69f4d8f05ac8cce3fe657","status":"alive"}
docker port atpost_stack-monetization-service-1                            # 8099/tcp -> 127.0.0.1:8099 only (correction C)
export EXPECTED_IMAGE_ID="$(docker inspect atpost_stack-monetization-service-1 --format '{{.Image}}')"   # the script refuses any other image
```

```sql
SELECT filename FROM schema_migrations WHERE service='monetization-service' AND filename >= '018' ORDER BY 1;
-- 018_ledger_types_and_adjustments.sql … 023_accrual_build_sha.sql (six rows)
SELECT column_name, data_type FROM information_schema.columns
WHERE table_name='creator_ledger' AND column_name IN ('balance','lifetime_earnings','pending_payout');
-- all three: bigint
```

### 0.2 Maintenance mode, not plain writes (correction B)

Enabling `MONETIZATION_WRITES_ENABLED` starts the accrual, settlement and eligibility workers (`workers.StartAll`) and opens the Kafka producer; after step 2 the two artefact rows are settled-uncredited and **claimable by a January settlement**. Pre-checks on what the workers would find, and running between the 02:00/03:00 ticks, are **not** mutual exclusion — they are a bet on timing. The mutual exclusion is:

1. **`MONETIZATION_MAINTENANCE=true`** (`internal/runmode`, `cmd/server/main.go`, `internal/http/handler.go`): writes are enabled for the `/v1/monetization/admin/*` routes only; **no worker and no Kafka client starts**; every non-admin financial write answers `503 MAINTENANCE`; the beta reads stay open; every admin route requires `X-Internal-Service-Key` in addition to the scope header. The process refuses to boot in this mode with `MONETIZATION_PAYOUTS_ENABLED=true` or with `INTERNAL_SERVICE_KEY` empty. Proven by `TestMaintenanceModeStartsNoWorkersAndClosesNonAdminWrites` (`internal/http/maintenance_mode_test.go`) and `internal/runmode/runmode_test.go`.
2. **The advisory locks** in 0.3, held for the whole run.

The script in step 3–4 flips maintenance on itself (a recreate, not a rebuild) and its exit trap flips it off; do it by hand here once so the boot log is seen and recorded:

```bash
cd /c/workspace/modernsmapp/Architecture/docker
MONETIZATION_MAINTENANCE=true MONETIZATION_WRITES_ENABLED=false MONETIZATION_PAYOUTS_ENABLED=false docker compose up -d --no-deps monetization-service
docker logs --since 2m atpost_stack-monetization-service-1 2>&1 | grep -E "monetization run mode|MAINTENANCE MODE|zero workers|TDS at payout"
# {"level":"WARN","msg":"monetization run mode: maintenance=true writes_enabled=false payouts_enabled=false tds_apply=false workers=false kafka_producer=false redis=false admin_key_required=true",…}
# {"level":"WARN","msg":"MAINTENANCE MODE: admin routes only (internal key + admin scope); every other financial write answers 503 MAINTENANCE; no worker and no Kafka client started",…}
# {"level":"WARN","msg":"TDS at payout: NOT APPLIED — computed and recorded in tds_ledger, transfers pay the gross (founder decision 12 Sep 2026; deduction is the later tax module)",…}
# {"level":"WARN","msg":"maintenance mode: zero workers started, no Kafka producer opened",…}
docker logs --since 2m atpost_stack-monetization-service-1 2>&1 | grep -cE "starting monetization background workers|worker started"   # 0
docker inspect atpost_stack-monetization-service-1 --format '{{.Image}}'   # unchanged: the approved image id
```

If the count is not 0, or the `monetization run mode` line says `workers=true`, **stop**: the container is not in maintenance mode.

Observed on 2026-09-11 17:46 UTC on the pre-commit image `952389c9…` (code identical to the committed image for this check; re-observe on `73af182c…` at step 0.2 before proceeding) (`container_maintenance_boot_check.txt`, then returned to the defaults, `container_final_state.txt`): exactly the four lines above, `worker-start lines: 0`, `/healthz` `23d1d09e…`, admin route **401** without the key, **403** with the key and no scope, `POST /payouts` **503 `MAINTENANCE`**, `GET /creator-fund/earnings` 200. No reversal was sent.

### 0.3 Hold the settlement period locks for the duration (correction B)

The settlement worker guards a period with `pg_try_advisory_lock(settlementLockKey(period))` (`internal/store/postgres/creator_fund_period.go` `TryAdvisorySettlementLock`; key from `internal/workers/creator_fund.go` `settlementLockKey`: `int64(fnv64a("creator_fund_settlement:" + periodKey) >> 1)`). Hold the same keys for `2026-01` (monthly cadence, the compose default) and `2026-01-H1` / `2026-01-H2` (semimonthly) so **any** instance that tries to settle January while this runs — including one started by mistake — skips. Keys, computed with the same function:

| period key | advisory lock key |
|---|---|
| `2026-01` | `8351032510781926477` |
| `2026-01-H1` | `2144265680879544926` |
| `2026-01-H2` | `2144264031612102610` |

Open a **dedicated psql session in its own terminal** and leave it open until step 7 (advisory locks are session-scoped; closing the session releases them):

```bash
docker exec -it atpost_stack-postgres-1 psql -U postgres -d app
```

```sql
SELECT pg_advisory_lock(8351032510781926477);
SELECT pg_advisory_lock(2144265680879544926);
SELECT pg_advisory_lock(2144264031612102610);
SELECT objid, pid, granted FROM pg_locks WHERE locktype='advisory' ORDER BY objid;
-- three rows, granted=t, your pid
```

(`objid` shows the low 32 bits of the key in this view; `classid` the high 32; match on your `pid`.) With maintenance mode on there is no settlement worker to contend with; the lock is what makes that true even if the mode is lost.

### 0.4 Operator identity and access (correction C)

Access to the admin routes is gated by two things: the container's port is bound to **loopback** (`127.0.0.1:8099:8099` in `docker-compose.yml`; other containers still reach it by service name), and in maintenance mode every admin route requires **`X-Internal-Service-Key` = `INTERNAL_SERVICE_KEY`**. `INTERNAL_SERVICE_KEY` is set for `monetization-service` in `docker-compose.yml` (`${INTERNAL_SERVICE_KEY:-…}`, resolved from `Architecture/docker/.env`, which carries it; the gateway stamps the header on every proxied request and the direct callers — commerce, post, food — send it, so setting it on this container breaks nothing).

Because `INTERNAL_SERVICE_KEY` is now set on the container, **every** direct call to `127.0.0.1:8099` — reads included — needs `X-Internal-Service-Key` (observed: `GET /creator-ledger` answers 401 without it, 200 with it); in maintenance mode the admin routes check it a second time inside the boundary, so the global middleware being removed later would not open them.

`X-Scopes` and `X-User-Id` are **an operator identity claim recorded for audit, not authentication**: `hasAdminScope` trusts `X-Scopes` because the gateway strips and re-stamps it, and on the direct port you stamp it yourself. `X-User-Id` becomes `performer_id` on every audit row the route writes, and is written as `operator_user_id` into every SQL audit insert below.

Your user id, read-only from `identity_db` (2026-09-11; **confirm it is you** — no account exists under `<operator-email>`):

```bash
docker exec -i atpost_stack-postgres-1 psql -U postgres -d identity_db -Atc \
  "SELECT user_id, email, account_status FROM auth.users WHERE email = '<operator-email>'"
# 7cd6ea3a-9c80-4f20-806f-5d08de0f914b|<operator-email>|active
```

Environment for the rest of the runbook (the key is exported from `.env` into the shell and never written to a file under this folder):

```bash
export PSQL="docker exec -i atpost_stack-postgres-1 psql -U postgres -d app -v ON_ERROR_STOP=1"
export MON="http://127.0.0.1:8099/v1/monetization"
export ADMIN_ID="7cd6ea3a-9c80-4f20-806f-5d08de0f914b"     # your own user_id, from the query above
export $(grep -E '^INTERNAL_SERVICE_KEY=' /c/workspace/modernsmapp/Architecture/docker/.env)
export EVID="/c/Users/RVReddy/AppData/Local/Temp/claude/C--workspace-modernsmapp/cc2d1a2e-6cf4-4651-be21-c011fbd1cfa1/scratchpad/phase0-evidence"
export COMPOSE_DIR="/c/workspace/modernsmapp/Architecture/docker"
# Boundary checks while in maintenance mode (nothing is reversed by these: 401/403 stop before the handler):
curl -sS -o /dev/null -w "%{http_code}\n" -X POST "$MON/admin/creator-fund/earnings/9517e3ab-f5c4-49d3-b25f-b560a10c4015/reverse" \
  -H "Content-Type: application/json" -H "X-Scopes: admin" -H "X-User-Id: $ADMIN_ID" -d '{"reason":"x"}'                          # 401 (no key)
curl -sS -o /dev/null -w "%{http_code}\n" -X POST "$MON/admin/creator-fund/earnings/9517e3ab-f5c4-49d3-b25f-b560a10c4015/reverse" \
  -H "Content-Type: application/json" -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -H "X-User-Id: $ADMIN_ID" -d '{"reason":"x"}'  # 403 (no scope)
curl -sS -o /dev/null -w "%{http_code}\n" -X POST "$MON/payouts" -H "Content-Type: application/json" -H "X-User-Id: $ADMIN_ID" -d '{}'  # 503 (MAINTENANCE)
```

Sanity on what the workers *would* have found, still worth recording before step 1 (these are evidence, not exclusion):

```sql
SET TIME ZONE 'UTC';
SELECT count(*) AS uncredited_settled FROM creator_fund_earnings WHERE status='settled' AND credited=false;      -- 0
SELECT count(*) FROM creator_fund_budgets;                                                                        -- 0
```

## Step 1 — fresh evidence snapshot (before anything moves)

One row per January earning with its transactions, its ledger legs (by reference and by the `cf_net`/`cf_fee` keys), the summary rows, the proof of absence, and the ledger balance; to a CSV and into `monetization_audit_log`.

```bash
$PSQL --csv -c "SET TIME ZONE 'UTC';" -c "
SELECT e.id AS earning_id, e.creator_id, e.day_bucket, e.content_type, e.region_code,
       e.view_count, e.rpm_paise, e.gross_paise, e.platform_fee_paise, e.net_paise,
       e.status, e.credited, e.credited_at, e.settlement_id, e.settled_at,
       EXISTS (SELECT 1 FROM transactions t WHERE t.reference_id = e.id::text AND t.type='creator_fund_earning' AND t.status='completed' AND t.amount = e.net_paise) AS has_credit_txn,
       EXISTS (SELECT 1 FROM ledger_entries l WHERE l.idempotency_key = 'cf_net:'||e.id::text||':'||e.content_type AND l.amount_paise = e.net_paise) AS has_net_leg,
       EXISTS (SELECT 1 FROM ledger_entries l WHERE l.idempotency_key = 'cf_fee:'||e.id::text||':'||e.content_type AND l.amount_paise = e.platform_fee_paise) AS has_fee_leg,
       (SELECT json_agg(t ORDER BY t.created_at) FROM transactions t WHERE t.reference_id = e.id::text) AS transactions,
       (SELECT json_agg(l ORDER BY l.created_at) FROM ledger_entries l WHERE l.reference_id = e.id)     AS ledger_legs,
       (SELECT json_agg(d) FROM analytics.content_daily_summary d
         WHERE d.creator_id = e.creator_id AND d.day_bucket = e.day_bucket AND d.content_type = e.content_type) AS summary_rows,
       (SELECT count(*) FROM analytics.content_daily_summary d
         WHERE d.creator_id = e.creator_id AND d.day_bucket = e.day_bucket AND d.content_type = e.content_type
           AND EXISTS (SELECT 1 FROM analytics.content_hourly_agg h
                       WHERE h.content_id = d.content_id AND (h.hour_bucket AT TIME ZONE 'UTC')::date = d.day_bucket)) AS summary_rows_with_hourly,
       l.balance AS ledger_balance_before, l.is_frozen AS ledger_frozen_before
FROM creator_fund_earnings e
LEFT JOIN creator_ledger l ON l.user_id = e.creator_id
WHERE e.day_bucket BETWEEN '2026-01-15' AND '2026-01-16' AND e.status <> 'reversed'
ORDER BY e.day_bucket, e.creator_id" > "$EVID/january_contaminated_earnings_before.csv"
wc -l "$EVID/january_contaminated_earnings_before.csv"        # 20 (19 rows + header)
sha256sum "$EVID/january_contaminated_earnings_before.csv"     # record in MANIFEST.md
```

Expected in the CSV: 19 rows; `summary_rows_with_hourly = 0` on every row; `has_credit_txn`/`has_net_leg`/`has_fee_leg` exactly as in the table above (15 t/t/t, 2 t/t/f, 2 f/f/f). If any row differs from the table, **stop**: the evidence has moved since 11 September.

```sql
SET TIME ZONE 'UTC';
INSERT INTO monetization_audit_log (table_name, operation, old_data, new_data, performer_id)
SELECT 'creator_fund_earnings', 'january_remediation_snapshot',
       to_jsonb(e) || jsonb_build_object(
         'operator_user_id', '<ADMIN_ID>',
         'has_credit_txn', EXISTS (SELECT 1 FROM transactions t WHERE t.reference_id = e.id::text AND t.type='creator_fund_earning' AND t.status='completed'),
         'has_fee_leg',    EXISTS (SELECT 1 FROM ledger_entries l WHERE l.idempotency_key = 'cf_fee:'||e.id::text||':'||e.content_type),
         'transactions', (SELECT json_agg(t) FROM transactions t WHERE t.reference_id = e.id::text),
         'ledger_legs',  (SELECT json_agg(l) FROM ledger_entries l WHERE l.reference_id = e.id),
         'summary_rows', (SELECT json_agg(d) FROM analytics.content_daily_summary d
                           WHERE d.creator_id = e.creator_id AND d.day_bucket = e.day_bucket AND d.content_type = e.content_type),
         'summary_rows_with_hourly', (SELECT count(*) FROM analytics.content_daily_summary d
                           WHERE d.creator_id = e.creator_id AND d.day_bucket = e.day_bucket AND d.content_type = e.content_type
                             AND EXISTS (SELECT 1 FROM analytics.content_hourly_agg h
                                         WHERE h.content_id = d.content_id AND (h.hour_bucket AT TIME ZONE 'UTC')::date = d.day_bucket)),
         'ledger_balance_before', (SELECT balance FROM creator_ledger WHERE user_id = e.creator_id)),
       NULL, '<ADMIN_ID>'::uuid
FROM creator_fund_earnings e
WHERE e.day_bucket BETWEEN '2026-01-15' AND '2026-01-16' AND e.status <> 'reversed';
-- INSERT 0 19                                        (dry run: INSERT 0 19)
SELECT count(*) FROM monetization_audit_log WHERE operation = 'january_remediation_snapshot';   -- 19 (table is empty today)
```

## Step 2 — evidence-guarded flag clear for the two artefact rows

Clears `credited` only where the ledger's own record says nothing was ever posted: no transaction by reference, no leg by reference, no leg by either settlement key, and a zero balance. The `DO` block raises (and so rolls back) unless exactly 2 rows changed; the audit insert commits with it.

```sql
SET TIME ZONE 'UTC';
BEGIN;
DO $$
DECLARE n integer;
BEGIN
  UPDATE creator_fund_earnings e
  SET credited = FALSE, credited_at = NULL, settlement_id = NULL
  WHERE e.id IN ('9517e3ab-f5c4-49d3-b25f-b560a10c4015', 'bd6fa147-55a8-4559-9bb8-b8c57fdc63ad')
    AND e.status = 'settled' AND e.credited = TRUE
    AND NOT EXISTS (SELECT 1 FROM transactions t WHERE t.reference_id = e.id::text)
    AND NOT EXISTS (SELECT 1 FROM ledger_entries l WHERE l.reference_id = e.id)
    AND NOT EXISTS (SELECT 1 FROM ledger_entries l WHERE l.idempotency_key IN ('cf_net:'||e.id::text||':'||e.content_type, 'cf_fee:'||e.id::text||':'||e.content_type))
    AND COALESCE((SELECT balance FROM creator_ledger cl WHERE cl.user_id = e.creator_id), 0) = 0;
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n <> 2 THEN
    RAISE EXCEPTION 'january flag clear touched % rows, expected exactly 2; rolled back', n;
  END IF;
END $$;
INSERT INTO monetization_audit_log (table_name, operation, old_data, new_data, performer_id)
SELECT 'creator_fund_earnings', 'january_remediation_flag_clear',
       jsonb_build_object('credited', true, 'operator_user_id', '<ADMIN_ID>', 'note', 'set by migration 017 blanket UPDATE; no credit transaction, no ledger leg, balance 0'),
       to_jsonb(e), '<ADMIN_ID>'::uuid
FROM creator_fund_earnings e
WHERE e.id IN ('9517e3ab-f5c4-49d3-b25f-b560a10c4015', 'bd6fa147-55a8-4559-9bb8-b8c57fdc63ad') AND e.credited = FALSE;
COMMIT;
-- DO / INSERT 0 2 / COMMIT                            (dry run: UPDATE 2, audit rows 2, uncredited now 2)
SELECT id, credited, credited_at, settlement_id, status FROM creator_fund_earnings
WHERE id IN ('9517e3ab-f5c4-49d3-b25f-b560a10c4015', 'bd6fa147-55a8-4559-9bb8-b8c57fdc63ad');
-- both: credited=f, credited_at NULL, settlement_id NULL, status settled
```

Guard evidence from the dry run: the same UPDATE aimed at the two **mixed** rows returned `UPDATE 0` (they have a transaction and a `cf_net` leg), and a re-run on the artefact ids returned `UPDATE 0` (idempotent). From here until the two artefact rows are reversed in step 3 they are settled-uncredited: this is the window maintenance mode and the locks exist for.

## Steps 3, 4a, 4b — the 19 reversals, by script (correction E)

The curl loops are replaced by **`remediate_january.sh`** (this folder; sha256 in `MANIFEST.md`). It is not executed here. What it does:

- `set -euo pipefail`; refuses to start unless `ADMIN_ID`, `INTERNAL_SERVICE_KEY` and `EXPECTED_IMAGE_ID` are exported and the running container's image equals `EXPECTED_IMAGE_ID`.
- Puts the container into maintenance mode (recreate), reads the boot line, and stops unless it says `maintenance=true payouts_enabled=false workers=false kafka_producer=false` and no worker-start line appears; then proves the key is enforced (401 without key, 403 without scope) before any reversal.
- Checks its embedded table (the 19 ids with `net_reversed_paise`, `fee_reversed_paise`, `fee_leg_absent` per id, exactly the last three columns of the table above) sums to 19 / 533,576 / 208,376.
- Calls the route once per id in order — step 3 (2 artefacts), 4a (15 clean), 4b (2 mixed) — with `X-Internal-Service-Key`, `X-Scopes: admin`, `X-User-Id: $ADMIN_ID`; writes each response to `$EVID/exec/<id>.json` **before** checking it; checks HTTP 200, `earning.status="reversed"`, `already_reversed=false`, the three expected fields, `ledger_frozen=false`, `balance_after_paise=0`; **stops at the first mismatch** (exit 5; the response file names it).
- `--resume` skips any id whose `exec/<id>.json` already shows `"status":"reversed"` and tolerates `already_reversed=true` on the rest (the first pass got through and the file was lost).
- `trap … EXIT`: on **any** exit it recreates the container with `MONETIZATION_MAINTENANCE=false MONETIZATION_PAYOUTS_ENABLED=false`, confirms both from `docker inspect`, waits for `/healthz`, and verifies the admin route answers 503 again; everything it does is logged to `$EVID/exec/run.log`.

Run it (from the shell that has the exports of step 0.4):

```bash
bash "$EVID/remediate_january.sh"            # first pass
bash "$EVID/remediate_january.sh" --resume   # only if the first pass stopped and the cause is understood
```

Expected log shape (per id, observed in the dry run on look-alikes):

```
step 3  9517e3ab-…: ok net=0 fee=0 fee_leg_absent=false balance_after=0 frozen=false
step 4a cb506055-…: ok net=35000 fee=15000 fee_leg_absent=false balance_after=0 frozen=false
step 4b e51ecb3d-…: ok net=29852 fee=0 fee_leg_absent=true balance_after=0 frozen=false
complete: rows=19 (skipped on resume: 0) net_reversed=533576 fee_reversed=208376
EXIT (rc=0): restoring MONETIZATION_MAINTENANCE=false, MONETIZATION_PAYOUTS_ENABLED=false
admin route answers 503 again: boundary closed
```

Response shapes (dry run): artefact row — `"status":"reversed"`, `"credited":false`, no `reversal_transaction_id`, `"money_moved":false`, no `adjustment`, `net_reversed_paise:0`, `fee_reversed_paise:0`; clean row — `"credited":true`, `reversal_transaction_id` = `adjustment.id`, `adjustment.amount_paise` = −net, `adjustment.reference_type="creator_fund_earning_reversal"`, `fee_leg_absent:false`; mixed row — as clean but `fee_reversed_paise:0`, `fee_leg_absent:true`, and a container WARN `creator-fund reversal: no original platform-fee leg for this earning; net reversed, fee NOT reversed`.

Verification after the script (observed totals in the dry run: `net_reversed=486224 fee_reversed=208376` over the 15; `net_reversed=47352 fee_reversed=0` over the 2 mixed; reconciliation `15 of 15`):

```sql
SET TIME ZONE 'UTC';
SELECT count(*), sum(amount) FROM transactions WHERE type='adjustment' AND reference_type='creator_fund_earning_reversal';
-- 17 | -533576
SELECT count(*), sum(amount_paise) FROM ledger_entries WHERE idempotency_key LIKE 'adj:creator_fund_earning_reversal:%';
-- 17 | 533576   (user_wallet -> platform_revenue)
SELECT count(*), sum(amount_paise) FROM ledger_entries WHERE idempotency_key LIKE 'adj_fee:creator_fund_earning_reversal:%';
-- 15 | 208376   (platform_revenue_fees -> platform_revenue)
-- one-for-one against the originals on the 15 clean rows
SELECT count(*) AS reconciled_rows FROM creator_fund_earnings e
JOIN transactions orig ON orig.reference_id = e.id::text AND orig.type='creator_fund_earning' AND orig.status='completed'
JOIN transactions adj  ON adj.idempotency_key = 'adj:creator_fund_earning_reversal:'||e.id::text AND adj.amount = -orig.amount
JOIN ledger_entries cf  ON cf.idempotency_key  = 'cf_fee:'||e.id::text||':'||e.content_type
JOIN ledger_entries af  ON af.idempotency_key  = 'adj_fee:creator_fund_earning_reversal:'||e.id::text AND af.amount_paise = cf.amount_paise
WHERE e.day_bucket BETWEEN '2026-01-15' AND '2026-01-16' AND e.status='reversed' AND e.credited;
-- 15
-- nothing reversed without an original posting
SELECT count(*) AS adj_fee_without_original FROM ledger_entries af
WHERE af.idempotency_key LIKE 'adj_fee:creator_fund_earning_reversal:%'
  AND NOT EXISTS (SELECT 1 FROM ledger_entries cf WHERE cf.idempotency_key = 'cf_fee:'||af.reference_id::text||':long_video');
-- 0
SELECT count(*) AS unbacked_fee_legs_on_mixed FROM ledger_entries
WHERE idempotency_key LIKE 'adj_fee:%' AND reference_id IN ('e51ecb3d-53e5-42b0-96f5-d750c5e551c4','00b74229-63af-4c41-8a6b-4bc3b848abc6');
-- 0
SELECT count(*) FROM transactions  WHERE reference_id IN ('9517e3ab-f5c4-49d3-b25f-b560a10c4015','bd6fa147-55a8-4559-9bb8-b8c57fdc63ad');   -- 0
SELECT count(*) FROM ledger_entries WHERE reference_id IN ('9517e3ab-f5c4-49d3-b25f-b560a10c4015','bd6fa147-55a8-4559-9bb8-b8c57fdc63ad'); -- 0
SELECT user_id, balance, is_frozen FROM creator_ledger WHERE balance < 0 OR is_frozen;  -- (0 rows): no creator balance negative, no new freeze
SELECT status, credited, count(*), sum(net_paise) FROM creator_fund_earnings
WHERE day_bucket BETWEEN '2026-01-15' AND '2026-01-16' GROUP BY 1,2 ORDER BY 1,2;
-- reversed | f | 2  |  47352
-- reversed | t | 17 | 533576
SELECT count(*) FROM monetization_audit_log WHERE operation='reverse' AND performer_id='<ADMIN_ID>';  -- 19
```

## Step 5 — the proof query must return 0 rows

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
-- (0 rows)   <-- required.  Dry run: "step 5 proof query: 0 rows"
SELECT count(*) FROM creator_fund_earnings WHERE status <> 'reversed';   -- 8 (the 2026-09-07 rows, plus anything accrued since)
```

## Step 6 — delete the 25 + 25 fixture rows: one transaction that aborts before commit (correction D)

The file **`06_delete_fixtures.sql`** (this folder, sha256 `62ceae7ae6dd0dd961d05ee1325f6bbcf7b0186194209984e3070bdaa0e85cfd`; generated from the id lists below, not typed) does, inside one `BEGIN … COMMIT`:

1. snapshots the exact 25 rate ids and 25 band ids **with their full rows** into a temp table and into `monetization_audit_log` (`operation='january_remediation_fixture_delete'`, `operator_user_id` in the payload, `performer_id` = you);
2. in a `DO` block: `RAISE EXCEPTION` unless the lists carry 25 + 25, the snapshot holds 25 + 25, every approved id is still tagged `integration test window`, and no tagged row exists outside the lists; **re-runs the step-5 proof query and raises if it returns any row**; deletes **only** `WHERE id = ANY(<approved ids>)`; checks `ROW_COUNT` = 25 after each `DELETE` and raises otherwise;
3. only then the post-checks and `COMMIT`. Any raise aborts the transaction; with `ON_ERROR_STOP=1` psql exits and nothing is committed.

```bash
$PSQL -v operator="'$ADMIN_ID'" -f - < "$EVID/06_delete_fixtures.sql"
# … NOTICE:  deleted 25 rates and 25 bands; committing
# rates_tagged_after 0 | bands_tagged_after 0 | flick 1, long_video 1 | flick f 1 / t 1, long_video f 1 / t 1 | audit_rows 50 | COMMIT
```

Dry run (scratch pair): `rates before=1 DELETE 1 after=0; bands before=1 DELETE 1 after=0`. Re-run the step-5 query afterwards: still 0 rows.

Approved id lists, from a read-only query on 2026-09-11 (`SELECT id FROM <table> WHERE notes='integration test window' ORDER BY id`; each list re-verified read-only on 12 Sep: 25 of 25 still tagged):

| list | file | rows | sha256 |
|---|---|---|---|
| rates | `approved_fixture_rate_ids.txt` | 25 | `d0d70578649f92f215e7acff7b8ca1a899d825f13d4de0e104527774708e8a97` |
| bands | `approved_fixture_band_ids.txt` | 25 | `7163203448ba0d80d2b6bf30b6fa14e0807b14b9ce3ea966aca81228b0a1727b` |

`monetization_rpm_rates` (all `long_video`/`IN`/5000 paise, windows 2025-03, 2025-04, 2026-01-08 and 2026-01-09):

```
06713908-d1f1-4546-8bb0-86ce06fe6e8a
08e0f639-5bd0-4fb6-b847-a98b73935580
16afe41a-258a-4ef8-8918-ab2427f4e8dc
24903d2c-f625-429c-896c-fe1507ddc47a
255e661a-863f-4fbf-80db-14bee2f5f180
2734cb41-1b36-4e6b-9d5f-92c95be55ae8
391e07d4-b8bb-4813-9c34-4a7765db5ff0
39df5353-2d36-47da-aa55-f0f39a9bf2e2
4b2f426c-dfba-48d6-ab3b-c44533500247
4f026b3b-2689-4e3d-8502-73d03e34bd01
58d98c7a-0087-4076-bf8e-e6c6817fd2a2
64b6f43e-9af0-4c4c-9e51-acbd6959432c
66c6c7bc-6e45-4074-af5f-8c580f52705e
6acc6e72-3e25-464b-bef0-6891711d5dc5
6b93a6d7-d1b5-44c4-bb0a-114a8fa4d6ed
71981bf6-4283-4c6e-bf8a-04e59374dc0e
93417e65-3c1e-41d0-b5a9-6ac31fa64bd2
a695de00-6503-4e6e-92ba-389c90cccd2f
bf3e2a34-1e46-4967-824a-7dd36b4c6db2
d830ba17-f3c9-4361-9a5a-68477230e5e1
d8b4237a-a506-4816-be8f-7b832cb9e0a4
e3648495-43f4-4ba7-898e-687e4686cde3
e3833da5-68eb-4730-9697-edfcc9045098
e4d4c88e-a5f6-4ce9-aefc-793a37909c75
ec0a00ed-626e-47a5-a5bb-b81c1dab2e29
```

`monetization_quality_bands` (all `long_video`/`IN`, same four windows):

```
193a4ec8-4606-4daf-bff1-e4e176556110
1d7fb677-d18d-4af9-8266-3ba8faf594e3
377eb409-c1f4-42bc-b6ec-360fb30858cb
5676b3e1-b0da-4fdc-a467-f3512187596d
5813d808-630a-4001-a6cc-c0a59623e28d
6b16f654-5cfb-4efd-b5dc-741b6afc45ce
6c3e4d0f-685b-44fd-96a4-ba4703e84adf
70b65401-2032-4be5-9823-dbaec437c1b3
800fbf78-d87e-418c-ab73-88c8fca207bf
834281e5-7406-469c-83b5-40aeebc26f31
8c19763b-1dda-4817-8cea-56ea669f579c
910e4481-292d-4e89-ba27-235077c33a41
9549506c-6691-41c0-8d0a-2132376d9eff
9ee1f994-e33e-4e34-9c08-62ac3cce93c2
a633bbb2-4951-4513-a2f3-45dfb1d7e952
b31de189-e608-4961-b7a7-d9e1da444fc9
b45d00ea-c53d-4e2b-9d9a-5143d15008e0
b45db851-8ae0-484d-b7e1-8eb3acd54f3c
bb16cd23-c74e-4c44-ab26-0ff4b288960e
c652731c-97f9-48d0-922b-d6235e3c2a92
ceb7619b-4f3b-4729-b9de-da1725fc8881
d2aedf07-0635-4fd6-b102-0e0a3910930c
d6716c84-9cb4-4f77-bcbe-b7dae7e46fec
e013080e-16b2-4eec-bf46-b79e5e6c4b7e
f97e6fe3-ade0-4263-816e-9c7a1f57f5c2
```

Untouched by design: rates `6098f29c-…` (flick baseline), `c1f4ce3c-…` (long_video baseline); bands `95311712-…`, `197c6843-…` (closed baselines) and `5dd81d72-…`, `13990c31-…` (frozen successors).

## Step 7 — post-check, release the locks, confirm the boundary is closed

```sql
SET TIME ZONE 'UTC';
-- the 19 creators: 17 went net -> 0, 2 were already 0; none frozen
SELECT user_id, balance, lifetime_earnings, is_frozen FROM creator_ledger
WHERE user_id IN (SELECT creator_id FROM creator_fund_earnings WHERE day_bucket BETWEEN '2026-01-15' AND '2026-01-16')
ORDER BY user_id;
-- balance 0 on all 19; lifetime_earnings unchanged (the plan moves balance only); is_frozen f on all
SELECT count(*) FROM creator_ledger WHERE is_frozen;                                   -- 0   (no new freeze)
SELECT count(*) FROM creator_ledger WHERE balance < 0;                                 -- 0   (no creator balance negative)
SELECT account_type, sum(balance_paise) FROM accounts GROUP BY 1 ORDER BY 1;
-- platform_revenue      -214848     (= -956800 + 533576 + 208376; already negative, stays so)
-- platform_revenue_fees   64451     (= 272827 - 208376)
-- user_wallet            122397     (= 655973 - 533576)
SELECT operation, count(*) FROM monetization_audit_log GROUP BY 1 ORDER BY 1;
-- january_remediation_fixture_delete 50 | january_remediation_flag_clear 2 | january_remediation_snapshot 19 | reverse 19
-- idempotency: re-issuing any reverse call answers already_reversed:true and moves nothing (dry run: 0 of 19 moved on the second pass)
```

Dry-run observation for this step: `creators with balance<>0: 0; frozen: 0; reversed rows: 19; audit 'reverse' rows by admin: 19; adjustment txns: 17 (sum -533576); adj_fee legs: 15 (sum 208376)`.

Release the advisory locks — in the psql session from 0.3:

```sql
SELECT pg_advisory_unlock_all();
SELECT count(*) FROM pg_locks WHERE locktype='advisory';   -- 0
\q
```

The script's trap already closed the boundary; confirm it, and that the container is still the approved image:

```bash
docker inspect atpost_stack-monetization-service-1 --format '{{.Image}} {{range .Config.Env}}{{println .}}{{end}}' | grep -E "sha256|MONETIZATION_(MAINTENANCE|WRITES_ENABLED|PAYOUTS_ENABLED)"
# sha256:<approved id>  MONETIZATION_WRITES_ENABLED=false  MONETIZATION_PAYOUTS_ENABLED=false  MONETIZATION_MAINTENANCE=false
docker logs --since 5m atpost_stack-monetization-service-1 2>&1 | grep "monetization run mode" | tail -1
# … maintenance=false writes_enabled=false payouts_enabled=false tds_apply=false workers=false …
curl -sS -o /dev/null -w "%{http_code}\n" -X POST "$MON/admin/creator-fund/earnings/cb506055-60e1-44ad-afba-e269bcbbc549/reverse" \
  -H "Content-Type: application/json" -H "X-Scopes: admin" -H "X-User-Id: $ADMIN_ID" -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -d '{"reason":"x"}'
# 503  (MONETIZATION_NOT_LAUNCHED)
```

Then export the after-state next to the before-state and hash everything:

```bash
$PSQL --csv -c "SET TIME ZONE 'UTC';" -c "SELECT e.*, (SELECT balance FROM creator_ledger WHERE user_id=e.creator_id) AS ledger_balance_after
FROM creator_fund_earnings e WHERE e.day_bucket BETWEEN '2026-01-15' AND '2026-01-16' ORDER BY e.day_bucket, e.creator_id" > "$EVID/january_contaminated_earnings_after.csv"
sha256sum "$EVID/january_contaminated_earnings_before.csv" "$EVID/january_contaminated_earnings_after.csv" "$EVID"/exec/*.json "$EVID/exec/run.log"
unset INTERNAL_SERVICE_KEY
```

## The dry run this runbook is checked against (correction A)

`TestJanuaryRemediationDryRun` (`internal/http/january_remediation_dryrun_integration_test.go`, committed in `23d1d09e`) seeds the 19 look-alikes with the live amounts and classes, builds the real router with writes on, and runs steps 1–7 through `POST /v1/monetization/admin/creator-fund/earnings/:id/reverse`. Run on HEAD `23d1d09eb1ac270e96f69f4d8f05ac8cce3fe657` on 2026-09-11 with `-p 1 -v`; full output `dryrun_january_remediation_3eb2e8b8.log`, 5967 bytes, sha256 `455edcda069a5fc179ba04c3d099ab98f00fe6cb5128111c67aae64d66bee994`. Run it yourself:

```bash
cd /c/workspace/modernsmapp/Architecture/services/monetization-service
MONETIZATION_POSTGRES_DSN="postgres://postgres:postgres@127.0.0.1:5432/monetization_it_test?sslmode=disable" \
  go test -tags integration -count=1 -p 1 -v -run 'TestJanuaryRemediationDryRun$' ./internal/http/
```

Observed (verbatim; the step-1 line's `fee_with_credit=228669` is the **sum of the `platform_fee_paise` column over the 17 credited rows** — an evidence figure the seed is checked against, not an amount anything reverses; the amount reversed is `fee_with_leg=208376`):

```
=== RUN   TestJanuaryRemediationDryRun
    january_remediation_dryrun_integration_test.go:254: OBSERVED precondition: POST reverse without X-Scopes -> 403 {"error":{"code":"FORBIDDEN","message":"Admin access required"},"meta":{}}
    january_remediation_dryrun_integration_test.go:271: OBSERVED step 1 evidence: both=15 credit_no_fee=2 fee_no_credit=0 neither=2 net_with_credit=533576 fee_with_leg=208376 fee_with_credit=228669
    january_remediation_dryrun_integration_test.go:288: OBSERVED step 1 audit snapshot: INSERT 0 19
    january_remediation_dryrun_integration_test.go:308: OBSERVED step 2 guard on the two mixed rows: UPDATE 0 (must be 0)
    january_remediation_dryrun_integration_test.go:336: OBSERVED step 2 flag clear: UPDATE 2, audit rows 2, uncredited now 2
    january_remediation_dryrun_integration_test.go:341: OBSERVED step 2 re-run: UPDATE 0 (idempotent)
    january_remediation_dryrun_integration_test.go:350: OBSERVED step 3 response (artefact row): {"data":{"earning":{"id":"33a94b8d-c7bb-40f0-a915-a168709768eb","creator_id":"9470eb7b-8a07-4937-9f9e-1cc50a2d0689","day_bucket":"2026-01-15T00:00:00Z","content_type":"long_video","region_code":"IN","view_count":8529,"watch_time_ms":255870000,"rpm_paise":5000,"gross_paise":42645,"platform_fee_paise":12793,"net_paise":29852,"status":"reversed","settled_at":"2026-09-07T01:13:58+05:30","base_gross_paise":42645,"quality_cqs":0,"quality_effective_cqs":0,"quality_impressions":0,"quality_multiplier_bps":10000,"credited":false,"reversed_at":"2026-09-11T23:02:10.7876778+05:30","reversal_reason":"M-01: migration-017 artefact; no credit transaction and no ledger leg exist; reversed per plan Phase 2A","rule_version":"cf-1","gross_micro_paise":0,"carry_in_micro_paise":0,"carry_out_micro_paise":0},"already_reversed":false,"money_moved":false,"balance_after_paise":0,"net_reversed_paise":0,"fee_reversed_paise":0,"fee_leg_absent":false,"ledger_frozen":false}}
    january_remediation_dryrun_integration_test.go:353: OBSERVED step 3: adjustment transactions for artefact rows=0, ledger legs referencing them=0, statuses=reversed,reversed
    january_remediation_dryrun_integration_test.go:382: OBSERVED step 4a response (clean row): {"data":{"earning":{"id":"008f1674-782d-43f6-b8d9-8a34471bf1de","creator_id":"b96928b1-8382-408d-86f5-95c6c93099f8","day_bucket":"2026-01-15T00:00:00Z","content_type":"long_video","region_code":"IN","view_count":10000,"watch_time_ms":300000000,"rpm_paise":5000,"gross_paise":50000,"platform_fee_paise":15000,"net_paise":35000,"status":"reversed","settled_at":"2026-09-07T01:13:58+05:30","base_gross_paise":50000,"quality_cqs":0,"quality_effective_cqs":0,"quality_impressions":0,"quality_multiplier_bps":10000,"credited":true,"credited_at":"2026-09-07T01:13:58+05:30","reversed_at":"2026-09-11T23:02:10.8124873+05:30","reversal_reason":"M-01: January 2026 accrual priced from analytics rows with no hourly events (integration-test fixture); reversed per plan Phase 2A","reversal_transaction_id":"522215cb-7643-47e7-83f5-7260b5c61859","rule_version":"cf-1","gross_micro_paise":0,"carry_in_micro_paise":0,"carry_out_micro_paise":0},"already_reversed":false,"money_moved":true,"adjustment":{"id":"522215cb-7643-47e7-83f5-7260b5c61859","wallet_id":"b96928b1-8382-408d-86f5-95c6c93099f8","type":"adjustment","amount_paise":-35000,"currency":"INR","status":"completed","reference_type":"creator_fund_earning_reversal","reference_id":"008f1674-782d-43f6-b8d9-8a34471bf1de","description":"Reversal of creator fund earning 008f1674-782d-43f6-b8d9-8a34471bf1de (2026-01-15 long_video, 10000 views): M-01: January 2026 accrual priced from analytics rows with no hourly events (integration-test fixture); reversed per plan Phase 2A","created_at":"2026-09-11T23:02:10.8289108+05:30"},"balance_after_paise":0,"net_reversed_paise":35000,"fee_reversed_paise":15000,"fee_leg_absent":false,"ledger_frozen":false}}
    january_remediation_dryrun_integration_test.go:383: OBSERVED step 4a totals over 15 clean rows: net_reversed=486224 fee_reversed=208376
    january_remediation_dryrun_integration_test.go:396: OBSERVED step 4a reconciliation: 15 of 15 rows have adjustment = -credit AND adj_fee = cf_fee
2026/09/11 23:02:11 WARN creator-fund reversal: no original platform-fee leg for this earning; net reversed, fee NOT reversed earning_id=55931d54-158f-4dd5-a0a7-eb85dfab4fdf content_type=long_video platform_fee_paise=12793 settlement_id=<nil> expected_key=cf_fee:55931d54-158f-4dd5-a0a7-eb85dfab4fdf:long_video
2026/09/11 23:02:11 WARN creator-fund reversal: no original platform-fee leg for this earning; net reversed, fee NOT reversed earning_id=4bf90fd5-d1e1-4971-9c57-3bd792707283 content_type=long_video platform_fee_paise=7500 settlement_id=<nil> expected_key=cf_fee:4bf90fd5-d1e1-4971-9c57-3bd792707283:long_video
    january_remediation_dryrun_integration_test.go:422: OBSERVED step 4b (mixed rows, scratch only): net_reversed=47352 fee_reversed=0 adj_fee legs with NO original cf_fee leg=0 (sum 0)
    january_remediation_dryrun_integration_test.go:428: OBSERVED step 4 totals if all 17 credited rows go through the mechanism: net=533576 fee=208376 (the 2 mixed rows' 20,293 paise of platform_fee_paise is NOT reversed: no original posting)
    january_remediation_dryrun_integration_test.go:446: OBSERVED step 5 proof query: 0 rows (must be 0)
    january_remediation_dryrun_integration_test.go:463: OBSERVED step 6: rates before=1 DELETE 1 after=0; bands before=1 DELETE 1 after=0
    january_remediation_dryrun_integration_test.go:468: OBSERVED step 7: creators with balance<>0: 0; frozen: 0; reversed rows: 19; audit 'reverse' rows by admin: 19; adjustment txns: 17 (sum -533576); adj_fee legs: 15 (sum 208376)
    january_remediation_dryrun_integration_test.go:486: OBSERVED re-run of all 19: rows that moved money or were not already_reversed = 0
--- PASS: TestJanuaryRemediationDryRun (0.65s)
PASS
ok  	github.com/atpost/monetization-service/internal/http	0.835s
```

## Owners and what is not yet given

- **Budget approval** is the founder's, subject to company spending authority. **Not yet given.** Nothing in this runbook spends; the fund cap (`creator_fund_budgets`) stays empty.
- **Tax-counsel sign-off** requires a nominated Indian tax adviser. **Not yet named.** The section (`MONETIZATION_TDS_SECTION`, default 194-O) and the applicability of 194-O to a creator fund versus 194J for tips and subscriptions remain open questions for that adviser.
- **TDS at payout is not applied** until the later tax module, on the founder's instruction (12 Sep 2026): "leave that tax part; just transfer what the amount is; keep that calculation ready; we'll deduct later per the user's tax eligibility via government APIs, as the last module." Implemented as `MONETIZATION_TDS_APPLY` (default `false`): every payout still computes its TDS and writes the `tds_ledger` row with the gross and the computed amount, but the request carries `tds_paise 0` and `net_paise = gross`, and the transfer is the gross. `TestTDSNotAppliedWhenFlagOff`, `TestTDSAppliedWhenFlagOn`, `TestTDSCounterEntryMirrorsPricedRowWhenFlagOff`. This runbook moves no payout; the note is here because the boot line records the mode.

## What this runbook does not do

- It does not touch `content_daily_summary`; the 20 summary rows with no hourly rows stay as evidence for Phase 1E's rollup.
- It does not reduce `lifetime_earnings`.
- It does not build an image. The approved image id in 0.1 is verified, never rebuilt, and the runbook is void if the running image differs.
- It does not touch `commerce_db` or `identity_db`, and reads `identity_db` only to find your own user id.
- It does not rely on schedule avoidance or pre-checks for mutual exclusion: maintenance mode and the advisory locks are the exclusion; the pre-checks are evidence.
