#!/usr/bin/env bash
# values-tag-test.sh — proves the tag bump touches only the intended keys.
#
# Copies real deploy/services values files into a scratch dir (never edits the
# tree), runs values-tag.sh / bump-service-tags.sh on the copies, and diffs.
# Every assertion is on the DIFF, so an unrelated line changing is a failure.
#
#   scripts/ci/values-tag-test.sh            # scratch dir from mktemp
#   SCRATCH=/some/dir scripts/ci/values-tag-test.sh
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/../.." && pwd)"
vt="$here/values-tag.sh"
bump="$here/bump-service-tags.sh"

scratch="${SCRATCH:-$(mktemp -d)}/values-tag-test.$$"
mkdir -p "$scratch"
echo "scratch: $scratch"

SHA=0123456789abcdef0123456789abcdef01234567
fail=0
pass() { echo "ok   $*"; }
flunk() { echo "FAIL $*"; fail=1; }

# copy FILE -> scratch/<name>; prints the copy's path. Normalised to LF: the
# index is LF (a Windows checkout with autocrlf shows some of these as CRLF,
# and awk there drops the CR), and the CI runner sees LF.
stage() { local src="$1" name="$2"; tr -d '\r' < "$src" > "$scratch/$name"; printf '%s' "$scratch/$name"; }

# changed_lines FILE_ORIG FILE_NEW: the "> " lines of the diff, minus the marker
changed_lines() { diff "$1" "$2" | sed -n 's/^> //p' || true; }
removed_lines() { diff "$1" "$2" | sed -n 's/^< //p' || true; }

# ── 1. get ────────────────────────────────────────────────────────────────────
f="$(stage "$root/deploy/services/media-service/values-staging.yaml" media-staging.yaml)"
[[ "$(bash "$vt" get "$f" image.tag)" == "latest" ]] && pass "get image.tag (bare token)" || flunk "get image.tag"
[[ "$(bash "$vt" get "$f" worker.image.tag)" == "latest" ]] && pass "get worker.image.tag" || flunk "get worker.image.tag"
[[ "$(bash "$vt" get "$f" worker.image.repository)" == *"/atpost/media-worker" ]] && pass "get worker.image.repository (single-quoted template)" || flunk "get worker.image.repository"
[[ "$(bash "$vt" get "$f" image.repository)" == *"/atpost/media-service" ]] && pass "get image.repository" || flunk "get image.repository"

g="$(stage "$root/deploy/services/media-service/values-azure-staging.yaml" media-azure-staging.yaml)"
[[ "$(bash "$vt" get "$g" image.tag)" =~ ^[0-9a-f]{40}$ ]] && pass "get image.tag (double-quoted sha)" || flunk "get quoted image.tag: $(bash "$vt" get "$g" image.tag)"

# missing key → exit 3, nothing printed
h="$(stage "$root/deploy/services/post-service/values-staging.yaml" post-staging.yaml)"
set +e; out="$(bash "$vt" get "$h" worker.image.tag 2>/dev/null)"; rc=$?; set -e
[[ $rc -eq 3 && -z "$out" ]] && pass "get of a missing key exits 3" || flunk "missing key: rc=$rc out='$out'"

# ── 2. set: one line, comment kept ────────────────────────────────────────────
cp "$f" "$f.orig"
bash "$vt" set "$f" worker.image.tag "$SHA"
if [[ "$(changed_lines "$f.orig" "$f")" == "    tag: \"$SHA\" # CI overrides via --set worker.image.tag=<sha>" \
   && "$(removed_lines "$f.orig" "$f")" == "    tag: latest # CI overrides via --set worker.image.tag=<sha>" \
   && "$(bash "$vt" get "$f" image.tag)" == "latest" ]]; then
  pass "set worker.image.tag changes exactly that line, keeps the comment, leaves image.tag"
else
  flunk "set worker.image.tag diff:"; diff "$f.orig" "$f" || true
fi

cp "$f" "$f.orig"
bash "$vt" set "$f" image.tag "$SHA"
if [[ "$(changed_lines "$f.orig" "$f")" == "  tag: \"$SHA\" # CI overrides via --set image.tag=<sha>" \
   && "$(diff "$f.orig" "$f" | grep -c '^[<>]')" == 2 ]]; then
  pass "set image.tag changes exactly that line"
else
  flunk "set image.tag diff:"; diff "$f.orig" "$f" || true
fi

# idempotent
cp "$f" "$f.orig"; bash "$vt" set "$f" image.tag "$SHA"
diff -q "$f.orig" "$f" >/dev/null && pass "set is idempotent" || flunk "set is not idempotent"

# a file with no `tag:` under image → exit 3 and file untouched
printf 'image:\n  repository: x\nworker:\n  image:\n    tag: latest\n' > "$scratch/no-image-tag.yaml"
cp "$scratch/no-image-tag.yaml" "$scratch/no-image-tag.yaml.orig"
set +e; bash "$vt" set "$scratch/no-image-tag.yaml" image.tag "$SHA" 2>/dev/null; rc=$?; set -e
[[ $rc -eq 3 ]] && diff -q "$scratch/no-image-tag.yaml.orig" "$scratch/no-image-tag.yaml" >/dev/null \
  && pass "set of a missing key exits 3 and leaves the file untouched" || flunk "missing-key set: rc=$rc"

# a `tag:` deeper than the addressed depth is NOT touched (e.g. under env:)
printf 'image:\n  tag: latest\nenv:\n  tag: keep\n  nested:\n    tag: keep\nworker:\n  image:\n    tag: latest\n  env:\n    tag: keep\n' > "$scratch/decoys.yaml"
cp "$scratch/decoys.yaml" "$scratch/decoys.yaml.orig"
bash "$vt" set "$scratch/decoys.yaml" image.tag "$SHA"
bash "$vt" set "$scratch/decoys.yaml" worker.image.tag "$SHA"
if [[ "$(grep -c 'keep' "$scratch/decoys.yaml")" == 3 && "$(grep -c "\"$SHA\"" "$scratch/decoys.yaml")" == 2 ]]; then
  pass "decoy tag: lines under other keys are untouched"
else
  flunk "decoys:"; cat "$scratch/decoys.yaml"
fi

# ── 3. bump-service-tags: the three real shapes ───────────────────────────────
# media-service: own worker image (Dockerfile.worker) → both keys to SHA
m="$(stage "$root/deploy/services/media-service/values-prod.yaml" media-prod.yaml)"; cp "$m" "$m.orig"
bash "$bump" media-service "$m" "$SHA" >/dev/null
if [[ "$(diff "$m.orig" "$m" | grep -c '^>')" == 2 \
   && "$(bash "$vt" get "$m" image.tag)" == "$SHA" && "$(bash "$vt" get "$m" worker.image.tag)" == "$SHA" ]]; then
  pass "bump media-service: image.tag and worker.image.tag both -> sha, 2 lines changed"
else
  flunk "bump media-service diff:"; diff "$m.orig" "$m" || true
fi

# dating-service: worker runs from the SERVER image → both keys to SHA
d="$(stage "$root/deploy/services/dating-service/values-staging.yaml" dating-staging.yaml)"; cp "$d" "$d.orig"
bash "$bump" dating-service "$d" "$SHA" >/dev/null
if [[ "$(diff "$d.orig" "$d" | grep -c '^>')" == 2 \
   && "$(bash "$vt" get "$d" worker.image.tag)" == "$SHA" ]]; then
  pass "bump dating-service: worker from the server image follows image.tag, 2 lines changed"
else
  flunk "bump dating-service diff:"; diff "$d.orig" "$d" || true
fi

# post-service: no worker block → only image.tag
cp "$h" "$h.orig"
bash "$bump" post-service "$h" "$SHA" >/dev/null
if [[ "$(diff "$h.orig" "$h" | grep -c '^>')" == 1 && "$(bash "$vt" get "$h" image.tag)" == "$SHA" ]]; then
  pass "bump post-service: no worker block, only image.tag changed"
else
  flunk "bump post-service diff:"; diff "$h.orig" "$h" || true
fi

# a foreign worker image with no Dockerfile.worker → worker.image.tag untouched, warning
printf 'image:\n  repository: r/atpost/foo\n  tag: latest\nworker:\n  image:\n    repository: r/atpost/other\n    tag: v1\n' > "$scratch/foreign.yaml"
out="$(bash "$bump" foo-service "$scratch/foreign.yaml" "$SHA" 2>&1)"
if [[ "$(bash "$vt" get "$scratch/foreign.yaml" worker.image.tag)" == "v1" && "$out" == *"::warning ::"* ]]; then
  pass "bump: a worker image that was not pushed keeps its tag and warns"
else
  flunk "foreign worker: $out"
fi

# ACR deploy names resolve to the source dir (identity-auth-service → identity-platform/services/auth-service)
a="$(stage "$root/deploy/services/identity-auth-service/values-azure-staging.yaml" identity-auth-azure.yaml)"; cp "$a" "$a.orig"
bash "$bump" identity-auth-service "$a" "$SHA" >/dev/null
[[ "$(diff "$a.orig" "$a" | grep -c '^>')" == 1 ]] && pass "bump: ACR deploy name, only image.tag changed" || { flunk "identity-auth diff:"; diff "$a.orig" "$a" || true; }

# ── 4. every real values file: get image.tag works ────────────────────────────
bad=0
for vf in "$root"/deploy/services/*/values-*.yaml; do
  bash "$vt" get "$vf" image.tag >/dev/null 2>&1 || { echo "     cannot read image.tag in $vf"; bad=1; }
done
[[ $bad -eq 0 ]] && pass "image.tag readable in every deploy/services values file" || flunk "some values files unreadable"

echo
if [[ $fail -eq 0 ]]; then echo "ALL PASS"; else echo "FAILURES"; exit 1; fi
