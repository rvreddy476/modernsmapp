| D | Notification inbox | Implemented, **not yet reviewed** — `prompt/slice-d-notifications-claude-implementation.md` |
# Launch backlog — tracked debt from closed slices

Last updated: 2026-08-23

Every item here was reviewed, judged **not** a launch blocker, and deliberately
deferred. This file exists so that deferral is a decision with a record rather
than something that quietly evaporates when a slice closes.

Nothing here reopens a closed slice. Items are picked up when they are
scheduled, not when they are noticed.

## Slice status

| Slice | Scope | Status |
|---|---|---|
| A | Social graph + engagement | Closed — `prompt/social-graph-engagement-codex-clb-final-closure.md` |
| B | Chat / realtime | Closed — `prompt/slice-b-chat-codex-final-closure-review.md` |
| C | Android composer (create post) | Closed — `prompt/slice-c-android-composer-codex-closure-verdict.md` |
| D | Notification inbox | Implemented, **not yet reviewed** — `prompt/slice-d-notifications-claude-implementation.md` |

---

## P0 — mandatory before public release

These are not slice debt. They gate the release itself.

### R-1. AWS staging smoke on a physical device

Outstanding since Slice C began. The local stack runs MinIO, which **cannot**
prove S3 presigning, bucket CORS, CloudFront, IAM, or behaviour on a mobile
network. The full composer journey (init → presigned PUT → confirm → status →
alt-text → create → read) must be run once against AWS staging from a real
handset.

> The test handset carries a live SIM. Coordinate before driving it; do not
> inject blind input.

### R-2. Uncommitted work

Slices A, B and C are complete and unbanked on `codex/module-01-02-launch-safety`.
No commit, push, branch or PR has been authorised. This is a single point of
failure until it is.

### R-3. MP-LB-1 stale-completion race — generation/CAS on preview-repair obligations

Recorded 2026-08-28 from `prompt/chat-deleted-preview-durable-repair-review.md`
(the final chat review cycle). The durable deleted-preview repair is in place
and all single-attempt boundaries are proven, but ONE interleaving remains,
reproduced by the reviewer against the production SQL:

1. delete attempt A writes an obligation; its Scylla delete fails; the
   obligation ages past `liveMessageRetireAfter`;
2. a worker claims it, reads the message LIVE, and decides to retire —
   but has not completed yet;
3. the client retries the delete: `CreatePreviewRepairObligation` re-arms the
   SAME `message_id` (upsert bumps only `next_attempt_at`), and the retry's
   Scylla delete succeeds;
4. the stale worker's completion deletes by `message_id` alone — removing the
   NEWLY re-armed obligation;
5. if the retrying process dies before its inline repair, deleted plaintext
   can again remain in `last_message_preview` with no durable debt left.

**Required fix (design agreed, not yet implemented):**

- add a monotonically increasing `generation BIGINT` to
  `chat.preview_repair_obligations` (expand-only `ALTER ... ADD COLUMN`);
- `CreatePreviewRepairObligation` re-arm increments the generation AND
  refreshes `created_at` (the deletion-attempt age must restart, or the
  young-live-message deferral is bypassed too);
- claims return the generation; `CompletePreviewRepairObligation` and
  `DeferPreviewRepairObligation` become CAS:
  `... WHERE message_id = $1 AND generation = $2` — a stale worker's
  completion/defer of a newer generation is a no-op;
- proofs: a unit interleaving test racing live-message retirement against a
  same-message re-arm, and a live-PostgreSQL test executing the reviewer's
  exact five-step SQL sequence asserting `obligations_after_stale_completion = 1`.

Until fixed, the exposure window requires: a failed Scylla delete, a >10-minute
wait, a worker claim racing a client retry, the retry's delete succeeding, AND
process death before inline repair — narrow, but it is deleted plaintext, so it
gates release.

---

## Slice D debt (notification inbox)

> D-D2 (runtime permission) and D-D7 (inbox → preferences) were CLOSED on
> 2026-08-23 — see `prompt/slice-d-notifications-followup.md`.

### D-D1. FCM delivery is unproven

The path is wired end to end — manifest service, POST_NOTIFICATIONS, token
registration on session, unregistration on sign-out — but no push has been
received on a real device. Fold into the physical-device smoke (R-1).

### D-D3. notification-service test coverage

2 test files for 8 event consumers and two push transports. `processMessage`
takes a concrete `*service.Service`, so unit-testing the consumer needs an
interface extraction. Deferred because those files were under concurrent edit.

> **Partially closed 2026-08-27** — `prompt/chat-mute-and-preview-claude.md`.
> The CHAT consumer now depends on a narrow `chatNotifier` interface and has
> four unit tests (mute routing, sender skip, fan-out continuation, absent
> muted list). The other consumers still hold the concrete service.

### D-D4. Notification rows say "Someone"

Actor display names need a batch profile lookup; per-row resolution would fire
N requests on inbox open.

### D-D5. Comment deep links open the post, not the comment

The comment id is carried on `NotificationTarget.PostComment` and nothing
consumes it yet.

### D-D6. No realtime inbox

The inbox refreshes on open. notification-service exposes SSE at
`/v1/realtime/sse`; wiring it is follow-up.

---

## Slice C debt (from the closure verdict §9)

### C-D1. Confirmed-media claim protocol

C-CLB-1 closed by **disabling** confirmed-media reclamation, not by making it
safe. Confirmed assets are refused in both `ListReclaimableMedia` and
`DeleteOrphanMediaAtomic`; `pending_upload` GC still runs.

The cost is storage: an abandoned composer photo is retained rather than swept.
Re-enabling reclamation requires every live-reference writer — `users`,
`channels`, `business_pages`, `portfolio_items`, `video_metadata`,
`reel_drafts`, `audio_tracks`, `media_clips`, spanning six services — to join a
durable claim/lease protocol.

**Do first:** monitor retained confirmed composer assets. Build the protocol only
if that volume justifies it. `upload_purpose='composer'` is already being
recorded on every composer upload, so the data a future sweeper needs is
accumulating from launch rather than starting empty on the day someone writes it.

### C-D2. Rate-limit override governance

Four knobs were added during C-CLB-PROOF-1 with production values as defaults:
`RATE_LIMIT_IP_PER_SEC` (100), `RATE_LIMIT_USER_PER_SEC` (60),
`MAX_POSTS_PER_HOUR` (20), `MAX_POSTS_PER_DAY` (50).

Any positive value is accepted, so a deployment typo such as `5000000` would
effectively remove abuse protection. Needed: reviewed upper bounds (or an
explicitly non-production proof mode), startup logging of effective values in
both services, and an alert on non-default production values.

**Until then: leave all four unset in production manifests.**

### C-D3. Same-key replays consume post quota

`CheckPostRateLimit` runs *before* the durable idempotency lookup, so a retry
under an existing key spends a unit of the user's post quota. A replay at the
exact quota boundary can receive a temporary 429 even though no second post is
created. Exactly-once effects are not violated and a later retry returns the
durable result. Fix: check the durable replay path before charging a new-post
quota unit.

### C-D4. Media lifecycle plumbing

- `media_transcode_inbox` rows are not removed when their parent media is
  reclaimed. Decide retain-or-clean explicitly.
- `media_event_outbox` has no pending-versus-published retention semantics.
  Its migration deliberately omits an FK so a committed-but-unpublished event
  survives; the reclaim transaction neither protects nor cancels such a row.
  **Must be settled before any video-capable composer uses confirmed GC.**

### C-D5. Test coverage gaps

- The media-status contract test constructs `MediaStatusResponse` directly, so
  it would stay green if the service stopped copying `ModerationStatus`. Replace
  with a service/handler-path test. (The executed live contract covers this for
  now.)
- Add the precise "press Post after `Published`" regression test. The current
  double-press test covers an in-flight double tap, not a press after completed
  success.

### C-D6. Composer polish

- Top-bar Back on an **empty** composer calls `onDiscardRequested()` and does
  not close, while system Back closes normally. Two ways out, two behaviours.
- Draft writes launch an unconstrained coroutine per edit
  (`ComposerViewModel.persist`). The standard Room executor usually preserves
  order, but the contract should be explicit — serialise or coalesce.

---

## Slice B debt

### B-D1. `LoginRequestDto.platform` omitted

The server's login `platform` field is optional, so this is an observability and
audit limitation, not a broken recovery path. Deliberately **not** covered by a
test: a test asserting the current omission would freeze undesired behaviour and
make the eventual fix look like a regression. Add the test for the desired
emitted value at the time of the fix.

### B-D2. Other deferred Slice B items

Outbound typing publication; privacy-cache invalidation; bootstrap `ALTER`
ownership; the `410 SETTINGS_AUTHORITY_MOVED` tombstone.

---

## Slice A debt

### A-D1. Raw Redis body versus semantic PostgreSQL fingerprint

The comment idempotency middleware fingerprints the raw body while PostgreSQL
fingerprints normalised text. Two equivalent JSON encodings of the same intent
can therefore produce a safe false 409. It cannot recreate the cross-post replay
defect that was fixed.

Note this is the *comment* path. Post creation was moved to a canonical
whole-request fingerprint in Slice C and is not affected.
