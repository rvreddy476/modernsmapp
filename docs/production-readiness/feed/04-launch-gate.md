# Feed: launch gate

Audit date 2026-11-16. Code: `codex/module-01-02-launch-safety` @ `3c860874e` (working tree); web `feat/echo-web` @ `ca96921`.

## Verdict: NO-GO

Lines 1, 2, 3 and 8 fail. Any `FAIL` on lines 1 to 8 means `NO-GO`.

| # | Gate | Result | Evidence |
|---|---|---|---|
| 1 | Every core user journey works end to end | **FAIL** | No journey was executed end to end, by this audit or by CI: the one end-to-end test polls a route that does not exist and skips itself (feed-13). By reading: a web like is undone by a refetch within 5 minutes (feed-04); a Following page can come back empty with no way forward on web (feed-05); the like count over-counts on `POST /like` after 24 h (feed-49). See `02-feature-status.md`. |
| 2 | No open P0 findings | **FAIL** | feed-01 (liveness probe path not served by feed, post and graph services), feed-02 (no production or QA environment; CI has never built an image of this code). Both confirmed in Stage 3. |
| 3 | Every route has an explicit authentication decision, and every object-ID route has an authorization check, tested | **FAIL** | Authentication decisions: present for every in-scope route (route table in `tracks/track-b-security.md`; gateway identity and internal-route blocking re-checked in Stage 3 and hold). Authorization: share and repost have no read check (feed-03, confirmed). "Tested": no request was made as user A against user B's resource; only owner-only delete has an integration test. |
| 4 | No secrets in the repository, client bundle or logs | **UNVERIFIED** | No scanner could be run here. Manual read of the feed services found one default local DSN credential (`feed-service/cmd/backfill/main.go:97`, P3 note in Track B). gitleaks on `main` reports private-key files and AWS-token-shaped strings in git history (`01-baseline.md` §2); nobody has confirmed whether they are real. |
| 5 | Dependency and static security scans have no unaddressed critical or high results | **UNVERIFIED** | Last passing `Security` run on this branch is `44f00fafe` (31 Oct), 158 commits behind the head. On `main` all seven scan jobs fail (old code). No scan exists for the current head. |
| 6 | Upload and media paths meet section 4.4 | **UNVERIFIED** | The feed does not upload; it displays post media. Whether a media URL enforces the post's audience was confirmed only at the call site to post-service, not end to end. The upload path belongs to the reels and long-video runs. |
| 7 | Money paths meet the commerce checks | **NOT APPLICABLE** | The home feed handles no payment. |
| 8 | Retried and concurrent writes are safe for the module's core actions | **FAIL** | Holds: `POST /reactions` (`IF NOT EXISTS`), deterministic timeline keys, consumer commit-after-effect, follow and block. Fails: `POST /like` re-counts a user after the 24 h key expiry and can leave Scylla opposite to Redis (feed-49); `POST /react` reads without a lock (feed-30); the post-service outbox re-publishes with a new event id, no ordering key and no row claim (feed-07); a retried visibility change loses rows (feed-28). |
| 9 | The build, tests and migrations pass in CI, from a clean state | **FAIL** | Unit build and tests pass locally (`01-baseline.md`), and integration passes in CI at the head for post-service and graph-service. But Go CI and Lint last ran 158 commits ago, `build-push` has never succeeded, feed-service and api-gateway have no integration tests (feed-47, feed-50), Android CI has no run on this branch, web tests are in no CI job (feed-57), and migrations were not run from empty. |
| 10 | Logs, metrics and alerts exist for the module's core flows | **FAIL** | Structured logs and Prometheus metrics exist. Alert rules exist for feed-service production values only, and no receiver routes a service alert to a person (feed-10). Request IDs are not forwarded on feed-service's outbound calls and tracing never starts (feed-46). |
| 11 | A rollback path exists and backups can be restored | **FAIL** | No rollback procedure, migrations forward-only at boot (feed-11). ScyllaDB has no backup; no restore has been tested for any store (feed-12). |
| 12 | Production configuration has debug, test accounts and permissive settings off | **UNVERIFIED** | No production environment exists to inspect (feed-02). In code: production refuses HS256 tokens and refuses to boot without issuer, audience, public key and (feed-service) the internal key; internal routes are blocked at the gateway. Open items: `FEED_INDEX_LEGACY_READ` is `"true"` everywhere (feed-48); a wildcard CORS origin is only warned about (feed-64); gateway `/metrics` may be on the public listener (feed-63, suspected); staging accepts HS256 and post/graph run without the internal key outside production. |

## Items checked and found to hold (re-checked in Stage 3)

These were reported sound by a track and then attacked by a separate reviewer. They are not gate lines on their own; they are what the gate lines above could rely on.

| Item | Result | Caveat |
|---|---|---|
| Gateway verifies the token and strips client identity headers before any token read | Holds | `X-User-Role` and `X-Service-Authorization` are not in the strip list; nothing in feed, post or graph trusts them today. |
| Internal routes are unreachable through the gateway | Holds | Tested by the verifier against encoded, case, backslash and `;param` variants (gateway unit tests ran green). |
| feed-service fails closed without its internal key | Holds in production, staging and QA | post-service and graph-service only enforce it in production. |
| Home-feed privacy filters fail closed when graph-service is down | Holds | Delta count cached 10 s, following list 5 min; neither serves an unfiltered page. |
| Hydration cache is keyed per viewer | Holds | A cached followers-only card is not re-checked after an unfollow: up to 300 s. |
| Block removes content immediately and symmetrically | Holds | 10 s on the "new posts" count. |
| Deleted posts cannot hydrate fresh | Holds | A cached card survives up to 300 s if the delete event is missed. |
| Keyset cursor in ranked and chronological modes | Holds | Discovery items and a failed celeb pull are outside the keyset. |
| SQL and CQL are parameterised | Holds | |
| Like through `POST /reactions` cannot double-count | Holds | `POST /like` does not (feed-49). |

## What it takes to change the verdict

To reach `GO WITH CONDITIONS`, every `FAIL` on lines 1 to 8 must clear:

1. **Line 2:** fix feed-01; stand up QA and get one green image build of the launch commit (feed-02). Owner: founder for the AWS account, secrets and branches; engineering for the workflow fix.
2. **Line 3:** fix feed-03 and add the user-A-on-user-B tests for share, repost, comment, bookmark and delete.
3. **Line 8:** fix feed-07, feed-49, feed-30, feed-28 and add the concurrent like test (feed-14).
4. **Line 1:** fix feed-04 and feed-05, repair the end-to-end test so it fails when the gateway is down (feed-13), then run the six journeys on QA and record the result.

Then each `UNVERIFIED` becomes a condition with an owner:

- **Lines 4 and 5:** run the `Security` workflow on the branch (Actions → Security → Run workflow). A person reviews the key and token hits in git history and rotates anything real.
- **Line 6:** on QA, request a followers-only post's media URL as a non-follower and as a signed-out client.
- **Line 12:** review the rendered production values once the environment exists.

`GO` needs lines 9 to 11 to pass as well: CI at the head, alert routing, a rollback drill and a restore drill.
