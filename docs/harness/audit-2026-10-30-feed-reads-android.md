# Audit: post-service feed read paths, graph lookups, Android feed client

Branch `codex/module-01-02-launch-safety` (worktree `-9erjug`). Read-only. No repo edits, no git writes.
One Go unit test was run on a **scratch copy** of post-service, not on the repo, to confirm finding B1:
`/tmp/claude-0/-home-user-modernsmapp/932aa0f0-df00-5bf2-94af-2676731b82ec/scratchpad/copy/Architecture/services/post-service/internal/service/zz_audit_thread_test.go`

Paths below are relative to `/home/user/modernsmapp`.

---

## Part 1: Backend (post-service, graph-service)

### B1. CRITICAL: `GET /v1/posts/:postId/thread` skips the private-account, block, deactivated-author and age gates
- **Where:** `Architecture/services/post-service/internal/service/threads.go:288-293` (`canViewThread`), called from `GetThread` at `threads.go:257-282`.
- **What happens:** If the thread root's visibility is `public` or `unlisted`, `canViewThread` returns `true` straight away. It never calls `canViewAuthor` or `canViewPosts`, which is where the graph `view_posts` check (private account, blocks in either direction) and the `post_hidden_authors` check (deactivated or pending-delete accounts) happen. `GetThread` also never calls `checkAgeGate` or `hiddenFromViewer`. Then `redactPostRowsForViewer` returns every entry.
- **Example:** Alice has a private account and posts a public-visibility thread. Bob doesn't follow her, or Alice has blocked him, or he isn't signed in. `GET /v1/posts/{anyThreadPostId}/thread` returns every post in the thread. The same post through `GET /v1/posts/:id` returns 404. A deactivated author's threads and 18+ threads also open to anyone.
- **Confirmed by test:** I used a graph fake that answers `view_posts=false` and `blocked=true`. The output was `direct read allowed=false, thread allowed (signed-in blocked)=true, thread allowed (anonymous)=true`.
- **Fix:** Start `canViewThread` with `s.viewerMayViewPost(ctx, root, viewerID)`, which covers the account gate, hidden authors, blocks and the per-post audience. Then run `checkAgeGate` and `hiddenFromViewer` per entry in `GetThread`. Turn the scratch test above into a regression test.

### B2. CRITICAL: `GET /v1/posts/trending` returns flagged, rejected and copyright-restricted posts, and private accounts' posts
- **Where:** `Architecture/services/post-service/internal/store/postgres/saved.go:187` (store `GetTrendingPosts`). The service side is `internal/service/post.go:3945-3980`.
- **What happens:** The WHERE clause checks only `deleted_at IS NULL`, `visibility = 'public'` and `publish_at IS NULL`. It has no `viewerApprovedSQL` / `ViewerEligibleSQL`, so posts that are `pending`, `flagged`, `rejected` or `restricted` (under an active copyright hold) are ranked and returned. The service never calls `canViewPosts`, so posts from private accounts, from blocked or blocking users, and from deactivated authors are listed too. This is the same bug the `GetRecentPosts` comment (post.go:2075-2083) says was fixed there. The hashtag page does both checks (`hashtag_follows.go:172-173`, `hashtag_total.go:32`). Trending is the one listing left out.
- **Example:** Auto-moderation flags a reel, or trust-safety puts a copyright hold on it, and it still has high engagement. It stays in the public Trending tab for every viewer, including anonymous ones.
- **Fix:** Replace the condition list with `ViewerEligibleSQL("p")` plus `p.visibility = 'public'`. In the service, filter rows through `canViewPosts(ctx, viewerID, authors)` the way `GetRecentPosts` does (post.go:2090-2108).

### B3. HIGH: The saved/bookmarks list (`GET /v1/posts/bookmarks`) ignores every visibility change made after the save
- **Where:** `Architecture/services/post-service/internal/store/postgres/posts.go:1465` (filters only `p.deleted_at IS NULL`), and `internal/service/post.go:2609-2635`.
- **What happens:** Nothing checks the author's account visibility, blocks, `post_hidden_authors`, the post's current `visibility` (followers, friends or private), or `effective_review_status`. The only checks are media state and age.
- **Example:** Bob saves a followers-only post. Alice then removes him as a follower, blocks him, or makes her account private, or the post is later flagged or put under a copyright hold. Bob's Saved page still shows the full post body and media.
- **Fix:** Add `viewerApproved("p")` and `p.publish_at IS NULL` to the SQL. In the service, apply `canViewPosts`, then `batchVisibilityAllowsEdges` using `followEdgesOfRestrictedPosts`. That is the same per-row rule as `loadPostsByIDs` (post.go:2218-2257).

### B4. MEDIUM: The post body cache can serve a stale `tier_required_id` for up to 5 minutes, so a members-only post is shown in full
- **Where:** `Architecture/services/post-service/internal/service/post_cache.go:97-104` (`applyPostAccessState`) and `:80-86` (the fill on a cache miss). The access-state query is `internal/store/postgres/post_access_state.go:48`.
- **What happens:** On a cache hit, only the review status, restriction count, visibility, age flag and publish time are re-checked against Postgres. `tier_required_id` is not, even though `RedactDetailForViewer` (handler.go:1151) relies on it. Invalidation is a best-effort `Del` after the commit (`membership.go:97`, `post_edit.go:266`), so two cases leave stale data:
  1. **Read/write race:** A reader misses the cache and loads the row before the gate exists. The author sets the gate and the `Del` runs. Then the reader's `Set` writes the ungated body back for 5 minutes.
  2. **Failed `Del`:** If Redis briefly fails, the error is ignored.
- **Example:** A creator makes a post members-only. For up to 5 minutes, `GET /v1/posts/:id` gives non-members the full text, media and playback with no redaction.
- **Fix:** Add `tier_required_id` to `GetPostAccessState` and `applyPostAccessState`. That query already runs on every cache hit, so the extra column is free. It fully closes the hole whatever the invalidation does.

### B5. HIGH (resilience/latency): graph-service `CanBatch` fetches privacy settings one target at a time, and post-service hides the whole page when that runs past its 5-second timeout
- **Where:** `Architecture/services/graph-service/internal/service/follow_requests.go:224-235`. The loop calls `s.fetchPrivacy(ctx, targetID)` serially, and each call is an identity HTTP GET with a 5s client timeout (`graph.go:104`). The cache TTL is only 3s (`permissions.go`, `PrivacyCacheTTL`). The caller is `post-service/internal/service/privacy_gate.go:148-160`, which has one 5s `http.Client` for the whole `/can` call (`post.go:353`) and denies the whole chunk on error.
- **Example:** A feed page with about 20 distinct authors, none cached, while identity is answering at 250ms or more. `/can` takes over 5 seconds, post-service's client times out, and every author is denied. `GetPostsByIDs` returns an empty map and the home feed shows nothing. The 3s TTL means this happens again on most scrolls. It also costs one identity round trip per author on every page.
- **Fix:** Fetch privacy settings in parallel with a bounded worker pool, or add a batch settings endpoint to identity. Pass a context deadline shorter than the caller's timeout so a slow tail doesn't deny the whole page.

### B6. MEDIUM-HIGH (resilience): One Scylla error fails the whole feed hydration, and Scylla reads ignore the request context
- **Where:** `post-service/internal/service/post.go:2199-2211`. A `BatchGetCounts` or `BatchGetReactions` error returns `fmt.Errorf("load post counts")` and the handler answers 500. `GetPost` behaves the same way through `countsForPost` (post.go:1874-1877). In `internal/store/scylla/interactions.go:96` (`GetReaction`) and `:221` (`GetCounts`), the queries have no `.WithContext(ctx)`. The write paths in the same file do have it.
- **Example:** Scylla is degraded or one node times out. Every `POST /v1/posts/batch` call, which hydrates the whole home feed, returns 500 even though the post bodies in Postgres are fine. Because reads ignore cancellation, abandoned requests keep holding gocql connections for gocql's own timeout (default 11s in v1.7.0, and `cmd/server/main.go:136-140` sets none).
- **Fix:** Add `.WithContext(ctx)` to the read queries. Make like counts and the viewer's reaction degrade to zero or unknown (log and continue) rather than failing the page. Set `cluster.Timeout` explicitly.

### B7. MEDIUM (performance): Serial count reads per row on the profile grid, recent, saved, hashtag and trending lists
- **Where:** `post-service/internal/service/post.go:2054` (by-author, which is the profile feed), `:2112` (recent), `:2618` (bookmarks), `:3920` (hashtag), `:3953` (trending). Each `countsForPost` makes one Scylla read and one Postgres query (`comment_counts.go:27-37`), run in sequence.
- **Example:** A 50-row page costs 100 sequential round trips, roughly 100-300ms on a healthy cluster and seconds when degraded. Polls add `GetPoll` per row.
- **Fix:** Use the batched path `loadPostsByIDs` already uses: `scyllaStore.BatchGetCounts` plus `overlayCommentCounts` (post.go:2199-2204).

### B8. MEDIUM (performance): One relationship request per author in batch hydration
- **Where:** `post-service/internal/service/batch_visibility.go:91-133`. For each distinct author of a followers or friends post, it makes a separate `GET /v1/graph/relationship` (concurrency 8, `fetchFollowEdges` in post.go:3477).
- **Example:** A page of followers-only posts from 40 authors makes 40 graph HTTP calls per hydration, on top of the `/can` call. graph-service already offers `POST /v1/graph/relationships/batch`, which takes up to 100 targets in one SQL query (`graphclient.RelationshipBatch`).
- **Fix:** Call `graphclient.RelationshipBatch` once per page, in chunks of 100, and read `Follows` and `FollowedBy` from it. Keep the fail-closed behaviour on error.

### B9. LOW (performance): Poll lookups per row in batch hydration
- **Where:** `post-service/internal/service/post.go:2308-2311`. `GetPoll` and `GetUserPollVotes` run once per poll post, while the rest of the page is batched.
- **Fix:** Add batched `GetPollsByPostIDs` and `GetUserPollVotesBatch` store calls.

### B10. LOW-MEDIUM (resilience): GetPost always asks Redis first, and the Redis client has no tight timeouts
- **Where:** `post-service/internal/service/post_cache.go:47-49`, with the client built in `Architecture/shared/transport/redis.go:14-27`. It uses go-redis defaults (DialTimeout 5s, ReadTimeout 3s), `MaxRetries: 3` and `PoolTimeout: 30s`.
- **Example:** If Redis is unreachable and packets are silently dropped (not refused), each "best-effort" cache `Get` can block for several seconds across retries before falling back to Postgres. Every direct post open then gets that slower.
- **Fix:** Give cache reads a short context deadline (about 50-100ms) in `getCachedPostBody`, or set explicit low Dial and Read timeouts and `MaxRetries: 0` for the cache client.

### Backend: checked and found sound
- `GetPost`: checks review status (including `restricted`), the media-processing and scheduled gates, `viewerMayViewPost` (account gate via graph `view_posts`, which includes blocks in both directions; hidden authors; per-post audience; private share list), then the age gate. Members-only redaction happens in the handler. Denials are 404, not 403.
- Cache revalidation on a hit re-reads deleted, review, restriction, visibility, age and schedule from Postgres. A deleted post is dropped from cache and returns 404, so no resurrection (apart from `tier_required_id`, see B4).
- `GetPostsByIDs` / `POST /v1/posts/batch`: capped at 100 IDs. Reads Postgres directly with `deleted_at IS NULL`. Applies `canViewPosts`, `batchVisibilityAllowsEdges`, effective review status, the media and scheduled gates, the age gate, and members-only redaction. Bookmarks, reposts and repost counts are batched.
- By-author (profile feed): `canViewAuthor` gate, then the per-post audience with lazy lookups that fail closed. The SQL filters approved, live and not-scheduled rows. The limit is clamped to 50.
- `GetRecentPosts`, live recordings and hashtag pages apply `canViewPosts`. The hashtag SQL uses `ViewerEligibleSQL`.
- Comments, poll, poll results, reactions/me, reaction counts, reposters, chapters and product tags all go through `requirePostReadable`.
- The privacy gate fails closed on graph errors, missing answers and hidden-author lookup errors. Errors are not cached. The 3s TTL cache cleans itself up.
- graph-service `view_posts`: a block in either direction denies. A failed privacy fetch is treated as unknown, which means followers only. `/can` rejects more than 100 targets.
- HTTP server timeouts are set (shared/server: read 15s, header 5s, write 30s). Outbound graph clients use 5s timeouts.

---

## Part 2: Android feed client (`mobile/android`)

### A1. MEDIUM: A just-published post pinned at the top of Home or Reels stays visible after you delete it, hide it or block, and ignores edits
- **Where (Home):** `feature/feed/src/main/kotlin/com/us/android/feature/feed/ui/FeedViewModel.kt:200-201` (`_head`), `:249-253` (the head only filters paged rows), `:274-289` (`pin`). `_head` is never cleared. It is drawn directly at `FeedScreen.kt:248`, then by `stageItemAt` at `FeedScreen.kt:279` and `:410`. The `hidden.state` and `edits` overlays at `FeedViewModel.kt:235-248` apply only to the paged rows, not to `_head`. Delete goes through `core/feed/src/main/kotlin/com/us/android/core/feed/ui/more/PostMoreViewModel.kt:359` (`hidden.hidePost(postId)`).
- **Where (Reels):** `feature/feed/src/main/kotlin/com/us/android/feature/feed/ui/reels/ReelsViewModel.kt:352` (`_live`). The user's own live reel is deliberately kept through refreshes (`:385`, `:464`), and `head` at `:516-523` doesn't check `hidden.state` or edits.
- **Example:** You post, land on Home with your post pinned at page 0, long-press, choose Delete and confirm. You see "Post deleted", but the post is still on page 0 and still plays, for as long as the Home ViewModel lives (the session). Choosing "Edit post" on it also leaves the old text on screen. Reels behaves the same for a just-posted reel.
- **Fix:** In both ViewModels, combine `_head`/`_live` with `hidden.state` (set it to null when hidden) and with `edits` (apply `saved.applyTo`), or clear `_head` when `HiddenPosts` gets its id.

### A2. MEDIUM (memory): The endless Home feed keeps every loaded page in memory with no cap
- **Where:** `core/feed/src/main/kotlin/com/us/android/core/feed/data/FeedPager.kt:21-27`. `PagingConfig` sets no `maxSize`. `EndlessFeed.kt:20-36` makes For You and Following never end (primary, then fallback, then newest-first), and `cachedIn(viewModelScope)` (`FeedViewModel.kt:217-220`) holds every page. Related sets also only grow: `FeedPagingSource.kt:107` (`emittedIds`) and `FeedViewModel.kt:466` (`hydratedIds`).
- **Example:** A long scrolling session (hundreds to thousands of full-screen posts, each `FeedItem` carrying media ladders, credits, chapters and sound) grows the heap without limit. Both tabs keep their caches. On mid-range devices this ends in GC jank or an OutOfMemoryError.
- **Fix:** Set `maxSize` (for example 150-200; it must be at least `pageSize + 2*prefetchDistance`). Since the pager is keyed by post id and `enablePlaceholders=false`, check that dropping pages behind the reader doesn't jump the pager. Alternatively, cap total rows in the endless loader.

### A3. LOW (memory): `MediaDeliveryCache` never removes expired entries
- **Where:** `core/feed/src/main/kotlin/com/us/android/core/feed/data/MediaDeliveryCache.kt:26-34`. It is a `@Singleton` `ConcurrentHashMap`. `get` ignores stale entries but never deletes them, and `put` doesn't trim.
- **Example:** Every media and cover ID hydrated in a session (hashtag pages, covers on every Home page, Tube surfaces) stays for the life of the process.
- **Fix:** Remove the entry in `get` when it is stale, or use a bounded LRU (`android.util.LruCache` with about 500 entries).

### A4. LOW: Follow-status lookups for whole page snapshots repeat failed requests on every page load
- **Where:** `feature/feed/src/main/kotlin/com/us/android/feature/feed/ui/FeedViewModel.kt:493-498` and `:591-593`. `onAppendHydrated` passes `items.itemSnapshotList.items` (every loaded row) to `learnAuthors`. `core/feed/src/main/kotlin/com/us/android/core/feed/data/FollowGraph.kt:63-87` never records a failed lookup ("simply not recorded").
- **Example:** Authors whose relationship lookup keeps failing (deleted or blocked accounts returning 404, or a 5xx) get one request each on every append and every hidden-filter change (`FeedScreen.kt:674` is keyed on `itemCount`). That is one request per author on every page for the whole session.
- **Fix:** Pass only the newly appended rows (the ones `hydratedIds.add` accepted), and remember failed IDs (or back off) in `FollowGraph`.

### A5. LOW: A failed pull-to-refresh with posts already on screen gives no feedback
- **Where:** `feature/feed/src/main/kotlin/com/us/android/feature/feed/ui/FeedScreen.kt:346-353`. The error state shows only when the list is empty. `rememberFeedRefresher` (`:556-568`) just calls `landed()` on `LoadState.Error`.
- **Example:** When offline, a pull-to-refresh or a return after more than 10 minutes shows the spinner, which then disappears. Nothing says the feed is stale or offline, so the reader thinks it is up to date.
- **Fix:** On a refresh `LoadState.Error` while items are present, show a `UsMessageHost` snackbar with `feedMessage()`, as Reels does with `_refreshFailure`.

### Android: checked and found sound
- `FeedPagingSource`: removes duplicate IDs within each generation (no duplicate LazyColumn or pager key crash). `CancellationException` is rethrown. A 200 response with an `error` envelope becomes `LoadResult.Error`. An empty page ends paging, and deleted rows are dropped after that check. `getRefreshKey` is null, and each refresh starts a new source and a new dedup set, so refresh and load-more can't mix generations.
- `endlessPage`: the fallback cursor prefix and the dedup set together stop For You rows reappearing in the fallback.
- DTOs: every field has a default. The JSON config uses `ignoreUnknownKeys`, `explicitNulls=false` and `coerceInputValues`, so missing, extra, unknown-kind or null fields don't crash. `stageKind` handles unknown media. `aspectRatio` guards against 0x0.
- Players: each feed body gets its own `PlayerPool` (at most 4 players), released in `DisposableEffect` (`FeedScreen.kt:266-267`). Reels releases on its lifecycle (`ReelsScreen.kt:1834-1849`). The pool prepares again when the playback at an index changes. Playback is paused unless the screen is resumed.
- Hidden posts, blocks and deletes of paged rows are filtered at once through `HiddenPosts` (only the pinned head misses it, see A1). Edits are overlaid on paged rows.
- There is no persistent feed cache (network only, no Room), so no on-disk copy can bring back a deleted post.
- Coroutines use `viewModelScope` throughout, and the Following poll runs only while the screen is resumed.

---

## Ranked summary
1. **B1 critical:** the thread endpoint bypasses private-account, block, deactivated and age gates (confirmed by test).
2. **B2 critical:** Trending lists flagged, rejected and copyright-held posts, and private accounts' posts.
3. **B3 high:** Saved/bookmarks returns posts after their author blocks you, goes private, changes the audience, or the post is moderated.
4. **B5 high:** graph `CanBatch` fetches privacy one target at a time; post-service's 5s timeout then empties the home feed.
5. **B6 medium-high:** a Scylla error fails the whole batch hydration (500), and Scylla reads ignore the request context.
6. **B4 medium:** the body cache doesn't re-check `tier_required_id`, so members-only content leaks for up to 5 minutes.
7. **A1 medium:** the pinned just-published post survives delete, hide and edit (Home and Reels).
8. **A2 medium:** the endless Home feed has no Paging `maxSize`, so memory grows without limit.
9. **B7 medium:** serial count reads per row on the profile, recent, saved, hashtag and trending lists.
10. **B8 medium:** one relationship request per author in batch hydration.
11. **B10 low-medium:** no tight Redis timeouts on the best-effort cache.
12. **B9, A3, A4, A5 low.**
