# Doorstep: founder-run dev readiness

Prepared 5 October 2026. **These steps were not executed by Codex.** No container
was rebuilt/restarted, no dev data was seeded, no AWS configuration changed,
and no emulator or device was used. Keep the public launch gate closed until
the human decisions below are settled. Payouts stay OFF.

## Setup order

1. Review the three branches and their Doorstep commits: backend
   `codex/module-01-02-launch-safety`, customer web `codex/apps-ui-refresh`,
   admin console `feat/commerce-catalogue-attributes`. Preserve unrelated
   Android premium-design work; it was not included in the Doorstep commit.
2. Put secrets only in the gitignored dev environment/secret store. Set
   `DOORSTEP_PII_KEYS=v1:<32-byte key encoded as base64 or hex>` and
   `DOORSTEP_PII_LOOKUP_SALT=<random salt, at least 16 bytes>`. They must be
   set together. Preserve existing key versions when rotating: old sealed
   addresses and KYC fields must remain readable. Do not reuse the lookup salt
   as an encryption key and do not paste either into logs or this document.
3. Generate a separate Ed25519 pair with
   `Architecture/shared/servicetoken.GenerateKeypair()` (returns base64 public
   key, then base64 private key). Securely record the private value as
   `DOORSTEP_SERVICE_TOKEN_KEY`, choose `DOORSTEP_SERVICE_TOKEN_KID=ds1`, and
   register the matching public value as
   `SERVICE_CALLER_DOORSTEP_SERVICE_PUBKEY` with
   `SERVICE_CALLER_DOORSTEP_SERVICE_KID=ds1`. Never print the private value or
   commit a generated key. Use managed key storage for production.
4. Append `doorstep-service` to the existing payments `SERVICE_CALLERS` list;
   preserve all other callers. Payments scopes are
   `payments:intent.create,payments:intent.read,payments:refund.create`, reference
   types `doorstep_booking,doorstep_extras`, application `doorstep`. Compose
   already declares those restrictions. Apply payments migration 015 through
   the service's migration runner before creating Doorstep intents.
5. Append `doorstep-service` to `MEDIA_SERVICE_CALLERS`, preserving the existing
   callers. Media must register the same public key and kid, with BOTH
   `media:image-bytes.read,media:doorstep-photo.prepare`. The latter is new:
   without it visit evidence uploads fail closed. Media migration
   `027_doorstep_photo_scope.sql` must run. Public storage objects are refused
   as visit evidence; use fresh private uploads. Booking participants view
   evidence through `/v1/doorstep/bookings/{id}/photos/{mediaId}` or the
   equivalent professional route, never a public media URL.
6. Keep the admin caller registered in `DOORSTEP_SERVICE_CALLERS` with its
   existing admin-service key. Add `doorstep:prices.review` to the existing
   permitted operation list. Identity and admin-service catalogues include
   the permission; grant it to the intended reviewer through the normal
   permission process. Grant `doorstep:pros.approve` only to authorized
   reviewers: it also controls the step-up GST registration review.
7. Use `DOORSTEP_DEV_SEED=true` only in local/dev/development. It is explicitly
   refused outside those environments. Doorstep migration
   `006_doorstep_visit.sql` must run through startup. Keep
   `DOORSTEP_PUBLIC_ENABLED=false` and use verified UUIDs in
   `DOORSTEP_PILOT_USER_IDS` for the dev pilot. A signed-out shared visit link
   remains unavailable while the product is closed; the launch gate is not
   bypassed for capability links.
8. Set the real dev origin in `DOORSTEP_PUBLIC_BASE_URL` and the approved return
   URL in `DOORSTEP_PRO_APP_LINK_URL`. Complete the professional app's Firebase
   setup and verified App Link host/assetlinks before testing background
   offers or DigiLocker returns. The checked-in `.invalid` hosts are deliberate
   fail-closed placeholders. Confirm the professional application id before
   publishing to Play.
9. Configure actual staffed responders in
   `DOORSTEP_SAFETY_RESPONDER_USER_IDS` and `DOORSTEP_SAFETY_OPS_EMAIL`, and
   exercise the escalation with the responsible team. An in-app SOS is not
   a substitute for emergency services. Do not launch safety messaging with
   an empty responder roster.
10. Obtain the tax-adviser decisions in `docs/DOORSTEP-TAX-ADVISER-REVIEW.md`,
    approve fees/rework windows/commissions and the public product name, then
    approve pilot activation. New registered-only service families and salon
    require an admin-reviewed GSTIN before a professional becomes bookable.
    Separate parts billing and unregistered salon treatment remain disabled.
    Tax registration cannot change during an active booking. Settlements are
    computed only; there is no payout switch or payout action in this work.

## Rebuild: founder only, after the setup above

From `C:/workspace/modernsmapp/Architecture/docker`, on the intended DEV host:

```sh
docker compose build doorstep-service media-service payments-service admin-service notification-service identity-auth web
docker compose up -d --no-build --force-recreate doorstep-service media-service payments-service admin-service notification-service identity-auth web
docker compose ps doorstep-service media-service payments-service admin-service notification-service identity-auth web
```

Do not copy these commands to production or run `down -v`. Startup migrations
are idempotent. Check service health without displaying environment contents.
Deploy the admin console using its own existing pipeline; it is a separate repo.

## Pilot acceptance, founder/QA

Use two pilot accounts and an admin reviewer. Confirm manual skills/documents/
prices/professional/GST approval; select a professional; book scheduled and
ASAP; pay; decline and choose another without silent reassignment; geo arrival;
before/after/kit evidence; OTP attempt lock; extras decisions and signed capture;
15-minute outstanding; completion; ratings; rework; conversation read/pagination;
trusted contact; share rotation/revocation; support; SOS/unsafe exit and staffed
escalation; audited moderation and compute-only settlements. Verify Android
camera/location/payment/App Links and FCM on real test devices yourself.

Only use `doorstep_it_test` for automated integration tests. The test runner
recreates the Doorstep schema and applies migrations twice; never aim it at dev
or production data. See the completion report for exact verification results.
