#!/usr/bin/env bash
# seed-keyvault.sh — populate the per-service Key Vault secrets that ESO
# syncs into the cluster. Run AFTER `terraform apply` (so the managed-store
# reference secrets atpost-<env>-{postgres,redis,scylla,redpanda,minio}
# exist) and BEFORE the ApplicationSets sync.
#
# For each service it reads the remoteRefs its values-azure-<env>.yaml
# actually requests and composes one JSON secret named atpost-<env>-<svc>:
#   internal_service_key / jwt_secret / jwt_kid / jwt_*_key_pem
#       → shared values, generated ONCE (must match across services for
#         inter-service auth + token verification) and cached in
#         atpost-<env>-shared so re-runs are idempotent.
#   postgres_dsn → built from the postgres reference secret + the service's
#                  database (svc name with dashes → underscores).
#   redis_addr / redis_password → redis reference secret.
#   kafka_brokers → redpanda reference. scylla_hosts → scylla reference.
#   anything else → empty placeholder + a warning (fill it in yourself).
#
# Requires: az (logged in), openssl, python3. Idempotent.
#
# Usage: scripts/seed-keyvault.sh [staging|prod] [key-vault-name]
set -euo pipefail
cd "$(dirname "$0")/.."

ENV="${1:-staging}"
case "$ENV" in
  staging) KV="${2:-atpost-staging-454350}" ;;
  prod)    KV="${2:-atpost-prod-454350}" ;;
  *) echo "env must be staging|prod" >&2; exit 1 ;;
esac

command -v az >/dev/null || { echo "az CLI not found / not logged in" >&2; exit 1; }
echo "Seeding per-service secrets into Key Vault '$KV' for env '$ENV'…"

ENV="$ENV" KV="$KV" python3 - <<'PY'
import os, json, glob, subprocess, secrets, sys

ENV = os.environ["ENV"]
KV  = os.environ["KV"]

def az_get(name):
    r = subprocess.run(
        ["az", "keyvault", "secret", "show", "--vault-name", KV, "--name", name,
         "--query", "value", "-o", "tsv"],
        capture_output=True, text=True)
    return r.stdout.strip() if r.returncode == 0 else None

def az_set(name, value):
    subprocess.run(
        ["az", "keyvault", "secret", "set", "--vault-name", KV, "--name", name,
         "--value", value, "-o", "none"], check=True)

def ref(name):
    """A managed-store reference secret (JSON). Returns {} if absent."""
    raw = az_get(name)
    if not raw:
        print(f"  ! reference secret {name} missing — related fields left blank")
        return {}
    try:
        return json.loads(raw)
    except json.JSONDecodeError:
        return {}

# ── shared app secrets (generate once, cache) ────────────────────
shared_name = f"atpost-{ENV}-shared"
shared_raw = az_get(shared_name)
if shared_raw:
    shared = json.loads(shared_raw)
    print(f"  reusing shared secrets from {shared_name}")
else:
    print(f"  generating shared app secrets → {shared_name}")
    priv = subprocess.run(["openssl", "genrsa", "2048"], capture_output=True, text=True).stdout
    pub = subprocess.run(["openssl", "rsa", "-pubout"], input=priv,
                         capture_output=True, text=True).stdout
    shared = {
        "internal_service_key": secrets.token_urlsafe(48),
        "jwt_secret":           secrets.token_urlsafe(48),
        "jwt_kid":              "hs-1",
        "jwt_private_key_pem":  priv,
        "jwt_public_key_pem":   pub,
    }
    az_set(shared_name, json.dumps(shared))

# ── managed-store references ─────────────────────────────────────
pg   = ref(f"atpost-{ENV}-postgres")
rds  = ref(f"atpost-{ENV}-redis")
kfk  = ref(f"atpost-{ENV}-redpanda")
scy  = ref(f"atpost-{ENV}-scylla")

def value_for(prop, svc):
    if prop in shared:
        return shared[prop]
    if prop == "postgres_dsn":
        db = svc.replace("-", "_")
        if pg:
            return (f"postgres://{pg.get('username')}:{pg.get('password')}"
                    f"@{pg.get('host')}:{pg.get('port',5432)}/{db}?sslmode=require")
        return ""
    if prop == "redis_addr":
        return rds.get("addr", "")
    if prop == "redis_password":
        return rds.get("password", "")
    if prop == "kafka_brokers":
        return kfk.get("brokers", "")
    if prop == "scylla_hosts":
        return scy.get("hosts", "")
    print(f"    ? unknown property '{prop}' for {svc} — empty placeholder, fill it in")
    return ""

# ── per-service secrets ──────────────────────────────────────────
import re
def remote_refs(path):
    # Lightweight parse: collect `remoteRef: <name>` under externalSecret.data.
    refs = []
    for line in open(path):
        m = re.search(r"remoteRef:\s*([A-Za-z0-9_]+)", line)
        if m:
            refs.append(m.group(1))
    return refs

count = 0
for src in sorted(glob.glob(f"deploy/services/*/values-azure-{ENV}.yaml")):
    svc = os.path.basename(os.path.dirname(src))
    refs = remote_refs(src)
    if not refs:
        continue
    payload = {p: value_for(p, svc) for p in refs}
    az_set(f"atpost-{ENV}-{svc}", json.dumps(payload))
    print(f"  set atpost-{ENV}-{svc}  ({', '.join(refs)})")
    count += 1

print(f"done — seeded {count} per-service secrets (+ {shared_name}).")
print("NOTE: any '?' / blank fields above need a real value — re-run after `az keyvault secret set`.")
PY
