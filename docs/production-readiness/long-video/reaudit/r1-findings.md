# Long video (PostTube / MTube): re-audit R1

- **Date:** 2026-11-17. **Code:** `codex/module-01-02-launch-safety` @ `cdb530dd5` (40 commits after the audit's `7765e81a7`). **Web:** `C:\workspace\wt\pb-echo` `feat/echo-web` @ `1b596b4` (4 commits after `ca96921`).
- **Method:** read-only. Every file was read as committed (`git show HEAD:<path>`); about 680 uncommitted files in the main tree were ignored. Commit messages were used only to find the code, never as evidence.
- **CI read:**
  - E2E run 44136948310 on `fea421626`, job 134228971157 `e2e (video)`, log downloaded and read.
  - Go CI 44139090113, Integration 44139090211, Lint 44139090011 and Security 44139089955, all on `cdb530dd5`.
  - Android Native CI 44123854344 on `f3f68c1aa`.
  - web-ci 44078366557 on `1b596b4`.
- **Run by me:** `QA_GUARDS_ONLY=1 bash charts/atpost-service/render_test.sh` on a `git archive HEAD` snapshot in the scratchpad. Nothing else was run locally. Helm is not installed.
- **Evidence levels:**
  - **VERIFIED-RUN:** I ran the check, or I read its pass line in a named CI log.
  - **VERIFIED-READ:** I read the committed code.
  - **SUSPECTED:** inferred but not confirmed.
  - **UNVERIFIED:** not checked.
- **CI caveats:**
  - **Integration-tagged tests:** the Integration workflow logs only `PASS integration <service>`. Its logs cannot tell a passed test from a skipped one (reels-70, still open), and it uploads no artefacts. These tests are marked "CI Integration job PASS, per-test outcome not logged".
  - **Go unit tests:** Go CI logs per-service `PASS test <service>` (no `-v`), except for the real-ffmpeg job, which names every test. A unit test with no build tag is marked "ran in Go CI (`PASS test <svc>`)".

## 1. Findings

Status key:
- **FIXED:** the code path is closed end to end, and a test that runs somewhere would fail if it reopened.
- **PARTLY FIXED:** some of the finding is fixed and the rest is listed in the "What remains" column.
- **OPEN:** nothing has changed.

### 1a. New long-video P1s

| ID | Status | Commit(s) | Evidence at HEAD | Pinning test (layer) | Ran in CI? | Acceptance / what remains |
|---|---|---|---|---|---|---|
| lv-1 subscriber not notified | **FIXED** | 3f2fe1e4e, 677ccaed3, 8ed473d16 | <ul><li>`notification-service/internal/service/fanout_eligibility.go:309-424`: the post is read as its author; `classifyUpload` (`:352`) returns ready, wait or skip; a 404 counts as gone only when `confirmGone` (moderation subject) agrees.</li><li>`store/postgres/fanout_jobs.go:69-96`: a later trigger wakes a waiting job or reopens a failed one, one row per post.</li><li>`events/consumer.go:278`: enqueues on `PostSearchEligibilityChanged`.</li><li>The wait ceiling is about 72 h (migration 021).</li></ul> | <ul><li>`TestUploadFanout_ProcessingAtCreateNotifiedOnceWhenReady` and 13 more in `fanout_upload_visibility_test.go` (unit).</li><li>`TestEnqueueOnVisible_Gating`, `TestUploadTriggers_ShareOneJobKey` (unit).</li><li>`TestFanoutJobsIntegration_UploadWaitWakeAndExactlyOnce` (DB).</li><li>E2E `…/subscriber_notified_after_processing_(lv-1)`.</li></ul> | <ul><li>Unit: Go CI `PASS test notification-service`.</li><li>DB: Integration PASS, per-test outcome not logged.</li><li>**E2E: VERIFIED-RUN** (`--- PASS`, log "lv-1 asserted: one creator_uploaded_video for V1"); `ATPOST_E2E_KNOWN_OPEN` was empty.</li></ul> | <ul><li>Acceptance met.</li><li>3f2fe1e4e was committed with a mutation (`if false`) in the creator opt-out check; 677ccaed3 restored it. I scanned the whole fix-round diff for other `if false`, `if true`, `\|\| true` and `&& false` lines: none.</li></ul> |
| lv-2 segment URLs live up to 12 h | **FIXED** (server, dev signer); CloudFront path VERIFIED-READ only | 6c1d48300, b9a4fdaea | <ul><li>`media-service/internal/delivery/sign_cache.go:66-71`: default 10 min, range 1 to 15 min whatever the length; `segmentWindow` at `:130-138`.</li><li>`deploy/services/media-service/values-prod.yaml:93`: `MEDIA_HLS_SEGMENT_TTL: 10m`.</li></ul> | <ul><li>`TestHLSSegmentWindowIsShortAndLengthIndependent`, `TestFourHourPlaylistIsShortLivedAndSignedOnce`, `TestPlaylistRefetchAfterRevocationIsRefused`, `TestSegmentDirGrantRule` (unit).</li><li>E2E `…/issued_segment_URL_stops_within_the_window_(lv-2)`.</li></ul> | <ul><li>Unit: Go CI `PASS test media-service`.</li><li>**E2E: VERIFIED-RUN**, log: "lv-2 holds: segment URL issued at play time expires 30s later, window 1m0s; fetched after expiry: HTTP 403".</li><li>The tier sets `MEDIA_HLS_SEGMENT_TTL: 1m` and uses MinIO, not CloudFront.</li></ul> | <ul><li>The web 403-refresh half is in lv-27.</li><li>The CloudFront wildcard custom-policy signer has never been exercised against a real distribution; see section 3.</li></ul> |
| lv-3 no per-user quota; limiter fails open | **FIXED** (numbers await the founder) | 058a97e30, caa92a660 | <ul><li>`service/ratelimit.go:27-79`: fails closed (`ErrUploadLimiterUnavailable`).</li><li>`service/upload_quota.go:186-260`: daily bytes and seconds plus an in-flight long cap; a ledger error is refused (`:262-270`).</li><li>Prod byte cap is explicit: `values-prod.yaml:121` = 16 GiB.</li><li>Code defaults are 32 GiB, 8 h and 2 in flight (`service/limits.go:58-59`).</li></ul> | <ul><li>`TestUploadRateLimitFailsClosedOnRedisError`, `TestVideoBudgetDecision`, `TestAdmitVideoAtConfirm`, `TestUploadLimitErrorsAnswerTheirCodes` (unit).</li><li>`TestLiveUploadQuotaLedger` (DB).</li></ul> | <ul><li>Unit: Go CI PASS.</li><li>DB: Integration PASS, not logged per test.</li></ul> | <ul><li>Acceptance met.</li><li>The in-flight long cap applies at init only when the client declares `duration_ms`, which neither client does (see lv-30). It also applies at confirm.</li></ul> |
| lv-4 two long lanes in the reel loop | **PARTLY FIXED** | caa92a660, d8461772e, 8f72fcd9e | <ul><li>`values-prod.yaml:204-251`: short worker 6 replicas and long worker 2 replicas, separate Deployments, `MEDIA_TRANSCODE_CLASSES` per class, long CPU 3/4 and 80Gi scratch.</li><li>`internal/processing/threads.go`: `-threads` follows the container limit.</li></ul> | <ul><li>`render_test.sh:534-545` (render).</li><li>`TestFFmpegThreadsFollowTheContainerLimit` (unit).</li></ul> | <ul><li>**The render test runs in no CI workflow** (no `.github` file names `render_test`, `helm` or `charts/`), and helm is not installed locally. The commit message's "render_test.sh runs in CI" is false.</li><li>The thread test: Go CI PASS.</li></ul> | <ul><li>The chart split has never been rendered anywhere.</li><li>L5 has not been run.</li></ul> |
| lv-5 every disruption restarts a long job from zero | **PARTLY FIXED** | 8f72fcd9e, caa92a660 | <ul><li>`cmd/worker/rungs.go`: each rung is uploaded with a marker and reused.</li><li>`cmd/worker/drain.go`: SIGTERM takes no new work and finishes the rung in hand.</li><li>`values-prod.yaml:187`: `karpenter.sh/do-not-disrupt`.</li><li>`templates/worker-pdb.yaml`: a worker-only PDB.</li><li>`worker-deployment.yaml:73`: still `Recreate`; the long grace period is 3600 s.</li></ul> | <ul><li>`TestReRunReusesEveryFinishedUnit`, `TestInterruptedRunResumesFromTheFirstMissingRung`, `TestSIGTERMStopsNewWorkButNotTheJobInHand` (unit).</li><li>The annotation is asserted only by `render_test.sh:536`.</li></ul> | <ul><li>Unit: Go CI PASS.</li><li>Render: never run.</li></ul> | <ul><li>No runbook step checks for in-flight long jobs (grep of `docs/runbooks`: none).</li><li>No QA consolidation check.</li><li>A rung longer than 1 h is still lost on a rollout.</li></ul> |
| lv-6 crash, OOM or eviction loops for ever | **FIXED** | 8f72fcd9e | <ul><li>`cmd/worker/delivery.go:13-31,60-110`: a start is recorded before any work and a finish when the run returns.</li><li>At `MEDIA_TRANSCODE_MAX_LOST_WORKERS` (3) the request is recorded failed with a creator-visible `pipeline` row.</li><li>Migration 033.</li></ul> | <ul><li>`TestLostWorkersPastTheCapAreRecordedFailed`, `TestUncountedStartRunsNothing` (unit).</li><li>`TestTranscodeDeliveryCountsStartsFinishesAndResetsPerRequest` (DB).</li></ul> | <ul><li>Unit: Go CI PASS.</li><li>DB: Integration, not logged per test.</li></ul> | <ul><li>Unit acceptance met.</li><li>No worker-kill run exists at any layer (journey 6).</li></ul> |
| lv-7 a stranger can block erasure | **FIXED** | 8146a8767, 8ed473d16 | <ul><li>Post-service refuses a foreign `channel_id` with 403 `NOT_OWN_CHANNEL` (`http/video_series_handler.go:57,272`; `service/channel_ref_authz.go`).</li><li>The trailer must be the creator's own video.</li><li>`user-service/internal/store/purge.go:82-84` clears both references before `DELETE FROM channels`.</li><li>Migration 086 sets `ON DELETE SET NULL` and clears existing foreign references.</li></ul> | <ul><li>`TestCreateVideoSeriesRefusesAStrangersChannel`, `TestCreatePlaylistRefusesAStrangersChannel`, `TestSeriesTrailerMustBeTheCreatorsOwnVideo`, `TestNotOwnChannelMapsTo403` (unit).</li><li>`TestPurgeIT_StrangersSeriesAndPlaylistDoNotBlockTheErasure` (DB).</li></ul> | <ul><li>Unit: Go CI PASS.</li><li>DB: Integration `PASS integration user-service`, not logged per test.</li></ul> | <ul><li>Met.</li><li>The client `channel_id` is refused, not ignored; that is equivalent.</li></ul> |
| lv-8 web caption upload always fails | **FIXED** (web and server) | web 5d54a0a; server 5bee3ff8a | <ul><li>Web `src/features/posttube/data/captionUpload.ts:7-11,87-90` sends `content` and never `content_url`.</li><li>The studio surfaces a failure (`components/PublishProblems.tsx`).</li><li>The server now parses VTT and SRT. It used to store a file as one cue spanning the whole video, a second defect found and fixed here.</li></ul> | <ul><li>`launchSafety.test.tsx` "a .vtt goes as `content`, never `content_url`…" and "a 400 from media-service reaches the studio state…" (web unit).</li><li>`TestCaptionUploadVTTRoundTripsWithTimings` (Go unit).</li></ul> | <ul><li>web-ci 44078366557: 5,830 pass, 0 fail; the file is listed.</li><li>Go CI PASS.</li></ul> | The manual dev upload in the acceptance has not been done. |
| lv-9 18+ not part of publishing | **FIXED** | a8a90d515; web 5d54a0a; Android f8cc49701 | <ul><li>`http/create_post_handler.go:167-173,569` stores `age_restricted` in the create insert; the draft route does too.</li><li>Web and Android send it at create. If it is not echoed, they PATCH, then make the video private.</li></ul> | <ul><li>`TestCreatePostStoresAgeRestrictionInTheCreateWrite`, `TestPostDraftCarriesAgeRestriction` (unit).</li><li>Web `launchSafety` "18+ in the create request (lv-9)".</li><li>Android `ReelPublishPipelineTest`, `AgeRestrictionGuardTest`.</li></ul> | <ul><li>Go CI PASS.</li><li>web-ci PASS.</li><li>Android CI `:feature:post:testDebugUnitTest` ran.</li></ul> | Met. |
| lv-10 no long-video e2e | **FIXED** | 9fd89fc1a, 139891ba9, d83616ad6, be57a46bf | `Architecture/tools/integration/video_long_e2e_test.go`: two 605 s 1280x720 clips on real ffmpeg, a resumable upload, the long class, the review gate, every rung played by B, then private. | E2E `TestE2E_Video_LongVideo_UploadRoutePublishPlay` | **VERIFIED-RUN**: `--- PASS: TestE2E_Video_LongVideo_UploadRoutePublishPlay (128.25s)`. | <ul><li>Met: the forward line is asserted from the service log, the ladder is asserted, and the notification is asserted.</li><li>One subtest SKIPs (search; see section 2).</li><li>Cosmetic: on success `trackedCheck` logs the failure text ("lv-1 holds: … got no … notification").</li></ul> |
| lv-11 no long-video load test | **PARTLY FIXED** (scripts only) | b4db71b3a | `Architecture/tools/loadtest/long-video/l1,l2,l3,l4,l5,l7,l8,l9*.js`. | k6 (load) | Never run: no workflow mentions k6, and there is no results file. | <ul><li>L6 (16 GiB uploads) is not written.</li><li>No results file exists in this folder.</li><li>The acceptance needs measured results.</li></ul> |

### 1b. Inherited reels P1s cited by the long-video gate

| ID | Status | Commit(s) | Evidence at HEAD | Pinning test | Ran in CI? | What remains |
|---|---|---|---|---|---|---|
| reels-1 post pending for ever | **FIXED** | 8ed473d16 | <ul><li>`post-service/internal/consumers/media.go:388`: `finalizeReviewGate` returns errors, which are retried and then dead-lettered.</li><li>`internal/videogate` sweeper, wired in `cmd/server/main.go` (5 min).</li><li>`video_metadata` is written in the create transaction.</li></ul> | <ul><li>`TestReviewGateFlipFailureIsRetried`, `TestSweepResolvesPendingVideoPosts` (unit).</li><li>`TestVideoGate_*` (DB).</li><li>E2E "hidden before approval", then playable.</li></ul> | <ul><li>Go CI PASS.</li><li>E2E VERIFIED-RUN.</li></ul> | none |
| reels-2 web HLS sends credentials to the CDN | **PARTLY FIXED** | web c645048 | `src/features/reels/playback/hlsAuth.ts`: credentials go only to API playlists; CDN segments go without; `TubePlayer.tsx:42-43,370-373` uses it. | `hlsAuth.test.ts` "xhrSetup sets withCredentials per request…" (web unit) | web-ci PASS | The staging check (CDN `.ts` 200 in Chrome and Safari) has not been run. The commit itself says "Not checked in a browser or against a real CDN". |
| reels-3 `media.events.long` not in MSK | **FIXED** in the repo; operator step pending | d8461772e | `infra/terraform/modules/msk/topics-job/variables.tf` plus the `scripts/ci/kafka-topics` guard. | Go CI job "Kafka topics provisioned" | **VERIFIED-RUN**: "PASS kafka-topics all 29 topics named in code are in …variables.tf". | The topics Job has to be re-run in prod (operator). |
| reels-4 two workers, no capacity | **PARTLY FIXED** | caa92a660 | `values-prod.yaml:209`: 6 short replicas, matching the 6 partitions. | `render_test.sh` (render) | Never run | Render never executed. There is no replicas-versus-partitions guard. |
| reels-5 pipeline unmonitored | **PARTLY FIXED** | caa92a660, 8f72fcd9e | <ul><li>`templates/worker-podmonitor.yaml`.</li><li>Rules at `values-prod.yaml:261-345`, routed to `ops-email` (`infra/terraform/modules/observability/alerting.tf:50-95`).</li><li>Counters `media_transcode_starts_total` and `media_transcode_quarantined_total`.</li></ul> | <ul><li>`render_test.sh:537-538` (render).</li><li>`TestAlertedCountersArePreCreated` (unit).</li></ul> | <ul><li>Render never run.</li><li>Unit: Go CI PASS.</li></ul> | <ul><li>Render not executed.</li><li>The SNS e-mail subscription has to be confirmed.</li><li>New defect N-1: the per-class queue gauges carry a pipeline-wide value.</li></ul> |
| reels-6 bare `X-User-Id` trusted | **PARTLY FIXED** | b9a4fdaea | `media-service/internal/http/trusted_identity.go:41`: the header is kept only with the internal key; otherwise the viewer comes from a verified token. | `TestBareUserIDHeaderIsAnonymousOnReads`, `TestServerInstallsTrustedViewerIdentityBeforeRoutes` (unit) | Go CI PASS | The NetworkPolicy half is open: `charts/atpost-service/values.yaml:405-406` keeps `networkPolicy.enabled: false`; media prod values do not enable it; there is no EKS network-policy addon in Terraform. |
| reels-7 views route has no owner check | **FIXED** | 99ebbd656 | `analytics-service/internal/http/handler.go`: the content views route is owner or admin only. | `TestContentViewsStrangerCannotTellContentApart`, `TestContentViewsOwnerAndAdminRead` | Go CI PASS | none |
| reels-9 tests not in CI at the release commit | **FIXED** | b46d271bb, web fa397d7 | <ul><li>Go CI, Lint and Security run on `codex/**`.</li><li>Android CI has a placeholder `google-services.json`.</li><li>web-ci exists.</li></ul> | the workflows themselves | **VERIFIED-RUN**: Go CI `PASS test feed-service`; Android `:feature:tube:testDebugUnitTest` ran; web-ci "Ran 5930 tests … 0 fail". | <ul><li>feed-service is still absent from `scripts/ci/integration-services.yaml`; its long-video leg runs in the e2e video tier instead.</li><li>The weekly scheduled Security run uses main's stale copy (`security.yml:7-9`).</li></ul> |
| reels-10 no upload-to-play e2e | **FIXED** | 9fd89fc1a | `video_reels_e2e_test.go` plus the video tier in `.github/workflows/e2e.yml`. | `TestE2E_Video_Reels_UploadTranscodePublishPlay`, `…DeleteDuringProcessing`, `…RefusedUploads` | **VERIFIED-RUN**: all `--- PASS` | none |
| reels-11 no load test | **PARTLY FIXED** (scripts only) | b4db71b3a | `Architecture/tools/loadtest/reels/*.js` | k6 | Never run | No results. |
| reels-12 real ffmpeg skipped | **FIXED** | 4a5218e8c, 8f72fcd9e | `go-ci.yml:160` `REQUIRE_FFMPEG: "1"`, plus `go-test-skip-check.sh`. | `TestHLSRepackagedFromRenditionsIsPlayable`, `TestFFmpegGate` | **VERIFIED-RUN**: Go CI "media-service real ffmpeg" `--- PASS: TestHLSRepackagedFromRenditionsIsPlayable (28.67s)`. The e2e also encodes a landscape long-class clip for real. | `TestTranscodePipelineTiming` still SKIPs; it is a timing test and allowed. |
| reels-13 no server resumable test | **FIXED** | 058a97e30 | `media-service/internal/service/resumable_flow_test.go:273-277` (a missing part gives `ErrResumableIncomplete`). | 8 `TestResumable*` (unit); E2E `resumableUploadAll` | Go CI PASS; E2E VERIFIED-RUN | none |
| reels-15 HS256 accepted in prod (P2 in the reels list) | **FIXED** for AWS prod and QA | b9a4fdaea | <ul><li>`internal/http/jwt_config.go:13-35`: prod is RS256 only, requires `exp`, checks `iss`/`aud`, and refuses to start while `JWT_SECRET` is mounted.</li><li>`values-prod.yaml:52-53,87-90`.</li></ul> | `TestProductionKeySetRefusesHS256`, `TestLoadJWTKeySetProductionRefusals` | Go CI PASS | <ul><li>`values-azure-prod.yaml:29,33,41` still mounts `JWT_SECRET` with `ENV: prod`, so media-service would refuse to start there (N-4).</li><li>Staging also still mounts it.</li></ul> |

### 1c. P2s (and P3s) cited by the gate

| ID | Status | Commit(s) | Evidence / test / CI | What remains |
|---|---|---|---|---|
| lv-13 worker sizing | **PARTLY FIXED** | 8f72fcd9e, caa92a660 | <ul><li>`-threads` set.</li><li>Scratch check uses the volume's limit (`TestScratchCheckUsesTheVolumesOwnLimit`).</li><li>Each rung is deleted after upload.</li><li>Long-class scratch is 80Gi.</li><li>Go CI PASS.</li></ul> | The measured encode is not done (section 3), and the render guard is never run. |
| lv-16 24 h upload expiry | **PARTLY FIXED** | 058a97e30 | <ul><li>Sliding expiry up to 7 days (`TestResumableExpirySlidesOnActivityUpToTheCeiling`).</li><li>GC skips open sessions (`TestOrphanSweepSkipsOpenResumableSessions`).</li><li>Go CI PASS; `TestLiveResumableSlidingExpiry` (DB) is not logged per test.</li></ul> | <ul><li>The part target is still 9,000 (`service/resumable.go:28-31`), so 16 GiB is still about 3,277 parts. The acceptance's "at most 1,000 parts" fails.</li><li>Android still copies the file into app storage.</li></ul> |
| lv-17 scheduled publish mid-transcode | **FIXED** | 8ed473d16 | <ul><li>`store/postgres/scheduled.go:50` `scheduledVideoReadySQL` is applied in the list and in the guarded UPDATE.</li><li>`TestScheduledVideoReadyRule` (unit, Go CI).</li><li>`TestVideoGate_ScheduledVideoWaitsForReady` (DB, not logged per test).</li></ul> | none |
| lv-21 long-video metrics | **PARTLY FIXED** | 8f72fcd9e, caa92a660 | <ul><li>Class label on both queue gauges (`cmd/worker/readiness_metrics.go:38-50`).</li><li>Start and quarantine counters exist.</li></ul> | <ul><li>`media_transcode_duration_seconds` buckets still end at 600 s (`cmd/worker/main.go:44`).</li><li>There are no `post_schedule_publish_total` or fan-out counters (grep: none).</li><li>The gauges' value is pipeline-wide (N-1).</li><li>The render guard is never run.</li></ul> |
| lv-27 web restarts at 0:00 | **PARTLY FIXED** | web c645048 | <ul><li>`TubePlayer.tsx:370-395` uses `SignedPlaylistRefresher`: refresh about 30 s before `X-HLS-Expires-At` or on a segment 403; a refused refresh shows "unavailable"; other fatal errors fall back at the same position (`useFile(fileUrl, true)`).</li><li>Tests: `hlsRefresh.test.ts` (10 tests), web-ci PASS.</li></ul> | <ul><li>The `/serve` MP4 fallback is still signed for `MaxProtectedTTL` = 5 min (`delivery/signer.go:72`), and no media-service test was added (second acceptance line).</li><li>No browser run.</li><li>No test drives `TubePlayer` itself; the refresher is tested as a module.</li></ul> |
| lv-28 cover-frame `thumbnail_url` unchecked | **OPEN**, confirmed | none | <ul><li>No commit touches `video_tools_handler.go` or `post_video_tools.go` since `7765e81a7`.</li><li>`service/post_video_tools.go:163-169` still stores `thumbnail_url` as given; `verifyCoverOnWrite` (`:149`) covers only `cover_media_id`.</li></ul> | All of it. It was in no fix lane. |
| lv-29 edits not reaching search | **FIXED** | 8ed473d16, 5b596c4d0, 309ce3901 | <ul><li>`post_edit.go` emits the eligibility event on title, text, tags, hashtags, category, language and cover.</li><li>search-service `events/post_content.go` re-indexes.</li><li>`TestEditReindexesOnIndexedFields`, `post_content_test.go`; Go CI PASS.</li></ul> | Not exercised end to end: search is not in the e2e tier. |
| lv-30 4 h limit enforced after upload | **PARTLY FIXED** | 058a97e30, d83616ad6 | <ul><li>Refused at init when `duration_ms` is declared, and at confirm from the MP4 header, before any transcode.</li><li>E2E `RefusedUploads/over-long` PASS (VERIFIED-RUN).</li></ul> | <ul><li>Neither client declares `duration_ms` at init: web `src/lib/resumableUpload.ts` sends `file_type`, `mime_type` and `total_bytes` only; Android `ResumableInitRequest` (`MediaUploadApi.kt:322-330`) is the same. So a real over-length file still uploads in full before it is refused.</li><li>The web studio still says "Any length" (`fileRules.ts:23`).</li><li>The founder decision is open.</li></ul> |
| lv-31 playlist share | **PARTLY FIXED**: web yes, Android no | web c645048 | <ul><li>Web: Share on public and unlisted collections (`library/components/CollectionView.tsx`); `/posttube/playlists/<uuid>` passes signed out (`src/middleware.ts`).</li><li>Tests: `collectionShare.test.tsx`, `middleware.test.ts`; web-ci PASS.</li></ul> | <ul><li>**Android: OPEN**, confirmed. No share action in `feature/tube/ui/collections/*.kt`, and no Android commit touched it. It was in no fix lane.</li><li>Web: a signed-out browser open-and-play (the acceptance) has not been run, and I did not establish whether the gateway answers `GET /v1/playlists/:id` to an anonymous caller.</li></ul> |
| lv-53 progress ordering (P3) | **FIXED** | f8cc49701, web c645048 | <ul><li>Both clients send `sent_at_ms`: Android `WatchProgressApi.kt:45` with `WatchProgressPauseTest`; web `watchProgress.test.ts`.</li><li>The server guard already existed.</li><li>The E2E resume step sends it.</li></ul> | none |
| lv-67 chart guard drift (P3) | **OPEN**, VERIFIED-RUN | none | My run on the HEAD snapshot: `FAIL QA: prod host in a QA file: deploy/services/post-service/values-qa.yaml:121: ACTION_LINK_OWN_DOMAINS` → `STATIC GUARDS FAILED`. reels-62 (media cap drift) no longer fires and is fixed. | Allow the key in the guard, or set the QA host. Also put the guard in CI. |
| lv-32 Android captions | **OPEN** | none | No `/v1/subtitles` call or `SubtitleConfiguration` in Android main sources (grep: test fixtures for offline copies only). | All |
| lv-33 Android Unlisted at create | **FIXED** | f8cc49701 | <ul><li>`createhub/ReelSurface.kt:1455-1461`; `CreatePostDtos.kt:392`.</li><li>`ReelPublishPipelineTest` "an unlisted long video goes on the wire as visibility unlisted"; Android CI ran.</li></ul> | The "Reel surface does not offer it" half has no test. |
| lv-34 Android creator tools | **OPEN** | none | No change. | All |
| Web captions (lv-8 upload, playback) | **FIXED** | as lv-8 | See lv-8. | Dev manual check. |

### 1d. New findings in this re-audit

| ID | Sev | Level | Finding |
|---|---|---|---|
| N-1 | P2 | VERIFIED-READ | **The per-class queue alerts read a pipeline-wide value.** `cmd/worker/readiness_metrics.go:38-42` says it: "The VALUE is still the store's pipeline-wide oldest age". Each worker writes that one value under its own class label, so `MediaTranscodeShortQueueWaiting` (`values-prod.yaml:287`, `class="short"` > 600 s) fires whenever any long video has waited 10 minutes. With two long lanes and multi-hour jobs that is routine. The result is false pages to the only real receiver, and the short and long alerts cannot be told apart. |
| N-2 | P2 | VERIFIED-READ | **The chart render guard runs in no CI.** No workflow runs `charts/atpost-service/render_test.sh`, and helm was not available where the change was made, so the worker split, the `do-not-disrupt` annotation, the worker PDB, the PodMonitor, the alert rules and the scratch sizes have never been rendered. Commit caa92a660's claim that it "runs in CI" is wrong. The renamed Deployments also need the old `media-service-worker` pruned (operator step). |
| N-3 | P2 | VERIFIED-READ | **No real client hits the init-time length refusal.** Neither client sends `duration_ms` at init, so the refusal is unreachable from real clients (lv-30 detail). |
| N-4 | P3 | VERIFIED-READ | **Azure prod would refuse to start.** `deploy/services/media-service/values-azure-prod.yaml` sets `ENV: prod` and mounts `JWT_SECRET`, and media-service now refuses to start in that state. This matters only if Azure is still a deploy target; the AWS prod values are correct. |
| N-5 | process | VERIFIED-READ | **A mutation-test edit reached a commit.** 3f2fe1e4e shipped with the creator opt-out check replaced by `if false`; 677ccaed3 fixed it. No other leftover was found. |

## 2. Journeys (e2e video tier, run 44136948310, job `e2e (video)`, on `fea421626`)

The tier runs api-gateway, user-service (app and identity), feed-service, graph-service, post-service, media-service, media-worker (ffmpeg, classes `short,long`), notification-service, trust-safety-service and the deny stub, with `media.events.long` created (log: "media.events.long OK"). It does **not** run search-service; the search subtest SKIPs with "TIER GAP … last status 502".

| # | Journey | Status | Test and pass line | Still unexecuted |
|---|---|---|---|---|
| 1 | Upload, publish, play each quality on a second account | **VERIFIED-RUN** | <ul><li>`TestE2E_Video_LongVideo_UploadRoutePublishPlay`: `--- PASS … (128.25s)`. B plays the master, each child playlist and the first segment of each rung: 360p and 720p, no 1080p for a 720p source.</li><li>`…/routed_on_media.events.long` `--- PASS`.</li><li>`…/hidden_before_approval` `--- PASS`.</li></ul> | <ul><li>A 1080p or 4K source is not encoded.</li><li>Clips are 605 s at 1 fps, so this is not a real long encode.</li><li>The clients are not driven.</li></ul> |
| 2 | Leave mid-video, resume on another device | **VERIFIED-RUN** at the API | `…/watch_progress_resumes_on_a_second_device` `--- PASS`: a second token reads the phone's position, with `sent_at_ms`. | The clients' resume logic is covered by unit tests only (Android `ResumeRuleTest`, web `watchProgress.test.ts`). |
| 3 | Public to private: an open player and a copied stream URL stop | **PARTLY VERIFIED-RUN** | <ul><li>After the private PATCH, B's master, child playlist and `/url` give 404 at once (inline asserts in the parent test, which passed).</li><li>`…/issued_segment_URL_stops_within_the_window_(lv-2)` `--- PASS`: "fetched after expiry: HTTP 403" with a 1 min window.</li></ul> | <ul><li>**The open-player half**: no real player (hls.js or ExoPlayer) is driven through a refresh that is then refused. There are only `hlsRefresh.test.ts` "the refresh says 403: the viewer is revoked" (web unit) and Android `HlsRefresh`.</li><li>The prod window (10 min) and the CloudFront signer are not exercised.</li></ul> |
| 4 | Create, reorder and share a playlist | **PARTLY VERIFIED-RUN** | `…/playlist_create_and_reorder` `--- PASS`. | <ul><li>**Share is not executed at any layer** beyond web unit tests.</li><li>Android has no share at all.</li><li>The signed-out open of a shared link has not been run.</li></ul> |
| 5 | Subscribe and receive the next upload | **VERIFIED-RUN** | `…/subscriber_notified_after_processing_(lv-1)` `--- PASS`, log "lv-1 asserted: one creator_uploaded_video for V1 after 1 poll(s)". `ATPOST_E2E_KNOWN_OPEN` is empty, so this is not a tracked skip. | <ul><li>Push or e-mail delivery is not checked (only the in-app notification row).</li><li>The 72 h wait ceiling is unit-tested only (`TestUploadFanout_WaitExpires`).</li></ul> |
| 6 | Kill the worker mid-transcode; confirm recovery | **UNVERIFIED** (not executed) | No e2e kills the worker (grep `WorkerCrash`/`SIGKILL` in `tools/integration` and `.github`: none). Only unit tests cover it: `TestLostWorkersPastTheCapAreRecordedFailed`, `TestInterruptedRunResumesFromTheFirstMissingRung`, `TestSIGTERMStopsNewWorkButNotTheJobInHand`. | All of the journey. |
| — | Unlisted absent from listings and search | Listings **VERIFIED-RUN**; search **skipped** | <ul><li>`…/unlisted_absent_from_listings` `--- PASS` across by-author, channel feed, trending, recent, video feeds and home.</li><li>`--- SKIP: …/unlisted_absent_from_search (60.05s)`.</li></ul> | Search exclusion of unlisted is still unexecuted. |

## 3. Deferred or unproven

1. **A real long encode on production-sized hardware** (lv-13, lv-4 L5). Nothing has encoded more than a 605 s, 1 fps, 720p clip. Peak scratch, RSS and wall time for 4 h 1080p and 1 h 4K are unmeasured.
2. **CloudFront wildcard signing against the real CDN** (lv-2, reels-2). The custom-policy `hls/*` signer (`delivery/signer.go:185-229`) is unit-tested only. If the policy or the resource string is wrong, every prod segment answers 403.
3. **A browser check of the web player refresh** (lv-27, lv-2) and of the credential split (reels-2), in Chrome and Safari. The commit itself says "Not checked in a browser or against a real CDN".
4. **Android on a device.** f8cc49701 says "Not checked on a device".
5. **Load tests written but never run** (lv-11, reels-11). L6 is not written. There is no results file.
6. **The chart has never been rendered** (N-2), and `render_test.sh` is not in CI.
7. **Per-user quota defaults await the founder:** 32 GiB a day, 8 h a day and 2 long videos in flight (`service/limits.go:33-42,58-59`). The segment window of 10 min was set by engineering and has no recorded founder sign-off.
8. **Founder decisions still open:**
   - Do members-only uploads notify every subscriber? (`UPLOAD_FANOUT_NOTIFY_MEMBERS_ONLY`, default true; lv-56)
   - Do 18+ uploads notify? (`UPLOAD_FANOUT_NOTIFY_AGE_RESTRICTED`, default true; lv-71)
   - The creator-fund eligibility rule: the code requires AND, while its comment says OR.
   - The 4 h limit against "no length cap": the studio still says "Any length".
   - Backup retention.
   - The 4K rung (lv-14).
9. **Operator steps:**
   - Re-run the MSK topics Job (`media.events.long`, `monetization.events.dlq`).
   - Deploy analytics-service before auth-service, or purges stay pending (08d8a122f).
   - Confirm the SNS ops alert e-mail subscription (`alerting.tf:28`).
   - Prune the old `media-service-worker` Deployment after the split.
   - Apply migrations 021 (notification), 033 and 034 (media) and 086 (post).
   - Apply the EKS network-policy addon and enable `networkPolicy` for media (reels-6).
   - Remove `JWT_SECRET` from the Azure and staging media values if those targets are used.
10. **Test-layer gaps that remain:**
    - Search exclusion of unlisted (SKIP).
    - The worker kill.
    - Playlist share.
    - The open player after private.
    - Stranger tests on trim, category override, publish, distribution, playlist delete, post restore, caption owner refusal and series episode add/delete (verify/v3 part 2a). No test was added for any of them.
    - lv-74 (flick-series episode add without a post check) is unchanged.
    - Integration logs still cannot show per-test outcomes (reels-70).

## 4. Launch gate on today's evidence

| # | Gate line | Was | Now | Pointer |
|---|---|---|---|---|
| 1 | Every core user journey works end to end | FAIL | **UNVERIFIED** | <ul><li>Journeys 1, 2 and 5 are VERIFIED-RUN; 3 and 4 are partly; 6 is unexecuted (section 2).</li><li>Nothing walked in code now fails on the web.</li><li>**On Android, playlist share does not exist (lv-31)**. If the gate is judged on the main client ("Android is the super app"), this line is FAIL.</li></ul> |
| 2 | No open P0 | PASS | **PASS** | <ul><li>No P0 found in this re-audit.</li><li>No regression in owner checks on the routes read.</li></ul> |
| 3 | Every route has an auth decision; every object-ID route has a tested authorization check | FAIL | **FAIL** | <ul><li>lv-7 and reels-7 are fixed, and the header half of reels-6 is fixed.</li><li>Still **no stranger test** for trim, category override, publish, distribution, playlist delete, post restore, the caption owner refusal, or series episode add/delete; none was added in the fix round.</li><li>lv-74 is unchanged.</li><li>The reels-6 network policy is still off.</li></ul> |
| 4 | No secrets in repo, bundle or logs | UNVERIFIED | **UNVERIFIED** | <ul><li>gitleaks is green on `cdb530dd5`, but logged "1 commits scanned": git history has still not been scanned.</li><li>The web repo has no secrets scan.</li></ul> |
| 5 | Dependency and static scans clean | UNVERIFIED | **UNVERIFIED** | <ul><li>Go: govulncheck and Trivy are green on `cdb530dd5` (VERIFIED-RUN: "Your code is affected by 0 vulnerabilities").</li><li>The web and Android dependency scans have not run.</li><li>The weekly scheduled run on main still uses the stale workflow.</li></ul> |
| 6 | Upload and media paths meet section 4.4 | FAIL | **FAIL** | <ul><li>Quota (lv-3), the segment window (lv-2) and sliding expiry (lv-16) are fixed.</li><li>**lv-28 is still open**: `cover-frame` stores any `thumbnail_url`, which bypasses image moderation and works as a tracking pixel.</li><li>lv-30 is partial: over-length files still upload in full from both clients.</li></ul> |
| 7 | Money paths | N/A | **N/A** | No payments in this module. |
| 8 | Retried and concurrent writes are safe | FAIL | **PASS** | <ul><li>lv-17, reels-1 and lv-53 are fixed (section 1).</li><li>The lv-6 start counter is in place.</li><li>Duplicate compute after a rebalance remains; state stays safe, as before.</li></ul> |
| 9 | Build, tests and migrations pass in CI from clean | FAIL | **FAIL** | <ul><li>All workflows are green: Go, Integration, Lint and Security at `cdb530dd5`; E2E at `fea421626`; Android at `f3f68c1aa`; web-ci at `1b596b4`.</li><li>A real transcode runs in CI.</li><li>But the chart guard fails at HEAD (lv-67, my run), the chart render test runs in no CI (N-2), and feed-service is still absent from the integration matrix.</li></ul> |
| 10 | Logs, metrics and alerts for core flows | FAIL | **FAIL** | <ul><li>The worker is now scraped and alerted.</li><li>But there are no fan-out or schedule metrics, the duration histogram still caps at 600 s, the per-class queue alerts read a pipeline-wide value (N-1), the rules were never rendered, and the e-mail subscription is unconfirmed (lv-21, reels-5).</li></ul> |
| 11 | Rollback path and restore | UNVERIFIED | **UNVERIFIED** | <ul><li>Migrations 021, 033 and 034 say the old binary ignores the new columns.</li><li>No rollback or restore has been exercised.</li><li>Retention is not decided.</li></ul> |
| 12 | Prod config: debug, test accounts and permissive settings off | FAIL | **PASS** (AWS prod) | <ul><li>reels-15 is fixed for `values-prod.yaml`.</li><li>`MEDIA_UPLOAD_RATE_LIMIT_OPTIONAL` is unset in every deploy file.</li><li>Caveat N-4 (Azure prod values).</li></ul> |

### Proposed verdict: **NO-GO**

- **Deciding lines:** line 3 (FAIL) and line 6 (FAIL). By the rule, any FAIL on lines 1 to 8 means NO-GO.
- **Line 1:** UNVERIFIED, and it becomes FAIL if Android playlist share is required.
- **Lines 3 and 6 are narrow:**
  - Line 3 needs about nine stranger tests plus the lv-74 post check.
  - Line 6 needs lv-28: `thumbnail_url` must be the creator's own platform image.
  - Both are S-sized.
- **After those, GO WITH CONDITIONS at best.** These are the conditions and who must do what:
  1. **Engineering:**
     - Add the stranger tests and fix lv-28 and lv-74.
     - Put `render_test.sh` in CI and fix lv-67.
     - Fix the class value of the queue gauges (N-1).
     - Add the fan-out and schedule counters.
     - Send `duration_ms` at init from both clients.
     - Build Android playlist share, or the founder waives it.
  2. **Someone with staging access:**
     - Run journeys 3 (with a real player), 4 (share, signed out) and 6 (kill the worker).
     - Run search exclusion with search-service in the tier.
     - Check CloudFront wildcard signing and the web player in Chrome and Safari.
     - Do one real long encode on production-sized hardware (L5).
  3. **Ops:**
     - Re-run the topics Job.
     - Deploy analytics before auth.
     - Confirm the SNS subscription.
     - Prune the old worker Deployment.
     - Enable the network policy.
     - Exercise one rollback and one restore.
  4. **Founder:** quota numbers, the 4 h cap, members-only and 18+ notification, creator-fund rule, backup retention.
  5. **CI:** a full-history secrets scan, and dependency scans for web and Android.

## 5. Counts

| | FIXED | PARTLY FIXED | OPEN |
|---|---|---|---|
| New long-video P1s (lv-1 to lv-11) | 8 (lv-1, 2, 3, 6, 7, 8, 9, 10) | 3 (lv-4, 5, 11) | 0 |
| Inherited reels items cited (13: reels-1 to 7, 9 to 13, 15) | 8 (reels-1, 3, 7, 9, 10, 12, 13, 15) | 5 (reels-2, 4, 5, 6, 11) | 0 |
| Cited P2s and P3s (lv-13, 16, 17, 21, 27, 28, 29, 30, 31, 53, 67, 32, 33, 34) | 4 (lv-17, 29, 33, 53) | 6 (lv-13, 16, 21, 27, 30, 31) | 4 (lv-28, 67, 32, 34) |
| New in this re-audit | | | N-1, N-2, N-3 (P2); N-4 (P3); N-5 (process) |

**Gate lines** (3 PASS, 4 FAIL, 4 UNVERIFIED, 1 N/A): 1 UNVERIFIED, 2 PASS, 3 FAIL, 4 UNVERIFIED, 5 UNVERIFIED, 6 FAIL, 7 N/A, 8 PASS, 9 FAIL, 10 FAIL, 11 UNVERIFIED, 12 PASS.

**What got worse:**
- N-1: false short-queue alerts once the rules go live.
- N-4: the Azure prod media values would now fail to start.
- N-2: a chart change carrying five fixes shipped unrendered, with a false "runs in CI" claim.
- N-5: a mutation reached a commit for one revision.
- Nothing that worked before is broken in the code read.
