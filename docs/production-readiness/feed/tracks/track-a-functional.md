# Track A — functional completeness (home feed)

- Audit date: 2026-11-16. Read-only. Backend and Android: `C:\workspace\modernsmapp` (working tree, branch `codex/module-01-02-launch-safety`). Web: `C:\workspace\wt\pb-echo` (`feat/echo-web` @ `ca96921`).
- Path prefixes: backend paths are relative to `Architecture/services/`; Android paths to `mobile/android/`; web paths to `C:\workspace\wt\pb-echo\`.
- Nothing here was run against a live stack, a device or a browser. Runtime behaviour is VERIFIED-READ at best. The only thing executed: `go test ./internal/service -run 'TestHomeWindow_|TestColdStart' -count=1` in `feed-service` → `ok github.com/atpost/feed-service/internal/service 0.363s`.

## 1. Feature status table

| Feature | Backend | Android | Web | Evidence |
|---|---|---|---|---|
| For You feed | COMPLETE | COMPLETE | COMPLETE | `feed-service/internal/http/handler.go:129-219`, `feed.go:368-615`; Android `FeedRepository.kt:22-27` sends `feed_mode=ranked`; web `useFeedPosts.ts:90-97`, `Feed.tsx:105` |
| Following feed | COMPLETE | COMPLETE (runs on into For You when it ends, never shows an empty Following) | PARTIAL (A-2) | `feed.go:492-502` (fails closed); Android `FeedViewModel.kt` `endlessFeed(Following, ForYou)`; web `Feed.tsx:99-106` |
| Ranking | COMPLETE (reorders the page only) | n/a | n/a | `feed.go:578-596`, `ranking/ranker.go:63-101` |
| Fallback when the ranker fails | COMPLETE (VERIFIED-READ); how often the 20 ms budget is missed in production is UNVERIFIED | n/a | n/a | `feed.go:584-591` (error → chronological order of the same window, `rankFallbacks` counter); budget `cmd/server/main.go:133` |
| Cursor pagination, stable under new posts | COMPLETE | COMPLETE | COMPLETE | keyset on the row's timeuuid: `feed.go:318-343`, `store/scylla/timelines.go:378-399,547-567`; opaque token `handler.go:576-598`; tests ran green |
| Short or empty pages | PARTIAL (A-2: page is not refilled after filters) | PARTIAL (stops after 5 empty pages) | BROKEN on an empty first page (A-2) | see A-2 |
| Pull to refresh | n/a | COMPLETE | COMPLETE (no pull gesture; "Show posts" + refresh on return) | Android `FeedScreen.kt:342-365,566-629`; web `Feed.tsx:182-197` |
| Post types | emits post, poll, long_video, voice, repost rows (flick/reel and notes dropped from home) | PARTIAL (voice A-3, repost attribution A-4) | PARTIAL (voice SUSPECTED A-3) | `post-service/internal/service/post_content_types.go:36-39`; `feed.go:541-552`; Android `FeedStageRules.kt:79-91`; web `PostCard.tsx:100,343,353,367` |
| Unknown post type | n/a | COMPLETE (falls to photo / poll / text by content, never crashes) | COMPLETE (generic card) | Android `FeedStageRules.kt:86-91`, `NetworkModule.kt:86-88`; web `PostCard.tsx` has no type switch that throws |
| Like | COMPLETE (`/reactions` idempotent, `/react` toggle) | COMPLETE | BROKEN after a refetch inside 5 minutes (A-1) | `post_reactions.go:20-84,163-225`; Android `EngagementApi.kt:58-66`; web `useFeedReaction.ts:38` |
| Comment | route exists; UNVERIFIED end to end | UNVERIFIED | UNVERIFIED | `post-service/internal/http/handler.go:146-147`; not walked (see section 6) |
| Share / repost | routes exist; UNVERIFIED end to end | PARTIAL (repost row shown without "reposted by", A-4) | COMPLETE for render (VERIFIED-READ `PostCard.tsx:98,367`); write path UNVERIFIED | `reposts_handler.go:26-27`, `handler.go:145` |
| Save | COMPLETE | COMPLETE | COMPLETE (but each save refetches every loaded page and triggers A-1) | `post_engagement.go:22-42`; Android `EngagementApi.kt:71-75`; web `usePostActions.ts:167-181` |
| Follow from the feed | COMPLETE | COMPLETE | MISSING on the card (only in the "People you may know" strip) | graph outbox `graph-service/internal/store/pair_atomic.go:175-176`; Android `FollowGraph.kt` follow(); web `PostCard.tsx` has no follow control (grep `follow` → 0 matches) |
| Optimistic update + rollback | n/a | COMPLETE | COMPLETE for the tap; reconciliation wrong (A-1) | Android `EngagementStore.kt` drain()/reconcile(); web `PostHeartButton.tsx:30-45`, `PostCard.tsx:156-170` |
| Loading / error / empty states | n/a | COMPLETE | COMPLETE except A-2 | Android `FeedScreen.kt:344-356,636-656`; web `Feed.tsx:277-348` |
| Offline state | n/a | PARTIAL (error copy only; no cached feed, by design `FeedPagingSource.kt` header comment) | PARTIAL (generic "check your connection"; no offline detection) | same lines |
| New user, no follows | PARTIAL (one page only, A-6) | PARTIAL | PARTIAL | `feed.go:289-294,511-536` |
| Seen-content de-duplication | PARTIAL: only discovery items are remembered (48 h); timeline posts are re-served on every refresh; `post_impressions` is written and never read by the home read | client de-dup by post id within one load | client de-dup by post id | `discovery.go:184-257`; `store/postgres/meta.go:105-109`; Android `FeedPagingSource.kt:125`; web `feedPresentation.ts:28-37` |

## 2. Specific checks

**Client parameters vs. server.** Match. Server reads `limit, feed_mode, exclude_self, circle_only, following_only, platform, cursor` (`handler.go:137-160`). Web sends `limit=20, feed_mode, platform=postbook, following_only, cursor` (`useFeedPosts.ts:90-97`). Android sends `limit, cursor, following_only, circle_only, feed_mode` and no `platform` (`FeedApi.kt:53-61`). Both replay `meta.next_cursor` verbatim. `feed_mode` values: `ranked` / `chronological` on both; anything else becomes chronological (`handler.go:151-153`). Delta: both send `feed_type` + `anchor`, read `new_count` / `newest_anchor` / `has_more` (`handler_delta.go:28-63`, `FeedDtos.kt` `FeedDeltaDto`, `useFeedDelta.ts:74-88`).

**Response fields vs. clients.** Android reads `viewer_queued`, `viewer_disliked`, `credits`, `genres`, `chapters`, `copyright_review`, `tier_required_price_paise`, `source` from a feed row; feed-service's `HydratedPost` (`hydration.go:40-253`) does not declare them, so they are always absent on `/v1/feed/home` and decode to defaults. No crash (`coerceInputValues = true`, `explicitNulls = false`, `ignoreUnknownKeys = true`, `core/network/.../NetworkModule.kt:86-88`). Android does NOT read `is_repost` / `reposted_by` (A-4). Web reads `viewer_reaction` (omitted when none → not liked, `postHeart.ts:14-21`), `counts`, `is_bookmarked`, `is_repost`, `reposted_by`, `poll`, `tier_required_id`, `body_redacted` — all emitted.

**Where the count on a card comes from.** `counts.likes` = Scylla `social_engagement.post_counters.like_count`; `counts.comments/shares/saves` = PostgreSQL `post_engagement_counts`; `viewer_reaction` = Scylla `reactions_by_post`; `is_bookmarked` = PostgreSQL (`post-service/internal/service/comment_counts.go:16-47,123-150`, `post_reads.go:371-398`). feed-service then caches the whole row per (viewer, post) for 5 minutes (`hydration.go:25,912-953`). So other people's likes and comments show up to 5 minutes late on a refetch; the viewer's own action is invalidated only on some routes (A-1). The like count the web shows right after a tap comes from a different store (A-5).

## 3. Findings

```
ID:            A-1
Title:         A like made on the web through POST /v1/posts/:id/react is not removed from feed-service's 5-minute hydration cache, so the next feed refetch shows the post un-liked with the old count, and a second tap then really removes the like.
Severity:      P1
Category:      functional
Evidence level: VERIFIED-READ
Evidence:      post-service/internal/service/post_reactions.go:163-225 (ToggleReaction: no invalidateFeedHydration call) vs :41 (React) and :97 (Unreact) and post_engagement.go:179 (ToggleLike) which do call it; route post-service/internal/http/handler.go:259; cache feed-service/internal/service/hydration.go:25 (5 min), :407-476 (cache-only path returns the cached viewer_reaction); web caller pb-echo/src/hooks/useFeedReaction.ts:38; web refetch triggers: usePostActions.ts:94,133,155,176 (every save, delete, pin invalidates ["home-feed"]), useBlocking.ts:43, Feed.tsx:182-197 (Show posts, return refresh), useFeedPosts.ts:184 (after creating a post). No test covers it (grep for a ToggleReaction invalidation test: none).
How to reproduce: On web /feed, like post P. Within 5 minutes save any other post (or press "Show posts", or leave the tab for a minute and return). GET /v1/feed/home returns P from Redis key feed:hydrate:<viewer>:<P> with no viewer_reaction and the old counts.likes; the heart is empty again. Tap it: /react toggles the PostgreSQL row OFF and the like is gone.
Impact:        The most common action on the feed appears to undo itself, and the user's natural correction deletes the like. Android is not affected (it uses /reactions, which invalidates).
Fix:           Add s.invalidateFeedHydration(ctx, userID, postID) in ToggleReaction after the Scylla sync (one line, post_reactions.go ~line 213).
Acceptance:    Unit test in post-service: seed Redis (miniredis) with feed:hydrate:<u>:<p>, call ToggleReaction, assert the key is gone. Fails now.
Effort:        S
```

```
ID:            A-2
Title:         GET /v1/feed/home does not refill a page after its filters, so a Following page whose newest ~30 timeline rows are reels or other filtered rows comes back empty with a cursor; the web then shows "No posts yet" with no way forward, and Android ends the list after five such pages.
Severity:      P1
Category:      functional
Evidence level: VERIFIED-READ (server half VERIFIED-RUN: TestHomeWindow_FilteredOutPageStillAdvances passes, i.e. an empty page with a cursor is the designed output)
Evidence:      feed-service/internal/service/feed.go:383-389 (one read of limit+10 rows), :541-552 (reels, notes and main_feed=false rows dropped after the read; reels and long videos share home_timeline_by_user), :606-614 (empty page + cursor returned); handler.go:189-202 (platform=postbook filter and every hydration-tail filter run after the page is cut). Web: pb-echo/src/components/Feed.tsx:210 (autoLoad needs posts.length > 0), :321 (empty state shown when posts.length === 0), :370 (Load more and the sentinel rendered only when posts.length > 0). Android: core/feed/.../FeedPagingSource.kt:166-179 (list ends at 5 empty pages in a row; with page size 15 that is 125 consecutive filtered rows).
How to reproduce: Viewer follows accounts whose 30 most recent posts are all flicks (or are all hidden by keyword filter / not-interested / deleted). Open web /feed → Following. Response: `{"data":[],"meta":{"next_cursor":"…"}}`. Screen: "No posts yet — Discover people", although older posts from followed accounts exist. For You hides it only while discovery has something to add.
Impact:        Following looks empty for exactly the users who follow the app's Shorts creators; on Android older posts become unreachable behind a long run of reels. Pages are also routinely shorter than `limit` for the same reason.
Fix:           In GetHomeFeed, when the cut window holds fewer than pageLimit items and the raw read was full, read the next window (cursor = last raw row) and append, up to a small bound (e.g. 5 reads) — the loop the category surfaces already use (service/category.go). Web belt-and-braces: render the sentinel / "Load more" whenever feed.hasNextPage, and do not show the empty state while hasNextPage is true (Feed.tsx:210,321,370).
Acceptance:    Service test: a timeline of 40 flick rows followed by 5 post rows, limit 20 → first page returns the 5 posts. Web test: a first page `{data: [], meta:{next_cursor}}` renders no "No posts yet" and requests the next page.
Effort:        M
```

```
ID:            A-3
Title:         Voice posts reach the home feed but neither home client has an audio renderer; on Android a voice post is treated as a photo page.
Severity:      P2
Category:      functional
Evidence level: VERIFIED-READ (Android); SUSPECTED (web — only PostCard.tsx was searched; a child media component could handle audio; open a voice post on /feed to confirm)
Evidence:      post-service/internal/service/post_content_types.go:36-39 ("voice" is valid), post_create.go:500; Android can create one: feature/post/.../CreatePostDtos.kt:344-354; home drops only short-form and notes (feed.go:541-547); Android feature/feed/.../FeedStageRules.kt:86-91 (any non-video media → PHOTO, handed to the image loader), no "voice"/"audio" match anywhere under feature/feed or core/feed/data; web PostCard.tsx has no "voice"/"audio" match.
How to reproduce: Publish a voice post from Android; open Home as a follower.
Impact:        A supported post type shows as a blank picture with a caption and cannot be played from the feed.
Fix:           Smallest: keep voice out of Home until a player exists (add "voice" to the home drop next to dropNotes in feed.go:547 and delta.go), or add a VOICE StageKind with the existing audio player.
Acceptance:    Service test: a voice row in the timeline is absent from GetHomeFeed (or UI test: a voice item maps to StageKind.VOICE).
Effort:        S (hide) / M (render)
```

```
ID:            A-4
Title:         Android ignores is_repost / reposted_by, so a repost in Home is shown as an ordinary post by its original author, and both clients silently drop a repost whose original is already on screen.
Severity:      P2
Category:      functional
Evidence level: VERIFIED-READ
Evidence:      server emits them: feed-service/internal/service/hydration.go:221-223, :831-851 (a repost row keeps the ORIGINAL post id); Android core/feed/.../dto/FeedDtos.kt (FeedItemDto has no is_repost / reposted_by field; grep for is_repost|reposted_by|isRepost under core/feed and core/model returns only isRepostable); Android de-dup by post id FeedPagingSource.kt:125; web de-dup feedPresentation.ts:28-37; web does render the banner PostCard.tsx:98,367.
How to reproduce: A follows B; B reposts C's post; A (not following C) opens Home on Android: C's post appears with no sign of why.
Impact:        "Why am I seeing this stranger" on Android; reposts lose their meaning. Not a privacy leak (visibility is checked at hydration).
Fix:           Add `is_repost` and `reposted_by` to FeedItemDto and FeedItem, and draw "Reposted by …" on the stage.
Acceptance:    FeedItemDto decode test: `{"is_repost":true,"reposted_by":"<id>"}` maps to a domain item carrying the reposter.
Effort:        S
```

```
ID:            A-5
Title:         The like count the web shows after a tap is COUNT(*) of PostgreSQL `reactions`, while the count on every feed card is Scylla `post_counters.like_count`; the two are kept in step only by a best-effort write that is logged and dropped on failure.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ for the two sources; SUSPECTED for the size of the drift (compare `SELECT count(*) FROM reactions WHERE target_type='post' AND target_id=…` with `post_counters.like_count` for a sample of posts on dev)
Evidence:      post_reactions.go:200-218 (Scylla sync failure → log only; counts returned from pgStore.GetReactionCounts), store/postgres/reactions.go:136-170; card source comment_counts.go:16-47 (its own comment: "likes have their own, separate inconsistency (five stores)"); web writes the PG total onto the card: useFeedReaction.ts:17-19; a third counter (Redis post:eng) is returned by POST /like (engagement/lua.go:44-70).
How to reproduce: Like a post on web whose Scylla counter and PG row count differ; the number jumps to the PG value on tap and back to the Scylla value on the next refetch.
Impact:        Visible count jumps; no money or access consequence on the home feed.
Fix:           Return the Scylla counter (countsForPost) from ToggleReaction so tap and card read one store; make the Scylla sync failure an error the client retries, or add the existing reconciler to like counts.
Acceptance:    ToggleReaction test with a counter seam: the returned likes equals the seam's like_count, not the PG row count.
Effort:        S
```

```
ID:            A-6
Title:         A viewer who follows nobody gets one page of For You (at most `limit` posts) and then the feed ends; cold-start fill runs on the first page only and discovery is capped at 20 % of a page.
Severity:      P2
Category:      functional
Evidence level: VERIFIED-READ
Evidence:      feed-service/internal/service/feed.go:289-294 (firstPage only), :513 (limit*2 fetched), :318-343 (cold-start items carry no cursor → next = ""), discovery.go:102-114 and :118-125 (no discovery on a cold-start page). Android then falls back to the chronological timeline (FeedViewModel.kt NEWEST_FIRST), which for this viewer is empty or their own posts; web shows "You're caught up on loaded posts" (Feed.tsx:393-398).
How to reproduce: New account, no follows: GET /v1/feed/home?feed_mode=ranked&limit=20 → up to 20 posts, no meta.next_cursor.
Impact:        The first session of every new user ends after about 20 posts (30 on Android's first load).
Fix:           Give the cold-start fill its own cursor (created_at of the last public post, passed to /v1/posts/recent) so it pages, or raise the first fill and let discovery fill a whole page when the timeline has nothing from others.
Acceptance:    Service test: viewer with an empty timeline and 60 public posts available walks 3 pages without repeats.
Effort:        M
```

```
ID:            A-7
Title:         On the web a newly created post is put at the top of For You and the feed is refetched in the same tick, before fan-out has written the timeline row, so the refetch can replace the page without the new post.
Severity:      P2
Category:      functional
Evidence level: SUSPECTED (timing: needs one run on dev — create a post on /feed and watch whether the card disappears; depends on outbox + Kafka latency vs. one GET /v1/feed/home)
Evidence:      pb-echo/src/hooks/useFeedPosts.ts:139-186 (setQueriesData then invalidateQueries(["home-feed"]) immediately; nothing schedules a later refetch); the code's own comment says the refetch "fires first and still loses"; the author's row is written by the Kafka consumer (feed-service/internal/events/consumer.go:455-529, feed.go:1150).
How to reproduce: Create a text post from /feed with the network tab open.
Impact:        "I posted and it vanished" until the next refresh. Android is not affected (it pins the post fetched by id, FeedViewModel.kt pin()).
Fix:           Keep the optimistic row across the refetch (merge it back in `select` until a server page contains its id), or delay the invalidation by ~2 s.
Acceptance:    Hook test: after create, a refetch that returns a page without the id still yields the post first.
Effort:        S
```

```
ID:            A-8
Title:         Q&A questions are fanned out into home timelines under their question id, but home hydration only asks post-service's post batch, so those rows can never render and only shorten pages.
Severity:      P3
Category:      functional
Evidence level: SUSPECTED (confirm that a question id is not a row in post-service `posts`, and whether the Q&A producer is live at launch)
Evidence:      feed-service/internal/events/consumer.go:412-433, feed.go:1673-1686 (rows with content_type qa_question); hydration.go:480-520, :835-840 (ids missing from the batch answer are skipped); no other reference to qa_question in feed-service or post-service read paths (grep).
How to reproduce: Create a question, read the author's home timeline row count vs. the rendered page.
Impact:        Dead rows; feeds A-2.
Fix:           Drop qa_question rows in the home read next to dropNotes, or stop the fan-out until a renderer exists.
Acceptance:    Service test: a qa_question row is not counted toward the page window.
Effort:        S
```

```
ID:            A-9
Title:         Notifications in other users' inboxes that point at a deleted post are not removed; only the author's inbox is cleaned.
Severity:      P3
Category:      functional
Evidence level: VERIFIED-READ (stated in the code); client handling of the resulting 404 UNVERIFIED
Evidence:      notification-service/internal/events/consumer.go:479-495 (comment: rows in other inboxes "stay and the client resolves a 404").
How to reproduce: Mention a user in a post, delete the post, open the mention from the notification list.
Impact:        A dead notification that opens "content no longer available".
Fix:           Accept and document, or index notifications by entity id for delete.
Acceptance:    n/a if accepted.
Effort:        L (index) / none
```

## 4. Journeys (walked by reading; none executed)

1. **Create a post, see it in your own and a follower's feed.** Server path sound, VERIFIED-READ: create → outbox `post.created` → `handlePostCreated` (`events/consumer.go:455-529`) → `FanoutPost` writes the author's own home row, then followers and connections in resumable chunks, errors returned so the event retries (`feed.go:1128-1318`). Android shows it at once by fetching the post by id and pinning it (`FeedViewModel.kt` `pin()`). Web: A-7 (SUSPECTED). Fan-out latency and the outbox relay were not measured: UNVERIFIED.
2. **Follow an account and see its content.** VERIFIED-READ: follow commits a `UserFollowed` outbox event (`graph-service/internal/store/pair_atomic.go:175-176`); feed-service drops the cached following list and backfills up to 50 posts from the last 3 months (`events/consumer.go:225-297`). Limits: nothing older than 3 monthly buckets or beyond 50 posts is backfilled; a private account's follow is a request and backfills only on approval (`follow_requests.go:109`). A single failed `AddToHomeTimeline` in the backfill is logged, not retried (`consumer.go:289-296`) — minor.
3. **Block an account; it disappears everywhere.** Home feed: sound, VERIFIED-READ. The block set is symmetric (`graph-service/internal/store/graph.go:1073-1080`), read uncached on every request and failing closed (`feed.go:446-450,1369-1405,1442-1469`); it also covers the cold-start fill (`feed.go:525`), discovery (`discovery.go:209`), the delta pill (`delta.go:128-151`) and reposts of a blocked original (`repostfilter.go:24`). Already loaded pages: web refetches the feed on block (`useBlocking.ts:36-45`); Android hides the author's rows through the session overlay when the block is made from the post's More sheet (`core/feed/.../PostMoreViewModel.kt:269-294`). A block made from a profile screen on Android does not call `hideAuthor` (only PostMoreViewModel does) → the cached feed keeps the rows until refresh: SUSPECTED, P3. Search, comments, notifications, chat: not walked — UNVERIFIED (other tracks/modules).
4. **Delete a post; gone from feeds, search, notifications.** VERIFIED-READ: soft delete emits `PostDeleted` in the delete transaction (`post-service/internal/store/postgres/my_uploads.go:120-130`); feed-service writes a tombstone first, removes every timeline row through the reverse index and drops cached rows (`events/post_lifecycle.go:41-80,240-261`); the batch endpoint drops deleted posts, so a surviving row cannot render (`hydration.go:835-840`); search removes the projection (`search-service/internal/events/consumer.go:771-790`); notifications: author inbox only (A-9). Pages already on another user's screen keep the card until refresh.
5. **Scroll 20 pages while posts are created.** No duplicates or gaps from new posts, VERIFIED-READ + unit tests run: the cursor is the exact timeuuid of the last row served, new rows sort above it (`feed.go:318-343`, `timelines.go:110-135,378-399`; `TestHomeWindow_WalkServesEveryEligiblePostExactlyOnce` passes). Discovery items are remembered per viewer so they do not repeat (`discovery.go:245-257`); both clients also de-duplicate by id. Known gap: a row written late with an old `created_at` (follow backfill, restore, slow fan-out) below a cursor already passed is missed until refresh — acceptable. Not run against Scylla: UNVERIFIED at runtime.
6. **Double-tap like 50 times concurrently.** Android: the count moves by one, VERIFIED-READ. The client serialises taps per post and sends idempotent set/unset (`EngagementStore.kt` `toggle()`/`drain()`); the server records the reaction with `INSERT … IF NOT EXISTS` and increments only on the winning insert (`post-service/internal/store/scylla/interactions.go:42-71`). Web: `/react` is a toggle; the button and a per-post lock stop concurrent sends from one tab (`PostHeartButton.tsx:30-33`, `useFeedReaction.ts:33-36`), but 50 concurrent requests sent directly would land on an unpredictable parity (the SELECT-then-INSERT in `reactions.go:63-110` has no row lock; the unique key turns the race into a 500, not a double count). Count never moves by more than one. Not executed: UNVERIFIED at runtime.

## 5. Checked and sound

- Home narrowing filters fail closed and never backfill strangers under Following/Friends: `feed.go:289-294,464-502`; tests `TestColdStart_NotAllowedWhenNarrowed*` ran green.
- Cold start does not stop when the viewer's own posts make the timeline non-empty: `feed.go:358-366,511`; `TestColdStartRunsForAFeedOfOnlyYourOwnPosts` ran green.
- Page cut before ranking; ranker failure or timeout keeps the same window in timeline order: `feed.go:578-596,622-637`.
- Cursor: opaque `v1:<timeuuid>` token, validated, legacy RFC3339 still accepted, bad cursor → 400: `handler.go:247-259,576-598`.
- Hydration failure is a 502, never bare ids: `handler.go:179-184`.
- Timeline writes are idempotent upserts (key is a function of post and created_at): `timelines.go:131-135,153-177`.
- Deleted-post tombstone and stale marker are checked on every cached row, failing closed: `deleted_tombstone.go:78-109`.
- Viewer mutations through `/reactions`, `/like`, `/bookmark`, repost and saved collections invalidate the viewer's cached row: `post_reactions.go:41,97`, `post_engagement.go:30,41,179`, `post_reposts.go:154,207`, `saved_collections.go:298` (assumes feed-service and post-service use the same Redis — both read `REDIS_ADDR`; the production value is a secret reference, UNVERIFIED).
- Like is exactly-once per user on the Scylla path: `interactions.go:42-91`.
- Block set symmetric, uncached, fail closed: `graph.go:1073-1099`, `feed.go:1442-1469`.
- Unfollow purges only rows the viewer no longer has a claim to, and fails closed: `events/consumer.go:315-375`.
- Android: empty page with a cursor pages on; echoed cursor cannot loop: `FeedPagingSource.kt:166-175`; soft-deleted rows filtered: `FeedPagingSource.kt:197-203`.
- Android: optimistic like/save/repost with rollback, retry, account-switch fence, and reconcile only on fresh rows: `EngagementStore.kt` (whole file), `FeedViewModel.kt` `onRefreshHydrated`/`onAppendHydrated`.
- Android: follow is optimistic with rollback and handles `requested`: `FollowGraph.kt` follow()/unfollow().
- Android: loading, error, empty, append-retry and pull-to-refresh states exist: `FeedScreen.kt:344-365,636-656`.
- Web: request cancellation, de-dup across pages, first-page-only refresh on "Show posts": `useFeedPosts.ts:99-114`, `Feed.tsx:115,182-194`.
- Web: delete, hide and block remove or refetch cached rows: `usePostActions.ts:92-96`, `removePostFromPages.ts`, `useBlocking.ts:36-45`.
- Delta contract and anchor handling match both clients; the pill counts the same filtered set as the page: `handler_delta.go:28-87`, `delta.go:113-169`.

## 6. Not verified

- Anything at runtime: no stack, device or browser was used. Fan-out latency, ranker fallback rate, Scylla paging under load, A-7's timing.
- Comment add/list/delete from a feed card on both clients, and the share dialog / repost write path (routes exist; client code not walked).
- Web media component for non-image/video kinds (A-3 web half).
- Block, mute and delete effects in search, comments, notifications and chat beyond the lines cited.
- Whether production feed-service and post-service share one Redis (needed for every cache invalidation above).
- Celeb pull (`FEED_CELEB_PULL`, on by default, `celebpull.go:62-79`): the merge of pulled rows into the page window was not walked.
- Which web branch ships (map, section 0): all web results are for `feat/echo-web` only.

## 7. Out of scope observations

- `POST /v1/posts/:id/like` (used by `pb-echo/src/hooks/usePostReaction.ts:18` and Echo) decides liked/unliked from a Redis key with a 24 h TTL (`post-service/internal/engagement/lua.go:44-70`): the first "unlike" tap a day after liking re-likes instead.
- Web `useImpressionTracker.ts:31-35` posts `signal: "impression"` to `/v1/feed/signal`, which accepts only `see_less|see_more` (`handler.go:684-687`); the hook has no importer, dead code.
- Web `PostCard.tsx:97-98` fetches each author's profile per card although the feed row embeds `author`; Android `FollowGraph.ensureKnown` asks one relationship per author — one request per card on both clients (Track D).
- Android For You does not send `platform=postbook`, web does: the two clients can show different sets of long videos in Home (`handler.go:189-202`).
- The old audit file `FEED_AUDIT.md` was not used for any claim above.
