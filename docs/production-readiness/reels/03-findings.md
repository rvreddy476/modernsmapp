# Reels: findings

- **Audit date:** 2026-11-17. Code: `codex/module-01-02-launch-safety` @ `58f602b6d` (dirty working tree, read as it is). Web: `C:\workspace\wt\pb-echo`, `feat/echo-web` @ `ca96921`.
- **Sources:** six Stage 2 tracks (`tracks/track-a…f`), four Stage 3 verifiers (`verify/v1-pipeline.md`, `v2-security-perf.md`, `v3-tests-ci.md`, `v4-gapfill-pass.md`) and an independent re-check of V4-1 and V4-7 by the lead. Where a verifier disagreed with a track, the verifier's verdict, severity and evidence level are used.
- **Nothing was executed against a running stack.** No database, server, device, browser or deployed environment was used (repository rules forbid it). VERIFIED-RUN means a unit test, a CI log or a repository command.
- **IDs:** `reels-N` is the final ID. Each finding has a `Was:` line listing the track or verifier IDs it came from (A = functional, B = security, C = data, D = performance, E = reliability and deployment, F = tests, V4 = gap-fill verifier). 119 original IDs were merged down to 105 findings.
- **Paths:** backend paths are relative to `Architecture/services/` unless they start with another top-level folder (`infra/`, `deploy/`, `charts/`, `identity-platform/`, `.github/`). Android paths are relative to `mobile/android/`, where `…/` stands for the module's `src/main/kotlin/com/us/android/` root. Web paths are relative to `C:\workspace\wt\pb-echo`.

## Counts

| Severity | Count | VERIFIED-RUN | VERIFIED-READ | SUSPECTED | UNVERIFIED |
|---|---|---|---|---|---|
| P0 | 0 | 0 | 0 | 0 | 0 |
| P1 | 14 | 6 | 7 | 1 | 0 |
| P2 | 56 | 3 | 45 | 8 | 0 |
| P3 | 35 | 0 | 31 | 4 | 0 |
| **Total** | **105** | **9** | **83** | **13** | **0** |

The evidence level counted is the finding's primary level. Several findings carry a second level for one part (for example the mechanism is read and the timing under load is suspected); the field in each finding says which part is which.

**Conditional P0:** reels-2 becomes P0 for the web if the browser check shows the progressive fallback is also blocked by CORS (reels would then not play on production web at all).

## Merges and verifier changes

| Final | Was | Change |
|---|---|---|
| reels-1 | A-2, C-1, C-4 | One root cause, three entry points (v1). A-2's "manual_review has no resolver" clause dropped (v1: unreachable for a ready asset). Lookup errors also swallowed (v1, V4) |
| reels-2 | D-1 | P1 SUSPECTED; fallback goes browser → gateway → CloudFront in prod, not through Next, and is probably blocked too (v2) |
| reels-3 | C-3, reels-E-1 | Duplicates (v1) |
| reels-4 | D-3, reels-E-13 | Duplicates; E-1 makes it worse (v2) |
| reels-5 | reels-E-2, C-7, V4-16 | Same gap (no worker metrics, quarantine invisible); default crash alerts exist but route to the `null` receiver (v2) |
| reels-6 | REELS-B-1 | Worse: the EKS `vpc-cni` addon enforces no NetworkPolicy, so enabling the chart's policy alone does nothing (v2) |
| reels-7 | V4-1 | New in V4; re-checked and confirmed by the lead |
| reels-8 | REELS-B-2 | Two dead erasure paths exist; re-point one (v2) |
| reels-9 | reels-F-11 | Reworded: media and post Go tests ran green at HEAD~1; only feed-service unit tests are stale (v3) |
| reels-15 | REELS-B-13, V4-7 | B-13 (P3) widened by V4-7 to P2; re-checked by the lead |
| reels-20 to -27, -29, -30, -41 to -44 | V4-2 … V4-17 | New in V4 |
| reels-28 | REELS-B-5, C-11, V4-P3-4 | Same inflation; intra-day part VERIFIED-READ, persistence after rollup not supported |
| reels-34 | reels-E-4 | P1 → P2 (v1) |
| reels-35 | reels-E-3 | P1 → P2 SUSPECTED; P1 if `terraform plan` shows a 20 GiB root (v1) |
| reels-40 | A-7 | SUSPECTED → VERIFIED-READ (V4 confirmed the fall-through; draft publish carries no Idempotency-Key) |
| reels-46 | A-3, D-6 | Duplicates (no web preload) |
| reels-51 | D-11, V4-10 | Telemetry-on-unload clause is the same defect |
| reels-64 | reels-F-2, reels-F-13 | P1 → P2 (v1); F-13 is a case of the same table test |
| reels-71 | A-1, C-2 | Both DISPROVED as publication bugs (v1); P1 → P3 residual |
| reels-76 | REELS-B-9, V4-P3-9 | Same proxy header-relay issue |
| reels-99 | D-2 | P1 → P3 (v2: prod web build bypasses the proxy; `deploy/web/app/values-prod.yaml:8` contradicts `build-web.yml`) |

## Index

| ID | Sev | Evidence | Title |
|---|---|---|---|
| reels-1 | P1 | VERIFIED-READ | A reel can stay 'pending' for ever, visible only to its author, with no retry, sweeper or message |
| reels-2 | P1 | SUSPECTED | Web HLS sends credentials to a CloudFront policy that refuses them; production web playback likely fails |
| reels-3 | P1 | VERIFIED-READ | `media.events.long` is not provisioned, so long videos block the reel transcode queue in prod |
| reels-4 | P1 | VERIFIED-READ | The transcode fleet runs two short jobs at once platform-wide, no autoscaling, no back-pressure |
| reels-5 | P1 | VERIFIED-READ | The transcode pipeline is unmonitored: worker metrics unscraped, no alert reaches a person, quarantine invisible |
| reels-6 | P1 | VERIFIED-READ | media-service trusts a bare X-User-Id on reads and no network policy can be enforced |
| reels-7 | P1 | VERIFIED-READ | Anyone, signed out included, can read view and watch-depth buckets of any content id |
| reels-8 | P1 | VERIFIED-READ | Account deletion never erases analytics; auth-service reports the purge complete |
| reels-9 | P1 | VERIFIED-RUN | Android and web reels tests run in no CI at head; feed-service unit tests last ran 31 Oct |
| reels-10 | P1 | VERIFIED-RUN | No test uploads a video, transcodes, publishes and plays it on a second account |
| reels-11 | P1 | VERIFIED-RUN | No load test exists for any reels endpoint, the transcode pipeline or the playlist route |
| reels-12 | P1 | VERIFIED-RUN | Every real-ffmpeg test skips without ffmpeg and no CI job installs it |
| reels-13 | P1 | VERIFIED-RUN | The server half of resumable upload has no test |
| reels-14 | P1 | VERIFIED-RUN | No test that one reel player is active, the pool is bounded, or audio never overlaps |
| reels-15 | P2 | VERIFIED-READ | media-service accepts HS256 tokens in prod; no exp means no expiry; no iss/aud check |
| reels-16 | P2 | VERIFIED-READ | User-supplied audio reaches ffprobe/ffmpeg through the un-hardened runner |
| reels-17 | P2 | VERIFIED-READ | Deleted or privatised reels keep playing from issued URLs for up to 35 min; deleted objects kept 90 days |
| reels-18 | P2 | VERIFIED-READ | Like, reaction and comment routes skip the review, processing and schedule gates |
| reels-19 | P2 | VERIFIED-READ | Trending hashtags count tags from private, pending, rejected and deleted reels |
| reels-20 | P2 | VERIFIED-READ | Android's streaming video cache survives sign-out |
| reels-21 | P2 | VERIFIED-READ | The web app sets no CSP, frame-ancestors, nosniff, Referrer-Policy or HSTS |
| reels-22 | P2 | SUSPECTED | The web /v1 proxy may let dot segments escape /v1/ on the gateway |
| reels-23 | P2 | SUSPECTED | The web /v1 proxy buffers every request body whole, before authentication |
| reels-24 | P2 | VERIFIED-READ | media-service has no request rate limit; owner ffmpeg routes pull the whole original into the API pod |
| reels-25 | P2 | VERIFIED-READ | Destructive media internal routes accept the one shared internal key |
| reels-26 | P2 | SUSPECTED | The devbox compose stack falls back to secrets published in this public repo |
| reels-27 | P2 | VERIFIED-READ | Watch time is client-controlled and feeds Creator Fund eligibility |
| reels-28 | P2 | VERIFIED-READ | One account can inflate a reel's displayed view count for the day with fresh session ids |
| reels-29 | P2 | VERIFIED-READ | Resolving a report has no effect on the reported reel |
| reels-30 | P2 | VERIFIED-READ | "New upload" notifications are dropped for reels pending at create and for followers-only uploads |
| reels-31 | P2 | VERIFIED-READ | A transcode failure rejects the post for good; a later successful reprocess never reopens it |
| reels-32 | P2 | VERIFIED-READ | Confirm marks a video 'uploaded' and queues its transcode in two commits |
| reels-33 | P2 | VERIFIED-READ | Sound use counts only go up and can be inflated by one account |
| reels-34 | P2 | VERIFIED-READ | A Rekognition outage re-encodes each video up to 32 times and ends with the upload failed |
| reels-35 | P2 | SUSPECTED | The worker's 50 Gi scratch may sit on a ~20 GiB node disk |
| reels-36 | P2 | VERIFIED-READ | Android fallback camera has no 10-minute cap |
| reels-37 | P2 | VERIFIED-READ | Android cannot save, resume or publish a reel draft |
| reels-38 | P2 | VERIFIED-READ | Android reels neither set nor show the AI-generated label |
| reels-39 | P2 | VERIFIED-READ | Web uploads cannot resume after a reload or closed tab |
| reels-40 | P2 | VERIFIED-READ | Web draft publish falls through to a direct create on an unknown outcome |
| reels-41 | P2 | VERIFIED-READ | Android reels never send end_reason=ended; Auto scroll records a phantom loop |
| reels-42 | P2 | VERIFIED-READ | Web background tab keeps sending heartbeats after play_end |
| reels-43 | P2 | SUSPECTED | Web loop_count is always 0 and end_reason never "ended" |
| reels-44 | P2 | VERIFIED-READ | Web Follow badge is wrong past about 100 distinct authors |
| reels-45 | P2 | VERIFIED-READ | Portrait reels are delivered at about 404x720; master declares 1280x720 |
| reels-46 | P2 | VERIFIED-READ | Web reel start-up is slow: no preload, 6 s segments, three sequential requests |
| reels-47 | P2 | VERIFIED-READ | Each playlist request costs a Postgres read, an S3 GET and a post-service call |
| reels-48 | P2 | SUSPECTED | Android media cache keyed on the full signed URL; the cache does little for HLS |
| reels-49 | P2 | VERIFIED-READ | Android preload ignores data saver; Auto quality removes the cap |
| reels-50 | P2 | VERIFIED-READ | Playback failure is silent on Android and loops on web |
| reels-51 | P2 | VERIFIED-READ | Web reels stage fires 4-6 requests per swipe; telemetry unbatched and lost on unload |
| reels-52 | P2 | VERIFIED-READ | Analytics ingest: 3 statements per event, unbounded receipts, one DEFAULT partition |
| reels-53 | P2 | VERIFIED-READ | Reels feed filters content type in Go: up to 48 Scylla reads per page, 15 hydrations per chip |
| reels-54 | P2 | SUSPECTED | `/v1/posts/trending` scans a week of posts per request with no cache or index |
| reels-55 | P2 | VERIFIED-READ | Resumable parts travel through gateway and media-service, not presigned to S3 |
| reels-56 | P2 | VERIFIED-READ | Worker kills in-flight ffmpeg on SIGTERM; every rollout restarts all transcodes |
| reels-57 | P2 | SUSPECTED | Nothing restarts or reports a hung worker |
| reels-58 | P2 | VERIFIED-READ | No procedure restores the media database and bucket together |
| reels-59 | P2 | VERIFIED-READ | A reel cannot be traced from upload through worker to the review gate |
| reels-60 | P2 | VERIFIED-READ | media-service shutdown cuts chunk uploads; background workers never cancelled |
| reels-61 | P2 | VERIFIED-READ | All reel playback answers 503 when post-service is down |
| reels-62 | P2 | VERIFIED-RUN | The chart's QA guard fails on media-service today |
| reels-63 | P2 | VERIFIED-READ | ffmpeg is unpinned on end-of-life Alpine 3.19; no image scan |
| reels-64 | P2 | VERIFIED-RUN | The consumer that releases a reel from review has no behaviour test |
| reels-65 | P2 | VERIFIED-READ | The worker's `processMessage` orchestration is called by no test |
| reels-66 | P2 | VERIFIED-READ | No test sends a real bad video through confirm and the worker |
| reels-67 | P2 | VERIFIED-READ | No test that a privatised or deleted reel stops playing from a fetched playlist |
| reels-68 | P2 | VERIFIED-READ | No test follows a report to a decision and checks the reel is gone |
| reels-69 | P2 | VERIFIED-READ | The worker's Kafka durability test never runs in CI |
| reels-70 | P2 | VERIFIED-RUN | Integration CI logs cannot tell a skipped test from a passing one |
| reels-71 | P3 | VERIFIED-READ | The create-time video gate is one layer instead of two; dead manual_review branch in the worker |
| reels-72 | P3 | VERIFIED-READ | The author can clear the AI and altered labels after publish |
| reels-73 | P3 | VERIFIED-READ | Profile counts include private, pending and scheduled reels for any viewer |
| reels-74 | P3 | VERIFIED-READ | Mention notifications go out while the reel is pending and stay after takedown |
| reels-75 | P3 | VERIFIED-READ | Neither client tells the creator about a moderation hold or rejection |
| reels-76 | P3 | VERIFIED-READ | Web proxy forwards X-User-Id, follows redirects anywhere, relays their Set-Cookie |
| reels-77 | P3 | VERIFIED-READ | The media cookie reaches state-changing /v1/media routes |
| reels-78 | P3 | VERIFIED-READ | Many 500s return raw Go error text |
| reels-79 | P3 | VERIFIED-READ | `/v1/reels/slug/:slug` answers the id of any reel with no visibility check |
| reels-80 | P3 | VERIFIED-READ | Report errors are generic; `details` is unbounded |
| reels-81 | P3 | VERIFIED-READ | Latent config switches can silently disable the scanner, view cap or internal key |
| reels-82 | P3 | SUSPECTED | Web exit animation can overlap two reels' audio for 0.22 s |
| reels-83 | P3 | VERIFIED-READ | Android cannot add captions to a reel |
| reels-84 | P3 | SUSPECTED | Android discard after confirm leaves the uploaded media in storage |
| reels-85 | P3 | VERIFIED-READ | Android offline copy survives deleting the reel |
| reels-86 | P3 | VERIFIED-READ | Android owner has no creator comment menu on the pinned top reel |
| reels-87 | P3 | VERIFIED-READ | Android leaves the previous view open on a reel with no playback |
| reels-88 | P3 | VERIFIED-READ | Web engagement fails silently and races the live poll |
| reels-89 | P3 | VERIFIED-READ | Web hashtag Reels tab asks for `flick` only |
| reels-90 | P3 | VERIFIED-READ | Web Hub edit, delete and publish leave the reels caches stale |
| reels-91 | P3 | VERIFIED-READ | Web reels list has no cross-page de-dup or `maxPages` |
| reels-92 | P3 | VERIFIED-READ | Deleting an asset drops its resumable rows without aborting the multipart upload |
| reels-93 | P3 | VERIFIED-READ | A late chunk can move a completed session back to 'uploading' |
| reels-94 | P3 | VERIFIED-READ | Migration 008's moderation CHECK is a no-op that hides the value set |
| reels-95 | P3 | VERIFIED-READ | `media.copyright.pairs` has no consumer |
| reels-96 | P3 | VERIFIED-READ | Transcode retry window is about 4 minutes, not the 15 its comment says |
| reels-97 | P3 | VERIFIED-READ | Cleanup of an asset deleted mid-transcode writes no reclaim row |
| reels-98 | P3 | VERIFIED-READ | Scheduled posts retry for ever on a persistent error; parked items not notified |
| reels-99 | P3 | VERIFIED-READ | The web proxy would stream media and buffer uploads if the API base were empty |
| reels-100 | P3 | VERIFIED-READ | Android reel page recomposes 4x a second; a page append un-pauses a paused reel |
| reels-101 | P3 | VERIFIED-READ | Web processing poll has no limit and swallows errors |
| reels-102 | P3 | VERIFIED-READ | `/v1/clips` is unreachable; `/v1/series` is used by no client |
| reels-103 | P3 | SUSPECTED | The media-service PDB also selects worker pods |
| reels-104 | P3 | SUSPECTED | Every media-service replica runs the outbox relay without a claim |
| reels-105 | P3 | VERIFIED-READ | The web studio's processing-failed branches have no test |

---

## P1

### reels-1

- **ID:** reels-1
- **Title:** A reel can stay in `review_status='pending'` for ever, visible only to its author, with no retry, sweeper or message: create holds it pending on any media-service error or race, and the transcode consumer drops review-gate errors and commits the offset.
- **Severity:** P1
- **Category:** data
- **Evidence level:** VERIFIED-READ (v1 confirmed all three entry points; V4 widened the lookup case)
- **Evidence:**
  - Create: `post-service/internal/service/post_create.go:494-496` calls `gateVideoReviewStatus` before the post exists; `post_review.go:161-166` answers "pending" on any media-service error (3 s client, :177-208).
  - Race (v1): the `video_metadata` row is inserted after the post transaction commits (`post_create.go:614-666`), so a completion consumed in between finds no row; a failed `CreateVideoMetadata` is log-only (:663-665). The web creates only after `/renditions` reports `all_ready`, so the completion has normally already been consumed.
  - Consumer: `post-service/internal/consumers/media.go:353-398` `finalizeReviewGate` has no error return; a flip error (:371-376) and the audit insert (:384-392) are logged only; any `GetVideoMetadataByMediaAsset` error is read as "no post" (:115-130,366-369); `handle` returns nil (:240), so the offset is committed. Other errors get 3 retries over about 7 s, then `media.events.dlq`, which has no replayer.
  - No recovery: the only writers from pending are the consumer, `FlipReviewStatusFromPending` (`store/postgres/posts.go:1540-1550`) and admin `ModeratePost` (`moderation_authority.go:137-148`), whose queue lists `flagged` only (`admin_content.go:164`). No reconciler reads review status (grep).
- **How to reproduce:** (a) Wait for a media asset to be ready+passed, make media-service answer 503 once to `GET /v1/media/:id`, then `POST /v1/posts` with `content_type=flick`: the post is stored pending and never changes. (b) Fail the posts UPDATE once (lock timeout, failover) while a completion is consumed: the event is committed and the post stays pending.
- **Impact:** A normal reel never reaches anyone. The author sees it on their own profile and assumes it is live. No error, no alert, and support cannot see why.
- **Fix:** (1) Return errors from `finalizeReviewGate` (the flip is conditional, so a retry is safe) and treat only `pgx.ErrNoRows` as "no post"; let the consumer retry until the store recovers, with only undecodable payloads permanent. (2) After the insert, if the post is pending and its media is a video, re-run the decision from post-service's own read of `media_assets`. (3) A periodic reconciler: video posts pending more than 5 minutes whose media is ready → the same rule.
- **Acceptance:** Consumer test with a store whose flip fails once: the first delivery returns an error and the post is approved after the second (today the first delivery returns nil). Reconciler test: pending post + ready/passed media → approved after one tick.
- **Effort:** M
- **Was:** A-2, C-1, C-4 (lookup widening from V4 §1.3)

### reels-2

- **ID:** reels-2
- **Title:** The web player sends every HLS request with credentials, while the CloudFront response-headers policy answers CORS with `allow_credentials=false` and overrides the origin, so in production every segment fetch is expected to be blocked; the progressive fallback is also a credentialed request redirected to the same CDN and probably fails too.
- **Severity:** P1 for web (P0 for web if the fallback is confirmed blocked). Android is not affected.
- **Category:** functional / performance
- **Evidence level:** SUSPECTED (the static chain is VERIFIED-READ by Track D and v2; the browser outcome was not executed)
- **Evidence:**
  - `src/features/reels/components/ReelVideo.tsx:348-354`: `xhrSetup: xhr.withCredentials = true` on every hls.js request (hls.js `^1.7.3`, default XhrLoader for playlists and fragments). The same pattern is in `posttube/components/TubePlayer.tsx:362` and `posttube/hub/coverFrame.ts:160`.
  - `media-service/internal/service/playback.go:313-322`: child playlists list absolute signed CloudFront segment URLs.
  - `infra/terraform/modules/media/main.tf:365-378`: `access_control_allow_credentials = false`, `origin_override = true`, attached to the protected default behaviour (:318) and `public/*` (:299). Prod web origins (`infra/terraform/envs/prod/main.tf:175-178`) are cross-origin to `media.cleestudio.com`.
  - Fallback (v2 correction): `.github/workflows/build-web.yml:103-110,154` forces `NEXT_PUBLIC_API_BASE_URL=https://api.cleestudio.com`, so the fallback `/v1/media/<id>/serve` goes browser → gateway → 307 → CloudFront, not through Next. The `<video crossOrigin="use-credentials">` (`ReelVideo.tsx:646`) makes it credentialed too; Safari's native HLS path (:339-342) has the same problem.
  - The dev box has no CDN (`infra/devbox/devbox.cfn.yaml:71-107`), so this has never been seen.
- **How to reproduce:** On staging (or QA with the CloudFront distribution) with a web image built by `build-web.yml`: open `/reels` in Chrome with DevTools; look for a CORS error naming `Access-Control-Allow-Credentials` on a `media.<domain>/…/*.ts` request; check whether the following `/v1/media/<id>/serve` redirect also fails; repeat in Safari. Header half only: `curl -sI -H 'Origin: https://app.<domain>' '<signed segment url>'` shows Allow-Origin and no Allow-Credentials.
- **Impact:** Production web reels play without adaptive streaming at best, and not at all if the fallback is blocked. Every reel start has a failed request first.
- **Fix:** Set `withCredentials` only for API-host or same-origin URLs: `xhrSetup: (xhr, url) => { xhr.withCredentials = isApiUrl(url) }`. Segments carry their authorisation in the signature. Drop `crossOrigin="use-credentials"` on the `<video>` for CDN sources (use `anonymous`).
- **Acceptance:** Unit test of the hls config factory: a CDN fragment URL gets `withCredentials=false`, an API playlist URL gets `true` (fails now). On staging the network panel shows `.ts` requests to the media host with 200 and no `/serve` request, in Chrome and Safari.
- **Effort:** S
- **Was:** D-1

### reels-3

- **ID:** reels-3
- **Title:** `media.events.long` is not in the MSK topic list and auto-create is off, so in production every long video is transcoded inside the reel consumer and reels wait behind it for hours.
- **Severity:** P1
- **Category:** reliability / deployment
- **Evidence level:** VERIFIED-READ (code and Terraform); UNVERIFIED whether someone created the topic by hand on any cluster
- **Evidence:** `infra/terraform/modules/msk/topics-job/variables.tf:29-59` (27 topics, no `media.events.long`); `modules/msk/main.tf:147` `auto.create.topics.enable=false`; `envs/prod/main.tf:337-348` passes no `topics`. The name is hard-coded as `intakeTopic + ".long"` (`media-service/cmd/worker/transcode_classes.go:66-71`); no `CreateTopics` anywhere. On a write error the request is transcoded in the short loop (:213-221), one message at a time (:229-255), after the writer's own retry. Prod runs `MEDIA_TRANSCODE_CLASSES: "short,long"` (`deploy/services/media-service/values-prod.yaml:176`, whose comment at :174 says the topic must exist first). Local compose creates it (`Architecture/docker/init-topics.sh:54`), so dev never shows the gap. A long job may run up to 48 h (`processing/limits.go:157-158`).
- **How to reproduce:** On a broker without the topic, queue a 40-minute video, then a 15-second reel on the same partition: the reel stays `processing` until the long job ends; `media_transcode_routed_total{outcome="forward_failed"}` rises.
- **Impact:** The class split (reels never wait behind long videos) does nothing in production. Reels on that partition look stuck in processing for hours, with no error. Nothing is lost.
- **Fix:** Add `"media.events.long"` to the topics-job default list and re-run the Job. Optionally refuse to start the worker when the long class is configured and the topic is missing.
- **Acceptance:** A test (or render guard) asserting every topic named in code is in the MSK list (fails now). On QA a long upload shows `outcome="forwarded"`.
- **Effort:** S
- **Was:** C-3, reels-E-1

### reels-4

- **ID:** reels-4
- **Title:** The transcode fleet runs at most two short-form jobs at once for the whole platform (one sequential consumer per class in each of two worker pods, no HPA or KEDA), with no back-pressure at upload init, so a burst of uploads or a few 10-minute 4K reels queue every other reel for hours.
- **Severity:** P1
- **Category:** performance
- **Evidence level:** VERIFIED-READ (structure); SUSPECTED (queue times, arithmetic only, to be measured by load test L4)
- **Evidence:** One `kafka.Reader` per class, one goroutine each, strictly sequential (`media-service/cmd/worker/main.go:214-252`, `transcode_classes.go:229-256`). Prod `worker.replicaCount: 2`, classes `short,long` in the same pods, CPU limit 3 (`deploy/services/media-service/values-prod.yaml:153-176`). The HPA targets only the server (`charts/atpost-service/templates/hpa.yaml:1-13`); no ScaledObject anywhere. `media.events` has 6 partitions (`topics-job/variables.tf:62-66`). The reel encode runs three separate ultrafast passes, each decoding the full source (`internal/processing/video.go:383-435`). Per-job budget up to 210 min for a 600 s reel (`limits.go:129-158`). No queue-depth check at `POST /v1/media/init`; the only limit is the per-user budget of 10/h and 30/day, fail-open (`internal/service/ratelimit.go:21-55`). The only consumer-lag rule is in the local compose Prometheus (`Architecture/docker/alerts/slo_rules.yaml:65-66`). reels-3 puts long videos on the same loop.
- **How to reproduce:** Load test L4 (Track D §6): 200 portrait reels of 30-60 s in 5 minutes plus three 10-minute 4K reels; measure confirm-to-ready for the 200th short reel.
- **Impact:** At an assumed 30-90 s per 60 s reel, two lanes clear about 80-240 reels an hour. A launch burst leaves new reels in processing for a long time and nothing alerts.
- **Fix:** (a) `worker.replicaCount: 6` (equal to the partitions), 2 CPU requested each. (b) A lag rule for group `media-transcode-worker` and on `media_transcode_oldest_pending_age_seconds` (needs reels-5). (c) Encode the three renditions in one ffmpeg run with a split filter. (d) Later: KEDA on lag, and 503 `TRANSCODE_BUSY` at init past a threshold.
- **Acceptance:** A render guard: worker replicas ≥ media.events partitions in prod (fails now). L4: p95 confirm-to-ready under 120 s for 60 s reels at 200 uploads per 5 minutes; the lag alert fires with workers scaled to 0.
- **Effort:** S for (a)+(b); M for (c)
- **Was:** D-3, reels-E-13

### reels-5

- **ID:** reels-5
- **Title:** The transcode pipeline is unmonitored: the worker's metrics are never scraped, no alert about the worker reaches a person, and quarantined transcode messages and `media.events.dlq` are visible only in the database; an asset quarantined before its first heartbeat stays "processing" under steady load.
- **Severity:** P1
- **Category:** observability
- **Evidence level:** VERIFIED-READ
- **Evidence:** Metrics exist on `:9091` (`media-service/cmd/worker/main.go:39-63`, `readiness_metrics.go:27-46`, `transcode_lease.go:49-52`, `transcode_classes.go:81-84`). The worker container declares no port and the chart renders "NO SERVICE" (`charts/atpost-service/templates/worker-deployment.yaml:23-26,84-116`); the ServiceMonitor selects port `http` on the server Service (`servicemonitor.yaml:12-19`); no PodMonitor or extra scrape config (`infra/terraform/modules/observability/main.tf`). Rules match `container="media-service"` only (`prometheusrule.yaml:41-74`); no rule mentions `media_transcode` anywhere. kube-prometheus default rules (`KubePodCrashLooping`) do evaluate for the worker, but `observability/alerting.tf:19-23,77-83` routes everything without `source=atpost-service` to the `null` receiver (v2). Quarantine rows are inserted only (`store/postgres/media_outbox.go:156-172`) with no metric, alert, read route or replay. A permanent error in `resolveTranscodeSubject` (`transcode_guards.go:207-213`) happens before the heartbeat stamp (`main.go:365`), and the sweeper requeues NULL-heartbeat assets only when the pipeline is idle (`transcode_lease.go:147-149`) (V4-16).
- **How to reproduce:** Render the media-service prod chart and search the PrometheusRule and ServiceMonitor for the worker (none). Send a malformed record to `media.events`: one quarantine row, nothing reports it.
- **Impact:** A crash-looping worker, a Rekognition outage failing every scan, a stalled partition or reels-3 produce no signal; users report stuck reels first.
- **Fix:** Add `containerPort: 9091` (name `metrics`) and a PodMonitor for `component=worker`; rules in values-prod for worker restarts and zero available replicas (`container="media-service-worker"`), `media_transcode_oldest_pending_age_seconds > 1800 for 10m`, failure rate, stall give-ups, `forward_failed > 0`; a `media_transcode_quarantined_total` counter and a gauge of recent quarantine rows; mark the asset failed on a permanent pre-heartbeat error; route these to the platform receiver (shares feed-10).
- **Acceptance:** `render_test` asserts the worker is scraped and that rules exist for the worker container (fails now). Worker test: a permanent pre-heartbeat error increments the counter and leaves the asset failed. On QA, scaling the worker to 0 with one upload pending notifies within 30 minutes.
- **Effort:** S
- **Was:** reels-E-2, C-7, V4-16

### reels-6

- **ID:** reels-6
- **Title:** media-service takes the viewer from a bare `X-User-Id` header with no key or token on every read route, network policy is off, and the cluster's CNI would not enforce one anyway, so any workload inside the cluster can fetch any private, pending or deleted-but-not-purged media as its owner.
- **Severity:** P1
- **Category:** authn
- **Evidence level:** VERIFIED-READ (the trust and the missing policy); SUSPECTED (exploitation needs code inside the cluster)
- **Evidence:** `media-service/internal/http/delivery_viewer.go:27-34` (viewer = `uuid.Parse(c.GetHeader("X-User-Id"))`); read routes registered with no middleware (`handler.go:121-131,150-181`), `RequireInternalKey` only on `/v1/media/internal` (:195); global middleware is Recovery, RequestID, Logger, Metrics only (`cmd/server/main.go:440-444`). Same on `renditions_handler.go:13-19`, `clips_handler.go:63-73`, `audio_handler.go:26-31`. Owner short-circuits trust the header (`record_read.go:110-113`, `media_batch.go:203,227`, `download.go:74`, `caption_read.go:86`); post-service answers owner-preview for whatever `viewer_id` is sent (`story_surface.go:253-260`). The gateway does not stamp the internal key on `/v1/media`, `/v1/audio`, `/v1/subtitles` (`api-gateway/pkg/internalroutes/internalroutes.go:367-441`). `charts/atpost-service/values.yaml:380-381` `networkPolicy.enabled: false`, no media override. `infra/terraform/modules/eks/main.tf:58-61` installs `vpc-cni` without `enableNetworkPolicy`; no mesh, Cilium or Calico anywhere (v2). Edge traffic is safe: ingress is off (`values-prod.yaml:143-144`) and the gateway strips the header.
- **How to reproduce:** From any pod: `curl -H 'X-User-Id: <author uuid>' http://media-service.<ns>.svc.cluster.local:8087/v1/media/<media id>/url` → signed URLs for a private or pending reel; `/download` returns the original. The author id is public on the post.
- **Impact:** One compromised service, debug pod or header-setting SSRF reads every user's private video.
- **Fix:** Require `RequireInternalKey` on the `/v1/media`, `/v1/audio`, `/v1/subtitles` groups and have the gateway stamp the key on those prefixes, as for post, feed and analytics. Turn on network-policy enforcement in the `vpc-cni` addon (`configuration_values = {enableNetworkPolicy = "true"}`) and enable the chart's policy for media-service with ingress from the gateway and named peers only.
- **Acceptance:** Handler test: a read with `X-User-Id` and no key answers 401/404 (fails now). Gateway test: the key is stamped on `/v1/media/*`. `terraform plan` shows the addon setting; a rendered prod manifest contains a NetworkPolicy for media-service.
- **Effort:** S (key) + M (network policy and peers)
- **Was:** REELS-B-1

### reels-7

- **ID:** reels-7
- **Title:** Anyone, signed out included, can read the view and watch-depth buckets of any content id, including private, pending and deleted reels.
- **Severity:** P1 (it exposes counts, not content, and ids are UUIDs; a strict reading of "read another user's data" could argue P0)
- **Category:** authz
- **Evidence level:** VERIFIED-READ (V4 lead re-read; independently re-checked by the audit lead)
- **Evidence:** `analytics-service/internal/http/handler.go:74` registers `GET /v1/analytics/content/:contentId/views`; `GetContentViews` (:182-214) reads no identity and does no owner check, returning display plus `views_1s/3s/10s/30s/60s`. Its only guard is the engine-wide `RequireInternalKey` (:66-69). The gateway stamps the internal key on every `/v1/analytics` request, anonymous ones included (`api-gateway/pkg/internalroutes/internalroutes.go:11,440-441`; `api-gateway/cmd/server/main.go:383-392,687`). The same buckets are owner-gated on `/content/:id` (`content_insights.go:95-145`) and `/dashboard` (`dashboard.go:293-295`).
- **How to reproduce:** With no token, `GET /v1/analytics/content/<private reel id>/views` → 200 with non-zero buckets. User A on user B's reel → 200.
- **Impact:** Leaks the engagement of private, pending and deleted content and confirms that an id exists.
- **Fix:** Gate it like `GetContentInsights` (owner or admin, 404 otherwise), or remove the route (feeds use `/internal/content-views`).
- **Acceptance:** httptest: no `X-User-Id` → 401; A on B's content → 404; owner and admin → 200 (fails now). Add the route to `private_route_inventory_test.go`.
- **Effort:** S
- **Was:** V4-1

### reels-8

- **ID:** reels-8
- **Title:** Account deletion never reaches analytics-service: its two erasure paths are dead, so a deleted user's watch history, sessions and events are kept indefinitely while auth-service reports the purge complete.
- **Severity:** P1
- **Category:** privacy
- **Evidence level:** VERIFIED-READ
- **Evidence:** `identity-platform/services/auth-service/internal/purge/worker.go:44` `DefaultRequiredServices` has no `analytics` (or `monetization`); `REQUIRED_PURGE_SERVICES` is set in no deploy or Terraform file. Dead path 1 (v2): `analytics-service/internal/consumers/engagement_consumer.go:127-162` handles `user.deletion_requested`, which auth-service no longer emits (`auth-service/internal/store/lifecycle.go:142-149`, `http/account_control.go:5-16`); the purge worker emits `user.purge_requested`, which analytics does not consume. Dead path 2: `store/postgres/events.go:72-93` `DeleteEventsByUser` is called only by a test. Neither touches `analytics.playback_sessions` or Scylla `social_analytics.watch_sessions`, `viewer_history`, `reel_views`; neither acks on `platform.purge-acks.v1`.
- **How to reproduce:** Delete an account; after the auth purge completes, select from `analytics.events_raw` and `playback_sessions` for the deleted id: rows remain.
- **Impact:** DPDP erasure obligation not met for viewing history; the purge reports success.
- **Fix:** Re-point the existing handler at `user.purge_requested` on the identity topic (shared `purge.NewHandler`, as `media-service/cmd/server/main.go:229-235`), extend it to sessions and the Scylla watch tables (keep creator aggregates), ack as `analytics`, add `analytics` to the required list.
- **Acceptance:** Handler unit test deleting the user's rows across events, sessions and watch history (fails now: no handler for the event). Auth purge test that waits for the analytics ack.
- **Effort:** M
- **Was:** REELS-B-2

### reels-9

- **ID:** reels-9
- **Title:** The Android and web reels tests run in no CI at the current head, and feed-service's unit tests last ran in CI on 31 Oct; media-service and post-service tests did run green in integration-all at HEAD~1.
- **Severity:** P1
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (`gh run list`, `gh run view`)
- **Evidence:** Android: `android-native.yml` runs 40891364208 (29 Oct), 40175440840 (25 Oct) and the August runs all failed at "Write google-services.json" (`android-native.yml:83-90`, secret empty) before Gradle; no test results produced. Web: `pb-echo` has no `.github` folder and no remote branch has workflows; `build-web.yml` builds images with no test step. Go: `go-ci.yml` last ran 41168872400 at `44f00fafe` (31 Oct, 177 commits back; triggers PR, push to main, dispatch only, :19-35); integration-all 44008075845 at `770f5ed9d` ran `go.sh --full --db` for media and post, green (v3). feed-service is not in `scripts/ci/integration-services.yaml` and has 6 commits since its last CI unit run.
- **How to reproduce:** `gh run list --workflow=android-native.yml --branch codex/module-01-02-launch-safety`; `gh run view 40891364208`; `gh run list --workflow=go-ci.yml`.
- **Impact:** Launch gate "tests pass in CI from a clean state" cannot pass for reels; Android's ~1,800 unit tests and the web's ~788 reels-area cases are verified only by hand runs.
- **Fix:** Set `GOOGLE_SERVICES_JSON` or let the step write a placeholder for unit-test-only runs; add a `bun test` workflow to postbook-ui; add the release branch to go-ci push triggers (or dispatch it) and add feed-service to the integration manifest.
- **Acceptance:** A green run of Android CI, web tests and go-ci at the release commit, each with test-result artefacts.
- **Effort:** S
- **Was:** reels-F-11

### reels-10

- **ID:** reels-10
- **Title:** No test at any layer uploads a video, lets it transcode and publish, and plays it on a second account; the e2e media job proves only an image upload.
- **Severity:** P1
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (e2e run 44008085438 log; repository search, v3)
- **Evidence:** `Architecture/tools/integration/media_e2e_test.go:100-110,149` (PNG upload, text-as-MP4 refusal; the comment says a finished transcode belongs in a media-specific job that does not exist). `.github/workflows/e2e.yml:57-60` starts only api-gateway and media-service; `.github/e2e/docker-compose.e2e.yml` has no media-worker, ffmpeg or Kafka. No file in `tools/integration` creates a flick. `scripts/smoke/dev-smoke.mjs:595-701` does the journey for the author only, by hand.
- **How to reproduce:** `grep -l "flick\|m3u8\|video/mp4" Architecture/tools/integration/*.go`; read job "e2e (media)" of run 44008085438.
- **Impact:** The core journey can break across media-service, the worker, the review gate (reels-1) and the delivery gate with every CI job green.
- **Fix:** An e2e matrix entry "reels" (api-gateway, media-service, media-worker with ffmpeg, post-service, identity-profile, trust-safety, Redpanda) and `TestE2E_Reels_UploadPublishPlay` using `scripts/smoke/fixtures/smoke-2s-portrait.mp4`: upload as A, poll until ready+passed, create a flick, as B eventually see it approved and fetch `/hls/master.m3u8` listing 360p; anonymous on a private copy → 404.
- **Acceptance:** The flow is green, and goes red when `finalizeReviewGate` is stubbed out or the worker is not started.
- **Effort:** M
- **Was:** reels-F-1

### reels-11

- **ID:** reels-11
- **Title:** No load test exists for any reels endpoint, the transcode pipeline or the playlist route, and none has been run, so every capacity statement about reels is unproven.
- **Severity:** P1
- **Category:** tests / performance
- **Evidence level:** VERIFIED-RUN (repository search, Track D and v3)
- **Evidence:** `git ls-files | grep -iE "k6|vegeta|locust|gatling|loadtest|load-test|artillery|wrk|ghz|bench"` finds only dating-service and rider-service scripts and one Android unit test; no `func Benchmark` in media, feed or post-service; no results file anywhere. The Android macrobenchmark job was skipped and measures client frames only.
- **How to reproduce:** Run the search above.
- **Impact:** The rate at which `/v1/feed/reels`, the playlist route or analytics ingest misses its target, the upload burst the worker survives, and the cost per web viewer are unknown.
- **Fix:** Write and run L1-L7 (Track D §6) against a QA stack sized like production; commit scripts and results.
- **Acceptance:** k6 scripts under `Architecture/tools/loadtest/reels/` and a results file in this folder with p95, p99, error rate, consumer lag and queue age per scenario, meeting the Track D thresholds.
- **Effort:** L
- **Was:** D-4

### reels-12

- **ID:** reels-12
- **Title:** Every test that runs real ffmpeg skips when ffmpeg is absent, no CI job installs it, and the logs cannot show whether they ran, so there is no proof that a real reel transcodes, HLS is playable, GPS is stripped or ffmpeg is killed at its deadline.
- **Severity:** P1
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (local `go test -v`: 1164 pass, 10 skip; workflow grep); whether the CI runner image carries ffmpeg is UNVERIFIED (v3)
- **Evidence:** `media-service/internal/processing/hls_pipeline_test.go:39-46`, `reels_hardening_test.go:105-106,238-239` skip on `exec.LookPath("ffmpeg")`. No workflow installs ffmpeg (only image builds mention it); the tests do reach CI (`module-05-messaging-safety.yml:51-52`, integration-all) on `ubuntu-26.04`, and `scripts/verify/go.sh:133,168` runs without `-v`, so skips are invisible.
- **How to reproduce:** `cd Architecture/services/media-service && go test -count=1 -v ./internal/processing -run 'TestHLSRepackagedReelIsPlayable|TestRenditionCarriesNoLocation'` on a machine without ffmpeg.
- **Impact:** A broken encoder argument, a missing codec in the Alpine image or an HLS regression passes CI; `ProbeVideo` → `CheckVideoLimits` never runs on a real file.
- **Fix:** `sudo apt-get install -y ffmpeg` in the media-service CI jobs; `requireFFmpegOrSkip` calls `t.Fatal` when `CI=true`. Better: also run them inside the built media-worker image.
- **Acceptance:** The CI log shows the five ffmpeg tests as PASS in a `-v -run` step; removing ffmpeg turns the job red.
- **Effort:** S
- **Was:** reels-F-3

### reels-13

- **ID:** reels-13
- **Title:** The server half of resumable upload (upload a part, read the session status, complete) has no test at any layer.
- **Severity:** P1
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (repository search, v3)
- **Evidence:** `media-service/internal/service/resumable.go` defines `UploadPart`, `GetResumableUploadStatus`, `CompleteResumableUpload`; grep of every `*_test.go` for `.(UploadPart|CompleteResumableUpload|GetResumableUploadStatus)(` finds nothing. Covered: chunk plan, init rate limit and cap, session expiry (`reels_hardening_integration_test.go:179`). Android resume is well tested against a fake server (`core/media/…/upload/ResumableUploaderTest.kt:249,401,424,437,463,473`). The web client half is reels-39.
- **How to reproduce:** The grep above.
- **Impact:** A re-sent, out-of-order or missing part, or a status read that misreports parts, can corrupt or lose large uploads with green CI.
- **Fix:** Integration test against Postgres and a fake multipart blob store: parts 1 and 3 → status lists exactly {1,3}; re-upload of 1 is idempotent; complete refused until 2 lands, then succeeds once, and a second complete is a no-op; another user's uploadId → 404.
- **Acceptance:** The test passes; dropping the part-count check in `CompleteResumableUpload` fails it.
- **Effort:** M
- **Was:** reels-F-5 (server part)

### reels-14

- **ID:** reels-14
- **Title:** Neither client has a test that only one reel player is active, that the Android pool stays bounded and releases players, or that two videos never play audio at once; the web player tests read the component source as text.
- **Severity:** P1
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (repository search, v3)
- **Evidence:** Android: no test constructs `PlayerPool` or calls `playOnly`; `PlayerPoolPlaceTest.kt` (3) and `PlayerPoolRelocationTest.kt` (5) test helpers; `FakeExoPlayer.kt` exists. Added-sound overlap is covered (`ReelSoundPlayerTest.kt:436`). No `androidTest` sources. Web: `soundSync.test.ts:269`, `presentation.test.tsx:21,59,76`, `stage.test.ts:23` `readFileSync` the components; nothing mounts `ReelVideo`.
- **How to reproduce:** `grep -rn "playOnly\|PlayerPool(" mobile/android --include=*Test.kt`.
- **Impact:** Journey 4 (swipe 50, no overlap, flat memory) can regress on both clients with green tests.
- **Fix:** `core/media/src/test/.../PlayerPoolTest.kt` with `FakeExoPlayer`: after `playOnly(i)` for i in 0..49 exactly one player has `playWhenReady` and volume > 0, at most 4 players were created, each evicted one released. Web: a happy-dom test mounting `ReelsScreen` with three reels: one `<video>` playing, previous hls instance destroyed on swipe.
- **Acceptance:** Breaking `playOnly` so it skips pausing the previous player fails the Android test.
- **Effort:** M
- **Was:** reels-F-7

---

## P2

### reels-15

- **ID:** reels-15
- **Title:** media-service accepts HS256 user tokens in production with the shared `JWT_SECRET`, treats a token without `exp` as never expiring, and checks neither issuer nor audience; the gateway is RS256-only.
- **Severity:** P2
- **Category:** authn / config
- **Evidence level:** VERIFIED-READ (V4 lead; re-checked by the audit lead)
- **Evidence:** `media-service/internal/http/middleware.go:86-162` `verifyJWTClaims`: HS256 branch with no production switch (:108-119), empty kid → active secret (:41-43), `claims.Exp > 0 &&` (:147), no iss/aud. `deploy/services/media-service/values-prod.yaml:51-55` mounts `JWT_SECRET`. The same secret is mounted into post-service and feed-service prod values, which never read it. Edge tokens are checked by the gateway first, so the exposure is to in-cluster holders of the secret (compounds reels-6).
- **How to reproduce:** With `JWT_SECRET`, mint `{"alg":"HS256"}{"sub":"<any user>"}` with no exp and call a media-service user route in-cluster (`POST /v1/media/init`): accepted as that user, for ever.
- **Impact:** Any pod with the shared secret can act as any user on media routes with non-expiring tokens.
- **Fix:** In production refuse HS256 at boot (require `JWT_PUBLIC_KEY_PEM`), require exp, check iss and aud as the gateway does; remove `JWT_SECRET` from post and feed prod values.
- **Acceptance:** Middleware tests with production config: an HS256 token → 401; an RS256 token without exp → 401; a foreign aud → 401 (all fail now).
- **Effort:** S
- **Was:** REELS-B-13, V4-7

### reels-16

- **ID:** reels-16
- **Title:** User-supplied audio (alternate audio tracks, voice uploads) reaches ffprobe/ffmpeg through the un-hardened runner: no protocol whitelist, no forced demuxer, no deadline.
- **Severity:** P2
- **Category:** upload
- **Evidence level:** VERIFIED-READ (code path); SUSPECTED (exploitability depends on the image's ffmpeg build; needs a sandboxed run)
- **Evidence:** The video path is hardened (`processing/tool.go:17-33,121,175-196`). The audio path is not: `processing/audio.go:34,58`, `audio_mux.go:35`, `audio_probe.go:30` use bare `exec.CommandContext`. Track uploads are probed after a header-only MIME check (`service/audio_tracks.go:445-466`) and muxed by the worker; voice confirm calls `ProbeAudio` (`service/voice.go:67`).
- **How to reproduce:** `POST /v1/media/<own ready video>/audio-tracks` with an `audio/mp4` body that is a playlist or external-reference container; observe whether ffprobe opens a URL (sandbox only).
- **Impact:** Possible SSRF or local file read baked into a playable track; a crafted file can hold a goroutine and CPU with no deadline.
- **Fix:** Route `ProbeAudio`, `ExtractAudio`, `MuxAudio`, `ProbeAudioFile` through `runTool` with an audio magic-byte allow-list.
- **Acceptance:** Unit tests: a playlist body is refused before ffprobe; argv has `-protocol_whitelist file` and `-f`; a stubbed slow tool times out.
- **Effort:** S
- **Was:** REELS-B-3

### reels-17

- **ID:** reels-17
- **Title:** A reel that is deleted, made private or blocked keeps playing from already-issued signed URLs for up to 35 minutes (12 h for long video), and deleted objects stay in the bucket for 90 days.
- **Severity:** P2
- **Category:** privacy
- **Evidence level:** VERIFIED-READ (V4 confirmed new manifest and URL requests are refused at once after a takedown)
- **Evidence:** `media-service/internal/delivery/signer.go:66-72` (5 min manifests and files); segments 2x duration + 15 min, cap 12 h (`sign_cache.go:54-89`); canned signatures, not viewer-bound; no CloudFront invalidation. Bucket versioned, noncurrent versions kept 90 days (`infra/terraform/modules/media/main.tf:138-151`); `DeleteObject` removes the current version only (`internal/store/blob/store.go:509`).
- **How to reproduce:** Copy a segment URL from a media playlist, delete or privatise the reel, fetch the URL: it plays until expiry.
- **Impact:** Revocation is not immediate; "deleted" bytes are recoverable for 90 days (DPDP erasure).
- **Fix:** Cap short-form segment TTL to a few minutes past duration (players refresh on `X-HLS-Expires-At`); on purge delete every object version, or expire noncurrent versions under `user/` after 1 day.
- **Acceptance:** Unit test on the segment TTL for a 60 s reel; integration test that purge leaves no object version.
- **Effort:** S
- **Was:** REELS-B-4

### reels-18

- **ID:** reels-18
- **Title:** Like, reaction and comment routes skip the review, processing and schedule gates the detail read applies, so a stranger can engage with a pending, rejected, processing or scheduled public reel, notify its author and confirm it exists.
- **Severity:** P2
- **Category:** authz / privacy
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `post-service/internal/service/post_visibility.go:51-72` (`loadPostForEngagement` → `GetPost` filters on `deleted_at` only → `viewerMayViewPost` :78-126 checks account, visibility and graph, never review status, processing or `publish_at`); compare `post_reads.go:36-50`. Used by `post_reactions.go:28`, `post_comments.go:147`, `reel_engagement.go:53`.
- **How to reproduce:** As B, like A's pending public reel by id → 200 (a random id → 404); A gets "liked your post".
- **Impact:** Existence oracle and notifications for content that is not live.
- **Fix:** In `loadPostForEngagement`, for non-authors return not found unless the post is approved and not processing or scheduled.
- **Acceptance:** Table test over pending, flagged, rejected, processing, scheduled: stranger → not found, author → OK.
- **Effort:** S
- **Was:** V4-3

### reels-19

- **ID:** reels-19
- **Title:** Trending hashtags count tags from pending, private, followers-only, rejected and deleted reels (and comments on them), are never decremented, and are served to everyone.
- **Severity:** P2
- **Category:** privacy / integrity
- **Evidence level:** VERIFIED-READ (one reader); whether post-service and search-service share one Redis in deploy config is UNVERIFIED
- **Evidence:** `post-service/internal/service/post_create.go:691-693` → `schedule.go:253-290` (`publicHashtags` :157-162 excludes only members-only; `ZIncrBy trending:hashtags:{date}`); `post_comments.go:184-201`. Readers: `search-service/internal/http/handler.go:548-575` (`GET /v1/discover/trending`), `post-service/internal/http/hashtag_trending_handler.go:249`.
- **How to reproduce:** Post a followers-only reel tagged `#uniqueTag123`; as a stranger `GET /v1/discover/trending` lists it, and it stays after delete.
- **Impact:** Leaks private tags; trending can be gamed with unpublished content.
- **Fix:** Bump only when `events.SearchEligible(visibility, review status)` holds; move the pending bump to the approval transition; same for comment tags.
- **Acceptance:** miniredis test: pending or followers-only → no ZIncrBy; public approved → ZIncrBy.
- **Effort:** S
- **Was:** V4-4

### reels-20

- **ID:** reels-20
- **Title:** Android's 256 MiB streaming video cache is not cleared on sign-out, so the next account on the device holds the previous account's watched segments, members-only included.
- **Severity:** P2
- **Category:** privacy
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `core/media/…/di/MediaModule.kt:51-52` (`SimpleCache(cacheDir/media)`); no sign-out task touches it; only image caches are cleared (`app/…/ImageCacheTeardown.kt`).
- **How to reproduce:** Watch members-only reels as A, sign out, sign in as B: the media cache folder still holds A's segments.
- **Impact:** Cross-account data left on a shared device.
- **Fix:** A sign-out task that removes every cached key or empties the folder.
- **Acceptance:** Test: after the sign-out task `cache.keys` is empty.
- **Effort:** S
- **Was:** V4-8

### reels-21

- **ID:** reels-21
- **Title:** The web app sets no CSP, X-Frame-Options/frame-ancestors, nosniff, Referrer-Policy or HSTS on any route, reels included.
- **Severity:** P2
- **Category:** security hardening
- **Evidence level:** VERIFIED-READ (repo-wide grep including infra)
- **Evidence:** `next.config.ts` has no `headers()`; `src/middleware.ts:236-240` sets headers only for `/doorstep-pro/digilocker`.
- **How to reproduce:** `curl -I` a local build of `/reels`.
- **Impact:** Clickjacking of the signed-in reels stage; no XSS containment.
- **Fix:** `headers()` with a CSP (`frame-ancestors 'none'`), nosniff, Referrer-Policy, HSTS, Permissions-Policy.
- **Acceptance:** A build check on `next.config` headers for `/:path*`.
- **Effort:** S
- **Was:** V4-14

### reels-22

- **ID:** reels-22
- **Title:** The web `/v1` proxy joins decoded catch-all path segments into the upstream URL, so dot segments may escape `/v1/` on the gateway.
- **Severity:** P2
- **Category:** security
- **Evidence level:** SUSPECTED (depends on how Next.js decodes `%2e%2e` in catch-all params)
- **Evidence:** `src/app/api/proxy/[...path]/route.ts:66` (`${API_GATEWAY}/v1/${path.join("/")}` then `new URL()`). The gateway's `/internal/` refusal still applies.
- **How to reproduce:** Against a local build, request `/api/proxy/..%2f..%2finternal/...` and inspect the upstream URL.
- **Impact:** The proxy may reach gateway paths outside `/v1`.
- **Fix:** Refuse `.`/`..` segments or segments containing `/`; `encodeURIComponent` each segment.
- **Acceptance:** Route test: a dot-segment path → 400; a normal path unchanged.
- **Effort:** S
- **Was:** V4-13

### reels-23

- **ID:** reels-23
- **Title:** The web `/v1` proxy buffers every request body whole with no size cap, before authentication.
- **Severity:** P2
- **Category:** input
- **Evidence level:** SUSPECTED (Node and hosting limits not measured). v2: in prod legitimate media and upload traffic goes to the API host directly, but the proxy route still exists and accepts any POST.
- **Evidence:** `src/app/api/proxy/[...path]/route.ts:98-102` `await req.arrayBuffer()` for any non-GET; the comment at :83-98 says nothing else bounds it.
- **How to reproduce:** Several concurrent unauthenticated multi-GB POSTs to `/v1/anything` on the web host; watch Next memory.
- **Impact:** Anyone can crash the web front end.
- **Fix:** Reject when Content-Length exceeds 64 MiB plus headroom; stream the body (`duplex: "half"`).
- **Acceptance:** Proxy test: a 65 MiB body → 413 without calling upstream.
- **Effort:** S
- **Was:** REELS-B-8

### reels-24

- **ID:** reels-24
- **Title:** media-service has no request rate limit of its own, and owner endpoints that run ffmpeg download the whole original into the API pod on every call.
- **Severity:** P2
- **Category:** input
- **Evidence level:** VERIFIED-READ
- **Evidence:** No rate-limit middleware (`cmd/server/main.go:440-457`). `POST /v1/media/:id/frames` streams the original (16 GiB default in prod) to server disk and runs ffprobe + up to 10 ffmpeg runs per call (`service/frames.go:28-58`) on 1 CPU / 1 GiB pods (`values-prod.yaml:25-31`). Audio tracks read whole into memory (`audio_tracks.go:439-462`); voiceover `io.ReadAll` with no cap (`http/audio_handler.go:219-237`).
- **How to reproduce:** Loop `POST /v1/media/<id>/frames` on a large video from one account; watch pod disk and CPU.
- **Impact:** One account can take down playback for everyone (authorization runs in the same pods).
- **Fix:** Per-user Redis limits on frames, extract-frame, cover-frame, audio-track writes, voiceover; extract from the 720p rendition; `http.MaxBytesReader` on voiceover.
- **Acceptance:** Handler tests: N+1th call → 429; oversized voiceover → 413.
- **Effort:** S
- **Was:** REELS-B-6

### reels-25

- **ID:** reels-25
- **Title:** Destructive media internal routes (delete any asset, anonymize, reprocess, sound takedown) accept the one shared internal key with no per-caller service token.
- **Severity:** P2
- **Category:** authz
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/internal/http/handler.go:193-230` (`RequireInternalKey` only, some add `refuseGatewayIdentity`); image-bytes, document-bytes and recording import also require a signed service token (:234-244). Every service holds the key.
- **How to reproduce:** With the key, `DELETE /v1/media/internal/<any id>` naming an accepted referrer, or `POST .../sounds/<id>/takedown`.
- **Impact:** Compromise of any one service lets it delete or silence any user's media.
- **Fix:** Require a service token with an op per route via `requireServiceCaller`.
- **Acceptance:** Handler tests: bare key → 403 on each route; the named caller's token succeeds.
- **Effort:** M
- **Was:** REELS-B-7

### reels-26

- **ID:** reels-26
- **Title:** The devbox compose stack falls back to secrets published in this public repository (JWT, internal key, moderation HMAC keys, TURN, LiveKit) and to one-year access tokens when its `.env` misses a key.
- **Severity:** P2
- **Category:** secrets / config
- **Evidence level:** SUSPECTED (the devbox `.env` was not readable)
- **Evidence:** `Architecture/docker/docker-compose.yml` defaults for `JWT_SECRET`, `INTERNAL_SERVICE_KEY`, `POST_MODERATION_HMAC_KEY`, `STORY_MODERATION_HMAC_KEY`, `TURN_SECRET` (:1501, :2473), `ACCESS_TOKEN_TTL:-8760h` (:422); `docker-compose.devbox.yml` overrides none and adds LiveKit dev keys (:144-145). Values not reproduced here.
- **How to reproduce:** On the devbox, check whether each key is set in its `.env`.
- **Impact:** If any is missing, anyone reading the public repo can mint tokens or call internal routes on the internet-facing dev stack.
- **Fix:** `${VAR:?required}` instead of defaults in the devbox overlay.
- **Acceptance:** `docker compose -f … -f docker-compose.devbox.yml config` with an empty env fails.
- **Effort:** S
- **Was:** V4-17

### reels-27

- **ID:** reels-27
- **Title:** Watch time is client-controlled, with no wall-clock bound and no per-viewer cap, and it feeds Creator Fund eligibility.
- **Severity:** P2
- **Category:** integrity / monetization
- **Evidence level:** VERIFIED-READ (ingest re-read by the V4 lead; rollup and monetization reads by one reader). This is the FAIL half of V4's candidate PASS (c4).
- **Evidence:** `analytics-service/internal/service/ingest.go:449-454` (only `watched_ms_total ≥ increment` and ≤ max duration), :480-495 (clamp to server duration × (client `loop_count`+1), loop cap 20); nothing compares watched time with elapsed time. `aggregation/hourly.go:496-502` sums over every session; `daily.go:541,569` copies it; `monetization-service/internal/store/postgres/creator_fund.go:475-487`, `internal/service/creator_fund.go:31,131,140` use it for the 10-hour threshold.
- **How to reproduce:** As one account on a 10-minute reel, send play_start plus one heartbeat with `watched_ms_total=12,600,000`, `loop_count=20`, for three fresh session ids: about 10.5 h credited at once.
- **Impact:** Creator Fund eligibility can be manufactured with a handful of requests (paid views stay capped).
- **Fix:** Clamp watched-time growth to (now − first_seen) × speed + slack in `applySessionUpdate`; cap watch time per viewer per content in the daily rollup.
- **Acceptance:** Unit test: a heartbeat claiming more than elapsed × speed is clamped. Rollup test: 3 sessions from one viewer contribute at most one session's watch time.
- **Effort:** M
- **Was:** V4-2

### reels-28

- **ID:** reels-28
- **Title:** One signed-in account can raise a reel's displayed view count for the rest of the day by sending `play_end` events with fresh session ids; `play_end` without `play_start` is accepted.
- **Severity:** P2
- **Category:** integrity
- **Evidence level:** VERIFIED-READ for the intra-day inflation (C-11, V4); B-5's claim that it persists after the nightly rollup is SUSPECTED and not supported by the other two readers
- **Evidence:** Today's figure = capped daily rows + uncapped hourly rows (`analytics-service/internal/store/postgres/aggregates.go:330-378`); the per-viewer cap applies only in the daily rollup (`aggregation/daily.go:23-34`); display views are `COUNT(*)` of sessions (`aggregation/hourly.go:398,496`); dedupe per (actor, session, content) (`store/postgres/sessions.go:164`) with client-minted session ids (`ingest.go:351-361`); `play_end` without `play_start` accepted (`ingest.go:322-327,548-603`, `http/content_views.go:30-36`). Only the gateway's 60 rps/user limits it.
- **How to reproduce:** As one account, POST batches of 200 `play_end` events for one reel, each with a new session id and `watched_ms_total ≥ 3000`; after the hourly aggregation the display count rises by the number sent; it falls back at the next rollup.
- **Impact:** Fake popularity on the public counter and in anything ranking on it during the day; money is not affected.
- **Fix:** Drop a `play_end` whose session has no `play_start`; count distinct viewers per content for the open-hours term.
- **Acceptance:** Aggregation test: 100 sessions from one actor in a day → one display view; 10 orphan `play_end`s → 0.
- **Effort:** S
- **Was:** REELS-B-5, C-11, V4-P3-4

### reels-29

- **ID:** reels-29
- **Title:** Resolving a report has no effect on the reported reel: nothing consumes `report.resolved`, so a takedown needs a separate admin-console action.
- **Severity:** P2
- **Category:** functional / moderation
- **Evidence level:** VERIFIED-READ for the services; whether the admin console chains the two steps is UNVERIFIED
- **Evidence:** `trust-safety-service/internal/service/moderation.go:316-370` (status written, `ReportResolved` published, no consumer anywhere); `POST /v1/posts/internal/moderation` called only by appeal overturn (`post-service/internal/http/post_moderation_http.go:133-160`); the real takedown is `admin-service/internal/http/handler_content.go:51-104`.
- **How to reproduce:** File a report on a reel; `PATCH /v1/reports/:id {"status":"resolved"}`: the reel stays approved, searchable and playable.
- **Impact:** "Resolved" reports leave violating reels live.
- **Fix:** Require an action on post-target resolution (none | reject) and on reject call the post-service decision endpoint; or let the console resolve only after the content decision succeeds.
- **Acceptance:** Integration test: resolve with action=reject → review_status rejected, search removal, media-access denies a non-owner.
- **Effort:** M
- **Was:** V4-5

### reels-30

- **ID:** reels-30
- **Title:** "New upload" notifications are dropped for good for any reel still pending or processing at create (the normal path), and for every followers-only upload.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `PostCreated` is emitted at create while pending (`post-service/internal/service/post_create.go:491-496,528-559`); fan-out reads the post anonymously (`notification-service/internal/service/fanout_eligibility.go:180,279-284`); `GetPost` 404s for pending or non-public posts (`post_reads.go:36-58`), read as "not exists" and skipped for good (:83-84,187-188); the approval flip emits only `PostSearchEligibilityChanged` (`store/postgres/posts.go:1540-1551`).
- **How to reproduce:** B subscribes to A; A posts a reel whose create beats its transcode: B never gets `creator_uploaded_flick`.
- **Impact:** Subscriber upload notifications mostly do not work for reels.
- **Fix:** Fan out on the first transition to approved (dedupe on post id), or treat pending as retry-later via an internal fan-out-state route.
- **Acceptance:** Fan-out test: pending first, approved second → delivered once.
- **Effort:** M
- **Was:** V4-6

### reels-31

- **ID:** reels-31
- **Title:** A transcode failure rejects the post permanently: a later successful reprocess (operator or sweeper) never reopens it, and media events carry no per-asset ordering.
- **Severity:** P2
- **Category:** data
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/consumers/media.go:356-357` (failed → rejected); `FlipReviewStatusFromPending` moves only from pending (`store/postgres/posts.go:1543-1544`); failures include the sweeper's give-up after infrastructure faults (`media-service/cmd/worker/transcode_lease.go:422-485`); events keyed by event id (`media-service/internal/events/producer.go:62`).
- **How to reproduce:** Kill the worker mid-transcode three times, then `POST /v1/media/internal/:id/reprocess`: asset ready+passed, post stays rejected.
- **Impact:** The creator sees "rejected" for an upload never judged; operator recovery does not recover the post.
- **Fix:** Map failed → pending or `needs_changes`; let a newer-generation ready+passed completion flip it; key media events by `media_asset_id`.
- **Acceptance:** Consumer test: failed then ready+passed for one media → approved.
- **Effort:** S
- **Was:** C-5

### reels-32

- **ID:** reels-32
- **Title:** Confirm marks a video 'uploaded' and queues its transcode in two commits; a crash between them leaves an asset that no sweeper or GC will pick up.
- **Severity:** P2
- **Category:** data
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/internal/service/upload.go:295` (`MarkUploadConfirmed`) and :385 (`QueueTranscode`, separate tx, `store/postgres/media_outbox.go:30-67`); the sweeper selects only processing (`transcode_lease.go:234-251`); orphan GC only `pending_upload` (`media.go:541-547`); `media_generation.go:171-175` ignores 'uploaded'.
- **How to reproduce:** Fail the `QueueTranscode` transaction once on a web upload whose tab is then closed.
- **Impact:** The reel shows "processing" to its author for ever; no count, no alert.
- **Fix:** Do the 'uploaded' update inside `QueueTranscode`'s transaction, or let the sweeper re-queue a video at 'uploaded' for over 30 minutes with no request row.
- **Acceptance:** Integration test: `QueueTranscode` fails after `MarkUploadConfirmed` → after one sweep the asset has a request outbox row.
- **Effort:** S
- **Was:** C-6

### reels-33

- **ID:** reels-33
- **Title:** Sound use counts only ever go up: `POST /v1/audio/:id/use` adds one on every call with no per-user dedupe, attach adds again, and deleting a reel never subtracts.
- **Severity:** P2
- **Category:** data
- **Evidence level:** VERIFIED-READ for the writes; SUSPECTED that clients call both for one reel
- **Evidence:** `media-service/internal/service/audio_reads.go:219-233` → `store/postgres/audio.go:292-303` (unconditional +1); `post-service/internal/service/post.go:467-474`, `store/postgres/audio.go:156-163`; no decrement anywhere.
- **How to reproduce:** Loop `POST /v1/audio/<id>/use` as one user: usage_count climbs per call.
- **Impact:** "N videos use this sound" and catalogue order can be inflated by one account.
- **Fix:** Count once per (user, sound) or only on attach; recount nightly from live posts.
- **Acceptance:** 10 `/use` calls by one user move usage_count by at most 1.
- **Effort:** S
- **Was:** C-8

### reels-34

- **ID:** reels-34
- **Title:** A Rekognition outage re-encodes every queued video up to 32 times, blocks the reel queue for about 15 minutes per message, and ends with the uploads marked failed.
- **Severity:** P2 (was P1; v1: availability and cost, nothing unscanned is published)
- **Category:** reliability
- **Evidence level:** VERIFIED-READ (code path); timings computed, not measured
- **Evidence:** The scan runs after every encode and upload (`media-service/cmd/worker/main.go:984-1012`); `classifyScanError` (:1031-1037) treats provider errors as transient; `retryUntilDurable` re-runs the whole message 8 times (`transcode_guards.go:60,130-167`; backoff `main.go:561-564`); the sweeper re-queues 3 times then writes failed + manual_review (`store/postgres/transcode_lease.go:69-76,446`); 1 + 3 × 8 = 32 runs; no rendition reuse, no breaker.
- **How to reproduce:** On dev, point the scanner at an unreachable endpoint and upload one reel; count ffmpeg runs and read the final status.
- **Impact:** During an outage over about an hour every reel uploaded ends failed and must be re-uploaded; CPU wasted and the partition blocked.
- **Fix:** Sample and scan frames from the original before encoding; after the attempt cap, complete as ready + manual_review (unpublishable) and queue a re-scan.
- **Acceptance:** Unit test: a scanner failing 3 times then succeeding → exactly one encode, ready+passed; scanner down throughout → ready+manual_review, not failed.
- **Effort:** M
- **Was:** reels-E-4

### reels-35

- **ID:** reels-35
- **Title:** The worker's 50 Gi scratch emptyDir is probably backed by a ~20 GiB node root volume, its free-space check cannot see the emptyDir limit, and the short and long loops share it, so large uploads fail outright or two concurrent jobs fill the node disk.
- **Severity:** P2 (P1 if `terraform plan` shows a 20 GiB root)
- **Category:** deployment
- **Evidence level:** SUSPECTED (node disk not provable from the repo); the rest VERIFIED-READ (v1)
- **Evidence:** `deploy/services/media-service/values-prod.yaml:155,166` (2 replicas, `scratchSizeLimit: 50Gi`), `charts/atpost-service/values.yaml:249` (`nodeSelector: {}`); no `MEDIA_WORKER_MAX_ORIGINAL_BYTES` in prod → 16 GiB default (`processing/original_cap.go:18`) with 3x headroom (`transcode_guards.go:63,248-255`); `infra/terraform/modules/eks/main.tf:15-16,77` (module `~> 20.29`, `disk_size = 100` with no `use_custom_launch_template=false` or `block_device_mappings`). v1 mechanism correction: on a 20 GiB filesystem the guard refuses any original over about 6.6 GiB permanently; eviction remains possible when two concurrent jobs each pass the check.
- **How to reproduce:** `terraform plan` of `envs/prod` and read the launch template's root volume; or on QA `df -h /var/lib/kubelet` on a node.
- **Impact:** Large uploads fail, or the worker (and other pods on the node) is evicted mid-job.
- **Fix:** Root volume ≥ 100 GiB via `block_device_mappings` (or `use_custom_launch_template = false`); set `MEDIA_WORKER_MAX_ORIGINAL_BYTES` with 3x headroom for two loops; pod anti-affinity for workers.
- **Acceptance:** Plan shows the root size; a render guard asserts 3 × max original × 2 loops ≤ `scratchSizeLimit` in every values file.
- **Effort:** S
- **Was:** reels-E-3

### reels-36

- **ID:** reels-36
- **Title:** On Android without a Banuba licence the Record button opens the system camera with no duration limit, so the 10-minute-per-clip camera rule is not enforced.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** `feature/post/…/createhub/ReelSurface.kt:208,248-254` (`CaptureVideo()` with only an output URI); the cap exists only for Banuba (`banuba/BanubaKoinModule.kt:61-62`, `core/media/…/publish/ReelLength.kt:36`).
- **How to reproduce:** Build without a Banuba token, Create → Reel → Record for 12 minutes: accepted, offered as long video.
- **Impact:** Founder rule broken in the default build.
- **Fix:** Put `MediaStore.EXTRA_DURATION_LIMIT = 600` on the intent; reject a camera clip over the cap in `VideoGate`.
- **Acceptance:** Unit test on the intent builder: `EXTRA_DURATION_LIMIT == 600`.
- **Effort:** S
- **Was:** A-4

### reels-37

- **ID:** reels-37
- **Title:** Android cannot save, resume, edit or publish a reel draft; a draft started on the web is stuck there and leaving the Android composer loses the work.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** Only `GET v1/reels/drafts` (`feature/tube/…/data/hub/CreatorHubApi.kt:17,38`); server routes `post-service/internal/http/drafts.go:20-25`; web `useUploadStudio.ts:477-547,597-608`.
- **How to reproduce:** Save a draft on web; open Creator Hub drafts on Android: listed, cannot be opened or published.
- **Impact:** "Android has every web feature" broken for drafts.
- **Fix:** Draft POST/PATCH/DELETE/publish in the Android API, "Save draft" in `ReelSurface`, "Continue" on the hub row.
- **Acceptance:** Contract test against the server fixture; ViewModel test that Save draft calls POST then PATCH.
- **Effort:** M
- **Was:** A-5

### reels-38

- **ID:** reels-38
- **Title:** Android reels neither let the creator set the AI-generated label nor show it to viewers, while the web does both.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** No AI control in `createhub` (grep matches only the photo studio: `studio/StudioScreen.kt:1145`, `StudioPostRequest.kt:60`); the reels player never reads disclosures (labels drawn only on `FeedStage.kt:340`); the DTO decodes them (`core/feed/…/dto/FeedDtos.kt:120`, `core/model/…/FeedItem.kt:191-204`). Web: `features/upload/studioApi.ts:55`, `features/reels/components/ReelOverlay.tsx:74`.
- **How to reproduce:** Publish from web with "Made with AI"; view in Android Reels: no label.
- **Impact:** Founder rule "AI label shown to viewers" fails on the super-app.
- **Fix:** Draw `item.disclosure.labels` in the reel overlay; add an AI switch to `SwitchesCard` and send `ai_generated`.
- **Acceptance:** Compose test: `aiGenerated=true` shows "AI-generated" on the reel page.
- **Effort:** S
- **Was:** A-2b

### reels-39

- **ID:** reels-39
- **Title:** A web upload cannot resume after a reload, a closed tab or a crash: the resumable session is held only in memory, so the whole file is sent again.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ (confirmed by v3)
- **Evidence:** `src/lib/resumableUpload.ts:54-65` (init → chunks → complete in one call), :126-134 (retries only within the call); no storage use and no `GET /v1/media/upload/resumable/:id`.
- **How to reproduce:** Upload a 2 GB short, reload at 60 %: restarts from 0 % and spends another upload-budget slot.
- **Impact:** The "interrupt and resume" journey fails on web beyond a brief network blip.
- **Fix:** Persist {uploadId, name, size, lastModified}; on re-pick call the status route and send only missing parts.
- **Acceptance:** Unit test with a fake API: stored session + status `{uploaded_parts:[1,2]}` sends parts 3..n only.
- **Effort:** M
- **Was:** A-6 (and the web clause of reels-F-5)

### reels-40

- **ID:** reels-40
- **Title:** Web publish of a draft falls through to a direct create when the draft publish fails for any non-final reason, including a timeout after the server already published, which can post the reel twice.
- **Severity:** P2
- **Category:** data
- **Evidence level:** VERIFIED-READ for the fall-through (V4 confirmed; the draft publish carries no Idempotency-Key); whether the media claim refuses the second create is UNVERIFIED
- **Evidence:** `src/features/upload/useUploadStudio.ts:597-608` (catch → fall through unless `isFinalPublishRefusal`), :612-622 (`createReel` with a fresh key); `data/reelsApi.ts:302-311`.
- **How to reproduce:** Throttle so `publishDraft` times out after the server commits; the studio calls `createReel` for the same media.
- **Impact:** Duplicate reel or a confusing error.
- **Fix:** On an unknown outcome GET the draft and use its post id; fall through only on a definite 4xx; send an Idempotency-Key on draft publish.
- **Acceptance:** Hook test: publishDraft rejects with a network error and the draft reads back published → `createReel` not called.
- **Effort:** S
- **Was:** A-7

### reels-41

- **ID:** reels-41
- **Title:** Android reels never send `end_reason=ended`, and with Auto scroll a finished reel records a phantom loop.
- **Severity:** P2
- **Category:** analytics integrity
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** The only `PlayEndReason.ENDED` caller is Tube (`WatchViewModel.kt:411`); Auto scroll seeks to 0 then scrolls (`feature/feed/…/ui/reels/ReelsScreen.kt:1204-1213`), closing as `swipe_next` (`ReelsViewModel.kt:1089-1090`) and counting a loop (`core/analytics/…/VideoWatchTracker.kt:447-456`).
- **How to reproduce:** Auto scroll on, let a reel finish, inspect the queued `play_end`.
- **Impact:** Completion under-reported, loop_count inflated (feeds reels-27 clamps and ranking).
- **Fix:** `endView(id, ENDED)` from the STATE_ENDED listener before the seek.
- **Acceptance:** ViewModel test: ended → `end_reason=ended`, loop_count unchanged.
- **Effort:** S
- **Was:** V4-9

### reels-42

- **ID:** reels-42
- **Title:** On the web a backgrounded tab keeps ticking and sends heartbeats after `play_end`; on return no new `play_start` is sent and the final `play_end` is skipped.
- **Severity:** P2
- **Category:** analytics integrity
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `src/features/reels/playback/useWatchTelemetry.ts:275-277` (on hidden: play_end, `startedRef=false`, interval keeps running, video not paused); `ReelVideo.tsx:251-253`.
- **How to reproduce:** Play a reel, switch tabs for 60 s, return, swipe.
- **Impact:** Watch time credited while hidden; corrupt sessions.
- **Fix:** Pause and stop the tick when hidden; new session when visible.
- **Acceptance:** Hook test with fake `visibilityState`: no heartbeat after play_end; new play_start on return.
- **Effort:** S
- **Was:** V4-11

### reels-43

- **ID:** reels-43
- **Title:** Web `loop_count` is always 0 and `end_reason` is never "ended" with the default loop preference.
- **Severity:** P2
- **Category:** analytics integrity
- **Evidence level:** SUSPECTED (code and media spec; not run in a browser)
- **Evidence:** `playback/playerPrefs.ts:60` (onEnd "loop") → `video.loop=true` (`ReelVideo.tsx:397`); the native loop fires `seeking`, which nulls `lastTimeRef` (`useWatchTelemetry.ts:218-221`), so the wrap branch (:244-248) never runs; `ended` never fires (:290).
- **How to reproduce:** Watch a 6 s reel three times; `play_end` has loop_count 0 and `swipe_next`.
- **Impact:** As reels-41, on the web.
- **Fix:** In `onSeeking`, count a loop when the last time was within 2 s of the end and currentTime is near 0.
- **Acceptance:** Hook test: timeupdate near end then seeking to 0 → loop_count 1.
- **Effort:** S
- **Was:** V4-12

### reels-44

- **ID:** reels-44
- **Title:** The web reels Follow badge is wrong after about 100 distinct authors: the relationship batch covers only the first 100 ids, so tapping it follows an account already followed.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** `src/hooks/useConnections.ts:510` (`slice(0,100)`); `authorIds` grows with every page (`src/features/reels/components/ReelsScreen.tsx:238-239`); the whole batch refetches when its key changes.
- **How to reproduce:** Swipe past 100 distinct authors, open a reel by someone you follow: badge says Follow.
- **Impact:** Wrong social state on a core action.
- **Fix:** Query relationships for a window around the current index, or per active author.
- **Acceptance:** Hook test with 150 authors: the active author's state comes from a query that includes it.
- **Effort:** S
- **Was:** V4-15 (also the follow-state clause of D-11)

### reels-45

- **ID:** reels-45
- **Title:** Portrait reels are scaled by height, so a 9:16 reel's best rendition is about 404x720 (360p about 202x360), while the master playlist declares `RESOLUTION=1280x720` and `640x360`.
- **Severity:** P2
- **Category:** performance / quality
- **Evidence level:** VERIFIED-READ (ladder cap VERIFIED-RUN; output size not measured, no ffmpeg)
- **Evidence:** `media-service/internal/processing/video.go:205-218` (`scale=-2:%d`), called with 360/480/720 (:401-432); HLS rungs are stream copies (`hls.go:15-34,145-149`); RESOLUTION from a landscape table (`hls.go:75,196-197`); no `-maxrate` (`video.go:184`).
- **How to reproduce:** Run `TestHLSRepackagedReelIsPlayable` with ffmpeg on a 1080x1920 input and ffprobe the 720p output.
- **Impact:** Soft video on the core surface; misleading track selection.
- **Fix:** Scale the short side for short-form; write RESOLUTION from the probed output; add maxrate/bufsize.
- **Acceptance:** Args test for the short-side expression; ffmpeg-tagged test: 720 rung of 1080x1920 has width 720.
- **Effort:** S
- **Was:** D-5

### reels-46

- **ID:** reels-46
- **Title:** Web reel start-up is slow by construction: the next reel is not preloaded, segments are 6 s, and master, child and first segment are fetched in sequence on every swipe.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ (start-up time not measured)
- **Evidence:** Only the active reel is mounted (`src/features/reels/components/ReelsScreen.tsx:796-839`); hls.js created on mount (`ReelVideo.tsx:348-373`); `hlsSegmentSeconds = 6`, keyframes every 2 s (`media-service/internal/processing/video.go:192-196`); playlists `no-store` (`http/handler.go:725`). Android preloads both neighbours (`ReelsScreen.kt:1126-1128`).
- **How to reproduce:** Throttle to Fast 4G and measure swipe-to-playing over 20 reels.
- **Impact:** Visible delay on every swipe; the harness requires preload of next items.
- **Fix:** Mount the next `ReelVideo` paused and muted with a small buffer (or prefetch its playlists); `hls_time 2` for short-form.
- **Acceptance:** Component test: index 0 of [a,b] renders two `ReelVideo`s, one active; args test asserting `-hls_time 2` for a reel plan.
- **Effort:** S (segments) + M (prefetch)
- **Was:** A-3, D-6

### reels-47

- **ID:** reels-47
- **Title:** Every HLS playlist request costs one Postgres read, one un-cached S3 GET of an immutable playlist, one post-service authorisation call over a 2-idle-connection client with no retry or breaker, and one RSA signature per segment; Android triggers 2-4 per swipe.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ (cost); SUSPECTED (saturation point, load test L2)
- **Evidence:** `media-service/internal/service/playback.go:251,271,314`; `delivery/authz.go:171-177` with `cmd/server/main.go:548` passing nil (default transport); `post-service/internal/service/story_surface.go:310-372`; `delivery/sign_cache.go:52-72,184-198` (global mutex, map wiped when full).
- **How to reproduce:** L2; watch `/hls/` p95 and post-service media-access rps.
- **Impact:** Playlist traffic, not segments, becomes the scaling limit of media and post-service.
- **Fix:** In-process LRU of playlist bodies; a pooled HTTP transport for the authorizer; shard or LRU the sign cache.
- **Acceptance:** Unit test: second `GetHLSPlaylist` for an asset does not call `DownloadObject` (fails now).
- **Effort:** S
- **Was:** D-7

### reels-48

- **ID:** reels-48
- **Title:** The Android media cache is keyed on the full signed URL while segment URLs are re-signed every 2 minutes, so replays and swipe-backs download segments again.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** SUSPECTED (no cache key factory is VERIFIED-READ; Media3's default key is library behaviour)
- **Evidence:** `core/media/…/di/MediaModule.kt:51-55,93`; `media-service/internal/delivery/sign_cache.go:54`; `core/media/…/HlsRefresh.kt:56-70`.
- **How to reproduce:** Media3 CacheDataSource test with two URLs differing only in signature parameters.
- **Impact:** Extra mobile data and CDN egress.
- **Fix:** `setCacheKeyFactory` stripping the query for media-host URLs.
- **Acceptance:** One cache entry for both URLs.
- **Effort:** S
- **Was:** D-8

### reels-49

- **ID:** reels-49
- **Title:** Android preloads two neighbours at full quality on any network, data saver does not reduce preload, and choosing Auto quality removes the data-saver cap and forces a second prepare.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ (delegated read; per-neighbour buffering inferred from Media3)
- **Evidence:** `core/media/…/PlayerPool.kt:112,135-138,221-253`; `DataSaver.kt:38,46-55`; `feature/feed/…/ui/reels/ReelsScreen.kt:715-735,1126-1127,1609`.
- **How to reproduce:** Data saver on, choose Auto: selected track height exceeds 480.
- **Impact:** About 20 s of video buffered per swipe; data-saver users get full quality.
- **Fix:** `applyQuality(Auto)` re-applies the cap; on metered networks preload only index+1 with a 2 s buffer.
- **Acceptance:** Unit test: with data saver, Auto keeps `maxVideoHeight = 480` and no second prepare.
- **Effort:** S
- **Was:** D-9

### reels-50

- **ID:** reels-50
- **Title:** On a dropping network Android shows nothing when playback fails, and the web retries a fatal media error without limit and has no retry control after falling back.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:** Android `VideoLoading.kt:111,122,297-302` (IDLE treated as not waiting; no `onPlayerError` UI in Reels). Web `ReelVideo.tsx:364-371` (`recoverMediaError` with no counter), :500,703-704 (error text, no retry).
- **How to reproduce:** Android: cut the network after the buffer drains. Web: serve a corrupt segment.
- **Impact:** Black or frozen reel with no message; busy loop on the web.
- **Fix:** Android: listen for `onPlayerError` and show the Retry row. Web: 2 recovers, then swap codec once, then the file; add Retry.
- **Acceptance:** Compose test: error shows Retry; web test: three fatal media errors → fallback, not a fourth recover.
- **Effort:** S
- **Was:** D-10

### reels-51

- **ID:** reels-51
- **Title:** The web reels stage fires several per-reel requests on every swipe and polls each reel, and its watch telemetry is one POST per event with no batching and an unload path that uses axios instead of a keepalive request, so `play_end` is lost on tab close.
- **Severity:** P2
- **Category:** performance / analytics integrity
- **Evidence level:** VERIFIED-READ
- **Evidence:** `GET /v1/posts/:id` on activation and every 30 s, possibly twice (`useReelLive.ts:12-20`, `commentCache.ts:23-28`); audio-tracks polled every 5 s while pending (`ReelsScreen.tsx:262`); subtitles fetched twice (`ReelsScreen.tsx:270-275`, `useSubtitleTrack.ts:57`). Telemetry: `HEARTBEAT_MS 5000` (`playback/telemetry.ts:15`), one request per event (:88,142,151), failures dropped (:109-115); `sendEventsOnUnload` uses the axios sender (:118-122, wired at `useWatchTelemetry.ts:278`); the API client needs an Authorization header (`src/lib/api.ts:143,154`), so plain `sendBeacon` would fail too.
- **How to reproduce:** Network panel over 20 swipes; watch a reel 8 s and close the tab: heartbeats arrive, play_end does not.
- **Impact:** 4-6 API requests per swipe plus 0.2 rps per viewer; under-counted watch time and wrong end reasons.
- **Fix:** Batch telemetry (flush every 10 s, on swipe, and on pagehide with `fetch(..., {keepalive:true})` as `src/features/webPush/signOut.ts:93` does); one post refetch interval; reuse the probe's subtitle response.
- **Acceptance:** Telemetry test: 6 heartbeats in 30 s → at most 3 POSTs; the unload path calls fetch with `keepalive:true` (fails now).
- **Effort:** S
- **Was:** D-11, V4-10

### reels-52

- **ID:** reels-52
- **Title:** Analytics ingest runs three sequential statements per event in one transaction, writes every heartbeat into an unpartitioned, never-pruned `ingest_receipts`, and puts every `events_raw` row in the DEFAULT partition.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ
- **Evidence:** `analytics-service/internal/store/postgres/events.go:201-249`; `internal/service/ingest.go:172,259-275`; migrations `004_creator_analytics_integrity.sql:15`, `006_full_video_event_ingest.sql:20-36`; `database/setup.sql:13-19`, `cmd/server/main.go:349-352`.
- **How to reproduce:** `pg_stat_statements` on QA during L3.
- **Impact:** About 6,000 write statements per second at 10,000 viewers (arithmetic), on two ever-growing tables.
- **Fix:** Multi-row inserts per batch; a monthly partition job and retention on receipts.
- **Acceptance:** Store test: a 100-event batch executes at most 4 statements (fails now: 300); next month's partition exists after startup.
- **Effort:** M
- **Was:** D-12

### reels-53

- **ID:** reels-53
- **Title:** The reels feed reads the shared home timeline and filters content type in Go, so a viewer whose follows mostly post photos can cost up to 48 sequential Scylla reads per page, and a category chip can run up to 15 full timeline-plus-hydration windows.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ; SUSPECTED for latency (L1)
- **Evidence:** `feed-service/internal/store/scylla/timelines.go:401-514`; `service/feed.go:740-741`; `service/reels_category.go:49,64-71`, `category.go:53`.
- **How to reproduce:** A QA viewer following 200 photo-heavy accounts; `GET /v1/feed/reels?limit=15` and `?category=comedy`; count reads and batch calls.
- **Impact:** Page latency grows with how much non-reel content follows post.
- **Fix:** Cap the while-empty budget at 5 and return the cursor; later a short-form table or clustering column at fan-out.
- **Acceptance:** Service test with 5,000 non-reel rows: bounded reads; a category page makes at most 5 hydrations (fails now: 15).
- **Effort:** S (cap) / L (table)
- **Was:** D-13

### reels-54

- **ID:** reels-54
- **Title:** `GET /v1/posts/trending` (the Reels trending tab) recomputes an engagement score over a full week of public posts of every type on every request, with no cache and no `(content_type, created_at)` index.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** SUSPECTED (query and indexes VERIFIED-READ; the plan not run)
- **Evidence:** `post-service/internal/store/postgres/saved.go:153-167,212-290`; only `idx_posts_not_deleted` (`migrations/007_reels_gold_spec.sql:121`); no cache in `internal/http/hashtag_trending_handler.go:168-188`.
- **How to reproduce:** `EXPLAIN ANALYZE` of `trendingQuery({"flick"}, 20, "", now)` on launch-volume data.
- **Impact:** Each page scans and sorts the week's posts.
- **Fix:** 60 s Redis cache of the first page per type set; `CREATE INDEX CONCURRENTLY … ON posts (content_type, created_at DESC) WHERE deleted_at IS NULL AND visibility='public'`.
- **Acceptance:** EXPLAIN shows an index range scan; handler test: second call within 60 s does not query.
- **Effort:** S
- **Was:** D-14

### reels-55

- **ID:** reels-55
- **Title:** Resumable upload parts (5-64 MiB) travel client → gateway → media-service → S3 instead of going to S3 through presigned part URLs.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/internal/http/resumable_handler.go:110-150` (streamed, relayed); `api-gateway/pkg/edgelimits/edgelimits.go:56-61`; pods 1 CPU / 1 Gi (`values-prod.yaml:18-31`); thresholds Android 100 MiB, web 20 MB.
- **How to reproduce:** L5.
- **Impact:** Large uploads compete with playlist serving on the same pods.
- **Fix:** Return a presigned UploadPart URL per part, keeping session and size checks.
- **Acceptance:** Integration test: a part PUT goes to storage directly and complete still verifies size and ETag.
- **Effort:** M
- **Was:** D-15

### reels-56

- **ID:** reels-56
- **Title:** The worker cancels in-flight ffmpeg immediately on SIGTERM, so the 300 s grace period is unused and every rollout or drain restarts all in-flight transcodes.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/cmd/worker/main.go:161-168`; `internal/processing/tool.go:177`; `transcode_guards.go:138-140`; `transcode_classes.go:244-249`; `charts/atpost-service/templates/worker-deployment.yaml:55,83`.
- **How to reproduce:** SIGTERM the worker mid-transcode: "Context cancelled before commit" at once.
- **Impact:** Lost work per deploy and a zero-worker gap; multi-hour long videos may never finish.
- **Fix:** On SIGTERM stop fetching but let the current job run on its own context until grace − 30 s.
- **Acceptance:** Test: SIGTERM during a 5 s fake job → job completes and commits; a job longer than the budget stays uncommitted.
- **Effort:** S
- **Was:** reels-E-5

### reels-57

- **ID:** reels-57
- **Title:** Nothing restarts a worker whose consumer has hung, and nothing reports it.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** SUSPECTED
- **Evidence:** `charts/atpost-service/values.yaml:248` (`livenessProbe: {}`); `transcode_classes.go:229-256` has no progress signal; gauges unscraped (reels-5).
- **How to reproduce:** On dev, block the worker's Kafka connection: pod stays Running, uploads stay processing.
- **Impact:** A wedged worker holds its partitions until someone notices.
- **Fix:** Track last fetch and last completed job; `/livez` on `:9091` failing after 10 idle-and-stuck minutes; set `worker.livenessProbe`.
- **Acceptance:** Unit test of the handler; on QA a blocked consumer is restarted.
- **Effort:** S
- **Was:** reels-E-6

### reels-58

- **ID:** reels-58
- **Title:** No procedure restores the media database and the media bucket together; restoring one without the other leaves rows without objects or objects nothing will delete.
- **Severity:** P2
- **Category:** data
- **Evidence level:** VERIFIED-READ
- **Evidence:** `infra/terraform/modules/media/main.tf:31-178` (no replication, backup plan or `prevent_destroy`); `docs/runbooks/restore-aurora.md`, `service-rollback.md`, `service-alerts.md` do not mention the bucket; `service/deletion.go:156-226`, `service/orphan_gc.go:38-43`.
- **How to reproduce:** Read the files.
- **Impact:** A restore "succeeds" on row counts and still leaves broken or orphaned media.
- **Fix:** A post-PITR reconciliation section in `restore-aurora.md`; `prevent_destroy` on the bucket; founder decision on cross-region replication.
- **Acceptance:** A dated QA drill: PITR, reconciliation, every ready reel plays.
- **Effort:** M
- **Was:** reels-E-7

### reels-59

- **ID:** reels-59
- **Title:** A reel cannot be traced from the upload request through the outbox, the worker job and post-service's review gate: no request id travels with the event and the worker logs plain text.
- **Severity:** P2
- **Category:** observability
- **Evidence level:** VERIFIED-READ
- **Evidence:** Request id appears in one media-service file only (`internal/http/trending_sounds_handler.go`); the outbox event and gate call carry none; no OTel init although `values-prod.yaml:89` sets the endpoint; 88 `log.Printf` calls in `cmd/worker`.
- **How to reproduce:** Upload with `X-Request-Id: test-1`; search worker and post-service logs.
- **Impact:** "My reel is stuck" means hand-joining three services' logs by media id.
- **Fix:** Request id in the transcode envelope, logged by the worker; shared slog JSON logger; forward `X-Request-Id` on the gate client.
- **Acceptance:** One request id appears in media-service, worker and post-service logs for one upload.
- **Effort:** S
- **Was:** reels-E-8

### reels-60

- **ID:** reels-60
- **Title:** media-service's shutdown cuts resumable chunk uploads (10 s drain against 10-minute request timeouts) and its background workers run on a context that is never cancelled.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/cmd/server/main.go:62,181-235,307,470-486`; no server `terminationGracePeriodSeconds` (default 30 s).
- **How to reproduce:** SIGTERM a pod during a resumable chunk upload.
- **Impact:** Every rollout and scale-down fails in-flight chunks and kills dubbing muxes; clients that resume recover.
- **Fix:** Shared fix from feed E-8; `ShutdownTimeout` ~60 s, grace 75 s; derive worker ctx from a signal context.
- **Acceptance:** Rolling restart on QA during a 500 MB resumable upload completes with no client-visible error.
- **Effort:** S
- **Was:** reels-E-9

### reels-61

- **ID:** reels-61
- **Title:** Every reel playback, public ones included, depends on post-service answering the media-access question; when post-service is down all video answers 503.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/internal/delivery/authz.go` (3 s client); `cmd/server/main.go:167-172`; `internal/http/delivery_viewer.go:34-60`; sign cache softens repeats only (`delivery/sign_cache.go:52-58`).
- **How to reproduce:** Stop post-service on dev and open `/reels`.
- **Impact:** A post-service outage is a full reels outage. Fail-closed is correct; there is no degraded mode.
- **Fix:** Cache positive public-and-approved answers per media id for 60 s, invalidated by visibility events.
- **Acceptance:** Test: public media served from cache within 60 s of a positive answer when post-service errors; a private one never.
- **Effort:** M
- **Was:** reels-E-10

### reels-62

- **ID:** reels-62
- **Title:** The chart's static QA guard fails today on media-service: QA sets `MEDIA_WORKER_MAX_ORIGINAL_BYTES` and prod does not.
- **Severity:** P2
- **Category:** deployment
- **Evidence level:** VERIFIED-RUN
- **Evidence:** `QA_GUARDS_ONLY=1 bash charts/atpost-service/render_test.sh` → "FAIL QA: env drifts from prod: deploy/services/media-service/values-qa.yaml: adds MEDIA_WORKER_MAX_ORIGINAL_BYTES", exit 1 (baseline row 8).
- **How to reproduce:** Run the command.
- **Impact:** The guard is red, so a real drift would not stand out; prod runs on an implicit 16 GiB cap (reels-35).
- **Fix:** Set the variable explicitly in `values-prod.yaml` (value per reels-35).
- **Acceptance:** The command ends without "STATIC GUARDS FAILED".
- **Effort:** S
- **Was:** reels-E-11

### reels-63

- **ID:** reels-63
- **Title:** ffmpeg, which parses every untrusted upload, is installed unpinned from Alpine 3.19 in both media images, and no image scan runs.
- **Severity:** P2
- **Category:** deployment
- **Evidence level:** VERIFIED-READ (Alpine 3.19 end of support from general knowledge; confirm upstream)
- **Evidence:** `media-service/Dockerfile.worker:25-26`, `media-service/Dockerfile:22-26`; `security.yml` runs `trivy fs` only.
- **How to reproduce:** Read the Dockerfiles.
- **Impact:** Two builds of one commit can ship different ffmpeg; no CVE tracking path.
- **Fix:** Supported Alpine pinned by digest, pinned ffmpeg version, `trivy image` for media-service and media-worker in `build-push.yml`.
- **Acceptance:** Dockerfiles reference `@sha256` and a versioned ffmpeg; the build log shows a trivy image result.
- **Effort:** S
- **Was:** reels-E-12

### reels-64

- **ID:** reels-64
- **Title:** The consumer that releases a reel from review (pending → approved on ready+passed, → rejected on failed or scan-rejected) has no behaviour test, including the case of a post deleted while its video processes.
- **Severity:** P2 (was P1; v1: a regression approving unscanned media is still caught by the tested read-time gates; the real exposure is a regression that never releases reels)
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (`go test -list` in `post-service/internal/consumers`: 7 tests, none for the gate)
- **Evidence:** The only `media.go` test (`media_cover_guard_test.go:15`) greps source and covers the voice release; `FlipReviewStatusFromPending` appears once in `store/postgres/post_restrictions_integration_test.go:530`; no deleted-post case.
- **How to reproduce:** `grep -rn "finalizeReviewGate" --include=*_test.go Architecture/services/post-service` (none).
- **Impact:** A change that never releases reels, or flips a deleted post, passes CI.
- **Fix:** Table test `internal/consumers/media_review_gate_test.go` with a fake store: (ready,passed) → approved + audit row + cache delete; (ready,rejected) → rejected; (failed,*) → rejected; already approved, deleted or cover-only → untouched; flip error → handler error (with reels-1).
- **Acceptance:** Mutating `finalizeReviewGate` to approve on manual_review, or to ignore `deleted_at`, fails the test.
- **Effort:** S
- **Was:** reels-F-2, reels-F-13

### reels-65

- **ID:** reels-65
- **Title:** The worker's orchestration function `processMessage` (lock, inbox, guards, encode, scan, complete) is called by no test.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** No `processMessage(` in `media-service/cmd/worker/*_test.go`; step order in `main.go:260-482` is unpinned.
- **How to reproduce:** The grep.
- **Impact:** Reordering or dropping a guard passes every test.
- **Fix:** `process_message_test.go` with fakes: a limits refusal never calls the encoder; a scanner error never yields passed; a deleted asset is skipped.
- **Acceptance:** Deleting the `CheckVideoLimits` call fails the test.
- **Effort:** M
- **Was:** reels-F-4

### reels-66

- **ID:** reels-66
- **Title:** Rejection of bad video files is tested only with text bytes or hand-built probe structs; no test sends a real wrong-container, over-long or video-less file through confirm and the worker.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `Architecture/tools/integration/media_e2e_test.go:149`; `media-service/cmd/worker/transcode_guards_test.go:477`; `internal/processing/reels_hardening_test.go:139`.
- **How to reproduce:** Read the tests.
- **Impact:** A regression in the sniffer or ffprobe parsing lets disguised or over-long files through.
- **Fix:** ffmpeg-gated tests (mandatory after reels-12): AVI renamed .mp4, audio-only MP4, a clip over a lowered max duration; add the AVI case to the e2e confirm flow.
- **Acceptance:** Each case is refused with its reason code.
- **Effort:** S
- **Was:** reels-F-6

### reels-67

- **ID:** reels-67
- **Title:** No test shows that a reel made private or deleted stops playing from an already fetched playlist, and media-service HTTP denial tests do not cover GET of an HLS playlist.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** Gate decision tests exist (`post-service/internal/service/media_access_gate_test.go`, `media_access_anonymous_integration_test.go:118-123`, `media-service/internal/http/handler_head_test.go:482-527`, `record_read_handler_test.go:382,387`); segment window up to 12 h (`delivery/sign_cache_test.go:127`; reels-17).
- **How to reproduce:** Read the files.
- **Impact:** The exposure window is undocumented and untested.
- **Fix:** HTTP test: stranger `GET /v1/media/:id/hls/720p.m3u8` on a private asset → 404, and after the gate flips to deny a second GET → 404.
- **Acceptance:** Fails when the playlist route skips the gate.
- **Effort:** S
- **Was:** reels-F-8

### reels-68

- **ID:** reels-68
- **Title:** Report → moderation is tested in pieces, but no test follows a reported reel to a decision and checks it is gone from the reels feed and refused by the media gate.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `trust-safety-service/internal/service/report_entities_integration_test.go:65`; `post-service/internal/http/admin_token_integration_test.go:110,217`; Android `PostMoreViewModelTest.kt:557,575`. reels-29 shows resolution itself does nothing.
- **How to reproduce:** Read the files.
- **Impact:** Journey 6 can break between services unnoticed.
- **Fix:** post-service integration test: a "remove" decision on a flick → stranger denied by `CanViewMedia` and absent from the reels listing; extend the reels-10 e2e flow with report and decision.
- **Acceptance:** Fails when the decision handler skips flicks.
- **Effort:** M
- **Was:** reels-F-9

### reels-69

- **ID:** reels-69
- **Title:** The Kafka half of the worker's durability (offset committed only after the quarantine row is written) is tested only by a test that needs `KAFKA_BROKERS`, which no CI job provides.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/cmd/worker/kafka_durability_integration_test.go:22-26`; `scripts/ci/integration-services.yaml:64-65`.
- **How to reproduce:** Read the files.
- **Impact:** A regression committing before quarantine loses poison requests silently.
- **Fix:** Start Redpanda for the media entry and set `KAFKA_BROKERS`.
- **Acceptance:** The test shows PASS in a `-v` run of the media integration job.
- **Effort:** S
- **Was:** reels-F-10

### reels-70

- **ID:** reels-70
- **Title:** The integration CI logs print one PASS line per module, so a test that silently skips on a missing variable is indistinguishable from one that ran.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (jobs of run 44008075845 read)
- **Evidence:** `scripts/verify/go.sh:168` runs without `-v`/`-json`; logs show "PASS integration media-service (18s)" only.
- **How to reproduce:** `gh run view --job 133788443047 --log`.
- **Impact:** Lease, durability, view-dedup and gate integration tests cannot be shown to have run.
- **Fix:** `-json` output uploaded as an artefact; fail on "not set" skips in manifest modules.
- **Acceptance:** The artefact lists `TestTranscodeLeaseSweepSelection`, `TestLiveIngestPersistsEveryEventTypeAndDedupesOnReplay`, `TestAnonymousMediaAgainstPostgres` as pass.
- **Effort:** S
- **Was:** reels-F-12

---

## P3

### reels-71

- **ID:** reels-71
- **Title:** The create-time video gate approves on 'ready' with any moderation status but 'rejected' and writes an 'approved' audit row, and the worker's "no verdict → manual_review" branch is dead code; neither can publish unscanned video, because read-time gates require ready+passed.
- **Severity:** P3 (A-1 and C-2 were P1; both DISPROVED as publication bugs by v1)
- **Category:** data / defence in depth
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/service/post_review.go:161-174`; audit `post_create.go:592-594`; `create_guards.go:194-203` accepts manual_review on purpose. v1: `runTranscode` returns nil only after passed or rejected (`media-service/cmd/worker/main.go:994-1015`); manual_review is written only with failed (:392-396, `store/postgres/transcode_lease.go:446`). Read gates: `post-service/internal/service/processing.go:39-95`, `feed-service/internal/service/processingfilter.go:32`, `story_surface.go:252-270`. C-2's "setter has no caller" is false (`media-service/internal/service/upload.go:575`); failed+manual_review assets have no reviewer UI (their posts are rejected, reels-31).
- **How to reproduce:** Seed media ready+manual_review and create a flick: the row is 'approved' but stays author-only and unplayable to others.
- **Impact:** Audit inaccuracy; one layer of defence instead of two; the gate has no test.
- **Fix:** Approve only for passed/approved, rejected → rejected, else pending; remove or fail-close the dead worker branch.
- **Acceptance:** Table test over (ready × passed/approved/rejected/manual_review/pending/""): manual_review and pending → pending.
- **Effort:** S
- **Was:** A-1, C-2

### reels-72

- **ID:** reels-72
- **Title:** The author can clear the AI-generated and altered-content labels after publish (policy question for the founder).
- **Severity:** P3
- **Category:** policy / integrity
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `post-service/internal/http/post_edit.go:80,95`; `store/postgres/post_edit.go:175,190`.
- **How to reproduce:** PATCH a published reel with `ai_generated:false`: accepted.
- **Impact:** Disclosure can be removed after viewers saw the reel.
- **Fix:** If one-way, 409 on true→false for a published post.
- **Acceptance:** A clearing PATCH → 409.
- **Effort:** S
- **Was:** V4-P3-1

### reels-73

- **ID:** reels-73
- **Title:** Profile counts include private, pending, scheduled and members-only reels for any viewer.
- **Severity:** P3
- **Category:** privacy
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `post-service/internal/store/postgres/posts.go:1155-1161`; `service/post_reads.go:540-546`.
- **How to reproduce:** As a stranger, compare `/v1/posts/by-author/:id/counts` with the list length.
- **Impact:** Leaks the existence of hidden reels.
- **Fix:** Non-author counts use the list's filters.
- **Acceptance:** A stranger's count equals the list length.
- **Effort:** S
- **Was:** V4-P3-2

### reels-74

- **ID:** reels-74
- **Title:** Mention notifications go out while the reel is pending and are not removed on takedown, rejection or delete.
- **Severity:** P3
- **Category:** privacy
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `post-service/internal/service/schedule.go:257-265`; `notification-service/internal/service/note_consumer.go:147`.
- **How to reproduce:** Mention B in a reel that is later rejected; B's notice stays.
- **Impact:** Notifications about content that never went live.
- **Fix:** Emit mentions on approval; delete mention rows on takedown or delete.
- **Acceptance:** A pending post emits no mention.
- **Effort:** S
- **Was:** V4-P3-3

### reels-75

- **ID:** reels-75
- **Title:** Neither client tells the creator about a moderation hold or rejection of a published reel: the web always says "published", and Android shows raw status codes before create.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** Web `src/features/posttube/hub/hubApi.ts:188-225`, `useUploadStudio.ts:700-702`; Android `feature/tube/…/ui/hub/CreatorHubModels.kt:174-260`, `createhub/ReelMediaUploads.kt:250`.
- **How to reproduce:** Reject a reel through the admin console; the Hub row does not say so.
- **Impact:** Creators cannot act on rejections (compounds reels-1 and reels-31).
- **Fix:** Map post review status to Hub flags; plain wording.
- **Acceptance:** A rejected reel's Hub row shows "Rejected".
- **Effort:** S
- **Was:** V4-P3-5

### reels-76

- **ID:** reels-76
- **Title:** The web proxy forwards a browser-supplied `x-user-id`, follows redirects to any origin server-side, relays Set-Cookie from them, and logs 500 characters of upstream error bodies.
- **Severity:** P3
- **Category:** authn / hardening
- **Evidence level:** VERIFIED-READ
- **Evidence:** `src/app/api/proxy/[...path]/route.ts:33-36,121,190-200` (`FORWARDED_HEADERS` includes `x-user-id`); `storageRoute.ts:100-110`. Safe today only because the gateway strips the header.
- **How to reproduce:** Send `X-User-Id` to `/v1/...` on the web host and inspect the upstream request.
- **Impact:** An identity forgery if `API_GATEWAY_URL` ever points at a service directly (reels-6 trusts the header).
- **Fix:** Drop `x-user-id`; allow-list redirect hosts; strip Set-Cookie on non-gateway hops.
- **Acceptance:** Route tests for each.
- **Effort:** S
- **Was:** REELS-B-9, V4-P3-9

### reels-77

- **ID:** reels-77
- **Title:** The media cookie reaches every state-changing `/v1/media` route, contrary to its comment; CSRF protection rests on SameSite=Lax alone.
- **Severity:** P3
- **Category:** input
- **Evidence level:** VERIFIED-READ
- **Evidence:** `src/app/api/auth/_lib/session.ts:48-54,119-128`; `media-service/internal/http/middleware.go:159-166`.
- **How to reproduce:** POST to `/v1/media/init` with only the cookie: accepted.
- **Impact:** Same-site subdomains are not blocked.
- **Fix:** Accept the cookie only on GET/HEAD.
- **Acceptance:** Middleware test: POST with cookie and no Authorization → 401.
- **Effort:** S
- **Was:** REELS-B-10

### reels-78

- **ID:** reels-78
- **Title:** Many 500 responses return the raw Go error text to the client.
- **Severity:** P3
- **Category:** input
- **Evidence level:** VERIFIED-READ
- **Evidence:** media-service `http/handler.go:555`, `renditions_handler.go:61`, `clips_handler.go:112,137,167,210`, `audio_handler.go:239`; post-service `http/drafts.go:70,145,174,200`, `reel_discovery.go:37,74,89`, `hashtag_trending_handler.go:184`.
- **How to reproduce:** Fail the store under a handler test.
- **Impact:** SQL, constraint text and hostnames can leak.
- **Fix:** Log the error and answer a fixed message (as `audio_handler.go:184`).
- **Acceptance:** Handler test with a failing store: body contains no error text.
- **Effort:** S
- **Was:** REELS-B-11 (plus the D-14 note)

### reels-79

- **ID:** reels-79
- **Title:** `GET /v1/reels/slug/:slug` answers the reel id for any slug with no visibility check.
- **Severity:** P3
- **Category:** authz
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/http/reel_discovery.go:43-60`; every content read is gated, so this confirms existence only.
- **How to reproduce:** Look up a private reel's slug as a stranger: id returned.
- **Impact:** Existence oracle.
- **Fix:** Pass the viewer; 404 unless `viewerMayViewPost` allows.
- **Acceptance:** Handler test: a private reel's slug → 404 to a stranger.
- **Effort:** S
- **Was:** REELS-B-12

### reels-80

- **ID:** reels-80
- **Title:** Report errors are generic on both clients (429 and 400 say "try again") and `details` is unbounded on the server.
- **Severity:** P3
- **Category:** functional / input
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** Web `src/features/reels/components/ReelReportDialog.tsx:46-53`; `trust-safety-service/database/migrations/001_initial.sql:11`.
- **How to reproduce:** Exceed the 20/h quota: the dialog says "try again".
- **Impact:** Confusing reporter experience; large bodies stored.
- **Fix:** Map 429/400 to their own messages; cap `details` at 2,000 characters.
- **Acceptance:** A 429 shows the quota text; a long body → 400.
- **Effort:** S
- **Was:** V4-P3-11

### reels-81

- **ID:** reels-81
- **Title:** Latent config switches can silently weaken production: image scanning defaults off with no production refusal, `ANALYTICS_VIEW_CAP_PER_VIEWER_PER_DAY ≤ 0` disables the paid-view cap, and the internal-key check matches `DEPLOY_ENV == "production"` exactly.
- **Severity:** P3
- **Category:** config
- **Evidence level:** VERIFIED-READ (one reader); prod values set safe values today
- **Evidence:** `media-service/internal/config/config.go:86`, `internal/service/media.go:186-187`; `analytics-service/internal/aggregation/daily.go:122-134`; `media-service/cmd/server/main.go:281`.
- **How to reproduce:** Boot with each value unset or zero in a production-like env: no refusal.
- **Impact:** One config slip disables a safety control.
- **Fix:** Refuse each at boot in production.
- **Acceptance:** Boot tests.
- **Effort:** S
- **Was:** V4-P3-18

### reels-82

- **ID:** reels-82
- **Title:** On the web the outgoing reel stays mounted and playing for the 0.22 s exit animation, so two videos (and two added sounds) can be audible during a swipe.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** SUSPECTED (needs a browser)
- **Evidence:** `src/features/reels/components/ReelsScreen.tsx:796-803,818-821`; `ReelVideo.tsx:377-385`.
- **How to reproduce:** Swipe quickly between two reels with sound.
- **Impact:** Brief audio overlap; the harness asks for none.
- **Fix:** Pause and mute the exiting `ReelVideo` at exit start.
- **Acceptance:** Component test: the previous video is paused before the exit completes.
- **Effort:** S
- **Was:** A-8

### reels-83

- **ID:** reels-83
- **Title:** Android cannot add captions to a reel (no subtitle upload or auto-caption request), which the web offers at publish.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** No `v1/subtitles` call under `mobile/android/*/src/main`; web `useUploadStudio.ts:584-593`; server `clips_handler.go:68-74`.
- **How to reproduce:** Look for a captions option in the Android reel form.
- **Impact:** Android gap.
- **Fix:** `POST /v1/subtitles/:mediaId/request` after confirm, behind a switch.
- **Acceptance:** API contract test.
- **Effort:** S
- **Was:** A-9

### reels-84

- **ID:** reels-84
- **Title:** Discarding a reel on Android after its video was confirmed leaves the original and transcodes in storage with no owner post.
- **Severity:** P3
- **Category:** data
- **Evidence level:** SUSPECTED (the post-service orphan draft media cleanup was not read)
- **Evidence:** `feature/post/…/createhub/ReelPublishWorker.kt:269-288` (no `DELETE /v1/media/:id`); `media-service/internal/service/orphan_gc.go:38-43` (pending_upload only).
- **How to reproduce:** Post, then Discard while Processing.
- **Impact:** Storage and transcode cost.
- **Fix:** `DELETE /v1/media/:confirmedVideoId` on discard when no post id exists.
- **Acceptance:** Controller test: discard with confirmedVideoId and no postId calls delete once.
- **Effort:** S
- **Was:** A-10

### reels-85

- **ID:** reels-85
- **Title:** On Android an offline copy survives deleting the reel and can still open it.
- **Severity:** P3
- **Category:** data / privacy
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `PostMoreViewModel.kt:341-361`, `ReelsViewModel.kt:619-622`.
- **How to reproduce:** Save offline, delete the reel, open the offline page.
- **Impact:** Deleted content stays playable on the device.
- **Fix:** `offline.remove(id)` on delete.
- **Acceptance:** Delete → offline entry gone.
- **Effort:** S
- **Was:** V4-P3-6

### reels-86

- **ID:** reels-86
- **Title:** On Android the owner cannot pin, heart or delete comments on the pinned top reel because `postAuthorId` is empty there.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `feature/feed/…/ui/reels/ReelsScreen.kt:376-377`, `CommentMenu.kt:56`.
- **How to reproduce:** Open comments on your own top reel: no creator menu.
- **Impact:** Creator moderation tools missing on one surface.
- **Fix:** Resolve the author from `head` as well.
- **Acceptance:** Owner sees the creator menu on the top reel.
- **Effort:** S
- **Was:** V4-P3-7

### reels-87

- **ID:** reels-87
- **Title:** On Android, swiping onto a reel with no playback leaves the previous view session open, and a Follow is credited to it.
- **Severity:** P3
- **Category:** analytics integrity
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `ReelsViewModel.kt:1034,1045,1088`.
- **How to reproduce:** Swipe onto a reel whose probe is null, then Follow.
- **Impact:** Wrong attribution in analytics.
- **Fix:** End the session before the `probe == null` return; check `contentId`.
- **Acceptance:** ViewModel test.
- **Effort:** S
- **Was:** V4-P3-8

### reels-88

- **ID:** reels-88
- **Title:** Web reel engagement fails silently and races the live poll: like/save failures are silent, "Saved" shows before the result, no `isPending` guard or `cancelQueries`, share is counted when the sheet opens, and not-interested has no rollback.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `src/features/reels/hooks/useReelEngagement.ts:44-91`, `ReelsScreen.tsx:298-307`, `useReelLive.ts:24-30`.
- **How to reproduce:** Like with the network offline: the heart stays filled then silently reverts.
- **Impact:** Users are not told when an action failed.
- **Fix:** Error toasts, `cancelQueries` in `onMutate`, record the share on the target.
- **Acceptance:** Hook tests.
- **Effort:** S
- **Was:** V4-P3-10

### reels-89

- **ID:** reels-89
- **Title:** The web hashtag Reels tab asks for `content_type=flick` only, while the stage accepts `reel` and `short` too.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `src/app/hashtag/[tag]/HashtagClient.tsx:30`, `src/features/reels/model.ts:18`.
- **How to reproduce:** Open a hashtag with legacy `reel` posts.
- **Impact:** Legacy reels missing from the tab.
- **Fix:** Send all accepted types.
- **Acceptance:** The request includes `reel`.
- **Effort:** S
- **Was:** V4-P3-12

### reels-90

- **ID:** reels-90
- **Title:** On the web, Hub edit, Hub delete and a new publish leave the `["reels", …]` caches stale.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `useHub.ts:368-410`, `useUploadStudio.ts:713-717`.
- **How to reproduce:** Edit a caption in the Hub, return to `/reels`: old caption.
- **Impact:** Stale content until refetch.
- **Fix:** Invalidate or patch the `["reels"]` and `["hub","library"]` keys.
- **Acceptance:** Cache test.
- **Effort:** S
- **Was:** V4-P3-13

### reels-91

- **ID:** reels-91
- **Title:** The web reels list has no cross-page de-dup and no `maxPages`.
- **Severity:** P3
- **Category:** functional / performance
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `src/features/reels/components/ReelsScreen.tsx:166-174,196-206`.
- **How to reproduce:** Two overlapping pages show the same reel twice.
- **Impact:** Possible repeats in the 50-swipe journey; item memory grows (one player only).
- **Fix:** De-dup by id in the memo; set `maxPages`.
- **Acceptance:** Two overlapping pages → unique ids.
- **Effort:** S
- **Was:** V4-P3-14

### reels-92

- **ID:** reels-92
- **Title:** Deleting a media asset (owner, orphan GC, purge) deletes its resumable session rows without aborting the storage multipart upload.
- **Severity:** P3
- **Category:** data
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/internal/store/postgres/asset_purge.go:161-163`, `purge.go:89-90,103-105`; `setup.sql:83`; `store/postgres/resumable.go:104-131`.
- **How to reproduce:** Delete an asset with an open resumable session; list multipart uploads.
- **Impact:** Storage cost; AWS aborts after 7 days, MinIO never.
- **Fix:** Abort the multipart (or record `storage_upload_id` for reclaim) first.
- **Acceptance:** Test: delete → multipart aborted.
- **Effort:** S
- **Was:** C-9

### reels-93

- **ID:** reels-93
- **Title:** A late or re-sent chunk can move a completed resumable session back to 'uploading', and 'aborted' sessions still accept chunks.
- **Severity:** P3
- **Category:** data
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/internal/service/resumable.go:247-252`; `store/postgres/resumable.go:61-67`.
- **How to reproduce:** Re-send a chunk after complete; read the status.
- **Impact:** Wrong status to a resuming client; no upload lost.
- **Fix:** Conditional progress update and chunk refusal outside initiated/uploading.
- **Acceptance:** Test: chunk after complete → refused, status stays completed.
- **Effort:** S
- **Was:** C-10

### reels-94

- **ID:** reels-94
- **Title:** Migration 008's CHECK on `moderation_status` is a no-op that hides the real value set; a schema built from migrations alone would fail every manual_review write.
- **Severity:** P3
- **Category:** data
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/internal/store/postgres/bootstrap.go:81-87`, `database/setup.sql:57`, `migrations/008_media_moderation.sql:4-6`; values written incl. `manual_review` and voice values (`service/voice.go:31`).
- **How to reproduce:** Build the schema from migrations only and complete a failed transcode.
- **Impact:** Schema does not state the truth.
- **Fix:** A migration dropping any such CHECK and adding one listing every written value `NOT VALID`.
- **Acceptance:** Migration test on both build orders.
- **Effort:** S
- **Was:** C-12

### reels-95

- **ID:** reels-95
- **Title:** `media.copyright.pairs` has no consumer; enabling `COPYRIGHT_PAIR_RELAY` would mark pair rows published while nothing acts on them.
- **Severity:** P3
- **Category:** data
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/cmd/server/main.go:192-197`; no reader in any service; `infra/terraform/modules/msk/topics-job/variables.tf:40`.
- **How to reproduce:** grep for the topic across services.
- **Impact:** Silent loss once the switch is flipped.
- **Fix:** Keep it off until trust-safety has a consumer; boot refusal when on with no declared consumer.
- **Acceptance:** Boot test.
- **Effort:** S
- **Was:** C-13

### reels-96

- **ID:** reels-96
- **Title:** The transcode retry window is about 4 minutes, not the "quarter of an hour" its comment says, so a storage outage of about an hour permanently fails in-flight uploads.
- **Severity:** P3
- **Category:** reliability
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `media-service/cmd/worker/transcode_guards.go:54-60,130-167`.
- **How to reproduce:** Sum the backoff schedule.
- **Impact:** Uploads fail during long blob outages.
- **Fix:** Raise `MEDIA_TRANSCODE_MAX_ATTEMPTS` to about 12, or fix the comment.
- **Acceptance:** Backoff test sums to about 15 minutes.
- **Effort:** S
- **Was:** V4-P3-15

### reels-97

- **ID:** reels-97
- **Title:** Cleanup of an asset deleted mid-transcode is best effort and writes no reclaim row, so late-uploaded segments can leak.
- **Severity:** P3
- **Category:** data
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `media-service/cmd/worker/transcode_guards.go:368-393`.
- **How to reproduce:** Delete an asset while its HLS upload is in flight; list the prefix afterwards.
- **Impact:** Storage leak.
- **Fix:** Insert the prefix into `media_blob_reclaim`.
- **Acceptance:** Worker test: gone asset → reclaim row.
- **Effort:** S
- **Was:** V4-P3-16

### reels-98

- **ID:** reels-98
- **Title:** Scheduled posts retry every 30 s for ever on a persistent non-standing error, and a parked post or blocked draft is not notified to its author.
- **Severity:** P3
- **Category:** reliability
- **Evidence level:** VERIFIED-READ (one reader)
- **Evidence:** `post-service/internal/postschedule/worker.go:121-130`, `service/schedule.go:407-435`, `reel_draft_queue.go:43-74`.
- **How to reproduce:** Schedule a post whose publish always fails.
- **Impact:** Log noise and a creator who never learns the post failed.
- **Fix:** Per-item backoff and park after N (as migration 079 does for drafts); notify.
- **Acceptance:** Worker test: N failures → parked.
- **Effort:** S
- **Was:** V4-P3-17

### reels-99

- **ID:** reels-99
- **Title:** The web `/v1` proxy follows media redirects itself and streams the bytes, and buffers 64 MiB upload parts, in any build whose API base is empty; production builds bypass it today, but a deploy comment says the opposite.
- **Severity:** P3 (was P1; v2 disproved it for production)
- **Category:** performance / config
- **Evidence level:** VERIFIED-READ
- **Evidence:** `src/app/api/proxy/[...path]/route.ts:28-31,98-101,179-203`; `next.config.ts:13-19`. Bypass: `src/features/reels/model.ts:187-195` (`mediaHref`), `src/lib/api.ts:142`, `.github/workflows/build-web.yml:103-110,154`. Contradiction: `deploy/web/app/values-prod.yaml:8` says the prod build must pass `NEXT_PUBLIC_API_BASE_URL=""`; compose uses `""` (`Architecture/docker/docker-compose.yml:2985`).
- **How to reproduce:** Build with an empty API base and open `/reels`: media is served from the site origin.
- **Impact:** None in prod as built; returns at P1 if someone follows the values comment.
- **Fix:** Correct the values comment (or the build check) so they agree; optionally return 307s for off-gateway redirects.
- **Acceptance:** The comment and `build-web.yml` agree; a route test returns 307 for a CDN redirect when no storage route is set.
- **Effort:** S
- **Was:** D-2

### reels-100

- **ID:** reels-100
- **Title:** On Android the settled reel page recomposes four times a second, a cover URL is resolved per row although Reels draws no poster, and appending a page re-runs `onReelShown` and un-pauses a paused reel.
- **Severity:** P3
- **Category:** performance / functional
- **Evidence level:** VERIFIED-READ (delegated); cover lookup on the Reels surface SUSPECTED
- **Evidence:** `feature/feed/…/ui/reels/ReelsScreen.kt:1110,1511,1534,1634-1636,1674-1679,1906`; `HashtagPostHydrator.kt:73-81`; `ReelsViewModel.kt:1058`.
- **How to reproduce:** Pause a reel near the end of a page and wait for the next page.
- **Impact:** Wasted work and battery; a pause undone.
- **Fix:** Read progress in a child composable; skip cover resolution for Reels; key the effect on the settled page only.
- **Acceptance:** ViewModel test: paused stays true when pageCount changes.
- **Effort:** S
- **Was:** D-16

### reels-101

- **ID:** reels-101
- **Title:** Web upload processing is polled every 3 s with no maximum duration or attempt count, and errors are swallowed.
- **Severity:** P3
- **Category:** performance
- **Evidence level:** VERIFIED-READ (delegated)
- **Evidence:** `src/features/upload/useUploadStudio.ts:287-312`; `reelsApi.ts:217`.
- **How to reproduce:** Upload a reel whose transcode is quarantined and leave the tab open.
- **Impact:** Unbounded requests during a backlog and no message.
- **Fix:** Back off to 10 s after a minute; stop after 30 minutes with a message.
- **Acceptance:** Hook test with fake timers.
- **Effort:** S
- **Was:** D-17

### reels-102

- **ID:** reels-102
- **Title:** `/v1/clips` is registered but unreachable through the gateway, and `/v1/series` is routed but used by no client.
- **Severity:** P3
- **Category:** deployment
- **Evidence level:** VERIFIED-READ
- **Evidence:** `api-gateway/cmd/server/main.go:563-573`, `internalroutes.go:391`; `post-service/internal/http/handler.go:347-357`; `media-service/internal/http/clips_handler.go:52-55`. Map open question 11 was wrong about `/v1/series`.
- **How to reproduce:** grep both trees for callers.
- **Impact:** Dead surface; `/v1/clips` reachable in-cluster (reels-6).
- **Fix:** Remove `/v1/clips`; record `/v1/series` as server-only.
- **Acceptance:** Route table test no longer lists `/v1/clips`.
- **Effort:** S
- **Was:** reels-E-14

### reels-103

- **ID:** reels-103
- **Title:** The media-service PodDisruptionBudget also selects the worker pods, so a drain can evict both API pods while a worker pod satisfies `minAvailable`.
- **Severity:** P3
- **Category:** deployment
- **Evidence level:** SUSPECTED (chart not rendered)
- **Evidence:** `charts/atpost-service/templates/pdb.yaml:13-16`, `_helpers.tpl:29-32`, `worker-deployment.yaml:58-60`; `values-prod.yaml:146-148`.
- **How to reproduce:** Render the chart; `kubectl get pdb media-service -o yaml` on QA shows 4 expected pods.
- **Impact:** A drain can leave zero API pods.
- **Fix:** `component: server` label on the server pods and in PDB, Service and ServiceMonitor selectors (before first deploy); a worker PDB.
- **Acceptance:** `render_test` asserts selectors exclude `component=worker`.
- **Effort:** S
- **Was:** reels-E-15

### reels-104

- **ID:** reels-104
- **Title:** Every media-service server replica runs the outbox relay without a claim, so each transcode request can be published more than once.
- **Severity:** P3
- **Category:** reliability
- **Evidence level:** SUSPECTED (`ListUnpublishedMediaEvents` not read in full)
- **Evidence:** `media-service/internal/service/media_outbox.go:14-56`; no `SKIP LOCKED` in `store/postgres/media_outbox.go`; consumers dedupe by event id.
- **How to reproduce:** Count messages per event id on dev with two replicas.
- **Impact:** Duplicate traffic and log noise; no duplicate work found.
- **Fix:** `FOR UPDATE SKIP LOCKED` inside the relay pass.
- **Acceptance:** Integration test with two relays: each row published once.
- **Effort:** S
- **Was:** reels-E-16

### reels-105

- **ID:** reels-105
- **Title:** The web upload studio's "processing failed" and stuck-processing branches have no test.
- **Severity:** P3
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `src/features/upload/useUploadStudio.ts:292-303,332-342`; no test hits under `features/upload`.
- **How to reproduce:** grep the web tests.
- **Impact:** A creator could be left on a spinner.
- **Fix:** Reducer test with a failed renditions response and one that never becomes `all_ready`.
- **Acceptance:** Both render the error message.
- **Effort:** S
- **Was:** reels-F-14

---

## Out of scope observations

- A feed-composer video (`content_type="post"`) skips the video publish gate at create (`post-service/internal/service/post_create.go:494`, `post_content_types.go:170-176`); feed module.
- trust-safety `GET /v1/media-labels/:mediaId` has no role check (`trust_extras_handler.go:200`); no gateway prefix, in-cluster only.
- `dangerouslySetInnerHTML` on user content in web QA and community pages (`app/qa/questions/[id]/page.tsx:158`, `components/communities/CommunityPostCard.tsx:312`, `CommunityWikiTab.tsx:119`, `CommunityQASettingsTab.tsx:167`); not reels.
- `POST /v1/media/studio/stickers/:id/use` is public and starts a goroutine per call (`media-service/internal/http/studio_handler.go:28,194`).
- media-service server pods run ffmpeg under a `/tmp` emptyDir with no `sizeLimit` (chart `deployment.yaml` volumes); noted by Track E, not scored.
- One invalid event rejects a whole analytics batch, which Android retries 5 times and then drops (`analytics-service/internal/service/ingest.go:186-205`); noted by Track D, not scored.
- Web still sends `publish_to_feed: true` for a Short (`features/upload/types.ts:185`); harmless to the home read.
- Prod values use `MEDIA_CDN_BASE_URL=https://media.cleestudio.com` while `media_custom_domain_enabled` defaults false (`deploy/services/media-service/values-prod.yaml:102`, `infra/terraform/envs/prod/variables.tf:35-43`); no track raised or verified it, and signed URLs would point at an unresolved host if the flag stays off. Check before launch.
- `FEED_INDEX_LEGACY_READ: "true"` in feed-service prod values (`deploy/services/feed-service/values-prod.yaml:78`); feed module, founder to confirm.
- Git history and container images were never secret-scanned (gitleaks and trufflehog not installed); the tracked working tree is clean (V4 Part 2a).
