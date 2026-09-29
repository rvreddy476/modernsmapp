# Copyright Match: response to the design review

**Status: design response only.** No application code, dependency, deployment configuration, migration or database was changed. No test, benchmark, image build or evaluation was executed, and no founder or counsel decision has been made. Every acceptance test below is **PROPOSED**.

- Evidence was read at commit `bad0219e` (branch `codex/module-01-02-launch-safety`). Paths are relative to `Architecture/services/` unless they start with `Architecture/`, `deploy/`, `infra/` or `.github/`.
- `media-service/cmd/worker/main.go`, `media-service/internal/processing/video.go` and `Architecture/docker/docker-compose.yml` have uncommitted working-tree edits, so their line numbers are HEAD line numbers.
- The corrected design is `docs/designs/copyright-match-plan.md`. "Plan §n" below refers to its sections, "P-n" to its prerequisites (plan §4), and "F-/L-/O-n" to its decisions (plan §15).

## Summary

| # | Finding | Status | Main correction | Plan section |
|---|---|---|---|---|
| 1 | Case-specific removal and safe restoration | **Verified**, and broader than stated | Per-case restriction rows, separate from `review_status`; a new signed capability; appeal fixes at three points | §6.2, §6.3, §7, §9.2, §9.5 |
| 2 | End-to-end privacy and access revocation | **Verified**, and broader than stated | A discovery predicate AND the canonical read gate on every read; stated revocation latencies | §8.1, §8.2 |
| 3 | Jurisdiction-specific legal procedures | **Verified** (a design error; no code exists) | Regime per case, set by a person; separate timestamps; filed-action evidence reviewed and bounded | §7, §11 |
| 4 | Strike-standing diagnosis | **Verified**, plus two more defects; latent on dev | A narrow service-to-service standing contract; one choke point for every publication path | §4 P-5, §6.4 |
| 5 | Audited, idempotent, reversible strikes | **Verified** | Case-linked strikes, idempotency keys, explicit expiry, void, audit, outbox | §6.4, §9.3 |
| 6 | Ownership, licensing and remix abuse | **Verified** | "Potential match" language; reviewer checklist; no remix exemption | §1, §8.4 |
| 7 | Entitlement, quotas and public-form bypasses | **Verified** | Stored-row programme definition; access precedence; atomic shared quotas | §6.5, §8.3 |
| 8 | Durable legal notices and evidence retention | **Verified** | Delivery records in trust-safety; retention classes; legal holds | §6.4, §10 |
| 9 | Candidate recall for multi-index hashing | **Verified** (the cost side too) | Radius-2 probing in all four bands; scale gate; not called exact | §12.3 |
| 10 | Matching thresholds and audio contradictions | **Verified** | Informative-weight denominators; copy ≥ 0.80; classes; audio deferred | §12.2, §12.4, §12.5 |
| 11 | Generation fencing, pair identity, upload time | **Verified**, and extended | `media_generation`, `upload_confirmed_at`, fenced writes; pair versus match identity | §6.1, §6.2 |
| 12 | Safe outbox migration and queue isolation | **Verified** (more dependents than listed) | Existing outbox untouched; separate pair outbox, relay and topic | §6.1, §9.1 |
| 13 | Measured resource and capacity gates | **Verified** | Per-environment resources; kill switches; admission; readiness gate; benchmarks | §13 |
| 14 | Worker image build prerequisite | **Disputed as to the committed state**; a real CI/image gap remains | Record the CI build/push gap and the tag-bump defect as P-17 | §4 P-17, §14.2 |
| 15 | Explicit fingerprint input contract | **Verified at HEAD**; the rung guarantee must be re-verified after the in-progress HLS change | Lowest HLS rung via internal reads; fallbacks; fpcalc gate for phase 2 | §12.1, §12.6 |

---

## Finding 1: case-specific removal and safe restoration

**Status: Verified, and broader than stated.**

If the plan routed copyright removals through the existing moderation route, they would land on the single `posts.review_status`. Four existing writers can move that status back to visible, and the author can undelete. Today no copyright removals exist, so this is a hazard in the plan as written, not a live data defect.

**Current code evidence**
- **One status, no case concept.**
  - Subject `{review_status, search_rev, deleted}`: `post-service/internal/store/postgres/moderation_authority.go:20-26`.
  - Transitions allow approving from `rejected` or `needs_changes`: `:78-89`.
  - `source` is limited to `admin`/`appeal`: `:94-98` and `post-service/database/migrations/033_post_moderation_authority.sql:10`.
  - A decision overwrites `review_status`: `:159-171`.
  - `sameModerationClaims` ignores `ExpectedRevision`, and `PolicyVersion` is never persisted: `:193-202, 148-156`.
- **The internal route hardcodes `appeal`.** `ModeratePostInternal` always writes `Source: "appeal"` (`post-service/internal/http/moderation_authority_handler.go:135`) and passes any signed decision through (`:106-137`).
- **The capability.** `Claims` has no case, source or expected state (`Architecture/shared/moderationcap/capability.go:22-36`). `Verify` has no decision allowlist (`:104-136`). The HMAC is taken over the marshalled struct (`:124`), which the story protocol shares (`post-service/internal/consumers/story_moderation.go:93`).
- **Other writers that can undo a removal.**
  - Legacy moderator route: `post-service/internal/http/handler.go:136`.
  - Admin-token route: `admin_token.go:454-464`.
  - Resubmit then reviewer or ML approve: `posts.go:1442-1453, 1418-1438`; `feedback_handler.go:63-103`.
  - Author `RestorePost`: `posts_lifecycle.go:41-72`.
- **Appeals.**
  - Eligibility checks status only, with no decision link: `trust-safety-service/internal/service/trust_extras.go:56-59, 63-64`.
  - The overturn uses the **fresh** revision: `:106-120` (`:117`).
  - The canonical mutation happens before the local transition, so uphold and overturn can race: `:115-128`; store `trust_extras.go:197-229`.
  - Overturn claims: `post_moderation_http.go:73-78`.
  - One active appeal per content, with no decision tie: `008_launch_report_and_appeal_integrity.sql:53-55`.
- **Read paths.** 56 `review_status` references in 16 files, e.g. `reel_feed.go:33,42,123,132,183,192`, `posts.go:825,909,972`. Also `post_access_state.go:11-40` and `search_eligibility.go:75-118`. `SearchEligible` is an allowlist (`Architecture/shared/events/events.go:679-689`).

**Design correction**
- **Holds are separate rows.** A copyright removal is a row in `post_restrictions` keyed by `(post_id, source, case_id)`, with an append-only `post_restriction_events` ledger. `active_restriction_count` feeds `effective_review_status` (plan §6.2).
- **A release is narrow.** It writes only its own row, the count and the eligibility projection. It never writes `review_status`, `visibility`, audience, `age_restricted`, `deleted_at`, `publish_at`, `published_at` or `created_at`. So it cannot undelete, publish a scheduled post, widen the audience, or clear another case or a safety decision. None of the existing writers touch restrictions.
- **A new capability.** `post_restriction` has its own key and a claims set: case id, case revision, action, reason code, expected state, stable `decision_id`, subject author. It has an allowed (action, reason) table. `moderationcap.Claims` is not modified, so adding a `copyright` string is not the mechanism (plan §3, §6.2).
- **Revision checks and replays.** `case_revision` must increase and `expected_state` must match. A same-digest replay returns the stored result; a different digest returns 409. The dispatcher retries only transient errors, and the enforcement outbox shares the case transaction (plan §9.2).
- **Appeals are enforced at three points** (plan §6.3):
  - **submission:** base status only, with `appealed_decision_id` recorded; refused for an approved base under a hold;
  - **adjudication:** an `overturning` state under a row lock removes the uphold/overturn race;
  - **final mutation:** a v2 capability carries `expected_base_decision_id`, and post-service returns `409 SUPERSEDED` on mismatch.

  Pre-existing appeals are backfilled or superseded. Concurrent admin decisions supersede an in-flight overturn.
- **Reconciliation** runs every 15 minutes and daily, re-sending only recorded decisions. Orphan holds are never auto-released. A nightly count self-check repairs drift (plan §9.5).
- **The state machine and hold-state sets** are in plan §7.

**Remaining decisions and dependencies**
- Prerequisites P-1 to P-4: an inventory of the 56 read references, a source-scan guard, and an audit of eligibility-event consumers.
- Provision `POST_RESTRICTION_HMAC_KEY` (O-2).
- A coordinated deploy of the appeal capability v2.
- Country-scoped holds (L-13). Author-facing wording (F-18).

**Proposed acceptance tests (PROPOSED)**
- **T1-1:** two copyright holds plus a base safety rejection plus a `safety` row; each release is isolated, and the other fields are byte-identical.
- **T1-2:** release on a scheduled post.
- **T1-3:** release on a deleted post, and author restore under a hold.
- **T1-4:** capability integrity refusals.
- **T1-5:** idempotency and stale revisions.
- **T1-6:** admin routes cannot lift a hold.
- **T1-7:** reconciliation.
- **T1-8:** read-path coverage.
- **TM-1:** two cases across the state machine.
- **TM-2:** a pre-existing appeal racing a copyright removal, variants a–d.

---

## Finding 2: end-to-end privacy and access revocation

**Status: Verified, and broader than stated.** Beyond the finding:
- the byte gate is weaker than the detail gate on several axes;
- some presigned URLs outlive five minutes;
- realtime rooms are authorized only at join;
- inbox payloads carry the actor id.

**Current code evidence**
- **The detail gate admits the viewers Matches must exclude.** `viewerMayViewPost` admits unlisted, followers and share-list viewers (`post-service/internal/service/post.go:2852-2882`; shares at `private_shares.go:30-40, 75-85`). The canonical gate is `read_gate.go:1-31`.
- **Account privacy.** Checked via graph with a 3 s per-process cache (`privacy_gate.go:39, 191-230`).
- **Age gate.** `age_gate.go:83-133`.
- **Scheduling.** `schedule.go:34-39, 72-77`.
- **Soft delete and restore window.** `posts_lifecycle.go:41-70`; 720 h purge at `postpurge/worker.go:62`.
- **Byte gate gaps.** `story_surface.go:585-677` skips account privacy, hidden authors and `publish_at`. `circle` diverges between the gates (`post.go:2869` versus `story_surface.go:670-676`).
- **Anonymous poster gaps.** `public_poster.go:113-143` and `public_post_media.go:58-76` skip account privacy, hidden authors and `age_restricted`.
- **Presigned TTLs.** `MaxProtectedTTL = 5m` (`media-service/internal/delivery/signer.go:66-72`). Other presigns are 15 min, 30 min or 24 h (`media.go:31`, `frames.go:65,93`, `audio.go:146,372,478`).
- **Realtime and notifications.** Rooms are checked at join only (`notification-service/internal/ws/rooms.go:30-57`). Realtime publishes the whole notification struct, including `actor_user_id` (`notification.go:298-310`).
- **Web cache.** React Query stale times such as 60 s (`postbook-ui/src/hooks/useMonetization.ts:698-708`).

**Design correction**
- **Two predicates on every read** (plan §8.1):
  - `copyright_discoverable(copy)` on the canonical row, as the anonymous stranger for account-level checks: public only, not scheduled, approved and unrestricted, not age-restricted, ready media, author not hidden, public account, not the claimant's own;
  - `match_readable` = current entitlement + reference ownership + discovery + `PostReadGate(copy, claimant)`.
- **Unlisted is off**, as a compile-time constant.
- **A full surface matrix** covers list, detail, counts, thumbnails, archive and unarchive, removal submission, case views, the reviewer queue, notifications, realtime (none in v1) and cached pages (plan §8.2).
  - Failures return 404 `MATCH_NOT_FOUND`, never 403.
  - There are no URLs in JSON. Thumbnails are single 5-minute stills behind a 302.
  - Notifications carry ids only, with a system actor.
  - Responses are `no-store`.
- **Stated revocation latencies:**
  - next request after commit;
  - ≤ 3 s for account privacy;
  - ≤ 60 s for programme status;
  - ≤ 5 min for an already-issued thumbnail URL (no earlier mechanism exists);
  - rendered pages until the next refetch.

  Instant revocation is not promised.
- **Suppression, deletion and retention** (plan §8.2, §10). Privacy transitions cancel unsent digests but never reset `notified_at`. Soft delete hides a match; purge deletes it within 24 h. Case snapshots survive.
- **The fingerprint worker uses no presigned URLs** (plan §12.1).

**Remaining decisions and dependencies**
- Unlisted (F-11/L-11). Blocks (F-4). Age-restricted copies for adults (F-5).
- CloudFront header verification (O-4).
- The gate defects are recorded as P-8 and P-9 and tracked as separate tickets. Matches does not rely on those gates.

**Proposed acceptance tests (PROPOSED)**
- **T2-1:** followers, share-list, unlisted-link and account-private copies are hidden everywhere.
- **T2-2:** transitions with the page open, and the ≤ 5 min thumbnail window.
- **T2-3:** reverse transitions, with no new notification.
- **T2-4:** scheduled copies.
- **T2-5:** graph outage fails closed.
- **T2-6:** removal submission racing a privacy flip.
- **T2-7:** a digest after privacy loss sends nothing.
- **T2-8:** payload content.
- **T2-9:** cache headers.
- **T2-10:** purge.

---

## Finding 3: jurisdiction-specific legal procedures

**Status: Verified.** This is a design error in the plan. No counter-notice, DMCA, Rule 75 or legal-hold code exists; a grep finds only the unused `check_type` enum in `post-service/database/migrations/011_postgram_features.sql`. **This is not legal advice, and nothing has been approved.**

**Current evidence (primary sources)**
- **US, 17 U.S.C. §512** (https://www.law.cornell.edu/uscode/text/17/512).
  - The restore window runs from receipt of an effective counter-notice: "not less than 10, nor more than 14, business days" (§512(g)(2)(C)).
  - Restoration is prevented only by notice to the designated agent that an action has been **filed**.
  - Also relevant: notice elements (§512(c)(3)(A)), curable deficiencies (§512(c)(3)(B)), repeat infringers (§512(i)), misrepresentation (§512(f)), and designated-agent expiry after 3 years (37 CFR 201.38(c)(4), https://www.law.cornell.edu/cfr/text/37/201.38).
- **India, Copyright Act s.52(1)(c)** (https://copyright.gov.in/Documents/CopyrightRules1957.pdf).
  - It covers transient or incidental storage.
  - Its proviso says to "refrain from facilitating such access for a period of twenty-one days" from the **complaint**.
- **India, Copyright Rules 2013, Rule 75** (https://copyright.gov.in/Documents/Copyright_Rules_2013_and_Forms.pdf).
  - Particulars including an undertaking to sue (75(2)).
  - Takedown within 36 hours (75(3)).
  - A reasons notice to viewers (75(4)).
  - Permissive restoration: the host "may restore" (75(5)).
  - No obligation to respond to a repeat complaint after failure (75(6)).
  - **There is no counter-notice step.** Whether a UGC video host falls within the Rule at all is for counsel.
- **Plan errors.**
  - It treated "US 10–14 business days" and "India 21 days" as settings of one timer.
  - It chose the regime "per country".
  - It let "records legal action" act as a bare assertion.
  - It treated disclosure as one-way, although the US counter-notice must be copied to the claimant.
- **Separate prerequisite (P-20).** trust-safety's grievance timer is 15 days (`trust-safety-service/internal/service/grievances.go:15`, `007_grievances.sql`). The IT Rules consolidated text updated 10.02.2026 (https://www.meity.gov.in/static/uploads/2026/02/550681ab908f8afb135b0ad42816a1c9.pdf) appears to require acknowledgement in 24 h and resolution in 7 days. **Counsel is to confirm. This is recorded, not fixed.**

**Design correction**
- **Regime per case** (plan §11.2). `legal_regime` is one of `us_512`, `in_rule75`, `in_court_order`, `platform_policy_only`, `unassigned`, set by a person with a recorded basis. Country is an input only. `unassigned` blocks approval, restoration and strikes, and escalates.
- **Separate write-once timestamps** for:
  - receipt;
  - validation (per-element checklist; a curable deficiency creates a prompt-contact task);
  - decision;
  - enforcement ack;
  - subscriber notice;
  - counter-notice receipt and effectiveness;
  - forwarding with delivery evidence;
  - the US window on an approved business-day calendar;
  - the Rule 75 period;
  - closure.

  A failed forward never resets a clock (plan §6.4, §11.2).
- **Regime-specific state-machine branches** (plan §7.1). US: `counter_notice_open → awaiting_restoration → restoring | legal_action_hold`. India: `removed → restore_decision_required | legal_action_hold`, with permissive restoration.
- **Filed-action or court-order evidence** must be documents plus a reviewer decision, before a bounded due time, with mandatory re-verification. A checkbox never holds restoration. The schema has no state-changing boolean (plan §6.4, §11.2).
- **Counsel's approval list** is plan §15 L-1…L-14: applicability, notice fields, deadlines, restoration, evidence standards, disclosures, exceptions, repeat-infringer policy, abuse, retention, unlisted, corpus, country scope, and takedown while unassigned.

**Remaining decisions and dependencies**
- Every L-item in plan §15.
- Designated-agent registration and Grievance Officer publication (O-6).
- An approved business-day calendar.
- P-20 timelines (L-3).

**Proposed acceptance tests (PROPOSED)**
- **T3-1…T3-16** (plan §17.2). These include:
  - the US window on the calendar;
  - a validation delay that does not move the clock;
  - a deficient counter-notice that starts nothing;
  - a forwarding failure that does not reset the clock;
  - a checkbox that never holds;
  - an invalid or unreviewed legal-action assertion bounded by the deadline;
  - Rule 75 timing and court orders;
  - a regime that is never inferred;
  - mixed regimes on one post;
  - retention under a legal hold.

---

## Finding 4: repair the strike-standing diagnosis

**Status: Verified in every claimed part, plus two further defects.** (1) The default URL port is 8118 while trust-safety listens on 8091, and no post-service environment overrides it. (2) Strikes never receive an expiry, so they are permanent.

**Identified by code reading, not observed failing.** On dev the composer drafts table (`post_drafts`) is empty, so the draft-publish defect is latent there. The claim that composer-draft publication is blocked wherever the check runs is a static inference, to be confirmed by T4-1.

**Current code evidence**
- **Client** (`post-service/internal/service/post_drafts.go`):
  - calls `GET /v1/strikes/{authorID}`: `:467`;
  - sends only `X-User-Id` (the author) and the internal key: `:472-475`;
  - any non-200 is `ErrStandingUnknown`: `:481-483`;
  - expects `data:[...]`: `:484-492`;
  - severity switch `ban|suspend|suspension|severe`: `:498-501`;
  - on unknown, releases the claim: `:305-318`; store `post_drafts.go:314-321`;
  - the worker retries every 60 s forever: `:428-441`; `cmd/server/main.go:543-567`.
- **Server.**
  - `GetUserStrikes` requires `adminAllowed` (`trust-safety-service/internal/http/trust_extras_handler.go:366-370`; `admin_token.go:242-247`), so the answer is 403.
  - The response is `{"data":{"items":[…]}}` (`:382`), and `items` is `null` when empty (`store/postgres/trust_extras.go:496-507`).
- **Severity.** The DB CHECK is `warning|strike|severe_strike` (`004_trust_extras.sql:62`).
- **Configuration.**
  - Default `http://trust-safety-service:8118`: `post-service/cmd/server/main.go:171`.
  - trust-safety port 8091: `trust-safety-service/cmd/server/main.go:34`; compose `:1123,1140`.
  - Not set in the post-service compose block (`:709-816`) or in the four values files.
- **Expiry.** `IssueStrike` never sets `ExpiresAt` (`trust_extras.go:190`), and `expires_at IS NULL` is treated as active (`store :488-489`).
- **Scope.** Standing is checked only in `publishDraftRow` (`post_drafts.go:305`). `CreatePost`, reel drafts, scheduled posts, threads, reposts, live VOD, stories and crossposts skip it (plan P-5 lists file:line). `suspended_until` is enforced nowhere (`006_user_trust_state.sql:1-3,17`). There are no tests.

**Design correction** (plan §6.4, P-5)
- **The route.** `GET /v1/internal/standing/:userId` sits behind an Ed25519 service token: issuer `post-service`, operation `trust_safety:standing.read` only. Admin routes stay pinned to `admin-service`, so post-service gains **no** admin privilege.
- **A canonical response.** `active_strikes` is always an array, with a shared severity enum that equals the DB. trust-safety alone computes `publish_allowed` from a versioned policy, and the placeholder is labelled unapproved.
- **Outage behaviour.** A 60 s cache for allow and deny. Background paths fail closed with backoff, then `blocked` after 24 h. Interactive paths return 503.
- **Configuration.** Fix the URL; refuse to boot outside dev without the URL or the signer.
- **Scope.** One choke point on every publication path, with a route-inventory test.
- **Explicitly not the fix:** granting post-service admin scopes.

**Remaining decisions and dependencies**
- `standing-v1` thresholds and suspension (F-6).
- Interactive fail-open versus fail-closed (F-7).
- Whether visibility widening counts (F-8).
- Legacy permanent strikes (F-15).
- Ed25519 keys (O-2).
- The finding-5 schema, so that "active" is well defined.

**Proposed acceptance tests (PROPOSED)**
- **T4-1:** reproduce the defect in an isolated environment (not observed on dev).
- **T4-2:** authorization matrix.
- **T4-3:** response contract.
- **T4-4:** outage.
- **T4-5:** coverage of every publication path.
- **T4-6:** configuration boot test.

---

## Finding 5: audited, idempotent and reversible strikes

**Status: Verified.** The strike table and methods have no case link, idempotency key, policy version, set expiry, reversal, audit row or outbox. Retries multiply strikes. The only issuing route trusts headers.

**Current code evidence**
- `trust.user_strikes` has only a PK and a `(user_id, created_at)` index: `trust-safety-service/database/migrations/004_trust_extras.sql:56-67`.
- `IssueStrike` mints `uuid.New()` per call and sets no expiry: `trust-safety-service/internal/service/trust_extras.go:182-195` (`:190`).
- `CreateStrike` is a bare INSERT with no transaction, audit or outbox: `store/postgres/trust_extras.go:474-482`.
- Readers treat NULL expiry as active, and there is no void method: `:484-517`.
- `admin_audit.target_type` cannot name a strike: `010_admin_audit.sql:23`.
- `POST /v1/strikes` takes the actor from `X-User-Id` behind the internal key only: `trust_extras_handler.go:328-364`; `handler.go:91-95`.
- The console proxies only the read: `admin-service/internal/http/handler_trust.go:66`.
- No transactional outbox: `internal/purge/purge.go:38-39, 200-201`.
- Stats count voided strikes: `admin_stats.go:44`.

**Design correction** (plan §6.4, §9.3)
- **Schema.**
  - An append-only `trust.strike_policies` table.
  - Strike columns: `case_type`, `case_id`, `policy_version`, `idempotency_key`, `issue_decision_id`, `issued_at`, explicit `expires_at`, and the void fields.
  - Unique indexes per case, idempotency key and decision.
  - The `admin_audit` target-type migration.
  - `trust.enforcement_outbox`.
- **One transaction** for case transition, strike (`ON CONFLICT DO NOTHING` plus a re-read assertion), two audit rows, and outbox rows (restriction command, events).
- **Void is terminal and idempotent.** Strikes are never deleted. Expiry is passive.
- **Daily reconciliation** re-enqueues recorded decisions only.
- **Suspension policy stays open.**

**Remaining decisions and dependencies**
- Strike duration, whether counter-notice or Rule 75 restoration voids a strike, and repeat-infringer and suspension thresholds (L-8, F-6).
- Shared strike groups (F-17).
- Legacy rows with NULL expiry (F-15).
- Retire or token-gate `POST /v1/strikes`.

**Proposed acceptance tests (PROPOSED)**
- **T5-1:** duplicate claims and retries.
- **T5-2:** strike reversal.
- **T5-3:** crash atomicity.
- **T5-4:** expiry and policy immutability.
- **T5-5:** authorization.
- **TM-3:** duplicate claims and strike reversal across two cases.

---

## Finding 6: ownership, licensing and remix abuse

**Status: Verified.** The remix lineage the plan relied on does not exist at runtime. The plan's "Direction = upload time" and "original/copy" wording presented precedence as ownership.

**Current code evidence**
- `remix_source_id`, `remix_type`, `original_reel_id` and `ref_post_id` are declared only:
  - `post-service/database/migrations/011_postgram_features.sql:5-9`
  - `010_postbook_features.sql:1-2`
  - `007_reels_gold_spec.sql:108-116`
  - `post-service/cmd/server/main.go:962,967`

  They have no Go writer or reader (`posts.go:235` `postCols` has none) and no web reference.
- The "remix token" is unsigned JSON: `post-service/internal/service/series.go:57-71`.
- Licence and remix settings are uploader assertions: `hub_settings.go:35-36`.

**Design correction** (plan §1, §8.4)
- **Language.** "Potential match", "your video / matched video", and "similarity confidence" (shown only after calibration). Upload order is shown as a fact. The reviewer screen states that similarity and upload order do not establish ownership.
- **A reviewer checklist** covers:
  - claimant authority and scope;
  - whether the claimant's own video is the later side of another pair (senior tier);
  - licences and collaborations (mandatory for Creative Commons);
  - public domain and exceptions (escalated to counsel);
  - an earlier respondent upload (senior tier, authority evidence required);
  - remixes (informational).
- **Licensing evidence works in v1 without an allowlist.** There is a claimant-scoped `licensed` archive and respondent evidence uploads.
- **No automatic remix exemption.** Future lineage is server-written only. Client-declared lineage is shown as "declared, unverified" and is never an exemption.

**Remaining decisions and dependencies**
- Public-domain and exception criteria, and the agent evidence standard (L-7).
- A pre-decision respondent window (F-10).
- The remix compose flow (F-19).

**Proposed acceptance tests (PROPOSED)**
- **T6-1:** a client-supplied `remix_source_id` gives no exemption.
- **T6-2:** the claimant's video is itself a later upload.
- **T6-3:** the respondent's video is earlier.
- **T6-4:** a Creative Commons reference.
- **T6-5:** a claimant-scoped licensed archive.
- **T6-6:** copy lint.

---

## Finding 7: entitlement, quotas and public-form bypasses

**Status: Verified.** The stored eligibility row and the fresh decision can disagree. There is no service-to-service route. The sweep runs only when monetization writes are on. Quotas were not atomic or shared, and an upheld request could reopen revoked access.

**Current code evidence**
- Stored row: `monetization-service/database/migrations/010_creator_fund.sql:14-27`.
- Fresh, unstored decision: `internal/service/creator_fund.go:214-229`, with `DecideEligibility` at `:114-135`.
- Evaluation and sticky suspension: `:166-203`. Sweep: `:415-433`.
- Unsuspend sets `pending`: `admin_console.go:252-292` (`:289`).
- Caller-scoped routes only: `internal/http/handler.go:195-203, 271`.
- Workers run only with writes enabled: `internal/runmode/runmode.go:48-50, 97`.
- `CheckEntitlement` is viewer subscriptions: `entitlement_handler.go:12-15`.
- No monetization outbox.
- Trust-safety duplicate-index precedent: `008_launch_report_and_appeal_integrity.sql:27, 53`.

**Design correction** (plan §6.5, §8.3, §6.4)
- **"Programme member"** means the stored row is `eligible`, unsuspended, and evaluated ≤ 48 h ago. It is read through a new service-token route. The fresh decision is display-only, and subscriptions are irrelevant.
- **Tool-access precedence:** revocation, then lifecycle or suspension, then admin grant, then an `upheld_request` grant (suppressed by an active revocation), then programme, then deny.
- **Caching and failure.** Programme answers are cached ≤ 60 s. Revocations and grants are never cached. Writes are uncached. An unresolved answer fails closed with 503.
- **Atomic quotas** live in trust-safety's database, keyed per account, claimant hash, claimant-and-target channel, and IP (anonymous only). They are shared by the tool and the public form.
- **A duplicate-case unique index** covers open states.
- **Invalid versus abusive.** `incomplete` does not count. Invalid outcomes count toward a threshold. Bad faith needs two reviewers and revokes on one occurrence.
- **The public form stays open to everyone** but never restores revoked tool access.

**Remaining decisions and dependencies**
- The launch eligibility basis while the fund is in beta (F-2); under the strict definition, no one qualifies today.
- Quota numbers and the invalid threshold (F-3).
- Whether abuse creates a strike (L-9).
- The monetization internal route (P-11).

**Proposed acceptance tests (PROPOSED)**
- **T7-1:** stored versus fresh.
- **T7-2:** stale row.
- **T7-3:** a subscriber is not a member.
- **T7-4:** entitlement revocation while the page is open.
- **T7-5:** monetization outage.
- **T7-6:** concurrent quota.
- **T7-7:** concurrent duplicates.
- **T7-8:** a revoked account using the form.
- **T7-9:** invalid versus abusive.

---

## Finding 8: durable legal notices and evidence retention

**Status: Verified.** The notification consumer is at-most-once and drops on errors. trust-safety has no outbox. Account purges delete what a case needs. There is no legal-hold concept outside dating-service.

**Current code evidence**
- `ReadMessage` with a GroupID auto-commits, and handler errors are only logged: `notification-service/internal/events/consumer.go:83-90, 115-151, 126-128`. The comment at `:173-176` does not hold for this loop.
- The durable precedent: `call_consumer.go:150-210`.
- Suppression lookup failure drops: `notification.go:204-214`. Preferences decide transports: `:216-243`. Realtime and push errors are logged only: `:300-310, 340-343`. There is no email channel.
- **Purges.**
  - trust-safety: `store/postgres/purge.go:23-46`.
  - post-service: `purge.go:95-241` (`:199-202, 223-225, 238`).
  - media: `purge.go:37-56`.
  - Inbox rows are deleted on `user.deletion_requested`: `consumer.go:324-335`.
- **The only retention precedent** is dating-service: `setup.sql:1167-1213`.

**Design correction** (plan §6.4, §6.6, §10)
- **The system of record is trust-safety.** It holds `copyright_legal_notices` (receipt and validation timestamps, sealed content, hash), `copyright_notice_deliveries` (idempotency key, attempts, provider ids, escalation, manual postal record) and restricted `copyright_party_contacts` (pii scope `copyright`).
- **A delivery worker** runs backoff and escalates at 4 h or on a bounce, with an ops alert and an admin queue item. Clocks anchor to receipt, never to delivery. A failed forward blocks dependent actions and never resets a clock.
- **Inbox and push are convenience only.** They carry ids and a case link, go through the trust-safety outbox and a durable consumer, and retry a failed suppression lookup.
- **PII unmasking** needs a permission and step-up, and is audited.
- **Retention classes.** Case evidence is kept until `closed_at + R`, with subject tokens on purge and an ack meaning "erased except documented exceptions". Evidence snapshots are kept instead of videos. `trust.legal_holds` has a mandatory `review_by`, is never auto-released, and overrides every purge path. A daily sweeper removes expired evidence.

**Remaining decisions and dependencies**
- Receipt rules, retention period R, disclosures, postal service (L-10, L-6).
- A transactional notification category (F-9).
- The trust-safety outbox (P-7).
- An email provider and the `copyright` pii scope (O-2).
- Purge changes across three services (P-13).

**Proposed acceptance tests (PROPOSED)**
- **T8-1…T8-8:**
  - a provider outage and escalation with clocks unchanged;
  - a double accept;
  - restoration blocked on an unsent forward;
  - notification-service down;
  - suppression lookup retry;
  - payload content;
  - PII access;
  - an invalid legal-action checkbox.
- **TR-1…TR-5:** respondent and claimant purge, a media hold, the sweeper, and an expired hold review.

---

## Finding 9: candidate recall for multi-index hashing

**Status: Verified, and the cost side matters as much.** Exact lookup on 4×16-bit bands guarantees only Hamming ≤ 3, so the 3/3/2/2 split is missed. Every PostgreSQL layout that is exact to d=10 produces too many candidate rows at catalogue scale.

**Current evidence**
- The plan's "4×16-bit bands … Hamming ≤ 10" named no probe strategy (previous plan text).
- **Pigeonhole:** with m bands probed to radius ρ, a hit is guaranteed for d ≤ m(ρ+1) − 1.
- The combinatorics (probes per frame and expected rows under uniform hashes) were computed with a local script. That is arithmetic, not a benchmark.
- Prod DB: Aurora `db.r7g.large` (`infra/terraform/envs/prod/main.tf:95-97`).

**Design correction** (plan §12.3)
- **Probing.** Radius-2 probing in all four bands: 137 probes per band, 548 per frame, guaranteed for d ≤ 11 at the frame-pair level. Hits are filtered on the full hash. The minimum band distance is recorded, so radius 1 or 0 can be evaluated counterfactually.
- **Phase after trims.** Integer-second anchors (spacing `clamp(informative/12, 1, 5)`) and 2 fps queries give ≤ 0.25 s phase error for any trim.
- **Duration buckets.** Query every bucket overlapping [0.75·C, 1.25·C], derived from the coverage rules, so nothing is lost at a boundary.
- **Low-entropy hotspots.** A stop-list, a whole-key drop at 257 postings, a per-upload 2M row cap, and a nightly hot-key list. Every truncation is logged and never counted as "no match".
- **A scale gate** (p95 ≤ 2M rows and ≤ 5 s) with two options past it: radius 1 with a measured ≤ 1 pp recall loss, or a dedicated index.
- **Insert-then-query** removes concurrent misses.
- **The pipeline is explicitly not called exact.** Only the frame-pair lookup is, because frames are sampled and hot keys capped. The claim is measured recall against exhaustive search (E2, E3).

**Remaining decisions and dependencies**
- Accept "not exact" and choose the post-gate option (F-12).
- The windowed-query recall loss is measured in E3.
- Index placement (O-3).

**Proposed acceptance tests (PROPOSED)**
- **T9-1:** the 3/3/2/2 case.
- **T9-2:** a property test for d ≤ 11.
- **T9-3:** the probe generator.
- **T9-4:** one-second and sub-second trims.
- **T9-5:** duration-bucket boundaries.
- **T9-6:** hot keys.
- **T9-7:** concurrent insert-then-query.
- **Evaluations:** E2 and E3.

---

## Finding 10: matching thresholds and audio contradictions

**Status: Verified.**
- "Copy ≥ 60%" does not exclude compilations: 6 minutes of reference inside 10 minutes scores 0.60.
- Dropping static frames contradicts "≥ 8 informative frames".
- The audio-only promise contradicts the evidence rules.
- "≥ 10 s" excludes identical short clips.
- Hamming ≤ 10 and the "survives" list were unvalidated.

**Current evidence**
- Previous plan lines on fingerprinting and matching (the "Fingerprinting" section of the superseded one-page plan).
- `MinVideoDurationSeconds = 3` (`media-service/internal/processing/video.go:298-299`, HEAD).

**Design correction** (plan §12.2, §12.4, §12.5)
- **Border crop.** One crop per video.
- **Weights.** Flat frames weigh 0. Static runs are kept for alignment but capped at 2 s of weight.
- **Alignment.** τ = 10 is a tuning start, not a fact. Gaps ≤ 3 s are bridged but add no weight. Up to 8 monotonic segments.
- **Coverage denominators** are informative weight on each side.
- **Classes.** `full_or_near_full` (ref ≥ 0.85, copy ≥ 0.80, ≥ 8 informative seconds, diversity ≥ 3, reference ≥ 10 s) is the only actionable class. `contains`, `partial` and `short_clip_candidate` are shadow-only. `insufficient_evidence` is an explicit outcome.
- **Audio-only matching and the in-house audio fallback are deferred from v1.** Shared music cannot create or strengthen a match.
- **The evaluation** measures precision **and** recall: an E1 labelled set with ffmpeg-transformed positives and ≥ 1,000 hard negatives (including shared music, slides and compilations), E2 against exhaustive search, and E3 shadow sampling of all pairs found by either path. It has proposed pass criteria.

**Remaining decisions and dependencies**
- The pass criteria (F-12).
- Deferring audio (F-13).
- The corpus (L-12).
- τ, the gap and the static cap are fixed only after E1.

**Proposed acceptance tests (PROPOSED)**
- **T10-1:** compilations.
- **T10-2:** static slides.
- **T10-3:** shared music.
- **T10-4:** short clips.
- **T10-5:** black padding.
- **T10-6:** letterbox and pillarbox.
- **T10-7:** gaps.
- **Evaluations:** E1–E3.

---

## Finding 11: generation fencing, pair identity and upload time

**Status: Verified, and extended.** `media_assets.created_at` is not a safe substitute for upload time either. It is stamped at upload init, and a resumable session can stay open for 24 h.

**Current code evidence**
- **No generation column:** `media-service/database/setup.sql:3-24`; `021_transcode_lease.sql:22-24`.
- **Rebuild in place while ready:** `media-service/internal/store/postgres/media_outbox.go:211-213, 278-300`.
- **Deterministic keys overwritten:** `cmd/worker/main.go:582-597, 638-649, 712-752`; the rotation override replaces the original at `:477-501` (HEAD).
- **The original is not pinned:** `media-service/internal/service/media.go:281-283, 349-357`.
- **`created_at` at init:** `media.go:305-308`; `resumable.go:18, 57, 70`; `orphan_gc.go:26, 43`.
- **Scheduled publish rewrites `posts.created_at`:** `post-service/internal/store/postgres/scheduled.go:72-80, 104-111`.
- **One asset can back many posts:** `post-service/database/setup.sql:22-27`.
- **The completion payload has no generation:** `transcode_inbox.go:112-119`.

**Design correction** (plan §6.1, §6.2)
- **New columns:** `media_generation`, `ready_generation`, write-once `upload_confirmed_at`, and `original_etag`.
- **Requeue** bumps the generation and supersedes fingerprints and pairs in the same transaction.
- **Completion** records `ready_generation` only for the current generation.
- **The fingerprint write is fenced** (`FOR SHARE` plus the claim token), and stale results are discarded.
- **`algo_version`** is part of every key.
- **Upload precedence** comes from `upload_confirmed_at`, with a 60 s contemporaneous window and legacy ambiguity. It is never `posts.created_at` and never ownership.
- **Pair identity versus match identity.** Media pairs are unique on `(media_lo, gen_lo, media_hi, gen_hi, algo)`. `copyright_matches` is unique on `(reference_owner_id, copy_post_id)`, with `copyright_match_evidence` mapping many-to-many. Archive and "already surfaced" state persist and are never reset.

**Remaining decisions and dependencies**
- P-14, including the original-object pinning.
- Trace the resumable completion path.
- Observation O-obs-1 (stall sweeper) was not verified in depth.

**Proposed acceptance tests (PROPOSED)**
- **T11-1…T11-8:**
  - stale results after reprocessing;
  - invalidation;
  - stale postings ignored;
  - scheduled-post precedence;
  - resumable precedence;
  - contemporaneous and legacy direction;
  - archive persistence;
  - an old-generation completion.

---

## Finding 12: safe outbox migration and queue isolation

**Status: Verified, with more dependents than listed.** Dropping `UNIQUE (media_asset_id, event_type)` breaks three `ON CONFLICT` writers and silently changes four readers and two purge paths. The relay stops on the first failure.

**Current code evidence**
- **Constraint:** `media-service/database/migrations/013_media_event_outbox_and_quarantine.sql:6-20`.
- **Writers:** `QueueTranscode` (`media_outbox.go:51-56`), `completeTranscodeTx` (`transcode_inbox.go:157-162`), the subtitle snapshot (`subtitle_events.go:137-150`).
- **Readers:** `transcode_lease.go:175-187, 348-365, 384-389`; `media_outbox.go:266-293`. Purge: `asset_purge.go:167`, `purge.go:62`.
- **Relay:** one loop, 100 rows ordered by `created_at`, returns on the first failure, 2 s ticker (`media-service/internal/service/media_outbox.go:16, 33, 46-55`; order at `store/postgres/media_outbox.go:85-92`).
- **Topic:** one topic keyed by `event_id` (`internal/events/producer.go:50-64`), consumed by the worker, post-service and search-service.

**Design correction** (plan §6.1, §9.1, §13)
- **Leave `media_event_outbox`, its constraint and its relay untouched.**
- **A separate pair path.** `copyright_pair_outbox` is unique on `(pair_id, pair_revision)`, with no FK. It has its own relay: `SKIP LOCKED` lease claims, per-row backoff, **continue on failure**, a budget, and a kill switch. It publishes to `media.copyright.pairs`, keyed by `pair_id`. The consumer uses an inbox and a per-pair revision check.
- **Purge** writes invalidations first.
- **Why separate tables are not enough.** Readiness is protected by admission control and the readiness gate (finding 13), because separate tables do not remove CPU, memory or DB contention.

**Remaining decisions and dependencies**
- A new topic versus a polled read (F-16).
- Topic provisioning and ACLs (O-2).

**Proposed acceptance tests (PROPOSED)**
- **T12-1:** existing outbox behaviour unchanged.
- **T12-2:** a poison pair row.
- **T12-3:** a 100k backlog does not delay transcode events.
- **T12-4:** duplicate claim and crash.
- **T12-5:** reordered revisions.
- **T12-6:** purge.
- **TX-1…TX-5:** crashes and retry recovery.

---

## Finding 13: measured resource and capacity gates

**Status: Verified.** The CPU and storage figures were estimates. "1 CPU" described dev only. No readiness latency metric exists.

**Current code evidence**
- **Dev:** 1 CPU / 1G (`Architecture/docker/docker-compose.yml:940-967`, HEAD). An uncommitted working-tree change raises the dev worker to 4 CPU / 2G.
- **Staging:** limits of 2 CPU / 2Gi (`deploy/services/media-service/values-staging.yaml:90-105`).
- **Prod:** 3 replicas at 1–4 CPU and 1–4Gi, with no HPA (`values-prod.yaml:89-104`). No worker readiness probe (`charts/atpost-service/templates/worker-deployment.yaml:121`).
- **Prod DB:** Aurora `db.r7g.large` (`infra/terraform/envs/prod/main.tf:95-98`).
- **The worker** handles one job at a time (`main.go:195-219`) and downloads the whole original (`main.go:456`; `blob/store.go:243`).
- **Metrics.** Only wall time is measured (`main.go:41-45, 283-286`). `OldestPendingTranscodeAge` is never called (`transcode_inbox.go:168-175`).
- **Storage model:** 72M anchors at 100,000 hours equals 288M postings, about 23–35 GB estimated.

**Design correction** (plan §13)
- **Environments** are listed per environment.
- **Four independent kill switches.**
- **Admission:** worker idle, transcode backlog < 60 s, DB health.
- **Pre-emption:** cancel on transcode, with segment checkpoints.
- **Memory:** streaming only, with an RSS target of 512 MiB.
- **Deadlines** scale with duration. Statement timeouts and candidate caps apply.
- **Backfill:** a token bucket, a peak pause and a backlog stop.
- **Alerts and a circuit breaker.**
- **Readiness gate.** New confirm→ready and queue-wait metrics with a 14-day baseline; a regression of ≤ max(5%, 15 s) at p95/p99. Unchanged readiness is not promised.
- **Benchmarks B1 (extraction), B2 (index on the prod DB class) and B3 (contention)** have datasets, hardware, metrics and PROPOSED pass criteria.

**Remaining decisions and dependencies**
- Budgets and a separate Deployment (F-14).
- Index placement (O-3).
- The upload trace (O-2).
- The P-16 metrics before any baseline.
- Re-read dev resources after the in-progress change lands.

**Proposed acceptance tests (PROPOSED)**
- **T13-1…T13-4:** kill switch, pre-emption, long input, circuit breaker.
- **TB-1…TB-4:** backfill pressure while new uploads are transcoding.
- **Benchmarks:** B1–B3.

---

## Finding 14: worker image build prerequisite

**Status: Disputed as to the committed state. A real CI/image gap remains.**

The finding says `Dockerfile.worker` builds only `cmd/worker/main.go` while `main.go` references symbols in `transcode_lease.go`. At HEAD that is not true, and no commit has ever contained the defective combination.

**Git evidence**
- `git show HEAD:Architecture/services/media-service/Dockerfile.worker` builds the **package**: `RUN go build -mod=vendor -o /worker ./services/media-service/cmd/worker`. The comment explains that `cmd/worker` has more than one source file (lines 21-23).
- `git show HEAD~1:…/Dockerfile.worker` line 22 was `…/cmd/worker/main.go`.
- `git show --stat bad0219e` changes `Dockerfile.worker` (5 lines) in the **same commit** that adds `cmd/worker/transcode_lease.go` (216 lines). Its message lists "Dockerfile.worker builds the package, not main.go alone".
- `git ls-tree HEAD~1 …/cmd/worker/` contains only `main.go` and two `_test.go` files, so the single-file build was correct at HEAD~1. The defective pairing existed only in an uncommitted working tree.
- HEAD `main.go` does reference `transcode_lease.go` symbols: `newBusyTracker` (:176), `stallSweepEnabled`/`runStallSweeper` (:177-178), `supersededDelivery` (:270), `startTranscodeHeartbeat` (:280). CI compiles the package (`.github/workflows/go-ci.yml:31-32`).

**The real remaining gap**
- CI never builds or publishes the worker image. `build-push.yml:148-163` builds only `<service>/Dockerfile`, and `build-push-acr.yml:170` does the same. No workflow references `Dockerfile.worker`.
- No `media-worker` ECR repository was found in `infra/terraform`.
- The tag bump (`build-push.yml:198`) rewrites **every** `tag:` line in the values file, including `worker.image.tag` (`values-prod.yaml:96`, `values-staging.yaml:97`), to a SHA never pushed for the worker.
- The tag-bump effect is inferred from the workflow text. It was not observed in a run. No image build was executed in this pass.

**Design correction** (plan §4 P-17, §14.2 Phase 1). Recorded as a prerequisite and not implemented here:
- keep the package build;
- add a CI build and push of `atpost/media-worker:<sha>` for `linux/arm64`;
- provision the ECR repository;
- make the tag bump key-specific;
- the image gate must pass before any worker-image package change (including phase-2 chromaprint).

**Remaining decisions and dependencies**
- The CI owner confirms whether the worker image is built out of band (O-1).
- ECR provisioning (O-2).

**Proposed acceptance tests (PROPOSED)**
- **T14-1:** an arm64 buildx build of `Dockerfile.worker` succeeds.
- **T14-2:** `/worker` exits with a configuration error, not a missing symbol; `ffmpeg -version` works.
- **T14-3:** a CI dry run pushes both images and bumps only the pushed tags.
- **T14-4:** removing `transcode_lease.go` fails the build.

---

## Finding 15: explicit fingerprint input contract

**Status: Verified at HEAD.**
- A 360p MP4 is not guaranteed, and `MinVideoResolution` is never referenced.
- A successful transcode does guarantee an HLS lowest rung.
- Alpine 3.19's chromaprint package ships `fpcalc`, according to primary package data; runtime behaviour is unverified.
- **The rung guarantee must be re-verified after the in-progress HLS change lands.** That change is uncommitted, and it builds HLS rungs by stream-copying the MP4 renditions, with a per-rung re-encode fallback.

**Current code evidence** (HEAD)
- **`MinVideoResolution`** is declared at `video.go:301-302` with no references.
- **MP4 ladder.** Rungs are skipped when the source is shorter (`main.go:519-532`). A failed rendition is silently absent (`video.go:340-371, 396-449`), and its `transcoding_jobs` row stays `processing` (`main.go:543, 613-621`). A 426×240 source produces no MP4 (`main.go:625-627`).
- **HLS.** The first rung is always kept, including as an upscale (`video.go:40-54`). A rung failure fails HLS (`:95-99`), and an HLS failure fails the transcode (`main.go:706-711`). Playlists use relative paths (`video.go:79-80, 114-115`).
- **Legacy.** `hls_master_key` was added later (`setup.sql:76`; `transcode_inbox.go:143`), so older ready videos may lack HLS. The count is unverified.
- **Alpine v3.19.** `chromaprint` 1.5.1-r6 (community) ships `/usr/bin/fpcalc` and links FFmpeg 6 sonames matching `ffmpeg 6.1.1-r0` (https://pkgs.alpinelinux.org/contents?file=fpcalc&branch=v3.19&arch=x86_64; APKBUILD at https://gitlab.alpinelinux.org/alpine/aports/-/raw/3.19-stable/community/chromaprint/APKBUILD).
- **fpcalc `-length` defaults to 120 s.** `0` means unlimited (https://raw.githubusercontent.com/acoustid/chromaprint/v1.5.1/src/cmd/fpcalc.cpp). Recorded as P-19.

**Design correction** (plan §12.1, §12.6)
- **Input selection.** Prefer the lowest HLS rung, chosen by lowest `RESOLUTION` height with a bandwidth tie-break, streamed through the worker's own blob client in playlist order. Fall back to the smallest existing MP4 (legacy), then the original by range reads under a cap.
- **No presigned or CDN URLs.** A duration consistency check. `input_kind`, `input_ref` and ETags are recorded. The write is fenced by generation.
- **Re-verification.** Tests T15-1, T15-2 and T15-6 must pass against the repackaging pipeline once it lands.
- **Audio is deferred (finding 10).** The worker-image smoke gate G1–G8 (including `-length 0`, the raw-PCM pipe, determinism, and linkage) is specified as a **future** phase-2 gate that runs after P-17.

**Remaining decisions and dependencies**
- Enforcing `MinVideoResolution` is a product decision outside this feature (F-20).
- The legacy no-HLS count needs to be measured.
- Re-verify after the HLS change.
- Deferring audio (F-13).

**Proposed acceptance tests (PROPOSED)**
- **T15-1:** a 240p source gets an upscaled 360p rung.
- **T15-2:** a failed 360p MP4, including the fallback encode under the new pipeline.
- **T15-3:** legacy fallbacks.
- **T15-4:** no presigned URLs.
- **T15-5:** reprocess mid-read.
- **T15-6:** reel and 4K sources.
- **G1–G8:** the phase-2 fpcalc image gate.
