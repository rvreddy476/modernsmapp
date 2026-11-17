#!/usr/bin/env bash
# Fail when `go test -v` output shows a test skipped for a named reason, or
# when a required test did not PASS. CI uses it so that tests which skip
# without a tool (ffmpeg) cannot pass silently (reels-12).
#
#   scripts/ci/go-test-skip-check.sh <go-test-v.log> <reason-regex> [Test ...]
#
#   reason-regex  extended regex, case-insensitive, matched against the log
#                 lines a skipped test printed (its t.Skip reason)
#   Test ...      top-level or sub-test names that must show "--- PASS"
#
# Prints the offending tests, then PASS/FAIL and RESULT, like scripts/verify.
set -u
LOG="${1:-}"
REASON="${2:-}"
[ -n "$LOG" ] && [ -f "$LOG" ] && [ -n "$REASON" ] || { sed -n '2,12p' "$0"; exit 2; }
shift 2

# Attribute every indented log line to the test that printed it: the last
# "=== RUN/CONT/PAUSE/NAME" line, or the "--- SKIP/PASS/FAIL" line just
# before it (Go prints sub-test logs after the result line).
mapfile -t BAD < <(awk -v re="$REASON" '
  /^=== (RUN|CONT|PAUSE|NAME) / { cur = $3; next }
  /^ *--- (SKIP|PASS|FAIL): / {
    name = $3; cur = name
    if ($2 == "SKIP:") skipped[name] = 1
    next
  }
  /^    / && cur != "" {
    line = tolower($0)
    if (line ~ tolower(re)) hit[cur] = 1
  }
  END { for (t in skipped) if (t in hit) print t }
' "$LOG" | sort)

MISSING=()
for t in "$@"; do
  grep -qE -- "^ *--- PASS: ${t} \(" "$LOG" || MISSING+=("$t")
done

for t in "${BAD[@]}"; do echo "SKIPPED  $t  (reason matches /$REASON/)"; done
for t in "${MISSING[@]}"; do echo "NOT RUN  $t  (no --- PASS line)"; done

if [ ${#BAD[@]} -gt 0 ] || [ ${#MISSING[@]} -gt 0 ]; then
  echo "FAIL skip-check     ${#BAD[@]} skipped for /$REASON/, ${#MISSING[@]} required tests did not pass"
  echo "RESULT FAIL (1 failed)"
  exit 1
fi
echo "PASS skip-check     $(grep -cE -- '^ *--- PASS: ' "$LOG") passed; none skipped for /$REASON/; $# required passed"
echo "RESULT PASS"
