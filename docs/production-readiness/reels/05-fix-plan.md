# Reels: fix plan

- **Audit date:** 2026-11-17. Code: `codex/module-01-02-launch-safety` @ `58f602b6d`. Web: `pb-echo` `feat/echo-web` @ `ca96921`.
- **Scope:** every P1 in `03-findings.md` in the order to fix it, then a short list of the P2s to take next. There are no P0s; reels-2 becomes P0 for the web if its browser check shows the fallback is also blocked.
- **Each item:** dependencies, the smallest fix, and a test that fails now and passes after the fix. Run `scripts/verify/go.sh --fast <module>` while working and `--full` before reporting; Android `scripts/verify/android.sh`; integration tests in CI only.

## P1, in fix order

### 1. reels-9: tests run in CI at the release commit

- **Depends on:** nothing. Do it first: every later "test passes" needs CI to run it.
- **Smallest fix:** let the "Write google-services.json" step in `android-native.yml` write a placeholder when `GOOGLE_SERVICES_JSON` is empty and the job runs unit tests only; add a `bun test` workflow to the web repo; add the release branch to `go-ci.yml` push triggers and feed-service to `scripts/ci/integration-services.yaml`.
- **Test:** Android CI at the release commit currently fails before Gradle; after, it is green with test-result artefacts. go-ci shows a run at the release commit that includes feed-service.

### 2. reels-3: provision `media.events.long`

- **Depends on:** nothing.
- **Smallest fix:** add `"media.events.long"` to the default topic list in `infra/terraform/modules/msk/topics-job/variables.tf` and re-run the topics Job.
- **Test:** a guard test that collects every topic name used in Go code (including `intakeTopic + ".long"`) and asserts each is in the MSK list. Fails now on `media.events.long`.

### 3. reels-1: no reel stays pending for ever (with the reels-64 table test)

- **Depends on:** reels-9 for CI proof. Write the reels-64 table test first; it pins today's behaviour and the new error path.
- **Smallest fix:** `finalizeReviewGate` returns its error; only `pgx.ErrNoRows` means "no post"; the consumer retries store errors. After `CreateVideoMetadata`, re-run the decision when the post is pending and the media is a video. Add a reconciler for video posts pending over 5 minutes with ready media.
- **Test:** consumer test with a store whose flip fails once: the first delivery returns an error and the second approves the post (today the first returns nil and the post stays pending). Reconciler test: pending post + ready/passed media → approved after one tick.

### 4. reels-5: monitor the transcode pipeline

- **Depends on:** the feed module's alert routing fix (feed-10), so alerts reach a receiver that is not `null`.
- **Smallest fix:** `containerPort: 9091` named `metrics` on the worker and a PodMonitor for `component=worker`; PrometheusRules for worker restarts, zero ready replicas, oldest pending age > 30 min, failure rate, `forward_failed > 0`; a `media_transcode_quarantined_total` counter; mark the asset failed on a permanent pre-heartbeat error.
- **Test:** `charts/atpost-service/render_test.sh` asserts the worker is scraped and that a rule names `container="media-service-worker"` (fails now). Worker unit test: a permanent pre-heartbeat error increments the counter and leaves the asset failed.

### 5. reels-4: transcode capacity

- **Depends on:** reels-3 (long videos off the reel loop) and reels-5 (the lag alert needs scraped metrics).
- **Smallest fix:** `worker.replicaCount: 6` in `deploy/services/media-service/values-prod.yaml` (equal to the `media.events` partitions), 2 CPU requested each; a lag rule for group `media-transcode-worker`. Encoding all renditions in one ffmpeg run comes later.
- **Test:** a render guard asserting prod worker replicas ≥ `media.events` partitions (fails now: 2 < 6). The capacity proof itself is load test L4 (item 14).

### 6. reels-2: web HLS without credentials to the CDN

- **Depends on:** a staging or QA environment with the CloudFront distribution, to confirm both the defect and the fix in Chrome and Safari.
- **Smallest fix:** `xhrSetup: (xhr, url) => { xhr.withCredentials = isApiUrl(url) }` in `ReelVideo.tsx` (and `TubePlayer.tsx`, `coverFrame.ts`); `crossOrigin="anonymous"` on the `<video>` for CDN sources.
- **Test:** unit test of the hls config factory: a CDN fragment URL gets `withCredentials=false`, an API playlist URL `true` (fails now: always true). On staging: `.ts` requests to the media host answer 200 with no `/serve` fallback.

### 7. reels-6: media-service stops trusting a bare X-User-Id

- **Depends on:** nothing for the key; the network policy needs a Terraform apply of the EKS addon.
- **Smallest fix:** `RequireInternalKey` on the `/v1/media`, `/v1/audio`, `/v1/subtitles` groups and the gateway stamping the key on those prefixes (`api-gateway/pkg/internalroutes/internalroutes.go`); `vpc-cni` `configuration_values = {enableNetworkPolicy = "true"}` in `infra/terraform/modules/eks/main.tf`; enable the chart's NetworkPolicy for media-service with ingress from the gateway and named peers.
- **Test:** handler test: a read with `X-User-Id` and no key answers 401 (fails now: 200). Gateway test: the key is stamped on `/v1/media/x`. Rendered prod manifest contains a NetworkPolicy for media-service.

### 8. reels-8: analytics joins account erasure

- **Depends on:** nothing.
- **Smallest fix:** re-point the handler in `analytics-service/internal/consumers/engagement_consumer.go` from `user.deletion_requested` to `user.purge_requested`, using the shared purge handler as media-service does; extend it to `playback_sessions` and the Scylla watch tables; ack as `analytics`; add `analytics` to `DefaultRequiredServices` in auth-service.
- **Test:** handler unit test: a `user.purge_requested` event deletes the user's events, sessions and watch history and sends an ack (fails now: the event is ignored). Auth purge test: completion waits for the analytics ack.

### 9. reels-7: owner gate on the analytics views route

- **Depends on:** nothing. Small and independent; can be done at any point.
- **Smallest fix:** gate `GetContentViews` in `analytics-service/internal/http/handler.go` like `GetContentInsights` (owner or admin, else 404), or remove the route.
- **Test:** httptest: no `X-User-Id` → 401; user A on B's content → 404; owner and admin → 200 (fails now: 200 for all). Add the route to `private_route_inventory_test.go`.

### 10. reels-12: real ffmpeg in CI

- **Depends on:** reels-9.
- **Smallest fix:** `sudo apt-get install -y ffmpeg` in the media-service CI jobs; `requireFFmpegOrSkip` calls `t.Fatal` when `CI=true`.
- **Test:** a `-v -run` step showing `TestHLSRepackagedReelIsPlayable` and `TestRenditionCarriesNoLocation` as PASS; with ffmpeg removed the job goes red (today it silently skips).

### 11. reels-10: reel journey e2e

- **Depends on:** reels-12 (worker needs ffmpeg in CI) and reels-1 (the flow would be flaky on the pending race).
- **Smallest fix:** an e2e matrix entry "reels" with api-gateway, media-service, media-worker, post-service, identity-profile, trust-safety and Redpanda; `TestE2E_Reels_UploadPublishPlay` with `scripts/smoke/fixtures/smoke-2s-portrait.mp4`.
- **Test:** the new flow is green, and red when `finalizeReviewGate` is stubbed out or the worker is not started (no such test exists now).

### 12. reels-13: server resumable upload test

- **Depends on:** reels-9.
- **Smallest fix:** an integration test against Postgres and a fake multipart blob store covering parts out of order, re-sent part, status, complete refused until all parts, double complete, another user's upload id.
- **Test:** passes after; dropping the part-count check in `CompleteResumableUpload` makes it fail.

### 13. reels-14: single active player test

- **Depends on:** reels-9.
- **Smallest fix:** `core/media/src/test/.../PlayerPoolTest.kt` with `FakeExoPlayer`; a happy-dom test mounting the web `ReelsScreen` with three reels.
- **Test:** after `playOnly(i)` for i in 0..49 exactly one player plays with volume > 0 and at most 4 were created; breaking `playOnly` so it skips pausing the previous player fails it.

### 14. reels-11: load tests L1-L7

- **Depends on:** a QA environment sized like production (shared with feed-02), and the fixes for reels-2, reels-3 and reels-4, so the run measures the system that will launch.
- **Smallest fix:** k6 scripts under `Architecture/tools/loadtest/reels/` for the reels feed, playlist route, analytics ingest, upload burst (L4), resumable parts, and trending; a results file in this folder.
- **Test:** the results meet the Track D thresholds (for example p95 confirm-to-ready under 120 s for 60 s reels at 200 uploads per 5 minutes). No result exists now.

## P2: take next

In rough order of user impact:

1. reels-15: refuse HS256 and require exp, iss, aud in prod (fold in with item 7).
2. reels-27: bound watch time by elapsed time before Creator Fund pays on it.
3. reels-29: resolving a report takes the reel down.
4. reels-31: a transcode failure no longer rejects the post for good.
5. reels-32: confirm and queue in one transaction.
6. reels-18 and reels-19: engagement and trending respect the review and visibility gates.
7. reels-30: subscriber notifications on approval.
8. reels-34 and reels-35: scanner outage handling and worker scratch disk (check `terraform plan` first; reels-35 becomes P1 on a 20 GiB root).
9. reels-17: shorter segment TTL and purge of object versions.
10. reels-24, reels-16, reels-25: media-service rate limits, hardened audio ffmpeg, service tokens on destructive internal routes.
11. reels-46, reels-45, reels-47: web preload and 2 s segments, portrait renditions, playlist caching.
12. reels-37, reels-38, reels-36: Android drafts, AI label, camera cap.
13. reels-39, reels-40: web resume after reload and draft publish idempotency.
14. reels-56, reels-57, reels-60: graceful worker and server shutdown, worker liveness.
15. reels-62 and reels-63: QA guard green, pinned ffmpeg and image scan.
16. reels-64 to reels-70: remaining test gaps (reels-64 is already part of item 3).
