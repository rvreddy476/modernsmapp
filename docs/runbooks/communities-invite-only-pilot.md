# Runbook — Communities: invite-only pilot

**Status:** pilot, internal-only. **Owner: NOT ASSIGNED.**
**Service:** `Architecture/services/channel-service` (port 8106, proxied by api-gateway at `/v1/broadcast-channels`).
**Decision this implements (founder, 2026-09-12):**

> "Communities decision: launch as an invite-only pilot with no public discovery directory. This does not waive moderation requirements. Document the minimum report-review process, moderation owner, member-removal controls and emergency disable procedure. Until a named owner is assigned, keep access internal-only. Do not expand into a public launch."

A community *is* a broadcast channel. There is no separate communities table or service.

---

## 1. What the pilot is, and what is deliberately switched off

| Deliberately off | How |
| --- | --- |
| Public communities | Creating one with `visibility=public`, or any public `channel_type` (`public`, `creator`, `brand`, `education`, `official`, `topic`), is refused `403 PUBLIC_COMMUNITY_NOT_ALLOWED`. An omitted visibility now defaults to **private** — it used to default to public. |
| Making an existing private community public | The same refusal on `PUT /v1/broadcast-channels/{id}`, whether the caller sends `visibility` or a raw `channel_type`. |
| The public discovery directory | Nothing a pilot community can be can appear in it. `GET /discover` filters `channel_type IN ('public','creator','brand','education','official','topic')` in SQL; a private community is not in that set. Search does the same now (see §7). |
| Joining without an invite | `POST /{id}/subscribe` on a private community is refused `403 INVITE_REQUIRED`. Joining goes through an invite code. |
| Unrestricted creation | `COMMUNITIES_ALLOWED_CREATORS` restricts creation to named user ids. **It is currently empty, which means it restricts nobody.** See §2. |

Still on: creating a private community, inviting people to it, posting updates, reactions, reports, and fan-out to subscribers.

---

## 2. Moderation owner — NOT ASSIGNED

**There is no named moderation owner for communities today.** Per the decision, access stays internal-only until one is assigned. The enforcement mechanism is `COMMUNITIES_ALLOWED_CREATORS`: a comma-separated list of user ids that may create a community. While it is **empty**, any authenticated user can create one — and channel-service logs this loudly on every boot:

```
WARN communities launch policy: COMMUNITIES_ALLOWED_CREATORS is EMPTY — ANY authenticated user can create a community. This is the "no named moderation owner" state: per the founder's 2026-09-12 decision, access must stay internal-only until an owner is named. Set COMMUNITIES_ALLOWED_CREATORS to the pilot owner's user id.
```

To assign the owner and close creation to everyone else, set the flag and restart the service:

```bash
# 1. Put the owner's user id in the environment (compose, or the deploy's env).
#    Comma-separate to allow more than one.
COMMUNITIES_ALLOWED_CREATORS=<owner-user-uuid>

# 2. Restart and confirm the boot log no longer carries the EMPTY warning:
docker compose -f Architecture/docker/docker-compose.yml up -d channel-service
docker compose -f Architecture/docker/docker-compose.yml logs channel-service | grep "communities launch policy"
# expect: creator_allowlist_configured=true allowed_creators=1
```

Anyone not on the list who tries to create a community gets `403 COMMUNITY_CREATION_RESTRICTED`.

A non-empty list whose entries are all unparseable fails **closed** — creation is refused to everyone, and the boot log says `closed to EVERYONE (fail closed)`. That is on purpose: a typo must not silently reopen the product.

**Until the owner is named, nobody is accountable for the report queue in §3.** That is the single biggest reason this cannot become a public launch.

---

## 3. Minimum report-review process

Reports land in `channel_reports` from `POST /{id}/report` and `POST /{id}/updates/{uid}/report`. Reporters are rate limited to 20/hour. Until 2026-09-12 nothing read that table: the reporter got `202` and nobody ever looked. There is now a reader, and a `channel.report.filed` Kafka event on the `channel-events` topic for a future trust-and-safety consumer.

**Who looks:** the named moderation owner from §2. **Until that person exists, whoever is on call for channel-service.** This is a gap, not a process.

**How often:** at least once per working day while the pilot is internal-only. One pass over the open queue.

**Target first-response time:** ⚠️ **NEEDS THE FOUNDER'S CONFIRMATION — do not treat the number below as a commitment.** A common floor for an internal pilot is *one working day* to first review; it is written here as a placeholder so the runbook is actionable, not because it has been agreed.

All commands below need the internal service key. Never paste the key into a shared log; read it from the environment.

```bash
# List the open queue, newest first.
curl -sS "http://localhost:8106/internal/channel-reports?status=open&limit=50" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" | jq .

# Next page: pass the meta.next_cursor from the previous response.
curl -sS "http://localhost:8106/internal/channel-reports?status=open&limit=50&cursor=<next_cursor>" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" | jq .

# Everything, any state (open|reviewed|dismissed|all).
curl -sS "http://localhost:8106/internal/channel-reports?status=all&limit=50" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" | jq .
```

Each row carries the report, the channel's `handle`, `name` and `status`, and the review trail (`reviewer_id`, `review_note`, `reviewed_at`).

Action a report. `status` must be `reviewed` (action taken or none needed) or `dismissed` (not a violation). `X-User-Id` is the reviewer and is recorded — so the audit trail names a person, not "internal":

```bash
curl -sS -X POST "http://localhost:8106/internal/channel-reports/<report-id>/review" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" \
  -H "X-User-Id: <your-user-uuid>" \
  -H 'Content-Type: application/json' \
  -d '{"status":"reviewed","note":"removed the member and the update"}' | jq .
```

Acting on what the report says is §4 (remove/ban the member) or §5 (suspend the whole community).

---

## 4. Member-removal and ban controls

Owner/admin only, with `X-User-Id` as the actor. Rules, all enforced server-side:

- only the owner or an admin may remove or ban;
- nobody may remove or ban **themselves** (`422 CANNOT_MODERATE_SELF` — unsubscribe instead);
- nobody may remove or ban the **owner** (`403 CANNOT_MODERATE_OWNER`);
- an admin may not act on **another admin** (`403 CANNOT_MODERATE_ADMIN`); the owner may.

**See who is in the community, with roles.** `?role=` accepts nothing (the non-banned roster), `all` (every row, banned included), or one exact role:

```bash
# Current roster with roles.
curl -sS "http://localhost:8106/v1/broadcast-channels/<channel-id>/subscribers?limit=100" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -H "X-User-Id: <owner-or-admin>" | jq .

# Who is banned.
curl -sS "http://localhost:8106/v1/broadcast-channels/<channel-id>/subscribers?role=banned&limit=100" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -H "X-User-Id: <owner-or-admin>" | jq .
```

**Remove a member** — subscription gone; they can rejoin with a new invite:

```bash
curl -sS -X DELETE "http://localhost:8106/v1/broadcast-channels/<channel-id>/members/<user-id>" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -H "X-User-Id: <owner-or-admin>" | jq .
# → {"status":"removed"}
```

**Ban a member** — they stay out. A ban outranks any invite link: a banned user hitting a valid code is refused. Banning emits `channel.member.banned`:

```bash
curl -sS -X POST "http://localhost:8106/v1/broadcast-channels/<channel-id>/members/<user-id>/ban" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -H "X-User-Id: <owner-or-admin>" | jq .
# → {"status":"banned"}
```

Observable effect of a ban: the user drops out of `member_count` and the subscriber roster, is refused on every read and engagement path (`403 BANNED`), and stops receiving fan-out notifications and feed injections.

**Unban:**

```bash
curl -sS -X DELETE "http://localhost:8106/v1/broadcast-channels/<channel-id>/members/<user-id>/ban" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -H "X-User-Id: <owner-or-admin>" | jq .
# → {"status":"unbanned"}   (returns them to plain subscriber standing)
```

**Rotate or kill the invite link** — a leaked link is a moderation problem too. `POST` mints a fresh code and revokes the previous one in the same transaction:

```bash
# Rotate (owner/admin). Body optional: expires_in_seconds (default 7 days, max 90), max_uses (0 = unlimited).
curl -sS -X POST "http://localhost:8106/v1/broadcast-channels/<channel-id>/invite-link" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -H "X-User-Id: <owner-or-admin>" \
  -H 'Content-Type: application/json' -d '{"expires_in_seconds":604800,"max_uses":25}' | jq .

# Read the current one.
curl -sS "http://localhost:8106/v1/broadcast-channels/<channel-id>/invite-link" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -H "X-User-Id: <owner-or-admin>" | jq .

# Revoke it outright — no new joins until someone mints another.
curl -sS -X DELETE "http://localhost:8106/v1/broadcast-channels/<channel-id>/invite-link" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -H "X-User-Id: <owner-or-admin>" | jq .
```

After a revoke, the code answers `410 INVITE_NOT_LIVE` on join and `is_live: false` on preview.

---

## 5. Emergency disable

Two levels. Level 2 first if one community is the problem; level 1 if the product is.

### Level 1 — the whole product (`COMMUNITIES_ENABLED=false`)

Requires a restart, so it is the bigger hammer.

```bash
# 1. Set the flag in the service's environment.
COMMUNITIES_ENABLED=false

# 2. Restart channel-service.
docker compose -f Architecture/docker/docker-compose.yml up -d channel-service

# 3. Confirm from the boot log.
docker compose -f Architecture/docker/docker-compose.yml logs channel-service \
  | grep "COMMUNITIES_ENABLED=false"
```

Expected observable effect:

- **every** path under `/v1/broadcast-channels` answers `404` with `{"error":{"code":"NOT_FOUND","message":"Not found"}}` — including paths that match no route, so the shape of the product does not leak through 404-vs-405 differences. 404 rather than 503 for the same reason api-gateway's dormant-product gate uses 404: a client should not learn a product exists behind a closed door;
- the schedule worker and the fan-out worker **do not start** — no notifications, no scheduled publishes, no feed injections;
- the **GDPR deletion consumer keeps running**, deliberately. Its only subject is `UserDeletionRequested`: it archives the communities a deleted user solely owned and removes them from every other one. Erasing a person's data is not a feature of this product and must not switch off with it. Gating it behind the flag would have stopped applying deletion requests to community data with nothing to show the gap, because channel-service publishes no purge acknowledgement for anything downstream to wait on;
- `/healthz`, `/metrics` and the `/internal` moderation routes keep working on purpose: a shutdown is exactly when you need to read the report queue and suspend rows.

Verify:

```bash
curl -sS -o /dev/null -w '%{http_code}\n' "http://localhost:8106/v1/broadcast-channels/discover" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY"   # → 404
curl -sS -o /dev/null -w '%{http_code}\n' "http://localhost:8106/healthz"                          # → 200
```

To re-enable: `COMMUNITIES_ENABLED=true` (or unset it) and restart.

> This is deliberately **not** wired into the gateway's `DORMANT_PRODUCTS_ENABLED` list. That flag is all-or-nothing and would couple communities to groups, so switching communities off would switch groups off with it.

### Level 2 — one community (`suspended` status, no restart)

```bash
# Suspend. Body optional; the reason goes into the service log.
curl -sS -X POST "http://localhost:8106/internal/channels/<channel-id>/suspend" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" \
  -H 'Content-Type: application/json' -d '{"reason":"report #1234, CSAM claim"}' | jq .
# → {"status":"suspended"}

# Lift it.
curl -sS -X DELETE "http://localhost:8106/internal/channels/<channel-id>/suspend" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" | jq .
# → {"status":"active"}
```

Expected observable effect, immediately (the 60-second channel-metadata cache is invalidated on the write, so there is no delay):

| Surface | After suspend |
| --- | --- |
| `GET /v1/broadcast-channels/{id}` | `404 NOT_FOUND` — for members and the **owner** too |
| `GET /v1/broadcast-channels/discover` | gone from the listing |
| `GET /v1/broadcast-channels/my` | gone, including from the owner's list |
| `GET /{id}/updates`, reactions, sparks, comments | `404` / refused |
| `POST /{id}/subscribe`, invite preview, invite join | `404` |
| Fan-out (notifications + feed injection) | produces nothing for that channel |
| Rows | untouched — a suspension is reversible, unlike delete |

Verify:

```bash
curl -sS -o /dev/null -w '%{http_code}\n' "http://localhost:8106/v1/broadcast-channels/<channel-id>" \
  -H "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" -H "X-User-Id: <owner>"   # → 404
```

---

## 6. Environment flags

All read by `channel-service` at boot. Every one is logged on startup under `communities launch policy`.

| Flag | Default | Effect |
| --- | --- | --- |
| `COMMUNITIES_ENABLED` | `true` | `false` = level-1 emergency disable: all `/v1/broadcast-channels` answer 404, workers do not start. |
| `COMMUNITIES_PILOT` | `true` | `true` = private by default, no public communities, no private→public flip, invite-only joining. `false` restores the pre-pilot open behaviour and logs a loud warning. |
| `COMMUNITIES_ALLOWED_CREATORS` | *(empty)* | Comma-separated user ids allowed to create a community. **Empty = nobody is restricted** (and the boot log says so loudly). A non-empty value with no valid UUID refuses everyone (fail closed). |
| `COMMUNITIES_INVITE_BASE_URL` | *(empty)* | Prefix for the `url` field of an invite response. Empty = only the bare `code` is returned. |
| `INTERNAL_SERVICE_KEY` | *(unset)* | When unset, **nothing in this service authenticates at all**, and the `/internal` moderation routes are not registered (they fail closed rather than serve unauthenticated). Must be set in any shared environment. |
| `HTTP_PORT` | `8106` | — |

Booleans accept `true/false`, `1/0`, `yes/no`, `on/off`. An unparseable value keeps the default and logs a warning.

---

## 7. Routes and their auth

Everything under `/v1` additionally requires `X-Internal-Service-Key` when `INTERNAL_SERVICE_KEY` is set (it is the service-to-service gate; api-gateway supplies it).

| Route | Auth |
| --- | --- |
| `POST /v1/broadcast-channels/{id}/invite-link` | `X-User-Id` = owner/admin |
| `GET /v1/broadcast-channels/{id}/invite-link` | `X-User-Id` = owner/admin |
| `DELETE /v1/broadcast-channels/{id}/invite-link` | `X-User-Id` = owner/admin |
| `GET /v1/broadcast-channels/invites/{code}` | none (`X-User-Id` optional, only sets `is_member`) |
| `POST /v1/broadcast-channels/invites/{code}/join` | `X-User-Id` |
| `DELETE /v1/broadcast-channels/{id}/members/{userId}` | `X-User-Id` = owner/admin |
| `POST /v1/broadcast-channels/{id}/members/{userId}/ban` | `X-User-Id` = owner/admin |
| `DELETE /v1/broadcast-channels/{id}/members/{userId}/ban` | `X-User-Id` = owner/admin |
| `GET /v1/broadcast-channels/{id}/subscribers?role=` | `X-User-Id` = owner/admin |
| `GET /internal/channel-reports` | internal key |
| `POST /internal/channel-reports/{id}/review` | internal key + `X-User-Id` (recorded as reviewer) |
| `POST /internal/channels/{id}/suspend` | internal key |
| `DELETE /internal/channels/{id}/suspend` | internal key |

The invite **preview** is the only thing a private community discloses to an outsider, and it discloses only `name`, `description`, `avatar_url`, `member_count`, `is_member`, `is_live`, `expires_at`. No handle, no owner, no channel type, no update counts.

Error codes: `PUBLIC_COMMUNITY_NOT_ALLOWED` 403, `COMMUNITY_CREATION_RESTRICTED` 403, `INVITE_REQUIRED` 403, `INVITE_NOT_FOUND` 404, `INVITE_NOT_LIVE` 410, `CANNOT_MODERATE_SELF` 422, `CANNOT_MODERATE_OWNER` 403, `CANNOT_MODERATE_ADMIN` 403.

---

## 8. What would have to be true before a public launch

Drawn from what the audit and this pass actually found. None of these is speculative.

1. **A named moderation owner, with `COMMUNITIES_ALLOWED_CREATORS` set.** Nobody owns the report queue today. Everything else on this list is downstream of this.
2. **An agreed first-response time for reports** (§3 is a placeholder), and evidence the queue is actually being worked — a report volume and time-to-review number someone looks at.
3. **Something consuming `channel.report.filed`.** The event exists; there is no consumer. Today review is a human running a curl against an internal route. A public launch needs a real queue with assignment and escalation, not a runbook command.
4. **Proactive detection, not just user reports.** There is no automated classification of community names, descriptions, avatars or updates anywhere in channel-service. At public scale, reports alone are not a moderation system.
5. **A revalidation of every surface that indexes or lists a community.** This pass found channel documents leaking out of `search-service` with no visibility filter at all — the backfill and the Kafka consumer both index private channels, and only a query-side filter now holds them back. Before a public launch the *index* should be filtered too (so the documents are not there to leak), and every other consumer of `channel.created` should be checked the same way.
6. **Rate limits that survive contact with abuse.** Creation is 5/day/user and reports 20/hour/user. There is no limit on invite minting, and the invite-join limit is 20/hour/user and silently skipped when Redis is unavailable.
7. **A decision on paid communities.** `channel_type='paid'` maps to private visibility and is therefore allowed through the pilot, but nothing in the pilot was designed around payment, refunds or access-on-expiry.
8. **Appeals.** A banned user has no route to contest a ban, and a suspended community's owner is not told why — they just get a 404.
9. **An owner-transfer path.** The owner cannot be removed, banned, demoted or unsubscribed; the only exit is deleting the community. If a pilot owner leaves, their communities are stranded.
10. **Moderation coverage for updates, not just members and channels.** A moderator can ban a member and suspend a whole community, but there is no internal route to take down a single offending update — only the channel's own owner/admin can delete one, which is useless when the owner is the problem.

---

## 9. Changelog

- **2026-09-12** — Pilot built. Private by default, creation allowlist, public-flip guard on create and update, invite links, invite-only joining, member removal, ban/unban, report queue reader + `channel.report.filed`, two-level emergency disable, and the search-service private-channel filter. Before this pass: communities defaulted to **public**, anyone could join any private channel directly (`Subscribe` had a literal `_ = ch` with the comment "Private channels could require approval, but for now allow direct subscribe"), there was no invite mechanism, no route could remove or ban a member, nothing read `channel_reports`, and `suspended` was a status the CHECK allowed and nothing wrote.
