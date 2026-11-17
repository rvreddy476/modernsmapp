# Track C: data integrity and concurrency (reels)

- **Date:** 2026-11-17. Read-only. Working tree of `codex/module-01-02-launch-safety` @ `770f5ed9d` (dirty).
- **Paths** are relative to `Architecture/services/` unless they start with `deploy/`, `infra/` or `Architecture/`. `media/`, `post/`, `analytics/` mean `media-service/`, `post-service/`, `analytics-service/`.
- **Not repeated:** likes, comments, the post-service outbox relay, the feed consumer and counter flushes (feed Track C, fixed 16-17 Nov per `docs/production-readiness/feed/05-fix-plan.md`). Reels share those paths, so those fixes cover them too.
- **Verdict for this track:** no P0. Four P1s, five P2s, four P3s. The transcode worker itself is careful and idempotent. The weak point is the hand-off from "media is ready" to "post is visible". Three paths leave a reel invisible to everyone but its author, forever, with no sweeper and no message: (1) a failed status read at create, (2) a swallowed flip error, (3) a scan with no verdict. Nothing found lets a reel be seen before processing and moderation complete.
- **Commands run** (all `ok`):
  - `go test ./cmd/worker -run 'TestTransientFailureIsQuarantinedAfterMaxAttempts|TestUnwritableQuarantineDoesNotCommit|TestForwardFailureTranscodesHere|TestAssetDeletedMidRunIsSkippedAndItsObjectsRemoved|TestCompletionForADeletedAssetIsAcceptedNotRetried' -count=1` in `media/`
  - `go test ./internal/store/postgres -run TestStallRule -count=1` in `media/`
  - `go test ./internal/consumers -run 'Reclassify|ReviewGate|Finalize' -count=1 -v` in `post/` (2 tests matched, both pass)

## Answers to the map's open questions

| Q | Answer | Evidence |
|---|---|---|
| 3: `moderation_status` CHECK vs `manual_review` | **The write does not fail.** The CHECK never exists on a database built by the service. `BootstrapSchema` runs `setup.sql` before the migrations (`media/internal/store/postgres/bootstrap.go:81-87`). `setup.sql:57` adds `moderation_status` with no CHECK. Migration 008 then runs `ADD COLUMN IF NOT EXISTS … CHECK (…)` (`008_media_moderation.sql:4-6`): the column exists, so Postgres skips the whole column definition, constraint included. Both lines arrived in the same commit (`91b2323e9`), so no environment ever ran 008 first. The integration suite writes `manual_review` through `CompleteTranscode` against the real schema (`reels_hardening_integration_test.go:63`). No other CHECK on the column exists (grep across all `.sql` and `.go`). If the CHECK ever did exist (a schema built from the migrations alone), the completion write would fail as transient: 8 attempts, then quarantine, then the asset stays `processing`. The sweeper's give-up writes `manual_review` too and would fail every 5 minutes, so the asset would sit in `processing` forever (C-12). The real defect behind `manual_review` is C-2: nothing ever moves it on. | as cited |
| 2: `media.events.long` | Not in the MSK topic list (`infra/terraform/modules/msk/topics-job/variables.tf:29-59`, which has `media.events` and `media.events.dlq` only), and `auto.create.topics.enable=false` (`modules/msk/main.tf:147`). Prod runs `MEDIA_TRANSCODE_CLASSES: "short,long"` (`deploy/services/media-service/values-prod.yaml:176`). With the topic missing, every forward fails, and `forwardIfLong` then transcodes the request on the short loop (`media/cmd/worker/transcode_classes.go:214-221`). Nothing is lost; the long loop just spins on fetch errors (`:232-238`). Consequence: C-3. | as cited |
| 4: `media.copyright.pairs` | Provisioned (`variables.tf:40`), produced only when `COPYRIGHT_PAIR_RELAY` is on (default off), consumed by nothing. Today the pairs end in `copyright_pairs`/`copyright_pair_outbox` in media's database, matching the shadow-mode decision. If the relay is switched on, rows are marked published and nobody acts on them (C-13). | `media/cmd/server/main.go:192-197`; grep for the topic in all services finds no reader |
| 7 (data side) | Sound audience gating was not re-traced (Track B). Data side: a sound whose source is **deleted** is retired, not destroyed (migration 032, `media/internal/service/sound_retirement.go`). It cannot be attached, and existing reels keep playing it. | `032_sound_retirement.sql:1-40` |

## State machine

Source of truth for each column in brackets. "Guard" means what stops a repeated or concurrent delivery from doing harm.

| Object | From → To | Moved by | Atomic | Guard against repeat or race | Evidence |
|---|---|---|---|---|---|
| media_assets.processing_status [media PG] | (none) → `pending_upload` | `POST /v1/media/init`, resumable init (multipart opened first, aborted if the row insert fails) | single insert | new uuid per call | `media/internal/service/resumable.go:131-174` |
| | `pending_upload`/`uploaded` → `uploaded` | confirm: `MarkUploadConfirmed` | yes | conditional `WHERE processing_status IN ('pending_upload','uploaded')`; a loser gets `ErrUploadAlreadyConfirmed` and answers idempotently | `media_generation.go:43-61`, `upload.go:236-240,295-296` |
| | `uploaded` → `processing` + request outbox row | `QueueTranscode` | yes (one tx, row lock) | conditional UPDATE; outbox `UNIQUE(media_asset_id,event_type)` | `media_outbox.go:30-67`. **Separate commit from the step above: C-6** |
| | `pending_upload` → `rejected` | confirm size, MIME or magic failure | — | — | map step 2c |
| | `processing` → `ready` \| `failed`, plus `moderation_status`, inbox row and completion outbox row | `CompleteTranscode` | yes (one tx, `FOR UPDATE`) | inbox PK on `event_id`; per-event advisory lock; generation fence; asset-gone becomes a skip | `transcode_inbox.go:102-249`, `media_outbox.go:177-195`, `cmd/worker/main.go:292-301,325-335` |
| | `processing` → new request (gen+1) \| `failed` | stall sweeper (heartbeat older than 10 min, or NULL for 30 min with the pipeline idle; max 3) | yes (per-row tx, `SKIP LOCKED`) | refuses while any request or completion is unpublished; old message skipped as superseded | `transcode_lease.go:69-76,224-396` |
| | `ready`/`failed` → `processing` (gen+1) | internal reprocess | yes | same in-flight refusal; row lock | `media_outbox.go:250-358` |
| | `pending_upload` older than 24 h → deleted | orphan GC | re-checked under lock | references re-checked | `orphan_gc.go`, `media.go:530-547` |
| | `uploaded` older than any age → nothing | **no sweeper selects it** | — | — | C-6 |
| media_assets.moderation_status [media PG] | `pending` → `passed` \| `rejected` \| `manual_review` | worker scan, with `CompleteTranscode` | yes | as above | `cmd/worker/main.go:433-441,985-1013` |
| | `manual_review` → anything | **no writer exists** (`UpdateMediaModerationStatus` has no caller; no other service updates `media_assets`) | — | — | C-2 |
| transcoding_jobs.status | `queued` → `processing` → `completed`/`failed` | worker; sweeper fails lingering rows | per row | — | `transcode_lease.go:465-483` |
| Kafka message (media.events) | fetched → committed | worker consume loop | commit only after the durable effect | 8 attempts (2 s doubling to 2 min), then `media_transcode_quarantine`, then commit; quarantine write failure leaves the offset uncommitted (tested) | `transcode_guards.go:130-167`, `transcode_classes.go:229-255` |
| resumable_uploads.status | `initiated` → `uploading` → `completed`; → `expired` | chunk; complete; reclaim worker | per statement | part upsert on PK; size-exact parts; double complete re-confirms | `resumable.go:239-361`, `store/postgres/resumable.go:61-150`; C-10 |
| posts.review_status [post PG] | (create) → `approved`\|`pending`\|`rejected`\|`flagged` | create: `gateVideoReviewStatus` reads media status **over HTTP before the insert** | no | — | `post/internal/service/post_create.go:494-496`, `post_review.go:153-172`; C-1, C-2 |
| | `pending` → `approved`\|`rejected` | `MediaTranscodeConsumer.finalizeReviewGate` | yes (`WHERE review_status='pending'`) | conditional flip, so duplicates are no-ops | `post/internal/consumers/media.go:353-398`, `store/postgres/posts.go:1540-1551`; C-4 |
| | `rejected` (from a failed transcode) → anything | only the owner's `/resubmit` or a moderator; a successful reprocess does not reopen it | — | — | C-5 |
| posts.publish_at | set → NULL (live) | schedule worker or "publish now" | yes (one tx with the outbox row) | `WHERE publish_at IS NOT NULL AND deleted_at IS NULL [AND publish_at <= now]` | `post/internal/store/postgres/scheduled.go:114-175` |
| reel_drafts.status | `draft` → `publishing_pending` → `published`\|`blocked` | draft worker claim (`SKIP LOCKED`, stale reclaim), `PublishDraft` | claim yes; post create plus mark-published no, but idempotent | draft id = post id, so a second create hits the posts PK and resolves to the existing post | `post/internal/service/drafts.go:564-635,768-862`, `store/postgres/drafts.go:401-489` |
| video_metadata | `pending` → `ready` + playback URL + measured kind | media consumer | per statement | idempotent overwrite | `consumers/media.go:123-220` |

## Findings

```
ID:            C-1
Title:         A reel whose media is already ready when its post is created stays 'pending' (visible only to its author) forever if create's HTTP status read fails or races the completion event; nothing re-checks pending video posts.
Severity:      P1
Category:      data
Evidence level: VERIFIED-READ
Evidence:      post/internal/service/post_create.go:494-496 calls gateVideoReviewStatus before the post row exists. post_review.go:161-166 answers "pending" on any error from GET media-service /v1/media/:id (3 s client, post_review.go:177-208). The only later writer is finalizeReviewGate on a media.transcode.completed event (consumers/media.go:353-398). The web publishes only after /renditions reports all_ready (map step 9), so that event was consumed before the post existed; finalizeReviewGate found no video_metadata row and returned (:366-369). A grep for review_status='pending' outside the consumer and store finds no reconciler or sweeper.
How to reproduce: Wait for ready+passed, make media-service return 503 (or slow it past 3 s) for one request, then POST /v1/posts content_type=flick. The post is stored 'pending' and never changes. Race variant: POST while the worker is completing; the completion is consumed between the status read and the insert.
Impact:        A normal reel never reaches anyone. The author sees it on their own profile, so they assume it is live. No error, no alert.
Fix:           Make the gate durable: after the insert, if review_status='pending' and the media is a video, re-run the decision from post-service's own read of media_assets (BatchGetMediaOwnership already reads that table, processing.go:79-82). Add a periodic sweep: "pending video posts older than 5 min whose media is ready" → finalizeReviewGate.
Acceptance:    Unit test: gateVideoReviewStatus erroring, media ready+passed in the store → the post ends 'approved' after the sweep tick. Fails today.
Effort:        S
```

```
ID:            C-2
Title:         'manual_review' is a dead end: nothing ever moves a media asset out of it, and the create-time gate turns ready+manual_review into an 'approved' post that no one but the author can play.
Severity:      P1
Category:      data
Evidence level: VERIFIED-READ
Evidence:      Worker: no scan verdict (scanner error, Rekognition throttle, no frames) → manual_review (cmd/worker/main.go:436-441, 995). The only setter, UpdateMediaModerationStatus (media/internal/store/postgres/media.go:114-120), has no caller. No other service runs UPDATE media_assets. Grep of admin-, trust-safety- and reviewer-service for manual_review finds only an unrelated story consumer. Post side: finalizeReviewGate ignores manual_review (consumers/media.go:363), so a post created during processing stays 'pending' and is never in a "pending video" queue. A post created after the asset is ready takes gateVideoReviewStatus, which returns "approved" for anything but rejected (post_review.go:167-172). Delivery to non-owners still requires passed (story_surface.go:262-267), and applyMediaState marks the post IsProcessing (processing.go:39-73).
How to reproduce: Run the worker with the scanner returning an error for every frame (or Rekognition throttled). Every reel finished in that window is ready+manual_review. Publish one: it is either 'pending' forever or 'approved' but unplayable.
Impact:        A scanner outage silently strands every reel uploaded during it. Moderators have no queue that lists these assets. The only recovery is the internal reprocess route, per asset, by someone who knows to look.
Fix:           (a) In gateVideoReviewStatus, return "pending" unless moderation is passed/approved; return "flagged" for manual_review so it enters the existing human queue. (b) In finalizeReviewGate, map ready+manual_review to "flagged". (c) Add a retry: the stall sweeper (or a sibling) re-queues ready+manual_review assets once the scanner is healthy, capped. Moderator approval of a flagged video post must also set the media to passed (one keyed internal route calling UpdateMediaModerationStatus).
Acceptance:    Consumer test: completion {ready, manual_review} → post 'flagged' (today it stays 'pending'). Create test: media ready+manual_review → post not 'approved'.
Effort:        M
```

```
ID:            C-3
Title:         media.events.long is not provisioned in MSK with auto-create off, so in prod every long video is transcoded on the reel queue and reels wait behind it for hours.
Severity:      P1
Category:      reliability
Evidence level: VERIFIED-READ for code and Terraform; UNVERIFIED whether someone created the topic by hand (needs `kafka-topics --describe` on the prod cluster)
Evidence:      infra/terraform/modules/msk/topics-job/variables.tf:29-59 (topic absent), modules/msk/main.tf:147 (auto-create false), deploy/services/media-service/values-prod.yaml:176 (short,long). transcode_classes.go:214-221: on a write error the request is transcoded where it is. The worker consumes one message at a time per loop (:229-255), and a long job may run up to the 48 h budget (:25-27). Local compose creates the topic (Architecture/docker/init-topics.sh:54), so dev never shows it.
How to reproduce: On a cluster without the topic, queue a 40-minute video, then a 15-second reel on the same partition. The reel's status stays 'processing' until the long job ends.
Impact:        A core journey (upload → playable reel) degrades to hours whenever a long video is ahead on the same partition. The sweeper does not help: a NULL heartbeat behind running work is skipped by design (transcode_lease.go:147-150).
Fix:           Add "media.events.long" to the topics-job default list (one line) and re-run the Job.
Acceptance:    Render or plan diff shows the topic. A worker started against the cluster logs "forwarded to media.events.long" for a long request; `media_transcode_routed_total{outcome="forward_failed"}` stays 0.
Effort:        S
```

```
ID:            C-4
Title:         The post-service media consumer drops review-gate and moderation-audit errors and commits the offset, so one database error during finalizeReviewGate leaves the reel 'pending' forever.
Severity:      P1
Category:      data
Evidence level: VERIFIED-READ
Evidence:      consumers/media.go:371-376: a FlipReviewStatusFromPending error is logged and the function returns. handle() continues and returns nil (:110-121, :240), so the shared consumer commits. The insert of the moderation_reviews audit row is also log-only (:384-392). Other errors (UpdateVideoMetadata :170-172, title review :227-235) do return, but the shared consumer gives 3 retries over about 7 s and then writes media.events.dlq (same consumer the feed C-1 finding describes). No replayer exists for that topic.
How to reproduce: Fail the posts UPDATE once (lock timeout, failover) while a completion is consumed. The event is committed; the post stays 'pending'.
Impact:        Same end state as C-1, from a transient database error. Also: a reel past 10 minutes whose reclassify or title-review step hits a 7-second blip lands in the DLQ and keeps the wrong kind or a stuck copyright hold.
Fix:           Return the error from finalizeReviewGate (the flip is conditional, so a retry is safe), and make the consumer RetryForever with only undecodable payloads permanent, as recommended for feed C-1.
Acceptance:    Consumer test with a store whose flip fails once: the handler returns an error on the first delivery and the post is 'approved' after the second. Today the first delivery returns nil.
Effort:        S
```

```
ID:            C-5
Title:         A transcode failure rejects the post permanently: a later successful reprocess (operator or sweeper) never reopens it, and the media events carry no per-asset ordering.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ
Evidence:      finalizeReviewGate maps ProcessingStatus=failed to 'rejected' (consumers/media.go:356-357). FlipReviewStatusFromPending only moves from 'pending' (posts.go:1543-1544), so a later ready+passed completion is a no-op. Failures include the sweeper's give-up after a worker crash or a hung S3 call (transcode_lease.go:422-485), i.e. infrastructure faults, not content. The media producer keys every message by event id (media/internal/events/producer.go:62), so two completions for one asset (generation 1 failed, generation 2 ready) can be consumed in either order.
How to reproduce: Kill the worker mid-transcode three times (the sweeper gives up), then POST /v1/media/internal/:id/reprocess. The asset becomes ready+passed; the post stays 'rejected'.
Impact:        The creator sees "rejected" for an upload that was never judged, and operator recovery does not recover the post. Owner /resubmit exists, but nothing tells them to use it.
Fix:           Map failed → keep 'pending' (or a distinct 'needs_changes', migration 024) instead of 'rejected'. Let finalizeReviewGate flip from that state when a ready+passed completion of a newer generation arrives (the payload carries media_generation). Key media events by media_asset_id (PublishEnvelopeKeyed exists).
Acceptance:    Consumer test: failed then ready+passed completions for one media → post 'approved'. Today it is 'rejected'.
Effort:        S
```

```
ID:            C-6
Title:         Confirm marks a video 'uploaded' and queues its transcode in two commits; a crash or error between them leaves an asset that no sweeper or GC will ever pick up.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ
Evidence:      upload.go:295 MarkUploadConfirmed commits 'uploaded'; upload.go:385 QueueTranscode is a separate transaction (media_outbox.go:30-67). The stall sweeper selects only 'processing' (and ready reprocesses) (transcode_lease.go:234-251). Orphan GC selects only 'pending_upload' (media.go:541-547). OldestQueuedTranscodeWait explicitly ignores 'uploaded' ("which nothing is going to pick up", media_generation.go:171-175). Recovery happens only if the client re-confirms (upload.go:240 accepts 'uploaded').
How to reproduce: Fail the QueueTranscode transaction once (database restart) on a web upload whose tab is then closed.
Impact:        The reel shows "processing" to its author forever. No count, no alert.
Fix:           Do the 'uploaded' update for videos inside QueueTranscode's transaction (it already locks the row), or let the sweeper treat a video at 'uploaded' for more than 30 minutes with no request row as a re-queue candidate.
Acceptance:    Integration test: QueueTranscode fails after MarkUploadConfirmed → after one sweep the asset has a request outbox row. Today it has none.
Effort:        S
```

```
ID:            C-7
Title:         Quarantined transcode messages and media.events.dlq are not visible to anyone: no metric, alert, read route or replay exists.
Severity:      P2
Category:      observability
Evidence level: VERIFIED-READ
Evidence:      media_transcode_quarantine is written (media_outbox.go:156-172) and referenced only by its migration and two integration tests (grep over Architecture, deploy, scripts). readiness_metrics.go exports no quarantine gauge. deploy/services/media-service/*.yaml has no rule for it or for media.events.dlq. The worker has no ServiceMonitor (map section 8).
How to reproduce: Send a malformed record to media.events: one quarantine row, and nothing anywhere reports it.
Impact:        The design's "a dead-letter path someone can see" is half there: the bytes are kept, but nobody is told. Assets behind a quarantined request do recover through the sweeper (they end 'failed', see C-5).
Fix:           A gauge for quarantine row count and age in runReadinessGauges, plus one PrometheusRule. A keyed read route or a documented SQL query is enough for replay.
Acceptance:    The gauge rises after a test quarantine; the rule renders in `helm template`.
Effort:        S
```

```
ID:            C-8
Title:         Sound use counts only ever go up: POST /v1/audio/:id/use adds one on every call with no per-user dedupe, attach adds again, and deleting a reel never subtracts.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ for the writes; SUSPECTED that clients call /use and attach for the same reel (would need the Android and web call sites read together)
Evidence:      media/internal/service/audio_reads.go:219-233 → IncrementAudioUsageCount (store/postgres/audio.go:292-303), unconditional +1. post/internal/service/post.go:467-474 and store/postgres/audio.go:156-163 count again on attach (attach itself is idempotent: AttachAudioToPost counts only a new link). Grep finds no decrement of use_count/usage_count anywhere. The only limit on /use is the gateway's 60 rps per user.
How to reproduce: Loop POST /v1/audio/<id>/use as one user: usage_count climbs by one per call.
Impact:        "N videos use this sound" and the catalogue's sort order can be inflated by one account and drift from the real number of reels. Whether trending sounds reads these columns was not determined.
Fix:           Make /use count once per (user, sound), or drop the count from /use and leave attach as the only counter. Recount nightly from posts.audio_track_id for live posts.
Acceptance:    Test: 10 calls of /use by one user move usage_count by at most 1.
Effort:        S
```

```
ID:            C-9
Title:         Deleting a media asset (owner, orphan GC, purge) deletes its resumable session rows without aborting the storage multipart upload, so uploaded parts are left in the bucket.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      media/internal/store/postgres/asset_purge.go:161-163 and purge.go:103-105 delete resumable_upload_parts and resumable_uploads. purge.go:89-90 records only object_key for reclaim, not storage_upload_id. resumable_uploads also cascades on the media FK (setup.sql:83). The expiry worker that aborts multiparts reads only surviving rows (store/postgres/resumable.go:104-131). Orphan GC (24 h after created_at) and session expiry (24 h after init) race for the same rows.
Impact:        Storage cost only. In AWS the bucket lifecycle aborts incomplete multiparts after 7 days (map 5.5); MinIO keeps them forever.
Fix:           Abort the multipart (or record storage_upload_id in media_blob_reclaim) before deleting the session row.
Effort:        S
```

```
ID:            C-10
Title:         A late or re-sent chunk can move a completed resumable session back to 'uploading', and 'aborted' sessions still accept chunks.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      UploadPart checks status in memory (resumable.go:247-252), then UpdateResumableUploadProgress writes status='uploading' unconditionally (store/postgres/resumable.go:61-67). 'aborted' is never checked.
Impact:        Status shown to a resuming client is wrong. A retried complete still succeeds (NoSuchUpload + stat path, resumable.go:348-353), so no upload is lost.
Fix:           `WHERE upload_id=$1 AND status IN ('initiated','uploading')` on the progress update, and refuse chunks unless the session is in those states.
Effort:        S
```

```
ID:            C-11
Title:         Today's view count is uncapped and falls back at the next day's rollup, so the creator-facing number can go down and differs from the paid number intra-day.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      ContentViewsBatch sums capped daily rows plus today's uncapped hourly rows (analytics/internal/store/postgres/aggregates.go:330-378, comment at :337-342). The cap (1 per viewer per content per day) applies only in the daily rollup (aggregation/daily.go:25-34). Sessions are client-minted ids (ingest.go:351-361), so one viewer can add a view per new session until midnight.
Impact:        A looping viewer or a script can inflate today's count; it drops after the rollup. Money is not affected: payment reads the capped daily table.
Fix:           Apply the same per-viewer cap to the open-hours term (count distinct viewers per content for today), or label it as provisional on clients.
Effort:        S
```

```
ID:            C-12
Title:         Migration 008's CHECK on moderation_status is a no-op that hides the real value set; if the schema were ever built from migrations alone, every manual_review completion would fail and loop.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      See open question 3 above. Values actually written: pending, passed, rejected, manual_review, plus voice values (service/voice.go:31). A failed CompleteTranscode is transient → 8 attempts → quarantine → sweeper give-up also writes manual_review (transcode_lease.go:446-448) and fails each sweep.
Fix:           A new migration that drops any media_assets moderation CHECK and adds one listing every written value NOT VALID, so the schema states the truth whichever way it was built.
Effort:        S
```

```
ID:            C-13
Title:         media.copyright.pairs has no consumer; enabling COPYRIGHT_PAIR_RELAY would mark pair rows published while nothing acts on them.
Severity:      P3
Category:      data
Evidence level: VERIFIED-READ
Evidence:      Producer gated off (media/cmd/server/main.go:192-197); no consumer in any service (grep); topic provisioned (variables.tf:40).
Impact:        None while the relay is off (shadow mode, matches in the database only). It becomes a silent loss of every pair event once someone flips the switch.
Fix:           Leave the relay off until trust-safety has a consumer; add a boot refusal if the relay is on with no declared consumer group.
Effort:        S
```

## Checked and found sound (for re-verification)

| Area | What holds | Evidence |
|---|---|---|
| Transcode idempotency | Same event delivered twice, or two workers on one event: per-event session advisory lock, inbox pre-check, and the inbox PK inside CompleteTranscode. The loser gets ErrTranscodeAlreadyApplied, treated as success | `media_outbox.go:177-195`, `transcode_inbox.go:158-168`, `cmd/worker/main.go:292-335` |
| Stale generations | A completion for a superseded generation records only its inbox row; ready_generation moves only for the current generation; the row is locked FOR UPDATE | `transcode_inbox.go:170-190` |
| Offsets | Committed only after a durable effect, or after the quarantine row is written; quarantine failure keeps the offset (VERIFIED-RUN, TestUnwritableQuarantineDoesNotCommit) | `transcode_guards.go:130-167`, `transcode_classes.go:241-254` |
| Retry limit | 8 attempts, 2 s doubling to 2 min, then quarantine (VERIFIED-RUN, TestTransientFailureIsQuarantinedAfterMaxAttempts) | `transcode_guards.go:53-85` |
| Stuck in processing | Worker crash: heartbeat stale after 10 min → re-queue with a new generation, up to 3, then 'failed' with the reason on transcoding_jobs (shown by `/status`). Lost or never-consumed request: NULL heartbeat for 30 min with the pipeline idle → same. SKIP LOCKED per row; in-flight refusal (VERIFIED-RUN, TestStallRule*). **Does not recover:** assets at 'uploaded' (C-6), assets waiting behind a busy pipeline (deliberate), and the post side (C-1, C-2, C-4, C-5) | `transcode_lease.go` |
| Delete during upload | Pending_upload rows are GC'd after 24 h; owner delete is refused while post_media references the asset (FK RESTRICT) or a claim exists (migration 031) | `orphan_gc.go`, `031_media_claims.sql` |
| Delete during processing | Asset gone before, during or after the run is a skip, with the run's objects removed under the exact prefix (VERIFIED-RUN, TestAssetDeletedMidRunIsSkippedAndItsObjectsRemoved, TestCompletionForADeletedAssetIsAcceptedNotRetried) | `cmd/worker/main.go:342-345,373-380,467-472`, `transcode_guards.go:368-393` |
| Confirm racing confirm | Conditional MarkUploadConfirmed; a late confirm can no longer move ready back to uploaded | `media_generation.go:31-61` |
| Resumable | Part upsert on (upload_id, part_number); exact part sizes; progress recomputed from rows; double complete → NoSuchUpload + stat of the declared size → confirm (idempotent); expiry aborts the multipart under SKIP LOCKED | `resumable.go:239-361`, `store/postgres/resumable.go:104-150` |
| Reachable before ready+passed | Non-owner delivery requires moderation passed/approved; posts with unpublishable media are IsProcessing; read filters require review_status approved | `post/internal/service/story_surface.go:252-269`, `processing.go:39-73` |
| Media events | Outbox in the same transaction as the state change, stable event_id on every re-publish; consumers idempotent (inbox; conditional flips) | `media_outbox.go:30-128`, `transcode_inbox.go:237-247` |
| Scheduled post | One guarded UPDATE plus the outbox row in one tx; a second tick or "publish now" is a no-op | `post/internal/store/postgres/scheduled.go:114-175` |
| Draft publish | Draft id = post id; concurrent manual and worker publish resolve to one post; claim SKIP LOCKED with stale reclaim, retry plan and park | `post/internal/service/drafts.go:607-635,768-862` |
| View rule | A view is a playback session (client session_id, required for playback events); each event has a receipt (ON CONFLICT DO NOTHING), so a replayed batch counts nothing; watch time clamped to the **server-measured** duration × loops; display view judged on the server; self-views flagged; paid and creator-facing totals come from the same capped daily table (1 per viewer per content per UTC day). `POST /v1/reels/:id/view` is retired | `analytics/internal/service/ingest.go:190-249,350-600`, `store/postgres/events.go:195-230`, `aggregation/daily.go:18-34`, `aggregates.go:330-378` |
| Soft delete in reels reads | By-sound page: public, deleted_at IS NULL, publish_at IS NULL, approved. Liked reels: ViewerEligibleSQL | `post/internal/store/postgres/audio.go:283-306`, `liked_reels.go:102-115` |
| Sounds | One sound per source video (unique partial index, migration 024 with a locked dedupe); retired, not destroyed, on source delete (032); attach counts only a new link | `024_sound_per_source.sql`, `032_sound_retirement.sql`, `post/internal/store/postgres/audio.go:186-190` |
| Newest migrations | media 032 and post 085 are additive (IF NOT EXISTS, nullable or defaulted columns; 085 sets lock_timeout 5s), so the previous app version runs against them | files as named |

## Not verified

- Whether `media.events.long` exists on any deployed MSK cluster (C-3); needs the cluster's topic list.
- Whether the Android and web clients call `POST /v1/audio/:id/use` and attach a sound for the same reel (C-8 double count).
- Trending scores: what `audio_trending_sounds` and hashtag trending rank on, and whether deleted reels are subtracted (hashtag `use_count` only increments at create, `post/internal/store/postgres/hashtags.go:122-134`).
- Whether anything still writes Scylla `social_analytics.reel_views` / `reel_view_counts` (counter tables, no dedupe) and whether any surface reads them.
- Anonymous viewers in view counting (how the cap groups a nil actor, `aggregation/daily.go:433`).
- Search indexing of flicks on delete or private (Track B / feed already cover the shared path).
- Edit racing draft publish (`UpdateDraft` against a `publishing_pending` claim) was not read.
- A zombie worker past its budget writing the same object keys as the re-queued generation: the generation fence stops its database write, but object overwrites under the shared prefix were not traced.
- `media_renditions` table writers (map question 12).
- No integration test was run (no database, by the rules); every database-behaviour claim is from reading.
