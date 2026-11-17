# Track F: reels test coverage against the user journeys

- Audit date: 2026-11-17. Read-only. Backend and Android tree `C:\workspace\modernsmapp` (branch `codex/module-01-02-launch-safety`, HEAD `58f602b6d`, dirty working tree); web tree `C:\workspace\wt\pb-echo` (`feat/echo-web`).
- Backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Android paths are relative to `mobile/android/`; `…/` is the module's `src/test/kotlin/com/us/android/` package root. Web paths are relative to the web tree.
- Evidence levels: VERIFIED-RUN (I ran it, or read the CI log), VERIFIED-READ (I opened the test body), SUSPECTED, UNVERIFIED.
- Two delegated sub-reads (Android and web inventories) died on a rate limit before returning; every Android and web claim below is from my own reads, which were narrower (test names and selected bodies), and is marked accordingly.

## Verdict for this track

Server-side unit and Postgres coverage of the reels pipeline is much better than the Stage 0 map suggested: the stall sweeper, lease, inbox, duplicate workers, quarantine, delete-during-processing, the delivery gate (private, pending review, soft-deleted, scheduled, anonymous) and view replay/dedup all have tests whose bodies would fail if the rule broke. What is missing is the joins: **no test anywhere uploads a video, transcodes it, releases the publish gate and plays it on a second account** (journey 1); the post-service consumer that flips a reel from `pending` to `approved`/`rejected` has no behaviour test; every test that runs real ffmpeg skips unless ffmpeg is installed, and no CI job installs it; the server half of resumable upload (part, status, complete) has no test at all; and neither client has a test of "one active player". CI evidence is also stale: Go CI last ran 177 commits ago, Android CI has not run a test on this branch since at least August (it fails on a missing secret before Gradle starts), and web tests run in no CI job.

## 1. CI status at the current head (gh, read-only)

| Workflow | Last run on this branch | At head? | What it shows |
|---|---|---|---|
| `e2e.yml` | 44008085438, success, 2026-11-17, `770f5ed9d` (dispatch) | yes (HEAD `58f602b6d` is docs only) | `e2e (media)`: `TestE2E_Media_UploadConfirm` PASS 0.03 s, `TestE2E_Media_ConfirmRefusesMistypedUpload` PASS 0.01 s. `e2e (social)`: `TestE2E_Social_PostFollowFeedLike` PASS 0.61 s. Read in the job log (VERIFIED-RUN) |
| `integration-all.yml` | 44008075845, success, `770f5ed9d` (push) | yes | Jobs for media, post, analytics, trust-safety each print `PASS build/vet/vet-integ/test/integration` and `RESULT PASS` (VERIFIED-RUN). `go.sh` runs `go test` without `-v` (`scripts/verify/go.sh:168`), so the log does not show which integration tests ran and which skipped. Kafka and Scylla are optional, not started, for media and post (`scripts/ci/integration-services.yaml:44-45,64-65`) |
| `go-ci.yml` | 41168872400, success, 2026-10-31, `44f00fafe` | **no: 177 commits behind** (`git rev-list --count 44f00fafe..HEAD`) | Triggers are dispatch, PR and push to `main` only (`go-ci.yml:17-34`); nobody dispatched it since 31 Oct. feed-service and api-gateway are not in the integration manifest, so **their unit tests have not run in CI at head** |
| `android-native.yml` | 40175440840, **failure**, 2026-10-25, `ae9259f02` (dispatch); earlier runs 26 Aug and 25 Aug also failed | no | Failed in step "Write google-services.json": the `GOOGLE_SERVICES_JSON` secret is empty and the step exits 1 by design (`android-native.yml:83-90`). No Gradle task ran, no test result was produced ("No files were found … build/test-results"). VERIFIED-RUN (log read; no secret value printed) |
| Web (`pb-echo`) | none | — | The web repo has no `.github` folder and no Playwright or Cypress config. `package.json` has no `test` script hit by any CI |
| `scripts/smoke/dev-smoke.mjs` | manual only | — | The only automated script that uploads a real MP4 and fetches its HLS master; when it last ran: UNVERIFIED |

## 2. Coverage matrix: the six journeys

"Integ" = `-tags integration`, run by `integration-all.yml` on `_test` databases.

| Journey | Go unit | Go integ | E2E (`tools/integration`) | Android unit | Web unit | Verdict |
|---|---|---|---|---|---|---|
| 1. Upload, process, publish, play on a second account | Pieces only: confirm checks `internal/service/reels_hardening_test.go:339` `TestConfirmChecksStoredTypeAndContainer`; outbox-only confirm `transcode_outbox_guard_test.go:13`; worker guards `cmd/worker/transcode_guards_test.go` (21 tests); HLS ladder from a real encode `internal/processing/hls_pipeline_test.go:106` `TestHLSRepackagedReelIsPlayable` (**skips without ffmpeg**, F-3); post read gate `post-service/internal/service/processing_test.go:113` `TestHiddenWhileProcessingIsAuthorOnly`; media gate `media_access_gate_test.go:234-429`, `media_access_moderation_test.go:16,118,202` | Queue/complete atomicity `media-service/internal/store/postgres/transcode_durability_integration_test.go:51,92`; gate against Postgres `post-service/internal/service/media_access_anonymous_integration_test.go:99` (private, pending review, soft-deleted cases at :118-123) | **None for video.** `media_e2e_test.go:110` uploads a 64 px PNG; its comment (:104-107) says a finished transcode "belongs in a media-specific job", which does not exist. No file in `tools/integration` creates a flick and plays it | `ReelPublishPipelineTest.kt` (feature/post, 44 tests: request shape, MEDIA_NOT_READY polling :583, 30-min window :642, restart without re-upload :564); `ReelsViewModelTest.kt:460-549` (playback URL choice) | `studioCreateFields.test.ts`, `reelDurationCap.test.ts` (payload and classification) | **P1 gap (F-1, F-2)**. Each hop has unit tests; the hop between transcode completion and publish has none, and nothing crosses services |
| 2. Interrupt an upload and resume | `resumable_chunk_plan_test.go:12,49,69` (plan maths), `resumable_ratelimit_test.go:15,35`, `resumable_video_cap_test.go:78`. **`UploadPart`, `GetResumableUploadStatus`, `CompleteResumableUpload` (`internal/service/resumable.go`) are called by no test** | Expiry only: `reels_hardening_integration_test.go:179` `TestLiveReelsExpiredResumableUploadsAreClosedOnce` | None | Strong against a fake server: `core/media/…/upload/ResumableUploaderTest.kt:249` (resume from last confirmed part), `:401` (only missing parts sent), `:424`, `:437`, `:450`, `:463`, `:473`; `ReelPublishPipelineTest.kt:982`; `ReelPublishRunTest.kt:93-146` (VERIFIED-READ, names and assertions) | `src/lib/__tests__/resumableUpload.test.ts:52-148`: per-part retry within one run only. The web client never reads the session status (`src/lib/resumableUpload.ts` calls only `/chunk` :58 and `/complete` :65), so a reload starts over; no test | **P1 gap (F-5)** on the server |
| 3. Invalid, oversized, over-long, disguised files rejected | Size: `upload_size_test.go:11,38`, `video_original_cap_test.go:23,43`, `upload_size_handler_test.go:44,95`. Type at confirm: `confirm_errors_test.go:17`, `reels_hardening_test.go:141,339`. Container sniff in worker: `cmd/worker/transcode_guards_test.go:477`; `processing/reels_hardening_test.go:203`. Probe limits: `processing/reels_hardening_test.go:139` `TestVideoLimits` on hand-built `VideoMeta` structs only | — | `media_e2e_test.go:149` text bytes declared `video/mp4` → 409 `UPLOAD_INTEGRITY_ERROR` and status `rejected` (VERIFIED-RUN in CI) | `VideoGateTest.kt`, `ReelLengthTest.kt`, `VideoSizeCapTest.kt` (names only); pipeline `:659` unreadable video, `:1021` 413 → resumable | `reelDurationCap.test.ts:18,29` | **Partial (F-6)**: no test feeds a real file of the wrong container, a real over-long file, or a real file through `ProbeVideo` → `CheckVideoLimits` |
| 4. Swipe 50 reels: no repeated playback, stalls, audio overlap | Feed paging: `feed-service/internal/service/reels_category_test.go:226` `TestReelCategory_CursorWalksEveryMatchOnceInOrder`; fill `reels_fill_test.go:17,39`; short-form split `shortform_mainfeed_test.go:35` (I ran 23 reel tests: PASS) | — | — | Sound: `core/media/…/sound/ReelSoundPlayerTest.kt` 36 tests incl. `:436` another reel's sound replaces the first, `:387` detach, `:454` background (VERIFIED-READ names). HLS signature expiry `HlsRefreshTest.kt:64-177`. **`PlayerPool` is constructed by no test**: only `PlayerPoolPlaceTest.kt` (3) and `PlayerPoolRelocationTest.kt` (5) on helper logic; `playOnly`, pool bound, preload and release are untested | Player tests are source-text greps: `src/features/reels/__tests__/soundSync.test.ts:269` reads `ReelVideo.tsx` as a string; `presentation.test.tsx:21,59,76` read `ReelsScreen.tsx`. No test mounts the player | **P1 gap (F-7)** for "one active player / no overlapping video audio" |
| 5. Delete a reel during processing | Worker: `transcode_guards_test.go:222` (deleted before worker → skipped, not retried), `:235` (deleted mid-run → skipped, objects removed), `:265`, `:493` (VERIFIED-RUN: PASS) | `reels_hardening_integration_test.go:54,77,92,122,159` (completion for a deleted asset recorded as skip; late confirm does not reset; owner delete records prefix for reclaim) | — | Client discard mid-upload `ReelPublishPipelineTest.kt:914`; delete from More sheet `feature/feed/…/ui/more/PostMoreViewModelTest.kt:600-671` | `recentlyDeleted.test.ts` (not opened) | **Media side covered**. Gap (F-13): post deleted while its media processes, then `media.transcode.completed` arrives: no test that the post-service consumer leaves it deleted |
| 6. Report a reel and follow it through moderation | Report filing: trust-safety `report_entities_handler_test.go:19,59`, `report_target_oracle_test.go:91,133`, `moderation_test.go:12` (`reel` → `post`) | `trust-safety-service/internal/service/report_entities_integration_test.go:65` files a `reel` report; `post-service/internal/http/admin_token_integration_test.go:110,217` admin decision and report review are audited; `review_audit_integration_test.go:253` flagged-reels queue is moderator-only | None (`TestE2E_TrustSafety` in another job; not opened for reels) | `PostMoreViewModelTest.kt:557,575` report wire and 409 | not found | **Partial (F-9)**: no test joins report → decision → reel gone from `/v1/feed/reels` and refused by the media gate |

## 3. Coverage matrix: "Check specifically"

| Check | Covered by (would fail if broken) | Gap |
|---|---|---|
| 4.4: server decides the key, short signed PUT with signed type and length | `media_e2e_test.go:41-75` exercises it for an image (VERIFIED-RUN in CI); `service/reels_hardening_test.go:86` replaced original refused | Not for a video PUT |
| 4.4: type verified from content | Confirm magic check `reels_hardening_test.go:339`; worker sniff `transcode_guards_test.go:477`; e2e `media_e2e_test.go:149` | Only text-as-MP4; no real foreign container (F-6) |
| 4.4: metadata (GPS) stripped from video | `processing/reels_hardening_test.go:226` (argument check), `:237` `TestRenditionCarriesNoLocation` (real encode, **skips without ffmpeg**) | F-3 |
| 4.4: ffmpeg isolated with time limits | `reels_hardening_test.go:37,49,61,74` (protocol whitelist, playlist input refused), `:89,116` (timeout maths), `:104` kill at deadline (**skips without ffmpeg**) | F-3 |
| Duration, size, format limits after probing the real file | Struct-level `TestVideoLimits`; size caps at init | No probed file (F-3, F-6) |
| Transcode idempotent | `transcode_durability_integration_test.go:51` `TestLiveQueueTranscodeIsAtomicAndIdempotent`, `:92`; `reels_hardening_integration_test.go:77` | — |
| Retries with a limit | `transcode_guards_test.go:40,58,70` (8 attempts then quarantine; permanent at once); `:381,419` job budget | VERIFIED-RUN locally |
| Dead-letter path | `transcode_guards_test.go:86` unwritable quarantine never commits; `transcode_durability_integration_test.go:134` poison durable | `cmd/worker/kafka_durability_integration_test.go:22` needs `KAFKA_BROKERS`, which `integration-all` never starts, so it skips in CI (F-10). post-service `media.events.dlq`: no test found |
| Never stuck in "processing" | Sweeper: `transcode_lease_integration_test.go:172` selection, `:263` give-up marks failed, `:358` SKIP LOCKED / replicas never double-queue, `:413` requeue refuses in-flight work (runs on `POSTGRES_DSN`, set by the manifest); worker `transcode_lease_test.go:39-166` heartbeat, superseded delivery, kill switch (VERIFIED-RUN: PASS) | Whether each integration test executed rather than skipped is not in the CI log (F-12). Client message for a stuck reel: Android 30-min window `ReelPublishPipelineTest.kt:642`; web `useUploadStudio.ts:298,337` failed-status branch has no test (F-14) |
| Worker crash recovery | Lease + sweeper tests above model it with stale heartbeats | No test kills a running worker; acceptable at unit level |
| Duplicate job delivery | `transcode_lease_test.go:119` `TestSupersededDelivery`; `transcode_durability_integration_test.go:134` `TestLiveDuplicateWorkersSerializeAndPoisonIsDurable` | Kafka-level redelivery skipped (F-10) |
| Not public before processing and moderation complete | Read side: `processing_test.go:113`, `:135` (no `hls_url` until ready and passed), `media_access_moderation_test.go:16,118,202` (negative control), `media_access_anonymous_integration_test.go:99` | **Write side untested**: `post-service/internal/consumers/media.go` (`finalizeReviewGate`, `FlipReviewStatusFromPending`) has only a source-grep test (`media_cover_guard_test.go:15`) and `media_reclassify_test.go:9` (F-2) |
| Private and deleted reels cannot be played from a saved manifest or segment | Gate: `media_access_gate_test.go:247,350,379`; `media_access_anonymous_integration_test.go` soft-deleted case; media HTTP HEAD `http/handler_head_test.go:482,490,498`; info `record_read_handler_test.go:382,387`; segment TTL rules `delivery/sign_cache_test.go:127-324` | No GET `/hls/:playlist` denial test at the media-service HTTP layer; no test (and by design no mechanism) that an already signed CloudFront segment URL stops working after delete or a switch to private (F-8) |
| View counted once, replay cannot inflate | analytics `service/ingest_full_model_integration_test.go:22` `TestLiveIngestPersistsEveryEventTypeAndDedupesOnReplay`, `ingest_integration_test.go:16`, `sessions_integration_test.go:195` late play_end no second view, `:262`, `:289`; `aggregation/daily_view_cap_integration_test.go:26,144,210`; unit `ingest_validation_test.go:167`, `ingest_duration_test.go:17,94`, `ingest_sessions_test.go:163` (I ran `internal/service` and `internal/model`: PASS). Android `core/analytics/…/VideoWatchTrackerTest.kt:201` ending twice → one play_end, `:275` loop counted once | Scylla `reel_views`/`reel_view_counts` writes: no test that touches Scylla |
| One player active; memory flat while swiping | none (F-7) | F-7 |
| Slow or dropping network | Android `HlsRefreshTest.kt:125` two retries then error; `ResumableUploaderTest.kt:198-329`; `ReelSoundPlayerTest.kt:113,323` | No test of player stall/rebuffer on either client; web hls.js error recovery untested |
| Creator controls in the request | Android `ReelPublishPipelineTest.kt:397,410`; post-service `http/create_post_controls_test.go:102`; feed `hydration_controls_test.go:16` | — |
| Reels carry no repost | `post-service/internal/http/heart_only_routes_test.go:155`, `service/heart_only_test.go:191` | — |

## 4. Counts

| Layer | Count (working tree, grep) |
|---|---|
| Go unit + integ, media-service | 179 files, 913 `func Test`, 26 integration-tagged files. I ran `go test -count=1 -v ./...`: **1164 PASS, 10 SKIP, 0 FAIL** (subtests counted); all 10 skips are "ffmpeg not installed" or the opt-in timing bench (VERIFIED-RUN) |
| post-service | 317 files, 1590 tests, 72 integration files; about 150 test names carry Flick/Reel/Short/Video |
| feed-service | 111 files, 489 tests, 0 integration; 23 reel-named tests (ran: PASS) |
| analytics-service | 44 files, 150 tests, 21 integration |
| trust-safety-service | 56 files, 205 tests, 13 integration |
| E2E `Architecture/tools/integration` | 50 `func Test` across domains; **0 touch a video or a reel** (`reviewer_e2e_test.go:141` sends `content_type: flick` to the reviewer service only) |
| Android unit (`src/test`, `@Test`) | feature/feed 19 files / 261; feature/post 43 / 390; core/media 30 / 285; core/feed 70 / 689; core/engagement 17 / 148; core/analytics 5 / 69. Reels-specific files I identified: about 40 (ReelsViewModel, ReelRail, ReelsExplore, SoundPage, ReelPublish*, ReelStudio, ReelSound*, Resumable*, HlsRefresh, PlayerPool*, VideoWatchTracker, VideoGate, ReelLength, VideoSizeCap, Banuba*) |
| Android instrumented | 0: no `src/androidTest` folder in the tree |
| Web unit (`bun:test`) | 371 files in total; `features/reels` 26 files / 362 cases, `features/upload` 12 / 180, `features/video-shell` 10 / 97, `features/offline` 8 / 149. At least 10 reels/upload test files assert on source text via `readFileSync` (e.g. `stage.test.ts:23`, `presentation.test.tsx:21`, `soundSync.test.ts:269`, `moreMenu.test.tsx`, `shareLink.test.ts`) |
| Web e2e | 0 |
| Load tests for reels | 0 |

## 5. Skipped and environment-gated tests in scope

| Where | Condition | In CI? |
|---|---|---|
| `media-service/internal/processing/hls_pipeline_test.go:39-46` (`TestHLSRepackagedFromRenditionsIsPlayable`, `TestHLSRepackagedReelIsPlayable`), `reels_hardening_test.go:105,238` (`TestFFmpegIsKilledAtItsDeadline`, `TestRenditionCarriesNoLocation`), `TestFFmpegFrameSampler_Real`, four `internal/fingerprint` harness tests | `exec.LookPath("ffmpeg")` fails → `t.Skip` | No workflow installs ffmpeg (`grep -rl ffmpeg .github/` hits only image builds). Whether `ubuntu-latest` has it preinstalled: UNVERIFIED; the logs cannot say (F-3) |
| `hls_pipeline_test.go:362` `TestTranscodePipelineTiming` | `MEDIA_PIPELINE_BENCH=1` | Opt-in bench; fine |
| `cmd/worker/kafka_durability_integration_test.go:26` | `MEDIA_DURABILITY_POSTGRES_DSN` and `KAFKA_BROKERS` | Kafka not started → skips (F-10) |
| `store/postgres/transcode_durability_integration_test.go:25`, `reels_hardening_integration_test.go` | `MEDIA_DURABILITY_POSTGRES_DSN` | Listed in the manifest `vars` (`integration-services.yaml:66`), so expected to run; per-test execution UNVERIFIED (F-12) |
| Android `core/feed/…/SoundsContractFixtureTest.kt:191,206`, `VideoLibraryContractFixtureTest.kt:112`, `offline/OfflineContractFixtureTest.kt:125` | `assumeTrue` the post-service / postbook-ui checkout exists beside the app | The postbook-ui sibling is never checked out in CI, so `:206` would always skip there; moot while Android CI does not run |
| `integration-all.yml:81` | `continue-on-error` for modules marked `blocked` | Not media/post/analytics/trust-safety at this run |

No `@Ignore`, `Thread.sleep` found in the six Android modules' `src/test`; no `.skip(`/`.todo(` in web `features/reels`, `features/upload`, `features/video-shell`, `src/lib/__tests__`.

## 6. Findings

```
ID:            reels-F-1
Title:         No test at any layer uploads a video, lets it transcode and publish, and plays it on a second account; the e2e media job proves only an image upload.
Severity:      P1
Category:      tests
Evidence level: VERIFIED-RUN (e2e log: two media flows, 0.03 s and 0.01 s, image and text only); VERIFIED-READ (media_e2e_test.go)
Evidence:      Architecture/tools/integration/media_e2e_test.go:104-107,110,149; .github/workflows/e2e.yml:57-60 starts only api-gateway and media-service (no media-worker, no post-service, no Kafka consumer of media.events); no file in tools/integration creates a flick. scripts/smoke/dev-smoke.mjs:595-701 does the journey but runs by hand, in no workflow.
How to reproduce: grep -l "flick\|m3u8\|video/mp4" Architecture/tools/integration/*.go; read run 44008085438 job "e2e (media)".
Impact:        Journey 1 (the core reels journey) can break across media-service, the worker, post-service's publish gate and the delivery gate with every CI job green. The 13 Oct and 17 Oct regressions of this class (stalled transcodes, wrong playback URL) would not be caught.
Fix:           Add an e2e matrix entry "reels": services api-gateway, media-service, media-worker (ffmpeg via apt-get), post-service, identity-profile, trust-safety; flow TestE2E_Reels_UploadPublishPlay using the checked-in scripts/smoke/fixtures/smoke-2s-portrait.mp4: init/PUT/confirm as A, poll /v1/media/:id/status until ready+passed, POST /v1/posts content_type=flick, Eventually GET /v1/posts/:id as B is 200 with review_status approved, GET /v1/media/:id/hls/master.m3u8 as B is 200 and lists 360p, and as an anonymous caller on a private copy is 404.
Acceptance:    The flow is green, and goes red when finalizeReviewGate is stubbed out or the worker is not started.
Effort:        M
```

```
ID:            reels-F-2
Title:         The consumer that releases a reel from review (pending → approved on ready+passed, → rejected on failed or scan-rejected, stays pending on manual_review) has no behaviour test; its only test greps its source.
Severity:      P1
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      post-service/internal/consumers has three test functions: media_cover_guard_test.go:15 (os.ReadFile("media.go") + strings.Contains), media_reclassify_test.go:9, and none for finalizeReviewGate. FlipReviewStatusFromPending is exercised once, as a side check in store/postgres/post_restrictions_integration_test.go:530. The read-side gates (processing_test.go:113,135; media_access_moderation_test.go) are tested, the transition is not.
How to reproduce: grep -rn "finalizeReviewGate" --include=*_test.go Architecture/services/post-service (no hits).
Impact:        A change that approves on ready without passed, or approves manual_review, makes unscanned reels public with green CI. This is the "not publicly reachable before processing and moderation complete" check.
Fix:           Table test in internal/consumers/media_review_gate_test.go with a fake store: events (ready,passed) → approved + moderation_reviews row + post:body cache delete; (ready,rejected) → rejected; (failed,*) → rejected; (ready,manual_review) → still pending; a post already approved or deleted → untouched; a cover-only post → untouched.
Acceptance:    Mutating finalizeReviewGate to approve on manual_review fails the test (scripts/harness/mutate.sh).
Effort:        S
```

```
ID:            reels-F-3
Title:         Every test that runs real ffmpeg skips when ffmpeg is absent, no CI job installs it, and the CI logs cannot show whether they ran, so no proof exists that a real reel transcodes, that HLS is playable, that GPS is stripped or that ffmpeg is killed at its deadline.
Severity:      P1
Category:      tests
Evidence level: VERIFIED-RUN (locally: go test -v ./... in media-service = 1164 PASS, 10 SKIP, the skips being TestHLSRepackagedReelIsPlayable, TestHLSRepackagedFromRenditionsIsPlayable, TestFFmpegIsKilledAtItsDeadline, TestRenditionCarriesNoLocation, TestFFmpegFrameSampler_Real, four fingerprint harness tests, the opt-in bench); UNVERIFIED (whether ubuntu-latest has ffmpeg)
Evidence:      internal/processing/hls_pipeline_test.go:39-46; reels_hardening_test.go:105-106,238-239; .github/workflows (only build-push.yml/build-push-acr.yml mention ffmpeg); scripts/verify/go.sh:133,168 run go test without -v.
How to reproduce: cd Architecture/services/media-service && go test -count=1 -v ./internal/processing -run 'TestHLSRepackagedReelIsPlayable|TestRenditionCarriesNoLocation'
Impact:        A broken encoder argument, a missing codec in the alpine image or a regression in the HLS repackaging passes CI. The worker's ProbeVideo → CheckVideoLimits path is never executed on a real file by any test.
Fix:           In go-ci.yml and integration-all.yml (media-service entry) add `sudo apt-get install -y ffmpeg`, and make requireFFmpegOrSkip call t.Fatal when CI=true. Better: also run these tests inside the built media-worker image so the alpine ffmpeg is the one under test.
Acceptance:    The CI log for media-service shows the five tests as PASS (a step running them with -v -run), and removing ffmpeg from the job turns it red.
Effort:        S
```

```
ID:            reels-F-4
Title:         The worker's orchestration function processMessage (lock, inbox, guards, encode, scan, CompleteTranscode) is called by no test; only its helpers are tested.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      grep "processMessage(" in media-service/cmd/worker/*_test.go: no hits (9 test files, 53 tests). The order of steps in cmd/worker/main.go:260-482 (e.g. that the sniff and limits run before any encode, that scan runs before completion) is unpinned.
How to reproduce: as above.
Impact:        Reordering or dropping a guard inside processMessage passes every test.
Fix:           cmd/worker/process_message_test.go with fakes for store, blob, encoder and scanner: a limits refusal never calls the encoder and completes as failed; a scanner error leaves manual_review, never passed; a deleted asset is skipped.
Acceptance:    Deleting the CheckVideoLimits call in processMessage fails the test.
Effort:        M
```

```
ID:            reels-F-5
Title:         The server half of resumable upload (upload a part, read the session status, complete) has no test at any layer, and the web client cannot resume across a reload at all.
Severity:      P1
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      media-service/internal/service/resumable.go defines InitResumableUpload, GetResumableUploadStatus, UploadPart, CompleteResumableUpload; grep of every *_test.go for UploadPart|CompleteResumable|GetResumable: no hits. Covered: chunk plan (resumable_chunk_plan_test.go), init rate limit and cap, session expiry (reels_hardening_integration_test.go:179). Android resume logic is well tested but only against a fake server (core/media/…/upload/ResumableUploaderTest.kt:249,401,424,437,463,473). Web: src/lib/resumableUpload.ts calls only /chunk (:58) and /complete (:65); resumableUpload.test.ts has no resume case.
How to reproduce: grep as above.
Impact:        Journey 2 has no server proof: a re-sent part, an out-of-order part, a complete with a missing part, or a status read that misreports parts can corrupt or lose large uploads. Web users lose a 3 GB upload on a reload (functional, for Track A).
Fix:           Integration test internal/store/postgres or internal/service against Postgres plus a fake multipart blob store: init, upload parts 1 and 3, status lists exactly {1,3}, re-upload part 1 is idempotent, complete is refused until part 2 lands, then succeeds once and a second complete is a no-op; another user's uploadId is 404.
Acceptance:    Test passes; dropping the part-count check in CompleteResumableUpload fails it.
Effort:        M
```

```
ID:            reels-F-6
Title:         Rejection of bad video files is tested only with text bytes or hand-built probe structs; no test sends a real wrong-container, over-long or zero-stream video through confirm and the worker.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      media_e2e_test.go:149 (text declared video/mp4); transcode_guards_test.go:477 calls checkSniffedContainer with constants; processing/reels_hardening_test.go:139 TestVideoLimits uses meta(...) structs.
How to reproduce: read the three tests.
Impact:        A regression in the container sniffer or in ffprobe parsing (the step that turns a file into VideoMeta) lets disguised or over-long files through to the encoder.
Fix:           In internal/processing (ffmpeg-gated, made mandatory by F-3): synthesise a 2 s AVI renamed .mp4, an audio-only MP4, and a clip whose probed duration exceeds a lowered MEDIA_MAX_VIDEO_DURATION_SEC; assert sniff mismatch, no_video_stream and duration_too_long as permanent failures. Add the AVI case to the e2e media flow at confirm.
Acceptance:    Each case is refused with its reason code.
Effort:        S
```

```
ID:            reels-F-7
Title:         Neither client has a test that only one reel player is active, that the Android pool stays bounded and releases players, or that two videos never play audio at once; the web player tests read the component source as text.
Severity:      P1
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      Android: no test constructs PlayerPool or calls playOnly (grep "playOnly\|PlayerPool(" in */src/test: none); PlayerPoolPlaceTest.kt (3) and PlayerPoolRelocationTest.kt (5) test helpers only; a FakeExoPlayer.kt exists in the test tree. Sound overlap IS covered (ReelSoundPlayerTest.kt:436). Web: soundSync.test.ts:269 and presentation.test.tsx:21,59,76 assert on readFileSync of ReelVideo.tsx / ReelsScreen.tsx; nothing mounts the player. Android instrumented tests: none.
How to reproduce: grep as above.
Impact:        Journey 4 (swipe 50 reels, no overlap, no memory growth) can regress on both clients with green tests; double audio and leaked decoders are the classic short-video bugs.
Fix:           core/media/src/test/.../PlayerPoolTest.kt with FakeExoPlayer: after playOnly(i) for i in 0..49 exactly one player has playWhenReady=true and volume>0, at most 4 players were ever created, and every evicted one was released. Web: a happy-dom test mounting ReelsScreen with three reels, asserting one <video> is playing and the previous hls instance's destroy() was called on swipe.
Acceptance:    Breaking playOnly to skip pausing the previous player fails the Android test.
Effort:        M
```

```
ID:            reels-F-8
Title:         No test shows that a reel made private or deleted stops playing from an already fetched playlist or segment URL, and the media-service HTTP denial tests cover HEAD and info routes but not GET of an HLS playlist.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      Gate decisions are tested (post-service media_access_gate_test.go, media_access_anonymous_integration_test.go:118-123; media-service http/handler_head_test.go:482-527, record_read_handler_test.go:382,387). Segment signatures last up to 12 h (delivery/sign_cache_test.go:127) and no invalidation exists (map open question 13), so the property is untested and, as designed, false for that window.
How to reproduce: read the files.
Impact:        A private or deleted reel keeps playing for viewers who already hold the playlist, with no test documenting the exposure window.
Fix:           media-service http test: GET /v1/media/:id/hls/720p.m3u8 for a stranger on a private asset is 404 identical to a missing id, and after the gate flips to deny a second GET of the same playlist is 404. Record the segment window as an accepted risk or shorten it (Track B).
Acceptance:    Test fails when the playlist route skips the gate.
Effort:        S
```

```
ID:            reels-F-9
Title:         Report → moderation is tested in pieces (filing, admin decision audit) but no test follows a reported reel to a decision and checks it is gone from the reels feed and refused by the media gate.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ (names and the reel line of report_entities_integration_test.go)
Evidence:      trust-safety-service/internal/service/report_entities_integration_test.go:65; post-service/internal/http/admin_token_integration_test.go:110,217; Android PostMoreViewModelTest.kt:557,575; no e2e flow for reels moderation.
How to reproduce: read the files.
Impact:        Journey 6 can break between services (event not consumed, restriction not applied to flicks) unnoticed.
Fix:           post-service integration test: apply a moderation decision "remove" to a flick through the internal decision route, then CanViewMedia for a stranger is denied and the post is absent from the reels listing query; extend the F-1 e2e flow with a report and a decision.
Acceptance:    Test fails when the decision handler skips flicks.
Effort:        M
```

```
ID:            reels-F-10
Title:         The Kafka half of the worker's durability (offset committed only after the quarantine row is written) is tested only in a test that needs KAFKA_BROKERS, which no CI job provides.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      cmd/worker/kafka_durability_integration_test.go:22-26; scripts/ci/integration-services.yaml:64-65 (media: needs postgres, kafka optional, not started). The Postgres half runs (transcode_durability_integration_test.go:134).
How to reproduce: read the files.
Impact:        A regression that commits the offset before quarantine loses poison transcode requests silently.
Fix:           Start Redpanda for the media-service entry (the e2e job already boots it) and add KAFKA_BROKERS to its env.
Acceptance:    The test appears as PASS in a -v run of the media integration job.
Effort:        S
```

```
ID:            reels-F-11
Title:         CI has not run the reels tests at the current head on three of four layers: Go CI is 177 commits behind, Android CI fails on a missing secret before Gradle starts, and web tests are in no CI job.
Severity:      P1
Category:      tests
Evidence level: VERIFIED-RUN (gh run list and the failed Android log)
Evidence:      go-ci.yml last run 41168872400 at 44f00fafe (31 Oct); triggers go-ci.yml:17-34. feed-service unit tests run only there (not in the integration manifest). android-native.yml run 40175440840 failed at "Write google-services.json" (android-native.yml:83-90, secret empty), as did the two runs before it in August; no test results were produced. pb-echo has no .github folder.
How to reproduce: gh run list --workflow=go-ci.yml --branch codex/module-01-02-launch-safety; gh run view 40175440840.
Impact:        Launch gate line 9 ("tests pass in CI from a clean state") cannot pass for reels; Android's 1,800+ unit tests and the web's 788 reels-area cases are verified only by hand runs.
Fix:           Dispatch go-ci on this branch (or add the branch to push triggers); set the GOOGLE_SERVICES_JSON secret or let the step write a placeholder for unit-test-only runs; add a workflow to postbook-ui running `bun test` on PRs.
Acceptance:    A green run of each at the release commit, with test-result artefacts.
Effort:        S
```

```
ID:            reels-F-12
Title:         The integration CI logs print one PASS line per module, so a test that silently skips on a missing variable is indistinguishable from one that ran.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-RUN (read the four reels jobs of run 44008075845)
Evidence:      scripts/verify/go.sh:168 runs go test -tags integration without -v or -json; the job log shows "PASS integration media-service (18s)" only; full logs are kept only on failure.
How to reproduce: gh run view --job 133788443047 --log.
Impact:        The feed audit already found suites green with nothing running; the same cannot be ruled out here for the lease, durability, view-dedup and gate integration tests.
Fix:           Run the integration stage with -json and upload the file as an artefact, and fail the stage when any test in a manifest module skips with a "not set" reason in CI.
Acceptance:    The artefact lists TestTranscodeLeaseSweepSelection, TestLiveIngestPersistsEveryEventTypeAndDedupesOnReplay and TestAnonymousMediaAgainstPostgres as pass.
Effort:        S
```

```
ID:            reels-F-13
Title:         No test covers a post deleted while its video is still processing and then receiving the transcode-completed event.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      media side is covered (transcode_guards_test.go:222,235,265; reels_hardening_integration_test.go:54); post-service consumers have no such test (see F-2).
How to reproduce: as F-2.
Impact:        A deleted reel could be flipped to approved or have its video_metadata refilled, reappearing on restore paths or in counts.
Fix:           Add the case "post soft-deleted before transcode completed" to the F-2 table: review_status and deleted_at unchanged.
Acceptance:    The case fails if FlipReviewStatusFromPending ignores deleted_at.
Effort:        S
```

```
ID:            reels-F-14
Title:         The web upload studio's "processing failed" and stuck-processing branches have no test.
Severity:      P3
Category:      tests
Evidence level: VERIFIED-READ
Evidence:      src/features/upload/useUploadStudio.ts:292-303,332-342; grep "Processing failed|all_ready" in web tests: hits only outside features/upload. Android has the equivalent (ReelPublishPipelineTest.kt:642).
How to reproduce: grep as above.
Impact:        A web creator could be left on a spinner or shown success for a failed transcode.
Fix:           Unit test of the polling reducer with a renditions response of status "failed" and one that never becomes all_ready within the budget.
Acceptance:    Both cases render the error message.
Effort:        S
```

## 7. Covered properly (bodies read, would fail if broken)

- Stall sweeper and lease: `media-service/internal/store/postgres/transcode_lease_integration_test.go:172,263,358,413`; `cmd/worker/transcode_lease_test.go:39,55,64,119,155,166` (ran: PASS).
- Retry limit and quarantine: `cmd/worker/transcode_guards_test.go:40,58,70,86` (ran: PASS).
- Delete during processing, media side: `transcode_guards_test.go:222,235,265,493`; `reels_hardening_integration_test.go:54,77,92,122,159`.
- Transcode request atomic with its outbox and idempotent: `transcode_durability_integration_test.go:51,92,134`; `service/transcode_outbox_guard_test.go:13`.
- Media gate rules (anonymous, private account, hidden author, scheduled, pending review, soft-deleted, moderation non-owner matrix with a negative control): `post-service/internal/service/media_access_gate_test.go:234-429`, `media_access_moderation_test.go:16,118,202`, `media_access_anonymous_integration_test.go:99` (ran the unit set: 22 PASS).
- Processing posts hidden from everyone but the author; no `hls_url` before ready and passed: `post-service/internal/service/processing_test.go:113,135`.
- View dedup, replay, late play_end, self-views, per-day cap: analytics tests listed in section 3 (unit set ran: PASS); Android `VideoWatchTrackerTest.kt:132,201,275`.
- Text disguised as MP4 refused at confirm and stays rejected: `media_e2e_test.go:149` (PASS in CI run 44008085438).
- Reels feed cursor walks each match once, fill rules, short-form never in the main feed: `feed-service/internal/service/reels_category_test.go:226`, `reels_fill_test.go:17,39`, `shortform_mainfeed_test.go:35` (ran: PASS; not run in CI at head, F-11).
- Android client resume logic and publish retry/idempotency: `ResumableUploaderTest.kt`, `ReelPublishPipelineTest.kt:548,564,583,982`, `ReelPublishRunTest.kt:93-146`.
- Android added-sound sync and single sound per player: `ReelSoundPlayerTest.kt` (36 tests).
- Reels refuse repost: `post-service/internal/http/heart_only_routes_test.go:155`, `service/heart_only_test.go:191`.

## Not verified

- Whether `ubuntu-latest` runners carry ffmpeg, hence whether the ten ffmpeg tests run in CI (logs carry no per-test lines).
- Per-test execution of the media, post, analytics and trust-safety integration suites in run 44008075845 (same reason, F-12).
- Android: the bodies of most tests beyond those named above (VideoGateTest, ReelLengthTest, VideoSizeCapTest, ReelsExploreViewModelTest, PostMoreViewModelTest) were read by name and selected lines only; nothing Android was run (the lead runs it).
- Web: no web test was run; bodies of `moreMenu.test.tsx`, `shareLink.test.ts`, `telemetry.test.ts`, `recentlyDeleted.test.ts` not opened beyond names and the `readFileSync` grep.
- When `scripts/smoke/dev-smoke.mjs` last ran and with what result.
- Mutation runs against reels guards: none searched for in `docs/` this pass.
- `TestE2E_TrustSafety` and `TestE2E_Reviewer` bodies for any reels content.
