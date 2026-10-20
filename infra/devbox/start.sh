#!/usr/bin/env bash
# Starts the server. On boot, cloudflared and the atpost-stack unit
# (docker compose up -d, no build) start by themselves.
source "$(dirname "$0")/lib.sh"
check_account
load_stack
state="$(instance_state)"
case "$state" in
  running) log "already running" ;;
  stopping) aws ec2 wait instance-stopped --instance-ids "$INSTANCE_ID"
            aws ec2 start-instances --instance-ids "$INSTANCE_ID" >/dev/null ;;
  *) aws ec2 start-instances --instance-ids "$INSTANCE_ID" >/dev/null ;;
esac
aws ec2 wait instance-running --instance-ids "$INSTANCE_ID"
wait_ssm_online
log "running. The stack takes a few minutes to come up: ./up.sh status"
