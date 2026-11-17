# Track B — security (reels)

- **Date:** 2026-11-17. Read-only. Backend and Android: `C:\workspace\modernsmapp` (`codex/module-01-02-launch-safety`, working tree as found). Web: `C:\workspace\wt\pb-echo` (`feat/echo-web`).
- **Method:** code read end to end for each claim. No service, database, container or device was started and no deployed environment was called. Two existing unit-test runs were the only commands executed (quoted under "Sound"). Two helper reads (client XSS/Android, analytics/reports) failed on a rate limit and were redone here in narrower form; what that narrowing left out is listed in "Not verified".
- Backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Web paths are relative to `C:\workspace\wt\pb-echo`.
- **Gateway behaviour is taken from the feed Track B** (`docs/production-readiness/feed/tracks/track-b-security.md` §1, re-verified there): the gateway strips client `X-User-Id`, `X-Scopes` and `X-Internal-Service-Key`, sets `X-User-Id` from the verified token, refuses any path with an `internal` segment, and lets a request with no token through as anonymous. The difference for this module is checked below: the gateway does **not** stamp the internal key on `/v1/media`, `/v1/audio`, `/v1/audio-library`, `/v1/subtitles` (00-map §3.0, `api-gateway/pkg/internalroutes/internalroutes.go:367-441`), and media-service has no key check on its user routes.

## Verdict for this track

**Not ready, no P0 found.** Through the gateway I found no way for one user to read another user's private, followers-only, deleted, pending, rejected or members-only reel bytes, manifest, segments, captions, renditions record, sounds or originals: every read path goes through one gate whose rules are strict and fail closed. Two P1s remain: media-service decides "who is asking" from a bare `X-User-Id` header with no key or token, and network policy is off, so anything running inside the cluster can read any media as its owner (B-1); and account deletion never reaches analytics, so a deleted user's watch history stays (B-2). Six P2s and five P3s follow.

Open questions from the map that belong to this track: **5** answered by B-1; **6** answered under "Sound" (all three routes are gated); **8** answered by B-5 and "Sound".

---

## 1. Route table (auth decision and object check)

**media-service.** Writes use media-service's own JWT middleware (`internal/http/middleware.go:177-196`: HS256/RS256 only, bearer or `access_token` cookie, sets `X-User-Id` from the token). Reads have no middleware: the caller is `X-User-Id` as received (`internal/http/delivery_viewer.go:28-34`), absent = anonymous. No internal key on these routes (`internal/http/handler.go:121-131`). "Gate" = `delivery.Gate` (`internal/delivery/authz.go:527-674`) asking post-service `POST /v1/internal/media-access`, whose rules are in `post-service/internal/service/story_surface.go:252-388` and `media_access.go:94-184`.

| Method | Path | Auth | Object check (file:line) |
|---|---|---|---|
| POST | `/v1/media/init` | token | key built server-side `user/<caller>/<new uuid>/original` (`service/upload.go:166-167`) |
| POST | `/v1/media/confirm` | token | uploader only (`service/upload.go:230`), key re-derived (:247) |
| DELETE | `/v1/media/:id` | token | uploader only (`http/handler.go:541-548`); refused while another service claims it |
| PATCH | `/v1/media/:id/alt-text` | token | uploader (not re-read) |
| POST | `/v1/media/:id/extract-frame`, `/frames` | token | uploader only (`service/frames.go:33`); see B-6 |
| POST | `/v1/media/batch` (≤50) | header | Gate per asset (`service/media_batch.go`) |
| GET | `/v1/media/:id` | header | uploader short-circuit else Gate (`service/record_read.go:106-122`) |
| GET | `/v1/media/:id/status` | header | same as above (`http/handler.go:562-585`) |
| GET | `/v1/media/:id/url`, `/url/:variant` | header | Gate (`service/playback.go:123-169`); video original only to the uploader and only if the ETag still matches (`service/media_batch.go:223-263`) |
| GET, HEAD | `/v1/media/:id/serve[/:variant]` | header | Gate, then 307 to a signed URL, `Cache-Control: private, max-age=60`, `Vary: Cookie, Authorization, X-User-Id` (`http/handler.go:616-669`) |
| GET | `/v1/media/:id/hls/:playlist` | header | Gate; playlist `private, no-store`, segments signed (`http/handler.go:710-727`, `service/playback.go:295-314`) |
| GET | `/v1/media/:id/hls-seg/:name` | header | `access_scope='anonymous'` assets only, Gate (`http/handler_anonymous.go:50`) |
| GET | `/v1/media/:id/renditions` | header | Gate, redacted for non-owners (`http/renditions_handler.go:21-38`, `service/renditions.go:155`) |
| GET | `/v1/media/:id/audio-tracks` | header | Gate; writes owner only (`service/audio_tracks.go:424`) |
| GET | `/v1/media/:id/download` | header | uploader only, else 404 (`service/download.go:74`) |
| * | `/v1/media/upload/resumable/*` | token | session owner (not re-read line by line) |
| * | `/v1/media/:id/cover-frame`, `/suggested-frames`, `/studio/sessions*` | token | owner (not re-read) |
| GET, POST | `/v1/media/studio/stickers*`, `/templates` | public | catalogue; `/use` spawns a goroutine per call (P3 note) |
| GET | `/v1/subtitles/:id`, `/:id/track/:lang`, `/:id/status` | header | Gate + draft tracks owner only (`http/clips_handler.go:124-141,324-388`) |
| POST, PATCH | `/v1/subtitles/:id[...]` | token | owner (`clips_handler.go:84-221,390-457`) |
| GET | `/v1/audio/search` | header | parameterised ILIKE, limit capped (`service/audio_reads.go:53,149`, `store/postgres/audio.go:276-284`), per-viewer filter |
| GET | `/v1/audio/:id`, `/url`, `/serve` | header | answered to the source video's audience via Gate (`http/audio_handler.go:84-134`, `service/record_read.go`) |
| GET | `/v1/audio/trending` | token | block filter per viewer (`cmd/server/main.go:292-307`) |
| POST | `/v1/audio/extract/:id`, `/:id/use`, `/voiceover` | token | owner / may-hear (`audio_handler.go:55-210`) |
| GET | `/v1/audio-library[...]`, `/serve` | public | curated catalogue, no user content |
| * | `/v1/media/internal/*` (orphan delete, delete, anonymize, reprocess, sound, sound takedown, fingerprint, claims, alt-text) | refused at the edge; in cluster: shared internal key only, some add `refuseGatewayIdentity` (`http/handler.go:193-246`); see B-7 | body names the referrer; store re-checks references for delete |
| * | `/v1/media/internal/{image,document}-bytes`, `recordings/import` | key + signed service token | per-caller scope (`cmd/server/main.go:309-344`) |
| * | `/v1/media/internal/admin/sounds/*`, `/v1/media/admin/evidence/:id` | admin-service token | scopes `media:sounds.*` (`cmd/server/main.go:458-467`) |
| * | `/v1/clips/:postId` | no gateway prefix found | owner of every clip (`clips_handler.go:229-282`) |

**post-service (reels parts).** All `/v1` routes require the internal key; caller is the gateway-set `X-User-Id` (feed Track B).

| Method | Path | Auth | Object check |
|---|---|---|---|
| POST, GET, PATCH, DELETE | `/v1/reels/drafts[/:id]`, `/:id/publish` | token | handler passes the caller to every service call (`http/drafts.go:35-248`); media and cover must be the author's own, claimed with media-service (`writeOwnMediaError`) |
| GET | `/v1/reels/slug/:slug` | public | **none**: returns the reel id for any slug (`http/reel_discovery.go:43-60`), B-12 |
| GET | `/v1/reels/hashtags/trending` | public | aggregate, limit ≤100 (`reel_discovery.go:25-41`) |
| GET | `/v1/reels/moderation/flagged`, `/:id/moderation` | moderator | `X-Scopes` moderator scope, gateway-derived (`http/moderator_gate.go:19-42`) |
| POST | `/v1/reels/:id/view`; GET/POST `/v1/reels/feed*` | retired | answer gone (00-map) |
| GET, POST | `/v1/reels/batch/counts`, `/saved`, `/liked`, `/v1/uploads/*` | token | own lists (not re-read) |
| POST, DELETE, GET | `/v1/posts/:id/offline`, `/offline`, `/offline/check` | token | viewer must be able to see the post, `allow_download` or owner, membership, device; copies revoked on check when the live decision refuses (`service/offline_copies.go:386,586-673`); no URL is issued |
| POST | `/v1/internal/media-access[/batch]` | key, no edge route | the playback authority (`http/media_access_handler.go:82-166`) |
| GET, PATCH | other post routes (detail, edit, delete, share, repost, comments, likes, saves) | — | audited in feed Track B |

**feed-service**: `/v1/feed/reels`, `/flicks`, related, feedback: key + `X-User-Id` required, 120/min per user (feed Track B and 00-map §3.3). Not re-read.

**analytics-service**: `POST /v1/analytics/events` requires a non-nil gateway actor (`internal/service/ingest.go:168-171`), 1-200 events, event id 16-128 chars, timestamp window, server-side ownership and duration (`ingest.go:172-242`). Per-content and dashboard reads were not re-read (Not verified).

**trust-safety-service**: `POST /v1/reports` user; `GET /v1/reports` own list with `?mine=true`, else admin; `GET`/`PATCH /v1/reports/:id` admin only (`internal/http/handler.go:238-330`; `adminAllowed` = admin-service token or gateway-derived `admin` scope, `admin_token.go:253-266`). `GET /v1/media-labels/:mediaId` has no role check (`trust_extras_handler.go:200`) but no gateway prefix was found (in-cluster only, P3 note).

---

## 2. Findings

```
ID:            REELS-B-1
Title:         media-service takes the viewer from a bare X-User-Id header with no key or token on every read route, and network policy is off, so any workload inside the cluster can fetch any private, pending or deleted-but-not-purged media as its owner.
Severity:      P1
Category:      authn
Evidence level: VERIFIED-READ (the trust); SUSPECTED (exploitation needs code running in the cluster)
Evidence:      delivery_viewer.go:28-34 (viewer = X-User-Id header, no verification); handler.go:121-131
               and 150-181 (read routes registered with no middleware; internal-key middleware only on
               /v1/media/internal); renditions_handler.go:13-19, clips_handler.go:63-73, audio_handler.go:26-31
               (same). The uploader short-circuits are local and need only the header:
               record_read.go:110-113, media_batch.go:203,227, download.go:74, caption_read.go:86;
               post-service answers "owner preview" to whatever viewer_id media-service sends
               (story_surface.go:253-260). charts/atpost-service/values.yaml:380-381 networkPolicy.enabled
               false and no media-service values file overrides it (only payments-service sets one).
               ingress.enabled false in deploy/services/media-service/values-prod.yaml:143-144, so the
               edge is the gateway, which does strip the header (feed Track B).
How to reproduce: From any pod in the namespace:
               curl -H 'X-User-Id: <author uuid>' http://media-service.atpost.svc.cluster.local:8087/v1/media/<media id>/url
               -> signed URLs for a private / followers-only / pending reel, its original included
               (GET /download returns the original as an attachment). The author id is public on the post.
Impact:        A single compromised or SSRF-with-headers-capable service, a debug pod or a misrouted
               internal client reads every user's private video. Through the public edge it is not
               reachable.
Fix:           Require RequireInternalKey on the /v1/media, /v1/audio, /v1/subtitles groups (the gateway
               then stamps the key on those prefixes, internalroutes.go) exactly as post/feed/analytics do;
               and enable the chart's networkPolicy for media-service with ingress from api-gateway and the
               named peers only.
Acceptance:    Handler test: a read with X-User-Id and no key answers 401/404; gateway test: the key is
               stamped on /v1/media/*; a rendered prod manifest contains a NetworkPolicy for media-service.
Effort:        S (key) + M (network policy for the peers that call media-service)
```

```
ID:            REELS-B-2
Title:         Account deletion never reaches analytics-service, so a deleted user's watch history, sessions and events are kept indefinitely.
Severity:      P1
Category:      privacy
Evidence level: VERIFIED-READ
Evidence:      identity-platform/services/auth-service/internal/purge/worker.go:44 DefaultRequiredServices
               lists graph, post, feed, ..., media, search, ... but not analytics or monetization;
               analytics-service has no identity.events consumer or purge handler (no match for purge,
               identity.events or user.purge_requested anywhere under analytics-service).
               Kept data: analytics.events_raw (user_id, session_id, payload),
               analytics.playback_sessions (actor_id, content_id), Scylla social_analytics.watch_sessions
               (viewer_id; TTL 90 d), viewer_history, reel_views (no TTL) (00-map §5.1;
               store/postgres/events.go:102, sessions.go:152, store/scylla/watch.go:45).
How to reproduce: Delete an account; after the auth purge completes, select from analytics.events_raw
               and playback_sessions where user_id/actor_id = the deleted id: rows remain.
Impact:        DPDP Act erasure obligation not met for viewing history, which is personal data;
               auth-service reports the purge complete while it is not.
Fix:           Add an analytics lifecycle consumer (the shared purge.NewHandler used by media-service,
               cmd/server/main.go:229-235) that deletes or pseudonymises the user's rows (keeping
               creator aggregates), ack as "analytics", and add "analytics" to REQUIRED_PURGE_SERVICES.
Acceptance:    Unit test of the handler deleting the user's rows; auth purge test that waits for the
               analytics ack.
Effort:        M
```

```
ID:            REELS-B-3
Title:         User-supplied audio (alternate audio tracks, voice uploads) reaches ffprobe/ffmpeg through the un-hardened runner: no protocol whitelist, no forced demuxer, no deadline.
Severity:      P2
Category:      upload
Evidence level: VERIFIED-READ (the code path); SUSPECTED (exploitability depends on the image's ffmpeg build)
Evidence:      The video pipeline is hardened: processing/tool.go:17-33,121,175-196 (-protocol_whitelist
               file, demuxer chosen from magic bytes, per-tool timeout). The audio path is not:
               processing/audio.go:34,58 (ExtractAudio, ProbeAudio via bare exec.CommandContext),
               audio_mux.go:35 (MuxAudio), audio_probe.go:30. Creator track upload writes the raw bytes
               and probes them with ProbeAudio after a header-only MIME check
               (service/audio_tracks.go:445-466), stores them, and the mux worker runs MuxAudio over them;
               voice confirm also calls ProbeAudio (service/voice.go:67). Image is alpine:3.19 with
               unpinned ffmpeg (00-map §8).
How to reproduce: POST /v1/media/<own ready video>/audio-tracks with an "audio/mp4" body that is a
               playlist or other external-reference container; observe whether ffprobe opens a network or
               file URL (needs a sandboxed run; not done).
Impact:        If any auto-probed demuxer follows a reference, SSRF to the metadata endpoint or internal
               services, or a local file read baked into an audio track the attacker can then play.
               Without a deadline a crafted file can hold a server goroutine and CPU indefinitely.
Fix:           Route ProbeAudio, ExtractAudio, MuxAudio and ProbeAudioFile through runTool with the audio
               magic-byte allow-list (ogg, mp3, m4a/mp4, wav, webm) the video path already has.
Acceptance:    Unit tests: a text/playlist body is refused before ffprobe runs; argv contains
               -protocol_whitelist file and -f; a stubbed slow tool times out.
Effort:        S
```

```
ID:            REELS-B-4
Title:         A reel that is deleted, made private or blocked keeps playing from already-issued signed URLs for up to 35 minutes, and deleted objects stay in the bucket for 90 days.
Severity:      P2
Category:      privacy
Evidence level: VERIFIED-READ
Evidence:      delivery/signer.go:66-72 MaxProtectedTTL 5 min for manifests and files; segments
               2x duration + 15 min, cap 12 h (00-map §4 step 13, delivery/sign_cache.go), so up to
               35 min for a 10-minute reel and 12 h for a long video. URLs are CloudFront canned
               signatures bound to the object only, not the viewer or IP. No CloudFront invalidation in
               media-service (no CreateInvalidation). Bucket is versioned and noncurrent versions are kept
               90 days (infra/terraform/modules/media/main.tf:138-151); blob DeleteObject deletes the
               current version only (internal/store/blob/store.go:509, no VersionId anywhere in the blob
               store).
How to reproduce: Open a reel, copy a segment URL from the media playlist, delete or privatise the reel,
               fetch the URL: it plays until the signature expires.
Impact:        Revocation is not immediate; a leaked segment URL is a bearer capability for its life;
               "deleted" video bytes remain recoverable by staff for 90 days (DPDP erasure).
Fix:           Cap the segment TTL for short-form at a few minutes past duration (players refresh on
               X-HLS-Expires-At already); on purge delete every object version (ListObjectVersions +
               DeleteObject with VersionId) or add a lifecycle rule expiring noncurrent versions under
               user/ after 1 day.
Acceptance:    Unit test on the segment TTL for a 60 s reel; integration test that purge leaves no
               object version.
Effort:        S
```

```
ID:            REELS-B-5
Title:         The displayed view count counts sessions, not viewers, so one signed-in account can raise a reel's public view count by sending play_end events with fresh session ids.
Severity:      P2
Category:      input
Evidence level: SUSPECTED
Evidence:      Views need an authenticated actor and use the server's duration and ownership; watched_ms is
               clamped to the server duration (ingest.go:167-242,377-599). Paid views are capped at 1 per
               viewer per day by default (aggregation/daily.go:23-34). But the displayed count is
               COUNT(*) of display-view sessions (aggregation/hourly.go:398,496), dedupe is per
               (actor, session, content) (store/postgres/sessions.go:164), session ids are client-chosen,
               and the only rate limit is the gateway's 60 rps per user.
How to reproduce: As one account, POST /v1/analytics/events batches of play_end events (200 per
               request) for one reel, each with a new session_id and watched_ms_total >= 3000; after the
               hourly aggregation the reel's views_display rises by the number sent. Not run: whether
               play_end without a prior play_start in the same session is accepted was not traced.
Impact:        Fake popularity on the public counter and in anything ranking on views_display (trending,
               discovery); creator-fund money is protected by the daily cap.
Fix:           Apply the same per-viewer-per-day cap to views_display, or count DISTINCT actor per day
               for the public number; require a play_start received at least watched_ms earlier in the
               same session.
Acceptance:    Aggregation test: 100 sessions from one actor in a day produce one display view.
Effort:        S
```

```
ID:            REELS-B-6
Title:         media-service has no request rate limit of its own, and owner endpoints that run ffmpeg download the whole original into the API pod on every call.
Severity:      P2
Category:      input
Evidence level: VERIFIED-READ
Evidence:      No HTTP rate-limit middleware in media-service (00-map §3.1; cmd/server/main.go:440-457 only
               recovery, request id, logger, metrics). POST /v1/media/:id/frames streams the original
               (up to 16 GiB by default, MEDIA_WORKER_MAX_ORIGINAL_BYTES absent in prod) to the server's
               disk and runs ffprobe + up to 10 ffmpeg runs per call, with no status check
               (service/frames.go:28-58); the server has 1 CPU / 1 GiB limits
               (values-prod.yaml:25-31). Audio track uploads are read whole into memory
               (audio_tracks.go:439-462); POST /v1/audio/voiceover reads the file with io.ReadAll and no
               size check in the handler (http/audio_handler.go:219-237). Only the upload-init budget
               (10/h, 30/day) and the gateway's 60 rps/user exist.
How to reproduce: Upload one large video, then call POST /v1/media/<id>/frames in a loop from one
               account; watch pod disk and CPU.
Impact:        One account can exhaust media-service disk, memory or CPU and take down playback for
               everyone (playback authorization runs in the same pods).
Fix:           Per-user Redis limits (the existing upload budget helper) on frames, extract-frame,
               cover-frame, audio-track writes and voiceover; extract frames from the 720p rendition
               rather than the original; cap the voiceover body with http.MaxBytesReader.
Acceptance:    Handler tests: the N+1th call in the window answers 429; voiceover over the cap 413.
Effort:        S
```

```
ID:            REELS-B-7
Title:         Destructive media internal routes (delete any asset, anonymize, reprocess, sound takedown) accept the one shared internal key with no per-caller service token.
Severity:      P2
Category:      authz
Evidence level: VERIFIED-READ
Evidence:      http/handler.go:193-230: the /v1/media/internal group uses RequireInternalKey only;
               DELETE /:mediaId, POST /:mediaId/anonymize, /reprocess, /sound, sound takedown, fingerprint
               add at most refuseGatewayIdentity. By contrast image-bytes, document-bytes and recording
               import also require a signed service token (:234-244). The same key is stamped by the
               gateway and held by every service (feed Track B §1.4).
How to reproduce: With the internal key, DELETE /v1/media/internal/<any media id> naming a referrer
               the store accepts, or POST .../sounds/<id>/takedown.
Impact:        Compromise of any one service holding the key lets it delete or silence any user's media.
Fix:           Require a service token with an op per route (post-service for delete/sound,
               group-service for anonymize, admin-service for takedown/reprocess) using the existing
               requireServiceCaller pattern.
Acceptance:    Handler tests: the bare key answers 403 on each route; the named caller's token succeeds.
Effort:        M
```

```
ID:            REELS-B-8
Title:         The web /v1 proxy buffers every request body whole with no size cap, before authentication.
Severity:      P2
Category:      input
Evidence level: SUSPECTED (Node and hosting limits not measured)
Evidence:      src/app/api/proxy/[...path]/route.ts:99-102 `await req.arrayBuffer()` for any non-GET; the
               comment at :83-98 states nothing else bounds it (route handlers have no body limit; the
               middleware is excluded from /v1).
How to reproduce: Send a few concurrent unauthenticated POSTs of several GB to /v1/anything on the web
               host; watch the Next process memory.
Impact:        Anyone can crash the web front end (and with it signed-in web upload and playback).
Fix:           Reject when Content-Length exceeds 64 MiB + headroom and stream the body to the gateway
               instead of buffering.
Acceptance:    Proxy unit test: a 65 MiB body answers 413 without calling upstream.
Effort:        S
```

```
ID:            REELS-B-9
Title:         The web proxy forwards the browser's X-User-Id header to the gateway.
Severity:      P3
Category:      authn
Evidence level: VERIFIED-READ
Evidence:      src/app/api/proxy/[...path]/route.ts:33-36 FORWARDED_HEADERS includes "x-user-id"; safe
               today only because the gateway strips it (feed Track B §1.1). If API_GATEWAY_URL ever
               points at a service directly, it is an identity forgery (B-1 makes media-service trust it).
Fix:           Remove "x-user-id" from the list (the X-Scopes comment at :56-59 already states the rule).
Acceptance:    Proxy test: an incoming X-User-Id is not sent upstream.
Effort:        S
```

```
ID:            REELS-B-10
Title:         The media cookie reaches every state-changing /v1/media route, contrary to its comment; CSRF protection rests on SameSite=Lax alone.
Severity:      P3
Category:      input
Evidence level: VERIFIED-READ
Evidence:      src/app/api/auth/_lib/session.ts:48-54,119-128: access_token, httpOnly, Secure in production,
               SameSite=Lax, path /v1/media, "no state-changing route ever receives it". Path /v1/media
               covers POST init/confirm/frames/cover-frame/audio-tracks/resumable and DELETE /v1/media/:id;
               media-service accepts the cookie as a token (middleware.go:159-166). Lax blocks cross-site
               POST/DELETE; a same-site subdomain is not blocked.
Fix:           Accept the cookie only on GET/HEAD in media-service's AuthMiddleware (writes need the bearer).
Acceptance:    Middleware test: POST with cookie and no Authorization answers 401.
Effort:        S
```

```
ID:            REELS-B-11
Title:         Many 500 responses return the raw Go error text to the client.
Severity:      P3
Category:      input
Evidence level: VERIFIED-READ
Evidence:      media-service http/handler.go:555, renditions_handler.go:61, clips_handler.go:112,137,167,210,
               audio_handler.go:239; post-service http/drafts.go:70,145,174,200, reel_discovery.go:37,74,89.
               Store errors can carry SQL or constraint text and internal hostnames.
Fix:           Log err and answer a fixed message, as audio_handler.go:184 already does.
Acceptance:    Handler test with a failing store: body contains no error text.
Effort:        S
```

```
ID:            REELS-B-12
Title:         GET /v1/reels/slug/:slug answers the reel id for any slug with no visibility check.
Severity:      P3
Category:      authz
Evidence level: VERIFIED-READ
Evidence:      post-service/internal/http/reel_discovery.go:43-60 (public, no viewer passed to
               LookupSlugRedirect). The id alone does not unlock content (every read is gated), so this
               confirms existence of private or deleted reels only.
Fix:           Pass the viewer and answer 404 unless viewerMayViewPost allows the reel.
Acceptance:    Handler test: a private reel's slug answers 404 to a stranger.
Effort:        S
```

```
ID:            REELS-B-13
Title:         media-service's JWT check accepts a token with no exp and checks neither issuer nor audience.
Severity:      P3
Category:      authn
Evidence level: VERIFIED-READ
Evidence:      internal/http/middleware.go:147-149 (`if claims.Exp > 0 && ...`), no iss/aud claims parsed
               (:19-26); HS256 with the shared JWT_SECRET still accepted (:109-119,
               values-prod.yaml:51-56). Reached only for write routes, and only after the gateway's own
               check for edge traffic; matters for in-cluster callers (B-1).
Fix:           Require exp, and check iss and aud as the gateway does; drop HS256 once RS256 is everywhere.
Acceptance:    Middleware tests: a token without exp, or with a foreign aud, answers 401.
Effort:        S
```

---

## 3. Checked and found sound (for re-verification)

| Item | Evidence |
|---|---|
| One gate for every byte and derivative read; unknown key prefix is protected; `user/...` keys are all protected | `delivery/signer.go:56-85`, `delivery/authz.go:501-674` |
| Non-owner needs media `passed`/`approved` moderation; pending, manual_review, rejected, unknown all refused | `post-service/internal/service/story_surface.go:252-269` |
| Non-owner playback: post approved, not deleted, not scheduled, author visible (private account, hidden/deactivated author), no block either way, no mute, audience (followers, friends, private share list), 18+ gate, members-only entitlement; unreferenced media denied; failures unresolved = 503, never cached allow | `post-service/internal/service/media_access.go:94-142,242-327,455-493`, `story_surface.go:302-388` |
| Signed-out rule: exactly public, approved, live, unscheduled, not 18+, not members-only, public visible author | `media_access.go:161-184`, `delivery/authz.go:243-255` |
| Video original never served to a non-owner, and to the owner only while its ETag matches (no swap after scan) | `service/media_batch.go:223-263` |
| Map OQ6: `/renditions`, `/v1/subtitles/:id`, `/track/:lang`, `/status` all go through the gate; draft caption tracks owner only; transcript `/status` gated | `http/renditions_handler.go:28-37`, `http/clips_handler.go:124-141,324-388` |
| Sounds answered to the source video's audience; retired sounds only via a live carrier post | `http/audio_handler.go:84-156`, `service/record_read.go:60-122` |
| Redirects `private, max-age=60` with `Vary`; playlists `private, no-store`; caption track `private, max-age=300` | `http/handler.go:654-669,724-725`, `clips_handler.go:385` |
| Production cannot start without the CloudFront signer, POST_SERVICE_URL, internal key, S3 via IRSA (no static keys) | `cmd/server/main.go:157-175,279-286,528-571,632-641`, scanner refuses static AWS keys (:646-647) |
| Upload: server-chosen key; presigned PUT 15 min with Content-Type and Content-Length signed; confirm checks owner, key, size, stored type, 64-byte magic; ETag pinned; MIME allow-list has no SVG or HTML | `service/upload.go:131-300`, `service/validation.go:53-57` |
| Worker: ffmpeg/ffprobe with `-protocol_whitelist file`, demuxer from magic, deadlines, size/scratch caps, duration/resolution/fps limits after probe, concat with `-protocol_whitelist file` | `processing/tool.go:17-196`, `processing/concat.go:103-110,224`, `cmd/worker/main.go:634-721` |
| GPS and other metadata stripped from MP4 renditions, HLS and thumbnails; image bomb guard on display images | `processing/video.go:136-145,205-226`, `processing/hls.go:110,128`, `processing/display_image.go:72-77` |
| Bucket private, SSE-KMS, CloudFront OAC with signed URLs (key group) on the default behaviour | `infra/terraform/modules/media/main.tf:31-129,274-319` |
| Account purge removes every asset the user uploaded (media is a required purge participant) | `media-service/cmd/server/main.go:224-235`, `auth-service/internal/purge/worker.go:44` |
| Offline copies issue no file URL; grant requires view rights + allow_download (or owner) + membership; revoked on check when refused | `post-service/internal/service/offline_copies.go:202,386,586-673` |
| Analytics: actor from the gateway, never the body; server-side ownership and duration; watched_ms clamped; self-views excluded from paid; paid views capped 1/viewer/day | `analytics-service/internal/service/ingest.go:152-242,377-599`, `aggregation/daily.go:23-34` |
| Report queue reads and updates need the admin token or gateway-derived admin scope; reel moderation reads need the moderator scope | `trust-safety-service/internal/http/handler.go:238-330`, `admin_token.go:253-266`, `post-service/internal/http/moderator_gate.go:19-42` |
| Sound search parameterised and page-capped | `store/postgres/audio.go:276-284`, `service/audio_reads.go:53` |
| Web: no `dangerouslySetInnerHTML` in reels, video shell, upload, post or reels app routes (hits only in qa, communities, groups, root layout); Action button links are https-only | grep over `src/`; `features/action-button/links.ts:29` |
| JWT algorithm pinned to HS256/RS256 by kid, no `none` | `media-service/internal/http/middleware.go:104-134` |
| Secret scan (pattern grep for private keys, AWS access keys, Google API keys, OpenAI keys, excluding the forbidden folders): only a fake `AKIA…` fixture in `scripts/smoke/dev-smoke.test.mjs:80,85` and the vendored SDK; prod secrets come from External Secrets by reference | `deploy/services/media-service/values-prod.yaml:38-81` |
| Android: Banuba licence, Maps keys, Google OAuth client id and the GIF key are read from `.secrets/` at build time into BuildConfig (client keys by design; restrict them by package and signing certificate at the provider) | `build-logic/convention/.../BanubaLicenseConventionPlugin.kt:34`, `ProjectExtensions.kt:277-319`, `GoogleSignInConventionPlugin.kt:43`, `AndroidApplicationConventionPlugin.kt:43`, `feature/chat/build.gradle.kts:32` |

Commands run (VERIFIED-RUN of the gate rules' existing unit tests, nothing else):

```
cd Architecture/services/post-service  && go test -count=1 -run 'MediaAccess|AnonymousMay|EvaluatePostMedia' ./internal/service/
ok  github.com/atpost/post-service/internal/service  0.255s
cd Architecture/services/media-service && go test -count=1 -run 'ClassForKey|AuthorizeAsset|Gate' ./internal/delivery/
ok  github.com/atpost/media-service/internal/delivery  2.410s
```

## 4. Out of scope observations

- `trust-safety-service` `GET /v1/media-labels/:mediaId` has no role check (`trust_extras_handler.go:200`); no gateway prefix was found, so in-cluster only.
- `dangerouslySetInnerHTML` on user content in web `app/qa/questions/[id]/page.tsx:158`, `components/communities/CommunityPostCard.tsx:312`, `CommunityWikiTab.tsx:119`, `CommunityQASettingsTab.tsx:167` (not reels; not traced for sanitising).
- `POST /v1/media/studio/stickers/:id/use` is public and starts a goroutine per call (`studio_handler.go:28,194`).

## 5. Not verified

- **Search and notifications for reels** (map OQ16): whether a pending, private, followers-only, deleted or blocked author's flick is indexed or removed, and whether notifications are suppressed on delete or block. Needs a read of the search-service consumer and notification-service.
- **Feed read path for reels** (`/v1/feed/reels`, related, discovery fill from `/v1/posts/recent`): relied on feed Track B and the map; the reels-specific filters (`dropShortForm`, category page) were not re-read.
- **Analytics read routes** (`/v1/analytics/content/:id[/views]`, `/creator/me`, `/dashboard/*`) owner check; analytics events' IP / user-agent fields; whether `play_end` without `play_start` is accepted (B-5).
- **Report filing**: body fields, target existence and visibility checks, reporter quota numbers and fail-open behaviour (`trust-safety-service/internal/service/reporter_quota.go`).
- **Mass assignment on reel create, edit and draft bodies** beyond feed Track B's create-struct read: AI label clearing, `allow_download`, `content_type`, `publish_at` in the past, mention caps.
- **Repost of a flick** (map OQ10) and `/v1/posts/by-author/:id/counts` leaking private or pending reel counts.
- **Android**: release cleartext policy, OkHttp logging level, token storage (EncryptedSharedPreferences or plain DataStore), offline copies unencrypted in `noBackupFilesDir` (map §5.7, one reader) and their removal on logout; caption and link rendering. Not read here.
- **Web**: CSP and security headers in `next.config.ts` / middleware; `storageRoute.ts` redirect-target validation (dev-only, map §5.5); gateway CORS prod origins.
- **ffmpeg behaviour for B-3**: whether the alpine:3.19 ffmpeg auto-probes any demuxer that follows external references for a `.m4a`/`.mp3`-named file. Needs a sandboxed run.
- **CloudFront**: whether the edge serves a cached object after the S3 object is deleted for a still-valid signature (expected yes, bounded by the signature, B-4); custom-domain wiring (`media_custom_domain_enabled` default false while prod values use `media.cleestudio.com`).
- **Gateway internals for media prefixes** (strip, no key stamp, body cap numbers, query-token paths) were taken from the map and feed Track B, not re-read.
- **Secrets in git history and container images**: the pattern grep covered the working tree only; gitleaks/trivy results belong to Stage 1.
