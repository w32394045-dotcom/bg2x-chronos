#!/usr/bin/env bash
#
# Project Chronos - download a model listed in models/registry.json.
#
# Usage:
#   bash scripts/fetch-models.sh --list
#   bash scripts/fetch-models.sh real-esrgan-x4plus
#   bash scripts/fetch-models.sh --dir /path/to/models real-esrgan-animevideov3
#   bash scripts/fetch-models.sh --force real-cugan-2x
#
# Downloads with python3 (stdlib json + urllib only), tries the primary URL and
# then every fallbackUrls entry, verifies sha256 when the registry provides one,
# and saves the file as models/<id>.<ext>. Files are gitignored (models/*.onnx).
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# The catalogue is embedded by internal/models (go:embed), so the single source
# of truth lives at internal/models/registry.json; models/registry.json is kept
# as a legacy fallback for older checkouts.
DEFAULT_REGISTRY="${REPO_ROOT}/internal/models/registry.json"
if [ ! -f "${DEFAULT_REGISTRY}" ] && [ -f "${REPO_ROOT}/models/registry.json" ]; then
  DEFAULT_REGISTRY="${REPO_ROOT}/models/registry.json"
fi
REGISTRY="${REGISTRY:-${DEFAULT_REGISTRY}}"
MODEL_DIR="${MODEL_DIR:-${REPO_ROOT}/models}"

find_python() {
  if [ -n "${PYTHON:-}" ] && command -v "${PYTHON}" >/dev/null 2>&1; then
    printf '%s' "${PYTHON}"
    return 0
  fi
  command -v python3 2>/dev/null || command -v python 2>/dev/null || true
}

PY="$(find_python)"
[ -n "${PY}" ] || { echo "[models] ERROR: python3 (or python) is required" >&2; exit 1; }
[ -f "${REGISTRY}" ] || { echo "[models] ERROR: registry not found: ${REGISTRY}" >&2; exit 1; }

"${PY}" - "${REGISTRY}" "${MODEL_DIR}" "$@" <<'PY'
import hashlib
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

registry_path = sys.argv[1]
model_dir = sys.argv[2]
user_args = sys.argv[3:]

import argparse

parser = argparse.ArgumentParser(
    prog="fetch-models.sh",
    description="Download a Project Chronos model listed in models/registry.json.",
)
parser.add_argument("model_id", nargs="?", help="model id, as printed by --list")
parser.add_argument("--list", action="store_true", help="list available model ids and exit")
parser.add_argument("--dir", default=model_dir, help="destination directory (default: %(default)s)")
parser.add_argument("--force", action="store_true", help="overwrite an existing file")
args = parser.parse_args(user_args)

try:
    with open(registry_path, "r", encoding="utf-8") as fh:
        registry = json.load(fh)
    models = registry.get("models", [])
except (OSError, ValueError) as exc:
    sys.exit("[models] ERROR: cannot read registry %s: %s" % (registry_path, exc))


def human(num):
    for unit in ("B", "KB", "MB", "GB"):
        if num < 1024 or unit == "GB":
            return ("%.1f %s" % (num, unit)) if unit != "B" else ("%d B" % num)
        num /= 1024.0


if args.list:
    print("%-30s %5s %-11s %9s  %s" % ("ID", "SCALE", "KIND", "SIZE (MB)", "NAME"))
    for model in models:
        print(
            "%-30s %5s %-11s %9s  %s"
            % (
                model.get("id", "?"),
                model.get("scale", "?"),
                model.get("kind", "?"),
                model.get("sizeMB", "?"),
                model.get("name", ""),
            )
        )
    sys.exit(0)

if not args.model_id:
    parser.print_help()
    sys.exit(2)

model = next((m for m in models if m.get("id") == args.model_id), None)
if model is None:
    sys.exit(
        "[models] ERROR: unknown model id '%s' (use --list)" % args.model_id
    )

urls = [u for u in [model.get("url")] + list(model.get("fallbackUrls") or []) if u]
if not urls:
    sys.exit("[models] ERROR: model '%s' has no download URL" % args.model_id)

path = urllib.parse.urlparse(urls[0]).path
ext = os.path.splitext(path)[1].lower()
if ext not in (".onnx", ".xml", ".bin", ".tgz", ".tar", ".zip"):
    ext = ".onnx"
dest_dir = os.path.abspath(args.dir)
os.makedirs(dest_dir, exist_ok=True)
dest = os.path.join(dest_dir, args.model_id + ext)
part = dest + ".part"

if os.path.exists(dest) and not args.force:
    print("[models] already present: %s (%s); use --force to re-download" % (dest, human(os.path.getsize(dest))))
    sys.exit(0)

expected_sha = (model.get("sha256") or "").strip().lower()
errors = []
for url in urls:
    print("[models] downloading %s" % url)
    try:
        request = urllib.request.Request(
            url,
            headers={"User-Agent": "chronos-model-fetcher/1.0 (+https://github.com/w32394045-dotcom/bg2x-chronos)"},
        )
        digest = hashlib.sha256()
        downloaded = 0
        with urllib.request.urlopen(request, timeout=60) as response:
            total = int(response.headers.get("Content-Length") or 0)
            with open(part, "wb") as fh:
                while True:
                    chunk = response.read(1024 * 256)
                    if not chunk:
                        break
                    fh.write(chunk)
                    digest.update(chunk)
                    downloaded += len(chunk)
                    if total > 0:
                        pct = downloaded * 100.0 / total
                        sys.stdout.write(
                            "\r[models]   %5.1f%%  %s / %s" % (pct, human(downloaded), human(total))
                        )
                        sys.stdout.flush()
            sys.stdout.write("\n")
        actual_sha = digest.hexdigest()
        if expected_sha and actual_sha != expected_sha:
            os.remove(part)
            errors.append("%s: sha256 mismatch (expected %s, got %s)" % (url, expected_sha, actual_sha))
            continue
        os.replace(part, dest)
        if expected_sha:
            print("[models] sha256 OK: %s" % actual_sha)
        else:
            print("[models] registry has no sha256 for '%s'; skipped verification (got %s)" % (args.model_id, actual_sha))
        print("[models] saved %s (%s)" % (dest, human(os.path.getsize(dest))))
        sys.exit(0)
    except (urllib.error.URLError, OSError, TimeoutError) as exc:
        errors.append("%s: %s" % (url, exc))
        if os.path.exists(part):
            os.remove(part)
        continue

sys.stderr.write("[models] ERROR: all download URLs failed for '%s'\n" % args.model_id)
for err in errors:
    sys.stderr.write("  - %s\n" % err)
sys.exit(3)
PY
