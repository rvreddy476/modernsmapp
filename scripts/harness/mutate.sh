#!/usr/bin/env bash
# Mutation runner: break each guard on purpose, run a check, restore by hash.
#
#   scripts/harness/mutate.sh <spec.tsv> -- <check command...>
#
# spec.tsv: one mutation per line, tab-separated, '#' comments allowed:
#   <id>  <category>  <repo-relative file>  <exact old text>  <new text>  [check command]
# <category> is money | security | privacy | permissions | logic. The old
# text must occur exactly once in the file (else NOT_APPLIED). The check
# command (default: the one after --) must FAIL with the mutation in place:
# a failing check is KILLED, a passing one SURVIVED.
#
# Safety (25 Oct incident: two overlapping runs shared one backup and
# scrambled three files):
#   - one run per tree: a lock dir holding the runner's pid; a second run
#     refuses, and a stale lock is never taken over silently;
#   - every file's hash is recorded before the run; before each mutation all
#     files must still match, or the run stops;
#   - backups live in a per-run folder; each restore is checked by hash and
#     the run stops on any mismatch;
#   - an interrupt restores the file under mutation before exiting.
# Results: <run dir>/results.tsv and a summary line.

set -u
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT" || exit 2

SPEC="${1:-}"
[ -n "$SPEC" ] && [ -f "$SPEC" ] || { sed -n '2,22p' "$0"; exit 2; }
shift
[ "${1:-}" = "--" ] && shift
DEFAULT_CHECK=("$@")

LOCK="$ROOT/.mutate.lock"
if ! mkdir "$LOCK" 2>/dev/null; then
  holder="$(cat "$LOCK/pid" 2>/dev/null || echo unknown)"
  echo "REFUSED: a mutation run holds $LOCK (pid $holder)."
  echo "If that run is truly gone, check 'git status' for half-restored files, then remove the lock by hand."
  exit 3
fi
echo $$ >"$LOCK/pid"

RUN="${TMPDIR:-/tmp}/mutate-runs/$(date +%Y%m%dT%H%M%S)-$$"
mkdir -p "$RUN/backup"
RESULTS="$RUN/results.tsv"
printf 'id\tcategory\tfile\toutcome\tlog\n' >"$RESULTS"

hash_of() { sha1sum "$1" | cut -d' ' -f1; }
CURRENT=""          # file under mutation, for the interrupt handler
declare -A START=() # file -> hash at the start of the run

restore() {
  local f="$1" b="$RUN/backup/$(printf '%s' "$1" | tr '/' '_')"
  rm -f "$f" && cp "$b" "$f" # rm first: a perl -i rewrite can leave cp unable to overwrite
  if [ "$(hash_of "$f")" != "${START[$f]}" ]; then
    echo "STOP: restore of $f does not match its starting hash; backup kept at $b"
    return 1
  fi
}
on_exit() {
  local rc=$?
  if [ -n "$CURRENT" ]; then
    echo "interrupted: restoring $CURRENT"
    restore "$CURRENT" || rc=4
  fi
  rm -rf "$LOCK"
  exit $rc
}
trap on_exit EXIT
trap 'exit 130' INT TERM

# field <line> <n>: the nth tab-separated field. cut, not read: read folds
# consecutive tabs, so an empty replacement text would shift every field.
field() { printf '%s' "$1" | cut -f"$2"; }

mapfile -t LINES < <(grep -vE '^\s*(#|$)' "$SPEC" | tr -d '\r')
for line in "${LINES[@]}"; do
  f="$(field "$line" 3)"
  [ -f "$f" ] || { echo "STOP: $f in the spec does not exist"; exit 2; }
  START[$f]="$(hash_of "$f")"
  cp "$f" "$RUN/backup/$(printf '%s' "$f" | tr '/' '_')"
done

killed=0 survived=0 notapplied=0
for line in "${LINES[@]}"; do
  id="$(field "$line" 1)" category="$(field "$line" 2)" f="$(field "$line" 3)"
  old="$(field "$line" 4)" new="$(field "$line" 5)" check="$(field "$line" 6-)"
  for g in "${!START[@]}"; do
    if [ "$(hash_of "$g")" != "${START[$g]}" ]; then
      echo "STOP: $g changed while the run was going; nothing more is mutated"
      exit 5
    fi
  done
  count="$(OLD="$old" perl -0777 -ne '$n = () = /\Q$ENV{OLD}\E/g; print $n' "$f")"
  if [ "$count" != 1 ]; then
    printf '%s\t%s\t%s\tNOT_APPLIED(%s matches)\t-\n' "$id" "$category" "$f" "$count" >>"$RESULTS"
    echo "NOT_APPLIED $id ($count matches of the old text)"
    notapplied=$((notapplied + 1))
    continue
  fi
  CURRENT="$f"
  OLD="$old" NEW="$new" perl -0777 -pi -e 's/\Q$ENV{OLD}\E/$ENV{NEW}/' "$f"
  log="$RUN/$id.log"
  if [ -n "${check:-}" ]; then
    bash -c "$check" >"$log" 2>&1
  else
    "${DEFAULT_CHECK[@]}" >"$log" 2>&1
  fi
  rc=$?
  restore "$f" || exit 4
  CURRENT=""
  if [ $rc -ne 0 ]; then
    outcome=KILLED; killed=$((killed + 1))
  else
    outcome=SURVIVED; survived=$((survived + 1))
  fi
  printf '%s\t%s\t%s\t%s\t%s\n' "$id" "$category" "$f" "$outcome" "$log" >>"$RESULTS"
  echo "$outcome $id [$category]"
done

total=$((killed + survived))
echo "MUTATIONS killed $killed/$total, survived $survived, not applied $notapplied  (results: $RESULTS)"
[ $survived -eq 0 ] && [ $notapplied -eq 0 ]
