# Track C: data integrity and concurrency (long video)

- **Date:** 2026-11-17. Read-only. Working tree of `codex/module-01-02-launch-safety` (dirty, as in `00-map.md`). Web tree `C:\workspace\wt\pb-echo`.
- **Paths** are relative to `Architecture/services/` unless they start with `deploy/`, `infra/`, `charts/`, `scripts/`, `mobile/` or `web:` (= `C:\workspace\wt\pb-echo`). `media/`, `post/`, `analytics/`, `notif/`, `user/` mean the matching `*-service/` folder.
- **Verdict for this track:** no P0, no P1 of its own. The P1s that hit long video come from the shared pipeline and are listed under "Inherited from reels". Two P2s are new or much worse for long video: an upload that takes more than 24 h can never finish (LV-C-1), and a scheduled long video goes live while it is still transcoding, so its subscribers are never told (LV-C-2). Ten P3s follow. The long-video stores themselves (playlists, series, subscriptions, pins, live VODs, schedule flip) are careful: row locks, conditional writes, unique indexes and outbox rows in the same transaction.
- **Commands run** (all `ok`):
  - `go test ./cmd/worker -run 'Forward|Class|Classify|Route' -count=1 -v` in `media/`: 7 tests, all PASS, including `TestForwardFailureTranscodesHere` and `TestShortConsumerForwardsLongRequests` (10 cases).
  - `go test ./internal/postschedule ./internal/service ./internal/http -run 'WatchProgress|Progress|Schedule|Playlist|DefaultChannel|EndScreen|Stat|Tick' -count=1 -v` in `post/`: 110 PASS, 0 FAIL (including `TestConcurrentTicksPublishExactlyOnce`).
  - Library source read: `segmentio/kafka-go@v0.4.50` `writer.go:609-660,744-768` from the local module cache.
- No integration test was run (no database, by the rules). Every claim about database behaviour comes from reading the code.

## Answers to the brief's questions

| Question | Answer | Evidence |
|---|---|---|
| `media.events.long` absent: does the forward fail fast, or does the short loop run a 48 h job? | **Both, in that order.** The writer has no `AllowAutoTopicCreation`, and the broker has auto-create off. `WriteMessages` first asks for metadata (`partitions`), gets `UNKNOWN_TOPIC_OR_PARTITION` back, and returns at once, with no retry and no backoff. `forwardIfLong` then returns false, and the **short** consumer transcodes the request itself, for up to the 48 h job budget, while every reel on that partition waits. Nothing is lost. Each long request costs a `GetMedia` read plus up to a few MP4 header range reads before the failed write. The fallback is counted as `media_transcode_routed_total{outcome="forward_failed"}` (VERIFIED-READ; VERIFIED-RUN for the fallback via `TestForwardFailureTranscodesHere`). This is reels-3. | `media/cmd/worker/transcode_classes.go:195-224`, `cmd/worker/main.go:229-243`, kafka-go `writer.go:647-650,744-768`, `infra/terraform/modules/msk/topics-job/variables.tf:39,58`, `modules/msk/main.tf:147`, `deploy/services/media-service/values-prod.yaml:176` |
| Is failed processing retried, shown to the creator, and free of duplicate jobs and duplicate videos? | Retried: 8 attempts per message, then the stall sweeper re-queues up to 3 generations, then `failed` (reels Track C "what holds"). Shown to the creator: the uploads list carries `processing_status=failed` and the flag `processing_failed` (`post/internal/service/my_uploads.go:132-160`). The creator has no retry; only a new upload works. No duplicate jobs: per-event advisory lock, inbox primary key, and a reprocess refusal while a job is in flight. A duplicate *post* is possible only through the web draft fall-through (reels-40). Android uses a stable `creationKey` as `Idempotency-Key` (`mobile/android/feature/post/.../createhub/ReelPublishPipeline.kt:256,268`). The failed post is rejected for good (reels-31). | as cited |
| Staging counts on `play_end` while prod counts sessions: a real divergence? | **Yes** (LV-C-9). A long-video session that never sends `play_end` (tab closed, app killed) becomes a view in prod through the 10-minute idle finalizer. Staging and Azure never count it. | LV-C-9 |
| End-screen and card counters (map Q14) | Not open to inflation by one account: owner views are excluded; there is one impression and one click per viewer (user id, or a hash of the client IP) per element per UTC day, through Redis `SETNX` with a 26 h TTL. The residual risks are fail-open on a Redis error, and anonymous viewers keyed on `X-Real-IP` / `X-Forwarded-For` (spoofing not traced, Track B). | `post/internal/service/end_screens.go:1116-1157,1166-1173`, `post/internal/http/end_screens_handler.go:252-257`, `product_tags_handler.go:228-240` |
| `channels` created by both services: which wins, and do the definitions diverge? (map Q9) | **post-service is the authority.** It creates the default channel from `UserRegistered`, and lazily elsewhere (`post/internal/service/channel_default.go:17-99`, `internal/events/consumer.go:240-257`). Whichever migration runs first creates the table. The two definitions **differ**: user-service 001 has `user_id … REFERENCES users(id)`, `icon_url` and `banner_url`; post-service 041 has no FK and neither column, although its comment says it copies "the full user-service column set". Both services still **write** the table (LV-C-6). | `user/database/migrations/001_phase6_channels_pages_reputation.sql:16-34`, `post/database/migrations/041_channels.sql:20-48` |
| `client_sent_at` clamped against the future? (map Q7) | Yes: clamped to server now. A clock that runs *behind* is not clamped. The guard is inert anyway, because neither client sends the field (LV-C-3). | `post/internal/http/video_series_handler.go:977-988` |
| Abandoned multipart: 24 h TTL vs 16 GiB, abort retries, user cancel (map Q11) | The 24 h limit is fixed and cannot be extended (LV-C-1). An abort failure is never retried; the bucket rule cleans up after 7 days (LV-C-8). There is no user-cancel route, so the `aborted` state has no writer. | LV-C-1, LV-C-8 |

## State machine (long video)

"Guard" means what stops a repeated or concurrent delivery from doing harm.

| Object | From → To | Triggered by | Guard | Dead end? | Evidence |
|---|---|---|---|---|---|
| resumable_uploads.status | (none) → `initiated` | client `POST /resumable/init` (multipart opened first, aborted if the row insert fails) | new upload id | — | `media/internal/service/resumable.go:100-174` |
| | `initiated`/`uploading` → `uploading` | each chunk | part upsert on `(upload_id, part_number)`; exact part size | late chunk after complete (reels-93) | `resumable.go:239-300`, `store/postgres/resumable.go:139-150` |
| | → `completed` | `POST /complete` | a second complete re-confirms; a complete after S3 assembly gets NoSuchUpload, then a stat of the declared size | — | `resumable.go:306-361` |
| | open → `expired` (+ one multipart abort) | blob-reclaim worker, 10 min, 200 per tick, `SKIP LOCKED` | the UPDATE moves each row out of the open states once | **upload > 24 h can never complete (LV-C-1)**; abort failure not retried (LV-C-8) | `store/postgres/resumable.go:104-131`, `service/deletion.go:217-231` |
| | → `aborted` | **no writer** (no cancel route) | — | — | `media/internal/http/resumable_handler.go:19-22` |
| media_assets.processing_status | `pending_upload` → `uploaded` → `processing` → `ready`/`failed` (and reprocess, stall re-queue) | confirm, QueueTranscode, worker, sweeper | as in the reels Track C table | `uploaded` with no queue row (reels-32); `manual_review` (reels Track C C-2) | reels Track C |
| | `pending_upload` older than 24 h → deleted | orphan GC every 15 min, by `created_at` | references re-checked under lock | also deletes a slow resumable upload's asset (LV-C-1) | `media/internal/store/postgres/media.go:577-580` |
| transcode class | intake → forwarded to `media.events.long` \| transcoded in place | short consumer | forward acked by all ISRs (`RequiredAcks -1`) before commit | the topic is missing in MSK, so long jobs always run in place (reels-3) | `transcode_classes.go:195-224` |
| posts.review_status | create → `pending` (media not ready) \| `approved` \| `rejected` | `gateVideoReviewStatus` before the insert | — | pending forever on a status-read error or race (reels-1) | `post/internal/service/post_create.go:494-496`, `post_review.go:161-172` |
| | `pending` → `approved`\|`rejected` | media consumer `finalizeReviewGate` | conditional `WHERE review_status='pending'` | flip error swallowed (reels-1); failed → rejected for good (reels-31) | `post/internal/consumers/media.go:353-378` |
| | `flick` → `long_video` (measured over 600 s) | media consumer reclassify | explicit kind kept | — | `consumers/media.go:174-215,261-275` |
| title-review hold | held while the length is unknown → queued or lifted once measured | create / schedule flip; media consumer | retried until durable | — | `service/copyright_title_review.go:352-400`, `consumers/media.go:222-235` |
| posts.publish_at (scheduled) | set → NULL (live) | schedule worker every 30 s, `PATCH /schedule {publish_at:null}`, `POST /v1/videos/:id/publish` | one guarded UPDATE plus the outbox row in one tx; the CAS makes ticks and "publish now" race-safe (VERIFIED-RUN `TestConcurrentTicksPublishExactlyOnce`) | **worker does not wait for the transcode (LV-C-2)**; parked, author not told (reels-98); head-of-line (LV-C-10) | `post/internal/store/postgres/scheduled.go:42-69,114-175`, `service/schedule.go:384-490`, `service/post_video_tools.go:186-208` |
| | set → parked (`post_publish_blocks`) | standing block, terminal copyright error, standing unknown for 24 h | reschedule or "publish now" clears it | author not told (reels-98) | `schedule.go:407-435`, `publish_standing.go:162-191` |
| posts.visibility | any of public / followers / friends / unlisted / private, both ways | owner `PATCH /v1/posts/:id` | — | already-signed segments stay valid up to 12 h (reels-17, longer for long video) | `service/post_edit.go:50,184`, `media/internal/delivery/sign_cache.go:59-67` |
| posts.deleted_at | live → soft-deleted → restored (`POST /:postId/restore`) → purged after 720 h | owner; purge worker | the scheduled flip skips deleted posts; a restore after `publish_at` publishes on the next tick | counts not recomputed at purge (LV-C-5) | `post/internal/http/handler.go:117-121`, `store/postgres/posts_lifecycle.go:310-425` |
| live VOD | `live.stream.vod_ready` → unlisted long video | live VOD consumer | unique partial index on `posts.live_stream_id` | — | `service/live_vod.go:140-215`, `database/migrations/051_mtube_contracts.sql:112-121` |

## Findings

```
ID:            LV-C-1
Title:         A resumable upload has a fixed 24 h life from init, and orphan GC deletes its asset at 24 h, so a long-video upload that takes longer than a day can never finish; Android then restarts it from byte zero.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ (server and Android); SUSPECTED for how often it happens (depends on the uplink)
Evidence:      media/internal/service/resumable.go:38 resumableUploadTTL = 24 h, set once at init; nothing extends expires_at (store/postgres/resumable.go:36-39). UploadPart refuses after ExpiresAt (resumable.go:250-252). Orphan GC deletes 'pending_upload' assets by created_at after 24 h, with no check for an open resumable session (store/postgres/media.go:577-580, orphan_gc.go:38-43); the cascade drops the session rows (reels-92). Prod allows a 16 GiB original (map section 7). Android drops an expired session and opens a fresh one ("The upload took too long and expired. Try again.", mobile/android/core/media/.../upload/ResumableUploader.kt:286-293,377). Web resumes only within one tab session (reels-39).
How to reproduce: Init a resumable upload of 10 GiB; send one chunk every few minutes so that the parts span more than 24 h. The first chunk after hour 24 is refused; status says expired; the asset row is gone.
Impact:        A 16 GiB file needs about 1.5 Mbit/s sustained to finish in 24 h. Creators on slow or intermittent mobile links lose the whole upload and start again, and each restart burns another init from the 10-per-hour upload budget.
Fix:           Sliding expiry: on each accepted part set expires_at = GREATEST(expires_at, now() + 24 h), with a hard cap (for example 7 days, below the bucket's abort rule). Make orphan GC skip assets whose resumable session is still open.
Acceptance:    Unit test: a part accepted at hour 23 moves expires_at to hour 47. Store test: orphan GC does not select a pending_upload asset with an open session.
Effort:        S
```

```
ID:            LV-C-2
Title:         The schedule worker publishes a long video at publish_at even while it is still transcoding; the PostCreated it emits finds the post 'pending', so the subscriber upload notification is dropped for good.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ
Evidence:      PublishScheduled (post/internal/service/schedule.go:384-490) checks standing and the copyright title hold. It does not check media readiness: the flip (store/postgres/scheduled.go:130-136) does not touch review_status, and ListDueScheduledPosts selects on publish_at only (:49-55). The author's own "publish now" does check readiness (post_video_tools.go:196-200, "video not ready" → 409). A video posted before its transcode finishes is stored 'pending' (post_review.go:167-169). Fan-out delivery reads the post and skips it for good when it is not approved (notif/internal/service/fanout_eligibility.go:80-84,199); the later approval emits no new PostCreated (reels-30). The earliest allowed schedule is 5 minutes ahead (schedule.go:42-57); a 4K multi-hour video takes far longer to transcode. The flip also sets created_at to the flip time (scheduled.go:131-135), so a video approved hours later sorts as hours old.
How to reproduce: Upload a 2-hour video, schedule it for now+10 min, and subscribe a second account with the bell on. At the flip the post is live but 'pending'. When the transcode finishes it becomes approved, and the subscriber never gets creator_uploaded_video.
Impact:        "Premiere"-style scheduled uploads, the main use of scheduling for long video, never notify subscribers.
Fix:           Smallest: in the worker path (dueOnly), defer a post whose video_metadata.upload_status is not 'ready' and leave publish_at set, so that the next tick retries. Show "waiting for processing" on the Scheduled list. The reels-30 fix (fan out on the first transition to approved) also covers this.
Acceptance:    Worker test: a due post whose video is processing stays scheduled; after the media becomes ready the next tick publishes it and the PostCreated carries review_status approved.
Effort:        S
```

```
ID:            LV-C-3
Title:         The watch-progress ordering guard (client_sent_at, migration 081) is inert: neither client sends sent_at_ms, so a late heartbeat or a second device can move the resume point back and clear 'completed'.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      The store applies a report only if client_sent_at is NULL or newer (post/internal/store/postgres/watch_progress.go:39-60). The handler reads sent_at_ms (post/internal/http/video_series_handler.go:965-988,1030). Android's WatchProgressRequest has position_ms, duration_ms, completed only (mobile/android/feature/tube/.../data/WatchProgressApi.kt:34-39). Web sends the same three fields (web:src/features/posttube/data/posttubeApi.ts:409-418). A grep for sent_at_ms in both client trees finds nothing. Separately, a device clock running behind is not clamped (only future times are, :984-986): once the field is sent, a skewed device's reports are refused for the length of the skew.
How to reproduce: Watch to 95% on web (completed). Open the same video on Android; the 10-second save from its start position is applied, and the video is back in Continue watching at 0:10. Or retry a stale heartbeat after a newer one: it is applied.
Impact:        Resume points jump backwards across devices and after network retries. Completed videos reappear on the shelf.
Fix:           Send sent_at_ms from both clients (the server side already exists). Optionally also refuse a report whose position is lower than the stored one within N seconds of the stored last_watched_at, unless it comes from a new play session.
Acceptance:    Client unit tests assert sent_at_ms on the wire. A server test exists for the ordering; add one where the second device's earlier timestamp is refused.
Effort:        S
```

```
ID:            LV-C-4
Title:         "Pause history" is a client preference only, and watch_progress has no retention: the server keeps recording from every other device and keeps the rows forever.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      Android save() returns early when its DataStore flag is set (mobile/android/feature/tube/.../data/WatchProgressRepository.kt:41-44). Web checks localStorage posttube_history_paused_v1 (web:src/features/posttube/library/historyPause.ts:2-10,29; WatchPage.tsx:289). The server has no pause setting: SaveWatchProgress always writes (post/internal/service/watch_progress.go:44-69). No sweeper deletes old rows (grep for watch_progress in post/internal outside the store and service finds only the purge paths, store/postgres/posts_lifecycle.go:419, purge.go:187); the Redis mirror has a 90-day TTL (watch_progress.go:14). Clear history deletes the database rows and then the Redis keys; a failed DEL leaves GET /progress answering from the stale hash, because the Redis-first read returns the cached value when the database row is gone (watch_progress.go:72-100,166-193).
Impact:        A viewer who pauses history on the phone still gets a history from the TV or browser. Privacy expectation and data minimisation are not met. Rows grow without bound.
Fix:           A server-side history_paused flag (per user, read in SaveWatchProgress, which then returns 204 without writing). A retention sweep (for example, rows older than 2 years). In GetWatchProgress, treat "database row missing" as authoritative and delete the Redis hash.
Acceptance:    Handler test: paused user → no row written. Service test: Redis hit + database miss → not found.
Effort:        S
```

```
ID:            LV-C-5
Title:         playlists.item_count and video_series.episode_count count items viewers cannot see, and are never recomputed when a post is purged, so the shown counts drift permanently upward.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      Counts are recomputed only by add, move and remove (post/internal/store/postgres/playlists.go:406-408,430-432; system_playlists.go:117-119,144-146; video_series.go:199-201,269-271). PurgePost relies on ON DELETE CASCADE for playlist_items and video_series_episodes (posts_lifecycle.go:310-318) and has no count update (grep for item_count and episode_count in posts_lifecycle.go and purge.go finds nothing). Soft-deleted, private and pending items keep counting while the hydrated item list drops them (service/playlists.go:334-360). The raw item_count is served on end-screen playlist cards (service/end_screens.go:996) and on the search reindex source (http/channels_internal_list.go:161).
Impact:        "12 videos" on a playlist that shows 9; wrong counts in search and on end screens. Display only; no money.
Fix:           Recompute item_count and episode_count for the affected playlists and series inside PurgePost and the account purge (one UPDATE … WHERE id IN (SELECT playlist_id FROM playlist_items WHERE post_id=$1) before the delete). Optionally count only visible items on read.
Acceptance:    Integration test: purge a post that is in a playlist → item_count decremented.
Effort:        S
```

```
ID:            LV-C-6
Title:         user-service still writes the shared channels table through two gateway-reachable routes that bypass post-service's rules and outbox; one of them (ensure-publisher) fails for every account whose channel post-service created.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ (routes, SQL); no client calls either route today (grep)
Evidence:      The gateway sends /v1/users and /v1/onboarding to user-service (api-gateway/cmd/server/main.go:483,526). user-service serves POST /v1/users/me/channels (user/internal/http/handler.go:183-187 → store/channels.go:56-71, a plain INSERT) and POST /v1/onboarding/ensure-publisher (handler.go:210 → store/publisher.go:118-180). Neither writes TubeChannelCreated (post-service writes it in the same tx, post/internal/store/postgres/channels.go:181-191), checks the handle or name rules, or claims the avatar. ensure-publisher looks for `is_default = TRUE` (publisher.go:121-125); post-service never sets is_default (041_channels.sql:43 default FALSE; no is_default in post/internal), so it inserts a second channel. That insert fails on uq_channels_user (041:46) or uq_channels_name_lower (067), and the user gets a 500. The table definitions also differ (see the answers table). The web hook useEnsurePublisher still exists (web:src/hooks/useEnsurePublisher.ts:18-40) with no caller. Legacy GET /v1/users/me/channels is still read (web:src/hooks/useChannels.ts:35-40).
Impact:        Latent. A new client or script calling these routes creates unindexed channels, or gets 500s. The uniqueness indexes prevent duplicate channels.
Fix:           Answer 410 ROUTE_RETIRED on user-service POST /v1/users/me/channels and /v1/onboarding/ensure-publisher, as was done for subscriptions (retired_subscription_routes.go).
Acceptance:    Handler tests: both routes return 410; no INSERT INTO channels remains in user-service non-test code.
Effort:        S
```

```
ID:            LV-C-7
Title:         Upload notifications for a members-only long video go to every subscriber; neither enqueue nor delivery checks tier_required_id.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ for the missing check; SUSPECTED that delivery passes (depends on GET /v1/posts/:id answering 200 for a members-only post to an anonymous caller, not traced)
Evidence:      Enqueue gates are content type, visibility private/unlisted, notify_subscribers and channel (notif/internal/events/subscriber_fanout.go:33-110). Delivery checks exists, approved, visibility and blocks (notif/internal/service/fanout_eligibility.go:61-126); postState reads only visibility, review_status and deleted_at (:186-199). grep -i tier in notif/internal finds only the membership consumers. PostCreated keeps tier_required_id and the title for this reason (post/internal/service/schedule.go:165-180), but no consumer acts on it here.
Impact:        Non-members get "X uploaded <title>" and land on a locked video. Whether that is a feature (upsell) or a leak is a product decision (map Q5).
Fix:           If not intended: skip at enqueue when e.TierRequiredID != "" (or check membership per recipient at delivery).
Acceptance:    Consumer test: a PostCreated with tier_required_id enqueues no job (or only members receive it).
Effort:        S
```

```
ID:            LV-C-8
Title:         An expired resumable session's multipart abort is tried once and never retried; there is no user-cancel route, so an abandoned upload holds storage for at least 24 h and, on an abort failure, until the bucket's 7-day rule (forever on MinIO).
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      ExpireResumableUploads marks the row 'expired' and returns it once (media/internal/store/postgres/resumable.go:104-131). The caller logs an abort failure and moves on (service/deletion.go:217-231). The routes are init, status, chunk and complete only (media/internal/http/resumable_handler.go:19-22). The bucket rule is infra/terraform/modules/media/main.tf:169-176. Related: reels-92 (purge drops session rows without aborting).
Impact:        Storage cost only; a 16 GiB abandoned upload stays billable for a week after an abort failure.
Fix:           On abort failure, write (object_key, storage_upload_id) into media_blob_reclaim for retry. Add DELETE /v1/media/upload/resumable/:id (owner) that aborts at once and marks 'aborted'.
Acceptance:    Service test: abort fails once → a reclaim row exists and the next tick aborts.
Effort:        S
```

```
ID:            LV-C-9
Title:         View counts are built from play_end rows in staging and both Azure environments but from playback sessions in prod and QA; long-video sessions closed without play_end count only in prod.
Severity:      P3 (P2 if Azure prod pays creators)
Category:      data
Evidence level: VERIFIED-READ
Evidence:      ANALYTICS_VIEW_SOURCE is sessions in deploy/services/analytics-service/values-prod.yaml:56 and values-qa.yaml:65. It is absent from values-staging.yaml, values-azure-prod.yaml and values-azure-staging.yaml, and empty means play_end (analytics/internal/viewsource/viewsource.go New). The hourly scan picks a different SQL per source (aggregation/hourly.go:328-332, 384-420 vs :496-501). Sessions are finalized after 10 min idle without a play_end (aggregation/finalizer.go:14-15); on the play_end path a session with no play_end yields no view.
Impact:        Staging cannot reproduce prod view counts or Creator Fund accruals for long video, so a launch rehearsal there validates the wrong numbers. Azure prod, if live, pays on different rules from AWS prod.
Fix:           Set ANALYTICS_VIEW_SOURCE: sessions in the staging and Azure values files (after the backfill the package comment requires), or delete the play_end path once nothing uses it.
Acceptance:    `helm template` for every environment renders ANALYTICS_VIEW_SOURCE=sessions.
Effort:        S
```

```
ID:            LV-C-10
Title:         The schedule worker reads the 100 oldest due posts every tick; posts stuck in standing backoff or failing a transient check stay at the head of that list and can starve newer due posts.
Severity:      P3
Category:      reliability
Evidence level: SUSPECTED (needs more than 100 items failing at once)
Evidence:      ORDER BY publish_at ASC LIMIT 100, excluding only parked posts (post/internal/store/postgres/scheduled.go:49-55). A post in in-memory standing backoff returns an error without being parked (publish_standing.go:162-166,180-190). A non-terminal copyright-check error is retried on every tick with no backoff (schedule.go:425-435). The worker just counts failures and continues (postschedule/worker.go:117-135).
Impact:        After a trust-safety or copyright-check outage involving more than 100 scheduled posts, recovery is oldest-first and newer premieres publish late.
Fix:           Persist next_attempt_at on the post (or in post_publish_blocks with a soft kind) and exclude it in the due query, or page past failures with a cursor within the tick.
Acceptance:    Worker test: 100 always-failing due posts plus one healthy one → the healthy one publishes in the same tick.
Effort:        S
```

```
ID:            LV-C-11
Title:         post-service applies its migrations on every replica boot without the advisory lock, and its chain cannot start from an empty database; channels is missing from schemaDependencies although migration 012 references it.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      BootstrapSchema calls migrationrunner.Run (post/internal/store/postgres/bootstrap.go:27-33). Run takes no lock (Architecture/shared/store/migrationrunner/runner.go:44-110), unlike RunExclusive (exclusive.go:17-90), which commerce, dating, monetization, payments, qa and rider use. post-service has no migrationJob, so every pod migrates on boot (deploy/services/post-service/values-prod.yaml:144-145). 012 references users(id), channels(id) and media_assets(id) (012_posttube_features.sql:7-10); CI needs a stubs file to run the chain (scripts/ci/sql/post-service-stubs.sql:1-48). The gate lists media_assets, user_preferences and users, not channels (values-prod.yaml:158-164; charts/atpost-service/templates/deployment.yaml init container). 081 drops and re-adds a primary key; it is re-runnable, so a second concurrent run would only block, then repeat.
Impact:        Two replicas booting with a new non-idempotent migration: one crash-loops until the other commits (it converges). On a brand-new environment (the AWS move), post-service can start before user-service has created channels and fail at 012 until it does.
Fix:           Switch BootstrapSchema to migrationrunner.RunExclusive (with WithLock around setup.sql), and add channels to schemaDependencies.
Acceptance:    Render shows channels in the init gate; an integration test boots two BootstrapSchema calls concurrently on a fresh database without error.
Effort:        S
```

```
ID:            LV-C-12
Title:         The "Loved" system collection mirrors long-video likes best-effort, outside the like's write; a failed mirror write is never repaired, so Loved and the liked state drift.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      syncLikedCollection logs and ignores errors (post/internal/service/system_playlists.go:107-125). It is called after the reaction write (like_toggle.go:200; post_reactions.go:45,101). No reconciler exists (grep for PlaylistKindLiked finds only these callers).
Impact:        A video shows as liked but is missing from Loved (or the reverse after an unlike), with no self-healing.
Fix:           Derive Loved from reactions at read time, or write the mirror in the same transaction as the reaction.
Acceptance:    Test: mirror store fails once → Loved still contains the post after the next read (or the like fails).
Effort:        S
```

## Inherited from reels (re-checked where stated, unchanged)

- **reels-1** (P1): a post can stay 'pending' forever. Re-checked: `post_create.go:494-496`, `post_review.go:161-166`, `consumers/media.go:371-376` unchanged. Long video is more exposed because the post is almost always created before a long transcode finishes.
- **reels-3** (P1): `media.events.long` is not in MSK. Re-checked `variables.tf:39,58`, `main.tf:147`, `values-prod.yaml:176`. The fallback fails fast and runs the job on the short loop (answers table).
- **reels-4, reels-5** (P1): two short jobs platform-wide; pipeline unmonitored. Apply as is. A long job holds a loop for hours. Not re-read.
- **reels-17** (P2): issued URLs keep playing after delete or privatise. For long video, segment signatures last up to 12 h (`media/internal/delivery/sign_cache.go:59-67`, re-read), not 35 min.
- **reels-27, reels-28** (P2): watch time is client-claimed (clamped to the server duration, `analytics/internal/service/ingest.go:575-583`, re-read); fresh session ids inflate today's views. Unchanged for long video.
- **reels-30** (P2): upload notifications are dropped when the post is pending at create. Re-checked `fanout_eligibility.go:80-84,199`. For long video this is the normal path, and LV-C-2 adds the scheduled case.
- **reels-31** (P2): a failed transcode rejects the post for good. Re-checked `consumers/media.go:356-357`. Long jobs are more likely to hit infrastructure faults (3 stall re-queues over hours).
- **reels-32** (P2): confirm and queue are two commits. Applies; not re-read.
- **reels-34** (P2): a Rekognition outage re-encodes up to 32 times. For a 4-hour 4K video that is days of compute. Not re-read.
- **reels-40** (P2): the web draft publish falls through to a direct create, so the same video can be posted twice. Applies to web long uploads (map 9.2). Not re-read.
- **reels-56** (P2): SIGTERM kills ffmpeg, so every rollout restarts all transcodes. For long video a rollout throws away hours of work. Not re-read.
- **reels-92, reels-93, reels-96, reels-97, reels-98, reels-104** (P3): resumable rows dropped without abort; late chunk reopens a session; retry window; reclaim on mid-run delete; schedule retries for ever and parked posts not notified (re-checked `postschedule/worker.go:117-135`); unclaimed outbox relay. Apply unchanged.

## What holds

| Area | What holds | Evidence |
|---|---|---|
| Subscriptions | PK `(channel_id, user_id)`; subscribe is `ON CONFLICT DO NOTHING` plus the outbox row in one tx, so a retry neither resets the bell nor emits twice; unsubscribe likewise; self-subscribe refused; the bell cannot create a subscription; only `notify_on='all'` rows are paged to fan-out | `post/internal/store/postgres/channel_subscriptions.go:77-166,188-201`, `service/channel_subscriptions.go:36,265,300` |
| Subscriber count | trigger on insert and delete, `GREATEST(0, …)`, so no negatives; no API writes it | `046_channel_subscriptions.sql` (trigger), user-service 004 has the same body |
| One channel per user | `uq_channels_user`; case-insensitive unique name (067); the post-service default channel is race-safe through the unique index | `041_channels.sql:46`, `067_channel_name_unique.sql`, `service/channel_default.go:43-99` |
| Playlists | parent row `FOR UPDATE` on add, move and remove; unique `(playlist_id, post_id)`; positions shifted without overwrite; duplicate add → `ErrPlaylistItemExists`; system kinds unique per owner and idempotent | `store/postgres/playlists.go:205-292,358-437`, `081_tube_integrity.sql`, `system_playlists.go:54-150`, `051_mtube_contracts.sql:143-154` |
| Series | series row `FOR UPDATE`; own non-deleted posts only; one post once per series (NOT EXISTS under the lock); PK `(series_id, episode_num)`; episode reads skip deleted posts | `store/postgres/video_series.go:142-209,295-310` |
| Pinned comment | one per post (unique index); pin is idempotent (`COALESCE(pinned_at, now())`); replies refused | `store/postgres/comment_creator.go:75-104`, `051_mtube_contracts.sql:164` |
| Like, dislike, watch later | like reads the stored reaction and sets the wanted state, counting only a real change; tune `ON CONFLICT DO NOTHING`; watch later idempotent | `service/like_toggle.go:125-205`, `store/postgres/tunes.go:17`, `system_playlists.go:89-126` |
| Watch progress | owner-only (X-User-Id); post readability checked before the write; negative values refused; future timestamps clamped; the database is the record, Redis best effort; account purge deletes the rows | `http/video_series_handler.go:990-1037`, `service/watch_progress.go:44-69`, `store/postgres/purge.go:187` |
| Scheduled publish | CAS flip plus outbox in one tx; restart ticks at once; deleted posts skipped; parked posts excluded (VERIFIED-RUN, schedule worker tests) | `store/postgres/scheduled.go`, `postschedule/worker.go` |
| Live VODs | one post per stream (unique partial index) | `051_mtube_contracts.sql:112-121` |
| Upload fan-out | enqueue durable before the offset commit; job unique per post (`ON CONFLICT (post_id) DO NOTHING`) | `notif/internal/events/subscriber_fanout.go:33-35`, `store/postgres/fanout_jobs.go:62-66` |
| View rule | ≥30 s, or ≥50% under 60 s, judged on the server-measured duration from the ownership projection; per-event receipts; 1 per viewer per day in the daily rollup | `analytics/internal/model/view_rules.go:76-115`, `service/ingest.go:560-600`; reels Track C |
| Soft deletes | playlists and history and continue watching hydrate through `GetPostsByIDs` (`deleted_at IS NULL`) and drop missing posts; feeds (related, subscriptions) hydrate through post-service batch reads; RSS uses `anonymousMayAccessPost` (deleted, scheduled, not approved, not public, age restricted and members-only refused) | `store/postgres/posts.go:1632`, `service/watch_progress.go:211-258`, `service/playlists.go:334-360`, `service/media_access.go:155-185`, `service/channel_feed.go:236-275` |
| Long transcode idempotency | per-event advisory lock held for the job; inbox check; generation fence (reels Track C) | `media/internal/store/postgres/media_outbox.go:177-195`, `cmd/worker/main.go:288-345` |
| Copyright title hold on a still-transcoding video | held until measured, then queued or lifted by the media consumer, durably | `service/copyright_title_review.go:352-400`, `consumers/media.go:222-235` |

## Not verified

- Whether `media.events.long` exists on any deployed cluster (needs the topic list).
- Whether the kafka-go group reader on a missing topic blocks quietly or hot-loops `Fetch error` (the long loop's `continue` has no sleep, `transcode_classes.go:232-238`).
- Whether `GET /v1/posts/:id` answers 200 to the anonymous fan-out read for a members-only post (decides LV-C-7 delivery).
- Whether anonymous (signed-out) playback events are accepted and counted, and how the daily cap groups a nil actor (the ingest handler passes an empty `X-User-Id` through, `analytics/internal/http/handler.go:158`).
- Feed-service: whether timeline fan-out of a pending long video is later surfaced on approval (LV-C-2 feed side); whether a subscriptions purge in user-service (`user/internal/store/purge.go:60`, no outbox event) leaves feed's 6 h `user:subscribed_owners` cache stale.
- The 12 h segment-URL window versus visibility changes (Track B).
- Search `view_count` and `item_count` freshness (reindex only).
- Two workers running the same long job after a lost database session releases the advisory lock (data-safe through the inbox; compute waste not measured).
- Flick-series (`/v1/series`) constraints were not read.

## Notes for other tracks

- **Track D/E:** a long transcode pins one pooled Postgres connection for the whole job, through the session advisory lock (`media_outbox.go:181-194`). A redelivered duplicate blocks in `pg_advisory_lock` with a second connection and a whole consumer loop until the first job ends. Size the pool for this.
- **Track E:** `migrationrunner.Run` without a lock is also used by media, analytics, feed, notification, live-service-v2 and others (grep `migrationrunner.Run(`). LV-C-11 is platform-wide.
- **Track B:** end-screen anonymous dedupe keys on client-supplied IP headers (`product_tags_handler.go:228-240`). Check that the gateway overwrites `X-Real-IP` and `X-Forwarded-For`.
- **Track A:** creators cannot retry a failed processing; Android has no Unlisted at create (map Q13); the history pause wording promises more than the server does (LV-C-4).
- **Track F:** no test covers resumable expiry against orphan GC, a scheduled flip of an unready video, or a purge's effect on counts.
