# Reels: Stage 0 map

- **Audit date:** 2026-11-17
- **Backend and Android tree:** `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` @ `770f5ed9d`. Dirty: 727 paths in `git status --porcelain` (uncommitted admin-service and other changes). The audit reads the working tree as it is.
- **Web tree:** `C:\workspace\wt\pb-echo` (repo `postbook-ui`), branch `feat/echo-web` @ `ca96921`, clean (0 paths). `docs/harness/AGENT-RULES.md:12` names `C:\workspace\wt\pb-integ` as the web tree; that folder does not exist here (`C:\workspace\wt` holds `aw-marks` and `pb-echo` only).
- **How this map was built:** backend sections (1 to 6, 10) were read directly from the code. Client (7), CI (8) and deployment (9) sections come from three delegated read-only passes; a sample of their claims was re-checked (MSK topic list, prod worker classes, web step schema, Android length constant) and held. Treat any other line in 7 to 9 as VERIFIED-READ by a single reader.
- Nothing was executed except `git rev-parse`, `git status` and read-only searches.

All backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Web paths are relative to `C:\workspace\wt\pb-echo`. Android paths are relative to `mobile/android/`, and `…/` stands for `src/main/kotlin/com/us/android/`.

## 1. Stack found in the repository

| Layer | What is there | Evidence |
|---|---|---|
| Backend language | Go (workspace `Architecture/go.work`), Gin | `media-service/internal/http/handler.go:120` |
| Upload, transcode, delivery, sounds | `media-service`: HTTP server (`cmd/server`), transcode worker (`cmd/worker`), one-off `cmd/fingerprint-backfill` | `media-service/cmd/` |
| Posts and engagement | `post-service`: posts with `content_type`, likes, comments, shares, saves, reel drafts, schedule, offline copies, sound attach | `post-service/internal/http/handler.go:65-443` |
| Reels feed | `feed-service` `/v1/feed/reels` and `/v1/feed/flicks` | `feed-service/internal/http/handler.go:94-95` |
| Views and watch time | `analytics-service` `POST /v1/analytics/events` | `analytics-service/internal/http/handler.go:73` |
| Reports, moderation, copyright cases | `trust-safety-service` | `trust-safety-service/internal/http/handler.go:69-138` |
| Earnings | `monetization-service` creator fund | `monetization-service/internal/http/handler.go:203-211` |
| Search | `search-service` (OpenSearch) | `search-service/internal/http/handler.go:145-188` |
| Edge | `api-gateway`: token verify, identity strip and stamp, rate limit, `/internal/` refusal, body cap | `api-gateway/cmd/server/main.go:341-357` |
| Transcoding | ffmpeg and ffprobe inside the worker container; no managed transcoder anywhere in `infra/` | `media-service/Dockerfile.worker:26`, `media-service/internal/processing/` |
| Datastores | PostgreSQL (media, post, feed control, analytics, trust), ScyllaDB (timelines, engagement, view counters), Redis (rate limits, caches, counters), OpenSearch | migrations folders, `Architecture/docker/scylla/schema*.cql` |
| Object storage | S3 (IRSA) in deployed environments, MinIO locally; one media bucket | `media-service/cmd/worker/main.go:144-145`, `infra/terraform/modules/media/main.tf:31-43` |
| CDN | CloudFront with signed URLs (key group), `public/*` unsigned | `infra/terraform/modules/media/main.tf:274-319` |
| Events | Kafka: `media.events`, `media.events.long`, `media.events.dlq`, `media.copyright.pairs`, `social.events.v1`, `identity.events.v1`, `platform.purge-acks.v1` | `media-service/cmd/worker/transcode_classes.go:63-72`, `post-service/internal/consumers/media.go:59-61` |
| Android | Kotlin, Compose, Media3/ExoPlayer, WorkManager, Banuba Video Editor SDK (optional) | `mobile/android/settings.gradle.kts:50-296` |
| Web | Next.js on Bun, hls.js | `src/features/reels/components/ReelVideo.tsx:336` |
| Deployment | Helm chart `charts/atpost-service`, values in `deploy/services/<service>/`, Terraform `infra/terraform`, Azure `infra/azure`, dev box `infra/devbox` | section 9 |
| CI | GitHub Actions in `.github/workflows/` | section 8 |

## 2. Name mapping

| Harness name | Backend | Android | Web |
|---|---|---|---|
| reel | `posts.content_type` = `flick` (canonical), `reel` and `short` accepted as legacy (`shared/postclassify/classify.go:27-30,62-68`; `post-service/internal/service/post_content_types.go:37-40`). Media side: `media_subtype:"reel"` on init; "short-form" / `isReel` in the worker (`media-service/cmd/worker/main.go:732`) | "Reels" (`feature/feed/…/ui/reels/`), `FeedSurface.Reels("reels")` (`core/model/…/FeedItem.kt:390`), posts created with `content_type=flick` | `/reels`, `features/reels`; upload type `reel` or `short`; telemetry `content_type:"flick"` |
| reels feed | `GET /v1/feed/reels` = `GET /v1/feed/flicks` (same service function, `feed-service/internal/service/feed.go:1021-1023`) | `v1/feed/{surface}` with `reels` | `/v1/feed/reels` |
| upload | `/v1/media/init` + presigned PUT + `/v1/media/confirm`, or `/v1/media/upload/resumable/*` | `core/media/…/upload/` | `features/reels/data/reelsApi.ts`, `lib/resumableUpload.ts` |
| processing | "transcode" (`media.events`, `media_assets.processing_status`) | polls `/v1/media/:id/status` | polls `/v1/media/:id/renditions` |
| cover | `thumb_150` variant, `POST /v1/media/:id/cover-frame`, `POST /v1/videos/:id/cover-frame`, custom cover image upload | — | frame picker in `DetailsStep.tsx` |
| audio | "sounds": `audio_tracks` (original sound per source video), `audio_library` (curated), `/v1/audio`, `/v1/audio-library`; "audio tracks" = alternate/dubbed language tracks `/v1/media/:id/audio-tracks` | `SoundsApi`, `AudioTracksApi` | `soundsApi.ts`, `audioTracksApi.ts` |
| draft | `reel_drafts` at `/v1/reels/drafts` (reels); `post_drafts` at `/v1/posts/drafts` (photo and text) | reel drafts read-only | create, edit, publish |
| save | bookmark: `/v1/posts/:id/bookmark`, `/v1/saved` | same | same |
| view and watch time | analytics events `play_start`, `watch_heartbeat`, `play_end`, `impression`, `milestone`; Scylla `social_analytics.watch_sessions`, `reel_views`, `reel_view_counts` | `VideoWatchTracker` | `playback/telemetry.ts` |
| download off | `posts.allow_download`; it only gates in-app offline copies | "Allow download" | `allow_download` |
| "Keep a copy" | offline copies: `post_offline_copies`, `/v1/posts/:id/offline` | "Save offline" | `features/offline` |
| comments off | `posts.no_comments` | "Allow comments" | `no_comments` |
| report | `POST /v1/reports` (trust-safety-service) | `ReportApi` | `useReport.ts` |
| long video (out of scope) | `long_video`, "Tube", "PostTube", "MTube" | `:feature:tube` | `/posttube` |

## 3. Route tables

### 3.0 How every route is reached (api-gateway)

| Item | Fact | Evidence |
|---|---|---|
| Chain | request id → strip identity and verify token → rate limit → refuse any `/internal/` path with 404 → session revocation → proxy | `api-gateway/cmd/server/main.go:333-348` |
| Token | A request with no token passes as anonymous; an invalid token is 401 except on the auth public paths. The gateway sets `X-User-Id` from the token | `api-gateway/cmd/server/main.go:959`, `pkg/publicauth/publicauth.go:70` |
| Rate limit | Per IP 100 rps (burst 200) and per user 60 rps (burst 120), in memory per pod plus Redis fleet-wide when Redis answers; fails open on a Redis error. Env `RATE_LIMIT_IP_PER_SEC`, `RATE_LIMIT_USER_PER_SEC`. This is the only limit on most routes below; the table says "edge only" for those | `api-gateway/cmd/server/main.go:256-268,1215-1240`, `ratelimit_redis.go:77-93` |
| Body cap | Global cap, lifted for `/v1/media`, `/v1/audio`, `/v1/subtitles` (chunks up to 64 MiB) | `api-gateway/pkg/edgelimits/edgelimits.go:56-61` |
| Internal key | Stamped on upstream calls to `/v1/posts`, `/v1/reels`, `/v1/uploads`, `/v1/videos`, `/v1/saved`, `/v1/comments`, `/v1/hashtags`, `/v1/creators`, `/v1/feed`, `/v1/search`, `/v1/analytics`, `/v1/reports`. NOT stamped on `/v1/media`, `/v1/audio`, `/v1/audio-library`, `/v1/subtitles` | `api-gateway/pkg/internalroutes/internalroutes.go:367-441` |
| Prefix routing | `/v1/uploads`, `/v1/videos`, `/v1/reels`, `/v1/saved`, `/v1/hashtags`, `/v1/comments`, `/v1/creators`, `/v1/posts` → post-service; `/v1/feed` → feed-service; `/v1/audio-library`, `/v1/audio`, `/v1/media`, `/v1/subtitles` → media-service; `/v1/search` → search-service; `/v1/reports`, `/v1/appeals` → trust-safety-service; `/v1/analytics` → analytics-service; `/v1/monetization` → monetization-service | `api-gateway/cmd/server/main.go:555-694` |
| Not routed | `/v1/clips` (media-service `clips_handler.go:52-55`) and `/v1/series` (post-service `handler.go:347`) have no gateway prefix that was found | `api-gateway/cmd/server/main.go:480-700` |

Authentication words used below: **public** = no token needed; **user** = user token required by the service; **gate** = no token required, but the media delivery gate decides per asset by asking post-service as the viewer (anonymous allowed for public content); **key** = `X-Internal-Service-Key`; **svc-token** = signed service token; **admin** = admin-service signed token.

### 3.1 media-service (`media-service/internal/http/`)

Auth: media-service verifies the JWT itself (`middleware.go:171-196`, HS256 and RS256, bearer or `access_token` cookie). Read routes carry no auth middleware and take the viewer from the `X-User-Id` header (`delivery_viewer.go:26-32`). No HTTP rate-limit middleware exists in this service; the only service-level limit is the upload budget of 10 per hour and 30 per day per user, Redis keys `upload:rate:<user>:hour|day`, fail-open (`internal/service/ratelimit.go:21-55`, `validation.go:37-38`).

| Method | Path | Handler file:line (registration) | Auth | Rate limit | Scope |
|---|---|---|---|---|---|
| POST | `/v1/media/init` | `handler.go:134` (`InitUpload` :304) | user | upload budget + edge | yes |
| POST | `/v1/media/confirm` | `handler.go:135` (`ConfirmUpload` :360) | user (owner check `service/upload.go:230`) | edge only | yes |
| DELETE | `/v1/media/:mediaId` | `handler.go:136` (`DeleteMedia` :528) | user | edge only | yes |
| PATCH | `/v1/media/:mediaId/alt-text` | `handler.go:137` | user | edge only | yes |
| POST | `/v1/media/:mediaId/extract-frame` | `handler.go:145` (:813) | user | edge only | yes |
| POST | `/v1/media/batch` | `handler.go:159` (:502) | gate | edge only | yes |
| GET | `/v1/media/:mediaId` | `handler.go:160` | gate | edge only | yes |
| GET | `/v1/media/:mediaId/status` | `handler.go:161` (:562) | gate | edge only | yes |
| GET | `/v1/media/:mediaId/url` | `handler.go:162` (:463) | gate | edge only | yes (playback) |
| GET | `/v1/media/:mediaId/url/:variant` | `handler.go:163` | gate | edge only | yes |
| GET, HEAD | `/v1/media/:mediaId/serve` | `handler.go:164,170` (:616) | gate; 307 to a signed URL | edge only | yes (playback, cover) |
| GET, HEAD | `/v1/media/:mediaId/serve/:variant` | `handler.go:165,171` | gate; `dub_<lang>_<rung>` variants play alternate audio | edge only | yes |
| GET | `/v1/media/:mediaId/hls/:playlist` | `handler.go:172` (:701) | gate; playlist rewritten with signed segment URLs | edge only | yes (manifest) |
| GET | `/v1/media/:mediaId/hls-seg/:name` | `handler.go:175` (`handler_anonymous.go:50`) | gate; bytes streamed by the service, only for `access_scope='anonymous'` assets | edge only | yes (segment) |
| GET | `/v1/media/:mediaId/audio-tracks` | `audio_tracks_handler.go:38` | gate | edge only | yes |
| POST | `/v1/media/:mediaId/audio-tracks` | `audio_tracks_handler.go:39` | user (owner) | edge only | yes |
| POST | `/v1/media/:mediaId/audio-tracks/generate` | `audio_tracks_handler.go:40` | user (owner); 503 with no dubbing backend | edge only | yes |
| DELETE | `/v1/media/:mediaId/audio-tracks/:trackId` | `audio_tracks_handler.go:41` | user (owner) | edge only | yes |
| GET | `/v1/media/:mediaId/download` | `download_handler.go:43` (:54) | uploader only, else 404 (`service/download.go:57-88`) | edge only | yes |
| GET | `/v1/media/:mediaId/renditions` | `renditions_handler.go:16` (:21) | none on the route | edge only | yes |
| POST | `/v1/media/:mediaId/frames` | `renditions_handler.go:17` | user | edge only | yes |
| POST | `/v1/media/upload/resumable/init` | `resumable_handler.go:19` (:32) | user | upload budget + edge | yes |
| GET | `/v1/media/upload/resumable/:uploadId` | `resumable_handler.go:20` | user | edge only | yes |
| POST | `/v1/media/upload/resumable/:uploadId/chunk` | `resumable_handler.go:21` (:114) | user | edge only | yes |
| POST | `/v1/media/upload/resumable/:uploadId/complete` | `resumable_handler.go:22` (:152) | user | edge only | yes |
| GET | `/v1/media/:mediaId/suggested-frames` | `studio_handler.go:34` | user | edge only | yes |
| POST | `/v1/media/:mediaId/cover-frame` | `studio_handler.go:35` (:272) | user | edge only | yes |
| POST, GET, PUT, DELETE | `/v1/media/studio/sessions[/:id]` | `studio_handler.go:21-24` | user | edge only | yes (editor sessions) |
| GET | `/v1/media/studio/stickers`, `/sticker-packs`, `/templates` | `studio_handler.go:26-30` | public | edge only | yes |
| POST | `/v1/media/studio/stickers/:id/use` | `studio_handler.go:28` | public; spawns a goroutine per call (:194) | edge only | yes |
| PUT, DELETE, GET, POST | `/v1/media/slots/*` | `slots_handler.go:20-25` | mixed | edge only | no (avatars and covers of profiles) |
| POST | `/v1/audio/extract/:mediaId` | `audio_handler.go:21` | user | edge only | yes |
| GET | `/v1/audio/trending` | `audio_handler.go:24` | user | edge only | yes |
| GET | `/v1/audio/search` | `audio_handler.go:25` | public | edge only | yes |
| GET | `/v1/audio/:audioId` | `audio_handler.go:26` | public route; audience gated on the source video (memory note, verify) | edge only | yes |
| GET | `/v1/audio/:audioId/url` | `audio_handler.go:27` | as above | edge only | yes |
| GET, HEAD | `/v1/audio/:audioId/serve` | `audio_handler.go:30-31` (`sound_handler.go:122`) | as above | edge only | yes |
| POST | `/v1/audio/:audioId/use` | `audio_handler.go:32` | user | edge only | yes |
| POST | `/v1/audio/voiceover` | `audio_handler.go:33` | user; multipart | edge only | yes |
| GET | `/v1/audio-library`, `/v1/audio-library/:audioId` | `audio_handler.go:38-39` | public | edge only | yes |
| GET, HEAD | `/v1/audio-library/:audioId/serve` | `audio_handler.go:42-43` | public | edge only | yes |
| POST | `/v1/audio-library/:audioId/use` | `audio_handler.go:46` | user | edge only | yes |
| GET | `/v1/subtitles/:mediaId`, track route, `/:mediaId/status` | `clips_handler.go:63,67,73` | none on the route | edge only | yes |
| POST | `/v1/subtitles/:mediaId`, `/:mediaId/auto`, `/:mediaId/request` | `clips_handler.go:68,69,74` | user | edge only | yes |
| PATCH | `/v1/subtitles/:mediaId`, `/:mediaId/:language` | `clips_handler.go:76`, `subtitles_mine_handler.go:38` | user | edge only | yes |
| GET | `/v1/subtitles/mine` | `subtitles_mine_handler.go:37` | user | edge only | yes |
| POST, GET | `/v1/clips/:postId` | `clips_handler.go:54-55` | user / none | n/a | yes (no gateway prefix found) |
| DELETE | `/v1/media/internal/orphan/:mediaId` | `handler.go:197` | key | none found | yes |
| DELETE | `/v1/media/internal/:mediaId` | `handler.go:201` | key | none found | yes (post purge) |
| POST | `/v1/media/internal/:mediaId/anonymize` | `handler.go:204` | key | none found | yes (anonymous posts) |
| PATCH | `/v1/media/internal/:mediaId/alt-text` | `handler.go:207` | key, refuses user identity | none found | yes |
| POST | `/v1/media/internal/claims`, `/claims/release` | `media_claims_handler.go:66-67` | key, refuses user identity | none found | yes |
| POST | `/v1/media/internal/:mediaId/reprocess` | `handler.go:216` (`reprocess_handler.go:32`) | key | none found | yes (recovery) |
| POST | `/v1/media/internal/:mediaId/sound` | `handler.go:219` (`sound_handler.go:66`) | key | none found | yes |
| GET | `/v1/media/internal/sounds/:audioId/extras` | `handler.go:222` | key | none found | yes |
| POST | `/v1/media/internal/sounds/:audioId/takedown` | `handler.go:226` | key | none found | yes |
| POST, GET | `/v1/media/internal/:mediaId/fingerprint` | `handler.go:229-230` | key | none found | yes (copyright shadow) |
| POST | `/v1/media/internal/recordings/import` | `handler.go:244` | key + svc-token (live-service-v2) | none found | no (live), shares the transcode queue |
| POST | `/v1/media/internal/chat-attachment/reserve`, GET `/:mediaId/profile-authority`, image-bytes, document-bytes, doorstep-photo | `handler.go:208-239` | key (+ svc-token) | none found | no |
| GET, POST, PATCH | `/v1/media/internal/admin/sounds/library[...]` (7 routes) | `admin_sounds_handler.go:78-85` | admin | none found | yes (curated sounds) |
| GET | `/v1/media/admin/evidence/:mediaId` | `partner_evidence.go:21` | admin | none found | no |
| POST | `/internal/v1/media/faces/compare`, liveness, dating photo and clip routes | `face_compare_handler.go:80-82`, `dating_*_handler.go` | key | none found | no |
| GET | `/metrics`, `/livez`, `/readyz`, `/healthz` | `cmd/server/main.go:446-447` | none | none | yes |

Removed route worth knowing: `POST /v1/media/upload/presigned` was deleted on 21 Oct 2026 (`handler.go:138-142`).

### 3.2 post-service (`post-service/internal/http/`)

Auth: every `/v1` route requires the internal key when one is configured (`handler.go:76-85`), so only the gateway and peer services reach it; the caller is the `X-User-Id` header the gateway sets. Reads accept an empty `X-User-Id` (anonymous) where the handler allows it (`post_reads_handler.go:52,110`). Service-level limits: post creation 20 per hour and 50 per day per user, keys `post:rate:<user>:hour|day` (`internal/service/ratelimit.go:26-70`, called at `create_post_handler.go:335`); `idempotent` middleware on comment and reply writes (`handler.go:66,146,325`).

| Method | Path | Handler file:line | Auth | Rate limit | Scope |
|---|---|---|---|---|---|
| POST | `/v1/posts` | `handler.go:90` (`create_post_handler.go:326`) | user | post budget + edge; `Idempotency-Key` supported (`post_create_idempotency` table) | yes (publish a reel) |
| POST | `/v1/posts/batch` | `handler.go:91` (`post_reads_handler.go:308`) | user or anonymous | edge only | yes |
| GET | `/v1/posts/categories` | `handler.go:92` | public | edge only | yes |
| GET | `/v1/posts/recent` | `handler.go:93` (`post_reads_handler.go:83`) | public | edge only | yes (feed fill) |
| GET | `/v1/posts/trending` | `handler.go:284` (`hashtag_trending_handler.go:168`) | public | edge only | yes (discovery) |
| GET | `/v1/posts/by-sound/:soundId` | `handler.go:99` (`sounds.go:118`) | public | edge only | yes |
| POST | `/v1/posts/:postId/sound` | `handler.go:100` (`sounds.go:86`) | user | `ErrSoundRateLimited` path (`sounds.go:62`) + edge | yes ("Use this sound") |
| GET | `/v1/posts/by-author/:authorId`, `/counts` | `handler.go:102-103` | public | edge only | yes (profile grid) |
| GET | `/v1/posts/offline` | `handler.go:107` (`offline_copies.go:153`) | user | edge only | yes |
| POST | `/v1/posts/offline/check` | `handler.go:108` (`offline_copies.go:129`) | user | edge only | yes |
| POST, DELETE | `/v1/posts/:postId/offline` | `handler.go:109-110` (`offline_copies.go:98,173`) | user | `OFFLINE_LIMIT` code exists; edge | yes |
| GET | `/v1/posts/:postId` | `handler.go:116` (`post_reads_handler.go:44`) | public (visibility checked) | edge only | yes |
| DELETE | `/v1/posts/:postId` | `handler.go:120` (`delete_post.go:15`) | user (owner) | edge only | yes |
| POST | `/v1/posts/:postId/restore` | `handler.go:121` | user (owner) | edge only | yes |
| GET | `/v1/posts/me/deleted`, `/me/scheduled`, `/me/summary` | `handler.go:122,125,131` | user | edge only | yes |
| PATCH | `/v1/posts/:postId/schedule` | `handler.go:126` (`schedule.go:76`) | user (owner) | edge only | yes |
| PUT | `/v1/posts/:postId/pin` | `handler.go:127` | user (owner) | edge only | yes |
| PATCH | `/v1/posts/:postId` | `handler.go:130` (`post_edit.go:184`) | user (owner) | edge only | yes (edit, creator controls) |
| GET, PUT | `/v1/posts/:postId/private-shares` | `handler.go:133-134` | user (owner) | edge only | yes (audience) |
| PATCH | `/v1/posts/:postId/distribution` | `handler.go:175` (`video_tools_handler.go:36`) | user (owner) | edge only | yes |
| PUT | `/v1/posts/:postId/membership` | `handler.go:418` | user (owner) | edge only | yes (members-only) |
| POST | `/v1/posts/:postId/resubmit` | `handler.go:157` (`feedback_handler.go:121`) | user (owner) | edge only | yes (after rejection) |
| POST | `/v1/posts/:postId/copyright-review/request` | `handler.go:421` (`copyright_review.go:398`) | user (owner) | edge only | yes |
| GET | `/v1/posts/:postId/remix-token` | `handler.go:360` (`flick_series_handler.go:164`) | user | edge only | yes |
| GET | `/v1/posts/:postId/series` | `handler.go:185` | public | edge only | yes |
| POST, GET, DELETE | `/v1/posts/:postId/crossposts[...]`, `/v1/crossposts/mine` | `crosspost.go:16-22` | user | edge only | yes |
| GET | `/v1/posts/action-labels`, `/:postId/action/stats`; `/v1/links/:id` | `handler.go:114-115,211` | mixed | edge only | check only where reels differ |
| POST, DELETE, GET | `/v1/posts/:postId/reactions`, `/reactions/me`, `/reactions/counts`, `/react`, `/like` | `handler.go:139-144,259-260` | user | edge only | audited under feed; re-check only where reels differ |
| POST | `/v1/posts/:postId/share` | `handler.go:145` (`reactions_handler.go:155`) | user | edge only | audited under feed |
| POST, GET | `/v1/posts/:postId/comments`, `/comments/around/:commentId` | `handler.go:146-148` | user / public | idempotency + edge | audited under feed; re-check `no_comments` |
| all | `/v1/comments/*` (reply, replies, reaction, like, dislike, delete, edit, inbox, heart, pin) | `handler.go:325-343` | user | edge only | audited under feed |
| POST, DELETE, PUT | `/v1/posts/:postId/bookmark`, `/bookmark/collection`; `/v1/posts/bookmarks`; `/v1/saved/*` | `handler.go:101,152-156,263-273` | user | edge only | audited under feed |
| POST, DELETE, GET | `/v1/posts/:postId/repost`, `/repost/me`, `/reposters`; `/v1/users/:userId/reposts`; `/v1/creators/:creatorId/reposts` | `reposts_handler.go:26-35` | user / public | edge only | audited under feed. Founder rule: reels carry no repost; verify it is refused |
| POST, GET, PATCH, DELETE | `/v1/reels/drafts`, `/v1/reels/drafts/:draftId` | `drafts.go:20-24` | user (owner) | `DRAFT_QUOTA` code; edge | yes |
| POST | `/v1/reels/drafts/:draftId/publish` | `drafts.go:25` (:216) | user (owner) | edge only | yes |
| POST | `/v1/reels/:reelId/view` | `reel_engagement.go:25` (`ReelViewGone` :84) | retired, answers gone | n/a | yes (confirm it cannot count views) |
| POST | `/v1/reels/batch/counts` | `reel_engagement.go:26` (:90) | user | edge only | yes |
| GET | `/v1/reels/saved`, `/v1/reels/liked` | `reel_engagement.go:27-28` (:125,:160) | user | edge only | yes |
| GET, POST | `/v1/reels/feed`, `/v1/reels/feed/refresh` | `reel_feed.go:22-23` | retired (`reelFeedGone`) | n/a | yes (confirm gone) |
| GET | `/v1/reels/hashtags/trending` | `reel_discovery.go:15` | public | edge only | yes |
| GET | `/v1/reels/slug/:slug` | `reel_discovery.go:18` (:43) | public | edge only | yes |
| GET | `/v1/reels/moderation/flagged`, `/v1/reels/:reelId/moderation` | `reel_discovery.go:21-22` | `requireModerator()` | edge only | yes |
| GET | `/v1/uploads/flicks`, `/videos`, `/posts`, `/counts` | `my_uploads.go:19-22` (:62) | user | edge only | yes |
| POST | `/v1/uploads/bulk`, `/bulk-delete` | `my_uploads.go:26-27` | user | edge only | yes |
| DELETE | `/v1/uploads/:postId` | `my_uploads.go:28` (:124) | user (owner) | edge only | yes |
| GET, PATCH, POST | `/v1/videos/:videoId`, `/trim`, `/category`, `/cover-frame`, `/publish` | `handler.go:315-319` (`video_tools_handler.go:183,242`) | user (owner) for writes | edge only | yes (`/publish` is used by the dev smoke for a flick) |
| POST, GET, DELETE | `/v1/videos/:videoId/progress`, `/v1/videos/history`, `/continue-watching` | `handler.go:427-434` | user | edge only | no (long video) |
| GET | `/v1/hashtags/trending`, `/trending/stream`, `/search`, `/:tag/posts`, `/:tag/stream`, follow routes | `handler.go:280-310` | public / user | `hashtagSearchLimit` on search (`handler.go:286-290`); edge | yes for `/:tag/posts` and trending; rest audited under feed |
| GET | `/v1/posts/places/autocomplete` | `handler.go:297` | user | `placesLimit` (`handler.go:293`) | yes (upload form) |
| GET | `/v1/posts/genres`; `/v1/credits/*`; `/v1/creators/:creatorId/appearances` | `credits.go:37-45` | mixed | edge only | no (long video credits; Android reel form shows a Credits step, check) |
| POST, GET | `/v1/series/*`, `/v1/creators/:creatorId/series` | `handler.go:349-357` | user / public | edge only | yes (flick series; no gateway prefix found for `/v1/series`) |
| GET | `/v1/admin/reports`, PATCH `/:reportId`; `/v1/admin/comments/moderation` | `report_handler.go:26-36` | `requireModerator()` | edge only | yes (legacy report queue) |
| POST | `/v1/posts/internal/review-status`, `/internal/visibility` | `handler.go:198-199` | svc-token (reviewer-service) | none found | yes |
| GET | `/v1/posts/internal/moderation-subject/:postId` | `handler.go:200` | key | none found | yes |
| POST | `/v1/posts/internal/moderation` | `handler.go:201` | svc-token (trust-safety) | none found | yes |
| POST, GET | `/v1/posts/internal/restrictions` | `handler.go:204-205` | svc-token (trust-safety) | none found | yes |
| POST | `/v1/posts/internal/copyright/owner-match` | `copyright_owner_match_internal.go:50,60` | svc-token (trust-safety) | none found | yes |
| POST | `/v1/internal/media-access`, `/media-access/batch` | `handler.go:225-226` (`media_access_handler.go:129,82`) | key | none found | yes (the playback authority) |
| GET | `/v1/internal/media-download-allowed` | `handler.go:227` (`media_download_internal.go:37`) | key | none found | yes |
| GET | `/v1/internal/posts/:id/visibility`, `/posts/by-media/:mediaId`, `/sound-uses`, `/posts/by-live-stream/:streamId` | `handler.go:229-234` | key | none found | yes |
| GET | `/internal/channels/*`, `/internal/users/:userId/subscribed-owner-ids` | `channel_subscriptions_handler.go:194-196` | key | none found | no |
| all | admin token family: posts review queue, moderation, `reels/flagged`, `reels/:reelId/moderation`, reports, comments moderation, copyright titles and reviews | `admin_token.go:277-313`, `copyright_review.go:92-99` | admin (key exempt, `handler.go:78-84`) | none found | yes |

### 3.3 feed-service (`feed-service/internal/http/handler.go`)

Auth: internal key on every route (:78-80). Rate limit: `feedRateLimit` 120 requests per minute per user, Redis key `feed_rl:<user>`, fail-open, skipped when `X-User-Id` is empty (:842-884). Request deadline middleware (:81).

| Method | Path | Handler | Auth | Scope |
|---|---|---|---|---|
| GET | `/v1/feed/reels` | `GetReelFeed` :94 (:261); params `limit`, cursor, `following_only`, `category` | user (401 without a valid `X-User-Id`, :264) | yes |
| GET | `/v1/feed/flicks` | `GetFlickFeed` :95 (:303); alias | user | yes |
| GET | `/v1/feed/videos/:postId/related` | `GetRelatedVideos` :101; serves the reel family too | user | yes where the seed is a reel |
| POST | `/v1/feed/feedback` | `PostFeedback` :112 | user | yes (Not interested, Don't recommend) |
| GET | `/v1/feed/feedback/authors` | `GetMutedAuthors` :113 | user | yes (Hidden channels page) |
| POST | `/v1/feed/signal` | `PostSignal` :108 | user | audited under feed |
| POST, DELETE, GET | `/v1/feed/hide/:postId`, `/mute`, `/muted`, `/preference`, `/delta`, `/home` | :91-125 | user | audited under feed |
| GET | `/v1/feed/internal/debug`, `/debug` | :118-119 | gateway refuses `/internal/` | audited under feed |

### 3.4 analytics-service (`analytics-service/internal/http/handler.go`)

Auth: internal key on every route (:67-69); caller from `X-User-Id` (:158).

| Method | Path | Handler | Auth | Rate limit | Scope |
|---|---|---|---|---|---|
| POST | `/v1/analytics/events` | `IngestEvents` :73; batch max 200 (:153) | user | edge only; per-viewer daily view cap env `ANALYTICS_VIEW_CAP_PER_VIEWER_PER_DAY` | yes (views, watch time) |
| GET | `/v1/analytics/content/:contentId/views` | :74 | user | edge only | yes |
| GET | `/v1/analytics/content/:contentId`, `/creator/me`, `/dashboard/*` | :77-78, `dashboard.go:60-68` | user (owner or admin) | edge only | yes (creator insights) |
| POST | `/v1/analytics/internal/content-views` | :97 | key (gateway refuses `/internal/`) | none found | yes (feed view counts) |
| POST, GET | `/v1/analytics/internal/aggregate`, `/internal/personalization[/run]` | :84-91 | key | none found | yes |

### 3.5 trust-safety-service (`trust-safety-service/internal/http/`)

| Method | Path | Handler | Auth | Rate limit | Scope |
|---|---|---|---|---|---|
| POST | `/v1/reports` | `handler.go:71` (`FileReport`) | user | reporter quota, 429 `ErrReportRateLimited` (`internal/service/reporter_quota.go:17-56`) | yes |
| GET, PATCH | `/v1/reports`, `/v1/reports/:id` | `handler.go:72-74` | moderator (verify) | edge only | yes |
| POST, GET, PATCH | `/v1/appeals`, `/v1/appeals/:id` | `handler.go:79-81` | user / moderator | edge only | yes |
| POST, GET | `/v1/media-labels`, `/v1/media-labels/:mediaId` | `handler.go:109-110` | verify (no gateway prefix found) | n/a | yes |
| GET | `/v1/internal/keyword-filters` | `handler.go:99` | key | none found | audited under feed |
| all | admin family: reports, appeals, strikes, media-labels, `copyright/cases` create, get, place, release | `admin_token.go:138-158` | admin | none found | yes |

### 3.6 search, monetization, notification

| Service | Routes touching reels | Evidence | Scope |
|---|---|---|---|
| search-service | `GET /v1/search/posts?type=flicks`, `GET /v1/search`, `/hashtags`, `/autocomplete`, `/discover/trending`, `/discover/suggested`; internal reindex routes | `search-service/internal/http/handler.go:145-188` | yes for the reels search page and for delete or private removal from the index |
| monetization-service | `/v1/monetization/creator-fund/status`, `/apply`, `/earnings`, `/rates`, `/quality-bands`, `/statements[/:periodKey]`, `/dashboard`, `/creator-ledger` | `monetization-service/internal/http/handler.go:102,144,203-211` | yes for shorts earnings read paths; payouts are the commerce and monetization runs |
| notification-service | No reels-specific HTTP route found; consumes `social.events.v1` for likes, comments, mentions, new uploads; copyright e-mail worker | `notification-service/cmd/server/main.go:295-339`, `internal/service/copyright_email.go:332` | yes for delete and block suppression |

## 4. Upload and processing pipeline (client to playable reel)

Storage key pattern for everything of one asset: `user/<uploaderId>/<mediaId>/…` in the single media bucket (`media-service/internal/service/upload.go:167`). Original: `…/original`. MP4 renditions and thumbnail: `…/<objectName>` for `thumb_150`, `360p`, `480p`, `720p` (reels stop at 720p; `cmd/worker/main.go:743-752,803-811`). HLS: `…/hls/master.m3u8` plus rung playlists and `.ts` segments under `…/hls/` (`cmd/worker/main.go:936-961`). Storyboard variants only for videos over a minute (:790).

| # | Step | Route or job | Writes | Queue or topic | Evidence |
|---|---|---|---|---|---|
| 1 | Client picks or records a file. Web and Android classify it as short when it is at most 600 s and portrait or square; a longer or landscape file is offered as a long video, not refused | client only | — | — | web `src/lib/videoLimits.ts:13-17`, `features/upload/useUploadStudio.ts:80-90`; Android `core/media/…/publish/ReelLength.kt:26`, `feature/post/…/createhub/VideoGate.kt:60` |
| 2a | Simple upload init | `POST /v1/media/init` | Validates size (video: at most 5 GiB on this path, else 413 use-resumable; hard cap 16 GiB default), MIME allow-list (`video/mp4`, `video/webm`, `video/quicktime`), upload budget. Inserts `media_assets` with `processing_status='pending_upload'`. Returns a presigned PUT valid 15 minutes with Content-Type and Content-Length signed in | — | `service/upload.go:131-219`, `validation.go:17-38,163-175`, `processing/original_cap.go:18-24` |
| 2b | Bytes | `PUT <presigned URL>` straight to S3 or MinIO | object `user/<u>/<m>/original` | — | `service/upload.go:167-173` |
| 2c | Confirm | `POST /v1/media/confirm` | Owner check, key check, stat object, size must equal declared, stored Content-Type must match, 64-byte magic check. Failure → `rejected`. Success → `uploaded` + `upload_confirmed_at` + ETag pin (`MarkUploadConfirmed`), then for video `QueueTranscode`: status `processing` and a `media.transcode.requested` row in `media_event_outbox` in one transaction. Idempotent: `processing` or `ready` returns the asset; `failed` or `rejected` is a state error | outbox | `service/upload.go:221-400`, `store/postgres/media_outbox.go:30-89`, `media_generation.go:43` |
| 3 | Resumable alternative | `POST /v1/media/upload/resumable/init` → `POST …/:uploadId/chunk` (server-sized 5 to 64 MiB, at most 10,000 parts) → `POST …/:uploadId/complete`; `GET …/:uploadId` for resume | `resumable_uploads` (`initiated`, `uploading`, `completed`, `aborted`, `expired`), `resumable_upload_parts`; S3 multipart upload to the same `…/original` key; session TTL 24 h. Complete ends in the same confirm path as 2c | — | `service/resumable.go:16-38,100-132,306`, `database/setup.sql:81-123`. Web threshold 20 MB (`src/lib/resumableUpload.ts:20`), Android 100 MiB (`core/media/…/upload/ResumableUploader.kt:29`) |
| 4 | Outbox relay (in the server process) | ticker every 2 s drains `media_event_outbox` to Kafka | marks outbox rows published | topic `media.events` | `service/media_outbox.go:14-29`, `cmd/server/main.go:186-189` |
| 5 | Class routing (worker, short consumer) | consumer group `media-transcode-worker` on `media.events`; reads the MP4 header or the row; a long request is forwarded byte for byte to `media.events.long` (group `media-transcode-worker-long`) | — | `media.events` → `media.events.long` | `cmd/worker/transcode_classes.go:22-72`, `cmd/worker/main.go:220-252` |
| 6 | Transcode job | `processMessage` under a per-event advisory lock; inbox check (`media_transcode_inbox`), superseded-delivery check, lease heartbeat every 30 s on `media_assets` | `transcoding_jobs` rows per rendition (`queued` → `processing` → `completed` or `failed`) | — | `cmd/worker/main.go:260-366`, `transcode_lease.go:34` |
| 6a | Guards before any encode | stat + ETag pin + size cap + scratch space (3x original), conditional download, container sniff must match declared MIME, `ffprobe`, `CheckVideoLimits` (default max duration 4 h, long side 4096, 240 fps; env overrides) | refusal recorded as a failed "limits" job and listed by `GET /v1/media/:id/status` | — | `cmd/worker/main.go:634-721`, `processing/limits.go:57-66`, `cmd/worker/transcode_guards.go:66-85` |
| 6b | Encode | `TranscodeReel` when at most 600 s and portrait or square, else `TranscodeVideo`; MP4 renditions + `thumb_150`; blurhash; duration, dimensions, orientation written to `media_assets`; HLS ladder repackaged from the MP4 renditions; stale variants and stale HLS objects pruned | `media_variants`, `media_assets.hls_master_key`, URLs | — | `cmd/worker/main.go:730-983`, `processing/video.go:345-360` |
| 6c | Safety scan | frames sampled in proportion to duration (cap 60), each sent to the scanner (`MEDIA_SCANNER_BACKEND=rekognition` in prod; mock locally). Unsafe frame → `rejected`; no verdict → `manual_review`; there is no default "passed" | `media_assets.moderation_status`; labels via `recordVideoScanLabels` | — | `cmd/worker/main.go:985-1013,433-441`, `processing/limits.go:169` |
| 6d | Completion | `CompleteTranscode` in one transaction: inbox row, `processing_status` `ready` or `failed`, moderation status, generation check, and a `media.transcode.completed` outbox row | `media_transcode_inbox`, `media_event_outbox` | `media.events` (via step 4) | `cmd/worker/main.go:454-482`, `store/postgres/transcode_inbox.go:102-241` |
| 6e | Failure handling | Transient error: retried in place, 8 attempts, 2 s doubling to 2 min; then the raw message is written to `media_transcode_quarantine` and the offset committed. Permanent error (bad container, limits, budget exceeded, no variants): `failed` + `manual_review`. Offsets are committed only after the durable effect | `media_transcode_quarantine` | — | `cmd/worker/main.go:203-216,373-415,554-565`, `transcode_guards.go:53-60`, `store/postgres/media_outbox.go:156-163` |
| 7 | Stall sweeper (in the worker) | every 5 min: assets in `processing` with a heartbeat older than 10 min, or never claimed for 30 min, are re-queued with a new generation; failed after 3 sweeper attempts. Off with `MEDIA_TRANSCODE_STALL_SWEEP=off` | `media_assets`, `media_event_outbox`, inbox skip rows | `media.events` | `cmd/worker/transcode_lease.go:42-46,154-187`, `store/postgres/transcode_lease.go:69-76,224` |
| 8 | Manual recovery | `POST /v1/media/internal/:mediaId/reprocess` (optional rotation) | new transcode request | `media.events` | `http/reprocess_handler.go:32`, `service/reprocess.go:43` |
| 9 | Post creation | `POST /v1/posts` with `content_type=flick` and the media id (Android: WorkManager queue `reel-publish-queue`; web: after `all_ready` from `/renditions`, or via `POST /v1/reels/drafts/:id/publish`) | `posts` (`review_status` `pending` while the video is not ready and passed; `visibility`; `publish_at` when scheduled), `post_media`, `video_metadata` (`upload_status` `pending`, `processing`, `ready`, `failed`), `post_outbox_events`; media claimed through `/v1/media/internal/claims` | `social.events.v1` via the post outbox worker | `post-service/internal/http/create_post_handler.go:326-335`, `cmd/server/main.go:624`, `database/migrations/008_video_metadata.sql:27`, `024_review_status_needs_changes.sql:6` |
| 10 | Publish gate | post-service consumer group `post-service-media-transcode` on `media.events` (DLQ `media.events.dlq`): `finalizeReviewGate` flips a `pending` post to `approved` (ready + passed) or `rejected` (failed, or scan rejected); `manual_review` leaves it `pending`. Writes `moderation_reviews`, deletes Redis `post:body:<id>`. Then fills `video_metadata.playback_url` (HLS first), thumbnail, measured duration and dimensions, and applies the Shorts cap: a flick measured over 600 s becomes `long_video` and `PostContentTypeChanged` is published | `posts.review_status`, `posts.content_type`, `video_metadata`, `moderation_reviews` | `media.events` in; `social.events.v1` out | `post-service/internal/consumers/media.go:59-61,86-241,261-269,353-398` |
| 11 | Scheduled publish | `postschedule` worker (tick `POST_SCHEDULE_INTERVAL`, default 30 s) publishes posts whose `publish_at` passed; reel draft worker every 60 s (`PublishScheduledDrafts`) with attempts and backoff (migration 079) | `posts`, `reel_drafts` | `social.events.v1` | `post-service/cmd/server/main.go:637-642,720-737` |
| 12 | Fan-out to the reels timeline | feed-service consumes `social.events.v1` and writes Scylla `social_feed.home_timeline_by_user` rows with `content_type`; the reels feed reads only `flick` and `reel` rows | Scylla | `social.events.v1` | `feed-service/internal/events/consumer.go`, `feed-service/internal/service/feed.go:741` |
| 13 | Playback | Client asks `GET /v1/media/:id/url` or `/hls/master.m3u8` or `/serve`; the delivery gate asks post-service `POST /v1/internal/media-access` as the viewer; protected URLs are CloudFront-signed for 5 minutes, HLS segment URLs for 2x duration + 15 min (cap 12 h) | — | — | `media-service/internal/service/playback.go:123,247`, `internal/delivery/authz.go:195,527-671`, `signer.go:72`, `sign_cache.go:52-91` |

### Every status and what moves it

| Object | Column | Values | Moved by |
|---|---|---|---|
| Media asset | `media_assets.processing_status` (CHECK, migration 011) | `pending_upload` → `uploaded` → `processing` → `ready` \| `failed`; `rejected` from confirm | init; confirm; `QueueTranscode`; `CompleteTranscode`; stall sweeper (back to a fresh request, or `failed`); reprocess (back to `processing`); orphan GC deletes `pending_upload` rows older than 24 h (`service/orphan_gc.go:38-43`, tick 15 min) |
| Media asset | `media_assets.moderation_status` | `pending`, `passed`, `rejected`, plus `manual_review` written by the worker. Migration 008 CHECK lists only `pending`, `passed`, `rejected` (`database/migrations/008_media_moderation.sql:6`): confirm whether a later migration widens it, otherwise the `manual_review` write fails (open question 3) | worker scan; voice safety |
| Media asset | `media_assets.access_scope` | NULL, `dating_photo`, `anonymous`, `dating_clip`, `doorstep_photo` | internal routes |
| Rendition job | `transcoding_jobs.status` | `queued`, `processing`, `completed`, `failed` | worker |
| `media_renditions.status` | — | `pending`, `processing`, `retrying`, `failed` (index, migration 004) | whether anything still writes this table was not determined |
| Resumable session | `resumable_uploads.status` | `initiated`, `uploading`, `completed`, `aborted`, `expired` | chunk and complete routes; blob reclaim worker expires sessions and aborts the multipart upload (`service/deletion.go:156-226`, tick 10 min) |
| Alternate audio | `media_audio_tracks.status` | `pending`, `processing`, `ready`, `failed` | audio track worker (`cmd/server/main.go:218`) |
| Fingerprint | `media_fingerprint_jobs.status` | `queued`, `claimed`, `done`, `superseded`, `failed`, `skipped` | worker fingerprint loop |
| Post | `posts.review_status` | `approved`, `flagged`, `rejected`, `pending`, `needs_changes` | create; transcode consumer; reviewer and trust-safety internal routes; admin routes; resubmit |
| Post | `posts.visibility` | `public`, `followers`, `friends`, `private`, `unlisted`, `trusted`, `close_friends`, `staged` (migration 066) | create, edit, internal visibility route |
| Post | `posts.deleted_at`, `posts.publish_at` | soft delete with restore window (`POST_PURGE_AFTER`, default 720 h); scheduled | delete, restore, purge worker, schedule worker |
| Video row | `video_metadata.upload_status` | `pending`, `processing`, `ready`, `failed` | create; transcode consumer |
| Reel draft | `reel_drafts.status` | `draft`, `processing`, `publishing_pending`, `published`, `rejected`, `deleted`, `blocked` (migration 055) | draft routes; draft worker |

## 5. Data, events, workers, caches, storage, flags

### 5.1 Tables

**PostgreSQL, media-service** (`media-service/database/setup.sql`, migrations `001` to `032`; two files are numbered 012 and two 013): `media_assets`, `media_variants`, `transcoding_jobs`, `media_renditions`, `resumable_uploads`, `resumable_upload_parts`, `media_event_outbox`, `media_transcode_inbox`, `media_transcode_quarantine`, `media_blob_reclaim`, `media_claims`, `media_subtitles`, `media_caption_jobs`, `media_audio_tracks`, `audio_tracks`, `audio_library`, `post_audio_refs`, `audio_trending_sounds`, `audio_trending_runs`, `media_clips`, `media_fingerprint_jobs`, `copyright_fingerprints`, `copyright_anchor_postings`, `copyright_band_hot`, `copyright_pairs`, `copyright_pair_outbox`, `copyright_match_observations`, `owner_media_slots`, `owner_media_resolved`, `media_chat_attachment_reservations`. Schema is applied by the server at boot (`cmd/server/main.go:85`).

**PostgreSQL, post-service** (83 migration files, latest `085_outbox_seq.sql`), reels-relevant: `posts`, `post_media`, `video_metadata`, `reel_drafts`, `reel_hashtags`, `reel_crosspost`, `crosspost_links`, `slug_history`, `flick_series`, `flick_series_items`, `flick_series_followers`, `audio_tracks`, `post_offline_copies`, `post_private_shares`, `post_engagement_counts`, `reactions`, `comments`, `comment_reactions`, `comment_idempotency`, `saved_items`, `saved_collections`, `post_reposts`, `post_mentions`, `post_hidden_authors`, `post_create_idempotency`, `idempotency_keys`, `post_outbox_events`, `engagement_event_log`, `moderation_reviews`, `post_moderation_decisions`, `post_review_audit`, `post_admin_audit`, `post_edit_audit`, `post_restrictions`, `post_restriction_events`, `post_publish_blocks`, `content_reports`, `comment_report_reporters`, `media_rights_checks`, `copyright_review_titles`, `copyright_title_reviews`, `copyright_title_review_events`, `copyright_owner_tracks`, `post_purge_media`, `post_purge_failures`, `ops_job_runs`, `action_links`.

**PostgreSQL, feed-service:** as in the feed map (`feed_feedback`, `feed_author_feedback`, `hidden_authors`, `post_impressions`, `viewer_media_prefs`, `event_dedup`, …).

**PostgreSQL, analytics-service** (latest `016_daily_rollup_finality_view.sql`): `analytics.events_raw` (partitioned), `analytics.ingest_receipts`, `analytics.playback_sessions`, `analytics.content_hourly_agg`, `analytics.content_daily_summary`, `analytics.content_ownership`, `analytics.content_type_exclusions`, `analytics.rollup_progress`, `analytics.aggregation_settings`.

**PostgreSQL, trust-safety-service:** `trust.reports`, `trust.content_appeals`, `trust.media_labels`, `trust.copyright_cases`, `trust.copyright_blocks`, `trust.user_strikes`, `trust.user_trust_state`, `trust.restriction_commands`, `trust.enforcement_outbox`, `trust.admin_audit`, `trust.grievances`.

**PostgreSQL, monetization-service:** `creator_fund_eligibility`, `creator_fund_earnings`, `creator_fund_budgets`, `creator_fund_carry`, `creator_fund_period_settlements`, `creator_fund_revision_adjustments`, `creator_ledger`.

**ScyllaDB** (`Architecture/docker/scylla/schema.cql`, `schema.qa.cql`, `schema.prod.cql`; prod RF 3): `social_analytics.watch_sessions` (TTL 90 d), `viewer_history`, `reel_views` (no TTL), `reel_view_counts` (counters), `content_engagement`; `social_feed.home_timeline_by_user`, `author_timeline_by_author`, `timeline_index_by_post[_v2]`, `user_post_interactions`; `social_engagement.*` (likes, comments, shares, bookmarks, counters).

### 5.2 Kafka topics and consumer groups

| Topic | Producer | Consumer groups | Dead letter |
|---|---|---|---|
| `media.events` | media-service outbox relay (`cmd/server/main.go:186`) | `media-transcode-worker` (worker, short class); `post-service-media-transcode` (`post-service/internal/consumers/media.go:59`) | `media.events.dlq` for the post-service consumer; the worker uses the `media_transcode_quarantine` table |
| `media.events.long` | worker short consumer (forward) | `media-transcode-worker-long` | quarantine table only. Topic is absent from the MSK topic list (`infra/terraform/modules/msk/topics-job/variables.tf:29-59`) while auto-create is off (`modules/msk/main.tf:147`) |
| `media.copyright.pairs` | media-service pair relay, switch `COPYRIGHT_PAIR_RELAY` default off (`cmd/server/main.go:192-197`) | none found in any service | none |
| `social.events.v1` | post-service outbox and engagement producers (`post-service/cmd/server/main.go:326-330`), trust-safety outbox, graph, live | feed-service; post-service groups `post-service-graph-group`, `reel-analytics-consumer`, `post-service-entitlement-changed`, `post-service-live-vod`, `post-service-story-moderation-applier`, comment-report and action-link groups, Scylla like, PG counter and WS broadcaster consumers; analytics `analytics-engagement`, `analytics-creator`, `analytics-content-ownership`, `analytics-follower-deltas`; trust-safety `trust-safety-copyright-review`, `trust-safety-story-moderation`, `trust-safety-connection-filter`; search; notification; monetization payments consumer | `social.events.v1.dlq`; `search.events.v1.dlq` for search |
| `identity.events.v1` | auth-service | `media-service-account-lifecycle` (`media-service/cmd/server/main.go:231`), post, feed, search, notification, trust-safety lifecycle groups | — |
| `platform.purge-acks.v1` | each service's purge handler | auth-service | — |

### 5.3 Background workers, sweepers and commands

| Where | Worker | Interval | Evidence |
|---|---|---|---|
| media server | outbox relay | 2 s | `service/media_outbox.go:16` |
| media server | orphan GC (`pending_upload` older than 24 h) | 15 min | `service/orphan_gc.go:38-43` |
| media server | blob reclaim + resumable expiry | 10 min | `service/deletion.go:156-165` |
| media server | caption worker, audio track (mux and dub) worker, copyright pair relay, trending sounds ranking (15 min), account lifecycle consumer | — | `cmd/server/main.go:197-235,292-307` |
| media worker | transcode consumers (one loop per class), stall sweeper (5 min), readiness gauges (30 s), fingerprint loop, stale scratch sweep at start | — | `cmd/worker/main.go:174-252` |
| media | `cmd/fingerprint-backfill` | manual | `media-service/cmd/fingerprint-backfill/main.go` |
| post-service | outbox worker, cross-post worker, post purge worker, schedule worker, reel draft worker (60 s), engagement reconciler (5 min), counter flush workers (10 s), event-log and orphan draft media cleanup (1 h), trending publisher, engagement and restriction reconcilers | — | `post-service/cmd/server/main.go:410-497,569-737` |
| post-service | `cmd/backfill-likes`, `cmd/media-claims` | manual | `post-service/cmd/` |
| feed-service | fan-out consumers; `cmd/backfill`, `cmd/timeline-maintenance` (Job in `deploy/jobs/timeline-maintenance`) | — | feed map |
| analytics-service | hourly aggregation, daily rollup, finalizer, personalization warmer | — | `analytics-service/internal/aggregation/*.go`, `personalization/warmer.go:169` |
| monetization-service | creator fund worker (1 h), ops workers | — | `monetization-service/internal/workers/creator_fund.go:110` |
| Deploy | No CronJob exists in the chart or in `deploy/`; every sweeper above runs inside a long-lived process | — | section 9 |

### 5.4 Caches and Redis keys

| Key | TTL | Owner | Evidence |
|---|---|---|---|
| `upload:rate:<user>:hour`, `:day` | 1 h, 24 h | media-service | `service/ratelimit.go:26-27` |
| signed-URL reuse cache (in process, per pod) | reuse window 2 min of a 5 min signature | media-service | `delivery/sign_cache.go:52-58,155-200` |
| redirect `Cache-Control: private, max-age=<redirectCacheTTL>` on `/serve`; playlists `private, no-store` | 60 s in the test fixture | media-service | `http/handler.go:665,725` |
| `post:rate:<user>:hour`, `:day` | 1 h, 24 h | post-service | `service/ratelimit.go:58-59` |
| `post:body:<postId>`, `post:<postId>` | not determined | post-service | `consumers/media.go:345,394` |
| sharded engagement counters and dirty sets | flushed every 10 s | post-service | `cmd/server/main.go:405-460` |
| `feed:hydrate:<viewer>:<post>`, `feed:hydrate-idx:<viewer>` | 5 min | feed-service (also deleted by post-service, `post-service/internal/service/post.go:551`) | `feed-service/internal/service/hydration.go:25-36` |
| `feed:following:<viewer>` | 5 min | feed-service | `discovery.go:54,83` |
| discovery pool, `feed:discovery:seen:v2:<viewer>` | 2 min, 48 h | feed-service | `discovery.go:49-51,90` |
| view-count cache | 60 s | feed-service | `view_counts.go:36` |
| block set memo | 1 min | feed-service | `requestmemo.go:35` |
| `feed_rl:<user>` | 1 min window | feed-service | `http/handler.go:858` |
| gateway rate-limit keys | 1 s window | api-gateway | `ratelimit_redis.go:40-54` |

### 5.5 Storage and CDN

| Item | Fact | Evidence |
|---|---|---|
| Bucket | one media bucket `atpost-<env>-media-<random>`; private (all four public-access blocks), versioned, SSE-KMS | `infra/terraform/modules/media/main.tf:31-84` |
| Lifecycle | noncurrent versions to IA at 30 d and expired at 90 d; abort incomplete multipart at 7 d; a `transcodes/` prefix rule that does not match the `user/<u>/<m>/` layout the code writes | `modules/media/main.tf:135-178` |
| CORS | GET, HEAD, PUT, POST from env origins | `modules/media/main.tf:183-193` |
| CDN | CloudFront, OAC, default behaviour requires signed URLs (key group); `public/*` unsigned; no signed cookies, WAF, access logs or origin shield | `modules/media/main.tf:207-332` |
| Prod wiring | `MEDIA_CDN_BASE_URL=https://media.cleestudio.com` in values while `media_custom_domain_enabled` defaults to false | `deploy/services/media-service/values-prod.yaml:102`, `infra/terraform/envs/prod/variables.tf:35-43` |
| Local | MinIO bucket `media`, created by code with no policy; dev box uses S3 without a CDN | `media-service/internal/store/blob/store.go:153-158`, `infra/devbox/devbox.cfn.yaml:71-107` |
| Web dev proxy | Next `/v1` proxy follows media redirects to storage itself when `MEDIA_STORAGE_PUBLIC_ORIGIN` and `MEDIA_STORAGE_INTERNAL_ORIGIN` are set (dev only) | web `src/app/api/proxy/_lib/storageRoute.ts:53-95` |

### 5.6 Feature flags and env switches

| Name | Default | Read at | Effect |
|---|---|---|---|
| `MEDIA_TRANSCODE_CLASSES` | both | `media-service/cmd/worker/main.go:108` | which queues a worker consumes |
| `MEDIA_TRANSCODE_MAX_ATTEMPTS` | 8 | `cmd/worker/transcode_guards.go:75` | per-message attempts |
| `MEDIA_TRANSCODE_STALL_SWEEP` | on | `cmd/worker/transcode_lease.go:154` | stall sweeper |
| `MEDIA_WORKER_MAX_ORIGINAL_BYTES` | 16 GiB (absent in prod values; 6 GiB in qa and staging) | `processing/original_cap.go:24` | size cap at init and in the worker |
| `MEDIA_MAX_VIDEO_DURATION_SEC`, `MEDIA_MIN_VIDEO_SHORT_SIDE`, `MEDIA_MAX_VIDEO_LONG_SIDE`, `MEDIA_MAX_VIDEO_PIXELS`, `MEDIA_MAX_VIDEO_FPS` | 4 h, …, 4096, 4096x2304, 240 | `cmd/worker/transcode_guards.go:68-72` | probe limits |
| `MEDIA_SCANNER_ENABLED`, `MEDIA_SCANNER_BACKEND`, `MEDIA_SCANNER_ALLOW_STUB`, `MEDIA_SCANNER_MIN_CONFIDENCE` | prod: true, rekognition, false | `cmd/worker/main.go:1086-1123` | safety scan |
| `MEDIA_DUBBING_BACKEND` | unset (generate answers 503) | `cmd/server/main.go:209` | AI dubbing |
| `MEDIA_STORAGE_BACKEND`, `S3_BUCKET`, `MINIO_*` | — | `cmd/worker/main.go:68-72,144` | storage backend |
| `MEDIA_CDN_BASE_URL`, `MEDIA_CLOUDFRONT_KEY_PAIR_ID`, `MEDIA_CLOUDFRONT_PRIVATE_KEY`, `POST_SERVICE_URL` | required in production, else boot refuses | `cmd/server/main.go:157-175` | delivery gate |
| `COPYRIGHT_FINGERPRINT_ENABLED`, `COPYRIGHT_INDEX_LOOKUP_ENABLED`, `COPYRIGHT_PAIR_RELAY`, `COPYRIGHT_FINGERPRINT_*` | prod values: fingerprint and matching true (shadow); relay off | `cmd/worker/fingerprint_runner.go`, `cmd/server/main.go:192-197` | copyright match |
| `INTERNAL_SERVICE_KEY`, `DEPLOY_ENV` | key required when `DEPLOY_ENV=production` | `cmd/server/main.go:279-286` | internal routes exist only with a key |
| `REVIEW_ALL_VIDEOS` | false in prod values | post-service; `deploy/services/post-service/values-prod.yaml:131` | hold every video for review |
| `COPYRIGHT_TITLE_HOLD_ENABLED`, `COPYRIGHT_AUTO_BLOCK_ENABLED` | true in prod values | `values-prod.yaml:84,86` | title holds |
| `MAX_POSTS_PER_HOUR`, `MAX_POSTS_PER_DAY` | 20, 50 | `post-service/internal/service/ratelimit.go:27-29` | post budget |
| `POST_PURGE_AFTER`, `POST_PURGE_INTERVAL`, `POST_SCHEDULE_INTERVAL` | 720 h, —, 30 s | `post-service/cmd/server/main.go:627-642` | delete and schedule |
| `FEED_DISCOVERY_SHARE`, `FEED_HF4_FALLBACK`, `FEED_INDEX_LEGACY_READ`, `FEED_REQUEST_DEADLINE` | 0.2, false, true | feed-service; `deploy/services/feed-service/values-prod.yaml:77-81` | feed behaviour |
| `ANALYTICS_VIEW_SOURCE`, `ANALYTICS_VIEW_CAP_PER_VIEWER_PER_DAY` | `sessions` in prod values | analytics-service; `values-prod.yaml:56` | which rule counts a view |
| `RATE_LIMIT_IP_PER_SEC`, `RATE_LIMIT_USER_PER_SEC`, `GATEWAY_DISABLED_PREFIXES` | 100, 60 | `api-gateway/cmd/server/main.go:256-257,427` | edge |
| `MONETIZATION_WRITES_ENABLED`, payouts, memberships | false in prod values | `deploy/services/monetization-service/values-prod.yaml:82-91` | earnings writes off |
| Android `BANUBA_LICENSE_TOKEN` | blank without `.secrets/banuba.token` | `build-logic/convention/…/BanubaLicenseConventionPlugin.kt:34,40`; `feature/post/…/banuba/BanubaGate.kt` | falls back to the Media3 studio and the system camera |
| Android `AppModule.REELS` | on | `core/profile/…/data/AppModule.kt:23` | shows the Reels tab |
| Web | no reels flag; `GET /v1/features` has no reels key | `src/lib/productFeatures.ts:31-44` | reels cannot be switched off on the web by flag |

### 5.7 Founder rules located in code

| Rule | Where it is enforced | Where it is only client-side or absent |
|---|---|---|
| Shorts at most 10 minutes; longer becomes a long video | `shared/postclassify/classify.go:22,45-57`; post-service `post_content_types.go:20,27-29` and the consumer's `reclassifyDecision` (`consumers/media.go:261-264`); media worker `processing/video.go:350-360`; error code `VIDEO_TOO_LONG` exists in post-service | web `src/lib/videoLimits.ts:13`, Android `ReelLength.kt:26` |
| Camera max 10 minutes per clip | — (no server rule; a clip is just a file) | Android Banuba camera only: `ReelLength.kt:36`, `banuba/BanubaKoinModule.kt:61-69`. The system-camera fallback sets no limit (`createhub/ReelSurface.kt:248-254`) |
| No 500 MB cap on video | video simple upload 5 GiB, overall 16 GiB (`media-service/internal/service/validation.go:25`, `processing/original_cap.go:18`). The 500 MB constant still caps non-video uploads (`validation.go:17,173`) | web `features/upload/fileRules.ts:11-25` |
| Shorts studio has 3 steps | — | web `src/features/upload/tokens.ts:21-22`. Android has no step constant; flow is source → edit → export → details (`ReelSurface.kt:124-135`) |
| "Keep a copy" is an in-app offline copy; viewers never get a file; download is owner-only | `GET /v1/media/:id/download` uploader only (`media-service/internal/http/download_handler.go:43`, `service/download.go:57-88`); offline grants `post-service/internal/http/offline_copies.go:98-173` | Android stores copies in `noBackupFilesDir/offline_copies` with no at-rest encryption (`core/media/…/offline/OfflineStorage.kt:10-44`, `di/OfflineMediaModule.kt:74`); web never shows the offline row on the reels stage (`features/video-shell/moreRows.ts:126-128`) |
| Reels never appear in the home feed | `feed-service/internal/service/feed.go:497`, `longvideo.go:259-292` (`dropShortForm`), also `delta.go:157`, `discovery.go:225` | web still sends `publishToFeed=true` for reels (`features/upload/types.ts:185`, `useUploadStudio.ts:502,619`) |
| Public videos play signed out | media read routes carry no auth middleware and the gate forwards the anonymous viewer to post-service (`media-service/internal/http/handler.go:152-175`, `delivery/authz.go:131-136`, `public_poster.go:50-61`, `cmd/server/main.go:167-172`); post-service `media_access_handler.go:30-40` | web: `/reels/<uuid>` is public and redirects signed-out viewers to `/post/<id>` (`src/app/reels/[id]/page.tsx:40-45`, `src/middleware.ts:158,170`). `/v1/feed/reels` itself needs a user. Android has no signed-out mode |

## 6. Clients

### 6.1 Android (no dedicated reels module; `settings.gradle.kts:50-296`)

| Module | Reels pieces |
|---|---|
| `:feature:feed` | `feature/feed/…/feature/feed/ui/reels/ReelsScreen.kt` (1954 lines), `ReelsViewModel.kt`, `ReelRail.kt`, `ReelsFeedTabs.kt`, `explore/ReelsExploreScreen.kt`, `sound/SoundPageScreen.kt`, `navigation/FeedNavigation.kt` |
| `:feature:post` | `createhub/ReelSurface.kt`, `ReelPublishViewModel.kt`, `ReelPublishPipeline.kt`, `ReelPublishWorker.kt`, `ReelMediaUploads.kt`, `VideoGate.kt`, `studio/ReelStudioScreen.kt`, `ReelExporter.kt`, `banuba/*`, `capture/CaptureScreen.kt` (photo only), `data/PostApi.kt` |
| `:core:media` | `PlayerPool.kt`, `Playback.kt`, `HlsRefresh.kt`, `DataSaver.kt`, `publish/ReelLength.kt`, `upload/MediaUploadApi.kt`, `upload/ResumableUploader.kt`, `upload/PresignedUploader.kt`, `sound/ReelSoundPlayer.kt`, `offline/*` |
| `:core:feed` | `data/FeedApi.kt`, `VideoFeedApi.kt`, `Discovery.kt`, `SoundsApi.kt`, `AudioTracksApi.kt`, `PostEditApi.kt`, `offline/*`, `ui/comments/CommentsSheet.kt`, `ui/more/*`, `ui/share/*`, `ui/edit/PostEditSheet.kt` |
| `:core:engagement` | `data/EngagementApi.kt`, `CommentActionsApi.kt`, `ReportApi.kt`, `PostLifecycleApi.kt` |
| `:core:analytics` | `VideoWatchTracker.kt`, `data/AnalyticsApi.kt`, `AnalyticsUploadWorker.kt` |
| `:core:ui`, `:core:datastore`, `:core:model`, `:feature:tube` (hub, offline page), `:feature:chat` (`SharedReelsViewer.kt`), `:app` (`navigation/UsNavHost.kt:1333-1362`) | shared pieces |

Player facts: one active player (`PlayerPool.playOnly`, `PlayerPool.kt:141-149`), pool of 4 (`:183`), preload of the neighbours (`ReelsScreen.kt:1126-1148`), 256 MiB stream cache (`PlayerPool.kt:256`), page size 15 (`core/feed/…/data/FeedPager.kt:56`), heartbeat every 5 s (`VideoWatchTracker.kt:562`).

Routes called (client declarations): `v1/feed/{surface}`, `v1/feed/feedback`, `v1/feed/feedback/authors`, `v1/posts` (create with `Idempotency-Key`), `v1/posts/{id}` (GET, PATCH, DELETE), `/restore`, `/pin`, `/schedule`, `v1/posts/me/deleted|scheduled`, `v1/posts/batch`, `/by-author/{id}`, `/bookmarks`, `/categories`, `/trending`, `v1/reels/liked`, `v1/reels/drafts` (GET only), `v1/uploads/{kind}`, `v1/media/init`, `/confirm`, `/{id}/status`, `/{id}/alt-text`, DELETE `/{id}`, `/upload/resumable/*`, `/{id}/url`, `/{id}/hls/master.m3u8`, reactions, bookmark, saved collections, repost, share, comments and comment actions, `v1/reports`, `v1/graph/follow|unfollow|block|mute`, channel subscribe, `v1/posts/{id}/sound`, `v1/posts/by-sound/{id}`, `v1/audio/{id}`, `v1/audio/trending`, `v1/audio-library`, `/use`, `v1/media/{id}/audio-tracks[...]`, `v1/posts/{id}/offline`, `v1/posts/offline[/check]`, `v1/analytics/events`. File and line for each: `core/feed/…/data/FeedApi.kt:52-162`, `VideoFeedApi.kt:34-141`, `core/media/…/upload/MediaUploadApi.kt:37-317`, `core/engagement/…/data/EngagementApi.kt:58-133`, `CommentActionsApi.kt:38-104`, `core/feed/…/data/SoundsApi.kt:35-77`, `AudioTracksApi.kt:37-57`, `core/feed/…/offline/OfflineApi.kt:35-54`, `core/analytics/…/data/AnalyticsApi.kt:28`.

### 6.2 Web (`C:\workspace\wt\pb-echo`)

| Route | File | Gate |
|---|---|---|
| `/reels` | `src/app/reels/page.tsx:18` → `features/reels/components/ReelsScreen.tsx` | signed in |
| `/reels/<uuid>` | `src/app/reels/[id]/page.tsx:29-46` (redirect) | public |
| `/post/<id>`, `/post/<id>/embed` | `src/app/post/[postId]/page.tsx`, `PostDetailClient.tsx` | public |
| `/reels/create`, `/posttube/upload?type=short` | `src/app/reels/create/page.tsx:12`, `src/app/posttube/upload/page.tsx:8-28` → `features/upload/UploadStudio.tsx`, `useUploadStudio.ts` | signed in |
| `/reels/liked`, `/reels/search`, `/reels/sounds/trending`, `/reels/live[...]`, `/discover`, `/saved`, `/settings/hidden-channels` | `src/app/reels/*`, `src/app/discover/page.tsx` | signed in |
| `/reels/sound/<id>` | `src/app/reels/sound/[id]/page.tsx:14-24` | public |
| `/posttube/hub/library?tab=shorts|drafts`, `/posttube/hub/sounds`, `/posttube/uploads`, `/posttube/offline` | `features/posttube/hub/components/LibraryPage.tsx:55-121` | signed in |
| `/v1/*` proxy | `next.config.ts:13-19` → `src/app/api/proxy/[...path]/route.ts` (header allow-list :33-62, body buffered whole :99-102) | forwards the browser's bearer and cookies; media auth rides the httpOnly `access_token` cookie scoped to path `/v1/media` (`src/app/api/auth/_lib/session.ts:53-54,119-128`) |

Player: `features/reels/components/ReelVideo.tsx` (hls.js first :336, native HLS fallback :338-340, `controlsList="nodownload"` :647). Only the active reel is mounted (`ReelsScreen.tsx:818-822`); the next reel is not preloaded. `components/ReelPlayer.tsx` is an older player nothing imports. Telemetry: `playback/telemetry.ts:14-121`. Middleware: `src/middleware.ts:84-131,152-170`.

### 6.3 What each client can do

| Capability | Android | Web |
|---|---|---|
| Capture | yes (Banuba when licensed, else system camera intent) | no (file input only, `features/upload/steps/VideoStep.tsx:36`) |
| Upload (simple and resumable) | yes, through WorkManager | yes |
| Save and resume a reel draft | no (read-only list in the hub) | yes |
| Schedule | yes | yes |
| Edit after publish | yes (owner, More sheet) | yes, from Creator Hub only |
| Delete and restore | yes | yes |
| Play | yes, signed in only | yes; signed out through `/post/<id>` |
| Like, comment, share, save, follow, use sound, not interested, block | yes | yes |
| Report | yes | yes |
| Creator controls (audience, comments off, allow download, remix) | yes (`ReelSurface.kt:617,1001-1026`) | yes (`steps/PublishStep.tsx:91-101`) |
| Offline copy | yes | plumbing exists; no row on the reels stage |
| Alternate audio track and dubbing | yes | yes |

## 7. Tests and CI

| Layer | Count and folder | CI job that runs it |
|---|---|---|
| Go unit | `_test.go` files: media-service 179, post-service 317, feed-service 111, trust-safety 56, analytics 44, monetization 88, search 66, notification 108, api-gateway 38 | `.github/workflows/go-ci.yml` job `workspace` (`scripts/verify/go.sh --full`, :63-75); `lint.yml`; `module-05-messaging-safety.yml` runs media-service unit tests (:51-52) |
| Go integration (`//go:build integration`) | media 26 files, post 72, trust-safety 13, analytics 21, monetization 29, search 10, notification 2, feed 0, gateway 0 | `integration-all.yml` from `scripts/ci/integration-services.yaml` (media `media_it_test` :60, post `post_it_test` :41). Postgres and optional Redis only: tests needing Kafka, MinIO, OpenSearch or Scylla are skipped as "blocked" (`integration-all.yml:81`), except Scylla for post-service (:147-161). feed-service and api-gateway are not in the manifest |
| Go integration, focused | media reference integrity, block symmetry | `integration-postgres.yml:103-114` |
| Search and feed safety | OpenSearch + Redpanda | `integration-opensearch.yml:100-188` |
| End to end (Go, compose stack) | `Architecture/tools/integration/media_e2e_test.go`: init, PUT and confirm of a PNG (`:110,149`). No video, no worker, no HLS, no reel. No file in `tools/integration` exercises a reel upload or playback | `e2e.yml` matrix entry `media` (:57-60) |
| Android unit | `src/test` files: `feature/feed` 19 (6 reels), `feature/post` 43 (about 16 reels), `core/media` 30, `core/feed` 70, `core/engagement` 17, `core/analytics` 5 | `android-native.yml` job `build`: `scripts/verify/android.sh --full` (:111), `./gradlew testDebugUnitTest :app:testDevDebugUnitTest` (:134), lint (:138), release assemble (:144) |
| Android instrumented | none: no `src/androidTest` folder exists (`android-native.yml:6-8`) | none |
| Web unit | 371 `bun:test` files; about 104 touch reels, upload or the player (`src/features/reels/__tests__` 22, `features/upload/__tests__` 12, `features/video-shell/__tests__` 6, `features/offline/__tests__` 5, `lib/__tests__/resumableUpload.test.ts`, `app/api/proxy/__tests__`) | **none.** `pb-echo` has no `.github/workflows`; `build-web.yml` in this repo only builds the Docker image on manual dispatch (:48-49,145-152). `scripts/verify.sh` is run by hand |
| Web end to end | none (no Playwright config) | none |
| Dev smoke | `scripts/smoke/dev-smoke.mjs`: uploads a 2 s portrait MP4, waits for `ready` + `passed`, creates a private flick, publishes, fetches the HLS master (:595-701) | **none.** Run by hand after a dev deploy |
| Load tests | none for media, feed or reels (k6 scripts exist only for dating and rider) | none |
| Security scans | gitleaks, govulncheck, `trivy fs` (`security.yml:15-85`; the last two skip on pull requests). No container image scan | `security.yml` |

## 8. Deployment artefacts

| Item | Fact | Evidence |
|---|---|---|
| Values files | every service has `values-prod.yaml`, `values-qa.yaml`, `values-staging.yaml`, `values-azure-prod.yaml`, `values-azure-staging.yaml`; no `values-dev.yaml` | `deploy/services/<service>/` |
| Chart | `charts/atpost-service`: deployment, worker-deployment, hpa, pdb, networkpolicy (off by default), ingress, prometheusrule, servicemonitor, externalsecret, migration-job. No CronJob template | `charts/atpost-service/templates/` |
| media-service server | prod 2 replicas, HPA 2 to 6; probes `/livez`, `/readyz` (chart defaults) | `deploy/services/media-service/values-prod.yaml:18-31`, `charts/atpost-service/values.yaml:193-203` |
| media worker | `worker:` block in the media-service values; prod 2 replicas, no HPA, no PDB, no Service, no readiness probe, `strategy: Recreate`, scratch `emptyDir` 50 Gi, termination grace 300 s, classes `short,long` in one deployment (the chart renders one worker only) | `values-prod.yaml:153-176`, `charts/atpost-service/templates/worker-deployment.yaml:55-126` |
| Worker observability | worker exposes `:9091/metrics` (`media-service/cmd/worker/main.go:52-59`) but has no Service or ServiceMonitor; default alerts select the server container only; no transcode, lag or quarantine alert exists in `deploy/` | `charts/atpost-service/templates/prometheusrule.yaml:42-65` |
| Images | `media-service/Dockerfile` and `Dockerfile.worker`: `alpine:3.19`, `apk add ffmpeg` unpinned, no `USER` line (chart forces non-root) | `Dockerfile:22-26`, `Dockerfile.worker:25-26` |
| Build and promote | `build-push.yml` builds arm64 images including `media-worker` (:186-247) and bumps staging values only (:274); `promote.yml` has an environment gate for qa and none for prod (:9,81) | `.github/workflows/` |
| Terraform | media bucket and CloudFront (`infra/terraform/modules/media/main.tf`), MSK and topics job (`modules/msk`), Aurora, ElastiCache, Scylla operator, observability alert routing (`modules/observability/alerting.tf`) | `infra/terraform/envs/prod/main.tf:105-348` |
| Staging | `S3_BUCKET: atpost-staging-media` literal while the Terraform bucket name carries a random suffix | `deploy/services/media-service/values-staging.yaml:65`, `modules/media/main.tf:32` |
| Azure | media-service Azure values carry no storage, database, Kafka or Redis settings; Front Door has no media origin | `values-azure-prod.yaml:23-44`, `infra/azure/modules/frontdoor/main.tf:28-66` |
| Jobs | `deploy/jobs/scylla-schema`, `deploy/jobs/timeline-maintenance` (Jobs, not CronJobs) | `deploy/jobs/` |
| Local | `Architecture/docker/docker-compose.yml`: `media-service` (:957-1060), `media-worker` (:1062-1106), optional `media-worker-long` profile (:1112-1117); topics from `init-topics.sh:44-65` | `Architecture/docker/` |

## 9. Dependencies of the two hot paths

### 9.1 Playing a reel

| # | Call | Evidence |
|---|---|---|
| 1 | Client → gateway → media-service `GET /v1/media/:id/url`, `/hls/:playlist` or `/serve[/:variant]`; viewer from `X-User-Id` | `media-service/internal/http/handler.go:162-175`, `delivery_viewer.go:26-32` |
| 2 | media-service → PostgreSQL: `media_assets`, `media_variants` | `media-service/internal/service/playback.go:123-227,247` |
| 3 | media-service → post-service `POST /v1/internal/media-access` (3 s client timeout, internal key); also group, chat, profile and commerce authorizers in an any-of chain | `media-service/internal/delivery/authz.go:171-195,219-238,398-437,766` |
| 4 | post-service decides from `posts`, `post_media`, visibility, blocks (graph-service), restrictions | `post-service/internal/http/media_access_handler.go:82-129` |
| 5 | media-service signs CloudFront URLs in process (RSA key from env); reuse cache per pod | `delivery/signer.go:108-203`, `sign_cache.go:155-238` |
| 6 | Client → CloudFront → S3 for the master, rung playlists and segments. Anonymous-scope assets are streamed by media-service from the blob store instead (`/hls-seg/`) | `infra/terraform/modules/media/main.tf:274-319`, `http/handler_anonymous.go:50-113` |
| 7 | Web in dev: the Next proxy fetches the redirect target itself | web `src/app/api/proxy/[...path]/route.ts:166-203` |
| 8 | Beside playback: `GET /v1/subtitles/:mediaId`, `GET /v1/media/:id/audio-tracks`, `GET /v1/audio/:id/serve` (sound), `POST /v1/analytics/events` → analytics PostgreSQL and Scylla `watch_sessions`, `reel_views`, `reel_view_counts` | `analytics-service/internal/store/scylla/engagement.go:34-75`, `watch.go:44` |

Failure behaviour to check: unresolved authorization answers 503, a resolved denial 404 (`http/delivery_viewer.go:34-45`); no Redis in this path.

### 9.2 Reels feed read (`GET /v1/feed/reels`)

| # | Call | Evidence |
|---|---|---|
| 1 | Gateway stamps the internal key; feed-service rate limit in Redis (`feed_rl:<user>`) | `feed-service/internal/http/handler.go:78-88,842-884` |
| 2 | Scylla `social_feed.home_timeline_by_user`, content types `flick` and `reel`, quorum | `feed-service/internal/service/feed.go:741`, `internal/store/scylla/timelines.go:418`, `cmd/server/main.go:76` |
| 3 | graph-service `GET /v1/internal/graph/blocked-and-muted` (memo 1 min), fail closed | `service/requestmemo.go:70`, `feed.go:1489` |
| 4 | PostgreSQL hidden-author filter | `service/hidefilter.go:37` |
| 5 | Following tab: follow list (Redis `feed:following:<viewer>` 5 min, then graph-service), fail closed | `service/requestmemo.go:96`, `feed.go:808-818` |
| 6 | First-page discovery fill: post-service `GET /v1/posts/recent` narrowed to flick and reel; a failure is logged and the page served without it | `feed.go:715-722,1629-1630` |
| 7 | Category chips: `GetReelCategoryPage` | `service/reels_category.go:55-92` |
| 8 | Hydration: Redis `MGET feed:hydrate:*` (`hydration.go:840`); post-service `POST /v1/posts/batch` (`post_batch.go:57`); profile `POST /v1/profiles/batch` (`hydration.go:711`); media-service `POST /v1/media/batch` (`hydration.go:751`); analytics `POST /v1/analytics/internal/content-views` (`view_counts.go:142`); trust-safety `GET /v1/internal/keyword-filters` (`keywordfilter.go:103`); graph `POST /v1/internal/graph/can` (`privacyfilter.go:174`) | as cited |
| 9 | A hydration failure answers 503 `FEED_UNAVAILABLE`, never raw ids | `http/handler.go:286-297` |

## 10. Open questions and things not determined

1. **Which web branch ships.** `AGENT-RULES.md` names `pb-integ` (`feat/tiktok-parity-web`), which is absent; this map read `pb-echo` (`feat/echo-web`).
2. **`media.events.long` in deployed Kafka.** The topic is not in the MSK topic list and auto-create is off, while prod and qa workers consume and forward to it. Whether it was created by hand is unknown; the code falls back to transcoding in place when the forward fails (`transcode_classes.go` rollout note), which needs a read of `longRouter.forwardIfLong`.
3. **`media_assets.moderation_status` CHECK.** Migration 008 allows `pending`, `passed`, `rejected`; the worker writes `manual_review`. A later migration may widen it (not found in the grep of CHECK lines); confirm in Stage 1 against a real schema.
4. **`media.copyright.pairs` has no consumer.** Confirm the copyright match pipeline ends in the database on purpose (shadow mode) and how a match reaches trust-safety (`trust-safety-copyright-review` reads `social.events.v1`).
5. **Auth on media reads behind the gateway.** Read routes trust `X-User-Id` with no token check of their own, and the gateway does not stamp the internal key on `/v1/media`. Whether anything other than the gateway can reach media-service (network policy is off for every reels service) decides whether that header can be forged.
6. **`GET /v1/media/:mediaId/renditions`, `/v1/subtitles/:mediaId` and `/status`** carry no auth middleware; whether their handlers go through the delivery gate was not read.
7. **`/v1/audio/:audioId/serve` for a signed-in web viewer.** The web media cookie is scoped to `/v1/media`, so the sound request carries no token; the server rule for sound audience was not traced.
8. **The view-count rule.** Thresholds and deduplication live in `analytics-service/internal/service/ingest.go` and `internal/model/video_events.go`; the exact rule (what counts once, replay protection, `ANALYTICS_VIEW_CAP_PER_VIEWER_PER_DAY` default) was not read end to end. `POST /v1/reels/:reelId/view` is retired.
9. **Server-side short-length refusal at create.** `VIDEO_TOO_LONG` exists in post-service, and the consumer reclassifies after measurement; whether `POST /v1/posts` refuses or converts an over-long flick whose media is already measured was not traced (`resolveVideoContentType`).
10. **Reels and repost.** The founder rule is "reels carry no repost"; `/v1/posts/:id/repost` is shared. Not checked whether post-service refuses a repost of a flick.
11. **`/v1/clips` and `/v1/series`** have handlers but no gateway prefix was found; dead or reachable another way.
12. **`media_renditions` table and `transcoding_jobs`.** Both exist; which one `GET /v1/media/:id/renditions` reads, and whether `media_renditions` is still written, was not determined.
13. **Deleting a reel during processing and after publish.** Paths exist (`skipGoneAsset`, post purge worker, `DELETE /v1/media/internal/:mediaId`, `media_blob_reclaim`); CDN invalidation was not found anywhere, so a signed segment URL stays valid until it expires (up to 12 h). Needs a Stage 2 read.
14. **GPS and metadata stripping for video.** `processing/strip_metadata.go` exists; whether video renditions and the retained original are stripped was not read.
15. **Cover image upload path** (custom cover as an image asset, `media_subtype`) and its moderation were not traced.
16. **Search indexing of flicks** (index name, removal on delete, private and blocked filtering) and **notification types** for reels were located only at the service level.
17. **Earnings for shorts.** Creator fund routes and tables exist and prod writes are off (`MONETIZATION_WRITES_ENABLED=false`); how flick views feed `creator_fund_earnings` was not traced.
18. **Android:** whether a Banuba token is present in any build (`.secrets` not opened); auto-scroll and data-saver defaults; "Reel 15s" and "Reel 60s" capture chips that attach no duration (`capture/CaptureRules.kt:20-21`).
19. **Secrets.** No secret value was opened. `.env`, `.secrets/`, `local.properties` and `google-services.json` were not read, as the rules require; a secret scan belongs to Stage 1.
20. **Client and deploy sections** rest on one delegated read each (see the note at the top).
