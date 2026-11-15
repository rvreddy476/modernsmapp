# Echo v2 contract: the X-style app (pinned 14 Nov 2026)

Founder decisions (14 Nov): Echo on the web is **its own X-style app**, not a page inside VChat.
Version 1 has **all** of: views, bookmark, share; Explore with trends; GIF, poll, emoji and schedule
in the composer; the post page with View quotes. Reference walked in the founder's Chrome: x.com.
Phase 1 rules ([echo-contract.md](echo-contract.md)) still hold unless this page changes them.
If a lane needs to change this page, it stops and says so.

Limits copy X's, so nothing here is invented: poll 2–4 choices, 25 characters each, open 5 minutes to
7 days; schedule 5 minutes to 18 months ahead.

## 1. Note JSON: new fields (always present)

```json
{
  "view_count": 0,
  "bookmark_count": 0, "viewer_has_bookmarked": false,
  "quote_count": 0,
  "status": "published",
  "scheduled_at": null,
  "poll": null,
  "thread_parts": 0
}
```

`thread_parts` (added 16 Nov 2026): on the first note of a thread, how many
live parts the thread has, the first included (`3` → "Thread · 3 parts");
`0` on every other note, later thread parts included.

`poll`, when the note has one:

```json
{
  "options": [{"index": 0, "label": "Yes", "votes": 12}, {"index": 1, "label": "No", "votes": 3}],
  "total_votes": 15,
  "ends_at": "RFC3339",
  "closed": false,
  "viewer_vote": null
}
```

- Vote counts are shown to everyone only after the viewer voted, or once the poll has closed, or to the
  note's author. Otherwise each `votes` is `null` and `total_votes` is still a number (X behaviour).
- `status` is `published` or `scheduled`. Scheduled notes are visible **only to their author**, and only
  through `/v1/notes/me/scheduled`.

## 2. Routes (all under the existing gateway prefix `/v1/notes`, post-service)

| Method and path | Auth | Body or query | Answer |
|---|---|---|---|
| `POST /v1/notes` (extended) | as today | adds `poll?: {options:[string], duration_minutes:int}` and `scheduled_at?: RFC3339`. A poll note may not have media (X rule); a reply may not be scheduled. | `201 Note` (status `scheduled` when `scheduled_at` is set) |
| `POST /v1/notes/:id/poll/vote` | signed in | `{option_index:int}` | `200 Note`. One vote per person, final; voting on a closed poll or own poll is refused. |
| `GET /v1/notes/me/scheduled` | signed in | `?cursor=&limit=` | `200 {items:[Note], next_cursor}`, soonest first |
| `PATCH /v1/notes/:id/schedule` | author | `{scheduled_at}` | `200 Note` |
| `DELETE /v1/notes/:id/schedule` | author | — | `204`. Deletes the scheduled note. |
| `POST /v1/notes/:id/bookmark` / `DELETE` | signed in | — | `200 {bookmarked:bool, count:int}` |
| `GET /v1/notes/bookmarks` | signed in | `?cursor=&limit=` | `200 {items:[Note], next_cursor}`, newest bookmark first |
| `POST /v1/notes/views` | optional (signed out is accepted and not counted) | `{note_ids:[uuid]}` (max 50) | `204`. A signed-in viewer counts once per note per 24 hours; the author never counts. |
| `GET /v1/notes/:id/quotes` | optional | `?cursor=&limit=` | `200 {items:[Note], next_cursor}`, newest first |
| `GET /v1/notes/trends` | optional | `?limit=` (default 10, max 30) | `200 {items:[{tag, note_count, rank}]}`: hashtags from public, published, top-level notes in the last 24 hours, ranked by distinct authors then notes; a tag needs at least 3 distinct authors |
| `GET /v1/notes/hashtags/:tag` | optional | `?tab=top\|latest&cursor=&limit=` | `200 {items:[Note], next_cursor}` |
| `GET /v1/notes/search` | optional | `?q=&tab=top\|latest&cursor=&limit=` | `200 {items:[Note], next_cursor}`. Text match on public published notes (Postgres full text is fine for v1). |

New error codes: `400 INVALID_POLL`, `400 POLL_WITH_MEDIA`, `400 INVALID_SCHEDULE`, `409 POLL_CLOSED`,
`409 ALREADY_VOTED`, `403 OWN_POLL`, `404 NOTE_NOT_FOUND` (reused).

Existing routes already cover: like, repost (plain), quote (POST with `quote_of_id`), replies, conversation,
profile tabs, Following / For You feeds.

## 3. Server rules

- **Scheduled notes** are stored with `status='scheduled'` and are absent from every read path except
  the author's scheduled list. A worker publishes them when due (claim with `FOR UPDATE SKIP LOCKED`,
  safe on every replica). `PostCreated`, fan-out, mention and hashtag work happen **at publish**, not at
  create. Publishing re-checks Premium and standing; a failed check leaves it `scheduled` with a
  `publish_error` the author sees, and it is retried hourly for 24 hours, then dropped to `failed`.
- **Views** live in post-service (`note_views` dedupe rows keyed by note, viewer and day, plus a counter
  on the note). Signed-out views are not counted.
- **Bookmarks** reuse the existing post bookmark storage if it can hold notes; bookmarks are private
  (only the count is public, as on X).
- **Trends and hashtag pages** read only notes; they never mix in ordinary posts, and ordinary trending
  never shows notes (phase 1 rule).
- Fixtures: add `note_v2.json` (with poll and counts), `note_scheduled.json`, `trends.json`,
  `error_poll_closed.json` to `post-service/testdata/contracts/notes/`. Web copies them byte for byte.

## 4. notification-service

- `GET /v1/notifications?filter=mentions` returns only mention-type notifications (`user.mentioned`
  on notes and replies to the viewer). If a filter param already exists, reuse it.

## 5. Web: Echo is its own app (pb-echo, `/echo/*`)

Walked from x.com on 14 Nov. Echo pages render **without** the VChat shell: no VChat left menu, no
second Echo menu.

- **Left column:** Echo logo; Home, Explore, Notifications, Chat (opens VChat chat), Bookmarks,
  Premium, Profile, More (Settings, Scheduled, back to VChat); a big **Post** button; the account chip
  at the bottom.
- **Centre column (Home):** tabs **For you / Following**; the inline composer with toolbar: photos/video
  (images only in v1), GIF, poll, emoji, schedule, and the **Everyone can reply** picker; then the
  timeline. "Show N posts" pill for new items.
- **Post card:** avatar, name, badge, @handle · time, `···` menu (Not interested, Follow/Unfollow,
  Mute, Block, View post activity (author), Copy link, Report); text with links/hashtags/mentions;
  media or GIF or poll; quoted note card; action bar: **reply count, repost (menu: Repost / Quote),
  like, views, bookmark, share** (Copy link, Send via Chat).
- **Post page `/echo/:handle/status/:id`:** back arrow + "Post"; the big post with full date and views;
  the action bar with counts; "Relevant" sort and **View quotes**; "Post your reply" box; replies.
- **Quotes page** `/echo/:handle/status/:id/quotes`.
- **Profile `/echo/:handle`:** cover, avatar, name, badge, handle, joined date, Following/Followers;
  tabs **Posts, Replies, Reposts, Media**; Edit profile goes to VChat's profile editor.
- **Explore `/echo/explore`:** search box; tabs **Trending** and **Latest** (no News/Sports
  categories: we have no topic data); trend rows link to `/echo/hashtag/:tag`; search results page.
- **Notifications `/echo/notifications`:** tabs **All / Mentions**.
- **Bookmarks `/echo/bookmarks`**, **Scheduled `/echo/scheduled`** (edit time, delete).
- **Right column:** search; **Subscribe to Premium** card (hidden for Premium members); **Trends**
  (from `/v1/notes/trends`); **Who to follow** from the existing suggestions route.
- **Compose modal** `/echo/compose` from the Post button, same composer; **threads**: the "+" adds
  another entry (existing `/v1/notes/thread`).
- **Free members** see the composer replaced by a short "Posting on Echo needs Premium" card with
  **Get Premium**; they can still reply, like, bookmark and vote.
- Views: the web batches visible note ids every few seconds into `POST /v1/notes/views`.
- GIFs: pick from the existing GIF search route, download the chosen GIF and upload it through the
  normal media flow as `image/gif` (phase-1 rule: exactly one GIF). No external URLs are stored.
- Emoji: a local picker inserting into the text.

## Video in notes (15 Nov 2026)

The founder asked for short video in notes, after X's limits. Hours-long video stays on PostTube.

- **What a note may carry:** up to 4 images, or one GIF on its own, or **one video on its own**
  (no images or GIF beside it). Threads take a video per entry under the same rule.
- **Limits,** decided by the poster when the note is written:

  | Poster | Longest video | Largest file |
  |---|---|---|
  | Without Premium (replies) | 140 s (2 min 20 s) | 512 MB |
  | With Premium (notes, threads, quotes, and their replies) | 10 minutes | the platform's own upload cap |

- **Upload:** the normal media flow with `file_type:"video"`, `media_subtype:"general"`:
  `POST /v1/media/init` → `PUT` → `POST /v1/media/confirm`. A file the simple path refuses with
  `413 USE_RESUMABLE_UPLOAD` goes up through the resumable path. Then `POST /v1/notes` with
  `media_ids:[id]`; there is no need to wait for transcoding.
- **Checks:** clients check length and size before uploading, from the local file. The server
  refuses a known-too-large file (`400 VIDEO_TOO_LARGE`) and a known-too-long one
  (`400 VIDEO_TOO_LONG`) at create. The length is usually measured only after transcoding, so the
  limit is stored on the note (`posts.note_video_max_ms`, migration 084).
- **Reading:** a video note has `post_type:"video"` and one `media` entry with `kind:"video"`,
  `duration_ms` and `hls_url` once ready. As with every post, it is shown only to its author
  until the video is `ready` + `passed` (`is_processing:true` meanwhile). A video that turns out
  longer than its note allows is shown **only to its author**, with `video_too_long:true`; clients
  say "This video is longer than {limit} and is only visible to you."
- **Playing:** muted autoplay in timelines when in view, tap for sound; HLS first (hls.js on the
  web, Media3 on Android), the note page plays with controls. Views count as for any note.
