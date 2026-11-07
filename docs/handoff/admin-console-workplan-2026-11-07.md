# Admin console work plan (7 Nov 2026)

Read-only review of the admin backend, the admin web console, and the
gateway rules that sit in front of them. Every finding below was traced in
code and cites `file:line`. Paths are relative to `C:\workspace\modernsmapp`
unless they start with `apps/admin` or `packages/` (those are in
`C:\workspace\atpost-web-ui`).

## 1. Prompt for the agent doing the work

```text
You are working on the admin console of the modernsmapp platform. Work from
this prompt and the document it came from:
C:\workspace\modernsmapp\docs\handoff\admin-console-workplan-2026-11-07.md
(sections 3 and 4 hold the detail for each task below).

TREES
- Backend (Go): C:\workspace\modernsmapp, branch codex/module-01-02-launch-safety.
  admin-service: Architecture/services/admin-service
  monetization-service: Architecture/services/monetization-service
  payments-service: Architecture/services/payments-service
  feature-flag-service: Architecture/services/feature-flag-service
  api-gateway: Architecture/services/api-gateway
  identity (auth-service): identity-platform/services/auth-service
  Deploy values: deploy/services/<service>/values-*.yaml
  Dev compose: Architecture/docker/*.yml
- Admin web console: C:\workspace\atpost-web-ui, folder apps/admin,
  branch feat/commerce-catalogue-attributes. Bun only (bun@1.3.14).

RULES (from docs/harness/AGENT-RULES.md; read that file first)
- No git writes: no commit, add, rm, reset, stash, checkout, push. The founder
  or lead commits. Reading git is fine.
- Never print, paste or log a secret, token, password, OTP or key, even in a
  test failure. Never invent a key. Where a task needs a key, stop and say so.
- Do not touch .env* (the .example templates excepted), local.properties,
  google-services.json, .secrets/, tmp/, output/, vchat-site/.
- Do not start Docker, a database or a server. Do not deploy, rebuild a dev
  container or change AWS. Integration tests run only in CI on databases whose
  name ends in _test.
- No production. No staging changes. You edit code and values files only.
- Take money only through payments-service on its signed event.
- One agent per folder. Before you edit a file, run `git status --short` on
  it. If it is already modified or untracked and you did not do it, it belongs
  to someone else: stop and report. Known on 7 Nov: the customer-facing fix
  lanes (lead: Claude) are changing identity-platform auth-service,
  notification-service, wallet-service, commerce-service, payments-service
  (including migration 018_refund_required_worker.sql), media-service and
  post-service. Wait until those are committed, then pull.
- Checks, exactly these, no home-made build lines:
  Go while working:   scripts/verify/go.sh --fast <module-dir>
  Go before report:   scripts/verify/go.sh --full <module-dir>
  Web (admin):        cd C:\workspace\atpost-web-ui\apps\admin; bun run lint;
                      bun run typecheck; bun run test
- Every new guard (auth gate, money rule, permission, validation) gets a
  mutation check: break the logic on purpose with scripts/harness/mutate.sh,
  watch a named test fail, let the script restore the file. A mutation that
  does not compile proves nothing. Report every survivor with its reason.

TASKS, IN ORDER. Do not start a task marked [FOUNDER] until the founder has
answered the decision named. Each "done" line is the bar.

T1  Close monetization's legacy admin routes. [FOUNDER D8 first: delete, or 410]
    monetization-service/internal/http/handler.go:158,161,164-170,215-236 are
    gated only by hasAdminScope (fraud_handler.go:30-37, used by getAdminID
    :44-70 and adminActor admin_token.go:330-340): any token whose scopes say
    `admin`, no per-action permission, no step-up, no second approver, no
    admin-service audit row. The console already uses the token family.
    Done: every legacy path (including PATCH /disputes/:id and POST /refunds,
    which are NOT under /admin/) answers 410 or 404 even with an admin scope
    and MONETIZATION_WRITES_ENABLED=true; the token family still works; the
    January runbook (docs/runbooks/creator-fund-january-2026-remediation/
    remediate_january.sh:116,216) is rewritten to the token family or marked
    retired as D8 says; stale comment api-gateway/cmd/server/main.go:846-851
    fixed; mutation: re-registering one legacy route makes a named test fail.

T2  Wire the admin-service signing key and SERVICE_CALLERS in values files.
    [FOUNDER D4: the founder generates the key and puts it in each secret store;
    you never see it.] Today ADMIN_SERVICE_TOKEN_KEY/KID are commented in every
    admin-service values file (e.g. deploy/services/admin-service/
    values-prod.yaml:60-61) and SERVICE_CALLERS is commented in 16 product
    values files, qa-service among them (values-prod.yaml:68-69). Without the
    key every product route answers 503 (admin-service/cmd/server/main.go:
    123-130). Done: for the environments D4 names, the externalSecret lines and
    env lines are uncommented together, in the boot order the comments give;
    qa-service OPS keeps qa:topics.manage; a short table in your report lists
    each service and env you changed.

T3  Give payments.refund_required rows a resolve path. [FOUNDER D5]
    Rows are inserted at payments-service/internal/store/postgres/p0.go:
    495-515 and only counted at admin.go:237-330; nothing writes resolved_at.
    The automatic refund WORKER is being built by the customer-facing
    payments lane (7 Nov, migration 018); do not build a second one. Your
    part is the console side: show parked/refused rows and their history.
    Check what the payments lane committed before starting. Done: the worker refunds the
    second/late payment under an idempotency key from the row id, the signed
    refund webhook resolves the row, a permanently refused refund is parked and
    shown in the console; tests cover claim/lease, idempotency, webhook resolve
    and "never touches the intent"; mutations on each.

T4  Record and limit the bootstrap superadmin. [FOUNDER D2]
    One superadmin was granted by SQL with no auth.admin_audit row (table at
    auth-service/database/setup.sql:191-200). With one TOTP holder, every
    two-person route runs alone (admin-service/internal/approvals/
    approvals.go:227-233). Done: identity writes one audit row at boot for each
    superadmin that has no grant row in admin_audit (granted_by NULL or env
    allowlist), never twice; the console shows a banner while any two-person
    permission has no second holder; tests + mutation.

T5  Make money-role grants two-person and stop sock-puppet approvals. [FOUNDER D3]
    Only superadmin grants are two-person (admin-service/internal/http/
    handler_access.go:184,197). An admin with roles.manage can grant a second
    account a money permission alone, then approve their own requests from it.
    Done: grants and revokes of any role holding a two-person permission are
    two-person; an approver whose role was granted by the requester inside the
    D3 window is refused (identity has granted_by, setup.sql:106); tests +
    mutation for each rule.

T6  Close the payments "test_data" bypass.
    admin-service/internal/http/handler_payments.go:348-350 makes a
    test_data resolution single-person, and payments-service never checks the
    command really is test data (store/postgres/refund_attention.go:51). Done:
    test_data is refused unless payments confirms the intent belongs to a test
    application or test-mode provider, else it is two-person like the others;
    tests + mutation.

T7  Put feature flags and mini-app status behind the admin gate.
    The gateway sends /v1/admin/flags straight to feature-flag-service
    (api-gateway/cmd/server/main.go:470), which checks only a global admin
    scope (feature-flag-service/internal/http/handler.go:66-76). admin-service
    PATCH /v1/apps/:id/status does the same (admin-service/internal/http/
    handler.go:498-499). Done: both are declared through admin-service's gate
    with their own permission, step-up on writes, and an audit row; the direct
    gateway route is removed; the permission is added to identity's catalogue
    and admin-service's mirror; tests + mutation.

T8  Write the audit row before money and access actions run.
    The gate writes its one row after the handler and only logs a failure
    (admin-service/internal/http/gate.go:434-435,448-458,498-507). Done: for
    every route that is twoPerson, mayTwoPerson or stepUp and not GET, a row is
    written before the product call (recordNow exists, gate.go:467-476) and the
    action is refused if that write fails; tests with a failing recorder prove
    the product is never called; mutation.

T9  Console: route every write through the shared mutation path.
    Catalogue writes skip step-up, reason and the approval toast, so publish
    (step-up, handler_catalogue.go:36) fails (apps/admin/src/hooks/
    useCatalogue.ts:197,217,412-418). Seller approve, KYC verify and product
    approve are one click with no reason (apps/admin/src/app/sellers/
    page.tsx:55,61; app/products/page.tsx:29). Done: all use
    useAdminMutation / runSteppedAdminMutation with a reason dialog; publish
    opens the step-up prompt; vitest covers each; admin-service requires a
    non-empty reason on seller approve, KYC verify and product approve (tests +
    mutation).

T10 Console: lock the proxy to the paths it needs.
    apps/admin/src/app/api/proxy/[...path]/route.ts:15-24 forwards any /v1
    path (packages/api-client/src/proxy.ts:57). Done: the admin route refuses
    anything outside /v1/admin/, /v1/auth/admin-session/ and
    /v1/commerce/categories (do it in apps/admin, not the shared package);
    vitest. Then check which IP identity's admin login limiter keys on; if it
    keys on the caller IP, forward the client IP from a trusted header and say
    in your report what you found.

T11 Low items, in this order: stuck "approved" approvals (T11a), unbounded
    lists (T11b), /ui sandbox and [app] placeholder (T11c), rider config
    lists (T11d), readability (T11e), data export path (T11f), admin-web
    INTERNAL_SERVICE_KEY (T11g). See section 3 of the plan for each.

YOUR REPORT
1. What changed, by area, with file paths.
2. The final RESULT block of each full check.
3. Mutations: killed / total, each survivor with its reason.
4. Judgement calls and anything left undone.
5. Files outside your lane that you saw change.
```

## 2. Where admin stands

Grades: **Good**, **Needs work**, **Bad**. "Function" is whether the screen
does its job against real APIs. "Security" covers authorisation per action,
audit, two-person and step-up. One thing holds for every row: in the
Kubernetes values files the admin signing key is switched off, so every
product dashboard answers 503 there (T2). The grades describe the code as it
runs on the devbox.

| Area | Function | Security | Performance | Readability | Note |
|---|---|---|---|---|---|
| Gate (admin-service) | Good | Good | Needs work | Good | Every `/v1/admin` route must be declared or boot fails (`gate.go:216-233`). It writes one audit row per request, reads included. That row is written after the action (T8). |
| Access / roles | Good | Needs work | Good | Good | Roles can be scoped to an app now (`auth-service/database/setup.sql:152-185`). Only superadmin grants are two-person (T5). |
| Approvals (two-person) | Good | Needs work | Good | Good | The payload is hash-bound (`approvals.go:194-214`) and a requester cannot approve their own request. With one holder it runs as sole holder (T4). A stuck "approved" row cannot be cleared (T11a). |
| Audit trail | Good | Needs work | Needs work | Good | The table is append-only (`002_audit_log_attribution_append_only.sql`). Routes that skip the gate leave no row (T1, T7). The legacy `/v1/admin/audit-log` uses COUNT+OFFSET (`audit.go:98-111`). |
| Console sign-in / session / CSRF | Good | Needs work | Good | Good | HttpOnly, SameSite=Strict, host-only cookies with a double-submit CSRF token. The access token lasts 15 min; the session has a 12 h absolute cap with no idle timeout. The devbox runs password-only with no 2FA (D1). |
| Gateway / internal key | Good | Good | Good | Needs work | Moderators no longer reach `/internal/` (`internalroutes.go:1-40`). The key is stamped per upstream, not on every request. One comment is stale (`main.go:846-851`). |
| Overview / Pending | Good | Good | Needs work | Bad | Up to 6 stats polls every 60 s. `AdminDashboard.tsx` is written in a minified style. |
| `[app]` page, `/ui` | Bad | Needs work | Good | Good | `[app]` is a placeholder (`app/[app]/page.tsx:24-27`). `/ui` is a sandbox that ships to production (T11c). |
| MStore: commerce, delivery, COD | Good | Good | Needs work | Needs work | COD settle is step-up plus two-person. Dead letters are capped at 200 with no paging. |
| Sellers + KYC | Good | Needs work | Needs work | Good | Approve and KYC verify are one click with no reason (T9). The queue has no server-side limit. |
| Products queue | Good | Needs work | Needs work | Good | Approve is one click with no reason (T9). |
| Catalogue editor | Bad | Bad | Needs work | Needs work | Publish fails, and writes skip step-up and reason (T9). Enum values are created by N POSTs, one after another. |
| Coupons / bank offers | Good | Needs work | Good | Good | Step-up is asked for first. Writes are single-person and need no reason. |
| Payouts (commerce) | Good | Good | Needs work | Good | Read-only, behind step-up. |
| Payments | Good | Needs work | Good | Needs work | `refund_required` rows can be seen but never resolved (T3). `test_data` skips two-person (T6). `PaymentsSections.tsx` is 569 lines. |
| Monetization | Good | Bad | Good | Good | The console path is sound: step-up plus two-person on fund and refund. The legacy service routes bypass all of it (T1). |
| Feast (food) | Good | Good | Needs work | Good | Refunds and mark-paid are two-person with an Idempotency-Key. Several lists are capped at 200. |
| Doorstep | Good | Good | Needs work | Needs work | Refund is two-person. `lib/admin/doorstep.ts` is 1,337 lines. |
| Mopedu (rider) | Needs work | Good | Needs work | Needs work | Config tabs are write-only and need IDs pasted in (T11d). `rider.ts` is 946 lines. |
| Dating | Good | Good | Needs work | Good | Photos and selfies are capped at 100 with no paging. |
| Trust & safety | Good | Good | Good | Good | Strikes need step-up. |
| Social | Good | Good | Good | Needs work | `ContentQueues.tsx` is 660 lines. |
| Tube | Good | Good | Good | Needs work | `CopyrightSections.tsx` is 700 lines. |
| Q&A | Needs work | Good | Good | Good | Dead in every Kubernetes environment (T2). The console never calls the topics routes (`handler_qa.go:50-51`). |
| Chat | Good | Good | Good | Good | Suspend needs step-up. |
| Live | Good | Good | Needs work | Good | Polls every 15 s. Lists have no paging. |
| Notifications ops | Good | Good | Needs work | Good | Polls every 15 s. |
| Sounds library | Needs work | Good | Good | Good | The backend routes exist (`handler_sounds.go`). The console has no screen for them. |
| Feature flags | Needs work | Bad | Good | Good | Sent straight to the flag service on a global admin scope (T7). |

## 3. Tasks in detail

Severity runs from Critical down to Low. Proof always means a named test plus
a mutation for every new guard.

### T1. Legacy monetization admin routes (High; becomes Critical the day writes go on)

- **Where:**
  - Routes: `Architecture/services/monetization-service/internal/http/handler.go:158` (PATCH `/disputes/:id`), `:161` (POST `/refunds`), `:164-170` (fraud reviews, wallet freeze/unfreeze/rebuild), `:215-236` (creator-fund rates, bands, suspend, settle, settle-period, reverse, budgets).
  - Check: `fraud_handler.go:30-37` (`hasAdminScope`), `:44-70` (`getAdminID`), `admin_token.go:330-340` (`adminActor`).
- **What goes wrong:**
  - The only check is `admin` or `superadmin` in `X-Scopes`. There is no per-action permission, no step-up, no second approver, and no admin-service audit row.
  - The gateway proxies `/v1/monetization` (`api-gateway/cmd/server/main.go:680`).
  - A consumer session that passed TOTP keeps its ladder scopes (`auth-service/internal/service/admin_session.go:12-16`; the gateway says the same at `pkg/adminsession/adminsession.go:7-8`). So an admin's phone token can call these routes.
  - Today `MONETIZATION_WRITES_ENABLED=false` blocks them (`handler.go:383`; `values-prod.yaml:82`).
  - In maintenance mode, admin routes are let through on the internal key alone (`handler.go:345`). But the gateway stamps that key on every `/v1/monetization` edge request (`api-gateway/pkg/internalroutes/internalroutes.go:339`). So in maintenance, the key check adds nothing for edge callers.
  - In-cluster, anyone holding the internal key can claim `X-Scopes: admin` and any `X-User-Id`. The January runbook does exactly that (`remediate_january.sh:116,216`).
  - `/disputes/:id` and `/refunds` are not under `/admin/`, so `isAdminPattern` (`handler.go:315`) does not treat them as admin routes.
- **Fix:** remove the legacy registrations, or answer 410 (D8). The token family (`admin_token.go`) is the only admin surface. Move the runbook to the token family through admin-service, or retire it. Fix the stale gateway comment (`main.go:846-851`).
- **Proof:** a table test over every legacy method+path, with `X-Scopes: admin`, the internal key and writes on, expects 404/410. The token family tests stay green. Mutation: re-add one route and the test fails.

### T2. Product routes switched off in every Kubernetes environment (High, function)

- **Where:**
  - `deploy/services/admin-service/values-prod.yaml:60-61`, `values-qa.yaml:69-70`, `values-staging.yaml:55-56`, `values-azure-prod.yaml:46-48` and `values-azure-staging.yaml:37-39` (signing key commented out).
  - `SERVICE_CALLERS` is commented in 16 product services' `values-prod.yaml`: channel, commerce, community, dating, doorstep, food, group, live-service-v2, media, monetization, payments, post, qa, rider, trust-safety and user.
  - Boot warning: `admin-service/cmd/server/main.go:123-130`.
- **What goes wrong:**
  - In every Kubernetes environment, every product dashboard answers 503 `PRODUCT_UNAVAILABLE`.
  - Gaps 2 and 3 from the review are part of this: qa-service lists `qa:topics.manage` in its OPS line, but the line is commented in all five values files (`qa-service/values-prod.yaml:68-69`, `values-qa.yaml:77-78`, `values-staging.yaml:65-66`, `values-azure-prod.yaml:57-58`, `values-azure-staging.yaml:48-49`).
  - The permission itself exists everywhere it should: identity (`auth-service/internal/permissions/catalogue.go:291-292`), admin-service's mirror (`adminauth/catalogue.go:285-286`), the admin route (`handler_qa.go:23,50-51`) and qa-service (`admin_topics.go:24`).
  - The devbox takes qa-service's caller list from `.env` (`Architecture/docker/docker-compose.yml:2757`, `${QA_SERVICE_CALLERS:-}`, empty by default). Agents may not read `.env`, so whether Q&A works on dev is not verified here.
- **Fix:**
  - The founder generates the key and stores it. The agent uncomments the matching lines, in the order the comments give: the product's public key first, then `SERVICE_CALLERS`, then admin-service's key.
  - Add the topics screens to the console (Q&A page).
- **Proof:** the founder deploys to the environment D4 names. `GET /v1/admin/qa/stats` returns 200 and `POST /v1/admin/qa/topics` writes an audit row.

### T3. `refund_required` has no resolve path (High, money)

- **Where:**
  - Insert: `payments-service/internal/store/postgres/p0.go:495-515` ("No provider refund is issued here").
  - Read: `admin.go:237-330`, which counts unresolved rows by `resolved_at IS NULL`.
  - The only `resolved_at` writer is for `refund_commands`, not this table (`refund_attention.go:390-396`).
  - The worker itself is being built by the 7 Nov customer-facing payments lane (migration 018); the console only needs to show and explain the rows.
- **What goes wrong:** a customer who paid twice, or paid late, is owed money. The row sits forever and the console only shows a count.
- **Fix (per "nothing manual"):**
  - The worker claims rows with `FOR UPDATE SKIP LOCKED` and asks the provider to refund that payment under an idempotency key derived from the row id.
  - The signed refund webhook resolves the row without touching the intent.
  - A permanent refusal parks the row, raises an alarm and shows it in the console. Retry needs step-up.
  - This is someone else's lane today: confirm with the lead first.
- **Proof:** tests for claim/lease, a repeat claim reusing the same idempotency key, webhook resolve, a parked row never retried, and the intent never changing. Mutations on the key derivation and on the intent guard.

### T4. Bootstrap superadmin (High)

- **Where:**
  - The env allowlist bootstrap (`auth-service/database/setup.sql:94-97`; `config.go:223-225`).
  - The SQL grant, which has no `auth.admin_audit` row (`setup.sql:191-200`).
  - Sole-holder mode (`approvals.go:227-233`), which is marked in the audit row only (`handler_approvals.go:98-100`).
- **What goes wrong:** nothing records who made the first superadmin. While there is one holder, every two-person action is single-person, and the console does not say so.
- **Already fixed (verify, do not redo):**
  - Roles can now be app-scoped (`setup.sql:152-185`).
  - Moderators no longer get internal-key paths (`api-gateway/pkg/internalroutes/internalroutes.go:1-40`, `pkg/routepolicy/routepolicy.go:40-60`).
- **Fix:**
  - At boot, identity writes one `bootstrap.superadmin_present` audit row for each superadmin with no grant row, and never a second time.
  - admin-service exposes, and the console shows, "sole-holder mode" per two-person permission. It can get this from `OtherTOTPHolders`, `adminauth/permissions.go:172-196`.
- **Proof:** tests that the row is written once and not again on reboot. The banner appears when the holder count is 0. Mutation.

### T5. Sock-puppet approvals (High)

- **Where:** `admin-service/internal/http/handler_access.go:169-198`. Only `superadmin` is two-person.
- **What goes wrong:** a holder of `platform:roles.manage` grants a second account `finance` (or `admin` on a money app) alone. They enrol TOTP on it, and then approve their own refunds and settlements from it. `ErrSelfApproval` (`approvals.go:268-270`) only compares user ids.
- **Fix:**
  - Grants and revokes of any role holding a two-person permission become two-person.
  - Approvals refuse an approver whose role was granted by the requester within the D3 window. `granted_by` exists (`setup.sql:106`).
- **Proof:** tests for both rules. A mutation on each comparison.

### T6. Payments `test_data` bypass (Medium, money)

- **Where:** `admin-service/internal/http/handler_payments.go:315-319,348-350`; `payments-service/internal/store/postgres/refund_attention.go:51`.
- **What goes wrong:** one admin can close a real customer's stuck refund as "test data". No second person is involved, and nothing checks that the command is test data.
- **Fix:** payments refuses `test_data` unless the intent is in a test application or test-mode provider. Otherwise it is two-person like `written_off`.
- **Proof:** tests and a mutation.

### T7. Routes that skip the gate (Medium)

- **Where:**
  - Feature flags: `api-gateway/cmd/server/main.go:470` and `feature-flag-service/internal/http/handler.go:55-76`.
  - Mini-app status: `admin-service/internal/http/handler.go:498-499`.
- **What goes wrong:** writes to kill switches and mini-app status need only a global admin scope. There is no permission, no step-up and no admin-service audit row.
- **Fix:**
  - Declare the routes in admin-service with a new permission (for example `platform:flags.manage`, added in identity's catalogue and the mirror). Forward them with a service token, and drop the direct gateway route.
  - Unused helper: food-service `requireAdminScope` (`food-service/internal/http/handler.go:2193`) is defined but never used. Delete it.
- **Proof:** a gate test for each declared route, and a gateway route-table test that `/v1/admin/flags` is gone. Mutation.

### T8. Audit row can be lost after a money action (Medium)

- **Where:** `admin-service/internal/http/gate.go:434-435` (row after the handler), `:448-458` and `:498-507` (failure only logged).
- **What goes wrong:** if Postgres blips after commerce settles a COD remittance, the money moved and admin-service has no record.
- **Fix:** for non-GET routes that are step-up, twoPerson or mayTwoPerson, write a `started` row first using the `recordNow` pattern (`gate.go:467-476`), and refuse if that fails. Then write the outcome row. Extend the outcome CHECK if needed (latest CHECK in `003_admin_approvals_and_denials.sql:10-12`).
- **Proof:** a failing recorder means the product client is never called. Mutation.

### T9. Console mutations that skip the shared path (Medium)

- **Where:**
  - Catalogue: `apps/admin/src/hooks/useCatalogue.ts:197` (raw `useMutation`), `:217`, `:412-418` (publish); `handler_catalogue.go:36` (publish needs step-up).
  - Sellers: `apps/admin/src/app/sellers/page.tsx:55,61`.
  - Products: `apps/admin/src/app/products/page.tsx:29`.
- **What goes wrong:**
  - Publish always fails unless a step-up window happens to be open.
  - Catalogue edits capture no reason.
  - A 202 approval would show as success.
  - Approving a seller or a product needs no reason.
- **Fix:** use `useAdminMutation` and `runSteppedAdminMutation` (`lib/admin/mutation.ts:181-214`) with a reason dialog. The server requires a reason on seller approve, KYC verify and product approve.
- **Proof:** vitest for each, and a Go test plus mutation for the reason rule.

### T10. Open proxy and client IP (Medium)

- **Where:** `apps/admin/src/app/api/proxy/[...path]/route.ts:15-24`; `packages/api-client/src/proxy.ts:57` (any `/v1/*`). The header allowlist has no client IP (`proxy.ts:6-50`).
- **What goes wrong:**
  - The console host can reach any gateway route. Today the gateway's per-path session rules contain this.
  - The upstream probably sees one IP (the Next server's) for every admin. This is inferred and not verified against identity's login limiter.
- **Fix:** an allowlist in the admin route only. Then check the limiter key and forward the client IP from a trusted header if needed.
- **Proof:** vitest that a disallowed path gets 404 and the allowed ones pass.

### T11. Low items

- **T11a, stuck approvals.** `approvals.go:287-316`: a crash between claim and finish leaves the row "approved" forever. `settled` answers `ErrInProgress` (`:334-335`), so reject cannot clear it. Fix: a sweeper turns "approved" rows older than 10 minutes into an alarmed `unknown` state shown in the inbox. Proof: test and mutation.
- **T11b, lists.** No server-side limit on the seller, product and payout queues (`hooks/useAdminCommerce.ts:37-62`), approvals, live, offers, Doorstep and Feast lists. Fixed caps of 200/100 with no paging (dead letters, Feast refunds, rider `rides/live`, dating photos). Fix: cursor paging end to end. Also review the polling: 15 s on live, delivery jobs, notifications and Doorstep incidents (`lib/admin/live.ts:32`, `CommerceDelivery.tsx:106`, `app/notifications/page.tsx:18`, `DoorstepSafety.tsx:15`). Each poll writes an audit row (`gate.go:434-435`); see D7.
- **T11c, dead pages.** `apps/admin/src/app/ui/page.tsx` ships to production with no `NODE_ENV` gate. `app/[app]/page.tsx:24-27` is a placeholder. Fix: gate `/ui` to development; give `[app]` a 404 or a real page.
- **T11d, rider config.** Write-only, with IDs pasted in (`components/rider/RiderConfig.tsx:96,212`). Needs list routes in rider-service first.
- **T11e, readability.** Backend:
  - `ProductClient` lives in `service/dating_client.go:77`. Move it to its own file.
  - `handler.go` (725 lines) mixes mini-apps and OAuth with admin. Split it.
  - The legacy `/v1/admin/audit-log` (`handler.go:228-229`, `audit.go:98-111`) uses COUNT+OFFSET while the console uses the cursor route. Retire it.
  - The catalogue mirror has a drift test only for Doorstep (`adminauth/catalogue_doorstep_test.go:39`). Add one for every app.

  Web:
  - `isRecord` is defined 7 times. `"/v1/admin/commerce"` is defined 4 times.
  - There are four stats hooks and two error-message helpers.
  - Eight files are over 500 lines.
  - `AdminDashboard.tsx` and `CommerceDelivery.tsx` are minified.
- **T11f, data export.** `POST /v1/admin/data-export` is declared self-service (`handler.go:249-252`). The gateway demands the admin cookie on every `/v1/admin` path (`adminsession.go:12-18`), so consumers cannot reach it. No caller was found in `mobile/`. Fix: move it to a consumer path or delete it.
- **T11g, unused secret.** The `admin-web` container receives `INTERNAL_SERVICE_KEY` (`Architecture/docker/docker-compose.admin-local.yml:14`). The console source never reads it. Remove the line.

## 4. Decisions only the founder can take

- **D1. Devbox console without 2FA.**
  - The devbox stack layers `docker-compose.admin-local.yml` (`docker-compose.devbox.yml:5-8`). That file calls itself laptop-only.
  - It sets password login for `admin.cleestudio.com` (`admin-local.yml:16`), `APP_ENV=local` on identity (`:20`), `ADMIN_REQUIRE_MFA=false` and `ADMIN_DEV_DISABLE_2FA=true` (`:26-28`).
  - The tunnel publishes that host to the internet (`cloudflared-dev.yml:43-44`).
  - So the dev console holds real dev data and a real superadmin behind one password, with no TOTP and no step-up.
  - Keep it, or require TOTP on the devbox?
- **D2. Second superadmin.** Who is it? Until there is one, every two-person action runs alone.
- **D3. Grant rules.** Should money-role grants be two-person? How long must an approver wait after the requester granted them the role (for example 30 days)?
- **D4. Which environments get the signing key, and when.**
  - Is `deploy/` (Kubernetes/Azure) still a target, given the AWS programme? Or is the devbox the only live environment until launch?
  - The founder generates and stores the key. No agent sees it.
- **D5. `refund_required`.**
  - Refund every late or duplicate capture automatically?
  - If the provider refuses for good, who writes the row off, and with whose approval?
- **D6. "refunded_manually".** The payments resolution `refunded_manually` (`handler_payments.go:47`) contradicts "nothing manual". Keep it, or remove it?
- **D7. Audit reads.**
  - Audit every read, including the 15–60 s polls? That is about 10 rows a minute per open tab.
  - Or only sensitive reads (KYC, documents, PII)?
- **D8. Legacy monetization routes.**
  - Delete them outright, or answer 410 for a period?
  - The January remediation runbook (`remediate_january.sh`) still calls them. Staging and prod remediation are not yet authorised. Is the runbook to be rewritten for the token family or retired?
