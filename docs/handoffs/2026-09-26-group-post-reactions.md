# Group post emoji reactions — backend handoff for web wiring

Date: 26 September 2026. Service: `Architecture/services/group-service`. Deployed and verified on dev (image `09ec970d6c1b`, migration `016_group_post_reactions.sql` applied at boot). Web client untouched; this is the contract for Codex to wire.

## Contract in one screen

| Route | Auth | Body | Answer |
|---|---|---|---|
| `PUT /v1/groups/:groupId/posts/v2/:postId/reaction` | bearer | `{"reaction": "<allowlist>"}` | `200 {data: ReactionState}` |
| `DELETE /v1/groups/:groupId/posts/v2/:postId/reaction` | bearer | — | `200 {data: ReactionState}` |
| `POST /v1/groups/:groupId/posts/v2/:postId/spark` (legacy) | bearer | `{"is_supernova": bool}` | `200 {data:{ok:true}}` — unchanged |
| `DELETE /v1/groups/:groupId/posts/v2/:postId/spark` (legacy) | bearer | — | `200 {data:{ok:true}}` — unchanged |

**Allowlist** (`service.ReactionAllowlist`, mirrored by a DB CHECK; a test pins the two together):

```
like · love · smile · wow · sad · angry
```

Case and surrounding whitespace are normalised (`" LOVE "` → `love`); anything else is refused before any row is touched.

**`ReactionState`** — the authoritative state after a write, read back from the rows, never echoed from the request:

```json
{
  "post_id": "uuid",
  "reaction": "love" | null,          // the viewer's current reaction; null when none
  "spark_count": 1,                   // legacy weighted heart count (see compatibility)
  "reaction_counts": {"love": 1},     // people per reaction; {} when nobody has reacted
  "viewer_sparked": true              // legacy flag; true whenever reaction != null
}
```

**On every group-post read surface** (single post, group feed v2, in-group search, pending queue, cross-group `/v1/groups/feed`) each post now carries two extra fields beside the existing ones:

```json
"viewer_reaction": "smile" | null,
"reaction_counts": {"like": 1, "smile": 1}
```

`reaction_counts` is always an object, never null. `viewer_reaction` is null for an anonymous viewer or a viewer who has not reacted. `viewer_sparked` is still emitted and is true whenever `viewer_reaction` is set, so nothing that reads the old flag changes.

### Semantics

- **One current reaction per viewer per post.** Changing it replaces the row atomically (the row is locked `FOR UPDATE`, decided, written, committed as one transaction).
- **Retrying the same reaction is a no-op** and still answers 200 with the same state.
- **Removal is safe to retry**: DELETE when nothing is set answers 200 with `reaction: null`; nothing moves.
- **Concurrent first reactions** from the same viewer produce exactly one row and move `spark_count` once (`INSERT … ON CONFLICT DO NOTHING`; the loser re-locks and takes the replace/no-op path). Proven by an integration test with 8 goroutines, twice.
- **A rejected request mutates nothing**: allowlist, then group access, then ban, then post∈group are all checked before the transaction opens. Proven by an integration test that snapshots counts and rows across every refusal.

### Error codes

| Status | `error.code` | When | Message |
|---|---|---|---|
| 400 | `INVALID_ID` | group or post id is not a UUID | `Invalid group ID` / `Invalid post ID` |
| 400 | `INVALID_REQUEST` | body missing `reaction` | `reaction is required` |
| 401 | `UNAUTHORIZED` | no identity | `Invalid user ID` |
| 403 | `FORBIDDEN` | viewer is banned from the group | `forbidden: you cannot react in this group` |
| 404 | `NOT_FOUND` | group missing; **or private group and viewer is not a member** (`not a member` maps to 404 on purpose — no probing of private groups); post not in this group / not published | `not found: group not found`, `forbidden: not a member of this private group`, `not found: post not found in this group` |
| 409 | `CONFLICT` | legacy `POST /spark` when the viewer already has ANY reaction | `already sparked` |
| 422 | `VALIDATION_ERROR` | reaction outside the allowlist | `invalid: reaction must be one of like, love, smile, wow, sad, angry` |

## Compatibility with legacy sparks (mobile) — no double counting

The reaction **is** the spark row. `group_post_sparks` was already unique on `(post_id, user_id)`, which is exactly "one reaction per viewer per post", so migration 016 adds `reaction TEXT NOT NULL DEFAULT 'like'` (+ `updated_at`, a CHECK on the allowlist, and an index on `(post_id, reaction)`) to that table. There is no second table that could disagree with the first.

| Concept | Meaning after this change |
|---|---|
| A legacy heart (`POST /spark`) | a `like` reaction (row inserted with `reaction='like'`) |
| Existing heart rows on a database | become `like` through the column default (dev had 0 rows at migration time) |
| `spark_count` on a post | **unchanged meaning**: legacy weighted heart total — one per row, a supernova counts 5. A Love and a Like each move it by one; **replacing one with the other does not move it** |
| `reaction_counts` | people per reaction, computed from the rows at read time (`GROUP BY post_id, reaction`), never stored — so it cannot drift |
| `viewer_sparked` | true iff the viewer has any reaction |
| `POST /spark` when a reaction exists | still `409 already sparked` — a legacy client that wants to change its mind unsparks first, as it always had to |
| `DELETE /spark` | removes the viewer's reaction whatever it is (a Love set on the web is un-hearted by mobile) and takes back the row's legacy weight |
| A supernova (`is_supernova:true`) | still weighs 5 in `spark_count`, counts as 1 person under `like` in `reaction_counts`; removing it through either route takes back 5 |

Two hardening changes rode along and are deliberate: (1) `SparkGroupPost` / `UnsparkGroupPost` used to check only post∈group, so a non-member could spark inside a private group and a banned member could keep sparking — both now pass the same gate as reactions (`engagementGate`: group access, ban, post∈group); (2) the legacy spark insert and its counter update are now one transaction (they were two autocommit statements).

The sparked event (`PublishGroupPostSparked`) and the member stat (`IncrementMemberSparks`) fire once, on a **first** reaction only — never on a replace or a retry — so the author's notification does not change shape or count.

Not done, by the brief: no change to the five-group cross-post cap, no new event types, no reaction on comments, no channel-service change (its `[{emoji,count}]` list shape was inspected; it has no allowlist to reuse, so group posts use an object keyed by reaction — cheaper for a client to index).

## Exact request/response bodies through the real dev gateway

Captured 26 Sep with the dev test accounts A (`call_a`) and B (`call_b`) on a disposable public group, created and deleted by the capture script (`scratchpad/capture-reactions.mjs`); residue purged to zero afterwards. Post objects below are trimmed to the reaction fields; every other post field is unchanged.

```
1. A sets love (first reaction)
PUT /v1/groups/{g}/posts/v2/{p}/reaction   {"reaction":"love"}
→ 200 {"data":{"post_id":"{p}","reaction":"love","spark_count":1,"reaction_counts":{"love":1},"viewer_sparked":true}}

2. A retries love (idempotent)
PUT …/reaction   {"reaction":"love"}
→ 200 {"data":{"post_id":"{p}","reaction":"love","spark_count":1,"reaction_counts":{"love":1},"viewer_sparked":true}}

3. A changes to smile (replace — spark_count does not move)
PUT …/reaction   {"reaction":"smile"}
→ 200 {"data":{"post_id":"{p}","reaction":"smile","spark_count":1,"reaction_counts":{"smile":1},"viewer_sparked":true}}

4. B sets like
PUT …/reaction   {"reaction":"like"}
→ 200 {"data":{"post_id":"{p}","reaction":"like","spark_count":2,"reaction_counts":{"like":1,"smile":1},"viewer_sparked":true}}

5. B sends 'heart' (outside the allowlist)
PUT …/reaction   {"reaction":"heart"}
→ 422 {"error":{"code":"VALIDATION_ERROR","message":"invalid: reaction must be one of like, love, smile, wow, sad, angry"},"meta":{"request_id":"…"}}

6. B sends no reaction
PUT …/reaction   {}
→ 400 {"error":{"code":"INVALID_REQUEST","message":"reaction is required"},"meta":{"request_id":"…"}}

7. GET /v1/groups/{g}/posts/v2/{p}   (as A)
→ 200 {"data":{"id":"{p}","spark_count":2,"viewer_sparked":true,"viewer_reaction":"smile","reaction_counts":{"like":1,"smile":1}, …}}

8. GET /v1/groups/{g}/feed/v2?limit=5   (as B)
→ 200 {"data":[{"id":"{p}","spark_count":2,"viewer_sparked":true,"viewer_reaction":"like","reaction_counts":{"like":1,"smile":1}, …}]}

9. GET /v1/groups/{g}/posts/v2/search?q=capture&limit=5   (as B)
→ 200 {"data":[{"id":"{p}","spark_count":2,"viewer_sparked":true,"viewer_reaction":"like","reaction_counts":{"like":1,"smile":1}, …}]}

10. GET /v1/groups/feed?limit=5   (cross-group feed, as A)
→ 200 {"data":[{"id":"{p}","spark_count":2,"viewer_sparked":true,"viewer_reaction":"smile","reaction_counts":{"like":1,"smile":1}, …}]}

11. B legacy POST …/spark while holding a like   {"is_supernova":false}
→ 409 {"error":{"code":"CONFLICT","message":"already sparked"},"meta":{"request_id":"…"}}

12. B removes reaction
DELETE …/reaction
→ 200 {"data":{"post_id":"{p}","reaction":null,"spark_count":1,"reaction_counts":{"smile":1},"viewer_sparked":false}}

13. B removes again (safe retry)
DELETE …/reaction
→ 200 {"data":{"post_id":"{p}","reaction":null,"spark_count":1,"reaction_counts":{"smile":1},"viewer_sparked":false}}

14. B legacy POST …/spark   {"is_supernova":false}
→ 200 {"data":{"ok":true}}

15. GET /v1/groups/{g}/posts/v2/{p}   (as B — a heart reads as like)
→ 200 {"data":{"id":"{p}","spark_count":2,"viewer_sparked":true,"viewer_reaction":"like","reaction_counts":{"like":1,"smile":1}, …}}

16. B legacy DELETE …/spark
→ 200 {"data":{"ok":true}}

17. A reacts via a wrong group id
PUT /v1/groups/{random}/posts/v2/{p}/reaction   {"reaction":"like"}
→ 404 {"error":{"code":"NOT_FOUND","message":"not found: group not found"},"meta":{"request_id":"…"}}

18. No token
PUT …/reaction   {"reaction":"like"}
→ 401 {"error":{"code":"UNAUTHORIZED","message":"Invalid user ID"},"meta":{"request_id":"…"}}
```

The private-group / banned refusals were exercised by the service-level integration test (below) rather than through the gateway — they need a private group and a banned membership, which the two dev accounts' public fixture does not have; the mapping is `not a member` → 404 and `forbidden` → 403 through the same `handleServiceError` the captures above went through.

## Tests (all green; every guard mutation-checked)

Unit / structural (`go test ./...`):
- `service.TestReactionAllowlistMatchesDatabaseCheck` — Go allowlist == CHECK in migration 016 == CHECK in setup.sql; covers like/love/smile. *Mutation: add `'heart'` to the CHECK → fails.*
- `service.TestValidateReaction` — normalisation and refusals; error wording carries `invalid` so it maps to 422.
- `service.TestEveryEngagementWritePassesTheGate` — Set/Remove/Spark/Unspark all call `engagementGate`; the gate checks access, ban, post∈group, published. *Mutation: spark bypasses the gate → fails.*
- `service.TestReactionIsValidatedBeforeAnyRead`, `TestWritesAnswerWithReadBackState`, `TestEventAndMemberStatsOnlyOnInsert`. *Mutation: stats outside the Inserted branch → fails.*
- `http.TestReactionRoutesAreRegisteredAndSparkRoutesRemain` — real engine; both new routes plus both legacy routes. *Mutation: PUT route removed → fails.*
- `http.TestReactionOutsideAllowlistIs422` — through the real error mapping; the message lists the allowlist.
- `store.TestEveryClientPostSurfaceAttachesReactionCounts` — the five client surfaces call `attachReactionCounts`. *Mutation: cross-group feed stops attaching → fails.*
- `store.TestViewerReactionIsSelectedAndScanned`, `TestLegacySparkInsertsALikeRow`, `TestReactionCountsNeverMarshalAsNull` (*mutation: nil-map normalisation removed → fails*), plus the existing projection/scanner arity guard and the wire golden-keys test (both extended).

Integration (`GROUP_POSTGRES_DSN=…/group_it_test go test -tags integration ./internal/store/ ./internal/service/ -run 'Reaction|LegacySparkAndReaction' -p 1`, `*_test` databases only, the real migration 016 applied by the fixture):
- `TestReactionReplaceIdempotentRemove` — replace keeps `spark_count` at 1 (*mutation: replace also increments → fails with `spark_count after replace = 2`*); retry no-op; remove then remove again (*mutation: second remove errors → fails*).
- `TestReactionConcurrentRetriesCountOnce` — 8 concurrent first likes → 1 row, 1 count, exactly one `Inserted`; 8 concurrent mixed replacements → still 1 row, counts sum to 1.
- `TestLegacySparkAndReactionShareOneRow` — heart reads as like; double heart 409; heart→love keeps count 1; heart over love 409; legacy unspark removes the love; supernova weighs 5 and is taken back on reaction removal.
- `TestReactionsOnEveryReadSurface` — viewer_reaction + reaction_counts on single post, group list, search, cross-group feed, anonymous viewer, and the pending queue (`{}`).
- `TestReactionCheckConstraintRefusesUnknownEmoji` — raw `'heart'` insert refused by the CHECK.
- `service.TestReactionAccessRefusalsMutateNothing` — stranger in a private group, banned member, wrong group, unlisted reaction, unknown group: all refused on set, remove and legacy spark; `spark_count` and rows unchanged; reload shows the member's reaction persisted; removal answers the read-back state.

## Deploy

- `docker compose build group-service && docker compose up -d --no-build --force-recreate group-service` — done on dev; boot log `migration applied … 016_group_post_reactions.sql`; running container image == built image; `app.group_post_sparks` has `reaction`, `updated_at`, the CHECK and the index.
- One boot-order trap found and fixed: `BootstrapSchema` runs `setup.sql` before migrations, and on a database whose sparks table predates the column a `CREATE INDEX … (post_id, reaction)` in `setup.sql` fails the boot. The index therefore lives only in migration 016; a comment in `setup.sql` says why. Verified by replaying setup.sql + 016 on both a clean database and a pre-016 one.

## For web wiring (Codex)

- Reaction picker on `GroupPostCard`: `PUT …/reaction {reaction}` on pick, `DELETE …/reaction` on un-pick; patch the card from the **response** (`reaction`, `spark_count`, `reaction_counts`, `viewer_sparked`), do not compute counts client-side. `applyGroupFeedPatch` already knows how to update a post across the group feed, search and cross-group feed pages.
- Read `post.viewer_reaction` (string | null) and `post.reaction_counts` (object; may be `{}`). Keep the existing `viewer_sparked === true` read — it stays correct.
- The heart button, if kept as a shortcut, is `PUT {reaction:"like"}` / `DELETE …/reaction`; prefer these over the legacy `/spark` pair so a change of mind is one request.
- Show the 422 message verbatim only in dev; the allowlist is fixed, so the picker should never send anything else.
- Anonymous posts: the mask is unchanged; reactions carry no author information.
