# Feed: findings

- **Audit date:** 2026-11-16. Code: `codex/module-01-02-launch-safety` @ `3c860874e` (working tree), web `feat/echo-web` @ `ca96921`.
- **Count:** 80 findings: 2 P0, 13 P1, 45 P2, 20 P3.
- **Evidence levels (primary level of each finding):** 5 VERIFIED-RUN, 67 VERIFIED-READ, 7 SUSPECTED, 1 UNVERIFIED. Several findings carry a second level for one part (for example the mechanism is read and its size under load is suspected); the field in each finding says which part is which.
- **Nothing was executed against a running stack.** No database, server, device or deployed environment was used (repository rules forbid it). "VERIFIED-RUN" here means a unit test, a CI log or a repository command.
- **Stage 3:** every P0 and P1 was re-checked by a fresh reviewer who was told to disprove it from the cited evidence. All survived; three were narrowed. The outcome is the `Verification:` line on each.
- **IDs:** `feed-NN` is the final ID. The track ID (A = functional, B = security, C = data, D = performance, E = reliability and deployment, F = tests, V = found during verification) is kept because the findings refer to each other by it, and the full reasoning is in `tracks/`.
- **Merged:** E-6 into C-1 (same defect), E-14 into C-2 (same defect).
- Backend paths are relative to `Architecture/services/` unless they start with another top-level folder; web paths are in `C:\workspace\wt\pb-echo`.

## Index

| ID | Track ID | Severity | Evidence | Title |
|---|---|---|---|---|
| feed-01 | E-1 | P0 | VERIFIED-READ | The chart's liveness probe calls /livez, which feed-service, post-service and graph-service do not serve, so their pods would be restarted in a… |
| feed-02 | E-2 | P0 | VERIFIED-RUN | No production (or QA) environment exists and CI has never produced a deployable image of the current code. |
| feed-03 | B-1 | P1 | VERIFIED-READ | Share and repost never check that the caller may see the post, so a blocked user or a stranger to a private account can share or repost it by id. |
| feed-04 | A-1 | P1 | VERIFIED-READ | A like made on the web through POST /v1/posts/:id/react is not removed from feed-service's 5-minute hydration cache, so the next feed refetch… |
| feed-05 | A-2 | P1 | VERIFIED-READ | GET /v1/feed/home does not refill a page after its filters, so a Following page whose newest ~30 timeline rows are reels or other filtered rows… |
| feed-06 | C-1 | P1 | VERIFIED-READ | A dependency outage longer than about 7 seconds moves every fan-out, delete and unfollow event to the dead-letter topic, and nothing can replay it. |
| feed-07 | C-2 | P1 | VERIFIED-READ | post-service's outbox relay publishes each row with a new event id, a random partition key and no lease, so events for one post can arrive out of… |
| feed-08 | D-1 | P1 | VERIFIED-READ | feed-service handles social.events.v1 one event at a time per pod, so one follow (about 100 sequential Scylla writes) or one delete of a widely… |
| feed-09 | D-3 | P1 | VERIFIED-READ | The web feed card loads every image, video and avatar through /v1/media/<id>/serve, which the Next.js server proxies and streams, so media bytes… |
| feed-10 | E-3 | P1 | VERIFIED-READ | Alert rules exist only for feed-service in prod and nothing routes any alert to a person. |
| feed-11 | E-4 | P1 | VERIFIED-READ | There is no rollback procedure and no evidence one has ever been exercised; migrations are forward-only and run inside the application at boot. |
| feed-12 | E-5 | P1 | VERIFIED-READ | ScyllaDB (home timelines, likes, reactions, comment and share indexes) has no backup of any kind, and no restore has been tested for any store. |
| feed-13 | F-1 | P1 | VERIFIED-READ | The only cross-service feed journey test polls GET /v1/feed, a route feed-service does not register, so no journey has a working end-to-end test. |
| feed-14 | F-2 | P1 | VERIFIED-READ | The like write path has no server test at any level: the engagement package and the Scylla interaction store have zero test files. |
| feed-15 | D-2 | P1 | VERIFIED-READ | No load test exists for any home feed endpoint and none has ever been run, so the 200 ms p95 target and every capacity figure are unproven. |
| feed-16 | A-3 | P2 | VERIFIED-READ | Voice posts reach the home feed but neither home client has an audio renderer; on Android a voice post is treated as a photo page. |
| feed-17 | A-4 | P2 | VERIFIED-READ | Android ignores is_repost / reposted_by, so a repost in Home is shown as an ordinary post by its original author, and both clients silently drop a… |
| feed-18 | A-5 | P2 | VERIFIED-READ | The like count the web shows after a tap is COUNT(*) of PostgreSQL `reactions`, while the count on every feed card is Scylla… |
| feed-19 | A-6 | P2 | VERIFIED-READ | A viewer who follows nobody gets one page of For You (at most `limit` posts) and then the feed ends; cold-start fill runs on the first page only… |
| feed-20 | A-7 | P2 | SUSPECTED | On the web a newly created post is put at the top of For You and the feed is refetched in the same tick, before fan-out has written the timeline… |
| feed-21 | B-2 | P2 | VERIFIED-READ | A post's rich_text style values are stored unchecked and the web puts them straight into CSS, so any author can make every viewer's browser load a… |
| feed-22 | B-3 | P2 | VERIFIED-READ | Editing a comment has no length limit and no rate limit, and a share's quote_text has no length limit; the only bound is the gateway's 10 MiB body… |
| feed-23 | B-4 | P2 | VERIFIED-READ | The legacy reaction routes (POST and DELETE /v1/posts/:postId/reactions) have no per-user rate limit, unlike /like and /react. |
| feed-24 | B-5 | P2 | VERIFIED-READ | Feed account erasure reports "purged" while leaving the user's home timeline, older author-timeline buckets and Redis rows in place. |
| feed-25 | B-6 | P2 | SUSPECTED | The web /v1 proxy forwards no client address, so the gateway's per-IP limit sees every web visitor as one client. |
| feed-26 | B-7 | P2 | VERIFIED-READ | post-service and graph-service return the raw Go error text to the client on a 500. |
| feed-27 | B-9 | P2 | VERIFIED-READ | One shared internal key is the only thing between any pod in the cluster and acting as any user on feed, post and graph; no NetworkPolicy narrows… |
| feed-28 | C-3 | P2 | VERIFIED-READ | A retried post.visibility_changed event deletes the rows its first attempt wrote and then resumes the fan-out from the saved position, so… |
| feed-29 | C-4 | P2 | VERIFIED-READ | Unfollow and repost events are published from a goroutine straight to Kafka, not through an outbox, so a failed publish is lost for good. |
| feed-30 | C-5 | P2 | VERIFIED-READ | POST /v1/posts/:postId/react is not safe under concurrent or repeated requests: it reads without a lock, emits an unreact event for deletes that… |
| feed-31 | C-6 | P2 | VERIFIED-READ | The saves count on a feed card never changes: the live bookmark routes do not move bookmark_count. |
| feed-32 | C-7 | P2 | VERIFIED-READ | Share, repost, bookmark and follower counts are flushed from Redis to Postgres as absolute values and never seeded, so lost Redis keys overwrite… |
| feed-33 | D-4 | P2 | VERIFIED-READ | GET /v1/feed/delta sends up to 200 post ids to post-service in one batch call, post-service refuses more than 100, and the pill request then fails… |
| feed-34 | D-5 | P2 | VERIFIED-READ | graph-service caches each user's privacy settings for 3 seconds and reads them one HTTP call per author from user-service, on a client with 2 idle… |
| feed-35 | D-6 | P2 | VERIFIED-READ | The viewer's following list is read on the request path by OFFSET paging, 100 ids per HTTP call, one call after another, and graph-service refuses… |
| feed-36 | D-8 | P2 | VERIFIED-READ | The API gateway proxies every route through Go's default HTTP transport: 2 idle connections kept per upstream and no time limit on waiting for an… |
| feed-37 | D-9 | P2 | VERIFIED-READ | post-service reads like counts and the viewer's reactions with two Scylla point reads per post (2N per page, at QUORUM), 16 at a time. |
| feed-38 | D-10 | P2 | SUSPECTED | Every like is a lightweight transaction on the post's single reactions_by_post partition, which has no shard and no size limit, so likes on one… |
| feed-39 | D-11 | P2 | VERIFIED-READ | The comment list sorts in ways the index cannot serve: "top" runs a COUNT subquery for every comment on the post and pages by OFFSET; "newest"… |
| feed-40 | D-12 | P2 | VERIFIED-READ | The hydration cache stores a full copy of every post body per viewer in Redis for 5 minutes, saves only one of about 35 calls on a hit, and its… |
| feed-41 | E-7 | P2 | SUSPECTED | A stalled Kafka consumer is invisible: readiness does not cover it and the lag gauge freezes when fetching stops. |
| feed-42 | E-8 | P2 | VERIFIED-READ | On shutdown the three services close their databases before draining HTTP, and the gateway does not handle SIGTERM at all. |
| feed-43 | E-9 | P2 | VERIFIED-READ | Readiness fails on any shared store, so a short Redis problem takes every feed, post and graph pod out of service at once even where the code… |
| feed-44 | E-10 | P2 | VERIFIED-READ | The home feed fails completely when identity-profile-service, media-service or trust-safety-service is down, although only the trust-safety lookup… |
| feed-45 | E-11 | P2 | VERIFIED-READ | A like is recorded in Redis first; the ScyllaDB write is best-effort and the Kafka event that feeds the durable counters is fire-and-forget. |
| feed-46 | E-12 | P2 | VERIFIED-READ | A request cannot be followed from the gateway through feed-service to graph-service or post-service: the request ID is not forwarded and the three… |
| feed-47 | E-13 | P2 | VERIFIED-RUN | The code that would ship has no current CI verdict for unit tests, lint or security scans, and feed-service and api-gateway have no tests against… |
| feed-48 | E-15 | P2 | VERIFIED-READ | The one-off timeline repair has not been run anywhere and every environment still reads the legacy timeline index. |
| feed-49 | F-3 | P2 | VERIFIED-READ | POST /v1/posts/:id/like is a pure toggle keyed on a Redis key with a 24 h TTL, so concurrent or late taps do not converge on "liked, count +1". |
| feed-50 | F-4 | P2 | VERIFIED-READ | feed-service has no integration-tagged test and is absent from the CI integration manifest, so no automated test writes or reads a real Scylla… |
| feed-51 | F-5 | P2 | VERIFIED-READ | Blocking is tested per service but never across them, and no test covers comments by a blocked account. |
| feed-52 | F-6 | P2 | VERIFIED-READ | The PostDeleted consumer handler has no behaviour test, and removal of a plain post's notifications is tested only for Echo notes. |
| feed-53 | F-7 | P2 | VERIFIED-RUN | Home pagination is proven only on a static timeline; no test inserts posts between pages or pages across the celeb pull-merge. |
| feed-54 | F-8 | P2 | VERIFIED-READ | The home feed's fall back to chronological order when the ranker fails has no test. |
| feed-55 | F-9 | P2 | VERIFIED-READ | No test shows a poison or exhausted message reaching the dead-letter topic, and no feed handler is run twice in a test. |
| feed-56 | F-10 | P2 | UNVERIFIED | No feed test covers an author who is suspended or deactivated after their posts were fanned out. |
| feed-57 | F-11 | P2 | VERIFIED-READ | Web unit tests are not run by any CI workflow, the web tree has no test script, and there is no browser end-to-end test. |
| feed-58 | F-12 | P2 | VERIFIED-READ | Client gaps: the web like hook's rollback, the web feed page's empty and error states, the Android FeedViewModel, and an unknown post type are… |
| feed-59 | F-15 | P2 | VERIFIED-READ | Three like routes write three stores and no test shows a like made through one is counted and shown through the others. |
| feed-60 | V-1 | P2 | VERIFIED-READ | The follow backfill copies the followee's author timeline, which includes private and unlisted posts, into the new follower's home timeline… |
| feed-61 | A-8 | P3 | SUSPECTED | Q&A questions are fanned out into home timelines under their question id, but home hydration only asks post-service's post batch, so those rows… |
| feed-62 | A-9 | P3 | VERIFIED-READ | Notifications in other users' inboxes that point at a deleted post are not removed; only the author's inbox is cleaned. |
| feed-63 | B-8 | P3 | SUSPECTED | The gateway serves /metrics on its public listener to any request without a forwarding header, and the web proxy can be steered outside /v1. |
| feed-64 | B-10 | P3 | VERIFIED-READ | A wildcard CORS origin is only warned about, and the gateway then reflects any origin with credentials allowed. |
| feed-65 | C-8 | P3 | VERIFIED-READ | The like count on a feed card has no repair path and no floor: a failed counter update after the conditional write leaves it wrong for good, and… |
| feed-66 | C-9 | P3 | VERIFIED-READ | Restoring a deleted post does not bring back the repost rows that the delete removed. |
| feed-67 | C-10 | P3 | VERIFIED-READ | The follow backfill swallows write failures, and the unfollow purge covers only the last three months of a 360-day timeline. |
| feed-68 | C-11 | P3 | VERIFIED-READ | Comment, like and unlike side effects run after the main write with errors only logged, and a comment without an Idempotency-Key is not protected… |
| feed-69 | D-7 | P3 | SUSPECTED | Fan-out pages an author's followers by OFFSET while the list can change, so a follower who is shifted across a page boundary by another account's… |
| feed-70 | D-13 | P3 | VERIFIED-READ | One home page repeats the same lookups: "don't recommend" authors twice, hidden authors twice, distribution policy twice, and three graph-service… |
| feed-71 | D-14 | P3 | VERIFIED-READ | The discovery pool asks post-service for 100 recent posts and gets 20, and when the 2-minute pool key expires every request in flight refetches it… |
| feed-72 | D-15 | P3 | VERIFIED-READ | An edited post keeps its old text for up to 5 minutes for any viewer who has it in the hydration cache, because no event marks the row stale on edit. |
| feed-73 | D-16 | P3 | VERIFIED-READ | A viewer with a short or empty timeline costs 12 sequential Scylla reads per page, plus up to 12 more for every followed celeb, because the read… |
| feed-74 | D-17 | P3 | VERIFIED-READ | Datastore clients are left on defaults that wait instead of failing fast: gocql has no retry or host-selection policy set, and Redis commands may… |
| feed-75 | E-16 | P3 | VERIFIED-READ | Boot-time migrations for feed, post and graph run without the advisory lock the shared runner already offers. |
| feed-76 | E-17 | P3 | VERIFIED-RUN | Builds are vendored but not fully pinned, and the working tree being tested differs from the committed tree in go.work.sum and 572 vendor files. |
| feed-77 | E-18 | P3 | VERIFIED-READ | The staging values files are stale and disagree with prod and QA, and promote.yml takes its image tags from them. |
| feed-78 | F-13 | P3 | VERIFIED-RUN | The feed-row contract fixture is shared byte-for-byte between Go and Android but not with the web, and it covers one row shape, not the home… |
| feed-79 | F-14 | P3 | VERIFIED-READ | No mutation result is recorded for any feed guard. |
| feed-80 | F-16 | P3 | VERIFIED-READ | The post-deploy smoke checks the home feed's shape only and never checks that a created post reaches a feed. |

## P0

```
ID:            feed-01 (track ID E-1)
Title:         The chart's liveness probe calls /livez, which feed-service, post-service and graph-service do not serve, so their pods would be restarted in a loop on Kubernetes.
Severity:      P0
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      charts/atpost-service/values.yaml:145-146 (livenessProbe.path: /livez); charts/atpost-service/templates/deployment.yaml:140-146 renders it for every service.
               Architecture/shared/health/checker.go:135-138 registers only /healthz and /readyz.
               feed-service/cmd/server/main.go:262, post-service/cmd/server/main.go:692, graph-service/cmd/server/main.go:315 call checker.RegisterRoutes and add no /livez.
               No livenessProbe override in deploy/services/{feed,post,graph}-service/values-{prod,qa,staging}.yaml (ripgrep "livez|livenessProbe" over deploy/, charts/, infra/ matches only the chart and deploy/web/*, whose comment says "the chart's defaults /livez and /readyz do not exist on a Next app").
               Only api-gateway serves /livez (api-gateway/cmd/server/probes.go:16).
How to reproduce: Render the chart for feed-service prod and read the livenessProbe path; then GET /livez on a running feed-service (expect 404 or 401, not 200).
Impact:        With initialDelaySeconds 10, periodSeconds 10, failureThreshold 3, the kubelet kills each pod about 30-40 s after start, for ever. The home feed, posting, likes, follows and blocks never stay up on EKS. The dev box does not use the chart, which is why this has not been seen.
Fix:           Smallest: make shared/health also register GET /livez (same handler as /healthz). Alternative: change the chart default to /healthz and set /livez for api-gateway only.
Acceptance:    A unit test in shared/health asserts /livez answers 200; render_test.sh asserts that each service's liveness path is one its binary registers.
Effort:        S
Verification:  CONFIRMED by an independent verifier. `charts/atpost-service/values.yaml:145-146` sets `/livez`; `Architecture/shared/health/checker.go:136-137` registers only `/healthz` and `/readyz`; no feed, post or graph values file overrides it; only api-gateway serves `/livez` (`cmd/server/probes.go:16`). It does not bite on the dev box, which does not use the chart. It appears to affect every Go service on the shared health package.
```

```
ID:            feed-02 (track ID E-2)
Title:         No production (or QA) environment exists and CI has never produced a deployable image of the current code.
Severity:      P0
Category:      deployment
Evidence level: VERIFIED-RUN
Evidence:      docs/runbooks/aws-first-production-deployment.md:7 "Nothing in this plan has been run. The repo's AWS design has never been applied" (2 Oct; last commit to that file 8082dca4a, 2026-10-02).
               `git ls-remote --heads origin` shows refs/heads/main only for the names main / qa / release/prod: the branches ArgoCD tracks (deploy/argocd/applicationset.yaml:64, deploy/README.md:27) do not exist.
               `gh run list --workflow build-push.yml --limit 4`: all four are failure, the latest 2026-10-03. `gh run list --workflow promote.yml`: no runs.
               .github/workflows/build-push.yml:33-36 builds on push to main only; `git log -1 origin/main` = adad00644 (2026-08-26) and `git rev-list --count origin/main..HEAD` = 1058. build-push.yml:73-87 skips every build while secrets.AWS_CI_ROLE_ARN is unset ("it did not on 25 Oct 2026").
               deploy/services/feed-service/values-prod.yaml:13 tag "" ; deploy/services/api-gateway/values-prod.yaml:183,186 still hold __TF_*__ placeholders; no *.tfvars in infra/terraform/envs/prod or envs/qa.
               deploy/README.md:204 says the new AWS account "is QA for good; production gets its own account later". infra/devbox/README.md:3-5: the running stack is Docker Compose on one EC2 server.
How to reproduce: Run the commands above.
Impact:        There is nowhere to launch to. Every production control in this track (probes, HPA, PDB, ExternalSecrets, alert rules, backups) is untested code. The first deployment will be the first test of all of it.
Fix:           Follow the plan's own order on QA first: Terraform, secrets seeder, a successful build-push run from the branch that ships, promote, sync, then scripts/smoke against QA. No new infrastructure is needed; the designed one has to be applied once.
Acceptance:    A green build-push run for feed/post/graph/gateway at the launch commit; `kubectl -n atpost get deploy` on QA shows all four Available for 24 h with zero restarts; smoke passes against QA.
Effort:        L
Verification:  CONFIRMED as far as the repository and GitHub show. No `qa` or `release/prod` branch; `build-push.yml` has no successful run (last attempt 2026-10-03, failing in 0 s on a workflow-file error); `promote.yml` has never run; `docs/runbooks/aws-first-production-deployment.md:7` says nothing has been applied. Two things the first reviewer left out: `build-push-acr` succeeded once on 2026-06-29 on old `main` code, and a GitHub environment `qa` has one deployment record (2026-10-25) whose Terraform run was cancelled. Live cloud state could not be seen. This is a missing prerequisite, not a code defect; it is P0 because there is nothing to launch on.
```

## P1

```
ID:            feed-03 (track ID B-1)
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
Verification:  CONFIRMED. Share handler has no `requirePostReadable` (`post-service/internal/http/reactions_handler.go:155-197`) though sibling handlers do (:43, :96, :229); repost follows the same pattern (`reposts_handler.go:45-80`, `service/post_reposts.go:43-71`). In addition `repostableVisibility` refuses only `private` and `friends` (`post_screen_fields.go:62-68`), so `followers` posts are repostable. Not traced: whether the author is notified, and whether the fan-out exposes the body to the reposter's followers (the cached hydration path re-filters per viewer).
```

```
ID:            feed-04 (track ID A-1)
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
Verification:  CONFIRMED. Web calls `/v1/posts/:id/react` (`pb-echo/src/hooks/useFeedReaction.ts:38`); `ToggleReaction` (`post-service/internal/service/post_reactions.go:163-225`) never invalidates the feed hydration cache, while `React` and `Unreact` do; feed-service's reaction handlers do not touch `feed:hydrate:*`; the cached row holds `viewer_reaction` for 5 minutes (`feed-service/internal/service/hydration.go:25, 376-381`). It only bites when the refetch lands within 5 minutes of that post's last hydration.
```

```
ID:            feed-05 (track ID A-2)
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
Verification:  CONFIRMED. One read of `limit + max(limit/2, 10)` rows with no refill (`feed-service/internal/service/feed.go:383-401, 578, 606-614`); reels are written to the home timeline on purpose (`longvideo.go:259-271`) and dropped at read (`feed.go:541`); web shows "No posts yet" and both auto-load and "Load more" require `posts.length > 0` (`Feed.tsx:210, 321, 370`). The verifier rates the Android half closer to P2 (it stops after five empty pages, `FeedPagingSource.kt:166-179`).
```

```
ID:            feed-06 (track ID C-1)
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
Verification:  CONFIRMED, with one correction. Defaults are 3 retries at 1/2/4 s (`Architecture/shared/kafka/consumer.go:118-123, 272-345`); feed-service sets no override (`feed-service/internal/events/consumer.go:61-66`) and marks no error permanent. The only Kafka DLQ replayer in the repository belongs to search-service. Correction: 7 s is the fail-fast case; with a hanging dependency each attempt can run to the 2-minute event deadline, so events are parked after up to about 8 minutes. Track E reported the same defect as E-6; it is merged here.
```

```
ID:            feed-07 (track ID C-2)
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
Verification:  PARTLY CONFIRMED. (a) Fresh event id per publish: confirmed (`post-service/internal/service/outbox.go:25`, `shared/events/events.go:2046`). (b) Random key and a `LeastBytes` balancer that ignores keys: confirmed (`post-service/internal/events/producer.go:26, 237`), 6 partitions. (c) No row claim, 2 replicas in production: confirmed, but "every row is published twice" is overstated; it is a race that becomes routine under backlog or a slow broker. Harm: fan-out is an upsert, so duplicates are harmless there, but `PostReacted` and `CommentCreated` do a bare increment, so ranking velocity double-counts; a restore consumed before its delete strips the live post from every timeline and tombstones it for 24 h (`feed-service/internal/events/post_lifecycle.go:55-63, 278`). Track E reported (c) as E-14; it is merged here.
```

```
ID:            feed-08 (track ID D-1)
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
Verification:  CONFIRMED, and slightly worse than stated: the shared consumer handles one event at a time per pod, not per partition (`Architecture/shared/kafka/consumer.go:133-140, 165-207`), so at the 2-replica minimum two events are in flight platform-wide. The follow backfill is up to 50 posts at 2 sequential Scylla inserts each. The lag itself is arithmetic, not a measurement; the verifier calls it P1 if onboarding triggers bulk follows, otherwise a strong P2.
```

```
ID:            feed-09 (track ID D-3)
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
Verification:  CONFIRMED, including for production. Card sources are `/v1/media/<id>/serve` (`PostCard.tsx:338, 688, 734`); media-service answers 307 to a signed URL, but the Next.js proxy fetches with `redirect: "manual"`, follows the hops itself and streams the body (`src/app/api/proxy/[...path]/route.ts:131, 178-203`). With the two storage env vars unset in production the Next pod still downloads from the CDN and relays every byte. Android uses the signed URLs directly. Not checked: whether a production ingress sends the web host's `/v1` straight to the gateway.
```

```
ID:            feed-10 (track ID E-3)
Title:         Alert rules exist only for feed-service in prod and nothing routes any alert to a person.
Severity:      P1
Category:      observability
Evidence level: VERIFIED-READ
Evidence:      deploy/services/feed-service/values-prod.yaml:93-137 defines six rules (lag, DLQ, fan-out, ranker fallback, hydration, p95). `grep -rln prometheusRule deploy/services` = 1 file: no rules for post-service, graph-service or api-gateway, and none in any values-qa.yaml or values-staging.yaml.
               infra/terraform/modules/observability/main.tf:126-147 configures Alertmanager with replicas, storage and a node selector only: no `config`, receivers or routes (grep for "receivers|route|webhook|sns" in the module: no match). The SNS e-mail topic in infra/terraform/modules/ops-baseline/main.tf:17-71 receives budget, backup, GuardDuty and SES events; nothing connects Alertmanager to it.
               No Grafana dashboard for these services in the repo (grep "grafana_dashboard" finds only the sidecar label in the observability module).
How to reproduce: Read the files above.
Impact:        A firing FeedHydrationFailing or FeedConsumerLagHigh is visible only to someone who opens the Alertmanager UI. A dead post-service, graph-service or gateway raises nothing at all. Outages are found by users.
Fix:           Add an Alertmanager config to the existing observability module with one receiver (e-mail through the existing ops SNS topic, or a Slack webhook) and a route for severity page/ticket. Add to the chart a default rule set for every service: pod restarts, 5xx ratio, p95 latency, readiness failing. Copy the feed rules into values-qa.yaml.
Acceptance:    On QA, scaling graph-service to zero produces a message in the chosen channel within 10 minutes.
Effort:        M
Verification:  PARTLY CONFIRMED. Rules exist only at `deploy/services/feed-service/values-prod.yaml:93`; Alertmanager in `infra/terraform/modules/observability/main.tf:127-146` has no receivers or routes. Correction: an e-mail (SNS) subscription does exist in `modules/ops-baseline/main.tf:68-70`, but it carries budget, backup, GuardDuty and SES alerts only. Service alerts still reach nobody.
```

```
ID:            feed-11 (track ID E-4)
Title:         There is no rollback procedure and no evidence one has ever been exercised; migrations are forward-only and run inside the application at boot.
Severity:      P1
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      docs/runbooks/aws-first-production-deployment.md:133 lists "a written rollback (ArgoCD history) drill" as future hardening step 9. grep -i "rollback|roll back" over docs/runbooks, docs/harness, deploy/README.md, DEPLOY_SECRETS.md, SECURITY.md, PRODUCTION_READY_SUMMARY.md, infra/terraform/envs/prod/README.md finds no procedure. .github/workflows/promote.yml has no rollback or revert path.
               feed-service/cmd/server/main.go:67, post-service/cmd/server/main.go:129, and graph-service's bootstrap apply migrations at process start through shared/store/migrationrunner/runner.go:44-113 (forward only, no down files). post-service/cmd/server/main.go:134 also runs ensureSchema DDL at boot.
               The new version's migrations therefore run when its first pod starts, while old pods still serve. Rolling the image back does not undo them.
How to reproduce: Read the files above.
Impact:        A bad release cannot be backed out with confidence. Whether version N-1 runs against version N's schema is unknown for every release, and nobody has practised the steps.
Fix:           Write a one-page procedure: revert the values-<env>.yaml tag commit, sync in ArgoCD, check /readyz and smoke. Adopt the rule that a migration must be compatible with the previous image (add, backfill, then remove in a later release). Drill it once on QA.
Acceptance:    A dated record of a QA drill: deploy N, roll back to N-1, smoke passes, time taken written down.
Effort:        M
Verification:  CONFIRMED. No down migrations; migrations run at boot (`feed-service bootstrap.go:26`, post `bootstrap.go:31`, graph `bootstrap.go:31`); the chart's migration job is off by default and only commerce-service opts in; the only rollback mention is a future step at `docs/runbooks/aws-first-production-deployment.md:133`.
```

```
ID:            feed-12 (track ID E-5)
Title:         ScyllaDB (home timelines, likes, reactions, comment and share indexes) has no backup of any kind, and no restore has been tested for any store.
Severity:      P1
Category:      data
Evidence level: VERIFIED-READ
Evidence:      infra/terraform/modules/scylla/main.tf:89-159 defines the ScyllaCluster with racks, resources and storage only; grep -i "backup|snapshot|manager" in modules/scylla matches only the agent version variable description (variables.tf:23). No Scylla Manager, no backup task, no bucket.
               Aurora: backup_retention_period from var (infra/terraform/modules/aurora/main.tf:188; prod default 7 days, envs/prod/variables.tf:326-330), deletion_protection true (envs/prod/main.tf:120), plus AWS Backup daily, 14 days (envs/prod/ops.tf:4-13, modules/ops-baseline/main.tf:75-140).
               ElastiCache: daily snapshot, 5 days (envs/prod/main.tf:151, modules/elasticache/main.tf:142-146).
               None of this is applied (E-2). docs/runbooks/aws-first-production-deployment.md:133 lists "Backups verified by a restore" as a future step. grep for "restore drill|restore test|pg_restore" in docs/, infra/, deploy/ finds no record.
               Dev box: no database backup (infra/devbox/devbox.cfn.yaml:406 DeleteOnTermination true on the data disk; only the S3 buckets are retained).
How to reproduce: Read the files above.
Impact:        A lost or corrupted Scylla cluster loses social_engagement (post_likes, user_likes, reactions_by_post, comments_by_post, bookmarks) and every home timeline. Timelines could be rebuilt with cmd/backfill; engagement rows have no other complete copy that this track could find. For Postgres and Redis the backups are configuration that has never produced a restorable copy.
Fix:           Scylla: enable the operator's backup task (Scylla Manager, which the operator chart already supports) to an S3 bucket, daily, 7-14 days. Then run one restore of each store on QA and write down the steps and the time.
Acceptance:    A dated restore record for Aurora (point in time), ElastiCache (snapshot) and Scylla (one keyspace) on QA, with row counts compared.
Effort:        M
Verification:  CONFIRMED. `infra/terraform/modules/scylla` has no backup resource; `deploy/jobs` holds only `scylla-schema` and `timeline-maintenance`. Aurora and ElastiCache backups are defined in code but unapplied. No restore record exists. The verifier notes this becomes a data-loss exposure (P0 by definition) once real likes and comments are stored.
```

```
ID:            feed-13 (track ID F-1)
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
Verification:  CONFIRMED, and worse than claimed. `e2e.yml` shows 15 green runs, but in the latest one the social job logs "not reachable", then `--- SKIP: TestE2E_Social_PostFollowFeedLike`, then `PASS`: `SkipIfDown` (`Architecture/tools/integration/client.go:255-272`) turns a dead gateway into a skip. The journey has never been exercised by CI.
```

```
ID:            feed-14 (track ID F-2)
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
Verification:  CONFIRMED. No `_test.go` under `post-service/internal/engagement` or `internal/store/scylla`; no test references `ToggleLike`. The verifier notes P2 is arguable because this is a coverage gap; it stays P1 here because the independent check of the like path (see feed finding for track ID F-3) found a real over-count that a test would have caught.
```

```
ID:            feed-15 (track ID D-2)
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
Verification:  CONFIRMED by repository search: the only load scripts are for dating and rider; `docs/TESTING.md:237` says so; no benchmark exists in feed, post, graph, gateway or shared. This is a process gap, not a defect.
```

## P2

```
ID:            feed-16 (track ID A-3)
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
ID:            feed-17 (track ID A-4)
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
ID:            feed-18 (track ID A-5)
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
ID:            feed-19 (track ID A-6)
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
ID:            feed-20 (track ID A-7)
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
ID:            feed-21 (track ID B-2)
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
ID:            feed-22 (track ID B-3)
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
ID:            feed-23 (track ID B-4)
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
ID:            feed-24 (track ID B-5)
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
ID:            feed-25 (track ID B-6)
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
ID:            feed-26 (track ID B-7)
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
ID:            feed-27 (track ID B-9)
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
ID:            feed-28 (track ID C-3)
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
ID:            feed-29 (track ID C-4)
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
ID:            feed-30 (track ID C-5)
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
ID:            feed-31 (track ID C-6)
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
ID:            feed-32 (track ID C-7)
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
ID:            feed-33 (track ID D-4)
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
ID:            feed-34 (track ID D-5)
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
ID:            feed-35 (track ID D-6)
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
ID:            feed-36 (track ID D-8)
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
ID:            feed-37 (track ID D-9)
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
ID:            feed-38 (track ID D-10)
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
ID:            feed-39 (track ID D-11)
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
ID:            feed-40 (track ID D-12)
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
ID:            feed-41 (track ID E-7)
Title:         A stalled Kafka consumer is invisible: readiness does not cover it and the lag gauge freezes when fetching stops.
Severity:      P2
Category:      observability
Evidence level: SUSPECTED
Evidence:      feed-service/cmd/server/main.go:118-125 registers readiness checks for postgres, redis and scylladb only. The consumer goroutines (lines 208, 218, 234, 252) report to nothing.
               The loop no longer exits on a fetch error (shared/kafka/consumer.go:186-195), which is sound, but retryDurable (lines 435-455) blocks a partition for ever on a failing commit or DLQ write, logging only.
               The lag gauge reads reader.Stats().Lag every 10 s (consumer.go:462-481). In the vendored kafka-go the periodic lag reader is off for consumer groups (vendor/github.com/segmentio/kafka-go/reader.go:1145-1151) and the gauge is set only when a fetch returns (reader.go:1531), as the last fetched partition's value, not a sum.
               The channel-update consumer (internal/consumers/channel_updates.go:89-112) and the account-purge consumer have no metrics at all.
What would confirm it: on dev, block the handler (for example pause Scylla) and watch atpost_feed_service_kafka_consumer_lag while producing events: it should stay flat.
How to reproduce: As above.
Impact:        The pod stays Ready and FeedConsumerLagHigh may not fire while feeds stop updating.
Fix:           Record the time of the last successful handler return and of the last fetch in the shared consumer; export `kafka_consumer_last_progress_timestamp_seconds`; alert when it is older than 5 minutes while the topic has traffic. Do not add it to readiness (removing the pod from the Service does not help a consumer).
Acceptance:    The dev experiment above raises the alert within 10 minutes.
Effort:        S
```

```
ID:            feed-42 (track ID E-8)
Title:         On shutdown the three services close their databases before draining HTTP, and the gateway does not handle SIGTERM at all.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      Architecture/shared/server/server.go:83-94: on a signal it calls cfg.OnShutdown() first (line 88), then srv.Shutdown(ctx) (line 91).
               feed-service/cmd/server/main.go:270-279 OnShutdown closes rdb, scyllaSession and dbPool; post-service/cmd/server/main.go:745-753 and graph-service/cmd/server/main.go:323-328 do the same. Requests still in flight during the drain then run against closed stores and fail.
               api-gateway/cmd/server/main.go:309 `log.Fatal(edgelimits.NewServer(...).ListenAndServe())`; grep "signal\." in api-gateway/cmd/server finds nothing. SIGTERM ends the process and cuts every proxied request.
               charts/atpost-service/templates/deployment.yaml has no preStop hook and no terminationGracePeriodSeconds (grep: no match), so a pod also receives SIGTERM while the load balancer and kube-proxy may still send it new requests.
               feed-service/cmd/server/main.go:252 starts the account-lifecycle consumer on ctx, not consumerCtx, so it is never cancelled before its reader is closed (line 251 defer only).
How to reproduce: Send SIGTERM to feed-service while a /v1/feed/home request is in flight; the request fails with a closed-pool error.
Impact:        Every rolling deploy and every node drain produces a burst of 5xx on feed, like, comment and follow requests. Consumers are safe: offsets are committed only after the handler (see Checked and sound).
Fix:           In shared/server.Run call srv.Shutdown before OnShutdown. Add a 5 s preStop sleep to the chart. Give the gateway the same signal handling (http.Server.Shutdown with a 10 s timeout).
Acceptance:    A unit test in shared/server proves an in-flight request completes before OnShutdown runs; a rolling restart on QA under a light k6 load shows zero 5xx.
Effort:        S
```

```
ID:            feed-43 (track ID E-9)
Title:         Readiness fails on any shared store, so a short Redis problem takes every feed, post and graph pod out of service at once even where the code degrades without Redis.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ (readiness); SUSPECTED (that the home read works fully without Redis)
Evidence:      Architecture/shared/health/checker.go:62-109 marks the service down if any check fails; feed-service/cmd/server/main.go:119-125 includes Redis; post-service/cmd/server/main.go:344-348 and graph-service/cmd/server/main.go:123-124 likewise. Probe: every 5 s, 3 failures (charts/atpost-service/values.yaml:151-155), check timeout 3 s.
               The feed code itself tolerates Redis errors on the paths read: rate limit fails open (feed-service/internal/http/handler.go:866-874), ranker falls back to chronological (internal/service/feed.go:584-588), policy cache falls back conservatively (internal/service/longvideo.go:382-391).
What would confirm it: stop Redis on dev and call /v1/feed/home with readiness ignored.
How to reproduce: Stop Redis for 20 s on a cluster; all pods of the three services go NotReady together.
Impact:        A 15-second Redis failover (ElastiCache has one replica in prod) becomes a full outage of feed, posting and graph instead of a slower, unranked feed.
Fix:           Keep Postgres and Scylla in feed-service readiness; report Redis in the /readyz body but do not fail on it for feed-service. Leave post-service as is (its like path needs Redis).
Acceptance:    With Redis stopped, feed-service /readyz answers 200 with redis: down, and /v1/feed/home returns a page.
Effort:        S
```

```
ID:            feed-44 (track ID E-10)
Title:         The home feed fails completely when identity-profile-service, media-service or trust-safety-service is down, although only the trust-safety lookup is a safety filter.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      feed-service/internal/service/hydration.go:658-663: a profile or media fetch error fails the whole hydration ("profile hydration failed", "media hydration failed"); the channel card on the next lines is best-effort. internal/service/keywordfilter.go:112-116 fails closed on trust-safety (cached 60 s per viewer, line 31). feed-service/internal/http/handler.go:179-182 answers 502 HYDRATION_FAILED.
How to reproduce: Stop identity-profile-service on dev; GET /v1/feed/home returns 502.
Impact:        Seven services and stores must all be up for one feed page (Scylla, Postgres, graph, post, trust-safety, profile, media). Author names, avatars and media URLs are needed to draw a card, so serving without them is a product decision; failing the whole page for one missing avatar batch is the costly choice.
Fix:           Decide per dependency. Minimum: when the profile or media batch fails, retry once, then serve the posts whose data is cached and drop the rest with a cursor, instead of 502 for all. Keep the graph and keyword filters fail-closed.
Acceptance:    Test: profile fetch errors, cached authors still render, response is 200 with a shorter page.
Effort:        M
```

```
ID:            feed-45 (track ID E-11)
Title:         A like is recorded in Redis first; the ScyllaDB write is best-effort and the Kafka event that feeds the durable counters is fire-and-forget.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ (code path); SUSPECTED (permanent loss)
Evidence:      post-service/internal/service/post_engagement.go:152-156 toggles in Redis (Lua). Lines 159-171: Scylla React/Unreact errors are logged with log.Printf and ignored. Lines 193-200: the engagement event is published in a goroutine with context.Background(); an error is logged and dropped. Only PostReacted (the notification trigger) goes through the outbox (lines 205-216).
What would confirm it: read engagement.ToggleLike and the pg/scylla counter consumers to see whether anything other than that Kafka event writes the durable like row; then on dev stop Kafka, like a post, restart Redis without persistence.
How to reproduce: As above.
Impact:        With Scylla down the user sees a successful like that hydration may not show; with Kafka down the durable counters miss it. Neither failure has a metric. reconcile.NewEngagementReconciler (post-service/cmd/server/main.go:709) recounts but can only count what was stored.
Fix:           Publish the engagement event through the existing outbox table (as PostReacted already is) and add a counter for Scylla reaction write failures.
Acceptance:    Test: Kafka producer returns an error; the event is still delivered after recovery.
Effort:        S
```

```
ID:            feed-46 (track ID E-12)
Title:         A request cannot be followed from the gateway through feed-service to graph-service or post-service: the request ID is not forwarded and the three services never start tracing.
Severity:      P2
Category:      observability
Evidence level: VERIFIED-READ
Evidence:      The gateway sets X-Request-Id on the upstream request (api-gateway/cmd/server/main.go:1264-1269) and injects the trace context (line 386). feed-service reads it into the logger (Architecture/shared/middleware/requestid.go:22-49).
               feed-service outbound calls set only X-Internal-Service-Key and Content-Type (for example internal/service/privacyfilter.go:175-182, internal/service/feed.go:1444-1452); grep -i "X-Request-ID|traceparent|otelhttp|trace.Init" over feed-service (non-test) returns nothing.
               grep "otel|o11y/trace|Tracing" in post-service and graph-service cmd/server/main.go returns nothing, although every values file sets OTEL_EXPORTER_OTLP_ENDPOINT (deploy/services/feed-service/values-prod.yaml:55) and the FeedHomeLatencyHigh alert says "See the per-dependency breakdown in traces" (line 137).
               feed-service has 160 log.Printf lines against 45 slog calls (grep count, non-test); log.Printf lines are plain text with no request_id, for example internal/service/feed.go:412, 467, 587.
               Architecture/shared/httpclient/client.go has no Prometheus metric (grep prometheus: 0), so an open circuit breaker is visible only in logs.
How to reproduce: Call /v1/feed/home with X-Request-Id: test-1 and search graph-service logs for test-1: nothing.
Impact:        A slow or failing feed page cannot be tied to the downstream call that caused it without matching timestamps by hand. Traces sent to Tempo contain the gateway span only.
Fix:           Add a RoundTripper in shared/httpclient that copies X-Request-Id (and injects traceparent) from the context; it covers every client built by NewWithBreaker. Add the existing OTel middleware and init to the three mains, or remove the endpoint from values and the sentence from the alert. Export breaker state as a gauge.
Acceptance:    The same request_id appears in gateway, feed-service and graph-service logs for one home request.
Effort:        S
```

```
ID:            feed-47 (track ID E-13)
Title:         The code that would ship has no current CI verdict for unit tests, lint or security scans, and feed-service and api-gateway have no tests against a real database.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-RUN
Evidence:      .github/workflows/go-ci.yml:18-35 and security.yml:3-12 run on pull_request, push to main and manual dispatch, not on push to the working branch. `gh run list --workflow go-ci.yml`: latest run 2026-10-31, workflow_dispatch, head 44f00fafe, success; HEAD is 3c860874e. Lint, Security and E2E on this branch: also last run 2026-10-31 (success).
               Integration (all Go services) does run on push: run 43861254948 at 3c860874e is success for 33 modules including post-service, graph-service and Architecture/shared. The two pushes before it failed in commerce-service (run 43858523110). 10 of the last 17 runs on the branch failed.
               That job list has no feed-service and no api-gateway: `grep -rl "go:build integration"` in those two modules returns nothing, and scripts/ci/integration-services.yaml lists post-service (line 41) and graph-service (line 76) only.
               Security on main (weekly schedule) failed on 2026-11-01, 11-08 and 11-15 in every job: gitleaks, trivy, govulncheck x5 (run 43695693441). main is the 26 Aug commit.
               Scanners: gitleaks, govulncheck per module, trivy filesystem with exit-code 1 on CRITICAL/HIGH (security.yml:79-85): they fail the workflow. govulncheck and trivy are skipped on pull requests (lines 31, 76). trivy uses aquasecurity/trivy-action@master (unpinned). Whether a failed check blocks a merge depends on branch protection, which was not read.
How to reproduce: Run the gh commands above.
Impact:        A regression in feed-service unit tests or a new vulnerable dependency since 31 Oct would not be reported by CI. The feed's Scylla and Postgres stores are exercised only with fakes.
Fix:           Add the working branch (or all branches) to the push trigger of go-ci.yml and lint.yml, or open the PR to main so the pull_request triggers run. Run Security by dispatch on the launch commit. Add one integration suite for feed-service's Scylla timeline store and Postgres MetaStore to integration-services.yaml.
Acceptance:    Green Go CI, Lint, Security and Integration runs whose head SHA is the launch commit.
Effort:        M
```

```
ID:            feed-48 (track ID E-15)
Title:         The one-off timeline repair has not been run anywhere and every environment still reads the legacy timeline index.
Severity:      P2
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      FEED_INDEX_LEGACY_READ: "true" in deploy/services/feed-service/values-prod.yaml:78, values-qa.yaml:87, values-staging.yaml:73. Architecture/docker/docker-compose*.yml does not set it (grep: no match), and the code default is on (feed-service/internal/store/scylla/timelines.go:78), so dev reads the legacy index too.
               deploy/jobs/timeline-maintenance/run.sh:21-23 accepts only qa or prod, which do not exist (E-2). The old audit lists the QA run as a to-do (docs/production-readiness/FEED_AUDIT.md:62). grep for a record of a run in docs/harness and docs/production-readiness: none.
How to reproduce: Read the files above.
Impact:        Production starts with empty Scylla tables, so for prod the repair has nothing to fix and the flag can be "false" from day one. On dev the legacy index and unrepaired rows remain, so dev behaviour differs from what prod will run.
Fix:           Set FEED_INDEX_LEGACY_READ "false" in values-prod.yaml and values-qa.yaml now (empty clusters). Run the binary's dry run against dev Scylla once and record the counts.
Acceptance:    Values show "false"; a dated note with the dev dry-run output.
Effort:        S
```

```
ID:            feed-49 (track ID F-3)
Title:         POST /v1/posts/:id/like is a pure toggle keyed on a Redis key with a 24 h TTL, so concurrent or late taps do not converge on "liked, count +1".
Severity:      P2
Category:      correctness (found while reading for tests)
Evidence level: VERIFIED-READ (raised from SUSPECTED by Stage 3; see Verification)
Evidence:      post-service/internal/engagement/lua.go:44-68: the script flips on EXISTS liked:{uid}:{pid}; the SET carries EX 86400 (line 63). The route has no idempotency middleware (internal/http/handler.go:144; compare line 146 for comments). 50 concurrent calls from one user end unliked with a net count of 0. After 24 h the key is gone, so a tap meant to unlike runs the "like" branch and HINCRBY +1 again. Callers: web src/hooks/usePostReaction.ts:18, Echo src/features/echo/api/index.ts:68, dev-smoke.mjs:506. The web home feed uses a third route, POST /react (src/hooks/useFeedReaction.ts:38, Postgres toggle); Android uses POST/DELETE /reactions.
How to reproduce: miniredis: ToggleLike, FastForward(25h), ToggleLike; expect IsSet=false and count 0, observe IsSet=true and count 2.
Impact:        Like counts on the Redis counter can drift upward and a double tap from two tabs can cancel itself. Whether the reconcile job repairs the count was not checked.
Fix:           Decide from the durable record (or drop the TTL and seed the key from Scylla on miss); give clients an explicit set/unset. Test as in "How to reproduce".
Acceptance:    The test above passes; 50 concurrent "like" intents leave count +1.
Effort:        M
Verification:  UPGRADED from suspected to VERIFIED-READ by the independent check of the like path. `POST /v1/posts/:id/like`: the Redis membership key `liked:<uid>:<pid>` expires after 24 h (`post-service/internal/engagement/lua.go:63`), so the next tap by a user who already liked counts them again in Redis, in `engagement_counters` (`consumers/scylla_like.go:100-105`, no per-user dedupe) and in the PostgreSQL `like_count` that feeds popular sort. Reproduce: like through `/like`, wait more than 24 h, tap twice; the user ends unliked and those counters stay +1, repeatable daily. The same happens when a user likes through `/reactions` and then taps `/like`. The Scylla calls after the toggle are unordered (`post_engagement.go:155-168`), so with two or more concurrent taps the feed's `viewer_reaction` can end opposite to Redis. `POST /reactions` (Android) holds: `INSERT ... IF NOT EXISTS` gates the increment (`store/scylla/interactions.go:42-69`).
```

```
ID:            feed-50 (track ID F-4)
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
ID:            feed-51 (track ID F-5)
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
ID:            feed-52 (track ID F-6)
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
ID:            feed-53 (track ID F-7)
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
ID:            feed-54 (track ID F-8)
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
ID:            feed-55 (track ID F-9)
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
ID:            feed-56 (track ID F-10)
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
ID:            feed-57 (track ID F-11)
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
ID:            feed-58 (track ID F-12)
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
ID:            feed-59 (track ID F-15)
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
ID:            feed-60 (track ID V-1)
Title:         The follow backfill copies the followee's author timeline, which includes private and unlisted posts, into the new follower's home timeline without a visibility filter.
Severity:      P2
Category:      privacy
Evidence level: VERIFIED-READ
Evidence:      feed-service/internal/events/consumer.go:270-283 (backfill from the author timeline); feed-service/internal/service/feed.go:1145 (the author timeline is always written, whatever the visibility). The celeb pull also reads author timelines. The row is NOT served: feed-service/internal/service/listingvisibility.go:60-68 and post-service/internal/service/batch_visibility.go:34-46 drop it on every read path.
How to reproduce: A creates a private post. B follows A. B's partition in social_feed.home_timeline_by_user now holds a row for A's private post. GET /v1/feed/home for B does not return it.
Impact:        No exposure today. The post is protected by one layer (read-time filtering) instead of two, and a row that will never render takes a slot in the page window, which makes the short-page defect (track ID A-2) more likely.
Fix:           In the follow backfill, skip rows whose post is not public or followers-visible (apply the same rule FanoutPost uses) before AddToHomeTimeline.
Acceptance:    A consumer test: the author timeline holds one public and one private post; after handleUserFollowed the follower's home timeline holds only the public one. Fails now.
Effort:        S
Source:        found by the Stage 3 verifier while trying to break the "visibility at fan-out" claim.
```

## P3

```
ID:            feed-61 (track ID A-8)
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
ID:            feed-62 (track ID A-9)
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

```
ID:            feed-63 (track ID B-8)
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
ID:            feed-64 (track ID B-10)
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

```
ID:            feed-65 (track ID C-8)
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
ID:            feed-66 (track ID C-9)
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
ID:            feed-67 (track ID C-10)
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
ID:            feed-68 (track ID C-11)
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

```
ID:            feed-69 (track ID D-7)
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
ID:            feed-70 (track ID D-13)
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
ID:            feed-71 (track ID D-14)
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
ID:            feed-72 (track ID D-15)
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
ID:            feed-73 (track ID D-16)
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
ID:            feed-74 (track ID D-17)
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

```
ID:            feed-75 (track ID E-16)
Title:         Boot-time migrations for feed, post and graph run without the advisory lock the shared runner already offers.
Severity:      P3
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      feed-service/internal/store/postgres/bootstrap.go:26, post-service/internal/store/postgres/bootstrap.go:31, graph-service/internal/store/bootstrap.go:31 call migrationrunner.Run. Architecture/shared/store/migrationrunner/exclusive.go:49 (RunExclusive, pg_advisory_lock) exists and is not used by these three. Run (runner.go:44-113) reads the applied set, then applies each missing file in its own transaction.
How to reproduce: Start two fresh replicas against an empty database at the same moment.
Impact:        On a first deploy or a scale-up during a release, two pods can apply the same migration concurrently; one fails and exits (main.go os.Exit(1)) and restarts. It converges, through a crash.
Fix:           Call RunExclusive in the three bootstrap files.
Acceptance:    The existing exclusive_integration_test pattern passes for the three services.
Effort:        S
```

```
ID:            feed-76 (track ID E-17)
Title:         Builds are vendored but not fully pinned, and the working tree being tested differs from the committed tree in go.work.sum and 572 vendor files.
Severity:      P3
Category:      deployment
Evidence level: VERIFIED-RUN
Evidence:      feed-service/Dockerfile:1 golang:1.26.10-alpine and :24 alpine:3.19 are tags, not digests; :3 `apk add --no-cache build-base` is unpinned; :20,22 build with -mod=vendor from the committed Architecture/vendor (4,023 tracked files). post, graph and gateway Dockerfiles use the same bases. The image sets no USER; the chart forces uid 1000 (charts/atpost-service/templates/deployment.yaml:162-164).
               Web: C:\workspace\wt\pb-echo\Dockerfile:4,9 `oven/bun:1` (floating major), :63 node:20-alpine; `bun install --frozen-lockfile` (line 7).
               `git diff --stat -- Architecture/go.work.sum`: 34 insertions, uncommitted. `git status --short Architecture/vendor`: 572 files modified (git prints LF/CRLF warnings for them; whether the change is only line endings was not established). vendor/modules.txt is not among them.
How to reproduce: Run the two git commands.
Impact:        Two builds of the same commit weeks apart can differ in base image and compiler toolchain packages. Local test results on this machine are for a tree CI has not seen.
Fix:           Pin the three base images by digest. Commit or discard the go.work.sum and vendor changes before the launch commit is cut (run `go work vendor` and check the diff is empty).
Acceptance:    `git status --short Architecture/vendor Architecture/go.work.sum` is empty at the launch commit; Dockerfiles reference @sha256 digests.
Effort:        S
```

```
ID:            feed-77 (track ID E-18)
Title:         The staging values files are stale and disagree with prod and QA, and promote.yml takes its image tags from them.
Severity:      P3
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      deploy/README.md:16 "staging is not applied anywhere". deploy/services/feed-service/values-staging.yaml:14 `tag: latest` (ECR is immutable and CI pushes SHAs, build-push.yml:221-224); ENV: staging (line 48); no JWT_*_PREVIOUS keys (lines 44-45 against values-prod.yaml:46-49); no prometheusRule, HPA or PDB block.
               post-service treats only ENV=prod or APP_ENV=production as production (post-service/cmd/server/main.go:59-63), so at ENV=staging it would start without INTERNAL_SERVICE_KEY; feed-service refuses at staging (feed-service/cmd/server/main.go:322-326).
               .github/workflows/promote.yml:12-14 copies image.tag from values-staging.yaml into values-qa.yaml or values-prod.yaml; until build-push succeeds once that tag is "latest".
               feed-service mounts JWT_SECRET, JWT_KID and the previous pair (values-prod.yaml:46-49) but does not read them: its env reads (grep over non-test Go files) contain no JWT variable.
How to reproduce: Read the files above.
Impact:        A first promotion would write "latest" into QA or prod values. An HMAC signing secret is mounted into a service that has no use for it.
Fix:           Make promote.yml refuse a tag that is not a 40-character SHA. Remove the four JWT keys from feed-service's externalSecret in all three files. Either delete the staging files or state in each header that only image.tag is live.
Acceptance:    promote.yml fails on "latest"; render_test.sh still passes.
Effort:        S
```

```
ID:            feed-78 (track ID F-13)
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
ID:            feed-79 (track ID F-14)
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
ID:            feed-80 (track ID F-16)
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

