# Long video (PostTube / MTube): Track A, functional completeness

- **Audit date:** 2026-11-17. Read-only. Trees as in `00-map.md` (backend and Android `C:\workspace\modernsmapp` @ `58f602b6d` with a dirty working tree, web `C:\workspace\wt\pb-echo` @ `ca96921`).
- **What was run:** `go test ./internal/... -count=1 -run 'Playlist|WatchProgress|WatchHistory|Tube|MTube|VideoSeries|Chapter|EndScreen|Channel|Subscri|Schedule|PostEdit'` in `Architecture/services/post-service`: every package `ok` (VERIFIED-RUN that those unit tests pass; they are unit tests, not a journey). Nothing else was executed: no Android build, no web tests, no Docker, no devices.
- **Paths:** backend paths are relative to `Architecture/services/`; web paths to `C:\workspace\wt\pb-echo\src`; Android paths to `mobile/android/`, with `T/` = `feature/tube/src/main/kotlin/com/us/android/feature/tube/` and `CH/` = `feature/post/src/main/kotlin/com/us/android/feature/post/createhub/`.
- **Rating rule:** a rating of COMPLETE or PARTIAL means the path was followed in code from the client call to the server store; a screen or route that was only located, not followed, is UNVERIFIED.

## 1. Feature ratings

| Feature | Server | Android | Web | Evidence and notes |
|---|---|---|---|---|
| Chunked resumable upload of large files | COMPLETE (READ) | COMPLETE (READ) | PARTIAL (READ) | Server: 5 to 64 MiB parts, at most 10,000 parts, fixed 24 h TTL (`media-service/internal/service/resumable.go:19-38`); size cap refused at init with 413 `VIDEO_TOO_LARGE` (`internal/http/resumable_handler.go:53-75`). Android: session saved the moment `init` answers and resumed after process death (`core/media/.../upload/ResumableUploader.kt:44-60`), picked file copied out of the cache (`CH/ReelPublishStore.kt:306-336`). Web: resumable at 20 MB or more, 5 tries per part, then the whole upload throws; the plan is never stored, so a reload or a long outage restarts from byte 0 (`lib/resumableUpload.ts:20-24,98-142`; reels-39). 24 h TTL vs slow links: LV-A-13 |
| Processing into a quality ladder | PARTIAL (READ) | n/a | n/a | MP4 360/480/720/1080/4K up to the source height (`media-service/cmd/worker/main.go:741-752`); HLS 360/720/1080 only (`internal/processing/video.go:21-25,49-61`). The 4K rendition is encoded and never played (LV-A-8). The long class depends on a topic absent from MSK (reels-3). Duration cap of 4 h is enforced only after upload (LV-A-6) |
| Thumbnails, generated | PARTIAL (READ) | n/a | n/a | The only generated still is a 150x150 letter-boxed square at 25% of the duration (`internal/processing/video.go:136-146,391-396`); 3 suggested frames on request (`internal/http/studio_handler.go:34,235-237`). LV-A-14 |
| Thumbnails, custom | COMPLETE (READ) | COMPLETE (READ) | COMPLETE (READ) | Server cover claim on edit (`post-service/internal/service/post_edit.go:240-247`), `SetCoverFrame` (`service/post_video_tools.go:136-170`). Android: a cover is part of every publish and a cover failure fails the post (`CH/ReelPublishPipeline.kt:64-66,236-240`). Web: frame or custom image uploaded before create, then `cover-frame` (`features/upload/useUploadStudio.ts:569-582,653-662`) |
| Title, description, tags | COMPLETE (READ) | COMPLETE (READ) | COMPLETE (READ) | Web title required, at most 100, description at most 2200, at most 30 hashtags (`features/upload/validation.ts:74-88`). Edits after publish do not reach search: LV-A-5 |
| Category | PARTIAL (READ) | COMPLETE (READ) | COMPLETE (READ) | Required for long video on Android (`CH/ReelSurface.kt:602-603`, `CH/ReelPublishViewModel.kt:263-267`) and web (`features/upload/validation.ts:95-96`); the server does not require it (LV-A-10) |
| Captions or subtitles | COMPLETE (READ) | MISSING (READ) | COMPLETE (READ) | Server tracks behind the delivery gate (`media-service/internal/http/clips_handler.go:324-347`). Android plays captions only from offline copies and uploads none (`T/ui/watch/WatchViewModel.kt:905-930`). Web uploads a file at publish (failure tolerated, `useUploadStudio.ts:584-593`) and shows text tracks (`features/posttube/components/TubePlayer.tsx:416-424`). LV-A-2 |
| Visibility public, unlisted, private | COMPLETE (READ) | PARTIAL (READ) | COMPLETE (READ, create list from map) | Server edit allowlist (`post-service/internal/service/post_edit.go:372-378,626`). Android create offers Public, Followers, Private only (`CH/ReelSurface.kt:1425-1429`; `feature/post/.../data/dto/CreatePostDtos.kt:360-372` "`unlisted` stays out"); Android edit offers Unlisted (`core/feed/.../data/PostEditForm.kt:27-31`). LV-A-3 |
| Scheduled publish | COMPLETE (READ) | UNVERIFIED | UNVERIFIED | Server: standing and copyright checks at flip time, `publish_at` cleared, `PostCreated` built at publish (`post-service/internal/service/schedule.go:384-470`, `store/postgres/scheduled.go:131`). Clients: `PATCH /v1/posts/:id/schedule` and `publishDraft(schedule_at)` located (`core/feed/.../data/VideoFeedApi.kt:141`, `useUploadStudio.ts:600-601`), Scheduled screens exist (`T/ui/scheduled/ScheduledPostsScreen.kt`); not followed |
| Edit after publish | COMPLETE (READ) | PARTIAL (READ) | UNVERIFIED | Server: one transaction, audit row, search bump and `PostVisibilityChanged` on a visibility change, offline copies revoked on private (`store/postgres/post_edit.go:130-322`), but text edits never reach search (LV-A-5). Android: owner Edit sheet from the watch More menu (`T/ui/watch/WatchScreen.kt:402-403,435-440`); chapters, series, end screens, trim and captions not editable (LV-A-4). Web hub edit client located (`features/posttube/hub/hubApi.ts:575-632`), not followed |
| Channel page and video listing | COMPLETE (READ, map) | UNVERIFIED | UNVERIFIED | `GET /v1/posts/by-author/:id?type=long_video`, unlisted listed to the author only (`post-service/internal/service/post_reads.go:251`, map). Screens exist (`T/ui/channel/ChannelScreen.kt`, `app/posttube/channel/[handle]/page.tsx`) |
| Playlists (create, reorder) | COMPLETE (READ) | COMPLETE (READ) | COMPLETE (READ, located) | Routes incl. reorder `PATCH /v1/playlists/:id/items/:postId` (`post-service/internal/http/handler.go:384-399`). Android create, edit, move top/up/down/bottom (`core/feed/.../data/VideoLibraryApi.kt:69-114`, `T/ui/collections/CollectionEditing.kt:47-120`). Web drag reorder (`features/posttube/library/reorder.ts`) |
| Playlists (share) | n/a | MISSING (READ) | MISSING (READ) | No share control on either client; the web playlist page needs sign-in. LV-A-7 |
| Subscriptions and subscriptions feed | COMPLETE (READ) | COMPLETE (READ, client) | COMPLETE (READ, client) | `subscribed_only` honoured on `/v1/feed/videos` and `/v1/feed/watch` (`feed-service/internal/http/handler.go:392,462,541-551`); Android `VideoFeedRepository.kt:33-38`; web `features/posttube/discovery/discoveryApi.ts:224` |
| Notifications of new uploads | BROKEN (READ) | n/a | n/a | Normal long uploads are created while processing and the fan-out drops them for good. LV-A-1 |
| Player: quality | PARTIAL (READ) | PARTIAL (map) | COMPLETE (map) | Top HLS rung 1080p (LV-A-8). Android MP4 and dubs offer Auto only (`T/ui/watch/PlayerQuality.kt`, map 9.1) |
| Player: speed, seek | n/a | COMPLETE (READ) | COMPLETE (READ) | Android `WatchViewModel.kt:870-903`; web `features/reels/playback/playerPrefs.ts:16-25`, `TubePlayer.tsx:407-414` |
| Player: captions | n/a | MISSING | COMPLETE (READ) | LV-A-2 |
| Resume position across devices | COMPLETE (READ) | COMPLETE (READ) | UNVERIFIED (map) | Server writes the DB first, Redis mirror best effort, a Redis hit is still answered from the DB (`post-service/internal/service/watch_progress.go:43-109`); readability gate on write and read (`internal/http/video_series_handler.go:1009-1013,1101-1104`). Android `T/data/WatchProgressRepository.kt:35-44`, `T/ui/watch/ResumeRule.kt:13-24`. Threshold mismatch LV-A-12 |
| Watch history, clear | COMPLETE (READ) | UNVERIFIED (map) | UNVERIFIED (located) | `DELETE /v1/videos/history` clears DB rows and mirrors (`video_series_handler.go:1159-1173`); clients `core/feed/.../data/WatchHistoryApi.kt:33-37`, `features/posttube/data/posttubeApi.ts:389-396` |
| Watch history, pause | MISSING | PARTIAL (READ) | PARTIAL (READ) | Client-only flags (`T/data/WatchProgressRepository.kt:42-43`, `features/posttube/library/historyPause.ts:29-40`). LV-A-11 |
| Comments, replies, pinning, likes | COMPLETE (READ) | UNVERIFIED (shared sheet) | UNVERIFIED | Pin is post-author only (`post-service/internal/service/comment_creator.go:108,123`); routes `handler.go:322-343`. Shared with reels (reels map 3.2) |
| Related videos | COMPLETE (READ) for signed in | UNVERIFIED | PARTIAL (READ) | Signed-out viewers of the public watch page get none. LV-A-9 |
| Search | PARTIAL (READ) | UNVERIFIED | UNVERIFIED | Eligibility and removal paths are sound (`search-service/internal/events/eligibility.go:37-120`); title and description edits are never re-indexed (LV-A-5) |
| Series | COMPLETE (READ, routes + tests) | PARTIAL (READ) | UNVERIFIED (located) | Server create, edit, delete, episodes (`handler.go:362-379`). Android reads `GET /v1/posts/:id/series` only (`T/data/VideoSeriesApi.kt:17`) and auto-advances; cannot create or manage. Web `features/upload/studioApi.ts:204-274` |
| Chapters | COMPLETE (READ) | PARTIAL (READ) | UNVERIFIED (located) | Android shows chapters from the detail (`core/model/.../VideoChapters.kt:3-12`) but cannot save them; web saves at publish (`useUploadStudio.ts:690-694`) |
| Credits | COMPLETE (map, tests pass) | COMPLETE (map) | UNVERIFIED | Android accept, decline, remove (`core/feed/.../credits/CreditsApi.kt:42-49`) and credits at upload (map 9.1) |
| End screens and cards | COMPLETE (map) | PARTIAL (READ) | UNVERIFIED (located) | Android reads and records impressions (`T/data/WatchElements.kt:40-47`); no editor ("the end-screen editor stay[s] on the web", `T/ui/hub/CreatorHubScreen.kt:102-103`) |

## 2. Journeys

None of these can be executed under the audit rules. Each was walked through the code; "a real run needs" says what would confirm it.

1. **Upload a long video, publish it, play it at each quality on a second account.** Android uploads resumably, creates the post at once (`CH/ReelPublishPipeline.kt:44-52,247-265`); web the same (`useUploadStudio.ts:557-622`). post-service hides the post from everyone but the author until every asset is `ready` + `passed` (`post-service/internal/service/processing.go:12-21,90-95`). The long transcode is forwarded to `media.events.long`, which prod MSK lacks, so it runs on the short loop (reels-3; re-checked `infra/terraform/modules/msk/topics-job/variables.tf:39,58`, `media-service/cmd/worker/transcode_classes.go:210-220`). The second account can pick 360, 720 and 1080 on HLS; 480 and 4K exist only as MP4 that no player requests (LV-A-8). Web production HLS may fail on the CloudFront credentials policy (reels-2). **Breaks or weakens at:** reels-3, reels-2, LV-A-8. **A real run needs:** a 20-minute 4K landscape upload on staging with the MSK topic list, then a second account switching each rung and capturing the rendition requested.
2. **Leave mid-video and resume on another device.** Android saves every 10 s and on pause or background (map 9.1, `WatchViewModel.kt:416-462`); the server stores DB first and reads the DB even on a Redis hit (`watch_progress.go:43-109`); the second device reads `GET /v1/videos/:id/progress` beside prepare. Works, except between 90% and 95% (LV-A-12) and when either device has history paused (LV-A-11: the paused device does not save, so the other resumes from an older point). **A real run needs:** web and Android signed in as one account, stop at 40% on one, open on the other within 10 s and after 60 s.
3. **Switch a public video to private; an open player and a copied stream URL must stop.** The edit commits with a search removal, `PostVisibilityChanged` and offline-copy revocation (`store/postgres/post_edit.go:263-312`). New manifest requests are re-authorised and refused at once. Segment URLs already handed out stay valid for 2x the duration plus 15 min, up to 12 h (`media-service/internal/delivery/sign_cache.go:40-66`), so an open player, which holds the whole VOD playlist, plays to the end, and a copied segment URL keeps working: **fails** as specified (reels-17, unchanged). **A real run needs:** a 2-hour video, a player at minute 5, the owner switching to private, and `curl` on a copied manifest and a copied segment.
4. **Create a playlist, reorder it, share it.** Create and reorder work on both clients (section 1). **Share fails:** neither client has a share action, and `/posttube/playlists/[id]` is not on the web's public list (map 9.2; `middleware.ts:47,105,112,126`), so a recipient who is signed out meets a sign-in wall; only a hand-built `/posttube/watch?v=&list=` link is public. LV-A-7.
5. **Subscribe and receive the next upload.** The upload appears in the subscriber's Subscriptions feed once processing finishes (feed hydration hides it until then). **The notification does not arrive** for an ordinary upload: LV-A-1. A scheduled upload does notify, because `PostCreated` is emitted at publish time, after the transcode (`schedule.go:450-462`). **A real run needs:** accounts A and B, B subscribed with `notify_on=all`, A uploads a 15-minute video, then check `notification_fanout` rows and B's inbox.
6. **Kill the worker mid-transcode and confirm recovery.** On SIGTERM the worker kills ffmpeg (reels-56) and the `Recreate` rollout stops every pod (`charts/atpost-service/templates/worker-deployment.yaml:48-55`); the uncommitted Kafka message is redelivered and the job restarts from zero. A hung or dead job's lease goes stale after 10 min and the sweeper re-queues it, at most 3 times, then fails it (`media-service/internal/store/postgres/transcode_lease.go:20-76,155-169`, `cmd/worker/transcode_lease.go:30-46`); a failure rejects the post for good (reels-31). Recovery exists, but for a multi-hour 4K encode each restart costs the hours already spent. **A real run needs:** a 2-hour upload on staging, `kubectl delete pod` at 50%, then the `media_transcode_stall_reclaims_total` metric and the asset's final status.

## 3. Founder rules

| Rule | Result | Evidence |
|---|---|---|
| Android has every feature the web has | **FAIL** | Missing on Android: captions (playback and upload), Unlisted at create, chapter editing, series creation and management, end-screen and card editor, trim, bulk edit, private shares, playlist share (also missing on web). `T/ui/hub/CreatorHubScreen.kt:92-104` says editing, bulk edit and the end-screen editor "stay on the web". LV-A-2, LV-A-3, LV-A-4 |
| Upload flow has visibility and category | PASS on both clients (VERIFIED-READ); server does not enforce category (LV-A-10). Android visibility lacks Unlisted (LV-A-3) | `CH/ReelSurface.kt:602-603,1425-1429`; `features/upload/validation.ts:95-96` |
| Long videos have no length cap | **FAIL** (by design comment) | 4 h worker cap, checked after the full upload (`media-service/internal/processing/limits.go:52-57,93-95`). LV-A-6. No client-side length cap (map 9.1) |
| Every account has a default channel | PASS (VERIFIED-READ) | `UserRegistered` creates it (`post-service/internal/events/consumer.go:156-163,240-258`, wired at `cmd/server/main.go:370-372`); `/v1/channels/me` and long-video publish create a missing one (`service/channels.go:256-272,436-455`). The web `ChannelGate` still renders "First, your channel" if `/me` answers with no channel (`features/posttube/channelGate/ChannelGate.tsx:83-89`); reachable only when that ensure fails |

## 4. Inherited from reels (re-checked, apply unchanged)

- reels-1: post-service can leave a video post `pending` for ever; same consumer path for long video (`post-service/internal/consumers/media.go:353-398`).
- reels-2: web HLS credentials vs the CloudFront policy; same player family (`TubePlayer.tsx:340-379`).
- reels-3: `media.events.long` is missing from MSK; long transcodes run on, and block, the short loop (re-checked above).
- reels-4: two short jobs at once platform-wide, no autoscaling; a multi-hour long job holds a slot for hours.
- reels-10: no test uploads, transcodes and plays a video end to end (map 10: none for long video either).
- reels-13: the server half of resumable upload has no test; long video is its main user.
- reels-17: privatised or deleted videos keep playing from signed segment URLs; 12 h for long video (`sign_cache.go:59-66`).
- reels-30: upload notifications dropped while pending or processing; for long video this is the normal case, raised to P1 as LV-A-1.
- reels-31: a transcode failure rejects the post for good.
- reels-39: web uploads cannot resume after a reload (re-checked `lib/resumableUpload.ts`: no stored plan).
- reels-55: resumable parts travel through the gateway and media-service, not presigned to S3.
- reels-56 and reels-57: SIGTERM kills ffmpeg; nothing restarts a hung worker (journey 6).
- reels-83: Android cannot add captions to a reel; the long-video gap is wider (LV-A-2).
- reels-92 and reels-93: multipart rows dropped without abort on asset delete; a late chunk can reopen a completed session.
- reels-98: scheduled posts retry for ever on a persistent error.

## 5. Findings

### LV-A-1

- **ID:** LV-A-1
- **Title:** Subscribers are never notified of an ordinary long-video upload, because the post is created while it is still processing and the fan-out reads the processing post as deleted and skips it for good.
- **Severity:** P1
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - Both clients create the post right after the bytes are confirmed, without waiting for the transcode: Android `CH/ReelPublishPipeline.kt:44-52,247-257`; web `features/upload/useUploadStudio.ts:557-567,595-622`.
  - post-service hides a processing post from every viewer except its author, and an anonymous reader is never the author (`post-service/internal/service/processing.go:12-21,90-95`, applied on the direct read at `service/post_reads.go:42-50`); a pending review status is hidden the same way (`post_reads.go:32-40`).
  - The notification job is enqueued on `PostCreated` (`notification-service/internal/events/consumer.go:267`, `internal/events/subscriber_fanout.go:45-60`). Delivery reads `GET /v1/posts/:id` with only the internal key (`internal/service/fanout_eligibility.go:180-203,281-284`); a 404 sets `exists=false`, and `!exists || !approved` is a terminal skip (`:82-84`). The drain runs every 5 s (map 8); a long transcode takes minutes to hours.
  - Readiness is never announced again: the approval flip emits only the search eligibility event (`post-service/internal/store/postgres/posts.go:1540-1551`), and the visibility edit emits `PostVisibilityChanged`, not `PostCreated` (`store/postgres/post_edit.go:263-289`), so an upload first published unlisted and later made public, and a live replay (created unlisted, map 1.1) made public, never notify either.
  - Scheduled uploads are the exception: `PostCreated` is emitted at publish time (`post-service/internal/service/schedule.go:450-462`).
- **How to reproduce:** B subscribes to A's channel with `notify_on=all`. A uploads a 10-minute video from either client and publishes at once. Within 5 s the fan-out job reads the post, gets 404, and marks every recipient skipped; B receives nothing after the video becomes playable.
- **Impact:** The "subscribe and get the next upload" loop, the core retention mechanic of a long-video product, does not work for almost every upload. Creators see subscribers but no traffic from them.
- **Fix:** Smallest: in post-service, when the media consumer flips the last asset to ready and approved (`consumers/media.go` `finalizeReviewGate`), emit a `PostReady` (or re-emit `PostCreated` with a flag) once per post; have notification-service enqueue on that event, deduplicated on post id. Do the same on the first transition of a post to `public` from `unlisted` or `private`. Alternatively, have the fan-out treat "processing or pending" as retry-later rather than terminal, using an internal state read that distinguishes it from deleted.
- **Acceptance:** A notification-service test where the first state read says processing and a later one says public and approved delivers exactly once. A post-service test that the ready flip and an unlisted-to-public edit each write one fan-out trigger event.
- **Effort:** M

### LV-A-2

- **ID:** LV-A-2
- **Title:** Android plays no captions on network playback and cannot upload or manage caption tracks, while the web does both.
- **Severity:** P2
- **Category:** functional (parity, accessibility)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `T/ui/watch/WatchViewModel.kt:905-930` ("empty for a video played from the network, which carries none on Android yet"); no Android call to `/v1/subtitles` anywhere (search across `mobile/android` for `subtitles` and `captions` endpoints found none). Web: upload at publish (`features/upload/useUploadStudio.ts:584-593`), text tracks in the player (`features/posttube/components/TubePlayer.tsx:416-424`), hub captions page (map 9.2). Server tracks are served behind the delivery gate (`media-service/internal/http/clips_handler.go:324-347`).
- **How to reproduce:** Upload a video with an SRT on the web; open it on Android: no CC control while streaming.
- **Impact:** Deaf and hard-of-hearing viewers on the main client cannot watch; breaks the founder rule that Android has every web feature.
- **Fix:** Android: fetch `GET /v1/subtitles/:mediaId` with the playback, add each published track as a Media3 `SubtitleConfiguration` pointing at `/v1/subtitles/:mediaId/track/:language.vtt`, reuse the existing `captions` flow and the settings sheet. Upload can follow later.
- **Acceptance:** A ViewModel test that a network playback with two server tracks offers both; a manual check that a WebVTT track renders.
- **Effort:** M

### LV-A-3

- **ID:** LV-A-3
- **Title:** Android cannot publish a long video as Unlisted, so a creator who wants an unlisted video must publish it publicly first and change it afterwards.
- **Severity:** P2
- **Category:** functional (parity)
- **Evidence level:** VERIFIED-READ
- **Evidence:** create options Public, Followers, Private (`CH/ReelSurface.kt:1425-1429`); the create allowlist excludes `unlisted` on purpose (`feature/post/src/main/kotlin/com/us/android/feature/post/data/dto/CreatePostDtos.kt:360-372`). The Edit sheet does offer Unlisted (`core/feed/src/main/kotlin/com/us/android/core/feed/data/PostEditForm.kt:27-31`). The web create offers it (map 9.2, `features/upload/components/StudioSettings.tsx:30-36`). The server accepts it at create and edit (`post-service/internal/service/post_edit.go:626`).
- **How to reproduce:** Android, Create, Video: the Audience sheet lists three choices, none Unlisted.
- **Impact:** The workaround makes the video public in followers' feeds, the trending and category lists, and search until the edit lands (timeline rows are written for the public post, `store/postgres/post_edit.go:267-289`).
- **Fix:** Add `VISIBILITY_UNLISTED` to `SupportedAudience` and `AudienceOptions` for the Video surface only, with the web's one-line explanation.
- **Acceptance:** A create-hub test that the Video surface offers Unlisted and sends `visibility=unlisted`; the Reel surface does not.
- **Effort:** S

### LV-A-4

- **ID:** LV-A-4
- **Title:** Android has no creator tools for chapters, series, end screens and cards, trim, private shares or bulk edit, all of which the web has.
- **Severity:** P2
- **Category:** functional (parity)
- **Evidence level:** VERIFIED-READ
- **Evidence:** the Android Creator Hub is read-only by design: "Editing, bulk edit, the end-screen editor and payouts stay on the web" (`T/ui/hub/CreatorHubScreen.kt:92-104`, `CreatorHubViewModel.kt:94-95`). The Android endpoint inventory (search of `@POST|@PATCH|@PUT|@DELETE` across `feature/tube`, `core/feed`, `core/media`) has no call to `/chapters`, `/end-screens` or `/cards` writes, `/v1/video-series` writes, `/trim`, `/cover-frame`, `/private-shares` or `/v1/uploads/videos/bulk`; series is read only (`T/data/VideoSeriesApi.kt:17`). The web has each (`features/posttube/hub/hubApi.ts:575-921,1634-1639`, `features/upload/studioApi.ts:204-274`, `useUploadStudio.ts:636-694`).
- **How to reproduce:** On Android, try to add a chapter list, start a series or add an end screen to an uploaded video: no control exists.
- **Impact:** Breaks the founder rule ("Android is the super app"); Android-only creators cannot use features that drive watch time (end screens, series auto-advance).
- **Fix:** In order of value: series add-at-upload and manage (one picker plus three calls), chapters editor in the Edit sheet (text rows to `POST /v1/posts/:id/chapters`), then the end-screen editor. Bulk edit and trim last.
- **Acceptance:** Each tool has a ViewModel test against a fake API and appears in the Edit sheet or the publish flow.
- **Effort:** L

### LV-A-5

- **ID:** LV-A-5
- **Title:** Editing a published video's title, description, tags or category never updates search, which keeps serving the old title until a manual reindex.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** the edit emits the search eligibility event only for a visibility change, a credits change or a tier change (`post-service/internal/store/postgres/post_edit.go:253-266`; `creditSearchChanged` covers `credits`, `genres`, `release_year`, `production_house` only, `:473-480`). search-service changes a post document only on `PostCreated`, the eligibility event and deletion-type events; there is no post-updated case (`search-service/internal/events/consumer.go:439-1040`). The eligibility event carries the title and text and replaces the document whole (`search-service/internal/events/eligibility.go:97-117`), so a bump would fix it. The tube reindex is manual only (map 8).
- **How to reproduce:** Publish "Draft title", wait for indexing, edit the title to "Final title"; `GET /v1/search/posts?type=videos&q=Final` finds nothing, `q=Draft` still finds it.
- **Impact:** Creators fixing a typo or retitling for discovery get no effect in search; search shows stale titles that differ from the watch page.
- **Fix:** Add `title`, `text`, `tags`, `hashtags` and `category` to the set that triggers `BumpSearchRevAndEmitTx` in `UpdatePostFields`.
- **Acceptance:** A store test that a title-only edit writes one `PostSearchEligibilityChanged` outbox row carrying the new title.
- **Effort:** S

### LV-A-6

- **ID:** LV-A-6
- **Title:** A video longer than 4 hours is accepted, uploaded in full (up to 16 GiB), and only then refused by the worker, contrary to the founder's no-length-cap rule.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** `DefaultMaxVideoDurationSeconds = 4 * 60 * 60` with the comment that the founder said there is no limit (`media-service/internal/processing/limits.go:52-57`); enforced by the worker's probe (`:93-95`, env override `cmd/worker/transcode_guards.go:68`). Init knows only the byte count (`internal/http/resumable_handler.go:53-75`). Neither client checks duration (map 9.1 and 9.2: no client size or length cap). The failure rejects the post (reels-31).
- **How to reproduce:** Upload a 4 h 30 min recording: the upload completes, the post shows as processing to its author, then fails.
- **Impact:** Hours of upload wasted for streamers and event recordings; a stated product rule is not what ships.
- **Fix:** Take the decision to the founder. If 4 h stays, have both clients read the local duration and refuse over the cap before uploading, with the reason, and say "up to 4 hours" in the studio. If not, raise `MEDIA_MAX_VIDEO_DURATION_SEC` with a matching segment TTL cap (`sign_cache.go:59-62`) and job budget.
- **Acceptance:** A client test that a 4 h 1 min file is refused before `init`; or a worker test at the new cap.
- **Effort:** S

### LV-A-7

- **ID:** LV-A-7
- **Title:** Neither client can share a playlist, and the web playlist page requires sign-in, so a playlist cannot be sent to someone outside the app.
- **Severity:** P2
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** no share action in the Android collection screens (`T/ui/collections/*.kt`, search for "share" finds none) or the web library (`features/posttube/library/**`, same search). Middleware public prefixes are `/posttube/watch`, `/posttube/channel/` and `/tube/` only (map 9.2, `middleware.ts:47,105,112,126`), so `/posttube/playlists/[id]` redirects a signed-out visitor to sign-in. The server already serves `public` and `unlisted` playlists to anyone (`post-service/internal/service/playlists.go:56-60`), and `/posttube/watch?v=&list=` is public (`app/posttube/watch/page.tsx:9-14`).
- **How to reproduce:** Create a public playlist; look for Share on either client; open `/posttube/playlists/<id>` in a private window.
- **Impact:** The "share it" step of the playlist journey does not exist; unlisted playlists, whose whole purpose is link sharing, are unusable.
- **Fix:** Add Share (system share sheet on Android, copy link on web) that shares `/posttube/watch?v=<first item>&list=<id>`, which is already public; optionally add `/posttube/playlists/` to the public prefixes.
- **Acceptance:** A signed-out browser opens the shared link and plays the first item with the playlist panel.
- **Effort:** S

### LV-A-8

- **ID:** LV-A-8
- **Title:** The worker encodes a 4K MP4 for every 2160p upload, but no player requests it and the HLS ladder stops at 1080p.
- **Severity:** P3
- **Category:** functional (cost)
- **Evidence level:** VERIFIED-READ
- **Evidence:** MP4 jobs include `{"4k", 2160}` (`media-service/cmd/worker/main.go:741-752`); HLS rungs 360, 720, 1080 (`internal/processing/video.go:21-25`). No reference to a `4k` rendition or 2160 in the web player code or the Android tube and core modules (search).
- **How to reproduce:** Upload 3840x2160; the quality menu tops out at 1080p on both clients.
- **Impact:** The most expensive encode of the ladder is spent and stored for nothing; creators uploading 4K never see 4K.
- **Fix:** Either stop producing the 4K MP4, or add a 1440p and 2160p HLS rung for long-class sources at that height. Founder choice; stopping is the smaller change.
- **Acceptance:** A ladder test for a 2160p long-class source matches the chosen rule.
- **Effort:** S

### LV-A-9

- **ID:** LV-A-9
- **Title:** A signed-out viewer of a public watch page gets no related videos or Up next, because the related route requires a user.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** `GetRelatedVideos` answers 401 without `X-User-Id` (`feed-service/internal/http/related.go:90-95`); the web treats any failure as an empty page (`features/posttube/watch/watchApi.ts:260-266`); the watch page is public (map 9.2).
- **How to reproduce:** Open `/posttube/watch/<id>` in a private window: the Up next column is empty.
- **Impact:** Shared links lead nowhere after the first video; no session continues for visitors who are not signed in.
- **Fix:** Allow an empty viewer in `GetRelatedVideos`, ranked without viewer signals and limited to public, approved posts (the hydration allowlist already exists).
- **Acceptance:** A handler test that an anonymous request returns only public rows.
- **Effort:** S

### LV-A-10

- **ID:** LV-A-10
- **Title:** The server accepts a long video without a category, although both clients require one.
- **Severity:** P3
- **Category:** functional (data quality)
- **Evidence level:** VERIFIED-READ
- **Evidence:** the only server `binding:"required"` on category is the override route (`post-service/internal/http/video_tools_handler.go:147`); clients enforce it (`CH/ReelSurface.kt:602-603`, `features/upload/validation.ts:95-96`).
- **How to reproduce:** `POST /v1/posts` with `content_type=long_video` and no category: 201.
- **Impact:** Category chips and topic pages miss videos from older clients and API callers.
- **Fix:** Refuse `long_video` create without a valid category with 422 `CATEGORY_REQUIRED` (leave live replays, which are created server-side, out).
- **Acceptance:** A create handler test for the refusal and the live-replay exemption.
- **Effort:** S

### LV-A-11

- **ID:** LV-A-11
- **Title:** "Pause watch history" is stored only on the device where it was set, so other devices keep recording and the server has no setting.
- **Severity:** P3
- **Category:** functional (privacy expectation)
- **Evidence level:** VERIFIED-READ
- **Evidence:** Android DataStore flag checked before each save (`T/data/WatchProgressRepository.kt:42-43`); web `localStorage` key (`features/posttube/library/historyPause.ts:29-40`); no server pause field or route (`post-service/internal/http/handler.go:423-434`).
- **How to reproduce:** Pause history on the web; watch on Android; the web history shows the video.
- **Impact:** A viewer who paused history believes nothing is recorded; feeds still use the record.
- **Fix:** A `watch_history_paused` user setting in post-service that `SaveWatchProgress` honours, read by both clients.
- **Acceptance:** A handler test that a save is a no-op while paused; both clients read the server value.
- **Effort:** M

### LV-A-12

- **ID:** LV-A-12
- **Title:** "Finished" means 90% on the server and web but 95% on Android, so a video stopped between 90% and 95% leaves Continue watching while Android still resumes it near the end.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** VERIFIED-READ
- **Evidence:** server `pct >= 90.0` (`post-service/internal/http/video_series_handler.go:1061-1073`); web `COMPLETED_PERCENT = 90` (`features/posttube/model.ts:38-39`); Android `COMPLETION_FRACTION = 0.95` (`T/ui/watch/ResumeRule.kt:13-25`).
- **How to reproduce:** Stop at 92% on Android; the web Continue watching row is gone, and Android reopens at 92%.
- **Impact:** Inconsistent resume across devices for the end credits of long videos.
- **Fix:** Use one threshold (90%, the server's) in `ResumeRule.kt`.
- **Acceptance:** The Android table test uses 90%.
- **Effort:** S

### LV-A-13

- **ID:** LV-A-13
- **Title:** A resumable session expires 24 hours after init whatever its progress, so a multi-gigabyte upload on a slow link restarts from byte zero.
- **Severity:** P3
- **Category:** functional
- **Evidence level:** SUSPECTED (the TTL is read; whether the expiry is fixed from init or extended per part was not traced, and the slow-link case was not run)
- **Evidence:** `resumableUploadTTL = 24 * time.Hour` (`media-service/internal/service/resumable.go:38`); the Android uploader forgets an expired session and opens a new one (`ResumableUploader.kt:62-70` `discardSession`); the 16 GiB prod cap (map 7) at 1.5 Mbit/s takes about 24 h.
- **How to reproduce:** Throttle to 1 Mbit/s and upload 12 GiB from Android; after 24 h the session is refused and the progress resets.
- **Impact:** Creators on slow uplinks cannot publish their largest files.
- **Fix:** Extend `expires_at` on each accepted part (sliding TTL), keeping the bucket's 7-day abort rule as the backstop.
- **Acceptance:** A service test that a part accepted at hour 23 moves the expiry.
- **Effort:** S

### LV-A-14

- **ID:** LV-A-14
- **Title:** The only generated thumbnail is a 150x150 letter-boxed square, which is all a long video without a custom cover, such as a live replay, has to show.
- **Severity:** P3
- **Category:** functional (quality)
- **Evidence level:** SUSPECTED (generation read; which variant each client card shows for a cover-less long video was not traced, nor whether the live replay path sets a cover)
- **Evidence:** `ExtractThumbnail(..., 150)` with `pad` to a square (`media-service/internal/processing/video.go:136-146,391-396`); live replays are created server-side (`post-service/internal/service/live_vod.go:159`).
- **How to reproduce:** Make a live replay public and view it in a 16:9 grid.
- **Impact:** Blurry, boxed posters on channel pages and feeds.
- **Fix:** Generate a 1280-wide 16:9 poster for long-class sources in the same ffmpeg pass and prefer it over `thumb_150` when no cover is set.
- **Acceptance:** A worker test that a long-class transcode records the poster variant.
- **Effort:** S

## 6. Counts

| Severity | Count | Evidence levels |
|---|---|---|
| P0 | 0 | |
| P1 | 1 | VERIFIED-READ 1 |
| P2 | 6 | VERIFIED-READ 6 |
| P3 | 7 | VERIFIED-READ 5, SUSPECTED 2 |

## Not verified

- No journey was executed; section 2 lists what each real run needs.
- Android and web unit tests were not run; only the post-service unit tests named above were.
- Web screens rated UNVERIFIED in section 1 (scheduled list, hub edit, history clear, channel page, series manager, end-screen editor, search) were located, not followed.
- Android scheduled list, channel page, comments sheet for long video, search VIDEOS scope and credit flows were not followed beyond the API declarations in map 9.1.
- The search-service query side (`handler_tube.go`) and the feed ranking for `/v1/feed/videos` were not read.
- Members-only long videos, offline copies, live replays beyond LV-A-1 and LV-A-14, RSS and admin tube routes were out of this track's depth.
- Whether the resumable `expires_at` slides (LV-A-13) and which thumbnail variant each client card uses (LV-A-14).
- Whether a redelivered message after a rollout counts toward the stall sweeper's 3 attempts (journey 6); read suggests it does not, since attempts are counted on sweeper re-queues only.

## Notes for other tracks

- **Security / privacy (B):** `GET /v1/subtitles/:mediaId` does go through the delivery gate (`media-service/internal/http/clips_handler.go:330-333`), which answers map open question 12 for that route; `/renditions` was not checked. Segment TTL for long video is the main revocation gap (reels-17).
- **Data (C):** a visibility edit commits the search bump, `PostVisibilityChanged` and offline revocation in one transaction (`store/postgres/post_edit.go:263-312`), answering map question 17 except for already-signed URLs. Scheduled publish clears `publish_at` (`store/postgres/scheduled.go:131`), answering question 18.
- **Reliability (E):** the long transcode recovery path is journey 6; `MaxAttempts` is 3 in the stall policy (`transcode_lease.go:69-76`) against 8 for message retries (map 7), and a multi-hour encode restarts from zero on each rollout.
- **Tests (F):** LV-A-1 has no test anywhere (no fan-out test with a processing post); LV-A-5 has no store test for text edits; no test covers the Android `ResumeRule` against the server threshold.
- **Product:** LV-A-6 (4 h cap) and LV-A-8 (4K) need founder decisions; map question 16 (live replay made public) is answered by LV-A-1: no notification.
