# C:\workspace\atpost\Architecture\docker\start-stack.ps1
# Bring up the full AtPost stack on a Windows dev box.

$env:DOMAIN = "cleestudio.com"

# ── Force serial builds + legacy builder (the actual fix for parallel-build OOM) ──
$env:COMPOSE_PARALLEL_LIMIT   = "1"
$env:DOCKER_BUILDKIT          = "0"
$env:COMPOSE_DOCKER_CLI_BUILD = "0"

$compose = @("-f", "docker-compose.yml", "-f", "docker-compose.external.yml")

# ── Infra (no builds) ─────────────────────────────────────────────────────
docker compose @compose up -d `
    postgres redis redpanda scylla minio opensearch `
    redpanda-init scylla-init

# ── Batch 1: identity + core social + media + notifications ───────────────
docker compose @compose up -d --build `
    identity-auth identity-profile identity-user `
    post-service feed-service graph-service user-service `
    search-service media-service notification-service `
    suggestion-service analytics-service

# ── Batch 2: community + chat + Q&A + gateway + live ──────────────────────
docker compose @compose up -d --build `
    community-service group-service channel-service qa-service `
    mediamtx chat-message-service chat-ws-gateway live-service `
    api-gateway call-service

# ── Batch 3: commerce + ops + admin ───────────────────────────────────────
docker compose @compose up -d --build `
    shop-service orders-service payments-service commerce-service `
    monetization-service admin-service feature-flag-service `
    trust-safety-service memories-service media-worker

# ── Batch 4: Pulse + Wallet + Food (Wallet must be up before Batch 5) ─────
docker compose @compose up -d --build `
    dating-service `
    wallet-service `
    food-service

# ── Batch 5: mini-apps that depend on wallet-service ──────────────────────
docker compose @compose up -d --build `
    bill-pay-service `
    rider-service

# ── Frontend (Next.js inside compose + Caddy reverse proxy) ───────────────
docker compose @compose up -d nextjs caddy

# ── Web dev server (separate window) ──────────────────────────────────────
# Open a NEW PowerShell window for these — they're long-running:
#   cd C:\workspace\postbook-ui ; npm run dev
#   cloudflared tunnel run aa9b62d9-bfff-4b23-86d0-81c4e16715b1
#
# Or run them inline if you don't need the dev server:
# Start-Process pwsh -ArgumentList '-NoExit', '-Command', 'cd C:\workspace\postbook-ui; npm run dev'
# Start-Process pwsh -ArgumentList '-NoExit', '-Command', 'cloudflared tunnel run aa9b62d9-bfff-4b23-86d0-81c4e16715b1'

Write-Host ""
Write-Host "Stack up. Quick health check:" -ForegroundColor Green
Write-Host "  docker compose ps"
Write-Host "  curl http://localhost:8112/healthz   # dating-service (Pulse)"
Write-Host "  curl http://localhost:8113/healthz   # food-service (FiGo)"
Write-Host "  curl http://localhost:8114/healthz   # wallet-service"
Write-Host "  curl http://localhost:8115/healthz   # bill-pay-service"
Write-Host "  curl http://localhost:8116/healthz   # rider-service (Mopedu)"
Write-Host ""
Write-Host "Kafka topics (should include dating-events, wallet-events, billpay-events, rider-events):" -ForegroundColor Green
Write-Host "  docker compose exec redpanda rpk topic list"
