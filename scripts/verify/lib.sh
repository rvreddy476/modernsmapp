#!/usr/bin/env bash
# Shared helpers for scripts/verify/*.sh: one PASS/FAIL line per stage, the
# full output in a log file, and the changed-file list every mode starts from.
#
# Output contract (agents and the lead read it the same way):
#   PASS <stage> <target>  (<seconds>s)
#   FAIL <stage> <target>  (<seconds>s)  log: <path>
#   ...the last lines of a failing log, indented...
#   RESULT PASS | RESULT FAIL (<n> failed)
# Nothing here prints an environment value; a database address is shown by
# its database name only.

set -u

VERIFY_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
VERIFY_LOG_DIR="${VERIFY_LOG_DIR:-${TMPDIR:-/tmp}/verify-logs/$(date +%Y%m%dT%H%M%S)-$$}"
mkdir -p "$VERIFY_LOG_DIR"
VERIFY_FAILED=0
VERIFY_TAIL_LINES="${VERIFY_TAIL_LINES:-30}"

# run_stage <stage> <target> <dir> <command...>: runs the command in dir,
# writes its output to a log, prints one line.
run_stage() {
  local stage="$1" target="$2" dir="$3"
  shift 3
  local safe log start rc
  safe="$(printf '%s-%s' "$stage" "$target" | tr -c 'A-Za-z0-9._-' '_')"
  log="$VERIFY_LOG_DIR/$safe.log"
  start=$SECONDS
  (cd "$dir" && "$@") >"$log" 2>&1
  rc=$?
  if [ $rc -eq 0 ]; then
    printf 'PASS %-14s %s  (%ss)\n' "$stage" "$target" "$((SECONDS - start))"
  else
    VERIFY_FAILED=$((VERIFY_FAILED + 1))
    printf 'FAIL %-14s %s  (%ss)  log: %s\n' "$stage" "$target" "$((SECONDS - start))" "$log"
    tail -n "$VERIFY_TAIL_LINES" "$log" | sed 's/^/    /'
  fi
  return $rc
}

verify_result() {
  if [ "$VERIFY_FAILED" -eq 0 ]; then
    echo "RESULT PASS  (logs: $VERIFY_LOG_DIR)"
    return 0
  fi
  echo "RESULT FAIL ($VERIFY_FAILED failed)  (logs: $VERIFY_LOG_DIR)"
  return 1
}

# changed_files [base]: repo-relative paths changed in the working tree
# (staged, unstaged, untracked) plus, with a base revision, everything
# changed since it. Deleted files are included: a deletion still touches
# its module.
changed_files() {
  local base="${1:-}"
  {
    git -C "$VERIFY_ROOT" status --porcelain --untracked-files=all |
      sed -E 's/^.{3}//; s/^.* -> //; s/^"//; s/"$//'
    if [ -n "$base" ]; then
      git -C "$VERIFY_ROOT" diff --name-only "$base"...HEAD
    fi
  } | sort -u
}

# db_name_of <dsn>: the database name of a postgres URL or key=value DSN.
db_name_of() {
  local dsn="$1" name
  case "$dsn" in
    postgres://* | postgresql://*)
      name="${dsn%%\?*}"
      name="${name##*/}"
      ;;
    *)
      name="$(printf '%s' "$dsn" | tr ' ' '\n' | sed -n 's/^dbname=//p' | head -1)"
      ;;
  esac
  printf '%s' "$name"
}

# dsn_vars <module-dir>: the DSN variables the module's tests read:
# os.Getenv/os.LookupEnv("..._DSN" / "...DATABASE_URL...") in *_test.go files
# and in any .go file carrying the integration build tag (test helpers such
# as analytics-service/internal/testsupport). scripts/ci/integration-
# manifest-check.sh uses the same function, so the CI manifest and go.sh
# always agree on the list.
dsn_vars() {
  {
    grep -rl --include='*_test.go' -e 'Getenv' -e 'LookupEnv' "$1" 2>/dev/null
    grep -rlE --include='*.go' '^//go:build (.*[ (&|])?integration([ )&|]|$)' "$1" 2>/dev/null
  } | sort -u |
    xargs -r -d '\n' grep -hoE 'os\.(Getenv|LookupEnv)\("[A-Z0-9_]*(DSN|DATABASE_URL)[A-Z0-9_]*"\)' 2>/dev/null |
    sed -E 's/.*"([A-Z0-9_]+)".*/\1/' | sort -u
}

# require_test_db <dsn>: refuses any database that is not a scratch *_test one.
require_test_db() {
  local name
  name="$(db_name_of "$1")"
  case "$name" in
    *_test) return 0 ;;
  esac
  echo "REFUSED: database '${name:-<none>}' does not end in _test; integration tests run only on scratch _test databases" >&2
  return 1
}
