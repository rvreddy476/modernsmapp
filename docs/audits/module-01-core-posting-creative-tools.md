# Module 1 Audit — Core Posting and Creative Tools

_Claude (primary builder) · audit-only, no code changed · 2026-08-07_
_Per SUPER_APP_SHARED_BRIEF.md: PostTube stays a separate long-video product; social sync is optional and driven by creator + viewer choices. Awaiting scope approval before implementation._

---

## 1. Objective and primary user loop

Give every user a reliable unified composer (text, image, carousel, voice, poll, Story, Reel, links) and give creators a canonical long-video home (PostTube) with *optional, reference-based* distribution into the social feed.

**Loop:** create → distribute (by choice) → get engagement (reactions/comments/reposts) → see analytics/notifications → create again.

## 2. Personas and jobs-to-be-done

- **Casual poster** — share a moment (text/photo/poll) in <30 s, in their language, on a low-end phone.
- **Creator** — publish a long video once, control where it appears (PostTube only vs feed preview vs teaser), schedule it, keep monetization/analytics attached to the canonical record.
- **Viewer** — tune how much long video invades their social feed; subscribe to a channel without following the person socially (and vice versa).

## 3. Repository findings — gap map

Legend: **EXISTS** (verified in code) · **PARTIAL** · **MISSING** · **CONFLICTING**

### 3.1 Unified composer (brief: text, image, carousel, voice, poll, Story, Reel, links)

| Requirement | Status | Evidence |
|---|---|---|
| Text / photo / video / article / poll post types | **EXISTS** | `PostType` enum in `creation_provider.dart`; `posts` + `polls/poll_options/poll_votes` tables; poll composer UI complete |
| Mixed carousel (multi-media) | **EXISTS** | `addFiles(List<XFile>)`, per-file upload progress; `post_media(post_id, media_id, kind)` |
| Voice post | **MISSING** | No voice `PostType`, no recorder in composer. (`audio_tracks` is background music for reels, not voice notes) |
| Story (with interactive stickers) | **EXISTS** | `stories` + `story_interactive`; poll/quiz/slider/question/countdown widgets; expiry cleanup worker (5 min tick) |
| Reel composer (trim, cover, audio, filters) | **EXISTS** | `reels_editor_screen`, trimmer, cover-frame picker, audio browser, `reel_drafts` (30+ option columns) |
| Links + previews | **EXISTS** | `link_previews` table (24 h cache), rich text `rich_text JSONB` on posts |
| Feeling/activity, location, background color | **EXISTS** | `posts.feeling/activity/location_*`; color posts gated to text-only |

### 3.2 Threads, replies, reposts, hashtags, mentions, drafts, scheduling

| Requirement | Status | Evidence |
|---|---|---|
| Replies (nested comments) | **EXISTS** | `comments.parent_id`, like/dislike, `around/:commentId` deep-link fetch |
| Reposts + quote-posts | **EXISTS** | `post_reposts(repost_type IN plain/quote)`, undo, reposters list |
| X-style threads (chained multi-part posts) | **MISSING** | No `thread_root_id`/`reply_to_post_id` on `posts`; comments ≠ threads |
| Hashtags | **EXISTS** | `posts.hashtags[]`, trending + SSE streams (`/v1/hashtags/*`), mobile trending strip + two hashtag surfaces |
| Mentions | **EXISTS** | `posts.mentions UUID[]`, `post_mentions`, mention field widget |
| Server-side drafts — video/reel | **EXISTS** | `reel_drafts` CRUD + publish under `/v1/reels/drafts` |
| Server-side drafts — text/photo/poll posts | **MISSING** | Composer keeps one local in-memory draft ("draft will be cleared"); nothing survives reinstall |
| Scheduling — video/reel drafts | **EXISTS** | `reel_drafts.schedule_at`; in-process worker `PublishScheduledDrafts` every 60 s in post-service `cmd/server/main.go` |
| Scheduling — other post types | **MISSING** | No path |
| Standalone scheduler binary | **CONFLICTING** | `post-service/cmd/scheduler/main.go` queries `posts.status='scheduled' AND publish_at<=NOW()` — **neither column exists anywhere in the schema or Go code**. Dead binary; if deployed it fails every 30 s tick |

### 3.3 PostTube as separate product + optional sync (the confirmed decision)

| Requirement | Status | Evidence |
|---|---|---|
| Canonical long-video record | **EXISTS** | `video_metadata` (1:1 with posts; `final_category IN ('flick','long_video')`), playback/thumbnail/trim owned there; chapters, cards, end-screens, series, playlists |
| Separate PostTube surfaces | **EXISTS** | Feed-service `/videos`, `/watch` (distinct from `/home`, `/flicks`); mobile `posttube/` (home, channel, subscriptions, trending, watch history, upload); watch progress + continue-watching |
| Subscribe ≠ follow separation | **EXISTS** (structure) | `channel_subscriptions` (user-service) is independent of `graph.follows` |
| Distribution as references, not copies | **PARTIAL** | `crosspost_links(source_module posttube/postgram → target postbook)` with ownership check + 5/hr rate limit; reference row deletion doesn't touch source. But feed inclusion is *also* governed by 3 overlapping post columns (below) |
| Creator choice: `publish_to_feed` | **PARTIAL** | Column exists on `posts` and `reel_drafts` (default TRUE); **PostTube upload screen hardcodes `visibility:'public'` and exposes no distribution choices at all** |
| Creator choice: visibility public/unlisted/private/scheduled/subscriber-only | **PARTIAL** | reel_drafts: public/followers/private/unlisted ✔; `tier_required_id` + `PUT /:postId/membership` = subscriber-only bones ✔; `scheduled` visibility missing for non-reel; none surfaced in PostTube upload UI |
| Creator choice: notify subscribers on/off | **PARTIAL/CONFLICTING** | notification-service fans out `PostCreated` for video/flick to **followers** (graph), not channel **subscribers**, always-on, no per-upload toggle. Brief requires subscriber-targeted + opt-out |
| Creator choice: auto/manual Reel teaser | **MISSING** | `reel_crosspost` targets feed/story/group/page (manual, idempotent) but nothing generates a teaser from a long video |
| Creator choice: publish to selected communities | **MISSING** | No community target anywhere; brief also notes Communities are disabled in Flutter nav (consolidated into Groups) — targeting model undecided |
| Viewer choice: long-video frequency (hidden/reduced/balanced/preferred) | **MISSING** | feed `user_preferences.feed_mode` is only chronological/ranked; `viewer_media_prefs` learns dwell implicitly but there is no explicit user control |
| Viewer choice: autoplay / data-saver | **MISSING** (in scope surfaces) | No data-saver or long-video autoplay setting found in settings module |
| Distribution flags coherence | **CONFLICTING** | Three overlapping columns govern the same intent: `posts.share_to_postbook`, `posts.publish_to_feed`, `posts.app_origin`, plus `reel_drafts.cross_post_postbook/cross_post_posttube`, plus `crosspost_links`, plus `reel_crosspost`. No single readable "distribution policy" — exactly what the brief's JSON policy object is meant to fix |

### 3.4 Reliable media, captions, accessibility, low-data

| Requirement | Status | Evidence |
|---|---|---|
| Resilient upload (presigned + resumable chunked + orphan GC) | **EXISTS** | 3-step init/PUT/confirm; 20 MB threshold chunked path with per-part retry; client + server orphan cleanup |
| Transcode/renditions/HLS | **EXISTS** | media-worker, `media_renditions`, `media_variants`, 307-to-CDN serve |
| Subtitles incl. auto-generation | **EXISTS** | `media_subtitles(source auto_generated/manual/translated)`, `POST /:mediaId/subtitles/auto` |
| Alt text | **PARTIAL** | Backend complete (`alt_text` on init, `PATCH /:mediaId/alt-text`); composer never asks for it |
| Low-data delivery controls | **PARTIAL** | Server renditions exist; no client data-saver mode |
| Indian languages / transliteration ("Language Twins") | **MISSING** | `posts.language` column only; translation infra exists in qa-service but not for posts |
| Moderation gate | **EXISTS** | `review_status` (approved/flagged/rejected/pending/needs_changes), resubmit loop, reviewer pipeline |
| Remix/fork provenance | **PARTIAL** | `remix_setting`, `GET /:postId/remix-token`; no provenance chain table, no revenue share |
| Post Canvas / Action Post blocks | **PARTIAL** (foundation) | `rich_text JSONB` + product tags + polls exist as separate mechanisms; no unified block model |

### 3.5 Other conflicts worth recording

1. `posts.content_type` **and** `posts.post_type` both exist (both default `'post'`) — schema drift, pick one.
2. Two draft systems (server `reel_drafts` vs composer local state) will confuse users once text drafts ship — needs one mental model.
3. Doc caveat confirmed: web app absent from workspace; docs stale in places (e.g., PLATFORM_SPEC says "tokens carry no scopes claim" — already fixed).

## 4. Proposed scope

### P0 (required for a safe, coherent launch)

| # | Item | Why P0 |
|---|---|---|
| P0-1 | **Canonical distribution policy object.** One JSON policy per post (`{posttube, main_feed, notify_subscribers, create_reel_preview, communities[], visibility}`) stored as `posts.distribution JSONB` + normalized read; existing boolean columns become derived/legacy-write-through. Single service-layer gate decides feed candidacy | Resolves the CONFLICTING 3-flag mess before more features stack on it; is the contract every later phase uses |
| P0-2 | **Creator publication choices in PostTube upload + reel composer (mobile).** Visibility (public/unlisted/private/scheduled), main-feed toggle, notify toggle. Kills the hardcoded `visibility:'public'` | The confirmed product decision is unusable without UI |
| P0-3 | **Subscriber-aware, toggleable publish notifications.** Fan-out targets `channel_subscriptions` (fallback: followers when no channel), honors `notify_subscribers`, respects existing bundling/dedup | Current behavior contradicts the brief and spams followers |
| P0-4 | **Viewer long-video frequency control** (`hidden/reduced/balanced/preferred`) in feed-service `user_preferences` + ranking multiplier + settings UI | The viewer half of the confirmed decision |
| P0-5 | **Generalize server-side drafts + scheduling to all post types** (text/photo/carousel/poll/article), one worker (extend `PublishScheduledDrafts`); **delete the dead `cmd/scheduler` binary** | Phase-one boundary lists drafts+scheduling as core; removes a broken deployable |
| P0-6 | **Voice posts (minimal).** Composer recorder → audio upload (existing media path) → `post_media kind='voice'` render with player | In the brief's phase-one composer list; India-first (voice > typing for many users) |
| P0-7 | **Alt-text field in composer** (backend already done) | Accessibility is a release requirement per principle 5; trivially small |

### P1 (shortly after launch)

- P1-1 Auto/manual **Reel teaser** from long video (`create_reel_preview`) reusing trim + `reel_crosspost`.
- P1-2 **Threads** (multi-part chained posts): `posts.thread_root_id`, composer "add to thread", feed collapse.
- P1-3 **Data-saver + long-video autoplay** settings honored by players (renditions exist).
- P1-4 **Groups/communities as distribution targets** (after the Groups-vs-Communities consolidation decision).
- P1-5 Surface **auto-captions** + language tagging at publish time (backend exists).
- P1-6 `scheduled` + `subscriber-only` visibility for regular posts (rides on P0-1/P0-5 + `tier_required_id`).

### P2 / EXPERIMENT

- Post Canvas block model (P2, build on `rich_text`); Language Twins (EXPERIMENT — start captions-only); fork provenance chain + revenue split (EXPERIMENT); co-create ownership (P2).

## 5. User journeys and key UI states

**J1 — Creator publishes long video (the decision, end-to-end):** PostTube upload → metadata (title, chapters auto, captions auto, alt text) → **Distribution sheet**: visibility ▸ public / unlisted / private / scheduled(+datetime) · [x] Also show in social feed · [x] Notify subscribers · [ ] Create Reel teaser (P1) → publish → video lives in PostTube; if main_feed, feed shows a *reference card* (removing it never touches the video). States: uploading (resumable, background), processing, scheduled (editable), published, review-pending, failed(retry).

**J2 — Casual composer:** type/attach (photo·carousel·voice·poll) → mentions/hashtags inline → save draft (server) or schedule → post. States: empty, validating (poll min-options), draft-saved badge, offline-queued, moderation-held, posted.

**J3 — Viewer tunes the feed:** Settings ▸ Content preferences ▸ Long videos: hidden/reduced/balanced/preferred; Data saver on/off. Feed ranking shifts immediately; PostTube surface unaffected (subscriptions still deliver there).

**J4 — Scheduled publish:** draft with `schedule_at` → worker publishes ≤60 s after due → distribution policy applied at publish time (not at schedule time) → notifications fan out.

## 6. Technical architecture

- **No new service.** Per the brief, media-service + post-service + feed-service suffice for MVP; a video-metadata service is a later scaling boundary.
- **Canonical record:** `posts` row + `video_metadata` stays the single owner of playback/monetization/copyright. Feed/communities/messages hold references (`crosspost_links` today; policy object becomes the authority for *implicit* feed distribution).
- **Distribution decision point:** one function in post-service (`ResolveDistribution(post)`) consumed by (a) feed candidate emission, (b) notification fan-out, (c) crosspost creation. Emits `post.distribution.updated` event so feed-service can re-evaluate on change.
- **Viewer preference:** feed-service only — new column + ranking multiplier ({hidden:0, reduced:0.3, balanced:1, preferred:1.6} — tune later); no cross-service coupling.
- **Scheduling:** stays an in-process worker on post-service (already exists for reels); table generalizes from `reel_drafts` to `post_drafts` (reels view kept for compatibility). Delete `cmd/scheduler`.
- **Voice:** reuse media pipeline (`file_type=audio` already supported by audio endpoints); no new storage.

## 7. Affected files/services (implementation footprint)

**Backend**
- `Architecture/services/post-service/` — `internal/service/drafts.go` (generalize), `internal/store/postgres/{posts,drafts}.go`, `internal/http/{drafts,posts,crosspost}.go`, `internal/events/producer.go` (+`post.distribution.updated`), `ensure_schema` DDL, **delete `cmd/scheduler/`**
- `Architecture/services/feed-service/` — `user_preferences` DDL + `/preference` handler + ranking multiplier
- `Architecture/services/notification-service/` — `internal/events/consumer.go` fan-out target = channel subscribers, honor `notify_subscribers`
- `Architecture/services/user-service/` — internal endpoint: list subscriber IDs for a channel (if not present)
- `Architecture/services/media-service/` — none expected (alt text + audio + subtitles exist)

**Mobile (`mobile/atpost_app/`)**
- `lib/features/posttube/posttube_upload_screen.dart` — distribution sheet (kills hardcoded visibility)
- `lib/features/create/` — voice recorder, alt-text field, server drafts + schedule picker; `creation_provider.dart` (+`PostType.voice`)
- `lib/features/reels/reels_caption_screen.dart` — same distribution sheet
- `lib/features/settings/` — long-video frequency + (P1) data saver
- `lib/data/repositories/{post,feed,drafts}_repository.dart`, `lib/data/models/post.dart`

## 8. Schema / API / event impact

**DDL (all additive, idempotent `ensureSchema` style):**
```sql
ALTER TABLE posts ADD COLUMN IF NOT EXISTS distribution JSONB;           -- P0-1 (null ⇒ derive from legacy flags)
-- generalize drafts: new table post_drafts(id, author_id, post_type, payload JSONB,
--   schedule_at, status, published_post_id, created_at, updated_at);    -- P0-5
ALTER TABLE user_preferences ADD COLUMN IF NOT EXISTS long_video_frequency TEXT
  NOT NULL DEFAULT 'balanced' CHECK (long_video_frequency IN ('hidden','reduced','balanced','preferred')); -- P0-4 (feed db)
-- P1-2: ALTER TABLE posts ADD COLUMN IF NOT EXISTS thread_root_id UUID, reply_to_post_id UUID;
```

**API:**
- `POST/PATCH /v1/posts` accept `distribution{}` (legacy booleans still accepted, mapped, deprecated in response docs)
- `POST /v1/posts/drafts` · `GET/PATCH/DELETE /v1/posts/drafts/:id` · `POST /v1/posts/drafts/:id/publish {schedule_at?}` (mirrors reels drafts)
- `POST /v1/feed/preference {long_video_frequency}` (extends existing `/preference`)
- Voice: none new (media init with `file_type=audio`, post create with `kind='voice'`)

**Events:** `post.created` payload +`distribution`; new `post.distribution.updated`; notification consumer reads both. All on existing `social.events.v1`.

## 9. Migration & compatibility plan

1. Additive DDL only; `distribution IS NULL` ⇒ resolver derives from legacy columns (`publish_to_feed`, `share_to_postbook`, visibility) — **zero backfill required**, old rows behave identically.
2. Dual-write window: new writes populate both `distribution` and legacy booleans; readers switch to resolver; legacy columns retired in a later cleanup phase (documented, not dropped now).
3. `reel_drafts` stays; `post_drafts` is new. Reels UI keeps its endpoints; internally both feed one scheduler loop. No mobile force-upgrade: old app versions posting without `distribution` keep working via defaults.
4. Notifications: flag-gated rollout (`NOTIFY_SUBSCRIBER_FANOUT=true`) with follower-fanout fallback, so a bad subscriber query can be reverted by env var.
5. `cmd/scheduler` deletion is safe: it references columns that never existed, so nothing can be depending on it in any environment.
6. Rollback: every feature behind feature-flag-service flags (`composer_voice`, `posttube_distribution_sheet`, `feed_lv_frequency`); DDL is additive so rollback = flag off.

## 10. Test plan

- **Go unit:** distribution resolver truth table (legacy×new precedence — the critical matrix); drafts generalization (create/patch/publish/schedule, due-publish idempotency — extend existing `internal/service` test dirs); notification fan-out targeting + toggle (notification-service currently has thin coverage); feed multiplier per frequency value.
- **Integration (`Architecture/tools/integration/`):** new `posting_distribution_test.go` — publish PostTube-only ⇒ absent from `/v1/feed/home`, present in `/v1/feed/videos`; flip main_feed ⇒ appears; remove reference ⇒ video intact (the brief's invariant); scheduled draft publishes ≤90 s; subscriber-notify on/off.
- **Flutter:** widget tests for distribution sheet state, voice recorder states, poll validation (exists), draft save/restore; `patrol`/integration happy path: compose text+photo → appears in feed. Note: feed-service has **zero Go tests today** — the multiplier work must arrive with its first test file.
- **Regression guard:** old-payload post create (no `distribution`) via integration test to pin backward compatibility.

## 11. Safety, privacy, abuse, compliance

- Scheduled posts must re-check `review_status` and author standing (strikes/ban) **at publish time**, not schedule time.
- Voice posts enter the same moderation queue (audio → reviewer pipeline; auto-transcript via existing subtitle auto-gen aids keyword filters).
- `unlisted` must be excluded from feeds/search/trending everywhere (add to integration invariants).
- Notification fan-out respects existing prefs/dedup; subscriber lists are not exposed client-side.
- DPDP: distribution policy and viewer prefs are user-controlled data — export/delete paths ride on existing settings machinery.

## 12. Metrics & acceptance criteria

- ≥99% of publishes with `main_feed:false` never surface in `/home` (invariant, monitored).
- Scheduled-publish latency p95 ≤90 s; zero double-publishes (idempotency).
- Composer: draft-loss reports → ~0; voice post p50 create time <15 s on 3G profile.
- Notification opt-out honored 100%; complaint rate on upload notifications trending down.
- Viewer frequency: measurable feed composition shift within one session for `hidden`/`preferred`.

## 13. Open decisions (need answers before/while building)

1. **`distribution` JSONB vs normalized table?** Proposed: JSONB on posts (MVP speed) + resolver; revisit if per-target state machines emerge (communities, teasers).
2. **Groups vs Communities** consolidation — blocks P1-4 targeting model. Codex call.
3. Feed multiplier values for reduced/preferred — product tuning, ship behind config.
4. Retire `content_type` or `post_type`? Proposed: keep `post_type`, freeze `content_type` (read-only legacy).
5. Voice post max duration (proposed 5 min) and whether voice gets auto-transcript captions at P0 (proposed: yes-async, non-blocking).
6. Does "notify subscribers" also badge the PostTube subscriptions tab (in-app) when push is off? (No FCM in the app yet — push arrives with the separate notifications workstream.)

**Assumptions:** single-workspace source of truth (no web app changes owed); reels drafts UI remains unchanged for now; `crosspost_links` remains the mechanism for *explicit* cross-surface references while `distribution` governs implicit feed candidacy; existing rate limits (5 crossposts/hr) stay.
