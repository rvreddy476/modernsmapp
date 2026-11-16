# Track B — security (home feed)

- **Date:** 2026-11-16. Read-only. Backend and Android: `C:\workspace\modernsmapp` (working tree as found). Web: `C:\workspace\wt\pb-echo`.
- **Method:** code read end to end for each claim. No service, database or device was started and no deployed environment was called. No Go test was run in this track; the only command run for evidence is the `node` URL check quoted in B-8. So almost everything is VERIFIED-READ, not VERIFIED-RUN.
- Backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Where a line number is marked `≈` the function name is the reliable anchor.

## Verdict for this track

**Not ready.** No P0 was found: I found no path by which one user reads or changes another user's post, comment, bookmark, mute list or preference, and no path to bypass authentication from the internet. One P1 (share and repost skip the "may this caller see the post" check, which also lets a blocked user repost the blocker) and several P2s must be fixed. The list of things I could not verify is at the end and is not small.

---

## 1. Route table

### How every route learns the caller

1. The gateway deletes every client copy of `X-User-Id`, `X-Verified-User-Id`, `X-Scopes`, `X-Admin-Role`, `X-Device-Id`, `X-Internal-Service-Key`, `X-Graph-Write-Source` and the admin headers on every request (`api-gateway/cmd/server/main.go:844-885`, `895-905`, called at `957`).
2. It verifies the token (`Authorization: Bearer`, else the `access_token` cookie, else a query token only on `GATEWAY_QUERY_TOKEN_PATHS`; `main.go:996-1019`) and sets `X-User-Id` from the verified subject (`main.go:1065-1067`). A bad token is 401 (`main.go:1035-1040`).
3. **The gateway does not require a token.** A request with no token is proxied with no `X-User-Id` (`main.go:1020-1023`). Authentication is therefore enforced per handler: each one parses `X-User-Id` and answers 401 when it is absent.
4. The gateway stamps the shared `X-Internal-Service-Key` on every request to feed-service, post-service and graph-service, anonymous ones included (`pkg/internalroutes/internalroutes.go:377-400`, `521-531`). The three services refuse any request without it (`shared/middleware/internal_auth.go:27-41`). The key proves "came through the gateway or from inside the cluster", nothing more.
5. Any path with an `internal` segment is refused with 404 at the edge for every caller (`internalroutes.go:72-212`), and `/v1/internal` has no route prefix at all (`main.go:468-756`).

**Gateway rate limit on every route below:** 100 requests/second per client IP (burst 200) and 60 requests/second per signed-in user (burst 120), in memory and in Redis (`main.go:256-269`, `1208-1236`). "Gateway only" in the table means nothing else limits the route.

### feed-service (`feed-service/internal/http/handler.go`)

All routes: internal key required (`handler.go:79-81`); caller is `X-User-Id`, 401 without it; service limit **120 requests/minute per user**, Redis, fails open on a Redis error (`feedRateLimit`, `handler.go` ≈842-884).

| Method | Path | Handler | Auth | Page size cap |
|---|---|---|---|---|
| GET | `/v1/feed/home` | `GetHomeFeed` `handler.go:127` | required | limit ≤ 100 (`handler.go:138-144`) |
| GET | `/v1/feed/delta` | `FeedDelta` `handler_delta.go:36` | required | n/a |
| POST, GET | `/v1/feed/preference` | `SetPreference`, `GetPreference` | required | n/a |
| POST | `/v1/feed/signal` | `PostSignal` | required | n/a |
| POST | `/v1/feed/feedback` | `PostFeedback` | required | n/a |
| GET | `/v1/feed/feedback/authors` | `GetMutedAuthors` | required | none (whole list) |
| POST, DELETE | `/v1/feed/hide/:postId` | `HidePost`, `UnhidePost` | required | n/a |
| POST, DELETE | `/v1/feed/mute` | `MuteTarget`, `UnmuteTarget` | required | n/a |
| GET | `/v1/feed/muted` | `GetMutedTargets` | required | none (whole list) |
| GET | `/v1/feed/internal/debug` | `DebugFeed` | not reachable from the edge (rule 5); in-cluster, key only | n/a |
| GET | `/v1/feed/debug` | `legacyDebugGone` | 410 for everyone | n/a |
| GET | `/metrics`, health | `cmd/server/main.go:262-263` | no key (registered before the key gate); no gateway route reaches them | n/a |

### post-service (`post-service/internal/http/handler.go:65-448`, `reposts_handler.go`)

All routes: internal key required (`handler.go:76-85`). "optional" means the handler answers signed-out callers and applies the read gate as an anonymous viewer.

| Method | Path | Handler | Auth | Authorisation check | Service rate limit |
|---|---|---|---|---|---|
| POST | `/v1/posts` | `CreatePost` `handler.go:90` | required (`create_post_handler.go:327`) | author is `X-User-Id`; body has no author, status or counter fields (`create_post_handler.go:25-165`) | 20/hour and 50/day per user, fails open (`service/ratelimit.go:25-88`); body ≤ 256 KiB (`create_post_handler.go:212`) |
| POST | `/v1/posts/batch` | `BatchGetPosts` `post_reads_handler.go:308` | optional | viewer is the header, never the body (`:333-337`); per-post visibility (`service/post_reads.go:332-420`, `batch_visibility.go`) | gateway only; ≤ 100 ids |
| GET | `/v1/posts/:postId` | `GetPost` `post_reads_handler.go:44` | optional | `viewerMayViewPost` (`service/post_visibility.go:78-127`) | gateway only |
| DELETE | `/v1/posts/:postId` | `DeletePost` `delete_post.go` | required | author only (`service/my_uploads.go:233-244`) | gateway only |
| PATCH | `/v1/posts/:postId` | `UpdatePost` `post_edit.go` | required | author only (`service/post_edit.go:163-178`) | gateway only; body ≤ 256 KiB |
| POST | `/v1/posts/:postId/like` | `ToggleLike` | required | `loadPostForEngagement` (`service/post_engagement.go:139`) | 120/hour (`:148`) |
| POST | `/v1/posts/:postId/react` | `ToggleReaction` | required | read gate + `loadPostForEngagement` (`service/post_reactions.go:175`) | 120/hour (`:183`) |
| POST | `/v1/posts/:postId/reactions` | `React` | required | read gate + `loadPostForEngagement` (`service/post_reactions.go:28`) | **none** (B-4) |
| DELETE | `/v1/posts/:postId/reactions` | `Unreact` | required | own row only | **none** (B-4) |
| GET | `/v1/posts/:postId/reactions/me` | `GetMyReaction` | required | read gate | gateway only |
| GET | `/v1/posts/:postId/reactions/counts` | `GetReactionCounts` | optional | read gate | gateway only |
| POST | `/v1/posts/:postId/share` | `SharePost` | required | **none on the caller** (B-1) | 30/hour (`service/post_engagement.go:278`) |
| POST | `/v1/posts/:postId/repost` | `CreateRepost` `reposts_handler.go` | required | **none on the caller** (B-1) | 30/hour (`service/post_reposts.go:51`) |
| DELETE | `/v1/posts/:postId/repost` | `UndoRepost` | required | own repost only | gateway only |
| GET | `/v1/posts/:postId/repost/me` | `GetRepostState` | required | own state only; no read gate | gateway only |
| GET | `/v1/posts/:postId/reposters` | `ListReposters` | optional | read gate | limit ≤ 50 |
| POST | `/v1/posts/:postId/comments` | `AddComment` `comments_handler.go` | required | `loadPostForEngagement`, members gate, comment audience (`service/post_comments.go:147-166`) | 10/minute (`:168`); body ≤ 8 KiB (`middleware/idempotency.go:37`) |
| GET | `/v1/posts/:postId/comments` | `ListComments` | optional | `singlePostRead` (`service/comment_creator.go:47`) | limit ≤ 50 (`store/postgres/comments.go:379`) |
| GET | `/v1/posts/:postId/comments/around/:commentId` | `ListCommentsAround` | optional | `singlePostRead` + members gate (`service/post_comments.go:385-390`) | limit ≤ 50 |
| POST | `/v1/comments/:commentId/reply` | `CreateReply` | required | same gates as a comment (`service/post_comments.go:261-290`) | 20/hour (`:286`) |
| GET | `/v1/comments/:commentId/replies` | `ListReplies` | optional | `gateCommentPost` (`service/series_visibility.go:128-141`) | limit ≤ 100 |
| POST | `/v1/comments/:commentId/like`, `/dislike` | toggles | required | `gateCommentPost` | 120/hour (`service/post_comments.go:35`, `76`) |
| DELETE | `/v1/comments/:commentId` | `DeleteComment` | required | comment author only (`store/postgres/comments.go:635-646`) | gateway only |
| PATCH | `/v1/comments/:commentId` | `EditComment` | required | comment author only, 15 minutes (`store/postgres/comments.go:664-691`) | **none, and no length cap** (B-3) |
| POST, DELETE | `/v1/posts/:postId/bookmark` | `AddBookmark`, `RemoveBookmark` | required | add: `checkEngagementVisibility` (`service/post_engagement.go:24`, `saved_collections.go:288`); remove: own row | gateway only |
| GET | `/v1/posts/bookmarks` | `GetBookmarks` | required | own list; each row re-gated (`service/post_engagement.go:58-90`) | limit ≤ 100 |
| POST | `/v1/saved` | `SaveItem` `bookmarks_handler.go:172` | required | read gate (`:192`); collection must be the caller's (`store/postgres/saved_collections.go:255-261`) | gateway only |
| GET | `/v1/saved` | `ListSavedItems` | required | `WHERE user_id = caller` | store cap (not read: UNVERIFIED) |
| DELETE | `/v1/saved/:savedId` | `UnsaveItem` | required | `WHERE id = $1 AND user_id = $2` (`store/postgres/saved.go:43-45`) | gateway only |
| GET, POST, PATCH, DELETE | `/v1/saved/collections[/:collectionId]` | saved collections | required | every query carries `user_id = caller` (`saved_collections.go:114-120`, `184-195`, `219-230`) | gateway only |
| any | `/v1/posts/internal/*` | service to service | refused at the edge; in-cluster: key plus a signed service token on writes (`handler.go:198-207`) | | |
| any | `/v1/internal/*` | service to service | no gateway route; in-cluster: key only | body or query names the viewer (`handler_post_visibility.go:23`, `media_access_handler.go:54`) | |

### graph-service (`graph-service/internal/http/handler.go:99-202`)

All routes: internal key required (`handler.go:101-103`). Writes also need the gateway's write-source label (`canonical_guard.go`), strict in production (`deploy/services/graph-service/values-prod.yaml`, `GRAPH_WRITE_SOURCE_STRICT: "true"`).

| Method | Path | Handler | Auth | Authorisation check | Service rate limit |
|---|---|---|---|---|---|
| POST | `/v1/graph/follow` | `Follow` `handler.go:392` | required | actor is the header, target the body (`:344-361`); block refuses (`service/graph.go` ≈189-197) | 200/day per user, fails open (`ratelimit/ratelimit.go:33-45`) |
| POST | `/v1/graph/unfollow` | `Unfollow` `:585` | required | own edge | gateway only |
| POST, DELETE | `/v1/graph/block` | `Block`, `Unblock` | required | own edge | 200/day (block and unblock together) |
| POST, DELETE | `/v1/graph/mute` | `Mute`, `Unmute` | required | own edge | 200/day |
| GET | `/v1/graph/blocked-and-muted` | `GetBlockedAndMuted` `:1075` | required | `user_id` may only repeat the caller, else 400 `VIEWER_BOUND` (`:1080-1084`) | gateway only |
| GET | `/v1/graph/relationship`, `/mutuals`; POST `/relationships/batch`, `/connections/mutual-counts` | | required at the edge | `boundActor`: an explicit id may only repeat the caller (`:318-337`) | batch ≤ 100 |
| GET | `/v1/graph/followers/:userId`, `/following/:userId`, `/:userId/following-ids`, `/connections/:userId` | | optional | `callerAllowedToReadConnections` (`connections_rule.go`): owner always; blocked caller or stranger to a private account gets an empty list; signed-out reads public accounts only | limit ≤ 100 (`store/graph.go:324`), offset ≤ 10000 |
| GET | `/v1/graph/counts/:userId` | `GetCounts` `:638` | optional | none (see public routes) | gateway only |
| any | `/v1/internal/graph/*` | internal | no gateway route; in-cluster: key only | viewer named in the body or query | |

### Public routes (answer with no token) and why

| Route | Justification |
|---|---|
| `GET /v1/posts/:postId`, `POST /v1/posts/batch`, `GET /v1/posts/recent`, `GET /v1/posts/by-author/:id[/counts]`, `GET /v1/posts/categories` | Signed-out web reading of public posts (founder decision 29 Sep: public videos play signed out). An anonymous viewer is treated as a stranger: private accounts, followers, friends and private posts are refused (`service/post_visibility.go:78-127`, `privacy_gate.go:203-218`). |
| `GET /v1/posts/:postId/comments`, `/comments/around/:id`, `GET /v1/comments/:id/replies`, `GET /v1/posts/:postId/reactions/counts`, `/reposters`, `/poll`, `/poll/results` | Same read gate as the post itself. |
| `GET /v1/graph/followers|following|connections/:userId`, `/:userId/following-ids` | Public accounts only when signed out. |
| `GET /v1/graph/counts/:userId` | Follower and following counts of any account, private or blocking the caller included. Counts only. Acceptable if the product treats counts as public; say so explicitly (P3 note, not a finding). |
| `GET /v1/links/:id` | Action button redirect; out of this track. |
| Home feed routes | **None are public.** Every `/v1/feed/*` handler answers 401 without `X-User-Id`. |

---

## 2. Findings

```
ID:            B-1
Title:         Share and repost never check that the caller may see the post, so a blocked user or a stranger to a private account can share or repost it by id.
Severity:      P1
Category:      authz
Evidence level: VERIFIED-READ
Evidence:      SharePost (post-service/internal/service/post_engagement.go:277-296) and CreateRepost
               (service/post_reposts.go:43-71) load the target with loadRepostTarget, which is a bare
               pgStore.GetPost with no viewer (service/heart_only.go:144-149). Neither calls
               viewerMayViewPost, loadPostForEngagement or canViewAuthor, and neither handler calls
               requirePostReadable (http/reactions_handler.go SharePost; http/reposts_handler.go CreateRepost),
               unlike like, react, comment, bookmark and save, which all do.
               The only audience rule left is the post's own column: repostableVisibility refuses only
               `private` and `friends` (service/post_screen_fields.go:62-68), so a `followers` post is
               repostable by a non-follower although the comment at post_reposts.go step 2 says it is not;
               shareRestricted lets share_type=external through for `followers` and `friends`
               (post_screen_fields.go:223-231). Blocks and private accounts are not consulted at all.
How to reproduce: As user A, blocked by B (or not following private account B), with a post id of B's:
               POST /v1/posts/<id>/repost {"type":"plain"} -> expected 404, code path returns 201.
               POST /v1/posts/<id>/share {"share_type":"external"} -> 200 with the share count.
Impact:        A blocked user can repost the blocker's posts to their own followers and bump the counters;
               the author is told about it through the PostShared / repost events (notification delivery
               itself not traced: SUSPECTED). Existence and share count of a post the caller cannot open
               are confirmed. Followers-only posts gain repost rows from non-followers. The post body does
               not leak: feed hydration re-checks every viewer (feed-service/internal/service/privacyfilter.go,
               post-service batch_visibility.go).
Fix:           In both service functions replace loadRepostTarget with loadPostForEngagement(ctx, postID, userID)
               and map ErrPostNotVisible to the existing POST_NOT_FOUND. Make repostableVisibility refuse
               `followers` too, or correct the comment if followers reposting is intended.
Acceptance:    Service tests using engagementPostSeam: CreateRepost and SharePost return POST_NOT_FOUND when the
               seam returns ErrPostNotVisible; a route test that POST /repost on an invisible post is 404.
Effort:        S
```

```
ID:            B-2
Title:         A post's rich_text style values are stored unchecked and the web puts them straight into CSS, so any author can make every viewer's browser load a URL of the author's choosing.
Severity:      P2
Category:      privacy
Evidence level: VERIFIED-READ
Evidence:      Server: rich_text is a json.RawMessage passed to the store with no validation
               (post-service/internal/http/create_post_handler.go:47, 499; service/post_create.go:260); the web
               tree states the same (pb-echo src/components/CreatePortal.tsx:445 "stores rich_text as arbitrary JSON").
               Web: src/components/feed/WordsOnColour.tsx, function WordsOnColour: `background: bg || STAGE_GROUND`
               and `backgroundImage: url(/v1/media/${bgImage}/serve)` with bg = rich.background and
               bgImage = rich.background_media_id, plus `color: textColor`; none is checked to be a hex colour
               or a UUID. No Content-Security-Policy is set (next.config.ts has none; src/middleware.ts sets only
               Cache-Control and Referrer-Policy).
How to reproduce: POST /v1/posts with rich_text {"background":"url(https://attacker.example/p.gif)"} and no media;
               open the web feed as another user; the browser requests attacker.example. Not run.
Impact:        The author collects the IP address, time and user agent of everyone whose feed shows the post
               (a tracking pixel), and can draw arbitrary remote imagery behind the card. No script execution.
               Android rendering of the same field was not read (UNVERIFIED).
Fix:           Server: on create and edit accept rich_text.background / text_color only when they match
               ^#[0-9a-fA-F]{3,8}$ and background_media_id only as a UUID; reject otherwise. Web: apply the same
               two checks in WordsOnColour before building the style object. Add a CSP with img-src limited to
               self and the media origin.
Acceptance:    Go test: CreatePost with background "url(https://x)" is 400. Web unit test: WordsOnColour given
               that value renders the fallback ground.
Effort:        S
```

```
ID:            B-3
Title:         Editing a comment has no length limit and no rate limit, and a share's quote_text has no length limit; the only bound is the gateway's 10 MiB body cap.
Severity:      P2
Category:      input
Evidence level: VERIFIED-READ
Evidence:      PATCH /v1/comments/:commentId: EditCommentRequest is `body` required and nothing else
               (http/comments_handler.go EditComment); service EditComment (service/post_comments.go:364-372)
               and store EditComment (store/postgres/comments.go:664-691) write body as given, with no size
               check, no blank check and no limiter. Creating a comment is capped at 8 KiB only because it passes
               the idempotency middleware (middleware/idempotency.go:37, handler.go:146); the edit route does
               not. Gateway cap: MaxBodyBytes = 10 << 20 (api-gateway/pkg/edgelimits/edgelimits.go:48).
               ShareRequest.QuoteText has no binding limit (http/reactions_handler.go) and is copied into the
               Kafka event (service/post_engagement.go event.QuoteText).
               Whether a comment is re-moderated after an edit was not traced (SUSPECTED gap: the edit is a bare UPDATE).
How to reproduce: Post a one-word comment, then within 15 minutes PATCH it with a 9 MB body. Every later
               GET /v1/posts/:id/comments page returns it.
Impact:        Storage and bandwidth abuse; a comments page that carries megabytes breaks clients. A comment can
               be swapped for different text after it was accepted.
Fix:           In the handler: trim, reject blank, cap at the same rune limit the product shows for comments, and
               wrap the body in http.MaxBytesReader(8 KiB). Add rl:comment_edit per user. Cap quote_text at 500
               runes as CreateRepost already does.
Acceptance:    Route test: PATCH with a 9 KiB body is 413 or 400; with blank text is 400.
Effort:        S
```

```
ID:            B-4
Title:         The legacy reaction routes (POST and DELETE /v1/posts/:postId/reactions) have no per-user rate limit, unlike /like and /react.
Severity:      P2
Category:      input
Evidence level: VERIFIED-READ
Evidence:      Service React (service/post_reactions.go:20-60) and Unreact call no rateLimiter; the only limiter
               in that file is in ToggleReaction (:183). Each new reaction enqueues a PostReacted outbox event
               (post_reactions.go ≈50-60). The comment in the code says Android likes through this route.
               Only the gateway's 60 requests/second per user applies.
How to reproduce: Loop POST then DELETE /v1/posts/<id>/reactions as one user.
Impact:        Up to about 30 like/unlike cycles a second per account against one author, each a counter write and
               a notification trigger (whether notification-service de-duplicates was not traced: SUSPECTED).
Fix:           Add the same `rl:react:<user>` check (LikeLimitPerHour) at the top of React and Unreact.
Acceptance:    Service test: the 121st React in an hour returns RATE_LIMITED.
Effort:        S
```

```
ID:            B-5
Title:         Feed account erasure reports "purged" while leaving the user's home timeline, older author-timeline buckets and Redis rows in place.
Severity:      P2
Category:      privacy
Evidence level: VERIFIED-READ
Evidence:      StoreEraser.PurgeUser (feed-service/internal/purge/eraser.go:43-50) calls
               DeleteTimelineEntriesByAuthor, which deletes only the current and previous two monthly buckets of
               author_timeline_by_author (store/scylla/timelines.go:839-857), then the PostgreSQL rows
               (store/postgres/purge.go:72-93). Nothing deletes the erased user's own home_timeline_by_user
               partitions (keyed by their user id: what they were shown and from whom), their notes_timeline,
               the timeline_index rows, or their Redis keys (feed:hydrate:<viewer>:*, feed_delta:*). Timelines
               live 360 days (Architecture/docker/scylla/schema.prod.cql: default_time_to_live = 31104000).
               The handler then publishes the purge acknowledgement (purge/purge.go:151-160).
How to reproduce: Read the three functions; there is no other delete on those tables in the purge path.
Impact:        Personal data keyed by the deleted user's id stays up to 360 days after the platform records the
               erasure as complete. Other users do not see it (hydration drops deleted and hidden authors).
Fix:           In PurgeUser also delete the user's home_timeline_by_user and notes_timeline partitions for every
               bucket inside the TTL window, loop the author-timeline delete over the same window (12 buckets,
               not 3), and delete the user's Redis keys through the per-viewer index. Ack only after all succeed.
Acceptance:    Eraser test with a fake timeline store: PurgeUser calls the home-timeline delete for the user and
               covers 12 buckets; the ack is not published when any delete fails.
Effort:        M
```

```
ID:            B-6
Title:         The web /v1 proxy forwards no client address, so the gateway's per-IP limit sees every web visitor as one client.
Severity:      P2
Category:      reliability
Evidence level: SUSPECTED
Evidence:      pb-echo src/app/api/proxy/[...path]/route.ts:33-62: FORWARDED_HEADERS has no x-forwarded-for or
               x-real-ip, and the request is a server-side fetch (:166-183). The gateway keys its IP bucket on the
               resolved client address (api-gateway/cmd/server/main.go:1217-1224), 100 requests/second.
               What would confirm it: how the web server reaches the gateway in production (API_GATEWAY_URL: the
               public ALB or the cluster service) and the clientip resolver's behaviour for that peer
               (pkg/clientip/clientip.go, not read in full). There is no web deployment values file in this repo.
How to reproduce: From one machine send more than 100 requests/second of anonymous GET /v1/posts/recent to the
               web origin; signed-out web users then receive 429.
Impact:        One anonymous sender can exhaust the shared bucket and lock every web user out of the API, or,
               seen the other way, the per-IP limit does nothing against abuse sent through the web origin.
               Signed-in abuse is still held by the per-user limits.
Fix:           Have the proxy set X-Forwarded-For to the browser address it received, and add the web pods' range
               to GATEWAY_TRUSTED_PROXY_CIDRS; or rate-limit per IP at the web origin's load balancer.
Acceptance:    Proxy unit test: the upstream request carries X-Forwarded-For equal to the caller's address.
Effort:        S
```

```
ID:            B-7
Title:         post-service and graph-service return the raw Go error text to the client on a 500.
Severity:      P2
Category:      observability
Evidence level: VERIFIED-READ
Evidence:      `"INTERNAL_ERROR", err.Error()` appears 98 times in post-service/internal/http and 33 times in
               graph-service/internal/http (command: grep -rn '"INTERNAL_ERROR", err.Error()' --include=*.go
               <svc>/internal/http | grep -v _test | wc -l), for example post_reads_handler.go:64, 118, 167 and
               graph handler.go:240, 409. shared/api ErrorWithContext writes the message unchanged
               (Architecture/shared/api: ErrorWithContext -> Error). The wrapped errors include upstream calls
               ("graph-service status 502", "load viewer bookmark: ...") and database driver errors.
               feed-service already fixed this (internalError, handler.go ≈238-242; count 0).
How to reproduce: Cause any store error, for example with the database unavailable; the response body carries the
               driver text.
Impact:        Internal host names, table and constraint names and query fragments reach any caller. Useful to an
               attacker, embarrassing in a client UI.
Fix:           One helper per service, as feed-service has: log the error with the request id, answer a fixed
               sentence. Replace the 131 call sites mechanically.
Acceptance:    A handler test with a store that returns errors.New("pq: relation \"x\"") asserts the body does
               not contain "pq:".
Effort:        M
```

```
ID:            B-8
Title:         The gateway serves /metrics on its public listener to any request without a forwarding header, and the web proxy can be steered outside /v1.
Severity:      P3
Category:      deployment
Evidence level: SUSPECTED
Evidence:      METRICS_ADDR is not set in deploy/services/api-gateway/values-prod.yaml, so the public listener
               answers /metrics to requests with no X-Forwarded-* header (pkg/metricsgate/metricsgate.go Handler).
               The web proxy builds `${API_GATEWAY}/v1/${path.join("/")}` and URL() resolves dot segments
               (route.ts:66-67). Command run:
               node -e 'for (const p of [["..","metrics"],["%2e%2e","metrics"]]) console.log(new URL("http://gw:8080/v1/"+p.join("/")).pathname)'
               output: /metrics, /metrics.
               What would confirm it: whether Next passes a `..` or `%2e%2e` segment into the catch-all params
               (not tested), and whether the web server reaches the gateway without a load balancer in between.
Impact:        If both hold, anyone reads gateway runtime counters, including the session-revocation failure
               counter. No user data.
Fix:           Set METRICS_ADDR in the gateway values and scrape that port; in the proxy reject any segment equal
               to "." or "..".
Acceptance:    Proxy test: path ["..","metrics"] answers 404 and makes no upstream request.
Effort:        S
```

```
ID:            B-9
Title:         One shared internal key is the only thing between any pod in the cluster and acting as any user on feed, post and graph; no NetworkPolicy narrows who may connect.
Severity:      P2
Category:      authn
Evidence level: VERIFIED-READ
Evidence:      The three services accept identity from X-User-Id once the key matches
               (feed handler.go:79-81; post handler.go:76-85; graph handler.go:101-103). The same key is given to
               every service's ExternalSecret (deploy/services/*/values-prod.yaml, INTERNAL_SERVICE_KEY).
               networkPolicy.enabled defaults to false (charts/atpost-service/values.yaml:332-336) and none of
               feed-service, post-service or graph-service values-prod.yaml turns it on. /v1/internal/* on post
               and graph take the viewer from the body with the key alone (post handler.go:225-234; graph
               handler.go:184-192). Ingress is disabled for all three, so this is not reachable from the internet.
               Outside production the key is optional: post-service exits only when ENV=prod or
               APP_ENV=production (cmd/server/main.go:58-63); graph-service the same (cmd/server/main.go:294-305);
               feed-service is stricter (prod, staging, qa: cmd/server/main.go:317-329).
How to reproduce: From any pod holding the key: POST post-service /v1/posts with X-User-Id of any user.
Impact:        A compromise of any one of about thirty services, or a leak of one secret, is full impersonation of
               every user on these three services. Known debt (docs/harness/design-gateway-identity-2026-11-08.md);
               recorded here because it is still true in the code.
Fix:           Smallest step now: enable networkPolicy for the three services with ingressFrom limited to the
               gateway and their real callers, and confirm the CNI enforces it. Make post and graph refuse to
               start without the key in staging and qa, as feed does. The signed gateway identity is the real fix.
Acceptance:    render_test.sh asserts a NetworkPolicy object for the three services in prod values; a unit test
               for each service's key check covers ENV=staging.
Effort:        M
```

```
ID:            B-10
Title:         A wildcard CORS origin is only warned about, and the gateway then reflects any origin with credentials allowed.
Severity:      P3
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      api-gateway/cmd/server/main.go:59-64 logs a warning for "*"; isAllowedOrigin returns true for "*"
               (:1099-1107) and corsMiddleware then echoes the caller's Origin with
               Access-Control-Allow-Credentials: true (:1283-1285). Production values list three fixed origins
               (deploy/services/api-gateway/values-prod.yaml CORS_ORIGINS), so this is a guard against a future
               mistake, not a present hole.
How to reproduce: Start the gateway with CORS_ORIGINS=* and APP_ENV=production: it boots.
Impact:        One wrong value would let any website make credentialed calls with a visitor's cookie.
Fix:           Refuse to start when tokenPolicy.Production and an origin is "*".
Acceptance:    Unit test on the startup check.
Effort:        S
```

---

## 3. Checked and sound

Each line is something I read and found correct. All VERIFIED-READ; none was run.

**Authentication**
- Token verification accepts only HS256 and RS256, and the algorithm is re-checked against policy: `api-gateway/pkg/tokenpolicy/verify.go:135-156`, `policy.go:199-208`. Production accepts RS256 only (`policy.go:116`, `202-205`).
- `exp` mandatory, `nbf` honoured, 60 s skew: `policy.go:210-220`. Refresh tokens refused as bearer: `policy.go:224-233`. Issuer and audience enforced when configured and mandatory in production: `policy.go:118-126`, `236-254`. Subject must be a UUID: `policy.go:258-264`. `sid` mandatory in production: `policy.go:267-269`.
- Production values turn those rules on: `deploy/services/api-gateway/values-prod.yaml` sets `APP_ENV: "production"`, `JWT_ISSUER`, `JWT_AUDIENCE`, `JWT_RS256_KID`, and query tokens only on `/v1/ws`.
- Identity headers stripped case-insensitively then re-derived: `api-gateway/cmd/server/main.go:895-905`, `957`, `1065-1079`. The web proxy forwarding a browser's `x-user-id` (`route.ts:35`) is therefore harmless.
- Revoked sessions refused at the gateway: `pkg/sessionrevocation/sessionrevocation.go:173-216`; production refuses to boot without Redis for it (`main.go:234-236`). auth-service writes the mark on logout, logout-all, revoke-one, password reset and the session-security path: `identity-platform/services/auth-service/internal/service/sessions.go:300`, `318`, `330`, `355`, `367-372`; `password_reset.go:150`; `session_security.go:53`. Access tokens default to 15 minutes (`internal/config/config.go:212`; unset in prod values).
- Internal routes unreachable from the edge: `pkg/internalroutes/internalroutes.go:72-212` (decoded, raw, re-decoded, backslash and `;` forms), and no `/v1/internal` prefix in `main.go:468-756`.
- `RequireInternalKey` compares in constant time and panics on an empty secret: `Architecture/shared/middleware/internal_auth.go:27-41`. An empty key in production stops feed (`feed-service/cmd/server/main.go:317-329`), post (`post-service/cmd/server/main.go:58-63`) and graph (`graph-service/cmd/server/main.go:294-299`).
- No handler in the three services decides anything from a role or scope header on the in-scope routes: the only `X-Scopes` read is `post-service/internal/http/moderator_gate.go:25` (moderation, not feed), and the gateway re-derives that header.

**Authorisation**
- No in-scope handler takes the acting user from the body or query on an edge route. grep for `viewer_id|user_id|author_id|actor_id|owner_id` in `post-service/internal/http` finds only the `/v1/internal/*` handlers. `POST /v1/posts/batch` ignores a body `viewer_id` (`post_reads_handler.go:304-337`).
- Single post read: review status, processing, account privacy and hidden authors, then per-post audience with unknown values denied and graph failures denied: `post-service/internal/service/post_reads.go:21-63`, `post_visibility.go:78-127`, `privacy_gate.go:203-218`, `225-251`.
- Batch read applies the same rules; `private` is never listed; a block in either direction hides restricted posts: `batch_visibility.go:22-36`, `83-137`.
- Like, react, comment, reply, bookmark, save, poll vote, reaction counts, reposters all pass the read gate (references in the route table).
- Delete and edit post are author-only (`my_uploads.go:233-244`, `post_edit.go:163-178`); delete and edit comment are comment-author-only (`store/postgres/comments.go:635-646`, `664-683`).
- Bookmarks, saved items and collections are always scoped by the caller's id in SQL (`store/postgres/saved.go:34-45`, `saved_collections.go:114-120`, `255-261`). The bookmark list re-checks each post against the viewer (`service/post_engagement.go:58-90`).
- Feed preferences, hides, mutes and muted lists are read and written only for `X-User-Id` (`feed-service/internal/http/handler.go`, handlers `SetPreference` to `GetMutedTargets`); there is no id parameter to tamper with.
- Home feed block and mute filter fails closed and covers the cold-start fill: `feed-service/internal/service/feed.go:446-453`, `525-528`, `1369-1405`; the graph answer is checked for status and decode errors (`feed.go:1465-1478`) and includes blocks in both directions (`graph-service/internal/http/handler.go:1066-1086`).
- Feed hydration tail re-checks cached rows for deletion, processing, unlisted/private, reposts of blocked authors, private accounts and feedback, each failing closed: `feed-service/internal/service/hydration.go:407`, `424-463`; `privacyfilter.go:65-96`; `repostfilter.go`; `listingvisibility.go`.
- Feed caches are per viewer: `hydration.go:28-30` (`feed:hydrate:<viewer>:<post>`), `handler_delta.go:67` (`feed_delta:<type>:<viewer>:<anchor>`). Signed media URLs are not cached (`hydration.go:605-608`).
- A failed hydration is an error, never raw timeline rows: `handler.go` `GetHomeFeed` (≈179-184).
- Graph reads cannot name another viewer from the edge: `handler.go:318-337`, `1075-1086`; follower lists honour blocks and private accounts (`connections_rule.go`).
- Mass assignment: the create body has no `author_id`, `user_id`, status, verification or counter field (`create_post_handler.go:25-165`); the author is the header (`:327`).

**Input, output, abuse**
- SQL: every `fmt.Sprintf` and string concatenation into a query in the three services' stores inserts placeholder numbers or compile-time constants; the two that take a name check it against a fixed list first (`graph-service/internal/store/graph.go:249-275`; `post-service/internal/store/postgres/author_sort.go:133-201` from a parsed enum). Command: `grep -rn 'fmt.Sprintf(' ... | grep -i 'select|insert|update|delete|where|order by'` and a concatenation grep; all hits read. Scylla stores have no `Sprintf` in queries.
- No server-side fetch of a user-supplied URL in the post-create path: grep for link preview, unfurl, og:, oembed and `http.Get` in post, feed and graph services finds nothing. `link_url` must be https with a host and no user info (`service/post_screen_fields.go:70-76`).
- Web post text is rendered as React text with hashtags and mentions as internal links (`pb-echo src/components/feed/WordsOnColour.tsx:25-44`); the rich-text renderer drops any link that is not http, https or a site path (`src/components/studio/RichTextRenderer.tsx:44-55`). No `dangerouslySetInnerHTML` in `src/components/feed` or the feed card; the two in `src/app/layout.tsx:83`, `103` are static.
- Web credentials: access token in memory only (`src/lib/accessToken.ts:1-40`); refresh token in an httpOnly, SameSite=Lax, Secure-in-production cookie scoped to `/api/auth`; the `access_token` cookie is httpOnly, Lax and scoped to `/v1/media` (`src/app/api/auth/_lib/session.ts:86-126`). State-changing feed routes therefore never receive a cookie credential from the browser, so cross-site request forgery does not apply to them. Admin routes have their own CSRF check (`main.go:1055-1058`).
- CORS in production is a fixed three-origin list (`values-prod.yaml`), with `Vary: Origin` (`main.go:1282`).
- Android: release builds log no HTTP traffic and debug builds log request lines only (`mobile/android/core/network/.../di/NetworkModule.kt:105-119`); cleartext is off in the main config (`app/src/main/res/xml/network_security_config.xml:10`); tokens are kept under an Android Keystore key (`core/auth/.../KeystoreTokenStore.kt:19-26`); the feed modules contain no WebView; the one external link opener allows https only (`core/feed/.../ui/action/ActionLeavingPage.kt:80-95`).
- Page size caps exist on every list route I traced (values in the route table).

**Secrets**
- No secret values in `deploy/services/{api-gateway,feed-service,post-service,graph-service}` or `charts/`: secrets are ExternalSecret references only. No key-shaped strings (Google, Stripe live, AWS, private key blocks) in `mobile/android` outside the excluded files or in the web `src` and `public` trees. The web tree tracks only `.env.example`.
- One default DSN with a local development credential (type: PostgreSQL password, value masked) in a one-off tool: `feed-service/cmd/backfill/main.go:97`. Localhost only; P3, remove the default.

---

## 4. Out of scope observations

- Web renders server HTML with `dangerouslySetInnerHTML` in Q&A answers (`src/app/qa/questions/[id]/page.tsx:158`), community posts (`src/components/communities/CommunityPostCard.tsx:312`) and community wiki pages (`CommunityWikiTab.tsx:119`): stored XSS unless the server sanitises. Not read further.
- `/v1/internal/*` on post-service (media-access, post visibility, by-media, sound-uses) is protected by the shared key only, with no service token (see B-9).
- Consumer session revocation fails open when Redis cannot answer (`sessionrevocation.go:186-203`); deliberate, bounded by the 15-minute access token.
- Anonymous reads (`/v1/posts/recent`, `/batch`, by-author) are limited only by the gateway's 100 requests/second per IP; that is a scraping rate of thousands of posts a second per address.
- `GET /v1/feed/feedback/authors` and `/v1/feed/muted` return whole lists with no page size.

---

## 5. Not verified

Each of these is UNVERIFIED, not a pass.

1. **Nothing was executed.** No unit test, no request. Every finding and every "sound" line rests on reading.
2. **Media in a feed card.** media-service does call post-service's `/v1/internal/media-access` (`media-service/internal/delivery/authz.go:194-195`), but I did not read the serve path end to end, the signed-URL lifetime, or what happens when that call fails.
3. **`pkg/clientip`** (trusted proxy walk) was not read in full; B-6 and B-8 depend on it.
4. **Notification effects** of B-1 and B-4 (does the author get a push, is it de-duplicated).
5. **Comment moderation on edit**, and whether comment creation runs any text filter.
6. **Android handling of `rich_text`** style values and of `link_url` outside the Action button.
7. **Staleness of cached feed rows after an unfollow** of a public author with followers-only posts (five-minute hydration cache; `unfollow_purge.go` not read).
8. **The dev and staging environments' actual configuration** (is the internal key set, is HS256 still accepted on the AWS dev box). `.env` files were not opened by rule.
9. **Web deployment topology and headers**: no web values file in the repo; WAF rules (`modules/waf`) not read; whether the CNI enforces NetworkPolicy.
10. **auth-service token minting** (that it really emits `typ`, `iss`, `aud`, `sid` as the gateway requires) and the password-change handler by name; only the revocation writers were traced.
11. **`ListSavedItems` store cap**, `GetRecentPosts` and by-author store caps (handlers pass the client's limit through; store caps at `posts.go:1089`, `1445` seen but not matched to each function).
12. **Hashtag, trending, stories, polls beyond the vote gate, threads, product tags, offline copies** and every other post-service route not listed in the table.
13. **Kafka consumer trust**: whether feed-service validates event payloads it fans out from.
