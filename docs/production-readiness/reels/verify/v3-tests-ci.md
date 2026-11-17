# Verifier 3: tests and CI findings (reels)

Independent, read-only check on 2026-11-17 at HEAD 58f602b6d (branch codex/module-01-02-launch-safety). I read only each finding's Title and Evidence and then looked for anything in the repository, the web worktree (C:\workspace\wt\pb-echo) or CI history that would make the claim false.

## D-4: no load test for reels

- Verdict: CONFIRMED
- Evidence level: VERIFIED-RUN (repository search + file listing)
- Checked:
  - `git ls-files | grep -iE "k6|vegeta|locust|gatling|loadtest|load-test|artillery|wrk|ghz|bench"`: outside vendor/, the only hits are dating-service/loadtest/*.js (3), rider-service/loadtest/*.js (3) and Android RefreshUnderLoadTest.kt. Nothing for media, feed, post or reels.
  - `grep -rln "func Benchmark"` in media-service, feed-service, post-service: no hits.
  - Docs search for k6/vegeta/locust/"load test" outside this audit folder: only dating and rider files. No results file exists anywhere.
  - Android CI has a "Macrobenchmark (Phase 4)" job, but it was skipped in run 40891364208, and no macrobenchmark or baselineprofile module is tracked under mobile/android. It would measure client frame timing anyway, not server load.
- Caveat: the claim that no load test "has been run" can only be shown by the absence of scripts and results in the repository. A test run off-repo would not show up.
- Severity: P1 stands.

## F-1: no upload, transcode, publish, then play on a second account test

- Verdict: CONFIRMED
- Evidence level: VERIFIED-RUN (repository search)
- Checked:
  - Architecture/tools/integration has 27 files. Only two media tests exist: media_e2e_test.go:110 TestE2E_Media_UploadConfirm and :149 TestE2E_Media_ConfirmRefusesMistypedUpload. The comment at :100-107 says the flow uses an image on purpose and that a finished transcode "belongs in a media-specific job". No such job exists.
  - The other "flick" hits in tools/integration are a seeded rate row in creator_fund_test.go:58 and an edge-404 enqueue body in reviewer_e2e_test.go:141. Neither uploads or plays a video.
  - .github/workflows/e2e.yml:58 starts only "api-gateway media-service" for the media domain. There is no media-worker and no Kafka. .github/e2e/docker-compose.e2e.yml has no media-worker, ffmpeg or Kafka entry.
  - scripts/smoke/dev-smoke.mjs exists. A grep of all workflows for "smoke" hits only a comment in terraform.yml:36, so the smoke script is run by hand only.
  - mobile/android tracks no androidTest sources. In the web repo, the only upload test is reelDurationCap.test.ts.
- Severity: P1 stands.

## F-3: every real-ffmpeg test skips, and no CI installs ffmpeg

- Verdict: CONFIRMED
- Evidence level: VERIFIED-RUN (workflow grep + CI log)
- Checked:
  - processing/hls_pipeline_test.go:39-45 skips under -short and when `exec.LookPath("ffmpeg")` fails. reels_hardening_test.go:105 has the same LookPath skip.
  - The only workflows that mention ffmpeg are build-push.yml:231 and build-push-acr.yml, which build the worker image. No workflow installs ffmpeg. The only apt-get installs are of postgresql-client (integration-all.yml:136, integration-postgres.yml:67, integration-opensearch.yml:243, module-05:153/223, module-06:89). No workflow uses a setup-ffmpeg action.
  - The media-service unit tests do run in CI, in two places: module-05-messaging-safety.yml:51-52 (`go test ./...`, no -short) and integration-all (go.sh --full --db). So the ffmpeg tests do reach CI. Whether they skip there depends on the runner image (ubuntu-26.04, version 20261111.82.6 in run 44008075845), and nothing in the logs records it.
  - The job log for run 44008075845 (media-service) shows only "PASS test media-service (12s)" and "PASS integration media-service (18s)", with no -v output. Twelve seconds for the whole module points to skips but does not prove them.
- Correction: the claim "no proof exists" holds. It is not proven that the tests skip in CI, because a preinstalled ffmpeg on the runner image has not been ruled out. The finding's core claim (there is no evidence a real reel transcodes in CI) stands.
- Severity: P1 stands.

## F-5: the server half of resumable upload is untested, and the web cannot resume after a reload

- Verdict: CONFIRMED
- Evidence level: VERIFIED-RUN (repository search)
- Checked:
  - `grep -rnE "\.(UploadPart|CompleteResumableUpload|GetResumableUploadStatus)\("` over every *_test.go in Architecture/services: no hits.
  - Every test file that mentions resumable upload covers init only: document_test.go:190-196, video_original_cap_test.go:23-38, resumable_ratelimit_test.go, resumable_chunk_plan_test.go, upload_size_handler_test.go:21,74 (the /resumable/init route only) and anonymous_routes_test.go:63 (route registration). edgelimits_test.go:97 only names the chunk path in an edge size-limit table. Nothing in tools/integration covers it either.
  - Web: pb-echo src/lib/resumableUpload.ts calls only init (:54), chunk (:58) and complete (:65). There is no status call and no localStorage, sessionStorage or IndexedDB use. Its tests cover slicing, retry, backoff and a 403 refusal, but there is no resume case.
- Severity: P1 stands.

## F-7: no single-active-player, bounded-pool or no-audio-overlap test

- Verdict: CONFIRMED
- Evidence level: VERIFIED-RUN (repository search)
- Checked:
  - Android: the only test files that mention PlayerPool are PlayerPoolPlaceTest.kt (3 tests), PlayerPoolRelocationTest.kt (5) and ui/VideoLoadingTest.kt. VideoLoadingTest.kt only mentions PlayerPool.preload in a comment (:51) and tests the buffering-indicator rule.
  - HlsRefreshTest.kt uses FakeExoPlayer("reel") but tests re-prepare after a 403 or an expired playlist, not exclusivity. The ReelsViewModelTest play/pause tests (:623, :637, :680) cover pause and mute state, not player count.
  - No androidTest sources are tracked.
  - Web: the reels tests that load player code use readFileSync on ReelsScreen.tsx and reels-screen.css (presentation.test.tsx:21,59,76; stage.test.ts:23). No test mounts ReelVideo, and nothing asserts that only one player is active.
- The track's own caveat (added-sound overlap is covered by ReelSoundPlayerTest) is consistent with what I found.
- Severity: P1 stands.

## F-11: CI not run at head on Go, Android and web

- Verdict: CONFIRMED for Android and web, with a correction on Go
- Evidence level: VERIFIED-RUN (gh run list / gh run view)
- Checked:
  - Go CI: `gh run list --workflow go-ci.yml` returns 41168872400 at 44f00fafe (31 Oct), success. `git rev-list --count 44f00fafe..HEAD` returns 177. On main, the last two Go CI runs (Aug) failed. The workflow triggers only on pull requests, pushes to main and manual dispatch (go-ci.yml:19-35), so it never runs on pushes to this branch.
  - Go correction: integration-all run 44008075845 at 770f5ed9d (HEAD~1; HEAD only changes docs/production-readiness/feed/05-fix-plan.md) ran `go.sh --full --db` for media-service and post-service. That covers build, vet, unit and integration tests, and both passed. So the media and post reels Go tests did run at the current code. What has not run at head is feed-service's unit tests: feed-service is not in scripts/ci/integration-services.yaml, it has 6 commits since 44f00fafe, and e2e.yml only starts it as a service. The title's "Go CI is 177 commits behind" is literally true but overstates the gap. The Go-layer gap is feed-service (and any other module with no integration-manifest entry), not reels Go tests in general.
  - Android: `gh run view 40891364208` (29 Oct, branch codex/module-01-02-launch-safety-9erjug, a newer run than the track cites) shows "Assemble, test and analyse: failure" at step "Write google-services.json", with Macrobenchmark skipped. On this branch, runs 40175440840 (25 Oct) and the Aug runs failed. On main, the Aug runs also failed. No Android run has produced test results.
  - Web: pb-echo (feat/echo-web) has no .github folder, and no remote branch has .github/workflows. The package.json has no CI hook. The Dockerfile does not run tests. modernsmapp build-web.yml builds images with no test step.
- Corrected severity: P1 stands, carried by Android and web. Reword the Go part: "feed-service unit tests last ran 31 Oct; media and post reels tests run in integration-all at head".

## Summary

| ID | Verdict | Evidence level | Severity |
|---|---|---|---|
| D-4 | CONFIRMED | VERIFIED-RUN | P1 (unchanged) |
| F-1 | CONFIRMED | VERIFIED-RUN | P1 (unchanged) |
| F-3 | CONFIRMED (whether the tests skip in CI is not proven; the runner image may carry ffmpeg) | VERIFIED-RUN | P1 (unchanged) |
| F-5 | CONFIRMED | VERIFIED-RUN | P1 (unchanged) |
| F-7 | CONFIRMED | VERIFIED-RUN | P1 (unchanged) |
| F-11 | CONFIRMED for Android and web; Go part narrowed (media and post ran green in integration-all at HEAD~1; only feed-service unit tests are stale) | VERIFIED-RUN | P1 (unchanged), title reworded |
