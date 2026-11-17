# Re-audit regression review: reels and long video fix round

Date: 2026-11-17. Branch `codex/module-01-02-launch-safety`, HEAD `cdb530dd5`.
Range reviewed: `9b4d9870f..HEAD` (39 commits), committed code only (`git diff` / `git show HEAD:`),
plus the web worktree `C:\workspace\wt\pb-echo` `ca96921..HEAD` (4 commits).

Purpose: find defects the fix round itself introduced. This review does not confirm that the fixes work.

Method: read the diffs; traced callers, consumers and deploy values; ran one throwaway program against a copy
of `internal/captions` in a scratch directory outside the repo. No repo files were changed except this document.

## Summary

| Severity | Count | IDs |
|---|---|---|
| P0 | 0 | none |
| P1 | 0 | none (REG-1 becomes P1 if Azure prod is a live target) |
| P2 | 4 | REG-1, REG-2, REG-3, REG-4 |
| P3 | 5 | REG-5, REG-6, REG-7, REG-8, REG-9 |

| Evidence level | IDs |
|---|---|
| VERIFIED-RUN | REG-4, REG-8 |
| VERIFIED-READ | REG-1, REG-2, REG-3, REG-5, REG-6, REG-7 |
| SUSPECTED | REG-9 |

---

## REG-1: media-service refuses to start on the Azure prod values (P2; P1 if Azure prod is live)

- **Evidence level:** VERIFIED-READ
- **Commit:** b9a4fdaea (media: X-User-Id is believed only with proof, and production is RS256 only)
- **Evidence:**
  - `Architecture/services/media-service/internal/http/jwt_config.go:84-92`: in production, `LoadJWTKeySet` refuses to start when `JWT_ISSUER` is empty, `JWT_AUDIENCE` is empty, or `JWT_SECRET` / `JWT_SECRET_PREVIOUS` is set.
  - `isProduction` matches `ENV=prod`.
  - `deploy/services/media-service/values-azure-prod.yaml:41` sets `ENV: prod`.
  - The same file mounts `JWT_SECRET` (line 29) and `JWT_SECRET_PREVIOUS` (line 33).
  - It sets neither `JWT_ISSUER` nor `JWT_AUDIENCE`.
  - `cmd/server/main.go` calls `os.Exit(1)` on that error.
  - Only `values-prod.yaml` and `values-qa.yaml` were updated in this commit. `values-staging.yaml` and `values-azure-staging.yaml` use `ENV: staging`, so they keep HS256 and are unaffected.
- **Reproduce:** Run `LoadJWTKeySet` with `ENV=prod`, `JWT_SECRET=x` and `JWT_PUBLIC_KEY_PEM=<key>`. It returns `ErrJWTConfig`. You can also render the Azure prod values and start the pod.
- **Impact:** The media API crash-loops on the Azure prod target. If that target is live, uploads, playback and every media read stop working.
- **Fix:** In `values-azure-prod.yaml`, remove the `JWT_SECRET` and `JWT_SECRET_PREVIOUS` secret mounts and add `JWT_ISSUER` and `JWT_AUDIENCE`, matching the gateway. If Azure is retired, delete the file.
- **Acceptance:** A template render plus `LoadJWTKeySet` over the rendered env for every `values-*.yaml` returns nil. A CI check covering that would stop this recurring.

## REG-2: the worker's SIGTERM drain outlasts the pod grace period outside prod, so healthy transcodes count as lost and get quarantined (P2)

- **Evidence level:** VERIFIED-READ
- **Commits:** 8f72fcd9e (drain, lost-worker counting), caa92a660 (classes)
- **Evidence:**
  - `cmd/worker/drain.go:44-50`: the drain window is the class grace minus 60 s. It is 3540 s whenever the worker consumes the long class.
  - `transcode_classes.go:89-92`: an unset `MEDIA_TRANSCODE_CLASSES` means both classes, so the window is 3540 s.
  - The chart default grace is `terminationGracePeriodSeconds: 300` (`charts/atpost-service/values.yaml:249`).
  - `values-staging.yaml` (worker block from line 105) sets neither the grace, the variants nor `MEDIA_TRANSCODE_CLASSES`. `values-azure-*.yaml` do not either.
  - `Architecture/docker/docker-compose.yml` media-worker (line 1062) uses `short,long` and has no `stop_grace_period`, so Docker's 10 s default applies.
  - In all of these, the kubelet or Docker sends SIGKILL long before the work context is cancelled. `finishDelivery` (`delivery.go`) therefore never runs, and the run is counted as lost (`lostRuns = starts-1-finishes`).
  - Before this round, SIGTERM cancelled the job at once, so the run returned.
  - After `MEDIA_TRANSCODE_MAX_LOST_WORKERS` (default 3) such restarts during one long transcode, the next start calls `lostWorkerFailure`. That is permanent: the asset fails, then the post-service consumer or sweep sets the post to `rejected`.
  - Only prod and QA set per-class grace (600 / 3600) and are consistent.
- **Reproduce:**
  1. On staging (or the devbox compose), start a transcode longer than 5 minutes (10 s on compose).
  2. Roll the worker 3 times while it runs.
  3. The 4th start logs "GIVEN UP" and the video and post are failed and rejected.
- **Impact:** Ordinary deploys on staging and the shared devbox permanently kill in-flight long uploads. This turns a transient condition into a permanent rejection, which is the failure mode this round set out to remove.
- **Fix (smallest):** Read the grace from the pod rather than the class. Either set `MEDIA_WORKER_DRAIN_SECONDS` in the chart from `terminationGracePeriodSeconds - 60` for every worker Deployment, or clamp `drainWindow` to a `MEDIA_WORKER_GRACE_SECONDS` env the chart always sets. Also add `stop_grace_period` to the compose `media-worker`.
- **Acceptance:** For every values file and the compose file, drain window < grace. An interrupted run under a rollout is recorded finished, so `lost` stays 0.

## REG-3: a video whose length the confirm probe cannot read is charged as a long video, so a third WebM/MKV clip in flight is refused after its bytes are uploaded (P2)

- **Evidence level:** VERIFIED-READ
- **Commit:** 058a97e30 (media: video uploads get a daily budget, and the upload limits fail closed)
- **Evidence:**
  - `internal/service/upload_quota.go:231`: an unreadable length becomes `seconds = 0`. This happens for anything that is not an MP4/MOV header `processing.MP4Duration` reads, such as WebM from browser MediaRecorder, MKV or AVI.
  - `:240`: `isLong := isLongVideoSeconds(0)`, and `limits.go:139` returns true for 0.
  - `:189`: the in-flight cap applies, with `defaultMaxInflightLong = 2`. `store/postgres/upload_quota.go:53` counts every `is_long` row still `uploaded` or `processing`.
  - So a creator posting short WebM reels back to back gets `429 LONG_VIDEO_IN_PROGRESS` at confirm on the third one. The full file is already uploaded and the asset stays `pending_upload`.
  - The init-time check cannot warn them, because only a declared length meets the cap at init.
  - Prod values do not override `MEDIA_UPLOAD_MAX_INFLIGHT_LONG`.
- **Reproduce:** Upload and confirm three ~30 s `.webm` videos in a row while the first two are still `processing`. The third confirm answers 429 with `limit=inflight_long_videos`.
- **Impact:** Reel uploads fail after the upload, on a new code path that existing clients do not handle. Whether this breaks the web depends on how often it sends WebM; Android uploads MP4 and is mostly unaffected.
- **Fix:** Do not apply the in-flight cap when the length is unknown, or classify unknown by size (for example under 500 MB means not long). The worker routes by its own ffprobe anyway.
- **Acceptance:** A unit test of `admitVideoAtConfirm` with a probe error and two in-flight long rows admits the upload. The bytes and seconds budgets still apply.

## REG-4: valid caption files with overlapping cues are now refused with 400 CAPTION_OVERLAP (P2)

- **Evidence level:** VERIFIED-RUN
- **Commits:** 5bee3ff8a (media: uploaded WebVTT and SRT captions keep their own timings), with web 5d54a0a
- **Evidence:**
  - `internal/captions/cuefile.go:203`: `if startMs < prevEnd` returns `CodeCaptionOverlap`.
  - WebVTT allows overlapping cues: two speakers, rolling captions, and YouTube auto-caption exports all produce them. Many SRT tools also emit small overlaps.
  - Before this round the same uploads were accepted, though stored as one flat block.
  - Run: a copy of the package in a scratch module, with `ParseWebVTT` on cues 1.000-4.000 and 3.500-6.000, returns `line 6: cue starts at 00:00:03.500, before the previous cue ends ... must not overlap`. `ParseSRT` behaves the same.
- **Impact:** Creators cannot upload common professional or exported caption files on the web or Android caption routes.
- **Fix:** Accept overlaps. Sort by start time and keep each cue's own end. Players handle simultaneous cues. Keep refusing only end <= start.
- **Acceptance:** The overlapping VTT and SRT above parse into two cues and render back as valid WebVTT.

## REG-5: a running fan-out job drops the "post became visible" wake-up, so the announcement can be up to 30 min late (P3)

- **Evidence level:** VERIFIED-READ
- **Commit:** 3f2fe1e4e (notification: upload fan-out waits for a processing video)
- **Evidence:**
  - `notification-service/internal/store/postgres/fanout_jobs.go:79-90`: the enqueue `DO UPDATE` wakes only a `failed` job or a `pending` one not yet due. A job that is `running` (claimed, mid post-state read) is untouched.
  - `internal/service/subscriber_fanout.go:321`: the worker then defers it with `fanoutBackoff(waits+1)`, which reaches 30 min after about 7 waits.
  - Sequence:
    1. The worker claims the job.
    2. It reads the post: still processing, so the outcome is "wait".
    3. The review gate flips the post, and the trigger finds the job `running`, so the trigger is a no-op.
    4. The worker writes `available_at = now + up to 30 min`.
- **Impact:** Subscriber notifications for a long video arrive late. Nothing is lost.
- **Fix:** In the defer `UPDATE`, compare `updated_at` with the value at claim time. Alternatively, let the enqueue on a `running` row set a `wake_requested` flag that `DeferFanoutJob` honours by deferring to `NOW()`.
- **Acceptance:** In a test, a trigger arriving between claim and defer leaves the job due immediately.

## REG-6: a scheduled video whose upload failed never publishes and is never resolved (P3)

- **Evidence level:** VERIFIED-READ
- **Commit:** 8ed473d16
- **Evidence:**
  - `post-service/internal/store/postgres/scheduled.go:50-52`: `scheduledVideoReadySQL` excludes any post with a `video_metadata` row whose `upload_status <> 'ready'`.
  - `videogate.Flip` marks failed media `upload_status='failed'` and rejects the post.
  - The scheduled post therefore stays scheduled for ever. The worker skips it on every tick, with no metric and no state change for the creator.
  - Before, it published at `publish_at`, visible to the author only.
- **Impact:** A creator's scheduled upload sits as "scheduled" for ever. Low volume.
- **Fix:** Treat `upload_status='failed'` (or `review_status='rejected'`) as due, so the existing publish path runs. Or count and log the skipped rows.
- **Acceptance:** A failed-media scheduled post leaves the scheduled list after its time.

## REG-7: the new client fields break against a post-service that has not been deployed yet (deploy-order hazard) (P3)

- **Evidence level:** VERIFIED-READ
- **Commits:** web 5d54a0a; server a8a90d515
- **Evidence:**
  - The web studio always sends `age_restricted` in the draft PATCH (`useUploadStudio.ts:536`).
  - The draft payload is decoded with `DisallowUnknownFields` (`post_drafts.go:293`). A post-service from before a8a90d515 answers 400 on every draft save, including `age_restricted:false`.
  - The post create route is not strict and is unaffected.
- **Impact:** Draft save fails for everyone if the web ships before post-service.
- **Fix:** Send the field only when true, or document and enforce the deploy order: post-service first.
- **Acceptance:** A draft save from the new web client against the previous post-service image succeeds.

## REG-8: the web's SRT-to-VTT conversion produces timestamps the new server parser refuses (P3)

- **Evidence level:** VERIFIED-RUN
- **Commits:** web 5d54a0a; server 5bee3ff8a
- **Evidence:**
  - `pb-echo src/features/posttube/data/captionUpload.ts:43` rewrites only `\d{2}:\d{2}:\d{2},\d{3}`. The client pre-check at `SRT_TIMING` accepts 1-digit hours (`\d{1,2}`).
  - An SRT written with `0:00:01,000` passes the client check and is sent as `format:"vtt"` with the comma still in place.
  - Run: `ParseWebVTT("WEBVTT\n\n1\n0:00:01,000 --> 0:00:02,000\nA\n")` returns `start time "0:00:01,000" is not a valid timestamp`.
- **Impact:** A 400 with no explanation for those files.
- **Fix:** On the web, convert `(\d{1,2}):(\d{2}):(\d{2}),(\d{3})`. Better, send `format:"srt"` with the raw file, since the server now parses SRT itself.
- **Acceptance:** A 1-digit-hour SRT uploads from the web.

## REG-9: two workers may encode the same unit concurrently after a lock-free redelivery path (P3)

- **Evidence level:** SUSPECTED
- **Commit:** 8f72fcd9e
- **Evidence:**
  - Runs of one event id are serialised by `WithTranscodeEventLock` (`media_outbox.go:177`, a session advisory lock), so the common rebalance case is safe.
  - However, `supersededDelivery` lets a reprocess (a new event id) and the original run in parallel on the same `baseKey`. Both write the same `hls/<q>_NNN.ts` keys and `rungs/<unit>.json` markers.
  - Markers pin the generation, so a stale marker is not reused. But segment files of two different recipes or rotations could interleave under one playlist if both finish.
- **What would confirm it:** An e2e run that sends a reprocess (with rotation) while the original long transcode is mid-ladder, then compares the segment hashes against the playlist.
- **Fix:** Lock on the media id, not the event id. Or write each generation under its own `hls/g<gen>/` prefix.

---

## Looked at and found sound

- `TrustedViewerIdentity`: an empty internal key fails closed (header stripped). A verified token overwrites `X-User-Id`. It is mounted with `r.Use` before routes are registered.
- JWT policy: `typ` refresh and mini-app tokens are refused. Auth-service refresh tokens are opaque, not JWTs. `exp` is mandatory. An empty HS secret is no key.
- CloudFront wildcard grant: scoped to `user/<uploader>/<id>/hls/*.ts`, one asset. `*` and `?` are refused in both the directory and the CDN base. Only protected keys are signed. Authorization runs on every call and is never cached. The cache key is the resource, which is correct because the signature is a bearer capability.
- The playlist answers with `Cache-Control: private, no-store` and exposes `X-HLS-Expires-At` itself.
- Segment TTL clamp, bounded LRU sign cache with per-insert eviction, and the reuse window.
- Durable rungs: the marker is written only after every upload. Reuse checks media id, generation, ETag, recipe and object sizes. Local files are deleted per unit. `tmpDir` is removed. `errWorkerDraining` leaves the offset uncommitted. Runs of one event id are serialised by an advisory lock, so a rebalance does not double-run a job.
- Lost-run arithmetic `starts-1-finishes` with `>= max`: no off-by-one. A clean drain records the finish (prod values only, see REG-2).
- Migration 033: additive, defaulted columns. Migration 034: new tables. 086: a bounded rewrite, `SET NULL` foreign keys, idempotent.
- Quota ledger: the per-user advisory xact lock prevents double charging. A retried confirm returns `ErrUploadAlreadyCharged`, which is admitted. A failed confirm leaves nothing charged.
- Resumable sliding expiry: `TouchResumableUpload` only touches `initiated`/`uploading` unexpired rows and is capped at `created_at + max`. The S3 multipart abort (7 d) equals the max lifetime.
- post-service review gate: errors now retry. `ErrNoRows` (no row) is a no-op. Flip is compare-and-set, so the sweep, the consumer and moderators race safely. The sweep only flips `pending` posts, and only the media gate sets `pending`.
- `video_metadata` in the create transaction: no unique constraint on `media_asset_id`, so no new create failures.
- `age_restricted` at create: the column exists (migration 052). The create route is not strict, so older clients are unaffected.
- `PostSearchTaxonomy`: no key collisions with the other `PostCreated` or eligibility fields. Consumers checked: feed, analytics, notification and search. None strict-decodes these keys, and search strips and reads them field by field. The `posts.tags`/`hashtags`/`category`/`language` columns exist with matching types.
- The scheduled publish now emits eligibility and `PostCreated` at the same revision. Notification and search de-duplicate.
- Search trending: a terms aggregation on the keyword `hashtags`, gated public+approved, so the response shape is unchanged. The new posts mapping fields are additive.
- Notification fan-out:
  - One row per post.
  - A completed job is never reopened.
  - Per-recipient markers.
  - The `notify_subscribers=false` check is made on the owner read. post-service trusts `X-User-Id` with the internal key, so it is owner-scoped.
  - The 6-day trigger window is inside the 7-day retention.
- Analytics views route: owner or admin only, and no in-cluster caller (grep). Purge deletes are batched. `analytics` was added to the required acks with a consumer wired.
- Alerts: every series referenced in prod values is emitted, with the class and reason labels pre-created. The `{class=""}` rule is intentionally silent.
- The `variants` merge in the worker chart (no `.Values` mutation); PDB selectors.
- Android f8cc49701 (read only):
  - Media cache teardown is unqualified `SimpleCache`, wrapped in `runCatching`.
  - `sent_at_ms` is accepted by post-service.
  - `age_restricted` is only sent when true.
  - The 18+ guard fails closed to private.
- Web `hlsAuth`: credentials go only to API origins; segments are fetched without credentials. The `hlsRefresh` loaders keep the MediaSource, and a 403 triggers one retry.

## Not reviewed

- media `internal/processing` audio tool hardening (`audio_tool.go`), `threads.go`, the `hls.go` rewrite internals, `reclaim_policy.go`, `orphan_gc.go`, `ratelimit.go` changes.
- notification `publicReadable` per-recipient visibility internals beyond the job-level gate.
- analytics purge `eraser.go` Scylla and Redis paths, and the `personalization/purge.go` key patterns.
- Web: `hlsPreload.ts`, `telemetry.ts`, `watchSession.ts`, `useWatchTelemetry.ts`, `CollectionView`/`libraryApi`, the middleware playlist page.
- Android: `ReelsScreen`/`ReelsViewModel` pause state, `CaptureVideoUpTo`, `ReelSurface`, `PlayerPool` tests.
- Whether the migration runner wraps files in a transaction. 086 uses `SET LOCAL lock_timeout`, which is a no-op outside a transaction.
- `scripts/ci/kafka-topics`, k6 load tests, e2e harness changes, and the commerce and payments commits (out of scope).
- No unit tests in the repo were run. Only the scratch caption probe was executed.
