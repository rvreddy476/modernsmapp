# Cloud agent brief: TikTok parity audit and phased plan (whole app, beyond the Reels player)

Read `docs/production-readiness/agents/00_COMMON_RULES.md` first. Its rules apply here in full, with three changes:
- The output is `docs/production-readiness/TIKTOK_PARITY_AUDIT.md` + `TIKTOK_PARITY_AUDIT_LOG.md`.
- The branch is `audit/tiktok-parity`.
- The scope is the list below, not Feed/Reels/PostTube.

**This is Phase 1 only: audit and phased implementation plan. Do not modify any application code.**

## 1. Why this run exists

The founder asked for a comparison of Momentum against TikTok as a Principal Product Engineer / UI architect would make it. The comparison covers:
- feature audit
- code comparison
- gap analysis
- UI/UX plan
- testing plan

Another cloud agent (`02_REELS_AGENT.md`) already audits the reels **player, upload pipeline, engagement counts, moderation and scaling**. Do not repeat that work: link to `REELS_AUDIT.md` if it exists on `audit/reels-production-readiness`, and otherwise write "covered by the Reels audit".

This run covers everything else a TikTok user touches. The reference is the founder's own signed-in TikTok web account, walked read-only on 14 Oct 2026; the result is section 3. You have no TikTok access, so treat section 3 as the reference, and do not browse TikTok.

## 2. Rules specific to this run

- **No branding copy.** Never recommend copying TikTok's name, logo, colours, icons, sounds, layouts pixel for pixel, or its wording where Momentum already has its own words.
  - Momentum's design system is "Momentum": navy palette, ember gradient accent, Bodoni/Outfit/Figtree, Lucide icons, tokens only (no hex).
  - The web tokens live in `postbook-ui/src/ui/theme.css`; check for `src/ui/` token files.
  - Compare **functions**, not looks.
- Founder decisions already made, which you must not re-open:
  - Reels never appear in the home feed.
  - Android reels have no Following tab, and a double tap opens full mode.
  - Reels open muted; sound stays on once the viewer turns it on.
  - The reels More menu is one alphabetical list.
  - "Keep a copy" is an in-app offline copy only; viewers never get a file.
  - Public videos play signed out.
  - PostTube follows RUTUBE layouts and words.
  - LIVE has no co-hosts, no gifts and no coins for now (viewers comment only); LiveKit is the live stack.
  - Payouts are OFF; monetisation is gated.
- The founder's real work lives on three branches:
  - web: `rvreddy476/postbook-ui` branch `codex/apps-ui-refresh`
  - backend and Android: `rvreddy476/modernsmapp` branch `codex/module-01-02-launch-safety`
  - admin console: `rvreddy476/atpost-web` branch `feat/commerce-catalogue-attributes`, under `apps/admin`

## 3. TikTok reference (walked 14 Oct 2026, web, signed in; account with 0 posts and 0 followers)

### 3.1 Main navigation (left sidebar)
- Search box
- For You · Shop · Explore · Following · Friends · LIVE · Messages · Activity (with an unread badge) · Upload · Profile · More
- When signed in, a "Following accounts" list sits under the menu
- Top right: Get Coins, Get App

### 3.2 More menu
- **Settings:** General · Language (English (US)) · Dark mode (3-way)
- **Tools:** TikTok Studio · Create effects (external effects tool) · Promote post · LIVE tools · Get Coins · Sell on TikTok Shop
- **Other:** Help Center · Log out

### 3.3 For You (one video in the feed)
- Full-screen vertical video with a progress slider
- A creator link with a Follow "+"
- Like (with count) · Comments (with count) · Add to Favorites (with count) · Share (with count)
- A sound link to `/music/<name>-<id>`
- Up/down navigation between videos

### 3.4 Video detail page (`/@user/video/<id>`)
- **Top:** close, "Find related content" search bar, `…` menu
- **`…` menu:**
  - Speed (0.75 / 1.0 / 1.25 / 1.5 / 2.0)
  - Quality (Auto ›)
  - Auto scroll (toggle)
  - Floating Player
  - Captions
  - Not interested
  - Report
- **Right panel:** creator, date, Follow; caption with #hashtags and @mentions; the sound line
- **Counters:** Like · Comment · Favorite
- **Share row:** Repost · Embed · Send to friends · Facebook · WhatsApp · more (X, LinkedIn, Reddit, Telegram, Email, Line, Pinterest)
- A copyable link with **Copy link**
- **Tabs:** Comments (N) and Creator videos
- **Each comment:** avatar, name, text and stickers/images, time, Reply, like count; `…` → Report (for someone else's comment)
- **Composer:** Add comment…, @ to tag a user, emoji, Post
- **Bottom:** mute toggle and a mini-player toggle

### 3.5 Explore
Category chips:
All · Singing & Dancing · Comedy · Sports · Anime & Comics · Relationship · Shows · Lipsync · Daily Life · Beauty Care · Games · Society · Outfit · Cars · Food · Animals · Family · Drama · Fitness & Health · Education · Technology

Below them is a grid of tiles showing like counts and creators.

### 3.6 Search (`/search?q=`)
- Tabs: Top · Users · Videos · LIVE · Photo
- The Top tab mixes a LIVE row ("See more") with video results
- The right rail shows "Others searched for" suggestions

### 3.7 Hashtag page (`/tag/<name>`)
- Avatar, #title, share, `…` (Report)
- A grid of videos

### 3.8 Sound page (`/music/<name>-<id>`)
- Play button, title, creator, "N videos", share, `…` (Actions)
- A grid of videos that use the sound

### 3.9 Following and Friends
With nothing followed, both pages show a grid of suggested creators, each with a playing preview and a Follow button.

### 3.10 Activity panel ("Notifications")
- Tabs: All activity · Likes · Comments · Mentions and tags · Followers
- Empty state text

### 3.11 Messages
- A list column with an empty state "No messages yet" and a conversation pane
- Not explored further (no conversations exist)

### 3.12 LIVE (`/live`)
- **Left:** Back · Discover LIVE · Go LIVE (opens a download page for the desktop LIVE Studio app) · Creator tools (LIVE Center, LIVE Creator Hub) · More · Get Coins · Suggested LIVE creators (with viewer counts)
- **Top chips:** For You · Following · Gaming · Lifestyle · per-game categories
- **Hero:** a large rotating live preview
- **Rows:** categories with "View all"
- **Live room:**
  - Header: creator, likes, Follow, Share
  - Video
  - Gift bar: gifts with coin prices, Send, More, Recharge
  - Right column: Viewers list with top-3 ranking; chat with system messages ("Welcome…", "comments filtered", "<user> joined"); quick reactions; Type… input with emoji; like heart
  - "Recommended LIVE streams" underneath
  - Opening a room posts "<viewer> joined" in the chat

### 3.13 Profile (own)
- Edit profile · Promote post · settings · share
- Counts for Following, Followers and Likes
- Tabs: Videos · Favorites · Liked
- Sort: Latest · Popular · Oldest
- **Another user's profile:** Follow · Message · suggested accounts · Share · Actions

### 3.14 Upload (`/tiktokstudio/upload`)
- Videos | Photos tabs
- Drag and drop or Select video
- Limits: 30 GB, 60 min, .mp4 recommended, 1080p/1440p/4K, 16:9 or 9:16

### 3.15 TikTok Studio (`/tiktokstudio/*`; collapsible icon sidebar)
- **Upload**
- **Home:**
  - profile counts
  - Key metrics for the last 7 days (Video views, Profile views, Likes, Comments, Shares, Est. rewards) with a daily chart
  - Recent posts · Latest comments · Inspiration (Trending/Recommended, Posts/Creators, region and category filters) · Knowledge for you (articles)
- **Posts** (`/content`): a table of Posts (Created on) · Privacy · Views · Likes · Comments · Actions; posted and scheduled videos
- **Analytics** (`/analytics`):
  - Tabs: Overview · Content · Viewers · Followers
  - Download data
  - Same key metrics, Traffic source, Search queries
- **Comments** (`/comment`): filters All comments · Posted by all · Follower count · Comment date
- **Inspiration** (`/inspiration`):
  - Tabs: Trending · Recommended · My inspiration
  - Posts/Creators, region, categories
  - Ranked list with views and likes
- **Monetization** (`/monetization`):
  - Rewards analytics, Sources of rewards, Active programs
  - Programs for you: Gaming Incentive, Video Gifts, Subscription, Creator Marketplace, LIVE Incentive, Service+, each with eligibility
  - Learn to monetize
- **Sound library** (`/sound-library`): Royalty-free sounds · Playlists · Sounds/Songs/Sound effects · Theme/Style/Duration/Language filters · Sort by Hot
- **Feedback** · **Back to TikTok**

### 3.16 Settings (`/setting`)
- **Manage account:**
  - Account region
  - Download your data
  - Deactivate or delete account
- **Privacy:**
  - Private account (Discoverability)
  - Who can comment
  - Who can send you messages
- **Push notifications:**
  - Desktop "Allow in browser"
  - Interactions (likes, comments, new followers, mentions and tags)
  - In-app notifications
- **Business verification**
- **Ads:**
  - Manage ad topics · Mute advertisers · Edit personal details (gender for ads)
  - Targeted ads outside of TikTok · Off-TikTok activity · Disconnect advertisers · Clear off-TikTok data
- **Screen time:**
  - Daily screen time · Sleep hours · Weekly screen time updates · Summary
  - Digital well-being tips
- **Content preferences:** Filter keywords
- **Family Pairing**
- **Accessibility:** Increase color contrast

## 4. Scope checklist (each item becomes a row in your feature matrix: Momentum web / Momentum Android / backend)

1. **Navigation and shell:**
   - main menu items and their order vs 3.1
   - More menu vs 3.2
   - language, dark/light/system theme
   - Get App
   - Help Center
2. **Profile:**
   - own and other users' profiles
   - counts
   - Videos / Favorites (saved) / Liked tabs and their privacy
   - sort Latest/Popular/Oldest
   - Edit profile fields
   - share profile
   - Message button
   - suggested accounts
   - block/report from the profile
3. **Explore and discovery:**
   - category chips and the source of the category list
   - grid
   - Following and Friends pages, including empty-state suggestions
   - suggested accounts
4. **Search:**
   - tabs Top/Users/Videos/LIVE/Photo
   - suggestions and "others searched for"
   - search history
   - "Find related content" from a video
5. **Hashtag and sound pages:**
   - `/tag`, `/music` (or Momentum's equivalents)
   - video counts
   - share
   - report
   - "use this sound"
6. **Video page details:**
   - `…` menu items in 3.4: speed, quality, auto scroll, floating/mini player, captions, not interested, report
   - share row: repost, embed, send to friends, copy link, external networks
   - Creator videos tab
   - deep links that open the video signed out
7. **Comments:**
   - replies
   - likes
   - @mention autocomplete
   - emoji/stickers/images in comments
   - report
   - delete own comment
   - creator pin and heart
   - filtering (keywords)
   - "comments filtered" notice
8. **Activity / notifications:**
   - filter tabs in 3.10
   - unread badge
   - web push / desktop notifications
   - per-category preferences
9. **Messages:**
   - inbox
   - message requests
   - "who can message me"
   - share a video into a chat ("Send to friends")
10. **LIVE** (viewer and creator):
    - discover page, categories, Following
    - live room: chat, viewer list, likes/hearts, join notices, comment filtering, report
    - Go LIVE from web (streaming-software link / browser)
    - LIVE creator centre
    - mark as out of scope and only list (do not plan) gifts, coins, recharge and guests, per the founder's decisions
11. **Upload:**
    - limits (size, duration, formats, resolutions, aspect ratios)
    - Videos and Photos (photo carousel posts?)
    - drafts, schedule, privacy, comments/duet/stitch-like toggles, cover
    - Covered in depth by the Reels audit for flicks; here, compare only the upload entry points and the limits.
12. **Creator studio** (Momentum's Creator Hub for PostTube and any reels creator tools):
    - Home dashboard with key metrics and period
    - posts table (privacy, views, likes, comments, actions, scheduled)
    - analytics tabs (Overview/Content/Viewers/Followers, traffic source, search queries, download data)
    - comments inbox with filters
    - inspiration/trending
    - monetisation programmes page (read-only while payouts are off)
    - sound library
    - feedback
13. **Settings and privacy:**
    - every item in 3.16, mapped to what exists, for example:
      - user-service privacy settings
      - auth-service account control (deactivate/delete)
      - data export
      - notification preferences
      - screen time / sleep reminders
      - filter keywords
      - family pairing / teen controls
      - accessibility (contrast, captions default)
    - ads settings: list them, but treat them as "no ads product yet" unless code exists
14. **Trust and safety surfaces:**
    - report flows on video, comment, profile, hashtag, sound and LIVE
    - not interested
    - restricted/age-gated content
    - "comments filtered" behaviour
15. **Shop and coins entry points:**
    - Momentum has MStore (`/shop`), so map the TikTok Shop entry point to it
    - Coins are out of scope

## 5. Code map: starting points only (confirm everything; nothing here was re-verified for this run)

- **Web** (`postbook-ui`, branch `codex/apps-ui-refresh`):
  - `src/app/*` (route list: run `ls src/app`)
  - `src/features/{reels,posttube,settings,upload,offline,shop}`
  - shell/sidebar components (search `Sidebar`, `nav.ts`)
  - messenger (`PostbookMessenger.tsx`)
  - notifications (search `notifications`, `useNotifications`)
  - profile (`src/app/profile`, `src/app/u/*` or similar)
  - search (`src/app/search`)
  - settings (`src/app/settings/*`, including `privacy`, `notifications`, `hidden-channels`)
  - the PostTube Creator Hub (`src/app/posttube/hub/*`)
  - Bun only.
- **Backend** (`Architecture/services`):
  - `user-service`: profiles, settings, privacy
  - `graph-service`: follow, connections, block, the permission engine `internal/permission/permission.go`
  - `post-service`: posts, comments, reels, sounds catalogue removed, channels, uploads list, comments inbox
  - `feed-service`: for-you, following, reels/flicks, feedback, hidden channels
  - `search-service`: posts/users/channels search
  - `notification-service`: inbox, push, preferences, SSE realtime
  - `message-service` / `chat-service`: chat, message requests
  - `live-service-v2`: LiveKit, ingress, recording
  - `analytics-service`: creator and content analytics
  - `monetization-service`: creator fund, tips, memberships; payouts off
  - `media-service`: sounds `/v1/audio/*`, subtitles, frames
  - `trust-safety-service`: reports, appeals, grievances
  - `auth-service`: sessions, 2FA, deactivate/delete
  - `api-gateway`: route table in `cmd/server/main.go`
- **Android** (`mobile/android`):
  - `feature/{feed,profile,search,notifications,chat,settings,tube,post,live}` (confirm the names with `ls feature`)
  - `core/*`
  - the `:app` launcher

## 6. What to deliver (sections of `TIKTOK_PARITY_AUDIT.md`)

1. **Executive summary.** Parity level per area (High/Medium/Low), the 10 biggest gaps, and what Momentum does better or deliberately differently.
2. **Feature matrix.** One row per checklist item and sub-item: TikTok behaviour (from section 3) | Momentum web | Momentum Android | Backend route(s) | Status (Complete / Partial / Missing / Broken / Unverified / Out of scope by founder decision) | Evidence (file:function).
3. **Code comparison.** For each Partial/Broken row: what exists, where it stops, and the smallest change that closes it. Reuse first. Name existing components and routes to extend, never rewrite working code.
4. **Gap analysis.** P0–P3 backlog in the 00_COMMON_RULES format. P0 here means a broken or insecure user-facing flow, for example a report button that does nothing, a privacy setting that is not enforced server-side, or a signed-out deep link that fails.
5. **UI/UX plan.** Per screen (shell, profile, explore, search, hashtag/sound, video page, comments, activity, messages, LIVE, upload, Creator Hub, settings):
   - the target information architecture in Momentum's own words
   - which existing Momentum components it reuses
   - states: empty, loading, error, signed-out, private
   - mobile/tablet/desktop behaviour
   - accessibility: keyboard, focus, contrast, captions
   - Momentum tokens only
   - No TikTok screenshots, logos or copy; describe functions.
6. **Phased implementation plan.**
   - Phases in dependency order: contracts → backend → web → Android.
   - Each phase lists: objectives, work items, files, DB/API changes, tests, definition of done, rollback.
   - Split into independent work packages (one service tree or one web feature folder each).
   - Give each package a ready-to-paste prompt for a later implementation agent.
   - One Android agent at a time.
7. **Testing plan.**
   - Unit, contract fixture, integration (`_test` databases only) and E2E per phase.
   - Accessibility checks.
   - E2E journeys:
     - sign up → edit profile → upload → appears on profile with the right privacy
     - search a user/hashtag/sound and open it signed out
     - comment with a mention → the mentioned user gets an Activity → Mentions entry
     - report a video and a comment → the item reaches trust-safety
     - switch a setting (private account, who can message, filter keywords) → enforced by the server for another user
     - Creator Hub numbers equal the analytics source for one video
8. **Open questions for the founder.** Only real product decisions, for example:
   - Momentum's names for Favorites and Repost
   - whether Photos/carousel posts belong in Reels
   - screen-time features
   - family pairing
   - whether Explore categories are a new taxonomy or the existing post categories

Write the matching `TIKTOK_PARITY_AUDIT_LOG.md` (commands, exit codes, results). Commit both files on `audit/tiktok-parity` and push. Open no PR.

## 7. Budget

Same as 00_COMMON_RULES section 8. Prefer `rg` over opening whole trees. Run each touched service's unit tests once at most. If the web repo is not available, mark the web column **Unverified** and continue.
