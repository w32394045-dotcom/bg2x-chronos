#!/usr/bin/env bash
#
# Project Chronos - build the Linux distribution packages (.deb + .tar.gz).
#
# Run from anywhere; paths are resolved against the repository root:
#
#   bash scripts/package-linux.sh
#   VERSION=0.2.0 OUT_DIR=dist-packages bash scripts/package-linux.sh
#
# Expects an already built desktop binary (from `wails build`) and the native
# shared library (from `cmake --build native/build`).
#
# Environment:
#   VERSION      package version, default 0.1.0 (no leading "v")
#   ARCH         Debian architecture, default amd64
#   OUT_DIR      output directory, default dist-packages
#   BIN          path to the desktop binary; autodetected under build/bin
#   LIB          path to libchronos.so, default native/build/libchronos.so
#   REGISTRY     model registry, default models/registry.json
#
# Layout of the portable tarball (the native library is loaded from libs/
# relative to the binary):
#   chronos-<version>-linux-<arch>/
#     chronos
#     libs/libchronos.so
#     models/registry.json
#     README.md, LICENSE (when present)
#
# The .deb installs the payload under /opt/chronos and a tiny /usr/bin/chronos
# wrapper that exports LD_LIBRARY_PATH=/opt/chronos/libs.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

VERSION="${VERSION:-0.1.0}"
VERSION="${VERSION#v}"
ARCH="${ARCH:-amd64}"
OUT_DIR="${OUT_DIR:-dist-packages}"
LIB="${LIB:-native/build/libchronos.so}"
BIN="${BIN:-}"

# The registry is embedded into the Go binary from internal/models/registry.json;
# a copy is shipped in the payload so users can inspect the catalogue offline.
REGISTRY="${REGISTRY:-}"
if [ -z "${REGISTRY}" ]; then
  for candidate in internal/models/registry.json models/registry.json; do
    if [ -f "${candidate}" ]; then
      REGISTRY="${candidate}"
      break
    fi
  done
fi

log() { printf '[package] %s\n' "$*"; }
fail() { printf '[package] ERROR: %s\n' "$*" >&2; exit 1; }

command -v dpkg-deb >/dev/null 2>&1 || fail "dpkg-deb is required (apt-get install dpkg)"
command -v tar >/dev/null 2>&1 || fail "tar is required"

if [ -z "${BIN}" ]; then
  for candidate in build/bin/chronos build/bin/chronos.exe build/bin/Chronos build/bin/Chronos.exe; do
    if [ -x "${candidate}" ]; then
      BIN="${candidate}"
      break
    fi
  done
fi
if [ -z "${BIN}" ]; then
  # Last resort: the single largest executable in build/bin.
  BIN="$(find build/bin -maxdepth 1 -type f -perm -u+x 2>/dev/null | head -n 1 || true)"
fi
[ -n "${BIN}" ] || fail "desktop binary not found; run 'wails build' or set BIN=..."
[ -f "${BIN}" ] || fail "BIN does not exist: ${BIN}"
[ -f "${LIB}" ] || fail "native library not found: ${LIB} (build native/build first)"
[ -n "${REGISTRY}" ] || fail "model registry not found (expected internal/models/registry.json)"
[ -f "${REGISTRY}" ] || fail "model registry not found: ${REGISTRY}"

log "version=${VERSION} arch=${ARCH} bin=${BIN} lib=${LIB}"

mkdir -p "${OUT_DIR}"
TARBALL="chronos-${VERSION}-linux-${ARCH}.tar.gz"
DEB="chronos_${VERSION}_${ARCH}.deb"
STAGE="$(mktemp -d)"
trap 'rm -rf "${STAGE}"' EXIT

TAR_ROOT="${STAGE}/${TARBALL%.tar.gz}"
mkdir -p "${TAR_ROOT}/libs" "${TAR_ROOT}/models"
install -m 0755 "${BIN}" "${TAR_ROOT}/chronos"
install -m 0644 "${LIB}" "${TAR_ROOT}/libs/libchronos.so"
install -m 0644 "${REGISTRY}" "${TAR_ROOT}/models/registry.json"
for extra in README.md LICENSE; do
  if [ -f "${extra}" ]; then
    install -m 0644 "${extra}" "${TAR_ROOT}/${extra}"
  fi
done

log "creating ${OUT_DIR}/${TARBALL}"
tar -C "${STAGE}" -czf "${OUT_DIR}/${TARBALL}" "${TARBALL%.tar.gz}"

# --- .deb -------------------------------------------------------------------
DEB_ROOT="${STAGE}/deb"
mkdir -p "${DEB_ROOT}/DEBIAN" \
  "${DEB_ROOT}/usr/bin" \
  "${DEB_ROOT}/usr/share/applications" \
  "${DEB_ROOT}/usr/share/doc/chronos" \
  "${DEB_ROOT}/opt/chronos/libs" \
  "${DEB_ROOT}/opt/chronos/models"

install -m 0755 "${BIN}" "${DEB_ROOT}/opt/chronos/chronos"
install -m 0644 "${LIB}" "${DEB_ROOT}/opt/chronos/libs/libchronos.so"
install -m 0644 "${REGISTRY}" "${DEB_ROOT}/opt/chronos/models/registry.json"
for extra in README.md LICENSE; do
  if [ -f "${extra}" ]; then
    install -m 0644 "${extra}" "${DEB_ROOT}/opt/chronos/${extra}"
  fi
done
cat > "${DEB_ROOT}/usr/bin/chronos" <<'WRAPPER'
#!/bin/sh
# Chronos launcher: the native library ships next to the binary in /opt/chronos.
LD_LIBRARY_PATH="/opt/chronos/libs${LD_LIBRARY_PATH:+:${LD_LIBRARY_PATH}}"
export LD_LIBRARY_PATH
exec /opt/chronos/chronos "$@"
WRAPPER
chmod 0755 "${DEB_ROOT}/usr/bin/chronos"

cat > "${DEB_ROOT}/usr/share/applications/chronos.desktop" <<'DESKTOP'
[Desktop Entry]
Type=Application
Name=Chronos
Comment=Desktop image upscaler
Exec=chronos
Terminal=false
Categories=Graphics;2DGraphics;
DESKTOP

if [ -f LICENSE ]; then
  cp LICENSE "${DEB_ROOT}/usr/share/doc/chronos/copyright"
fi
if [ -f README.md ]; then
  cp README.md "${DEB_ROOT}/usr/share/doc/chronos/README.md"
fi

INSTALLED_SIZE="$(du -sk "${DEB_ROOT}/opt" "${DEB_ROOT}/usr" | awk '{sum += $1} END {print sum}')"
cat > "${DEB_ROOT}/DEBIAN/control" <<CONTROL
Package: chronos
Version: ${VERSION}
Section: graphics
Priority: optional
Architecture: ${ARCH}
Installed-Size: ${INSTALLED_SIZE}
Depends: libgtk-3-0 | libgtk-3-0t64, libwebkit2gtk-4.1-0
Maintainer: Project Chronos <noreply@github.com>
Homepage: https://github.com/w32394045-dotcom/bg2x-chronos
Description: Project Chronos desktop image upscaler
 A Wails desktop front-end for Project Chronos: tiled C++ inference with
 an optional OpenVINO backend, batch queue and sleep/lock inhibition.
CONTROL

log "creating ${OUT_DIR}/${DEB}"
dpkg-deb --build --root-owner-group "${DEB_ROOT}" "${OUT_DIR}/${DEB}" >/dev/null

# --- checksums --------------------------------------------------------------
if command -v sha256sum >/dev/null 2>&1; then
  (
    cd "${OUT_DIR}"
    sha256sum "${DEB}" "${TARBALL}" > SHA256SUMS
    cat SHA256SUMS
  )
fi

log "done:"
ls -lh "${OUT_DIR}/${DEB}" "${OUT_DIR}/${TARBALL}"
