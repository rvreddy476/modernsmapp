# Devbox: the dev stack on one AWS server

The whole local Docker Compose stack (57 services) moves off the laptop to
one EC2 server in AWS account **551626543760**, region **ap-south-1**.
AWS S3 replaces MinIO. The laptop no longer runs Docker for the stack.

- **Server:** m7i.2xlarge (8 vCPU, 32 GiB), Ubuntu 24.04, 200 GB encrypted
  gp3 disk, 8 GB swap. It has **no inbound ports**. You reach it through
  SSM Session Manager.
- **Public addresses stay the same:** `https://cleestudio.com`,
  `https://api-dev.cleestudio.com`, `admin.`, `partner.`. The same
  Cloudflare tunnel serves them, but it now runs on the server and points
  at the same `localhost` ports as on the laptop.
- **Storage:** five private S3 buckets: media, live-recordings,
  commerce-invoices, food-files and transfer. The containers use the
  server's instance role and hold no static AWS keys.
  - Browsers and the Android app get presigned HTTPS links
    (`https://<bucket>.s3.ap-south-1.amazonaws.com/...`).
  - The media bucket's CORS rules allow the web origins.
- **Fresh start:** the server begins with empty databases. Nothing is
  copied from the laptop except code and configuration.

Files:

| File | What it is |
|---|---|
| `devbox.cfn.yaml` | CloudFormation: instance, role, security group, buckets, optional night stop/start |
| `../../Architecture/docker/docker-compose.devbox.yml` | Compose override applied last on the server (S3, MinIO off, fresh-volume gaps) |
| `lib.sh` | Shared by the scripts |
| `remote/devbox-remote.sh` | The server-side helper the scripts run through SSM |
| `deploy-stack.sh` | Create/update the stack; wait for first boot |
| `push-code.sh` | Copy the committed code of both repos to the server |
| `push-env.sh` | Copy `.env` + the mounted `.secrets` files; add the bucket names |
| `push-tunnel.sh` | Move the Cloudflare tunnel to the server |
| `push-image.sh` | Copy the locally built admin console image (optional) |
| `make-egress-key.sh` | Key for LiveKit Cloud recordings (optional) |
| `up.sh` | `up`, `--no-build`, `status`, `logs <service>` |
| `start.sh` / `stop.sh` | Start/stop the instance |
| `shell.sh` | A shell on the server, or a port forward |

The scripts:

- run from **Git Bash** on the laptop;
- use `AWS_PROFILE=atpost-qa` (the default);
- refuse to run against any other AWS account;
- never print a secret;
- can be re-run safely (idempotent).

## One-time setup on the laptop

1. Install the AWS Session Manager plugin, which `shell.sh` needs:
   <https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html>
2. Sign in to AWS:
   ```bash
   aws sso login --profile atpost-qa
   ```
3. The lead commits `infra/devbox/` and
   `Architecture/docker/docker-compose.devbox.yml`. `push-code.sh` copies
   only what is committed at HEAD, so these must be committed first.

## Bring it up (in this order)

From Git Bash:

```bash
cd /c/workspace/modernsmapp/infra/devbox
export AWS_PROFILE=atpost-qa
```

1. Create the server and buckets. This takes about 5 minutes and waits for
   first boot to finish.
   ```bash
   bash deploy-stack.sh
   ```
   To stop the server every night (23:30 IST) and start it every morning
   (08:00 IST), use this instead:
   ```bash
   DEVBOX_NIGHTLY=true bash deploy-stack.sh
   ```
2. Copy the code. This sends a git bundle of modernsmapp at HEAD and one of
   `C:\workspace\wt\pb-integ` at HEAD.
   ```bash
   bash push-code.sh
   ```
3. Copy the configuration: `.env`, the FCM key and the Google route key.
   The bucket names are added on the server.
   ```bash
   bash push-env.sh
   ```
4. **Stop the laptop tunnel**, then move it to the server.
   - If cloudflared runs in a terminal, press Ctrl+C there.
   - If it runs as a Windows service, use admin PowerShell:
     `Stop-Service cloudflared; Set-Service cloudflared -StartupType Disabled`
   ```bash
   bash push-tunnel.sh
   ```
5. Start the stack. The first run builds every image, which takes a while.
   ```bash
   bash up.sh
   bash up.sh status        # repeat until everything is Up / Exited (0)
   ```
   Most services have no restart policy, as on the laptop. If `status`
   lists any as NOT HEALTHY once the build has finished, check their logs
   with `bash up.sh logs <service>`. Running `bash up.sh --no-build` once
   more starts whatever stopped.
6. Optional, only while Docker is still installed on the laptop: copy the
   admin console image. Until you do, `up.sh` starts everything except
   `admin-web`.
   ```bash
   bash push-image.sh
   bash up.sh --no-build
   ```
7. Optional, for live-stream **recordings** with LiveKit Cloud (see
   "Live" below):
   ```bash
   bash make-egress-key.sh && bash push-env.sh && bash up.sh --no-build
   ```
8. Create the first accounts (next section).
9. Shut down the laptop stack for good, from PowerShell:
   `docker compose -p atpost_stack down`. Leave the laptop volumes alone
   unless you want them gone.

## The fresh stack

These set themselves up on the first `up.sh`, with no extra steps:

- **Postgres databases:** `app`, `identity_db`, `chat_db`,
  `commerce_db`, `feed_db` and `call_db`, from `init-databases.sql`.
- **Schemas and migrations:** every service applies its own when it
  boots (`BootstrapSchema` / the migration runner). The compose comment
  above identity-auth that says otherwise is out of date.
- **Seed rows that ship with a service's SQL:** feature flags, commerce
  categories, food cuisines and a demo service area, payments
  applications, the rider city.
- **Scylla keyspaces and tables:** created by `scylla-init`; post,
  analytics and chat add their own tables.
- **OpenSearch indices:** created by search-service.
- **Kafka topics:** created by `redpanda-init`, plus `devbox-topics`
  for the eight topics `init-topics.sh` never created. The laptop only
  had those from manual runs.
- **Creative Studio tables:** created by `devbox-studio-schema` from
  `database/08-creative-studio.sql`, which no service applies.
- **group-service** restarts on failure. On an empty database its
  migration can run before post-service has created `posts`.
- **S3 buckets** come from the stack. The services never create
  buckets.

These existed on the laptop only because of old data. Redo them on the
server:

- **User accounts.** They are not seeded (next section).
- **Pilot and allow-lists.** These hold user ids, and the compose file's
  defaults are the laptop's ids, which do not exist here:
  - `LIVE_PILOT_USER_IDS`
  - `DATING_PILOT_USER_IDS`
  - `DOORSTEP_PILOT_USER_IDS`
  - `PAGES_ADMIN_USER_IDS`
  - `COMMUNITIES_ALLOWED_CREATORS`
  - `COMMUNITIES_ALLOWED_PARTICIPANTS`

  Put the new ids in the laptop `.env`, then run
  `bash push-env.sh && bash up.sh --no-build`.
- **Admin console superadmin.** The laptop's superadmin was granted by
  SQL. On the server, set `SUPERADMIN_USER_IDS=<your new id>` in `.env`
  the same way. Then enrol 2FA as in
  `docs/runbooks/admin-console-weekend-test.md` (steps 2.1–2.3).
- **Optional demo data** (run the Go tools on the server):
  - Doorstep catalogue: `DOORSTEP_DEV_SEED=true` in `.env`, or
    `go run ./cmd/devseed`.
  - Doorstep coverage SQL:
    `services/doorstep-service/database/seeds/uppal_peerzadiguda_coverage.sql`.
  - Commerce demo shop:
    `go run ./cmd/seed-demo --yes --allow-db=commerce_db`.
  - Feast: `services/food-service/scripts/dev-seed-feast.sh` with
    `--owner/--rider/--customer/--admin <new ids>`.
  - Mopedu and Dating seeds: `dev-seed-mopedu.sh` and
    `dev-seed-dating.sh` have the old call_a/call_b ids written into
    them. The dating seed also needs the MinIO container, so it will not
    run on the devbox until it is changed.

## First test accounts (call_a, call_b, your own)

No script creates accounts. `identity-platform/seed.sql` is empty, and
`call_a` / `call_b` existed on the laptop only as registered users.
Register each one through the API, the same way `Architecture/docker/stack.sh`
does in its smoke test. Registration answers 201, and you can sign in
straight away.

Run this once per account. It prompts for the password, which is never
written down:

```bash
read -rp 'email: ' EMAIL; read -rsp 'password: ' PW; echo
curl -sS -X POST https://api-dev.cleestudio.com/v1/auth/register \
  -H 'Content-Type: application/json' \
  -d "{\"first_name\":\"Call\",\"last_name\":\"A\",\"gender\":\"male\",\"dob\":\"1991-05-15\",\"email\":\"$EMAIL\",\"password\":\"$PW\"}"; echo
unset PW
```

To read the new user ids, open a shell on the server:

```bash
bash shell.sh
sudo docker exec atpost_stack-postgres-1 psql -U postgres -d identity_db -c "select user_id, email from auth.users order by email"
```

Then set those ids in the `.env` variables listed above.

Verification codes, when a flow asks for one, appear in identity-auth's
log (SES is not configured). To read them:

```bash
bash up.sh logs identity-auth 50
```

## Every day

| What | Command |
|---|---|
| Deploy new commits (both repos) | `bash push-code.sh && bash up.sh` |
| `.env` changed | `bash push-env.sh && bash up.sh --no-build` |
| What is running / what failed | `bash up.sh status` |
| A service's log | `bash up.sh logs media-service 200` |
| Shell on the server | `bash shell.sh` (`sudo -i` for root; code in `/srv/atpost`) |
| Postgres on `localhost:15432` | `bash shell.sh port 5432 15432` |
| Stop paying for compute | `bash stop.sh` (the site is down until `bash start.sh`) |
| Start again | `bash start.sh` (the stack and the tunnel come back by themselves) |

On the server:

- the code is in `/srv/atpost/modernsmapp` and `/srv/atpost/web`;
- logs are in `/srv/atpost/logs`;
- first-boot output is in `/var/log/atpost-devbox-init.log`;
- the `atpost-stack.service` unit runs `docker compose up -d` at every
  boot.

## Storage, in detail

| Service | Before | Now |
|---|---|---|
| media-service, media-worker | MinIO `media`, static keys | S3 media bucket. Uses `MEDIA_STORAGE_BACKEND=s3` (the QA/production path) with the instance role. |
| media-service live-recording import | MinIO `live-recordings` | S3 live-recordings bucket, copied server-side. |
| commerce-service (invoices, bulk-import CSVs) | MinIO `commerce-invoices` | S3 bucket. Uses `COMMERCE_BLOB_BACKEND=s3`. |
| food-service (settlement files) | inline in Postgres (no store) | S3 food-files bucket. Uses `FOOD_BLOB_BACKEND=s3`. |
| livekit-egress (in-stack) | MinIO, static keys | S3 with the instance role. |
| live-service-v2 egress target | `media-dev` → minio-edge → MinIO | S3 regional endpoint. |
| web `/v1` media proxy | MinIO short-cut | Follows the redirect to S3, as in production. |

`S3_ALLOW_INSTANCE_ROLE=true` lets the three Go clients use the EC2
instance role:

- They accept it only when `ENV` is local/dev.
- QA and production still require IRSA.
- Static AWS keys are refused everywhere.

Media links are presigned S3 URLs that last 5 minutes for protected
media. There is no CloudFront on the devbox. Moderation and face checks
stay on the **mock**, as on the laptop. To try Rekognition, which is
available in ap-south-1 and allowed by the role, set
`MEDIA_SCANNER_BACKEND=rekognition` and/or
`MEDIA_FACE_COMPARE_BACKEND=rekognition` in `.env`, then run push-env
and up.

The tunnel's `media-dev.cleestudio.com` rule now has nothing behind it and
answers 502. No link points there any more.

## Live

`.env` points live-service-v2 at **LiveKit Cloud**, and that keeps working.
Phones cannot reach the in-stack LiveKit server on the devbox, because
there are no inbound ports, so the in-stack SFU, coturn and livekit-egress
sit idle.

Recordings are the one difference:

- LiveKit Cloud uploads them from outside AWS, so it cannot use the
  instance role.
- `make-egress-key.sh` creates an access key for the stack's
  `atpost-devbox-live-egress` user, which may only PutObject into the
  recordings bucket. The key goes straight into `.env` and is not printed.
- Without the key, streams still work and recordings fail to upload.

## Cost (estimate, on-demand, ap-south-1, October 2026 prices)

| Item | Per month |
|---|---|
| m7i.2xlarge, $0.4242/h, running 24×7 | ~$310 |
| m7i.2xlarge, running about 15.5 h/day (nightly stop 23:30–08:00) | ~$200 |
| 200 GB gp3, $0.0912/GB-month (billed even while stopped) | ~$18 |
| Public IPv4 address, $0.005/h while running | ~$2–4 |
| S3 Standard, $0.025/GB-month (e.g. 100 GB) plus requests | ~$3 |
| Data out to the internet, through the tunnel and direct S3 links. The first 100 GB/month across the account is free, then about $0.11/GB. | $0–10 |
| **Total** | **about $335 (24×7) or $225 (nightly stop)** |

These are estimates. Rekognition and SES are billed per call if you turn
them on.

## Removing it

```bash
aws cloudformation delete-stack --stack-name atpost-devbox --profile atpost-qa --region ap-south-1
```

The four data buckets are kept (`DeletionPolicy: Retain`). Empty and
delete them by hand if you no longer want them. The transfer bucket is
deleted with the stack, and only once it is empty.
