# Commerce verification v1: money and orders

Independent verifier, 2026-11-17. Read-only, against HEAD (`git show HEAD:<path>` for files that are modified in the working tree). For each finding I read only its Title and Evidence, then tried to disprove it from the code and the HEAD deploy values.

Path shorthand, as in the tracks: `C` = `Architecture/services/commerce-service`, `S` = `C/internal/service`, `P` = `C/internal/store/postgres`, `H` = `C/internal/http`, `PAY` = `Architecture/services/payments-service`, `DV` = `deploy/services`.

Tests run: `go test ./internal/config/ -run TestResolveRefundRequiredWorker -count=1` in PAY passed. Its case "prod, unset" expects the flag off. `go test ./internal/courier/` in C also passed.

## What I checked to try to disprove the findings

- **Launch switches.** I searched every `os.Getenv` in `C/cmd` and `C/internal`. There is no checkout, pilot or shop kill switch. The only related flags are `COMMERCE_LOCAL_DELIVERY_ENABLED` and `PAYMENTS_ALLOW_STUB`. HEAD `DV/commerce-service/values-prod.yaml:10-15,79-85` keeps "the shop must not take orders" before Shiprocket only as a comment. Nothing in the code enforces it.
- **Payments flags.** The two payments flags appear in no values file, chart or secret mapping at HEAD (`git grep` finds only a SQL comment). Payments `ENV` is `prod` in qa (`values-qa.yaml:108`), `prod` in prod (`:97`) and `staging` in staging (`:103`).
- **Other consumers.** Nothing outside commerce consumes `commerce.order.paid` or `delivered` except notification-service. There is no payout or settlement in payments or monetization.

## Findings

### COM-A-1: CONFIRMED, P0 (VERIFIED-READ)

**Evidence I checked**
- The payment confirmation inserts `fulfill_paid_order` inside the paid transaction (`P/p0.go:339-345`).
- The worker is always started (`C/cmd/server/main.go:425-426`) and polls every second (`C/internal/workers/fulfillment.go:46`).
- `FulfillPaidOrderJob` books whenever `s.courier != nil` (`S/jobs.go:43-56`). The courier is always set (`main.go:138`), and anything other than `shiprocket` gets the stub (`C/internal/courier/courier.go:125-135`).
- `CreateShipmentsForOrder` gates only on `payment_status=paid`, with no seller-packed check (`S/shipments.go:113-122`). It stamps `ShippedAt: now` and then calls `MarkOrderShipped` as `system` (`:286`, `:350-357`).
- Migration 033 sits in the boot set and admits `confirmed→shipped` for `system`. `MarkOrderShipped` walks the matrix (`P/order_fulfilment.go:232-262`).
- The matrix has `shipped→cancelled` for `admin` only (`010_gst_and_state_machine.sql:113`). No service code transitions as `admin`.
- Pack and cancel apply only from `confirmed` or `packed` (`S/fulfilment.go:127-161`).
- The worker passes no manual courier, so `SellerManaged=false` (`S/fulfilment.go:62-70`). A later seller "ship" reuses the existing shipment (`S/shipments.go:182-186`).
- The seller cannot mark delivered without `SellerManaged` (`S/seller_delivery.go:92-108`).
- The stub's `ParseWebhook` returns nothing (`courier.go:156-158`). The stub auto-deliver timer is refused in managed environments (`main.go:409-420`).
- A local-delivery escape exists (`S/shipments.go:187-196`), but `COMMERCE_LOCAL_DELIVERY_ENABLED` is not set in prod or qa.

**Correction.** Under real Shiprocket, webhooks can still deliver, so "never delivered" holds only under the stub. The stub is what qa runs and what prod falls back to (the prod comment calls this "manual shipping").

**Severity: P0.** It is a core-journey failure. Buyers lose the right to cancel within about one second, sellers cannot fulfil, and under the stub courier that the prod values intend for manual shipping, every paid order stays in `shipped` forever. No switch fences it. The pilot scope does not reduce it: it hits every order.

**Related:** COM-E-4 (the configuration that causes the stub case), COM-C-8 (the cancel/booking race), COM-A-2.

### COM-C-1 / COM-E-2: CONFIRMED, P0 (VERIFIED-RUN for the flag defaults, VERIFIED-READ for the rest)

**Evidence I checked**
- `devDefaultFlag` turns the flags on only for `""`, `dev` or `local` (HEAD `PAY/internal/config/config.go:143-152,172-190`). The test above passed.
- The worker and consumer start only behind their flags (HEAD `PAY/cmd/server/main.go:341-355`).
- Payments records a capture on a `failed` intent and a second distinct capture as owed (`P…/store/postgres/p0.go:270-301`), and only logs "returns it when enabled" (`:314-328`). These captures are never forwarded to commerce.
- On the commerce side, a duplicate capture writes only an outbox event for payments to refund (`P/p0.go:972-1007`).
- HEAD has no `refund_required` operator route; only `refund_commands` are listed (`admin_routes.go:4-8`, `admin_token.go:206-208`). The `/refund-required/:id/retry` route exists only in uncommitted work.
- No metric reads `refund_required` (git grep of HEAD obs). The chart sets `prometheusRule.enabled: false`.

**Partial disproof.** A late capture on an order that commerce already expired or cancelled *is* refunded. Commerce writes an `order_refund_commands` row (`P/p0.go:265-290`), and the always-on refund worker sends it (`PAY/cmd/server/main.go:336`). The unrefunded cases are the payments-side failed-intent capture (the reconciler's 15-minute window), the duplicate capture and the amount/currency mismatch.

**Severity: P0** under "take money incorrectly". The buyer is charged with no goods and no refund, and there is no operator path or alert in qa or prod. The fix is two env lines. Neither a switch nor the pilot scope reduces it.

**Merge:** COM-E-2 into COM-C-1.

### COM-A-2: CONFIRMED, P1 (VERIFIED-READ)

**Evidence I checked**
- The `/returns` route families are fenced (`H/handler_p0.go:52-58`).
- `delivered→refund_pending` and `shipped→cancelled` are admin-only (`010:113,117`), and no service code acts as `admin`.
- The only refund primitive is the service-family `POST /v1/payments/internal/intents/:id/refund`, callable only by commerce or food with a service token (HEAD `PAY/internal/http/handler.go:240`, prod `SERVICE_CALLERS: 'commerce-service,food-service'`). It is not reachable by a human.
- I did not find a partial-refund or dispute writer in commerce. `partially_refunded` is read-only status mapping.

**Severity: P1.** It is a missing post-sale capability, not wrong money movement. It becomes urgent within days, once disputes arrive.

**Related:** COM-A-1 removes the pre-ship cancel, so together they leave no refund path at all after payment.

### COM-A-4: CONFIRMED, P1 (VERIFIED-READ for what is sent; the courier's response is SUSPECTED)

**Evidence I checked**
- The P0 checkout writes the order rupee columns as 0 (`P/checkout.go:544`) and the item rupee columns as 0 (`:609`). No trigger or generated column backfills them.
- `GetOrderItems` scans the rupee columns into `FinalPrice` and `UnitPrice` (`P/orders.go:383-410`).
- `packageValue += it.FinalPrice` and `Price: it.UnitPrice` (`S/shipments.go:239-242`) become `sub_total` and `selling_price` (`C/internal/courier/shiprocket.go:133,154`).
- The invoice was fixed to read the stored minor-unit money (`S/shipments.go:792-797`); the booking was not.

**Severity: P1.** It has no effect under the stub that launches. With Shiprocket on, every booking either declares ₹0 (no transit-insurance value) or fails 4xx and retries to the DLQ.

### COM-C-2: CONFIRMED, P1 (VERIFIED-READ)

**Evidence I checked**
- The intent names the seller only as payee (`S/checkout_p0.go:401-405`).
- The paid transaction writes stock, status, job and outbox only (`P/p0.go:312-356`).
- `CreatePayoutBatch` has no caller (`P/seller_payouts.go:183`).
- Earnings use `row.Item.FinalPrice`, which is 0, with float `round2` maths (`S/seller_account.go:122-136,176,184`).
- No other service settles commerce sellers.
- The earnings and payout routes are fenced (`H/handler_p0.go:49-50`).

**Severity: P1.** No money is mis-moved and the amount owed is still derivable from the `*_minor` columns. But seller payout, a core marketplace journey, does not exist, so payouts would be manual from day one.

### COM-C-3: CONFIRMED, P1 (VERIFIED-READ)

**Evidence I checked**
- Invoice numbers use the format `PBK/<FY>/<seq6>` (`Architecture/shared/invoice/generator.go:102-105`) on an FY-only sequence (`002_invoices_shipments.sql:4-8`).
- `NextInvoiceSequence` is an autonomous statement (`P/shipments.go:17-27`), taken before the render, upload and insert (`S/shipments.go:640-681`).
- Commerce renders with `generator.go` `HTMLRenderer` (`:165-215`), which has no place of supply. The newer `invoice/service.go:71-72,307` does have it, but commerce does not use it.
- The buyer name is set from `shipTo.Line1`, the street (`S/shipments.go:750-753,788`).
- There is no credit-note code in `C`.

**Severity: P1.** This is GST-compliance exposure (numbering gaps, a missing mandatory field, no credit notes), not money loss. Fix it before invoices are issued in volume.

### COM-E-1: CONFIRMED, P1 (VERIFIED-READ)

**Evidence I checked**
- `C/Dockerfile:20` builds only `/server`.
- The gated set is embedded separately (`C/database/embed.go`). It holds `1000_*`, `1001_*`, `1002_*`, `998_*` and `999_*`.
- `listMigrationFiles` uses `sort.Strings` (`SH/store/migrationrunner/runner.go:132`), so `1000_*` runs first.
- `1000` raises unless `ciphertext_authoritative_since` is set (`gated/1000_pii_plaintext_scrub.sql:64-73`), while prod and qa run `COMMERCE_PII_CUTOVER: 'dual'`.
- `RunExclusive` returns on the first apply error (`exclusive.go`, the apply loop).
- `runGated` calls `RunExclusive(..., "gated")` (`C/cmd/migrate/main.go:112-118`). `migrationJob.enabled: false` in prod and qa.

**Severity: P1.** The Go paths still check the transition matrix (`transitionAllowedTx`), so the missing database guards are defence-in-depth and not an open hole today.

### COM-E-4: CONFIRMED with a correction, P1 (VERIFIED-READ)

**Evidence I checked**
- The stub is the fallback (`courier.go:125-135`), wired with no environment check (`main.go:137-138`), unlike the stub payment (`main.go:749-756`).
- Prod reads the provider from the secret (`values-prod.yaml:79-85`); its own comment says "the code does NOT refuse the stub in production".

**Correction.** The stub's `VerifyWebhook` does return nil, but its `ParseWebhook` returns no updates (`courier.go:156-161`, used at `S/shipments.go:509-512`). An accepted fake webhook therefore changes nothing. That part of the claim is inert.

**Severity: P1.** It is a configuration gate a lead must check, with no boot guard. Its real harm is COM-A-1's stub case.

**Related:** link to COM-A-1, which is the impact.

### COM-A-13: remains SUSPECTED

**Evidence I checked**
- `createOrder` sends amount, currency, receipt, notes and offers, with no `payment_capture` (`PAY/internal/gateway/razorpay_provider.go:128-145`).
- Razorpay declares `ManualCapture: true` (`:102`).
- No non-test caller of `Capture` exists in any service (git grep of HEAD).
- `payment.authorized` causes no change (HEAD `PAY/internal/service/p0.go:857`).

Whether capture happens depends on the Razorpay account's capture setting, which is outside the code.

**Severity:** P0 if the account is set to manual capture; otherwise not a defect.

**What would confirm it:** the prod Razorpay dashboard's payment-capture setting, and one test-mode payment on qa that reaches `payment.captured` without a manual capture call.

## Summary

| ID | Verdict | Evidence | Corrected severity | Merge / link |
|---|---|---|---|---|
| COM-A-1 | CONFIRMED (the "never delivered" part holds under the stub only) | VERIFIED-READ | P0 | link E-4, C-8, A-2 |
| COM-C-1 | CONFIRMED (a late capture on an expired order IS refunded by commerce) | VERIFIED-RUN + READ | P0 | absorbs E-2 |
| COM-E-2 | CONFIRMED | VERIFIED-RUN + READ | P0 (raised from P1) | duplicate of C-1 |
| COM-A-2 | CONFIRMED | VERIFIED-READ | P1 | link A-1 |
| COM-A-4 | CONFIRMED | VERIFIED-READ / courier response SUSPECTED | P1 | none |
| COM-C-2 | CONFIRMED | VERIFIED-READ | P1 | none |
| COM-C-3 | CONFIRMED | VERIFIED-READ | P1 | none |
| COM-E-1 | CONFIRMED | VERIFIED-READ | P1 | link C-4 |
| COM-E-4 | CONFIRMED (the fake-webhook harm is inert) | VERIFIED-READ | P1 | link A-1 |
| COM-A-13 | stays SUSPECTED | SUSPECTED | P0 if the account is on manual capture, else none | none |
