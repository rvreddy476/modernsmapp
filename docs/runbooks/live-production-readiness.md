# Runbook: Live (live-service-v2) production readiness

**Status (1 Oct 2026):** dev only. Going live is a **closed pilot**. **Moderation owner: NOT ASSIGNED.**
**Service:** `Architecture/services/live-service-v2` (port 8117). api-gateway proxies it at `/v1/livestream`.
**Scope:** this runbook lists what staging and production need before Live can run there. **No staging or production file has been changed.** Every values key below is a key to add later. None exists yet.

Decisions this implements (live-fix contract, 1 Oct 2026):

- v2 (LiveKit) is the only live stack. v1 is retired. api-gateway answers `/v1/live` and everything under it with `410 Gone` `{"error":{"code":"LIVE_V1_RETIRED"}}` and proxies nothing. `/v1/livestream` is unchanged. MediaMTX and `live-service` still run on dev but nothing uses them. Removing them comes later.
- Going live needs an id on `LIVE_PILOT_USER_IDS`. The check fails closed: an empty list means nobody can go live. Viewing is not affected.

---

## 1. What blocks a public launch

| Blocker | Why | Unblocks when |
|---|---|---|
| **No named moderation owner** | Live chat and video are user content shown in real time. Someone must own the report queue, live bans and emergency stops, with response times. | The founder names an owner. Only then may `LIVE_PILOT_USER_IDS` grow beyond internal accounts. Opening going-live to everyone also needs a code change (the allowlist is the only gate), so it goes through review. |
| No LiveKit deployment in staging or prod | No values file and no Terraform contains a LiveKit server, egress, TURN or their secrets. See §2–§4. | §2–§5 are deployed and §7 passes. |
| `live-service-v2` values carry no LiveKit or S3 secrets | Without them `POST /start` fails, and the webhook refuses every event (fail closed, by design). | §5 keys are added. |

Until the first row is cleared, staging and prod stay off: no Live in clients, no pilot list.

---

## 2. LiveKit server

- **Image:** `livekit/livekit-server` pinned to an exact patch version. Dev runs `v1.8` (resolves to **v1.8.4**). Pin the same version in prod, or move server and egress together (§3).
- **Config keys** (checked against livekit/livekit v1.8.4 `pkg/config/config.go`):
  ```yaml
  port: 7880
  rtc:
    tcp_port: 7881          # ICE/TCP fallback
    port_range_start: 50000 # or udp_port for a single muxed port
    port_range_end: 60000
    use_external_ip: true   # STUN-discovered public IP; or set node_ip explicitly
  keys:
    <API_KEY>: <API_SECRET> # from the secret store, never in a values file
  redis:                    # required for egress and for more than one node
    address: <elasticache-endpoint>:6379
    username: <user>
    password: <password>
    db: 0
    use_tls: true
  webhook:
    api_key: <API_KEY>      # must be one of `keys`; LiveKit signs with its secret
    urls:
      - http://live-service-v2.atpost.svc.cluster.local:8117/v1/livestream/webhooks/livekit
  turn:
    enabled: true
    domain: turn.<domain>
    tls_port: 5349
    udp_port: 3478
    external_tls: true      # when TLS is terminated at the load balancer
  ```
- **Networking:** the signalling port (7880) goes behind a TLS load balancer, `wss://live.<domain>`, which is the `LIVEKIT_PUBLIC_URL` clients dial. Media ports (UDP range and 7881/TCP) must reach the nodes directly: host networking or a NodePort/NLB per node. The usual pattern is a dedicated node group with one LiveKit pod per node. The Cloudflare tunnel cannot carry media.
- **Webhook delivery:** LiveKit POSTs each event with `Authorization: <JWT>`. The JWT is HS256, signed with the API secret, with `iss` = API key and claim `sha256` = base64(SHA-256 of the raw body). live-service-v2 checks exactly this (the contract's §1 webhook rule) and is idempotent on the event `id`. It refuses every webhook when the key or secret is unset. The scheme was checked against livekit/protocol v1.34.0 `webhook/verifier.go` (`Receive`: Authorization header, `auth.ParseAPIToken`, secret looked up by the token's key, `claims.Sha256` compared with base64 SHA-256 of the body). The old `LIVEKIT_WEBHOOK_SECRET` / `X-LiveKit-Signature` scheme no longer exists. Do not configure it.
- **Alternative: LiveKit Cloud.** Dev's `.env` currently points live-service-v2 at a LiveKit Cloud project. With Cloud:
  - Set webhooks in the Cloud project settings, not in config, at a **public** URL, for example `https://api.<domain>/v1/livestream/webhooks/livekit`.
  - Cloud egress uploads to the S3 target in each request, so the bucket must be reachable from the internet (AWS S3 in prod).
  - The in-stack `livekit` and `livekit-egress` services stay idle.

## 3. LiveKit Egress (recordings)

- **Image:** `livekit/egress`, version-paired with the server. Dev pins **v1.9.0**: it is built on the same psrpc line (v0.6.1) as server v1.8.4, so the two can share the redis bus. Upgrade both together, and check psrpc compatibility in the egress README before enabling `psrpc.compression`.
- **Config** (`EGRESS_CONFIG_BODY` or `EGRESS_CONFIG_FILE`; keys checked against egress v1.9.0 `pkg/config/base.go`, `service.go`, `storage.go`):
  ```yaml
  api_key: <API_KEY>
  api_secret: <API_SECRET>
  ws_url: wss://live.<domain>   # or the in-cluster ws URL
  redis:                        # MUST be the same redis the server uses
    address: ...
    username: ...
    password: ...
    db: 0
    use_tls: true
  health_port: 9090
  storage:                      # default only; live-service-v2 sends the S3 target per request
    s3:
      region: ap-south-1
      bucket: <recordings bucket>
      # access_key/secret omitted -> IAM role (IRSA) on the egress pods
  ```
- **Sizing:** room-composite egress runs headless Chrome. Plan about 2–4 vCPU and 2–4 GiB for each concurrent recording. Egress refuses a request when free CPU is below `cpu_cost.room_composite_cpu_cost`. Dev lowers that cost to fit a 2-CPU container. Production must not lower it; size the nodes instead.
- **Chrome sandbox:** off by default in v1.9. Turning it on needs the seccomp profile from the egress repo. No `SYS_ADMIN`.
- **Open item:** `StartEgressToS3` always sends `access_key` and `secret`. With IRSA they would be empty. Confirm egress then falls back to its IAM role, or give live-service-v2 a mode that omits the keys. Do this before prod.
- **VOD gap:** post-service turns `live.stream.vod_ready` into a long video only when the recording URL resolves to a registered `media_assets` row, either a `/v1/media/<id>/` URL or a matching `storage_key`. Egress writes to its own bucket, so nothing registers that asset today. The owners of live-service-v2 and media-service must close this before recordings can publish.

## 4. TURN

- Mobile networks need TURN/TLS on 443 or 5349. Use LiveKit's built-in TURN (`turn:` above) with a certificate for `turn.<domain>`, or an external coturn. Dev's coturn is for 1:1 calls only.

## 5. Secrets and values keys

Secrets go in the secret store under `atpost/<env>/live-service-v2` (ExternalSecret), never in a values file.

| Key | Where | Notes |
|---|---|---|
| `LIVEKIT_API_KEY`, `LIVEKIT_API_SECRET` | live-service-v2 (ExternalSecret), LiveKit server `keys`, egress | One pair per environment. The secret is at least 32 characters. Also verifies webhooks. |
| `LIVEKIT_URL` | live-service-v2 env | The server URL the service calls (Twirp, room admin). |
| `LIVEKIT_PUBLIC_URL` | live-service-v2 env | `wss://live.<domain>`, what devices dial. |
| `MINIO_ENDPOINT`, `MINIO_BUCKET_LIVE_RECORDINGS`, `MINIO_REGION`, `MINIO_USE_SSL` | live-service-v2 env | The egress S3 target. These are the variable names the service reads today; in prod they point at AWS S3. |
| `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY` | live-service-v2 (ExternalSecret), or none with IRSA (see §3 open item) | |
| `LIVE_RECORDING_PUBLIC_BASE_URL` | live-service-v2 env | Blank means `endpoint/bucket/key`. |
| `LIVE_PILOT_USER_IDS` | live-service-v2 env | Comma-separated user ids. Empty means nobody (fail closed). Internal accounts only until a moderation owner exists. |
| `LIVE_RECONNECT_GRACE` (60s), `LIVE_START_TIMEOUT` (120s) | live-service-v2 env | Lifecycle timeouts. A sweeper applies them every 15 s. |
| `SERVICE_CALLERS=admin-service`, `SERVICE_CALLER_ADMIN_SERVICE_{KID,PUBKEY,OPS}` | live-service-v2 | OPS = `live:streams.read,live:streams.stop,live:reports.read,live:reports.act,live:chat.moderate,live:users.ban`. Same contract as commerce and payments. |
| `LIVE_V2_SERVICE_URL` | admin-service, chat-ws-gateway (api-gateway already has it) | `http://live-service-v2.atpost.svc.cluster.local:8117`. |
| `ENABLE_LIVE_ROOMS=true` | chat-ws-gateway | Owner-checked `live:stream:<id>` rooms. `EnableScopedRooms` stays false. |
| `INTERNAL_SERVICE_KEY` | chat-ws-gateway → live-service-v2 internal viewer route | Already present in both. |
| Redis credentials | LiveKit server and egress | The same redis for both. |

Identity must ship the `live:*` permissions (superadmin and admin; moderators get `streams.read`, `reports.read`, `reports.act`, `chat.moderate`) before the admin console's Live page works. `streams.stop` and `users.ban` need step-up at the BFF.

## 6. Pilot allowlist

- Dev default: the founder's account and the `call_a` / `call_b` test accounts, by identity `auth.users.user_id`. Override with `LIVE_PILOT_USER_IDS` in `Architecture/docker/.env`.
- A non-listed user's create or start gets `403 LIVE_NOT_ENABLED`. Clients show "Going live is in a closed pilot".
- A platform live ban (admin `POST .../admin/users/:userId/live-ban`) blocks going live and chatting everywhere, even for a listed user.
- To add someone: append the id, then redeploy live-service-v2. Each addition is a decision for the moderation owner. Until one is named, the list holds internal accounts only.

### Founding creator badge cutoff (founder decision, 2 Oct 2026)

The Founding creator badge is earned by a creator's first stream that stays on air for 5 minutes while the founding window is open. The window closes **90 days after going live opens to everyone**. That date does not exist yet, so `LIVE_FOUNDING_CREATOR_UNTIL` stays unset (window open) during the pilot.

On the day going live opens to everyone, in the same change that opens it:

1. Set `LIVE_FOUNDING_CREATOR_UNTIL` to that day plus 90 days, as an RFC3339 time (for example, opening on 2027-01-10 gives `2027-04-10T00:00:00+05:30`), in every environment's live-service-v2 config.
2. Redeploy live-service-v2. A stream that started before the cutoff still qualifies; one that starts after it does not. Badges already granted are kept.

Do not open going live to everyone without setting this, or the badge stays earnable for ever.

## 7. Go-live checklist (per environment)

1. LiveKit server and egress are on the same paired versions and share one redis. Server logs show the webhook URL configured; egress logs show it registered with the cluster.
2. `POST /v1/livestream/webhooks/livekit` with no `Authorization` header gets 401. A request signed with the wrong secret gets 401.
3. A pilot host starts a stream. Status goes `starting` → `live` only after the host's track is published. Killing the host's network gives `reconnecting`, then `ended` (`host_lost`) after the grace period.
4. A viewer's count excludes the host. `viewer_peak` is the maximum seen.
5. Ending the stream writes an MP4 to the recordings bucket and emits `live.stream.vod_ready` (§3 VOD gap).
6. Admin console → Live: list, stop with a reason, resolve a report, live ban. Each writes an audit row.
7. `/v1/live/...` gets 410 `LIVE_V1_RETIRED`.

## 8. Dev specifics (for reference)

`Architecture/docker/docker-compose.yml`:

- `livekit` has the `redis` and `webhook` blocks and joins the private `livekit-media` bridge at **10.0.2.2**. The SFU advertises the emulator node IP, which no container could otherwise reach, so this is what lets egress subscribe to the media.
- `livekit-egress` (v1.9.0) writes to MinIO `live-recordings`. The one-shot `live-recordings-bucket` creates that bucket.
- When `.env` points `LIVEKIT_URL` at LiveKit Cloud, these in-stack pieces are idle (§2, alternative).

Sources checked (1 Oct 2026):

- https://raw.githubusercontent.com/livekit/livekit/v1.8.0/config-sample.yaml
- https://github.com/livekit/livekit/blob/v1.8.4/pkg/config/config.go
- https://github.com/livekit/protocol/blob/v1.34.0/webhook/verifier.go
- https://github.com/livekit/egress/blob/main/README.md
- https://github.com/livekit/egress/tree/v1.9.0/pkg/config
- go.mod of livekit v1.8.4 and egress v1.9.0 (psrpc v0.6.1)
