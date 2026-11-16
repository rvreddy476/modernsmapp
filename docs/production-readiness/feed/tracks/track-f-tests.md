# Track F: test coverage against the user journeys

- Audit date: 2026-11-16. Read-only. Backend and Android tree `C:\workspace\modernsmapp` (branch `codex/module-01-02-launch-safety`, dirty working tree); web tree `C:\workspace\wt\pb-echo` (`feat/echo-web`).
- Backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Android paths are relative to `mobile/android/`. Web paths are relative to the web tree.
- Evidence levels: VERIFIED-RUN (I ran it), VERIFIED-READ (I opened the test and the code), SUSPECTED, UNVERIFIED.

## Verdict for this track

No journey has a working automated end-to-end test. The one cross-service journey test that exists polls a route feed-service does not serve. Unit coverage of the feed's read-time privacy filters and fan-out audience rules is strong and I confirmed a sample by running it. The like path (journey 6) has no server test at any level, and feed-service has no test that touches a real Scylla or Kafka.

## 1. Coverage matrix: journeys

"Integration" means `-tags integration`. In CI: post-service and graph-service are in `scripts/ci/integration-services.yaml` (lines 41 and 76, wave 1) and run in `integration-all.yml`. feed-service is not in the manifest and has no integration-tagged test file. Scylla and Kafka are listed as `optional` for post-service (line 45), so any test needing them skips.

| Journey | Backend unit | Backend integration | Cross-service / e2e | Android unit | Web unit | Web e2e |
|---|---|---|---|---|---|---|
| 1. Create post, see it in own and follower's feed | Audience of the fan-out: `feed-service/internal/service/listingvisibility_test.go:16,37`; `post_screens_test.go:53`; subscribers `fanout_subscribers_test.go:126`. Uses fake stores, no Scylla. No behaviour test of `handlePostCreated` (only a call-order check for notes, `events/notes_wiring_test.go:33`). | NONE for feed-service. | `Architecture/tools/integration/social_e2e_test.go:23` exists but cannot pass (F-1). `scripts/smoke/dev-smoke.mjs:476-508` creates a **private** post and reads `/v1/feed/home` for shape only; it never checks that the post is in a feed. | Row decoding `core/feed/.../FeedContractTest.kt:34`; no test that a created post is inserted in the list (`FeedViewModel.kt` has no test file). | `src/hooks/__tests__/feedPostFromCreate.test.ts` (not opened in detail). | NONE (no Playwright config or spec in the tree). |
| 2. Follow an account, see its content | Reason label `service/reason_following_test.go:115`; following narrowing `coldstart_narrowing_test.go:84`. No behaviour test of `handleUserFollowed` backfill. | graph-service follow store: `graph-service/internal/store/follow_requests_integration_test.go:47`, `pair_atomic_integration_test.go:192` (runs in CI). Feed side NONE. | Same broken test as journey 1 (follow happens before the post, so backfill-on-follow is not exercised even on paper). | `core/feed/.../FollowGraphTest.kt` (11 tests, not opened). | NONE found for feed. | NONE |
| 3. Block an account, gone everywhere incl. cached pages | Strong at read time: `service/blocksafety_test.go:34,66,96-140`; every surface resolves the block set `feedsurface_safety_test.go:28`; reposts `feedmutes_test.go:129,142`, `repost_hydration_test.go:15` (fresh and cached path); pagination with a blocked author `home_pagination_test.go:80`. | graph-service block store: `block_symmetry_integration_test.go:72`, `pair_atomic_integration_test.go:93,228,293` (in CI). | NONE. No test blocks through graph-service and then reads a feed. | Client-side removal: `feature/feed/.../more/PostMoreViewModelTest.kt:388,476`. | Cache removal helper `src/components/feed/__tests__/removePostFromPages.test.ts:36,42,107`. | NONE |
| 4. Delete a post, gone from feeds, search, notifications | Cached and fresh hydration: `service/deleted_tombstone_test.go:22,41,61`; delta `delta_test.go:67`. No behaviour test of `handlePostDeleted` (`events/post_lifecycle.go:41`). Search: `search-service/internal/events/soft_delete_test.go:18,80`, `eligibility_test.go:577` (names only, not opened). Notifications: only the Echo-note case `notification-service/internal/events/note_consumer_test.go:208`. | Owner-only delete: `post-service/internal/service/delete_post_owner_integration_test.go:27` (in CI). | NONE. dev-smoke deletes its own post as cleanup (`dev-smoke.mjs:785`) and does not check it is gone. | `PostMoreViewModelTest.kt:600,623,635,647`; `core/engagement/.../PostLifecycleContractTest.kt:61`; `FeedContractTest.kt:286`. | `removePostFromPages.test.ts:90-134`; `postControls.test.tsx:41`. | NONE |
| 5. Scroll 20 pages with concurrent creation, no duplicates or gaps | `service/home_pagination_test.go:80` walks 300 rows on a **static** timeline, limits 1/7/20, with shuffled ranking and same-millisecond keys; `:137,157,170`; cursor `http/ranked_pagination_test.go:19,37`; key stability `store/scylla/timeline_ts_test.go:13,48`. No test inserts rows between pages; no test pages across the celeb pull-merge (`celebpull_test.go:20` merges one page only). | NONE | NONE | `core/feed/.../FeedPagingDeduplicationTest.kt:111,127`; `FeedPagingBoundsTest.kt:152-222`. | `feedPresentation.test.ts:12`. | NONE |
| 6. Like 50 times concurrently, count moves by one | NONE. `post-service/internal/engagement/` (Lua toggle, counters, reconcile, hydrate) and `post-service/internal/store/scylla/` have zero test files. `heart_only_test.go:68,96` only checks which reaction names are accepted. | NONE for post likes. (`store/postgres/comment_replies_reactions_integration_test.go:295` is comments.) | The broken e2e test likes once and asserts count >= 1. | Client lane serialisation: `core/engagement/.../EngagementStoreTest.kt:127,149,239`. | Pure toggle maths `postHeart.test.tsx:33,36,40`. | NONE |

## 2. Coverage matrix: specific checks

| Check | Covered by | Gap |
|---|---|---|
| Blocked author never appears | `blocksafety_test.go:34,66`; fail closed `:96,119,132`; `feedsurface_safety_test.go:28` | Unit only. Comments by a blocked author inside a post's comment list: no test found and no block filter found in `post-service/internal/service/post_comments.go` (UNVERIFIED, see F-5). |
| Muted author | `feedmutes_test.go:39,53`; `feedback_test.go:416,457,500`; `muted_author_window_test.go:190` | Unit only. |
| Private account's posts | `privacyfilter_test.go:56,85,105`; post-service `privacy_gate_test.go:72,243` | Unit only. |
| Deleted post | `deleted_tombstone_test.go:22,41,61` | Consumer handler not tested (F-6). |
| Suspended / deactivated author | Hidden authors: `hidefilter_test.go:14,36`; post-service `privacy_gate_test.go:309,384`; publish refused for suspended authors `publish_standing_test.go:146` | No feed test where an author is suspended after posting. UNVERIFIED (F-10). |
| Through reposts | `feedmutes_test.go:129,142`; `repost_hydration_test.go:15`; `privacyfilter_test.go:85`; `processingfilter_test.go:40` | Unit only. |
| Through quoted posts | Only Echo quoted notes `keywordfilter_quoted_test.go:31`. Home rows carry no quoted-post field other than `quoted_note` (`hydration.go:218`). | Out of scope (Echo) unless home shows quotes. |
| Through counts and delta | `delta_test.go:16`, `delta_openable_test.go:17` | Unit only. |
| Followers / friends / private / unlisted reach only the audience: fan-out | `listingvisibility_test.go:16,37`; `post_screens_test.go:53,79` | Fake stores. |
| Same: hydration | `listingvisibility_test.go:53`; post-service `batch_visibility_test.go:20,45`, `post_screen_fields_test.go:253,280,316`, `thread_audience_test.go:70,93` | Unit only; no cross-service test (the old audit's batch-route leak has no regression test through the gateway). |
| Visibility edit re-derives rows and cache | `events/stale_marker_test.go:39,59,74`; `visibility_reason_test.go:17`; post-service `store/postgres/feed_reads_guard_test.go:74` | Unit only. |
| Cache keys per viewer | `hydration_index_test.go:15` (another viewer's row untouched); `privacyfilter_test.go:159` (different viewer does not share the answer); members rows never cached `hydration_members_test.go:89,157` | Sound at unit level. |
| Large-audience fan-out (celeb threshold, pull-merge) | `celebpull_test.go:20,56,80`; `fanout_chunks_test.go:178,204,240`; `store/postgres/celeb_test.go:12` | No test of the read path merging pulled rows across several pages with a cursor (F-7). |
| Seen-content deduplication | Discovery offered-set only: `discovery_test.go:89,193,238` | The home timeline itself has no seen-content dedup in code (`ExcludeSeen` is called only from `ranking/ranker.go:74` for the related-videos seed path). NONE; flag to the correctness track. |
| Engagement counts match source of truth | `post_screens_test.go:177` (counts carried through); post-service `list_reads_test.go:228,392`; `comment_counts_test.go:56`, `comment_counts_integration_test.go:49,166` | Like counts: NONE (F-2, F-3). |
| Ranker failure fallback | NONE for home (`service/feed.go:584-588`). | F-8. |
| Consumer redelivery idempotency | Shared consumer: `Architecture/shared/kafka/consumer_dedupe_test.go:106,134,168,199`; fan-out resume `fanout_chunks_test.go:118,159`; purge `purge/purge_test.go:61` | Feed handlers are not run twice in any test. |
| DLQ | `shared/kafka/consumer_durability_test.go:12,22,39` cover classification and the retry loop | No test that an exhausted or poison message is written to the DLQ topic and then committed (F-9). |
| Object-level authorization: delete | `delete_post_owner_integration_test.go:27` (stranger gets `ErrPostForbidden`, row stays) | Sound. |
| Same: comment edit/delete | `store/postgres/comment_author_only_integration_test.go:23` (name read only) | Not opened. |
| Same: bookmark / saved | `saved_collections_test.go:327,508`; `list_reads_test.go:141` | Not opened beyond names. |
| Same: like on a post the viewer may not see | `end_screens_integration_test.go:538-544` calls `loadPostForEngagement` for private/unlisted | Indirect; no route-level test of `POST /:postId/like` for a non-visible post. |
| Gateway cannot be bypassed with a forged X-User-Id | `api-gateway/cmd/server/identity_strip_test.go:46` (VERIFIED-RUN, passes), `:113`; `edge_internal_test.go:146,222,289`; contract `integration/contracts/edge-auth/contract_test.go:88,121,177`; feed-service refuses to boot without the internal key outside dev `feed-service/cmd/server/internalkey_test.go:7` | No single test sends a valid token for user A plus `X-User-Id: B` to a feed or post route and checks the upstream sees A; `edge_internal_test.go:823` does this for the permissions route (line read, test body not opened). |
| Client: optimistic like rollback | Android `EngagementStoreTest.kt:174,196,217`. Web: pure functions only, `postHeart.test.tsx:33-40` | Web hook `src/hooks/useFeedReaction.ts:21-44` (the cache write and the catch) has no test (F-12). |
| Client: empty / loading / error / offline | Android `FeedRefresherTest.kt:31,44,54`; `PostMoreViewModelTest.kt:635`. Web `feedReturnRefresh.test.ts:25` (offline return) | No test of the Android `FeedViewModel` or of the web feed page's empty and error rendering (F-12). |
| Client: pagination | Android `FeedPagingBoundsTest.kt`, `FeedPagingDeduplicationTest.kt`, `EndlessFeedTest.kt:41-87`. Web `feedPresentation.test.ts:12` | Sound at unit level. |
| Client: unknown post type | Android `FeedContractTest.kt:112` covers unknown **fields** only | No test on either client that a row with an unknown `content_type` is skipped or shown safely (F-12). |

## 3. Counts

Test files and `func Test` functions per package (grep, working tree):

| Service | Package | Files | Tests |
|---|---|---|---|
| feed-service | internal/service | 59 | 310 |
| | internal/store/scylla | 9 | 28 |
| | internal/http | 11 | 27 |
| | internal/ranking | 4 | 21 |
| | internal/events | 8 | 18 |
| | internal/purge | 2 | 9 |
| | internal/pipeline | 1 | 7 |
| | internal/consumers | 1 | 3 |
| | internal/store/postgres | 2 | 3 |
| | cmd/server, cmd/timeline-maintenance, database | 3 | 3 |
| | **total** | **100** | **429** |
| post-service | internal/service | 128 | 793 |
| | internal/http (+ middleware) | 74 | 339 |
| | internal/store/postgres | 67 | 262 |
| | other packages | 30 | 115 |
| | internal/engagement, internal/store/scylla | 0 | 0 |
| | integration-tagged files | 67 | n/a |
| graph-service | http 10/33, permission 8/29, service 8/32, store 8/32, purge 1/5, cmd 1/2, writesource 1/3 | 37 | 136 |
| api-gateway | cmd/server 22/114, pkg/* 15/79 | 37 | 193 |

Android `@Test` counts: `feature/feed` 17 test files (most are reels), `core/feed` about 70, `core/engagement` 16. Web: `bun test src/components/feed` ran 72 tests in 10 files, 0 failures (VERIFIED-RUN).

Tests I ran (all passed): `go test ./feed-service/internal/service -run 'TestHomeWindow_WalkServesEveryEligiblePostExactlyOnce|TestFilterDeletedFromCache_StaleMarkedRowIsAMiss|TestFanoutPost_UnlistedAndPrivateReachOnlyTheAuthor|TestFanoutPost_FriendsReachesOnlyMutualFollows|TestHydratePosts_DropsRepostOfMutedOriginalAuthor|TestAuthorPrivacyFilter_CachesPerViewerAndAuthorForThreeSeconds|TestInvalidateViewerHydrationCache_UsesTheIndex|TestFanoutPost_FollowerCountFlagsCelebBeforeAnyPush' -count=1 -v` (8 PASS); `go test ./cmd/server -run 'TestGatewayStripsSpoofedIdentityHeadersWhenUnauthenticated|TestGatewayIgnoresClientScopesWithLowPrivToken' -count=1 -v` in api-gateway (2 PASS).

## 4. Skipped tests in scope

| Where | Condition | Effect |
|---|---|---|
| `feed-service/internal/service/post_screens_test.go:181` | skips if the post-service fixture is not in the checkout | Runs in a full checkout. |
| `feed-service/internal/store/scylla/timelines_primary_key_test.go:175` | skips if the checked-in CQL schema is unreachable | Runs in a full checkout. |
| `post-service/internal/http/middleware/idempotency_test.go:39` | skips when no Redis answers | The comment-idempotency concurrency tests (`:158`, `:210`) are skipped in any job without Redis. Whether `go-ci.yml` has Redis: UNVERIFIED. |
| graph-service and post-service `*_integration_test.go` | skip when the DSN variable is unset | Expected; they run in `integration-all.yml`. |
| `Architecture/tools/integration/*` | `SkipIfNotIntegration`, `SkipIfDown` | Run only in `e2e.yml`. |

No `@Ignore`, `@Disabled`, `.skip(`, `xit`, `it.todo` found in Android `feature/feed`, `core/feed`, `core/engagement` or web `src/components/feed`, `src/hooks`, `src/app/feed`.

## 5. Mutation checks and contract fixtures

- Mutation: `scripts/harness/mutate.sh` exists. The only spec file in the repository is `Architecture/services/admin-service/testdata/mutations.tsv`. No document under `docs/` records a mutation run against any feed guard (searched `docs/` for `mutate.sh` and "mutation"; hits are rules and other modules). See F-14.
- Contract fixture for the feed row, SHA-256:
  - `Architecture/services/feed-service/internal/service/testdata/contracts/posts/feed_row_photo_carousel.json` = `7ea71569…b06906`
  - `mobile/android/core/feed/src/test/resources/contracts/posts/feed_row_photo_carousel.json` = `7ea71569…b06906` (identical; also asserted by `PostScreensFeedRowFixtureTest.kt:22`)
  - Web: no copy of this file anywhere under `src/`. The web has a different, web-only feed fixture (`src/features/memberships/__tests__/fixtures/feed_row_reel_redacted.json`, `ad5596e3…`), and Android has `contracts/memberships/feed_rows_members_only.json` (`a15db0a7…`) with no Go twin found by name. See F-13.

## 6. Findings

```
ID:            F-1
Title:         The only cross-service feed journey test polls GET /v1/feed, a route feed-service does not register, so no journey has a working end-to-end test.
Severity:      P1
Category:      tests
Evidence level: VERIFIED-READ (route mismatch); SUSPECTED (that the CI job is red or never ran)
Evidence:      Architecture/tools/integration/social_e2e_test.go:55 calls b.Do(ctx, "GET", "/v1/feed", nil) and requires status 200 and the post id in the body. feed-service/internal/http/handler.go:90-125 registers /v1/feed/home, /delta, /reels, ... and no handler for the bare group path. The gateway forwards the /v1/feed prefix unchanged (api-gateway/cmd/server/main.go:585). .github/workflows/e2e.yml runs this test only on pull_request and push to main, and its health wait for the social domain lists ports 8080, 8084, 8083, not feed-service 8086.
How to reproduce: Read the two files; or open the last "E2E / e2e (social)" run on GitHub.
Impact:        Journeys 1, 2 and 6 have no automated proof across post-service, Kafka, feed-service and Scylla. A fan-out regression (the 13 Oct audit's top blocker) would reach production unnoticed.
Fix:           Change line 55 to /v1/feed/home?feed_mode=chronological and parse the envelope's data list for the id; add http://localhost:8086 to the social health list; add a second assertion that the author's own feed has the post.
Acceptance:    e2e (social) is green on a PR and goes red when FanoutPost is stubbed out.
Effort:        S
```

```
ID:            F-2
Title:         The like write path has no server test at any level: the engagement package and the Scylla interaction store have zero test files.
Severity:      P1
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      post-service/internal/engagement/ (lua.go, consumer.go, consumers/scylla_like.go, consumers/pg_counter.go, reconcile.go, hydrate.go) and post-service/internal/store/scylla/ (interactions.go, engagement.go) contain no *_test.go. grep for ToggleLike, likeToggleScript, HydrateEngagement in tests returns only heart_only_test.go, which checks accepted reaction names. Scylla is "optional" in scripts/ci/integration-services.yaml:45, so nothing in CI runs the LWT insert (store/scylla/interactions.go:42-48).
How to reproduce: find post-service/internal/engagement post-service/internal/store/scylla -name '*_test.go' (empty).
Impact:        Journey 6 is unproven. Android likes through POST /reactions (Scylla, IF NOT EXISTS), which is the idempotent route; nothing checks that N concurrent calls add one like and announce one event.
Fix:           (a) miniredis test for engagement.ToggleLike: one user, one call sets and returns count 1; a second call unsets. (b) An integration test, Scylla required, firing 50 concurrent Service.React for one user and post, asserting one reactions_by_post row, count +1 and exactly one PostReacted outbox row. Add Scylla to the post-service CI job or the test will skip.
Acceptance:    Both tests run in CI (not skipped) and fail when IF NOT EXISTS is removed.
Effort:        M
```

```
ID:            F-3
Title:         POST /v1/posts/:id/like is a pure toggle keyed on a Redis key with a 24 h TTL, so concurrent or late taps do not converge on "liked, count +1".
Severity:      P2
Category:      correctness (found while reading for tests)
Evidence level: VERIFIED-READ (code); SUSPECTED (user-visible drift)
Evidence:      post-service/internal/engagement/lua.go:44-68: the script flips on EXISTS liked:{uid}:{pid}; the SET carries EX 86400 (line 63). The route has no idempotency middleware (internal/http/handler.go:144; compare line 146 for comments). 50 concurrent calls from one user end unliked with a net count of 0. After 24 h the key is gone, so a tap meant to unlike runs the "like" branch and HINCRBY +1 again. Callers: web src/hooks/usePostReaction.ts:18, Echo src/features/echo/api/index.ts:68, dev-smoke.mjs:506. The web home feed uses a third route, POST /react (src/hooks/useFeedReaction.ts:38, Postgres toggle); Android uses POST/DELETE /reactions.
How to reproduce: miniredis: ToggleLike, FastForward(25h), ToggleLike; expect IsSet=false and count 0, observe IsSet=true and count 2.
Impact:        Like counts on the Redis counter can drift upward and a double tap from two tabs can cancel itself. Whether the reconcile job repairs the count was not checked.
Fix:           Decide from the durable record (or drop the TTL and seed the key from Scylla on miss); give clients an explicit set/unset. Test as in "How to reproduce".
Acceptance:    The test above passes; 50 concurrent "like" intents leave count +1.
Effort:        M
```

```
ID:            F-4
Title:         feed-service has no integration-tagged test and is absent from the CI integration manifest, so no automated test writes or reads a real Scylla timeline or consumes a real Kafka event.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      grep "go:build integration" under feed-service returns nothing. scripts/ci/integration-services.yaml lists 34 modules; feed-service is not one. integration-all.yml:13-15 says jobs needing Kafka or Scylla are skipped. Store behaviour is pinned by statement-text tests (store/scylla/timelines_primary_key_test.go:74,115) and fakes.
How to reproduce: grep -n "feed-service" scripts/ci/integration-services.yaml (no match).
Impact:        The duplicate-row and wrong-key classes of defect the October audit found live in Scylla semantics that fakes cannot show.
Fix:           One integration test in feed-service/internal/store/scylla: write the same (post, created_at) twice and read one row; page 60 rows by token with no repeat. Add the module to the manifest with needs: [scylla] and start Scylla in that job.
Acceptance:    The job runs (not "blocked") and fails if TimelineTS is replaced by a random timeuuid.
Effort:        M
```

```
ID:            F-5
Title:         Blocking is tested per service but never across them, and no test covers comments by a blocked account.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ (cross-service gap); UNVERIFIED (comment filtering: I found no block filter in post-service/internal/service/post_comments.go or a test by name, but did not read the store query)
Evidence:      Feed read-time filters: blocksafety_test.go:34-140, feedsurface_safety_test.go:28. Graph store: block_symmetry_integration_test.go:72. feed-service consumes no block event (events/consumer.go:130-202 has no UserBlocked case), so removal depends wholly on the read-time call to graph-service; no test wires the two. grep for tests named *Comment*Block* in post-service returns only a hidden-replies test.
How to reproduce: grep -n "case events\." feed-service/internal/events/consumer.go.
Impact:        A change to the graph route's response shape or cache would pass both suites and leak blocked content. Comments are the likeliest place a blocked account still shows.
Fix:           Add TestE2E_Social_BlockHidesBothWays to Architecture/tools/integration: A posts, B follows and sees it, A blocks B, B's /v1/feed/home and /v1/posts/:id/comments no longer carry A within 5 s, and the reverse. Add a post-service unit test that the comment list omits authors in the viewer's block set (or record that it is intended not to).
Acceptance:    The e2e test is green and goes red when applyBlockFilter is bypassed.
Effort:        M
```

```
ID:            F-6
Title:         The PostDeleted consumer handler has no behaviour test, and removal of a plain post's notifications is tested only for Echo notes.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      feed-service/internal/events/post_lifecycle.go:41-80 writes the tombstone, clears counters and removes timeline rows. The only tests naming it are a call-presence check for notes (events/notes_wiring_test.go:70) and tests that set the tombstone key by hand (service/deleted_tombstone_test.go:68). notification-service: the PostDeleted test is note_consumer_test.go:208 (reply and quote notes).
How to reproduce: grep -rn "handlePostDeleted\|DeletedTombstoneKey" feed-service --include=*_test.go.
Impact:        If the handler stopped writing the tombstone, cached rows would serve a deleted post for the cache TTL and no test would fail.
Fix:           In events/, reuse staleConsumer (stale_marker_test.go:33): send a PostDeleted envelope, assert the tombstone key exists with a TTL and that a Redis failure returns an error (so the consumer retries). Add a notification-service case with content_type "post".
Acceptance:    Removing the rdb.Set at post_lifecycle.go:55 fails the test.
Effort:        S
```

```
ID:            F-7
Title:         Home pagination is proven only on a static timeline; no test inserts posts between pages or pages across the celeb pull-merge.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-RUN (the existing test passes); VERIFIED-READ (the gap)
Evidence:      service/home_pagination_test.go:80 builds 300 rows once and walks them (PASS). celebpull_test.go:20 tests mergeTimelineRows (celebpull.go:172) for one page. No test calls the walk with rows added above the cursor, or with pulled and pushed rows interleaved over several pages.
How to reproduce: Read the two tests.
Impact:        Journey 5 as written (concurrent creation) is argued by design (keyset cursor) but not tested; the pull-merge path is where a duplicate or a gap is most likely.
Fix:           Extend the walk: before each page, prepend 3 new rows; assert every original row is served exactly once and no new row is served twice. Add the same walk with half the rows coming from the pulled source through mergeTimelineRows.
Acceptance:    Both walks pass; replacing the cursor with the last ranked item's timestamp fails them.
Effort:        S
```

```
ID:            F-8
Title:         The home feed's fall back to chronological order when the ranker fails has no test.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      service/feed.go:584-588 logs, counts rankFallbacks and keeps the window. No test file injects a failing ranker for home (grep for a failing ranker in feed-service tests finds none; home_pagination_test.go:170 covers a ranker that drops items, not one that errors).
How to reproduce: grep -rn "rankFallbacks" feed-service --include=*_test.go (empty).
Impact:        A refactor that returns the error would turn a ranker outage into an empty or failed home feed.
Fix:           Extract the ranking step into a small function taking the ranker interface; test that an error returns the input window unchanged and increments the counter.
Acceptance:    Returning the error from that branch fails the test.
Effort:        S
```

```
ID:            F-9
Title:         No test shows a poison or exhausted message reaching the dead-letter topic, and no feed handler is run twice in a test.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      Architecture/shared/kafka/consumer.go:210-345 (processWithRetry, sendToDLQUntilDurable). Tests: consumer_dedupe_test.go:106-199 (receipt ordering, redelivery suppressed) and consumer_durability_test.go:12-39 (classification, retryDurable). None asserts a DLQ write followed by a commit. Feed handlers rely on idempotent upserts (timeline_ts_test.go:13) but no test delivers PostCreated or PostDeleted twice.
How to reproduce: grep -n "DLQ" Architecture/shared/kafka/*_test.go (comments only).
Impact:        The October blocker (lost fan-out) was fixed by this consumer; its last-resort path is unproven.
Fix:           With a fake writer and committer: a handler returning Permanent(err) results in one DLQ message carrying the original payload and then one commit; a DLQ write failure results in no commit.
Acceptance:    Swapping the order of DLQ write and commit fails the test.
Effort:        S
```

```
ID:            F-10
Title:         No feed test covers an author who is suspended or deactivated after their posts were fanned out.
Severity:      P2
Category:      tests
Evidence level: UNVERIFIED (I read hidefilter_test.go names and the header of hidefilter.go only; I did not trace which event marks an author hidden for a moderation suspension)
Evidence:      service/hidefilter_test.go:14,36,54,61 cover the hidden-authors filter; hidefilter.go:16-28 names user.deactivated and deletion_scheduled. post-service refuses publishing by a suspended author (publish_standing_test.go:146). Nothing found for "suspended" in feed-service tests.
How to reproduce: grep -rli "suspend" feed-service --include=*_test.go (only purge tests).
Impact:        A suspended account's existing posts may stay in followers' feeds.
Fix:           Confirm the mechanism, then add a hydrate-level test: author enters the hidden set, their cached and fresh rows are both dropped for another viewer.
Acceptance:    Test fails if the hidden-author filter is skipped on the cached path.
Effort:        S
```

```
ID:            F-11
Title:         Web unit tests are not run by any CI workflow, the web tree has no test script, and there is no browser end-to-end test.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ (CI); VERIFIED-RUN (tests pass locally)
Evidence:      package.json scripts are dev, build, start, lint only. The web tree has no .github folder; modernsmapp/.github/workflows/build-web.yml contains no "bun test". No playwright.config.* or *.spec.ts exists. `bun test src/components/feed`: 72 pass, 0 fail, 10 files.
How to reproduce: grep -n "bun test" .github/workflows/*.yml in both trees.
Impact:        The 72 feed tests guard nothing at merge time.
Fix:           Add "test": "bun test" to package.json and a step in build-web.yml (or a web CI workflow) that runs it before the image build.
Acceptance:    A PR that breaks removePost fails CI.
Effort:        S
```

```
ID:            F-12
Title:         Client gaps: the web like hook's rollback, the web feed page's empty and error states, the Android FeedViewModel, and an unknown post type are untested.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      Web: src/hooks/useFeedReaction.ts:21-44 (cache write, catch) has no test; only pure helpers are tested (postHeart.test.tsx:28-44). No test renders src/app/feed/page.tsx. Android: feature/feed/src/main/.../FeedViewModel.kt has no test file among the 17 under feature/feed/src/test (FeedRefresherTest and FeedHydrationTest cover extracted pieces). FeedContractTest.kt:112 covers unknown fields, not an unknown content_type.
How to reproduce: List the test folders named above.
Impact:        A failed like on the web could leave a wrong heart and count; a new server post type could crash or blank a client.
Fix:           Web: a test that mocks api.post to reject and asserts the query cache returns to the prior liked state and count. Both clients: decode a row with content_type "future_type" and assert it is dropped or rendered as a plain card without throwing.
Acceptance:    Removing the rollback branch fails the web test; the unknown-type row test passes on both clients.
Effort:        M
```

```
ID:            F-13
Title:         The feed-row contract fixture is shared byte-for-byte between Go and Android but not with the web, and it covers one row shape, not the home response envelope.
Severity:      P3
Category:      tests
Evidence level: VERIFIED-RUN (sha256sum)
Evidence:      feed_row_photo_carousel.json has SHA-256 7ea71569e8b9dda650ab74bb12ec75f3588bc704af419c7a3f88746a74b06906 in feed-service testdata and in Android test resources; no copy exists under the web src tree. The Go test regenerates and compares it (post_screens_test.go:150-171). No fixture holds a full /v1/feed/home page (data plus meta.next_cursor).
How to reproduce: sha256sum the two files; find the web tree for feed_row*.
Impact:        A field rename breaks Android's test but not the web's.
Fix:           Copy the file into src/components/feed/__tests__/contracts and decode it in a test with a hash check; add a home_page.json envelope fixture to all three.
Acceptance:    Changing a field in the Go golden fails a test in all three trees.
Effort:        S
```

```
ID:            F-14
Title:         No mutation result is recorded for any feed guard.
Severity:      P3
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      The only mutation spec is admin-service/testdata/mutations.tsv. docs/ mentions mutate.sh in rules and other modules' handoffs only.
How to reproduce: grep -rn "mutate.sh" docs.
Impact:        Whether the privacy tests really fail when the guard is removed is known only for the eight I sampled by reading.
Fix:           A feed spec with about ten mutations (applyBlockFilter, listing visibility in FanoutPost, dropTombstoned, privacy fail-closed, hydratedCacheable, homeWindow cursor) run with scripts/verify/go.sh --fast on feed-service; record results.tsv in docs/production-readiness/feed.
Acceptance:    All ten KILLED, results committed.
Effort:        S
```

```
ID:            F-15
Title:         Three like routes write three stores and no test shows a like made through one is counted and shown through the others.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ (routes and callers); SUSPECTED (that counts diverge)
Evidence:      /reactions writes Scylla (post_reactions.go:20-45; Android EngagementApi.kt:58), /like writes Redis then Scylla best-effort with errors only logged (post_engagement.go:158-171; web usePostReaction.ts:18), /react writes Postgres then Scylla best-effort (post_reactions.go:192-213; web home feed useFeedReaction.ts:38) and returns counts from Postgres. Only the fold of reaction names is tested (heart_only_test.go:144).
How to reproduce: Read the three service functions.
Impact:        The count the web shows after a tap (Postgres) and the count the feed hydrates may differ, most visibly when one user is on Android and another on the web.
Fix:           An integration test (Postgres and Scylla): like via ToggleReaction, read counts via the batch hydration path and via GetReactionCounts; they agree. Repeat starting from React.
Acceptance:    Test runs in CI and fails if the Scylla sync line is removed.
Effort:        M
```

```
ID:            F-16
Title:         The post-deploy smoke checks the home feed's shape only and never checks that a created post reaches a feed.
Severity:      P3
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      scripts/smoke/dev-smoke.mjs:476-486 asserts data is a list and items have ids; :490-508 creates a private post and likes it as its author; :785 deletes it without checking it is gone.
How to reproduce: Read the lines.
Impact:        A dev deploy with a dead fan-out consumer passes smoke.
Fix:           After creating the post, poll /v1/feed/home?feed_mode=chronological for its id (author's own row is written for private posts); after cleanup, assert GET /v1/posts/:id is 404.
Acceptance:    Smoke fails when the feed consumer is stopped.
Effort:        S
```

## 7. Checked and sound

- Fan-out audience by visibility: `listingvisibility_test.go:16,37` (unlisted and private reach only the author; public and followers reach followers) against `feed.go:1128`; friends is mutual-only and fails closed, `post_screens_test.go:53,79`. VERIFIED-RUN for `:16` and `:53`.
- Read-time block and mute filters fail closed on every graph failure mode: `blocksafety_test.go:96,119,132`; structure check that every surface resolves the block set, `feedsurface_safety_test.go:28`.
- Reposts of a muted, blocked or private original author are dropped on both the fresh and the cached path: `repost_hydration_test.go:15` (VERIFIED-RUN), `feedmutes_test.go:129,142`, `privacyfilter_test.go:85`, covering `repostfilter.go:24,44`.
- Deleted and stale-marked posts are a cache miss for every viewer: `deleted_tombstone_test.go:22,41,61` covering `deleted_tombstone.go:61,78` (VERIFIED-RUN for `:61`); the visibility-change consumer writes the stale marker and retries when Redis is down, `events/stale_marker_test.go:39,59,74`.
- Per-viewer cache isolation: `hydration_index_test.go:15` (VERIFIED-RUN), `privacyfilter_test.go:159` (VERIFIED-RUN); members-only rows are never cached, `hydration_members_test.go:89,157` covering `hydration.go:908`.
- Static pagination walk with blocked authors, same-millisecond keys and arbitrary ranking serves each post exactly once: `home_pagination_test.go:80` covering `feed.go:318` (VERIFIED-RUN).
- Celeb threshold: flagged before any push, no follower pages read, small authors still pushed: `fanout_chunks_test.go:178,204`, `celebpull_test.go:56,80` (VERIFIED-RUN for `:178`). Fan-out retry resumes at the failed chunk, `fanout_chunks_test.go:118`.
- Unfollow purge cannot delete rows the viewer is still entitled to: `unfollow_purge_test.go:24-116`, `events/unfollow_purge_guard_test.go:24`.
- Shared consumer never writes the dedupe receipt before the effect and suppresses a completed redelivery, with a negative control: `Architecture/shared/kafka/consumer_dedupe_test.go:106,134,168,199`.
- Delete is author-only at service and store level on a real schema: `post-service/internal/service/delete_post_owner_integration_test.go:27-90` covering `my_uploads.go:233`; runs in CI.
- Gateway strips forged identity, scope and internal-key headers from unauthenticated requests and ignores forged scopes on a real token: `identity_strip_test.go:46,113` covering `api-gateway/cmd/server/main.go:950` (VERIFIED-RUN). feed-service refuses to start without the internal key outside dev: `feed-service/cmd/server/internalkey_test.go:7`.
- Block writes are atomic, symmetric and emit exactly one outbox event: graph-service `pair_atomic_integration_test.go:93,228,293`, `block_symmetry_integration_test.go:72`; in CI wave 1 (names and file headers read, bodies not opened).
- Android optimistic like: serialised lane, rollback to the confirmed value, counts derived not accumulated, viewer switch isolation: `EngagementStoreTest.kt:127,174,239,488` covering `EngagementStore.kt:116` (test names read; bodies not opened; Gradle not run per the rules).
- Android paging dedup and end-of-list rules: `FeedPagingDeduplicationTest.kt:111-171`, `FeedPagingBoundsTest.kt:152-222`.
- Web optimistic removal helpers with snapshot and restore: `removePostFromPages.test.ts:23-134` covering `removePostFromPages.ts:37-113` (VERIFIED-RUN, part of the 72).

## 8. Not verified

- Whether `e2e.yml` has ever run green for the social domain (needs the GitHub run history; I did not call GitHub).
- Android tests were read by name only; Gradle was not run.
- post-service, graph-service, search-service and notification-service tests cited "names only" were not opened.
- Whether `go-ci.yml` provides Redis (decides if the idempotency concurrency tests skip there).
- Whether the like reconcile job (`post-service/internal/engagement/reconcile.go`) repairs the drift in F-3.
- Whether the comment list filters blocked authors in the store query (F-5) and how a moderation suspension reaches the feed's hidden-author set (F-10).
- The forged `X-User-Id` with a valid token for another user on a feed or post route specifically (`edge_internal_test.go:823` covers the permissions route; body not opened).
- No full Go suite, no `-tags integration`, no build, no device: by the rules of this audit.
