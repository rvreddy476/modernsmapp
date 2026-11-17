# Long video: extra scope (settings, upload settings, realtime money, new features)

- **Date:** 2026-11-17. Read-only. Same trees as `00-map.md` (backend and Android `C:\workspace\modernsmapp` on `codex/module-01-02-launch-safety`; web `C:\workspace\wt\pb-echo` on `feat/echo-web`).
- **This is not part of the launch verdict.** It is a gap list and a list of ideas. Nothing here is rated P0 or P1. The words used are **present**, **partial** and **gap**.
- **What the founder asked:** "while checking the Reels and LongVideo, also check did we miss anything in functional interms Settings, Upload Settings, and make more realtime with monetization and can we add even more creative features to attact Users?"
- **How it was built:** three read-only passes (Android, web, server), each citing file and line, plus direct reads of `monetization-service/internal/service/creator_fund.go`, `analytics-service/internal/aggregation/{finalizer,hourly}.go`, `core/media/.../di/MediaModule.kt`, the web `studioApi.ts` and `useUploadStudio.ts`, and a grep of every `CommentModeration` reader. Claims about YouTube and RUTUBE come from product knowledge and the earlier walks (memory notes "MTube YouTube reference walkthrough", "MTube layout references"); they were **not walked again** for this section.
- **Companion:** `docs/production-readiness/reels/06-extra-scope.md`. Where a fact is the same, this file says **"same as reels"** and does not repeat it.
- **Path short forms:**
  - **A:** `mobile/android/`
  - **T:** `A feature/tube/src/main/kotlin/com/us/android/feature/tube/`
  - **W:** `C:\workspace\wt\pb-echo\src\`
  - **S:** `Architecture/services/`
  - **DS:** `A core/datastore/.../SettingsDataStore.kt`
  - **WVM:** `T ui/watch/WatchViewModel.kt`
  - **WSS:** `T ui/watch/WatchSettingsSheet.kt`
  - **TP:** `W features/posttube/components/TubePlayer.tsx`
  - **WP:** `W features/posttube/watch/WatchPage.tsx` (path as cited by the web pass)
  - **ES:** `W features/posttube/hub/components/EditSheet.tsx`
  - **PES:** `A core/feed/.../ui/edit/PostEditSheet.kt`
  - **PIPE:** `A feature/post/.../createhub/ReelPublishPipeline.kt`
  - **RS:** `A feature/post/.../createhub/ReelSurface.kt`

---

## Summary for the founder

- **Settings: the web player remembers your choices; the Android player forgets them.**
  - On the web, quality, speed, captions on/off and autoplay-next are saved (in the browser only).
  - On Android, quality, speed and captions reset on every video.
  - Android shows **no captions at all** on streamed long videos.
  - **Data saver does nothing on the Android Tube player.** The web only turns off autoplay; it does not lower quality.
  - There is no background play on either client. On the web the mini-player was hidden on 19 Oct.
  - Nothing is saved on the server, so no setting follows the viewer to another device.
  - Missing on every layer: private subscriptions, restricted mode and auto-delete of history.
  - **Creators' comment moderation does nothing.** "Hold all for review" is saved and never enforced. The web has no queue of held comments. Creators have no blocked-words list.
- **Upload settings: the web studio is complete; Android is the gap.**
  - Missing on Android: captions, chapters, series, end screens, drafts, Unlisted at upload, the AI label (both at upload and in edit), and a "related video" link.
  - Android picks the video's language from the phone's locale without asking.
  - The web sets 18+ in a second call after publishing. If that call fails, the video stays public without the age gate (it shows a warning only).
  - The web hub shows followers-only and friends-only videos as "Public".
  - Premiere exists only as two unused database columns.
- **Monetization is not realtime (same as reels), with one long-video twist.**
  - Earnings are off in prod (`MONETIZATION_WRITES_ENABLED=false`).
  - With earnings on:
    - The estimate takes **51 to 75 hours**.
    - A payable balance arrives monthly.
  - **A long-video view and its watch time are counted only when the viewer stops watching.** For a 90-minute film watched to the end, the creator sees nothing for 90 minutes, and then sees it 1 to 7 minutes later.
  - There is no "watching now" counter, and no earnings screen on Android.
  - There are no ads. Long video earns ₹50 per 1,000 views, against ₹3 for shorts.
- **Ideas, top five:**
  1. Premieres (the columns exist)
  2. Auto captions and auto chapters at upload
  3. "Watch in your language" (dubbed audio tracks)
  4. Audio-only and background play, with smart offline copies, for data-light India
  5. A "Watching now" and creator "today" panel

---

## Part 1. Settings that affect long video

### 1.1 Viewer playback, per client and on the server

Legend: **S** = a server field and route exist and the server enforces the setting. **S-stored** = the server stores it and nothing reads it. **local** = kept on the device or in the browser only. **—** = not found.

No playback preference is stored on the server. The server pass searched `playback_quality|autoplay_enabled|default_quality|caption_language|restricted_mode` and found none.

There is **no Tube settings page** on either client:
- **Android** spreads the settings over:
  - the watch gear sheet (WSS:91-136)
  - History's "Pause recording"
  - the Offline page's Wi-Fi switch
  - three Settings-hub rows (`A feature/profile/.../SettingsHubScreen.kt:130,135,139`)
- **Web:** PostTube keeps its own prefs in `posttube_player_prefs_v1` (`W features/posttube/model.ts:220-245`; hook `W features/posttube/hooks/useTubePrefs.ts:15-41`). No page under `W app/settings` reads or writes any `posttube_*` key.

| Setting | Android | Web | Server | Notes |
|---|---|---|---|---|
| Default quality | **partial.** Gear sheet offers Auto + HLS ladder (WSS:91-99). **Not saved:** in-memory `MutableStateFlow(Auto)` (WVM:355-358). Forced to Auto while a dub plays (WVM:508) | **present, local.** Saved and applied through hls `nextLevel` (TP:370,404-405). Player menu only (TP:1133,1201-1206) | — | Android gap. YouTube saves a default on mobile and Wi-Fi separately |
| Data saver | **does nothing for Tube.** The switch exists (`SettingsHubScreen.kt:135`, DS:57-63) and works in `PlayerPool` (`A core/media/.../PlayerPool.kt:37,112`). Tube builds its own player with `createLongForm()` (WVM:334), and `createLongForm` never applies it (`A core/media/.../di/MediaModule.kt:139-151`). There is no `DataSaver` reference in `:feature:tube` | **partial.** The page promises "Videos won't autoplay" (`W app/settings/data-saver/page.tsx:233`). WP reads it (WP:159) and passes `autoPlay={!dataSaver}`, `deferLoad` (WP:825-826). **No quality cap**, and autoplay-next ignores it (WP:388) | — | The Android gap matters most: long video uses the most data |
| Autoplay next | **partial.** "Autoplay next episode", local, default on (WSS:128-136; DS:183-189). Applies to **series only**: with no series the end screen shows (`T ui/watch/SeriesAdvance.kt:50-54`). Up next never plays on its own (`T ui/watch/UpNext.kt:10-11`) | **present, local.** `posttube_autoplay_next_v1`, default on (`model.ts:152,201-212`). The countdown is at WP:388,402-409 | — | The clients behave differently. Decide one rule (F-1.3) |
| Captions on/off | **gap for streamed video.** Tracks only for offline copies (WVM:909-913,926-930). Off by default, not saved (WVM:915-918) | **present, local** (TP:422-424; toggles TP:660,956) | Tracks `GET /v1/subtitles/:mediaId` (S `media-service/internal/http/clips_handler.go:63`) | Same gap as reels on Android, but worse here: long videos are where captions get uploaded (the web studio does it) |
| Caption language | — | **not saved** (`useState(null)`, WP:465) | any BCP-47 tag (S `media-service/internal/service/voice.go:184-187`) | Gap on both clients |
| Caption style / size | — (searched `CaptionStyleCompat`, `captioningManager`) | — (searched `::cue`, `captionSize`) | — | Gap. YouTube has it; Android could follow the system caption style for free |
| Default speed | not saved (WVM:360-361,900-903). 0.5x–2x | **saved, local** (TP:410-414). 0.25, 1, 1.25, 1.5, 2 | — | Android gap. The two speed lists differ |
| Background play | **gap.** Going to the background pauses and releases the player (WVM:937-949). No `MediaSessionService` | — (no `mediaSession` in `features/posttube`) | — | Gap. Lectures, podcasts and music videos need it (Part 4, idea 4) |
| Picture-in-picture | **present, no setting.** Auto-enters on Android 12+ (`T ui/watch/WatchPictureInPicture.kt:47-53,110-115`) | **hidden.** Mini-player off: "Miniplayer hidden for now (founder, 2026-10-19)" (WP:863-865). The `I` key does nothing (TP:738-739). No native PiP | — | Android needs an off switch (YouTube has one). The web state is a decision you took |
| Audio-track (dub) language | present, local, shared with reels (DS:231-239; WVM:442-443,498-509) | present, local `posttube_watch_prefs_v1.audioLanguage` (`W features/posttube/watch/watchPrefs.ts:9-23`). A comment there says ambient mode is set "in Settings"; no Settings page has it (`watchPrefs.ts:19`) | — | Same as reels: not synced |
| Muted words | **not applied in Tube** (only `A feature/feed/.../FeedViewModel.kt:242-243`) | a feed filter (`W app/settings/keyword-filters`) | **S** in feed hydration (same as reels) | Whether `/v1/feed/videos` hydration applies it was not traced. Confirm on a device |

### 1.2 History, notifications, privacy, restricted mode

| Setting | Android | Web | Server | Notes |
|---|---|---|---|---|
| Pause history | **local** (DS:290-299; enforced `T data/WatchProgressRepository.kt:43`) | **local** `posttube_history_paused_v1` (`W features/posttube/library/historyPause.ts:13`), read only in WP:289. A second writer `useSaveWatchProgress` (`W hooks/usePosttubeExtras.ts:185-198`) does not check it. No caller was found today | — (searched `history_paused`) | `00-map.md` question 8. Pausing on one device does not pause the others |
| Remove one / clear all | present (`A core/feed/.../WatchHistoryApi.kt:33-38`) | present (`W features/posttube/data/posttubeApi.ts:395-396`; button `RecentScreen.tsx:120`) | **S** (`S post-service/internal/http/handler.go:430-434`) | Done |
| Auto-delete history (3 / 18 / 36 months) | — | — | — . No retention sweep; rows stay until deleted (`S post-service/internal/store/postgres/watch_progress.go:91,230`) | Gap on every layer. It is also a data-minimisation point under India's DPDP Act |
| Per-channel bell | present, after subscribing; All / None only (`T ui/channel/SubscribeControl.kt:55-74`; `A core/feed/.../ChannelApi.kt:88`) | present on the channel page (`W .../channel/components/ChannelMasthead.tsx:110`; `SubscribeButton.tsx:42,81-85`). **None on the subscriptions page** | **S.** `notify_on` `all`/`none`; fan-out reads `all` only (S `post-service/internal/store/postgres/channel_subscriptions.go:25-29,184-194`). The legacy user-service copy still uses `all/highlights/none` and filters `IN ('all','uploads')` (S `user-service/internal/store/subscriber_fanout.go:63`) | YouTube has All / Personalised / None. "Personalised" was folded into None. The user-service mismatch matters only if that path is still live (`00-map.md` question 9) |
| New-upload notifications | present: "New videos", "Live", "Video credits", "Memberships" (`A core/profile/.../SettingsModels.kt:75,77,92,98`) | present (`W app/settings/_lib/notificationPreferences.ts:34-50`) | **S.** `push_new_videos`, `inapp_new_videos` default on (S `notification-service/database/migrations/006_new_videos_preference.sql:8-9`). The fan-out respects creator, subscriber and viewer choices (`internal/service/subscriber_fanout.go:15-33`) | Done |
| Upload digest ("3 new videos from your subscriptions") | — | — | **gap.** The digest worker summarises unread notifications only, and its email send is a TODO (S `notification-service/internal/workers/digest.go:15-16,107-110,192`) | Useful for heavy subscribers; it cuts push noise |
| Keep my subscriptions private | — | — | — (searched `subscriptions_private`) | Gap. YouTube offers it. The subscriptions list is not public today, so this matters only if a public list is ever added |
| Liked / Watch later private | private by construction | same | the system playlists `liked` and `watch_later` are always private; visibility changes are refused (S `post-service/internal/store/postgres/system_playlists.go:19-25,60-61`) | Done |
| New playlist default visibility | create sheet: a "Private" switch, on by default, so private or public (`T ui/collections/CollectionPickerSheet.kt:152-185`). Edit offers public / unlisted / private (`CollectionEditing.kt:181-260`) | save dialog private / public, default private (`W .../SaveToPlaylistDialog.tsx:30,133-134`); the collections index offers all three (`CollectionsIndex.tsx:71,148-151`) | column default `public` (S `post-service/database/migrations/012_posttube_features.sql:39`) | Both clients default to private, which is safe. Unlisted is missing from both create dialogs |
| Restricted mode / sensitive content | — . `under_18_mode` is read-only and nothing in Tube reads it (`A core/profile/.../dto/SettingsDtos.kt:23`) | — | partial: the per-post `age_restricted` gate (same as reels) | Gap on every layer. RUTUBE and YouTube both have it. It also matters for shared family phones in India |
| Kids profile / supervised | — | — | `is_made_for_kids` is stored per post; nothing filters on it | Gap. It needs a policy decision before code (F-1.4) |

### 1.3 Creator and channel settings

| Setting | Android | Web | Server | Notes |
|---|---|---|---|---|
| Channel profile (name, handle, about) | present (`T ui/channel/EditChannelSheet.kt:182,191,201`) | present | **S** (S `post-service/database/migrations/041_channels.sql:23-43`; links, contact and featured in `051_mtube_contracts.sql:130-140`) | Android lacks banner, links, contact email and featured video, which the server has. That is an Android parity gap |
| Upload defaults (visibility, category, language, comments, licence) | — | **partial, local.** `posttube_publish_defaults_v1` (`W features/posttube/hub/publishDefaults.ts:22-44`), applied only to fields the creator has not touched (`W features/upload/studioDefaults.ts:18-38`). **Licence is saved and never read** (`studioDefaults.ts:12-14`) while the page still shows it (`PreferencesPage.tsx:93-103`) | — . The channels table has no default columns | Same as reels: belongs on the server so Android gets it. The licence row does nothing |
| Comment moderation level (none / basic / strict / hold_all) | per video in the edit sheet (PES:234-271) | per video in the edit sheet (ES:628) | **S-stored, never enforced.** Validated (S `post-service/internal/service/hub_settings.go:37`). No comment path reads `CommentModeration`: every reader is in create, edit or draft code (grep across `post-service/internal`). Create defaults to `none` (`service/post_create.go:234-237`), drafts to `basic` (`service/drafts.go:332`) | **A switch that does nothing**, and the worst kind: a creator who picks "hold all" believes comments wait for approval |
| Held-for-review queue | — | — (Conversations has unanswered / all filters only, `ConversationsPage.tsx:77-98`) | — | Gap. It is needed before "hold_all" can mean anything |
| Blocked words on my channel | — | — | partial: `trust.keyword_filters` has a `channel` scope and `hide/flag/block` actions (S `trust-safety-service/database/migrations/004_trust_extras.sql:20-29`), not wired to comments | Gap. YouTube's most-used creator setting |
| Hide user from my channel | — (Hub Conversations: heart and pin only, `T ui/hub/CreatorHubViewModel.kt:190-210`) | present: hide (mute) and block per commenter (`ConversationsPage.tsx:178,186`) | block exists (graph-service) | Android parity gap |
| Who can comment | Everyone / Subscribers only / Nobody in edit (PES:147-209) | present | **S** (`comment_access`, S `post-service/internal/service/post_screen_fields.go:189`) | Done (edit only on Android) |

### 1.4 Offline copies

| Setting | Android | Web | Server | Notes |
|---|---|---|---|---|
| Wi-Fi only | present, local, default on (DS:259-265; `T ui/offline/OfflineScreen.kt:236-240`; `OfflineRules.kt:39-41`) | — | — | Web has no Wi-Fi notion; fine |
| Quality of the copy | — (the server grant decides, `A core/feed/.../offline/OfflineApi.kt:28-39`) | — (fixed `"720p"`, `W features/offline/wire.ts:157`) | grant | Gap: YouTube offers low, medium and high. For India this is the main data and storage lever |
| Storage used, remove all, expiry | present (OfflineScreen.kt:224-228,246; renew every 24 h, `OfflineRules.kt:101-127`; 200 MB reserve, :56) | remove all only (`W features/offline/components/OfflinePage.tsx:296`) | **S** (S `post-service/internal/service/offline_copies.go:20-40`) | Done on Android |
| Creator: allow offline copies | upload and edit (RS:1000-1022; PES) | edit only (ES:444) | **S**, default on | Web gap at upload |

### 1.5 Compared with YouTube and RUTUBE

| Setting | Here: Android / web / server | YouTube / RUTUBE | Recommendation |
|---|---|---|---|
| Captions on streamed long video | gap / present / S | both | **Add for launch** (Android). Web creators already upload captions that Android viewers never see |
| Data saver on the Tube player | does nothing / partial / — | both | **Add for launch** (Android): one call in `createLongForm`, or hide the claim |
| Comment moderation that works ("hold all") | does nothing / does nothing / stored | YouTube | **Add for launch:** enforce it or remove the choice. A held-review queue comes with it |
| Remember quality, speed, captions | gap / present / — | both | **Add for launch** (Android, local is enough) |
| Background play / audio-only | gap / gap / — | YouTube (Premium), RUTUBE (app) | **Add later**, but it is a strong idea (Part 4, idea 4) |
| PiP off switch | always on / hidden / — | YouTube | Add later (Android) |
| Restricted mode | — / — / partial | both | **Add later:** needs a sensitive-content label first (same as reels) |
| Auto-delete history | — / — / — | YouTube | Add later. Pair it with a server-side pause (map question 8) |
| Blocked words, hidden users on Android | — / partial / partial | YouTube | Add later |
| Upload digest | — / — / — | YouTube | Add later |
| Settings synced across devices | — / — / — | both | **Add later:** store playback and history prefs on the server once |
| Caption style | — / — / — | YouTube | Skip for now. Android can follow the system caption style for free |

**Founder decisions, Part 1**

- **F-1.1:** For launch, should Android get network captions and remembered quality, speed and captions? (Recommended: yes. It is Android parity with the web.)
- **F-1.2:** Comment moderation "hold all" does nothing today. Build the held queue before launch, or remove the choice until then?
- **F-1.3:** Autoplay next: series only (Android today) or any Up next video (web today)?
- **F-1.4:** Do you want a restricted mode or a kids profile at all for launch? It needs a content-labelling policy first.
- **F-1.5:** Should the web mini-player stay hidden (your 19 Oct decision) now that Android has PiP?

---

## Part 2. Upload settings for long video

**Where things are:**
- **Android:** the Video tile opens `ReelSurface` as a long video (`A feature/post/.../ReelPublishViewModel.kt:751-752`). The body is built at PIPE:439-494 (DTO `A feature/post/.../data/dto/CreatePostDtos.kt`). Edit is PES, PATCH `PostEditApi.kt:108-159` (changed fields only).
- **Web:** steps `video, details, audience, engage, enrich, publish` (+ `credits`) (`W features/upload/tokens.ts:23,33-37`). The publish order is in `W features/upload/useUploadStudio.ts:551-717`. Edit is ES, PATCH `W features/posttube/hub/hubApi.ts:575-634`.
- **Server:** create body at S `post-service/internal/http/create_post_handler.go:31-166`; edit allowlist at S `post-service/internal/service/post_edit.go:83-135`.

### 2.1 Field by field

| Field | Android upload → sent | Web upload → sent | Edit (Android / web) | Server |
|---|---|---|---|---|
| Title | required, ≤100 (RS:403-406) → `title` (PIPE:461) | required, ≤100 (`validation.ts:76-79`) | both | **required for long video** (S `post-service/internal/service/create_guards.go:68-69`) |
| Description | → `text` (PIPE:454) | ≤2200 → `text` | both | stored. **Timestamps in the description become chapters** (S `post-service/internal/service/chapters.go:44-50,123`) |
| Category | **required**, "None" hidden (VM:263-267; RS:633,919) | **required for long** (`validation.ts:9-10,95-96`) | both, with "None" on Android | **optional** (S `post-service/internal/service/categories.go:156-160,201-207`). Your rule is met on both clients' upload; the server, and an Android edit, accept none |
| Tags / hashtags | hashtag chips → `hashtags` (PIPE:474). Tags only in edit | both (`DetailsStep.tsx:187-220`) | both | tags ≤20, hashtags |
| Language | **not asked; device locale sent**, else "en" (PIPE:443,504-510) | asked, but pre-filled "en" (`types.ts:174`; `EnrichStep.tsx:52-53`) | both | stored |
| Visibility | Public / Followers / Private; **no Unlisted** (RS:1426-1428) | public / followers / friends / unlisted / private (`StudioSettings.tsx:30-36`) | Android public / unlisted / private (`PostEditForm.kt:29-31`); web public / unlisted / private / scheduled (`hubApi.ts:114`). **The web hub shows followers and friends as "public"** (`normalizeVisibility`, `hubApi.ts:157-162`) | 5 values at create and edit (`post_edit.go:48-50`) |
| Schedule | → `publish_at` | → `publish_at` | both | 5 min to 30 days |
| Thumbnail | filmstrip, exact frame or gallery (`CoverPicker.kt:64-67,133,269-294`) → `cover_media_id` | frame slider + own image; **does not use the server's suggested frames** (`DetailsStep.tsx:291-313`) | **Android: cannot change the cover after publish.** Web: frame + upload (ES:347-366) | `cover_media_id`, `cover_index`, `/v1/media/:id/suggested-frames` |
| Captions | **gap** | one SRT/VTT file in Extras. A failure is ignored (`useUploadStudio.ts:590`) | Android gap; web Captions tab (ES:883-905) | manual + `/auto` (S `media-service/internal/http/clips_handler.go:69`). **Auto captions never start on their own for videos** (only voice assets, S `media-service/internal/service/upload.go:359`) |
| Chapters | **gap** (read-only, `WatchChapters.kt`). A creator can only type timestamps into the description | Extras step | Android gap; web Elements tab | `POST /v1/posts/:id/chapters`. `source=ai_generated` exists, but **no generator does**. The draft flag `auto_chapters` (default true) has no effect (S `post-service/internal/service/drafts.go:324`) |
| Credits, genres, year, production house | when the category asks (RS:478-490; PIPE:483-486) | Credits step | both | long video only (`create_post_handler.go:139-149`) |
| Series | **gap** (no series in `createhub/`) | SeriesPicker (`PublishStep.tsx:120`) | Android gap; web `SeriesSection` (ES:776) | `/v1/video-series/*` |
| Playlist at upload | gap | gap | — | playlists API | YouTube offers it at upload. Small |
| Related video ("watch part 2") | gap | gap at upload | **Android gap**; web edit (ES:547-669), shown on the watch page (`watchApi.ts:194`) | `related_post_id`, edit only (`post_edit.go:121`) |
| End screens / cards | gap (the hub says to use the web, `CreatorHubScreen.kt:102`) | not at upload | Android gap; web Elements tab (ES:691-693) | routes, long video only |
| Comments on/off, who can comment | on/off → `no_comments`. `comment_access` is never sent at upload | both | both | `comment_access` enforced; `comment_moderation` not enforced (Part 1.3) |
| Age restriction (18+) | gap at upload; edit (PES:217-233) | Audience step, **sent as a separate PATCH after publish** (`studioApi.ts:158-168`). If it fails: the warning "could not be saved. Turn it on in Creator Hub" and the video stays without the gate (`useUploadStudio.ts:667-674`) | both | **not accepted at create**, only at edit (`post_edit.go:118-121`) |
| Made for kids | gap at upload; edit | radio, pre-set "No" → `is_made_for_kids` | both | stored; nothing filters on it |
| AI-generated label | **gap at upload and edit.** Edit has only "Altered or AI content" → `altered_content` | toggle → `ai_generated` (sent only when true) (`StudioSettings.tsx:271`). **Not editable after publish**; the edit "Altered or AI" maps to `altered_content` (ES:559) | see left | both fields stored | Same pattern as reels, worse on Android |
| Paid promotion / branded | `paid_promotion` in edit only; `is_branded` never | `paid_promotion` upload + edit; `is_branded` never | — | both stored | ASCI disclosure; same as reels |
| Members only | when levels exist (RS:493-505) → `tier_required_id` | when levels exist (`PublishStep.tsx:92`) | both | enforced; payments off |
| Monetization on/off per video | — | — | — | — . The fund is per creator, not per video | Fine for launch. A per-video switch matters only when ads exist |
| Allow offline copy | upload + edit | edit only | both | default true |
| Remix | not sent for long video, but **offered in Android edit** | upload + edit | both | stored | Remix of a long video means little today. Hide it for long video |
| Location | typed name → `location_name`, no place id | place search → both fields | neither (only `recording_location`) | stored |
| Premiere | — | — | — | **schema only:** `posts.premiere_at`, `premiere_reminder_count` (S `post-service/database/migrations/012_posttube_features.sql:115-117`); no Go code reads them | Part 4, idea 1 |
| Action button | present → `action` | present | both | validated |
| Notify subscribers | **hard-coded true** (PIPE:460) | not asked; the server default is true | before publish only (ES:216,561) | ignored after publish (`post_edit.go:105-107`) |
| Drafts | **gap** (`ReelPublishStore` is an upload queue, not a draft) | present (`useUploadStudio.ts:525-547`) | — | `/v1/reels/drafts` |
| Copyright check before publish | none; read back after | none (`copyrightCheck` never set, `types.ts:121,195`); read back after (`studioApi.ts:101-111`) | — | shadow fingerprinting | Same as reels |

### 2.2 Fields sent without asking, and fields no client offers

**Sent by a client without the creator choosing**
- Android: `language` taken from the device locale; `notify_subscribers=true`; `main_feed=true`; `app_origin="postbook"`; `post_type="video"` (PIPE:443,457,460; `CreatePostDtos.kt:77,304,315`).
- Web: `publish_to_feed:true` with no control in the UI (`useUploadStudio.ts:502,619`; `W features/reels/data/reelsApi.ts:342`).
- Web: `original_audio_volume` / `overlay_audio_volume` on long videos, which have no sound step (`studioApi.ts:48-49`).
- Web: `language:"en"` unless the creator changes it.

**Accepted by the server, offered by no long-video client at upload:**
- `is_branded`
- `link_url`
- `seo_title`
- `comment_moderation`
- `cover_index`
- `license` and `allow_embedding` (edit only)
- `age_restricted` (edit only on the server)
- `related_post_id` (edit, web only)
- `hide_like_count`, `default_comment_sort` (edit only)
- on Android also: `comment_access`, `paid_promotion`, `ai_generated` and `location_place_id` (`CreatePostDtos.kt:211-226` against PIPE:453-493)

**Founder's rules against the code**
- **Category required (web):** met on both clients; the server does not enforce it.
- **Visibility and category on the web:** met.
- **AI label shown to viewers:** web only, as for reels. The Android upload has no way to set it.
- **Android has everything the web has:** not met for long video. Captions, chapters, series, end screens, related video, drafts, Unlisted at upload, AI label, channel banner and links, and hide-commenter are web-only.

### 2.3 Compared with YouTube and RUTUBE

| Field | Here (A / W / S) | YouTube / RUTUBE | Recommendation |
|---|---|---|---|
| 18+ set at create, not after | edit only / PATCH after publish / edit only | both set it before publish | **Add for launch:** accept `age_restricted` on create (server) and send it in the body. Today a failed PATCH leaves an 18+ video ungated |
| Hub shows the true visibility | — / followers and friends shown as public / — | — | **Add for launch** (web): a creator could "fix" a followers video to public by mistake |
| AI label on Android | gap / partial / S | YouTube requires disclosure | **Add for launch** (same as reels F-2.1) |
| Unlisted at upload on Android | gap / present / S | both | **Add for launch:** one list item (`00-map.md` question 13) |
| Captions and chapters on Android | gap / present / S | both | Add later, early. An "Auto captions" button is the cheap version (Part 4, idea 2) |
| Series, end screens, related video on Android | gap / present / S | YouTube | Add later (Android parity) |
| Drafts on Android | gap / present / S | both | Add later. Long uploads are where drafts matter most |
| Change the thumbnail after publish on Android | gap / present / S | both | Add later |
| Suggested frames | not used / not used / route exists | YouTube offers 3 auto thumbnails | Add later. Small |
| Playlist at upload | gap / gap / S | YouTube | Add later. Small |
| Premiere | — / — / columns only | both | Add later (Part 4, idea 1) |
| Stop hidden values (locale, feed, audio volumes) | sends / sends / — | — | **Add for launch:** ask for the language on Android, as the web does |
| Per-video monetization switch | — / — / — | YouTube (ads) | Skip until there are ads |

**Founder decisions, Part 2**

- **F-2.1:** Should the server accept 18+ at create so the web stops patching it afterwards? (Recommended: yes.)
- **F-2.2:** Which Android long-video upload gaps must close before launch: Unlisted, AI label, captions, chapters, series, drafts? (Suggested minimum: Unlisted and AI label.)
- **F-2.3:** Should the Android upload ask for the video's language (needed for captions and dubbing), instead of using the phone's locale?
- **F-2.4:** Should "Remix" be hidden for long videos on Android edit?

---

## Part 3. How "realtime" monetization is for long video

### 3.1 What is the same as reels, and what differs

**Same as reels** (see the reels file 3.1–3.2):
- the rollup timings (hourly rebuild every 5 min, daily re-roll every 15 min, a day freezes 48 h after it ends)
- daily accrual at 03:00 UTC for final days only, so **51 to 75 hours** to an estimate when writes are on
- monthly settlement
- payouts closed
- `MONETIZATION_WRITES_ENABLED=false` in prod, so no worker starts
- the uncapped "today" count that can drop at midnight
- the web `/monetization` chart that is hard-coded bars (`W components/monetization/EarningsDashboard.tsx:73-79`)
- no Android earnings screen
- unused web creator-fund hooks (`W hooks/useMonetization.ts:253-669`)

**Different for long video:**

| Point | Fact | Evidence |
|---|---|---|
| Rate | **₹50 per 1,000 views** (flick ₹3). Same 0.85x–1.25x quality band and 70/30 split | S `monetization-service/database/migrations/010_creator_fund.sql:47-62`; `014_creator_fund_quality_multiplier.sql:48-67` |
| What is a view | at least 30 s watched, or at least 50% for a video under 60 s | S `analytics-service/internal/model/view_rules.go:68-72,95-104` |
| **When a view is counted** | Views, watch time and unique viewers come only from **finalised** playback sessions ("An open session is not a view yet"). A session is finalised by `play_end`, or 10 min after its last heartbeat (checked every 60 s). It is then booked to the hour it **started** | S `analytics-service/internal/aggregation/hourly.go:445-451,496-511`; `finalizer.go:13-17` |
| Effect | A viewer watching a 90-minute film adds **nothing** to views or watch time until they stop. The count then appears within 1 to 7 minutes (or about 17 minutes if the app was killed). For shorts the session is seconds long, so the effect is invisible there | as above |
| Eligibility | **3 earning videos as a floor, and then 1,000 view-score or 10 hours of watch time**, over 90 days. Watch hours are not weighted by content type. The code comment says "any single one" is enough; the code says otherwise | S `monetization-service/internal/service/creator_fund.go:26-32,137-145`; `store/postgres/creator_fund.go:475-486` |
| Watch-time progress | Because watch time lands only at session end, a creator watching the "10 hours" bar sees it jump after each viewer leaves, not smoothly | as above |
| Ads | **None.** RPM on views is the only model (`017_periodic_settlement.sql:67` calls it "ad-style views revenue") | server pass (searched `ads`, `ad_revenue`, `cpm`) |
| Memberships | Android has the full membership screens (`A core/memberships/.../MembershipsApi.kt:25-102`; four screens in `MembershipsNavigation.kt:45-52`) and locks the player (WVM:557,593-600). The web has a Join button on the lock card only (`WatchDetails.tsx:271`). `MEMBERSHIPS_ENABLED=false` everywhere (`00-map.md` 7) | as cited |
| Tips (Thanks) | The web watch page shows Thanks when the server says `tips_enabled` and the viewer is not the owner (`W features/posttube/watch/thanks.ts:65-70`; WP:252-254,900). Android has none. With writes off, money routes outside the beta rules answer 503 `MONETIZATION_NOT_LAUNCHED` (S `monetization-service/internal/http/handler.go:304-384`) | as cited |
| Creator screens | Android Creator Hub: views, watch time, unique viewers and views by day, loaded on open and on period change. **No polling and no pull-to-refresh**, no earnings (`T ui/hub/CreatorHubViewModel.kt:135-154`; `CreatorHubModels.kt:43-81`). Web hub insights: staleTime 60 s; per video 5 min; **no `refetchInterval` on any analytics, earnings or membership query** (`W features/posttube/hub/useHub.ts:625-634`) | as cited |
| Live push | No SSE or websocket for creator stats. The ws-gateway `post:<id>` rooms carry comment and count frames only (`chat-service/services/ws-gateway/internal/http/postrooms.go:17-59`). No "watching now" counter for VOD (searched `concurrent_viewers`, `watching_now`) | server pass |

### 3.2 The real delay today (long video)

| From a view to... | Today in prod | If `MONETIZATION_WRITES_ENABLED` were on |
|---|---|---|
| (a) the view count the creator sees | **Length of the watch + 1 to 7 minutes**, because the session must end first. **About 17 minutes** after the app is killed. Client batching adds an amount nobody measured (web flush every 10 s or 50 events, `W hooks/useVideoTracker.ts:15-22`) | same |
| (b) watch time | same as (a) | same |
| (c) the estimated earnings figure | **Never** (no accrual, no screen) | **51 to 75 hours** (same as reels) |
| (d) eligibility progress | `GET /creator-fund/status` reflects it after the first daily rollup covering that day: **0 to 24 hours + up to 15 minutes**. No screen on either client | same |
| (e) a payable balance | **Never** | monthly, around the 3rd of the next month; not withdrawable |

### 3.3 What "more realtime" could mean

**My reading of your request:** creators should see their views, earnings and eligibility progress update live, while the money itself stays careful. That matches the reels reading (reels F-3.1).

**Other readings worth checking:**
1. **Viewers see live counts** on the watch page (views and likes ticking up).
2. **"Watching now"** for the creator: how many people are on a video right now. This is the long-video version of a live count.
3. **Instant money:** earnings withdrawable the same day. Not advised; same risks as reels R5 and R6.
4. **Live-stream money:** money during PostTube Live. Your 2 Oct decision rules out gifts and coins.
5. **Milestone pushes** ("your video crossed 1,000 views", "you are eligible").

| # | Option | What exists to build on | What must be built | Risk | Size |
|---|---|---|---|---|---|
| L1 | **Count watch time while the session is open** (partial watch time every 5 min, not only at the end) | Heartbeats every 5 s already advance the session row (`finalizer.go:24-33`; Android `VideoWatchTracker.kt:562,572`) | Let the hourly rebuild add the open sessions' `watched_ms` into a separate "provisional" column, shown only on the creator screen | Low if it stays out of money. The daily rollup that prices views stays on finalised sessions | **S–M** |
| L2 | **"Watching now" per video and per channel** | Open `playback_sessions` rows with a heartbeat in the last minute | A read-only count route (owner only), polled every 30 s on the Hub. Push via the ws-gateway later | Low. It is a count, not money. It needs an index on (creator, last heartbeat) | **S** (poll) / M (push) |
| R1–R4 | Eligibility bar, live view counter, labelled estimate, "earning now" badge | **Same as reels** | Same. For long video, use ₹50 RPM and label the watch-time bar "updates when viewers finish" | Same as reels. The estimate is 17x bigger per view, so a wrong live number is more disputed | S–M |
| L3 | **Android earnings and eligibility screen** | Same routes as the web; open in beta | One screen in `:feature:tube` Creator Hub (`CreatorHubScreen.kt:102` says payouts stay on the web) | Low | **S–M** |
| L4 | **Milestone pushes** (1K views, 10 watch hours, eligible) | notification-service categories; `/creator-fund/status` | A daily or hourly job compares thresholds and sends once | Low | S |
| L5 | **Memberships made live** (new member shown at once, members-only premiere) | Android memberships module; tiers and subscribe routes | Payments via payments-service (memberships plan, 23 Oct). Blocked on `MEMBERSHIPS_ENABLED` | Medium (money in) | M |
| L6 | **Ads / pre-roll** | none | Everything: ad server, inventory, brand safety | High | L. Skip for launch |

**Recommendation:** L2 and L1 first: they are cheap and specific to long video, and they make the Hub feel alive without touching money. Then R1 and L3, so Android stops lagging the web. Then R3 with a range. Keep L6 out of scope.

**Founder decisions, Part 3**

- **F-3.1:** Which reading is yours? Creators see live progress (mine), viewers see live counts, "watching now", or faster money?
- **F-3.2:** May the creator Hub show provisional watch time (L1) that later settles to a final figure?
- **F-3.3:** Should Android get the creator earnings and eligibility screen before launch? (Same question as reels F-3.4.)
- **F-3.4:** The eligibility rule in code needs 3 videos AND (1,000 views or 10 hours), but the code comment says any one is enough. Which did you intend?

---

## Part 4. Feature ideas to attract and keep users

These ideas respect the decisions already taken:
- launch only Social, Shorts, Long video with monetization, and Commerce
- no gifts, coins or guests on live
- heart-only reactions on videos
- no Stories
- RUTUBE layouts and words, with YouTube used as a function checklist
- Android is the super app
- offline copies are never files

| # | Idea | What it is | What exists to build on | Size | Why it attracts people in India |
|---|---|---|---|---|---|
| 1 | **Premieres** | Schedule a video as a premiere: a watch page with a countdown, a "Remind me" button, and live chat while it plays for the first time, then a normal video | `posts.premiere_at`, `premiere_reminder_count` (S `post-service/database/migrations/012_posttube_features.sql:115-117`); scheduled publish (`post-service/internal/service/schedule.go:457`, publisher every 30 s); subscriber fan-out (S `notification-service/internal/service/subscriber_fanout.go:15-33`); live chat from PostTube Live | **M** | Creators (web series, film trailers, music labels) get an event and a launch spike. Viewers get a shared moment. Common for Indian film and music launches |
| 2 | **Auto captions and auto chapters at upload, on by default** | An "Auto captions" switch in both studios; chapters suggested from the transcript; creator edits both | `POST /v1/subtitles/:mediaId/auto` with Whisper (S `media-service/internal/http/clips_handler.go:69`; `internal/captions/whisper.go`); `media_chapters.source='ai_generated'` (`012_posttube_features.sql:62`); draft `auto_chapters` flag already true and unused (S `post-service/internal/service/drafts.go:324`); chapters from description timestamps (`service/chapters.go:44-50`) | **S–M** | Search and watch time on lectures, tutorials and podcasts. Captions let people in noisy or shared spaces watch without sound. Closes the Android captions gap at the same time |
| 3 | **"Watch in your language"** | A language chip on the watch page; creators add Hindi, Tamil, Telugu and other audio tracks (uploaded or auto-dubbed) and captions; viewers' choice is remembered | Audio tracks routes and `Generate` (S `media-service/internal/http/audio_tracks_handler.go:20-41`; `service/audio_tracks.go:517`); dub selection on both players (WVM:442-443; `watchPrefs.ts:9-23`); `MEDIA_DUBBING_BACKEND` (`media-service/internal/config/config.go:56-66`) | **M** (L with auto-dub) | The strongest India-specific idea: one creator reaches several language audiences. YouTube offers it to few creators. Same idea as reels idea 3, bigger payoff on long video |
| 4 | **Audio-only, background play and smart offline** | Keep listening with the screen off (Android media session); an "audio only" mode that skips video segments; "Keep the next episode" automatically saves the next episode of a followed series on Wi-Fi | Long-form player with focus handling (`A core/media/.../di/MediaModule.kt:139-151`); offline copies with Wi-Fi only and expiry (`T ui/offline/OfflineRules.kt:39-41,101-127`; S `post-service/internal/service/offline_copies.go:20-40`); series (`/v1/video-series`); HLS ladder from 360p | **M** | Data-light and battery-light. Fits commuters, lectures, devotional and podcast content. YouTube charges for background play; giving it free is a reason to switch |
| 5 | **Creator "today" panel with "Watching now"** | Live "watching now", provisional watch time, eligibility bar and a labelled estimate | Part 3 L1, L2, R1–R3; `/creator-fund/status`; the 48 h strip (S `analytics-service/internal/service/creator.go:141-244`) | **S–M** | The direct answer to your realtime request. Creators come back to the app to watch the number |
| 6 | **Shows: series as seasons, with a binge row** | Series pages with Season 1, Season 2, a "Continue the show" row on Home, and auto-advance on the web as on Android | `video_series` and episodes (`012_posttube_features.sql:5-68`); Android auto-advance (`T ui/watch/SeriesAdvance.kt:23,50-54`); continue-watching (`post-service/internal/http/handler.go:427`); the series-subscription work (memory note "Tube series & subscriptions state") | **M** | Web series and regional dramas are a large Indian genre. A binge row keeps viewers on long video, which pays creators ₹50 per 1,000 views |
| 7 | **Cast pages: "Appears in" as filmographies** | Tap an actor in the credits to see everything they appear in, with Follow, and "More with this cast" on the watch page | Credits with accept and decline, "Appears in" route (S `post-service/internal/http/credits.go:37-45`); genres, year, production house (`070_video_credits.sql:29-78`); search by genre and year (`search-service/.../handler_tube.go:41`) | **S–M** | Independent filmmakers, YouTube-style comedy troupes and regional actors get discovered; viewers follow people, not only channels. RUTUBE and YouTube have no such thing for small creators |
| 8 | **Shop the video** | Product tags with timestamps ("at 3:20, this kurta") and a product shelf under the video | Product-tag routes with impression and click, already valid for long video (S `post-service/internal/service/product_tags.go:50-87`; `http/handler.go:438-443`); chapters for the timestamp UI; Commerce catalogue | **M** | Joins Long video and Commerce, both launch apps. It gives creators income beyond the fund. Reviews, unboxing and cooking channels convert well |
| 9 | **Clips: the short that leads to the full video** | A "Full video" link on a short, and a "Clips from this video" row on the watch page | `related_post_id`, editable, shown on the web watch page (S `post-service/database/setup.sql:405-421`; `W features/posttube/watch/watchApi.ts:194`; ES:659) | **S** | Same as reels idea 8 seen from the long side. Moves short-video viewers into long video. Needs the Android edit and watch support |
| 10 | **Live replays with highlights** | Every PostTube Live becomes a replay with auto chapters and a "Highlights" short cut from it | Live VOD to an unlisted long video (S `post-service/internal/service/live_vod.go:20-39`); auto chapters (idea 2); trim (`/v1/videos/:id/trim`) | **M** | Creators who go live get two more pieces of content for free; viewers who missed it can catch up |
| 11 | **Members' early access** | Members watch 24 to 72 hours before public; members-only premieres | `tier_required_id` (`016_membership_gating.sql:14`); scheduled visibility change; Android memberships module | **S** once payments are on | Creator money beyond the fund. Blocked on `MEMBERSHIPS_ENABLED` and the memberships plan |
| 12 | **"Protected by Copyright Match" for creators** | A creator page that lists copies of their video found elsewhere on the platform, and lets them track, block or claim | `ApplyOwnerMatch` with block, mute and track (S `post-service/internal/service/copyright_owner_match.go:14-33`); fingerprinting in shadow (`00-map.md` 7) | **L** (needs a fingerprint provider; plan of 24 Oct) | Re-uploads of film and comedy clips are a top creator complaint in India. A visible protection is a reason to upload here first |
| 13 | **Podcast feeds out** | Publish a channel or series as an audio podcast RSS, so it shows up in Spotify and Apple Podcasts | Channel RSS with enclosures (S `post-service/internal/service/channel_feed.go:236`; `W app/posttube/channel/[handle]/feed.xml/route.ts:106`) | **S–M** | Creators reach new audiences with no extra work, and the platform gets backlinks. It needs audio-only renditions and prod `FEED_MEDIA_BASE_URL` |

**Ideas that conflict with a decision taken**

| Idea | Conflict |
|---|---|
| Super Chat or gifts during premieres or live | Your 2 Oct decision: no gifts or coins on live |
| Emoji reactions on videos | Your 24 Oct decision: heart only on videos |
| Downloadable video files | Your 2 Oct decision: offline copies only, viewers never get a file |
| Mini-player on the web | Your 19 Oct decision to hide it (F-1.5 asks whether to reopen it) |
| Watch parties with guests on camera | Your 2 Oct decision: no live guests |

### Top five to do first

| Order | Idea | Why first |
|---|---|---|
| 1 | Auto captions and auto chapters (idea 2) | Small. The server is mostly built. It closes an Android parity gap and lifts search and watch time |
| 2 | Creator "today" panel with "Watching now" (idea 5, Part 3 L1, L2) | Answers your realtime request. Reads data that already exists. Size S–M |
| 3 | Premieres (idea 1) | The columns, scheduler and fan-out exist. It gives each upload a launch moment |
| 4 | "Watch in your language" (idea 3) | The strongest India differentiator. Audio tracks already exist on both players |
| 5 | Audio-only, background play and smart offline (idea 4) | Data-light India. Fixes the Android Data-saver gap and the background-play gap in one piece of work |

**Founder decisions, Part 4**

- **F-4.1:** Approve the top five order, or reorder?
- **F-4.2:** Premieres: with live chat (re-uses PostTube Live chat) or with a countdown and reminder only?
- **F-4.3:** Background play free for everyone, or a Premium perk?
- **F-4.4:** Auto-dubbing and auto captions cost per minute. May the platform pay for them, and which provider? (Same as reels F-4.2.)

---

## What this section could not determine

- Whether the server's keyword filter applies to `/v1/feed/videos` and the Tube home on Android.
- What happens to a playback session when the viewer pauses for more than 10 minutes and then resumes: a new session (and a second view) or the same one.
- Android Creator Hub refresh beyond loading on open and changing the period.
- Whether the user-service subscription fan-out path (`IN ('all','uploads')`) is still reachable (`00-map.md` question 9).
- How a tip is funded, and whether the web Thanks button answers 503 in prod today.
- YouTube and RUTUBE details: they were not walked again for this section.
