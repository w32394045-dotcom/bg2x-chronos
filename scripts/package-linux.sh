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
#   WEB_BIN      path to the headless browser-mode CLI; autodetected under build/bin
#   DIST_DIR     built frontend (index.html + assets/) copied into the web tarball
#                as web/; default frontend/dist
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
# A second, browser-mode tarball is emitted from WEB_BIN (no GTK/WebView needed):
#   chronos-<version>-linux-<arch>-web/
#     chronos          headless CLI (same binary, `chronos serve`)
#     chronos-web      launcher: exec ./chronos serve --open
#     web/             built frontend (index.html + assets); ResolveAssets finds
#                      <exeDir>/web first, so `./chronos serve` works as-is
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

# Browser-mode CLI (no GTK/WebView): `go build -tags chronos_native -o
# build/bin/chronos-cli ./cmd/chronos`. It is shipped in its own tarball.
WEB_BIN="${WEB_BIN:-}"
if [ -z "${WEB_BIN}" ]; then
  for candidate in build/bin/chronos-cli build/bin/chronos-cli.exe; do
    if [ -f "${candidate}" ] && [ -x "${candidate}" ]; then
      WEB_BIN="${candidate}"
      break
    fi
  done
fi
[ -n "${WEB_BIN}" ] || fail "browser-mode CLI not found; build it with 'go build -tags chronos_native -o build/bin/chronos-cli ./cmd/chronos' or set WEB_BIN=..."
[ -f "${WEB_BIN}" ] || fail "WEB_BIN does not exist: ${WEB_BIN}"

# Built frontend for browser mode: the CLI looks for <exeDir>/web first.
DIST_DIR="${DIST_DIR:-}"
if [ -z "${DIST_DIR}" ] && [ -f "frontend/dist/index.html" ]; then
  DIST_DIR="frontend/dist"
fi
if [ -z "${DIST_DIR}" ]; then
  fail "frontend build not found (expected frontend/dist/index.html); run 'cd frontend && pnpm build' or set DIST_DIR=..."
fi
[ -f "${DIST_DIR}/index.html" ] || fail "DIST_DIR has no index.html: ${DIST_DIR}"

[ -f "${LIB}" ] || fail "native library not found: ${LIB} (build native/build first)"
[ -n "${REGISTRY}" ] || fail "model registry not found (expected internal/models/registry.json)"
[ -f "${REGISTRY}" ] || fail "model registry not found: ${REGISTRY}"

log "version=${VERSION} arch=${ARCH} bin=${BIN} lib=${LIB}"

mkdir -p "${OUT_DIR}"
TARBALL="chronos-${VERSION}-linux-${ARCH}.tar.gz"
WEB_TARBALL="chronos-${VERSION}-linux-${ARCH}-web.tar.gz"
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

# --- browser-mode tarball ---------------------------------------------------
# Same native library and model registry, but the entry point is the headless
# CLI: `chronos serve --open` serves the embedded UI over 127.0.0.1. No GTK or
# WebView runtime is required, so this is the fallback for Linux boxes without
# WebKitGTK (and for CI where no display server exists).
WEB_ROOT="${STAGE}/${WEB_TARBALL%.tar.gz}"
mkdir -p "${WEB_ROOT}/libs" "${WEB_ROOT}/models" "${WEB_ROOT}/web"
install -m 0755 "${WEB_BIN}" "${WEB_ROOT}/chronos"
install -m 0644 "${LIB}" "${WEB_ROOT}/libs/libchronos.so"
install -m 0644 "${REGISTRY}" "${WEB_ROOT}/models/registry.json"
cp -R "${DIST_DIR}/." "${WEB_ROOT}/web/"
for extra in README.md LICENSE; do
  if [ -f "${extra}" ]; then
    install -m 0644 "${extra}" "${WEB_ROOT}/${extra}"
  fi
done
cat > "${WEB_ROOT}/chronos-web" <<'LAUNCHER'
#!/bin/sh
# Chronos browser mode: serve the UI on 127.0.0.1 and open the default browser.
# The CLI links libchronos.so from libs/ (no rpath), so export the library path.
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd) || exit 1
LD_LIBRARY_PATH="${here}/libs${LD_LIBRARY_PATH:+:${LD_LIBRARY_PATH}}"
export LD_LIBRARY_PATH
cd "${here}" || exit 1
exec ./chronos serve --open "$@"
LAUNCHER
chmod 0755 "${WEB_ROOT}/chronos-web"

log "creating ${OUT_DIR}/${WEB_TARBALL}"
tar -C "${STAGE}" -czf "${OUT_DIR}/${WEB_TARBALL}" "${WEB_TARBALL%.tar.gz}"

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
    sha256sum "${DEB}" "${TARBALL}" "${WEB_TARBALL}" > SHA256SUMS
    cat SHA256SUMS
  )
fi

log "done:"
ls -lh "${OUT_DIR}/${DEB}" "${OUT_DIR}/${TARBALL}" "${OUT_DIR}/${WEB_TARBALL}"
