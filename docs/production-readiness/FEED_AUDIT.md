# Feed module: production-readiness audit

- **Date:** 2026-10-13
- **Scope:** the Feed module only (home For You and Following, engagement on feed cards, refresh, privacy and visibility, caching, duplicate prevention). Brief: `docs/production-readiness/agents/01_FEED_AGENT.md`. Rules: `00_COMMON_RULES.md`.
- **Code audited:**
  - `rvreddy476/modernsmapp` at `codex/module-01-02-launch-safety` @ `d015cab3` (backend and Android)
  - `rvreddy476/postbook-ui` at `codex/apps-ui-refresh` @ `eb10bd3` (web)
- **Method:** I read the code with targeted searches and ran the safe checks listed in `FEED_AUDIT_LOG.md`. I changed no application code.
- **Legend:**
  - **Verified defect:** I saw it in code, or reproduced it in a test or command.
  - **Risk:** plausible, but it needs runtime validation.
  - **Unverified:** I could not check it in this sandbox. Each one names the check a person must run.

All paths are relative to `Architecture/services/` unless they start with `mobile/`, `deploy/`, `infra/`, `.github/` or `src/` (the web repo).

---

## 0. Fix status (updated 2026-10-13)

The audit below is unchanged: it describes the code as audited at `d015cab3` / `eb10bd3`. This section records what has been fixed since, on branch `claude/keen-heisenberg-gr4867` in both repos. Commit ids are in `rvreddy476/modernsmapp` unless marked "web" (`rvreddy476/postbook-ui`).

**Summary:** 25 of the 29 backlog items are fixed and 4 are partly fixed (F-03, F-11, F-13, F-14). Of the P0s, F-01 and F-02 are fixed and F-03 is fixed for the leak itself (the follow-up is defence in depth). The defects D-1 to D-19 are covered by the backlog items that cite them.

| ID | Status | Commits | Notes |
|---|---|---|---|
| F-01 | Fixed | `2abd1750` | Shared at-least-once consumer: retry, DLQ `social.events.v1.dlq`, dedup receipts, commit after a durable result; channel consumer commits after applying. No Kafka liveness probe was added; consumer lag and DLQ alerts (`433d840c`) cover a stalled consumer instead. |
| F-02 | Fixed | `2abd1750` | `FanoutPost` keeps `unlisted` and `private` off other timelines; hydration drops them for anyone but the author. |
| F-03 | Partly fixed | `2abd1750` | `GetPostsByIDs` applies the single-post rule, including the follow edge for `followers` posts (fails closed). The route is still reachable through the gateway; with the rule applied it no longer leaks, but moving it internal-only was not done. |
| F-04 | Fixed | `476b42cd`, `ece8b877` | Deterministic `scylla.TimelineTS` key, so re-writes upsert. Existing duplicates are removed by `cmd/timeline-maintenance`, which has **not been run** yet. |
| F-05 | Fixed | `476b42cd` | Page cut before ranking (`homeWindow`), opaque keyset cursor; a 300-row walk test serves each post once. |
| F-06 | Fixed | `98362b9e`; web `34e95b2` | Delta runs the page filters; Following is its own count; `newest_anchor` is a timestamp; bad anchor → 400. Web anchors on the newest post shown. |
| F-07 | Fixed | `98362b9e` | Group, channel, community and space deltas removed from feed-service. |
| F-08 | Fixed | `dd28cec8` | post-service emits `post.visibility_changed` in the edit transaction; feed removes the post from timelines and fans it out again under the new visibility. |
| F-09 | Fixed | web `34e95b2` | "Trusted" removed from both composers. |
| F-10 | Fixed | `f63a190e` | For You sends `feed_mode=ranked`; Following and Friends send `chronological`. Not compiled (no Android SDK). |
| F-11 | Partly fixed | `98362b9e` | A failed follower page or write now fails the message so it is retried (writes are idempotent). Followers are still paged by offset; a keyset endpoint in graph-service was not added. |
| F-12 | Fixed | `dd28cec8` | LWT `IF NOT EXISTS` / `IF EXISTS` on react/unreact; outbox written inside the reaction transaction. The L4 load test was not run. |
| F-13 | Partly fixed | `433d840c` | Prometheus metrics (fan-out, ranker fallback, hydration failures, page size, consumer lag, DLQ) and alerts as a `PrometheusRule` in the Helm values. No OTEL tracer init and no dashboards. |
| F-14 | Partly fixed | `433d840c` | Automatic celeb flag at `FEED_CELEB_FOLLOWER_THRESHOLD` and pull-merge in `GetHomeFeed` behind `FEED_CELEB_PULL` (both off by default). Fan-out was not moved to a sharded topic. |
| F-15 | Fixed | `98362b9e` |  |
| F-16 | Fixed | `98362b9e` |  |
| F-17 | Fixed | `2abd1750` |  |
| F-18 | Fixed | `98362b9e` |  |
| F-19 | Fixed | `dd28cec8` |  |
| F-20 | Fixed | `98362b9e` |  |
| F-21 | Fixed | `98362b9e` | Boot-time affinity pipeline deleted; analytics-service is the only writer. |
| F-22 | Fixed | `f63a190e`, `d5b128fe` | Pull-to-refresh, and an "N new posts" pill on For You and Following. Not compiled (no Android SDK). |
| F-23 | Fixed | `433d840c` | The staging secret must hold the keys the values reference. |
| F-24 | Fixed | `ece8b877` | The scan fallback is off unless `FEED_HF4_FALLBACK=true`, and `"false"` in prod, QA and staging values. Rows written before the index existed stay unindexed until `timeline-maintenance --apply` runs. |
| F-25 | Fixed | `dd28cec8` | `PostUnreacted` decrements velocity, floored at 0. |
| F-26 | Fixed | `2abd1750` | `MinBytes: 1`, `MaxWait: 500ms`. |
| F-27 | Fixed | `ece8b877` | Retention decided: **360 days** from `created_at` (`USING TTL` on every write plus `default_time_to_live`). Rows written before need `timeline-maintenance --apply`. |
| F-28 | Fixed | `ece8b877` | `timeline_index_by_post_v2`, partitioned `(post_id, shard)` with 16 shards. Reads include the legacy table until `FEED_INDEX_LEGACY_READ=false`. |
| F-29 | Fixed | `50bb7782` | Out-of-network posts (velocity first, then newest) fill `FEED_DISCOVERY_SHARE` (0.2) of ranked For You pages. No topic-based source; no offline metric or A/B test. |

**Checked in the sandbox:** feed-service, post-service, shared, search, notification and analytics build, vet and pass `go test -race`; `go build github.com/atpost/...` passes; the Scylla schema render tests pass; web `bunx tsc --noEmit` is clean and the feed and hooks tests pass.

**Still Unverified (each needs a person to run it):**

1. Android: `./gradlew :core:model:testDebugUnitTest :core:feed:testDebugUnitTest :feature:feed:testDebugUnitTest`, then a device check of pull-to-refresh, ranked For You and the new-posts pill. Nothing Android was compiled.
2. Helm: `charts/atpost-service/render_test.sh` (needs Docker). It also enforces QA values = prod values.
3. `deploy/jobs/timeline-maintenance/run.sh qa <image>` dry run on QA, then `--apply`; confirm the `scylla` namespace network policy lets the Job reach port 9042.
4. Integration and load: the k6 scenarios L1–L4 in §8 and the E2E matrix in §11.

**Deploy order per environment:** `deploy/jobs/scylla-schema/apply.sh <env>` → deploy post-service and feed-service together → `timeline-maintenance` dry run, then `--apply` → set `FEED_INDEX_LEGACY_READ: "false"` and redeploy → `run.sh <env> <image> --apply -- -truncate-legacy-index`. Turn on `FEED_CELEB_PULL` before ever setting `FEED_CELEB_FOLLOWER_THRESHOLD`.

**Open follow-ups:** move `/v1/posts/batch` behind service auth (F-03); keyset follower paging in graph-service (F-11); OTEL tracing and dashboards (F-13); sharded fan-out topic (F-14); topic-based discovery source and an A/B test (F-29).

---

## 1. Executive summary

**Readiness: Low, about 45%.** The read path is careful: block, mute, keyword, privacy and processing filters fail closed, and 236 unit tests pass. But the write path, which puts posts into feeds, has no delivery guarantee. Timeline rows get duplicated, ranked pagination is wrong, and two visibility gaps let restricted posts reach people they should not.

**Top 3 blockers**

1. **Fan-out is at-most-once and can stop silently (P0).**
   - `feed-service/internal/events/consumer.go:57-69` uses kafka-go `ReadMessage`. That call commits the offset *before* the handler runs (vendored `kafka-go/reader.go:792-804`), and handler errors are only logged. Any temporary Scylla, graph or Postgres error permanently loses the fan-out, delete, unfollow purge or distribution row.
   - On any read or commit error the loop `break`s. The consumer goroutine exits for good, and the pod stays Ready: health checks cover only Postgres, Redis and Scylla (`cmd/server/main.go:111-119`).
2. **Visibility leaks (P0).**
   - (a) `unlisted` posts are fanned out to every follower's home timeline (`service/feed.go:946 FanoutPost` only special-cases `trusted`). post-service's batch hydration then serves them (`post-service/internal/service/post.go:1895-1906` drops only `private`).
   - (b) `POST /v1/posts/batch` can be reached by any signed-in user through the gateway (`api-gateway/cmd/server/main.go:440`, `post-service/internal/http/handler.go:94`). It never checks the `followers` visibility. Any user who holds a post id can read a followers-only post that the single-post gate (`post.go:2936 viewerMayViewPost`) would refuse.
3. **Feed correctness: duplicates and skipped posts (P1).**
   - Timeline inserts are not idempotent: `store/scylla/timelines.go:69-71` uses `gocql.UUIDFromTime`, which increments the clock sequence on every call (`gocql/uuid.go:199`). A comment in the code records 7 copies of one post seen on 2026-09-09.
   - The ranked home cursor is the `created_at` of the *last ranked* item (`http/handler.go:188-190`), not a chronological watermark, so ranked pages skip and repeat posts.

**Immediate next steps**

1. Replace `ReadMessage` with `FetchMessage` → handle with bounded retry → write failures to the DLQ (`social.events.v1.dlq` already exists in `infra/terraform/modules/msk/topics-job/variables.tf:32`) → `CommitMessages`. Restart the reader on error. Export consumer lag and a liveness signal.
2. Make fan-out and batch hydration visibility-aware. Skip fan-out for `unlisted` and `private`. Apply `viewerMayViewPost` rules (including `followers`) in `GetPostsByIDs`.
3. Derive the timeline `ts` deterministically from `(post_id, created_at)` so writes become upserts. Move ranked home to the existing `keysetWindow`/`rankedPageMeta` cursor already used by the Tube surfaces.
4. Remove "Trusted" from the two web composers, or accept it on the server (§3 D-11).
5. Fix the `-race` failure in the test fixture `internal/service/related_test.go:76` so `go-ci.yml` passes.

---

## 2. Feature matrix

| Feature | Status | Evidence | Notes |
|---|---|---|---|
| Home feed, ranked ("For You") | Partial | `service/feed.go:280 GetHomeFeed`; `ranking/scorer.go:77 ScoreCandidates`; `ranking/ranker.go` (20 ms timeout → chronological fallback, `feed.go:451-455`) | Ranks only a window of up to 2.5× limit (max 200) of the user's *own push timeline* (`feed.go:296-301`). There is no out-of-network candidate source except cold start (`/v1/posts/recent`, newest public posts only). Web asks for `ranked` (`src/components/Feed.tsx:74`). **Android never sends `feed_mode`** (`mobile/android/core/feed/.../FeedApi.kt:47-55`), so the server falls back to the saved preference, default `chronological` (`feed.go:851-861`). Android "For You" is therefore chronological for every user who never saved a preference, and Android has no call to `/v1/feed/preference`. |
| Following feed (chronological) | Partial | `feed.go:383-394` (`following_only`, fails closed); `coldStartAllowed:250` stops backfill | Correct filter. It only *filters* the push timeline, so a celeb (pull-model) author never appears (latent, see D-3). Android tab `FeedTab.FOLLOWING` uses it. **Web has no Following tab**: `Feed.tsx` mounts only ranked. |
| Ranking signals: watch time, engagement, creator interaction, preferences | Partial | `ranking/signals.go:156 LoadSignals` (affinities, author feedback, media prefs, velocity, CQS, topic affinity, completions, mutuals, subscriptions); analytics warmer `analytics-service/internal/personalization/warmer.go` | Signals exist. `user:affinities` has two writers (D-8). `post:counters:*:likes` velocity input is inflated (D-15). There is no offline evaluation or A/B harness. |
| Pagination / infinite scroll | **Broken** (ranked); Partial (chronological) | Ranked: `http/handler.go:188-190` cursor = last ranked item's `created_at` (D-4). Chronological: `store/scylla/timelines.go:352-358` rebuilds the cursor as `UUIDFromTime(before)` with a fresh clock sequence | Web dedupes by id (`src/components/feed/feedPresentation.ts:9 uniqueFeedPosts`). Android dedupes by id (`core/feed/.../FeedPagingSource.kt:99,116`). Dedup hides duplicates but cannot recover *skipped* posts. Web uses a "Load more posts" button (`Feed.tsx:270-295`), not infinite scroll. Android uses Paging 3 (page 15, prefetch 5). |
| Content types: text, images, mixed, polls | Complete (server) | Hydration `service/hydration.go:304 HydratePosts`; post-service batch | Rendering by `src/components/PostCard.tsx`, Android `FeedScreen.kt`. Rendering on device is Unverified. |
| Feed-composer videos stay normal posts | Complete (by test) | `service/shortform_mainfeed_test.go`; classification in `shared/postclassify/classify.go` | Founder decision respected. I did not re-verify the classifier itself. |
| PostTube long videos in home (as configured) | Complete | `filterMainFeedExcluded` (`feed.go:432`, P0-1), long-video tier `longvideo.go` (`feed.go:437-441, 462`), `platform=postbook` filter `handler.go:160-174` | The `platform` filter runs *after* trimming to the limit, so pages under-fill. Android does not send `platform`. |
| Reels excluded from home | Complete | `dropShortForm` (`feed.go:427`, also in delta `delta.go:131`); tests `shortform_mainfeed_test.go` | Matches the founder decision. |
| Likes | Partial | `post-service/internal/service/post.go:3094 ToggleReaction`; Postgres `reactions` UNIQUE(target_type,target_id,user_id) (`post-service/database/migrations/002_phase1_stories_reactions_saved.sql:43`) | Postgres row is race-safe. The displayed count comes from the Scylla counter, which uses read-then-increment (`post-service/internal/store/scylla/interactions.go:34-51`), so it can drift (D-14). The PostReacted outbox insert is outside the reaction transaction (`post.go:3149`). |
| Comments | Partial | Comment count overlaid from Postgres in batch (`post.go:1855 overlayCommentCounts`) | Not audited in depth (owned by post-service). |
| Shares / reposts | Partial | `FanoutRepost` `feed.go:1598`, `UndoRepostFanout:1653`; repost counts `post.go` batch | Same idempotency and at-most-once issues as posts (D-1, D-2). |
| Saves (bookmarks) | Complete (read) | `IsBookmarked` in batch (`post.go:1945-1950`) | Write path not audited. |
| Real-time count updates | Partial | Web `Feed.tsx:89-121 subscribeToPostUpdates` patches the `home-feed` cache | Android realtime: Unverified. Counts are otherwise frozen for up to 5 min by the hydration cache (`hydration.go:25`). |
| Pull-to-refresh | Partial | Web: `installFeedReturnRefresh` (`Feed.tsx:158`). Android: no PullToRefresh in `mobile/android/feature/feed` (only Tube screens) | Android home cannot be refreshed by gesture (D-12). |
| "N new posts" pill / background refresh | **Broken** (accuracy); Missing (Android) | `service/delta.go:23 ComputeFeedDelta`; web `src/hooks/useFeedDelta.ts`, anchor from `Feed.tsx:124-127`; Android `FeedApi.kt:64 getDelta` has no caller | See D-5. |
| Stale-content handling | Partial | Hydration cache 5 min plus tombstone and privacy, keyword and feedback re-check on cached rows (`hydration.go:330-372`) | Post-level visibility edits are not re-checked on cached rows (D-7). |
| Private accounts | Complete | `service/privacyfilter.go` (graph `/v1/internal/graph/can`, 3 s cache, fail-closed); post-service `canViewPosts` (`post.go:1882`) | Good. |
| Blocks, both directions | Complete (read-time) | `graph-service/internal/store/graph.go:1015-1021` (blocker ∪ blocked ∪ muted); `resolveBlockedSet` fail-closed `feed.go:1115`; cold-start also filtered `feed.go:415` | Timeline rows are never removed on block. Filtering is read-time only, which is acceptable. **The delta endpoint does not filter blocks** (D-5). |
| Mutes | Complete (read) | same set; `/v1/feed/mute` routes `handler.go:89-92` | Delta ignores mutes (D-5). |
| Deleted content | Partial | `events/post_lifecycle.go handlePostDeleted` (index delete, tombstone, cache drop); batch `deleted_at IS NULL` (`post-service/internal/store/postgres/posts.go:1579`) | Display is safe because batch is the durable gate. Timeline rows can survive (D-6). Delta still counts deleted posts until their rows go. |
| Restricted / unlisted / members-only | **Broken** | D-9 (unlisted fanned out), D-10 (batch skips `followers`) | Members-only is not represented in feed-service at all (no matches). Unverified whether any members-only *post* type can reach `PostCreated`. |
| Hidden authors (deactivated / deletion-scheduled) | Partial | `service/hidefilter.go:44-47` | **Fails open** on DB error (Risk R-6), unlike every other filter. |
| "Not interested" / author feedback | Complete | `service/feedback.go`; `feed_feedback`, `feed_author_feedback` tables; part of `resolveBlockedSet` | Good. |
| Efficient queries and caching | Partial | §6, §8 | About 8–10 serial downstream calls per home request. `ALLOW FILTERING` fallback enabled by default (`timelines.go:757-758`, `FEED_HF4_FALLBACK` unset in prod values). |
| Duplicate prevention: timeline rows | **Broken** | D-2 | |
| Duplicate prevention: near-duplicate content | Missing | `ranking/diversity.go` enforces author and type spacing only; no content hashing | Same post via repost plus original can appear on one page; dedup is by post id only. |
| Duplicate prevention: idempotent events | **Broken** | `event_dedup` table never read or written (`database/setup.sql:9`); D-1, D-2 | |

---

## 3. Verified defects

### Results for the 10 suspected defects in the brief

| # | Suspected defect | Verdict | Evidence |
|---|---|---|---|
| 1 | Kafka at-most-once, no DLQ, false "Kafka redelivers" comment | **Confirmed**, and worse | `events/consumer.go:57-69` (`ReadMessage`, log-only, `break` on read error); kafka-go v0.4.50 `reader.go:792-804` commits before returning; false comment at `consumer.go:408`. The file itself says so at `consumer.go:239-241` ("No retry exists"). Same pattern in `consumers/channel_updates.go:72-87`. Only `purge/purge.go:269-290` is at-least-once. |
| 2 | Timeline writes not idempotent; `event_dedup` unused | **Confirmed** | `timelines.go:52-90` (`ts := gocql.UUIDFromTime(createdAt)` at :71 and :98); gocql `uuid.go:199` `atomic.AddUint32(&clockSeq, 1)`; `event_dedup` has DDL only. The claim at `post_lifecycle.go:119-120` that "AddTo* upserts on the primary key" is false. |
| 3 | Celeb pull model never read | **Confirmed (latent)** | `GetAuthorTimeline` (`timelines.go:407`) has no caller; `GetHomeFeed` never merges author timelines; `FanoutPost` skips followers for celebs (`feed.go:983-1024`). **But** nothing ever writes `celeb_authors` (`SetCelebStatus`, `store/postgres/meta.go:35`, has no caller). Today every author is push-model. Effects: (a) any manual `is_celeb=true` row makes that author invisible to followers; (b) there is no protection against very large fan-outs (§8). |
| 4 | Ranked pagination cursor = last ranked item's `created_at` | **Confirmed** | `handler.go:188-190`; `feed.go:296-301, 451-471` (window = 2.5×limit chronological rows, ranked, trimmed to limit). Tube surfaces use `rankedPageParams`/`keysetWindow` (`handler.go:449-481`, test `service/ranked_pagination_test.go:9`). |
| 5 | Delta problems | **Confirmed (all sub-claims) plus 2 more** | `deltaFollowing` returns `deltaHome` (`delta.go:149-152`). No block, mute, hidden-author, privacy or feedback filter in `deltaHome` (`delta.go:80-145`). Only the newest 201 rows (`delta.go:81`); the cold-start count is silently clamped to 20 by post-service (`posts.go:1050`, `limit>50 → 20`). `newest_anchor` is a post id while the anchor is parsed as RFC3339 (`delta.go:25, 142`); web works around it (`useFeedDelta.ts:101-118`). `group_posts`/`channel_updates`/`community_posts` are not in feed-service migrations (only in the dev monolith `docker/database/02-app-db.sql`). Web anchor = `posts[0].created_at` of a *ranked* list (`Feed.tsx:124-127`). **Extra:** (a) group, channel and community deltas run no membership check, so they disclose a count and the newest post id to non-members (D-13); (b) a bad anchor returns 500, not 400 (`handler_delta.go`, the `ComputeFeedDelta` error maps to `INTERNAL_ERROR`). |
| 6 | Delete/restore gaps | **Confirmed (2 of 3); tombstone TTL refuted** | `UploadDeleted` deletes only the author row in the *current* month bucket (`consumer.go:597-603`, `now := time.Now()`) and no home rows. `PostRestored` has no channel id (`post_lifecycle.go:122-126`). The 24 h tombstone is fine: it only has to outlive the 5-min hydration cache, and batch filters `deleted_at IS NULL` (`posts.go:1579`). |
| 7 | `post.visibility_changed` has no producer or consumer | **Confirmed** | Constant only at `shared/events/events.go:186`. Partly mitigated: batch drops `private` and re-checks author privacy. Not mitigated for `unlisted`, `followers` or cached rows (D-9, D-10). |
| 8 | `user:affinities` written by two services | **Confirmed (impact latent)** | Writers: `feed-service/internal/pipeline/affinity.go:55` (HSET, 25 h TTL, runs **once at boot** `cmd/server/main.go:135-141`, despite the comment "daily runs") and `analytics-service/.../warmer.go:80` (DEL+HSET of squashed scores every 15 min). The feed writer reads `user_interactions`, which only `ComputeFromImpressions` fills, and that function has no caller. If the table is ever populated, raw `total_score` values will fight the analytics squashed scores every restart. |
| 9 | Fan-out fetch failures logged; continues with partial set | **Confirmed, and coarser than partial** | `feed.go:1013-1024`. `fetchFollowers` (`feed.go:1282-1330`) returns `nil` on *any* page error, so one failed page of 100 drops **all** followers and only circle members receive the post. Offset pagination over a live set can skip or repeat followers. |
| 10 | Kafka `MinBytes/MaxWait` untuned (≈10 s self-post lag) | **Refuted for the main consumer, still present elsewhere** | Main consumer: `MinBytes: 1, MaxWait: 500ms` (`consumer.go:30-54`). Channel feed-inject consumer still has `MinBytes: 10e3` and the default 10 s `MaxWait` (`consumers/channel_updates.go:49-56`). |

### Defect register (full detail)

**D-1 (P0) Fan-out consumer is at-most-once and can die silently.**
- **Files:** `feed-service/internal/events/consumer.go:57-69 Start`; `consumers/channel_updates.go:70-87`.
- **Reasoning:**
  - `ReadMessage` with a GroupID commits synchronously before returning (kafka-go `reader.go:798-801`; `CommitInterval` is 0).
  - `processMessage` errors are only logged. Every handler that returns an error is losing data: `handlePostCreated` → `RecordDistribution` (`consumer.go:407-410`), `FanoutPost`, `handlePostDeleted`, `handleUserUnfollowed`.
  - On any `ReadMessage` error, including a failed commit or a broker rebalance error, `Start` breaks and the goroutine ends. Nothing restarts it, and `/health` does not cover Kafka (`main.go:111-119`).
- **Impact:** posts never reach followers, deletions leave rows, unfollows leave rows. After one broker blip the pod keeps serving but never fans out again until it restarts. No metric shows it.

**D-2 (P1) Duplicate timeline rows.**
- **Files:** `store/scylla/timelines.go:69-90 AddToHomeTimeline`, `:96-114 AddToAuthorTimeline`.
- **Reasoning:** each insert gets a unique `ts`. These paths re-insert: follow backfill (`consumer.go:169+ handleUserFollowed` → `AddToHomeTimeline` for up to N author posts on every follow), refollow after unfollow, `PostRestored` (re-fanout), channel subscribe overlap, and any future redelivery once D-1 is fixed. The in-code comment records 7 live copies for one account on 2026-09-09.
- **Impact:** duplicate cards (hidden by client dedup), wasted window slots that shrink the ranked candidate pool, and growth of the reverse index.

**D-3 (P2, latent) Celeb pull path missing.** See row 3 above.

**D-4 (P1) Ranked home pagination skips and repeats posts.**
- **Files:** `http/handler.go:188-190`; `service/feed.go:296-301, 451-471`.
- **Repro by reasoning:**
  1. Page 1 reads the newest 50 rows (t50…t1), ranks them and returns 20, the last of which is, for example, t30.
  2. Page 2 reads rows older than t30.
  3. Rows t31–t50 that ranked below the cut are never served (skipped).
  4. Rows older than t30 that were served on page 1 come back on page 2 (duplicates).
- **Impact:** missing posts on web and Android whenever ranked mode is used.

**D-5 (P1) "N new posts" count is wrong.** See row 5.
- **Concrete outcomes:**
  - A blocked or muted author's posts inflate the pill.
  - `private` posts of followed authors (which fan-out still writes) are counted, then dropped at hydration, so the pill says "3 new posts" and refresh shows none.
  - Following uses the same count as Home.
  - The web anchor is a ranked item's time, so older-but-unseen posts are counted as new.

**D-6 (P2) Upload-deleted cleanup is incomplete.**
- **File:** `consumer.go:571-607`. The author row is deleted only if the post was created in the current month. Home rows are never deleted, only masked by the tombstone and the batch gate.
- **Impact:** window slots are wasted, and the delta counts deleted posts (deltaHome does not consult the tombstone).

**D-7 (P1) Post-level visibility edits never reach the feed.**
- There is no producer or consumer for `post.visibility_changed`. `post_edit.go` emits only `PostDistributionUpdated` (`post-service/internal/service/post_edit.go:397`).
- A post edited from `public` → `followers`/`private`/`unlisted` stays in every timeline.
- The cached hydration row (5 min) is served without a post-visibility re-check (`hydration.go:330-372` re-checks author privacy, not post visibility).

**D-8 (P2) Two writers of `user:affinities`.** See row 8.

**D-9 (P0) Unlisted posts appear in followers' home feeds.**
- **Files:**
  - `post-service/internal/service/post.go:1346` emits `PostCreated` for every non-scheduled post regardless of visibility.
  - `feed-service/internal/service/feed.go:946-1078 FanoutPost` only branches on `visibility == "trusted"`.
  - `post.go:1895-1906` (batch) drops only `private`.
  - `grep unlisted feed-service` → no matches.
- **Impact:** a creator's "link-only" post (`media_access.go:22` describes unlisted as link-only) is pushed to all followers. This breaks the YouTube "Unlisted" contract and the brief's visibility requirement.
- **Confirm with an integration test:** create an `unlisted` post as A, where B follows A. `GET /v1/feed/home` as B returns it.

**D-10 (P0) Batch hydration ignores `followers` visibility and is user-callable.**
- **Files:**
  - `post-service/internal/http/handler.go:94` (`POST /v1/posts/batch` in the public `/v1/posts` group).
  - Gateway proxies `/v1/posts` (`api-gateway/cmd/server/main.go:440`).
  - `post.go:1895-1901` comment: "`followers`/`circle` are trusted to be gated by the recipient-set the fanout produced".
- **Impact:**
  - (a) Any authenticated user who knows a post id (shared links, notifications, reposts, search) can fetch a followers-only post's full content with `POST /v1/posts/batch {"ids":["<id>"]}`.
  - (b) Within the feed, a non-follower who still holds a row reads it. This happens when the unfollow purge was lost to D-1, or for circle members who do not follow (`fetchCircleMembers` recipients, `feed.go:1001-1004`).
- **Repro (human, QA):** as user B (not following A), `curl -X POST https://api-qa.../v1/posts/batch -H 'Authorization: Bearer <B>' -d '{"ids":["<A followers-only post>"]}'`. Expected: empty. Code predicts: the post is returned.

**D-11 (P1) Web composers offer a visibility the server rejects.**
- `src/components/CreatePortal.tsx:112` and `src/app/create/post/page.tsx:48` offer `trusted`.
- post-service binding is `oneof=public followers private unlisted` (`post-service/internal/http/handler.go:405`; `post_edit.go:50`). Choosing "Trusted" returns 400.
- feed-service's `trusted` fan-out branch (`feed.go:962-981`) is unreachable.

**D-12 (P2) Android feed parity gaps.**
- No `feed_mode` is sent, so "For You" is chronological by default (`FeedApi.kt:47-55`; `feed.go:851-861`).
- `getDelta` is declared but unused (`FeedApi.kt:64`), so there is no new-posts pill.
- There is no pull-to-refresh in `feature/feed`.
- `platform` is not sent, so the `ShareToPostbook` filter is not applied on Android.

**D-13 (P1) Delta disclosure.** `GET /v1/feed/delta?feed_type=group&group_id=<private group>` runs `SELECT COUNT(*), newest id FROM group_posts …` with no membership check (`delta.go:155-175`, same for channel, community and space). If those tables are reachable from feed-service's DSN, this leaks activity and post ids of private groups. If they are not, the endpoint returns 500 for those types. Unverified which (§12).

**D-14 (P1) Like counter drift.**
- **File:** `post-service/internal/store/scylla/interactions.go:30-51 React` (SELECT then INSERT then `like_count + 1`, not atomic). Two concurrent toggles both see "no reaction" and both increment.
- Postgres rejects the second insert (UNIQUE), but the Scylla `React` runs only on the successful path. The remaining window: concurrent `Unreact`/`React` interleavings, and driver retries of the non-idempotent counter `UPDATE`.
- Displayed likes come from this counter (`post.go:1851 BatchGetCounts`). The PostReacted outbox insert is a separate statement after commit (`post.go:3149`), so the event can be lost while the reaction persists.

**D-15 (P2) Feed velocity counter only increments.**
- **File:** `events/consumer.go:478-500 handlePostReacted`. It INCRs `post:counters:%s:likes` for every `PostReacted`, and ToggleReaction emits on reaction-*type changes* too (`post.go:3141`, `isSet`). There is no decrement on un-react.
- **Effect:** velocity and trending inputs are inflated by type-switching.

**D-16 (P2) Per-user feed rate limit is inert behind the gateway.**
- `http/handler.go:722-731` skips the 120/min limiter when `X-Internal-Service-Key` matches. The gateway stamps that key on every `/v1/feed` request (`api-gateway/pkg/internalroutes/internalroutes.go:276-280`). Only the gateway's generic 60 req/s per user applies (`api-gateway/cmd/server/main.go:219-220`).
- Separately, `pipe.Expire` on every request (`handler.go:737`) extends the window, so a client that never pauses never resets.

**D-17 (P2) Internal error text returned to clients.** `handler.go:141, 212, 250` pass `err.Error()` into the 500 body. This includes graph and post-service URLs and statuses from `resolveBlockedSet` and `fetchFollowing` errors.

**D-18 (P2) CI `-race` job fails on a feed-service test fixture.**
- `internal/service/related_test.go:76` appends to `f.calls` from concurrent `httptest` handler goroutines.
- `go test -race ./internal/service/` failed in 2 of 2 runs (`TestRelated_PagingConsumesTheOrderingExactly`, `TestRelated_ReelSeedReturnsReels`).
- `.github/workflows/go-ci.yml:39` runs `go test -race` over `Architecture/...`, so this blocks the backend CI gate. This is a test bug, not a production race.

**D-19 (P2) Staging values cannot boot feed-service.**
- `deploy/services/feed-service/values-staging.yaml` has no POSTGRES_DSN, REDIS_*, KAFKA_* or SCYLLA_HOSTS. The externalSecret data only has a comment placeholder.
- `main.go` defaults `SCYLLA_HOSTS=localhost` and `KAFKA_BROKERS=localhost:9092`, and exits if the Postgres connect or ping fails (`main.go:53-62`).
- Runtime effect Unverified: check `kubectl -n atpost get pods -l app=feed-service` in staging.

---

## 4. Risks and architectural concerns

- **R-1, unbounded synchronous fan-out on one goroutine per partition.**
  - `FanoutPost` collects *all* followers into memory through sequential 100-row HTTP pages (`feed.go:1282-1330`), then writes 2 QUORUM inserts per recipient with 16 workers (`feed.go:1036-1078`). This runs inline in the Kafka loop, so one large author blocks every later event on that partition.
  - `social.events.v1` has 6 partitions (`msk/topics-job/variables.tf`), so at most 6 consumers across the HPA's max 6 pods (`values-prod.yaml:16-20`). With celeb mode never active (D-3) there is no ceiling.
- **R-2, reverse-index hot partition.** `timeline_index_by_post` is partitioned by `post_id` only (`docker/scylla/schema.prod.cql:37-44`). A post fanned out to 1M followers creates a 1M-row partition. `DeletePostFromTimelines` then deletes row by row, serially (`timelines.go:785-797`).
- **R-3, `ALLOW FILTERING` fallback is on by default in prod.** If an index read returns zero rows (index writes are best-effort, `timelines.go:84-89`), delete and reclassify run a cluster-wide scan at QUORUM (`timelines.go:756-778`). `FEED_HF4_FALLBACK` is not set in `values-prod.yaml`, so it defaults to on.
- **R-4, no TTL on `home_timeline_by_user`/`author_timeline_by_author`** (`schema.prod.cql:4-28`, unlike the 7-day and 90-day tables at :52, :246). Storage grows without bound. Month buckets bound the partition, but a heavy follower gets 10^5+ rows per month.
- **R-5, 8–10 serial downstream calls per home request.** In order:
  1. Scylla, up to 12 sequential bucket queries for sparse users (`timelines.go:339-347`)
  2. graph blocked-and-muted
  3. Postgres hidden authors, distribution, long-video tier, feedback
  4. Redis ranking
  5. post-service batch, which itself calls graph `can`, Scylla counts and Postgres
  6. trust-safety keywords
  7. graph `can` (privacy)
  8. analytics view counts
  9. graph reasons
  10. profile, media and channels render data

  The p95 < 200 ms target depends on every hop staying under about 15 ms. Nothing measures this (R-8).
- **R-6, hidden-author filter fails open** on a Postgres error (`hidefilter.go:44-47`). Deactivated or deletion-scheduled accounts' posts would show. Every other safety filter fails closed.
- **R-7, the hydration cache could cost a lot of Redis memory.** `feed:hydrate:{viewer}:{post}` stores a full hydrated JSON row per viewer per post for 5 minutes (`hydration.go:25-29`). Estimate: 20–50 posts per page × about 2 KB ≈ 40–100 KB per active viewer; 500K concurrent ≈ 20–50 GB. This is an **estimate** and must be measured (§12).
- **R-8, no feed-specific telemetry.** Only HTTP and DB-pool metrics (`main.go:105-106`). There are no metrics for consumer lag, fan-out duration or failures, ranker fallback rate, hydration failure rate, or empty-reason distribution. `OTEL_EXPORTER_OTLP_ENDPOINT` is set in values, but no tracer is initialised in feed-service (no `otel` import).
- **R-9, chronological cursor ties.** `queryHomeTimelineBucket` builds the `ts < ?` bound from `UUIDFromTime(before)` with a fresh clock sequence (`timelines.go:352-358`). Rows that share the cursor's millisecond may be repeated or skipped. Duplicate rows from D-2 share exactly that timestamp.
- **R-10, unfollow purge relies on a single delivery.** `consumer.go:239-241`. Combined with D-10, a lost purge leaves a non-follower able to read followers-only posts.
- **R-11, followers-only posts reach "connections".** Fan-out targets followers ∪ circle members (`feed.go:1001-1004`), and batch does not check `followers` (D-10). If a circle member does not follow the author, they see followers-only posts. Whether circle ⊆ followers is Unverified.
- **R-12, Scylla consistency and availability.** QUORUM reads and writes with RF 3 (`main.go:74`, `schema.prod.cql:2`) tolerate one replica down per DC. The fan-out loop has no retry or backoff for timeouts, so failed rows are logged and lost (`feed.go:1047-1049`).
- **R-13, cross-module note for PostTube.** `GetPostsByAuthor` lists `unlisted` posts to non-owners (`post-service/internal/service/post.go:1724`). This is out of Feed scope, flagged for the PostTube agent.

---

## 5. Missing functionality vs reference (Feed is a TikTok + YouTube hybrid)

| Reference feature | TikTok / YouTube name | State here |
|---|---|---|
| Out-of-network candidate generation for For You | TikTok "For You" retrieval; YouTube Home recommendations | **Missing.** Only the push timeline plus newest-public cold start (`feed.go:401-422`). There is no trending, topic, similar-user or embedding retrieval for users who follow someone. |
| Watch-time / dwell signal in ranking | TikTok completion rate; YouTube watch time | Partial. `user:completions` set exists (`signals.go`). Feed post dwell is reported by Android (`FeedDwellRuleTest`). Its use in home scoring is limited to completions; no dwell regression. |
| Session-level freshness and "seen" suppression | TikTok de-dup of already-watched; YouTube "watched" demotion | Partial. `user_post_interactions` (TTL 7 d) penalises interacted posts; no impression-based "seen" suppression for home. `post_impressions` table exists, but its use for home is not wired (`ComputeFromImpressions` has no caller). |
| Following feed that includes high-follower creators | TikTok Following; YouTube Subscriptions | Missing for celebs (D-3, latent). |
| Unlisted semantics | YouTube Unlisted | Broken (D-9). |
| Members-only content gating in feed | YouTube channel memberships | Missing in feed-service. |
| "Not interested" / "Don't recommend channel" | YouTube | Complete. |
| New-content pill | TikTok/YouTube refresh prompts | Broken accuracy (D-5); missing on Android (D-12). |
| Experimentation / ranking A/B | both | Missing. Only `feed_mode=shadow` logging (`feed.go:457-459`). |

---

## 6. Database and API gaps

**Scylla (`social_feed`)**

- `home_timeline_by_user` PK `((user_id,bucket),ts)`: `ts` is not derived from `post_id`, so there is no idempotent upsert (D-2). There is no TTL (R-4). Needed: a deterministic timeuuid (timestamp = `created_at`, clock/node bits from a `post_id` hash), or a clustering key `(created_at, post_id)`.
- `timeline_index_by_post` PK `((post_id), …)`: an unbounded partition for large fan-outs (R-2). Consider `((post_id, shard))` with a fixed shard count.
- Query `SELECT … FROM home_timeline_by_user WHERE post_id = ? ALLOW FILTERING` (`timelines.go:758`) has no supporting index by design. Turn the fallback off once the backfill (`cmd/backfill`) has been run in prod (Unverified that it has).

**Postgres (feed-service)**

- `event_dedup(event_id PK)` exists but nothing uses it. It is the natural home for idempotency once D-1 is fixed (`INSERT … ON CONFLICT DO NOTHING RETURNING`).
- `celeb_authors` has no writer (D-3).
- `user_interactions` has no populated writer (D-8).
- `post_impressions` writer exists (`store/postgres/meta.go:73`), but I found no retention or partitioning. The table grows without bound if impressions are recorded. Unverified write volume.
- `feed_distribution`, `feed_hides`, `feed_mutes`, `feed_feedback`, `feed_author_feedback` have suitable PKs and indexes (`database/migrations/002-007`).
- **Delta queries** on `group_posts`/`channel_updates`/`community_posts` (`delta.go:155-253`) depend on tables owned by other services (`group-service/database/setup.sql`, `channel-service/database/setup.sql`, `community-service/database/migrations/001_full_communities.sql`). That is a cross-service DB coupling, and there is no index proof for `(group_id, status, created_at)`.

**Postgres (post-service, the cold-start query)**

- `WHERE visibility='public' AND deleted_at IS NULL AND <approved> AND publish_at IS NULL ORDER BY created_at DESC LIMIT n` (`post-service/internal/store/postgres/posts.go:1057-1076`). The usable index is `idx_posts_not_deleted (created_at DESC) WHERE deleted_at IS NULL`. Visibility and approval are filtered after the scan; cheap while public posts dominate. Plan is Unverified (§12).

**API contract gaps**

- `GET /v1/feed/home`:
  - ranked `next_cursor` is wrong (D-4)
  - `limit` capped at 100 here but 50 on other surfaces (`handler.go:111` vs `:454`)
  - `meta.next_cursor` is decided from the pre-hydration item count, so a page that hydration or the platform filter shrinks still advertises more (correct behaviour, but clients see short pages)
  - errors leak internals (D-17)
- `GET /v1/feed/delta`: `newest_anchor` (post id) is not a valid `anchor` input (timestamp); `following` is identical to `home`; a bad anchor gives 500, not 400; there is no authorisation for group or community types (D-13).
- `POST /v1/posts/batch`: user-reachable and visibility-incomplete (D-10). It should be internal-only (service token) or apply the single-post gate.
- Web `CreatePostPayload.visibility` type includes `trusted` (`src/hooks/useFeedPosts.ts:16`), which the server does not accept (D-11).
- Error envelope: consistent `api.ErrorWithContext` with codes (`UNAUTHORIZED`, `INVALID_CURSOR`, `HYDRATION_FAILED` 502, `RATE_LIMITED` 429).

**Transactions**

- PostCreated is written to the outbox in the same transaction as the post (`post.go:1333-1352`). Good.
- PostReacted is not (`post.go:3149`, D-14).
- The feed side has no transaction boundary between `AddToHomeTimeline` and its index row (`timelines.go:84-89`, best-effort).

---

## 7. Tests

**Existing coverage: feed-service (49 files, all unit tests with fakes, no integration tests)**

- `internal/events/`: `channel_subscription_test.go`, `unfollow_purge_guard_test.go`
- `internal/http/`: `feedback_body_test.go`, `hydration_fallback_test.go`, `ranked_pagination_test.go` (Tube cursor only), `tube_chips_test.go`, `tube_narrowing_test.go`
- `internal/pipeline/`: `topics_test.go`
- `internal/purge/`: `eraser_test.go`, `purge_test.go`
- `internal/ranking/`: `author_penalty_test.go`, `cold_start_test.go`, `scorer_subscription_test.go`, `topical_test.go`
- `internal/service/`: `blocksafety_test.go`, `category_test.go`, `coldstart_narrowing_test.go`, `coldstart_own_posts_test.go`, `deleted_tombstone_test.go`, `distribution_failure_test.go`, `fanout_subscribers_test.go`, `feedback_test.go`, `feedsurface_safety_test.go`, `hidefilter_test.go`, `hydration_avatar_url_test.go`, `hydration_channels_test.go`, `hydration_controls_test.go`, `hydration_render_test.go`, `hydration_sound_test.go`, `keywordfilter_test.go`, `longvideo_fill_test.go`, `longvideo_test.go`, `muted_author_window_test.go`, `privacyfilter_test.go`, `processingfilter_test.go`, `ranked_pagination_test.go` (keysetWindow only), `reason_following_test.go`, `reason_test.go`, `reels_fill_test.go`, `reels_following_test.go`, `related_test.go`, `schedulefilter_test.go`, `shortform_mainfeed_test.go`, `tube_chips_test.go`, `unfollow_purge_test.go`, `view_counts_test.go`, `watch_following_test.go`, `watch_subscribed_test.go`
- `internal/store/scylla/`: `timelines_primary_key_test.go`
- **No tests in:** `internal/consumers`, `internal/store/postgres`, `cmd/*`. There is no test of `consumer.Start` error handling, of home ranked pagination across pages, or of `ComputeFeedDelta` filtering.

**Web:** `src/components/feed/__tests__/{chooseContentType,feedPresentation,feedReturnRefresh,postControls,removePostFromPages}.test.*`, `src/hooks/__tests__/{feedPostFromCreate,messageAffordance,muting,sentRequests}.test.ts`. There is no test for `useFeedDelta` or `useHomeFeed` pagination.

**Android:** `core/feed/src/test/.../{FeedApiRequestTest,FeedContractTest,FeedPagingDeduplicationTest,ChannelHandleTest,VideoThumbTest}.kt`, `feature/feed/src/test/.../{FeedHydrationTest,FeedPostViewerStartTest,FeedDwellRuleTest,FeedAutoplayRuleTest,FeedAutoplayResolutionTest,HomeMenuRowsTest,…}.kt`.

**Commands run** (full list in `FEED_AUDIT_LOG.md`)

| Check | Result |
|---|---|
| feed-service `go build ./...` / `go vet ./...` | pass / pass |
| feed-service `go test -count=1 ./...` | **236 passed, 0 failed** |
| feed-service `go test -race -count=1 ./...` (CI flags) | **FAILED**: data race in test fixture `related_test.go:76`; reproduced 2/2 (D-18). 6 other packages pass under `-race`. |
| post-service build/vet/test | pass; **615 passed, 15 skipped** (integration-gated) |
| graph-service build/vet/test | pass; **41 passed** |
| analytics-service build/vet/test | pass; **67 passed** |
| search-service build/vet/test | pass; **138 passed** |
| web `bun install` / `bunx tsc --noEmit` | pass / 0 errors |
| web `bunx bun test src/components/feed src/hooks/__tests__` | **47 passed, 0 failed** |
| web `bun run build` | pass (216 pages) |

**Not run, with reasons**

- **Go integration tests (`-tags integration`):** no Docker daemon in the sandbox. feed-service has none. Related post-service ones: `internal/events/unfollow_integration_test.go`, `internal/events/deletion_durability_integration_test.go`, `internal/http/post_restrictions_integration_test.go`. A human should run them from `Architecture/services/post-service`:
  ```
  docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=x postgis/postgis:16-3.4
  createdb -h localhost -U postgres post_it_test
  GOFLAGS=-mod=vendor <DSN env var named in the test file>=postgres://postgres:x@localhost:5432/post_it_test?sslmode=disable go test -tags integration ./internal/events/ ./internal/http/
  ```
- **Android unit tests:** no Android SDK. A human should run `cd mobile/android && ./gradlew --offline :core:feed:testDebugUnitTest :feature:feed:testDebugUnitTest`.
- **E2E `TestE2E_Social`** (`.github/workflows/e2e.yml:42`): needs the compose stack. I did not inspect whether it covers home feed.

**Required new tests**

1. `events/consumer_test.go`: a handler error must not commit the offset. Use a fake reader that asserts no `CommitMessages` on error, and assert that a DLQ write happens after N retries. A read error must restart, not exit.
2. `store/scylla` (needs Scylla, integration tag): `AddToHomeTimeline` twice for the same `(user, post)` gives exactly 1 row.
3. `service/home_ranked_pagination_test.go`: 200 timeline rows, ranked mode, walk pages until the cursor is empty. The union equals all eligible rows, with no duplicates.
4. `service/delta_test.go`: blocked, muted, private and deleted posts are not counted; `following` counts only followed authors; a bad anchor gives 400.
5. post-service `GetPostsByIDs` tests: a `followers` post is refused to a non-follower; an `unlisted` post is excluded from feed hydration (a new `context=feed` flag or a separate internal endpoint).
6. feed-service `FanoutPost`: `unlisted` and `private` are not fanned out; a follower fetch failure is returned, not swallowed.
7. Web: `useFeedDelta` anchor taken from max(`created_at`) of rendered posts, not `posts[0]`.
8. Android: `FeedApiRequestTest` asserts `feed_mode=ranked` on For You.
9. Fix `related_test.go` fixture with a mutex so `-race` passes.

---

## 8. Scaling assessment

**Targets (brief):** home feed < 200 ms p95 API and < 500 ms p99 generation; engagement writes < 100 ms p95; millions of users, 500K+ concurrent on the shared infrastructure; 99.9% availability.

**No target can be claimed as met.** There are no load-test results, and feed-service has no feed-specific latency or lag metrics (R-8).

**Bottlenecks named from code** (figures marked "est." are arithmetic estimates, not measurements)

1. **Fan-out on write, single goroutine per partition** (`events/consumer.go:57`, `service/feed.go:946`).
   - Follower fetch: ⌈followers/100⌉ sequential HTTP calls (`feed.go:1282-1330`); 100K followers = 1,000 calls before the first write.
   - Writes: 2 QUORUM inserts per recipient (`timelines.go:73-89`) over 16 workers.
   - At about 2 ms per insert (est.), that is ≈ 8K rows/s per event, so a 100K-follower post occupies the partition's consumer for ≈ 15–25 s (est.), and a 1M-follower post for minutes. All later events on that partition (other people's posts, deletes, unfollows) wait.
   - Kafka parallelism ceiling: 6 partitions (`msk/topics-job/variables.tf`), HPA max 6 (`values-prod.yaml:19`).
2. **Read amplification per home request.** Up to 12 sequential Scylla bucket queries for sparse timelines (`timelines.go:339-347`), plus about 8–10 serial HTTP and Postgres hops (R-5).
3. **Postgres pool:** 25 per pod × 6 pods = 150 connections against Aurora `app`, which is shared with post-service (`values-prod.yaml:37-39`). Unverified Aurora `max_connections`.
4. **Redis:** the hydration cache (R-7, est. 20–50 GB at 500K concurrent), plus per-user signal hashes (`user:affinities`, `user:mutual_follows`, `user:subscribed_owners`, `user:topic_affinity`, `user:completions`) and the per-post keys `post:cqs`, `post:counters`, `post:likers` (`post:likers` sets of all likers per post, 7-day TTL: unbounded per viral post).
5. **Scylla partitions:**
   - Home partition per user per month. Example: following 2,000 creators × 3 posts/day × 30 = 180K rows (~20 MB at ~110 B/row, est.).
   - Index partition per post = number of recipients (R-2).
6. **Engagement writes:** `ToggleReaction` = Postgres transaction (SELECT plus INSERT, UPDATE or DELETE) + Scylla read + insert + counter + Postgres count query + outbox insert, all sequential (`post.go:3094-3160`). That is about 6 round trips. < 100 ms p95 is plausible but unmeasured.

**Required infrastructure and changes before load tests can pass**

- At-least-once consumer with a separate fan-out worker pool or topic (`feed.fanout.v1`) partitioned by recipient shard, so one author cannot stall a partition.
- An activated celeb threshold (for example > 10K followers) with a pull-merge in `GetHomeFeed`.
- Streaming follower pagination with a keyset cursor.
- Partition count raised to at least the expected peak consumer count (24–48).
- Feed metrics: `feed_fanout_duration_seconds`, `feed_fanout_recipients_total{result}`, `feed_consumer_lag`, `feed_rank_fallback_total`, `feed_hydration_fail_total`, per-dependency client latency histograms; plus OTEL tracing.

**Capacity assumptions to validate:** an average user follows 200 accounts, a power user 5K; 2% of authors have > 10K followers; 10 feed requests per active user per session; 0.5 posts per active user per day.

**Load tests to run** (k6 against a QA stack with production-sized Scylla, Redis and Aurora; never against prod)

| # | Scenario | Data | Load | Pass criteria |
|---|---|---|---|---|
| L1 | Home read, ranked | 1M users; timelines seeded to 500 rows/user/month; 5K follows for 1% of users | ramp to 10K VUs, 50 req/s per pod, 6→N pods, 30 min | p95 < 200 ms, p99 < 500 ms, error < 0.1%, `feed_rank_fallback_total` < 1% |
| L2 | Home read, following + paging | same | 2K VUs each paging 10 pages | 0 duplicate and 0 missing ids vs the server-side oracle; p95 < 200 ms per page |
| L3 | Fan-out burst | 1 author with 100K followers, 1 with 1M (celeb mode on), plus 1K normal authors posting 1/s | 15 min | p95 PostCreated → follower-timeline < 5 s for normal authors; consumer lag < 10K messages; no partition stalled > 30 s |
| L4 | Engagement | 1 hot post | 5K VUs toggling like concurrently, 5 min | p95 < 100 ms; final Scylla `like_count` = Postgres `COUNT(*)` exactly; one PostReacted per net set |
| L5 | Delta polling | 500K simulated sessions at 1 poll / 10 min (web) | steady 1 h | p95 < 50 ms; Redis memory growth < X GB (set from L1 measurement) |
| L6 | Chaos | L1 + kill one Scylla node, then one Kafka broker, then graph-service 30 s | 30 min | feed availability ≥ 99.9% (graph outage → explicit 5xx by design); after broker kill, fan-out resumes without pod restart and loses no events (count PostCreated vs timeline rows) |

---

## 9. Prioritised backlog (P0–P3)

Every item is in Module **Feed** unless noted.

| ID | Pri | Issue | Evidence | Impact | Solution | Dependencies | Acceptance criteria | Required tests | Cx | Status |
|---|---|---|---|---|---|---|---|---|---|---|
| F-01 | P0 | Consumer at-most-once; dies on read error | D-1; `events/consumer.go:57-69`; `consumers/channel_updates.go:70-87` | Silent loss of fan-out, deletes, unfollow purges; permanent stall after a broker blip | `FetchMessage` → handler with bounded retry (3×, exponential) → on final failure write to `social.events.v1.dlq` → `CommitMessages`. Restart the reader loop with backoff on error. Add a Kafka liveness check (last successful fetch < 2 min) to `/health/live` | event dedup (F-03) so redelivery is safe | Injected Scylla error: message retried then DLQ'd and offset committed only after; broker restart: consumption resumes without pod restart | unit (fake reader), integration (Kafka testcontainer) | M | Fixed (`2abd1750`) |
| F-02 | P0 | Unlisted posts fanned out to followers | D-9; `feed.go:946`; `post.go:1346, 1895` | Privacy breach of link-only posts | In `FanoutPost`, skip follower, circle and subscriber legs unless visibility ∈ {public, followers}; author's own rows stay. Defence in depth: feed hydration (feed context) drops `unlisted` unless viewer = author | none | unlisted post visible only on author's own timeline and by direct link | unit (`FanoutPost`), post-service batch test, E2E | S | Fixed (`2abd1750`) |
| F-03 | P0 | `/v1/posts/batch` ignores `followers` visibility and is public | D-10; `post-service/internal/http/handler.go:94`; `post.go:1895-1901` | Any user reads followers-only posts by id | Apply `viewerMayViewPost` (batched: one graph follow-set call per page) in `GetPostsByIDs`. Move the batch route behind service-token auth (internal only) | graph-service batch "follows" API (exists: `/v1/internal/graph/can`) | Non-follower gets an empty map for a followers post; feed latency regression < 10 ms p95 | unit, integration, security test (curl as non-follower) | M | Partly fixed (`2abd1750`) |
| F-04 | P1 | Duplicate timeline rows | D-2; `timelines.go:69-114` | Duplicate cards, reduced ranking pool, storage growth | Deterministic `ts`: `gocql.UUIDFromTime`-compatible timeuuid with clock seq and node = first 8 bytes of `post_id`; same for the index row. One-off job to dedupe existing partitions (keep min ts per post) | none | Inserting the same post twice yields 1 row; production duplicate count per sampled user = 0 | Scylla integration test | M | Fixed (`476b42cd`, `ece8b877`) |
| F-05 | P1 | Ranked home pagination skips and repeats | D-4; `handler.go:188-190` | Missing and duplicated posts | Reuse `keysetWindow` + `rankedPageMeta`: cursor = oldest chronological row of the *fetched window*; serve all window rows over consecutive pages, or rank per window and emit the unserved remainder first | F-04 | 10-page walk: union = all eligible rows, 0 dups (L2) | unit + L2 load check | M | Fixed (`476b42cd`) |
| F-06 | P1 | Delta counts are wrong; following = home; no filters | D-5; `delta.go:80-152` | Misleading "N new posts"; blocked authors' activity leaks through the count | Reuse `GetHomeFeed` pre-ranking pipeline (block, hidden, privacy, distribution, followingOnly) in delta; cap via Scylla `ts > anchor` query; return `newest_anchor` as RFC3339 | none | Pill count = number of new items on refresh in a table test of 6 scenarios | unit | M | Fixed (`98362b9e`; web `34e95b2`) |
| F-07 | P1 | Group, channel and community delta: no authorisation, foreign tables | D-13; `delta.go:155-253` | Private-group activity disclosure or 500s | Remove these types from feed-service, or call owning services with the viewer id (group-service etc. enforce membership) | owning services' count endpoints | Non-member gets 403/404; no cross-DB SQL in feed-service | unit + security | M | Fixed (`98362b9e`) |
| F-08 | P1 | Visibility edits not propagated | D-7 | Edited-to-private or followers posts stay visible in timelines and cache | post-service emits `post.visibility_changed` from `post_edit.go` via outbox; feed consumer drops `feed:hydrate:*:{post}` (via index owners) and deletes timeline rows when new visibility ∉ fan-out set | F-01, F-02 | Edit public→private: no other viewer sees it after ≤ 5 s | integration | M | Fixed (`dd28cec8`) |
| F-09 | P1 | Web composers offer "Trusted"; server rejects | D-11; `CreatePortal.tsx:112`; `create/post/page.tsx:48`; `post-service/internal/http/handler.go:405` | Post creation fails with 400 for users who choose it | Remove the option (feature retired per `post.go:2964-2970`) or re-add server support deliberately | product decision | Composer shows only server-accepted values; type `CreatePostPayload.visibility` matches | web unit test | S | Fixed (web `34e95b2`) |
| F-10 | P1 | Android For You is chronological | D-12; `FeedApi.kt:47-55`; `feed.go:851-861` | Ranking not delivered to Android users | Send `feed_mode=ranked` for `FeedTab.FOR_YOU` (after F-05) | F-05 | Request test asserts the param; server logs `X-Feed-Mode: ranked` | Android `FeedApiRequestTest` | S | Fixed (`f63a190e`) |
| F-11 | P1 | Fan-out drops all followers on one failed page; offset paging | D-9 row; `feed.go:1013-1024, 1282-1330` | Posts silently not delivered | Return an error (with F-01 retry) instead of continuing; stream followers with a keyset cursor from graph-service; fan out per page | F-01, graph-service keyset endpoint | Injected page-5 failure → message retried; all followers receive exactly 1 row | unit | M | Partly fixed (`98362b9e`) |
| F-12 | P1 | Like counter drift; PostReacted not transactional | D-14; `interactions.go:30-51`; `post.go:3149` (Module: post-service) | Wrong counts; lost notification and ranking events | Write the outbox inside the reaction transaction; derive the Scylla counter from the Postgres result (`isSet`, previous type) instead of read-before-write, or use LWT `INSERT … IF NOT EXISTS` before the counter update | none | L4: counts exact under 5K concurrent toggles | integration + L4 | M | Fixed (`dd28cec8`) |
| F-13 | P1 | No feed telemetry | R-8; `main.go:105-106` | Blind to lag, fallback, fan-out failures | Prometheus metrics listed in §8 plus OTEL tracer init; alerts: consumer lag > 10K for 5 min, fan-out failure rate > 1%, hydration 502 > 0.5% | none | Dashboards and alerts exist in `infra/terraform/modules/observability` | n/a | M | Partly fixed (`433d840c`) |
| F-14 | P1 | Unbounded inline fan-out; celeb mode unused | R-1, D-3; `feed.go:983-1078` | Partition stalls; Scylla write storms | Set `celeb_authors` from follower count (graph event or nightly job, threshold configurable); implement pull-merge of followed celeb author timelines in `GetHomeFeed` (`GetAuthorTimelineMultiBucket` exists); move recipient writes to a sharded fan-out topic | F-01, F-04 | L3 pass criteria | unit + L3 | L | Partly fixed (`433d840c`) |
| F-15 | P2 | Rate limiter inert behind gateway | D-16; `handler.go:722-731` | 120/min feed cap does not exist in prod | Rate-limit on `X-User-Id` even when the internal key is present (exempt only requests without `X-User-Id`); set EXPIRE only on first INCR | none | 121st request in a minute → 429 through the gateway | unit | S | Fixed (`98362b9e`) |
| F-16 | P2 | Internal errors in response bodies | D-17 | Info disclosure | Log `err`, return a generic message | none | No URL or host in any 5xx body | unit | S | Fixed (`98362b9e`) |
| F-17 | P2 | CI `-race` failure | D-18; `related_test.go:76` | Backend CI red | Guard `f.calls` with a mutex | none | `go test -race ./internal/service/` passes 10/10 | n/a | S | Fixed (`2abd1750`) |
| F-18 | P2 | UploadDeleted cleanup wrong bucket; no home rows | D-6; `consumer.go:571-607` | Stale rows, delta over-count | Call `DeletePostFromTimelines` as `PostDeleted` does | F-01 | No timeline rows for deleted upload | unit | S | Fixed (`98362b9e`) |
| F-19 | P2 | PostRestored loses channel subscribers | D-6; `post_lifecycle.go:122-126` | Restored long video missing from Subscriptions | Add `ChannelID` to `PostRestoredPayload` (post-service) | post-service | Subscriber timeline regains the row | unit | S | Fixed (`dd28cec8`) |
| F-20 | P2 | Hidden-author filter fails open | R-6; `hidefilter.go:44-47` | Deactivated users' posts shown during DB errors | Fail closed like the block filter | none | DB error → 5xx, not unfiltered page | unit | S | Fixed (`98362b9e`) |
| F-21 | P2 | Two `user:affinities` writers | D-8 | Ranking signal flapping when the table gets data | Delete the feed boot-time `AffinityPipeline.Run` and the unused `ComputeFromImpressions`; analytics owns the key | none | Single writer in code search | n/a | S | Fixed (`98362b9e`) |
| F-22 | P2 | Android no pull-to-refresh / new-posts pill | D-12 | Stale feed UX | `PullToRefreshBox` in `FeedScreen.kt`; wire `getDelta` after F-06 | F-06 | Manual QA plus UI test | Android UI test | M | Fixed (`f63a190e`, `d5b128fe`) |
| F-23 | P2 | Staging values incomplete | D-19 | No staging environment for feed | Mirror prod secret keys and env with staging hosts | infra | Staging pod Ready | n/a | S | Fixed (`433d840c`) |
| F-24 | P2 | `ALLOW FILTERING` fallback default on | R-3 | Cluster-wide scans | Run `cmd/backfill` index back-populate in prod, then set `FEED_HF4_FALLBACK=false` in values | backfill run | No `ALLOW FILTERING` in Scylla slow-query log | n/a | S | Fixed (`ece8b877`) |
| F-25 | P2 | Velocity INCR on type change, no decrement | D-15 | Inflated trending | Emit and consume `PostUnreacted`, or recompute velocity from post-service counts | F-12 | Velocity equals net likes in window | unit | S | Fixed (`dd28cec8`) |
| F-26 | P2 | Channel feed-inject consumer: MinBytes 10 KB, 10 s wait | `channel_updates.go:49-56` | 10 s lag on channel updates | `MinBytes:1, MaxWait:500ms` as the main consumer | none | Lag < 1 s on quiet topic | n/a | S | Fixed (`2abd1750`) |
| F-27 | P3 | Timelines have no TTL | R-4 | Storage growth | `default_time_to_live` (for example 400 days) on home and author timelines, or bucket-drop job | product retention decision | Table TTL set | n/a | S | Fixed (`ece8b877`) |
| F-28 | P3 | Index partition unbounded | R-2 | Hot partition for viral posts | Shard `timeline_index_by_post` by `(post_id, shard)` | F-04 migration | Max partition < 100 MB | Scylla test | M | Fixed (`ece8b877`) |
| F-29 | P3 | Out-of-network retrieval for For You | §5 | Weak discovery for followed users | Add trending and topic candidate sources mixed into the ranking window | F-05, F-13 | Offline metric plus A/B | unit + A/B | L | Fixed (`50bb7782`) |

---

## 10. Roadmap for this module

**Phase 0: delivery and privacy guarantees** (F-01, F-02, F-03, F-09, F-17)
- **Objectives:** no lost events, no visibility leaks, green CI.
- **Files:** `feed-service/internal/events/consumer.go`, `internal/consumers/channel_updates.go`, `internal/service/feed.go` (`FanoutPost`), `cmd/server/main.go` (health); `post-service/internal/service/post.go` (`GetPostsByIDs`), `internal/http/handler.go:94`; web `CreatePortal.tsx`, `app/create/post/page.tsx`; `feed-service/internal/service/related_test.go`.
- **DB/API:** batch route becomes internal (service token). No schema change.
- **Tests:** consumer retry/DLQ unit; batch visibility unit and security; `FanoutPost` visibility unit.
- **Perf/security validation:** curl as non-follower; injected-failure test on QA.
- **Definition of done:** all P0 acceptance criteria met; `go test -race` green.
- **Rollback:** revert the deployment (Helm rollback). The consumer change is backward compatible because offsets only advance later. If the batch route move breaks a caller, temporarily re-expose it with the visibility fix in place.

**Phase 1: idempotency and correct pages** (F-04, F-05, F-06, F-07, F-11, F-18, F-19, F-10)
- **Objectives:** exactly one row per (owner, post); ranked and chronological pages complete; accurate pill.
- **Files:** `store/scylla/timelines.go`, `http/handler.go`, `service/feed.go`, `service/delta.go`, `events/post_lifecycle.go`, Android `FeedApi.kt`, web `Feed.tsx` anchor.
- **DB:** deterministic `ts` (no schema change); one-off dedupe job (new `cmd/dedupe`, dry-run mode first); start using `event_dedup`.
- **Tests:** items 2–4, 6–8 in §7; L2.
- **Definition of done:** L2 shows 0 dups and 0 missing; delta table test passes.
- **Rollback:** the new `ts` scheme is read-compatible (same column type and order). The dedupe job writes a deleted-key log for restore.

**Phase 2: scale and observability** (F-12, F-13, F-14, F-24, F-28)
- **Objectives:** meet the §8 targets with evidence.
- **Files:** feed `pipeline/`, new fan-out worker, `infra/terraform/modules/msk/topics-job/variables.tf` (partitions, new topic), `deploy/services/feed-service/values-prod.yaml` (HPA, env), `infra/terraform/modules/observability`.
- **DB:** celeb threshold writer; sharded index table (new table plus dual-write, then cut over).
- **Tests:** L1, L3–L6.
- **Definition of done:** load-test pass criteria met on QA; alerts fire in a game day.
- **Rollback:** feature flags `FEED_CELEB_PULL`, `FEED_FANOUT_TOPIC`; dual-write keeps the old index readable.

**Phase 3: product parity** (F-15, F-16, F-20–F-23, F-25–F-27, F-29)
- **Objectives:** Android refresh and pill, rate limit, retention, discovery sources.
- **Definition of done:** per-item acceptance.
- **Rollback:** per-feature flags.

---

## 11. E2E acceptance test matrix

| Journey | Steps | Expected | Type | Current status |
|---|---|---|---|---|
| Create post → own, followers', ranked feed | A posts public; poll A's home; B (follows A) polls home ranked and following | Appears for A immediately (optimistic) and in timeline ≤ 2 s; for B ≤ 5 s; not for C (non-follower) except via ranked cold start only if C has no follows | integration + E2E | **Not run.** Code risk: D-1 (loss on error), D-9 (unlisted leaks) |
| Follow a creator → prioritised | B follows A; A's recent posts backfilled; rank B's feed | A's posts present (`handleUserFollowed` backfill) and ranked higher via affinity once warmed | integration | Not run. Backfill duplicates rows on refollow (D-2) |
| Block → hidden both ways | A blocks B; check A's and B's home, reels, videos, delta, comments, profile | No content either way; delta does not count | integration + security | Partially covered by unit `blocksafety_test.go`, `feedsurface_safety_test.go` (passed in the run of 236). **Delta not filtered (D-5): expected to fail.** Comments and profile not audited here. |
| Pagination ×10 pages | Seed 300 eligible posts; walk ranked and following | No dups, no missing, stable order | integration + load (L2) | Not run. **Ranked expected to fail (D-4).** Unit `ranked_pagination_test.go` covers Tube only. |
| Delete post → gone everywhere | A deletes; check B's home, delta, search | Gone from home immediately (tombstone plus batch), from search (search consumer handles `PostDeleted`, `search-service/internal/events/consumer.go:704`), delta no longer counts | integration | Not run. Unit `deleted_tombstone_test.go` passed. Delta still counts deleted rows (D-6) when the timeline delete failed. |
| Concurrent likes/comments | 1,000 concurrent toggles on one post | Counts exact; one event per net change | load (L4) | Not run. Risk D-14. |
| Expired session / network interruption | Expire JWT mid-scroll; drop network on page 3 | 401 → re-auth flow; page retry button (web "Retry loading more" `Feed.tsx:287-291`; Android `LoadState.Error` `FeedScreen.kt:489, 723`) | E2E (manual or Playwright / Espresso) | Not run. UI paths exist in code. |
| Unauthorised access via feed or hydration | Non-follower requests followers-only post via `/v1/posts/batch`; any user requests `unlisted` via home; deleted post via batch | All refused | security | Not run. **Expected to fail for followers-only (D-10) and unlisted (D-9).** Deleted is refused by code (`posts.go:1579`). |

---

## 12. Open questions and unverified items

1. **Do `group_posts` / `channel_updates` / `community_posts` exist in the database behind feed-service's prod `POSTGRES_DSN`?** If yes, D-13 is a live disclosure; if no, those delta types return 500. Check (read-only, QA first): `psql "$FEED_DSN" -c "\dt group_posts channel_updates community_posts"`. DSN is in AWS Secrets Manager `atpost/qa/feed-service` → key `postgres_dsn`.
2. **Production duplicate-row rate (D-2).** Sample: `cqlsh -e "SELECT post_id, count(*) FROM social_feed.home_timeline_by_user WHERE user_id=<id> AND bucket=202610 GROUP BY post_id;"` (Scylla supports GROUP BY on partition plus clustering prefix only; else export and count client-side).
3. **Has the HF4 index back-populate (`feed-service/cmd/backfill`) been run in QA and prod?** Check job history (`kubectl -n atpost get jobs | grep backfill`) before setting `FEED_HF4_FALLBACK=false`.
4. **Is circle membership a subset of followers?** This decides R-11. Check graph-service and identity-user-service semantics; query `SELECT count(*) FROM circle_members cm LEFT JOIN follows f … WHERE f IS NULL` (table names to be confirmed by the graph owner).
5. **Redis memory per active viewer (R-7).** On QA after L1: `redis-cli --bigkeys`, `redis-cli MEMORY USAGE feed:hydrate:<viewer>:<post>`, and `INFO memory`. ElastiCache console → `atpost-qa` cluster → BytesUsedForCache.
6. **Kafka consumer-group lag and partition count in prod.** `kafka-consumer-groups.sh --bootstrap-server <msk> --command-config scram.properties --describe --group feed-service-group`. Also MSK console → topics → `social.events.v1` partitions (expected 6).
7. **Aurora `max_connections` vs 150 feed + post-service pools.** RDS console → parameter group of Aurora `app`, or `SHOW max_connections;`.
8. **Android unit tests:** `cd mobile/android && ./gradlew --offline :core:feed:testDebugUnitTest :feature:feed:testDebugUnitTest` on a machine with the Android SDK.
9. **post-service integration tests** relevant to feed (unfollow, deletion durability): commands in §7.
10. **CloudFront / signed delivery for feed-card video:** not exercised here. Feed only hydrates metadata, and I found no feed-service code that proxies video bytes. The media URL signing path belongs to the media/PostTube audits. Verify in QA: open a feed video card and confirm the HLS manifest host is the CloudFront distribution and URLs or cookies are signed.
11. **Staging pod health (D-19):** `kubectl --context staging -n atpost get pods -l app.kubernetes.io/name=feed-service`.
12. **Does any members-only post type reach `PostCreated`?** If yes, it needs the same treatment as F-02. Check `post-service` create path for a members-only flag (`grep -rn members_only post-service/internal`).
13. **Does `TestE2E_Social` (`.github/workflows/e2e.yml:42`) assert home-feed delivery?** Read `Architecture/tools/integration` test sources and its last CI run.
