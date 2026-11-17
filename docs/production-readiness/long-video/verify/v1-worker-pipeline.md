# Verify V1: long-video worker pipeline

Date: 2026-11-17. Independent verifier, read-only. For each finding I read only its Title and Evidence fields, then went to the code and tried to disprove it. Nothing in the repo was changed apart from this file. Things I ran: one throwaway Go program in the scratchpad (RSA signing cost), and the existing unit tests `go test ./internal/processing -run 'TestCallTimeoutScalesWithDurationAndIsCapped|TestHLSLadderKeepsFullLadderForLongForm'`, `./internal/delivery -run Cache` and `./cmd/worker -run 'Retry|Stall|Heartbeat'` in media-service. All of them pass.

What I searched for to disprove the findings, and did not find:

- a `karpenter.sh/do-not-disrupt` annotation, a preStop hook or a worker-only PDB;
- a SIGTERM drain that lets ffmpeg finish, or reuse of rungs between runs;
- a start or delivery counter;
- `-threads` on the x264 calls;
- deleting a rung between encodes;
- a playlist body cache or a wildcard (custom-policy) signature;
- a second worker Deployment, a worker HPA or KEDA;
- a per-user quota under any name;
- anything that extends a resumable session.

---

## LV-E-1: an interrupted long transcode restarts from the first rung

- **Verdict:** CONFIRMED, with one nuance on the Karpenter part.
- **Evidence level:** VERIFIED-READ.
- **Checked:**
  - **No protection.** `charts/atpost-service/values.yaml:232` sets `podAnnotations: {}`. media `values-prod.yaml:153-176` adds no annotations. A repo-wide grep (docs excluded) for do-not-disrupt and preStop finds nothing.
  - **Consolidation.** `infra/terraform/modules/karpenter/main.tf:184-187` sets `WhenEmptyOrUnderutilized` / `30s` with no `budgets`.
  - **PDB.** `templates/pdb.yaml:14-16` selects `selectorLabels`, and the worker pods also carry those labels (`worker-deployment.yaml:59-60`). So the media PDB (`minAvailable: 1`, `values-prod.yaml:146-148`) covers the API pods and the worker pods together, and allows a busy worker to be evicted.
  - **Rollouts.** `worker-deployment.yaml:55` uses `type: Recreate`. The worker image tag is set by CI (`values-prod.yaml:158`).
  - **SIGTERM.** On SIGTERM the root context is cancelled (`cmd/worker/main.go:161-168`). Every ffmpeg call runs under `exec.CommandContext(callCtx, ...)` (`internal/processing/tool.go:175-177`), so the 300 s grace period (`values.yaml:247`, `worker-deployment.yaml:83`) does not let ffmpeg finish. The chart comment at `values.yaml:245-246` claims the opposite and is wrong.
  - **Restart from zero.** The worker makes a new temp dir and downloads the original again (`main.go:621-672`). Every rung is re-encoded (`processing/video.go:439-513`). Variants are uploaded and recorded only after every rung is done (`main.go:806-848`). Nothing checks for rungs that already exist.
- **Nuance.** The worker has no nodeSelector (`values.yaml:249`). The untainted EKS managed node groups `general` and `system` (`modules/eks/main.tf:88-133`) can therefore host it, and Karpenter does not consolidate those nodes. Interruption by consolidation depends on where the pod lands. Rollouts and node rotation are deterministic. Prod is on-demand only (`envs/prod/variables.tf:277-280`), so spot interruption is not a factor.
- **Severity: P1.** Every worker image deploy cancels every in-flight long transcode and throws its work away. A multi-hour job may never finish on a cluster that deploys daily.

## LV-E-2: crash, OOM or eviction is counted by nothing, so the job loops for ever

- **Verdict:** CONFIRMED.
- **Evidence level:** VERIFIED-READ.
- **Checked:**
  - **The in-process cap.** `retryUntilDurable` (`cmd/worker/transcode_guards.go:130-167`) counts only attempts that return an error. A dead process returns nothing.
  - **No commit.** `consume` leaves the offset uncommitted (`transcode_classes.go:244-249`).
  - **The only persistent counter.** That is `transcode_attempts`, bumped only by a stall re-queue (`store/postgres/media_outbox.go:351`). A re-queue needs a heartbeat older than 10 min (`store/postgres/transcode_lease.go:69-76, 135-141`).
  - **The heartbeat.** A redelivered job stamps the heartbeat at once (`cmd/worker/transcode_lease.go:78-85`, called at `main.go:365`). The sweeper first waits 5 min (`transcode_lease.go:39-42`).
  - **The job budget.** The 48 h budget also restarts with each delivery, because `jobBudget` is created per call (`main.go:364`). It does not bound the loop either.
  - **Search for a start counter.** A grep for `transcode_starts|delivery_count` finds nothing.
- **Nuance.** If the dead pod's partition is handed to a replica whose long loop is busy, its heartbeat can go stale for more than 10 min. The counter then does move, and gives up after 3 (`transcode_lease.go:73,155-162`). So "never" is too strong. In the normal case it holds: an OOM restart in the same pod, or a replacement pod within minutes.
- **Severity: P1.** One input too big for the pod blocks a `media.events.long` partition indefinitely, burning hours of CPU each cycle. Nothing alerts anyone.

## LV-E-3: the worker is not sized for 16 GiB / 4 h / 4K

- **Verdict:** DOWNGRADE-TO-SUSPECTED. The limits and the code are as stated. The sizes are estimates that nobody has measured.
- **Evidence level:** VERIFIED-READ for the limits and the code paths. SUSPECTED for the size and time figures.
- **Checked:**
  - **Admitted inputs.** The cap is 16 GiB when the env var is unset (`processing/original_cap.go:18,29-33`), and prod's `env` does not set it (`values-prod.yaml:83-141`). Length is 4 h, frame size 4096 px and rate 240 fps (`limits.go:57-66`).
  - **Time limits.** Each ffmpeg call is capped at 24 h and the job at 48 h (`limits.go:129-158`).
  - **Threads.** `mp4TranscodeArgs` passes no `-threads` (`video.go:202-218`). Only the fingerprint extractor pins threads.
  - **Scratch.** All rungs stay in one temp dir until the job returns (`video.go:439-513`, `main.go:621-625`). The HLS rungs go to a second dir while the first still exists (`main.go:916-920`).
  - **The space check.** It uses `syscall.Statfs` (`diskspace_unix.go:10-17`), so it sees the node filesystem, not the emptyDir `sizeLimit` (`worker-deployment.yaml:80-82`). 3x 16 GiB = 48 GiB passes on the 100 Gi Karpenter node volume (`karpenter/main.tf:127`).
- **What would make it false:** a measured peak under 50 Gi and under 4 Gi RSS. There is no such measurement.
- **Merge:** the scratch half duplicates LV-D-5. The threads half overlaps LV-D-2.
- **Severity: P2.** It is plausible but unmeasured, and only the largest admitted inputs trigger it. It becomes P1 through LV-E-2 if measurement confirms an eviction.

## LV-D-1: RSA-signing every segment of a child playlist

- **Verdict:** CONFIRMED.
- **Evidence level:** VERIFIED-RUN for the cost. VERIFIED-READ for the code path.
- **Checked:**
  - **One key per segment.** `service/playback.go:277-288` makes one key per segment line, and `:314-315` signs them. `delivery/authz.go:651-669` loops `signSegment` per key.
  - **One RSA per URL.** `sign_cache.go:235-253` calls `signer.go:163-184`: one `rsa.SignPKCS1v15` per URL, with a canned policy naming that one resource. There is no wildcard and no custom policy.
  - **Segment length.** `video.go:196` sets `hlsSegmentSeconds = 6`, which gives 2,400 segments per rung for 4 h.
  - **The cache.** It holds 20,000 entries and drops the whole map when full of non-stale entries (`sign_cache.go:71,184-196`). One mutex guards it (`:151`). A grep for a playlist or body cache, or singleflight, finds nothing.
  - **Pod size.** The CPU limit is 1 (`values-prod.yaml:25-31`).
- **My run.** 2,400 RSA-2048 signs took 6.15 s with GOMAXPROCS=1 (2.56 ms each, amd64, Go 1.26). This is consistent with the claimed 5.0 s.
- **Severity: P2.** It costs seconds of CPU and start latency, but only on cold child playlists of multi-hour videos (1 h is about 1.5 s). The 2-minute reuse window absorbs repeat viewers. It is a performance and CPU-amplification issue, not broken data or auth.

## LV-D-2: two sequential long-transcode lanes, sharing CPU with reels

- **Verdict:** CONFIRMED, and slightly worse than stated.
- **Evidence level:** VERIFIED-READ.
- **Checked:**
  - **Two lanes.** There is one sequential loop per class (`transcode_classes.go:229-256`, `main.go:220-252`). Prod has `replicaCount: 2`, CPU request 1 / limit 3, 4Gi, `MEDIA_TRANSCODE_CLASSES: "short,long"` (`values-prod.yaml:153-176`). The chart renders one worker Deployment and no worker HPA (`worker-deployment.yaml:23-27`). A grep for `ScaledObject|keda` finds nothing.
  - **Worse than stated.** `media.events.long` is missing from the MSK topic list (`infra/terraform/modules/msk/topics-job/variables.tf:28-59`), and `auto.create.topics.enable=false` (`modules/msk/main.tf:147`). So in prod every forward fails, and long jobs run inside the short loop (`transcode_classes.go:214-220`). Reels then queue behind them, and the long consumer has no topic to read.
- **Severity: P1.** Platform-wide throughput is two multi-hour jobs at a time, with no autoscaling. With the missing topic, long uploads also block reels.

## LV-D-3 and LV-B-1 (duplicates): no per-user storage or transcode quota, and the limit fails open

- **Verdict:** CONFIRMED.
- **Evidence level:** VERIFIED-READ.
- **Checked:**
  - **The only limit.** `media-service/internal/service/ratelimit.go:21-24` returns nil when there is no Redis, and `:49-51` returns `true // fail-open` on a pipeline error. The limits are 10/h and 30/day (`validation.go:37-38`).
  - **Resumable uploads.** Resumable init counts against the same budget (`resumable.go:120-125`).
  - **Search for a quota.** A grep in media-service for `quota|bytes_used|transcode_minutes` and similar finds only a test comment. post-service's `MAX_POSTS_PER_DAY` (`post-service/internal/service/ratelimit.go:29`) does not gate the transcode, which media-service triggers.
- **Merge:** LV-D-3 and LV-B-1 are one finding.
- **Severity: P1.** One account can queue 30 four-hour transcodes a day onto a two-lane fleet, a serious abuse vector. The fail-open is the smaller half.

## LV-C-1: a resumable upload expires 24 h after init

- **Verdict:** CONFIRMED.
- **Evidence level:** VERIFIED-READ.
- **Checked:**
  - **Fixed expiry.** `service/resumable.go:38` sets the TTL to 24 h, and `:158` sets the expiry once at init. Nothing updates `expires_at`: a grep finds only the INSERT and SELECTs in `store/postgres/resumable.go:36,48,82,112`.
  - **Refusal.** `UploadPart` refuses after expiry (`resumable.go:250-252`).
  - **Orphan GC.** It selects `processing_status='pending_upload' AND created_at < cutoff` (`store/postgres/media.go:576-581`) with `maxAge 24h` (`service/orphan_gc.go:43`). It has no session check, and nothing in the resumable path moves the asset off `pending_upload` before complete.
  - **Android.** The client shows the expiry message at `ResumableUploader.kt:377`.
- **Severity: P2.** Only uploads that span more than 24 h fail: a 16 GiB file on a link under about 1.6 Mbit/s, or a session paused overnight. The creator still has the file and gets a clear error. Nothing published is lost.

---

| ID | Verdict | Evidence level | Severity | Note |
|---|---|---|---|---|
| LV-E-1 | CONFIRMED | VERIFIED-READ | P1 | Rollout restarts are certain; Karpenter only if placed on a Karpenter node |
| LV-E-2 | CONFIRMED | VERIFIED-READ | P1 | "Never" is too strong when the partition moves to a busy replica |
| LV-E-3 | DOWNGRADE-TO-SUSPECTED | VERIFIED-READ code / SUSPECTED sizes | P2 | Scratch half = LV-D-5; threads overlap LV-D-2 |
| LV-D-1 | CONFIRMED | VERIFIED-RUN (6.15 s / 2,400) | P2 | Performance, multi-hour only |
| LV-D-2 | CONFIRMED | VERIFIED-READ | P1 | Long topic missing in MSK, auto-create off: long jobs run in the reel loop |
| LV-D-3 = LV-B-1 | CONFIRMED | VERIFIED-READ | P1 | Merge |
| LV-C-1 | CONFIRMED | VERIFIED-READ | P2 | More than 24 h uploads only |
