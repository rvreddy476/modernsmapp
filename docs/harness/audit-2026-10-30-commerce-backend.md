# Audit: commerce backend (commerce-service, payments-service)

Branch `codex/module-01-02-launch-safety`. Read-only audit: no edits except this file, no git writes, no databases, no deploys.
Paths are relative to `Architecture/services/` unless they start with `Architecture/shared/` or `deploy/`.

Covered: commerce-service P0 loop (quote, `POST /v2/orders/checkout`, payment intent open/retry, payment-event consumer, cancel, durable refund commands, reservation expiry, coupons, shipments/invoices, delivery-ops ownership, addresses, seller variant/stock writes, reviews, route fence), payments-service (route families and credentials, Razorpay webhook verify + apply, intent create idempotency, refund commands, late-capture handling, refund settlement and its outbox event), `Architecture/shared/outbox` and `shared/paymentevents` as used by both, api-gateway routes fronting them (`api-gateway/cmd/server/main.go:684-749`, `pkg/routepolicy/routepolicy.go`, `pkg/internalroutes/internalroutes.go:366-377`), and `.github/workflows/integration-commerce.yml` (money-type gate).

wallet-service: no call from commerce-service or payments-service exists (only comments in `wallet-service/internal/http/handler_internal.go:12` and `service/send.go:306` name Commerce). Not audited further, per the "real caller" rule.

`scripts/verify/go.sh --fast` per module:

```
PASS build          commerce-service  (6s)
SKIP vet/test       commerce-service  (no changed package)
RESULT PASS  (logs: /tmp/verify-logs/20261030T181528-9082)
PASS build          payments-service  (3s)
SKIP vet/test       payments-service  (no changed package)
RESULT PASS  (logs: /tmp/verify-logs/20261030T181535-9409)
```

---

## HIGH

### H1. In production, any service holding the platform-wide internal key can refund any payment of any domain
- **Files:**
  - `payments-service/cmd/server/main.go:279-291`: when `INTERNAL_SERVICE_KEY` is set, it is installed as a legacy credential on `/v1/payments/internal/*` even when `SERVICE_CALLERS` is configured ("legacy internal-key callers admitted on /internal alongside service tokens"). `deploy/services/payments-service/values-prod.yaml:54` mounts it, and it is the same `internal_service_key` secret referenced by about 158 deploy files across the platform.
  - `payments-service/internal/http/handler.go:287-312` (`requireServiceCredential`): a request with no token and the right `X-Internal-Service-Key` is marked `legacy_internal_caller`. `requireOp` then passes it with no check.
  - `handler.go:822-827` (`ownsIntent`): returns true for any legacy caller.
  - `handler.go:908-923` (`InitiateRefund`): for a legacy caller it sets `CallerDomain = intent.OwnerDomain`. The owner-domain check in `store/postgres/p0.go` `CreateRefundCommand` then compares the intent's owner with itself, which always passes. An `amount_minor` of 0 becomes "everything left", and the idempotency key is derived for the caller. `application_id` comes from the body, and the store only requires it to equal the intent's (a guessable constant such as the commerce application key).
  - Only the refund-admin and registry-write routes refuse the legacy key in production (`refuseLegacyKeyInProduction`, `handler.go:233-240`).
- **Scenario:** An attacker gets code execution or SSRF with header control in any of the dozens of pods that hold `INTERNAL_SERVICE_KEY` (feed, media, chat, and others). They POST `/v1/payments/internal/intents/<id>/refund` with `{"application_id":"<commerce app>"}`. Intent ids are visible to commerce clients (`payment_intent_id` in the checkout and intent responses). Payments queues a full refund to the payer and pays it out through Razorpay, while commerce still shows the order as paid and ships it. The buyer can be the attacker: they buy, receive the goods, and refund themselves. The same key also opens `POST /v1/payments/intents` (user-facing family) for any payer.
- **Fix:** In production, refuse the legacy key on `POST /internal/intents/:id/refund` and `POST /internal/intents` (reuse `refuseLegacyKeyInProduction`, as the admin routes already do). Move the remaining legacy callers to service tokens first (the deploy comments list rider, dating, doorstep and monetization). Add a guard test showing that a legacy-key refund with ENV=prod gets 401/403. Longer term, give payments its own key instead of the shared one.

---

## MEDIUM

### M1. A second, distinct capture on an already-succeeded intent is acknowledged and never refunded
- **Files:** `payments-service/internal/store/postgres/p0.go:265-298`. `transitionAllowed("succeeded","succeeded")` is false (`:466-480`), so the event's inbox row commits and nothing else happens. The only branch that records money owed back is `intent.Status == "failed"` (`:278`, `recordLateCaptureTx`).
- **Scenario:** A buyer on a slow UPI collect taps pay twice, or a late authorisation arrives on the same Razorpay order. Razorpay captures payment `pay_A` (intent goes to succeeded, commerce marks the order paid) and later `pay_B` on the same provider order. The `payment.captured` for `pay_B` hits this branch: 200 OK, no `refund_required` row, no alarm. The buyer has paid twice, and nothing in the system records the second payment except the provider inbox row.
- **Fix:** In the `!transitionAllowed` branch, when `e.NewStatus == "succeeded"`, the intent status is in (`succeeded`, `partially_refunded`, `refunded`) and `e.ProviderPaymentID` is non-blank and differs from the intent's `provider_payment_id`, write a `refund_required` row with reason `duplicate_capture` (same dedupe key as `recordLateCaptureTx`). Do not do this for `order.paid`, which repeats the same payment id.

### M2. Commerce settles refunds without checking the reference type, the intent, the amount, partial status or duplicates
- **Files:**
  - `commerce-service/internal/consumers/payments_p0.go:275-291` (`applyRefund`): the only filter is "reference_id parses as a UUID". It ignores `reference_type`, although payments now sends it (`payments-service/internal/store/postgres/p0.go:1086-1105`), and it ignores `status`. Every other domain verifies with `paymentevents.CheckRefund` (food, dating, rider, doorstep, monetization). Commerce does not call it.
  - `commerce-service/internal/store/postgres/p0.go:696-721` (`SettleRefund`): marks every unsettled refund command of the order `succeeded`, sets `payment_status = 'refunded'` unconditionally (outside the `paymentStatusTransitions` table), and enqueues `commerce.order.refunded`. It has no inbox, so each redelivery writes another event.
- **Scenarios:** (a) An ops partial refund from the Razorpay dashboard (₹200 of ₹2,000) arrives as `payment.refunded` with `status: partially_refunded`. Commerce marks the order fully `refunded` and closes any pending cancel refund command as succeeded, so the remaining ₹1,800 owed on a later cancel is never sent. (b) Every food, dating, doorstep, rider and membership refund (all UUID references) runs `SettleRefund` on a non-existent order id and emits a `commerce.order.refunded` event. (c) Duplicate Kafka delivery, which is routine (see M4), sends the buyer a second "refunded" notification.
- **Fix:** In `applyRefund`, return early unless `p.ReferenceType == "order"`. Load the order and run `paymentevents.CheckRefund` against `final_amount_minor` and the bound intent, as food-service does. Settle only the command whose amount and intent match, set `partially_refunded` versus `refunded` from the cumulative amount, and claim a `payment_event_inbox` row (the `applyPaymentOnce` helper) so a redelivery is a no-op.

### M3. Coupon capacity and per-buyer use are never released when an order dies unpaid
- **Files:** `commerce-service/internal/store/postgres/checkout.go:480` claims capacity (`uses_count + 1`) and `:641` inserts `coupon_usages`. No code path decrements or deletes them: `expireOne` (`p0.go:785-819`), `ApplyPaymentFailed` (`p0.go:279-327`), `CancelOrder` (`p0.go:457-578`). The search for `uses_count -` or `DELETE FROM coupon_usages` finds nothing. Per-buyer use is counted from `coupon_usages` (`coupons.go:306`).
- **Scenario:** A seller runs a public `LAUNCH50` coupon with `max_uses = 100` and `max_uses_per_user = 1`. A rival opens 100 checkouts across throwaway accounts and never pays. Within 20 minutes the orders expire, but the coupon reads `COUPON_EXHAUSTED` for every real buyer. More commonly, an honest buyer's UPI app times out, the order expires, and their one use is burned, so the re-checkout is refused.
- **Fix:** In the same transactions that terminate an unpaid order (expiry, payment_failed with no retry, cancel before payment), run `DELETE FROM coupon_usages WHERE order_id=$1 RETURNING coupon_id` and then `UPDATE coupons SET uses_count = uses_count - 1 WHERE id = $2`. `RetryPaymentReservation` must re-claim the coupon, or re-check it and refuse.

### M4. Outbox publisher: every replica publishes the same rows, and partition keys are ignored
- **Files:** `Architecture/shared/outbox/publisher.go`
  - `:124-131`: `SELECT ... WHERE published_at IS NULL ORDER BY id LIMIT $1` with no `FOR UPDATE SKIP LOCKED` or lease, and `:181` marks the rows afterwards. commerce and payments both run with `replicaCount: 2` (`deploy/services/*/values-prod.yaml`), polling every 500 ms against a synchronous writer whose `BatchTimeout` is 1 s by default.
  - `:93`: `Balancer: &kafka.LeastBytes{}`. In kafka-go, LeastBytes ignores `Message.Key`, so the `partition_key` that both services carefully set (order id, intent id) has no effect.
- **Scenario:** Every `payment.succeeded`, `commerce.order.paid`, `commerce.order.cancelled` and so on is published twice. The payment consumer survives this because of the inbox and fixed envelope ids, but `commerce.order.refunded` (M2) and notification-service consumers do not, so buyers get duplicate pushes. Because of LeastBytes, `commerce.order.paid` and `commerce.order.cancelled` for one order can land on different partitions and be consumed out of order, so a notification says "paid" after "cancelled". `payment.succeeded` and `payment.refunded` are keyed differently anyway.
- **Fix:** Claim rows with `UPDATE ... SET claimed_until = now()+30s WHERE id IN (SELECT id ... AND (claimed_until IS NULL OR claimed_until < now()) ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1) RETURNING ...`. Use `Balancer: &kafka.Hash{}` (or `CRC32Balancer`) so keys are honoured. Make the payments refund event key the reference id, as succeeded already is.

### M5. Shipments, invoices and courier contact read the live, editable address row, not the order's snapshot
- **Files:**
  - `commerce-service/internal/service/shipments.go:133-152` (courier drop address), `:736-745` (invoice ship-to), `service/delivery_operations.go:197-205` (partner contact) and `service/service.go:2858` (return pickup) all call `GetAddressRow(order.DeliveryAddressID)`.
  - `store/postgres/store.go:1749-1772` (`UpdateAddress`) lets the owner rewrite every field of a row that paid orders reference.
  - Checkout binds the quote to the address content and stores a sealed snapshot (`checkout.go:368-381`, `delivery_address_snapshot_enc`), which the order detail already reads (`service.go:1436`), but fulfilment does not.
- **Scenario:** A buyer pays for an order to a Karnataka address (intra-state, CGST+SGST, Bengaluru shipping rate). Before the seller books, they edit that saved address to a remote Assam pincode. The courier is booked to Assam at the quoted Bengaluru rate, and the platform absorbs the difference. That is the loss B7 was written to stop, now reached after checkout instead of before it. The GST invoice shows an Assam ship-to while `place_of_supply_state` says Karnataka with intra-state tax, which makes it a non-compliant invoice.
- **Fix:** Add `openOrderSnapshot(order)`, which opens `delivery_address_snapshot_enc` with `pii.ScopeOrderSnapshot` (already done at `service.go:1436`). Use it in all four places, and fall back to the live row only for `address_snapshot_provenance = 'legacy_unverified'` orders.

---

## LOW

### L1. The money-type CI gate does not check two of the fences that its exemptions cite
`commerce-service/internal/service/service.go:2577` (`money-exempt: seller earnings, fenced at /v1/commerce/seller/earnings`) and `:2929` (`... fenced at /v1/commerce/seller/cod-remittances`) rely on `FencedPrefixes` entries (`internal/http/handler_p0.go:50-51`). The "Money-gate exemptions are still fenced" step in `.github/workflows/integration-commerce.yml` checks only `/orders/checkout`, `/returns` and `/payout`. If a later edit drops either prefix, the float paths become reachable and CI stays green. **Fix:** add the two greps beside the existing three. Better, have the step extract every path cited after `fenced at` and require it in `handler_p0.go`.

### L2. A seller can never respond to a review (seller id compared with user id)
`commerce-service/internal/service/service.go:3201` compares `r.SellerID` (a `sellers.id`) with `actorID`, which is `X-User-Id` (`internal/http/handler.go:2007`). The route always answers 403. It fails closed, so this is not a security issue. **Fix:** resolve the seller with `sellerForCaller` and compare `seller.ID`.

### L3. Delivery handlers keep running after a missing `X-User-Id`
`commerce-service/internal/http/handler_delivery_operations.go:42,58,63,...,200` use `u, _ := getUserID(c)`. `getUserID` writes a 401 and the handler goes on with `uuid.Nil`, which writes a second response. Store predicates filter on the user, so no data leaks. **Fix:** `if !ok { return }`.

### L4. The payments webhook reads an unbounded body
`payments-service/internal/http/handler.go:1058` calls `io.ReadAll(c.Request.Body)` before signature verification, on a public ingress that does not go through the gateway's 10 MB cap. **Fix:** wrap the body in `http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)`.

### L5. A customer cannot delete an address used by any order
`orders.delivery_address_id REFERENCES customer_addresses(id)` has no `ON DELETE` clause (`commerce-service/database/setup.sql:447`). `DeleteAddress` (`store.go:1782`) then fails with a raw FK error, which surfaces as a generic error. Once M5 is fixed, this can become a soft delete or `ON DELETE SET NULL`.

---

## Checked and sound
- **Server-side pricing:** checkout re-prices every line in paise under `FOR UPDATE OF v, p` (`checkout.go:859-970`). It refuses unless `computed.Total == expected_total_minor`, and that field is mandatory and positive at the edge, in the service and in the store. It checks product, variant, seller and approval status, applies the single-seller rule, and refuses a missing tax class rather than zero-rating it. The quote is bound to cart version, items hash, coupon, payment method, address content hash, row fingerprint and pincode. The client never names an amount to payments (`OpenPaymentIntent`, `openPaymentIntent`).
- **Checkout idempotency:** `pg_advisory_xact_lock(user:key)`, then lookup with a fingerprint conflict check (`checkout.go:256-285`). Payment intents use a deterministic `order:<id>[:attempt:n]` key, and payments checks the key's owner, reference, parties, amount, currency, method and app on conflict (`payments-service/internal/store/postgres/store.go:118-240`).
- **Oversell:** stock rows are locked in sorted order, backed by `CHECK (reserved_qty <= total_qty)` (validated in gated 999). Commit and release are in the payment, expiry and cancel transactions, with no clamping. Seller `AdjustStock` locks the row and checks ownership.
- **Coupon over-redemption:** a conditional `UPDATE ... uses_count < max_uses` in checkout plus a CHECK. Seller coupons apply only to that seller's bag, and an unknown scope refuses. Flat discounts are capped at the base.
- **Payment events in commerce:** inbox row and effect in one transaction. Amount, currency, payer and bound intent are checked before marking paid. A capture on an expired, cancelled or failed order becomes a durable refund command. `RetryForever` applies only to transient errors, and poison goes to the DLQ.
- **payments-service webhook:** HMAC over the raw body, fails closed without a secret. The event-id header is not signed, but a replay with a fresh id changes nothing: terminal transitions refuse and refunds dedupe on the provider refund id. Amount and currency are verified inside the transaction, ambiguous provider orders are refused, `payment.failed` no longer finalises an intent, and a late capture on a failed intent is recorded in `refund_required`.
- **Refunds:** commerce refund commands are durable, with a deterministic `cancel:<order>` key and a SKIP LOCKED claim. payments caps `refunded + reserved + amount <= amount`, checks the token caller's owner domain and the application, and allows only `succeeded` or `partially_refunded` intents.
- **Order state machine:** paid, failed, expire, cancel and retry each lock the order row. Cancel checks ownership for customers and runs the D6 matrix in Go and in the trigger. Shipment booking requires `payment_status = paid`.
- **AuthZ:** order reads and cancel are customer-only. Seller order, fulfilment and history go through `sellerForCaller` and requireWholeOrder. Variant, stock and price-tier writes check product ownership. Address update, delete and default are scoped by `user_id`. Delivery job, contact, code and location are partner- or customer-scoped in SQL. Reviews require the buyer's delivered item. Shipment and invoice require a party to the order (single-seller orders).
- **Edge:** the gateway never routes `/v1/payments` (routepolicy prefix and target guard). It refuses every `internal` segment, and commerce requires the gateway key on the whole engine. The legacy `/orders/checkout`, returns and payout families are unregistered and fenced, and the stub confirm route is dev-only.
- **Provider HTTP:** Razorpay and Cashfree clients have 30 s timeouts. Refunds send `X-Refund-Idempotency` with the persisted key. The commerce-to-payments client uses `paymentsclient.DefaultTimeout`.
- **Migrations:** `order_refund_commands.amount_minor > 0`, unique idempotency keys on refund commands, intents and orders, the inbox event-id non-blank CHECK, and constraints added `NOT VALID` then validated in gated migrations (`SHARE UPDATE EXCLUSIVE`).
