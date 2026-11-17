# Long video — Stage 1 baseline

Run on 17 Nov 2026 on branch `codex/module-01-02-launch-safety` at `7765e81a7`. The run was read-only: nothing was started against any deployed environment, and no local databases, Docker or devices were used. The two commits since the reels baseline (`58f602b6d`, `7765e81a7`) change only docs, so the Go results from that run still describe this code.

## Build, lint and unit tests

| # | Command | Exit | Result |
|---|---|---|---|
| 1 | `scripts/verify/go.sh --full` on media-service, post-service, feed-service, graph-service, api-gateway, trust-safety-service, monetization-service, analytics-service, search-service, notification-service (reels baseline, same code) | 0 | PASS: build, vet, lint and unit tests on all ten. |
| 2 | `scripts/verify/android.sh --fast :feature:tube :feature:search :core:media :core:memberships` | 0 | RESULT PASS, unit tests on 4 modules in 38 s. |
| 3 | Go unit tests per service (Track F) | 0 | • post-service: 2698 pass<br>• media-service: 1163 pass, 10 ffmpeg skips, 1 timing flake (LV-F-11)<br>• feed-service: 663 pass<br>• notification-service: 755 pass, 23 DB skips<br>• search-service: 489 pass<br>• user-service: 303 pass, 5 skips<br>• analytics-service: 216 pass |
| 4 | Android `:feature:tube` (forced re-run) and `:core:feed` (Track F) | 0 | 310 pass and 689 pass. |
| 5 | Web unit tests, posttube and upload folders in `pb-echo` (Track F) | 0 | 818 pass. |
| 6 | media `cmd/worker` routing tests (7), post-service schedule, progress and playlist tests (110) (Track C) | 0 | PASS |
| 7 | media worker, post-service schedule publisher and notification-service fan-out tests (Track E) | 0 | PASS |
| 8 | `QA_GUARDS_ONLY=1 charts/atpost-service/render_test.sh` (Track E) | 1 | FAIL, two drifts: QA sets `MEDIA_WORKER_MAX_ORIGINAL_BYTES` and prod does not (reels-62), and `ACTION_LINK_OWN_DOMAINS` (LV-E-11). The probe guard passes, which closes feed-01. |
| 9 | `migration-compat.sh --self-test`, then `--files` over the 15 long-video post-service migrations (Track E) | 0 | Self-test passes. 27 statements are flagged; all are compatible with rolling back one version at first launch. |
| 10 | A scratch program outside the repo that copies the production segment-signing code (Track D) | 0 | 2,400 RSA signatures for one 4-hour rung took 5.0 s on one laptop core and gave a 1.3 MB playlist (LV-D-1). |

## CI at head (from `gh run list` / `gh run view`)

| Workflow | Last run on the branch | State |
|---|---|---|
| Integration (integration-all.yml) | 44008075845 at `770f5ed9d` (the commits since change docs only) | Green. feed-service is not in the integration manifest. |
| E2E (e2e.yml) | 44008085438 at `770f5ed9d` | Green, but there is no long-video flow. The media flow uploads only an image, with no worker and no Kafka (LV-F-1). |
| Go CI / Lint | 31 Oct, 177+ commits back | Stale. |
| Security (weekly, on main) | failing since 30 Aug | FAIL |
| Android CI | 29 Oct | FAIL at "Write google-services.json", before Gradle starts. |
| Web (`pb-echo`) | none | No CI on any branch. |
| dev-smoke | manual only | Only GETs trending for long video. |

## Security tooling

| Scan | State |
|---|---|
| Secret scanner (gitleaks / trufflehog) | UNVERIFIED: not installed. A grep-based check is in `verify/v3-gapfill-pass.md`. |
| Go dependency scan (govulncheck) | UNVERIFIED: not installed locally, and the CI Security workflow is failing. |
| Android and web dependency scans | UNVERIFIED: none configured or run. |
| Static analyser (gosec / semgrep) | UNVERIFIED: not installed. |
| Container / IaC scan (trivy / checkov / tfsec) | UNVERIFIED: not installed. |
| ffmpeg / ffprobe | Not installed, so the 10 real-transcode tests skip locally (reels-12). |

## Not run, and why

- **Real encodes:** no encode of a long file was timed. There is no ffmpeg here and no production-sized hardware.
- **Query plans:** no `EXPLAIN` plans, because the audit uses no database.
- **Journeys:** none was executed end to end. `02-feature-status.md` walks them in code.
