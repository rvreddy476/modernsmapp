# Track C: data integrity and concurrency (home feed)

- **Date:** 2026-11-16. Read-only. Working tree of `codex/module-01-02-launch-safety` @ `3c860874e` (dirty).
- **Paths** are relative to `Architecture/` unless they start with `deploy/` or `mobile/`. `feed/`, `post/`, `graph/` below mean `services/feed-service/`, `services/post-service/`, `services/graph-service/`.
- **Verdict for this track:** no P0 found. Two P1s and eight P2s. Nothing I read loses or exposes a user's content: where the timelines go wrong, post-service's batch read (`deleted_at IS NULL` plus the visibility gate) still decides what is shown. What goes wrong is delivery (posts not reaching followers after an outage) and counters (several can disagree with their rows, with no repair job).
- **Commands run** (all passed):
  - `go test ./internal/service -run 'TestHomeWindow|TestWholeWindow|TestFanoutPost_RetryResumesAtTheFailedChunk|TestFanoutPost_DifferentEventStartsFresh' -count=1` in `feed/` → `ok`
  - `go test ./internal/store/scylla -run 'TestTimelineTS|TestHomeTimelineStatementsOnlyMatchOnPrimaryKey|TestPinnedPrimaryKeysMatchTheCheckedInSchema|TestUnfollowPurge' -count=1` in `feed/` → `ok`
  - `go test ./kafka -run 'Dedupe|Durab' -count=1` and `go test ./outbox -run Test -count=1` in `shared/` → `ok`, `ok`

## Findings

```
ID:            C-1
Title:         A dependency outage longer than about 7 seconds moves every fan-out, delete and unfollow event to the dead-letter topic, and nothing can replay it.
Severity:      P1
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      feed/internal/events/consumer.go:61-66 builds the shared consumer with no MaxRetries, RetryBackoff or RetryForever, so shared/kafka/consumer.go:118-123 applies 3 retries at 1 s, 2 s, 4 s. After the last attempt the message is written to the DLQ and its offset committed (shared/kafka/consumer.go:336-344). Fan-out errors on any failed graph page or timeline write (feed/internal/service/fanout_chunks.go:231-243, feed.go:1313-1315). The only DLQ replayer in the repository is search-service's (services/search-service/internal/events/dlq_replay.go); a search for "dlq" under scripts/, deploy/ and docs/harness/ finds only the alert text in deploy/services/feed-service/values-prod.yaml:104-109 ("Inspect the x-dlq-error header and replay once fixed"). The DLQ topic is shared with other consumers (consumer.go:21-26).
How to reproduce: Make graph-service or Scylla unavailable to feed-service for 10 seconds while posts are created. Each PostCreated fails four times in about 7 s and is parked. When the dependency returns, the offsets are already committed.
Impact:        Posts created during the outage never reach any follower's home timeline (push model). PostDeleted, visibility changes and unfollow purges from the same window are also parked. The alert fires, but recovery needs a tool that does not exist.
Fix:           Set RetryForever for the feed consumer (the shared consumer already supports it, consumer.go:35-39, 272) and mark only undecodable payloads Permanent; or keep the DLQ and add a replay command modelled on search-service's. The per-event deadline (consumer.go:98) already bounds a stuck handler.
Acceptance:    A consumer test with a handler that fails for 30 s then succeeds: today the message reaches the DLQ writer; after the fix the handler's effect is applied and no DLQ write happens.
Effort:        S (RetryForever) or M (replay tool)
```

```
ID:            C-2
Title:         post-service's outbox relay publishes each row with a new event id, a random partition key and no lease, so events for one post can arrive out of order and every replica publishes them.
Severity:      P1
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      post/internal/service/outbox.go:19-36 reads unpublished rows with a plain SELECT (post/internal/store/postgres/outbox.go:53-62, no FOR UPDATE, no lock), wraps each in events.NewEnvelope, which calls uuid.New() for EventID (shared/events/events.go:2042-2053), and publishes with Key = that EventID (post/internal/events/producer.go:230-240). The relay runs in every pod (post/cmd/server/main.go:624) and prod runs at least 2 (deploy/services/post-service/values-prod.yaml:16-20). The shared relay in shared/outbox (lock, lease, stable id, partition key) is not used here. feed-service's dedupe key is the envelope EventID (shared/kafka/consumer.go:246-256), so it cannot recognise a re-publish.
How to reproduce: (a) Delete a post and restore it at once: PostDeleted and PostRestored get different keys, so different partitions; if PostRestored is handled first, PostDeleted then removes every timeline row (feed/internal/events/post_lifecycle.go:63) and the restored post is in nobody's feed. (b) Create then delete while the PostCreated fan-out is retrying: the delete finds no rows, the fan-out then writes them. (c) Two pods poll in the same 5 s tick: both publish the same row with different ids.
Impact:        Restored posts missing from followers' feeds; rows for deleted or now-private posts left in timelines (hidden at read time by post-service's batch gate, but they take page slots for up to 360 days); every PostReacted or CommentCreated published twice moves feed-service's velocity counter twice (feed/internal/events/consumer.go:588-592, 659-663) and, outside this module, can notify twice. This is the event source for all of fan-out, so I rate it P1 rather than P2.
Fix:           Publish with Key = aggregate_id and EventID = the outbox row id (both are already columns), and take a Postgres advisory lock for the pass as shared/outbox/store.go:93-117 does. No schema change.
Acceptance:    A unit test of PublishOutboxEvents with a fake producer: two calls for one unpublished row (mark-published failing in between) must produce the same EventID and Key = aggregate id. Fails today on both.
Effort:        S
```

```
ID:            C-3
Title:         A retried post.visibility_changed event deletes the rows its first attempt wrote and then resumes the fan-out from the saved position, so followers in the finished chunks lose the post.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ (the resume half is VERIFIED-RUN: TestFanoutPost_RetryResumesAtTheFailedChunk passes)
Evidence:      feed/internal/events/post_lifecycle.go:138-171 runs removePostFromTimelines and then FanoutPost on every attempt. FanoutPost loads progress keyed by post id + event id (feed/internal/service/fanout_chunks.go:91-106; the event id is put on the context at feed/internal/events/consumer.go:122) and skips finished legs and chunks (fanout_chunks.go:226-229). Progress is cleared only when the whole fan-out succeeds (feed.go:1316). A retry has the same event id.
How to reproduce: Author with 250 followers changes a post from followers to public. Attempt 1 writes chunks 0-99 and 100-199, saves offset 200, then the third graph page fails. Attempt 2 removes all rows through the index, then resumes at offset 200. Followers 0-199 never get the post back. The same happens when the followers leg finished and the connections leg failed.
Impact:        After an audience edit plus one transient error, the post silently disappears from most followers' home feeds. It stays on the profile.
Fix:           In rederiveVisibility, delete the event's progress record (Redis DEL of FanoutProgressKey) after removePostFromTimelines and before FanoutPost.
Acceptance:    A test with a fake timeline store and a graph stub that fails page 3 once: after the retry every one of 250 followers holds the row. Today 200 do not.
Effort:        S
```

```
ID:            C-4
Title:         Unfollow and repost events are published from a goroutine straight to Kafka, not through an outbox, so a failed publish is lost for good.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      graph/internal/service/graph.go:305-318 deletes the follow row, then publishUserUnfollowedAsync (graph.go:413-424) publishes in a goroutine with a 5 s timeout and only logs a failure. Follow, by contrast, writes its event in the same transaction (graph/internal/store/pair_atomic.go:159-186). post/internal/service/post_reposts.go:138-149 and :196-205 publish PostReposted and PostRepostUndone the same way. The comment at graph.go:408-412 says an "outbox replay closes the gap"; no outbox row is written for these events.
How to reproduce: Unfollow, repost or undo a repost while Kafka is unreachable for 5 s, or kill the pod after the HTTP response.
Impact:        Lost unfollow: the ex-follower keeps the author's rows (public posts keep showing in For You; followers-only posts are refused by the batch gate). Lost repost: followers never see it. Lost undo: "X reposted" stays in followers' feeds until the row's TTL. Also, because follow goes through the 500 ms relay and unfollow goes direct, a follow then unfollow within the relay interval can arrive reversed; handleUserFollowed backfills without checking the follow still exists (feed/internal/events/consumer.go:225-297), leaving up to 50 rows.
Fix:           Write UserUnfollowed with appendGraphOutboxTx inside the delete's transaction (the helper exists, pair_atomic.go:464); write the two repost events with InsertOutboxEventTx in the repost row's transaction.
Acceptance:    Integration test: unfollow with the producer returning an error; a graph_outbox_events row for user.unfollowed must exist. None is written today.
Effort:        S
```

```
ID:            C-5
Title:         POST /v1/posts/:postId/react is not safe under concurrent or repeated requests: it reads without a lock, emits an unreact event for deletes that removed nothing, and syncs Scylla after commit in no fixed order.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ
Evidence:      post/internal/store/postgres/reactions.go:71-75 selects the existing reaction with no FOR UPDATE; :82-86 deletes without checking RowsAffected; :105-110 writes the outbox event from the pre-read state. post/internal/service/post_reactions.go:194-203 then calls Scylla React or Unreact after the commit and only logs a failure. The feed card's like count and the viewer's heart come from Scylla (post/internal/service/comment_counts.go:16-23, 125-171; post/internal/service/post_reads.go:393-397), not from the Postgres row this endpoint decides on. The web home feed uses this endpoint (postbook-ui src/hooks/useFeedReaction.ts:38).
How to reproduce: 50 identical concurrent requests. With no prior reaction: one INSERT wins the UNIQUE(target_type,target_id,user_id) constraint (post/database/migrations/002_phase1_stories_reactions_saved.sql:43), the others either fail with a 500 or, if they start after the commit, toggle it off again; the final state depends on timing. With a prior reaction: every request sees it, every DELETE "succeeds", and each writes a PostUnreacted outbox row, so feed-service decrements the post's velocity counter up to 50 times for one removed like (feed/internal/events/consumer.go:627-645, floored at 0). Two quick taps: request B can commit and run Scylla Unreact (no row yet, no-op) before request A runs Scylla React; Postgres then says "not liked" and Scylla says "liked" with the count one too high.
Impact:        A double tap or client retry can leave the heart and count on the card disagreeing with the stored reaction until the user taps twice more. One user can drain a post's ranking velocity with concurrent toggles. The Scylla LWT gating itself is sound (see Checked and sound), so the like count stays equal to the Scylla rows.
Fix:           SELECT ... FOR UPDATE on the reaction row (or lock the (target,user) pair with pg_advisory_xact_lock), emit the event only when the DELETE or INSERT affected a row, and return an error when the Scylla sync fails so the client retries. Better: have the clients send the wanted state (the POST/DELETE /reactions pair Android already uses is idempotent).
Acceptance:    Integration test: 50 concurrent toggles on an existing reaction produce exactly one PostUnreacted outbox row and no 500s. Today it produces up to 50.
Effort:        S
```

```
ID:            C-6
Title:         The saves count on a feed card never changes: the live bookmark routes do not move bookmark_count.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ
Evidence:      counts.saves is read from post_engagement_counts.bookmark_count (post/internal/service/comment_counts.go:200-208). The only writer of that column is the engagement consumer on EventPostBookmarked (post/internal/engagement/consumers/pg_counter.go:83-84). The only producer of that event is ToggleBookmarkNew (post/internal/service/post_engagement.go:253), reached only from the handler ToggleBookmark (post/internal/http/bookmarks_handler.go:126-141), which no route registers (handler.go:152-153 register AddBookmark and RemoveBookmark; a search for "ToggleBookmark" across internal/http finds no route). AddBookmark and RemoveBookmark write saved_items only (post/internal/store/postgres/posts.go:1379-1395). Android's model carries the field (mobile/android/core/model/.../FeedItem.kt:344).
How to reproduce: Bookmark a post through POST /v1/posts/:id/bookmark, then read it through POST /v1/posts/batch: counts.saves is unchanged.
Impact:        Any client that shows saves shows a number that does not match saved_items. No reconcile job covers it (post/internal/reconcile/counts.go recounts comments only). I did not check which screens render the field.
Fix:           In AddBookmark and RemoveBookmark, adjust bookmark_count by ±1 only when the INSERT or DELETE affected a row; add saves to the hourly recount (COUNT(*) FROM saved_items WHERE target_type='post').
Acceptance:    Integration test: add, add again, remove → counts.saves reads 1, 1, 0. Today 0, 0, 0.
Effort:        S
```

```
ID:            C-7
Title:         Share, repost, bookmark and follower counts are flushed from Redis to Postgres as absolute values and never seeded, so lost Redis keys overwrite the stored count with a near-zero number.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ for the code path; UNVERIFIED whether production Redis persists and never evicts (needs the ElastiCache or Helm Redis settings)
Evidence:      shared/counters/worker.go:66-83 reads the shard sum and calls flush(total). The flush functions SET the column: post/internal/store/postgres/engagement_counts.go:45-57 (wired at post/cmd/server/main.go:410-459) and graph/internal/store/graph.go:266-279. Counter.Seed exists (shared/counters/counters.go) but nothing calls it (a search for ".Seed(" in post-service, graph-service and shared finds none outside tests). The shard keys have no TTL and no backing store.
How to reproduce: Delete counter:post_share_count:<post>:* in Redis for a post with 500 shares, then share once more. Within 10 s share_count is 1.
Impact:        After a Redis flush, failover without persistence, or eviction, every counter touched afterwards restarts from the delta. Shares, reposts and saves have no recount, so the loss is permanent. Follower and following counts are corrected hourly by graph/internal/reconcile/counts.go:34-66 and then overwritten again on the user's next follow, because the reconciler does not reseed Redis; that reconciler also skips users whose true count is 0 (they are absent from its GROUP BY).
Fix:           Make the flush a delta: read-and-reset the shards atomically (a Lua GETDEL sum) and apply `col = GREATEST(col + delta, 0)`, which IncrementEngagementCount and IncrementCountColumn already do.
Acceptance:    Unit test of Worker.Flush against miniredis: with a stored value of 500 and empty shards, one Inc(+1) then Flush must store 501. Today it stores 1.
Effort:        M
```

```
ID:            C-8
Title:         The like count on a feed card has no repair path and no floor: a failed counter update after the conditional write leaves it wrong for good, and it can go negative.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      post/internal/store/scylla/interactions.go:44-68: the INSERT ... IF NOT EXISTS applies, then the counter UPDATE is a separate statement; if it fails React returns (true, err), the client retries, the row now exists, and the increment is never made. Unreact (:74-91) later decrements for that row. post_counters.like_count is a Scylla counter (docker/scylla/schema.cql:207-211) read raw (interactions.go:170-181). No job compares it with reactions_by_post: post/internal/reconcile/counts.go covers comments, post/internal/engagement/reconcile.go compares Redis with engagement_counters (a different table), cmd/backfill-likes copies rows into Postgres.
How to reproduce: Fault-inject a timeout on the counter UPDATE after a successful LWT; retry the like; unlike. The count is one below the number of rows, or -1 on a post with no other likes.
Impact:        Rare, small, permanent drift on the most visible counter.
Fix:           Clamp at 0 on read (one line in GetCounts), and add a recount command that sets the counter from COUNT(*) of reactions_by_post for posts flagged dirty.
Acceptance:    Unit test on the read path: a stored -1 is served as 0.
Effort:        S (clamp), M (recount)
```

```
ID:            C-9
Title:         Restoring a deleted post does not bring back the repost rows that the delete removed.
Severity:      P3
Category:      functional
Evidence level: VERIFIED-READ
Evidence:      Repost rows are stored under the original post's id (feed/internal/service/feed.go:1741, 1751), so DeletePostFromTimelines removes them with everything else (feed/internal/store/scylla/timelines.go:955-991). handlePostRestored only calls FanoutPost for the author's audience (feed/internal/events/post_lifecycle.go:316). post_reposts rows stay active and repost_count is unchanged.
How to reproduce: B reposts A's post; A deletes and restores it within 72 hours; C, who follows only B, no longer has the repost in the feed, while B's repost still shows as active.
Impact:        Small inconsistency between repost state and feeds after a restore.
Fix:           On PostRestored, post-service emits PostReposted again for the post's active reposts (or feed-service asks for them and re-fans them out).
Acceptance:    Consumer test: after delete then restore, the repost row for C exists again.
Effort:        M
```

```
ID:            C-10
Title:         The follow backfill swallows write failures, and the unfollow purge covers only the last three months of a 360-day timeline.
Severity:      P3
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      feed/internal/events/consumer.go:288-296 counts failed AddToHomeTimeline calls, logs, and returns nil, so the event is committed with a partial backfill. feed/internal/events/consumer.go:320, 369 and feed/internal/store/scylla/timelines.go:707-728 purge bucketLookback = 3 months; rows live 360 days (timelines.go:40).
How to reproduce: Fail Scylla writes during a follow backfill: the new follower gets fewer than the 50 posts and nothing retries. Unfollow an author followed for six months and scroll past three months: their public posts are still there (ranked For You; the Following tab filters by the live follow list).
Impact:        Minor feed inaccuracy.
Fix:           Return an error when failures > 0 (the writes are upserts, so a retry is safe); pass maxTimelineBucketLookback to the purge.
Acceptance:    Consumer test with a store that fails one write: handleUserFollowed returns an error.
Effort:        S
```

```
ID:            C-11
Title:         Comment, like and unlike side effects run after the main write with errors only logged, and a comment without an Idempotency-Key is not protected against a retry.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      post/internal/service/post_comments.go:202-204 (count +1 after the insert, warn on failure; the hourly recount at post/internal/reconcile/counts.go:55-61 repairs it), :241-243 (CommentCreated outbox insert outside the comment's transaction, warn on failure), post/internal/service/post_reactions.go:59-61 and :115-117 (same for PostReacted and PostUnreacted on the /reactions routes). post/internal/http/middleware/idempotency.go:212-218 passes a request with no key straight through.
How to reproduce: Fail the outbox INSERT after a comment commits: the comment exists, its notification and velocity signal do not. Send the same comment twice with no Idempotency-Key: two comments.
Impact:        Lost notification or ranking signal on a database blip; a duplicate comment from a client that does not send the header (I did not check which clients send it).
Fix:           Move the three outbox inserts into the transactions that write the row (ToggleReaction already does this, post/internal/store/postgres/reactions.go:105-110).
Acceptance:    Integration test: comment create with the outbox insert failing must roll the comment back or return an error.
Effort:        S
```

## Checked and sound

| What | Proof |
|---|---|
| Timeline row key is a pure function of (post, created_at); a re-write is an upsert | `feed/internal/store/scylla/timelines.go:131-135, 153-166`; `TestTimelineTS_*` pass (run) |
| Home cursor is a keyset on the exact `ts` timeuuid, strictly below, for both ranked and chronological modes; ranking only reorders the page already cut | `feed/internal/service/feed.go:318-343, 578-591`; `timelines.go:378-399, 553-559`; `TestHomeWindow_WalkServesEveryEligiblePostExactlyOnce` passes (run). New posts sort above the cursor, so concurrent inserts cannot repeat or skip later pages. A row that lands late with an old `created_at` above the cursor is seen only on refresh. |
| Out-of-network discovery items are not offered twice across pages or refreshes | `feed/internal/service/discovery.go:184-200` (per-viewer offered set in Redis) |
| Consumer commits only after the handler succeeded or the DLQ write was durable; the dedupe receipt is written after the effect; a fetch error does not end the loop | `shared/kafka/consumer.go:187-195, 277-296, 306-313, 432-455`; `go test ./kafka -run 'Dedupe|Durab'` passes (run) |
| DLQ writes are counted and alerted | `shared/kafka/consumer.go:368-370`; `deploy/services/feed-service/values-prod.yaml:97-111` (lag, DLQ, incomplete fan-out) |
| Partial fan-out fails the event and a retry resumes at the failed chunk; rewriting a chunk cannot duplicate | `feed/internal/service/fanout_chunks.go:172-215, 224-251`; `feed.go:1313-1316`; `TestFanoutPost_RetryResumesAtTheFailedChunk` passes (run) |
| Delete removes every row through the reverse index (v2 and legacy), index rows last; tombstone written first | `timelines.go:214-245, 955-991`; `feed/internal/events/post_lifecycle.go:52-66`; prod keeps `FEED_INDEX_LEGACY_READ: "true"` (`values-prod.yaml:78`) |
| Deleted posts are excluded from the batch read that hydrates every feed, during and after the 72 h window | `post/internal/store/postgres/posts.go:1629-1633` |
| Hidden (deactivated, deletion-scheduled) authors: filter fails closed | `feed/internal/service/hidefilter.go:36-52`; called at `feed.go:451, 525` |
| Unlisted and private posts are written to the author's rows only | `feed.go:1154-1160` |
| Unfollow purge checks the live relationship first and fails closed; deletes on the primary key only | `feed/internal/service/unfollow_purge.go:84-135`; `timelines.go:756-785`; `TestUnfollowPurge*` pass (run) |
| Post create, delete, restore and visibility edit write their event in the same transaction as the row | `post/internal/store/postgres/posts.go:702-764`; `posts_lifecycle.go:53-103`; `my_uploads.go:125`; `post_edit.go:274` |
| Post create has a durable idempotency key claimed in the create transaction | `posts.go:712-742`; `post/database/migrations/035_post_create_idempotency.sql:45` |
| Comment create: atomic Redis claim, body fingerprint, plus a durable (actor, post, key) record; a replay runs no side effects | `post/internal/http/middleware/idempotency.go:46-53, 232-244`; `post/internal/service/post_comments.go:176-186`; `migrations/034_comment_idempotency.sql:20` |
| Comment count: one definition, floor at zero, hourly and boot-time recount; deleted and hidden comments excluded | `post/internal/store/postgres/comment_counts.go:26, 66-68, 82-113`; `engagement_counts.go:30-35`; `post/internal/reconcile/counts.go` |
| Scylla like row and count: conditional write decides the increment, so concurrent likes or unlikes count once | `post/internal/store/scylla/interactions.go:44-91` |
| POST/DELETE `/reactions` (Android) and POST/DELETE `/bookmark` are idempotent | `interactions.go:47-63, 77-86`; `posts.go:1379-1395`; `saved_items UNIQUE(user_id,target_type,target_id)` at `migrations/002_...sql:57` |
| Repost: one active row per (user, post); a duplicate is refused; undo decrements only when a row changed | `migrations/015_post_reposts.sql:18-20`; `post/internal/store/postgres/reposts.go:77-80, 86-98` |
| Follow: block check, insert and event under one pair lock in one transaction; duplicate follow is a no-op | `graph/internal/store/pair_atomic.go:159-186`; `follows PRIMARY KEY (follower_id, followee_id)` at `graph/database/migrations/001_initial.sql:11` |
| Graph outbox relay: lease with SKIP LOCKED, mark after publish, no attempt cap, stable event id, pair-keyed partition | `graph/internal/store/outbox_relay.go:126-177`; `graph/internal/events/outbox_publisher.go:108-124` |
| Block and mute rows: primary keys, idempotent inserts | `001_initial.sql:20`; `003_mutes.sql:10`; `pair_atomic.go:291-296`; `graph/internal/store/graph.go:953-957` |
| Feed hide, mute, feedback, preference, distribution writes are upserts on primary keys; distribution is revision-guarded against replay and reordering | `feed/internal/store/postgres/control.go:21, 57-59`; `feedback.go:27-29, 94-96`; `distribution.go:18-22`; `feed/database/migrations/002, 003, 006, 007` |
| feed-service migrations are idempotent (IF NOT EXISTS, guarded DO block), applied one file per transaction under an advisory lock and recorded in `schema_migrations` | `feed/database/migrations/001-008`; `shared/store/migrationrunner/exclusive.go` |
| The three Scylla schema files hold the same 27 tables and differ only in replication | `diff` of `docker/scylla/schema.cql`, `schema.qa.cql`, `schema.prod.cql` (comments stripped): only the five `CREATE KEYSPACE` lines differ. Applied by `deploy/jobs/scylla-schema/apply.sh`, which refuses a file with the wrong strategy or replication factor (lines 44-51). TTL changes to existing tables are carried by explicit ALTERs (`schema.prod.cql:88-90`). |
| post-service `migrations_release_b/037` is not embedded, on purpose | `post/database/embed.go` embeds `migrations/*.sql` only; `migrations_release_b/README.md` |

## Not verified

- Migrations from an empty database: I read feed-service's ten files and the post-service and graph-service constraints named above, but did not run them (no database allowed) and did not read all 82 post-service or 12 graph-service files in order. Numbering in post-service starts at 002 and skips 037 by design; I saw no duplicate numbers in the listing. No down migrations exist in any of the three services, so all are forward-only; whether each is safe to roll forward past a failed deploy is unchecked.
- The 50-concurrent-request behaviour in C-5 is from reading the statements, not from a run.
- Production Redis durability and eviction policy (decides how likely C-7 is).
- Comment previews on feed cards and whether they exclude deleted comments.
- Whether two timeline rows for one post (an original and a repost of it) are collapsed server-side within a page; `mergeTimelineRows` collapses by post id only when celeb pull adds rows (`feed/internal/service/celebpull.go:172-190`).
- View counts (served by analytics-service) and their drift protection.
- Fan-out for an author above 10,000 followers under load. By reading: pull-merge is on by default with a 10,000 threshold (`celebpull.go:28-60, 68-79`), followers are paged by offset ordered `created_at DESC` (`graph/internal/store/graph.go:327-333`), so an unfollow during a fan-out shifts the pages and one follower can miss the post, and a new follow repeats one (harmless). A viewer following more than 50 flagged authors gets only the first 50 pulled (`celebpull.go:82, 122-124`).
- The delta ("N new posts") path and the feed-service purge consumer were not read in this track.
- `timeline-maintenance` and `cmd/backfill` were not run; whether pre-index rows exist in any environment is unknown.

## Out of scope observations

- Echo: POST `/v1/posts/:id/like` keeps "liked" in a Redis key with a 24-hour TTL (`post/internal/engagement/lua.go:44-69`); after it expires, or after a like made through another route, the next tap likes again instead of unliking.
- Shares: `SharePost` checks then sets its Redis repost marker in two steps and publishes its event from a goroutine (`post/internal/service/post_engagement.go:296-326`).
- Engagement consumer: `incrementCounter` and the unlike deletes ignore errors (`post/internal/engagement/consumers/scylla_like.go:90-104, 241-280`).
- Notifications: C-2's duplicate publishes reach notification-service as distinct event ids.
