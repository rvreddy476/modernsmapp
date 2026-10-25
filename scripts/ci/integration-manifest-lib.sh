#!/usr/bin/env bash
# Reader for scripts/ci/integration-services.yaml, sourced by the
# integration-*.sh scripts. No yq: the manifest is a fixed YAML subset (see
# its header) and this awk reader refuses anything outside it, so a typo
# fails loudly instead of being read as something else.
#
#   mf_load [file]          parse once into MF_FLAT; returns 2 on a parse error
#   mf_modules              every manifest module, in file order
#   mf_index <module>       its entry index (empty when absent)
#   mf_get <idx> <key>      the key's values, one per line (lists give several;
#                           env gives K=V; extra_dbs give extra.<n>.<key>)
#   mf_require <idx> <key>  like mf_get, but fails with a message when empty
#   mf_extras <idx>         the extra_dbs indexes of an entry
#   mf_excluded             excluded modules, "module<TAB>reason"
#
# MF_FLAT holds one record per line: section<TAB>index<TAB>key<TAB>value.

MF_ROOT="${MF_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
MF_FILE_DEFAULT="$MF_ROOT/scripts/ci/integration-services.yaml"
MF_FLAT=""

# Keys an entry may carry, and the ones an extra_dbs item may carry.
MF_KEYS=" module db wave needs optional pg_image vars schema record_migrations extra_dbs env go_flags reason "
MF_EXTRA_KEYS=" db vars schema record_migrations "

MF_AWK='
function trim(s) { sub(/^[ \t]+/, "", s); sub(/[ \t]+$/, "", s); return s }
function unq(s) {
  s = trim(s)
  if (s ~ /^".*"$/) s = substr(s, 2, length(s) - 2)
  return s
}
function die(msg) { printf "%s:%d: %s\n", FILENAME, FNR, msg > "/dev/stderr"; bad = 1; exit 2 }
function emit(key, val) { printf "%s\t%d\t%s\t%s\n", sec, idx, key, val }
function emit_value(key, v,   n, a, i, x) {
  if (v ~ /^\[.*\]$/) {
    v = substr(v, 2, length(v) - 2)
    n = split(v, a, ",")
    for (i = 1; i <= n; i++) { x = unq(a[i]); if (x != "") emit(key, x) }
  } else if (v != "") {
    emit(key, unq(v))
  }
}
function split_kv(body) {
  if (body !~ /^[A-Za-z_][A-Za-z0-9_]*:( |$)/) die("expected \"key: value\", got: " body)
  K = body; sub(/:.*/, "", K)
  V = body; sub(/^[^:]*:/, "", V); V = trim(V)
}
function check_key(k, allowed) { if (index(allowed, " " k " ") == 0) die("unknown key: " k) }
{
  line = $0
  sub(/\r$/, "", line)
  if (line ~ /^[ \t]*(#.*)?$/) next
  if (line ~ /^ *\t/) die("tab in indentation")
  sub(/[ \t]+#.*$/, "", line)
  match(line, /^ */); ind = RLENGTH; body = substr(line, ind + 1)
  if (ind % 2) die("odd indentation")

  if (ind == 0) {
    if (body == "modules:" || body == "exclude:") { sec = substr(body, 1, length(body) - 1); idx = -1; parent = ""; next }
    die("unknown top-level key: " body)
  }
  if (sec == "") die("content before modules: or exclude:")

  if (ind == 2) {
    if (body !~ /^- /) die("an entry starts with \"- module: ...\"")
    body = substr(body, 3)
    split_kv(body)
    if (K != "module") die("an entry starts with \"- module: ...\"")
    idx++; extra = -1; parent = ""
    ind = 4
  }
  if (ind == 4) {
    split_kv(body)
    check_key(K, keys)
    parent = K
    emit_value(K, V)
    next
  }
  if (ind == 6) {
    if (parent == "") die("nested value without a parent key")
    if (parent == "extra_dbs") {
      if (body !~ /^- /) die("an extra_dbs item starts with \"- db: ...\"")
      split_kv(substr(body, 3))
      if (K != "db") die("an extra_dbs item starts with \"- db: ...\"")
      extra++; xparent = K
      emit_value("extra." extra "." K, V)
      next
    }
    if (parent == "env") {
      split_kv(body)
      if (K !~ /^[A-Z][A-Z0-9_]*$/) die("env names are UPPER_CASE: " K)
      emit("env", K "=" unq(V))
      next
    }
    if (body ~ /^- /) { emit(parent, unq(substr(body, 3))); next }
    die("expected \"- value\" under " parent)
  }
  if (ind == 8) {
    if (parent != "extra_dbs" || extra < 0) die("indent 8 is only used inside extra_dbs")
    split_kv(body)
    check_key(K, xkeys)
    xparent = K
    emit_value("extra." extra "." K, V)
    next
  }
  if (ind == 10) {
    if (parent != "extra_dbs" || extra < 0 || body !~ /^- /) die("indent 10 is only used for lists inside extra_dbs")
    emit("extra." extra "." xparent, unq(substr(body, 3)))
    next
  }
  die("unexpected indentation (" ind " spaces)")
}
END { if (bad) exit 2 }
'

mf_load() {
  local file="${1:-${INTEG_MANIFEST:-$MF_FILE_DEFAULT}}" out
  if [ ! -f "$file" ]; then
    echo "manifest not found: $file" >&2
    return 2
  fi
  out="$(awk -v keys="$MF_KEYS" -v xkeys="$MF_EXTRA_KEYS" "$MF_AWK" "$file")" || return 2
  MF_FLAT="$out"
}

mf_modules() {
  awk -F'\t' '$1 == "modules" && $3 == "module" { print $4 }' <<<"$MF_FLAT"
}

mf_index() {
  awk -F'\t' -v m="$1" '$1 == "modules" && $3 == "module" && $4 == m { print $2; exit }' <<<"$MF_FLAT"
}

mf_get() {
  awk -F'\t' -v i="$1" -v k="$2" '$1 == "modules" && $2 == i && $3 == k { print $4 }' <<<"$MF_FLAT"
}

mf_require() {
  local v
  v="$(mf_get "$1" "$2")"
  if [ -z "$v" ]; then
    echo "manifest: entry '$(mf_get "$1" module)' has no '$2'" >&2
    return 1
  fi
  printf '%s\n' "$v"
}

mf_extras() {
  awk -F'\t' -v i="$1" '$1 == "modules" && $2 == i && $3 ~ /^extra\.[0-9]+\.db$/ {
    split($3, a, "."); print a[2] }' <<<"$MF_FLAT"
}

mf_excluded() {
  awk -F'\t' '$1 == "exclude" && $3 == "module" { m[$2] = $4; order[++n] = $2 }
              $1 == "exclude" && $3 == "reason" { r[$2] = $4 }
              END { for (i = 1; i <= n; i++) printf "%s\t%s\n", m[order[i]], r[order[i]] }' <<<"$MF_FLAT"
}

# mf_db_ok <name>: a scratch database name: lower-case, ends in _test.
mf_db_ok() {
  [[ "$1" =~ ^[a-z][a-z0-9_]*_test$ ]]
}

# mf_expand <pattern>: repo-relative files matching a path or glob, sorted
# byte-wise (the order a deploy applies migrations in). Prints nothing when
# nothing matches.
mf_expand() {
  (cd "$MF_ROOT" && compgen -G "$1" | LC_ALL=C sort)
}
