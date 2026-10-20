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

[ -d "$WEB_REPO" ] || die "web repo not found at $WEB_REPO (set DEVBOX_WEB_REPO)"
git -C "$REPO_ROOT" cat-file -e HEAD:Architecture/docker/docker-compose.devbox.yml 2>/dev/null \
  || die "Architecture/docker/docker-compose.devbox.yml is not committed at HEAD; commit it first"

for repo in "$REPO_ROOT" "$WEB_REPO"; do
  dirty="$(git -C "$repo" status --porcelain --untracked-files=no | wc -l | tr -d ' ')"
  [ "$dirty" = "0" ] || warn "$repo has $dirty modified tracked files; only the commit at HEAD is copied"
  log "$repo: $(git -C "$repo" rev-parse --abbrev-ref HEAD) @ $(git -C "$repo" log -1 --format='%h %s')"
done

APP_KEY="$(handoff_key modernsmapp .bundle)"
WEB_KEY="$(handoff_key web .bundle)"
git -C "$REPO_ROOT" bundle create -q "$DEVBOX_TMP/modernsmapp.bundle" HEAD
git -C "$WEB_REPO" bundle create -q "$DEVBOX_TMP/web.bundle" HEAD
log "bundles: modernsmapp $(du -h "$DEVBOX_TMP/modernsmapp.bundle" | cut -f1), web $(du -h "$DEVBOX_TMP/web.bundle" | cut -f1); uploading ..."
aws s3 cp "$(winpath "$DEVBOX_TMP/modernsmapp.bundle")" "s3://$TRANSFER_BUCKET/$APP_KEY" --only-show-errors
aws s3 cp "$(winpath "$DEVBOX_TMP/web.bundle")" "s3://$TRANSFER_BUCKET/$WEB_KEY" --only-show-errors

wait_ssm_online
ssm_run 900 pull-code "$TRANSFER_BUCKET" "$APP_KEY" "$WEB_KEY"
log "code on the server. Next: ./push-env.sh (first time), then ./up.sh"
