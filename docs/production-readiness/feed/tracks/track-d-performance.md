# Track D: performance and scale (home feed)

- **Date:** 16 Nov 2026
- **Trees read:** `C:\workspace\modernsmapp` (branch `codex/module-01-02-launch-safety`, working tree as found) and `C:\workspace\wt\pb-echo` (web).
- **Method:** read-only. Every code path below was read in the current working tree. **Nothing was run**: no tests, no benchmarks, no EXPLAIN, no load. So no finding here is VERIFIED-RUN, and no latency or capacity number in this file is a measurement.
- Backend paths are relative to `Architecture/services/` unless they start with another top-level folder.

## 1. Verdict for this track

**The home feed has never been load tested, so no statement about how many users it supports can be made.** There is no k6, vegeta, Locust or Gatling script for any feed, post or graph endpoint in the repository, and no recorded result (section 8). The earlier audit says the same thing about itself (`docs/production-readiness/FEED_AUDIT.md:401`: "No target can be claimed as met").

By reading alone, the read path has no query that scans a whole table and no unbounded IN list. The weak points are structural and are listed as findings:

- the event consumer handles one event at a time (D-1);
- the web client pulls every feed image and video through the application servers (D-3);
- one home page costs roughly 35 round trips from feed-service and well over 100 across the services behind it (section 2).

## 2. Round trips for one home page

Case counted: `GET /v1/feed/home?feed_mode=ranked&limit=20`, first page, a viewer who follows people, pull model on, no celebs followed, hydration cache cold, 60-second in-process caches warm. N = 20 posts, A = distinct authors on the page.

### 2.1 Calls made by feed-service itself

| Step | Store | Count | Evidence |
|---|---|---|---|
| Rate limit script | Redis | 1 | `feed-service/internal/http/handler.go:866` |
| Warm markers (mutual follows, subscribed owners) | Redis | 2 | `service/mutuals.go:116`, `service/subscriptions.go:237` |
| Home timeline read | Scylla | 1 to 12 (one per month bucket until `limit+10` rows are found) | `store/scylla/timelines.go:525-533` |
| Followed-celeb set | Redis | 1 (miss: +1 Redis, +1 Postgres, + graph pages, see D-6) | `service/celebpull.go:101-131` |
| Block and mute set | HTTP to graph | 1 | `service/feed.go:1442-1479` |
| Feed mutes, "don't recommend" authors | Postgres | 2 | `service/feed.go:1379`, `1391` |
| Hidden authors | Postgres | 1 | `service/hidefilter.go:21` |
| Distribution policy, plus cache write | Postgres 1, Redis 1 | 2 | `service/longvideo.go:324`, `356-370` |
| Long-video preference | Postgres | 1 | `service/longvideo.go:123` |
| Discovery: pool, following list, velocity, offered set, hidden authors, distribution (+cache), remember offered | Redis 5, Postgres 2, Redis 1 | 8 | `service/discovery.go:155-262` |
| Ranker signals | Redis | 1 pipeline (20 ms budget) | `ranking/signals.go:185-265`, `cmd/server/main.go:133` |
| Hydration cache read, tombstones | Redis | 1 to 2 | `service/hydration.go:877`, `service/deleted_tombstone.go:91` |
| Post batch | HTTP to post-service | 1 | `service/hydration.go:489-500` |
| Hydration cache write (after the response) | Redis | 1 | `service/hydration.go:912-953` |
| Keyword filters | HTTP to trust-safety | 0 or 1 (60 s per viewer per pod) | `service/keywordfilter.go:31-60` |
| Author privacy | HTTP to graph `/can` | 1 (3 s per viewer and author per pod) | `service/privacyfilter.go:102-145` |
| "Not interested" posts, "don't recommend" authors (second read) | Postgres | 2 | `service/feedback.go:253-257` |
| View counts | HTTP to analytics | 0 or 1 (60 s per post per pod) | `service/view_counts.go:72-124` |
| Reason for discovery rows | Redis | 0 or 1 | `service/reason.go:167-190` |
| Follow reasons | HTTP to graph `relationships/batch` | 1 (400 ms budget) | `service/reason.go:239-262` |
| Authors, media delivery, channels (in parallel) | HTTP to profile, media, post | 3 | `service/hydration.go:630-657` |

**Total from feed-service: about 33 to 37 round trips** (8 or 9 HTTP calls, 9 Postgres queries, 1 Scylla read in the normal case, 14 to 16 Redis calls). This number does not grow with N.

### 2.2 Calls made behind those

| Callee | What one call costs | Evidence |
|---|---|---|
| post-service `POST /v1/posts/batch` | 7 Postgres queries (posts, media, media state, repost counts, bookmarks, active reposts, engagement rows) + 1 hidden-author query + **2N Scylla point reads** (N counters, N reactions; 40 for a 20-post page) + 1 HTTP to graph `/can` + 0 or 1 HTTP to graph `relationships/batch` + 0 to 3 for polls and sounds | `post-service/internal/service/post_reads.go:345-437`, `internal/store/scylla/interactions.go:104-221`, `internal/service/comment_counts.go:126-151` |
| graph-service `/can` and `relationships/batch` | 10 sequential SQL statements each, plus a privacy read per author (D-5) | `graph-service/internal/store/graph.go:1151-1294`, `internal/service/follow_requests.go:217-233` |
| graph-service `blocked-and-muted` | 1 SQL statement (3-way UNION, all three arms indexed) | `graph-service/internal/store/graph.go:1073-1079` |
| media-service `POST /v1/media/batch` | 1 Postgres query + 1 HTTP batch call back to post-service for authorisation | `media-service/internal/service/media_batch.go:37-98`, `internal/delivery/authz.go:348-349` |

**End to end for N = 20: roughly 120 or more datastore and HTTP operations.** Only the 2N Scylla point reads in post-service grow with the page size. graph-service is asked three times about the same viewer and the same authors (once by post-service, twice by feed-service), about 30 SQL statements per page.

A chronological page skips discovery and the ranker (about 9 fewer). A page served fully from the hydration cache skips only the post batch call; every other call above still happens.

A brand-new account that follows nobody (the commonest viewer on launch day) reads 12 month buckets from Scylla one after another, then calls post-service `GET /v1/posts/recent` (`service/feed.go:511-513`), then hydrates as above.

## 3. Findings

```
ID:            D-1
Title:         feed-service handles social.events.v1 one event at a time per pod, so one follow (about 100 sequential Scylla writes) or one delete of a widely delivered post (one sequential delete per follower) holds up every like, comment, post and unfollow behind it on that partition.
Severity:      P1
Category:      performance
Evidence level: VERIFIED-READ for the structure; SUSPECTED for the lag it causes at launch load (confirm with load test L3 in section 8)
Evidence:
  - Shared consumer loop: FetchMessage, then processWithRetry inline, then the next message. No worker pool. Architecture/shared/kafka/consumer.go:172-206.
  - Per event: Redis EXISTS (dedupe), the handler, Redis SET, then a synchronous offset commit (no CommitInterval on the reader, consumer.go:133-140, 166).
  - A retry sleeps 1 s, 2 s, 4 s in the same loop (consumer.go:210 onward, MaxRetries 3).
  - UserFollowed backfill: up to 50 posts, AddToHomeTimeline for each in a plain for loop; each call is 2 Scylla writes at QUORUM. feed-service/internal/events/consumer.go:230, 289-294; store/scylla/timelines.go:153-177.
  - PostDeleted and a visibility change: one DELETE per follower row in a for loop, then 17 index deletes. store/scylla/timelines.go:955-991.
  - PostReacted: 4 separate Redis commands (not pipelined) + 1 Scylla write + a log line per like. events/consumer.go:577-612.
  - Fan-out for one post runs inside the same loop (16 writers, section 5).
  - Topic has 6 partitions in prod (infra/terraform/modules/msk/topics-job/variables.tf:62-66), feed-service runs 2 to 6 pods (deploy/services/feed-service/values-prod.yaml:15-20), so at most 6 events are in progress at once for the whole platform.
  - The consumer runs inside the API pods and the HPA scales on CPU only (values-prod.yaml:17-20); lag does not add pods.
How to reproduce: On a QA stack, publish 2,000 UserFollowed events in one minute (200 new users following 10 suggested accounts each) while 50 authors post. Watch atpost_feed_service_kafka_consumer_lag and the time from PostCreated to the row appearing in a follower's home_timeline_by_user.
Impact: At an assumed 1 to 3 ms per Scylla write, one follow costs 100 to 300 ms of consumer time, so one partition clears about 3 to 10 follows per second and the whole service about 20 to 60. A sign-up wave with follow suggestions can exceed that; new posts then reach followers late, and deletes and visibility changes are applied late (the per-viewer cache then serves the old row for up to 5 minutes, section 4). These numbers are arithmetic from the code, not measurements.
Fix: Smallest change first: (a) write the follow backfill and the delete rows through the existing pushChunkWith worker pool (service/fanout_chunks.go:180) instead of a for loop; (b) pipeline the Redis commands in handlePostReacted, handlePostUnreacted and handleCommentCreated. If L3 still shows lag, give the shared consumer a small per-partition worker count. No new infrastructure is needed.
Acceptance:    A unit test that feeds handleUserFollowed a timeline store whose AddToHomeTimeline takes 10 ms and asserts 50 backfill rows finish in under 100 ms (fails now: about 500 ms). Load test L3 passes its lag threshold.
Effort:        S for (a) and (b); M for consumer concurrency
```

```
ID:            D-2
Title:         No load test exists for any home feed endpoint and none has ever been run, so the 200 ms p95 target and every capacity figure are unproven.
Severity:      P1
Category:      tests
Evidence level: VERIFIED-READ (repository search); the absence of results outside the repository is UNVERIFIED
Evidence:
  - `git ls-files | grep -iE "k6|vegeta|locust|gatling|loadtest"` returns only dating-service/loadtest/*.js and rider-service/loadtest/*.js.
  - `git grep -l -iE "k6 run|k6/http|vegeta attack|locust"` returns the same six files.
  - `git grep "func Benchmark"` under feed-service, graph-service and post-service/internal/service returns nothing.
  - docs/production-readiness/FEED_AUDIT.md:401 "There are no load-test results"; line 37 "The L4 load test was not run"; lines 428-437 define L1 to L6 as work still to do; lines 520-522 "Not run".
  - The prod alert thresholds say they are "starting points; tune them against the k6 scenarios" (deploy/services/feed-service/values-prod.yaml, prometheusRule comment).
How to reproduce: Run the two git commands above.
Impact: Nobody knows the request rate at which /v1/feed/home passes 200 ms p95, when the Postgres pools (25 connections per pod) saturate, how much Redis memory an active viewer costs, or how far the consumer falls behind in a posting burst. A launch on this evidence is a launch on a guess.
Fix: Write and run the tests in section 8 against a QA stack sized like production. Keep the scripts in the repository and record the results in this folder.
Acceptance:    k6 scripts for L1 to L5 are committed, and a results file with p95, p99, error rate and consumer lag for each exists and meets the thresholds in section 8.
Effort:        L
```

```
ID:            D-3
Title:         The web feed card loads every image, video and avatar through /v1/media/<id>/serve, which the Next.js server proxies and streams, so media bytes pass through the application servers instead of going from the CDN to the browser.
Severity:      P1 (web only; Android is not affected)
Category:      performance
Evidence level: VERIFIED-READ
Evidence:
  - Card image, card video and avatar URLs: C:\workspace\wt\pb-echo\src\components\PostCard.tsx:338, 688, 734 (all `/v1/media/${id}/serve`).
  - `/v1/:path*` is rewritten to `/api/proxy/:path*`: next.config.ts:16-17.
  - The proxy follows redirects itself and streams the body to the browser: src/app/api/proxy/[...path]/route.ts:182-199, with the comment "The bytes are streamed to the browser from here, which is what keeps media same-origin for it."
  - media-service answers /serve with a 307 to the signed CDN URL after authorising the viewer: media-service/internal/http/handler.go:164-165, 632, 656-658, 693.
  - The feed response already carries signed CDN URLs per variant (`media[].variants`, feed-service/internal/service/hydration.go:353, 706), and Android uses them directly (mobile/android/feature/feed/.../FeedViewModel.kt:155-158). The web card does not.
How to reproduce: Open /feed on the web build with the browser network panel open. Every image and video request goes to the site's own origin under /v1/media/..., not to the CDN host (media.cleestudio.com in deploy/services/media-service/values-prod.yaml:102).
Impact: One feed page of 20 cards makes about 40 extra requests (20 media + 20 avatars), and each one costs a Next.js request, a gateway request, a media-service authorisation (which calls post-service) and a CDN fetch made by the Node server. API traffic for the web is multiplied many times over, a playing video holds a Node connection for its whole length, and the CDN's edge caching is lost to the browser. Web server bandwidth and CPU grow with media bytes, not with page views.
Fix: In PostCard, use the URLs the feed already returns: `media[].variants` for images, `playback_url` or `hls_url` for video, `author.avatar_url` for avatars, with the current /serve URL as the fallback when a variant is missing or has expired. No backend change.
Acceptance:    A component test that renders PostCard with a media item carrying `variants.medium_1080` and asserts the <img> src is that URL (fails now: the src is /v1/media/<id>/serve...). In the browser, a feed page load shows no /v1/media/*/serve requests for cards that carry variants.
Effort:        M
```

```
ID:            D-4
Title:         GET /v1/feed/delta sends up to 200 post ids to post-service in one batch call, post-service refuses more than 100, and the pill request then fails with 500.
Severity:      P2
Category:      functional
Evidence level: VERIFIED-READ
Evidence:
  - deltaHome reads deltaMaxCount+1 = 201 timeline rows and passes every row newer than the anchor to openableForPill, which calls HydratePosts: feed-service/internal/service/delta.go:24, 113-115, 185-193.
  - HydratePosts sends all uncached ids in one request, with no chunking: service/hydration.go:480-512.
  - post-service: "Maximum 100 IDs per request", HTTP 400: post-service/internal/http/post_reads_handler.go:320-323.
  - A non-200 from the batch is an error (hydration.go:511-513); the delta handler answers 500 (http/handler_delta.go, "Failed to compute feed delta").
  - The web polls home every 10 minutes and keeps the same anchor until the viewer taps the banner (pb-echo/src/hooks/useFeedDelta.ts:43, consumeNew). The hydration cache lives 5 minutes, so at each poll all the fresh rows are uncached again.
How to reproduce: Unit level: call ComputeFeedDelta for a viewer whose timeline has 150 rows newer than the anchor, with a post-service stub that enforces the 100-id cap. It returns an error. In the app: leave /feed open for a viewer whose follows post more than 100 times before they tap the banner.
Impact: For the viewers with the busiest feeds the "new posts" banner stops updating (the web hook swallows the error) and each poll logs an error. The same call with 100 ids costs post-service 200 Scylla reads.
Fix: Cap the rows openableForPill checks at 100 (the newest 100; set has_more when there were more), or chunk the batch call in HydratePosts into groups of 100.
Acceptance:    A test in feed-service/internal/service/delta_test.go with 150 fresh rows and a post-service stub that returns 400 above 100 ids: expects new_count 100 or more with has_more true and no error. Fails now.
Effort:        S
```

```
ID:            D-5
Title:         graph-service caches each user's privacy settings for 3 seconds and reads them one HTTP call per author from user-service, on a client with 2 idle connections and no circuit breaker; the relationship batch reads them one after another.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ for the code; the latency effect is SUSPECTED (confirm in L1 by tracing graph-service calls to user-service per home request)
Evidence:
  - PrivacyCacheTTL = 3 s: graph-service/internal/service/permissions.go:259.
  - One GET /v1/users/<id>/settings per uncached author; "user-service has no batch settings endpoint": permissions.go:290-296, 397-399.
  - `/can` fetches misses 10 at a time (permissions.go:312-394). `relationships/batch` calls fetchPrivacy in a plain loop, one author after another: graph-service/internal/service/graph.go:842-866 (line 856).
  - The client is `&http.Client{Timeout: 5 * time.Second}`: graph.go:104. That is Go's default transport, which keeps 2 idle connections per host, and it has no breaker.
  - feed-service gives the relationship batch 400 ms (feed-service/internal/service/reason.go:237-262).
  - One home page triggers `/can` twice and `relationships/batch` once for the same authors (section 2.2).
How to reproduce: Trace one home request for a page with 15 authors whose settings are not in Redis: graph-service makes up to 15 GETs to user-service, 10 in flight on a transport that keeps only 2 connections open.
Impact: user-service receives up to A requests per home page, repeated every 3 seconds per author. With many authors and a slow user-service the 400 ms reason lookup times out (rows lose "why am I seeing this", no error) and `/can` falls back to strict defaults, which hides posts from the page. Connection churn adds latency under concurrency.
Fix: (1) Build the client with shared httpclient.NewWithBreaker (20 idle connections per host and a breaker). (2) In GetRelationshipBatch call the existing fetchPrivacyBatch once instead of fetchPrivacy in a loop. (3) Longer term, the comment in permissions.go already names the right fix: invalidate on the settings write and lengthen the TTL.
Acceptance:    A graph-service test with a user-service stub that takes 50 ms per call: GetRelationshipBatch for 20 uncached targets returns in under 300 ms (fails now: about 1 s, serial).
Effort:        S
```

```
ID:            D-6
Title:         The viewer's following list is read on the request path by OFFSET paging, 100 ids per HTTP call, one call after another, and graph-service refuses any offset above 10,000.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ for the mechanism; whether any account can follow more than 10,000 people is UNVERIFIED (no follow cap was looked for)
Evidence:
  - graphIDList pages `GET /v1/graph/following/<id>?limit=100&offset=N` until a short page: feed-service/internal/service/fanout_chunks.go:292-306, 256-286.
  - Used on the request path by ranked Home (discovery, service/discovery.go:166; celeb pull, service/celebpull.go:114), by following_only (service/feed.go:493) and by the Following delta (service/delta.go).
  - Cached in Redis for 5 minutes, dropped on follow and unfollow: service/requestmemo.go:96-118, service/discovery.go:54, events/consumer.go (handleUserFollowed).
  - SQL: `ORDER BY created_at DESC LIMIT $2 OFFSET $3` with a NOT EXISTS on hidden_users: graph-service/internal/store/graph.go:475-486. Index idx_follows_follower_desc exists (graph-service/database/setup.sql:14).
  - Offset cap: graph-service/internal/http/handler.go:383-388 (OFFSET_TOO_LARGE above 10,000).
  - A keyset route already exists and is unused by feed-service: handler.go:705-717 (`paginate=cursor`).
How to reproduce: Seed a viewer following 5,000 accounts, clear feed:following:<viewer>, request ranked Home: 50 sequential graph calls before the page is cut. Seed 10,200 follows and request following_only=true: 500.
Impact: A cache miss costs F/100 sequential HTTP calls, each scanning further into the index. For a viewer following more than 10,100 accounts, following_only fails with 500, and discovery and celeb pull are skipped with only a log line. The same OFFSET paging drives fan-out (D-7).
Fix: Page with the existing cursor route (`paginate=cursor`), and raise the page size graph-service allows internal callers. No new endpoint is needed.
Acceptance:    A feed-service test with a graph stub that rejects `offset` above 10,000 and serves `cursor`: viewerFollowing returns all 10,200 ids (fails now with an error).
Effort:        S
```

```
ID:            D-7
Title:         Fan-out pages an author's followers by OFFSET while the list can change, so a follower who is shifted across a page boundary by another account's unfollow is skipped for that post.
Severity:      P3
Category:      reliability
Evidence level: SUSPECTED (follows from the query and the loop; confirm with a test that removes a follower between two pages)
Evidence:
  - graphLeg reads page after page by offset and saves the offset as progress: feed-service/internal/service/fanout_chunks.go:224-251.
  - The list is ordered `created_at DESC` (graph-service/internal/store/graph.go:323-335), so deleting a row from an earlier page moves every later row up by one.
How to reproduce: Author with 250 followers. Serve page 1 (offset 0), delete one follower from page 1, serve page 2 (offset 100): the follower who was at position 101 is now at 100 and is never returned.
Impact: Rare, one follower per concurrent unfollow, one post. The post still appears on the author's profile. Recorded because the audit asked for the fan-out loop to be read.
Fix: Same as D-6: use the cursor route for fan-out pages and save the cursor as progress.
Acceptance:    A fan-out test with a graph stub that drops one follower between page 1 and page 2: every remaining follower receives the row.
Effort:        S (done together with D-6)
```

```
ID:            D-8
Title:         The API gateway proxies every route through Go's default HTTP transport: 2 idle connections kept per upstream and no time limit on waiting for an upstream response.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ for the configuration; the cost under load is SUSPECTED (confirm in L1 by counting new TCP connections from gateway to feed-service)
Evidence:
  - `proxy.Transport = otelhttp.NewTransport(http.DefaultTransport)`: api-gateway/cmd/server/main.go:390.
  - Go's default transport sets MaxIdleConnsPerHost to 2 (net/http DefaultMaxIdleConnsPerHost) and has no ResponseHeaderTimeout.
  - The server deliberately sets no ReadTimeout or WriteTimeout: api-gateway/pkg/edgelimits/edgelimits.go:10, 68.
How to reproduce: Drive 200 concurrent requests through the gateway to /v1/feed/home and watch connection counts on the feed-service Service: connections are opened and closed continuously instead of being reused.
Impact: Above 2 concurrent requests to one upstream, most requests pay for a new TCP connection. It affects every module, not only the feed. With no response timeout the gateway relies on each upstream to give up (feed-service does, after 15 s).
Fix: Give the proxy its own transport: MaxIdleConnsPerHost around 100, IdleConnTimeout 90 s, ResponseHeaderTimeout a little above the longest upstream deadline, with an exception for streaming routes.
Acceptance:    A gateway test asserting the route's transport has MaxIdleConnsPerHost of at least 50 and a non-zero ResponseHeaderTimeout (fails now). In L1, new connections per second from gateway to feed-service stay near zero at steady load.
Effort:        S
```

```
ID:            D-9
Title:         post-service reads like counts and the viewer's reactions with two Scylla point reads per post (2N per page, at QUORUM), 16 at a time.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ
Evidence:
  - BatchGetCounts and BatchGetReactions loop over the ids and call GetCounts / GetReaction for each: post-service/internal/store/scylla/interactions.go:104-221, concurrency 16 (line 110).
  - Statements: `SELECT like_count, comment_count FROM post_counters WHERE post_id = ?` and `SELECT reaction FROM reactions_by_post WHERE post_id = ? AND user_id = ?` (interactions.go:96-104, 171-180). Both are primary-key reads.
  - Consistency QUORUM, 10 connections per host, 3 s timeout: post-service/cmd/server/main.go:140-148.
How to reproduce: Trace one POST /v1/posts/batch with 20 ids: 40 Scylla queries. With 100 ids (the delta pill, D-4): 200.
Impact: The largest N-dependent cost on the page. It is parallel, so it adds latency of about 3 rounds for 20 posts, but it multiplies Scylla read load by 40 per home page. A slow Scylla node degrades to zero counts, not an error (comment_counts.go:126-151), which is the right behaviour.
Fix: Read each table with one statement per page: `WHERE post_id IN ?` for post_counters, and `WHERE post_id IN ? AND user_id = ?` for reactions_by_post (post_id is the partition key and user_id the clustering key, so both are legal multi-partition reads capped at 100 keys).
Acceptance:    A store test that counts statements issued by BatchGetCounts for 20 ids and expects at most 2 (fails now: 20).
Effort:        S
```

```
ID:            D-10
Title:         Every like is a lightweight transaction on the post's single reactions_by_post partition, which has no shard and no size limit, so likes on one very popular post contend with each other.
Severity:      P2
Category:      performance
Evidence level: SUSPECTED (the statements are read; the contention needs load test L4, which the earlier audit records as never run)
Evidence:
  - `INSERT INTO reactions_by_post ... IF NOT EXISTS`, then a counter update: post-service/internal/store/scylla/interactions.go:44-66. Unlike: `DELETE ... IF EXISTS` (lines 73-88).
  - Table key is ((post_id), user_id): Architecture/docker/scylla/schema.cql:186-192. The sharded post_likes table (schema.cql:124-130) is not what this path writes.
  - The like path overall: Postgres post read, graph `/can`, Redis rate limit, Redis Lua toggle, Scylla LWT + counter, 2 to 3 Postgres writes, Redis DEL, outbox insert, async Kafka publish: post-service/internal/service/post_engagement.go:133-213.
  - feed-service adds the liker to the Redis set post:likers:<post> with a 7-day TTL and no size limit: feed-service/internal/events/consumer.go:595-599.
  - FEED_AUDIT.md:37 "The L4 load test was not run."
How to reproduce: Load test L4 (5,000 users toggling like on one post).
Impact: Paxos rounds on one partition run one at a time, so a post that goes viral can see like requests time out (3 s) while the rest of the system is healthy. One million likes on one post is one partition of a million rows and one Redis set of a million members.
Fix: Run L4 first. If it fails its threshold, add a shard to the partition key the way post_likes already does. Cap or drop the post:likers set (the ranker only asks "has this viewer interacted", which user_post_interactions already answers).
Acceptance:    L4 passes: p95 under 100 ms and the final like_count equals the number of reaction rows.
Effort:        M
```

```
ID:            D-11
Title:         The comment list sorts in ways the index cannot serve: "top" runs a COUNT subquery for every comment on the post and pages by OFFSET; "newest" orders by an expression before created_at.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ for the query text; SUSPECTED for the plan (run EXPLAIN ANALYZE on a post with 50,000 comments)
Evidence:
  - Top order: `(SELECT COUNT(*) FROM comment_reactions cr WHERE cr.comment_id = comments.id) DESC, reply_count DESC, created_at DESC, id DESC` with `LIMIT $2 OFFSET $n`: post-service/internal/store/postgres/comments.go:356-357, 397-407.
  - Newest order: `ORDER BY (pinned_at IS NOT NULL) DESC, created_at DESC LIMIT $2`: comments.go:408-417.
  - Index: `idx_comments_post ON comments (post_id, created_at DESC) WHERE is_deleted = FALSE` (post-service/database/setup.sql:98). It orders by created_at only.
  - Page size is capped at 50 (comments.go:379-381).
How to reproduce: Seed one post with 50,000 top-level comments; EXPLAIN ANALYZE both statements for page 1 and for a deep page.
Impact: On a post with very many comments, each page of "top" evaluates one subquery per comment and sorts them all; "newest" is expected to sort every remaining comment instead of walking the index. Normal posts (tens of comments) are unaffected.
Fix: Newest: fetch the pinned comment with its own small query and order the rest by created_at DESC alone, which the index serves. Top: order by the stored like_count column (already on the row) or keep a reaction_count column, and page by keyset.
Acceptance:    An integration test (CI, _test database) that seeds 20,000 comments and asserts the newest page uses an index scan with no Sort node and returns under 50 ms.
Effort:        M
```

```
ID:            D-12
Title:         The hydration cache stores a full copy of every post body per viewer in Redis for 5 minutes, saves only one of about 35 calls on a hit, and its memory cost has never been measured.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ for the design; memory use and hit rate are UNVERIFIED (needs `INFO memory` and MEMORY USAGE on a loaded QA Redis, and the Redis maxmemory policy, which was not read)
Evidence:
  - Key feed:hydrate:<viewer>:<post>, TTL 5 minutes, value is the whole HydratedPost JSON: feed-service/internal/service/hydration.go:25-30, 912-953.
  - On a full hit only the post-service batch call is skipped; block, keyword, privacy, feedback, view-count, reason, profile, media and channel calls still run: hydration.go:422-477.
  - The same Redis holds the rate limiter, consumer dedupe receipts, delete tombstones and fan-out progress (cmd/server/main.go:93, 164, 198-207).
  - FEED_AUDIT.md:534 lists the memory measurement as still to do.
How to reproduce: After load test L1, run `redis-cli INFO memory` and `MEMORY USAGE` on a sample of feed:hydrate keys.
Impact: Memory grows with (active viewers) x (posts seen in 5 minutes) x (row size). A viewer rarely sees the same post twice in 5 minutes, so most rows are written and never read. If Redis reaches its memory limit, what gets evicted depends on a policy this audit did not read; losing tombstones or dedupe receipts would be worse than losing cache rows.
Fix: Measure first (L1 and L5). If the hit rate is low, remove the per-viewer cache, or cache the viewer-independent part of a post once per post and keep only the viewer's reaction and bookmark per viewer.
Acceptance:    A recorded L1 result with Redis used memory per 1,000 active viewers and the hydration cache hit ratio, and a decision written next to it.
Effort:        S to measure; M to change
```

```
ID:            D-13
Title:         One home page repeats the same lookups: "don't recommend" authors twice, hidden authors twice, distribution policy twice, and three graph-service batch calls about the same viewer and authors.
Severity:      P3
Category:      performance
Evidence level: VERIFIED-READ
Evidence:
  - MutedAuthorIDs: feed-service/internal/service/feed.go:1391 and service/feedback.go:257 (the request memo holds the block set, not this set).
  - GetHiddenAuthorIDs and ExcludedFromMainFeed for the page and again for discovery: service/feed.go:451, 552 and service/discovery.go:231-235.
  - graph `/can` from post-service (post-service/internal/service/post_reads.go:438-455) and again from feed-service (service/privacyfilter.go:60), then `relationships/batch` (service/reason.go:254).
How to reproduce: Count statements for one request with a tracing Postgres stub.
Impact: About 4 Postgres queries and 20 graph SQL statements per page that return what the request already knows.
Fix: Keep the muted-author set in the request memo next to the block set. Drop feed-service's own `/can` call for rows that came fresh from the post batch (post-service has just applied the same rule) and keep it for cached rows only.
Acceptance:    A test asserting MutedAuthorIDs is called once per request.
Effort:        S
```

```
ID:            D-14
Title:         The discovery pool asks post-service for 100 recent posts and gets 20, and when the 2-minute pool key expires every request in flight refetches it at once.
Severity:      P3
Category:      performance
Evidence level: VERIFIED-READ
Evidence:
  - discoveryPoolSize = 100, TTL 2 minutes, plain GET then fetch then SET with no lock: feed-service/internal/service/discovery.go:49-50, 273-298.
  - post-service: `if limit <= 0 || limit > 50 { limit = 20 }`: post-service/internal/store/postgres/posts.go:1089-1091.
How to reproduce: Call GET /v1/posts/recent?limit=100: 20 rows come back.
Impact: For You draws its out-of-network share from 20 posts, which the 48-hour "already offered" set exhausts quickly, so discovery slots go empty. The refetch burst is small (one indexed query each).
Fix: Ask for 50 (the cap), or page twice. Refresh the pool under a Redis SET NX lock, as mutuals.go already does for its warmer.
Acceptance:    A test that the pool holds more than 20 posts when post-service has them.
Effort:        S
```

```
ID:            D-15
Title:         An edited post keeps its old text for up to 5 minutes for any viewer who has it in the hydration cache, because no event marks the row stale on edit.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ (no PostUpdated case in the dispatch); confirm with Track A whether an edit emits another event that reaches markStale
Evidence:
  - The dispatch switch handles PostCreated, PostDeleted, PostRestored, visibility, copyright, content type and distribution, and has no case for a post update: feed-service/internal/events/consumer.go (dispatch).
  - Stale markers are written only on visibility change (events/post_lifecycle.go:121) and similar; post-service drops only the acting viewer's own cache row (post-service/internal/service/post.go:540-548).
How to reproduce: Viewer B loads the feed. Author A edits the post. B refreshes within 5 minutes: old text.
Impact: Minor for text. It matters when the edit removes something the author wanted gone.
Fix: Have the consumer call markStale on the post-updated event (the mechanism exists and costs one Redis SET).
Acceptance:    A consumer test: a post-updated event sets post:stale:<id>.
Effort:        S
```

```
ID:            D-16
Title:         A viewer with a short or empty timeline costs 12 sequential Scylla reads per page, plus up to 12 more for every followed celeb, because the read walks one month bucket at a time.
Severity:      P3
Category:      performance
Evidence level: VERIFIED-READ
Evidence:
  - collectHomeTimeline and GetHomeTimelineBeforeToken loop up to maxTimelineBucketLookback = 12 months until `limit` rows are found: feed-service/internal/store/scylla/timelines.go:34, 389-397, 525-533.
  - Celeb pull does the same per celeb, 8 at a time, for up to 50 celebs: service/celebpull.go:82, 138-166; timelines.go:614-652.
How to reproduce: Request Home for a new account: 12 queries against 12 empty partitions.
Impact: Every new account pays this on every first page, and new accounts are most of the traffic on launch day. Each read is a cheap single-partition lookup, so this is latency (12 sequential QUORUM reads), not a scan. Worst case with 50 quiet celebs: 600 reads for one page.
Fix: Read the month buckets of one walk in parallel (bounded), or stop after the first two empty months for a viewer whose newest row is known to be older.
Acceptance:    A store test with a session stub that takes 5 ms per query: an empty-timeline read returns in under 20 ms (fails now: about 60 ms).
Effort:        S
```

```
ID:            D-17
Title:         Datastore clients are left on defaults that wait instead of failing fast: gocql has no retry or host-selection policy set, and Redis commands may wait 3 s and retry 3 times.
Severity:      P3
Category:      reliability
Evidence level: VERIFIED-READ for what is set; the effect of the gocql defaults is SUSPECTED (check which host policy gocql v1.7.0 applies when none is given)
Evidence:
  - gocql: Consistency QUORUM, NumConns 10, Timeout 3 s, nothing else: feed-service/cmd/server/main.go:74-83; post-service/cmd/server/main.go:140-148.
  - Redis: PoolSize 50, PoolTimeout 30 s, MaxRetries 3, read and write timeouts left at the library default: Architecture/shared/transport/redis.go:21-27.
  - Only some Redis calls carry their own short timeout (200 ms on the rate limiter, the cache MGETs and the reason lookup; 20 ms on the ranker). The warm markers, the celeb and following caches and the discovery reads use the 15 s request context: service/mutuals.go:116, service/celebpull.go:104, service/discovery.go:176-190.
How to reproduce: Add latency to Redis in a QA stack and request Home.
Impact: When Redis is slow rather than down, Home requests stall for seconds on optional steps instead of skipping them. Without token-aware routing every Scylla query takes an extra hop through a coordinator.
Fix: Set a token-aware host policy on the gocql cluster config. Give the Redis client explicit ReadTimeout and WriteTimeout of a few hundred milliseconds and MaxRetries 1.
Acceptance:    A test asserting the Redis options have ReadTimeout at or below 500 ms; a feed-service test with a Redis stub that hangs, asserting Home still answers within 1 s.
Effort:        S
```

## 4. Caches: lifetime, invalidation, and who can see what

| Cache | Key | Lifetime | Dropped early by | Per viewer? | Evidence |
|---|---|---|---|---|---|
| Hydrated post | Redis `feed:hydrate:<viewer>:<post>` | 5 min | delete tombstone, stale marker, viewer's own like | yes | `service/hydration.go:25-30`, `service/deleted_tombstone.go:78-103` |
| Delete tombstone | Redis `post:deleted:<post>` | 24 h | n/a | no (a fact about the post) | `events/post_lifecycle.go:55` |
| Stale marker | Redis `post:stale:<post>` | 10 min | n/a | no | `service/deleted_tombstone.go:22` |
| Author privacy answer | in process, `viewer|author` | 3 s, max 50,000 entries | n/a | yes | `service/privacyfilter.go:40-49, 147` |
| Privacy settings (graph-service) | Redis `privacy:<user>` | 3 s | n/a | no | `graph-service/internal/service/permissions.go:259-287` |
| Keyword filters | in process, per viewer | 60 s, max 100,000 entries | n/a | yes | `service/keywordfilter.go:31-33` |
| View counts | in process, per post | 60 s, max 20,000 entries | n/a | no (a public number) | `service/view_counts.go:36-44` |
| Following list | Redis `feed:following:<viewer>` | 5 min | follow and unfollow events | yes | `service/requestmemo.go:96-118`, `service/discovery.go:54` |
| Connections list | Redis `feed:connections:<viewer>` | 1 min | nothing | yes | `service/requestmemo.go:35` |
| Followed celebs | Redis `feed:followed_celebs:<viewer>` | 5 min | follow and unfollow events | yes | `service/celebpull.go:87-131` |
| Mutual follows | Redis `user:mutual_follows:<viewer>` | 6 h | nothing (ranking weight only) | yes | `service/mutuals.go:53` |
| Discovery pool | Redis `feed:discovery:pool` | 2 min | nothing | no (ids of public posts only; each is hydrated per viewer) | `service/discovery.go:47-50, 273-298` |
| Main-feed policy | Redis `feed:mainfeed:<post>` | 24 h | rewritten on every read | no | `service/longvideo.go:346-370` |
| Feed mode preference | Redis `feed:pref:<viewer>` | 5 min | rewritten on change | yes | `service/feed.go:1036-1059` |
| Delta result | Redis `feed_delta:<type>:<viewer>:<anchor>` | 10 s | nothing | yes | `http/handler_delta.go` |
| Block, mute, feed-mute sets | request memo only | one request | n/a | yes | `service/requestmemo.go:44-90` |

**How long stale content can be served, by reading the code:**

| Change | Worst case before the feed reflects it | Why |
|---|---|---|
| Viewer blocks an author (or is blocked) | next request | the block set is read from graph-service on every request and is never cached across requests (`service/feed.go:446`, `service/requestmemo.go:70-90`); the graph query returns both directions (`graph-service/internal/store/graph.go:1073-1079`) |
| Viewer mutes, hides, marks "not interested" | next request | read from Postgres per request (`service/feedback.go:240-262`) |
| Author deletes a post | uncached viewers: next request (post-service filters `deleted_at IS NULL`, `post-service/internal/store/postgres/posts.go:1632`). Viewers with a cached row: until the PostDeleted event is consumed, at most 5 min | tombstone check on every cached row, fail closed (`service/deleted_tombstone.go:78-103`). The delay is the consumer lag (D-1). |
| Post visibility changes | same as delete: consumer lag, at most 5 min | stale marker (`events/post_lifecycle.go:121`) |
| Author account goes private, or a follower is removed | up to 6 s | 3 s in feed-service plus 3 s in graph-service; checked on cached rows too (`service/hydration.go:453`) |
| Membership ends or starts; Action button blocked | next request | such rows are never cached (`service/hydration.go:908-910`) |
| Post still processing or scheduled | next request | never cached (`service/hydration.go:930`) |
| Viewer adds a filter keyword | up to 60 s, per pod | in-process cache |
| Post text edited | up to 5 min for viewers with a cached row | D-15 |
| Like and comment counts seen by other viewers | up to 5 min | by design (`service/hydration.go:21-25`) |

**Stampede protection:** the mutual-follows and subscribed-owners warmers use Redis SET NX markers (`service/mutuals.go:132`). The discovery pool, the following list and the in-process caches have none (D-14); their refill is one indexed read or one small HTTP call each.

No cache was found that can serve one viewer's row to another viewer: every cache that holds a per-viewer answer has the viewer id in its key.

## 5. Fan-out cost (read from the loop, not measured)

- **Writes per post** for an author with F followers and C connections who is not a celeb: 2 Scylla writes for the author's own timeline row and its index row, 2 for the author's home row, then **2 writes per recipient** (home row, then reverse-index row, one after the other, QUORUM), for F + C recipients minus duplicates. `store/scylla/timelines.go:153-177`, `service/feed.go:1144-1152, 1227-1288`.
- **Chunking:** pages of 100 recipients (`fanoutPageSize`), one graph HTTP call per page, 16 concurrent writers per page (`fanoutWorkers`), the next page is not read until the current one is written, progress saved to Redis after each page. `service/fanout_chunks.go:43-53, 172-251`.
- **Celeb threshold and pull-merge, production defaults:** `deploy/services/feed-service/values-prod.yaml` sets neither `FEED_CELEB_PULL` nor `FEED_CELEB_FOLLOWER_THRESHOLD`, so the code defaults apply: pull ON, threshold 10,000, and the threshold is clamped to 10,000 because graph-service will not page past that offset. `service/celebpull.go:56-79`. An author at or above 10,000 followers is flagged and nothing is pushed to followers; readers pull that author's own timeline (at most 50 celebs per viewer, `celebpull.go:82`).
- **Largest push, 10,000 followers:** 20,000 writes, 100 graph pages, 100 Redis progress writes. If one write takes 1 ms: about 1.3 s of writes plus the graph pages, roughly 2 s. At 5 ms per write: about 7 s. For that time the partition this event sits on processes nothing else (D-1).
- **One million followers:** not pushed. With pull on (the default) the cost is zero follower writes; subscribers of the author's channel still get a row for a long video. If pull is switched off, fan-out cannot work at this size: graph-service refuses offset 10,100, the leg fails, the event is retried three times and goes to the dead-letter topic, and followers past the first 10,100 never receive the post. The 2-minute event deadline (`events/consumer.go`, DefaultEventDeadline) would be exceeded anyway: 2,000,000 writes through 16 writers at 1 ms each is about 125 s before graph paging.
- **Caveats of the pull model, by reading:** a newly flagged celeb's posts are missing from a follower's Home until that follower's cached celeb set expires (up to 5 min, `celebpull.go:87`); a viewer who follows more than 50 celebs silently gets only 50 of them (`celebpull.go:122-124`).
- **Partition sizes:** `home_timeline_by_user` is keyed ((user_id, month), ts) with a 360-day TTL (`Architecture/docker/scylla/schema.cql:4-13`). Rows per partition = posts delivered to that user in that month, with no cap; at roughly 100 bytes a row, a viewer following 5,000 accounts that post twice a day holds about 300,000 rows, about 30 MB, per month. `timeline_index_by_post_v2` is ((post_id, shard 0..15)) so a pushed post has at most about 625 rows per partition (`schema.cql:70-78`). `reactions_by_post` is the unbounded one (D-10).

## 6. Outbound calls, pools, timeouts, deadline

| From feed-service to | Pool | Timeout | Retry | Breaker | Evidence |
|---|---|---|---|---|---|
| graph, post, profile, media, user, trust-safety (request path) | 20 idle connections per host, no cap on open connections | 5 s per call | none | per upstream and host: opens after 5 failures in a row, 30 s, one probe; a caller's own cancellation is not counted | `service/feed.go:154-160`, `shared/httpclient/client.go:254-271` |
| analytics (view counts) | same | 2 s on the client, 1.5 s on the call | none | yes, own breaker | `service/feed.go:154`, `service/view_counts.go:128` |
| graph, post, user (fan-out and warmers) | separate clients | 10 s | consumer retries the event | separate breakers, so a fan-out failure cannot close the request path | `service/traffic.go:51-59` |
| Postgres | pgx pool, max 25, min 5 per pod | request context only | none | none | `cmd/server/main.go:50-53` |
| Scylla | 10 connections per host, QUORUM | 3 s per query (`FEED_SCYLLA_TIMEOUT`) | library default | none | `cmd/server/main.go:74-83` |
| Redis | pool 50, min idle 10 | pool wait 30 s, library read/write default | 3 | none | `shared/transport/redis.go:21-27` |
| Kafka | one reader per group, serial | 2 min per event | 3 retries (1 s, 2 s, 4 s), then dead-letter | n/a | `shared/kafka/consumer.go`, `events/consumer.go` |

post-service and graph-service use the same Postgres pool size (25 per pod: `post-service/cmd/server/main.go:111-112`, `graph-service/cmd/server/main.go:61-62`).

**Request deadline:** 15 s for every feed route (`FEED_REQUEST_DEADLINE`), set in middleware (`http/handler.go:55, 67-74, 81`). When it fires mid-page the context is cancelled, the call in flight returns an error, and the handler answers 500 (`internalError`) or 502 `HYDRATION_FAILED`. No partial page is sent, and the cancelled call is not counted against the breaker. The filters that protect viewers (block, keyword, privacy, feedback) fail closed; view counts, reasons, channels, discovery, celeb pull and ranking are skipped or fall back without failing the page.

**Rate limit:** 120 requests per minute per user across all feed routes, fails open if Redis errors (`http/handler.go:842-884`). The Android Following poll (every 60 s, `FeedViewModel.kt:630`) and the web polls count against it.

## 7. Resource settings (prod values, AWS)

| Service | Replicas | HPA | CPU request / limit | Memory request / limit | Evidence |
|---|---|---|---|---|---|
| feed-service | 2 | 2 to 6 at 65% CPU | 250m / 1 | 256Mi / 1Gi | `deploy/services/feed-service/values-prod.yaml:15-27` |
| post-service | 2 | 2 to 8 at 65% CPU | 250m / 1 | 256Mi / 1Gi | `deploy/services/post-service/values-prod.yaml:16-29` |
| graph-service | 2 | 2 to 6 at 65% CPU | 250m / 1 | 256Mi / 1Gi | `deploy/services/graph-service/values-prod.yaml:15-28` |
| api-gateway | 2 | 2 to 8 at 60% CPU | 250m / 1 | 256Mi / 1Gi | `deploy/services/api-gateway/values-prod.yaml:18-31` |

- Kafka: 6 partitions per topic in prod, replication factor 2 (`infra/terraform/modules/msk/topics-job/variables.tf:62-72`). feed-service runs one serial reader for `social.events.v1` per pod, so 2 pods share 6 partitions and more than 6 pods adds no consumer capacity.
- The HPA measures CPU against the 250m request, so pods are added at about 160m of CPU. The consumer shares those pods and that CPU limit with the API.
- Postgres connections at maximum scale: 25 x (6 feed + 8 post + 6 graph) = 500 from these three services alone. Whether Aurora's connection limit allows that next to every other service was not checked.

## 8. Load testing

### 8.1 What load the module has actually been tested at

**None.** No load test script exists for feed-service, post-service, graph-service or the gateway feed routes, and no result is recorded anywhere in the repository (D-2 has the commands). The only load scripts in the repository are for dating-service and rider-service. The earlier audit defined scenarios L1 to L6 and recorded them as not run.

### 8.2 The load test that is needed

Run against a QA stack with production-sized Scylla (3 nodes, RF 3), Redis and Aurora, through the API gateway, never against production. Targets below are per scenario; set the request rates from the launch forecast, which this audit was not given. The rates shown are a floor for a first run.

**Data to seed first**

| Item | Volume |
|---|---|
| Users | 1,000,000 |
| Follows | 90% of users follow 50 to 300; 9% follow 1,000; 1% follow 5,000; 20% of users follow nobody (cold start) |
| Authors | 1,000 with 10,000 followers (largest push), 20 flagged celebs with 100,000 to 1,000,000 followers |
| Posts | 20,000,000 over 12 months; active timelines hold about 500 rows per user per month |
| Engagement | 200,000,000 reactions; one post with 1,000,000 reactions and 50,000 comments |
| Blocks and mutes | 5% of users with 1 to 50 each |

**Scenarios**

| ID | What | Rate and duration | Pass thresholds |
|---|---|---|---|
| L1 | `GET /v1/feed/home`, 60% ranked, 30% chronological, 10% following_only; 3 pages per session with the returned cursor; 20% of sessions are cold-start users. Each page followed by `POST /v1/posts/batch` (indirect) and, for a web profile, the media requests | ramp to 500 requests/s over 10 min, hold 30 min | p95 under 200 ms, p99 under 500 ms, errors under 0.1%, ranker fallback under 1%, Postgres pool wait near zero, no pod restarts |
| L2 | Paging correctness under load: 2,000 users each read 10 pages | once, during L1 | no duplicate and no missing post id against a server-side list |
| L3 | Fan-out and consumer: 1,000 authors posting 1 per second in total, 20 posts from 10,000-follower authors, 5 celeb posts, plus 50 follows/s, 20 unfollows/s, 500 likes/s and 5 deletes/s | 15 min | p95 from PostCreated to the follower's timeline row under 5 s (under 30 s for a 10,000-follower author); consumer lag under 10,000 and back to zero within 2 min of the end; nothing on the dead-letter topic; no partition idle for more than 30 s |
| L4 | `POST /v1/posts/:id/like` and `/reactions`: 5,000 users toggling on one post, plus 300 likes/s spread over 10,000 posts | 5 min | p95 under 100 ms, errors under 0.1%, final `like_count` equals the reaction row count |
| L5 | `GET /v1/feed/delta`: 100,000 sessions polling `following` every 60 s and `home` every 10 min, with posts arriving from L3 | 1 h | p95 under 100 ms, zero 500s (D-4), Redis memory growth recorded per 1,000 viewers |
| L6 | `GET` and `POST /v1/posts/:id/comments`: 200 reads/s over normal posts, 20 reads/s and 5 writes/s on the 50,000-comment post, both sorts | 10 min | p95 under 150 ms for reads, under 200 ms for writes |
| L7 | `POST /v1/feed/signal`, `/feedback`, `/hide`: 50 requests/s | 10 min | p95 under 100 ms |
| L8 | L1 again while one Scylla node is stopped, then while graph-service is stopped for 30 s | 20 min | with one Scylla node down: L1 thresholds still met. With graph down: explicit 5xx (by design), recovery within 30 s of graph returning, no stuck breaker |

**Record for every run:** p50, p95, p99 and error rate per route; requests per second per pod; CPU and memory per pod; Postgres pool in use and wait count; Scylla read and write latency; Redis used memory and evictions; consumer lag per partition; fan-out duration histogram; hydration cache hit ratio.

## 9. Checked and sound

Each of these was read in full and found correct. None was run.

| What | Evidence |
|---|---|
| Every home timeline read is a single-partition read on the full partition key (user_id, bucket), ordered by the clustering key, with LIMIT | `feed-service/internal/store/scylla/timelines.go:554-566`; key at `Architecture/docker/scylla/schema.cql:12` |
| Home pagination is keyset on the timeline key, not OFFSET | `service/feed.go:318-343`, `store/scylla/timelines.go:378-399` |
| No ALLOW FILTERING on the request path; the two scan statements run only when `FEED_HF4_FALLBACK=true`, and prod sets it to "false" | `store/scylla/timelines.go:86-88, 320-345`; `deploy/services/feed-service/values-prod.yaml` (`FEED_HF4_FALLBACK: "false"`) |
| Page size is bounded: limit at most 100, timeline over-read at most 200 rows | `http/handler.go:137-144`, `service/feed.go:383-389` |
| Timeline rows expire: TTL 360 days from the post's created_at on every write, table default as a backstop | `store/scylla/timelines.go:40, 66-72, 154-164`; `schema.cql:13, 88-90` |
| Timeline writes are idempotent (the key is derived from the post), so a retry or redelivery does not duplicate rows | `store/scylla/timelines.go:131-135` |
| The reverse index is sharded 16 ways, so a post's index is not one large partition | `store/scylla/timelines.go:45-52`; `schema.cql:70-78` |
| Fan-out memory is one page of 100 plus a 20,000-entry dedupe set, never the whole audience | `service/fanout_chunks.go:43-50, 146-167` |
| Fan-out resumes at the failed chunk on retry | `service/fanout_chunks.go:91-142, 224-251` |
| Fan-out and warmers use separate HTTP clients and breakers from the request path | `service/traffic.go:45-80` |
| Every upstream HTTP call has a timeout and a circuit breaker that ignores caller cancellations | `shared/httpclient/client.go:254-271` and the RoundTrip above it |
| Every feed request has an overall deadline (15 s) and Scylla queries a 3 s timeout | `http/handler.go:55-81`; `cmd/server/main.go:83` |
| Hydration is batched: one post batch, one profile batch, one media batch (chunks of 50), one channel batch, one view-count batch per page; the last three run in parallel | `service/hydration.go:489-500, 630-657, 772-817`; `service/view_counts.go:72-124` |
| post-service loads repost counts, bookmarks, reposts, media and polls for the page in a fixed number of queries, not per post | `post-service/internal/service/post_reads.go:361-380, 432-435` |
| `posts` batch read uses the primary key (`id = ANY($1)`), capped at 100 ids | `post-service/internal/store/postgres/posts.go:1629-1633`; `internal/http/post_reads_handler.go:320` |
| The cold-start query has a matching index (`created_at DESC` where not deleted) and a limit capped at 50 | `post-service/internal/store/postgres/posts.go:1088-1126`; `database/migrations/007_reels_gold_spec.sql:121` |
| feed-service Postgres lookups on the page all hit a primary key or an index: `hidden_authors(author_id)`, `feed_distribution(post_id)`, `feed_mutes(user_id, ...)`, `feed_feedback(user_id, post_id)`, `feed_hides(user_id, post_id)`, `feed_author_feedback(user_id, author_id)`, `celeb_authors(author_id)`, `user_preferences(user_id)` | `feed-service/database/migrations/001_initial.sql`, `002_feed_control.sql`, `003_...sql`, `004_hidden_authors.sql`, `006_feed_feedback.sql`, `007_feed_author_feedback.sql` |
| graph-service block and mute lookup is indexed in all three arms: blocks primary key (blocker_id first), `idx_blocks_blocked_id`, `idx_mutes_muter` | `graph-service/internal/store/graph.go:1073-1079`; `database/setup.sql:16-23, 74`; `database/migrations/005_blocks_muted_indexes.sql:2` |
| graph-service batch lookups are capped at 100 targets and refuse a larger batch instead of truncating it | `graph-service/internal/store/graph.go:1147-1157`; `internal/http/handler.go:564-568` |
| The follows table has an index for each direction | `graph-service/database/setup.sql:11, 14` |
| The ranker is one Redis pipeline under a 20 ms budget and falls back to chronological order on error | `ranking/ranker.go` (rank), `ranking/signals.go:185-265`, `service/feed.go:582-596` |
| Every per-viewer cache has the viewer in its key (table in section 4) | `service/hydration.go:28-30`, `service/privacyfilter.go:47-49`, `service/requestmemo.go`, `service/celebpull.go:90-92` |
| Cached rows are re-checked for delete, visibility change, processing state, listing visibility, repost author, keywords, account privacy and feedback before being served; the tombstone check fails closed | `service/hydration.go:407, 422-477`; `service/deleted_tombstone.go:78-103` |
| Membership-gated rows, rows with an Action button, and processing or scheduled posts are never cached | `service/hydration.go:908-910, 930-937` |
| Signed media URLs are not stored in the 5-minute cache; delivery data is fetched after the cache on every page | `service/hydration.go:605-608` |
| Media for feed cards is delivered from the CDN on Android: image variants are signed CDN URLs used directly; HLS segments are signed CDN URLs, only the small playlist comes through media-service | `media-service/internal/delivery/signer.go:127-150`; `media-service/internal/service/media_batch.go:37-150`; `mobile/android/feature/feed/src/main/kotlin/com/us/android/feature/feed/ui/FeedViewModel.kt:155-158`; `mobile/android/core/feed/.../FeedPlayback.kt:33-40` |
| media-service reuses URL signatures for 2 minutes, so a feed page does not cost one RSA signature per URL | `media-service/internal/delivery/sign_cache.go` (SignatureReuseWindow) |
| A like drops the liker's own cached row at once, so their heart does not flicker back | `post-service/internal/service/post.go:540-548`; `post_engagement.go:171` |
| The feed rate limiter sets its window atomically and fails open on a Redis error | `http/handler.go:830-884` |
| Web polling is modest: Home banner every 10 min, Following dot every 60 s, paused while the tab is hidden | `pb-echo/src/hooks/useFeedDelta.ts:42-47, 73` |
| Prod alert rules exist for consumer lag, dead-lettered events, fan-out failures, ranker fallback, hydration failures and Home p95 | `deploy/services/feed-service/values-prod.yaml` (prometheusRule) |

## 10. Not verified

Each of these is UNVERIFIED, with what is missing.

- **All latency and throughput numbers.** No test or benchmark was run. Needs section 8.
- **Query plans.** No EXPLAIN was run on any Postgres statement (comments, recent posts, follows by offset, the graph batch). Needs a seeded database.
- **Redis sizing and eviction policy.** The Terraform for ElastiCache was not read. Needs the node type, `maxmemory-policy`, and the L1 memory measurement.
- **Aurora connection limit** against 25 connections per pod across all services.
- **Scylla cluster shape, compaction strategy and tombstone load.** The schema sets no compaction strategy on the timeline tables (so the default applies) although every row has a TTL and unfollows and deletes write tombstones. Whether reads slow down on timelines with many deletes needs a soak test.
- **Deployed Kafka partition count.** 6 is the Terraform default; the applied value was not checked.
- **post-service outbox relay delay** between a post being saved and PostCreated reaching Kafka. Not read.
- **Cost inside the services feed-service calls for decoration:** identity-profile `/v1/profiles/batch`, analytics `/content-views`, trust-safety `/keyword-filters`, post-service `/v1/channels/batch`, and the media-service to post-service authorisation batch. Only the callers were read.
- **Whether a follow cap exists** (decides how far D-6 reaches).
- **CDN configuration** (cache behaviour, signed-URL key group) and whether the web proxy's storage route variables are unset in prod.
- **Android request pattern in detail:** page size, prefetch distance and image preloading in `FeedPagingSource` were not read; only the URL construction and the 60 s Following poll were.
- **Gateway rate limits** for `/v1/feed`, `/v1/posts` and `/v1/media` (Track B owns these).
- **`POST /v1/feed/signal`** was read only as far as the handler and the store call (`store/postgres/meta.go:107`, one Postgres statement); the statement text and its table's index were not checked.
- **The azure values files** were read only for replica counts.

## 11. Observations for other tracks and modules

One line each; not investigated further here.

- **Track E (reliability):** post-service's Kafka writer uses the `LeastBytes` balancer (`post-service/internal/events/producer.go:26`), which ignores the message key, so the per-user ordering the comment at `producer.go:215-225` promises is not delivered; a delete can be processed before the create it follows.
- **Track A (functional):** feed-service's close-friends fan-out calls `GET /v1/graph/close-friends` (`feed-service/internal/service/feed.go:1485`), a route graph-service removed on 21 Sep (`graph-service/internal/http/handler.go`, "REMOVED 21 Sep"); a post with visibility `trusted` would fail fan-out, retry and be dead-lettered.
- **Track E:** UserFollowed backfill ignores write failures and commits the event (`feed-service/internal/events/consumer.go:289-297`), so a failed backfill is never retried.
- **Data integrity:** likes are written by two paths with two counters, Redis (web `/like`) and Scylla (`/reactions`, Android); a Scylla failure on the `/like` path is logged and ignored (`post-service/internal/service/post_engagement.go:155-169`), so the two counts can drift.
- **Reels and long video (out of scope):** the content-type windows may read up to 48 pages of 1,000 rows from a viewer's timeline to fill one page (`feed-service/internal/store/scylla/timelines.go:451, 471-514`).
- **Reels and long video (out of scope):** the web player also uses `/v1/media/<id>/serve` through the Next.js proxy (same pattern as D-3; 216 occurrences of `/serve` under `pb-echo/src`).
- **All modules:** D-8 (gateway transport) applies to every route.
