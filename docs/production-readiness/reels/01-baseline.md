# Reels — Stage 1 baseline

Run on 16–17 Nov 2026 on branch `codex/module-01-02-launch-safety`, at `58f602b6d` and the commits just before it. The run was read-only: nothing was started against any deployed environment, and no local databases, Docker or devices were used.

## Build, lint and unit tests

| # | Command | Exit | Result |
|---|---|---|---|
| 1 | `scripts/verify/go.sh --full` on each of media-service, post-service, feed-service, graph-service, api-gateway, trust-safety-service, monetization-service, analytics-service, search-service, notification-service | 0 | PASS: build, vet, lint and unit tests on all ten. Log kept in the session scratchpad (`reels_baseline_go.log`). |
| 2 | `scripts/verify/android.sh --fast :feature:feed :feature:post :core:media :core:feed :core:engagement` | 0 | RESULT PASS, 0 failures. Tests per module: feature/feed 261, feature/post 390, core/media 285, core/feed 689, core/engagement 148. |
| 3 | `go test ./...` in media-service (Track F) | 0 | 1164 pass, 10 skip. All 10 skips are the real-ffmpeg tests; ffmpeg is not installed on this machine. |
| 4 | media-service processing tests (Track D) | 0 | 18 pass, 2 skip (ffmpeg missing). |
| 5 | Shorts 10-minute cap unit tests in post-service (Track A) | 0 | PASS |
| 6 | Media access-gate unit tests (Track B), two runs | 0 | PASS |
| 7 | Worker, sweeper and post-service consumer unit tests (Track C) | 0 | PASS |
| 8 | `QA_GUARDS_ONLY=1` chart static guards (Track E) | ≠0 | FAIL on one drift: QA sets `MEDIA_WORKER_MAX_ORIGINAL_BYTES` and prod does not (reels-E-11). |
| 9 | `migration-compat.sh` self-test, plus post-service 080–085 and media-service 030–032 (Track E) | 0 | PASS: all are compatible with rolling the app back one version. |
| 10 | `go test -list` in the post-service consumers package (verifier v1) | 0 | No behaviour test for `finalizeReviewGate`. |

## CI at head (from `gh run list` / `gh run view`)

| Workflow | Last run on the branch | State |
|---|---|---|
| Integration (integration-all.yml) | 44008075845 at `770f5ed9d` (HEAD~1; HEAD changes docs only) | Green. Covers media-service and post-service build, vet, unit and integration tests. |
| E2E (e2e.yml) | 44008085438 at `770f5ed9d` | Green, but the media flow uploads only an image. No video, no worker, no Kafka (F-1). |
| Go CI / Lint / Security | `44f00fafe`, 31 Oct, 177 commits back | Stale. feed-service has 6 commits since its last unit-test run in CI. |
| Android CI | 40891364208, 29 Oct | FAIL at "Write google-services.json": the `GOOGLE_SERVICES_JSON` secret is empty. No Android test has run in CI since August. |
| build-web | none | The web repo (`pb-echo`, at `ca96921`, clean) has no CI on any branch. |
| build-push | 3 Oct | FAIL |

## Security tooling

| Scan | State |
|---|---|
| Secret scanner (gitleaks / trufflehog) | UNVERIFIED: not installed. A grep-based check was done instead (see `verify/v4-gapfill-pass.md`). |
| Go dependency scan (govulncheck) | UNVERIFIED locally: not installed. The CI Security workflow is 177 commits stale. |
| Android / Gradle dependency scan | UNVERIFIED: no scanner configured. |
| Web (`bun audit` / npm audit) | UNVERIFIED: not run, and the web repo has no CI. |
| Static analyser (gosec / semgrep) | UNVERIFIED: not installed. |
| Container / IaC scan (trivy / checkov / tfsec) | UNVERIFIED: not installed. |
| ffprobe / ffmpeg | Not installed, so the real-transcode tests skip locally (F-3). |
