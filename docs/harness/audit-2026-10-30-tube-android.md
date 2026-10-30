# Audit: Android long video (Tube / PostTube)

Branch `codex/module-01-02-launch-safety`. Read-only audit: no edits, no git writes, no Gradle builds, no device.
All paths are relative to `mobile/android/`.

Modules covered: `feature/tube` (watch screen, player, PiP, chapters, end screens and cards, series, history, collections/Watch later/Liked, channel page, discovery), `core/media` (player factory, data sources, captions, load indicator, resumable upload), `core/feed` (playback resolution, paging, hydration, watch history, offline), `feature/post/createhub` (long video publish pipeline and worker), plus the sign-out path in `core/auth` and the watch entry points in `app/.../UsNavHost.kt`.

Every finding below was traced end to end in code. Anything I could not confirm was dropped (for example, screen-on while playing, and the 6-hour dataSync foreground-service timeout on Android 15).

---

## HIGH

### H1. Any playback error leaves the player dead: no recovery for expired segment URLs (403), and Play does nothing
- **Files:**
  - `feature/tube/src/main/kotlin/com/us/android/feature/tube/ui/watch/WatchViewModel.kt:339-366`: the only `Player.Listener` handles `onPlaybackStateChanged` and `onIsPlayingChanged`. It has no `onPlayerError`.
  - `WatchViewModel.kt:754-762` (`togglePlay`): flips `playWhenReady` and never calls `prepare()`.
  - `feature/tube/src/main/kotlin/com/us/android/feature/tube/ui/watch/WatchControls.kt:217`: Retry (`player::prepare`) is shown only for `VideoLoadState.STALLED`.
  - `core/media/src/main/kotlin/com/us/android/core/media/ui/VideoLoading.kt:297-302`: `STATE_IDLE` counts as "not waiting", so after a fatal error the overlay and its Retry go away.
  - `core/media/src/main/kotlin/com/us/android/core/media/di/MediaModule.kt:113-115`: the player is built with the default load-error policy.
  - No `onPlayerError` exists anywhere in `feature/tube` or `core/media/ui` (grep).
- **Scenario:** HLS media playlists embed signed segment URLs. ExoPlayer loads a VOD media playlist once and never reloads it. Server segment URLs live 2x the duration + 15 min, capped at 2 h. A 2.5 h video therefore hits 403 on every segment past about the 2 h mark of wall-clock time. A 40-minute video paused for 1 h 40 min (phone left on the table) hits 403 when Play is pressed. `playback_kind: original` (a signed MP4) expires the same way.
  - On 403, Media3 excludes variants and then retries a few times. It then throws a fatal error and goes to `STATE_IDLE`. The spinner shows for a few seconds, then disappears (IDLE is not "waiting"), and the last frame stays frozen.
  - The centre button shows Play (`isPlaying` is false). The first tap sets `playWhenReady = false`, the second sets it to `true`, and nothing happens either way, because an errored player needs `prepare()`.
  - The only way out is to leave the screen and reopen the video. That resumes from the last progress report.
- **Fix:**
  - Add `onPlayerError` to `WatchViewModel.listener`. On an HTTP 401/403/410 cause (`HttpDataSource.InvalidResponseCodeException`, or `ERROR_CODE_IO_BAD_HTTP_STATUS`): note `currentPosition`, rebuild the source with a fresh `HlsMediaSource`, which re-fetches the gateway playlists (re-read the post first for a `Progressive` source), then `prepare()`, `seekTo` that position and restore `playWhenReady`. Cap it at 2 or 3 attempts per item.
  - Make `togglePlay` call `prepare()` when `playbackState == STATE_IDLE && playerError != null`.
  - Show the error state with Retry for `IDLE` + `playerError`.

### H2. The previous account's History, Watch later and Liked lists appear in "Up next" after signing out and in as someone else
- **Files:**
  - `feature/tube/src/main/kotlin/com/us/android/feature/tube/data/TubeQueue.kt:21-31`: a `@Singleton` holding a `MutableStateFlow<List<FeedItem>>`. It is never cleared.
  - Writers: `ui/history/HistoryViewModel.kt:204-207` (History rows), `ui/collections/CollectionViewModel.kt:315` (Watch later, Liked, private collections), `ui/home/TubeHomeViewModel.kt:246` (Continue watching), `ui/saved/SavedVideosViewModel.kt:76`, `ui/offline/OfflineViewModel.kt:90`.
  - Reader: `ui/watch/WatchViewModel.kt:218` (`upNext = combine(queue.items, ...)`). `ui/watch/UpNext.kt:19` offers the whole list when the current id is not in it.
  - `WatchViewModel.kt:487`: a stale queue row is also used as the item itself, with the previous viewer's `viewer` flags, until the detail refresh lands.
  - Sign-out: `core/auth/src/main/kotlin/com/us/android/core/auth/AuthRepository.kt:309` runs the `SessionTeardownTask`s. No feed or tube task clears the queue (implementations exist only for dating, chat, push, analytics and offline).
  - Entry points that open Watch without setting the queue: `app/src/main/kotlin/com/us/android/navigation/UsNavHost.kt:687` (push link), `:1695` (notification), `:1750` (long video from a profile).
- **Scenario:** Account A opens a video from History (or Watch later, or Liked), so the queue holds A's history. A signs out in Settings; the process lives on. B signs in, taps a notification or opens a long video from a profile. B's Watch screen shows A's watch history (or private Watch later or Liked list) under "Up next", and again on the end screen.
- **Fix:** Bind a `SessionTeardownTask` that calls `queue.set(emptyList())`, or key the queue by viewer id and return nothing on a mismatch. Also clear or replace the queue when Watch is opened from a surface that does not set it.

---

## MEDIUM

### M1. The long-video player takes no audio focus and ignores headphones being unplugged
- **Files:**
  - `core/media/src/main/kotlin/com/us/android/core/media/di/MediaModule.kt:113-125`: `ExoPlayer.Builder(context).setLoadControl(...).build()` without `setAudioAttributes(..., handleAudioFocus = true)` or `setHandleAudioBecomingNoisy(true)`.
  - `feature/tube/.../ui/watch/WatchViewModel.kt:296-299`: turns the volume to 1 and changes nothing else. A grep shows only `feature/dating/.../ClipPlayerUi.kt:95-102` sets these.
- **Scenario:**
  - Unplugging wired headphones, or a Bluetooth headset dropping, carries on playing the long video out loud through the speaker.
  - An incoming call, a navigation prompt or another music app does not pause it or duck it.
  - It plays over Spotify instead of taking focus.
  - Reels and feed players are muted, so this matters mainly for Tube.
- **Fix:** In `WatchViewModel` (or a Tube-specific factory), call `player.setAudioAttributes(AudioAttributes.Builder().setUsage(C.USAGE_MEDIA).setContentType(C.AUDIO_CONTENT_TYPE_MOVIE).build(), true)` and `player.setHandleAudioBecomingNoisy(true)`.

### M2. Several Watch players stay alive, each holding a decoder, when Watch screens stack
- **Files:**
  - `feature/tube/.../navigation/TubeNavigation.kt:263`: `navigateToWatch` pushes a new `WatchRoute` every time, without `launchSingleTop`.
  - `WatchViewModel.kt:296`: each Watch ViewModel builds its own `ExoPlayer`.
  - `WatchViewModel.kt:831-839`: `onBackground` only sets `playWhenReady = false`. The player stays prepared with its decoders and up to 10 s of buffer.
  - `WatchScreen.kt:669-679`: `PauseOnBackground` calls only that.
- **Scenario:** Watch, then the credit or author's channel (`onOpenChannel`), then a tile, then Watch. Or Watch, then a notification (`UsNavHost.kt:1695`), then Watch. Or the end-screen element "channel", then a video. Every hop leaves the previous player prepared. `PlayerPool.kt:21-25` documents that mid-range devices throw when hardware decoder sessions run out. With the Reels pool's 4 players also alive in the back stack, a 3rd or 4th stacked Watch can fail to get a decoder, or force a software fallback.
- **Fix:** On `ON_STOP` when not in PiP, call `player.stop()` after recording the position, and on `ON_START` call `prepare()` + `seekTo`. Alternatively make `navigateToWatch` single-top and route repeat opens through `viewModel.open(id)`.

### M3. After backgrounding (or leaving for the channel and coming back), the rest of the viewing is never tracked, and likes, saves and subscribes lose attribution
- **Files:**
  - `WatchViewModel.kt:831-839`: `onBackground()` ends the view and nulls `watchSession`. The comment at :835-837 says "Coming back to the foreground opens a fresh one", but nothing does.
  - `WatchScreen.kt:669-679`: observes `ON_STOP` only.
  - `startWatchAnalytics` is called only from `load()` (`WatchViewModel.kt:525`).
  - `recordEngagement` (`:959-961`) is a no-op while `watchSession == null`.
- **Scenario:** The viewer checks a message mid-video (or closes PiP, or opens the channel from the author row), comes back and presses Play. Everything watched from then on sends no view or heartbeats, so the creator is not credited for it. `LIKE`, `SAVE`, `SHARE` and `FOLLOW_FROM_CONTENT` from this screen are silently dropped for the rest of the session.
- **Fix:** On `ON_START` (or the first `onIsPlayingChanged(true)` after a background), start a fresh session with `startWatchAnalytics(currentItem)` when `watchSession == null` and content is Ready and unlocked.

### M4. A multi-GB long-video upload gives up after about 31 s without network and waits for a manual Retry
- **Files:**
  - `core/media/src/main/kotlin/com/us/android/core/media/upload/ResumableUploader.kt:295-311` and `:323`: 6 attempts with backoff of 1+2+4+8+16 s, about 31 s in total.
  - `:241`: `Exhausted` returns `Failed(retryable = true)`.
  - `feature/post/.../createhub/ReelPublishWorker.kt:115-117`: maps that to a Failed tile and `Result.success`.
  - `:95`: a later run without the retry flag exits at once because `pending.failure != null`.
- **Scenario:** A 3 GB upload is two hours in when the phone loses signal for a minute (a lift, a train tunnel). The part's retries run out, the publish shows "The upload didn't finish..." and stops. The session is kept, so a manual Retry resumes, but nothing resumes on its own even though the worker has a `CONNECTED` constraint. If the user does not notice for a day, the server session expires and the upload restarts from byte 0.
- **Fix:** For a retryable failure that kept its resumable session, return `Result.retry()` with exponential backoff instead of persisting a terminal failure. Retry does not cancel the unique chain. Mark it Failed only after N worker attempts.

### M5. Collections, Watch later and Liked load every item at once and fan out one request per item
- **Files:**
  - `core/feed/src/main/kotlin/com/us/android/core/feed/data/VideoLibraryRepository.kt:136-145`: `api.items(collection.id)` has no limit or cursor, and the server's `GetPlaylistItems` (`post-service/internal/store/postgres/playlists.go:285`) has no limit either.
  - The whole list then goes through `hydrator.hydrate`. `core/feed/.../HashtagPostHydrator.kt:75-78` launches one `async` HTTP call per author and per media id, all at once and unbounded.
  - `feature/tube/.../ui/collections/CollectionScreens.kt:212` renders it as one list.
- **Scenario:** A user with 1,500 liked videos opens Liked videos. One large response comes back, then about 1,500 delivery calls plus author calls are queued on OkHttp, which allows 5 per host. The page shows a spinner for tens of seconds and holds every `FeedItem` in memory. The same list then becomes the `TubeQueue`.
- **Fix:** Page the items (limit and offset or cursor, server and client) and hydrate per page. Bound the hydrator's concurrency, for example `Semaphore(8)`, or use a batch delivery endpoint.

---

## LOW

### L1. Opening a video that is not in the queue keeps the old one playing, with sound, behind "Loading video"
- **Files:** `WatchViewModel.kt:463-469` (`open()` does not pause) and `:481-489` (`load()` shows Loading, then may `fetch()` over the network before replacing the media source).
- **Scenario:**
  - A series episode row, or a mid-video card that targets a video, is not in the queue, so it is fetched. The previous video keeps playing audibly under the spinner for the length of the request.
  - If the old video reaches ENDED in that window, `startCountdown()` runs against the new `_currentId`. `load()` never cancels the countdown, so 10 s later the episode after the one the viewer picked starts.
- **Fix:** In `open()`, call `player.pause()` (or `stop()`), and call `cancelCountdown()` at the top of `load()`.

### L2. `reloadAfterUnlock` races the main load
- **File:** `WatchViewModel.kt:476-479`. The reload is launched on `viewModelScope` outside the `collectLatest` at `:399`.
- **Scenario:** The viewer joins, then taps an Up-next row before the server re-read returns. Both `load()` calls run, and whichever finishes last owns `_content` and the player. The unlocked old video can then play while `_currentId` is the new one.
- **Fix:** Route the reload through the same flow (for example a `reloadTick` combined into the `collectLatest`), or check `_currentId.value == postId` before applying in `load()`.

### L3. Open sheets and messages are drawn into the PiP window
- **File:** `WatchScreen.kt:216-265`. Only `WatchBody` is swapped for `PictureInPicturePlayer`. `UsMessageHost`, `WatchSheetsHost` (comments, More, settings, collection picker) and the Share sheet are still composed while `inWindow`.
- **Scenario:** The viewer has comments open while the video plays, then presses Home. Auto-enter shrinks the activity, and the sheet covers the tiny PiP video.
- **Fix:** Skip `WatchSheetsHost`, the Share host and `UsMessageHost` while `inWindow`, or close the sheets when PiP starts.

### L4. History "Clear all" can come back from an in-flight Load more
- **File:** `HistoryViewModel.kt:123-139` (`loadMore` appends `now.rows + page`) and `:181-198` (`clearAll` empties the rows, but does not cancel or ignore the pending append).
- **Scenario:** The viewer scrolls to the end, so Load more is in flight, then taps Clear all. The server clears, and then the late page is appended, so cleared history reappears until the next refresh.
- **Fix:** Keep the append `Job` and cancel it in `clearAll`, or ignore an append whose starting cursor no longer matches `nextCursor`.

### L5. An age-restricted refusal reads as a generic failure
- **File:** `WatchViewModel.kt:489-493` and `:594-595`. `fetch()` discards the `AppError`.
- **Scenario:** The server's `AGE_RESTRICTED_SIGN_IN`, `AGE_RESTRICTED` or `AGE_UNVERIFIED` (`post-service/internal/service/age_gate.go`) shows "We couldn't load this video." This is not a privacy hole, because the server enforces the gate.
- **Fix:** Map these codes to their own message, with a "confirm your date of birth" action for `AGE_UNVERIFIED`.

### L6. "Pause recording" for History applies to the whole device, not the account
- **File:** `feature/tube/.../data/WatchProgressRepository.kt:124` reads `WatchHistoryPauseStore`, which is a single DataStore flag.
- **Scenario:** A pauses history recording and signs out. B signs in, and nothing B watches is recorded, with no sign of why.
- **Fix:** Key the flag by user id, or reset it in a sign-out teardown task.

### L7. Tube uses the Reels buffer profile (10 s max buffer)
- **File:** `core/media/src/main/kotlin/com/us/android/core/media/PlayerPool.kt:188-203` (MIN 2 s, MAX 10 s, start at 250 ms), applied to every player by `MediaModule.kt:114`, including `WatchViewModel.kt:296`. The KDoc itself says these values are "tuned for short vertical video".
- **Scenario:** A long video on a fluctuating mobile connection has only 10 s of headroom, so it rebuffers far more often than with the Media3 defaults (50 s).
- **Fix:** Give `PlayerFactory` a long-form profile, for example `create(longForm = true)` with Media3 default or larger buffers, and use it from `WatchViewModel`.

---

## Checked and sound

- **One player per Watch ViewModel, released.** It is released in `onCleared` with its listener removed, after a last progress report on the app scope (`WatchViewModel.kt:963-971`, `:742`).
- **Pause on background.** `ON_STOP` of the nav entry pauses, reports progress and drops a running countdown (`WatchScreen.kt:669-679`, `WatchViewModel.kt:831-839`).
- **PiP:**
  - Eligible only while playing, not ended, not locked, and with a playback.
  - Auto-enter is set only while eligible, and cleared when the screen is disposed.
  - The play/pause receiver is `RECEIVER_NOT_EXPORTED`, its intent is package-scoped and `FLAG_IMMUTABLE`, and it is unregistered on dispose.
  - Before Android 12 it enters through the user-leave hint, wrapped in `runCatching` (`WatchPictureInPicture.kt`).
  - PiP pauses rather than stops the activity, so playback continues as intended; closing the window stops the activity, which pauses playback.
- **Members-only gate fails closed:**
  - `membersPlayback` treats redacted rows, or gated rows without media, as Locked (`core/model/.../MembersOnly.kt:71-96`).
  - The lock is decided before `prepare()` (`WatchViewModel.kt:495`, `:534-542`).
  - The detail refresh re-locks a lapsed membership and stops the player (`:616-629`).
  - Dubs never play on a locked video (`:444`).
  - Unlock re-reads from the server, never from the list row (`:476-479`, apart from the race in L2).
- **Age restriction.** Enforced server-side: the detail is refused and lists drop the row (`age_gate.go`). The client has no path that bypasses it.
- **HLS playlists are never cached.** Only segments are cached (`PlaylistAwareDataSourceFactory.kt`). Hidden-chat media is excluded from the cache, and the bearer is scoped to the API origin.
- **Resume.** It never waits before the first frame, ignores late answers for a different id (`_currentId` check), does not jump once more than 5 s has played, and starts from the top at 95% or more. Progress goes out every 10 s, on pause, on end, on background and on clear, and is skipped while History is paused.
- **Countdown.** It is cancelled on any transport action, on background and on `open()`. An autoplayed next episode goes through the full load and lock path.
- **Crash safety:**
  - `FeedPagingSource` de-duplicates ids per paging generation, which covers all `feedPager` lists: the channel's videos, Shorts, Live, Members and Appears-in tabs, Saved, Subscriptions and You.
  - `DiscoveryPager`, History pages, Up next, series episodes and chapters (`normalizeChapters`) are all de-duplicated before keyed lists.
  - The end-screen and card parsers drop unknown kinds and malformed rows.
  - JSON is set to `ignoreUnknownKeys`, `coerceInputValues` and `explicitNulls = false`.
- **Upload:**
  - Parts are streamed through 64 KiB okio segments, and a `FileChannel` seek is used for resume (`ResumableUploader.kt:417-449`, `ReelPublishStore.kt:359-367`), so nothing big is held in memory.
  - The session is persisted before the first part; dead or expired sessions are replaced; the server's part plan is validated.
  - The stash copy goes to `noBackupFilesDir` after a free-space check.
  - Uploads of 100 MB or more are promoted to a foreground `dataSync` worker; process death resumes from the persisted session.
  - Only the cover is read into memory.
- **Offline copies** are scoped to the signed-in viewer, held or deleted on sign-out by a teardown task, and played from the offline cache only.
- **No main-thread I/O on the watch path.** The offline index is read on the IO dispatcher; `thumb()` and `playable()` are in-memory lookups; BlurHash is a 32x18 decode inside `remember`; frame extraction runs on IO or Default.
- **The History page, its ViewModel and the other Tube ViewModels** are cleared on sign-out, because the back stack is popped to Login (`UsNavHost.kt:1547-1551`). The cross-account leak goes only through the `TubeQueue` singleton (H2).

Not run: `scripts/verify/*` (read-only audit; no code changed).
