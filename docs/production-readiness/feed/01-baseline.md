# Feed: Stage 1 baseline (build, tests, scans)

Run on 2026-11-16 on Windows 11, Go 1.26.5, JDK 21, from the working tree of `codex/module-01-02-launch-safety` @ `3c860874e` (728 uncommitted paths in the tree; in the feed's services only `api-gateway/cmd/server/main.go`, one gateway test and the `post-service` deploy values are modified).

## 1. Commands run here

| # | Command | Exit | Result | Level |
|---|---|---|---|---|
| 1 | `scripts/verify/go.sh --full Architecture/services/feed-service Architecture/services/post-service Architecture/services/graph-service Architecture/services/api-gateway` | 0 | `RESULT PASS`. Per service: build, `go vet`, `go vet -tags integration`, `go test` all `PASS`. Packages reporting `ok`: feed-service 12, post-service 21, graph-service 6, api-gateway 12. No `FAIL` line. | VERIFIED-RUN |
| 2 | `scripts/verify/android.sh --fast :feature:feed :core:feed :core:engagement` | 0 | `RESULT PASS`. Unit tests: `:feature:feed` 261, `:core:feed` 689, `:core:engagement` 148, 0 failures. | VERIFIED-RUN |
| 3 | Web feed unit tests in `C:\workspace\wt\pb-echo` | see `tracks/track-f-tests.md` | Run by Track F. | see track |
| 4 | `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` (feed-service) | not run | Blocked by this session's permission policy (downloading and running a tool). | UNVERIFIED |
| 5 | `gitleaks`, `gosec`, `staticcheck`, `semgrep`, `trivy` | not run | None is installed on this machine (`which` finds none). | UNVERIFIED |

What the unit run does **not** cover: every test behind `-tags integration` (they need a database and run only in CI), the Android instrumented tests, any device or browser run, and any load test.

## 2. CI results read with `gh` (read-only)

| Workflow | Latest run on this branch | Result |
|---|---|---|
| `integration-all.yml` | 2026-11-16 11:50 UTC, `3c860874e` (the current head), manual run | success. Jobs for `post-service` and `graph-service` succeeded. **No job named for `feed-service` appears in the run**; Track F checks whether feed-service has integration tests in CI at all. |
| `integration-all.yml` | the two push runs before it (`16767c70c`, `2f5679693`) | failure, then green on the next commit |
| `go-ci.yml` | 2026-10-31, `44f00fafe` | success. **158 commits older than the head.** |
| `lint.yml` | 2026-10-31, `44f00fafe` | success. 158 commits old. |
| `security.yml` (gitleaks, govulncheck, Trivy) | 2026-10-31, `44f00fafe` | success. 158 commits old; three `go.mod` lines changed since. |
| `android-native.yml` | 2026-10-29, on a different branch (`...-9erjug`) | failure (last four runs all failed). No run on this branch. |

`security.yml` on `main` (weekly schedule, latest 2026-11-15, commit `adad00644`): **all seven jobs fail.**

- Trivy: 23 findings (2 critical, 21 high). The critical one is `CVE-2026-33815` in `github.com/jackc/pgx/v5` at v5.5.5.
- govulncheck fails for `Architecture`, `chat-service` and three identity services.
- gitleaks: 148 hits. Most are browser profile files under `.tmp/` and test fixtures. Three hits are rule `private-key` on `.tmp/secure-smoke/certs/{redis,ca,redpanda}.key` (commit `fc24b4bd`), and three are rule `aws-access-token` in `scripts/smoke/dev-smoke.test.mjs:80,85`. Values were not opened or printed here.

`main` is 1,058 commits behind this branch, so those results describe old code. On this branch `pgx` is v5.10.0 and `klauspost/compress` v1.19.2 (`Architecture/services/feed-service/go.mod:11,40`), which are above the fixed versions Trivy names, and `git ls-files .tmp Architecture/docker/.tmp` returns nothing. But the private keys and tokens gitleaks reports are in **git history**, and no scan has run on the current head.

## 3. What this baseline establishes

- The four services compile and their unit tests pass on the code as it stands. VERIFIED-RUN.
- The Android feed modules' unit tests pass. VERIFIED-RUN.
- Integration tests for post-service and graph-service passed in CI on the current head. VERIFIED-RUN (CI).
- Dependency, secret and static security scans on the current head: **UNVERIFIED**. The last passing scan is 158 commits old. To clear: run the `Security` workflow by hand on this branch (Actions → Security → Run workflow → `codex/module-01-02-launch-safety`).
- Whether the key files and token-shaped strings in git history are real credentials: **UNVERIFIED**. A person has to look at them and rotate anything real.
