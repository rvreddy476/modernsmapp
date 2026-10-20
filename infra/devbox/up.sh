#!/usr/bin/env bash
# Brings the stack up on the server, or reports on it.
#
#   ./up.sh                 docker compose ... -f docker-compose.devbox.yml up -d --build,
#                           in the background with a log; returns at once
#   ./up.sh --no-build      same without rebuilding images
#   ./up.sh status          last lines of the up log, every container's state,
#                           anything exited non-zero, tunnel and disk
#   ./up.sh logs <service> [lines]
#
# A fresh server initialises itself on the first up: databases
# (init-databases.sql), every service's schema and migrations (each applies
# its own at boot), Scylla keyspaces (scylla-init), Kafka topics
# (redpanda-init + devbox-topics), the Creative Studio tables
# (devbox-studio-schema). The S3 buckets already exist. The first build
# takes a while; poll ./up.sh status.
#
# The server also gets a boot unit (atpost-stack.service) that runs
# `up -d` without building whenever the instance starts.
source "$(dirname "$0")/lib.sh"
check_account
load_stack
[ "$(instance_state)" = "running" ] || die "instance is $(instance_state); run ./start.sh"
wait_ssm_online

case "${1:-up}" in
  up|--build)  ssm_run 300 up ;;
  --no-build)  ssm_run 300 up --no-build ;;
  status)      ssm_run 120 status ;;
  logs)        [ -n "${2:-}" ] || die "usage: ./up.sh logs <service> [lines]"
               ssm_run 120 logs "$2" "${3:-120}" ;;
  *)           die "usage: ./up.sh [--no-build|status|logs <service> [lines]]" ;;
esac
