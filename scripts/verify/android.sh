#!/usr/bin/env bash
# The one Android check every agent and the lead run (harness doc, changes 4
# and 5).
#
#   scripts/verify/android.sh [--fast|--full] [--base REV] [:module...]
#
# Modules default to every Gradle module with a changed file under
# mobile/android (working tree, plus everything since --base). A change to
# build-logic, gradle/ or a root build file means every module.
#
#   --fast  while working: the unit tests of the touched modules only.
#   --full  once, before reporting (default): moduleGraphCheck, the unit
#           tests of the touched modules, the unit-test sources of every
#           module that depends on them compiled (a broken fake or a renamed
#           API shows here), :app:compileDevDebugKotlin, and detekt.
#
# Always --offline (ANDROID_VERIFY_ONLINE=1 to drop it). Runs in the tree
# the script sits in, so a second worktree checks itself. One run per tree
# at a time: a second run waits for the lock rather than sharing Gradle.

source "$(dirname "$0")/lib.sh"

MODE=full
BASE=""
MODULES=()
while [ $# -gt 0 ]; do
  case "$1" in
    --fast) MODE=fast ;;
    --full) MODE=full ;;
    --base) BASE="$2"; shift ;;
    -h | --help) sed -n '2,20p' "$0"; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; exit 2 ;;
    :*) MODULES+=("$1") ;;
    *) echo "modules are Gradle paths, e.g. :core:model" >&2; exit 2 ;;
  esac
  shift
done

ANDROID_DIR="$VERIFY_ROOT/mobile/android"
cd "$ANDROID_DIR" || exit 2
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) GRADLE=./gradlew.bat ;; *) GRADLE=./gradlew ;; esac
GRADLE_FLAGS=(-q --continue)
[ "${ANDROID_VERIFY_ONLINE:-0}" = 1 ] || GRADLE_FLAGS+=(--offline)

# One run per tree: Gradle in one project dir does not share well.
LOCK="$ANDROID_DIR/build/.verify-android.lock"
mkdir -p "$ANDROID_DIR/build"
waited=0
until mkdir "$LOCK" 2>/dev/null; do
  if [ $waited -eq 0 ]; then echo "waiting for another android verify in this tree ($LOCK)"; fi
  waited=1
  sleep 5
done
trap 'rmdir "$LOCK" 2>/dev/null' EXIT

# Every module: Gradle path and its directory (":core:commerce-ui" -> core/commerce-ui).
mapfile -t ALL < <(grep -oE 'include\("[^"]+"\)' settings.gradle.kts | sed -E 's/include\("(.*)"\)/\1/')
dir_of() { local p="${1#:}"; printf '%s' "${p//://}"; }

# kind_of <module>: app | android | jvm, from its convention plugin.
kind_of() {
  local f
  f="$(dir_of "$1")/build.gradle.kts"
  if grep -q 'us.android.application' "$f"; then echo app
  elif grep -q 'us.jvm.library' "$f"; then echo jvm
  else echo android; fi
}
test_task() {
  case "$(kind_of "$1")" in
    app) echo "$1:testDevDebugUnitTest" ;;
    jvm) echo "$1:test" ;;
    *) echo "$1:testDebugUnitTest" ;;
  esac
}
test_compile_task() {
  case "$(kind_of "$1")" in
    app) echo "$1:compileDevDebugUnitTestKotlin" ;;
    jvm) echo "$1:compileTestKotlin" ;;
    *) echo "$1:compileDebugUnitTestKotlin" ;;
  esac
}
# accessor_of <module>: the typesafe accessor (":core:commerce-ui" -> projects.core.commerceUi).
accessor_of() {
  local p="${1#:}" out="projects" seg
  IFS=: read -ra segs <<<"$p"
  for seg in "${segs[@]}"; do
    out="$out.$(printf '%s' "$seg" | sed -E 's/-(.)/\U\1/g')"
  done
  printf '%s' "$out"
}

module_of_path() {
  local rel="$1" best="" m d
  for m in "${ALL[@]}"; do
    d="$(dir_of "$m")"
    case "$rel" in "$d"/*) [ ${#d} -gt ${#best} ] && best="$d" && bestm="$m" ;; esac
  done
  [ -n "$best" ] && printf '%s' "$bestm"
}

EVERYTHING=0
if [ ${#MODULES[@]} -eq 0 ]; then
  while read -r f; do
    case "$f" in mobile/android/*) ;; *) continue ;; esac
    rel="${f#mobile/android/}"
    case "$rel" in
      build-logic/* | gradle/* | build.gradle.kts | settings.gradle.kts | gradle.properties)
        EVERYTHING=1 ;;
      test-fixtures/*)
        fx="${rel#test-fixtures/}"; fx="test-fixtures/${fx%%/*}"
        for m in "${ALL[@]}"; do
          grep -q "$fx" "$(dir_of "$m")/build.gradle.kts" && MODULES+=("$m")
        done ;;
      *)
        bestm=""
        m="$(module_of_path "$rel")"
        [ -n "$m" ] && MODULES+=("$m") ;;
    esac
  done < <(changed_files "$BASE")
  mapfile -t MODULES < <(printf '%s\n' "${MODULES[@]}" | sed '/^$/d' | sort -u)
fi
if [ $EVERYTHING -eq 1 ]; then
  mapfile -t MODULES < <(printf '%s\n' "${ALL[@]}")
  echo "build setup changed: checking every module"
fi
if [ ${#MODULES[@]} -eq 0 ]; then
  echo "No changed Android module. Name one: scripts/verify/android.sh --full :core:model"
  exit 0
fi
echo "modules: ${MODULES[*]}"

TESTS=()
for m in "${MODULES[@]}"; do TESTS+=("$(test_task "$m")"); done

if [ "$MODE" = fast ]; then
  run_stage unit-tests "${#MODULES[@]}-modules" "$ANDROID_DIR" "$GRADLE" "${GRADLE_FLAGS[@]}" "${TESTS[@]}"
  verify_result
  exit $?
fi

# Transitive dependents of the touched modules (excluding the touched ones).
declare -A SEEN=()
for m in "${MODULES[@]}"; do SEEN[$m]=1; done
QUEUE=("${MODULES[@]}")
DEPENDENTS=()
while [ ${#QUEUE[@]} -gt 0 ]; do
  cur="${QUEUE[0]}"; QUEUE=("${QUEUE[@]:1}")
  acc="$(accessor_of "$cur")"
  for m in "${ALL[@]}"; do
    [ -n "${SEEN[$m]:-}" ] && continue
    if grep -qE "${acc//./\\.}([^A-Za-z0-9]|$)" "$(dir_of "$m")/build.gradle.kts"; then
      SEEN[$m]=1; DEPENDENTS+=("$m"); QUEUE+=("$m")
    fi
  done
done

run_stage graph all "$ANDROID_DIR" "$GRADLE" "${GRADLE_FLAGS[@]}" moduleGraphCheck
run_stage unit-tests "${#MODULES[@]}-modules" "$ANDROID_DIR" "$GRADLE" "${GRADLE_FLAGS[@]}" "${TESTS[@]}"
if [ ${#DEPENDENTS[@]} -gt 0 ]; then
  COMPILES=()
  for m in "${DEPENDENTS[@]}"; do COMPILES+=("$(test_compile_task "$m")"); done
  run_stage dependents "${#DEPENDENTS[@]}-modules" "$ANDROID_DIR" "$GRADLE" "${GRADLE_FLAGS[@]}" "${COMPILES[@]}"
fi
run_stage app-compile :app "$ANDROID_DIR" "$GRADLE" "${GRADLE_FLAGS[@]}" :app:compileDevDebugKotlin
run_stage detekt all "$ANDROID_DIR" "$GRADLE" "${GRADLE_FLAGS[@]}" detekt
verify_result
