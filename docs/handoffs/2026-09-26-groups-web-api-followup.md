# Groups web — backend follow-up: handover from Claude

Date: 26 September 2026. Answers `prompt/groups-web-api-followup-for-claude.md`.

Boundaries honoured: only `Architecture/services/group-service` was edited;
nothing under `C:\workspace\postbook-ui`, no other service, no mobile. The
tree is left **dirty and unstaged** — two files, listed below — with no
commit, push, branch or reset. No database volume was touched. group-service
was rebuilt and redeployed on dev (a code change; **no migration**).

## 1. GET /v1/groups/my → 500 — root cause, proven

**Symptom** (group-service access log, 2026-09-25T18:17:22Z, user
`82acf454…`): `/v1/groups/my` → 500 in 1 ms, 168 bytes, repeatedly; the same
account's `GET /v1/groups/4f400cc9…` (MyFamily), `/members`, `/media`,
`/feed/v2` → 200.

**Not** an ambiguous column, and not the SQL. The exact query from
`Store.ListGroupsByUser` run in psql against the live rows returned three
rows cleanly for that user. The failure was in the **scan**:

- `groups.handle` is nullable and carries a UNIQUE index (`groups_handle_unique`),
  so unlike `category` / `location` / `language` (migration 001:
  `NOT NULL DEFAULT ''`) it cannot default to `''`.
- `store.Group.Handle` is a plain `string`. pgx refuses to scan NULL into
  `*string`.
- One row had a NULL handle: `Bengaluru Weekend Riders`
  (`aaaa1111-2222-4333-8444-555566667777`), which I seeded on 25 Sep by direct
  SQL, without a handle. `CreateGroup` always writes one (it slugifies the
  name when none is given), so the API cannot produce this row; only a direct
  write can.
- User `82acf454…` is a member of that group (I added all four dev accounts).
  Her list included the bad row → whole list failed. `GET /:groupId` on
  MyFamily scans the same projection but only that row, which has a handle →
  200. That is exactly the observed asymmetry.

**Captured error text** (from the regression test with the fix reverted —
this is the message the 500 body carried):

```
can't scan into dest[13]: cannot scan NULL into *string
```

`dest[13]` is the 14th column of `groupColumns`: `g.handle`.

**Running image vs source:** the container was on image
`f6daf139…` built 14:44 IST from HEAD `8d153fdf` — current source. The bug
was in current source, not a stale image.

### The fix (one projection, every group read)

`internal/store/group.go`, `groupColumns`:
`g.handle` → `COALESCE(g.handle, '') AS handle`, with a comment explaining
why. `groupColumns` is the single projection behind `scanGroup` and
`scanGroups`, so `GetGroupByID`, `GetGroupByHandle`, `ListGroupsByUser`,
discover and search all become NULL-safe at once. `Handle` has `omitempty`,
so `''` and NULL are identical on the wire — **no response shape changes**.

Chosen over changing `Handle` to `*string`, which ripples through every
caller for the same wire result, and over a schema `NOT NULL`, which the
UNIQUE index forbids for `''`.

**Data:** after verifying the fix live against the NULL row, I set that
seed row's handle to `bengaluru-weekend-riders` (what `CreateGroup` would
have slugified), guarded on uniqueness. No live row now depends on the
COALESCE; the regression test does.

### Regression test (new file)

`internal/store/list_groups_by_user_integration_test.go`
(`//go:build integration`, refuses any DSN whose database is not `*_test`):

| test | pins |
|---|---|
| `TestListGroupsByUserSurvivesANullHandle` | the reproduction: NULL-handle group + active membership lists, `Handle == ""`; `GetGroupByID` agrees |
| `TestListGroupsByUserExcludesBannedRemovedAndDeleted` | acceptance 3 |
| `TestListGroupsByUserEmptyAccount` | acceptance 2 |
| `TestListGroupsByUserPaginationContract` | acceptance 5, including the `limit > 100 → 20` quirk |

Mutation-checked: with the COALESCE reverted (compiles), the first test
fails with the exact message above; restored byte-identical.

**A finding from writing it:** the real boot path
(`BootstrapSchema` = `setup.sql` + every embedded migration) **cannot run on
a group-only scratch database** — migration `002_groups_home_v2.sql`
references post-service's `posts` table, which exists only in the shared
`app` database. group-service therefore cannot bootstrap before post-service's
schema exists. Not a fresh-install bug on the shared DB, but a real
cross-service ordering dependency. The fixture patches the columns forward
the way `group_post_viewer_flags_integration_test.go` already does.

### Commands and results

```
go build ./... && go vet ./... && go vet -tags integration ./internal/store/   → OK
go test ./...                                                                  → http, service, store: ok
GROUP_POSTGRES_DSN=…/group_it_test go test -tags integration ./internal/store/ -run ListGroupsByUser -p 1
  --- PASS: TestListGroupsByUserSurvivesANullHandle
  --- PASS: TestListGroupsByUserExcludesBannedRemovedAndDeleted
  --- PASS: TestListGroupsByUserEmptyAccount
  --- PASS: TestListGroupsByUserPaginationContract
mutation (COALESCE reverted):
  --- FAIL: TestListGroupsByUserSurvivesANullHandle
      … can't scan into dest[13]: cannot scan NULL into *string
docker compose build group-service && docker compose up -d --no-build --force-recreate group-service
  container image == freshly built image (sha256 compared); boot log clean
```

### Live verification (acceptance 1, 2, 3, 4)

I hold no browser session, so the "authenticated request" was made with the
gateway's **own identity contract** — `X-User-Id` + `X-Scopes` +
`X-Internal-Service-Key`, issued from inside the api-gateway container with
its own key (value never printed) to `group-service:8090`. This is byte-for-
byte what the gateway forwards for a signed-in session; it is not itself a
session. **The real browser session confirmed it on its own:** the original
500s at 18:17Z arrived at group-service from client IP `172.18.0.5`; my
direct calls arrive from `172.18.0.49`. After the deploy, `172.18.0.5`
requested `/v1/groups/my` for `82acf454…` four times and every one was 200:

```
2026-09-25T18:53:09Z user 82acf454 status 200 client_ip 172.18.0.5
2026-09-25T18:54:26Z user 82acf454 status 200 client_ip 172.18.0.5
2026-09-25T18:55:27Z user 82acf454 status 200 client_ip 172.18.0.5
2026-09-25T18:55:29Z user 82acf454 status 200 client_ip 172.18.0.5
```

Those are the web's own retries, not mine.

| call (as `82acf454…`) | before | after |
|---|---|---|
| `GET /v1/groups/my?limit=20&offset=0` | **500** | **200**, `data: [MyFamily, MyFamilyDigest, Bengaluru Weekend Riders]` — the NULL-handle row returned with `handle` omitted |
| `GET /v1/groups/my` as `c839e100…` (no memberships) | `{"data":[]}` | `{"data":[]}` |

Acceptance 4, with a disposable identity (`ffff…000a`) and a disposable group,
all through the same gateway-equivalent path:

1. `POST /v1/groups {name:"Fixture Group ZZ", …, idempotency_key}` → 201,
   handle auto-slugified `fixture-group-zz`, chat conversation provisioned.
   (Without `idempotency_key` the endpoint answers **400
   `IDEMPOTENCY_KEY_REQUIRED`** — the web must always send it.)
2. `GET /v1/groups/my` → `[Fixture Group ZZ]`.
3. `GET /v1/groups/:id/feed/v2` → **`{"data":null}`** for an empty group.
   The feed loads; note the shape is `null`, not `[]` — `ListPendingPosts`
   normalises to `[]`, `feed/v2` does not. The web coalesces with `?? []`.
   Not changed here (mobile reads this route); a one-line normalisation if
   you want it.

Everything created for this was removed afterwards (§5). Real groups and
memberships untouched: three active groups, same members, verified after
cleanup.

### Acceptance 5 — pagination contract for the sidebar

`GET /v1/groups/my?limit=N&offset=M`, ordered by `joined_at DESC`.

- `limit` defaults to **20**; valid range 1–100; **a value above 100 is not
  clamped — it falls back to 20** (`if limit <= 0 || limit > 100 { limit = 20 }`).
- `offset` ≥ 0, default 0.
- Response is `{"data": Group[]}` with **no total and no `has_more`**.
- The web's `useMyGroups` calls `/v1/groups/my` with no params → the first
  20 only. A user in more than 20 groups will not see the rest in the
  sidebar today. Continuation: request `offset += limit` while the last page
  returned exactly `limit` rows; a short page is the end. Do not claim all
  groups are served unless you paginate.

## 2. Existing recommendation support — verified, one privacy gap proven

Path: `GET /v1/suggestions/people` → `GetSuggestions` → Redis cache
(`suggestions:<viewer>:friend[:surface]`) → stored `suggestion_candidates` →
on miss, `RunBatchForUser` computes live.

**Data sources (what the codes really mean):**

| code | derived from | match rule |
|---|---|---|
| `SAME_CITY` | `identity_db.profile.profiles.location`, viewer vs candidate | case-insensitive **exact** string (`LOWER(a)=LOWER(b)`); "Bengaluru" ≠ "Bangalore"; no geo |
| `SAME_SCHOOL` / `SAME_COMPANY` | `identity_db.profile.user_about` rows, `section='life_entry'`, `data->>'type'` in school/education/company/work, matched on `data->>'name'` (ILIKE) | exact name |
| `SAME_PROFESSION` | `profile.profiles.profession` | exact |
| `MUTUAL_FRIENDS`, `COMMON_GROUPS`, `MUTUAL_FOLLOW` | graph / group tables | — |
| `POPULAR` (bucket `trending`) | fallback when nothing else scores | — |

**Why Codex saw only generic labels:** on dev, **0 of 77 profiles have a
location** and the whole `profile.user_about` table has **0 life-entry rows**.
The codes cannot fire for any real dev account because the data does not
exist; the code path is fine. Every real account gets the POPULAR fallback.

**Proof with consented synthetic fixtures** (identity + app rows, removed
after): viewer V = Hyderabad + school "Osmania University" (public entry).

| candidate | fixture | result |
|---|---|---|
| A | location Hyderabad | returned, `["SAME_CITY"]`, bucket `location`, explain "Lives in Hyderabad" ✓ |
| B | school entry, `visibility='public'` | returned, `["SAME_SCHOOL"]`, "Studied at Osmania University" ✓ |
| C | Hyderabad, but **V blocked C** (`public.blocks`) | **absent** ✓ — block filter is symmetric via graph `/v1/internal/graph/blocked-and-muted` |
| D | school entry, **`visibility='private'`** | **returned, `["SAME_SCHOOL"]`, "Studied at Osmania University"** ✗ |

**The gap:** `GetUserLifeEntries` / `GetUsersByLifeEntry`
(`suggestion-service/internal/store/postgres.go:685-730`) never read
`user_about.visibility`. A life entry the user marked private still produces a
recommendation reason **and** puts the institution name in `explain_text`. The
web hides `explain_text` and institution names, but `SAME_SCHOOL` itself
discloses "same school as you" for a private entry, and mobile may render
`explain_text`.

**Minimal change (not made — outside this brief's scope; say the word):**
add `AND visibility = 'public'` (or `IN ('public','connections')` per the
product's visibility vocabulary — check `profile.user_about.visibility`
values first) to both life-entry queries, and the same for `profession` if
profiles carry a per-field visibility. One file, two WHERE clauses, plus a
fixture-backed test that the private case stays out.

**Other settings:** there is no "discoverable in suggestions" setting
anywhere (only `allow_phone_discovery`, `discoverable_by_phone_to_contacts`,
`account_visibility`), and the pipeline does not gate on
`account_visibility='private'`. So "existing recommendation settings" =
blocks (respected) and nothing else. Reasons are never invented: a code is
emitted only when the underlying match is true.

## 3. Batch add/invite — exercised, semantics captured

`POST /v1/groups/:groupId/invite {user_ids:[…]}` → `{"data":{"added":N,"invited":N,"skipped":N}}`.
Aggregate only — per-target outcomes are deliberately not returned (a named
refusal would reveal a block).

**Decision per target** (`addOrInvite`): self → skipped; already a member →
skipped; banned from the group → skipped; otherwise graph-service
`GET /v1/permissions/check?actions=add_to_group` decides from the **target's**
`who_can_add_to_groups` (authority: identity-user `/v1/users/:id/settings`,
table `identity_db.usr.user_settings`, schema default `connections_only`):

| target setting | relationship | outcome |
|---|---|---|
| `connections_only` (default) | connected | **added** (direct membership) |
| `connections_only` | not connected | skipped (`privacy_connections_only`) — **no invite** |
| `everyone_with_approval` | any | **invited** (pending `group_invites` row, event published) |
| `friends_of_friends_invites` | connection / 2nd degree / else | added / invited / skipped |
| `no_one`, or settings unreadable | any | skipped (fails closed: `strictPrivacyDefaults` = `no_one`) |
| any | blocked either way | skipped |

Batch rules: max 50 ids (400 above), duplicates collapse to one, rate limit
100 invites / 24 h per actor, actor must be an active member and pass the
group's `who_can_invite`. **Partial outcomes:** a policy refusal never fails
the batch; a real store error (not policy) fails the **whole** call with no
partial count — by design, "3 invited" over a failed write is worse than an
error. One malformed id → 400 for the whole request before any write.

**Live run** (disposable actor V, disposable group; targets with real
`usr.user_settings` rows and a real `connections` row):

```
user_ids = [A connections_only+connected, B everyone_with_approval,
            C everyone_with_approval but blocked by V, D no_one, V (self), A (dup)]
→ {"added":1,"invited":1,"skipped":3}
tables: group_members has A active; group_invites has B pending; nothing for C, D
second call user_ids=[A] → {"added":0,"invited":0,"skipped":1}   (existing member)
```

**Admission recheck** (`AcceptInvite`): verifies the invite belongs to the
caller, is still `pending`, and has not expired (~7 days, marked `expired`
on late replay), then adds the member. It does **not** re-run `CheckBanned`
or check the group's status — an invite issued before a ban, accepted
after it, admits the banned user; an invite to a since-deleted group still
writes a membership row. Minimal change: two guards before `AddMemberWithInviter`.
Not made here.

## 4. Files changed (unstaged)

```
M  Architecture/services/group-service/internal/store/group.go
     groupColumns: COALESCE(g.handle, '') AS handle  (+ comment)
??  Architecture/services/group-service/internal/store/list_groups_by_user_integration_test.go
```

Rebuild: **yes** (group-service image rebuilt and redeployed on dev).
Migration: **no**. Data: one UPDATE setting a handle on my own seed row.

## 5. Fixtures created and removed

All ids in the `ffff0000-0000-4000-8000-…` range. Created: 5 `auth.users` +
`profile.profiles`, 3 `profile.user_about`, 4 `usr.users` + `usr.user_settings`
(identity_db); 1 `public.blocks`, 2 `public.connections`, `suggestion_candidates`
for V (app); 1 group `Fixture Group ZZ` with its members, invites and the
chat conversation it provisioned (chat_db); Redis `suggestions:ffff…*` keys.
After cleanup every count is 0 (queried per table). Real groups: three,
unchanged, memberships unchanged.

## 5a. Follow-up (26 Sep, founder-requested): both gaps fixed

Both fixes are in the same unstaged tree; both services rebuilt and
redeployed on dev; no migration.

### Private life entries no longer feed recommendations (suggestion-service)

`internal/store/postgres.go`: the life-entry reader is split by side.
`GetUserLifeEntries` (the VIEWER's own entries, every visibility — using
your own private school to find people discloses nothing about you) and
new `GetPublicLifeEntries` (a CANDIDATE's entries, `visibility = 'public'`
only — this is what the viewer is TOLD about someone else). Candidate
discovery `GetUsersByLifeEntry` also filters `visibility = 'public'`: being
found through a school is itself a disclosure of it. `batch.go` reads the
candidate side through the public reader. `'public'` is the column default
and the only value safe to show a stranger; a suggestion is by definition
to a non-connection, so a connections-only entry does not qualify either.

Guards (parsed with go/parser, comments excluded), each mutation-checked
and restored byte-identical:
- `TestCandidateDiscoveryReadsOnlyPublicLifeEntries` — fails when the
  filter is removed from `GetUsersByLifeEntry`.
- `TestCandidateLifeEntriesAreFilteredAndTheViewerReaderIsNot` — fails when
  the wrappers' flags are swapped.
- `TestBatchUsesThePublicReaderForCandidates` — fails when `batch.go` reads
  the candidate with the all-visibilities reader.

Live proof, same consented fixtures as §2, after redeploy: B (public
entry) returned with `SAME_SCHOOL`; **D (private entry) absent**; C
(blocked) absent. A (same city) was absent this time because the seed also
made A a connection of V for the invite proof, and `GetFriendIDs` reads
`connections` — connections are excluded from suggestions by design.

Not changed: `SAME_CITY` and `SAME_PROFESSION` read `profile.profiles`,
which has no per-field visibility; nothing to filter on.

### AcceptInvite re-checks the group and the ban (group-service)

`internal/service/group.go`: before the invite is marked accepted and
before any membership write, the group is loaded and refused if missing,
`deleted` or `archived`, and `CheckBanned` is consulted for the invitee.
Both refusals close the invite (`rejected`) and return one generic
`ErrInviteNoLongerAcceptable` ("forbidden: this invite can no longer be
accepted") → 403, so which condition applied is not learnable from the
endpoint. The later chat-sync lookup reuses the loaded group.

Fixed in passing, same function: the expiry path wrote status `expired`,
but `group_invites.status` is CHECK-constrained to
`pending | accepted | rejected`, so that write failed every time and its
error was discarded — expired invites stayed `pending` for ever. It now
writes `rejected`.

Guards (structural; the store is concrete), each mutation-checked:
- `TestAcceptInviteRechecksBanAndGroupBeforeAdmitting` — fails when the
  ban check is removed, or when the `"archived"` comparison is dropped;
  also asserts both checks precede `AddMemberWithInviter` by AST position.
- `TestAcceptInviteRefusalIsGeneric` — fails when a refusal returns a
  distinct, informative error.

Live proof (disposable actor V, group `Accept Proof ZZ`, invitees B and D
with `everyone_with_approval`), through the gateway identity contract:
1. invite → `{"added":0,"invited":2,"skipped":0}`, two pending invites.
2. B banned after the invite — staged as the `group_members` row with
   `status='banned'` that `BanMember` writes and `CheckBanned` reads
   (`BanMember` itself refuses a non-member, so the realistic path is
   join → ban → accept an older invite).
3. B accepts → **403**; active membership rows for B: 0; invite: `rejected`.
4. Group deleted with the exact UPDATE `DeleteGroup` runs; D accepts →
   **403**; membership rows for D: 0; invite: `rejected`.
Access log shows both accepts as 403 for the fixture ids.

Everything created for this was removed; residue zero across app,
identity_db, chat_db and Redis; the three real groups unchanged.

## 6. Unverified boundaries

- No browser session was used; live calls are gateway-equivalent, not a
  cookie session. The browser's own `/my` retry is the final confirmation.
- The recommendation proof exercises the batch computation and block filter;
  it does not exercise the Kafka `user.blocked` repair path or the Scylla
  pair-signal cache.
- `AcceptInvite` was read, not executed.
- Mobile behaviour on `feed/v2 → data: null` was not checked.
