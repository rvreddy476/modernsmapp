# Dev smoke test

Run it after every dev deploy to confirm that the four launch apps (Social, Shorts,
Long video, Commerce) still work end to end on `https://api-dev.cleestudio.com`.

```bash
bash scripts/smoke/dev-smoke.sh                      # everything
bash scripts/smoke/dev-smoke.sh --only health        # no login, no writes
bash scripts/smoke/dev-smoke.sh --only post,video    # login + those groups + their cleanup
bash scripts/smoke/dev-smoke.sh --skip video --keep  # leave created data for inspection
node --test scripts/smoke/dev-smoke.test.mjs         # unit tests, no network
```

You need Node 20 or newer. The script has no dependencies and does not need jq.

| Flag | Meaning |
|---|---|
| `--api <url>` | Target. It must be localhost or a host with a `dev` label (`api-dev.`, `*.dev.*`). Any other host prints `REFUSED: <host> is not a dev host` and exits 2 before sending a single request. Hosts with `prod`, `staging` or `live` labels are always refused. |
| `--only a,b` / `--skip a,b` | Run or skip groups (`health login me feed post follow video shop cleanup logout`) or single steps (`post.heart`). `login` is added automatically when a selected step needs a session. |
| `--keep` | Skip cleanup. Prints a `KEEP <what> <id>` line for each item the run created. |
| `--video <file>` | Clip to upload (default: `scripts/smoke/fixtures/smoke-2s-portrait.mp4`). |
| `--follow-user <name>` | Account to follow (default `call_b`). |
| `--video-timeout <s>` | How long to wait for the transcode (default 300). |

The account comes from `.secrets/harness-test-account.env`
(`HARNESS_TEST_USERNAME`, `HARNESS_TEST_PASSWORD`). Set `HARNESS_ACCOUNT_FILE` to use a
different file. The username value is the account's email address, because
auth-service logs in by phone or email and cannot look up a username.

## What it checks

| Step | Route |
|---|---|
| health.gateway | `GET /health`, `GET /readyz` |
| health.features | `GET /v1/features` |
| health.auth | `GET /v1/auth/health` |
| health.social | `GET /v1/hashtags/trending` |
| health.shorts | `GET /v1/posts/trending?content_type=flick` |
| health.longvideo | `GET /v1/posts/trending?content_type=long_video` |
| health.shop | `GET /v1/commerce/categories` |
| login | `POST /v1/auth/login` `{identifier, password}`. Runs **once** per run. |
| me | `GET /v1/auth/me` |
| feed | `GET /v1/feed/home?limit=10` (a list, each item has an id) |
| post.create | `POST /v1/posts` with `Idempotency-Key`, `visibility: private` ("only you"), `content_type: post` |
| post.heart | `POST /v1/posts/:id/like` (posts take a heart only) |
| post.comment | `POST /v1/posts/:id/comments` `{text}` |
| post.comment-read | `GET /v1/posts/:id/comments` contains the comment with the same text |
| follow.resolve | `GET /v1/users/by-username/call_b` (app user-service is the username authority) |
| follow.check | `GET /v1/graph/relationship?user_id=&other_id=`. If the account already follows the user, the follow is left in place and `follow.follow` is skipped. |
| follow.follow | `POST /v1/graph/follow` `{user_id}` (`followed`, or `requested` for a private account) |
| follow.verify | the relationship read again |
| video.init | `POST /v1/media/init` `{file_type: video, mime_type, file_size_bytes, media_subtype: general, upload_purpose: composer}` |
| video.upload | `PUT <upload_url>`. Sends the raw bytes straight to the dev S3 bucket, with no auth header. |
| video.confirm | `POST /v1/media/confirm` `{media_id}` |
| video.ready | Polls `GET /v1/media/:id/status` every 3 s until `ready` + `passed` |
| video.post | `POST /v1/posts` with `content_type: flick` and `visibility: private`. A clip of a few seconds is a Short, so the long-video guards (title, channel) do not apply. |
| video.publish | `POST /v1/videos/:postId/publish`. It takes the **post** id, not the media id. |
| video.play | `GET /v1/media/:id/url`, then the playback URL. Expects either the HLS master (`application/vnd.apple.mpegurl`, starting with `#EXTM3U`) or the original as `video/*`. |
| shop.home | `GET /v1/commerce/home` |
| shop.pick | `GET /v1/commerce/cart`, `GET /v1/commerce/products?in_stock=true`, `GET /v1/commerce/products/:id`. Picks an active variant with stock, not already in the bag, from the bag's seller if the bag is not empty. |
| shop.add | `POST /v1/commerce/cart/items` `{variant_id, quantity: 1}` |
| shop.quote | `GET /v1/commerce/addresses`, then `POST /v1/commerce/checkout/quote` `{address_id}`. **The test stops here.** It never calls checkout or a payment intent. |
| cleanup.* | See below |
| logout | `POST /v1/auth/logout` with this run's refresh cookie, so smoke sessions do not pile up |

## Reading the output

```
PASS <step> (<ms>ms) [note]
FAIL <step> (<ms>ms) <reason>
SKIP <step> (needs <dep>)
RESULT PASS | RESULT FAIL (<n> failed)
```

The script exits 0 when everything passes, 1 when any step fails, and 2 on a refused
host or a usage error.

- `FAIL x HTTP <status> <body>`: the route answered something unexpected. The body is
  redacted (fields named like token, password, secret, authorization, cookie, email or
  phone are removed, along with JWTs, bearer values and signed-URL parameters) and cut
  to about 300 characters. These failures are usually the product, not the script.
- `FAIL x ... network error: <code>`: dev is down or unreachable.
- `FAIL video.ready timed out after 300s at processing_status=X moderation_status=Y`:
  the transcode or moderation stopped at that state. `processing` usually means the
  worker is stalled; see the memory note on stalled transcodes.
- `FAIL login ... HTTP 429`: the login limiter (10 per IP per 15 min) tripped. Wait 15
  minutes. Do not loop the script.
- `FAIL login ... step-up` or `... 2FA`: the account is asking for a second factor and
  needs fixing by hand.
- `FAIL shop.pick no in-stock products listed`: the dev storefront is empty.
- `FAIL shop.quote ... no saved address`: give `harness_check` a delivery address.
- `SKIP x (needs y)`: `y` failed or was not selected.
- `FAIL x script error: ...`: a bug in the script itself.

## Cleanup

Cleanup runs after the steps, in reverse order, even when steps failed and even after
Ctrl-C (press Ctrl-C a second time to quit at once). It touches only ids this run
created, and each action prints its own PASS or FAIL line:

1. `cleanup.bag`: `DELETE /v1/commerce/cart/items/:variantId`
2. `cleanup.comment`: `DELETE /v1/comments/:id`
3. `cleanup.post`, `cleanup.video-post`: `DELETE /v1/posts/:id`. These are soft deletes:
   the posts go to "Recently deleted", and the purge worker removes them and their
   media later.
4. `cleanup.media`: `DELETE /v1/media/:id`, only for an upload that never became a post.
5. `cleanup.unfollow`: `POST /v1/graph/unfollow`, or `DELETE /v1/graph/follow-requests/:id`
   for a pending request. This runs only if this run created the follow.

A price quote row (`shop.quote`) has no delete route. It expires on its own and
reserves no stock.

## Known gaps

- The default clip is the tracked `scripts/smoke/fixtures/smoke-2s-portrait.mp4`
  (13.5 KB, 2 s, portrait), so it uploads as a Short; the long-video guards
  (title, channel, length) are not exercised.
