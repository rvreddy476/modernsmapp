#!/usr/bin/env bash
# OPTIONAL — only for live recordings while .env points live at LiveKit
# Cloud. Cloud egress uploads from outside AWS, so it cannot use the
# server's role. This creates an access key for the stack's live-egress IAM
# user (it may only PutObject into the recordings bucket) and appends it to
# the LAPTOP's Architecture/docker/.env as
# DEVBOX_LIVE_EGRESS_ACCESS_KEY_ID / DEVBOX_LIVE_EGRESS_SECRET_ACCESS_KEY,
# which docker-compose.devbox.yml hands to live-service-v2. The key is
# never printed. Then: ./push-env.sh && ./up.sh --no-build.
#
# Idempotent: does nothing when .env already carries a key of that user.
# Rotate: delete the two lines from .env, then
#   aws iam delete-access-key --user-name <user> --access-key-id <id>
# and run this again.
source "$(dirname "$0")/lib.sh"
check_account

ENV_FILE="$DOCKER_DIR/.env"
[ -f "$ENV_FILE" ] || die "$ENV_FILE not found"
USER_NAME="$(stack_output LiveEgressUserName)"
[ -n "$USER_NAME" ] && [ "$USER_NAME" != "None" ] || die "the stack has no live-egress user (CreateLiveEgressUser=false?)"

existing="$(sed -n -E 's/^DEVBOX_LIVE_EGRESS_ACCESS_KEY_ID=(.+)$/\1/p' "$ENV_FILE" | tr -d '\r"' | head -1)"
if [ -n "$existing" ]; then
  if aws iam list-access-keys --user-name "$USER_NAME" --query 'AccessKeyMetadata[].AccessKeyId' --output text | tr '\t' '\n' | grep -qx "$existing"; then
    log "$ENV_FILE already holds an active key of $USER_NAME; nothing to do"
    exit 0
  fi
  die "$ENV_FILE has a DEVBOX_LIVE_EGRESS_ACCESS_KEY_ID that is not a key of $USER_NAME; remove both DEVBOX_LIVE_EGRESS_* lines and re-run"
fi

read -r KEY_ID KEY_SECRET < <(aws iam create-access-key --user-name "$USER_NAME" \
  --query 'AccessKey.[AccessKeyId,SecretAccessKey]' --output text)
[ -n "$KEY_ID" ] && [ -n "$KEY_SECRET" ] || die "create-access-key returned nothing"
[ -z "$(tail -c1 "$ENV_FILE")" ] || echo >> "$ENV_FILE"
{
  echo "# devbox: LiveKit Cloud egress -> S3 recordings (infra/devbox/make-egress-key.sh)"
  echo "DEVBOX_LIVE_EGRESS_ACCESS_KEY_ID=$KEY_ID"
  echo "DEVBOX_LIVE_EGRESS_SECRET_ACCESS_KEY=$KEY_SECRET"
} >> "$ENV_FILE"
unset KEY_SECRET
log "key for $USER_NAME written to $ENV_FILE (not shown). Next: ./push-env.sh && ./up.sh --no-build"
