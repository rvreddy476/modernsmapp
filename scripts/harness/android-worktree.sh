#!/usr/bin/env bash
# Second Android lane: a worktree of the current commit, with the gitignored
# build inputs copied in, and a claim on the modules the lane may touch.
#
#   scripts/harness/android-worktree.sh claim  <lane> <:module...>   # lane in THIS tree, no worktree
#   scripts/harness/android-worktree.sh create <lane> <:module...>   # new worktree + branch + claim
#   scripts/harness/android-worktree.sh list
#   scripts/harness/android-worktree.sh release <lane>               # drop the claim (keeps the worktree)
#   scripts/harness/android-worktree.sh remove  <lane>               # drop the claim and the worktree
#
# Modules are Gradle paths (:feature:seller). ':' claims the whole Android
# tree (build-logic, gradle/, root build files) and overlaps every lane. A
# claim is refused when any of its modules equals, contains or sits inside a
# module another lane holds, so two lanes never edit the same module.
#
# Copied into a new worktree, never committed (all gitignored):
#   mobile/android/local.properties, mobile/android/*/google-services.json.
# Nothing is printed from them. The GIF key is not copied: an APK build in
# the worktree exports CHAT_GIF_API_KEY_ANDROID from the main tree's
# Architecture/docker/.env, as in the main tree.
#
# Agents do not run this script (it writes git state); the lead does.

set -u
ROOT="$(git rev-parse --show-toplevel)" || exit 2
COMMON="$(cd "$ROOT" && cd "$(git rev-parse --git-common-dir)" && pwd)"
CLAIMS="$COMMON/harness-android-lanes"
WT_BASE="${ANDROID_WORKTREE_BASE:-$(dirname "$(dirname "$COMMON")")/wt}"
mkdir -p "$CLAIMS"

usage() { sed -n '2,22p' "$0"; exit 2; }
valid_lane() { case "$1" in '' | *[!a-z0-9-]*) return 1 ;; esac; }

# overlaps <a> <b>: same module, or one is a path prefix of the other.
overlaps() {
  local a="$1" b="$2"
  [ "$a" = ":" ] || [ "$b" = ":" ] && return 0
  [ "$a" = "$b" ] && return 0
  case "$a" in "$b":*) return 0 ;; esac
  case "$b" in "$a":*) return 0 ;; esac
  return 1
}

# check_free <lane> <modules...>: refuses a claim that overlaps another lane.
check_free() {
  local lane="$1" f other m held bad=0
  shift
  for f in "$CLAIMS"/*; do
    [ -f "$f" ] || continue
    other="$(basename "$f")"
    [ "$other" = "$lane" ] && continue
    while IFS= read -r held; do
      case "$held" in '' | tree=* | branch=*) continue ;; esac
      for m in "$@"; do
        if overlaps "$m" "$held"; then
          echo "REFUSED: $m overlaps $held, held by lane '$other'"
          bad=1
        fi
      done
    done <"$f"
  done
  return $bad
}

validate_modules() {
  local m settings="$ROOT/mobile/android/settings.gradle.kts"
  for m in "$@"; do
    case "$m" in
      :) continue ;;
      :*) ;;
      *) echo "REFUSED: '$m' is not a Gradle path like :feature:seller"; return 1 ;;
    esac
    if ! grep -q "\"$m\"" "$settings"; then
      echo "REFUSED: $m is not included in mobile/android/settings.gradle.kts"
      return 1
    fi
  done
}

write_claim() {
  local lane="$1" tree="$2" branch="$3"
  shift 3
  { echo "tree=$tree"; echo "branch=$branch"; printf '%s\n' "$@"; } >"$CLAIMS/$lane"
}

field_of() { sed -n "s/^$2=//p" "$CLAIMS/$1"; }

cmd="${1:-}"
[ -n "$cmd" ] || usage
shift

case "$cmd" in
  claim | create)
    lane="${1:-}"
    shift || true
    valid_lane "$lane" || { echo "REFUSED: lane name must be lowercase letters, digits and '-'"; exit 2; }
    [ $# -gt 0 ] || { echo "REFUSED: name the modules the lane may touch"; exit 2; }
    [ -e "$CLAIMS/$lane" ] && { echo "REFUSED: lane '$lane' already exists (release it first)"; exit 2; }
    validate_modules "$@" || exit 2
    check_free "$lane" "$@" || exit 3
    if [ "$cmd" = claim ]; then
      # One Android agent per tree: a tree holds at most one lane.
      for f in "$CLAIMS"/*; do
        [ -f "$f" ] && [ "$(field_of "$(basename "$f")" tree)" = "$ROOT" ] &&
          { echo "REFUSED: this tree already runs lane '$(basename "$f")'"; exit 3; }
      done
      write_claim "$lane" "$ROOT" "$(git -C "$ROOT" rev-parse --abbrev-ref HEAD)" "$@"
      echo "CLAIMED $lane in $ROOT: $*"
      exit 0
    fi
    tree="$WT_BASE/android-$lane"
    branch="harness/android-$lane"
    [ -e "$tree" ] && { echo "REFUSED: $tree already exists"; exit 2; }
    git -C "$ROOT" show-ref --verify --quiet "refs/heads/$branch" &&
      { echo "REFUSED: branch $branch already exists"; exit 2; }
    git -C "$ROOT" worktree add -q -b "$branch" "$tree" HEAD || exit 1
    copied=0
    for src in "$ROOT"/mobile/android/local.properties "$ROOT"/mobile/android/*/google-services.json; do
      [ -f "$src" ] || continue
      rel="${src#"$ROOT"/}"
      mkdir -p "$(dirname "$tree/$rel")"
      cp "$src" "$tree/$rel"
      if ! git -C "$tree" check-ignore -q "$rel"; then
        rm -f "$tree/$rel"
        echo "STOP: $rel is not gitignored in the new tree; removed it"
        exit 4
      fi
      copied=$((copied + 1))
    done
    tree="$(git -C "$tree" rev-parse --show-toplevel)"
    write_claim "$lane" "$tree" "$branch" "$@"
    echo "CREATED $lane: $tree on $branch from $(git -C "$ROOT" rev-parse --short HEAD)"
    echo "  modules: $*"
    echo "  copied $copied gitignored build input(s)"
    echo "  verify there with: bash scripts/verify/android.sh --fast $*"
    ;;
  list)
    found=0
    for f in "$CLAIMS"/*; do
      [ -f "$f" ] || continue
      found=1
      lane="$(basename "$f")"
      printf '%-16s %s  [%s]\n' "$lane" "$(field_of "$lane" tree)" "$(field_of "$lane" branch)"
      grep -v -e '^tree=' -e '^branch=' "$f" | sed 's/^/    /'
    done
    [ $found -eq 1 ] || echo "no Android lanes claimed"
    ;;
  release | remove)
    lane="${1:-}"
    [ -f "$CLAIMS/$lane" ] || { echo "no lane '$lane'"; exit 2; }
    tree="$(field_of "$lane" tree)"
    if [ "$cmd" = remove ]; then
      [ "$tree" = "$ROOT" ] && { echo "REFUSED: lane '$lane' is this tree; use release"; exit 2; }
      if [ -n "$(git -C "$tree" status --porcelain 2>/dev/null)" ]; then
        echo "REFUSED: $tree has uncommitted changes; commit or discard them first"
        exit 2
      fi
      git -C "$ROOT" worktree remove "$tree" || exit 1
      echo "REMOVED worktree $tree (branch $(field_of "$lane" branch) kept)"
    fi
    rm -f "$CLAIMS/$lane"
    echo "RELEASED $lane"
    ;;
  *) usage ;;
esac
