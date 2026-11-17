# Long video (PostTube / MTube): findings

- **Audit date:** 2026-11-17. Code: `codex/module-01-02-launch-safety` @ `7765e81a7` (the commits since `770f5ed9d` change docs only; dirty working tree, read as it is). Web: `C:\workspace\wt\pb-echo`, `feat/echo-web` @ `ca96921`.
- **Sources:** six Stage 2 tracks (`tracks/track-a…f`), three Stage 3 verifiers (`verify/v1-worker-pipeline.md`, `v2-notify-visibility-tests.md`, `v3-gapfill-pass.md`) and the audit lead's re-check of LV-V3-1. Where a verifier disagreed with a track, the verifier's verdict, severity and evidence level are used, except where the lead decided otherwise (stated in "Merges and verifier changes").
- **Nothing was executed against a running stack.** No database, server, device, browser or deployed environment was used (repository rules forbid it). VERIFIED-RUN means a unit test, a scratch program outside the repo, a chart guard, a CI log or a repository command.
- **IDs:** `lv-N` is the final ID. Each finding has a `Was:` line listing the original IDs (LV-A = functional, LV-B = security, LV-C = data, LV-D = performance, LV-E = reliability and deployment, LV-F = tests, LV-V3 = gap-fill verifier, v2 FQ = verifier v2 founder question). 90 original IDs were merged down to 77 findings.
- **Inherited reels findings** (`reels-N`, same upload and transcode pipeline) are listed in their own section and are **not** counted in the long-video totals.
- **Paths:** backend paths are relative to `Architecture/services/` unless they start with another top-level folder (`infra/`, `deploy/`, `charts/`, `identity-platform/`, `scripts/`, `.github/`). Android paths are relative to `mobile/android/`; `T/` = `feature/tube/src/main/kotlin/com/us/android/feature/tube/`, `CH/` = `feature/post/src/main/kotlin/com/us/android/feature/post/createhub/`, and `…/` stands for a module's `src/main/kotlin/com/us/android/` root. Web paths are relative to `C:\workspace\wt\pb-echo`.

## Counts

| Severity | Count | VERIFIED-RUN | VERIFIED-READ | SUSPECTED | UNVERIFIED |
|---|---|---|---|---|---|
| P0 | 0 | 0 | 0 | 0 | 0 |
| P1 | 11 | 2 | 9 | 0 | 0 |
| P2 | 31 | 3 | 27 | 1 | 0 |
| P3 | 35 | 4 | 28 | 3 | 0 |
| **Total (long video)** | **77** | **9** | **64** | **4** | **0** |
| Inherited from reels (not counted above) | 58 | | | | |

The inherited 58 are 13 P1, 34 P2 and 11 P3 (section "Inherited from reels").

The evidence level counted is the finding's primary level. Several findings carry a second level for one part (for example the code path is read and the size under load is suspected); the field in each finding says which part is which.

**Conditional raises:** lv-27 becomes P1 if reels-2 is confirmed (every production web view would then take the MP4 fallback). lv-13 becomes P1 if a measurement shows a prod-shaped worker is evicted or OOM-killed at the admitted caps (it then feeds the lv-6 loop).

## Merges and verifier changes

| Final | Was | Change |
|---|---|---|
| lv-2 | LV-B-2 | P2 → P1 (v2; lead decision). VERIFIED-RUN: `TestHLSSegmentTTL` runs and pins the 12 h window |
| lv-3 | LV-B-1, LV-D-3, LV-F-9 | Duplicates (v1); LV-F-9 is the test for it |
| lv-4 | LV-D-2 | Confirmed and worse: `media.events.long` is missing in prod, so long jobs also run in the reel loop (v1, reels-3) |
| lv-5 | LV-E-1 | Confirmed (v1). Nuance: Karpenter consolidation applies only on Karpenter nodes; rollouts and node rotation are certain |
| lv-6 | LV-E-2 | Confirmed (v1). Nuance: when the partition moves to a busy replica the counter does move and gives up after 3 |
| lv-7 | LV-V3-1, LV-B-9 | LV-B-9 (P3 mass assignment) is the entry point of the P1. Re-checked by the lead |
| lv-8 | LV-V3-5, Track B note, v2 FQ1 | One finding. P2 → P1 (v2; lead decision). Track A's COMPLETE rating for web captions corrected to BROKEN |
| lv-9 | v2 FQ2 | New, P1 |
| lv-10 | LV-F-1 | v2 suggested P2; kept P1, consistent with reels-10. Evidence VERIFIED-READ (v2: repository search) |
| lv-11 | LV-D-12 | v2 suggested P2; kept P1, consistent with reels-11 |
| lv-12 | LV-D-1 | P1 → P2 (v1: CPU and start latency on cold child playlists of multi-hour videos only) |
| lv-13 | LV-E-3, LV-D-5 | LV-E-3 P1 → P2 SUSPECTED (v1: sizes unmeasured). Its scratch half is LV-D-5; its thread half overlaps lv-4 |
| lv-14 | LV-D-4, LV-A-8 | The same unplayed 4K rung |
| lv-16 | LV-C-1, LV-A-13, LV-D-13 | The same fixed 24 h session. LV-A-13 SUSPECTED → VERIFIED-READ (v1 found nothing that extends `expires_at`) |
| lv-22 | LV-D-6, LV-B-8, LV-F-4 | The same unthrottled heartbeat; LV-F-4 is its test. LV-C-4's stale-mirror clause folded in |
| lv-23 | LV-D-7, LV-C-4 (retention half) | Same missing retention |
| lv-27 | LV-D-11, LV-V3-4 | The same web fallback to the MP4 at 0:00 |
| lv-35 | LV-F-2 | Track F P1 → P2 (v2; lead decision). Stands with lv-2 |
| lv-37 | LV-F-5 | V3 Part 1.1 found every named surface correct in code; only the test is missing |
| lv-45 | LV-A-11, LV-B-7, LV-C-4 (pause half) | Same client-only pause |
| lv-48 | LV-B-4 | Notification-leak clause disproved by V3 1.3: the fan-out reads anonymously and a private account's posts answer 404, so nothing is sent (see lv-18) |
| lv-56 | LV-C-7 | Delivery confirmed by V3 1.3 (title kept by the members-only redaction); founder decision |
| lv-58 | LV-C-9, LV-E-10 | Same environment drift |

## Index

| ID | Sev | Evidence | Title |
|---|---|---|---|
| lv-1 | P1 | VERIFIED-READ | Subscribers are never notified of an ordinary long-video upload |
| lv-2 | P1 | VERIFIED-RUN | Segment URLs stay valid up to 12 h after private, delete, takedown or membership lapse |
| lv-3 | P1 | VERIFIED-READ | No per-user storage or transcode quota; the count budget fails open |
| lv-4 | P1 | VERIFIED-READ | Two long-transcode lanes for the whole platform, sharing CPU with reels |
| lv-5 | P1 | VERIFIED-READ | Any worker interruption restarts a multi-hour transcode from the first rung |
| lv-6 | P1 | VERIFIED-READ | A crash, OOM or eviction is counted by nothing, so the job loops for ever |
| lv-7 | P1 | VERIFIED-READ | Any user can block another user's account erasure through a series or playlist `channel_id` |
| lv-8 | P1 | VERIFIED-READ | Web caption upload always fails, silently |
| lv-9 | P1 | VERIFIED-READ | 18+ is set by a separate PATCH after publish and fails open |
| lv-10 | P1 | VERIFIED-READ | No test uploads, transcodes on the long class, publishes and plays a long video |
| lv-11 | P1 | VERIFIED-RUN | No load test for any long-video endpoint, the long transcode or a 16 GiB upload |
| lv-12 | P2 | VERIFIED-RUN | Every child playlist RSA-signs each segment: 2,400 signatures for a 4 h rung |
| lv-13 | P2 | SUSPECTED | The worker pod is not sized for the inputs prod admits |
| lv-14 | P2 | VERIFIED-READ | The ladder decodes the original six times; the 4K MP4 is played by no client |
| lv-15 | P2 | VERIFIED-READ | A failed MP4 rung is dropped without a log line; its job row stays processing |
| lv-16 | P2 | VERIFIED-READ | A resumable upload dies 24 h after init; 3,277 parts for 16 GiB; Android copies the file |
| lv-17 | P2 | VERIFIED-READ | A scheduled long video goes live while still transcoding, and its notification is lost |
| lv-18 | P2 | VERIFIED-READ | Followers-only uploads and uploads by private accounts never notify |
| lv-19 | P2 | VERIFIED-READ | Upload notifications are lost after ~30 s of post-service or graph-service trouble |
| lv-20 | P2 | VERIFIED-READ | While a long job runs, the stall sweeper and Copyright Match fingerprints stop |
| lv-21 | P2 | VERIFIED-READ | Nothing would tell a person that long video is broken |
| lv-22 | P2 | VERIFIED-READ | Each progress heartbeat runs the full read gate, with no per-user limit |
| lv-23 | P2 | VERIFIED-READ | `watch_progress` is never pruned and every heartbeat rewrites an indexed column |
| lv-24 | P2 | VERIFIED-READ | "Top" comment order counts reactions per comment on every page, with OFFSET |
| lv-25 | P2 | VERIFIED-READ | Related videos are rebuilt from scratch on every page, uncached |
| lv-26 | P2 | VERIFIED-READ | Channel search takes one character and counts videos per matching channel |
| lv-27 | P2 | VERIFIED-READ | Web HLS failure or expired signature restarts the video at 0:00 on a 5-minute MP4 |
| lv-28 | P2 | VERIFIED-READ | `cover-frame` stores any `thumbnail_url`: unmoderated image and tracking pixel |
| lv-29 | P2 | VERIFIED-READ | Title, description, tag and category edits never reach search |
| lv-30 | P2 | VERIFIED-READ | A video over 4 h is refused only after the full upload; web says "Any length" |
| lv-31 | P2 | VERIFIED-READ | Playlists cannot be shared; the web playlist page needs sign-in |
| lv-32 | P2 | VERIFIED-READ | Android plays no network captions and uploads none |
| lv-33 | P2 | VERIFIED-READ | Android cannot publish a long video as Unlisted |
| lv-34 | P2 | VERIFIED-READ | Android has no creator tools for chapters, series, end screens, trim, private shares, bulk edit |
| lv-35 | P2 | VERIFIED-RUN | No test that going private stops a player or copied URL; the TTL test pins the gap |
| lv-36 | P2 | VERIFIED-READ | Resume across devices is tested only as pure rules and store ordering |
| lv-37 | P2 | VERIFIED-READ | Unlisted exclusion from trending, appearances, end-screen targets has no behaviour test |
| lv-38 | P2 | VERIFIED-READ | Upload fan-out delivery eligibility has no test |
| lv-39 | P2 | VERIFIED-READ | The multipart abort call, its failure and the cancel path are untested |
| lv-40 | P2 | VERIFIED-READ | No check ties produced Kafka topics to the MSK topic list |
| lv-41 | P2 | VERIFIED-RUN | No client test of quality selection, network captions or the create visibility list |
| lv-42 | P2 | VERIFIED-READ | Worker crash recovery during a long transcode is never tested by killing a worker |
| lv-43 | P3 | VERIFIED-READ | Signed-out viewers get no related videos or Up next |
| lv-44 | P3 | VERIFIED-READ | The server accepts a long video without a category |
| lv-45 | P3 | VERIFIED-READ | "Pause watch history" is client-only on each device |
| lv-46 | P3 | VERIFIED-READ | "Finished" is 90% on server and web, 95% on Android |
| lv-47 | P3 | SUSPECTED | The only generated thumbnail is a 150x150 letter-boxed square |
| lv-48 | P3 | VERIFIED-READ | Subscribing to a private account writes the row while the follow is only requested |
| lv-49 | P3 | VERIFIED-READ | RSS enclosures ignore `allow_download`; shared caches keep items up to 70 min |
| lv-50 | P3 | VERIFIED-READ | Android offline copies unencrypted and kept 48 h after sign-out |
| lv-51 | P3 | VERIFIED-READ | The self-service export has no viewing data |
| lv-52 | P3 | VERIFIED-READ | A playlist can list posts the owner cannot see and leak their ids |
| lv-53 | P3 | VERIFIED-READ | The progress ordering guard is inert: no client sends `sent_at_ms` |
| lv-54 | P3 | VERIFIED-READ | Playlist and series counts include invisible items and drift up after purge |
| lv-55 | P3 | VERIFIED-READ | user-service still writes `channels` through two reachable routes |
| lv-56 | P3 | VERIFIED-READ | Members-only upload notifications go to every subscriber |
| lv-57 | P3 | VERIFIED-READ | Multipart abort is tried once; there is no user-cancel route |
| lv-58 | P3 | VERIFIED-RUN | Staging and Azure differ from prod on long video; QA caps originals at 6 GiB |
| lv-59 | P3 | SUSPECTED | The schedule worker's oldest-100 window can starve newer due posts |
| lv-60 | P3 | VERIFIED-READ | post-service migrates on every replica without the lock; `channels` missing from the gate |
| lv-61 | P3 | VERIFIED-READ | The Loved collection mirrors likes best-effort and never heals |
| lv-62 | P3 | VERIFIED-READ | Web hls.js: unlimited back buffer, no cap to player size |
| lv-63 | P3 | VERIFIED-READ | Moving one playlist item rewrites up to 5,000 rows |
| lv-64 | P3 | VERIFIED-READ | The web watch page fires about 18 API reads per open |
| lv-65 | P3 | SUSPECTED | A rebalanced duplicate long job blocks a lane on an advisory lock for the whole job |
| lv-66 | P3 | VERIFIED-READ | No switch stops long-video intake in an incident |
| lv-67 | P3 | VERIFIED-RUN | The chart QA guard now fails twice (`ACTION_LINK_OWN_DOMAINS`) |
| lv-68 | P3 | VERIFIED-READ | Failure messages are thin: web "may still be processing", Android "Still processing" on a failed transcode |
| lv-69 | P3 | VERIFIED-RUN | `TestUnwritableQuarantineDoesNotCommit` is timing-dependent |
| lv-70 | P3 | VERIFIED-RUN | Part of the web posttube suite asserts source or CSS text |
| lv-71 | P3 | VERIFIED-READ | An age-restricted upload makes every fan-out recipient fail and retry |
| lv-72 | P3 | VERIFIED-READ | The owner can edit a staged post straight to public |
| lv-73 | P3 | VERIFIED-READ | Post edits never check author standing |
| lv-74 | P3 | VERIFIED-READ | Flick series accept anyone's post as an episode and overwrite episodes |
| lv-75 | P3 | VERIFIED-READ | Scheduled lists: Android shows "Nothing scheduled" on error; web stops at 50 |
| lv-76 | P3 | VERIFIED-READ | The web series manager cannot rename or delete a series |
| lv-77 | P3 | VERIFIED-READ | The RSS "original" enclosure states the wrong size |

---

## P1

### lv-1

- **ID:** lv-1
- **Title:** Subscribers are never notified of an ordinary long-video upload: both clients create the post while the video is still processing, the fan-out reads the post anonymously, gets 404 and marks every recipient done for good, and nothing re-announces the post once it becomes playable.
- **Severity:** P1
- **Category:** functional
- **Evidence level:** VERIFIED-READ (Track A; confirmed by v2)
- **Evidence:**
  - Clients create the post right after the bytes are confirmed: Android `CH/ReelPublishPipeline.kt:44-52,247-257` ("NO WAIT FOR THE TRANSCODE"); web `src/features/upload/useUploadStudio.ts:557-567,611-622`.
  - post-service emits `PostCreated` at create for any immediate, unheld post (`post-service/internal/service/copyright_title_review.go:329-338`, `createdEventFor`).
  - The fan-out reads `GET /v1/posts/:id` with only the internal key (`notification-service/internal/service/fanout_eligibility.go:180-203,281-284`); the key grants no bypass and the viewer stays nil (`post-service/internal/http/post_reads_handler.go:52-70`). A processing or pending post is 404 to a nil viewer (`post-service/internal/service/post_reads.go:32-50`, `processing.go:90-95`).
  - 404 sets `exists=false`; `!exists || !approved` is a terminal skip (`fanout_eligibility.go:82-84,187-188`) and each recipient is `MarkDelivered` (`notification-service/internal/service/subscriber_fanout.go:387-393`), which also blocks any later re-announce of that post id. The drain runs every 5 s with no not-before (`store/postgres/fanout_jobs.go:118-130`).
  - The only enqueue is `case events.PostCreated` (`notification-service/internal/events/consumer.go:267-276`). The ready flip (`post-service/internal/consumers/media.go:353` `finalizeReviewGate`) emits only the search eligibility event (`store/postgres/posts.go:1540-1551`); an unlisted-to-public edit emits `PostVisibilityChanged` (`store/postgres/post_edit.go:263-289`). So live replays (created unlisted) and videos made public later never notify either.
  - Scheduled uploads whose transcode finished before `publish_at` are the exception (`post-service/internal/service/schedule.go:450-462`); see lv-17 for the rest.
- **How to reproduce:** B subscribes to A's channel with `notify_on=all`. A uploads a 10-minute video from either client and publishes at once. Within 5 s the fan-out job reads the post, gets 404 and marks every recipient skipped; B receives nothing after the video becomes playable.
- **Impact:** The "subscribe and get the next upload" loop, the core retention mechanic of a long-video product, fails for almost every upload. Creators see subscribers but no traffic from them.
- **Fix:** In post-service, when the review gate first flips a long-video post to approved (and on the first transition of a post to `public` from `unlisted` or `private`), write one fan-out trigger event (`PostReady`, or `PostCreated` with a flag) in the same transaction; have notification-service enqueue on it, de-duplicated on post id. Alternatively make "processing or pending" a retry-later answer from an internal state read (shared with lv-18) instead of a terminal skip. This also fixes lv-17 and the long-video half of reels-30.
- **Acceptance:** A notification-service test where the first state read says processing and a later one says public and approved delivers exactly once. A post-service test that the ready flip and an unlisted-to-public edit each write one trigger event. Fails now: no trigger exists.
- **Effort:** M
- **Was:** LV-A-1 (long-video form of reels-30)

### lv-2

- **ID:** lv-2
- **Title:** Long-video HLS segment URLs are signed for twice the duration plus 15 minutes, up to 12 hours, after one authorisation, so a video made private, deleted, taken down, copyright-blocked or behind a lapsed membership keeps playing at segment level for hours; an open player plays to the end and a copied segment URL keeps working.
- **Severity:** P1
- **Category:** authz / visibility
- **Evidence level:** VERIFIED-RUN (`TestHLSSegmentTTL` runs and passes, pinning 8 h 15 min for 4 h and the 12 h cap) plus VERIFIED-READ (v2)
- **Evidence:**
  - `media-service/internal/delivery/sign_cache.go:60-90`: `MaxHLSSegmentTTL = 12h`, TTL = 2 × duration + 15 min. `HLSURLsForAsset` signs every segment under `/hls/` with that TTL after one authorisation (`delivery/authz.go:628-670`). The service's own comment: the TTL is the revocation window (`delivery/signer.go:66-71`).
  - Canned CloudFront signatures not bound to a viewer. No `CreateInvalidation` in Go, Terraform or YAML; no env override of the segment TTL; no key rotation or prefix move on a visibility change (video keys stay under `user/...`, `signer.go:80-85`) (v2).
  - The manifest is re-gated on every fetch (`no-store`), but a VOD child playlist carries `EXT-X-ENDLIST`, so a player never re-fetches it (V3 1.5).
  - The reels equivalent is 5 min (`MaxProtectedTTL`); reels-17 covers the 35-minute reel window.
- **How to reproduce:** As a member, open a 3-hour members-only video and save the media playlist. Cancel the membership (or have the creator delete or privatise the video, or an admin block it for copyright). Fetch the segment URLs: they play for about 6 h 15 min.
- **Impact:** The harness journey "switch to private; an open player and a copied stream URL stop" fails. Legal and copyright takedowns, deletion and paid-membership lapse are enforced hours late; a leaked playlist is a bearer link to the whole film for up to half a day. Not P0: only someone already authorised, or anyone they share the link with, keeps access.
- **Fix:** Cap the segment TTL for protected assets at a stated bounded window (for example 30 min), independent of duration, and keep `X-HLS-Expires-At`. Ship with the web reload on 403 or expiry (lv-27), otherwise web viewers lose their place sooner. Shorter TTLs mean more re-signing, so pair with lv-12 fix (a) (one signature per playlist). A stated window is still needed: even 30 min does not stop an open player at once.
- **Acceptance:** `TestHLSSegmentTTL` changed to assert ≤ 30 min for a 3 h video (fails now). The joined test of lv-35 passes. A web player test: a 403 on a segment reloads the playlist once and resumes at the same position.
- **Effort:** M (server S; web refresh path in lv-27)
- **Was:** LV-B-2 (long-video form of reels-17)

### lv-3

- **ID:** lv-3
- **Title:** There is no per-user storage or transcode quota; the only upload control is a count of 10 per hour and 30 per day that fails open on Redis, so one account can confirm about 30 four-hour, 16 GiB uploads a day, more long-transcode work than the two-lane fleet does in weeks, and every original is kept beside its renditions.
- **Severity:** P1
- **Category:** input / abuse (cost and availability)
- **Evidence level:** VERIFIED-READ (v1 confirmed; LV-D-3 and LV-B-1 are one finding)
- **Evidence:**
  - `media-service/internal/service/ratelimit.go:21-24` (no Redis = no limit), `:49-51` (`return true // fail-open`); limits 10/h and 30/day (`validation.go:37-38`). Resumable init counts against the same budget (`resumable.go:120-125`).
  - Size cap: prod leaves `MEDIA_WORKER_MAX_ORIGINAL_BYTES` unset = 16 GiB (`processing/original_cap.go:18-33`); length 4 h; job budget 48 h (`processing/limits.go:57,158`).
  - A grep in media-service for `quota|bytes_used|transcode_minutes` finds only a test comment; post-service `MAX_POSTS_PER_DAY` does not gate the transcode (v1). Map section 7: no `*QUOTA*` env in any Go service.
  - Storage per video: the original is kept (owner-only `/download`), plus 5 MP4 renditions and 3 HLS rungs copied from three of them (`processing/video.go:38-45,457-510`). FIFO per partition, no per-uploader fairness (`cmd/worker/transcode_classes.go:229-256`).
  - Tests cover only the count budget (`resumable_ratelimit_test.go:15,35`, `resumable_video_cap_test.go`); nothing tests fail-open or cumulative bytes (LV-F-9).
- **How to reproduce:** With one account, init and confirm 30 resumable uploads of a 4 h test file in a day on QA. All are accepted and queued ahead of every other creator. During a Redis blip the count limit disappears.
- **Impact:** One account, abusive or simply prolific, can deny long-video publishing to everyone for days (and reels, while long jobs run on the reel loop, reels-3). Storage and egress cost have no per-account ceiling.
- **Fix:** At confirm (and resumable init), a per-user rolling 24 h byte budget and a daily source-duration budget for long transcodes (read from the MP4 header the worker already parses, `processing.MP4Duration`), at most one in-flight long job (or about 3 non-terminal videos) per uploader, 429 `TRANSCODE_QUOTA`, and fail closed for video on a Redis error. Set `MEDIA_WORKER_MAX_ORIGINAL_BYTES` explicitly in `values-prod.yaml`. Product decides the numbers.
- **Acceptance:** Service tests: the init or confirm that would cross the byte or minute budget answers 429; a second in-flight long job answers 429; a Redis error refuses a video init (fails now: fail-open). The prod values render the explicit byte cap.
- **Effort:** M
- **Was:** LV-B-1, LV-D-3, LV-F-9

### lv-4

- **ID:** lv-4
- **Title:** The platform runs at most two long transcodes at once (one long loop per pod, two pods, no autoscaling), a job can hold a lane for up to 48 h, and its ffmpeg shares the pod's 3 CPUs with the reel loop and fingerprinting; in prod it is worse, because `media.events.long` is missing and long jobs run inside the reel loop.
- **Severity:** P1
- **Category:** performance / capacity
- **Evidence level:** VERIFIED-READ (structure, v1); SUSPECTED (job durations; no ffmpeg here)
- **Evidence:**
  - One sequential consumer loop per class (`media-service/cmd/worker/transcode_classes.go:23-54,229-256`, `cmd/worker/main.go:220-252`). Prod: `MEDIA_TRANSCODE_CLASSES: "short,long"`, `worker.replicaCount: 2`, CPU request 1, limit 3, 4Gi (`deploy/services/media-service/values-prod.yaml:153-176`). One worker Deployment, no worker HPA, no KEDA (`charts/atpost-service/templates/worker-deployment.yaml:23-27`, `hpa.yaml:9-12`).
  - Sequential long-form pipeline: five x264 `veryfast` encodes from the original, then a storyboard that decodes it again, then the HLS copy (`processing/video.go:439-513`, `storyboard.go:84-89`). x264 gets no `-threads` (`video.go:202-218`), so it competes with the reel loop in the same pod.
  - Budgets: each ffmpeg call 10 min + 20 × duration, capped 24 h; the job 12 × that, capped 48 h; past the budget is terminal (`processing/limits.go:129-158`, `cmd/worker/main.go:597-605`). The file's own note: about 15x real time per long-form rendition on 1 CPU (`limits.go:126-128`); the devbox measured a 24-minute upload at about 10 h on 1 CPU (`Architecture/docker/docker-compose.yml:1097-1100`).
  - Worse in prod (v1): `media.events.long` is absent from `infra/terraform/modules/msk/topics-job/variables.tf:28-59` and `auto.create.topics.enable=false` (`modules/msk/main.tf:147`), so every forward fails fast and the job runs in the short loop (`transcode_classes.go:214-220`; Track C read the kafka-go writer). Reels then queue behind multi-hour jobs (reels-3).
- **How to reproduce:** Load test L5 (Track D section 6): on a prod-sized worker, confirm two 4 h 1080p uploads and one 4 h 4K upload, then a 60 s reel; measure confirm-to-ready for each and the reel's time while the long jobs run.
- **Impact:** Arithmetic from the code's own figures: a 4 h 1080p job is about a day at 3 CPUs; two such uploads hold the platform's long capacity for a day, everything else waits; a 4 h 4K job is likely to end at the 48 h budget as a terminal failure. With the topic missing, reels wait too.
- **Fix:** (a) Add `media.events.long` to MSK (reels-3). (b) A dedicated long-class worker Deployment (`MEDIA_TRANSCODE_CLASSES=long`, its own CPU) beside short-only pods, as `values-prod.yaml:166-174` already plans. (c) `-threads` equal to the CPU limit. (d) lv-14 to cut work per job. (e) A lag and oldest-age alert on group `media-transcode-worker-long` (needs reels-5, lv-21).
- **Acceptance:** A render test: the prod chart renders a worker with `MEDIA_TRANSCODE_CLASSES=long` separate from the short worker (fails now). L5 thresholds met.
- **Effort:** M
- **Was:** LV-D-2

### lv-5

- **ID:** lv-5
- **Title:** A multi-hour long transcode has to run uninterrupted for up to two days, but every worker rollout (Recreate), node rotation and, on Karpenter nodes, consolidation after 30 s of under-use stops it, and every interruption restarts it from the first rung.
- **Severity:** P1
- **Category:** reliability
- **Evidence level:** VERIFIED-READ (v1); SUSPECTED (how often it happens; no cluster exists, feed-02)
- **Evidence:**
  - No protection: `podAnnotations: {}` (`charts/atpost-service/values.yaml:232`), none in media values; no `karpenter.sh/do-not-disrupt` or `preStop` anywhere (v1 grep).
  - Karpenter `WhenEmptyOrUnderutilized`, `consolidateAfter 30s`, no budgets (`infra/terraform/modules/karpenter/main.tf:184-187`). Nuance (v1): the worker has no nodeSelector, so it may land on the managed groups (`modules/eks/main.tf:88-133`), which Karpenter does not consolidate.
  - The PDB selector also matches worker pods (`templates/pdb.yaml:14-16`, `worker-deployment.yaml:59-60`), `minAvailable: 1` over API and worker pods together (`values-prod.yaml:146-148`; reels-103).
  - `type: Recreate` (`worker-deployment.yaml:55`); the worker image tag is bumped by CI per commit (`values-prod.yaml:157-158`).
  - On SIGTERM the root context is cancelled and every ffmpeg runs under `exec.CommandContext` (`cmd/worker/main.go:161-168`, `processing/tool.go:175-177`), so the 300 s grace (`worker-deployment.yaml:83`) buys nothing; the chart comment at `values.yaml:245-246` claims the opposite (reels-56).
  - Restart from zero: a new temp dir, the original downloaded again, every rung re-encoded; variants recorded only after every rung is uploaded (`cmd/worker/main.go:621-672,806-848`, `processing/video.go:439-513`).
- **How to reproduce:** On QA once it exists: upload a 2-hour 1080p video, wait 30 min, roll the media worker (or drain its node); the log shows the context cancelled before commit and the next pod starting the same media from the first rung.
- **Impact:** Each media deploy inside a long job's window throws all of its work away; on a cluster that deploys daily the longest uploads may never finish, and the creator sees "processing" for days. Reels on the same pod lose their in-flight work too.
- **Fix:** Smallest: `karpenter.sh/do-not-disrupt: "true"` in `worker.podAnnotations` for prod and QA; a worker-only PDB; a runbook step in `docs/runbooks/service-rollback.md` and the deploy procedure to check for in-flight long jobs (fresh heartbeat, duration over 600 s) before rolling the worker. Next: the reels-56 drain (stop fetching on SIGTERM, let the current rung finish), then reuse MP4 rungs already uploaded for the same `media_generation` (M).
- **Acceptance:** The rendered worker pod carries the annotation (`render_test.sh` assertion, fails now); on QA a consolidation leaves a busy worker node alone; the runbook has the in-flight check. With rung reuse: a worker test where a second run of the same generation skips uploaded rungs.
- **Effort:** S (annotation, PDB, runbook); M (rung reuse)
- **Was:** LV-E-1

### lv-6

- **ID:** lv-6
- **Title:** A long job that crashes, is OOM-killed or is evicted is redelivered and re-run without limit, because no counter sees a worker death; a video too big for the pod loops for ever, blocks its long partition and kills the reels running beside it on each cycle.
- **Severity:** P1
- **Category:** reliability
- **Evidence level:** VERIFIED-READ (counting gap, v1); SUSPECTED (that real inputs trigger it; see lv-13)
- **Evidence:**
  - `retryUntilDurable` counts only attempts that return an error (`media-service/cmd/worker/transcode_guards.go:122-167`); a dead process returns nothing and the offset stays uncommitted (`transcode_classes.go:244-249`).
  - The only persistent counter, `transcode_attempts`, moves only on a stall re-queue (`store/postgres/media_outbox.go:351`), which needs a heartbeat older than 10 min (`store/postgres/transcode_lease.go:69-76,128-146`). A redelivered job stamps the heartbeat at once (`cmd/worker/transcode_lease.go:77-86`, called at `main.go:364-366`), and the sweeper first waits 5 min (`transcode_lease.go:38-42`).
  - The 48 h job budget is created per delivery (`main.go:364`), so it does not bound the loop either. No `transcode_starts` or delivery counter exists (v1 grep).
  - The pre-download space check uses `Statfs` on the node filesystem and cannot see the emptyDir `sizeLimit` (`cmd/worker/diskspace_unix.go:10-17`, `transcode_guards.go:226-246`).
  - Short and long loops share one container (`main.go:220-252`).
  - Nuance (v1): if the partition moves to a replica whose long loop is busy, the heartbeat can go stale and the counter gives up after 3 (`transcode_lease.go:73,155-162`). The normal case (OOM restart in place, or a replacement pod within minutes) loops.
- **How to reproduce:** Unit test: a store fake and a handler that never returns, delivered three times; assert no counter moved. On dev: limit the compose worker to 2 GB, upload a long 4K video whose encode needs more, and watch repeated OOM restarts with `transcode_attempts` at 0.
- **Impact:** One oversized or pathological upload occupies a worker indefinitely, burning hours of CPU each cycle; every long video behind it on that partition waits for ever; reels on that pod restart every cycle. Nothing alerts (reels-5, lv-21).
- **Fix:** Count starts, not failures: in `processTranscodeLocked`, before `run()`, increment a per-asset start counter for the current `media_generation`; if it is already at 3 when a delivery begins, record the terminal failure "transcode lost its worker 3 times (crash, OOM or eviction)" with `CompleteTranscode(failed, manual_review)` and commit. Reset on a new generation.
- **Acceptance:** Unit test: three deliveries that never return leave the fourth recorded as failed with that reason and committed; a normal job increments once (fails now: no counter).
- **Effort:** S
- **Was:** LV-E-2

### lv-7

- **ID:** lv-7
- **Title:** Any user can block another user's account erasure for good by pointing a video series or a playlist at the victim's channel: post-service stores any `channel_id` unchecked, the foreign key has no `ON DELETE`, and the user-service purge deletes the victim's channel without clearing those references, so its transaction fails on every retry.
- **Severity:** P1
- **Category:** privacy / data (DPDP erasure), authz (mass assignment)
- **Evidence level:** VERIFIED-READ (V3; re-checked by the audit lead)
- **Evidence:**
  - `post-service/internal/http/video_series_handler.go:201-205` (series create) and `:587-590` (playlist create) store any parseable `channel_id`. No ownership check on `ChannelID` exists in `post-service/internal/service/video_series.go` or `playlists.go` (only the cover is checked, `video_series.go:128-139`, `playlists.go:87-106`). Stored as given (`store/postgres/video_series.go:46-49`).
  - `post-service/database/migrations/012_posttube_features.sql:8,35`: `channel_id UUID REFERENCES channels(id)` with no `ON DELETE`; no later migration changes it.
  - `user-service/internal/store/purge.go:73-77` deletes the channel's links, milestones, members and subscriptions, then `DELETE FROM channels WHERE user_id = $1`; it never clears `video_series` or `playlists`. The purge runs in one transaction and its consumer retries with backoff (`purge.go:38-48`), so a violation repeats for ever.
  - The two services must share one database: post-service migrations reference `users(id)` and post-service has no `users` table (V3 1.4), so the FK is live.
  - The victim's channel id is public: `GET /v1/search/channels` returns it as `id` (`search-service/internal/http/handler_tube.go:104-106,162`).
  - Same mass-assignment entry point: `trailer_post_id` is also stored unchecked (`video_series_handler.go:299,339`); no reader uses it today.
- **How to reproduce:** A reads B's channel id from `/v1/search/channels?q=<B's name>`, then `POST /v1/video-series {"title":"x","channel_id":"<B's channel id>"}`. B requests account deletion. After the grace period the user-service purge fails on `DELETE FROM channels` (SQLSTATE 23503) on every retry; B's user row, profile and channel remain.
- **Impact:** A stranger can make another person's erasure request impossible to complete, a legal breach for the platform. The same failure hits a user who set their own `channel_id` on someone else's object through the API.
- **Fix:** Smallest: in user-service `PurgeUser`, before deleting channels, `UPDATE video_series SET channel_id = NULL WHERE channel_id IN (SELECT id FROM channels WHERE user_id = $1)` and the same for `playlists` (probed with `to_regclass` like the other steps). Then: ignore a client `channel_id` in post-service (derive it from the creator's channel), require `trailer_post_id` to be the creator's own post, and add `ON DELETE SET NULL` in a migration.
- **Acceptance:** user-service purge test: a stranger's series and playlist referencing the user's channel do not block the purge and end with `channel_id` NULL (fails now). post-service handler test: a body `channel_id` is ignored; a stranger's `trailer_post_id` answers 403.
- **Effort:** S
- **Was:** LV-V3-1, LV-B-9

### lv-8

- **ID:** lv-8
- **Title:** Caption upload on the web always fails: the client sends the track as a `data:` URL in `content_url`, which media-service refuses with 400, and the upload studio swallows the error, so creators get no captions and no message; with Android unable to upload captions at all, no client can add a caption track.
- **Severity:** P1
- **Category:** functional (accessibility)
- **Evidence level:** VERIFIED-READ (both sides re-read by Track B, v2 and V3)
- **Evidence:**
  - `src/features/posttube/data/posttubeApi.ts:615-630` `createSubtitleTrack` always builds `data:text/vtt,...` and sends it as `content_url`, never `content`.
  - `media-service/internal/http/clips_handler.go:405-431`: any non-blank `content_url` answers 400 `CONTENT_URL_NOT_ACCEPTED` on `POST /v1/subtitles/:mediaId` (route `:68`). No proxy rewrites the body.
  - It is the only web caption-upload path: upload studio (`src/features/upload/useUploadStudio.ts:412,589`, failure swallowed at `:586-592`, `catch { /* non-fatal */ }`) and Creator Hub (`src/features/posttube/hub/hubApi.ts:1178-1180`, `hub/components/EditSheet.tsx:860-862`).
  - Android has no caption upload (lv-32).
- **How to reproduce:** In the studio or Hub edit, Captions, upload any `.vtt`: `POST /v1/subtitles/<id>` answers 400 and no track appears.
- **Impact:** A creator journey is silently broken; deaf and hard-of-hearing viewers get no creator captions on any long video. Track A's "web captions COMPLETE" rating was wrong for upload.
- **Fix:** Send `content: vttContent` and drop `content_url`; show an error in the studio when the caption step fails.
- **Acceptance:** Web unit test that the request body has `content` and no `content_url`; a studio test that a caption failure is shown to the creator; a manual upload on dev shows the track.
- **Effort:** S
- **Was:** LV-V3-5, Track B note to Track A, v2 FQ1

### lv-9

- **ID:** lv-9
- **Title:** A creator's 18+ flag is not part of publishing: post-service takes `age_restricted` only on the edit route, the web studio sets it by a separate PATCH after the post is created, and if that call fails the video stays published and ungated with only a soft warning.
- **Severity:** P1
- **Category:** safety / authz (age gate)
- **Evidence level:** VERIFIED-READ (v2)
- **Evidence:**
  - `age_restricted` is accepted only by `PATCH /v1/posts/:id` (`post-service/internal/http/post_edit.go:89,311`); the create route has no such field.
  - The studio sends it afterwards, `updatePost(postId, {age_restricted: true})` in `onSuccess` (`src/features/upload/useUploadStudio.ts:664-674`, `src/features/upload/studioApi.ts:159-168`). On failure it only warns "The 18+ age restriction could not be saved. Turn it on in Creator Hub."
  - What limits it: the PATCH runs seconds after create, while a long video is usually still processing and hidden from everyone but the author. The exposure is real when the PATCH fails, or when processing finishes first (a short clip, a re-used asset).
  - The Android create path was not followed for this field.
- **How to reproduce:** In the studio, tick 18+ and publish while the `PATCH /v1/posts/:id` is made to fail (offline after create, or a 5xx): the post is published without the restriction and the creator sees only a warning.
- **Impact:** Content the creator marked as adult can reach under-18 viewers and signed-out visitors, with the platform relying on a best-effort follow-up call.
- **Fix:** Accept `age_restricted` on `POST /v1/posts` and on the draft publish route and store it in the create transaction; the studio sends it there and fails closed (no publish) if it cannot be set. Check Android sends it at create.
- **Acceptance:** A create handler test: `age_restricted: true` is stored at create (fails now: field ignored). A web test that the flag is in the create or draft body and that no separate PATCH is needed. An Android publish test for the field.
- **Effort:** S
- **Was:** v2 FQ2

### lv-10

- **ID:** lv-10
- **Title:** No test at any layer uploads a long video, sends it through the long transcode class, publishes it and plays it at each HLS quality on a second account.
- **Severity:** P1
- **Category:** tests
- **Evidence level:** VERIFIED-READ (repository search; v2)
- **Evidence:** `Architecture/tools/integration` matches `long_video` only in the creator-fund and settlement tests; no `/v1/videos`, `m3u8` or `media.events.long`. The e2e media domain runs only `api-gateway media-service`, with no worker (`.github/workflows/e2e.yml:57-60`). Smoke `health.longvideo` is one trending GET (`scripts/smoke/dev-smoke.mjs:429-431`), and the `video.post` step deliberately creates a `flick` so the long-video guards do not apply (`:651-665`). The long-class tests use a fake writer (`media-service/cmd/worker/transcode_classes_test.go:160,199`); the ladder tests check argument building only (`processing/video_hls_test.go:15,25`). No Android `androidTest`; no web e2e. This is why lv-1 and lv-8 went unseen.
- **How to reproduce:** `grep -lE "/v1/videos|m3u8|long_video" Architecture/tools/integration/*.go`; read `e2e.yml`.
- **Impact:** The classifier boundary, the forward to `media.events.long`, the long ladder, the review gate and the delivery gate for a second viewer can all break with every CI job green.
- **Fix (test to add):** An e2e domain `longvideo` (api-gateway, media-service, media-worker with ffmpeg and `MEDIA_TRANSCODE_CLASSES=short,long`, post-service, feed-service, notification-service, Redpanda with `media.events.long` created). `TestE2E_LongVideo_UploadPublishPlay`: as A, resumable-upload a 12 s 1280x720 landscape fixture (landscape routes to the long class), poll until ready, `POST /v1/posts content_type=long_video`; as B, fetch `master.m3u8`, assert the 360p and 720p variants and no 1080p, fetch one segment of each; signed out on a private copy, assert 404; assert B's subscriber notification row (lv-1).
- **Acceptance:** Green in CI; red when the long consumer is not started (also assert the `media.events.long` consumer offset moved), when the ladder drops 720p, or when the notification is not delivered.
- **Effort:** M (on top of reels-10 and reels-12)
- **Was:** LV-F-1

### lv-11

- **ID:** lv-11
- **Title:** No load test exists, or has ever been run, for any long-video endpoint, for the long transcode, or for a 16 GiB resumable upload, so every capacity statement about long video is unproven.
- **Severity:** P1
- **Category:** tests / performance
- **Evidence level:** VERIFIED-RUN (repository search; Track D step 5 and v2)
- **Evidence:** The only load tests in the trees are `dating-service/loadtest/*.js` and `rider-service/loadtest/*.js` (and their copies under `.claude/worktrees`); pb-echo has none; no `func Benchmark` in any service under `Architecture/services`; no workflow mentions k6. The dev smoke checks only `GET /v1/posts/trending?content_type=long_video`. The only measurement in this audit is a scratch program: 2,400 RSA signatures in 5.0 s (Track D) and 6.15 s (v1) on one laptop core (lv-12).
- **How to reproduce:** `git ls-files | grep -iE "k6|vegeta|locust|gatling|loadtest|artillery|ghz"` in both trees.
- **Impact:** The playlist rate at which media-service stops starting videos (lv-12), the heartbeat rate post-service survives (lv-22), the queue time of a long upload (lv-4) and whether a worker fits the admitted inputs (lv-13) are all unknown.
- **Fix:** Write and run L1 to L9 from Track D section 6 (watch-open mix, playback start and switches, progress heartbeats, analytics for long sessions, long transcode with a reel burst, 16 GiB uploads, lists and search, comments, a 6 h soak with real browsers) against a QA stack sized like production with `media.events.long` created.
- **Acceptance:** k6 scripts under `Architecture/tools/loadtest/long-video/` and a results file in this folder with p50, p95, p99, error rate, CPU, Postgres statements per second, consumer lag and queue age for each scenario, meeting the Track D section 6 thresholds.
- **Effort:** L
- **Was:** LV-D-12 (long-video half of reels-11)

---

## P2

### lv-12

- **ID:** lv-12
- **Title:** Every long-video child playlist RSA-signs each of its segments: about 2,400 signatures, 5 to 6 s of one core and a 1.3 MB body for one 4-hour rung, paid at start-up and on every quality switch on 1-CPU media-service pods; the 20,000-entry sign cache holds fewer than three such videos before it is wiped.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-RUN (scratch program outside the repo: 5.0 s in Track D, 6.15 s in v1) / VERIFIED-READ (code path) / SUSPECTED (saturation under real traffic)
- **Evidence:** One key per segment line (`media-service/internal/service/playback.go:277-288,314-315`); one `signSegment` per key (`delivery/authz.go:651-669`); a miss does one `rsa.SignPKCS1v15` with a canned policy for that one URL (`sign_cache.go:235-253`, `signer.go:163-199`). 6 s segments for every rung (`processing/video.go:196`). Reuse lasts 2 min; at 20,000 entries with nothing stale the whole map is dropped; one global mutex (`sign_cache.go:54,151-196`). The body is `no-store` and uncompressed. Pods: CPU limit 1, HPA 2 to 6 (`deploy/services/media-service/values-prod.yaml:18-31`).
- **How to reproduce:** Benchmark `serveHLSPlaylist` for a fixture with 2,400 segment lines and the real signer: cold, within 2 min, after 2 min, and three distinct assets. Load test L2.
- **Impact:** About 0.2 to 0.4 cold 4-hour child playlists per second per pod, with reel playlists on that pod waiting; several seconds before the first segment; 1.3 MB per rung on a phone. A 1-hour video costs a quarter. Shorter TTLs (lv-2) make re-signing more frequent.
- **Fix:** (a) One CloudFront custom-policy signature per playlist for the asset's `.../hls/*` prefix, the same query on every line. (b) Until then, cache the rewritten child body per pod for the reuse window (authorisation still per request). (c) `-hls_time 10` for long-form plans.
- **Acceptance:** A counting fake signer: a 2,400-segment child playlist makes at most 2 signer calls (fails now: 2,400). A benchmark p50 under 50 ms on one core.
- **Effort:** S for (b), M for (a)
- **Was:** LV-D-1

### lv-13

- **ID:** lv-13
- **Title:** The worker pod was never sized for the inputs prod admits (16 GiB, 4 h, 4K, 240 fps): the 3x scratch check sees the node disk, not the 50 Gi emptyDir, a 4 h 4K ladder plausibly needs more than 50 Gi, ffmpeg threads follow the node's CPU count inside a 4 Gi limit shared by three loops, and a 4K rung of a long video can exceed the 24 h per-call cap.
- **Severity:** P2 (P1 if measurement shows eviction or OOM at the caps, through lv-6)
- **Category:** deployment / reliability
- **Evidence level:** SUSPECTED (sizes and times computed from code and one dev measurement; v1) / VERIFIED-READ (limits and code paths)
- **Evidence:** Admitted inputs: `processing/original_cap.go:18-33` (16 GiB when unset; prod does not set it), `limits.go:52-66` (4 h, 4096 px, 240 fps). Scratch: `scratchHeadroom = 3` (`cmd/worker/transcode_guards.go:61-63,240-255`) measured with `Statfs` on the node (`diskspace_unix.go:10-17`), so 48 GiB passes on a 100 Gi Karpenter root (`modules/karpenter/main.tf:123-131`) while the emptyDir limit is 50Gi (`values-prod.yaml:166`). Every MP4 rung stays in one temp dir until the job returns and HLS copies go to a second dir on the same volume (`cmd/worker/main.go:618-625,902-932`); CRF 23 with no maxrate (`processing/video.go:176,205-218`). Rough estimate for 4 h 4K: about 62 GiB of rungs and HLS plus the 16 GiB original. No `-threads` (`video.go:202-218`); memory 4Gi for the long encode, a reel encode and a fingerprint ffmpeg (`values-prod.yaml:160-165`). Per-call cap 24 h (`limits.go:127-142`). QA caps the original at 6 GiB on 20 Gi scratch, so QA cannot show any of this (`values-qa.yaml:119-126,184`).
- **How to reproduce:** One measured encode on a QA-shaped pod (1 CPU request, 3 limit, 4 Gi, 50 Gi scratch) of a 4 h 1080p and a 1 h 4K file: peak `du`, peak RSS, wall time per rung (L5).
- **Impact:** The largest admitted uploads are the most likely to be evicted (then lv-6 loops), OOM-killed, lose their 4K or 1080p rung silently (lv-15), or fail after two days. The founder's "long videos unlimited" is bounded by numbers nobody has measured.
- **Fix:** After the measurement, set `MEDIA_WORKER_MAX_ORIGINAL_BYTES` and `MEDIA_MAX_VIDEO_DURATION_SEC` explicitly in prod (also clears reels-62); `-threads <CPU limit>`; compute headroom from duration × rung bitrates (or 6x at ≥ 1440p) against the emptyDir limit; delete each MP4 rung from scratch once uploaded and no longer needed for HLS; lv-14 (a) removes the largest file.
- **Acceptance:** A guard test: a 16 GiB 2160p original with 50 GiB of scratch is refused up front (fails now). A dated measurement in this folder and a render guard that `scratchSizeLimit` covers the computed peak for the cap in every values file.
- **Effort:** M
- **Was:** LV-E-3, LV-D-5

### lv-14

- **ID:** lv-14
- **Title:** The long-form ladder decodes the original six times (five separate x264 encodes plus the storyboard), and its most expensive rung, a 4K MP4, is requested by no Tube player because the HLS ladder stops at 1080p.
- **Severity:** P2
- **Category:** performance / cost
- **Evidence level:** VERIFIED-READ (pipeline and consumers); SUSPECTED (relative costs)
- **Evidence:** `processing/video.go:457-510` (`TranscodeToMP4` 360/480/720/1080/2160, each `-i inputPath`); HLS rungs 360/720/1080 (`video.go:21-25`); `cmd/worker/main.go:741-752`. `4k` is named only by the legacy web `src/hooks/useMediaVariants.ts:23` (not `TubePlayer`) and last in `bestRenditionKey`; no Android code names it. Offline and RSS take 720/480/360. The storyboard decodes the whole original (`storyboard.go:36,84-89`).
- **How to reproduce:** Upload 3840x2160: the quality menu tops out at 1080p on both clients. Time rungs with `TestTranscodePipelineTiming` on a machine with ffmpeg.
- **Impact:** The 4K encode costs about as much as all lower rungs together and serves no viewer; creators uploading 4K never see 4K. Feeds lv-4 and lv-13.
- **Fix:** (a) Skip the 4K MP4 until a 4K HLS rung exists (founder choice: or add 1440p/2160p HLS rungs). (b) Encode the remaining rungs in one ffmpeg run (`split` filter). (c) Storyboard from the 360p rendition.
- **Acceptance:** An args test: a 2160p plan produces no `4k` output and one ffmpeg invocation for the MP4 rungs (fails now: 5); the storyboard args name the 360p file.
- **Effort:** S for (a) and (c), M for (b)
- **Was:** LV-D-4, LV-A-8

### lv-15

- **ID:** lv-15
- **Title:** A failed MP4 rung (timeout, full disk, killed ffmpeg) is dropped without a log line; its `transcoding_jobs` row stays `processing` for ever, and the video goes ready without its 1080p or 4K rendition.
- **Severity:** P2
- **Category:** observability
- **Evidence level:** VERIFIED-READ
- **Evidence:** `processing/video.go:457-512`: `if err := TranscodeToMP4(...); err == nil { outputs = append(...) }`, error discarded, `TranscodeVideo` returns nil. `cmd/worker/main.go:747-766,777-782,832-843`: one job row per expected rung marked processing; only present outputs completed; the failed path runs only when `TranscodeVideo` errors.
- **How to reproduce:** Unit test with a fake `runFFmpeg` failing for 2160: no error, no `4k` output, no log.
- **Impact:** A 4K or 1080p upload silently plays lower; `/v1/media/:id/status` shows a rung processing for ever; support cannot tell dropped from slow. The most likely outcome for the largest uploads (lv-13).
- **Fix:** Log each rung error (media id, rung, last ffmpeg line), return per-rung errors, mark the matching row `failed`, count `media_transcode_rung_failures_total{rung}`; keep "ready with fewer rungs".
- **Acceptance:** A failing 2160p rung leaves the `4k` row failed with the ffmpeg line, the asset ready and the counter at 1.
- **Effort:** S
- **Was:** LV-E-4

### lv-16

- **ID:** lv-16
- **Title:** A resumable upload has a fixed 24 h life from init and orphan GC deletes its asset at 24 h, so a long-video upload that takes more than a day can never finish; a 16 GiB file is also cut into 3,277 sequential 5 MiB parts, and Android copies the whole file into app storage first and restarts from zero on expiry.
- **Severity:** P2
- **Category:** data / performance
- **Evidence level:** VERIFIED-READ (v1 confirmed); SUSPECTED for how often it happens (depends on the uplink)
- **Evidence:** `media-service/internal/service/resumable.go:38` (24 h) set once at init (`:158`); nothing updates `expires_at` (only the INSERT and SELECTs in `store/postgres/resumable.go:36,48,82,112`); parts refused after expiry (`resumable.go:250-252`). Orphan GC deletes `pending_upload` assets older than 24 h with no session check (`store/postgres/media.go:576-581`, `service/orphan_gc.go:43`). Chunk = max(5 MiB, total / 9,000) → 3,277 parts for 16 GiB (`resumable.go:21-30,53-71`). Android drops an expired session and starts again (`core/media/…/upload/ResumableUploader.kt:286-293,377`) and copies the picked file to `noBackupFilesDir` (`CH/ReelPublishStore.kt:306-330`). Web resumes only within one tab session (reels-39).
- **How to reproduce:** Init a 10 GiB resumable upload and send parts slowly so they span more than 24 h: the first part after hour 24 is refused, status says expired, the asset row is gone.
- **Impact:** A 16 GiB file needs about 1.6 Mbit/s sustained; creators on slow or intermittent links lose the upload, and each restart burns another init from the 10-per-hour budget. Android needs twice the file's size free.
- **Fix:** Sliding expiry: on each accepted part `expires_at = GREATEST(expires_at, now() + 24 h)`, capped at 7 days (below the bucket's abort rule); orphan GC skips assets with an open session; target about 1,000 parts; Android keeps a persistable URI grant instead of copying when the picker allows it.
- **Acceptance:** A part accepted at hour 23 moves `expires_at` to at least hour 47; orphan GC does not select an asset with an open session; 16 GiB plans at most 1,000 parts.
- **Effort:** S
- **Was:** LV-C-1, LV-A-13, LV-D-13

### lv-17

- **ID:** lv-17
- **Title:** The schedule worker publishes a long video at `publish_at` even while it is still transcoding; the `PostCreated` it emits finds the post pending, so the subscriber notification is dropped for good, and the post's `created_at` is set to the flip time.
- **Severity:** P2
- **Category:** data / functional
- **Evidence level:** VERIFIED-READ (v2 confirmed)
- **Evidence:** `ListDueScheduledPosts` selects on `publish_at`, deleted, not-note and no block only (`post-service/internal/store/postgres/scheduled.go:49-55`); the flip does not look at media or review state and writes `PostCreated` in the same transaction (`:130-163`). `PublishScheduled` checks standing and the title hold, not readiness (`service/schedule.go:384-490`); only "publish now" checks `UploadStatus` (`service/post_video_tools.go:186-208`). The fan-out then skips as in lv-1. Earliest schedule is 5 min ahead (`schedule.go:42-57`).
- **How to reproduce:** Upload a 2-hour video, schedule it for now + 10 min, subscribe a second account with the bell on: at the flip the post is pending; once approved, no notification.
- **Impact:** Premiere-style scheduled uploads, the main use of scheduling for long video, do not notify when the transcode outlasts the lead time.
- **Fix:** In the worker path, defer a due post whose video is not ready and leave `publish_at` set; show "waiting for processing" on the Scheduled list. The lv-1 re-announce also covers it.
- **Acceptance:** Worker test: a due post with processing media stays scheduled; after the media is ready the next tick publishes it with review status approved.
- **Effort:** S
- **Was:** LV-C-2

### lv-18

- **ID:** lv-18
- **Title:** Subscribers are never notified of a followers-only upload or of any upload by a private account, because the fan-out reads the post as an anonymous viewer and a 404 is a terminal skip.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** `notification-service/internal/service/fanout_eligibility.go:180-210,281-296` (no `X-User-Id`); 404 terminal (`:82-84`). post-service with a nil viewer: a private account fails `canViewAuthor` (`post-service/internal/service/post_visibility.go:85`), `followers` refuses a nil viewer (`:95-98`). So the followers branch at `fanout_eligibility.go:112-115` never runs.
- **How to reproduce:** B follows and subscribes to A (`notify_on=all`); A publishes a followers-only long video, or A is private and publishes a public one: B gets nothing.
- **Impact:** Followers-only audiences and private creators lose upload notifications entirely, scheduled uploads included.
- **Fix:** Read post state through an internal state route (visibility, review status, deleted, tier, age restriction) without the viewer gate, and keep the per-recipient follow and block checks in `eligible()`. Shared with lv-1 and lv-71.
- **Acceptance:** A followers-only post is delivered to a follower and skipped for a non-follower; a private author's approved follower is delivered.
- **Effort:** S
- **Was:** LV-V3-2

### lv-19

- **ID:** lv-19
- **Title:** Upload notifications to channel subscribers are lost for good after about 30 s of post-service or graph-service trouble: failed jobs are retried every 5 s with no backoff, failed after 5 attempts, and nothing replays them.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:** `notification-service/internal/service/subscriber_fanout.go:35-41,197-204,233-251,305-308`; `store/postgres/fanout_jobs.go:117-131` (claim any pending, increment), `:177-182` (release with no not-before), `:186-191` (fail terminal), `:224-228` (cleanup completed only); no re-queue tool for failed fan-out jobs. Eligibility errors burn an attempt (`fanout_eligibility.go:218-223`).
- **How to reproduce:** Unit test with a subscriber source that errors 6 times then succeeds: the job ends failed. On dev, stop post-service for 40 s right after publishing.
- **Impact:** Any post-service rollout (feed-42 ordering), failover or graph hiccup silently drops the push for every long video published in that window. No metric (lv-21).
- **Fix:** `available_at = now() + backoff` (30 s doubling to 30 min) on release and skip not-due rows; about 10 attempts; an admin re-queue for failed jobs like `partner_queue_ops.go:53`.
- **Acceptance:** Six consecutive failures then success delivers; attempts are spaced by the backoff.
- **Effort:** S
- **Was:** LV-E-5

### lv-20

- **ID:** lv-20
- **Title:** While any long video is transcoding anywhere, the stall sweeper stops recovering never-started requests and the Copyright Match fingerprint loop stops admitting work; with multi-hour long jobs that is most of the time.
- **Severity:** P2
- **Category:** reliability
- **Evidence level:** VERIFIED-READ (logic); SUSPECTED (share of time busy in prod)
- **Evidence:** A NULL-heartbeat asset is re-queued only when the pipeline is idle (`media-service/store/postgres/transcode_lease.go:142-151`); "busy" is any fresh heartbeat on any asset (`:294-305`) and the tracker is shared by both loops (`cmd/worker/main.go:181-188,247-249`). Fingerprints need the worker idle for 60 s and the oldest queued wait under 60 s, and are cancelled when any transcode starts (`cmd/worker/fingerprint_runner.go:147-148,300-319,372-385`).
- **How to reproduce:** Unit test of `ClassifyStalledTranscode`: a NULL-heartbeat asset queued 2 h with `pipelineBusy=true` is skipped.
- **Impact:** A lost request waits until no long video is transcoding anywhere (hours or days) instead of 30 minutes; Copyright Match evidence arrives late or never.
- **Fix:** Compute "busy" per class (heartbeats of the candidate's class only, split at 600 s); count only the short loop in the fingerprint admission check.
- **Acceptance:** A lost short request is re-queued while a long heartbeat is fresh; the fingerprint runner admits work while only the long loop is busy.
- **Effort:** S
- **Was:** LV-E-6

### lv-21

- **ID:** lv-21
- **Title:** Nothing would tell a person that long video is broken: transcode metrics have no class label and a duration histogram that ends at 10 minutes, the queue-age gauges are platform-wide, and progress writes, the schedule publisher and the subscriber fan-out have no signal beyond log lines and a service-wide error ratio.
- **Severity:** P2
- **Category:** observability
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/cmd/worker/main.go:40-48` (buckets 1..600 s; every long job in +Inf); oldest-pending and queue-oldest gauges are platform-wide MINs (`store/postgres/transcode_inbox.go:270-272`, `media_generation.go:179-185`); no class on `media_upload_ready_latency_seconds`. HTTP alerts are service-wide only (`charts/atpost-service/templates/prometheusrule.yaml:84-95`). `postschedule.Tick` and the fan-out log only, no counters. Worker metrics are not scraped at all (reels-5).
- **How to reproduce:** Render media-service prod and search the PrometheusRule for a transcode, schedule or fan-out rule: none.
- **Impact:** Every failure mode in lv-4 to lv-6, lv-15, lv-17 to lv-20 and reels-3's `forward_failed` is silent; the first signal is a creator's complaint.
- **Fix:** A `class` label and buckets to 48 h on the transcode metrics, ready latency and both age gauges; `post_schedule_publish_total{result}` and `notification_fanout_jobs_total{result}`; once reels-5 lands, rules for long oldest-waiting > 6 h, long failures, fan-out failed, schedule failures, and a per-path 5xx rule for `/v1/videos/:videoId/progress`.
- **Acceptance:** `render_test.sh` asserts the rules in prod values; a unit test reads the class label from a long and a short job.
- **Effort:** S
- **Was:** LV-E-7

### lv-22

- **ID:** lv-22
- **Title:** Every 10-second progress heartbeat runs the full post read gate before its upsert (3 Postgres statements, a graph-service call with no breaker, 2 Redis round trips), with no per-user or per-video limit beyond the edge's 60 rps, and the Redis mirror it writes saves nothing.
- **Severity:** P2
- **Category:** performance / abuse
- **Evidence level:** VERIFIED-READ (cost per request); SUSPECTED (saturation point; L3)
- **Evidence:** `post-service/internal/http/video_series_handler.go:990-1037` (`requirePostReadable` on every POST, `knownWatchDurationMs`, upsert, mirror); read gate `service/comment_counts.go:302-344`, `post_cache.go:94-107`, `post_visibility.go:78-87`, `privacy_gate.go:39,157-177` (graph `Can`, 3 s cache), `store/postgres/purge.go:52-71`. Graph client 5 s timeout, default transport, no breaker (`post-service/pkg/graphclient/*.go:58`). `GetWatchProgress` on a Redis hit reads Postgres anyway (`service/watch_progress.go:70-96`); when the DB row is gone it returns the cached hash, so a failed `DEL` after "clear history" leaves a stale resume point (Track C). No test counts or limits progress writes (LV-F-4).
- **How to reproduce:** L3 (20,000 viewers at 10 s); count statements per write with `pg_stat_statements`. Abuse: loop `POST /v1/videos/<id>/progress` at 60 rps from one token across many ids.
- **Impact:** About 0.3 × C statements/s and 0.1 × C graph calls/s for C playing viewers; one account can drive about 180 DB operations a second on the Postgres that serves every product; a graph slowdown turns into 5 s writes holding pool connections.
- **Fix:** Per (user, video) Redis `SET NX` with a 2 to 3 s TTL (answer 202 without writing); cache the gate's "readable" answer per (viewer, post) for 60 s for this route; delete the mirror or treat a DB miss as authoritative; optionally 30 s client cadence with forced saves on pause, end and leave.
- **Acceptance:** Handler test with counting fakes: 6 writes for one (viewer, post) within 60 s make 1 graph call and at most 7 statements (fails now: 6 and 18); a second write within the window does not reach the store; Redis hit + DB miss answers not found.
- **Effort:** S
- **Was:** LV-D-6, LV-B-8, LV-F-4 (and the stale-mirror clause of LV-C-4)

### lv-23

- **ID:** lv-23
- **Title:** `watch_progress` keeps one row per (viewer, video) for ever, and every heartbeat rewrites the indexed `last_watched_at`, so no update is HOT and the history index takes a new entry every 10 s per viewer.
- **Severity:** P2
- **Category:** performance / data growth / privacy
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/database/migrations/012_posttube_features.sql:96-106` (PK and `idx_watch_progress_user (user_id, last_watched_at DESC)`); `store/postgres/watch_progress.go:40-53` sets `last_watched_at = NOW()` on every write; delete paths only per row, per user and FK cascade (`:89-93,229-244`); no sweeper (map section 8; Track C grep). The Redis mirror expires after 90 d; the rows do not.
- **How to reproduce:** During L3 compare `n_tup_hot_upd` with `n_tup_upd` for the table (expected near 0) and watch the index grow.
- **Impact:** Rows grow as viewers × videos ever watched; bloat and autovacuum load grow with heartbeat traffic; viewing history is kept with no minimisation.
- **Fix:** A batched retention sweeper (window set by product, for example 2 years); per-table autovacuum settings; lv-22's cadence change cuts index churn.
- **Acceptance:** Sweeper integration test deletes rows past the window in bounded batches and keeps recent rows.
- **Effort:** S
- **Was:** LV-D-7, LV-C-4 (retention half)

### lv-24

- **ID:** lv-24
- **Title:** The watch page's default comment order, "top", computes a correlated `COUNT(*)` over `comment_reactions` for every top-level comment of the video on every page and pages with OFFSET.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ (query and default); SUSPECTED (plan; no EXPLAIN)
- **Evidence:** `post-service/internal/store/postgres/comments.go:341-357,395-404`; only `idx_comments_post (post_id, created_at DESC)` (`database/setup.sql:98`); "top" is the default on web (`src/features/posttube/components/WatchPage.tsx:442`) and Hub.
- **How to reproduce:** EXPLAIN ANALYZE `ListCommentsSorted(..., "top")` on a post with 50,000 top-level comments, pages 1 and 10 (L8).
- **Impact:** Comment cost for an evergreen popular video grows with its total comment count on every open; deep pages repeat or skip rows.
- **Fix:** Cache the first "top" page per post for 60 s; then a maintained `reaction_count`, a partial index and a keyset cursor.
- **Acceptance:** EXPLAIN shows an index scan with no sort; page 2 stable while a reaction is added.
- **Effort:** S (cache) / M (column and index)
- **Was:** LV-D-8

### lv-25

- **ID:** lv-25
- **Title:** Related videos (Up next) are rebuilt from scratch on every watch page open and every next page (seed lookup, three post-service listings, block set, ranker, hydration), the requested source sizes are cut from 60 to 20 by post-service, and the category source filters an unindexed column.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ (calls and caps); SUSPECTED (plan of the category scan)
- **Evidence:** `feed-service/internal/service/related.go:92-94,111-117,129-332`; post-service turns `limit > 50` into 20 (`post-service/internal/store/postgres/posts.go:1089-1091`, `author_sort.go:208-210`); `AND category = $n` with no index (`posts.go:1112-1116`, `migrations/047_posts_category_slug.sql:39-47`). Web calls it on every open (`src/features/posttube/watch/hooks/useWatch.ts:56-64`).
- **How to reproduce:** L1; trace one `/v1/feed/videos/:id/related` and count downstream calls; EXPLAIN the rare-category listing.
- **Impact:** Probably the most expensive call on the watch page, repeated per page; offset over a re-ranked pool repeats or skips videos.
- **Fix:** Cache the ranked id list per (viewer, seed, chip) for 10 min and hydrate only the page; ask for 50 per source; the partial index the migration comment names.
- **Acceptance:** Page 2 within 10 min makes no post-service listing call (fails now: 3); sources request `limit=50`.
- **Effort:** S
- **Was:** LV-D-9

### lv-26

- **ID:** lv-26
- **Title:** Android's channel search (`GET /v1/channels/search`) accepts a one-character query, matches `lower(name) LIKE '%q%'`, and orders by a correlated count of each matching channel's videos; since every account has a channel, a short query counts videos for most of the user base.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ (query and caller); SUSPECTED (plan)
- **Evidence:** `post-service/internal/store/postgres/channels.go:519-557`; `service/channels.go:471-478` refuses only empty input; trigram index only if `pg_trgm` installs and never for patterns under 3 characters (`migrations/043_channels_search.sql:20-26`); caller `feature/search/…/data/SearchApi.kt:37,88`.
- **How to reproduce:** EXPLAIN ANALYZE `SearchChannels("a", 20)` on 1 million channels.
- **Impact:** Search-as-you-type from a few viewers can load the database.
- **Fix:** Require 2 characters (3 for the infix arm) and order by `subscriber_count`, or move Android to search-service `/v1/search/channels`.
- **Acceptance:** `q=a` answers 400 or empty without a query; EXPLAIN shows no per-row subplan.
- **Effort:** S
- **Was:** LV-D-10

### lv-27

- **ID:** lv-27
- **Title:** On the web, any fatal hls.js network error, including an expired segment signature after a pause, switches to a progressive MP4 that starts again at 0:00, is signed for only 5 minutes and has no adaptive bitrate; the player never refreshes the manifest or reads `X-HLS-Expires-At`.
- **Severity:** P2 (P1 if reels-2 is confirmed, because then every production web view takes this path)
- **Category:** functional (playback) / performance
- **Evidence level:** VERIFIED-READ (handler, fallback and TTL); SUSPECTED (that hls.js escalates the 403 to fatal; browser range-request behaviour after the redirect)
- **Evidence:** `src/features/posttube/components/TubePlayer.tsx:319-383`: no `X-HLS-Expires-At` handling, no `loadSource` retry; every fatal non-media error calls `useFile()`, which sets `video.src` without carrying the position (`carryRef` filled only in the effect cleanup, `:389-390`; `seekAppliedRef` stays true, `:473`). `fileUrl` is `/v1/media/:id/serve`, redirected to 720p signed at `MaxProtectedTTL` = 5 min (`media-service/internal/service/playback.go:95,413-428`, `sign_cache.go:55-58`); the redirect is cacheable 60 s. Android handles expiry correctly (`core/media/…/HlsRefresh.kt:55-114,173-176`).
- **How to reproduce:** Open a 10-minute video (segments valid about 35 min), play to 5:00, pause 40 min, press play: it restarts at 0:00 or shows the error state. Or force a fatal hls.js error at 30 min on a 2 h video, pause 6 min, seek 1 h.
- **Impact:** Long sessions with pauses, the normal long-video case, lose their place or fail; lv-2's shorter TTL makes it happen sooner.
- **Fix:** On a fatal network error with a 403, or once `X-HLS-Expires-At` passes, save `currentTime`, `hls.loadSource(hlsUrl)` again and seek back; fall back to MP4 only on a manifest 404, and carry the position into `useFile()`; sign the `/serve` redirect for video variants with the segment TTL, or re-request it on a 403.
- **Acceptance:** Player test with a mocked 403 fragment: one manifest reload, playback resumes at the same time (fails now: 0). A media-service test: the viewer redirect for a 2 h video's 720p is signed for more than 5 min.
- **Effort:** S (position and reload) + M (signature)
- **Was:** LV-D-11, LV-V3-4

### lv-28

- **ID:** lv-28
- **Title:** `POST /v1/videos/:id/cover-frame` stores any string as the video's `thumbnail_url`, which every viewer then loads: it skips image moderation and works as a tracking pixel, the defect already fixed for chapter thumbnails and playlist covers.
- **Severity:** P2
- **Category:** input / visibility (unmoderated content, viewer privacy)
- **Evidence level:** VERIFIED-READ (Track B; v2 confirmed)
- **Evidence:** `post-service/internal/http/video_tools_handler.go:195-215` binds `ThumbnailURL *string`; `service/post_video_tools.go:135-172` checks only the owner, `verifyCoverOnWrite` runs only for `cover_media_id`. Served by `dto/video.go:67,87` and the related and end-screen cards (`service/hub_detail.go:192-194`). Web renders it unchanged (`src/lib/imageUrl.ts:74-83`; `UpNext.tsx:106`, `WatchPage.tsx:821`); pb-echo sets no CSP `img-src`. The web client can send it (`posttubeApi.ts:599-604`), though the studio does not. Compare `tube_audit.go:196-217,304-327`.
- **How to reproduce:** As a creator, `POST /v1/videos/<own>/cover-frame {"thumbnail_url":"https://attacker.example/p.gif"}` → 200; open a related card for it signed out: the browser requests the attacker host.
- **Impact:** Unscanned imagery on watch pages and other creators' end screens; IP and user-agent logging of everyone who sees the card.
- **Fix:** Run `thumbnail_url` through the chapter rule (own platform image, stored as `/serve`), or drop the field.
- **Acceptance:** An off-platform URL answers 400 or 422 and writes nothing; an own image id is stored as its `/serve` path; a stranger's image id answers 403.
- **Effort:** S
- **Was:** LV-B-3

### lv-29

- **ID:** lv-29
- **Title:** Editing a published video's title, description, tags or category never updates search, which keeps serving the old text until a manual reindex.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ (v2 confirmed)
- **Evidence:** The edit records the fields (`post-service/internal/store/postgres/post_edit.go:384-400`) but emits the search event only on visibility, credits-type or tier changes (`:253-266,473-480`); search-service has no post-updated case (`search-service/internal/events/consumer.go:439-1040`); results take the title from `_source`; reindex is manual.
- **How to reproduce:** Publish "Draft title", wait, edit to "Final title": `q=Final` finds nothing, `q=Draft` still finds it.
- **Impact:** Retitles and typo fixes have no effect in search; results differ from the watch page.
- **Fix:** Add `title`, `text`, `tags`, `hashtags`, `category` to the set that triggers `BumpSearchRevAndEmitTx` in `UpdatePostFields`.
- **Acceptance:** A title-only edit writes one `PostSearchEligibilityChanged` outbox row with the new title.
- **Effort:** S
- **Was:** LV-A-5

### lv-30

- **ID:** lv-30
- **Title:** A video longer than 4 hours is accepted, uploaded in full (up to 16 GiB) and only then refused by the worker, while the web studio tells long-video uploaders "Any length" and the founder's rule says long videos are unlimited.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ (v2 confirmed)
- **Evidence:** `media-service/internal/processing/limits.go:52-57,92-95` (4 h, with a comment that the founder said there is no limit); env override `cmd/worker/transcode_guards.go:68`, set nowhere. Init checks bytes only (`internal/http/resumable_handler.go:45-75`). `src/features/upload/fileRules.ts:23` "Any length"; no Android check. The failure rejects the post (reels-31).
- **How to reproduce:** Upload a 4 h 30 min recording: it uploads, shows processing to the author, then fails.
- **Impact:** Hours of upload wasted for streamers and event recordings; a stated product rule is not what ships.
- **Fix:** Founder decision. If 4 h stays: both clients read the local duration and refuse before init, and the studio says "up to 4 hours". If not: raise the cap with a matching segment TTL (lv-2) and job budget (lv-13).
- **Acceptance:** A client test that a 4 h 1 min file is refused before `init`, or a worker test at the new cap.
- **Effort:** S
- **Was:** LV-A-6

### lv-31

- **ID:** lv-31
- **Title:** Neither client can share a playlist, and the web playlist page requires sign-in, so a playlist cannot be sent to anyone outside the app; unlisted playlists, whose purpose is link sharing, are unusable.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** No share action in `T/ui/collections/*.kt` or `src/features/posttube/library/**`. Public web prefixes are `/posttube/watch`, `/posttube/channel/`, `/tube/` only (`src/middleware.ts:47,105,112,126`). The server serves public and unlisted playlists to anyone (`post-service/internal/service/playlists.go:56-60`); `/posttube/watch?v=&list=` is public.
- **How to reproduce:** Create a public playlist; look for Share; open `/posttube/playlists/<id>` in a private window.
- **Impact:** The "share it" step of the playlist journey does not exist.
- **Fix:** A Share action (system share sheet on Android, copy link on web) sharing `/posttube/watch?v=<first>&list=<id>`; optionally make `/posttube/playlists/` public.
- **Acceptance:** A signed-out browser opens the shared link and plays the first item with the playlist panel.
- **Effort:** S
- **Was:** LV-A-7

### lv-32

- **ID:** lv-32
- **Title:** Android plays no captions on network playback and cannot upload or manage caption tracks.
- **Severity:** P2
- **Category:** functional (parity, accessibility)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `T/ui/watch/WatchViewModel.kt:905-930` (captions only for offline copies); no Android call to `/v1/subtitles`. Server tracks are served behind the delivery gate (`media-service/internal/http/clips_handler.go:324-347`).
- **How to reproduce:** Upload a video with a caption track (once lv-8 is fixed); open it on Android: no CC control while streaming.
- **Impact:** Deaf and hard-of-hearing viewers on the main client cannot follow; breaks "Android is the super app".
- **Fix:** Fetch `GET /v1/subtitles/:mediaId` with the playback and add each track as a Media3 `SubtitleConfiguration`, reusing the existing captions flow; upload later.
- **Acceptance:** ViewModel test: a network playback with two server tracks offers both.
- **Effort:** M
- **Was:** LV-A-2 (wider form of reels-83)

### lv-33

- **ID:** lv-33
- **Title:** Android cannot publish a long video as Unlisted, so a creator who wants an unlisted video must publish it publicly first and change it afterwards.
- **Severity:** P2
- **Category:** functional (parity)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `CH/ReelSurface.kt:1425-1429` (Public, Followers, Private); the create allowlist excludes `unlisted` on purpose (`feature/post/…/data/dto/CreatePostDtos.kt:360-372`); the Edit sheet offers it (`core/feed/…/data/PostEditForm.kt:27-31`); the server accepts it (`post-service/internal/service/post_edit.go:626`).
- **How to reproduce:** Android, Create, Video: the Audience sheet has no Unlisted.
- **Impact:** The workaround puts the video in followers' feeds, trending and search until the edit lands.
- **Fix:** Offer Unlisted on the Video surface only.
- **Acceptance:** A create-hub test that the Video surface offers Unlisted and sends `visibility=unlisted`; the Reel surface does not.
- **Effort:** S
- **Was:** LV-A-3

### lv-34

- **ID:** lv-34
- **Title:** Android has no creator tools for chapters, series, end screens and cards, trim, private shares or bulk edit, all of which the web has.
- **Severity:** P2
- **Category:** functional (parity)
- **Evidence level:** VERIFIED-READ
- **Evidence:** "Editing, bulk edit, the end-screen editor and payouts stay on the web" (`T/ui/hub/CreatorHubScreen.kt:92-104`); no Android write call to `/chapters`, `/end-screens`, `/cards`, `/v1/video-series`, `/trim`, `/cover-frame`, `/private-shares`, `/v1/uploads/videos/bulk`; series read only (`T/data/VideoSeriesApi.kt:17`). Web has each (`src/features/posttube/hub/hubApi.ts:575-921,1634-1639`, `src/features/upload/studioApi.ts:204-274`).
- **How to reproduce:** On Android, try to add chapters, start a series or add an end screen.
- **Impact:** Breaks the founder rule; Android-only creators cannot use the features that drive watch time.
- **Fix:** In order: series at upload and manage, chapters in the Edit sheet, end-screen editor; bulk edit and trim last.
- **Acceptance:** Each tool has a ViewModel test against a fake API.
- **Effort:** L
- **Was:** LV-A-4

### lv-35

- **ID:** lv-35
- **Title:** No test proves that making a video private, deleting it or taking it down stops an open player or a copied stream URL, and the segment TTL test pins the 12 h gap in place.
- **Severity:** P2
- **Category:** tests / security
- **Evidence level:** VERIFIED-RUN (`TestHLSSegmentTTL` passes asserting 8 h 15 min and the 12 h cap; v2)
- **Evidence:** `media-service/internal/delivery/sign_cache_test.go:127-147`; the gate tests use `fakeAuthz{allow:...}` (`delivery/authz_test.go:97-104`); the post-side rule is tested separately (`post-service/internal/service/media_access_anonymous_integration_test.go:116-118`); no test flips visibility and re-requests the manifest or a segment.
- **How to reproduce:** `grep -rn "private" Architecture/services/media-service/internal/delivery/*_test.go`.
- **Impact:** Neither the lv-2 fix nor a regression of the manifest gate (for example a cached "yes") across the service boundary can be proven.
- **Fix (test to add):** A media-service integration test with an authorizer stub reading a shared fixture: manifest as B (200), set private, manifest again → 404 within the reuse window; a documented-expectation test for the segment window chosen in lv-2.
- **Acceptance:** Fails if a cached signature is served after the authorizer flips to deny, or if the playlist route skips the authorizer.
- **Effort:** M
- **Was:** LV-F-2 (long-video form of reels-67)

### lv-36

- **ID:** lv-36
- **Title:** Resume across devices is tested only as pure rules on the clients and as store ordering on the server; no test writes progress on one client and resumes from it on another, or drives the 10 s save cadence.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** Android `ResumeRuleTest` (pure), `WatchProgressPauseTest`; no `WatchViewModel` test of the cadence or of applying the read position at prepare (`T/ui/watch/WatchViewModel.kt:416-462,738-744,820-830`). Web only `src/features/posttube/__tests__/model.test.ts:28-80`. Server `tube_audit_integration_test.go:211`, `tube_audit_test.go:673`; nothing checks GET after a newer write whose mirror failed.
- **How to reproduce:** `grep -rln "saveProgress\|WatchProgressApi" mobile/android/feature/tube/src/test`.
- **Impact:** A cadence regression, a resume ignored at prepare or a stale mirror on device B ships green; the 90%/95% mismatch (lv-46) is pinned nowhere.
- **Fix (test to add):** Android ViewModel test with a fake clock (25 s → 2 saves; pause → save; reopen at 120 s → seek before play); web hook test with fake timers; post-service test of t1 then t2 with Redis failing → GET returns t2.
- **Acceptance:** Each fails when its code path is removed.
- **Effort:** M
- **Was:** LV-F-3

### lv-37

- **ID:** lv-37
- **Title:** Unlisted exclusion from trending, categories, topics, creator appearances, end-screen targets and `/v1/posts/batch` is correct in code but asserted by no behaviour test; the trending guard reads source text.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/store/postgres/feed_reads_integration_test.go:54-63` seeds only public rows; `feed_reads_guard_test.go:19,58` checks the printed source of `trendingQuery` for `ViewerEligibleSQL`, not the `visibility = 'public'` clause (`saved.go:281-283`). V3 Part 1.1 confirmed each surface correct in code today.
- **How to reproduce:** `grep -n "unlisted\|visibility" .../trending_query_test.go .../feed_reads_integration_test.go`.
- **Impact:** Loosening the public clause would leak unlisted long videos to every viewer with CI green.
- **Fix (test to add):** Seed unlisted, private and followers rows in `feed_reads_integration_test`; assert absence from trending, appearances and end-screen latest and popular.
- **Acceptance:** Fails when `p.visibility = 'public'` is removed from `trendingQuery`.
- **Effort:** S
- **Was:** LV-F-5

### lv-38

- **ID:** lv-38
- **Title:** The new-upload fan-out is tested at enqueue only; the delivery-time eligibility check for upload jobs has no test, which is why lv-1, lv-18 and lv-71 went unseen.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `notification-service/internal/events/subscriber_fanout_test.go:30-70` (enqueue gating); `service/fanout_eligibility.go:62,170` tested only for live jobs (`live_fanout_test.go:434-494`).
- **How to reproduce:** `grep -n "eligible(\|postStateFor" Architecture/services/notification-service/internal/service/*_test.go`.
- **Impact:** Changes to the per-recipient rule go uncaught; processing, private-after-enqueue, followers-only, age-restricted and members-only cases are unpinned.
- **Fix (test to add):** `TestUploadEligible` with `httptest` post and graph servers: public approved → true; private, deleted, rejected → false; processing → retry later (after lv-1); followers-only → follower true; post-service 5xx → error.
- **Acceptance:** Fails if `eligible` returns true without consulting the post state.
- **Effort:** S
- **Was:** LV-F-6

### lv-39

- **ID:** lv-39
- **Title:** Abandoned multipart cleanup is tested only at the store; the `AbortMultipartUpload` call, its failure and the user-cancel path are untested.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `reels_hardening_integration_test.go:179` covers `ExpireResumableUploads` only; `media-service/internal/service/deletion.go:216-231` has no test; no test checks the bucket rule (`infra/terraform/modules/media/main.tf:169-176`).
- **How to reproduce:** `grep -rln AbortMultipart --include=*_test.go`.
- **Impact:** A regression that stops aborting leaves 16 GiB of parts billed for up to 7 days, or for ever if the rule is dropped.
- **Fix (test to add):** Service test with a fake blob store: two expired sessions, one abort fails; both attempted, failure counted, retried next tick (after lv-57); a plan or conftest check for the lifecycle rule.
- **Acceptance:** Fails when the abort call is removed.
- **Effort:** S
- **Was:** LV-F-7

### lv-40

- **ID:** lv-40
- **Title:** No test or check ties the Kafka topics the code produces to the MSK topic list, so the missing `media.events.long` was not caught.
- **Severity:** P2
- **Category:** tests / config
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media.events.long` is in `Architecture/docker/init-topics.sh`, compose, prod values and `transcode_classes_test.go`, but not in `infra/terraform/modules/msk/topics-job/variables.tf:30-59`; no test compares the lists.
- **How to reproduce:** `grep -rln "media.events.long" --include=*.tf .` returns nothing.
- **Impact:** reels-3 shipped unnoticed; the next new topic will too.
- **Fix (test to add):** A Go test or CI script that collects every topic default and literal (including `intakeTopic + ".long"`) and asserts each is in `topics-job/variables.tf` and `init-topics.sh`. This is the test of reels fix plan item 2.
- **Acceptance:** Fails today; passes once the topic is added.
- **Effort:** S
- **Was:** LV-F-8

### lv-41

- **ID:** lv-41
- **Title:** The Android and web players have no test of quality selection, network captions or the create-time visibility list; Android has no instrumented tests.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (grep and file count)
- **Evidence:** No `PlayerQuality` test (`T/ui/watch/PlayerQuality.kt:14,32-52` untested); network captions unwired and unpinned (`WatchViewModel.kt:909-911`); no test of the create list (`CH/ReelSurface.kt:1425-1429`). Web `pickHlsLevel` tested only under reels; `TubePlayer.tsx` imported only for its settings menu (`src/features/posttube/watch/__tests__/presentation.test.tsx:12`). `src/androidTest` count 0.
- **How to reproduce:** `git ls-files | grep -iE "Quality.*Test"`.
- **Impact:** "Play at each quality" has no client proof.
- **Fix (test to add):** Android `PlayerQualityTest` (tracks {360,720,1080} → [Auto,1080,720,360]; picking 720 sets the override; MP4 → Auto only); a web `TubePlayer` test with a fake hls.js levels list.
- **Acceptance:** Fails when the override is not applied.
- **Effort:** S
- **Was:** LV-F-10

### lv-42

- **ID:** lv-42
- **Title:** Worker crash recovery during a long transcode is proven only at the SQL and loop level, never by killing a running worker and checking the job completes exactly once.
- **Severity:** P2
- **Category:** tests / reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/.../transcode_lease_integration_test.go:172,263,358,413`, `transcode_lease_test.go` (fakes); no test runs ffmpeg and kills it (`grep -rn "Kill\|SIGKILL\|os.Process" --include=*_test.go .../cmd/worker`). Orphaned processing jobs happened before on dev.
- **How to reproduce:** The grep above.
- **Impact:** A heartbeat that outlives ffmpeg, or a lease never reclaimed on the long loop, would leave long videos stuck processing; lv-6 has no test either.
- **Fix (test to add):** In the lv-10 domain, `TestE2E_LongVideo_WorkerCrashRecovers`: kill the worker after the first heartbeat, start a new one with a short stall setting, assert ready with one completion and one set of variants; plus the lv-6 unit test.
- **Acceptance:** Red when the sweeper is disabled.
- **Effort:** M
- **Was:** LV-F-13

---

## P3

### lv-43

- **ID:** lv-43
- **Title:** A signed-out viewer of a public watch page gets no related videos or Up next, because the related route requires a user.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** `feed-service/internal/http/related.go:90-95` answers 401 without `X-User-Id`; the web treats any failure as empty (`src/features/posttube/watch/watchApi.ts:260-266`); the watch page is public.
- **How to reproduce:** Open `/posttube/watch/<id>` in a private window: Up next is empty.
- **Impact:** Shared links lead nowhere after the first video.
- **Fix:** Allow an empty viewer, ranked without viewer signals, public and approved only.
- **Acceptance:** Handler test: an anonymous request returns only public rows.
- **Effort:** S
- **Was:** LV-A-9

### lv-44

- **ID:** lv-44
- **Title:** The server accepts a long video without a category, although both clients require one.
- **Severity:** P3
- **Category:** functional (data quality)
- **Evidence level:** VERIFIED-READ
- **Evidence:** Only the override route requires it (`post-service/internal/http/video_tools_handler.go:147`); clients `CH/ReelSurface.kt:602-603`, `src/features/upload/validation.ts:95-96`.
- **How to reproduce:** `POST /v1/posts` with `content_type=long_video` and no category → 201.
- **Impact:** Category chips and topic pages miss videos from older clients and API callers.
- **Fix:** 422 `CATEGORY_REQUIRED` for `long_video` create, live replays exempt.
- **Acceptance:** Handler test for the refusal and the exemption.
- **Effort:** S
- **Was:** LV-A-10

### lv-45

- **ID:** lv-45
- **Title:** "Pause watch history" is stored only on the device where it was set (Android DataStore, web localStorage), so every other device keeps recording and feeding recommendations, and the web flag survives sign-out.
- **Severity:** P3
- **Category:** privacy (DPDP consent expectation)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `T/data/WatchProgressRepository.kt:41-44`; `src/features/posttube/library/historyPause.ts:7-44`; `SaveWatchProgress` has no pause check (`post-service/internal/http/video_series_handler.go:990-1037`, `service/watch_progress.go:44-69`); Android resets the flag at sign-out (`TubeTeardown.kt:23-26`), web does not.
- **How to reproduce:** Pause history on Android; watch on the web: `/v1/videos/history` lists the video.
- **Impact:** A viewer who paused history believes nothing is recorded.
- **Fix:** A server-side `watch_history_paused` preference; `SaveWatchProgress` answers 204 without writing; both clients read and write it.
- **Acceptance:** Handler test: with the preference set, POST `/progress` writes no row.
- **Effort:** M
- **Was:** LV-A-11, LV-B-7, LV-C-4 (pause half)

### lv-46

- **ID:** lv-46
- **Title:** "Finished" means 90% on the server and web but 95% on Android, so a video stopped between 90% and 95% leaves Continue watching while Android still resumes it near the end.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/http/video_series_handler.go:1061-1073`; `src/features/posttube/model.ts:38-39`; `T/ui/watch/ResumeRule.kt:13-25`.
- **How to reproduce:** Stop at 92% on Android; the web row is gone and Android reopens at 92%.
- **Impact:** Inconsistent resume across devices at the end credits.
- **Fix:** Use 90% in `ResumeRule.kt`.
- **Acceptance:** The Android table test uses 90%.
- **Effort:** S
- **Was:** LV-A-12

### lv-47

- **ID:** lv-47
- **Title:** The only generated thumbnail is a 150x150 letter-boxed square, which is all a cover-less long video, such as a live replay, has to show.
- **Severity:** P3
- **Category:** functional (quality)
- **Evidence level:** SUSPECTED (generation read; which variant each client card shows, and whether the replay path sets a cover, not traced)
- **Evidence:** `media-service/internal/processing/video.go:136-146,391-396`; replays created server-side (`post-service/internal/service/live_vod.go:159`).
- **How to reproduce:** Make a live replay public and view it in a 16:9 grid.
- **Impact:** Blurry, boxed posters on channel pages and feeds.
- **Fix:** A 1280-wide 16:9 poster for long-class sources in the same pass, preferred over `thumb_150`.
- **Acceptance:** Worker test: a long-class transcode records the poster variant.
- **Effort:** S
- **Was:** LV-A-14

### lv-48

- **ID:** lv-48
- **Title:** Subscribing to a private account's channel records the subscription while the follow is only "requested", raising the public subscriber count.
- **Severity:** P3
- **Category:** authz / privacy
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/service/channel_subscriptions.go:152-163,255-289` (`Subscribe` runs regardless of `followStatus`). The suspected notification leak does not happen: the anonymous fan-out read gets 404 for a private account's posts (V3 1.3; that is lv-18).
- **How to reproduce:** B is private; A subscribes (follow requested): B's `subscriber_count` rises and A is on the subscriber-id list.
- **Impact:** Wrong counts; the requester sits on the fan-out list of an owner who never approved them.
- **Fix:** Write the row only when the follow is accepted; promote it on follow-accept.
- **Acceptance:** Service test: "requested" writes no row and returns requested.
- **Effort:** S
- **Was:** LV-B-4

### lv-49

- **ID:** lv-49
- **Title:** The channel RSS feed gives a downloadable MP4 of every public long video to anyone, ignoring the video's `allow_download` setting, with no channel opt-out; shared caches keep an item up to 70 min after the video goes private.
- **Severity:** P3
- **Category:** privacy / policy
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/service/channel_feed.go:186-227` (no `allow_download` read); `src/app/posttube/channel/[handle]/feed.xml/route.ts:21` (`s-maxage=600, stale-while-revalidate=3600`). Founder rule: viewers never get a file; offline grants do require `allow_download` (`offline_copies.go:386`).
- **How to reproduce:** Set `allow_download=false` on a public video; fetch the channel `feed.xml`: the item carries an enclosure.
- **Impact:** A creator's "no downloads" choice is not honoured, and a public directory of direct files is published.
- **Fix:** Omit the enclosure when `allow_download` is false; a per-channel `rss_enabled` switch; drop stale-while-revalidate to ≤ 600 s.
- **Acceptance:** `channel_feed` unit test: `allow_download=false` produces no enclosure.
- **Effort:** S
- **Was:** LV-B-5

### lv-50

- **ID:** lv-50
- **Title:** Android offline copies are stored unencrypted and kept 48 h after sign-out; expiry is checked on the device clock.
- **Severity:** P3
- **Category:** privacy (at rest)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `SimpleCache(... NoOpCacheEvictor(), database)` with no cipher sink (`OfflineCopies.kt:606-618`, `OfflineMediaModule.kt:74`); `holdForSignOut` deletes only unfinished copies (`OfflineRules.kt:102,135-141`); the streaming cache is not torn down (reels-20, `MediaModule.kt:51`).
- **How to reproduce:** Save a members-only video offline, sign out, inspect app data on a rooted or debug build.
- **Impact:** On a shared or rooted phone the next user gets watched and paid video; "viewers never get a file" holds only on non-rooted devices.
- **Fix:** Delete stored copies at sign-out; AES cipher sink and source with a Keystore key; DRM long term.
- **Acceptance:** Unit test: `holdForSignOut` leaves no copy bytes.
- **Effort:** S (delete) / M (encryption)
- **Was:** LV-B-6

### lv-51

- **ID:** lv-51
- **Title:** The self-service data export covers auth-service data only; watch history, subscriptions, playlists, dislikes and comments are in no export.
- **Severity:** P3
- **Category:** privacy (DPDP right to information)
- **Evidence level:** VERIFIED-READ (export scope); UNVERIFIED (any off-platform process)
- **Evidence:** `identity-platform/services/auth-service/internal/http/me.go:181-196`; `admin-service/internal/service/exports.go:11-23` only records a request; no post-service export route.
- **How to reproduce:** Request an export for a user with history and subscriptions; none appear.
- **Impact:** An access request cannot be answered from the product for viewing data.
- **Fix:** `/internal/users/:id/export` in post-service, included in the bundle.
- **Acceptance:** Integration test: the export for a seeded user contains progress rows and subscriptions.
- **Effort:** M
- **Was:** LV-B-10

### lv-52

- **ID:** lv-52
- **Title:** A playlist can list posts its owner cannot see, and readers of a public playlist get the `post_id` of rows they may not open.
- **Severity:** P3
- **Category:** visibility (existence and id disclosure)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/service/playlists.go:236-254` (ownership only); `:317-321,355-364` keeps unreadable rows with `Post` nil.
- **How to reproduce:** Add a stranger's private post id to your public playlist; signed-out `GET /v1/playlists/<id>/items` returns `{"post_id":"<id>","post":null}`.
- **Impact:** Confirms that a private or deleted video exists; the id unlocks nothing.
- **Fix:** Refuse adds of unreadable posts; drop nil rows for non-owners.
- **Acceptance:** Adding an unreadable post answers 404; a non-owner's page has no nil rows.
- **Effort:** S
- **Was:** LV-B-11

### lv-53

- **ID:** lv-53
- **Title:** The watch-progress ordering guard (`client_sent_at`, migration 081) is inert because neither client sends `sent_at_ms`, so a late heartbeat or a second device's first save can move the resume point back and clear "completed".
- **Severity:** P3
- **Category:** data
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/store/postgres/watch_progress.go:39-60`; handler reads `sent_at_ms` (`internal/http/video_series_handler.go:965-988,1030`); Android `T/data/WatchProgressApi.kt:34-39` and web `posttubeApi.ts:409-418` send only position, duration, completed. A clock running behind is not clamped.
- **How to reproduce:** Watch to 95% on web; open on Android: its first 10 s save puts the video back in Continue watching at 0:10.
- **Impact:** Resume points jump backwards across devices and after retries.
- **Fix:** Send `sent_at_ms` from both clients.
- **Acceptance:** Client tests assert the field on the wire; a server test refuses the earlier timestamp.
- **Effort:** S
- **Was:** LV-C-3

### lv-54

- **ID:** lv-54
- **Title:** `playlists.item_count` and `video_series.episode_count` count items viewers cannot see and are never recomputed when a post is purged, so shown counts drift up.
- **Severity:** P3
- **Category:** data
- **Evidence level:** VERIFIED-READ
- **Evidence:** Recomputed only on add, move, remove (`post-service/internal/store/postgres/playlists.go:406-408,430-432`, `video_series.go:199-201,269-271`); `PurgePost` relies on cascade (`posts_lifecycle.go:310-318`); raw counts served on end-screen cards and the search source (`service/end_screens.go:996`, `http/channels_internal_list.go:161`).
- **How to reproduce:** Purge a post that is in a playlist; `item_count` is unchanged.
- **Impact:** "12 videos" on a playlist that shows 9. Display only.
- **Fix:** Recompute counts for affected playlists and series inside `PurgePost` and the account purge.
- **Acceptance:** Integration test: purge decrements `item_count`.
- **Effort:** S
- **Was:** LV-C-5

### lv-55

- **ID:** lv-55
- **Title:** user-service still writes the shared `channels` table through two gateway-reachable routes that bypass post-service's rules and outbox; `ensure-publisher` fails for every account whose channel post-service created.
- **Severity:** P3
- **Category:** data
- **Evidence level:** VERIFIED-READ (no client calls either route today)
- **Evidence:** `POST /v1/users/me/channels` and `POST /v1/onboarding/ensure-publisher` (`user-service/internal/http/handler.go:183-187,210`, `store/channels.go:56-71`, `store/publisher.go:118-180`); post-service never sets `is_default` (`041_channels.sql:43`), so a second insert hits `uq_channels_user` → 500. Table definitions differ (Track C).
- **How to reproduce:** Call `ensure-publisher` for an account with a post-service channel → 500.
- **Impact:** Latent; a new client or script would create unindexed channels or get 500s.
- **Fix:** Answer 410 `ROUTE_RETIRED` on both, as for subscriptions.
- **Acceptance:** Handler tests return 410; no `INSERT INTO channels` remains in user-service.
- **Effort:** S
- **Was:** LV-C-6

### lv-56

- **ID:** lv-56
- **Title:** Upload notifications for a members-only long video go to every subscriber, members or not, with the title.
- **Severity:** P3 (founder decision: upsell or leak)
- **Category:** data / product
- **Evidence level:** VERIFIED-READ (V3 1.3 confirmed delivery passes; the redaction keeps the title)
- **Evidence:** Enqueue and delivery check no tier (`notification-service/internal/events/subscriber_fanout.go:33-110`, `service/fanout_eligibility.go:61-126,186-199`); members-only redaction keeps the title (`post-service/internal/service/members_read.go:33-45`).
- **How to reproduce:** Publish a members-only video; a non-member subscriber gets "X uploaded <title>" and lands on a locked page.
- **Impact:** Consistent with "title is public"; may read as a leak or spam to non-members.
- **Fix:** If not intended, skip at enqueue when `tier_required_id` is set, or check membership per recipient.
- **Acceptance:** Consumer test for the chosen rule.
- **Effort:** S
- **Was:** LV-C-7

### lv-57

- **ID:** lv-57
- **Title:** An expired resumable session's multipart abort is tried once and never retried, and there is no user-cancel route, so an abandoned upload holds storage for at least 24 h and, on an abort failure, until the bucket's 7-day rule (for ever on MinIO).
- **Severity:** P3
- **Category:** data (cost)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media-service/internal/store/postgres/resumable.go:104-131`; `service/deletion.go:217-231` logs and moves on; routes are init, status, chunk, complete only (`internal/http/resumable_handler.go:19-22`); bucket rule `infra/terraform/modules/media/main.tf:169-176`.
- **How to reproduce:** Make one abort fail; the session is `expired` and never tried again.
- **Impact:** Storage cost only.
- **Fix:** On abort failure write a reclaim row for retry; `DELETE /v1/media/upload/resumable/:id` (owner) that aborts at once.
- **Acceptance:** Abort fails once → a reclaim row exists and the next tick aborts.
- **Effort:** S
- **Was:** LV-C-8

### lv-58

- **ID:** lv-58
- **Title:** Staging and both Azure environments behave differently from prod on long video (views counted on `play_end`, scanner and fingerprinting off, 6 GiB cap), and QA, the prod-equal environment, cannot take a long original over 6 GiB.
- **Severity:** P3 (P2 if Azure prod pays creators)
- **Category:** deployment / data
- **Evidence level:** VERIFIED-RUN (prod/QA/staging key diff, Track E) / VERIFIED-READ (defaults)
- **Evidence:** `ANALYTICS_VIEW_SOURCE: sessions` only in prod and QA (`deploy/services/analytics-service/values-prod.yaml:56`, `values-qa.yaml:65`), default `play_end` (`analytics-service/internal/viewsource/viewsource.go:42-51`); staging lacks the copyright, CDN and Kafka TLS keys; Azure prod has the scanner off; QA `MEDIA_WORKER_MAX_ORIGINAL_BYTES` 6 GiB (`values-qa.yaml:124-126`).
- **How to reproduce:** Diff the values files' env blocks.
- **Impact:** A rehearsal on staging or Azure validates the wrong view counts and copyright behaviour; QA cannot exercise lv-6 and lv-13.
- **Fix:** Bring staging and Azure to prod's keys or delete them (feed-77); one measured cap in QA and prod (lv-13, reels-62).
- **Acceptance:** `render_test.sh`'s drift guard extended to staging; QA and prod carry the same cap.
- **Effort:** S
- **Was:** LV-C-9, LV-E-10

### lv-59

- **ID:** lv-59
- **Title:** The schedule worker reads the 100 oldest due posts every tick; posts in standing backoff or failing a transient check stay at the head and can starve newer due posts.
- **Severity:** P3
- **Category:** reliability
- **Evidence level:** SUSPECTED (needs more than 100 failing at once)
- **Evidence:** `post-service/internal/store/postgres/scheduled.go:49-55` (`ORDER BY publish_at ASC LIMIT 100`); `publish_standing.go:162-190`; `schedule.go:425-435`; `postschedule/worker.go:117-135`.
- **How to reproduce:** Worker test: 100 always-failing due posts plus one healthy one.
- **Impact:** After a trust-safety or copyright outage, newer premieres publish late.
- **Fix:** Persist `next_attempt_at` and exclude it in the due query, or page past failures.
- **Acceptance:** The healthy post publishes in the same tick.
- **Effort:** S
- **Was:** LV-C-10

### lv-60

- **ID:** lv-60
- **Title:** post-service applies its migrations on every replica boot without the advisory lock, and `channels` is missing from its schema-dependency gate although migration 012 references it.
- **Severity:** P3
- **Category:** data / deployment
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/store/postgres/bootstrap.go:27-33` uses `migrationrunner.Run` (no lock, `Architecture/shared/store/migrationrunner/runner.go:44-110`) not `RunExclusive`; no migration job (`deploy/services/post-service/values-prod.yaml:144-145`); gate lists `media_assets`, `user_preferences`, `users` (`:158-164`); 012 references `channels(id)`. Platform-wide pattern (Track C note).
- **How to reproduce:** Boot two replicas with a new non-idempotent migration; or boot post-service before user-service on an empty database.
- **Impact:** A crash-loop until the other replica commits; on a brand-new environment (the AWS move) post-service can fail at 012.
- **Fix:** `RunExclusive`; add `channels` to the gate.
- **Acceptance:** Render shows `channels` in the gate; two concurrent bootstraps on a fresh database succeed.
- **Effort:** S
- **Was:** LV-C-11

### lv-61

- **ID:** lv-61
- **Title:** The "Loved" system collection mirrors long-video likes best-effort, outside the like's write; a failed mirror write is never repaired.
- **Severity:** P3
- **Category:** data
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/service/system_playlists.go:107-125` logs and ignores; called after the reaction write (`like_toggle.go:200`, `post_reactions.go:45,101`); no reconciler.
- **How to reproduce:** Fail the mirror store once; the liked video is missing from Loved.
- **Impact:** Liked state and Loved drift with no self-healing.
- **Fix:** Derive Loved from reactions at read time, or write it in the same transaction.
- **Acceptance:** Mirror fails once → Loved still contains the post.
- **Effort:** S
- **Was:** LV-C-12

### lv-62

- **ID:** lv-62
- **Title:** The web player's hls.js setup is a reels setup: an unlimited back buffer and no cap to the player size, so long sessions keep growing media buffers and a mini-player downloads 1080p.
- **Severity:** P3
- **Category:** performance
- **Evidence level:** VERIFIED-READ (config; hls.js 1.7.3 default `backBufferLength: Infinity`)
- **Evidence:** `src/features/posttube/components/TubePlayer.tsx:358-364` (`maxBufferLength: 30, capLevelToPlayerSize: false`, no `backBufferLength`). The expiry half is lv-27.
- **How to reproduce:** Play a 2 h video for 90 min; record `video.buffered` and tab memory.
- **Impact:** Memory growth on long sessions; wasted bandwidth in the mini-player.
- **Fix:** `backBufferLength: 90`, `capLevelToPlayerSize: true` unless a quality is pinned.
- **Acceptance:** Unit test of the hls config factory.
- **Effort:** S
- **Was:** LV-D-14

### lv-63

- **ID:** lv-63
- **Title:** Moving one item in a playlist rewrites the whole playlist, up to 5,000 rows, one INSERT per row inside a transaction holding the playlist's row lock.
- **Severity:** P3
- **Category:** performance
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/store/postgres/playlists.go:205-288,329`.
- **How to reproduce:** Time `MovePlaylistItem` on a 5,000-item playlist.
- **Impact:** A drag in a large playlist makes 5,000 round trips and blocks other writes on it.
- **Fix:** Shift only the rows between the two positions, or one `INSERT ... SELECT FROM unnest(...)`.
- **Acceptance:** At most 5 statements for a move.
- **Effort:** S
- **Was:** LV-D-15

### lv-64

- **ID:** lv-64
- **Title:** The web watch page fires about 18 API reads per open, besides the playlists, including the uncached related call (lv-25).
- **Severity:** P3
- **Category:** performance
- **Evidence level:** VERIFIED-READ (hook list; count not measured)
- **Evidence:** `src/features/posttube/components/WatchPage.tsx:167-252`, `watch/hooks/useWatch.ts:47-64`.
- **How to reproduce:** Count requests before the first frame in the network panel.
- **Impact:** Slower first frame on mobile web; per-view backend load many times the playlist cost.
- **Fix:** Defer below-the-fold reads until `playing`; later a composite `GET /v1/videos/:id/watch`.
- **Acceptance:** At most 8 requests before the first `playing` event.
- **Effort:** S
- **Was:** LV-D-16

### lv-65

- **ID:** lv-65
- **Title:** A rebalanced duplicate of a running long job parks the other worker's long loop for the whole job behind a blocking advisory lock, held on one pooled connection for up to 48 hours.
- **Severity:** P3
- **Category:** reliability
- **Evidence level:** SUSPECTED (rebalance behaviour of kafka-go not verified)
- **Evidence:** `media-service/internal/store/postgres/media_outbox.go:174-195` (`pg_advisory_lock`, blocking, on a dedicated connection); `cmd/worker/main.go:291-301`. A dropped connection releases the lock silently; the inbox still prevents a double completion (V3 PASS d7).
- **How to reproduce:** Two long workers on dev (`--profile split-transcode`), restart one during a long job, check `pg_locks`.
- **Impact:** Halves long throughput for hours; a duplicate multi-hour encode on a dropped connection. No correctness loss.
- **Fix:** `pg_try_advisory_lock`; back off or commit when another worker holds it.
- **Acceptance:** With the lock held, a second delivery returns promptly without transcoding.
- **Effort:** S
- **Was:** LV-E-8

### lv-66

- **ID:** lv-66
- **Title:** There is no switch to stop long-video intake in an incident; the levers that exist act after the creator has uploaded the file.
- **Severity:** P3
- **Category:** deployment
- **Evidence level:** VERIFIED-READ
- **Evidence:** No `TUBE*|LONG*|POSTTUBE*|MTUBE*` env in Go services; Android `ProductGate` leaves `LauncherApp.TUBE` ungated (`app/…/features/ProductGate.kt:26-28`); no web PostTube flag (`src/lib/productFeatures.ts:31-44`). `MEDIA_TRANSCODE_CLASSES=short` only stops the loop; the duration cap applies after upload.
- **How to reproduce:** Read the files.
- **Impact:** During a long-transcode incident (lv-6, reels-3) the only stop is a code change or a size cap that also blocks big reels.
- **Fix:** `LONG_VIDEO_UPLOADS_ENABLED` in post-service create and draft publish (503 `UPLOADS_PAUSED`) and media resumable init; document in the alerts runbook.
- **Acceptance:** With the flag false, a `long_video` create answers 503 and a flick create succeeds.
- **Effort:** S
- **Was:** LV-E-9

### lv-67

- **ID:** lv-67
- **Title:** The chart's static QA guard now fails twice: besides the media cap (reels-62), post-service QA carries a prod host in `ACTION_LINK_OWN_DOMAINS`.
- **Severity:** P3
- **Category:** deployment
- **Evidence level:** VERIFIED-RUN
- **Evidence:** `QA_GUARDS_ONLY=1 bash charts/atpost-service/render_test.sh` → "prod host in a QA file: deploy/services/post-service/values-qa.yaml:121: ACTION_LINK_OWN_DOMAINS" and "STATIC GUARDS FAILED", exit 1 (baseline row 8).
- **How to reproduce:** Run the command.
- **Impact:** A red guard hides the next real drift.
- **Fix:** Allow the key in the guard if the own-domain list is the same everywhere, or set the QA host.
- **Acceptance:** The command ends without STATIC GUARDS FAILED once reels-62 is also fixed.
- **Effort:** S
- **Was:** LV-E-11

### lv-68

- **ID:** lv-68
- **Title:** Long-video failure messages are thin: the web says "may still be processing" for every playback failure and offers no retry, and Android's watch page shows "Still processing" for a video whose transcode failed.
- **Severity:** P3
- **Category:** reliability (user feedback)
- **Evidence level:** VERIFIED-READ (web, Android hub); SUSPECTED (Android watch page for a failed transcode)
- **Evidence:** `src/features/posttube/components/TubePlayer.tsx:825-830`; `T/ui/watch/WatchScreen.kt:609-614`; the Creator Hub does show "Processing failed" (`T/ui/hub/CreatorHubScreen.kt:507,751`).
- **How to reproduce:** Web: stop post-service on dev and open a watch page. Android: open one's own failed upload.
- **Impact:** Viewers wait for processing that already finished; creators are told the wrong state.
- **Fix:** Web retry button and a distinct 503 line; Android maps failed or rejected to "Processing failed".
- **Acceptance:** Web overlay test with retry; Android ViewModel test for a failed status.
- **Effort:** S
- **Was:** LV-E-12

### lv-69

- **ID:** lv-69
- **Title:** `TestUnwritableQuarantineDoesNotCommit` (media worker) depends on wall-clock timing and failed once under parallel load.
- **Severity:** P3
- **Category:** tests
- **Evidence level:** VERIFIED-RUN
- **Evidence:** First run: `transcode_guards_test.go:95: quarantine tried 1 times`; 7 of 7 re-runs passed; 50 ms context at `:88`.
- **How to reproduce:** `go test -cpu 1 -count 50 -run TestUnwritableQuarantineDoesNotCommit ./cmd/worker`.
- **Impact:** Random red CI jobs on a busy runner.
- **Fix:** Inject a clock or attempt counter, or raise the deadline to 500 ms.
- **Acceptance:** `-count 200 -cpu 1` is green.
- **Effort:** XS
- **Was:** LV-F-11

### lv-70

- **ID:** lv-70
- **Title:** Part of the web posttube suite asserts source or CSS text rather than behaviour.
- **Severity:** P3
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (count); per-assertion nature SUSPECTED
- **Evidence:** 15 of 53 posttube and upload test files call `readFileSync` (for example `src/features/posttube/watch/__tests__/presentation.test.tsx:17-18`).
- **How to reproduce:** `grep -c readFileSync` over the test files.
- **Impact:** Tests pass with broken behaviour and inflate the 818-pass figure.
- **Fix:** Replace behaviour assertions on source text with rendered-DOM or hook tests; keep design-token CSS checks.
- **Acceptance:** No posttube test reads a `.ts` or `.tsx` source file.
- **Effort:** M
- **Was:** LV-F-12

### lv-71

- **ID:** lv-71
- **Title:** An age-restricted upload makes every recipient's fan-out fail and retry until the job's attempts are exhausted, because the anonymous post read answers 401/403, which the fan-out treats as an error.
- **Severity:** P3
- **Category:** reliability / functional
- **Evidence level:** VERIFIED-READ (status mapping); SUSPECTED (exact retry count)
- **Evidence:** `post-service/internal/service/post_reads.go:62` (age gate); `notification-service/internal/service/fanout_eligibility.go:203-204`; 5 attempts per job.
- **How to reproduce:** Publish an age-restricted public long video with subscribers; watch the rows fail.
- **Impact:** No notification and wasted reads per recipient per attempt.
- **Fix:** The internal state route of lv-18; decide whether adult subscribers are notified.
- **Acceptance:** An age-restricted post yields a terminal decision, not an error.
- **Effort:** S
- **Was:** LV-V3-3 (also v2 side observation)

### lv-72

- **ID:** lv-72
- **Title:** The owner can edit a `staged` post straight to `public`, skipping the reviewer promotion step.
- **Severity:** P3
- **Category:** authz (workflow bypass), latent
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/store/postgres/post_edit.go:137-152` never checks the current visibility; promotion is scoped to `staged` (`store/postgres/posts.go:1598-1617`); no current create path writes `staged`.
- **How to reproduce:** With a post in `staged`, owner PATCH `{"visibility":"public"}` → 200.
- **Impact:** None today; reopens the bypass if staged rollout returns.
- **Fix:** 409 `STAGED_UNDER_REVIEW` on a visibility edit from `staged`.
- **Acceptance:** Service test for the refusal.
- **Effort:** S
- **Was:** LV-V3-6

### lv-73

- **ID:** lv-73
- **Title:** Post edits never check author standing, so a suspended or long-video-paused creator can still make a private video public or retitle it.
- **Severity:** P3
- **Category:** authz (moderation enforcement)
- **Evidence level:** VERIFIED-READ (no call); SUSPECTED (whether readers hide a suspended author's posts anyway)
- **Evidence:** `requirePublishStanding` at create (`post-service/internal/service/post_create.go:55`) and scheduled publish (`schedule.go:408-418`), not in `service/post_edit.go`; title edits not re-run through the copyright title review.
- **How to reproduce:** Suspend a creator; the creator PATCHes a private video to public → 200.
- **Impact:** A publishing sanction does not cover publishing by edit.
- **Fix:** Call `requirePublishStanding` when an edit widens visibility or changes a long video's title.
- **Acceptance:** A suspended author's private-to-public edit answers 403.
- **Effort:** S
- **Was:** LV-V3-7

### lv-74

- **ID:** lv-74
- **Title:** Flick series (`/v1/series`) let an owner attach anyone's post as an episode, silently overwrite episodes, and are read without an author gate.
- **Severity:** P3
- **Category:** authz
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/service/series.go:33-45` (series owner only); `store/postgres/series.go:83-89` (`ON CONFLICT ... DO UPDATE`); reads `series.go:18,29` without `canViewAuthor`. No client calls `/v1/series` (reels-102).
- **How to reproduce:** `POST /v1/series/<own>/episodes {"post_id":"<stranger's reel>","episode_num":1}` → 201.
- **Impact:** Misattribution of other creators' content; latent.
- **Fix:** Apply the video-series rules (own post, author gate, refuse an occupied number), or retire the routes.
- **Acceptance:** Service tests mirroring `tube_audit_test.go:225`.
- **Effort:** S
- **Was:** LV-V3-8

### lv-75

- **ID:** lv-75
- **Title:** Scheduled lists: Android shows "Nothing scheduled" when the load fails and flips the order after a change; the web hides the server's scheduling rule and stops at 50.
- **Severity:** P3
- **Category:** functional (client)
- **Evidence level:** VERIFIED-READ (one helper pass in V3)
- **Evidence:** `core/feed/…/VideoFeedRepository.kt:257`, `T/ui/scheduled/ScheduledPostsScreen.kt:112`, `ScheduledViewModel.kt:99` vs `post-service/internal/store/postgres/scheduled.go:341`; web `src/app/posttube/scheduled/page.tsx:41,55,82` (cursor unused).
- **How to reproduce:** Android offline → "Nothing scheduled"; web, a time 2 minutes ahead → "Could not reschedule".
- **Impact:** Creators think their schedule is empty or cannot tell why a time is refused.
- **Fix:** Android error state with retry and one sort order; web shows the server message and pages.
- **Acceptance:** ViewModel test for the error state; web test for the message.
- **Effort:** S
- **Was:** LV-V3-9

### lv-76

- **ID:** lv-76
- **Title:** The web series manager cannot rename or delete a series, although the server routes exist.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** VERIFIED-READ (one helper pass in V3)
- **Evidence:** `src/features/upload/studioApi.ts:250-275` has no PATCH or DELETE; `post-service/internal/http/video_series_handler.go:303,408`.
- **How to reproduce:** Create a series on the web; look for rename or delete.
- **Impact:** A mistyped series title is permanent from the product.
- **Fix:** Add rename and delete.
- **Acceptance:** Web test for both calls.
- **Effort:** S
- **Was:** LV-V3-10

### lv-77

- **ID:** lv-77
- **Title:** The RSS "original" enclosure states the original file's size, but an anonymous `/serve` of `original` returns the best rendition.
- **Severity:** P3
- **Category:** functional (RSS)
- **Evidence level:** VERIFIED-READ (one helper pass in V3)
- **Evidence:** `post-service/internal/service/channel_feed.go:220-227`; `media-service/internal/service/playback.go:413-427`.
- **How to reproduce:** A ready video with only 360p; compare the enclosure length to the bytes served.
- **Impact:** Podcast clients may reject or truncate the download.
- **Fix:** Use the rendition actually served, or omit the length, for the fallback.
- **Acceptance:** `channel_feed` unit test for the fallback.
- **Effort:** S
- **Was:** LV-V3-11

---

## Inherited from reels

These are findings of `docs/production-readiness/reels/03-findings.md` on the shared upload, transcode, delivery and client code. They were re-checked by the long-video tracks where stated and apply unchanged, often in a worse form. **They are not counted in the long-video totals: 58 inherited, of which 13 P1, 34 P2, 11 P3.** reels-14 (single reel player) does not apply to the long-video player and is left out.

| reels-N | Title | Sev | How it bites long video |
|---|---|---|---|
| reels-1 | A video post can stay `pending` for ever, visible only to its author | P1 | Same consumer path; long video is more exposed because the post is almost always created before the transcode finishes (Track C) |
| reels-2 | Web HLS sends credentials to a CloudFront policy that refuses them | P1 | Same pattern in `TubePlayer.tsx:361-363,799` and `coverFrame.ts`; if confirmed, every web long video takes the MP4 fallback (lv-27) |
| reels-3 | `media.events.long` is not provisioned in MSK | P1 | Every long video transcodes on the reel loop in prod; root of lv-4 and lv-40 |
| reels-4 | Two transcode workers, no autoscaling, no back-pressure | P1 | Two long lanes platform-wide (lv-4) |
| reels-5 | Transcode pipeline unmonitored; alerts reach no one | P1 | Long jobs that die or loop (lv-6) produce no signal; lv-21 adds the class split |
| reels-6 | media-service trusts a bare X-User-Id; no network policy enforced | P1 | Covers long-video HLS, storyboards, captions and owner `/download` of 16 GiB originals |
| reels-7 | Anyone can read view and watch-depth buckets of any content | P1 | `/v1/analytics/content/:id/views` covers long-video watch depth |
| reels-8 | Account deletion never erases analytics | P1 | Long-video playback sessions and `watch_sessions` are never erased; erasure also blocked by lv-7 |
| reels-9 | Android and web tests run in no CI at head; feed-service unit tests stale | P1 | Long-video feed, subscribed filter and fan-out leg (feed-service) and the 818 posttube web tests run in no CI |
| reels-10 | No upload → transcode → publish → play e2e | P1 | lv-10 is the long-video flow on the same harness |
| reels-11 | No load test | P1 | lv-11 is the long-video half |
| reels-12 | Real-ffmpeg tests skip; no CI installs ffmpeg | P1 | No test encodes a landscape, > 600 s or 1080p/4K source even with ffmpeg; blocks lv-10 |
| reels-13 | The server half of resumable upload has no test | P1 | Long video is its main user (Android ≥ 100 MiB, web ≥ 20 MB) |
| reels-15 | media-service accepts HS256 in prod, no exp, no iss/aud | P2 | Covers resumable upload and caption writes |
| reels-16 | User audio reaches un-hardened ffmpeg | P2 | Dub tracks on long videos |
| reels-17 | Privatised or deleted videos keep playing from issued URLs | P2 | 12 h for long video, raised as lv-2 |
| reels-20 | Android streaming cache survives sign-out | P2 | Long-video segments cached too (lv-50) |
| reels-21 | Web sets no CSP or security headers | P2 | `/posttube/*` unchanged; lets lv-28's external thumbnail load |
| reels-22 | Web `/v1` proxy dot segments | P2 | Same proxy for `/posttube` calls |
| reels-23 | Web `/v1` proxy buffers bodies before auth | P2 | Resumable chunks pass through the same proxy |
| reels-24 | No media rate limit; owner frame routes pull the original into the API pod | P2 | Worst case is a 16 GiB long original |
| reels-25 | Destructive media internal routes take the shared key | P2 | Same routes for long-video assets |
| reels-27 | Watch time is client-controlled and feeds Creator Fund | P2 | Long-video RPM type `long_video` uses the same watch time |
| reels-28 | Fresh session ids inflate today's views | P2 | Unchanged for long video |
| reels-30 | Upload notifications dropped for pending and followers-only uploads | P2 | For long video pending is the normal path: raised as lv-1; followers-only is lv-18 |
| reels-31 | A transcode failure rejects the post for good | P2 | Long jobs hit infrastructure faults more often (lv-5, lv-6, lv-13); over-4 h uploads end here (lv-30) |
| reels-32 | Confirm and queue in two commits | P2 | Same path |
| reels-34 | A Rekognition outage re-encodes up to 32 times | P2 | Each retry is a multi-hour encode, days of compute for a 4 h 4K video |
| reels-35 | Worker scratch may sit on a small node disk | P2 | Partly changed: Karpenter nodes have 100 Gi; the emptyDir limit is what evicts (lv-13) |
| reels-39 | Web uploads cannot resume after a reload | P2 | A 16 GiB upload loses hours (lv-16) |
| reels-40 | Web draft publish can double-post | P2 | Web long uploads publish through drafts |
| reels-47 | Each playlist request costs a PG read, an S3 GET and a post-service call | P2 | lv-12 adds per-segment signing on top |
| reels-48 | Android cache keyed on the signed URL | P2 | Long-video segments re-download after re-signing |
| reels-52 | Analytics ingest 3 statements per event | P2 | A 1 h viewing is about 720 heartbeats |
| reels-53 | Feeds filter content type in Go over the timeline | P2 | Tube feeds read the shared timeline the same way |
| reels-54 | `/v1/posts/trending` scans a week per request | P2 | `content_type=long_video` uses the same query |
| reels-55 | Resumable parts relayed through gateway and media-service | P2 | 3,277 relays for 16 GiB (lv-16) |
| reels-56 | SIGTERM kills ffmpeg; rollouts restart every transcode | P2 | Throws away up to two days of a long job (lv-5) |
| reels-57 | Nothing restarts or reports a hung worker | P2 | A hung long loop holds its partitions for good |
| reels-58 | No joint restore of media database and bucket | P2 | Long originals are the costliest objects to lose |
| reels-59 | A video cannot be traced upload → worker → review gate | P2 | "Still processing after a day" tickets are joined by media id by hand |
| reels-60 | media-service shutdown cuts chunk uploads | P2 | A 16 GiB upload spans every rollout |
| reels-61 | All playback answers 503 when post-service is down | P2 | Same for long video; web says "may still be processing" (lv-68) |
| reels-62 | Chart QA guard red on `MEDIA_WORKER_MAX_ORIGINAL_BYTES` | P2 | QA refuses long originals over 6 GiB (lv-58); a second failure is lv-67 |
| reels-63 | ffmpeg unpinned on end-of-life Alpine, no image scan | P2 | Same worker image |
| reels-67 | No test that a privatised video stops playing from a fetched playlist | P2 | lv-35 is the long-video form |
| reels-70 | Integration CI logs cannot tell skipped from passed | P2 | The post-service long-video integration stage shows one PASS line only |
| reels-78 | Raw Go error text in 500s | P3 | Also in `video_tools_handler.go` and `video_series_handler.go` |
| reels-83 | Android cannot add captions to a reel | P3 | Wider for long video (lv-32) |
| reels-92 | Deleting an asset drops resumable rows without aborting the multipart | P3 | Parts of an abandoned 16 GiB upload billed up to 7 days |
| reels-93 | A late chunk can reopen a completed session | P3 | Same resumable path |
| reels-96 | Transcode retry window about 4 min, not 15 | P3 | Same worker |
| reels-97 | Cleanup of an asset deleted mid-transcode writes no reclaim row | P3 | More likely during multi-hour jobs |
| reels-98 | Scheduled posts retry for ever; parked posts not notified | P3 | Applies to scheduled long videos (lv-17, lv-59) |
| reels-99 | Web proxy would stream and buffer if the API base were empty | P3 | Latent, unchanged |
| reels-102 | `/v1/series` used by no client | P3 | The flick-series authz gaps are lv-74 |
| reels-103 | The media PDB also selects worker pods | P3 | A drain may evict both workers mid-job (lv-5) |
| reels-104 | Every media replica runs the outbox relay without a claim | P3 | Same relay for long transcode requests |

## Out of scope observations

- Platform items inherited from the feed audit and not counted here: feed-02 (no QA or prod environment), feed-10 (alert rules unapplied), feed-11 (rollback never drilled; no in-flight transcode step), feed-12 (no Scylla backup; tube OpenSearch indexes rebuildable only by a manual reindex), feed-42, feed-43 (post-service readiness fails on Redis although progress falls back to the database), feed-46 (request id not forwarded), feed-74, feed-76, feed-77.
- The Subscriptions tab narrows one timeline window in Go and emits no cursor when the narrowed page is short, so it can end after one page (`feed-service/internal/service/feed.go:912-967,1049-1059`); feed module, Track D note.
- Video search has no second page (`search-service/internal/http/handler.go:222-281`); the storyboard is always 100 frames, a seek preview every 144 s on a 4 h video (`media-service/internal/processing/storyboard.go:36,59-71`).
- Android description links are not clickable (`T/ui/watch/WatchDetails.kt:494`); web description and channel links accept `http://` while the server stores https only (`src/features/posttube/watch/linkify.ts:13`, `channel/channelApi.ts:146-152`).
- `/v1/posts/batch` returns unlisted posts by id by design, so client-built "liked" or "reposts" lists can show an unlisted video someone liked (V3 1.1).
- An untitled legacy `content_type='video'` card falls back to the first text line without a tier check (`post-service/internal/service/hub_detail.go:177-178`); long videos require a title, so not raised.
- Step-up is not required for admin title-catalogue edits and `/visibility` (V3 1.2, one helper pass); admin track.
- The post-service admin key is "NOT enabled yet" in prod values, and Azure prod has the media scanner off (map section 7).
- If `media.events.long` is missing, the long loop's `continue` on a fetch error has no sleep and may hot-loop (`media-service/cmd/worker/transcode_classes.go:232-238`; Track C, not verified).
- Migration 081 adds an FK from post-service `video_series.cover_media_id` to `media_assets(id)`, another cross-service reference that assumes one shared database (Track E note; V3 1.4).
- The web `ChannelGate` still renders "First, your channel" if `/v1/channels/me` answers with no channel, reachable only when the ensure fails (`src/features/posttube/channelGate/ChannelGate.tsx:83-89`).
- Graph-service follow rate limits (subscribe and unsubscribe toggling to spam follow notifications) were not checked (Track B).
- Git history and container images were never secret-scanned (gitleaks and trufflehog not installed); the tracked tree passed a pattern scan; the tracked `Architecture/docker/local.env` was not opened; media-service falls back to `minioadmin` when `MINIO_ENDPOINT` is unset, which prod does not use (V3 Part 2 b).
