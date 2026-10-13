# Momentum vs TikTok: parity audit and phased plan (Phase 1: audit only)

- **Date:** 2026-10-14
- **Brief:** `docs/production-readiness/agents/05_TIKTOK_PARITY_AGENT.md` (reference: section 3, the founder's TikTok web walk of 14 Oct 2026). Rules: `00_COMMON_RULES.md`.
- **Code audited:**
  - `rvreddy476/modernsmapp` @ `codex/module-01-02-launch-safety` `82cd1a16` (backend, Android)
  - `rvreddy476/postbook-ui` @ `codex/apps-ui-refresh` `eb10bd3` (web)
  - The admin console (`rvreddy476/atpost-web`) is not in this session's repository scope and was not audited.
- **Not repeated here:** the reels player, upload pipeline, engagement counts, moderation and scaling. The branch `audit/reels-production-readiness` does not exist (`git ls-remote` found nothing), so those topics are "covered by the Reels audit".
- **Method:** targeted reads and searches, plus every safe check in `TIKTOK_PARITY_AUDIT_LOG.md`. No application code was changed. Functions are compared, not looks; no TikTok names, colours, icons or wording are proposed.
- **Labels:** **Verified defect** = seen in code (the P0s in §4 were re-checked by hand); **Risk** = plausible, needs runtime proof; **Unverified** = could not be checked here.

Paths: `W/` = `postbook-ui/src`, `A/` = `mobile/android`, `S/` = `Architecture/services`, `I/` = `identity-platform/services`, `C/` = `chat-service/services`, `GW` = `S/api-gateway/cmd/server/main.go` (prefix routing; a prefix matches only the exact path or `prefix/…`, GW:359).

---

## 1. Executive summary

**Overall parity: Medium-Low.** The backend already has most of the TikTok functions: privacy and comment/message permissions are enforced server-side, sounds and "use this sound" work end to end, LIVE v2 has chat moderation and RTMP ingress, and the PostTube Creator Hub is broad. The gap is mostly in the **clients**. Several web flows call retired or non-existent routes, so they fail silently, and Android lacks whole surfaces (creator studio, comment actions, LIVE extras).

| Area | Parity | One-line reason |
|---|---|---|
| 1 Navigation and shell | Medium | The reels sidebar mirrors the function list; Help/About links 404; no language; Android theme is system-only |
| 2 Profile | **Low** | Web follow, block and follower lists call retired 410 routes; no counts on web; no Liked tab, sort or share on other profiles |
| 3 Explore and discovery | **Low** | No short-video category chips or tile grid; empty Following/Friends show no suggested creators |
| 4 Search | **Low** | No Videos/LIVE/Sounds/Photo tabs on web; server history is never written |
| 5 Hashtag and sound pages | Medium | Sound page and "use this sound" are complete; hashtag is a list, with no count, share or report |
| 6 Video page details | Medium | Web reels have speed, quality, captions, not-interested, report; share links break signed-out; embed broken |
| 7 Comments | Medium (web) / Low (Android) | Web has replies, likes, emoji, report, delete; mentions are never notified; Android is read-and-post only |
| 8 Activity | **Low** | Web tabs filter nothing; web preference toggles are ignored; no web push |
| 9 Messages | Medium | Inbox, requests and who-can-message enforcement exist; no "send to friends" |
| 10 LIVE | Medium (web) / Low (Android) | Web discover, room, hearts, RTMP; no join notices or word-filter UI; Android is a thin client |
| 11 Upload | Medium | Entry points, schedule, privacy, cover all work; web reel cap is 180 s against the 300 s rule |
| 12 Creator studio | Medium (web) / Missing (Android) | Hub covers library, insights, comments inbox; no CSV, inspiration, sound library; Android has none |
| 13 Settings and privacy | **Low** | Server enforcement is good; six web settings pages are broken or save nothing |
| 14 Trust and safety | **Low** | Web post report and profile report do nothing; hashtag/sound cannot be reported |
| 15 Shop entry | Medium | MStore exists; it is not in the reels menu or the Android bar |

**The 10 biggest gaps**
1. **Web follow, unfollow, block and followers/following lists call retired profile-service routes that return 410** (`W/hooks/useEditProfile.ts:175,189`, `W/hooks/useBlocking.ts:32,55,78`, `W/hooks/useConnections.ts:160-250` → `I/profile-service/internal/http/handler.go:157-193 retiredGraphRoute`). Follow from the reels screen is affected too (`W/features/reels/components/ReelsScreen.tsx:214-228`). P0.
2. **Reporting is broken or missing on several surfaces.** Web post report calls `POST /v1/posts/:id/report`, which no service serves (`W/components/PostCard.tsx:126-133`). There is no profile report on web or Android. trust-safety accepts only user/post/comment (`S/trust-safety-service/internal/service/moderation.go:20`), so hashtags and sounds cannot be reported. P0.
3. **Shared links fail.** `/reels/<id>` is not public (`W/middleware.ts` PUBLIC lists), so signed-out visitors land on sign-in. Android "Copy link" builds `https://momentum.app/p/<id>`, and no `/p` route exists (`A/core/ui/.../UsPostMoreState.kt:519`). Android Share sends text without a URL (`A/core/ui/.../SharePost.kt`). P0.
4. **Web settings save nothing or the wrong thing:**
   - `settings/social-preferences/page.tsx:124-157` never loads stored values and saves defaults, so a private account can be silently made public.
   - Delete account is broken on both web paths (`settings/data/page.tsx:79` DELETE `/v1/users/me` has no route; `W/hooks/useSecurity.ts:330` omits the required password, `I/auth-service/internal/http/account_control.go:43`).
   - Verification calls `/v1/trust/verification`, which is not routed (GW has only `/v1/verification-requests`, :506).
   - Wellbeing sends `daily_limit_minutes` / `bedtime_mode_*` where the server expects `daily_limit_mins` / `bedtime_start` (`S/user-service/internal/http/wellbeing_validate.go:18-20`).

   P0.
5. **Web notifications:** category toggles write `muted_types`, which delivery never reads (`S/notification-service/internal/store/postgres/preferences.go:35`; enforcement uses `push_*`/`inapp_*` in `service/preferences.go`), and the inbox tabs filter nothing because `categoryTypePrefix` honours only `qa` (`service/notification.go:544`). P0/P1.
6. **Comment @mentions are rendered but never stored or notified** (`S/post-service/internal/service/post.go:2587 CreateCommentPG`, `CreateReply` call no mention code). Android comments have no reply, like, report, delete or creator pin. P1.
7. **Explore has no short-video categories, no tile grid and no suggested creators** on empty Following/Friends; the suggestion APIs exist but are unused (`S/suggestion-service` `/v1/suggestions/interstitial`, web `useFollowSuggestions` has no consumers). P1.
8. **Profile parity:** no follower/following counts on web; Android counts read `profile_stats`, which nothing updates any more (Risk, `I/profile-service/internal/store/profiles.go:1243-1258`); no likes-received total, no Liked tab, no Latest/Popular/Oldest sort (`S/post-service/internal/store/postgres/posts.go:1015` hard-codes the order). P1.
9. **Search:** web has no Videos/LIVE/Sounds/Photo tabs although the backend supports `type=videos|flicks` and `/v1/audio/search`; server history is never recorded (`RecordSearchHistory` has no caller); per-item delete clears everything. P1.
10. **Web reel limit is 180 s, against the founder's 300 s rule** (`W/features/upload/tokens.ts:72-73`, `useUploadStudio.ts:74-83`, vs `Architecture/shared/postclassify/classify.go:20`). A 181–300 s portrait clip is refused or becomes a long video. P1.

**What Momentum does better or deliberately differently**
- Privacy, comment and message permissions come from one graph permission engine (`S/graph-service/internal/permission/permission.go`), with more message levels (no_one, connections, mutual, followers, everyone, paused) than the reference.
- Reels open muted and keep sound on; Android double tap opens full mode; Reels never enter the home feed. These are founder decisions and are kept.
- "Keep a copy" is an in-app offline copy only. The reference offers no equivalent, and viewers never get a file.
- PostTube follows RUTUBE layouts and words; the Creator Hub already covers long video, shorts, LIVE, captions and branding in one place.
- LIVE has creator moderation (moderators, bans, word filters, mute, pin) on the server, and the reference's gifts and coins are deliberately out of scope.
- Comment, message and LIVE moderation are richer on the server than the clients show.

---

## 2. Feature matrix

Status values: Complete · Partial · Missing · Broken · Unverified · **OOS** (out of scope by founder decision). "Web" and "Android" name the screen/component; "Backend" names the route and handler.

### 2.1 Navigation and shell
| Item | TikTok behaviour (§3) | Momentum web | Momentum Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Main menu items and order | Search · For You · Shop · Explore · Following · Friends · LIVE · Messages · Activity · Upload · Profile · More | Reels sidebar: For You · Explore (app launcher) · Following · Friends · LIVE · Messages · Activity · Upload · Profile · More | Bar: Home · Reels · Create · Explore · Me; Messages and bell in the Home header | n/a | Partial | `W/features/video-shell/nav.ts:130-151`; `A/app/.../navigation/TabResolver.kt:32-37`; `A/feature/feed/ui/MomentumHeader.kt:35-62` |
| Search box in menu | Top of sidebar | `SidebarSearch` exists but is never rendered; search is top-right | Header icon | `/v1/search` | Partial | `W/features/video-shell/VideoSidebar.tsx:236`; `VideoTopCluster.tsx:14` |
| Following-accounts list | Under the menu when signed in | Forced empty in sidebar chrome | none | `/v1/graph/following/:id` | Missing | `VideoSidebar.tsx:63` |
| Activity unread badge | Badge on Activity | Not drawn in the reels sidebar (main Header has it) | Bell count | `GET /v1/notifications/unread-count` | Partial | `VideoSidebar.tsx:70-117`; `W/components/Header.tsx:611` |
| More: General settings | Settings group | `/settings` | Settings in More sheets | n/a | Complete | `nav.ts:244`; `A/feature/feed/ui/HomeMoreSheet.kt:50-54` |
| More: Dark mode (3-way) | Dark/light/system | Auto/Dark/Light | System only | client | Partial | `W/features/video-shell/VideoMorePanel.tsx:121`, `themeChoice.ts:77`; `A/core/designsystem/.../theme/Theme.kt:123` |
| More: Language | Language selector | none; `<html lang="en">` hard-coded | no `values-<locale>` | none | Missing | searched i18n libraries and locale folders |
| More: Tools | Studio, effects, promote, LIVE tools, sell | Upload, Your channel, LIVE tools | none | n/a | Partial | `nav.ts:252-254` |
| More: Help Center | Link | Links to `/help`, `/about`; no such routes → 404 | none | n/a | **Broken** | `nav.ts:159-160,272`; `ls W/app` (no help/about) |
| More: Log out | Log out | Works | Settings → Account | `POST /v1/auth/logout` | Complete | `VideoMorePanel.tsx:47` |
| Get App | Top-right | none | n/a | n/a | Missing | — |
| Get Coins | Top-right | none | none | none | OOS | founder decision |

### 2.2 Profile
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Own profile: Edit, settings, share | Edit, Promote, settings, share | Edit → `/settings/profile`; QR modal with copy link (QR is a decorative grid) | Edit, settings icon | `PUT /v1/profiles/me` | Partial | `W/components/profile/ProfilePage.tsx:56-132,614`; `A/feature/profile/ui/ProfileScreen.kt:296-317` |
| Promote post | Owner action | none | none | none | Missing | searched promot/boost |
| Follow on another profile | Follow | Calls retired `/v1/profiles/:u/follow` → **410** | `POST /v1/graph/follow` | graph `h.Follow` | **Broken (web)** | `W/hooks/useEditProfile.ts:175,189`; `I/profile-service/internal/http/handler.go:189-190` |
| Message button | Opens chat | Creates a DM but never opens it; permission label calls unrouted `/v1/permissions` | Opens the thread | `/v1/chat` | Partial | `W/components/connections/MessageButton.tsx:67-112`; `A/feature/profile/.../StartChatViewModel.kt:64-88` |
| Share another profile | Share | none (copy link only in orphan `ProfileActions.tsx`) | none | n/a | Missing | `W/components/profile/ProfileActions.tsx:229-233` (no importer) |
| Block | Actions → Block | Props passed but never rendered; hook hits retired 410 route | `POST /v1/graph/block` | graph `h.Block` | **Broken (web)** | `ProfileHeader.tsx:109-112`; `W/hooks/useBlocking.ts:32,55` |
| Report profile | Actions → Report | none (orphan menu item only closes) | none | `POST /v1/reports {user}` | Missing | `ProfileActions.tsx:223-227` |
| Suggested accounts | On another profile | none | none | `GET /v1/suggestions/interstitial` (no client) | Missing | suggestion-service `handler.go:49,161` |
| Counts: Following/Followers | Shown | Not shown | Shown from `profile_stats` (Risk: never updated) | `GET /v1/graph/counts/:id` | Partial | `ProfileHeader.tsx:558-596`; `ProfileScreen.kt:283-289`; `I/profile-service/internal/store/profiles.go:1243-1258` |
| Likes received total | Shown | none | none | analytics creator/me only (self) | Missing | — |
| Tabs: Videos | Videos | Videos, Reels tabs | Posts, Reels, Videos | `GET /v1/posts/by-author/:id` | Complete | `W/components/profile/ProfileTabs.tsx:41-117`; `ProfileGridViewModel.kt:39-43` |
| Tabs: Favorites (saved) | Favorites | "Stash" (own only) | none | `GET /v1/posts/bookmarks` | Partial | `StashedTab.tsx:25` |
| Tabs: Liked | Liked | Separate page `/reels/liked`, not a tab | none | `GET /v1/reels/liked` | Partial | `W/features/reels/liked.ts` |
| Liked/Favorites privacy | Owner-controlled | Always private (owner only) | n/a | caller-only routes | Partial | `S/post-service/internal/http/reel_engagement.go:387,412` |
| Sort Latest/Popular/Oldest | Sort control | none | none | fixed `ORDER BY is_pinned DESC, created_at DESC` | Missing | `S/post-service/internal/store/postgres/posts.go:1015` |
| Edit profile fields | Name, bio, photo, links | Many fields; the username box is dropped by the server | Same minus username | `PUT /v1/profiles/me` (no username field); `PUT /v1/profiles/me/handle` | Partial | `I/profile-service/internal/http/handler.go:412-421,451`; `W/.../BasicInfoSection.tsx:91` |
| Private profile for non-follower | Private notice | Empty grid; `PrivateProfileView` never used; viewer header not forwarded | `PrivatePlaceholder` | posts enforced: `canViewAuthor` (`S/post-service/internal/service/privacy_gate.go:255`) | Partial (web) | `W/app/api/profile/[username]/route.ts:47-51` |
| Followers/Following lists | Lists | Retired 410 routes | Works | `GET /v1/graph/followers/:id` | **Broken (web)** | `W/hooks/useConnections.ts:160-250` |

### 2.3 Explore and discovery
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Category chips + source | ~20 chips | Long video only (`/posttube/topics`); `/discover` has none | Tube chips only | Hard-coded 18 categories, `GET /v1/posts/categories` | Partial | `S/post-service/internal/service/categories.go:51-78`; `W/features/posttube/hooks/usePosttubeHome.ts:15` |
| Category filter on the short-video feed | Chip filters the grid | none | none | `GetReelFeed` has no category | Missing | `S/feed-service/internal/http/handler.go:66` |
| Tile grid with likes/creator | Grid | `/discover` is a PostCard list | none | `/v1/discover/suggested` | Partial | `W/app/discover/page.tsx` |
| Following page empty state | Suggested creators with preview + Follow | Text + "Go to For You" | Text only | `/v1/suggestions?type=follow` (unused) | Partial | `ReelsScreen.tsx:673-686`; `A/feature/feed/ui/FeedScreen.kt:148-151` |
| Friends page | Friends videos + suggestions | `/connections` management; no Friends video feed | Friends feed, text empty state | `/v1/feed/home?circle_only=true` | Partial | `W/hooks/useFeedPosts.ts:81`; `A/feature/feed/ui/FriendsFeedScreen.kt` |
| Suggested accounts | In several places | Home right panel, PYMK strip, connections | Chat home only | `/v1/suggestions/people` | Partial | `W/components/RightPanel.tsx:56` |

### 2.4 Search
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Tabs Top/Users/Videos/LIVE/Photo | 5 tabs | All, People, Posts, Hashtags, Channels, Products, Events, Messages; reels search separate | Users, Posts, Reels, Videos, Channels | `GET /v1/search` (`type=videos|flicks` supported) | Partial | `W/app/search/page.tsx:81-92`; `A/feature/search/navigation/SearchNavigation.kt:17-35`; `S/search-service/internal/http/handler.go:278,425-451` |
| LIVE in results | Top mixes LIVE row | none | none | none in search-service | Missing | — |
| Photo tab | Photos | none | none | none | Missing | `handler.go:226-229` |
| Sounds in search | (sound results) | none | none | `GET /v1/audio/search` exists | Missing | `S/media-service/internal/http/audio_handler.go:23` |
| Autocomplete | Suggestions | `/v1/search/autocomplete` | none | handler.go:562 | Partial | `W/hooks/useSearch.ts:184` |
| "Others searched for" | Right rail | none | none | queries logged, no read | Missing | `S/search-service/internal/store/postgres/analytics.go:28` |
| Search history | Stored, clearable | Recent always empty; per-item delete clears all; every 3+ char query auto-saved as "saved search" | Local only (max 10), clear-all | `RecordSearchHistory` has no caller | **Broken (web)** | `S/search-service/internal/store/postgres/search_extras.go:114`; `W/app/search/page.tsx:243-245,826` |
| Signed-out search | Works | `/search` gated | n/a | anonymous allowed | Partial | `W/middleware.ts` |
| Find related content | Search bar on video page | Empty box, not pre-filled | none | `/v1/feed/videos/:id/related` | Partial | `W/features/reels/components/ReelGapSearch.tsx:17` |

### 2.5 Hashtag and sound pages
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Hashtag page | Avatar, title, grid | `/hashtag/[tag]` list with tabs and live "new posts" | `HashtagPostsScreen` list | `GET /v1/hashtags/:tag/posts` | Partial | `W/app/hashtag/[tag]/page.tsx:44`; `A/feature/feed/ui/HashtagPostsScreen.kt:19` |
| Hashtag video count | "N videos" | Client "N loaded" | none | no total | Missing | `S/post-service/internal/http/handler.go:258` |
| Hashtag share / report | Share, Report | none | none | trust-safety rejects entity | Missing | `moderation.go:20` |
| Sound page | Play, title, creator, N videos, grid | Complete | Complete (artist not a link) | `GET /v1/posts/by-sound/:id`, `GET /v1/audio/:id` | Complete (signed-in) | `W/features/reels/.../SoundReelsScreen.tsx:27`; `A/feature/feed/ui/reels/sound/SoundPageScreen.kt:191-301` |
| Use this sound | Start a video with it | `/reels/create?sound=` | Opens Create (Reel) | `POST /v1/posts/:id/sound` | Complete | `A/.../SoundPageViewModel.kt:162` |
| Sound share / report / favourite | Share, actions | none | none | no sound report type | Missing | — |
| Signed-out hashtag/sound | Public | gated | n/a | anonymous allowed | Partial | `W/middleware.ts` |

### 2.6 Video page details
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Speed 0.75–2.0 | Menu | Reels slider 0.25–2; Tube | Tube only (0.5–2) | client | Partial | `W/features/reels/playback/playerPrefs.ts:18-20`; `A/feature/tube/ui/watch/WatchSettingsSheet.kt:39-61` |
| Quality Auto | Menu | Yes (when renditions > 1) | Yes | client | Complete | `W/features/video-shell/moreRows.ts:127`; `A/core/ui/.../UsPostMoreState.kt:240` |
| Auto scroll | Toggle | Next/loop at end | none | client | Partial | `moreRows.ts:131` |
| Floating / mini player | Menu | Tube only | none | client | Partial | `W/features/posttube/watch/miniPlayer.tsx:175` |
| Captions | Menu | Reels (when a track exists) | Tube only | `/v1/subtitles` | Partial | `moreRows.ts:121` |
| Not interested | Menu | Reels, PostCard, Tube | All surfaces | `POST /v1/feed/feedback` | Complete | `S/feed-service/internal/http/handler.go:80` |
| Report | Menu | Reels, Tube work; **PostCard broken** | Works | `POST /v1/reports` → trust-safety | Partial (web PostCard Broken) | `W/components/PostCard.tsx:126-133` |
| Share: Copy link | Copy | Reels `/reels/<id>` (gated signed-out) | `momentum.app/p/<id>` (no route) | — | **Broken (Android)** | `UsPostMoreState.kt:517-519` |
| Share: Repost | Repost | PostCard/Tube yes; reels share sheet no | none | `POST /v1/posts/:id/repost` | Partial | `W/components/ShareDialog.tsx:49-61` |
| Share: Embed | Embed code | Copies iframe to `/post/<id>/embed`, which does not exist | none | no oEmbed | **Broken** | `PostCard.tsx:192-198` |
| Share: Send to friends | Into chat | none | none | chat has no post message type | Missing | `C/message-service/internal/http/handler.go:659` |
| Share: external networks | Many networks | Reels: WhatsApp, Telegram, X, Facebook, Email, native | System chooser, text only | client | Partial | `W/features/reels/components/ShareSheet.tsx:52-58`; `A/core/ui/.../SharePost.kt:46-53` |
| Creator videos tab | Tab | none | none | `GET /v1/posts/by-author/:id` | Missing | — |
| Signed-out deep link | Opens video | `/post/<id>` and `/posttube/watch` public; `/reels/<id>` → sign-in | No post App Link | `GET /v1/posts/:id`, `viewerMayViewPost` | Partial | `W/middleware.ts`; `A/app/src/main/AndroidManifest.xml:73-79`; `S/post-service/internal/service/post.go:2936` |
| Private/unlisted refusal | Refused | 404 "not found" | — | `viewerMayViewPost`, fails closed | Complete | `post.go:2936` |

### 2.7 Comments
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Replies | Reply | Two levels | none (stale comment says owner-only) | `POST /v1/comments/:id/reply` | Partial | `W/components/CommentSection.tsx:363`; `A/core/ui/.../UsCommentsSheet.kt:290-297` |
| Comment likes | Like count | Yes | none | `PUT /v1/comments/:id/reaction` | Partial | `W/components/comments/CommentReactions.tsx:103` |
| @mention autocomplete | @ to tag | Yes | none | `/v1/search/users` | Partial | `W/components/comments/MentionInput.tsx` |
| Mentions notified | Mentioned user notified | — | — | Not stored or published from comments | **Missing** | `S/post-service/internal/service/post.go:2587` (no mention code) |
| Emoji | Emoji | Picker | Quick reactions | — | Complete | `EmojiPickerPopover.tsx` |
| Stickers/images | Supported | none | none | text only | Missing | `Comment.Body` only |
| Report comment | … → Report | Yes | none | `POST /v1/reports {comment}` | Partial | `CommentSection.tsx:94,340` |
| Delete own | Delete | Yes (+ edit) | none | `DELETE /v1/comments/:id` | Partial | `CommentSection.tsx:355` |
| Creator pin and heart | Pin, heart | Tube only (`creatorTools`) | none | `PUT /v1/comments/:id/pin`, heart routes | Partial | `W/features/posttube/components/WatchPage.tsx:548`; `S/post-service/internal/service/comment_creator.go:59` |
| Keyword filter (creator) | Filter comments | none | none | none | Missing | — |
| Keyword filter (viewer) | Filter keywords | no UI | Settings → content prefs | trust-safety keyword-filters; applied to feeds only | Partial | `S/feed-service/internal/service/keywordfilter.go:106` |
| "Comments filtered" notice | Notice | none | none | none | Missing | — |

### 2.8 Activity / notifications
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Filter tabs | All, Likes, Comments, Mentions, Followers | Tabs exist but only `qa` filters | none (time bands) | `categoryTypePrefix` honours `qa` only | **Broken (web)** | `W/app/notifications/page.tsx:22`; `S/notification-service/internal/service/notification.go:544` |
| Unread badge | Badge | Header | Bell | `GET /v1/notifications/unread-count` | Complete | `W/components/Header.tsx:611` |
| Web push / desktop | Allow in browser | none (no push handler in SW) | FCM | `POST /v1/notifications/devices` accepts `web` | Missing (web) | `W/../public/offline-sw.js`; `S/notification-service/internal/push/dispatcher.go:38` |
| Per-category preferences | Likes, comments, followers, mentions | Writes `muted_types`, never read | `/preferences/detailed` | enforced via `push_*`/`inapp_*` | **Broken (web)** | `S/notification-service/internal/service/preferences.go:33,270,323` |

### 2.9 Messages
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Inbox + conversation | List + pane | `/messenger` | Chat inbox/thread | `/v1/chat/conversations` | Complete | `W/components/messenger/PostbookMessenger.tsx` |
| Message requests | Requests | Accept/decline (no block/report) | Accept, decline, block, report | `/v1/chat/requests/*` | Complete | `C/message-service/internal/http/handler.go:206-210` |
| Who can message | Setting, enforced | Social prefs page (saves defaults — see 2.13) | Direct messages screen | `checkMessagePermission` → graph `resolveMessage` | Complete (server) | `C/message-service/internal/service/message.go:603`; `permission.go:162` |
| Send to friends | Share into chat | none | none | no post message type | Missing | `handler.go:659 Type oneof=text media` |

### 2.10 LIVE
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Discover: live now | Grid | `/live`, PostTube Live, Reels Live | Flat list | `GET /v1/livestream/streams` | Complete (basic) | `S/live-service-v2/internal/http/handler.go:262` |
| For you / Following | Chips | Reels Live, PostTube Live | none | `following=true` | Partial | `W/features/reels/live/liveStage.ts:39-50`; `A/feature/live/.../LiveApi.kt:48` |
| Categories + rows | Chips, rows | PostTube Live rows | none | `GET /v1/livestream/categories/live` | Partial | `W/features/posttube/discovery/components/LivePage.tsx:233-265` |
| Hero preview | Hero | PostTube Live | none | — | Partial | `LivePage.tsx:280` |
| Suggested live creators | With viewers | Reels Live side list | none | `GET /v1/livestream/creators/live` | Partial | `LiveStageViews.tsx:244` |
| Room header: Follow, Share | Yes | Reels Live yes; `/live/[streamId]` no Follow/Share | Report only | — | Partial | `W/app/live/[streamId]/page.tsx:139-157` |
| Chat | Yes | Yes | Yes | `POST/GET /streams/:id/chat` | Complete | `handler.go:96-97` |
| Viewer list (top 3) | Viewers | Supporters ranking, not present viewers | none | presence only feeds a count | Partial | `S/live-service-v2/internal/service/lifecycle.go:325` |
| Join notices | "<user> joined" | none | none | not published | Missing | `lifecycle.go:325-327` |
| Comment filtering | "comments filtered" | no UI | none | word filters enforced (`ErrChatBlockedWord`) | Partial | `S/live-service-v2/internal/service/live.go:1017-1022` |
| Hearts | Like hearts | Yes | none | `POST /streams/:id/hearts` | Partial | `W/features/live/components/LiveHearts.tsx:30` |
| Report | Report | Yes | Yes | `POST /streams/:id/reports` (live-v2 queue, not trust-safety) | Complete | `moderation_routes.go:158` |
| Go LIVE (browser) | Studio app | Browser WebRTC | Title only | `POST /streams`, start, end | Complete (web) / Partial (Android) | `W/app/live/[streamId]/broadcast/page.tsx:39`; `LiveScreens.kt:261` |
| Go LIVE (streaming software) | Studio app | RTMP key | none | `/streams/:id/ingress` | Complete (web) | `S/live-service-v2/.../livekit/ingress.go:108` |
| LIVE creator centre | LIVE Center | Hub Live page (upcoming, past, recordings, peak) | none | `/users/:id/streams` | Partial | `W/features/posttube/hub/components/HubLivePage.tsx:398` |
| Gifts, coins, recharge, guests | Present | — | — | v1 retired | OOS | founder decision |

### 2.11 Upload (entry points and limits only)
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Entry points | Upload page | `/reels/create`, `/posttube/upload`, `/create/post` | Create hub | `/v1/media/init`, resumable | Complete | `W/features/upload/UploadStudio.tsx:32`; `A/feature/post/.../createhub/CreateHubScreen.kt` |
| Size | 30 GB | 500 MB client cap | 500 MB | 500 MB single-shot, 2 GB resumable | Partial | `W/features/upload/fileRules.ts:12-13`; `S/media-service/internal/service/validation.go:9` |
| Duration (reel) | 60 min | **180 s** | 300 s | classify 300 s, not rejected | **Broken (web vs rule)** | `tokens.ts:72-73`; `postclassify/classify.go:20` |
| Formats | mp4 | mp4/webm/mov | picker | mp4/webm/mov | Complete | `media.go:249` |
| Resolutions | up to 4K | no check | no check | ladder tops at 720p (Reels audit) | Partial | `processing/video.go:353-375` |
| Aspect | 16:9 or 9:16 | landscape relabelled long | not enforced | classification only | Partial | `classify.go:50` |
| Photos | Photos tab | 4 images, feed post | 10 images | `/v1/posts` | Partial | `W/app/create/post/page.tsx:277-281`; `CreateHubScreen.kt:467` |
| Drafts | Drafts | Server drafts, no list | one local draft | `/v1/reels/drafts` list unused | Partial | `S/post-service/internal/http/drafts.go:17-24` |
| Schedule | Schedule | Yes | Yes | `publish_at` 5 min–30 days | Complete | `S/post-service/internal/http/handler.go:477-480` |
| Privacy / comments toggle | Yes | Yes | Yes | create fields | Complete | `studioApi.ts:20` |
| Duet/stitch-like toggles | Separate | One remix toggle | One remix toggle | `remix_setting` | Partial | `handler.go:450` |
| Cover | Choose | Yes | Yes | extract-frame | Complete | `CoverPicker.kt:75` |

### 2.12 Creator studio
| Item | TikTok | Web (Creator Hub) | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Home metrics + chart | 7-day metrics, chart | 28-day totals; likes/comments/shares dropped; daily chart reads a field the server does not send | none | `GET /v1/analytics/creator/me` (`daily_stats`) | Partial | `W/features/posttube/hub/hubApi.ts:1205 normalizeCreatorInsights`; `S/analytics-service/internal/service/creator.go:124-184` |
| Recent posts / latest comments | Cards | Latest video only; comments on Conversations page | none | `/v1/uploads/*`, `/v1/comments/inbox` | Partial | `OverviewDashboard.tsx:63-77` |
| Posts table | Privacy, views, likes, comments, actions | Library table (no Likes column), bulk edit; scheduled list | Scheduled only | `/v1/uploads/*` | Partial | `LibraryTable.tsx:88-93` |
| Analytics tabs | Overview, Content, Viewers, Followers | Channel + per-content; no Viewers/Followers | none | retention/demographics routes unused | Partial | `S/analytics-service/.../dashboard.go:40-41` |
| Period | 7 days etc. | 7/28/90/365 | — | 7/28/30/90/365 | Complete | `InsightsPage.tsx:17-36` |
| Traffic source | Yes | Per content | — | content insights | Complete | `InsightsPage.tsx:183-211` |
| Search queries | Yes | none | — | none | Missing | — |
| Download data | CSV | none | — | none | Missing | — |
| Comments inbox + filters | Filters | Status, type, sort, per post | none | `/v1/comments/inbox` | Partial | `ConversationsPage.tsx:45` |
| Inspiration | Trending posts/creators | none | none | none | Missing | — |
| Monetisation programmes | Programmes page | `/monetization` pages; payout form shows no gating copy | none | monetization-service | OOS (payouts off) — Risk noted | `W/app/monetization/*` |
| Sound library | Browse, filters | none (only `?sound=`) | none | `/v1/audio/trending`, `/search`; `/v1/audio-library` **not routed** | Partial / Broken | `S/media-service/internal/http/audio_handler.go:34-37`; GW:445 |
| Feedback | Link | none | none | `/v1/feedback` (post-service) | Missing (UI) | GW:439 |
| Same numbers as public counts | — | Three sources (post counters, analytics hourly, dashboard) | — | — | Partial (Risk) | `S/analytics-service/.../aggregates.go:263` |

### 2.13 Settings and privacy
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Account region | Setting | none | Manage account | `PUT /v1/users/me/region` | Partial | `A/feature/settings/.../ManageAccountScreen.kt:90` |
| Download your data | Request | Yes (account rows only) | none | `GET /v1/auth/data-export` | Partial | `I/auth-service/internal/service/auth.go:1091` |
| Deactivate | Yes | none | Yes | `POST /v1/auth/account/deactivate` | Partial | `account_control.go:72` |
| Delete account | Yes | **Both paths broken** | Yes | `DELETE /v1/auth/account {password}` | **Broken (web)** | `W/app/settings/data/page.tsx:79`; `W/hooks/useSecurity.ts:330` |
| Private account | Toggle, enforced | Privacy page works; social-preferences page overwrites it with defaults | Yes | enforced by graph `resolveViewPosts` | **Broken (web)** | `W/app/settings/social-preferences/page.tsx:124-157`; `permission.go:279` |
| Other privacy controls | — | Privacy page toasts "Saved" for controls it never sends | — | — | **Broken (web)** | `W/app/settings/privacy/page.tsx:173-176` |
| Who can comment | Setting, enforced | none | Yes | `resolveComment` via `canComment` | Partial | `privacy_gate.go:261` |
| Who can message | Setting, enforced | 3 of 6 values | Yes | `resolveMessage` (thread creation only) | Partial | `message.go:440,989` |
| Push (browser) | Allow | none | FCM | ready | Missing (web) | §2.8 |
| Push per-category | Toggles | Ignored (`muted_types`) | Works | enforced | **Broken (web)** | §2.8 |
| Business verification | Apply | Calls unrouted `/v1/trust/verification` | none | `/v1/verification-requests` | **Broken (web)** | `W/app/settings/verification/page.tsx:121,193`; GW:506 |
| Ads settings | Several | none | none | none (no ads product) | Missing (no ads product) | searched ads/advertis |
| Screen time / sleep | Limits, reminders | Field names mismatch → values dropped | Works (client nudge) | `/v1/users/me/wellbeing` | **Broken (web)** | `W/app/settings/wellbeing/page.tsx:196-198,341-343`; `wellbeing_validate.go:18-20` |
| Weekly summary | Weekly updates | none | week view | — | Partial | `A/.../ScreenTimeScreen.kt:121-135` |
| Filter keywords | Setting | none | Yes (feeds) | trust-safety, enforced in feed hydration | Partial | `S/feed-service/internal/service/hydration.go:353,445` |
| Family pairing / teen | Pairing | none | none | `/v1/teen-accounts` exists, **not routed**, no client | Missing | `S/trust-safety-service/internal/http/handler.go:90-94` |
| Age gating | — | registration 18+ | registration | `CheckRegistrationEligibility` | Complete (18+) | `I/auth-service/internal/service/eligibility.go:114` |
| Increase contrast | Toggle | none | none | — | Missing | — |
| Captions default | — | per-device player pref | Tube only | — | Partial | `playerPrefs.ts:53-58` |

### 2.14 Trust and safety surfaces
| Surface | Web | Android | Endpoint / routed? | Status | Evidence |
|---|---|---|---|---|---|
| Video (reel, tube) | Works | Works | `POST /v1/reports` → trust-safety `FileReport` | Complete | `S/trust-safety-service/internal/http/handler.go:57-63,140` |
| Post (feed card) | `prompt()` + `/v1/posts/:id/report` (no route) | Works | 404 | **Broken (web)** | `W/components/PostCard.tsx:126-133` |
| Comment | Works | none | `/v1/reports {comment}` | Partial | §2.7 |
| Profile | none (channel report works on Tube) | none | `/v1/reports {user}` | Missing | `W/features/posttube/channel/channelApi.ts:327` |
| Hashtag | none | none | entity type rejected | Missing | `moderation.go:20` |
| Sound | none | none | entity type rejected | Missing | `moderation.go:20` |
| LIVE | Works | Works | live-v2 own queue | Complete (separate queue) | `moderation_routes.go:158` |
| Comment report → auto-hold | — | — | Threshold hold only runs on post-service `/v1/reports`, which the gateway never reaches | Risk (dead code) | `S/post-service/internal/store/postgres/comments.go:722` |
| Not interested | Works | Works | `/v1/feed/feedback` | Complete | — |
| Age-restricted content | Tube edit + playback | none | `checkAgeGate` enforced | Partial | `S/post-service/internal/service/age_gate.go` |
| Sensitive labels | — | — | `/v1/media-labels` not routed | Missing | `trust-safety handler.go:98` |

### 2.15 Shop and coins
| Item | TikTok | Web | Android | Backend | Status | Evidence |
|---|---|---|---|---|---|---|
| Shop entry | Menu item | Main sidebar More flyout + launcher tile; not in reels menu | Launcher tile only | `/v1/commerce` | Partial | `W/components/Sidebar.tsx:94`; `A/app/.../explore/LauncherTiles.kt:39` |
| Coins | Get Coins | none | none | none | OOS | founder decision |

---

## 3. Code comparison (Partial and Broken rows: what exists, where it stops, smallest change)

Reuse first; nothing below rewrites working code.

1. **Web social graph calls.** `useEditProfile.ts` (follow), `useBlocking.ts`, `useConnections.ts`, `app/api/profile/[username]/route.ts` still use `/v1/profiles/...` graph paths that profile-service now answers with 410. The canonical routes are on graph-service (`S/graph-service/internal/http/handler.go:62-111`) and Android already uses them (`A/core/profile/.../ProfileApi.kt:87-141`; web `W/features/reels/data/reelFeedApi.ts:98` uses `/v1/graph/block`). **Change:** swap paths and bodies (`{user_id}`) in those four files; forward `Authorization`/`X-User-Id` in the profile proxy so block denial and privacy apply.
2. **Web profile header.** `ProfileHeader.tsx` receives `onBlock/onMute/onUnblock` (:109-112) but renders no menu; `ProfileActions.tsx` has the menu but is imported nowhere and its Report item only closes. **Change:** mount a fixed `ProfileActions` (Share via `ShareDialog`, Block via the fixed hook, Report via `ReportChannelDialog` with `entity_type:"user"`), render `follower_count`/`following_count` from `/v1/graph/counts/:id`, render `PrivateProfileView` when private and not following.
3. **Web reporting.** `PostCard.handleReport` → replace with `useSubmitReport({targetType:'post'})` and the existing `ReelReportDialog`/`ReportDialog`. Remove the Embed item until an embed page exists, or add `W/app/post/[postId]/embed/page.tsx` (already under the public `/post/` prefix).
4. **Share links.** Make `/post/<id>` the one permalink on both clients: `UsPostMoreState.kt:519` → `/post/`, add the URL to `EXTRA_TEXT` in `SharePost.kt`, add an App Link for `/post/` in `AndroidManifest.xml`; on web either add `/reels/` to `PUBLIC_PREFIXES` (the stage must tolerate no session) or have reel share use `/post/<id>`.
5. **Web settings.**
   - `social-preferences/page.tsx`: load `GET /v1/users/me/settings` before rendering, and send only changed fields.
   - `privacy/page.tsx`: remove or wire the unsent controls, and add `allow_comments_from`.
   - Delete account: one path, `DELETE /v1/auth/account {password}`; delete the `/v1/users/me` call.
   - Verification: use the existing `useTrust.ts` hooks against `/v1/verification-requests`.
   - Wellbeing: rename the fields to `daily_limit_mins`, `bedtime_start`, `bedtime_end`.
   - Notifications: switch the page to `/v1/notifications/preferences/detailed` (`push_*`, `inapp_*`) as Android does.
6. **Notification tabs.** Extend `categoryTypePrefix` (`S/notification-service/internal/service/notification.go:544`) to a type set derived from `categoryForEvent` (`service/preferences.go:170`): likes, comments, mentions, followers. Web tabs map to those; Android adds the same chips.
7. **Comment mentions.** In `CreateCommentPG` and `CreateReply` (`post.go:2587,2714`), call the existing `extractMentions` → resolve → `PublishUserMentioned` (`events/producer.go:149`) with a comment deep link; notification-service already has a `comment_mention` category (`preferences.go:182`).
8. **Search history.** Call `RecordSearchHistory` from `UniversalSearch`/`SearchPosts` when `X-User-Id` is present; add `DELETE /v1/search/history/:id`; stop auto-saving every query as a saved search (`W/app/search/page.tsx:826`). Add Videos and Reels tabs that call `/v1/search/posts?type=videos|flicks` (as Android `SearchApi.kt:39-44` does) and a Sounds tab on `/v1/audio/search`.
9. **Explore.** Add `category` to `GetReelFeed` (reuse `NormalizeCategoryFilter`), chips from `GET /v1/posts/categories?kind=short`, and a tile grid reusing the profile `VideosTab` tile. Empty Following/Friends: render `useFollowSuggestions` (exists, unused) with a Follow button on graph-service.
10. **Profile sort and Liked.** Add `?sort=latest|popular|oldest` to `GetPostsByAuthor` (`S/post-service/internal/http/handler.go:1089`, store `posts.go:1015`); add a Liked tab reusing `W/features/reels/liked.ts`.
11. **Reel duration.** Set `tokens.ts` reel/short `maxDuration` to 300 and the `classifyVideo` threshold in `useUploadStudio.ts:74-83` to 300.
12. **Gateway gaps.** Add `/v1/permissions` → graph-service (used by `W/hooks/usePermissions.ts:73`) and `/v1/audio-library` → media-service; decide on `/v1/teen-accounts` and `/v1/media-labels` (open question).
13. **trust-safety entity types.** Add `hashtag` and `sound` (string ids) to `validEntityTypes` and `FileReportRequest` (`S/trust-safety-service/internal/http/handler.go:133`).
14. **Send to friends.** Add `type=post` (`post_id`) to `SendMessageRequest` (`C/message-service/internal/http/handler.go:659`), render it with the existing embed card, and add a friend picker (reuse `NewMessageSheet`) to `ShareDialog`/`ShareSheet`.
15. **Creator Hub numbers.** Map `daily_stats` and likes/comments/shares in `normalizeCreatorInsights` (`hubApi.ts:1205`); add a Likes column to `LibraryTable`; client-side CSV export of the insights arrays; a Drafts tab on `GET /v1/reels/drafts`.
16. **LIVE.**
    - Publish a throttled `viewer.joined` event from `applyPresence` (`lifecycle.go:325`) and render it as a system chat row.
    - Add word-filter, mute and pin controls to `ModerationPanel.tsx`, and copy for `ErrChatBlockedWord`.
    - Add Follow and Share to `/live/[streamId]`.
17. **Shell.** Render `SidebarSearch`; add an unread badge for `key==="activity"`; add Shop and Creator Hub entries to `REELS_NAV_TOP`/More; create `/help` and `/about` or remove the links; persist an Android theme choice in DataStore and pass it to `UsTheme(darkTheme=)`.

---

## 4. Gap analysis (P0–P3 backlog)

Format per `00_COMMON_RULES.md` §6.9. Module: W = web, A = Android, B = backend. Complexity S/M/L.

| ID | Pri | Module | Issue | Evidence | Impact | Solution | Deps | Acceptance criteria | Tests | Cx |
|---|---|---|---|---|---|---|---|---|---|---|
| T-01 | P0 | W | Follow/unfollow/block/follower lists call retired routes (410) | `useEditProfile.ts:175,189`; `useBlocking.ts:32,55,78`; `useConnections.ts:160-250`; profile-service `handler.go:157-193` | Follow and block do nothing on web, including in Reels; blocked users stay unblocked | Point hooks at `/v1/graph/{follow,unfollow,block}`, `/v1/graph/followers|following/:id`, `/v1/graph/relationship` | none | Follow from a profile and from Reels creates a graph edge; block hides the profile; lists load | web unit (hook paths), E2E follow/block | S |
| T-02 | P0 | W | Post report calls a non-existent route | `PostCard.tsx:126-133` | Reports from feed/post pages are lost | Use `useSubmitReport` + report dialog | none | Report from `/post/<id>` creates a trust-safety report | web unit, E2E report | S |
| T-03 | P0 | W+A | No report on profiles; hashtag and sound cannot be reported | `ProfileActions.tsx` orphaned; `moderation.go:20` | Abuse on profiles, tags and sounds cannot be flagged | Profile report (web + Android, `entity_type:user`); add `hashtag`, `sound` entity types | T-13 for B | Each surface's report reaches the trust-safety queue | B unit (validation), web/Android unit, E2E | M |
| T-04 | P0 | W | Social-preferences page saves defaults and can make a private account public | `social-preferences/page.tsx:124-157` | Privacy regression on save | Load settings first; send only changed fields | none | Saving one toggle leaves `account_visibility` unchanged | web unit, E2E privacy | S |
| T-05 | P0 | W | Delete account broken on both paths | `settings/data/page.tsx:79`; `useSecurity.ts:330`; `account_control.go:43` | Users cannot delete their account on web | Single flow `DELETE /v1/auth/account {password}` | none | Delete with password schedules a purge (200) | web unit, auth integration | S |
| T-06 | P0 | W | Notification category toggles are ignored | `settings/notifications/page.tsx:40-47`; `preferences.go:35` | Users keep getting notifications they turned off | Use `/preferences/detailed` (`push_*`, `inapp_*`) | none | Turning off "likes" stops like pushes for that user | web unit, notification integration | S |
| T-07 | P0 | W+A | Shared links fail: `/reels/<id>` gated signed-out; Android `/p/<id>` 404; Android share has no URL | `middleware.ts`; `UsPostMoreState.kt:519`; `SharePost.kt:46-53` | Shares do not open for recipients | One public permalink (`/post/<id>`); App Link; URL in share text | none | A signed-out browser opens a shared public reel; a private one shows not-found | web unit (middleware), Android unit, E2E signed-out | S |
| T-08 | P0 | W | Privacy page shows "Saved" for controls it never sends | `privacy/page.tsx:173-176` | False confirmation of a privacy change | Remove or wire each control; add who-can-comment | T-04 | Every visible control round-trips to `GET /v1/users/me/settings` | web unit | S |
| T-09 | P1 | W | Business verification calls an unrouted path | `verification/page.tsx:121,193`; GW:506 | Verification cannot be requested | Use `useTrust.ts` hooks on `/v1/verification-requests` | none | Request appears in trust-safety | web unit | S |
| T-10 | P1 | W | Wellbeing settings dropped (field names) | `wellbeing/page.tsx:196-198,341-343`; `wellbeing_validate.go:18-20` | Screen-time limits never saved | Rename fields | none | GET returns the saved limit | web unit | S |
| T-11 | P1 | B+W+A | Notification tabs filter nothing | `notification.go:544` | Activity tabs are cosmetic | Category → type set (likes, comments, mentions, followers) | none | `?category=likes` returns only like notifications | B unit, web/Android unit | M |
| T-12 | P1 | B | Comment @mentions not stored or notified | `post.go:2587,2714` | Mentioned users never hear about it | Reuse `extractMentions` + `PublishUserMentioned` | none | Mention in a comment creates an Activity → Mentions entry | B unit + integration | M |
| T-13 | P1 | B | trust-safety rejects hashtag/sound; post-service comment auto-hold unreachable | `moderation.go:20`; `comments.go:722` | Tags/sounds unreportable; report threshold never hides comments | Add entity types; consume `ReportFiled` in post-service for comment holds | none | Report on a sound stores; N comment reports hold the comment | B unit/integration | M |
| T-14 | P1 | W | Web reel cap 180 s vs founder rule 300 s | `tokens.ts:72-73`; `useUploadStudio.ts:74-83` | 181–300 s reels refused or mislabelled | Set 300 | none | 240 s portrait uploads as a reel | web unit | S |
| T-15 | P1 | B+W | Search history never recorded; per-item delete clears all | `search_extras.go:114`; `page.tsx:243-245,826` | Broken recents; accidental wipe | Record on search; `DELETE /history/:id`; stop auto-save | none | Recents show the last queries; deleting one keeps the rest | B unit, web unit | S |
| T-16 | P1 | W+A | Search lacks Videos/Reels/Sounds (and LIVE, Photo) tabs | `page.tsx:81-92`; `handler.go:425-451` | Users cannot search videos or sounds on web | Tabs on existing routes; LIVE row from live-v2 listing (later: search entity) | none | Each tab returns the right entity | web unit, E2E search | M |
| T-17 | P1 | A | Comments: no reply, like, report, delete, pin/heart | `UsCommentsSheet.kt:290-297` | Android comment threads are one-way | Add actions using existing routes | none | All five actions work on a reel | Android unit + UI | M |
| T-18 | P1 | W+B | Explore: no short-video chips, grid or category feed | `GetReelFeed` `handler.go:66`; `categories.go:51-78` | Discovery limited to the ranked feed | `category` on reel feed; chips + tile grid | none | Selecting a chip shows only that category | B unit, web unit, E2E | M |
| T-19 | P1 | W+A | Empty Following/Friends show no suggested creators; suggestion APIs unused | `ReelsScreen.tsx:673-686`; `FeedScreen.kt:148-151` | New users see empty pages | Render follow suggestions with Follow | T-01 | Empty Following shows ≥ 1 suggestion with a working Follow | web/Android unit, E2E | M |
| T-20 | P1 | W+A+B | Profile counts: none on web; Android reads `profile_stats` that nothing updates (Risk); no likes total | `ProfileHeader.tsx:558-596`; `profiles.go:1243-1258` | Wrong or missing counts | Read `/v1/graph/counts/:id`; likes total from post-service aggregate | none | Counts change after follow/unfollow on both clients | unit, integration | M |
| T-21 | P1 | W+B | No Liked tab, no sort, no share/report on other profiles | `posts.go:1015`; `ProfileTabs.tsx` | Profile browsing below reference | `?sort=`; Liked tab; share | T-01, T-03 | Sort changes order; Liked tab lists liked reels | B unit, web unit | M |
| T-22 | P1 | W | Message button never opens chat; `/v1/permissions` not routed | `MessageButton.tsx:67-112`; GW route table | Messaging from profile is a dead end | Route on the conversation id; add GW prefix | none | Tapping Message opens the thread | web unit, GW unit | S |
| T-23 | P1 | W | Embed copies a link to a missing page | `PostCard.tsx:192-198` | Broken embeds on other sites | Public embed page or remove the item | Q-8 | Embed iframe plays a public post; private refused | web unit | S |
| T-24 | P1 | W | Help and About links 404 | `nav.ts:159-160,272` | Dead links in the shell | Add pages or remove links | Q-10 | No 404 in nav | nav unit test covering footer/More | S |
| T-25 | P2 | B+W+A | Send to friends (share a video into chat) missing | `handler.go:659` | Core sharing loop missing | `type=post` message + picker | none | Shared post renders as a card in the recipient's thread | B unit, web/Android unit | M |
| T-26 | P2 | W | No web push | `offline-sw.js` | No desktop notifications | FCM web token → `/v1/notifications/devices` | Q-11 | Desktop receives a like push | manual E2E | M |
| T-27 | P2 | W | Creator Hub drops likes/comments/shares and the daily series | `hubApi.ts:1205` | Hub misreports | Map fields; Likes column; CSV | none | Hub numbers equal `/v1/analytics/creator/me` for one video | web unit, E2E | S |
| T-28 | P2 | B | `/v1/audio-library` not routed; no sound library UI | `audio_handler.go:34-37`; GW:445 | Creators cannot browse sounds | GW prefix; sound picker sheet | none | Picker lists trending/searched sounds | GW unit, web unit | M |
| T-29 | P2 | B+W | LIVE: no join notices, no word-filter/mute/pin UI, no Follow/Share on `/live/[id]` | `lifecycle.go:325`; `live.go:1017` | Room below reference | Publish `viewer.joined`; panel controls | none | Joining posts a system row; blocked word shows notice | B unit, web unit | M |
| T-30 | P2 | A | LIVE thin client: no categories, following, hearts, RTMP, creator centre | `LiveApi.kt:48`; `LiveScreens.kt` | Android LIVE well behind web | Extend `LiveApi` + screens | none | Android parity with web LIVE rows | Android unit/UI | L |
| T-31 | P2 | A | No creator studio on Android | grep of `A/feature` | Creators need the web | Read-only studio: overview, library, comments inbox | T-27 | Android shows the same numbers as web | Android unit | L |
| T-32 | P2 | A | Reels sheet lacks speed, auto scroll, captions; theme is system-only | `UsPostMoreState.kt:229-270`; `Theme.kt:123` | Player prefs below web | Reuse Tube speed/captions; DataStore theme | none | Rows present and persisted | Android unit | M |
| T-33 | P2 | W | Shell: sidebar search hidden, no following list, no Activity badge, no Shop/Creator Hub in reels menu | `VideoSidebar.tsx:63,236`; `nav.ts:130-151,252` | Navigation below reference | Small nav edits | none | Items render and route | nav unit | S |
| T-34 | P2 | W | Hashtag page: no count, share, report; list not grid; tag/sound/search pages gated signed-out | `hashtag/[tag]/page.tsx`; `middleware.ts` | Weak sharing of tags/sounds | Count in meta; ShareSheet; public prefixes | T-13 | Signed-out visitor sees a tag grid | B unit, web unit | M |
| T-35 | P2 | W | Comment pin/heart only on Tube; no comment keyword filter or "filtered" notice | `WatchPage.tsx:548` | Creator tools inconsistent | Pass `creatorTools` on reels/feed; comment filter later | none | Creator can pin on a reel | web unit | S |
| T-36 | P2 | W | Web filter keywords, who-can-comment, region, deactivate missing | §2.13 | Settings only on Android | Reuse Android's routes | T-08 | Each saves and is enforced | web unit | M |
| T-37 | P3 | W+A | Language/i18n | no i18n | Single-language app | i18n framework | Q-12 | Locale switch changes UI strings | unit | L |
| T-38 | P3 | W+A+B | Family pairing / teen accounts (backend unrouted, no UI) | `trust-safety handler.go:90-94` | — | Founder decision first | Q-4 | — | — | L |
| T-39 | P3 | W | Inspiration, search-queries analytics, Viewers/Followers tabs, feedback link | §2.12 | Studio depth | Use retention/demographics routes first | T-27 | — | — | M |
| T-40 | P3 | W+A | Increase contrast; account-level captions default | §2.13 | Accessibility | Token-based high-contrast theme | — | Contrast ≥ 7:1 option | a11y check | M |
| T-41 | P3 | W+A+B | Photos mode in Reels; separate duet/stitch toggles; resolutions above 720p | §2.11 | Upload depth | Founder decisions; ladder is the Reels audit's | Q-2 | — | — | L |

**Risks (need runtime proof):**
- **R-1.** Android profile counts may be frozen. `profile_stats` is only incremented by the retired `FollowUser`. Check by following an account, then reading `GET /v1/profiles/:id/stats` on staging.
- **R-2.** Who-can-message is checked only when a conversation is created (`message.go:989`). Tightening the setting later does not close existing threads. This is a product decision (Q-7).
- **R-3.** LIVE reports go to live-service-v2's own queue, not trust-safety. Moderators must watch two queues.
- **R-4.** Creator Hub numbers come from three sources and can disagree (T-27).
- **R-5.** The web payouts page renders a form with no client-side "payouts are off" copy and relies on the server refusing. Verify with `PAYOUTS_ENABLED=false` that the request returns a clear error.

---

## 5. UI/UX plan (Momentum's words, Momentum tokens only)

Shared rules for every screen:
- **Web:** use `src/ui/theme.css` variables only (`--theme-brand-*`, `--theme-canvas*`, `--theme-text-muted`, `--theme-danger`, `--theme-success`, `--theme-glass-*`, `--reel-stage`, `--reel-on-stage`) and `src/ui/tokens.ts` (`space`, `radius`, `type`, `z`, `duration`). Icons are Lucide; type is Bodoni/Outfit/Figtree.
- **Android:** use `UsTheme.spacing`, `UsTheme.radii` and `UsTheme.colors`, plus `Us*` components (`UsScaffold`, `UsTopBar`, `UsButton`/`UsPillButton`, `UsChoiceRow`, `UsSurfaceCard`, `UsMessageHost`, `UsAvatar`). No hex anywhere.
- **States everywhere:** loading (skeleton), empty (honest reason + one action), error (message + retry), signed-out (public view or a sign-in prompt), private (explain; no content leak).
- **Breakpoints:**
  - mobile < 768 px: bottom nav, full-width sheets
  - tablet 768–1279 px: icon rail, two-pane where a pane exists
  - desktop ≥ 1280 px: full sidebar + right rail
- **Accessibility:** every action reachable by keyboard; visible focus ring (`--theme-brand-outline`); AA contrast; captions offered wherever video plays; sheets trap focus and close on Esc/Back.

| Screen | Target information architecture | Reuses | Notes |
|---|---|---|---|
| Shell | Search · For You · Following · Friends · Explore · LIVE · Shop (MStore) · Messages · Activity (badge) · Create · Profile · More (Settings, Appearance 3-way, Language later, Creator Hub, LIVE tools, Help, Sign out); signed-in "People you follow" list | `VideoSidebar`, `nav.ts`, `VideoMorePanel`, `ThemeRow`; Android `TabResolver`, `HomeMoreSheet` | Rail collapses to icons on tablet; badge from `/v1/notifications/unread-count` |
| Profile | Header (avatar, name, handle, bio, links), counts (Following, Followers, Likes), owner: Edit · Share · Settings; visitor: Follow · Message · Share · ⋮ (Block, Report); tabs Videos · Reels · Saved (owner) · Liked (owner, or public by toggle); sort Latest/Popular/Oldest | `ProfileHeader`, `ProfileActions`, `ProfileTabs`, `VideosTab` tile, `PrivateProfileView`, `ShareDialog`, `ReportChannelDialog`; Android `ProfileScreen`, `RelationshipControls` | Private: header + "This account is private" + Follow; blocked: neutral not-found |
| Explore | Category chips (post categories) · tile grid (cover, creator, likes) · "People to follow" row | posttube chip component, `VideosTab` tile, `PeopleYouMayKnowStrip` | Grid 2/3/5 columns by breakpoint; chips scroll horizontally with arrow keys |
| Following / Friends | Feed; when empty, a grid of suggested creators (preview muted, Follow) | `useFollowSuggestions`, `ReelsScreen` empty branch; Android `FriendsFeedScreen` | Previews never autoplay with sound |
| Search | Tabs: Top · People · Videos · Reels · Sounds · LIVE · Hashtags; recent searches (per-item remove, clear all); suggestions while typing; popular searches when empty | `app/search/page.tsx`, `useAutocomplete`, `ReelSearchScreen`; Android `SearchScreen` | Signed-out allowed; Esc clears |
| Hashtag / Sound | Header (title, count, Share, ⋮ Report; sound: play preview, creator link, "Use this sound") · tile grid | `SoundPageHeader`, `SoundReelsScreen`, `ShareSheet`; Android `SoundPageScreen` | Public when content is public |
| Video page | Player · ⋮ (Speed, Quality, Auto-next, Mini player, Captions, Not interested, Report) · Share (Copy link, Send in Messages, Repost, Embed, other apps) · tabs Comments · More from creator | `ReelMoreMenu`, `moreRows.ts`, `ShareSheet`, `ShareDialog`; Android `UsPostMoreState`, `WatchSettingsSheet` | Founder rules kept: muted start, Android double tap = full mode |
| Comments | Row (avatar, name, text, time, Reply, Like count, ⋮ Report/Delete/Pin) · composer (@ suggestions, emoji) · "Some comments are hidden by filters" notice | `CommentSection`, `MentionInput`, `CommentReactions`, `ReportDialog`; Android `UsCommentsSheet` | Two reply levels |
| Activity | Tabs: All · Likes · Comments · Mentions · Followers · Requests; grouped by time | `app/notifications/page.tsx`; Android `NotificationsScreen` | Unread dot per row; mark-all-read |
| Messages | Inbox · Requests (Accept, Decline, Block, Report) · thread with post cards | `PostbookMessenger`, `NewMessageSheet`, `EmbedCard`; Android `ChatInboxScreen` | Post card shows a not-available state when the viewer may not see it |
| LIVE | Discover (For you, Following, categories, hero, rows) · room (header Follow/Share, video, chat with system rows, hearts, viewer count, Report) · creator centre (upcoming, past, recordings, stats) | `LivePage`, `ReelsLiveScreen`, `LiveChat`, `LiveHearts`, `HubLivePage`; Android `LiveHubScreen`, `LiveWatchScreen` | No gifts, coins or guests |
| Upload | Entry: Create → Reel / Video / Photo post; limits shown before pick (Reel ≤ 5 min, portrait or square) | `UploadStudio`, `fileRules.ts`; Android `CreateHubScreen` | Errors name the limit that failed |
| Creator Hub | Overview (period 7/28/90 days, views, likes, comments, shares, followers, daily chart, latest video, latest comments) · Library (Likes column, Drafts) · Insights (Content, Audience, Followers) · Conversations · Sounds · LIVE · Download CSV | Hub pages, `hubApi.ts`, `LibraryTable`, `ConversationsPage` | Payout areas read-only while payouts are off |
| Settings | Account (region, data download, deactivate, delete) · Privacy (private account, comments from, messages from, filter words) · Notifications (per category, push on this device) · Wellbeing (daily limit, sleep hours) · Appearance (theme, contrast) | `app/settings/*`, Android settings screens | Each control round-trips to its server value; no "Saved" without a successful write |

---

## 6. Phased implementation plan

Order: contracts → backend → web → Android. Each work package (WP) is one service tree or one web feature folder. Only **one Android agent at a time**.

**Shared prompt preamble** (paste before every WP prompt):
> Repo `rvreddy476/modernsmapp` (branch from `codex/module-01-02-launch-safety`) or `rvreddy476/postbook-ui` (branch from `codex/apps-ui-refresh`, Bun only — never npm/yarn/pnpm). Read `docs/production-readiness/TIKTOK_PARITY_AUDIT.md` sections 2–4 for the IDs below. Change only the files named, reuse the components and routes named, use Momentum tokens only (no hex, no TikTok names, colours or wording), keep founder decisions (`05_TIKTOK_PARITY_AGENT.md` §2). Add tests. Run `go build ./... && go vet ./... && go test ./...` in each touched service, or `bunx tsc --noEmit` and `bunx bun test <folder>` on web, or `./gradlew :<module>:testDebugUnitTest` on Android. Commit on a new branch `feat/tiktok-<wp-id>` and push; open no PR unless told.

### Phase 0 — Contracts (½ day)
- **Objectives:** pin the request/response shapes the later phases change.
- **Work:** contract fixtures for:
  - `GET /v1/notifications?category=`
  - `POST /v1/reports` with hashtag/sound
  - `DELETE /v1/search/history/:id`
  - `GET /v1/posts/by-author/:id?sort=`
  - `GET /v1/feed/reels?category=`
  - message `type=post`
- **Files:** `<service>/internal/http/testdata/contracts/**`.
- **DoD:** fixtures committed; existing tests pass.
- **Rollback:** delete the fixtures.

### Phase 1 — Fix what is broken (P0 + quick P1)

Objectives: every visible control works, every privacy setting saves what the user chose, every shared link opens.

| WP | Scope | IDs | Files | DB/API |
|---|---|---|---|---|
| 1-W1 | Web social graph | T-01, T-22 | `src/hooks/useEditProfile.ts`, `useBlocking.ts`, `useConnections.ts`, `src/app/api/profile/[username]/route.ts`, `src/components/connections/MessageButton.tsx` | none |
| 1-W2 | Web report, share, embed, deep links | T-02, T-07 (web), T-23, T-24 | `src/components/PostCard.tsx`, `src/middleware.ts`, `src/features/reels/model.ts`, `src/features/video-shell/nav.ts` | none |
| 1-W3 | Web settings | T-04, T-05, T-06, T-08, T-09, T-10 | `src/app/settings/{social-preferences,privacy,data,notifications,verification,wellbeing}/page.tsx`, `src/hooks/useSecurity.ts` | none |
| 1-W4 | Web upload limit | T-14 | `src/features/upload/tokens.ts`, `useUploadStudio.ts` | none |
| 1-B1 | Gateway prefixes | T-22, T-28 | `Architecture/services/api-gateway/cmd/server/main.go` (+ test) | `/v1/permissions`, `/v1/audio-library` |
| 1-A1 | Android share link + report profile | T-07 (Android), T-03 (Android) | `core/ui/.../UsPostMoreState.kt`, `SharePost.kt`, `app/src/main/AndroidManifest.xml`, `feature/profile/ui/ProfileScreen.kt` | none |

- **Tests:** hook path unit tests, middleware test, settings round-trip tests, gateway route test, Android unit tests.
- **DoD:** E2E journeys 1, 2, 4, 5 (§7) pass on staging.
- **Rollback:** revert the WP branch; there are no schema changes.

Prompts:
- **1-W1:** "Fix T-01 and T-22 in postbook-ui. Replace every `/v1/profiles/<x>/follow|block`, `/v1/profiles/me/blocks`, `/v1/profiles/<id>/followers|following|relationship` call with the graph-service routes (`POST /v1/graph/follow` and `/v1/graph/unfollow` `{user_id}`, `POST/DELETE /v1/graph/block` `{user_id}`, `GET /v1/graph/blocks`, `GET /v1/graph/followers/:id`, `/v1/graph/following/:id`, `/v1/graph/relationship`) — match the shapes Android uses in `mobile/android/core/profile/.../ProfileApi.kt`. Forward the viewer's Authorization/X-User-Id in `src/app/api/profile/[username]/route.ts`. Make `MessageButton` navigate to the returned conversation. Add hook tests asserting the paths."
- **1-W2:** "Fix T-02, T-07 (web), T-23, T-24 in postbook-ui:
  - Replace `PostCard.handleReport` with `useSubmitReport({targetType:'post'})` and the existing report dialog (no `prompt()`).
  - Make reel share/copy use a public permalink. Either add `/reels/` to `PUBLIC_PREFIXES` and make the reels stage work with no session, or share `/post/<id>`; pick the smaller change and say which.
  - Add `src/app/post/[postId]/embed/page.tsx`: a bare public player that refuses non-public posts. If that is not feasible, remove the Embed item.
  - Create `/help` and `/about` pages, or remove those links from `nav.ts`, and extend `nav.test.ts` to cover footer and More links.
  - Add tests."
- **1-W3:** "Fix T-04, T-05, T-06, T-08, T-09, T-10 in postbook-ui `src/app/settings/*`:
  - Load `GET /v1/users/me/settings` before rendering social-preferences and privacy, and send only changed fields.
  - Remove or wire every unsent privacy control, and add who-can-comment (`allow_comments_from`).
  - Delete account: use only `DELETE /v1/auth/account` with `{password}`, and remove the `/v1/users/me` call.
  - Notifications: move to `/v1/notifications/preferences/detailed` (`push_*`/`inapp_*`), as Android `SettingsApi.kt` does.
  - Verification: use the `useTrust.ts` hooks on `/v1/verification-requests`.
  - Wellbeing: rename to `daily_limit_mins`, `bedtime_start`, `bedtime_end` (see `user-service/internal/http/wellbeing_validate.go`).
  - Never toast 'Saved' without a 2xx. Add round-trip tests."
- **1-W4:** "Fix T-14 in postbook-ui: the reel/short `maxDuration` in `src/features/upload/tokens.ts` and the `classifyVideo` threshold in `useUploadStudio.ts` must be 300 s (founder rule, `Architecture/shared/postclassify/classify.go`). Update the upload tests."
- **1-B1:** "In `Architecture/services/api-gateway/cmd/server/main.go` add route prefixes `/v1/permissions` → GRAPH_SERVICE_URL and `/v1/audio-library` → MEDIA_SERVICE_URL. Check the internal-key stamping policy in `pkg/internalroutes` for both. Add gateway tests."
- **1-A1:** "Android only (one agent):
  - In `core/ui/.../UsPostMoreState.kt`, the copy-link base must be the web public permalink `/post/<id>`.
  - Add the URL to the share text in `core/ui/.../SharePost.kt`.
  - Add an App Link intent filter for `/post/` in `app/src/main/AndroidManifest.xml`.
  - Add Report (entity `user`, via `ReportRepository`) to another user's profile menu in `feature/profile/ui/ProfileScreen.kt`.
  - Add unit tests."

### Phase 2 — Backend for social parity
| WP | Scope | IDs | Files | DB/API |
|---|---|---|---|---|
| 2-B1 | notification-service tabs | T-11 | `internal/service/notification.go`, `preferences.go` | `?category=likes|comments|mentions|followers` |
| 2-B2 | post-service comment mentions, author sort, reel category, hashtag count | T-12, T-21, T-18, T-34 | `internal/service/post.go`, `internal/http/handler.go`, `internal/store/postgres/posts.go`, `internal/events/producer.go` | `?sort=`; meta `total`; event `user.mentioned` for comments |
| 2-B3 | feed-service reel category | T-18 | `internal/http/handler.go GetReelFeed`, service | `?category=` |
| 2-B4 | trust-safety entity types + comment-hold consumer | T-13, T-03 | `internal/http/handler.go`, `internal/service/moderation.go`; post-service consumer | entity `hashtag`, `sound` (string id); migration if `entity_id` is UUID-typed |
| 2-B5 | search-service history + tabs | T-15, T-16 | `internal/http/handler.go`, `store/postgres/search_extras.go` | `DELETE /v1/search/history/:id` |
| 2-B6 | message-service post card | T-25 | `C/message-service/internal/http/handler.go`, service | `type=post`, `post_id` |
| 2-B7 | live-service-v2 join notices | T-29 | `internal/service/lifecycle.go` | realtime `viewer.joined` (throttled) |

- **Tests:** unit for each; integration on `_test` databases (post, search, notification, message).
- **DoD:** contract fixtures from Phase 0 pass; E2E journey 3 passes.
- **Rollback:** each WP is additive (new params/types default to old behaviour).

Prompts:
- **2-B1:** "Implement T-11 in notification-service: `GetNotificationsPage` `?category=` must filter by the categories `categoryForEvent` (preferences.go) already defines (likes, comments, mentions, followers, plus the existing qa). An unknown category returns 400. Unit-test each category."
- **2-B2:** "Implement in post-service:
  - T-12: comment and reply mentions. In `CreateCommentPG` and `CreateReply`, reuse `extractMentions`, resolve usernames, and publish `PublishUserMentioned` with a comment deep link. Respect blocks and the mentioned user's privacy.
  - T-21: `GET /v1/posts/by-author/:id?sort=latest|popular|oldest` (popular = like count desc). Keep pinned-first only for `latest`, and keep the cursor stable.
  - T-34: a hashtag `total` in the `/v1/hashtags/:tag/posts` meta.
  - Unit and integration tests (`post_it_test`)."
- **2-B3:** "Implement T-18 in feed-service: `GET /v1/feed/reels?category=<id>` using `service.NormalizeCategoryFilter`, with the same filters as today. Unit tests."
- **2-B4:** "Implement T-13 in trust-safety:
  - Accept entity types `hashtag` and `sound` (string ids); add a migration only if needed.
  - In post-service, consume trust-safety's report event to apply the existing `IncrementCommentFlaggedCount` hold.
  - Tests."
- **2-B5:** "Implement T-15 and T-16 in search-service:
  - Record search history in `UniversalSearch`/`SearchPosts` for signed-in users, deduplicated.
  - Add `DELETE /v1/search/history/:id`.
  - Add a popular-queries read for the empty state.
  - Tests."
- **2-B6:** "Implement T-25 in chat message-service:
  - Add message `type=post` with `post_id`, validated.
  - On read, return a minimal card, or let the client hydrate it through `/v1/posts/batch`, which already enforces visibility.
  - Tests."
- **2-B7:** "Implement the server side of T-29 in live-service-v2: publish a throttled `viewer.joined` realtime event from `applyPresence` (at most 1 per user per minute). Tests."

### Phase 3 — Web parity screens
| WP | Folder | IDs |
|---|---|---|
| 3-W1 | `src/components/profile/*` | T-20 (web), T-21, T-03 (web), profile share |
| 3-W2 | `src/app/discover`, `src/features/reels` (empty states) | T-18, T-19 |
| 3-W3 | `src/app/search` | T-15, T-16 |
| 3-W4 | `src/app/notifications` + push | T-11 (web), T-26 |
| 3-W5 | `src/components/comments`, reels comments | T-35, mention notices |
| 3-W6 | `src/features/posttube/hub` | T-27, T-28 picker, drafts tab |
| 3-W7 | `src/features/live`, `src/app/live` | T-29 UI |
| 3-W8 | `src/features/video-shell` + share sheets | T-33, T-25 picker, T-34 |

Prompt pattern for each: "Implement <IDs> in postbook-ui `<folder>` following TIKTOK_PARITY_AUDIT.md §5 for that screen. Reuse the components named there. Cover all states (empty, loading, error, signed-out, private). Make it keyboard accessible. Tokens only. Add bun tests."

DoD: E2E journeys 1–6 pass on staging. Rollback: revert the WP.

### Phase 4 — Android (one agent at a time, in this order)
1. **4-A1** comments actions (T-17)
2. **4-A2** profile counts, tabs, share (T-20, T-21)
3. **4-A3** search tabs, autocomplete, notifications tabs (T-16, T-11)
4. **4-A4** reels sheet speed/captions/auto-next and 3-way theme (T-32)
5. **4-A5** LIVE parity (T-30)
6. **4-A6** read-only creator studio (T-31)

Prompt pattern: "Android only. Implement <IDs> in `mobile/android/<module>` using the existing APIs named in TIKTOK_PARITY_AUDIT.md §2–§3. Use `Us*` components and `UsTheme` tokens. Keep the founder rules (no reels Following tab; double tap opens full mode; muted start). Add unit tests, then run `./gradlew :<module>:testDebugUnitTest`."

### Phase 5 — Later (needs founder decisions)
T-37 language, T-38 family pairing, T-39 inspiration/analytics depth, T-40 contrast/captions default, T-41 photos mode, duet/stitch split.

---

## 7. Testing plan

Test levels per phase:
- **Unit:** Go `go test ./...` per touched service; web `bunx bun test <folder>`; Android `:<module>:testDebugUnitTest`.
- **Contract:** the Phase 0 fixtures run in each service's `internal/http` tests.
- **Integration:** `-tags integration` with a Postgres DSN whose database name ends in `_test` (`post_it_test`, `search_it_test`, `notification_it_test`, `message_it_test`).
- **E2E:** Playwright for web against staging; Android instrumented tests on a device farm. Both are Unverified here; there is no emulator.
- **Accessibility:** axe on every changed web page; keyboard-only walk; focus order; contrast AA (target ≥ 4.5:1 text); captions present on video; TalkBack labels on Android.

| # | Journey | Steps | Expected | Type | Status |
|---|---|---|---|---|---|
| 1 | Profile → upload → privacy | Sign up → edit profile (name, bio, handle) → upload a 4-minute portrait reel as "followers" → open own profile; open the same profile as a non-follower | Reel in Reels tab for owner; non-follower does not see it; handle change persisted | E2E | Not run (fails today: web handle not saved, 180 s cap) |
| 2 | Search and open signed out | Search a user, a hashtag, a sound → copy each link → open in a signed-out browser | Public profile, tag and sound pages open; private content refused | E2E | Not run (fails today: `/hashtag`, `/reels`, `/search` gated) |
| 3 | Mention → Activity | User A comments "@B hi" on a post → B opens Activity → Mentions | Entry present, deep link to the comment | Integration + E2E | Not run (fails today: T-12, T-11) |
| 4 | Report video and comment | Report a reel and a comment from web and Android | Two rows in trust-safety queue with entity types post and comment | Integration + E2E | Not run (web PostCard path fails today) |
| 5 | Settings enforced for another user | A sets private account, who-can-message = no one, filter word "spoiler" → B (non-follower) views A's posts, tries to message A; A's feed hides posts containing "spoiler" | B sees no posts and cannot start a chat; A's feed has no "spoiler" posts | Integration + E2E | Not run (web settings pages broken today; server enforcement exists) |
| 6 | Creator Hub = analytics source | Generate views/likes on one video → compare Hub overview and Library row with `GET /v1/analytics/creator/me` and `GET /v1/posts/:id` counts | Same numbers within the aggregation delay | Integration | Not run (Hub drops likes/comments/shares today) |
| 7 | Follow/block on web | Follow, unfollow, block, unblock from a profile and from Reels | Graph edges change; lists update | E2E | Not run (fails today: T-01) |
| 8 | Delete account on web | Delete with password | Account scheduled for purge; sign-out | E2E | Not run (fails today: T-05) |

**Run here (see the log):** 19 Go service test suites passed (`go test ./...`). Web: `bunx tsc --noEmit` clean, and 1,327 bun tests passed across reels, video-shell, upload, settings, live and posttube. Android was not run (no SDK).

---

## 8. Open questions for the founder

1. **Names:** what does Momentum call Favorites (web says "Stash") and Repost, on every surface?
2. **Photos in Reels:** should photo carousel posts be allowed in Reels, or stay feed posts (web 4 images, Android 10 — which limit)?
3. **Screen time:** a nudge only (Android today) or a hard stop? Is a weekly summary push wanted?
4. **Family pairing / teen accounts:** the backend exists but is unrouted. Build it, or keep the 18+ registration rule only?
5. **Explore categories:** reuse the 18 post categories (`post-service/internal/service/categories.go`), or a new taxonomy?
6. **Liked tab privacy:** always private (today), public by default, or a per-user toggle?
7. **Who can message:** should tightening the setting close or freeze existing threads (R-2)?
8. **Embeds:** allowed for public posts only, and for reels as well as posts?
9. **Signed-out reels:** open `/reels/<id>` to signed-out viewers, or always share `/post/<id>`?
10. **Help Center and About:** in-app pages, or links to an external help site?
11. **Web push:** FCM for web (backend ready), or no desktop notifications?
12. **Language:** which languages first?
13. **LIVE reports:** keep the separate live-v2 queue, or forward to trust-safety (R-3)?
14. **Shop placement:** should Shop be a top-level nav item in the reels menu and the Android bar?
15. **Promote post:** is there any plan for paid promotion, or should it stay absent?
