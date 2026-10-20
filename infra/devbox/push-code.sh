#!/usr/bin/env bash
# Copies the COMMITTED code to the server: a git bundle of modernsmapp at
# HEAD and one of the web repo (C:\workspace\wt\pb-integ) at HEAD go through
# the transfer bucket; the server checks each out (detached) into
# /srv/atpost/modernsmapp and /srv/atpost/web. No GitHub credentials ever
# reach the server. Uncommitted changes are NOT copied. Re-run any time;
# then ./up.sh to rebuild.
#
#   AWS_PROFILE=atpost-qa ./push-code.sh
source "$(dirname "$0")/lib.sh"
check_account
load_stack
make_tmp
# Git for Windows may not read Git Bash's /c/... paths: hand it C:/... ones.
lgit() { local dir="$1"; shift; git -C "$(winpath "$dir")" "$@"; }

[ -d "$WEB_REPO" ] || die "web repo not found at $WEB_REPO (set DEVBOX_WEB_REPO)"
lgit "$REPO_ROOT" cat-file -e HEAD:Architecture/docker/docker-compose.devbox.yml 2>/dev/null \
  || die "Architecture/docker/docker-compose.devbox.yml is not committed at HEAD; commit it first"

for repo in "$REPO_ROOT" "$WEB_REPO"; do
  dirty="$(lgit "$repo" status --porcelain --untracked-files=no | wc -l | tr -d ' ')"
  [ "$dirty" = "0" ] || warn "$repo has $dirty modified tracked files; only the commit at HEAD is copied"
  log "$repo: $(lgit "$repo" rev-parse --abbrev-ref HEAD) @ $(lgit "$repo" log -1 --format='%h %s')"
done

APP_KEY="$(handoff_key modernsmapp .bundle)"
WEB_KEY="$(handoff_key web .bundle)"
lgit "$REPO_ROOT" bundle create -q "$(winpath "$DEVBOX_TMP/modernsmapp.bundle")" HEAD
lgit "$WEB_REPO" bundle create -q "$(winpath "$DEVBOX_TMP/web.bundle")" HEAD
log "bundles: modernsmapp $(du -h "$DEVBOX_TMP/modernsmapp.bundle" | cut -f1), web $(du -h "$DEVBOX_TMP/web.bundle" | cut -f1); uploading ..."
aws s3 cp "$(winpath "$DEVBOX_TMP/modernsmapp.bundle")" "s3://$TRANSFER_BUCKET/$APP_KEY" --only-show-errors
aws s3 cp "$(winpath "$DEVBOX_TMP/web.bundle")" "s3://$TRANSFER_BUCKET/$WEB_KEY" --only-show-errors

wait_ssm_online
ssm_run 900 pull-code "$TRANSFER_BUCKET" "$APP_KEY" "$WEB_KEY"
log "code on the server. Next: ./push-env.sh (first time), then ./up.sh"
