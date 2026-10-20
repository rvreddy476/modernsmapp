#!/usr/bin/env bash
# Server side of infra/devbox. Installed as /usr/local/sbin/devbox-remote by
# every laptop script (lib.sh ssm_run), run as root through SSM Run Command.
#
# Prints names, counts, commit ids and states only, never a value from .env,
# a key or tunnel credentials: SSM keeps command output for 7 days in the
# transfer bucket.

set -euo pipefail
umask 077

ROOT=/srv/atpost
APP="$ROOT/modernsmapp"
WEB="$ROOT/web"
DOCKER_DIR="$APP/Architecture/docker"
LOGS="$ROOT/logs"
WEB_OVERRIDE="$ROOT/web-from-parity.yml"
export HOME="${HOME:-/root}"
export AWS_REGION="${AWS_REGION:-ap-south-1}"
export AWS_DEFAULT_REGION="$AWS_REGION"

# The laptop's file list, then the web build override, then the devbox one.
COMPOSE_FILES=(
  -f docker-compose.yml
  -f docker-compose.admin-local.yml
  -f docker-compose.google-routing.local.yml
  -f docker-compose.partner-local.yml
  -f docker-compose.partner-rollout.yml
  -f "$WEB_OVERRIDE"
  -f docker-compose.devbox.yml
)

say() { printf '[server] %s\n' "$*"; }
die() { printf '[server] ERROR: %s\n' "$*" >&2; exit 1; }

compose() { (cd "$DOCKER_DIR" && docker compose "${COMPOSE_FILES[@]}" "$@"); }

# Fetch an S3 object to a local file and delete the object at once.
fetch_and_delete() {
  aws s3 cp "s3://$1/$2" "$3" --only-show-errors
  aws s3 rm "s3://$1/$2" --only-show-errors
}

cmd_wait_ready() {
  [ -f /var/lib/atpost-devbox/ready ] || { say "first-boot setup still running (see /var/log/atpost-devbox-init.log)"; exit 3; }
  say "ready: $(docker --version)"
  say "       $(docker compose version)"
  say "       $(aws --version 2>&1)"
  say "       $(cloudflared --version 2>&1 | head -1)"
  say "       $(nproc) vCPU, $(free -g | awk '/^Mem:/{print $2}') GiB RAM, swap $(free -g | awk '/^Swap:/{print $2}') GiB, $(df -h / | awk 'NR==2{print $4}') free on /"
}

# pull-code <bucket> <app-bundle-key> <web-bundle-key>
checkout_bundle() {
  local bucket="$1" key="$2" dir="$3" bundle
  bundle="$(mktemp --suffix=.bundle)"
  aws s3 cp "s3://$bucket/$key" "$bundle" --only-show-errors
  # verify needs a repository to run in, so the target is initialised first.
  [ -d "$dir/.git" ] || git init -q "$dir"
  git config --global --add safe.directory "$dir" 2>/dev/null || true
  git -C "$dir" bundle verify -q "$bundle" >/dev/null
  git -C "$dir" fetch -q "$bundle" HEAD
  # Untracked files (.env, .secrets) are gitignored and survive this.
  git -C "$dir" checkout -q --force --detach FETCH_HEAD
  rm -f "$bundle"
  aws s3 rm "s3://$bucket/$key" --only-show-errors
  say "$dir at $(git -C "$dir" log -1 --format='%h %cd %s' --date=short)"
}

cmd_pull_code() {
  mkdir -p "$ROOT" "$LOGS"
  umask 022
  checkout_bundle "$1" "$2" "$APP"
  checkout_bundle "$1" "$3" "$WEB"
}

# install-env <bucket> <key> <media> <live-recordings> <commerce-invoices> <food-files>
cmd_install_env() {
  local bucket="$1" key="$2" tgz
  [ -d "$DOCKER_DIR" ] || die "$DOCKER_DIR is missing; run push-code.sh first"
  tgz="$(mktemp --suffix=.tgz)"
  fetch_and_delete "$bucket" "$key" "$tgz"
  tar -xzf "$tgz" -C "$ROOT" --no-same-owner
  rm -f "$tgz"
  local env="$DOCKER_DIR/.env"
  [ -f "$env" ] || die ".env not in the hand-off"
  sed -i 's/\r$//' "$env"
  # The bucket block is the server's own; the rest of .env is the laptop's.
  sed -i -E '/^(# devbox buckets|DEVBOX_(MEDIA|LIVE_RECORDINGS|COMMERCE_INVOICES|FOOD_FILES)_BUCKET=)/d' "$env"
  [ -z "$(tail -c1 "$env")" ] || echo >> "$env"
  {
    echo "# devbox buckets (infra/devbox/push-env.sh, from the CloudFormation outputs)"
    echo "DEVBOX_MEDIA_BUCKET=$3"
    echo "DEVBOX_LIVE_RECORDINGS_BUCKET=$4"
    echo "DEVBOX_COMMERCE_INVOICES_BUCKET=$5"
    echo "DEVBOX_FOOD_FILES_BUCKET=$6"
  } >> "$env"
  chmod 600 "$env"
  # Bind-mounted into containers that do not run as root (FCM key, Google
  # route key), so they must be world-readable on this single-user host.
  chmod 755 "$APP/.secrets" 2>/dev/null || true
  chmod 644 "$APP"/.secrets/* 2>/dev/null || true
  chmod 644 "$WEB_OVERRIDE"
  say ".env installed: $(grep -cE '^[A-Za-z_][A-Za-z0-9_]*=' "$env") variables"
  say "DEVBOX_* names set: $(grep -oE '^DEVBOX_[A-Z_]+' "$env" | tr '\n' ' ')"
  say "secret files: $(ls "$APP/.secrets" 2>/dev/null | wc -l) in $APP/.secrets"
  say "web override: $WEB_OVERRIDE -> build context $(sed -n 's/^ *context: *//p' "$WEB_OVERRIDE")"
  say "hand-off object deleted from s3://$bucket"
}

# install-tunnel <bucket> <key>
cmd_install_tunnel() {
  local tgz
  tgz="$(mktemp --suffix=.tgz)"
  fetch_and_delete "$1" "$2" "$tgz"
  install -d -m 700 /etc/cloudflared
  tar -xzf "$tgz" -C /etc/cloudflared --no-same-owner
  rm -f "$tgz"
  chmod 600 /etc/cloudflared/*
  [ -f /etc/cloudflared/config.yml ] || die "config.yml not in the hand-off"
  if systemctl cat cloudflared.service >/dev/null 2>&1; then
    systemctl restart cloudflared
  else
    cloudflared service install
  fi
  systemctl enable cloudflared >/dev/null 2>&1 || true
  sleep 5
  say "cloudflared: $(systemctl is-active cloudflared)"
  say "ingress rules: $(grep -c 'service:' /etc/cloudflared/config.yml)"
  journalctl -u cloudflared --since '-2 min' --no-pager -o cat 2>/dev/null | grep -E 'Registered tunnel connection|ERR' | sed -E 's/connIndex=[0-9]+ //' | tail -5 || true
}

# Services to run: everything compose knows, minus admin-web when its
# locally built image is not on this server yet (push-image.sh).
services_to_run() {
  local all image
  all="$(compose config --services)"
  # jq reads the resolved config straight from the pipe; nothing is printed.
  image="$(compose config --format json 2>/dev/null | jq -r '.services["admin-web"].image // empty' || true)"
  if [ -n "$image" ] && ! docker image inspect "$image" >/dev/null 2>&1; then
    echo "[server] admin-web skipped: image $image is not on this server (run push-image.sh)" >&2
    all="$(printf '%s\n' "$all" | grep -vx admin-web)"
  fi
  printf '%s\n' "$all" | tr '\n' ' '
}

install_boot_unit() {
  cat > /etc/systemd/system/atpost-stack.service <<'EOF'
[Unit]
Description=atpost dev stack (docker compose up)
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/local/sbin/devbox-remote boot
TimeoutStartSec=1800

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
  systemctl enable atpost-stack.service >/dev/null 2>&1
}

check_prereqs() {
  [ -f "$DOCKER_DIR/docker-compose.devbox.yml" ] || die "docker-compose.devbox.yml missing; is it committed, and did push-code.sh run?"
  [ -f "$DOCKER_DIR/.env" ] || die ".env missing; run push-env.sh"
  [ -f "$WEB_OVERRIDE" ] || die "$WEB_OVERRIDE missing; run push-env.sh"
  [ -d "$WEB/.git" ] || die "$WEB missing; run push-code.sh"
  compose config --quiet || die "compose configuration does not resolve (see above)"
}

# up [--no-build]: start in the background, return at once.
cmd_up() {
  check_prereqs
  install_boot_unit
  mkdir -p "$LOGS"
  local build="--build" log svcs
  [ "${1:-}" = "--no-build" ] && build=""
  log="$LOGS/up-$(date +%Y%m%dT%H%M%S).log"
  svcs="$(services_to_run)"
  ln -sfn "$log" "$LOGS/up-latest.log"
  # setsid + closed stdio: SSM returns now, compose keeps going.
  setsid bash -c "cd '$DOCKER_DIR' && { docker compose ${COMPOSE_FILES[*]} up -d $build $svcs; echo \"[devbox] compose exit=\$?\"; } > '$log' 2>&1" < /dev/null > /dev/null 2>&1 &
  say "started: docker compose up -d $build ($(echo "$svcs" | wc -w) services)"
  say "log: $log (./up.sh status shows its tail)"
}

# boot: the systemd unit after an instance start. No build.
cmd_boot() {
  check_prereqs
  mkdir -p "$LOGS"
  local svcs
  svcs="$(services_to_run)"
  # shellcheck disable=SC2086
  compose up -d $svcs > "$LOGS/boot-$(date +%Y%m%dT%H%M%S).log" 2>&1
}

cmd_status() {
  local latest="$LOGS/up-latest.log"
  if [ -e "$latest" ]; then
    say "last up log: $(readlink -f "$latest")"
    tail -n 25 "$latest" | sed 's/^/  | /'
  fi
  say "containers:"
  compose ps -a --format 'table {{.Service}}\t{{.State}}\t{{.Status}}' 2>/dev/null | sed 's/^/  /'
  local bad
  bad="$(compose ps -a --format '{{.Service}} {{.State}} {{.ExitCode}}' 2>/dev/null \
    | awk '($2=="exited" && $3!="0") || $2=="restarting" || $2=="dead" {print $1}' | tr '\n' ' ')"
  [ -n "$bad" ] && say "NOT HEALTHY: $bad (./up.sh logs <service>)"
  say "cloudflared: $(systemctl is-active cloudflared 2>/dev/null || echo not-installed)"
  say "boot unit: $(systemctl is-enabled atpost-stack.service 2>/dev/null || echo not-installed)"
  say "memory: $(free -h | awk '/^Mem:/{print $3" used of "$2}'), disk /: $(df -h / | awk 'NR==2{print $3" used, "$4" free"}')"
}

cmd_logs() {
  local svc="${1:?service name}" n="${2:-120}"
  compose logs --no-color --tail "$n" "$svc"
}

# load-image <bucket> <key>
cmd_load_image() {
  local f
  f="$(mktemp --suffix=.tar.gz)"
  fetch_and_delete "$1" "$2" "$f"
  gunzip -c "$f" | docker load
  rm -f "$f"
}

sub="${1:-}"; shift || true
case "$sub" in
  wait-ready)      cmd_wait_ready "$@" ;;
  pull-code)       cmd_pull_code "$@" ;;
  install-env)     cmd_install_env "$@" ;;
  install-tunnel)  cmd_install_tunnel "$@" ;;
  up)              cmd_up "$@" ;;
  boot)            cmd_boot "$@" ;;
  status)          cmd_status "$@" ;;
  logs)            cmd_logs "$@" ;;
  load-image)      cmd_load_image "$@" ;;
  *) die "unknown subcommand '$sub'" ;;
esac
