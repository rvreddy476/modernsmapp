#!/usr/bin/env bash
# Shared by every infra/devbox/*.sh script. Sourced, never run.
#
# Runs from Git Bash on the founder's laptop with the Windows AWS CLI v2.
# Nothing here prints a secret: command output from the server is the
# remote script's own output (infra/devbox/remote/devbox-remote.sh), which
# prints names, counts and states only.

set -euo pipefail

# Git Bash rewrites arguments that look like POSIX paths (/srv/atpost ->
# C:/Program Files/Git/srv/atpost) before handing them to a Windows program
# such as aws.exe. Off for everything; local paths go through winpath.
export MSYS_NO_PATHCONV=1
export MSYS2_ARG_CONV_EXCL='*'

export AWS_PROFILE="${AWS_PROFILE:-atpost-qa}"
export AWS_REGION="${DEVBOX_REGION:-ap-south-1}"
export AWS_DEFAULT_REGION="$AWS_REGION"
export AWS_PAGER=""

# The Windows AWS CLI ends every line with CR LF; a CR left on a captured
# value (an instance id, a bucket name) breaks the next command. Interactive
# sessions use `exec aws`, which bypasses this.
aws() { command aws "$@" | tr -d '\r'; }

DEVBOX_ACCOUNT="551626543760"
DEVBOX_STACK="${DEVBOX_STACK:-atpost-devbox}"
DEVBOX_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$DEVBOX_DIR/../.." && pwd)"
DOCKER_DIR="$REPO_ROOT/Architecture/docker"
WEB_REPO="${DEVBOX_WEB_REPO:-/c/workspace/wt/pb-integ}"

log()  { printf '\033[1;34m[devbox]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[devbox] WARNING:\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[devbox] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

# A local path in the form aws.exe understands (C:/...).
winpath() { cygpath -m "$1" 2>/dev/null || printf '%s' "$1"; }

# A private scratch directory, removed on exit.
make_tmp() {
  DEVBOX_TMP="$(mktemp -d)"
  chmod 700 "$DEVBOX_TMP"
  trap 'rm -rf "$DEVBOX_TMP"' EXIT
}

# Refuse to touch any account but the devbox one.
check_account() {
  command -v aws >/dev/null 2>&1 || die "AWS CLI v2 not found on PATH"
  local acct
  acct="$(aws sts get-caller-identity --query Account --output text 2>/dev/null)" \
    || die "AWS credentials for profile '$AWS_PROFILE' are missing or expired. Run: aws sso login --profile $AWS_PROFILE (or aws login)"
  [ "$acct" = "$DEVBOX_ACCOUNT" ] || die "profile '$AWS_PROFILE' is account $acct, not $DEVBOX_ACCOUNT"
}

stack_output() {
  aws cloudformation describe-stacks --stack-name "$DEVBOX_STACK" \
    --query "Stacks[0].Outputs[?OutputKey=='$1'].OutputValue | [0]" --output text 2>/dev/null
}

# Fills INSTANCE_ID, TRANSFER_BUCKET and the four data bucket names.
load_stack() {
  aws cloudformation describe-stacks --stack-name "$DEVBOX_STACK" >/dev/null 2>&1 \
    || die "stack $DEVBOX_STACK not found in $AWS_REGION. Run ./deploy-stack.sh first"
  INSTANCE_ID="$(stack_output InstanceId)"
  TRANSFER_BUCKET="$(stack_output TransferBucketName)"
  MEDIA_BUCKET="$(stack_output MediaBucketName)"
  LIVE_BUCKET="$(stack_output LiveRecordingsBucketName)"
  COMMERCE_BUCKET="$(stack_output CommerceInvoicesBucketName)"
  FOOD_BUCKET="$(stack_output FoodFilesBucketName)"
  [ -n "$INSTANCE_ID" ] && [ "$INSTANCE_ID" != "None" ] || die "stack has no InstanceId output"
}

instance_state() {
  aws ec2 describe-instances --instance-ids "$INSTANCE_ID" \
    --query 'Reservations[0].Instances[0].State.Name' --output text
}

# Waits until the SSM agent on the instance answers (first boot: a few minutes).
wait_ssm_online() {
  local i status
  for i in $(seq 1 60); do
    status="$(aws ssm describe-instance-information \
      --filters "Key=InstanceIds,Values=$INSTANCE_ID" \
      --query 'InstanceInformationList[0].PingStatus' --output text 2>/dev/null || true)"
    [ "$status" = "Online" ] && return 0
    [ "$i" = 1 ] && log "waiting for the SSM agent on $INSTANCE_ID ..."
    sleep 10
  done
  die "SSM agent not online after 10 minutes (instance state: $(instance_state))"
}

# Uploads the server-side helper. Every ssm_run fetches it fresh, so the
# server always runs the version in this checkout.
upload_remote_script() {
  aws s3 cp "$(winpath "$DEVBOX_DIR/remote/devbox-remote.sh")" \
    "s3://$TRANSFER_BUCKET/devbox/remote/devbox-remote.sh" --only-show-errors
}

# ssm_run <timeout-seconds> <remote-subcommand> [args...]
#
# Runs /usr/local/sbin/devbox-remote <subcommand> <args> as root on the
# server through SSM Run Command, waits, prints its output and returns its
# status. Full output also lands in s3://<transfer>/ssm-output/ (7 days).
# Arguments must be plain tokens (bucket names, object keys, service names).
ssm_run() {
  local timeout="$1"; shift
  local a
  for a in "$@"; do
    [[ "$a" =~ ^[A-Za-z0-9._/:=@,+-]*$ ]] || die "unsafe argument for the remote command: $a"
  done
  upload_remote_script
  local cmd="set -e; aws s3 cp s3://$TRANSFER_BUCKET/devbox/remote/devbox-remote.sh /usr/local/sbin/devbox-remote --only-show-errors; chmod 700 /usr/local/sbin/devbox-remote; /usr/local/sbin/devbox-remote $*"
  local params
  params="$(mktemp)"
  printf '{"commands":["%s"],"executionTimeout":["%s"]}' "$cmd" "$timeout" > "$params"
  local id
  id="$(aws ssm send-command \
    --instance-ids "$INSTANCE_ID" \
    --document-name AWS-RunShellScript \
    --comment "devbox $1" \
    --parameters "file://$(winpath "$params")" \
    --output-s3-bucket-name "$TRANSFER_BUCKET" \
    --output-s3-key-prefix ssm-output \
    --query Command.CommandId --output text)"
  rm -f "$params"
  local status="Pending" waited=0
  while :; do
    sleep 3; waited=$((waited + 3))
    status="$(aws ssm get-command-invocation --command-id "$id" --instance-id "$INSTANCE_ID" \
      --query Status --output text 2>/dev/null || echo Pending)"
    case "$status" in
      Success|Failed|Cancelled|TimedOut|Cancelling) break ;;
    esac
    if [ $((waited % 60)) = 0 ]; then log "... $1 still running (${waited}s)"; fi
  done
  aws ssm get-command-invocation --command-id "$id" --instance-id "$INSTANCE_ID" \
    --query StandardOutputContent --output text
  local err
  err="$(aws ssm get-command-invocation --command-id "$id" --instance-id "$INSTANCE_ID" \
    --query StandardErrorContent --output text)"
  if [ -n "$err" ] && [ "$err" != "None" ]; then printf '%s\n' "$err" >&2; fi
  if [ "$status" != "Success" ]; then
    warn "remote '$1' ended $status (command $id; full output under s3://$TRANSFER_BUCKET/ssm-output/$id/)"
    return 1
  fi
}

# A random object key under the transfer bucket's handoff prefix.
handoff_key() { printf 'devbox/handoff/%s-%s-%s%s' "$1" "$(date +%Y%m%dT%H%M%S)" "$RANDOM$RANDOM" "$2"; }
