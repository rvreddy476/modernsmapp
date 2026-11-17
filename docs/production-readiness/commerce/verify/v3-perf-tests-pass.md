# Commerce verify pass V3: performance, reliability, tests, launch-gate PASS candidates

- **Date:** 2026-11-17. Read-only. Tree `C:\workspace\modernsmapp` @ `be57a46bf` (branch `codex/module-01-02-launch-safety`). Committed code only: the relevant directories were exported with `git archive HEAD` to a scratch folder, and dirty files were read with `git show HEAD:`. Web `C:\workspace\wt\pb-echo` @ `1b596b4` (clean).
- **Path shorthand** (as in the tracks): `C/` = `Architecture/services/commerce-service/`, `P/` = `C/internal/store/postgres/`, `S/` = `C/internal/service/`, `H/` = `C/internal/http/`, `PAY/` = `Architecture/services/payments-service/`, `MS/` = `Architecture/services/media-service/`.
- **Run:**
  - `go test ./internal/courier/ ./internal/consumers/ ./internal/obs/ -count=1` in commerce-service: ok.
  - `gh run list` for `integration-commerce.yml`, `integration-all.yml` and the branch.
  - `gh pr list --head codex/module-01-02-launch-safety`.
  - Nothing else was executed: no Docker, no database, no device, no deployed environment, no secret store.
- **Method:** for each finding I looked for what would make it false: a later migration, a cache, a pool, DSN or role timeout, a breaker, a load test, a recent run, or a test with another name.
- No secret value appears below.

## Part 1: re-check of track findings

| ID | Verdict | Evidence level | Checked (file:line) | Corrected severity |
|---|---|---|---|---|
| COM-D-1 | DOWNGRADE-TO-SUSPECTED | VERIFIED-READ for the facts; SUSPECTED for latency | `P/storefront.go:777-793` (OR-join with correlated EXISTS). A full index grep of `C/database/**` finds no index on `product_media.media_id`, `products.primary_image_media_id` or `product_variants.image_media_id`. `MS/cmd/server/main.go:537-543` (five authorities, commerce fourth). `MS/internal/delivery/authz.go:437-492` (sequential; any unresolved id fails the batch). `authz.go:511-512` says "Authorization is never cached". `C/internal/media/media.go` has no cache. | P2. The facts hold. "Past about 10,000 products it exceeds 3 s" is arithmetic with no EXPLAIN, and the launch catalogue is small. |
| COM-D-2 | DOWNGRADE-TO-SUSPECTED | VERIFIED-READ for the locks; SUSPECTED for the ceiling | `P/checkout.go:859-864` (`FOR UPDATE OF v, p`), `P/quote.go:288-318` (the quote takes the same locks and always rolls back), `P/checkout.go:1006-1039` (inventory `FOR UPDATE`), `P/coupons.go:420-430` (conditional coupon claim). The courier call happens before the transaction (`S/checkout_p0.go:118-150`), so no lock is held across the network. | P2. "About 100 checkouts/s per product" is arithmetic, not measured. |
| COM-D-3 | CONFIRMED (stronger than the track) | VERIFIED-READ | No timeout is set at any level: <br>• Pool: `C/cmd/server/main.go:66-78` sets MaxConns 25, MinConns 5 and lifetimes, but no RuntimeParams or AfterConnect. <br>• Prod DSN template: `tools/prodsecrets/resolve.go:573-597` gives `?sslmode=require` only. <br>• Aurora parameter group: `infra/terraform/modules/aurora/main.tf:113-150` sets logging only. <br>• Repo-wide `git grep statement_timeout` matches only dating and media files. <br>• The gateway transport is `http.DefaultTransport` (`api-gateway/cmd/server/main.go:399`). | P1 |
| COM-D-4 | CONFIRMED for the adapter, but latent | VERIFIED-READ | `S/checkout_p0.go:118-150` (a live call on every quote). `C/internal/courier/shiprocket.go:40-48` (20 s timeout, default transport) and `:94-119` (login under the mutex; the token is not dropped on a 401). No breaker, retry or singleflight in `C/internal/courier`. No per-route limit on `/checkout/quote` (`H/handler_p0.go:284`). **But** the prod secrets manifest seeds `courier_provider: const:stub` (`tools/prodsecrets/manifest.yaml:613-616`), so Shiprocket is not called in prod as seeded. | P2 now; P1 the day `courier_provider` switches to shiprocket |
| COM-D-5 | DOWNGRADE-TO-SUSPECTED | VERIFIED-READ for no cache; SUSPECTED for cost | No singleflight, TTL or cache in `C/internal`. `GET /home` sets no Cache-Control (`H/handler_storefront.go:142`); the only public cache header is on attributes (`H/handler_attributes.go:63`). | P2 (latency at catalogue size is arithmetic) |
| COM-D-6 | DOWNGRADE-TO-SUSPECTED (the evidence was incomplete) | VERIFIED-READ for predicates and indexes; SUSPECTED for the plan | The track missed two indexes: `idx_products_created_id ON products(created_at DESC, id DESC) WHERE status='active' AND approval_status='approved'` (`C/database/setup.sql:1027-1028`) and `idx_products_live_keyset` (`migrations/030_search_readback_indexes.sql:26-28`). Both are partial on `products.*`. `productSummaryLive` (`P/storefront.go:177-182`) states only `po.*`, so neither qualifies, and the conclusion stands. Cheaper fix: `product_offers` lifecycle columns are a copy of `products` (`P/productoffers.go:145-176`), so adding `p.status='active' AND p.approval_status='approved'` to the listing query lets the existing index serve it, with no migration. | P2 |
| COM-D-22 | CONFIRMED | VERIFIED-READ | `git ls-tree` loadtests are dating 3, rider 3, and `tools/loadtest/{lib,long-video,reels}`. No commerce or payments script. No `func Benchmark` in commerce, payments, `shared/outbox` or `shared/kafka`. Nothing in pb-echo. | P1 (gate prerequisite; every capacity number above is unmeasured) |
| COM-E-3 | CONFIRMED | VERIFIED-READ | `CheckoutTotal`, `CheckoutDuration`, `OversellAttempt`, `RefundInitiateFailed`, `OrderTransitionRejected` and `QuoteRejected` occur only in `C/internal/obs/metrics.go:30-90`. `prometheusRule` appears only in feed and media values. `infra/terraform/modules/observability` has no commerce or payment rule. payments registers exactly two metrics (`PAY/internal/obs/metrics.go:29-40`). notification-service `ops_alerts` is not written by commerce or payments. | P1 |
| COM-F-1 | CONFIRMED | VERIFIED-READ | `Razorpay-Signature` appears in tests only inside payments (`provider_contract_test.go`, `handler_test.go`, `moneytruth_c3_integration_test.go`, `offers_fake_integration_test.go`). `tools/integration/commerce_prepaid_test.go:34-36,139-165` asserts the 503 on a stub stack. The dev smoke has no webhook step. | P1 |
| COM-F-4 | CONFIRMED | VERIFIED-RUN (`gh`) | Last `integration-commerce.yml` run was 41162319116 on 2026-10-31 (workflow_dispatch). There are 19 commerce/payments commits since `545baf5f0`. Triggers are `pull_request` paths or push to `main` only (`.github/workflows/integration-commerce.yml:15-30`). The branch has no open PR; #6 and #7 merged in August. `scripts/ci/integration-services.yaml:163` skips the four gated proofs. integration-all is green on HEAD (44111295029) but does not run them. | P1 |
| COM-A-6 | CONFIRMED with a correction | VERIFIED-READ | `CommerceApi.kt` has no coupon, review, invoice, payment-offers or `/addresses/{id}` route. `couponCode` is only restored (`CheckoutViewModel.kt:139`). **Correction:** Android buyers DO track local deliveries. `OrdersScreen.kt:412` renders `LocalOrderDeliveries`, which polls `/delivery/orders/{id}/jobs` and shows the partner's live map pin (`core/commerce-ui/.../LocalDelivery.kt:30-35,173-243`). Android uses `/orders/{id}/shipments` only for the seller's pickup codes. Courier (AWB) tracking is the real gap. | P2 (parity; not abuse, and not a broken core flow) |
| COM-A-10 | CONFIRMED, narrowed; severity raised | VERIFIED-READ | `PAY/internal/config/config.go:56-71,172-190`: unset means off outside dev. Every prod, qa and staging values file sets ENV and neither flag. Narrowed: per the table at `P/owed_captures.go:3-34`, `late_capture` and order-side `capture_mismatch` are refunded by commerce's own refund worker, which always runs (`C/cmd/server/main.go:393`). Only the payments-ledger cases (`duplicate_order_capture`, `duplicate_capture`, `late_capture_after_failed`, payments-side `capture_mismatch`) wait on the off worker. | P1. The buyer's money is kept with no alert (COM-E-3) and no working console. The track rated it P2. |

## Part 2: launch-gate PASS candidates

| Item | Result | Evidence and reason |
|---|---|---|
| (a) Gate 7: money computed only on the server | **PASS** (with one caveat) | **Server-side money:** <br>• Quote body is address, coupon and method (`H/handler_p0.go:312-314`). <br>• Checkout body adds `quote_id`, `terms_version` and a compare-only `expected_total_minor` (`:373-378`). <br>• Price comes from `selling_price_minor` under lock (`P/checkout.go:875-878`), with currency fixed `"INR"` (`:661`). <br>• The intent amount is `OrderTotalMinor` (`S/checkout_p0.go:469-478`). <br>• Legacy float checkout and quote are fenced (`H/handler_p0.go:88-118`, `H/handler.go:142-160`). <br>**Caveats:** <br>• The live but unused float preview is COM-V3-3. <br>• The prod shipping rate is a stub constant: COM-V3-1. |
| (b) Paid only from webhook or server verification | **FAIL** | **What passes:** <br>• HMAC over the raw body with `hmac.Equal`, failing closed on an empty secret (`PAY/internal/gateway/razorpay_provider.go:166-190`). <br>• Amount and payer tuple check (`P/p0.go:215-240`). <br>• The status poll is read-only (`S/checkout_p0.go:483-507`). <br>• Stub confirm is refused in prod by both services (`C/cmd/server/main.go:311-319`; PAY main `:139-140`). <br>• No card field exists server-side (grep). <br>**Why it fails:** the commerce consumer marks paid from a `payment.succeeded` on the shared topic with no call back to payments. `paymentevents.AllowUnstated` accepts a blank currency or payer (`P/p0.go:215-232`), so a holder of the shared Kafka credential can mark an order paid (COM-B-2, re-confirmed). Replay is absorbed idempotently but not rejected (COM-B-13). |
| (c) No oversell; abandoned holds released | **PASS** (READ only) | **Oversell:** <br>• `FOR UPDATE` in variant order (`P/checkout.go:1006-1039`). <br>• `CHECK (reserved_qty <= total_qty)` (`migrations/009_inventory_integrity.sql:84-93`; validated in 013). <br>• The constraint violation maps to OutOfStock (`P/checkout.go:691-724`). <br>• Single-seller carts (`:935`). <br>**Release:** <br>• Expiry runs unconditionally every minute (`C/cmd/server/main.go:399`). <br>• It is status-checked under lock and releases stock and coupon (`P/p0.go:1310-1352`). <br>• The payment-failed path also releases (`P/p0.go:440-452`). <br>The C1 integration proof was not run by me; its last run was 31 Oct. |
| (d) State machine in code; every transition authorised | **FAIL** (on authorisation) | **State machine passes:** <br>• `transitionOrderStatusTx` refuses moves outside `order_status_transitions` (`P/order_fulfilment.go:41-90`), and cancel uses the same matrix (`P/p0.go:721`). <br>• Payment, failure and expiry writes are gated by explicit state checks under `FOR UPDATE` (`P/p0.go:405-452,1325-1345`). <br>• The unguarded `UpdateOrderStatus` (`P/orders.go:359-372`) is reachable only from the fenced returns flow (`S/returns.go:93,134,298`). <br>**Authorisation fails:** the `payment_pending → confirmed` move can be driven by a forged Kafka event (COM-B-2). |
| (e) Tenant isolation | **FAIL** (one exception; every commerce object-ID route I re-read passes) | **Re-read, all pass:** <br>• Stock read and write (`P/inventory_adjust.go:140-153,286-305,350-372`). <br>• Seller variant. <br>• Addresses (`P/addresses.go:125,164,178,190,209`). <br>• Delivery seller location, order jobs, partner jobs and customer code (`P/delivery_operations.go:26-35,171-196,317-349,526-545,879-880`; `S/delivery_operations.go:124-133`). <br>• Seller order history (`S/fulfilment.go:163-176`). <br>• Seller coupon patch (`P/coupons.go:1303-1330`). <br>**Exception:** a variant's `image_media_id` is in the store allowlist with no `verifyMedia` call (`P/variants_stock.go:165-172`; `S/catalogue_products.go:520-556`), and `VisibleProductMediaIDs` then grants the seller, or everyone once the listing is live, any media id (COM-B-1, re-confirmed). |
| (f) Refund caps; coupon reuse and stacking | **PASS** | **Refunds:** <br>• `CHECK (refunded_amount_minor + refund_reserved_minor <= amount_minor)` (`PAY/database/migrations/007...sql:192`). <br>• `refunded_captured_minor BETWEEN 0 AND captured_minor` (`014_payment_offers.sql:177-178`). <br>• A cancel refunds only what is due net of settled and owed, under one key `cancel:<orderId>` (`P/p0.go:783-805`). <br>**Coupons:** <br>• Conditional claim (`P/coupons.go:420-440`). <br>• Per-user count under the cart lock (`:302-311`). <br>• One code field. <br>• Platform coupons are off in prod (`values-prod.yaml:130`). |
| (g) Gate 4: no secrets | **PASS** | **Grep of commerce, payments, the Android commerce, payments and seller modules, and their deploy values:** <br>• Only fake `rzp_test_Contract…` key ids, in test fixtures. <br>• No private key, AWS key or live key. <br>• No secret field in a `slog` call. <br>**History:** gitleaks is not installed locally, but `.github/workflows/security.yml:26-34` runs gitleaks-action with `fetch-depth: 0`, and run 44111294714 on HEAD succeeded. The `.gitleaksignore` entries were not reviewed. |
| (h) Gate 12: prod configuration | **FAIL** | **Failures:** <br>• KYC is the stub unconditionally (`C/cmd/server/main.go:354-359`). <br>• The courier is the stub in prod as seeded (`manifest.yaml:613-616`), and `courier.New()` falls back silently with no ENV guard (`C/internal/courier/courier.go:124-135`): COM-V3-1. <br>• The Razorpay test-key refusal exists only in the seeder (`tools/prodsecrets/env.go:31-58`). payments-service does not check the prefix at runtime. <br>• `deploy/services/payments-service/values-prod.yaml:8-11` still says "the seeder stores the Razorpay TEST key pair here", which contradicts `env.go`. <br>**Good:** <br>• `PAYMENTS_ALLOW_STUB` is refused in prod by both services. <br>• `COMMERCE_PRODUCT_AUTO_APPROVE: "false"`. <br>• `LOG_LEVEL: info`. <br>• `COURIER_STUB_AUTO_DELIVER_AFTER` is ignored in managed ENVs (`main.go:409-419`). |

## Part 3: coverage gaps settled

- **Seller stock, address and delivery-partner authorization** (track B "Not verified"): settled; all scoped. See (e).
- **Courier webhook past signature checking** (track A "Not verified"): read line by line. Defects in COM-V3-2. The stub's `ParseWebhook` returns nil, so in prod as seeded the route verifies nothing and changes nothing.
- **Order expiry while a payment is in flight:**
  - `expireOne` (`P/p0.go:1310-1352`) does not consult the intent.
  - The Razorpay order carries no expiry (`PAY/internal/gateway/razorpay_provider.go:124-145`).
  - Opening the intent does not extend the 20-minute hold (`P/checkout.go:680`; `S/checkout_p0.go:452-458`).
  - A payment after the lapse becomes a `late_capture` and is refunded by commerce's own worker (`P/p0.go:283-300`).
  - No money is lost; this is UX only (COM-V3-4).
- **Admin web console commerce screens:**
  - pb-echo has admin routes only for dating and mopedu. Commerce has only coupon contract fixtures under `src/features/shop/__tests__`.
  - `postbook-ui` and modernsmapp hold no `/v1/admin/commerce` consumer.
  - UNVERIFIED: the console's location is unknown.

### COM-V3-1

- **ID:** COM-V3-1
- **Title:** Production is seeded with the stub courier. Every buyer pays a hard-coded ₹49 "obviously-fake" shipping rate, every six-digit pincode is serviceable, and the code's comments claim prod refuses this adapter.
- **Severity:** P1
- **Category:** money / configuration (Gate 12)
- **Evidence level:** VERIFIED-READ (seeded value and code path); UNVERIFIED (whether the live secret was rotated since)
- **Evidence:**
  - `tools/prodsecrets/manifest.yaml:613-616`: `courier_provider: const:stub` in the environment-independent `commerce-service` block, used for both qa and prod.
  - `deploy/services/commerce-service/values-prod.yaml:85` maps `COURIER_PROVIDER` from that secret.
  - `C/internal/courier/courier.go:124-135`: any value but `shiprocket` returns the stub, and there is no ENV check.
  - `:167-187`: the stub serves any shape-valid pincode with `ShippingChargeMinor: 4900`, commented "A visible, obviously-fake rate. M-10: production refuses to load this adapter at all", which is false.
  - `S/checkout_p0.go:115-120` says "production must not fall back to a made-up rate". The B8 zero-rate guard passes 4900.
  - `C/cmd/server/main.go:137-138` logs no warning.
- **How to reproduce:** render the prod secrets with the manifest; quote any cart to any six-digit pincode on qa. The quote shows ₹49 delivery and `serviceable: true`.
- **Impact:**
  - Real money is charged on a placeholder rate.
  - Orders are accepted for pincodes no courier serves, then end in a seller cancel and refund.
  - The stub webhook accepts unsigned calls (COM-B-10).
  - COM-D-4 is latent until the switch.
- **Fix:** a founder decision. Either:
  - make a flat manual-shipping fee a named, configured price (env value, documented) and drop the "fake" adapter from prod; or
  - switch to `const:shiprocket` before launch.
  
  Either way, refuse `stub` when `ENV` is managed, at boot.
- **Acceptance:**
  - A boot test: `ENV=prod COURIER_PROVIDER=stub` exits non-zero, or is explicitly allowed by a named flag.
  - The manifest's prod value is not `stub`, or the flat fee is documented as policy.
- **Effort:** S

### COM-V3-2

- **ID:** COM-V3-2
- **Title:** Courier webhook processing has no shipment state guard. Unknown statuses become `in_transit`, the full activity history is re-applied on every call, and per-update write errors are swallowed.
- **Severity:** P2
- **Category:** reliability / fulfilment
- **Evidence level:** VERIFIED-READ (code); SUSPECTED (Shiprocket's array order and field names, not checked against the vendor)
- **Evidence:**
  - `C/internal/courier/shiprocket.go:270-300`: `default: return "in_transit"`, so NDR, undelivered or cancelled scans read as in transit. Every `shipment_track_activities` entry becomes an update.
  - `S/shipments.go:521-531`: append, then `UpdateShipmentStatus`, with errors only logged.
  - `P/shipments.go:285-297`: an unconditional `UPDATE shipments SET status=$1`, so the shipment's final status is whichever activity is last in the array. A replay or out-of-order call can move `delivered` back.
  - `P/shipments.go:299-305`: `shipment_events` are re-inserted with no dedupe.
  - `rto_delivered` and `lost` change nothing on the order.
- **How to reproduce:** unit-test `HandleShipmentWebhook` with a fake provider returning `[delivered, picked_up]`. The shipment ends `picked_up` and the event count doubles on the second call.
- **Impact:**
  - Wrong tracking shown to buyers and sellers.
  - Duplicate event rows.
  - RTO parcels leave the order `shipped`, with no refund route (COM-B-4).
- **Fix:** a shipment transition table (forward-only, terminal states sticky). Apply only the newest activity by `occurred_at`. Dedupe events on (shipment, status, occurred_at). Map RTO and lost explicitly to an order action.
- **Acceptance:** the test above leaves `delivered` and one event per scan; an unknown status is logged and ignored.
- **Effort:** S

### COM-V3-3

- **ID:** COM-V3-3
- **Title:** The legacy float pricing preview (`GET /cart/coupon-preview`) and the seller price-tier ladder (`PUT/GET /variants/:id/price-tiers`) are live, but P0 checkout ignores tiers.
- **Severity:** P3
- **Category:** money hygiene
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `H/handler.go:114-115,137` registers the routes.
  - `H/handler_orders.go:96-116` calls `svc.Quote`, which calls `priceCart`, which uses the tiers, a float, and the rupee column (`S/checkout_quote.go:152-180,374-375`).
  - `P/checkout.go:875-878` (P0) reads `selling_price_minor` only.
  - No client calls either route (pb-echo `features/shop/api/coupons.ts:7`; Android grep).
- **How to reproduce:** set a tier on a variant, call the preview, then quote. The totals differ.
- **Impact:** none to money today. A future client would show prices checkout does not charge.
- **Fix:** fence both routes alongside the legacy quote.
- **Acceptance:** both answer 404 through `FenceMiddleware`.
- **Effort:** XS

### COM-V3-4

- **ID:** COM-V3-4
- **Title:** A buyer who opens the payment late in the 20-minute hold can pay after the order expires. They are then auto-refunded rather than kept.
- **Severity:** P3
- **Category:** UX / payments
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `P/checkout.go:680` (`reservationTTL = 20m`).
  - `S/checkout_p0.go:452-458` (the first intent open does not extend the hold).
  - `P/p0.go:1310-1352` (expiry ignores the intent).
  - `PAY/internal/gateway/razorpay_provider.go:124-145` (no provider-side expiry).
  - `P/p0.go:283-300` (late capture → refund command).
- **How to reproduce:** check out, wait 19 minutes, open the intent, pay at minute 21. The order is `expired` and `refund_pending`.
- **Impact:** a paying buyer loses the item and waits for a refund.
- **Fix:** extend the hold, or refuse the intent, when less than about 5 minutes remain. Pass Razorpay checkout `timeout` to the client.
- **Acceptance:** an integration test where an intent opened at T-1 min either extends the hold or is refused.
- **Effort:** S

## What remains UNVERIFIED and what it needs

- **Every capacity number** (COM-D-1/2/5/6): EXPLAIN ANALYZE on a 100k-product `commerce_it_test` fixture, and k6 runs (COM-D-22).
- **Whether the live `courier_provider` or Razorpay secrets were rotated** after seeding: read access to `atpost/prod/commerce-service` metadata, values not needed.
- **Gated 998 proofs and the C1 oversell proof on HEAD:** a `workflow_dispatch` of `integration-commerce.yml`. I did not trigger it, since that is a write action.
- **Shiprocket webhook payload order and field names** (COM-V3-2): the vendor documentation or a sandbox capture.
- **Admin console commerce screens:** the repository or branch that hosts the admin web.
- **`.gitleaksignore` suppressions:** a human review of each entry.
