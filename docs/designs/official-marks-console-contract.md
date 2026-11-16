# Official and Government marks in the admin console — contract (16 Nov 2026)

Reviewers decide Official-mark (organisation) applications and issue Government
invitations from the admin console. Until now the only way was the profile
service's internal-key routes, which no screen calls.

Three parts, built against this file:

| Part | Tree | Owns |
|---|---|---|
| P | `identity-platform/` (profile-service, shared, auth-service catalogue) | token-only console family, list/detail/revoke store queries, permissions |
| A | `Architecture/services/admin-service/` | `/v1/admin/marks/*`, gates, audit, two-person, document view, nav entry |
| W | `atpost-web` `apps/admin` (worktree `C:\workspace\wt\aw-marks`, branch `feat/admin-official-marks` cut from `feat/commerce-catalogue-attributes`) | the Marks page |

## 1. Permissions (identity catalogue + admin-service mirror)

App `platform` (identity itself). Spelled exactly:

| Permission | Holders besides admin/superadmin | Used for |
|---|---|---|
| `platform:official_marks.read` | `kyc_reviewer`, `auditor` | stats, queue, detail, invitation list |
| `platform:official_marks.review` | `kyc_reviewer` | approve / reject, view the registration document |
| `platform:government_marks.invite` | none (admin only) | create and revoke Government invitations |

Moderators and support hold none of them: the detail carries GSTIN/CIN and a
signatory e-mail.

## 2. Profile-service console family (part P)

Token-only, audience **`profile`**, issuer `admin-service`, scope = the route's
permission, `act` = the acting admin's user id, single-use `jti` is NOT required
(reads repeat; writes are idempotent by state). Verify half: copy
`identity-platform/services/auth-service/internal/servicetoken` into
`identity-platform/shared/servicetoken` (byte-compatible with the existing
fixture) and use it from profile-service; auth-service is left as it is.

Env: `ADMIN_SERVICE_TOKEN_PUBKEY`, `ADMIN_SERVICE_TOKEN_KID` (default `a1`).
Without a key the family is not registered (404). A request carrying any
gateway identity header (`X-User-Id`, `X-Verified-User-Id`, `X-Scopes`,
`X-Admin-Role`) is refused 403 `USER_CALLER_REFUSED`, as today.

Prefix: `/internal/v1/profiles/admin` (outside `/v1/profiles`, so the edge never
forwards it). The two internal-key routes
(`.../organisation/applications/:applicationId/review`,
`.../government/invitations`) are **removed**; the token family replaces them.
The role check by `HasRole("admin")` goes with them: the token's scope is the
authority (admin-service checked the permission against identity).

Every error is the usual envelope `{"error":{"code","message"}}`. Flags off →
503 `ORG_VERIFICATION_DISABLED` / `GOVERNMENT_VERIFICATION_DISABLED` as today.

```
GET  /internal/v1/profiles/admin/marks/stats                       official_marks.read
  200 {"data":{"in_review":3,"approved_30d":5,"rejected_30d":1,
               "invitations_pending":0,"official_accounts":12,"government_accounts":1}}

GET  /internal/v1/profiles/admin/marks/organisation/applications   official_marks.read
     ?status=in_review|approved|rejected|refused|email_pending (default in_review)
     &limit=1..50 (default 20) &cursor=<opaque>
  200 {"data":{"items":[ApplicationSummary...],"next_cursor":"..."|null}}
  Oldest first for in_review (first come, first reviewed); newest first otherwise.

ApplicationSummary = {
  "application_id","user_id","status","created_at",
  "legal_name","registration_kind":"gstin"|"cin",
  "official_domain","registry_confirmed":bool,"has_document":bool,
  "applicant":{"username","display_name","verification_level","badge_flags"}
}

GET  /internal/v1/profiles/admin/marks/organisation/applications/:applicationId   official_marks.read
  200 {"data":Application}
  Application = ApplicationSummary + {
    "registration_number",            // full GSTIN/CIN
    "signatory_email",
    "registry_name","registry_provider",
    "refusal_code"?, "reviewer_id"?, "review_reason"?, "reviewed_at"?,
    "applicant":{... + "is_person_verified":bool}
  }
  404 APPLICATION_NOT_FOUND

GET  /internal/v1/profiles/admin/marks/organisation/applications/:applicationId/document   official_marks.review
  200 {"data":{"document_media_id":"<uuid>"}}   404 DOCUMENT_NOT_FOUND when none
  (admin-service only; the console never sees the media id)

POST /internal/v1/profiles/admin/marks/organisation/applications/:applicationId/review   official_marks.review
     {"decision":"approve"|"reject","reason":"..."}   reason required, 1..500 runes
  200 {"data":Application}  (the application after the decision)
  400 INVALID_DECISION / REASON_REQUIRED
  403 REVIEWER_IS_APPLICANT (act == applicant)
  409 APPLICATION_NOT_IN_REVIEW
  422 PERSON_MARK_REQUIRED (applicant lost the person mark; the application is refused)

GET  /internal/v1/profiles/admin/marks/government/invitations      official_marks.read
     ?status=pending|accepted|revoked|expired (default pending) &limit &cursor
  200 {"data":{"items":[Invitation...],"next_cursor":...}}
  Invitation = {"invitation_id","user_id","official_name","status","created_at","expires_at",
                "accepted_at"?, "approver_ids":[a,b], "reason",
                "invitee":{"username","display_name","verification_level"}}
  Never the code or its hash.

POST /internal/v1/profiles/admin/marks/government/invitations      government_marks.invite
     {"user_id","official_name","reason","requested_by":"<uuid>"}
     approver_ids = [requested_by, act]; they must differ (400 TWO_APPROVERS_REQUIRED)
     and neither may be the invitee (403 APPROVER_IS_INVITEE).
  201 {"data":{"invitation_id","invite_code","expires_at"}}   code shown once
  409 ALREADY_GOVERNMENT, 404 PROFILE_NOT_FOUND, 400 INVALID_OFFICIAL_NAME / REASON_REQUIRED

POST /internal/v1/profiles/admin/marks/government/invitations/:invitationId/revoke   government_marks.invite
     {"reason"}
  200 {"data":Invitation}   409 INVITATION_NOT_PENDING   404 INVITATION_NOT_FOUND
```

`expired` is computed (`status='pending' AND expires_at <= now()`); pending
lists exclude expired rows.

## 3. Admin-service routes (part A)

Client `NewProfileConsoleClient` (audience `profile`, prefix
`/internal/v1/profiles/admin`, env `PROFILE_SERVICE_URL`). Audit app
`platform`. Console paths mirror the product paths under `/v1/admin/marks`:

| Console route | Permission | Gate |
|---|---|---|
| `GET /v1/admin/marks/stats` | official_marks.read | — |
| `GET /v1/admin/marks/organisation/applications` | official_marks.read | — |
| `GET /v1/admin/marks/organisation/applications/:applicationId` | official_marks.read | audited read (PII) |
| `GET /v1/admin/marks/organisation/applications/:applicationId/document/view` | official_marks.review | step-up; streams bytes with the KYC view headers, one audit row per view |
| `POST /v1/admin/marks/organisation/applications/:applicationId/review` | official_marks.review | step-up |
| `GET /v1/admin/marks/government/invitations` | official_marks.read | — |
| `POST /v1/admin/marks/government/invitations` | government_marks.invite | step-up + **two-person always** (202 → approvals; the executor sends `requested_by` = requester; the approver is `act`). Sole-holder path as elsewhere is NOT allowed here: the profile service needs two distinct people. |
| `POST /v1/admin/marks/government/invitations/:invitationId/revoke` | government_marks.invite | step-up |

The document view fetches the media id from P, then the bytes from media-service
the way the Doorstep/seller KYC document views do; types image/jpeg, png, webp,
application/pdf.

The two-person approval's result (`invite_code`, `expires_at`) is returned to
the approver when they approve, and is not stored in `admin.approvals` or the
audit log.

`/v1/admin/me` navigation gains `{"app":"marks","label":"Official marks"}` for
any holder of `platform:official_marks.read`.

## 4. Console page (part W)

Route `/marks`, three tabs:

1. **Applications** — the in-review queue (status filter), oldest first; a row
   shows legal name, GSTIN/CIN kind, domain, registry confirmed, document
   attached, applicant @username, age. Opens the detail.
2. **Detail** — every field above; "View document" (step-up, DocumentViewer);
   Approve / Reject through ConfirmReasonDialog (reason required). Refusal
   codes shown in plain words. Approve is disabled when the applicant is not
   person-verified, with that sentence.
3. **Government** — invitations list (status filter) and "Invite a government
   account" form: user (Access page's user search), official name, reason →
   202 "Waiting for a second admin". After approval the approver sees the
   invite code once with a copy button and "Give this code to the
   government body directly. It is shown once." Revoke with a reason.

Stats strip on top: in review, approved/rejected last 30 days, pending invitations.

## 5. As built (16 Nov 2026)

- Token errors from profile-service: 401 `ADMIN_SERVICE_TOKEN_REQUIRED`, 403
  `SERVICE_TOKEN_REJECTED`, `ADMIN_PERMISSION_NOT_IN_TOKEN`, `ADMIN_ACTOR_REQUIRED`,
  `USER_CALLER_REFUSED`; bad query/path values 400 `INVALID_REQUEST`. The token
  travels in `X-Service-Authorization: Bearer …`; a jti must be present but is
  not single-use.
- Reason over 500 runes or blank: 400 `REASON_REQUIRED` (the console asks for
  at least 10 characters). A person mark lost during review leaves the
  application refused with `PERSON_MARK_REQUIRED`.
- `is_person_verified` is the person bit (an Official account keeps it).
- Audit operations: `marks.stats.read`, `marks.applications.list`,
  `marks.application.read`, `marks.application.document_view`,
  `marks.application.review`, `marks.invitations.list`, `marks.invitation.create`,
  `marks.invitation.revoke`; targets `official_application`,
  `government_invitation`, `user`.
- Invitation create with only one holder of `government_marks.invite`: 409
  `SECOND_APPROVER_REQUIRED`, nothing stored.
- The document view reads bytes from media-service's private evidence route
  (`media:partner-evidence.read`, owner = applicant, kind
  `registration_document`), so it needs both `official_marks.read` and
  `official_marks.review`. That route serves images only: a PDF answers 404,
  and the console shows images only.
- Invitation lists are newest first for every status.
