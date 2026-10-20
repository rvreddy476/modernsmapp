#!/usr/bin/env bash
# Creates or updates the devbox CloudFormation stack (one EC2 server, its
# role and the S3 buckets), prints the outputs and waits until the server
# has finished its first-boot setup. Safe to re-run: no change, no-op.
#
#   AWS_PROFILE=atpost-qa ./deploy-stack.sh
#
# Optional: DEVBOX_NIGHTLY=true (stop 02:30 / start 08:00 IST),
# DEVBOX_INSTANCE_TYPE=m7i.2xlarge, DEVBOX_SUBNET_ID=subnet-... (default: the
# default VPC's subnet in ap-south-1a), DEVBOX_EGRESS_USER=false.
source "$(dirname "$0")/lib.sh"
check_account

VPC_ID="$(aws ec2 describe-vpcs --filters Name=is-default,Values=true --query 'Vpcs[0].VpcId' --output text)"
[ -n "$VPC_ID" ] && [ "$VPC_ID" != "None" ] || die "no default VPC in $AWS_REGION"
SUBNET_ID="${DEVBOX_SUBNET_ID:-$(aws ec2 describe-subnets \
  --filters Name=vpc-id,Values="$VPC_ID" Name=default-for-az,Values=true Name=availability-zone,Values="${AWS_REGION}a" \
  --query 'Subnets[0].SubnetId' --output text)}"
[ -n "$SUBNET_ID" ] && [ "$SUBNET_ID" != "None" ] || die "no default subnet found; set DEVBOX_SUBNET_ID"

log "stack $DEVBOX_STACK in $AWS_REGION (account $DEVBOX_ACCOUNT), VPC $VPC_ID, subnet $SUBNET_ID"
aws cloudformation deploy \
  --stack-name "$DEVBOX_STACK" \
  --template-file "$(winpath "$DEVBOX_DIR/devbox.cfn.yaml")" \
  --capabilities CAPABILITY_NAMED_IAM \
  --no-fail-on-empty-changeset \
  --tags atpost:env=devbox \
  --parameter-overrides \
    VpcId="$VPC_ID" \
    SubnetId="$SUBNET_ID" \
    InstanceType="${DEVBOX_INSTANCE_TYPE:-m7i.2xlarge}" \
    EnableNightlySchedule="${DEVBOX_NIGHTLY:-false}" \
    CreateLiveEgressUser="${DEVBOX_EGRESS_USER:-true}"

log "outputs:"
aws cloudformation describe-stacks --stack-name "$DEVBOX_STACK" \
  --query 'Stacks[0].Outputs[].[OutputKey,OutputValue]' --output table

load_stack
[ "$(instance_state)" = "running" ] || { log "instance is $(instance_state); start it with ./start.sh"; exit 0; }
wait_ssm_online
for i in $(seq 1 40); do
  if ssm_run 120 wait-ready 2>/dev/null; then
    log "server ready. Next: ./push-code.sh"
    exit 0
  fi
  [ "$i" = 1 ] && log "first-boot setup (Docker, AWS CLI, cloudflared) still running; waiting ..."
  sleep 15
done
die "first-boot setup did not finish in 10 minutes; ./shell.sh then: sudo tail -n 80 /var/log/atpost-devbox-init.log"
