#!/usr/bin/env bash
# Flag migration statements that the PREVIOUS image may not survive.
#
# Why: services apply their migrations when the first pod of a new image
# starts, while pods of the old image still serve, and a rollback to image
# N-1 does not undo them (migrations are forward-only). So every migration
# must work with the previous image: add first ("expand"), remove or tighten
# in a LATER release ("contract"). Rule and procedure:
# docs/runbooks/service-rollback.md.
#
#   scripts/verify/migration-compat.sh <base-ref> [<head-ref>] [-- <path>...]
#       check the .sql / .cql files under a migrations/ directory that were
#       ADDED or CHANGED between the two git refs (head defaults to HEAD).
#       Image tags are commit shas, so base = the N-1 tag, head = the N tag.
#       Optional paths narrow it to one service.
#   scripts/verify/migration-compat.sh --files <file>...
#       check exactly these files (no git).
#   scripts/verify/migration-compat.sh --self-test
#       run the built-in cases.
#
# Exit 0: nothing flagged. Exit 1: at least one statement flagged. Exit 2:
# usage error. Reads only; never writes to git or a database.
#
# What is flagged (per statement, case-insensitive, comments ignored):
#   DROP-COLUMN     ALTER TABLE ... DROP COLUMN        N-1 still selects it
#   DROP-OBJECT     DROP TABLE / TYPE / VIEW / SCHEMA  N-1 still uses it
#   RENAME          ALTER ... RENAME ...               N-1 uses the old name
#   SET-NOT-NULL    ALTER COLUMN ... SET NOT NULL      N-1 inserts without it
#   ADD-NOT-NULL    ADD COLUMN ... NOT NULL, no DEFAULT  same
#   ALTER-TYPE      ALTER COLUMN ... TYPE              N-1 writes the old type
#   ADD-CONSTRAINT  ADD [CONSTRAINT] ... CHECK|UNIQUE|FOREIGN KEY|EXCLUDE,
#                   CREATE UNIQUE INDEX                N-1 may write rows the
#                                                      new rule refuses
#   DATA-LOSS       TRUNCATE                           not undone by rollback
#
# It is a text check, not a SQL parser: it can miss a statement built inside
# a function and it cannot know that a dropped column is already unused. A
# statement that is safe for a known reason (the "contract" half, shipped a
# release after the code stopped using the column) is acknowledged in the
# migration itself, on a comment line inside the statement:
#     -- compat: ok <reason, naming the release that stopped using it>
# Acknowledged statements are printed and do not fail the check.
#
# Not wired into CI (16 Nov 2026). Schema that a service creates in Go at
# boot (post-service ensureSchema) is not in a migration file and is not
# seen here.
set -euo pipefail

usage() { sed -n '2,24p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//' >&2; exit 2; }

# check_files <file>...  prints findings, returns 1 when any is unacknowledged.
check_files() {
  [[ $# -gt 0 ]] || return 0
  awk '
    function flag(rule, why) {
      if (ack != "") {
        printf "%s:%d: acknowledged %s (%s)\n", file, start, rule, ack
        acked++
      } else {
        printf "%s:%d: %s: %s\n      %s\n", file, start, rule, why, shown
        bad++
      }
    }
    function check(s,    u, rest, seg, cut, n, i, parts) {
      u = toupper(s)
      gsub(/[ \t\r\n]+/, " ", u)
      shown = s; gsub(/[ \t\r\n]+/, " ", shown); sub(/^ /, "", shown)
      if (length(shown) > 160) shown = substr(shown, 1, 157) "..."
      if (u ~ /^ *$/) return
      if (u ~ /DROP COLUMN/) flag("DROP-COLUMN", "the previous image still reads or writes this column")
      if (u ~ /DROP (TABLE|TYPE|VIEW|MATERIALIZED VIEW|SCHEMA)/) flag("DROP-OBJECT", "the previous image still uses this object")
      if (u ~ /ALTER .* RENAME /) flag("RENAME", "the previous image uses the old name")
      if (u ~ /SET NOT NULL/) flag("SET-NOT-NULL", "the previous image may insert rows without this column")
      if (u ~ /ALTER COLUMN [^ ]+ (SET DATA )?TYPE /) flag("ALTER-TYPE", "the previous image writes the old type")
      if (u ~ /TRUNCATE /) flag("DATA-LOSS", "a rollback does not bring the rows back")
      if (u ~ /CREATE UNIQUE INDEX/ || u ~ /ADD (CONSTRAINT [^ ]+ )?(CHECK|UNIQUE|FOREIGN KEY|EXCLUDE)/)
        flag("ADD-CONSTRAINT", "the previous image may write rows the new rule refuses")
      # ADD COLUMN ... NOT NULL without DEFAULT, clause by clause.
      n = split(u, parts, /ADD COLUMN /)
      for (i = 2; i <= n; i++) {
        seg = parts[i]
        cut = match(seg, /, *(ADD|ALTER|DROP|RENAME) /)
        if (cut > 0) seg = substr(seg, 1, cut - 1)
        if (seg ~ /NOT NULL/ && seg !~ /DEFAULT/ && seg !~ /GENERATED/) {
          flag("ADD-NOT-NULL", "the previous image inserts rows without this column and there is no DEFAULT")
          break
        }
      }
    }
    FNR == 1 {
      if (stmt ~ /[^ \t\r\n]/) check(stmt)   # last statement of the previous file, no ";"
      file = FILENAME; stmt = ""; ack = ""; start = 0; incomment = 0
    }
    {
      line = $0
      # The acknowledgement marker, read before comments are stripped.
      if (match(line, /--[ \t]*compat:[ \t]*ok[ \t]+[^ \t\r]/)) {
        ack = substr(line, RSTART + RLENGTH - 1); sub(/[ \t\r]+$/, "", ack)
      }
      # Block comments (single-line and multi-line), then line comments.
      while (1) {
        if (incomment) {
          e = index(line, "*/")
          if (e == 0) { line = ""; break }
          line = substr(line, e + 2); incomment = 0
        }
        b = index(line, "/*")
        if (b == 0) break
        e = index(substr(line, b + 2), "*/")
        if (e == 0) { line = substr(line, 1, b - 1); incomment = 1; break }
        line = substr(line, 1, b - 1) " " substr(line, b + 2 + e + 1)
      }
      sub(/--.*$/, "", line)
      # Statements end at ";". A line may hold several.
      while ((p = index(line, ";")) > 0) {
        piece = substr(line, 1, p - 1)
        if (start == 0 && piece ~ /[^ \t\r]/) start = FNR
        stmt = stmt " " piece
        check(stmt)
        stmt = ""; ack = ""; start = 0
        line = substr(line, p + 1)
      }
      if (line ~ /[^ \t\r]/) { if (start == 0) start = FNR; stmt = stmt " " line }
    }
    END {
      if (stmt ~ /[^ \t\r\n]/) check(stmt)
      if (bad + acked == 0) print "migration-compat: nothing flagged"
      else printf "migration-compat: %d flagged, %d acknowledged\n", bad, acked
      exit (bad > 0 ? 1 : 0)
    }
  ' "$@"
}

self_test() {
  local d rc out fails=0
  d="$(mktemp -d)"
  # shellcheck disable=SC2064
  trap "rm -rf '$d'" RETURN
  expect() { # expect <name> <want rc> <want substring or -> <file content>
    local name="$1" want="$2" sub="$3"
    printf '%s\n' "$4" > "$d/$name.sql"
    set +e; out="$(check_files "$d/$name.sql" 2>&1)"; rc=$?; set -e
    if [[ "$rc" -ne "$want" ]] || { [[ "$sub" != - ]] && [[ "$out" != *"$sub"* ]]; }; then
      echo "FAIL $name: rc=$rc want $want, output wants [$sub]:"; echo "$out" | sed 's/^/     /'; fails=$((fails + 1))
    else
      echo "ok   $name"
    fi
  }
  expect add-nullable      0 "nothing flagged" "ALTER TABLE posts ADD COLUMN IF NOT EXISTS lang TEXT;"
  expect add-with-default  0 "nothing flagged" "ALTER TABLE posts ADD COLUMN pinned BOOLEAN NOT NULL DEFAULT false;"
  expect create-table      0 "nothing flagged" "CREATE TABLE IF NOT EXISTS notes (id UUID PRIMARY KEY, body TEXT NOT NULL, UNIQUE (id));"
  expect create-index      0 "nothing flagged" "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_posts_lang ON posts (lang);"
  expect comment-only      0 "nothing flagged" "-- ALTER TABLE posts DROP COLUMN legacy;
/* ALTER TABLE posts RENAME COLUMN a TO b; */
SELECT 1;"
  expect drop-column       1 "DROP-COLUMN"   "ALTER TABLE posts DROP COLUMN legacy;"
  expect drop-column-case  1 "DROP-COLUMN"   "alter table posts
  drop column if exists legacy;"
  expect drop-table        1 "DROP-OBJECT"   "DROP TABLE IF EXISTS old_feed;"
  expect rename-column     1 "RENAME"        "ALTER TABLE posts RENAME COLUMN body TO content;"
  expect rename-table      1 "RENAME"        "ALTER TABLE posts RENAME TO posts_v1;"
  expect set-not-null      1 "SET-NOT-NULL"  "ALTER TABLE posts ALTER COLUMN lang SET NOT NULL;"
  expect add-not-null      1 "ADD-NOT-NULL"  "ALTER TABLE posts ADD COLUMN region TEXT NOT NULL;"
  expect add-not-null-2nd  1 "ADD-NOT-NULL"  "ALTER TABLE posts ADD COLUMN a INT NOT NULL DEFAULT 0, ADD COLUMN b INT NOT NULL;"
  expect alter-type        1 "ALTER-TYPE"    "ALTER TABLE posts ALTER COLUMN views TYPE BIGINT;"
  expect add-check         1 "ADD-CONSTRAINT" "ALTER TABLE posts ADD CONSTRAINT posts_kind_chk CHECK (kind IN ('post','reel'));"
  expect unique-index      1 "ADD-CONSTRAINT" "CREATE UNIQUE INDEX IF NOT EXISTS uq_posts_slug ON posts (slug);"
  expect truncate          1 "DATA-LOSS"     "TRUNCATE feed_cache;"
  expect inside-do-block   1 "DROP-COLUMN"   "DO \$\$ BEGIN
  ALTER TABLE posts DROP COLUMN legacy;
END \$\$;"
  expect line-number       1 ".sql:3: DROP-COLUMN" "SELECT 1;

ALTER TABLE posts
  DROP COLUMN legacy;"
  expect acknowledged      0 "acknowledged DROP-COLUMN" "ALTER TABLE posts
  -- compat: ok unused since release 2026-11-01 (code stopped reading it)
  DROP COLUMN legacy;"
  expect ack-not-carried   1 "1 flagged, 1 acknowledged" "-- compat: ok unused since 2026-11-01
ALTER TABLE posts DROP COLUMN legacy;
ALTER TABLE posts DROP COLUMN other;"
  expect two-on-a-line     1 "2 flagged"     "ALTER TABLE a DROP COLUMN x; ALTER TABLE b RENAME TO c;"
  if [[ "$fails" -ne 0 ]]; then echo "SELF-TEST FAILED ($fails)"; return 1; fi
  echo "self-test passed"
}

case "${1:-}" in
  ""|-h|--help) usage ;;
  --self-test) self_test; exit $? ;;
  --files) shift; [[ $# -gt 0 ]] || usage; check_files "$@"; exit $? ;;
esac

base="$1"; shift
head_ref=HEAD
if [[ $# -gt 0 && "$1" != -- ]]; then head_ref="$1"; shift; fi
paths=()
if [[ "${1:-}" == -- ]]; then shift; paths=("$@"); fi
root="$(git rev-parse --show-toplevel)"
cd "$root"
git rev-parse --verify --quiet "$base^{commit}" > /dev/null || { echo "migration-compat: unknown ref $base" >&2; exit 2; }
git rev-parse --verify --quiet "$head_ref^{commit}" > /dev/null || { echo "migration-compat: unknown ref $head_ref" >&2; exit 2; }

mapfile -t files < <(git diff --name-only --diff-filter=AM "$base" "$head_ref" -- "${paths[@]}" \
  | grep -E '(^|/)migrations/[^/]+\.(sql|cql)$' || true)
echo "migration-compat: ${#files[@]} migration file(s) added or changed between $base and $head_ref"
[[ ${#files[@]} -gt 0 ]] || exit 0
# Check the files as they are at head (not the working tree).
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
list=()
for f in "${files[@]}"; do
  mkdir -p "$tmp/$(dirname "$f")"
  git show "$head_ref:$f" > "$tmp/$f"
  list+=("$f")
done
cd "$tmp"
check_files "${list[@]}"
