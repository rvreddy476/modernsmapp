#!/usr/bin/env bash
# values-tag.sh — read or write ONE image key in a deploy/services values file.
#
#   values-tag.sh get FILE KEY          # prints the value, unquoted, no comment
#   values-tag.sh set FILE KEY VALUE    # rewrites that one line in place
#
# KEY is image.<leaf> or worker.image.<leaf> (leaf: tag, repository, ...).
#
# WHY NOT `sed 's/tag:.*/…/'`: the old bump rewrote EVERY `tag:` line in the
# file, so worker.image.tag was set to the SERVER sha whether or not a worker
# image had been pushed for it (copyright-match-plan P-17). This script is
# structural: it walks the top-level key and the 2-space child key, so
# `image.tag` and `worker.image.tag` are addressed separately.
#
# WHY NOT yq: the runner image ships mikefarah/yq, but yq re-emits the whole
# document (quoting, flow maps, indentation), which turns a one-line bump into a
# noisy diff and defeats review of the ci(deploy) commits. This touches one line
# and keeps the trailing comment.
#
# Portable awk only (Ubuntu runners default to mawk: no match() with an array,
# no gensub). Exit codes: 0 ok, 2 usage, 3 key not found, 4 key found twice.
set -euo pipefail

mode="${1:-}"; file="${2:-}"; key="${3:-}"; value="${4:-}"
case "$mode" in
  get) [[ -n "$file" && -n "$key" ]] || { echo "usage: $0 get FILE KEY" >&2; exit 2; } ;;
  set) [[ -n "$file" && -n "$key" && -n "$value" ]] || { echo "usage: $0 set FILE KEY VALUE" >&2; exit 2; } ;;
  *)   echo "usage: $0 get|set FILE KEY [VALUE]" >&2; exit 2 ;;
esac
[[ -f "$file" ]] || { echo "$0: no such file: $file" >&2; exit 2; }

# Split KEY into (top, child, leaf). image.tag -> (image, "", tag);
# worker.image.tag -> (worker, image, tag).
case "$key" in
  image.*)        top=image;  child="";    leaf="${key#image.}" ;;
  worker.image.*) top=worker; child=image; leaf="${key#worker.image.}" ;;
  *) echo "$0: unsupported key '$key' (image.<leaf> or worker.image.<leaf>)" >&2; exit 2 ;;
esac
case "$leaf" in
  *.*|"") echo "$0: unsupported key '$key'" >&2; exit 2 ;;
esac

# The awk program: track the current top-level key (column 0) and the current
# 2-space child key; the target line is `<indent>leaf:` at the right depth.
# Comments and blank lines change nothing. Lines are checked BEFORE the
# trackers update, so `  tag:` under `image:` is seen with child still unset.
prog='
function is_key(s, indent,   pat) {
  pat = "^" indent "[A-Za-z0-9_.-]+:"
  return (s ~ pat)
}
{
  line = $0
  hit = 0
  if (child == "") {
    if (top_cur == top && is_key(line, "  ") && line ~ ("^  " leaf ":")) hit = 1
  } else {
    if (top_cur == top && child_cur == child && is_key(line, "    ") && line ~ ("^    " leaf ":")) hit = 1
  }
  if (hit) {
    n++
    # prefix = everything up to and including "leaf:" plus following spaces.
    p = index(line, leaf ":") + length(leaf) + 1
    prefix = substr(line, 1, p - 1)
    rest = substr(line, p)
    sub(/^[ \t]+/, "", rest)
    prefix = prefix " "
    # A trailing comment starts at the first " #" of the remainder.
    c = index(rest, " #")
    if (c > 0) { comment = substr(rest, c); val = substr(rest, 1, c - 1) } else { comment = ""; val = rest }
    sub(/[ \t]+$/, "", val)
    if (mode == "get") {
      gsub(/^["'"'"']|["'"'"']$/, "", val)
      print val
    } else {
      print prefix "\"" value "\"" comment
    }
  } else if (mode == "set") {
    print line
  }
  if (is_key(line, "")) {
    top_cur = line; sub(/:.*/, "", top_cur); child_cur = ""
  } else if (is_key(line, "  ")) {
    child_cur = line; sub(/^  /, "", child_cur); sub(/:.*/, "", child_cur)
  }
}
END {
  if (n == 0) { print FILENAME ": key " top (child == "" ? "" : "." child) "." leaf " not found" > "/dev/stderr"; exit 3 }
  if (n > 1)  { print FILENAME ": key " top (child == "" ? "" : "." child) "." leaf " found " n " times" > "/dev/stderr"; exit 4 }
}'

if [[ "$mode" == "get" ]]; then
  awk -v mode=get -v top="$top" -v child="$child" -v leaf="$leaf" "$prog" "$file"
else
  tmp="$(mktemp)"
  trap 'rm -f "$tmp"' EXIT
  awk -v mode=set -v top="$top" -v child="$child" -v leaf="$leaf" -v value="$value" "$prog" "$file" > "$tmp"
  # cat, not mv: keep the file's mode and inode.
  cat "$tmp" > "$file"
fi
