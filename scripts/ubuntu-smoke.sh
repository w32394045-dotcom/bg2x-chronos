#!/usr/bin/env bash
#
# Project Chronos - headless CLI smoke test (Linux / POSIX).
#
# Usage:
#   bash scripts/ubuntu-smoke.sh [path/to/chronos]
#   CHRONOS_BIN=./build/bin/chronos bash scripts/ubuntu-smoke.sh
#
# The script needs no third-party software: the test PNG is written with the
# standard library only (python3 + zlib + struct), so no Pillow/ImageMagick.
#
# Checks:
#   1. `chronos upscale -i in.png -o out.png -s 2 --no-model` succeeds and the
#      output is a valid PNG whose IHDR is exactly 128x96 (64x48 input, 2x).
#   2. `chronos inhibit status` exits 0.
#   3. `chronos info` prints an engine/backend line.
set -euo pipefail

BIN="${1:-${CHRONOS_BIN:-./chronos}}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

log() { printf '[smoke] %s\n' "$*"; }
fail() { printf '[smoke] FAIL: %s\n' "$*" >&2; exit 1; }

find_python() {
  if [ -n "${PYTHON:-}" ] && command -v "${PYTHON}" >/dev/null 2>&1; then
    printf '%s' "${PYTHON}"
    return 0
  fi
  command -v python3 2>/dev/null || command -v python 2>/dev/null || true
}

PY="$(find_python)"
[ -n "${PY}" ] || fail "python3 (or python) is required for PNG generation/assertions"

if [ ! -f "${BIN}" ]; then
  log "CLI '${BIN}' not found, building it from ${REPO_ROOT}"
  [ -d "${REPO_ROOT}/native/include" ] || fail "not a Chronos checkout: ${REPO_ROOT}"
  ( cd "${REPO_ROOT}" && CGO_ENABLED=1 go build -tags chronos_native -o "${BIN}" ./cmd/chronos )
fi
[ -x "${BIN}" ] || chmod +x "${BIN}" 2>/dev/null || fail "cannot execute CLI: ${BIN}"
BIN="$(cd "$(dirname "${BIN}")" && pwd)/$(basename "${BIN}")"
log "using CLI: ${BIN}"

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT
IN="${WORK}/in.png"
OUT="${WORK}/out.png"

# --- 1. generate a 64x48 RGB PNG (stdlib only) ------------------------------
"${PY}" - "${IN}" <<'PY'
import struct
import sys
import zlib

path = sys.argv[1]
width, height = 64, 48

rows = []
for y in range(height):
    row = bytearray([0])  # filter type 0 (None)
    for x in range(width):
        row += bytes(((x * 5) & 0xFF, (y * 7) & 0xFF, ((x + y) * 3) & 0xFF))
    rows.append(bytes(row))
raw = b"".join(rows)


def chunk(tag: bytes, data: bytes) -> bytes:
    crc = zlib.crc32(tag + data) & 0xFFFFFFFF
    return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", crc)


png = b"\x89PNG\r\n\x1a\n"
png += chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
png += chunk(b"IDAT", zlib.compress(raw, 9))
png += chunk(b"IEND", b"")
with open(path, "wb") as fh:
    fh.write(png)
print("[smoke] generated %s (%dx%d, %d bytes)" % (path, width, height, len(png)))
PY

# --- 2. upscale with the stub backend (no model download) -------------------
log "running: chronos upscale -i in.png -o out.png -s 2 --no-model"
"${BIN}" upscale -i "${IN}" -o "${OUT}" -s 2 --no-model \
  || fail "upscale exited non-zero"

if [ ! -f "${OUT}" ]; then
  # tolerate an implementation that treats -o as an output directory.
  ALT="$(find "${WORK}" -maxdepth 2 -name '*_chronos_x2.png' -type f 2>/dev/null | head -n 1 || true)"
  [ -n "${ALT}" ] || fail "upscale produced no output file"
  OUT="${ALT}"
  log "output found at ${OUT}"
fi

"${PY}" - "${OUT}" <<'PY'
import struct
import sys

path = sys.argv[1]
with open(path, "rb") as fh:
    data = fh.read()
if data[:8] != b"\x89PNG\r\n\x1a\n":
    raise SystemExit("[smoke] FAIL: output is not a PNG: " + path)
if data[12:16] != b"IHDR":
    raise SystemExit("[smoke] FAIL: output has no IHDR chunk: " + path)
width, height = struct.unpack(">II", data[16:24])
if (width, height) != (128, 96):
    raise SystemExit(
        "[smoke] FAIL: expected 128x96 output, got %dx%d (%s)" % (width, height, path)
    )
print("[smoke] PASS output PNG is %dx%d" % (width, height))
PY
log "PASS upscale 64x48 -> 128x96 (--no-model)"

# --- 3. inhibit status must be callable headless ----------------------------
log "running: chronos inhibit status"
"${BIN}" inhibit status || fail "inhibit status exited non-zero"
log "PASS inhibit status"

# --- 4. info must describe the engine backend ------------------------------
log "running: chronos info"
INFO="$("${BIN}" info 2>&1)" || fail "info exited non-zero"
printf '%s\n' "${INFO}"
printf '%s\n' "${INFO}" | grep -qi 'backend' || fail "info did not print a backend line"
log "PASS info reports an engine backend"

printf '\n[smoke] ALL CHECKS PASSED\n'
