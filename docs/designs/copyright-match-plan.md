# Copyright Match — plan (for founder approval)

Prepared 29 September 2026 from a read-only survey of media-service, post-service, trust-safety-service, admin-service, monetization-service, notification-service and the web Creator Hub. Reference behaviour (function only): YouTube's Copyright Match Tool — detect full or near-full re-uploads of a creator's videos on other channels; the creator reviews matches, archives them or requests removal; only uploads made after the original are considered; each match surfaces once; access for channels that filed a valid removal request or are in the creator programme; abuse of removal requests costs access.

## What exists and what does not

- **Nothing to fingerprint with today**: no perceptual hashing, audio fingerprinting, pgvector or embeddings anywhere; Postgres is `postgis/postgis:16-3.4` without a vector extension. No copyright / takedown workflow exists.
- **Reusable**: ffmpeg in the media worker (`Dockerfile.worker`, alpine 3.19); every accepted video has a 360p rendition (`MinVideoResolution = 360`); the DB-polled job pattern with `FOR UPDATE SKIP LOCKED` and claim tokens (`media_caption_jobs`, `media_audio_tracks`); the media outbox → `media.events` → post-service consumer; post-service's signed-capability moderation route (`POST /v1/posts/internal/moderation`, `post_moderation_decisions`); trust-safety's audited reports / appeals / strikes / grievances and the admin-console proxy (`admin-service/internal/http/handler_trust.go`).
- **Dead code not reused**: `media_rights_checks` / `rights.go` in post-service and the `CopyrightCheck` / `CopyrightClaim` web types (uploader-side shape; a possible v2 pre-publish check).
- **Existing bug found**: `post-service/internal/service/post_drafts.go:494-501` (`authorStandingOK`) checks strike severities `ban|suspend|suspension|severe`, which the `trust.user_strikes` CHECK (`warning|strike|severe_strike`) can never hold — so strikes currently enforce nothing anywhere.

## Who owns what

| Service | Owns |
|---|---|
| media-service | fingerprints, the index, matched pairs (media facts only; no audience decisions) |
| post-service | `copyright_matches`, visibility/eligibility/ownership filtering, "surfaces once", archive, the Matches API, moderation source `copyright` |
| trust-safety-service | tool access and abuse revocation, removal requests (from a match or a web form), counter-notices, restore timer, copyright strikes, audit, copyright events via an outbox |
| monetization-service | a new internal "is this creator in the programme" read |
| admin-service + console | the review queue (permissions + step-up) |
| notification-service | notices to claimant and respondent |

## Fingerprinting (our infrastructure, no paid APIs)

- A separate DB-polled job in the media worker, enqueued in the same transaction as a successful transcode and re-enqueued on reprocess; behind `COPYRIGHT_FINGERPRINT_ENABLED`. It never delays "ready" or fails an upload.
- One ffmpeg pass streams the 360p rendition by presigned URL: 1 frame/s at 64×64 grey, plus mono 11 kHz audio.
- **Video**: trim dark borders (handles letterbox/pillarbox), 32×32 DCT → 64-bit pHash per second; drop black/solid/fade and static-repeat frames.
- **Audio**: chromaprint `fpcalc` if it installs on alpine 3.19 (to verify), else an in-house Haitsma-Kalker sub-fingerprint. Used for confidence (high when audio agrees) and as the only signal for low-motion videos (slides, podcasts).
- **Index**: 4×16-bit bands of one hash per 5 s window in Postgres, partitioned by log-duration bucket (multi-index hashing).
- **Match rule (v1)**: offset voting on index hits, then frame-by-frame verification (Hamming ≤ 10/64, gaps ≤ 3 s): the original is ≥ 85 % covered, the copy ≥ 60 % (excludes compilations), ≥ 10 s and ≥ 8 informative frames matched. Direction = upload time.
- **Survives in v1**: re-encodes, resolution/frame-rate changes, brightness/colour, small logos, letterboxing, head/tail trims ~10–15 %, replaced audio. **Later**: mirroring, larger crops, speed changes, partial/compilation matches. **Out of scope**: picture-in-picture, camcorder, heavy edits.
- **Cost**: ~15–30 CPU-seconds per 10-minute video, ~45–75 KB stored, no external spend. The worker is limited to 1 CPU, so backfill runs throttled at low priority.

## Matches, privacy, removal

- **Matches tab** in Creator Hub (new "Copyright" item): your video vs the matched video, channel, public/unlisted, date, views, "~92 % of your video", confidence; Archive / Unarchive; Request removal; your removal requests; notices on your own videos (counter-notice).
- **Never shown**: private / followers / close-friends / staged / scheduled / unapproved / deleted videos; your own uploads; remixes of your video. A match disappears if the copy goes private and reappears (without a new notice) if it returns.
- **Eligibility**: in the creator programme, or has had a removal request upheld, or granted by an admin — and not revoked. Without access, the tab explains and links to a public removal-request form (the way a non-programme channel becomes eligible).
- **Removal request** (legal name, contact, owner/agent, declarations, signature — encrypted) → human review in the admin queue. **The platform never decides ownership and never removes automatically.** Approval (with step-up) rejects the copy through the existing moderation route with source `copyright`, issues a 90-day strike and notifies both sides.
- **Counter-notice** by the uploader → accepted by T&S → forwarded to the claimant → the video is restored after a waiting period unless the claimant records legal action. Ordinary appeals are refused for copyright removals.
- **Anti-abuse**: ≤ 10 new requests / day, ≤ 50 open, ≤ 20 open against one channel; an abusive request or 3 invalid ones in 90 days revokes access automatically; reinstatement is admin-only and audited.

## Phases

0. **Prerequisites** — verify fpcalc on the worker image; relax the media outbox's one-event-per-type rule for pair events; add `trust_safety:copyright.*` permissions; **fix the strike-severity bug** so strikes actually enforce.
1a. **Shadow** (2–4 weeks) — fingerprint new and existing videos, record pairs, show nothing; label a sample to measure false positives and tune thresholds.
1b. **v1** — matches and surfacer in post-service; access, requests, review, takedown, strike, counter-notice, restore, withdraw, abuse revocation in trust-safety; admin queue; notifications; Hub Copyright page and the public form; all behind a `copyright_match` flag.
2. **Later** — mirror/crop/speed robustness, partial matches, a courtesy notice with a 7-day scheduled removal, licensed-channel allowlist, daily digest, an uploader-side pre-publish check, a dedicated index if the catalogue grows past ~10^5–10^6 hours.

## Decisions needed from the founder (legal input marked)

1. Approve the plan and the order (strike bug first, then shadow mode).
2. Who is eligible at launch: creator programme + upheld requests + admin grants (recommended), or everyone.
3. Show **unlisted** copies as matches? They are reachable by link but not listed; YouTube shows them. **(legal)**
4. Counter-notice waiting period before restoring, per country — e.g. US 10–14 business days, India 21 days. **(legal)**
5. What the uploader sees about the claimant (name only, or name + contact). **(legal)**
6. Strike policy: does a copyright strike count toward suspension, and after how many?

Full technical detail (tables, routes, events, test strategy, risks, file paths) is in the research report this summary was drawn from; it is kept with this file's history in the session notes and will be expanded into lane briefs on approval.
