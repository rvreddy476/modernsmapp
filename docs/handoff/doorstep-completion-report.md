# Doorstep implementation completion

5 October 2026. Continued Claude's existing work from
`docs/handoff/doorstep-codex-handoff.md`; did not replace the booking model or
silently reassign the customer's chosen professional.

## Delivered

- **API:** A5 visit lifecycle, geo arrival, before/after/sealed-kit evidence,
  start/end OTP with attempt lock, no-show, extras decisions/withdrawal,
  authoritative signed payment capture, outstanding grace period and booking
  restriction, invoice tax split using the actual professional.
- **Aftercare:** participant conversations and read receipts, ratings, rework,
  support tickets, encrypted trusted contact, revocable/rotatable limited visit
  sharing, customer/pro SOS and unsafe exit.
- **Admin:** reviewed professional prices, needs-attention bookings, audited
  incidents/tickets/rating moderation, compute-only settlements, and step-up
  human GST-registration review. GST changes are refused during active bookings.
- **Web:** professional selection/ASAP/change choice, payment/visit panels,
  private evidence proxy, conversation pagination, support forms, trusted
  contact, shared visit page. Validation errors retain drafts.
- **Android:** customer professional selection and visit/aftercare screens;
  professional prices, visit evidence/OTP/extras, conversation pagination/read
  receipts, safety and earnings. Both application variants compile.
- **Media privacy:** new `doorstep_photo` scope and caller-authorized preparation;
  generic public media reads deny that scope. Doorstep authorizes participant
  access before proxying bytes. Public storage objects cannot be reclassified.
- **Notifications/permissions/contracts:** new events, reviewed-price scopes,
  strict route inventories and real-handler golden fixtures copied unchanged
  to consumers. No fabricated JSON for binary/204 endpoints.

## Changed-file map

Backend repository (`codex/module-01-02-launch-safety`):

- `Architecture/services/doorstep-service/`: migration 006, server workers,
  HTTP handlers/contracts, service/store/model/payment/media/tax code and tests.
- `Architecture/services/media-service/`: migration 027, private Doorstep photo
  handler, service/store implementation and privacy/integration tests.
- `Architecture/services/admin-service/internal/adminauth/` and
  `internal/http/`: price permission, BFF route mappings and step-up checks.
- `Architecture/services/notification-service/internal/{events,push,service}/`:
  Doorstep event/push wiring and registry contract tests.
- `Architecture/shared/gst/`,
  `identity-platform/services/auth-service/internal/permissions/`,
  `Architecture/docker/docker-compose.yml`: service tax families, permissions
  and the media caller's required preparation scope.
- `contracts/doorstep/{openapi.yaml,README.md}` and
  `docs/DOORSTEP-TAX-ADVISER-REVIEW.md`: current contracts and unresolved human
  tax decisions. This report and `doorstep-dev-readiness.md` are handoff outputs.
- `mobile/android/feature/{doorstep,doorstep-pro}/`: API/DTO/repository/UI tests
  and copied contract fixtures. Unrelated premium Android UI remains uncommitted.

Web repository (`codex/apps-ui-refresh`): `src/features/doorstep/`,
`src/app/doorstep/`, `src/middleware.ts` (public shared-visit page classification,
not a gateway launch-gate bypass).

Admin repository (`feat/commerce-catalogue-attributes`):
`apps/admin/src/components/doorstep/`, `src/lib/admin/doorstep*.ts`,
`src/lib/admin/contracts/doorstep/`, `src/lib/admin/sections.ts`.

Client commits: web `015fe11`, admin `14af69e`. The backend/Android commit
containing this report is on `codex/module-01-02-launch-safety`.
The scoped Git commits provide the exact changed-file inventory. No backend
outside the named Doorstep dependencies, unrelated Android redesign, executable
or `vchat-site/` content was intentionally included.

## Verification results

| Check | Result |
|---|---|
| Go build, vet and tests: Doorstep, admin, media, notification, shared GST and identity auth modules | PASS |
| Doorstep PostgreSQL integration, `go test ./internal/itest -count=1` | PASS, 55.643 s; `doorstep_it_test` only; migrations applied twice |
| Media real PostgreSQL `TestDoorstepPhotoScopeIntegration` | PASS, 0.273 s, same scratch database |
| Vendored build of Doorstep/admin/media/notification under Architecture | PASS |
| Web `bunx tsc --noEmit` | PASS |
| Web `bunx bun test src/features/doorstep` | 143 pass, 0 fail, 436 assertions |
| Web `bun run build` | PASS, production build |
| Console `bun run typecheck`, `bun run lint`, `bun run test` | PASS; 470 tests across 24 files |
| Android offline module graph, customer/pro unit tests, customer/pro DevDebug Kotlin compilation | BUILD SUCCESSFUL, 611 tasks; customer 42 and professional 43 tests, zero failures |

The final notification registry fixture was updated for its authoritative
38 types and conditional payload fields; its full build/vet/test rerun passed.
The professional chat's pagination label was corrected to "More messages";
the incremental offline pro test/compile check passed in 22 s (248 tasks).
All 308 JSON fixtures present in the four client fixture directories were
checked byte-identical against the backend handler fixtures.

### Guard mutation verification

Each mutation was restored byte-identical (hash compared). The named test failed
while its guard was disabled, then the restored suite passed. 37 mutations:

- Visit (10): `TestVisitStartOwnerGuard`, `TestVisitStartPhotoGuard`,
  `TestVisitSalonKitGuard`, `TestVisitStatusGuard`, `TestVisitSuspensionGuard`,
  `TestVisitArrivalRadiusGuard`, `TestVisitFinishPhotoGuard`,
  `TestVisitFinishPendingExtrasGuard`, `TestVisitCompleteUnpaidGuard`,
  `TestVisitOTPInputGuard`.
- Service (18): rating owner/completed; chat owner/window; safety owner/status;
  rework owner/window/child; extras owner/status/finished/salon/quantity/parts;
  GST verification/checksum/reason. Corresponding `Test*Guard` tests live in
  `internal/service/{aftercare,rework,visit_extras,admin_tax}_test.go`.
- Media (4): `TestDoorstepPhotoCallerGuard` separately for issuer and operation,
  `TestDoorstepPhotoShapeGuard` for unknown fields,
  `TestDoorstepPhotoPublicReadGuard` for public scope refusal.
- Database (5): `TestAdminTaxActiveBookingGuardAndAudit` active-booking lock;
  `TestExtraWithdrawDecisionGuard` proposed-only withdrawal;
  `TestDoorstepPhotoScopeIntegration` independently for owner, public storage
  key and passed moderation constraints.

Real database tests also cover the 15-minute outstanding boundary and suspended
professional worker opening customer choice, rather than automatic reassignment.

## Not verified / launch decisions still open

- **Not deployed.** No container builds/restarts, dev seeds, AWS changes,
  emulator, ADB, physical-device interaction or payout execution occurred.
- No live external DigiLocker, background-check, face comparison, payment
  provider, FCM/device/App Link or staffed SOS acceptance test was performed.
  Local contract, unit, build and isolated database tests do not prove these.
- No manual browser/device visual acceptance or accessibility audit was run.
- Founder must provision encryption/service keys, Firebase/App Links and staffed
  responders; approve tax-adviser decisions, commissions, fees, rework windows
  and public naming before enabling the pilot/launch.
- Registered-only services fail closed without reviewed GSTIN; unregistered
  salon and separate parts treatment await the adviser. Settlement tax handling
  remains a review item, and settlement computation is not payout authorization.
- Admin care queues currently cap at 100 rows (earnings detail at 1,000;
  totals remain authoritative); wider rollout needs pagination/scaling review.
- Visit evidence retention is Doorstep-owned; no new automatic retention/deletion
  job was added. Existing signed URLs cannot be retroactively revoked, hence
  refusal of previously public objects and the requirement for fresh private uploads.
- Signed-out capability sharing remains unavailable while Doorstep's gateway
  launch gate is closed. Do not open the gate merely to test sharing.

Founder setup and QA sequence: [doorstep-dev-readiness.md](doorstep-dev-readiness.md).
