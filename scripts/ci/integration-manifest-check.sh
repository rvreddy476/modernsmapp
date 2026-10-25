#!/usr/bin/env bash
# Check scripts/ci/integration-services.yaml against the code. No database,
# no Docker: safe to run anywhere, and the integration-all workflow runs it
# before building its matrix.
#
#   scripts/ci/integration-manifest-check.sh
#
# Fails when:
#   - a Go module has //go:build integration files, or tests that read a DSN
#     variable, but is neither in the manifest nor excluded with a reason;
#   - an entry is malformed: missing field, unknown need, wave not 1|2, a
#     database name not ending in _test, a schema path that matches nothing;
#   - the DSN variables an entry assigns are not exactly the ones its tests
#     read (detected with go.sh's own dsn_vars, scripts/verify/lib.sh);
#   - the module is not in its workspace's go.work;
#   - integration-setup.sh --dry-run or integration-matrix.sh fails on it.
# Prints one PASS/FAIL line per module and ends with RESULT PASS|FAIL.

set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
source "$HERE/../verify/lib.sh"
source "$HERE/integration-manifest-lib.sh"
cd "$MF_ROOT" || exit 2

ALLOWED_NEEDS=" postgres redis kafka opensearch scylla minio "
ALLOWED_IMAGES=" postgres:16 postgis/postgis:16-3.4 "
FAILED=0
PROBLEMS=()
problem() { PROBLEMS+=("$*"); }

if ! mf_load; then
  echo "FAIL manifest       parse error (above)"
  echo "RESULT FAIL (1 failed)"
  exit 1
fi

# Every Go module, deepest first (same list go.sh uses).
mapfile -t ALL_MODULES < <(git ls-files '*go.mod' | grep -v '/vendor/' | grep -v '^\.claude/' |
  sed 's#/\?go\.mod$##' | awk '{ print length, $0 }' | sort -rn | cut -d' ' -f2-)

module_of() {
  local path="$1" m
  for m in "${ALL_MODULES[@]}"; do
    case "$path" in "$m"/*) printf '%s\n' "$m"; return 0 ;; esac
  done
  return 1
}

# Modules that need coverage: integration-tagged files, or tests reading a DSN.
declare -A NEEDS_COVER
while IFS= read -r f; do
  m="$(module_of "$f")" && NEEDS_COVER[$m]="integration-tagged files"
done < <(git ls-files '*.go' | grep -v '/vendor/' |
  xargs -r -d '\n' grep -lE '^//go:build (.*[ (&|])?integration([ )&|]|$)' 2>/dev/null)
for m in "${ALL_MODULES[@]}"; do
  [ -n "${NEEDS_COVER[$m]:-}" ] && continue
  if [ -n "$(dsn_vars "$m")" ]; then NEEDS_COVER[$m]="tests read a DSN variable"; fi
done

declare -A IN_MANIFEST EXCLUDED
mapfile -t MODULES < <(mf_modules)
for m in "${MODULES[@]}"; do
  [ -n "${IN_MANIFEST[$m]:-}" ] && problem "$m is listed twice"
  IN_MANIFEST[$m]=1
done
while IFS=$'\t' read -r m reason; do
  [ -n "$m" ] || continue
  [ -n "$reason" ] || problem "exclude: $m has no reason"
  [ -n "${IN_MANIFEST[$m]:-}" ] && problem "$m is both listed and excluded"
  [ -f "$m/go.mod" ] || problem "exclude: $m is not a Go module"
  EXCLUDED[$m]=1
done < <(mf_excluded)

for m in "${!NEEDS_COVER[@]}"; do
  if [ -z "${IN_MANIFEST[$m]:-}" ] && [ -z "${EXCLUDED[$m]:-}" ]; then
    problem "$m has ${NEEDS_COVER[$m]} but is not in the manifest (add it, or exclude it with a reason)"
  fi
done

for p in "${PROBLEMS[@]}"; do echo "FAIL coverage       $p"; done
FAILED=$((FAILED + ${#PROBLEMS[@]}))
[ ${#PROBLEMS[@]} -eq 0 ] && echo "PASS coverage       ${#NEEDS_COVER[@]} modules need a database, ${#MODULES[@]} listed, ${#EXCLUDED[@]} excluded"

contains() { case "$1" in *" $2 "*) return 0 ;; esac; return 1; }

check_entry() {
  local m="$1" i errs=() key v x detected assigned ws need dup missing extra out pat
  i="$(mf_index "$m")"
  [ -f "$m/go.mod" ] || errs+=("no go.mod")
  [ -n "${NEEDS_COVER[$m]:-}" ] || errs+=("no integration-tagged files and no DSN reads: stale entry?")
  for key in db wave needs vars; do
    [ -n "$(mf_get "$i" "$key")" ] || errs+=("missing '$key'")
  done
  v="$(mf_get "$i" wave)"
  case "$v" in 1 | 2 | "") ;; *) errs+=("wave '$v' is not 1 or 2") ;; esac
  mf_get "$i" needs | grep -qx postgres || errs+=("needs must include postgres")
  for key in needs optional; do
    while IFS= read -r need; do
      [ -n "$need" ] || continue
      contains "$ALLOWED_NEEDS" "$need" || errs+=("unknown $key '$need'")
    done < <(mf_get "$i" "$key")
  done
  v="$(mf_get "$i" pg_image)"
  [ -z "$v" ] || contains "$ALLOWED_IMAGES" "$v" || errs+=("pg_image '$v' is not one of:$ALLOWED_IMAGES")
  v="$(mf_get "$i" go_flags)"
  case " $v " in *" -run "* | *" -tags"* | *" -p "*) errs+=("go_flags may not override -run, -tags or -p") ;; esac

  # Databases and variable partition.
  for x in "" $(mf_extras "$i" | sed 's/^/extra./; s/$/./'); do
    v="$(mf_get "$i" "${x}db")"
    [ -z "$v" ] || mf_db_ok "$v" || errs+=("database '$v' must be lower-case and end in _test")
    while IFS= read -r pat; do
      [ -n "$pat" ] || continue
      if [ -z "$(mf_expand "$pat")" ]; then
        errs+=("schema '$pat' matches no file")
      elif mf_expand "$pat" | grep -vq '\.sql$'; then
        errs+=("schema '$pat' matches a non-.sql file")
      fi
    done < <(mf_get "$i" "${x}schema")
  done
  assigned="$( { mf_get "$i" vars; for x in $(mf_extras "$i"); do mf_get "$i" "extra.$x.vars"; done; } | sort)"
  detected="$(dsn_vars "$m")"
  dup="$(uniq -d <<<"$assigned")"
  [ -z "$dup" ] || errs+=("assigned twice: $(tr '\n' ' ' <<<"$dup")")
  missing="$(comm -23 <(sort -u <<<"$detected") <(sort -u <<<"$assigned") | tr '\n' ' ')"
  extra="$(comm -13 <(sort -u <<<"$detected") <(sort -u <<<"$assigned") | tr '\n' ' ')"
  [ -z "${missing// /}" ] || errs+=("tests read DSN vars the manifest does not assign: $missing")
  [ -z "${extra// /}" ] || errs+=("manifest assigns DSN vars no test reads: $extra")

  # Workspace.
  ws="${m%%/*}"
  if [ ! -f "$ws/go.work" ]; then
    errs+=("no $ws/go.work for setup-go")
  elif ! grep -qE "^[[:space:]]*(use[[:space:]]+)?\./${m#"$ws"/}[[:space:]]*$" "$ws/go.work"; then
    errs+=("not listed in $ws/go.work")
  fi

  # The setup script must accept it.
  if ! out="$(bash "$HERE/integration-setup.sh" --dry-run "$m" 2>&1)"; then
    errs+=("integration-setup.sh --dry-run: $(tail -1 <<<"$out")")
  fi

  if [ ${#errs[@]} -eq 0 ]; then
    printf 'PASS %-14s %s  (wave %s, db %s)\n' entry "$m" "$(mf_get "$i" wave)" "$(mf_get "$i" db)"
  else
    printf 'FAIL %-14s %s\n' entry "$m"
    printf '    %s\n' "${errs[@]}"
    FAILED=$((FAILED + 1))
  fi
}

for m in "${MODULES[@]}"; do check_entry "$m"; done

# The matrix must be valid JSON with one include per entry.
if matrix="$(bash "$HERE/integration-matrix.sh" all)"; then
  n="$(grep -o '"module":' <<<"$matrix" | wc -l | tr -d ' ')"
  json_ok=1
  if command -v node >/dev/null; then
    node -e 'JSON.parse(process.argv[1])' "$matrix" 2>/dev/null || json_ok=0
  elif command -v jq >/dev/null; then
    jq -e . >/dev/null <<<"$matrix" || json_ok=0
  fi
  if [ "$json_ok" -eq 1 ] && [ "$n" -eq ${#MODULES[@]} ]; then
    echo "PASS matrix         $n jobs"
  else
    echo "FAIL matrix         invalid JSON or $n jobs for ${#MODULES[@]} entries"
    FAILED=$((FAILED + 1))
  fi
else
  echo "FAIL matrix         integration-matrix.sh failed"
  FAILED=$((FAILED + 1))
fi

# Cross-check the awk reader against a real YAML parser when one is around
# (bun locally; CI runners have neither requirement).
if command -v bun >/dev/null; then
  ours="$(awk -F'\t' '$1 == "modules" && ($3 == "module" || $3 == "db") { print $4 }' <<<"$MF_FLAT")"
  theirs="$(bun -e '
    const y = Bun.YAML.parse(await Bun.file(process.argv[1]).text());
    for (const e of y.modules) { console.log(e.module); console.log(e.db); }
  ' "${INTEG_MANIFEST:-$MF_FILE_DEFAULT}" 2>&1)"
  if [ "$ours" = "$theirs" ]; then
    echo "PASS yaml-parse     the awk reader agrees with Bun.YAML"
  else
    echo "FAIL yaml-parse     the awk reader and Bun.YAML disagree"
    diff <(echo "$ours") <(echo "$theirs") | head -10 | sed 's/^/    /'
    FAILED=$((FAILED + 1))
  fi
fi

if [ "$FAILED" -eq 0 ]; then
  echo "RESULT PASS"
  exit 0
fi
echo "RESULT FAIL ($FAILED failed)"
exit 1
