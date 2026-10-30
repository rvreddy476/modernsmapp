# Audit: reel creation and interaction (post-service), sounds, and the Android reels client

Branch `codex/module-01-02-launch-safety`, read-only. No edits, no git writes, no builds or servers run.
Every finding below was traced end to end through the code path; I dropped anything I could not confirm.
Paths are relative to `/home/user/modernsmapp`.

## Findings by severity

### HIGH 1. One bad scheduled reel draft is retried every minute forever, and 50 of them stop scheduled reel publishing for everyone

- `Architecture/services/post-service/internal/service/drafts.go:755-773` (`PublishScheduledDrafts`)
- `Architecture/services/post-service/internal/service/drafts.go:776-838` (`publishClaimedReelDraft`)
- `Architecture/services/post-service/internal/store/postgres/drafts.go:367-397` (`ClaimDueReelDrafts`, `ReleaseReelDraftClaim`)
- Unvalidated draft writes: `internal/service/drafts.go:390` (caption), `:396` (visibility), `:447` (remix_setting), `:459` (comment_access), `:512` and `:580` (schedule_at, any RFC 3339 time, including 1970)

**What happens.** The worker runs every 60 s on every replica (`cmd/server/main.go:686-699`). It claims the 50 due drafts with the earliest `schedule_at` (`ORDER BY schedule_at ASC LIMIT 50`). When `CreatePost` fails, the draft is parked as `blocked` only for a tier, Action or copyright error. Every other error calls `ReleaseReelDraftClaim`, which puts the status back to `draft` and leaves `schedule_at` as it was. The same draft is therefore at the front of the queue on the next tick.

Several kinds of draft can never publish, and `UpdateDraft`/`CreateDraft` accept every one of them:
- `remix_setting`, `comment_access` or `visibility` outside the allowed values. CreatePost refuses these with `ErrInvalidRemixSetting`/`ErrInvalidCommentAccess` at `post.go:1098-1105`.
- A draft with no media and an empty caption, or a caption over `MaxPostTextRunes`. CreatePost refuses these with `ErrEmptyPost`/`ErrTextTooLong` at `create_guards.go:87-99`.
- A draft whose media has since been deleted.

**Concrete failure.** One user creates 50 drafts with `remix_setting: "x"` and `schedule_at: "1970-01-01T00:00:00Z"`, or 50 broken drafts simply build up over time. From then on, every tick on every replica claims those 50, fails them and releases them. Nobody else's scheduled reel is ever claimed again. The same loop also writes 50 rows and runs up to 50 failing CreatePost attempts per minute per replica.

**Fix.** Treat a validation error from CreatePost as terminal: call `MarkReelDraftBlocked` with a reason, as the tier and Action errors already do. Also validate the enums, caption and `schedule_at` window in `UpdateDraft`/`PublishDraft`, the way `draftPublishUpdates` already checks `content_type`. As a backstop, add an attempt count or backoff to the claim (`next_attempt_at`) so a draft that keeps failing cannot hold the head of the queue.

### HIGH 2. A withdrawn, deactivated or licence-expired library sound can still be put on new reels

- `Architecture/services/post-service/internal/service/audio.go:148-186` (`AttachAudioToPost`: a library sound is checked only for `status == ready`)
- `Architecture/services/post-service/internal/service/sounds.go:355-371` (`soundForViewer`) and `:435-446` (`UseSound` returns an existing library sound on a reel)
- `Architecture/services/media-service/internal/store/postgres/admin_sounds.go:384-427` (`WithdrawAdminLibrarySound`: scope `new_uses` sets only `audio_library.is_active = FALSE`, and the `audio_tracks` row stays `ready`)
- `Architecture/services/media-service/internal/service/audio_reads.go:84-90` (`admitToTrack`: a library sound passes when its status is `ready`), used by `SearchAudioForViewer`

**What happens.** The library is "materialised" into one `audio_tracks` row on first use. Withdrawing with scope `new_uses` is meant to stop new uses while existing posts keep playing. Deactivating the row, or letting the licence lapse, should also stop new uses. In all three cases only `audio_library` changes; the materialised track keeps `status='ready'`. Nothing in post-service joins `audio_library`, and nothing re-checks `license_expires_at` (only the trending ranking and the enable path read it).

**Concrete failure.**
1. An admin withdraws library sound L for new uses because the licence was revoked.
2. Its `audio_track_id` is public: it is `sound.id` on every reel that plays it, and `GET /v1/audio/search` still lists it, because `admitToTrack` admits any `ready` library track.
3. A creator posts `POST /v1/posts {audio_track_id: <L's track>}`, publishes a draft that holds it, or taps "Use this sound" on any reel that plays it (`UseSound` answers it at sounds.go:435-446). `AttachAudioToPost` links it and counts a new use.

The platform keeps distributing a sound whose licence ended, which is a rights exposure. `/v1/audio-library/:id/use` checks `is_active` only, so a sound whose licence has expired but which nobody switched off is also still materialised (`library_sounds.go:110-112`).

**Fix.** Make "may this library sound be newly used?" a decision for media-service: on attach, read `audio_library.is_active`, `withdrawn_at` and `license_expires_at`, through an internal route or a join in `GetAudioTrack`. Refuse a sound that is withdrawn, inactive or expired in `AttachAudioToPost` and in `UseSound`, while existing posts keep playing it. Apply the same rule to `/use` (licence expiry) and to `SearchAudioForViewer`.

### MEDIUM 3. Process death before the video is stashed leaves the reel publish stuck on "Preparing" for good, and the worker crashes (Android)

- `mobile/android/core/media/src/main/kotlin/com/us/android/core/media/upload/MediaSourceResolver.kt:37-38, 52-53` (`resolver.getType` and `resolver.query` are not wrapped)
- `mobile/android/feature/post/src/main/kotlin/com/us/android/feature/post/createhub/ReelPublishStore.kt:318` (`stashVideo` calls `sources.resolve(uri)` outside `runCatching`)
- `mobile/android/feature/post/src/main/kotlin/com/us/android/feature/post/createhub/ReelPublishPipeline.kt:120-126` (`run` catches only its own `Stop`)
- `mobile/android/feature/post/src/main/kotlin/com/us/android/feature/post/createhub/ReelSurface.kt:246-261` (the raw Photo Picker URI, with no `takePersistableUriPermission` anywhere)

**What happens.** When the user posts without going through the studio export, `videoPath` is null. The video is copied ("stashed") inside the worker, and that worker only runs once a network is available. The Photo Picker URI grant lasts only while the app process does.

**Concrete failure.** The user picks a video while offline, taps Post and swipes the app away. Or the process is killed while a 2 GB copy is still running. When WorkManager restarts the run, `resolver.query()` on the picker URI throws `SecurityException`. Nothing catches it, so the exception escapes `doWork`, `CoroutineWorker` returns `failure()`, and the queued publishes behind it in `reel-publish-queue` are cancelled. The record has no `failure`, so `restore()` shows it as `Preparing` and re-enqueues it on every launch, where it crashes again. The user never sees an error or a Retry.

**Fix.**
- In `AndroidMediaSourceResolver.resolve`, wrap the `getType`/`query` calls with `runCatching` and return null, so the step ends as `Unreadable` with a message.
- Stash the copy on Post, in the ViewModel, before enqueueing. Or call `takePersistableUriPermission` on the picked URI.
- Also wrap `pipeline.run` in `doWork` with a catch-all that records a `Failed` state instead of letting the worker fail.

### MEDIUM 4. Shared-reels viewer in chat: the first reel never starts, reels are always silent, and playback continues in the background (Android)

- `mobile/android/feature/chat/src/main/kotlin/com/us/android/feature/chat/ui/SharedReelsViewer.kt:163-166` (`playOnly(settledPage)` runs once per settled page, before the page's player exists)
- `SharedReelsViewer.kt:177` (the player is acquired later, when the post has loaded, with `playWhenReady` still false)
- `SharedReelsViewer.kt:167` (pauses only on dispose; no `ON_STOP` handling)
- `mobile/android/core/media/src/main/kotlin/com/us/android/core/media/di/MediaModule.kt:124` (every factory player starts at `volume = 0f`; this screen never sets a volume)

**Concrete failure.**
1. You tap a shared reel in a chat. `ensure(start)` loads the post over the network. `LaunchedEffect(pager.settledPage)` calls `playOnly` while the page is still showing "Loading", so no player exists yet.
2. When the post arrives, the page acquires a prepared but paused player. Nothing calls `playOnly` again, and there is no tap-to-play, so the reel sits on its first frame.
3. A reel reached by swiping does play, but with no sound and no way to unmute it.
4. If the user presses Home while a reel plays, it keeps decoding and streaming in the background until the screen is disposed.

**Fix.**
- Key the play effect on the settled item being loaded, e.g. `LaunchedEffect(pager.settledPage, state.items[settledId])`, or call `playOnly` right after `acquire` when `page == pager.settledPage`.
- Set the volume from the Reels mute preference, or add a mute toggle.
- Add the same `ON_STOP` → `pauseAll()` observer that `ReleaseOnLifecycle` uses in Reels.

### MEDIUM 5. The reel studio preview keeps playing with sound after the app goes to the background (Android)

- `mobile/android/feature/post/src/main/kotlin/com/us/android/feature/post/createhub/studio/ReelPreviewPlayer.kt:32` (built with `ExoPlayer.Builder(context)` directly, so the volume is the default 1.0 and there is no audio-focus handling)
- `mobile/android/feature/post/src/main/kotlin/com/us/android/feature/post/createhub/studio/ReelStudioScreen.kt:165-167` (released on dispose, but there is no lifecycle pause)

**Concrete failure.** A creator is trimming a clip in the studio and presses Home or switches to another app. The composition is not disposed, so the looping preview keeps playing the video's audio. The `keepInside` loop also keeps seeking every tick. It only stops when the user comes back and leaves the studio.

**Fix.** Add a `LifecycleEventObserver` (or `LifecycleEventEffect(ON_STOP)`) that sets `player.playWhenReady = false` / calls `actions.setPlaying(false)`. Consider building the player with `setAudioAttributes(..., handleAudioFocus = true)` and `setHandleAudioBecomingNoisy(true)`.

### MEDIUM 6. The legacy `/v1/reels/:id/*` engagement routes are a second, unguarded engagement store that is still reachable

- Routes: `Architecture/services/post-service/internal/http/reel_engagement.go:19-34`. The gateway proxies `/v1/reels` (`api-gateway/cmd/server/main.go:548`). Android uses only `/v1/posts/:id/*` (`core/engagement/.../EngagementApi.kt`), so nothing first-party needs these routes.
- Service: `internal/service/reel_engagement.go:43-66` (AddReelComment), `:81-96` (ShareReel)
- Store: `internal/store/scylla/engagement.go:17-61, 82-127, 132-143, 148-189`

Confirmed defects, compared with the canonical `/v1/posts` paths:
- **Comments.** `AddReelComment` writes to Scylla `reel_comments_by_reel`. It has no rate limit (CreateCommentPG uses `rl:comment:`), no idempotency key, no spam or moderation hook, no notification or outbox event, and no delete or report route. Anyone who can open a reel can attach unlimited, unmoderatable text to it, and `GET /v1/reels/:id/comments` serves that text to anyone.
- **Comments list only the current month.** `ListReelComments` (`engagement.go:107-113`) reads `bucket = currentBucket()` only, so on the 1st of each month every earlier comment disappears while `comment_count` still counts it.
- **Counter races.** `ReactToReel`, `UnreactToReel`, `SaveReel` and `UnsaveReel` do a read followed by an insert or delete, then `counter ± 1`, with no LWT. A double-tap or two concurrent retries both pass the read and count twice; an unlike race can drive the count negative. The canonical `React` uses `IF NOT EXISTS`.
- **Likes split across two counters.** A like through `/v1/reels/:id/react` writes the Postgres reaction (the liked list) and Scylla `reel_counts`, but not `reactions_by_post` or `post_counters`. The post's visible like count and `viewer_reaction` never see that like, while the liked list does.
- **Share spam.** `ShareReel` has no rate limit and no dedup (`SharePost` has `rl:share:`) and skips `shareRestricted`. Spamming the route inflates `share_count`.
- **No query timeouts.** None of the legacy Scylla queries pass `.WithContext(ctx)`, so they ignore request cancellation and deadlines.

**Fix.** Retire these routes the way `/view` was retired: answer 410 and name the `/v1/posts/:id/...` replacement. If anything must stay, route it through the canonical service methods (`React`, `CreateCommentPG`, `SharePost`, bookmarks).

### MEDIUM 7. `GET /v1/reels/feed` serves reels from private accounts, blocked users and hidden authors

- `Architecture/services/post-service/internal/service/reel_feed.go:24-97`
- `Architecture/services/post-service/internal/store/postgres/reel_feed.go:13-70`

**What happens.** The candidates are rows with `content_type='reel'` and `visibility IN ('public','staged')`; migration 003 backfilled legacy reels with that type. The service applies the age filter and the members-only redaction, but never `canViewPosts`. That is the author gate (private account, blocks, hidden or deactivated author) which every other public listing uses; see the note at `post.go:2085-2093` about the same gap that was fixed in GetRecentPosts.

**Concrete failure.** User A blocks B, or A's account is private. B calls `GET /v1/reels/feed` and receives A's legacy public reels: id, caption, author id and settings. The same route has other problems:
- It is N+1: two Scylla reads per item, plus one Redis `SISMEMBER` per candidate.
- It reports `viewer_reaction` from the legacy `reel_reactions` table.
- It drops up to 10 over-fetched candidates per page, because the cursor jumps to the end of the over-fetch.
- It ends the feed early (empty cursor) whenever fewer than `limit` unseen rows fall in a window.

**Fix.** Retire the route (no first-party client calls it). Otherwise pass the page through `canViewPosts` and `attachMediaStateToDetails` like the sound page does, and build the cursor from the last item actually returned.

### MEDIUM 8. The sound page fetches view counts with one HTTP call per reel

- `Architecture/services/post-service/internal/service/sounds.go:704-708` (`hydrateSoundPosts` calls `getViewCount` per detail)
- `internal/service/view_counts.go:58-60` (one id per `fetchViewCounts` call)

**Concrete failure.** `GET /v1/posts/by-sound/:id?limit=50` with a cold cache makes up to 50 sequential calls to analytics-service, each with a 1.5 s timeout. If analytics is slow, one page can take more than a minute. The origin card adds one more call.

**Fix.** Call `s.fetchViewCounts(ctx, ids)` once for the page and assign `ViewCount` from the map.

### LOW 9. After backgrounding, Reels comes back frozen with no play glyph (Android)

- `mobile/android/feature/feed/src/main/kotlin/com/us/android/feature/feed/ui/reels/ReelsScreen.kt:1834-1851` (`ReleaseOnLifecycle` pauses on `ON_STOP`, with no matching resume)
- `ReelsScreen.kt:1135-1137` (`LaunchedEffect(view.mayPlay)` is keyed only on view state, which does not change)

**Concrete failure.** Opening the share chooser, a notification, or pressing Home then returning gives a paused reel. Because `view.paused` is still false, no play glyph shows. The first tap "pauses" (the glyph appears) and only the second tap plays.

**Fix.** On `ON_START`, call `pool.playOnly(pagerState.settledPage)` when `view.mayPlay` (for example `LifecycleResumeEffect`, as FeedScreen does at `FeedScreen.kt:138`).

### LOW 10. When the confirm fails, a reel under 100 MB is uploaded again from byte 0, and transient failures are not retried automatically (Android)

- `mobile/android/feature/post/src/main/kotlin/com/us/android/feature/post/createhub/ReelMediaUploads.kt:97-126`
- `ReelPublishWorker.kt:115-118`

**What happens.** On the single-PUT path, the reserved `mediaId` is never checkpointed. If the PUT succeeded but `confirm` failed transiently, Retry reserves a new asset and re-sends the whole file, up to 100 MB, and the first asset is left orphaned. Any transient PUT or confirm failure on this path ends the run as `Failed(retryable)` with no automatic retry. The worker returns success by design, so WorkManager's backoff never applies, and the user must tap Retry even after the network comes back. The resumable path (≥100 MB) retries parts with backoff and has neither problem.

**Fix.** Checkpoint `mediaId` after `reserve`. On resume, call `confirm` and then `status` before re-uploading. For a retryable failure, re-enqueue the same key with an initial delay (a few attempts) instead of stopping.

### LOW 11. A blocked reel draft cannot be re-armed by editing, contrary to its own comment

- `Architecture/services/post-service/internal/store/postgres/drafts.go:399-403` says "The author re-arms it by editing (UpdateDraft sets status back)", but `UpdateDraft` (`internal/service/drafts.go:357-538`) never writes `status`.

**What happens.** A draft parked as `blocked` stays blocked after the author fixes it. Only `POST /publish` with a `schedule_at`, or an immediate publish, revives it.

**Fix.** In `UpdateDraft`, set `status='draft'` and clear `blocked_reason` when the row is `blocked`, or correct the comment and tell clients to re-schedule.

## Checked and found sound

**post-service**
- `/v1/reels/:id/*` read gates: each reel route except unreact/unsave runs `requirePostReadable` (the post detail gate). Batch counts are capped at 100 and filtered by `ViewablePostIDs`.
- "Use this sound" (`UseSound`):
  - it runs the single-post gate, then `hiddenFromViewer`;
  - a sound the viewer may not hear falls back to the reel's own audio;
  - creator consent (`remix_setting=disallow`) is checked against every post that carries the media;
  - a 30-per-hour rate limit applies before the media-service call;
  - the HTTP call has its own 30 s timeout and a 1 MiB body limit.
- Sound attach (create, draft publish): a private sound is the creator's only; the author must pass the media-access decision for the source media (this covers private or blocked source reels); consent is checked; the post's own sound is skipped; the use is counted only when it is new to the post (row lock in `AttachAudioToPost`).
- `attachSounds`: one batched read and one batched audience decision per page; fails closed; no sound is ever in the cached body.
- `GET /v1/posts/by-sound/:id`:
  - the sound must be audible to the viewer;
  - rows are public, live, approved, not scheduled, `flick/reel`;
  - the author gate, age allowance and members-only redaction are applied;
  - the limit is capped at 50 and the cursor is validated;
  - the extras call has a 2 s timeout.
- User's reels tab (`GetPostsByAuthorFiltered`): private-account gate, per-row followers/friends audience (fail closed), limit capped at 50, media state and members redaction.
- Liked reels: keyset cursor, limit capped at 100, viewable filter.
- Saved reels list: limit capped at 50. It returns ids only.
- Retired `POST /v1/reels/:id/view`: answers 410. Views come from analytics-service, with a bounded cache and a 1.5 s timeout.
- Reel draft publish: draft id = post id makes publishing idempotent, and claims use `SKIP LOCKED`. Author standing is checked on both paths, and an invalid tier, Action or copyright hold is parked. The sound is carried over best-effort through the same attach rule.
- Canonical `/v1/posts` react/unreact: LWT-guarded, context-aware.

**media-service (sounds)**
- `POST /v1/audio/extract/:mediaId` is owner-only.
- Sound record and URL reads go through the delivery gate of the source video.
- Withdraw with scope `everywhere` marks the track `deleted`, which silences existing posts.
- Library `audio_url` is checked as a storage key or https URL (no traversal).

**Android**
- `PlayerPool`: a 4-player ceiling with recycling; re-prepares when the content changes at the same index; released when Reels leaves composition; `ON_STOP` pauses.
- Neighbour preload is limited to ±1 and `beyondViewportPageCount = 1`.
- Playback is driven by the settled page, so a fling does not start several videos.
- `ReelSoundPlayer`: a separate audio-only player; detached/released before the pool; follows the video's events; released by its ViewModel as a backstop.
- `SoundPreviewPlayer` (sound page, library, trending, reel form): paused on `ON_STOP` and released in `onCleared`.
- Uploads stream from a seekable stashed file, so a large video is never read into memory:
  - the resumable path checkpoints the session, resumes from the parts the server already has, retries with backoff and has monotonic progress;
  - the presigned PUT runs on IO with the bare client;
  - big uploads are promoted to a foreground worker;
  - the copy into storage checks free space first and lives in `noBackupFilesDir`.
- A missing or members-locked playback returns `null` and is never handed to a player; offline lookups are in memory, so there is no disk I/O on the main thread in `playback()`.
- The studio export (Transformer) is cancelled with its coroutine.

## Not covered
- media-service upload, transcode and playback (another auditor's lane).
- feed-service's reels reader (already fixed).
- I did not run any tests: every finding was confirmed by reading the code path, and none needed a run to settle.
