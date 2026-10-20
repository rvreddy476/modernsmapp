#!/usr/bin/env bash
# A shell on the server through SSM Session Manager (no SSH, no open port).
#
#   ./shell.sh                      interactive shell (ssm-user; sudo -i for root)
#   ./shell.sh port 5432 15432      forward server port 5432 to localhost:15432
#                                   (pgAdmin, psql, cqlsh, Redpanda console ...)
#
# Needs the Session Manager plugin for the AWS CLI:
# https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html
source "$(dirname "$0")/lib.sh"
check_account
load_stack
command -v session-manager-plugin >/dev/null 2>&1 || die "install the AWS Session Manager plugin first (link at the top of this script)"
[ "$(instance_state)" = "running" ] || die "instance is $(instance_state); run ./start.sh"

case "${1:-}" in
  "") exec aws ssm start-session --target "$INSTANCE_ID" ;;
  port)
    remote="${2:?usage: ./shell.sh port <server-port> [local-port]}"
    local_port="${3:-$remote}"
    log "localhost:$local_port -> server :$remote (Ctrl+C to stop)"
    exec aws ssm start-session --target "$INSTANCE_ID" \
      --document-name AWS-StartPortForwardingSession \
      --parameters "portNumber=$remote,localPortNumber=$local_port" ;;
  *) die "usage: ./shell.sh [port <server-port> [local-port]]" ;;
esac
