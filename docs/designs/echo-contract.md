# Echo contract (phase 1 + basic For You)

Pinned 2026-11-12. Design and decisions: [short-posts-design.md](short-posts-design.md).
Every lane builds against this page. If a lane needs to change it, it stops and says so.

## Switches (all default OFF)

| Where | Switch | Off means |
|---|---|---|
| post-service | `ECHO_ENABLED` | every `/v1/notes*` route answers `503 ECHO_NOT_ENABLED` |
| feed-service | `ECHO_ENABLED` | `/v1/feed/notes/*` answers `503 ECHO_NOT_ENABLED`; fan-out to `notes_timeline` still runs |
| Android | `Product.ECHO` (`"echo"`, label "Echo") through the existing product kill switches | no app-switcher entry and no Home shortcut |
| Web | the same product switch the web already reads for closed products | `/echo` shows the closed-product page |

## Storage (post-service)

A note is a `posts` row with `content_type='note'`. The migration:
- adds `'note'` to the one `chk_content_type` constraint (drop and re-add it, following migration 044's rule);
- adds `reply_to_id`, `conversation_id`, `reply_policy`, `reply_count`, `quote_of_id` and `edited_at`;
- adds the three partial indexes described in design §5.2.

Every note is written with `distribution={"version":1,"main_feed":false}`.

## Rules enforced by the server

| Rule | Value | Error |
|---|---|---|
| Text | 1–500 runes after trim (a note with images may have empty text) | `400 NOTE_TOO_LONG` / `400 EMPTY_NOTE` |
| Media | at most 4 images, or exactly 1 GIF (image/gif); never video or audio | `400 TOO_MANY_MEDIA` / `400 MEDIA_NOT_ALLOWED` |
| Top-level note, thread, quote | needs a live `premium` role. Read through `GET /v1/auth/internal/roles/:userId`, cached 60 s per user. Fails closed. | `403 PREMIUM_REQUIRED` / `503 PREMIUM_CHECK_UNAVAILABLE` |
| Reply | needs good standing (the existing standing check), an account at least 7 days old, and the target's `reply_policy` passing: `everyone`; `following` = the note's author follows the replier; `mentioned` = the replier is in the note's mentions. The note's author may always reply. Blocks deny everything. | `403 ACCOUNT_TOO_NEW` / `403 REPLY_NOT_ALLOWED` |
| Reply / quote target | must be a visible, non-deleted note | `404 NOTE_NOT_FOUND` |
| Thread | 2–25 entries, each obeying the text and media rules; premium | `400 INVALID_THREAD` |
| Rate limits (env-tunable) | notes, threads and quotes: 30 per hour and 200 per day (`ECHO_NOTES_PER_HOUR`, `ECHO_NOTES_PER_DAY`); replies: 60 per hour for Premium, 20 per hour for everyone else (`ECHO_REPLIES_PER_HOUR`, `ECHO_REPLIES_PER_HOUR_FREE`) | `429 RATE_LIMITED` |
| Edit | `PATCH` text within 30 minutes of creation, by the author only; sets `edited_at`. `reply_policy` can change at any time. | `403 EDIT_WINDOW_CLOSED` / `403 NOT_AUTHOR` |
| Visibility | always `public` in phase 1. Private accounts follow the existing account-visibility gate. | — |

Notes are **not monetizable**, and they appear in **no** existing list: Home, Reels, Videos, by-author, hashtag pages, trending, recent, the creator summary or ordinary post search. Each of those read paths gets a test.

## Routes (gateway prefix `/v1/notes` goes to post-service)

All bodies and answers use the existing `api` envelope.

| Method and path | Auth | Body or query | Answer |
|---|---|---|---|
| `POST /v1/notes` | signed in, `Idempotency-Key` header | `{text, media_ids?:[uuid], reply_to_id?, quote_of_id?, reply_policy?:"everyone"\|"following"\|"mentioned"}` (`reply_to_id` and `quote_of_id` are mutually exclusive) | `201 Note` |
| `POST /v1/notes/thread` | signed in, `Idempotency-Key` | `{entries:[{text, media_ids?}], reply_policy?}` | `201 {notes:[Note]}` |
| `GET /v1/notes/:id` | optional (signed-out works for public notes) | — | `200 Note` |
| `GET /v1/notes/:id/replies` | optional | `?cursor=&limit=` (default 20, max 50) | `200 {items:[Note], next_cursor}` |
| `GET /v1/notes/:id/conversation` | optional | — | `200 {items:[Note]}`, from the top note down to the parent of `:id` |
| `PATCH /v1/notes/:id` | author | `{text?, reply_policy?}` | `200 Note` |
| `DELETE /v1/notes/:id` | author | — | `204` (the existing soft delete and restore) |
| `GET /v1/notes/users/:userId` | optional | `?tab=notes\|replies\|reposts&cursor=&limit=` | `200 {items:[Note], next_cursor}` |
| `GET /v1/notes/me/eligibility` | signed in | — | `200 {can_post:bool, can_reply:bool, reason:""\|"premium_required"\|"account_too_new"\|"suspended"}` |
| `POST/DELETE /v1/posts/:id/like`, `POST/DELETE /v1/posts/:id/repost` (`type=plain`) | existing | existing | existing. A quote **is** `POST /v1/notes` with `quote_of_id`; the server also writes the `quote` repost row. |
| `GET /v1/feed/notes/following` | signed in | `?cursor=&limit=` | `200 {items:[Note], next_cursor}`, newest first |
| `GET /v1/feed/notes/for-you` | signed in | `?cursor=&limit=` | `200 {items:[Note], next_cursor}`, ranked |

Premium purchase uses the routes that already exist (`/v1/monetization/premium/*`, monetization-service).

Two codes added while building (12 Nov):
- `503 ACCOUNT_CHECK_UNAVAILABLE`: the account-age read failed; it fails closed, like the Premium check.
- `400 INVALID_REQUEST`: covers both `reply_to_id` and `quote_of_id` set, a bad `reply_policy` or `tab`, and duplicate media ids.

Media errors reuse the existing post create codes: `403 MEDIA_NOT_OWNED`, `400 MEDIA_NOT_FOUND`, `400 MEDIA_NOT_READY`. Person verification uses the identity routes from commit `9fe3d2245`.

### `Note` (JSON)

This is the existing post JSON that `GET /v1/posts/:id` returns (same field names, so clients reuse their post model), with these fields always present:

```json
{
  "id": "uuid", "author_id": "uuid", "content_type": "note", "text": "…",
  "media": [ … existing post media items … ],
  "created_at": "RFC3339", "edited_at": null,
  "reply_to_id": null, "conversation_id": "uuid", "quote_of_id": null,
  "reply_policy": "everyone",
  "reply_count": 0, "like_count": 0, "repost_count": 0,
  "viewer_has_liked": false, "viewer_has_reposted": false, "viewer_can_reply": true,
  "author": {"id":"uuid","username":"…","display_name":"…","avatar_url":"…","is_verified":false,"verification_level":"","badge_flags":0},
  "quoted_note": null
}
```

- `conversation_id` equals `id` for a top-level note.
- `quoted_note` is one level deep: a quoted note that is itself a quote has `quoted_note: null`.
- `author` is filled the same way post-service fills author info today. If post-service has no author hydration, clients hydrate from profiles, just as they do for posts, and the field is omitted.
- A deleted or hidden target reads as `quoted_note: null` and `reply_to_id` kept.

Golden fixtures live in `Architecture/services/post-service/testdata/contracts/notes/`: `note.json`, `note_list.json`, `eligibility.json`, `error_premium_required.json`. Web and Android copy them byte for byte.

## Events (`social.events.v1`, through the post-service outbox)

| Event | Payload |
|---|---|
| `PostCreated` | the existing payload, `content_type:"note"`, `main_feed:false`, plus `reply_to_id`, `conversation_id` and `quote_of_id` (omitted when empty) |
| `note.replied` (new) | `{note_id, conversation_id, reply_to_id, actor_id, recipient_id, created_at}`. `recipient_id` is the author of `reply_to_id`; nothing is emitted when actor equals recipient. |
| `note.quoted` (new) | `{note_id, quote_of_id, actor_id, recipient_id, created_at}`, with the same self rule |
| `user.mentioned`, `PostReacted`, `post.reposted`, `PostDeleted` and friends | unchanged; notes ride them |

Constants go in `Architecture/shared/events/events.go`: `EventNoteReplied = "note.replied"`, `EventNoteQuoted = "note.quoted"`, plus their payload structs.

## feed-service

- On `PostCreated` with `content_type='note'` **and** an empty `reply_to_id` (replies never go to timelines):
  - fan out to the followers' rows in a new Scylla table `notes_timeline` (the same shape as the home timeline);
  - authors with more than `ECHO_FANOUT_MAX_FOLLOWERS` followers (default 100000) are not fanned out and are pulled at read time.
- Delete, purge and unfollow handling mirrors the home timeline.
- Notes never enter the home timeline reads. Existing `main_feed=false` handling already keeps them out of Home. Add a test.
- **For You**, with the existing ranking package plus blocks, mutes and keyword filters. Candidates:
  1. Following;
  2. notes liked or reposted by people you follow (from `PostReacted` / `post.reposted`);
  3. public top-level notes from the last 48 hours with the highest velocity.

## notification-service

- `note.replied` sends type `note_reply`: "{actor} replied to your note".
- `note.quoted` sends type `note_quote`: "{actor} quoted your note".
- Both respect the existing per-user notification preferences and block checks. Push deep link: `momentum://echo/note/{note_id}`; web `/echo/note/{note_id}`.

## search-service

Leave notes out of ordinary post search: filter on `content_type != 'note'` in the existing query. A separate notes search comes later.
