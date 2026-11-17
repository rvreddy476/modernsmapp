# Commerce: Track E, reliability, observability and deployment

- **Audit date:** 2026-11-17. Read-only.
- **Tree:** `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` @ `be57a46bf` (the map was written at `d83616ad6`; the five commits between touch e2e, android lint and media-worker only). Committed code (HEAD) only. Modified files were read with `git show HEAD:<path>`. commerce-service has no uncommitted edits. payments-service has uncommitted edits in `internal/http/admin_*`, `internal/service/refund_required.go` and `internal/store/postgres/{admin,refund_attention,refund_required}.go`; those were read at HEAD.
- **Web tree:** `C:\workspace\wt\pb-echo` @ `1b596b4` (read for the degraded-state copy only).
- **Shorthand** (same as `00-map.md`): `C/` = `Architecture/services/commerce-service/`, `PAY/` = `Architecture/services/payments-service/`, `P/` = `C/internal/store/postgres/`, `S/` = `C/internal/service/`, `H/` = `C/internal/http/`, `SH/` = `Architecture/shared/`, `CH/` = `charts/atpost-service/`, `DV/` = `deploy/services/` (always read at HEAD).
- **Evidence tags:** VERIFIED-RUN (I ran it), VERIFIED-READ (I read the code or config at the cited lines), SUSPECTED (inferred, not proven), UNVERIFIED (not checked).

## Counts

| Severity | Count | IDs |
|---|---|---|
| P0 | 0 | |
| P1 | 4 | COM-E-1, COM-E-2, COM-E-3, COM-E-4 |
| P2 | 10 | COM-E-5 to COM-E-14 |
| P3 | 5 | COM-E-15 to COM-E-19 |

Platform items already recorded for feed or reels are in the **Inherited** table at the end. They are re-checked there and not counted here.

## Answers to the map's open questions

| Map Q | Answer | Finding |
|---|---|---|
| 1 (gated migrations) | No deployed environment can have commerce 998-1002 or payments 998-999 applied by the deployment. The migrator is not in the image and the Job is off. There is also a defect: the gated files run in lexical order, so `1000_*` runs before `998_*`. While the PII cutover is `dual` (every values file), `commerce-migrate -gated` therefore stops at 1000 and applies nothing. | COM-E-1 |
| 3 (no seller payout) | Not a Track E question. Track E adds only that no worker, metric or alert exists for payouts, COD remittances or earnings. | Notes |
| 4 (KYC stub) | Confirmed: `kyc.StubValidator{}` is wired unconditionally (`C/cmd/server/main.go:358`). There is no boot check, metric or environment switch, so the same stub runs in production. | Notes (Track B/C) |
| 6 (refund-required worker, duplicate-capture consumer) | Both are OFF in prod, qa, staging and Azure, because their config default is ON only for `ENV` blank, `dev` or `local`. Late captures on a failed intent, duplicate captures and mismatched captures are recorded in `payments.refund_required` and never refunded. Nothing counts or alerts on that table. commerce's own late-capture path (capture on an expired or cancelled order) does refund, through `order_refund_commands`. | COM-E-2 |
| 7 (webhook replay) | Owned by Track B. Track E adds that Razorpay has no replay window (`ErrReplayWindowExpired` exists for Cashfree only, `PAY/internal/gateway/cashfree_provider.go:162`), and that no webhook counter (received, rejected, duplicate) exists. | COM-E-3 |

## Per-environment facts

### What schema a fresh deploy ends up with

| Env | commerce | payments | Gated set |
|---|---|---|---|
| dev (compose / devbox) | `setup.sql` and `migrations/001-055` at boot (`C/cmd/server/main.go:112`) | `setup.sql` and `001-019` at boot (`PAY/cmd/server/main.go:78`) | Not by any tool. CI applies commerce 998 by `psql` to the scratch DB `commerce_p0_test` only (`.github/workflows/integration-commerce.yml:356-359`). The devbox database was not inspected (UNVERIFIED). |
| qa | same as dev (boot) | same | none: `migrationJob.enabled: false` (`DV/commerce-service/values-qa.yaml:171-185`), no `/commerce-migrate` in the image (`C/Dockerfile:20`) |
| prod | same (boot) | same | none (`DV/commerce-service/values-prod.yaml:165-182`) |
| staging | **no schema**: commerce refuses to boot (COM-E-12) | boots and migrates (ENV `staging` is not prod, so it boots with Razorpay test credentials) | none |
| azure-prod / azure-staging | no schema: no `POSTGRES_DSN` | no schema: no DSN and no gateway credentials | none |

**Missing from every deployed database** (VERIFIED-READ):
- commerce 998 (`C/database/gated/998_contract_triggers_and_fences.sql`):
  - `trg_order_transition` (:74), the DB guard on `orders.status`. `P/order_fulfilment.go:3-9` says that without it "nothing guards `UPDATE orders SET status`".
  - `orders_payment_method_prepaid_only` (:120).
  - `trg_fence_returns` (:135), the generated `trg_fence_<table>` triggers on 12 fenced tables (:155) and `trg_fence_reviews` (:162).
- commerce 999 (`999_validate_and_tighten.sql`):
  - `VALIDATE` of five NOT VALID constraints (:61-65) and of `product_variants_status_check` (:121). On an empty production database this changes nothing, because NOT VALID constraints already apply to new writes.
  - `SET NOT NULL` on `orders.{final_amount,subtotal,tax_amount}_minor` and `order_items.{final_price,net_inclusive}_minor` (:78-84).
- commerce 1000, 1001, 1002: the address plaintext scrub, the approval-status tightening and the KYC plaintext scrub.
- payments 998: `application_id` NOT NULL on six tables (`PAY/database/gated/998_application_id_not_null.sql`).
- payments 999: unique provider reference per intent. Its own header says that without it "a genuine, signature-verified capture [can settle] the wrong local order" (`PAY/database/gated/999_provider_reference_uniqueness.sql:8-13`).

### Workers ON or OFF per environment

All workers are goroutines in the server pod; `worker.enabled: false` (`CH/values.yaml:216-217`). "ON" means every replica runs it.

| Worker | Gate | dev | qa | prod | staging | azure | Multi-replica safety |
|---|---|---|---|---|---|---|---|
| commerce outbox relay | none (`C/cmd/server/main.go:434-438`) | ON | ON | ON | (no boot) | (no boot) | lease plus `SKIP LOCKED` (`SH/outbox/publisher.go:60-61`) |
| commerce payments consumer (group `commerce-payments`) | none (:385-387) | ON | ON | ON | – | – | durable inbox; `RetryForever` (`C/internal/consumers/payments_p0.go:102-108`) |
| commerce refund worker, 20 s | none (:393) | ON | ON | ON | – | – | `SKIP LOCKED` (`P/p0.go:1160-1171`) |
| reservation expiry, 1 min, TTL 20 min | none (:399; `P/checkout.go:680`) | ON | ON | ON | – | – | `FOR UPDATE` re-check (`P/p0.go:1324-1330`) |
| fulfilment worker (invoice and shipment booking) | none (:425-426) | ON | ON | ON | – | – | `SKIP LOCKED` (`P/jobs.go:53-69`) |
| identity role worker | none (:452-457) | ON | ON | ON | – | – | not read |
| local-delivery dispatcher | `COMMERCE_LOCAL_DELIVERY_ENABLED` (off) | per compose | off | off | – | – | – |
| stub auto-delivery | `COURIER_STUB_AUTO_DELIVER_AFTER`, refused in managed ENVs (:409-421) | ON (2 m) | off | off | – | – | – |
| **courier status sync** | **does not exist** (webhook only) | – | – | – | – | – | COM-E-8 |
| payments outbox relay | none (`PAY/cmd/server/main.go:324-329`) | ON | ON | ON | ON | (no boot) | lease |
| payments refund worker, 15 s | none (:335) | ON | ON | ON | ON | – | claims (not re-read) |
| payments reconciler, 60 s, age 600 s | only when a real provider is configured (:361-365) | **OFF** (stub) | ON | ON | ON (age 300 s) | – | **no claim** (COM-E-16) |
| payments refund-required worker | `PAYMENTS_REFUND_REQUIRED_WORKER` (`PAY/internal/config/config.go:63-68,172-190`) | ON | **OFF** | **OFF** | **OFF** | – | `SKIP LOCKED` lease |
| payments duplicate-capture consumer | `PAYMENTS_DUPLICATE_CAPTURE_CONSUMER` (same rule) | ON | **OFF** | **OFF** | **OFF** | – | consumer group |

### Secrets per environment (names only, from the `externalSecret` blocks at HEAD)

| Service / env | Secret keys |
|---|---|
| commerce prod | POSTGRES_DSN, REDIS_ADDR, REDIS_PASSWORD, KAFKA_BROKERS, KAFKA_SASL_USERNAME, KAFKA_SASL_PASSWORD, INTERNAL_SERVICE_KEY, JWT_SECRET, JWT_KID, JWT_SECRET_PREVIOUS, JWT_KID_PREVIOUS, COMMERCE_SERVICE_TOKEN_KEY, COMMERCE_SERVICE_TOKEN_KID, COMMERCE_KMS_KEY_ID, COMMERCE_PII_LOOKUP_SALT, COURIER_PROVIDER, SHIPROCKET_EMAIL, SHIPROCKET_PASSWORD, SHIPROCKET_WEBHOOK_TOKEN, SHIPROCKET_WEBHOOK_HMAC (`DV/commerce-service/values-prod.yaml:45-89`) |
| commerce qa | as prod, without COURIER_PROVIDER and the four SHIPROCKET_* keys. `COURIER_PROVIDER: stub` is a plain env value |
| commerce staging | INTERNAL_SERVICE_KEY, JWT_SECRET, JWT_KID, POSTGRES_DSN, KAFKA_BROKERS, REDIS_ADDR, COMMERCE_SERVICE_TOKEN_KEY/KID, COMMERCE_KMS_KEY_ID, COMMERCE_PII_LOOKUP_SALT, four SHIPROCKET_* keys |
| commerce azure-* | INTERNAL_SERVICE_KEY, JWT_* only |
| payments prod / qa | POSTGRES_DSN, KAFKA_BROKERS, KAFKA_SASL_USERNAME, KAFKA_SASL_PASSWORD, INTERNAL_SERVICE_KEY, JWT_* (4), RAZORPAY_KEY_ID, RAZORPAY_KEY_SECRET, RAZORPAY_WEBHOOK_SECRET, SERVICE_CALLER_{COMMERCE,FOOD}_SERVICE_{PUBKEY,KID} (`DV/payments-service/values-prod.yaml:40-71`) |
| payments staging | DSN, KAFKA_BROKERS, RAZORPAY_* (3), SERVICE_CALLER_COMMERCE_* (2) and others |
| payments azure-* | INTERNAL_SERVICE_KEY, JWT_* only |

### Launch switches and what each fences (prod, HEAD)

| Switch | Value | Fences |
|---|---|---|
| `PAYMENTS_ALLOW_STUB` | absent in both services | the stub settle route. Refused in managed ENVs by commerce (`C/cmd/server/main.go:749-756`) and refused for `ENV=prod` by payments (`PAY/cmd/server/main.go:139-142`) |
| Razorpay key mode | secret | real money. Enforced **only by the seeder** (`tools/prodsecrets/env.go:37-45`), not at boot (COM-E-5) |
| `COURIER_PROVIDER` | secret; empty or `stub` means stub | fake AWBs (COM-E-4) |
| `COMMERCE_PLATFORM_COUPONS_ENABLED` | false | platform-funded coupons |
| `COMMERCE_PRODUCT_AUTO_APPROVE` | false | listings without a moderator |
| `COMMERCE_KYC_REQUIRE_DOCUMENT_KIND` | false | requires the document kind for KYC uploads |
| `COMMERCE_PII_CUTOVER`, `COMMERCE_KYC_PII_CUTOVER` | dual / dual | ciphertext-only reads; also blocks gated 1000 and 1002, and with them the whole gated run (COM-E-1) |
| `SERVICE_CALLERS` (commerce) | commented out | the admin-token routes (401) |
| `PAYMENTS_OFFERS_ENABLED` | absent (off) | bank offers |
| `PAYMENTS_REFUND_REQUIRED_WORKER`, `..._DUPLICATE_CAPTURE_CONSUMER` | absent (off) | **automatic refund of owed captures** (COM-E-2) |
| code fences | always | returns, payouts, earnings, RFQ, B2B, COD (`H/handler_p0.go:43-59`) |
| **MStore as a whole** | **no switch** | nothing can turn checkout off in prod short of scaling the service to zero |

---

## P1

### COM-E-1

- **ID:** COM-E-1
- **Title:** The deployment cannot apply the gated contract migrations. The runner would also apply them in the wrong order and abort, so no deployed database gets the order-transition trigger, the provider-reference uniqueness or the money NOT NULLs.
- **Severity:** P1
- **Category:** deployment / data integrity
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - The image builds only `/server`: `C/Dockerfile:20`, `PAY/Dockerfile:20`. `cmd/migrate` is never built.
  - `migrationJob.enabled: false`, and the comment says the gated set "is run by hand": `DV/commerce-service/values-prod.yaml:165-182` (qa :171-185).
  - Boot applies only `migrations/`: `C/cmd/server/main.go:112` → `P/bootstrap.go:14-22`. `C/database/embed.go` keeps `gated/` out of the boot set.
  - **Order defect.** `commerce-migrate -gated` runs `migrationrunner.RunExclusive(..., "gated")` (`C/cmd/migrate/main.go`, `runGated`). That calls `listMigrationFiles`, which uses `sort.Strings` (`SH/store/migrationrunner/runner.go:132`, `exclusive.go:54`). Lexically, `1000_…`, `1001_…`, `1002_…` sort before `998_…` and `999_…`.
  - 1000 refuses unless `pii_cutover_state.ciphertext_authoritative_since` is set (`C/database/gated/1000_pii_plaintext_scrub.sql:64-79`). Every values file runs `COMMERCE_PII_CUTOVER: 'dual'` (`values-prod.yaml:154`). The runner aborts the whole run on the first failure (`runner.go` "On failure we abort"). So 998 and 999 are never reached.
  - Even run by hand in the right order, 998 must come before 999 (998 header: "Run this BEFORE 999").
  - payments gated files have no migrator at all. The runbook says to run them with psql (`docs/runbooks/payments-applications.md:153`).
  - No runbook covers the commerce gated set: `git grep "gated"` in `docs/runbooks` and `docs/harness` finds only the payments line above.
  - What the code relies on:
    - `P/order_fulfilment.go:3-9`: without 998, "nothing guards `UPDATE orders SET status`", and only some Go paths consult the matrix.
    - `PAY/database/gated/999_provider_reference_uniqueness.sql:8-13`: under READ COMMITTED a phantom duplicate reference can settle the wrong order.
- **How to reproduce:**
  1. `git show HEAD:Architecture/services/commerce-service/Dockerfile`: only `/server` is built.
  2. `printf '998_a\n999_b\n1000_c\n' | LC_ALL=C sort` prints `1000_c` first; Go's `sort.Strings` orders bytes the same way.
  3. On a scratch DB at `dual`, `go run ./services/commerce-service/cmd/migrate -gated` fails on 1000 with "B5 scrub precondition failed", and `schema_migrations` has no `commerce-service-gated` rows.
- **Impact:**
  - Production launches with the order state machine enforced only in the Go paths that ask for it, payment methods narrowed in code only, the fenced tables writable by any SQL, money columns nullable, and the provider-reference race open.
  - Nobody can apply the fix without a hand-built binary run against a private Aurora endpoint, and the documented command silently applies nothing.
- **Fix (smallest):**
  1. Rename the commerce gated files so they sort correctly (`0998_`, `0999_`, `1000_`…), or have the gated runner sort numerically. Make `-gated` take an explicit file list (`-only 998,999`) so the PII scrubs are run separately.
  2. Add `RUN go build -o /commerce-migrate ./services/commerce-service/cmd/migrate` to `C/Dockerfile`.
  3. Write a runbook. Prod starts empty, so on a fresh prod run `commerce-migrate -gated -only 998,999` once, before opening the shop, and the two payments files by psql, as a one-off Job using the service's ExternalSecret.
- **Acceptance:**
  - A unit test that `listMigrationFiles` on `C/database/gated` returns 998 before 999 before 1000.
  - On QA: `SELECT filename FROM schema_migrations WHERE service='commerce-service-gated'` lists 998 and 999.
  - `\d orders` shows `trg_order_transition`.
  - `\d payments.payment_intents` shows the unique provider-reference index.
- **Effort:** S (rename, Dockerfile) + S (runbook and one-off Job)

### COM-E-2

- **ID:** COM-E-2
- **Title:** In every deployed environment, money owed back to buyers (late capture on a failed intent, duplicate capture, mismatched capture) is recorded and never refunded, and nothing counts it.
- **Severity:** P1
- **Category:** reliability / money
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `PAY/internal/config/config.go:63-68`: unset means ON only on a dev ENV, "so a production deploy turns it on explicitly". `devDefaultFlag` returns ON only for `""`, `dev` and `local` (:172-190).
  - `DV/payments-service/values-{prod,qa}.yaml` (HEAD) do not set `PAYMENTS_REFUND_REQUIRED_WORKER` or `PAYMENTS_DUPLICATE_CAPTURE_CONSUMER` (prod env block :96-135). Staging and Azure don't set them either.
  - The worker is started only `if cfg.RefundRequiredWorker` (`PAY/cmd/server/main.go:341-343`), and the consumer only if its flag is on (:350-355).
  - What gets parked there: `PAY/internal/service/refund_required.go` header (HEAD): "a late capture on a FAILED intent, or a duplicate capture on a paid one".
  - The only alarm gauge counts `refund_commands WHERE status='needs_attention'` (`PAY/internal/store/postgres/refund_attention.go:167` at HEAD; `PAY/internal/obs/metrics.go:29-40`). No metric reads `refund_required`.
  - The operator surface is unreachable in prod: admin-service is not in `SERVICE_CALLERS` (`values-prod.yaml:93-94,130`), and the legacy key is refused on operator routes in production (`PAY/cmd/server/main.go:257-258`).
  - The reconciler fails an intent after 15 minutes of quiet (`PAYMENTS_FAILED_ATTEMPT_WINDOW`, map §10). A buyer who completes payment after that lands in exactly this table.
- **How to reproduce:** On QA, let an intent fail through the reconciler, then complete the payment on the same Razorpay order. A `payments.refund_required` row appears and stays unresolved. `payments_refunds_needs_attention` stays 0.
- **Impact:** A buyer is charged with no order and no refund until someone runs SQL. No alert fires and no dashboard shows it. The amount is real money once live keys are in.
- **Fix (smallest):** Set `PAYMENTS_REFUND_REQUIRED_WORKER: "true"` and `PAYMENTS_DUPLICATE_CAPTURE_CONSUMER: "true"` in `DV/payments-service/values-{prod,qa}.yaml`. Add a gauge for unresolved `refund_required` rows older than 1 h, with an alert rule (COM-E-3).
- **Acceptance:**
  - On QA the boot log reads `refund-required worker ... enabled=true`.
  - The repro above ends with the row resolved by the signed refund webhook.
  - A rendered prod PrometheusRule contains the refund-required alert.
- **Effort:** S

### COM-E-3

- **ID:** COM-E-3
- **Title:** No commerce or payments alert exists beyond the chart's generic pod and HTTP rules. Half of the commerce "money-integrity" metrics are never incremented, and payments has no webhook or reconciliation metrics.
- **Severity:** P1
- **Category:** observability
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - **No service rules.** `git show HEAD:` of all ten `DV/{commerce,payments}-service/values-*.yaml` contains no `prometheusRule`. The only service rules in `deploy/` are feed-service's (`DV/feed-service/values-prod.yaml:132-200`).
  - **Default rules only.** commerce and payments get `CH/templates/prometheusrule.yaml` (pod crash and restart, no ready pod, scrape target down, 5xx ratio > 5 % and p99 > 1 s **service-wide**; `CH/values.yaml:172-191`). These do route to the ops e-mail topic (`infra/terraform/modules/observability/alerting.tf:46-60`; `envs/prod/main.tf:362-363`).
  - **Dead metrics.** `C/internal/obs/metrics.go:14-17` says four counters "PAGE". Of the instruments it registers, only `PaymentApplied`, `DuplicateSuppressedTotal` and `AmountMismatchTotal` are ever incremented (`C/internal/consumers/payments_p0.go:213,221,377,387,400`). `rg` over `C/` finds no call site for `CheckoutTotal`, `CheckoutDuration`, `OversellAttempt`, `RefundInitiateFailed`, `OrderTransitionRejected` or `QuoteRejected`. Three gauges are sampled (`C/cmd/server/main.go:606-630`).
  - **payments metrics.** payments-service defines exactly two business metrics, `payments_refunds_needs_attention` and `payments_refund_parked_total` (`PAY/internal/obs/metrics.go:29-40`). There is no counter for webhooks received, rejected (401) or duplicate, none for reconciler repairs or drift, and no outbox-lag gauge.
  - **Dev-only alert.** `PaymentsRefundNeedsAttention`, the alert the runbook `docs/runbooks/payments-refund-needs-attention.md:3` names, exists only in the dev compose Prometheus (`Architecture/docker/prometheus/alerts.yml:109`), which has no Alertmanager configured.
  - **Unalerted lag.** Kafka consumer metrics exist (`metrics.NewKafkaConsumerMetrics`, `C/cmd/server/main.go:379`), but no rule watches commerce lag or `social.events.v1.dlq`.
- **How to reproduce:** `git grep -n "OversellAttempt\|CheckoutTotal" HEAD -- Architecture/services/commerce-service` matches only `internal/obs/metrics.go`. `git grep -n prometheusRule HEAD -- deploy/services/commerce-service deploy/services/payments-service` returns nothing.
- **Impact:** Every case below reaches a person only through a buyer complaint:
  - an amount mismatch;
  - a stuck refund (`commerce_refund_pending_age_seconds` climbing);
  - an outbox backlog;
  - a consumer stalled on `RetryForever`;
  - webhooks rejected after a webhook-secret rotation;
  - a dead-lettered fulfilment job.
  
  A checkout route failing at 100 % stays under a 5 % service-wide ratio while catalogue GETs dominate traffic.
- **Fix (smallest):** Add a `prometheusRule` block to `DV/commerce-service/values-{prod,qa}.yaml` and `DV/payments-service/values-{prod,qa}.yaml` (the chart already supports it, `CH/values.yaml:136-140`) with:
  - `increase(commerce_payment_amount_mismatch_total[10m]) > 0` (critical);
  - `commerce_refund_pending_age_seconds > 3600`;
  - `commerce_reservation_age_seconds > 2400`;
  - `commerce_outbox_unpublished_rows > 100` for 10 m;
  - commerce consumer lag > 1000 for 10 m;
  - `payments_refunds_needs_attention > 0`;
  - a 5xx-ratio rule scoped to `path=~"/v1/commerce/(v2/orders/checkout|orders/:orderId/payment.*)"`.
  
  Increment the six dead instruments where their events happen. Add `payments_webhook_total{result}`.
- **Acceptance:**
  - `helm template` for commerce prod renders the rules.
  - A unit test per dead metric shows it increments.
  - On QA, a forced mismatch event produces an e-mail.
- **Effort:** M

### COM-E-4

- **ID:** COM-E-4
- **Title:** Production silently runs the stub courier unless a secret says `shiprocket`. A paid order then auto-books a fake AWB, and the courier webhook accepts anything.
- **Severity:** P1
- **Category:** deployment / config
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `C/internal/courier/courier.go:125-135`: any value other than `shiprocket` returns `&StubCourier{}`.
  - The stub creates `STUB<hex>` AWBs with label `https://example.com/labels/<awb>.pdf` and tracking `https://postbook.app/track/<awb>` (:141-150). `VerifyWebhook` returns nil (:160-161).
  - `C/cmd/server/main.go:137-138` wires it with no environment check, unlike the stub payment (:749-756) and stub auto-delivery (:415-417), which both refuse a managed ENV.
  - Every paid order enqueues `fulfill_paid_order`, which calls `CreateShipmentForOrder` (`S/jobs.go:44-56`).
  - prod reads `COURIER_PROVIDER` from the secret (`DV/commerce-service/values-prod.yaml:79-89`). The comment says "the shop must not take orders before then" (:10-15), but nothing fences checkout.
  - qa hard-codes `COURIER_PROVIDER: stub`.
  - `shiprocket` with empty credentials also boots. The first quote then answers 503 `COURIER_UNAVAILABLE` (`H/handler_p0.go:796-801`).
- **How to reproduce:** Boot commerce with `ENV=prod` and no `COURIER_PROVIDER`. Pay an order; the shipment row carries `STUB…` and an example.com label.
- **Impact:** Real buyers receive fake tracking links and sellers fake labels. A forged courier callback is accepted (it parses no updates, so no state change follows). The mistake is invisible: no log line or metric names the courier in use.
- **Fix (smallest):** In `courier.New`, or in main, refuse to boot in a managed ENV when the provider is not `shiprocket`, unless an explicit `COMMERCE_MANUAL_SHIPPING=true` is set. In manual mode, make the fulfilment job skip shipment booking. Refuse `shiprocket` with empty credentials. Log `courier provider=<name>` at boot.
- **Acceptance:** A unit test in `C/cmd/server` shows `ENV=prod` without a provider exits non-zero. With `COMMERCE_MANUAL_SHIPPING=true`, a paid order has no shipment row.
- **Effort:** S

## P2

### COM-E-5

- **ID:** COM-E-5
- **Title:** Razorpay live or test mode is enforced only by the secrets seeder. payments-service accepts any key in prod and never logs the mode. The prod values comment contradicts the seeder, and an empty key takes MStore and Feast checkout down with a generic 500.
- **Severity:** P2
- **Category:** deployment / config
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `tools/prodsecrets/env.go:31-45` accepts only `rzp_live_` for prod (and `rzp_test_` elsewhere), and an empty value passes (:52-53).
  - `DV/payments-service/values-prod.yaml:8-12` says the seeder stores the **TEST** pair in prod, which the code forbids. The map (§7) repeats it.
  - `PAY/internal/config/config.go:156-167`: no prefix check. With no key and no stub, boot fails with `ErrNoGateway`.
  - `PAY/cmd/server/main.go:152` logs "production credentials detected" whatever the key.
  - If payments is down, commerce intent open returns `INTERNAL_ERROR` (COM-E-10).
- **How to reproduce:** Read the files above.
- **Impact:**
  - If the founder follows the values comment, the seeder refuses.
  - If the secret is left empty "until approval", payments crash-loops in prod and every checkout's pay step answers 500.
  - If a test key is written by hand, prod runs test mode, and test-card payments confirm real orders.
- **Fix:**
  - At boot, refuse a key whose prefix does not match `ENV` (`rzp_live_` iff prod), unless `PAYMENTS_TEST_MODE_IN_PROD=true` is set. Log and expose a `payments_provider_mode{mode}` gauge.
  - Correct the values comment.
  - Decide whether prod launches with MStore closed (see the missing switch in the launch-switch table).
- **Acceptance:** A config unit test: `ENV=prod` with `rzp_test_…` fails. Boot log line `mode=live`.
- **Effort:** S

### COM-E-6

- **ID:** COM-E-6
- **Title:** A Kafka, consumer or commerce outage longer than the 20-minute reservation TTL turns every captured payment into an expired order plus an automatic refund. One poison-adjacent event blocks a partition for ever.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ (mechanism); SUSPECTED (head-of-line scale)
- **Evidence:**
  - The expiry sweep expires any `payment_pending` order whose reservation lapsed, without asking payments (`P/p0.go:1255-1266,1313-1349`). The TTL is 20 min (`P/checkout.go:680`).
  - The capture then arrives as a late capture and is refunded (map §10, `P/p0.go:260-304`).
  - The consumer uses `RetryForever: true` (`C/internal/consumers/payments_p0.go:102-108`) on the shared `social.events.v1`, keyed by `reference_id`. An event that keeps failing (not poison, e.g. a constraint error) holds every later event on that partition for this group.
  - Buyers see "Confirming…" and then "Still confirming" (web `src/features/shop/orders/OrderDetailScreen.tsx:216-238`; Android `PaymentCoordinator.kt:170` `TimedOut`).
  - No lag alert exists (COM-E-3).
- **How to reproduce:** On QA, stop commerce for 25 minutes after a payment captures, then start it. The order goes `expired`, then `refund_pending`.
- **Impact:** Lost sales and charged-then-refunded buyers during any incident longer than 20 minutes. Nobody is paged.
- **Fix:** Before expiring, have `expireOne` ask payments for the bound intent's status, or skip orders whose intent is `succeeded` or `processing`. Alert on consumer lag (COM-E-3). Cap `RetryForever` per event at N hours, then park the event in a table, not the partition.
- **Acceptance:** Integration test: an intent succeeded at payments but no event delivered means the sweep leaves the order `payment_pending`.
- **Effort:** M

### COM-E-7

- **ID:** COM-E-7
- **Title:** Invoice and shipment booking for a paid order dead-letter after about 75 seconds of S3 or courier trouble. Nothing replays them, counts them or alerts on them.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `C/internal/workers/fulfillment.go:45` (`maxAttempts: 5`) and :161-165 (backoff 5, 10, 20, 40 s) lead to `DeadLetterJob` (:152-158; `P/jobs.go:116-123`).
  - `FulfillPaidOrderJob` issues the invoice, then books the shipment (`S/jobs.go:44-56`).
  - The only read is `GET /internal/jobs/dead-letter` (internal key, `H/handler_onboarding.go`), and the admin console is 401. No requeue code exists (`rg -i "requeue|replay"` finds only comments).
- **How to reproduce:** Point `COMMERCE_BLOB_BUCKET` at a missing bucket on QA and pay an order. The job is `dead` after about 75 s.
- **Impact:** Paid orders silently get no GST invoice and no booked shipment. The seller can book by hand only if they notice.
- **Fix:** Retry for 24 h (cap the backoff at 5 min, raise `maxAttempts` to about 300). Add a gauge `commerce_fulfillment_dead_jobs` with an alert, and an internal `POST /internal/jobs/:id/requeue`.
- **Acceptance:** A unit test that a job failing for 10 min is still `pending`. The gauge appears on `/metrics`.
- **Effort:** S

### COM-E-8

- **ID:** COM-E-8
- **Title:** Shipment status comes only from courier webhooks. A missed Shiprocket webhook leaves an order `shipped` for ever.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - The worker list in `C/cmd/server/main.go:385-457` has no tracking poller.
  - `rg "func (s \*Service) Run"` in `S/` finds only refund, reservation expiry, delivery dispatcher and stub auto-delivery.
  - The courier interface's tracking input is `ParseWebhook` (`C/internal/courier/courier.go:155-158`).
- **How to reproduce:** Read the files above.
- **Impact:** Buyers never see "delivered", and post-delivery flows (reviews need a delivered order, `S/reviews.go:30-46`) never open. Nobody is told.
- **Fix:** A 1-hourly sweep that calls Shiprocket tracking for shipments in `shipped`/`out_for_delivery` with no event for more than 24 h.
- **Acceptance:** A unit test with a fake courier: a stale shipment is moved by the sweep.
- **Effort:** M

### COM-E-9

- **ID:** COM-E-9
- **Title:** A Redis outage takes all of MStore down (readiness and boot), though commerce uses Redis only for the product view counter.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - Boot exits if Redis does not answer (`C/cmd/server/main.go:97-100`). Readiness registers `redis` (:128-130).
  - The only Redis use is the sharded view counter (`S/service.go:191-204`; `rg "s\.rdb"` in `C/internal` finds no other use). The payments consumer discards `rdb` (`C/internal/consumers/payments_p0.go:85`).
  - The same pattern was recorded for feed services (feed-43).
- **How to reproduce:** On QA, stop ElastiCache. Every commerce pod's `/readyz` answers 503, and the Service has no endpoints.
- **Impact:** Checkout, orders and webhooks into commerce stop for the length of a cache incident. Payments keep capturing, and with COM-E-6 those orders then expire and refund.
- **Fix:** Drop the `redis` readiness check and make the boot ping a warning. The counter already falls back to a direct UPDATE when Redis is nil (`S/service.go:211-213`).
- **Acceptance:** A unit test that `/readyz` stays 200 with Redis down.
- **Effort:** S

### COM-E-10

- **ID:** COM-E-10
- **Title:** When payments-service is unreachable, "Pay" answers 500 `INTERNAL_ERROR` "something went wrong", not a retryable 503.
- **Severity:** P2
- **Category:** reliability / UX
- **Evidence level:** VERIFIED-READ (server); SUSPECTED (client copy)
- **Evidence:**
  - `S/checkout_p0.go:393-415` returns the payments client error unwrapped.
  - `writeCommerceError` has no case for payments errors and reaches the default (`H/handler_p0.go:811-838`).
  - Courier and media outages, by contrast, have 503 codes (:796-801, :695).
  - The client timeout is `paymentsclient.DefaultTimeout` (`C/internal/payments/p0client.go:141-147`).
- **How to reproduce:** Scale payments to 0 on QA, then `POST /v1/commerce/orders/:id/payment/intent` → 500.
- **Impact:** Buyers see a generic failure with no "try again in a minute". Ops sees an "unmapped error" log line, not a dependency signal.
- **Fix:** Map transport errors and 5xx from payments to 503 `PAYMENTS_UNAVAILABLE` with retry copy. Have both clients show "Payments are briefly unavailable, your order is saved".
- **Acceptance:** A handler test with a closed payments listener expects 503 and the code.
- **Effort:** S

### COM-E-11

- **ID:** COM-E-11
- **Title:** A paid order cannot be followed by one id from gateway to webhook to confirmation.
- **Severity:** P2
- **Category:** observability
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - The gateway sets `X-Request-Id` (`api-gateway/cmd/server/main.go:1268-1279` at HEAD). commerce reads it (`SH/middleware/requestid.go:22-25`).
  - commerce → payments forwards W3C trace context through `otelhttp` (`C/internal/payments/p0client.go:147`) but not `X-Request-Id`, so payments logs a fresh `request_id`.
  - The outbox relay injects the relay's own span, "the originating request context is not stored with the row" (`SH/outbox/relay.go:271-274`). The webhook → payments outbox → commerce consumer chain is therefore a separate trace.
  - The webhook success path logs nothing (`PAY/internal/http/handler.go:1208-1222`): no line with `provider_order_id` or `intent_id` on accept. Only failures are logged.
  - The commerce consumer logs `order_id` on apply (`payments_p0.go:379`).
- **How to reproduce:** Read the files above.
- **Impact:** In an incident, nobody can show from logs that Razorpay's webhook for order X arrived, when, and what payments did with it. Support cannot answer "I paid, where is my order" without SQL.
- **Fix:** Log one info line per accepted webhook with provider, event type, event id, provider order id and intent id. Store `trace_id` on the outbox row and inject it. Forward `X-Request-Id` in the payments client.
- **Acceptance:** On QA, one `kubectl logs` grep for an order id returns lines from commerce checkout, the payments webhook and the commerce consumer.
- **Effort:** S

### COM-E-12

- **ID:** COM-E-12
- **Title:** Staging and both Azure environments cannot boot commerce, so no non-production environment runs a checkout against a signed webhook except QA. Staging's PDB also blocks node drains.
- **Severity:** P2
- **Category:** deployment
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - **staging commerce:** `ENV: staging` is managed (`C/cmd/server/main.go:709-714`), so `blobProduction` is true. No `COMMERCE_BLOB_BACKEND` is set (`DV/commerce-service/values-staging.yaml:72-135` at HEAD), so `ConfigFromEnv` refuses (`C/internal/store/blob/config.go:91-95`) and main exits (:159-163). There is also no `AWS_REGION` (needed by blob and KMS), and staging IRSA is MSK-only (map §6).
  - **staging misc:** `COMMERCE_SKIP_BOOT_MIGRATIONS: 'true'` is set but read by nothing. `pdb` is unset, so the chart default `minAvailable: 2` applies with 2 replicas (`CH/values.yaml:205-207`). There is no webhook ingress.
  - **azure-prod and azure-staging:** commerce has only INTERNAL_SERVICE_KEY and JWT_* secrets and no `POSTGRES_DSN`, so the boot ping fails (`C/cmd/server/main.go:85-88`). payments has no Razorpay key, no stub and no `SERVICE_CALLERS`, so it fails with `ErrNoGateway` or `ErrServiceCallersRequired` (`PAY/internal/config/config.go:133-134,167`). Image tag `latest` in prod (`values-azure-prod.yaml`).
- **How to reproduce:** `QA_GUARDS_ONLY` does not cover staging. Read the values files.
- **Impact:** The ArgoCD ApplicationSets for staging and Azure sync crash-looping pods. The only rehearsal of a real-webhook checkout before prod is QA, with the stub courier.
- **Fix:** Either delete the staging and Azure commerce/payments values (and their ApplicationSet entries), or add the missing keys. In staging, set `pdb.enabled: false` or `minAvailable: 1`.
- **Acceptance:** `helm template` and a boot of each overlay's env in a unit test (`C/cmd/server` config dry-run) succeed, or the overlays are gone.
- **Effort:** S (delete) / M (complete)

### COM-E-13

- **ID:** COM-E-13
- **Title:** A database restore breaks commerce's agreement with Razorpay and may reuse GST invoice numbers. No commerce restore procedure exists, and tax-record retention is undefined.
- **Severity:** P2
- **Category:** data / backup
- **Evidence level:** VERIFIED-READ (backup config); SUSPECTED (numbering reuse)
- **Evidence:**
  - Aurora point-in-time restore keeps 7 days and AWS Backup 14 days, with no vault lock and no cross-region copy (`infra/terraform/envs/prod/variables.tf:326-330,411-414`; `modules/ops-baseline/main.tf:75-95`).
  - `docs/runbooks/restore-aurora.md:3` says "UNTESTED", and the runbook restores the whole cluster. `commerce_db` holds both commerce and `payments.*`, so a restore to time T0 loses every intent, capture and refund after T0.
  - The reconciler only revisits intents that still exist and are `pending` (`PAY/internal/store/postgres/p0.go:1439-1451`). Captures after T0 have no local row.
  - `invoice_sequences` (002, 039) rewinds while S3 keeps `invoices/<FY>/<number>.html` (`S/shipments.go:654-689`; bucket versioned, noncurrent versions kept 365 d, `infra/terraform/envs/prod/buckets.tf:29-37`).
  - Retention policy is "awaiting ruling" (`C/internal/pii/envelope.go:21,47`; `C/database/migrations/011_address_pii_encryption.sql:97-104`). GST requires 72 months.
- **How to reproduce:** Read the files above.
- **Impact:** After any restore, buyers charged after T0 have no order and are never refunded automatically, and invoice numbers issued after T0 can be issued again to different orders, which is illegal under GST.
- **Fix:** Add a commerce section to `restore-aurora.md`:
  1. Pull Razorpay's payments and refunds since T0 (dashboard export or API).
  2. Diff them against `payments.payment_intents`.
  3. Refund the orphans.
  4. Set `invoice_sequences` to the maximum number found in S3 before reopening.
  
  Write down the retention decision (orders, invoices, payments: 8 years) and add an S3 Object Lock or a lifecycle rule that does not expire current invoice versions.
- **Acceptance:** A QA drill record that includes the Razorpay diff step and the sequence check.
- **Effort:** M

### COM-E-14

- **ID:** COM-E-14
- **Title:** The PSP webhook shares the edge WAF's per-IP rate rule, with no path scope and no IP allowlist. The dedicated webhook ACL is built but cannot be attached.
- **Severity:** P2
- **Category:** deployment / reliability
- **Evidence level:** VERIFIED-READ (config); SUSPECTED (blocking under load, body-size rule)
- **Evidence:**
  - `DV/payments-service/values-prod.yaml:145-160` uses `wafAclArn` = the web ACL, with the comment that the psp-webhook ACL "cannot attach here".
  - The edge ACL `rate-limit-per-ip` blocks above `rate_limit_per_5min` per IP (`infra/terraform/modules/waf/main.tf:87-98`; prod uses the module default, `envs/prod/main.tf:189-194`).
  - `module "waf_psp_webhook"` (:200-205) is created and unused.
  - `AWSManagedRulesCommonRuleSet` (:29-46) includes a body-size rule (8 KB).
  - Razorpay posts from a small fixed IP set, for MStore, Feast and every other payments application.
  - No CloudWatch alarm on the WAF block metric exists in `infra/` (`rg "aws_cloudwatch_metric_alarm"` in `modules/waf`: none).
- **How to reproduce:** Read the files above.
- **Impact:** On a busy sale, webhooks beyond the per-IP ceiling are blocked at the edge. Razorpay retries, and the reconciler catches up after 10 min, so confirmations lag 10+ min and some orders cross the 20-minute expiry (COM-E-6). Nobody sees the WAF blocks.
- **Fix:** Add a rule to the edge ACL scoped to `/v1/payments/webhook` that allows Razorpay's published IP ranges ahead of the rate rule, or move the webhook to its own host and ALB with the psp-webhook ACL. Add a CloudWatch alarm on `BlockedRequests` for that rule.
- **Acceptance:** `terraform plan` shows the scoped rule. render_test.sh asserts the webhook host or rule.
- **Effort:** S

## P3

### COM-E-15

- **ID:** COM-E-15
- **Title:** No startup probe. Boot work (setup.sql, migrations, and a KMS seal-and-open test with a 30 s timeout) runs before the listener, and liveness kills the pod about 30 s after start.
- **Severity:** P3
- **Category:** deployment
- **Evidence level:** SUSPECTED
- **Evidence:**
  - `C/cmd/server/main.go:112,278` run before `server.Run` (:583). `piiReadinessTimeout = 30 * time.Second` (:788).
  - `CH/values.yaml:193-197` sets liveness at 10 s / 10 s / 3. There is no `startupProbe` in `CH/templates/deployment.yaml`.
  - The migration lock wait is 5 min (`SH/store/migrationrunner/exclusive.go:170`). A second replica waiting on it is killed by liveness.
- **How to reproduce:** On QA, add a migration that sleeps 60 s. Pods restart in a loop until it commits.
- **Impact:** A slow migration or slow KMS start becomes a crash loop during a rollout.
- **Fix:** Add `startupProbe` (path `/livez`, failureThreshold 30, period 10) to the chart.
- **Acceptance:** render_test.sh asserts a startup probe.
- **Effort:** S

### COM-E-16

- **ID:** COM-E-16
- **Title:** The payments reconciler runs on every replica with no claim, so each stale intent is looked up at Razorpay once per replica per minute.
- **Severity:** P3
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:** `PAY/cmd/server/main.go:361-365` starts it unconditionally. `StalePending` is a plain SELECT (`PAY/internal/store/postgres/p0.go:1439-1451`). Applying the result is idempotent (webhook transaction), so this is cost and rate-limit exposure, not double money.
- **How to reproduce:** Read the files above.
- **Impact:** With 6 replicas (HPA max), 6× Razorpay API calls. Under Razorpay rate limiting, recovery of lost webhooks slows.
- **Fix:** Wrap `reconcileOnce` in `pg_try_advisory_lock`.
- **Acceptance:** A unit test with two runners: one lookup.
- **Effort:** S

### COM-E-17

- **ID:** COM-E-17
- **Title:** Deployed images never produce a PDF invoice: wkhtmltopdf is not in the image.
- **Severity:** P3
- **Category:** deployment
- **Evidence level:** VERIFIED-READ
- **Evidence:** `C/Dockerfile:22-25` is `alpine:3.19` with `/server` only. `SH/invoice/pdf.go:32-35` looks up `wkhtmltopdf` and errors when it is absent. `S/shipments.go:649` falls back to HTML.
- **How to reproduce:** Read the files above.
- **Impact:** Buyers and sellers get HTML invoices only. Fine if intended; otherwise a silent gap.
- **Fix:** Decide. Either document HTML-only, or install a renderer in the image.
- **Acceptance:** The decision is recorded. If PDF: an image test that `wkhtmltopdf --version` runs.
- **Effort:** S

### COM-E-18

- **ID:** COM-E-18
- **Title:** Boot migration hygiene: setup.sql runs unlocked on every replica, a documented flag is read by nothing, and the newest compat-flagged migrations carry no acknowledgement.
- **Severity:** P3
- **Category:** deployment
- **Evidence level:** VERIFIED-RUN (compat check); VERIFIED-READ (rest)
- **Evidence:**
  - `P/bootstrap.go:15` and `PAY/internal/store/postgres/bootstrap.go:20` run `setup.sql` before `RunExclusive` takes the lock. Concurrent `CREATE … IF NOT EXISTS` on two pods can race.
  - `COMMERCE_SKIP_BOOT_MIGRATIONS` is mentioned in values (`values-prod.yaml:140-143`, staging sets it) and `C/cmd/migrate/main.go:22`, but `git grep` finds no reader in the server.
  - `scripts/verify/migration-compat.sh --files` on commerce `05*.sql` and payments `016-019` flags `051_stock_adjustment_idempotency.sql:22` and `052_reservation_identity_live_only.sql:22` (unique indexes) with no `-- compat: ok`. The script is not in CI (its header, `scripts/verify/migration-compat.sh:43`).
- **How to reproduce:** Run the command in "What I ran".
- **Impact:** A rare boot failure on a scale-out, and a rollback check that is manual and noisy.
- **Fix:** Move the setup.sql exec inside the advisory lock. Delete the dead flag from values or implement it. Acknowledge 051 and 052.
- **Acceptance:** The compat check exits 0 on the last two releases' migrations.
- **Effort:** S

### COM-E-19

- **ID:** COM-E-19
- **Title:** commerce-service has no NetworkPolicy, and payments' policy admits the whole VPC CIDR and the gateway. Neither is enforced by the cluster.
- **Severity:** P3 (here; the security weight belongs to Track B)
- **Category:** deployment
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - No `networkPolicy` in `DV/commerce-service/values-*.yaml`; the chart default is `enabled: false` (`CH/values.yaml:405-406`).
  - payments: `DV/payments-service/values-prod.yaml:164-178` (`api-gateway`, `10.30.0.0/16`).
  - `vpc-cni` has no `enableNetworkPolicy` (`infra/terraform/modules/eks/main.tf:58-61`). See reels-6.
- **How to reproduce:** Read the files above.
- **Impact:** Any pod can reach commerce's internal-key routes, and in practice payments as well.
- **Fix:** Enable enforcement (reels-6). Add a commerce policy: gateway, admin-service, search-service and media-service as peers. Narrow payments' CIDR rule to the ALB subnets.
- **Acceptance:** The rendered prod manifest has both policies, and a probe pod is refused.
- **Effort:** M

---

## Inherited (re-checked at `be57a46bf`, not counted)

| Item | Original | Status for commerce and payments | Evidence |
|---|---|---|---|
| Liveness `/livez` not served | feed-01 (P0) | **Fixed.** `SH/health/checker.go:141` registers `/livez`. commerce and payments use `checker.RegisterRoutes` (`C/cmd/server/main.go:577`, `PAY/cmd/server/main.go:308`) | VERIFIED-READ; render_test "probes" ok (VERIFIED-RUN) |
| No environment and no deployable image | feed-02 (P0) | **Still open.** `docs/runbooks/aws-first-production-deployment.md:7` "Nothing in this plan has been run". `build-push.yml:74-87` skips without `AWS_CI_ROLE_ARN`. GitHub run history not queried (UNVERIFIED) | VERIFIED-READ |
| Alerts reach nobody | feed-10 (P1) | **Partly fixed.** Alertmanager now routes `source=atpost-service` to the ops SNS e-mail (`infra/terraform/modules/observability/alerting.tf:46-60`, prod `main.tf:362-363`), and the chart renders default rules for every service. Commerce-specific gaps are COM-E-3. The subscription confirmation is a manual step (`alerting.tf:27-28`) | VERIFIED-READ |
| No rollback procedure | feed-11 (P1) | **Procedure written, never drilled** (`docs/runbooks/service-rollback.md:3` "UNTESTED"). It does not mention gated migrations, or that commerce and payments share `commerce_db` and the event contract | VERIFIED-READ |
| No tested restore | feed-12 (P1) | **Still open** (`docs/runbooks/restore-aurora.md:3` "UNTESTED"). Commerce-specific consequences are COM-E-13 | VERIFIED-READ |
| Shutdown closes the DB before draining HTTP, no preStop | feed-42 (P2) | **Still open; applies to both services.** `SH/server/server.go:87-91` runs `OnShutdown` (commerce closes the pool, `C/cmd/server/main.go:586-592`; payments `PAY/cmd/server/main.go:369-372`) before `srv.Shutdown`. No `lifecycle.preStop` in `CH/templates/deployment.yaml`. In-flight checkouts and webhooks fail on every rollout; Razorpay retries webhooks. payments ignores `server.Run`'s error (`PAY/cmd/server/main.go:367`) | VERIFIED-READ |
| Readiness on a shared store | feed-43 (P2) | Commerce instance counted separately as COM-E-9 (commerce's own code) | — |
| Request ID not forwarded | feed-46 (P2) | **Partly fixed.** The gateway now sets `X-Request-Id`. The commerce → payments and Kafka hops are COM-E-11 | VERIFIED-READ |
| No CI verdict on the shipped code | feed-47 (P2) | **Still open.** Not re-run here | UNVERIFIED |
| Boot migrations without an advisory lock | feed-75 (P3) | **Fixed** for commerce and payments (`RunExclusive`), except setup.sql (COM-E-18) | VERIFIED-READ |
| Staging values stale | feed-77 (P3) | **Still open;** for commerce it is a boot failure (COM-E-12) | VERIFIED-READ |
| DLQ has no replay | feed-06 (P1) | **Applies:** poison commerce payment events go to `social.events.v1.dlq` (`payments_p0.go:104,148`) and nothing replays them | VERIFIED-READ |
| NetworkPolicy not enforced | reels-6 (P1) | **Applies** to payments' policy (COM-E-19) | VERIFIED-READ |
| End-of-life Alpine 3.19, no image scan | reels-63 (P2) | **Applies:** `C/Dockerfile:22`, `PAY/Dockerfile:22` | VERIFIED-READ |
| The chart's QA guard fails | reels-62 (P2) | **Still failing,** now on `DV/post-service/values-qa.yaml:121` (prod host in a QA file; working tree). No commerce or payments failure | VERIFIED-RUN |

## What I ran

- `QA_GUARDS_ONLY=1 bash charts/atpost-service/render_test.sh`: QA guards pass for commerce and payments; overall **FAILED** on post-service (`ACTION_LINK_OWN_DOMAINS`). The probe, default-alert and feed-rule guards pass. Note: it reads the working tree, not HEAD.
- `bash scripts/ci/kafka-topics-check.sh`: PASS (29 topics).
- `bash scripts/verify/migration-compat.sh --files <commerce 001-055> <payments 001-019>`: 64 flagged, 0 acknowledged (mostly historic; irrelevant for an empty prod). On commerce `05*` and payments `016-019`: 2 flagged (051, 052), exit 1.
- `go test ./services/payments-service/internal/config/... ./services/commerce-service/cmd/server/... ./services/commerce-service/internal/courier/... ./shared/health/... ./shared/server/... ./shared/outbox/...`: all ok (working tree; none of these packages has uncommitted edits).
- Read-only `git show HEAD:`, `git grep`, `rg`, `diff` of values files. No database, cluster, Terraform, Docker or GitHub call.

## Not verified

- Whether any running database (devbox, or anything on AWS) has the gated sets applied. Nothing on AWS is believed to exist (feed-02).
- GitHub Actions history for `build-push.yml` and `integration-commerce.yml` (no network calls made).
- Actual Razorpay webhook body sizes against the WAF body-size rule, and Razorpay's sender IP count (COM-E-14).
- The payments refund worker's claim SQL (not re-read; assumed from the map).
- Client copy for a 500 on "Pay" (COM-E-10). The web and Android "still confirming" states were read; the "Pay" error path was not.
- Aurora `max_connections` against 2 × (25 pool × up to 6 replicas) on `commerce_db` (Track D).
- Whether `identityroles` worker claims are replica-safe.
- The devbox compose: the commerce block shows no `ENV:` line in the first 90 lines read (it may come from an anchor or env_file), so how the dev commerce boots was not settled.

## Notes for other tracks

- **Track B:**
  - `payments_service` and `commerce_service` are both granted `commerce_db_owner` (`tools/prodsecrets/emit_state_test.go:80-85`). Each service can alter or drop the other's tables.
  - Razorpay has no replay window; Cashfree does (`PAY/internal/gateway/cashfree_provider.go:162`). Map Q7 stands.
  - The courier stub's `VerifyWebhook` accepts anything in prod when the stub is selected (COM-E-4).
- **Track C:**
  - Without gated 998, `orders.status` writers not using `order_status_transitions` are unguarded (COM-E-1). Please list them.
  - Without payments gated 999, the provider-reference phantom is open.
  - GST invoice numbering after a restore (COM-E-13).
- **Track A:**
  - MStore has no off switch in prod.
  - `COMMERCE_PAYMENT_METHODS` is documentation only.
  - The prod values comment on Razorpay test keys contradicts the seeder (COM-E-5); map §7 and §15 Q12 should be corrected: the seeder refuses `rzp_test_` for prod.
- **Track D:**
  - Each commerce replica runs every worker. The fulfilment worker polls every 1 s per replica.
  - Pool 25 per pod for both services on one database.
  - The reconciler is duplicated per replica (COM-E-16).
- **Track F:**
  - `commerce-migrate -gated` is never exercised in CI (CI applies 998 by psql only, `integration-commerce.yml:359`). A test of the gated file order would have caught COM-E-1.
  - No test asserts that each `obs` instrument is incremented.
  - `render_test.sh` and `migration-compat.sh` are still not in CI.
- **Map corrections:**
  - §15 Q7: dedupe now also has a Cashfree-only replay window.
  - §7: prod Razorpay is live-only by the seeder.
  - The commit is now `be57a46bf`.
