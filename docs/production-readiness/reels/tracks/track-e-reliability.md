# Track E: reliability, observability and deployment (reels)

- **Audit date:** 2026-11-17. Read-only. Tree: `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` @ `58f602b6d`, dirty working tree read as it is. Web tree read: `C:\workspace\wt\pb-echo` (`feat/echo-web`).
- **Scope:** harness sections 4.7 and 4.8 for media-service (server and worker), the reels paths through post-service, feed-service and analytics-service, the chart, the media values files and the media, MSK and EKS Terraform.
- Backend paths are relative to `Architecture/services/` unless they start with another top-level folder.
- **Executed (and nothing else):**
  - `QA_GUARDS_ONLY=1 bash charts/atpost-service/render_test.sh` → exit 1, `STATIC GUARDS FAILED` (one failure, E-11; every other static guard `ok`).
  - `bash scripts/verify/migration-compat.sh --self-test` → `self-test passed`, exit 0.
  - `migration-compat.sh --files` over all media-service migrations (29 flagged, all historical, see section 5), all post-service migrations (92 flagged, historical), and over the latest files (post 080-085, media 030-032: 5 flagged, read below).
  - `go test -count=1 ./cmd/worker/ ./internal/store/postgres/ -run 'Stall|Classify|Retry|Class|Budget|Scratch|Consume'` in media-service → both `ok`.
  - Read-only `grep`, `sed`, `git`.

## Inherited from the feed audit, still open

Not re-reported. Each applies to media-service exactly as it does to feed.

| Feed ID | What | State on 17 Nov | Reels note |
|---|---|---|---|
| feed-02 (feed E-2) | No QA or production environment exists; `build-push.yml` has never built an image (no `AWS_CI_ROLE_ARN`, no ECR) | open | `media-worker` image included in `build-push.yml`; never built either |
| feed-10 (feed E-3) | Default chart alert rules and an e-mail receiver written, unrendered and unapplied | config written | Covers the media-service **server** only; see E-2 for the worker |
| feed-11 (feed E-4) | Rollback runbook and `migration-compat.sh` written, not drilled, script not in CI | runbook written | Section 5 |
| feed-12 (feed E-5) | Scylla backup config and restore runbooks for Aurora, ElastiCache, Scylla written, never applied or drilled | config written | No runbook covers the media bucket: E-7 |
| feed E-8 | `shared/server.Run` calls `OnShutdown` (closes DB) before `srv.Shutdown`; no preStop | open (`Architecture/shared/server/server.go:87-91`) | Media-specific consequences in E-9 |
| feed E-12 | Request id not forwarded between services; no tracing started | open | Media and worker specifics in E-8 |
| feed E-17 | Base images by tag, not digest | open | ffmpeg specifics in E-12 |
| feed E-18 | Staging values stale (`tag: latest`) | open | media staging also lacks Kafka SASL and uses a literal bucket name (map section 8) |

## Short answers

| Question | Answer |
|---|---|
| What does Kubernetes probe on the worker? | **Nothing.** No readiness probe by design, `worker.livenessProbe: {}` in the chart default and no override in prod (`charts/atpost-service/templates/worker-deployment.yaml:117-126`, `values.yaml:248`). A hung worker is never restarted (E-6). |
| In-flight transcode on SIGTERM, drain or rollout? | Cancelled at once (context cancel kills ffmpeg), offset left uncommitted, redelivered to the next owner and re-run from the start. Nothing is lost or duplicated, but the 300 s grace is unused and every rollout restarts all in-flight work (E-5). |
| Is the stall sweeper deployed and scheduled? | **Yes in code, yes in the chart:** it is a goroutine inside every worker replica (`cmd/worker/main.go:183-188`, every 5 min, on unless `MEDIA_TRANSCODE_STALL_SWEEP=off`; prod values do not set it). There is no CronJob and none is needed. It runs only where a worker runs, so it is as deployed as the worker (that is, nowhere outside the dev box today, feed-02). |
| Worker metrics and alerts? | Metrics exist (`:9091/metrics`: duration, results, routing, stall reclaims, queue wait, oldest pending). **Nothing scrapes them and no alert covers the worker**; the default alerts select the server container and deployment only (E-2). |
| `media.events.long` in MSK? | **Absent** from the topic job list with auto-create off. Long videos would then be transcoded on the reel queue (E-1). |
| `/v1/clips`, `/v1/series` (open question 10/11)? | `/v1/series` **is routed** by the gateway (`api-gateway/cmd/server/main.go:573`); the map was wrong. `/v1/clips` has no gateway prefix and is unreachable from outside. Neither is called by Android or web (E-14). |

## Findings

```
ID:            reels-E-1
Title:         media.events.long is not in the MSK topic list and auto-create is off, so in production every long video is transcoded inside the reel consumer and reels wait behind it again.
Severity:      P1
Category:      deployment
Evidence level: VERIFIED-READ (topic absent; fallback path). SUSPECTED (exact kafka-go behaviour on the missing topic).
Evidence:      infra/terraform/modules/msk/topics-job/variables.tf:29-59: 27 topics incl. media.events, media.events.dlq, media.copyright.pairs; no media.events.long. infra/terraform/modules/msk/main.tf:147 auto.create.topics.enable=false. No env overrides `topics` (grep "topics =" over infra/terraform/envs: no match).
               Architecture/docker/init-topics.sh:54 creates it locally, so dev and CI compose never see the gap.
               deploy/services/media-service/values-prod.yaml:176 MEDIA_TRANSCODE_CLASSES "short,long"; the comment at :174 says "The topic must exist first".
               media-service/cmd/worker/transcode_classes.go:214-221: a failed forward is logged, counted (media_transcode_routed_total{outcome="forward_failed"}) and the request is transcoded "here", i.e. in the short loop. :50-54 states that as the design ("slower for the reels behind it, never lost").
               cmd/worker/main.go:220-229: the long loop opens a group reader on the missing topic; consume() (transcode_classes.go:231-239) logs fetch errors and loops.
How to reproduce: Render the topics job for prod and list topics; or on a broker with auto-create off, start the worker and confirm one long upload: expect forward_failed and the reel queue on that partition blocked for the transcode's duration (hours for a long video).
Impact:        The 7 Nov class split (reels never wait behind long videos) does nothing in production. A 2-hour long video occupies the short consumer of its partition for hours; every reel hashed to that partition waits. Reels appear "stuck in processing" with no error. Nothing is lost.
Fix:           Add "media.events.long" to the topics list in modules/msk/topics-job/variables.tf (the job re-runs on a new topic). Optionally make the worker refuse to start when the long class is configured and the topic does not exist.
Acceptance:    A test (or the render guard) asserting every topic named in code (media.events.long included) is in the MSK list; on QA, a long upload shows outcome="forwarded".
Effort:        S
```

```
ID:            reels-E-2
Title:         The transcode pipeline is unmonitored: the worker's metrics are never scraped and no alert covers worker crashes, queue age, quarantine or stall give-ups.
Severity:      P1
Category:      observability
Evidence level: VERIFIED-READ
Evidence:      Worker metrics exist: cmd/worker/main.go:39-63 (media_transcode_total, media_transcode_duration_seconds, own server on :9091), readiness_metrics.go:27-46 (media_upload_ready_latency_seconds, media_transcode_queue_wait_seconds, media_transcode_oldest_pending_age_seconds, media_transcode_queue_oldest_waiting_seconds), transcode_lease.go:49-52 (media_transcode_stall_reclaims_total), transcode_classes.go:81-84 (media_transcode_routed_total).
               Not scraped: the worker container declares no port (worker-deployment.yaml:84-116); the chart has one Service (service.yaml, port "http" -> the server's named port) and one ServiceMonitor (servicemonitor.yaml:16-19, port http). The worker template says "NO SERVICE" (:23-26).
               Not alerted: prometheusrule.yaml:42-74 select container=<service.name> ("media-service") and deployment=<service.name>; the worker's container is "media-service-worker" and its Deployment "media-service-worker" (worker-deployment.yaml:32,85). values-prod.yaml for media-service has no prometheusRule block. grep "media_transcode" over deploy/ and charts/: no match.
               Quarantine (media_transcode_quarantine) has no metric and no alert; a message there is visible only in the database.
How to reproduce: Read the files; or render the chart for media-service prod and search the PrometheusRule for "worker" (none).
Impact:        A crash-looping worker, a Rekognition outage failing every scan, a stalled partition, or the E-1 regression produce no signal. Users see reels stuck in processing; the team learns from them.
Fix:           Add `containerPort: 9091` (name metrics) to the worker container, a PodMonitor (or a headless Service + ServiceMonitor) selecting component=worker, and in values-prod.yaml rules: worker CrashLoop/restarts (container=media-service-worker), worker Deployment replicas available == 0, media_transcode_oldest_pending_age_seconds > 1800 for 10m, increase(media_transcode_total{result="failure"}[30m]) > N, increase(media_transcode_stall_reclaims_total{action="give_up"}[1h]) > 0, media_transcode_routed_total{outcome="forward_failed"} > 0. Export a quarantine row count from runReadinessGauges.
Acceptance:    render_test asserts the worker is scraped and that rules exist for container=media-service-worker; on QA, scaling the worker to 0 with one upload pending fires within 30 minutes.
Effort:        S
```

```
ID:            reels-E-3
Title:         The worker's 50 Gi scratch emptyDir is backed by node disk that is probably 20 GiB, its free-space check cannot see the emptyDir limit, and the short and long loops share it, so a large upload can get the pod evicted mid-job.
Severity:      P1
Category:      deployment
Evidence level: SUSPECTED
Evidence:      deploy/services/media-service/values-prod.yaml:166 scratchSizeLimit 50Gi, :155 two replicas, no nodeSelector or anti-affinity (worker nodeSelector {} in charts/atpost-service/values.yaml:249, so no placement at all).
               infra/terraform/modules/eks/main.tf:15-16 terraform-aws-modules/eks ~> 20.29; :77 disk_size = 100 in eks_managed_node_group_defaults with no use_custom_launch_template=false and no block_device_mappings. In that module, managed node groups use a custom launch template by default and disk_size is then ignored, which leaves the AMI's default root volume (20 GiB on AL2023).
               values-qa.yaml (media) :119-126 says it itself: "statfs cannot see an emptyDir sizeLimit, so the cap is what keeps a huge upload from getting the pod evicted"; prod sets no MEDIA_WORKER_MAX_ORIGINAL_BYTES, so the 16 GiB default applies (processing/original_cap.go:18-24) and the 3x headroom check (cmd/worker/transcode_guards.go:61-63, 248-255) needs 48 GiB, measured against the node filesystem.
               Each replica runs the short and the long loop concurrently (cmd/worker/main.go:220-252) on the same /tmp, plus the fingerprint runner (main.go:198, os.TempDir()). A 16 GiB long original (48 GiB) and a 5 GiB reel (15 GiB) need 63 GiB > 50 Gi.
What would confirm it: `terraform plan` of envs/prod and read the launch template's root volume size; or on QA, `kubectl debug node` and `df -h /var/lib/kubelet`.
How to reproduce: Upload a 10 GiB long video on QA (cap 6 GiB there, so use prod limits) while a reel is processing on the same worker.
Impact:        Kubelet evicts the worker (ephemeral storage over the limit, or node disk pressure that also evicts other services on the node). Both in-flight jobs restart on another pod; a long original that cannot fit retries until the sweeper gives up and the upload fails.
Fix:           In modules/eks set `use_custom_launch_template = false` or a block_device_mappings root volume of at least 100 GiB for the general group; set MEDIA_WORKER_MAX_ORIGINAL_BYTES in values-prod.yaml to a value with 3x headroom for two concurrent jobs (with 50 Gi and two loops at 3x, about 8 GiB; or raise scratchSizeLimit to fit the 16 GiB cap), and add pod anti-affinity so two workers do not share a node's disk.
Acceptance:    terraform plan shows the root volume size; a render guard asserts 3 x MEDIA_WORKER_MAX_ORIGINAL_BYTES x 2 loops <= scratchSizeLimit in every values file.
Effort:        S
```

```
ID:            reels-E-4
Title:         A Rekognition outage re-encodes every queued video up to 32 times, blocks the reel queue for about 15 minutes per message, and ends with the uploads marked failed.
Severity:      P1
Category:      reliability
Evidence level: VERIFIED-READ (code path). SUSPECTED (timings, computed not measured).
Evidence:      cmd/worker/main.go:985-1004: the safety scan runs at the END of runTranscode, after every MP4 rung and the HLS ladder are encoded and uploaded. classifyScanError (:1032-1037): any provider error other than an input refusal is transient.
               A transient error returns from processMessage (main.go:388-391) and retryUntilDurable (transcode_guards.go:130-167) re-runs the WHOLE message (download, encode, upload, scan) up to 8 times, 2 s doubling to 2 min (main.go:561-564), serially on that partition, then quarantines and commits.
               The asset stays 'processing' with a heartbeat that goes stale; the stall sweeper re-queues it after 10 min (store/postgres/transcode_lease.go:69-76), up to 3 times, then failStalledTranscodeTx (:431-485) marks it failed + manual_review.
               No circuit breaker or provider health check around the scanner (grep "breaker" in internal/processing/scanner*: none read; not found in the worker).
How to reproduce: On dev, point MEDIA_SCANNER_BACKEND at an unreachable Rekognition endpoint and upload one reel; count ffmpeg runs in the worker log and read the final processing_status.
Impact:        During a provider outage longer than about an hour, every reel uploaded in it ends "failed" and must be re-uploaded; CPU is spent on 4 x 8 full encodes per video and every other reel on the partition waits behind the retries. Safety is preserved (nothing is published), availability is not.
Fix:           Smallest: extract and scan the sample frames from the original BEFORE the encode (the frames already come from inputPath), so a scan retry costs seconds, not a full transcode; and on a transient scan failure after the attempt cap, complete the transcode as ready with moderation_status 'manual_review' (not publishable, as today) and queue a re-scan, instead of failing the upload.
Acceptance:    Unit test: a scanner that fails 3 times then succeeds causes exactly one encode and ends ready+passed; a scanner down for the whole cap ends ready+manual_review, not failed.
Effort:        M
```

```
ID:            reels-E-5
Title:         The worker cancels in-flight ffmpeg immediately on SIGTERM, so the 300 s grace period is unused and every rollout or drain restarts all in-flight transcodes from the beginning.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      cmd/worker/main.go:161-168: on SIGINT/SIGTERM cancel() the root ctx. ffmpeg runs under exec.CommandContext with that context (internal/processing/tool.go:177), so it is killed. retryUntilDurable returns false on ctx.Err() (transcode_guards.go:138-140) and consume() stops without committing (transcode_classes.go:244-249): the message is redelivered (no loss, no duplicate, sound).
               worker-deployment.yaml:83 terminationGracePeriodSeconds 300 and its comment at values.yaml:245-247 ("Long enough for an in-flight transcode to finish") do not match the code. :55 strategy Recreate: all workers stop before new ones start.
               Long-video jobs may run up to 48 h (processing/limits.go:157-158).
How to reproduce: Send SIGTERM to the worker during a transcode; the log shows "Context cancelled before commit" at once.
Impact:        Reels (at most 10 min) lose minutes of work per deploy and see a gap of zero workers during every rollout. A multi-hour long video restarted by each daily deploy may never finish. No node is spot (eks/main.tf:76 ON_DEMAND), so drains come from deploys, upgrades and scale-downs.
Fix:           On SIGTERM stop fetching new messages but let the current handle() run on a separate job context until it finishes or until grace - 30 s, then cancel. Keep Recreate.
Acceptance:    Test: SIGTERM during a fake 5 s job: the job completes and its offset is committed; SIGTERM during a job longer than the drain budget leaves the offset uncommitted.
Effort:        S
```

```
ID:            reels-E-6
Title:         Nothing restarts a worker whose consumer has hung, and nothing reports it.
Severity:      P2
Category:      reliability
Evidence level: SUSPECTED
Evidence:      No liveness probe on the worker (values.yaml:248 livenessProbe: {}, no prod override). consume() (transcode_classes.go:229-256) has no progress signal; the stall sweeper re-queues assets of a dead job but runs inside the same process. The readiness gauges (readiness_metrics.go) would show the age rising, but are not scraped (E-2).
What would confirm it: on dev, block the worker's Kafka connection (or its Postgres advisory lock) and watch: the pod stays Running and uploads stay processing.
How to reproduce: As above.
Impact:        A wedged worker holds its partitions (no rebalance, the group member is alive) until a person notices; reels on those partitions stop processing.
Fix:           Record the time of the last fetch return and the last completed message; serve /livez on the :9091 mux that fails when the consumer has neither fetched nor been inside a job for 10 minutes; set worker.livenessProbe in values-prod.yaml with a generous failureThreshold.
Acceptance:    Unit test of the liveness handler; on QA, a blocked consumer is restarted.
Effort:        S
```

```
ID:            reels-E-7
Title:         No procedure restores the media database and the media bucket together; restoring one without the other leaves rows without objects or objects nothing will ever delete.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ
Evidence:      Bucket: versioning on, noncurrent versions kept 90 days, no cross-region replication, no AWS Backup plan, no prevent_destroy (infra/terraform/modules/media/main.tf:31-178; grep "replication|backup|prevent_destroy" in modules/media: none). docs/runbooks/restore-aurora.md, service-rollback.md, service-alerts.md: no mention of the media bucket, S3 or media assets (grep).
               After an Aurora point-in-time restore to T: rows deleted after T come back while their objects were removed by the blob reclaim worker (service/deletion.go:156-226) and are noncurrent versions only, so CloudFront answers 403/404 for them; objects uploaded after T have no row, and the orphan GC only reclaims rows at pending_upload (service/orphan_gc.go:38-43), so those objects are kept and billed for ever.
               The reverse (bucket lost, database intact): every ready reel points at missing renditions and originals; nothing detects it.
How to reproduce: Read the files.
Impact:        A restore drill would "succeed" on row counts and still leave broken or orphaned media; a bucket deletion has no copy to restore from after the noncurrent window.
Fix:           Add a section to restore-aurora.md: after a PITR, run a reconciliation that (a) restores the noncurrent version for each media_assets key whose current object is missing, within 90 days, and (b) lists objects under user/ with no media_assets row for deletion. Add prevent_destroy to the bucket. Decide on cross-region replication (founder; cost).
Acceptance:    A dated QA drill: PITR of the media database, reconciliation run, every ready reel plays.
Effort:        M
```

```
ID:            reels-E-8
Title:         A reel cannot be traced from the upload request through the outbox, the worker job and post-service's review gate: no request id is stored with the event and the worker logs plain text.
Severity:      P2
Category:      observability
Evidence level: VERIFIED-READ
Evidence:      media-service non-test Go code mentions X-Request-Id/request_id in one file only (internal/http/trending_sounds_handler.go). The outbox event (store/postgres/media_outbox.go) carries no request id; the delivery gate's call to post-service (internal/delivery/authz.go) does not forward it. No OTel init in media-service/cmd (grep "otel|o11y/trace": none) although values-prod.yaml:89 sets OTEL_EXPORTER_OTLP_ENDPOINT. The worker logs with log.Printf (88 calls across cmd/worker/*.go), not JSON.
               Mitigation that exists: every worker log line names the media id, and the server's access log is JSON with request_id (shared/middleware).
How to reproduce: Upload a reel with X-Request-Id: test-1; search worker and post-service logs for test-1: nothing.
Impact:        "My reel is stuck" means joining by media id by hand across three services' logs; a slow playback cannot be tied to the post-service gate call.
Fix:           Put the request id in the MediaTranscodeRequested envelope (the envelope has metadata fields) and log it in the worker; switch the worker to the shared slog JSON logger; forward X-Request-Id on the gate's HTTP client (the shared RoundTripper fix from feed E-12 covers it).
Acceptance:    One request id appears in media-service, worker and post-service logs for one upload.
Effort:        S
```

```
ID:            reels-E-9
Title:         media-service's own shutdown cuts resumable chunk uploads and leaves its background workers to be killed: 10 s drain against 10-minute request timeouts, and the workers run on a context that is never cancelled.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      cmd/server/main.go:470-479 ShutdownTimeout 10 s while ReadTimeout and WriteTimeout are 10 min for 64 MiB chunks; OnShutdown closes Redis, the producer and the pool first (:480-486; ordering is inherited feed E-8). The chart sets no terminationGracePeriodSeconds on the server (default 30 s).
               ctx := context.Background() (:62) is passed to the outbox relay (:189), orphan GC (:181), caption worker (:203), audio-track worker that runs ffmpeg muxes in the server pod (:218), blob reclaim (:222), trending (:307) and the account-lifecycle consumer (:235); none is cancelled on shutdown.
How to reproduce: SIGTERM a media-service pod during a resumable chunk upload.
Impact:        Every rollout and HPA scale-down (2 to 6 replicas) fails in-flight chunk and /complete requests and kills in-flight dubbing muxes. Clients that resume (the resumable protocol) recover; the cost is a retried 64 MiB chunk. Workers are idempotent by design (SKIP LOCKED claims), so no data is lost (not verified per worker).
Fix:           Shared fix from feed E-8 (Shutdown before OnShutdown, preStop sleep); for media-service set ShutdownTimeout to about 60 s, terminationGracePeriodSeconds 75 in values, and derive the worker ctx from a context cancelled on SIGTERM.
Acceptance:    Rolling restart on QA during a 500 MB resumable upload: the upload completes without a client-visible error.
Effort:        S
```

```
ID:            reels-E-10
Title:         Every reel playback, public ones included, depends on post-service answering the media-access question; when post-service is down all video answers 503.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      Gate call: internal/delivery/authz.go (3 s client, map 9.1 step 3); signed-out viewers go through the same gate (cmd/server/main.go:167-172). Unresolved authorization is 503, a denial 404 (internal/http/delivery_viewer.go:34-60). The per-pod sign cache reuses a signature for 2 min (delivery/sign_cache.go:52-58), which softens only repeat requests for the same asset.
               Clients: Android re-requests on an expired signature (core/media/.../HlsRefresh.kt:56,173); web ReelVideo listens for the element error (ReelVideo.tsx:499-527). What either shows the user on a 503 was not read.
How to reproduce: Stop post-service on dev and open /reels.
Impact:        A post-service outage or deploy failure is a full reels outage (the feed also fails, since post-service hydrates it). Fail-closed is the deliberate privacy choice and is correct; the cost is that there is no degraded mode.
Fix:           Keep fail-closed. Smallest improvement: cache positive public-and-approved answers per media id for 60 s in the gate (invalidated by the existing visibility events), so playback of public reels survives a short post-service blip.
Acceptance:    Test: gate answers public media from cache when post-service errors within 60 s of a positive answer, and never serves a private one from cache.
Effort:        M
```

```
ID:            reels-E-11
Title:         The chart's static QA guard fails today on media-service: QA sets MEDIA_WORKER_MAX_ORIGINAL_BYTES and prod does not.
Severity:      P2
Category:      deployment
Evidence level: VERIFIED-RUN
Evidence:      `QA_GUARDS_ONLY=1 bash charts/atpost-service/render_test.sh` → "FAIL QA: env drifts from prod: deploy/services/media-service/values-qa.yaml: adds MEDIA_WORKER_MAX_ORIGINAL_BYTES (not in prod)", final "STATIC GUARDS FAILED", exit 1. Every other static guard printed ok.
How to reproduce: Run the command.
Impact:        The guard that keeps QA equal to prod is red, so a real drift would not stand out; and prod runs on an implicit 16 GiB default that E-3 says may not fit.
Fix:           Set MEDIA_WORKER_MAX_ORIGINAL_BYTES explicitly in values-prod.yaml (value per E-3), which also clears the guard.
Acceptance:    The same command ends without STATIC GUARDS FAILED.
Effort:        S
```

```
ID:            reels-E-12
Title:         ffmpeg, which parses every untrusted upload, is installed unpinned from Alpine 3.19 in both media images and no image scan runs.
Severity:      P2
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      media-service/Dockerfile.worker:25-26 `FROM alpine:3.19` / `RUN apk add --no-cache ffmpeg`; media-service/Dockerfile:22-26 the same (the server runs ffmpeg for frames, liveness and audio muxes). security.yml runs trivy fs, not an image scan (map section 7). Alpine 3.19 reached end of support for its main repository in November 2025 (from general knowledge; confirm on alpinelinux.org).
How to reproduce: Read the Dockerfiles.
Impact:        Two builds of one commit can ship different ffmpeg builds (different renditions, different CVEs); a published ffmpeg demuxer CVE has no tracking path. The input whitelist and per-format demuxer in processing/tool.go reduce the exposure but do not remove it.
Fix:           Move both images to a supported Alpine release pinned by digest, pin `ffmpeg=<version>`, and add `trivy image` for media-service and media-worker to build-push.yml.
Acceptance:    Dockerfiles reference @sha256 and a versioned ffmpeg; the build log shows a trivy image result.
Effort:        S
```

```
ID:            reels-E-13
Title:         The worker runs at a fixed two replicas with no autoscaling, so reel processing throughput is capped at two concurrent transcodes however long the queue gets.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ
Evidence:      values-prod.yaml:153-176 replicaCount 2, no HPA (hpa.yaml targets the server Deployment only, :10-13); no KEDA object anywhere in charts/ or deploy/ (grep "ScaledObject": none). media.events has 6 partitions (topics-job/variables.tf:62-66), so up to 6 short consumers could work in parallel. Requests 1 CPU, limit 3 CPU, 4 Gi per worker on m7g.xlarge (4 vCPU) nodes.
How to reproduce: Read the files.
Impact:        An upload burst (a campaign, a creator import) queues on Kafka; time to playable grows linearly. Nothing alerts on it (E-2). Launch load is unknown (Track D).
Fix:           After E-2 lands, add a KEDA ScaledObject (Kafka lag on media-transcode-worker, 2 to 6 replicas) or, without KEDA, raise replicaCount to match the forecast. Keep Recreate semantics in mind (scaling is not a rollout).
Acceptance:    Load test from Track D shows queue wait p95 under the target at the forecast upload rate.
Effort:        M
```

```
ID:            reels-E-14
Title:         /v1/clips is a registered but unreachable route, and /v1/series is reachable but used by no client.
Severity:      P3
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      /v1/series: gateway prefix exists, api-gateway/cmd/server/main.go:563-573 (comment: added because the series roots 404'd) and internalroutes.go:391 stamps the key. post-service registers it (handler.go:347-357). No caller in mobile/android (grep "v1/series": none; Android uses /v1/video-series for Tube) or in pb-echo src.
               /v1/clips: media-service clips_handler.go:52-55 registers it (writes refused without POST_SERVICE_URL, cmd/server/main.go:140-144); grep "clips" in the gateway: no prefix. No client calls it.
How to reproduce: grep as above.
Impact:        Dead surface to maintain and audit; /v1/clips is reachable by anything that can reach media-service directly (network policy is off, map open question 5).
Fix:           Remove the /v1/clips routes (or route them if a product needs them); record /v1/series as a server-only feature until a client ships.
Acceptance:    Route table test in media-service no longer lists /v1/clips.
Effort:        S
```

```
ID:            reels-E-15
Title:         The media-service PodDisruptionBudget also selects the worker pods, so a drain can evict both API pods while a worker pod satisfies minAvailable.
Severity:      P3
Category:      deployment
Evidence level: SUSPECTED
Evidence:      pdb.yaml:13-16 selects atpost-service.selectorLabels (name + instance, _helpers.tpl:29-32); worker pods carry those labels plus component=worker (worker-deployment.yaml:58-60). values-prod.yaml:146-148 minAvailable 1. The server Deployment's own selector has no component label, so it would also count the worker pods as matching (Kubernetes warns about overlapping selectors). The Service is unaffected in practice because the worker has no port named http.
What would confirm it: render the chart and `kubectl get pdb media-service -o yaml` on QA: expectedPods 4 with 2 API and 2 worker pods.
How to reproduce: As above.
Impact:        A node drain can leave zero API pods (uploads and playback down) while the PDB reports healthy.
Fix:           Add `app.kubernetes.io/component: server` to the server Deployment's pod labels and to the PDB and Service selectors (a selector change needs a Deployment recreate, so do it before the first deploy), and give the worker its own PDB (maxUnavailable 1).
Acceptance:    render_test asserts the server PDB, Service and ServiceMonitor selectors exclude component=worker.
Effort:        S
```

```
ID:            reels-E-16
Title:         Every media-service server replica runs the outbox relay without a claim, so each transcode request can be published more than once.
Severity:      P3
Category:      reliability
Evidence level: SUSPECTED
Evidence:      service/media_outbox.go:14-56 lists unpublished rows every 2 s in each replica, publishes, then marks; the list query in store/postgres/media_outbox.go was not seen to lock rows (grep "SKIP LOCKED" in that file: none; the advisory lock at :175-193 is for a different operation, not read in full). Prod runs 2 to 6 replicas.
               Consumers absorb repeats: the worker dedupes by event id (inbox + per-event advisory lock, cmd/worker/main.go:289-301, 325-330); post-service's consumer uses the shared consumer's event-id dedupe.
What would confirm it: read ListUnpublishedMediaEvents; count messages per event id on dev with two replicas.
How to reproduce: As above.
Impact:        Duplicate Kafka traffic and log noise; no duplicate work found.
Fix:           SELECT ... FOR UPDATE SKIP LOCKED inside the relay pass, as graph-service and the fixed post-service relay do.
Acceptance:    Integration test with two relays: each row published once.
Effort:        S
```

## Dependency-down behaviour, per reels path

| Path | Dependency down | What the code does | User sees | Evidence |
|---|---|---|---|---|
| Upload init/confirm | Postgres | request fails; pod NotReady (readiness checks Postgres only) | error; client retries | `cmd/server/main.go:244-245` |
| Upload | Redis | upload budget fails open; boot without Redis disables the budget for the pod's life | upload works, no budget | `cmd/server/main.go:101-113`, `service/ratelimit.go` |
| Upload | Kafka | request goes to `media_event_outbox` in the confirm transaction; relay retries every 2 s | "processing" for longer; nothing lost | `store/postgres/media_outbox.go:30-89`, `service/media_outbox.go` |
| Upload | S3 | presigned PUT fails at the client; confirm's stat fails | client error; Android WorkManager retries (not read in detail) | `service/upload.go:221-400` |
| Processing | S3 blip | transient, retried with bounded per-call deadlines | delay | `transcode_guards.go:276-295` |
| Processing | Rekognition | transient, full re-encode per retry, then failed | **reel fails after about an hour** (E-4) | `cmd/worker/main.go:985-1037` |
| Processing | Postgres | transient retries; offset not committed | delay | `transcode_guards.go:130-167` |
| Processing | `media.events.long` missing | long videos transcoded on the reel loop | reels delayed for hours (E-1) | `transcode_classes.go:214-221` |
| Publish gate | post-service down | `media.events` waits in Kafka for `post-service-media-transcode`; post stays `pending` | reel not visible yet | `post-service/internal/consumers/media.go` |
| Reels feed | Scylla, graph, post, profile, media, trust-safety | as the feed audit: fail closed on graph; 503 `FEED_UNAVAILABLE` on hydration; keyword filter now falls back (17 Nov fix) | error with retry (web and Android retry controls; reels-screen retry at `ReelsScreen.kt:997`) | `feed-service/internal/http/handler.go:286-297`; feed fix plan |
| Playback | post-service | 503 for every video (E-10) | playback error | `delivery_viewer.go:34-60` |
| Playback | CloudFront | no fallback (anonymous-scope assets are streamed by media-service and still work) | playback error | `handler_anonymous.go:50-113` |
| Playback | Redis | not in the path | none | map 9.1 |
| Engagement | Redis/Scylla/Kafka | as feed E-11 (like is Redis first) and the 17 Nov fixes | — | feed track |
| View tracking | analytics-service | Android batches through `AnalyticsUploadWorker` (WorkManager); web telemetry best-effort; feed view counts fall back to zero | counts lag or show 0 | `core/analytics/.../AnalyticsUploadWorker.kt`; feed track "analytics" row |
| Dubbing | provider unset (prod) | `generate` answers 503 `DUBBING_UNAVAILABLE`; creator uploads still mux | button error | `cmd/server/main.go:205-218`, values-prod.yaml:116-118 |
| Copyright | fingerprint failures | separate loop with its own admission and fencing; never blocks a transcode or publish (shadow mode) | none | `cmd/worker/fingerprint_runner.go:197-245` |
| Search | OpenSearch | only the reels search page | search error | map 3.6 |

## Production configuration (media-service and media-worker), enumerated

| Setting | Prod value | Verdict |
|---|---|---|
| ENV / DEPLOY_ENV | prod / production | makes the internal key, the delivery gate and S3-only storage mandatory (`cmd/server/main.go:157-175, 279-286`; worker `main.go:144-150`) |
| Scanner | enabled, rekognition, ALLOW_STUB false; worker refuses to start on the stub before touching any dependency | sound (`cmd/worker/main.go:87-104`) |
| Storage | `MEDIA_STORAGE_BACKEND=s3`, IRSA, `S3_BUCKET` `__TF_MEDIA_BUCKET_NAME__` placeholder | placeholder must be filled (inherited feed-02) |
| CDN | `MEDIA_CDN_BASE_URL=https://media.cleestudio.com` while `media_custom_domain_enabled` defaults false (map 5.5) | signed URLs would point at a host that does not exist unless the Terraform flag is turned on: Track B/D already have this from the map; not re-raised |
| Signing keys | per environment by ExternalSecret (`atpost/prod/...` vs `atpost/qa/...`, guard ok) | sound |
| Bucket | private (4 public blocks), SSE-KMS, versioned; CORS methods GET/HEAD/PUT/POST from `var.cors_allowed_origins`, headers `*` | no wildcard origin found; allowed_headers `*` acceptable |
| Dubbing | unset; OpenAI key commented out | sound for launch |
| Copyright | fingerprint + matching true (shadow), relay off | as decided |
| Debug, stubs, seeds | none found in media values; QA guard "stub media/payments absent" ok | sound |
| Server resources | 250m/256Mi request, 1 CPU/1 Gi limit, HPA 2-6, PDB 1 | server pods also run ffmpeg (audio muxes, liveness, extract-frame) under the default `/tmp` emptyDir with no sizeLimit (deployment.yaml volumes `tmp: emptyDir: {}`): P3 note |
| Worker resources | 1 CPU/1 Gi request, 3 CPU/4 Gi limit, scratch 50 Gi, grace 300 s, Recreate, 2 replicas, both classes | E-3, E-5, E-13 |
| Node pools | general m7g.xlarge, on-demand, no worker-specific pool or taint | acceptable; disk size E-3 |
| Worker probes | none | E-6 |

## Section 5: rollback and migrations

- media-service applies `setup.sql` plus forward-only migrations at boot in **both** the server and the worker, serialised by a session advisory lock (`internal/store/postgres/bootstrap.go:50-90`). Sound against concurrent boots. Consequence for rollback: the worker of image N (Recreate) can migrate before the server pods roll; the rule "every migration works with N-1" (`docs/runbooks/service-rollback.md`) must hold for the worker too.
- `migration-compat.sh` over whole folders flags 29 media and 92 post statements; all are in migrations already applied on dev, and production will apply every file on an empty database, so none of them is a rollback risk at first launch. Over the newest files (post 080-085, media 030-032) it flags 5 `ADD-CONSTRAINT` statements: `081_tube_integrity.sql:55,80` (FK re-created with ON DELETE SET NULL; playlist primary key re-added after a renumber), `082_echo_notes.sql:30,52` (`chk_content_type` re-created to a wider list), `030_trending_sounds.sql:30` (unique rank index on a table the same file creates). All are compatible with N-1 as read (a wider CHECK, a new table, a looser FK). None touches the reels tables (`media_assets`, `posts` columns for flicks, `reel_drafts`, `video_metadata`).
- The moderation_status CHECK question (map open question 3): `setup.sql:57` adds the column with no CHECK before migration 008 runs, so 008's `ADD COLUMN IF NOT EXISTS ... CHECK` is a no-op on a fresh database and `manual_review` can be written. For Track C to confirm against a real schema.
- Rollback of the worker image alone: a worker of N-1 reading requests written by N is covered by the class-routing rollout note (`transcode_classes.go:50-54`); not tested.

## Checked and found sound

1. Offsets are committed only after the durable effect (completion row, quarantine row, or an acknowledged forward), and never on shutdown: `transcode_classes.go:229-256`, `transcode_guards.go:130-167`, `cmd/worker/main.go:203-217`. VERIFIED-RUN: worker tests ok.
2. Replays are absorbed: per-event advisory lock, inbox check, superseded-delivery check, generation check in `CompleteTranscode`: `cmd/worker/main.go:289-335, 454-482`, `transcode_lease.go:223-234`.
3. Poison messages cannot stall a partition: undecodable envelopes and payloads are permanent and quarantined (`main.go:260-286`); transient errors are capped at 8 attempts (`transcode_guards.go:53-60, 141-153`).
4. A dead job is recovered: heartbeat every 30 s, stale after 10 min, re-queue with a fresh event under `FOR UPDATE SKIP LOCKED`, give up after 3 with `failed` + a reason on `transcoding_jobs` that `GET /v1/media/:id/status` returns (`store/postgres/transcode_lease.go:68-76, 224-485`). A job past its own budget stops renewing the lease (`transcode_lease.go:77-113`). VERIFIED-RUN: stall tests ok.
5. A never-started request is not mistaken for lost work while the pipeline is busy (`ClassifyStalledTranscode`, `transcode_lease.go:142-153`).
6. Every ffmpeg/ffprobe has a deadline, a `file`-only protocol whitelist and an explicit demuxer (`internal/processing/tool.go:13-44`, `limits.go:120-158`); every S3 call has a size-scaled deadline (`transcode_guards.go:276-295`); stale scratch is swept at start (`main.go:172-176`).
7. The worker refuses to start with the stub scanner in production, before connecting to anything (`main.go:87-104`), and refuses MinIO in production (`main.go:144-150`).
8. There is no default "passed": no verdict means `manual_review` (`main.go:433-441`).
9. A worker that cannot forward to the long topic transcodes the request itself, so E-1 delays work but loses none (`transcode_classes.go:214-221`).
10. The upload path survives a Kafka outage through the transactional outbox (`store/postgres/media_outbox.go:30-89`).
11. Server probes: `/livez`, `/readyz`, `/healthz` registered by `shared/health` before any auth middleware (`cmd/server/main.go:446`); the chart's probe guard passes (VERIFIED-RUN, render_test static section).
12. Worker pod hardening: non-root 10001, read-only root, all capabilities dropped, bounded scratch (`worker-deployment.yaml:69-101`); its ServiceAccount is the service's own IRSA role, no broader.
13. Per-environment configuration is values-only; QA is prod with QA data, enforced by the static guards (all but E-11 ok, VERIFIED-RUN).
14. The web upload studio tells the creator when processing failed (`pb-echo/src/features/upload/useUploadStudio.ts:282-303`).

## Not verified

- Anything on a live cluster, broker or AWS account: whether `media.events.long` was created by hand anywhere, the real node root volume (E-3), PDB behaviour (E-15). No kubectl, Terraform, AWS or Kafka command was run.
- Helm render of the chart (only the static guards ran; `QA_GUARDS_ONLY=1`).
- kafka-go's exact behaviour on the missing long topic (writer error timing, group reader loop) for E-1.
- The Android reel publish pipeline's message to the creator when processing fails or the post is rejected after upload (`ReelPublishWorker.kt`, `ReelPublishPipeline.kt` did not match a status grep); what Android and web show on a 503 from playback.
- `ListUnpublishedMediaEvents` body (E-16); the caption, audio-track and blob-reclaim workers' restart safety beyond their SKIP LOCKED claims.
- Rekognition retry timings in E-4 are computed from the constants, not measured.
- Dev smoke and any deployed environment: forbidden by the rules.
- CI state of media-service unit and integration tests at HEAD (not queried with gh in this track).

## For the other tracks

- Track B: `/v1/clips` is reachable by anything that can reach media-service directly (no gateway prefix, network policy off). Media bucket noncurrent versions keep deleted reels for 90 days (deletion and DPDP erasure).
- Track C: open question 3 is answered in code (no CHECK on a fresh database); confirm on a real schema.
- Track D: worker throughput is fixed at two concurrent short transcodes (E-13); E-1 puts long videos on the reel queue.
- Track A: the "reel stuck in processing" journey depends on E-1, E-2 and E-4.
