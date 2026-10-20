#!/usr/bin/env bash
# Builds the admin console image on the SERVER from atpost-web-ui (no Docker
# on the laptop): a git bundle of the chosen branch goes through the transfer
# bucket, the server builds ZONE=admin and tags it with the image name
# docker-compose.partner-rollout.yml expects, then ./up.sh --no-build starts
# admin-web on admin.cleestudio.com.
#
#   AWS_PROFILE=atpost-qa ./build-admin.sh [branch]   (default feat/admin-sound-library)
source "$(dirname "$0")/lib.sh"
check_account
load_stack
make_tmp

ADMIN_REPO="${DEVBOX_ADMIN_REPO:-/c/workspace/atpost-web-ui}"
REF="${1:-feat/admin-sound-library}"
TAG="$(awk '/^  admin-web:/{f=1;next} f&&/^  [a-z]/{f=0} f&&/image:/{print $2; exit}' "$DOCKER_DIR/docker-compose.partner-rollout.yml" | tr -d '\r')"
[ -n "$TAG" ] || die "could not read admin-web's image from docker-compose.partner-rollout.yml"
[ -d "$ADMIN_REPO" ] || die "admin repo not found at $ADMIN_REPO (set DEVBOX_ADMIN_REPO)"
lgit() { local dir="$1"; shift; git -C "$(winpath "$dir")" "$@"; }
lgit "$ADMIN_REPO" rev-parse --verify -q "$REF" >/dev/null || die "branch $REF not found in $ADMIN_REPO"

log "admin source: $REF @ $(lgit "$ADMIN_REPO" log -1 --format='%h %ad %s' --date=short "$REF") -> image $TAG"
KEY="$(handoff_key admin-web .bundle)"
lgit "$ADMIN_REPO" bundle create -q "$(winpath "$DEVBOX_TMP/admin.bundle")" "$REF"
aws s3 cp "$(winpath "$DEVBOX_TMP/admin.bundle")" "s3://$TRANSFER_BUCKET/$KEY" --only-show-errors
wait_ssm_online
ssm_run 2400 build-admin "$TRANSFER_BUCKET" "$KEY" "$REF" "$TAG"
log "built. ./up.sh --no-build starts admin-web"
