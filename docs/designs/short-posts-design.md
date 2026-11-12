# Short posts ("Echo"): a premium, X/Threads-style surface

Status: **approved 2026-11-11, all 14 defaults taken (see section 9).** Echo ships after launch; only the phase 0 prerequisites are built now, switched off.
Paths are relative to the repository root. "Echo" is a working name (see decision 1);
the internal content type is `note` so the product name can change without a migration.

## 0. What the founder asked for

Two kinds of posting:

1. **The feed we have today.** Anyone can post. It stays as it is.
2. **A new short-post product with its own surface.** "Tweet it and post it", the way
   Instagram has Threads. Only **premium members** can post there, and accounts that
   post **"official matters"** must be **verified**.

## 1. What already exists (read from the code)

The short version: most of the posting side already exists. The **premium** and
**verified** sides do not, and they are the expensive part.

### 1.1 Posting machinery we can reuse (post-service)

| Need | What exists | Where |
|---|---|---|
| A post "kind" | `content_type` with one CHECK constraint holding the whole set: `post, poll, voice, flick, long_video, reel, video, video_embed, flick_embed` | `Architecture/services/post-service/database/migrations/044_one_content_type_constraint.sql:61-72`; allowlist in `internal/service/post_content_types.go:38-41` |
| Threads (one author, several entries) | 2–25 entries, up to 5,000 characters each, created atomically and idempotently; only the first entry goes to the main feed | `database/migrations/027_threads.sql`, `internal/service/threads.go:17-38`; routes `POST /v1/posts/thread`, `GET /v1/posts/:postId/thread` (`internal/http/handler.go:178-179`) |
| Keeping a kind out of the social Home feed | `posts.distribution` policy with `main_feed=false`; feed-service records it for every post and the Home read filters on it | `internal/service/distribution.go:10-50`; `Architecture/services/feed-service/internal/events/consumer.go:450-476` |
| Heart-only likes | Posts are heart-only (founder rule 24 Oct) | `internal/http/heart_only.go`; `POST /v1/posts/:postId/like` |
| Repost and quote | `plain` and `quote` reposts; quote text capped at 500 characters | `internal/http/reposts_handler.go:23-35, 39-97`; migration `015_post_reposts.sql` |
| Mentions that respect privacy | `who_can_mention` / `who_can_tag` checked through graph `/can`; a disallowed mention is dropped | `internal/service/mention_tag_privacy.go:19`, `pkg/graphclient/client.go:187`; rules in `Architecture/services/graph-service/internal/permission/permission.go:455-469` |
| Block and private accounts | A block in either direction hides everything; `account_visibility=private` limits to followers | `graph-service/internal/permission/permission.go:189-190, 436-444`; `post-service/internal/service/privacy_gate.go:22`; `feed-service/internal/service/privacyfilter.go:174` |
| Standing (suspended accounts cannot post) | Checked first on create, fails closed (403 suspended, 503 when trust-safety is unreachable) | `internal/service/post_create.go:55`; `cmd/server/main.go:206-234`; rule in `trust-safety-service/internal/service/standing.go:21-22` (3 strikes in 90 days, or 1 severe) |
| Spam heuristics | 3 regex patterns, >5 URLs, >30 posts/hour; score >0.95 rejected, >0.7 held for review | `internal/spam/detector.go`; applied at `internal/service/post_create.go:475-484` |
| Posting rate limits | 20 posts/hour, 50/day (environment-tunable) | `internal/service/ratelimit.go:27-29` |
| Text ceiling on ordinary posts | 5,000 characters | `internal/service/create_guards.go:81` |
| Reports | Target `post` (and `comment`, `user`, …), 12 reasons plus `scam_link` and copyright | `trust-safety-service/internal/service/moderation.go:20-83`; `POST /v1/reports` |
| Notifications | `PostCreated`, `PostReacted` (batched likes), `CommentCreated`, `user.mentioned`, `post.reposted`, `UserFollowed` on `social.events.v1` | `notification-service/internal/events/consumer.go:234-440`; names in `Architecture/shared/events/events.go:18-33, 240, 342` |
| Search | `PostCreated` indexed into OpenSearch `posts_v1` with its content type, only when `SearchEligible` passes | `search-service/internal/events/consumer.go:531-638` |
| Fan-out timelines | Fan-out on write to a Scylla home timeline that stores each row's content type; per-kind reads such as Reels and Videos filter by type | `feed-service/internal/store/scylla/timelines.go:405-471`; `internal/service/feed.go:681, 855` |
| Ranking | Ranker, scorer, diversity, author penalty, cold start, topical signals, velocity | `feed-service/internal/ranking/*`, `internal/pipeline/velocity.go` |
| Gateway | `/v1/posts` and `/v1/feed` already routed; anonymous requests pass through and each service decides; the gateway sets `X-User-Id` and `X-Scopes` from the verified token | `Architecture/services/api-gateway/cmd/server/main.go:468-580, 1015-1073` |
| Android on/off switch per product | `Product` list with kill switches, shown in the app switcher | `mobile/android/core/model/src/main/kotlin/com/us/android/core/model/ProductFeatures.kt` |

**Gaps on the posting side:**

- **Replies.** The existing `thread_*` columns are for one author's own chain. There is no
  place for **someone else's reply that is itself a post**. Comments are a separate table
  and carry the 6-emoji reactions.
- **Notifications.** There is no notification for quotes. A `comment.replied` template
  exists, but nothing produces the event (`notification-service/internal/service/templates.go:41`).
- **Text classifier.** Trust-safety has none. Keyword filters are per-user mute lists only.

### 1.2 Premium: nothing platform-wide exists

- **No platform premium.** There is no subscription tier for viewers anywhere in
  user-service, payments-service or monetization-service.
- **Creator memberships are per-creator.** They do not mean premium.
  - Memberships pay a creator (`monetization-service`, `MEMBERSHIPS_ENABLED`,
    `cmd/server/main.go:60`).
  - The money goes through the payments-service application `memberships`, reference
    `creator_membership` (`payments-service` migration `016_memberships_application.sql`).
  - Renewal is a reminder plus a fresh checkout, not an automatic charge
    (`monetization-service/internal/workers/memberships.go:1-21`).
- **Dating Premium** is the only consumer premium. It is sold as one-off 30/90/365-day
  passes with no auto-renew (`dating-service/internal/payments/catalogue.go:10-26`).
  This is the pattern to copy.
- **No recurring billing.** payments-service has no mandates or auto-debit.
- **No Google Play Billing**, anywhere.

### 1.3 Verified: the columns exist, but nothing sets them

- **The columns exist.**
  - `profile.profiles.is_verified`, `verification_level` and `badge_flags`
    (`identity-platform/services/auth-service/database/setup.sql:343, 356-357`).
  - `usr.users.is_verified` (`setup.sql:259`).
  - All of them are returned in the public profile
    (`identity-platform/services/profile-service/internal/http/public_profile_dto.go:60-61`).
  - Search indexes `is_verified` (`search-service/internal/store/search/mappings.go:188`).
- **Nothing ever sets them to true**, with one exception: approving a business page
  (`Architecture/services/user-service/internal/store/business_pages.go:300`).
- **The token does not carry verification.** It holds only `sid`, `scopes` and `typ`
  (`auth-service/internal/service/auth.go:306-321`).
- **Android has no badge.** It shows a plain "Verified" text label
  (`mobile/android/feature/profile/.../ProfileScreen.kt:908-914`).
- **Identity checks are scattered.** DigiLocker clients live separately in dating, doorstep,
  food and rider (`internal/digilocker` in each), and face compare lives in
  `rider-service/internal/service/facecompare.go`.
  - **No service owns a person-level "identity verified" status.**
  - `Architecture/shared/kyc` only validates formats (PAN, GSTIN).

### 1.4 Roles: a ready place to hold "premium"

- **Where roles live.** `auth.user_roles` has `app`, `expires_at` and `reason` columns
  (`auth-service/database/setup.sql:98-174`).
  - Ecosystem roles are granted by services through `POST /v1/auth/internal/roles`
    using `Architecture/shared/identityroles` (`auth-service/internal/http/internal_roles.go:103-130`).
  - Roles are read back with `GET /v1/auth/internal/roles/:userId` (`:168`).
  - Roles reach the token's `scopes` on the next refresh. The gateway forwards them as `X-Scopes`.
- **Known hole, a blocker before we add a role anyone can buy.** The comment at
  `internal_roles.go:36-42` says a moderator can reach the internal grant route through the
  gateway, because `/v1/auth/internal` is missing from `routepolicy.ForbiddenPrefixes`.

## 2. Product shape

| Topic | Recommendation |
|---|---|
| Name | **Echo** (a repost is "echoing"). Alternatives: **Buzz**, **Wire**, **Say**. "Pulse" is taken by Dating's mechanics, and "Spark" by Dating's Super Spark. |
| Length | **500 characters** per note (the same as Threads, and as our quote limit). Longer thoughts use a thread of up to 25 notes, from the thread code that already exists. |
| Media | **Text-first.** Up to **4 images** or **1 GIF** per note in phase 1, plus link previews. **Video comes later**: it pulls in the transcode, copyright-match and audio pipelines and would turn Echo into a third video surface. |
| Replies | **A reply is a note**, as on X and Threads. It has its own page, can be liked, reposted and quoted, and builds a conversation tree. Replies are **heart-only** like every post. The 6-emoji reactions stay on comments, and Echo has no comments. |
| Who may reply | The author chooses per note: **Everyone** (default) / **People you follow** / **Only people you mention**. Graph rules apply on top, so a block hides everything both ways. |
| Likes | Heart only (24 Oct rule). |
| Repost | Plain repost: yes (the feed keeps repost, and Echo behaves like the feed). Quote: yes, and a quote is itself a note. |
| Edit | Within **30 minutes**, with an "Edited" mark. Phase 3. |
| Timelines | Two tabs: **Following** (newest first, notes from people you follow) and **For You** (ranked). Following is the default on first open. |
| Separation from the feed | Notes **never appear in social Home, Reels or PostTube**, and feed posts never appear in Echo. A note can be shared into chat, and a "Share to Feed" card can come later. |
| Profile | An **Echo tab** on the profile: Notes, Replies, Reposts. |
| Android | Android is the super app, so Echo is complete on Android. It gets **its own `Product` entry** in the app switcher (with a kill switch) plus a shortcut on the Home top bar. **Not a bottom-bar tab at launch**: that bar already holds Home, Reels, Friends, Me, Messages and Explore (`app/.../navigation/TopLevelDestination.kt:46-51`). |
| Web | `postbook-ui` route **`/echo`**: a centre timeline with side panels (trends, who to follow). That is the big-screen card layout the founder asked for, not a ported phone screen. The composer is a box at the top of the timeline. |

## 3. Who can post

### 3.1 What "premium" means

There is no platform premium today, so we have to create one. Recommendation:

- **One platform plan, "Momentum Premium".** It is not tied to a creator. It unlocks
  posting on Echo now and can carry other perks later.
- **Sold as passes:** 30 days and 365 days, the same pattern as Dating Premium.
  - Payment goes through **payments-service**, as a new application `premium`, reference
    `platform_premium`.
  - Access opens **on the signed payment event** (founder rule: nothing manual).
  - **Renewal is a reminder with a pay button**, like memberships. Auto-renew (UPI Autopay
    mandates) comes later, because payments-service has no mandates today.
- **Owner: monetization-service.** It already runs memberships, reminders and grace
  periods. It keeps `premium_plans` and `premium_periods`, and on activation or expiry it
  **grants or revokes an identity role `premium` with `expires_at`** through
  `shared/identityroles`. That keeps the founder's rule that identity owns every role
  (2026-09-07).
- **Android and Google Play.** Selling a digital subscription inside an Android app is
  likely to fall under Google Play's payments policy (Play Billing, or Google's alternative
  billing programme where it applies). The same question is already open for memberships.
  It must be answered before Android sells Premium. Until then, Android can send people to
  web checkout only if the policy allows that; this needs checking.

### 3.2 Enforcing the gate on the server

| Option | How it works | Problem |
|---|---|---|
| Token claim only | post-service reads `premium` in `X-Scopes` | The token lives until it expires, so a lapsed member can keep posting for one access-token lifetime. |
| Live check only | post-service asks on every create | One more network call per post. |
| **Recommended: both** | Clients use the scope to **show** the composer. post-service **decides** with a live read of `GET /v1/auth/internal/roles/:userId`, honouring `expires_at`, cached for **60 seconds** per user and **failing closed** (503) like the standing check. | None worth the risk. |

**Where the gate applies:**

- **New top-level notes, threads and quotes** need premium.
- **Replies** follow decision 6. Recommended: anyone signed in, in good standing, with an
  account older than 7 days.
- **Likes and plain reposts** are open to everyone signed in.
- **Deleting your own notes** is always allowed.

Answers: `403 PREMIUM_REQUIRED`, `403 VERIFICATION_REQUIRED`, and the existing `403`
suspended answer. A cheap `GET /v1/notes/me/eligibility` returns
`{can_post, can_reply, reason}` so clients can show the right sheet ("Get Premium",
"Verify your organisation") before the person types anything.

### 3.3 When premium lapses

- **Existing notes stay up and readable.** Deleting them would punish readers and break
  links.
- **No new notes, threads or quotes** until the person renews. They can still read, like,
  repost, reply (when replies are open to them), delete and edit within the window.
- **Grace period:** 3 days, the same as memberships, before the role expires.
- **Badges:** a paid mark disappears. An identity tick does not, because identity is not
  paid for (see 3.4).

### 3.4 Premium and verified are different things

On X, selling the blue tick made "paid" and "verified" look the same, which confused
people and helped impersonators. We keep them apart:

| Mark | Meaning | How it is earned | Stored as |
|---|---|---|---|
| (none, or a small "Premium" label) | Paid for Premium | Payment | identity role `premium` |
| **Verified** (tick) | A real, unique person | **DigiLocker** name and date of birth, plus **selfie** liveness and face match. Auto-approved (founder rule 19 Sep). One verified account per person, enforced by a keyed hash of the DigiLocker identity. The display name must match the DigiLocker name. | `profile.profiles.is_verified=true`, `verification_level='person'`, `badge_flags` bit |
| **Official** (a different colour) | An organisation, brand, media outlet or public body | GSTIN or company registration checked with a provider, a domain e-mail on the organisation's own domain, and a verified person as the authorised signatory. Auto-approved when all of these are machine-checked. One human review fallback for hand-uploaded documents only (founder rule). Reuses the business-page approval (`user-service/internal/store/business_pages.go:300`). | `verification_level='organisation'` |
| **Government** (grey) | A government body or official | Invitation-only, two-person admin approval | `verification_level='government'` |

**How "official matters must be verified" works:**

- **We cannot detect what a note is about.** So the rule is about the account: any
  account that **presents as** an organisation, brand, newsroom, public office or public
  figure must hold the Official (or Government) mark to post on Echo.
- **The rule is enforced in four ways:**
  1. The Official account type cannot be chosen without the checks above.
  2. **Impersonation check at sign-up and at every name change.** Display names and
     handles that closely match a verified or official name are held. Changing the
     display name of a verified account removes the tick until the name is checked again.
  3. A new report reason, **`impersonation`**.
  4. A note from an Official account carries the organisation name on the note.

**A new building block:** person verification has to live in **identity-platform** (one
owner, readable by every service). The DigiLocker client moves from the four per-service
copies into one shared package. Dating, Doorstep, Feast and Mopedu can switch to it later.
Nothing in them has to change for Echo.

## 4. Reading

| Who | Can do |
|---|---|
| Signed out | Open a **single note page** and a **profile's Echo tab** for public accounts. This matches the 29 Sep decision that public videos play signed out, and gives shared links a polished landing (launch rule 21 Oct). **No timelines, no search** signed out. |
| Signed in, not premium | Read both timelines, like, repost, reply (decision 6), report, mute, block. |
| Premium | All of the above, plus post, thread and quote. |
| Private account | Notes visible to approved followers only, the same as posts today. Replies to a private account's notes are visible only to people who can see the note. |

## 5. Architecture

### 5.1 Recommendation: a new kind inside post-service

**Option A, recommended: `content_type='note'` in post-service.**

What we get for free:

- Standing check, spam detector, rate limiter
- Mentions with `who_can_mention`, block and privacy gate through graph `/can`
- Heart likes, plain and quote reposts
- Threads, reports (target `post`), search indexing (`ContentType` on the document)
- Outbox events, the purge and restore lifecycle, moderation and admin routes
- Hashtags and trending

**Option B: a new `echo-service`.**

- Its one advantage is clean isolation.
- It would have to repeat about ten integrations that took months to harden: standing,
  spam, privacy gate, mentions, reposts, reports, search events, purge, moderation routes
  and the admin console's post tools.
- **Rejected:** more code, more ways to get security wrong, no product benefit.

**The real cost of A:** every place that lists posts must leave notes out. `main_feed=false`
already keeps them out of social Home. But these read paths must be checked one by one,
with a test each:

- By-author lists (`GET /v1/posts/by-author/:authorId` and `/counts`)
- Hashtag pages, trending posts, recent, bookmarks
- PostTube and Reels feeds (already filtered by type)
- Search results
- Profile grids, the creator summary
- Monetization (`CanonicalMonetizationType('note')` must be "not monetizable")

This audit is the biggest single server task. It is listed as its own lane.

### 5.2 Data model (post-service, one migration)

```sql
-- add 'note' to the ONE constraint (044's rule: drop and re-add chk_content_type, never a second name)
ALTER TABLE posts DROP CONSTRAINT IF EXISTS chk_content_type;
ALTER TABLE posts ADD CONSTRAINT chk_content_type CHECK (content_type IN (
  'post','poll','voice','flick','long_video','reel','video','video_embed','flick_embed','note'));

ALTER TABLE posts
  ADD COLUMN IF NOT EXISTS reply_to_id      UUID,          -- the note this replies to (another author's, or own)
  ADD COLUMN IF NOT EXISTS conversation_id  UUID,          -- the top note of the conversation
  ADD COLUMN IF NOT EXISTS reply_policy     TEXT NOT NULL DEFAULT 'everyone'
       CHECK (reply_policy IN ('everyone','following','mentioned')),
  ADD COLUMN IF NOT EXISTS reply_count      INT  NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS quote_of_id      UUID,          -- a quote note points at what it quotes
  ADD COLUMN IF NOT EXISTS edited_at        TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_posts_note_replies
  ON posts (reply_to_id, created_at) WHERE content_type = 'note' AND reply_to_id IS NOT NULL AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_posts_note_conversation
  ON posts (conversation_id, created_at) WHERE content_type = 'note' AND deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_posts_note_author
  ON posts (author_id, created_at DESC) WHERE content_type = 'note' AND deleted_at IS NULL;
```

**How existing features fit:**

- **Quotes.** A quote is written as a `note` row with `quote_of_id` **and** a `quote`
  repost row, so repost counts, `post.reposted` and the reposters list keep working.
- **Threads.** These reuse `thread_root_id/thread_seq` with `content_type='note'`. The
  500-character cap applies to each entry.
- **Distribution.** Every note is written with `distribution={"version":1,"main_feed":false}`.

**Note-specific guards** (in a new `internal/service/note_create.go`, beside
`create_guards.go`):

- 500 runes, at most 4 images or 1 GIF, no video.
- A reply's target must be a visible note.
- `reply_policy` is checked against graph (`following` = the author follows the replier;
  `mentioned` = the replier is in the target note's mentions).
- Premium and age checks, as described in 3.2.

### 5.3 API (all through the gateway)

New gateway prefix: `/v1/notes` goes to post-service. Add it to `routeDefinitions()` in
`api-gateway/cmd/server/main.go:468`. `/v1/users` is not proxied to post-service
(`reposts_handler.go:33-34`), so profile lists live under `/v1/notes`.

| Method and path | Service | Purpose |
|---|---|---|
| `POST /v1/notes` | post | Create a note, reply (`reply_to_id`) or quote (`quote_of_id`). Needs an `Idempotency-Key`. |
| `POST /v1/notes/thread` | post | Create a thread of notes (wraps `CreateThread`) |
| `GET /v1/notes/:id` | post | One note, with its author's badge. Signed-out readable for public notes. |
| `GET /v1/notes/:id/replies?cursor=` | post | Direct replies, ranked (3.4 / 5.5) |
| `GET /v1/notes/:id/conversation` | post | The parent chain up to the top note |
| `PATCH /v1/notes/:id` | post | Edit within 30 minutes (phase 3); change `reply_policy` |
| `DELETE /v1/notes/:id` | post | The existing delete and restore path |
| `GET /v1/notes/users/:userId?tab=notes\|replies\|reposts` | post | Profile Echo tab |
| `GET /v1/notes/me/eligibility` | post | `{can_post, can_reply, reason}` |
| `POST/DELETE /v1/posts/:id/like`, `/repost` | post | Existing routes, unchanged |
| `GET /v1/feed/notes/following?cursor=` | feed | Following timeline |
| `GET /v1/feed/notes/for-you?cursor=` | feed | Ranked timeline |
| `GET /v1/premium/plans`, `POST /v1/premium/checkout`, `GET /v1/premium/me` | monetization | Premium passes (checkout returns a payments-service order) |
| `POST /v1/identity/verification/person/start`, `…/selfie`, `GET …/status` | identity (profile-service) | DigiLocker and selfie person verification |
| `POST /v1/identity/verification/organisation` | identity | Official mark application |
| `POST /v1/reports` | trust-safety | Existing; add reason `impersonation` |

### 5.4 Events (all on the existing `social.events.v1` through the outbox)

| Event | Producer | Consumers |
|---|---|---|
| `PostCreated` with `content_type='note'`, plus new fields `reply_to_id`, `conversation_id`, `quote_of_id` | post | feed (Echo timelines), search (index, with a `note` filter in results), notification |
| `note.replied` (new) | post | notification: "X replied to your note" |
| `note.quoted` (new) | post | notification: "X quoted your note" (no quote notification exists today) |
| `user.mentioned`, `PostReacted`, `post.reposted` | post (existing) | unchanged |
| `premium.activated` / `premium.lapsed` (new) | monetization | notification (receipts, "renew in 3 days"); identity role grant and revoke happen through the identityroles outbox |
| `identity.verification.changed` (new) | identity | search (re-index the user's `is_verified`), feed (drop the cached badge), post (impersonation list) |

### 5.5 Timelines and ranking

- **Following.** Keep the fan-out on write that already exists, with one change:
  - Today the home timeline mixes every kind, and a per-kind read walks the buckets until
    it finds enough rows of that kind (`collectByContentTypes`, `timelines.go:457-471`).
  - Notes will be much rarer than other posts for most readers, so that walk would be slow.
  - **So notes fan out to their own Scylla table, `notes_timeline`,** with the same shape
    as the home timeline.
  - Accounts with very large followings (say over 100,000 followers) are **pulled at read
    time** instead of pushed, the usual celebrity split.
- **For You.** The candidates are:
  1. Following
  2. Notes liked or reposted by people you follow
  3. Notes on hashtags you follow (hashtag follows already exist, migration `074`)
  4. Fast-rising notes (the `pipeline/velocity.go` signal)

  These are scored by the existing `ranking` package (recency decay, engagement velocity,
  author diversity and penalty, cold start), with every block, mute and keyword filter
  feed-service already applies.
- **No paid ranking boost.** Premium buys posting rights, not reach. The one exception is
  in **reply ordering**, where replies from Verified people and from accounts the reader
  follows rank first. That is where spam collects on X.

## 6. Safety and launch fit

### 6.1 Moderation and abuse

- **Moderation load.** Text is cheap to post and fast to spread. The premium gate is the
  main spam control, because each account costs money. Even so, we have **no text
  classifier**.
  - **Phase 1:** keep the existing regex spam detector. Add link checking with the Web
    Risk check the Action button already uses (`WEB_RISK_API_KEY`). Route reported notes
    to the existing moderation queue.
  - **Phase 3:** add a toxicity and hate classifier to trust-safety, which also helps the
    feed and comments.
- **Rate limits** (post-service, environment-tunable):

  | Who | Limit |
  |---|---|
  | Premium | 30 notes per hour, 200 per day |
  | Replies (all users) | 60 per hour |
  | Non-premium replies | 20 per hour |
  | Accounts under 7 days old | Cannot reply |

  The gateway's global 60 requests per second per user stays as the outer wall.
- **Verification abuse.**
  - One verified person per DigiLocker identity.
  - The display name is locked to the DigiLocker name for the tick.
  - Changing the name removes the tick until it is checked again.
  - Official marks need checks against government or registry data.
  - A strike for impersonation removes the mark and Premium without a refund.
- **The security fix must ship first.** `/v1/auth/internal` must be added to
  `routepolicy.ForbiddenPrefixes`, or a moderator could grant `premium` (or any ecosystem
  role) through the gateway.
- **Copyright.** Being text-first avoids the video copyright pipeline. Images go through
  the existing media pipeline.

### 6.2 Fit with the "launch four apps" rule (21 Oct)

Echo is posting, so it belongs to **Social**, one of the four. But it drags in two
platform pieces that do not exist and that touch **money** and **identity**: Premium
billing (plus the Google Play question) and person and organisation verification. Those
are the slow, risky parts.

| Option | What ships | Cost and risk |
|---|---|---|
| **A. Inside Social at launch** | Echo, Premium and Verification together | About 5–7 extra weeks of lanes on the launch path. It adds a new billing product and a new identity flow right before launch. Launch moves unless other Social work is cut. |
| **B. First app after launch (month 1)** (recommended) | Launch as planned. Build Echo's prerequisites (security fix, verification, Premium) during launch hardening, behind switches, then ship Echo as the first monthly app. | No launch delay. Verification and Premium are tested on real users before they gate anything. |
| C. Inside Social at launch, gated on **verification only** (free), Premium added later | Echo plus person verification | About 3–4 extra weeks. No billing at launch. It changes the founder's "premium only" rule for the first weeks. |

## 7. Phased plan

Sizes: S = up to 2 days, M = up to 1 week, L = 1–2 weeks of one lane. Android lanes run
one at a time (founder rule).

| Phase | Lane | Work | Size |
|---|---|---|---|
| **0. Prerequisites** | Server: security | Add `/v1/auth/internal` to the forbidden prefixes; test that a moderator token cannot grant roles | S |
| | Server: identity | One shared DigiLocker package; person verification in identity (start, callback, selfie liveness and face match, unique-person hash); setter for `is_verified`, `verification_level`, `badge_flags`; `identity.verification.changed` event | L |
| | Server: premium | monetization `premium_plans`/`premium_periods`, payments-service application `premium`, signed-event activation, reminder renewal, 3-day grace, role grant and revoke with `expires_at`, `PREMIUM_ENABLED` switch | M |
| | Web + Android | Verify-me flow, Premium purchase sheet, one badge component on both (replacing Android's "Verified" text label) | M + M |
| **1. Echo core** | Server: post | Migration, `note` kind, note guards, replies and reply policy, quotes as notes, eligibility route, premium live check, `/v1/notes` routes, gateway prefix, new events | L |
| | Server: read-path audit | Exclude `note` from every existing list (5.1), with a test per path; search filter; not monetizable | M |
| | Server: feed | `notes_timeline` fan-out, Following route, large-account pull | M |
| | Server: notifications | `note.replied`, `note.quoted`, Premium receipts and reminders | S |
| | Android | `Product` entry, Following timeline, composer (text, 4 images, GIF, thread), note page with replies, profile Echo tab, eligibility sheets | L |
| | Web | `/echo` centre timeline, composer, note page, profile tab, signed-out note page with rich link preview | L |
| **2. For You** | Server: feed | Candidate sources and ranking, reply ordering | M |
| | Android + Web | For You tab, "Not interested", mute words | S + S |
| **3. Trust and polish** | Server | Official / Government marks (registry checks, signatory, review fallback), impersonation name check, `impersonation` report reason, edit within 30 minutes, text classifier | L |
| | Android + Web | Official application flow, edit, badge on every surface | M + M |
| **Later** | | Video in notes, UPI Autopay renewal, Play Billing (if required), "Share to Feed", Echo trends page | — |

**Rough total:**

| Phase | Lane-weeks | Calendar weeks (server, web and Android in parallel, one Android lane at a time) |
|---|---|---|
| 0 | about 4 | 2–3 |
| 1 | about 6 | 3–4 |
| 2 + 3 | about 5 | 3 |

## 8. Decisions for the founder

Each has a recommended default. Answering "go with the defaults" is enough to start
phase 0.

1. **Name.** Default: **Echo**. Alternatives: Buzz, Wire, Say.
2. **When.** Default: **B, the first app after launch.** Prerequisites are built now
   behind switches. A = at launch; C = at launch with verification-only posting.
3. **What Premium is.** Default: **one platform plan, "Momentum Premium"**, sold as
   30-day and 365-day passes through payments-service, with a renewal reminder and no
   auto-renew at first. Founder to set the prices.
4. **Google Play.** Default: **check Google Play's payments policy before Android sells
   Premium.** Until then, sell on the web, and on Android only in the way the policy allows.
5. **Does posting need Premium only, or Premium plus a verified identity?** Default:
   **Premium only** for ordinary people. Verification is optional and gives the tick.
   Accounts that present as an organisation or public body need the Official mark.
6. **Who can reply, like and repost.** Default: **everyone signed in can like and
   repost.** Replies are open to anyone in good standing whose account is over 7 days old.
   Only Premium can start notes, threads and quotes. The author can limit replies per note.
7. **Replies are notes, heart only.** Default: **yes.** There are no comments and no
   6-emoji reactions on Echo, which matches the 24 Oct rule.
8. **Length and media.** Default: **500 characters**, threads up to 25 notes, **up to 4
   images or 1 GIF**, and no video at first.
9. **When Premium lapses.** Default: **notes stay up.** The person can't post new notes
   until they renew. There is a 3-day grace period, and the identity tick stays.
10. **Badges.** Default: **three marks:** Verified person (tick), Official organisation
    (a different colour) and Government (grey). Premium alone shows **no tick**.
11. **Signed-out reading.** Default: **single notes and profiles are readable signed out**
    (for shared links). Timelines and search need sign-in.
12. **Reach.** Default: **no paid ranking boost.** Verified and followed accounts' replies
    rank first under a note.
13. **Kept apart from the feed.** Default: **notes never appear in the social feed, Reels
    or PostTube.** "Share to Feed" comes later.
14. **Where on Android.** Default: **its own app-switcher entry plus a Home top-bar
    shortcut, not a bottom tab.**

## 9. Decided (11 Nov 2026)

The founder accepted all 14 defaults above as written. Echo is the first app after launch (option B);
phase 0 prerequisites (gateway security fix, Momentum Premium behind `PREMIUM_ENABLED`, person
verification) are built now, switched off.
