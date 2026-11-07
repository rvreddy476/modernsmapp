# Audit: platform services under the launch apps (search, groups/communities/channels, live, trust & safety, suggestions, user-service)

Branch `codex/module-01-02-launch-safety`. This was a read-only audit: no edits except this file, no git writes, no tests or builds, no databases, no deploys. Because of that, no `scripts/verify/go.sh` block is included.
Paths are relative to `Architecture/services/` unless they start with `Architecture/shared/`, `identity-platform/`, `mobile/` or `deploy/`.

**Covered**
- **search-service:** HTTP surface, block, private and hidden scope, every query builder, the Kafka consumer (users, posts, eligibility, communities, channels, products, chat), the users and tube reindex, `cmd/backfill`, and the purge adapter.
- **group-service, community-service and channel-service:** customer routes, the invite-only pilot policy, moderation and fan-out.
- **live-service-v2:** lifecycle, viewer gate, LiveKit tokens, chat, moderation v2, hearts, surfaces, webhooks.
- **trust-safety-service:** reports, appeals, grievances and verification as users see them. Admin flows are out of scope.
- **suggestion-service:** candidate pools, actions, egress filters, batch.
- **user-service:** profiles, by-username, business pages, endorsements, About.
- **api-gateway:** the rules in front of all of these: `cmd/server/main.go:653-723`, `pkg/internalroutes/internalroutes.go`, `pkg/routepolicy/routepolicy.go`.

**How exposed each product is today**
- **Live now:** `/v1/search`, `/v1/discover`, `/v1/livestream`, `/v1/broadcast-channels` (chat Communities, not dormant-gated, `api-gateway/cmd/server/main.go:1379-1381`), `/v1/users`, `/v1/pages`, `/v1/suggestions`, `/v1/reports`, `/v1/appeals` and `/v1/grievances`.
- **Closed at the edge:** `/v1/groups` and `/v1/communities` sit behind `GROUPS_PUBLIC_ENABLED` / `COMMUNITIES_PUBLIC_ENABLED`, which default to closed (`main.go:1360-1361`). Their findings are rated one level lower for that reason. G-M1 to G-M3 become HIGH the day the flag opens.

| ID | Sev | Service | One line |
|---|---|---|---|
| S-H1 | HIGH | search | Anyone, signed out included, can write any document into `users_v1` through `POST /v1/search/users/bulk-sync`. A crafted id injects arbitrary bulk actions into any index. |
| S-M1 | MEDIUM | search | Full-document rewrites drop `is_hidden` (and the backfill drops `author_is_private`), so deactivated accounts and private authors' posts come back into search. |
| S-M2 | MEDIUM | search | Private, invite-only and suspended communities, and suspended channels, stay searchable. |
| S-M3 | MEDIUM | search | Account erasure and deactivation leave the person's Tube channel and playlists searchable. Deleted playlists are never removed. |
| S-M4 | MEDIUM | search | `channel.updated` / `community.updated` replace the whole document: an edited channel vanishes from search, and an edited community loses `owner_id`, so blocks stop applying. |
| S-L1 | LOW | search | Dead indices and routes (`/events`, `/messages`, unused writers). `opensearch.go` is 2,373 lines. |
| LV-M1 | MEDIUM | live | A stream ban or a new block does not keep a viewer out: they rejoin with their 4-hour LiveKit token, and `participant_joined` never re-checks. |
| LV-L1 | LOW | live | The pinned chat message is served with no viewer gate: to signed-out callers, to blocked users, and on followers-only streams. |
| LV-L2 | LOW | live | A platform live ban stops chat, hearts and the realtime room, but not the viewer token. |
| LV-L3 | LOW | live | A moderator can lift any ban or mute, including the host's. |
| LV-L4 | LOW | live | `?status=all` runs the old live listing: it ends early behind hidden streams and makes one graph call per row. |
| LV-L5 | LOW | live | 500 responses carry the internal error text to clients. |
| X-L1 | LOW | search, live, channel, community | They boot in production without `INTERNAL_SERVICE_KEY` and then trust any `X-User-Id`. |
| CH-M1 | MEDIUM | channel | Comment, poll, RSVP and attendee routes skip the viewer and ban gate. `/comments/delta` reads any community's comments. |
| CH-M2 | MEDIUM | channel | The owner of any channel can delete or pin any comment platform-wide. |
| CH-M3 | MEDIUM | channel | A banned member erases the ban by unsubscribing, then rejoins. |
| CH-M4 | MEDIUM | channel | Mute does not stop push notifications. |
| CH-L1 | LOW | channel | Per-row `COUNT(*)` plus a member lookup on every channel response. |
| CH-L2 | LOW | channel | Fan-out pages the roster with OFFSET over an unindexed sort, so members are skipped or notified twice. |
| CH-L3 | LOW | channel | An owner can add anyone as admin, bypassing the pilot allowlist and the person's consent. |
| CH-L4 | LOW | channel | `/my` pagination is broken and lists banned communities. |
| G-M1 | MEDIUM* | group | Group search returns private groups. |
| G-M2 | MEDIUM* | group | An admin can demote, remove or ban the owner and other admins. |
| G-M3 | MEDIUM* | group | Cross-group writes by id: wiki overwrite, comment delete, post unpin. |
| G-M4 | MEDIUM* | group | A private group's rules, channels and wiki are readable by anyone. |
| G-L1 | LOW | group | `comment_permission`, `member_list_visible` and `group_type=hidden` are stored but never enforced. |
| G-L2 | LOW | group | Leaving checks only the first 100 members for another admin, and the creator can leave. |
| K-M1 | MEDIUM* | community | Private community content is readable without membership (`?space_id=` and ungated reads). |
| K-M2 | MEDIUM* | community | Community writes have no membership or scope check (events, cross-community `space_id`, engagement). |
| K-L1 | LOW | community | Ban evasion by leaving, and a moderator can lift any ban. |
| K-L2 | LOW | community | Discover lists communities that their own pages then refuse. |
| R-L1 | LOW | group, community, channel | God files and duplicate API paths. |
| US-M1 | MEDIUM | user | Deactivated and deletion-scheduled accounts are still served anonymously by id and by handle. |
| US-M2 | MEDIUM | user, suggestion | Neither service checks the internal key on its user routes, so any pod in the cluster can act as any user, page admins included. |
| US-M3 | MEDIUM | user | Seller approval sets a page to `active`: review is skipped, disabled pages are revived, any page id is accepted, and Discover shows only these pages. |
| US-L1 | LOW | user | Endorsements: free text with no block check, the recipient cannot delete them, and the public list is unbounded. |
| US-L2 | LOW | user | Compatibility and the legacy About read use private items. Unknown visibility counts as public. |
| US-L3 | LOW | user | Page reviews ignore page status and ownership, and a duplicate review is a raw 500. |
| US-L4 | LOW | user | Anonymous link-click and QR-scan counters can be inflated. |
| US-L5 | LOW | user | Schema lives in two places, one stale. `handler.go` mixes six concerns. |
| SG-M1 | MEDIUM | suggestion | One user's "block" removes that person from every viewer's suggestions, with a full-table scan. |
| SG-M2 | MEDIUM | suggestion | Deactivated accounts are suggested from six pools, and nothing filters them on the way out. |
| SG-M3 | MEDIUM | suggestion | The batch makes about 4,000 queries per viewer and also runs inside GET requests, where an empty result is never cached. |
| SG-L1 | LOW | suggestion | `GET /v1/suggestions/batch` is open to anyone and does nothing. |
| SG-L2 | LOW | suggestion | Impressions do one insert per item, with no cap. |
| SG-L3 | LOW | suggestion | The popular fallback ignores hide, dismiss and decline. |
| TS-M1 | MEDIUM | trust-safety | Reporters and appellants see the moderator's user id and internal notes. |
| TS-L1 | LOW | trust-safety | Grievances have no quota, and each one starts a statutory clock. |
| TS-L2 | LOW | trust-safety | The copyright 409 sends users to a counter-notice process that does not exist. |
| TS-L3 | LOW | trust-safety | Users cannot see their own verification request or its outcome. |

`*` Rated one level down because the product is closed at the gateway.

---

## search-service

### HIGH

#### S-H1. Anyone, signed out included, can overwrite any user's search document, and a crafted id injects arbitrary bulk actions into every index
- **Files:**
  - `search-service/internal/http/handler.go:137`: `v1.POST("/users/bulk-sync", h.BulkSyncUsers)` is registered on the public `/v1/search` group. Its path has no `internal` segment, so `internalroutes.Middleware` never refuses it.
  - `handler.go:214-230` (`BulkSyncUsers`): binds `{"users":[UserDoc...]}` from the body and calls `BulkIndexUsers`. It checks no identity, no role and no ownership.
  - `api-gateway/cmd/server/main.go:655`: the gateway proxies `/v1/search`. `jwtExtractMiddleware` passes a request without a token through anonymously (`main.go:791-797`). `pkg/internalroutes/internalroutes.go:309-311` stamps the internal key on `/v1/search`, so the engine-wide `RequireInternalKey` (`handler.go:125-127`) passes too.
  - `search-service/internal/store/search/opensearch.go:578-586`: the bulk action line is built with ``fmt.Sprintf(`{"index":{"_index":"users_v1","_id":"%s"}}`, doc.UserID)``, with the body's `user_id` spliced in raw. Every other bulk writer marshals the action line (`tube.go:547`).
  - Nothing in the tree calls the route. The grep for `bulk-sync` finds only the registration.
- **Scenarios:**
  - **(a) Impersonation in search.** A signed-out attacker posts `{"users":[{"user_id":"<founder id>","username":"founder","display_name":"Founder — DM for refunds","bio":"<scam link>","is_verified":true,"avatar_media_id":"<any media id>"}]}`. Every search, autocomplete and post-author card now shows that profile, because `postResults` hydrates authors from `users_v1` (`results.go:106-141`).
  - **(b) Undoing a hide.** The same body with `is_hidden:false` brings back a deactivated or deletion-scheduled account. One with `is_hidden:true` hides any real user from search.
  - **(c) Injection into other indices.** A `user_id` of `x"}}\n{"delete":{"_index":"author_fences_v1","_id":"<erased author>"}}\n{"index":{"_index":"posts_v1","_id":"<id>"}}\n{"visibility":"public","review_status":"approved","text":"…"}\n{"index":{"_index":"users_v1","_id":"y` is decoded by Go's JSON into real newlines. OpenSearch then runs every injected line with the service's master credentials. That deletes erasure fences (so erased content can be re-indexed), deletes any post's tombstone, or plants public "approved" posts with any text and any `author_id`.
- **Fix:**
  - Delete the route. The users reindex is already `/v1/search/internal/reindex/users`, and the consumer feeds the index.
  - If a bulk path must stay, move it under `/internal/` behind `requireServiceCaller`.
  - Build the action line with `json.Marshal` (as `bulkIndexDocs` does), and refuse a `user_id` that is not a UUID.
  - Add guard tests:
    - an edge-shaped `POST /v1/search/users/bulk-sync` answers 404;
    - a `user_id` containing `"` or a newline is refused;
    - a mutation that re-registers the route kills the first test.

### MEDIUM

#### S-M1. Full-document rewrites drop `is_hidden`, and the posts backfill drops `author_is_private`, so deactivated accounts and private authors' posts come back
- **Files:**
  - Only `UpdateUserHidden` and `UpdatePostsAuthorHidden` set the hidden flag (`opensearch.go:1357-1425`, driven by `events/purge_adapter.go:99-108`). Every other writer replaces the whole `_source` and never sets `IsHidden`, so it becomes `false`:
    - `IndexUser` on `UserRegistered` and `UserProfileUpdated` (`events/consumer.go:430-474`);
    - the users reindex and auto-heal: `reindex/reindex.go:156-197` writes through `BulkIndexUsers`. Its own read-back goes through `GetUsersByIDs`, which already excludes hidden docs (`opensearch.go:705-728`), so the flag cannot even be carried forward;
    - the post projection: the painless script `ctx._source.clear(); ctx._source.putAll(next)` at `store/search/projection.go:186-197`, fed by `PostCreated` and `PostSearchEligibilityChanged` docs (`events/consumer.go:571-600`, `events/eligibility.go:97-121`);
    - `cmd/backfill/main.go:547-571`, which re-projects every post with `Reproject: true`.
  - The backfill post doc also never sets `AuthorIsPrivate` (compare `consumer.go:567-574`). So `author_is_private` becomes `false` for every private author's post, and `applyPrivateAuthorMustNot` (`blockfilter.go:206-222`) stops hiding it.
  - `identity-platform/services/profile-service/internal/store/profiles.go:251-258`: the reindex source, `/v1/profiles/discover`, lists every profile with no lifecycle filter.
- **Scenarios:**
  - A user deactivates to get away from someone. Later, an operator runs `go run ./cmd/backfill -entity posts` for a mapping change, as `tube.go:19-25` tells them to. Or OpenSearch is wiped and `AutoHealUsersOnStartup` rebuilds `users_v1`. Either way, the deactivated account and all of its posts are searchable again by anyone.
  - With the same backfill, every post by a private account becomes visible to strangers and to signed-out callers.
  - On the live path, a moderator re-approves a hidden author's post during their deactivation window, and it reappears.
- **Fix:**
  - Make `is_hidden` and `author_is_private` survive rewrites. In the projection script, copy `is_hidden` from the stored `_source` when the incoming doc does not assert it. Do the same for user docs, by writing users through a scripted upsert instead of index or bulk replace.
  - In the reindex and backfill, resolve both flags from their authorities: account lifecycle and `privacyLookup` (the backfill already has the identity DSN). Skip a row whose flags cannot be resolved, as the reindex already does for privacy.
  - Add guard tests: a hidden author, then a re-projection, must still be hidden; a backfill doc for a private author must carry `author_is_private: true`.

#### S-M2. Private, invite-only and suspended communities, and suspended channels, stay searchable
- **Files:**
  - `store/search/opensearch.go:2181-2188` (`EntityCommunities`) has no filter at all. `autocompleteCommunities` (`:1587-1623`) also has none.
  - Communities are indexed from `community.created` with whatever `community_type` they have (`events/consumer.go:833-844`). community-service's types include `private` and `invite` (`community-service/database/setup.sql:10`).
  - `EntityChannels` filters on `channel_type` only (`opensearch.go:2189-2201`, `publicChannelFilterAny` `:841-845`), never on status. channel-service's suspension writes the status and emits no event (`channel-service/internal/service/moderation.go:248-263`), so the document stays.
  - `cmd/backfill/main.go:841` and `:885` index `communities` and `broadcast_channels` `WHERE status != 'deleted'`, which includes suspended (and, for communities, archived) rows.
- **Scenario:**
  - Moderation suspends an abusive community under the emergency switch. Its name, handle, description and owner still come up in `GET /v1/search?types=channels&q=…`, even though `/discover` and `GET /{id}` hide it. The founder rule says hidden when closed.
  - For communities: once any community exists (pilot or dev data plus a backfill), a private or invite-only community's name, handle, description and `owner_id` are returned to signed-out callers by `?types=communities` and by autocomplete.
- **Fix:**
  - Add a `status`/`is_active` field to both documents and filter `status = active` in the query builder, failing closed when the field is absent.
  - Filter communities to the same public set community-service uses (`public`, `education`, `professional`; see K-L2).
  - Have channel-service emit `channel.updated` (with status) on suspend and unsuspend, or emit `channel.deleted` on suspend.
  - Backfill only `status = 'active'`.
  - Add guard tests for each type and status.

#### S-M3. Erasure and deactivation leave the person's Tube channel and playlists searchable, and deleted playlists are never removed
- **Files:**
  - `events/purge_adapter.go:50-79` (`PurgeUser`) erases posts, the user doc and chat messages, then acks "search" as done. It never touches `tube_channels_v1` or `tube_collections_v1`, and neither does `SetUserHidden` (`:99-108`).
  - `applyHiddenMustNot` is a no-op on those indices: they have no `is_hidden` field (`blockfilter.go:129-133`).
  - Every account has a default channel named after the person, with the handle set to their username (founder decision of 21 Oct). The channel doc carries name, handle, about and avatar (`store/search/tube.go:44-54`).
  - The only feed is the manual `POST /v1/search/internal/reindex/tube` (`reindex/tube.go:50-125`). It indexes listed rows and deletes only listed non-public playlists. A playlist that post-service hard-deletes (`post-service/internal/store/postgres/playlists.go:249`) is never listed again, so its document stays forever.
- **Scenario:**
  - A user requests deletion. Search acks the purge, but `GET /v1/search/channels?q=<their name>` keeps returning their channel with their name, handle, bio and avatar id.
  - A creator deletes a playlist titled after someone. It stays findable in `/v1/search/collections` indefinitely.
- **Fix:**
  - In `PurgeUser` and `SetUserHidden`, delete-by-query (or set `is_hidden` on) `tube_channels_v1` and `tube_collections_v1` by `owner_id`, and add `is_hidden` to both mappings.
  - Make the tube reindex delete documents whose ids were not seen in a complete walk (mark and sweep), and add a post-service event for playlist and channel delete.
  - Do the same purge for `channels_v1` and `communities_v1` docs owned by the erased user.

#### S-M4. `channel.updated` and `community.updated` replace the whole document
- **Files:**
  - `store/search/opensearch.go:1948-1972` (`indexDoc`) is a plain `IndexRequest`, which is a full replace.
  - `events/consumer.go:898-906` writes `ChannelDoc{ChannelID, CreatedAt}` and nothing else. channel-service's payload carries only ids (`channel-service/internal/events/producer.go:116-123`).
  - `consumer.go:846-857` writes a `CommunityDoc` with no `OwnerID`, no counts and no handle. The comment there says "Partial update; leaves counters intact", which is not what the code does.
- **Scenarios:**
  - A community owner edits their channel's description. The document now has `channel_type: ""`, so `publicChannelFilterAny` drops it, and it disappears from search permanently (nothing re-adds it short of a backfill).
  - An edited community loses `owner_id`. The block exclusion keys on `owner_id` (`blockfilter.go:166-178`), so someone who blocked the owner sees the community again. Its member-count ranking also resets to zero.
- **Fix:** use the scripted partial update (`_update` with `doc` and `doc_as_upsert:false`) for both update events, or read the row back from the owning service as products do (`events/consumer.go:166-200`). Add a test: create, then update, must keep `owner_id`, `channel_type` and `name`.

### LOW

#### S-L1. Dead indices and routes; god file
- **Routes over indices nothing writes:**
  - `GET /v1/search/events` (`handler.go:891`) searches `events_v1`, and its only writer, `IndexEvent` (`opensearch.go:1722`), has no caller.
  - `GET /v1/search/messages` (`handler.go:926`) searches `messages_v1`, and its only writer, `IndexMessage` (`:1742`), has no caller. The code says so itself (`opensearch.go:2331-2336`).
- **Unused functions:** `IncrementHashtagUse` (`:2065`) has no caller, and `TombstonePost` is marked deprecated.
- **God file:** `opensearch.go` is 2,373 lines and mixes mappings, every entity's writers and every query builder.
- **Fix:** delete the two routes and the four functions, and split `opensearch.go` by entity, as `tube.go`, `credits.go` and `chat_messages.go` already are.

---

## live-service-v2

### MEDIUM

#### LV-M1. A stream ban or a new block does not keep the viewer out: they rejoin with the token they already hold
- **Files:**
  - `live-service-v2/internal/service/live.go:527-551` mints the viewer token with `viewerTokenTTL = 4 * time.Hour` (`:90`).
  - `Ban` (`internal/service/moderation_v2.go:84-122`) calls `kick`, which runs `RemoveParticipant` once (`:125-133`). LiveKit's RemoveParticipant does not revoke the JWT.
  - On rejoin, `HandleWebhook` routes `participant_joined` to `applyPresence` (`internal/service/lifecycle.go:325-328`, `:355-376`). That records presence and, through `announceJoin`, runs `MayWatch` (`joins.go:76-104`). A failing gate only suppresses the "joined" notice: it never removes the participant. `joins.go:33-35` names the case ("A stale LiveKit token must not put a banned or blocked user's name in the room") and handles only the name.
  - A host blocking a viewer mid-stream triggers no kick at all. Nothing in the service consumes a block.
- **Scenario:** A host bans a harasser from a live stream. The harasser's client reconnects with the same token (any LiveKit client retries on disconnect) and keeps watching for up to four hours. They are counted in `viewer_count`, while the moderation UI shows them as banned. The ws-gateway refuses them the chat room, but the video plays.
- **Fix:**
  - In `applyPresence`, for a joined viewer, run `MayWatch`. On a definite "no", call `RemoveParticipant` and do not count presence. An undecidable gate keeps the current behaviour.
  - Cut `viewerTokenTTL` to minutes, and have clients refresh through `/viewer-token`, which re-runs the gate.
  - On `user.blocked` between a viewer and a live host, kick.
  - Add a guard test: ban, then a `participant_joined` for the banned id, must call `RemoveParticipant`.

### LOW

#### LV-L1. The pinned chat message has no viewer gate
`internal/http/handler.go:112` and `:706-717` call `GetPinnedMessage` (`internal/service/live.go:1254-1265`), which loads the stream and returns the pinned row with its author card. It runs no `canSee` or `authorizeViewer`. Every other chat read is gated (`ListChat`, `:1071-1086`). **Scenario:** a signed-out caller, someone the host blocked, or a non-follower of a followers-only stream reads the pinned message text and its author's name, handle and avatar. They also learn that the stream exists. **Fix:** take `optionalUserID` in the handler and run `authorizeViewer` before the read, as `ListChat` does.

#### LV-L2. A platform live ban does not stop the viewer token
`SendChat` (`live.go:1014`), `SendHearts` (`hearts.go:112`) and `MayWatch` (`live.go:798-827`, "a live-banned user gets no real-time room") all check `IsPlatformBanned`. `IssueViewerToken` (`live.go:527-551`) checks only visibility, blocks and the stream ban. A user under a platform live ban can still fetch tokens and watch every stream's video through LiveKit. **Fix:** check `IsPlatformBanned` in `IssueViewerToken` if the ban is meant to cover watching. If it is not, say so in the `MayWatch` comment.

#### LV-L3. A moderator can lift the host's bans and mutes
`Ban` refuses a moderator who targets another moderator (`moderation_v2.go:101-103`). But `Unban` (`:136-151`) and `Unmute` (`live.go:1129-1146`) let any moderator lift any ban or mute, including one the host placed. **Scenario:** a host removes a moderator's friend, and the moderator unbans them a second later. **Fix:** record the actor's role on `live_stream_bans` and on mutes, and refuse a moderator lifting a host action.

#### LV-L4. `?status=all` still runs the old live listing
`listAllStreams` (`live.go:693-716`) calls `ListLiveNow` (`:571-604`) rather than `pageVisible` (`surfaces.go:338-388`). `ListLiveNow` over-fetches `2×limit`, filters, and emits a cursor only when the filtered page is full. So a page of streams the viewer cannot see ends the listing early. It also calls `canSee` per row (one cached graph call each) instead of the batch `visible` (`surfaces.go:290-332`). **Fix:** build the live phase of `status=all` on `pageLive`, then delete `ListLiveNow`.

#### LV-L5. Internal errors go to clients verbatim
`writeServiceErr`'s default branch answers 500 with `err.Error()` (`internal/http/handler.go`, the `default:` of the switch at about `:413`). Wrapped errors such as `livekit create room: …`, `livekit publisher token: …` and pg messages reach the client. **Fix:** log the error and answer a fixed "internal error" message.

---

## Cross-cutting

### LOW

#### X-L1. Four services boot in production without `INTERNAL_SERVICE_KEY` and then trust any `X-User-Id`
`search-service/cmd/server/main.go:251-258`, `live-service-v2/cmd/server/main.go:275-280`, `channel-service/cmd/server/main.go:227-233` and `community-service/cmd/server/main.go:177-182` only log a warning when the key is unset. Every handler then reads the actor from `X-User-Id`, so any pod that can reach the service can act as anyone. group-service refuses to boot in production in this case (`group-service/cmd/server/main.go:156-160`). **Fix:** copy group-service's `key == "" && isProduction()` exit into all four. user-service and suggestion-service are worse (US-M2).

---

## channel-service (`/v1/broadcast-channels`, chat Communities; live surface)

### MEDIUM

#### CH-M1. Comment, poll, RSVP and attendee routes skip the viewer and ban gate
- **Files:**
  - `channel-service/internal/service/channel.go:1246-1255` (`ListComments`) and `:1446-1456` (`ListAttendees`) check only that the update belongs to the channel.
  - `:1257-1263` (`ListCommentsSince`, the `/comments/delta` route) loads the channel and discards it. It never checks that `updateID` belongs to `channelID`, so any channel id plus any update id returns that update's comments.
  - `:1265-1295` (`AddComment`) checks only `comment_mode`: no ban, no private-channel membership, no status.
  - `:1356-1400` (`VoteOnPoll`), `:1402-1424` (`GetPollResults`) and `:1426-1444` (`RSVPEvent`) have no gate.
  - Compare `authorizeViewer` / `authorizeEngagement` (`:81-128`), which spark, stash, echo, view, `ListUpdates`, `GetUpdate`, `ListAdmins` and `Report` all go through.
- **Scenario:** A member banned from, or removed from, a private community keeps the update ids they saw. With them they can:
  - keep reading comment threads, through `/comments/delta` under any public channel id;
  - read poll results, vote and RSVP;
  - list every member who RSVPed.

  A banned member keeps commenting on a public community with comments enabled. All of this works on a **suspended** community too.
- **Fix:** Start each read with `authorizeViewer` and each write with `authorizeEngagement`. In `ListCommentsSince`, require `u.ChannelID == channelID`. Require `published` for non-admins, as `viewUpdate` does (`community.go:433-449`). Add one guard test per route for a banned member and for a non-member of a private channel.

#### CH-M2. Comment delete and pin are not scoped to the channel: any channel owner can delete or pin any comment platform-wide
- **Files:**
  - `channel.go:1309-1342` (`DeleteComment`) decides `isOwner` from the path channel. `store/channel.go:808-831` then runs `DELETE FROM update_comments WHERE id = $1` with no update or channel predicate.
  - `PinComment` (`channel.go:1344-1354`, `store/channel.go:833-848`) works the same way.
- **Scenario:** Anyone who owns any channel sends `DELETE /v1/broadcast-channels/<my channel>/updates/<anything>/comments/<victim comment id>`, and the comment is deleted from someone else's community. Comment ids come back from `GET .../comments`.
- **Fix:** Load the update and require `u.ChannelID == channelID`. In the store, use `WHERE id = $1 AND update_id = $2` for delete and for both pin UPDATEs. Add a cross-channel guard test.

#### CH-M3. A banned member erases the ban by unsubscribing, then rejoins
- **Files:**
  - `channel.go:747-774` (`Unsubscribe`) refuses only `owner`. A `banned` row is deleted (`store/channel.go:296-300`), and the subscriber count is decremented a second time (`BanMember` already did, `moderation.go:130-135`).
  - `JoinByInvite` (`invites.go:276-287`) and `Subscribe` (`channel.go:686-695`) recognise a ban only as an existing `role = 'banned'` row, and that row is now gone.
- **Scenario:** A moderator bans an abuser. The abuser sends `DELETE .../subscribe` and rejoins through any live invite, or through a plain `POST /subscribe` on a public community. Each round also drifts `subscriber_count` down by one.
- **Fix:** In `Unsubscribe`, return `ErrChannelMemberBanned` (or answer 204 without deleting) for a banned row. Add a guard test: ban, then unsubscribe, then rejoin must be refused.

#### CH-M4. Muting a community does not stop its push notifications
- **Files:**
  - `community.go:334-366` writes `channel_members.muted_until`, and the only reader is the display flag `viewer_muted` (`channel.go:388`).
  - The fan-out selects `user_id, notify_on` only (`workers/fanout.go:340-345`) and decides on `notify_on` (`:211-229`).
  - notification-service holds no copy of the mute.
- **Scenario:** A member taps Mute, the app shows "muted", and every new update still pushes.
- **Fix:** Select `cm.muted_until` in `subscriberBatchQuery`, and skip the push (still injecting the update into the feed) while it is in the future.

### LOW

#### CH-L1. Per-row `COUNT(*)` and member lookup on every channel response
`channel.go:364-390` (`withMembership`) runs `CountSubscribers` (`store/channel.go:334-338`, `COUNT(*) ... role != 'banned'`) plus `GetMember` for every channel. `/my` (`:1057-1090`) and `/discover` (`:1092-1110`) call it per row, which is about 40 queries per 20-row page and a full roster count per `GET /{id}`. This bypasses the sharded Redis counter the service already keeps (`:173-183`). **Fix:** use the stored or sharded count, and batch the viewer's roles with `channel_id = ANY($2)`.

#### CH-L2. Fan-out pages the roster with OFFSET over an unindexed sort
`workers/fanout.go:186-284` pages 1,000 rows at a time with `ORDER BY cm.subscribed_at LIMIT/OFFSET` (`:340-345`). There is no `(channel_id, subscribed_at)` index (`database/setup.sql:32-43, 280`). Joins and leaves during a run shift the window, so some members are skipped and others notified twice. **Fix:** keyset pagination on `(channel_id, user_id) > last`.

#### CH-L3. An owner can add anyone as admin, bypassing the pilot allowlist and the person's consent
`community.go:279-311` (`AddAdmin`) inserts a non-member directly as `admin`. It skips `policy.ParticipantAllowed` (which `Subscribe` and `JoinByInvite` enforce, `channel.go:704`, `invites.go:296`), consent and the block check. **Fix:** require the target to be an existing, non-banned member, or apply `ParticipantAllowed` plus a graph block check.

#### CH-L4. `/my` pagination is broken and lists banned communities
`channel.go:1057-1090` pages owned and subscribed channels separately with the same `limit/offset` and concatenates the two: a page can hold up to `2×limit` rows, and page 2 skips or repeats. `GetSubscribedChannels` (`store/channel.go:501-512`) has no `role != 'banned'` filter, so a banned user still sees the community in their list. **Fix:** one UNION with a single ORDER BY and LIMIT, plus `cm.role <> 'banned'`.

---

## group-service (`/v1/groups`, closed at the edge)

### MEDIUM*

#### G-M1. Group search returns private groups
`group-service/internal/store/group.go:1172-1190` (`SearchGroups`) filters only `status = 'active'`. `service/group.go:1801-1803` and `http/handler.go:955-973` add nothing. Discover filters `privacy_level = 'public'` (`store/group.go:1132-1149`). **Scenario:** `GET /v1/groups/search?q=support` returns every matching private group's name, description, creator id and member count. **Fix:** add `AND g.privacy_level IN ('public','restricted')` or a membership EXISTS, plus a guard test.

#### G-M2. An admin can demote, remove or ban the owner and other admins
- **Files:** `service/group.go`
  - `:791-833` (`UpdateMemberRole`): any admin may change any member's role. Only promotion to admin is reserved to the owner.
  - `:835-879` (`RemoveMember`): an admin may remove an admin or the creator. A moderator may remove a moderator.
  - `:881-937` (`BanMember`): an admin may ban another admin or the creator.
  - `:1662-1697` (`UnbanMember`): requires an active member, so a banned creator cannot unban themselves.
- **Scenario:** The creator promotes a co-admin, and the co-admin bans the creator. The group is taken over for good.
- **Fix:** Refuse `targetID == g.CreatorID`, and let only the owner act on admins. Mirror channel-service's `guardMemberModeration` (`channel-service/internal/service/moderation.go:30-54`) and its tests.

#### G-M3. Cross-group writes by id: wiki overwrite, comment delete, post unpin
- **Files:**
  - Wiki: `group.go:2152-2161` checks the **path** group, then `store/group.go:1559-1565` runs `UPDATE group_wiki_pages ... WHERE id=$1`.
  - Comment delete: `group.go:2559-2566` decides privilege on the path group, then `store/group.go:2245-2254` deletes `WHERE id = $1`.
  - Unpin: `group.go:1632-1659` has no group check (`PinPost` has one, `:1609-1615`). `store/group.go:1199-1201` updates the shared `posts` table by id.
- **Scenario:** A user creates a group, which makes them its admin. They then overwrite another group's wiki page (a private group's included) and read back its content, delete any group's comment, and clear `pinned_at` on any post.
- **Fix:** Scope every write by the path group, and add one cross-group guard test per route.

#### G-M4. A private group's rules, channels and wiki are readable by anyone
`http/handler.go:1062-1078`, `:1493-1508` and `:1561-1576` call `GetGroupRules`, `ListGroupChannels` and `ListWikiPages` (`service/group.go:1429-1431, 2124-2126, 2163-2165`) without `checkGroupAccess` (`:451-462`). The wiki list returns full `content` with no LIMIT (`store/group.go:1570-1574`). `checkGroupAccess` also answers "forbidden: not a member" for an existing private group, but nil for a missing one (`:365-371`), which confirms to an outsider that the group exists. **Fix:** gate all three reads, add a LIMIT, and answer a non-member with the same 404 as a missing group.

### LOW

#### G-L1. Settings that are stored but never enforced
- `comment_permission` is stored (`group.go:197-203`), but `AddGroupPostComment` (`:2504-2530`) checks membership only.
- `ListMembers` (`:763-789`) ignores `member_list_visible`.
- `group_type` `hidden`/`private` (`:296-299`) never changes `privacy_level`, so a "hidden" group is listed by `DiscoverPublicGroups`.

**Fix:** enforce each setting or drop it from the wire, and map `group_type` onto `privacy_level` at create time.

#### G-L2. Leaving checks only the first 100 members for another admin, and the creator can leave
`group.go:721-735` looks for another admin within `ListMembers(..., 100, 0)`, so a departing admin can be wrongly refused. The creator may leave whenever another admin exists, which leaves `creator_id` pointing at a non-member. **Fix:** `SELECT EXISTS(... role='admin' AND user_id <> $2)`, and refuse the creator or transfer ownership.

---

## community-service (`/v1/communities`, closed at the edge)

Most of the affected handlers call `h.svc.Store()` directly, which bypasses the service-layer gates.

### MEDIUM*

#### K-M1. Private community content is readable without membership
- **Files:**
  - `community-service/internal/http/handler_posts.go:157-170`: `GET /:communityId/posts?space_id=X` takes the `space_id` branch before the viewer gate and ignores `communityId`.
  - Ungated reads:
    - `:192-204` (featured posts);
    - `:388-400` and `:449-462` (wiki list and page);
    - `internal/http/handler.go:553-567` (spaces);
    - `handler_events.go:82-96` (events, unbounded, `store/community.go:714-721`).
  - `handler.go:302-321` / `service/community.go:403-422` (`GetCommunity`) has no visibility check at all.
- **Scenario:** An outsider who knows one space id reads every post in it. One who knows the community id reads its name, owner, spaces, events, wiki and featured posts.
- **Fix:** Run `authorizeViewerFromHeader` on every read. Resolve the space and require `space.CommunityID == communityID`. Answer a refusal with 404.

#### K-M2. Community writes have no membership or scope check
- **Files:**
  - `handler_events.go:23-80` (`CreateEvent`): any signed-in user can create an event in any community.
  - `handler_posts.go:36-119`: membership of the path community is checked, but not that `spaceId` belongs to it. A post can therefore be written into community B's space and is listed there (`store/community_posts.go:115-116`).
  - `:244-292` (spark, stash, view): no gate, and stash and view proceed with an empty `X-User-Id`.
- **Fix:** Require membership (or moderator, for events) and that the space belongs to the community. Gate engagement with `AuthorizeViewer` and refuse a blank user.

### LOW

#### K-L1. Ban evasion by leaving; any moderator can lift any ban
`service/community.go:652-681` (`LeaveCommunity`) deletes a `banned` row (`store/community.go:322-326`) and decrements the count again. `UnbanMember` (`:788-830`) lets any moderator lift a ban placed by an admin or the owner. **Fix:** refuse leave for a banned member, and check the banning actor's rank on unban.

#### K-L2. Discover lists communities that their own pages then refuse
Discover treats `local`, `fan` and `brand` as public (`store/community.go:248`). `AuthorizeViewer` treats only `public`, `education` and `professional` as public (`service/community.go:115-135`). **Fix:** define one public set and use it in both places, and in search (S-M2).

---

## group, community, channel: readability

#### R-L1. God files and duplicate paths
- group-service has three god files: `service/group.go` (2,758 lines), `store/group.go` (2,391) and `http/handler.go` (2,222).
- group-service has two post APIs (v1 on the shared `posts` table, and `/posts/v2`) and two moderation queues (`handler.go:121-127`).
- community-service's handlers reach the store directly, which is where K-M1 and K-M2 live.
- channel-service keeps deprecated spark routes and a `ListSubscribers` alias (`channel.go:792-794`).

**Fix:** split by domain, retire v1 posts and one of the queues, and route every community-service handler through a service method that owns its gate.

---

## user-service

### MEDIUM

#### US-M1. Deactivated and deletion-scheduled accounts are still served by id and by handle, anonymously
- **Files:**
  - `user-service/internal/store/purge.go:17-23` sets `users.hidden`, and `internal/events/consumer.go:166-182` sets `users.deleted_at`.
  - `GetUser` / `GetUserByUsername` (`store/users.go:231-240`) are `WHERE id = $1` / `WHERE username = $1`. Neither column is read anywhere.
  - `GET /v1/users/:userId`, `/by-username/:username` and the `/links`, `/pins`, `/portfolio`, `/endorsements` and `/reputation` reads answer anonymous callers (`internal/http/handler.go:315-357`, `profile_visibility.go:31-35`).
- **Scenario:** A user deactivates. For the 30 days before purge, anyone can resolve their handle (Android does, through `UsernameLookupApi.kt:23`) and read their name, bio, avatar, location, website and links.
- **Fix:** Add `AND NOT hidden AND deleted_at IS NULL` to these reads, and answer 404 except to the owner and to service-token callers. Add a guard test.

#### US-M2. user-service and suggestion-service never check the internal key on their user routes
- **Files:**
  - `user-service/internal/http/handler.go:164-167` installs `RequireInternalKey` only when `h.internalKey != ""`, and `WithInternalKey` (`:85-88`) is never called. `cmd/server/main.go:236` calls only `WithInternalRoutes`, which gates `/internal` (`handler.go:171-173`).
  - `suggestion-service/cmd/server/main.go:221` is `handler.New(suggSvc)`, with no key, so the gate at `internal/http/handler.go:31-34` is never installed.
  - The gateway's `StampPolicy` claims both are engine-wide gated (`api-gateway/pkg/internalroutes/internalroutes.go:243-246, 341-342`).
- **Scenario:** An SSRF or code execution in any pod reaches `user-service:8082` with `X-User-Id: <victim>`. With it, the attacker can:
  - edit or delete the victim's business page, links, About and status;
  - endorse as the victim;
  - as a `PAGES_ADMIN_USER_IDS` member, approve, suspend or disable any page (`business_pages_handler.go:639-690`).

  Against `suggestion-service:8100`, they can read anyone's suggestions and act as them (SG-M1). No key is needed at all.
- **Fix:** Call `.WithInternalKey(internalKey)` in both mains and refuse to boot without the key, as trust-safety does (`trust-safety-service/cmd/server/main.go:301-311`, admin-token family registered before the `Use`). Correct the gateway comments. Add a guard test: no key on `/v1/users/me/links` answers 401.

#### US-M3. `commerce.seller.approved` sets the page to `status='active'`, outside the page lifecycle
- **Files:**
  - `user-service/internal/events/consumer.go:210-237` (`handleSellerApproved`) runs `SetBusinessPageSellerID`, then `ActivateBusinessPage`, on the event's `business_page_id`. It checks neither status nor owner.
  - `store/business_pages.go:195-200` writes `status='active'`. `'active'` is not a lifecycle status (`internal/pages/config.go:30-35`), and `database/setup.sql:128-133` migrated legacy `active` to `approved`.
  - Commerce stores the `business_page_id` from the seller's request body without an ownership check (`commerce-service/internal/service/onboarding.go:41-75`, copied into the event at `:328-338`).
  - Effects:
    - `GetBusinessPage` treats `active` as public (`business_pages_handler.go:176-188`);
    - `FollowPage` requires `approved` (`business_pages.go:481`);
    - `DiscoverPages` lists only `active` (`business_pages.go:425`).
- **Scenarios:**
  - (a) A disabled scam page comes back publicly with no review once its owner is approved as a seller.
  - (b) A seller names someone else's page id during onboarding. On approval, the victim's draft page goes public, linked to the attacker's shop.
  - (c) Discover never shows approved pages, only these.
- **Fix:**
  - In the consumer, set only `seller_id`, and only when the page's owner is the seller's user (add `user_id` to the payload). Never change status.
  - Make `DiscoverPages` list `approved`.
  - Run a one-off status repair.
  - Have commerce verify page ownership before storing the id.

### LOW

#### US-L1. Endorsements: free text from anyone, blocked users included, and the recipient cannot remove them
`handler.go:746-783`, `service/user.go:545-550` and `store/reputation.go:61-91` do no block check and no target-exists check, and cap neither `skill_tag` nor `message`. Uniqueness is per `(from, to, skill_tag)` only (`cmd/server/main.go:499-506`). `GetEndorsements` (`reputation.go:123-143`) is unbounded and anonymous, and `DeleteEndorsement` is unused. **Fix:** retire the feature, or add a block check, length caps, a per-pair limit, delete by the recipient and paging.

#### US-L2. Compatibility and the legacy About read use private items
`GetCompatibility` (`service/user.go:594-622`) reads `GetAllAbout` unfiltered, which gives an oracle for the target's private sections. `canView` (`:379-391`) returns true for any unknown visibility value, and `UpsertAboutItem` (`handler.go:599-646`) stores any string. Clients now write About to identity profile-service. **Fix:** remove `/compatibility`, `/about` and `/me/about` from user-service, or filter with `ViewerAccess` and default to false.

#### US-L3. Page reviews ignore page status and ownership, and a second review is a raw 500
`business_pages_handler.go:547-585` and `store/business_pages.go:378-409` let anyone review a page in any status, including the page's own owner. `UNIQUE(page_id, reviewer_id)` (`setup.sql:104`) surfaces as a 500 carrying the pg message. **Fix:** require `approved` and a reviewer who is not the owner, and map 23505 to 409 `ALREADY_REVIEWED`.

#### US-L4. Anonymous counters can be inflated
`POST /v1/users/links/:platform/click?user_id=` (`handler.go:808-822`) and `POST /v1/users/:userId/qr/scan` (`profile_extras_handler.go:257-270`) increment counters with no identity. The web posts link clicks to post-service instead. **Fix:** drop both, or require a viewer and dedupe per day.

#### US-L5. Schema in two places; mixed handler
`cmd/server/main.go:362-560` (`ensurePhase6Schema`) re-creates tables that `database/setup.sql` already has. `main.go:112` still declares the retired three-value status CHECK. `handler.go` is 1,157 lines across six concerns. **Fix:** delete the inline DDL, and split the handler.

---

## suggestion-service

### MEDIUM

#### SG-M1. Any user's "block" action deletes that person from every viewer's suggestions, with a full-table scan
`suggestion-service/internal/service/suggestion.go:347-357`: action `block` calls `RemoveCandidateFromAllViewers(candidateID)`, which runs `DELETE FROM suggestion_candidates WHERE candidate_id = $1` (`internal/store/postgres.go:1189-1195`). Every index leads with `viewer_id` (`postgres.go:82-125`), so the delete scans the table. Android sends this action (`SuggestionsRepository.kt:41`). **Scenario:** A rival scripts `{"action":"block","candidate_user_id":"<target>"}` hourly, and the target never appears in anyone's "People you may know". **Fix:** scope the action to the caller. The `user.blocked` consumer already handles both directions (`events/consumer.go:399-428`). If a global removal is kept, put it on a service-only route and add `CREATE INDEX ON suggestion_candidates (candidate_id)`.

#### SG-M2. Deactivated accounts are suggested from six of the candidate pools, and nothing filters them on the way out
- **Pools that filter `hidden_users`:** FoF, triadic, social proof and follows-you (`postgres.go:244-264, 850-882, 938-956, 615`).
- **Pools that do not:**
  - location and profession (`:374-421`);
  - groups (`:695-702`);
  - life entries (`:797-805`);
  - popular (`:287-289`, also used live, `suggestion.go:500-508`);
  - trending (`:979-984`).
- **Egress filter:** `filterBlocked` (`block_safety.go:120-164`) checks blocks only.
- **Consumer:** there is no deactivation handler (`events/consumer.go:277-297`).

**Fix:** add the `hidden_users` NOT EXISTS to all six queries, or better, check it once in the egress filter, failing closed. Consume `user.deactivated` / `user.deletion_scheduled`.

#### SG-M3. Two queries per candidate in the batch, and the batch runs inside a GET
`batch.go:247` (`GetPublicLifeEntries`) and `:276` (`GetCandidateBlockRate`, a `COUNT(*) FROM blocks`) run per candidate. A pool can reach about 2,000 candidates, so one viewer costs about 4,000 round trips. That happens every 6 hours for every user (`cmd/server/main.go:262`). It also happens on the request path whenever a viewer has no stored candidates (`suggestion.go:145-152`), and an empty result is not cached. **Fix:** batch both reads with `= ANY($1)`, cache an empty result with a short TTL, and queue on-demand generation instead of running it in the request.

### LOW

#### SG-L1. `GET /v1/suggestions/batch` is open to any caller and does nothing
`internal/http/handler.go:52, 264-269` has no admin check and no `internal` segment, so the gateway proxies it. Its goroutine uses `c.Request.Context()`, which is cancelled when the handler returns, so `RunFullBatch` (`batch.go:580-593`) stops at once. **Fix:** delete the route. The tickers already run the batch.

#### SG-L2. Impressions are an unbounded per-request fan-out
`suggestion.go:269-299` runs one `INSERT` per item, with no cap and no validation of `rank` or `surface`. **Fix:** cap at 50 and use one multi-row insert.

#### SG-L3. The popular fallback ignores hide, dismiss and decline
`getPopularFallback` (`suggestion.go:500-560`) excludes only friends and followees, and is uncached. A hidden account comes back on the next request, despite its 30-day cooldown (`:322-333`). **Fix:** exclude `GetActiveCooldowns(viewerID)`.

---

## trust-safety-service (customer-facing)

### MEDIUM

#### TS-M1. Reporters and appellants are shown which staff member handled their case, and the internal notes
- **Files:**
  - `GET /v1/reports?mine=true` (`trust-safety-service/internal/http/handler.go:232-247`) returns the full `Report` (`store/postgres/reports.go:18-42, 158-185`), including `assigned_to` and `resolution_notes`, which is the reason the moderator typed (`handler.go:307-309`).
  - `GET /v1/appeals?mine=true` (`appeals_handler.go:80-92`) returns `reviewed_by` and `resolution_note` (`appeals.go:66-91, 150-156`).
  - Grievances return `assigned_to` (`grievances.go:14-29`).
- **Scenario:** A creator whose post was removed lists their appeals and sees `reviewed_by: <uuid>`. They open `/v1/users/<uuid>` (anonymous, US-M1), find the moderator, and go after them.
- **Fix:** Return user-facing projections without `assigned_to`, `reviewed_by` or `evidence.source`, with a user-safe outcome message. Expose the grievance officer as a role label. Keep the full structs on the admin-token routes.

### LOW

#### TS-L1. Grievances can be filed without limit, and each one starts a statutory clock
`internal/service/grievances.go:34-63` has no per-complainant quota (reports have one, `reporter_quota.go:46-59`). Each grievance gets `due_at`, and the overdue queue drives IT Rules compliance (`grievance_handler.go:120-127`). **Fix:** reuse the reporter-quota pattern, and refuse a duplicate on the same `about_entity_id` while one is open.

#### TS-L2. Copyright-held posts send users to a counter-notice process that does not exist
`appeals_handler.go:159-160` answers 409 `COPYRIGHT_CASE_USE_COUNTER_NOTICE`, but nothing accepts a counter-notice. The only fallback is a free-text grievance that is not linked to the case (`grievances.go:17-23`). **Fix:** add a counter-notice route bound to `case_id`, or point the message at the grievance route with the case id.

#### TS-L3. Users cannot see their own verification request
`GET /v1/verification-requests` is admin-only (`handler.go:120-125`, `trust_extras_handler.go:266-270`). A user sees "pending already exists" (`service/trust_extras.go:168-174`) but never the outcome, or the reason when more information is needed. **Fix:** add `?mine=true`, returning status and reason with no reviewer id.

---

## Checked and sound

### search-service
- **Block, private and hidden scope:** resolved once per request in middleware, and fails closed with 503 when the block set cannot be read (`internal/http/blockscope.go:44-81`). It is applied at the single serializer for every query (`store/search/blockfilter.go:113-138`): blocks both ways plus outgoing mutes, private authors' posts limited to the viewer and their follows, and `is_hidden` on every query.
- **Post eligibility:** a mandatory `visibility=public AND review_status=approved` filter on every post query path: the plain, credit, video, ranked, hashtag and popular queries (`opensearch.go:800-806, 2148-2160`, `credits.go:109-131`). Hashtags are derived from a live aggregation over `posts_v1`, so removed content leaves autocomplete. The regex prefix is escaped and length-capped (`opensearch.go:902-994`).
- **Index-time gate:** fails closed for held, scheduled and members-only posts (`events/consumer.go:521-603`, `events/eligibility.go:36-139`, `events/membership_gate.go`). Writes are revision-barriered inside OpenSearch, and an erased author is fenced permanently (`store/search/projection.go`, `authorfence.go`).
- **Consumer durability:** offsets are committed only after a durable outcome, with DLQ replay for the social topic (`consumer.go:290-416`, `dlq_replay.go`).
- **Products:** indexed only from commerce's read-back `Visible` flag. A 404 deletes. An outage retries rather than empties the index (`consumer.go:131-200`).
- **Chat message index:** internal route only, key required and fails closed (`internal/http/handler_chat.go:37-53`). The query requires the caller in `member_ids`, excludes tombstones and requests, and message-service re-checks.
- **Edge:**
  - Every reindex and alias route carries an `internal` segment, so the gateway refuses it (`api-gateway/pkg/internalroutes/internalroutes.go:178-209`).
  - Inbound identity headers are stripped (`api-gateway/cmd/server/main.go:837-900`).
  - The client IP is resolved at the gateway, so the autocomplete per-IP limit is not spoofable (`pkg/clientip`).
- **Query input:** queries are capped at 500 characters, ranked offsets at 10,000, and limits at 100. User text reaches OpenSearch only as JSON-encoded values, never spliced, except S-H1.

### live-service-v2
- **Viewer gate:** `canSee` / `authorizeViewer` (`internal/service/live.go:829-886`) covers visibility, blocks either way (fail closed on a graph error), stream bans, and refusing paid for everyone. It is applied to the viewer token, chat send and list, reminders, hearts, supporters, reports, moderators, and every listing. Listings use `pageVisible`, which batches relationships and resumes after the last row read (`surfaces.go:290-388`).
- **Host-only actions:**
  - start, end, ingress, update, pins and word filters check the creator;
  - nobody can ban the host, and only the host can ban a moderator (`moderation_v2.go:84-103`);
  - an encoder stream gets no publisher token (`live.go:479-482`);
  - viewer tokens are subscribe-only (`internal/livekit/client.go:160-171`).
- **Go-live eligibility:** checked on create, start and ingress. A platform ban stops a host from rejoining (`eligibility.go:609-670`).
- **Chat:** text cleaned and capped at 500 characters. Mute and word filter run before the rate limit, which fails closed at 20 per 60 s (`live.go:999-1067`). Removal and pin are scoped to the stream (`store/postgres/moderation.go:106-125`, `store.go:554-595`).
- **Webhook:** LiveKit JWT signature over the body, exp/nbf leeway, a body cap, and unparseable bodies refused (`internal/http/webhook.go`).
- **Internal routes** (`/internal/streams/:id/viewer`, `/reminders`) require the key strictly, and the admin family takes admin tokens only. All of them are refused at the edge.
- **Indexes** cover the on-air, upcoming, creator-status, presence and reminder queries (`database/migrations/003`, `005`).

### channel-service
- **Engine and routes:**
  - `RequireInternalKey` is engine-wide when the key is set (`internal/http/handler.go:96-98`). Admin-token routes come first and are judged by the token alone.
  - `/internal` fails closed.
  - The kill switch 404s the prefix while moderation keeps working (`:64-84`).
- **Pilot policy** (`internal/service/policy.go`):
  - empty allowlists admit nobody (`:206-264`);
  - communities are private by default, and private-to-public is refused (`:315-358`);
  - paid is refused in three spellings (`:288-305`);
  - direct subscribe to a private community is refused (`:362-371`);
  - invites check the participant allowlist before consuming a use, and a ban outranks an invite (`invites.go:276-311`).
- **Reads and engagement:** `GetChannel` 404s outsiders and suspended channels (`policy.go:392-404`). Updates and engagement go through `authorizeViewer` / `authorizeEngagement`, and drafts are hidden (`community.go:433-449`). Discover lists only active, public-class types.
- **Moderation and updates:** member moderation follows owner > admin > member (`moderation.go:30-54`). Only owners and admins post updates (`channel.go:811`). Poll votes are deduped by primary key (`setup.sql:163-169`).

### group-service
- Refuses to boot in production without the key (`cmd/server/main.go:156-160`).
- Feed, members, media, events, v2 posts and comments gate private groups.
- `engagementGate` checks access, ban, that the post belongs to the group, and `published` (`reactions.go:81-108`).
- Invites check blocks and fail closed (`group.go:1034-1075`). `AcceptInvite` re-checks expiry, ban and group state.
- Anonymous author reveal is moderator-only and audited (`anonymous_reveal.go:36-80`).
- Group-post search is gated, and the member cap is enforced atomically.

### community-service
- Creating a post requires membership.
- Delete, feature and pin require a moderator of the post's own community.
- Wiki writes are moderator-only and scoped by community plus slug.
- Role changes and bans are rank-checked, and bans are written to the mod log, failing closed.
- Space update, delete and quarantine check that the space belongs to the community.

### user-service
- Private fields go only to the owner or a service-token caller, and the key alone is not a credential (`internal/http/profile_visibility.go:96-131`).
- Online status asks graph and fails closed, with batches capped at 100 (`presence_visibility.go:37-58`).
- `/internal` fails closed and is refused at the edge.
- Non-approved pages 404 to non-owners. Follow locks the page, requires `approved`, and refuses the owner (`business_pages.go:461-503`). Page edits are scoped by `user_id`.
- Settings and profile `PUT` writes are retired.
- Usernames come only from the identity event, under a unique partial index (`users.go:362-389`, `setup.sql:25`).

### suggestion-service
- Blocks are filtered at egress on every path, cache hits included, failing closed (`service/block_safety.go:120-164`).
- `user.blocked` is applied durably through an inbox (`events/consumer.go:399-428`).
- Private life entries never feed discovery (`store/postgres.go:740-805`).
- Community suggestions exclude private, paid and inactive channels and blocked owners (`store/communities.go:44-45`).
- The actor is always `X-User-Id`.

### trust-safety-service
- An engine-wide key plus a separate admin-token family, registered in the right order (`cmd/server/main.go:301-311`). Service routes are under `/v1/internal/*`.
- User reports are quota-capped (`internal/http/handler.go:198-202`). "Mine" lists are scoped to `X-User-Id`.
- Appeals are author-only, exclude copyright cases, and are bound to the decision id (`service/appeals.go:128-144`). Appeal review requires a verified human (`:250-254`).
- Grievance reads are limited to the complainant or an admin. Keyword filters are bounded.
- No report event reaches notification-service, so a reporter's identity never reaches the reported user.
