# Commerce (MStore / MSeller): Track A, functional completeness

- **Audit date:** 2026-11-17. Read-only. Backend and Android tree `C:\workspace\modernsmapp` @ `be57a46bf` (branch `codex/module-01-02-launch-safety`, dirty working tree; **commerce-service has no uncommitted files**, so its working copy equals HEAD). Web tree `C:\workspace\wt\pb-echo` @ `1b596b4`, clean. payments-service files with uncommitted edits (`admin_routes.go`, `admin_token.go`, `refund_required.go`, store `admin.go`, `refund_attention.go`, `refund_required.go`) were not cited; deploy values were read with `git show HEAD:`.
- **What was run:** `go test ./... -count=1` in `Architecture/services/commerce-service` (unit tests only, no integration tag): every package `ok` (VERIFIED-RUN that those unit tests pass; they are not a journey). Nothing else was executed: no integration tests (they need a database), no Android build, no web tests, no Docker, no Razorpay, no Shiprocket.
- **Paths:** shorthand as in `00-map.md`: `C/` = `Architecture/services/commerce-service/`, `H/` = `C/internal/http/`, `S/` = `C/internal/service/`, `P/` = `C/internal/store/postgres/`, `PAY/` = `Architecture/services/payments-service/`. Android paths are relative to `mobile/android/`, with `API` = `core/commerce/src/main/kotlin/com/us/android/core/commerce/network/CommerceApi.kt`, `FC/` = `feature/commerce/src/main/kotlin/com/us/android/feature/commerce/`, `FS/` = `feature/seller/src/main/kotlin/com/us/android/feature/commerce/`. Web paths are relative to `C:\workspace\wt\pb-echo\src`.
- **Rating rule:** COMPLETE or PARTIAL means the path was followed in code from the client call to the server store (or, for a server-only rating, from the route to the store). A screen or route only located is UNVERIFIED. "Fenced" means switched off for launch on purpose (map section 8); the rating says what the user sees instead.

## 1. Feature ratings

| Feature | Server | Android | Web | Evidence and notes |
|---|---|---|---|---|
| Seller onboarding and verification | PARTIAL (READ) | COMPLETE (READ, API surface) | COMPLETE (READ, API surface) | Readiness requires sell scope (GSTIN, or PAN + business name + business address + address proof for books-only), store name, email, pickup address, payout account, KYC document (`P/sellerreadiness.go:122-161`); submit refuses with every missing item (`S/onboarding.go:296-311`). Android covers start, documents, payout, sell-scope, seller address, submit (`API:329,387,393,399,403,413`); web covers every step (`features/shop/api/sell.ts:56-127`). **Approval is manual and has no working tool in any deployed ENV** (COM-A-3). KYC is format-only everywhere (map 7) |
| Books-only sellers (PAN-only) | COMPLETE (READ) | COMPLETE (READ, API surface) | COMPLETE (READ, located) | `045_books_only_sellers.sql:1-40`; listing held to book categories and a zero-rated class on create and edit (`S/catalogue_products.go:123`, `S/productwrite.go:645`, `S/sellscope.go:346`); upgrade needs a GSTIN matching the PAN (`API:426`) |
| Catalogue, variants | COMPLETE (READ) | PARTIAL (READ) | COMPLETE (READ, client) | Selling price above MRP refused (`S/reviewedcontent.go:64-79`). Android can create a product, edit a variant price, upload media, submit (`API:345,364,154,371`); it has no product edit (`PATCH /products/:id`), attribute, media reorder/delete, readiness or action-needed calls, all of which web has (`features/shop/api/sell.ts`, `sellMedia.ts`). COM-A-7 |
| Inventory | COMPLETE (READ) | COMPLETE (READ, API) | COMPLETE (READ, client) | Stock read/adjust `API:297,316`; web `sell.ts` `/seller/variants/:id/stock`; seller-scoped idempotency (map 2.1) |
| Search and browse | COMPLETE (READ, map) | COMPLETE (READ, API) | COMPLETE (READ, located) | `GET /products?q=&category_id=&sort=` (`API:49-56`); web `/shop/browse`. Ranking quality not assessed |
| Cart | COMPLETE (READ) | COMPLETE (READ, API) | COMPLETE (READ, client) | Keyed on the user (map 2.1); `API:169-181`; `features/shop/api/bag.ts:15-30`. Single seller per cart (`MULTIPLE_SELLERS`) |
| Address book | COMPLETE (READ, map) | PARTIAL (READ) | COMPLETE (READ, client) | Android has list and add only (`API:188,191`); no edit, delete or set-default. Web calls `/addresses/:id` and `/:id/default`. COM-A-6 |
| Checkout | COMPLETE (READ) | PARTIAL (READ) | COMPLETE (READ) | Server: quote bound to cart version, items hash, address content, coupon and method (`P/checkout.go:349-418`); price from the database only, `expected_total_minor` compared, never used (`P/checkout.go:436-451,515-521`); mandatory `Idempotency-Key` (`H/handler_p0.go:362-370`). Android has no way to enter a coupon: `couponCode` is only ever restored from saved state (`FC/checkout/CheckoutViewModel.kt:136-139,450`). COM-A-6 |
| Payment: UPI and card (Razorpay) | COMPLETE (READ) with one SUSPECTED gap | COMPLETE (READ, map + client) | COMPLETE (READ, map) | Signed webhook → payments → `payment.succeeded` → commerce `ApplyPaymentSucceeded` (`P/p0.go:110-350`). Neither client marks paid. Razorpay orders carry no `payment_capture` and `Capture` has no caller (COM-A-13) |
| Payment: cash on delivery | FENCED | FENCED | FENCED | Refused at checkout with `COD_NOT_SUPPORTED` (`H/handler_p0.go:410-421`) and at intent creation (map 2.2). Android offers UPI and Card only (`FC/checkout/CheckoutState.kt:185-188`). User sees no COD option |
| Order lifecycle | BROKEN (READ) | PARTIAL (READ) | PARTIAL (READ) | Paid orders are booked with the courier and set to `shipped` within about a second of payment (COM-A-1). Under the stub courier (qa; prod if the secret says so) nothing ever moves the order past `shipped` |
| Shipping and tracking | PARTIAL (READ) | MISSING (READ) | COMPLETE (READ, client) | Server books per seller, Shiprocket webhook verified (map 2.1). Booking sends ₹0 values (COM-A-4). Android has no shipments call (`API` endpoint list); web `/orders/:id/shipments` (`features/shop/api/orders.ts`). Neither client offers seller mark-delivered (COM-A-9) |
| Cancellation | BROKEN (READ) | BROKEN (READ) | BROKEN (READ) | Store path is sound (owner check, matrix, release, restock, durable refund: `P/p0.go:674-821`), but the auto-booking moves every paid order out of the cancellable states almost at once (COM-A-1) |
| Return | FENCED | FENCED, shown | FENCED | `/returns`, `/seller/returns`, `/me/returns` 404 (`H/handler_p0.go:43-59`). Android seller hub still shows a Returns tile that errors (COM-A-8); web has no return screen |
| Refund, full | PARTIAL (READ) | n/a | n/a | Only via cancel or late capture → `order_refund_commands` → payments (map 10). No admin refund route (COM-A-2) |
| Refund, partial | MISSING (READ) | MISSING | MISSING | No route performs one; returns fenced; admin has none (COM-A-2) |
| Taxes | COMPLETE (READ) | n/a | n/a | Per-line GST, CGST+SGST or IGST by seller state vs destination, unknown state refused (`P/checkout.go:494-521`) |
| Invoices | PARTIAL (READ) | MISSING (READ) | COMPLETE (READ, client) | Issued by the paid-order job from stored paise (`S/shipments.go:792-810`); buyer name printed as the street (COM-A-12); no e-invoice IRN. Android has no invoice call; web `orders.ts` `/orders/:id/invoice` |
| Coupons, seller-funded | COMPLETE (READ) | MISSING (READ) | COMPLETE (READ, client) | Claim inside the checkout transaction (`P/checkout.go:474-487`). Web buyer `api/coupons.ts:17`, seller `api/sellerCoupons.ts`. Android neither (COM-A-6, COM-A-7) |
| Coupons, platform-funded | FENCED | — | — | `COMMERCE_PLATFORM_COUPONS_ENABLED=false` (HEAD `values-prod.yaml:130`) |
| Bank offers | FENCED | MISSING | PARTIAL (located) | `PAYMENTS_OFFERS_ENABLED` off; prod commerce also lacks `payments:offers.read` (map 2.2). Web `api/offers.ts:15` |
| Reviews | COMPLETE (READ, map) | MISSING (READ) | COMPLETE (READ, client) | Review needs the item on the caller's own **delivered** order (map 2.1), which the stub courier never reaches (COM-A-1). Android neither reads nor writes reviews (no `/reviews` in `API`) |
| Seller dashboard | PARTIAL (READ) | MISSING (READ) | PARTIAL (READ, client) | Revenue always ₹0 (COM-A-5). Android has no `/dashboard` call |
| Seller payouts | MISSING (map) | FENCED, shown | MISSING | No payout executor, `CreatePayoutBatch` has no caller, `/payout`, `/seller/earnings` fenced (map 9; `H/handler_p0.go:49-51`). Android Earnings tile errors (COM-A-8). How sellers are paid at launch is open (map Q3) |
| Buyer notifications | PARTIAL (READ) | UNVERIFIED | UNVERIFIED | paid, shipped, delivered, cancelled, refunded, payment_failed (`notification-service/internal/events/commerce_consumer.go:9-17`); nothing for `late_capture_refunded` or `partially_refunded` (COM-A-11). "Shipped" fires at booking, seconds after payment (COM-A-1) |
| Seller notifications | PARTIAL (READ) | UNVERIFIED | UNVERIFIED | Only `seller_new_order` on paid (`commerce_consumer.go:9`); no buyer-cancel, refund, KYC or moderation notice (COM-A-11) |
| Admin tools | BROKEN in deployed ENVs (READ) | n/a | n/a (admin web not audited) | Token routes answer 401 because `SERVICE_CALLERS` is commented out (HEAD `deploy/services/commerce-service/values-prod.yaml:158-160`); only the internal-key group works, from inside the cluster (COM-A-3) |
| Dispute tools | MISSING (map) | n/a | n/a | Nothing sets `disputed`; no admin route for disputes, order refunds or cancel on behalf (map 2.3, 10) |
| Try-on (Face AR) | COMPLETE (map) | COMPLETE (map) | MISSING | Android-only; not a parity gap in the founder's direction |
| Local delivery | FENCED | FENCED | FENCED | `COMMERCE_LOCAL_DELIVERY_ENABLED` off |

## 2. Journeys

None can be executed under the audit rules. Each was walked through the code; "a real run needs" says what would confirm it.

1. **Full purchase by UPI or card, through to delivery.** Quote (`H/handler_p0.go:303`) → checkout v2 locks cart, quote, address, variants and inventory, prices from the database, compares `expected_total_minor`, reserves stock for 20 min, writes the order `payment_pending` and an outbox row (`P/checkout.go:349-675`) → `POST /orders/:id/payment/intent`, amount from the order (`S/checkout_p0.go:427-470`) → Razorpay Checkout with the server `order_id`/`key_id` (map 7) → signed webhook → payments dedupe and money check → `payment.succeeded` → commerce confirms, commits stock and enqueues `fulfill_paid_order` in the same transaction (`P/p0.go:310-343`). **Breaks at:** within about 1 s the fulfilment worker issues the invoice, books the courier with ₹0 declared value and sets the order `shipped` (COM-A-1, COM-A-4). With Shiprocket, delivery then comes from the signed courier webhook; with the stub, the order stays `shipped` for ever and the buyer can never review. If the Razorpay account does not auto-capture, there is no `payment.captured` at all (COM-A-13). **A real run needs:** qa (test keys, webhook ingress present) with `COURIER_PROVIDER=shiprocket` sandbox, one UPI and one card payment, then the order's status history, the Shiprocket order's declared value, and the delivered webhook.
2. **COD.** Not offered: refused at the edge and at payments; neither client shows it (FENCED). **A real run needs:** nothing for launch.
3. **Payment succeeds at the provider, user closes the app before returning.** Server side is independent of the client: the webhook (prod and qa ingress) or, failing that, the reconciler after `PENDING_AGE_SEC` (600 s prod; `PAY/internal/service/p0.go:709-820`) confirms the order. Android keeps a continuation and resumes from the server on reopen (`FC/checkout/CheckoutViewModel.kt:314-386`); web polls `/payment` (map 11.2). Works (VERIFIED-READ). Staging has no webhook ingress, so it relies on the reconciler only. **A real run needs:** qa, pay in the Razorpay sheet, kill the app before the callback, reopen after 30 s and after 11 min with the webhook blocked.
4. **Payment fails.** `payment.failed` changes nothing on the intent so the buyer can retry on the same Razorpay order (`PAY/internal/service/p0.go:82-93`); the reconciler sets `failed` only after the 15-minute quiet window, then commerce moves the order to `payment_failed`, and `OpenPaymentForOrder` re-holds stock under a new attempt (`S/checkout_p0.go:427-460`). The reservation TTL is 20 minutes, so a buyer who waits sees "confirming" for up to about 15 minutes before "failed". Works as designed (VERIFIED-READ). **A real run needs:** qa with a Razorpay test failure card, timing of the client state changes.
5. **Webhook twice, late, out of order.** Twice: `provider_events (provider, event_id)` in payments and `payment_event_inbox` in commerce, each in the effect's transaction (`P/p0.go:133-163`) — absorbed. A second event id for the same capture is `captureRedelivered`, a no-op (`P/p0.go:256-263`). Late (after the 20-minute expiry or a cancel): `captureLate` writes a durable refund command and owed-capture row and never fulfils (`P/p0.go:265-309`). Out of order: a `payment.failed` after a capture is ignored (it never changes state); Kafka partitions on `reference_id` (map 4). A second paid intent on the same order is recorded as a duplicate capture whose refund is OFF in every deployed ENV (COM-A-10). Replay with a fresh unsigned event id is a security-track item (map Q7). **A real run needs:** the integration suites `duplicate_capture_*`, `payment_mismatch_*`, `expiry_sweep_*` (CI only) plus a qa replay of a captured webhook with a new `X-Razorpay-Event-Id`.
6. **Two buyers check out the last unit at once.** Both lock `inventory_items ... FOR UPDATE` in variant-id order (`P/checkout.go:1006-1044`); the second sees availability 0 and gets `OUT_OF_STOCK`; the `reserved_qty <= total_qty` CHECK backs it (`P/checkout.go:684-704`). Correct (VERIFIED-READ); proven by `P/proof_integration_test.go:250` in CI (not run here).
7. **Change the price in the request.** The checkout body has no price, discount or shipping field (`H/handler_p0.go:372-379`); `expected_total_minor` must be positive and must equal the server's computed total, else `PRICE_CHANGED` (`P/checkout.go:436-451,515-521`); the intent amount comes from the order (`H/handler_p0.go:464`, `S/checkout_p0.go:461-470`). Cannot be tampered with (VERIFIED-READ).
8. **Cancel before shipping.** Buyer cancel is allowed from `payment_pending`, `confirmed`, `packed` (map 10) and the store path refunds what is owed (`P/p0.go:674-821`). **Fails in practice:** the order is `shipped` about a second after payment (COM-A-1). An unpaid `payment_pending` order cancels fine. **A real run needs:** qa, pay, then press Cancel as soon as the order page shows.
9. **Cancel after shipping.** Refused for buyer and seller; `shipped → cancelled` is admin-only (`010_gst_and_state_machine.sql:90,113`) and no admin route performs it (COM-A-2). The buyer is told to wait; no return exists (fenced).
10. **Partial and full refund.** Full: only through cancel (dead after payment, journey 8) or a late capture. Partial: no route anywhere (COM-A-2). Refund settlement itself (payments refund webhook → `payment.refunded` → commerce `refunded`) is implemented (map 10, `P/p0.go:1129-1225`).
11. **Seller A requests seller B's order by id.** `GET /seller/orders/:id` returns `ErrNotOrderOwner` when A has no line (`S/orders_read.go:288-298`); pack/cancel/ship require every line to be A's (`S/fulfilment.go:108-126`); `/orders/:id/shipment(s)` and `/invoice` require a seller on the order (`H/shipments.go:35-61`, `S/order_lifecycle.go:51-82`). Refused (VERIFIED-READ).
12. **Buyer requests another buyer's invoice by id.** `GET /orders/:id/invoice` → `requireOrderRole` → 403 `FORBIDDEN` for a non-customer non-seller (`H/shipments.go:54-58,201-215`). Refused (VERIFIED-READ); the 403-vs-404 difference confirms the order id exists (P3, COM-A-14).

## 3. Founder rules

| Rule | Result | Evidence |
|---|---|---|
| Android has every feature the web has | **FAIL** | Missing on Android: coupons (buyer and seller), reviews (read and write), invoice download, shipment tracking, address edit/delete/default, bank offers, seller dashboard, product edit/attributes/media reorder/readiness/action-needed, seller fulfilment queue. Endpoint inventory `API:49-530` vs web `features/shop/api/*.ts`. COM-A-6, COM-A-7 |
| Every payment through payments-service on the signed provider event (nothing manual) | PASS (VERIFIED-READ) | Signed webhook or reconciler fetch → payments → `payment.succeeded` → commerce (map 10); the stub `/payment/confirm` route is registered only with `PAYMENTS_ALLOW_STUB`, refused outside local (map 2.1). Caveat COM-A-13 (capture mode) |
| Seller approval stays manual | PASS on design, **no working tool** | `COMMERCE_PRODUCT_AUTO_APPROVE=false`; no auto-approve for sellers found. The admin console path answers 401 in every ENV (COM-A-3) |
| Books sellers are PAN-only | PASS (VERIFIED-READ) | `P/sellerreadiness.go:129-141`; `045_books_only_sellers.sql`. Invoices for an unregistered seller are labelled "Tax Invoice / Bill of Supply" (`Architecture/shared/invoice/generator.go:215`); whether that satisfies the adviser is a Track tax item |
| Landing is /shop | PASS for the shop entry (VERIFIED-READ) | Legacy `/commerce`, `/cart`, `/checkout`, `/orders`, `/products` redirect into `/shop/*` (`app/commerce/page.tsx`, `app/cart/page.tsx`, etc.). The site root `/` is the social app (`app/page.tsx`); if the rule means the site root, it FAILS — UNVERIFIED which is meant |

**Fenced for launch and what the user sees:** COD (no option shown); returns (web: nothing; Android seller: Returns tile → error state); payouts and earnings (web: nothing; Android: Earnings tile → error); platform coupons (seller coupons only); bank offers (none listed); local delivery (courier only); RFQ, B2B, affiliate, bulk import (no client entry found). None of these is rated BROKEN.

## 4. Findings

### COM-A-1

- **ID:** COM-A-1
- **Title:** Every paid order is booked with the courier and set to `shipped` about one second after payment, so neither buyer nor seller can cancel it, the seller's pack and own-courier steps are dead, and under the stub courier the order never reaches `delivered`.
- **Severity:** P0
- **Category:** functional (order lifecycle, cancellation)
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - Payment confirmation enqueues `fulfill_paid_order` in the same transaction (`P/p0.go:341-343`).
  - The worker polls every second (`C/internal/workers/fulfillment.go:46`) and runs `FulfillPaidOrderJob`, which issues the invoice and calls `CreateShipmentForOrder` whenever a courier is configured (`S/jobs.go:43-56`); a courier is always configured, the stub being the default (`C/cmd/server/main.go:138`, `C/internal/courier/courier.go:125-135`).
  - The booking stamps `ShippedAt: now` and calls `MarkOrderShipped` as `system` (`S/shipments.go:286,357`); migration 033 admits `confirmed → shipped` for `system` ("the worker booked before the seller packed", `C/database/migrations/033_system_ships_at_booking.sql:1-14`).
  - From `shipped`, cancel is admin-only (`010_gst_and_state_machine.sql:90,113`) and no admin route exists (map 2.3). Seller pack requires `confirmed` and seller cancel `confirmed|packed` (`S/fulfilment.go:128-161`). A later seller "ship" with their own courier finds the existing shipment and reuses it (`S/shipments.go:182-186`).
  - A stub or carrier booking is not `seller_managed` (`S/shipments.go:289-291`), so the seller cannot mark it delivered (`S/seller_delivery.go:92-108`); the stub parses no webhooks (`courier.go:156-161`). qa runs the stub, prod reads `COURIER_PROVIDER` from a secret and does not refuse the stub (map 7).
  - The buyer gets an "order shipped" notice at booking time (`commerce_consumer.go:10`).
- **How to reproduce:** On qa, buy an item and pay with a Razorpay test card. Open the order: it reads Shipped with a `STUB…` AWB; Cancel is not offered (`can_cancel` false); the seller's Pack button fails; the order never becomes Delivered and Write a review never appears.
- **Impact:** The "cancel before shipping" journey and the whole seller fulfilment flow (pack, cancel for out-of-stock, ship with own courier) do not work; a buyer who paid has no way to get money back (COM-A-2). With Shiprocket, the order says Shipped before any pickup. With the stub, orders are stuck and reviews are impossible.
- **Fix:** Smallest: drop the `CreateShipmentForOrder` call from `FulfillPaidOrderJob` (keep the invoice), so booking happens when the seller presses Ship (`POST /seller/orders/:id/ship`, already wired on both clients). If auto-booking is wanted, book on `packed` and leave the order `confirmed`/`packed` until the courier's first pickup event moves it to `shipped`. Separately, refuse `COURIER_PROVIDER` empty or `stub` when `ENV=prod`.
- **Acceptance:** A service test that `FulfillPaidOrderJob` leaves a paid order `confirmed`; a test that a buyer can cancel a paid order before the seller ships and a refund command is written; a boot test that `ENV=prod` with the stub courier refuses to start.
- **Effort:** S

### COM-A-2

- **ID:** COM-A-2
- **Title:** There is no way to refund a buyer after shipping, no partial refund anywhere, and no admin or dispute tool, so the only refund path at launch is a cancel that COM-A-1 makes unreachable.
- **Severity:** P1
- **Category:** functional (refunds, admin)
- **Evidence level:** VERIFIED-READ
- **Evidence:** Returns fenced (`H/handler_p0.go:52-58`); `delivered → refund_pending` and `shipped → cancelled` are admin-only rows (`010_gst_and_state_machine.sql:113`; map 10) with no admin route calling them (map 2.3: "No admin route exists for disputes, order refunds, returns, order cancel on behalf of a buyer"); nothing sets `disputed` (map 10). The admin token routes that exist answer 401 (COM-A-3).
- **How to reproduce:** A buyer receives a damaged item. There is no Return button (fenced), support has no console action, and the only code path is an in-cluster SQL or internal-key call.
- **Impact:** Every post-shipment complaint, chargeback or wrong item needs a database hand-edit, outside payments' audit trail; Razorpay disputes are invisible to the order.
- **Fix:** Smallest: an admin-token route `POST /internal/admin/orders/:id/refund {amount_minor, reason}` (permission `commerce:refunds.issue`, step-up, two-person above a threshold) that writes `createScopedRefundCommandTx` for at most the unrefunded balance and moves `delivered|shipped → refund_pending` as `admin`; surface it in admin-service. Map Razorpay `payment.dispute.*` to `disputed` later.
- **Acceptance:** Integration tests: full and partial admin refund of a delivered order create one refund command each, never exceed the captured amount, and settle to `refunded`/`partially_refunded`.
- **Effort:** M

### COM-A-3

- **ID:** COM-A-3
- **Title:** Seller approval and product moderation have no working tool in any deployed environment, because commerce's admin-token routes answer 401 everywhere and auto-approve is off, so no seller or listing can go live without an in-cluster internal-key call.
- **Severity:** P1
- **Category:** functional (seller onboarding, admin)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `SERVICE_CALLERS` and `SERVICE_CALLER_ADMIN_SERVICE_OPS` are commented out (HEAD `deploy/services/commerce-service/values-prod.yaml:158-160`; same in qa, staging and Azure per map 8); admin-token verification needs them (`H/admin_token.go:121-171`). `COMMERCE_PRODUCT_AUTO_APPROVE: "false"` (`values-prod.yaml:132`). The internal-key group (`H/handler_onboarding.go:36-73`) works only in-cluster with an unverified actor.
- **How to reproduce:** On qa, submit a complete seller application; open the admin console's MStore sellers queue: 401.
- **Impact:** MSeller cannot onboard anyone at launch without engineers; approvals done by `curl` bypass step-up and the admin audit log.
- **Fix:** Apply the documented Wave 2 order: add the admin-service caller key to commerce's externalSecret, then uncomment the two env lines in each values file, then admin-service's lines (`values-prod.yaml:93-94` comment).
- **Acceptance:** On qa, the console approves a seller and a product; `commerce_admin_audit_log` has both rows; a direct internal-key approve from outside admin-service is refused or alarmed.
- **Effort:** S

### COM-A-4

- **ID:** COM-A-4
- **Title:** The courier booking declares a package value of ₹0 and item prices of ₹0, because it reads the legacy rupee columns that the P0 checkout writes as zero.
- **Severity:** P1
- **Category:** functional (shipping), money
- **Evidence level:** VERIFIED-READ for what is sent; SUSPECTED for the courier's response
- **Evidence:** Checkout writes `unit_mrp, unit_price, discount_amount, tax_amount, final_price` as `0` (`P/checkout.go:609`) and the order's rupee columns as 0 (`:544`). `GetOrderItems` scans both families (`P/orders.go:383-410`), and the booking uses the rupee ones: `packageValue += it.FinalPrice`, `Price: it.UnitPrice` (`S/shipments.go:239,242`), sent as Shiprocket `sub_total` and `selling_price` (`C/internal/courier/shiprocket.go:133,154`). The invoice was fixed for the same defect (`S/shipments.go:792-797`); the booking was not. To confirm: one Shiprocket sandbox booking from a P0 order, read back the declared value or the 4xx.
- **How to reproduce:** On a Shiprocket-backed ENV, pay for an order and read the Shiprocket order: sub-total ₹0 (or the job dead-letters with a validation error and the order stays `confirmed` with no shipment).
- **Impact:** Either no shipment can ever be booked (every paid order stuck), or parcels travel declared at ₹0: no transit insurance claim value, wrong e-way-bill threshold, and a courier invoice mismatch.
- **Fix:** In `CreateShipmentsForOrder`, use `it.FinalPriceMinor` and `it.UnitPriceMinor` converted to rupees (`float64(x)/100`); same in `recordCODRemittance` before COD is ever unfenced.
- **Acceptance:** A unit test with a fake courier asserting the request's `PackageValue` equals the seller's lines' `final_price_minor/100` for a P0 order.
- **Effort:** S

### COM-A-5

- **ID:** COM-A-5
- **Title:** The seller dashboard reports total revenue as ₹0 for every seller, because it sums the legacy `final_price` column.
- **Severity:** P2
- **Category:** functional (seller dashboard)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `SELECT COALESCE(SUM(oi.final_price),0) … AND o.payment_status='paid'` (`P/onboarding.go:704-707`), while checkout writes `final_price` as 0 (`P/checkout.go:609`). `OrdersToday` counts joined item rows, not orders, and includes unpaid ones (`P/onboarding.go:699-703`). Every query's error is discarded (`_ =`).
- **How to reproduce:** A seller with paid orders opens the web dashboard (`GET /v1/commerce/dashboard`): revenue 0.
- **Impact:** The seller's first number is wrong; support load and loss of trust.
- **Fix:** Sum `final_price_minor` (return paise), count `DISTINCT o.id` with `payment_status IN ('paid','partially_refunded')`.
- **Acceptance:** A store test with two paid orders of two lines each returns the paise total and `orders_today = 2`.
- **Effort:** S

### COM-A-6

- **ID:** COM-A-6
- **Title:** Android buyers cannot apply a coupon, read or write reviews, download an invoice, track a shipment, or edit, delete or default an address, all of which the web offers.
- **Severity:** P1
- **Category:** functional (parity)
- **Evidence level:** VERIFIED-READ
- **Evidence:** Android endpoint list `API:49-530` has no `/cart/coupons`, `/reviews`, `/invoice`, `/shipments`, `/addresses/{id}` or `/payment-offers`. `couponCode` is never set from the UI (`FC/checkout/CheckoutViewModel.kt:136-139`; only read at `:450`). Web: `features/shop/api/coupons.ts:17`, `reviews.ts`, `orders.ts` (invoice, shipments), `addresses.ts` (`/addresses/:id`, `/:id/default`), `offers.ts:15`.
- **How to reproduce:** Android MStore: checkout has no coupon field; an order detail shows no tracking or invoice; a product shows no reviews; a saved address cannot be changed.
- **Impact:** Breaks the founder rule ("Android is the super app"); Android buyers pay more than web buyers for the same cart and cannot see where their parcel is.
- **Fix:** In order: coupon picker in checkout (`GET /cart/coupons`, pass `coupon_code` to quote and checkout), tracking and invoice on order detail (`GET /orders/:id/shipments`, `GET /orders/:id/invoice` opened in the browser), reviews read on product and write from a delivered order, address edit/delete/default.
- **Acceptance:** ViewModel tests per feature against a fake API; the checkout test proves the quote and checkout carry the same coupon.
- **Effort:** M

### COM-A-7

- **ID:** COM-A-7
- **Title:** Android sellers cannot create coupons, see the dashboard, edit a product after creation, set attributes, reorder or delete images, or see readiness and action-needed, all of which the web seller centre offers.
- **Severity:** P2
- **Category:** functional (parity)
- **Evidence level:** VERIFIED-READ
- **Evidence:** Absent from `API:280-530`: `/seller/coupons`, `/dashboard`, `PATCH /products/{id}`, `/products/{id}/attributes`, `/media/order`, `DELETE /media/{id}`, `/products/{id}/readiness`, `/seller/action-needed`, `/seller/fulfillment`. Web has each (`features/shop/api/sellerCoupons.ts`, `sell.ts:56-333`, `sellMedia.ts`).
- **How to reproduce:** Android MSeller: no Coupons entry; a published listing's title cannot be edited.
- **Impact:** Founder parity rule; Android-only sellers must use the web for routine catalogue work.
- **Fix:** Add product edit and media management first, then readiness, dashboard and coupons.
- **Acceptance:** ViewModel tests per screen against a fake API.
- **Effort:** L

### COM-A-8

- **ID:** COM-A-8
- **Title:** The Android seller hub shows Returns and Earnings tiles that call fenced routes and always land on an error screen.
- **Severity:** P2
- **Category:** functional (fenced feature exposure)
- **Evidence level:** VERIFIED-READ
- **Evidence:** Tiles `FS/seller/SellerScreen.kt:271-272` → `SellerReturnsRoute`, `SellerEarningsRoute` (`FS/navigation/SellerNavigation.kt:146-147,164-178`) → `GET seller/returns`, `POST returns/{id}/approve|reject`, `GET seller/earnings` (`API:500-530`) → 404 by the fence (`H/handler_p0.go:49-58`); the earnings screen renders `UsErrorState` on failure (`FS/seller/SellerEarningsScreen.kt:60`).
- **How to reproduce:** Android MSeller → Earnings: error.
- **Impact:** Sellers read "something went wrong" where the product says "not yet"; support tickets about missing money.
- **Fix:** Hide both tiles behind a server-driven capability (or a build flag) until the fence lifts; or show a "coming soon" state on a 404.
- **Acceptance:** A UI test that the hub shows neither tile when the capability is off.
- **Effort:** S

### COM-A-9

- **ID:** COM-A-9
- **Title:** Neither client offers the seller "mark delivered" action, so an order a seller ships with their own courier can never be completed from an app.
- **Severity:** P2
- **Category:** functional (fulfilment)
- **Evidence level:** VERIFIED-READ
- **Evidence:** Server route `POST /seller/orders/:id/mark-delivered` (`H/handler_seller_fulfilment.go:139-166`). No `mark-delivered` call in Android (`API` list; search of `feature/seller`, `core/commerce`) or web (`features/shop/model/sell.ts:1038-1046` routes only pack, ship, cancel).
- **How to reproduce:** Once COM-A-1 is fixed, a seller ships with their own courier on the stub ENV; the order stays `shipped`.
- **Impact:** No delivered state, no review eligibility, no return window start.
- **Fix:** Add the action to both seller order-detail screens when every shipment is `seller_managed`.
- **Acceptance:** Client tests that the button appears only for a seller-managed shipped order and posts the route.
- **Effort:** S

### COM-A-10

- **ID:** COM-A-10
- **Title:** A second capture on an already-paid order, and a capture whose amount disagrees with the intent, are recorded as owed but never refunded in any deployed environment.
- **Severity:** P2
- **Category:** functional (refunds), money
- **Evidence level:** VERIFIED-READ
- **Evidence:** commerce records a duplicate and emits `commerce.order.duplicate_capture` (`P/p0.go:211-213,989-1031`); payments records mismatches in `refund_required` (`PAY/internal/service/p0.go:141-155,1154`). Both the consumer and the refund worker default OFF outside dev (`PAY/internal/config/config.go:56-71,176-190`); HEAD prod values set neither (map 8). The admin resolve route is token-only and 401 today (map 2.2).
- **How to reproduce:** On qa, pay attempt 1, let the order fail and retry, then complete attempt 1 late: the second capture appears in `refund_required` and stays there.
- **Impact:** Rare, but each case keeps a buyer's money with no automatic return and no console to return it.
- **Fix:** Set `PAYMENTS_DUPLICATE_CAPTURE_CONSUMER=true` and `PAYMENTS_REFUND_REQUIRED_WORKER=true` in qa, then prod, once COM-A-3's caller wiring lets an operator watch `needs-attention`.
- **Acceptance:** qa run of the journey above ends with a Razorpay refund and `refund_required` settled.
- **Effort:** S

### COM-A-11

- **ID:** COM-A-11
- **Title:** Sellers are notified only of new paid orders, and buyers are not told about late-capture or partial refunds.
- **Severity:** P2
- **Category:** functional (notifications)
- **Evidence level:** VERIFIED-READ
- **Evidence:** consumer table `notification-service/internal/events/commerce_consumer.go:9-17` (paid → buyer + seller; shipped, delivered, cancelled, refunded, payment_failed → buyer only). commerce emits `commerce.order.late_capture_refunded` (`P/p0.go:303`) and `partially_refunded` (map 4), which no consumer reads.
- **How to reproduce:** Buyer cancels a confirmed order: the seller gets no notice and may still pack it.
- **Impact:** Sellers ship cancelled orders; buyers whose late payment is refunded hear nothing and contact support.
- **Fix:** Add seller notices on `commerce.order.cancelled` (seller from the order) and buyer notices on `late_capture_refunded` and `partially_refunded`.
- **Acceptance:** Consumer tests that each event creates exactly one inbox row for the right recipient.
- **Effort:** S

### COM-A-12

- **ID:** COM-A-12
- **Title:** The tax invoice prints the buyer's first address line as the buyer's name.
- **Severity:** P3
- **Category:** functional (invoices)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `inv.Buyer = invoice.Party{Name: shipTo.Line1, Address: shipTo}` (`S/shipments.go:788`), although the opened address carries `ContactName` (used for the courier at `S/shipments.go:145-149`).
- **How to reproduce:** Download any invoice: "Bill to" shows the street.
- **Impact:** Invoice content wrong for GST records and buyer reimbursement claims.
- **Fix:** Carry `ContactName` through `orderFulfilmentAddress` into `invoice.Address`/`Party.Name`.
- **Acceptance:** An invoice draft test asserting the buyer name equals the snapshot's contact name.
- **Effort:** S

### COM-A-13

- **ID:** COM-A-13
- **Title:** Razorpay orders are created without `payment_capture` and nothing ever captures an authorised payment, so payments complete only if the Razorpay account is set to auto-capture.
- **Severity:** P2
- **Category:** functional (payments), configuration
- **Evidence level:** SUSPECTED
- **Evidence:** `createOrder` body is amount, currency, receipt, notes, offers (`PAY/internal/gateway/razorpay_provider.go:128-145`); `Capture` exists (`:298-307`) with no non-test caller (search of `PAY/internal`, `PAY/cmd`); only `payment.captured` / `order.paid` set `succeeded` (`PAY/internal/service/p0.go:79-81`); the reconciler treats `authorized` as non-terminal (`razorpay_provider.go:511`, `service/p0.go:871`, `reconcileOutcome`). To confirm: the prod Razorpay account's capture setting, and one test-mode payment on qa showing `payment.captured` without manual capture.
- **How to reproduce:** With manual capture on the account, pay: the payment sits `authorized`, the order expires after 20 min, Razorpay auto-refunds days later.
- **Impact:** If misconfigured, every order fails silently while buyers see a debit.
- **Fix:** Send `"payment_capture": 1` on order creation (or a boot/readiness check that reads the account setting).
- **Acceptance:** A gateway test asserting the order body carries `payment_capture: 1`.
- **Effort:** S

### COM-A-14

- **ID:** COM-A-14
- **Title:** Order sub-resources answer 403 to a stranger and 404 only for a missing id, which confirms that another user's order id exists.
- **Severity:** P3
- **Category:** functional (tenant isolation)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `requireOrderRole` → 404 `ORDER_NOT_FOUND` vs 403 `FORBIDDEN` (`H/shipments.go:41-58`); same for `/orders/:id/payment` (map 2.1). Ids are random UUIDs, so practical impact is low; no data is returned.
- **How to reproduce:** Buyer B calls `GET /v1/commerce/orders/<A's id>/invoice`: 403; a random UUID: 404.
- **Impact:** Existence oracle only.
- **Fix:** Answer 404 `ORDER_NOT_FOUND` for "not associated".
- **Acceptance:** Handler test: stranger and missing id get the same status and body.
- **Effort:** S

## 5. Not verified

- No journey was executed: no qa or dev order, no Razorpay test payment, no Shiprocket sandbox booking or webhook, no notification delivery.
- Integration tests (`//go:build integration`: oversell proof, duplicate capture, refund settlement, expiry sweep, admin review) were not run; they need a scratch database (CI only).
- Android and web were not built and their unit tests not run; Android screens were followed only to the API layer, except checkout and the seller hub.
- Shiprocket's response to a ₹0 `sub_total` (COM-A-4) and the Razorpay account's capture mode (COM-A-13).
- Courier webhook handling past signature verification (`S/shipments.go:502-616`) was located, not followed line by line.
- Order expiry with an in-flight payment (`P/p0.go:1255-1353`, backoff 053) was not followed.
- Admin web console pages for MStore and Payments were not audited.
- Whether "landing is /shop" refers to the site root or to the shop entry.
- Android push deep links for order notices (`/shop/orders/{id}`).

## 6. Notes for other tracks

- **Security:** webhook replay with a fresh unsigned `X-Razorpay-Event-Id` (map Q7); `payment.succeeded` trusted from the shared topic with blank currency / nil payer allowed (`P/p0.go:215-229`); internal-key admin group with unverified actor and no commerce NetworkPolicy (map Q11); stub courier accepts any webhook and is not refused in prod (`courier.go:161`).
- **Money / data:** the rupee mirror columns are written as 0 by the P0 checkout but still read by the courier booking (COM-A-4), the seller dashboard (COM-A-5) and the payout calculator (`S/seller_account.go:176-184`, fenced). A sweep for every `.FinalPrice`/`.UnitPrice`/`.FinalAmount` read is in this report's search (`S/checkout_quote.go:389-408` legacy quote preview also reads them).
- **Ops:** gated migrations 998/999 likely unapplied (map Q1); prod courier provider chosen by a secret with no boot assertion; payments' refund workers off (COM-A-10); dev smoke stops at the quote.
- **Tax:** books-only invoices are a combined "Tax Invoice / Bill of Supply"; no e-invoice IRN; marketplace TCS (s.52) and GSTR-1 ownership open (map Q15).
- **Product decisions needed:** auto-booking at payment vs seller-driven shipping (COM-A-1); launch refund policy beyond cancel (COM-A-2); how sellers are paid at launch (map Q3).
