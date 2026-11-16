# Feed: Stage 0 map

- **Audit date:** 2026-11-16
- **Backend and Android tree:** `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` @ `3c860874e`, with a dirty working tree (uncommitted admin-service and other changes; the audit reads the working tree as it is).
- **Web tree:** `C:\workspace\wt\pb-echo` (repo `postbook-ui`), branch `feat/echo-web` @ `ca96921`, clean. `docs/harness/AGENT-RULES.md` names `C:\workspace\wt\pb-integ` (`feat/tiktok-parity-web`) as the web tree, but that folder does not exist on this machine. Which web branch ships is an open question (see `state.json`).
- **Earlier audit:** `docs/production-readiness/FEED_AUDIT.md` (13 Oct 2026, at `d015cab3`) with a fix-status table. Nothing in it is taken as true here; the items it marks "fixed" were re-checked.

All backend paths are relative to `Architecture/services/` unless they start with another top-level folder.

## 1. Stack found in the repository

| Layer | What is there | Evidence |
|---|---|---|
| Backend language | Go 1.26 (workspace `Architecture/go.work`), Gin HTTP framework | `feed-service/internal/http/handler.go`, `go version` |
| Feed service | `feed-service` (69 Go files, 100 test files) | `feed-service/cmd/server/main.go` |
| Posts and engagement | `post-service` (318 Go files): posts, likes and reactions, comments, shares, reposts, bookmarks and saved | `post-service/internal/http/handler.go:88-398` |
| Follow, block, mute | `graph-service` | `graph-service/internal/http/handler.go:105-200` |
| Edge | `api-gateway` (token check, rate limits, internal-key stamping) | `api-gateway/cmd/server/main.go`, `pkg/internalroutes`, `pkg/tokenpolicy`, `pkg/edgelimits` |
| Datastores | PostgreSQL (feed control tables, posts), ScyllaDB (timelines, engagement counters), Redis (rate limits, caches, velocity) | `feed-service/database/migrations/`, `Architecture/docker/scylla/schema*.cql` |
| Events | Kafka topic `social.events.v1`, dead-letter `social.events.v1.dlq` | `feed-service/cmd/server/main.go:201`, `feed-service/internal/events/consumer.go:26` |
| Android | Kotlin, Jetpack Compose, Gradle modules `:feature:feed`, `:core:feed`, `:core:engagement` | `mobile/android/feature/feed/`, `mobile/android/core/feed/` |
| Web | Next.js (Bun), `src/app/feed/page.tsx`, `src/components/feed/` | `C:\workspace\wt\pb-echo` |
| Deployment | Helm chart `charts/atpost-service`, per-service values in `deploy/services/<service>/`, Argo CD, Terraform (`infra/terraform`, `infra/azure`), dev box (`infra/devbox`) | `deploy/`, `charts/`, `infra/` |
| CI | GitHub Actions: `go-ci.yml`, `integration-all.yml`, `android-native.yml`, `build-web.yml`, `lint.yml`, `security.yml`, `e2e.yml` | `.github/workflows/` |

## 2. Name mapping

| Harness name | Name in the code |
|---|---|
| feed (home feed) | `feed-service` `GET /v1/feed/home` (For You = `feed_mode=ranked`, Following = `chronological`); Android "Home" (`feature/feed`), web `/feed` |
| like | `POST /v1/posts/:postId/like`, `/reactions`, `/react` (post-service) |
| comment | `/v1/posts/:postId/comments`, `/v1/comments/*` (post-service) |
| share or repost | `POST /v1/posts/:postId/share`, `reposts_handler.go` (post-service) |
| save | `/v1/posts/:postId/bookmark`, `/v1/saved` (post-service) |
| follow, block, mute | `/v1/graph/*` (graph-service); feed-level mute and hide in `/v1/feed/mute`, `/v1/feed/hide` |
| reels, long video, notes (Echo) feeds | `/v1/feed/reels`, `/flicks`, `/videos`, `/watch`, `/notes/*` in the same service. These belong to the `reels`, `long-video` and `echo` runs and are out of scope here, except where they share code with home. |

## 3. Route table

Authentication, caller identity and rate limits for each route are in the completed route table at the top of `tracks/track-b-security.md`. In short: no `/v1/feed/*` route is public; the gateway verifies the token, strips client identity headers and sets them from the token; the public (no-token) routes are the post reads, graph follower/following/count reads and `GET /v1/links/:id`, listed and justified in that file.

### feed-service (`feed-service/internal/http/handler.go:79-125`)

The whole engine sits behind `RequireInternalKey` (line 79) when a key is configured, a request deadline (line 81) and `feedRateLimit` (line 87). The gateway stamps the internal key on `/v1/feed` (`api-gateway/pkg/internalroutes/internalroutes.go:400`).

| Method | Path | Handler | In scope |
|---|---|---|---|
| GET | `/v1/feed/home` | `GetHomeFeed` | yes |
| GET | `/v1/feed/delta` | `FeedDelta` (`handler_delta.go`) | yes |
| POST, GET | `/v1/feed/preference` | `SetPreference`, `GetPreference` | yes |
| POST | `/v1/feed/signal` | `PostSignal` | yes |
| POST | `/v1/feed/feedback` | `PostFeedback` | yes |
| GET | `/v1/feed/feedback/authors` | `GetMutedAuthors` | yes |
| POST, DELETE | `/v1/feed/hide/:postId` | `HidePost`, `UnhidePost` | yes |
| POST, DELETE | `/v1/feed/mute` | `MuteTarget`, `UnmuteTarget` | yes |
| GET | `/v1/feed/muted` | `GetMutedTargets` | yes |
| GET | `/v1/feed/internal/debug` | `DebugFeed` | yes (exposure check) |
| GET | `/v1/feed/debug` | `legacyDebugGone` | yes (exposure check) |
| GET | `/v1/feed/reels`, `/flicks`, `/videos`, `/videos/:postId/related`, `/watch` | reel and video feeds | no (other runs) |
| GET | `/v1/feed/notes/following`, `/notes/for-you` | Echo timelines | no (echo run) |
| GET | `/metrics`, health probes | `cmd/server/main.go:263` | yes |

### post-service routes used from a feed card (`post-service/internal/http/handler.go`)

| Method | Path | Handler | Line |
|---|---|---|---|
| POST | `/v1/posts` | `CreatePost` | 90 |
| POST | `/v1/posts/batch` | `BatchGetPosts` | 91 |
| GET | `/v1/posts/:postId` | `GetPost` | 116 |
| DELETE | `/v1/posts/:postId` | `DeletePost` | 120 |
| PATCH | `/v1/posts/:postId` | `UpdatePost` | 130 |
| POST, DELETE | `/v1/posts/:postId/reactions` | `React`, `Unreact` | 139-140 |
| POST | `/v1/posts/:postId/like` | `ToggleLike` | 144 |
| POST | `/v1/posts/:postId/react` | `ToggleReaction` | 259 |
| POST | `/v1/posts/:postId/share` | `SharePost` | 145 |
| POST, GET | `/v1/posts/:postId/comments` | `AddComment`, `ListComments` | 146-147 |
| POST, DELETE | `/v1/posts/:postId/bookmark` | `AddBookmark`, `RemoveBookmark` | 152-153 |
| POST, GET, DELETE | `/v1/saved`, `/v1/saved/:savedId` | saved items | 263-273 |
| reposts | see `reposts_handler.go`, `comment_routes.go` | | |
| POST | `/v1/posts/internal/*`, `/v1/internal/*` | service-to-service | 198-234 |

### graph-service routes used from the feed (`graph-service/internal/http/handler.go`)

| Method | Path | Handler | Line |
|---|---|---|---|
| POST | `/v1/graph/follow`, `/unfollow` | `Follow`, `Unfollow` | 112-113 |
| POST, DELETE | `/v1/graph/block` | `Block`, `Unblock` | 114-115 |
| POST, DELETE | `/v1/graph/mute` | `Mute`, `Unmute` | 132-133 |
| GET | `/v1/graph/blocked-and-muted` | `GetBlockedAndMuted` | 136 |
| GET, POST | `/v1/internal/graph/*` | internal lookups used by feed-service | 184-192 |

## 4. Tables

**PostgreSQL, feed-service** (`feed-service/database/migrations/001`-`010`): `user_preferences`, `event_dedup`, `celeb_authors`, `feed_distribution`, `feed_hides`, `feed_mutes`, `hidden_authors`, `feed_feedback`, `feed_author_feedback`, `post_impressions`, `user_interactions`, `viewer_media_prefs`, `echo_notes`, `echo_pull_authors`.

**ScyllaDB** (`Architecture/docker/scylla/schema.cql`, `schema.qa.cql`, `schema.prod.cql`):
- `social_feed`: `home_timeline_by_user`, `author_timeline_by_author`, `timeline_index_by_post`, `timeline_index_by_post_v2`, `user_post_interactions`, `notes_timeline`
- `social_engagement`: `post_likes`, `user_likes`, `reactions_by_post`, `post_counters`, `engagement_counters`, `comments_by_post`, `comment_likes`, `post_shares`, `user_shares`, `user_bookmarks`, `bookmark_check`

**PostgreSQL, post-service:** 82 migrations (`post-service/database/migrations/`), latest `084_echo_note_video.sql`. **graph-service:** 12 migrations, latest `012_drop_circles_and_close_friends.sql`.

## 5. Topics and workers

- `social.events.v1`: consumed by feed-service (`feed-service/internal/events/consumer.go`, `post_lifecycle.go`) for fan-out, delete, visibility change, unfollow purge.
- `social.events.v1.dlq`: dead-letter topic.
- `feed-service/internal/consumers/channel_updates.go`: channel updates consumer.
- `feed-service/internal/purge/`: account erasure.
- Commands: `feed-service/cmd/backfill`, `feed-service/cmd/timeline-maintenance`.

## 6. Clients

- **Android:** `mobile/android/feature/feed/` (`FeedScreen.kt`, `FeedViewModel.kt`, `FriendsFeedScreen.kt`, `HomeMoreSheet.kt`), `mobile/android/core/feed/`, `mobile/android/core/engagement/`. 19 unit-test files under `feature/feed/src/test`.
- **Web:** `src/app/feed/page.tsx`, `src/components/feed/` in `C:\workspace\wt\pb-echo`.

## 7. Dependencies of the home feed read

feed-service calls graph-service (follows, blocks, mutes), post-service (hydration), analytics-service (affinities), Redis and Scylla. Details and line references are in `tracks/track-d-performance.md` and `tracks/track-e-reliability.md`.
