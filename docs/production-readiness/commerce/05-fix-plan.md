# Commerce (MStore / MSeller): fix plan

- **Audit date:** 2026-11-17. Code: `codex/module-01-02-launch-safety` @ `be57a46bf`. Web: `C:\workspace\wt\pb-echo`, `feat/echo-web` @ `1b596b4`.
- **Scope:** the three P0s and nineteen P1s in `03-findings.md` (com-1 to com-22), in the order to fix them, then a short list of the P2s to take next. IDs, evidence and acceptance criteria are in `03-findings.md`; this file adds the order, the dependencies, the smallest fix and the test that proves it.
- **Each item:** dependencies, the smallest fix, and a test that **fails now and passes after**. For the test-gap items (com-18 to com-21) "fails now" means the CI check does not exist or does not run; their proof is a recorded mutation (remove the guarded code, see the new test go red). Run `scripts/verify/go.sh --fast <module-dir>` while working and `--full` before reporting; Android `scripts/verify/android.sh --fast :module` while working and `--full` once at the end. Integration tests run in CI only, on `_test` databases (`commerce_it_test`, `commerce_p0_test`, `payments_it_test`); never `commerce_db`.
- **Markers:** **[FOUNDER]** needs a founder (or adviser) decision before or during the work. **[OUTSIDE]** needs an action outside the repository (a secret, a dashboard setting, a contract, Terraform apply, a one-off production Job). Everything else is a code or values change an agent can make.
- **Not in this plan:** inherited platform items (feed-02 environment and image, reels-6 NetworkPolicy enforcement, feed-42 shutdown order, reels-63 base image) are fixed once in their own plans; they are named below only where an item depends on them.

## Founder decisions and outside actions, collected

| Item | Decision or action | Blocks |
|---|---|---|
| com-1 | **[FOUNDER]** Auto-book the courier at payment, or seller-driven shipping (recommended for launch: seller presses Ship) | step 4 |
| com-5 | **[FOUNDER]** Shiprocket at launch, or a named manual-shipping mode with a documented flat fee. **[OUTSIDE]** Shiprocket contract, credentials and webhook secret in the prod secret store | step 5, com-7, com-10's live check |
| com-22 | **[OUTSIDE]** Read the prod Razorpay dashboard's payment-capture setting; one test-mode payment on qa | step 6 severity (P0 if manual capture) |
| com-4 | **[OUTSIDE]** Per-service Kafka users and ACLs, or a dedicated payments topic (Terraform, MSK) | step 7, second half |
| com-8 | **[OUTSIDE]** Seed admin-service's signing key and add its caller public key to commerce's and payments' ExternalSecrets | steps 8, 9, 10, 11 |
| com-7 | **[FOUNDER]** A KYC vendor, or manual verification with an audited override recording that the reviewer viewed the documents | step 10 |
| com-6 | **[FOUNDER]** Launch refund policy beyond cancel (who may refund after shipping, the two-person threshold) | step 11 |
| com-12 | **[FOUNDER]** + adviser: the invoice template signed off by the tax adviser, books (PAN-only) sellers included | step 13 |
| com-11 | **[FOUNDER]** Merchant of record, commission, TCS (s.52) and TDS (s.194-O) handling, payout rail. **[OUTSIDE]** Razorpay Route or RazorpayX account | step 14 |
| com-13 | **[FOUNDER]** + legal: the retention class and period for orders, invoices and GST records | step 15 |
| com-14 | **[OUTSIDE]** One-off Job applying gated 998/999 and the two payments gated files to the fresh prod database before the shop opens | step 16 |
| com-15 | **[OUTSIDE]** Confirm the ops SNS e-mail subscription (feed-10) | step 17's last check |
| com-17 | **[OUTSIDE]** A QA stack sized like production (feed-02) | step 22 |

## P0 and P1, in fix order

### 1. com-21: run the commerce P0 gate on this branch

- **Depends on:** nothing. Do it first: every later "test passes" needs the gated-998 proofs, the money-type gate and the C1 oversell proof to actually run, and they last ran on 31 Oct, 19 commerce and payments commits ago.
- **Smallest fix:** add `push` on `codex/**` and `feat/**` with the existing paths filter to `.github/workflows/integration-commerce.yml` (as `integration-all.yml:21-26` does), or fold its commerce-proofs job into integration-all as a gated-998 extra database.
- **Test:** `gh run list --workflow integration-commerce.yml` shows a run for the current HEAD (none now). Mutation: a commit that breaks `TestProofIllegalOrderTransitionRejected` turns the branch red.

### 2. com-2 (P0): turn on the payments refund-required worker and duplicate-capture consumer

- **Depends on:** item 1 for CI proof. Nothing else; the code exists and is tested in dev mode.
- **Smallest fix:** set `PAYMENTS_REFUND_REQUIRED_WORKER: "true"` and `PAYMENTS_DUPLICATE_CAPTURE_CONSUMER: "true"` in `deploy/services/payments-service/values-prod.yaml` and `values-qa.yaml` (and staging if it ever runs checkout). Add a gauge for unresolved `refund_required` rows older than 1 h and an alert rule for it (the first rule of item 17).
- **Test:** a values or `render_test.sh` assertion that both flags render `"true"` for prod and qa (fails now: unset, so off). `refundrequired_rr_*` and `duplicate_capture_m1_*` run with `ENV=prod` and the flags set and pass. On qa: let the reconciler fail an intent, post a signed `payment.captured` for it; the `refund_required` row ends resolved by the refund webhook (now it stays `resolved=false`).

### 3. com-3 (P0): verify ownership of a variant image

- **Depends on:** nothing.
- **Smallest fix:** in `UpdateProductVariant` (`S/catalogue_products.go:525-556`), when `image_media_id` is in the patch, call `s.verifyMedia(ctx, actorID, media.KindImage, &id)` before the store write, as `PATCH /products` does for `primary_image_media_id`. Apply the same rule to any variant-create path that accepts an image.
- **Test:** an HTTP integration test: `PATCH /variants/:id` with a media id owned by another user answers 403 `NOT_YOUR_MEDIA` and the variant row is unchanged (fails now: 200 and written); the seller's own image still succeeds.

### 4. com-1 (P0): stop shipping every order one second after payment **[FOUNDER]**

- **Depends on:** the founder's choice between auto-booking and seller-driven shipping. Pair with com-38 (P2), whose race window moves to wherever booking ends up.
- **Smallest fix (seller-driven):** remove the `CreateShipmentForOrder` call from `FulfillPaidOrderJob` (`S/jobs.go:43-56`), keeping invoice issue; booking happens on `POST /seller/orders/:id/ship`, already wired on both clients. **Auto-book variant:** book on `packed`, and leave the order `packed` until the courier's first pickup event instead of `MarkOrderShipped` at booking. Either way the buyer is told "shipped" only when it is true.
- **Test:** a service test: after `FulfillPaidOrderJob` a paid order is `confirmed` with no shipment row (fails now: `shipped` with a `STUB…` AWB). An integration test: the buyer cancels a paid order before the seller ships; one `order_refund_commands` row is written and the order reaches `refund_pending` (fails now: 409 `CANCEL_NOT_PERMITTED`).

### 5. com-5: refuse the stub courier in production **[FOUNDER] [OUTSIDE]**

- **Depends on:** the founder's choice of Shiprocket or a named manual-shipping mode; the Shiprocket contract and secrets if chosen. Item 4 first, so manual mode does not leave orders stuck in `shipped`.
- **Smallest fix:** in `courier.New` / `C/cmd/server/main.go`, refuse `stub` when `ENV` is managed unless `COMMERCE_MANUAL_SHIPPING=true`; in manual mode the shipping fee is a documented configured value and fulfilment skips booking (the seller ships with their own courier and marks delivered, com-27). Refuse `shiprocket` with empty credentials or no webhook token/HMAC. Log `courier provider=<name>` at boot. Correct the false comments at `courier.go:167-187` and `values-prod.yaml:10-15`. Change `tools/prodsecrets/manifest.yaml:613-616` so prod is not seeded `const:stub`.
- **Test:** a boot test: `ENV=prod COURIER_PROVIDER=stub` exits non-zero (fails now: boots silently); with `COMMERCE_MANUAL_SHIPPING=true` it boots and a paid order has no shipment row; `ENV=prod COURIER_PROVIDER=shiprocket` with no webhook secret exits. A manifest contract test: prod `courier_provider` is not `stub`.

### 6. com-22: send `payment_capture: 1` **[OUTSIDE]**

- **Depends on:** nothing for the code; the dashboard check decides whether this was a live P0.
- **Smallest fix:** add `"payment_capture": 1` to the order body in `PAY/internal/gateway/razorpay_provider.go:128-145`. Outside the repo: read the prod Razorpay account's capture setting, and make one test-mode payment on qa that reaches `payment.captured` with no manual capture call.
- **Test:** a gateway test asserting the create-order request body carries `payment_capture: 1` (fails now).

### 7. com-4: commerce stops trusting an unverified `payment.succeeded` **[OUTSIDE] for the second half**

- **Depends on:** nothing for the code. The Kafka ACL half is Terraform.
- **Smallest fix:** in the commerce payments consumer, use a strict policy for `payment.succeeded` (blank currency, nil payer or blank intent id is a mismatch) instead of `AllowUnstated` (`P/p0.go:214-230`), and before confirming call payments `GET /internal/intents/:id` (op `intent.read`, already granted) and require `succeeded` and an equal amount. Later, outside the repo: per-service Kafka users with write ACLs on payment event types, or a dedicated payments topic (com-63).
- **Test:** an integration test: a `payment.succeeded` with blank currency, nil payer or an unknown intent id leaves the order `payment_pending` (fails now: `confirmed`). A forged event naming a real but unpaid intent is refused by the intent check.

### 8. com-8: give the admin console a working path to commerce **[OUTSIDE]**

- **Depends on:** the signing key and caller public keys in the secret store (outside the repo).
- **Smallest fix:** in the documented Wave 2 order: seed admin-service's signing key and add its caller public key to commerce's and payments' ExternalSecrets; uncomment `SERVICE_CALLERS` and `SERVICE_CALLER_ADMIN_SERVICE_OPS` in each commerce values file; then uncomment `ADMIN_SERVICE_TOKEN_KEY` in admin-service (`values-prod.yaml:47-61,93-94`). Do the same for payments so the operator screens of com-2 and com-15 work.
- **Test:** a render test that commerce prod and qa set `SERVICE_CALLERS` including `admin-service` (fails now: commented out). On qa the console approves a seller and a product and `commerce_admin_audit_log` has both rows (now: 503).

### 9. com-9: remove money and identity actions from the internal-key group

- **Depends on:** item 8 (the console must use the admin token first). The NetworkPolicy half needs reels-6 for enforcement.
- **Smallest fix:** move sellers approve, reject, request-changes, suspend, unsuspend, `kyc/verify`, products approve and reject, and `cod-remittances/:id/settle` out of the IK group (`H/handler_onboarding.go:36-67`, `H/handler_seller.go:106-113`) so they are reachable only with the admin token; leave service reads (search docs, media-access). Add a commerce NetworkPolicy (gateway, admin-service, search-service, media-service). Add step-up to seller and product approve in admin-service.
- **Test:** a key-only `POST /internal/sellers/:id/approve` answers 404 or 401 (fails now: 200 with an invented actor). The console's approve still works through the admin token. A chart render test shows the commerce NetworkPolicy.

### 10. com-7: no seller goes live on unverified identity **[FOUNDER]**

- **Depends on:** item 8 (reviewers must be able to see the documents), item 5 (no stub in prod), item 11 (a refund path for shipped and delivered orders). The KYC decision.
- **Smallest fix:** approve refuses unless `verification_status = 'verified'`, or an explicit admin override records (audited) that the reviewer viewed the documents (`P/onboarding.go:337-361`, `S/onboarding.go:341-361`). Replace the unconditional `kyc.StubValidator{}` (`C/cmd/server/main.go:358`) with the chosen vendor, or keep it and rely on the audited override.
- **Test:** approve of a `format_ok` seller is refused (fails now: approved); approve with the override writes one audit row naming the reviewer; com-5's boot test; item 11's refund test.

### 11. com-6: an admin refund route for shipped and delivered orders **[FOUNDER]**

- **Depends on:** item 8 (admin token path); the refund-policy decision.
- **Smallest fix:** `POST /internal/admin/orders/:id/refund {amount_minor, reason}` behind the admin token (permission `commerce:refunds.issue`, step-up, two-person above a threshold) that writes a scoped `order_refund_commands` row for at most the unrefunded balance and moves `shipped|delivered → refund_pending` as `admin`; surface it in admin-service. Map Razorpay `payment.dispute.*` to `disputed` later.
- **Test:** integration tests: a full and a partial admin refund of a delivered order each create one refund command, never exceed the captured amount, and settle to `refunded` / `partially_refunded` (fails now: no route).

### 12. com-10: declare the real package value to the courier

- **Depends on:** nothing; matters the day Shiprocket is on (item 5).
- **Smallest fix:** in `CreateShipmentsForOrder` (`S/shipments.go:239-242`), use `it.FinalPriceMinor` and `it.UnitPriceMinor`, converted to rupees at the adapter edge; same in `recordCODRemittance` before COD is unfenced.
- **Test:** a unit test with a fake courier: the request's `PackageValue` equals the seller's lines' `final_price_minor/100` for a P0 order (fails now: 0). One Shiprocket sandbox booking reads back the right declared value **[OUTSIDE]**.

### 13. com-12: GST-correct invoices **[FOUNDER]** (adviser sign-off)

- **Depends on:** the adviser's template sign-off; item 4 (invoice at payment, booking separate).
- **Smallest fix:** take the sequence and insert the invoice in one transaction and upload after commit (`S/shipments.go:640-681`, `P/shipments.go:17-27`); key `invoice_sequences` by `(seller_id, financial_year)`; render place of supply (`orders.place_of_supply_state`) and the snapshot's contact name as the buyer (`S/shipments.go:788`); add the seller's street; compute FY in `Asia/Kolkata`; issue a credit note from `tax.RefundComponents` when a refund settles on an invoiced order.
- **Test:** `TestGoodsInvoiceCarriesRule46Fields` (supplier GSTIN, place of supply with state code, buyer name, number format) fails now. Twenty concurrent `IssueInvoice` calls give twenty distinct consecutive numbers per seller and FY (fails now: gaps and a unique-violation). Cancelling an invoiced order produces a credit note that reverses the invoice exactly.

### 14. com-11: a seller payable ledger **[FOUNDER] [OUTSIDE]**

- **Depends on:** the merchant-of-record, commission, TCS/TDS and payout-rail decisions; the payout account (outside the repo). Earnings and payouts stay fenced until done.
- **Smallest fix:** a `seller_payables` table in minor units, written in the capture transaction (gross from `order_items.net_inclusive_minor` per seller, commission and TCS as integer basis points), reversed in cancel and refund settlement; earnings read `*_minor` instead of `FinalPrice` with float `round2` (`S/seller_account.go:122-136,176,184`).
- **Test:** after paid → cancel → refund the per-seller ledger sum is 0; after paid → delivered it equals the stored net (fails now: no ledger). The money-type gate (item 1) covers the new file.

### 15. com-13: commerce and payments join account deletion and export **[FOUNDER]** (retention class)

- **Depends on:** the retention decision (CGST s.36 / Rule 56 requires roughly 72 months for tax records; DPDP s.8(7) permits legally required retention, so this is a partial purge).
- **Smallest fix:** a commerce purge consumer: delete addresses, cart, favourites, reactions; anonymise reviews; crypto-shred seller PII and KYC once no open order or settlement needs them; keep orders, invoices and GST records under the retention class with the user link reduced to an id; ack. Add `commerce` and `payments` to `DefaultRequiredServices` (`identity-platform/services/auth-service/internal/purge/worker.go:46`). A JSON export of orders, addresses and reviews.
- **Test:** an integration test purges a buyer and a seller and asserts the per-table result; the auth purge waits for the commerce ack (fails now: commerce is not a required service).

### 16. com-14: the gated contract migrations can be applied **[OUTSIDE]** (the one-off prod run)

- **Depends on:** item 1 (CI applies them); before the shop opens.
- **Smallest fix:** rename the commerce gated files to sort correctly (`0998_`, `0999_`) or sort numerically in `SH/store/migrationrunner/runner.go:132`; let `-gated` take an explicit list so the PII scrubs run separately; redefine `money_minor_drift` in a boot migration to exclude P0 rows whose NUMERIC mirror is 0, and move the inventory reconciliation view to a boot migration; build `/commerce-migrate` into the image; a runbook. Outside the repo: a one-off Job using the service's ExternalSecret runs 998, 999 and the two payments gated files on the fresh prod database.
- **Test:** a unit test that `listMigrationFiles` on `C/database/gated` returns 998 before 999 before 1000 (fails now). CI applies 998 then 999 to `commerce_p0_test` after a journey test and both succeed (fails now on drift). On qa, `\d orders` shows `trg_order_transition`.

### 17. com-15: alerts and the dead metrics **[OUTSIDE]** (SNS subscription)

- **Depends on:** item 2 (its alert is the first rule), item 8 (the operator list is behind the admin token).
- **Smallest fix:** a `prometheusRule` block in commerce and payments prod and qa values: amount mismatch > 0 (critical), refund pending age > 1 h, reservation age > 40 min, outbox unpublished > 100 for 10 m, consumer lag > 1000 for 10 m, DLQ growth, `payments_refunds_needs_attention > 0`, unresolved `refund_required` > 1 h, a 5xx ratio scoped to checkout and payment routes. Increment `CheckoutTotal`, `CheckoutDuration`, `OversellAttempt`, `RefundInitiateFailed`, `OrderTransitionRejected`, `QuoteRejected`; add `payments_webhook_total{result}`. A read-only operator list of commerce `needs_attention` refund commands.
- **Test:** `helm template` for commerce and payments prod renders the rules (fails now: none). A unit test per dead metric shows it increments (fails now). On qa a forced mismatch produces an e-mail.

### 18. com-16: database timeouts

- **Depends on:** nothing.
- **Smallest fix:** `statement_timeout = 5s`, `lock_timeout = 2s`, `idle_in_transaction_session_timeout = 15s` in the pool's `RuntimeParams` (`C/cmd/server/main.go:66-78`); map `lock_not_available` / `query_canceled` on checkout to a retryable 503 `CHECKOUT_BUSY`. Platform-wide: `ResponseHeaderTimeout` on the gateway transport.
- **Test:** a unit test that the pool config carries the three parameters (fails now). An integration test holding the product row `FOR UPDATE` while 30 checkouts and one browse hit one instance: checkouts fail fast with 503 and the browse completes (fails now: the browse waits).

### 19. com-20: object-id isolation tests

- **Depends on:** item 1. Folds in com-68 (seller detail 403 → 404) as one more row.
- **Smallest fix:** one table-driven HTTP integration test over every `/orders/:id/*` and `/seller/orders/:id*` route with five callers (owner buyer, owning seller, another buyer, another seller, a user with no seller row): status, no `download_url` or address in a refused body, no invoice row from a refused POST.
- **Test:** the new test passes; mutations: removing `requireOrderRole` from `GetInvoice`, or the seller filter at `S/orders_read.go:295-297`, turns it red (record both).

### 20. com-19: courier webhook verifier tests

- **Depends on:** item 5 (the boot test lives there).
- **Smallest fix:** `C/internal/courier/shiprocket_webhook_test.go`: no secrets → error; token only (right token in `X-Api-Key`, `Token`, `X-Token`, mixed case) passes, wrong or missing fails; HMAC only, right signature passes, altered body fails. A route test: bad token → 401 and no `shipment_events` row; a courier name not matching the configured one is refused.
- **Test:** each test goes red when its branch of `VerifyWebhook` is removed (record the mutations).

### 21. com-18: a signed-webhook end-to-end test

- **Depends on:** item 1; best after items 2 and 7 so the e2e also proves their behaviour. Includes com-64's refund follow-through.
- **Smallest fix:** an e2e commerce variant with payments in `razorpay` mode against an `httptest` fake of the Razorpay API and a test webhook secret: `TestE2E_Commerce_SignedWebhookPays` (checkout, intent, signed `payment.captured`, poll until `paid` with no client confirm); the same body under a new event id gives one `commerce.order.paid`; a body 1 paisa short leaves `payment_pending` with an owed capture; a bad signature answers 401; cancel a paid order and poll until `refunded` with one provider refund call.
- **Test:** green in CI; red if the commerce payments consumer is not started or the amount comparison is removed from `VerifyProviderMoney` (record).

### 22. com-17: load tests **[OUTSIDE]** (production-sized QA)

- **Depends on:** feed-02 (an environment) and most of the above, so the numbers describe the launch code.
- **Smallest fix:** k6 scenarios L1 to L9 from Track D section 6 under `Architecture/tools/loadtest/commerce/` using `assertSafeTarget`, with Razorpay and Shiprocket fakes; extend the dev smoke to checkout and intent, stopping before payment.
- **Test:** a dated results file in this folder with p50/p95/p99, error rate, CPU, statements per second, lock waits, consumer lag and queue ages per scenario, meeting the Track D thresholds (none exists now).

## P2, next (grouped by who feels it)

**Buyers' money and orders**
- com-37 and com-69: give the Razorpay order an expiry aligned to the hold, and extend or refuse a late intent.
- com-39: the expiry sweep asks payments before expiring a captured order.
- com-38: book under a lock on the order (follows item 4).
- com-41, com-42: a tracking poller and a forward-only shipment state guard.
- com-40: fulfilment jobs retry for 24 h, with a dead-job gauge and requeue.
- com-25: notify sellers of cancels and buyers of late-capture and partial refunds.

**Configuration and operations**
- com-23: payments refuses a Razorpay key whose mode does not match `ENV`; fix the values comment.
- com-32: launch prod at `ciphertext`; stop storing cancelled-cheque numbers.
- com-46: delete or complete the staging and Azure overlays.
- com-43, com-44: Redis out of readiness; 503 `PAYMENTS_UNAVAILABLE`.
- com-45: one log line per accepted webhook and request-id forwarding.
- com-47: a commerce section in the restore runbook.
- com-35: narrow the payments NetworkPolicy, add commerce's, scope a WAF rule to the webhook.

**Security hardening**
- com-31: audit and re-verify payout and identity changes on approved sellers.
- com-33: ids only in order and invoice events.
- com-34: caps on open orders, cart lines and quantity; per-route limits.
- com-36: dedupe captures on the signed payment id.
- com-30: scope invoice and shipment reads per seller before multi-seller checkout.

**Sellers and parity**
- com-26: the dashboard reads `*_minor` (₹0 revenue today).
- com-27, com-28: "mark delivered" on both clients; hide the fenced Earnings and Returns tiles.
- com-24, com-29: Android buyer and seller parity.

**Scale (after com-17 gives numbers)**
- com-48: cache and break the Shiprocket serviceability call (P1 the day Shiprocket is on).
- com-49, com-50, com-51, com-52, com-53: image authorisation union and indexes; `FOR SHARE` on the product row; cached home; the live predicate that lets the existing indexes qualify; search through search-service.
- com-54, com-55, com-56: cap public offsets and review limits; keyset seller lists.
- com-57, com-58, com-59, com-60, com-63: timeouts, a claimed reconciler, a parallel expiry sweep, concurrent fulfilment drainers, a dedicated payments topic.
- com-61, com-62: stable image cache keys; thumbnails in the web grid.

**Tests**
- com-64 (commerce refund worker), com-65 (web contract fixtures fail instead of skip), com-66 (every `orders.status` writer goes through the matrix).
