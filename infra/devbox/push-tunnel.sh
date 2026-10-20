#!/usr/bin/env bash
# Moves the Cloudflare tunnel from the laptop to the server: the tunnel's
# credentials JSON and the laptop's ~/.cloudflared/config.yml (credentials
# path rewritten to /etc/cloudflared) go through the transfer bucket to
# /etc/cloudflared, and cloudflared runs there as a systemd service. Every
# ingress rule targets localhost:<port>, the same ports compose publishes on
# the server, so cleestudio.com, api-dev.cleestudio.com, admin. and partner.
# keep working unchanged. cert.pem (account login) is NOT copied.
#
# STOP THE LAPTOP TUNNEL FIRST. Two connectors on one tunnel split traffic
# between the laptop and the server. This script refuses while a local
# cloudflared is running (DEVBOX_FORCE_TUNNEL=1 overrides).
#
#   AWS_PROFILE=atpost-qa ./push-tunnel.sh
source "$(dirname "$0")/lib.sh"
check_account
load_stack
make_tmp

CF_DIR="${DEVBOX_CLOUDFLARED_DIR:-$HOME/.cloudflared}"
CONFIG="$CF_DIR/config.yml"
[ -f "$CONFIG" ] || die "$CONFIG not found"

if [ "${DEVBOX_FORCE_TUNNEL:-}" != "1" ]; then
  if tasklist /FI "IMAGENAME eq cloudflared.exe" 2>/dev/null | grep -qi cloudflared; then
    die "cloudflared is running on this laptop. Stop it first: Ctrl+C in its terminal, or (admin PowerShell) 'Stop-Service cloudflared; Set-Service cloudflared -StartupType Disabled' if it runs as a service"
  fi
fi

TUNNEL_ID="$(sed -n -E 's/^tunnel:[[:space:]]*([^[:space:]]+).*/\1/p' "$CONFIG" | tr -d '\r' | head -1)"
CRED_NAME="$(sed -n -E 's/^credentials-file:[[:space:]]*(.+)$/\1/p' "$CONFIG" | tr -d '\r' | head -1 | sed -E 's#.*[\\/]##')"
[ -n "$TUNNEL_ID" ] || die "no 'tunnel:' line in $CONFIG"
[ -n "$CRED_NAME" ] && [ -f "$CF_DIR/$CRED_NAME" ] || die "credentials file named in $CONFIG not found in $CF_DIR"

STAGE="$DEVBOX_TMP/stage"
mkdir -p "$STAGE"
cp "$CF_DIR/$CRED_NAME" "$STAGE/$CRED_NAME"
tr -d '\r' < "$CONFIG" \
  | sed -E "s#^credentials-file:.*#credentials-file: /etc/cloudflared/$CRED_NAME#" > "$STAGE/config.yml"
tar -czf "$DEVBOX_TMP/tunnel.tgz" -C "$STAGE" .

KEY="$(handoff_key tunnel .tgz)"
trap 'aws s3 rm "s3://$TRANSFER_BUCKET/$KEY" --only-show-errors >/dev/null 2>&1 || true; rm -rf "$DEVBOX_TMP"' EXIT
log "handing over the tunnel config ($(grep -c 'hostname:' "$STAGE/config.yml") hostname rules) and its credentials"
aws s3 cp "$(winpath "$DEVBOX_TMP/tunnel.tgz")" "s3://$TRANSFER_BUCKET/$KEY" --only-show-errors --sse AES256
wait_ssm_online
ssm_run 180 install-tunnel "$TRANSFER_BUCKET" "$KEY"
log "tunnel runs on the server now. Keep the laptop's cloudflared stopped."
