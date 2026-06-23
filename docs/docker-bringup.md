# Docker Bring-Up Guide — AtPost / VChat

Everything you need to run the full 30-service stack locally in one command.
For the first-time Ubuntu setup checklist see `docs/session_resume_ubuntu.md`.

---

## TL;DR

```bash
cd ~/code/Vchat/modernsmapp      # or wherever the repo lives
bash scripts/docker-bringup.sh
```

That's it. The script handles everything below automatically.

---

## What the script does (step by step)

### 1 — Fix Docker daemon (`/etc/docker/daemon.json`)

**Problem:** IPv6 addresses from Jio/Airtel ISPs (`2401:4900:…`) have
unreliable routing to US CloudFront (where Docker Hub stores image layers).
The symptom is a `connection reset by peer` error mid-download, e.g.:

```
read tcp [2401:4900:88f5:7308:...]:49570 →
         [2600:9000:2572:b000:9:4855:aac0:93a1]:443: read: connection reset
```

**Fix:** Write `/etc/docker/daemon.json` to:
- Pin DNS to Google's resolvers (`8.8.8.8`, `1.1.1.1`) — both reliably
  return IPv4 A records for `registry-1.docker.io`
- Set `"ip6tables": false` — prevents Docker from registering IPv6 NAT rules,
  which forces its internal resolver to prefer IPv4

```json
{
  "dns": ["8.8.8.8", "1.1.1.1"],
  "ip6tables": false
}
```

Requires `sudo`. The script calls `sudo tee` + `sudo systemctl restart docker`.
Skip with `--skip-daemon-fix` if you've already done this.

### 2 — Pull infra images with retry

Nine infra images are pulled **one at a time** with exponential back-off
(5 attempts: 10 s → 20 s → 40 s → 80 s between retries):

| Image | Purpose |
|---|---|
| `postgis/postgis:16-3.4` | PostgreSQL 16 with PostGIS |
| `redis:7` | Cache / sessions / pub-sub |
| `scylladb/scylla:5.4` | High-cardinality time-series (feeds, chats) |
| `jaegertracing/all-in-one:latest` | Distributed tracing UI |
| `redpandadata/redpanda:latest` | Kafka-compatible event streaming |
| `redpandadata/console:latest` | Redpanda web UI |
| `minio/minio:latest` | S3-compatible object storage |
| `opensearchproject/opensearch:2` | Full-text search |
| `bluenviron/mediamtx:latest` | RTMP/HLS live-stream relay |

### 3 — Start infra first

Uses `docker-compose.infra.yml` (infra only, no Go services):

```bash
docker compose -f Architecture/docker/docker-compose.infra.yml \
  --env-file Architecture/docker/local.env up -d
```

Then waits for health checks:
- **postgres** — `pg_isready` (up to 60 s)
- **redis** — `redis-cli ping` (up to 30 s)
- **scylla** — `cqlsh -e 'DESCRIBE KEYSPACES'` (up to 240 s — ScyllaDB is slow)
- **redpanda** — broker ready (up to 90 s)
- **OpenSearch** — HTTP `GET /_cluster/health` (up to 120 s)
- **MinIO** — HTTP `GET /minio/health/live` (up to 60 s)

### 4 — Build Go services sequentially

**Why sequential?** Building 30+ services in parallel overwhelms Docker's
layer cache and causes intermittent `EOF` / `unexpected EOF` errors. The
handoff doc explicitly noted this. The script runs:

```bash
docker compose build <service>
```

…for each service in order, one at a time.

#### Skipped services (port 8117 collision)

| Service | Why skipped |
|---|---|
| `feature-flag-service` | Port 8117 already bound by `ai-service` |
| `live-service-v2` | Also attempts port 8117; use `live-service` instead |

These are passed as `--scale svc=0` to `docker compose up` so they are never started.

### 5 — Start everything

```bash
docker compose -f Architecture/docker/docker-compose.yml \
  --env-file Architecture/docker/local.env up -d \
  --scale feature-flag-service=0 --scale live-service-v2=0
```

### 6 — Health verification

The script polls these endpoints (with retry) and reports HTTP status:

| Endpoint | URL |
|---|---|
| API Gateway | `http://localhost:8080/health` |
| Identity Auth | `http://localhost:8081/health` |
| User Service | `http://localhost:8082/health` |
| Group Service | `http://localhost:8090/health` |
| Chat WS Gateway | `http://localhost:8093/health` |
| Redpanda Console | `http://localhost:8085` |
| MinIO Console | `http://localhost:9001` |
| Jaeger UI | `http://localhost:16686` |

---

## Script options

```
bash scripts/docker-bringup.sh [options]

  --skip-daemon-fix   Skip daemon.json rewrite (already patched)
  --infra-only        Only start infra containers; skip service builds
  --rebuild           Add --no-cache to all docker compose build calls
  --help              Print this help
```

### Common invocations

```bash
# First time on a new machine (requires sudo for daemon.json)
bash scripts/docker-bringup.sh

# After you've already fixed the daemon — just bring services up
bash scripts/docker-bringup.sh --skip-daemon-fix

# Just start infra so you can run Go services locally via run-local.sh
bash scripts/docker-bringup.sh --infra-only

# Nuclear rebuild (clears Docker build cache)
bash scripts/docker-bringup.sh --skip-daemon-fix --rebuild
```

---

## One-time prerequisites

### 1. Add your user to the docker group

Without this every `docker` call says `permission denied`.

```bash
sudo usermod -aG docker $USER
# then log out and log back in, OR:
newgrp docker    # activates the group for the current shell session only
```

### 2. Check available RAM

| Component | Minimum RAM |
|---|---|
| ScyllaDB 5.4 | 2 GB |
| OpenSearch 2 | 2 GB |
| All 30 Go services | ~4 GB |
| **Total recommended** | **16 GB** |

OpenSearch will OOM-kill itself on machines with < 4 GB free. If that happens:

```bash
# Limit JVM heap in docker-compose.yml or set env:
OPENSEARCH_JAVA_OPTS="-Xms512m -Xmx512m"
```

### 3. Disk space

Docker images + volumes need ~15 GB free. Check with `df -h`.

---

## Teardown

```bash
# Stop everything (keeps volumes)
docker compose -f Architecture/docker/docker-compose.yml down
docker compose -f Architecture/docker/docker-compose.infra.yml down

# Stop and delete all data volumes (full reset)
docker compose -f Architecture/docker/docker-compose.yml down -v
docker compose -f Architecture/docker/docker-compose.infra.yml down -v

# Remove all unused images to free disk
docker image prune -a
```

---

## Running the Next.js web UI

The web front-end (`postbook-ui/`) is not a Docker service — run it directly:

```bash
cd /home/raghuvaran/code/Vchat/postbook-ui
npm install
npm run dev
# → http://localhost:3000
```

Requires Node 20+. Install with: `nvm install 20 && nvm use 20`

---

## Troubleshooting

### `connection reset by peer` when pulling images

Root cause: IPv6 instability to Docker Hub's CloudFront CDN.
Fix: ensure daemon.json has been patched (step 1 of the script).

```bash
cat /etc/docker/daemon.json
# should show: "ip6tables": false
```

If not, run the script without `--skip-daemon-fix`.

### `port is already allocated`

Another process holds a port. Find it:

```bash
sudo ss -tlnp | grep <port>
sudo kill <pid>
```

### ScyllaDB never becomes healthy

ScyllaDB is the slowest to start (can take 3–4 minutes). Wait longer, or:

```bash
docker logs atpost_infra-scylla-1 --tail 50
```

Look for `Starting listening for CQL clients` — that's the ready signal.

### `feature-flag-service` or `live-service-v2` won't start

These are intentionally skipped. They both try to bind port 8117 which
`ai-service` already owns. If you need feature flags, the endpoint is
served by `ai-service` directly in this local setup.

### Build EOF errors

If a `docker compose build` step dies with `unexpected EOF`, it's a transient
network blip. The script already builds sequentially to minimize this, but if
it still happens:

```bash
# Rebuild just the failed service
docker compose -f Architecture/docker/docker-compose.yml \
  --env-file Architecture/docker/local.env build <service-name>
```

### Out of disk space mid-build

```bash
docker builder prune --force     # clear build cache
docker image prune -a --force    # remove dangling images
```

---

## Port map (quick reference)

| Port | Service |
|---|---|
| 8080 | API Gateway |
| 8081 | Identity Auth |
| 8082 | User Service |
| 8083 | Graph Service |
| 8084 | Post Service |
| 8085 | Redpanda Console |
| 8086 | Feed Service |
| 8087 | Media Service |
| 8088 | Notification Service |
| 8089 | Search Service |
| 8090 | Group Service |
| 8091 | Trust & Safety Service |
| 8092 | Chat Message Service |
| 8093 | Chat WS Gateway |
| 8094 | Analytics Service |
| 8096 | Admin Service |
| 8097 | Identity User |
| 8098 | Identity Profile |
| 8099 | Monetization Service |
| 8100 | Suggestion Service |
| 8102 | Payments Service |
| 8103 | Live Service |
| 8104 | Memories Service |
| 8106 | Channel Service |
| 8107 | Community Service |
| 8108 | QA Service |
| 8109 | Commerce Service |
| 8117 | AI Service (also wanted by feature-flag + live-v2) |
| 5432 | PostgreSQL |
| 6379 | Redis |
| 9042 | ScyllaDB (CQL) |
| 9092 | Redpanda (Kafka) |
| 9000 | MinIO S3 API |
| 9001 | MinIO Console |
| 9200 | OpenSearch HTTP |
| 9300 | OpenSearch transport |
| 16686 | Jaeger UI |
| 1935 | MediaMTX RTMP |
| 3000 | Next.js web UI (npm run dev) |
