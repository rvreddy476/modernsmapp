# Long video (PostTube / MTube): Stage 0 map

- **Audit date:** 2026-11-17
- **Backend and Android tree:** `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` @ `58f602b6d`. 728 dirty paths in `git status --porcelain` (mostly uncommitted admin-service work). The working tree was read as it is.
- **Web tree:** `C:\workspace\wt\pb-echo` (repo `postbook-ui`), branch `feat/echo-web` @ `ca96921`, clean. `docs/harness/AGENT-RULES.md` names `pb-integ` as the web tree; that folder is still absent (same as the reels map).
- **How this map was built:** the gateway, post-service routes, watch progress, the RSS rule, upload expiry, the long-transcode router, the HLS ladder and segment signing were read directly. Android (section 9.1), web (9.2), deploy, CI and test counts (7, 10, 11), and the feed, search, analytics, notification, user-service, live and admin routes (2.3 to 2.8, 4 to 6) come from four delegated read-only passes. A sample of their claims was re-checked. One claim was corrected: the deploy pass said long videos "would never transcode" without `media.events.long`. In fact `forwardIfLong` falls back to transcoding in place (`media-service/cmd/worker/transcode_classes.go:214-220`). Treat any other line in those sections as read once, by one reader.
- Nothing was executed except `git rev-parse`, `git status` and read-only searches. No secret file was opened.

Backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Web paths are relative to `C:\workspace\wt\pb-echo`. Android paths are relative to `mobile/android/`; `T/` = `feature/tube/src/main/kotlin/com/us/android/feature/tube/`.

**Shared with reels (re-checked, not repeated in full):** the stack table, the media upload and transcode pipeline, the status machines, the media-service route table, the delivery gate and the CloudFront facts in `docs/production-readiness/reels/00-map.md` sections 1, 3.0, 3.1, 4, 5.5 and 9.1 apply to long video too. This map records only the facts that differ for long video, plus everything long-video-only.

## 1. Stack and name mapping

### 1.1 Stack (deltas from the reels map)

| Layer | Long-video fact | Evidence |
|---|---|---|
| Posts, channels, playlists, series, progress, chapters, credits, end screens | all in `post-service` | `post-service/internal/http/handler.go:315-434`, `channels_handler.go:45-64`, `credits.go:37-45` |
| Long-video feeds | `feed-service` `/v1/feed/videos`, `/v1/feed/watch`, related | `feed-service/internal/http/handler.go:374,449`, `related.go:90` |
| Channel subscriptions | moved from user-service to post-service; user-service subscribe routes answer 410 `ROUTE_RETIRED` | `user-service/internal/http/handler.go:195-204`, `retired_subscription_routes.go:45-58` |
| Search | OpenSearch indexes `posts_v1` (videos are post documents), `tube_channels_v1`, `tube_collections_v1` | `search-service/internal/store/search/tube.go:41-42,84-128` |
| Transcode | same worker. A long request (over 600 s, or landscape) is forwarded from `media.events` to `media.events.long`; the ladder keeps 1080p and 4K MP4 and 360/720/1080 HLS | `media-service/cmd/worker/transcode_classes.go:63-78`, `cmd/worker/main.go:743-752`, `internal/processing/video.go:21-25,49-56` |
| Storyboard (seek previews) | `storyboard_jpg` / `storyboard_vtt` variants for videos over a minute | `cmd/worker/main.go:787-795`, `internal/processing/storyboard.go` |
| Live replays | live-service-v2 emits `live.stream.vod_ready`; post-service creates an **unlisted** long video with `source='live'` | `post-service/internal/consumers/live_vod.go:59`, `service/live_vod.go:159` |
| RSS | built by post-service `GET /v1/channels/:ref/feed`, rendered by the web route `feed.xml` | `post-service/internal/service/channel_feed.go:236`, web `src/app/posttube/channel/[handle]/feed.xml/route.ts:106` |
| Admin | admin-service `/v1/admin/tube/*` proxies to post-service admin-token routes | `admin-service/internal/http/handler_tube.go:20-55`, `post-service/internal/http/admin_token.go:278-313` |

### 1.2 Name mapping

| Harness name | Backend | Android | Web |
|---|---|---|---|
| long video | `posts.content_type` = `long_video`. Legacy `video` maps to it (`monetization-service/internal/service/creator_fund_accrual.go:200`). The classifier boundary is 600 s or landscape (`shared/postclassify/classify.go:22`). A flick measured over 600 s is converted to `long_video` by the transcode consumer (reels map, step 10) | "Tube", module `:feature:tube`, `AppModule.POSTTUBE("posttube")` (`core/profile/…/data/AppModule.kt:29`), `LauncherApp.TUBE`, `FeedSurface` `videos` / `watch` (`core/model/…/FeedItem.kt:388-393`) | "PostTube", routes `/posttube/*`, `/tube/*` redirect (`src/app/tube/[[...path]]/page.tsx:20`), brand `tube` (`src/lib/appBrand.ts:72`), telemetry `contentType:"long_video"`, `surface:"posttube_watch"` |
| "MTube" | test and file names only (`post-service/internal/http/mtube_routes_test.go`, migration `051_mtube_contracts.sql`) | — | — |
| media side | no `long` subtype at upload. The worker class is `classLong` and the topic is `media.events.long`; "long-form" in comments | — | — |
| channel | `channels` table (one per user, `uq_channels_user`, migration 041), `/v1/channels/*` | `ChannelApi`, `TubeChannelRoute` | `/posttube/channel/[handle]` |
| playlist, "collection" | `playlists` (`kind` `user` or system; visibility `public`, `unlisted`, `private`), `playlist_items`; system kinds `watch_later`, `liked` | "Collections" (`TubeCollectionsRoute`) | `/posttube/playlists`, `/queue` (= watch later), `/loved` (= liked) |
| dislike | "tune": `tunes` table, `/v1/posts/:id/tune` | `VideoLibraryApi.kt:41` | `watchApi.ts:247` |
| series | **two systems:** `video_series` + `video_series_episodes` at `/v1/video-series` (long video), and `flick_series` at `/v1/series` (shorts) | `VideoSeriesApi.kt:17` | `studioApi.ts:251-274` |
| resume, history | `watch_progress` table, `/v1/videos/:id/progress`, `/v1/videos/history`, `/v1/videos/continue-watching` | `WatchProgressApi`, `WatchHistoryApi` | `posttubeApi.ts:361-422` |
| captions | `media_subtitles`, `media_caption_jobs`, `/v1/subtitles/*` (media-service) | network captions not wired (`T/ui/watch/WatchViewModel.kt:909-911`) | `hubApi.ts:1168-1185`, player text tracks |
| members-only | `posts.tier_required_id` (migration 016), `PUT /v1/posts/:id/membership` | `MembersOnly.kt`, locked state | `memberships/model.ts:812-841` |
| visibility | `posts.visibility` (`public`, `followers`, `friends`, `private`, `unlisted`, `trusted`, `close_friends`, `staged`); edit allows `public`, `followers`, `friends`, `private`, `unlisted` (`post-service/internal/service/post_edit.go:50`). "Scheduled" is `publish_at` set, not a visibility | create: Public, Followers, Private only (`feature/post/…/createhub/ReelSurface.kt:1425-1429`) | create: public, followers, friends, unlisted, private (`src/features/upload/components/StudioSettings.tsx:30-36`); hub type adds `scheduled` (`hub/hubApi.ts:113`) |
| Copyright Match | media fingerprints and shadow matching, post-service title holds and reviews (reels map 5.1 and 5.6) | `CopyrightReviewRepository.kt:35` | `copyright/api.ts:21` |
| offline copy | `post_offline_copies`, `/v1/posts/:id/offline` | `T/ui/offline/OfflineScreen.kt` | `/posttube/offline` |

## 2. Route tables

### 2.0 Gateway

Chain, token handling, rate limit (per IP 100 rps, per user 60 rps, fail-open on Redis), body cap and the `/internal/` refusal are as in the reels map 3.0. Long-video prefixes, all routed to post-service with the internal key stamped:

| Prefix | Routing | Internal key stamped |
|---|---|---|
| `/v1/channels` | `api-gateway/cmd/server/main.go:488` | `pkg/internalroutes/internalroutes.go:381` |
| `/v1/videos` | `main.go:556` | `:383` |
| `/v1/playlists` | `main.go:562` | `:389` |
| `/v1/video-series` | `main.go:572` | `:390` |
| `/v1/series` | `main.go:573` | `:391` |
| `/v1/creators` | `main.go:574` | `:392` |
| `/v1/credits` | `main.go:588` | `:396` |
| `/v1/livestream` (to live-service-v2) | `main.go:737` | `:459` |

**Correction to the reels map:** it said `/v1/series` had no gateway prefix. It is routed (`main.go:573`).

Auth words below are the reels map's. **user** means post-service rejects an empty or invalid `X-User-Id`. Every post-service `/v1` route needs the internal key, so only the gateway and peer services reach it (`post-service/internal/http/handler.go:76-85`). **Rate limit:** unless stated, the only limit is the gateway's ("edge").

### 2.1 post-service: long-video routes

| Method | Path | Handler file:line | Auth | Rate limit | Gateway |
|---|---|---|---|---|---|
| POST | `/v1/posts` (`content_type=long_video`, `notify_subscribers`, `tier_required_id`, `publish_at`) | `handler.go:90` (`create_post_handler.go:326`) | user | post budget 20/h, 50/day (`service/ratelimit.go:26-70`) + `Idempotency-Key` | yes |
| GET | `/v1/posts/:postId` | `handler.go:116` | public, visibility checked | edge | yes |
| PATCH | `/v1/posts/:postId` (visibility, cover, license, embedding, age restriction, credits, genres, tier) | `handler.go:130` (`post_edit.go:184`) | user (owner) | edge | yes |
| PATCH | `/v1/posts/:postId/schedule`; GET `/v1/posts/me/scheduled` | `handler.go:126,125` | user (owner) | edge | yes |
| GET, PUT | `/v1/posts/:postId/private-shares` | `handler.go:133-134` | user (owner) | edge | yes |
| POST, DELETE | `/v1/posts/:postId/watch-later` | `handler.go:135-136` (`post_edit.go:512`) | user | edge | yes |
| POST, DELETE, GET | `/v1/posts/:postId/tune`, `/tune/me` (dislike) | `handler.go:170-172` | user | edge | yes |
| GET | `/v1/posts/:postId/series` | `handler.go:185` | public | edge | yes |
| GET | `/v1/posts/live-recordings` | `handler.go:96` (`post_reads_handler.go:140`) | public | edge | yes |
| GET | `/v1/posts/by-author/:authorId[?type=long_video]`, `/counts` (channel video tab) | `handler.go:102-103` (`post_reads_handler.go:210-219`) | public; viewer-scoped, unlisted listed to the author only (`service/post_reads.go:251`) | edge | yes |
| GET | `/v1/posts/categories?kind=long`, `/v1/posts/trending?content_type=long_video` | `handler.go:92,284` | public | edge | yes |
| GET | `/v1/posts/genres` | `credits.go:37` | public | edge | yes |
| GET, POST, DELETE | `/v1/credits/requests`, `/:creditId/accept`, `/decline`, DELETE `/:creditId` | `credits.go:40-43` | user | edge | yes |
| GET | `/v1/creators/:creatorId/appearances` | `credits.go:45` | public | edge | yes |
| GET, POST | `/v1/posts/:postId/chapters` | `handler.go:406-407` (`video_series_handler.go:886`) | GET public (viewer-gated, `service/watch_progress.go:283`); POST user (owner) | edge | yes |
| GET, POST | `/v1/posts/:postId/end-screens`, `/cards` | `handler.go:408-411` | GET public; POST owner | edge | yes |
| POST | `/v1/posts/:postId/end-screens/:elementId/impression\|click`, `/cards/:cardId/impression\|click` | `handler.go:414-417` | open (verify) | **edge only: counters writable by anyone** (open question 14) | yes |
| PUT | `/v1/posts/:postId/membership` | `handler.go:418` | user (owner) | edge | yes |
| POST | `/v1/posts/:postId/copyright-review/request` | `handler.go:421` | user (owner) | edge | yes |
| GET | `/v1/videos/continue-watching` | `handler.go:427` | user | edge | yes |
| GET, DELETE | `/v1/videos/history` | `handler.go:430-431` (`video_series_handler.go:1136,1165`) | user | edge | yes |
| POST, GET, DELETE | `/v1/videos/:videoId/progress` | `handler.go:432-434` (`video_series_handler.go:990`) | user; POST checks the post is readable (`:1012`) | **edge only** | yes |
| GET, PATCH, POST | `/v1/videos/:videoId`, `/trim`, `/category`, `/cover-frame`, `/publish` | `handler.go:315-319` (`video_tools_handler.go`) | user (owner) for writes | edge | yes |
| POST, GET, PATCH, DELETE | `/v1/video-series[/:seriesId[/episodes[/:episodeRef]]]` | `handler.go:365-378` | user (owner) for writes | edge | yes |
| GET | `/v1/creators/:creatorId/video-series` | `handler.go:380` | public, viewer-filtered (`service/series_visibility.go:54-114`) | edge | yes |
| POST, GET | `/v1/series/*` (flick series; shorts) | `handler.go:349-354,357` | mixed | edge | yes (see 2.0) |
| POST, GET, PATCH, DELETE | `/v1/playlists`, `/system/:kind`, `/:playlistId`, `/:playlistId/items[/:postId]` | `handler.go:385-398` (`video_series_handler.go:600,696`) | user for writes; GET public for `public` and `unlisted` playlists, creator only for `private` (`service/playlists.go:56-60`) | edge | yes |
| GET | `/v1/creators/:creatorId/playlists` | `handler.go:400` | public | edge | yes |
| POST, GET, PATCH | `/v1/channels`, `/me`, `/me/action/stats`, `/handle-available`, `/name-available`, `/batch`, `/search`, `/subscriptions`, `/:ref` | `channels_handler.go:47-59` | mixed | edge | yes |
| GET | `/v1/channels/:ref/feed` (RSS source) | `channels_handler.go:60` (`:441`; `service/channel_feed.go:236`) | public, no viewer by design | edge (web adds `s-maxage=600`) | yes |
| POST, DELETE, GET, PATCH | `/v1/channels/:ref/subscribe`, `/:ref/subscription` (`notify_on` `all` or `none`) | `channels_handler.go:61-64` | user | edge | yes |
| GET | `/v1/uploads/videos`, `/counts`; POST `/bulk`, `/bulk-delete`; DELETE `/:postId` | `my_uploads.go:19-28` | user | edge | yes |
| PUT, DELETE | `/v1/comments/:commentId/pin` | `handler.go:342-343` | user (verify: post owner) | edge | yes |
| GET | `/internal/channels/by-owner/:userId`, `/internal/channels/:channelId/subscriber-ids`, `/internal/users/:userId/subscribed-owner-ids` | `channel_subscriptions_handler.go:194-196` | key | none | refused |
| GET | `/internal/tube/channels`, `/internal/tube/playlists` (search reindex source) | `channels_internal_list.go:41-42` | key | none | refused |
| all | admin tube: `/tube/stats`, reports, channels search and get, `/creators/:userId/video-series`, copyright titles, reviews, decisions, tracks | `admin_token.go:278-313`, `copyright_review.go:92-99` | admin (`tube:*` permissions, `admin_token.go:63-67`) | none | admin-service only |

Comments, likes, shares, bookmarks, reports and offline routes are shared with reels; see the reels map 3.2.

### 2.2 media-service

Shared with reels (reels map 3.1). Long-video-specific uses: `/v1/media/upload/resumable/*` (large files), `/v1/media/:id/serve/storyboard_vtt|storyboard_jpg` (web `watch/watchApi.ts:282,286`), `/v1/media/:id/suggested-frames` and `/cover-frame`, `/v1/subtitles/*` (captions), `/v1/media/:id/download` (owner only). No route-level auth on `GET /v1/subtitles/:mediaId` or `/renditions` (reels open question 6 still stands).

### 2.3 feed-service (internal key on every route; `feed_rl:<uid>` 120 per minute, fail-open, skipped for empty `X-User-Id`, `handler.go:837-880`)

| Method | Path | Handler | Auth |
|---|---|---|---|
| GET | `/v1/feed/videos` (`following_only`, `subscribed_only`, `category`, `sort`, `chip`) | `GetLongVideoFeed` `handler.go:374` (chip :516, subscribed :541) | user |
| GET | `/v1/feed/watch` | `GetVideoFeed` `handler.go:449` | user |
| GET | `/v1/feed/videos/:postId/related` | `related.go:90` | user |
| GET | `/v1/feed/home?platform=posttube` | `handler.go:129`, filter `:158-190` | user |
| POST, GET | `/v1/feed/preference` (`long_video_frequency`) | `handler.go:604-662`, store `service/longvideo.go:122-133` | user |

The web watch page is public; whether a signed-out viewer gets related videos was not checked (feed routes need a user).

### 2.4 search-service (internal key on every route, `handler.go:137`; `resolveBlockScope` fails closed, `:143`)

| Method | Path | Handler | Rate limit |
|---|---|---|---|
| GET | `/v1/search/posts?type=videos&sort&duration&date&features&genre&year` | `handler.go:222`, options `handler_tube.go:41` | edge only |
| GET | `/v1/search/channels` | `handler_tube.go:130` | edge only |
| GET | `/v1/search/collections` | `handler_tube.go:174` | edge only |
| POST | `/v1/search/internal/reindex/tube` (synchronous, 15 min timeout) | `handler_tube.go:224` | key; refused at gateway |

### 2.5 analytics-service (internal key, `handler.go:66-69`)

| Method | Path | Auth |
|---|---|---|
| POST | `/v1/analytics/events` (views, watch time; batch at most 200) | user (`X-User-Id`) |
| GET | `/v1/analytics/content/:contentId?period=7d\|28d\|90d\|365d` | owner or admin, else 404 (`content_insights.go:81-85,154`) |
| GET | `/v1/analytics/dashboard/{overview,content,content/:id,…/trend,…/retention,…/demographics,trend}` | owner (`dashboard.go:59-68`, `cmd/server/main.go:285,293`) |
| GET | `/v1/analytics/creator/me`, `/content/:id/views` | user |

### 2.6 user-service

| Method | Path | Fact |
|---|---|---|
| any | `/v1/users/me/channels`, `/v1/channels/:id`, `/v1/users/:userId/channels` | `handler.go:142,183-192`. The gateway routes `/v1/channels` to post-service (`api-gateway/cmd/server/main.go:488`), so these user-service channel routes are reachable only if another prefix leads there (open question 9) |
| any | subscribe, subscription, subscribers, `/v1/users/:userId/subscriptions` | 410 `ROUTE_RETIRED` (`retired_subscription_routes.go:45-58`) |
| GET | `/internal/channels/*`, `/internal/users/:userId/subscribed-owner-ids` | key; marked superseded by post-service (`handler.go:127-129`) |

### 2.7 live-service-v2, admin-service, monetization, trust-safety

| Service | Routes | Evidence |
|---|---|---|
| live-service-v2 | no public replay route. VOD path: egress webhook (LiveKit JWT) → import sweeper → `live.stream.vod_ready` → post-service | `live-service-v2/internal/http/handler.go:69-127` |
| admin-service | `/v1/admin/tube/stats`, `/videos/review-queue`, `/:postId`, `/moderation`, `/review-status`, `/visibility`, `/reports`, `/channels/*`, `/creators/:userId/series`, `/copyright/*`; reject and block need `tube:videos.remove` plus step-up | `admin-service/internal/http/handler_tube.go:20-55`, `handler_content.go:48,75-87` |
| monetization-service | `/v1/monetization/creator-fund/*`; long-video RPM types `long_video` and `flick` | `monetization-service/internal/http/handler.go:201-212`, `service/creator_fund.go:371,410` |
| trust-safety-service | `GET /v1/internal/standing/:userId` returns `LongVideoPausedUntil` (key + service signature) | `trust-safety-service/internal/http/standing_handler.go:199`, `service/standing.go:76` |

## 3. Tables and migrations

**post-service** (`post-service/database/migrations/`, 83 files, latest `085_outbox_seq.sql`):

| Table or column | Migration | Notes |
|---|---|---|
| `video_series`, `video_series_episodes`, `playlists` (CHECK `public`, `unlisted`, `private`), `playlist_items`, `media_chapters` (`source` `manual` or `ai_generated`), `video_end_screens` | `012_posttube_features.sql:5-68` | |
| `watch_progress` | `010_postbook_features.sql`; `client_sent_at` added in `081_tube_integrity.sql:87` | no retention sweeper found; cleared per user by `DeleteAllWatchProgress` (`store/postgres/watch_progress.go:230`) |
| `tunes` (dislike), `video_cards`, `post_product_tags` | 010 / 012 (not split further) | |
| `posts.tier_required_id` | `016_membership_gating.sql:14` | |
| `channels` (one per user) | `041_channels.sql:23-52`, `043`, `067_channel_name_unique.sql`; banner, links, contact, featured in `051_mtube_contracts.sql:130-138` | **also created by user-service** `001_phase6_channels_pages_reputation.sql`; 041 copies the user-service column set (open question 9) |
| `channel_subscriptions` (`notify_on` `all` or `none`) | `046_channel_subscriptions.sql:24-57`; also user-service `004_channel_subscriptions.sql` | |
| `posts.category_slug` | `047_posts_category_slug.sql` | |
| `posts.source` (`upload` or `live`), `posts.live_stream_id` (unique), `playlists.kind` | `051_mtube_contracts.sql:111-143` | |
| `end_screen_stats`, `card_stats` | `054_end_screen_v2.sql:38-56` | |
| live recordings not saveable | `061_live_recordings_not_saveable.sql` | |
| `post_credits`, `post_credit_tag_events`, `posts.genres`, `release_year`, `production_house` | `070_video_credits.sql:29-78` | |
| `reel_drafts.tier_*` | `071_reel_draft_tier.sql` | web long uploads publish through `/v1/reels/drafts` |
| copyright title review tables | `073_copyright_title_review.sql` | |
| unique `(playlist_id, post_id)`, series ownership fixes | `081_tube_integrity.sql:4-87` | |

**media-service:** same tables as the reels map 5.1. Long-video weight: `resumable_uploads`, `resumable_upload_parts`, `media_variants` (1080p, 4K, storyboard), `media_subtitles`, `media_caption_jobs`, `copyright_*`.

**analytics-service:** `analytics.playback_sessions`, `analytics.ingest_receipts`, hourly and daily rollups (reels map 5.1). **ScyllaDB:** `social_analytics.watch_sessions` (TTL 90 d, `analytics-service/internal/store/scylla/watch.go:42-55,163`); `viewer_history` is written only by `IncrementViewerHistory`, which no non-test code calls (`watch.go:119-146`).

**feed-service:** the `long_video_frequency` preference table (name not read; `service/longvideo.go:122-133`).

## 4. Kafka topics

| Topic | Producer | Consumers (group) | Long-video use |
|---|---|---|---|
| `media.events` | media-service outbox relay | `media-transcode-worker` (forwards long requests); `post-service-media-transcode`; `search-service-media-group` (`search-service/cmd/server/main.go:212-214`, sets `has_subtitles`) | transcode request and completion |
| `media.events.long` | media worker short loop (`transcode_classes.go:214`) | `media-transcode-worker-long` (`:77-78`) | **absent from the MSK topic list** (`infra/terraform/modules/msk/topics-job/variables.tf:30-59`) while `auto.create.topics.enable=false` (`modules/msk/main.tf:147`). A failed forward transcodes in place (`transcode_classes.go:214-220`). Present locally (`Architecture/docker/init-topics.sh:54`) |
| `social.events.v1` | post-service outbox: PostCreated (`internal/events/producer.go:95`), scheduled publish (`service/schedule.go:457`), live VOD post (`service/live_vod.go:202`), TubeChannelCreated and Updated (`store/postgres/channels.go:191,242`), TubePlaylistVisibilityChanged (`playlists.go:170`), PostSearchEligibilityChanged, `post.copyright_review.changed` (`copyright_title_review.go:353`) | `feed-service-group` (PostCreated `feed-service/internal/events/consumer.go:156`, distribution `:225`, TubeChannelSubscribed and Unsubscribed `:236`); `search-service-group` (`consumer.go:542-600,970-984`); `notification-service-group` (upload fan-out); `analytics-content-ownership` (`content_ownership.go:52`); `trust-safety-copyright-review` (`copyright_review_consumer.go:62`) | new uploads, indexing, ownership (duration source for views), copyright |
| `social.events.v1` (`live.stream.vod_ready`) | live-service-v2 outbox (`live-service-v2/internal/events/producer.go:94-101`) | `post-service-live-vod` (`post-service/internal/consumers/live_vod.go:59`, topic env `LIVE_KAFKA_TOPIC`) | replay becomes an unlisted long video |
| `social.events.v1.dlq`, `<COPYRIGHT_REVIEW_KAFKA_TOPIC>.dlq`, `search.events.v1.dlq` | consumers | replayers | |

## 5. Caches and Redis keys (long-video-specific; others in the reels map 5.4)

| Key | TTL | Owner | Evidence |
|---|---|---|---|
| `watch_progress:<user>:<post>` (hash: position, last watched, completed) | 90 d | post-service; best-effort mirror after the DB write; GET reads Redis first | `post-service/internal/service/watch_progress.go:14,43-79` |
| `user:subscribed_owners:<viewer>` (set) | 6 h | feed-service | `feed-service/internal/ranking/signals.go:122`, `service/subscriptions.go:66,210` |
| `feed:subscriptions:looked:<viewer>`, `feed:subscriptions:warming:<viewer>` | 6 h, 10 min | feed-service | `subscriptions.go:107,112,212,241` |
| `feed:mainfeed:<postId>` | 24 h | feed-service | `service/longvideo.go:347,352` |
| `feed:pref:<uid>` | 5 min | feed-service | `feed.go:1082-1104` |
| `feed:hydrate:<viewer>:<post>` (scheduled, processing and members-only rows never cached) | 5 min | feed-service | `hydration.go:863-893` |
| upload budget `upload:rate:<user>:hour|day` (also counts resumable inits) | 1 h, 24 h | media-service | `media-service/internal/service/resumable.go:120-125`, `ratelimit.go:26-27` |
| notification post state, channel name | 30 s, 10 min, in memory | notification-service | `service/fanout_eligibility.go:173,217` |
| RSS enclosure cache | in process | post-service | `service/channel_feed.go:339-349` |
| RSS HTTP cache | `public, s-maxage=600, stale-while-revalidate=3600`, ETag | web | `feed.xml/route.ts:21,89-96` |
| signed URL reuse | 2 min reuse window; segments signed for 2x duration + 15 min, cap 12 h | media-service | `media-service/internal/delivery/sign_cache.go:54,63,66` |

## 6. Storage buckets and CDN

Same single media bucket and CloudFront distribution as reels (reels map 5.5), with these long-video facts:

| Item | Fact | Evidence |
|---|---|---|
| Object layout | `user/<uploader>/<media>/original`, MP4 rungs up to `1080p` and `4k`, `hls/` (360/720/1080, capped at the source height), storyboard variants | `cmd/worker/main.go:743-795`, `processing/video.go:21-56` |
| Abandoned multipart, app side | resumable session TTL 24 h (`resumable.go:38`); the blob reclaim worker (every 10 min) marks expired sessions and calls `AbortMultipartUpload`, best-effort, 200 per tick (`service/deletion.go:156-231`); a failed init aborts at once (`resumable.go:154,172`) | as cited |
| Abandoned multipart, bucket side | lifecycle `abort-incomplete-multipart` after 7 days on all prefixes | `infra/terraform/modules/media/main.tf:169-176` |
| Abandoned simple upload | `pending_upload` rows older than 24 h are deleted by orphan GC (every 15 min) | `service/orphan_gc.go:38-43` |
| Cold tier rule | `transcodes/` prefix to IA at 60 d; the code never writes that prefix | `main.tf:153-166` |
| CDN behaviours | default signed, CachingOptimized 24 h; `public/*` unsigned; no HLS or long-video behaviour, no signed cookies, no invalidation found in code | `main.tf:289-319` |
| Manifest gate | `/v1/media/:id/hls/:playlist` asks post-service as the viewer, then rewrites segment URLs as signed URLs | reels map 9.1, `media-service/internal/http/handler.go:172` |
| Segment gate | CloudFront signature only. A 4-hour video gets 12 h signed segment URLs, which outlive a visibility change, delete or takedown | `sign_cache.go:63-66` |
| Original retained | the original is kept; the owner-only `/download` route serves it | `service/download.go:57-88` |
| RSS enclosure | public `/v1/media/:id/serve/720p` (or 480p, or original) on the API host; web prepends `FEED_MEDIA_BASE_URL` | `channel_feed.go:200-227`, `deploy/web/app/values-prod.yaml:77` |

## 7. Feature flags and env config per environment

From `deploy/services/*/values-*.yaml`. No `values-dev.yaml` exists; dev is the devbox.

| Setting | prod | qa | staging | azure-prod | azure-staging |
|---|---|---|---|---|---|
| media worker replicas | 2 (`media-service/values-prod.yaml:153-155`) | 1 (`values-qa.yaml:169-171`) | 1 (`values-staging.yaml:105-107`) | 3 (`values-azure-prod.yaml:53-55`) | 1 (`values-azure-staging.yaml:47-49`) |
| worker scratch `emptyDir` | 50 Gi (:166) | 20 Gi (:184) | 20 Gi (:120) | 50 Gi (:66) | 20 Gi (:60) |
| `MEDIA_TRANSCODE_CLASSES` | `short,long` (:176) | `short,long` (:189) | unset = both | unset = both | unset = both |
| `MEDIA_WORKER_MAX_ORIGINAL_BYTES` | unset = 16 GiB | 6 GiB (:126) | 6 GiB (:74) | unset = 16 GiB | 6 GiB (:38) |
| `MEDIA_MAX_VIDEO_DURATION_SEC` | unset = 4 h (`processing/limits.go:57`) | same | same | same | same |
| `MEDIA_SCANNER_ENABLED` / `BACKEND` | true / rekognition (:107-109) | same (:116-118) | same (:66-68) | **absent = scanner off** | absent |
| `COPYRIGHT_FINGERPRINT_ENABLED` / matching (media) | true / true (:140-141) | true / true (:157-158) | absent = off | absent | absent |
| `COPYRIGHT_TITLE_HOLD_ENABLED` / `AUTO_BLOCK_ENABLED` (post) | true / true (`post-service/values-prod.yaml:84,86`) | true / true | true / true | true / true | true / true |
| `ANALYTICS_VIEW_SOURCE` | `sessions` (`analytics-service/values-prod.yaml:56`) | `sessions` (:65) | **unset = `play_end`** | unset = `play_end` | unset = `play_end` |
| `ANALYTICS_VIEW_CAP_PER_VIEWER_PER_DAY` | unset = 1 (`aggregation/daily.go:23`) | same | same | same | same |
| `SUBSCRIBERS_SERVICE_URL` (notification) | set (`notification-service/values-prod.yaml:88`) | set (:97) | unset = `POST_SERVICE_URL` | unset | unset |
| `MEMBERSHIPS_ENABLED` (monetization) | false (:91) | false | false | false | false |
| `FEED_MEDIA_BASE_URL` (RSS, web app) | `deploy/web/app/values-prod.yaml:77` | `values-qa.yaml:85` | absent | absent | absent |
| tube admin ops in `SERVICE_CALLER_ADMIN_SERVICE_OPS` (post) | listed (:143); the admin key "NOT enabled yet" (:70) | :152 | :92 | :91 | :82 |

**Code defaults with no values entry:** `MEDIA_TRANSCODE_MAX_ATTEMPTS` 8, `MEDIA_TRANSCODE_STALL_SWEEP` on, `COPYRIGHT_FINGERPRINT_ORIGINAL_MAX_MINUTES` 180 and `_MAX_MB` 2048 (`cmd/worker/fingerprint_runner.go:137-157`), `MaxTranscodeJobBudget` 48 h (`processing/limits.go:158`).

**No env flag exists** named `TUBE_*`, `LONG_*`, `PLAYLIST*`, `SERIES*`, `CHAPTER*`, `RSS*` or `*QUOTA*` in any Go service. **No per-user storage quota and no transcode-minute quota exist.** The only upload control is the count budget (10 per hour, 30 per day, fail-open), so one user can start about 30 x 16 GiB per day in prod.

**Clients:** web has no PostTube flag (`src/lib/productFeatures.ts:31-44`, not in `parkedProducts.ts:20-28`). On Android, `ProductGate` leaves `LauncherApp.TUBE` ungated on purpose (`app/…/features/ProductGate.kt:26-28`). PostTube cannot be switched off by flag on either client.

## 8. Background workers and sweepers

| Where | Worker | Interval | Evidence |
|---|---|---|---|
| media server | blob reclaim + resumable expiry (multipart abort) | 10 min | `service/deletion.go:156-231` |
| media server | orphan GC of `pending_upload` | 15 min, age 24 h | `service/orphan_gc.go:38-43` |
| media server | caption worker (auto subtitles) | — | `cmd/server/main.go` (reels map 5.3) |
| media worker | short loop (forwards long), long loop, stall sweeper 5 min, fingerprint loop | — | `cmd/worker/main.go:174-252`, `transcode_classes.go` |
| post-service | schedule publisher (`POST_SCHEDULE_INTERVAL` 30 s), draft publisher 60 s, outbox, purge worker (720 h restore window), live VOD consumer | — | `post-service/cmd/server/main.go:535,637-737` |
| post-service | **no watch_progress retention sweep found** | — | — |
| notification-service | subscriber fan-out drain every 5 s (batch 10, stale 10 min, 5 attempts); cleanup every 6 h removes jobs older than 7 d | — | `notification-service/internal/service/subscriber_fanout.go:38-40,188-215`, `cmd/server/main.go:335` |
| analytics-service | hourly aggregator 5 min, daily rollup 15 min, session finalizer 60 s (10 min idle) | — | `aggregation/hourly.go:22`, `daily.go:40`, `finalizer.go:14-15` |
| search-service | tube reindex: manual only (`POST /internal/reindex/tube`, mark and sweep); `cmd/backfill -entity posts` | manual | `search-service/internal/reindex/tube.go:63-195` |
| live-service-v2 | lifecycle sweeper 15 s; recording import (20 per claim, 30 s re-poll, give up after 6 h) | — | `service/lifecycle.go:438`, `recording.go:1-25,290-313` |
| monetization-service | creator fund accrual (interval not read) | — | `cmd/server/main.go:332-336` |
| Deploy | no CronJob anywhere; every sweeper runs inside a long-lived pod | — | section 11 |

## 9. Clients

### 9.1 Android (`:feature:tube` plus `:core:feed`, `:core:media`, `:core:analytics`, `:feature:post`, `:feature:search`)

Routes are type-safe Navigation-Compose classes in `T/navigation/TubeNavigation.kt`, registered at `:227-254` and wired at `app/…/navigation/UsNavHost.kt:1375-1418`.

| Route (TubeNavigation.kt) | Screen | ViewModel |
|---|---|---|
| `TubeHomeRoute` :35 | `T/ui/home/TubeHomeScreen.kt:63` | `TubeHomeViewModel.kt:80` |
| `TubeSubscriptionsRoute` :39 | `T/ui/subscriptions/SubscriptionsScreen.kt:42` | `SubscriptionsViewModel.kt:36` |
| `TubeYouRoute` :43 | `T/ui/you/YouScreen.kt:87` | `YouViewModel.kt:45` |
| `TubeChannelRoute(userId)` :47 (Live tab = replays, filtered client-side on `source=="live"`) | `T/ui/channel/ChannelScreen.kt:90`, `EditChannelSheet.kt`, `SubscribeControl.kt` | `ChannelViewModel.kt:148` |
| `TubeScheduledRoute` :54 | `T/ui/scheduled/ScheduledPostsScreen.kt:60` | `ScheduledViewModel.kt:32` |
| `TubeSavedRoute` :58 | `T/ui/saved/SavedVideosScreen.kt:49` | `SavedVideosViewModel.kt:43` |
| `TubeCollectionsRoute` :62, `TubeCollectionRoute(id)` :70 (`watch_later` :72, `liked` :75) | `T/ui/collections/CollectionScreens.kt:76,377`, `CollectionPickerSheet.kt` | `CollectionViewModel.kt:129,439` |
| `TubeHistoryRoute` :85 | `T/ui/history/HistoryScreen.kt:73` | `HistoryViewModel.kt:67` (pause :155, remove :161, clear :190) |
| `TubeOfflineRoute` :92 | `T/ui/offline/OfflineScreen.kt:75` | `OfflineViewModel.kt:37` |
| `TubeCreatorHubRoute` :100, `…UploadsRoute` :108 | `T/ui/hub/CreatorHubScreen.kt:106` | `CreatorHubViewModel.kt:99` |
| `TubeCreditRequestsRoute` :115 | `T/ui/credits/CreditRequestsScreen.kt:68` | `CreditRequestsViewModel.kt:52` |
| `TubeTrendingRoute` :119, `TubeTopicsRoute` :123, `TubeTopicRoute` :127 | `T/ui/discovery/DiscoveryScreens.kt:89,145,214` | `DiscoveryViewModels.kt:153,182,214` |
| `WatchRoute(postId)` :131 | `T/ui/watch/WatchScreen.kt:110` (chapters `WatchChapters.kt:41`, credits, series `SeriesAdvance.kt`, `UpNext.kt`, end screens, PiP, settings sheet, shared `CommentsSheet` :356) | `WatchViewModel.kt:177` |
| Upload | `feature/post/…/createhub/CreateHubScreen.kt:85-87` (Video surface, 16:9 cover), `ReelSurface.kt`, `ReelPublishViewModel.kt`, `CoverPicker.kt:64-75`, `ReelPublishWorker.kt` | |
| Search | `feature/search/…/SearchNavigation.kt:66,81` (`SearchScope.VIDEOS`, `wireType="videos"`) | |
| Deep links | `/posttube/watch/{id}`, `/posttube/channel/{handle}` (`app/…/push/PushDestinations.kt:63,69`, `UsNavHost.kt:711`) | |

API declarations: `T/data/WatchProgressApi.kt:19,25`, `core/feed/…/data/WatchHistoryApi.kt:26-37`, `VideoFeedApi.kt:34-141`, `Discovery.kt:32-58`, `VideoLibraryApi.kt:29-114`, `ChannelApi.kt:48-88`, `T/data/VideoSeriesApi.kt:17`, `credits/CreditsApi.kt:36-56`, `T/data/WatchElements.kt:40-47`, `T/data/hub/CreatorHubApi.kt:27-45`, `offline/OfflineApi.kt:35-54`, `core/media/…/upload/MediaUploadApi.kt:37-317`.

| Player and state | Fact | Evidence |
|---|---|---|
| Quality | Auto or pinned height from the HLS tracks; MP4 and dubs offer Auto only | `PlayerQuality.kt:14,32-52`, `WatchViewModel.kt:895-898`, `WatchSettingsSheet.kt:41-46` |
| Speed | `PLAYBACK_SPEEDS` 0.5x to 2x | `core/ui/…/UsPlaybackChoices.kt:15-16`, `WatchViewModel.kt:900-903` |
| Seek | double-tap ±10 s; `recordSeek` to the tracker | `WatchViewModel.kt:870-893` |
| Captions | **offline copies only**; network playback carries none | `WatchViewModel.kt:905-930`, `PlayerCaptions.kt:16-20` |
| Resume | read beside prepare; dropped after 5 s of play; restart at ≥95% | `WatchViewModel.kt:738-744,1107`, `ResumeRule.kt:13-24` |
| Progress writes | every 10 s while playing, on pause or end, Up-next, background, clear; `completed` at ≥95% | `WatchViewModel.kt:416-462,820-830,1104` |
| History pause | **client-only** DataStore `watch_history_paused`; `save` returns early | `T/data/WatchProgressRepository.kt:42-43`, `core/datastore/…/SettingsDataStore.kt:290-297,349` |
| Telemetry | `VideoWatchTracker` surface `POSTTUBE`; heartbeat 5 s | `WatchViewModel.kt:615-645`, `core/analytics/…/VideoWatchTracker.kt:562,572` |
| HLS refresh | re-prepare on 403 or `X-HLS-Expires-At`, at most 2 in a row | `core/media/…/HlsRefresh.kt:31-46,118` |
| Upload | resumable at ≥100 MiB, server-sized chunks, 6 attempts; WorkManager unique queue `reel-publish-queue`; queue JSON in `filesDir`, video copy in `noBackupFilesDir`; `content_type=long_video`, `notify_subscribers=true`; no client size or length cap | `ResumableUploader.kt:23-41,359-370`, `ReelPublishWorker.kt:168-186`, `ReelPublishStore.kt:243,309`, `ReelPublishPipeline.kt` (~:456) |
| Upload metadata | title (100), description, visibility (Public, Followers, Private; **no Unlisted at create**), category, hashtags (30), schedule, credits, genres, year, production house, members level, action button, cover. **No captions at upload** | `ReelPublishStore.kt:61-112`, `ReelSurface.kt:1425-1429` |
| Offline | Media3 DownloadManager in `noBackupFilesDir/offline_copies`; **not encrypted**; expiry enforced client-side | `OfflineStorage.kt:10-99`, `OfflineRules.kt:102-141` |
| Local DB | no Tube Room tables; progress and history are server-only | `core/database/…/UsDatabase.kt:23` |

### 9.2 Web (`C:\workspace\wt\pb-echo`)

Middleware protects everything by default; public prefixes are `/posttube/watch` (no trailing slash, so it matches any path with that prefix), `/posttube/channel/` and `/tube/` (`src/middleware.ts:47,105,112,126`). `isPublicPath` also matches the bare prefix, so `/posttube/channel` (own channel) is public too (`:168-178`).

| Route | File | Sign-in |
|---|---|---|
| `/posttube` home | `src/app/posttube/page.tsx:3` (layout with mini-player `layout.tsx:17`) | yes |
| `/posttube/watch?v=&list=`, `/posttube/watch/[videoId]?list&t` | `watch/page.tsx:9-14`, `watch/[videoId]/page.tsx:19,28` → `WatchPage.tsx` | no |
| `/posttube/channel`, `/posttube/channel/[handle]` | `channel/page.tsx:18`, `channel/[handle]/page.tsx` | no |
| `/posttube/channel/[handle]/feed.xml` | `feed.xml/route.ts:106,110`; builder `features/posttube/feed/rss.ts:395-450` (50 items) | no |
| `/posttube/history`, `/queue`, `/loved`, `/playlists[/id]`, `/subscriptions`, `/search`, `/trending`, `/topics[/slug]` | `src/app/posttube/*` | yes |
| `/posttube/live`, `/live/[streamId]` (replay link `LiveWatchPage` :228-240) | `live/*` | yes |
| `/posttube/upload?type=long` (default long, `ChannelGate`) | `upload/page.tsx:8-36` → `features/upload/UploadStudio.tsx`, `useUploadStudio.ts` | yes |
| `/posttube/uploads`, `/scheduled`, `/offline`, `/hub/*` (library, insights, captions, conversations, live, preferences, sounds) | `src/app/posttube/*` | yes |
| `/credits/requests` | `src/app/credits/requests/page.tsx` | yes |
| sitemap | `src/app/sitemap.ts:5-13` lists the site root only | — |

API clients: `features/posttube/data/posttubeApi.ts` (channels :79-152, feeds :262-352, trending :313, by-author :328, progress and history :361-422, series :430, schedule :460-466, playlists :497-547, subtitles :554,623, renditions :572, video tools :592-611), `watch/watchApi.ts` (tune :247, storyboard :282-286, end screens and cards :630-656), `library/libraryApi.ts:148-212`, `hub/hubApi.ts` (uploads :242-366, post edit :575-632, chapters :701-710, end screens :800-921, captions :1168-1185, private shares :1634-1639), `discovery/discoveryApi.ts` (search :477-487, live :519-541), `credits/creditsApi.ts:34-79`, `upload/studioApi.ts:87-88,251-274`, `lib/mediaUpload.ts:51,81`, `lib/resumableUpload.ts:54-65`.

| Player and state | Fact | Evidence |
|---|---|---|
| Player | `features/posttube/components/TubePlayer.tsx` (1226 lines); hls.js first, native HLS only without MSE, MP4 fallback | `:340-379` |
| Quality, speed | `pickHlsLevel` (auto or `NNNp`); speeds 0.25, 1, 1.25, 1.5, 2 | `features/reels/playback/playerPrefs.ts:22,78-81,115`, `TubePlayer.tsx:366-410` |
| Seek | `seekBy`, `seekTo`, `seekPercent`; storyboard previews and chapter ticks | `TubePlayer.tsx:63,630-706`, `features/posttube/playerKeys.ts` |
| Captions | text tracks from `GET /v1/subtitles/:mediaId` | `TubePlayer.tsx:416-424` |
| Resume and progress | every 10 s while playing, forced on pause and unmount, skipped under 1 s of movement; **nothing saved signed out**; resume skipped at ≥95%, completed at 90% (Android uses 95%) | `features/posttube/model.ts:38-42`, `WatchPage.tsx:288-327` |
| History pause | **client-only** `localStorage` `posttube_history_paused_v1` | `library/historyPause.ts:29` |
| Telemetry | `useVideoTracker`: play_start, milestones, heartbeat 5 s, play_end; batched to `/v1/analytics/events` every 10 s or 50 events | `src/hooks/useVideoTracker.ts:15-22,32,76`, `WatchPage.tsx:274-282` |
| Upload studio | long steps `video, details, audience, engage, enrich, publish` (+ `credits` when the topic takes credits); fields in `types.ts:22-136`; title ≤100 required, description ≤2200, ≤30 hashtags; visibility default public | `features/upload/tokens.ts:23,33-48`, `validation.ts:76-96`, `types.ts:181` |
| Publish order | upload video (resumable at ≥20 MB, server chunk size, 5 attempts), cover, subtitles (failure tolerated), then draft publish or `POST /v1/posts`, then trim, category, cover-frame, age restriction PATCH, series and chapters | `useUploadStudio.ts:551-680`, `lib/resumableUpload.ts:20-94` |
| Frame picker | canvas capture from `/serve[/720p]`, uploaded as an image (max width 1280) | `hub/coverFrame.ts:24-51` |

## 10. Tests and CI

| Layer | Count | CI job |
|---|---|---|
| Go unit, matching long-video terms (`long_video`, tube, playlist, `watch_progress`, `video_series`, chapters) | post-service 87, feed 26, media 16, search 17, analytics 8, user 1, notification 12 | `.github/workflows/go-ci.yml` (`scripts/verify/go.sh --full`) |
| Go integration (`//go:build integration`), same terms | post-service 33 (39 with credits, subscriptions, rss), media 1, search 3, analytics 9; feed 0 | `integration-all.yml` from `scripts/ci/integration-services.yaml` (post `post_it_test` with `VIDEO_SERIES_POSTGRES_DSN`, `CREDITS_POSTGRES_DSN`, `MEMBERS_POSTGRES_DSN`, :41-53; media :60-70; search wave 2 :217-222; analytics :191-199). feed-service is not in the manifest. Tests that need Kafka, MinIO or OpenSearch are skipped |
| Named examples | `post-service/internal/http/mtube_routes_test.go`, `playlist_routes_test.go`, `tube_audit_*`, `tube_decisions_integration_test.go`, `video_series_integration_test.go`, `watch_progress_test.go`, `watch_history_integration_test.go`, `chapters_test.go`, `channel_subscriptions_integration_test.go`; `feed-service/…/longvideo_test.go`, `watch_subscribed_test.go`; `search-service/…/tube_search_test.go` | as above |
| End to end | **no tube, playlist, progress or long-video upload flow** in `Architecture/tools/integration`; only creator-fund tests seed `long_video` rows | `e2e.yml` |
| Dev smoke | `health.longvideo` = `GET /v1/posts/trending?content_type=long_video` only; the upload and HLS steps use a 2 s flick | `scripts/smoke/dev-smoke.mjs:173,429-431,660-685`; run by hand |
| Android unit | `feature/tube/src/test` 34 test files (12 in `ui/watch`); plus `core/feed` (channels, chapters, credits, library, history, offline 7), `core/media` (HlsRefresh, ResumableUploader, VideoSizeCap, offline), `feature/post/createhub` (6) | `android-native.yml` (`scripts/verify/android.sh --full`, `testDebugUnitTest`) |
| Android instrumented | none (no `src/androidTest`) | none |
| Web unit | 42 files in `src/features/posttube/**/__tests__`, 2 in `src/app/posttube`, 12 in `features/upload/__tests__`; 93 of 371 files touch long video | **none**: no `.github` in `pb-echo`, no `test` script in `package.json:5-10`; `scripts/verify.sh` is run by hand |
| Load | none for long video | none |

## 11. Deployment manifests

| Item | Fact | Evidence |
|---|---|---|
| Chart | `charts/atpost-service`: deployment, worker-deployment, hpa, pdb, migration-job, externalsecret, ingress, webhook-ingress, networkpolicy, prometheusrule, service, serviceaccount, servicemonitor. No CronJob | `charts/atpost-service/templates/` |
| Long-class worker | **one worker Deployment per service**; short and long loops share every pod; a dedicated long Deployment needs a second chart worker (values comment) | `worker-deployment.yaml:32`, `media-service/values-prod.yaml:171-176` |
| Worker shape | `Recreate`, no readiness probe, no HPA (HPA targets the server only), scratch `emptyDir` | `worker-deployment.yaml:56,82,118`, `hpa.yaml:9-12` |
| PDB | selector `name` + `instance` also matches worker pods, so `minAvailable` can be met by workers alone | `pdb.yaml:14-16`, `_helpers.tpl:29-32`, `values-azure-prod.yaml:47-49` |
| MSK | `media.events.long` missing; auto-create off | section 4 |
| Bucket and CDN | `infra/terraform/modules/media/main.tf` | section 6 |
| Web | `deploy/web/app/values-prod.yaml:77` carries `FEED_MEDIA_BASE_URL`; `build-web.yml` builds the image on manual dispatch | reels map 7 |
| Jobs | `deploy/jobs/scylla-schema`, `timeline-maintenance`: one-off Jobs, nothing tube-specific | `deploy/jobs/` |

## 12. Open questions for the review tracks

Focus areas from the brief are marked [multipart], [visibility], [unlisted], [progress], [views], [quota].

1. **[quota] No per-user storage or transcode quota.** The only control is 10 uploads per hour and 30 per day, fail-open on Redis, with up to 16 GiB each in prod and a transcode budget of up to 48 h per job (`processing/limits.go:158`). Is a byte or minute quota required for launch?
2. **`media.events.long` in MSK.** The topic is absent and auto-create is off. Check how the kafka-go writer behaves on an unknown topic: does it fail fast (then long videos transcode in place on the short loop and block shorts for hours), or does it retry and stall the short queue? And should the topic be added to `topics-job/variables.tf`?
3. **[visibility] Segment-level enforcement.** Manifests are gated per request; segments are CloudFront-signed for up to 12 h. A video made private, deleted, taken down or copyright-blocked stays playable to anyone holding a manifest until the signatures expire, and no CDN invalidation exists. Is that acceptable for long video?
4. **[unlisted] Exclusion coverage.** Exclusion is confirmed for feed hydration (allowlist, `feed-service/internal/service/listingvisibility.go:35-68`), search (`SearchEligible` public + approved, `shared/events/events.go:871-881`), RSS (public only via `anonymousMayAccessPost`, `post-service/internal/service/media_access.go:161-184`), channel listings (`unlisted_listing.go`), playlists and series. The sitemap lists no videos. Not traced: `/v1/posts/trending?content_type=long_video`, `/v1/posts/categories`, topic pages, creator `appearances`, end-screen "latest" and "popular" targets, and `/v1/posts/batch`, whose `batchVisibilityAllowsEdges` admits `unlisted` (`service/batch_visibility.go:36`).
5. **[unlisted] Notification fan-out** uses a denylist (`private`, `unlisted`). An empty `review_status` counts as approved at delivery (`notification-service/internal/service/fanout_eligibility.go:202`). Members-only and `followers`-only nuances are not checked. Is notifying non-members about a members-only upload intended?
6. **Members-only in listings.** Feeds list members-only videos with a redacted body (`feed-service/internal/service/hydration.go:171-185`). post-service `EligibilityRow.Eligible()` ignores the tier (`store/postgres/search_eligibility.go:221`); this is latent while it has no caller. The manifest and segment path for a members-only video needs a Stage 2 read.
7. **[progress] Watch-progress writes.** Only the edge limit applies; each write does a readability check plus a DB upsert plus a Redis mirror. Clients write every 10 s, so cost scales with concurrent viewers. No retention sweeper exists (Redis mirror TTL 90 d; DB rows forever). `client_sent_at` ordering protects against reordering, but is it clamped against the future (`clampProgressSentAt`)?
8. **[progress] History pause is client-only** (web localStorage, Android DataStore). It does not sync across devices, and the server keeps recording from any other client. The completion threshold differs: web 90%, Android and server 95% or 90% (`video_series_handler.go:1068`). Is a server-side pause setting required?
9. **Channel ownership is split.** `channels` and `channel_subscriptions` are created by both user-service and post-service migrations against what may be the same database or different ones. User-service still serves channel CRUD routes behind a prefix the gateway sends to post-service. Which store is authoritative, and are the user-service routes reachable?
10. **[views] View counting.** The rule: long-form views need ≥30 s, or ≥50% under 60 s, with server-measured duration; dedup is on `client_event_id`; there is a cap of 1 per viewer per day in the daily rollup only; events older than 24 h are refused. Open: anonymous viewers (the web watch page is public, but the tracker needs a user?), multi-tab and multi-device inflation, staging and Azure counting on `play_end` while prod uses `sessions`, and search `view_count` changing only on reindex.
11. **[multipart] Abandoned uploads.** The app aborts expired sessions after 24 h (best-effort, 200 per 10 min), and the bucket aborts after 7 d. Does a 24 h TTL suit a 16 GiB upload on a slow link, given that both clients resume only within a session? Is an abort failure retried or left to the lifecycle rule? Are the parts of an `aborted` session (user cancel) aborted at once?
12. **Captions parity.** Android plays no network captions and uploads none; web uploads subtitle files and tolerates their failure. `GET /v1/subtitles/:mediaId` has no route-level auth: does it go through the delivery gate for private and members-only videos?
13. **Unlisted at create on Android** is missing (Public, Followers, Private only), while web offers it. Is that a parity gap or intended?
14. **End-screen and card impression and click counters** (`handler.go:414-417`) appear writable by anyone with no dedup. Check the auth and whether creator stats can be inflated.
15. **Signed-out playback paths.** The web `/posttube/watch` and channel pages are public, but `/v1/feed/*` needs a user. Check what related, Up next and the subscribe button do signed out, and that the bare `/posttube/channel` being public is intended.
16. **Live replays.** VODs are created unlisted with `source='live'`. When the owner makes one public, does the fan-out fire (PostCreated is not re-emitted) and does search index it (through PostSearchEligibilityChanged)?
17. **Edit after publish.** A visibility or tier change through `PATCH /v1/posts/:id`: is it propagated to feed timelines (fan-out skips non-listable posts at write time, `feed.go:1204`), to the hydrate cache (5 min), to search, and to already-signed URLs (question 3)?
18. **Scheduled publish.** `anonymousMayAccessPost` refuses any `publish_at`. Is `publish_at` cleared on publish, so that RSS and media access open once it fires (`service/schedule.go:457`)?
19. **Two series systems** (`/v1/series` flick series and `/v1/video-series`). Both are now routed; confirm the flick-series routes apply the same visibility rules as `series_visibility.go`.
20. **Ops gaps for long transcodes.** No worker HPA, readiness probe or transcode lag or quarantine alert; the PDB counts workers; Azure prod has the scanner off. A 4K, 4 h encode on 50 Gi scratch (3x original guard) needs a capacity read.
21. **Offline copies on Android are unencrypted**, and expiry is enforced client-side. Does the founder rule "viewers never get a file" hold on rooted devices?
22. **No end-to-end, smoke or load coverage** of a long-video upload, transcode on the long class, playback, progress or subscription feed; the web tree has no CI at all.
