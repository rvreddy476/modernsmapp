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

## 0. How you authenticate

The two operator routes (`GET …/refunds/needs-attention` and
`POST …/refunds/:commandId/resolve`) are on the `/v1/payments/internal`
family. Which credential works depends on the environment. payments-service
treats `ENV=prod` as production, and nothing else (a blank `ENV`, `dev` and
`staging` are not production):

| Environment | Internal key (`X-Internal-Service-Key`) | Service token with `payments:refund.admin` |
| --- | --- | --- |
| local / dev / staging | Accepted. It sees every domain. | Accepted. It is scoped to its domain. |
| production (`ENV=prod`) | **Refused** with `403 SERVICE_TOKEN_REQUIRED` before anything is read or written, plus one WARN log line. | Required. |

The production refusal body is:

```json
{"error":{"code":"SERVICE_TOKEN_REQUIRED","message":"this route requires a service token carrying payments:refund.admin; the internal service key is not accepted in production"},"meta":{"request_id":"…"}}
```

Every other `/internal` route keeps its existing credential rules. In
production, payments-service also refuses to start when `SERVICE_CALLERS` is
empty.

### Production: register a caller that holds `payments:refund.admin`

1. **Pick the caller name.** A token only sees and resolves refunds on intents
   whose `owner_domain` equals the token's issuer (`iss`). Food intents are
   owned by `food-service` and commerce intents by `commerce-service`. A
   caller registered under any other name lists nothing and gets `404` on
   every resolve. `ref_types` does not widen this. It only limits which
   `?ref_type=` values the caller may pass.
2. **The founder generates the Ed25519 key pair** offline. The private key is
   never pasted into chat, a ticket, a shell history or a values file. The
   public key (base64) goes into the secret store (`atpost/prod/payments-service`)
   under the caller's `…_pubkey` / `…_kid` entries, which the chart already maps
   to `SERVICE_CALLER_<NAME>_PUBKEY` and `SERVICE_CALLER_<NAME>_KID`.
3. **Grant the operation and reference types** in
   `deploy/services/payments-service/values-prod.yaml` (these are not secret):

   ```yaml
   SERVICE_CALLERS: 'commerce-service,food-service'
   SERVICE_CALLER_FOOD_SERVICE_OPS: 'payments:intent.create,payments:intent.read,payments:refund.create,payments:refund.admin'
   SERVICE_CALLER_FOOD_SERVICE_REFTYPES: 'food_order'
   ```

   The environment shape allows one key per caller name. Adding
   `payments:refund.admin` to `food-service` therefore also lets the
   food-service workload's own signing key mint operator tokens. Decide that
   deliberately.
4. Deploy. Boot logs `payments: registered service caller` with the caller's
   `ops` and `ref_types`. It never logs a key.

### The service-token header

```
X-Service-Authorization: Bearer <token>
```

`<token>` is a compact JWS: `base64url(header).base64url(claims).base64url(signature)`,
signed with Ed25519 by the caller's private key:

```json
{"alg":"EdDSA","typ":"JWT","kid":"<the registered kid>"}
{"iss":"food-service","sub":"<operator or caller>","aud":"payments",
 "iat":…, "nbf":…, "exp":…, "jti":"<random>",
 "scope":["payments:refund.admin"], "ref_types":["food_order"]}
```

- `aud` must be `payments`. `exp - iat` may be at most 5 minutes, so mint a
  fresh token for each session of work.
- The operation must be in both the token's `scope` and the caller's registered
  `OPS`. A `?ref_type=` must be in both `ref_types` and `REFTYPES`.
- `shared/servicetoken` `Signer.Mint(servicetoken.AudiencePayments, subject,
  []string{"payments:refund.admin"}, []string{"food_order"}, 5*time.Minute)`
  produces exactly this. Only the holder of the private key can mint.
  payments-service verifies and never mints.
- `X-User-Id` still names you on a resolve.

A refusal of a token is a terse `403 FORBIDDEN` (wrong key, expired, missing
operation or reference type). The reason is logged by payments-service as
`payments: service token refused`.

## 1. List the parked refunds

Outside production, the internal key works as below. Read it inside the
command and never echo it. In production, send
`-H "X-Service-Authorization: Bearer $TOKEN"` in place of the key header on
this command and on the resolve in step 3. A token only sees intents its own
domain owns.

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
