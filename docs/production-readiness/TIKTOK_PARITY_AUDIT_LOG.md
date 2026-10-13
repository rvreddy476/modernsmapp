# TikTok parity audit — command log

All commands were read-only except creating the two report files and the git worktrees used to check out the audited commits. Code audited: `rvreddy476/modernsmapp` `codex/module-01-02-launch-safety` @ `82cd1a16`; `rvreddy476/postbook-ui` `codex/apps-ui-refresh` @ `eb10bd3`. Scratch checkouts: `tt-api` (modernsmapp worktree on branch `audit/tiktok-parity`) and `tt-web` (detached postbook-ui worktree).

## Setup

| # | Command | Exit | Result |
|---|---|---|---|
| 1 | `git fetch origin codex/module-01-02-launch-safety audit/reels-production-readiness` | 128 | `audit/reels-production-readiness` does not exist → Reels topics are "covered by the Reels audit" |
| 2 | `git ls-remote --heads origin audit/reels-production-readiness audit/tiktok-parity` | 0 | no output (neither branch exists) |
| 3 | `git worktree add -b audit/tiktok-parity tt-api origin/codex/module-01-02-launch-safety` | 0 | first at `d015cab3`; brief `05_TIKTOK_PARITY_AGENT.md` not there |
| 4 | `git log --all --oneline -i --grep=tiktok` | 0 | brief added in `41d19724` on the remote branch |
| 5 | `git fetch origin` then `git reset --hard origin/codex/module-01-02-launch-safety` (worktree only) | 0 | worktree at `82cd1a16`, which contains the brief |
| 6 | `git worktree add --detach tt-web origin/codex/apps-ui-refresh` | 0 | `eb10bd3` |
| 7 | read `docs/production-readiness/agents/00_COMMON_RULES.md`, `05_TIKTOK_PARITY_AGENT.md` | 0 | rules and brief read in full |
| 8 | `ls src/app src/features src/ui` (web); `ls mobile/android/feature mobile/android/core Architecture/services chat-service/services identity-platform/services` | 0 | inventories used to split the audit |
| 9 | `grep -o -- "--[a-z0-9-]*:" src/ui/theme.css`; `sed -n 1,40p src/ui/tokens.ts`; Android `theme/UsTokens.kt`, `component/` listing | 0 | token names for the UI plan (§5) |

## Unit tests and type checks (one run each)

| # | Command | Exit | Result |
|---|---|---|---|
| 10 | `cd tt-api/Architecture/services/user-service && go test ./...` | 0 | ok packages 4 , FAIL lines 0, 54s |
| 11 | `cd tt-api/Architecture/services/graph-service && go test ./...` | 0 | ok packages 5 , FAIL lines 0, 5s |
| 12 | `cd tt-api/Architecture/services/post-service && go test ./...` | 0 | ok packages 11 , FAIL lines 0, 26s |
| 13 | `cd tt-api/Architecture/services/feed-service && go test ./...` | 0 | ok packages 7 , FAIL lines 0, 12s |
| 14 | `cd tt-api/Architecture/services/search-service && go test ./...` | 0 | ok packages 10 , FAIL lines 0, 18s |
| 15 | `cd tt-api/Architecture/services/notification-service && go test ./...` | 0 | ok packages 10 , FAIL lines 0, 13s |
| 16 | `cd tt-api/Architecture/services/live-service-v2 && go test ./...` | 0 | ok packages 5 , FAIL lines 0, 7s |
| 17 | `cd tt-api/Architecture/services/analytics-service && go test ./...` | 0 | ok packages 8 , FAIL lines 0, 5s |
| 18 | `cd tt-api/Architecture/services/monetization-service && go test ./...` | 0 | ok packages 5 , FAIL lines 0, 8s |
| 19 | `cd tt-api/Architecture/services/media-service && go test ./...` | 0 | ok packages 11 , FAIL lines 0, 145s |
| 20 | `cd tt-api/Architecture/services/trust-safety-service && go test ./...` | 0 | ok packages 6 , FAIL lines 0, 8s |
| 21 | `cd tt-api/Architecture/services/api-gateway && go test ./...` | 0 | ok packages 8 , FAIL lines 0, 4s |
| 22 | `cd tt-api/chat-service/services/call-service && go test ./...` | 0 | ok packages 7 , FAIL lines 0, 100s |
| 23 | `cd tt-api/chat-service/services/message-service && go test ./...` | 0 | ok packages 4 , FAIL lines 0, 9s |
| 24 | `cd tt-api/chat-service/services/ws-gateway && go test ./...` | 0 | ok packages 2 , FAIL lines 0, 6s |
| 25 | `cd tt-api/identity-platform/services/auth-service && go test ./...` | 0 | ok packages 11 , FAIL lines 0, 72s |
| 26 | `cd tt-api/identity-platform/services/profile-service && go test ./...` | 0 | ok packages 3 , FAIL lines 0, 5s |
| 27 | `cd tt-api/identity-platform/services/user-service && go test ./...` | 0 | ok packages 4 , FAIL lines 0, 4s |
| 28 | `cd tt-web && bun install --frozen-lockfile` | 0 | ok packages 0 , FAIL lines 0, 2s |
| 29 | `cd tt-web && bunx tsc --noEmit` | 0 | ok packages 0 , FAIL lines 0, 75s |
| 29 | `cd tt-web && bunx bun test src/features/reels` | 0 | 302 pass 0 fail Ran 302 tests across 23 files. [3.07s] |
| 30 | `cd tt-web && bunx bun test src/features/video-shell` | 0 | 91 pass 0 fail Ran 91 tests across 10 files. [370.00ms] |
| 31 | `cd tt-web && bunx bun test src/features/upload` | 0 | 75 pass 0 fail Ran 75 tests across 5 files. [673.00ms] |
| 32 | `cd tt-web && bunx bun test src/features/settings` | 0 | 35 pass 0 fail Ran 35 tests across 2 files. [357.00ms] |
| 33 | `cd tt-web && bunx bun test src/features/live` | 0 | 306 pass 0 fail Ran 306 tests across 19 files. [754.00ms] |
| 34 | `cd tt-web && bunx bun test src/features/posttube` | 0 | 518 pass 0 fail Ran 518 tests across 35 files. [1081.00ms] |
| 35 | Android `./gradlew` | — | **Not run:** no Android SDK or Gradle cache in the sandbox (Unverified) |

Totals: 19 Go test suites pass with 0 failures; web type-check clean; 1,327 web tests pass, 0 fail.

## Manual verification of P0 claims (by the lead auditor)

| # | Command | Exit | Result |
|---|---|---|---|
| 36 | `sed -n 50,110p src/middleware.ts \| grep -o '"/[^"]*"'` | 0 | public list includes `/post/`, `/u/`, `/posttube/watch`, `/shop`; **no `/reels/`, `/hashtag/`, `/search`** |
| 37 | `grep -n report src/components/PostCard.tsx`; `grep -rn '/report' post-service/internal/http` | 0 | web posts to `/v1/posts/${id}/report`; post-service only has `/v1/reports` (and the gateway sends that to trust-safety) |
| 38 | `grep -rn momentum.app mobile/android/core/ui/src/main`; `ls src/app/post/[postId]` | 0 | `POST_LINK_BASE = "https://momentum.app/p/"`; no `/p` route; post route has only `page.tsx` (no embed) |
| 39 | `sed -n 68,76p src/features/upload/tokens.ts`; `grep FlickMaxDurationSeconds shared/postclassify/classify.go` | 0 | web reel/short `maxDuration: 180`; server constant 300 |
| 40 | `grep -n audio api-gateway/cmd/server/main.go`; `grep audio-library media-service/internal/http/audio_handler.go`; `sed -n 352,366p main.go` | 0 | only `/v1/audio` prefix; media registers `/v1/audio-library`; matching is exact or `prefix+"/"`, so it is unreachable |
| 41 | `grep -rln "oembed\|/embed" src/app services/*/internal/http` | 1 | no embed or oEmbed route |
| 42 | `grep -rn RecordSearchHistory search-service` | 0 | definition only (`search_extras.go:114`), no caller |
| 43 | `ls -d src/app/help src/app/about` | 2 | neither exists |
| 44 | `grep -n 'v1/users/me\|v1/auth/account' src/app/settings/data/page.tsx src/hooks/useSecurity.ts`; `grep -n DELETE( user-service/internal/http/handler.go`; `grep bindAccountControl account_control.go` | 0 | web DELETE `/v1/users/me` (no such route; `/v1/users` → user-service has no DELETE /me) and DELETE `/v1/auth/account` without body; server requires `password` |
| 45 | `grep -rn 'muted_types\|MutedTypes' notification-service` | 0 | written by the handler and store only; no delivery read |
| 46 | `grep -n '"/v1/trust\|/v1/verification' main.go`; `grep v1/trust settings/verification/page.tsx` | 0 | gateway has `/v1/verification-requests` only; page calls `/v1/trust/verification` |
| 47 | `grep -n 'daily_limit\|bedtime' settings/wellbeing/page.tsx user-service/.../wellbeing_validate.go` | 0 | web `daily_limit_minutes`/`bedtime_mode_*` vs server `daily_limit_mins`/`bedtime_start` |
| 48 | `grep -n 'useState\|useEffect\|api.get\|api.put' settings/social-preferences/page.tsx` | 0 | state initialised to defaults, no load, PUT writes `account_visibility` |
| 49 | `grep -n -A14 'func categoryTypePrefix' notification-service/internal/service/notification.go` | 0 | only `qa` maps to a prefix |
| 50 | `grep -n -A6 validEntityTypes trust-safety-service/internal/service/moderation.go` | 0 | user, post, comment only |
| 51 | `sed -n 2587,2720p post-service/internal/service/post.go \| grep ention` | 1 | no mention handling in comment create/reply |
| 52 | `grep -n '"/v1/profiles"\|"/v1/permissions\|"/v1/graph"' main.go`; `sed -n 148,193p profile-service/internal/http/handler.go`; `grep -n api. src/hooks/useEditProfile.ts src/hooks/useBlocking.ts` | 0 | `/v1/profiles` → profile-service, which answers follow/block/followers/following/relationship with `retiredGraphRoute` (410); web hooks still call them; no `/v1/permissions` prefix |

## Read-only code reading (delegated to five parallel read-only agents, then spot-checked above)

Each agent only ran `ls`, `rg`/`grep`, `sed -n`, `cat` and file reads on the two checkouts. Summary of what was read, by area:

- **Navigation, settings, shop (areas 1, 13, 15):** `nav.ts`, `VideoSidebar.tsx`, `VideoMorePanel.tsx`, `themeChoice.ts`, `VideoTopCluster.tsx`, `app/layout.tsx`, `Sidebar.tsx`, `MobileBottomNav.tsx`, `ExploreLauncher.tsx`, `appBrand.ts`, `next.config.ts`, `nav.test.ts`; web settings pages (data, privacy, social-preferences, notifications, verification, wellbeing), `useSecurity.ts`, `useTrust.ts`, `useWellbeing.ts`, `useActivityNotifications.ts`; Android `TopLevelDestination.kt`, `TabResolver.kt`, `UsNavHost.kt`, `HomeMoreSheet.kt`, `MomentumHeader.kt`, `MeMoreSheet.kt`, `LauncherTiles.kt`, settings screens and APIs, `Theme.kt`, `MainActivity.kt`; gateway route table; identity user-service, auth-service (`account_control.go`, `ExportUserData`, `eligibility.go`), user-service wellbeing, graph `permission.go`, post-service `privacy_gate.go`, chat `checkMessagePermission`, notification-service preferences and processor, trust-safety handlers, feed `keywordfilter.go`; rg for i18n, coins, ads, contrast, captions, push handlers.
- **Profile, explore (areas 2, 3):** `ProfilePage.tsx`, `ProfileHeader.tsx`, `ProfileTabs.tsx`, `ProfileActions.tsx` (and its importers), `useEditProfile.ts`, `useBlocking.ts`, `useConnections.ts`, `app/api/profile/[username]/route.ts`, `MessageButton.tsx`, `usePermissions.ts`, edit-profile pages, `app/discover`, `app/trending`, `FriendsView.tsx`, reels feed code; profile-service routes, `retired_graph_routes.go`, `profile_validation.go`, stats store and consumer; graph-service routes; post-service routes, `categories.go`, `posts.go` ordering, `reel_engagement.go`; suggestion-service routes and callers; search-service discover; feed-service routes; Android `ProfileApi.kt`, `ProfileScreen.kt`, `ProfileGridViewModel.kt`, `StartChatViewModel.kt`, `EditProfileScreen.kt`, `FeedTab.kt`, `FriendsFeedScreen.kt`, `TubeHomeViewModel.kt`.
- **Search, hashtag/sound, video page (areas 4, 5, 6):** `middleware.ts`, `app/search/page.tsx`, `useSearch.ts`, `ReelSearchScreen.tsx`, `app/hashtag/[tag]/page.tsx`, sound page and `SoundPageHeader.tsx`, `sounds.ts`, reels `menu.ts`/`moreRows.ts`/`ReelMoreMenu.tsx`/`playerPrefs.ts`, `ShareSheet.tsx`, `ShareDialog.tsx`, `PostCard.tsx`, `useReport.ts`, `miniPlayer.tsx`, `ReelGapSearch.tsx`; search-service handlers and stores; post-service hashtag, sound, report, repost routes and `viewerMayViewPost`; media-service audio handler; trust-safety report validation; feed-service feedback; Android search files, `HashtagPostsScreen.kt`, `SoundPageScreen.kt`/ViewModel, `UsPostMoreState.kt`, `SharePost.kt`, `PostMoreViewModel.kt`, `ReportApi.kt`, `WatchSettingsSheet.kt`, `AndroidManifest.xml`.
- **Comments, activity, messages, safety (areas 7, 8, 9, 14):** `CommentSection.tsx`, `components/comments/*`, `usePostComments.ts`, `useMentionSearch.ts`, `ReelCommentsDrawer.tsx`, `WatchPage.tsx`, notifications page and hooks, `Header.tsx`, `public/offline-sw.js`, `PostbookMessenger.tsx`, `messageService.ts`, `ProfileActions.tsx`; post-service comment handlers and `CreateCommentPG`/`CreateReply`, `comment_creator.go`, comments store; notification-service routes, `GetNotificationsPage`, `categoryTypePrefix`, preferences, dispatcher; chat message-service routes, `checkMessagePermission`, `SendMessage`; trust-safety handlers and moderation; live-v2 reports; age gate; Android `UsCommentsSheet.kt`, `EngagementApi`, notifications screens, `UnreadBadge.kt`, chat screens and API.
- **LIVE, upload, creator studio (areas 10, 11, 12):** `app/live/*`, `features/live/*`, PostTube `LivePage.tsx`, Reels Live (`ReelsLiveScreen.tsx`, `liveStage.ts`, `LiveStageViews.tsx`), broadcast and encoder pages, `HubLivePage.tsx`; `features/upload/*` (`tokens.ts`, `fileRules.ts`, `useUploadStudio.ts`, `studioApi.ts`, steps), `app/create/post`, `app/posttube/upload`; Creator Hub (`hubNav.ts`, `hubApi.ts`, Overview, Library, Insights, Conversations pages), `app/monetization/*`; live-service-v2 routes and services (`lifecycle.go`, `live.go`, ingress); media-service upload validation, resumable, audio handler; post-service drafts, uploads, comments inbox; analytics-service creator and dashboard routes; monetization-service routes; Android `feature/live` (`LiveApi.kt`, `LiveScreens.kt`, `LiveChatViews.kt`), `feature/post` create hub, `ReelLength.kt`, `VideoGate.kt`, `CoverPicker.kt`, `ScheduledPostsScreen.kt`.

No application code, schema, config, migration or test was modified. No deploy, cloud call or credential was used.
