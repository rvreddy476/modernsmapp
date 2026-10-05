# Doorstep dev deployment — 5 October 2026

Executed after the founder explicitly requested deployment. This supersedes
the earlier **not deployed** status for API/web only; it is not a production
launch or evidence that external payment/KYC/device journeys have passed.

## Deployed

- Built and recreated the existing local/dev Compose services:
  `doorstep-service`, `media-service`, `payments-service`, `admin-service`,
  `notification-service`, `identity-auth`, `api-gateway`, `user-service`, `web`.
  Used `--no-build --no-deps --force-recreate` after successful image builds;
  did not restart databases, Redis, Kafka or other infrastructure.
- Source: backend `d1fbe924`, customer web `015fe11`, admin `14af69e`.
  Fixed the admin Dockerfile's obsolete Bun pin and missing workspace-local
  dependencies; verified its production image build and pushed fix `02cf111`.
- Admin image `atpost/admin-doorstep:14af69e` includes that Dockerfile fix.
  Container `atpost-admin-web` runs with `--restart unless-stopped`, on the
  existing `atpost_stack_default` network, bound **only** to
  `127.0.0.1:3002`. Its upstream is `http://api-gateway:8080`.
- Started the existing configured Cloudflare dev tunnel without changing DNS
  or ingress rules after the public site returned error 1033. Public URLs
  subsequently passed the checks below. This is a hidden process, not a newly
  installed Windows service; host restart/reboot supervision remains an ops task.

## Smoke checks

| Check | Result |
|---|---|
| Doorstep `/healthz` inside the service | `{"status":"alive"}` |
| Identity through local gateway `/v1/auth/health` | HTTP 200 |
| `https://api-dev.cleestudio.com/v1/auth/health` | HTTP 200 |
| `http://localhost:3000/doorstep` | HTTP 307, signed-out redirect |
| `https://cleestudio.com/doorstep` | HTTP 307, signed-out redirect |
| `http://localhost:3002/admin/login` | HTTP 200 |
| `http://localhost:3002/admin/api/health` | HTTP 200 |
| Compose web and identity | Healthy; other affected services Up |
| Database migration filenames | Doorstep 001–006 and media 027 recorded |

No signed-in booking/admin mutation was performed. The unauthenticated
Doorstep catalogue returns HTTP 404 while the gateway launch gate is closed;
this is not a successful catalogue/booking acceptance test.

## Deliberately unresolved

- `DOORSTEP_PUBLIC_ENABLED` remains false. No launch/pilot permissions were
  expanded, no payouts enabled and no live tax/safety decisions assumed.
- Doorstep PII keyring/lookup salt and service private key/kid are absent;
  media/payments lack the matching Doorstep public key and Doorstep lacks its
  admin caller registration. Booking/private-data routes must remain fail-closed.
  Asked the founder whether to generate/store new **local dev-only** keys;
  no answer had been received when this report was written.
- Catalogue has zero categories and services. No dev seed or account seed was
  executed. Catalogue population must use reviewed admin data or an explicitly
  authorized dev-only seed; never seed production.
- Existing superadmin: `rvreddy47621@gmail.com`, active, password already set,
  **authenticator MFA not enrolled**. Password is stored as a hash, so its
  original value cannot be retrieved. It was not reset.
- No separate `admin` account exists. Asked for its email and whether access
  should be Doorstep-only; did not invent credentials, grant privileges or
  bypass MFA. Sign-in requires password then authenticator code.
- No Android APK installation, emulator, ADB, Play distribution, AWS change or
  physical-device acceptance test was performed.
- Attempted rollback tagging encountered an old running-image ID no longer
  available in the Docker image store. No rollback image was retained; health
  was checked after the successfully built images were deployed. No image or
  volume cleanup was run.

Next: supply admin identity/scope, authorize local key setup and catalogue
population, enroll MFA personally, then run the pilot checks from
`doorstep-dev-readiness.md`. Do not call this deployment production-ready.
