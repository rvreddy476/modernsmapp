# Verifier v1: the reel pipeline (review gate, data, reliability, tests)

Independent verification, read-only. For each finding only the Title and
Evidence fields of the track file were read; everything below was checked
against the code. Paths are relative to `Architecture/services/` unless they
start with `infra/`, `deploy/` or `charts/`.

Two facts change several verdicts and were not in any cited evidence:

1. **The worker never writes `ready` + `manual_review` (or `ready` + `pending`).**
   `runTranscode` (media-service/cmd/worker/main.go:609-1016) has a single
   `return nil` (:1015), reached only after the scan sets the result to
   `passed` or `rejected` (:994-1012). A scanner error returns
   `classifyScanError` (:999, :1031-1037): transient (retried, then quarantine,
   then the stall sweeper) or permanent. The "no verdict -> manual_review"
   branch (:435-440) is dead code on the success path. `manual_review` is
   only written together with `failed` (main.go:392-396; store/postgres/transcode_lease.go:446).
   Reprocess (internal/service/reprocess.go:55-70, RequeueTranscode) leaves the
   statuses alone, and imports start `processing` + `pending`
   (store/postgres/recording_import.go:66-73).
2. **Two read-time gates hide any post whose media is not exactly `ready` + `passed`,
   whatever its `review_status`.** post-service/internal/service/processing.go:39-41
   (`mediaPublishable`), :50-73 (`IsProcessing`), :90-95 (`hiddenWhileProcessing`);
   feed-service/internal/service/processingfilter.go:32 and hydration.go:893;
   and the byte gate post-service/internal/service/story_surface.go:252-270
   (`EvaluateMediaAccessFacts`: non-owner needs `passed`/`approved`), called at
   :322 and :487. The pure rules are unit tested (processing_test.go:47-88).

---

## A-1: create-time gate approves on ready + anything but rejected

- **Verdict:** DISPROVED (as a publication bug). Residual hardening only.
- **Evidence level:** VERIFIED-READ
- **Checked:** post-service/internal/service/post_review.go:161-174 does return
  `"approved"` for `ready` + anything but `rejected`, and it is the only caller
  path (post_create.go:494-495). The voice gate (:130-151) is stricter, as cited.
  But (1) `ready` + `manual_review`/`pending` is not produced by the worker, and
  (2) even if it were, the post is `IsProcessing` and author-only on every read
  surface and the bytes are refused to non-owners (fact 2 above). Create also
  accepts `manual_review` on purpose and relies on that rule
  (create_guards.go:194-203). Nothing unscanned becomes public.
- **Residual:** an `approved` row and an `approved` moderation audit row
  (post_create.go:592-594, RecordVideoModeration) can be written for media with
  no verdict; the gate is one layer instead of two, and has no test (grep: none).
- **Corrected severity:** P3. Defence in depth and audit accuracy; the
  read-time gates already stop publication.

## A-2: reel can stay pending forever

- **Verdict:** CONFIRMED (the stuck-pending part); the manual_review clause is wrong.
- **Evidence level:** VERIFIED-READ
- **Checked:** consumers/media.go:371-376 logs a flip error and returns;
  `handle` goes on and returns nil (:110, :240). Lookup errors are also
  swallowed (:366-369: any `GetVideoMetadataByMediaAsset` error is treated as
  "no post"). Create holds `pending` on any media-service error
  (post_review.go:162-166). The only writers of `review_status` from `pending`
  are the consumer (media.go:327, :371), `FlipReviewStatusFromPending`
  (store/postgres/posts.go:1540-1550) and the admin `ModeratePost`
  (moderation_authority.go:137-148, :219), which nobody is prompted to use: the
  admin queue lists `flagged` only (admin_content.go:164). No reconciler or
  sweeper touches pending posts (grep over internal/ and cmd/:
  claimsync, reconcile/counts.go, reconcile/restrictions.go do not read review status).
  The "manual_review has no resolver" clause does not hold for a ready asset
  (fact 1); for a failed asset the consumer rejects the post (media.go:356-357).
- **Corrected severity:** P1 stands. **Duplicate:** merge A-2 with C-1 and C-4
  (one root cause, three entry points).

## C-1: a post created after the asset is ready can stay pending forever

- **Verdict:** CONFIRMED
- **Evidence level:** VERIFIED-READ
- **Checked:** post_review.go:162-166 (any HTTP error -> pending) and
  :178-198 (non-200 -> error). The completion is consumed once per generation;
  finalizeReviewGate returns when no video_metadata row exists (media.go:366-369).
  The race is wider than the evidence says: the video_metadata row is inserted
  AFTER the post transaction commits (post_create.go:614-666), so a completion
  consumed between the gate read and that insert is lost too. And a failed
  `CreateVideoMetadata` is log-only (post_create.go:663-665), which leaves the
  post pending with no join row at all. Create does read `media_assets`
  directly for dimensions (`mediaMeta`, post_create.go:633-635), yet the gate
  uses the HTTP call.
- **Corrected severity:** P1 stands. Merge with A-2 and C-4.

## C-2: manual_review is a dead end; create turns ready+manual_review into approved

- **Verdict:** DISPROVED for the reel path; one sub-claim is factually wrong.
- **Evidence level:** VERIFIED-READ
- **Checked:** the cited worker lines (main.go:436-441, 995) do not produce
  `ready` + `manual_review` (fact 1): a scanner error or a throttle is retried,
  not turned into a verdict. "UpdateMediaModerationStatus has no caller" is false:
  internal/service/upload.go:575 calls it (image path). Moving an asset out of
  manual_review is possible: the internal reprocess route
  (internal/http/handler.go:216, reprocess_handler.go:32) re-runs the scan and
  `CompleteTranscode` rewrites `moderation_status` (store/postgres/transcode_inbox.go:210-218).
  No code calls it, so it is an operator curl only. "Approved but unplayable"
  needs the unreachable state.
- **What remains true:** `failed` + `manual_review` assets have no reviewer UI;
  the post is rejected (C-5, not in scope here).
- **Corrected severity:** P3 (remove the dead branch or make it fail closed in
  the create gate). Not P1.

## C-3: media.events.long not provisioned in MSK (same as reels-E-1)

- **Verdict:** CONFIRMED
- **Evidence level:** VERIFIED-READ
- **Checked:** infra/terraform/modules/msk/topics-job/variables.tf:29-59
  (27 topics, no `media.events.long`); modules/msk/main.tf:147
  `auto.create.topics.enable=false`; envs/prod/main.tf:337-348 passes no
  `topics`, so the default list applies. The topic name is hard-coded,
  `intakeTopic + ".long"` (media-service/cmd/worker/transcode_classes.go:66-71).
  No `CreateTopics`/`AllowAutoTopicCreation` anywhere in media-service or shared
  (grep). The writer is a plain `kafka.NewWriter` (main.go:230-238). On a write
  error the request is transcoded in the short loop (transcode_classes.go:213-220).
  The short consumer handles one message at a time (:229-255). prod sets
  `MEDIA_TRANSCODE_CLASSES: "short,long"` (deploy/services/media-service/values-prod.yaml:176),
  and its own comment says the topic must exist first (:174).
- **Note:** each long request also pays the writer's own retry and timeout
  before it falls back.
- **Corrected severity:** P1 stands (reels wait hours behind a long video in
  prod). **Duplicate:** merge with reels-E-1.

## C-4: consumer drops review-gate errors and commits

- **Verdict:** CONFIRMED
- **Evidence level:** VERIFIED-READ
- **Checked:** consumers/media.go:353-398: `finalizeReviewGate` has no error
  return. A flip error (:371-376) and the audit insert (:384-392) are log-only.
  The lookup error at :366-369 is also dropped silently, a case the finding
  missed. `handle` calls it at :110 and returns nil at :240 when nothing later
  fails. Only a later failure (UpdateVideoMetadata :170-172, title review
  :227-235) makes the shared consumer redeliver, and that redelivery re-runs
  the gate too. The flip itself is idempotent (posts.go:1543-1544), so returning
  the error would be safe.
- **Corrected severity:** P1 stands. Merge with A-2 and C-1.

## reels-E-1: media.events.long not in MSK topics

- **Verdict:** CONFIRMED. Same evidence as C-3; also checked the long loop
  (main.go:219-252: a reader on the missing topic, and `consume` logs fetch errors
  and loops, transcode_classes.go:231-238).
- **Evidence level:** VERIFIED-READ
- **Corrected severity:** P1. **Duplicate of C-3: keep one.**

## reels-E-3: 50 Gi worker scratch on a ~20 GiB node disk

- **Verdict:** DOWNGRADE-TO-SUSPECTED. The repo facts hold. The node disk size
  cannot be proven from the repo, and the failure mode is partly different.
- **Evidence level:** SUSPECTED (node disk); VERIFIED-READ (the rest)
- **Checked:** values-prod.yaml:155 (2 replicas), :166 (`scratchSizeLimit: 50Gi`);
  charts/atpost-service/templates/worker-deployment.yaml:80-82 (emptyDir sizeLimit);
  charts/atpost-service/values.yaml:249 `nodeSelector: {}`; no
  `MEDIA_WORKER_MAX_ORIGINAL_BYTES` in prod (qa sets it, values-qa.yaml:126),
  so the 16 GiB default applies (internal/processing/original_cap.go:18);
  headroom 3x (transcode_guards.go:63); infra/terraform/modules/eks/main.tf:15-16
  (module `~> 20.29`), :77 `disk_size = 100`, with no `use_custom_launch_template`
  or `block_device_mappings` anywhere in infra/terraform (grep). The claim that
  v20 ignores `disk_size` when it uses its default custom launch template
  (which leaves the AMI's 20 GiB root) matches how the upstream module behaves,
  but the module source is not in the repo and no plan was run, so this is
  not verified here.
- **Correction to the mechanism:** if statfs sees a 20 GiB node filesystem,
  the guard (transcode_guards.go:248-255) refuses any original over about 6.6 GiB
  permanently ("cannot fit the scratch volume"), so large uploads fail instead
  of evicting the pod. Eviction is still possible when two concurrent jobs
  (short + long loops, main.go:219-252) each pass the free-space check and
  together fill the node disk (kubelet node-pressure eviction).
- **Corrected severity:** P2 until `terraform plan` shows the launch template's
  root volume. If it is 20 GiB, P1.

## reels-E-4: Rekognition outage re-encodes up to 32 times

- **Verdict:** CONFIRMED
- **Evidence level:** VERIFIED-READ
- **Checked:** the scan runs after all encodes and uploads (main.go:984-1012).
  `classifyScanError` (:1031-1037) treats every provider error except an input
  refusal as transient. `retryUntilDurable` (transcode_guards.go:130-167) re-runs
  the whole `processMessage`. `defaultMaxAttempts = 8` (:60), backoff 2 s
  doubling to 2 min (main.go:561-564). The stall policy is 10 min stale and
  `MaxAttempts: 3` (store/postgres/transcode_lease.go:69-76). That gives 1 + 3
  re-queues x 8 = 32 runs. Nothing reuses renditions across attempts (no
  skip-if-exists in runTranscode, grep), and the scanner has no breaker (grep).
  The give-up writes failed + manual_review (transcode_lease.go:446).
- **Corrected severity:** P2 (availability and cost during a provider outage;
  nothing unscanned is published).

## F-2: finalizeReviewGate has no behaviour test

- **Verdict:** CONFIRMED
- **Evidence level:** VERIFIED-RUN
- **Checked:** `go test -count=1 -list '.*' ./internal/consumers/` in
  post-service lists 7 tests, none for the transcode gate. The only media.go test,
  `TestReviewGateReleaseReadsAttachmentsOnly` (media_cover_guard_test.go:15), reads
  the source with `os.ReadFile` and checks strings. It covers the voice release,
  not `finalizeReviewGate`. Grep for `finalizeReviewGate`/`MediaTranscodeConsumer`
  in `*_test.go`: none. `FlipReviewStatusFromPending` appears once, in
  store/postgres/post_restrictions_integration_test.go:530.
- **Corrected severity:** P2. A regression that approves `manual_review` would
  still be caught at read time by the tested `mediaPublishable` and
  `EvaluateMediaAccessFacts` rules. The real exposure is a regression that never
  releases reels (every reel stays invisible) with green CI.

---

## Summary

| ID | Verdict | Evidence | Severity (corrected) | Merge |
|---|---|---|---|---|
| A-1 | DISPROVED (residual hardening) | VERIFIED-READ | P3 | none |
| A-2 | CONFIRMED (manual_review clause wrong) | VERIFIED-READ | P1 | with C-1, C-4 |
| C-1 | CONFIRMED (race wider: metadata insert after commit, log-only) | VERIFIED-READ | P1 | with A-2, C-4 |
| C-2 | DISPROVED (ready+manual_review unreachable; setter has a caller; reprocess exists) | VERIFIED-READ | P3 | none |
| C-3 | CONFIRMED | VERIFIED-READ | P1 | = reels-E-1 |
| C-4 | CONFIRMED (also drops lookup errors) | VERIFIED-READ | P1 | with A-2, C-1 |
| reels-E-1 | CONFIRMED | VERIFIED-READ | P1 | = C-3 |
| reels-E-3 | DOWNGRADE-TO-SUSPECTED | SUSPECTED | P2 (P1 if plan shows 20 GiB) | none |
| reels-E-4 | CONFIRMED | VERIFIED-READ | P2 | none |
| F-2 | CONFIRMED | VERIFIED-RUN | P2 | none |
