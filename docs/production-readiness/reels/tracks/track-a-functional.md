# Track A: functional completeness (reels)

- Audit date: 2026-11-17. Read-only. Backend and Android: `C:\workspace\modernsmapp` (working tree, branch `codex/module-01-02-launch-safety`). Web: `C:\workspace\wt\pb-echo` (`feat/echo-web`).
- Path prefixes: backend paths are relative to `Architecture/services/`; Android paths to `mobile/android/`; web paths to `C:\workspace\wt\pb-echo\`.
- Nothing was run against a live stack, a device or a browser. Runtime behaviour is VERIFIED-READ at best. The only thing executed: `go test ./internal/consumers ./internal/service -run 'TestReclassifyDecision|TestResolveVideoContentType|TestCreateAndPatchShareTheShortsCap|TestShortsBoundaryIsTenMinutes|TestGateVideo' -count=1` in `post-service` → `ok …/internal/consumers 0.350s`, `ok …/internal/service 0.416s`. (No test named `TestGateVideo*` exists; the gate in A-1 has no test at all.)
- Coverage caveat: this track was cut short. The two client deep-reads were meant to run as delegated passes and did not complete. Client rows below rest on targeted reads of the files cited; rows marked UNVERIFIED were not followed end to end. The lead should assign a second pass for the UNVERIFIED rows before relying on this table for any PASS.

## 1. Feature status

| Feature | Server | Android | Web | Evidence |
|---|---|---|---|---|
| Record | n/a (a clip is a file) | PARTIAL: Banuba camera capped at 10 min; the system-camera fallback has no cap (A-4) | MISSING by design (file pick only; web is upload-only) | Android `feature/post/…/createhub/ReelSurface.kt:248-254` (`ActivityResultContracts.CaptureVideo()`, no duration extra), `banuba/BanubaKoinModule.kt:61-62`, `core/media/…/publish/ReelLength.kt:26,36` |
| Pick | COMPLETE (MIME allow-list, size caps, magic check at confirm, container sniff + ffprobe in the worker: map §4 steps 2a-6a) | COMPLETE (over 10 min is offered as a long video, `VideoGate.kt:60-66`) | COMPLETE: MP4/WebM/MOV by `file.type`, non-empty, unreadable duration refused, no size or length cap (`src/features/upload/fileRules.ts:27-38`); over 10 min shows a notice and is posted as long video (`:45-48`) | as cited |
| Upload (resumable) | COMPLETE (map §4 step 3) | COMPLETE: session checkpointed in `ReelPublishStore`, resumed via `GET …/resumable/:id`, expired session discarded and re-initialised (`core/media/…/upload/ResumableUploader.kt:286-295,377`; `createhub/ReelPublishPipeline.kt:160-210`; worker restart-safe `ReelPublishWorker.kt:50-56`) | PARTIAL: per-chunk retry with backoff inside the tab (`src/lib/resumableUpload.ts:126-134`); nothing persisted, no status call, so a reload or closed tab restarts the whole upload (A-6) | as cited |
| Processing | COMPLETE for transcode (outbox, inbox, leases, retries, quarantine, stall sweeper: map §4 steps 4-8); PARTIAL for the publish gate (A-1, A-2) | PARTIAL: post created at confirm, shown to author as processing (`ReelPublishPipeline.kt:44-62`); what the author sees when the post is later rejected was not traced (UNVERIFIED) | PARTIAL: uploads at Publish then creates at once (`useUploadStudio.ts:557-623`); later failure/rejection display UNVERIFIED | as cited |
| Cover selection | COMPLETE (cover media id on create) | COMPLETE: frame picker + gallery image, cover upload fails the post rather than posting without it (`ReelSurface.kt:407-420,563-572`; `ReelPublishPipeline.kt:231-241`) | COMPLETE: client-side frame capture or custom JPEG/PNG/WebP ≤10 MB, uploaded once per attempt, sent as `cover_media_id` (`useUploadStudio.ts:442-473,569-582,617`) | as cited |
| Captions / subtitles | routes exist (`media-service/internal/http/clips_handler.go:63-76`) | PARTIAL: plays caption tracks (`core/media/…/PlayerCaptions.kt`); cannot upload or request captions at create (no `v1/subtitles` call anywhere under `src/main`) (A-9) | COMPLETE for upload at publish, failure non-fatal (`useUploadStudio.ts:584-593`) | as cited |
| Hashtags | UNVERIFIED (parsing/indexing not traced) | chips field (`ReelSurface.kt:124-128` doc); render tap target UNVERIFIED | merged chips + caption (`useUploadStudio.ts:485,610`); render tap target UNVERIFIED | — |
| Mentions | UNVERIFIED | "people picked" (`ReelSurface.kt:127`); UNVERIFIED end to end | UNVERIFIED | — |
| Audio: sounds / original sound | routes exist, audience gated on the source video (memory; not re-traced) | COMPLETE for playback: `ReelSoundPlayer` attached to the settled page's player before play, detached otherwise (`feature/feed/…/ui/reels/ReelsScreen.kt:1117-1121`); "Use this sound" and Library sheet at create (`ReelSurface.kt:137-146`) | COMPLETE for playback: hidden looped `<audio>` synced by the player (`features/reels/components/ReelVideo.tsx:131-171,658`) | as cited. The 2026-09-30 memory note "Android plays no added sounds yet" is out of date |
| Audio tracks (dubs) | COMPLETE for list/upload; generation 503 with no backend | UNVERIFIED (client declared `AudioTracksApi.kt:37-57`) | wired as `sourceOverride` MP4 (`ReelVideo.tsx:322-326`) | as cited |
| Draft | COMPLETE (`post-service/internal/http/drafts.go:20-25`, worker) | MISSING: read-only list in Creator Hub (`feature/tube/…/data/hub/CreatorHubApi.kt:17,38`); no create, edit, resume or publish (A-5) | COMPLETE save/resume/publish (`useUploadStudio.ts:477-547,597-608`); fallback hazard A-7 | as cited |
| Publish | COMPLETE (idempotent create, map §3.2) | COMPLETE: one stable creation key across worker retries (`ReelPublishPipeline.kt:38-42,256`) | COMPLETE: one idempotency key per Publish press (`useUploadStudio.ts:554-555,620`) | as cited |
| Schedule | COMPLETE (map §4 step 11) | COMPLETE per map §6.3 (not re-traced) | COMPLETE (`schedule_at` on draft publish, `useUploadStudio.ts:600-601`) | — |
| Edit after publish | COMPLETE (`PATCH /v1/posts/:id`) | per map §6.3 (owner More sheet); UNVERIFIED | Creator Hub only; UNVERIFIED | — |
| Delete / restore | COMPLETE (soft delete, 720 h restore) | discard of a queued publish deletes local files and the record (`ReelPublishWorker.kt:269-288`); server-side media of a discarded upload is left behind (A-10) | UNVERIFIED | as cited |
| Vertical player, swipe, autoplay, pause, mute | n/a | COMPLETE: playback keyed to the settled page (one start per fling), `playOnly` pauses every other player, paused on stop, resumed on return (`ReelsScreen.kt:1103-1144`; `core/media/…/PlayerPool.kt:141-152`) | COMPLETE with caveat: only the active reel is mounted (`ReelsScreen.tsx:818-822`); muted-autoplay fallback (`ReelVideo.tsx:29-36,432-434`); exit animation can overlap audio (A-8) | as cited |
| Preload next items | n/a | COMPLETE: one neighbour each side preloaded paused (`ReelsScreen.kt:1126-1128`), pool of 4 (`PlayerPool.kt:183`), 256 MiB disk cache (`:256`) | MISSING (A-3) | as cited |
| Adaptive streaming | COMPLETE (HLS master + rungs to 720p, map §4) | HLS via `Playback.kt`/`HlsRefresh.kt` per map; not re-traced | COMPLETE: hls.js first, ABR, fatal network/manifest error falls back to the progressive file, media error recovered (`ReelVideo.tsx:333-373`); `capLevelToPlayerSize:false` (`:350`) fetches up to 720p on small players | as cited |
| Like, comment, share, save, follow | COMPLETE; `no_comments` enforced on comment and reply (`post-service/internal/service/post_comments.go:155,277`); reels refuse repost (`post_reposts.go:64-69`) | per map §6.3; optimistic/rollback UNVERIFIED for reels | per map §6.3; UNVERIFIED for reels | as cited |
| View and watch-time tracking | COMPLETE for the rule: session-based ingest with server-known duration (`analytics-service/internal/service/ingest.go:332-384,441-442`), one paid view per viewer per video per day (`internal/aggregation/daily.go:23-34`); retired `POST /v1/reels/:id/view` answers gone | heartbeat 5 s (map) ; UNVERIFIED | `playback/telemetry.ts` (map); UNVERIFIED | as cited |
| Discovery and trending | routes exist (feed, search, hashtags, trending sounds) | UNVERIFIED | UNVERIFIED | map §3 |
| Reporting and moderation | PARTIAL: filing COMPLETE (validated reasons, reporter quota, partial unique index for duplicates, `trust-safety-service/internal/service/moderation.go:182-277`); `ReportFiled` publish is fire-and-forget (`:268-274`); takedown path to post-service exists (`post_moderation_http.go:154`) but was not followed | `ReportApi` (map); UNVERIFIED | `useReport.ts` (map); UNVERIFIED | as cited |
| Creator controls | COMPLETE for comments off; download off gates offline copies only (`post-service/internal/http/offline_copies.go:15-48`) | PARTIAL: comments, hide share, allow download, remix, audience (`ReelSurface.kt:994-1033,619`); no AI-label control (A-2b) | COMPLETE incl. AI label (`features/upload/studioApi.ts:35-55`) | as cited |

## 2. Founder rules

| Rule | Holds? | Evidence |
|---|---|---|
| Shorts ≤ 10 min, longer becomes long video | HOLDS on the server, VERIFIED-RUN for the decision functions: create (`post_content_types.go:111-117`), PATCH override (`:134-143`), and the transcode consumer once measured (`consumers/media.go:196-219,261-264`). Caveat: the consumer reclassifies only on a `ready` completion that carries a URL and has a `video_metadata` row (`:115-133`), and duration is an integer second, so a 600.9 s clip stays a Short | tests listed at the top |
| Camera max 10 min per clip | FAILS on the Android fallback camera (A-4) | `ReelSurface.kt:248-254` |
| No 500 MB cap on video | HOLDS: video simple PUT ≤ 5 GiB then resumable; 500 MB applies to non-video only | `media-service/internal/service/validation.go:14-25`; web `fileRules.ts:11-17` |
| Shorts studio has 3 steps, no kids/age/altered steps | HOLDS on web: `reel`/`short` = video, details, publish (`src/features/upload/tokens.ts:20-25`). The create body still sends `is_made_for_kids` / `altered_content` from form defaults (`studioApi.ts:42-44`, `useUploadStudio.ts:503-505`); no step asks for them. Android has no step list; flow is source → studio → form (`ReelSurface.kt:196-223`), no kids/age/altered field found | as cited |
| "Keep a copy" in-app only; download route owner-only | HOLDS on the server: `downloadVerdict` allows the uploader only, anonymous never (`media-service/internal/service/download.go:61-78`). Web shows no Save offline on the reels More menu by founder decision (`features/video-shell/moreRows.ts:126`); the reel model's `downloadUrl` is only `/serve` and nothing in `features/reels` renders it (`features/reels/model.ts:179,369`) | as cited |
| Reels never in the home feed | HOLDS on the server (feed track A: `feed-service/internal/service/feed.go:541-552`). Web still sends `publish_to_feed: true` for a Short (`types.ts:185`, `useUploadStudio.ts:502,619`); harmless to the home read but a dead, misleading field (P3, not raised) | as cited |
| Public videos play signed out | HOLDS on web through `/reels/<uuid>` → `/post/<id>` (map §5.7); Android has no signed-out mode at all | map §5.7 |
| AI label shown to viewers | HOLDS on web (`features/reels/components/ReelOverlay.tsx:74`, `model.ts:160-161,361`); FAILS on Android reels (A-2) | as cited |
| Android has every web feature | FAILS: see section 4 | — |

## 3. Findings

```
ID:            A-1
Title:         A reel created after its media finished transcoding is approved and made public whenever the media's moderation status is anything but "rejected", including "manual_review" (no scan verdict) and "pending".
Severity:      P1
Category:      functional
Evidence level: VERIFIED-READ for the logic; SUSPECTED for how often a create lands after completion with a non-"passed" verdict (confirm on dev: an asset with processing_status='ready', moderation_status='manual_review', then POST /v1/posts with it)
Evidence:      post-service/internal/service/post_review.go:161-174 (procStatus ready → returns "approved" unless modStatus == "rejected"); called from post_create.go:494-495. Compare the voice gate in the same file, :142-150, which approves only "approved" and holds everything else, and the consumer, consumers/media.go:355-363, which approves only passed/approved and leaves manual_review pending. The worker writes manual_review when there is no verdict (media-service/cmd/worker/main.go:433-441). Paths that create after completion: Android fallback create after ready (createhub/ReelPublishPipeline.kt:258-260), reel draft publish, any short clip whose transcode beats the create. No test covers gateVideoReviewStatus (grep in *_test.go: none).
How to reproduce: Seed a media asset ready + manual_review owned by U; as U POST /v1/posts {content_type:"flick", media_ids:[it], visibility:"public"}; the row is created review_status='approved' and plays to anyone.
Impact:        Unscanned video is published, the exact case the worker's "no default passed" rule exists to prevent.
Fix:           In gateVideoReviewStatus return "approved" only for modStatus "passed" or "approved"; "rejected" → "rejected"; anything else → "pending".
Acceptance:    Table test over (ready, passed|approved|rejected|manual_review|pending|"") with an httptest media-service; manual_review and pending must give "pending". Fails today.
Effort:        S
```

```
ID:            A-2
Title:         A reel can be left in review_status 'pending' forever, invisible, with no recovery and no message: the transcode consumer swallows a failed flip, a create that misses the completion event has nothing to flip it later, and manual_review has no resolver.
Severity:      P1
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      consumers/media.go:371-375 (FlipReviewStatusFromPending error → log and return; handle() then continues and returns nil at :240, so the offset is committed and the event is never retried); post_review.go:163-166 (media-service error at create → "pending"); the only flips from pending are the consumer (media.go:327,371) and admin/reviewer routes; grep for a pending-video reconciler in post-service (`reconcile.*(pending|review)`, `pending.*sweep`) finds only the draft reclaimer (store/postgres/drafts.go:404). The completion event fires once per generation, so a post created after it (Android fallback, draft publish) whose media-service call failed is never revisited. manual_review: consumer returns without action (media.go:362-363).
How to reproduce: Make media-service return 503 for GET /v1/media/:id while U creates a flick whose media is already ready → post pending; no further event arrives. Or fail the UPDATE in FlipReviewStatusFromPending once.
Impact:        Creator's reel never goes live and nobody is told; support cannot see why.
Fix:           (1) Return the flip error from finalizeReviewGate so the consumer retries / DLQs it. (2) A periodic reconciler (the post-service already runs several) that re-reads media status for video posts pending longer than N minutes and applies the same rule as A-1. (3) Route manual_review into the existing admin review queue.
Acceptance:    Consumer test: store flip returns an error → handle returns an error. Reconciler test: pending post + ready/passed media → approved after one tick.
Effort:        M
```

```
ID:            A-3
Title:         The web reels player does not preload the next reel; every swipe starts a cold HLS load (manifest, rung playlist, first segment) for the newly mounted player.
Severity:      P2
Category:      performance
Evidence level: VERIFIED-READ
Evidence:      web src/features/reels/components/ReelsScreen.tsx:796-839 (only `active` is rendered as a ReelVideo); ReelVideo.tsx:348-373 (hls.js instance created on mount). Android preloads both neighbours (ReelsScreen.kt:1126-1128).
How to reproduce: On /reels, throttle to Fast 3G, swipe: each reel shows its poster and spinner before the first frame.
Impact:        Visible start delay on every swipe on the web; the harness requires preload of the next items.
Fix:           Mount the next reel's ReelVideo off-screen, paused, with the existing `maxBufferLength: active ? 20 : 6` (ReelVideo.tsx:349) already written for that case, or at least prefetch its master playlist.
Acceptance:    Component test: with reels [a,b], index 0 renders two ReelVideo elements, only one with active=true.
Effort:        S
```

```
ID:            A-4
Title:         On Android without a Banuba licence the "Record" button opens the system camera with no duration limit, so the founder's 10-minute-per-clip camera rule is not enforced.
Severity:      P2
Category:      functional
Evidence level: VERIFIED-READ
Evidence:      createhub/ReelSurface.kt:248-254 (ActivityResultContracts.CaptureVideo() launched with only an output URI); :208 (fallback used whenever Banuba is not Ready); the cap exists only for Banuba (banuba/BanubaKoinModule.kt:61-62, core/media/…/publish/ReelLength.kt:36). Builds without .secrets/banuba.token have no licence (map §5.6).
How to reproduce: Build without a Banuba token, Create → Reel → Record, record 12 minutes: accepted, then offered as a long video by VideoGate.
Impact:        Founder rule broken in the default build; long camera clips go to upload.
Fix:           Use a custom contract that puts MediaStore.EXTRA_DURATION_LIMIT = CAMERA_MAX_RECORDING_MS/1000 on the capture intent (camera apps honour it best effort), and reject a returned clip over the cap in VideoGate when its source was the camera.
Acceptance:    Unit test on the intent builder: EXTRA_DURATION_LIMIT == 600.
Effort:        S
```

```
ID:            A-5
Title:         Android cannot save, resume, edit or publish a reel draft; the web can, so a draft started on the web is stuck there and leaving the Android composer loses the work.
Severity:      P2
Category:      functional
Evidence level: VERIFIED-READ (Android has only GET v1/reels/drafts: feature/tube/…/data/hub/CreatorHubApi.kt:17,38; grep for "reels/drafts" under mobile/android finds no other call)
Evidence:      server drafts routes post-service/internal/http/drafts.go:20-25; web save/publish useUploadStudio.ts:477-547,597-608.
How to reproduce: Save a draft on web; open Creator Hub drafts on Android: listed, cannot be opened into the composer or published.
Impact:        Founder rule "Android has every web feature" broken for drafts.
Fix:           Add POST/PATCH/DELETE drafts and POST drafts/:id/publish to the Android API and a "Save draft" action in ReelSurface plus "Continue" on the hub row.
Acceptance:    Contract test against the server fixture; ViewModel test that Save draft calls POST then PATCH.
Effort:        M
```

```
ID:            A-2b (lead: renumber)
Title:         Android reels neither let the creator set the AI-generated label nor show it to viewers, while web does both.
Severity:      P2
Category:      functional
Evidence level: VERIFIED-READ
Evidence:      Android: no aiGenerated/AI control in createhub (grep "aiGenerated|ai_generated" under feature/post/src/main matches only studio/StudioScreen.kt:1145, StudioPostRequest.kt:60, PostDetailsState.kt — the photo/post studio); the reels player never reads disclosures (grep "disclos" under feature/feed/…/ui/reels: none; the labels are drawn only on the home stage, FeedStage.kt:340). The DTO already decodes the field (core/feed/…/dto/FeedDtos.kt:120, core/model/…/FeedItem.kt:191-204). Web: set studioApi.ts:55, shown ReelOverlay.tsx:74.
How to reproduce: Publish a reel from web with "Made with AI"; view it in Android Reels: no label.
Impact:        Founder rule "AI label shown to viewers" fails on the super-app client; an Android creator cannot disclose AI content on a reel.
Fix:           Draw `item.disclosure.labels` in the reel overlay (the helper exists); add an "AI-generated" switch to SwitchesCard and send `ai_generated`.
Acceptance:    Compose test: a FeedItem with aiGenerated=true shows "AI-generated" on the reel page.
Effort:        S
```

```
ID:            A-6
Title:         A web upload cannot resume after a reload, a closed tab or a crash: the resumable session is held only in memory, so the whole file is sent again.
Severity:      P2
Category:      functional
Evidence level: VERIFIED-READ
Evidence:      src/lib/resumableUpload.ts:54-65 (init → chunks → complete in one call), :126-134 (retries only within the call); no localStorage/sessionStorage/IndexedDB and no GET /v1/media/upload/resumable/:id in the file. Android does persist and resume (ResumableUploader.kt:286-295).
How to reproduce: Upload a 2 GB short on web, reload at 60 %: the studio starts again from 0 % (and a second resumable session counts against the 10/hour upload budget).
Impact:        Harness journey "interrupt and resume" fails on web for anything but a brief network blip.
Fix:           Persist {uploadId, file name, size, lastModified} in localStorage; on re-pick of the same file call GET …/resumable/:id and send only missing parts.
Acceptance:    Unit test with a fake api: a stored session plus status {uploaded_parts:[1,2]} sends parts 3..n only.
Effort:        M
```

```
ID:            A-7
Title:         Web publish of a draft falls through to a direct create when the draft publish fails for any non-final reason, including a timeout after the server already published, which can post the reel twice.
Severity:      P2
Category:      data
Evidence level: SUSPECTED (whether the media claim refuses the second create was not traced; confirm by forcing a client timeout on POST /v1/reels/drafts/:id/publish on dev)
Evidence:      src/features/upload/useUploadStudio.ts:597-608 (catch → fall through unless isFinalPublishRefusal), :612-622 (createReel with a fresh idempotency key that the draft publish never carried).
How to reproduce: Throttle the network so publishDraft times out after the server commits; the studio then calls createReel for the same media.
Impact:        Duplicate reel, or a confusing error if the claim refuses it.
Fix:           On an unknown outcome, GET the draft and use its published post id instead of creating; fall through only on a definite 4xx that means "draft cannot publish".
Acceptance:    Hook test: publishDraft rejects with a network error and the draft reads back published → createReel is not called.
Effort:        S
```

```
ID:            A-8
Title:         On the web the outgoing reel stays mounted and playing for the 0.22 s exit animation, so two videos (and two added sounds) can be audible during a swipe.
Severity:      P3
Category:      functional
Evidence level: SUSPECTED (confirm by swiping two reels with sound in a browser)
Evidence:      ReelsScreen.tsx:796-803 (AnimatePresence mode="popLayout", exit transition 0.22 s) with ReelVideo rendered `active` (:818-821); ReelVideo stops only in its unmount cleanup (ReelVideo.tsx:377-385).
How to reproduce: Swipe quickly between two reels with sound on.
Impact:        Brief audio overlap; the harness asks for no audio overlap.
Fix:           Pause and mute the exiting ReelVideo at exit start (pass active={false} via the custom prop, or pause in onExitComplete's predecessor).
Acceptance:    Component test: after index changes, the previous video element's paused is true before the exit completes.
Effort:        S
```

```
ID:            A-9
Title:         Android cannot add captions to a reel (no subtitle upload or auto-caption request), which web offers at publish.
Severity:      P3
Category:      functional
Evidence level: VERIFIED-READ (grep "v1/subtitles" under mobile/android/*/src/main: no match)
Evidence:      web useUploadStudio.ts:584-593; server clips_handler.go:68-74.
How to reproduce: Look for a captions option in the Android reel form.
Impact:        Web-only feature (Android gap).
Fix:           Add POST /v1/subtitles/:mediaId/request after confirm, behind a switch.
Acceptance:    API contract test for the request.
Effort:        S
```

```
ID:            A-10
Title:         Discarding a reel on Android after its video was confirmed leaves the uploaded original and its transcodes in storage with no owner post; orphan GC only removes assets still in pending_upload.
Severity:      P3
Category:      data
Evidence level: SUSPECTED (the post-service "orphan draft media cleanup" job was not read; confirm whether it deletes confirmed, unclaimed media)
Evidence:      ReelPublishWorker.kt:269-288 (discard removes the record and local files, makes no DELETE /v1/media/:id call); media-service/internal/service/orphan_gc.go:38-43 (pending_upload older than 24 h only, per map §4).
How to reproduce: Post, then Discard while the tile shows Processing (fallback path) or before create.
Impact:        Storage and transcode cost for abandoned uploads.
Fix:           Call DELETE /v1/media/:confirmedVideoId on discard when no post id exists.
Acceptance:    Controller test: discard of a record with confirmedVideoId and no postId calls the delete API once.
Effort:        S
```

## 4. Android gaps (web has it, Android does not)

1. Reel drafts: save, resume, edit, publish (A-5).
2. AI-generated label: set at create and shown on the reel (A-2b).
3. Caption upload / auto-caption request at create (A-9).
4. Signed-out playback of a public reel (web `/reels/<uuid>` → `/post/<id>`; Android has no signed-out mode). Whether this counts as a gap is a founder call; noted, not raised.

Not a gap by decision: web shows no Save offline on the reels More menu (`moreRows.ts:126`); Android has offline copies.

## 5. Journeys (read, not run)

1. **Upload, process, publish, play on a second account.** Android: pick/record → studio → form → Post enqueues the worker → stash → upload (simple or resumable) → confirm → cover → create immediately with the creation key → post `pending` → worker transcodes, scans → consumer flips to `approved` → fan-out → second account's Reels feed → `/hls/master.m3u8` through the delivery gate. Web: same, with upload starting only at Publish. Breaks: A-1 (approved without a verdict when the create comes after completion), A-2 (stuck pending with no recovery). Playback on a second account: gate path per map §9.1, not re-traced here. UNVERIFIED end to end; the dev smoke (`scripts/smoke/dev-smoke.mjs`) covers it only for the author with a private flick.
2. **Interrupt and resume an upload.** Android: COMPLETE by read (checkpointed session, status call, expiry handled, worker restarts). Web: survives a short network drop only; reload restarts from zero (A-6).
3. **Invalid, oversized, over-long, disguised file.** Invalid MIME: refused by web `fileRules.ts:28-29` and server allow-list at init. Oversized: > 5 GiB simple → 413 use-resumable, > 16 GiB (prod default) refused at init and in the worker (map §4 step 2a). Over-long: there is no Shorts refusal by design; over 10 min becomes long video (§2); over 4 h fails the worker's limits guard → post rejected by the consumer (`media.go:356-357`); what the creator sees then is UNVERIFIED. Disguised: confirm's 64-byte magic check marks `rejected`, the worker's container sniff fails permanently (map §4 steps 2c, 6a). The web uploads only at Publish, so a confirm rejection surfaces as a publish error; its wording UNVERIFIED.
4. **Swipe 50 reels.** Android: one player plays (`PlayerPool.playOnly`), pool of 4 recycled players, neighbours preloaded, playback keyed to the settled page so a fling starts one playback, released on lifecycle (`ReelsScreen.kt:1841`); repeats across pages and memory over 50 items UNVERIFIED (FeedPager de-dup not read). Web: one mounted player, hls.js destroyed on unmount (`ReelVideo.tsx:377-385`), no preload (A-3), possible 0.22 s audio overlap (A-8); growth of the item array over 50 swipes not read.
5. **Delete a reel during processing.** Android before create: Discard stops the PUT and deletes local state; server media left behind (A-10). After create: `DELETE /v1/posts/:id` soft-deletes; the worker keeps transcoding (it skips only a deleted media asset, `worker/main.go:467-471`); the consumer may later flip a deleted post's review status, harmless because reads exclude `deleted_at`. Signed URLs already handed out stay valid until expiry (map open question 13; Track B).
6. **Report a reel and follow it through moderation.** Filing: validated, quota-limited, deduplicated by index, stored `open` (`moderation.go:182-249`); the `ReportFiled` event is best effort and lost on a Kafka error (`:268-274`) — matters only if something consumes it for auto-action (not traced). Moderator resolution → `POST /v1/posts/internal/moderation` exists (`post_moderation_http.go:154`); that a takedown stops manifest and segment playback was not followed. UNVERIFIED past filing.

## 6. Checked and found sound (re-verify before any PASS)

- Shorts cap at create, PATCH override and post-transcode reclassify share one function (`post_content_types.go:22-31`); unit tests pass (VERIFIED-RUN).
- Repost of a reel refused server-side (`post_reposts.go:64-69`).
- `no_comments` enforced on comment and reply creation (`post_comments.go:155,277`).
- Download route uploader-only, anonymous never (`download.go:61-78`).
- Paid view counted at most once per viewer per video per day by default (`aggregation/daily.go:23-34`, test `daily_test.go:36-37`).
- Worker never defaults moderation to passed (`worker/main.go:426-441,995-1013`).
- Consumer approves only on ready + passed/approved and rejects on failed or rejected (`consumers/media.go:355-363`).
- Android publish is restart-safe with one creation key, resumable session persistence and expiry handling (`ReelPublishPipeline.kt`, `ResumableUploader.kt:286-295`).
- Android single active player and lifecycle pause/resume (`PlayerPool.kt:141-152`, `ReelsScreen.kt:1103-1144`).
- Web 3-step Shorts studio (`tokens.ts:20-25`); web has no size or length cap for video (`fileRules.ts:11-38`).
- Web hls.js teardown on unmount and fallback to MP4 on fatal network error (`ReelVideo.tsx:362-385`).

## 7. Out of scope observations

- A feed-composer video (`content_type="post"`) is not run through the video publish gate at all (`post_create.go:494` uses `isVideoContentType`, which excludes "post", `post_content_types.go:170-176`), so an unscanned video in a home-feed post may be approved at create. Feed module; whether the media delivery gate blocks unscanned bytes independently was not checked.
- Map open question 3 (the `moderation_status` CHECK lacking `manual_review`) bears on A-1/A-2: if the write fails, the asset never completes. Track C should settle it.

## Not verified

- Every client row marked UNVERIFIED in section 1: hashtag and mention rendering and tap targets, edit after publish, discovery/search/trending screens, like/comment/share/save/follow optimistic paths on the reels surface, report UI, telemetry event timing and replay behaviour, Android HLS refresh. Needs a full read of `feature/feed/…/ui/reels/*`, `core/feed/…/data/*`, `core/analytics/…/VideoWatchTracker.kt`, web `features/reels/**` and `playback/telemetry.ts`.
- What the creator sees when a reel is rejected, held, or fails processing after create, on both clients.
- Report → moderator action → playback revoked at manifest and segment level (needs trust-safety admin flow plus media delivery gate read; Track B).
- Behaviour on a slow or dropping network beyond upload retries (needs a device or browser).
- Memory over 50 swipes and duplicate items across pages (needs FeedPager/ReelsViewModel read, ideally a device profile).
- Any running journey: no stack, device or browser was used.
