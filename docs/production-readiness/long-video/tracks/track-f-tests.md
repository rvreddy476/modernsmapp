# Long video (PostTube / MTube): Track F, tests against the user journeys

- **Audit date:** 2026-11-17. Read-only.
- **Trees:** backend and Android `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` @ `7765e81a7`. `7765e81a7` and `58f602b6d` change only `docs/`, so the code is the same as at `770f5ed9d`, the last CI run. `git status --porcelain` showed 728 dirty paths before and after this track. Web `C:\workspace\wt\pb-echo` @ `ca96921`, clean before and after.
- **What was run:** `go test -count=1 -json ./...` in seven services, with no DSN set, so integration-tagged files do not build. `bun test` on the posttube and upload folders. Gradle `:feature:tube:testDebugUnitTest --rerun` and `:core:feed:testDebugUnitTest`. Read-only `gh run list` and `gh run view`. No Docker, database, device or deployed environment was used.
- **Evidence tags:** VERIFIED-RUN (a command and its output), VERIFIED-READ (a file:line read), SUSPECTED, UNVERIFIED.
- Paths are relative to `Architecture/services/` unless they start with another top-level folder. Web paths are relative to `pb-echo`. Android paths are relative to `mobile/android/`.

## 1. Inherited from reels (re-checked 2026-11-17, apply unchanged to long video)

- **reels-9, CI staleness.** Still true. Android CI's last runs failed at "Write google-services.json" (40891364208, 29 Oct). `go-ci.yml` last ran 41168872400 at `44f00fafe` (31 Oct). It triggers only on PRs and on pushes to `main` (`.github/workflows/go-ci.yml:21,34-35`). feed-service is still missing from `scripts/ci/integration-services.yaml`: no module entry, while 35 others are listed (`:41-351`). feed-service has 6 commits since `44f00fafe` (`git log 44f00fafe..HEAD -- Architecture/services/feed-service`). `pb-echo` has no CI and no `test` script (`package.json:5-10`). For long video this means the long-video feed, the subscribed filter and the subscriber fan-out leg (feed-service) have not run in CI for 17 days. The posttube web tests (818) have never run in CI. VERIFIED-RUN.
- **reels-10, no upload → play e2e.** Still true, and long video is worse off; see LV-F-1.
- **reels-11, no load test.** Still true for long video: no k6 or bench scripts for `/v1/videos/*/progress`, `/v1/feed/videos` or the long transcode class. The same `git ls-files` search was re-run on the reels evidence; I added nothing new. VERIFIED-RUN (reels v3), re-read.
- **reels-12, ffmpeg tests skip.** Still true. Locally, media-service skipped 10 tests, all of which need ffmpeg: `TestHLSRepackagedFromRenditionsIsPlayable`, `TestHLSRepackagedReelIsPlayable`, `TestTranscodePipelineTiming`, `TestFFmpegFrameSampler_Real`, `TestFFmpegIsKilledAtItsDeadline`, `TestRenditionCarriesNoLocation` and 4 fingerprint harness tests. None of them uses a landscape, longer-than-600 s or 1080p/4K source, so even with ffmpeg present no test would encode the long-video ladder. VERIFIED-RUN.
- **reels-13, resumable server half untested.** Still true. Long video carries more weight here, because Android switches to resumable upload at 100 MiB and web at 20 MB (`00-map.md` 9.1, 9.2). LV-F-7 adds the expiry and abort half. VERIFIED-READ.
- **v3 caveat on skip visibility.** `scripts/verify/go.sh:133,168` runs tests without `-v`. In CI, the post-service integration stage shows only `PASS integration post-service (45s)` (run 44008075845, job 133788443135). So the CI logs cannot show which long-video integration tests ran and which skipped. VERIFIED-RUN.

## 2. Inventory per layer

### 2.1 Go unit (local run, VERIFIED-RUN)

| Service | Pass | Skip | Fail | Long-video test files (by name) | Skips that matter |
|---|---|---|---|---|---|
| post-service | 2698 | 0 | 0 tests; 1 package build failure (`internal/engagement`, `[build failed]`) | about 60 non-integration files: `tube_audit_*`, `mtube_*`, `playlist_*`, `video_series_*`, `watch_*`, `channel_*`, `chapters_test`, `schedule_test`, `postschedule/worker_test`, `channel_feed_test` (RSS), `end_screens_*`, `credits_*`, `copyright_*`, `media_access_*`, `video_authoring_authz_test`, `post_edit_test`, `live_vod_test` | none. The engagement build failure did not recur: a re-run gave `ok … internal/engagement 0.478s`. The first run hit `fork: Resource temporarily unavailable` while seven modules were building at once, so it is environmental |
| media-service | 1163 | 10 | 1 (`cmd/worker TestUnwritableQuarantineDoesNotCommit`) | `cmd/worker/transcode_classes_test` (long forward), `transcode_lease_test`, `transcode_guards_test`; `delivery/authz*_test`, `sign_cache_test`, `signer_test`; `processing/video_hls_test` (ladder), `storyboard_test`, `hls_test`; `service/resumable_*`, `caption_*`, `subtitle_gate_test`, `download_test` | the 10 ffmpeg skips (section 1). The failure is a timing flake: 6 of 6 re-runs passed (LV-F-11) |
| feed-service | 663 | 0 | 0 | `service/longvideo_test`, `listingvisibility_test`, `watch_subscribed_test`, subscriber fan-out leg tests, `events/visibility_*_test` | none. These tests are not in CI (section 1) |
| search-service | 489 | 0 | 0 | `events/eligibility_test`, `reindex/tube_test`, tube search tests | OpenSearch tests run in `integration-opensearch.yml`; last run 30 Oct, success |
| notification-service | 755 | 23 | 0 | `events/subscriber_fanout_test`, `service/subscriber_fanout_test`, `fanout_channel_name_test` | `TestFanoutJobsIntegration_UploadJobHasNoFollowerCursor` and `_UploadJobDefaultsToSubscriberPhase` skip without a DB. They run in integration-all (the module is in the manifest), but whether they ran there is unverifiable (section 1) |
| user-service | 303 | 5 | 0 | `retired_subscription_routes_test` (410 + Location, canonical post-service) | 5 page-admin integration tests; not long video |
| analytics-service | 216 | 0 | 0 | `model/view_rules_test`, `service/ingest_*_test`, `aggregation/daily_test` | none |

Counts include subtests (`go test -json` "pass" events that carry a Test name).

### 2.2 Go integration (`//go:build integration`, VERIFIED-READ; CI ran them green at `770f5ed9d`, VERIFIED-RUN)

- **post-service**, on `post_it_test`. The CI env sets `VIDEO_SERIES_POSTGRES_DSN`, `SCHEDULE_POSTGRES_DSN`, `CREDITS_POSTGRES_DSN`, `MEMBERS_POSTGRES_DSN` and others (job log, run 44008075845). Long-video files: `store/postgres/mtube_integration_test.go` (6: live VOD idempotent per stream; system playlists one per owner; comments pin, heart and inbox; edit audit + search revision + owner guard `:509`), `tube_audit_integration_test.go` (6: concurrent playlist adds, move/add race, stale progress not applied `:211`), `tube_decisions_integration_test.go`, `playlist_events_integration_test.go`, `video_series_integration_test.go`, `watch_history_integration_test.go` (4: clear removes only the viewer's rows `:178`), `channel_subscriptions_integration_test.go` (4: outbox in the same tx, bell-off excluded `:256`), `channels_feed_integration_test.go`, `scheduled_integration_test.go` (publish exactly once; `publish_at` cleared, one PostCreated `:215-265`), `feed_reads_integration_test.go`; `service/media_access_anonymous_integration_test.go` (signed-out rule per clause, including unlisted, private, scheduled, members-only, deleted `:110-130`), `channel_feed_integration_test.go` (RSS), `end_screens_integration_test.go`, `delete_post_owner_integration_test.go`.
- **media-service:** `transcode_lease_integration_test.go` (sweep selection, give-up marks failed, SKIP LOCKED never double-queues, requeue refuses in-flight work), `transcode_durability_integration_test.go` (queue is atomic and idempotent, duplicate workers serialize, poison is durable), `reels_hardening_integration_test.go:179` (expired resumable sessions closed once).
- **analytics-service:** `sessions_integration_test.go` (a late play_end makes no second view `:195`), `ingest_full_model_integration_test.go:22` (dedupe on replay), `daily_view_cap_integration_test.go` (1 view per viewer per day, self-views excluded).
- **feed-service:** none.

### 2.3 End to end and smoke (VERIFIED-RUN / VERIFIED-READ)

- `Architecture/tools/integration/*.go` has no test that touches `/v1/videos`, `/v1/playlists`, `/v1/video-series`, `/v1/channels/*/subscribe`, progress or `m3u8`. A grep over all files finds `long_video` only as seeded rate and metrics rows in `creator_fund_test.go:58` and `fund_settlement_test.go:106`.
- `.github/workflows/e2e.yml`: the media domain starts only `api-gateway media-service` (`:57-58`). The social domain starts post-service and feed-service (`:48`) but runs no tube flow. E2E run 44008085438 at `770f5ed9d`: all 13 domains succeeded.
- `scripts/smoke/dev-smoke.mjs:429-431`: `health.longvideo` is only `GET /v1/posts/trending?content_type=long_video` with status 200. The smoke runs by hand: the only "smoke" match in the workflows is a comment at `terraform.yml:36`.

### 2.4 Android unit (VERIFIED-RUN)

- `:feature:tube`: **310 tests, 0 skip, 0 fail** (`--rerun`, BUILD SUCCESSFUL in 31 s), across 34 files. Largest: `CreatorHubNormaliseTest` 32, `CreatorHubViewModelTest` 21, `CollectionViewModelTest` 20, `WatchPlaybackTest` 18, `HistoryViewModelTest` 17, `WatchEngagementTest` 16, `WatchElementsTest` 15, `CollectionsTest` 14. Watch-player state is tested through `FakeWatchPlayer`.
- `:core:feed`: **689 tests, 0 skip, 0 fail**. Gradle reported the task UP-TO-DATE, which means a cached result for identical inputs.
- Not run here: `:core:media` (`ResumableUploaderTest`, `HlsRefreshTest`), `:feature:post` (`ReelPublishPipelineTest`, which covers `long_video`), and `app` (`PushTargetsTest:16-30` routes `creator_uploaded_video` with `/tube/watch/` and `/posttube/watch/`).
- Instrumented: none (`git ls-files | grep -c src/androidTest` = 0).

### 2.5 Web unit (VERIFIED-RUN)

- `bun test src/features/posttube src/app/posttube src/features/upload`: **818 pass, 0 fail, 56 files, 2.46 s**.
- 15 of the 53 posttube/upload test files call `readFileSync`, which reads CSS, component source or fixtures. For example, `watch/__tests__/presentation.test.tsx:17-18` reads `watch.css` and `tube-player.css`. Those assertions check text, not behaviour (SUSPECTED, per file).
- No test mounts `TubePlayer` or the watch page's progress loop. Progress is covered only as pure functions in `features/posttube/__tests__/model.test.ts:28-80` (`normalizeProgressRow`, `resumePositionMs`).

## 3. Journeys: which test covers which step

| Journey step | Covering tests | Kind | Gap |
|---|---|---|---|
| **J1 Upload a long video** (resumable, at least 100 MiB on Android, at least 20 MB on web) | Android `ResumableUploaderTest` (fake server); web `lib/resumableUpload` tests (slicing, retry); media `resumable_chunk_plan_test`, `resumable_ratelimit_test`, `resumable_video_cap_test` | behaviour, but client-side or init-only | server part, status and complete untested (reels-13) |
| J1 transcode on the long class | `transcode_classes_test.go:160` (short consumer forwards long requests to a fake writer), `:199` (forward failure transcodes in place); `video_hls_test.go:15,25` (long form keeps the full ladder, capped at the source height); `TestLongFormRenditionsUseVeryfast` | behaviour on argument building, with fakes | no real encode of a landscape or more-than-600 s file (reels-12); `media.events.long` never exercised against a broker (LV-F-8) |
| J1 publish | post `create_post_handler` route tests; `mtube_routes_test.go:270` (invalid long category 422); `end_screens_integration_test.go:506` (publish keeps visibility); Android `ReelPublishPipelineTest` | behaviour (route + store) | no test joins media "ready" to post publish to feed and search for a long video |
| J1 play at each quality on a second account | media `delivery/authz_test.go` (fake authorizer); post `media_access_anonymous_integration_test.go`; `hls_test.go:339-398` (master bandwidth); Android `WatchPlaybackTest` (re-prepare, retry; fake player) | behaviour, but each half alone | **no test fetches `master.m3u8` as user B and checks the 360/720/1080 variants; no Android or web test of quality selection** (LV-F-1, LV-F-10) |
| **J2 Leave mid-video, resume on another device** | post `tube_audit_test.go:673` (save survives a Redis failure), `tube_audit_integration_test.go:211` (stale progress not applied), `tube_audit_routes_test.go:232` (clamp `client_sent_at`), `read_gate_routes_test.go:48-49` (progress POST and GET run the detail gate); Android `ResumeRuleTest` (pure), `WatchProgressPauseTest`; web `model.test.ts:66` (pure) | server behaviour at the store level; client rules pure | **no test writes progress from client A and reads it as resume on client B; no client test of the 10 s save cadence or of applying the resume at prepare** (LV-F-3) |
| **J3 Make a public video private; an open player and a copied URL stop** | post `media_access_policy_test.go:14`, `media_access_anonymous_integration_test.go:116-118`; `mtube_integration_test.go:509` (visibility edit bumps search revision); feed `events/visibility_changed_test`; search `TestEligibility_DeletionAndVisibilityDowngradeRemove`; Android `HlsRefresh` re-prepare on 403 | behaviour per component | **no test changes visibility and then asks for the manifest or a segment; `sign_cache_test.go:127 TestHLSSegmentTTL` asserts the long segment TTL, so the test pins the gap in place** (LV-F-2) |
| **J4 Create a playlist, reorder, share** | post `tube_audit_test.go:577,606`, `tube_audit_integration_test.go:48,98,135` (concurrent add and move race), `playlist_routes_test.go:77` (move to position 0), `playlist_visibility_test.go:118` (an unlisted playlist can be fetched but is not listed), `video_authoring_authz_test.go:178,218,260`; web `library/__tests__/reorder.test.ts`; Android `CollectionViewModelTest` (20) | behaviour | sharing is covered only as "unlisted fetch allowed". No test opens a shared link signed out (the web route needs sign-in for `/posttube/playlists`). Minor |
| **J5 Subscribe and receive the next upload** | post `channel_subscriptions_integration_test.go:199,256`, `channel_subscriptions_handler_test.go:63`; feed fan-out subscriber leg (`TestFanoutPost_CelebLongVideoReachesSubscribers`, `…SubscriberLegOnlyForLongVideosWithAChannel`), `watch_subscribed_test.go` (fail closed); notification `subscriber_fanout_test.go:30` (enqueue gating table, including unlisted, private and opt-out), `service/subscriber_fanout_test.go:179-387` (paging, failed recipient blocks cursor, self exclusion); Android `PushTargetsTest`, `WatchSubscribeTest` | behaviour per service | **no cross-service test; delivery-time eligibility for upload jobs is untested** (LV-F-6); the feed-service leg is not in CI |
| **J6 Kill the worker mid-transcode; confirm recovery** | media `transcode_lease_test.go` (heartbeat, busy tracker, superseded delivery, sweep kill switch), `transcode_lease_integration_test.go` (sweep selection, give up, SKIP LOCKED, no requeue of in-flight work), `transcode_durability_integration_test.go:134` (duplicate workers serialize), `transcode_guards_test.go` (retry until durable, quarantine) | behaviour at SQL and loop level | **no process-level test: no worker is killed while ffmpeg runs, and none checks that the sweeper requeues the job and that it completes once on the long loop** (LV-F-13) |

## 4. Specific checks: would a test fail if the rule broke?

| Rule | Test that would fail | Verdict |
|---|---|---|
| Unlisted absent from feeds | feed `listingvisibility_test.go:16,53` | **yes** (VERIFIED-READ) |
| Unlisted absent from search | `shared/events/search_eligibility_test.go:43`; search `eligibility_test.go:382,577`; `tube_test.go:146,194` (collections) | **yes** |
| Unlisted absent from channel listings, playlists and series | post `tube_decisions_test.go:21,46,82,136`, `tube_decisions_integration_test.go:22,93`, `tube_decisions_routes_test.go:101` | **yes** |
| Unlisted absent from RSS | post `channel_feed_test`, `channel_feed_integration_test.go` (4 unlisted cases), web `feed.xml/__tests__/route.test.ts` | **yes** |
| Unlisted absent from the sitemap | none. The sitemap lists only the root (`src/app/sitemap.ts:5-13`) | no test, nothing to break today |
| Unlisted absent from trending, categories, topics, appearances, end-screen latest/popular, `/v1/posts/batch` | `feed_reads_integration_test.go:54-63` seeds only `public` rows. `feed_reads_guard_test.go:58` greps the printed source of `trendingQuery` for `ViewerEligibleSQL`, not for the `visibility = 'public'` clause (`saved.go:282`) | **no** (LV-F-5) |
| Visibility at the manifest | media `delivery/authz_test.go:97-148,184-213` (fail closed, fake authorizer); post `media_access_anonymous_integration_test.go:116-118` | yes per half; **no joined test** (LV-F-2) |
| Visibility at the segment | none; `sign_cache_test.go:127` asserts the long TTL | **no** (LV-F-2) |
| Only the owner can edit or change visibility | post `post_edit_test.go:147 TestUpdatePostIsOwnerOnly`, `mtube_integration_test.go:509` (store refuses a stranger with `ErrPostEditNotOwned`), `post_edit_test.go:252` (category), `video_authoring_authz_test.go:104-169` (fail closed) | **yes** |
| Only the owner can delete | `delete_post_owner_integration_test.go:27`, `hub_batch_test.go:277` | **yes** |
| Only the post author can pin or heart a comment | `service/comment_creator_test.go:64` | **yes** |
| Watch progress owned by the caller | `watch_history_integration_test.go:178` (clear removes only the viewer's rows); progress is keyed on `X-User-Id` (`watch_history_routes_test.go:36`, identity required) | **yes** for clear and identity. No test reads user A's progress as user B (low risk, since the key is the caller) |
| Watch progress throttled | none, and no limit exists beyond the edge | **no** (LV-F-4) |
| Progress `client_sent_at` clamp and ordering | `tube_audit_routes_test.go:232`, `tube_audit_integration_test.go:211` | **yes** |
| View counting (long form needs at least 30 s; server duration) | analytics `model/view_rules_test.go:36`, `ingest_duration_test.go:17,94` | **yes** |
| View replay protection | `ingest_full_model_integration_test.go:22`, `ingest_integration_test.go:16`, `sessions_integration_test.go:195,262`, `daily_view_cap_integration_test.go:26,210` | **yes** |
| Failed processing retried without duplicates | `transcode_guards_test.go` (retry, quarantine, offset not committed), `transcode_lease_integration_test.go:358,413`, `transcode_durability_integration_test.go:51,134` | **yes** at SQL and loop level; not with a real encode |
| Abandoned multipart cleanup | `reels_hardening_integration_test.go:179` (store closes expired sessions once). **Nothing tests `service/deletion.go:216-231` calling `AbortMultipartUpload`**, the user-cancel path, or the bucket lifecycle rule | **partial** (LV-F-7) |
| Per-user quota | none: no quota exists (`00-map.md` 7). Only the count budget is tested (`resumable_ratelimit_test.go:15,35`) | **no** (LV-F-9) |
| Scheduled video private until it fires | `media_access_policy_test.go:53-54`, `scheduled_integration_test.go:97,215`, `postschedule/worker_test.go:81,110` | **yes** |
| Retired user-service subscribe routes stay 410 | `user-service/…/retired_subscription_routes_test.go:17,52` | **yes** |

## 5. CI state (VERIFIED-RUN)

| Workflow | Runs these tests | Last run on `codex/module-01-02-launch-safety` | Last run on `main` |
|---|---|---|---|
| Integration (all Go services) `integration-all.yml` | post, media, search, notification, analytics, user (unit + integration, `go.sh --full --db`); **not feed-service, not api-gateway** | 44008075845 @ `770f5ed9d`, 17 Nov, **success** (all 33 module jobs). Push trigger is `codex/**`, `feat/**`, `main` on Go/SQL paths (`:21-37`) | no run in the last 20 `main` runs |
| E2E `e2e.yml` | no long-video flow | 44008085438 @ `770f5ed9d`, **success**; earlier runs on 16 Nov failed (for example 43994563478) | none listed |
| Go CI `go-ci.yml` | every module, including feed-service | 41168872400 @ `44f00fafe`, 31 Oct, success | 32955368438 @ `adad00644`, 26 Aug, **failure** |
| Integration (OpenSearch) | search-service tube and eligibility tests | 30 Oct @ `b04cca3ad`, success | 26 Aug, success |
| Android `android-native.yml` | `:feature:tube`, `:core:feed`, … | last runs on `…-9erjug` (40891364208, 29 Oct) **failure** before Gradle | Aug, failure |
| Web | none (no `.github` in `pb-echo`) | — | — |
| `main`, overall | — | — | the latest `main` runs are the weekly "Security" workflow, failing every week since 30 Aug at `adad00644` |

HEAD `7765e81a7` has no CI run of its own. It differs from `770f5ed9d` only in `docs/` (19 files).

## 6. Findings

### LV-F-1
- **ID:** LV-F-1
- **Title:** No test at any layer uploads a long video, sends it through the long transcode class, publishes it and plays it at each HLS quality on a second account.
- **Severity:** P1
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (repository search, e2e job list, smoke read)
- **Evidence:** `tools/integration` has no `/v1/videos`, `m3u8` or long-video flow (section 2.3). `e2e.yml:57-58`: the media domain has no worker. Smoke `health.longvideo` is only a trending GET (`dev-smoke.mjs:429-431`); its upload step uses a 2 s flick (`00-map.md` 10). The long-class tests use a fake writer (`transcode_classes_test.go:160,199`). The ladder tests check argument building only (`video_hls_test.go:15,25`).
- **How to reproduce:** `grep -lE "/v1/videos|m3u8|long_video" Architecture/tools/integration/*.go`; `gh run view 44008085438 --json jobs`.
- **Impact:** The long-video core journey (classifier boundary, forward to `media.events.long`, 1080p/4K rungs, the review gate, the delivery gate for a second viewer) can break while every CI job stays green.
- **Fix (test to add):** e2e domain `longvideo`: api-gateway, media-service, media-worker with ffmpeg and `MEDIA_TRANSCODE_CLASSES=short,long`, post-service, feed-service, Redpanda with `media.events.long` created. Test `TestE2E_LongVideo_UploadPublishPlay`: as user A, resumable-upload a 12 s 1280x720 landscape fixture (landscape routes to the long class); poll until ready; `POST /v1/posts content_type=long_video`. As user B, `GET /v1/media/:id/hls/master.m3u8`, assert the 360p and 720p variants and no 1080p (capped at the source), fetch one segment of each and assert 200. Signed out, on a private copy, assert 404.
- **Acceptance:** Green in CI. It goes red when the long consumer is not started (the job then transcodes in place and must still pass, so also assert the `media.events.long` consumer offset moved), or when the ladder drops 720p.
- **Effort:** M

### LV-F-2
- **ID:** LV-F-2
- **Title:** No test proves that making a video private (or deleting it, or a takedown) stops an open player or a copied stream URL, and the segment TTL test pins the gap in place.
- **Severity:** P1
- **Category:** tests / security
- **Evidence level:** VERIFIED-READ
- **Evidence:** The manifest gate is tested only with a fake authorizer (`delivery/authz_test.go:97-148`). The post-side rule is tested separately (`media_access_anonymous_integration_test.go:116-118`). `sign_cache_test.go:127 TestHLSSegmentTTL` asserts that segments get the long TTL (twice the duration plus 15 min, cap 12 h; `sign_cache.go:63-66`). Android re-prepares on 403 (`HlsRefresh`), but nothing asserts that the server ever answers 403 after a visibility change.
- **How to reproduce:** `grep -rn "private" Architecture/services/media-service/internal/delivery/*_test.go`: no test flips a post and re-requests the manifest.
- **Impact:** Open question 3 (segments outlive a visibility change by up to 12 h) has no regression test either way. A future fix could not be proven, and a regression of the manifest gate (for example a cached "yes") is not caught across the service boundary.
- **Fix (test to add):** Integration test in media-service with a real post-service HTTP authorizer stub that reads from a shared fixture. Fetch the manifest as B (200), set the post `private`, fetch again: expect 404 within the 2 min sign-cache reuse window (`sign_cache.go:54`, also covered by `TestCachedSignatureIsNeverServedWithoutAuthorization`). Add a documented-expectation test for segments that states the accepted exposure window, so that changing the TTL policy is a visible decision.
- **Acceptance:** The test fails if `URLsForAsset` serves a cached signature after the authorizer flips to deny, or if the playlist route skips the authorizer.
- **Effort:** M

### LV-F-3
- **ID:** LV-F-3
- **Title:** Resume across devices is tested only as pure rules on the clients and as store ordering on the server; no test writes progress on one client and resumes from it on another.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** Android `ResumeRuleTest` (5, pure) and `WatchProgressPauseTest` (3, repository gate). No `WatchViewModel` test drives the 10 s save cadence, the save on pause or background (`WatchViewModel.kt:416-462,820-830`), or applying the read position at prepare (`:738-744`). Web: only `model.test.ts:28-80` (pure). Nothing mounts the watch page's progress loop. Server: `tube_audit_integration_test.go:211` (stale write ignored) and `tube_audit_test.go:673` (Redis failure). No test checks that GET (Redis first, `watch_progress.go:43-79`) returns the DB value after a newer write from another client whose Redis mirror failed.
- **How to reproduce:** `grep -rln "saveProgress\|WatchProgressApi" mobile/android/feature/tube/src/test` finds only the pause test.
- **Impact:** A regression in the client cadence, a resume ignored at prepare, or a stale Redis mirror served on device B would not be caught. The web and Android completion thresholds already differ (90% and 95%; `00-map.md` question 8), and no test pins either value end to end.
- **Fix (test to add):** (a) An Android `WatchViewModel` test with a fake clock and `FakeWatchPlayer`: play for 25 s, assert 2 saves; pause, assert a save; reopen with a saved position of 120 s, assert a seek to 120 000 ms before play. (b) A web test of the progress hook with fake timers. (c) A post-service test: save at t1 (Redis ok), save at t2 with Redis set to fail, then GET must return the t2 position (or the mirror must be deleted on a failed write).
- **Acceptance:** All three are green, and each fails when its code path is removed.
- **Effort:** M

### LV-F-4
- **ID:** LV-F-4
- **Title:** Watch-progress writes have no server throttle, so no test can fail on progress flooding.
- **Severity:** P2
- **Category:** tests / performance
- **Evidence level:** VERIFIED-READ
- **Evidence:** `00-map.md` 2.1 (`/v1/videos/:videoId/progress`, edge only). No test file mentions a progress rate limit (grep `progress` in post-service `*_test.go` finds `read_gate_routes_test`, `tube_audit_*` and `watch_progress_test` only; none counts calls).
- **How to reproduce:** `grep -rn "progress" --include=*_test.go Architecture/services/post-service | grep -i "limit\|rate\|429"` returns nothing.
- **Impact:** Each write does a readability check, a DB upsert and a Redis mirror. One user can send up to the 60 rps edge limit per video, and nothing would show a regression in any limit added later.
- **Fix (test to add):** After a per-user, per-video limit is added (for example 1 write per 2 s, burst 3), a route test: 5 POSTs within 1 s give 3 accepted and the rest 429 (or coalesced 204). Assert that the upsert ran only 3 times.
- **Acceptance:** The test fails if the limiter is removed.
- **Effort:** S (test), M (with the limiter)

### LV-F-5
- **ID:** LV-F-5
- **Title:** Unlisted exclusion from trending, categories, topics, creator appearances, end-screen targets and `/v1/posts/batch` is not asserted by any test; the trending guard reads source text.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `feed_reads_integration_test.go:54-63` seeds only `public` rows with different review states. `feed_reads_guard_test.go:19,58` checks the printed source of `trendingQuery` for `ViewerEligibleSQL("p")`, but not for `p.visibility = 'public'` (`saved.go:282`). `trending_query_test.go` and `trending_shape_test.go` never mention visibility. `batch_visibility_test.go:20` covers the edge rules where `unlisted` is admitted by design (`00-map.md` question 4).
- **How to reproduce:** `grep -n "unlisted\|visibility" Architecture/services/post-service/internal/store/postgres/trending_query_test.go Architecture/services/post-service/internal/store/postgres/feed_reads_integration_test.go`: no unlisted seed.
- **Impact:** Deleting or loosening the `visibility = 'public'` clause in trending (or in the category, topic and appearances reads) would leak unlisted long videos to every viewer with CI green.
- **Fix (test to add):** Extend `feed_reads_integration_test` with `it.seed("unlisted","approved","long_video")`, `private` and `followers` rows. Assert they are absent from `GetTrendingPosts([long_video])`, the categories listing, `/v1/creators/:id/appearances` and the end-screen "latest" and "popular" targets.
- **Acceptance:** The test fails when `"p.visibility = 'public'"` is removed from `trendingQuery`.
- **Effort:** S

### LV-F-6
- **ID:** LV-F-6
- **Title:** The new-upload notification fan-out is tested at enqueue time only; the delivery-time eligibility check for upload jobs has no test.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `events/subscriber_fanout_test.go:30-70` tests enqueue gating (unlisted, private, opt-out, content types). `service/fanout_eligibility.go:62` (`eligible`) and `:170` (`postStateFor`) are tested only for live jobs (`live_fanout_test.go:434-494`). No upload-job case covers: post made private or deleted after enqueue; empty `review_status` treated as approved (`fanout_eligibility.go:202`); a members-only upload; a blocked recipient.
- **How to reproduce:** `grep -n "eligible(\|postStateFor" Architecture/services/notification-service/internal/service/*_test.go`: only live callers.
- **Impact:** A video made private or taken down between upload and the 5 s drain still notifies every subscriber, and changes to the per-recipient rule go uncaught.
- **Fix (test to add):** `TestUploadEligible` table using `httptest` post and graph servers: public approved gives true; private, deleted, rejected or pending gives false; empty review gives the documented answer; post-service 5xx gives an error (retry, never true).
- **Acceptance:** The test fails if `eligible` returns true without consulting the post state for upload jobs.
- **Effort:** S

### LV-F-7
- **ID:** LV-F-7
- **Title:** Abandoned multipart cleanup is tested only at the store (sessions closed once); the abort call, the retry on abort failure and the user-cancel path are untested.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `reels_hardening_integration_test.go:179` (`ExpireResumableUploads` closes once). `service/deletion.go:216-231` (`expireResumableUploads` → `AbortMultipartUpload`, best-effort) has no test. `grep -rln AbortMultipart --include=*_test.go` finds only that integration file, which does not call the service. The bucket rule (`infra/terraform/modules/media/main.tf:169-176`) has no plan or policy test.
- **How to reproduce:** The grep above.
- **Impact:** For 16 GiB long uploads, a regression that stops aborting leaves parts billed for up to 7 days (the lifecycle rule), or forever if the rule is dropped, with CI green.
- **Fix (test to add):** A service unit test with a fake blob store: two expired sessions, abort fails for one. Assert that both aborts were attempted, the failed one is logged or counted, and (if retry is intended) it comes back on the next tick. Add a cancel-path test: `aborted` status leads to an immediate abort. Add a terraform check (`terraform show -json` on the plan, or a conftest rule) that `abort-incomplete-multipart` exists.
- **Acceptance:** The tests fail when the abort call is removed from `expireResumableUploads`.
- **Effort:** S

### LV-F-8
- **ID:** LV-F-8
- **Title:** No test or check ties the topics the code produces to the MSK topic list, so the missing `media.events.long` topic was not caught.
- **Severity:** P2
- **Category:** tests / config
- **Evidence level:** VERIFIED-READ
- **Evidence:** `media.events.long` appears in `Architecture/docker/init-topics.sh`, `docker-compose.yml`, `deploy/services/media-service/values-prod.yaml` and `transcode_classes_test.go`, but not in `infra/terraform/modules/msk/topics-job/variables.tf:30-59` (`00-map.md` 4). Auto-create is off (`modules/msk/main.tf:147`). No test compares these lists.
- **How to reproduce:** `grep -rln "media.events.long" --include=*.tf .` returns nothing.
- **Impact:** In prod every long video takes the forward-failure path and transcodes in place on the short loop (`transcode_classes.go:214-220`), so shorts can queue behind multi-hour encodes. Only a fake writer test (`:199`) knows about this path.
- **Fix (test to add):** A Go test in `Architecture/shared` (or a CI script) that collects every `KAFKA_*TOPIC` default and topic literal used by the producers and asserts each one is in `topics-job/variables.tf` and `init-topics.sh`.
- **Acceptance:** Fails today; passes once the topic is added.
- **Effort:** S

### LV-F-9
- **ID:** LV-F-9
- **Title:** No per-user storage or transcode-minute quota exists, so no test guards one; only the upload count budget is tested.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `00-map.md` 7 (no `*QUOTA*` env, 10 per hour and 30 per day, fail-open). Tests: `resumable_ratelimit_test.go:15,35` (init counted, validated before counting) and `resumable_video_cap_test.go` (16 GiB per file). Nothing tests fail-open behaviour or cumulative bytes.
- **How to reproduce:** `grep -rni "quota" --include=*_test.go Architecture/services/media-service`: no long-video quota test.
- **Impact:** About 30 x 16 GiB per user per day, with up to 48 h of transcode budget per job, cannot be shown to be refused.
- **Fix (test to add):** Once a quota decision is taken: a test where init is refused when the user's ready plus pending bytes plus the new size exceeds the quota, and a test that pins the fail-open (or fail-closed) answer when Redis is down.
- **Acceptance:** Each test fails when its check is removed.
- **Effort:** S (test) after the product decision

### LV-F-10
- **ID:** LV-F-10
- **Title:** The Android and web players have no test of quality selection, network captions or the create-time visibility list; Android has no instrumented tests at all.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (grep, file count)
- **Evidence:** No `PlayerQuality` test in `mobile/android` (`git ls-files | grep -iE "Quality.*Test"` returns nothing; `PlayerQuality.kt:14,32-52` is untested). Network captions are not wired (`WatchViewModel.kt:909-911`), and no test pins that gap. Unlisted is missing at create (`ReelSurface.kt:1425-1429`) and no test covers the list. Web: `pickHlsLevel` is tested only under reels (`features/reels/__tests__/playerPrefs.test.ts`). `TubePlayer.tsx` is imported only for `TubeSettingsMenu` in a render test (`watch/__tests__/presentation.test.tsx:12`). `src/androidTest` count is 0.
- **How to reproduce:** As above.
- **Impact:** "Play at each quality" (J1) has no client proof. A pinned 1080p that silently falls back to Auto, or a dub that offers a broken quality list, ships green.
- **Fix (test to add):** Android `PlayerQualityTest`: tracks {360,720,1080} give the options [Auto,1080,720,360]; picking 720 sets a track override of height 720; an MP4 source gives Auto only. Web: a `TubePlayer` test with a fake hls.js levels list asserting that `currentLevel` follows the selection.
- **Acceptance:** The tests fail when the override is not applied.
- **Effort:** S

### LV-F-11
- **ID:** LV-F-11
- **Title:** `TestUnwritableQuarantineDoesNotCommit` (media worker) depends on timing and failed once under parallel load.
- **Severity:** P3
- **Category:** tests
- **Evidence level:** VERIFIED-RUN
- **Evidence:** In the first local run, `transcode_guards_test.go:95: quarantine tried 1 times; it must keep trying`. It then passed 7 times out of 7 (one `-v` run, then 6 more). The test gives `retryUntilDurable` a 50 ms context (`:88`) and needs at least 2 quarantine attempts.
- **How to reproduce:** Run `go test -count=1 ./...` in the seven services at once (CPU contention), or run the test under `-cpu 1 -count 50`.
- **Impact:** Random red integration-all jobs on a busy runner.
- **Fix (test to add):** Drive the loop with an injected clock or attempt counter instead of wall time, or raise the deadline to 500 ms and assert at least 2 attempts.
- **Acceptance:** `-count 200 -cpu 1` is green.
- **Effort:** XS

### LV-F-12
- **ID:** LV-F-12
- **Title:** Part of the web posttube suite asserts source or CSS text rather than behaviour.
- **Severity:** P3
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (count); per-assertion nature SUSPECTED
- **Evidence:** 15 of 53 posttube and upload test files call `readFileSync`. For example, `watch/__tests__/presentation.test.tsx:17-18` reads `watch.css` and `tube-player.css`, and `upload/__tests__/studioSound.test.tsx` has 11 calls. Some of these read fixtures, not source.
- **How to reproduce:** `grep -c readFileSync` over the files in section 2.5.
- **Impact:** These tests can pass while the behaviour is broken, and fail on harmless refactors. They inflate the 818-pass figure as a measure of journey coverage.
- **Fix (test to add):** Replace the source-text assertions on behaviour (player, studio publish order) with rendered-DOM or hook tests. Keep the CSS token checks where they enforce the design-token rule.
- **Acceptance:** No posttube test reads a `.tsx` or `.ts` source file.
- **Effort:** M

### LV-F-13
- **ID:** LV-F-13
- **Title:** Worker crash recovery during a long transcode is proven only at the SQL and loop level, never by killing a running worker.
- **Severity:** P2
- **Category:** tests / reliability
- **Evidence level:** VERIFIED-READ
- **Evidence:** `transcode_lease_integration_test.go:172,263,358,413` (sweep selection, give up, no double queue, no requeue of in-flight work) and `transcode_lease_test.go` (heartbeat and busy tracker, with fakes) are solid. No test runs ffmpeg, kills the process, and then watches the 5 min stall sweeper requeue and a second worker finish exactly once. Memory notes an earlier real incident of orphaned `processing` jobs (MEMORY `media-stalled-transcodes`).
- **How to reproduce:** `grep -rn "Kill\|SIGKILL\|os.Process" --include=*_test.go Architecture/services/media-service/cmd/worker` finds no crash test.
- **Impact:** A regression in heartbeat wiring (for example, a heartbeat that keeps beating after ffmpeg dies, or a lease never reclaimed on the long loop) would leave long videos stuck in `processing`.
- **Fix (test to add):** In the LV-F-1 e2e domain, `TestE2E_LongVideo_WorkerCrashRecovers`: start the transcode of a 60 s fixture, `docker kill` the worker after the first heartbeat, start a new worker with a short `MEDIA_TRANSCODE_STALL` setting, and assert the asset reaches `ready` with exactly one completion event and one set of variants.
- **Acceptance:** Green. Red when the sweeper is disabled (`TestStallSweepKillSwitch` already shows the switch exists).
- **Effort:** M

## 7. Already well tested

- **Ownership on writes:** post edit and visibility (`post_edit_test.go:147`, `mtube_integration_test.go:509`), delete (`delete_post_owner_integration_test.go:27`), playlist and series writes (`playlist_visibility_test.go:206`, `video_authoring_authz_test.go:104-218`, `tube_audit_test.go:221`, `video_series_watch_test.go:257`), comment pin and heart (`comment_creator_test.go:64`). All are behavioural, at service or store level.
- **Unlisted exclusion** from feeds, search, channel listings, playlists, series and RSS (section 4).
- **View counting and replay protection** in analytics, at the integration level.
- **Transcode durability and leases** (SKIP LOCKED, idempotent queue, quarantine, give up).
- **Scheduled publish** exactly once, with `publish_at` cleared and one PostCreated.
- **Playlist concurrency** (concurrent adds, move/add race) on real Postgres.
- **Subscriptions:** outbox in the same tx, bell-off excluded from subscriber ids, `notify_on` validation, enqueue gating table, cursor paging.
- **Search eligibility** (public + approved only; stale approval cannot resurrect; visibility downgrade removes).
- **Signed-out media rule** per clause on the real schema (`media_access_anonymous_integration_test.go:110-130`).
- **Unit volume:** post-service 2698, media 1163, feed 663, notification 755, search 489, user 303, analytics 216 pass. Android tube 310 and core feed 689 pass. Web posttube and upload 818 pass.

## 8. Not verified

- Whether each post-service and media-service integration test ran or skipped in CI: no `-v` output (section 1). Many long-video integration files use shared helpers rather than a direct `Getenv`, so their DSN source was not traced file by file.
- Whether the CI runner image has ffmpeg (reels-12 caveat).
- `:core:media`, `:feature:post` and `app` Android tests were not re-run here. The `:core:feed` result was a Gradle up-to-date cache hit.
- The web `scripts/verify.sh` and any web test outside the three folders were not run.
- Whether the e2e "social" domain exercises any long-video path implicitly (its flows were not read in full).
- The behaviour of end-screen and card impression counters under anonymous writes (open question 14): no test was looked for beyond the route list.

## 9. Notes for other tracks

- **Track B (security):** LV-F-2. The segment TTL test (`sign_cache_test.go:127`) treats a 12 h segment life as correct; decide the policy before writing the joined test. LV-F-6: delivery-time eligibility for upload notifications is unproven.
- **Track C (data):** LV-F-7 (multipart abort is best-effort and untested). There is no watch_progress retention sweeper and no test (`00-map.md` 8).
- **Track D (performance):** LV-F-4 (progress write rate), and the inherited reels-11 (no load test). There is no capacity test for a 4 h 4K encode.
- **Track E (reliability):** LV-F-8 (missing `media.events.long` topic, no config test), LV-F-13 (crash recovery), LV-F-11 (flaky timing test). The weekly `main` "Security" workflow has failed every week since 30 Aug.
- **Track A (functional):** the upload notification deep link is `/tube/watch/<id>` (`subscriber_fanout_test.go`, case "public long_video"). Android routes it correctly (`PushTargetsTest.kt:16`). On the web it relies on the `/tube/*` redirect (`src/app/tube/[[...path]]/page.tsx:20`). The completion thresholds differ: web 90%, Android 95%.
