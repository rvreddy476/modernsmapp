# Track E: reliability, observability and deployment (long video)

- **Audit date:** 2026-11-17. Read-only. Tree: `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` (dirty working tree read as it is). Web tree read: `C:\workspace\wt\pb-echo` (`feat/echo-web`).
- **Scope:** harness sections 4.7 and 4.8 for the long-video paths: the media worker's long class, the chart and values that run it, Karpenter and EKS disruption, post-service (channels, progress, schedule publisher, copyright hold, RSS source), notification-service subscriber fan-out, the web RSS route and the two players' failure states. The shared upload and transcode pipeline is covered by the reels track (`docs/production-readiness/reels/tracks/track-e-reliability.md`); only what changes for long video is reported here.
- Backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Web paths are relative to `C:\workspace\wt\pb-echo`.
- **Executed (and nothing else):** see "What I ran" at the end.

## Inherited from reels (re-checked 17 Nov, not re-counted)

Each item was re-read at the cited lines; the state is unchanged unless noted. Long-video note in the last column.

| Reels ID | What | Re-check | Long-video consequence |
|---|---|---|---|
| reels-3 | `media.events.long` absent from the MSK topic list, auto-create off | still absent: `infra/terraform/modules/msk/topics-job/variables.tf:39-58` (only `media.events`, `media.copyright.pairs`, `media.events.dlq`), `modules/msk/main.tf:147`; present locally only (`Architecture/docker/init-topics.sh:54`) | every long video transcodes on the reel loop (`media-service/cmd/worker/transcode_classes.go:214-221`); LV-E-1 to LV-E-3 then also stall reels |
| reels-5 | worker metrics never scraped, no worker alert, kube rules on the `null` receiver | still no container port or PodMonitor (`charts/atpost-service/templates/worker-deployment.yaml`, no `ports:`); `grep media_transcode` over `deploy/` and `charts/`: none; `infra/terraform/modules/observability/alerting.tf:83` root receiver `null`, chart alerts routed to `ops-email` (`:46-57`) but kube-prometheus rules only when `route_platform_critical` | a long job that dies or loops (LV-E-2) produces no signal |
| reels-35 | worker scratch may sit on a ~20 GiB node disk | **partly changed:** Karpenter nodes get a 100 Gi gp3 root (`infra/terraform/modules/karpenter/main.tf:123-131`); the managed group still relies on `disk_size = 100` that the EKS module may ignore (`modules/eks/main.tf:77`) | the 3x headroom check passes on a 100 Gi node while the 50 Gi emptyDir limit is what evicts (LV-E-3) |
| reels-34 | a Rekognition outage re-encodes each video up to 32 times | unchanged: scan after the full ladder (`cmd/worker/main.go:985-1004`), 8 attempts (`transcode_guards.go:53-60`), 3 sweeper re-queues (`store/postgres/transcode_lease.go:69-76`) | each retry is a full multi-hour encode, not minutes |
| reels-56 | worker cancels ffmpeg at once on SIGTERM; 300 s grace unused | unchanged (`cmd/worker/main.go:161-168`, `worker-deployment.yaml:55,83`) | LV-E-1 |
| reels-57 | nothing restarts a hung worker | unchanged (`charts/atpost-service/values.yaml` `worker.livenessProbe: {}`) | a hung long loop holds its partitions for good |
| reels-58 | no joint restore of the media database and bucket | unchanged (`docs/runbooks/restore-aurora.md` no media section) | long originals (up to 16 GiB) are the costliest objects to lose |
| reels-59 | no request id through outbox and worker; worker logs plain text | unchanged (worker uses `log.Printf`) | a "my video is still processing after a day" ticket is joined by media id by hand |
| reels-60 | media-service server shutdown cuts resumable chunk uploads | unchanged | a 16 GiB upload has ~250 chunks, so it sees every rollout |
| reels-61 | all playback answers 503 when post-service is down | unchanged | same for long video; see the dependency table |
| reels-62 | chart QA guard red on `MEDIA_WORKER_MAX_ORIGINAL_BYTES` | still red (VERIFIED-RUN, below); a second failure has appeared (LV-E-11) | QA refuses any long original over 6 GiB, so QA cannot reproduce LV-E-2 or LV-E-3 |
| reels-63 | ffmpeg unpinned on Alpine 3.19, no image scan | unchanged | — |
| reels-4 | two workers, no autoscaling | unchanged (`deploy/services/media-service/values-prod.yaml:153-155`) | two long transcodes at a time platform-wide (one long loop per pod, `cmd/worker/main.go:220-252`) |
| reels-103 | the media PDB also selects worker pods | unchanged (`values-prod.yaml:146-148` `minAvailable: 1`) | a drain may evict both workers at once |
| reels-92 | deleting an asset does not abort its multipart upload | unchanged; bucket rule `abort-incomplete-multipart` after 7 days covers it (`infra/terraform/modules/media/main.tf:167-176`, VERIFIED-READ) | parts of an abandoned 16 GiB upload are billed up to 7 days |
| reels-98 | scheduled posts retry for ever on a persistent error; parked posts not notified | unchanged (`post-service/internal/postschedule/worker.go`, Tick logs only) | applies to scheduled long videos |

## Inherited from the feed audit (platform-wide, not re-counted)

| Feed ID | What | State on 17 Nov | Long-video note |
|---|---|---|---|
| feed-01 | chart liveness `/livez` not served by post-, feed-, graph-service | **closed:** the static guard prints `ok probes: ... every service outside the known gaps serves both` (VERIFIED-RUN); commit `3d008bdbe` | — |
| feed-02 | no QA or production environment; no image ever built | open | the media-worker image included |
| feed-10 | alert rules written, unrendered, unapplied; one e-mail receiver | config written (`envs/prod/main.tf:362-363`) | covers server containers only; nothing long-video-specific (LV-E-7) |
| feed-11 | rollback runbook written, never drilled; `migration-compat.sh` not in CI | open | the runbook has no step for in-flight transcodes (LV-E-1) |
| feed-12 | no Scylla backup; no restore ever tested | open | OpenSearch tube indexes have no restore runbook, but are rebuildable from Postgres (`search-service/internal/reindex/tube.go:63-195`, manual) |
| feed-42 | services close the DB before draining HTTP | open (`Architecture/shared/server/server.go:87-91`) | — |
| feed-43 | readiness fails on any shared store | open: post-service readiness checks Postgres, Redis, Scylla (`post-service/cmd/server/main.go:343-348`) | a Redis blip takes every channel, progress and playlist route out, although progress itself falls back to the database (`post-service/internal/service/watch_progress.go:68-108`) |
| feed-46 | request id not forwarded beyond the gateway | open: the gateway sets and forwards `X-Request-Id` (`api-gateway/cmd/server/main.go:1271-1281`); no service forwards it on its own calls (`grep X-Request-Id` in post-, media-, notification-service: none) | gateway → post is joined; post → media, notification, trust-safety are not |
| feed-74 | datastore clients on defaults | open | `GetWatchProgress` calls Redis with no deadline of its own (`watch_progress.go:70`) |
| feed-76 | builds not fully pinned | open | — |
| feed-77 | staging values stale; `promote.yml` reads tags from them | open | long-video drift in LV-E-10 |

## Short answers

| Question | Answer | Evidence |
|---|---|---|
| Health and readiness | Server probes are served and guarded (VERIFIED-RUN). The worker has no probe of either kind by design. | chart guard output; `worker-deployment.yaml:117-126` |
| Kill mid-transcode | ffmpeg is killed with the root context, the offset stays uncommitted, the message is redelivered and **re-run from the first rung**. Nothing is lost, nothing is published twice (inbox + generation checks). No rung is reused. | `cmd/worker/main.go:161-168`; `transcode_classes.go:244-249`; `cmd/worker/main.go:740-783` (rungs recreated every run) |
| Lease and sweeper | Heartbeat every 30 s; stale after 10 min; up to 3 re-queues, then `failed`. A job past its budget stops renewing and is reclaimed. | `cmd/worker/transcode_lease.go:33-46,77-113`; `store/postgres/transcode_lease.go:69-76` |
| 48 h limit | Every long video (over ~11.5 min) gets the full 48 h job budget; each ffmpeg call is capped at 24 h. Hitting the job budget is a **permanent** failure. | `internal/processing/limits.go:123-158`; `cmd/worker/main.go:597-604` |
| Pod eviction, OOM | Same as a kill, but no counter sees it: LV-E-2. | |
| Node drain, consolidation, spot | Prod is on-demand only (`envs/prod/variables.tf:277-280`), so no spot interruption. **Karpenter consolidation (`WhenEmptyOrUnderutilized`, 30 s) can move a worker at any time** and the worker pods carry no `karpenter.sh/do-not-disrupt`; the PDB lets both workers go: LV-E-1. | `modules/karpenter/main.tf:184-187`; `values-prod.yaml:146-148`; `grep do-not-disrupt` repo-wide: none |
| Scratch for 16 GiB + ladder | Not sized: LV-E-3. | |
| Multipart abort lifecycle | Present: 7 days, every prefix. App side aborts expired resumable sessions after 24 h (best effort). | `infra/terraform/modules/media/main.tf:167-176`; `media-service/internal/service/resumable.go:38` |
| Structured logs, request id | Server logs JSON with request id; worker logs plain text; id not forwarded past the gateway (inherited). | reels-59, feed-46 |
| Metrics and alerts for long video | Service-wide HTTP rate, 5xx ratio and p99 only; transcode metrics unscraped, unlabelled by class, and the duration histogram stops at 600 s: LV-E-7. | |
| Dependency down | Table below. | |
| Scheduled publish, fan-out, RSS, Copyright Match | In-process loops in post-service, notification-service and the media worker; no CronJob. Fan-out loses notifications after ~30 s of trouble (LV-E-5); fingerprinting starves behind long transcodes (LV-E-6). | section "Background work" |
| Env config per environment | Values-only. QA equals prod for media, post, feed, search, notification, user and analytics except `MEDIA_WORKER_MAX_ORIGINAL_BYTES` (VERIFIED-RUN diff). Staging and Azure drift: LV-E-10. | |
| Debug or permissive flags in prod | None found in the seven services' prod values (`LOG_LEVEL: info`; only `MEDIA_SCANNER_ALLOW_STUB: "false"` matches the scan). | `deploy/services/*/values-prod.yaml` |
| Feature flag for long video | None, server or client: LV-E-9. | |
| Rollback exercised | No (feed-11). Long-video migrations are compatible at first launch (section "Migrations"). | |
| Backups | Inherited (feed-12, reels-58). | |
| View counting drift | Prod and QA `sessions`; staging and Azure fall back to `play_end`: LV-E-10. | |

## Findings

```
ID:            LV-E-1
Title:         A multi-hour long-video transcode has to run uninterrupted for up to two days, but the platform interrupts workers routinely (Karpenter consolidation every 30 s of under-use, Recreate rollouts, node expiry and upgrades), and every interruption restarts the job from the first rung.
Severity:      P1
Category:      reliability
Evidence level: VERIFIED-READ (disruption sources, restart from zero). SUSPECTED (how often it happens; no cluster exists, feed-02).
Evidence:      Job length: dev measured "at 1 CPU a 24-minute upload took ~10 hours" (Architecture/docker/docker-compose.yml:1097-1100). Prod worker requests 1 CPU, limit 3 (deploy/services/media-service/values-prod.yaml:160-165). Budget for any video over ~11.5 min is 48 h (internal/processing/limits.go:150-158).
               Disruption: Karpenter NodePool consolidationPolicy WhenEmptyOrUnderutilized, consolidateAfter 30s, no disruption budgets (infra/terraform/modules/karpenter/main.tf:184-187); no karpenter.sh/do-not-disrupt on worker pods (worker.podAnnotations {} in charts/atpost-service/values.yaml; none in media values; grep "do-not-disrupt" repo-wide: none). PDB minAvailable 1 over 2 API + 2 worker pods (values-prod.yaml:146-148, reels-103). Strategy Recreate stops every worker on each worker image change (worker-deployment.yaml:55); the image tag is bumped per commit by CI (values-prod.yaml:157-158).
               On SIGTERM the root context is cancelled and ffmpeg is killed (cmd/worker/main.go:161-168; reels-56), so terminationGracePeriodSeconds 300 (worker-deployment.yaml:83) buys nothing.
               Restart from zero: runTranscode recreates the scratch dir, re-downloads the original and re-encodes every rung (cmd/worker/main.go:618-624, 740-783); MP4 variants are only recorded after every rung is uploaded (:800-845), so nothing of a killed run is reused.
How to reproduce: On QA once it exists: upload a 2-hour 1080p video, wait 30 min, `kubectl cordon` and drain the worker's node (or let consolidation do it); the worker log shows "Context cancelled before commit (long media.events.long)" and the next pod logs "Processing video transcode for media <id>" from the start.
Impact:        A long video needs one unbroken window of many hours (estimated over a day for a 4-hour 1080p source at 3 CPUs, from the dev figure). Each media deploy, consolidation or node rotation inside that window throws all of it away. On an active cluster the longest uploads may never finish; the creator sees "processing" for days. Reels on the same pod lose their in-flight work too.
Fix:           Smallest: add `karpenter.sh/do-not-disrupt: "true"` to worker.podAnnotations in media-service values-prod.yaml and values-qa.yaml (stops voluntary Karpenter disruption of a running worker), and add a step to docs/runbooks/service-rollback.md and the deploy procedure: before rolling media-worker, list assets with a fresh transcode_heartbeat_at and duration over 600 s and wait or accept the restart. Next: the reels-56 drain (stop fetching on SIGTERM), and skip MP4 rungs already uploaded for the same media_generation on a re-run (M).
Acceptance:    Rendered worker pod template carries the annotation (render_test assertion); on QA a Karpenter consolidation leaves a busy worker node alone; the rollback runbook has the in-flight check.
Effort:        S (annotation + runbook); M (rung reuse)
```

```
ID:            LV-E-2
Title:         A long job that crashes, is OOM-killed or is evicted is redelivered and re-run without limit: no attempt counter sees a worker death, so a video too big for the pod loops for ever, blocking its long partition and killing the reels running beside it each time.
Severity:      P1
Category:      reliability
Evidence level: VERIFIED-READ (counting gap). SUSPECTED (that real inputs trigger it; see LV-E-3).
Evidence:      The in-process cap counts only attempts that return an error (retryUntilDurable, cmd/worker/transcode_guards.go:122-167); a process death returns nothing and the offset stays uncommitted (transcode_classes.go:229-256).
               The only other counter is transcode_attempts, incremented by the stall sweeper when it re-queues (store/postgres/media_outbox.go:351) and only after the heartbeat has been silent for 10 minutes (store/postgres/transcode_lease.go:69-76, :128-146). The sweeper's first sweep waits one interval of 5 minutes (cmd/worker/transcode_lease.go:38-42).
               After an eviction the Deployment replaces the pod at once; the redelivered message re-stamps the heartbeat the moment it starts (cmd/worker/transcode_lease.go:77-86, cmd/worker/main.go:364-366). So the heartbeat is never 10 minutes stale and attempts never move when the same pod (or a free one) picks the partition back up.
               The pre-download space check measures the node filesystem, which cannot see the emptyDir sizeLimit (deploy/services/media-service/values-qa.yaml:119-124 says so; check at cmd/worker/transcode_guards.go:226-246), so it does not stop a job that will exceed the 50 Gi limit.
               Each replica runs the short and the long loop in the same container (cmd/worker/main.go:220-252), so an eviction caused by the long job also kills the reel transcode beside it.
How to reproduce: On dev with the compose worker limited to 2 GB (docker-compose.yml:1101-1103): upload a long 4K video whose encode needs more memory; watch `docker events` for repeated OOM restarts and `SELECT transcode_attempts FROM media_assets WHERE id=...` staying 0. Or unit-test: a store fake plus a handler that panics, run twice, assert no counter moved.
Impact:        One oversized or pathological long upload occupies a worker indefinitely, every cycle burning hours of CPU before the kill; its partition of media.events.long never advances, so every long video behind it waits for ever; reels on that pod are restarted on every cycle. Nothing reaches a person (reels-5).
Fix:           Count starts, not failures: in processTranscodeLocked, before run(), increment a per-asset start counter for the current media_generation (new column transcode_starts, or reuse the inbox with a start row); if it is already at 3 when a delivery begins, record the terminal failure "transcode lost its worker 3 times (crash, OOM or eviction)" with CompleteTranscode(failed, manual_review) and commit. Reset on a new generation.
Acceptance:    Unit test: three deliveries that never return leave the fourth recorded as failed with that reason and committed; a normal job still increments once.
Effort:        S
```

```
ID:            LV-E-3
Title:         The worker pod was never sized for the inputs prod admits (16 GiB, 4 hours, 4K, 240 fps): the ladder probably needs more scratch than the 50 Gi limit, ffmpeg threads follow the node's CPU count inside a 4 Gi memory limit, and the 4K rung of a long video can exceed the 24 h per-call cap.
Severity:      P1
Category:      deployment
Evidence level: SUSPECTED (all sizes and times are computed from code and one dev measurement, none measured on the prod shape).
Evidence:      Admitted inputs: 16 GiB original (MEDIA_WORKER_MAX_ORIGINAL_BYTES unset in prod, internal/processing/original_cap.go:18-24), 4 h (limits.go:52-57), 4096 px, 240 fps (limits.go:58-66).
               Scratch: every MP4 rung (thumb, 360, 480, 720, 1080, 4K) is written into one temp dir that is removed only when the job returns (cmd/worker/main.go:618-624; processing/video.go:439-512), and the HLS 360/720/1080 rungs are stream copies of those MP4s written to a second dir while the first still exists (cmd/worker/main.go:902-917). Encodes are CRF 23 "veryfast" with no maxrate or bufsize (processing/video.go:176, 205-218), so a rung's size is not bounded by the source's. Rough estimate for a 4 h 4K source: rungs about 29 Mbit/s together plus about 8 Mbit/s of HLS copies, about 62 GiB, plus the 16 GiB original, about 78 GiB, against scratchSizeLimit 50Gi (values-prod.yaml:166) shared with the short loop and the fingerprint runner (cmd/worker/main.go:198, os.TempDir()). The 3x guard asks for 48 GiB of node free space (transcode_guards.go:61-63, 240-245) and passes on a 100 Gi Karpenter node.
               Memory and threads: mp4TranscodeArgs passes no -threads (processing/video.go:205-218), so libx264 sizes its thread pool from the host's CPUs (the CFS limit of 3 CPUs does not change the count); Karpenter may place the pod on any m/c/r 6th-gen+ arm64 size (modules/karpenter/main.tf:164-177). Memory limit 4Gi for the long encode, a reel encode and a fingerprint ffmpeg together (values-prod.yaml:160-165).
               Time: dev "at 1 CPU a 24-minute upload took ~10 hours" (docker-compose.yml:1097-1100), about 25x real time for the ladder. At the 1-CPU request a 4 h source would take about 100 h, over the 48 h budget, which is a permanent failure (cmd/worker/main.go:597-604). Each ffmpeg call is capped at 24 h (limits.go:127-142); a 4K rung of a 4 h video at 3 CPUs plausibly exceeds it, and a rung that fails is dropped silently (LV-E-4).
               QA cannot show any of this: QA caps the original at 6 GiB on 20 Gi scratch (values-qa.yaml:119-126, 184).
What would confirm it: one measured encode on a QA-shaped pod (1 CPU request, 3 CPU limit, 4 Gi, 50 Gi scratch) of a 4 h 1080p and a 1 h 4K test file: peak scratch (du), peak RSS, wall-clock per rung.
How to reproduce: As above.
Impact:        The largest admitted uploads are the ones most likely to be evicted (LV-E-2 loop), OOM-killed, silently lose their 4K or 1080p rung, or fail after two days of CPU. The founder's "long videos unlimited" promise (video-length rules) is bounded in practice by numbers nobody has measured.
Fix:           Smallest: after the measurement, set MEDIA_WORKER_MAX_ORIGINAL_BYTES and MEDIA_MAX_VIDEO_DURATION_SEC explicitly in values-prod.yaml to what fits (this also clears the reels-62 guard), add "-threads", "<worker CPU limit>" to mp4TranscodeArgs and the HLS encode, and raise scratchSizeLimit so that (sum of rungs + HLS + original) for the cap fits with the short loop's 3x beside it. Delete each MP4 rung's local file once uploaded and not needed by HLS (720p and 360p/1080p are needed; 480p and 4K are not) to cut the peak.
Acceptance:    A dated measurement in docs/production-readiness/long-video/ with the chosen caps; a render guard asserting scratchSizeLimit >= the computed peak for MEDIA_WORKER_MAX_ORIGINAL_BYTES in every values file; one QA run of a file at the cap completing with every rung.
Effort:        M
```

```
ID:            LV-E-4
Title:         A failed MP4 rung (timeout, full disk, killed ffmpeg) is dropped without a log line; its transcoding_jobs row stays 'processing' for ever and the video goes ready without its 1080p or 4K rendition.
Severity:      P2
Category:      observability
Evidence level: VERIFIED-READ
Evidence:      processing/video.go:457-510: each rung is `if err := TranscodeToMP4(...); err == nil { outputs = append(...) }`; the error (which carries ffmpeg's last line, video.go:229-233) is discarded. TranscodeVideo returns nil error regardless (:512).
               cmd/worker/main.go:747-766 creates one transcoding_jobs row per expected rung and marks it 'processing'; only rungs present in outputs are marked 'completed' (:832-838); the failure path that marks rows 'failed' (:777-782) runs only when TranscodeVideo itself errors, which it no longer does for a rung.
               The job ends ready as long as one variant exists (:841-843); the HLS ladder then encodes the missing heights from the original (processing/video.go:36-45), so the HLS 1080p rung may exist while the 1080p MP4 does not; the 4K MP4 has no HLS twin and is simply missing.
How to reproduce: Unit test with a fake runFFmpeg that fails for maxHeight 2160: TranscodeVideo returns nil and no 4k output; no log is written. Or on dev, fill /tmp during the 4K rung.
Impact:        The creator's 4K upload plays at 1080p at most with no explanation; GET /v1/media/:id/status shows a rung "processing" for ever; nobody can tell a dropped rung from a slow one. Combined with LV-E-3 this is the most likely outcome for the largest uploads.
Fix:           In TranscodeVideo log each rung error (media id, rung, last ffmpeg line) and return the per-rung errors; in runTranscode mark the matching jobEntries row 'failed' with the message, and count media_transcode_rung_failures_total{rung}. Keep "ready with fewer rungs" as the behaviour.
Acceptance:    Unit test: a failing 2160p rung leaves the 4k transcoding_jobs row 'failed' with the ffmpeg line, the asset ready, and the counter at 1.
Effort:        S
```

```
ID:            LV-E-5
Title:         "New upload" notifications to channel subscribers are lost for good after about 30 seconds of post-service or graph-service trouble: failed fan-out jobs are retried every 5 s with no backoff, failed after 5 attempts, and nothing can replay them.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      notification-service/internal/service/subscriber_fanout.go:35-41 (fanoutMaxAttempts 5, drain every 5 s, :197-204); drainOnce (:233-251) fails a job whose attempts exceed 5 and releases any other error straight back to 'pending'.
               store/postgres/fanout_jobs.go:117-131: a claim takes any 'pending' row and increments attempts; ReleaseFanoutJob (:177-182) sets 'pending' with no not-before time; FailFanoutJob (:186-191) is terminal. CleanupFanoutRecords deletes only completed jobs (:224-228); no route or tool re-queues a 'failed' fan-out job (grep "'failed'" in notification-service: only these lines and the separate partner queue's requeue at partner_queue_ops.go:53).
               A subscriber page or eligibility error fails the whole pass: processJob returns on SubscriberIDs failure (:305-308); post state is read from post-service (fanout_eligibility.go:169-209) and relationships from graph-service (:150-165), and the comment at :218-223 confirms an eligibility error burns one of the five attempts.
How to reproduce: Unit test with a subscriber source that errors 6 times then succeeds: the job ends 'failed' and is never retried. On dev: stop post-service for 40 s right after publishing a long video with subscribers; no push arrives after it is back.
Impact:        Any post-service rollout (feed-42 shutdown ordering), failover or 30-second graph hiccup silently drops the subscriber push for every long video published in that window, which is the main way subscribers learn of a new upload. No metric or alert (LV-E-7).
Fix:           Give ReleaseFanoutJob an available_at = NOW() + backoff (30 s doubling to 30 min) and have ClaimFanoutJobs skip rows not yet due; raise fanoutMaxAttempts to about 10 (about 6 h of cover); add an admin re-queue for 'failed' jobs modelled on partner_queue_ops.go:53.
Acceptance:    Unit or integration test: six consecutive source failures then success delivers the notification; attempts are spaced by the backoff.
Effort:        S
```

```
ID:            LV-E-6
Title:         While any long video is transcoding anywhere, the stall sweeper stops recovering never-started requests and the Copyright Match fingerprint loop stops admitting work; with multi-hour long jobs that is most of the time.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ (logic). SUSPECTED (how much of the time long jobs keep the pipeline "busy" in prod).
Evidence:      Stall sweeper: a NULL-heartbeat asset is re-queued only when the pipeline is idle (store/postgres/transcode_lease.go:142-151); "busy" is any fresh heartbeat on any asset (anyFreshTranscodeHeartbeat, :294-305) or the sweeping worker itself busy; the busy tracker is shared by the short and long loops (cmd/worker/main.go:181-188, 247-249). A long job stamps a fresh heartbeat every 30 s for its whole duration (cmd/worker/transcode_lease.go:33-36).
               Fingerprints (Copyright Match): admission requires the worker idle for IdleGrace 60 s and the platform's oldest queued wait under 60 s (cmd/worker/fingerprint_runner.go:147-148, 300-319), and a running fingerprint is cancelled as soon as any transcode starts on the pod (:372-385). The tracker is busy throughout a long job.
How to reproduce: Unit test of ClassifyStalledTranscode: a NULL-heartbeat asset queued 2 h with pipelineBusy=true is skipped. On dev: start a long transcode, insert a 'processing' asset with no request in Kafka; it is not re-queued until the long job ends.
Impact:        A reel or long video whose request was lost waits until no long video is transcoding anywhere (hours or days) instead of 30 minutes. Fingerprints of every upload are deferred for as long as long jobs keep the two workers busy, so Copyright Match evidence (shadow today; auto-block is a founder decision of 24 Oct) arrives late or not at all.
Fix:           Smallest for the sweeper: compute "busy" per class (only heartbeats of assets in the candidate's class, using duration_ms > 600 000 for long), so a running long job no longer masks lost short work and vice versa. For fingerprints: count only the short loop in the admission busy check, so fingerprints of finished uploads run beside a long encode (CPU limit permitting) instead of never.
Acceptance:    Unit tests: a lost short request is re-queued while a long heartbeat is fresh; the fingerprint runner admits work while only the long loop is busy.
Effort:        S
```

```
ID:            LV-E-7
Title:         Nothing would tell a person that long video is broken: the transcode metrics have no class label and a duration histogram that ends at 10 minutes, and the long-video endpoints and background loops (progress writes, schedule publisher, subscriber fan-out) have no signal beyond log lines and a service-wide error ratio.
Severity:      P2
Category:      observability
Evidence level: VERIFIED-READ
Evidence:      cmd/worker/main.go:40-48: media_transcode_total{result} and media_transcode_duration_seconds with buckets 1..600 s; every long job falls in +Inf. readiness_metrics.go: media_transcode_oldest_pending_age_seconds and _queue_oldest_waiting_seconds are platform-wide MINs with no class (store/postgres/transcode_inbox.go:270-272, media_generation.go:179-185), so one healthy 20-hour long job pins the "oldest pending" gauge at 20 h and any threshold either pages all day or hides stuck reels. No class label on media_upload_ready_latency_seconds (buckets end at 4 h).
               Endpoints: the only HTTP alerts are service-wide (charts/atpost-service/templates/prometheusrule.yaml:84-95: sum of 5xx over all paths > 5 %, overall p99 > 1 s); POST /v1/videos/:id/progress failing completely is invisible while it is under 5 % of post-service traffic. No values file overrides defaultAlerts for post- or media-service.
               Background loops: postschedule.Tick logs only (post-service/internal/postschedule/worker.go: "post schedule: publish failed"); fan-out logs only (subscriber_fanout.go:239-249); no counter in either (grep prometheus in both packages: none).
               And none of the worker metrics is scraped (reels-5, re-checked).
How to reproduce: Read the files; or render media-service prod and search the PrometheusRule for a transcode or schedule rule: none.
Impact:        Every long-video failure mode in this track (LV-E-1 to LV-E-6, reels-3's forward_failed) is silent; the first signal is a creator's complaint.
Fix:           Add a class label ("short"/"long") to media_transcode_total, _duration_seconds (buckets up to 48 h: 600, 1800, 3600, 7200, 14400, 43200, 86400, 172800), the ready-latency histogram and both age gauges (two queries split at 600 s). Add post_schedule_publish_total{result} and notification_fanout_jobs_total{result} counters. Once reels-5 lands, rules in media and post/notification values-prod: long oldest-waiting > 6 h, long failures > 0 in 1 h, fan-out failed > 0, schedule failures > 0 for 15 min, and a per-path 5xx rule for /v1/videos/:videoId/progress.
Acceptance:    render_test asserts the new rules exist in values-prod; a unit test reads the class label from a long and a short job.
Effort:        S
```

```
ID:            LV-E-8
Title:         A rebalanced duplicate of a running long job parks the other worker's long loop for the whole job behind a blocking advisory lock, and that lock lives on one pooled connection held for up to 48 hours.
Severity:      P3
Category:      reliability
Evidence level: SUSPECTED
Evidence:      store/postgres/media_outbox.go:174-195: WithTranscodeEventLock takes a dedicated pool connection and calls pg_advisory_lock (blocking, not pg_try_advisory_lock) for the job's duration; the comment says it serialises "duplicate/rebalanced deliveries". cmd/worker/main.go:291-301 wraps the whole transcode in it.
               kafka-go group readers can move a partition on any rebalance (a pod restart, eviction, scale change); the new owner fetches from the last committed offset, i.e. the message the old owner is still transcoding (transcode_classes.go:229-256). Not verified: the assignment strategy in use and whether kafka-go keeps the old owner's assignment.
               If that one connection drops during the job (Aurora failover, network reset), PostgreSQL releases the lock silently while the first job keeps running, and a duplicate may start; the inbox and generation checks still stop a double completion (store/postgres/transcode_lease.go:223-234, reels finding "checked and found sound" 2).
What would confirm it: a two-worker dev run with `--profile split-transcode` (docker-compose.yml:1108-1117) scaled to 2, restarting one worker during a long job; check whether the other blocks in pg_advisory_lock (pg_locks).
How to reproduce: As above.
Impact:        With two long loops in prod, one stuck behind the other halves long-video throughput for hours; a dropped lock connection costs a duplicate multi-hour encode. No correctness loss.
Fix:           Use pg_try_advisory_lock; if the event is locked by another worker, leave the message uncommitted and back off (or commit, since the holder will record the outcome and the inbox absorbs it), rather than waiting.
Acceptance:    Unit test with a store whose lock is held: the second delivery returns promptly without running the transcode.
Effort:        S
```

```
ID:            LV-E-9
Title:         There is no switch to stop long-video intake in an incident: no flag on server, web or Android, and the levers that exist act after the creator has uploaded the file.
Severity:      P3
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      grep for Getenv("TUBE*|LONG*|POSTTUBE*|MTUBE*|VIDEO*") across Go services: only VIDEO_SERIES_POSTGRES_DSN. Android ProductGate leaves LauncherApp.TUBE ungated (mobile/android/app/.../features/ProductGate.kt:26-28); web has no PostTube product flag (map section 7, src/lib/productFeatures.ts:31-44).
               Partial levers: MEDIA_TRANSCODE_CLASSES=short stops the long loop (uploads keep arriving and queue on the long topic, cmd/worker/transcode_classes.go:91-117); MEDIA_WORKER_MAX_ORIGINAL_BYTES refuses at init by size, not length (values-qa.yaml:124-126); MEDIA_MAX_VIDEO_DURATION_SEC refuses only after the upload and download (limits.go:52-57); trust-safety LongVideoPausedUntil is per user (post-service/internal/service/copyright_title_review.go:294-306).
How to reproduce: Read the files.
Impact:        During a long-transcode incident (LV-E-2, reels-3) the only way to stop new multi-GiB uploads is a code change or a size cap that also blocks big reels.
Fix:           Add LONG_VIDEO_UPLOADS_ENABLED (default true) read by post-service create and draft publish for content_type long_video (answer 503 UPLOADS_PAUSED with the existing UploadsPausedError shape) and by media-service resumable init for files the client marks long; document it in docs/runbooks/service-alerts.md.
Acceptance:    Unit test: with the flag false, a long_video create answers 503 and a flick create succeeds.
Effort:        S
```

```
ID:            LV-E-10
Title:         Staging and Azure behave differently from prod on long video (views counted on play_end, scanner and fingerprinting off, 6 GiB cap), and QA cannot take a long original over 6 GiB, so no pre-prod environment reproduces prod's long-video behaviour.
Severity:      P3
Category:      deployment
Evidence level: VERIFIED-RUN (key diff) / VERIFIED-READ (defaults)
Evidence:      Key diff prod vs QA (all seven services): identical except media-service MEDIA_WORKER_MAX_ORIGINAL_BYTES in QA only (6 GiB, values-qa.yaml:124-126).
               Prod keys missing from staging: analytics ANALYTICS_VIEW_SOURCE (default play_end, analytics-service/internal/viewsource/viewsource.go:42-51; prod sessions values-prod.yaml:56); media COPYRIGHT_FINGERPRINT_ENABLED and COPYRIGHT_MATCHING_ENABLED (off), MEDIA_CDN_BASE_URL, Kafka SASL/TLS; post REVIEW_ALL_VIDEOS (same default false), TRUST_SAFETY_SERVICE_URL, Kafka SASL/TLS; notification SUBSCRIBERS_SERVICE_URL and POST_SERVICE_URL. Map section 7 adds Azure prod: scanner absent, 3 workers.
               The devbox matches prod on view source (Architecture/docker/docker-compose.yml:1680 defaults to sessions).
How to reproduce: The diff in "What I ran".
Impact:        A view-count, copyright or large-upload behaviour checked on staging or Azure does not predict prod; QA (the prod-equal environment) cannot exercise uploads between 6 and 16 GiB, the range where LV-E-2 and LV-E-3 live. Inherits feed-77 for the stale staging tree.
Fix:           Either delete the staging and Azure values trees or bring them to prod's keys (feed-77); set the QA cap and the prod cap to the same measured value (LV-E-3), which also clears reels-62.
Acceptance:    render_test's drift guard extended to staging (or the files removed); QA and prod carry the same MEDIA_WORKER_MAX_ORIGINAL_BYTES.
Effort:        S
```

```
ID:            LV-E-11
Title:         The chart's static QA guard now fails twice: besides the media cap (reels-62), post-service QA carries a prod host in ACTION_LINK_OWN_DOMAINS, used by the Action button on long videos.
Severity:      P3
Category:      deployment
Evidence level: VERIFIED-RUN
Evidence:      `QA_GUARDS_ONLY=1 bash charts/atpost-service/render_test.sh` → "FAIL QA: prod host in a QA file: deploy/services/post-service/values-qa.yaml:121: ACTION_LINK_OWN_DOMAINS: 'cleestudio.com'" and the media drift failure; "STATIC GUARDS FAILED", exit 1. Every other static guard printed ok.
How to reproduce: Run the command.
Impact:        A red guard hides the next real drift. The value itself is probably intended (own domains are the same in QA), so the defect is in the guard's allowlist or the value, not in behaviour.
Fix:           Decide: if cleestudio.com is the own-domain list in every environment, add ACTION_LINK_OWN_DOMAINS to the guard's allowed prod-host keys; otherwise set the QA host.
Acceptance:    The same command ends without STATIC GUARDS FAILED once reels-62 is also fixed.
Effort:        S
```

```
ID:            LV-E-12
Title:         Long-video failure messages are thin: web says "may still be processing" for every playback failure and offers no retry; Android's watch page shows "Still processing" for a video whose transcode failed.
Severity:      P3
Category:      reliability
Evidence level: VERIFIED-READ (web, Android hub). SUSPECTED (Android watch for a failed transcode).
Evidence:      Web: TubePlayer falls back from HLS to the progressive file on any fatal network error (src/features/posttube/components/TubePlayer.tsx:372-380, useFile :319-332) and then shows "This video can't be played right now. It may still be processing. Try again in a moment." with no button (:825-830); a 503 from the media gate (post-service down, reels-61) reads as "still processing".
               Android: playback failures show "This video stopped playing." with Retry (feature/tube/.../ui/watch/WatchControls.kt:220,234-264) — sound. When the post has no playable URL the watch page shows "Still processing / This video isn't ready to play yet." (ui/watch/WatchScreen.kt:609-614) whatever the reason; the Creator Hub does show "Processing failed" (ui/hub/CreatorHubScreen.kt:507,751). Not traced: what the author's own watch page receives for processing_status 'failed'.
How to reproduce: Web: stop post-service on dev and open /posttube/watch/<id>. Android: open the watch page of one's own failed upload.
Impact:        During an outage viewers are told to wait for processing that already finished; a creator whose upload failed is told it is still processing on the watch page and must find the hub to learn otherwise.
Fix:           Web: a Retry button in tube-player__failed and a distinct line when the media gate answered 503. Android: map processing_status 'failed' or 'rejected' to a "Processing failed" empty state with the hub as the action.
Acceptance:    Web unit test for the failed overlay with a retry; Android ViewModel test for a failed status.
Effort:        S
```

## Dependency down, per long-video path

| Path | Dependency down | What the code does | User sees | Evidence |
|---|---|---|---|---|
| Upload (resumable, 16 GiB) | media-service rollout | chunk requests cut at 10 s drain (reels-60); clients resume within the 24 h session | retried chunks; progress continues | `media-service/internal/service/resumable.go:38`; Android `ResumableUploader.kt` 6 attempts, web 5 (map 9) |
| Upload | Redis | upload budget fails open | upload works | reels dependency table |
| Upload session older than 24 h | — | session expired, multipart aborted by the reclaim worker or the 7-day rule | upload must restart | `resumable.go:250`; `modules/media/main.tf:167-176` |
| Transcode | `media.events.long` missing | transcoded on the reel loop | long delays for reels and long videos (reels-3) | `transcode_classes.go:214-221` |
| Transcode | worker disrupted | restart from zero (LV-E-1); loop on OOM/eviction (LV-E-2) | "processing" for hours or for ever | as cited |
| Transcode | S3 or Postgres blip | transient, whole job retried up to 8 times serially | delay of a full encode per retry | `transcode_guards.go:122-167` |
| Transcode | Rekognition | full re-encode per retry, then failed (reels-34) | long video failed after days of retries | `cmd/worker/main.go:985-1037` |
| Publish (create or schedule) | trust-safety | standing check fails closed: author gets 503 STANDING_UNAVAILABLE; the schedule worker retries with backoff and parks after 24 h | "publish failed"/scheduled post parked | `post-service/internal/service/schedule.go:401-419`; `copyright_title_review.go:294-306` |
| Scheduled publish | media still processing at publish_at | published anyway, author-only until ready | subscribers see it once ready (fan-out timing: Track A) | `schedule.go:38-39` |
| Subscriber push | post-service or graph-service | fan-out job failed after ~30 s, no replay | no push (LV-E-5) | `subscriber_fanout.go:233-251` |
| Watch progress | Redis | DB is the record; read falls back to DB; mirror write best effort (500 ms) | none; but readiness drops the pod (feed-43) | `watch_progress.go:42-108` |
| Watch, channel, playlists | post-service | 503 for metadata and for the media gate | web "can't be played … may still be processing" (LV-E-12); Android error state with Back | `TubePlayer.tsx:825-830`; `WatchScreen.kt:688` |
| Playback segments | CloudFront | no fallback | playback error; web falls back to the progressive file (also via CloudFront) | `TubePlayer.tsx:372-380` |
| Playback, a 4-hour video | segment signature expiry | segments signed for 2x duration + 15 min, capped 12 h; Android re-prepares on 403 (at most twice); web hls.js fatal → file fallback | a resumed or paused long session may drop to the MP4 | `media-service/internal/delivery/sign_cache.go:63-66`; `core/media/.../HlsRefresh.kt:31-46` |
| Long-video feeds, related | feed-service deps | as the feed audit (503 FEED_UNAVAILABLE on hydration) | error with retry | feed track E-10 |
| Search (videos, channels, collections) | OpenSearch | search page error; index updates retried then DLQ; tube index rebuilt only by the manual reindex | search error; stale channel results until a reindex | `search-service/internal/reindex/tube.go:63-195` (UNVERIFIED beyond the map) |
| RSS | post-service | web route answers 502 `no-store`; a 200 is cached `s-maxage=600, stale-while-revalidate=3600` | feed reader retries; a CDN in front would serve stale (none confirmed) | `src/app/posttube/channel/[handle]/feed.xml/route.ts:17-21,57-70` |
| Views and watch time | analytics-service | Android batches in WorkManager; web best effort | counts lag | reels dependency table |
| Copyright Match | worker busy with long jobs | fingerprints deferred (LV-E-6); title hold is a post-service decision at publish and does not need the worker | none now (shadow) | `fingerprint_runner.go:300-319` |

## Background work for long video: what runs it, what if it is down

| Work | Runs in | If it is down | Evidence |
|---|---|---|---|
| Long transcode loop | every media-worker pod (2 in prod), group `media-transcode-worker-long` | requests wait in Kafka; nothing lost; nothing alerts | `cmd/worker/main.go:220-252` |
| Stall sweeper | every media-worker pod, 5 min | runs only where a worker runs (by design); blind while long jobs run (LV-E-6) | `cmd/worker/main.go:183-188` |
| Fingerprint runner (Copyright Match) | every media-worker pod, idle-only | deferred; jobs stay queued | `fingerprint_runner.go:300-385` |
| Scheduled publish | every post-service pod, 30 s, compare-and-set flip (safe in replicas) | posts publish late; catch-up on the first tick after start | `post-service/internal/postschedule/worker.go` (package doc and Start) |
| Scheduled draft publish (web long uploads publish through drafts) | every post-service pod, 60 s | late | `post-service/cmd/server/main.go:715-739` |
| Subscriber fan-out | every notification-service pod, 5 s, `FOR UPDATE SKIP LOCKED` | jobs wait as `pending`; but errors during a pass fail them (LV-E-5) | `subscriber_fanout.go:186-251` |
| RSS | built on request by post-service; web adds HTTP caching | 502 to readers | above |
| Live replay import | live-service-v2 sweeper, gives up after 6 h; post-service consumer | replay never becomes a video | map section 8 |
| Search tube reindex | manual internal route only | channel and playlist index drift is never repaired automatically | `search-service/internal/http/handler_tube.go:224` |
| CronJobs | none in the chart or `deploy/jobs/` | — | map section 11 |

## Migrations and rollback

- `migration-compat.sh --files` over the 15 long-video post-service migrations (010, 012, 016, 041, 043, 046, 047, 051, 054, 061, 067, 070, 071, 073, 081) flags 27 `ADD-CONSTRAINT` statements (unique indexes, CHECKs, the 081 FK to `media_assets` and the `(playlist_id, post_id)` unique index). All are in migrations already applied on dev, and production applies every file to an empty database, so none is a rollback risk at first launch (same reasoning as the reels track). After launch, rolling back across a migration like 081 would make the N-1 image's duplicate-add into a constraint error; the rule "every migration works with N-1" (`docs/runbooks/service-rollback.md`) must be checked per new migration, and the script is still not in CI (feed-11).
- media-service migrations run at boot in both the server and the worker under an advisory lock (reels track section 5); a worker rollout (Recreate) can migrate before the server pods roll.
- Rollback of the media worker during a long job restarts that job (LV-E-1); the runbook does not say so.

## Checked and found sound

1. Nothing of a long transcode is lost or published twice across a kill: offset committed only after the durable effect, inbox, generation fence, superseded-delivery check (`transcode_classes.go:229-256`; `cmd/worker/main.go:325-335`). VERIFIED-RUN: worker unit tests ok.
2. A runaway job ends: per-call timeout, job budget, heartbeat stops past the budget so the sweeper reclaims it (`limits.go:123-158`; `cmd/worker/transcode_lease.go:77-113`).
3. Abandoned multipart uploads are cleaned at both layers (app 24 h, bucket 7 d).
4. Scheduled publish is safe in every replica and catches up after a restart (`postschedule/worker.go`, VERIFIED-RUN tests ok).
5. Fan-out claims use `FOR UPDATE SKIP LOCKED` and advance a cursor per page, so a retry does not re-notify those already paged (`fanout_jobs.go:117-131`; per-recipient dedup `:197-215`). VERIFIED-RUN: fan-out tests ok.
6. Watch progress survives a Redis outage (database first, mirror best effort).
7. Per-environment config is values-only and QA equals prod except one key (VERIFIED-RUN); no debug, stub or bypass flag in the seven services' prod values.
8. Spot interruption is not a risk at launch: prod Karpenter is on-demand only (`envs/prod/variables.tf:277-280`); the interruption queue is wired anyway (`modules/karpenter/main.tf:67-68`).
9. The web RSS route never caches an error (`feed.xml/route.ts:29-33`).

## What I ran

- `QA_GUARDS_ONLY=1 bash charts/atpost-service/render_test.sh` → exit 1, `STATIC GUARDS FAILED`: the media drift (reels-62) and post-service `ACTION_LINK_OWN_DOMAINS` (LV-E-11); probes and default-alert guards ok.
- `bash scripts/verify/migration-compat.sh --self-test` → `self-test passed`.
- `bash scripts/verify/migration-compat.sh --files` over the 15 long-video post-service migrations → 27 flagged, exit 1 (read above).
- `go test -count=1 ./cmd/worker/ -run 'Stall|Classif|Class|Budget|Scratch|Consume|Retry|Heartbeat|Fingerprint'` (media-service) → ok.
- `go test -count=1 ./internal/postschedule/` (post-service) → ok.
- `go test -count=1 ./internal/service/ -run 'Fanout|Subscriber'` (notification-service) → ok.
- A key-and-value diff of the env blocks of `values-prod.yaml` against `values-qa.yaml` and `values-staging.yaml` for media, post, feed, search, notification, user and analytics (grep, comm, diff; hosts and ARNs filtered).
- Read-only `grep`, `sed`, `ls`.

## Not verified

- Anything on a live cluster, broker or AWS account (none exists, feed-02): Karpenter's actual disruption of the worker, the node root volume in the managed group, kafka-go's partition movement on rebalance (LV-E-8), whether `media.events.long` was created by hand anywhere.
- Every size and time in LV-E-3 is computed (bitrates are rough CRF-23 estimates; the 25x real-time figure is one dev measurement at 1 CPU). No encode was run.
- Helm render of the chart (static guards only).
- Android author's watch page for a failed transcode (LV-E-12); Android upload of a 16 GiB file across process death beyond the WorkManager facts in the map.
- Whether the fan-out eligibility read of graph-service errors per recipient or per page (the comment at `fanout_eligibility.go:218-223` was taken at its word).
- Search-service and feed-service behaviour under OpenSearch or Scylla loss for the tube routes (map and feed track only).
- CI state of the cited tests at HEAD (not queried with gh).
- Dev smoke and any deployed environment: forbidden by the rules.

## Notes for other tracks

- **Track D:** two long transcodes at a time platform-wide (one long loop per pod, 2 pods); a 4 h source plausibly takes more than a day each, so the long queue's wait is measured in days under modest load. `GetWatchProgress` reads Redis and then the database on every hit (`watch_progress.go:68-93`), so the cache saves nothing. A 4 h video's segments outlive viewer sessions only up to 12 h of signature.
- **Track A:** the 24 h resumable session TTL from init (`resumable.go:38`) cannot carry a 16 GiB upload on a link under about 1.6 Mbit/s; both clients resume only within a session. Scheduled long videos still processing at `publish_at` are published author-only; check when subscribers are notified.
- **Track C:** migration 081 adds an FK from post-service's `video_series.cover_media_id` to `media_assets(id)` (`081_tube_integrity.sql:55`), a cross-service table reference; confirm both live in one database in prod.
- **Track B:** none new.
- **Track F:** no test covers a rung failure (LV-E-4), a worker death counter (LV-E-2) or fan-out retry spacing (LV-E-5); no end-to-end long-video upload exists (map 10).
