#!/usr/bin/env bash
# Stops the server (compute billing stops; the disk and the buckets stay).
# Containers and data survive; ./start.sh brings everything back.
source "$(dirname "$0")/lib.sh"
check_account
load_stack
state="$(instance_state)"
case "$state" in
  stopped) log "already stopped"; exit 0 ;;
  stopping) ;;
  *) aws ec2 stop-instances --instance-ids "$INSTANCE_ID" --query 'StoppingInstances[0].CurrentState.Name' --output text >/dev/null ;;
esac
log "stopping $INSTANCE_ID ..."
aws ec2 wait instance-stopped --instance-ids "$INSTANCE_ID"
log "stopped. cleestudio.com is down until ./start.sh"
