# Copyright Match: design

**Status: design only. Nothing here is approved.** No founder decision, counsel decision, test, benchmark, image build or evaluation named in this document has been executed or approved. Every test, benchmark, threshold, budget and pass criterion is **PROPOSED**.

- Prepared 29 September 2026. It replaces the one-page plan of the same date and answers the 15 review findings. The finding-by-finding response is in `docs/designs/copyright-match-review-response.md`.
- Code evidence was read at commit `bad0219e` (branch `codex/module-01-02-launch-safety`). Paths are relative to `Architecture/services/` unless they start with `Architecture/`, `deploy/`, `infra/`, `.github/` or `postbook-ui/`. For `media-service/cmd/worker/main.go`, `media-service/internal/processing/video.go` and `Architecture/docker/docker-compose.yml`, line numbers are **HEAD** line numbers, because the working tree has uncommitted edits to those files (see P-18 and section 13).
- Reference behaviour, used for function only: YouTube's Copyright Match Tool. It finds full or near-full reuploads of a creator's videos on other channels, and the creator reviews them, archives them or requests removal.
- `P-n` is an implementation prerequisite (an existing defect). `Tn-m` is a PROPOSED test. `B1`–`B3` are PROPOSED benchmarks. `E1`–`E3` are PROPOSED evaluations. `F-n` / `L-n` / `O-n` are open decisions for the founder, counsel and operations.

---

## 1. Scope and explicit non-goals

**Goal (narrow).** Find **full or near-full video reuploads**. Show eligible creators each **potential match** with a **similarity confidence**. Let them archive it or submit a removal request. Removal is decided by a **human reviewer**, and there is a counter-notice or restoration process for each applicable legal regime.

**Principles that bind every part of this design:**

1. **Similarity and upload order never establish ownership.** A potential match means "these two videos look alike". The earlier upload is not proven to be the owner, and the later upload is not proven to infringe. UI, API and reviewer text say "your video" and "matched video", never "original", "copy", "infringing", "stolen" or "owner" about either party. (In this document, "reference" and "copy" are only internal names for the two sides of a pair.)
2. **The platform never removes anything automatically.** Every removal is a human decision on a case.
3. **A removal is a case-specific hold.** Holds are separate from safety moderation. Releasing one never affects another hold, a safety decision, deletion, scheduling or audience.
4. **Discovery is stricter than viewing.** A viewer who may legitimately watch a video (a follower, a private-share recipient, a holder of an unlisted link) does not thereby get to discover it through Copyright Match.
5. **Fail closed** on entitlement, standing, privacy and generation checks.

**Non-goals (not in v1):**

- Audio matching in any form: audio-only matches, audio as confidence, `fpcalc`, and an in-house audio fingerprint (section 12).
- Partial matches, compilations ("contains") and short clips (under 10 s) as actionable results. They are recorded in shadow only.
- Mirroring, large crops, speed changes, picture-in-picture, camcorder recordings, blurred-fill pillarbox.
- Unlisted, followers-only, private-share, close-friends, age-restricted, scheduled, deleted or account-private copies as matches.
- Automated licensing allowlists, automatic remix exemptions, a pre-publish uploader check, courtesy notices with scheduled removal, realtime updates.
- Adjudicating fair use, fair dealing, public-domain status or licences. Reviewers record these and escalate them to counsel.
- An "exact" search claim. The pipeline is not exact. Its claim is the recall measured in section 12.

---

## 2. Current state

**Nothing to fingerprint with today.** There is no perceptual hashing, audio fingerprinting, pgvector or embedding anywhere. Postgres is `postgis/postgis:16-3.4`, with no vector extension. There is no copyright, takedown or counter-notice workflow. A grep finds only the unused `check_type` enum in `post-service/database/migrations/011_postgram_features.sql`.

**What can be reused:**

- ffmpeg in the media worker (`media-service/Dockerfile.worker`, `alpine:3.19`).
- The HLS ladder, which guarantees a lowest rung for every video that reaches `ready` (section 12.1).
- The DB-polled job pattern with `FOR UPDATE SKIP LOCKED` and claim tokens (`media_caption_jobs`, `media_audio_tracks`, transcode leases).
- The `busyTracker` idle signal (`media-service/cmd/worker/transcode_lease.go:101-132`).
- The media outbox and `media.events` topic, which stay **untouched**.
- post-service's signed-capability pattern (`Architecture/shared/moderationcap/capability.go`), which is used as a template for a **new** capability, not extended.
- trust-safety's reports, appeals and grievances, the append-only `trust.admin_audit` (`trust-safety-service/database/migrations/010_admin_audit.sql`), and the admin-console token proxy (`admin-service/internal/http/handler_trust.go`).
- `Architecture/shared/servicetoken` (Ed25519 service tokens), `Architecture/shared/pii` (sealed PII and lookup hashes), `Architecture/shared/outbox` and `Architecture/shared/mailer`.
- The durable consumer pattern in `notification-service/internal/events/call_consumer.go:150-210`.
- The retention precedent in `dating-service/database/setup.sql:1167-1213`.

**Not reused:** the dead `media_rights_checks` / `rights.go` in post-service and the `CopyrightCheck` / `CopyrightClaim` web types. They have an uploader-side shape; a pre-publish check is deferred.

**Existing defects that block this feature** are listed in section 4. The plan's earlier statement that the strike bug means "strikes enforce nothing anywhere" was incomplete: by code reading, the same check also blocks composer-draft publication (P-5).

---

## 3. Service ownership and trust boundaries

| Service | Owns | Never does |
|---|---|---|
| media-service | Media generation and upload-time identity; fingerprint jobs, fingerprints, anchor postings and hot-key lists; media **pairs** (media facts only); the pair outbox and topic `media.copyright.pairs`; signing one still per thumbnail request | Audience, privacy, entitlement or ownership decisions. It exposes no pair data to viewers. |
| post-service | `copyright_matches` (post-level match identity), the discovery predicate, "surfaces once", archive state, the Matches API, the per-case **restriction** rows and the effective eligibility they produce | Deciding cases, issuing strikes, or releasing a hold on its own authority |
| trust-safety-service | Tool access (grants, revocations, programme lookup); cases and their state machine; legal notices, delivery records and sealed contacts; counter-notices; filed-action evidence; strikes and strike policy; the standing answer; legal holds and case-evidence retention; the enforcement outbox; audit | Changing `posts.review_status` for copyright reasons |
| monetization-service | One internal read: "is this user a creator-programme member" | Granting tool access. It does not answer viewer subscriptions to this feature. |
| admin-service and console | The reviewer queue UI, with permissions and step-up. It acts **only** through trust-safety case transitions. | Direct post mutation for copyright |
| notification-service | Convenience pings: ids-only inbox and push, with a safe case link | Legal delivery. It is not a system of record. |

**Trust boundaries:**

| Boundary | Mechanism | Notes |
|---|---|---|
| trust-safety → post-service (place or release a hold) | New HMAC capability `post_restriction` with its own key `POST_RESTRICTION_HMAC_KEY` (plus `_PREVIOUS`), ≥ 32 bytes, held only by trust-safety and post-service; TTL ≤ 15 min | Separate from `POST_MODERATION_HMAC_KEY`. `moderationcap.Claims` is **not** modified, because its HMAC is computed over the marshalled struct (`moderationcap/capability.go:124`) and is shared with the story protocol (`post-service/internal/consumers/story_moderation.go:93`). Defence in depth, once trust-safety has an Ed25519 signer: also require a service token with operation `post:restrictions.write`. |
| post-service → trust-safety (standing) | Ed25519 service token: audience `trust_safety`, operation `trust_safety:standing.read`, issuer `post-service`, TTL 60 s | post-service's key registers **only** this operation. Admin routes stay pinned to issuer `admin-service` (`trust-safety-service/internal/http/admin_token.go:204`). |
| post-service → trust-safety (open case from Matches) | Service token, operation `trust_safety:copyright.case.create` | trust-safety calls back to verify (next row) |
| trust-safety → post-service (verify match, read restrictions) | Service token, operations `post:copyright.verify` and `post:restrictions.read` | |
| trust-safety → monetization (programme) | Service token, operation `monetization:creator_programme.read` | Replaces "internal key plus header". This is the lesson of P-5. |
| post-service → media-service (sign one still) | Service token, operation `media:copyright.still.sign` | Returns one URL signed with `MaxProtectedTTL` (5 min) |
| Viewer → post-service / trust-safety | Existing user auth through the gateway; `/internal/` paths are refused at the edge (`api-gateway/pkg/internalroutes/internalroutes.go`) | |
| Reviewer → trust-safety | admin-service token with `act` claim, a new `trust_safety:copyright.*` permission, and step-up for decisions, PII reads and legal holds | |

**New permissions** (admin catalogue, `admin-service/internal/adminauth/catalogue.go`):
- `trust_safety:copyright.review`: read cases and evidence (masked).
- `trust_safety:copyright.decide`: approve, reject or reverse; step-up.
- `trust_safety:copyright.legal`: senior reviewer or counsel-designated. Covers the regime, filed-action evidence, court orders and re-imposition; step-up.
- `trust_safety:copyright.pii.read`: unmask contacts; step-up; each read audited.
- `trust_safety:copyright.legal_hold`: place or release legal holds; step-up.
- `trust_safety:copyright.access.manage`: grants, revocations, reinstatement; step-up.

---

## 4. Implementation prerequisites (existing defects)

These defects exist in the code today. None was fixed in this pass. "Blocks" names the earliest phase (section 14) that cannot start until the item is done.

| ID | Defect | Evidence (file:line) | Required correction | Blocks |
|---|---|---|---|---|
| **P-1** | Post moderation has one `review_status` and no concept of a case, source or hold. The internal moderation route hardcodes `source=appeal` and passes any signed decision through. Its `source` is limited to `admin` or `appeal` in both code and schema. The capability `Claims` has no case, action family, source or expected state. `PolicyVersion` is signed but never stored. | `post-service/internal/store/postgres/moderation_authority.go:20-26, 78-89, 94-98, 148-156, 159-171, 193-202`; `post-service/database/migrations/033_post_moderation_authority.sql:10`; `post-service/internal/http/moderation_authority_handler.go:106-137` (`Source:"appeal"` at :135); `Architecture/shared/moderationcap/capability.go:22-36, 104-136` | Case-specific restrictions (section 6.2), with a new capability (section 3). Do **not** add `copyright` to `source`. | Phase 3 |
| **P-2** | Four other writers can lift `rejected`/`needs_changes`, and the author can undelete: the legacy moderator route, the admin-token route, resubmit then reviewer or ML auto-resolve, and `RestorePost`. | `post-service/internal/http/handler.go:136`; `post-service/internal/http/admin_token.go:454-464`; `post-service/internal/store/postgres/posts.go:1418-1438, 1442-1453`; `post-service/internal/http/feedback_handler.go:63-103`; `post-service/internal/store/postgres/posts_lifecycle.go:41-72` | Holds are never expressed through `review_status` or `deleted_at`. None of these writers can touch restrictions (section 6.2). | Phase 3 |
| **P-3** | Ordinary appeals are not tied to a decision. At adjudication the overturn reads the **fresh** revision, so its check covers only milliseconds. The canonical mutation runs before the local transition, so an uphold and an overturn racing each other can leave the appeal `upheld` and the post `approved`. | `trust-safety-service/internal/service/trust_extras.go:56-59, 63-64, 106-120` (fresh `ContentRevision` at :117), `:115-128`; `trust-safety-service/internal/store/postgres/trust_extras.go:197-229`; `trust-safety-service/internal/service/post_moderation_http.go:73-78`; `trust-safety-service/database/migrations/008_launch_report_and_appeal_integrity.sql:53-55` | Appeal rules at submission, adjudication and final mutation (section 6.3) | Phase 3 |
| **P-4** | Viewer reads hardcode `review_status = 'approved'`: 56 references in 16 non-test files. The cache revalidation and the search-eligibility event read `review_status`. `SearchEligible` is an allowlist, but not every consumer has been audited. | e.g. `post-service/internal/store/postgres/reel_feed.go:33,42,123,132,183,192`, `posts.go:825,909,972`, `channels.go:300,313,363`, `end_screens.go:91`, `live_vod.go:153`; `post_access_state.go:11-40`; `search_eligibility.go:75-118`; `Architecture/shared/events/events.go:679-689` | One helper for viewer eligibility on `effective_review_status`; a source-scan guard test; a consumer audit (section 6.2) | Phase 3 |
| **P-5** | **The standing check for composer drafts is broken in five ways.** (a) It calls the admin-only `GET /v1/strikes/:userId` with only the author's `X-User-Id` and the internal key, so the answer is 403. (b) It expects `data:[...]`, but the server returns `data:{items:[...]}` (and `items:null` when there are no strikes). (c) Its severity switch (`ban|suspend|suspension|severe`) can never match the DB values (`warning|strike|severe_strike`). (d) The default URL port is 8118, but trust-safety listens on 8091, and no post-service environment overrides it. (e) Every failure maps to `ErrStandingUnknown`, so the draft publish fails closed and a scheduled draft is retried every 60 s forever. Standing is checked on **one** publication path only. Strikes never get an `expires_at`, so they are permanent. `trust.user_trust_state.suspended_until` is enforced nowhere. There are no tests. **Identified by code reading, not observed failing:** on dev the composer drafts table `post_drafts` is empty, so the defect is latent there. | `post-service/internal/service/post_drafts.go:235-286, 305-318, 428-441, 467, 472-475, 481-492, 498-501`; `post-service/internal/store/postgres/post_drafts.go:314-321`; `post-service/cmd/server/main.go:171, 174, 543-567`; `trust-safety-service/cmd/server/main.go:34`; `Architecture/docker/docker-compose.yml:709-816` (post-service block, no URL), `:1123,1140`; `deploy/services/post-service/values-{prod,staging,azure-prod,azure-staging}.yaml` (no URL); `trust-safety-service/internal/http/trust_extras_handler.go:366-370, 382`; `admin_token.go:242-247`; `trust-safety-service/internal/store/postgres/trust_extras.go:488-489, 496-507`; `trust-safety-service/database/migrations/004_trust_extras.sql:62`; `006_user_trust_state.sql:1-3,17`; unchecked paths `post.go:855, 3221, 3239, 3483`, `drafts.go:247, 342`, `schedule.go:317, 397`, `postschedule/worker.go:125`, `threads.go:65`, `live_vod.go:85`, `story_surface.go:202`, `crosspost.go:17` | Narrow standing contract (section 6.4). Do **not** grant post-service admin rights. | Phase 0 (stand-alone fix) |
| **P-6** | Strikes have no case link, idempotency key, policy version, explicit expiry, reversal, audit row or outbox. Retries multiply them. `IssueStrike` mints a new id per call and never sets `ExpiresAt`. `admin_audit` cannot name a strike. The only issuing route trusts the `X-User-Id`/`X-Scopes` headers behind the internal key alone. The admin console can only read strikes. Admin stats count voided strikes. | `004_trust_extras.sql:56-67`; `trust-safety-service/internal/service/trust_extras.go:182-195` (`uuid.New` at :190); `trust-safety-service/internal/store/postgres/trust_extras.go:474-482, 484-517`; `010_admin_audit.sql:16-41` (target CHECK at :23); `trust_extras_handler.go:328-364`; `trust-safety-service/internal/http/handler.go:91-95`; `admin-service/internal/http/handler_trust.go:66`; `trust-safety-service/internal/store/postgres/admin_stats.go:44` | Strike schema, atomic decision and void (section 6.4) | Phase 0 |
| **P-7** | trust-safety has no transactional outbox. Purge acknowledgements are published directly. | `trust-safety-service/internal/purge/purge.go:38-39, 200-201` | `trust.enforcement_outbox` and a dispatcher (section 6.4) | Phase 0 |
| **P-8** | **Media gate gaps.** (a) The byte gate `viewerMayAccessPostMedia` does not check account privacy, hidden (deactivated or pending-deletion) authors, or `publish_at`. (b) The anonymous poster route does not check account privacy, hidden authors or `age_restricted`. (c) `circle` is admitted by the detail gate but denied by the media gate. (d) Body-cache revalidation omits `publish_at` (fail-closed today). (e) Realtime rooms are authorized only at join. (f) CloudFront headers on the protected behaviour are unverified. | `post-service/internal/service/story_surface.go:585-624, 656-677`; `story_policy.go:73-79`; `media-service/internal/delivery/public_poster.go:113-143`; `media-service/internal/store/postgres/public_post_media.go:58-76`; `post-service/internal/service/post.go:2869`; `post_cache.go:52-67`; `notification-service/internal/ws/rooms.go:30-57` | Matches never uses these gates as authorization (section 8). Fixing each gate is a separate ticket. | Phase 4 (documented; not a code blocker for Matches) |
| **P-8 status (29 Sep)** | (a), (c), (d) fixed in post-service commit after 65d35a31 (`media_access.go`); (b) closed by retiring the poster SQL authority (`public_poster.go` / `public_post_media.go` deleted; the anonymous rule now lives in post-service); (e) the live `post:<id>` rooms are in ws-gateway `postrooms.go`, not notification-service — follow-up lane; (f) still unverified. | — | — | — |
| **P-9** | **Presigned URL lifetimes.** Protected delivery is capped at 5 min, and "the TTL IS the revocation window". Other presigns outside the delivery gate live 15 min, 30 min or 24 h. | `media-service/internal/delivery/signer.go:66-72`; `media-service/internal/service/media.go:31`; `frames.go:65, 93`; `audio.go:146, 372, 478` | Matches uses only 5-minute single-still URLs. The fingerprint worker uses no URL (section 12.1). | Phase 4 |
| **P-10** | **Remix lineage is never written.** `remix_source_id`, `remix_type`, `original_reel_id` and `ref_post_id` are declared columns with no Go writer or reader and no web reference. The "remix token" is unsigned JSON. | `post-service/database/migrations/011_postgram_features.sql:5-9`; `010_postbook_features.sql:1-2`; `007_reels_gold_spec.sql:108-116`; `post-service/cmd/server/main.go:962, 967`; `post-service/internal/service/series.go:57-71` | No remix exemption in v1. Future lineage is written server-side only (section 8.4). | — (design constraint) |
| **P-11** | **The monetization programme answer.** There is a stored eligibility row and a separate fresh decision that is not stored. There is no service-to-service route and no outbox. The eligibility sweep runs only when monetization writes are enabled. `CheckEntitlement` is about viewer subscriptions, not programme membership. | `monetization-service/database/migrations/010_creator_fund.sql:14-27`; `monetization-service/internal/service/creator_fund.go:114-135, 166-203, 214-229, 415-433`; `admin_console.go:252-292`; `monetization-service/internal/http/handler.go:195-203, 271`; `monetization-service/internal/runmode/runmode.go:48-50, 97`; `entitlement_handler.go:12-15` | New internal read. The definition fails closed (section 8.3). | Phase 5 |
| **P-12** | **Notifications are best-effort.** The main consumer uses `ReadMessage` with a GroupID, which auto-commits, and handler errors are only logged. A failed suppression lookup drops the notification. Preferences can switch the inbox off. Realtime and push failures are logged only. There is no email channel. | `notification-service/internal/events/consumer.go:83-90, 115-151, 126-128, 173-176`; `notification-service/internal/service/notification.go:204-214, 216-243, 300-310, 340-343` | Legal delivery lives in trust-safety. The ping goes through the durable consumer pattern (section 6.4, 9). | Phase 5 |
| **P-13** | **Purge versus legal hold.** Account purge deletes strikes, appeals, grievances, moderation decisions, reports and posts. Media purge reclaims every blob. There is no legal-hold concept outside dating-service. | `trust-safety-service/internal/store/postgres/purge.go:23-46`; `post-service/internal/store/postgres/purge.go:95-241` (:199-202, :223-225, :238); `media-service/internal/store/postgres/purge.go:37-56`; `notification-service/internal/events/consumer.go:324-335`; `post-service/internal/postpurge/worker.go:62` | Retention classes, subject tokens and legal holds (section 10) | Phase 5 |
| **P-14** | **No media generation or upload-time identity.** Reprocessing rebuilds variants and HLS in place under the same keys while the asset stays `ready`. The completion payload carries no generation. `media_assets.created_at` is set at upload **init** and can be up to 24 h early through resumable sessions. `posts.created_at` is rewritten on scheduled publication. One media asset can back many posts. The original object is not pinned after confirm: the 15-minute presigned PUT has no ETag check. | `media-service/database/setup.sql:3-24`; `media-service/internal/store/postgres/media_outbox.go:211-213, 261-300`; `media-service/cmd/worker/main.go:477-501, 582-597, 638-649, 712-752` (HEAD); `media-service/internal/service/media.go:281-283, 305-308, 349-357`; `media-service/internal/store/postgres/media.go:77-81`; `resumable.go:18, 57, 70`; `orphan_gc.go:26, 43`; `transcode_inbox.go:112-119`; `post-service/internal/store/postgres/scheduled.go:72-80, 104-111`; `post-service/database/setup.sql:22-27` | `media_generation`, `ready_generation`, `upload_confirmed_at`, `original_etag` and fencing (section 6.1) | Phase 1 |
| **P-15** | **The media outbox cannot carry pair events.** `UNIQUE (media_asset_id, event_type)` is required by three `ON CONFLICT` writers and assumed by four readers and two purge paths. The relay is a single loop of 100 rows ordered by `created_at` that **stops on the first failure**, on one topic, keyed by `event_id`. | `media-service/database/migrations/013_media_event_outbox_and_quarantine.sql:6-20`; writers `media_outbox.go:51-56`, `transcode_inbox.go:157-162`, `subtitle_events.go:137-150`; readers `transcode_lease.go:175-187, 348-365, 384-389`, `media_outbox.go:266-293`; `asset_purge.go:167`, `purge.go:62`; relay `media-service/internal/service/media_outbox.go:16, 33, 46-55`; `media-service/internal/events/producer.go:50-64` | Leave it untouched. Add a separate pair outbox, relay and topic (section 6.1). | Phase 1 |
| **P-16** | **No readiness observability.** `OldestPendingTranscodeAge` is never called. Only job wall time is measured (no confirm→ready or queue-wait metric). The worker downloads the whole original into memory and runs one job at a time. The worker chart has no readiness probe. | `transcode_inbox.go:168-175`; `main.go:41-45, 195-219, 283-286, 456` (HEAD); `media-service/internal/store/blob/store.go:243`; `charts/atpost-service/templates/worker-deployment.yaml:121` | Metrics, a 14-day baseline and a readiness gate (section 13) | Phase 1 |
| **P-17** | **CI never builds or publishes the worker image.** `build-push.yml` builds only `<service>/Dockerfile` (`build-push-acr.yml` does the same). No ECR repository `media-worker` was found. The tag bump `sed` rewrites **every** `tag:` line, including `worker.image.tag`, to a SHA never pushed for the worker. (The Dockerfile's package build is already correct at HEAD.) | `.github/workflows/build-push.yml:148-163, 198`; `.github/workflows/build-push-acr.yml:170`; `deploy/services/media-service/values-prod.yaml:96`, `values-staging.yaml:97`; `media-service/Dockerfile.worker:21-23` | Build and push `atpost/media-worker:<sha>` for `linux/arm64`; provision ECR; key-specific tag bump; image gate (T14-1..4) | Phase 1 |
| **P-18** | **The rendition contract.** `MinVideoResolution` is declared and never referenced. MP4 renditions are skipped for short sources, and a failed MP4 rendition is silently absent while its `transcoding_jobs` row stays `processing`. A 426×240 source produces no MP4 at all. HLS playlists use relative paths, so a presigned playlist URL does not sign its segments. Legacy `ready` videos may have no HLS; the count is unverified. **In-progress change (uncommitted, not reviewed here):** HLS rungs will be built by stream-copying the already-encoded MP4 renditions, with a per-rung re-encode fallback from the original, and the dev worker will be raised to 4 CPU / 2G. | `video.go:21-25, 40-54, 79-80, 95-99, 114-115, 301-302, 340-371, 396-449` (HEAD); `main.go:519-532, 543, 613-627, 683-684, 706-711` (HEAD); `media-service/internal/service/media.go:452-459, 617-637`; `transcode_inbox.go:143` | The input contract prefers the lowest HLS rung (section 12.1). **Re-verify the "ready implies a lowest rung" guarantee after the HLS change lands.** | Phase 1 |
| **P-19** | `fpcalc` processes only 120 s of audio by default (`-length` defaults to 120; `0` means unlimited). | chromaprint 1.5.1 `src/cmd/fpcalc.cpp` (https://raw.githubusercontent.com/acoustid/chromaprint/v1.5.1/src/cmd/fpcalc.cpp) | Pass `-length 0` or `-chunk`; gate G5 (section 12.6) | Deferred (audio is phase 2) |
| **P-20** | **Grievance timer.** trust-safety hardcodes a 15-day resolution deadline. It tracks no 24-hour acknowledgement and has no 3-hour court/government-order intake. The consolidated IT Rules text updated 10.02.2026 **appears** to require acknowledgement in 24 h and resolution in 7 days. **Counsel to confirm. This is recorded as a separate prerequisite, not a fix.** | `trust-safety-service/internal/service/grievances.go:15, 48`; `trust-safety-service/database/migrations/007_grievances.sql`; comments in `admin_stats.go` | Counsel confirms the timelines and commencement dates; then change the timer and add acknowledgement and order intake | Before any IN-facing launch (L-3) |

**Observation, not verified in depth (O-obs-1).** The stall sweeper reads the current request row assuming one row per asset. A `ready` asset whose reprocess dies may never be swept. Check this while doing P-14.

---

## 5. End-to-end flow

1. **Upload and transcode (unchanged path).** Confirm sets `upload_confirmed_at` once (P-14). Transcode completion for the current generation sets `ready_generation` and, in the same transaction, enqueues `media_fingerprint_jobs(asset, generation, algo_version)`.
2. **Fingerprint (media worker, admission-controlled).** The worker streams the lowest HLS rung through internal blob reads. It writes the frame sequence, then the anchor postings, then commits. It then queries the index, votes, verifies and classifies. Pairs and one pair-outbox row per pair revision are written in one transaction, fenced by generation and claim token.
3. **Pair relay.** Pairs go to topic `media.copyright.pairs`, keyed by `pair_id`, from a separate relay that does not stop on failure.
4. **post-service consumer.** It keeps an inbox by `event_id` and a `last_revision` per `pair_id`. It maps media pairs to post pairs (`copyright_match_evidence`) and to `copyright_matches` keyed by `(reference_owner_id, copy_post_id)`. Direction comes from `upload_confirmed_at`, as a fact only.
5. **Matches (viewer).** Every request re-evaluates entitlement (trust-safety), the discovery predicate and the canonical read gate. There are no URLs in list JSON. Thumbnails come from a separate 302 route.
6. **Removal request.** post-service re-checks access, then calls trust-safety with a service token. trust-safety calls back to verify, freezes an evidence snapshot and opens a case under atomic quotas. The public form enters at the same point without a match.
7. **Review.** A reviewer works the checklist (section 8.4). A senior reviewer or counsel role assigns the legal regime. Approval, the strike, audit and outbox commands are one transaction.
8. **Enforcement.** The dispatcher sends `place_hold` to post-service. The ack moves the case to `removed`. Legal notices are delivered with durable records.
9. **Counter-notice, filed-action evidence, restoration.** These follow the regime-specific state machine (section 7). A release affects only this case's restriction row.
10. **Reconciliation** jobs compare case states, strikes, outbox acks and post-service restriction rows (section 9.5).

---

## 6. Proposed schemas, constraints, APIs and event envelopes

All DDL is **proposed**. None has been written or applied. Migration numbers are assigned at implementation.

### 6.1 media-service

```sql
-- Generation and upload-time identity (expand-only; defaults keep existing writers valid).
ALTER TABLE media_assets
  ADD COLUMN IF NOT EXISTS media_generation    BIGINT NOT NULL DEFAULT 1,
  ADD COLUMN IF NOT EXISTS ready_generation    BIGINT,       -- generation that last completed 'ready'
  ADD COLUMN IF NOT EXISTS upload_confirmed_at TIMESTAMPTZ,  -- set ONCE when bytes are verified
  ADD COLUMN IF NOT EXISTS upload_time_source  TEXT CHECK (upload_time_source IN ('confirmed','legacy_created_at')),
  ADD COLUMN IF NOT EXISTS original_etag       TEXT;         -- pinned at confirm, verified by the worker (P-14)
-- Backfill: upload_confirmed_at = created_at, upload_time_source = 'legacy_created_at';
--           ready_generation = 1 where processing_status = 'ready'.

CREATE TABLE media_fingerprint_jobs (
  media_asset_id UUID NOT NULL, media_generation BIGINT NOT NULL, algo_version SMALLINT NOT NULL,
  priority SMALLINT NOT NULL,                 -- 0 new upload, 10 backfill
  status TEXT NOT NULL CHECK (status IN ('queued','claimed','done','superseded','failed','skipped')),
  skip_reason TEXT,                           -- required when status='skipped'
  attempts INT NOT NULL DEFAULT 0, claim_token UUID, heartbeat_at TIMESTAMPTZ,
  not_before TIMESTAMPTZ NOT NULL DEFAULT now(), progress_ms INT NOT NULL DEFAULT 0,
  truncation JSONB, last_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (media_asset_id, media_generation, algo_version),
  CHECK (status <> 'skipped' OR skip_reason IS NOT NULL)
);
CREATE INDEX media_fingerprint_jobs_ready ON media_fingerprint_jobs (priority, not_before) WHERE status = 'queued';

CREATE TABLE copyright_fingerprints (
  media_asset_id UUID NOT NULL, media_generation BIGINT NOT NULL, algo_version SMALLINT NOT NULL,
  input_kind TEXT NOT NULL CHECK (input_kind IN ('hls_rung','mp4_variant','original')),
  input_ref  TEXT NOT NULL,                   -- rung or variant name; never a URL
  input_etags JSONB,                          -- object key -> ETag read (audit only)
  duration_ms INT NOT NULL, informative_ms INT NOT NULL,
  frames BYTEA NOT NULL,                      -- 2 fps: [t_ms int32 | hash int64 | flags uint8]*
  anchor_spacing_s SMALLINT NOT NULL, dur_bucket SMALLINT NOT NULL,
  superseded_at TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (media_asset_id, media_generation, algo_version)
);

CREATE TABLE copyright_anchor_postings (
  algo_version SMALLINT NOT NULL, dur_bucket SMALLINT NOT NULL,
  band_no SMALLINT NOT NULL CHECK (band_no BETWEEN 0 AND 3),
  band_value INT NOT NULL CHECK (band_value BETWEEN 0 AND 65535),
  media_asset_id UUID NOT NULL, media_generation BIGINT NOT NULL, anchor_ms INT NOT NULL,
  hash BIGINT NOT NULL,
  PRIMARY KEY (algo_version, dur_bucket, band_no, band_value, media_asset_id, media_generation, anchor_ms)
);  -- the PK is the covering lookup index; placement (schema vs separate DB) is decision O-3

CREATE TABLE copyright_band_hot (
  algo_version SMALLINT, dur_bucket SMALLINT, band_no SMALLINT, band_value INT, postings BIGINT,
  refreshed_at TIMESTAMPTZ NOT NULL, PRIMARY KEY (algo_version, dur_bucket, band_no, band_value)
);

CREATE TABLE copyright_pairs (
  pair_id UUID PRIMARY KEY,
  media_lo UUID NOT NULL, gen_lo BIGINT NOT NULL,     -- media_lo < media_hi (byte order)
  media_hi UUID NOT NULL, gen_hi BIGINT NOT NULL,
  algo_version SMALLINT NOT NULL,
  pair_revision BIGINT NOT NULL DEFAULT 1,            -- bumps on any class/score/status/direction change
  status TEXT NOT NULL CHECK (status IN ('active','invalidated')),
  invalidated_reason TEXT CHECK (invalidated_reason IN ('reprocessed','deleted','algo_retired')),
  direction TEXT NOT NULL CHECK (direction IN ('lo_earlier','hi_earlier','contemporaneous','ambiguous_legacy')),
  class TEXT NOT NULL CHECK (class IN ('full_or_near_full','contains','partial','short_clip_candidate','insufficient_evidence')),
  ref_coverage REAL, copy_coverage REAL, matched_s REAL, matched_informative_s REAL,
  median_hamming REAL, p90_hamming REAL, diversity SMALLINT,
  computed_at TIMESTAMPTZ NOT NULL,
  UNIQUE (media_lo, gen_lo, media_hi, gen_hi, algo_version)          -- pair_key
);

CREATE TABLE copyright_pair_outbox (       -- separate from media_event_outbox, which stays untouched
  event_id UUID PRIMARY KEY, pair_id UUID NOT NULL, pair_revision BIGINT NOT NULL,
  event_type TEXT NOT NULL CHECK (event_type IN ('media.copyright_pair.upserted','media.copyright_pair.invalidated')),
  payload JSONB NOT NULL,                   -- ids, generations, revision, class, scores, direction, status; no URLs, no PII
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  published_at TIMESTAMPTZ, attempts INT NOT NULL DEFAULT 0, last_error TEXT,
  UNIQUE (pair_id, pair_revision)
);
CREATE INDEX copyright_pair_outbox_due ON copyright_pair_outbox (next_attempt_at) WHERE published_at IS NULL;
-- No FK to pairs or media: deletion must not erase an unpublished invalidation (same reasoning as migration 013).
```

**Generation rules:**

1. **Confirm.** Every path that moves an asset to `uploaded` sets `upload_confirmed_at = COALESCE(upload_confirmed_at, now())` and pins `original_etag`. This covers the single PUT (`media.go:390`) and resumable completion, whose call path must be traced. The value is never overwritten.
2. **Requeue.** Both operator reprocess and stall retry go through `requeueTranscodeTx` (under the existing `FOR UPDATE`, `media_outbox.go:261-264`). In that transaction:
   - bump `media_generation` and put it in `TranscodeRequestPayload`;
   - mark older fingerprints `superseded_at = now()`;
   - mark affected pairs `invalidated` and write one pair-outbox row per pair.

   Stale postings are deleted later by a sweeper.
3. **Completion.** `completeTranscodeTx` sets `ready_generation` only when `media_generation` equals the payload generation. A completion for an older generation sets nothing and enqueues nothing.
4. **Fence on write.** The fingerprint job's final transaction runs `SELECT media_generation, ready_generation, processing_status … FOR SHARE`. It proceeds only if `media_generation = ready_generation = job_generation` and `status = 'ready'`, and it updates the job row by `claim_token`. Otherwise it marks the job `superseded` and writes nothing. The generation bump commits before the relay publishes the new request, so before any object is overwritten. A job that read objects mid-overwrite therefore always fails the fence.
5. **Algorithm version.** It is part of every key. A new version is dual-written by backfill, reads switch in one configuration flip, and the old version is then deleted.

**Upload precedence (a fact, not ownership):**
- The earlier upload is the one with the smaller `upload_confirmed_at`.
- If `|Δ| ≤ 60 s`, the direction is `contemporaneous`.
- If either side is `legacy_created_at` and `|Δ| < 24 h`, the direction is `ambiguous_legacy`.
- `posts.created_at` and `media_assets.created_at` are never used.

**Pair relay `StartCopyrightPairRelay`.** It has its own goroutine and ticker.
- **Claim:** `… ORDER BY next_attempt_at LIMIT 50 FOR UPDATE SKIP LOCKED`, pushing `next_attempt_at` forward by a 60 s lease.
- **Publish** to `media.copyright.pairs`, keyed by `pair_id`.
- **Failure:** increment `attempts`, back off up to 15 min, and **continue with the next row**. After 20 attempts, alert.
- **Budget:** ≤ 200 events/s per replica. Kill switch `COPYRIGHT_PAIR_RELAY=off`.
- **Purge:** asset-purge paths write `invalidated` rows before deleting fingerprints, postings and pairs. The pair outbox is not purged, because it carries no PII.

### 6.2 post-service

```sql
-- Case-specific restrictions. Never written into posts.review_status.
CREATE TABLE post_restrictions (
  restriction_id UUID PRIMARY KEY,
  post_id UUID NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,
  source TEXT NOT NULL CHECK (source IN ('copyright','safety')),   -- 'safety' reserved; refused in v1
  case_id UUID NOT NULL,
  issuer TEXT NOT NULL CHECK (issuer = 'trust-safety-service'),
  scope TEXT NOT NULL DEFAULT 'global' CHECK (scope IN ('global')),  -- country scope is decision L-13
  state TEXT NOT NULL CHECK (state IN ('active','released')),
  case_revision BIGINT NOT NULL CHECK (case_revision > 0),
  last_decision_id UUID NOT NULL, policy_version TEXT NOT NULL, reason_code TEXT NOT NULL,
  first_placed_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (post_id, source, case_id)
);
CREATE INDEX idx_post_restrictions_active ON post_restrictions (post_id) WHERE state = 'active';
CREATE INDEX idx_post_restrictions_case   ON post_restrictions (source, case_id);
CREATE INDEX idx_post_restrictions_sync   ON post_restrictions (source, updated_at, restriction_id);

CREATE TABLE post_restriction_events (      -- append-only (trigger refuses UPDATE/DELETE/TRUNCATE)
  decision_id UUID PRIMARY KEY,             -- idempotency key minted by trust-safety
  restriction_id UUID NOT NULL REFERENCES post_restrictions(restriction_id),
  action TEXT NOT NULL CHECK (action IN ('place_hold','release_hold')),
  case_revision BIGINT NOT NULL,
  prev_state TEXT CHECK (prev_state IN ('active','released')), new_state TEXT NOT NULL CHECK (new_state IN ('active','released')),
  actor_id UUID NOT NULL, policy_version TEXT NOT NULL, reason_code TEXT NOT NULL,
  claims_digest BYTEA NOT NULL,             -- sha256 of canonical claims excluding issued/expires times
  changed BOOLEAN NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

ALTER TABLE posts
  ADD COLUMN active_restriction_count INT NOT NULL DEFAULT 0 CHECK (active_restriction_count >= 0),
  ADD COLUMN effective_review_status TEXT GENERATED ALWAYS AS
      (CASE WHEN active_restriction_count > 0 THEN 'restricted' ELSE review_status END) STORED;
-- A deferred constraint trigger asserts count = active rows at commit; a nightly self-check repairs drift.

-- Matches: post-level identity, separate from media pairs.
CREATE TABLE copyright_matches (
  match_id UUID PRIMARY KEY,
  reference_owner_id UUID NOT NULL, reference_post_id UUID NOT NULL, copy_post_id UUID NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('active','pending_recompute')),
  class TEXT NOT NULL, similarity_bucket TEXT, coverage_ref REAL, coverage_copy REAL,
  algorithm_version SMALLINT NOT NULL, direction TEXT NOT NULL,
  first_surfaced_at TIMESTAMPTZ, notified_at TIMESTAMPTZ, last_discoverable_at TIMESTAMPTZ,
  archived_at TIMESTAMPTZ, archived_by UUID, archive_reason TEXT
      CHECK (archive_reason IN ('licensed','mine_elsewhere','not_a_copy','other')),
  UNIQUE (reference_owner_id, copy_post_id)                 -- "each copy surfaces once per claimant"
);
CREATE TABLE copyright_match_evidence (                     -- media pairs -> post pairs (many-to-many)
  match_id UUID NOT NULL REFERENCES copyright_matches(match_id),
  pair_id UUID NOT NULL, pair_revision BIGINT NOT NULL, reference_post_id UUID NOT NULL,
  PRIMARY KEY (match_id, pair_id)
);
CREATE TABLE copyright_pair_inbox (event_id UUID PRIMARY KEY, received_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE copyright_pair_revisions (pair_id UUID PRIMARY KEY, last_revision BIGINT NOT NULL);
CREATE TABLE copyright_claimant_known_licensees (          -- claimant-controlled, reversible; not a platform allowlist
  claimant_id UUID NOT NULL, copy_channel_id UUID NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (claimant_id, copy_channel_id)
);
```

- `first_surfaced_at`, `notified_at` and `archived_at` are **never reset**. That holds across a new generation, a new `pair_revision`, and invalidation followed by reactivation. While every evidence pair is invalidated, the match is `pending_recompute` and hidden. When a pair reactivates, the match is shown again without a new notification.
- A pair of class `full_or_near_full`, with `direction` not `contemporaneous` and not `ambiguous_legacy`, creates a match only on the side whose upload is earlier. Otherwise it creates matches on **both** sides, marked "no precedence", and the reviewer sees both uploads. Direction is displayed as "uploaded earlier / later" only.

**Restriction rules (from finding 1):**

- **Effective eligibility.** A post is viewer-eligible only if all of these hold:
  - `deleted_at IS NULL`
  - `publish_at IS NULL`
  - `effective_review_status = 'approved'`
  - audience, age and privacy rules pass.
- **What a place or release may write:** only the restriction row, `post_restriction_events`, `active_restriction_count` and the eligibility projection (`BumpSearchRevAndEmitTx` and cache invalidation).
- **What a place or release never writes:** `review_status`, `visibility`, distribution, `age_restricted`, `deleted_at`, `publish_at`, `published_at` or `created_at`.
- **Isolation.** `ModeratePost`, appeals, reviewer auto-resolve, resubmit, author delete/restore and scheduling never read or write restrictions.
- **Read path (P-4).** Every viewer predicate goes through one helper (e.g. `postgres.ViewerEligibleSQL(alias)`) on `effective_review_status`. This covers feeds, reels, channels, end screens, live VOD, threads, search candidates, embeds, the media-access answer (`internal/http/media_access_handler.go:101`), ws room admission (`handler.go:186`) and `GetPostAccessState`. A source-scan test fails on any `review_status = 'approved'` literal outside an allowlist of writers and admin views.
- **Search eligibility.** `PostSearchEligibilityChanged` keeps `review_status` but fills it with the **effective** value, and adds `base_review_status` and `restricted`. Because `SearchEligible` is an allowlist, a consumer that has not been updated treats `restricted` as ineligible. Every consumer must be audited for allowlist semantics before rollout.
- **Owner view.** The author still sees their post, with a banner ("Removed: copyright case", a link) and the counter-notice entry point. Admin views show the base status and every restriction.

**Restriction command** `POST /v1/posts/internal/restrictions`. It sits behind the internal key and requires a valid `post_restriction` capability.

Claims (all signed):
```json
{ "version": 1, "issuer": "trust-safety-service", "purpose": "post_restriction", "audience": "post-service",
  "action": "place_hold | release_hold", "source": "copyright", "case_id": "uuid", "case_revision": 7,
  "subject_id": "post uuid", "subject_author_id": "uuid", "expected_state": "absent | active | released",
  "decision_id": "uuid", "policy_version": "copyright-v1",
  "reason_code": "removal_upheld | reinstated_on_review | counter_notice_restore | rule75_restore | claim_withdrawn | reversed_on_review",
  "actor_id": "uuid", "issued_at_unix": 0, "expires_at_unix": 0 }
```

Allowed combinations; any other combination is refused:

| action | reason_code | case transition |
|---|---|---|
| `place_hold` | `removal_upheld` | → `approved_enforcing` |
| `place_hold` | `reinstated_on_review` | `restored` → `approved_enforcing` (senior) |
| `release_hold` | `counter_notice_restore` | → `restoring` (US) |
| `release_hold` | `rule75_restore` | → `restoring` (India, discretionary) |
| `release_hold` | `claim_withdrawn` / `reversed_on_review` | withdrawal / reversal |

`source = safety` returns `403 SOURCE_NOT_ENABLED`. No other caller or service has any access.

Processing happens in one transaction:
1. Verify the capability. A failure is `403 INVALID_CAPABILITY`.
2. Take an advisory lock on `decision_id`. An existing event with the same digest returns the stored outcome with `replayed=true`. An existing event with a different digest is `409 DECISION_CONFLICT`.
3. Lock the post with `FOR UPDATE`. A missing post is `404 SUBJECT_NOT_FOUND`. A different author is `409 SUBJECT_MISMATCH`. Deleted and scheduled posts are **accepted**.
4. Lock the restriction row. `case_revision ≤ row.case_revision` is `409 STALE_CASE_REVISION`. An `expected_state` that differs from the current state is `409 STATE_MISMATCH`.
5. Apply the change to **this row only**, and adjust the count.
6. Insert the event.
7. Emit the eligibility change and invalidate the cache after commit.
8. Respond with `{restriction_id, post_id, case_id, state, case_revision, active_restriction_count, effective_review_status, replayed}`.

**Other post-service routes:**

| Route | Auth | Purpose |
|---|---|---|
| `GET /v1/posts/internal/restrictions?source=copyright&updated_after=&cursor=` | internal key + service token `post:restrictions.read` (trust-safety) | Reconciliation |
| `GET /v1/posts/internal/moderation-subject/:postId` (extended) | existing | Adds `base_review_status`, `effective_review_status`, `latest_base_decision_id`, `active_restrictions[]` |
| `POST /v1/posts/internal/copyright/matches/:id/verify` | service token `post:copyright.verify` (trust-safety) | Returns `{discoverable, readable_by_claimant, copy_post_rev, evidence_snapshot}` |
| `GET /v1/copyright/matches?cursor=` | user | List (section 8.1) |
| `GET /v1/copyright/matches/summary` | user | Counts computed at request time |
| `GET /v1/copyright/matches/:id` | user | Detail |
| `GET /v1/copyright/matches/:id/thumbnail/{reference\|copy}` | user | 302 to one `thumb_300` still, signed ≤ 5 min |
| `POST` / `DELETE /v1/copyright/matches/:id/archive` | user | Archive with a reason, or unarchive |
| `POST /v1/copyright/matches/:id/removal-requests` | user | Opens a trust-safety case (section 8.1) |

All user routes send `Cache-Control: private, no-store` and `Vary: Authorization`. An unreadable match returns `404 MATCH_NOT_FOUND`, never 403.

### 6.3 Ordinary appeals (post-service and trust-safety)

- **New columns and statuses.**
  - `trust.content_appeals` gains `appealed_decision_id UUID NULL`.
  - Its status CHECK gains `overturning` and `superseded`.
  - The post-moderation capability gains a **new version** carrying `expected_base_decision_id`. The existing `Claims` struct is not edited in place.
- **At submission:**
  - The appeal is eligible only if `base_review_status ∈ {rejected, needs_changes}`.
  - Record `appealed_decision_id`.
  - If the base status is `approved` and a copyright restriction is active, refuse with `COPYRIGHT_CASE_USE_COUNTER_NOTICE`.
  - If there is both a base rejection and a hold, accept the appeal and tell the author that it cannot make the video available while the case holds it.
- **At adjudication:**
  - Move the appeal to `overturning` under a row lock before sending the command. An appeal in `overturning` cannot be upheld.
  - On success, move to `overturned`. On `409 SUPERSEDED`, move to `superseded`.
  - On a transient failure, stay in `overturning`. A sweeper replays the same `decision_id` (the appeal id).
- **At the final mutation.** post-service refuses with `409 SUPERSEDED` if the post's latest base decision differs from `expected_base_decision_id`. It refuses `approve` unless the base status is still the one that was appealed. `ModeratePost` never touches restrictions, so an overturn under a hold leaves `effective_review_status = 'restricted'`.
- **Appeals that pre-date the change.**
  - Backfill `appealed_decision_id` from the latest `post_moderation_decisions` row created at or before `submitted_at`.
  - Where no such row exists (pending-gate or ML rejections write `post_review_audit`), adjudication requires `base_review_status = action_taken` and no newer decision. Otherwise the appeal is `superseded`.
- **Concurrent admin actions.** Any admin decision creates a new base decision, so an in-flight overturn fails with `409 SUPERSEDED`. No admin route can release a copyright hold.

### 6.4 trust-safety-service

```sql
CREATE TABLE trust.copyright_cases (
  id UUID PRIMARY KEY,
  state TEXT NOT NULL,                        -- section 7
  revision BIGINT NOT NULL DEFAULT 1,
  outcome_code TEXT CHECK (outcome_code IN ('upheld','rejected_licensed','rejected_insufficient',
                   'rejected_exception_escalated','rejected_bad_faith','incomplete','withdrawn')),
  review_tier TEXT NOT NULL DEFAULT 'standard' CHECK (review_tier IN ('standard','senior')),
  entry TEXT NOT NULL CHECK (entry IN ('matches_tool','public_form')),
  claimant_user_id UUID, claimant_key BYTEA NOT NULL,   -- LookupHasher(normalised legal name + verified email)
  respondent_user_id UUID, target_post_id UUID NOT NULL, target_group_id UUID,
  match_id UUID, evidence_snapshot JSONB NOT NULL,      -- frozen at submission (section 8.1)
  claimant_role TEXT CHECK (claimant_role IN ('owner','exclusive_licensee','authorised_agent')),
  work_description TEXT, rights_territories TEXT[],
  reference_is_matched_later BOOLEAN, claimant_upload_after_respondent BOOLEAN,
  respondent_declared_remix BOOLEAN, remix_verified BOOLEAN NOT NULL DEFAULT false,
  legal_regime TEXT NOT NULL DEFAULT 'unassigned' CHECK (legal_regime IN
      ('us_512','in_rule75','in_court_order','platform_policy_only','unassigned')),
  regime_basis TEXT, regime_set_by UUID, regime_set_at TIMESTAMPTZ, regime_due_at TIMESTAMPTZ,
  policy_version TEXT NOT NULL, calendar_version TEXT,
  submitter_access_revoked BOOLEAN NOT NULL DEFAULT false,
  retain_until TIMESTAMPTZ, subject_tokens JSONB, closed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- One open case per claimant and target (states from section 7).
CREATE UNIQUE INDEX uq_cr_case_open_claimant_target ON trust.copyright_cases (claimant_key, target_post_id)
  WHERE state IN ('submitted','incomplete','under_review','approved_enforcing','removed','enforcement_failed',
                  'counter_notice_open','awaiting_restoration','legal_action_hold','restore_decision_required','removed_final');

CREATE TABLE trust.copyright_case_decisions (      -- one row per human or system transition; idempotency
  decision_id UUID PRIMARY KEY, case_id UUID NOT NULL REFERENCES trust.copyright_cases(id),
  from_state TEXT NOT NULL, to_state TEXT NOT NULL, case_revision BIGINT NOT NULL,
  actor_id UUID NOT NULL, reason_code TEXT NOT NULL, rationale_sealed BYTEA,
  request_digest BYTEA NOT NULL, step_up_ref TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (case_id, case_revision)
);

-- Legal notices and their timestamps (write-once; corrections are new audit rows).
CREATE TABLE trust.copyright_legal_notices (
  id UUID PRIMARY KEY, case_id UUID NOT NULL REFERENCES trust.copyright_cases(id),
  kind TEXT NOT NULL CHECK (kind IN ('takedown_notice','counter_notice','forward_takedown_to_respondent',
        'forward_counter_to_claimant','decision_to_claimant','decision_to_respondent','restoration_notice','withdrawal')),
  submitted_at TIMESTAMPTZ, received_at TIMESTAMPTZ,      -- received_at = legally effective receipt (rule: counsel)
  validated_at TIMESTAMPTZ, validation_due_at TIMESTAMPTZ,
  validation_outcome TEXT CHECK (validation_outcome IN ('pending','effective','deficient_curable','invalid','withdrawn')),
  element_checklist JSONB,                                -- per-element result for the regime's fields
  content_sealed BYTEA NOT NULL, content_key_ver INT NOT NULL, content_sha256 BYTEA NOT NULL,
  policy_version TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE trust.copyright_notice_deliveries (
  id UUID PRIMARY KEY, notice_id UUID NOT NULL REFERENCES trust.copyright_legal_notices(id),
  recipient_party TEXT NOT NULL CHECK (recipient_party IN ('claimant','respondent','agent')),
  channel TEXT NOT NULL CHECK (channel IN ('email','inbox','push','postal_manual')),
  idempotency_key TEXT NOT NULL UNIQUE,                   -- notice_id:party:channel
  status TEXT NOT NULL CHECK (status IN ('queued','sending','sent','delivered','bounced','failed','escalated','superseded')),
  attempts INT NOT NULL DEFAULT 0, next_attempt_at TIMESTAMPTZ, first_attempt_at TIMESTAMPTZ,
  sent_at TIMESTAMPTZ, delivered_at TIMESTAMPTZ, provider_message_id TEXT, last_error_code TEXT,
  escalated_at TIMESTAMPTZ, manual_reference TEXT, claim_token UUID, claimed_at TIMESTAMPTZ
);
CREATE TABLE trust.copyright_party_contacts (           -- restricted; pii scope 'copyright'
  case_id UUID, party TEXT, name_sealed BYTEA, email_sealed BYTEA, address_sealed BYTEA, phone_sealed BYTEA,
  key_ver INT, email_lookup_hash BYTEA, PRIMARY KEY (case_id, party)
);

CREATE TABLE trust.counter_notices (
  id UUID PRIMARY KEY, case_id UUID NOT NULL REFERENCES trust.copyright_cases(id),
  state TEXT NOT NULL CHECK (state IN ('submitted','incomplete','rejected','accepted','forwarded','closed','withdrawn')),
  notice_id UUID NOT NULL REFERENCES trust.copyright_legal_notices(id),
  counter_received_at TIMESTAMPTZ, counter_effective_at TIMESTAMPTZ, counter_forwarded_at TIMESTAMPTZ,
  restore_not_before TIMESTAMPTZ, restore_not_after TIMESTAMPTZ, restore_scheduled_at TIMESTAMPTZ,
  calendar_version TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_counter_open_per_case ON trust.counter_notices (case_id)
  WHERE state IN ('submitted','incomplete','accepted','forwarded');

CREATE TABLE trust.copyright_legal_action_evidence (   -- filed action (US) or court order (IN); never a boolean
  id UUID PRIMARY KEY, case_id UUID NOT NULL REFERENCES trust.copyright_cases(id),
  kind TEXT NOT NULL CHECK (kind IN ('us_filed_action','in_court_order','court_order_intake')),
  court TEXT NOT NULL, case_number TEXT NOT NULL, filed_or_order_date DATE NOT NULL,
  parties_sealed BYTEA NOT NULL, document_object_ids UUID[] NOT NULL CHECK (cardinality(document_object_ids) > 0),
  operative_scope JSONB, action_notice_received_at TIMESTAMPTZ NOT NULL,
  action_review_due_at TIMESTAMPTZ NOT NULL,
  review_outcome TEXT CHECK (review_outcome IN ('accepted','rejected','timed_out')),
  reviewed_by UUID, reviewed_at TIMESTAMPTZ, reverify_due_at TIMESTAMPTZ
);
CREATE TABLE trust.copyright_rule75_periods (           -- India only
  case_id UUID PRIMARY KEY REFERENCES trust.copyright_cases(id),
  disable_due_at TIMESTAMPTZ NOT NULL, rule75_period_ends_at TIMESTAMPTZ NOT NULL, reasons_notice_text_id UUID
);

-- Tool access.
CREATE TABLE trust.copyright_access_grants (
  id UUID PRIMARY KEY, user_id UUID NOT NULL, kind TEXT NOT NULL CHECK (kind IN ('admin','upheld_request')),
  source_case_id UUID, granted_by UUID, granted_at TIMESTAMPTZ NOT NULL DEFAULT now(), expires_at TIMESTAMPTZ,
  suppressed_by_revocation_id UUID
);
CREATE TABLE trust.copyright_access_revocations (
  id UUID PRIMARY KEY, user_id UUID, claimant_key BYTEA, reason_code TEXT NOT NULL, policy_version TEXT NOT NULL,
  revoked_by UUID, revoked_at TIMESTAMPTZ NOT NULL DEFAULT now(), reinstated_by UUID, reinstated_at TIMESTAMPTZ
);
CREATE TABLE trust.copyright_submission_counters (
  key_type TEXT NOT NULL CHECK (key_type IN ('account','claimant','claimant_target_channel','ip')),
  key TEXT NOT NULL, window_start TIMESTAMPTZ NOT NULL, count INT NOT NULL,
  PRIMARY KEY (key_type, key, window_start)
);

-- Strikes (finding 5).
CREATE TABLE trust.strike_policies (        -- append-only
  policy_version TEXT PRIMARY KEY,
  category TEXT NOT NULL CHECK (category IN ('copyright','community_guidelines')),
  severity TEXT NOT NULL CHECK (severity IN ('warning','strike','severe_strike')),
  duration INTERVAL NOT NULL CHECK (duration > interval '0'),     -- 90 days proposed; decision L-8
  approved_by TEXT NOT NULL, approved_at TIMESTAMPTZ NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
ALTER TABLE trust.user_strikes
  ADD COLUMN category TEXT CHECK (category IN ('copyright','community_guidelines')),
  ADD COLUMN case_type TEXT CHECK (case_type IN ('copyright_case','moderation_case')),
  ADD COLUMN case_id UUID, ADD COLUMN policy_version TEXT REFERENCES trust.strike_policies(policy_version),
  ADD COLUMN reason_code TEXT, ADD COLUMN idempotency_key TEXT, ADD COLUMN issue_decision_id UUID,
  ADD COLUMN issued_at TIMESTAMPTZ, ADD COLUMN voided_at TIMESTAMPTZ,
  ADD COLUMN void_reason TEXT CHECK (void_reason IN ('claim_withdrawn','counter_notice_restored','reversed_on_review',
                                                     'claimant_abuse','duplicate','legacy_cleanup')),
  ADD COLUMN voided_by UUID, ADD COLUMN void_decision_id UUID;
ALTER TABLE trust.user_strikes ADD CONSTRAINT user_strikes_complete CHECK (
  case_id IS NOT NULL AND category IS NOT NULL AND policy_version IS NOT NULL AND idempotency_key IS NOT NULL
  AND issue_decision_id IS NOT NULL AND issued_at IS NOT NULL AND expires_at IS NOT NULL AND expires_at > issued_at) NOT VALID;
ALTER TABLE trust.user_strikes ADD CONSTRAINT user_strikes_void_shape CHECK (
  (voided_at IS NULL AND void_reason IS NULL AND voided_by IS NULL AND void_decision_id IS NULL) OR
  (voided_at IS NOT NULL AND void_reason IS NOT NULL AND voided_by IS NOT NULL AND void_decision_id IS NOT NULL));
CREATE UNIQUE INDEX uq_user_strikes_case      ON trust.user_strikes (case_type, case_id)   WHERE case_id IS NOT NULL;
CREATE UNIQUE INDEX uq_user_strikes_idem      ON trust.user_strikes (idempotency_key)      WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX uq_user_strikes_issue_dec ON trust.user_strikes (issue_decision_id)    WHERE issue_decision_id IS NOT NULL;
CREATE INDEX idx_user_strikes_active ON trust.user_strikes (user_id, expires_at) WHERE voided_at IS NULL;

ALTER TABLE trust.admin_audit DROP CONSTRAINT <existing target_type check>;
ALTER TABLE trust.admin_audit ADD CONSTRAINT admin_audit_target_type_check CHECK (target_type IN
  ('report','appeal','grievance','strike','copyright_case','counter_notice','legal_hold','copyright_access','copyright_pii'));

-- Transactional outbox: HTTP commands to post-service and Kafka events.
CREATE TABLE trust.enforcement_outbox (
  decision_id UUID PRIMARY KEY,
  kind TEXT NOT NULL CHECK (kind IN ('restriction_command','event')),
  case_id UUID, case_revision BIGINT, topic TEXT, payload JSONB NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','acked','superseded','failed')),
  attempts INT NOT NULL DEFAULT 0, next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(), last_error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE trust.legal_holds (
  id UUID PRIMARY KEY, scope_type TEXT NOT NULL CHECK (scope_type IN ('case','user','post','media')),
  scope_id UUID NOT NULL, reason_code TEXT NOT NULL, reference_sealed BYTEA,
  placed_by UUID NOT NULL, placed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  review_by TIMESTAMPTZ NOT NULL, released_by UUID, released_at TIMESTAMPTZ
);
```

**Strike rules:**
- **Idempotency.** One strike per case (`uq_user_strikes_case`). The idempotency key is `'copyright_case:' || case_id || ':strike'`.
- **Expiry.** `expires_at = issued_at + policy.duration` is computed at insert and never recomputed.
- **Active** means `voided_at IS NULL AND expires_at > now()`. This replaces the reader at `trust-safety-service/internal/store/postgres/trust_extras.go:488-489`.
- **Never deleted.** Strikes are voided, not deleted. `admin_stats` counts only non-voided strikes.
- **Legacy rows.** Rows with a NULL `expires_at` are grandfathered with `NOT VALID` until decision F-15. The header-trusting `POST /v1/strikes` is retired, or replaced by a token-gated `POST /v1/internal/admin/trust/strikes` that requires `strikes.manage`, step-up, a case id and an idempotency key.

**The approval transaction** (`POST /v1/internal/admin/trust/copyright/cases/:id/decisions`) takes `{decision_id, expected_case_revision, outcome, reason_code}`, requires `copyright.decide` and step-up, and runs as one transaction:
1. Lock the case.
   - An existing `decision_id` with the same request digest returns the stored result. A different digest is `409 DECISION_CONFLICT`.
   - Require `state = 'under_review'`, a revision match and `legal_regime ≠ 'unassigned'`. For `review_tier = 'senior'`, also require `copyright.legal`.
2. Move the case to `approved_enforcing` and bump the revision.
3. Insert the strike with `ON CONFLICT (idempotency_key) DO NOTHING`. On a conflict, re-read and assert the same case, user and policy.
4. Write audit rows `copyright_case.approved` and `strike.issued`. The actor is the token's `act` claim.
5. Write outbox rows: `restriction_command` (`place_hold`), `event` `trust.copyright.case_updated` for each party, and `event` `trust.standing_changed`.
6. Commit.

**Void** is one transaction:
- update the case (revision bump);
- `UPDATE … SET voided_at … WHERE case_type='copyright_case' AND case_id=$c AND voided_at IS NULL`. Zero rows with the same `void_decision_id` is a replay. Zero rows with a different id returns the original void, because voiding is terminal;
- write audit rows;
- write outbox rows `release_hold` (this case only), `case_updated` and `standing_changed`.

**Standing** `GET /v1/internal/standing/:userId`. It is registered before `RequireInternalKey`, so the internal key is no evidence here. A `requireServiceCaller("trust_safety:standing.read", {post-service})` middleware gates it. Configuration: `SERVICE_CALLERS=admin-service,post-service`, `SERVICE_CALLER_POST_SERVICE_OPS=trust_safety:standing.read`.

```json
{ "data": { "user_id": "uuid", "policy_version": "standing-v1",
  "standing": "good | restricted | suspended", "publish_allowed": true,
  "active_strikes": [ { "strike_id": "uuid", "category": "copyright | community_guidelines",
     "severity": "warning | strike | severe_strike", "reason": "reason_code",
     "case_id": "uuid", "issued_at": "RFC3339", "expires_at": "RFC3339" } ],
  "suspended_until": "RFC3339 | null", "evaluated_at": "RFC3339", "max_age_seconds": 60 } }
```

- `active_strikes` is always an array. The severity enum lives in one shared package and equals the DB CHECK. An unknown value is treated as unknown standing, which fails closed.
- **trust-safety alone computes** `publish_allowed`. post-service enforces it and never re-derives policy from severities.
- The placeholder `standing-v1` (labelled "unapproved default") denies publication only when `suspended_until > now()` or an active `severe_strike` exists. The real thresholds are decision F-6.
- **post-service caller.**
  - It caches both allow and deny for ≤ 60 s, with an 800 ms timeout and one retry.
  - **Background paths fail closed**, with per-item exponential backoff. After 24 h of continuous unknown, the item moves to `blocked` with reason `standing_unavailable`.
  - **Interactive paths** return `503 STANDING_UNAVAILABLE`.
  - Set `TRUST_SAFETY_SERVICE_URL=http://trust-safety-service:8091` (in-cluster FQDN in helm) in compose and all four values files, and remove the `:8118` default. Outside dev, refuse to boot when the URL or the signer is missing.
- **Enforcement scope.** A single choke point, `requirePublishStanding`, is called from:
  - `CreatePost` (all types) and `CreateThread`
  - `PublishPostDraft` and `publishDraftRow`
  - reel-draft `PublishDraft` and `publishClaimedReelDraft`
  - `PublishScheduled` at flip time (worker, publish-now and `PublishVideo`)
  - `CreateRepost`, `CreateCrosspost`, `CreateStoryPending` and `CreateLiveVODPost`
  - recommended: visibility widening (decision F-8).

  A route-inventory test asserts that every publication entry point calls it.

**Other trust-safety routes:**

| Route | Auth | Purpose |
|---|---|---|
| `POST /v1/internal/copyright/cases` | service token `trust_safety:copyright.case.create` (post-service) | Case from Matches: verify callback, quotas, snapshot |
| `POST /v1/copyright/notices` | user, or anonymous with a verified email | Public legal-notice form (section 8.3) |
| `GET /v1/copyright/cases`, `GET /v1/copyright/cases/:id` | party to the case | Frozen snapshot and status only |
| `POST /v1/copyright/cases/:id/withdraw` | claimant | Withdrawal |
| `POST /v1/copyright/cases/:id/counter-notices` | respondent | Counter-notice (US regime) |
| `POST /v1/copyright/cases/:id/respondent-evidence` | respondent | Licence or collaboration evidence (protected objects) |
| `POST /v1/copyright/cases/:id/legal-action-evidence` | claimant | Filed-action or court-order documents (never a boolean) |
| `/v1/internal/admin/trust/copyright/*` | admin token + `copyright.*` permission + step-up where marked | Queue, decisions, regime, evidence review, PII read, legal holds, grants, revocations, reinstatement |
| `GET /v1/internal/standing/:userId` | service token (post-service) | Standing |

**Delivery worker (legal notices).**
- DB-polled with `SKIP LOCKED` and a claim token. Email goes through `shared/mailer`, with the provider idempotency key = `idempotency_key`.
- Backoff: 1 m, 5 m, 30 m, 2 h, 6 h, then every 12 h.
- After 4 h without `sent`, or on any bounce, the row becomes `escalated`. An ops alert is written with `dedupe_key = 'copyright-delivery:' || id` (critical if a statutory action is waiting), and an admin queue item is created for a manual channel (`postal_manual` with a reference).
- Statutory clocks anchor only to `copyright_legal_notices` receipt or validation times, **never** to delivery rows. A failed forward never resets or extends a clock. Any action that depends on the forward (for example restoration) is blocked and escalated.

### 6.5 monetization-service

`GET /v1/monetization/internal/creator-programme/:userId`. Service token, issuer `trust-safety-service`, operation `monetization:creator_programme.read`. It sits outside the beta write boundary because it moves no money.

```json
200 {"data":{"user_id":"…","member":true,"status":"eligible","evaluated_at":"RFC3339","suspended":false,"policy_version":"cf-elig-v1"}}
404 → member=false; any other status or malformed body → error (caller fails closed)
```

`member = true` only when the **stored** row has `status='eligible'`, `suspended_at IS NULL` and `last_evaluated_at ≥ now() − 48h`. The fresh `decision` is display-only.

### 6.6 Event envelopes

| Event | Topic / channel | Key | Payload (no URLs, no legal PII) |
|---|---|---|---|
| `media.copyright_pair.upserted` / `.invalidated` | `media.copyright.pairs` (new) | `pair_id` | `{event_id, occurred_at, pair_id, pair_revision, media_lo, gen_lo, media_hi, gen_hi, algo_version, status, invalidated_reason, class, direction, ref_coverage, copy_coverage, matched_informative_s, median_hamming, diversity}` |
| `MediaTranscodeRequested` / completion (changed) | `media.events` (unchanged topic) | unchanged | adds `media_generation` |
| `PostSearchEligibilityChanged` (changed) | existing | existing | `review_status` = effective value; adds `base_review_status`, `restricted` |
| `trust.copyright.case_updated` | trust-safety outbox → Kafka | `case_id` | `{event_id, occurred_at, case_id, recipient_user_id, update_kind: notice_received\|action_taken\|counter_notice\|decision\|restored, case_link:"/studio/copyright/cases/<id>"}` |
| `trust.copyright.access_changed` | trust-safety outbox → Kafka | `user_id` | `{event_id, user_id, access, reason_code}` |
| `trust.standing_changed` | trust-safety outbox → Kafka | `user_id` | `{event_id, user_id, policy_version}` (a cache hint only; correctness relies on the 60 s max age) |
| Inbox ping "matches available" | notification-service | — | `{type:"copyright_matches_available", entity_type:"copyright_digest", entity_id, deep_link:"/studio/copyright/matches", actor_user_id: 00000000-…}` |

notification-service consumes `trust.copyright.*` with the durable pattern (fetch → retry → DLQ → commit, as in `call_consumer.go:150-210`) and `CreateNotificationIdempotent` keyed by `event_id`. Category `copyright_case` is transactional (decision F-9). A failed suppression lookup **retries** for this type instead of dropping.

---

## 7. Case state machine

**Actors:**
- **C**: claimant
- **R**: respondent (the uploader)
- **S**: system (validators, timers, dispatcher acks)
- **Rv**: reviewer (`copyright.decide` plus step-up)
- **Sr**: senior reviewer or counsel-designated role (`copyright.legal` plus step-up)

Every transition bumps `revision`, writes a `copyright_case_decisions` row with a stable `decision_id` and one `admin_audit` row, and writes any outbox commands **in the same transaction**. Statutory timings are **placeholders that counsel must approve** (section 11).

### 7.1 Removal request (`trust.copyright_cases.state`)

| From | To | Actor | Condition | Hold | Strike | Command |
|---|---|---|---|---|---|---|
| — | `submitted` | C | Entitled (Matches) or public form; quotas and duplicate index pass; verify callback passes (Matches) | none | — | — |
| `submitted` | `incomplete` | S/Rv | Required fields missing under the regime checklist. A US curable deficiency creates a prompt-contact task. | none | — | notify C |
| `incomplete` | `submitted` | C | Corrected within the window (new receipt time) | none | — | — |
| `incomplete` | `closed_incomplete` | S | Window lapsed | none | — | — |
| `submitted` | `under_review` | S | Fields complete. `review_tier='senior'` if `reference_is_matched_later` or `claimant_upload_after_respondent`. | none | — | — |
| `submitted`, `under_review` | `withdrawn` | C | Before a decision | none | — | — |
| `under_review` | `rejected` | Rv (Sr for senior tier) | `outcome_code` ∈ licensed / insufficient / exception_escalated. `rejected_bad_faith` needs a second reviewer plus step-up. | none | — | abuse counters (8.3) |
| `under_review` | `approved_enforcing` | Rv (Sr for senior tier) | Checklist complete; `legal_regime ≠ unassigned` | placing | issued | `place_hold` (`removal_upheld`) |
| `approved_enforcing` | `removed` | S | `place_hold` acked | active | active | notify C and R; US: subscriber notice (durable) |
| `approved_enforcing` | `enforcement_failed` | S | Non-retryable error or ack timeout (proposed 1 h) | reconciled | active | page on-call |
| `enforcement_failed` | `approved_enforcing` | Rv | Re-drive after a fix (same `decision_id`) | placing | active | same command |
| `removed` | `counter_notice_open` | S | **US only:** a counter-notice becomes `accepted` (effective) | active | active | forward to C (durable) |
| `counter_notice_open` | `awaiting_restoration` | S | Forward recorded as `sent`; window `[restore_not_before, restore_not_after]` computed from `counter_received_at` with the approved calendar | active | active | notify C of the deadline |
| `awaiting_restoration` | `legal_action_hold` | Sr | Filed-action evidence **reviewed and accepted** by `action_review_due_at`. A checkbox alone never qualifies. | active | active | notify R |
| `awaiting_restoration` | `restoring` | S | `restore_scheduled_at` reached (never before not_before), the forward was sent, and no accepted evidence exists | releasing | void if L-8 says so (`counter_notice_restored`) | `release_hold` (`counter_notice_restore`) |
| `removed` | `legal_action_hold` | Sr | **India only:** a court order produced and reviewed before `rule75_period_ends_at` | active | active | notify R |
| `removed` | `restore_decision_required` | S | **India only:** `rule75_period_ends_at` reached without an accepted order. Restoration is permissive, not automatic (unless L-4 approves automatic restoration). | active | active | queue Sr |
| `restore_decision_required` | `restoring` | Sr (or S if L-4 approves automatic) | Decision to restore | releasing | per L-8 | `release_hold` (`rule75_restore`) |
| `restore_decision_required` | `removed_final` | Sr | Decision not to restore, per counsel policy | active | active | — |
| `legal_action_hold` | `restoring` | Sr | Evidence found invalid or lapsed, a dismissal is recorded, or re-verification fails. `reverify_due_at` is mandatory, so no hold is indefinite. | releasing | per L-8 | `release_hold` |
| `legal_action_hold` | `removed_final` | Sr | Adjudicated outcome recorded | active | active | — |
| `removed` | `withdrawn_after_removal` | C | Retraction | releasing | voided (`claim_withdrawn`) | `release_hold` (`claim_withdrawn`) |
| `removed`, `counter_notice_open`, `awaiting_restoration`, `legal_action_hold`, `restore_decision_required` | `reversed_on_review` | Rv/Sr | Erroneous approval; a reason is required | releasing | voided (`reversed_on_review`) | `release_hold` (`reversed_on_review`) |
| `restoring`, `withdrawn_after_removal`, `reversed_on_review` | `restored` | S | `release_hold` acked | released | as above | notify C and R |
| `restored` | `approved_enforcing` | Sr | Re-imposition on new evidence (`reinstated_on_review`, higher revision). By default no new strike: the unique-per-case rule means a new strike would need a new case. | placing | none | `place_hold` |

**Guards that apply to every row:**
- A case in `legal_regime = 'unassigned'` cannot be approved, cannot schedule restoration and cannot issue a strike. It escalates at `regime_due_at` and pages an owner; it never defaults to a regime. (Whether takedown may proceed before a regime is assigned is decision L-14.)
- Any restoration requires that every required forward has been sent. If it has not, the case is blocked and escalated, and the clock anchors do not move.
- A filed-action notice that arrives after `restored` never re-removes automatically. It opens a review task, or a new case.
- `in_court_order` cases (IT Rules court or government orders) use a separate intake with a 3-hour timer placeholder and an on-call page. They have no counter-notice path. `platform_policy_only` cases have no statutory clock; what they may do is decision L-1.

**Hold-state sets** (used by reconciliation):
- **Hold must be active:** `approved_enforcing`, `removed`, `enforcement_failed`, `counter_notice_open`, `awaiting_restoration`, `legal_action_hold`, `restore_decision_required`, `removed_final`.
- **Hold must be released:** `withdrawn_after_removal`, `reversed_on_review`, `restoring`, `restored`.
- **No hold:** `submitted`, `incomplete`, `closed_incomplete`, `under_review`, `withdrawn`, `rejected`.

A deleted or scheduled post does not change the case. The restriction row persists, and a release never undeletes or publishes.

### 7.2 Counter-notice (`trust.counter_notices.state`, US regime)

| From | To | Actor | Condition |
|---|---|---|---|
| — | `submitted` | R | The case is `removed`; one open counter-notice per case. The form states, before submission, that the respondent's name, address and phone will be sent to the claimant. |
| `submitted` | `incomplete` → `submitted` | Rv → R | Missing §512(g)(3) elements; a cured resubmission gets a fresh receipt time |
| `submitted` | `rejected` | Rv | Invalid under counsel's rules; the case stays `removed` |
| `submitted` | `accepted` | Rv | Effective; the case moves to `counter_notice_open` |
| `accepted` | `forwarded` | S | A durable delivery to C is recorded as `sent`. Receipt and forward times are kept separate. |
| `forwarded` | `closed` | S | The case reaches `restored`, `removed_final` or `reversed_on_review` |
| `submitted`, `accepted` | `withdrawn` | R | The respondent retracts; the case returns to `removed` |

Under `in_rule75` there is no counter-notice step. An uploader's statement is recorded as respondent evidence (or as an IT Rules grievance) and changes no Rule 75 deadline.

### 7.3 Ordinary appeals

An ordinary appeal can never move a case or a hold. The rules are in section 6.3.

---

## 8. Authorization and privacy

### 8.1 Two predicates, both required on every read

`copyright_discoverable(copy)` is evaluated in post-service on the **canonical** `posts` row, never the Redis body cache. Account-level questions are asked as the anonymous stranger (`uuid.Nil`), so the claimant's own follow, share or friendship grants nothing.

```
copy.deleted_at IS NULL AND copy.publish_at IS NULL
AND lower(btrim(copy.review_status)) = 'approved' AND copy.effective_review_status = 'approved'
AND lower(btrim(copy.visibility)) = 'public'            -- v1: exactly public; unlisted is OFF (compile-time constant)
AND copy.age_restricted = false
AND every media asset on copy: processing_status = 'ready' AND moderation passed
AND copy.author_id NOT IN post_hidden_authors            -- deactivated / pending deletion
AND graph.can(uuid.Nil, 'view_posts', copy.author_id)    -- public account; fail closed
AND copy.author_id <> claimant AND copy channel owner <> claimant
```

`match_readable(claimant, match)`:

```
copyright_tool_access(claimant) = allowed                       -- 8.3
AND match.reference_owner_id = claimant AND reference post not deleted or purged
AND copyright_discoverable(match.copy_post)
AND PostReadGate(match.copy_post_id, claimant) = nil            -- canonical detail gate incl. blocks and age (read_gate.go:1-31)
```

- **Never used as authorization for Matches:** `viewerMayViewPost`, `ViewerMayAccessMedia` / `viewerMayAccessPostMedia`, the anonymous poster route and realtime room admission, because of P-8.
- **Unlisted.** `copyrightMatchIncludeUnlisted = false` is a compile-time constant, not an operator flag, until decision F-11/L-11.
- **Raw data stays internal.** Media pairs (media ids, generations, offsets, per-frame distances) never leave media-service and post-service internals.
- **Removal submission.**
  1. post-service re-runs entitlement (uncached) and `match_readable`.
  2. It calls trust-safety.
  3. trust-safety calls `…/verify` and **rejects on any false**, inside the same request.
  4. The frozen `evidence_snapshot` holds: post ids, title and description at submission, channel id, upload timestamps, similarity and coverage, algorithm version, pair digest and still hashes.
  5. From then on the case lives under trust-safety authority. Later privacy changes do not dissolve it, and the claimant sees only the snapshot and the status.

### 8.2 Matrix

| Surface | Required checks (all, per request) | Failure result | Revocation latency (stated, not instant) |
|---|---|---|---|
| Match list | Entitlement; SQL discovery on the canonical row; batch hidden-author and `graph.can(uuid.Nil)`; batch `PostReadGate`; reference owned and not deleted | 403 `COPYRIGHT_ACCESS_REQUIRED` (with the public-form link) or 503 `ACCESS_UNRESOLVED`. Failing rows are omitted silently. A graph or hidden-author outage gives 503, never unfiltered rows. | Next request after commit. Account privacy ≤ 3 s (`privacy_gate.go:39`). Programme status ≤ 60 s. Revocations and grants 0 s. |
| Match detail | Same, for one match | 404 `MATCH_NOT_FOUND` (never 403) | Same |
| Aggregate counts | Computed from the same filtered query at request time; no stored counters; no "N hidden" hints | as list | Same |
| Thumbnail / preview | Full `match_readable` re-check, then one `thumb_300` still signed with `MaxProtectedTTL`, returned as a 302 with `no-store`. No URLs in JSON. No playback of the matched video in the tool; "View" links to the public watch page. | 404 / 503 | A URL already issued stays valid ≤ 5 min (`signer.go:72`); there is no earlier revocation mechanism. No new URL is issued after revocation. |
| Archive / unarchive | `match_readable`; idempotent; archive state persists while suppressed | 404 | Next request |
| Removal submit | Uncached entitlement; `match_readable`; trust-safety verify callback; atomic quotas and duplicate index; snapshot frozen | 403 / 404 / 409 `DUPLICATE_CASE` / 422 `INCOMPLETE` / 429 `LIMIT_REACHED`. Failures consume no quota. | 0 s (writes are never cached) |
| Case view (claimant/respondent) | Party to the case; frozen snapshot only; contacts per disclosure policy (L-6) | 404 | Next request |
| Reviewer queue | Admin token + `copyright.review`; PII masked; unmasking needs `copyright.pii.read` + step-up and is audited | 403 | Next request |
| Notification (inbox/push) | Digest built only from rows passing `match_readable` at send time; ids-only payload; `actor_user_id = uuid.Nil`; legal notices never use this channel | Nothing is sent if no rows are readable. Pending digest items are cancelled on a privacy change. | Delivered pings cannot be recalled, but they carry no match metadata |
| Realtime | None in v1 | n/a | n/a |
| Cached pages | `Cache-Control: private, no-store`; web queries use `staleTime: 0`, refetch on focus, `gcTime ≤ 60 s`, and clear the cache on any 403/404; no server-side list caching | A refetch yields 403/404 and clears data | Already-rendered data stays on screen until the next refetch; nothing new is served after revocation |
| Fingerprint worker | Worker service identity, internal object reads only | n/a | n/a (no URLs are created) |

**Privacy-change handling.** post-service consumes its own transitions: visibility, delete/restore, review, age restriction, `user.deactivated` / `user.deletion_scheduled` and their reversals, and account privacy. It uses them to update `last_discoverable_at` and cancel unsent digest items. It does **not** reset `first_surfaced_at` or `notified_at`, so a copy that returns to public reappears without a new notification.

**Deletion.** A soft-deleted copy is hidden, and reappears if the author restores it within 720 h. On a hard purge of either post, a sweep deletes the match rows within 24 h. Case snapshots are unaffected. On pair invalidation, the match is hidden until it is re-verified.

### 8.3 Entitlement, quotas and the public form

**"In the creator programme"** means the stored monetization row is eligible, unsuspended and evaluated within 48 h (section 6.5). The fresh decision never grants access, and viewer subscriptions are irrelevant. While monetization writes are off, the sweep does not run, so this definition grants nobody. That is intentional fail-closed behaviour. Decision F-2 chooses the launch basis.

**`copyright_tool_access(user)`**, first match wins:
1. An active revocation → **denied**. Only an audited admin reinstatement clears it.
2. Account hidden, deactivated or pending deletion, or a platform suspension from standing → **denied**.
3. An active admin grant → **allowed**.
4. An `upheld_request` grant written at decision time, dated after the latest reinstatement, and not `suppressed_by_revocation_id` → **allowed**.
5. Programme member → **allowed**.
6. Otherwise → **denied**.

The monetization answer is cached ≤ 60 s (positive and negative). Revocations and grants are never cached. Writes always evaluate uncached. If monetization is unreachable and nothing is cached, basis 5 is unknown: the result is `503 ACCESS_UNRESOLVED` unless basis 3 or 4 applies.

**Quotas** are enforced atomically in trust-safety, in the case-insert transaction, and shared by the Matches tool and the public form. Redis is not used. The numbers are **plan values pending decision F-3**.

| Key | Applies to | Limit |
|---|---|---|
| `account:<user_id>` | Tool, and the form when signed in | 10 new/day; 50 open |
| `claimant:<lookup_hash>` | Both (normalised legal name + verified email) | 10 new/day; 50 open across accounts and anonymous submissions |
| `claimant_target_channel:<hash>:<channel_id>` | Both | 20 open |
| `ip:<prefix>` | Anonymous form only | 20/day (abuse throttle, not a legal limit) |

- **Duplicates.** The same claimant and target returns `409 DUPLICATE_CASE` with the existing case id. Different claimants on the same target get separate cases, linked by `target_group_id`.
- **Invalid versus abusive.**
  - `incomplete` does not count.
  - `rejected_insufficient`, `rejected_licensed` and `rejected_exception_escalated` count as **invalid**. N invalid in 90 days (plan value 3, F-3) triggers a warning, then revocation.
  - `rejected_bad_faith` is **abusive**. It needs a second reviewer and step-up, and one occurrence revokes access. Whether abuse also creates a strike is decision L-9.
  - A revocation never auto-expires and emits `trust.copyright.access_changed`.
- **The public form stays open to everyone**, including revoked accounts and non-users, because a rights holder must be able to notify.
  - A submission from a revoked account or revoked claimant hash is flagged `submitter_access_revoked` for reviewers.
  - An upheld form submission writes its grant **suppressed** while a revocation is active.
  - The form never lists, reveals or unarchives matches.

### 8.4 Ownership, licensing and remixes (reviewer handling)

Before a decision, the reviewer must complete this checklist. Each item has an outcome code.

| Question | Case fields | Reviewer action |
|---|---|---|
| Claimant authority and scope | `claimant_role`, `work_description`, `rights_territories`, authority evidence, agent's principal (sealed) | Missing or implausible → `incomplete`. An agent without a principal → `incomplete`. |
| Is the claimant's own video the later side of another pair? | `reference_is_matched_later`, the earlier pair ids | Senior tier. Cannot be upheld on similarity alone. |
| Licence or collaboration | Respondent evidence, the claimant's `licensed` archive history, the reference post's licence (`standard`/`creative_commons`), collaboration note | A credible licence → `rejected_licensed`. For Creative Commons, the terms check is mandatory before `upheld`. |
| Public domain | Who asserted it, and on what basis | Escalate; the criteria are decision L-7 |
| Exceptions (fair use / fair dealing) | `exception_asserted`, basis | Record and escalate; applicability is L-7. The platform does not adjudicate exceptions. |
| The respondent's video is earlier | `claimant_upload_after_respondent`, respondent evidence | Needs authority evidence beyond similarity; senior tier |
| Remix | `respondent_declared_remix`; `remix_verified` is always false in v1 | Informational only |

- **Licensing evidence in v1.** No allowlist is needed.
  - A claimant `licensed` archive pre-archives future matches from that channel **for that claimant only**.
  - The respondent may attach evidence to the case. In v1 it does not pause an upheld removal; it feeds the counter-notice and reversal review.
  - Whether there is a pre-decision respondent window is decision F-10.
- **Remix lineage (P-10).**
  - No automatic exemption and no automatic suppression.
  - Future lineage is written only by a server-side compose flow. That flow resolves the source from its own session, checks `PostReadGate(source, remixer)` and the source's `remix_setting`, and stamps `remix_source_id`, `remix_type` and `remix_verified_at` in the same transaction as the post insert.
  - Client-asserted lineage is shown only as "declared, unverified".
  - Even verified lineage is evidence for the reviewer, not an exemption.

---

## 9. Idempotency, retries, ordering and reconciliation

### 9.1 Media (fingerprint jobs and pairs)

- **Jobs.**
  - Key `(asset, generation, algo_version)`, enqueued with `ON CONFLICT DO NOTHING` in the completion transaction.
  - Claimed with `SKIP LOCKED`, a claim token and a heartbeat. A lease older than 10 min is reclaimable.
  - The final write needs a matching claim token and the generation fence.
  - At most 5 attempts, then `failed` with an alert. The upload is never affected.
- **Writes.**
  - Postings use `ON CONFLICT DO NOTHING`. The fingerprint row and its postings commit together.
  - **Insert-then-query:** a job commits its own postings before it queries, so of two concurrent jobs the later query sees the other's postings. The `pair_key` uniqueness collapses a double find.
  - `pair_revision` bumps only on a change of class, score, status or direction. Each revision writes exactly one outbox row, in the same transaction.
- **Crash recovery.**
  - A crash before commit leaves nothing behind.
  - A crash after commit but before `done` means the reclaim finds the postings already present and the pairs unchanged, so no duplicate events are written.
  - A relay crash between publish and mark republishes the same `event_id`, and the consumer inbox collapses it.
- **Ordering.** Pair events are keyed by `pair_id`. The consumer drops `revision ≤ last_revision`. An invalidation carries a revision and can be superseded. Transcode events keep their existing table, constraint, relay and topic.

### 9.2 Restrictions (trust-safety → post-service)

- **Stable ids.** `decision_id` is minted once per case transition. Retries re-sign the capability but never change the id.
- **Post-service guards.**
  - A replay with the same digest returns the stored result. A different digest is a conflict.
  - `case_revision` must increase, and `expected_state` must match. A delayed older command can therefore never undo a newer one.
- **Dispatcher.** It claims outbox rows with `SKIP LOCKED` and signs at send time.
  - 2xx or a replay → `acked`, and the case advances.
  - 5xx or a transport error → exponential backoff with jitter, 30 s up to 30 min.
  - `409 STALE_CASE_REVISION` → `superseded` if a newer command exists; otherwise escalate.
  - 403, `DECISION_CONFLICT`, `SUBJECT_MISMATCH` and 404 are **never retried blindly**. They escalate, and the case shows "enforcement failed".
  - Not acked within 1 h (proposed) → page.
- **Parties are told "removed" only after the ack.** The P95 < 60 s enforcement latency is a measurement goal, not a promise.

### 9.3 Cases and strikes

- **Case decisions.** Every human decision carries a client `decision_id` and an `expected_case_revision`. Replays with the same digest return the stored result. The case row lock serialises concurrent reviewers.
- **Strikes.** The strike, audit and outbox writes share the case transaction. `uq_user_strikes_case` and the idempotency key mean that duplicate requests, double-clicks and dispatcher restarts yield **one** strike.
- **Appeals.** The `overturning` state plus `expected_base_decision_id` serialise uphold versus overturn and admin versus appeal (section 6.3).

### 9.4 Notices and pings

- Deliveries are keyed by `notice_id:party:channel`, and the provider idempotency key is the same value. A duplicate `sent` callback is a no-op.
- `case_updated` events are written to the trust-safety outbox. notification-service consumes them durably and dedupes on `event_id`.

### 9.5 Reconciliation

| Job | Where | Cadence | Checks | Repair |
|---|---|---|---|---|
| Hold reconciliation | trust-safety, reading `GET /v1/posts/internal/restrictions` | Every 15 min (incremental) and daily (full) | Every hold-active case has an `active` row at revision ≥ its last enforced revision. Every released-state case has a `released` row. | Re-enqueue the **recorded** decision only. An orphan active row with an unknown case is **never auto-released**: P1 alert and admin queue. |
| Strike reconciliation | trust-safety | Daily | A hold-active case has exactly one non-voided strike (where policy issues one). A released case's strike is voided or expired. | Re-enqueue recorded decisions only. It never creates or voids a strike on its own. |
| Restriction count | post-service | Nightly | `active_restriction_count` equals the number of active rows | Repair under `FOR UPDATE`, emit eligibility, alert |
| Pair→match | post-service | Nightly | Matches whose evidence pairs are all invalidated are `pending_recompute` | Recompute from the pair events |
| Stale postings | media-service | Continuous sweeper | Postings for generations older than current | Batched delete |
| Outbox health | all three | Continuous | Oldest pending age | Metrics `enforcement_outbox_oldest_pending_seconds`, `copyright_reconcile_missing_hold`, `…_orphan_hold`, `…_stale_revision` |

---

## 10. Retention and legal-hold rules

1. **Retention classes.**
   - **Case evidence:** sealed notices, declarations, signatures, validation outcomes, decisions and rationale, reviewer ids, delivery records, strike linkage, the evidence snapshot and filed-action documents. Kept until `retain_until = closed_at + R`. R is decision L-10; no placeholder value is approved.
   - **Matches with no case:** deleted when either post is purged.
   - **media-service fingerprints, postings and pairs:** deleted with either asset (after writing invalidation events).
   - **Contact PII:** kept only as long as the case evidence.
2. **An evidence snapshot, not the video.** A case freezes metadata and a few still hashes, optionally with the stills as protected objects under `protected/copyright/<case_id>/`. Full video blobs are **not** retained after the uploader purges unless a `media` legal hold exists.
3. **Account purge meets a case.** For copyright tables, trust-safety `PurgeUser`:
   - replaces user ids with a per-case subject token (the dating precedent);
   - keeps the case-evidence class until `retain_until`;
   - deletes everything else (tool preferences, archived matches, known-licensee lists).

   The purge ack is still sent, and it means "erased except documented retention exceptions". The exception list belongs in the privacy notice (L-10).
4. **Cross-service conflicts to resolve before launch (P-13).**
   - post-service purge deletes `post_moderation_decisions`, and trust-safety purge deletes `user_strikes` and `content_appeals`. Copyright decision, hold and strike records therefore live in trust-safety case tables, and the case, not the post, carries the evidence.
   - Media purge must skip blobs under an active `media` legal hold.
   - Notification inbox deletion is acceptable, because inbox rows are not evidence.
5. **Soft-deleted copies.** The 720 h purge proceeds normally, because the case holds the snapshot. An author's restore of a held post stays hidden because of the restriction (section 6.2).
6. **Legal holds.**
   - Placing or releasing one needs `copyright.legal_hold`, step-up and an audit row.
   - A hold overrides `retain_until` and every purge path for its scope.
   - `review_by` is mandatory. An expired, unreviewed hold raises an ops alert and is **not** auto-released. Nothing is held indefinitely without a human choosing it again.
7. **Sweeper.** A daily job deletes case evidence past `retain_until` with no active hold, in batches, writing an audit row per case (a count only, no content).

---

## 11. Jurisdiction handling

> **This is not legal advice, and nothing here is approved.** It is an engineer's reading of primary texts, so the data model can represent them. Counsel decides which regime applies, how deadlines are computed, and every item in L-1…L-14. Indian case law on how Copyright Act s.52(1)(c), IT Act s.79 and the s.81 proviso interact for user-upload hosts was **not** reviewed.

### 11.1 Why one "waiting period per country" is wrong

| | US 17 U.S.C. §512(c)/(g) | India Copyright Act s.52(1)(c) and Copyright Rules 2013, Rule 75 |
|---|---|---|
| Source | https://www.law.cornell.edu/uscode/text/17/512 (confirm currency at https://uscode.house.gov) | Act: https://copyright.gov.in/Documents/CopyrightRules1957.pdf (consolidated Act text despite the file name). Rules: https://copyright.gov.in/Documents/Copyright_Rules_2013_and_Forms.pdf |
| Applies to | Material stored at a user's direction, for a provider with a designated agent | "Transient or incidental storage" for links, access or integration. Whether a UGC video host falls within this is for counsel. |
| Takedown trigger | An effective §512(c)(3)(A) notice to the designated agent (six elements, "substantially") | A written complaint with the Rule 75(2)(a)–(f) particulars, including an undertaking to sue, **if the host is satisfied** |
| Takedown timing | "expeditiously to remove, or disable access" (§512(c)(1)(C)); no fixed hours | Within 36 hours (Rule 75(3)) |
| Restore clock starts at | Receipt of an **effective counter-notice** | Receipt of the **complaint** |
| Period | "not less than 10, nor more than 14, business days" (§512(g)(2)(C)) | Up to 21 days, or an earlier court order (s.52(1)(c) proviso; Rule 75(3)) |
| What prevents restoration | The designated agent first receives notice that the claimant has **filed an action** | The complainant **produces a court order** |
| At expiry | Replace the material (a condition of the §512(g)(1) protection) | The host "may restore" (Rule 75(5)); permissive |
| Uploader's role | The counter-notice starts the clock | None in this Rule. An IT Rules grievance is a separate channel. |
| Other | §512(c)(3)(B): a curable deficiency needs a prompt attempt to contact the sender. §512(g)(2)(B): a copy of the counter-notice goes to the claimant. §512(i): repeat-infringer policy. §512(f): misrepresentation liability. The designated-agent registration expires after 3 years (37 CFR 201.38(c)(4), https://www.law.cornell.edu/cfr/text/37/201.38). | Rule 75(4): show viewers a reasons notice. Rule 75(6): no obligation to respond to a repeat complaint from the same complainant after failure to produce an order. |

**IT Rules 2021 context** (consolidated text "updated as on 10.02.2026", https://www.meity.gov.in/static/uploads/2026/02/550681ab908f8afb135b0ad42816a1c9.pdf):
- Rule 3(1)(b)(iv): user terms must bar copyright-infringing content.
- Rule 3(1)(d): court or government orders, apparently now due "within three hours". The G.S.R. 120(E) commencement date comes from secondary reporting and is to be confirmed on https://egazette.gov.in.
- Rule 3(2)(a)(i): grievances are acknowledged in 24 h and resolved in 7 days. The 36-hour removal-request proviso appears to exclude the copyright item.

These appear to conflict with P-20 (a 15-day timer). Counsel confirms.

### 11.2 Design rules

- **The regime is a case attribute selected by a person.** `legal_regime` is one of `us_512`, `in_rule75`, `in_court_order`, `platform_policy_only`, `unassigned`, with `regime_basis`, `regime_set_by` and `regime_set_at`.
  - Account country, IP and claimant address are shown as **inputs only** and never set the regime automatically.
  - `unassigned` blocks approval, restoration and strikes, and escalates at `regime_due_at`.
  - Changing a user's country never alters an open case. Changing a regime is a separate audited action that recomputes deadlines.
  - One post may carry several cases under different regimes, each an independent hold.
- **Separate, write-once timestamps.** Each of these is stored with `calendar_version` and `policy_version`:
  - notice receipt;
  - validation (a per-element checklist for the regime; `deficient_curable` creates a prompt-contact task);
  - the decision;
  - enforcement ack (`disabled_at`; for Rule 75, `disable_due_at = received + 36 h`, with counsel to confirm the start event);
  - subscriber notified;
  - counter-notice receipt and effectiveness;
  - forwarding (with delivery records);
  - the US window `restore_not_before` / `restore_not_after` from `counter_received_at` on an **approved business-day calendar** (holidays, time zone, cut-off);
  - the Rule 75 period end;
  - closure.

  Corrections are new audit rows. A calendar change triggers an audited recompute and never moves deadlines silently.
- **Filed-action and court-order evidence is reviewed, bounded, and never a checkbox.**
  - **US.** A claim that an action was filed requires the court, the case number, the filing date, the parties, and the complaint or a docket reference. It records `action_notice_received_at`. The review is due at the earlier of a counsel-approved offset and `restore_not_after` minus an operational margin.
    - Accepted in time → `legal_action_hold`, with a mandatory `reverify_due_at`.
    - Rejected, or not reviewed in time → restoration proceeds by `restore_not_after`. The assertion is logged.
  - **India.** Only a produced **court order** ends the Rule 75 period early. An undertaking to sue is required notice content, not an order.
  - The schema has **no** boolean such as `legal_action_taken` that affects state.
- **Disclosures.**
  - US: the respondent's counter-notice (name, address, phone) is copied to the claimant, and the form says so before submission.
  - What the respondent sees of the claimant's notice, India disclosures, and whether the Rule 75(4) public notice names the complainant are all decision L-6.
  - Inbox and push carry only safe case links.
- **Repeat-infringer policy (§512(i))** is separate from the strike ladder, although it may reuse it. Counsel decides which events count (for example, upheld notices only, not ones reversed by counter-notice or restored under Rule 75), the look-back window, and termination criteria. Decision F-6 is therefore partly a compliance item.
- **Legal launch prerequisites (not code defects):**
  - register the US designated agent and publish it in-product, with a 3-year renewal alarm;
  - publish the Grievance Officer details;
  - obtain counsel's applicability memo;
  - produce an approved business-day calendar artifact;
  - confirm the grievance timelines (P-20).

---

## 12. Matching algorithm and evaluation plan

The pipeline is **not exact**. Only the frame-pair lookup at radius 2 is exhaustive, for Hamming ≤ 11. The claim made to users and reviewers is the **measured** recall and precision below, once measured. Audio is **not used** in v1.

### 12.1 Input contract

Selection is evaluated at job start from one snapshot of `media_assets` (generation, `ready_generation`, `hls_master_key`, `duration_ms`) plus `media_variants`:

1. **Preferred: `hls_rung`, the lowest rung.**
   - Read and strictly parse the worker-generated master at `hls_master_key`.
   - Choose the variant with the **lowest `RESOLUTION` height** (expected `360p`), breaking ties by lowest `BANDWIDTH`. Selecting by resolution rather than bandwidth keeps the choice stable if the in-progress change starts deriving `BANDWIDTH` from measured segment peaks.
   - Stream its segments in playlist order through the worker's **own blob client** (IRSA in prod, MinIO in dev) into `ffmpeg -f mpegts -i pipe:0 -vf "crop=…,fps=2,scale=64:64,format=gray" -f rawvideo pipe:1`.
   - Segments are the checkpoint unit.
2. **Fallback A: `mp4_variant`.** Only when there is no `hls_master_key` (legacy). Use the smallest existing `video/mp4` variant by height, with range reads.
3. **Fallback B: `original`.** Only when neither exists. Range-read the original, never with `DownloadObject`, under an admission cap (≤ 3 h and ≤ the benchmarked size). Otherwise `skipped` with reason `original_too_large`.

**Why the lowest rung.**
- At HEAD, `hlsVariantsFor` always keeps the first rung (360p) and upscales short sources (`video.go:40-54`).
- Any rung failure fails HLS (`video.go:95-99`), and an HLS failure fails the transcode (`main.go:706-711`).
- So `ready` implies `hls/master.m3u8` plus a 360p rung.
- A 360p **MP4** is not guaranteed (P-18), and `MinVideoResolution` proves nothing.
- **Re-verify this guarantee after the in-progress HLS change lands.** That change builds rungs by stream copy from the MP4 renditions, with a per-rung re-encode fallback. Tests T15-1, T15-2 and T15-6 must pass against the new pipeline, including the case of a failed 360p MP4 with the fallback encode.

**Access and consistency.**
- No presigned or CDN URL is created, logged or put in any event. Relative playlist paths mean a presigned playlist would not sign its segments anyway.
- The measured duration must be within 2% (or 1 s) of `duration_ms`. Otherwise the job is `input_inconsistent` and retried after the generation re-check.
- `input_kind`, `input_ref` and `input_etags` are recorded.
- The final write is fenced by generation (section 6.1).

### 12.2 Per-frame data

- **Border crop.** One crop rectangle per video: the median of `cropdetect`-style luma bounds (threshold 24/255) over up to 20 non-flat frames. It is applied to every frame, which is then resized to 64×64. Blurred-background fills are not removed in v1; they form a separate evaluation stratum.
- **Hash.** A 64-bit pHash (32×32 DCT of the 64×64 grey frame) at **2 fps**.
- **Flags and weights.**
  - `flat` (luma stddev < 6, or low AC energy) has weight 0 on both sides.
  - `static_repeat` (Hamming ≤ 2 to the previous frame) is kept for alignment, but each static run contributes **at most 2 s** of weight. This replaces "drop static frames", so a slideshow keeps its timeline.
- **Storage.** The whole sequence is stored, flat and static frames included.

### 12.3 Index and lookup

- **Symmetric roles.** Every ready video is indexed as a potential reference and queried as a potential copy.
- **Anchors (indexed side).**
  - Grid spacing `s = clamp(floor(informative_s / 12), 1, 5)` seconds, so every video with ≥ 12 informative seconds has ≥ 12 anchors.
  - Skip flat frames and anchors within Hamming ≤ 2 of the previous anchor.
  - Never index degenerate hashes: popcount ≤ 4 or ≥ 60, plus an operator stop-list.
- **Query frames.**
  - Every 2 fps frame when informative duration ≤ 60 s.
  - Otherwise three windows of `min(20 s, 10%)`, centred at 20%, 50% and 80% of the duration, giving ≤ 120 query frames.
  - The recall cost of windowing is measured, not assumed (E3).
- **Phase after trims.** Anchors sit on integer seconds and queries run at 2 fps, so every anchor inside a window has a query frame within ≤ 0.25 s, whatever the trim. Verification also runs at 2 fps on both sides.
- **Probe strategy: 4×16-bit bands, radius-2 probing in every band.**
  - That is 137 probes per band and 548 per query frame.
  - By pigeonhole, some band has ≤ ⌊d/4⌋ differing bits, so radius 2 guarantees a band hit for d ≤ 11. The finding's 3/3/2/2 split is found, because two bands are within radius 2.
  - Exact band lookup (radius 0) guarantees only d ≤ 3.
  - Each posting carries the full hash. Hits are filtered to Hamming ≤ τ_hit = 12.
  - Each hit records the minimum band distance that produced it, so the evaluation can compute exactly what radius 1 or 0 would have found.
  - **Uniform-hash expected candidate rows per query frame**, a model rather than a measurement (real pHash is less uniform, so real volume is higher):

    | Layout, radius | Probes/frame | Guaranteed d ≤ | N=1M | N=10M | N=72M |
    |---|---|---|---|---|---|
    | 4×16, ρ=0 | 4 | 3 | 61 | 610 | 4,395 |
    | 4×16, ρ=1 | 68 | 7 | 1,038 | 10,376 | 74,707 |
    | **4×16, ρ=2** | **548** | **11** | **8,362** | **83,618** | **602,051** |
    | 8×8, ρ=1 | 72 | 15 | 281,250 | 2.8M | 20.3M |
    | 3×(21,21,22), ρ=3 | 4,918 | 11 | 1,917 | 19,174 | 138,050 |
- **Duration buckets** are part of the index key: `dur_bucket = floor(ln(informative_s)/ln(1.25))`.
  - The coverage rules (below) imply that the reference duration R lies in [0.80·C, 1.176·C], where C is the copy duration.
  - The lookup therefore queries **every bucket overlapping [0.75·C, 1.25·C]**: compute both endpoint buckets and include everything between them. No pair is lost at a boundary.
- **Hot keys and fan-out.**
  - Static stop-list.
  - Per-key cap `LIMIT 257`. A hot key is dropped **entirely**, never partially.
  - A nightly `copyright_band_hot` refresh.
  - Per-upload cap of 2M candidate rows, after which the job is `lookup_truncated`.
  - Every truncation is recorded in `truncation` and metrics. A truncated pair is never counted as "no match".
- **Scale gate.** Radius 2 is allowed only while measured candidate rows stay at p95 ≤ 2M per upload and lookup p95 ≤ 5 s on the prod DB class (B2). That is roughly N_eff ≈ 2M anchors, about 2,800 catalogue hours if buckets filter nothing. Beyond the gate there are two options (decision F-12):
  - (a) radius 1, only if the measured video-level recall loss versus radius 2 **and** versus exhaustive search is ≤ 1 pp;
  - (b) a dedicated in-memory MIH/ANN index.

  At 72M anchors (100,000 catalogue hours at 5 s spacing), neither PostgreSQL option is viable. The trigger is anchor count and measured latency, not hours.
- **Voting.** Offsets are binned into 0.5 s bins. Proceed with ≥ 2 hits in one bin, or in adjacent bins.

### 12.4 Verification and classification

- **Alignment.**
  - Walk both full 2 fps sequences at the voted offset.
  - A frame pair matches if Hamming ≤ τ, with **τ = 10 as an unvalidated starting value** tuned in shadow.
  - Gaps ≤ 3 s are bridged for continuity but add **no** matched weight.
  - Segments are ≥ 5 s; up to 8 monotonic segments with per-segment offsets. The time scale is fixed at 1.0.
- **Coverage denominators** are on informative weight: `ref_coverage = matched_ref / informative_ref` and `copy_coverage = matched_copy / informative_copy`. Also recorded: `matched_seconds`, `matched_informative_s`, and `diversity` (matched frames pairwise more than Hamming 10 apart, capped at 10).

| Class | Rule | v1 use |
|---|---|---|
| `full_or_near_full` | ref ≥ 0.85, copy ≥ 0.80, matched informative ≥ 8 s, diversity ≥ 3, reference informative ≥ 10 s, no truncation involved | The only actionable class |
| `contains` | ref ≥ 0.85, copy < 0.80 (compilations, embedded references) | Shadow only |
| `partial` | ref < 0.85, matched informative ≥ 30 s | Shadow only |
| `short_clip_candidate` | reference informative < 10 s; ref and copy ≥ 0.95; median Hamming ≤ 6; diversity ≥ 2 | Shadow only, until its stratum meets the precision criterion |
| `insufficient_evidence` | Either side < 8 s informative, diversity < 3, truncated, or the short-clip rule fails | Never surfaced; counted |

- The plan's earlier "copy ≥ 60%" did not exclude compilations: 6 minutes of reference in a 10-minute compilation scores 0.60. The copy threshold is now 0.80, and that case is `contains`.
- A single-image podcast is `insufficient_evidence`. It is not an audio match.
- The absolute minimum duration stays `MinVideoDurationSeconds = 3` (`video.go:298-299`).
- **Similarity confidence.** A calibrated band (high/medium), from median and p90 Hamming plus coverage, is shown **only after E1 calibration**. Until then, no percentage labelled as confidence is shown to any user.
- **Audio is deferred.** No `fpcalc` and no in-house fallback. Shared music therefore cannot create or strengthen a match. If audio is re-introduced, it may only **annotate** a visual match, never promote a visually insufficient pair, and only with measured evidence.

### 12.5 Evaluation (PROPOSED; none executed)

- **E1: labelled set.**
  - **References:** ≥ 500, stratified by informative duration (< 10 s, 10–60 s, 1–10 min, 10–60 min, > 60 min), content type (talking head, gameplay, music video, slides/static, sports, animation, screen recording) and orientation. Corpus choice (open-licensed, platform-owned, or research corpora such as VCDB or FIVR-200K subject to licence review) is decision L-12.
  - **Transformed positives** (ffmpeg; each alone and in 2–3-transform chains): re-encode at CRF 32; scale to 240p; 24/60 fps; brightness, contrast and saturation changes; a 10% logo overlay; letterbox and pillarbox; trims of `-ss 1`, `-ss 0.4`, 10% head and 85% length (frame-accurate); replaced audio.
  - **Reported but out of pass scope:** hflip, 80% crop, 1.1× speed, picture-in-picture, blurred fill.
  - **Hard negatives** (≥ 1,000 pairs): same-creator episodes sharing an intro or template; different takes of a scene; slide decks on one template; **the same music with different visuals**; one game level recorded by different players; fade- and black-heavy videos; **compilations** (labelled `contains`); reaction and remix videos.
- **E2: candidate stage versus exhaustive search.** Brute-force all query frames against all reference frames at full Hamming, for every labelled positive. Compare with radius 2, and with the counterfactual radius 1 and 0 from the recorded band distances.
- **E3: shadow recall and precision.** Sampling only detected matches cannot measure missed ones. So:
  - for a random 1% of new uploads (capped per day), run exhaustive search offline and compare it with the index path;
  - label a sample of **all** pairs found by either path;
  - have humans label every `full_or_near_full` pair found in shadow, up to a cap.
- **Pass criteria (PROPOSED; decision F-12):**
  - precision of `full_or_near_full` ≥ 99%;
  - zero actionable false positives across ≥ 1,000 hard negatives;
  - recall ≥ 95% for references ≥ 30 s and ≥ 90% for 10–30 s on in-scope transforms, each with a Wilson 95% lower bound reported;
  - candidate-stage loss versus exhaustive ≤ 1 pp;
  - compilations never `full_or_near_full`;
  - shared-music negatives never actionable;
  - static slides correct or `insufficient_evidence`;
  - short clips stay shadow-only unless their stratum independently passes.

### 12.6 Audio, phase 2 only: the fpcalc gate

- **Package availability** (primary package data, not a build): Alpine v3.19 community has `chromaprint` 1.5.1-r6, which ships `/usr/bin/fpcalc`, and `chromaprint-libs`, for x86_64 and aarch64. It links against FFmpeg 6 sonames matching the `ffmpeg 6.1.1-r0` already installed by `Dockerfile.worker`.
  - https://pkgs.alpinelinux.org/packages?name=chromaprint*&branch=v3.19&arch=x86_64
  - https://pkgs.alpinelinux.org/contents?file=fpcalc&branch=v3.19&arch=x86_64
  - https://gitlab.alpinelinux.org/alpine/aports/-/raw/3.19-stable/community/chromaprint/APKBUILD
- **Runtime behaviour is unverified.** Before any audio work, the proposed CI gate G1–G8 must run green:
  - **G1** build per architecture;
  - **G2** `apk info` and `ldd` linkage;
  - **G3** a version regex;
  - **G4** a deterministic raw-PCM pipe with `-format s16le -rate 11025 -channels 1 -length 0 -raw -json -`, run twice for byte identity, and again without `-channels`;
  - **G5** the `-length` default trap (≈120 s without, ≈180 s with `-length 0`);
  - **G6** file-path text format;
  - **G7** failure exit codes mapped to "insufficient audio evidence";
  - **G8** a non-root, read-only, 256 MB, 1 CPU, no-network smoke run.
- `-channels` is deprecated in FFmpeg 6.1.1's PCM demuxer and expected to disappear with libavutil 59. The gate re-runs on every base-image or package bump, and it runs after P-17.

---

## 13. Capacity and operational safeguards

**Environments differ** (the earlier "the worker is limited to 1 CPU" described dev only):

| Env | Worker resources | Source |
|---|---|---|
| Dev (HEAD) | 1.0 CPU / 1G limit, 0.5 / 512M reservation | `Architecture/docker/docker-compose.yml:940-967` (HEAD) |
| Dev (in-progress, uncommitted) | Raised to 4 CPU / 2G | working-tree change; re-read after it lands |
| Staging | 1 replica; requests 500m / 512Mi; limits 2 CPU / 2Gi; 20Gi scratch | `deploy/services/media-service/values-staging.yaml:90-105` |
| Prod | 3 replicas; requests 1 CPU / 1Gi; limits 4 CPU / 4Gi; 50Gi scratch; no HPA; no worker readiness probe | `deploy/services/media-service/values-prod.yaml:89-104` |
| Prod DB | Aurora `db.r7g.large` + reader; 30-day backups | `infra/terraform/envs/prod/main.tf:95-98` |

**Storage model** (estimates at 80–120 B per posting; B2 measures the real value):
- 100,000 catalogue hours at one anchor per 5 s is **72M anchors**. With 4 bands that is 288M postings, **23–35 GB**.
- The 2 fps sequences add about 6.5 GB.
- Redo/WAL on Aurora, 30-day backups and vacuum after bulk invalidation all add to this.
- The earlier "15–30 CPU-seconds and 45–75 KB per 10-minute video" were **estimates, not benchmarks**.

**Safeguards:**

- **Kill switches.** All default off, all independent, all re-read every loop iteration (ConfigMap-sourced, so no redeploy is needed):
  - `COPYRIGHT_FINGERPRINT_ENABLED`
  - `COPYRIGHT_FINGERPRINT_BACKFILL_ENABLED`
  - `COPYRIGHT_INDEX_LOOKUP_ENABLED`
  - `COPYRIGHT_PAIR_RELAY`
- **Admission.** A worker claims a fingerprint job only when all of these hold:
  - `busyTracker` reports idle past its grace;
  - the exported oldest-pending-transcode age is < 60 s;
  - DB replica lag is < 30 s and write latency is within the benchmarked limit.
- **Pre-emption.** A transcode arriving mid-job cancels the fingerprint job. Progress is checkpointed per segment chunk (~5 min). One job per pod, `ffmpeg -threads 1`, low priority. Alternative: a separate `media-fingerprint-worker` Deployment, which needs a chart change (F-14).
- **Memory.** Streaming only; never `DownloadObject`. Frames are 4 KiB each. Target RSS ≤ 512 MiB.
- **Deadlines.**
  - Per job: `60 s + k × duration`, with k from B1.
  - Lookup statement timeout: 5 s.
  - Verification: 30 s per candidate, ≤ 50 candidates per upload, and the rest recorded as truncation.
- **Backfill.**
  - A token bucket, starting at a PROPOSED 50 media-hours per hour cluster-wide.
  - Paused when admission fails and in a configured peak window.
  - Postings written in batches of ≤ 5,000 rows.
  - Stops above 10,000 queued new-upload jobs.
- **Alerts.** New-upload jobs older than 24 h; failed jobs > 1%/day; hot-key truncations above baseline; pair-relay rows past 20 attempts. `skipped` requires a reason, and nothing is dropped silently.
- **Readiness budget (P-16).**
  - Add `media_upload_ready_latency_seconds` (confirm → ready) and `media_transcode_queue_wait_seconds`, and export oldest-pending age as a gauge.
  - Collect a **14-day baseline** before enabling fingerprinting.
  - **Gate:** with fingerprinting on, p95 and p99 ready latency regress by ≤ max(5%, 15 s), and queue wait p95 does not rise.
  - **Circuit breaker:** if the oldest pending transcode age exceeds 120 s while fingerprint jobs run, stop claiming for 15 minutes.
  - Unchanged readiness is **not promised**; it is gated by measurement.

**Benchmarks (PROPOSED; none executed):**

- **B1: extraction.**
  - Inputs: HLS 360p rungs and the fallbacks; 10 s to 3 h; 240p to 4K sources; portrait and landscape; 24, 30 and 60 fps.
  - Hardware: a prod-equivalent Graviton arm64 pod (1 CPU request, 4 CPU limit, 4Gi).
  - Metrics: CPU-s per media-minute, peak RSS, scratch bytes, and wall time with and without a concurrent transcode.
  - Pass: p95 ≤ 6 CPU-s per media-minute; RSS ≤ 512 MiB; a concurrent transcode slows by ≤ 5%.
- **B2: index.**
  - Setup: a `db.r7g.large` clone with background load; N = 1M, 10M and 72M anchors; uniform hashes and E1-sampled real hashes.
  - Metrics: lookup p50/p95/p99, rows per upload, buffer-cache impact on other services, bytes and WAL per posting, reader lag, vacuum time.
  - Pass: lookup p95 ≤ 5 s and ≤ 2M rows; ≤ 150 B per posting; reader lag ≤ 30 s; other services' reference query p95 rises by ≤ 10%.
- **B3: contention.** Replay the upload trace at 1×, 2× and 5× peak with backfill on. Pass: the readiness gate holds, and backfill self-pauses at 5×.

---

## 14. Backward-compatible rollout and rollback

### 14.1 Flags

| Flag | Owner | Default | Effect |
|---|---|---|---|
| `COPYRIGHT_FINGERPRINT_ENABLED`, `…_BACKFILL_ENABLED`, `COPYRIGHT_INDEX_LOOKUP_ENABLED`, `COPYRIGHT_PAIR_RELAY` | media-service | off | See section 13 |
| `COPYRIGHT_MATCH_SURFACE` | post-service | off | Matches API and UI. When off, the consumer still records matches in shadow. |
| `COPYRIGHT_CASES_ENABLED` | trust-safety | off | Case intake (tool and form) |
| `COPYRIGHT_ENFORCEMENT_DISPATCH` | trust-safety | off | Dispatches restriction commands. When off, cases can be decided but holds queue. |
| `COPYRIGHT_TOOL_ALLOWLIST` | trust-safety | on (pilot) | Access limited to admin grants during the pilot |
| `copyrightMatchIncludeUnlisted` | post-service | **false, compile-time** | Not an operator flag |

### 14.2 Phases and migration order

Every schema step is **expand-only**: additive columns with defaults, new tables, `NOT VALID` constraints. Any "contract" step comes only after a stable period.

- **Phase 0: stand-alone prerequisites.** These are worth doing even without Copyright Match.
  1. P-5 standing contract: the trust-safety route first, then the post-service signer and URL, then switch the client, then extend the choke point.
  2. P-6 strike schema, with the legacy decision F-15.
  3. P-7 trust-safety outbox.
  4. `admin_audit` target types.
  5. P-20 grievance timers, once counsel confirms (L-3).
- **Phase 1: media foundations.**
  - P-17: the CI worker image, ECR repository, key-specific tag bump, and gate T14-1..4.
  - P-14: generation and upload-time columns, confirm and requeue writers, completion guard.
  - P-16: metrics, then start the 14-day baseline.
  - P-18: re-verify the rung guarantee after the HLS change lands; run the legacy no-HLS count.
  - Add the job, fingerprint, posting, pair and pair-outbox tables. Ship code with every flag off.
- **Phase 2: shadow.** Duration is set by E1–E3 and B1–B3, not by a calendar.
  - Enable fingerprinting for new uploads on dev and staging, then prod, subject to the readiness gate.
  - Enable lookup and the relay. post-service records matches with the surface off.
  - Run E1–E3. Enable backfill only after B2/B3 pass.
- **Phase 3: post-service restrictions** (before any hold can exist).
  1. Migrate `post_restrictions`, events and the `posts` columns.
  2. Swap the read path to the helper, add the source-scan test, audit the eligibility consumers.
  3. Ship the event field change.
  4. Provision the new HMAC key.
  5. Add the extended moderation subject.
  6. Ship the appeal capability v2: post-service accepts v1 and v2 first, then trust-safety emits v2, then post-service requires v2 for appeal approvals. Backfill `appealed_decision_id`.
- **Phase 4: Matches surface (internal pilot).** Entitlement via admin grants only, the Matches API and web page, the thumbnail route, and the notification ping. Discovery predicates as specified.
- **Phase 5: cases and removal (pilot).**
  - Case tables, legal notices and deliveries, contacts (`copyright` pii scope in terraform), access tables, quotas, legal holds.
  - The monetization internal route (P-11).
  - The durable notification consumer (P-12).
  - Purge changes (P-13).
  - The admin queue. The counsel-approved regimes only.
  - Enforcement dispatch on, for pilot cases.
- **Phase 6: v1.** Widen access per decision F-2, after the precision and recall criteria and every counsel item in section 15 are signed off.

### 14.3 Rollback

| Component | Rollback | Must never happen |
|---|---|---|
| Fingerprinting, lookup, relay | Flip the kill switches. Jobs stay queued. Fingerprint tables can be truncated safely, because they are derived data. | — |
| Matches surface | `COPYRIGHT_MATCH_SURFACE=off`. The API returns 404 and the page is hidden. Match rows are kept. | — |
| Cases | `COPYRIGHT_CASES_ENABLED=off` stops intake only. Open cases, timers and legal deliveries **keep running**, because statutory clocks do not pause for a rollback. | Silently stopping a restoration or forwarding deadline |
| Enforcement dispatch | Off queues commands, and cases show "enforcement pending". Reconciliation catches up when it is re-enabled. | — |
| Restrictions read path | Can be reverted **only while no `active` restriction exists**. Otherwise, fix forward. | Reverting the helper while holds exist, which would republish removed videos |
| Restriction data | Holds are released only through case transitions. A rollback **never** bulk-releases holds. | Truncating `post_restrictions` |
| Appeal capability v2 | Keep accepting v1 until v2 is stable. To roll back, re-accept v1 for base-only approvals; the restriction isolation is unaffected. | — |
| Standing contract | Revert the client to the old path only as far as `DRAFT_REQUIRE_STANDING_CHECK=false` on dev. Prod stays fail-closed. | Granting post-service admin scopes |
| Schema | Expand-only migrations stay in place. Down-migrations only for tables that hold no evidence. | Dropping case, notice, delivery, strike or audit tables |

---

## 15. Decisions still required

None of these is made. Owner: **F** = founder, **L** = counsel, **O** = operations / CI / DB owner.

| ID | Decision | Owner |
|---|---|---|
| F-1 | Approve this design and the phase order (standing and strike fixes first, then media foundations, then shadow) | Founder |
| F-2 | Launch eligibility while the creator fund is in beta: programme membership (grants nobody today), or upheld requests plus admin grants, or everyone | Founder |
| F-3 | Quota numbers (10/day, 50 open, 20 per channel, 20/day per IP) and the invalid-request threshold (3 in 90 days) | Founder |
| F-4 | Whether a block between claimant and uploader suppresses matches | Founder |
| F-5 | Age-restricted copies for an adult-verified claimant (v2) | Founder |
| F-6 | `standing-v1` thresholds; whether copyright strikes count toward suspension, after how many, over what window | Founder + counsel |
| F-7 | Interactive publish during a trust-safety outage: fail closed for all types (recommended), or fail open for low-risk types | Founder |
| F-8 | Whether visibility widening counts as publication for standing; comments and DMs out of scope | Founder |
| F-9 | Make `copyright_case` a transactional notification category that preferences cannot switch off | Founder |
| F-10 | A pre-decision respondent response window | Founder + counsel |
| F-11 | Show unlisted copies as matches (off until decided) | Founder + counsel |
| F-12 | Matching pass criteria; accept that the pipeline is not exact; radius 1 versus a dedicated index at the scale gate; whether windowed queries stay (per E3) | Founder |
| F-13 | Defer audio to phase 2 (recommended) | Founder |
| F-14 | Capacity budgets; a separate fingerprint Deployment; the backfill rate | Founder (with O-3) |
| F-15 | Legacy strikes with NULL `expires_at`: void with `legacy_cleanup`, assign an expiry, or keep them permanent | Founder |
| F-16 | New Kafka topic `media.copyright.pairs`, or a polled internal read | Founder (with O-2) |
| F-17 | Whether several cases against one upload share a strike group | Founder + counsel |
| F-18 | Author- and claimant-facing wording (banners, case pages) | Founder |
| F-19 | When to build the server-side remix compose flow | Founder |
| F-20 | Whether to enforce `MinVideoResolution` (outside this feature) | Founder |
| L-1 | Applicability: §512(c) eligibility; whether s.52(1)(c)/Rule 75 applies to UGC video hosting or complaints run under IT Act s.79 and the IT Rules; the regime-assignment rule and who decides; what `platform_policy_only` cases may do | Counsel |
| L-2 | Notice and counter-notice fields mapped to §512(c)(3)(A), §512(g)(3) and Rule 75(2); "substantially" compliant; the curable-deficiency process; e-signatures; languages | Counsel |
| L-3 | Deadlines: the internal SLA for "expeditiously"; the Rule 75 36-hour start event; 21-day counting; the US business-day calendar and restore day; review deadlines; IT Rules grievance timelines (24 h / 7 days, P-20) and 3-hour order handling, with Gazette commencement dates | Counsel |
| L-4 | Restoration policy: US automatic within the window; India discretionary at 21 days (automatic or case-by-case, and who decides) | Counsel |
| L-5 | Filed-action and court-order evidence standards, verification, re-verification cadence, release triggers | Counsel |
| L-6 | Disclosures to each party (replaces the earlier one-way "what the uploader sees"); counter-notice consent wording; the Rule 75(4) reasons notice; privacy handling of legal PII | Counsel + founder |
| L-7 | Exceptions (fair use, fair dealing, other s.52 exceptions), public-domain criteria, the agent-authority evidence standard | Counsel |
| L-8 | Repeat-infringer policy; strike duration (90 days proposed); whether counter-notice restoration or Rule 75 restoration voids the strike | Counsel + founder |
| L-9 | Misrepresentation (§512(f)) and Rule 75(6) in access revocation; whether abuse creates a strike | Counsel + founder |
| L-10 | Evidence retention period R, legal-hold triggers, what counts as legally effective receipt, the purge exception list in the privacy notice | Counsel |
| L-11 | Unlisted discovery (with F-11) | Counsel + founder |
| L-12 | Which corpus may be used for E1 (real user videos need a privacy decision) | Counsel |
| L-13 | Whether holds must become country-scoped (the schema reserves `scope`) | Counsel |
| L-14 | Whether takedown may proceed while a case's regime is `unassigned` (the design says no) | Counsel |
| O-1 | Confirm whether the worker image is currently built out of band, and where (P-17) | CI owner |
| O-2 | Provision the topic `media.copyright.pairs` and its ACLs; the ECR repo `atpost/media-worker`; the `copyright` pii scope; the `POST_RESTRICTION_HMAC_KEY`; Ed25519 keys for post-service and trust-safety; an email provider; the upload trace for B3 | Ops |
| O-3 | Where the anchor index lives (same cluster, separate schema, or separate database) | DB owner |
| O-4 | Verify CloudFront response headers on the protected behaviour (`private, max-age ≤ 300` or `no-store`) | Ops |
| O-5 | Name a trust-safety on-call and legal-queue owner for escalations and pages | Founder |
| O-6 | Register the US designated agent (3-year renewal alarm) and publish the Grievance Officer details | Legal ops (after L-1) |

**Blocked on measurement** (not decisions, but no launch without them): B1–B3, E1–E3, the 14-day readiness baseline, the legacy no-HLS count, the count of legacy strikes, the rung guarantee after the HLS change, and (phase 2) gate G1–G8.

---

## 16. v1 versus deferred

| In v1 | Deferred |
|---|---|
| Visual matching; `full_or_near_full` only; 4×16 radius-2 lookup under the scale gate | Audio (fpcalc, in-house fallback, audio confidence); `contains`, `partial` and short clips as actionable; mirroring, crops, speed, picture-in-picture, blurred fill |
| Public copies only; discovery plus canonical read gate per request | Unlisted (F-11/L-11); age-restricted for adults (F-5); realtime updates |
| Matches list, detail, counts, thumbnail, archive/unarchive with reason, removal request | Licensed-channel allowlist (a claimant's `licensed` archive covers v1); daily digest scheduling beyond one ids-only ping |
| Public legal-notice form sharing quotas; tool-access precedence; atomic quotas; duplicate index; abuse revocation | Automated remix exemption; the server-side remix compose flow (F-19) |
| Human review with checklist, senior tier, step-up; regime set by a person | Country-scoped holds (L-13); case-based `safety` restrictions |
| Case-specific holds, signed commands, reconciliation; appeal fixes | Ed25519 service token as a second factor on restriction commands |
| Strikes with case link, expiry, void, audit (subject to L-8) | Courtesy notice with a 7-day scheduled removal; uploader pre-publish check |
| Durable legal notices, sealed contacts, legal holds, retention | Dedicated MIH/ANN index (at the scale gate, F-12) |
| Counter-notice and restoration only for the regimes counsel approves | Similarity-confidence bands shown to users before E1 calibration |
| Standing contract across all publication paths | |

---

## 17. Test matrix (every row PROPOSED; none written or run)

### 17.1 Coverage of the reviewer's required matrix

| Required row | Tests |
|---|---|
| Two independent copyright holds plus an unrelated safety hold | T1-1, TM-1, T3-13 |
| Pre-existing appeal racing a copyright removal | TM-2 (variants a–d), T1-6 |
| Private / followers / scheduled / deleted / account-private / age-restricted transitions | T2-1, T2-2, T2-3, T2-4, T2-10, T1-2, T1-3 |
| Entitlement revocation while the page is open | T7-4 (with T2-2 for the thumbnail window) |
| Duplicate and reordered events, crashes and retry recovery | T1-5, T1-7, T5-3, TX-1..TX-5, T12-2..T12-5, T8-2, T8-4 |
| Stale fingerprint results after reprocessing | T11-1, T11-2, T11-3, T11-8, T15-5 |
| Hamming 3/3/2/2, one-second trims and duration-bucket boundaries | T9-1, T9-2, T9-4, T9-5 |
| Shared music, static slides, compilations and short clips | T10-3, T10-2, T10-1, T10-4, plus the E1 hard-negative strata |
| Duplicate claims and strike reversal | T5-1, T5-2, TM-3, T7-6, T7-7 |
| Failed legal-notice delivery and invalid legal-action assertions | T8-1, T8-3, T8-4, T8-5, T3-4, T3-5, T3-6, T8-8 |
| Backfill pressure while new uploads are transcoding | TB-1..TB-4, T12-3, B3 |

### 17.2 Tests by area

**Holds, appeals, reconciliation (finding 1)**
- **T1-1** Two copyright holds plus a base safety rejection plus a store-level `safety` row.
  - Each release changes only its own row.
  - The post stays ineligible until every restriction is released and the base status is approved.
  - `visibility`, `deleted_at`, `publish_at`, `published_at`, `created_at` and distribution are byte-identical after every step.
- **T1-2** Release on a scheduled post: `publish_at` is unchanged and no `PostCreated` is emitted. A hold placed before the schedule gives a published but ineligible post.
- **T1-3** Release on a deleted post: the post stays deleted. An author restore under a hold gives an undeleted but ineligible post.
- **T1-4** Capability integrity: each of these returns 403 and writes nothing:
  - a tampered `case_id`;
  - the wrong purpose;
  - the story key;
  - an expired capability;
  - `source=safety`;
  - `release_hold` with `removal_upheld`.

  A previous-key signature is accepted during rotation.
- **T1-5** Idempotency and reordering:
  - 10 concurrent sends of the same `decision_id` give 1 event, 1 count change and 9 replays;
  - the same id with a different reason gives 409;
  - a stale revision gives `409 STALE_CASE_REVISION`.
- **T1-6** Legacy approve, admin-token approve, `AdminSetReviewStatus`, and resubmit-then-ML each change only the base status under a hold.
- **T1-7** Reconciliation:
  - a dropped command is re-sent;
  - an orphan active row raises P1 and is never released;
  - count drift is repaired.
- **T1-8** Source-scan guard. Every viewer surface (home feed, reels, channel, search, end screens, media-access, ws room, cached body) excludes a restricted post.
- **TM-1** Case C1 goes `removed → withdrawn_after_removal → restored` while C2 is in `awaiting_restoration` with a base rejection and a safety row. Only C1's row changes, and reconciliation reports zero divergence.
- **TM-2** Admin rejects (D1) → the author appeals → copyright hold → overturn. The base is approved and the post stays restricted. Variants:
  - (a) an admin decision D2 mid-flight gives `superseded`;
  - (b) concurrent uphold and overturn: exactly one wins, and the post matches the appeal;
  - (c) an appeal on an approved base under a hold is refused with `COPYRIGHT_CASE_USE_COUNTER_NOTICE`;
  - (d) a lost overturn command is replayed with the same id, giving one decision row.

**Privacy and revocation (finding 2)**
- **T2-1** Follower-only copy, private copy with the claimant on the share list, unlisted copy with the claimant holding the link, and a public copy on a now-private account: each is absent from list, detail, counts and thumbnail (404).
- **T2-2** Transitions with the page open, one at a time:
  - public → followers, public → private, public → unlisted;
  - account public → private;
  - author deactivated; deletion scheduled;
  - soft delete;
  - `age_restricted` false → true;
  - review approved → rejected.

  After each commit, the next call excludes the match or returns 404. An old thumbnail URL works ≤ 5 min, and no new URL is issued.
- **T2-3** Reverse transitions: the match reappears, `notified_at` is unchanged, and no new notification is sent.
- **T2-4** A scheduled copy is never listed before publication and is discoverable after it.
- **T2-5** A graph or hidden-author outage gives 503 or no rows, never unfiltered rows.
- **T2-6** A removal submission racing a privacy flip: the verify callback returns false, the response is 404, no case is created and no quota is used.
- **T2-7** A digest whose only match went private sends nothing.
- **T2-8** Notification and realtime payloads contain no title, channel, URL, uploader id or claimant id.
- **T2-9** `Cache-Control: private, no-store` on responses. The thumbnail target expires ≤ 300 s after issue.
- **T2-10** A copy purge deletes the match rows. The case snapshot is unaffected.

**Jurisdiction (finding 3)**
- **T3-1** US window on the approved calendar across a holiday. A restore before `restore_not_before` is refused. A job still pending at `restore_not_after` pages a human and executes.
- **T3-2** A late validation does not move the clock. A calendar change produces an audited recompute.
- **T3-3** A deficient counter-notice starts nothing. A cured resubmission gets a new receipt time.
- **T3-4** A forward hard-bounces: retries, escalation, and `restore_not_after` unchanged.
- **T3-5** A bare `legal_action_taken` flag creates no hold, and restoration proceeds.
- **T3-6** Filed-action evidence rejected, or not reviewed: the case restores by `restore_not_after` (US) or reaches `restore_decision_required` (India). The review due time is never later than the deadline minus the margin.
- **T3-7** Accepted evidence gives `legal_action_hold`. A missed re-verification pages. A dismissal releases only this case.
- **T3-8** A filed-action notice after restoration does not re-remove, and opens a review.
- **T3-9** Rule 75: disabled within the 36 h placeholder; the reasons notice is served; at 21 days the case is in `restore_decision_required`; an uploader statement changes no deadline.
- **T3-10** A Rule 75 court order on day 15 cancels the expiry, and the hold follows the order's scope.
- **T3-11** A Rule 75(6) repeat complaint is flagged for a reviewer and not auto-actioned.
- **T3-12** Both parties have country=IN: the case stays `unassigned`, schedules nothing and escalates. A country change moves no deadline.
- **T3-13** A US case and a Rule 75 case on one post keep independent deadlines and holds.
- **T3-14** A court-order intake starts the 3 h timer placeholder and a page, with no counter-notice path.
- **T3-15** Repeat-infringer counting is case-linked and idempotent. Reversed notices are excluded if counsel so rules.
- **T3-16** An uploader deletes their account during `legal_action_hold`: the evidence is kept under the hold, and other data is deleted normally.

**Standing (finding 4)**
- **T4-1** Reproduce the current defect in an isolated environment. With default configuration, a composer-draft publish fails with standing unknown, and a scheduled draft stays `draft` across three ticks. (This has not been observed on dev, where `post_drafts` is empty.)
- **T4-2** Authorization:
  - a post-service token on the standing route → 200;
  - the same token on any admin route → 403;
  - an admin token without `standing.read` → 403;
  - internal key only, or a forged `X-Scopes: admin` → 401/403;
  - an expired token, the wrong audience, or a TTL over 5 min → 403.
- **T4-3** Response contract:
  - zero strikes gives `[]` and `publish_allowed: true`;
  - voided and expired strikes are excluded;
  - an unknown severity fails closed;
  - `publish_allowed` comes from the policy table.
- **T4-4** Outage:
  - interactive create returns 503 and the scheduled draft is held;
  - after recovery the draft publishes within one backoff;
  - after a simulated 24 h the draft is `blocked`;
  - a cached allow or deny is honoured within 60 s.
- **T4-5** Every publication path in section 6.4 refuses when `publish_allowed=false`. The route-inventory test fails on a new path without the check.
- **T4-6** Boot refuses without `TRUST_SAFETY_SERVICE_URL` outside dev. A helm lint asserts the URL is set.

**Strikes (finding 5)**
- **T5-1** Five concurrent approvals give one transition, one strike, two audit rows and one command. A different `decision_id` gives `409 STALE_CASE`. Two claimants give two holds and strikes per the default policy.
- **T5-2** Withdrawal voids only that case's strike and hold. A void replay is a no-op. Audit shows `strike.issued` then `strike.voided`. Standing reflects the void within 60 s.
- **T5-3** A crash before commit leaves nothing. A crash after commit results in exactly one hold after the dispatcher restarts.
- **T5-4** Expired strikes are excluded. Stats exclude voided strikes. A policy row edit is refused. A new policy applies only to new strikes.
- **T5-5** The legacy `POST /v1/strikes` with forged headers is refused. The token route without step-up or a case id is refused.
- **TM-3** Duplicate claims from the same claimant are linked, and only one can be approved. Five retried approvals give one strike. C1 withdrawn keeps C2. A stale `place_hold` after the release gets 409. Standing reflects the void within 60 s.

**Ownership and remixes (finding 6)**
- **T6-1** A client-supplied `remix_source_id` gives no exemption and no hiding. The reviewer sees "declared, unverified".
- **T6-2** The claimant's video is the later side against a third account: the case is senior tier only.
- **T6-3** The respondent's video is earlier: authority evidence is required, and the UI states order without a verdict.
- **T6-4** A Creative Commons reference: the checklist item is mandatory before `upheld`.
- **T6-5** A `licensed` archive pre-archives future matches from that channel for that claimant only.
- **T6-6** A copy lint finds no "original", "infringing", "stolen" or "owner of" about the matched parties.

**Entitlement and quotas (finding 7)**
- **T7-1** Stored eligible but fresh decision ineligible: allowed until the sweep writes ineligible, then denied within 60 s for reads and immediately for writes.
- **T7-2** A 49 h-old evaluation is denied.
- **T7-3** A paid subscriber with no programme row is denied, and `CheckEntitlement` is never called.
- **T7-4** Revocation with the page open (fund suspension or a trust-safety revocation):
  - the next list call returns 403 and the next submit returns 403;
  - the web drops its cache;
  - no thumbnail URL is issued after revocation, and old ones expire within 5 min.
- **T7-5** monetization down: 503 for programme-only users. Grant users are unaffected.
- **T7-6** 20 concurrent submissions at the limit minus 1: exactly one succeeds. The same holds across two accounts plus the anonymous form sharing a claimant hash.
- **T7-7** Concurrent duplicates give one case and a 409 with the same id.
- **T7-8** A revoked account's form submission is upheld: the grant is suppressed and the tool stays 403.
- **T7-9** Five `incomplete` submissions cause no revocation. Three invalid in 90 days revoke per policy. `rejected_bad_faith` without a second reviewer is refused.

**Legal notices, retention, holds (finding 8)**
- **T8-1** A 4 h provider outage: the same idempotency key throughout, escalation at 4 h, one ops alert, and clock anchors unchanged.
- **T8-2** A double provider accept gives one message and one `sent_at`.
- **T8-3** A restoration is due with an unsent forward: it is blocked and escalated. After a `postal_manual` record, it proceeds on the original anchor.
- **T8-4** notification-service is down: the outbox holds the event. Recovery gives one inbox row, and redelivery gives no duplicates.
- **T8-5** A suppression lookup failure retries for `copyright_case`.
- **T8-6** Inbox, realtime and push carry only the id, the link and generic copy.
- **T8-7** A reviewer without `pii.read` sees masked data. With it but without step-up: 403. Each unmasked read is audited.
- **T8-8** A claimant checkbox alone sets no hold on restoration.
- **TR-1** A respondent purge during an open case: the evidence survives with subject tokens, other rows are gone, and the ack is sent once and idempotently.
- **TR-2** A claimant purge after an upheld decision: the respondent's view keeps the decision without the claimant's id.
- **TR-3** A media hold survives an uploader purge. After an audited release, the blob is reclaimed.
- **TR-4** Past `retain_until` with no hold, the sweeper deletes and audits the count. With a hold, nothing is deleted.
- **TR-5** A hold past `review_by` raises an alert and stays active.

**Index and recall (finding 9)**
- **T9-1** A 3/3/2/2 pair: radius 0 and 1 miss; radius 2 and exhaustive hit; the recorded minimum band distance is 2.
- **T9-2** Property test: radius 2 always hits for d ∈ [0,11]. The d=12 hit rate matches the combinatorial value.
- **T9-3** 137 distinct probes per band, 548 per frame.
- **T9-4** Trims `-ss 1`, `-ss 0.4`, `-ss 2.5` and a 10% head: ≥ 2 votes in one bin, with the offset within ±0.5 s.
- **T9-5** Copies on, 1 ms below and 1 ms above a bucket boundary, and at 0.75× and 1.25×: all are found, and the bucket list covers [0.75C, 1.25C].
- **T9-6** A hot key with 10,000 postings is dropped entirely, recorded and counted. Other keys still return.
- **T9-7** Concurrent insert-then-query of A and B: the pair is found at least once, and one `pair_key` exists.

**Thresholds (finding 10)**
- **T10-1** A 6-minute reference in a 10-minute compilation is `contains`, not surfaced.
- **T10-2** 20 slides at 30 s each: weight capped at 2 s per slide; `full_or_near_full` only with diversity ≥ 3. A two-slide deck is `insufficient_evidence`.
- **T10-3** The same audio with unrelated visuals gives no candidate. The same visuals with replaced audio are still found.
- **T10-4** Identical 6 s copies are `short_clip_candidate` (shadow). A generic 6 s hard negative is not actionable.
- **T10-5** A reference followed by 5 minutes of black: flat frames weigh 0, and the class is `full_or_near_full`.
- **T10-6** The crop removes letterbox and pillarbox. Blurred fill is recorded as a known miss.
- **T10-7** A 2.5 s insert is bridged. A 4 s insert splits into two segments. Bridged frames add no weight.

**Generations (finding 11)**
- **T11-1** A reprocess during a gen-1 job: the write is `superseded`, and no postings, pairs or events are written.
- **T11-2** Gen-1 pairs are invalidated with one event each. Gen 2 produces new revisions.
- **T11-3** Lookups ignore stale-generation postings before cleanup runs.
- **T11-4** The earlier upload is scheduled for day 5 and the later one uploaded on day 3: direction follows `upload_confirmed_at`.
- **T11-5** A resumable session opened early and completed late loses precedence.
- **T11-6** Confirmations 30 s apart give `contemporaneous`. A legacy row within 24 h gives `ambiguous_legacy`.
- **T11-7** An archive persists across reprocess and reactivation, with no new notification.
- **T11-8** An old-generation completion sets no `ready_generation` and enqueues no job.

**Outbox and queues (finding 12)**
- **T12-1** Existing outbox tests pass. The constraint is intact, and the three writers and one-row readers are unchanged.
- **T12-2** A poison pair row does not block other pair rows or `media.events`.
- **T12-3** With 100,000 pending pair rows, a new `MediaTranscodeRequested` publishes within 4 s.
- **T12-4** `SKIP LOCKED` relays publish each row at most once per lease. A crash republishes the same `event_id`, and the inbox collapses it.
- **T12-5** Revisions 3, 2, invalidated 4, upsert 5: the final state is active at revision 5.
- **T12-6** Purging an asset with pairs emits one invalidation per pair.
- **TX-1** A replayed completion gives one job row.
- **TX-2** A kill after the postings commit: no duplicate postings or revisions.
- **TX-3** A relay kill after publish: the same `event_id` is applied once.
- **TX-4** Out-of-order revisions, and an invalidation before its upsert: the final state is correct.
- **TX-5** An expired lease: the first claimer's write fails on `claim_token`.

**Capacity (finding 13)**
- **T13-1** A kill switch flipped mid-job stops new claims.
- **T13-2** A transcode arriving mid-fingerprint: cancelled within 2 s, with the checkpoint kept.
- **T13-3** A 12-hour input stays within the RSS budget, and its deadline scales with duration.
- **T13-4** The circuit breaker trips when pending age exceeds 120 s.
- **TB-1** 10,000 backfill jobs plus uploads at 2× peak: the readiness p95 stays within budget, backfill claims drop to 0 while the worker is busy, and `media.events` is not delayed.
- **TB-2** A transcode during a backfill job on the same pod: cancelled, checkpoint kept, resumed later.
- **TB-3** DB reader lag above 30 s: backfill and new-upload fingerprinting pause, and transcodes are unaffected.
- **TB-4** Backfill kill switch off: backfill stops within one iteration, and new-upload jobs continue.

**Worker image (finding 14)**
- **T14-1** `docker buildx build --platform linux/arm64 -f services/media-service/Dockerfile.worker Architecture` succeeds.
- **T14-2** `/worker` with no configuration exits with a configuration error (not an exec-format or missing-symbol error). `ffmpeg -version` succeeds.
- **T14-3** A CI dry run pushes both `atpost/media-service:<sha>` and `atpost/media-worker:<sha>`, and bumps each tag only for an image actually pushed.
- **T14-4** Removing `transcode_lease.go` from the context fails the build.

**Input contract (finding 15)**
- **T15-1** A 240p source reaches ready with no MP4, and the contract picks `hls_rung/360p`.
- **T15-2** A fault-injected 360p MP4 failure still reaches ready, and the contract picks HLS. Re-run against the repackaging pipeline, where the 360p rung must come from the re-encode fallback.
- **T15-3** A legacy asset with no HLS picks the smallest MP4. With none, it streams the original. Over the cap, it records `skipped/original_too_large`.
- **T15-4** No presigned URLs: only internal `GetObject` calls, and no URLs in logs.
- **T15-5** A reprocess during the segment read: `input_inconsistent`, or the fence rejects the result.
- **T15-6** A reel (720p cap) and a 4K source both yield a 360p rung. Same-content hashes are within τ for ≥ 95% of frames.
- **G1–G8** The fpcalc image gate (section 12.6). Phase 2 only.

**Evaluations and benchmarks:** E1, E2, E3 (section 12.5) and B1, B2, B3 (section 13). All PROPOSED.
