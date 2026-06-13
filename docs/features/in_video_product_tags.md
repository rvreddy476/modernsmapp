# In-video product tags (affiliate overlay)

End-to-end documentation for the TikTok-style affiliate overlay that
ships across post-service, monetization-service, commerce-service,
postbook-ui, and mobile/atpost_app.

## What it does

A creator places a tappable product card on top of their reel. While
the video plays, the card is rendered at the configured (X%, Y%) of
the player viewport during the configured time window. A viewer who
taps the card:

1. Is sent through `/v1/commerce/affiliate/:linkId` which resolves
   the affiliate link → product + code, 302s to
   `/products/:id?via=<affiliate_code>`.
2. Lands on the product detail page; the client persists the
   affiliate code in session storage so it survives the cart +
   checkout hops.
3. When the order is placed, the affiliate code is forwarded to
   monetization-service which records the conversion + commission
   for the creator who tagged the video.

## Services touched

```
post-service        owns the tag rows + impression / click counters
monetization-service owns affiliate_links, conversions, payouts
commerce-service     owns the product catalog + the public redirect
postbook-ui          renders the overlay + composer (web)
mobile/atpost_app    renders the overlay + composer (Flutter)
```

## API surface

### post-service

```
POST   /v1/posts/:postId/product-tags                    creator: place tag
GET    /v1/posts/:postId/product-tags                    public: list tags
DELETE /v1/posts/:postId/product-tags/:tagId             creator: remove tag
POST   /v1/posts/:postId/product-tags/:tagId/impression  player: view fired
POST   /v1/posts/:postId/product-tags/:tagId/click       player: tap fired
GET    /v1/creators/:creatorId/product-tags              creator: analytics
```

Validation gates on POST (handled server-side; clients don't
pre-check):

- Caller must be the post's author (post.author_id == X-User-Id).
- The affiliate link must exist, be active, and be owned by the
  caller (cross-service validator hits monetization-service).
- Position values, when set, must be in [0, 100].
- time_start_ms <= time_end_ms when both set.

### monetization-service

```
GET  /v1/monetization/affiliate/links/:linkId   internal-only;
                                                used by post-service
                                                validator + commerce
                                                redirect resolver
GET  /v1/monetization/affiliate/links           creator: list own links
POST /v1/monetization/affiliate/links           creator: create a link
GET  /v1/monetization/affiliate/:linkCode       public: look up by
                                                short code (legacy
                                                viewer-link flow)
```

### commerce-service

```
GET  /v1/commerce/affiliate/:linkId             public: 302 redirect →
                                                /products/:id?via=<code>
GET  /v1/commerce/products/:productId/preview   public: compact
                                                product card (composer
                                                picker)
```

## Data model

`post_product_tags` (post-service / app DB) — see
`Architecture/services/post-service/database/migrations/020_post_product_tags.sql`.

| Column            | Type   | Notes                                       |
| ----------------- | ------ | ------------------------------------------- |
| id                | UUID   | primary                                     |
| post_id           | UUID   | which video                                 |
| affiliate_link_id | UUID   | cross-service ref (no FK)                   |
| creator_id        | UUID   | denormalised for delete authz               |
| time_start_ms     | INT    | nullable — null = "from the beginning"      |
| time_end_ms       | INT    | nullable — null = "until the end"           |
| position_x        | REAL   | 0–100 percent; nullable = player picks      |
| position_y        | REAL   | 0–100 percent; nullable = player picks      |
| label             | TEXT   | cached at create time                       |
| image_url         | TEXT   | cached at create time                       |
| impression_count  | BIGINT | dedup'd at SETNX layer; bumped via UPDATE   |
| click_count       | BIGINT | sibling                                     |
| is_active         | BOOL   | soft-delete flag                            |
| created_at        | TZ     |                                             |
| updated_at        | TZ     |                                             |

CHECK constraints (defence-in-depth, mirrored on the handler side):

- `position_x BETWEEN 0 AND 100` when set
- `position_y BETWEEN 0 AND 100` when set
- `time_end_ms >= time_start_ms` when both set

Indexes:

- UNIQUE partial on `(post_id, affiliate_link_id) WHERE is_active`
- `(post_id, is_active) WHERE is_active` for the player read path
- `(creator_id, created_at DESC) WHERE is_active` for analytics

## Anti-fraud + performance

### Per-IP dedup on impressions / clicks

`Architecture/services/post-service/internal/service/product_tag_dedup.go`

A 30-second viral video lets one viewer fire ~10 impressions
(overlay re-mounts each loop). Bots can hit `/click` in a tight
loop. Mitigation:

- SETNX on `ptg_dedup:<imp|click>:<tagID>:<ipHash>` with TTL.
- Impression window 1h, click window 15m.
- IP hashed (sha256, first 128 bits) — never persisted raw (PII).
- Fail-open on Redis errors; the gateway H5 fleet rate limit is
  the upstream flood guard.

### Validator cache

`Architecture/services/post-service/internal/service/affiliate_validator.go`

- 5-minute TTL on the positive (linkID, callerID) →
  (creatorID, listingID) result keyed in Redis.
- Negative cases (not found / inactive / not owned) are NOT cached
  — the validator is the commission-fraud gate and a creator
  reactivating their link should be effective immediately.
- `InvalidateAffiliateValidatorCache(linkID)` for the
  monetization.affiliate.link_changed event consumer (Kafka
  wire-up still TODO; the helper is ready).

### Fail-closed cross-service validation

- post-service validator returns errors (not "allow") when
  monetization is unreachable. The handler maps the unreachable
  case to 500 so a creator tagging someone else's affiliate link
  during a transient outage is rejected.
- commerce-service redirect resolver returns 503 when monetization
  is unreachable. Better to surface "try again" than to land the
  viewer on an unresolved redirect.

## Web (postbook-ui)

| File                                                            | Role                              |
| --------------------------------------------------------------- | --------------------------------- |
| `src/hooks/useProductTags.ts`                                   | Query + mutations + emitters      |
| `src/hooks/useAffiliateLinks.ts`                                | Composer link picker + preview    |
| `src/hooks/useAffiliateAttribution.ts`                          | sessionStorage attribution glue   |
| `src/features/posttube/components/ProductTagOverlay.tsx`        | Player-side overlay rendering     |
| `src/features/posttube/components/ProductTagComposer.tsx`       | Creator composer modal            |
| `src/features/posttube/components/ProductTagComposerButton.tsx` | Author-gated trigger              |
| `src/features/posttube/components/VideoStage.tsx`               | Wires currentTimeMs into overlay  |
| `src/features/reels/components/ReelPlayer.tsx`                  | Exposes onTimeUpdateMs callback   |
| `src/app/products/[productId]/page.tsx`                         | Reads ?via=, shows affiliate badge|
| `src/app/creator/product-tags/page.tsx`                         | Creator analytics dashboard       |

### Attribution flow

```
overlay tap
  → emitProductTagClick (best-effort POST)
  → window.location.assign('/v1/commerce/affiliate/<linkId>')
  → server 302 → /products/<id>?via=<code>
  → product page mounts
  → useAffiliateAttribution captures ?via= → sessionStorage
  → user adds to cart, navigates, checks out
  → checkout payload reads readAffiliateAttribution()
  → on success → clearAffiliateAttribution()
```

## Mobile (mobile/atpost_app)

| File                                                              | Role                                  |
| ----------------------------------------------------------------- | ------------------------------------- |
| `lib/data/models/product_tag.dart`                                | Tag model                             |
| `lib/data/models/affiliate_link.dart`                             | AffiliateLink + ProductPreview models |
| `lib/data/repositories/product_tags_repository.dart`              | post-service surface                  |
| `lib/data/repositories/affiliate_links_repository.dart`           | monetization + commerce surfaces      |
| `lib/providers/product_tags_provider.dart`                        | Riverpod tag-list provider            |
| `lib/services/affiliate_attribution.dart`                         | In-memory attribution                 |
| `lib/shared/widgets/product_tag_overlay.dart`                     | Player-side overlay                   |
| `lib/shared/widgets/video_player_widget.dart`                     | Throttled onPositionUpdate (10Hz)     |
| `lib/features/reels/product_tag_composer_sheet.dart`              | Composer modal bottom sheet           |
| `lib/features/reels/product_tag_composer_button.dart`             | Author-gated trigger pill             |
| `lib/features/commerce/affiliate_redirect_screen.dart`            | Deep-link redirect handler            |
| `lib/app/router.dart`                                             | `/commerce/affiliate/:linkId` route   |

### Attribution flow

```
overlay tap
  → emitClick (best-effort)
  → GoRouter.push('/commerce/affiliate/<linkId>')
  → AffiliateRedirectScreen calls /v1/commerce/affiliate/<linkId>
    with followRedirects=false
  → reads Location header → captures ?via= into
    AffiliateAttribution → routes to /commerce/product/<id>
  → checkout reads ref.read(affiliateAttributionProvider).code
  → on success → notifier.clear()
```

## Operator runbook

### Adding a new creator to the program

1. Creator opens monetization settings → creates an affiliate link
   for a product (existing flow).
2. Creator opens one of their reels → taps "Tag products" →
   composer modal/sheet → picks the link → places it.
3. Tag goes live immediately. Validator cache is bypassed (positive-
   only; new link is fresh).

### Invalidating a tag

- Creator-driven: open composer → "Remove" next to the tag (soft
  delete; counters preserved for historical analytics).
- Admin-driven (e.g. ToS violation): direct UPDATE on
  `post_product_tags SET is_active = FALSE WHERE id = ?` —
  reads stop within 5 minutes (no overlay-side cache beyond that).

### Investigating "this overlay isn't showing"

1. `GET /v1/posts/:postId/product-tags` — is the tag in the active
   list? If not → it was deleted or never created.
2. Check `time_start_ms` / `time_end_ms` against the viewer's
   playhead. Outside the window → expected hidden state.
3. Check `position_x` / `position_y` — values outside [0, 100]
   would have been rejected at create time, so this is unlikely.
4. Check viewer client logs for `emitProductTagImpression` 4xx /
   5xx — counter is stuck but overlay should still render.

### Investigating "commissions aren't crediting"

1. Order payload — does it carry `affiliate_code`?
   - If no → attribution layer didn't capture or didn't survive
     to checkout.
   - If yes → monetization-service `affiliate_conversions` table.
2. `SELECT * FROM affiliate_conversions WHERE order_id = ?` — was
   a row created? Status:
   - `pending` → awaiting payment success
   - `confirmed` → ready for payout
   - `paid` → in a settled payout batch
   - `reversed` → buyer refunded; commission reversed

### Per-IP dedup cleared

- Redis `KEYS ptg_dedup:imp:*` shows impression dedup. TTL 1h.
- Redis `KEYS ptg_dedup:click:*` shows click dedup. TTL 15m.
- The IP hashes are sha256 → first 128 bits hex. Can't be
  reversed; safe to dump for debugging.

## Env vars

| Service               | Var                       | Notes                                    |
| --------------------- | ------------------------- | ---------------------------------------- |
| post-service          | `MONETIZATION_SERVICE_URL` | Validator target                        |
| post-service          | `INTERNAL_SERVICE_KEY`    | Forwarded to monetization                |
| commerce-service      | `MONETIZATION_SERVICE_URL` | Redirect resolver target                |
| commerce-service      | `INTERNAL_SERVICE_KEY`    | Forwarded to monetization                |
| mobile / web          | (none specific to this feature) — uses the existing api base URL |

## Known follow-ups

Captured in commit bodies; consolidated here:

- Drag-to-place placement UI on web + mobile composer (currently
  numeric inputs / sliders).
- Order-create wiring to forward sessionStorage / provider via= into
  the commerce checkout payload + Conversion creation.
- monetization.affiliate.link_changed Kafka event consumer in
  post-service to invalidate the validator cache.
- Product-update event consumer to refresh cached `label` / `image_url`
  on the tag when the underlying listing changes.
- Server-side enrichment of `image_url` from the linked listing's
  media-service URL (composer sends label from the preview but image
  needs media-service resolution).
- ProductTagComposer wiring on the standalone PostTube watch screen
  (web + mobile) — only reels has the trigger today.
- Redis-batched counter flush worker — current impressions and
  clicks do per-event UPDATE; fine through ~10k events/min, would
  bottleneck on the row at higher fan-out.
- Drag handle accessibility audit.
