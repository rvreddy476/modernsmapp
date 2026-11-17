# Reels V4: gap-fill and PASS check

- Date: 2026-11-17. Read-only. Backend and Android: `C:\workspace\modernsmapp` (branch `codex/module-01-02-launch-safety`, dirty working tree read as is). Web: `C:\workspace\wt\pb-echo` (`feat/echo-web`).
- Path prefixes: backend paths are relative to `Architecture/services/`, Android paths to `mobile/android/`, and web paths to the web repo root, unless a path starts with `deploy/`, `infra/` or `charts/`.
- Method: five parallel read-only passes (Android client, web client, search/notification/report/analytics backend, secrets and prod config, the "already solid" claims). The lead then re-read the evidence for the highest-severity items itself: V4-1 (`analytics-service/internal/http/handler.go:74,182-214`), V4-2 (`analytics-service/internal/service/ingest.go:445-503`) and V4-7 (`media-service/internal/http/middleware.go:41-49,104-155`, `cmd/server/main.go:40-44,249-254`). All other rows are VERIFIED-READ by one reader unless marked otherwise.
- Commands run: only existing unit tests, and all passed:
  - media-service: `go test -count=1 -run 'Retry|Quarantine|Stall|Superseded|Gone|Transcode|Classify' ./cmd/worker/ ./internal/store/postgres/`
  - media-service: `go test -count=1 -run 'Caption|Rendition|Status|Delivery|Gate|Authoriz' ./internal/http/ ./internal/service/ ./internal/delivery/`
  - analytics-service: `go test -count=1 ./internal/aggregation/ ./internal/service/`
  - analytics-service: `go test -count=1 ./internal/http -run 'PrivateAnalyticsRouteInventory|ContentInsights'`
  - post-service: `go test -count=1 ./internal/consumers/ ./internal/postschedule/`
  - notification-service: `go test -count=1 ./internal/service -run 'Eligib|Fanout'`
  - Integration tests were not run.
- Neither gitleaks nor trufflehog is installed (checked with `where` and `command -v`), so git history was not scanned.
- Nothing else was executed: no Docker, databases, devices, browser or deployed environment.

## Part 1. UNVERIFIED rows settled

### 1.1 Track A feature table

| Row (Track A) | Android | Web | Evidence |
|---|---|---|---|
| Discovery and trending | PARTIAL. The reels feed, Following/For You, category Explore and sound page are COMPLETE. The hashtag chip opens the general card list (not a reels pager, not filtered to reels). Search: one page, no cursor. Trending (reels or hashtags) is not used on the Reels screen. | PARTIAL. Feed, tabs, Explore categories, sound pages and the hashtag Reels tab are present. Search is one page with no paging. No "trending reels" surface. The hashtag tab sends `content_type=flick` only (V4-P3-12). | Android: `feature/feed/…/ui/reels/ReelsViewModel.kt:488-505`, `core/feed/…/data/FeedApi.kt:52-59,112`, `Discovery.kt:43-48`, `feature/search/…/SearchRepository.kt:40-42,119`. Web: `src/features/reels/data/reelFeedApi.ts:19-37`, `hooks/useReelSearch.ts:20-33`, `src/app/hashtag/[tag]/HashtagClient.tsx:30` |
| Like, comment, share, save, follow | COMPLETE for like and save: optimistic, per-post mutex, rollback, refusal shown (`core/engagement/…/EngagementStore.kt:216,297-358`). Share COMPLETE and hidden on `hide_share`. No repost on reels (COMPLETE). Follow: optimistic with rollback, but a failure is silent on the rail (`ReelsViewModel.kt:1030-1047`). Comment PARTIAL: the glyph is hidden on `no_comments` (`ReelRail.kt:47-52`), but the sheet has no gate, and a `COMMENTS_DISABLED` 403 is worded as "try again" (`core/ui/…/UsCommentsSheet.kt:1017-1022`) | PARTIAL. Like and save are optimistic with rollback but fail silently. Follow is not optimistic. Comments-off is respected in three places. No repost. Defects: V4-15, V4-P3-10 | web `hooks/useReelEngagement.ts:31-78`, `components/ReelRail.tsx:115`, `ReelsScreen.tsx:245-254` |
| View and watch-time tracking | COMPLETE with defects. Events: `play_start` at READY, heartbeat every 5 s, milestones, `play_end`. Session UUID per view, event UUID kept across retries. A loop keeps the same session and counts `loop_count`. Room outbox (5000 events, batches of 100, 5 attempts). Defects: V4-9, V4-P3-8 | PARTIAL. Same events, 5 s heartbeat, UUID session and event ids, no retry (a failed batch is dropped). Defects: V4-10, V4-11, V4-12 | Android `core/analytics/…/VideoWatchTracker.kt:232-256,396-461,562`, `AnalyticsStore.kt:83-200`. Web `src/features/reels/playback/telemetry.ts:15,37-40,109-122`, `useWatchTelemetry.ts:188,218-290` |
| Reporting UI | COMPLETE. More sheet → `POST /v1/reports {entity_type:"post",entity_id,reason,details}`; 14 reasons (copyright reasons on video only); a 409 shows "already reported"; double submit blocked | COMPLETE. Same body, a 409 handled, every other error generic (V4-P3-11) | Android `core/engagement/…/ReportRepository.kt:35-76`, `ReportApi.kt:25-37`. Web `hooks/useReport.ts:42-53,87-91`, `components/ReelReportDialog.tsx:46-53` |
| Edit after publish | COMPLETE. More sheet → `PostEditSheet` → `PATCH /v1/posts/:id` with only changed fields; applied at once through `withEdits` (`PostEditForm.kt:133-160,237-256`) | PARTIAL. No Edit on the stage More menu (`components/ReelMoreMenu.tsx:89`). Creator Hub has `PATCH /v1/posts/:id` but does not invalidate the reels caches (V4-P3-13) | as cited; web `src/features/posttube/hub/hubApi.ts:91-95` |
| Delete / restore | COMPLETE. `DELETE /v1/posts/:id`; 404 treated as already gone; removed from the pager at once. An offline copy outlives the delete (V4-P3-6) | COMPLETE on the stage: optimistic, put back on error, Undo → `/restore` (`reelFeedApi.ts:102-108`, `ReelsScreen.tsx:367-395`). Hub delete calls `DELETE /v1/uploads/:id` and leaves the reels caches stale | as cited |
| What the creator sees after reject, hold or failure | PARTIAL. Before the post exists, "Couldn't post · Retry · Discard" works, but the message shows raw status codes (`createhub/ReelMediaUploads.kt:250`). After the post exists: Hub shows only processing failed, copyright hold and draft "Held"; a moderation rejection is invisible (V4-P3-5) | PARTIAL. The studio says "published" for every reel. Hub reads only `processing_failed` and `review_hold` and never the post status (V4-P3-5) | Android `feature/tube/…/ui/hub/CreatorHubModels.kt:174-260`. Web `useUploadStudio.ts:700-702`, `hubApi.ts:175-225` |
| Hashtags, mentions (render and tap) | Only @mentions and #hashtags become links; no URL autolinking (`core/ui/…/UsRichText.kt:89-120`). Server-side hashtag parsing and indexing is still UNVERIFIED, apart from the trending path (V4-4) | React text only; mentions limited to `[A-Za-z0-9._]` (`src/components/comments/MentionText.tsx`) | — |
| Swipe 50: duplicates and memory | COMPLETE. `emittedIds` de-dups within a load generation (`FeedPagingSource.kt:108,125`); the list is capped at 150 rows (`FeedPager.kt:48-65`); at most 4 players | PARTIAL. No cross-page de-dup and no `maxPages` (`ReelsScreen.tsx:166-174`), so memory grows with item objects only; one mounted player (V4-P3-14) | as cited |
| Android HLS refresh, slow-network behaviour | UNVERIFIED (needs a device) | UNVERIFIED (needs a browser) | — |

### 1.2 Track B "Not verified" items

| Item | Status | Evidence |
|---|---|---|
| Search: are pending, private, deleted or blocked reels indexed? | COMPLETE. Only `public` + `approved` posts are indexed, and an unknown status fails closed. Every status or visibility write bumps the search revision and emits an event. Blocks in both directions and hidden authors are filtered at query time, with a 503 if the block set can't be read. `type=flicks` maps to `{flick, reel}` | `search-service/internal/events/consumer.go:542-575,668-713,771-790`; `shared/events/events.go:871-881`; `post-service/internal/store/postgres/search_eligibility.go:39-165`; `search-service/internal/http/blockscope.go:45-81`; `store/search/blockfilter.go:128-163` |
| Search: `GET /v1/discover/trending` | BROKEN | V4-4 |
| Notifications: blocked actor | COMPLETE. Enforced upstream: engagement and mentions are refused by graph checks; notification-service does not check again itself | `post-service/internal/service/post_visibility.go:51-72`, `mention_tag_privacy.go:137-174` |
| Notifications: pending, deleted or taken-down reel | PARTIAL. New-upload fan-out drops pending reels (V4-6). A mention goes out while the reel is pending and stays after takedown (V4-P3-3). Author inbox rows are deleted on delete; other rows deep-link to a 404; no thumbnail is stored | `notification-service/internal/service/fanout_eligibility.go:61-188`, `note_consumer.go:135-151` |
| Report filing: body, target checks, quota | COMPLETE. Body is entity_type, entity_id, reason (allow-list), details, evidence. The target is deliberately not read (no existence oracle; a test covers it). Quota is 20 per hour, counted in Postgres, and fails closed. GET and PATCH `/v1/reports` are admin-gated, with `?mine=true` returning the caller's own reports. `details` is unbounded (V4-P3-15) | `trust-safety-service/internal/http/handler.go:146-163,238-340`, `service/reporter_quota.go:24-60`, `internal/http/report_target_oracle_test.go` |
| Report → moderation → playback revoked | PARTIAL. Resolving a report changes nothing on the content (V4-5). A takedown through the admin console does revoke new manifest and URL requests at once (the gate reads the post row, and media-service does not cache verdicts). Segment URLs already issued live up to max(5 min, 2×duration + 15 min), capped at 12 h; that is about 35 min for a 10-minute Short, a known trade-off | `post-service/internal/service/media_access.go:94-184`; `media-service/internal/delivery/authz.go:511-513`, `sign_cache.go:54-89`; `admin-service/internal/http/handler_content.go:51-104` |
| Analytics read routes: owner checks | `/content/:id`, `/dashboard/*` and `/creator/me` are COMPLETE. `/content/:id/views` is BROKEN (V4-1) | `analytics-service/internal/http/content_insights.go:95-145`, `dashboard.go:293-295`, `handler.go:74,182-214` |
| Analytics: IP or user agent stored | Neither is stored | grep of `analytics-service/{internal,cmd,database}` |
| Analytics: `play_end` without `play_start` | Accepted (V4-P3-4) | `ingest.go:322-327,548-603` |
| `/v1/posts/by-author/:id/counts` | PARTIAL: hidden reels are counted (V4-P3-2) | `post-service/internal/store/postgres/posts.go:1155-1161` |
| Mass assignment on create, PATCH and draft | COMPLETE for `review_status`, author, `content_type` past the cap, and `publish_at` in the past (5 min to 30 days only). The AI label can be cleared after publish (V4-P3-1) | `post-service/internal/http/create_post_handler.go:30-140`, `post_edit.go:60-105`, `store/postgres/post_edit.go:175-198`, `service/schedule.go:57-68` |
| Repost of a flick | COMPLETE: refused by the server; no client offers it | `post_reposts.go:64-69`; Android `ReelRail.kt:49-52`; web `video-shell/moreRows.ts` |
| Android release cleartext, logging, token storage, offline copies | COMPLETE. Main config denies cleartext. OkHttp logging is `NONE` in release and `BASIC` in debug. The refresh token is AES-GCM under a Keystore key, with backup off. Offline copies sit in `noBackupFilesDir` and are purged 48 h after sign-out. The streaming cache survives sign-out (V4-8) | `app/src/main/res/xml/network_security_config.xml:10`, `core/network/…/NetworkModule.kt:105-119`, `core/auth/…/KeystoreTokenStore.kt:26-124`, `app/src/main/AndroidManifest.xml:30-32` |
| Web CSP and headers, storage route, proxy | No CSP or security headers (V4-14). There is no open redirect to the browser. Proxy path handling is SUSPECTED (V4-13). Proxy header and cookie relaying is P3 (V4-P3-9) | `next.config.ts`, `src/middleware.ts:236-240`, `src/app/api/proxy/[...path]/route.ts:35,66,190-200` |
| ffmpeg external references (B-3), CloudFront cache after delete, gateway media-prefix internals | Still UNVERIFIED: these need a sandboxed ffmpeg run, the AWS edge, or a gateway re-read | — |

### 1.3 Corroboration of earlier findings

- Track A's A-2 (consumer swallows a failed flip) is confirmed and widened. A transient error from `GetVideoMetadataByMediaAsset` is also read as "no post", and the event is committed (`post-service/internal/consumers/media.go:115-130,366-376`). Fix A-2 together with that lookup. Treat only `pgx.ErrNoRows` as "no post".
- Track A's A-7 (web draft publish falls through to a direct create) is confirmed. `POST /v1/reels/drafts/:id/publish` carries no Idempotency-Key (web `data/reelsApi.ts:302-311`). It is not renumbered here.

## New defects

### P1

```
ID:            reels-V4-1
Title:         Anyone, signed out included, can read the view and watch-depth buckets of any content id, including private, pending and deleted reels.
Severity:      P1 (it exposes counts, not content, and ids are UUIDs; by a strict reading of "read another user's data" it could be argued P0)
Category:      security / authz
Evidence level: VERIFIED-READ (re-read by the lead)
Evidence:      analytics-service/internal/http/handler.go:74 registers GET /v1/analytics/content/:contentId/views. :182-214 (GetContentViews) reads no X-User-Id, does no ownership check, and returns display plus views_1s/3s/10s/30s/60s. The only middleware is the internal key (:67-69). The gateway stamps that key on /v1/analytics and lets anonymous requests through (map §3.0; api-gateway/pkg/internalroutes/internalroutes.go:441). The same buckets sit behind the owner gate on /content/:id (content_insights.go:95-145) and /dashboard (dashboard.go:293-295).
How to reproduce: With no token, GET /v1/analytics/content/<private reel id>/views → 200 with non-zero buckets. User A on user B's reel → 200.
Impact:        Leaks the engagement of private, pending and deleted content, and confirms that an id exists.
Fix:           Gate it like GetContentInsights (owner or admin, 404 otherwise), or remove the route (feeds use /internal/content-views).
Acceptance:    httptest: no X-User-Id → 401; A on B's content → 404; owner → 200; admin → 200. Add the route to private_route_inventory_test.go.
Effort:        S
```

### P2

```
ID:            reels-V4-2
Title:         Watch time is client-controlled with no wall-clock bound and no per-viewer cap, and it feeds Creator Fund eligibility.
Severity:      P2
Category:      integrity / monetization
Evidence level: VERIFIED-READ (ingest re-read by the lead; the rollup and monetization reads by one reader)
Evidence:      analytics-service/internal/service/ingest.go:449-454 only requires watched_ms_total ≥ increment and ≤ the max video duration. :480-495 clamps to server duration × (loop_count+1), with loop_count claimed by the client and capped at maxLoopCount (20). Nothing compares watched time with the time since play_start. aggregation/hourly.go:496-502 sums watched_ms over every session, with no per-viewer cap. daily.go:541,569 copies it. monetization-service/internal/store/postgres/creator_fund.go:475-487 and internal/service/creator_fund.go:31,131,140 use it for the 10-hour eligibility threshold.
How to reproduce: As one non-creator account, on a 10-minute reel, send play_start plus one heartbeat with watched_ms_total=12,600,000 and loop_count=20 for each of three fresh session ids. That credits about 10.5 h at once.
Impact:        Creator Fund eligibility can be manufactured with a handful of requests. Paid views stay capped at one per viewer per day.
Fix:           In applySessionUpdate, clamp the growth of watched_ms to (now − first_seen) × playback_speed + slack. In the daily rollup, cap watch time per viewer per content the same way views_display is ranked.
Acceptance:    Unit test: a heartbeat claiming more than the elapsed wall time × speed is clamped. Rollup test: 3 sessions from one viewer contribute at most one session's watch time.
Effort:        M
```

```
ID:            reels-V4-3
Title:         Like, reaction and comment routes skip the review, processing and scheduled gates the detail read applies, so a stranger can engage with a pending, rejected, processing or scheduled public reel, notify its author, and confirm it exists.
Severity:      P2
Category:      authz / privacy
Evidence level: VERIFIED-READ (one reader)
Evidence:      post-service/internal/service/post_visibility.go:51-72 (loadPostForEngagement → GetPost filters on deleted_at only → viewerMayViewPost :78-126, which checks account, visibility and graph, never review_status, processing or publish_at). Compare post_reads.go:36-50, which hides all four. Used by post_reactions.go:28, post_comments.go:147 and reel_engagement.go:53.
How to reproduce: As B, like A's pending or rejected public reel by id → 200 (a random id gives 404); A gets a "liked your post" notice.
Impact:        Existence oracle for held or rejected content; engagement and notifications on content that is not live.
Fix:           In loadPostForEngagement, for non-authors, return ErrPostNotFound unless the effective review status is approved and the post is not hidden from the viewer (processing or scheduled).
Acceptance:    Table test over pending, flagged, rejected, processing and scheduled: stranger → not found, author → OK.
Effort:        S
```

```
ID:            reels-V4-4
Title:         Trending hashtags count tags from pending, private, followers-only, rejected and deleted reels (and from comments on them), are never decremented, and are served to everyone.
Severity:      P2
Category:      privacy / integrity
Evidence level: VERIFIED-READ (one reader). Whether post-service and search-service share one Redis in deploy config is UNVERIFIED
Evidence:      post-service/internal/service/post_create.go:691-693 → schedule.go:253-290 (publicHashtags :157-162 excludes only members-only posts; ZIncrBy trending:hashtags:{date}); post_comments.go:184-201 (+0.5 per tag in any comment). Readers: search-service/internal/http/handler.go:548-575 (GET /v1/discover/trending, straight from Redis) and post-service hashtag_trending_handler.go:249.
How to reproduce: Post a followers-only reel tagged #uniqueTag123; as a stranger, GET /v1/discover/trending lists it, and it stays after delete.
Impact:        Leaks private tags; trending can be gamed with unpublished or rejected content.
Fix:           Bump only when events.SearchEligible(visibility, effective review status) holds; move the pending bump to the transition to approved; apply the same rule to comment tags.
Acceptance:    miniredis test: a pending or followers-only post does not ZIncrBy; a public approved post does.
Effort:        S
```

```
ID:            reels-V4-5
Title:         Resolving a report has no effect on the reported reel: nothing consumes report.resolved, so a takedown needs a separate admin-console action.
Severity:      P2
Category:      functional / moderation
Evidence level: VERIFIED-READ for the services. Whether the admin console chains the two steps is UNVERIFIED (console web not read)
Evidence:      trust-safety-service/internal/service/moderation.go:316-370 (UpdateReport writes status and publishes ReportResolved/Dismissed; no consumer of ReportResolved in any service). POST /v1/posts/internal/moderation is called only by appeal overturn (post-service post_moderation_http.go:133-160). The real takedown is admin-service handler_content.go:51-104.
How to reproduce: File a report on a reel; PATCH /v1/reports/:id {"status":"resolved"} → the reel is still approved, searchable and playable.
Impact:        "Resolved" reports leave violating reels live.
Fix:           Require an action on post-target resolution (none | reject), and on reject call the post-service decision endpoint; or let the console resolve only after the content decision succeeds.
Acceptance:    Integration test: resolve with action=reject → review_status rejected, search removal, media-access denies a non-owner.
Effort:        M
```

```
ID:            reels-V4-6
Title:         "New upload" notifications are dropped for good for any reel still pending or processing at create (the normal path), and for every followers-only upload.
Severity:      P2
Category:      functional
Evidence level: VERIFIED-READ (one reader)
Evidence:      PostCreated is emitted at create while review_status is pending (post-service post_create.go:491-496,528-559). The fan-out reads the post anonymously (notification-service fanout_eligibility.go:180,279-284). GetPost returns 404 for pending, processing or non-public posts (post_reads.go:36-58), which is read as "not exists" and skipped for good (:83-84,187-188). The pending→approved flip emits only PostSearchEligibilityChanged (store/postgres/posts.go:1540-1551).
How to reproduce: B subscribes to A; A posts a reel whose create beats its transcode → B never gets creator_uploaded_flick.
Impact:        Subscriber upload notifications mostly do not work for reels.
Fix:           Fan out on the first eligibility transition to approved (dedupe on post id), or have an internal fan-out-state route and treat pending as retry-later.
Acceptance:    Fan-out test: pending on the first pass, approved on the second → delivered once.
Effort:        M
```

```
ID:            reels-V4-7
Title:         media-service accepts HS256 user tokens in production, where an empty kid takes the active secret, a token without exp never expires, and issuer and audience are not checked; the gateway is RS256-only.
Severity:      P2
Category:      security / config (launch gate 12)
Evidence level: VERIFIED-READ (re-read by the lead). External reachability is limited: the gateway rejects an HS256 token first, so the exposure is to any in-cluster holder of JWT_SECRET
Evidence:      media-service/internal/http/middleware.go:108-119 (HS256 branch, no production switch), :41-43 (empty kid → active secret), :147 (`claims.Exp > 0 &&` — no exp means no expiry), no iss/aud check in :86-156. deploy/services/media-service/values-prod.yaml:51-56 mounts JWT_SECRET ("HS256 stays accepted in parallel"). The same secret is also mounted into post-service and feed-service prod values (:46-49 each), which never read it.
How to reproduce: With JWT_SECRET, mint {"alg":"HS256"} {"sub":"<any user>"} with no exp; call media-service directly in-cluster on a user route (e.g. POST /v1/media/init) → accepted as that user, forever.
Impact:        Any pod that holds the shared secret can act as any user on media routes, with non-expiring tokens.
Fix:           In production, refuse HS256 at boot (require JWT_PUBLIC_KEY_PEM and disable the HS branch), require exp, and check iss/aud as the gateway does. Remove JWT_SECRET from the post and feed prod values.
Acceptance:    Middleware test: with production config, an HS256 token → 401, and an RS256 token without exp → 401.
Effort:        S
```

```
ID:            reels-V4-8
Title:         Android's 256 MiB streaming video cache is not cleared on sign-out, so the next account on the device holds the previous account's watched segments, members-only included.
Severity:      P2
Category:      privacy
Evidence level: VERIFIED-READ (one reader)
Evidence:      core/media/…/di/MediaModule.kt:51-52 (SimpleCache(cacheDir/media)); no sign-out task touches it, and only the image caches are cleared (app/…/ImageCacheTeardown.kt).
How to reproduce: Watch members-only reels as A, sign out, sign in as B; the media cache folder still holds A's segments.
Impact:        Cross-account data remains on a shared device.
Fix:           Add a sign-out task that removes every cached key (SimpleCache.removeResource) or empties the folder.
Acceptance:    Instrumented or unit test: after the sign-out task, cache.keys is empty.
Effort:        S
```

```
ID:            reels-V4-9
Title:         Android reels never send end_reason=ended, and with Auto scroll a finished reel records a phantom loop.
Severity:      P2
Category:      analytics integrity
Evidence level: VERIFIED-READ (one reader)
Evidence:      The only PlayEndReason.ENDED caller is Tube (WatchViewModel.kt:411). Auto scroll seeks to 0 and then scrolls (feature/feed/…/ui/reels/ReelsScreen.kt:1204-1213), the view closes as swipe_next (ReelsViewModel.kt:1089-1090), and the wrap counts a loop (core/analytics/…/VideoWatchTracker.kt:447-456).
How to reproduce: Turn Auto scroll on, let a reel finish, and inspect the queued play_end.
Impact:        Completion rate is under-reported and loop_count inflated, which feeds watch-time clamps (V4-2) and ranking.
Fix:           Call endView(id, ENDED) from the STATE_ENDED listener before the seek and scroll.
Acceptance:    ViewModel test: an ended event → play_end with end_reason=ended and loop_count unchanged.
Effort:        S
```

```
ID:            reels-V4-10
Title:         Web play_end is lost on tab close or navigation: sendEventsOnUnload claims sendBeacon but uses axios.
Severity:      P2
Category:      analytics integrity
Evidence level: VERIFIED-READ (one reader; browser behaviour by specification)
Evidence:      src/features/reels/playback/telemetry.ts:118-122; wired at useWatchTelemetry.ts:278. The API client uses an Authorization header (src/lib/api.ts:143,154), so a plain sendBeacon would also fail.
How to reproduce: Watch a reel for 8 s and close the tab: heartbeats arrive, play_end does not.
Impact:        Sessions close only by server inactivity; end reasons are wrong on the web.
Fix:           fetch(url, {method:"POST", keepalive:true, headers:{Authorization, Content-Type}}), as src/features/webPush/signOut.ts:93 does.
Acceptance:    Unit test: the unload path calls fetch with keepalive:true.
Effort:        S
```

```
ID:            reels-V4-11
Title:         On the web, a backgrounded tab keeps ticking and sends heartbeats after play_end; on return no new play_start is sent, and the final play_end is skipped.
Severity:      P2
Category:      analytics integrity
Evidence level: VERIFIED-READ (one reader)
Evidence:      useWatchTelemetry.ts:275-277 (on hidden: sends play_end and sets startedRef=false, but the tick interval keeps running and the video is not paused); ReelVideo.tsx:251-253.
How to reproduce: Play a reel, switch tabs for 60 s, return, swipe.
Impact:        Watch time is credited while hidden; the session state is corrupt.
Fix:           Pause the video and stop the tick when the tab is hidden; open a new session when it is visible again.
Acceptance:    Hook test with a fake document.visibilityState: no heartbeat after play_end; a new play_start on return.
Effort:        S
```

```
ID:            reels-V4-12
Title:         Web loop_count is always 0 and end_reason is never "ended" with the default loop preference.
Severity:      P2
Category:      analytics integrity
Evidence level: SUSPECTED (from code and the media spec; not run in a browser)
Evidence:      playback/playerPrefs.ts:60 (onEnd "loop") → video.loop=true (ReelVideo.tsx:397). The native loop fires `seeking`, which nulls lastTimeRef (useWatchTelemetry.ts:218-221), so the wrap branch :244-248 never runs; `ended` never fires (:290).
How to reproduce: Watch a 6 s reel three times through; play_end has loop_count 0 and end_reason swipe_next.
Impact:        Same as V4-9, on the web.
Fix:           In onSeeking, count a loop when the last time was within 2 s of the end and currentTime is near 0.
Acceptance:    Hook test simulating timeupdate near the end, then seeking to 0 → loop_count 1.
Effort:        S
```

```
ID:            reels-V4-13
Title:         The web /v1 proxy joins decoded catch-all path segments into the upstream URL, so dot segments may escape /v1/ on the gateway.
Severity:      P2
Category:      security
Evidence level: SUSPECTED (depends on how Next.js decodes %2e%2e in catch-all params; not tested)
Evidence:      src/app/api/proxy/[...path]/route.ts:66 (`${API_GATEWAY}/v1/${path.join("/")}` then new URL()).
How to reproduce: Against a local build, request /api/proxy/..%2f..%2finternal/... and inspect the upstream URL.
Impact:        The proxy may reach gateway paths outside /v1; the gateway's /internal/ refusal still applies.
Fix:           Refuse any segment equal to "." or ".." or containing "/", and re-encode each segment with encodeURIComponent.
Acceptance:    Route unit test: a dot-segment path → 400; a normal path → unchanged upstream URL.
Effort:        S
```

```
ID:            reels-V4-14
Title:         The web app sets no CSP, X-Frame-Options/frame-ancestors, nosniff, Referrer-Policy or HSTS on any route, reels included.
Severity:      P2
Category:      security hardening
Evidence level: VERIFIED-READ (one reader; repo-wide grep including infra)
Evidence:      next.config.ts has no headers(); src/middleware.ts:236-240 sets headers only for /doorstep-pro/digilocker.
How to reproduce: curl -I a local build of /reels: none of these headers.
Impact:        Clickjacking of the signed-in reels stage; no XSS containment.
Fix:           Add headers() with a CSP (frame-ancestors 'none'), nosniff, Referrer-Policy, HSTS and Permissions-Policy.
Acceptance:    A test or build check on next.config headers for "/:path*".
Effort:        S
```

```
ID:            reels-V4-15
Title:         The web reels Follow badge is wrong after about 100 distinct authors: the relationship batch is capped at the first 100 ids, so tapping it follows an account already followed.
Severity:      P2
Category:      functional
Evidence level: VERIFIED-READ (one reader)
Evidence:      src/hooks/useConnections.ts:510 (first 100 ids); authorIds grows with every page (src/features/reels/components/ReelsScreen.tsx:238-239); the whole batch refetches when its key changes.
How to reproduce: Swipe past 100 distinct authors, then open a reel by an author you follow: the badge says Follow.
Impact:        A wrong social state on a core action.
Fix:           Query relationships for a window around the current index, or per active author.
Acceptance:    Hook test with 150 authors: the active author's state comes from a query that includes it.
Effort:        S
```

```
ID:            reels-V4-16
Title:         Transcode quarantine has no metric, alert or replay tool, and an asset quarantined before its first heartbeat stays "processing" under steady load.
Severity:      P2
Category:      reliability / operability
Evidence level: VERIFIED-READ (one reader)
Evidence:      media-service/internal/store/postgres/media_outbox.go:158-172 (insert only); cmd/worker/transcode_guards.go:147,149 (log lines only); no alert on media_transcode_quarantine under deploy/ or infra/. A permanent error in resolveTranscodeSubject (transcode_guards.go:207-213) happens before the heartbeat stamp (main.go:365); the sweeper requeues NULL-heartbeat assets only when the pipeline is idle (transcode_lease.go:147-149).
How to reproduce: Quarantine a message for an asset with a wrong storage key while other transcodes run: the asset stays processing and nobody is alerted.
Impact:        Silent stuck reels; there is no operator signal.
Fix:           Add a media_transcode_quarantined_total counter and a gauge of quarantine rows under 24 h old; alert on any increase; mark the asset failed on a permanent pre-heartbeat error.
Acceptance:    Worker test: a permanent error → counter incremented and asset failed.
Effort:        S
```

```
ID:            reels-V4-17
Title:         The devbox compose stack falls back to secrets published in this public repo (JWT, internal key, moderation HMAC keys, TURN, LiveKit) and to one-year access tokens when its .env misses a key.
Severity:      P2
Category:      secrets / config
Evidence level: SUSPECTED (the devbox .env was not readable)
Evidence:      Architecture/docker/docker-compose.yml: ${JWT_SECRET:-loca***}, ${INTERNAL_SERVICE_KEY:-loca***}, ${POST_MODERATION_HMAC_KEY:-loca***}, ${STORY_MODERATION_HMAC_KEY:-loca***}, ${TURN_SECRET:-atpo***} (:1501,:2473), ACCESS_TOKEN_TTL:-8760h (:422). docker-compose.devbox.yml overrides none of these and adds LiveKit devk***/devs*** (:144-145).
How to reproduce: On the devbox, check whether each of these keys is set in its .env.
Impact:        If any key is missing, anyone reading the public repo can mint tokens or call internal routes on the internet-facing dev stack.
Fix:           Use ${VAR:?required} instead of a default in the devbox overlay.
Acceptance:    `docker compose -f … -f docker-compose.devbox.yml config` with an empty env fails.
Effort:        S
```

### P3 (compact)

Each P3 below is VERIFIED-READ by one reader unless marked otherwise, and each is effort S.

| ID | Title | Evidence | Fix (smallest) | Acceptance |
|---|---|---|---|---|
| reels-V4-P3-1 | The author can clear the AI-generated and altered labels after publish (policy question for the founder) | `post-service/internal/http/post_edit.go:80,95`, `store/postgres/post_edit.go:175,190` | If the label should be one-way, return 409 when changing true→false on a published post | A PATCH that clears the label → 409 |
| reels-V4-P3-2 | Profile counts include private, pending, scheduled and members-only reels for any viewer | `post-service/internal/store/postgres/posts.go:1155-1161`, `service/post_reads.go:540-546` | Non-author counts use the same filters as the by-author list | A stranger's count equals the length of the list |
| reels-V4-P3-3 | Mention notifications go out while the reel is pending and are not removed on takedown, rejection or delete | `post-service/internal/service/schedule.go:257-265`; `notification-service/…/note_consumer.go:147` | Emit mentions on the transition to approved; delete mention rows on takedown or delete | A pending post emits no mention |
| reels-V4-P3-4 | `play_end` without `play_start` is accepted, so today's live display count can be inflated until the nightly cap | `analytics-service/internal/service/ingest.go:322-327,548-603`; `http/content_views.go:30-36` | Drop a `play_end` whose session has no `play_start` | 10 `play_end`s from one actor → hourly display count 1 |
| reels-V4-P3-5 | Neither client tells the creator about a moderation hold or rejection of a published reel. Web always says "published"; Android shows raw status codes before create | web `hubApi.ts:188-225`, `useUploadStudio.ts:700-702`; Android `CreatorHubModels.kt:174-260`, `createhub/ReelMediaUploads.kt:250` | Map post review status to Hub flags; plain wording | Hub row for a rejected reel shows "Rejected" |
| reels-V4-P3-6 | Android: an offline copy survives deleting the reel and can still open it | `PostMoreViewModel.kt:341-361`, `ReelsViewModel.kt:619-622` | Call `offline.remove(id)` on delete | Delete → offline entry gone |
| reels-V4-P3-7 | Android: the owner cannot pin, heart or delete comments on the pinned top reel (`postAuthorId` is "") | `ReelsScreen.kt:376-377`, `CommentMenu.kt:56` | Resolve the author from `head` as well | The owner sees the creator menu on the top reel |
| reels-V4-P3-8 | Android: swiping onto a reel with no playback leaves the previous view open, and a Follow is credited to it | `ReelsViewModel.kt:1034,1045,1088` | End the session before the `probe == null` return; check `contentId` | ViewModel test |
| reels-V4-P3-9 | Web proxy forwards a browser-supplied `x-user-id`, follows redirects to any origin server-side, relays Set-Cookie from them, and logs 500 characters of upstream error bodies | `src/app/api/proxy/[...path]/route.ts:35,121,190-200`, `storageRoute.ts:100-110` | Drop `x-user-id`; allow-list redirect hosts; strip Set-Cookie on non-gateway hops | Route tests |
| reels-V4-P3-10 | Web engagement: silent like/save failures, "Saved" toast before the result, no `isPending` guard, no `cancelQueries` (the live poll races), share counted when the sheet opens, no rollback on not-interested | `useReelEngagement.ts:44-91`, `ReelsScreen.tsx:298-307`, `useReelLive.ts:24-30` | Error toasts, `cancelQueries` in `onMutate`, record the share on the target | Hook tests |
| reels-V4-P3-11 | Report errors are generic on both clients (429 and 400 say "try again"); `details` is unbounded on the server | web `ReelReportDialog.tsx:46-53`; `trust-safety-service/database/migrations/001_initial.sql:11` | Map 429/400 to their own messages; cap `details` at 2,000 characters | A 429 shows the quota text; a long body → 400 |
| reels-V4-P3-12 | Web hashtag Reels tab filters `content_type=flick` only, while the stage accepts `reel` and `short` too | `HashtagClient.tsx:30`, `model.ts:18` | Send all accepted types | Request includes `reel` |
| reels-V4-P3-13 | Web: Hub edit, Hub delete and a new publish leave the `["reels", …]` caches stale | `useHub.ts:368-410`, `useUploadStudio.ts:713-717` | Invalidate or patch the `["reels"]` and `["hub","library"]` keys | Cache test |
| reels-V4-P3-14 | Web: no cross-page de-dup and no `maxPages` on the reels list | `ReelsScreen.tsx:166-174,196-206` | De-dup by id in the memo; set `maxPages` | Two overlapping pages → unique ids |
| reels-V4-P3-15 | Transcode retry window is about 4 minutes, not the "quarter of an hour" its comment says; a blob outage of about an hour permanently fails in-flight uploads | `media-service/cmd/worker/transcode_guards.go:54-60,130-167` | Raise `MEDIA_TRANSCODE_MAX_ATTEMPTS` to about 12, or fix the comment | Backoff test sums to about 15 min |
| reels-V4-P3-16 | Cleanup of an asset deleted mid-transcode is best effort and writes no reclaim row, so late-uploaded segments can leak | `media-service/cmd/worker/transcode_guards.go:368-393` | Insert the prefix into `media_blob_reclaim` | Worker test: gone asset → reclaim row |
| reels-V4-P3-17 | Scheduled posts retry forever every 30 s on a persistent non-standing error; a parked post or blocked draft is not notified | `post-service/internal/postschedule/worker.go:121-130`, `service/schedule.go:407-435`, `reel_draft_queue.go:43-74` | Per-item backoff and park after N, as migration 079 does; notify the author | Worker test: N failures → parked |
| reels-V4-P3-18 | Latent config risks. Image scanning defaults off with no production refusal (prod sets it true today). `ANALYTICS_VIEW_CAP_PER_VIEWER_PER_DAY ≤ 0` silently disables the paid-view cap. Media's internal-key check matches `DEPLOY_ENV == "production"` exactly | `media-service/internal/config/config.go:86`, `internal/service/media.go:186-187`; `analytics-service/internal/aggregation/daily.go:122-134`; `media-service/cmd/server/main.go:281` | Refuse each of these at boot in production | Boot tests |

## Part 2. Candidate PASS items

| Item | Verdict | Own evidence and reason |
|---|---|---|
| (a) Gate 4: no secrets in repo, client config or logs | **PASS for the tracked working tree; UNVERIFIED for git history and the devbox env** | No AWS key ids, private-key blocks, Google keys or live payment tokens in tracked files. The two JWT-shaped strings are unsigned or garbage test fakes (`api-gateway/cmd/server/dormant_products_gate_test.go:257`, Android `feature/auth/src/test/…/SignInSecurityFakes.kt:243`). `.env`, `.secrets/`, `local.properties` and `google-services.json` are git-ignored and not tracked (`.gitignore:36,42`; `mobile/android/.gitignore:4,18`). Tracked env files hold placeholders only. Prod values reference External Secrets only. The Android Banuba and GIPHY keys come from ignored files or the environment at build time (`BanubaLicenseConventionPlugin.kt:34`, `feature/chat/build.gradle.kts:27-34`). Web tracks only `.env.example` with placeholders. Logs: the request logger records the path only (`shared/middleware/logger.go:14`, `media-service/internal/http/middleware.go:248`); OkHttp logging is `NONE` in release. Not a PASS overall: no gitleaks or trufflehog is installed, so history is unscanned. The devbox compose defaults are repo-known secrets (V4-17). Dev placeholders such as `minioadmin` and the backfill DSN are refused or unused in prod |
| (b) Gate 12: prod config for media-service, media-worker, post-service, feed-service | **FAIL** | Cause: V4-7. media-service prod accepts HS256 with no exp, iss or aud check, and the shared `JWT_SECRET` is mounted into post and feed for no reason. What does hold: `gin.ReleaseMode` hardcoded; `LOG_LEVEL` info; no pprof or debug routes; `MEDIA_SCANNER_BACKEND=rekognition` with stub off, and both the worker and the server refuse to start without it in production (`media-service/cmd/worker/main.go:1114-1118`, `cmd/server/main.go:659-669`); IRSA required; stall sweep on; internal-key, HMAC-key, account-age and WEB_RISK_MOCK checks fail closed. There is no `deploy/services/media-worker`: the worker is the `worker:` block of `deploy/services/media-service/values-prod.yaml:153-176`. Smaller items: V4-P3-18 and `FEED_INDEX_LEGACY_READ: "true"` (`deploy/services/feed-service/values-prod.yaml:78`, founder to confirm) |
| (c1) Transcode idempotency, retry cap, quarantine | **PASS** (operability gap V4-16, window V4-P3-15) | The offset is committed only when `handle` returns true (`media-service/cmd/worker/transcode_classes.go:229-255`). Advisory lock per event (`store/postgres/media_outbox.go:177-195`), inbox check and superseded check (`main.go:292-331`). Generation fence under `FOR UPDATE` inside the completion transaction (`transcode_inbox.go:176-248`). Retries are capped, then the message is quarantined; if the quarantine insert fails, the offset is not committed (`transcode_guards.go:130-167`). A duplicate completion is idempotent in post-service (`consumers/media.go:353-379`). Tests run green |
| (c2) Stall sweeper | **PASS** | Thresholds 10 min, 30 min, 3 attempts; tick every 5 min (`media-service/internal/store/postgres/transcode_lease.go:69-76`, `cmd/worker/transcode_lease.go:42,154-160`). Per-row `FOR UPDATE OF m SKIP LOCKED` (`store/postgres/transcode_lease.go:323-339`). A stale generation's completion is refused (`transcode_inbox.go:184-190`). On in prod: the switch is not set |
| (c3) Delete during processing | **PASS** (P3 leak V4-P3-16) | A post delete is soft; the gate denies deleted posts (`post-service/internal/service/media_access.go:105,165`); purge runs after 720 h. Media delete is refused while claimed and records reclaim rows (`media-service/internal/store/postgres/media.go:392-447`). The worker skips and cleans up a gone asset at each of three points (`cmd/worker/main.go:342-345,373-380,467-471`) |
| (c4) View rule | **PASS for paid views** (one per viewer per video per day; server-side duration; replay dedupe by event id). **FAIL for watch time** (V4-2) | Default cap 1 (`analytics-service/internal/aggregation/daily.go:23-34`), ranking per viewer and content (`:489-525`), not overridden in prod (`deploy/services/analytics-service/values-prod.yaml:56`). Duration comes from the ownership projection (`ingest.go:369-377,442`), and a session with no server duration never counts (`store/postgres/sessions.go:404-413`). `ingest_receipts ON CONFLICT DO NOTHING` (`store/postgres/events.go:212-225`). Anonymous ingest is refused (`ingest.go:167-170`). Monetization reads the capped figures (`monetization-service/internal/store/postgres/creator_fund.go:444-487`). Watch time is uncapped and client-inflatable |
| (c5) Media access gate fails closed on captions, renditions and status | **PASS** | Every handler calls the gate even where the route has no auth middleware: subtitles, track and status (`media-service/internal/http/clips_handler.go:130,330,367` → `service/caption_read.go:79-93`); renditions (`service/renditions.go:137-156`); status (`service/record_read.go:106-122,173-179`); url, serve and hls (`service/playback.go:123-247`); hls-seg only for anonymous scope (`http/handler_anonymous.go:50-61`). A transport error or a bad status → unresolved → 503; a deny → 404 (`delivery/authz.go:273-327,408-435`, `http/delivery_viewer.go:54-70`). Post-side, pending, deleted and scheduled posts are denied to non-owners (`post-service/internal/service/media_access.go:99-111,165-171`). Nit: a raw store error in `GetMediaURL`/`GetHLSPlaylist` returns 404 instead of 503, which still fails closed |
| (c6) Scheduled and draft publishing | **PASS** (P3 V4-P3-17) | A guarded single UPDATE plus the outbox row prevents a double publish (`post-service/internal/store/postgres/scheduled.go:129-181`). Drafts use `SKIP LOCKED` and the draft id becomes the post id, so a retry is a unique violation (`store/postgres/drafts.go:409-430`, `service/drafts.go:827-840`). A scheduled flip never touches `review_status`, and drafts publish through `CreatePost` (`schedule.go:113-120`). Nothing leaks before `publish_at`: listings filter on `publish_at IS NULL` and the gate denies. Draft attempts cap at 30, then `blocked`. Composer-draft scheduling (`PublishScheduledPostDrafts`) was not read |

## Still UNVERIFIED and what it needs

- Git history and image secrets: install gitleaks or trufflehog in CI and run it over the full history.
- Devbox `.env` contents (V4-17): someone with devbox access checks which keys are set.
- Browser-only behaviour behind V4-12 (loop events) and V4-13 (Next.js dot-segment decoding): a local build plus a browser or curl test.
- Android HLS refresh, slow-network behaviour, memory over 50 swipes on a real profile: a device run, started by the founder.
- Admin console: whether resolving a report is chained to a content decision (V4-5). Needs a read of the admin web.
- Whether post-service and search-service share one Redis for trending (V4-4). Needs a read of the deploy values.
- Whether `/v1/uploads/flicks` returns a moderation status the clients could show (V4-P3-5).
- ffmpeg external-reference probing (B-3) and CloudFront serving after delete (B-4): a sandboxed ffmpeg run and the AWS edge.
- Server hashtag parsing and indexing for mentions and hashtags, beyond trending.
- How the server treats heartbeats after `play_end`, and speed-scaled increments.
