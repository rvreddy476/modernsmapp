# Track B: security (long video / PostTube / MTube)

- **Date:** 2026-11-17. Read-only. Backend and Android: `C:\workspace\modernsmapp` (`codex/module-01-02-launch-safety`, working tree as found). Web: `C:\workspace\wt\pb-echo` (`feat/echo-web`).
- **Method:** I read the code for each claim myself. One helper pass covered the Android and web client questions (offline storage, link rendering, deep links, `dangerouslySetInnerHTML`, RSS escaping, caption upload). I re-checked a sample of its claims against the code (`OfflineCopies.kt:606-618`, `OfflineMediaModule.kt:74`, `MediaModule.kt:51`, `posttubeApi.ts:615-628`); the rest are tagged as read by that pass. Nothing was started; no database, container, device or deployed environment was used. The only commands executed were the existing unit tests quoted at the end of "Checked and sound".
- Backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Web paths are relative to `C:\workspace\wt\pb-echo`. Android paths are relative to `mobile/android/`.
- **Gateway behaviour** is as in the reels and feed Track B: it strips client `X-User-Id`, `X-Scopes` and `X-Internal-Service-Key`, sets `X-User-Id` from the verified token, refuses any `/internal/` path, lets tokenless requests through as anonymous, and stamps the internal key on the post-service prefixes (00-map §2.0). It resolves the client IP only across trusted proxies (`api-gateway/pkg/clientip/clientip.go:3-26,215-226`). Every post-service `/v1` route needs the internal key (`post-service/internal/http/handler.go:76-85`).

## Verdict for this track

**Not ready. No P0 found.** Long-video authorization has been hardened by several earlier "tube audit" rounds. Through the gateway I found no way for user A to read, change or delete user B's video, playlist, series, chapters, end screens, cards, captions, membership gate, credits, comments pins, watch progress or history. Every object-ID write I traced checks the owner before writing. Private and unlisted are kept out of listings, search, RSS, trending and playlists.

What remains:
- **P1 (one):** no per-user storage or transcode quota. The only upload control is a count budget that fails open (LV-B-1).
- **P2 (two):**
  - Long-video segment signatures last up to 12 h, so a takedown, deletion, privatisation or membership lapse is not enforced at segment level for hours (LV-B-2).
  - `POST /v1/videos/:id/cover-frame` still stores an arbitrary `thumbnail_url`. It is shown to every viewer and skips image moderation; this is the exact defect already fixed for chapters and playlists (LV-B-3).
- **P3:** eight lower findings (LV-B-4 to LV-B-11).

Map open questions answered here:
- **OQ1:** yes, LV-B-1.
- **OQ3:** LV-B-2.
- **OQ4:** trending, search collections and RSS are sound; the playlist-row case is LV-B-11.
- **OQ7:** `clampProgressSentAt` clamps future times. Rate limiting is LV-B-8.
- **OQ8:** LV-B-7.
- **OQ12:** captions are gated (reels "Sound"). Web caption upload is broken; see Notes.
- **OQ14:** the counters are deduplicated per viewer, per element, per day. Sound.
- **OQ21:** LV-B-6.

---

## 1. Route and auth table

Auth words:
- **user**: an empty or invalid `X-User-Id` answers 401.
- **owner**: the object's owner is checked before any write.
- **viewer-gated**: the post read gate (`singlePostRead` / `requirePostReadable`) or the playlist or series rule runs, with a nil viewer meaning signed out.

| Method | Path | Auth | Object check (file:line) | Evidence |
|---|---|---|---|---|
| GET | `/v1/videos/:videoId` | public, viewer-gated | `singlePostRead`; `storage_video_url` redacted for non-owners; `playback_url` dropped for non-members (`service/post_video_tools.go:42-73`) | VERIFIED-READ |
| PATCH | `/v1/videos/:videoId/trim`, `/category` | user, owner | `GetPostAuthorID != userID` refused (`post_video_tools.go:76-133`) | VERIFIED-READ |
| POST | `/v1/videos/:videoId/cover-frame` | user, owner | owner check `:136-143`; `cover_media_id` goes through `verifyCoverOnWrite` (`:149`); **`thumbnail_url` is not checked** (`:164-171`) → LV-B-3 | VERIFIED-READ |
| POST | `/v1/videos/:videoId/publish` | user, owner | owner check plus `ready`; keeps the chosen visibility (`post_video_tools.go:186-208`) | VERIFIED-READ |
| PUT | `/v1/posts/:postId/membership` | user, owner | `IsPostAuthor` (`http/membership_gate_handler.go:45-57`); the tier must be the author's own active tier (`service/membership.go:80-91`); strict body (`:36-43`) | VERIFIED-READ |
| GET, POST | `/v1/posts/:postId/chapters` | GET viewer-gated plus members gate; POST owner | `requirePostReadable` (`http/video_series_handler.go:939`), `GetChaptersForViewer` (`service/watch_progress.go:283-291`); POST `requirePostAuthor`, then validate, then thumbnail is own image (`watch_progress.go:240-265`, `tube_audit.go:225-327`) | VERIFIED-READ |
| GET, POST | `/v1/posts/:postId/end-screens`, `/cards` | GET viewer-gated; POST owner | `SaveEndScreens` / `SaveVideoCards` take the caller (`service/end_screens.go:393,513`); `target_url` must be https with a host (`:301-316`); video targets go through `viewablePostCard` (`service/hub_detail.go:150-175`) | VERIFIED-READ |
| POST | `/v1/posts/:postId/end-screens/:id/impression\|click`, `/cards/:id/...` | open (anonymous allowed) | detail gate; the element must belong to the post; owner not counted; one of each per viewer (user id, or hashed gateway-resolved IP) per element per UTC day; Redis failure fails open (`end_screens.go:1116-1150`, `http/end_screens_handler.go:252-301`) | VERIFIED-READ |
| POST | `/v1/posts/:postId/copyright-review/request` | user, owner | SQL `WHERE post_id = $1 AND author_id = $2 FOR UPDATE` (`store/postgres/copyright_title_review.go:681`) | VERIFIED-READ |
| GET | `/v1/videos/continue-watching`, `/history`; DELETE `/history` | user | caller's own rows only; posts hydrated as the caller (`service/watch_progress.go:132-215`); limit ≤100 (`http/video_series_handler.go:1116-1118,1142`) | VERIFIED-READ |
| POST, GET, DELETE | `/v1/videos/:videoId/progress` | user | key `(caller, post)`; POST requires the post to be readable (`video_series_handler.go:1012`); negative values refused (`:1006`); `sent_at_ms` clamped to now (`:979-988`); no own rate limit → LV-B-8 | VERIFIED-READ |
| POST, PATCH, DELETE | `/v1/video-series[/:id[/episodes[/:ref]]]` | user, owner | `requireVideoSeriesOwner` (`service/video_series.go:160-169,233,307`); episode must be the creator's own post (`:241,264-276`); cover is own image (`:133,311`); `channel_id` and `trailer_post_id` not checked → LV-B-9 | VERIFIED-READ |
| GET | `/v1/video-series/:id`, `/episodes`, `/v1/creators/:id/video-series`, `/v1/posts/:id/series` | public, viewer-filtered | `is_public` plus `canViewAuthor` (`video_series.go:174-225`); episodes filtered per viewer (`:285-296`) | VERIFIED-READ |
| POST, PATCH, DELETE | `/v1/playlists[/:id[/items[/:postId]]]` | user, owner | `requireUserPlaylistOwner` refuses other users and system kinds (`service/system_playlists.go:128-139`); cover is own image (`tube_audit.go:196-217`); the added post's readability is not checked → LV-B-11 | VERIFIED-READ |
| GET | `/v1/playlists/:id`, `/items`, `/v1/creators/:id/playlists` | public, viewer-filtered | private is owner only; unlisted readable by id but not listed; block / private account → 404 (`service/playlists.go:62-158`); unlisted items hidden from non-owners (`:289-364`); page ≤100, legacy walk ≤500 (`http/video_series_handler.go:694-746`) | VERIFIED-READ |
| GET | `/v1/playlists/system/:kind` | user | caller's own collection | not re-read line by line |
| PUT, DELETE | `/v1/comments/:id/pin`, `/heart` | user, owner of the post | `requireCommentPostAuthor` (`service/comment_creator.go:107-131`) | VERIFIED-READ |
| GET, POST | `/v1/credits/requests`, `/:id/accept\|decline`, DELETE | user | caller's own credits (`service/credits.go:535-640`) | VERIFIED-READ (signatures and SQL `c.user_id = $1`, `store/postgres/credits.go:461-478`) |
| GET | `/v1/creators/:id/appearances` | public | public, published, approved, long video, not members-only, profile visible (`service/credits.go:643-711`) | VERIFIED-READ |
| PATCH | `/v1/channels/me` | user | own channel only; banner is own image; links at most 10, https only; email validated; featured post must be own (`service/channel_branding.go:52-150`) | VERIFIED-READ |
| GET | `/v1/channels/:ref`, `/batch` (≤100), `/search` | public | `maxChannelBatch = 100` (`http/channels_handler.go:42`); only the featured post id is returned | VERIFIED-READ (partial) |
| POST, DELETE, GET, PATCH | `/v1/channels/:ref/subscribe`, `/subscription` | user | self-subscribe refused; graph follow first; row written even when the follow is only "requested" → LV-B-4 (`service/channel_subscriptions.go:255-330`) | VERIFIED-READ |
| GET | `/v1/channels/:ref/feed` (RSS source) | public, no viewer by design | owner visible to a stranger; every row re-judged by `anonymousMayAccessPost`; enclosure only for ready and passed media (`service/channel_feed.go:16-50,185-290`) → LV-B-5 | VERIFIED-READ |
| GET, POST, DELETE | `/v1/uploads/videos`, `/bulk`, `/bulk-delete`, `/:postId` | user | `ownedBulkIDs` plus a per-row author check (`service/post_edit.go:895-925`); delete passes the caller (`http/my_uploads.go:124-136`) | VERIFIED-READ |
| GET | `/v1/posts/trending?content_type=long_video` | public | SQL viewer-eligible public rows; the cache-hit path re-checks public, unscheduled and approved (`service/list_pages.go:137-151`); author gate (`post_hashtag_feeds.go:67-110`) | VERIFIED-READ |
| GET | `/v1/posts/live-recordings` | public | `visibility = 'public' AND deleted_at IS NULL` (`store/postgres/live_vod.go:179`) | VERIFIED-READ (SQL line only) |
| GET | `/v1/search/collections` | key, edge | refuses to index non-public playlists; query-time `visibility: public` term (`search-service/internal/store/search/tube.go:467-505`); internal source gives only id and visibility for non-public (`post-service/internal/http/channels_internal_list.go:29-31,150-156`) | VERIFIED-READ |
| * | `/v1/media/upload/resumable/*` | token | session owner on status, part and complete (`media-service/internal/service/resumable.go:207,244,311`); init counts against the upload budget (`:123-128`) | VERIFIED-READ |
| POST, PATCH | `/v1/subtitles/:mediaId` | token, owner | `AssertMediaOwner`, language tag, ≤200 000 bytes, `content_url` refused and derived by the server (`media-service/internal/service/audio.go:170-187`, `http/clips_handler.go:413-430`) | VERIFIED-READ |
| GET | `/v1/subtitles/:mediaId/track/:lang` | header, gate | `text/vtt`, `nosniff`, `private, max-age=300` (`clips_handler.go:385-387`) | VERIFIED-READ |
| GET | `/internal/channels/*`, `/internal/tube/*`, `/v1/search/internal/reindex/tube` | key only | refused at the edge | from the map; not re-read |
| all | admin tube routes | admin-service token, `tube:*` | `admin_token.go:278-313` | not re-read (admin track) |

**Deliberately public, and why:**
- **Watch, channel, video detail, chapters, end screens, cards, series, playlists by id, appearances, trending, live recordings:** the web watch and channel pages are public (`src/middleware.ts:47,105,112`). Each route applies the anonymous rule.
- **RSS:** podcast apps fetch it with no account.
- **Element impression and click:** a signed-out viewer sees end screens.

---

## 2. Findings

```
ID:            LV-B-1
Title:         No per-user storage or transcode quota; the only control is an upload-count budget that fails open, so one account can push about 30 x 16 GiB originals and 4-hour 4K transcodes a day.
Severity:      P1
Category:      input / abuse (cost and availability)
Evidence level: VERIFIED-READ
Evidence:      media-service/internal/service/ratelimit.go:21-24 (no Redis = no limit), :45-53
               (pipeline error -> "return true // fail-open"); MaxUploadsPerHour 10, MaxUploadsPerDay 30.
               resumable.go:100-130: a video is capped only at the worker cap
               (validateVideoOriginalSize), then counted against that same budget. upload.go:110-113 says
               the video size caps are not in init. Prod leaves MEDIA_WORKER_MAX_ORIGINAL_BYTES unset, so
               the cap is 16 GiB; the duration cap is 4 h; the job budget is 48 h (00-map §7, processing/limits.go:57,158).
               No *QUOTA* setting and no byte or minute counter exist in any Go service (00-map §7).
               The worker fleet is 2 replicas in prod with short and long loops sharing pods (00-map §11).
How to reproduce: With one account, init 10 resumable video uploads of 16 GiB each per hour (30 a day) and
               upload 4 h 4K files. Watch S3 bytes, worker scratch and the long queue. Repeat from a few free
               accounts. During a Redis blip the count limit disappears entirely.
Impact:        Storage and transcode cost with no ceiling. A handful of accounts can hold every
               transcode worker for days and starve every other creator's uploads (reels share the fleet).
Fix:           Smallest: in InitUpload and InitResumableUpload, keep a per-user rolling 24 h byte counter
               (Redis INCRBY total_bytes, e.g. 50 GiB/day) and a cap on concurrent non-terminal video
               assets per user (e.g. 3). Fail closed for video when Redis errors. Set
               MEDIA_WORKER_MAX_ORIGINAL_BYTES explicitly in values-prod.
Acceptance:    Unit tests: the init that would cross the daily byte budget answers 429; a 4th concurrent
               processing video answers 429; a Redis error refuses a video init. The prod values render
               the explicit byte cap.
Effort:        S-M
```

```
ID:            LV-B-2
Title:         Long-video HLS segment URLs are signed for up to 12 hours, so takedown, deletion, privatisation, copyright block or a lapsed membership is not enforced at segment level for hours.
Severity:      P2
Category:      authz / visibility
Evidence level: VERIFIED-READ
Evidence:      media-service/internal/delivery/sign_cache.go:60-70: MaxHLSSegmentTTL = 12 h, TTL = 2 x duration
               + 15 min. URLs are CloudFront canned signatures not bound to a viewer (reels Track B,
               REELS-B-4). There is no CreateInvalidation anywhere in Go code. The manifest is re-gated on
               every fetch (no-store), but a player that holds it, or anyone it was shared with, keeps every
               segment. Clients can already refresh: Android re-prepares on 403 or X-HLS-Expires-At
               (core/media/.../HlsRefresh.kt:31-46).
How to reproduce: As a member, open a 3-hour members-only video and save the media playlist. Cancel the
               membership (or have the creator delete the video, or have an admin block it for copyright).
               Fetch the segment URLs: they play for about 6 h 15 min.
Impact:        Revocation of paid, private or taken-down long video (including legal takedowns) is delayed by
               hours. A leaked playlist is a bearer link to the whole film for half a day. This is the
               long-video extension of reels-17, and much wider here.
Fix:           Cap the segment TTL for protected assets at about 30 min, independent of duration, and keep
               X-HLS-Expires-At. Make sure web hls.js reloads the playlist on 403 or expiry, as Android
               does (not verified on web).
Acceptance:    Unit test: HLSSegmentTTL for a 3 h video ≤ 30 min. Web player test: a 403 on a segment
               triggers one playlist reload and resumes at the same position.
Effort:        M (server S; web refresh path to confirm or build)
```

```
ID:            LV-B-3
Title:         POST /v1/videos/:id/cover-frame stores any string as the video's thumbnail_url, which every viewer then loads; it skips image moderation and works as a tracking pixel.
Severity:      P2
Category:      input / visibility (unmoderated content, viewer privacy)
Evidence level: VERIFIED-READ
Evidence:      http/video_tools_handler.go:195-215 binds thumbnail_url *string. service/post_video_tools.go:163-171
               writes it to video_metadata.thumbnail_url with no check (the owner check at :141 is the only
               gate; the cover-media check at :146-161 runs only when cover_media_id is sent).
               Where viewers get it:
               - GET /v1/videos/:id (public) returns it (dto/video.go:67,87);
               - related-post and end-screen video cards for any viewer (service/hub_detail.go:192-194);
               - the credited person's request list (store/postgres/credits.go:470).
               Web renders it as given: lib/imageUrl.ts:78-82 returns non-/serve URLs unchanged;
               watch/components/UpNext.tsx:106 uses <img src={r.video.thumbnail_url}>; WatchPage.tsx:821 uses
               it as the poster.
               The same defect was fixed for chapter thumbnails ("used to take any string, an off-platform
               tracking URL included", tube_audit.go:304-327) and playlist covers (:196-217), but not here.
               No client sends thumbnail_url (useUploadStudio.ts:655-658 sends cover_media_id and
               timestamp_ms only). The integration test cover_guard_integration_test.go:225-251 only checks
               that a refused cover does not write the thumbnail; it never sends thumbnail_url alone.
How to reproduce: As a creator, POST /v1/videos/<own video>/cover-frame {"thumbnail_url":"https://attacker.example/p.gif"}
               -> 200. Open any video whose end screen or related card points at it, signed out: the browser
               requests the attacker host.
Impact:        A creator can show viewers an image that never passed Rekognition (explicit or illegal
               imagery on the watch page and other creators' end screens), and log the IP and user agent of
               everyone who sees the card.
Fix:           Run thumbnail_url through the chapter rule (parsePlatformMediaRef + verifyOwnImage, stored
               as /v1/media/<id>/serve), or drop the field from the request since no client uses it. The
               worker-written thumbnail (consumers/media.go:147-150) is unaffected.
Acceptance:    Handler or service test: an off-platform thumbnail_url answers 400 or 422 and writes nothing;
               an own image id is stored as its /serve path; a stranger's image id answers 403 MEDIA_NOT_OWNED.
Effort:        S
```

```
ID:            LV-B-4
Title:         Subscribing to a private account's channel records the subscription while the follow is only "requested".
Severity:      P3
Category:      authz / privacy
Evidence level: VERIFIED-READ (the write); SUSPECTED (any notification leak)
Evidence:      service/channel_subscriptions.go:255-289: graph.Follow returns "requested" for a private
               account (:152-163) and channels.Subscribe(...) runs regardless (:276). The row raises the
               public subscriber_count and puts the requester on /internal/channels/:id/subscriber-ids, the
               fan-out list. Delivery re-checks visibility only per post visibility, block and follower for
               "followers" posts (notification-service/internal/service/fanout_eligibility.go:62-122). A
               "public" post by a private account is not checked against an accepted follow. It is saved today
               only if postStateFor's anonymous GET /v1/posts/:id answers 404 for a private-account author
               (:174-205; that viewer behaviour was not traced).
How to reproduce: Account B is private. A subscribes (follow requested, not accepted). B uploads a public
               video. Check whether A receives an "uploaded" notification carrying the title.
Impact:        At worst, titles of a private account's uploads reach people the owner never approved. At
               least, the subscriber counts are wrong.
Fix:           In Subscribe, write the row only when followStatus == "followed". Promote it on follow-accept
               (graph event), or answer {"status":"requested"} with no row.
Acceptance:    Service test: Follow returning "requested" writes no subscription row and returns requested.
Effort:        S
```

```
ID:            LV-B-5
Title:         The channel RSS feed hands a downloadable MP4 of every public long video to anyone, ignoring the video's allow_download setting, with no channel opt-out; shared caches keep the item up to 70 min after a video goes private.
Severity:      P3
Category:      privacy / policy
Evidence level: VERIFIED-READ
Evidence:      service/channel_feed.go:186-227: feedEligible is anonymousMayAccessPost only; the enclosure is
               /serve/720p, then 480p, then the original. No allow_download or channel setting is read.
               The web route caches public, s-maxage=600, stale-while-revalidate=3600 (feed.xml/route.ts:21, map §5).
               Founder rule: "Keep a copy = in-app offline copy only; viewers never get a file"
               (memory offline-copies-not-downloads); offline grants do require allow_download
               (offline_copies.go:386).
How to reproduce: Set allow_download=false on a public long video, then fetch /posttube/channel/<handle>/feed.xml.
               The item carries an <enclosure> URL that downloads the 720p file signed out.
Impact:        A creator's "no downloads" choice is not honoured, and a public directory of direct files is
               published. The bytes are public anyway; the issue is advertising them as a file.
Fix:           Leave out the enclosure (or the item) when allow_download is false, and add a per-channel
               rss_enabled switch (default on, as built). Drop stale-while-revalidate to ≤600 s.
Acceptance:    channel_feed unit test: allow_download=false produces no enclosure.
Effort:        S
```

```
ID:            LV-B-6
Title:         Android offline copies are stored unencrypted and kept for 48 h after sign-out; the streaming cache is never cleared.
Severity:      P3
Category:      privacy (at rest)
Evidence level: VERIFIED-READ (OfflineCopies.kt:606-618, OfflineMediaModule.kt:74, MediaModule.kt:51 re-read by me;
               48 h rule and teardown list from the helper pass: OfflineRules.kt:135-141, TubeTeardown.kt:23-26)
Evidence:      SimpleCache(storage.mediaDir, NoOpCacheEvictor(), database) with no cipher sink. Files are in
               noBackupFilesDir/offline_copies (app-private, not backed up). holdForSignOut deletes only
               unfinished copies and stamps signedOutAtMs; stored copies stay until the 48 h rule runs at the
               next start or background check. The streaming SimpleCache in cacheDir/media is not in any
               teardown (reels-20). Expiry is checked on the device clock (OfflineRules.kt:102,141).
How to reproduce: Save a members-only video offline, sign out, then inspect the app data on a rooted
               device or a debug build: plain HLS/MP4 bytes remain for 48 h, and the stream cache indefinitely.
Impact:        On a shared or rooted phone, the next user or an extractor gets watched and paid video.
               "Viewers never get a file" holds only against non-rooted devices.
Fix:           Delete stored copies at sign-out (keeping 48 h buys nothing a re-download does not), clear the
               streaming SimpleCache in the teardown, and pass an AesCipherDataSink / AesCipherDataSource
               with a Keystore key for offline writes. DRM is the long-term answer.
Acceptance:    Unit test: holdForSignOut leaves no copy bytes; a teardown test clears the media cache.
Effort:        S (delete) / M (encryption)
```

```
ID:            LV-B-7
Title:         "Pause watch history" exists only in each client; the server keeps recording history from every other device and from the web after sign-out.
Severity:      P3
Category:      privacy (DPDP consent)
Evidence level: VERIFIED-READ
Evidence:      web src/features/posttube/library/historyPause.ts:7,13,43-44 (localStorage flag, no network
               call); Android feature/tube/.../WatchProgressRepository.kt:43 (`if (pause.paused.first()) return`,
               DataStore only). The server's SaveWatchProgress has no pause check
               (post-service/internal/http/video_series_handler.go:990-1037). The web flag is not reset at
               sign-out (helper pass); Android resets it (TubeTeardown.kt:23-26).
How to reproduce: Pause history on Android, then watch on the web: GET /v1/videos/history lists the video.
Impact:        The user's privacy choice is not honoured server-side. History still feeds recommendations and
               "continue watching" on every device.
Fix:           Store watch_history_paused in a server-side preference; SaveWatchProgress answers 204 without
               writing when paused; both clients read and write that setting.
Acceptance:    Handler test: with the preference set, POST /progress writes no row.
Effort:        M
```

```
ID:            LV-B-8
Title:         Watch-progress writes have no per-user or per-video limit; each costs a read gate, an upsert and a Redis write.
Severity:      P3
Category:      input / abuse
Evidence level: VERIFIED-READ
Evidence:      video_series_handler.go:990-1037 (requirePostReadable, knownWatchDurationMs that can do two
               more reads, UpsertWatchProgress, Redis mirror service/watch_progress.go:43-66). The only limit is
               the gateway's 60 rps per user, which fails open on Redis (00-map §2.0). Clients write every 10 s,
               so the honest rate is about 0.1 rps.
How to reproduce: Loop POST /v1/videos/<id>/progress at 60 rps from one token, across many video ids.
Impact:        One account generates about 180 DB operations a second. A few accounts load post-service
               Postgres, which serves every product.
Fix:           Per (user, video) Redis SET NX with a 2-3 s TTL; when the key exists, answer 202 without
               writing (or 429).
Acceptance:    Handler test: a second write within the window does not reach the store.
Effort:        S
```

```
ID:            LV-B-9
Title:         Mass assignment: video-series channel_id and trailer_post_id, and playlist channel_id, are stored without any ownership or readability check.
Severity:      P3
Category:      authz (mass assignment)
Evidence level: VERIFIED-READ
Evidence:      http/video_series_handler.go:201-215 (create), :299,339 (PATCH trailer_post_id), :587-590 (playlist
               channel_id). service/video_series.go:128-139 checks only the cover; playlists.go:87-106 checks
               only the cover. Stored as given (store/postgres/video_series.go:46-49,392-399). No server or client
               reader uses trailer_post_id today (only purge.go:253-257 and posts_lifecycle.go:424 clear it).
How to reproduce: POST /v1/video-series {"title":"x","trailer_post_id":"<stranger's private post>","channel_id":"<stranger's channel>"}
               -> 201, echoed back.
Impact:        None visible today. The first client that renders a series trailer or groups series by
               channel_id would show a stranger's or a private video under someone else's series.
Fix:           Ignore channel_id (derive it from the creator's channel) and require trailer_post_id to be the
               creator's own post (the requireOwnEpisodePost rule).
Acceptance:    Service test: a stranger's trailer_post_id answers 403; channel_id in the body is ignored.
Effort:        S
```

```
ID:            LV-B-10
Title:         The self-service data export covers auth-service data only; watch history, subscriptions, playlists, dislikes and comments are in no export.
Severity:      P3
Category:      privacy (DPDP right to information)
Evidence level: VERIFIED-READ (export scope); UNVERIFIED (whether an off-platform export process exists)
Evidence:      identity-platform/services/auth-service/internal/http/me.go:181-196 ("all personal data held
               by the auth service"); route handler.go:374. admin-service/internal/service/exports.go:11-23 only
               records a request. No post-service export route exists.
               Erasure looks covered: post-service purge deletes watch_progress, offline copies, private
               shares and credits (store/postgres/purge.go:187,227,232,245). user-service purge deletes
               channels and channel_subscriptions (user-service/internal/store/purge.go:60,76-78), and tunes,
               playlists and video_series cascade from users(id) (migrations 010:22, 012:7,34,97). This holds
               only if both services share one database, as local compose does (POSTGRES_DB app); the prod
               DSN was not read.
Impact:        A DPDP access request cannot be answered from the product for viewing data.
Fix:           Add /internal/users/:id/export to post-service (history, subscriptions, playlists, tunes,
               comments) and include it in the export bundle.
Acceptance:    Integration test: the export for a seeded user contains their progress rows and subscriptions.
Effort:        M
```

```
ID:            LV-B-11
Title:         A playlist can list posts the owner cannot see, and readers of a public playlist get the post_id of rows they may not open.
Severity:      P3
Category:      visibility (existence and id disclosure)
Evidence level: VERIFIED-READ
Evidence:      service/playlists.go:236-254: AddPlaylistItem checks playlist ownership only, never that the caller
               may read the post. :317-321,355-364: rows whose post the viewer may not see are kept with
               Post nil ("so a client can tell gone from never there"); only unlisted ones are dropped.
How to reproduce: Add a stranger's private post id to your public playlist. A signed-out GET
               /v1/playlists/<id>/items returns {"post_id":"<id>","post":null}.
Impact:        It confirms that a private or deleted video exists and leaks its id. The id alone unlocks
               nothing; every read is gated.
Fix:           Refuse AddPlaylistItem unless viewerMayViewPost(caller) passes. For non-owners, drop rows whose
               Post is nil instead of keeping them.
Acceptance:    Service test: adding an unreadable post answers 404; a non-owner's page has no nil-post rows.
Effort:        S
```

---

## 3. Inherited from reels (re-checked in code, not re-derived)

- **reels-6 (REELS-B-1):** media-service still takes the viewer from a bare `X-User-Id` (`media-service/internal/http/delivery_viewer.go:28-34`). It covers long-video HLS, storyboards, `/download` of originals of up to 16 GiB, and captions.
- **reels-7:** `GET /v1/analytics/content/:contentId/views` is still registered (`analytics-service/internal/http/handler.go:74`). It covers long-video watch depth too.
- **reels-8 (REELS-B-2):** `DefaultRequiredServices` still has no `analytics` (`identity-platform/services/auth-service/internal/purge/worker.go:44`). Long-video playback sessions and `watch_sessions` are never erased.
- **reels-15 (REELS-B-13):** media JWT still accepts a token without `exp` (`media-service/internal/http/middleware.go:147`). It covers the resumable upload and caption writes.
- **reels-17 (REELS-B-4):** revocation by signature TTL. Much wider for long video; see LV-B-2.
- **reels-20:** the Android streaming cache survives sign-out (`core/media/.../di/MediaModule.kt:51`); see LV-B-6.
- **reels-21, -22, -23:** web security headers, and the `/v1` proxy dot-segment and whole-body buffering. These apply unchanged to `/posttube/*`; a long-video resumable chunk passes through the same proxy.
- **reels-24 (REELS-B-6):** the owner frame routes stream the whole original into the API pod. A 16 GiB long-video original makes this worse; the web frame picker uses canvas capture instead (`hub/coverFrame.ts:24-51`).
- **reels-25, -16:** the shared-key destructive internal media routes, and the un-hardened audio ffmpeg path (dub tracks on long videos).
- **reels-78:** raw Go error text in long-video handlers too (`post-service/internal/http/video_tools_handler.go:62,128,223,266`, `video_series_handler.go:946,1034`).
- **reels-92:** deleting an asset drops its resumable rows without aborting the multipart upload. There is also no user-facing abort route; parts wait for the 24 h expiry sweep (`media-service/internal/service/resumable.go`, no Abort function; map §6).

---

## 4. Checked and sound

| Item | Evidence |
|---|---|
| Every long-video object write checks the owner before writing: trim, category, cover media, publish, membership gate, chapters, end screens, cards, series and episodes (own posts only), playlists (system kinds refused), pins and hearts, bulk edit and delete, copyright review request, channel branding | §1 table |
| Watch progress and history are keyed by the gateway caller and never take a user id from the body; history hydration runs as the caller | `video_series_handler.go:990-1209`, `service/watch_progress.go:132-215` |
| `sent_at_ms` in the future is clamped to now (map OQ7) | `video_series_handler.go:979-988` |
| `/v1/videos/:id` redacts `storage_video_url` for non-owners, honours the read gate and the members gate | `service/post_video_tools.go:42-73` |
| Playlist rules: private is owner only; unlisted readable by link but never listed; block / private account answers 404; unlisted items hidden; covers are the owner's platform image | `service/playlists.go:54-158,289-364`, `tube_audit.go:196-217` |
| Series rules: `is_public`, author gate, episodes filtered per viewer, episode must be the creator's own | `service/video_series.go:174-296` |
| Chapter titles ≤100 runes, ≤100 chapters, ascending starts; thumbnails own image | `tube_audit.go:225-327` |
| End-screen and card links https only; video targets through `viewablePostCard`; counters deduplicated per viewer per day, IP resolved by the gateway | `end_screens.go:301-316,1116-1150`, `hub_detail.go:150-175`, `api-gateway/pkg/clientip/clientip.go` |
| Channel links https only, ≤10; banner own image; featured post own | `channel_branding.go:52-150` |
| Trending excludes non-public, scheduled and unapproved posts, including the cache-hit path | `list_pages.go:137-151` |
| Search collections index public playlists only, refused at write and filtered at query | `search-service/internal/store/search/tube.go:467-505` |
| RSS: anonymous rule applied twice, private or hidden owner answers 404; all user text escaped or CDATA-split, description escaped before `<p>`, links built with `encodeURIComponent` from env bases | `channel_feed.go:16-50,236-290`; web `features/posttube/feed/rss.ts:195-245,356,381,400` (helper pass, spot-checked at 200-245) |
| Captions: `content_url` refused and derived by the server; owner only; size cap; served as `text/vtt` with `nosniff`; web renders through a native `<track>` (no cue HTML) | `media-service/internal/http/clips_handler.go:385-430`, `service/audio.go:170-187`; web `TubePlayer.tsx:806` |
| Members-only: gate validates the author's own active tier; chapters and playback URL hidden from non-members; RSS and appearances exclude members-only | `membership.go:80-91`, `post_video_tools.go:67-69`, `watch_progress.go:283-291`, `credits.go:700-704` |
| Resumable sessions: owner checked on status, part and complete; init counted against the budget | `resumable.go:123-128,207,244,311` |
| Web: no `dangerouslySetInnerHTML` or markdown HTML in posttube, upload or app/posttube; external links `rel="noopener noreferrer"`; end-screen links https only | helper pass: `watch/watchApi.ts:456-461`, `VideoElementsOverlay.tsx:169,312`, `ChannelLinks.tsx:18` |
| Android: no HTML rendering or WebView in feature/tube; end-screen links https and re-checked before `ACTION_VIEW` with `CATEGORY_BROWSABLE`; deep links https, allowed hosts, UUID id, handle regex, `autoVerify` | helper pass: `data/WatchElements.kt:275-279`, `core/model/.../WebLinks.kt:182-212`, `AndroidManifest.xml:102-112` |

Commands run (the existing unit tests only):

```
cd Architecture/services/post-service && go test -count=1 -run 'Playlist|VideoSeries|Chapter|WatchProgress|ChannelFeed|EndScreen|CoverFrame|Credit|Subscri' ./internal/service/ ./internal/http/
ok  github.com/atpost/post-service/internal/service  0.578s
ok  github.com/atpost/post-service/internal/http     0.417s
cd Architecture/services/media-service && go test -count=1 -run 'HLSSegment|SignCache|Subtitle|Caption|Resumable' ./internal/delivery/ ./internal/service/ ./internal/http/
ok  github.com/atpost/media-service/internal/delivery  2.295s
ok  github.com/atpost/media-service/internal/service   0.271s
ok  github.com/atpost/media-service/internal/http      0.394s
```

## 5. Not verified

- **Feed-service long-video surfaces:** `/v1/feed/videos`, `/watch`, related, `home?platform=posttube`, chips and categories pages. Relied on the map (allowlist `listingvisibility.go:35-68`); not re-read. Category topic pages were not traced.
- **Notification fan-out request identity:** which viewer `postStateFor` asks as, which decides LV-B-4's severity. Members-only upload notifications to non-members (map OQ5) were not traced either.
- **Graph-service follow rate limits:** subscribe/unsubscribe toggling to spam follow notifications.
- **`PATCH /v1/posts/:id` for long-video fields:** visibility to `unlisted`, `allow_download`, `age_restriction`, `related_post_id`, recording location, link URL. Only the related-post and link paths were glanced at (`post_edit.go:500-540`). Propagation to search and feed after an edit (map OQ17) is not covered.
- **Scheduled publish:** whether `publish_at` is cleared on publish (map OQ18).
- **Live replay:** promotion from unlisted to public (map OQ16).
- **Flick-series routes `/v1/series/*`:** visibility parity with `series_visibility.go` (map OQ19).
- **user-service channel CRUD reachability** (map OQ9), and whether prod post-service and user-service share one database (decides the erasure note in LV-B-10).
- **Web player behaviour on segment 403 or expiry** (needed for LV-B-2's fix).
- **Web upload of large files through the proxy, CORS prod origins, CSRF on the media cookie for resumable writes:** inherited reels items, not re-read.
- **Live behaviour:** CloudFront edge caching after delete; any ffmpeg probing behaviour; secrets in git history or images (Stage 1).
- **Admin tube routes and step-up:** admin track.

## 6. Notes for other tracks

- **Track A (functional):**
  - **Web caption upload always fails.** `createSubtitleTrack` posts `content_url: data:text/vtt,...` (web `features/posttube/data/posttubeApi.ts:615-628`); media-service refuses any `content_url` with 400 `CONTENT_URL_NOT_ACCEPTED` (`media-service/internal/http/clips_handler.go:426-430`). The studio tolerates the failure, so creators are never told; hub captions use the same function (`hub/hubApi.ts:1179`). Fix: send `content: vttContent`.
  - Android description links are not clickable (`ui/watch/WatchDetails.kt:494`).
- **Track C (data):** LV-B-10 erasure depends on a shared database. `video_series.trailer_post_id` has no ON DELETE action and is handled by hand in purge.
- **Track D (performance):** LV-B-8 progress write cost. LV-B-1 transcode fleet exposure.
- **Track E (reliability):** LV-B-1, a single tenant can monopolise the long-transcode loop. The upload budget is fail-open on Redis.
- **Clients:** web description and channel links accept `http://` (`watch/linkify.ts:13`, `channel/channelApi.ts:146-152`) while the server only stores https channel links. Harmless, but inconsistent.
