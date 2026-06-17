#!/usr/bin/env bash
# ══════════════════════════════════════════════════════════════════════════════
# setup-cloudflare-tunnel.sh  —  One-shot Cloudflare Tunnel setup for AtPost
#
# Exposes the local Docker stack to the internet via cleestudio.com:
#   https://cleestudio.com          → Web UI      (port 3000)
#   https://api.cleestudio.com      → API Gateway (port 8080)
#   https://ws.cleestudio.com       → Chat WS     (port 8093)
#   https://media.cleestudio.com    → Media       (port 8087)
#
# Usage (run in your terminal — not via Claude, needs sudo + browser):
#   bash scripts/setup-cloudflare-tunnel.sh
# ══════════════════════════════════════════════════════════════════════════════
set -euo pipefail

DOMAIN="cleestudio.com"
TUNNEL_NAME="atpost-local"
CONFIG_DIR="$HOME/.cloudflared"

GREEN='\033[0;32m'; CYAN='\033[0;36m'; YELLOW='\033[1;33m'; RESET='\033[0m'
log()  { echo -e "${CYAN}[tunnel]${RESET} $*"; }
ok()   { echo -e "${GREEN}[  OK  ]${RESET} $*"; }
warn() { echo -e "${YELLOW}[ WARN ]${RESET} $*"; }

# ── Step 1: Install cloudflared ───────────────────────────────────────────────
# Uses the GitHub release binary — works on any distro/version including
# Ubuntu 26.04 "resolute" which isn't in Cloudflare's apt repo yet.
if ! command -v cloudflared &>/dev/null; then
  log "Installing cloudflared from GitHub releases..."
  curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 \
    -o /tmp/cloudflared
  sudo install -m 755 /tmp/cloudflared /usr/local/bin/cloudflared
  rm -f /tmp/cloudflared
  ok "cloudflared $(cloudflared --version) installed"
else
  ok "cloudflared already installed: $(cloudflared --version)"
fi

# ── Step 2: Authenticate (opens browser) ─────────────────────────────────────
if [[ ! -f "$CONFIG_DIR/cert.pem" ]]; then
  log "Authenticating with Cloudflare (a browser tab will open)..."
  log "Log in and select the '${DOMAIN}' zone when prompted."
  cloudflared tunnel login
  ok "Authenticated"
else
  ok "Already authenticated (cert.pem exists)"
fi

# ── Step 3: Create tunnel ─────────────────────────────────────────────────────
TUNNEL_ID=$(cloudflared tunnel list --output json 2>/dev/null \
  | python3 -c "import sys,json; tunnels=json.load(sys.stdin); \
    match=[t['id'] for t in tunnels if t['name']=='$TUNNEL_NAME']; \
    print(match[0] if match else '')" 2>/dev/null || true)

if [[ -z "$TUNNEL_ID" ]]; then
  log "Creating tunnel '$TUNNEL_NAME'..."
  cloudflared tunnel create "$TUNNEL_NAME"
  TUNNEL_ID=$(cloudflared tunnel list --output json 2>/dev/null \
    | python3 -c "import sys,json; tunnels=json.load(sys.stdin); \
      print([t['id'] for t in tunnels if t['name']=='$TUNNEL_NAME'][0])")
  ok "Created tunnel: $TUNNEL_ID"
else
  ok "Tunnel already exists: $TUNNEL_ID"
fi

# ── Step 4: Write config file ─────────────────────────────────────────────────
CRED_FILE=$(ls "$CONFIG_DIR/${TUNNEL_ID}.json" 2>/dev/null || echo "")
if [[ -z "$CRED_FILE" ]]; then
  CRED_FILE="$CONFIG_DIR/${TUNNEL_ID}.json"
fi

mkdir -p "$CONFIG_DIR"
cat > "$CONFIG_DIR/config.yml" <<EOF
tunnel: ${TUNNEL_ID}
credentials-file: ${CONFIG_DIR}/${TUNNEL_ID}.json

ingress:
  # Main web app
  - hostname: ${DOMAIN}
    service: http://localhost:3000

  # API Gateway — all /api/* calls from the web app
  - hostname: api.${DOMAIN}
    service: http://localhost:8080
    originRequest:
      noTLSVerify: true
      connectTimeout: 10s

  # Chat WebSocket gateway
  - hostname: ws.${DOMAIN}
    service: http://localhost:8093
    originRequest:
      noTLSVerify: true

  # Media service (uploads / streaming)
  - hostname: media.${DOMAIN}
    service: http://localhost:8087
    originRequest:
      noTLSVerify: true

  # Catch-all — required by cloudflared
  - service: http_status:404
EOF

ok "Config written to $CONFIG_DIR/config.yml"

# ── Step 5: Create DNS records ────────────────────────────────────────────────
log "Creating DNS CNAME records..."
for sub in "" "api" "ws" "media"; do
  host="${sub:+$sub.}${DOMAIN}"
  cloudflared tunnel route dns "$TUNNEL_NAME" "$host" 2>/dev/null \
    && ok "DNS: $host → $TUNNEL_NAME" \
    || warn "DNS $host already exists or failed — check Cloudflare dashboard"
done

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}══════════════════════════════════════════════════════${RESET}"
echo -e "${GREEN}  Cloudflare Tunnel ready!${RESET}"
echo -e "${GREEN}══════════════════════════════════════════════════════${RESET}"
echo ""
echo "  Run the tunnel:     cloudflared tunnel run $TUNNEL_NAME"
echo "  Run as service:     sudo cloudflared service install"
echo "                      sudo systemctl start cloudflared"
echo ""
echo "  Routes:"
echo "    https://${DOMAIN}          → Web UI      (localhost:3000)"
echo "    https://api.${DOMAIN}      → API Gateway (localhost:8080)"
echo "    https://ws.${DOMAIN}       → Chat WS     (localhost:8093)"
echo "    https://media.${DOMAIN}    → Media       (localhost:8087)"
echo ""
echo "  Make sure the Docker stack is running first:"
echo "    see docs/HOW_TO_RUN.md"
