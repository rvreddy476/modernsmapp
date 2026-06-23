# How to Run the Stack

Everything you need to bring up AtPost / VChat locally.

---

## One-time setup (do this once after a fresh install)

### 1. Add your user to the docker group
```bash
sudo usermod -aG docker $USER
# Log out and log back in, OR run:
newgrp docker
```

### 2. Fix Docker for Indian ISP (Jio/Airtel IPv6 instability)
```bash
sudo tee /etc/docker/daemon.json > /dev/null <<'EOF'
{
  "dns": ["8.8.8.8", "1.1.1.1"],
  "ip6tables": false
}
EOF
sudo systemctl restart docker
```

### 3. Build all service images (first time only, ~30 min)
```bash
cd ~/code/Vchat/modernsmapp

# These files must exist (empty is fine — Go uses vendor/ not sum checks)
touch Architecture/go.work.sum identity-platform/go.work.sum chat-service/go.work.sum

# Build each service one at a time (parallel builds cause EOF errors)
COMPOSE="docker compose -f Architecture/docker/docker-compose.yml --env-file Architecture/docker/local.env"

for svc in api-gateway identity-auth identity-user identity-profile \
  user-service graph-service group-service post-service feed-service \
  media-service media-worker notification-service search-service \
  trust-safety-service chat-message-service chat-ws-gateway call-service \
  monetization-service analytics-service ai-service admin-service \
  payments-service suggestion-service commerce-service food-service \
  wallet-service bill-pay-service rider-service live-service \
  memories-service channel-service community-service qa-service dating-service; do
  echo "Building $svc..."
  $COMPOSE build "$svc"
done
```

---

## Every time: Start the stack

```bash
cd ~/code/Vchat/modernsmapp
COMPOSE="docker compose -f Architecture/docker/docker-compose.yml --env-file Architecture/docker/local.env"

# Step 1 — Start infra first
$COMPOSE up -d postgres redis scylla redpanda minio opensearch jaeger mediamtx

# Step 2 — Wait for infra to be healthy (scylla takes ~60s)
echo "Waiting for postgres..."
until $COMPOSE exec -T postgres pg_isready -U postgres &>/dev/null; do sleep 3; done

echo "Waiting for redis..."
until $COMPOSE exec -T redis redis-cli ping &>/dev/null; do sleep 3; done

echo "Waiting for scylla (slowest, up to 3 min)..."
until $COMPOSE exec -T scylla cqlsh -e "DESCRIBE KEYSPACES" &>/dev/null; do sleep 5; done

echo "Waiting for redpanda..."
until nc -z localhost 9092; do sleep 3; done

echo "All infra ready."

# Step 3 — Start all services (skip the two with port 8117 collision)
$COMPOSE up -d --remove-orphans \
  --scale feature-flag-service=0 \
  --scale live-service-v2=0
```

---

## Check status

```bash
# Quick overview — everything should say "Up"
docker ps -a --format "table {{.Names}}\t{{.Status}}" | grep atpost | sort

# Check a specific service log
docker logs atpost_stack-api-gateway-1 --tail 20

# Key health endpoints
curl http://localhost:8080/health   # API Gateway
curl http://localhost:8081/health   # Identity Auth
curl http://localhost:8090/health   # Group Service
```

### Web UIs
| URL | What |
|---|---|
| http://localhost:8080 | API Gateway |
| http://localhost:8085 | Redpanda Console (Kafka topics) |
| http://localhost:9001 | MinIO Console (object storage) |
| http://localhost:16686 | Jaeger (distributed traces) |
| http://localhost:9200 | OpenSearch |

---

## Run the web frontend

```bash
cd ~/code/Vchat/postbook-ui
bun install   # first time only (~5x faster than npm)
bun run dev
# → http://localhost:3000
```

---

## Expose to the internet (Cloudflare Tunnel → cleestudio.com)

### First time only
```bash
bash scripts/setup-cloudflare-tunnel.sh
# Opens a browser tab — log in to Cloudflare and select the cleestudio.com zone
```

### Every time (after Docker stack is up)
```bash
cloudflared tunnel run atpost-local
```

Or as a background system service (survives reboots):
```bash
sudo cloudflared service install
sudo systemctl enable --now cloudflared
```

### Routes
| URL | Local service |
|---|---|
| https://cleestudio.com | Web UI (port 3000) |
| https://api.cleestudio.com | API Gateway (port 8080) |
| https://ws.cleestudio.com | Chat WebSocket (port 8093) |
| https://media.cleestudio.com | Media Service (port 8087) |

> **Order matters:** start Docker stack first, then run the tunnel.

---

## Stop the stack

```bash
cd ~/code/Vchat/modernsmapp

# Stop everything, keep data volumes (fast restart next time)
docker compose -f Architecture/docker/docker-compose.yml down

# Stop and WIPE all data (use when schema migrations fail on restart)
docker compose -f Architecture/docker/docker-compose.yml down -v
```

---

## If something crashes on startup

Services that fail immediately are almost always a **startup race** — they ran their DB migration before another service had time to create its tables. Fix:

```bash
# Wait ~30s for all services to finish their schema setup, then restart crashed ones
docker ps -a --format "{{.Names}}\t{{.Status}}" | grep "Exited (1)"

# Restart them
docker start atpost_stack-<service-name>-1
```

### Known issues and fixes

| Problem | Fix |
|---|---|
| `connection reset by peer` pulling images | daemon.json not patched — redo step 2 of one-time setup |
| `port is already allocated` | Another process holds the port: `sudo ss -tlnp \| grep <port>` then kill it |
| Service exits with `relation "X" does not exist` | Race condition — wait 30s then `docker start` the failed container |
| `column "option_id" does not exist` on post-service | Stale postgres volume — run `docker compose down -v` then restart |
| ScyllaDB never healthy | It takes 2-4 min on first boot — wait longer before starting services |
| Build `unexpected EOF` | Transient network blip — rerun `docker compose build <service>` |

### Intentionally skipped services
- `feature-flag-service` — port 8117 conflict with `ai-service`
- `live-service-v2` — also wants port 8117; use `live-service` instead

---

## Port reference

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
| 8091 | Trust & Safety |
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
| 8117 | AI Service |
| 5432 | PostgreSQL |
| 6379 | Redis |
| 9042 | ScyllaDB |
| 9092 | Redpanda (Kafka) |
| 9000 | MinIO S3 API |
| 9001 | MinIO Console |
| 9200 | OpenSearch |
| 16686 | Jaeger UI |
| 3000 | Web UI (bun run dev) |
