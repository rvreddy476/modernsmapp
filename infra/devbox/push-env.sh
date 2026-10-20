#!/usr/bin/env bash
# Hands the server its configuration, through the transfer bucket, and
# deletes the object as soon as the server has it:
#
#   * Architecture/docker/.env (the laptop's, unchanged), plus the four
#     DEVBOX_*_BUCKET names from the stack outputs, appended on the server;
#   * the .secrets/ files the compose files bind-mount (the FCM key for
#     notification-service, the Google route key for food-service);
#   * /srv/atpost/web-from-parity.yml: the web image builds from
#     /srv/atpost/web (the server copy of C:\workspace\wt\pb-integ).
#
# Prints counts and names only. Re-run after any .env change, then ./up.sh.
#
#   AWS_PROFILE=atpost-qa ./push-env.sh
source "$(dirname "$0")/lib.sh"
check_account
load_stack
make_tmp

ENV_FILE="$DOCKER_DIR/.env"
[ -f "$ENV_FILE" ] || die "$ENV_FILE not found"

STAGE="$DEVBOX_TMP/stage"
mkdir -p "$STAGE/modernsmapp/Architecture/docker" "$STAGE/modernsmapp/.secrets"
cp "$ENV_FILE" "$STAGE/modernsmapp/Architecture/docker/.env"

# Every ../../.secrets/<file> the stack's compose files mount.
secrets=()
while IFS= read -r f; do
  [ -n "$f" ] && secrets+=("$f")
done < <(cd "$DOCKER_DIR" && grep -ohE '\.\./\.\./\.secrets/[A-Za-z0-9._-]+' \
  docker-compose.yml docker-compose.admin-local.yml docker-compose.google-routing.local.yml \
  docker-compose.partner-local.yml docker-compose.partner-rollout.yml docker-compose.devbox.yml \
  | sed 's#^\.\./\.\./\.secrets/##' | sort -u)
copied=0
for f in "${secrets[@]}"; do
  if [ -f "$REPO_ROOT/.secrets/$f" ]; then
    cp "$REPO_ROOT/.secrets/$f" "$STAGE/modernsmapp/.secrets/$f"
    copied=$((copied + 1))
  else
    warn ".secrets/$f is mounted by compose but missing on this laptop; that service will fail"
  fi
done

cat > "$STAGE/web-from-parity.yml" <<'EOF'
# devbox: the web image builds from the server's copy of the web repo
# (C:\workspace\wt\pb-integ on the laptop, branch feat/tiktok-parity-web),
# put there by infra/devbox/push-code.sh.
services:
  web:
    build:
      context: /srv/atpost/web
EOF

tar -czf "$DEVBOX_TMP/env.tgz" -C "$STAGE" .
KEY="$(handoff_key env .tgz)"
# Whatever happens next, the object does not outlive this script.
trap 'aws s3 rm "s3://$TRANSFER_BUCKET/$KEY" --only-show-errors >/dev/null 2>&1 || true; rm -rf "$DEVBOX_TMP"' EXIT

log "handing over .env ($(grep -cE '^[A-Za-z_][A-Za-z0-9_]*=' "$ENV_FILE") variables) and $copied secret file(s)"
aws s3 cp "$(winpath "$DEVBOX_TMP/env.tgz")" "s3://$TRANSFER_BUCKET/$KEY" --only-show-errors --sse AES256
wait_ssm_online
ssm_run 300 install-env "$TRANSFER_BUCKET" "$KEY" "$MEDIA_BUCKET" "$LIVE_BUCKET" "$COMMERCE_BUCKET" "$FOOD_BUCKET"
log "done. Next: ./push-tunnel.sh (first time), then ./up.sh"
