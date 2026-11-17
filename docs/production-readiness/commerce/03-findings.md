# Commerce (MStore / MSeller): findings

- **Audit date:** 2026-11-17. Code: `codex/module-01-02-launch-safety` @ `be57a46bf` (map built at `d83616ad6`; the commits between touch e2e, Android manifests and lint, and media-worker only). commerce-service has no uncommitted edits. payments-service, admin-service, api-gateway and `deploy/services/*` files with uncommitted edits were read at HEAD with `git show HEAD:`; their uncommitted hunks were not audited. Web: `C:\workspace\wt\pb-echo`, `feat/echo-web` @ `1b596b4`, clean.
- **Sources:** the Stage 0 map (`00-map.md`), six Stage 2 tracks (`tracks/track-a…f`) and three Stage 3 verifiers (`verify/v1-money-orders.md`, `v2-security.md`, `v3-perf-tests-pass.md`). Where a verifier disagreed with a track, the verifier's verdict, severity and evidence level are used, except where the audit lead decided otherwise (stated in "Merges and verifier changes").
- **Nothing was executed against a running stack.** No database, server, device, browser, deployed environment, Razorpay or Shiprocket was used. VERIFIED-RUN means a unit test, a repository command or a CI log (`gh`).
- **IDs:** `com-N` is the final ID. Each finding has a `Was:` line listing the original IDs (COM-A = functional, COM-B = security, COM-C = data, COM-D = performance, COM-E = reliability and deployment, COM-F = tests, COM-V3 = verifier v3, "v2 note" = an observation in verifier v2). 104 original items were merged down to 85 findings.
- **Inherited platform findings** (`feed-N`, `reels-N`) are listed in their own section and are **not** counted.
- **Paths:** backend paths are relative to `Architecture/services/` unless they start with another top-level folder (`infra/`, `deploy/`, `charts/`, `identity-platform/`, `tools/`, `scripts/`, `.github/`). Shorthand: `C/` = `commerce-service/`, `H/` = `C/internal/http/`, `S/` = `C/internal/service/`, `P/` = `C/internal/store/postgres/`, `PAY/` = `payments-service/`, `MS/` = `media-service/`, `SH/` = `Architecture/shared/`, `DV/` = `deploy/services/` (always HEAD), `CH/` = `charts/atpost-service/`. Android paths are relative to `mobile/android/`: `API` = `core/commerce/src/main/kotlin/com/us/android/core/commerce/network/CommerceApi.kt`, `FC/` = `feature/commerce/src/main/kotlin/com/us/android/feature/commerce/`, `FS/` = `feature/seller/src/main/kotlin/com/us/android/feature/commerce/`. Web paths are relative to `C:\workspace\wt\pb-echo`.
- **"Prod as seeded":** `tools/prodsecrets/manifest.yaml:613-616` seeds `courier_provider: const:stub` for qa and prod (v3). Whether the live secret was rotated since was not checked.

## Counts

| Severity | Count | VERIFIED-RUN | VERIFIED-READ | SUSPECTED | UNVERIFIED |
|---|---|---|---|---|---|
| P0 | 3 | 1 | 2 | 0 | 0 |
| P1 | 19 | 1 | 17 | 1 | 0 |
| P2 | 44 | 2 | 37 | 5 | 0 |
| P3 | 19 | 3 | 13 | 3 | 0 |
| **Total (commerce)** | **85** | **7** | **69** | **9** | **0** |
| Inherited platform findings (not counted above) | 21 rows | | | | |

The evidence level counted is the finding's primary level. Many findings carry a second level for one part (for example the code path is read and the courier's response is suspected); the field in each finding says which part is which.

**Conditional severities:**
- com-22 (Razorpay capture mode) is P1 SUSPECTED; it is **P0 if the Razorpay account uses manual capture**.
- com-48 (live Shiprocket call on every quote) is P2 while prod runs the stub courier and **P1 the day `courier_provider` is switched to `shiprocket`**.
- com-13 (DPDP) could drop to P2 if launch precedes the DPDP Rules' operative date (v2; not verified).

## Merges and verifier changes

| Final | Was | Change |
|---|---|---|
| com-1 | COM-A-1 | Confirmed P0 (v1). Correction: "never delivered" holds only under the stub courier; real Shiprocket webhooks can still deliver |
| com-2 | COM-C-1, COM-E-2, COM-A-10 | One defect: the two payments flags are off. COM-E-2 P1 → P0 (v1, lead). COM-A-10 (raised P2 → P1 by v3) describes the same off worker and consumer for the duplicate and mismatch cases, so it is folded in here rather than counted as a separate P1. Partial disproof kept: a late capture on an order commerce itself expired or cancelled IS refunded |
| com-3 | COM-B-1 | P1 → P0 (v2). The track's SUSPECTED part is settled: media-service refuses `document` kind (`MS/internal/service/document.go:140`), so KYC documents are not exposed |
| com-5 | COM-E-4, COM-V3-1, COM-B-10 | The same unrefused stub courier. v3 found prod is **seeded** with it, a hard-coded ₹49 rate and every pincode serviceable. v1: an accepted fake webhook changes nothing (`ParseWebhook` returns no updates) |
| com-7 | COM-B-4 | Confirmed P1 (v2). Its SUSPECTED part ("prod runs the stub") is now the seeded value (v3) |
| com-8 | COM-A-3 | Confirmed P1 (v2). Corrections: the console sees 503 from admin-service; Helm environments only (docker-compose auto-approves) |
| com-12 | COM-C-3, COM-A-12, COM-F-8 | COM-A-12 (buyer name printed as the street) is one clause of the invoice defect; COM-F-8 is its missing test |
| com-13 | COM-B-5 | Confirmed P1 (v2), with the tax retention duty: a partial purge, not a full deletion |
| com-14 | COM-E-1, COM-C-4 | Both are "the gated contract set cannot be applied"; COM-C-4 adds the drift view that blocks 999 even in the right order. v1: the Go paths still check transitions, so this is a lost backstop |
| com-15 | COM-E-3, COM-C-7 | Same missing alert rules; COM-C-7 adds the DLQ with no reader and parked commerce refunds with no operator path |
| com-16 | COM-D-3 | Confirmed and stronger (v3): no timeout in the pool, the prod DSN template or the Aurora parameter group |
| com-17 | COM-D-22, COM-F-9 | Same absent load test. Evidence VERIFIED-RUN → VERIFIED-READ (v3) |
| com-20 | COM-F-3 | Test gap only (v2): the authorization checks exist on all four read routes |
| com-22 | COM-A-13 | Track P2 → P1 SUSPECTED (lead). P0 if the account uses manual capture |
| com-23 | COM-E-5; map §7 and §8 "prod is seeded with the TEST key pair" | Correction: "prod runs Razorpay test keys" is NOT established. The seeder refuses test keys for prod; payments-service does not check at runtime; `DV/payments-service/values-prod.yaml:8-11` says the opposite |
| com-24 | COM-A-6 | P1 → P2 (v3, lead). Correction: Android does track local deliveries; the gap is courier tracking and the other buyer features |
| com-26 | COM-A-5, COM-C-5, COM-D-19 | Same dashboard query (₹0 revenue, eight sequential aggregates, errors discarded) |
| com-30 | v2 note on COM-F-3 | New, P2 (lead) |
| com-31 | COM-B-6, COM-F-6 | COM-F-6 is the missing audit and its test |
| com-34 | COM-B-9, COM-D-11 | Same missing caps (open holds, cart lines, quantity) |
| com-35 | COM-B-11, COM-E-14, COM-E-19 | Same network and edge exposure of payments, plus commerce's missing NetworkPolicy |
| com-36 | COM-B-13, COM-F-11 | COM-F-11 is the test for the replay under a fresh event id; the higher severity (P2) is kept |
| com-48 | COM-D-4 | P1 → P2 now, P1 once Shiprocket is switched on (v3) |
| com-49, com-50, com-51, com-52 | COM-D-1, COM-D-2, COM-D-5, COM-D-6 | P1 → P2 SUSPECTED (v3): the capacity numbers are arithmetic. COM-D-6: the track missed two partial indexes; the conclusion stands and a cheaper fix exists |
| com-58 | COM-D-13, COM-E-16 | Same reconciler: oldest-50 head-of-line and no claim across replicas |
| com-67 | COM-A-14, COM-B-14 | Same 403-vs-404 oracle on buyer order sub-resources; COM-B-14 adds raw error text |
| com-68 | v2 per-route note on COM-F-3 | New, P3 (lead) |
| com-69, com-70, com-42 | COM-V3-4, COM-V3-3, COM-V3-2 | New from v3 (P3, P3, P2) |
| com-76 | COM-C-9, COM-E-18 | Same dead `COMMERCE_SKIP_BOOT_MIGRATIONS` flag; COM-E-18 adds the unlocked setup.sql and unacknowledged migrations |

## Index

| ID | Sev | Evidence | Title |
|---|---|---|---|
| com-1 | P0 | VERIFIED-READ | Every paid order is booked and set `shipped` about 1 s after payment; no cancel, no seller fulfilment, never delivered under the stub |
| com-2 | P0 | VERIFIED-RUN | Captures owed back by payments-service are never refunded in qa or prod |
| com-3 | P0 | VERIFIED-READ | A self-serve seller can attach any media id as a variant image, and commerce vouches for it to media-service |
| com-4 | P1 | VERIFIED-READ | A forged `payment.succeeded` on the shared Kafka topic confirms an order; blank currency, payer and intent accepted |
| com-5 | P1 | VERIFIED-READ | Prod is seeded with the stub courier and nothing refuses it: ₹49 placeholder rate, every pincode, fake AWBs |
| com-6 | P1 | VERIFIED-READ | No refund after shipping, no partial refund, no admin or dispute tool |
| com-7 | P1 | VERIFIED-READ | A seller goes live without verified identity, and a buyer of a seller who never ships has no refund route |
| com-8 | P1 | VERIFIED-READ | Seller approval and product moderation have no working tool in any Helm environment |
| com-9 | P1 | VERIFIED-READ | Seller approve, KYC verify, product approve and COD settle are callable with the shared internal key as any actor |
| com-10 | P1 | VERIFIED-READ | The courier booking declares ₹0 package and item values |
| com-11 | P1 | VERIFIED-READ | No seller payable ledger, no payout path; earnings read ₹0 columns |
| com-12 | P1 | VERIFIED-READ | GST invoices: platform-wide series with gaps, no place of supply, buyer name is the street, no credit note |
| com-13 | P1 | VERIFIED-READ | Commerce and payments take no part in account deletion and offer no export (DPDP) |
| com-14 | P1 | VERIFIED-READ | The gated contract migrations cannot be applied by the deployment, run in the wrong order, and 999 can never pass |
| com-15 | P1 | VERIFIED-READ | No commerce or payments alert rule; half the money metrics are never incremented; DLQ and parked refunds unread |
| com-16 | P1 | VERIFIED-READ | No statement, lock or idle-in-transaction timeout anywhere; a hot row stalls the whole pod |
| com-17 | P1 | VERIFIED-READ | No load test or benchmark exists for any commerce or payments path |
| com-18 | P1 | VERIFIED-READ | No test drives a signed Razorpay webhook through payments and Kafka to a paid commerce order |
| com-19 | P1 | VERIFIED-READ | The Shiprocket webhook verifier and both courier webhook routes have no test |
| com-20 | P1 | VERIFIED-READ | Object-id isolation is untested on invoice, shipment reads, order items and seller order detail |
| com-21 | P1 | VERIFIED-RUN | The commerce P0 gate workflow has not run for 17 days and 19 commits; the gated-998 proofs run nowhere else |
| com-22 | P1 | SUSPECTED | Razorpay orders carry no `payment_capture` and nothing captures (P0 if the account uses manual capture) |
| com-23 | P2 | VERIFIED-READ | Razorpay live or test mode is checked only by the seeder; the prod values comment says the opposite |
| com-24 | P2 | VERIFIED-READ | Android buyers lack coupons, reviews, invoices, courier tracking, address edit, bank offers |
| com-25 | P2 | VERIFIED-READ | Sellers hear only of new orders; buyers are not told of late-capture or partial refunds |
| com-26 | P2 | VERIFIED-READ | The seller dashboard shows ₹0 revenue, runs eight sequential aggregates and discards errors |
| com-27 | P2 | VERIFIED-READ | Neither client offers the seller "mark delivered" |
| com-28 | P2 | VERIFIED-READ | Android seller hub shows Returns and Earnings tiles that always error |
| com-29 | P2 | VERIFIED-READ | Android sellers lack coupons, dashboard, product edit, attributes, media management, readiness |
| com-30 | P2 | VERIFIED-READ | On a multi-seller order any seller reads the whole invoice and every seller's shipments |
| com-31 | P2 | VERIFIED-READ | An approved seller can replace the payout account and identity with no audit or re-verification |
| com-32 | P2 | VERIFIED-READ | Full bank account numbers and PANs stored in plaintext in prod (`dual`); some document numbers plaintext for good |
| com-33 | P2 | VERIFIED-READ | Order and invoice events carry the buyer's email and a 7-day invoice link on the shared topic |
| com-34 | P2 | VERIFIED-READ | No caps on open holds, cart lines or quantity; coupon codes guessable; no per-route limits |
| com-35 | P2 | VERIFIED-READ | payments NetworkPolicy admits the VPC; commerce has none; the PSP webhook has no own WAF rule |
| com-36 | P2 | VERIFIED-RUN | A signed webhook replayed under a fresh event id is absorbed, not rejected, and the capture case is untested |
| com-37 | P2 | VERIFIED-READ | An expired or cancelled order leaves its intent and Razorpay order payable |
| com-38 | P2 | SUSPECTED | A cancel can race the courier booking |
| com-39 | P2 | VERIFIED-READ | An outage longer than the 20-minute hold turns every capture into an expiry plus refund |
| com-40 | P2 | VERIFIED-READ | Invoice and shipment jobs dead-letter after about 75 s; nothing replays or alerts |
| com-41 | P2 | VERIFIED-READ | Shipment status comes only from webhooks; a missed webhook leaves the order `shipped` for ever |
| com-42 | P2 | VERIFIED-READ | Courier webhook processing has no shipment state guard |
| com-43 | P2 | VERIFIED-READ | A Redis outage takes all of MStore down |
| com-44 | P2 | VERIFIED-READ | payments unreachable gives "Pay" a 500, not a retryable 503 |
| com-45 | P2 | VERIFIED-READ | A paid order cannot be followed by one id from checkout to webhook to confirmation |
| com-46 | P2 | VERIFIED-READ | Staging and both Azure environments cannot boot commerce |
| com-47 | P2 | VERIFIED-READ | A database restore breaks the Razorpay agreement and may reuse GST invoice numbers |
| com-48 | P2 | VERIFIED-READ | Every quote makes a live, uncached, unbroken Shiprocket call (P1 once Shiprocket is on) |
| com-49 | P2 | SUSPECTED | Product images are authorised through five services; the commerce hop is an unindexed OR-join |
| com-50 | P2 | SUSPECTED | A flash sale serialises quote and checkout on one `products` row |
| com-51 | P2 | SUSPECTED | `/home` and `/categories` are rebuilt from the whole live catalogue on every request |
| com-52 | P2 | SUSPECTED | Browse and category listings cannot use an index |
| com-53 | P2 | VERIFIED-READ | Product search is unindexed `ILIKE '%q%'` over nine expressions |
| com-54 | P2 | VERIFIED-READ | The public product list keeps an unbounded offset path with a full `COUNT(*)` |
| com-55 | P2 | VERIFIED-READ | The public reviews list takes an unbounded `limit` |
| com-56 | P2 | VERIFIED-READ | Seller order lists recompute full history per page; tabs filter after paging |
| com-57 | P2 | VERIFIED-READ | Inverted payment timeouts (15 s outer, 30 s inner), no breaker |
| com-58 | P2 | VERIFIED-READ | The reconciler takes the 50 oldest stale intents and runs unclaimed on every replica |
| com-59 | P2 | VERIFIED-READ | Expired holds are released at most 200 orders per minute |
| com-60 | P2 | VERIFIED-READ | Paid orders are fulfilled one job at a time per pod |
| com-61 | P2 | VERIFIED-READ | Product image URLs change every 2 minutes; client caches miss and lazy tiles can expire |
| com-62 | P2 | VERIFIED-READ | The web grid draws the 1080 px rendition in every tile |
| com-63 | P2 | VERIFIED-READ | Order confirmation rides one sequential consumer of the shared firehose with `RetryForever` |
| com-64 | P2 | VERIFIED-READ | The commerce refund worker is untested; no test follows a cancel to `refunded` |
| com-65 | P2 | VERIFIED-RUN | The web's golden-fixture contract tests skip in CI and have drifted |
| com-66 | P2 | VERIFIED-READ | No test proves every `orders.status` writer goes through the matrix |
| com-67 | P3 | VERIFIED-READ | Buyer order sub-resources answer 403 to a stranger (existence oracle) and echo raw errors |
| com-68 | P3 | VERIFIED-READ | Seller order detail answers 403, not 404, for another seller's order |
| com-69 | P3 | VERIFIED-READ | A buyer who opens payment late in the hold pays after expiry and is auto-refunded |
| com-70 | P3 | VERIFIED-READ | The legacy float preview and the price-tier ladder are live but checkout ignores tiers |
| com-71 | P3 | VERIFIED-READ | Legacy float and NUMERIC money paths remain reachable (COD settle, `UpdateOrderStatus`) |
| com-72 | P3 | VERIFIED-READ | Review text has no length limit and is published immediately |
| com-73 | P3 | SUSPECTED | The anonymous share limiter keys on a spoofable IP |
| com-74 | P3 | VERIFIED-READ | B2B approve and reject escape the `/organizations` fence |
| com-75 | P3 | SUSPECTED | KYC uploads may be ordinary image-kind media |
| com-76 | P3 | VERIFIED-RUN | Boot migration hygiene: unlocked setup.sql, a flag nothing reads, unacknowledged migrations |
| com-77 | P3 | SUSPECTED | No startup probe; slow boot work meets liveness |
| com-78 | P3 | VERIFIED-READ | No PDF invoice is ever produced (no renderer in the image) |
| com-79 | P3 | VERIFIED-READ | On a Redis error every product view updates the hot `products` row |
| com-80 | P3 | VERIFIED-READ | Append-only tables are never pruned |
| com-81 | P3 | VERIFIED-READ | Smaller unbounded or per-row work; Android's `sort` ignored |
| com-82 | P3 | VERIFIED-READ | No test sends forged money fields in cart, quote or checkout bodies |
| com-83 | P3 | VERIFIED-RUN | CI cannot show which commerce and payments integration tests ran |
| com-84 | P3 | VERIFIED-RUN | The e2e admin refusals check only the status code |
| com-85 | P3 | VERIFIED-READ | No Android instrumented commerce tests |

---

## P0

### com-1

- **ID:** com-1
- **Title:** Every paid order is booked with the courier and set to `shipped` about one second after payment, so neither buyer nor seller can cancel it, the seller's pack and own-courier steps are dead, and under the stub courier (qa, and prod as seeded) the order never reaches `delivered`.
- **Severity:** P0
- **Category:** functional (order lifecycle, cancellation)
- **Evidence level:** VERIFIED-READ (Track A; confirmed by v1)
- **Evidence:**
  - Payment confirmation inserts `fulfill_paid_order` inside the paid transaction (`P/p0.go:339-345`). The fulfilment worker is always started and polls every second (`C/cmd/server/main.go:425-426`, `C/internal/workers/fulfillment.go:46`).
  - `FulfillPaidOrderJob` issues the invoice and books whenever `s.courier != nil` (`S/jobs.go:43-56`). The courier is always set; anything but `shiprocket` gets the stub (`C/cmd/server/main.go:138`, `C/internal/courier/courier.go:125-135`).
  - `CreateShipmentsForOrder` gates only on `payment_status=paid`, with no packed check (`S/shipments.go:113-122`), stamps `ShippedAt: now` and calls `MarkOrderShipped` as `system` (`:286`, `:350-357`). Migration 033 admits `confirmed → shipped` for `system` (`C/database/migrations/033_system_ships_at_booking.sql:1-14`).
  - From `shipped`, cancel is admin-only (`010_gst_and_state_machine.sql:113`) and no service code acts as `admin`. Seller pack needs `confirmed`, seller cancel `confirmed|packed` (`S/fulfilment.go:127-161`). A later seller "ship" reuses the booked shipment (`S/shipments.go:182-186`).
  - The booking is not `seller_managed` (`S/fulfilment.go:62-70`), so the seller cannot mark it delivered (`S/seller_delivery.go:92-108`). The stub parses no webhooks (`courier.go:156-158`); the stub auto-deliver timer is refused in managed environments (`main.go:409-420`).
  - The buyer gets "order shipped" at booking time (`notification-service/internal/events/commerce_consumer.go:10`).
  - Correction (v1): under real Shiprocket, webhooks can still deliver, so "never delivered" holds only under the stub, which qa runs and prod is seeded with (com-5).
- **How to reproduce:** On qa, buy an item and pay with a Razorpay test card. Open the order: it reads Shipped with a `STUB…` AWB; Cancel is not offered (`can_cancel` false); the seller's Pack fails; the order never becomes Delivered and Write a review never appears.
- **Impact:** Every paid order. Buyers lose the right to cancel within a second of paying; sellers cannot pack, cancel for out-of-stock or ship with their own courier; with the stub every order is stuck in `shipped` and reviews are impossible; with Shiprocket the order says Shipped before any pickup. Together with com-6 there is no refund path at all after payment.
- **Fix:** Founder decision: auto-booking at payment or seller-driven shipping. Smallest: drop the `CreateShipmentForOrder` call from `FulfillPaidOrderJob` (keep the invoice), so booking happens when the seller presses Ship (`POST /seller/orders/:id/ship`, wired on both clients). If auto-booking is wanted, book on `packed` and leave the order `confirmed`/`packed` until the courier's first pickup event. The stub refusal is com-5.
- **Acceptance:** A service test that `FulfillPaidOrderJob` leaves a paid order `confirmed` with no shipment (fails now: `shipped`). A test that a buyer can cancel a paid order before the seller ships and one refund command is written.
- **Effort:** S
- **Was:** COM-A-1 (linked: COM-E-4 → com-5, COM-C-8 → com-38, COM-A-2 → com-6)

### com-2

- **ID:** com-2
- **Title:** Captures owed back to buyers are never refunded in qa or prod: the payments refund-required worker and duplicate-capture consumer are off, so a capture on an intent the reconciler already failed, a second capture, a second paid intent on one order, and an amount or currency mismatch are recorded and kept, with no alert and no operator path.
- **Severity:** P0
- **Category:** money / refunds
- **Evidence level:** VERIFIED-RUN for the flag defaults (`TestResolveRefundRequiredWorker`, case "prod, unset" expects off, passed in v1); VERIFIED-READ for the rest
- **Evidence:**
  - `devDefaultFlag` turns `PAYMENTS_REFUND_REQUIRED_WORKER` and `PAYMENTS_DUPLICATE_CAPTURE_CONSUMER` on only for `ENV` `""`, `dev` or `local` (HEAD `PAY/internal/config/config.go:56-71,143-152,172-190`). Payments `ENV` is `prod` in prod (`DV/payments-service/values-prod.yaml:97`) and in qa (`values-qa.yaml:108`), `staging` in staging (`:103`); no values file, chart or secret mapping sets either flag (v1 `git grep`).
  - Worker and consumer start only behind the flags (HEAD `PAY/cmd/server/main.go:337-357`).
  - Parked, never refunded: a capture on a `failed` intent and a second distinct capture (`PAY/internal/store/postgres/p0.go:270-301`, logging "returns it when enabled", `:314-328`); an amount or currency mismatch (`PAY/internal/service/p0.go:144-156`). A second paid intent on one order: commerce records the owed money and emits `commerce.order.duplicate_capture` (`P/p0.go:972-1007`), which payments never consumes. Table of owners: `P/owed_captures.go:3-34` (v3).
  - Not affected (v1 partial disproof): a late capture on an order commerce expired or cancelled writes `order_refund_commands` (`P/p0.go:265-290`), sent by commerce's always-on refund worker (`C/cmd/server/main.go:393`).
  - No operator path: HEAD payments admin routes list and resolve only `refund_commands` (`PAY/internal/http/admin_routes.go:4-8`); the `/refund-required/:id/retry` route exists only in uncommitted work; the console cannot reach payments anyway (com-8).
  - No alert: no metric reads `refund_required`; the chart default is `prometheusRule.enabled: false` (`CH/values.yaml:140-142`) and no commerce or payments values enable it (com-15).
  - Reachable in normal use: the hold is 20 min (`P/checkout.go:680`); the reconciler fails a quiet intent after 600 s plus the 15-minute window; the Razorpay order has no expiry (com-37), so a UPI collect approved late or a re-opened Checkout captures on a `failed` intent.
- **How to reproduce:** CI scratch DB `payments_it_test` with a prod-shaped config: create an intent, let the reconciler fail it, post a signed `payment.captured` for the same provider order. A `refund_required` row with `resolved=false` appears and never changes; no refund call reaches the provider.
- **Impact:** A buyer is charged with no goods and no refund until someone runs SQL or refunds from the Razorpay dashboard outside the ledger. Nobody is paged.
- **Fix:** Set `PAYMENTS_REFUND_REQUIRED_WORKER: "true"` and `PAYMENTS_DUPLICATE_CAPTURE_CONSUMER: "true"` in `DV/payments-service/values-prod.yaml` and `values-qa.yaml` (and staging if checkout runs there). Add a gauge for unresolved `refund_required` rows older than 1 h with an alert rule (with com-15).
- **Acceptance:** A values or render test that both flags are `"true"` in prod and qa (fails now). The prod-shaped boot logs `refund-required worker ... enabled=true`. `refundrequired_rr_*` and `duplicate_capture_m1_*` pass with `ENV=prod` and the flags set. On qa the repro above ends with the row resolved by the signed refund webhook.
- **Effort:** S (config) + S (alert)
- **Was:** COM-C-1, COM-E-2, COM-A-10

### com-3

- **ID:** com-3
- **Title:** A self-serve seller can attach any media id as a variant image, and commerce then tells media-service that the seller (and, once the listing is live, everyone) may view it, bypassing every other service's privacy decision for that asset.
- **Severity:** P0
- **Category:** authorization / IDOR (cross-service)
- **Evidence level:** VERIFIED-READ (whole chain; v2)
- **Evidence:**
  - `PATCH /variants/:variantId` (`H/handler.go:111`) decodes into `map[string]any` and passes it on (`H/handler_variants.go:150-176`).
  - `S/catalogue_products.go:525-556` checks product ownership, then `checkVariantPatch` (`S/reviewedcontent.go:447-480`: status, sku, price) and the store write. No `verifyMedia` call. `image_media_id` is in the store allowlist and written as sent (`P/variants_stock.go:161-172`). Contrast: product images (`S/catalogue_products.go:131-136`, `S/productwrite.go:657-662`), storefront logo and banner, delivery proof are all verified.
  - `VisibleProductMediaIDs` (`P/storefront.go:767-793`, served at `H/handler_storefront.go:67-68`) allows a media id referenced by `pv.image_media_id` when the listing is live OR when `seller_user_id = viewer`.
  - media-service asks five authorities and the first yes wins (`MS/cmd/server/main.go:537-543`, `MS/internal/delivery/authz.go:408-435,437+`).
  - What limits it (v2): media-service refuses `document` kind and dating-scoped assets to viewers (`MS/internal/service/document.go:140`), so **KYC documents are not exposed**. On an approved listing an image change is substantive and sends the listing back to review (`S/reviewedcontent.go:376-383`, `gateApprovedEdit :120-135`), so **publishing to everyone needs a moderator to approve** a listing showing the foreign image. The seller-only read needs no review. A seller row and a draft product need no approval (`H/sellerresolve.go:34-51`).
  - The attacker needs the victim's media UUID (not guessable): a former follower, chat or group member, someone later blocked, or anyone the post's JSON reached before it went private or was deleted. Not checked: whether a deleted post's asset row still exists.
- **How to reproduce:** Seller S has draft product P with variant V. S sends `PATCH /v1/commerce/variants/V {"image_media_id":"M"}` with another user's private media id M: 200. `GET /v1/commerce/products/P/variants` as S returns a signed URL for M. If a moderator approves P, M opens to anonymous viewers.
- **Impact:** Any user who registers as a seller can read, and with one missed moderation publish, another user's followers-only, chat or group photo whose id they once saw. No cluster access is needed.
- **Fix:** In `UpdateProductVariant`, when `image_media_id` is in the body, call `s.verifyMedia(ctx, actorID, media.KindImage, &id)` before the store write, as PATCH `/products` does for `primary_image_media_id`. Apply the same rule to any future variant-create path that takes an image. Later (com-49 (b)): media-service routes an asset to its owning authority instead of first-yes-wins.
- **Acceptance:** A test that `PATCH /variants/:id` with a media id owned by another user returns 403 `NOT_YOUR_MEDIA` and writes nothing (fails now: 200); the seller's own image still succeeds.
- **Effort:** S
- **Was:** COM-B-1

---

## P1

### com-4

- **ID:** com-4
- **Title:** Any holder of the single shared Kafka credential can confirm any order as paid by publishing `payment.succeeded`, because commerce accepts a blank currency, payer and intent id and never asks payments-service.
- **Severity:** P1
- **Category:** payment integrity / event trust
- **Evidence level:** VERIFIED-READ (VERIFIED-RUN for the policy behaviour: `shared/paymentevents` tests pass); confirmed by v2 and v3 gate (b) FAIL
- **Evidence:**
  - One SCRAM application user with full access and no ACLs (`infra/terraform/modules/msk/main.tf:11-17,231-233,240-241`); no Kafka ACL resource under `infra/` or `deploy/`.
  - The commerce consumer filters only on `reference_type == "order"`, with no producer check and no envelope signature (`C/internal/consumers/payments_p0.go:346-371`).
  - `CheckCapture(..., AllowUnstated)` (`P/p0.go:214-230`); under `AllowUnstated` a blank currency, nil payer and blank intent are skipped and only the amount must match (`SH/paymentevents/check.go:63-95`). `captureIntentToBind` with an empty intent binds nothing (`P/p0.go:918-931`); `decideCapture` then applies (`:877-888`).
  - Commerce has `GetIntent` (`C/internal/payments/p0client.go:247`) but uses it only on checkout and confirm paths, never in the consumer. payments-service always publishes id, currency and payer (`PAY/internal/service/service.go:566-572`), so a strict policy costs nothing; payments' own consumer of the same topic re-reads its ledger (`PAY/internal/consumers/commerce.go:22-25`).
  - The order total is shown to the buyer, so the amount is known.
- **How to reproduce:** From any pod holding the Kafka secret, publish on `social.events.v1` an envelope of type `payment.succeeded` with `{reference_type:"order", reference_id:<order>, amount_minor:<total>}` and no currency, payer or intent. The order moves `payment_pending → confirmed`, `paid`, and the courier is booked.
- **Impact:** Goods shipped without payment. It needs an in-cluster foothold in any of about 30 services, which turns a low-value compromise into theft.
- **Fix:** (a) A strict policy for `payment.succeeded` in commerce: a blank currency, nil payer or blank intent is a mismatch. (b) Before confirming, call payments `GET /internal/intents/:id` (op `intent.read`, already granted) and require `succeeded` and an equal amount. Longer term (outside the repo's code: Terraform and MSK): per-service Kafka users with write ACLs on payment event types, or a dedicated payments topic (com-63).
- **Acceptance:** Integration test: a `payment.succeeded` with a blank currency, nil payer or unknown intent leaves the order `payment_pending` (fails now). A forged event naming a real but unpaid intent is refused by the intent check.
- **Effort:** M
- **Was:** COM-B-2

### com-5

- **ID:** com-5
- **Title:** Production is seeded with the stub courier and nothing refuses it: every buyer pays a hard-coded ₹49 shipping rate, every six-digit pincode is "serviceable", paid orders get fake AWBs, the stub webhook accepts unsigned calls, and a code comment wrongly says production refuses the stub.
- **Severity:** P1
- **Category:** money / configuration (Gate 12)
- **Evidence level:** VERIFIED-READ (seeded value and code path); UNVERIFIED whether the live secret was rotated after seeding
- **Evidence:**
  - `tools/prodsecrets/manifest.yaml:613-616`: `courier_provider: const:stub` in the environment-independent `commerce-service` block (qa and prod). `DV/commerce-service/values-prod.yaml:79-89` maps `COURIER_PROVIDER` from that secret; its comment says "the shop must not take orders before then" (`:10-15`) and "the code does NOT refuse the stub in production", but nothing fences checkout. qa hard-codes `COURIER_PROVIDER: stub`.
  - `C/internal/courier/courier.go:124-135`: any value but `shiprocket` returns the stub, with no ENV check; `C/cmd/server/main.go:137-138` wires it and logs nothing, unlike the stub payment (`:749-756`) and stub auto-deliver (`:409-421`), which both refuse a managed ENV.
  - The stub serves any shape-valid pincode with `ShippingChargeMinor: 4900`, commented "A visible, obviously-fake rate. M-10: production refuses to load this adapter at all", which is false (`courier.go:167-187`). `S/checkout_p0.go:115-120` says "production must not fall back to a made-up rate"; the zero-rate guard passes 4900.
  - The stub creates `STUB<hex>` AWBs with label `https://example.com/labels/<awb>.pdf` (`courier.go:141-150`). `VerifyWebhook` returns nil (`:160-161`); `ParseWebhook` returns no updates, so an unsigned call changes nothing (v1).
  - `shiprocket` with empty credentials also boots; the first quote then answers 503 `COURIER_UNAVAILABLE` (`H/handler_p0.go:796-801`).
- **How to reproduce:** Render the prod secrets from the manifest; on qa, quote any cart to any six-digit pincode: ₹49 delivery and `serviceable: true`. Pay: the shipment row carries `STUB…` and an example.com label.
- **Impact:** Real money charged on a placeholder rate; orders accepted for pincodes no courier serves; fake tracking for buyers and fake labels for sellers; and the stub is the precondition for com-1's stuck orders and com-7's seller-marked delivery. Nothing in logs or metrics names the courier in use.
- **Fix:** Founder decision, with an action outside the repo: either sign the Shiprocket contract and seed `const:shiprocket` with credentials and a webhook token or HMAC, or make manual shipping a named mode (`COMMERCE_MANUAL_SHIPPING=true`, a documented flat fee from configuration, fulfilment skips booking) and drop the stub from prod. Either way, refuse `stub` at boot when `ENV` is managed unless the named flag is set, refuse `shiprocket` with empty credentials or no webhook secret, log `courier provider=<name>`, and correct the false comments.
- **Acceptance:** A boot test: `ENV=prod COURIER_PROVIDER=stub` exits non-zero (fails now), or boots only with the named flag. With `COMMERCE_MANUAL_SHIPPING=true` a paid order has no shipment row. The manifest's prod value is not `stub`, or the flat fee is documented as policy.
- **Effort:** S
- **Was:** COM-E-4, COM-V3-1, COM-B-10

### com-6

- **ID:** com-6
- **Title:** There is no way to refund a buyer after shipping, no partial refund anywhere, and no admin or dispute tool; with com-1 the only refund path at launch, a pre-ship cancel, is unreachable after payment.
- **Severity:** P1
- **Category:** functional (refunds, admin)
- **Evidence level:** VERIFIED-READ (Track A; confirmed by v1)
- **Evidence:**
  - Returns fenced (`H/handler_p0.go:52-58`). `delivered → refund_pending` and `shipped → cancelled` are admin-only rows (`010_gst_and_state_machine.sql:113,117`) and no service code acts as `admin`.
  - The only refund primitive is payments' service-family `POST /v1/payments/internal/intents/:id/refund`, callable by commerce or food with a service token (HEAD `PAY/internal/http/handler.go:240`; prod `SERVICE_CALLERS: 'commerce-service,food-service'`). No human can reach it.
  - payments' admin can only resolve existing refund commands (HEAD `admin_routes.go:8`); admin-service has no order cancel or refund route (map §2.3). Nothing sets `disputed`; no Razorpay dispute event is mapped (`PAY/internal/service/p0.go:65-161`).
- **How to reproduce:** A buyer receives a damaged item. There is no Return button (fenced), support has no console action, and the only code path is SQL or an internal-key call.
- **Impact:** Every post-shipment complaint, wrong item or chargeback needs a database hand-edit or a Razorpay dashboard refund outside payments' ledger; disputes are invisible to the order.
- **Fix:** Founder decision on the launch refund policy beyond cancel. Smallest: an admin-token route `POST /internal/admin/orders/:id/refund {amount_minor, reason}` (permission `commerce:refunds.issue`, step-up, two-person above a threshold) that writes a scoped refund command for at most the unrefunded balance and moves `delivered|shipped → refund_pending` as `admin`; surface it in admin-service. Map Razorpay `payment.dispute.*` to `disputed` later. Needs com-8 for the admin path.
- **Acceptance:** Integration tests: full and partial admin refund of a delivered order create one refund command each, never exceed the captured amount, and settle to `refunded` / `partially_refunded` (fails now: no route).
- **Effort:** M
- **Was:** COM-A-2

### com-7

- **ID:** com-7
- **Title:** A seller goes live with no verified identity, and a buyer of a seller who never ships has no in-app route to a refund: under the stub courier the seller can record any tracking number and mark the order delivered.
- **Severity:** P1
- **Category:** fraud / payment
- **Evidence level:** VERIFIED-READ (each step; v2). The track's SUSPECTED part (prod runs the stub) is now the seeded value (com-5)
- **Evidence:**
  - Approve never reads `verification_status` (`P/onboarding.go:337-361`, `S/onboarding.go:341-361`).
  - `svc.WithKYC(kyc.StubValidator{})` is unconditional (`C/cmd/server/main.go:358`); the stub can reach only `format_ok`, never `verified` (`S/onboarding.go:443-465`).
  - Reviewers cannot see documents: the view is admin-token only and the console cannot reach commerce (com-8).
  - Under the stub, the seller's typed courier and AWB are stored (`S/fulfilment.go:62-81`), and the seller can mark shipped orders delivered (`S/seller_delivery.go:98-150`).
  - Buyer cancel after shipment answers 409 `CANCEL_NOT_PERMITTED` (`H/handler_p0.go:763`); returns fenced (`:110-118`); no admin cancel or refund route (com-6).
- **How to reproduce:** Under the stub courier: register a seller with a well-formed fictitious PAN and bank account; an admin approves; list a product; a buyer pays; the seller sends `POST /seller/orders/:id/ship {"courier":"x","tracking_number":"y"}`, then `/mark-delivered`. The buyer's cancel returns 409 and no return or dispute route exists.
- **Impact:** Buyer money is lost in-app; only an out-of-band Razorpay refund returns it. Payouts do not run yet (com-11), so the platform still holds the money; it becomes direct loss once payouts exist.
- **Fix:** (1) Approve refuses unless `verification_status` is `verified`, or an explicit, audited override records that the reviewer viewed the documents (founder decision: KYC vendor or manual verification). (2) Refuse the stub courier in managed ENVs (com-5). (3) An admin cancel-and-refund route for shipped and delivered orders (com-6).
- **Acceptance:** Tests: approve of a `format_ok` seller is refused (fails now); a prod-ENV boot with the stub exits (com-5); an admin refund of a delivered order writes one refund command (com-6).
- **Effort:** M
- **Was:** COM-B-4

### com-8

- **ID:** com-8
- **Title:** Seller approval and product moderation have no working tool in any Helm environment: the admin console gets 503 from admin-service, commerce's admin-token routes answer 401, and auto-approve is off, so no seller or listing can go live without an in-cluster internal-key call.
- **Severity:** P1
- **Category:** functional (seller onboarding, admin)
- **Evidence level:** VERIFIED-READ (Track A; confirmed with corrections by v2)
- **Evidence:**
  - `SERVICE_CALLERS` and `SERVICE_CALLER_ADMIN_SERVICE_OPS` are commented out in all five commerce Helm values files (qa `:163`, staging `:83`, prod `:158-160`, azure-prod `:58`, azure-staging `:49`). `H/admin_token.go:121-171` refuses with 401 when no caller is configured.
  - admin-service's `ADMIN_SERVICE_TOKEN_KEY` is commented out (`DV/admin-service/values-prod.yaml:47-61`), so admin-service never calls commerce and answers 503 `PRODUCT_UNAVAILABLE` (`admin-service/cmd/server/main.go:130`) (v2 correction).
  - `COMMERCE_PRODUCT_AUTO_APPROVE: "false"` in prod and qa, unset (false) in staging (`C/cmd/server/main.go:333`). The local docker-compose stack defaults it to true (`Architecture/docker/docker-compose.yml:2028`), so this holds for the Helm environments only.
  - The internal-key group (`H/handler_onboarding.go:36-73`) works only in-cluster, with an unverified actor (com-9).
- **How to reproduce:** On qa, submit a complete seller application and open the console's MStore sellers queue: 503.
- **Impact:** MSeller cannot onboard anyone at launch without engineers; approvals done by `curl` bypass step-up and the admin audit log. KYC documents cannot be viewed (com-7); payments operator screens are equally unreachable (com-2).
- **Fix:** Apply the documented Wave 2 order. Outside the repo: seed admin-service's signing key and add its caller public key to commerce's (and payments') ExternalSecret. Then uncomment the env lines in each commerce values file, then admin-service's (`values-prod.yaml:93-94` comment).
- **Acceptance:** A render test that commerce prod and qa set `SERVICE_CALLERS` including `admin-service` (fails now). On qa the console approves a seller and a product and `commerce_admin_audit_log` has both rows.
- **Effort:** S
- **Was:** COM-A-3

### com-9

- **ID:** com-9
- **Title:** Seller approve, KYC verify, product approve and COD settle are callable by any pod holding the shared internal key, as any actor named in `X-User-Id`, bypassing admin-service's step-up, two-person rule and audit.
- **Severity:** P1
- **Category:** authentication / service-to-service
- **Evidence level:** VERIFIED-READ (Track B; confirmed by v2)
- **Evidence:**
  - The IK group (`H/handler_onboarding.go:36-67`) holds sellers approve, reject, request-changes, suspend, unsuspend, `kyc/verify`; `payouts/pending`; products approve and reject; `cod-remittances/:id/settle` (`H/handler_seller.go:106-113`); attribute and category publishing.
  - `requireActor` falls back to `X-User-Id` when no admin token is present; the key compare is the only gate (`H/internal_guard.go:42-74`).
  - The key is one `random_password` (`infra/terraform/modules/auth-keys/main.tf:25,65`) referenced by 158 HEAD values files, and is also the gateway-trust key on every user route (`H/gatewaytrust.go:63-114`).
  - The edge refuses `/internal/` (`api-gateway/pkg/internalroutes/internalroutes.go:21,192-201`), so this needs an in-cluster foothold. Commerce has no NetworkPolicy (only payments has one).
  - Even in admin-service, seller approve and product approve have no step-up (HEAD `admin-service/internal/http/handler_commerce.go`).
- **How to reproduce:** From any pod: `POST http://commerce-service:8109/v1/commerce/internal/sellers/<id>/approve` with `X-Internal-Service-Key` and `X-User-Id:<any uuid>`. The seller goes live and gets the seller role; the audit row names the invented actor.
- **Impact:** Any compromised service can put a seller live, mark KYC checked, approve listings and settle remittances, with a falsely attributed audit trail.
- **Fix:** Enable the admin-token path (com-8), then remove the money and identity actions from the IK group, leaving only service reads (search docs, media-access). Add a commerce NetworkPolicy (enforcement needs reels-6). Add step-up to approve in admin-service.
- **Acceptance:** A key-only `POST /internal/sellers/:id/approve` returns 404 or 401 (fails now: 200). The console's approve works through the admin token. A chart render test shows the commerce NetworkPolicy.
- **Effort:** M
- **Was:** COM-B-3

### com-10

- **ID:** com-10
- **Title:** The courier booking declares a package value of ₹0 and item prices of ₹0, because it reads the legacy rupee columns that the P0 checkout writes as zero.
- **Severity:** P1
- **Category:** functional (shipping), money
- **Evidence level:** VERIFIED-READ for what is sent; SUSPECTED for the courier's response (v1)
- **Evidence:**
  - Checkout writes the order rupee columns as 0 (`P/checkout.go:544`) and `unit_mrp, unit_price, discount_amount, tax_amount, final_price` as 0 (`:609`); no trigger backfills them.
  - `GetOrderItems` scans both families (`P/orders.go:383-410`); the booking uses `packageValue += it.FinalPrice`, `Price: it.UnitPrice` (`S/shipments.go:239-242`), sent as Shiprocket `sub_total` and `selling_price` (`C/internal/courier/shiprocket.go:133,154`).
  - The invoice was fixed for the same defect (`S/shipments.go:792-797`); the booking was not.
- **How to reproduce:** On a Shiprocket-backed environment, pay an order and read the Shiprocket order: sub-total ₹0, or the job dead-letters with a validation error and no shipment is booked.
- **Impact:** No effect under the stub. With Shiprocket, either no shipment can be booked or parcels travel declared at ₹0: no transit-insurance value, wrong e-way-bill threshold, courier invoice mismatch.
- **Fix:** In `CreateShipmentsForOrder`, use `it.FinalPriceMinor` and `it.UnitPriceMinor` converted to rupees at the adapter edge; same in `recordCODRemittance` before COD is ever unfenced.
- **Acceptance:** A unit test with a fake courier: the request's `PackageValue` equals the seller's lines' `final_price_minor/100` for a P0 order (fails now: 0). One Shiprocket sandbox booking reads back the right declared value.
- **Effort:** S
- **Was:** COM-A-4

### com-11

- **ID:** com-11
- **Title:** Seller proceeds: no payable ledger is written, no payout path exists, and the only earnings derivation reads ₹0 columns with float maths.
- **Severity:** P1
- **Category:** payouts / ledger
- **Evidence level:** VERIFIED-READ (Track C; confirmed by v1)
- **Evidence:**
  - Every capture lands in the platform's Razorpay account; the seller is only the intent's `PayeeID` (`S/checkout_p0.go:401-405`).
  - The paid transaction writes stock, status, fulfilment job and outbox only (`P/p0.go:312-356`). `CreatePayoutBatch` has no caller (`P/seller_payouts.go:183`). No other service settles commerce sellers (monetization's RazorpayX rail serves creators only).
  - Earnings are a read-time derivation from `row.Item.FinalPrice`, the NUMERIC column written as 0, with float `round2` (`S/seller_account.go:122-136,176,184`).
  - `COMMERCE_COMMISSION_PCT`, `COMMERCE_PLATFORM_FEE_PCT`, `COMMERCE_TDS_PCT` are unset everywhere. Earnings and payouts are fenced (`H/handler_p0.go:49-50`).
- **How to reproduce:** Unfence `/seller/earnings` on a dev database with one paid P0 order: every row shows gross 0.
- **Impact:** Sellers cannot be paid from system data; any payout built on the existing derivation would pay ₹0; commission and TDS/TCS (s.194-O, s.52) are undefined. Payouts would be manual from day one.
- **Fix:** Founder decision first: merchant of record, commission, TCS and the payout rail (Razorpay Route or RazorpayX, an account outside the repo). Then write a `seller_payables` row in minor units (gross from `order_items.net_inclusive_minor` per seller, commission and TCS as integer basis points) in the capture transaction, its reversal in cancel and refund settlement, and make earnings read `*_minor`.
- **Acceptance:** After paid → cancel → refund the ledger sum per seller is 0; after paid → delivered it equals the stored net (fails now: no ledger). The money-type gate covers the new file.
- **Effort:** M
- **Was:** COM-C-2

### com-12

- **ID:** com-12
- **Title:** GST invoices: one platform-wide number series for all sellers taken outside the invoice transaction (gaps and orphaned documents), no place of supply, the buyer's street printed as the buyer's name, no seller street, FY from UTC, and no credit note when an invoiced order is cancelled.
- **Severity:** P1
- **Category:** GST / compliance
- **Evidence level:** VERIFIED-READ (Track C; confirmed by v1)
- **Evidence:**
  - Format `PBK/<FY>/<seq6>` (`SH/invoice/generator.go:102-105`) on `invoice_sequences` keyed by FY only (`C/database/migrations/002_invoices_shipments.sql:4-8`); each seller is the supplier with its own GSTIN (`S/shipments.go:886-905`).
  - `NextInvoiceSequence` is its own statement (`P/shipments.go:17-27`), taken before render, upload and insert (`S/shipments.go:640-681`). A failure burns the number; two concurrent `IssueInvoice` calls (the job, `S/jobs.go:47`, and the seller's `POST /orders/:id/invoice`) both pass the existence check, both upload, and the second insert fails on `invoices.order_id UNIQUE`.
  - Commerce renders with `generator.go` `HTMLRenderer` (`:165-215`), which has no place of supply; the newer `SH/invoice/service.go:71-72,307` has it but commerce does not use it (v1).
  - `inv.Buyer = invoice.Party{Name: shipTo.Line1, …}` (`S/shipments.go:750-753,788`) although `ContactName` is available (`:145-149`). Seller address omits the street (`:894-903`). FY from server-local `time.Now()` (`generator.go:94-100`), UTC in the pod.
  - The invoice is issued at payment; a `confirmed` or `packed` order can still be cancelled and refunded; commerce has no credit-note code.
  - Tests: `invoice_money_test.go` and `invoice_issue_integration_test.go` assert paise, CGST/SGST/IGST and HSN only; nothing asserts seller GSTIN, place of supply, buyer details or sequence uniqueness under concurrency (Track F).
  - Books (PAN-only) sellers get a combined "Tax Invoice / Bill of Supply" with no GSTIN (`generator.go:215`): an adviser question.
- **How to reproduce:** On the CI database, call `IssueInvoice` twice concurrently for one paid order: `last_sequence` moves by 2, one invoice row exists, two objects are in the bucket. Download any invoice: "Bill to" shows the street.
- **Impact:** Invoices that fail Rule 46 (place of supply, recipient name, consecutive series per supplier), un-reversed output tax on cancelled orders, GSTR-1 mismatches.
- **Fix:** Take the sequence and insert the invoice in one transaction, upload after commit, key `invoice_sequences` by `(seller_id, financial_year)`; add place of supply (`orders.place_of_supply_state`) and the snapshot's contact name; compute FY in `Asia/Kolkata`; issue a credit note from `tax.RefundComponents` when a refund settles on an invoiced order. Founder action outside the repo: the tax adviser signs off the template (books sellers included).
- **Acceptance:** `TestGoodsInvoiceCarriesRule46Fields` (supplier GSTIN, place of supply with state code, buyer name, number format) fails now. Twenty concurrent issues give twenty distinct consecutive numbers per seller and FY. Cancelling an invoiced order produces a credit note whose components reverse the invoice exactly.
- **Effort:** M
- **Was:** COM-C-3, COM-A-12, COM-F-8

### com-13

- **ID:** com-13
- **Title:** Commerce and payments take no part in account deletion and offer no export (DPDP); auth-service reports an account purged while commerce still holds its addresses, reviews, seller PII and orders.
- **Severity:** P1
- **Category:** privacy
- **Evidence level:** VERIFIED-READ (Track B; confirmed by v2)
- **Evidence:**
  - `identity-platform/services/auth-service/internal/purge/worker.go:46`: `DefaultRequiredServices` lists neither commerce nor payments; no `REQUIRED_PURGE_SERVICES` override exists in `deploy/` or compose.
  - `C/internal/consumers/` holds only payments consumers; `git grep` for purge, deletion, `user.purged`, anonymise finds nothing in commerce or payments.
  - What stays: addresses (plaintext until gated 1000), carts, favourites, reactions, reviews with `reviewer_id` and text, seller rows with PAN and bank ciphertext (and plaintext under `dual`, com-32), seller documents, order snapshots.
  - Export: the only commerce export is the fenced seller earnings CSV; admin-service `exports.go` pulls nothing from commerce.
  - **Tax retention duty (v2):** orders, invoices and GST records must be kept: CGST Act s.36 with Rule 56 requires roughly 72 months from the annual-return due date, and DPDP s.8(7) permits retention that law requires. The correct design is a partial purge; full deletion would be wrong.
- **How to reproduce:** Delete an account; after the purge reports complete, query `commerce_db` for the user id: every row above remains.
- **Impact:** DPDP erasure and access obligations are not met for the module. Could drop to P2 if launch precedes the DPDP Rules' operative date (not verified).
- **Fix:** A commerce purge consumer that deletes addresses, cart, favourites and reactions; anonymises reviews; crypto-shreds the seller's PII and KYC once no open order or settlement needs them; keeps orders, invoices and GST records under a documented tax-retention class with the user link reduced to an id; then acks. Add `commerce` (and `payments`) to `DefaultRequiredServices`. Add a JSON export of orders, addresses and reviews. Founder or legal decision: the retention class and period.
- **Acceptance:** An integration test purges a buyer and a seller and asserts the per-table result; the auth purge waits for the commerce ack (fails now: not required).
- **Effort:** L
- **Was:** COM-B-5

### com-14

- **ID:** com-14
- **Title:** The deployment cannot apply the gated contract migrations: the migrator is not in the image and the Job is off; the runner would apply `1000` before `998` and abort at `dual`; and `999` refuses for ever because every P0 order is "drift". No deployed database gets the order-transition trigger, the provider-reference uniqueness or the money NOT NULLs.
- **Severity:** P1
- **Category:** deployment / data integrity
- **Evidence level:** VERIFIED-READ (Tracks E and C; confirmed by v1). Live database state UNVERIFIED
- **Evidence:**
  - Images build only `/server` (`C/Dockerfile:20`, `PAY/Dockerfile:20`); `migrationJob.enabled: false` with "run by hand" (`DV/commerce-service/values-prod.yaml:165-182`, qa `:171-185`); even enabled, its command has no `-gated` flag. Boot applies only `migrations/` (`C/cmd/server/main.go:112` → `P/bootstrap.go:14-22`).
  - `listMigrationFiles` uses `sort.Strings` (`SH/store/migrationrunner/runner.go:132`, `exclusive.go:54`), so `1000_*`-`1002_*` run before `998_*`. `1000` raises unless `ciphertext_authoritative_since` is set (`gated/1000_pii_plaintext_scrub.sql:64-79`); every values file runs `COMMERCE_PII_CUTOVER: 'dual'`; the runner aborts on the first failure.
  - `999` refuses while `money_minor_drift` has rows (`gated/999_validate_and_tighten.sql:34-44`); the view compares `final_amount_minor` with `ROUND(final_amount*100)` (`migrations/007_money_minor.sql:160-176`) and the P0 checkout writes `final_amount = 0`, so every order is drift.
  - payments gated files have no migrator (psql by hand, `docs/runbooks/payments-applications.md:153`); no runbook covers the commerce gated set. CI applies 998 only, to `commerce_p0_test`.
  - What is lost: `trg_order_transition` (`998:74`; `P/order_fulfilment.go:3-9` says nothing else guards `UPDATE orders SET status`), the prepaid-only CHECK, the fence triggers, the money NOT NULLs, the inventory ledger reconciliation view (`999:95-109`, the only real data loss), and payments' unique provider reference, whose header says a phantom duplicate can settle the wrong order (`PAY/database/gated/999_provider_reference_uniqueness.sql:8-13`).
  - Severity note (v1): the Go paths still check the matrix (`transitionAllowedTx`) and payments refuses ambiguous provider matches in code, so this is a lost backstop, not an open hole today.
- **How to reproduce:** `printf '998_a\n999_b\n1000_c\n' | LC_ALL=C sort` prints `1000_c` first. On a scratch DB at `dual`, `go run ./services/commerce-service/cmd/migrate -gated` fails on 1000 and records nothing. On any DB with one P0 order, `SELECT count(*) FROM money_minor_drift` > 0.
- **Impact:** Production launches with the state machine, payment-method narrowing, fences and provider-reference uniqueness enforced in code only, and with no ledger-vs-stock reconciliation; nobody can apply the fix without a hand-built binary against the private Aurora endpoint.
- **Fix:** Rename the commerce gated files to sort correctly (`0998_`, `0999_`) or sort numerically, and let `-gated` take an explicit list (`-only 998,999`) so the PII scrubs run separately. Redefine `money_minor_drift` in a boot migration to exclude P0 rows whose NUMERIC mirror is 0, and move the reconciliation view to a boot migration. Build `/commerce-migrate` into the image. Write a runbook. Outside the repo: run `998,999` and the two payments files once on the fresh prod database before opening the shop, as a one-off Job using the service's ExternalSecret.
- **Acceptance:** A unit test that `listMigrationFiles` on `C/database/gated` returns 998 before 999 before 1000 (fails now). CI applies 998 then 999 to `commerce_p0_test` after a journey test and both succeed (fails now on drift). On qa, `schema_migrations` lists the gated 998 and 999 and `\d orders` shows `trg_order_transition`.
- **Effort:** S (rename, view, Dockerfile) + S (runbook and Job)
- **Was:** COM-E-1, COM-C-4

### com-15

- **ID:** com-15
- **Title:** No commerce or payments alert exists beyond the chart's generic pod and service-wide HTTP rules; six commerce money metrics are never incremented; payments has no webhook or reconciliation metrics; poison payment events go to a DLQ nobody reads; and parked commerce refunds have no operator path.
- **Severity:** P1
- **Category:** observability
- **Evidence level:** VERIFIED-READ (Tracks E and C; confirmed by v3)
- **Evidence:**
  - No `prometheusRule` in any of the ten `DV/{commerce,payments}-service/values-*.yaml`; only feed and media values have service rules; `infra/terraform/modules/observability` has none for commerce or payments. The chart's defaults (crash, no ready pod, scrape down, service-wide 5xx > 5 % and p99 > 1 s, `CH/values.yaml:172-191`) do route to the ops e-mail topic.
  - `CheckoutTotal`, `CheckoutDuration`, `OversellAttempt`, `RefundInitiateFailed`, `OrderTransitionRejected`, `QuoteRejected` occur only in `C/internal/obs/metrics.go:30-90`; only `PaymentApplied`, `DuplicateSuppressedTotal` and `AmountMismatchTotal` are incremented (`payments_p0.go:213,221,377,387,400`).
  - payments registers exactly two business metrics (`PAY/internal/obs/metrics.go:29-40`); `PaymentsRefundNeedsAttention` exists only in the dev compose Prometheus with no Alertmanager.
  - Poison payment events go to `social.events.v1.dlq` (`payments_p0.go:390-410`) with no consumer and no alert. Commerce refund commands parked `needs_attention` (`P/p0.go:1200-1219`) have no route in commerce or admin-service. Gauges `OldestUnsettledRefundAge` and `OldestLiveReservationAge` exist but nothing pages on them.
- **How to reproduce:** `git grep -n "OversellAttempt\|CheckoutTotal" HEAD -- Architecture/services/commerce-service` matches only `metrics.go`; `git grep -n prometheusRule HEAD -- deploy/services/commerce-service deploy/services/payments-service` returns nothing.
- **Impact:** An amount mismatch, a stuck refund, an outbox backlog, a stalled `RetryForever` consumer, webhooks rejected after a secret rotation and a dead-lettered fulfilment job reach a person only through a buyer complaint. A checkout route failing at 100 % stays under a 5 % service-wide ratio.
- **Fix:** A `prometheusRule` block in commerce and payments prod and qa values: amount mismatch increase > 0 (critical); refund pending age > 1 h; reservation age > 40 min; outbox unpublished > 100 for 10 m; consumer lag > 1000 for 10 m; DLQ growth; `payments_refunds_needs_attention > 0`; unresolved `refund_required` > 1 h (com-2); a 5xx ratio scoped to checkout and payment routes. Increment the six dead instruments; add `payments_webhook_total{result}`. Add a read-only operator list of commerce `needs_attention` commands behind the admin token. Outside the repo: confirm the ops SNS e-mail subscription (feed-10).
- **Acceptance:** `helm template` for commerce and payments prod renders the rules (fails now). A unit test per dead metric shows it increments. On qa a forced mismatch produces an e-mail.
- **Effort:** M
- **Was:** COM-E-3, COM-C-7

### com-16

- **ID:** com-16
- **Title:** No `statement_timeout`, `lock_timeout` or `idle_in_transaction_session_timeout` exists at any level, so checkouts queued on a hot row each hold one of the pod's 25 pool connections for as long as the wait lasts, and browsing on that pod stalls with them.
- **Severity:** P1
- **Category:** resilience / performance
- **Evidence level:** VERIFIED-READ (Track D; confirmed stronger by v3)
- **Evidence:**
  - Pool: `MaxConns` 25 by default, no `RuntimeParams` or `AfterConnect` (`C/cmd/server/main.go:66-78`); `POSTGRES_MAX_CONNS` not set in prod values.
  - Prod DSN template is `?sslmode=require` only (`tools/prodsecrets/resolve.go:573-597`); the Aurora parameter group sets logging only (`infra/terraform/modules/aurora/main.tf:113-150`); repo-wide `git grep statement_timeout` matches only dating and media files (v3).
  - Every catalogue read, checkout, quote and worker shares the one pool. The 30 s HTTP `WriteTimeout` does not cancel the handler's context (`SH/server/server.go:16`); the gateway proxies with `http.DefaultTransport`, no response-header timeout (`api-gateway/cmd/server/main.go:399`).
- **How to reproduce:** In an integration test, hold `SELECT … FROM products WHERE id=$1 FOR UPDATE` open, fire 30 checkouts for that product at one instance, then `GET /products`: the browse waits for a pool connection.
- **Impact:** A hot product or a stuck transaction becomes a pod-wide browsing outage; HPA scales on CPU, so a lock-bound stall does not scale out.
- **Fix:** Set `statement_timeout = 5s`, `lock_timeout = 2s`, `idle_in_transaction_session_timeout = 15s` in the pool's `RuntimeParams`; map `lock_not_available` / `query_canceled` on checkout to a retryable 503 `CHECKOUT_BUSY`. Optionally a smaller separate pool for checkout. Platform-wide: `ResponseHeaderTimeout` on the gateway transport.
- **Acceptance:** A unit test that the pool config carries the three parameters (fails now). The integration test above: checkouts fail fast with 503 and the browse request completes.
- **Effort:** S
- **Was:** COM-D-3

### com-17

- **ID:** com-17
- **Title:** No load test exists, or has ever been run, for any commerce or payments endpoint, the checkout transaction, a flash sale or a webhook burst, and there is no benchmark; the dev smoke stops at the quote. Every capacity statement about MStore or MSeller is unproven.
- **Severity:** P1
- **Category:** tests / performance
- **Evidence level:** VERIFIED-READ (v3; the track's repository search is the same evidence)
- **Evidence:**
  - Load tests in the repo: `dating-service/loadtest/*.js` (3), `rider-service/loadtest/*.js` (3), `Architecture/tools/loadtest/{lib,long-video,reels}`; none names `/v1/commerce` or payments. pb-echo has none.
  - No `func Benchmark` in commerce, payments, `SH/outbox` or `SH/kafka`.
  - `scripts/smoke/dev-smoke.mjs:182,884-952` ends at `shop.quote`. The k6 guard `Architecture/tools/loadtest/lib/guard.js` is reusable.
- **How to reproduce:** `git ls-files | grep -iE "k6|loadtest|bench"` and grep the hits for `commerce`.
- **Impact:** The hot-product ceiling (com-50), image authorisation latency (com-49), the carrier's limit (com-48), the home page cost (com-51), confirmation lag (com-63) and the fulfilment and expiry drains (com-59, com-60) are unknown numbers. The C1 proof shows correctness at 50 goroutines, not throughput.
- **Fix:** Write and run Track D section 6's L1 to L9 (browse, search, flash sale, payment start and confirm, webhook burst, fulfilment drain, seller surfaces, image resolution, soak) with k6 under `Architecture/tools/loadtest/commerce/` using `assertSafeTarget`, against a QA stack sized like production (outside the repo: feed-02) with Razorpay and Shiprocket fakes; commit the results. Extend the dev smoke to checkout and intent, stopping before payment.
- **Acceptance:** Scripts exist; a dated results file in this folder records p50/p95/p99, error rate, CPU, statements per second, lock waits, consumer lag and queue ages per scenario and meets the Track D thresholds (none exists now).
- **Effort:** L
- **Was:** COM-D-22, COM-F-9

### com-18

- **ID:** com-18
- **Title:** No test at any layer drives a real signed Razorpay webhook through payments-service, the outbox and Kafka to a commerce order becoming paid; the e2e settles only through the stub confirm.
- **Severity:** P1
- **Category:** tests
- **Evidence level:** VERIFIED-READ (v3); the e2e log was VERIFIED-RUN in Track F
- **Evidence:**
  - `Architecture/tools/integration/commerce_journey_test.go:208-216` and `commerce_prepaid_test.go:104-106` pay through `/payment/confirm` with `gateway=stub`; `commerce_prepaid_test.go:34-36,139-165` asserts the webhook answers 503 on a stub stack and says the signed path "is not exercised here".
  - `Razorpay-Signature` appears in tests only inside payments (`provider_contract_test.go`, `handler_test.go`, `moneytruth_c3_integration_test.go`, `offers_fake_integration_test.go`); payments stops at `ApplyWebhook` (`moneytruth_c3 :236-276`). Commerce injects `PaymentSucceeded` structs into the store. The seam is covered by decode unit tests only (`SH/paymentevents/events_test.go:70`).
- **How to reproduce:** `grep -n "Razorpay-Signature" Architecture/tools/integration/*.go` returns nothing.
- **Impact:** The deployed paid path is never run end to end. A change to the event shape, partition key, `application_id`, consumer group or the `reference_type` filter could stop every real order being confirmed with CI green. Journey 2 depends on exactly this path.
- **Fix:** An e2e commerce variant with payments in `razorpay` mode against an `httptest` fake of the Razorpay API and a test webhook secret: `TestE2E_Commerce_SignedWebhookPays` (checkout, intent, signed `payment.captured`, poll `/payment/status` until `paid` with no client confirm), then the same body under a new event id (one `commerce.order.paid`), a body 1 paisa short (order stays `payment_pending`, owed capture recorded), a bad signature (401). Add com-64's cancel → `refunded` follow-through in the same domain.
- **Acceptance:** Green in CI; red if the commerce payments consumer is not started or the amount comparison is removed from `VerifyProviderMoney`.
- **Effort:** M
- **Was:** COM-F-1

### com-19

- **ID:** com-19
- **Title:** The courier webhook is unauthenticated as far as tests know: `ShiprocketCourier.VerifyWebhook`, both webhook routes and the stub-in-prod case have no test.
- **Severity:** P1
- **Category:** tests / security
- **Evidence level:** VERIFIED-READ (Track F; confirmed by v2)
- **Evidence:**
  - `C/internal/courier/shiprocket.go:63-89` refuses when no secret is set and compares token or HMAC in constant time; routes at `H/shipments.go:21,28`, 401 on a verify error (`:234-236`).
  - `C/internal/courier` holds only `shiprocket_etd_test.go` and `shiprocket_rate_test.go`; a repo-wide `git grep` for `VerifyWebhook`, `shipments/webhooks`, `courier-callback` in tests finds only two doubles that return nil (`checkout_journey_integration_test.go:67`, `contracts_integration_test.go:245`).
  - Under the stub, `ParseWebhook` returns no updates, so an unsigned stub call changes nothing (v2); the risk is a regression on the Shiprocket path.
- **How to reproduce:** The grep above; `ls Architecture/services/commerce-service/internal/courier`.
- **Impact:** Courier updates move orders to `shipped`, `out_for_delivery` and `delivered`, and `delivered` unlocks reviews. A regression that accepts an unsigned callback would let anyone mark orders delivered with CI green.
- **Fix:** `shiprocket_webhook_test.go`: no secrets → error; token only (right token in `X-Api-Key`, `Token`, `X-Token`, mixed case) passes, wrong or missing fails; HMAC only, right signature passes, wrong body fails. A route test: bad token → 401 and no `shipment_events` row; a courier name not matching the configured one → refused. The boot test from com-5.
- **Acceptance:** Each test fails when its branch of `VerifyWebhook` is removed (record the mutations).
- **Effort:** S
- **Was:** COM-F-2

### com-20

- **ID:** com-20
- **Title:** Object-id isolation is untested on the invoice routes, shipment reads, order items and seller order detail; the authorization checks exist on all four, but nothing would catch their removal.
- **Severity:** P1
- **Category:** tests / security
- **Evidence level:** VERIFIED-READ (Track F; v2 confirmed as a test gap only)
- **Evidence:**
  - Checks exist (v2): invoice GET and POST go through `requireOrderRole` (`H/shipments.go:183-215`; `OrderActor`, `S/order_lifecycle.go:51-79`); shipment GETs too (`:132-160`); `GET /orders/:id/items` is customer-only with 404 (`S/orders_read.go:129-143`); `GET /seller/orders/:id` uses `sellerForCaller` plus `sellerLines` (`:289-298`).
  - Tests: the only invoice test is the buyer's own 200 (`contracts_fixtures_integration_test.go:675-676`); items, the buyer only (`:670`); seller detail, the owning seller only (`:908`, `ownorder_integration_test.go:342-347`); shipment GET has no stranger case. The comment at `H/shipments.go:199-200` says GET invoice "was previously unauthenticated".
- **How to reproduce:** `git grep -n "/invoice\|/items\"" -- 'Architecture/services/commerce-service/*_test.go'`.
- **Impact:** The invoice carries the buyer's name and address and a 7-day presigned URL. Dropping `requireOrderRole` again, or loosening `OrderActor`, would leak them to any signed-in user with an order id, and no test would fail.
- **Fix:** One table-driven HTTP integration test over every `/orders/:id/*` and `/seller/orders/:id*` route with five callers (owner buyer, owning seller, another buyer, another seller, no seller row): assert the status, that no `download_url` or address is in the body, and that a refused POST creates no invoice row.
- **Acceptance:** Fails when `requireOrderRole` is removed from `GetInvoice`, or when the seller filter in `S/orders_read.go:295-297` is removed (record both mutations).
- **Effort:** S
- **Was:** COM-F-3

### com-21

- **ID:** com-21
- **Title:** The commerce P0 gate (`integration-commerce.yml`) has not run for 17 days and 19 commerce and payments commits; the gated-998 proofs and the named-PASS checks run nowhere else.
- **Severity:** P1
- **Category:** tests / CI
- **Evidence level:** VERIFIED-RUN (`gh`; Track F, confirmed by v3)
- **Evidence:**
  - Last run 41162319116 @ `545baf5f0` on 2026-10-31 (workflow_dispatch). Triggers: `workflow_dispatch`, `pull_request` on commerce and payments paths, `push` to `main` (`.github/workflows/integration-commerce.yml:15-30`). This branch has no open PR.
  - `git log 545baf5f0..HEAD` lists 19 commerce and payments commits, including refund and duplicate-capture logic (`2f5679693`, `00685ad32`, `2166d3141`, `0a57631fd`).
  - integration-all skips the four gated proofs (`scripts/ci/integration-services.yaml:162-163`) and prints no test names; only integration-commerce asserts named `--- PASS` lines and that the C1 negative control reproduced the oversell (`:822-851`).
- **How to reproduce:** `gh run list --workflow integration-commerce.yml --limit 5`.
- **Impact:** The DB-level state-machine proof, the fence triggers, the money-type gate and the oversell proof could be broken today without anyone seeing it.
- **Fix:** Add `push` on `codex/**` and `feat/**` with the same paths filter (as `integration-all.yml:21-26`), or fold the commerce-proofs job into integration-all as a gated-998 extra database.
- **Acceptance:** A run exists for the current HEAD (none now); a commit that breaks `TestProofIllegalOrderTransitionRejected` turns the branch red.
- **Effort:** XS
- **Was:** COM-F-4

### com-22

- **ID:** com-22
- **Title:** Razorpay orders are created without `payment_capture` and nothing ever captures an authorised payment, so payments complete only if the Razorpay account auto-captures. **P0 if the Razorpay account uses manual capture.**
- **Severity:** P1 (SUSPECTED; P0 if the account uses manual capture)
- **Category:** functional (payments), configuration
- **Evidence level:** SUSPECTED (v1: stays SUSPECTED; the deciding fact is outside the code)
- **Evidence:**
  - `createOrder` sends amount, currency, receipt, notes and offers, no `payment_capture` (`PAY/internal/gateway/razorpay_provider.go:128-145`); the provider declares `ManualCapture: true` (`:102`).
  - `Capture` (`:298-307`) has no non-test caller in any service (v1 `git grep` of HEAD).
  - Only `payment.captured` / `order.paid` set `succeeded` (`PAY/internal/service/p0.go:79-81`); `payment.authorized` changes nothing (`:857`); the reconciler treats `authorized` as non-terminal (`razorpay_provider.go:511`, `reconcileOutcome`).
- **How to reproduce:** With manual capture on the account, pay: the payment sits `authorized`, the order expires after 20 minutes, and Razorpay auto-refunds days later.
- **Impact:** If the account is on manual capture, every order fails silently while buyers see a debit.
- **What confirms it:** the prod Razorpay dashboard's payment-capture setting (outside the repo), and one test-mode payment on qa that reaches `payment.captured` without a manual capture call.
- **Fix:** Send `"payment_capture": 1` on order creation, or a readiness check that reads the account setting.
- **Acceptance:** A gateway test asserting the order body carries `payment_capture: 1` (fails now).
- **Effort:** S
- **Was:** COM-A-13

---

## P2

### com-23

- **ID:** com-23
- **Title:** Razorpay live or test mode is enforced only by the secrets seeder; payments-service accepts any key in prod and logs "production credentials" whatever the key; the prod values comment says the seeder stores the TEST pair, which the seeder forbids.
- **Severity:** P2
- **Category:** deployment / configuration
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `tools/prodsecrets/env.go:31-45` accepts only `rzp_live_` for prod and `rzp_test_` elsewhere; an empty value passes (`:52-53`).
  - `DV/payments-service/values-prod.yaml:8-11` says the seeder stores the Razorpay TEST key pair in prod; the map (§7, §8) repeated it. **Correction:** "prod runs Razorpay test keys" is not established.
  - `PAY/internal/config/config.go:156-167` checks no prefix; with no key and no stub, boot fails with `ErrNoGateway`. `PAY/cmd/server/main.go:152` logs "production credentials detected" for any key.
- **How to reproduce:** Read the files above.
- **Impact:** A founder following the comment is refused by the seeder; a secret left empty "until approval" crash-loops payments and every pay step answers 500 (com-44); a test key written by hand makes test-card payments confirm real orders.
- **Fix:** At boot refuse a key whose prefix does not match `ENV` (`rzp_live_` iff prod) unless `PAYMENTS_TEST_MODE_IN_PROD=true`; log and expose `payments_provider_mode{mode}`; correct the values comment.
- **Acceptance:** A config test: `ENV=prod` with `rzp_test_…` fails (fails now). Boot log carries `mode=live`.
- **Effort:** S
- **Was:** COM-E-5 (corrects map §7/§8)

### com-24

- **ID:** com-24
- **Title:** Android buyers cannot apply a coupon, read or write reviews, download an invoice, follow courier tracking, edit, delete or default an address, or see bank offers, all of which the web offers.
- **Severity:** P2
- **Category:** functional (parity)
- **Evidence level:** VERIFIED-READ
- **Evidence:** The Android endpoint list (`API:49-530`) has no `/cart/coupons`, `/reviews`, `/invoice`, `/shipments`, `/addresses/{id}` or `/payment-offers`; `couponCode` is never set from the UI (`FC/checkout/CheckoutViewModel.kt:136-139`). Web: `features/shop/api/coupons.ts:17`, `reviews.ts`, `orders.ts`, `addresses.ts`, `offers.ts:15`. **Correction (v3):** Android does track local (seller-managed) deliveries; the gap is courier tracking and the other features.
- **How to reproduce:** Android MStore: checkout has no coupon field; order detail has no courier tracking or invoice; product shows no reviews.
- **Impact:** Breaks "Android is the super app"; Android buyers pay more than web buyers for the same cart.
- **Fix:** Coupon picker in checkout, courier tracking and invoice on order detail, reviews read and write, address edit/delete/default, in that order.
- **Acceptance:** ViewModel tests per feature against a fake API; the checkout test proves quote and checkout carry the same coupon.
- **Effort:** M
- **Was:** COM-A-6

### com-25

- **ID:** com-25
- **Title:** Sellers are notified only of new paid orders, and buyers are not told about late-capture or partial refunds.
- **Severity:** P2
- **Category:** functional (notifications)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `notification-service/internal/events/commerce_consumer.go:9-17` (paid → buyer and seller; shipped, delivered, cancelled, refunded, payment_failed → buyer only). Commerce emits `commerce.order.late_capture_refunded` (`P/p0.go:303`) and `partially_refunded`, which nothing consumes.
- **How to reproduce:** A buyer cancels a confirmed order: the seller gets no notice.
- **Impact:** Sellers pack cancelled orders; refunded buyers hear nothing and contact support.
- **Fix:** Seller notice on `commerce.order.cancelled`; buyer notices on `late_capture_refunded` and `partially_refunded`.
- **Acceptance:** Consumer tests: each event creates one inbox row for the right recipient.
- **Effort:** S
- **Was:** COM-A-11

### com-26

- **ID:** com-26
- **Title:** The seller dashboard shows ₹0 revenue for every seller, counts unpaid order lines as "orders today", runs eight sequential aggregates (one over all lifetime lines) and discards every error.
- **Severity:** P2
- **Category:** money read path / performance
- **Evidence level:** VERIFIED-READ
- **Evidence:** `P/onboarding.go:682-711`: four offer counts, a low-stock count, today's orders (item rows, unpaid included, `:699-703`), and `SUM(oi.final_price)` (`:704-707`), the NUMERIC column checkout writes as 0 (`P/checkout.go:608-609`); a float result; every `Scan` error discarded (`_ =`).
- **How to reproduce:** A seller with paid orders opens `GET /v1/commerce/dashboard`: revenue 0.
- **Impact:** A wrong financial figure is the seller's first number; latency grows with lifetime sales and a timeout reads as zero.
- **Fix:** `SUM(oi.net_inclusive_minor)` into `revenue_total_minor`; `COUNT(DISTINCT o.id)` with `payment_status IN ('paid','partially_refunded')`; counts in one statement with `FILTER`; return errors; later a per-seller daily roll-up.
- **Acceptance:** A store test with two paid orders of two lines returns the paise total and `orders_today = 2` (fails now: 0).
- **Effort:** S
- **Was:** COM-A-5, COM-C-5, COM-D-19

### com-27

- **ID:** com-27
- **Title:** Neither client offers the seller "mark delivered", so an order shipped with the seller's own courier cannot be completed from an app.
- **Severity:** P2
- **Category:** functional (fulfilment)
- **Evidence level:** VERIFIED-READ
- **Evidence:** Route `POST /seller/orders/:id/mark-delivered` (`H/handler_seller_fulfilment.go:139-166`); no call in Android (`API`, `feature/seller`) or web (`features/shop/model/sell.ts:1038-1046`: pack, ship, cancel only).
- **How to reproduce:** Once com-1 is fixed, ship with the seller's own courier: the order stays `shipped`.
- **Impact:** No delivered state, no review eligibility.
- **Fix:** Add the action to both seller order-detail screens when every shipment is `seller_managed`.
- **Acceptance:** Client tests: the button appears only for a seller-managed shipped order and posts the route.
- **Effort:** S
- **Was:** COM-A-9

### com-28

- **ID:** com-28
- **Title:** The Android seller hub shows Returns and Earnings tiles that call fenced routes and always land on an error screen.
- **Severity:** P2
- **Category:** functional (fenced feature exposure)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `FS/seller/SellerScreen.kt:271-272` → `FS/navigation/SellerNavigation.kt:146-147,164-178` → `GET seller/returns`, `seller/earnings` (`API:500-530`) → 404 by the fence (`H/handler_p0.go:49-58`); `FS/seller/SellerEarningsScreen.kt:60` renders `UsErrorState`.
- **How to reproduce:** Android MSeller → Earnings: error.
- **Impact:** Sellers read "something went wrong" about their money.
- **Fix:** Hide both tiles behind a capability until the fence lifts, or show "coming soon" on 404.
- **Acceptance:** A UI test: the hub shows neither tile when the capability is off.
- **Effort:** S
- **Was:** COM-A-8

### com-29

- **ID:** com-29
- **Title:** Android sellers cannot create coupons, see the dashboard, edit a product after creation, set attributes, reorder or delete images, or see readiness and action-needed.
- **Severity:** P2
- **Category:** functional (parity)
- **Evidence level:** VERIFIED-READ
- **Evidence:** Absent from `API:280-530`: `/seller/coupons`, `/dashboard`, `PATCH /products/{id}`, `/products/{id}/attributes`, `/media/order`, `DELETE /media/{id}`, `/products/{id}/readiness`, `/seller/action-needed`. Web has each (`features/shop/api/sellerCoupons.ts`, `sell.ts:56-333`, `sellMedia.ts`).
- **How to reproduce:** Android MSeller: a published listing's title cannot be edited.
- **Impact:** Parity rule; Android-only sellers need the web for routine catalogue work.
- **Fix:** Product edit and media management first, then readiness, dashboard, coupons.
- **Acceptance:** ViewModel tests per screen against a fake API.
- **Effort:** L
- **Was:** COM-A-7

### com-30

- **ID:** com-30
- **Title:** On a multi-seller order, any seller with one line reads the whole invoice (buyer name and address, the other sellers' lines) and every seller's shipments.
- **Severity:** P2 (latent: checkout refuses multi-seller carts today)
- **Category:** authorization (tenant scoping)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `OrderActor` (`S/order_lifecycle.go:51-79`) admits the customer or any seller with an item on the order and does not scope by seller; the invoice and shipment routes use it (`H/shipments.go:132-160,183-215`). Checkout refuses multi-seller carts (`P/checkout.go:921-928`), so no such order can be created now (v2).
- **How to reproduce:** Not reproducible until multi-seller checkout exists; on a hand-built two-seller order, seller B's `GET /orders/:id/invoice` returns seller A's lines.
- **Impact:** The day multi-seller carts ship, sellers see each other's sales and the buyer's details for lines that are not theirs.
- **Fix:** Scope shipment reads to the caller's seller id, and issue invoices per seller (com-12 keys the series by seller already).
- **Acceptance:** A test with a two-seller order: seller B sees only its own shipment and invoice.
- **Effort:** S
- **Was:** new (v2 side observation on COM-F-3)

### com-31

- **ID:** com-31
- **Title:** An approved seller can replace the payout bank account and business identity at any time with no re-verification, audit row, notice or test.
- **Severity:** P2
- **Category:** authorization / account-takeover hardening
- **Evidence level:** VERIFIED-READ
- **Evidence:** `PUT /onboarding/step/payout` (`H/handler_onboarding.go:311-339`) → `S/onboarding.go:251-262` → `P/onboarding.go:209-248` (no status read, no verification reset); `SaveOnboardingBasic` rewrites store name, owner, email, phone on an approved seller (`P/onboarding.go:132-143`); KYC re-upload leaves the seller approved (`:160-176`). No audit insert on these paths; no test asserts one (`git grep -il payout -- '*_test.go' | xargs grep -il audit`).
- **How to reproduce:** As an approved seller, PUT a new payout account: 204, seller stays approved and verified.
- **Impact:** Latent (no payouts yet, com-11); the day they run, one stolen seller session redirects the seller's money without trace.
- **Fix:** On an approved seller, a payout or identity change resets verification to pending, writes an audit row (old and new last4, no plaintext), notifies the seller, and requires step-up.
- **Acceptance:** An integration test: one audit row, no plaintext number, seller back in review for payouts (fails now).
- **Effort:** S-M
- **Was:** COM-B-6, COM-F-6

### com-32

- **ID:** com-32
- **Title:** Full bank account numbers and PANs are stored in plaintext in prod (KYC cutover at `dual`); cancelled-cheque document numbers (bank accounts) and passport, voter ID and DL numbers stay plaintext for good.
- **Severity:** P2
- **Category:** data protection at rest
- **Evidence level:** VERIFIED-READ
- **Evidence:** `DV/commerce-service/values-prod.yaml:154-156` sets both cutovers to `dual`; `S/kycpii.go:89`; `P/onboarding.go:221-235` writes the plaintext beside the ciphertext; the gated scrub 1002 cannot run from the image (com-14); `035_seller_kyc_pii_encryption.sql:226-234` refuses only Aadhaar-shaped numbers and exempts `cancelled_cheque`; `gated/1002_kyc_pii_plaintext_scrub.sql:145-149` nulls only Aadhaar.
- **How to reproduce:** Read the files above.
- **Impact:** A database or backup read exposes every seller's bank account and PAN.
- **Fix:** Launch the empty prod database directly at `ciphertext` for both scopes; do not store `document_number` for `cancelled_cheque`; seal or drop the other document numbers.
- **Acceptance:** A values test asserts prod is `ciphertext` (fails now); a cancelled-cheque document saves with a null number.
- **Effort:** S + S
- **Was:** COM-B-7

### com-33

- **ID:** com-33
- **Title:** Order and invoice events put the buyer's email and a 7-day presigned invoice link (name and address) on the shared topic.
- **Severity:** P2
- **Category:** PII in events
- **Evidence level:** VERIFIED-READ
- **Evidence:** `S/shipments.go:686-702` (`invoice_url` from `PresignedGetURL(…, 7*24h)`, `buyer_email`); `buyer_email` also at `S/checkout.go:207,225`, `S/order_lifecycle.go:157`, `S/shipments.go:329,604-608`; `social.events.v1` is readable with the one shared Kafka user (`infra/terraform/modules/msk/main.tf:11-17`).
- **How to reproduce:** Read the files above.
- **Impact:** Any service, log or DLQ sink of the topic gets buyer emails and working invoice links.
- **Fix:** Carry ids only; notification-service resolves the email and a short-lived URL at send time.
- **Acceptance:** An event contract test: no `buyer_email` or `invoice_url` in any payload.
- **Effort:** S
- **Was:** COM-B-8

### com-34

- **ID:** com-34
- **Title:** No cap on open unpaid orders per user, cart lines or line quantity, and no per-route limits on checkout, intent, coupon preview or reviews; coupon codes are guessable.
- **Severity:** P2
- **Category:** abuse / unbounded work in a lock
- **Evidence level:** VERIFIED-READ
- **Evidence:** Each checkout holds stock 20 min (`P/checkout.go:680-704`); quantity `min=1`, no max (`H/handler_cart.go:32-33`); no line cap (`S/cart.go:16-80`); no open-order cap (grep of `S/checkout_p0.go`, `P/checkout.go`, `P/cart_items.go`); `GET /cart/coupon-preview` distinguishes valid from invalid codes (`P/coupons.go:249-285`); in-process limits only on reaction, vote, share (`H/handler_engagement.go:45-114`); the edge allows 60 rps per user and fails open.
- **How to reproduce:** N scripted accounts check out a rival seller's whole stock and never pay; the listing shows out of stock, renewed every 20 minutes. A 1,000-line cart holds locks on hundreds of products for seconds.
- **Impact:** A competitor can deny a seller's sales; one buyer can block others on many products (com-50) and hold a pool connection (com-16); private coupon codes can be enumerated.
- **Fix:** Cap open `payment_pending` orders per user (e.g. 3), distinct lines (e.g. 50) and per-line quantity (e.g. 20) with 409 `CART_LIMIT`; per-user token buckets on checkout, intent, coupon preview and review POST.
- **Acceptance:** Tests: the 4th open checkout and the 51st line are refused; the coupon-preview limiter trips.
- **Effort:** S-M
- **Was:** COM-B-9, COM-D-11

### com-35

- **ID:** com-35
- **Title:** The payments NetworkPolicy admits the whole VPC CIDR and api-gateway; commerce has no NetworkPolicy; neither is enforced; the PSP webhook shares the edge's per-IP rate rule and its own WAF ACL cannot be attached.
- **Severity:** P2
- **Category:** network / ingress hardening
- **Evidence level:** VERIFIED-READ for configuration; SUSPECTED that pods get VPC IPs and that webhooks would be blocked under load
- **Evidence:** `DV/payments-service/values-prod.yaml:145-178` (`ingressControllerCidrs: 10.30.0.0/16`, api-gateway in the allowlist, `wafAclArn` = the web ACL with "cannot attach here"); no `networkPolicy` in any commerce values file (chart default off, `CH/values.yaml:405-406`); `vpc-cni` has no `enableNetworkPolicy` (`infra/terraform/modules/eks/main.tf:58-61`, reels-6); `module "waf_psp_webhook"` built and unused (`infra/terraform/envs/prod/main.tf:200-205`); `rate-limit-per-ip` (`modules/waf/main.tf:87-98`); no WAF block alarm; no rate limit in payments.
- **How to reproduce:** Read the files above.
- **Impact:** Any pod reaches payments (still behind service tokens) and commerce's internal-key routes (com-9). On a busy sale Razorpay webhooks beyond the per-IP ceiling are blocked; confirmations lag 10+ minutes and some orders cross the 20-minute expiry (com-39); nobody sees the blocks.
- **Fix:** Narrow the CIDR to the ALB subnets and drop api-gateway; add a commerce policy (gateway, admin-service, search-service, media-service); enable enforcement (reels-6); add a WAF rule scoped to `/v1/payments/webhook` allowing Razorpay's ranges ahead of the rate rule, with a `BlockedRequests` alarm.
- **Acceptance:** `render_test.sh` (wired into CI) asserts the CIDR is not the VPC CIDR and that commerce has a policy; `terraform plan` shows the scoped rule.
- **Effort:** M
- **Was:** COM-B-11, COM-E-14, COM-E-19

### com-36

- **ID:** com-36
- **Title:** A signed Razorpay webhook replayed under a fresh `X-Razorpay-Event-Id` is absorbed by the state machine rather than rejected (the event id is an unsigned header, no time window), and the capture replay case is untested.
- **Severity:** P2
- **Category:** webhook integrity / tests
- **Evidence level:** VERIFIED-RUN (`TestRazorpay_CapabilitiesDeclareNoWebhookTimestamp` passes) + VERIFIED-READ
- **Evidence:** `PAY/internal/gateway/razorpay_provider.go:166-190` (HMAC over the body only; event id from a header). Refunds dedupe on the signed refund id (`PAY/internal/service/p0.go:65-80`) and the new-id case is tested (`proof_b_integration_test.go:309-325`); capture dedupe tests reuse one event id (`proof_integration_test.go:71-110`, `handler_test.go:393-410`). Commerce absorbs a redelivered capture (`capture_verdict_test.go:33-70`).
- **How to reproduce:** `git grep -n "EventID" -- Architecture/services/payments-service/internal/store/postgres/proof_integration_test.go`.
- **Impact:** The effects found are idempotent, and an attacker needs a real signed body; but whether payments publishes a second `payment.succeeded` or records an owed capture for a replay is unpinned.
- **Fix:** Also dedupe on (provider, payment_id, event type) from the signed body; optionally reject `created_at` older than 24 h. Test: apply the same capture body twice with different event ids in `moneytruth_c3_integration_test.go`.
- **Acceptance:** One `succeeded` transition, at most one outbox `payment.succeeded`, no owed-capture row; fails if dedupe relies on the event id alone.
- **Effort:** S
- **Was:** COM-B-13, COM-F-11

### com-37

- **ID:** com-37
- **Title:** An expired or cancelled order leaves its intent and Razorpay order payable; the Razorpay order has no expiry.
- **Severity:** P2
- **Category:** paid path
- **Evidence level:** VERIFIED-READ
- **Evidence:** Expiry (`P/p0.go:1313-1350`) and cancel (`:674-818`) never call payments; no cancel or expire intent operation exists (grep `CancelIntent|expire_by|ExpireBy`); the Razorpay order body has no expiry (`PAY/internal/gateway/razorpay_provider.go:124-145`).
- **How to reproduce:** Cancel an order with an open intent, then complete payment in the still-open Checkout: the capture becomes a late-capture refund, or com-2 if the reconciler already failed the intent.
- **Impact:** Buyers charged then refunded days later; feeds com-2.
- **Fix:** Set Razorpay `expire_by` (or close the order) aligned to the reservation expiry; close the provider order on cancel.
- **Acceptance:** The provider order carries an expiry no later than reservation expiry plus a margin (fake provider test).
- **Effort:** S
- **Was:** COM-C-6

### com-38

- **ID:** com-38
- **Title:** A buyer's cancel can race the fulfilment job's courier booking, leaving a cancelled, refunded order with a live shipment.
- **Severity:** P2
- **Category:** concurrency
- **Evidence level:** SUSPECTED
- **Evidence:** The job reads the order without a lock, checks `paid` and books (`S/shipments.go:104-125`, `S/jobs.go:51-55`); `CancelOrder` locks the order `FOR UPDATE` (`P/p0.go:692-696`) but booking takes no lock; `cancelled → shipped` is refused by the matrix, so the order stays cancelled with an AWB. With com-1 the window is about one second after payment; once com-1 is fixed it moves to the seller's Ship action.
- **How to reproduce:** An integration test with a barrier between the job's read and its booking.
- **Impact:** A courier booking on a refunded order; goods possibly dispatched after the refund; restocked units physically shipped.
- **Fix:** Book under `SELECT … FOR UPDATE` on the order, re-checking status before recording the booking, or refuse cancel while a fulfilment job is `running`.
- **Acceptance:** The barrier test ends with either the cancel refused or no shipment.
- **Effort:** S
- **Was:** COM-C-8

### com-39

- **ID:** com-39
- **Title:** A Kafka, consumer or commerce outage longer than the 20-minute hold turns every captured payment into an expired order plus refund, and a repeatedly failing event blocks its partition for ever.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ (mechanism); SUSPECTED (head-of-line scale)
- **Evidence:** The sweep expires any lapsed `payment_pending` order without asking payments (`P/p0.go:1255-1266,1313-1349`); the capture then becomes a late-capture refund (`:260-304`); the consumer uses `RetryForever: true` on the shared topic keyed by `reference_id` (`C/internal/consumers/payments_p0.go:102-108`); no lag alert (com-15).
- **How to reproduce:** On QA, stop commerce for 25 minutes after a capture: the order goes `expired`, then `refund_pending`.
- **Impact:** Lost sales and charged-then-refunded buyers in any incident over 20 minutes; nobody paged.
- **Fix:** Before expiring, ask payments for the bound intent's status (skip `succeeded`/`processing`); cap `RetryForever` per event and park it in a table; lag alert (com-15).
- **Acceptance:** Integration test: intent succeeded at payments with no event delivered → the sweep leaves the order `payment_pending`.
- **Effort:** M
- **Was:** COM-E-6

### com-40

- **ID:** com-40
- **Title:** Invoice and shipment jobs for a paid order dead-letter after about 75 seconds of S3 or courier trouble; nothing replays, counts or alerts on them.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:** `C/internal/workers/fulfillment.go:45` (`maxAttempts: 5`), `:161-165` (backoff 5-40 s), `:152-158` → `P/jobs.go:116-123`; the only read is the internal-key `GET /internal/jobs/dead-letter`; no requeue code.
- **How to reproduce:** Point `COMMERCE_BLOB_BUCKET` at a missing bucket on QA and pay: the job is `dead` after about 75 s.
- **Impact:** Paid orders silently get no GST invoice (and, once com-1 is decided, no booking).
- **Fix:** Retry for 24 h (backoff capped at 5 min); gauge `commerce_fulfillment_dead_jobs` with an alert; internal `POST /internal/jobs/:id/requeue`.
- **Acceptance:** A job failing for 10 minutes is still `pending`; the gauge is on `/metrics`.
- **Effort:** S
- **Was:** COM-E-7

### com-41

- **ID:** com-41
- **Title:** Shipment status comes only from courier webhooks; a missed Shiprocket webhook leaves the order `shipped` for ever.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:** No tracking poller among the workers (`C/cmd/server/main.go:385-457`); the courier interface's only tracking input is `ParseWebhook` (`C/internal/courier/courier.go:155-158`).
- **How to reproduce:** Read the files above.
- **Impact:** No "delivered", no reviews (`S/reviews.go:30-46`), nobody told.
- **Fix:** An hourly sweep calling Shiprocket tracking for shipments with no event for 24 h.
- **Acceptance:** A fake-courier test: a stale shipment is moved by the sweep.
- **Effort:** M
- **Was:** COM-E-8

### com-42

- **ID:** com-42
- **Title:** Courier webhook processing has no shipment state guard: unknown statuses become `in_transit`, the whole activity history is re-applied on each call, the last array entry wins, and write errors are swallowed.
- **Severity:** P2
- **Category:** reliability / fulfilment
- **Evidence level:** VERIFIED-READ (code); SUSPECTED (Shiprocket's array order and field names)
- **Evidence:** `C/internal/courier/shiprocket.go:270-300` (`default: return "in_transit"`); `S/shipments.go:521-531` (errors logged only); `P/shipments.go:285-297` (unconditional `UPDATE shipments SET status`); `:299-305` (events re-inserted with no dedupe); `rto_delivered` and `lost` change nothing on the order.
- **How to reproduce:** Unit-test `HandleShipmentWebhook` with a fake returning `[delivered, picked_up]`: the shipment ends `picked_up` and events double on a second call.
- **Impact:** Wrong tracking; duplicate event rows; RTO parcels leave the order `shipped` with no refund route (com-7).
- **Fix:** A forward-only shipment transition table with sticky terminal states; apply the newest activity by `occurred_at`; dedupe events on (shipment, status, occurred_at); map RTO and lost to an order action.
- **Acceptance:** The test above leaves `delivered` and one event per scan; an unknown status is logged and ignored.
- **Effort:** S
- **Was:** COM-V3-2

### com-43

- **ID:** com-43
- **Title:** A Redis outage takes all of MStore down (boot and readiness), though commerce uses Redis only for the product view counter.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:** Boot exits if Redis does not answer (`C/cmd/server/main.go:97-100`); readiness registers `redis` (`:128-130`); the only use is the view counter (`S/service.go:191-204`), which already falls back when Redis is nil (`:211-213`). Same pattern as feed-43.
- **How to reproduce:** Stop ElastiCache on QA: every commerce `/readyz` answers 503.
- **Impact:** Checkout, orders and payment confirmation stop for a cache incident; with com-39 captured orders then expire and refund.
- **Fix:** Drop the `redis` readiness check; make the boot ping a warning.
- **Acceptance:** A unit test: `/readyz` stays 200 with Redis down.
- **Effort:** S
- **Was:** COM-E-9

### com-44

- **ID:** com-44
- **Title:** When payments-service is unreachable, "Pay" answers 500 `INTERNAL_ERROR`, not a retryable 503.
- **Severity:** P2
- **Category:** reliability / UX
- **Evidence level:** VERIFIED-READ (server); SUSPECTED (client copy)
- **Evidence:** `S/checkout_p0.go:393-415` returns the client error unwrapped; `writeCommerceError` falls to the default (`H/handler_p0.go:811-838`); courier and media outages have 503 codes (`:796-801`, `:695`).
- **How to reproduce:** Scale payments to 0 on QA; `POST /orders/:id/payment/intent` → 500.
- **Impact:** A generic failure with no "try again"; ops sees an unmapped-error log, not a dependency signal.
- **Fix:** Map transport errors and payments 5xx to 503 `PAYMENTS_UNAVAILABLE`; both clients show "your order is saved, try again".
- **Acceptance:** A handler test with a closed payments listener expects 503 and the code.
- **Effort:** S
- **Was:** COM-E-10

### com-45

- **ID:** com-45
- **Title:** A paid order cannot be followed by one id from checkout to webhook to confirmation.
- **Severity:** P2
- **Category:** observability
- **Evidence level:** VERIFIED-READ
- **Evidence:** Commerce → payments forwards trace context but not `X-Request-Id` (`C/internal/payments/p0client.go:147`); the outbox relay injects its own span (`SH/outbox/relay.go:271-274`); the webhook success path logs nothing (`PAY/internal/http/handler.go:1208-1222`).
- **How to reproduce:** Read the files above.
- **Impact:** Support cannot answer "I paid, where is my order" without SQL.
- **Fix:** Log one line per accepted webhook (provider, type, event id, provider order id, intent id); store `trace_id` on outbox rows; forward `X-Request-Id`.
- **Acceptance:** On QA one log grep for an order id returns lines from checkout, the webhook and the consumer.
- **Effort:** S
- **Was:** COM-E-11

### com-46

- **ID:** com-46
- **Title:** Staging and both Azure environments cannot boot commerce (and Azure cannot boot payments); staging's PDB blocks node drains.
- **Severity:** P2
- **Category:** deployment
- **Evidence level:** VERIFIED-READ
- **Evidence:** Staging: managed ENV with no `COMMERCE_BLOB_BACKEND` → `C/internal/store/blob/config.go:91-95` refuses and main exits (`C/cmd/server/main.go:159-163`); no `AWS_REGION`; chart PDB `minAvailable: 2` with 2 replicas (`CH/values.yaml:205-207`). Azure: no `POSTGRES_DSN` for commerce; payments has no key, stub or `SERVICE_CALLERS` (`PAY/internal/config/config.go:133-134,167`); image tag `latest`.
- **How to reproduce:** Read `DV/commerce-service/values-staging.yaml:72-135` and the Azure overlays.
- **Impact:** ArgoCD syncs crash-looping pods; QA (with the stub courier) is the only real-webhook rehearsal before prod.
- **Fix:** Delete the staging and Azure commerce/payments overlays and ApplicationSet entries, or complete them; staging PDB `minAvailable: 1`.
- **Acceptance:** `helm template` and a config dry-run of each overlay succeed, or the overlays are gone.
- **Effort:** S (delete) / M (complete)
- **Was:** COM-E-12

### com-47

- **ID:** com-47
- **Title:** A database restore breaks commerce's agreement with Razorpay and may reissue GST invoice numbers; no commerce restore procedure or tax-record retention exists.
- **Severity:** P2
- **Category:** data / backup
- **Evidence level:** VERIFIED-READ (backup config); SUSPECTED (number reuse)
- **Evidence:** PITR 7 days, AWS Backup 14 days, no vault lock or cross-region copy (`infra/terraform/envs/prod/variables.tf:326-330,411-414`); `docs/runbooks/restore-aurora.md:3` "UNTESTED"; `commerce_db` holds commerce and `payments.*`; the reconciler revisits only existing intents (`PAY/internal/store/postgres/p0.go:1439-1451`); `invoice_sequences` rewinds while S3 keeps the documents (`S/shipments.go:654-689`); retention "awaiting ruling" (`C/internal/pii/envelope.go:21,47`).
- **How to reproduce:** Read the files above.
- **Impact:** After a restore, buyers charged after T0 have no order and no automatic refund; invoice numbers can be issued twice.
- **Fix:** A commerce section in the restore runbook (Razorpay export since T0, diff, refund orphans, reset `invoice_sequences` to the S3 maximum); record the retention decision; S3 Object Lock or a non-expiring rule for current invoice versions.
- **Acceptance:** A QA drill record including the Razorpay diff and the sequence check.
- **Effort:** M
- **Was:** COM-E-13

### com-48

- **ID:** com-48
- **Title:** Every quote, and so every checkout, makes a live Shiprocket serviceability call: no cache, no breaker, a 20 s timeout, token refresh under a global mutex, and no per-route quote limit.
- **Severity:** P2 now; **P1 once `courier_provider` is switched to `shiprocket`**
- **Category:** performance / resilience (outbound)
- **Evidence level:** VERIFIED-READ; UNVERIFIED (Shiprocket's rate limit)
- **Evidence:** `S/checkout_p0.go:101-150` calls `CheckServiceability` per quote; the code says Shiprocket rate-limits it and the delivery-date cache is not used by quotes (`S/engagement.go:230-262`); `C/internal/courier/shiprocket.go:46-48` (20 s, default transport), `:94-119` (login under `c.mu.Lock()`, token not dropped on 401); no quote limit beyond the edge's 60 rps per user. Latent while prod runs the stub (com-5) (v3).
- **How to reproduce:** A courier fake sleeping 15 s or answering 429; run the quote phase of L3 and record p95 and 503 share.
- **Impact:** Checkout throughput capped platform-wide by the carrier's limit; a carrier slowdown holds quotes up to 20 s; a flash sale can trip the limiter for every buyer.
- **Fix:** Cache serviceability per (pickup pin, drop pin, weight band, mode) for ~10 min with `singleflight`; a breaker and 5 s timeout; refresh the token outside the mutex and drop it on 401; a per-user quote limit.
- **Acceptance:** 100 concurrent quotes for one lane make 1 carrier call; a 401 triggers one re-login.
- **Effort:** S-M
- **Was:** COM-D-4

### com-49

- **ID:** com-49
- **Title:** Every product image is authorised through five services in turn; commerce's hop, `VisibleProductMediaIDs`, is an OR-join with correlated subqueries and no supporting index, and one slow authority fails the whole batch.
- **Severity:** P2 (SUSPECTED)
- **Category:** performance / resilience
- **Evidence level:** SUSPECTED (the plan and latency; no EXPLAIN was run); VERIFIED-READ for the query, missing indexes and chain
- **Evidence:** `P/storefront.go:777-794` (disjunctive join over `primary_image_media_id`, `product_media`, `product_variants`); no index on `product_media.media_id`, `products.primary_image_media_id` or `product_variants.image_media_id`; `MS/cmd/server/main.go:537-543` and `MS/internal/delivery/authz.go:437-492` (sequential authorities, any unresolved id fails the batch, 3 s timeouts); `C/internal/media/media.go:345-350,405-416` swallows failures into placeholders.
- **How to reproduce:** On a 100k-product fixture, `EXPLAIN (ANALYZE, BUFFERS)` the statement with 50 ids; time `POST /v1/media/batch`.
- **Impact:** At launch sizes slower than needed; past roughly 10,000 products every grid, cart and order screen may lose its images at once (arithmetic).
- **Fix:** (a) a `UNION` of three indexed lookups plus three partial indexes; (b) route an asset to its owning authority (also closes com-3's class); (c) a public delivery class for live product images.
- **Acceptance:** EXPLAIN shows index scans only, under 20 ms for 50 ids; a product-only batch makes at most 2 authority calls.
- **Effort:** S (a), M (b), M (c)
- **Was:** COM-D-1

### com-50

- **ID:** com-50
- **Title:** A flash sale serialises every quote and checkout of a product on its `products` row (`FOR UPDATE OF v, p`); captures and expiry releases queue on the same inventory row.
- **Severity:** P2 (SUSPECTED)
- **Category:** performance (lock contention)
- **Evidence level:** SUSPECTED (the ceiling is arithmetic); VERIFIED-READ for the locks
- **Evidence:** `P/checkout.go:859-913` (`FOR UPDATE OF v, p`); quote takes the same lock (`P/quote.go:292-318`); checkout then locks inventory, claims the coupon and writes about 6N + 11 statements (`P/checkout.go:422-724`); the payments consumer (`P/p0.go:606-612`), expiry (`:552-590`), stock edits and the view-count flush write the same rows. The oversell guard is correct.
- **How to reproduce:** L3: 5,000 buyers on one SKU quote and check out within 60 s against 500 units; record lock waits and p95.
- **Impact:** Roughly 100 checkouts per second per product at best; confirmations wait behind new checkouts.
- **Fix:** Lock the product `FOR SHARE`; price quotes without the lock; move the coupon claim and reservation update to the end of the transaction.
- **Acceptance:** Two concurrent checkouts of different variants of one product do not wait on each other; L3 thresholds met.
- **Effort:** S-M
- **Was:** COM-D-2

### com-51

- **ID:** com-51
- **Title:** `/home` and `/categories` are rebuilt from the whole live catalogue on every request: five sequential catalogue-wide statements with two `LATERAL`s per product, and no server cache.
- **Severity:** P2 (SUSPECTED)
- **Category:** performance
- **Evidence level:** SUSPECTED (latency at catalogue size); VERIFIED-READ for the queries
- **Evidence:** `S/storefront.go:422-459`; `P/storefront.go:563-659` (rails ordered by expressions over laterals), `:506-537` (category counts); no cache (`grep singleflight|ttlcache|homeCache`: none); Android requests home on every store open.
- **How to reproduce:** `EXPLAIN ANALYZE` each statement on the 100k fixture; L1.
- **Impact:** The landing screen's cost grows with the catalogue and is repeated per shopper for identical output.
- **Fix:** Cache the viewer-independent home and category cards in-process for 60 s with `singleflight`; later maintained price and stock columns.
- **Acceptance:** 100 concurrent `Home` calls make one call per store method within the TTL; L1 home p95 < 150 ms.
- **Effort:** S (a), M (b)
- **Was:** COM-D-5

### com-52

- **ID:** com-52
- **Title:** Browse and category listings cannot use an index: the partial indexes on `products` need `status = 'active'`, which the `product_offers`-based query no longer states.
- **Severity:** P2 (SUSPECTED)
- **Category:** performance (indexes)
- **Evidence level:** SUSPECTED (resulting plan); VERIFIED-READ for predicates and index definitions
- **Evidence:** `setup.sql:203,205` (`WHERE status = 'active'`); `P/storefront.go:177-182` and `P/products.go:371-453` (no `p.status` term; price and stock filters on lateral outputs). v3: the track missed two partial indexes, but the conclusion stands, and a cheaper fix exists (add the redundant `p.status = 'active'` predicate so the existing indexes qualify) before new indexes.
- **How to reproduce:** `EXPLAIN (ANALYZE, BUFFERS)` of `ListProductsFiltered` on the 100k fixture: no filter, one category, in-stock with a price floor.
- **Impact:** Page 1 of every listing costs time proportional to the catalogue.
- **Fix:** Add `p.status = 'active'` to the live predicate if every live offer implies it; otherwise `idx_products_created_id`, `idx_products_category_created` and a partial live index on `product_offers`.
- **Acceptance:** EXPLAIN shows an index scan driving `LIMIT` with no Sort node; L1 browse p95 < 150 ms.
- **Effort:** S
- **Was:** COM-D-6

### com-53

- **ID:** com-53
- **Title:** Product search is `ILIKE '%q%'` across nine expressions and three `EXISTS`, with no trigram or full-text index; Android searches as the shopper types.
- **Severity:** P2
- **Category:** performance (search)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `P/products.go:351-364,389-392`; no `pg_trgm`/`tsvector` on products; Android debounce 300 ms (`StoreBrowseViewModel.kt:76-84,192`); search-service already indexes products but no client uses it for the shop.
- **How to reproduce:** `EXPLAIN ANALYZE` with `Query = "shirt"` on the 100k fixture.
- **Impact:** Bursts of sequential scans on the writer that takes checkouts.
- **Fix:** Serve `?q=` from search-service with the live rule as a filter, or a generated `tsvector` with GIN.
- **Acceptance:** EXPLAIN shows an index path; L2 search p95 < 200 ms.
- **Effort:** M
- **Was:** COM-D-7

### com-54

- **ID:** com-54
- **Title:** The public product list keeps an unbounded `?offset=` path that runs a full `COUNT(*)` of the live catalogue on every call.
- **Severity:** P2
- **Category:** performance (pagination)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `H/handler_catalogue.go:567-580` (only negative offsets clamped); `P/products.go:494-497,510-515`; the route is anonymous; no first-party client uses offsets.
- **How to reproduce:** `GET /v1/commerce/products?offset=1000000&q=a`.
- **Impact:** Any anonymous caller can force the most expensive listing query at 100 rps per IP.
- **Fix:** Cap `offset` at 1,000 or remove the offset branch from the public route.
- **Acceptance:** `?offset=5000` answers 400; no `COUNT(*)` on the public route.
- **Effort:** S
- **Was:** COM-D-8

### com-55

- **ID:** com-55
- **Title:** The public reviews list takes an unbounded `limit`, pages by `OFFSET`, counts every review per request, and sorts by an unindexed `helpful_count`.
- **Severity:** P2
- **Category:** performance (unbounded list)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `H/handler_reviews.go:27-28` (no clamp); `S/engagement.go:453-454`; `P/reviews.go:46-59`; only index `(product_id, is_published, rating)` (`setup.sql:626`).
- **How to reproduce:** `GET /products/<id>/reviews?limit=1000000`.
- **Impact:** One anonymous request returns a product's entire review set with vote hydration.
- **Fix:** `clampListPagination` (cap 50); index `(product_id, helpful_count DESC, created_at DESC, id DESC) WHERE is_published`.
- **Acceptance:** `limit=1000000` returns at most 50; EXPLAIN shows no Sort.
- **Effort:** S
- **Was:** COM-D-9

### com-56

- **ID:** com-56
- **Title:** Seller order lists recompute over the seller's whole history per page, and the fulfilment tabs filter after paging, so a tab can be empty while matching orders exist.
- **Severity:** P2
- **Category:** performance / functional (pagination)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `P/orders.go:320-357` (discarded `COUNT(DISTINCT)`, `SELECT DISTINCT … OFFSET`); `S/orders_read.go:207-262` (stage filter in Go after the page).
- **How to reproduce:** A seller whose 20 newest orders are delivered: `GET /seller/fulfillment?stage=unshipped&limit=20` returns `[]`.
- **Impact:** Successful sellers see slow, misleadingly empty work queues.
- **Fix:** Drop the count; `EXISTS` on `order_items`; stage predicate in SQL; keyset on `(created_at, id)`.
- **Acceptance:** The 21st-newest, only unshipped order appears on the first `stage=unshipped` page.
- **Effort:** M
- **Was:** COM-D-10

### com-57

- **ID:** com-57
- **Title:** Payment timeouts are inverted (commerce gives payments 15 s, payments gives Razorpay 30 s), with no breaker and default transports.
- **Severity:** P2
- **Category:** resilience (outbound)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `C/internal/payments/p0client.go:141-148`, `SH/paymentsclient/client.go:95-96` (15 s); `PAY/internal/gateway/razorpay_provider.go:60`, `razorpay.go:31` (30 s); the status poll calls `GetIntent` on every read (`S/checkout_p0.go:500-504`).
- **How to reproduce:** A Razorpay fake sleeping 20 s: the intent call returns 5xx at 15 s while payments creates the order at 20 s.
- **Impact:** At PSP latency spikes buyers see "payment could not start" and retry into a slow PSP (no double order: idempotency holds).
- **Fix:** Razorpay 10 s, commerce → payments 12 s; shared transport with `MaxIdleConnsPerHost ≥ 32`; a breaker; answer young status polls from the order row.
- **Acceptance:** A unit test that the outer timeout exceeds the inner one.
- **Effort:** S
- **Was:** COM-D-12

### com-58

- **ID:** com-58
- **Title:** The payments reconciler takes the 50 oldest stale intents per minute, so never-terminal intents block newer lost webhooks for ever, and it runs unclaimed on every replica.
- **Severity:** P2
- **Category:** resilience / scale (worker)
- **Evidence level:** VERIFIED-READ (ordering, limit, no claim); SUSPECTED (how long such intents stay non-terminal)
- **Evidence:** `PAY/internal/store/postgres/p0.go:1439-1452` (`ORDER BY created_at LIMIT`, no `SKIP LOCKED`); `PAY/internal/service/p0.go:700-770,921-931` (non-terminal outcomes `continue` unchanged); started unconditionally on every pod (`PAY/cmd/server/main.go:361-365`), no advisory lock. Commerce fixed the same pattern in migration 053.
- **How to reproduce:** 50 old intents reporting `authorized` plus one newer `captured`: three `reconcileOnce` runs never reach the captured one.
- **Impact:** After a busy sale, a paid order whose webhook was lost is not repaired, expires and is refunded; each replica multiplies Razorpay calls.
- **Fix:** Persist `reconcile_attempts`/`reconcile_next_at` and order by it; claim with `SKIP LOCKED` or an advisory lock.
- **Acceptance:** The test above passes; two reconciler instances fetch each intent once per tick.
- **Effort:** M
- **Was:** COM-D-13, COM-E-16

### com-59

- **ID:** com-59
- **Title:** Expired holds are released at most 200 orders per minute, with every pod racing for the same 200.
- **Severity:** P2
- **Category:** scale (sweeper)
- **Evidence level:** VERIFIED-READ (batch and interval); SUSPECTED (backlog in practice)
- **Evidence:** `S/checkout_p0.go:696-717` (`ExpireStaleOrders(ctx, 200)` per minute); `P/p0.go:1255-1297` (no claim); `expireOne` writes the hot inventory row.
- **How to reproduce:** 10,000 lapsed `payment_pending` orders; count releases per minute with 1 and 3 instances.
- **Impact:** After a large sale unpaid stock stays unavailable for over an hour (arithmetic).
- **Fix:** Claim with `SKIP LOCKED` or a lease; loop while batches are full; batch 500.
- **Acceptance:** Two instances expire 10,000 orders within 5 minutes with no order processed twice.
- **Effort:** S
- **Was:** COM-D-14

### com-60

- **ID:** com-60
- **Title:** Paid orders are fulfilled one job at a time per pod (invoice upload plus two courier calls), so a sale's orders queue for an hour or more.
- **Severity:** P2
- **Category:** scale (worker)
- **Evidence level:** VERIFIED-READ (structure); SUSPECTED (per-job time)
- **Evidence:** `C/internal/workers/fulfillment.go` (one drain loop, `ClaimNextJob` one at a time, `P/jobs.go:63-69`); `S/jobs.go:43-57` (30 s budget); prod 2-6 pods at 500m CPU.
- **How to reproduce:** 5,000 jobs with an 800 ms courier fake on 2 pods: measure drain time.
- **Impact:** Sellers get AWBs and invoices long after payment during a sale.
- **Fix:** N concurrent drainers per pod; split invoice and shipment jobs; a queue-age gauge with an alert.
- **Acceptance:** 5,000 jobs drain in under 10 minutes on 2 pods.
- **Effort:** S-M
- **Was:** COM-D-15

### com-61

- **ID:** com-61
- **Title:** Product image URLs are re-signed every 2 minutes (5-minute lifetime), so client caches miss on every revisit and lazily loaded tiles can expire.
- **Severity:** P2
- **Category:** performance / cost (images)
- **Evidence level:** VERIFIED-READ (TTL, client cache keys); SUSPECTED (visible expiry)
- **Evidence:** `MS/internal/delivery/signer.go:72`, `sign_cache.go:55-60`; Coil default URL keys (`core/commerce-ui/.../CommerceComponents.kt:87-95`); web plain `<img>` (`ProductPhoto.tsx:31`); CloudFront itself ignores query strings (`infra/terraform/modules/media/main.tf:297,316`).
- **How to reproduce:** Open the web grid, wait 4 minutes, scroll to lazy tiles: count CDN 403s.
- **Impact:** Images re-downloaded on mobile data on every return after 2 minutes.
- **Fix:** Public stable URLs for live product images (com-49 (c)); short of that, cache keys without the query string and refetch lists older than 3 minutes.
- **Acceptance:** A second Android open after 3 minutes downloads 0 image bytes; no CDN 403 in the web scenario.
- **Effort:** S-M
- **Was:** COM-D-16

### com-62

- **ID:** com-62
- **Title:** The web product grid draws the 1080 px rendition in every tile instead of the thumbnail it is also sent.
- **Severity:** P2
- **Category:** performance (images, web)
- **Evidence level:** VERIFIED-READ
- **Evidence:** pb-echo `src/features/shop/model/storefront.ts:93-97,128-137` (`image_url || thumbnail_url`); `image_url` prefers `medium_1080` (`C/internal/media/media.go:303-327`); Android uses the thumbnail first.
- **How to reproduce:** Compare transferred bytes for a 24-tile page on throttled 4G.
- **Impact:** Several times the bytes per tile; slower first paint; more CDN egress.
- **Fix:** Use `thumbnail_url` (or `srcset`) for cards.
- **Acceptance:** A `toProductCard` unit test yields the thumbnail.
- **Effort:** S
- **Was:** COM-D-17

### com-63

- **ID:** com-63
- **Title:** Order confirmation rides one sequential consumer per pod of the shared `social.events.v1` firehose with `RetryForever`, so unrelated traffic and checkout contention delay "confirmed".
- **Severity:** P2
- **Category:** scale / resilience (consumer)
- **Evidence level:** VERIFIED-READ (structure); SUSPECTED (lag under load)
- **Evidence:** `C/internal/consumers/payments_p0.go:91-125`; `SH/kafka/consumer.go:186-201,283-355`; 6 partitions; Android stops polling at 180 s (`PaymentCoordinator.kt:108-110`).
- **How to reproduce:** L5: 2,000 signed webhooks in 60 s with social traffic running; measure webhook-to-confirmed p99 and lag.
- **Impact:** Confirmation latency coupled to platform traffic; one failing event pauses a partition.
- **Fix:** A dedicated `payments.events.v1` topic (also helps com-4); per-key concurrency; a lag alert (com-15).
- **Acceptance:** L5 p99 under 10 s; lag back to 0 within 60 s.
- **Effort:** M
- **Was:** COM-D-18

### com-64

- **ID:** com-64
- **Title:** The commerce refund worker is untested, and no test follows a cancel or partial refund through to `refunded`.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `S/checkout_p0.go:633-689` (`RunRefundWorker`, `deliverRefund`); `git grep "deliverRefund\|ClaimDueRefundCommands\|MarkRefundSubmitted" -- '*_test.go'` is empty; cancel tests stop at the command row (`cancel_refund_integration_test.go:63-122`).
- **How to reproduce:** The grep above.
- **Impact:** Cancel is the only buyer refund path at launch; a regression in key reuse or terminal mapping leaves buyers unrefunded or double-refunded with CI green.
- **Fix:** A unit test with fakes: success submitted once; 5xx retried with the same idempotency key; `ErrRefused` terminal; no intent terminal. Then cancel → `refunded` in the com-18 e2e.
- **Acceptance:** Fails when the key is regenerated per attempt or `ErrRefused` is retried.
- **Effort:** S (unit), M (e2e)
- **Was:** COM-F-5

### com-65

- **ID:** com-65
- **Title:** The web's golden-fixture contract tests skip in CI and have already drifted from the backend.
- **Severity:** P2
- **Category:** tests / contract
- **Evidence level:** VERIFIED-RUN
- **Evidence:** Local `bun test src/features/shop src/app/shop` in pb-echo: 2 failures (`admin/coupon_patch_200.json` `expires_at`; `orders/order_shipments_get_200` missing `seller_managed`). Hard-coded backend paths (`contractFixtures.ts:15`, `couponsOffers.contract.test.ts:26`) and wrong relative paths (`sell.contract.test.ts:31`, `storefront.contract.test.ts:25`); web-ci run 44078366557 green with 100 skips. Android's equivalent passes 71/71.
- **How to reproduce:** The `bun test` command above.
- **Impact:** A backend response change reaches the web with CI green; `seller_managed` drives com-27's button.
- **Fix:** Check out the backend fixtures in web-ci at a pinned ref and fail, not skip, when missing; one path helper; refresh the two fixtures.
- **Acceptance:** web-ci fails when a backend fixture changes and the web copy does not.
- **Effort:** S
- **Was:** COM-F-7

### com-66

- **ID:** com-66
- **Title:** No test proves every writer of `orders.status` goes through the transition matrix on a database without gated 998.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `P/order_fulfilment.go:3-7` says nothing guards raw status updates on a boot-migrated database; matrix tests cover seller actions, payment and customer cancel only; no test enumerates every `UPDATE orders SET status` site. Matters more while com-14 leaves the trigger unapplied.
- **How to reproduce:** `git grep -n "UPDATE orders SET status" -- Architecture/services/commerce-service/internal/store`.
- **Impact:** A new writer or a courier jump `payment_pending → delivered` would not be refused.
- **Fix:** A source-level guard test failing on any raw status update outside the allowlisted matrix helper, plus one illegal-source test per writer.
- **Acceptance:** Adding a raw status update elsewhere fails the build.
- **Effort:** S
- **Was:** COM-F-15

---

## P3

### com-67

- **ID:** com-67
- **Title:** Buyer order sub-resources answer 403 to a stranger and 404 only for a missing id (an existence oracle), and several routes echo raw Go error text.
- **Severity:** P3
- **Category:** information disclosure
- **Evidence level:** VERIFIED-READ
- **Evidence:** `H/shipments.go:41-58` (shipment, shipments, invoice); `H/handler_p0.go:519-533` + `S/customer_payment.go:109` (`/orders/:id/payment`); raw `err.Error()` at `H/shipments.go:105,197,235`, `H/handler_orders.go:195,285`. Ids are random UUIDs and no data is returned.
- **How to reproduce:** Buyer B: `GET /orders/<A's id>/invoice` → 403; a random UUID → 404.
- **Impact:** Existence oracle; internal error text (including `payment_status`) leaks.
- **Fix:** Map "not associated" to 404 `ORDER_NOT_FOUND`; route errors through `writeCommerceError`.
- **Acceptance:** A stranger and a missing id get the same status and body.
- **Effort:** S
- **Was:** COM-A-14, COM-B-14

### com-68

- **ID:** com-68
- **Title:** Seller order detail answers 403, not 404, for another seller's order.
- **Severity:** P3
- **Category:** information disclosure
- **Evidence level:** VERIFIED-READ
- **Evidence:** `GET /seller/orders/:id` uses `sellerForCaller` plus `sellerLines` and answers 403 when the caller has no line (`S/orders_read.go:289-298`) (v2).
- **How to reproduce:** Seller B requests seller A's order id: 403; a random id: 404.
- **Impact:** Existence oracle only.
- **Fix:** Answer 404 when the caller has no line.
- **Acceptance:** Included in com-20's table test: a foreign seller gets 404.
- **Effort:** XS
- **Was:** new (v2 per-route note on COM-F-3)

### com-69

- **ID:** com-69
- **Title:** A buyer who opens payment late in the 20-minute hold can pay after the order expires and is then auto-refunded rather than kept.
- **Severity:** P3
- **Category:** UX / payments
- **Evidence level:** VERIFIED-READ
- **Evidence:** `P/checkout.go:680`; `S/checkout_p0.go:452-458` (opening the intent does not extend the hold); `P/p0.go:1310-1352`; no provider expiry (com-37); late capture → refund command (`P/p0.go:283-300`). No money is lost.
- **How to reproduce:** Check out, wait 19 minutes, open the intent, pay at minute 21: `expired`, `refund_pending`.
- **Impact:** A paying buyer loses the item and waits for a refund.
- **Fix:** Extend the hold, or refuse the intent, when under ~5 minutes remain; pass Razorpay Checkout `timeout` to clients.
- **Acceptance:** An intent opened at T-1 min either extends the hold or is refused.
- **Effort:** S
- **Was:** COM-V3-4

### com-70

- **ID:** com-70
- **Title:** The legacy float pricing preview (`GET /cart/coupon-preview`) and the seller price-tier ladder are live, but P0 checkout ignores tiers.
- **Severity:** P3
- **Category:** money hygiene
- **Evidence level:** VERIFIED-READ
- **Evidence:** `H/handler.go:114-115,137`; `H/handler_orders.go:96-116` → `S/checkout_quote.go:152-180,374-375` (tiers, float, rupee column); `P/checkout.go:875-878` reads `selling_price_minor` only; no client calls either route.
- **How to reproduce:** Set a tier, call the preview, then quote: totals differ.
- **Impact:** None to money today; a future client would show prices checkout does not charge.
- **Fix:** Fence both routes with the legacy quote.
- **Acceptance:** Both answer 404 through `FenceMiddleware`.
- **Effort:** XS
- **Was:** COM-V3-3

### com-71

- **ID:** com-71
- **Title:** Legacy float and NUMERIC money paths remain reachable: COD remittance settle and `UpdateOrderStatus` without a matrix check.
- **Severity:** P3
- **Category:** money types
- **Evidence level:** VERIFIED-READ
- **Evidence:** COD remittances float/NUMERIC (`S/shipments.go:435-461`), settle route live in-cluster though no COD order can be created; `UpdateOrderStatus` (`P/orders.go:359-381`) with discarded errors in fenced return paths (`S/returns.go:93,134,298`).
- **How to reproduce:** Read the files above.
- **Impact:** Latent until COD or returns are unfenced.
- **Fix:** Delete the COD remittance writer and settle route until COD is scoped; route `UpdateOrderStatus` through `transitionOrderStatusTx`.
- **Acceptance:** The settle route is gone; the com-66 guard covers `UpdateOrderStatus`.
- **Effort:** S
- **Was:** COM-C-10

### com-72

- **ID:** com-72
- **Title:** Review title, body and seller response have no length limit, and reviews are published immediately.
- **Severity:** P3
- **Category:** input validation / abuse
- **Evidence level:** VERIFIED-READ
- **Evidence:** `H/handler_reviews.go:41-47,69-71,90-92`. Clients escape output (no `dangerouslySetInnerHTML` in the web shop; Compose `Text`).
- **How to reproduce:** POST a 1 MB review body on a delivered item: accepted and visible.
- **Impact:** Abuse and storage bloat; no XSS found.
- **Fix:** Max 150 / 5,000 / 2,000 runes; a per-user review limit.
- **Acceptance:** An over-length review returns 400.
- **Effort:** S
- **Was:** COM-B-12

### com-73

- **ID:** com-73
- **Title:** The anonymous share limiter keys on a client-spoofable IP.
- **Severity:** P3
- **Category:** rate limiting
- **Evidence level:** SUSPECTED (gin's `ClientIP` with default trusted proxies and the gateway's `X-Forwarded-For` handling not confirmed)
- **Evidence:** `C/cmd/server/main.go:517` (`gin.New()` without `SetTrustedProxies`); `H/handler_engagement.go:45-114` (20/min per `ip:<ClientIP>`).
- **How to reproduce:** Send shares with rotating `X-Forwarded-For` values through the gateway.
- **Impact:** Share counts inflatable.
- **Fix:** `SetTrustedProxies(nil)` and use the gateway-stamped address.
- **Acceptance:** A handler test: a forged `X-Forwarded-For` does not change the limiter key.
- **Effort:** S
- **Was:** COM-B-15

### com-74

- **ID:** com-74
- **Title:** B2B order approve and reject sit outside the `/organizations` fence, and a credit-terms approval confirms an order with no payment.
- **Severity:** P3 (latent: no organization order can be created while `/organizations` and `/rfqs` are fenced)
- **Category:** authorization / payment (latent)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `H/handler_organizations.go:29-30`; `S/organizations.go:268-296` (`CreditTermsDays > 0` → `confirmed` without capture); checkout writes no organization or credit terms.
- **How to reproduce:** Not reproducible while B2B creation is fenced.
- **Impact:** Unpaid confirmed orders the day B2B creation is unfenced without this.
- **Fix:** Add `/orders/:id/approve|reject` to `fencedRouteReason` until B2B launches.
- **Acceptance:** Both routes answer 404 through the fence.
- **Effort:** S
- **Was:** COM-B-16

### com-75

- **ID:** com-75
- **Title:** KYC uploads may be ordinary image-kind media in every environment.
- **Severity:** P3
- **Category:** data protection
- **Evidence level:** SUSPECTED (what media-service does with image kind: thumbnails, moderation copies, CDN class)
- **Evidence:** `DV/commerce-service/values-prod.yaml:136` (`COMMERCE_KYC_REQUIRE_DOCUMENT_KIND: "false"`); `S/onboarding.go:188-193` then allows image kind. Note: com-3 does not reach `document` kind, but image-kind KYC would be reachable by that class of bug.
- **How to reproduce:** Upload a KYC document as an image from Android and inspect its media row.
- **Impact:** Identity documents could get renditions or a weaker delivery class.
- **Fix:** Turn the flag on once Android's document upload ships; re-upload legacy image KYC as document kind.
- **Acceptance:** A values test: prod requires document kind.
- **Effort:** S + client release
- **Was:** COM-B-17

### com-76

- **ID:** com-76
- **Title:** Boot migration hygiene: setup.sql runs unlocked on every replica, `COMMERCE_SKIP_BOOT_MIGRATIONS` is read by no code, and migrations 051 and 052 carry no compatibility acknowledgement.
- **Severity:** P3
- **Category:** deployment / migrations
- **Evidence level:** VERIFIED-RUN (`scripts/verify/migration-compat.sh` flags 051 and 052, exit 1) + VERIFIED-READ
- **Evidence:** `P/bootstrap.go:15`, `PAY/internal/store/postgres/bootstrap.go:20` (setup.sql before `RunExclusive`); the flag appears in staging values and `C/cmd/migrate/main.go:22-23` only; `051_stock_adjustment_idempotency.sql:22`, `052_reservation_identity_live_only.sql:22`; the compat script is not in CI.
- **How to reproduce:** `bash scripts/verify/migration-compat.sh --files <commerce 05*.sql>`.
- **Impact:** A rare boot failure on scale-out; the documented "Job migrates, pods skip" switch does nothing.
- **Fix:** Run setup.sql inside the advisory lock; implement or delete the flag; acknowledge 051 and 052.
- **Acceptance:** The compat check exits 0 on the last two releases' migrations; a boot-config unit test covers the flag.
- **Effort:** S
- **Was:** COM-C-9, COM-E-18

### com-77

- **ID:** com-77
- **Title:** No startup probe: boot work (setup.sql, migrations, a 30 s KMS check) runs before the listener while liveness kills the pod after about 30 s.
- **Severity:** P3
- **Category:** deployment
- **Evidence level:** SUSPECTED
- **Evidence:** `C/cmd/server/main.go:112,278,788`; `CH/values.yaml:193-197`; no `startupProbe` in `CH/templates/deployment.yaml`; migration lock wait 5 min (`SH/store/migrationrunner/exclusive.go:170`).
- **How to reproduce:** On QA, add a migration that sleeps 60 s: pods restart in a loop.
- **Impact:** A slow migration or KMS start becomes a crash loop during rollout.
- **Fix:** A `startupProbe` on `/livez` (failureThreshold 30, period 10).
- **Acceptance:** `render_test.sh` asserts a startup probe.
- **Effort:** S
- **Was:** COM-E-15

### com-78

- **ID:** com-78
- **Title:** No PDF invoice is ever produced: wkhtmltopdf is not in the image, so buyers and sellers get HTML only.
- **Severity:** P3
- **Category:** deployment
- **Evidence level:** VERIFIED-READ
- **Evidence:** `C/Dockerfile:22-25`; `SH/invoice/pdf.go:32-35`; `S/shipments.go:649` falls back to HTML.
- **How to reproduce:** Read the files above.
- **Impact:** Fine if intended; otherwise a silent gap.
- **Fix:** Decide; document HTML-only or install a renderer.
- **Acceptance:** The decision is recorded; if PDF, an image test runs `wkhtmltopdf --version`.
- **Effort:** S
- **Was:** COM-E-17

### com-79

- **ID:** com-79
- **Title:** On a Redis error every product view falls back to `UPDATE products SET view_count` on the row checkout locks, in an unbounded goroutine.
- **Severity:** P3
- **Category:** resilience
- **Evidence level:** VERIFIED-READ
- **Evidence:** `S/catalogue_products.go:311`; `S/service.go:213-220`; `P/products.go:539-541`.
- **How to reproduce:** Stop Redis on a dev stack and run the product-detail mix.
- **Impact:** A Redis blip during a sale adds lock contention (com-50) and unbounded goroutines.
- **Fix:** Drop the view on Redis error; bound the goroutine with a 200 ms context.
- **Acceptance:** With a failing counter no `UPDATE products` is issued.
- **Effort:** S
- **Was:** COM-D-20

### com-80

- **ID:** com-80
- **Title:** Append-only tables (`outbox_events`, `shipping_quotes`, `payment_event_inbox`, `inventory_ledger`, finished reservations) are never pruned; one gauge scans reservations every 30 s without an index.
- **Severity:** P3
- **Category:** scale (retention)
- **Evidence level:** VERIFIED-READ
- **Evidence:** No `DELETE FROM` on these tables beyond targeted reservation deletes; `S/checkout_p0.go:186` inserts per quote; `P/p0.go:1354-1366` gauge every 30 s per pod; reservation indexes on `variant_id`, `order_id`, `user_id` only.
- **How to reproduce:** Record table sizes and the gauge's duration after L3 and L5.
- **Impact:** Storage and vacuum cost grow with traffic.
- **Fix:** A daily retention job (subject to the tax-retention decision, com-13); partial index `inventory_reservations(created_at) WHERE released_at IS NULL AND committed_at IS NULL`.
- **Acceptance:** The retention job has a test; the gauge's EXPLAIN uses the partial index.
- **Effort:** S
- **Was:** COM-D-21

### com-81

- **ID:** com-81
- **Title:** Smaller unbounded or per-row work: the bag's coupon list is N + C queries, the internal media-access batch takes any number of ids, admin queues take an unclamped `limit` with `OFFSET` and `COUNT`, and the cursor product list ignores Android's `sort`.
- **Severity:** P3
- **Category:** performance (N+1 / unbounded)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `P/coupons.go:480-600` (`:511`, `:305-306`); `H/handler_storefront.go:340-362`; `H/handler_onboarding.go:475-478,600-603` → `P/onboarding.go:296-305,515-523`.
- **How to reproduce:** Read the files above.
- **Impact:** Small latency and load costs; Android's sort choice silently does nothing.
- **Fix:** Batch the coupon usage counts; cap the media-access batch at 50; clamp admin queue limits; honour `sort` or remove it from Android.
- **Acceptance:** Unit tests per cap.
- **Effort:** S
- **Was:** COM-D-23

### com-82

- **ID:** com-82
- **Title:** No test sends forged money fields in cart, quote or checkout bodies.
- **Severity:** P3
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** Handlers bind only ids today (`H/handler_p0.go:373-378`); no test posts `total_minor`, `unit_price_minor`, `discount_minor` or `shipping_minor` and asserts they are ignored.
- **How to reproduce:** `grep -rn "unit_price_minor\|total_minor\":" --include=*_test.go Architecture/services/commerce-service/internal/http`.
- **Impact:** A future DTO change binding a client price would not be caught.
- **Fix:** Extend `TestC3JourneyQuoteThenCheckoutOverHTTP` with forged fields at one paisa.
- **Acceptance:** Fails if any of those fields is bound.
- **Effort:** XS
- **Was:** COM-F-10

### com-83

- **ID:** com-83
- **Title:** CI cannot show which commerce and payments integration tests ran; 13 delivery DB tests and one payments DB test skip locally.
- **Severity:** P3
- **Category:** tests / CI
- **Evidence level:** VERIFIED-RUN
- **Evidence:** integration-all job 134073094517 prints one PASS line per service; the untagged payments `store_db_test.go` has no `_test` database guard.
- **How to reproduce:** `gh run view 44093026646 --job 134073094517 --log | grep -E "^(PASS|FAIL)"`.
- **Impact:** A proof that starts to `t.Skip` goes unnoticed.
- **Fix:** `go test -json` in `go.sh` and fail on any skip in a must-run list (C1, C2, C6, C7, N6, B2, B3, C4, C8).
- **Acceptance:** Skipping one of those tests turns integration-all red.
- **Effort:** S
- **Was:** COM-F-12

### com-84

- **ID:** com-84
- **Title:** The e2e's admin refusals check only the status code, because admin-service relays commerce's 409 without its body.
- **Severity:** P3
- **Category:** tests
- **Evidence level:** VERIFIED-RUN
- **Evidence:** Log lines for `commerce_journey_test.go:123,151,160,171`: admin-service relayed the status without commerce's body.
- **How to reproduce:** `gh run view 44093031010 --job 134072872120 --log | grep relayed`.
- **Impact:** Any 409 passes as the intended refusal; the console cannot tell the operator why an approval failed.
- **Fix:** Relay the error code in admin-service; require it in `commerceRefusedByConsole`.
- **Acceptance:** The e2e fails on a 409 with another code.
- **Effort:** XS
- **Was:** COM-F-13

### com-85

- **ID:** com-85
- **Title:** Android has no instrumented commerce tests; the Razorpay launcher and process death are proven only with JVM fakes.
- **Severity:** P3
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** No `src/androidTest` in the five commerce modules; `PaymentLauncherOneFlightTest` and `CheckoutProcessDeathTest` run on the JVM.
- **How to reproduce:** `git ls-files mobile/android | grep -c "commerce.*/src/androidTest"` = 0.
- **Impact:** Razorpay result delivery after real activity recreation is unproven on a device.
- **Fix:** One instrumented test with Razorpay test mode and `ActivityScenario.recreate()` during the sheet.
- **Acceptance:** Runs in a device job (the founder runs the emulator).
- **Effort:** M
- **Was:** COM-F-14

---

## Inherited platform findings (not counted)

Re-checked for commerce and payments at `be57a46bf` by Tracks B and E.

| Source | What | Status for commerce and payments | Evidence |
|---|---|---|---|
| feed-27 | One shared internal key, no NetworkPolicy | Applies: commerce's gateway-trust key is `INTERNAL_SERVICE_KEY`; the commerce consequence is com-9 | VERIFIED-READ |
| feed-64 | Wildcard CORS reflected with credentials | Applies at the gateway | VERIFIED-READ |
| reels-21 | Web sets no CSP, frame-ancestors or HSTS | Applies; the web holds the access token in memory | VERIFIED-READ |
| reels-77 | `access_token` cookie is a gateway credential | Applies; `SameSite=Lax` keeps cross-site POSTs out; same-site sibling host SUSPECTED | VERIFIED-READ |
| feed-25 | Web `/v1` proxy forwards no client address | Applies: all web shoppers share one per-IP edge bucket | VERIFIED-READ |
| reels-78 / feed-26 | Raw Go error text returned | Applies: com-67 | VERIFIED-READ |
| feed-01 | `/livez` not served | Fixed for both services | VERIFIED-READ; render test VERIFIED-RUN |
| feed-02 | No environment and no deployable image | Still open | VERIFIED-READ |
| feed-10 | Alerts reach nobody | Partly fixed (ops SNS e-mail; subscription confirmation manual); commerce gaps are com-15 | VERIFIED-READ |
| feed-11 | No rollback procedure | Written, never drilled; silent on gated migrations and the shared `commerce_db` | VERIFIED-READ |
| feed-12 | No tested restore | Still open; commerce consequences are com-47 | VERIFIED-READ |
| feed-42 | Shutdown closes the DB before draining HTTP, no preStop | Still open for both services | VERIFIED-READ |
| feed-43 | Readiness on a shared store | Commerce instance is com-43 | — |
| feed-46 | Request ID not forwarded | Partly fixed; commerce → payments and Kafka hops are com-45 | VERIFIED-READ |
| feed-47 | No CI verdict on the shipped code | Still open; not re-run | UNVERIFIED |
| feed-75 | Boot migrations without an advisory lock | Fixed except setup.sql (com-76) | VERIFIED-READ |
| feed-77 | Staging values stale | Still open; for commerce a boot failure (com-46) | VERIFIED-READ |
| feed-06 | DLQ has no replay | Applies: `social.events.v1.dlq` (com-15) | VERIFIED-READ |
| reels-6 | NetworkPolicy not enforced | Applies (com-35) | VERIFIED-READ |
| reels-63 | End-of-life Alpine 3.19, no image scan | Applies to both Dockerfiles | VERIFIED-READ |
| reels-62 | Chart QA guard fails | Still failing, now on post-service; no commerce or payments failure | VERIFIED-RUN |

21 rows (reels-78 and feed-26 share one row). feed-43's commerce instance is counted as com-43 and feed-01 is fixed; neither row adds to the counts.

## Out of scope observations

- Virtual try-on is Android-only; the web has no try-on.
- The "landing is always /shop" rule is ambiguous now that Social is a launch app; not assessed.
- The admin web console's location (repository or branch) is unknown; its commerce screens were not read.
- The `.gitleaksignore` entries were not reviewed.
- The main Security workflow fails on this branch (not caused by commerce).
- E2E fails on the video tier and Android Lint fails on this branch (not caused by commerce).
- The untagged payments `store_db_test.go` points at `commerce_db`, and CI creates a database named `payments_p0` with no `_test` suffix.
- commerce-service and payments-service both connect as `commerce_db_owner`.
- The Azure overlays use the `latest` image tag.
- Open adviser and founder questions: Bill of Supply for books-only sellers, TCS under CGST s.52, who files GSTR-1, and who is merchant of record.
- The source column for `sellerPlaceOfSupply` is not established; the seller's pickup address is editable after approval and its state is not cross-checked against the GSTIN.
- `charts/atpost-service/render_test.sh` and `scripts/verify/migration-compat.sh` are not run in CI.
- The chart QA guard fails on post-service (`ACTION_LINK_OWN_DOMAINS`).
- With bank offers switched on, a cancel could refund more than was captured; offers are off today.
