# Cloud agent brief — FEED production-readiness audit

Read `docs/production-readiness/agents/00_COMMON_RULES.md` first. Audit only; no code changes. Output: `docs/production-readiness/FEED_AUDIT.md` + `FEED_AUDIT_LOG.md` on branch `audit/feed-production-readiness`.

## Scope checklist (every item becomes a row in your feature matrix)
- Home feed (algorithm-ranked) vs following feed (chronological); For You–style ranking from watch time, engagement, creator interactions, user preferences.
- Pagination / infinite scroll with no jumps, duplicates or missing items; consistent ordering.
- Content types in the home feed: text, images, mixed media, feed-composer videos, PostTube long videos (as configured) — and confirm Reels are excluded by design.
- Engagement: likes, comments, shares, saves with real-time count updates and persistence.
- Refresh: pull-to-refresh, background refresh / "N new posts" pill, stale-content handling.
- Privacy & visibility: private accounts, blocks (both directions), mutes, deleted content, restricted/unlisted/members-only visibility, hidden authors and "not interested" feedback.
- Efficient queries & caching: indexes, Redis caches, Scylla access patterns, hydration cost.
- Duplicate prevention: duplicate timeline rows, near-duplicate content, idempotent event handling.

## Code map (verified 13 Oct 2026 by read-only exploration — confirm, do not trust blindly)
**feed-service** (`Architecture/services/feed-service`, port 8086, Postgres + Scylla keyspace `social_feed` QUORUM + Redis; all `/v1/feed` routes gated by internal key from the gateway; per-user rate limit 120/min `feed_rl:%s`):
- Routes `internal/http/handler.go:62-95`: `GET home` (Following = `home?following_only=true`; flags `circle_only`, `exclude_self`, `platform=postbook`, `feed_mode=chronological|ranked|shadow`), `GET delta`, `reels`, `flicks`, `videos`, `videos/:postId/related`, `watch`, `preference`, `feedback/authors`, `muted`; `POST preference|signal|feedback|hide/:postId|mute`; `DELETE hide/:postId|mute`. `GET debug` → 410.
- Home pipeline `internal/service/feed.go:280 GetHomeFeed`: Scylla `GetHomeTimeline/Before` → `resolveBlockedSet` (fail-closed) → hidden-author filter → circle/following filter → cold-start fill (`coldStartAllowed:250`, `countFromOthers:270`, post-service `/v1/posts/recent`) → `dropShortForm` → `filterMainFeedExcluded` → long-video tier → `ranker.Rank` → `HydratePosts` (`service/hydration.go:304`, post-service `POST /v1/posts/batch`, 502 `HYDRATION_FAILED`). Empty pages set `X-Feed-Empty-Reason`.
- Filters: `hidefilter.go`, `privacyfilter.go`, `processingfilter.go`, `keywordfilter.go`, `category.go`, `view_counts.go`, `feedback.go`, `control.go`, `reason.go`, `mutuals.go`, `subscriptions.go`.
- Ranking `internal/ranking/`: `ranker.go` (`Rank`, `RankRelated`, `ExcludeSeen`), `scorer.go:77 ScoreCandidates` (interest × recency × mediaBoost + momentum + proximity + quality + topic + seed + subscription 0.15 − penalties), `signals.go:156 LoadSignals` (Redis then Scylla fallback), `diversity.go`, `topical.go`; 20 ms ranker circuit breaker in `cmd/server/main.go`.
- Fan-out `feed.go:945 FanoutPost` (author timeline + own home; trusted → close friends; celeb authors from Postgres `celeb_authors` get no push; else followers ∪ connections from graph-service, 16 workers; channel subscribers for long video); also `FanoutQuestion:1544`, `FanoutRepost:1598`, `UndoRepostFanout:1653`.
- Scylla `internal/store/scylla/timelines.go` (schema `Architecture/docker/scylla/schema.cql`, `schema.prod.cql`; job `deploy/jobs/scylla-schema`): `home_timeline_by_user`, `author_timeline_by_author` (PK `((owner, bucket YYYYMM), ts)` DESC), `timeline_index_by_post`, `user_post_interactions` (TTL 7 d). Functions `AddToHomeTimeline:69`, `AddToAuthorTimeline:96`, `GetAuthorTimeline:407`, `DeleteHomeTimelineEntriesByAuthorForUser:490`, `GetAuthorTimelineMultiBucket:576`, `DeleteTimelineEntriesByAuthor:621`, `DeletePostFromTimelines:732` (ALLOW FILTERING fallback via `FEED_HF4_FALLBACK`).
- Postgres tables: `celeb_authors`, `event_dedup` (unused?), `user_interactions`, `viewer_media_prefs`, `post_impressions`, `user_preferences`, `feed_distribution`, `feed_hides`, `feed_mutes`, `hidden_authors`, `feed_feedback`, `feed_author_feedback` (`database/setup.sql` + `migrations/001..007`).
- Redis keys: `post:counters:%s:likes|comments`, `post:likers:%s`, `post:velocity:ranked`, `post:deleted:%s` (24 h), `feed:hydrate:%s:%s` (5 min), `feed_delta:%s:%s:%s` (10 s), `user:affinities:%s`, `user:topic_affinity:%s`, `user:completions:%s`, `user:mutual_follows:%s`, `user:subscribed_owners:%s`, `post:cqs:%s`, and others.
- Kafka `internal/events/consumer.go` (group `feed-service-group`, topic `social.events.v1`; also `qa-events`), `internal/consumers/channel_updates.go` (`atpost.channel.feed-inject`), `internal/purge/` (`identity.events.v1` → acks `platform.purge-acks.v1`). Lifecycle `post_lifecycle.go`, `unfollow_purge.go:107`.
- Delta `internal/http/handler_delta.go:28 FeedDelta` → `internal/service/delta.go:23 ComputeFeedDelta` (`deltaHome:80`, `deltaFollowing:149`).
- Tests: ~49 unit test files (fakes; no integration tests). List them and run them.
**post-service** (`Architecture/services/post-service`): `POST /v1/posts` (`handler.go` CreatePost; `service/post.go:914`, PostCreated via outbox ~1346), `POST /v1/posts/batch` (hydration), `GET /recent` (cold start), `GET /by-author/:authorId` (`post.go:1683`), delete/restore (`delete_post.go`, `service/my_uploads.go:233/256`, outbox emit `store/postgres/my_uploads.go:117`, `posts_lifecycle.go:85`), `PATCH /:postId/distribution`, `POST /v1/posts/internal/visibility`. Event constants `Architecture/shared/events/events.go`.
**graph-service**: follow/unfollow/block/mute routes `internal/http/handler.go:62-125`; `store/graph.go:1015 GetBlockedAndMuted`; outbox relay `store/outbox_relay.go`.
**analytics-service**: `POST /v1/analytics/events` (`internal/service/ingest.go`), engagement consumer `internal/consumers/engagement_consumer.go` (CQS → `post:cqs`), warmer `internal/personalization/warmer.go`, content views `POST /v1/analytics/internal/content-views`.
**search-service**: deletion/eligibility handling `internal/events/consumer.go:608-780`, DLQ replay `dlq_replay.go`.
**Web** (postbook-ui): `src/components/Feed.tsx`, `src/hooks/useFeedPosts.ts` (`useInfiniteQuery` key `["home-feed",…]`), `src/hooks/useFeedDelta.ts`, `src/hooks/feedPostFromCreate.ts`, `src/hooks/useFeedReaction.ts`, `src/components/PostCard.tsx`, `src/components/feed/*`, `src/components/CreatePortal.tsx`, `src/app/feed/page.tsx`.
**Android**: `mobile/android/core/feed/.../data/{FeedApi,FeedPagingSource,FeedPager,FeedRepository,HiddenPostsFilter}.kt` (+ tests `FeedContractTest`, `FeedPagingDeduplicationTest`, `FeedApiRequestTest`), `feature/feed/.../ui/{FeedScreen,FeedViewModel,FeedTab*}.kt`.
**Infra**: `deploy/services/feed-service/values-{prod,qa,staging}.yaml` (check staging has no SCYLLA_HOSTS → defaults to localhost), Terraform `infra/terraform/modules/{scylla,elasticache,msk}` (topics list `modules/msk/topics-job/variables.tf`).

## Suspected defects to VERIFY (from a quick read; each must be confirmed or refuted with evidence)
1. Kafka consumer uses `reader.ReadMessage` (auto-commit) and only logs handler errors (`events/consumer.go:57-69`) — comments claiming "Kafka redelivers" (~:408) would be false → silent loss of fan-out/distribution on transient errors; no DLQ.
2. Timeline writes not idempotent: `AddToHomeTimeline` uses a fresh time-UUID ts per write (`timelines.go:52-68`) → follow/unfollow/refollow and PostRestored duplicate rows; `event_dedup` table unused.
3. Celeb pull model never read: celebs skipped in fan-out, `GetAuthorTimeline` (`timelines.go:407`) has no callers, `GetHomeFeed` never merges celeb timelines → followers of celeb accounts never see their posts.
4. Ranked home pagination cursor = last item's `created_at` (`handler.go:~188`) although ranked order is not chronological → skips/duplicates (web dedupes with `uniqueFeedPosts`). Tube surfaces use `rankedPageMeta`/`keysetWindow` instead.
5. Delta: `deltaFollowing` == `deltaHome`; no block/mute/hidden filter in delta; only newest 201 rows; anchor format mismatches; group/channel/community delta tables not in feed-service migrations; web anchor = `posts[0].created_at` of a ranked list.
6. Delete/restore: upload-deleted removes only the current-month author bucket; PostRestored lacks channel id; tombstone TTL 24 h.
7. Visibility changes (`post.visibility_changed` defined in shared events) have no producer/consumer → timelines keep stale entries; blocks filtered at read only.
8. `user:affinities` written by two services (feed `pipeline/affinity.go:55` and analytics warmer).
9. Fan-out failures (follower/circle fetch) logged and continue with a partial set.
10. Feed-composer self-post visibility lag (~10 s) previously traced to the Kafka reader `MaxWait` default; check whether `MinBytes/MaxWait` were tuned.

## Targets for the scaling assessment
Feed generation (home) **< 200 ms p95 API, < 500 ms p99 generation**, engagement writes **< 100 ms p95**, sized for **millions of users and 500K+ concurrent** on the Reels side sharing the feed infrastructure; 99.9% availability. Evaluate fan-out-on-write cost for large follower counts, Scylla partition sizes per month bucket, hydration batch cost, Redis memory per user, and Kafka consumer lag. Propose concrete k6 scenarios (e.g. 10K concurrent users, 5K follows each, 50 req/s per pod) with pass criteria.

## E2E journeys for your matrix
- Create a post → appears in own feed, followers' feeds (if public) and ranked feed.
- Follow a creator → their content prioritised.
- Block a user → their content, comments and profile hidden both ways.
- Pagination: no duplicates, no missing items, consistent order across 10+ pages (ranked and following).
- Delete a post → removed from all feeds, delta counts and search.
- Concurrent likes/comments on one post → counts exact (no race), events counted once.
- Expired session and network interruption → graceful recovery and messaging.
- Unauthorised users cannot see private/deleted/restricted content via feed or hydration.
