# VChat / atPost — Test Automation Plan

> Companion to `ARCHITECTURE.md`. Describes the **current** test footprint, the
> **target** test architecture, and the **workstreams** to get there. Grounded in
> what's actually in the repos (not aspirational) — each claim is checkable.

## 1. Current state (surveyed)

### Moped availability warning (2026-10-10, v0.4.1 hotfix)

The reported "Couldn't check Moped" message was reproduced from deployed
configuration, not from an approval record: the gateway has
`RIDER_PUBLIC_ENABLED=false`. Both the local gateway and public
`GET https://cleestudio.com/v1/rider/partners/me` return HTTP 404 with
`{"error":{"code":"NOT_FOUND","message":"Not found"}}`.
The gateway source confirms this gate applies to authenticated callers too;
Moped has no partner-only exception. No user account or server flags were
modified, and public ride bookings remain closed.

The mobile workspace now classifies that specific Moped unavailable-route
response as unavailable, not a profile/approval failure and not an absent
profile. The service's distinct `NOT_FOUND / no partner profile` response
still means Not started and permits entering onboarding. Authentication,
server, malformed-body and unrelated route failures remain real errors.
Unavailable categories cannot be added/opened from the picker/cards. A
previously selected/deep-linked Moped workspace shows an availability message
with retry instead of entering a failing onboarding flow. Previously
discovered registrations/names are retained on unavailable/error refreshes,
but stale approval is never presented as a current successful check.
Home Services approval and other categories remain independent.

Verified command from `mobile/android`:

```powershell
.\gradlew.bat --offline --max-workers=2 :app-doorstep-pro:assembleDevDebug :app-doorstep-pro:testDevDebugUnitTest moduleGraphCheck -PdevApiUrl=https://cleestudio.com -PdevWsUrl=wss://cleestudio.com
```

All 26 host tests pass (13 model, 6 real Retrofit/HTTP wire tests, 7 native
Compose layout/interaction tests), zero failures/skips. Tests replay the
observed gateway response, distinguish absent/approved profiles, preserve
genuine errors and cancellation, assert no Moped warning for the closed
unselected category, and keep selected unavailable cards non-clickable.
The full home native render was inspected; module graph checks pass.
No Android device was connected, so installation/on-device acceptance was
not performed.

Artifact: `output/partner-release-20261010/cleestudio-partner-0.4.1-dev.apk`
(unchanged package `com.us.doorstep.pro.dev`, version code 5, `0.4.1.dev`).
SHA-256: `9E48FC1ED088BED8BEAD2755B8279F6E35CEF8F430B079F7840AA02A964FAB46`.
Earlier APKs remain intact. This is an APK hotfix, not a Moped service launch
or a backend deployment.

### Partner mobile scope correction (2026-10-10, v0.4.0 test build)

This supersedes the v0.2/v0.3 merchant scope below. The mobile Partner host is
for **Food Delivery, Fish Delivery, Commerce Delivery, Home Services and Moped**,
not restaurant/kitchen owners or sellers. Kitchen/seller module dependencies,
navigation, onboarding and the host's kitchen alert adapter were removed.
Merchant accounts/data and the separate merchant apps were not deleted.
Saved `DELIVERY`/`RIDE` selections migrate to Food Delivery/Moped; old
`FOOD`/`SELLING` selections are dropped, never treated as delivery approval.

Food Delivery opens the real rider profile, vehicle and verification flow;
Home Services and Moped retain their native worker modules. Fish Delivery has
no dispatch/profile API in this repository, and commerce fulfilment currently
uses external couriers rather than a native delivery-worker API. Those two
categories are explicitly disabled as **Not available yet** in the picker.
They do not call merchant APIs, accept applications locally or invent jobs.

The real app uses the same tested PartnerShell/PartnerBusinessOverview:
compact header, fixed four-tab navigation, compact business cards in two
columns on normal phones and one column for narrow screens/large text.
Removed the large welcome panel, repeated setup prompts and resource/help
cards from the overview. The three supported categories fit at 360x800 dp
with zero scroll range; content may scroll only when needed at large font
sizes. Existing UsTheme tokens/components remain the design system.
Server approval is unchanged; a pending delivery application does not mark
an approved Home Services application pending.

Android command (from `mobile/android`):

```powershell
.\gradlew.bat --offline --max-workers=2 :app-doorstep-pro:assembleDevDebug :app-doorstep-pro:testDevDebugUnitTest :core:notifications:testDebugUnitTest :feature:rider:testDebugUnitTest :feature:doorstep-pro:testDebugUnitTest moduleGraphCheck -PdevApiUrl=https://cleestudio.com -PdevWsUrl=wss://cleestudio.com
```

All 232 tests pass (host 14, notifications 64, rider 95, professional 59),
zero failures/skips. Full native Compose renders include the actual fixed
header/navigation, normal home, 1.5x text businesses and Food Delivery entry;
callbacks, distinct statuses and merchant-field absence are asserted.
The runtime graph contains only rider, Moped captain, auth and professional
features. APK class inspection confirms kitchen/seller/customer-commerce
feature classes are absent. `moduleGraphCheck` additionally rejects both
direct and transitive kitchen/seller dependencies in this host.

Partner alert display/routing accepts only worker events. Notification-service
recipient selection no longer includes Partner installs in kitchen or seller
events. Go store/service/HTTP tests pass; five device/routing tests, including
upgrade, sign-out and merchant/customer exclusion, also pass against the
isolated local `notification_unified_20261010_test` database (synthetic rows
only, cleaned up by tests). **These backend changes are not deployed**.
System-rendered background FCM notification envelopes still depend on that
server rollout; an APK-only update cannot filter them before Android displays
them. No live notifications were sent for these checks.

Prepared artifact: `output/partner-release-20261010/cleestudio-partner-0.4.0-dev.apk`
(package `com.us.doorstep.pro.dev`, version code 4, `0.4.0.dev`). SHA-256:
`60F2C26E93D6239F2A82E19540887F51617F110BF4EF1AA845DAE191584F7972`.
Prior APKs are preserved. No device installation, production deployment,
production account mutation or full on-device delivery acceptance occurred.

### Partner mobile layouts and approval refresh (2026-10-10, v0.3.0 test build)

The unified Partner home, phone verification and submitted-application receipt
now follow the supplied mobile layouts using the existing UsTheme colours,
typography, icons, surfaces and navigation. Business cards adapt to narrow
screens/large text; the four primary tabs stay available inside a business.
The account bottom sheet links to businesses, notifications and phone settings.
The review receipt shows submission, not approval, and only actual submission
dates. It does not promise email delivery or invent documents/review decisions.

The user identified Rahul reddy as the account displaying pending verification.
Read-only checks of the live database and `/v1/doorstep/pro/me` confirmed
`approved`; `/v1/doorstep/pro/readiness` returned no missing steps and
`can_go_on_duty=true`. No approval records were changed. A different pending
account was not altered. The original incorrect pending screen has not yet been
provided, so that on-device symptom is not claimed as reproduced.

The user subsequently supplied the red "some business statuses couldn't be
loaded" message. Read-only requests reproduced its underlying API shapes for
this account: Food returns `200 data.items=null`, Ride returns
`404 NOT_FOUND / no partner profile`, Selling returns
`404 NOT_FOUND / seller not found`, and Delivery returns
`404 FOOD_DELIVERY_PROFILE_NOT_FOUND`. The aggregate parser previously treated
the first three as failures. It now recognises these service-specific absent
profiles as Not started. Missing/malformed responses, generic route failures,
authentication failures and server errors remain unavailable, not empty or
approved. Genuine failures name the business and offer a retry, without implying
the separately approved Home Services account failed verification.

Both workspace and professional status refresh on app resume. The pending
receipt additionally reconciles every 30 seconds while visible, without
unmounting content; forms being edited are not refreshed. The authoritative
professional result updates the home card. An old submitted application cannot
override an approved status, and network failure leaves existing content in
place with a refresh error. Profile parsing now reads `display_name`.

OTP uses the shared six-box control backed by one editable field (including
its previously omitted inner input). Paste is filtered to six ASCII digits;
change-number, resend cooldown and verify actions are wired. The resend timer
uses elapsed time so it catches up after backgrounding. Development delivery
is explicitly identified as server-log delivery, not SMS.

Verification command (from `mobile/android`):

```powershell
.\gradlew.bat --offline --max-workers=2 :app-doorstep-pro:assembleDevDebug :app-doorstep-pro:testDevDebugUnitTest :feature:doorstep-pro:testDebugUnitTest :feature:auth:testDebugUnitTest :core:designsystem:testDebugUnitTest moduleGraphCheck -PdevApiUrl=https://cleestudio.com -PdevWsUrl=wss://cleestudio.com
```

All 146 tests pass: host 12, authentication 6, professional 59, design system 69;
zero failures/skips. The module graph checks 58 modules. Native Compose renders
were inspected for the real business-card components, OTP and review receipt
at 412 dp, plus OTP at 320 dp with 1.5x font scaling. Tests exercise OTP editing,
verification/change-number callbacks, scroll access, distinct approved/pending
cards, receipt refresh, pending-to-approved reconciliation, network errors and
preserving an application being edited. These are not full-device screenshots
or complete multi-business end-to-end acceptance.

Prepared artifact: `output/partner-release-20261010/cleestudio-partner-0.3.0-dev.apk`
(package `com.us.doorstep.pro.dev`, version code 3, `0.3.0.dev`). SHA-256:
`7FAC4DA77CDC82796008F038436F0913F1A9359DDB20634D02089BFBCF9F96A0`.
Version 0.2 remains available. No installation, backend deployment, account
mutation or new physical-device acceptance was performed for v0.3.0.

### Unified Partner mobile workspace (2026-10-10)

The existing `:app-doorstep-pro` installation is the unified Cleestudio Partner host.
Its package ID stays unchanged for Firebase/install compatibility. It hosts native
Selling, Food, Ride, Delivery and Home Services; local business selection is an
application intent, not an approval. Each business still enforces its server-side
onboarding, document, availability and work permissions.

Seller screens now live in `:feature:seller`; shared commerce UI is in
`:core:commerce-ui`, and pure try-on vocabulary is in `:core:facear-model`.
The partner APK must not acquire the customer commerce flow or the Banuba runtime.
Customer shopping still consumes the shared UI and keeps its existing navigation.

Build and regression checks:

```powershell
.\gradlew.bat --offline --max-workers=2 moduleGraphCheck :app-doorstep-pro:assembleDevDebug :app-doorstep-pro:testDevDebugUnitTest :feature:seller:testDebugUnitTest :core:notifications:testDebugUnitTest :feature:commerce:compileDebugKotlin -PdevApiUrl=https://cleestudio.com -PdevWsUrl=wss://cleestudio.com
```

Release dependency: deploy notification-service migration 014 and unified device
routing before installing the new APK. Upgrading an existing Doorstep install
re-registers its unchanged FCM token as `partner`. Verify that customer/social
notifications do not arrive in Partner, that each role's alert opens its own
workspace, and that sign-out removes the actual persisted device ID.

Manual acceptance still requires a phone: new/existing multi-business accounts;
role switching and back navigation; returning to a partly completed form;
document upload; kitchen creation; foreground/background alerts; sign-out then
sign-in as a different account; large text; and each role's real work lifecycle.
No deployment or device-install success is implied by these instructions.

Verified locally on 2026-10-10: the dev APK assembles; moduleGraphCheck passes
for 58 modules; 397 unit tests pass across the Partner host, seller, notifications,
customer commerce, shared commerce UI, food core and kitchen feature. The
notification-service store, service and HTTP packages pass their Go tests.
The database-backed device migration test is conditional on
`NOTIFICATION_TEST_DSN`; it subsequently passed during deployment against the
isolated `notification_unified_20261010_test` database in the existing QA
Postgres container. Phone acceptance remains outstanding; this is a test build,
not production sign-off.

Deployment verified on 2026-10-10 after explicit approval:

- Updated only `atpost_stack-notification-service-1` with the existing four
  Compose files; preserved the live environment and Firebase secret mount.
- Image: `sha256:0fe4ecd71eab2e99f03a3d1942677eff9b2b69cbe1cd4a228c92eec979c1a068`.
- Migration `014_unified_partner_app.sql` is recorded and the device app CHECK
  includes `partner`. `/readyz` reports Postgres, Redis and Scylla up; zero restarts.
- Firebase startup confirms project `us-unified-services`. Public auth health
  returns 200; unauthenticated notifications return the expected 401.
- Rollback image retained as
  `atpost/notification-service:rollback-before-unified-partner-20261010`.
  Customer/admin containers, existing data and infrastructure were not restarted.
- Install artifact: `output/partner-release-20261010/cleestudio-partner-0.2.0-dev.apk`,
  package `com.us.doorstep.pro.dev`, version code 2, version `0.2.0.dev`.
  SHA-256: `09B862DC165B386FAFB2BCB463807CFC1BE887138D2B5AD53E991FBFC23BBF6C`.
  No phone was connected, so no installation or on-device acceptance is claimed.

| Layer | State | Where |
|---|---|---|
| **CI** | GitHub Actions | `.github/workflows/`: `go-ci` (`go test -race ./...`), `lint` (golangci v1.61), `security` (govulncheck/gosec/trivy, weekly cron), `build-push`, `promote`, `android-native` (assemble, test, detekt) |
| **Go unit** | 186 test files; **22/31 services** have tests → **9 with none** | `Architecture/services/*`, `identity-platform/*`, `chat-service/*` |
| **Integration** | build-tagged (`-tags integration`), HTTP-level vs a running stack, **run by hand**, not in CI | `Architecture/tools/integration/` (+ `run-integration.sh`) |
| **Load** | k6-style, **only** rider + dating, not automated | `Architecture/services/{rider,dating}-service/loadtest` |
| **Mobile** | 98 Android (Kotlin) unit-test files, **in CI** | `mobile/android/*/src/test/`, `android-native.yml` |
| **Web (postbook-ui)** | **Zero tests, not in CI** — biggest gap | — |

## 2. Known issue fixed (W1 — done)

The integration harness authenticated with synthetic `X-User-Id` headers. The
2026-06 auth hardening makes the **gateway strip inbound `X-User-Id`** and derive
identity from the verified JWT — so gateway-routed integration tests broke.
**Fixed:** `tools/integration/client.go` now mints a real HS256 JWT (signed with
the dev `JWT_SECRET`, `ATPOST_JWT_SECRET` override) and sends `Authorization:
Bearer` alongside `X-User-Id`; admin clients get privileged `scopes`. Works for
both gateway-routed and direct-to-service calls.

## 3. Target test architecture (mapped to the stack)

```
        ▲  fewer, slower, higher-confidence
   E2E  │  Playwright (web) + Flutter integration_test (mobile) → ephemeral stack
Contract│  consumer-driven contracts between services (gateway↔service drift guard)
Integr. │  per-service vs real Postgres/Redis/Kafka via testcontainers-go (in CI)
  Unit  │  Go per-service · Flutter widgets · Web Vitest + React Testing Library
        ▼  many, fast, run on every push
```

- **Unit** — pure logic, no network. Go (`go test -race`), Flutter (`flutter
  test`), **Web: Vitest + RTL** (new). Target: every service + every web
  feature module.
- **Integration** — one service + its real datastores, spun ephemerally with
  **testcontainers-go**; promote the existing build-tagged suite into CI.
- **Contract** — services call each other over HTTP; add consumer-driven
  contracts (Pact) or golden-response checks so a provider change can't silently
  break a consumer (esp. the gateway's identity-header contract).
- **E2E** — critical journeys through the whole system on an ephemeral stack
  (`stack.sh`): **Playwright** for web, Flutter `integration_test` for mobile.
- **Load/perf** — standardize k6, run nightly vs staging with pass/fail
  thresholds (p95 latency, error rate).

## 4. Workstreams (prioritized)

| ID | Workstream | Effort | Status |
|----|-----------|--------|--------|
| **W1** | Fix integration auth regression (real JWT minting) | S | ✅ done |
| **W2** | Web test foundation: Vitest + RTL + Playwright, wire into `frontend-ci` | L | next |
| **W3** | Integration-in-CI: testcontainers + ephemeral infra job; move build-tagged suite into the pipeline | M | |
| **W4** | Close Go unit gaps (9 bare services) + coverage threshold gate | M | |
| **W5** | E2E smoke: login→post→feed, chat send/receive, payment happy-path | M | |
| **W6** | Standardize k6 load + nightly thresholds vs staging | M | |

### W2 — Web test foundation (recommended next)
- Add `vitest` + `@testing-library/react` + `@testing-library/jest-dom`; unit-test
  pure logic first (`src/lib/api.ts` interceptor, response mappers, the new
  `/admin/access` console logic).
- Add `@playwright/test` for E2E; first specs: login, create post, admin role
  grant/revoke (the RBAC console).
- Extend `frontend-ci.yml` with a `web` job: `bun install` → `vitest run` →
  `playwright test` (against a preview build or the ephemeral stack from W3).

### W3 — Integration in CI
- `testcontainers-go` to boot Postgres + Redis + Redpanda per suite (fast, no
  full compose). For cross-service flows, a GitHub Actions `services:`/compose
  step bringing up the minimal set + `ATPOST_RUN_INTEGRATION=1`.
- New `integration` workflow (or job in `go-ci`) running `go test -tags
  integration` with the env wired to the ephemeral stack.

### W4 — Unit coverage gaps + gate
- Add unit tests to the 9 untested services (start with money-path: payments,
  wallet, bill-pay; then qa, channel, community, etc.).
- Add `-coverprofile` to `go-ci` and a soft threshold (e.g. fail under 50%,
  ratchet up) as a required check.

## 5. CI/CD wiring
Build on the existing Actions, don't replace:
- `go-ci`: add coverage profile + gate (W4).
- `frontend-ci` → add a `web` job (W2).
- new `integration` workflow (W3) — PR + nightly.
- `security.yml`: keep as-is (govulncheck/gosec/trivy).
- E2E + load run **post-deploy against staging** (ArgoCD stays the deploy path).
- Make unit + lint + integration **required checks** before merge to `main`.

## 6. Sequence
W1 ✅ → **W2** (biggest hole) → W3 (continuous cross-service safety) → W4
(coverage gate) → W5 (journey smoke) → W6 (perf). W3 and W4 can run in parallel
once W2 lands.

## Doorstep fixed-price assignment (October 2026)

The new flow is controlled by `DOORSTEP_AUTOMATIC_ASSIGNMENT_ENABLED=true`
on doorstep-service. It is **off by default**. Deploy the matching web changes
and migration `016_automatic_assignment.sql` before enabling it. Existing
customer-selected bookings retain their original assignment mode.

The customer selects a service, address and available appointment time. The
server uses the active administrator-set city price, never a partner-entered
price. A request enters `matching` without a payment intent or calendar hold.
Eligible partners with the lowest weekly workload receive short-lived offers.
The first valid acceptance acquires the booking/calendar lock and opens a
ten-minute payment window. Payment capture confirms the assignment; a late
capture requests a refund instead of reviving an expired slot. A paid booking
that needs a replacement keeps its original price and never charges again.
If no replacement accepts before the deadline, it is cancelled for a full refund.

Local verification:

- Run `go test ./...` from `Architecture/services/doorstep-service`.
- Set `TEST_PG_DSN` to a **disposable** PostGIS database and run
  `go test ./internal/itest -count=1`. This suite resets its database schema;
  never point it at the application or partner-demo database.
- The automatic-booking integration tests cover concurrent acceptance,
  administrator prices, no payment before acceptance, offer ownership,
  eligibility changes, matching expiry, replacement, late-payment refunds,
  workload fairness and worker recovery.
- In the web repository, run
  `bun test src/features/doorstep src/features/maps src/lib src/hooks src/features/partners`
  and the production build. Browser layout checks used the actual partner
  components with fictional fixtures at 1440, 390 and 320 pixels; this is not
  evidence of a signed-in full-stack browser payment test.
- Notification consumer/service tests cover review decisions, partner offers
  and customer booking/payment updates. The web inbox handles persistence,
  unread state, paging and reconnecting event streams. SMS is not added.

### Fictional AC partner fixtures

`go run ./cmd/devseed --test-partners` requires explicit development mode,
`DOORSTEP_TEST_FIXTURES=true`, and `POSTGRES_DSN` pointing to the exact
**loopback-only** database `doorstep_partner_demo_test`. It refuses other
databases, remote hosts, fallback hosts and a dataset containing real partners.
The seed is repeatable and does not overwrite administrator edits.

Two clearly labelled fictional partners cover AC service, gas refill, repair
and installation. The seed provides test coverage, weekly availability and
admin-set test prices. Approvals are explicitly simulated and audited; no real
identity documents, bank accounts, phone numbers or authentication accounts
are fabricated. The generated portraits and profile IDs are recorded in
`output/doorstep-test-partners/manifest.json`.

At this checkpoint, fixtures are in an isolated local database only. They are
**not portal logins**, and the portraits have not been uploaded to a media
service. Before an interactive user test, connect a separate authenticated
test stack and sandbox payment provider to this database and upload these
labelled fictional portraits there. Do not route the public portal or any real
payment provider to this fixture database. The current temporary QA container
has no persistent volume; its fixtures can be recreated with the guarded seed.
The ambiguous "home cooking installation" fixture request remains unseeded
until its service scope is confirmed.

### Deployment checkpoint — 10 October 2026 (Asia/Kolkata)

The existing local Docker/tunnel deployment was updated on explicit request.
Only `web`, `doorstep-service` and `notification-service` were recreated, with
the existing four Compose files. Admin, identity, payments and infrastructure
containers were not restarted. Both public domains loaded in the browser;
partner sign-in was displayed, but no signed-in partner mutation or payment
was performed. Customer discovery could not be checked because browser location
timed out. Web health and backend readiness (Postgres, Redis and Scylla) passed.
Migration `016_automatic_assignment.sql` is recorded in the application database.

**Automatic assignment remains disabled.** The deployment preflight found zero
active administrator prices and zero approved partner prices (12 proposals
are pending). No prices were invented or approved during deployment. Populate
`city_prices` through Admin before enabling the flag and verifying a real
authenticated booking with the intended payment environment. The application
database contains none of the fictional partner fixtures.

Deployed image IDs:

- web: `sha256:589ce02c51e0d4c4e17d42bb2bd1691cf496b28fba2bb468cc97c8bad8a32609`
- doorstep: `sha256:6769af58948f74ed4492b0225f0d9815827af3917e708fb266b31518d8ac0fd2`
- notifications: `sha256:4470fee75cf96e6047cb6ae6b2ec8291aa72d5cb2b7bf6c560888761e9e98201`

The previous images are retained under `atpost/web-rollback`,
`atpost/doorstep-rollback` and `atpost/notification-rollback`, each tagged
`20261010-before-fixed-price`. A pre-migration Doorstep-only data dump is in the
gitignored `.secrets/deployment-backups/doorstep-before-fixed-price-20261010.dump`.
It contains private data; do not commit or publish it. No rollback or restore
was performed. The notification startup also reports that incident safety
responders are not configured; ordinary review/booking inbox updates do not
constitute verification of that separate emergency escalation path.

### Payment progress and customer contact — 2026-10-10 (not deployed)

The customer's received-payment and accepted-assignment milestones now render
as completed checks; the next waiting step is named explicitly. A reserved
professional on a paid, still-offered booking is labelled awaiting acceptance.
No payment or assignment records were changed by this fix.

Partner Jobs now fetches `GET /pro/jobs/{id}/customer-contact` only on request.
It resolves the customer's verified mobile through identity-auth and permits
only the approved, currently accepted lead on an active paid booking. Responses
are private/no-store; access is rechecked after the identity call. Offers, job
lists and events still contain no phone. Missing/unverified contact fails closed.
The UI clears the displayed number after 30 seconds or window blur and discloses
booking-related phone sharing on customer checkout and booking details.

Verification: 438 web tests; production web build; all Doorstep Go packages,
including the isolated `doorstep_it_test` database suite, passed. The contact
integration test covers other users, unaccepted and closed assignments,
inactive bookings, suspension, private responses and list/offer non-disclosure.
No live call, payment or partner acceptance was performed. Deploy web and
doorstep-service together when requested; no new migration or admin change.

### App-only incoming Doorstep requests — 2026-10-10 (not deployed)

The signed-in Android partner shell now shows an incoming request across tabs,
with service, locality, slot, estimated earnings, a server-deadline countdown,
and Accept/Decline. Home's request list shares the same live state. Foreground
notification-tone ringing respects notification permission, channel silence,
silent mode and DND, stops on dismissal/response/expiry/background/logout,
and is capped at 30 seconds. This is not a phone call or full-screen alarm.
Background FCM offers are data-only, high-priority, deadline-checked notifications;
they time out automatically and an expired-offer push clears rather than rings.
Web notification/inbox behavior is unchanged; no web ringing UI was added.

Automatic assignment now has ONE live recipient per booking and a 30-second
server-owned response window. Decline immediately attempts the next eligible
professional; timeout advances on the five-second worker cadence. Existing
workload/offer-count fairness, booking locks, eligibility checks, payment gates
and historical customer-selected booking behavior are preserved. Expiry and
decline emit the offer-closed event. Concurrent worker ticks and stale accepts
are covered by isolated database integration tests.

Verification: all Doorstep Go packages (including `doorstep_it_test`) passed;
notification push/event/service suites passed; 53 Doorstep Pro and 42 shared
notification Android unit tests passed. `:app-doorstep-pro:assembleDevDebug`
passed. No device installation, live job acceptance, payment or deployment.

Background delivery is NOT verified: `app-doorstep-pro/google-services.json`
is absent (the customer app's Firebase file does not configure this app).
Supply the matching partner app Firebase configuration and rebuild before a
physical-device foreground/background/silent-mode test. Server Firebase settings
are present, but were not credential-tested. Automatic assignment remains OFF
on the running service; this change does not alter the live feature flag or
administrator service prices. Deploy backend/notifications with the updated
partner app, after configuration and rollout approval. No new migration.

#### Firebase configuration follow-up — 2026-10-10

The user supplied the gitignored `app-doorstep-pro/google-services.json`.
It matches project `us-unified-services` and includes `com.us.doorstep.pro.dev`.
Google Services processing generated the matching `google_app_id` resource.
The partner APK was rebuilt successfully using
`-PdevApiUrl=https://cleestudio.com -PdevWsUrl=wss://cleestudio.com` so a physical
phone does not depend on the emulator-only `10.0.2.2` address. The unauthenticated
public partner API check returned JSON HTTP 401, confirming reachability without
bypassing sign-in. Android unit tests: 53 partner + 42 notification, zero failures.

Artifact: `mobile/android/app-doorstep-pro/build/outputs/apk/dev/debug/app-doorstep-pro-dev-debug.apk`.
No Android device was connected, so no installation, FCM token registration,
background delivery, audible ring, or live request acceptance was verified.
No backend deployment or automatic-assignment feature-flag change was performed.

#### Physical-phone notification probe — 2026-10-10

The user installed the APK and confirmed readiness for the test. They later
clarified that the Motorola app was OPEN, not in the background. A read-only
check found exactly one active Android `doorstep_pro`
registration. No token or private key was printed or written to a probe file.
The opt-in `TestLiveDoorstepNotificationProbe` (`livefcm` build tag) sent exactly
one labelled "TEST ONLY - Doorstep alert (no booking)" notification using the
existing `firebase-adminsdk-fbsvc@us-unified-services.iam.gserviceaccount.com`
identity. FCM accepted it at `2026-10-10T07:22:54Z` (12:52:54 IST).

The probe uses the production FCM adapter's data-only offer path, a 30-second
expiry/TTL, a unique test notification subject, and a Home deep link. It does
not create a booking, offer, assignment or payment. The opt-in guard was also
verified to skip without consent. Provider acceptance verifies credentials and
send authorization, NOT phone display or sound. The user reported no alert.
Code inspection explains this foreground test limitation: offer pushes are
suppressed while the Activity is visible; the incoming-request host instead
requires an authoritative server offer, which this non-booking probe does not
create. This is not evidence of a background FCM delivery failure. Background
display/sound still require a repeat with the app genuinely backgrounded.
No backend deployment or automatic-assignment flag change was made.

Background follow-up: after the user confirmed the app was closed, FCM accepted
one further identical test at `2026-10-10T07:32:27Z` (13:02:27 IST). The user
confirmed receiving it, then requested the same notification visibility while
the app is open. No real booking or request was created by either probe.

#### Foreground partner notifications — 2026-10-10

Removed the blanket foreground suppression for Doorstep offer notifications.
An unexpired push now posts on `doorstep_pro_offers` whether the app is visible
or backgrounded, subject to Android permissions and channel settings. A shared
singleton sound coordinator ensures only the first arrival (push or live
request card) owns sound for the offer lifetime. A later push stays visible but
silent; a later request card retains Accept/Decline without starting a second
ringtone. The card clears the matching alert when handled/dismissed. Expiry,
private lock-screen visibility and foreground chat suppression remain intact.

The partner push receiver also accepts notification-envelope title/body from
older server builds when data fields are absent. This fallback is restricted
to `doorstep.pro.*`; it cannot import message content into chat notifications.

Verification: 59 notification tests (including foreground/background rendering,
sound ownership/races, duplicates, expiry, denied permission and both payload
formats), 53 partner tests and 6 affected chat regression tests passed with no
failures/skips. The HTTPS dev APK rebuilt successfully at 13:13:26 IST; it still
uses `com.us.doorstep.pro.dev` and the existing Firebase configuration. Generated
Hilt wiring confirms the request card and presenter share the sound coordinator.
The user must install this updated APK before foreground physical-device testing;
that test is not yet verified. No web changes, backend deployment, real booking,
payment or automatic-assignment feature-flag change was made in this follow-up.

#### Unified Partner notification integration — 2026-10-11

Implementation plan and status:

1. **Implemented: unified worker delivery.** The PostgreSQL audience lookup and
   all Food, Moped and Doorstep send-loop guards agree that an `app=partner`
   registration receives `feast_rider`, `mopedu_captain` and `doorstep_pro`
   events. Restaurant, seller and customer audiences remain excluded.
2. **Implemented: missing admin decisions.** Food Delivery account decisions
   and document reviews enqueue routing-only events in the decision's database
   transaction. Moped document/vehicle reviews, rejection, suspension/blocking,
   document expiry and subscription-payment rejection have worker push types
   and inbox entries. Expiry messages now carry the identity user ID instead
   of addressing the local partner record ID. Push copy excludes review notes,
   document contents and personal identifiers.
3. **Implemented: Android navigation and refresh.** Notification-tray and
   inbox taps use one worker router. Home Services job alerts open the job;
   Food Delivery document/account reviews open their review screens; Moped
   account and payment reviews open onboarding or plans. The visible Partner
   shell refreshes alerts and business summaries on worker pushes and polls
   its inbox every 30 seconds only while visible. These are refresh hints, not
   trusted approval or payment state. Customer Doorstep dispatch-unavailable
   and professional-changed types were added to the Android route registry
   and booking-update channel.
4. **Validated locally; rollout pending.** Notification service/event/provider
   tests and Food/Rider unit suites pass. The new Food transaction and rollback
   tests plus the existing manual-identity test pass on the isolated
   `food_notifications_20261011_test` database in the local QA container.
   Fixtures contain no real partner information; live records were not changed.

Validation boundaries:

- Partner Android tests cover exact notification destinations, recipient-scope
  exclusions, review channels, foreground/background rendering, duplicate and
  expired offers, and declined notification permission. JVM rendering tests
  are not proof of delivery or sound on a physical phone.
- The full Customer Android test task is blocked by the existing unresolved
  `FaceArConfig` dependency in `:app:kspDevDebugKotlin`. No unrelated Face AR
  changes were made to force a green build.
- Fish Delivery and Commerce Delivery still lack their own job backends.
  They have not been misrepresented as working food/merchant notifications.
  Moped's public-access flag remains unchanged (disabled).
- Outage-safe replay is **not complete**: Food's new producer events use the
  durable outbox, but existing notification push consumers claim before send,
  and Rider review publication is still post-commit/best-effort. A provider
  outage or a process failure can still lose an alert; foreground inbox
  refresh cannot recover an inbox event that was never persisted. Durable
  consumer/device retry requires a separate implementation and fault tests.
- No backend deployment, installation, real push probe, ride, order, payment
  or approval change was performed for this notification implementation.

Final local verification: 304 Android test cases across the Partner host (30),
shared notifications (66), Food Delivery (95), Moped (54) and Home Services (59):
303 passed, zero failed, one pre-existing Moped contract-fixture test skipped
because its recorded backend fixtures are not published. The Partner
`:app-doorstep-pro:assembleDevDebug` task succeeded. APK version `0.4.2.dev`
(code 6), package `com.us.doorstep.pro.dev`, uses `https://cleestudio.com` and
`wss://cleestudio.com`. Output:
`mobile/android/app-doorstep-pro/build/outputs/apk/dev/debug/app-doorstep-pro-dev-debug.apk`.
SHA-256: `D1C8382A601C602FE6D79CC5865B4204B0B781F9102EAEA20E83C260C6B665AD`.
This is a local development APK, not a production-store release or a deployment.

#### Four-category Partner implementation — 2026-10-11

This section supersedes the earlier partial-implementation notes above.
Implementation and local validation are complete as described below; production
rollout and physical-device/provider acceptance are **not** claimed complete.

Implemented:

- Mobile Partner has exactly Moped (passenger rides), Home Services (technicians),
  Commerce Delivery and Food Delivery, with Home / Work / Alerts / Account.
  Fish selections are discarded, not converted; merchant/kitchen/product screens
  are excluded. Existing registrations and approvals survive refresh failures.
  Network failure, missing registration, review and approval remain distinct.
- Relevant application, secure upload, review, requests, current work, history,
  earnings and support paths are integrated. Selfie is mandatory; introduction
  is optional and limited to one minute. Commerce evidence review uses owned,
  ready media and audited authenticated image/video readback. Merchant approval
  cannot grant delivery-partner approval.
- Shared Rider-hosted work coordination now guards Moped, Food and Home domain
  duty/offer/assignment writes in the shared application database. Commerce uses
  authenticated remote coordination. App readback is authenticated through
  `/v1/partner/work-session`. Revisions prevent stale second-device changes;
  accepted work and existing scheduled Home reservations prevent conflicts.
- Sequential fair offers use 20-second absolute deadlines. Expired offers cannot
  be accepted or replayed as fresh requests. Late decline, duplicate acceptance
  and ambiguous remote results cannot clear another winning reservation.
- Commerce migrations 040–042 provide separate profiles, document review,
  explicit coverage/seller opt-in and payout configuration, immutable routing
  and payout snapshots, availability/location, offers, jobs, verified pickup and
  delivery, failure/support handling and one-time earnings. No partners, areas
  or amounts are seeded. Existing couriers and customer shipping charges remain.
- Local prepaid per-seller shipments wait for seller readiness. Assignment is
  not shipping: actual verified pickup updates tracking and emits customer
  shipping notification; delivered-order notification waits for aggregate
  completion across all active seller shipments. Unassigned jobs stay visible.
  Existing shipments are unchanged; routing cannot silently book a second courier.
- Existing customer/seller mobile and web order screens support local tracking,
  explicit handover-code reveal and optional precise address-pin confirmation.
  Codes are not placed in notifications and clear when backgrounded. This does
  not add merchant workflows to the Partner app.
- Admin web includes delivery applications/evidence review, areas, payouts,
  seller participation, jobs and support. Signed proxies enforce permissions,
  review revisions and appropriate step-up checks. Notification operations show
  sanitized failures and audited retry of failed non-offer messages only.
- Supported application decisions, corrections, offers, assignment/lifecycle,
  completion, earnings and support notifications persist transactionally in
  domain outboxes and a durable notification delivery queue before Kafka ACK.
  Queue leases, per-device receipts, bounded batches/retries, suppression-error
  recovery, provider throttling and absolute offer expiry are tested. Manual
  retry preserves receipts and never replays an offer. Customer/merchant notices
  retain their existing notification policy, outside this Partner queue.
- Commerce foreground location sharing starts only after explicit on-duty
  consent and existing location permission; it has a persistent notification
  and stops on sign-out/category switch. No background-location permission was
  added. Stale/paused location is visible and excluded from matching.

Local validation:

- Android: **718 tests, 717 passed, 0 failed, 1 pre-existing skipped Moped fixture**.
  Unique module counts: Partner 51, notifications 70, Food rider 95, Moped 63
  passed + 1 skipped, Home Services 59, Food core 46, Commerce core 111,
  Commerce shared UI 12, customer 91 and seller 119. Strict fixture parity and
  contract decoding remain enabled.
- Admin web: **511 tests passed**; Commerce web: **186 tests passed**.
  Both TypeScript checks pass. Windows standalone packaging hit EPERM symlinks;
  both unchanged project Dockerfiles subsequently built successfully in an
  isolated Linux context, including standalone tracing and final image export.
  Local QA-only images (not deployed): `atpost/qa-partner-admin:20261011`
  (`sha256:c2edfc5fe82dcd6f645ccfdc390d76071d9ec14e7af8d834d4690f4a7c747368`)
  and `atpost/qa-partner-commerce:20261011`
  (`sha256:bfb9bd04e2dfe670d67a238f0a846b2970b77d55293d45bf705eecae3d40af04`).
  Isolated, network-disabled, read-only runtime checks returned HTTP 200 for
  Admin `/api/health` and `/login`, and Commerce `/shop/api/health` and `/shop`.
  Temporary QA containers and the copied build context were removed; images
  remain local. No running application services or source files were replaced.
  Commerce has one non-blocking effect-cleanup lint warning for the intentional
  invalidation of pending private handover-code reveals on unmount.
- Commerce service/store/HTTP/server suites pass, including **12 real PostgreSQL
  delivery tests** covering ownership, reviews, evidence, concurrent acceptance,
  expiry, transaction rollback, seller readiness through verified delivery,
  payout idempotency, mixed-seller tracking, cancellation, routing and support
  resolution/customer lifecycle events.
- **16 shared-coordinator real-database tests** pass, plus domain service/store,
  authenticated HTTP and gateway routing tests. Existing scheduled reservations,
  concurrent duty changes and cross-category reservation conflicts are covered.
- Notification queue: **10 tests (8 database + 2 unit)** pass, including outage
  recovery, suppression lookup recovery, bounded multi-device batches, duplicates,
  expiry, lease fencing and audited non-offer retry. Notification events/service/
  HTTP/push/store suites and server compile pass. Admin HTTP/service/server,
  signed-token evidence and operations permission tests pass.
- Database tests use isolated local QA databases only:
  `commerce_delivery_20261011_test`, `partner_dispatch_20261011_test`,
  `partner_notifications_20261011_test`, `food_notifications_20261011_test`.
  Push providers are fake; no live payment, order, notification or approval made.
- Fresh rendered small-screen/large-text evidence is under
  `mobile/android/app-doorstep-pro/build/outputs/partner-ui-checks/`.
  These are automated rendering checks, not physical-phone acceptance.
- APK: `mobile/android/app-doorstep-pro/build/outputs/apk/dev/debug/app-doorstep-pro-dev-debug.apk`.
  Version `0.5.0.dev`, code 7, package `com.us.doorstep.pro.dev`, 25,026,797 bytes.
  SHA-256: `53A843BD4324AE9B7B351528E73CA473F3DBD9041CE6C5A290FA5BEA884B28E7`.
  Existing signing/Firebase identity retained. Targets `https://cleestudio.com`
  and `wss://cleestudio.com`; new backend APIs must roll out before installing
  this against production. This is a development APK, not a store release.

Rollout gates and remaining external validation:

1. No production deployment or feature activation performed. Rider is the shared
   schema authority: migrate Rider first, then Food/Home guards; inspect legacy
   reservation conflicts. Rider/Food/Home must share the same application DB.
   `PARTNER_WORK_COORDINATION_ENABLED` remains off until reviewed activation.
   Turning the flag off does not remove SQL triggers; rollback requires an
   explicit reviewed migration, not an assumption that the flag undoes schema.
2. Commerce requires migrations 040–042, authenticated `PARTNER_WORK_URL` and
   `INTERNAL_SERVICE_KEY`, plus explicit `COMMERCE_LOCAL_DELIVERY_ENABLED`
   activation, configured coverage/payouts and seller opt-in. No live defaults
   were invented. See `Architecture/services/commerce-service/DELIVERY_OPERATIONS.md`.
3. Notification migrations 015–016, running outbox/queue workers, and the admin
   signing public key with its matching key ID are required for operations UI.
   Optional web map search requires the existing public Maps browser key.
4. No phone is attached and no emulator was running. Physical foreground/
   background ringing, denied permissions, interrupted uploads, notification
   tap/navigation and two-device interactive acceptance need a device run.
   Do not equate automated concurrency/rendering tests with these checks.
5. Real payment-provider/courier end-to-end and broker restart/rebalance staging
   checks remain. Verified local DB scenarios do not prove external providers.
   FCM delivery is at-least-once across the provider-acceptance/receipt crash
   window; deterministic identities/collapse keys reduce, not eliminate, repeats.
6. Existing Moped subscription grace/auto-renew cron publishers are legacy direct
   side effects outside the implemented work/application events; this work does
   not claim every legacy publisher is transactional.

No APK installation, production migration/deployment, local-delivery activation,
public-access change or fictional partner creation was performed.

#### Partner backend and admin deployment — 2026-10-11

The user subsequently authorized backend/admin deployment. This entry supersedes
the **not deployed** statements above for the following components only. The
target is the existing local Docker stack exposed by the configured Cleestudio
test tunnel, not a new production-hardened host or store release.

- Built and deployed Rider, Food, Doorstep, Commerce, notifications, admin-service
  and API gateway. Deployed the previously validated admin web image to
  `https://admin.cleestudio.com`. Customer/merchant web images were not changed.
- Restored the required existing database, queue, object-storage, login, upload,
  payment, account and WebSocket dependencies, which were stopped before this
  deployment. Restarted the existing tunnel hidden, using its unchanged ingress
  configuration; no DNS or public-access settings were changed.
- Retained rollback images as `atpost/rollback-<service>:before-partner-20261011`
  and `atpost/rollback-admin-web:before-partner-20261011`. Private application and
  Commerce database dumps, plus the previous Compose environment, are under
  `.secrets/backups/partner-20261011/` (gitignored). Dump catalogues verified.
- Preflight returned zero multi-category duty conflicts and zero overlapping
  accepted jobs. Rider, Food and Home target the same `app` database (Home uses
  its own search path). Rider applied shared work migrations 001–003 first;
  Food/Home installed their transactional guards afterward. Rider migration 009,
  Commerce 040–042 and notification 015–016 are applied.
- Shared duty coordination is enabled for Rider/Food/Home. The authenticated
  Commerce coordinator URL is configured, but **local Commerce dispatch remains
  disabled**. Zero enabled Commerce areas; no payouts, seller participation,
  fictional partners, approvals or jobs were inserted by the deployment.
- Reusable Compose override: `Architecture/docker/docker-compose.partner-rollout.yml`.
  Use it after the base, admin-local, google-routing.local and partner-local
  files with project `atpost_stack`. Preflight query:
  `scripts/partner-rollout-preflight.sql`. Do not omit the rollout override on
  later recreation; do not assume switching off a flag removes SQL guards.

Deployment checks passed:

- Rider/Food/Home/Commerce/notification readiness returned healthy dependency
  status. Admin readiness passed with its required internal-service header;
  gateway readiness returned HTTP 200. All seven upgraded containers had zero
  restarts at the final check.
- Public admin `/login` and `/api/health`, and
  `https://cleestudio.com/v1/auth/health`, returned HTTP 200.
- Signed-out `/v1/partner/work-session` and `/v1/commerce/delivery/profile`
  returned HTTP 401 (not an upstream failure or an unprotected endpoint).
- Read-only calls through admin-service's existing permission gate returned
  HTTP 200 for `/v1/admin/me`, Commerce delivery profiles, areas, jobs and support,
  and notification partner-jobs. No approval/configuration mutations were tested.
- Firebase push is configured for the existing `us-unified-services` project.
  No synthetic request or test push was sent; phone foreground/background
  delivery still requires the user's physical-device test.

Existing configuration limitation: no staff responder IDs are configured for
Doorstep emergency safety paging (nor Dating paging). Those responder lists were
not invented or changed; emergency staff alerting is not claimed operational.
The host and hidden tunnel must remain running for phone access.

Partner APK checksum reverified, unchanged from the validated build:
`53A843BD4324AE9B7B351528E73CA473F3DBD9041CE6C5A290FA5BEA884B28E7`.
Use `mobile/android/app-doorstep-pro/build/outputs/apk/dev/debug/app-doorstep-pro-dev-debug.apk`.
No APK installation or Play release was performed. Real payment/courier tests
and explicit local-Commerce feature activation remain separate steps.

#### Partner notification actions — 2026-10-11

The user confirmed that the two earlier labelled FCM diagnostics arrived with
the installed Partner app both open and backgrounded. Those were notification
transport tests, not actionable job offers.

APK `0.5.1.dev` (code 8, same `com.us.doorstep.pro.dev` identity) now renders
Moped, Home Services, Commerce Delivery and Food Delivery offer names, Accept
and Decline notification actions, and an OS countdown to the original absolute
expiry. Delayed pushes do not restart the server's 20-second offer window.
Actions use distinct immutable PendingIntents into a non-exported Activity,
require unlocking on supported Android versions, fetch the authenticated open
offers, and submit only through the matching existing worker endpoint. A
confirmation screen reports success, expiry or failure; Open Work refreshes
the category. No merchant routes, new live feature flags or payment mutations.

Action tests cover all four categories and decisions, expired/withdrawn offers,
unauthorized/unavailable responses, duplicate/opposite taps, process recreation,
and practice requests never reaching live APIs. App and notification suites:
129 tests, zero failures/errors/skips. `moduleGraphCheck` and `assembleDevDebug`
passed. Build log: `mobile/android/build/partner-notification-actions-check.log`.
APK SHA256: `35A561134CE68A7E464F412DD9375642949A85678972692D9A5AEB94CD19024D`.

An interactive practice probe is prepared in `TestLiveDoorstepNotificationProbe`:
in addition to its existing explicit single-send opt-in, set
`FCM_LIVE_INTERACTIVE_TEST=true` and `FCM_LIVE_TEST_CATEGORY=home|moped|commerce|food`.
It sends one random UUID, `test_only=true`, original 20-second expiry and TTL,
using the category's existing FCM channel. The debug APK records a labelled
local practice response; release builds refuse it. No booking, payout, customer
contact, duty change or production offer is created. This verifies notification
interaction, not end-to-end live dispatch/payment. The probe compiles and skips
without explicit opt-in. Do not send until the user installs this APK and is
ready; no interactive push has been sent yet. Physical action/countdown testing
remains pending. Credentials and device tokens must stay in memory/private
credential files and never in logs.

#### Automatic in-app request comparison — 2026-10-11

APK `0.5.2.dev` (code 9, `com.us.doorstep.pro.dev`) opens a shared incoming
requests dialog automatically while the signed-in Partner workspace is resumed.
It shows category, service, location, authoritative amount and original expiry
with separate Accept/Decline buttons. Three cards fit the 400dp layout; the
320dp/150%-font layout scrolls to every action. The previous Home-only popup is
disabled inside this unified host. Foreground FCM feeds an in-memory inbox even
without notification permission; background notification actions remain intact.
Authenticated offer polling/readback is required for live decisions. Missing
amounts are not fabricated, network failures disable stale actions, and practice
decisions never call live booking APIs. The estimated Moped fare is labelled as
an estimate, not a guaranteed delivery payout.

Backend changes are prepared, **not deployed**: shared migration
`004_offer_choices.sql` permits three pending choices under the session row lock
and atomically withdraws other pending reservations on acceptance. One duty
category, accepted/active work conflicts and scheduled Home calendar holds remain
enforced. Rider/Food dispatch and readback support bounded choices; Rider
readback adds saved pickup/drop and estimated fare. Commerce migration
`043_delivery_offer_choices.sql` replaces the one-pending-offer-per-partner index
with a three-choice guard while preserving one offer per job and one active job.
No live coverage/payouts, customer prices or local-Commerce feature flag changed.

Validation: Android 196 tests (64 app, 73 notifications, 59 Home feature), zero
failures/errors/skips; assembleDevDebug and moduleGraphCheck passed. Rendered
`mobile/android/app-doorstep-pro/build/outputs/partner-ui-checks/incoming-three-requests.png`
was visually inspected. Isolated database suites passed: 18 shared coordination,
5 Rider sequential dispatch, 4 Food sequential dispatch, and 9 Commerce delivery
operations tests (including concurrent three-choice cap/one acceptance).
Home store/service packages compile. Rider's fresh scratch database first needed
its base setup.sql before versioned migrations; no live data was used or edited.

APK: `mobile/android/app-doorstep-pro/build/outputs/apk/dev/debug/app-doorstep-pro-dev-debug.apk`.
SHA256: `686A2349C3CC25CBB2886F2DE7FC10044B4DBB8942F39EE831AD0F73E8DC3805`.
Build log: `mobile/android/build/partner-inapp-requests-check.log`.

The preceding single Home Services practice push was accepted by FCM at
2026-10-11T13:11:01Z. No new multi-request batch has been sent. After the user
installs 0.5.2 and confirms readiness, `TestLivePartnerComparisonProbe` can send
three clearly labelled practice offers (Home/Food/Commerce) with independent
20-second deadlines. It requires explicit `FCM_LIVE_COMPARISON_CONFIRM` opt-in
and skips without it. These sample amounts are diagnostic payloads only, not
live payout configuration. Physical popup/actions verification, backend rollout
and real end-to-end multi-offer dispatch remain pending.

#### Stop offers after acceptance — 2026-10-11

User confirmed the repeated five-request practice burst arrived, then requested
that accepting one immediately stop the remaining and subsequently arriving
requests. APK `0.5.3.dev` (code 10) now shares a persisted live/practice busy gate
between the in-app dialog, notification presenter and notification action screen.
Acknowledged acceptance clears remaining cards and offer notifications of the
same mode; delayed offers and duplicate Accept actions are suppressed. Declining
still advances the queue. Account/safety/job-update notifications are not cleared.
Practice acceptance never marks a real account busy or calls a live decision API.
After hiding the practice result, use "Practice job accepted · End practice" to
reset the test; live availability cannot be reset through that control.

The live busy gate survives process recreation and is cleared only by successful
authenticated work-session readback. Reads begun before local acceptance cannot
undo it. Readback runs on notification hints and a three-second foreground
fallback; other-device UI propagation is network-dependent, not instantaneous.
Network failures do not mark a busy partner available. Accepted scheduled work is
preserved; new offers pause while any reserved/active work remains, rather than
merely excluding overlapping new offers. Trusted completion/cancellation release
and subsequent authenticated readback restore availability.

Backend changes are **not deployed**: shared migration
`005_pause_after_accept.sql` enforces the pause in domain transactions; OfferTx
and offer readback enforce the same policy. The notification worker checks the
installed shared authority immediately before sending each queued offer and
records `expired/partner_busy` instead of ringing an already-busy partner.
Provider messages already in flight cannot be recalled; the installed app's
busy gate suppresses them after local acceptance. Legacy deployments without the
shared authority retain legacy behavior; rollout must retain coordination flags.

Validation: Android 200 tests (65 app, 76 notifications, 59 Home feature), no
failures/errors/skips; APK build and moduleGraphCheck passed. Shared coordination:
20 isolated-database tests passed. Notification queue: 10 tests passed using a
fake sender and isolated `partner_notification_busy_test`, including all four
offer categories blocked while busy. No live test request was sent this turn.
Physical accept-then-late-arrivals testing remains pending installation.

APK: `mobile/android/app-doorstep-pro/build/outputs/apk/dev/debug/app-doorstep-pro-dev-debug.apk`.
SHA256: `4FE160120A5F9ADCDE907E9293E4E6D59E2871334CE899C85FD4E2328065D15F`.
Build log: `mobile/android/build/partner-accept-pause-check.log`.
