# Feed: fix plan (P0, then P1)

Order is by what unblocks the most, with small code fixes placed so they can run in parallel with the slow environment work. Effort: S = under half a day, M = up to two days, L = more. Full detail for each item is in `03-findings.md`.

## P0

| Order | ID | What to do | Effort | Depends on | Proof it is fixed |
|---|---|---|---|---|---|
| 1 | feed-01 | Register `GET /livez` in `Architecture/shared/health` (same handler as `/healthz`). This covers every Go service on the shared package. | S | none | Unit test in `shared/health` asserts `/livez` answers 200; the chart render test asserts each service's liveness path is one its binary registers. |
| 2 | feed-02 | Get a green image build of the launch commit. Correction, 16 Nov (fix round): the `build-push.yml` file error (`split()` is not an Actions function) was already fixed on this branch on 3 Oct (`39b638a7b`); `main` still carries the broken file, and the fixed file has never run because it triggers only on push to `main` or a manual run. What is missing is the `AWS_CI_ROLE_ARN` secret (the repository has no secrets), the CI role with a trust entry for the branch that builds, and the ECR repositories. Create the `qa` and `release/prod` branches the Argo CD manifests track. Apply the designed QA environment, seed secrets, promote, sync, run smoke. No new infrastructure is needed. | L | feed-01 (or the pods restart in a loop on first deploy). Founder: AWS account, secrets, branch decision. | A green `build-push` run for feed, post, graph and gateway at the launch commit; on QA all four deployments Available for 24 h with zero restarts; smoke passes against QA. |

While item 2 is in progress, run the `Security` workflow on the branch and have a person review the private-key and token hits gitleaks reports in git history. These clear gate lines 4 and 5 and cost nothing to start.

## P1

| Order | ID | What to do | Effort | Depends on | Proof it is fixed |
|---|---|---|---|---|---|
| 3 | feed-03 | In share and repost, load the post with the viewer-aware loader (`loadPostForEngagement`) so block, follow and private-account rules apply; make `repostableVisibility` refuse `followers` posts for non-followers. | S | none | Service tests: `CreateRepost` and `SharePost` return not-found for a blocked caller and for a non-follower of a followers-only post. Fail now. |
| 4 | feed-04 | Call `invalidateFeedHydration` in `ToggleReaction` (`post-service/internal/service/post_reactions.go`, about line 213). One line. | S | none | Unit test seeds `feed:hydrate:<user>:<post>`, calls `ToggleReaction`, asserts the key is gone. Fails now. |
| 5 | feed-05 | In `GetHomeFeed`, when the filtered window is short and the raw read was full, read the next window, up to a small bound (the loop `service/category.go` already uses). On web, do not show the empty state while `hasNextPage` is true. | M | none | Service test: 40 reel rows then 5 post rows, limit 20, first page returns the 5 posts. Web test: an empty first page with a cursor requests the next page. |
| 6 | feed-06 | Set `RetryForever` on the feed consumer and mark only undecodable payloads permanent. Optionally add a replay command modelled on search-service's. | S (retry) or M (replay tool) | none | Consumer test: a handler that fails for 30 s then succeeds has its effect applied and no dead-letter write. |
| 7 | feed-07 | In the post-service outbox relay: publish with key = aggregate id and event id = outbox row id, use a key-respecting balancer, and take an advisory lock per pass as `shared/outbox/store.go:93-117` does. No schema change. | S | none | Unit test: two publishes of one row give the same event id and key = aggregate id. Fails now on both. |
| 8 | feed-14, with feed-49 | Add the like tests (miniredis test for `ToggleLike`; integration test firing 50 concurrent `React` calls, with Scylla added to the post-service CI job). Then fix `POST /like`: either route it to the idempotent `React`/`Unreact` path or stop the membership key expiring before the stored reaction does. feed-49 is P2 but belongs with this work because the test exposes it. | M | none | Both tests run in CI (not skipped) and fail when `IF NOT EXISTS` is removed; a like, a 24 h clock advance and two taps leave every counter where it started. |
| 9 | feed-13 | Point the end-to-end test at `/v1/feed/home?feed_mode=chronological`, add feed-service to its health list, and make an unreachable gateway a failure in CI, not a skip. | S | A stack for it to run against (QA from feed-02, or the CI compose stack). | `e2e (social)` is green on a pull request and goes red when `FanoutPost` is stubbed out. |
| 10 | feed-08 | Write the follow backfill and delete rows through the existing `pushChunkWith` worker pool; pipeline the Redis commands in the reaction and comment handlers. Give the shared consumer a small worker count only if the load test still shows lag. | S, then M | feed-15 to measure | Unit test: 50 backfill rows at 10 ms each finish in under 100 ms. Load test L3 meets its lag threshold. |
| 11 | feed-09 | In the web `PostCard`, use the variant and playback URLs the feed response already carries, with `/serve` as the fallback. No backend change. | M | Decision on which web branch ships. | Component test asserts the `<img>` source is the variant URL; a feed page load shows no `/v1/media/*/serve` requests for cards that carry variants. |
| 12 | feed-10 | Add an Alertmanager receiver and route to the existing observability module (the ops SNS e-mail topic already exists); add default rules to the chart for every service; copy the feed rules to QA values. | M | feed-02 | On QA, scaling graph-service to zero produces a message in the chosen channel within 10 minutes. |
| 13 | feed-12 | Enable the Scylla operator's backup task to S3, daily. Restore each store once on QA and write down the steps and the time. | M | feed-02 | A dated restore record for Aurora, ElastiCache and Scylla on QA with row counts compared. |
| 14 | feed-11 | Write the one-page rollback procedure, adopt the rule that a migration must work with the previous image, and drill it once on QA. | M | feed-02 | A dated QA drill record: deploy N, roll back to N-1, smoke passes. |
| 15 | feed-15 | Write and run the load tests specified in `tracks/track-d-performance.md` section 8 against a QA stack sized like production; commit the scripts and the results. | L | feed-02; a launch traffic forecast from the founder (the rates in the track file are a floor). | k6 scripts committed; a results file with p95, p99, error rate and consumer lag that meets the thresholds. |

Items 3 to 8 are independent code changes in different files and can be done in parallel while item 2 is under way.

## Tests to add (collected)

- `shared/health`: `/livez` answers 200; chart render test for probe paths.
- post-service: share and repost refused for a blocked caller and a non-follower; `ToggleReaction` invalidates the feed cache; outbox publishes a stable event id and key; `ToggleLike` set and unset; 50 concurrent `React` calls give one row and +1; like, 24 h, tap twice leaves counters unchanged.
- feed-service: home page refills after filtered rows; consumer survives a 30 s dependency outage without a dead-letter write; follow backfill skips private rows (feed-60); follow backfill timing.
- Cross-service: the repaired end-to-end test (post reaches the author's and a follower's feed), failing instead of skipping when the gateway is down.
- Web: empty first page with a cursor loads the next page; `PostCard` uses variant URLs; put the existing 72 feed tests in a CI job (feed-57).

## Fix round, 16 Nov 2026 (uncommitted at the time of writing)

Proven by unit tests and `scripts/verify/go.sh --full` on shared, feed-service, post-service, graph-service and api-gateway (all `RESULT PASS`). Nothing was run against a database, broker or cluster, so the audit verdict does not change until these are exercised on QA.

| ID | State | What was done | Still open |
|---|---|---|---|
| feed-01 | Fixed in code | `shared/health` registers `/livez` (plain 200, no dependency check); a test ties the chart's probe paths to the registered routes; the request logger skips `/livez`. | monetization-service and analytics-service register their own probes and still lack `/livez`. Not proven on a cluster. |
| feed-02 | Corrected, not fixed | No workflow change needed (see the correction in the P0 table). | `AWS_CI_ROLE_ARN`, CI role trust, ECR repositories, QA environment, branches: founder decisions. |
| feed-03 | Fixed in code | Share and repost load the post through `loadPostForEngagement` and the handler read gate; `repostableVisibility` is an allow-list (public, unlisted). 6 privacy mutations killed. | Founder to confirm: followers-only posts cannot be reposted by anyone (followers get 403, others 404). |
| feed-04 | Fixed in code | `ToggleReaction` invalidates the feed hydration cache. | |
| feed-06 | Fixed in code | Feed consumer retries until success (1 s to 32 s backoff); only malformed events and refused requests (400, 413, 414, 422; Postgres class 22) are dead-lettered. 12 mutations killed. | Fan-out past 10,000 followers by offset is refused by graph-service and dead-lettered (needs cursor paging). `consumers/channel_updates.go` still drops after about 15 s with no dead letter. No replay tool. The dead-letter alert no longer fires for an outage; a consumer-lag alert is needed. |
| feed-07 | Fixed in code | Outbox relay publishes with event id = row id and key = aggregate id on its own hash-balanced writer, one pass at a time under a session advisory lock, and holds back later rows of an aggregate after a failure. 9 mutations killed. | The lock was never executed against Postgres (needs CI or dev; breaks under a transaction-pooling proxy). Two events of one post in one transaction share `created_at`. Direct (non-outbox) publishes are still keyed by actor. |

## Fix round 2, 17 Nov 2026 (uncommitted at the time of writing)

Proven by unit tests, mutation runs (`scripts/harness/mutate.sh`: 59 further mutations, all killed) and `scripts/verify/go.sh --full` on every touched module. The integration tests added here compile but have not run: they run only in CI, on the next push. Nothing was rendered by Helm, planned by Terraform or run on a cluster. The audit verdict does not change until these are exercised in CI and on QA.

| ID | State | What was done | Still open |
|---|---|---|---|
| Fan-out past 10,000 followers (from feed-06) | Fixed in code | graph-service: new cursor-paged `/v1/internal/graph/{followers,following,connections}/:userId` (contract fixtures in `graph-service/testdata/contracts/internal_lists`); feed-service fan-out pages by cursor and resumes from a saved cursor. | Deploy graph-service before feed-service. The SQL is proven only by a CI integration test. A channel above 1,000,000 subscribers fails every attempt. `cmd/backfill` still pages by offset. |
| Channel-updates consumer | Fixed in code | Retries until applied (1 s to 30 s), no commit on shutdown. | No metrics on that consumer. |
| feed-05 | Fixed in code (server) | `GetHomeFeed` reads up to 5 windows when filters shorten a page. | Web empty-state half; `GetHomeFeed` itself has no end-to-end test. |
| feed-08, feed-60 | Fixed in code | Follow backfill and post deletes use a worker pool; a failed row fails the event; the backfill skips posts the follower may not see and fails closed. | Follow events now wait during a post-service outage. |
| PostUnreacted retries | Fixed in code | One Lua script: decrement only if the reactor was in the set. | PostReacted is still not idempotent on redelivery. Two-key script needs hash tags if ElastiCache moves to cluster mode. |
| feed-33 | Fixed in code | Hydration batches post-service calls in groups of 100. | |
| feed-28 | Fixed in code | A retried visibility change skips the removal when its own re-fan-out progress exists. | The remove-then-fan-out sequence has no end-to-end test (concrete Scylla store). |
| feed-07 leftovers | Fixed in code | Migration `085_outbox_seq.sql` (additive, catalogue-only) orders same-transaction rows; post-scoped direct publishes are keyed by post id on the hash writer; integration tests for the lock and a two-replica relay. | Direct events can still overtake un-relayed outbox rows (feed-29). |
| feed-14 | Tests added | Unit tests for `/like`; integration tests with real Scylla (50 concurrent likes count once); Scylla added to the post-service CI job. | Not yet run in CI. |
| feed-49 | Fixed in code | `/like` decides from the stored Scylla reaction through the conditional writes. Response body unchanged; `count` is now the card's count. | Liked-list row and Scylla row can still disagree under a like and unlike in flight together. `/like` has no handler read gate. The three routes still feed different counters. |
| feed-30 | Fixed in code | `/react` takes a per-viewer, per-post advisory lock; insert is `ON CONFLICT DO NOTHING`. | `/react` now returns 500 and rolls back when the Scylla copy fails. |
| feed-13 | Fixed in code | Test polls `/v1/feed/home`, reads `counts.likes`, seeds two real users; the whole workflow's wait step no longer counts a refused connection as healthy; an unreachable stack fails in CI. | Never run. Expect red jobs: other flows still use random user ids, and the stack may not boot in CI. |
| feed-01 (other services) | Fixed in code | `/livez` in monetization-service and analytics-service; trust-safety-service and reviewer-service no longer put the internal key in front of probes. | admin-service (same 401 defect), api-gateway (probes behind rate limiters), and the six identity and chat services (no `/livez` or `/readyz`) are not fixed. |
| feed-10 | Config written | Seven default alert rules in the chart, an e-mail receiver for QA and prod, feed consumer stall, lag and retry-age rules (`kafka_message_retry_age_seconds` is now exported by `shared/kafka`). | Unrendered and unapplied. E-mail only. |
| feed-11 | Runbook written | `docs/runbooks/service-rollback.md`, `scripts/verify/migration-compat.sh`. | Not drilled. |
| feed-12 | Config and runbooks written | Scylla Manager daily backup to S3 in Terraform; restore runbooks for Scylla, Aurora, ElastiCache. | Unplanned, unapplied, not drilled. |
| feed-48 | Runbook written | `docs/runbooks/feed-timeline-maintenance.md`. Flag untouched. | Job not run. |

Not in this round: feed-02 (environment and AWS decisions), feed-09 and the web half of feed-05 (which web branch ships is undecided), feed-15 (needs the QA environment).

## CI proof, 17 Nov 2026

- **Integration (all Go services):** run 43936139069 at `91dba85cb`, all 33 jobs green, including post-service with a real Scylla node and graph-service. The log has no per-test lines; the new tests' skip conditions (Postgres DSN, Scylla host) were not met, so they ran.
- **Latest, at `770f5ed9d`:** end-to-end run 44008085438 and integration run 44008075845, both green (probes for every service, commerce review guards).
- **End-to-end:** run 43995894510 at `c10efb1f6`, all 13 jobs green, 39 tests passed, 9 skipped, 0 failed. This is the first time the workflow has run a flow at all: before 16 Nov its wait step counted a refused connection as healthy and every flow skipped. feed-13 is closed: `TestE2E_Social_PostFollowFeedLike` follows, posts, sees the post in the follower's home feed and likes it, through the gateway.
- The 9 skips are the "open" paths of features closed for launch (money writes, memberships, groups, communities, Q&A, memories, reviewer programme). Their "closed" answers are asserted.

What the end-to-end stack does not cover: the author-standing check at publication (the stack sets `PUBLISH_REQUIRE_STANDING_CHECK=false`), a completed tip or paid membership, console sign-in with MFA, and the signed payment webhook (the stack uses the stub provider). The stack runs as `ENV=local`.

### Product findings the end-to-end runs exposed

| Finding | Evidence | Note |
|---|---|---|
| The home feed answers 502 when trust-safety-service is unreachable: it resolves the viewer's keyword filters on every request and fails closed. | feed-service log, run 43994563478: "keyword filters could not be resolved". | Missed by the audit's dependency list. **Fixed 17 Nov:** a page is now filtered with the viewer's last known keywords (process cache, then a Redis copy kept 7 days); a viewer with none known is served without the keyword filter. Counter `keyword_filter_fallback_total`, alert `FeedKeywordFilterFallback`. Blocks, mutes and feed mutes still fail the page. 7 tests, 6 mutations killed. Not run against a real outage. |
| The home feed answers 502 when the identity profile service is unreachable. | feed-service log, run 43995307297: "profile hydration failed". | Also missing from the dependency list. Defensible, but it is one more hard dependency. |
| The identity user-service answers 401 on its health route without the internal key. | Chat job start, run 43973930994. | **Fixed 17 Nov (`3d008bdbe`):** all six identity and chat services serve `/livez` and `/readyz` without credentials; admin-service registers its probes ahead of the key; the gateway answers its probes ahead of the rate limiters. The chart’s static test has no known probe gaps left. |
| Seller approval and product approval have no state guard: an admin can approve a draft shop with no KYC, and any product id. | `commerce-service/internal/store/postgres/onboarding.go:338-339`, `:582`. | **Fixed 17 Nov (`770f5ed9d`):** every admin review action is one guarded UPDATE from the states it belongs to; refusals answer 409. The flows now file a real application first. Open: admin-service drops the error body on these routes, so the console sees a bare 409. |
| `POST /v1/commerce/onboarding/start` returns the new shop with an empty status; the status route returns `draft`. | Commerce job, run 43995307297. | **Fixed 17 Nov (`770f5ed9d`).** |
| Image uploads: the magic-byte check accepts any image type whatever MIME was declared. | `media-service/internal/service/upload.go:445`. | Video does check its container. |
| `Architecture/docker/local.env` points `AI_SERVICE_URL` at port 8117 (live-service-v2); `run-local.sh` starts ai-service on 8098, a port it also gives identity-profile. | Corrected on the CI checkout only. | |
| The dating flow asserts only "not a server error", and dating sits behind the dormant-product gate (404), so its job proves nothing. | `dating_e2e_test.go`. | **Fixed 17 Nov (`3d008bdbe`):** asserts the closed-at-launch 404. |

## After the P1s

The P2 list (45 items) is in `03-findings.md`. The ones closest to the launch gate are feed-30 and feed-28 (concurrent and retried writes, gate line 8), feed-24 (account erasure leaves feed data, relevant to the DPDP Act), feed-27 (one shared internal key and no network policy), feed-33 (`/v1/feed/delta` returns 500 above 100 ids) and feed-48 (the timeline maintenance job has not been run).
