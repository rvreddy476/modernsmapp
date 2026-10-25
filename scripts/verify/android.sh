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
  holder="$(cat "$LOCK/pid" 2>/dev/null)"
  # A run killed from outside (timeout, closed shell) leaves its lock: take
  # it over once its process is gone, or a lock with no pid after a minute.
  if { [ -n "$holder" ] && ! kill -0 "$holder" 2>/dev/null; } ||
     { [ -z "$holder" ] && [ $waited -ge 12 ]; }; then
    echo "removing a stale android verify lock (pid ${holder:-unknown} is gone)"
    rm -rf "$LOCK"
    continue
  fi
  if [ $waited -eq 0 ]; then echo "waiting for another android verify in this tree (pid ${holder:-?}, $LOCK)"; fi
  waited=$((waited + 1))
  sleep 5
done
echo $$ >"$LOCK/pid"
trap 'rm -rf "$LOCK"' EXIT
trap 'exit 130' INT TERM

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

# Directories computed once: a subshell per module per changed file took
# over a minute on Windows.
declare -A DIR=()
for m in "${ALL[@]}"; do p="${m#:}"; DIR[$m]="${p//://}"; done

# module_of_path <rel>: sets HIT to the deepest module containing rel, or "".
module_of_path() {
  local rel="$1" best="" m d
  HIT=""
  for m in "${ALL[@]}"; do
    d="${DIR[$m]}"
    case "$rel" in "$d"/*) [ ${#d} -gt ${#best} ] && best="$d" && HIT="$m" ;; esac
  done
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
          grep -q "$fx" "${DIR[$m]}/build.gradle.kts" && MODULES+=("$m")
        done ;;
      *)
        module_of_path "$rel"
        [ -n "$HIT" ] && MODULES+=("$HIT") ;;
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

# Lane scope: when scripts/harness/android-worktree.sh gave this tree a lane,
# every touched module must sit inside the lane's claim.
CLAIMS_DIR="$(cd "$VERIFY_ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)/harness-android-lanes"
for claim in "$CLAIMS_DIR"/*; do
  [ -f "$claim" ] || continue
  [ "$(sed -n 's/^tree=//p' "$claim")" = "$VERIFY_ROOT" ] || continue
  lane="$(basename "$claim")"
  mapfile -t HELD < <(grep -v -e '^tree=' -e '^branch=' -e '^$' "$claim")
  outside=()
  for m in "${MODULES[@]}"; do
    inside=0
    for h in "${HELD[@]}"; do
      case "$m" in "$h" | "$h":*) inside=1 ;; esac
      [ "$h" = ":" ] && inside=1
    done
    [ $inside -eq 1 ] || outside+=("$m")
  done
  if [ $EVERYTHING -eq 1 ] && ! printf '%s\n' "${HELD[@]}" | grep -qx ':'; then
    outside=("build setup")
  fi
  if [ ${#outside[@]} -gt 0 ]; then
    VERIFY_FAILED=$((VERIFY_FAILED + 1))
    echo "FAIL lane-scope     $lane  outside the claim (${HELD[*]}): ${outside[*]}"
  else
    echo "PASS lane-scope     $lane"
  fi
done

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
