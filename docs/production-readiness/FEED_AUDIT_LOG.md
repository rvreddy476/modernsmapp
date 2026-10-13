# Feed production-readiness audit — command log

Audit date: 2026-10-13. Auditor: cloud agent (Feed). Read-only audit: no application code, schema, config or test was changed.

Repositories / revisions audited:

| Repo | Branch | Commit |
|---|---|---|
| `rvreddy476/modernsmapp` | `codex/module-01-02-launch-safety` (report branch `audit/feed-production-readiness` created from it) | `d015cab3` |
| `rvreddy476/postbook-ui` | `codex/apps-ui-refresh` (checked out locally as `audit-ref`, read-only) | `eb10bd3` |

Environment: Go 1.25.6 (toolchain auto-download), Bun 1.4.2, Java present, **no Android SDK** (`ANDROID_HOME` empty), **no Docker daemon** (`/var/run/docker.sock` missing), no AWS credentials (expected). Go builds used `GOFLAGS=-mod=vendor` from the Go workspace `Architecture/go.work`.

Long outputs were saved to the session scratchpad, not to the repo.

## Setup

| # | Command | Exit | Result |
|---|---|---|---|
| 1 | `git -C modernsmapp fetch origin codex/module-01-02-launch-safety` | 0 | fetched |
| 2 | `git -C postbook-ui fetch origin codex/apps-ui-refresh` | 0 | fetched (new remote branch) |
| 3 | `git -C modernsmapp checkout -B audit/feed-production-readiness FETCH_HEAD` | 0 | HEAD `d015cab3` |
| 4 | `git -C postbook-ui checkout -B audit-ref origin/codex/apps-ui-refresh` | 0 | HEAD `eb10bd3` |
| 5 | `which go java bun docker psql redis-server; go version` | 0 | all binaries on PATH; go1.25.6 |

## Backend checks (Go)

| # | Command (cwd) | Exit | Result |
|---|---|---|---|
| 6 | `go build ./...` (`Architecture/services/feed-service`) | 0 | builds clean |
| 7 | `go vet ./...` (feed-service) | 0 | no findings |
| 8 | `go test -count=1 ./...` (feed-service) | 0 | 7 packages ok; `cmd/*`, `database`, `internal/consumers`, `internal/store/postgres` have **no test files** |
| 9 | `go test -count=1 -json ./...` (feed-service), counted top-level tests | 0 | **236 pass, 0 fail, 0 skip** |
| 10 | `go test -race -count=1 ./...` (feed-service) — same flags as `.github/workflows/go-ci.yml:39` | **1** | **FAIL** `internal/service`: `WARNING: DATA RACE` in test fixture `internal/service/related_test.go:76` (`f.calls = append(...)` from concurrent httptest handler goroutines); failing test `TestRelated_PagingConsumesTheOrderingExactly`. All other packages ok |
| 11 | `go test -race -count=1 ./internal/service/` (feed-service) | 1 | reproduced: 1 data race, `--- FAIL: TestRelated_PagingConsumesTheOrderingExactly` |
| 12 | `go test -race -count=5 -run 'TestRelated_' ./internal/service/` | 1 | reproduced again; this time `--- FAIL: TestRelated_ReelSeedReturnsReels` (race in the same fixture) |
| 13 | `go test -race -count=1 ./internal/{events,http,pipeline,purge,ranking}/ ./internal/store/scylla/` | 0 | all 6 packages ok under `-race` |
| 14 | `go build ./... && go vet ./...` (`post-service`) | 0 / 0 | clean |
| 15 | `go test -count=1 -json ./...` (post-service) | 0 | **615 pass, 0 fail, 15 skip** (skips are integration-gated) |
| 16 | `go build/vet ./...` (`graph-service`) | 0 / 0 | clean |
| 17 | `go test -count=1 -json ./...` (graph-service) | 0 | **41 pass, 0 fail** |
| 18 | `go build/vet ./...` (`analytics-service`) | 0 / 0 | clean |
| 19 | `go test -count=1 -json ./...` (analytics-service) | 0 | **67 pass, 0 fail** |
| 20 | `go build/vet ./...` (`search-service`) | 0 / 0 | clean |
| 21 | `go test -count=1 -json ./...` (search-service) | 0 | **138 pass, 0 fail** |
| 22 | `timeout 20 docker ps` | 1 (daemon) | `dial unix /var/run/docker.sock: no such file` → integration tests **not run** |

## Library behaviour verified from vendored source

| # | Command | Exit | Result |
|---|---|---|---|
| 23 | `grep -n -A12 'func (r \*Reader) ReadMessage' vendor/github.com/segmentio/kafka-go/reader.go` (kafka-go v0.4.50) | 0 | `ReadMessage` = `FetchMessage` then `CommitMessages` **before returning** when a GroupID is set (reader.go:792-804) → offset is committed before the feed handler runs |
| 24 | `grep -n -A5 'func UUIDFromTime' vendor/github.com/gocql/gocql/uuid.go` | 0 | `clock := atomic.AddUint32(&clockSeq, 1)` (uuid.go:199) → every call yields a different timeuuid for the same timestamp → timeline inserts are not idempotent |

## Web checks (postbook-ui, Bun only)

| # | Command | Exit | Result |
|---|---|---|---|
| 25 | `bun install` | 0 | 179 packages installed; `git status` clean afterwards |
| 26 | `bunx tsc --noEmit` | 0 | 0 type errors |
| 27 | `bunx bun test src/components/feed src/hooks/__tests__` | 0 | **47 pass, 0 fail** (9 files, 96 expects) |
| 28 | `bun run build` | 0 | Next.js "Compiled successfully in 42s", 216/216 static pages generated; only warnings (middleware→proxy deprecation, `CHAT_PROXY_SIGNING_SECRET` unset in sandbox); `git status` clean afterwards |

## Android

| # | Command | Exit | Result |
|---|---|---|---|
| 29 | `echo $ANDROID_HOME; ls ~/.gradle` | 0 | `ANDROID_HOME` empty; no SDK → `./gradlew --offline :core:feed:testDebugUnitTest` **not run (Unverified)** |

## Read-only code searches (representative; all exit 0 unless noted)

| # | Command | Result |
|---|---|---|
| 30 | `grep -rn 'event_dedup' feed-service` | only DDL (`database/setup.sql:9`, `migrations/001_initial.sql:13`) and a purge comment — **no reader/writer** |
| 31 | `grep -rn 'GetAuthorTimeline(' feed-service` | only the definition `store/scylla/timelines.go:407` — **no caller** |
| 32 | `grep -rn 'celeb_authors\|SetCelebStatus' repo (excl. vendor)` | `SetCelebStatus` defined `store/postgres/meta.go:35`, **never called**; no other writer |
| 33 | `grep -rn 'PostVisibilityChanged\|post.visibility_changed' services shared` | constant only (`shared/events/events.go:186`) — **no producer, no consumer** |
| 34 | `grep -rn 'user:affinities' services` | writers: `feed-service/internal/pipeline/affinity.go:55`, `analytics-service/internal/personalization/warmer.go:80`; reader `feed-service/internal/ranking/signals.go:188` |
| 35 | `grep -rln 'INSERT INTO user_interactions'`; `grep -rn ComputeFromImpressions` | only `pipeline/affinity.go`; `ComputeFromImpressions` has **no caller** |
| 36 | `grep -rn 'ReadMessage\|FetchMessage\|CommitMessages' feed-service` | `events/consumer.go:59` and `consumers/channel_updates.go:72` use `ReadMessage`; only `purge/purge.go:269/290` uses Fetch+Commit |
| 37 | `grep -n '"/v1/feed"' api-gateway/pkg/internalroutes/internalroutes.go` | `"/v1/feed": true` (line 280) in the key-stamp policy → gateway stamps `X-Internal-Service-Key` on every feed request |
| 38 | `grep -rn 'unlisted' feed-service` | **no matches** — feed never treats `unlisted` specially |
| 39 | `grep -n 'Visibility .*binding' post-service/internal/http/handler.go` | line 405: `oneof=public followers private unlisted` |
| 40 | `grep -rn '"trusted"' postbook-ui/src` | offered by `src/components/CreatePortal.tsx:112` and `src/app/create/post/page.tsx:48` |
| 41 | `grep -rn 'getDelta' mobile/android (main sources)` | declared `core/feed/.../FeedApi.kt:64`, **no caller** |
| 42 | `grep -rln -i pulltorefresh mobile/android` | only `feature/tube/...` screens; none in `feature/feed` |
| 43 | `grep -rn 'feed_mode' mobile/android core/feed feature/feed` | **no matches** — Android never sends `feed_mode` |
| 44 | secret regex scan over `feed-service`, `deploy/services/feed-service`, `docker/scylla` | no matches |
| 45 | `diff values-prod.yaml values-staging.yaml` (deploy/services/feed-service) | staging lacks POSTGRES_DSN/REDIS/KAFKA/SCYLLA_HOSTS secrets+env (see report) |
| 46 | `grep -n 'partitions\|replication_factor' infra/terraform/modules/msk/topics-job/variables.tf` | 6 partitions, RF 2, retention 7 d; `social.events.v1.dlq` exists but feed never writes it |

No command modified application code. No deploy, cloud or credential use.
