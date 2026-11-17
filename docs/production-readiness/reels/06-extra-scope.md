# Reels: extra scope (settings, upload settings, realtime money, new features)

- **Date:** 2026-11-17. Read-only. Same trees as `00-map.md` (backend and Android `C:\workspace\modernsmapp` on `codex/module-01-02-launch-safety`; web `C:\workspace\wt\pb-echo` on `feat/echo-web`).
- **This is not part of the launch verdict.** It is a gap list and a list of ideas. Nothing here is rated P0 or P1. The words used are **present**, **partial** and **gap**.
- **What the founder asked:** "while checking the Reels and LongVideo, also check did we miss anything in functional interms Settings, Upload Settings, and make more realtime with monetization and can we add even more creative features to attact Users?"
- **How it was built:** three read-only passes (Android, web, server settings), each citing file and line, plus a direct read of monetization-service and analytics-service for Part 3. Claims about TikTok, Instagram Reels and YouTube Shorts come from product knowledge. Those apps were **not walked** for this section; walk them before building any UI (founder rule, 14 Nov).
- **Path short forms:**
  - **A:** `mobile/android/`
  - **W:** `C:\workspace\wt\pb-echo\src\`
  - **S:** `Architecture/services/`
  - **IP:** `identity-platform/services/`
  - **SA:** `A core/profile/.../data/SettingsApi.kt`
  - **RVM:** `A feature/feed/.../ui/reels/ReelsViewModel.kt`
  - **MORE:** `A core/ui/.../UsPostMoreState.kt`
  - **PRIV:** `A feature/profile/.../ui/PrivacySettingsScreen.kt`
  - **HUB:** `A feature/profile/.../ui/SettingsHubScreen.kt`
  - **DS:** `A core/datastore/.../SettingsDataStore.kt`

---

## Summary for the founder

- **Settings: the server is ahead of the apps.** Private account, comments, mentions and tags, muted words, "not interested" and notification categories are all real and enforced on the server. The gaps are on the viewer side:
  - Data saver does nothing for reels on the web.
  - Muted words do nothing in Reels on Android.
  - Captions on streamed reels are missing on Android.
  - Screen-time and break reminders are saved but never enforced on the web.
  - No restricted mode, no default upload settings, and no account-wide remix or download switch on any client.
- **Upload settings: Android and web collect different things, and the edit screen does not match either.** The worst mismatches:
  - The AI label: Android never sends it, and the Android reel player never shows it.
  - Product tags: the server has them, but no upload screen offers them.
  - Captions: neither upload flow has them.
  - The audience lists differ between upload and edit.
  - The web sends hidden fields for shorts ("share to feed", "made for kids", language "en") that the creator never chose.
- **Monetization is not realtime.**
  - In production today, earnings are **off**: the earnings workers do not start while `MONETIZATION_WRITES_ENABLED=false`.
  - With them on:
    - Views reach the creator in about 1 to 17 minutes.
    - Estimated earnings take **51 to 75 hours**.
    - Eligibility progress takes up to a day.
    - A payable balance arrives once a month, around the 3rd.
    - Withdrawals stay closed.
  - **Neither app has a screen for creator-fund earnings or eligibility.**
- **Ideas.** The best fits are:
  1. A live "today" view and earnings estimate for creators
  2. An eligibility progress bar
  3. Commerce product tags on reels
  4. Language audio tracks as a feature people can find
  5. A data-light Reels mode with offline copies

---

## Part 1. Settings that affect reels

### 1.1 What exists today, per client and on the server

Legend: **S** = a server field and route exist and the server enforces the setting. **S-stored** = the server stores it and nothing reads it. **local** = kept on the device only. **—** = not found.

| Setting | Android | Web | Server | Notes |
|---|---|---|---|---|
| Autoplay / auto-scroll to the next reel | partial. More-sheet row only (MORE:316-317, 493). Local `reels_auto_scroll` (DS:216-222), read RVM:764-770. Off by default | partial. Reel menu only (W `features/reels/components/ReelMoreMenu.tsx:150`). localStorage `reels_player_prefs_v2` (W `features/reels/playback/playerPrefs.ts:12,46,100-107`). Default loop (:60) | — (searched autoplay, data_saver) | Not on any Settings page on either client |
| Data saver / quality on mobile data | **present.** Hub switch (HUB:134-135, 247-256); caps 480p on metered networks (A `core/media/.../DataSaver.kt:35-74`), applied at `PlayerPool.kt:112`. Skipped in the chat shared-reels viewer (A `feature/chat/.../SharedReelsViewer.kt:130`) | **switch does nothing for reels.** Page `W app/settings/data-saver/page.tsx:203-219` promises "Reels capped at 240p" (:237), but nothing under `W features/reels` reads `useDataSaver` | — | Web gap: the page promises something the player does not do |
| Captions on/off | **gap for streamed reels.** The Captions row appears only for an offline copy (RVM:745-750). The choice lasts one session only (RVM:731-743) | present. Toggle (W `ReelMoreMenu.tsx:148`), saved locally | Tracks exist: `GET /v1/subtitles/:mediaId` (S `media-service/internal/http/clips_handler.go:63`) | No caption-language default anywhere. Web plays the first track whatever its language (W `features/reels/.../useSubtitleTrack.ts:59`) |
| Audio-track (dub) language | present, local (DS:231-239), shared with long video; read RVM:825, 839-860 | present, local (`playerPrefs.ts:48,84`); read W `features/reels/components/ReelsScreen.tsx:264-266` | — . `viewer_media_prefs` (S `feed-service/database/setup.sql:32-39`) holds dwell data, not language | Not synced across devices. No settings page |
| Mute by default | local `reels_sound_on`, default muted (DS:202-208) | local, default sound on (`playerPrefs.ts:11,55`) | — | The two clients start with opposite defaults |
| Content preferences / interests | — (category chips in Explore are a browse filter, A `.../reels/explore/ReelsExploreViewModel.kt:46-78`) | — (chips are a filter, W `features/reels/explore/exploreApi.ts:106,135`) | partial. `user_preferences.feed_mode`, `long_video_frequency` (S `feed-service/database/setup.sql:53-65`; route `/v1/feed/preference`, S `feed-service/internal/http/handler.go:106-107`). Neither affects reels | No interest picker anywhere |
| Filter keywords (muted words) | **switch does nothing in Reels.** Screen exists (A `feature/settings/.../content/ContentPreferencesScreen.kt:63-96`, SA:93/96), but it is applied on Home only (A `feature/feed/.../FeedViewModel.kt:242-243`) | present (W `app/settings/_lib/keywordFilters.ts:9-12,95-101`) | **S.** `trust.keyword_filters` (S `trust-safety-service/database/migrations/004_trust_extras.sql:20-28`); applied in feed hydration (S `feed-service/internal/service/keywordfilter.go:201-211`, `hydration.go:445,511`) | Server filtering may already cover reels pages, because the reels feed shares hydration. The Android Reels screen adds no filtering of its own. Confirm on a device |
| Not interested / don't recommend channel / hidden channels | present (MORE:348-351; page `RestrictedAccountScreens.kt:151`) | present (W `features/reels/components/MoreMenu.tsx:53-54`; W `app/settings/hidden-channels/page.tsx`) | **S** (S `feed-service/internal/service/feedback.go:240-261`, `feed.go:1415-1440`) | Done on all three |
| Watch history (view, pause, clear) | Tube only (A `feature/tube/.../history/HistoryScreen.kt:185,199`). Reels write no history | Tube only (W `app/posttube/history/page.tsx`). The pause flag is localStorage, read only by the PostTube watch page | partial. `/v1/videos/history` (S `post-service/internal/http/handler.go:427-434`) accepts flicks if sent. Analytics `viewer_history` in Scylla has no user route | Gap: there is no "reels you watched" list, and "pause history" does not stop reel telemetry |
| Restricted mode / sensitive content | — | — | partial. Per-post `age_restricted` with a date-of-birth gate (S `post-service/internal/service/age_gate.go:106-133`). `trust.teen_accounts.content_filter` is S-stored (004:37) | Gap on every layer for adults who want a calmer feed |
| Screen time / break reminders | **present.** Daily limit and sleep hours (A `feature/settings/.../screentime/ScreenTimeScreen.kt:65-97`) are enforced app-wide (A `app/.../screentime/ScreenTimeGuardCoordinator.kt`). Focus mode, nudges and hide-likes have no UI or reader (A `SettingsDtos.kt:258-261`) | **switches do nothing.** Six controls at W `app/settings/wellbeing/...` (217-429) save to the server. `useLogScreenTime` is never called and nothing reads the limits | S-stored. `digital_wellbeing` (S `user-service/internal/store/profile_extras.go:43-54`); routes `profile_extras_handler.go:34-37`. No service enforces it | Web gap: settings that do nothing |
| Private account | present (PRIV:149-181) | present (W `app/settings/privacy/page.tsx:233-242`) | **S** (IP `user-service/internal/store/users.go:38`; graph `S graph-service/internal/permission/permission.go:436-444`; post `privacy_gate.go:203-219`; media access) | Done |
| Who can comment (account) | present: Everyone / Friends (PRIV:192-199, 356-359) | present (W `privacy/page.tsx:278-287`) | **S** (`allow_comments_from`, IP `users.go:40`; enforced S `post-service/.../post_comments.go:164`) | The account values (everyone/friends) differ from the per-post values (everyone/followers/nobody). Creators will find this confusing |
| Who can reuse my sound / remix (account) | — | — | — (per-post `remix_setting` only, S `post-service/internal/service/audio.go:169-178`) | Gap |
| Duet / stitch permission | — | — | — (`remix_type` only describes a post) | Gap. There is no duet or stitch feature either |
| Who can keep an offline copy (account) | — | — | — (per-post `allow_download`, S `post-service/internal/service/media_download.go:44`) | Gap |
| Who can mention / tag me | present (PRIV:205-226) | present (W `privacy/page.tsx:304-328`) | **S** (IP `users.go:51-52`; S `post-service/.../mention_tag_privacy.go:85-132`) | Done |
| Liked videos visibility | — | — | `/v1/reels/liked` is owner-only (S `post-service/internal/http/reel_engagement.go:28,160-186`) | Private by construction. A "show my likes" option would be an addition, not a fix |
| Activity status | chat only (PRIV:256) | chat only (W `app/settings/messaging/page.tsx:186-192`) | **S** (graph `permission.go:541-559`) | Done |
| Notifications: likes, comments, follows, mentions, new uploads, live, quiet hours | present (A `NotificationSettingsScreen.kt:105-219`; channel bell A `ChannelApi.kt:88`) | present, except there is **no per-channel bell** (W `app/settings/notifications/page.tsx:252-545`) | **S.** v2 preferences (S `notification-service/internal/store/postgres/notification_prefs.go:22-83`), enforced in `ResolveDelivery` (`service/preferences.go:33-120`). Flicks map to `new_videos` (:226-227) | No reel-specific category. Legacy `muted_types` and `sms_enabled` are written and never read |
| Creator upload defaults (audience, comments, keep-a-copy, remix) | — (hard-coded, A `feature/post/.../ReelPublishViewModel.kt:172,184-187`) | **partial and not used for reels.** Hub Preferences (W `features/posttube/hub/publishDefaults.ts:4-22`) are localStorage only and apply to long video and podcasts only (W `features/upload/studioDefaults.ts:18-20`) | — | Gap |
| Default playback speed | session only (RVM:714-729) | local (`playerPrefs.ts`) | — | Minor |
| Blocked accounts | present | present. The Privacy page link goes to `/settings/security`, not `/settings/blocked` (W `privacy/page.tsx:458`) | **S** | Web link points to the wrong page |
| App / content language | — | — | — | Gap. India needs this (see Part 4) |

### 1.2 Switches that do nothing, and server features with no switch

**Switches that do nothing**

| Where | Which | Evidence |
|---|---|---|
| Web | Data saver promises 240p for reels; the reels player ignores it | W `app/settings/data-saver/page.tsx:237` |
| Web | Wellbeing: daily limit, focus, sleep hours, break reminders, hide like counts | saved by W `hooks/useWellbeing.ts:19,38`; no reader; `useLogScreenTime` never called |
| Web | History "pause" is ignored by reels | W `features/posttube/.../WatchPage.tsx:289` is the only reader |
| Web | Hub "Preferences" defaults never apply to shorts | W `features/upload/studioDefaults.ts:18-20` |
| Android | Muted words are not applied in Reels | A `FeedViewModel.kt:242-243` only |
| Server | `digital_wellbeing`, `teen_accounts.daily_limit_mins` and `content_filter`, `auto_filter_abusive_content`, legacy `muted_types` | stored, never read (server pass) |

**Server features with no switch**

| Server feature | Missing switch |
|---|---|
| Per-post `comment_access` everyone/followers/nobody (S `post-service/.../post_screen_fields.go:189-205`) | Android upload never sets it. Only the Android edit sheet and the web expose it |
| Per-post `remix_setting=allow_audio_only` | Android upload offers allow and disallow only. `allow_audio_only` is in the edit sheet |
| Notification `new_videos` per creator (channel bell, S `post-service/internal/store/postgres/channel_subscriptions.go:184-194`) | No bell on the web |
| `/v1/feed/preference` `feed_mode` | Not reels-aware, and no reels client reads it |
| Subtitles routes, including auto-generate (S `media-service/internal/http/clips_handler.go:68-69`) | No caption switch on Android streamed reels, and no caption upload in either reels upload flow |

### 1.3 Compared with the reference apps

TikTok (TT), Instagram Reels (IG), YouTube Shorts (YT). "Has it" is from product knowledge; confirm when you walk the apps.

| Setting | Here: Android / web / server | Reference apps | Recommendation |
|---|---|---|---|
| Data saver for reels | present / **does nothing** / — | TT, IG, YT | **Add for launch** (web): fix it or remove the promise. Showing a setting that does nothing is worse than having none |
| Muted words in Reels | does nothing / present / S | TT, IG | **Add for launch:** apply the Home filter on the Reels page too. The server already has the list |
| Captions on streamed reels and caption language | gap / partial / tracks exist | TT, IG, YT | **Add for launch:** reels are watched muted; captions drive watch time |
| Screen-time / take a break | present / does nothing / S-stored | TT, IG, YT | **Add for launch** (web): wire the stored limit, or hide the page |
| Restricted mode | — / — / partial | TT, YT | **Add later:** needs a sensitive-content label pipeline first |
| Interests picker on first run | — / — / — | TT, IG | **Add later:** helps a new account's first reels page. Cheap if it only seeds categories |
| Watch history for reels | — / — / partial | TT, YT | **Add later** |
| Default upload settings (audience, comments, remix, keep-a-copy) | — / partial / — | TT, IG, YT | **Add later:** store it on the server, not in localStorage, so Android and web agree |
| Account-wide remix / sound reuse / keep-a-copy | — / — / — | TT (duet, stitch, download), IG (remix) | **Add later**, together with the defaults above |
| Duet / stitch permission | — / — / — | TT | **Skip** until there is a duet or stitch feature |
| Show my likes publicly | private by construction | TT (optional) | **Skip:** private is the safer default |
| App and content language | — / — / — | TT, IG, YT | **Add later**, but early (India; see Part 4 idea 4) |
| Per-channel bell on web | present / gap / S | YT | **Add later** (web) |
| Same mute default on both clients | muted / sound on | TT and IG start with sound on | **Add for launch:** pick one default (decision F-1.2) |

**Founder decisions, Part 1**

- **F-1.1:** For launch, should the web Data saver and Wellbeing pages be wired up, or hidden until they work?
- **F-1.2:** Should reels start muted (Android today) or with sound on (web today)?
- **F-1.3:** Should creators get saved upload defaults (audience, comments, remix, keep-a-copy) on the server before launch, or after?

---

## Part 2. Upload settings for shorts

**The founder's rules:**
- shorts have 3 steps
- no kids, age or altered-content steps
- the AI label is shown to viewers
- shorts are at most 10 minutes
- visibility and category are required on the web

**Where things are:**
- **Android:** upload `A feature/post/.../createhub/ReelSurface.kt`; body `A feature/post/.../data/dto/CreatePostDtos.kt:37-268`, built at `ReelPublishPipeline.kt:439-494`; edit `A core/feed/.../ui/edit/PostEditSheet.kt`; PATCH `PostEditApi.kt:109-159`
- **Web:** upload `W features/upload/`; create `W features/reels/data/reelsApi.ts:334-345` plus `W features/upload/studioApi.ts:35-68`; drafts `useUploadStudio.ts:488-601`; edit `W features/posttube/hub/.../EditSheet.tsx`, PATCH `hubApi.ts:580-634`
- **Server create body:** `S post-service/internal/http/create_post_handler.go:40-166`

### 2.1 Field by field

| Field | Android upload → sent | Web shorts upload → sent | Edit after publish (Android / web) | Server |
|---|---|---|---|---|
| Steps | 3: pick or record, then edit (Banuba or the 5-tool studio, `studio/ReelStudioScreen.kt:81-82`), then details | 3 (W `features/upload/tokens.ts:20-25`) | — | — |
| Title | not shown; sends `""` (Pipeline:461) | **required** (W `features/upload/validation.ts:76`) | shown on both | stored |
| Caption | :407 → `text` | :106-116 → `text` / draft `caption` | both | stored |
| Hashtags | chips :454-465 → `hashtags` | typed in the caption (chips are long-only) | both | merged with the caption parser (create_post_handler.go:114) |
| @mentions | "Mention people" :877-891 → sent as **both** `mentions` and `tagged_user_ids` (VM:795-796) | text only | absent on both | enforced against `who_can_mention` (S `post-service/.../mention_tag_privacy.go`) |
| Cover: frame or own image | :416-422, 563-574 → `cover_media_id` | :135-138, 274-380 → `cover_media_id` | **Android absent**; web present (hint says "16:9 works best" even for a short, EditSheet:365) | stored |
| Sound: library, use-this-sound, volume mix | present → 4 audio fields (:477-480); `audio_start_ms` always 0 | present (W `features/upload/StudioSoundSection.tsx:94-110`) → same 4 fields | absent on both | stored. The curated library is empty (memory note "Sound library plan") |
| Voiceover | absent. Banuba music is off (`banuba/BanubaKoinModule.kt:63-76`) | absent | — | `POST /v1/audio/voiceover` exists (S `media-service/internal/http/audio_handler.go:33`) and **no client uses it** |
| Audience | Public / Followers / Private (:1426-1428) | public / followers / friends / unlisted / private (W `StudioSettings.tsx:30-51`) | Android Public / Unlisted / Private; web public / unlisted / private / scheduled | 8 values (migration 066) |
| Comments on/off | `no_comments` | `no_comments` + `comment_access` | both; plus "who can comment" | enforced |
| Keep a copy (allow download) | :1017 → `allow_download` | **absent at upload** | both ("Save offline") | enforced; defaults on |
| Sound reuse / remix | allow / disallow | allow / allow_audio_only / disallow | both, with audio-only | enforced for sounds (S `post-service/internal/service/audio.go:169-178`) |
| Hide share | :1009 → `hide_share` | absent | absent | stored |
| Schedule | present → `publish_at` | present → `publish_at` / `schedule_at` | separate `PATCH /schedule` on both | enforced (5 min to 30 days) |
| Location | typed name → `location_name`; `location_place_id` never set | place search → both fields | absent on both (Android has only `recording_location`) | stored |
| Category / topic | optional (:911-924) | **optional** (W `validation.ts:9-11`) | both | stored |
| Language | not asked; device locale sent (Pipeline:443, 504-510) | not asked for shorts; **`"en"` always sent** (W `features/upload/types.ts:174`) | both | stored |
| Captions / subtitles | absent | absent for shorts (upload code is reachable only from long video, `useUploadStudio.ts:585-592`) | Android absent; web present (EditSheet:836-929) | routes exist, including auto |
| Alternate audio / dubbing | absent at upload; owner's More sheet (MORE:325-326) | absent at upload; owner dialog on the reel (W `ReelAudioTracksDialog.tsx`) | — | present; `generate` answers 503 until `MEDIA_DUBBING_BACKEND` is set (`00-map.md` 5.6) |
| AI-generated label | **absent, never sent** | present (W `StudioSettings.tsx:271`) → `ai_generated` | Android: long video only; web has only "Altered", not the AI label | stored (create_post_handler.go:135). **Shown to viewers on web** (W `features/reels/components/ReelOverlay.tsx:74`). **Not found in the Android reel player** (no `aiGenerated` reader in `A feature/feed` or `A core/ui`) |
| Paid partnership / branded | **absent** (`paid_promotion`, `is_branded` never set) | `paid_promotion` (W `StudioSettings.tsx:267`); no `is_branded` | both have paid promotion | both fields stored |
| Action button ("Apply now") | present (:510-518) → `action` | present (W `PublishStep.tsx:126`) → `action` | both | validated with a safety check (create_post_handler.go:161-165); the full plan is still queued (memory "Action button queued") |
| Product tags (commerce) | **absent** | **absent** (only an analytics page, W `app/creator/product-tags/page.tsx:35-37`) | absent | **present**: `POST/GET/DELETE /v1/posts/:id/product-tags`, impression and click (S `post-service/internal/http/handler.go:438-443`) |
| Collaborators / credits | long video only | long video only | long video only | long video only |
| Series / playlist | absent | absent for shorts | absent | flick series exist (`00-map.md` 3.2) but `/v1/series` has **no gateway prefix** (`00-map.md` 3.0) |
| Also share to / crosspost | none; `distribution` fixed with `notify_subscribers=true` (:460) | switch hidden, but **`publish_to_feed:true` and `share_to_postbook:true` still sent** (W `useUploadStudio.ts:619`, `studioApi.ts:47`) | absent | the feed drops short-form from Home anyway (S `feed-service/internal/service/feed.go:497`), so these fields have no effect on reels. Crosspost routes exist (`crosspost.go:16-22`) |
| Save as draft | **absent.** The hub list is read-only; Banuba drafts are off (`BanubaKoinModule.kt:75`) | present (W `UploadStudio.tsx:342`) | — | `/v1/reels/drafts` full set |
| Members-only | present → `tier_required_id` | present → `tier_required_id` | both | enforced at create (create_post_handler.go:151-159). Payments stay off (`MEMBERSHIPS_ENABLED=false`) |
| Anonymous posting | absent | absent | — | an anonymous model exists for channels (memory "Anonymous posts model"); not offered on reels |
| Checks before publish (copyright, policy) | none. Copyright review is read back for long video only (Pipeline:280-283) | a required-fields checklist only (W `PreviewPanel.tsx:94-101`). `copyrightCheck` is never set (W `types.ts:121,195`) | review notice after publish, on both | copyright fingerprinting runs in shadow; title holds and auto-block are on in prod values (`00-map.md` 5.6) |
| Kids / age / altered | absent (correct) | absent in the UI (correct), but **`is_made_for_kids:false`, `altered_content:false` still sent** (W `studioApi.ts:42,44`) | **web edit shows all three for shorts** (EditSheet:445,557,559); Android long video only | stored |
| 10-minute cap | `ReelLength.kt:26`; longer → "Post as a Video" (`VideoGate.kt:58-64`) | `W lib/videoLimits.ts:13`; reclassified (`fileRules.ts:46-47`) | — | enforced after measurement (S `post-service/internal/consumers/media.go:261-264`) |

### 2.2 Fields sent but unused, and fields the server takes that no client collects

**Sent by a client, unused or never chosen by the creator**
- Web: `publish_to_feed:true`, `share_to_postbook:true` (draft: `cross_post_postbook`). The feed ignores them for reels.
- Web: `language:"en"`, `is_made_for_kids:false`, `altered_content:false`, `no_likes:false`, all sent for shorts without asking.
- Android: `app_origin="postbook"` on every reel (default left as is).
- Android: one people list is sent twice, as `mentions` and `tagged_user_ids`, so "mention" and "tag" cannot differ.

**Accepted by the server, collected by no reels client** (server field at create_post_handler.go:40-166):
- `product tags` (separate route)
- `is_branded`, both clients
- `ai_generated` on Android
- `comment_access` and `paid_promotion` on Android upload
- `location_place_id` on Android
- `link_url`
- `comment_moderation`
- `cover_index`
- voiceover (separate route)

**Required by the server, missing from a client:** nothing found. The server requires only content type, media and a valid audience.

**Founder's rule versus the code**
- "Category required on the web" is not met: topic is optional for shorts (W `validation.ts:9-11`).
- "AI label shown to viewers" is met on the web only.

### 2.3 Compared with the reference apps

| Field | Here (A / W / S) | TT / IG / YT | Recommendation |
|---|---|---|---|
| AI-generated label | gap / present / S | all three (TT and YT require a disclosure) | **Add for launch** on Android: send it at upload and show it in the player. It is your own rule |
| Paid partnership / branded content | gap / partial / S | all three | **Add for launch:** a legal disclosure in India (ASCI). One toggle on Android |
| Same audience list in upload and edit | mismatched | consistent | **Add for launch:** one list, from the server |
| Stop sending hidden flags (feed, kids, altered, "en") | — / sends them / — | — | **Add for launch:** sending choices the creator never made is a data-quality and trust problem |
| Hide kids/age/altered on the web edit for shorts | — / shows them / — | — | **Add for launch:** matches the 21 Oct rule |
| Captions at upload (auto) | gap / gap / S | TT, IG (auto captions), YT | **Add later:** the server route exists; one button "Auto captions" |
| Cover editing after publish (Android) | gap / present / S | TT, IG | **Add later** |
| Save as draft (Android) | gap / present / S | all three | **Add later.** Android is the super app; drafts are a creator expectation |
| Product tags | gap / gap / S | TT Shop, IG product tags, YT Shopping | **Add later**, first after launch (Part 4 idea 3) |
| Voiceover | gap / gap / S route | TT, IG | **Add later** |
| Collaborators (co-author) | long only / long only / — | IG Collab, TT | **Add later** |
| Series for shorts | gap / gap / not routed | TT series (paid), YT playlists | **Skip for now:** route is not exposed |
| Location with a place id | partial / present / S | all three | Add later (Android place search) |
| Pre-publish copyright check shown to the creator | gap / gap / shadow | TT "content checks", YT checks | **Add later**, tied to the copyright plan (24 Oct) |
| Title on shorts (web requires it) | none / required / optional | TT and IG have no title | **Decide** (F-2.2) |

**Founder decisions, Part 2**

- **F-2.1:** Should Android send and show the AI label and the paid-promotion label before launch? (Recommended: yes.)
- **F-2.2:** Should shorts on the web keep a required title? Android has none.
- **F-2.3:** Your rule says category is required on the web for shorts; the code makes it optional. Which is right?
- **F-2.4:** Which audience list should shorts use? Upload and edit disagree on both clients.

---

## Part 3. How "realtime" monetization is

### 3.1 How shorts earnings work today (from the code)

| Step | What happens | Evidence |
|---|---|---|
| What counts | **Views:** a playback session becomes a view when it ends (`play_end`) or after 10 minutes of silence. A finaliser checks every 60 s. Prod uses `ANALYTICS_VIEW_SOURCE=sessions`. **Money cap:** one view per viewer per video per UTC day. Self-views are excluded | S `analytics-service/internal/aggregation/finalizer.go:13-17`; `daily.go:30-34`; `00-map.md` 5.6 |
| Where views are kept | `analytics.events_raw`, `playback_sessions`. `content_hourly_agg` is rebuilt every 5 min for the current and two previous hours. `content_daily_summary` is re-rolled every 15 min for open days, and a day freezes 48 h after it ends | `hourly.go:22,84-88`; `daily.go:40-46` |
| Displayed view count | Rolled-up days plus today's hourly rows. **Today's views are not capped**, so a looping viewer counts every session until midnight. After midnight UTC the day folds to the capped figure, so **a creator can see the count go down** | S `analytics-service/internal/store/postgres/aggregates.go:325-351` |
| Eligibility | Over 90 days, the creator needs **at least 3 earning videos AND (1,000 view-score OR 10 hours of watch time)**. Read from `analytics.v_creator_daily_metrics_v1`. The stored status is re-rated nightly at 02:00 UTC. `GET /creator-fund/status` computes a fresh decision on every read; `POST /apply` forces a re-rate | S `monetization-service/internal/service/creator_fund.go:29-36,125-146,228-243`; `internal/store/postgres/creator_fund.go:475-488`; `internal/workers/creator_fund.go:221-233`; `internal/http/creator_fund_handler.go:25-61` |
| Rate | Flick baseline **₹3 per 1,000 views** (long video ₹50), with a quality multiplier. Platform fee 30% (creator keeps 70%), with a 0% intro window | S `monetization-service/database/migrations/010_creator_fund.sql:58`; `014_creator_fund_quality_multiplier.sql:62`; `creator_fund.go:33` |
| Earnings accrual | `runCreatorFundAccrual` runs **daily at 03:00 UTC** and prices only the days analytics calls final, looking back 7 days. A day is final once it is frozen (48 h after it ends) and the rollup watermark has passed it | `internal/workers/creator_fund.go:14-43`; S `analytics-service/internal/aggregation/daily.go:63-81` |
| Settlement (payable balance) | `runCreatorFundPeriodSettlement` wakes **hourly**. Prod cadence is **monthly** (`CF_SETTLEMENT_CADENCE: monthly`). It pays a closed period 1 day after it ends at 04:00 UTC, and waits further if the last day is not final yet | `creator_fund.go:104-181`; service `creator_fund.go:36-37`; `deploy/services/monetization-service/values-prod.yaml:101` |
| Withdrawal | Closed: `RequestPayout` answers 503 `PAYOUTS_NOT_ENABLED`. Earnings responses carry `estimate:true, withdrawable:false` | `cmd/server/main.go:420`; `internal/http/creator_fund_handler.go:89-95` |
| **Switched off in prod** | `MONETIZATION_WRITES_ENABLED: "false"`, `MONETIZATION_PAYOUTS_ENABLED: "false"`, `MEMBERSHIPS_ENABLED: "false"`. With writes off, **no background worker starts at all**, so nothing accrues or settles. Only the read routes answer (beta allow-list) | `values-prod.yaml:82-91`; `internal/runmode/runmode.go:125`; `cmd/server/main.go:380-381`; `internal/http/handler.go:242-267` |

**What creators can see**

| Client | Screen | Data | Refresh |
|---|---|---|---|
| Web | `/posttube/hub/insights` and Overview: views, watch time, likes, a "realtime" strip of the last 48 hours | `GET /v1/analytics/creator/me` (W `features/posttube/hub/hubApi.ts:1320-1322`); 48 h series from hourly rows (S `analytics-service/internal/service/creator.go:141-244`) | on open; react-query staleTime 60 s; no polling (W `useHub.ts:625-626`; global `refetchOnWindowFocus:false`, W `app/providers.tsx:109-116`) |
| Web | Per-video insights | `GET /v1/analytics/content/:id` | staleTime 5 min |
| Web | `/monetization`: wallet balance, lifetime, pending payout | `GET /v1/monetization/dashboard` | staleTime 30 s. **The "Earnings Overview" chart is hard-coded bars** (W `features/.../EarningsDashboard.tsx:73-86`) |
| Web | Creator-fund earnings and eligibility | hooks exist (W `hooks/useMonetization.ts:615-669`) | **no page uses them** |
| Android | Creator Hub insights | `GET /v1/analytics/creator/me` (A `feature/tube/.../data/hub/CreatorHubApi.kt:27`) | on open (refresh behaviour beyond that not traced) |
| Android | Earnings, eligibility, statements | **none.** No Android code calls `v1/monetization/creator-fund` or `creator-ledger` | — |

### 3.2 The real delay today

| From a view to... | Today in prod | If `MONETIZATION_WRITES_ENABLED` were on |
|---|---|---|
| (a) the view count the creator sees | **About 1 to 7 minutes** when `play_end` arrives: finaliser up to 60 s, hourly rebuild every 5 min, feed view-count cache 60 s (S `feed-service/.../view_counts.go:36`), web staleTime 60 s. **Up to about 17 minutes** when the app is killed mid-play (10 min silence + 60 s + 5 min + caches). Client batching of analytics adds an amount that was not measured | same |
| (b) the estimated earnings figure | **Never.** No accrual runs, and no screen shows it | **51 to 75 hours.** A view on day D is priced at 03:00 UTC on D+3 (08:30 IST) |
| (c) eligibility progress | The fresh decision on `GET /status` includes the view after the first daily rollup that covers its day: **0 to 24 hours + up to 15 minutes**. **No screen shows it** on either client | same. The stored status flips at 02:00 UTC, or at once on `POST /apply` |
| (d) a payable balance | **Never** (no settlement; withdrawals closed) | **Monthly.** Around the 3rd of the next month, so 3 to about 34 days after the view. Not withdrawable while payouts are off |

### 3.3 What "more realtime" could mean

**How I read your request:** creators should see their earnings and their progress towards eligibility update live, while the money itself stays careful. **Please confirm this reading** (F-3.1).

| # | Option | What exists to build on | What must be built | Fraud and reconciliation risk | Size |
|---|---|---|---|---|---|
| R1 | **Eligibility progress bar** ("2 of 3 videos, 6.5 of 10 hours") on Android and web | `GET /creator-fund/status` already returns a fresh decision with thresholds (S `monetization-service/internal/http/creator_fund_handler.go:25-44`), and it is open in beta (`handler.go:254`). Web hooks exist (W `useMonetization.ts:615-669`) | One card on web, one screen on Android. Optionally add today's hourly rows so progress moves the same day | Low. Progress is not money. It can still drop when today's uncapped views fold at midnight, so say "updates through the day; final at midnight UTC" | **S** |
| R2 | **Live view counter** on the creator dashboard and per reel | Hourly rows rebuilt every 5 min; the 48 h strip (S `analytics-service/internal/service/creator.go:141-244`); the ws-gateway already relays `post:<id>` and `feed:post_update` Redis channels (`chat-service/services/ws-gateway/internal/http/server.go:794-803,1087-1088`) fed by post-service (S `post-service/internal/engagement/consumers/ws_broadcaster.go:96-127`) | **Cheap version:** 30–60 s polling while the screen is open. **Push version:** analytics publishes a per-creator channel after each 5-min rebuild, and the gateway relays it (needs an owner-only room check) | Low. Fix the count dropping at midnight (cap today's figure too, or label it "today, not final") | S (polling) / M (push) |
| R3 | **Near-realtime estimated earnings** ("about ₹4.20 today, estimate"), with the final figure after reconciliation | Rate sheet and quality bands are public (`/creator-fund/rates`, `/quality-bands`). Estimate labelling exists (`estimateFlags`, `creator_fund_handler.go:89-95`). The pure functions `ComputeGrossPaise` and `SplitEarnings` (service `creator_fund.go:150-167`) | A read-only "today estimate" endpoint: today's capped views × rate × fee. It must never write the ledger. A clear "estimate, final on D+3" line, and a reconciliation note when the final differs | **Medium.** Creators treat the first number as owed. View fraud (loops, bots) is filtered only at the daily rollup, so the live figure will be higher than the final. The January remediation reversed 19 settlements (memory note "Monetization remediation state"); showing larger live numbers that later shrink invites the same disputes. Mitigations: cap per viewer live, show a range, round down | **M** |
| R4 | **"Earning now" badge per reel** | Same as R3, per content id; the eligibility decision | A per-reel flag: creator eligible, reel earning, not members-only or restricted, no copyright hold | Low to medium. It must clear when copyright or moderation holds a reel, or creators will dispute | **S** after R3 |
| R5 | **Shorter accrual delay** (D+3 down to D+1 or D+2) | The finality rule and freeze window (`daily.go:42-81`) | Shrink `freezeWindow`, or accrue provisionally and correct | **High.** The 48 h window exists because late events re-rolled priced days and failed whole settlements (`workers/creator_fund.go:23-30`). **Not recommended.** R3 gives the feeling of speed without touching this | M |
| R6 | **Faster settlement** (semi-monthly or weekly) | `semimonthly` cadence is already supported, one env var (service `creator_fund.go:64-68`) | Turn on writes, then payouts. Needs KYC, the payout rail and TDS (out of this section) | Medium. Each run is idempotent and locked (`workers/creator_fund.go:92-103`), but more runs mean more chances for clawbacks | **S** to switch, once payouts are approved |
| R7 | **Tips during reel playback** (not live) | `POST /v1/monetization/tips` and `GET /creators/:id/support` with `tips_enabled` (S `monetization-service/internal/http/tips_handler.go:58`, `creator_support.go:17-101`); the MTube watch page already hides the button in beta | Thanks button on reels, UPI through payments-service only (rule: money only via payments-service on its signed event). How tips are funded today was not traced | Medium: money in, refunds, block checks (sweep #16 already asks graph-service). **No gifts or coins on live** (decision 2 Oct): this is reels only | **M** |

**Recommendation:** do R1, then R2 as polling, then R3 with a clear estimate label and a range. Keep R5 off. Open R6 and R7 only when payouts and payments-service tips are approved. None of these needs `MONETIZATION_WRITES_ENABLED`: R1 to R4 read only. But R3 and R4 show nothing useful until accrual and eligibility are live for real creators.

**Founder decisions, Part 3**

- **F-3.1:** Does "make more realtime" mean creators see earnings and eligibility progress update live, while payment stays monthly? (My reading; please confirm.)
- **F-3.2:** May a live figure be shown that can later go down? If yes, as a range ("₹3–4 today") or as one rounded-down number?
- **F-3.3:** Should the displayed view count stop dropping at midnight? That means capping today's count live, or labelling it "not final".
- **F-3.4:** Should Android get a creator earnings screen before launch? Today it has none.
- **F-3.5:** When writes and payouts open, monthly or semi-monthly settlement?

---

## Part 4. Feature ideas to attract and keep users

Ranked by fit with this product and stack, against the decisions already taken:
- reels never in Home
- no gifts, coins or guests on live
- heart-only reactions and no repost on reels
- no Stories now
- stay on Banuba
- launch only Social, Shorts, Long video with monetization, and Commerce

| # | Idea | Why it helps | Reference app | What exists to build on | Size | Risks |
|---|---|---|---|---|---|---|
| 1 | **Creator "today" panel:** live views, estimated earnings (labelled) and eligibility progress | Creator side. A visible reward loop makes people post again; it is the most direct answer to your "realtime" request | TT Studio, YT Studio realtime | Part 3 R1–R3; `/creator-fund/status`; 48 h strip (S `analytics-service/internal/service/creator.go:141`) | S–M | Estimates that later shrink (Part 3) |
| 2 | **Product tags on reels** ("tap to shop") | Viewer and creator. It ties Shorts to Commerce, both launch apps, and gives creators money outside the fund | TT Shop, IG product tags, YT Shopping | Server tag routes with impressions and clicks (S `post-service/internal/http/handler.go:438-443`); web analytics page (W `app/creator/product-tags/page.tsx`); commerce catalogue | M | Fake or unsafe products; affiliate disclosure (needs the paid-partnership label from Part 2) |
| 3 | **"Watch in your language":** surface audio tracks and auto-dub as a headline feature with a language chip on the reel, plus captions in the viewer's language | Viewer side, very strong for India: one creator reaches Hindi, Telugu and Tamil viewers. Few rivals do this on shorts | YT multi-language audio (long video) | Audio tracks and `dub_<lang>_<rung>` variants (S `media-service/internal/service/audio_tracks.go:223`); `generate` route; the viewer's language choice on both clients; subtitles routes | M (L with auto-dub) | Dubbing cost per minute; voice-clone consent; `MEDIA_DUBBING_BACKEND` unset today |
| 4 | **Data-light Reels:** "Save for later on Wi-Fi" queues the next 10 reels as offline copies; a low-data mode that actually caps quality | Viewer side for poor networks; retention on 2G/3G and metered plans | YT offline, TT data saver | Offline copies (S `post-service/internal/http/offline_copies.go:98-173`; A `core/media/.../offline/*`; Wi-Fi-only switch DS:259-265); Android Data saver works (`DataSaver.kt`) | M | Storage; creators who turned "keep a copy" off must stay excluded (they are, per post); Android offline copies are not encrypted at rest (`00-map.md` 5.7) |
| 5 | **Use this sound + a Trending sounds page** that creators can browse | Creator side. Sound reuse is TikTok's main growth engine | TT, IG audio pages | Sound page (A `feature/feed/.../sound/SoundPageScreen.kt`; W `app/reels/sound/[id]`); trending ranking every 15 min (`00-map.md` 5.3); `/v1/posts/by-sound` | S | The curated library is empty: licensing (Epidemic Sound plan, 24 Oct). Original sounds only until then |
| 6 | **Banuba effects and Face AR filters pack** (10 launch effects, a "Try this effect" chip on reels made with it) | Creator side: effects make ordinary people want to post | TT effects, IG AR | Banuba camera and editor on Android (A `feature/post/.../banuba/*`) | M | No effect bundles exist yet (memory note "Banuba reel studio"); authoring rules in "Banuba effect format"; licence token |
| 7 | **Remix-lite: "Reply with a reel" to a comment** | Creator and viewer: turns comments into new reels without needing duet or stitch | TT video replies | Comments; `remix_setting` (enforced for sounds); `GET /v1/posts/:id/remix-token` (S `post-service/internal/http/flick_series_handler.go:164`) | M | Moderation of replies; respect `remix_setting` |
| 8 | **Shorts → long video bridge:** a "Watch full video" link from a short to the creator's long video | Creator side: drives MTube watch time, which earns about 17x more per view (₹50 vs ₹3) | YT "related video" on Shorts | The Action button plan (`create_post_handler.go:161-165`); linked videos exist server-side (memory "Tube on the web is its own app") | S | Link must stay inside the app; reels still never go to Home (no conflict) |
| 9 | **Auto captions at upload, on by default** | Viewer side (muted viewing) and accessibility | TT, IG, YT | `POST /v1/subtitles/:mediaId/auto` (S `media-service/internal/http/clips_handler.go:69`); caption worker | S–M | Transcription cost; wrong captions in Indian languages |
| 10 | **Weekly creator challenges by hashtag**, with a challenge page and a leaderboard by views | Both sides: a reason to post this week | TT hashtag challenges | Hashtag pages and trending streams (S `post-service/internal/http/handler.go:280-310`; SSE `trending_stream.go:30`) | S–M | Spam hashtags; moderation of the leaderboard |
| 11 | **Echo note on a reel:** a creator can pin a short text note to a reel (context, a correction) | Creator side; uses the Echo work; different from rivals | X community notes (loose) | Echo notes (memory "Echo short-posts programme"; `post-service .../notes_create.go`) | M | Echo is built dark; it must stay inside a launch app |
| 12 | **Members-only reels and early access** for paying members | Creator money beyond the fund | YT members-only Shorts, IG subscriptions | `tier_required_id` at create (create_post_handler.go:151-159); both upload flows | S once payments are on | Blocked on `MEMBERSHIPS_ENABLED` and the memberships plan (23 Oct); text leaks in feeds noted there |
| 13 | **UPI "Thanks" on reels** (not on live) | Creator money; India-native | YT Super Thanks on Shorts | Tips routes and `tips_enabled` (S `monetization-service/internal/http/tips_handler.go:58`, `creator_support.go`) | M | Must go through payments-service; refunds and abuse. Live stays without gifts (decision 2 Oct). Opens only when money actions are approved |
| 14 | **Reels series** ("Part 1, 2, 3", auto-advance) | Viewer retention: people binge a creator's story | TT series, IG series | `flick_series*` tables and routes (`00-map.md` 3.2), but `/v1/series` has **no gateway prefix** (`00-map.md` 3.0) | M | Paid series would need payments |
| 15 | **Bigger bet: Local reels by city** (a "Near me" tab using the place id), for local shops and services | Viewer side; ties into Commerce sellers later | TT Nearby, IG Map | `location_place_id` on posts (create_post_handler.go:137); places autocomplete (`handler.go:297`) | L | Location privacy (exact places of private people); needs city buckets, not exact points (as Dating did) |

**Ideas that conflict with a decision taken**

| Idea | Conflict |
|---|---|
| Reposting reels to your followers | Needs the founder to reopen "no repost on reels" (24 Oct) |
| Emoji reactions on reels | Needs the founder to reopen "heart only on reels" (24 Oct) |
| Reels inside the Home feed (a "Reels for you" rail) | Needs the founder to reopen "reels never in Home" (25 Sep) |
| Gifts or coins during Reels Live, or live guests | Needs the founder to reopen the 2 Oct live decision |
| Story-style 24-hour reels | Needs the founder to reopen "Stories not now" (24 Oct) |

### Top five to do first

| Order | Idea | Why first |
|---|---|---|
| 1 | Creator "today" panel (idea 1, Part 3 R1–R3) | Answers your realtime request. Mostly reads data that already exists. Size S–M |
| 2 | Auto captions on by default (idea 9) and the caption fixes from Part 1 | Cheap. The server routes exist. Reels are watched muted |
| 3 | Product tags on reels (idea 2) | Joins two launch apps. The server is already built. Gives creators a second income |
| 4 | "Watch in your language" (idea 3) | The strongest differentiator for India. The audio-track stack exists; turn it into something viewers notice |
| 5 | Shorts → long video bridge (idea 8) | Small. Moves viewers to the content that pays creators 17x more per view |

**Founder decisions, Part 4**

- **F-4.1:** Approve the top five order, or reorder?
- **F-4.2:** For "Watch in your language", may the platform pay for auto-dubbing, and which provider? Voice-clone consent is needed.
- **F-4.3:** Product tags: only products from your own Commerce sellers, or outside links too?

---

## What this section could not determine

- Whether the reels feed's server-side keyword filter already hides muted words on Android Reels. The server applies it in shared hydration; it needs a device check.
- Android Creator Hub refresh behaviour beyond loading on open.
- How client analytics batching adds to the view-count delay (Android `AnalyticsUploadWorker`, web telemetry flush).
- How a tip is funded today (wallet ledger or payments-service).
- Reference-app details: TikTok, Instagram and YouTube were not walked for this section.
