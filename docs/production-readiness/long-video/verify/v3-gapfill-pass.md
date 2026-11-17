# Long video: verification pass V3 (gap fill and PASS challenge)

- **Date:** 2026-11-17. Read-only. Trees as in `00-map.md`: backend and Android `C:\workspace\modernsmapp` (dirty working tree read as is), web `C:\workspace\wt\pb-echo`.
- **Method:** four read-only helper passes (feed/listing visibility; edit, schedule, series and admin authorization; notification, database, members-only, offline and RSS; client players and screens). Every P1 and P2 below, and every PASS/FAIL in Part 2, was re-read by me at the cited lines. Items tagged "(helper)" were read by one helper only.
- **Executed:** existing Go unit tests only. post-service `-run 'TestPostAuthoringWrites|TestPlaylistItemWrites|TestUpdatePost|TestSeries|TestVideoSeries|TestHubBatch|TestPrivateShare|TestCommentCreator|TestTubeAudit'` (service) and `'TestAdmin|TestCopyright|TestHub|TestCredits|TestEndScreens|TestMembers'` (http), `BatchVisibility|MembersRead|Appearance|Trending`; feed-service `Listing|Processing|Schedule|Members`: all `ok`. Plus `grep` scans. `gitleaks` and `trufflehog` are not installed, so git history was not scanned. No secret file was opened (`Architecture/docker/local.env` is tracked and was **not** opened).
- Backend paths are relative to `Architecture/services/`; web paths to `C:\workspace\wt\pb-echo\src`; Android paths to `mobile/android/`, `T/` = `feature/tube/src/main/kotlin/com/us/android/feature/tube/`.

## Part 1. Coverage gaps settled

### 1.1 Unlisted, private, scheduled, processing and members-only on listing surfaces

| Surface | Status | Evidence |
|---|---|---|
| feed-service `/v1/feed/videos` (plain, chip, category, sort, subscribed) | COMPLETE | all paths end in `HydratePosts` (`feed-service/internal/http/handler.go:416,428-434`, `service/tube_page.go:34`, `category.go:223-248`); tail filters on cache-hit and fresh paths (`hydration.go:429-432,500-502`): `applyProcessingFilter` (`processingfilter.go:32`), `applyListingVisibilityFilter` keeps only `""`, public, followers, friends (`listingvisibility.go:40`). Fresh rows come from post-service batch as the viewer (`post_batch.go:63`) (helper) |
| `/v1/feed/watch`, `/v1/feed/home?platform=posttube`, related | COMPLETE | `handler.go:487-493,179`; related seed via viewer-scoped batch and pool hydrated (`related.go:142,222`) (helper) |
| feed caches | COMPLETE | `feed:hydrate` never stores processing, scheduled or members-only rows (`hydration.go:871,893-900`); a visibility or gate change writes `post:stale:<id>`, checked before any cached row is used (`events/post_lifecycle.go:121`, `deleted_tombstone.go:93`); `feed:mainfeed` holds only a policy flag (`longvideo.go:347`) (helper) |
| `/v1/posts/categories?kind=long` | COMPLETE (n/a) | static taxonomy, no posts (`post-service/internal/http/post_reads_handler.go:37`) |
| `/v1/posts/trending?content_type=long_video` | COMPLETE | `ViewerEligibleSQL` + `visibility='public'` (`store/postgres/saved.go:281-283`); cache-hit re-filter (`service/list_pages.go:145`); members-only redaction (`post_hashtag_feeds.go:79-91`) |
| Topic pages | COMPLETE | both clients call `/v1/feed/videos?category=` (web `features/posttube/discovery/discoveryApi.ts:175-187`, Android `core/feed/.../Discovery.kt:274-281`), so the feed row above applies |
| `/v1/creators/:id/appearances` | COMPLETE | `appearanceListable`: public, long video, `tier_required_id IS NULL`, approved, not scheduled (`service/credits.go:701-703`) |
| End-screen latest / popular targets | COMPLETE | candidate SQL public, approved, unscheduled (`store/postgres/end_screens.go:83-94`); each target through `viewablePostCard` (`service/hub_detail.go:155-175`). A "specific" unlisted target opens for anyone (link-holder rule, by design) |
| `/v1/posts/batch` | COMPLETE for private, followers, scheduled, processing, members-only; unlisted returned by id **by design** | viewer only from `X-User-Id` (`post_reads_handler.go:335`); private refused even to share recipients (`service/batch_visibility.go:42-45`); members-only redacted (`post_reads.go:345`). Note: client-built "ids then batch" lists (web Liked tab, reposts) can show an unlisted post someone liked |
| `/v1/posts/live-recordings` | COMPLETE | `source='live' AND visibility='public' AND approved AND publish_at IS NULL` (`store/postgres/live_vod.go:176-180`) |
| `/v1/posts/by-author/:id?type=long_video` | COMPLETE | `author_sort.go:185-188`, `post_reads.go:255-270`, `unlisted_listing.go:50` |
| search `/v1/search/posts?type=videos` | COMPLETE | index-time: public + approved only, members-only removed (`search-service/internal/events/consumer.go:562,585-600`, `eligibility.go:45`); query-time mandatory `visibility=public AND review_status=approved` (`store/search/opensearch.go:710-714`) and block / private-author exclusion (`:767`). The tier is not indexed, so members-only exclusion is index-time only |
| Members-only in listings | COMPLETE (by design) | redaction keeps title, cover, duration, tier; removes text, media, chapters, credits, links (`service/members_read.go:33-45`, `membership.go:287-313`). Appearances and search leave the video out. Edge: an untitled legacy `content_type='video'` card falls back to the first text line without a tier check (`hub_detail.go:177-178`); long videos require a title (`create_guards.go:69`), so P3 at most, not raised |

### 1.2 Authorization of edits, schedule, series, replays, admin

| Item | Status | Evidence |
|---|---|---|
| `PATCH /v1/posts/:id` owner check | COMPLETE | service `post_edit.go:177`; store re-checks under `FOR UPDATE` (`store/postgres/post_edit.go:137-144`) and in the UPDATE `WHERE author_id` (:199). Tests `service/post_edit_test.go:151,154`, `http/hub_batch_routes_test.go:231`, integration `mtube_integration_test.go:558` |
| Mass assignment on PATCH | COMPLETE (none) | request struct has no author, review status, counts, content type, source or `publish_at` (`http/post_edit.go:59-113`) |
| Referenced objects | COMPLETE | `related_post_id` own post (`service/post_edit.go:566`), cover own image (`:617`), tier own active tier (`:409`), tier refused under hold (store :148). Credited-user resolution depth (`:461`) UNVERIFIED |
| Visibility values | PARTIAL | only the new value is checked (`post_edit.go:50,374,626`). A `staged` post can be edited straight to `public` (LV-V3-6). Edits never check author standing (LV-V3-7) |
| Live replay promotion | COMPLETE for privacy, BROKEN for notification | created unlisted (`store/postgres/live_vod.go:168-170`); owner PATCH to public bumps search eligibility and emits `PostVisibilityChanged` in one transaction (`store/postgres/post_edit.go:263-289`); no `PostCreated`, so no subscriber notification (LV-A-1, unchanged) |
| Scheduled publish routes | COMPLETE | owner `service/schedule.go:503` + store `author_id` (`store/postgres/scheduled.go:241`); list scoped by author (`:334`); window 5 min to 30 days (`schedule.go:521`). Test for the stranger 403 is integration only (`scheduled_integration_test.go:187`) |
| `/v1/series/*` (flick series) | PARTIAL | series-owner check only; the episode post is never checked (`service/series.go:33-45`), episode write is an upsert that overwrites (`store/postgres/series.go:83-89`); no author gate on reads. No client calls `/v1/series` (grep of both clients). LV-V3-8 |
| `/v1/video-series` | COMPLETE | `requireVideoSeriesOwner` (`service/video_series.go:160-169`), own-episode rule (:241,264); `channel_id` / `trailer_post_id` unchecked (LV-B-9, and now LV-V3-1) |
| Admin tube routes | COMPLETE | Ed25519 service token in `X-Service-Authorization`, issuer admin-service, signed actor, per-route permission (`http/admin_token.go:146-205,277-313`, `copyright_review.go:92-104`); the plain internal key cannot reach them (`handler.go:76-85`, `admin_token.go:110`). Step-up on reject and copyright block in admin-service (`admin-service/internal/http/handler_content.go:113-128`, `handler_tube.go:75-86`); none on title-catalogue edits and `/visibility` (helper). Tests `admin_token_test.go:276,379,431`, `admin_token_integration_test.go:115-273` |

### 1.3 Notification fan-out identity and members-only uploads

- **Identity: anonymous.** `postStateFor` calls `GET {POST_SERVICE_URL}/v1/posts/:id` with only `X-Internal-Service-Key`, no `X-User-Id` (`notification-service/internal/service/fanout_eligibility.go:180-210,281-296`, re-read). post-service judges it as a nil viewer (`post-service/internal/service/post_reads.go:20-65`, `post_visibility.go:78-104`).
- Consequences (all re-read): a followers-only post is 404 to a nil viewer, so the `followers` branch at `fanout_eligibility.go:112-115` can never deliver; a private account's posts are 404 to a nil viewer (`post_visibility.go:85`); an age-restricted post answers 401/403, which `postStateFor` returns as an error (`:203-204`), so every recipient fails and retries. LV-V3-2, LV-V3-3.
- **Members-only uploads:** a non-member subscriber is notified with the title (`members_read.go:43` keeps the title; `eligible()` has no tier check). Consistent with the "title is public" redaction rule: COMPLETE by design; founder confirmation still open (map OQ5).
- Empty `review_status` counts as approved (`:202`), harmless because `GetPost` already 404s unapproved posts (`post_reads.go:32-40`).

### 1.4 Shared database and erasure

- **Prod DSN: UNVERIFIED** (each service reads its own secret `postgres_dsn`, `deploy/services/post-service/values-prod.yaml:36,39`, `user-service/values-prod.yaml:35,39`; Terraform creates one Aurora cluster, `infra/terraform/modules/aurora/main.tf:164`). **But the two services must share one database:** post-service migrations 010, 011 and 012 hard-reference `users(id)` (`post-service/database/migrations/012_posttube_features.sql:7,34`) and post-service has no `users` table, so a split database cannot migrate. Migration 041 says it adapts user-service's `channels` table "in the shared app database" (`041_channels.sql:6-10`).
- **Erasure coverage:** post-service purge deletes comments, watch progress, offline copies, private shares (`store/postgres/purge.go:108-110,154,187,227,232`); user-service purge deletes channel subscriptions, channels, then users (`user-service/internal/store/purge.go:60,73-78`); tunes, playlists, video series cascade from `users` (012:7,34). COMPLETE in content, **BROKEN in ordering**: see LV-V3-1.

### 1.5 Expired signed segment mid-playback

| Client | Status | Evidence |
|---|---|---|
| Server | n/a | child playlist segment URLs signed once at fetch, `EXT-X-ENDLIST`, so no player reloads it; `X-HLS-Expires-At` sent and CORS-exposed (`media-service/internal/service/playback.go:276-314`, `internal/http/handler.go:717-725`) (helper) |
| Android | COMPLETE | refresh on 403 or passed `X-HLS-Expires-At`, `setMediaSource(source, currentPosition)` keeps position (`core/media/.../HlsRefresh.kt:55-114,173-176`), installed by `T/ui/watch/WatchPlayback.kt:72` (helper) |
| Web | BROKEN | LV-V3-4 (re-read `features/posttube/components/TubePlayer.tsx:319-383`) |

### 1.6 Web screens left unverified

| Screen | Status | Evidence |
|---|---|---|
| Hub edit | COMPLETE | `features/posttube/hub/hubApi.ts:580-634` keys match `post-service/internal/http/post_edit.go:60-112`; failure toast with server message (`hub/components/EditSheet.tsx:285-298`) (helper) |
| Scheduled list | PARTIAL | `posttubeApi.ts:459-467` matches `handler.go:125-126`; no cancel (none on the server either), generic errors hide the 5 min to 30 day rule, `next_cursor` ignored after 50 (`app/posttube/scheduled/page.tsx:41,55,82`). LV-V3-9 |
| Series manager | PARTIAL | create, list, episodes match (`features/upload/studioApi.ts:250-275`); rename and delete (`PATCH`/`DELETE /v1/video-series/:id`, `video_series_handler.go:303,408`) never called. LV-V3-10 |
| Search | COMPLETE | `discovery/discoveryApi.ts:335-343,476-489` matches search-service params (`store/search/tube.go:171-183`); one page only |
| Caption upload (hub and studio) | BROKEN | LV-V3-5 |
| Offline | COMPLETE | per-device grants, bytes in OPFS / Cache Storage, blob playback with `nodownload` (`features/offline/storage.ts:87-238`, `OfflinePlayer.tsx:82-94`); no file handed out (helper) |

### 1.7 Android screens left unverified

| Screen | Status | Evidence |
|---|---|---|
| Scheduled | PARTIAL | `T/ui/scheduled/ScheduledViewModel.kt` → `core/feed/.../VideoFeedRepository.kt:253-270`; a failed load returns an empty list and shows "Nothing scheduled" (:257, `ScheduledPostsScreen.kt:112`); client re-sorts ascending against server `DESC` (`ScheduledViewModel.kt:99`, `store/postgres/scheduled.go:341`). LV-V3-9 |
| Channel page | COMPLETE | `ChannelViewModel.kt:315-331`, `ChannelApi.kt:58`, by-author tabs `VideoFeedApi.kt:67` |
| Search Videos | COMPLETE | `feature/search/.../SearchNavigation.kt:35` → `SearchApi.kt:67-72` |
| Credit requests | COMPLETE | `CreditsApi.kt:39-49` matches `post-service/internal/http/credits.go:36-46` |
| History clear | COMPLETE | `HistoryViewModel.kt:190-213` → `DELETE /v1/videos/history` |
| Comments sheet | COMPLETE (wiring); DTO fields UNVERIFIED | `T/ui/watch/WatchScreen.kt:355-362` |

### 1.8 Members-only media, offline copies, RSS

- **Members-only media:** COMPLETE. Delivery gate POSTs `/v1/internal/media-access`, post-service checks audience then entitlement, fail-closed, 2 s timeout (`media-service/.../authz.go:195`, `post-service/internal/service/media_access.go:307-315`); only `thumb_*` count as posters (`authz.go:206-208`); captions through the same gate (`media-service/internal/http/clips_handler.go:330,367`); `/v1/videos/:id` nulls `playback_url` (`post_video_tools.go:59-70`). Title and cover are shown by design.
- **Offline:** COMPLETE server side. `/download` owner-only (`media-service/internal/service/download.go:67-78`); grants require audience, `allow_download`, entitlement, a 720/480/360 rendition (never the original), 30-day TTL, 48 h recheck (`post-service/internal/service/offline_copies.go:336-447`). Any viewer may still fetch `/serve/720p` as an MP4 (by design, not DRM). Android at rest unencrypted, device-clock expiry: LV-B-6 stands.
- **RSS:** COMPLETE except one P3. Public, approved, unscheduled, not age-restricted, no tier, not processing (`store/postgres/channels.go:345-353`, `service/channel_feed.go:186-194,274`); private or hidden owner 404 (`:251-254`); banned owner UNVERIFIED. Channel `contact_email` (creator-set) becomes `itunes:email` (web `features/posttube/feed/rss.ts:428-433`). LV-V3-11; LV-B-5 stands.

## Part 1. New defects

```
ID:            LV-V3-1
Title:         Any user can block another user's account erasure forever by pointing a video series or playlist at the victim's channel.
Severity:      P1
Category:      privacy / data (DPDP erasure), authz (mass assignment)
Evidence level: VERIFIED-READ (each line re-read; not run)
Evidence:      video_series.channel_id and playlists.channel_id REFERENCES channels(id) with no ON DELETE
               (post-service/database/migrations/012_posttube_features.sql:8,35; no later migration changes them).
               POST /v1/video-series and POST /v1/playlists store any parseable channel_id
               (post-service/internal/http/video_series_handler.go:201-205,587-590; service/video_series.go:128-139
               checks only the cover; store/postgres/video_series.go:46-49). The victim's channel id is public:
               GET /v1/search/channels returns it as "id" (search-service/internal/http/handler_tube.go:104-106,162).
               user-service PurgeUser runs every step in ONE transaction and deletes `channels` before `users`
               (user-service/internal/store/purge.go:40-48,77-78,125-133); any FK violation aborts it and the comment
               says the consumer "retries with backoff until that service catches up" (:38-40). Nothing ever clears
               another user's series/playlist channel_id (grep: no purge path touches channel_id).
               The tables are shared (Part 1.4), so the FK is live.
How to reproduce: A searches /v1/search/channels?q=<B's name> and reads B's channel id. A sends
               POST /v1/video-series {"title":"x","channel_id":"<B's channel id>"}. B requests account deletion.
               After the grace period, user-service purge fails on DELETE FROM channels (23503) on every retry;
               B's users row, profile and channels remain.
Impact:        A stranger can make another person's erasure request impossible to complete, a legal breach for
               the platform. The same path also fails for a user who set their own channel_id via the API.
Fix:           Smallest: in user-service PurgeUser, before deleting channels, run
               UPDATE video_series SET channel_id = NULL WHERE channel_id IN (SELECT id FROM channels WHERE user_id=$1)
               and the same for playlists (probed with to_regclass like the other steps). Also ignore client
               channel_id in post-service (derive it from the creator) and add ON DELETE SET NULL in a migration.
Acceptance:    user-service purge test: a stranger's series referencing the user's channel does not block the
               purge and its channel_id is NULL afterwards. post-service handler test: a body channel_id is ignored.
Effort:        S
```

```
ID:            LV-V3-2
Title:         Subscribers are never notified of a followers-only upload or of any upload by a private account, because the fan-out reads the post as an anonymous viewer.
Severity:      P2
Category:      functional
Evidence level: VERIFIED-READ
Evidence:      notification-service/internal/service/fanout_eligibility.go:180-210 (no X-User-Id), :281-296;
               a 404 is a terminal skip (:82-84). post-service GetPost with a nil viewer: canViewAuthor(nil) false for
               a private account (post_visibility.go:85); "followers" refuses a nil viewer (:95-98). So the
               followers branch at fanout_eligibility.go:112-115 never runs.
How to reproduce: B follows and subscribes to A (notify_on=all). A publishes a followers-only long video (or A is
               a private account and publishes a public one). B gets no notification; the fan-out row is skipped.
Impact:        The followers audience and private creators lose upload notifications entirely; independent of
               LV-A-1 (it also hits scheduled uploads, which LV-A-1 does not).
Fix:           Read the post state through an internal state route (visibility, review status, deleted, tier)
               that does not apply the viewer gate, and keep the per-recipient follow and block checks already in
               eligible().
Acceptance:    notification-service test with a followers-only post: a follower is delivered, a non-follower is
               skipped; a private-account author's approved follower is delivered.
Effort:        S
```

```
ID:            LV-V3-3
Title:         An age-restricted upload makes every recipient's fan-out fail and retry until the job's attempts are exhausted.
Severity:      P3
Category:      reliability / functional
Evidence level: VERIFIED-READ (status mapping); SUSPECTED (exact retry count)
Evidence:      post_reads.go:62 age gate after visibility; postStateFor returns an error for any status other than
               200 or 404 (fanout_eligibility.go:203-204); map §8 says 5 attempts per job.
How to reproduce: Publish an age-restricted public long video with subscribers; watch the fan-out rows fail.
Impact:        No notification, plus wasted post-service reads per recipient per attempt.
Fix:           Same internal state route as LV-V3-2; decide whether adult subscribers are notified.
Acceptance:    Test: an age-restricted post produces a terminal decision, not an error.
Effort:        S
```

```
ID:            LV-V3-4
Title:         On the web, an expired segment signature mid-playback restarts the video from 0:00 on the MP4, or shows the error state, instead of refreshing the manifest.
Severity:      P2
Category:      functional (playback)
Evidence level: VERIFIED-READ (handler); SUSPECTED (that hls.js escalates the 403 to fatal after its level switch)
Evidence:      features/posttube/components/TubePlayer.tsx:358-383: hls.js has no X-HLS-Expires-At handling and no
               loadSource retry; every fatal non-media error calls useFile() (:319-332), which destroys hls and does
               video.src = fileUrl; video.load(), resetting currentTime to 0, without saving the position (carryRef is
               filled only in the effect cleanup, :389-390). fileUrl prefers a short-lived signed playback_url
               (WatchPage.tsx:258, helper), which may itself have expired. Segments last 2x duration + 15 min
               (media-service/internal/delivery/sign_cache.go). Android handles this correctly (HlsRefresh.kt).
How to reproduce: Open a 10-minute video (segments valid ~35 min), play to 5:00, pause 40 min, press play.
Impact:        Long sessions with pauses (the normal long-video case) lose their place or fail; it is also the
               web half of the LV-B-2 fix (shorter TTLs make it happen sooner).
Fix:           On a fatal network error with a 403 (or once X-HLS-Expires-At passes), save currentTime, call
               hls.loadSource(hlsUrl) again and seek back; fall back to MP4 only on a manifest 404, and carry the
               position into useFile().
Acceptance:    Player test with a mocked 403 on a fragment: one manifest reload, playback resumes at the same time.
Effort:        S
```

```
ID:            LV-V3-5
Title:         Web caption upload always fails: the client sends content_url, which media-service refuses with 400.
Severity:      P2
Category:      functional (accessibility)
Evidence level: VERIFIED-READ (both sides re-read)
Evidence:      features/posttube/data/posttubeApi.ts:615-628 sends content_url = data:text/vtt,... and no content;
               media-service/internal/http/clips_handler.go:405-431 answers 400 CONTENT_URL_NOT_ACCEPTED for any
               non-blank content_url. Callers: upload studio (useUploadStudio.ts:412,589, failure swallowed at
               :590-592) and hub captions (hubApi.ts:1179, EditSheet.tsx:860-862). (First noted in Track B notes,
               not raised there as a finding.)
How to reproduce: Hub edit, Captions, upload any .vtt: the POST /v1/subtitles/<id> answers 400; no track appears.
Impact:        No manual captions can be added from any client (Android has no caption upload, LV-A-2), so the
               Track A "web captions COMPLETE" rating is wrong for upload.
Fix:           Send content: vttContent and drop content_url.
Acceptance:    Web unit test that the request body has content and no content_url; a manual upload shows a track.
Effort:        S
```

```
ID:            LV-V3-6
Title:         The owner can edit a staged post straight to public, skipping the reviewer promotion step.
Severity:      P3
Category:      authz (workflow bypass), latent
Evidence level: VERIFIED-READ
Evidence:      store/postgres/post_edit.go:137-152 checks owner and hold only, never the current visibility;
               the promotion path is scoped to visibility='staged' (store/postgres/posts.go:1598-1617). No current
               create path writes 'staged' (grep), so it is latent; reel_feed.go:182,191 still lists staged long videos.
How to reproduce: With a post in visibility staged, owner PATCH {"visibility":"public"} -> 200.
Impact:        None today; reopens the bypass if staged rollout is used again.
Fix:           Refuse a visibility edit when the current value is staged (409 STAGED_UNDER_REVIEW).
Acceptance:    Service test for the refusal.
Effort:        S
```

```
ID:            LV-V3-7
Title:         Post edits never check author standing, so a suspended or long-video-paused creator can still make a private video public or retitle it.
Severity:      P3
Category:      authz (moderation enforcement)
Evidence level: VERIFIED-READ (no call); SUSPECTED (whether readers hide a suspended author's posts anyway)
Evidence:      requirePublishStanding is called at create (post_create.go:55) and scheduled publish
               (schedule.go:408-418) but nowhere in service/post_edit.go. Title edits are not re-run through the
               copyright title review (helper).
How to reproduce: Suspend a creator in trust-safety; the creator PATCHes a private video to public -> 200.
Impact:        A sanction on publishing does not cover publishing by edit.
Fix:           Call requirePublishStanding when an edit widens visibility or changes the title of a long video.
Acceptance:    Service test: a suspended author's private-to-public edit answers 403.
Effort:        S
```

```
ID:            LV-V3-8
Title:         Flick series (/v1/series) let an owner attach anyone's post as an episode, silently overwrite episodes, and are read without an author gate.
Severity:      P3
Category:      authz
Evidence level: VERIFIED-READ
Evidence:      service/series.go:33-45 checks the series owner only; store/postgres/series.go:83-89
               ON CONFLICT (series_id, episode_num) DO UPDATE; reads series.go:18,29 have no canViewAuthor (helper).
               No client calls /v1/series (grep of Android and web). No authorization test.
How to reproduce: POST /v1/series/<own>/episodes {"post_id":"<stranger's public reel>","episode_num":1} -> 201.
Impact:        Misattribution of other creators' content; latent while no client uses the routes.
Fix:           Apply the video-series rules: own-post check and author gate; refuse an occupied episode number.
Acceptance:    Service tests mirroring tube_audit_test.go:225 for flick series.
Effort:        S
```

```
ID:            LV-V3-9
Title:         Scheduled lists: Android shows "Nothing scheduled" when the load fails and flips the order after a change; web hides the server's scheduling rule and stops at 50.
Severity:      P3
Category:      functional (client)
Evidence level: VERIFIED-READ (helper; Android :257 and server :341 re-read not done by me)
Evidence:      core/feed/.../VideoFeedRepository.kt:257, T/ui/scheduled/ScheduledPostsScreen.kt:112,
               ScheduledViewModel.kt:99 vs post-service store/postgres/scheduled.go:341; web
               app/posttube/scheduled/page.tsx:41,55,82, cursor unused.
How to reproduce: Android offline -> "Nothing scheduled". Web: pick a time 2 minutes ahead -> "Could not reschedule".
Impact:        Creators think their schedule is empty or cannot tell why a time is refused.
Fix:           Error state with retry on Android, one sort order; web shows the server message and pages.
Acceptance:    ViewModel test for the error state; web test for the message.
Effort:        S
```

```
ID:            LV-V3-10
Title:         The web series manager cannot rename or delete a series, although the server routes exist.
Severity:      P3
Category:      functional
Evidence level: VERIFIED-READ (helper)
Evidence:      features/upload/studioApi.ts:250-275 has no PATCH or DELETE; post-service video_series_handler.go:303,408.
How to reproduce: Create a series on the web; look for rename or delete.
Impact:        A mistyped series title is permanent from the product.
Fix:           Add rename and delete to the series manager.
Acceptance:    Web test for both calls.
Effort:        S
```

```
ID:            LV-V3-11
Title:         The RSS "original" enclosure states the original file's size, but an anonymous /serve of original returns the best rendition.
Severity:      P3
Category:      functional (RSS)
Evidence level: VERIFIED-READ (helper)
Evidence:      post-service/internal/service/channel_feed.go:220-227; media-service/internal/service/playback.go:413-427.
How to reproduce: A ready video with only a 360p rendition; compare enclosure length to the bytes served.
Impact:        Podcast clients may reject or truncate the download.
Fix:           Use the rendition actually served (or omit length) for the fallback.
Acceptance:    channel_feed unit test for the fallback.
Effort:        S
```

## Part 2. Candidate PASS items

| Item | Result | Evidence and reason |
|---|---|---|
| (a) Gate 3: owner-only object-ID routes have an authorization check AND a test | **FAIL** | Checks exist on every route read: PATCH post, delete and bulk delete, playlists, video series, pin, captions (`media-service/internal/service/audio.go:146,171`), cover-frame (`post_video_tools.go:141`), trim (:81), category (:115), publish (:191), distribution (:237), chapters / end screens / cards (`video_authoring_authz.go:101`), membership gate, private shares. **Tests missing** for a stranger on: trim, category override, publish, distribution, playlist delete, post restore, the caption owner refusal at service level (only faked in `mtube_contracts_test.go:306`), non-owner add-episode and delete-episode-by-post on video series, and flick-series episode add (which also lacks the post check, LV-V3-8). Schedule 403 is integration-only. `cover-frame` still takes any `thumbnail_url` (LV-B-3) |
| (b) Gate 4: no secrets in long-video code, client config, logs | **PASS (grep), history UNVERIFIED** | Pattern scan (AWS keys, private-key blocks, live payment keys, Google/Slack/GitHub tokens, `secret|token|password|api_key = <16+ chars>`) over post, media, feed, search, notification, user services, Android `feature/tube`, `core/media`, `core/feed` and the six `values-prod.yaml`: only env-variable names. Android HTTP log is BASIC with `chat_open` redacted (`core/network/.../NetworkModule.kt:105-117`). Web exposes only `NEXT_PUBLIC_*` client keys (Firebase, Maps, Giphy) by design. No log line prints keys or signed URLs in these services. Not covered: git history (no gitleaks/trufflehog), tracked `Architecture/docker/local.env` (not opened), media-service `minioadmin` fallback when `MINIO_ENDPOINT` is unset (`cmd/server/main.go:53-58`; prod uses `MEDIA_STORAGE_BACKEND: s3`, `values-prod.yaml:98`) |
| (c) Gate 12: prod config has debug, test accounts, permissive flags off | **PASS** | `ENV: prod` in all six services (`media :84-85`, `post :79`, `feed :52`, `search :61,65`, `notification :68`, `user :61`); the media worker inherits it (`charts/atpost-service/templates/worker-deployment.yaml:102-108`). `LOG_LEVEL: info` everywhere; `gin.SetMode(gin.ReleaseMode)` in all six mains; no pprof import; `MEDIA_SCANNER_ALLOW_STUB: "false"`, `REVIEW_ALL_VIDEOS: "false"`, `WEB_RISK_MOCK` unset, OpenSearch insecure skip absent; `PUBLISH_REQUIRE_STANDING_CHECK` defaults true (`post-service/cmd/server/main.go:217`); no seeded user rows in these services' migrations; web `NEXT_PUBLIC_ENABLE_STUB_PAYMENTS` documented false (`deploy/web/app/values-prod.yaml:11`). Caveat: the admin key on post-service is "NOT enabled yet" (map §7); Azure prod has the scanner off (map §7, not a debug flag) |
| (d1) Unlisted excluded from feeds, search, channel pages, playlists, series, RSS, sitemap | **PASS** | Part 1.1; playlists and series per Track B (`service/playlists.go:289-364`, `video_series.go:285-296`); RSS `store/postgres/channels.go:345-353`; sitemap lists the root only (`app/sitemap.ts`). Caveat: `/v1/posts/batch` returns unlisted by id by design, so client-built "liked" or "reposts" lists can show one |
| (d2) View counting and replay protection | **PASS for replay; PARTIAL overall** | `ingest_receipts` insert `ON CONFLICT DO NOTHING` on the client event id (`analytics-service/internal/store/postgres/events.go:214-224`); cap 1 per viewer per item per day in the daily rollup (`aggregation/daily.go:19-30`). The dedupe key is client-minted, so the cap per account is the real control; staging and Azure count on `play_end` (map §7) |
| (d3) Scheduled publish exactly once | **PASS** | conditional `UPDATE ... WHERE publish_at IS NOT NULL` clears it and the `PostCreated` outbox row is in the same transaction (`store/postgres/scheduled.go:129-171`); producer wired unconditionally (`post-service/cmd/server/main.go:326-328`), so the `s.producer == nil` branch (`schedule.go:452`) is test-only; concurrency integration test `scheduled_integration_test.go:232`. Publish-now does not re-check processing, harmless because readers hide processing posts |
| (d4) Playlist reorder concurrency | **PASS** | playlist row `FOR UPDATE`, then item rows `FOR UPDATE`, two-phase negative positions (`store/postgres/playlists.go:205-224,358-392`); unique `(playlist_id, post_id)` (migration 081) |
| (d5) Subscription idempotency and outbox | **PASS** | `ON CONFLICT (channel_id, user_id) DO NOTHING` and `TubeChannelSubscribed` outbox row in the same transaction (`store/postgres/channel_subscriptions.go:83-109`). LV-B-4 (row written while follow is "requested") stands |
| (d6) Watch progress owned by the caller | **PASS** | user from gateway `X-User-Id` only, readability gate before write (`internal/http/video_series_handler.go:991-1012`) |
| (d7) Transcode lease and duplicate safety | **PASS for state; PARTIAL for compute** | heartbeat lease, 10 min stale, 3 re-queues (`media-service/internal/store/postgres/transcode_lease.go:12-80`); worker skips a delivery that is no longer the asset's current request (`cmd/worker/transcode_lease.go:211-224`); inbox claim and completion in one transaction, already-applied is success (`transcode_inbox.go:12-25`). Two pods can still run the same request concurrently after a rebalance (wasted hours, not wrong state) |
| (d8) Abandoned multipart cleanup | **PASS** | bucket rule `abort-incomplete-multipart` 7 days, empty filter, in the prod media module (`infra/terraform/modules/media/main.tf:167-176`, `envs/prod/main.tf:171`); sweeper started (`media-service/cmd/server/main.go:222`), `FOR UPDATE SKIP LOCKED` expiry then `AbortMultipartUpload` (`store/postgres/resumable.go:109-116`, `service/deletion.go:212-231`). An abort failure is logged and left to the 7-day rule; no user cancel route (reels-92) |

## Still UNVERIFIED and what it needs

- **Prod DSN of post-service and user-service:** needs the `atpost/prod/*` secret contents or the bootstrap Job (founder or AWS access). Migrations force one database, so LV-V3-1 holds either way.
- **hls.js fatal escalation on a 403 fragment (LV-V3-4):** needs a browser run with a mocked 403.
- **Git history secrets:** needs gitleaks or trufflehog installed and run on all three trees.
- **Credited-user resolution depth on edit, banned-owner RSS behaviour, comment DTO fields on Android, admin title-catalogue step-up policy:** each needs one more targeted read.
- **Any journey:** still none executed (staging needed), as in Track A.

## Counts

| Severity | New | Evidence |
|---|---|---|
| P0 | 0 | |
| P1 | 1 (LV-V3-1) | VERIFIED-READ, re-checked |
| P2 | 3 (LV-V3-2, -4, -5) | VERIFIED-READ (LV-V3-4 handler read, escalation suspected) |
| P3 | 7 (LV-V3-3, -6 to -11) | VERIFIED-READ / helper |
