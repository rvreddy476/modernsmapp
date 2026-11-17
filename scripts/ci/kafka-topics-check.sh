#!/usr/bin/env bash
# Every Kafka topic the Go services name must be in the MSK topic list
# (infra/terraform/modules/msk/topics-job/variables.tf). MSK runs with
# auto.create.topics.enable=false, so a topic missing there does not exist in
# QA or prod (reels-3: media.events.long). No database, no Docker, no build:
# it parses the code. Run from anywhere:
#
#   scripts/ci/kafka-topics-check.sh [-v]
#
# Prints MISSING lines, then PASS/FAIL and RESULT PASS|FAIL; exits non-zero
# on a missing topic or when it could not check. How topics are found:
# scripts/ci/kafka-topics/main.go.
set -u
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT" || exit 2
# A stdlib-only file outside every module: no go.mod or go.work applies.
GOWORK=off go run scripts/ci/kafka-topics/main.go -root . "$@"
