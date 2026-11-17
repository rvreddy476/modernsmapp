# Verifier v2: notifications, visibility, tests (long video)

Date: 2026-11-17. Read-only. I read only each finding's Title and Evidence, then checked the code myself and looked for what would make it false. Web tree: `C:\workspace\wt\pb-echo` (read-only). Paths below are under `Architecture/services/` unless they say otherwise.

Test run: `go test ./internal/delivery -run TestHLSSegmentTTL -count=1` in media-service passed (`ok ... 4.163s`). That confirms the 12 h segment TTL is pinned by a test.

## LV-A-1: subscribers never notified of an ordinary long-video upload

- **Verdict:** CONFIRMED. **Evidence level:** VERIFIED-READ.
- **What I checked, and what could have made it false:**
  - Clients create the post before the transcode finishes. Web `src/features/upload/useUploadStudio.ts:557-567` uploads, then calls `createReel` at `:611-622`. Its comment says "the post becomes visible to viewers once renditions are ready". Android `feature/post/.../createhub/ReelPublishPipeline.kt:44-52` says in its own words "NO WAIT FOR THE TRANSCODE". Neither client creates the post only after processing.
  - post-service emits `PostCreated` at create time for any immediate, unheld post (`post-service/internal/service/copyright_title_review.go:329-338`, `createdEventFor`).
  - The fan-out reads `GET /v1/posts/:id` with only `X-Internal-Service-Key` and no `X-User-Id` (`notification-service/internal/service/fanout_eligibility.go:181,281-284`). The handler grants the internal key no bypass: `viewerUUID` stays nil (`post-service/internal/http/post_reads_handler.go:52-70`). `GetPost` returns nil (404) for a pending review status (`service/post_reads.go:32-40`) and for a processing post (`:45-50`; `processing.go:90-95`).
  - A 404 sets `exists=false` (`fanout_eligibility.go:187-188`). `!exists || !approved` returns false (`:82-84`). The worker then calls `MarkDelivered` on each recipient as a terminal skip (`notification-service/internal/service/subscriber_fanout.go:387-393`). This also blocks any later re-announce of the same post id.
  - The job claim has no delay or not-before: `status='pending'` only (`store/postgres/fanout_jobs.go:118-130`).
  - Re-announce: the only `enqueueSubscriberFanout` caller is `case events.PostCreated` (`notification-service/internal/events/consumer.go:267-276`). post-service writes `PostCreated` only at create, at the schedule flip, at a copyright-title clear and for a live VOD. The ready flip (`post-service/internal/consumers/media.go:353 finalizeReviewGate`) does not write one. No `PostReady`, `PostPublished` or `MediaReady` handler exists in notification-service.
- **Severity:** P1 (agree). Subscribe-and-notify is the core retention loop of a long-video product, and it fails for nearly every immediate upload. No data is exposed, so it is not P0.
- Side observation (SUSPECTED): for an 18+ post, the nil-viewer read hits `checkAgeGate` (`post_reads.go:62`). That returns 401/403, which the fan-out treats as an error to retry (`fanout_eligibility.go:203`), not as a skip. Not traced further.

## LV-C-2: scheduled long video published while still transcoding

- **Verdict:** CONFIRMED. **Evidence level:** VERIFIED-READ.
- `ListDueScheduledPosts` selects on `publish_at`, `deleted_at`, not-note and no publish block only (`post-service/internal/store/postgres/scheduled.go:49-55`). The flip UPDATE does not look at media or review state (`:130-140`). It writes `PostCreated` in the same transaction (`:155-163`). The service path (`service/schedule.go:384-490`) checks standing and the copyright hold, and nothing about readiness. Only the author's "publish now" checks `UploadStatus != "ready"` (`service/post_video_tools.go:186-208`).
- The fan-out then hits the same terminal skip as LV-A-1.
- What would make it false: the transcode finishing inside the schedule lead time (5 minutes at least). That holds for short long videos, so the bug is conditional. The flip also sets `created_at = now` (`scheduled.go:131-135`).
- **Severity:** P2 (agree). Narrower than LV-A-1 (only scheduled uploads whose transcode outlasts the lead time), and it is fixed by the same re-announce.

## LV-B-2: segment URLs valid up to 12 h after delete, private, block or lapse

- **Verdict:** CONFIRMED. **Evidence level:** VERIFIED-RUN (the TTL test runs and passes) plus VERIFIED-READ.
- `MaxHLSSegmentTTL = 12h`; the TTL is 2x the duration plus 15 min (`media-service/internal/delivery/sign_cache.go:60-90`). `HLSURLsForAsset` signs every `.ts` under `/hls/` with that TTL after one authorisation (`delivery/authz.go:628-670`). The service's own comment says "A signed URL cannot be revoked before it expires, so the TTL IS the revocation window" (`delivery/signer.go:66-71`).
- What could have made it false, and why it does not:
  - CloudFront invalidation: no `CreateInvalidation` anywhere in the Architecture Go, Terraform or YAML.
  - A prod TTL override: no env var exists for the segment TTL; the only matches are code and tests.
  - Key rotation or a prefix move on a visibility change: video keys are under `user/...`, which is the protected class (`signer.go:80-85`). No copy or move happens on a visibility change.
  - A revocation list: the only revocation code is gateway session revocation, which does not cover media URLs.
- **Severity:** raise to **P1**. The harness journey "switch a public video to private; an open player and a copied stream URL must stop" fails, and track A section 2 journey 3 records that. For a 3 h film the window is 6 h 15 min. It also covers legal or copyright takedowns and paid-membership lapse, the reels equivalent is 5 min (`MaxProtectedTTL`), and the server fix is small. It is not P0: only someone who was already authorised (or anyone they share the link with) keeps access.
- Caveat for the fix: even a 30-minute cap does not make an open player stop at once. The journey needs a stated bounded window, plus a web hls.js reload on 403. Android has `HlsRefresh`; I did not verify the web side.

## LV-B-3: cover-frame stores any thumbnail_url

- **Verdict:** CONFIRMED. **Evidence level:** VERIFIED-READ.
- The handler binds `ThumbnailURL *string` with no validation (`post-service/internal/http/video_tools_handler.go:195-215`). `SetCoverFrame` checks only the owner. `verifyCoverOnWrite` runs only for `cover_media_id`, and `thumbnail_url` is written verbatim (`service/post_video_tools.go:135-172`).
- It is served as given: `dto/video.go:67,87`, and the related or end-screen cards at `service/hub_detail.go:193-194`.
- On web, `resolveImageUrl` returns any non-`/serve` URL unchanged (`src/lib/imageUrl.ts:74-83`). pb-echo sets no CSP `img-src` (no `Content-Security-Policy` in the tree; `next.config.ts` has only `remotePatterns`, which applies to `next/image`, not to a plain `<img>`). So nothing blocks the external load.
- The web client itself can send it: `setCoverFrame` takes `thumbnail_url` (`posttubeApi.ts:599-604`), although the studio never passes it.
- **Severity:** P2 (agree). It needs a creator account and affects only the creator's own videos' cards, but it carries unmoderated imagery and works as a tracking pixel. Dropping the field closes it.

## LV-F-1: no end-to-end long-video upload, transcode, publish and play test

- **Verdict:** CONFIRMED. **Evidence level:** VERIFIED-READ (repository search).
- `Architecture/tools/integration` matches `long_video` only in the creator-fund and settlement tests. It has no `/v1/videos`, `m3u8` or `media.events.long`.
- The e2e media domain runs only `api-gateway media-service`, with no worker (`.github/workflows/e2e.yml:57-60`). Workflows have no `long_video`.
- Smoke: `health.longvideo` is one trending GET (`scripts/smoke/dev-smoke.mjs:429-431`). The `video.post` step explicitly creates a `content_type: 'flick'` "so the long-video guards ... do not apply" (around `:651-665`).
- No Android `androidTest` and no pb-echo e2e file references long video.
- **Severity:** P2 (agree). It is a coverage gap, not a defect, but it is why LV-A-1 and the caption bug below went unseen.

## LV-F-2: no test that going private stops an open player or copied URL

- **Verdict:** CONFIRMED. **Evidence level:** VERIFIED-RUN.
- `TestHLSSegmentTTL` (`media-service/internal/delivery/sign_cache_test.go:127-147`) asserts 8 h 15 min for a 4 h video and the 12 h cap, and it passes. The gate tests use `fakeAuthz{allow:...}` (`delivery/authz_test.go:97-104`). I found no test that changes visibility and then fetches a segment.
- **Severity:** P2. It stands with LV-B-2; the acceptance test belongs to that fix.

## LV-D-12: no load test for long video

- **Verdict:** CONFIRMED. **Evidence level:** VERIFIED-RUN (search).
- The only load tests are `dating-service/loadtest` and `rider-service/loadtest` (and copies of them under `.claude/worktrees`).
- No `func Benchmark` exists in any service under `Architecture/services`. No workflow mentions k6 or a load test.
- **Severity:** P2 (agree). Capacity is unproven, but that is a launch-gate item, not a defect.

## LV-A-5: metadata edits never re-index search

- **Verdict:** CONFIRMED. **Evidence level:** VERIFIED-READ.
- The edit records `title`, `text`, `tags`, `hashtags` and `category` in its changes (`post-service/internal/store/postgres/post_edit.go:384-400`). It emits the search event only on a visibility change, `creditSearchChanged` (credits, genres, release_year, production_house) or a tier change (`:253-266,473-480`).
- No other emitter is tied to edits: every `BumpSearchRevAndEmitTx` caller is a lifecycle, copyright, restriction or crosspost path.
- search-service has no post-updated case (`search-service/internal/events/consumer.go:440-1020`).
- Results take the title from the document `_source`; hydration adds only authors and avatars (`search-service/internal/http/results.go:16,86`).
- Reindexing is the manual `cmd/backfill`; I found no periodic reconcile.
- **Severity:** P2 (agree). Results are stale, not wrong data and not a security issue.

## LV-A-6: videos over 4 h refused only after the full upload

- **Verdict:** CONFIRMED. **Evidence level:** VERIFIED-READ.
- `DefaultMaxVideoDurationSeconds = 4*60*60` (`media-service/internal/processing/limits.go:52-57`), enforced at probe (`:92-95`), with the env override at `cmd/worker/transcode_guards.go:68`. No config sets `MEDIA_MAX_VIDEO_DURATION_SEC`.
- Resumable init checks bytes only (`internal/http/resumable_handler.go:45-60`).
- The web studio even tells long-video uploaders "Any length" (`src/features/upload/fileRules.ts:23`). Android has no 4 h check.
- **Severity:** P2. A rare case, but a 16 GiB upload is wasted and the founder's no-length-cap rule is broken. P3 would also be defensible if the founder accepts 4 h and the copy changes.

## Founder questions

1. **Is the web caption upload always failing?** Yes, CONFIRMED (VERIFIED-READ).
   - `createSubtitleTrack` always builds a `data:text/vtt` URL and sends it as `content_url`, and never sends `content` (`pb-echo src/features/posttube/data/posttubeApi.ts:615-630`).
   - The server answers 400 `CONTENT_URL_NOT_ACCEPTED` whenever `content_url` is non-empty (`media-service/internal/http/clips_handler.go:426-431`). The route is `POST /v1/subtitles/:mediaId` (`:68`). No proxy rewrites the body.
   - It is the only web caption-upload path: it is used by the upload studio (`useUploadStudio.ts:412,589`) and by Creator Hub (`hubApi.ts:1178-1180`).
   - In the studio the failure is swallowed (`catch { /* non-fatal */ }`, `useUploadStudio.ts:586-592`), so creators get no error and no captions. Suggested P1 for web: a creator journey is silently broken, though the fix is a one-line client change (send `content: vttContent`).
2. **Does the web set 18+ in a separate call after publish?** Yes, CONFIRMED (VERIFIED-READ).
   - post-service takes `age_restricted` only on the edit route (`post-service/internal/http/post_edit.go:89,311`). The create route has no such field.
   - The studio sends it afterwards as `updatePost(postId, {age_restricted:true})` in `onSuccess` (`useUploadStudio.ts:664-674`; `studioApi.ts:159-168`). A failure only adds the warning "The 18+ age restriction could not be saved. Turn it on in Creator Hub." The post stays published and ungated.
   - What limits it: the PATCH runs within seconds of create, while the post is usually still processing and so hidden from everyone but the author. The exposure is real only when the PATCH fails, or when processing finishes first (a short clip). Suggested P1: content the creator flagged as adult can reach under-18 viewers with only a soft warning. The fix is to take `age_restricted` on create (or on the draft) and fail closed.

## Summary

| ID | Verdict | Evidence | Severity (audit → mine) | Why |
|---|---|---|---|---|
| LV-A-1 | CONFIRMED | VERIFIED-READ | P1 → P1 | Core subscribe-notify loop broken for immediate uploads; terminal skip, no re-announce |
| LV-C-2 | CONFIRMED | VERIFIED-READ | P2 → P2 | Only scheduled uploads whose transcode outlasts lead time; same fix as A-1 |
| LV-B-2 | CONFIRMED | VERIFIED-RUN | P2 → **P1** | Harness privacy/takedown journey fails; up to 12 h, no invalidation or rotation; small server fix |
| LV-B-3 | CONFIRMED | VERIFIED-READ | P2 → P2 | Creator-only, own videos; unmoderated image and tracking pixel, no CSP |
| LV-F-1 | CONFIRMED | VERIFIED-READ | P2 → P2 | No long-video e2e anywhere; smoke uses a flick |
| LV-F-2 | CONFIRMED | VERIFIED-RUN | P2 → P2 | TTL test pins the gap; acceptance belongs to B-2 |
| LV-D-12 | CONFIRMED | VERIFIED-RUN | P2 → P2 | No load test or benchmark for any long-video path |
| LV-A-5 | CONFIRMED | VERIFIED-READ | P2 → P2 | Title, text, tags and category edits emit no search event; stale results |
| LV-A-6 | CONFIRMED | VERIFIED-READ | P2 → P2 | 4 h cap at probe only; web says "Any length" |
| Web captions | CONFIRMED | VERIFIED-READ | new, P1 (web) | Always 400, silently swallowed in the studio |
| Web 18+ after publish | CONFIRMED | VERIFIED-READ | new, P1 | Separate PATCH; on failure the video is published ungated |
