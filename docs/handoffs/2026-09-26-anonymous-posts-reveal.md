# Anonymous group posts — the model, and what the web shows

Date: 26 September 2026. Backend live on dev (group-service + media-service). This is the contract for Codex to wire on the web.

## The rule

- **Members, and anyone else:** nothing on the wire identifies the author of an anonymous post. The post's `author_id` is a per-post random alias (a UUID that resolves to nobody); the author's own comments on that post carry the same alias; the legacy feed, search, media grid, events and realtime payloads all say the alias; the attached photos/videos are served under URLs that never name the uploader; the media record (`GET /v1/media/:id`) is 404 to everyone but the uploader.
- **Group owner, admins, moderators:** may ask who posted, through one explicit endpoint. Every answer is written to `group_admin_audit`.
- Posting itself is authenticated as before; only the exposure changes.

## For the web

### Rendering an anonymous post (everyone)

`is_anonymous: true` on the post. Show **"Anonymous member"** with a neutral avatar. Do not look up `author_id` in the profile API — it is an alias and will 404. The same alias appears as `user_id` on the author's own comments (`is_anonymous: true` on the comment), so those can render as "Anonymous member (author)".

`cross_post_group_id` is absent on anonymous posts by design.

### Reveal (owner / admins / moderators only)

```
GET /v1/groups/{groupId}/posts/v2/{postId}/author
```

| Status | Meaning |
|---|---|
| 200 | `{data:{post_id, author_id, is_anonymous, revealed_at}}` — `author_id` is the real user; `revealed_at` present only when an anonymous author was revealed (and audited). For a non-anonymous post it simply echoes the author with no audit. |
| 403 `FORBIDDEN` | the viewer is not the group's creator or an active admin/moderator — *including the author themselves* |
| 404 | group or post not found in this group |

Response is `Cache-Control: private, no-store`. Never store the revealed id in the feed cache or in localStorage.

UI suggestion: on an anonymous post, if the viewer's role in the group is owner/admin/moderator (`viewer_role` on the group), show a **"Reveal author"** control in the ⋯ menu with the copy *"Only admins can see this. Every reveal is recorded."*; on click call the endpoint and show the profile inline for that session only. Members get no control.

### Media

Attachments render exactly as before: `<img src="/v1/media/{id}/serve">`. For anonymous posts media-service streams the bytes itself (200, no redirect) instead of redirecting to a signed object URL; `GET /v1/media/{id}/url` returns gateway-relative `variants` (`/v1/media/{id}/serve/480p` …) with no `expires_at`. Nothing to change on the client; just do not expect `X-Amz…` URLs for these.

### Composer

Unchanged: `POST /v1/groups/{groupId}/posts/v2` with `is_anonymous: true` and `attachments: [mediaId…]`. One new refusal: if media-service cannot scope an attachment the post is refused (500 `unavailable: attachments could not be made anonymous; try again`) rather than published with a photo that names its uploader — show "Try again".

## Verified on dev (26 Sep)

Non-member `call_b` on a public group's anonymous post uploaded by `call_a`: post 200 (alias), media record 404, `/url` 200 (gateway paths), `/serve` 200 streamed PNG with no `Location`, `/status` 404, `/renditions` 404, legacy feed masked, the author's own comment listed under the alias, reveal 403 for `call_b` and for `call_a`. The pre-existing anonymous post in "Bengaluru Weekend Riders" now serves its photo the same way.

## Not done

- Realtime rooms (`group_post:<id>` on ws-gateway) stay disabled (`EnableScopedRooms=false`); the `comment_created`/`comment_deleted` payloads now carry the alias, but the typing indicator injected by ws-gateway would still name the author if rooms are ever enabled. Fix there before enabling.
- The reveal is per request; there is no "who has been revealed" screen yet. `group_admin_audit` rows with `action='group_post.author_revealed'` hold the trail.
