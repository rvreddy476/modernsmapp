# Track D: performance and scale (reels)

- **Date:** 17 Nov 2026
- **Trees read:** `C:\workspace\modernsmapp` (branch `codex/module-01-02-launch-safety`, working tree as found) and `C:\workspace\wt\pb-echo` (web, branch `feat/echo-web`).
- **Method:** read-only. Backend paths were read directly. The Android player and web player sections come from two delegated read-only passes. Their load-bearing lines for D-1 and D-2 (`ReelVideo.tsx:348-371`, `route.ts:99-203`) were re-read here. Every other client line is VERIFIED-READ by one reader.
- **Executed:** one run, `go test -count=1 -v -run 'TestHLS|TestShortForm|TestReel' ./internal/processing/` in `media-service`, which passed. 18 tests passed, including `TestHLSLadderCapsReelsAt720p`, `TestReelsEncodeWithTheFasterPreset`, `TestHLSEncodeArgsAreTheOriginalLadderEncode` and `TestHLSMasterBandwidthIsTheMeasuredPeak`. The two tests that need a real ffmpeg (`TestHLSRepackagedReelIsPlayable`, `TestHLSRepackagedFromRenditionsIsPlayable`) were skipped because there is no ffmpeg on PATH. Nothing else was run: no EXPLAIN, no load, no device, no browser.
- **What the numbers mean:** any latency, throughput or byte figure below is arithmetic from the code, not a measurement.
- **Paths:** backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Web paths are relative to `C:\workspace\wt\pb-echo`. Android paths are relative to `mobile/android/`, where `…` stands for `src/main/kotlin/com/us/android`.
- **Not repeated here:** the home-feed fan-out consumer and the hydration fan-out. Both are covered by `docs/production-readiness/feed/tracks/track-d-performance.md` (D-1, and the round-trip table in section 2). Reels share that hydration path (`HydratePosts`), so that table applies to every reels page as well.

## 1. Verdict for this track

**Reels have never been load tested, so no statement can be made about how many viewers or uploads they support** (D-4). By reading alone:

- The server-side feed read is batched and keyset-paged.
- Segment bytes are designed to go from CloudFront to the client.
- The transcode worker is careful about durability.

The problems are capacity and delivery:

- On the web, video bytes most likely end up passing through the Next.js server, either through a CORS failure on every HLS segment or through the proxy that streams `/serve` (D-1, D-2).
- The transcode fleet runs two short-form jobs at a time for the whole platform, with no autoscaling and no back-pressure (D-3).
- Every reel start costs several un-cached backend round trips per playlist (D-7).
- Portrait reels are delivered at most 404x720 (D-5).

## 2. Cost of one reel, end to end

### 2.1 Feed page (`GET /v1/feed/reels`, first page, `limit` 15 from Android or 8 from web)

| Step | Cost | Evidence |
|---|---|---|
| Rate limit | 1 Redis | `feed-service/internal/http/handler.go:842-884` |
| Timeline window | Scylla reads of `home_timeline_by_user`. The content type is filtered in Go, not in CQL. `target = (limit+1)*3`, `fetchLimit = remaining*5` (at most 1000 rows per read), up to `maxContentTypeReads = 48` sequential reads over 12 month buckets | `service/feed.go:740-741`, `store/scylla/timelines.go:34,451,471-514` |
| Block and mute set | 1 HTTP to graph (memo 1 min) | `service/feed.go:759` |
| Hidden authors | 1 Postgres | `service/feed.go:764` |
| Following tab | Redis, or graph on a miss | `service/feed.go:772,812` |
| Discovery fill (first page, short window) | 1 HTTP to post-service `/v1/posts/recent` | `service/feed.go:715-722,787-794` |
| Hydration | Same as the home feed: about 25 to 30 round trips from feed-service and more than 100 behind it for a 20-row page (feed track, section 2). One media batch (at most 50 ids), one post batch (at most 100 ids) | `service/hydration.go`; `media-service/internal/service/media_batch.go:37-40` |
| Category chip (`category=`) | Up to `maxReelCategoryWindowsWhileEmpty = 15` windows, **each a full timeline read plus a full `HydratePosts`**, while the page is still empty | `service/reels_category.go:49,64-71`, `service/category.go:53` |

### 2.2 Starting one reel (Android, HLS ready)

| # | Request | Server cost | Evidence |
|---|---|---|---|
| 1 | `GET /v1/media/:id/hls/master.m3u8` through the gateway (relative URL from the feed payload) | media-service: `GetMedia` (1 Postgres), `DownloadObject` of the playlist from S3 (1 S3 GET, not cached), `HLSURLsForAsset` → post-service `POST /v1/internal/media-access` (1 HTTP; post-service runs at least 4 SQL statements: asset facts, channel-avatar owners, story-for-media, post media path), and signing | `media-service/internal/service/playback.go:247-324`, `media_batch.go:164-166` (relative `hlsPlaylistURL`), `post-service/internal/service/story_surface.go:302-372` |
| 2 | `GET /v1/media/:id/hls/720p.m3u8` (or 360p) | the same as row 1, plus one RSA signature per segment through the per-pod sign cache | `playback.go:277-319`, `delivery/sign_cache.go:206-240` |
| 3 | Segments | signed CloudFront URLs, fetched from the CDN directly with no bearer | `infra/terraform/modules/media/main.tf:304-319`; Android `di/MediaModule.kt:60-73` (delegated) |
| 4 | Side calls per settled reel | `GET v1/graph/relationship` per author (deduplicated), `GET v1/media/{id}/audio-tracks` (cached per process), channel subscription when the reel has a channel, sound file through a gateway `/serve` URL when an added sound exists | `FollowGraph.kt:73-108`, `AudioTracksRepository.kt:46-59`, `ReelsViewModel.kt:1062`, `ReelSoundPlayer.kt:83-84` (delegated) |

Android prepares current±1 (`ReelsScreen.kt:1126-1148`), so each swipe also triggers rows 1 and 2 for the newly preloaded neighbour.

**Per swipe, steady state:** about 2 to 4 playlist requests. That is about 2 to 4 Postgres reads, 2 to 4 S3 GETs and 2 to 4 post-service calls (8 to 16 SQL statements) in media-service and post-service, before any segment byte moves.

### 2.3 Starting one reel (web)

- The master URL is the same relative `/v1/media/:id/hls/master.m3u8` (`model.ts:324-327`, `mediaHref` prefixes `NEXT_PUBLIC_API_BASE_URL || ""` at `model.ts:192-195`). It therefore goes browser → Next `/api/proxy` → gateway → media-service, with the same cost as section 2.2 rows 1 and 2.
- Segments go to CloudFront directly, but see D-1.
- **No preloading:** only the active reel is mounted (`ReelsScreen.tsx:818-822`), so every swipe is a cold start: master, then child, then a 6-second segment, in sequence (D-6).

## 3. Findings

```
ID:            D-1
Title:         The web player sends every HLS request with credentials (xhr.withCredentials = true), while the CloudFront response-headers policy answers CORS with allow_credentials = false and overrides the origin's, so in production every segment fetch from the media host is expected to be blocked by the browser, and the player then falls back to the progressive MP4 streamed through the Next.js server.
Severity:      P1 (web only)
Category:      performance
Evidence level: SUSPECTED (both halves VERIFIED-READ; the browser outcome is not executed). Confirm by loading /reels on a build pointed at a stack with the CloudFront distribution (staging) and looking for a CORS error on media.<domain>/…/*.ts in the console and a /v1/media/<id>/serve request after it.
Evidence:
  - pb-echo src/features/reels/components/ReelVideo.tsx:348-354: new HlsCtor({ …, xhrSetup: (xhr) => { xhr.withCredentials = true } }). hls.js's default loader is XhrLoader, so this applies to playlists AND fragments.
  - The child playlist's segment lines are absolute signed CloudFront URLs: media-service/internal/service/playback.go:314-319 (HLSURLsForAsset → rewriteHLS).
  - infra/terraform/modules/media/main.tf:365-378: cors_config { access_control_allow_credentials = false … origin_override = true }, attached to the protected default behaviour (:318).
  - A credentialed cross-origin response without Access-Control-Allow-Credentials: true is rejected by the browser (Fetch standard CORS check).
  - ReelVideo.tsx:362-371: any fatal non-media error calls useFile(), which switches to reel.media.fileUrl = /v1/media/<id>/serve (model.ts:368). That URL goes through the Next proxy, which follows the 307 and streams the bytes (D-2).
  - The dev box has no CDN (infra/devbox/devbox.cfn.yaml:71-107), and S3's own CORS does return Allow-Credentials for a listed origin, which would explain why this has not been seen on dev.
How to reproduce: As above. Or with curl: send a GET to a signed segment URL with an Origin header and confirm the response carries Access-Control-Allow-Origin but not Access-Control-Allow-Credentials.
Impact: On production web, every reel would play as the progressive 720p MP4 piped through the Node server, not as adaptive HLS from the edge. No bitrate adaptation, no CDN offload, one Node stream per viewer for the whole video, and a failed request on every reel start before the fallback.
Fix: Set withCredentials only for same-origin (/v1) URLs: xhrSetup: (xhr, url) => { xhr.withCredentials = url.startsWith("/") || new URL(url).origin === location.origin }. Segments carry their authorisation in the signature and need no cookie.
Acceptance:    A unit test on the hls config factory: a CDN fragment URL gets withCredentials=false and a /v1 playlist URL gets true (fails now). On staging, the network panel shows .ts requests to the media host with status 200 and no /serve request.
Effort:        S
```

```
ID:            D-2
Title:         On the web, every reel byte that is not an HLS segment (the progressive fallback, the poster, the added sound, avatars) is fetched by the Next.js proxy, which follows media-service's 307 itself and streams the bytes, including in production where no storage route is set; upload parts are buffered whole (up to 64 MiB) in Node memory.
Severity:      P1 (web only; Android is not affected)
Category:      performance
Evidence level: VERIFIED-READ
Evidence:
  - URLs: progressive /v1/media/<id>/serve (model.ts:368), poster from cover_media_id → /serve (model.ts:370, drawn at ReelVideo.tsx:631), avatar → /serve (model.ts:338-339), sound /v1/audio/<id>/serve (sounds.ts:29-30).
  - next.config.ts:16-17 rewrites /v1/:path* to /api/proxy.
  - src/app/api/proxy/[...path]/route.ts:150-203: GET and HEAD use redirect "manual" and follow each hop themselves ("The bytes are streamed to the browser from here"). With MEDIA_STORAGE_* unset (production, storageRoute.ts:17) the proxy fetches the CloudFront URL itself (:196-200) and streams it back (:131).
  - No request or idle timeout. The only one is a 1 s connect attempt (route.ts:28-31).
  - route.ts:99-102: body = await req.arrayBuffer() for every write. A resumable part is 5 to 64 MiB (media-service service/resumable.go:16-38). The web uses resumable upload above 20 MB (src/lib/resumableUpload.ts:20).
  - The same pattern on the home feed is feed track D-3. Here it carries whole videos, through D-1's fallback and through every reel without HLS (instant publish plays the progressive file while the ladder is processing, media_batch.go:139-160).
How to reproduce: Open /reels with the network panel open. The poster, the sound and any fallback video are requested from the site's own origin under /v1/media/... and /v1/audio/..., never from the media host.
Impact: Node server bandwidth, memory and connection count grow with video bytes watched, not with page views. One long reel playing holds a Node stream for its whole length. Ten concurrent 64 MiB part uploads hold about 640 MiB of heap.
Fix: In read(), when a redirect leaves the gateway origin and no storage route is configured (production), return the 307 to the browser instead of following it. Media-service already sets "private, max-age=60" and Vary on it (media-service/internal/http/handler.go:654-669). For uploads, pipe req.body as a stream (duplex: "half") instead of arrayBuffer(). No backend change.
Acceptance:    A route test: GET /api/proxy/v1/media/x/serve against a stub gateway answering 307 to https://cdn.example/… returns 307 with that Location when MEDIA_STORAGE_* are unset (fails now: 200 with the body). A part-upload test asserts that the upstream fetch receives a ReadableStream body.
Effort:        S
```

```
ID:            D-3
Title:         The transcode fleet runs at most two short-form jobs at once for the whole platform (one sequential consumer loop per class in each of two worker pods, no HPA), with no back-pressure at upload init and no lag alert, so a burst of uploads or a few 10-minute 4K reels queue every other reel for hours.
Severity:      P1
Category:      performance
Evidence level: VERIFIED-READ for the structure; SUSPECTED for the queueing times (confirm with load test L4 in section 6)
Evidence:
  - One kafka.Reader per class, each consumed by one goroutine that handles a message to completion before fetching the next: media-service/cmd/worker/main.go:217-252 (consume(...)), :203-216.
  - Prod worker: replicaCount 2, no HPA, cpu limit 3, memory 4Gi, classes "short,long" in the same pods: deploy/services/media-service/values-prod.yaml:153-176. The chart renders a single worker Deployment (map section 8).
  - media.events has 6 partitions (infra/terraform/modules/msk/topics-job/variables.tf:39,62-66). Even with more pods, at most 6 short jobs could run at once; with 2 pods it is 2.
  - The reel encode runs three separate ultrafast passes from the original (360p, 480p, 720p), each decoding the full source: internal/processing/video.go:383-435, 184. A 4K portrait 10-minute source is decoded three times.
  - Per-job budget: FFmpegCallTimeout = 10 min + 20 x duration (210 min for 600 s), job budget = 12 x that, capped at 48 h: internal/processing/limits.go:129-158. A legitimately slow job holds its lane for that long.
  - The fingerprint loop and the long-class loop run in the same pods and compete for the same 3 CPUs (cmd/worker/main.go:174-199).
  - No queue-depth or lag check at POST /v1/media/init (grep "backpressure|queue depth" finds only the fingerprint gauge, cmd/worker/fingerprint_runner.go:52). The only limit is the per-user budget of 10 per hour and 30 per day (internal/service/ratelimit.go:21-55), which fails open.
  - The worker has no Service or ServiceMonitor and no transcode-lag alert (map section 8; charts/atpost-service/templates/prometheusrule.yaml:42-65).
How to reproduce: L4 in section 6. Upload 200 portrait reels of 30 to 60 s in 5 minutes, plus three 10-minute 4K portrait reels, and measure the time from confirm to processing_status=ready for the 200th short reel.
Impact: At an assumed 30 to 90 s of wall time per 60 s reel (three ultrafast passes, plus a Rekognition scan of 15 frames, plus fingerprinting), two lanes clear about 80 to 240 reels per hour. A launch-day burst, or one creator's 4K uploads on a shared partition, leaves new reels in "processing" for a long time. Creators see stuck uploads and nothing alerts anyone. These numbers are arithmetic, not measurements.
Fix: Smallest first. (a) Set worker.replicaCount to 6 (equal to the partitions) and request 2 CPU each. (b) Add a PrometheusRule on kafka consumer lag for group media-transcode-worker and on the oldest processing age (the readiness gauges exist, cmd/worker/readiness_metrics.go). (c) Encode the three reel renditions in one ffmpeg run with a split filter (one decode). (d) Later: a KEDA ScaledObject on lag, and a 503 TRANSCODE_BUSY at init when the backlog passes a threshold.
Acceptance:    L4 passes: p95 confirm-to-ready under 120 s for 60 s reels at 200 uploads per 5 minutes, and the lag alert fires in a test where workers are scaled to 0.
Effort:        S for (a) and (b); M for (c)
```

```
ID:            D-4
Title:         No load test exists for any reels endpoint, the transcode pipeline or the playlist route, and none has been run, so every capacity statement about reels is unproven.
Severity:      P1
Category:      tests
Evidence level: VERIFIED-RUN (repository search)
Evidence:
  - `git ls-files | grep -iE "k6|vegeta|locust|gatling|loadtest|load-test"` in modernsmapp returns only dating-service/loadtest/*.js (3), rider-service/loadtest/*.js (3) and one Android unit test (RefreshUnderLoadTest.kt). The same search in pb-echo returns nothing.
  - No func Benchmark exists in feed-service. The map (section 7) lists "Load tests: none for media, feed or reels".
How to reproduce: run the command above.
Impact: The request rate at which /v1/feed/reels, the playlist route or analytics ingest stops meeting its latency target is unknown, as are the size of an upload burst the worker survives and the Node proxy load per web viewer.
Fix: Write and run L1 to L6 in section 6 against a QA stack sized like production, and commit the scripts and results.
Acceptance:    k6 scripts under Architecture/tools/loadtest/reels/, and a results file in this folder with p95, p99, error rate, consumer lag and queue age for each scenario, meeting the thresholds in section 6.
Effort:        L
```

```
ID:            D-5
Title:         Portrait reels are scaled by height (scale=-2:H), so a 9:16 reel's best rendition is about 404x720 (360p rung about 202x360), while the master playlist declares RESOLUTION=1280x720 and 640x360.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ for the scaling and the declared resolutions (the ladder cap itself is VERIFIED-RUN: TestHLSLadderCapsReelsAt720p and TestReelsEncodeWithTheFasterPreset passed). The output pixel size was not measured because ffmpeg is not installed here.
Evidence:
  - internal/processing/video.go:205-218: "-vf", fmt.Sprintf("scale=-2:%d", maxHeight). The reel pipeline calls it with 360, 480 and 720 (:401-432). The probe stores display dimensions after rotation (:252-269), so a phone portrait clip is 1080x1920 at the filter.
  - The HLS rungs are stream copies of those files (hls.go:15-34,145-149). The reel ladder is 360p and 720p only (video.go:49-61).
  - hls.go:75,196-197: RESOLUTION comes from a landscape table, whatever the orientation.
  - reelMP4Profile is ultrafast at CRF 28 with no -maxrate/-bufsize (video.go:184). BANDWIDTH is the measured peak (hls.go:262-301), which is correct, but the peak is unbounded.
How to reproduce: Run TestHLSRepackagedReelIsPlayable on a machine with ffmpeg, with a 1080x1920 input, and ffprobe the 720p.mp4 output (expected about 404x720).
Impact: On a 1080-wide phone a reel is upscaled about 2.7 times; the 360p rung is about 202 px wide. The wrong RESOLUTION misleads size-capped track selection. Viewers see soft video on the core surface.
Fix: Scale the short side for short-form: scale='if(gt(ih,iw),min(720,iw),-2)':'if(gt(ih,iw),-2,min(720,ih))' (the pattern audio.go:192 already uses), write RESOLUTION from the probed output size, and add -maxrate 1.5x / -bufsize 2x of a per-rung target.
Acceptance:    An args test asserting the short-side scale expression for a portrait plan, and an ffmpeg-tagged test asserting the 720 rung of a 1080x1920 input has width 720.
Effort:        S
```

```
ID:            D-6
Title:         Reel start-up is slow by construction on the web: 6-second HLS segments, no preloading of the next reel, and three sequential requests (master, child, first segment) through the proxy on every swipe; hls.js must download a whole 6 s fragment before the first frame.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ (start-up time not measured)
Evidence:
  - hlsSegmentSeconds = 6 for every rung, keyframes every 2 s (internal/processing/video.go:192-196).
  - Web mounts only the active reel, with no rel=preload, no hidden next <video> and no manifest prefetch (ReelsScreen.tsx:796-822; delegated read).
  - hls.js config sets only maxBufferLength and capLevelToPlayerSize:false (ReelVideo.tsx:348-354). There is no startLevel and no fragment-load policy.
  - The playlists are served no-store (media-service/internal/http/handler.go:725) and cost the backend work in D-7.
How to reproduce: Throttle the network to "Fast 4G" in DevTools and measure the time from swipe to the first "playing" event over 20 reels.
Impact: At about 2 Mbit/s and a 720p ultrafast CRF-28 rung, the first fragment alone is roughly 1 to 3 MB (estimate), so start-up takes seconds on mobile networks. Swiping feels slow compared with the reference apps.
Fix: For short-form plans use hls_time 2 (the keyframe interval already allows it), and on the web prefetch the next reel's master and child playlists (or mount a paused, muted hls.js for index+1 with maxBufferLength 2).
Acceptance:    An args test asserting -hls_time 2 for a Reel plan. A web test asserting the next reel's playlist is requested before the swipe.
Effort:        S (segments) + M (web prefetch)
```

```
ID:            D-7
Title:         Every HLS playlist request (master and each child) costs one Postgres read, one un-cached S3 GET of an immutable playlist, one post-service authorisation call (at least 4 SQL) over a default HTTP client (2 idle connections per host, no retry or breaker), and one RSA signature per segment, and Android triggers 2 to 4 of these per swipe.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ (cost per request); SUSPECTED for the saturation point (L2)
Evidence:
  - media-service/internal/service/playback.go:251 (GetMedia), :271 (blobStore.DownloadObject on every request), :314 (HLSURLsForAsset).
  - delivery/authz.go:171-177: client = &http.Client{Timeout: 3 * time.Second} with the default transport. cmd/server/main.go:548 passes nil, so it is used for post-service. Go's default MaxIdleConnsPerHost is 2. No retry and no circuit breaker; a timeout becomes 503 (http/delivery_viewer.go:34-45).
  - post-service/internal/service/story_surface.go:310-372: facts, channel-avatar owners, story lookup, then viewerMayAccessPostMedia.
  - Signing: delivery/sign_cache.go:52-72,184-198. Reuse window 2 min. One global mutex. 20,000 entries; when full and nothing has expired, the whole map is dropped.
  - Android prepares current±1 (ReelsScreen.kt:1126-1148) and never caches playlists (PlaylistAwareDataSourceFactory.kt:64,112-117; delegated).
How to reproduce: L2 in section 6. Watch media-service p95 on /hls/ and post-service /v1/internal/media-access rps.
Impact: Playlist traffic, not segments, becomes the scaling limit of media-service and post-service. With 2 idle connections, most of the hundreds of calls per second to post-service open a new TCP connection. A slow post-service turns into 503s on playback with no retry.
Fix: (a) An in-process LRU of playlist bodies keyed by object key (they are immutable once written, and a reprocess writes new keys with a new generation). (b) Pass an http.Client with a Transport that has MaxIdleConnsPerHost 64 and an idle timeout to NewHTTPContentAuthorizer. (c) Shard the sign-cache mutex, or evict the oldest entries instead of wiping the map.
Acceptance:    A unit test that the second GetHLSPlaylist for the same asset does not call DownloadObject (fails now). A benchmark of serveHLSPlaylist at 32 parallel calls showing allocations and connection reuse.
Effort:        S
```

```
ID:            D-8
Title:         The Android media cache is keyed on the full signed URL, and the server re-signs segment URLs every 2 minutes, so replaying a reel, swiping back or re-fetching the playlist downloads the same segments again instead of reading the 256 MiB cache.
Severity:      P2
Category:      performance
Evidence level: SUSPECTED (no setCacheKeyFactory is VERIFIED-READ; the Media3 default key of uri.toString() is library behaviour, not read here). Confirm with a Media3 CacheDataSource unit test using two URLs that differ only in Signature, Expires and Key-Pair-Id.
Evidence:
  - core/media/…/di/MediaModule.kt:51-55,93: SimpleCache with LeastRecentlyUsedCacheEvictor(256 MiB) and no cache key factory (delegated read).
  - Server signature reuse window 2 min (media-service/internal/delivery/sign_cache.go:54); HlsRefresh re-prepares on expiry or a 403 (core/media/…/HlsRefresh.kt:56-70).
How to reproduce: The unit test above, or watch data use while replaying the same reel after 3 minutes.
Impact: Extra mobile data and CDN egress; the cache does almost nothing for HLS.
Fix: setCacheKeyFactory { spec -> spec.uri.buildUpon().clearQuery().build().toString() } for URLs on the media host only.
Acceptance:    The unit test above passes (one cache entry for both URLs).
Effort:        S
```

```
ID:            D-9
Title:         Android preloads two neighbours, each buffering up to 10 s at full adaptive quality on any network, data saver does not reduce how much is preloaded, and choosing Auto quality removes the data-saver cap and forces a second prepare on every page.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ (delegated); the per-neighbour buffering is Media3 behaviour, inferred
Evidence:
  - core/media/…/PlayerPool.kt:221-253: LoadControl min 2,000 ms, max 10,000 ms. Neighbours are prepared with playWhenReady=false (:135-138) at ReelsScreen.kt:1126-1127.
  - DataSaver.kt:38,46-55: data saver caps the height at 480 only; it does not change preload.
  - PlayerPool.kt:112 applies the cap. Then applyQuality(Auto) → clearVideoSizeConstraints() and stop()/prepare() (ReelsScreen.kt:715-735,1609).
How to reproduce: Turn data saver on and choose Auto. Read the selected track height in the player's analytics listener: it exceeds 480.
Impact: About 20 s of video per swipe is buffered in the background (roughly 5 MB at a 2 Mbit/s rung), most of it discarded on a fast swiper. Data-saver users get full quality, and every page does a double prepare.
Fix: Have applyQuality(Auto) re-apply dataSaverHeightCap instead of clearing it. On a metered network, preload only index+1 with a 2 s max buffer.
Acceptance:    A unit test: with data saver active, applyQuality(Auto) leaves maxVideoHeight = 480 and does not call prepare() a second time (fails now).
Effort:        S
```

```
ID:            D-10
Title:         On a dropping network Android shows nothing when playback fails (an IDLE player after an error is treated as "not waiting"), and the web retries a fatal media error without limit and never refetches an expired manifest.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ (both clients, delegated, plus ReelVideo.tsx:362-371 re-read)
Evidence:
  - Android: VideoLoading.kt:297-302 treats IDLE as not waiting, and Reels has no onPlayerError UI. The 12 s "taking longer" message with Retry only covers buffering (VideoLoading.kt:111,122).
  - Web: ReelVideo.tsx:364-366 calls hls.recoverMediaError() on every fatal MEDIA_ERROR with no counter. Any other fatal error goes to the progressive file (:370). After that a <video> error shows "This reel can't be played right now" with no retry button (:500,703-704). There is no stalled listener and no navigator.onLine handling.
How to reproduce: Android: cut the network mid-reel after the buffer drains. Web: serve a corrupt segment and watch recoverMediaError loop in the console.
Impact: A black or frozen reel with no message on Android. A busy loop on the web for a bad segment.
Fix: Android: in Reels, listen for onPlayerError and show the existing loading-error row with Retry (player.prepare()). Web: allow 2 recoverMediaError calls, then swapAudioCodec once, then the file; add a Retry button that reloads the source.
Acceptance:    An Android Compose test: a player that reports an error shows the Retry row. A web test: three fatal MEDIA_ERRORs lead to the fallback, not a fourth recover.
Effort:        S
```

```
ID:            D-11
Title:         The web reels stage fires several per-reel requests on every swipe and polls each reel, and its watch telemetry is one POST per event with no batching and no unload beacon.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ (delegated)
Evidence:
  - GET /v1/posts/:id on activation, then polled every 30 s (15 s with comments open). usePostRoom's interval also invalidates the same key, so it can be fetched twice (useReelLive.ts:12-20, commentCache.ts:23-28).
  - GET /v1/media/:id/audio-tracks (ReelsScreen.tsx:262), polled every 5 s while a track is pending.
  - GET /v1/subtitles/:id from the captions probe, and again, uncached, when captions are on (ReelsScreen.tsx:270-275, useSubtitleTrack.ts:57).
  - POST /v1/graph/relationships/batch re-keyed as authorIds grows, capped at slice(0,100) (useConnections.ts:510), so authors past the first 100 have no follow state.
  - Telemetry: HEARTBEAT_MS 5000 (telemetry.ts:15), one request per event (:88,142,151), failures dropped (:109-115). sendEventsOnUnload calls the axios sender, not sendBeacon (telemetry.ts:118-122), so play_end on pagehide is likely lost.
How to reproduce: Network panel while swiping 20 reels; count requests per reel.
Impact: About 4 to 6 API requests per swipe plus 0.2 requests per second per viewer for telemetry, all through the Next proxy and the gateway. Lost play_end events under-count watch time.
Fix: Batch telemetry (flush every 10 s, on swipe and on pagehide via navigator.sendBeacon or fetch keepalive). Dedupe the post refetch (one interval). Reuse the probe's subtitle response.
Acceptance:    A telemetry test: 6 heartbeats in 30 s produce at most 3 POSTs, and pagehide uses sendBeacon (fails now).
Effort:        S
```

```
ID:            D-12
Title:         Analytics ingest, the hottest write path in reels, issues three sequential statements per event inside one transaction (a 100-event batch is 300 round trips), writes every heartbeat into analytics.ingest_receipts, which has no partitioning or retention, and puts every events_raw row in the DEFAULT partition because no time partitions are ever created.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ
Evidence:
  - analytics-service/internal/store/postgres/events.go:201-249: for each event, INSERT receipt … RETURNING, INSERT events_raw, applySessionUpdate.
  - analytics-service/internal/service/ingest.go:172 (batch at most 200); :259-275 Scylla projection, one sequential write per play_end.
  - ingest_receipts: plain table with four indexes (migrations/004_creator_analytics_integrity.sql:15; 006_full_video_event_ingest.sql:20-36). Nothing deletes from it (grep: only migration 010's like repair).
  - events_raw: PARTITION BY RANGE (ts) with only events_raw_2024_01 and DEFAULT (database/setup.sql:13-19; cmd/server/main.go:349-352). No code creates a partition.
  - Android sends up to 100 events per batch (AnalyticsStore.kt:257-277, delegated). Web sends one per request (D-11).
How to reproduce: Count statements per batch with pg_stat_statements on QA during L3.
Impact: At 10,000 concurrent viewers with a 5 s heartbeat, about 2,000 events per second means about 6,000 write statements per second on Aurora, all landing in two ever-growing tables whose indexes slow every insert. Adding monthly partitions later means scanning the DEFAULT partition. Arithmetic, not measured.
Fix: (a) Insert receipts and events_raw with one multi-row statement each per batch (INSERT … SELECT FROM unnest(...) ON CONFLICT DO NOTHING RETURNING). (b) A monthly partition job for events_raw (and content_hourly_agg) plus a retention delete or partitioning on ingest_receipts.accepted_at.
Acceptance:    A store test asserting a 100-event batch executes at most 4 statements (fails now: 300). A migration test that next month's partition exists after startup.
Effort:        M
```

```
ID:            D-13
Title:         The reels feed reads the shared home timeline and filters the content type in Go, so a viewer whose follows mostly post photos and text can cost up to 48 sequential Scylla reads of up to 1,000 rows per page, and a category chip can run up to 15 full timeline-plus-hydration windows in one request.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ; SUSPECTED for latency (L1)
Evidence:
  - feed-service/internal/store/scylla/timelines.go:401-514. "Over-fetches and filters in Go since content_type is not a clustering key". maxContentTypeReads = 4 x 12 = 48, fetchLimit capped at 1000.
  - feed-service/internal/service/feed.go:740-741: target*3 rows requested.
  - feed-service/internal/service/reels_category.go:49,64-71 with category.go:53: maxReelCategoryWindowsWhileEmpty = 15, each window = reelTimelineWindow plus HydratePosts (collectHydratedCategoryPageWithin).
How to reproduce: A QA viewer who follows 200 accounts posting 95% photos. GET /v1/feed/reels?limit=15 and ?category=comedy; count Scylla reads and post-service batch calls.
Impact: Reels page latency grows with how much non-reel content the viewer's follows post. The category chip can make about 15 post batch, 15 media batch and 15 profile batch calls for one empty page.
Fix: Smallest: cap the while-empty budget at maxCategoryWindows (5) and return the cursor, since the client already asks again. Structural: write short-form rows to a separate Scylla table (or a content-class clustering column) at fan-out, so the reels read is a direct range read.
Acceptance:    A service test with a fake timeline of 5,000 non-reel rows: GetFlickFeedPage makes at most N reads, and a category page makes at most 5 hydrations (fails now: 15).
Effort:        S (cap) / L (table)
```

```
ID:            D-14
Title:         GET /v1/posts/trending (the Reels trending tab) recomputes an engagement score over a full week of public posts of every type on every request, with no cache and no (content_type, created_at) index.
Severity:      P2
Category:      performance
Evidence level: SUSPECTED (the query and the index list are VERIFIED-READ; the plan is not). Confirm with EXPLAIN ANALYZE on a database with production volume.
Evidence:
  - post-service/internal/store/postgres/saved.go:212-242,248-290: score expression (:153-167) over p JOIN counts, conditions created_at in (asOf-7d, asOf], visibility='public', content_type = ANY($n), ORDER BY score, LIMIT 21.
  - Indexes on posts: the only time index without an author is idx_posts_not_deleted (created_at DESC) WHERE deleted_at IS NULL (migrations/007_reels_gold_spec.sql:121). No index leads with content_type (grep of CREATE INDEX on posts).
  - Handler has no cache (internal/http/hashtag_trending_handler.go:168-188). It also returns err.Error() in a 500 (:184), noted for Track B.
How to reproduce: EXPLAIN ANALYZE of trendingQuery({"flick"}, 20, "", now) on a copy with a week of launch-volume posts.
Impact: Each trending page scans and sorts every post of the week. Cost grows with total platform volume, not with page size.
Fix: Cache the first page of each content-type set in Redis for 60 s (viewer filtering already runs after, in the service), and add CREATE INDEX CONCURRENTLY idx_posts_public_type_created ON posts (content_type, created_at DESC) WHERE deleted_at IS NULL AND visibility = 'public'.
Acceptance:    EXPLAIN shows an index range scan on the new index. A handler test shows the second call within 60 s does not query.
Effort:        S
```

```
ID:            D-15
Title:         Resumable upload parts (5 to 64 MiB) travel client → gateway → media-service → S3 instead of going to S3 through presigned part URLs, so large-reel upload bandwidth is carried by the API tier (and on the web by the Node proxy, D-2).
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ
Evidence:
  - media-service/internal/http/resumable_handler.go:110-150. The body is streamed into UploadPart, so it is not buffered in media-service (good), but it is relayed.
  - Gateway body cap lifted for /v1/media (api-gateway/pkg/edgelimits/edgelimits.go:56-61).
  - media-service pods: cpu limit 1, memory 1Gi, HPA 2 to 6 on CPU (deploy/services/media-service/values-prod.yaml:18-31).
  - Android uses resumable above 100 MiB, the web above 20 MB (ResumableUploader.kt:29; resumableUpload.ts:20).
How to reproduce: L5 in section 6.
Impact: Concurrent long-reel uploads consume gateway and media-service network and connections, competing with playlist serving (D-7) on the same pods.
Fix: Return a presigned UploadPart URL per part from the resumable session (S3 supports presigning UploadPart), keeping the session and size checks in media-service.
Acceptance:    An integration test where a part PUT goes to the storage host directly and complete still verifies size and ETag.
Effort:        M
```

```
ID:            D-16
Title:         Android: the settled reel page recomposes four times a second because playback progress is read in the page body, a cover URL is resolved per feed row although Reels draws no poster, and the settled effect is keyed on pageCount, so appending a page re-runs onReelShown and un-pauses a paused reel.
Severity:      P3
Category:      performance
Evidence level: VERIFIED-READ (delegated); the cover lookup running on the Reels surface is SUSPECTED. Confirm by logging HashtagPostHydrator calls while scrolling Reels.
Evidence:
  - progress written every PROGRESS_POLL_MILLIS = 250 (ReelsScreen.kt:1906,1674-1679) and read in ReelPage (:1511,1534).
  - GET v1/media/{coverId}/url per row (HashtagPostHydrator.kt:73-81); "there is no poster behind a reel" (ReelsScreen.kt:1634-1636).
  - LaunchedEffect keyed on pageCount (ReelsScreen.kt:1110) → onReelShown sets _paused = false (ReelsViewModel.kt:1058).
How to reproduce: Layout Inspector recomposition counts on a playing reel. Pause a reel near the end of a page and wait for the next page to load.
Impact: Wasted main-thread work and battery while watching, wasted requests per row, and a pause the viewer did not undo.
Fix: Read progress in a child composable (or pass a lambda), skip cover resolution for the Reels surface, and key the settled effect on the settled page only.
Acceptance:    A Compose test counting recompositions of ReelPage over 1 s (at most 1). A ViewModel test showing paused stays true when pageCount changes.
Effort:        S
```

```
ID:            D-17
Title:         Web upload processing is polled every 3 s with no maximum duration or attempt count, and errors are swallowed.
Severity:      P3
Category:      performance
Evidence level: VERIFIED-READ (delegated)
Evidence:      src/features/upload/useUploadStudio.ts:287-312; reelsApi.ts:217 (GET /v1/media/:id/renditions).
How to reproduce: Upload a reel whose transcode is quarantined and leave the tab open.
Impact: An unbounded request stream per open upload tab during a transcode backlog (D-3), and no message to the creator.
Fix: Back off to 10 s after 1 minute, stop after 30 minutes with a "still processing, we'll notify you" message.
Acceptance:    A hook test with fake timers asserting polling stops and the message shows.
Effort:        S
```

## 4. Checked and found sound

| Area | What holds | Evidence |
|---|---|---|
| Feed pagination | Keyset on the timeline timeuuid, no OFFSET. `limit` capped at 50 (`rankedPageParams`) | `feed-service/internal/http/handler.go:555-572`, `store/scylla/timelines.go:415-444` |
| Feed batching | One media batch (at most 50, refused above) and one post batch per page; no per-reel server call from the feed | `media-service/internal/service/media_batch.go:37-40`, feed track section 2 |
| Hydration failure | 503 `FEED_UNAVAILABLE`, never raw ids | `feed-service/internal/http/handler.go:286-297` |
| Simple upload | Presigned PUT straight to S3, 15 min, Content-Type and Content-Length signed; confirm checks size, type and magic | map section 4 step 2, `media-service/internal/service/upload.go:131-400` |
| Resumable chunks inside media-service | Streamed into UploadPart, not buffered | `resumable_handler.go:110-139` |
| Segment delivery | Signed CloudFront URLs. The signature is not in the CDN cache key, so one edge copy serves all viewers. The protected default behaviour needs the key group | `infra/terraform/modules/media/main.tf:304-319` |
| Caches cannot leak | Authorisation is never cached (`authz.go:511-512`). Signed URLs are viewer-independent and handed out only after a per-request decision. Playlists are `private, no-store`. `/serve` redirects are `private, max-age=60` with `Vary: Cookie, Authorization, X-User-Id` | `delivery/authz.go:511-517`, `http/handler.go:654-669,725` |
| Signature lifetime | Segments 2 x duration + 15 min (cap 12 h); everything else 5 min; the reuse cache is bounded at 20,000 entries | `delivery/sign_cache.go:52-89` |
| Reel ladder | Capped at 720p, 360p + 720p HLS rungs repackaged from MP4 by stream copy (no second encode), BANDWIDTH measured from segments | VERIFIED-RUN: `TestHLSLadderCapsReelsAt720p`, `TestHLSRungIsCopiedFromItsRendition`, `TestHLSMasterBandwidthIsTheMeasuredPeak` |
| Transcode durability | Offsets committed after the durable effect; 8 attempts with backoff then quarantine; stall sweeper; per-ffmpeg-call timeout and per-job budget; size cap and scratch-space check before download; probe limits | `cmd/worker/main.go:203-216,373-415,581-596`, `processing/limits.go:129-158`, map section 4 steps 6 to 7 |
| Authorisation outbound timeout | 3 s on the post-service call; an unresolved answer is a 503, not a silent allow | `delivery/authz.go:171-177`, `http/delivery_viewer.go:34-45` |
| Analytics batch | At most 200 events; one ownership lookup per distinct content per batch; idempotent receipts | `analytics-service/internal/service/ingest.go:172-214` |
| Android player | One playing player (`playOnly`), pool of 4 (+1 sound player), listeners removed, pool released on dispose; disk-queued analytics (cap 5,000, 100 per request, WorkManager with backoff) | `PlayerPool.kt:141-183`, `ReelsScreen.kt:1851-1856`, `AnalyticsStore.kt:257-277`, `AnalyticsModule.kt:76-108` (delegated) |
| Web player lifecycle | One hls.js instance per active reel, destroyed in the effect cleanup and on fallback; no leak path found | `ReelVideo.tsx:313-388` |
| Upload client retries | Web: 5 attempts per part, 1 to 16 s jittered backoff, no retry on 401/403/404/413. Android: 6 attempts, 1 to 30 s | `resumableUpload.ts:23-28,77-80`, `ResumableUploader.kt:359-367` (delegated) |

## 5. Outbound calls in the reels paths

| Caller → callee | Timeout | Pool | Retry / backoff | Breaker | Evidence |
|---|---|---|---|---|---|
| media-service → post-service media-access | 3 s | Go default (2 idle per host) | none | none | `delivery/authz.go:176`, `cmd/server/main.go:548` (D-7) |
| media-service → S3 (playlist read) | request context only; not traced further | AWS SDK default | SDK default retries | none | `service/playback.go:271` |
| media worker → S3 | `s3MetadataCall`, `s3Timeout(size)` | SDK default | in-place transient retry, 8 attempts | none | `cmd/worker/main.go:634,663,695` |
| media worker → Rekognition | not traced | SDK default | SDK default | none | `cmd/worker/main.go:985-1013` |
| media-service → OpenAI dubbing | `openAIRequestTimeout` | default | not traced | none; off in prod (`MEDIA_DUBBING_BACKEND` unset) | `internal/dubbing/openai.go:80` |
| media-service → Whisper captions | `whisperRequestTimeout` | default | not traced | none | `internal/captions/whisper.go:56` |
| Copyright fingerprint | in-worker, 10 s / 30 s / 1 min per step | — | job states | none | `cmd/worker/fingerprint_runner.go:261-323` |
| feed-service → graph, post, media, profile, analytics, trust-safety | see feed track D-section on outbound calls | | | | feed track |
| Web Next proxy → gateway and CDN | 1 s connect, no request or idle timeout | Node default | one retry of a failed read | none | `route.ts:28-31,166-176` |
| Android OkHttp (API) | 45 s call timeout | shared client | `RetryInterceptor` 3 attempts, 300 ms to 3 s, on transport errors and 503/504 | none | `NetworkModule.kt:194-197`, `RetryInterceptor.kt:126-131` (delegated) |

No circuit breaker exists on any reels outbound call. The only budgets are the timeouts above.

## 6. The load test that is needed

Run against a QA stack with production instance sizes, Aurora, MSK (6 partitions), Scylla RF 3 and CloudFront. Seed data:

- 200,000 users;
- 50,000 reels (mixed 15 to 90 s, 5% at 5 to 10 min, portrait), 20% of them carrying an added sound;
- home timelines in which 10% of rows are reels;
- 2 million analytics events back-filled.

All scripts are k6 unless noted.

| ID | Scenario | Rate | Pass thresholds |
|---|---|---|---|
| L1 | `GET /v1/feed/reels` first and next pages, 70% plain, 20% `following_only`, 10% `category` | ramp to 300 rps over 10 min, hold 20 min | p95 < 300 ms, p99 < 800 ms, errors < 0.5%, feed-service CPU < 70% |
| L2 | Playback start: master + one child playlist per reel, plus 2 preload pairs per swipe (Android shape), viewers swiping every 4 s | 3,000 concurrent virtual viewers (about 1,500 playlist rps) | `/hls/` p95 < 150 ms, post-service media-access p95 < 50 ms, 0 503s, CloudFront hit ratio > 90% after warm-up |
| L3 | `POST /v1/analytics/events`: Android shape (batches of 50 to 100) and web shape (single events, every 5 s per viewer) | 10,000 concurrent viewers | p95 < 200 ms, errors < 0.1%, Aurora write latency p95 < 20 ms, no lock waits on `playback_sessions` |
| L4 | Upload burst: 200 reels of 30 to 60 s (1080x1920, 20 to 60 MB) in 5 min via init + PUT + confirm, plus three 10-min 4K portrait reels at t=0 | as stated | p95 confirm-to-`ready` < 120 s for the short reels, no item in `processing` past 15 min, quarantine count 0, consumer lag back to 0 within 20 min |
| L5 | Resumable upload: 50 concurrent 1 GB uploads with 64 MiB parts (D-15) while L2 runs at 50% | as stated | L2 thresholds still met, media-service memory < 70% of its limit, no part error rate > 1% |
| L6 | Web proxy: 500 browsers (Playwright or k6 browser) on `/reels` swiping every 4 s | as stated | Next server egress < 5% of the video bytes watched (proves D-1 and D-2 are fixed), p95 time to first frame < 1.5 s on throttled 4G |
| L7 | Soak: L1 + L2 + L3 at 50% for 4 h | — | no memory growth > 10% per hour in media-service, feed-service or analytics-service; `events_raw` partition routing correct (D-12) |

Record p50, p95, p99, error rate, consumer lag (`media-transcode-worker`, `post-service-media-transcode`), Postgres connections per service and CloudFront hit ratio for each run, in a results file in this folder.

## 7. For the other tracks

- **Track B:** `GET /v1/posts/trending` returns `err.Error()` in its 500 (`post-service/internal/http/hashtag_trending_handler.go:184`). Deleted or blocked reels stay fetchable from cached HLS segments for up to 12 h through an already signed URL (`delivery/sign_cache.go:42-50`); no CDN invalidation exists (map question 13).
- **Track C:** one invalid event rejects a whole analytics batch (`analytics-service/internal/service/ingest.go:186-205`), and Android retries that batch 5 times before dropping it. Check that a poison event cannot hold good events behind it. Web `play_end` is likely lost on pagehide (D-11), so watch time is under-counted.
- **Track E:** no transcode lag, queue-age or quarantine alert, and the worker has no metrics scrape (D-3). There is no circuit breaker on any media outbound call (section 5). Android shows no message when playback fails (D-10).
- **Track A:** on Android, a reel the viewer paused starts playing again when the next feed page loads (D-16). On web, follow state is missing for authors after the first 100 on the page (D-11). Portrait reel quality is capped near 404x720 (D-5).

## Not verified

- Every number in sections 2, 3 and 6 is arithmetic or an estimate. No latency, throughput, byte size or queue time was measured.
- D-1: the browser CORS outcome against the real CloudFront distribution (needs a staging stack and a browser).
- D-5: the actual output width of a portrait encode (needs ffmpeg; the two ffmpeg-backed tests were skipped).
- Query plans for the trending query, `/v1/posts/recent`, `GetMediaBatch`, `GetMediaAccessFacts`, and the post-service visibility path after `viewerMayAccessPostMedia` (not read to the end). No EXPLAIN was run.
- Media3 and hls.js library defaults: cache key, how much a paused neighbour buffers, hls.js loader and fragment-before-append behaviour.
- AWS SDK client settings (timeouts, retries, pool) for S3 and Rekognition in media-service and the worker.
- The CloudFront cache hit ratio, and whether `media_custom_domain_enabled` is set in prod (map 5.5); without it `MEDIA_CDN_BASE_URL=https://media.cleestudio.com` may not resolve to the distribution.
- The Coil `ImageLoader` configuration and `CommentsSheet` internals on Android.
- Gateway proxy timeouts and connection pooling toward media-service for long playlist or segment streams.
- Whether `media.events.long` exists in MSK (map question 2); if it does not, long jobs fall back to the short lane and make D-3 worse.
- Memory growth while swiping 50 reels on a real device and in a real browser (needs the founder's device and a browser profile run).
