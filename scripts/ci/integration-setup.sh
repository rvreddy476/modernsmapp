#!/usr/bin/env bash
# Prepare one module's integration databases on the CI postgres service.
#
#   scripts/ci/integration-setup.sh [--dry-run] <module-dir>
#
# Reads the module's entry in scripts/ci/integration-services.yaml, then for
# its db and each extra_dbs item:
#   1. drops and creates the database (every name must end in _test);
#   2. applies the schema files in order with psql -v ON_ERROR_STOP=1;
#   3. with record_migrations, records each applied database/migrations/
#      file in schema_migrations so a later BootstrapSchema does not replay it.
# Then appends to $GITHUB_ENV (never to the log):
#   INTEG_DSN=<the module db's DSN>          (what go.sh --db is given)
#   <each DSN var>=<its database's DSN>      (go.sh keeps preset vars)
#   <each env entry>, GO_INTEG_FLAGS=<go_flags>
# The log shows variable and database names only, never a DSN or password.
#
# Connection: PGHOST (localhost or 127.0.0.1 only), PGPORT (5432), PGUSER,
# PGPASSWORD, as the workflow's job env sets them for the postgres service.
#
# --dry-run checks the entry and prints the plan without touching a database
# or $GITHUB_ENV; integration-manifest-check.sh runs it for every entry.

set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
source "$HERE/integration-manifest-lib.sh"

DRY=0
MODULE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY=1 ;;
    -h | --help) sed -n '2,23p' "$0"; exit 0 ;;
    -*) echo "unknown flag: $1" >&2; exit 2 ;;
    *) MODULE="${1%/}" ;;
  esac
  shift
done
[ -n "$MODULE" ] || { echo "usage: $0 [--dry-run] <module-dir>" >&2; exit 2; }

fail() { echo "integration-setup: $*" >&2; exit 1; }

mf_load || fail "could not read the manifest"
IDX="$(mf_index "$MODULE")"
[ -n "$IDX" ] || fail "module '$MODULE' is not in scripts/ci/integration-services.yaml"

# Required fields.
for key in db wave needs vars; do
  mf_require "$IDX" "$key" >/dev/null || exit 1
done
[ -f "$MF_ROOT/$MODULE/go.mod" ] || fail "$MODULE has no go.mod"

# Databases in order: the module's db, then each extra. Parallel arrays:
# name, the prefix its keys live under, and its DSN vars.
DB_NAMES=()
DB_PREFIX=()
DB_NAMES+=("$(mf_get "$IDX" db)")
DB_PREFIX+=("")
for x in $(mf_extras "$IDX"); do
  name="$(mf_get "$IDX" "extra.$x.db")"
  mf_get "$IDX" "extra.$x.vars" | grep -q . || fail "extra_dbs item '$name' has no vars"
  DB_NAMES+=("$name")
  DB_PREFIX+=("extra.$x.")
done

seen_db=" "
for name in "${DB_NAMES[@]}"; do
  mf_db_ok "$name" || fail "database '$name' must be lower-case and end in _test"
  case "$seen_db" in *" $name "*) fail "database '$name' is listed twice" ;; esac
  seen_db="$seen_db$name "
done

seen_var=" "
for i in "${!DB_NAMES[@]}"; do
  while IFS= read -r var; do
    [ -n "$var" ] || continue
    [[ "$var" =~ ^[A-Z][A-Z0-9_]*$ ]] || fail "bad variable name '$var'"
    case "$seen_var" in *" $var "*) fail "$var is assigned to two databases" ;; esac
    seen_var="$seen_var$var "
  done < <(mf_get "$IDX" "${DB_PREFIX[$i]}vars")
done

# Expand every schema entry now, so a missing file fails before any database
# is touched.
declare -A FILES
for i in "${!DB_NAMES[@]}"; do
  list=""
  while IFS= read -r pat; do
    [ -n "$pat" ] || continue
    matches="$(mf_expand "$pat")"
    [ -n "$matches" ] || fail "schema entry '$pat' (database ${DB_NAMES[$i]}) matches no file"
    while IFS= read -r f; do
      case "$f" in *.sql) ;; *) fail "schema file '$f' is not .sql" ;; esac
      list="$list$f"$'\n'
    done <<<"$matches"
  done < <(mf_get "$IDX" "${DB_PREFIX[$i]}schema")
  FILES[$i]="$list"
done

if [ "$DRY" -eq 0 ]; then
  [ -n "${GITHUB_ENV:-}" ] || fail "GITHUB_ENV is not set; this script runs in GitHub Actions (use --dry-run locally)"
  case "${PGHOST:-}" in
    localhost | 127.0.0.1) ;;
    *) fail "PGHOST must be the CI postgres service (localhost or 127.0.0.1)" ;;
  esac
  [ -n "${PGUSER:-}" ] && [ -n "${PGPASSWORD:-}" ] || fail "PGUSER and PGPASSWORD must be set"
  command -v psql >/dev/null || fail "psql is not installed"
fi
PORT="${PGPORT:-5432}"

psql_db() { psql -X -q -v ON_ERROR_STOP=1 -d "$1" "${@:2}"; }

dsn_for() {
  printf 'postgres://%s:%s@%s:%s/%s?sslmode=disable' "$PGUSER" "$PGPASSWORD" "$PGHOST" "$PORT" "$1"
}

for i in "${!DB_NAMES[@]}"; do
  name="${DB_NAMES[$i]}"
  prefix="${DB_PREFIX[$i]}"
  echo "database $name"
  if [ "$DRY" -eq 1 ]; then
    echo "  would drop and create $name"
  else
    psql_db postgres -c "DROP DATABASE IF EXISTS \"$name\" WITH (FORCE)" -c "CREATE DATABASE \"$name\"" ||
      fail "could not create $name"
  fi

  applied=()
  while IFS= read -r f; do
    [ -n "$f" ] || continue
    if [ "$DRY" -eq 1 ]; then
      echo "  would apply $f"
    else
      echo "  apply $f"
      psql_db "$name" -f "$MF_ROOT/$f" || fail "applying $f to $name failed"
    fi
    applied+=("$f")
  done <<<"${FILES[$i]}"

  svc="$(mf_get "$IDX" "${prefix}record_migrations")"
  if [ -n "$svc" ]; then
    [[ "$svc" =~ ^[a-z][a-z0-9-]*$ ]] || fail "record_migrations '$svc' is not a service name"
    sql="CREATE TABLE IF NOT EXISTS schema_migrations (
  service TEXT NOT NULL, filename TEXT NOT NULL,
  applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), PRIMARY KEY (service, filename));"
    n=0
    for f in "${applied[@]}"; do
      case "$f" in */database/migrations/*.sql) ;; *) continue ;; esac
      base="${f##*/}"
      [[ "$base" =~ ^[A-Za-z0-9_.-]+$ ]] || fail "migration file name '$base' has unexpected characters"
      sql="$sql
INSERT INTO schema_migrations (service, filename) VALUES ('$svc', '$base') ON CONFLICT DO NOTHING;"
      n=$((n + 1))
    done
    [ "$n" -gt 0 ] || fail "record_migrations '$svc' set but no database/migrations/ file was applied to $name"
    if [ "$DRY" -eq 1 ]; then
      echo "  would record $n migration(s) as $svc"
    else
      printf '%s\n' "$sql" | psql_db "$name" -f - || fail "recording migrations in $name failed"
      echo "  recorded $n migration(s) as $svc"
    fi
  fi

  while IFS= read -r var; do
    [ -n "$var" ] || continue
    if [ "$DRY" -eq 1 ]; then
      echo "  would set $var -> $name"
    else
      printf '%s=%s\n' "$var" "$(dsn_for "$name")" >>"$GITHUB_ENV"
      echo "  set $var -> $name"
    fi
  done < <(mf_get "$IDX" "${prefix}vars")
done

main_db="${DB_NAMES[0]}"
if [ "$DRY" -eq 1 ]; then
  echo "would set INTEG_DSN -> $main_db"
else
  printf 'INTEG_DSN=%s\n' "$(dsn_for "$main_db")" >>"$GITHUB_ENV"
  echo "set INTEG_DSN -> $main_db"
fi

while IFS= read -r kv; do
  [ -n "$kv" ] || continue
  key="${kv%%=*}"
  val="${kv#*=}"
  val="${val//\{repo\}/$MF_ROOT}"
  case "$val" in *$'\n'*) fail "env $key has a newline" ;; esac
  if [ "$DRY" -eq 1 ]; then
    echo "would set $key"
  else
    printf '%s=%s\n' "$key" "$val" >>"$GITHUB_ENV"
    echo "set $key"
  fi
done < <(mf_get "$IDX" env)

flags="$(mf_get "$IDX" go_flags)"
if [ -n "$flags" ]; then
  if [ "$DRY" -eq 1 ]; then
    echo "would set GO_INTEG_FLAGS: $flags"
  else
    printf 'GO_INTEG_FLAGS=%s\n' "$flags" >>"$GITHUB_ENV"
    echo "set GO_INTEG_FLAGS: $flags"
  fi
fi
echo "integration-setup: $MODULE ready"
