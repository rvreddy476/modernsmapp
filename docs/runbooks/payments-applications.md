# Runbook: payments applications

Every payment, refund and transaction in payments-service belongs to one **application**, so it can be listed and reconciled per product rather than across the whole platform. Migration `010_applications.sql` introduced this.

## What an application is

An application is a **product key**: `mstore`, `feast`, `mopedu`, `dating`, `vtube`, `momentum`, and so on.

- Feast Kitchen and Feast Rider payments both belong to `feast`.
- The installed app a payment came through is a separate, optional **channel**, for example `momentum_android`, `feast_kitchen_android`, `feast_rider_android` or `web`. A channel is never the application.

Keys and channels both match `^[a-z][a-z0-9_]{1,31}$`. A key is permanent: payment rows reference it, so it cannot be renamed or deleted.

## The registry

The registry is the table `payments.applications`, owned by payments-service.

| Column | Meaning |
| --- | --- |
| `key` | The product key (primary key). |
| `display_name` | Human name, 1-100 characters. |
| `status` | `active` or `disabled`. A disabled application refuses **new** intents with `422 APPLICATION_DISABLED`. Existing payments, refunds, webhooks and the reconciler are not affected, and refunds of its payments are still accepted. |
| `merchant_display_name` | The name the provider checkout sheet should show. It is returned as `client_session.merchant_display_name`. |
| `enabled_methods` | The methods this product accepts, from the launch vocabulary (`upi`, `card`). Any other method gets `422 METHOD_NOT_ENABLED_FOR_APPLICATION`. |
| `settings` | A JSON object for per-product settings (at most 16 KB). |

Migration 010 seeds only `mstore` and `feast`: both active, merchant name "Momentum Merchant", methods `card` and `upi`.

Every registry write that changes something leaves one row in `payments.application_audit_log`, recording the operator, the credential, and the row before and after.

### Read the registry

```sh
curl -s -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" \
  http://localhost:8102/v1/payments/internal/applications
curl -s -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" \
  http://localhost:8102/v1/payments/internal/applications/feast
```

A service token needs `payments:intent.read`, and sees only the applications its caller is allowed (see below).

### Add a product, or change one

`PUT` performs a full replace. Every field except `settings` is required. The response is `201` when the entry was created and `200` otherwise. A replay that matches the stored row returns `"changed": false` and writes no audit row.

```sh
curl -s -X PUT \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" \
  -H "X-User-Id: <operator id>" \
  -H "Content-Type: application/json" \
  http://localhost:8102/v1/payments/internal/applications/mopedu \
  -d '{"display_name":"MopEdu","status":"active","merchant_display_name":"Momentum Merchant","enabled_methods":["upi","card"],"settings":{}}'
```

Credential rules match the parked-refund operator routes:

- **Local, dev and staging:** the internal key is accepted.
- **Production (`ENV=prod`):** the internal key gets `403 SERVICE_TOKEN_REQUIRED`. Use a service token carrying `payments:application.admin`, which is added to that caller's `SERVICE_CALLER_<NAME>_OPS`.

To disable a product, `PUT` the same body with `"status":"disabled"`.

## The caller allowlist

Each token caller declares the applications it may open payments for:

```yaml
SERVICE_CALLERS: 'commerce-service,food-service'
SERVICE_CALLER_COMMERCE_SERVICE_APPLICATIONS: 'mstore'
SERVICE_CALLER_FOOD_SERVICE_APPLICATIONS: 'feast'
```

Boot checks this list against the registry:

| Problem | Production (`ENV=prod`) | Elsewhere |
| --- | --- | --- |
| Empty list | Refuses to start | WARN |
| Key not in the registry | Refuses to start | WARN |
| Key is `disabled` | WARN | WARN |

A disabled key is only a warning on purpose. Otherwise, disabling one product would turn the next pod restart into a crash loop and take every other product's payments down with it.

The legacy internal key carries no identity, so outside production it may name any active application.

### How a new product starts taking payments

1. `PUT` the application into the registry.
2. Add the key to the calling service's `SERVICE_CALLER_<NAME>_APPLICATIONS` in `deploy/services/payments-service/values-*.yaml`, then deploy payments-service.
3. The calling service sends `application_id` (and optionally `channel`) on create-intent and refund. The shared client `Architecture/shared/paymentsclient` already requires it.

## Requests

### Create intent: `POST /v1/payments/internal/intents`

`application_id` is required.

| Refusal | When |
| --- | --- |
| `422 APPLICATION_UNKNOWN` | The key is malformed or not registered. |
| `422 APPLICATION_NOT_ALLOWED` | The key is registered but not in this caller's allowlist. |
| `422 APPLICATION_DISABLED` | The application is disabled. |
| `422 METHOD_NOT_ENABLED_FOR_APPLICATION` | The method is not in `enabled_methods`. |

`channel` is optional; a malformed one is `400 INVALID_BODY`. The idempotency fingerprint includes the application, so the same key with a different application is refused.

### Refund: `POST /v1/payments/internal/intents/:id/refund`

`application_id` must equal the intent's application. Otherwise the request is refused with `422 APPLICATION_MISMATCH`, compared under the intent's row lock. An intent with no application also gets `APPLICATION_MISMATCH`.

### Temporary fallback (one release)

If `application_id` is absent and the token caller has exactly one allowed application, that application is used and a WARN is logged:

```
request named no application_id; attributed to the caller's only allowed application
```

Any other caller, including the legacy key, gets `422 APPLICATION_REQUIRED`.

The user-facing `POST /v1/payments/intents` has no caller identity. It derives an absent application from `reference_type` (`order` becomes `mstore`, `food_order` becomes `feast`).

Remove both fallbacks once every caller sends `application_id`.

## Per-application reads

```
GET /v1/payments/internal/applications/:applicationId/transactions?type=payment|refund&status=&limit=&cursor=
GET /v1/payments/internal/intents?ref_type=&ref_id=&application_id=
GET /v1/payments/internal/refunds/needs-attention?application_id=
```

`transactions` pages newest first:

- A **payment** is an intent; its `status` is the intent status.
- A **refund** is a refund command; its `status` is the command status.
- `limit` is 1-200 (default 50). Pass `next_cursor` back as `cursor`.

A service token may read only allowed applications (`403 APPLICATION_NOT_ALLOWED` otherwise), and only intents its own domain owns. The internal key sees every domain.

Refunds that settled without a refund command (for example a refund initiated from the provider dashboard) are in `provider_refunds_applied`. They do not appear in `transactions`.

Events carry `application_id` too: `payment.succeeded`, `payment.failed`, `payment.refunded` (provider and manual), `payment.refund_failed` and `payment.refund_pending`. Consumers can filter with `paymentevents.ForApplication`.

## Rollout: why the column is nullable first

Migration 010 runs when the first new replica boots, while old replicas are still serving. Old replicas insert intents and refund commands without `application_id`. A `NOT NULL` added in 010 would fail checkout and refunds for the whole rollout. So the migration runs in three steps:

1. **Expand (010, at boot).** It adds nullable `application_id` to `payment_intents`, `refund_commands`, `refund_required`, `provider_refunds_applied`, `refunds_applied` and `payment_holds`, plus a nullable `channel` on intents. Foreign keys to the registry are added `NOT VALID` and validated after the backfill.
2. **Write, echo and backfill.** New code refuses to write a row without an application, and copies the intent's application onto every child row. 010's backfill maps existing rows:
   - First by `owner_domain`: `commerce-service` becomes `mstore`, `food-service` becomes `feast`.
   - Otherwise by `reference_type`: `order` becomes `mstore`, `food_order` becomes `feast`.
   - Child rows copy their intent's application.
   - Rows that map to nothing stay NULL and are counted.
3. **Enforce (gated).** Once no old replica is left, run `database/gated/998_application_id_not_null.sql` by hand. It re-runs the backfill, which catches rows old replicas wrote after 010. It then refuses if any row is still NULL, and otherwise sets `NOT NULL` on all six columns. It takes an ACCESS EXCLUSIVE lock per table while it scans, so run it in a quiet window.

### Check the backfill

```sql
SELECT ran_at, table_name, backfilled, unmapped
  FROM payments.application_backfill_report ORDER BY id DESC LIMIT 12;
```

payments-service also logs the counts at every boot: `payment rows without an application_id` (WARN) or `every payment row carries an application_id` (INFO).

### Map what the backfill could not

1. Look at the unmapped rows:

   ```sql
   SELECT id, owner_domain, reference_type, created_at
     FROM payments.payment_intents
    WHERE application_id IS NULL;
   ```

2. Decide which product each row belongs to, then set it on the intent. Use an audited change ticket, since this is money data.

   ```sql
   UPDATE payments.payment_intents SET application_id = '<key>' WHERE id = '<intent id>' AND application_id IS NULL;
   ```

3. Copy it onto the child rows and record a new report:

   ```sql
   SELECT * FROM payments.backfill_application_ids();
   ```
