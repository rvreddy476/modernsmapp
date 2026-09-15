# Runbook — Dating (Pulse): internal pilot

**Status:** internal pilot, dev stack only. **Moderation owner: NOT ASSIGNED.**
**Service:** `Architecture/services/dating-service` (port 8112, proxied by api-gateway at `/v1/dating`).
**Decision this implements (founder, 2026-09-15):** an internal pilot only, behind a fail-closed allowlist, until a moderation owner is named. Distance is shown as buckets only. A selfie is required before discovery or messaging. Premium is sold as one-off passes through payments-service.

Every command below runs in **Git Bash** from `C:\workspace\modernsmapp` unless a step says otherwise.

---

## 0. Before you start: admin access

**Nobody holds an admin or moderator scope today.** `SUPERADMIN_USER_IDS`, `ADMIN_USER_IDS` and `MODERATOR_USER_IDS` on identity-auth are empty, and `auth.user_roles` has no rows. No access token therefore carries a scope. Through the gateway, every `/v1/dating/admin/*` route and `POST /v1/dating/photos/:id/moderation` answer **403 `ADMIN_SCOPE_REQUIRED`**.

### 0.1 Grant a moderator (founder decision; this is a privilege grant)

1. Open `Architecture/docker/.env` and add your own user id:
   ```
   MODERATOR_USER_IDS=<your-user-uuid>
   ```
2. Recreate identity-auth so it re-reads the environment:
   ```bash
   cd Architecture/docker
   docker compose up -d --no-deps identity-auth
   ```
3. Log out and log in again. The scope is stamped into the token when it is issued, so an existing token stays unprivileged.

### 0.2 Dev alternative: call dating-service directly

This works only on the laptop stack, where port 8112 is published locally. **Never use it against staging or production.**

1. Paste these helpers into your Git Bash session. The internal key is read from the container and handed to curl on stdin. It never lands in your history, a file or the output.
   ```bash
   export MSYS_NO_PATHCONV=1
   MOD=<your-user-uuid>   # every audit row names this id

   # dz METHOD PATH [JSON]  — as a moderator
   dz() {
     local key extra=(); key=$(docker exec atpost_stack-dating-service-1 printenv INTERNAL_SERVICE_KEY)
     [ -n "${3:-}" ] && extra=(-H 'Content-Type: application/json' --data "$3")
     printf 'header = "X-Internal-Service-Key: %s"\n' "$key" |
       curl -s -K - -X "$1" "http://localhost:8112$2" -H "X-User-Id: $MOD" -H "X-Scopes: moderator" "${extra[@]}"
     echo
   }

   # dzu USER_ID METHOD PATH [JSON]  — as that user (no scopes)
   dzu() {
     local key extra=(); key=$(docker exec atpost_stack-dating-service-1 printenv INTERNAL_SERVICE_KEY)
     [ -n "${4:-}" ] && extra=(-H 'Content-Type: application/json' --data "$4")
     printf 'header = "X-Internal-Service-Key: %s"\n' "$key" |
       curl -s -K - -X "$2" "http://localhost:8112$3" -H "X-User-Id: $1" "${extra[@]}"
     echo
   }
   ```
2. Check that it works:
   ```bash
   dz GET '/v1/dating/admin/reports?limit=5'
   ```

The rest of this runbook uses `dz` and `dzu`. Once 0.1 is done, the same paths work through the gateway on `http://localhost:8080` with your own token.

---

## 1. Who can use the pilot

The gateway answers **404** on every `/v1/dating/**` path unless one of these holds:
- `DATING_PUBLIC_ENABLED=true`, which **must stay false**;
- the caller's *verified* user id is in `DATING_PILOT_USER_IDS`.

Anonymous callers always get 404. The list fails closed: when it is empty, nobody gets in.

| Environment | Where the list lives | Today |
|---|---|---|
| dev (compose) | `api-gateway` env, default in `Architecture/docker/docker-compose.yml`, override in `Architecture/docker/.env` | call_a `2d598287-eee7-40b4-a7f5-b46b9412e4e7`, call_b `66668bc2-a3f6-40a5-9cdd-c998dcf72f29` (compose default; `.env` has no override) |
| staging / prod | `deploy/services/api-gateway/values-staging.yaml`, `values-prod.yaml` | `""`, so nobody |

### 1.1 See who is allowed (dev)

```bash
docker exec atpost_stack-api-gateway-1 printenv DATING_PILOT_USER_IDS
```

### 1.2 Add or remove someone (dev)

1. Edit `Architecture/docker/.env` and set the full list: comma-separated UUIDs, no spaces. To keep call_a and call_b, list them too.
   ```
   DATING_PILOT_USER_IDS=2d598287-eee7-40b4-a7f5-b46b9412e4e7,66668bc2-a3f6-40a5-9cdd-c998dcf72f29,<new-uuid>
   ```
2. Recreate the gateway. A plain `restart` does **not** re-read `.env`.
   ```bash
   cd Architecture/docker
   docker compose up -d --no-deps api-gateway
   ```
3. Check the effective list (1.1). Then check that anonymous callers still get 404:
   ```bash
   curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8080/v1/dating/profile   # expect 404
   ```
4. Removing someone only closes the door. Their dating data stays, and other pilot users can still see their profile. To hide them as well, suspend them (section 5).

### 1.3 Staging or production

1. Set `DATING_PILOT_USER_IDS` in `deploy/services/api-gateway/values-<env>.yaml`.
2. Commit, then deploy through CI.
3. In production an entry that is not a UUID **refuses boot**. Elsewhere it is dropped with a warning.
4. The Azure values files do not set the key, so the list there is empty and nobody gets in.

---

## 2. Seed and reset (dev)

Script: `Architecture/services/dating-service/scripts/dev-seed-dating.sh`. It refuses anything but the local stack, never logs in, sets no passwords and prints no secrets.

1. Seed, or top up. It is safe to re-run.
   ```bash
   cd Architecture/services/dating-service
   bash scripts/dev-seed-dating.sh
   ```
   It creates:
   - 20 synthetic adults around Bengaluru (10 women, 10 men), active, each with a placeholder photo and a passed selfie;
   - dating profiles for call_a (male, looking for women, Koramangala) and call_b (female, looking for men, Indiranagar), **only if they have none**;
   - matches: call_a with Ananya and Riya, call_b with Arjun and Dev, and call_a with call_b;
   - three incoming sparks each for call_a and call_b;
   - one report (Kabir against Vikram, `fake_profile`) and one panic incident (Kavya).
2. Read the summary. It lists active profiles, each pilot account's deck size with bucket codes, matches, incoming sparks and admin queue rows.
3. See what a reset would delete, without deleting anything:
   ```bash
   bash scripts/dev-seed-dating.sh --reset --dry-run
   ```
4. Reset. This removes the synthetic users' dating rows and media, and every spark and match between call_a and call_b. call_a's and call_b's own profiles, photos and consents stay.
   ```bash
   bash scripts/dev-seed-dating.sh --reset
   ```

Know before you seed:
- **Pushes reach the pilot phones.** Every new spark and match emits the normal events, so they notify call_a and call_b.
- **Matches have no chat conversation right now.** chat message-service answers `401 Missing bearer token` to dating-service's `POST /v1/chat/conversations/dating-match`. Its JWT middleware is installed on the whole router (`chat-service/services/message-service/cmd/server/main.go`), so it refuses the call before the handler's internal-key check runs. Every match stays `matched` with no `conversation_id`, and dating's saga reconciler retries every minute, logging `match saga retry failed`. This is a chat-service fix; the seeder cannot work around it.
- A match expires after 7 days unless someone sends the first message.
- Today's deck holds at most **7 cards**: the top scores, with at most 3 per intent and 2 per community. Nearer profiles usually fill it, so `km_10_25` and `gt_25_km` may not appear even though they are seeded.
- **Mock selfie on dev.** Photo prepare re-encodes the image, which strips the mock face marker. The mock liveness check then answers `NO_FACE` for *any* photo attached through the app. The seeder writes its marked placeholder back; a phone walkthrough on dev will fail the selfie until that is fixed. Use selfie review (3.2) to pass a tester by hand.
- These steps have no service route and write directly, dev only: media rows, MinIO objects, deck-cache drops, and the reset deletes. The script header marks each one.

---

## 3. Moderation queues

### 3.1 Photo review

A photo lands in the queue when its labels are borderline, when it was not scanned, or when a primary photo shows no face. Explicit labels at 80 or above are rejected automatically.

1. List the queue:
   ```bash
   dz GET '/v1/dating/admin/photos/pending?limit=50'
   ```
2. Approve a photo:
   ```bash
   dz POST /v1/dating/photos/<photo_id>/moderation '{"status":"approved"}'
   ```
3. Or reject it:
   ```bash
   dz POST /v1/dating/photos/<photo_id>/moderation '{"status":"rejected","reason":"not a photo of the person"}'
   ```

Approving a primary photo moves the profile from `pending_photo` to `pending_selfie`. Rejecting the approved primary of an active profile sends it back to `pending_photo`. Each decision writes an audit row naming you.

### 3.2 Selfie review (similarity 80–90, or a high-risk first attempt)

1. List the queue:
   ```bash
   dz GET '/v1/dating/admin/verification/selfie/pending?limit=50'
   ```
2. Decide:
   ```bash
   dz POST /v1/dating/admin/verification/selfie/<user_id>/review '{"decision":"approve","reason":"matches profile photo"}'
   dz POST /v1/dating/admin/verification/selfie/<user_id>/review '{"decision":"reject","reason":"different person"}'
   ```

Approve moves the profile from `pending_selfie` to `active`; reject counts as a failed attempt. **Gap:** moderators cannot view the selfie video yet, so the decision rests on the similarity score and the profile photos.

### 3.3 Reports

Every report does three things: it auto-blocks reporter and target both ways, it opens a trust-safety grievance with a 15-day timer (`grievance_id`), and an `underage` report moves the target to `pending_review` at once.

1. List new reports:
   ```bash
   dz GET '/v1/dating/admin/reports?status=submitted&limit=50'
   ```
2. Act on one:
   ```bash
   dz POST /v1/dating/admin/reports/<report_id>/action '{"action":"dismiss"}'
   ```

| `action` | Report becomes | Profile effect |
|---|---|---|
| `dismiss` | `closed_no_action` | none |
| `resolved` | `resolved` | none (action taken elsewhere) |
| `warn` | `actioned` | none (warning sent out of band) |
| `review` | `actioned` | target → `pending_review` |
| `restrict` | `actioned` | target → `restricted` |
| `suspend` | `actioned` | target → `suspended` |
| `reinstate` | `actioned` | target's hold lifted, remembered step restored |

`target_user_id` is optional. When given, it must be the report's own target.

### 3.4 Audit and risk

```bash
dz GET '/v1/dating/admin/audit?limit=50'
dz GET '/v1/dating/admin/risk?limit=50'
```

---

## 4. Panic handling

How it works today:
- A panic always records an incident. A repeat within 2 minutes returns the same incident. More than 5 in a day is flagged `suspected_abuse` and not paged.
- **`DATING_SAFETY_RESPONDER_USER_IDS` is empty** in dev compose, staging, prod and the Azure values, so **no staff member is paged.** notification-service still alerts the user's trusted contacts, if they set any.
- notification-service writes one row to `notify_meta.ops_alerts` (source `dating-service`) with kind `dating_panic_paged`, `dating_panic_no_responder` or `dating_panic_suspected_abuse`. The row carries no coordinates.

Steps:

1. Look for unhandled alerts:
   ```bash
   docker exec atpost_stack-postgres-1 psql -U postgres -d app -c "SELECT id, kind, severity, subject_id, created_at FROM notify_meta.ops_alerts WHERE source = 'dating-service' AND acknowledged_at IS NULL ORDER BY created_at DESC LIMIT 20;"
   ```
2. List open incidents. The list carries `has_location`, never the point.
   ```bash
   dz GET '/v1/dating/admin/safety/panic?status=open&limit=50'
   ```
3. Acknowledge the incident. The user is told support has seen it.
   ```bash
   dz POST /v1/dating/admin/safety/panic/<incident_id>/ack
   ```
4. Open the detail **only if you need the location to help**. It returns the exact point and writes a `panic_viewed` audit row with your id. Never copy the coordinates into chat, a ticket or a document.
   ```bash
   dz GET /v1/dating/admin/safety/panic/<incident_id>
   ```
5. Escalate using the founder's escalation contacts. They are not defined yet; see the checklist.
6. Resolve with a note:
   ```bash
   dz POST /v1/dating/admin/safety/panic/<incident_id>/resolve '{"note":"called user, safe"}'
   ```
7. Mark the ops alert handled. There is no route for this; it is dev only.
   ```bash
   docker exec atpost_stack-postgres-1 psql -U postgres -d app -c "UPDATE notify_meta.ops_alerts SET acknowledged_at = now() WHERE id = '<alert_id>' AND acknowledged_at IS NULL;"
   ```
8. To page real people, set `DATING_SAFETY_RESPONDER_USER_IDS` (and optionally `DATING_SAFETY_OPS_EMAIL`) on notification-service. On dev that means `.env`, then:
   ```bash
   cd Architecture/docker
   docker compose up -d --no-deps notification-service
   ```
   On staging and prod, set the same keys in `deploy/services/notification-service/values-*.yaml`. Each responder must also hold an admin or moderator role; notification-service re-checks it before paging.

---

## 5. Suspend, restrict, reinstate and ban

1. Every hold goes through a report action (3.3): `review`, `restrict`, `suspend` or `reinstate`.

   | Status | The user can | Others see them |
   |---|---|---|
   | `pending_review` | nothing outbound (no new sparks or chats) | no |
   | `restricted` | nothing outbound | no |
   | `suspended` | nothing interactive | no |
   | after `reinstate` | the step they were on before the hold | yes, if that step was `active` |

   The user's own pause and unpause never lift a hold.
2. **Ban:** dating has no separate ban state. Suspend the profile and never reinstate it. An account-wide ban belongs to identity and trust-safety, outside dating.
3. **Gap:** there is no admin route to hold a profile *without* a report. Until one exists, file the report first. A moderator reporting from their own account also auto-blocks them from the target.
4. Check the result as the user:
   ```bash
   dzu <user_id> GET /v1/dating/profile     # look at "profile_status"
   ```

---

## 6. Evidence retention, data export and deletion

### 6.1 Retention

- `DATING_EVIDENCE_RETENTION_DAYS` is `180`.
- When a profile is purged, its reports, panic incidents and HMAC-hashed risk and device signals are kept under an anonymised token until `retain_until`.
- Hashing needs the secret `dating_evidence_hmac_key`. Outside local/dev, boot refuses without it. **Do not rotate it casually:** a new key unlinks everything already retained.

### 6.2 Data export (the user's request)

1. Request it (limited to one per 7 days):
   ```bash
   dzu <user_id> POST /v1/dating/data-export
   ```
2. Poll until it is ready:
   ```bash
   dzu <user_id> GET /v1/dating/data-export/me
   ```
3. Download it (owner only, sealed at rest, available for 7 days):
   ```bash
   dzu <user_id> GET /v1/dating/data-export/<export_id>/download
   ```
4. The worker is `atpost_stack-dating-data-exporter-1` on dev. Check it is up:
   ```bash
   docker ps --filter name=dating-data-exporter
   ```

For the wider manual process (identity check, delivery, audit), see `docs/runbooks/privacy-data-rights-manual.md`.

### 6.3 Deletion

1. The user deletes their profile:
   ```bash
   dzu <user_id> DELETE /v1/dating/profile
   ```
   The profile becomes `deleted` (a soft delete) and disappears from every surface.
2. The purge of the rows should follow after `DPDP_GRACE_DAYS` (30) days. **Gap: the purger (`/data-purger`) runs nowhere.** Compose has no purger service, and the chart's single worker slot is taken by the exporter. Soft-deleted profiles are never purged today.
3. Manual one-off purge on dev. **Not yet run; check the output before trusting it.**
   ```bash
   cd Architecture/docker
   docker compose run --rm --no-deps -e DATA_PURGER_ONCE=true --entrypoint /data-purger dating-service
   ```

---

## 7. Premium test purchase on dev, and what a refund does

Catalogue: `pass_30d` ₹399, `pass_90d` ₹999, `pass_365d` ₹2,499, `boost` ₹49. Payments-service takes the money under application `dating`, and dating grants only on the signed `payment.succeeded` event.

1. Read the catalogue:
   ```bash
   dzu 2d598287-eee7-40b4-a7f5-b46b9412e4e7 GET /v1/dating/premium/catalogue
   ```
2. Start a purchase as call_a. Use a fresh idempotency key each time; never send a price, because the server refuses one (`400 CLIENT_PRICE_REFUSED`).
   ```bash
   idem=$(powershell.exe -NoProfile -Command "[guid]::NewGuid().ToString()" | tr -d '\r')
   dzu 2d598287-eee7-40b4-a7f5-b46b9412e4e7 POST /v1/dating/premium/purchases "{\"product\":\"pass_30d\",\"idempotency_key\":\"$idem\",\"method\":\"upi\"}"
   ```
   The response is 201 with `purchase` and a `client_session` (a Razorpay **test** order). Note `purchase.id` and `purchase.payment_intent_id`. A `503 PREMIUM_UNAVAILABLE` means payments returned no checkout session, or dating has no service-token key.
3. Pay, using one of:
   - **Real test payment (preferred):** Razorpay test checkout with a test card or UPI id, from the Android app once Wave 3 is in.
   - **Simulated capture:** a signed `payment.captured` webhook, the way `Architecture/services/food-service/scripts/dev-seed-feast.sh` builds one (`seed_order`). The webhook secret is read from the payments container, never pasted anywhere.
4. Check the grant:
   ```bash
   dzu 2d598287-eee7-40b4-a7f5-b46b9412e4e7 GET /v1/dating/premium/purchases/<purchase_id>/payment   # status: paid
   dzu 2d598287-eee7-40b4-a7f5-b46b9412e4e7 GET /v1/dating/premium/me                              # active pass + expiry
   ```
5. Refund. Dating has **no refund route of its own**; refunds go through payments-service. On dev the internal key is accepted. Production needs a dating-service token carrying `payments:refund.create`.
   ```bash
   export MSYS_NO_PATHCONV=1
   pkey=$(docker exec atpost_stack-payments-service-1 printenv INTERNAL_SERVICE_KEY)
   printf 'header = "X-Internal-Service-Key: %s"\n' "$pkey" | curl -s -K - -X POST \
     "http://localhost:8102/v1/payments/internal/intents/<payment_intent_id>/refund" \
     -H 'Content-Type: application/json' \
     --data '{"amount_minor":39900,"reason":"dev refund test","idempotency_key":"<new-uuid>","application_id":"dating"}'
   unset pkey
   ```
   Payments confirms the refund from the provider webhook, then emits `payment.refunded`.
6. Check the effect. `GET .../purchases/<purchase_id>/payment` shows `refund_status`, and `GET /premium/me` shows the pass.

   | Refund | Purchase | Pass | Boost |
   |---|---|---|---|
   | full | `refunded` | revoked | unsold tokens untouched; spent Boost not clawed back |
   | partial | `partially_refunded` | shortened pro rata | untouched |

   A refund of a **simulated** capture can never succeed at Razorpay. Payments parks it as `needs_attention`; resolve it with `docs/runbooks/payments-refund-needs-attention.md`. To see a real refund end to end, pay with a real test payment in step 3.

---

## 8. Launch checklist (open founder actions)

Nothing beyond the internal pilot opens until every **blocking** row is done.

| # | Action | Where | Blocking |
|---|---|---|---|
| 1 | **Name a dating moderation owner**, with coverage hours and response targets | founder | yes |
| 2 | **Panic responders:** set `DATING_SAFETY_RESPONDER_USER_IDS` (+ `DATING_SAFETY_OPS_EMAIL`) and name the escalation contacts. Each responder needs an admin or moderator role. | `deploy/services/notification-service/values-{staging,prod,azure-*}.yaml` | yes |
| 3 | **Admin scope bootstrap**, so moderators work through the gateway (section 0.1) | identity-auth env / `auth.user_roles` | yes |
| 4 | **Secret `dating_evidence_hmac_key`** (≥ 32 bytes; boot refuses without it) | staging/prod secret store + Azure key vault | yes |
| 5 | **Secrets `dating_pii_keys`** (`v1:<base64 32-byte key>`) **and `dating_pii_lookup_salt`** (≥ 16 random bytes) | same | yes |
| 6 | **Secrets `dating_service_token_key` / `dating_service_token_kid`** (Ed25519 seed + kid), then uncomment their refs in `deploy/services/dating-service/values-*.yaml` | same | yes, for Premium |
| 7 | **Secrets `caller_dating_pubkey` / `caller_dating_kid`** in payments-service, then add `dating-service` to its `SERVICE_CALLERS`. Enabling this earlier refuses payments boot for commerce and food too. | payments-service secret store + values | yes, for Premium |
| 8 | **Rekognition IAM:** media-service IRSA needs `rekognition:DetectFaces` and `rekognition:CompareFaces` | Terraform | yes, for selfie outside dev |
| 9 | **Play Billing policy check** for Premium passes sold in the Android app | founder / legal | yes, before public launch |
| 10 | **Legacy consent decision:** religion or community stored before consent existed is kept; gender preference has no consent gate | founder | yes |
| 11 | **Purger worker gap:** deploy `/data-purger` (second worker or CronJob) so deletions complete after 30 days | chart + compose | yes |
| 12 | Decide the 180-day evidence window, and whether panic coordinates are cleared after purge | founder | before staging |
| 13 | Replay protection for selfie (a pre-recorded blink video passes); AWS Face Liveness is the stronger option | founder | before public launch |
| 14 | DigiLocker API Setu registration, if Aadhaar verification is wanted later | founder | no |
| 15 | Grievance officer details for the IT Rules surface | founder | before public launch |

Engineering gaps found so far (not founder actions, tracked here so launch does not miss them):
- chat message-service refuses dating's conversation call (`401 Missing bearer token`), so no match gets a chat (section 2).
- Mock selfie fails for every app-attached photo on dev: prepare strips the mock marker (section 2).
- No admin route to hold a profile without a report (section 5). Moderators cannot view the selfie video (3.2).
- trust-safety's purge deletes grievances filed by the purged user, which undoes retention. Pre-D8 reports have no grievance backfill.
- Staging/prod: count duplicate open matches before the first deploy (`scripts/count-duplicate-matches.sql`; `DATING_DEDUPE_OPEN_MATCHES` for one deploy). Count legacy photos whose media is not the owner's (the recheck rejects them).
- A Kafka publish failure after purge leaves the chat open (no retry). `dating.premium.expiring_soon` has no notification handler. Purge deletes premium purchases (payments keeps its record).
- CI must set `worker.image.tag` equal to `image.tag` for the exporter worker.
