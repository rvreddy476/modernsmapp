# Track D: performance and scale (long video: PostTube / MTube)

- **Date:** 17 Nov 2026
- **Trees read:** `C:\workspace\modernsmapp` (branch `codex/module-01-02-launch-safety`, working tree as found) and `C:\workspace\wt\pb-echo` (web, branch `feat/echo-web`).
- **Method:** read-only, from `docs/production-readiness/long-video/00-map.md`. Every file:line below was read in this pass. Nothing was delegated.
- **Paths:** backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Web paths are relative to `C:\workspace\wt\pb-echo`. Android paths are relative to `mobile/android/`; `T/` = `feature/tube/src/main/kotlin/com/us/android/feature/tube/`.
- **Executed (VERIFIED-RUN):**
  1. `go test -count=1 -v -run 'TestHLSLadder|TestLongForm|TestCallTimeout|TestVideoLimits|TestPlanStoryboard|TestToolTimeout|TestHLSEncodeArgs' ./internal/processing/` in media-service: 11 tests passed, including `TestHLSLadderKeepsFullLadderForLongForm`, `TestLongFormRenditionsUseVeryfast`, `TestCallTimeoutScalesWithDurationAndIsCapped`.
  2. `go test -count=1 -run 'Sign|Segment|Cache' ./internal/delivery/` in media-service: ok.
  3. `go test -count=1 -run 'WatchProgress|WatchHistory|Playlist|ChannelFeed' ./internal/service/ ./internal/http/` in post-service: ok.
  4. A throwaway Go program in the session scratchpad (not in the repo) that copies media-service's CloudFront signing routine (`internal/delivery/signer.go:163-199`: canned policy, SHA-1, `rsa.SignPKCS1v15`, 2048-bit key) and signs the 2,400 segment URLs of one 4-hour HLS rung, on one core (`GOMAXPROCS=1`). Result: **5.0 s, 2.08 ms per signature, about 1.30 MB of playlist text**. That is this Windows laptop with Go's `crypto/rsa`, not a production node.
  5. `git ls-files | grep -iE "k6|vegeta|locust|gatling|loadtest|load-test|artillery|ghz"` in both trees and a search for `func Benchmark` in post, media, feed and search-service.
  6. There is no ffmpeg on this machine (`which ffmpeg` finds nothing), so no encode was timed. No EXPLAIN, no load, no device and no browser was run.
- **What the numbers mean:** apart from the signing measurement in step 4, every latency, throughput, byte or hour figure below is arithmetic from the code plus a stated assumption. None of it is a measurement. **Nothing in this file shows that long video supports any particular number of viewers, uploads or channels.**

## 1. Verdict for this track

**Long video has never been load tested** (LV-D-12). The delivery design is right: segments go from CloudFront, progress and history page by keyset, playlists and series are bounded. Reading the code shows three problems that grow with video length and that reels never hit:

1. **Every child playlist of a long video is signed segment by segment.** A 4-hour rung is 2,400 RSA signatures (about 5 s of one core, measured) and a 1.3 MB body, on media-service pods limited to 1 CPU. That cost lands on start-up and on every quality switch, and the sign cache holds fewer than three such videos (LV-D-1).
2. **The long transcode has two lanes for the whole platform.** Each job runs up to 48 h through five full-decode encodes, one of them a 4K MP4 that no Tube player requests. The lanes share CPU with reels. No quota exists, so one account can queue more transcode work in a day than the fleet can do in a week (LV-D-2, LV-D-3, LV-D-4, LV-D-5).
3. **Viewer heartbeats are full reads.** Each 10-second progress write runs the post read gate (3 Postgres statements and a graph-service call) before its upsert, and the table it writes is never pruned (LV-D-6, LV-D-7).

Long video also inherits every pipeline finding from reels (section 4), several of them in a worse form.

## 2. Cost model

### 2.1 Starting one long video (Android or web, signed in, HLS ready)

| # | Request | Server cost | Evidence |
|---|---|---|---|
| 1 | `GET /v1/media/:id/hls/master.m3u8` | 1 Postgres (`GetMedia`), 1 S3 GET of the playlist (not cached), 1 post-service media-access call (at least 4 SQL statements), as for reels | `media-service/internal/service/playback.go:247-274,314`; reels Track D section 2.2 |
| 2 | `GET /v1/media/:id/hls/<rung>.m3u8` | Row 1, plus **one RSA signature per segment**: `HLSURLsForAsset` loops every key (`delivery/authz.go:651-669`), `signSegment` signs on a cache miss (`delivery/sign_cache.go:235-253`). 6 s segments for every rung (`processing/video.go:196`), so a 4 h rung has 2,400 segments | VERIFIED-RUN: 2,400 signatures = 5.0 s on one core, about 1.30 MB |
| 3 | Segments | Signed CloudFront URLs fetched straight from the CDN. TTL is 2 × duration + 15 min, capped at 12 h (`sign_cache.go:77-89`) | `media-service/internal/service/playback.go:314-323` |
| 4 | An ABR switch or a pinned quality change | Row 2 again for the new rung | — |

Web watch page, on top of the playlists: about 18 API reads per open. They are detail, video metadata, channel, progress, series, categories, collection, related (`/v1/feed/videos/:id/related`), subtitles, storyboard, audio tracks, genres, offline row, membership access, end screens and cards, queue, loved ids, creator support and comments (`src/features/posttube/components/WatchPage.tsx:167-252`, `watch/hooks/useWatch.ts:47-64`). Related is the most expensive of these (LV-D-9).

### 2.2 Writes per playing viewer

| Write | Cadence | Per-call cost | Evidence |
|---|---|---|---|
| `POST /v1/videos/:id/progress` | every 10 s while playing, plus pause, end and leave (Android `T/ui/watch/WatchViewModel.kt:459,1104`; web `src/features/posttube/model.ts:42`, `WatchPage.tsx:286-327`) | Read gate: Redis GET of `post:body:<id>`, then on a hit `GetPostAccessState` (1 PG), or on a miss `GetPost` (1 PG + media); graph-service `Can(view_posts)` over HTTP (cache 3 s, so it misses at a 10 s cadence); `post_hidden_authors` lookup (1 PG). Then the upsert (1 PG) and the Redis HSET + EXPIRE mirror. A `followers` post adds a graph follow check. `duration_ms: 0` adds `GetVideoDetail` plus a progress read | `post-service/internal/http/video_series_handler.go:990-1037`, `read_gate.go:22-31`, `service/comment_counts.go:302-344`, `post_cache.go:81-131`, `privacy_gate.go:39,203-252`, `post_visibility.go:78-104`, `store/postgres/purge.go:52-71`, `service/watch_progress.go:43-67` |
| `POST /v1/analytics/events` | heartbeat every 5 s (Android `core/analytics/.../VideoWatchTracker.kt:41-44,420`; web `src/hooks/useVideoTracker.ts`, batched every 10 s) | 3 statements per event (reels-52) | reels-52 |

**Arithmetic, not measured.** At C concurrent playing viewers:

- progress is C / 10 writes/s, so about 0.3 × C Postgres statements/s, 0.1 × C graph-service requests/s and 0.2 × C Redis commands/s;
- analytics is C / 5 events/s, so 0.6 × C statements/s.

For C = 10,000 that is about 1,000 progress writes/s, 3,000 progress statements/s and 1,000 graph-service requests/s, plus about 6,000 analytics statements/s. The post-service pool is 25 connections per pod (`post-service/cmd/server/main.go:111`), with 2 to 8 pods (`deploy/services/post-service/values-prod.yaml:16-20`).

## 3. Findings

### LV-D-1

- **ID:** LV-D-1
- **Title:** Every long-video child playlist signs each of its segments with RSA, about 2,400 signatures, 5 s of one core and a 1.3 MB body for one 4-hour rung. This cost is paid at start-up and on every quality switch, on media-service pods limited to 1 CPU, and the 20,000-entry sign cache holds fewer than three such videos before it is wiped.
- **Severity:** P1
- **Category:** performance
- **Evidence level:** VERIFIED-RUN (signing cost and body size, scratch program, step 4) / VERIFIED-READ (code path, cache and pod sizing) / SUSPECTED (the saturation point under real traffic)
- **Evidence:**
  - `media-service/internal/service/playback.go:277-288`: every segment line of the child playlist becomes a key. `:314-315`: `HLSURLsForAsset(..., HLSSegmentTTL(duration))`.
  - `media-service/internal/delivery/authz.go:651-669`: one `signSegment` per key. `sign_cache.go:235-253`: a cache miss calls `SignProtectedSegment`, and `signer.go:163-199` does one `rsa.SignPKCS1v15` per URL with a canned policy naming that single resource.
  - `processing/video.go:196`: `hlsSegmentSeconds = 6` for every rung, so a 4 h video has 2,400 segments per rung. The long-form ladder has up to 3 HLS rungs (`video.go:21-25`, VERIFIED-RUN `TestHLSLadderKeepsFullLadderForLongForm`).
  - Cache reuse lasts only 2 min (`sign_cache.go:54,161-182`). The cache is bounded at 20,000 entries, and when it is full with nothing stale **the whole map is dropped** (`:187-196`). 3 rungs × 2,400 segments = 7,200 entries per 4 h video, so a third concurrently started 4 h video wipes the cache for everyone. Every cache read and write takes one global mutex (`:151-153,171,185`).
  - Measured here: 2,400 signatures took 5.0 s on one core (2.08 ms each), and the playlist was about 1.30 MB. The body is served `private, no-store` (`http/handler.go:724-726`). Neither the gateway nor media-service compresses responses (no `gzip` in `api-gateway/cmd`, `api-gateway/pkg`, `media-service/cmd` or `media-service/internal/http`).
  - media-service pods: CPU limit 1, HPA 2 to 6 (`deploy/services/media-service/values-prod.yaml:18-31`). Playlist authorization and upload parts run on the same pods (reels-47, reels-55).
- **How to reproduce:** Benchmark `serveHLSPlaylist` for a fixture asset whose child playlist has 2,400 segment lines, with the real `Signer` and a 2048-bit key: first call, a second call within 2 min, a call after 2 min, and calls on 3 distinct such assets. On QA, use load test L2 (section 6).
- **Impact:** Arithmetic: a pod with 1 CPU can build about 0.2 to 0.4 cold 4-hour child playlists per second, and while it does, every reel's playlist on that pod waits. A viewer waits several seconds of server CPU before the first segment. A phone on a slow link also downloads 1.3 MB per rung before playback starts, and again on each up-switch. A few popular long videos started within two minutes keep the cache cold. A 1-hour video costs a quarter of this (600 segments).
- **Fix (smallest first):**
  - (a) Sign once per playlist: a CloudFront **custom policy** whose `Resource` is the asset's `.../hls/*` prefix, with the same `Policy`, `Signature` and `Key-Pair-Id` query appended to every segment line. This is one RSA operation per response. The query strings are then identical, so the body also compresses well.
  - (b) Until then, cache the rewritten child body per pod, keyed by (media id, playlist, ttl), for the reuse window. Authorization still runs per request, and the URLs are viewer-independent (`sign_cache.go:13-22`).
  - (c) `-hls_time 10` for long-form plans, which cuts the segment count by 40%.
- **Acceptance:**
  - A unit test with a counting fake signer: a 2,400-segment child playlist causes at most 2 signer calls (fails now: 2,400).
  - A benchmark of `GetHLSPlaylist` for that fixture shows p50 under 50 ms on one core.
  - L2 passes.
- **Effort:** S for (b), M for (a)

### LV-D-2

- **ID:** LV-D-2
- **Title:** The platform runs at most two long transcodes at once. Each pod has one long loop, two pods exist, and nothing autoscales. One job can hold a lane for up to 48 h, long jobs queue behind each other, and the long loop's ffmpeg shares the same 3 CPUs as the reel loop and fingerprinting, so a long job also slows reels.
- **Severity:** P1
- **Category:** performance
- **Evidence level:** VERIFIED-READ (structure); SUSPECTED (job durations; no ffmpeg here)
- **Evidence:**
  - One consumer loop per class, strictly sequential (`media-service/cmd/worker/transcode_classes.go:23-54`). Prod runs `MEDIA_TRANSCODE_CLASSES: "short,long"` in every pod, with `worker.replicaCount: 2`, CPU request 1, limit 3, and memory 4Gi (`deploy/services/media-service/values-prod.yaml:153-176`). There is no worker HPA (reels-4).
  - The long-form pipeline is sequential: 360p, 480p, 720p, 1080p and 4K x264 `veryfast`, each from the original (`processing/video.go:439-513`), then a storyboard that decodes the whole original again (`processing/storyboard.go:84-89,146-150`; `cmd/worker/main.go:790-799`), then the HLS copy.
  - Budgets: each ffmpeg call gets 10 min + 20 × duration, capped at 24 h, and the job gets 12 × that, capped at **48 h** (`processing/limits.go:129-158`, VERIFIED-RUN `TestCallTimeoutScalesWithDurationAndIsCapped`). A job past its budget is terminal (`cmd/worker/main.go:598-605`). The limits file says the dev worker measured "~15x real time for a long-form veryfast rendition" on 1 CPU (`limits.go:126-128`).
  - x264 gets every CPU the container has (no `-threads`, `video.go:202-218`), so the long loop's encode competes with the reel loop in the same pod.
  - The long topic is missing in MSK (reels-3), so in prod long jobs may run inside the reel loop instead.
  - A rollout kills in-flight ffmpeg (reels-56), which restarts a day-long job from zero.
- **How to reproduce:** Load test L5. On a production-sized worker, queue two 4 h 1080p uploads and one 4 h 4K upload, then a 60 s reel. Measure confirm-to-ready for each, and the reel's time while both long jobs run.
- **Impact:** Arithmetic, using the file's own 15x figure scaled to 3 CPUs (about 5x real time for one 1080p rung): a 4 h 1080p job is roughly 20 h for the 1080p rung plus the smaller rungs. Two such uploads take both lanes for about a day, and every other long video waits behind them. A 4 h 4K job is likely to end at the 48 h budget as a terminal failure after holding a lane for two days. These figures are estimates for L5 to confirm.
- **Fix:**
  - (a) A dedicated long-class worker Deployment: a second chart worker with `MEDIA_TRANSCODE_CLASSES=long`, its own CPU, and short-only pods beside it. This is the values comment's own plan (`values-prod.yaml:166-174`).
  - (b) Add `media.events.long` to MSK (reels-3).
  - (c) LV-D-4 to cut the work per job.
  - (d) A lag and oldest-age alert on group `media-transcode-worker-long`.
- **Acceptance:** A render test: the prod chart renders a worker with `MEDIA_TRANSCODE_CLASSES=long` separate from the short worker (fails now). L5 thresholds met.
- **Effort:** M

### LV-D-3

- **ID:** LV-D-3
- **Title:** There is no per-user storage or transcode-minute quota. The only control is a fail-open count of 10 uploads per hour and 30 per day, so one account can confirm 30 four-hour, 16 GiB uploads a day, more long-transcode work than the two-lane fleet does in weeks, and every original is kept indefinitely beside its renditions.
- **Severity:** P1
- **Category:** performance / cost
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `media-service/internal/service/ratelimit.go:15-60`: 10 per hour and 30 per day, with `return true // fail-open` on a Redis error (`:51`). Resumable init counts against the same budget (`service/resumable.go:120-125`).
  - Size cap: prod leaves `MEDIA_WORKER_MAX_ORIGINAL_BYTES` unset, which means 16 GiB (map section 7). Length cap: 4 h (`processing/limits.go:53-57`). No quota exists: the map found no `*QUOTA*` env and no byte or minute counter (map section 7).
  - Storage per video: the original is kept (the owner-only download, map section 6). It sits beside 5 MP4 renditions (`video.go:457-510`) and 3 HLS rungs that are stream copies of three of them (`video.go:38-45`, `cmd/worker/main.go:926-932`), so 360p, 720p and 1080p are stored twice.
  - FIFO per partition, with no per-uploader fairness (`transcode_classes.go:229-256`, reels-4).
- **How to reproduce:** With one account, init and confirm 30 resumable uploads of a 4 h test file in a day on QA. All 30 are accepted and queued on `media.events.long` ahead of every other creator.
- **Impact:** One account, abusive or simply prolific, can deny long-video publishing to everyone for days. Storage and egress cost has no per-account ceiling. Arithmetic: roughly 3 to 5 times the original per video.
- **Fix:**
  - (a) A per-user daily source-duration budget for long transcodes (for example 8 h per day, configurable). Check it at confirm from the MP4 header duration the worker already reads (`processing.MP4Duration`, `transcode_classes.go:37-39`), with 429 `TRANSCODE_QUOTA` and fail-closed on a Redis error.
  - (b) At most one in-flight long job per uploader.
  - (c) A per-user stored-bytes quota. Product decides the number.
- **Acceptance:** A service test: the ninth hour of source in a day answers 429. A Redis error answers 503, not success (fails now: fail-open).
- **Effort:** M

### LV-D-4

- **ID:** LV-D-4
- **Title:** The long-form ladder decodes the original six times: five separate x264 encodes plus the storyboard. Its most expensive rung, a 4K MP4, is played by no Tube client, and 480p is encoded only for fallbacks.
- **Severity:** P2
- **Category:** performance / cost
- **Evidence level:** VERIFIED-READ (pipeline and consumers); SUSPECTED (relative costs)
- **Evidence:**
  - `processing/video.go:457-510`: `TranscodeToMP4` for 360, 480, 720, 1080 and 2160, each `-i inputPath` (`:205-218`). A rung's error is swallowed (`if err == nil`), so a 4K encode that runs into its 24 h call limit is dropped silently after burning those hours.
  - The HLS ladder is 360, 720 and 1080 only (`video.go:21-25`). 4K is not a rung, so no HLS player can ever select it.
  - Consumers of `4k`: `bestRenditionKey` lists it last (`service/playback.go:95`). On the web, only the legacy `src/hooks/useMediaVariants.ts:23`, used by `components/VideoPlayer.tsx` (not `TubePlayer`), names it. No Android code names `4k` (grep). Offline copies and RSS take 720p, 480p or 360p (`post-service/internal/service/offline_copies.go:85`, `channel_feed.go:73`).
  - The storyboard renders 100 frames at `fps=1/interval` from the original (`storyboard.go:36,84-89`), which decodes the whole file.
- **How to reproduce:** Run `TestTranscodePipelineTiming` (named in `video.go:171-173`) on a machine with ffmpeg, using a 4K 10-minute clip. Record wall time per rung and the decode share.
- **Impact:** A 4K encode costs about as much as all the lower rungs together (roughly 4x the pixels of 1080p) and serves no Tube viewer. Six full decodes of a 4K HEVC original multiply CPU time for a multi-hour video. This feeds LV-D-2 and LV-D-5.
- **Fix:**
  - (a) Skip the `4k` MP4 rung until a 4K HLS rung exists (`cmd/worker/main.go:743`, `video.go:501-510`).
  - (b) Encode the remaining rungs in one ffmpeg run (`-filter_complex split` with one output per height), which is one decode.
  - (c) Make the storyboard from the 360p rendition instead of the original.
- **Acceptance:** An args test: a 2160p source plan produces no `4k` output and one ffmpeg invocation for the MP4 rungs (fails now: 5 invocations). The storyboard args name the 360p file.
- **Effort:** S for (a) and (c), M for (b)

### LV-D-5

- **ID:** LV-D-5
- **Title:** The scratch check reserves 3 × the original, but a multi-hour 4K job writes the original plus five MP4 renditions plus HLS copies of three of them. Such a job can fill the 50 Gi volume hours into the encode and then end as a terminal failure.
- **Severity:** P2
- **Category:** performance / reliability
- **Evidence level:** VERIFIED-READ (guard and file layout); SUSPECTED (output sizes depend on content)
- **Evidence:**
  - `cmd/worker/transcode_guards.go:63` `scratchHeadroom = 3`, applied at `:248-255`. Scratch is a 50Gi `emptyDir` (`values-prod.yaml:166`). The comment assumes "roughly 2-3x the original" (`cmd/worker/main.go:615-620`).
  - The renditions stay in `tmpDir` until the end (`main.go:621-625,806-840`). The HLS copies go to a second directory on the same volume (`main.go:916-932`), and a rung that fails leaves its partial file behind.
  - A 16 GiB, 4 h original averages about 9.5 Mbit/s. Re-encoding at CRF 23 `veryfast` (`video.go:176`) is expected to produce a 4K file of the same order as the original or larger, a 1080p file of several GB, and HLS copies of 360p, 720p and 1080p again (estimate).
- **How to reproduce:** In L5, record the peak `du` of the scratch directory for the 4 h 4K job.
- **Impact:** A failed encode reports ENOSPC in a later step, often HLS generation, which is permanent (`main.go:933-935`), after many hours of work. reels-35 (scratch possibly on a small node disk) makes it worse.
- **Fix:** Raise `scratchHeadroom` to 6 for sources at or above 1440p, or base it on duration × the rung bitrates. Delete each MP4 rendition from scratch as soon as it is uploaded and copied to HLS. LV-D-4 (a) removes the largest file.
- **Acceptance:** A guard test: a 16 GiB 2160p original with 50 GiB of scratch is refused up front as `cannot fit` (fails now: accepted).
- **Effort:** S

### LV-D-6

- **ID:** LV-D-6
- **Title:** Every 10-second progress heartbeat runs the full post read gate before its upsert: 3 Postgres statements, 1 graph-service call and 2 Redis round trips. Only the edge rate limit applies, and the Redis mirror it writes saves nothing, because a cache hit also reads Postgres.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ (cost per request); SUSPECTED (where it saturates; L3)
- **Evidence:**
  - `post-service/internal/http/video_series_handler.go:1012`: `requirePostReadable` on every POST. It leads to `singlePostRead`, then `loadVisiblePost` (`service/comment_counts.go:302-344`), then `getCachedPostBody`, which on a hit still runs `GetPostAccessState` (`post_cache.go:94-107`).
  - `viewerMayViewPost`, then `canViewAuthor` (`post_visibility.go:78-87`), then graph `Can` through a 3 s per-process cache (`privacy_gate.go:39,157-177`), then `AnyHidden` (`store/postgres/purge.go:52-71`). Then `UpsertWatchProgress` (`store/postgres/watch_progress.go:39-73`) and the HSET + EXPIRE pipeline (`service/watch_progress.go:51-65`).
  - Mirror: `GetWatchProgress` on a Redis hit calls `s.pgStore.GetWatchProgress` anyway and returns the DB row (`service/watch_progress.go:70-96`).
  - The post-service graph client is `&http.Client{Timeout: 5 * time.Second}` with the default transport (2 idle connections per host) and no breaker (`post-service/pkg/graphclient/*.go:58`).
  - Cadence on both clients is 10 s (section 2.2). No progress-specific limit exists (map 2.1).
- **How to reproduce:** L3 (section 6). Count statements per request with `pg_stat_statements` and graph-service requests per progress write.
- **Impact:** Arithmetic: progress traffic is 0.3 × C statements/s and 0.1 × C graph calls/s (section 2.2). Most of that cost is the read decision, which does not change during a viewing. A graph-service slowdown turns into 5 s progress writes that each hold a pool connection, with no breaker.
- **Fix:**
  - (a) Cache the gate's "readable" answer per (viewer, post) in process for 60 s for this route only. Revocation still cuts the writes off within a minute.
  - (b) Delete the Redis mirror (or make the GET trust it); both reads go to Postgres today.
  - (c) Raise both clients' interval to 30 s, keeping the forced saves on pause, end and leave.
- **Acceptance:** A handler test with counting fakes: 6 progress writes for one (viewer, post) within 60 s make 1 graph call and at most 6 + 1 PG statements (fails now: 6 graph calls, 18 statements).
- **Effort:** S

### LV-D-7

- **ID:** LV-D-7
- **Title:** `watch_progress` keeps one row per (viewer, video) forever with no retention, and every heartbeat rewrites the indexed `last_watched_at`, so no update can be a HOT update and the history index takes a new entry every 10 s per viewer.
- **Severity:** P2
- **Category:** performance / data growth
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `post-service/database/migrations/012_posttube_features.sql:96-106`: primary key `(user_id, post_id)` and `idx_watch_progress_user (user_id, last_watched_at DESC)`.
  - The upsert sets `last_watched_at = NOW()` on every write (`store/postgres/watch_progress.go:40-53`). The only delete paths are per row, per user ("clear history"), and the FK cascades (`:89-93,229-244`). Map section 8 found no sweeper.
  - The Redis mirror expires after 90 d (`service/watch_progress.go:14`); the rows do not.
- **How to reproduce:** On QA during L3, compare `pg_stat_user_tables.n_tup_hot_upd` with `n_tup_upd` for `watch_progress` (expected near 0%), and watch index size growth over the 30 minutes.
- **Impact:** Rows grow as viewers × distinct videos ever watched. Index bloat and autovacuum load grow with heartbeat traffic, not with rows. Retention is also a privacy question for Track C.
- **Fix:** A retention sweeper inside post-service: delete rows whose `last_watched_at` is older than an agreed window (product: for example 2 years), in batches of 10,000. Autovacuum settings for this table (`autovacuum_vacuum_scale_factor = 0.02`). LV-D-6 (c) cuts the index churn by a factor of three.
- **Acceptance:** A sweeper integration test: rows past the window are deleted in bounded batches and recent rows are kept. A migration test that sets the table's autovacuum options.
- **Effort:** S

### LV-D-8

- **ID:** LV-D-8
- **Title:** The watch page's default comment order, "top", computes a correlated `COUNT(*)` over `comment_reactions` for every top-level comment of the video on every page and pages with OFFSET, so each page costs a full sort of the video's whole comment set.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ (query and default); SUSPECTED (plan; no EXPLAIN)
- **Evidence:**
  - `post-service/internal/store/postgres/comments.go:356-357`: `commentTopOrder` uses `(SELECT COUNT(*) FROM comment_reactions cr WHERE cr.comment_id = comments.id) DESC`. `:395-404` applies `ORDER BY ... LIMIT $2 OFFSET $n`.
  - The comment at `:341-344` assumes "threads are short-lived". Long videos are evergreen.
  - Index: `idx_comments_post (post_id, created_at DESC)` only (`database/setup.sql:98`), which cannot serve this order.
  - The web default is "top" (`src/features/posttube/components/WatchPage.tsx:442`; hub default `hub/hubApi.ts:227`; Android `core/feed/.../PostEditForm.kt:83`).
- **How to reproduce:** EXPLAIN ANALYZE `ListCommentsSorted(..., "top")` on a post with 50,000 top-level comments and 200,000 reactions, for page 1 and page 10. Or run L8.
- **Impact:** The comment read for a popular video costs grows with its total comment count on every open of the watch page, and deep pages repeat or skip rows as counts change.
- **Fix:** Smallest: cache the first "top" page per post (viewer-independent rows; the viewer's own `review` rows merged in after) for 60 s. Proper: a maintained `comments.reaction_count`, an index on `(post_id, reaction_count DESC, created_at DESC, id DESC) WHERE parent_id IS NULL AND is_deleted = FALSE`, and a keyset cursor.
- **Acceptance:** EXPLAIN shows an index scan with no sort node. A store test that page 2 is stable while a reaction is added.
- **Effort:** S (cache) / M (column and index)

### LV-D-9

- **ID:** LV-D-9
- **Title:** Related videos ("Up next") are rebuilt from scratch on every watch page open and every next page: a seed lookup, three post-service listings, the block set, the ranker and hydration, with no cache. The per-source sizes the code asks for are silently cut by post-service's caps, and the category source filters an unindexed column.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ (calls and caps); SUSPECTED (query plan of the category scan)
- **Evidence:**
  - `feed-service/internal/service/related.go:129-246`: `lookupPost`, `collectRelatedCandidates` (`:263-332`, with `/v1/posts/by-author` and `/v1/posts/recent` twice), `resolveBlockedSet`, `applyHiddenAuthorFilter`, `RankRelated` over the pool, then `HydratePosts` per chunk. The offset cursor (`:111-117`) means page 2 repeats all of it.
  - The sources ask for 60 each (`:92-94`), but post-service turns any `limit > 50` into **20** (`post-service/internal/store/postgres/posts.go:1089-1091`, `author_sort.go:208-210`). The pool is therefore at most about 60 rows, not 150.
  - `GetRecentPosts` with `AND category = $n` (`posts.go:1112-1116`) has no supporting index (`migrations/047_posts_category_slug.sql:39-47` says so). It walks `idx_posts_not_deleted (created_at DESC)` (`007_reels_gold_spec.sql:121`) until it finds 21 matches, which for a rare category means scanning far back.
  - Web calls it on every open with `limit` 16 (`WatchPage.tsx:192`, `watch/hooks/useWatch.ts:56-64`).
- **How to reproduce:** L1. Trace one `/v1/feed/videos/:id/related` call and count downstream requests. EXPLAIN `GetRecentPosts(nil, {long_video}, "<rare category>", 20, "")` on production-volume data.
- **Impact:** Related is probably the most expensive call on the watch page, and its cost repeats per page. A ranking that changes between pages makes an offset cursor repeat or skip videos (Track A).
- **Fix:**
  - (a) Cache the ranked id list per (viewer, seed, chip) in Redis for 10 min; page from it and hydrate only the page.
  - (b) Ask for 50 per source.
  - (c) After EXPLAIN, a partial index `posts (category, created_at DESC) WHERE visibility='public' AND deleted_at IS NULL AND content_type IN ('long_video','video')`, the index the migration's own comment names.
- **Acceptance:** A service test: page 2 within 10 min makes no post-service listing call (fails now: 3). A test that the source request carries `limit=50`.
- **Effort:** S

### LV-D-10

- **ID:** LV-D-10
- **Title:** Android's channel search (`GET /v1/channels/search`) accepts a one-character query, matches `lower(name) LIKE '%q%'`, and orders by a correlated `COUNT(*)` of each matching channel's videos. Since every account has a channel, a short query counts videos for most of the user base.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ (query and callers); SUSPECTED (plan)
- **Evidence:**
  - `post-service/internal/store/postgres/channels.go:543-557`: `WHERE c.handle LIKE $1 OR lower(c.name) LIKE $2`, `ORDER BY handle_prefix DESC, video_count DESC`, with `video_count` from `channelVideoCountCorrelated` (`:519-526`).
  - `NormalizeChannelQuery` only refuses empty input (`service/channels.go:471-478`). The trigram index exists only when `pg_trgm` installs (`migrations/043_channels_search.sql:20-26`), and trigrams cannot serve patterns under 3 characters.
  - Caller: Android `feature/search/.../data/SearchApi.kt:37,88` (the video app's channel search).
- **How to reproduce:** EXPLAIN ANALYZE `SearchChannels("a", 20)` on 1 million channels.
- **Impact:** A search-as-you-type call can cost a scan of channels plus a count per match. Several viewers typing at once is enough to load the database.
- **Fix:** Require at least 2 characters (3 for the `%q%` arm), and order by `channels.subscriber_count` or a denormalised video count instead of the correlated count. Or move Android to search-service's `/v1/search/channels` (`search-service/internal/http/handler_tube.go:130`), which already indexes `tube_channels_v1`.
- **Acceptance:** A handler test: `q=a` answers 400 or an empty result without a query. EXPLAIN on the remaining query shows no per-row subplan.
- **Effort:** S

### LV-D-11

- **ID:** LV-D-11
- **Title:** When hls.js fails on the web, a long video falls back to one progressive 720p MP4 that starts again at second 0 and is signed for only 5 minutes. A multi-hour video then plays at a fixed bitrate, and later range requests may hit an expired signature.
- **Severity:** P2 (P1 if reels-2 is confirmed, because then every production web view takes this path)
- **Category:** performance
- **Evidence level:** VERIFIED-READ (fallback and TTL); SUSPECTED (browser range-request behaviour after the redirect)
- **Evidence:**
  - `src/features/posttube/components/TubePlayer.tsx:372-381`: any fatal non-media error calls `useFile()`. That sets `video.src = fileUrl` (`:319-332`) without carrying the position: `carryRef` is set only in the effect cleanup (`:390`), and `seekAppliedRef` stays true (`:473`).
  - `fileUrl` = `/v1/media/:id/serve` (`WatchPage.tsx:258`, `model.ts:558-560`). For a viewer this redirects to `bestRenditionKey`, which is `720p` first (`media-service/internal/service/playback.go:95,413-428`), signed at `MaxProtectedTTL`. That is 5 min (`sign_cache.go:55-58`), not the segment TTL. The redirect is cacheable for 60 s (`http/handler.go:654-669`).
  - The `<video crossOrigin="use-credentials">` (`TubePlayer.tsx:799`) and `withCredentials` (`:361-363`) are reels-2.
- **How to reproduce:** In Chrome, force a fatal hls.js network error at 30 min on a 2 h video. Check that playback restarts at 0. Pause for 6 minutes, then seek forward 1 h, and check whether the range request to the CDN gets 403.
- **Impact:** Viewers lose their place in long videos. A 720p progressive file without ABR stalls on slow links. If the browser reuses the redirected URL, seeks after 5 min fail.
- **Fix:** Pass the current position into `useFile` (set `carryRef` before switching). For video variants, sign the `/serve` redirect with `HLSSegmentTTL(duration)` as segments are; that needs a long-TTL path for MP4 renditions beside `SignProtectedSegment`, or a fresh `/serve` request on a 403 `error` event.
- **Acceptance:** A web test: a fatal network error at 1,800 s resumes the MP4 at 1,800 s (fails now: 0). A media-service test: the viewer redirect for a 2 h video's 720p is signed for more than 5 min.
- **Effort:** S (position) + M (signature)

### LV-D-12

- **ID:** LV-D-12
- **Title:** No load test exists, or has ever been run, for any long-video endpoint, for the long transcode, or for a 16 GiB resumable upload. Every capacity statement about long video is therefore unproven.
- **Severity:** P1
- **Category:** tests / performance
- **Evidence level:** VERIFIED-RUN (step 5)
- **Evidence:** In modernsmapp the search finds only `dating-service/loadtest/*.js` (3), `rider-service/loadtest/*.js` (3) and `mobile/android/core/auth/.../RefreshUnderLoadTest.kt`. pb-echo has nothing. There is no `func Benchmark` in post, media, feed or search-service. Map section 10: "Load: none for long video". The dev smoke checks only `GET /v1/posts/trending?content_type=long_video` (`scripts/smoke/dev-smoke.mjs:173,429-431`). This is the long-video half of reels-11.
- **How to reproduce:** Run the search in step 5.
- **Impact:** The playlist rate at which media-service stops starting videos (LV-D-1), the heartbeat rate post-service survives (LV-D-6), and the queue time of a long upload (LV-D-2) are all unknown.
- **Fix:** Write and run L1 to L9 (section 6) against a QA stack sized like production. Commit the scripts and the results.
- **Acceptance:** k6 scripts under `Architecture/tools/loadtest/long-video/` and a results file in `docs/production-readiness/long-video/` with p50, p95 and p99 latency, error rate, CPU, Postgres statements per second, consumer lag and queue age for each scenario, meeting the section 6 thresholds.
- **Effort:** L

### LV-D-13

- **ID:** LV-D-13
- **Title:** A 16 GiB upload is cut into the 5 MiB minimum part size, so it takes 3,277 sequential part requests. The session expires 24 h after init no matter how much progress is made, so a link slower than about 1.6 Mbit/s can never finish. Android also copies the whole file into app storage first.
- **Severity:** P2
- **Category:** performance
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `media-service/internal/service/resumable.go:21-30,53-71`: chunk = max(5 MiB, ceil(total / 9,000)). For 16 GiB that is 5 MiB, so 3,277 parts.
  - `resumableUploadTTL = 24 * time.Hour` (`:38`) is set once at init (`:158`) and never extended (no `expires_at` write in `store/postgres/resumable.go` except at create, `:36-39`). Expiry is refused at `:250`.
  - Both clients upload parts one at a time and stream them, without buffering (web `src/lib/resumableUpload.ts:11-13,121-126`; Android `core/media/.../upload/ResumableUploader.kt:268,457-462`).
  - Android copies a picked file to `noBackupFilesDir/reel_publish/<key>.video` before upload (`feature/post/.../createhub/ReelPublishStore.kt:306-330`).
  - Parts are relayed through the gateway and media-service (reels-55). Web cannot resume after a reload (reels-39).
- **How to reproduce:** Arithmetic: 16 GiB at 1.5 Mbit/s takes about 25.5 h, so the session expires before the last part. On QA, throttle a 2 GiB upload to 0.2 Mbit/s and watch it fail at 24 h.
- **Impact:** Large uploads on slow mobile links fail at the end, with no way to finish. Android needs twice the file's size free on the device. Each upload makes thousands of requests through the API tier.
- **Fix:** Slide `expires_at` forward by 24 h on each accepted part, with an absolute cap of 7 days to match the bucket lifecycle (`infra/terraform/modules/media/main.tf:169-176`). Target about 1,000 parts (chunk = max(5 MiB, total / 1,000) → 17 MiB for 16 GiB). On Android, keep a persistable URI grant (`takePersistableUriPermission`) instead of copying when the picker allows it.
- **Acceptance:** A service test: a part accepted at hour 23 moves `expires_at` to at least hour 47. A chunk-plan test: 16 GiB gives at most 1,000 parts.
- **Effort:** S

### LV-D-14

- **ID:** LV-D-14
- **Title:** The web player's hls.js setup is a reels setup: an unlimited back buffer, no cap to the player size, and no reaction to `X-HLS-Expires-At`. Long sessions keep growing media buffers, a small player downloads 1080p, and a tab paused past the segment signature falls back to the MP4 at second 0.
- **Severity:** P3
- **Category:** performance
- **Evidence level:** VERIFIED-READ (config, and the library default in `node_modules/hls.js/dist/hls.mjs`, version 1.7.3: `backBufferLength: Infinity`)
- **Evidence:**
  - `TubePlayer.tsx:358-364`: `maxBufferLength: 30, capLevelToPlayerSize: false`, no `backBufferLength`.
  - No file in `src` reads `X-HLS-Expires-At` (grep), although media-service exposes the header for this purpose (`media-service/internal/http/handler.go:717-723`). Android does read it (`core/media/.../HlsRefresh.kt`, map 9.1).
  - Segment guarantee = 2 × duration + 15 min − 2 min (`sign_cache.go:77-89,226-231`). For a 10-minute video that is 33 min of pause.
- **How to reproduce:** Play a 2 h video in Chrome for 90 min and record `video.buffered` ranges and the tab's memory every 10 min. Pause a 10-minute video for 40 min, then press play.
- **Impact:** More memory on long sessions (bounded only by the browser's MSE eviction). Wasted bandwidth in the mini-player. A lost position after a long pause (LV-D-11).
- **Fix:** `backBufferLength: 90`, `capLevelToPlayerSize: true` (unless the viewer pinned a quality). On `ERROR` with a 403 response, or once past the header's instant, reload the source at the current time instead of calling `useFile()`.
- **Acceptance:** A unit test of the hls config factory for these values. A player test: a 403 fragment error reloads HLS at the same position.
- **Effort:** S

### LV-D-15

- **ID:** LV-D-15
- **Title:** Moving one item in a playlist rewrites the whole playlist, up to 5,000 rows, as one INSERT per row inside a transaction that holds the playlist's row lock.
- **Severity:** P3
- **Category:** performance
- **Evidence level:** VERIFIED-READ
- **Evidence:** `post-service/internal/store/postgres/playlists.go:205-288`: `SELECT ... FOR UPDATE`, `DELETE FROM playlist_items`, then `for i, r := range ordered { tx.Exec(INSERT ...) }`. `MaxPlaylistItems = 5000` (`:329`).
- **How to reproduce:** A store integration test timing `MovePlaylistItem` on a 5,000-item playlist.
- **Impact:** A drag in a large playlist makes 5,000 round trips, which blocks other adds and removes on that playlist and holds a pool connection for seconds.
- **Fix:** Shift only the rows between the old and new positions (two `UPDATE ... SET position` statements, as `AddPlaylistItem` does at `:383-395`), or one `INSERT ... SELECT FROM unnest($1::uuid[], $2::int[], $3::timestamptz[])`.
- **Acceptance:** A store test asserting at most 5 statements for a move in a 5,000-item playlist.
- **Effort:** S

### LV-D-16

- **ID:** LV-D-16
- **Title:** The web watch page fires about 18 API reads per open, besides the playlists, all through the gateway, and one of them is the uncached related call (LV-D-9).
- **Severity:** P3
- **Category:** performance
- **Evidence level:** VERIFIED-READ (hook list; request count not measured)
- **Evidence:** `src/features/posttube/components/WatchPage.tsx:167-252` (hooks listed in section 2.1). The detail query has `staleTime` 60 s (`watch/hooks/useWatch.ts:47-53`).
- **How to reproduce:** Open a watch page with the network panel open and count requests before the first frame.
- **Impact:** Slower first frame on mobile web, and per-view backend load is many times the playlist cost.
- **Fix:** Defer the below-the-fold reads (genres, creator support, queue, loved ids, offline row, end screens and cards) until after `playing`. Longer term, one `GET /v1/videos/:id/watch` composite.
- **Acceptance:** A page test: at most 8 requests before the first `playing` event.
- **Effort:** S

## 4. Inherited from reels (re-checked; applies unchanged unless noted)

- **reels-2:** CDN CORS versus `withCredentials`. The same pattern is in `TubePlayer.tsx:361-363` and `:799` (`crossOrigin="use-credentials"`) and in `hub/coverFrame.ts`. For long video the fallback is LV-D-11.
- **reels-3:** `media.events.long` is absent from `infra/terraform/modules/msk/topics-job/variables.tf` (0 matches, re-grepped), so long jobs may run in the reel loop (LV-D-2).
- **reels-4:** Two worker pods, no HPA, no back-pressure (`deploy/services/media-service/values-prod.yaml:153-176`, re-read). LV-D-2 is the long-class view of it.
- **reels-5:** Worker metrics are not scraped and no alert covers the transcode path. Also true for the long consumer group.
- **reels-11:** No load test; LV-D-12 is the long-video half.
- **reels-17:** A takedown does not stop segments that are already signed. For long video the window is up to 12 h, not 35 min (`sign_cache.go:60-63`); this is Track B's question.
- **reels-24:** The frames route streams the original into an API pod. A long video is the 16 GiB worst case.
- **reels-35:** The 50 Gi scratch may sit on a small node disk; it makes LV-D-5 worse.
- **reels-39:** Web uploads cannot resume after a reload. For a 16 GiB upload that costs hours (LV-D-13).
- **reels-47 / reels D-7:** Each playlist request costs 1 PG + 1 S3 GET + 1 post-service call. LV-D-1 adds the signing.
- **reels-48:** The Android cache is keyed on the signed URL, so long-video segments re-download after re-signing (`core/media/.../di/MediaModule.kt:78-93`, re-read: no cache key factory).
- **reels-52:** Analytics ingest runs 3 statements per event into a DEFAULT partition. A 1 h viewing is about 720 heartbeats.
- **reels-53:** The Tube feeds read the shared timeline and filter content type in Go (`feed-service/internal/service/feed.go:915`, `target*3` rows).
- **reels-54:** `/v1/posts/trending?content_type=long_video` scans a week of posts per request.
- **reels-55:** Resumable parts are relayed through the gateway and media-service; for 16 GiB that is 3,277 relays (LV-D-13).
- **reels-56:** SIGTERM kills ffmpeg. For a 24 to 48 h long job, any rollout restarts a day of work.
- **v2 D-2:** The Next proxy streams media and buffers uploads only when the API base is empty (P3, latent). This applies unchanged.

## 5. Checked and found sound

| Area | What holds | Evidence |
|---|---|---|
| Watch history paging | Keyset on `(last_watched_at DESC, post_id)`, limit 1 to 100, served by `idx_watch_progress_user`; one batched hydration per page | `post-service/internal/store/postgres/watch_progress.go:124-224`, `service/watch_progress.go:196-215`, `migrations/012_posttube_features.sql:106` |
| Continue watching | `WHERE user_id AND completed = FALSE ORDER BY last_watched_at DESC LIMIT ≤100`; same index | `watch_progress.go:96-117`, `http/video_series_handler.go:1116-1119` |
| Progress ordering | `client_sent_at` refuses stale reports; future times clamped | `watch_progress.go:39-73`, `video_series_handler.go:980-987` |
| Channel video tab | Keyset cursor on `(is_pinned, created_at, id)`, limit at most 50, `idx_posts_author_type` / `idx_posts_author_long_video` | `store/postgres/author_sort.go:128-153,207-210`, `migrations/008_video_metadata.sql:36`, `setup.sql:20` |
| Channel counts | One query per channel, cached 60 s | `store/postgres/channels.go:306-326` |
| Playlist items | Keyset by position, page at most 100, unlisted filter in SQL; 5,000-item cap; adds lock the parent | `store/postgres/playlists.go:329,358-412,453-493` |
| Series | 50 episodes per series at most; episode visibility in one batched read, not per episode | `store/postgres/video_series.go:106-114`, `service/series_visibility.go:58-80`, `service/tube_audit.go:69-112` |
| Subscribed-owner set | Redis set, 6 h TTL, "looked" marker for empty answers, invalidated on subscribe and unsubscribe, keyset paging of post-service with a page cap, fails closed | `feed-service/internal/service/subscriptions.go:61-275` |
| Search | `type=videos` is one OpenSearch page (limit at most 100); author and media hydration batched (media in chunks) | `search-service/internal/http/handler.go:222-281`, `http/results.go:87-117`, `mediaclient/mediaclient.go:87-110` |
| Post body cache | One key per post (viewer-independent), 5 min TTL, explicit drops on edit and delete, access state re-read from Postgres on every hit (no stale private or deleted reads), 100 ms Redis bound | `post-service/internal/service/post_cache.go:15-131` |
| Per-viewer caches | The hydrate cache is per viewer and skips scheduled, processing and members-only rows; signed URLs are viewer-independent and handed out after a per-request decision; playlists are `no-store` | map section 5; `media-service/internal/http/handler.go:654-669,724-726` |
| Media bytes | Segments, the `/serve` redirects (thumbnails, storyboard, RSS enclosures, offline copies) and the MP4 fallback all redirect to CloudFront; media-service streams bytes only for the anonymous group-post scope, which long videos do not use | `http/handler.go:614-694`, `service/playback.go:290-303`, `post-service/internal/service/offline_copies.go:85`, `channel_feed.go:200-227` |
| RSS build | 50 items at most, enclosures resolved with bounded concurrency and cached 10 min in process | `post-service/internal/store/postgres/channels.go:355-415`, `service/channel_feed.go:62-73,374-420` |
| Upload memory | Neither client nor media-service buffers a part (web `Blob.slice`, Android `PartRequestBody`, server streams to `UploadPart`); the worker streams the original to disk and never reads it whole | `src/lib/resumableUpload.ts:11-13`, `ResumableUploader.kt:457-462`, reels Track D section 4, `cmd/worker/main.go:615-672,812-814` |
| Android player | Long-form `ExoPlayer` with Media3 default buffers (its own LoadControl), HLS refresh on 403 or expiry | `core/media/.../PlayerPool.kt:239-245`, `di/MediaModule.kt:139-140`, map 9.1 |
| Ladder sizing | The long-form ladder never upscales past the source; VERIFIED-RUN `TestHLSLadderNeverExceedsTheSource` | `processing/video.go:49-61` |

## 5a. Outbound calls on the long-video paths

| Caller → callee | Timeout | Pool | Retry | Breaker | Evidence |
|---|---|---|---|---|---|
| post-service → graph-service (read gate on every progress write, listings) | 5 s | Go default (2 idle per host) | none | none | `post-service/pkg/graphclient/*.go:58` |
| post-service → identity-profile (age gate) | 2 s, result cached 10 min (2 min if absent) | — | — | none | `post-service/internal/service/age_gate.go:137-142` |
| post-service → media-service (RSS enclosure records) | per-call timeout plus a total budget, bounded concurrency | — | none | none | `service/channel_feed.go:395-420` |
| feed-service → post-service (related, subscribed owners, discovery fill) | 5 s | 20 idle per host | none | **yes** (`httpclient.NewWithBreaker`) | `feed-service/internal/service/feed.go:159`, `shared/httpclient/client.go:15-21,74-86` |
| media-service → post-service (playlist authorization) | 3 s | Go default | none | none | reels D-7 |
| Postgres pools | `MaxConns 25` per pod in post, media and feed | — | — | — | `post-service/cmd/server/main.go:111-114`, `media-service/cmd/server/main.go:68-71`, `feed-service/cmd/server/main.go:50-53` |

Only feed → post has a circuit breaker. The heartbeat path (LV-D-6) depends on graph-service with no breaker.

## 6. The load test that is needed

Run against a QA stack with production instance sizes: Aurora, MSK with `media.events.long` created, Redis, OpenSearch, CloudFront, and media-service and worker values as in prod. Seed data:

- 500,000 users, each with a channel;
- 100,000 long videos: 70% at 5 to 30 min, 25% at 30 to 120 min, 5% at 2 to 4 h, at 1080p, with 20 at 4K;
- 2,000 channels with 1,000 to 200,000 subscribers;
- 50 million `watch_progress` rows;
- one video with 50,000 top-level comments and 200,000 reactions;
- playlists up to 5,000 items;
- 2 million analytics events back-filled.

All scripts are k6 unless noted.

| ID | Scenario | Rate | Pass thresholds |
|---|---|---|---|
| L1 | Watch open, signed-in mix: `GET /v1/posts/:id`, `/v1/videos/:id`, `/v1/posts/:id/series`, `/v1/videos/:id/progress`, `/v1/channels/:ref`, `/v1/feed/videos/:id/related?limit=16` (page 1, 20% also page 2), `/v1/posts/:id/comments?sort=top` | ramp to 200 opens/s over 10 min, hold 20 min | each route p95 < 300 ms and p99 < 800 ms; related p95 < 500 ms; errors < 0.5%; post-service and feed-service CPU < 70% |
| L2 | Playback start and quality switches: master plus one child, plus a second child for 30% of viewers (switch), spread over 50 distinct videos of 1 to 4 h | 1,000 playlist requests/s | `/hls/*.m3u8` p95 < 200 ms, media-service CPU < 70%, 0 503s, signer calls per child playlist at most 2 (LV-D-1), CloudFront hit ratio > 90% after warm-up |
| L3 | Progress heartbeats: 20,000 concurrent viewers posting every 10 s with a 2-tab share of 5% | about 2,000 writes/s for 30 min | p95 < 50 ms, errors < 0.1%, post-service PG statements per write ≤ 2 (after LV-D-6), graph-service rps from post-service < 10% of writes, Aurora CPU < 60%, `watch_progress` HOT ratio recorded |
| L4 | Analytics for long sessions: the same 20,000 viewers, heartbeat every 5 s, web shape (batched every 10 s) and Android shape (batches of up to 100) | about 4,000 events/s | p95 < 200 ms, errors < 0.1%, no lock waits on `playback_sessions` |
| L5 | Long transcode: at t=0 confirm 4 × 1 h 1080p, 2 × 4 h 1080p and 1 × 4 h 4K (16 GiB); from t=10 min, the reels L4 burst (200 short reels in 5 min) | as stated | reel p95 confirm-to-ready < 120 s while long jobs run; 1 h 1080p ready < 6 h; no terminal failure; scratch peak < 80% of the volume; `media.events.long` lag back to 0 within the expected run time; per-job wall time recorded |
| L6 | 16 GiB resumable uploads: 20 concurrent (web and Android part shapes), plus one throttled to 2 Mbit/s for the full TTL | as stated | media-service and gateway CPU < 70% and memory < 70% of their limits; part error rate < 1%; L2 thresholds still met at 50% rate; the throttled upload completes (LV-D-13) |
| L7 | Lists and search: `/v1/feed/videos?subscribed_only=true` (40%), `/v1/feed/videos` (30%), `/v1/channels/search?q=` with 1 to 4 characters (10%), `/v1/search/posts?type=videos` (10%), `/v1/videos/history` (10%) | 300 rps | p95 < 300 ms, p99 < 800 ms; channel search p95 < 100 ms with no sequential scan in `pg_stat_statements` |
| L8 | Comments on the 50,000-comment video: page 1 to 10 `sort=top` and `newest` | 100 rps | p95 < 150 ms |
| L9 | Soak: L1 + L2 + L3 + L4 at 50% for 6 h, plus 20 real browsers (Playwright) each watching a 2 h video with a 40-minute pause midway | — | no memory growth > 10% per hour in post, media, feed or analytics-service; browser tab memory flat after the first 10 min; every paused browser resumes at its position (LV-D-14) |

For each run, record p50, p95, p99, error rate, CPU and memory per service, Postgres statements per second per service, Redis ops, consumer lag (`media-transcode-worker`, `media-transcode-worker-long`) and CloudFront hit ratio. Commit them in a results file in this folder.

## Not verified

- Every number except the signing measurement (VERIFIED-RUN, step 4) is arithmetic or an estimate. No latency, throughput, encode time or byte size was measured on production hardware. The signing figure is Go `crypto/rsa` on this laptop; production nodes may be faster or slower.
- Encode wall time per rung and output sizes for 1 h and 4 h sources at 1080p and 4K (LV-D-2, LV-D-4, LV-D-5). There is no ffmpeg here, and `TestTranscodePipelineTiming` was not run.
- Query plans for `GetRecentPosts` with a category, `ListCommentsSorted("top")`, `SearchChannels` with a short query, and the post-service batch behind related. No EXPLAIN was run.
- Whether `pg_trgm` is installed on the prod Aurora cluster (`migrations/043_channels_search.sql:20-26`).
- Browser behaviour for range requests after a 307 to a short-lived signed URL (LV-D-11); MSE eviction in Chrome and Safari for long sessions (LV-D-14).
- Whether Cloudflare compresses `application/vnd.apple.mpegurl` responses and whether it caches the web `feed.xml` route's `s-maxage=600`. No CDN in front of the web app was found in Terraform; Cloudflare is DNS (`infra/terraform/envs/prod/outputs-cloudflare.tf`).
- Gateway upstream timeouts for a slow child playlist. The gateway server sets no write timeout (`api-gateway/pkg/edgelimits/edgelimits.go:5-74`), and its reverse-proxy transport was not traced.
- The Android watch screen's request count at open, and the Android player's memory over a 2 h session (device needed).
- AWS SDK settings for S3 in media-service and the worker (as reels).
- Whether the related ranker (`ranker.RankRelated`) makes per-candidate Redis or Scylla reads; it was not traced into `feed-service/internal/ranking`.

## Notes for other tracks

- **Track A:**
  - The Subscriptions tab narrows one timeline window in Go and emits no cursor when the narrowed page is short (`feed-service/internal/service/feed.go:912-967,1049-1059`), so the tab can end after one page while older subscribed videos exist.
  - Related asks for 60 per source and gets 20 (LV-D-9), and its offset cursor over a re-ranked pool can repeat or skip videos.
  - Web loses the playback position on HLS fallback (LV-D-11, LV-D-14).
  - The storyboard is always 100 frames, so a 4 h video has a seek preview every 144 s (`processing/storyboard.go:36,59-71`).
  - Video search has no second page (`search-service/internal/http/handler.go:222-281`).
- **Track B:**
  - Long-video segments stay fetchable for up to 12 h after a takedown (`sign_cache.go:42-63`); a custom-policy wildcard (LV-D-1 fix (a)) keeps the same window unless the TTL is shortened.
  - Upload quota fails open on Redis (LV-D-3).
  - `SaveWatchProgress` and its siblings return `err.Error()` in 500s (`video_series_handler.go:1034,1104,1122,1149,1172`).
- **Track C:** `watch_progress` has no retention (LV-D-7); viewing history is personal data. The 4K MP4 and the duplicate HLS copies double the stored bytes (LV-D-4).
- **Track E:**
  - There is no breaker on post-service → graph-service, which every progress write depends on (section 5a).
  - A long transcode that hits its 48 h budget fails terminally after holding a lane for two days, with no alert (LV-D-2, reels-5). A rollout restarts it (reels-56).
- **Track F:** No test covers a long-form HLS playlist's signing cost, the long worker class end to end, or a resumable upload whose session outlives 24 h (LV-D-1, LV-D-2, LV-D-13).
