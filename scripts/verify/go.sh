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

# dsn_vars <module>: the DSN variables the module's tests read.
dsn_vars() {
  grep -rhoE --include='*_test.go' 'os\.Getenv\("[A-Z0-9_]*(DSN|DATABASE_URL)[A-Z0-9_]*"\)' "$1" 2>/dev/null |
    sed -E 's/.*"([A-Z0-9_]+)".*/\1/' | sort -u
}

for m in "${MODULES[@]}"; do
  if [ ! -f "$m/go.mod" ]; then
    echo "FAIL not-a-module  $m"
    VERIFY_FAILED=$((VERIFY_FAILED + 1))
    continue
  fi
  name="${m##*/}"
  run_stage build "$name" "$m" go build ./...

  if [ "$MODE" = fast ]; then
    mapfile -t PKGS < <(changed_packages "$m")
    if [ ${#PKGS[@]} -eq 0 ]; then
      echo "SKIP vet/test       $name  (no changed package)"
    else
      run_stage vet "$name" "$m" go vet "${PKGS[@]}"
      run_stage vet-integ "$name" "$m" go vet -tags integration "${PKGS[@]}"
      run_stage test "$name" "$m" go test -count=1 "${PKGS[@]}"
    fi
  else
    PKGS=(./...)
    run_stage vet "$name" "$m" go vet ./...
    run_stage vet-integ "$name" "$m" go vet -tags integration ./...
    run_stage test "$name" "$m" go test -count=1 ./...
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
    done < <(dsn_vars "$m")
    if [ $refused -eq 1 ]; then
      VERIFY_FAILED=$((VERIFY_FAILED + 1))
      echo "FAIL integration    $name  (a preset DSN is not a _test database)"
    else
      run_stage integration "$name" "$m" go test -tags integration -p 1 -count=1 "${PKGS[@]}"
    fi
  fi
done

verify_result
