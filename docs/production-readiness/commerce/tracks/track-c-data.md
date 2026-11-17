# Commerce: Track C (data integrity, concurrency, money correctness)

- **Audit date:** 2026-11-17. Read-only.
- **Tree:** `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` @ `be57a46bf` (HEAD). commerce-service has no uncommitted changes. For payments-service files with uncommitted edits (`internal/service/refund_required.go`, `internal/http/admin_routes.go`, `internal/store/postgres/{admin,refund_attention,refund_required}.go`) and every `deploy/services/*/values-*.yaml`, the committed version was read with `git show HEAD:<path>`.
- **Path shorthand** (as in `00-map.md`): `C/` = `Architecture/services/commerce-service/`, `P/` = `C/internal/store/postgres/`, `S/` = `C/internal/service/`, `PAY/` = `Architecture/services/payments-service/`.
- **Evidence tags:** VERIFIED-RUN (a command was run), VERIFIED-READ (code read at the cited lines), SUSPECTED (inferred from the code, not reproduced), UNVERIFIED (not checked).
- **Executed:**
  - `go test -count=1 ./internal/tax/ ./internal/money/ ./internal/consumers/` (commerce): ok.
  - `go test -count=1 -run 'Capture|Refund|Coupon|Retry|Transition|Restock|Cancel' ./internal/store/postgres/` (commerce, unit only, no DB): ok.
  - Nothing else: no Docker, no database, no integration test.

## Summary

| ID | Title | Sev |
|---|---|---|
| COM-C-1 | Owed captures (late, duplicate, mismatched) are recorded but never refunded in any deployed environment | **P0** |
| COM-C-2 | Seller proceeds: no payable ledger, no payout path, and the only earnings derivation reads ₹0 columns | P1 |
| COM-C-3 | GST invoice numbering and content: platform-wide series, gaps, no place of supply, no credit note | P1 |
| COM-C-4 | Gated 999 can never run (drift view flags every P0 order), and 998 is applied nowhere by default | P2 |
| COM-C-5 | The seller dashboard reports ₹0 revenue (it reads the NUMERIC mirror) | P2 |
| COM-C-6 | An expired or cancelled order leaves its intent and Razorpay order payable | P2 |
| COM-C-7 | Money alarms have no alert rule, the DLQ has no reader, and parked commerce refunds have no operator path | P2 |
| COM-C-8 | A cancel can race the fulfilment job's courier booking | P2 (SUSPECTED) |
| COM-C-9 | `COMMERCE_SKIP_BOOT_MIGRATIONS` is read by no code | P3 |
| COM-C-10 | Legacy float and NUMERIC money paths remain reachable (COD settle, `UpdateOrderStatus`) | P3 |

Counts: P0 1, P1 2, P2 5, P3 2.

## Open questions from `00-map.md`, settled

- **Q1 (gated 998/999 applied?).**
  - **How migrations run:**
    - commerce-service runs `setup.sql` and `migrations/001-055` at every pod boot (`C/cmd/server/main.go:111-115` → `P/bootstrap.go:14-24`).
    - The gated set is embedded separately (`C/database/embed.go:34`) and is applied only by `commerce-migrate -gated` (`C/cmd/migrate/main.go:95-117`).
    - That binary is not built into the image: the Dockerfile builds only `cmd/server` (`C/Dockerfile:20`).
    - The chart Job is off by default (`charts/atpost-service/values.yaml:318-319`) and off in prod and qa (HEAD `values-prod.yaml:176-177`, `values-qa.yaml:180-181`). Staging has no `migrationJob` block.
    - Even if the Job were enabled, its command has no `-gated` flag (`values-prod.yaml:179-180`).
    - Dev (docker-compose) has no reference to the gated set (repo-wide grep for `-gated`/`commerce-migrate`).
    - CI applies 998 only, to `commerce_p0_test` (`.github/workflows/integration-commerce.yml:359`). No workflow applies 999.
  - **Conclusion:** by construction, 998 and 999 are applied in no deployed environment unless someone ran them by hand. The live databases were not inspected, so that part is UNVERIFIED.
  - **Protection lost:** small, because the Go paths re-check. Details in COM-C-4 and in the state-machine table.
  - **Writers that update `orders.status` without consulting the matrix** (all VERIFIED-READ):
    - capture: `P/p0.go:318-324`, guarded by `decideCapture` (`:877-888`);
    - `payment_failed`: `P/p0.go:448-452`, guarded by `decideFailure`;
    - expiry: `P/p0.go:1339-1343`, guarded by `status == 'payment_pending'` under `FOR UPDATE` (`:1323-1330`);
    - `UpdateOrderStatus` (`P/orders.go:359-381`), whose only callers are the fenced returns routes (`S/returns.go:93,134,298`).
    - Every other status move goes through `transitionOrderStatusTx`, which reads `order_status_transitions` (`P/order_fulfilment.go:41-88`).
- **Q2 (legacy NUMERIC).**
  - The P0 checkout writes **0** into every NUMERIC money column of `orders` and `order_items` (`P/checkout.go:543-544,608-609`). The `*_minor` columns are the only truth for an order.
  - Variants keep both columns in sync (`P/variants_stock.go:56-74,186-215`).
  - Read paths:

    | Reader | Column |
    |---|---|
    | checkout, capture, refund, cancel, admin GMV | minor (`P/checkout.go:875`, `P/p0.go:177,693`, `P/admin_stats.go:27-31`) |
    | invoice | minor via `GetOrderInvoiceMoney`, then stored as NUMERIC on `invoices` (`S/shipments.go:803-815`) |
    | seller dashboard revenue | **NUMERIC** `oi.final_price` (`P/onboarding.go:704-707`): COM-C-5 |
    | seller earnings (fenced) | **NUMERIC** `FinalPrice` (`S/seller_account.go:176,184`): COM-C-2 |
    | COD remittance gross | **NUMERIC** `it.FinalPrice` (`S/shipments.go:435-440`): COM-C-10 |

  - **`money_minor_drift` cannot be zero** anywhere a P0 order exists: the view compares `final_amount_minor` with `ROUND(final_amount*100)` (`C/database/migrations/007_money_minor.sql:160-176`). That is COM-C-4.
- **Q3 (payout).** No payout executor exists, and nothing persists what a seller is owed (COM-C-2). Who the merchant of record is remains a founder question. In code, all capture lands in the platform's Razorpay account; the seller id is only the intent's `PayeeID` (`S/checkout_p0.go:401-405`).
- **Q6 (who owns which owed capture).**

  | Case | Owner | Deployed behaviour |
  |---|---|---|
  | Capture on an expired or cancelled order, **intent still pending** | commerce (`order_refund_commands` → commerce refund worker, on) | refunded automatically (`P/p0.go:265-309`) |
  | Capture on an intent the reconciler already finalised `failed` | payments `refund_required` (`PAY/internal/store/postgres/p0.go:292-296`) | **recorded, never refunded** (COM-C-1) |
  | Second capture on the same intent | payments `refund_required` (`:297-301`) | **never refunded** |
  | Amount or currency mismatch | payments `refund_required` (`PAY/internal/service/p0.go:144-156`) | **never refunded** |
  | Second paid intent on one order | commerce emits `commerce.order.duplicate_capture` (`P/p0.go:989-1007`); payments consumer records it | **consumer off: not even recorded in payments** |

## State machines

### Order status (`orders.status`)

Vocabulary: `C/database/migrations/010_gst_and_state_machine.sql:145-150`. Matrix rows: `010:93-118`, plus `033` (system `confirmed/packed → shipped`) and `054` (seller `→ delivered`).

**Enforcement:**
- **Go** = `transitionAllowedTx`/`transitionOrderStatusTx` (`P/order_fulfilment.go:41-88`).
- **Trig** = `trg_order_transition`. It is attached only by gated 998 (`gated/998_contract_triggers_and_fences.sql:73-76`), so it is **absent by default**.

| From → To | Actor | Trigger path | Enforced by |
|---|---|---|---|
| (insert) → `payment_pending` | customer | `POST /v2/orders/checkout`, `P/checkout.go:529-590` | Go (insert only) |
| `payment_pending` → `confirmed` (payment_status `paid`) | system | `payment.succeeded`, `P/p0.go:318-324` | Go `decideCapture`/`OrderPayable` (`P/p0.go:877-888`, `P/order_transitions.go:72-74`); not the matrix |
| `payment_pending` → `payment_failed` | system | `payment.failed` for the bound intent, `P/p0.go:428-452` | Go `decideFailure` |
| `payment_pending` → `expired` | system | sweeper, 1 min, TTL 20 min (`P/p0.go:1255-1350`, `P/checkout.go:680`) | Go (`status == payment_pending` under lock) |
| `payment_failed` → `payment_pending` | customer | `POST /orders/:id/payment/intent` retry, `P/payment_retry.go:57-160` | Go matrix (`:141`) |
| `payment_pending`/`payment_failed`/`confirmed`/`packed` → `cancelled` | customer/seller/admin per matrix | `POST /orders/:id/cancel`, seller cancel, `P/p0.go:674-818` | Go matrix (`:721-727,760`) |
| `confirmed` → `packed`; `packed` → `shipped` | seller | seller fulfilment (`P/order_fulfilment.go:143`) | Go matrix |
| `confirmed`/`packed` → `shipped`; `shipped` → `out_for_delivery` → `delivered` | system | courier webhook / fulfilment (`P/order_fulfilment.go:207,258-281`), local delivery (`P/delivery_operations.go:629-641`) | Go matrix |
| `shipped`/`out_for_delivery` → `delivered` | seller | seller-managed shipment (`P/seller_delivery.go:78`) | Go matrix |
| `delivered`/`cancelled` → `refund_pending` → `refunded` | admin/system | **no code path writes these order statuses**; refund progress lives in `payment_status` (`P/refund_settlement.go:296-300`) | — |
| `* → return_*` | customer/seller | `UpdateOrderStatus` (fenced returns) | **nothing** (no matrix check, `P/orders.go:359-381`) |

`paid` (as an order status) is in the vocabulary but in no matrix row and is never written.

**`payment_status`** (order): `pending → processing → paid → refund_pending → refunded/partially_refunded`, and `pending/processing → failed → paid`. Table: `P/order_transitions.go:35-42`.
- Capture writes `paid` (`P/p0.go:320`).
- A late capture writes `refund_pending` (`:299`).
- Cancel of a paid order writes `refund_pending` (`:800-804`).
- A retry writes `pending` (`P/payment_retry.go:144-146`).
- Settlement writes `refunded` or `partially_refunded` (`P/refund_settlement.go:295-300`).
- No DB trigger exists on `payment_status`. Only the setup CHECK on the vocabulary applies (setup.sql:444, per the map).

### Payment intent (payments-service `payment_intents.status`)

| From | To | Who | Where enforced |
|---|---|---|---|
| (insert) `pending` | — | commerce, service token `payments:intent.create`; key `order:<id>:attempt:<n>` | `PAY/internal/http/handler.go` (map 2.2); idempotent on key |
| `pending`/`processing` | `succeeded` | signed Razorpay webhook, or the reconciler (60 s; candidates pending > 600 s) | `transitionAllowed` (`PAY/internal/store/postgres/p0.go:550-564`) + `VerifyProviderMoney` inside the tx (`:352-390`) |
| `pending`/`processing` | `failed` | **reconciler only**, after the failed-attempt window (15 min) | `PAY/internal/service/p0.go:776-799` |
| `succeeded` | `refunded`/`partially_refunded` | signed refund webhook | `transitionAllowed`; CHECK `refunded + reserved <= amount` (payments 007:191 per map) |
| `succeeded`/`partially_refunded` | `disputed` | **nothing**: no dispute or chargeback event is mapped | — |
| `failed`/`succeeded`/... | `succeeded` again | refused by the state machine; recorded in `refund_required` instead (`:290-330`) | Go, in-tx |

Enforcement is Go only (`transitionAllowed`, in the same transaction as the `FOR UPDATE` on the intent, `:245-262`). There is no DB trigger. Ambiguous `provider_order_id` matches are refused (`:227-240`), because payments gated 999 (provider-reference uniqueness) is not applied by default either.

## Findings

### COM-C-1. Owed captures are recorded but never refunded in any deployed environment

- **Severity:** P0. A buyer is charged, does not get goods, and the money is not returned by any running process or tooled operator path.
- **Category:** money / refunds.
- **Evidence level:** VERIFIED-READ (re-read at the cited lines).
- **Evidence:**
  - **The flags are off.** `PAYMENTS_REFUND_REQUIRED_WORKER` and `PAYMENTS_DUPLICATE_CAPTURE_CONSUMER` default to ON only when `ENV` is `""`, `dev` or `local` (`PAY/internal/config/config.go:143-152,172-190`). HEAD `deploy/services/payments-service/values-prod.yaml:97` sets `ENV: prod` and sets neither flag. QA runs `ENV=prod`; staging is `staging`.
  - **The worker and consumer start only behind those flags** (`PAY/cmd/server/main.go:337-357`).
  - **Cases that land in `payments.refund_required` with no refund:**
    - a capture on an intent already finalised `failed` (`PAY/internal/store/postgres/p0.go:292-296`);
    - a second, distinct capture on a paid intent (`:297-301`);
    - an amount or currency mismatch (`PAY/internal/service/p0.go:144-156` → `RecordMismatchedCapture`).
  - All three log "the refund-required worker returns it when enabled" (`p0.go:314-328`).
  - **Second paid intent on one order:** commerce records the owed money and emits `commerce.order.duplicate_capture` for payments to refund (`P/p0.go:972-1007`). With the consumer off, payments never records it.
  - **No operator path:**
    - The committed payments admin routes list and resolve only `refund_commands`, not `refund_required` (HEAD `PAY/internal/http/admin_routes.go:6-8`).
    - The admin console answers 401 everywhere anyway (map 2.1 `AT`, 2.2).
  - **No alert:** no PrometheusRule in the commerce or payments values (HEAD values-prod has no `prometheusRule` block), and the chart default is `prometheusRule.enabled: false` (`charts/atpost-service/values.yaml:140-142`).
- **How it is reached:** the order's TTL is 20 min (`P/checkout.go:680`). The reconciler fails a quiet intent after `PENDING_AGE_SEC` 600 s plus the 15-min window. A UPI collect approved, or a Razorpay checkout re-opened, after that point captures on a `failed` intent. Nothing closes the Razorpay order (COM-C-6), so this is reachable in normal use.
- **How to reproduce (CI scratch DB `payments_it_test`, ENV=prod config):**
  1. Create an intent and let the reconciler fail it.
  2. Post a signed `payment.captured` for the same provider order.
  3. Observe a `refund_required` row with `resolved=false` that never changes, and no refund call to the provider.
- **Impact:** customer money is held indefinitely, with no automated return, no tooled manual return (only the Razorpay dashboard, outside the ledger) and no page.
- **Fix (smallest):**
  - Set `PAYMENTS_REFUND_REQUIRED_WORKER: 'true'` and `PAYMENTS_DUPLICATE_CAPTURE_CONSUMER: 'true'` in the payments `values-prod.yaml` and `values-qa.yaml` (and staging if checkout runs there).
  - Add one alert on unresolved `refund_required` older than N hours (the gauge or a query).
- **Acceptance:**
  - A prod-shaped config boot logs `refund-required worker ... enabled=true`.
  - The integration tests `refundrequired_rr_*` and `duplicate_capture_m1_*` pass with `ENV=prod` and the flags set.
  - The alert fires on a seeded stale row.
- **Effort:** S (config) plus S (alert).

### COM-C-2. Seller proceeds: no payable ledger, no payout path, and the only earnings derivation reads ₹0

- **Severity:** P1.
- **Category:** payouts / ledger.
- **Evidence level:** VERIFIED-READ.
- **Evidence:**
  - Every capture is to the platform account; the seller is only `PayeeID` on the intent (`S/checkout_p0.go:401-405`).
  - **No seller-payable row is written** at capture (`P/p0.go:312-356` writes stock, status, fulfilment job and outbox only).
  - `CreatePayoutBatch` has no caller (map, `P/seller_payouts.go:183`).
  - Earnings are "a read-time derivation" (`S/seller_account.go:138-142`). They compute from `row.Item.FinalPrice` (`:176,184`), which is the NUMERIC column the P0 checkout writes as 0 (`P/checkout.go:608-609`), using float `round2` maths (`:123-136`).
  - Commission, platform fee and TDS percentages are unset in every environment (map section 7).
  - Payouts and earnings are fenced, so nothing wrong is displayed today, but nothing correct exists either.
- **How to reproduce:** unfence `/seller/earnings` on a dev DB with one paid P0 order; every row shows gross 0.
- **Impact:** sellers cannot be paid from system data. Any payout built on the existing derivation would pay ₹0. Commission and TDS are undefined, which means potential under- or over-payment and TDS/TCS (s.194-O / s.52) non-compliance.
- **Fix (smallest, before any payout):**
  - Write a `seller_payables` ledger row (minor units: gross = `order_items.net_inclusive_minor` summed per seller, commission and TCS as integer basis points) in the capture transaction.
  - Write its reversal in the cancel and refund settlement transactions.
  - Make the earnings derivation read `*_minor`.
- **Acceptance:**
  - After paid → cancel → refund, the ledger sum per seller is 0.
  - After paid → delivered, it equals the stored net.
  - A property test shows no float in the path (the money-type gate covers the new file).
- **Effort:** M.

### COM-C-3. GST invoice numbering and content

- **Severity:** P1.
- **Category:** GST / compliance.
- **Evidence level:** VERIFIED-READ.
- **Evidence:**
  - **One platform-wide series per FY across all sellers.** The format is `PBK/<FY>/<seq6>` (`Architecture/shared/invoice/generator.go:103-105`) on `invoice_sequences` keyed by FY only (`C/database/migrations/002_invoices_shipments.sql:4-8`). Each seller (the supplier, with their own GSTIN, `S/shipments.go:886-905`) therefore gets a non-consecutive series under a platform prefix.
  - **The number is taken outside the invoice transaction.** `NextInvoiceSequence` is its own statement (`P/shipments.go:17-27`). HTML and PDF are rendered and uploaded under that number before `CreateInvoice` (`S/shipments.go:643-681`).
    - A render, upload or insert failure burns the number, which leaves a gap.
    - Two concurrent `IssueInvoice` calls (the fulfilment job, `S/jobs.go:47`, and the seller's `POST /orders/:id/invoice`) both pass the existence check (`:621-623`). Both upload a document, and the second insert fails on `invoices.order_id UNIQUE` (`002:12`). The result is a gap plus an orphaned stored document carrying a real invoice number.
  - **Invoice is issued at payment** (fulfilment job, `S/jobs.go:43-56`). The buyer can still cancel a `confirmed` or `packed` order (matrix `010:105-110`), and the refund goes out, but no credit note is issued: commerce has no credit-note code (repo grep of `C/` finds none).
  - **No place of supply** on the template (`generator.go`: no place-of-supply field; title "Tax Invoice / Bill of Supply" at `:215`).
  - **Buyer name is set to the street line** (`S/shipments.go:788`).
  - **Seller address omits the street** (`:894-903`).
  - **FY is computed from server-local `time.Now()`** (`generator.go:94-100`, `S/shipments.go:637,642`). The pod runs in UTC, so 1 April 00:00-05:29 IST falls in the previous FY.
  - **Books sellers (PAN-only) get the same combined "Tax Invoice / Bill of Supply" document** with no GSTIN. Whether that is acceptable for nil-rated books is an adviser question.
- **How to reproduce:** call `IssueInvoice` twice concurrently for one paid order (CI DB). The `invoice_sequences.last_sequence` delta is 2, one invoice row exists, and two objects are in the bucket.
- **Impact:** invoices that fail Rule 46 (place of supply, recipient name, consecutive series per supplier), un-reversed output tax on cancelled orders, and GSTR-1 mismatches.
- **Fix (smallest):**
  - Take the sequence and insert the invoice in one transaction, upload after commit, and key `invoice_sequences` by `(seller_id, financial_year)`.
  - Add place of supply (from `orders.place_of_supply_state`) and the buyer's contact name from the snapshot.
  - Compute the FY in `Asia/Kolkata`.
  - Issue a credit note from the stored components (`tax.RefundComponents`) when a refund settles on an invoiced order.
  - Have the tax adviser sign off the template.
- **Acceptance:**
  - A concurrency test gives one number per order and no gap.
  - The template golden file shows place of supply and buyer name.
  - Cancelling an invoiced order produces a credit note whose components reverse the invoice exactly.
- **Effort:** M.

### COM-C-4. Gated 999 can never run, and 998 is applied nowhere by default

- **Severity:** P2.
- **Category:** constraints / migrations.
- **Evidence level:** VERIFIED-READ (DB state UNVERIFIED).
- **Evidence:**
  - **999 refuses while `money_minor_drift` has rows** (`gated/999_validate_and_tighten.sql:34-44`). The view lists every order whose `final_amount_minor` differs from `ROUND(final_amount*100)` (`migrations/007_money_minor.sql:160-166`). The P0 checkout writes `final_amount = 0` and `final_amount_minor = total` (`P/checkout.go:543-545,553-554`), so every order is drift, and 999 refuses forever.
  - **Run paths:** the gated runner is not in the image and the Job is disabled (Q1 above).
  - **What is actually lost:**
    - **Without 998:**
      - the transition trigger (the Go re-checks cover every live writer; Q1);
      - the prepaid-only CHECK (Go validates at `H/handler_p0.go:329,413`, `S/checkout_p0.go:275`, `P/checkout.go:218`; the boot CHECK still admits `cod` and others, setup.sql:442);
      - the fence INSERT triggers (fenced in HTTP only).
    - **Without 999:**
      - validation of existing rows only. NOT VALID constraints (`chk_inv_reserved_le_total`, `chk_coupon_uses_within_max`, `chk_item_tax_reconciles`, `chk_item_single_tax_split`) are still enforced on every new insert and update.
      - `*_minor NOT NULL` (the columns default to 0, migration 007).
      - The one-intent unique index (already present as `idx_orders_one_intent`, `008_checkout_integrity.sql:153-154`).
      - The `inventory_ledger_reconciliation` view (`999:95-109`). **This is the only real loss: there is no ledger-vs-stock reconciliation.**
- **How to reproduce:** on any DB with one P0 order, `SELECT count(*) FROM money_minor_drift` > 0, and `psql -f gated/999_*.sql` raises "refusing to validate".
- **Impact:** the documented contract phase cannot complete, the nightly inventory reconciliation never exists, and the runbook's step "run 999 once drift is zero" is unreachable.
- **Fix (smallest):** redefine `money_minor_drift` in a boot migration to exclude rows where the NUMERIC mirror is 0 and `snapshot_cutover` is true, or drop the drift precondition for orders and items. Move the reconciliation view into a boot migration (it is additive). Build `cmd/migrate` into the image.
- **Acceptance:** CI applies 998 then 999 to `commerce_p0_test` after a journey test, and both succeed.
- **Effort:** S.

### COM-C-5. The seller dashboard reports ₹0 revenue

- **Severity:** P2.
- **Category:** money read path.
- **Evidence level:** VERIFIED-READ.
- **Evidence:** `GET /v1/commerce/dashboard` (live, map 2.1) computes `RevenueTotal` as `SUM(oi.final_price)` (`P/onboarding.go:704-707`). That is the NUMERIC column written as 0 (`P/checkout.go:608-609`). The result is also a float (`:678`), and the query error is discarded (`_ =`).
- **How to reproduce:** a seller with a paid order opens the dashboard and sees revenue 0.
- **Impact:** a wrong financial figure is shown to sellers.
- **Fix:** `SUM(oi.net_inclusive_minor)` into `int64` `revenue_total_minor`, with the minor value in the response.
- **Acceptance:** a store test where one paid order of N paise gives N.
- **Effort:** XS.

### COM-C-6. An expired or cancelled order leaves its intent and Razorpay order payable

- **Severity:** P2.
- **Category:** paid path.
- **Evidence level:** VERIFIED-READ.
- **Evidence:**
  - Expiry (`P/p0.go:1313-1350`) and cancel (`:674-818`) never call payments.
  - No cancel or expire intent operation exists in either service (grep for `CancelIntent|expire_by|ExpireBy` finds none).
  - The reservation TTL is 20 min from checkout, not from intent creation (`P/checkout.go:680,707-712`). An intent opened at minute 19 is payable for its whole Razorpay lifetime.
- **Impact:**
  - A payment while the intent is still pending becomes a commerce late-capture refund (charged, then refunded days later).
  - A payment after the reconciler fails the intent falls into COM-C-1.
- **Fix:** have payments set Razorpay `expire_by` (or close the order) on intent creation, aligned to the reservation expiry. Optionally, re-hold the reservation for the TTL when an intent is opened.
- **Acceptance:** the provider order carries an expiry no later than the reservation expiry plus a margin, and a capture attempt after it is refused by the provider.
- **Effort:** S.

### COM-C-7. Money alarms have no alert rule, the DLQ has no reader, and parked commerce refunds have no operator path

- **Severity:** P2.
- **Category:** events / ops.
- **Evidence level:** VERIFIED-READ.
- **Evidence:**
  - **Poison payment events** (amount mismatch, unknown order) go to `social.events.v1.dlq` (`C/internal/consumers/payments_p0.go:390-410`). No consumer of that topic exists outside the producers' own config (repo grep), and no alert is defined.
  - **Commerce refund commands parked `needs_attention`** (`P/p0.go:1200-1219`) have no route in commerce or admin-service (grep).
  - **Gauges exist but nothing pages on them:** `OldestUnsettledRefundAge`, `OldestLiveReservationAge` (`P/p0.go:1224-1236,1352-1366`).
  - Neither the commerce nor the payments values enable `prometheusRule` (chart default off, `values.yaml:140-142`).
- **Impact:** an amount mismatch (which needs a human, per its own comment) or a stuck refund is seen only by someone reading logs.
- **Fix:** enable `prometheusRule` for commerce and payments with rules for DLQ lag or growth, `needs_attention > 0`, refund age > 24 h, reservation age > 40 min and amount-mismatch count > 0. Add a read-only operator list for commerce `needs_attention` commands behind the admin token.
- **Acceptance:** `charts/atpost-service/render_test.sh` renders the rules for both services, and a seeded parked command fires the alert in a test.
- **Effort:** S.

### COM-C-8. A cancel can race the fulfilment job's courier booking

- **Severity:** P2.
- **Category:** concurrency.
- **Evidence level:** SUSPECTED.
- **Evidence:**
  - The fulfilment job reads the order outside any lock, checks `payment_status == 'paid'` and books the courier (`S/shipments.go:104-125`, called from `S/jobs.go:51-55`).
  - `CancelOrder` takes `FOR UPDATE` on the order (`P/p0.go:692-696`), but the booking path takes no lock on it.
  - Interleaving: job reads `paid`, then the buyer's cancel commits (refund command written, stock restocked, `:732-805`), then the job books an AWB.
  - The later `→ shipped` transition is refused by the matrix (`cancelled → shipped` is not a row), so the order stays cancelled with a live shipment.
- **Impact:** a courier booking (and cost) on a refunded order, and possibly goods dispatched after the refund, plus restocked units that are physically shipped.
- **Fix:** book under `SELECT ... FOR UPDATE` on the order, re-checking status and payment_status before the courier call is recorded, or refuse cancel once a `fulfillment_jobs` row is `running`.
- **Acceptance:** an integration test with a barrier between the job's read and its booking ends with either cancel refused or no shipment.
- **Effort:** S.

### COM-C-9. `COMMERCE_SKIP_BOOT_MIGRATIONS` is read by no code

- **Severity:** P3.
- **Category:** migrations.
- **Evidence level:** VERIFIED-READ.
- **Evidence:**
  - Staging sets it (HEAD `deploy/services/commerce-service/values-staging.yaml:86`), and the migrate command's comment relies on it (`C/cmd/migrate/main.go:22-23`).
  - No Go file reads it: the only occurrence in `C/` is that comment. The server always runs `BootstrapSchema` (`C/cmd/server/main.go:112`).
- **Impact:** the planned "Job migrates, pods skip" split will not happen when someone flips the documented switch. It is harmless today because `RunExclusive` serialises.
- **Fix:** read the flag in `main.go` and skip `BootstrapSchema` when it is true, or delete it from the values and comments.
- **Acceptance:** a unit test of the boot-config branch.
- **Effort:** XS.

### COM-C-10. Legacy float and NUMERIC money paths remain reachable

- **Severity:** P3.
- **Category:** money types.
- **Evidence level:** VERIFIED-READ.
- **Evidence:**
  - COD remittances are float and NUMERIC (`S/shipments.go:435-461`, `cod_remittances` NUMERIC-only per map). The IK route `cod-remittances/:id/settle` is live in-cluster (map 2.1), though no COD order can be created.
  - `UpdateOrderStatus` writes status with no matrix check (`P/orders.go:359-381`), and its callers discard the error (`S/returns.go:93,134,298`); those routes are fenced.
  - `invoices` totals are NUMERIC-only, converted from paise for storage and back with `math.Round` for JSON (`P/shipments.go:62-73`). This is lossless for 2-decimal values.
- **Fix:** delete the COD remittance writer and the settle route until COD is scoped. Route `UpdateOrderStatus` through `transitionOrderStatusTx` before returns are unfenced.
- **Effort:** S.

## What holds (VERIFIED-READ unless stated)

**Server-side pricing.**
- Checkout reads price, MRP and tax class from the DB under `FOR UPDATE OF v, p` (`P/checkout.go:857-907`).
- It refuses non-live, unapproved or suspended-seller listings (`:889-917`) and multi-seller carts (`:921-928`).
- It compares the client's `expected_total_minor` only (`:437-439,518-520`).
- The quote is bound to cart version, items hash, address id, address content fingerprint, pincode, coupon, payment method and seller (`:350-428`).
- Shipping comes from the courier, and zero is refused (`S/checkout_p0.go:156-161`).

**Integer money.**
- `tax.Compute` is total-preserving, with in-function invariant checks (`C/internal/tax/gst.go:255-265`).
- Taxable is floor and tax is the remainder (`:278-288`).
- Discount and shipping use largest-remainder allocation (`:303-367`).
- CGST/SGST split is floor plus remainder (`:239-240`).
- The coupon discount uses integer basis points, capped at the base (`P/coupons.go:144-176`).
- VERIFIED-RUN: tax unit tests pass.

**Order creation idempotency.**
- Advisory lock on `(user, key)`, then lookup, then fingerprint compare giving 409 (`P/checkout.go:261-287`).
- Unique `(customer_user_id, idempotency_key)` (`migrations/006:7-9`).

**Inventory.**
- Locked in ascending variant order (`P/checkout.go:1006-1041`).
- Reserved with `reserved_qty + n` under `CHECK (reserved_qty <= total_qty)`. NOT VALID, but enforced on writes (`009:88-92`); `chk_inv_qty` keeps both quantities non-negative (setup.sql:267).
- `inventory_items.variant_id UNIQUE` (setup.sql:258).
- Commit, release and restock run only once per reservation (`P/p0.go:551-658,1076-1125`).
- Expiry, payment failure and unpaid cancel release in the same transaction as the state change.

**Payment application.**
- The inbox row and the effect commit in one transaction (`P/p0.go:110-161`).
- The capture tuple is checked against the order (`:227-240`).
- An intent already bound to another order is refused (`:940-961`).
- Redelivery, duplicate and late captures are distinguished (`:877-902`).
- A retry is refused once a capture landed (`P/payment_retry.go:47-49,90-92`).
- Payments verifies amount and currency inside the webhook transaction and rolls back the inbox row on mismatch (`PAY/internal/store/postgres/p0.go:352-390`).

**Refund ledger.**
- Cancel refunds total minus settled minus in-flight (`P/p0.go:1036-1058`) under one key per order (`:796-797`).
- Settlement is deduped by event and provider refund id.
- Over-refund is refused (`P/refund_settlement.go:243-266`).
- The payments CHECK holds `refunded + reserved <= amount` (map).
- Refund commands are claimed `SKIP LOCKED` with a deterministic key (`P/p0.go:1160-1187`, `S/checkout_p0.go:680-688`).

**Coupons.**
- Capacity is claimed by a conditional `UPDATE ... WHERE uses_count < max_uses` (`P/coupons.go:424-441`).
- The per-user count is read after the coupon row lock, so checkout and retry (`FOR UPDATE`, `P/coupon_release.go:86-88`) serialise on the coupon row.
- Release happens exactly once via `DELETE ... RETURNING` (`:49-61`).
- Expiry and start are checked in SQL (`P/coupons.go:210-215`).
- Recorded discount equals applied discount: the same `computed.OrderDiscount` is written (`P/checkout.go:554`).
- Stacking: one coupon per order. Bank offers are off.

**Outbox.** Every order event is enqueued in the same transaction as its write (`P/checkout.go:653-665`, `P/p0.go:303,347,453,807`).

**Soft delete.** Only `customer_addresses` has `deleted_at`, and checkout excludes it (`P/checkout.go:328`). Products use status, and checkout rechecks status.

## Not verified

- Whether any live database (dev, qa, prod) had 998, 999 or 1000-1002 run by hand. No DB access. UNVERIFIED.
- `money_minor_drift` count on dev. UNVERIFIED (inferred to be non-zero, COM-C-4).
- Integration suites (`commerce_it_test`, `payments_it_test`, proofs). Not run; CI-only. UNVERIFIED.
- Concurrency claims (oversell, coupon race, COM-C-8) were reasoned from SQL, not load-tested. No load test exists.
- **payments internals not read in full:** refund command creation and over-refund reservation (`PAY/.../p0.go:633-834`), offer proration, outbox relay, and the `provider_events` replay window (Track B). UNVERIFIED.
- Courier webhook to status mapping, and Shiprocket rate float to paise conversion (`courier/shiprocket.go:392-394`). UNVERIFIED.
- `sellerPlaceOfSupply` source column (pickup state vs GST-registered state). UNVERIFIED.
- Rollback compatibility beyond the CI expand-only gate (`integration-commerce.yml:856+`). Not exercised.
- Android and web money handling. Out of track.

## Notes for other tracks

- **Track B (security):**
  - commerce accepts `payment.succeeded` from the shared `social.events.v1` with no producer check. A blank currency, nil payer or blank intent is "not a mismatch" (`Architecture/shared/paymentevents/check.go:63-92`).
  - Any UUID intent not bound elsewhere is bound (`P/p0.go:918-961`).
  - So a Kafka writer with an order id and its exact total confirms the order without any payments-service record. Recommend topic ACLs, or commerce verifying the intent with payments (`GetIntent`) before binding.
- **Track A / product:**
  - The only buyer refund is cancel before shipment; no route performs `delivered → refund_pending`.
  - Nothing sets `disputed`.
  - The invoice is issued at payment, before the cancel window closes (COM-C-3).
- **Ops track:** COM-C-1 and COM-C-7 are config and alerting, and should land before live Razorpay keys. `PAYMENTS_RECONCILE_INTERVAL_SEC`/`PENDING_AGE_SEC` interact with the 20-min reservation TTL (COM-C-6).
- **Tax adviser:**
  - shipping is taxed at each line's rate via allocation (`gst.go:202-217`);
  - bank-offer discount is a note only (`S/shipments.go:768-775`);
  - platform-coupon treatment is pending (`P/checkout.go:572-581`);
  - books PAN-only sellers get a "Tax Invoice / Bill of Supply" with no GSTIN;
  - TCS under s.52 has no implementation.
