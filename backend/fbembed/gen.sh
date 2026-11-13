#!/usr/bin/env bash
# Rebuild the embedded frontend (fbembed/dist.zip).
#
# Run from the fbembed package dir (go generate does this):
#   go generate ./fbembed
#
# The frontend build already emits pre-gzipped .js (served with
# Content-Encoding: gzip by the static handler); the archive keeps the built
# tree as-is, minus source maps and the generated sample config.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
backend="$(cd "$here/.." && pwd)"
root="$(cd "$backend/.." && pwd)"

echo "fbembed: building frontend…"
( cd "$root/frontend" && npm ci --no-audit --no-fund && npm run build )

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
cp -r "$backend/internal/web/embed/." "$stage/"

( cd "$stage"
  find . -name '*.map' -delete
  find . -name '.gitignore' -delete
  rm -f config.generated.yaml
)

rm -f "$here/dist.zip"
( cd "$stage" && zip -X -q -r -9 "$here/dist.zip" . )
echo "fbembed: wrote dist.zip ($(du -h "$here/dist.zip" | cut -f1))"
