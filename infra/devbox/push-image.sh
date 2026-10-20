#!/usr/bin/env bash
# Copies the locally built admin console image (admin-web has no build
# section; docker-compose.partner-rollout.yml names an image built on the
# laptop from C:\workspace\atpost-web-ui) to the server. Until this runs,
# up.sh starts everything except admin-web.
#
# Needs Docker running on the laptop, once.
#
#   AWS_PROFILE=atpost-qa ./push-image.sh [image]
source "$(dirname "$0")/lib.sh"
check_account
load_stack
make_tmp

IMAGE="${1:-$(awk '/^  admin-web:/{f=1;next} f&&/^  [a-z]/{f=0} f&&/image:/{print $2; exit}' "$DOCKER_DIR/docker-compose.partner-rollout.yml" | tr -d '\r')}"
[ -n "$IMAGE" ] || die "could not read admin-web's image from docker-compose.partner-rollout.yml; pass it as an argument"
docker image inspect "$IMAGE" >/dev/null 2>&1 || die "image $IMAGE not found locally (is Docker running?)"

KEY="$(handoff_key admin-image .tar.gz)"
trap 'aws s3 rm "s3://$TRANSFER_BUCKET/$KEY" --only-show-errors >/dev/null 2>&1 || true; rm -rf "$DEVBOX_TMP"' EXIT
log "saving $IMAGE ..."
docker save "$IMAGE" | gzip > "$DEVBOX_TMP/image.tar.gz"
log "uploading $(du -h "$DEVBOX_TMP/image.tar.gz" | cut -f1) ..."
aws s3 cp "$(winpath "$DEVBOX_TMP/image.tar.gz")" "s3://$TRANSFER_BUCKET/$KEY" --only-show-errors
wait_ssm_online
ssm_run 900 load-image "$TRANSFER_BUCKET" "$KEY"
log "loaded. ./up.sh --no-build starts admin-web"
