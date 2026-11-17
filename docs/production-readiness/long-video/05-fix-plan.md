# Long video (PostTube / MTube): fix plan

- **Audit date:** 2026-11-17. Code: `codex/module-01-02-launch-safety` @ `7765e81a7`. Web: `pb-echo` `feat/echo-web` @ `ca96921`.
- **Scope:** every long-video P1 in `03-findings.md` (lv-1 to lv-11) and every inherited reels P1 that bites long video (reels-1 to reels-13, marked **inherited**), in the order to fix them, then a short list of the P2s to take next. There are no P0s. Inherited items are fixed once, in the reels plan; they appear here only so the order and the dependencies are visible.
- **Each item:** dependencies, the smallest fix, and a test that fails now and passes after the fix. Run `scripts/verify/go.sh --fast <module>` while working and `--full` before reporting; Android `scripts/verify/android.sh --fast :module` while working and `--full` at the end; integration tests in CI only.
- **Founder decisions needed before or during the work:** the quota numbers (item 11), the segment exposure window (item 7), the 4-hour cap versus "unlimited" (lv-30, which also sets lv-13's caps), whether to keep the 4K rung (lv-14), and whether members-only uploads notify non-members (lv-56).

## P1, in fix order

### 1. reels-9 (inherited): tests run in CI at the release commit

- **Depends on:** nothing. Do it first: every later "test passes" needs CI to run it, and the long-video feed and fan-out leg (feed-service) and the 818 posttube web tests run in no CI today.
- **Smallest fix:** as the reels plan item 1 (Android placeholder `google-services.json` for unit-test runs, a `bun test` workflow in the web repo, the release branch in `go-ci.yml`, feed-service in `scripts/ci/integration-services.yaml`).
- **Test:** go-ci shows a run at the release commit that includes feed-service; Android CI green with test artefacts; a web workflow runs `src/features/posttube` and `src/features/upload`.

### 2. reels-3 (inherited): provision `media.events.long`

- **Depends on:** nothing.
- **Smallest fix:** add `"media.events.long"` to `infra/terraform/modules/msk/topics-job/variables.tf` and re-run the topics Job.
- **Test:** lv-40's guard (every topic name used in Go code, including `intakeTopic + ".long"`, is in the MSK list and `init-topics.sh`). Fails now on `media.events.long`.

### 3. lv-7: a stranger can no longer block another user's erasure

- **Depends on:** nothing. Small and legal-grade; do it early.
- **Smallest fix:** in user-service `PurgeUser` (`user-service/internal/store/purge.go`), before `DELETE FROM channels`, set `channel_id = NULL` on `video_series` and `playlists` rows that point at the user's channels (probed with `to_regclass`). Then in post-service ignore a client `channel_id` (derive from the creator), require `trailer_post_id` to be the creator's own post, and add `ON DELETE SET NULL` in a migration.
- **Test:** user-service purge test with a stranger's series and playlist pointing at the user's channel: the purge completes and both end with `channel_id` NULL (fails now on the FK). post-service handler test: a body `channel_id` is ignored; a stranger's `trailer_post_id` answers 403.

### 4. reels-8 (inherited): analytics joins account erasure

- **Depends on:** nothing; pairs naturally with item 3 (both are erasure).
- **Smallest fix:** as the reels plan item 8 (re-point the analytics handler to `user.purge_requested`, cover `playback_sessions` and the Scylla watch tables, add `analytics` to `DefaultRequiredServices`).
- **Test:** a `user.purge_requested` event deletes the user's long-video sessions and watch history and acks; auth purge waits for the analytics ack.

### 5. lv-9: 18+ is part of publishing and fails closed

- **Depends on:** nothing.
- **Smallest fix:** accept `age_restricted` on `POST /v1/posts` and on the draft publish route, stored in the create transaction; the web studio sends it there and does not publish if it cannot; confirm Android sends it at create.
- **Test:** create handler test: `age_restricted: true` is stored at create (fails now: ignored). Web test: the flag is in the create or draft body and no follow-up PATCH is sent.

### 6. lv-8: web caption upload works and failures are shown

- **Depends on:** nothing. One-line client change.
- **Smallest fix:** `createSubtitleTrack` (`src/features/posttube/data/posttubeApi.ts:615-630`) sends `content: vttContent`, no `content_url`; the studio shows an error when the caption step fails instead of swallowing it.
- **Test:** web unit test that the body has `content` and no `content_url` (fails now); studio test that a caption failure is shown. After deploy to dev, upload a `.vtt` from Hub and see the track.

### 7. lv-2: bounded segment exposure after private, delete, takedown or lapse

- **Depends on:** a founder decision on the window (for example 30 min). Ship together with the web refresh of lv-27 (P2), otherwise a shorter TTL makes web viewers lose their place sooner. lv-12 (a) (one signature per playlist) is advisable, since shorter TTLs mean more re-signing.
- **Smallest fix:** cap `HLSSegmentTTL` for protected assets at the chosen window independent of duration (`media-service/internal/delivery/sign_cache.go`), keep `X-HLS-Expires-At`; web hls.js reloads the source at the current time on a 403 or after the header's instant.
- **Test:** `TestHLSSegmentTTL` asserts ≤ 30 min for a 3 h video (fails now: 6 h 15 min). lv-35's joined test (manifest 200 as B, set private, manifest 404). Web player test: a mocked 403 fragment triggers one manifest reload and resumes at the same time.

### 8. reels-7 (inherited): owner gate on the analytics views route

- **Depends on:** nothing. Small and independent.
- **Smallest fix:** as the reels plan item 9.
- **Test:** httptest: signed out 401, user A on B's long video 404, owner and admin 200.

### 9. reels-1 (inherited): no video post stays pending for ever

- **Depends on:** item 1 for CI proof. Must land before item 10, which hooks the notification into the same review-gate flip.
- **Smallest fix:** as the reels plan item 3 (`finalizeReviewGate` returns errors, only `pgx.ErrNoRows` means "no post", a reconciler for video posts pending over 5 minutes with ready media).
- **Test:** consumer test with a store whose flip fails once: the second delivery approves the post; reconciler test approves a pending post with ready media after one tick. Add a long-video case (content type `long_video`, reclassified from flick).

### 10. lv-1: subscribers are notified when a long video becomes playable

- **Depends on:** item 9 (the approval flip must be reliable to be a trigger). Fixes lv-17 and the long-video half of reels-30 at the same time; lv-18 (internal state read) is the natural next step.
- **Smallest fix:** on the first approval of a long-video post, and on the first transition to `public` from `unlisted` or `private` (live replays included), write one fan-out trigger event in the same transaction; notification-service enqueues on it, de-duplicated by post id, and stops treating a pending or processing read as a terminal skip.
- **Test:** notification-service test: first state read processing, later public and approved → exactly one delivery (fails now: terminal skip). post-service tests: the ready flip and an unlisted-to-public edit each write one trigger event. Add lv-38's `TestUploadEligible` table.

### 11. lv-3: per-user storage and transcode quota, fail closed

- **Depends on:** the founder's numbers (bytes per day, source hours per day, in-flight long jobs). Do it before the capacity items, because it bounds what capacity has to absorb.
- **Smallest fix:** at media-service confirm and resumable init, a rolling 24 h byte counter and a daily source-duration counter per user (Redis), at most one in-flight long job per uploader, 429 `TRANSCODE_QUOTA`, and refuse video on a Redis error; set `MEDIA_WORKER_MAX_ORIGINAL_BYTES` explicitly in `values-prod.yaml`.
- **Test:** service tests: crossing the byte or minute budget answers 429; a second in-flight long job answers 429; a Redis error refuses a video init (fails now: `return true // fail-open`). Render test for the explicit cap.

### 12. reels-5 (inherited): monitor the transcode pipeline

- **Depends on:** feed-10 (alert routing to a real receiver). Needed by items 13 to 16 for their alerts.
- **Smallest fix:** as the reels plan item 4, plus the long-video split of lv-21 (P2): a `class` label and buckets to 48 h, long oldest-waiting and long failure rules, a lag rule for `media-transcode-worker-long`.
- **Test:** `render_test.sh` asserts the worker is scraped and that long-class rules exist; a unit test reads the class label from a long and a short job.

### 13. lv-6: count transcode starts, so a crash, OOM or eviction cannot loop for ever

- **Depends on:** nothing in code; item 12 to see it fire.
- **Smallest fix:** in `processTranscodeLocked`, before `run()`, increment a per-asset start counter for the current `media_generation`; on the fourth start record a terminal failure ("transcode lost its worker 3 times") and commit; reset on a new generation.
- **Test:** three deliveries that never return leave the fourth recorded as failed and committed; a normal job increments once (fails now: no counter).

### 14. lv-5: a running long transcode is not thrown away by routine disruption

- **Depends on:** nothing for the annotation; the drain needs reels-56 (P2).
- **Smallest fix:** `karpenter.sh/do-not-disrupt: "true"` in `worker.podAnnotations` for prod and QA; a worker-only PDB (fixes reels-103 too); a runbook step to check for in-flight long jobs before rolling the worker. Then the reels-56 drain and MP4 rung reuse per `media_generation`.
- **Test:** the rendered worker pod carries the annotation and its own PDB (render test, fails now). With rung reuse: a second run of the same generation skips rungs already uploaded.

### 15. reels-4 (inherited): transcode capacity for reels

- **Depends on:** item 2 (long videos off the reel loop) and item 12 (lag alert).
- **Smallest fix:** as the reels plan item 5 (short-worker replicas equal to the `media.events` partitions, 2 CPU each).
- **Test:** render guard: prod short-worker replicas ≥ `media.events` partitions (fails now: 2 < 6).

### 16. lv-4: a dedicated long-transcode worker

- **Depends on:** items 2, 12 and 15 (split the fleet once, by class).
- **Smallest fix:** a second chart worker Deployment with `MEDIA_TRANSCODE_CLASSES=long`, its own CPU and replica count, short-only pods beside it (`values-prod.yaml:166-174` already plans this); `-threads` equal to the CPU limit; lv-14 (a) to skip the unplayed 4K rung.
- **Test:** render test: prod renders a `long` worker separate from the short worker (fails now). Capacity proof is load test L5 (item 24).

### 17. reels-2 (inherited): web HLS without credentials to the CDN

- **Depends on:** a staging or QA environment with the CloudFront distribution.
- **Smallest fix:** as the reels plan item 6, applied to `TubePlayer.tsx` and `hub/coverFrame.ts` as well as `ReelVideo.tsx`. If it is confirmed broken, raise lv-27 to P1 and land it here.
- **Test:** hls config factory test: a CDN fragment URL gets `withCredentials=false`; on staging `.ts` requests to the media host answer 200 for a long video with no `/serve` fallback, in Chrome and Safari.

### 18. reels-6 (inherited): media-service stops trusting a bare X-User-Id

- **Depends on:** a Terraform apply of the EKS network-policy addon.
- **Smallest fix:** as the reels plan item 7. Covers long-video HLS, storyboards, captions and the owner `/download`.
- **Test:** a read with `X-User-Id` and no key answers 401 (fails now); the rendered prod manifest has a NetworkPolicy for media-service.

### 19. reels-12 (inherited): real ffmpeg in CI

- **Depends on:** item 1.
- **Smallest fix:** as the reels plan item 10, plus one landscape fixture longer than the classifier boundary or wider than tall, so the long-form ladder is encoded for real.
- **Test:** `-v -run` shows the HLS repackage tests PASS, including a landscape long-form case; with ffmpeg removed the job goes red.

### 20. reels-13 (inherited): server resumable upload test

- **Depends on:** item 1.
- **Smallest fix:** as the reels plan item 12. Add the long-video cases from lv-16 once its sliding expiry lands (a part at hour 23 moves the expiry; orphan GC skips an open session).
- **Test:** dropping the part-count check in `CompleteResumableUpload` makes it fail.

### 21. reels-10 (inherited): the e2e upload → publish → play harness

- **Depends on:** items 9 and 19.
- **Smallest fix:** as the reels plan item 11.
- **Test:** the reels flow is green and goes red when the review gate is stubbed out or the worker is not started.

### 22. lv-10: long-video e2e on the same harness

- **Depends on:** items 2, 10, 19 and 21.
- **Smallest fix:** an e2e domain `longvideo` (gateway, media-service, media-worker with ffmpeg and `MEDIA_TRANSCODE_CLASSES=short,long`, post-, feed- and notification-service, Redpanda with `media.events.long`). `TestE2E_LongVideo_UploadPublishPlay` (landscape 12 s fixture, resumable upload, publish as `long_video`, second account fetches `master.m3u8` and one segment per rung, signed-out private copy 404, subscriber notification row delivered). Add lv-42's `TestE2E_LongVideo_WorkerCrashRecovers` in the same domain.
- **Test:** green in CI; red when the long consumer is not started (assert its offset moved), when the ladder drops 720p, or when the notification is not delivered.

### 23. reels-11 (inherited): reels load tests

- **Depends on:** a QA environment sized like production (feed-02) and items 2, 15 and 17.
- **Smallest fix:** as the reels plan item 14.
- **Test:** results meet the reels Track D thresholds.

### 24. lv-11: long-video load tests L1 to L9

- **Depends on:** item 23's environment, plus items 3, 11, 13, 14 and 16 so the run measures the system that will launch; `media.events.long` created.
- **Smallest fix:** k6 scripts under `Architecture/tools/loadtest/long-video/` for Track D section 6 L1 to L9 (watch-open mix, playlist start and switches, progress heartbeats, analytics for long sessions, long transcode with a reel burst, 16 GiB uploads, lists and search, comments, a 6 h soak with real browsers), and a dated results file in this folder. L5 doubles as the measurement lv-13 needs.
- **Test:** the results meet the Track D thresholds (for example reel p95 confirm-to-ready under 120 s while long jobs run; 1 h 1080p ready under 6 h with no terminal failure; scratch peak under 80% of the volume; `/hls/*.m3u8` p95 under 200 ms). No result exists now.

## P2: take next

In rough order of user impact:

1. lv-27, lv-62: web player keeps its place on expiry and fallback (with item 7; P1 if reels-2 is confirmed).
2. lv-18, lv-19, lv-71, lv-17: the rest of the upload-notification chain (internal state read, backoff and replay, age-restricted, scheduled while transcoding), after item 10.
3. lv-13, lv-15: measure the worker at the admitted caps and set explicit caps (with L5); log and count dropped rungs. lv-13 becomes P1 if the measurement shows eviction or OOM.
4. lv-12, lv-14: one signature per playlist; skip the 4K rung and encode once.
5. lv-16, lv-39: sliding resumable expiry and abort retry, with tests.
6. lv-28: `cover-frame` takes only the creator's own platform image.
7. lv-29: metadata edits re-index search.
8. lv-22, lv-23: throttle and cheapen progress heartbeats; retention for `watch_progress`.
9. lv-20, lv-21: per-class busy tracking for the sweeper and fingerprints; long-video alerts (with item 12).
10. lv-30: founder decision on the 4-hour cap, then a client-side refusal before upload.
11. lv-24, lv-25, lv-26: comment "top" order, related cache, channel search minimum length.
12. lv-32, lv-33, lv-34, lv-31: Android captions, Unlisted at create, creator tools; playlist sharing on both clients.
13. lv-35 to lv-38, lv-40 to lv-42: remaining test gaps (lv-35 lands with item 7, lv-38 with item 10, lv-40 with item 2, lv-42 with item 22).
14. Inherited reels P2s that weigh most on long video: reels-56 and reels-57 (graceful worker drain, liveness; with item 14), reels-31 (a failed transcode no longer rejects for good), reels-34 (scanner outage re-encodes), reels-39 (web resume after reload), reels-17 (closed by item 7 for long video), reels-15 (with item 18).
