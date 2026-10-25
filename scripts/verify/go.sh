#!/usr/bin/env bash
# The one Go check every agent and the lead run (harness doc, change 5).
#
#   scripts/verify/go.sh [--fast|--full] [--base REV] [--db DSN] [module-dir...]
#
# Modules default to every Go module with a changed file (working tree, plus
# everything since --base). Module dirs are repo-relative, e.g.
# Architecture/services/post-service.
#
#   --fast  while working: build the module, vet and test only the packages
#           that changed (with and without the integration tag).
#   --full  once, before reporting (default): build, vet, vet -tags
#           integration, and test the whole module.
#   --db    also run the integration tests (-tags integration, -p 1) against
#           this scratch database. Every DSN variable the module's tests read
#           (os.Getenv("..._DSN")) is set to it unless already set; each one
#           must name a database ending in _test or the run is refused.
#           Agents never pass --db: there are no local databases. CI does.
#           The plain test stage then runs with those variables unset, and
#           GO_INTEG_FLAGS (e.g. "-timeout 30m") is added to the integration
#           go test; CI takes both from scripts/ci/integration-services.yaml.
#
# Prints one PASS/FAIL line per stage (scripts/verify/lib.sh).

source "$(dirname "$0")/lib.sh"

MODE=full
BASE=""
DSN=""
MODULES=()
while [ $# -gt 0 ]; do
  case "$1" in
    --fast) MODE=fast ;;
    --full) MODE=full ;;
    --base) BASE="$2"; shift ;;
    --db) DSN="$2"; shift ;;
    -h | --help) sed -n '2,24p' "$0"; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; exit 2 ;;
    *) MODULES+=("${1%/}") ;;
  esac
  shift
done

cd "$VERIFY_ROOT" || exit 2

# Every Go module in the repo, deepest first so the nearest go.mod wins.
mapfile -t ALL_MODULES < <(git ls-files '*go.mod' | grep -v '/vendor/' | grep -v '^\.claude/' |
  sed 's#/\?go\.mod$##' | awk '{ print length, $0 }' | sort -rn | cut -d' ' -f2-)

module_of() {
  local path="$1" m
  for m in "${ALL_MODULES[@]}"; do
    case "$path" in "$m"/*) printf '%s' "$m"; return 0 ;; esac
  done
  return 1
}

mapfile -t CHANGED < <(changed_files "$BASE")

if [ ${#MODULES[@]} -eq 0 ]; then
  mapfile -t MODULES < <(for f in "${CHANGED[@]}"; do
    case "$f" in *.go | *go.mod | *go.sum | *.sql) module_of "$f" && echo ;; esac
  done | sed '/^$/d' | sort -u)
fi
if [ ${#MODULES[@]} -eq 0 ]; then
  echo "No changed Go module. Name one: scripts/verify/go.sh --full Architecture/services/<svc>"
  exit 0
fi

if [ -n "$DSN" ]; then
  require_test_db "$DSN" || exit 2
fi

# changed_packages <module>: ./relative package dirs with a changed .go file.
changed_packages() {
  local m="$1" f dir
  for f in "${CHANGED[@]}"; do
    case "$f" in "$m"/*.go) ;; *) continue ;; esac
    [ "$(module_of "$f")" = "$m" ] || continue
    dir="$(dirname "${f#"$m"/}")"
    [ -d "$m/$dir" ] || continue # a deleted package
    if [ "$dir" = "." ]; then echo "."; else echo "./$dir"; fi
  done | sort -u
}

# dsn_vars <module>: the DSN variables the module's tests read (lib.sh).

# Extra flags for the integration go test only (e.g. -timeout 30m, -skip RE),
# word-split, no globbing. CI sets it from scripts/ci/integration-services.yaml.
INTEG_FLAGS=()
read -r -a INTEG_FLAGS <<<"${GO_INTEG_FLAGS:-}"

# Extra flags for the plain go test stage only (e.g. -race), same splitting.
# Go CI sets -race; agents leave it unset.
TEST_FLAGS=()
read -r -a TEST_FLAGS <<<"${GO_TEST_FLAGS:-}"

for m in "${MODULES[@]}"; do
  if [ ! -f "$m/go.mod" ]; then
    echo "FAIL not-a-module  $m"
    VERIFY_FAILED=$((VERIFY_FAILED + 1))
    continue
  fi
  name="${m##*/}"
  run_stage build "$name" "$m" go build ./...

  # With --db, the plain test stage runs with the module's DSN variables
  # unset, so database suites without the integration tag run once, in the
  # integration stage (-p 1), not in parallel packages here as well.
  mapfile -t MOD_DSN_VARS < <(dsn_vars "$m")
  UNSET_DSN=()
  if [ -n "$DSN" ]; then
    for var in "${MOD_DSN_VARS[@]}"; do [ -n "$var" ] && UNSET_DSN+=(-u "$var"); done
  fi

  if [ "$MODE" = fast ]; then
    mapfile -t PKGS < <(changed_packages "$m")
    if [ ${#PKGS[@]} -eq 0 ]; then
      echo "SKIP vet/test       $name  (no changed package)"
    else
      run_stage vet "$name" "$m" go vet "${PKGS[@]}"
      run_stage vet-integ "$name" "$m" go vet -tags integration "${PKGS[@]}"
      run_stage test "$name" "$m" env "${UNSET_DSN[@]}" go test -count=1 "${TEST_FLAGS[@]}" "${PKGS[@]}"
    fi
  else
    PKGS=(./...)
    # A module whose every package carries the integration tag
    # (Architecture/tools/integration) has nothing to vet or test without
    # it, and go exits 1 on "no packages to vet". Its vet-integ stage and,
    # with --db, its integration stage still run.
    HAS_PLAIN="$(cd "$m" && go list -e ./... 2>/dev/null)"
    if [ -z "$HAS_PLAIN" ]; then
      echo "SKIP vet/test       $name  (every package needs -tags integration)"
    else
      run_stage vet "$name" "$m" go vet ./...
    fi
    run_stage vet-integ "$name" "$m" go vet -tags integration ./...
    if [ -n "$HAS_PLAIN" ]; then
      run_stage test "$name" "$m" env "${UNSET_DSN[@]}" go test -count=1 "${TEST_FLAGS[@]}" ./...
    fi
  fi

  if [ -n "$DSN" ] && [ ${#PKGS[@]} -gt 0 ]; then
    refused=0
    while read -r var; do
      [ -n "$var" ] || continue
      if [ -z "${!var:-}" ]; then
        export "$var=$DSN"
      elif ! require_test_db "${!var}"; then
        echo "  ($var)" >&2
        refused=1
      fi
    done < <(printf '%s\n' "${MOD_DSN_VARS[@]}")
    if [ $refused -eq 1 ]; then
      VERIFY_FAILED=$((VERIFY_FAILED + 1))
      echo "FAIL integration    $name  (a preset DSN is not a _test database)"
    else
      run_stage integration "$name" "$m" go test -tags integration -p 1 -count=1 "${INTEG_FLAGS[@]}" "${PKGS[@]}"
    fi
  fi
done

verify_result
