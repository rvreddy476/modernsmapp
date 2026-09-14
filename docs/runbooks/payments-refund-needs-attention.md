# Runbook: payments refund needs attention

**Alert:** `PaymentsRefundNeedsAttention` (`max by (job) (payments_refunds_needs_attention) > 0` for 5 minutes, severity high).

**What it means.** payments-service accepted a refund, tried to place it, and
got an answer that will never change: the provider refused it, no captured
payment matches the intent, the intent was paid through the stub gateway, and
so on. The refund worker parked the command as `needs_attention` and will not
retry it. **The money is still owed** until someone settles it.

Each park also:

- logs one ERROR: `REFUND NEEDS ATTENTION — parked and not retried; the money is still owed` (with `command_id`, `intent_id`, `reason_code`);
- publishes one `payment.refund_failed` event on `social.events.v1` (contract below);
- increments `payments_refund_parked_total{reason_code}`.

## 1. List the parked refunds

The routes are on the `/v1/payments/internal` family. Use the service's
internal key (legacy callers) or a service token carrying
`payments:refund.admin`. A token only sees intents its own domain owns. Read
the key inside the command and never echo it:

```bash
KEY=$(docker exec atpost_stack-payments-service-1 printenv INTERNAL_SERVICE_KEY)
curl -s -H "X-Internal-Service-Key: $KEY" \
  'http://localhost:8102/v1/payments/internal/refunds/needs-attention?limit=50'
unset KEY
```

Optional query: `ref_type=food_order|order`, `limit` (1..200, default 50), and
`cursor` (the previous page's `next_cursor`). The list is oldest first:

```json
{"data":{"items":[{
  "id":"5b0e…", "intent_id":"9c1d…",
  "reference_type":"food_order", "reference_id":"3f2a…", "payer_id":"c329…",
  "amount_minor":60712, "currency":"INR",
  "reason_code":"provider_rejected",
  "reason":"placing the refund: razorpay: POST /payments/pay_…/refund returned 400 (code=BAD_REQUEST_ERROR reason=input_validation_failed): The id provided does not exist",
  "attempts":1, "provider":"razorpay",
  "provider_order_id":"order_…", "provider_payment_id":"pay_…",
  "requested_by":"food-service",
  "created_at":"2026-09-14T08:10:00Z", "updated_at":"2026-09-14T08:10:15Z"
}],"next_cursor":null}}
```

## 2. Decide what happened

| `reason_code` | Meaning | Usual action |
| --- | --- | --- |
| `provider_rejected` | The provider refused the refund (unknown payment, not captured, over the refundable amount). | Look the payment up in the provider dashboard. Refund it there, or by bank/UPI transfer, then resolve `refunded_manually`. On dev, a seeded simulated capture: `test_data`. |
| `payment_not_found` | No captured payment on the provider order matches the intent's amount and currency. | Check the order's payments in the dashboard. |
| `ambiguous_payment` | More than one captured payment matches, or the intent already holds a different payment id. | Refund the right payment by hand. Check whether the extra capture is also owed back. |
| `invalid_payment_id` | The stored payment id is really an order id. | Find the captured payment in the dashboard. |
| `stub_on_real_provider` | The intent was paid through the stub gateway, and this deployment has a real provider. | Almost always `test_data`. |
| `no_provider_reference` / `no_provider_adapter` / `intent_not_found` | There was nothing to send to a provider. | Settle out of band. |
| `already_refunded_unmatched` | The provider says the payment is fully refunded, but no single unclaimed refund matches this command. | Reconcile in the dashboard. |
| `unclassified` | The command was parked before reason codes existed. | Read `reason`. |

## 3. Resolve it

```bash
KEY=$(docker exec atpost_stack-payments-service-1 printenv INTERNAL_SERVICE_KEY)
curl -s -X POST -H "X-Internal-Service-Key: $KEY" -H "X-User-Id: <your user id>" \
  -H 'Content-Type: application/json' \
  -d '{"resolution":"refunded_manually","note":"UPI refund from the ops account, UTR 1234"}' \
  http://localhost:8102/v1/payments/internal/refunds/<command id>/resolve
unset KEY
```

`X-User-Id` names you; it is stored on the command and in
`payments.payment_audit_log`. `note` is required (at most 1000 characters).

| `resolution` | Use when | Effect |
| --- | --- | --- |
| `refunded_manually` | The money was returned outside the provider integration. | Credits the intent's refunded balance and publishes `payment.refunded` once, with `"manual": true` and `provider_refund_id: "manual:<command id>"`. food-service and commerce-service then finish the order's refund. |
| `written_off` | A decision was made not to return it. Record who approved it in the note. | Nothing is published. The reservation is released. |
| `test_data` | The payment never existed at a provider (dev seeds, stub intents). | Nothing is published. The reservation is released. |

The response is the stored resolution:

```json
{"data":{"command_id":"5b0e…","intent_id":"9c1d…","status":"resolved",
  "resolution":"test_data","note":"dev seed: simulated payment","resolved_by":"d746…",
  "resolved_at":"2026-09-14T09:00:00Z","amount_minor":60712,"currency":"INR",
  "replayed":false,"refund_event_emitted":false}}
```

Resolving an already-resolved command returns its stored resolution with
`"replayed": true` and changes nothing. Other responses:

- `404`: unknown command, or another domain's.
- `409 REFUND_NOT_PARKED`: the command is still being worked, or has settled.
- `409 MANUAL_REFUND_REFUSED`: the intent cannot absorb the refund (not refundable, a different currency, or over its amount).

**Do not** use `refunded_manually` for a refund the provider may still settle.
If a real provider refund later arrives for the same payment, the ledger would
refuse it as over the intent's amount, and the webhook would alarm.

The gauge drops on the next worker tick (15 s) on every replica, and the alert
clears.

## `payment.refund_failed` contract

Published once per command, from payments' outbox, in the transaction that
parks it. The partition key is the intent id. Consumers that do not know the
type ignore it (food-service and commerce-service do).

```json
{
  "event_id": "…", "event_type": "payment.refund_failed", "occurred_at": "…",
  "payload": {
    "id": "<intent id>", "intent_id": "<intent id>", "command_id": "<refund command id>",
    "reference_type": "food_order", "reference_id": "<order id>",
    "provider": "razorpay", "amount_minor": 60712, "currency": "INR",
    "reason_code": "provider_rejected",
    "reason": "placing the refund: razorpay: POST /payments/pay_…/refund returned 400 (code=BAD_REQUEST_ERROR reason=input_validation_failed): The id provided does not exist",
    "status": "needs_attention"
  }
}
```

`reason` is the redacted text stored in `last_error`: the status, the
provider's error code and reason, and a bounded description. It never includes
a raw provider body, a credential or a signature.
