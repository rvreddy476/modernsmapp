# Reels: feature status

- **Audit date:** 2026-11-17. Code: `codex/module-01-02-launch-safety` @ `58f602b6d` (HEAD~1 `770f5ed9d`; dirty working tree, read as it is). Web: `C:\workspace\wt\pb-echo`, `feat/echo-web` @ `ca96921`.
- **Method:** read-only. Statuses come from the six Stage 2 tracks and the four Stage 3 verifiers (`verify/v1…v4`). No feature was exercised against a running stack, a device or a browser. "COMPLETE" means the code path exists end to end on that surface and no defect was found in it; it does not mean it was seen working.
- **Statuses:** COMPLETE, PARTIAL (works with a known gap), BROKEN (does not work as built), MISSING (not built), UNVERIFIED (could not be judged from the repository), n/a (not applicable on that surface by design).
- **Evidence:** `reels-N` points to `03-findings.md`. Track and verifier names point to files in `tracks/` and `verify/`. Path conventions as in `03-findings.md`.

## Features

| # | Feature | Server | Android | Web | Evidence |
|---|---|---|---|---|---|
| 1 | Record a reel | n/a | PARTIAL | MISSING (by design: web is upload-only) | Banuba camera caps clips at 10 min; the fallback system camera has no cap (reels-36). Track A |
| 2 | Pick from gallery | n/a | COMPLETE | COMPLETE | Track A; Shorts ≤ 10 min enforced in post-service (baseline row 5) |
| 3 | Upload (resumable) | COMPLETE by read, untested | COMPLETE | PARTIAL | Server `media-service/internal/service/resumable.go` has no test (reels-13); late chunk can reopen a session (reels-93); parts pass through the API tier (reels-55). Android resume tested against a fake (`ResumableUploaderTest.kt`). Web cannot resume after reload (reels-39) |
| 4 | Processing (transcode, scan) | PARTIAL | n/a | n/a | Long-video topic missing in prod (reels-3); two lanes, no autoscale (reels-4); unmonitored (reels-5); scanner outage re-encodes 32x (reels-34); scratch disk (reels-35); confirm and queue in two commits (reels-32); real-ffmpeg tests skip (reels-12). V4: transcode idempotency and stall sweeper PASS |
| 5 | Thumbnail / cover selection | PARTIAL | COMPLETE | COMPLETE | Frame picker runs ffmpeg on the original in the API pod with no rate limit (reels-24). Web Hub frame picker per memory note; Track A |
| 6 | Captions (subtitles) | COMPLETE | PARTIAL | COMPLETE | Android cannot add captions (reels-83); plays them. Web requests captions at publish (`useUploadStudio.ts:584-593`) |
| 7 | Hashtags | PARTIAL | COMPLETE | PARTIAL | Trending counts private and pending tags (reels-19); trending query cost (reels-54). Web hashtag Reels tab asks for `flick` only (reels-89) |
| 8 | Mentions | PARTIAL | COMPLETE | COMPLETE | Mention notices sent while pending and kept after takedown (reels-74) |
| 9 | Audio (added and original sounds) | PARTIAL | COMPLETE | COMPLETE | Audio path through un-hardened ffmpeg (reels-16); sound use counts inflatable (reels-33). Android added-sound overlap tested (`ReelSoundPlayerTest.kt:436`) |
| 10 | Draft | COMPLETE | MISSING | PARTIAL | Android lists drafts only (reels-37). Web draft publish can double-post (reels-40). V4: scheduled and draft publishing PASS on the server; scheduled retry for ever (reels-98) |
| 11 | Publish | PARTIAL | COMPLETE | PARTIAL | A reel can stay pending for ever (reels-1); transcode failure rejects for good (reels-31); create-gate residual (reels-71); no behaviour test of the release (reels-64). Web always says "published" (reels-75) |
| 12 | Edit | COMPLETE | COMPLETE | PARTIAL | Web Hub edit leaves reels caches stale (reels-90). Author can clear AI labels after publish (reels-72, policy) |
| 13 | Delete (and restore) | COMPLETE | COMPLETE | COMPLETE | V4: delete during processing PASS. Issued URLs keep playing ~35 min; versions kept 90 days (reels-17). Android offline copy survives delete (reels-85); multipart not aborted (reels-92) |
| 14 | Vertical player, swipe | COMPLETE | COMPLETE | PARTIAL | Web: no cross-page de-dup (reels-91), 0.22 s audio overlap suspected (reels-82). No single-player test on either client (reels-14) |
| 15 | Autoplay, pause, mute | n/a | PARTIAL | COMPLETE | Android: a page append un-pauses a paused reel (reels-100) |
| 16 | Preload of next items | n/a | COMPLETE | MISSING | Android preloads both neighbours (`ReelsScreen.kt:1126-1128`), ignores data saver (reels-49). Web mounts only the active reel (reels-46) |
| 17 | Adaptive streaming (HLS) | PARTIAL | COMPLETE (refresh on slow network UNVERIFIED) | PARTIAL, suspected BROKEN in prod | Portrait renditions ~404x720 (reels-45); playlist cost (reels-47); playback depends on post-service (reels-61). Android cache key (reels-48); failure UI (reels-50). Web credentials vs CloudFront (reels-2); fatal-error loop (reels-50) |
| 18 | Like | PARTIAL | COMPLETE | PARTIAL | Engagement skips review and schedule gates (reels-18). Web failures silent (reels-88) |
| 19 | Comment | PARTIAL | PARTIAL | PARTIAL | Same gate gap (reels-18). Android owner has no creator menu on the top reel (reels-86). Web: reels-88 |
| 20 | Share | COMPLETE | COMPLETE | PARTIAL | Web counts a share when the sheet opens (reels-88) |
| 21 | Save | COMPLETE | COMPLETE | PARTIAL | Web shows "Saved" before the result (reels-88) |
| 22 | Follow | COMPLETE | COMPLETE | PARTIAL | Web Follow badge wrong past ~100 authors (reels-44) |
| 23 | View tracking | PARTIAL | COMPLETE with defects | PARTIAL | View inflation intra-day (reels-28); analytics views readable by anyone (reels-7); ingest cost (reels-52); analytics never erased (reels-8). Android: no `ended` reason (reels-41), phantom view session (reels-87). Web: background heartbeats (reels-42), loop_count 0 (reels-43), play_end lost on unload (reels-51). V4: view rule PASS for paid views |
| 24 | Watch-time tracking | BROKEN for monetization | COMPLETE with defects | PARTIAL | Watch time client-controlled and feeds Creator Fund (reels-27; V4 FAIL). Client defects as row 23 |
| 25 | Discovery (reels feed, categories) | PARTIAL | PARTIAL | PARTIAL | Feed over-reads the timeline (reels-53). V4: discovery partial on both clients. Search index COMPLETE (V4) |
| 26 | Trending | PARTIAL | PARTIAL | PARTIAL | Week-scan query (reels-54); private tags counted (reels-19) |
| 27 | Notifications for new reels | BROKEN | n/a | n/a | Dropped for pending and followers-only uploads (reels-30) |
| 28 | Reporting (UI and intake) | COMPLETE | COMPLETE | COMPLETE | V4: reporting UI complete on both. Errors generic, `details` unbounded (reels-80) |
| 29 | Moderation (report to takedown) | PARTIAL | n/a | n/a | Resolving a report does nothing to the reel (reels-29); moderation queue lists flagged only (reels-1); no end-to-end test (reels-68) |
| 30 | Creator feedback after hold or reject | PARTIAL | PARTIAL | PARTIAL | reels-75 |
| 31 | Creator controls: comments off, download off, audience | COMPLETE | COMPLETE | COMPLETE | Track A; media gate fails closed (V4 PASS) |
| 32 | AI-generated label | COMPLETE | MISSING | COMPLETE | Android neither sets nor shows it (reels-38) |
| 33 | Access control on media | PARTIAL | n/a | n/a | Bare X-User-Id trusted in-cluster (reels-6); HS256 accepted in prod (reels-15; V4 gate 12 FAIL); shared internal key on destructive routes (reels-25); slug oracle (reels-79) |

## Verifier PASS and FAIL checks used as evidence (V4 Part 2)

| Check | Result | Where it lands |
|---|---|---|
| Secrets in the tracked tree | PASS (grep-based); git history and images UNVERIFIED (no gitleaks or trufflehog) | Out-of-scope note in 03 |
| Gate 12: production config refuses weak auth | FAIL | reels-15 |
| Transcode idempotency (inbox, lease) | PASS | Row 4 |
| Stall sweeper requeues stuck jobs | PASS (except the pre-heartbeat case) | Row 4; reels-5 |
| Delete during processing | PASS by read | Row 13; journey 5 |
| Scheduled and draft publishing | PASS on the server | Row 10 |
| Media gate fails closed when post-service errors | PASS (no degraded mode, reels-61) | Rows 17, 31 |
| View rule for paid views (per-viewer daily cap) | PASS | Row 23 |
| Watch-time rule | FAIL | reels-27 |

## Journeys

No journey was executed end to end. The audit had no running environment: repository rules forbid local databases and devices, the founder's handset was not used, and no deployed environment was driven. Each result below is what the code says would happen, and what a real run needs.

| # | Journey | Result | Expected outcome from the code | What a real run needs |
|---|---|---|---|---|
| 1 | Upload, process, publish, play on a second account | NOT RUN; expected to work on Android and dev, with known failure modes | Works when the transcode completes before the create and the consumer's review flip succeeds. Can stay pending for ever (reels-1). On prod web, HLS likely fails CORS (reels-2). Prod long videos block reels (reels-3) | QA or staging stack with CloudFront, media-worker with ffmpeg, Kafka, post-service; two test accounts; Android build and Chrome + Safari. Automate as the reels e2e flow (reels-10) |
| 2 | Interrupt an upload and resume it | NOT RUN; expected PASS on Android, FAIL on web after reload | Android resumes from stored parts (unit-tested). Web restarts from 0 % after reload (reels-39). Server half untested (reels-13) | QA stack; a large (≥ 500 MB) file; network cut mid-upload on Android; tab reload on web; pod restart during chunks (reels-60) |
| 3 | Invalid, oversized, over-long, disguised files rejected cleanly | NOT RUN; expected PASS by read for sniffing and limits, untested with real files | Header sniff and size limits refuse at confirm; ffprobe limits refuse in the worker. Real-file cases never tested (reels-66, reels-12). Audio path un-hardened (reels-16). Android fallback camera has no 10-min cap (reels-36) | QA stack with the worker; fixtures: text renamed .mp4, AVI renamed .mp4, audio-only MP4, > 10 min clip, file over the size cap; check each reason code and that no post is created |
| 4 | Swipe 50 reels: no repeats, stalls, audio overlap | NOT RUN; Android expected PASS by read, web PARTIAL | Android: one active player, neighbours preloaded (no test, reels-14). Web: no preload (reels-46), possible repeats across pages (reels-91), 0.22 s overlap suspected (reels-82), follow badge wrong past ~100 authors (reels-44) | A seeded feed of ≥ 60 distinct reels on QA; Android device or emulator started by the founder; Chrome with network throttling; record repeats, start-up time per swipe, overlapping audio and memory |
| 5 | Delete a reel during processing | NOT RUN; expected PASS by read (V4) | Worker sees the asset gone and skips; post soft-deleted. Late segments can leak (reels-97); multipart not aborted (reels-92) | QA stack; a long-enough upload to delete mid-transcode; check worker logs, storage prefix and the post afterwards |
| 6 | Report a reel and follow it through moderation | NOT RUN; expected FAIL at the takedown step | Report is stored and listed; resolving it does not touch the reel (reels-29); takedown needs a separate admin action; creator not told (reels-75) | QA stack with trust-safety, admin console and an admin account; file a report, resolve it, check feed, search and media access as a stranger. Automate per reels-68 |
