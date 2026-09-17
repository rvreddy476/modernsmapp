# Admin console — weekend test guide

Everything below runs on this PC. Nothing here is started for you in advance:
the code is committed, but the dev containers still run images built before
the admin-console commits, so step 3 is the one that matters.

Run this guide **before** `dating-weekend-test.md`: its rebuild command
(step 3) includes every service the Dating guide rebuilds, so once it has run
you can skip that guide's step 2.

Your admin account: the dev account that holds `superadmin`, user id
`7cd6ea3a-9c80-4f20-806f-5d08de0f914b` (granted directly in the database on
2026-09-06; that row still works). It is **not** call_a or call_b.

---

## 1. What you'll be able to do

Sign in to one website with your password and an authenticator code, and see
a dashboard for every application — Dating, Feast, MStore, Money, Social,
Tube, Q&A, Chat, Mopedu — with only the sections your role allows. Approve,
dismiss, hide, verify and suspend from those screens, with a reason on every
write and a fresh code before anything sensitive. Every action lands in one
audit trail you can read from the console, and you can grant or revoke other
people's admin roles per application.

## 2. Before you start: 2FA on your account

**Without an authenticator app enrolled, the console will not let you in.**
Sign-in stops at "Admin accounts must use an authenticator app" (the server
code is `MFA_NOT_ENROLLED`). Today no dev account has one — including yours.

Two warnings before you enrol:

- **Enrol only on admin accounts.** Once 2FA is on, that account's sign-in
  needs a code, and the Momentum Android app cannot yet ask for one (it shows
  the message, but has no code screen). Do **not** enrol on call_a or call_b —
  you need them on the phone for the Dating test.
- The console has no enrolment screen. The Android app has one (Profile →
  Security settings), but your admin account has no app profile, so use the
  three commands below. They run in **one Git Bash window** — the second and
  third reuse a value the first one saves.

Install Google Authenticator, Authy or any TOTP app on your phone first.

1. Sign in and save the token (replace both placeholders, keep the quotes):

   ```bash
   TOKEN=$(curl -s -X POST http://localhost:8080/v1/auth/login -H 'Content-Type: application/json' -d '{"identifier":"<your admin email>","password":"<your password>"}' | grep -o '"access_token":"[^"]*"' | head -1 | cut -d'"' -f4); echo "token length: ${#TOKEN}"
   ```

   Expect a token length in the hundreds. If it prints `0`, run the same
   `curl` without the `TOKEN=$(` part and send me the reply — a
   `requires_step_up` reply means the login looked unusual, and no reply
   means the account has no password login.

2. Ask for a secret. You have **10 minutes** to finish step 3:

   ```bash
   curl -s -X POST http://localhost:8080/v1/auth/2fa/setup -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json'
   ```

   The reply contains `"secret":"..."` and `"recovery_codes":[...]`. In your
   authenticator app choose "enter a setup key", paste the secret, name it
   anything, keep it time-based. **Copy the recovery codes somewhere safe
   now** — they are shown once.

3. Confirm with the six-digit code the app is showing right now:

   ```bash
   curl -s -X POST http://localhost:8080/v1/auth/2fa/verify-setup -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"code":"<six digits from the app>"}'
   ```

   Expect `"2FA enabled successfully"`. Check it took:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d identity_db -tAf -' <<'SQL'
   select two_factor_enabled from auth.users where user_id = '7cd6ea3a-9c80-4f20-806f-5d08de0f914b';
   SQL
   ```

   Expect `t`.

4. **The moderator test account** (step 6, item 10) needs the same three
   commands with its own email, password and a second authenticator entry.
   Use any dev account whose password you know and that you do not use on the
   phone. If you have none, tell me and I'll seed one before the weekend.

## 3. Rebuild the dev services whose code changed

Every service below has commits newer than its image. Each one applies its
own schema when it starts, so this rebuild is also the database migration
(identity gains per-app roles, admin-service gains the audit columns and the
approvals table). Nothing else to run for the databases.

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose up -d --build identity-auth api-gateway admin-service dating-service dating-data-exporter food-service commerce-service trust-safety-service monetization-service payments-service post-service qa-service user-service channel-service group-service community-service rider-service graph-service notification-service chat-message-service call-service
```

That is 21 images; expect it to take a while. When it returns, wait about
30 seconds, then confirm they are up:

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose ps --format '{{.Name}} {{.Status}}' identity-auth api-gateway admin-service dating-service dating-data-exporter food-service commerce-service trust-safety-service monetization-service payments-service post-service qa-service user-service channel-service group-service community-service rider-service graph-service notification-service chat-message-service call-service && curl -s -o /dev/null -w 'identity via gateway: %{http_code}\n' http://localhost:8080/v1/auth/health
```

Expect 21 `Up` lines and `identity via gateway: 200`. Then confirm the
identity schema moved:

```bash
docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d identity_db -tAf -' <<'SQL'
select string_agg(column_name, ',' order by ordinal_position) from information_schema.columns where table_schema = 'auth' and table_name = 'user_roles';
SQL
```

Expect `user_id,role,granted_by,granted_at,app,expires_at,reason`. If
`identity-auth` is not `Up`, or `app` is missing, stop here and send me:

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose logs --since 10m identity-auth admin-service | tail -60
```

## 4. Start the admin website

The console is a separate website in the web repo. Its local settings are
already in place (`apps/admin/.env.local` points it at the gateway on
`http://localhost:8080`). Open a **second** Git Bash window and leave this
running:

```bash
cd /c/workspace/atpost-web/apps/admin && bun run dev
```

Wait for `Ready`, then open **exactly** this address in Chrome or Firefox:

```
http://localhost:3002/admin/login
```

The admin cookies are tied to the host name you open, so `127.0.0.1` or the
PC's Wi-Fi address will not sign you in — use `localhost`. (On dev the
console lives under `/admin`; on its own host later it will be at the root.)

## 5. Sign in

1. Enter your admin email (or phone) and password.
2. Enter the six-digit code from your authenticator app. You have five
   minutes; five wrong codes lock that step for 15 minutes.
3. You land on the overview. The session lasts 12 hours; sensitive actions
   ask for a fresh code (a "step-up") that then stays good for 5 minutes.

## 6. Walkthrough, in this order

1. **Overview counts.** The overview reads each application's `/stats`. The
   Dating tile should show reports pending, panics open and photos pending
   review. Compare with the database:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select (select count(*) from dating_reports where status = 'submitted') as reports_pending,
          (select count(*) from dating_panic_incidents where status = 'open') as panic_open,
          (select count(*) from dating_photos where moderation_status = 'pending') as photos_pending_review,
          (select count(*) from dating_profiles where profile_status = 'active') as profiles_active;
   SQL
   ```

   Today that is `1|1|0|22`. Feast should show 3 restaurants, Chat 2 open
   channel reports.

2. **Dating — approve a photo.** The queue is empty on dev (all 22 seeded
   photos are approved), so make one: on the phone, as call_a, add a photo
   to the Match profile. New uploads start as `pending`. Back in the console,
   open **Dating → Photos**, approve it with a reason. Confirm the audit row:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select action, admin_actor, entity_id, reason, outcome, created_at from admin.audit_log where action = 'dating.photo.decide' order by created_at desc limit 3;
   SQL
   ```

   Expect one row with your user id as `admin_actor` and `outcome` `success`.

3. **Dating — act on a report.** **Dating → Reports** has the one seeded
   report. Choose **dismiss** (the safe one; suspend and reinstate also
   exist) and give a reason. Confirm the row in the same place, and that
   dating recorded it too:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select action, admin_actor, reason, outcome, created_at from admin.audit_log where action = 'dating.report.act' order by created_at desc limit 3;
   select action, actor_admin_id, reason, created_at from dating_admin_audit order by created_at desc limit 3;
   SQL
   ```

4. **Dating — open the panic detail.** **Dating → Panic** lists the one open
   incident. Opening its detail asks for a fresh authenticator code first —
   the location is only revealed there, and the reveal is itself an audited
   read. Acknowledge it if you like. Confirm:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select action, admin_actor, entity_id, outcome, created_at from admin.audit_log where action in ('dating.panic.reveal','dating.panic.ack') order by created_at desc limit 5;
   SQL
   ```

5. **Feast.** Open **Feast**: three active restaurants, the dashboard
   numbers, tickets and settlements. There is **nothing to approve on dev**
   — the Feast seed submits its restaurant and approves it in the same run,
   and no document or partner is pending. Read the screens; the approve
   button gets its test the first time a partner applies. If you do exercise
   any Feast write, this is where it lands:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select action, admin_actor, entity_type, entity_id, reason, outcome, created_at from admin.audit_log where app = 'food' order by created_at desc limit 5;
   select action, actor_user_id, entity_type, created_at from food.admin_audit_logs order by created_at desc limit 5;
   SQL
   ```

6. **MStore — verify a seller's KYC.** **MStore → Sellers**: 819 sellers are
   `pending`, one is `verified`. Pick any pending one, choose **verify KYC**.
   The console must ask for a fresh authenticator code first (step-up) and
   then a reason. Confirm the audit row and the seller's own record:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select action, admin_actor, entity_id, reason, outcome, created_at from admin.audit_log where action = 'seller.kyc_verify' order by created_at desc limit 3;
   SQL
   ```

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d commerce_db -tAf -' <<'SQL'
   select action, actor_user_id, target_id, reason, created_at from commerce_admin_audit_log order by created_at desc limit 3;
   SQL
   ```

   `actor_user_id` must be your id, not zeros. (That table is new — it
   appears with the rebuild in step 3.)

7. **Money — a creator-fund settle.** **Money → Creator fund → settle a
   period**. Expect: step-up prompt, then a reason, then the action goes
   through as **founder-alone approval** (you are the only holder of the
   finance permission, so there is nobody to be the second person). On dev
   monetization writes are switched off (`MONETIZATION_WRITES_ENABLED` is
   `false`), so the product side answers "not launched" and the settlement
   itself does not change — that is expected. What matters is the record:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select action, admin_actor, payload->>'approval' as approval, outcome, status_code, created_at from admin.audit_log where action like 'monetization.fund.settle%' order by created_at desc limit 3;
   SQL
   ```

   Expect `approval` = `sole_holder`.

8. **Q&A — hide a question with a reason.** **Q&A** lists questions (18 on
   dev). Hide one; the console refuses to send without a reason. Confirm:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select action, admin_actor, entity_id, reason, outcome, created_at from admin.audit_log where action = 'qa.question.hide' order by created_at desc limit 3;
   select action_type, actor_id, reason, created_at from moderation_actions order by created_at desc limit 3;
   SQL
   ```

9. **Chat — decide a channel report.** **Chat → Channel reports** has two
   open reports. Decide one: **uphold** or **dismiss**, with a reason. Its
   status becomes `reviewed` or `dismissed`:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select action, admin_actor, entity_id, reason, outcome, created_at from admin.audit_log where action = 'chat.channels.report.decide' order by created_at desc limit 3;
   select status, reviewer_id, review_note, reviewed_at from channel_reports order by reviewed_at desc nulls last limit 3;
   SQL
   ```

10. **Access — grant, check, revoke.** This needs the second account from
    step 2, item 4 (2FA enrolled).

    Your role plan maps onto the console like this: you hold **superadmin**;
    an **Account admin** for one app is role **admin** with that app chosen
    as the scope; a **Finance admin** is role **finance** (refunds,
    settlements, payouts, fund rates). Two-person approval for money switches
    on by itself once a second finance admin with 2FA exists.

    1. **Access** → search the account (type the start of its email or
       handle, or paste its user id) → grant role **moderator**, app
       **dating**, with a reason. This is a role change, so it asks for a
       fresh code.
    2. Open a **private / incognito window** at
       `http://localhost:3002/admin/login` and sign in as that account. It
       should see **only Dating** in the navigation — no Feast, no MStore,
       no Money, no Access.
    3. Back in your window: **Access** → revoke that role, with a reason.
    4. In the private window, click anything. It must fail at once with a
       "session has ended" style refusal, not keep working. Identity ends
       every session of the account in the same transaction as the revoke,
       and the gateway checks that list on every request (dev's gateway has
       `REDIS_ADDR` set, which is what makes the check live).
    5. Confirm both sides recorded it:

       ```bash
       docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
       select action, admin_actor, entity_id, reason, outcome, created_at from admin.audit_log where action in ('access.role.grant','access.role.revoke') order by created_at desc limit 4;
       SQL
       ```

       ```bash
       docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d identity_db -tAf -' <<'SQL'
       select action, actor_id, target_id, detail, created_at from auth.admin_audit order by created_at desc limit 6;
       SQL
       ```

       The revoke's `detail` ends with `sessions_revoked=N`, N at least 1.

11. **Audit page.** Open **Audit** and check every action you took above is
    listed, newest first, with your account and the reasons you typed.

## 7. Known limits on dev — not bugs

- **Founder-alone approval.** Two-person approval switches itself on the
  moment a second person holds the same permission. Until then, money and
  role changes go through with your step-up and reason, and the audit row
  says `sole_holder`.
- **Money is not launched.** `MONETIZATION_WRITES_ENABLED` is `false` on
  dev, so settle, reverse, rates and budgets are recorded by the console but
  refused by the product. Payouts stay off everywhere.
- **Nothing is waiting in Feast.** The seed approves what it creates; the
  restaurant / document / partner approve buttons need a real application.
- **No document image previews.** KYC and seller documents are decided from
  their metadata; the image viewer is a later piece.
- **Group and community report queues are empty** — the tables exist but no
  dev data reports a group or a community.
- **Feast has no staging or production chart yet** (`deploy/services` has no
  `food-service` directory), so its console routes cannot be enabled there
  until one exists.
- **Tiles disappear per role, by design.** Permissions are per application:
  `commerce:sellers.read`, for example, belongs to MStore moderators and
  support only, and `commerce:stats.read` (it includes payout amounts) to
  finance and support. A Dating moderator therefore sees no MStore, Money or
  Feast tiles at all — that is the permission catalogue working, not a
  missing page. You, as superadmin, see everything.
- **The Android app cannot finish a 2FA sign-in.** Any account you enrol
  stays signed out of the phone until a code screen ships. Hence: admin
  accounts only.
- **Your superadmin row was inserted by SQL** with no audit entry. It works;
  re-granting it through the audited route and deleting the SQL row is a
  follow-up for after this test.

## 8. Before staging or production

These are yours to do; none of them is code.

1. **Generate the admin signing key** — one ed25519 key. The private half
   (base64 32-byte seed) goes to the secret store as
   `admin_service_token_key` with its kid as `admin_service_token_kid`
   (admin-service's values file has the two commented lines to switch on).
2. **Give each product the public half** as `caller_admin_pubkey` and
   `caller_admin_kid`, and enable the product's own two secret lines plus its
   `SERVICE_CALLERS` lines **before** enabling admin-service: dating-service,
   commerce-service, trust-safety-service, post-service, user-service,
   qa-service, channel-service, group-service, community-service,
   monetization-service, payments-service, rider-service — and food-service
   once it has deploy values. Until a product is enabled its dashboard
   answers `503 PRODUCT_UNAVAILABLE`, which is safe.
3. **Give identity the same public key** (`caller_admin_pubkey` /
   `caller_admin_kid` in identity-auth-service's values; a pubkey without a
   kid refuses boot). This is what the Access page needs.
4. **Dating's own secrets** if not already created: `dating_evidence_hmac_key`
   (at least 32 bytes, never rotated casually), `dating_pii_keys`
   (`v1:<base64 32-byte key>`) and `dating_pii_lookup_salt` (at least 16
   random bytes), plus `dating_service_token_key` / `_kid`.
5. **DNS and TLS for the admin host** — decided 2026-09-17:
   **`admin.cleestudio.com`** (staging: **`admin.staging.cleestudio.com`**,
   named after `app.staging.cleestudio.com`). `deploy/web/admin/values-prod.yaml`
   and `values-staging.yaml` (plus the generated `values-azure-*.yaml`) now
   carry those hosts at the root; the image is built with `ADMIN_BASE_PATH=/`
   (atpost-web `build-push.yml`); cookies are host-only, Secure,
   SameSite=Strict; the shell answers `app.cleestudio.com/admin` with a 307 to
   the new host once its env has `ADMIN_HOST_URL=https://admin.cleestudio.com`
   (`deploy/web/shell/values-*.yaml` — one line, still to add). The public
   `cleestudio.com` zone is at **Cloudflare** (`infra/terraform/modules/dns/main.tf`);
   the terraform ACM wildcard only covers `*.aws.cleestudio.com`, so the cert
   below is a separate one. Do these in order, per environment:
   1. **Certificate (AWS, before DNS).** ACM, region `ap-south-1` → *Request
      public certificate* → domain `admin.cleestudio.com` (or add it as a SAN
      to the certificate already carrying `app.cleestudio.com`; a
      `*.cleestudio.com` wildcard also covers it) → validation **DNS**. ACM
      shows one record: type `CNAME`, name `_<hash>.admin.cleestudio.com`,
      target `_<hash>.acm-validations.aws.`. Add it in Cloudflare as
      **DNS only (grey cloud)**, wait for *Issued*, then paste the ARN into
      `alb.ingress.kubernetes.io/certificate-arn` in
      `deploy/web/admin/values-prod.yaml` (it replaces `CHANGEME`). Staging:
      same for `admin.staging.cleestudio.com` in `values-staging.yaml`.
   2. **Deploy the admin zone** (ArgoCD sync) so the shared ALB
      (`atpost-web-prod` / `atpost-web-staging`) gains the host rule and the
      cert. Read the ALB name — it is the record target:
      `kubectl -n atpost get ingress web-admin -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'`
      (every web zone shares that ALB, so `web-shell` prints the same value).
   3. **DNS record (AWS).** Cloudflare, zone `cleestudio.com`: type `CNAME`,
      name `admin`, target = the ALB hostname from step 2
      (`k8s-atpost-….ap-south-1.elb.amazonaws.com`), TTL Auto. Proxy status:
      match what `app` uses today; if `app` is proxied (orange cloud), proxy
      `admin` too with SSL mode **Full (strict)** — the ACM cert on the ALB is
      required either way. Staging: name `admin.staging`, target = the
      staging ALB hostname.
   4. **Azure instead (only if that cloud is live).** TLS terminates at Front
      Door; there is no per-ingress cert. In profile `atpost-prod-fd`
      (`infra/azure/modules/frontdoor`) add a **custom domain**
      `admin.cleestudio.com` with a Front Door-managed certificate, attach it
      to `default-route` and to the WAF security policy — the terraform module
      declares only the default `*.azurefd.net` domain, so this is a portal/CLI
      step or a new `azurerm_cdn_frontdoor_custom_domain` resource. Front Door
      then shows a `_dnsauth.admin` TXT to add in Cloudflare. Record: type
      `CNAME`, name `admin`, target = the `frontdoor_endpoint` terraform
      output (`atpost-prod-<id>.z01.azurefd.net`), DNS only. If the Azure DNS
      zone is used instead, add `admin` to `edge_cname_records` in the env
      tfvars.
   5. **Verify.** `curl -sI https://admin.cleestudio.com/api/health` → `200`
      with `strict-transport-security: max-age=63072000; includeSubDomains; preload`;
      `curl -sI http://admin.cleestudio.com/` → `301` to https;
      `curl -sI https://app.cleestudio.com/admin` → `307` to
      `https://admin.cleestudio.com/` (needs the shell's `ADMIN_HOST_URL`).
6. **Decided on 17 September:**
   - Money **reads** are shown during the beta; only the money actions stay
     off until launch.
   - **Every refund goes to a second approver**, whatever the amount. While
     you are the only finance admin it runs on your own and the record says
     so.
7. **Name people when ready:** a second finance admin (switches on
   two-person approval), per-app moderators, and the grievance officer.
   Nobody else gets access until then.

## 9. If something looks broken

Console refuses you or a page is empty:

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose logs --since 5m api-gateway admin-service identity-auth | tail -60
```

A specific dashboard is wrong (swap the service name):

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose logs --since 5m dating-service | tail -40
```

The website itself errors: look at the Git Bash window running `bun run dev`
and copy the last screen.

Tell me what you saw and what you expected; I'll take it from there.
