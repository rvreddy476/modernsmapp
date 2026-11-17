#!/usr/bin/env bash
# Write a clearly FAKE google-services.json, for CI unit-test runs only.
#
#   scripts/ci/android-placeholder-google-services.sh <out-file> <package>...
#
# The Google Services Gradle plugin refuses to configure :app without a
# google-services.json holding a client for each variant's applicationId, so
# Android CI could not compile or run a single unit test while the
# GOOGLE_SERVICES_JSON repository secret was empty (reels-9). Unit tests
# never reach Firebase, so a placeholder is enough for them.
#
# Rules (enforced in .github/workflows/android-native.yml, not here):
#   - CI only, written into the runner's checkout, never committed
#     (mobile/android/.gitignore covers the name);
#   - never on a push to main, and never for a release build: those need the
#     real file from the secret and fail without it;
#   - nothing built with it is uploaded.
# Every identifier is zeros or says PLACEHOLDER, so it cannot reach a real
# Firebase project.
set -eu
OUT="${1:-}"
[ -n "$OUT" ] && [ $# -ge 2 ] || { sed -n '2,4p' "$0"; exit 2; }
shift
[ -e "$OUT" ] && { echo "refusing to overwrite $OUT (a real file may be there)" >&2; exit 2; }

clients=""
for pkg in "$@"; do
  case "$pkg" in *[!a-zA-Z0-9._]*) echo "bad package name: $pkg" >&2; exit 2 ;; esac
  [ -n "$clients" ] && clients="$clients,"
  clients="$clients
    {
      \"client_info\": {
        \"mobilesdk_app_id\": \"1:000000000000:android:0000000000000000\",
        \"android_client_info\": { \"package_name\": \"$pkg\" }
      },
      \"oauth_client\": [],
      \"api_key\": [ { \"current_key\": \"CI-PLACEHOLDER-NOT-A-FIREBASE-KEY\" } ],
      \"services\": { \"appinvite_service\": { \"other_platform_oauth_client\": [] } }
    }"
done

mkdir -p "$(dirname "$OUT")"
cat >"$OUT" <<EOF
{
  "_comment": "CI PLACEHOLDER, NOT A FIREBASE CONFIG. Written by scripts/ci/android-placeholder-google-services.sh for unit-test runs only; never commit, never ship.",
  "project_info": {
    "project_number": "000000000000",
    "project_id": "ci-placeholder-not-a-project",
    "storage_bucket": "ci-placeholder-not-a-project.invalid"
  },
  "client": [$clients
  ],
  "configuration_version": "1"
}
EOF
echo "wrote PLACEHOLDER google-services.json ($# clients: $*) to $OUT"
