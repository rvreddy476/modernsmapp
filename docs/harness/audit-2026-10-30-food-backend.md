# Audit: food backend (food-service, its payments and gateway edges)

Branch `codex/module-01-02-launch-safety`. Read-only audit: no edits except this file, no git writes, no databases, no deploys.
Paths are relative to `Architecture/services/food-service/` unless they start with `Architecture/`, `deploy/` or `identity-platform/`.

Covered: customer ordering (cart, `POST /v1/food/orders`, server-side pricing, payment intent, confirm, cancel, refund requests, substitutions, order chat, support tickets), restaurant partner flows (menu writes, order status, pickup-code verify, settlements view), delivery flows inside food (offer dispatch and accept, batches, assignment steps, delivery-code verify, location pings, presence), the payment-event consumer (`internal/payments`, `store/postgres/payment_events.go`), refunds (system, admin, support), settlements (`internal/settlement`, `store/postgres/settlements_b3.go`), background workers in `cmd/server/main.go`, the boot-time schema (`database/setup.sql` via `store/postgres/bootstrap.go`), `deploy/services/food-service/values-prod.yaml`, and the api-gateway front (`Architecture/services/api-gateway/cmd/server/main.go:734,838-842,1068,1387`, `pkg/internalroutes/internalroutes.go:355`).

rider-service: food-service never calls it (only code comments name it in `internal/geo/geo.go:3` and `internal/digilocker/client.go:4`). Not audited further, per the "real caller" rule. payments-service is reached only through `internal/payments/client.go` (token client in production, deterministic `food_order:<order>` intent key, refund key `food_refund:<order>:<refund row>`).

Launch posture that changes some severities: prod values set `FOOD_COUPONS_ENABLED`, `FOOD_COD_ENABLED` and `FOOD_WALLET_PAYMENTS_ENABLED` to `"false"`, and the gateway keeps `/v1/food` closed until `FOOD_PUBLIC_ENABLED` is true. HPA allows 1 to 4 replicas.

`scripts/verify/go.sh --fast Architecture/services/food-service`:

```
PASS build          food-service  (3s)
SKIP vet/test       food-service  (no changed package)
RESULT PASS  (logs: /tmp/verify-logs/20261030T194630-11155)
```

---

## HIGH

### H1. Any partner can put their own menu item into another restaurant's public menu
- **Files:**
  - `internal/http/handler.go:1150` (`CreateMenuItem`) takes `category_id` from the body. `internal/store/postgres/ops.go:259-290` (`CreateMenuItem`) checks only that the caller owns `restaurantId` (`requireRestaurantOwner`), then inserts `restaurant_id = <caller's restaurant>, category_id = <any category>`. Nothing checks that the category belongs to that restaurant, and `menu_items.category_id` is a plain FK (`database/setup.sql:385`).
  - `internal/store/postgres/store.go:245-270` (`GetMenu`) builds a restaurant's public menu by joining that restaurant's categories to every item whose `category_id` points at them, with no `i.restaurant_id = c.restaurant_id` predicate. Category ids are public in every `GET /v1/food/restaurants/:id/menu` response.
  - `POST /v1/food/partner/restaurants` (`handler.go:111`, `ops.go:17`) is open to any authenticated user, so getting a restaurant id you own needs only an account.
  - `moderation_status` defaults to `approved` (`setup.sql:1330`), and `GetMenu` does not read it anyway.
- **Scenario:** A competing restaurant owner reads the category ids from a popular rival's public menu. They create items in their own (ACTIVE) restaurant with those category ids, with the rival's dish names, a lower price, or a name like "Kitchen closed - order from X", and an image of their choice. The items show up in the rival's menu. When a customer taps one, `AddCartItem` (`customer.go:65-100`) takes the restaurant from the item, so the cart and the order go to the attacker's restaurant, and payment and settlement go to the attacker. If the attacker's restaurant is not live, the item still shows on the rival's menu as defacement, and checkout fails for the customers who pick it.
- **Fix:** In `CreateMenuItem`, refuse unless `SELECT 1 FROM food.menu_categories WHERE id=$category AND restaurant_id=$restaurant` (in the same statement as the insert, or `INSERT ... SELECT ... WHERE EXISTS`). Add `AND i.restaurant_id = c.restaurant_id` to `GetMenu`, so rows that are already mis-linked stop showing. Add a guard test: owner A plus a category of restaurant B should give 404/422. Add a one-off check query for existing items whose `category_id` names a category of another restaurant.

---

## MEDIUM

### M1. A capture that lands on an order cancelled by admin is only logged, and an admin cancel of a paid order requests no refund
- **Files:**
  - `internal/orderstate/orderstate.go:82,88`: admins may cancel `PAYMENT_PENDING` and `PAYMENT_FAILED` orders ("ordinary ops work").
  - `internal/payments/events.go:144-147`: a `payment.succeeded` on any terminal status returns `OutcomeLateCapture` with `EffectNone`. `internal/payments/consumer.go:187-190` logs it, and the inbox row commits. No `food.refunds` row, no refund command, no alarm row. payments-service writes `refund_required` only when its own intent is `failed`, and here the intent is `succeeded`.
  - `internal/store/postgres/ops.go:1123-1158` (`AdminCancelOrder`): moves a paid order (`CONFIRMED` through `OUT_FOR_DELIVERY`) to `CANCELLED_BY_ADMIN` and stops. The customer-cancel and reject paths request the refund in the same transaction (`customer.go:756-789`, `ops.go:437-443`, `kitchen.go:150-153`), but this path does not.
- **Scenario:** (a) A customer's UPI collect request sits unpaid. Support cancels the order from the admin console. The customer approves the collect two minutes later, Razorpay captures, and food logs "LATE CAPTURE" and acknowledges the event. The customer has paid for an order nobody will cook, and nothing in the database queues the money back. (b) A rider is not found, and ops cancels a paid `DELIVERY_ASSIGNING` order. The money stays captured until someone separately calls `/admin/orders/:id/refund`.
- **Fix:** On `OutcomeLateCapture`, insert a `food.refunds` row (`PENDING`, full amount, reason `late_capture`) in the same transaction and let `ResubmitPendingSystemRefunds` submit it. This needs a `payment_status` path for `CANCELLED_BY_*` orders, for example `CAPTURED` then `REFUND_PENDING` without an order-status change. In `AdminCancelOrder`, call `requestSystemRefundFromTx` (add `CancelledByAdmin` to `systemRefundSources`) as customer cancel does.

### M2. Substitutions move no money
- **Files:** `internal/store/postgres/substitutions.go:42-102` stores a restaurant-supplied float `price_diff`. `:104-146` (`RespondToSubstitution`) only flips the status, and `internal/service/service.go:746-754` only emits an event. No refund, no extra charge, and no change to `order_items`, totals, invoice or settlement. Respond also has no order-status check.
- **Scenario:** A ₹450 biryani is out of stock after the order is paid. The restaurant proposes a ₹300 one with `price_diff: -150`, and the customer approves. The customer is still charged ₹450, the invoice still says biryani, and the restaurant's settlement still pays out net supply on ₹450. With a positive `price_diff`, the restaurant gives away the difference. A customer who declines has no path except the restaurant rejecting the whole order.
- **Fix:** Store `price_diff_paise` (bigint) and compute it server-side from the suggested item's menu price. On approval of a negative diff, create a partial `food.refunds` row through `adminRequestRefundTx` (system actor) in the same transaction. Refuse positive diffs, or require a new payment for them, and record the substitution on the order line so that invoice and settlement read it.

### M3. Order chat, support tickets and batch detail trust `admin` and `moderator` scopes from a consumer session; riders stay in the chat forever
- **Files:**
  - `internal/http/handler_order_chat.go:37,57`, `handler_support.go:80,110` and `handler_delivery_offers.go:70` treat `X-Scopes` containing `admin`, `superadmin` or `moderator` (`handler_support.go:243`) as admin. Every other food admin route is admin-service-token only (`admin_token.go:145-175`, "X-User-Id and X-Scopes ... are ignored").
  - identity mints ladder scopes into a consumer session whenever the admin has 2FA (`identity-platform/services/auth-service/internal/service/admin_session.go:150-153`). The gateway refuses admin sessions on non-admin paths but stamps `X-Scopes` for consumer sessions (`api-gateway/cmd/server/main.go:1066-1069`).
  - `internal/store/postgres/order_messages.go:84-104` (`OrderPartyMembership`) makes any rider ever set on the assignment a party, with no status window. `MarkMessageRead` (`:65-79`, service `order_chat.go:62-64`) checks nothing: any user can stamp any message id as read under any role.
- **Scenario:** A social-side moderator, who has no food permission in admin-service, opens `GET /v1/food/support/tickets/<id>` or `GET /v1/food/orders/<id>/messages` from the consumer app and reads complaints, addresses and phone numbers that customers typed. They can also post into the thread as `admin`. Separately, a rider who delivered an order weeks ago can keep reading and posting in that order's chat, which often holds the customer's phone number and gate codes.
- **Fix:** Drop the X-Scopes overrides. Admin reads go through the existing token family (`/v1/food/internal/admin/support/tickets`, which already has `PermTicketsAct`). Limit the rider's chat membership to `DropVisible(assignment, order)`, and make chat read-only for everyone once the order is terminal plus a grace period. In `MarkOrderMessageRead`, resolve the role server-side and require party membership of the message's order.

### M4. Every pod boot takes ACCESS EXCLUSIVE locks on the hot tables, in one implicit transaction, with no lock_timeout
- **Files:** `internal/store/postgres/bootstrap.go:35-47` sends all of `database/setup.sql` (2,010 lines) in one simple-protocol `Exec`, which PostgreSQL runs as one implicit transaction. On every boot the script runs `DROP TRIGGER IF EXISTS ... ; CREATE TRIGGER` on `food.orders` (`:709`), `food.payments` (`:784`), `food.delivery_assignments` (`:835`), `food.restaurants` (`:248`), `food.menu_items` (`:410`) and more (each DROP TRIGGER takes AccessExclusiveLock). It also runs `ALTER TABLE food.restaurants DROP/ADD CONSTRAINT` twice (`:1712-1722`, validated with a full scan), `DROP/ADD` on `delivery_partner_documents` (`:1917`), and `UPDATE food.orders ... WHERE final_amount_paise IS NULL` (`:1792`, a sequential scan). Only the outbox block sets `lock_timeout` (`:1205-1218`). All of these locks are held until the whole script commits.
- **Scenario:** At lunch peak the HPA (`values-prod.yaml`, max 4, CPU 65%) adds a pod while an admin revenue or settlement report holds a long read on `food.orders`. The new pod's `DROP TRIGGER ... ON food.orders` queues behind that read, and every order read and write (placement, payment events, kitchen queue, rider steps) queues behind the DROP. Ordering stalls for as long as the report runs, and then for the rest of the script.
- **Fix:** Guard each trigger and constraint with an existence check (`IF NOT EXISTS (SELECT 1 FROM pg_trigger WHERE tgname=...)`), as the outbox block and `ck_food_order_paise_match` (`:1805`) already do. Set `SET lock_timeout = '5s'` at the top of the script, so a blocked boot fails and retries instead of stalling traffic. Move the one-off backfill `UPDATE`s behind a guard that checks for remaining NULLs.

### M5. A customer can cancel after the kitchen has started, with a full refund the restaurant never sees
- **Files:** `internal/orderstate/orderstate.go:92,97` allow `CONFIRMED` and `PREPARING` to `CANCELLED_BY_CUSTOMER`. `customer.go:756-789` then requests a refund of the full remaining amount (`system_refunds.go:70-99`). Settlement counts only delivered orders (`settlements_b3.go:111-140`), so the restaurant gets nothing for the food it made.
- **Scenario:** A customer orders, watches the order move to "Preparing" 15 minutes later, and cancels because the ETA slipped (or as a repeated prank). They get a full refund, and the restaurant absorbs the cost of the cooked food. Nothing limits how often one user can do this.
- **Fix:** Decide the policy (product and finance). The minimal version: allow customer cancel only from `CONFIRMED` (before the kitchen accepts), and route a later cancel to a support refund request (`POST /refunds`), where an admin decides the amount. If cancel from `PREPARING` stays, refund `final - restaurant net supply`, and add the restaurant's share as a settlement line.

### M6. An approved rider can rewrite their verified identity without review
- **Files:** `internal/store/postgres/ops.go:454-509` (`UpsertDeliveryPartner`, `POST/PATCH /v1/food/delivery/profile`): `ON CONFLICT (user_id) DO UPDATE SET full_name, phone, email, vehicle_type, vehicle_number, city` with no status condition and no reset to `PENDING_REVIEW`. Rider KYC (`internal/riderkyc`, `store/postgres/delivery_kyc.go`) never compares names.
- **Scenario:** A rider passes review as "R. Kumar, KA01AB1234". They then hand the account to someone else and PATCH the name, phone and vehicle number. Customers see, and call, a person and vehicle that ops never reviewed, and the payout account stays bound to the original holder.
- **Fix:** For `status IN ('APPROVED','ACTIVE','OFFLINE')`, refuse changes to `full_name`, `vehicle_number` and `vehicle_type` (or move the partner to `PENDING_REVIEW` and revoke the role intent). Re-verify `phone` with an OTP through identity.

---

## LOW

### L1. COD paths have no fences (behind `FOOD_COD_ENABLED`, off in prod)
- `payment_writers.go:187-193,243-252`: COD may be chosen from `PAYMENT_PENDING` after an online intent was opened upstream. The intent is not cancelled, and `food.payments.provider_payment_id` keeps the intent id. If the customer then completes the Razorpay payment, `decideSucceeded` sees a `CONFIRMED` order and records `already_paid` (`events.go:141-143`). The customer pays online and again in cash, and no refund is recorded.
- `customer.go:574-600` inserts COD orders as `CONFIRMED` without `accept_deadline_at`, so the SLA worker (`kitchen.go:110-117`) never auto-rejects them.
- **Fix:** Before the COD flag is turned on, refuse COD once `provider_payment_id` is set, and set `accept_deadline_at` in the COD insert.

### L2. Coupon fences are incomplete (behind `FOOD_COUPONS_ENABLED`, off in prod)
`per_user_usage_limit` (`setup.sql:629`) is never read in Go, so one user can redeem a "once per user" code on every order. `used_count` and `coupon_redemptions` (`customer.go:667-683`) are never released on cancel, reject or refund. Discounts are computed in float rupees (`customer.go:985-1021`). **Fix:** count `coupon_redemptions` per user under the coupon row lock, release the redemption in the cancel and reject transactions, and do the math in paise.

### L3. Rider location has no plausibility checks, and dispatch scans every online rider
`tracking_payments.go:141-190` accepts a ping from any partner row, including `PENDING_REVIEW` and `SUSPENDED` ones, with no speed or jump check. Each ping inserts a row, with no server-side rate limit beyond the gateway's per-user one. A rider can teleport next to a busy restaurant to be "nearest" in `rankCandidates`. `dispatch.go:50-71` runs a lateral latest-location lookup plus two offer subqueries for every ACTIVE online partner inside the bounding box, once per pending group (up to 50 per tick), every 2 s, on every replica. **Fix:** refuse pings from non-ACTIVE partners, drop pings implying more than about 150 km/h since the last one, and keep the latest point on `delivery_partners.current_latitude/longitude` (already written at `:171-174`), with an index used by dispatch in place of the lateral lookup.

### L4. A rider who toggles offline mid-job cannot finish it, and a silent rider's job is never re-dispatched
`ops.go:533-561` sets `status = 'OFFLINE'` on any offline toggle. `DeliveryUpdateAssignment` (`ops.go:600`) and `RiderVerifyDeliveryCode` (`delivery_proof.go:198-203`) require `ACTIVE`, so the rider standing at the door is refused until they go online again. The presence worker deliberately keeps `ACTIVE` for a rider with a job (`rider_presence.go:17-48`), but the manual toggle does not. A rider who goes silent while `ASSIGNED` or `ACCEPTED` keeps the order indefinitely, and only the admin recovery view surfaces it. **Fix:** mirror the presence worker's `on_job` rule in the toggle, and release `ASSIGNED`/`ACCEPTED` jobs whose rider has been silent for N minutes back to `DELIVERY_ASSIGNING`.

### L5. Generic idempotency keys are not bound to the request
`lockGenericIdempotency` (`customer.go:1090-1128`) stores no request hash. The restaurant status route (`ops.go:376-393`), rider steps (`ops.go:608-618`) and admin refunds share `food.idempotency_keys(user_id, key)`. A client that reuses a key for a different order or step gets the other request's state back as success, and the action never runs. **Fix:** store a hash of (route, order or assignment id, target status) as `PlaceOrder` does (`checkoutRequestHash`), and return `ErrIdempotencyPayloadMismatch` on a mismatch.

### L6. Minor
- Order chat and ticket message bodies have no length cap (`handler_order_chat.go:33`). Only the gateway's 10 MB body limit applies.
- The fraud worker runs a full pass at boot on every replica (`fraud_worker.go:28`), with one `RecordFraudScore` write per user.
- `CreateMenuItem` and `UpdateMenuItem` accept any `image_url` string when no media id is given (`ops.go:271-281`).

---

## Checked and sound
- **Server-side pricing:** item, variant and add-on prices are re-read from the menu under `FOR UPDATE`/`FOR SHARE` in the checkout transaction (`checkout_validation.go:29-130`, `customer.go:892-964`). Item ownership, availability, variant-to-item and required add-on groups are validated. Packaging, platform fee, delivery fee and GST go through `pricing.Price` in integer paise, and the NUMERIC columns are derived from paise in SQL with a `ck_food_order_paise_match` CHECK. The client sends no amount: `PlaceOrder` takes only an address, method, coupon and note. The intent amount is read from the database (`service/payments.go:85-95`), and the confirm call ignores the client's `amount_minor`. Minimum order, restaurant ACTIVE, open and accepting, hours, and radius are checked inside the transaction (`serviceability.go:258-281`). There are no tips or surge in code.
- **Checkout idempotency:** a user and key row plus a request hash, `ErrIdempotencyPayloadMismatch` on a different body, and an in-progress refusal. The payments intent key is deterministic per order (`client.go:191`), so a retry or a second tap cannot open a second intent.
- **Payment events:** inbox row and effect in one transaction (`payment_events.go:28-56`), order `FOR UPDATE`, `CheckCapture` with `RequireStated` (amount, currency, payer, intent). `moneyTaken` blocks a double confirm. `payment.failed` after a capture is ignored. Refunds go through `CheckRefund`, are deduped by provider refund id, and settle the matching row by amount and command id. Non-`food_order` references are ignored, a missing event id goes to the DLQ, and `RetryForever` applies to transient errors only.
- **Refunds:** system, admin and support refunds are durable `food.refunds` rows with remaining-balance caps computed under the order lock, a deterministic payments key (`food_refund:<order>:<row>`), and a backoff resubmitter. `REFUNDED` is written only by the event. Support approval re-checks ownership and that no refund is in flight.
- **Settlements:** restaurant payout = net supply - commission - commission GST + supplier GST - TCS - refund share, in big-int paise (`settlement/restaurant.go`). Platform fee, delivery fee and s.9(5) GST are excluded. Only delivered orders count. Paid periods are immutable, with carry-forward. Rider payout is the sum of stored per-assignment payouts.
- **Order state machine:** one transition table with actors (`orderstate.go`), and a compare-and-set writer (`order_transition.go:75-89`) that closes assignments and supersedes offers on cancel. Cancel vs accept vs payment races serialise on the order row. Restaurant buttons are limited to `CONFIRMED→PREPARING|REJECTED` and `PREPARING→READY`. The SLA auto-reject uses `FOR UPDATE SKIP LOCKED` and requests the refund in the same transaction.
- **Rider dispatch:** offer creation locks the order or batch, refuses a second live offer, locks the partner and caps pending offers (`sequential_delivery.go:21-121`). Accept locks the order before the offer, rechecks expiry after the locks, transitions only from `DELIVERY_ASSIGNING`, and claims the assignment only if unheld (`delivery_offers.go:137-245`). Batches lock members in sorted order. Every worker is safe across replicas.
- **Codes:** 4-digit pickup and delivery codes from `crypto/rand`, constant-time compare, 5-attempt caps counted and committed before refusal, codes cleared on release, the customer route retired (410), and `payloadSafe` refusing any realtime frame that carries a code.
- **Privacy windows:** rider location, drop address and pickup code are shown only inside the `delivery_visibility.go` windows. The rider view never reads the customer phone (`rider_navigation.go:295-300`). The restaurant order view carries no address, and `PartnerOrderOf` strips the delivery code. Order events carry ids only.
- **AuthZ (outside H1, M3):** customer order, cart, address, payment, cancel, refund-request and substitution queries are scoped by `user_id`. Partner order, menu, variant, add-on, pickup-verify and settlement queries are scoped by `owner_user_id`. Rider steps, verify and proof are scoped by the partner row. Admin routes are admin-service-token only, with per-route permissions.
- **Edge:** the gateway strips client `X-User-Id`, `X-Scopes` and `X-Admin-Role` (`main.go:838-842`), stamps the key for `/v1/food` (`internalroutes.go:355`), food requires it on the whole `/v1/food` group (`handler.go:50-52`), and `/v1/food` is behind `FOOD_PUBLIC_ENABLED`.
- **Outbound:** payments through `shared/paymentsclient` (token auth in prod, legacy key refused outside dev, `client.go:108-136`). Google Routes has a per-call timeout (`routing/google.go:126`). DigiLocker and the monetization client have 8 s timeouts. Realtime publishing is best-effort after commit.
- **Lists:** customer orders 50, partner orders 100, kitchen queue 200, rider assignments 50 and history 100, settlements 100, and admin lists paginated.
- **Migrations:** paise CHECK added once with a backfill first, the outbox column add guarded with `lock_timeout 5s`, and the bootstrap serialised across replicas by an advisory lock. M4 covers the unguarded statements.
