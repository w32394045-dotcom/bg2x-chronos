#!/usr/bin/env bash
#
# Project Chronos - one-shot build script (Linux / macOS, and Windows via Git Bash).
#
# Builds the whole product and assembles a ready-to-run dist tree:
#   frontend (Vue 3 + Vite) -> native C++ engine (CMake + Ninja) -> Go CLI
#   (headless / browser mode) -> optional desktop GUI (Wails, or a plain Go
#   build when the Wails CLI is not installed) -> <out>/.
#
# Usage:
#   bash scripts/build.sh [flags]
#   bash scripts/build.sh --help
#
# Flags:
#   --profile=cpu|openvino|all   native engine profile (default: all)
#                                cpu     : stub/CPU engine only (always buildable)
#                                openvino: OpenVINO backend; requires OpenVINO_DIR
#                                all     : build the CPU engine and, when
#                                          OpenVINO_DIR is set, also build the
#                                          OpenVINO variant into libs/optional/
#   --skip-frontend              do not run pnpm install / pnpm build
#   --skip-native                do not run CMake; reuse an existing native/build
#                                library, or build the pure-Go fallback engine
#   --skip-gui                   do not build the desktop GUI
#   --debug                      Debug native build (and wails -debug)
#   --clean                      remove <out> and (when native is built)
#                                native/build before building
#   --out=DIR                    output directory, default: build/dist
#   -h, --help                   print this help
#
# Environment:
#   CHRONOS_AUTODEP=1            if cmake/ninja are missing, download pinned
#                                copies into native/build/.deps (CMake 3.31.6 from
#                                cmake.org + the Ninja 1.13.2 wheel from PyPI,
#                                both sha256-pinned). This NEVER downloads a
#                                compiler, Node or pnpm.
#   CHRONOS_BUILD_TESTS=1        build the native ctest suite (default: off)
#   CHRONOS_GUI=plain            force the plain `go build` GUI path even when
#                                the wails CLI is installed
#   OpenVINO_DIR=DIR             directory containing OpenVINOConfig.cmake;
#                                required by --profile=openvino
#   TMPDIR / TEMP                scratch location for the non-ASCII-path
#                                link workaround (see below)
#
# Output layout (default build/dist):
#   README.txt
#   bin/chronos[.exe]            headless CLI (upscale/info/models/serve/bench)
#   bin/chronos-cli[.exe]        same CLI (release/package naming)
#   bin/chronos-desktop[.exe]    desktop GUI (when not --skip-gui)
#   bin/chronos.dll              native engine on Windows
#   bin/libchronos.so            native engine on Linux
#   bin/libs/libchronos.so       Linux runtime copy (matches the cgo rpath)
#   libs/chronos.dll | libs/libchronos.so
#   libs/optional/openvino/...   only for --profile=all + OpenVINO_DIR
#   web/                         built frontend (index.html + assets/)
#
# Idempotent: re-running rebuilds incrementally and overwrites only files inside
# <out>/ and the native CMake build tree. The ONLY writes outside those two
# places are (a) the Wails build directory build/bin/ when the GUI is built
# (hardcoded by the Wails CLI), and (b) a short-lived ASCII scratch directory in
# TMPDIR used to work around MinGW ld not accepting non-ASCII -L paths.
#
# Windows note: this script also runs from Git Bash (it detects MINGW/MSYS and
# builds Windows binaries). CGO requires a MinGW-w64 gcc/g++ on PATH.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

# ---------------------------------------------------------------------------
# defaults
# ---------------------------------------------------------------------------
PROFILE="all"
SKIP_FRONTEND=0
SKIP_NATIVE=0
SKIP_GUI=0
DEBUG=0
CLEAN=0
OUT="build/dist"

TOTAL_START="${SECONDS}"
STEP_N=0
STEP_NAME=""
STEP_START=0
LINK_STAGE=""
LINK_STAGE_WIN=""
OV_VARIANT_DIR=""
NATIVE_BUILD_TYPE="Release"
WEB_AVAILABLE=0

# ---------------------------------------------------------------------------
# logging / errors / step banners
# ---------------------------------------------------------------------------
log()  { printf '[build] %s\n' "$*"; }
err()  { printf '[build] ERROR: %s\n' "$*" >&2; }
die()  { err "$*"; exit 1; }

step_begin() {
  STEP_N=$((STEP_N + 1))
  STEP_NAME="$1"
  STEP_START="${SECONDS}"
  printf '\n[build] ===== STEP %d: %s =====\n' "${STEP_N}" "${STEP_NAME}"
}

step_end() {
  local elapsed=$((SECONDS - STEP_START))
  log "step '${STEP_NAME}' finished in ${elapsed}s"
}

cleanup() {
  if [ -n "${LINK_STAGE}" ] && [ -d "${LINK_STAGE}" ]; then
    rm -rf "${LINK_STAGE}" || true
  fi
}
trap cleanup EXIT

usage() {
  cat <<'USAGE'
Project Chronos - one-shot build script.

Usage:
  bash scripts/build.sh [flags]

Flags:
  --profile=cpu|openvino|all   native engine profile (default: all)
                               cpu      : stub/CPU engine only (always buildable)
                               openvino : OpenVINO backend; requires OpenVINO_DIR
                               all      : CPU engine, plus the OpenVINO variant
                                          under libs/optional/openvino when
                                          OpenVINO_DIR is set
  --skip-frontend              do not run pnpm install / pnpm build
  --skip-native                do not run CMake; reuse native/build if present,
                               otherwise build the pure-Go fallback engine
  --skip-gui                   do not build the desktop GUI
  --debug                      Debug native build (and wails -debug)
  --clean                      remove <out> and native/build before building
  --out=DIR                    output directory (default: build/dist)
  -h, --help                   print this help

Environment:
  CHRONOS_AUTODEP=1            fetch pinned CMake 3.31.6 + Ninja 1.13.2 into
                               native/build/.deps when they are missing
                               (sha256-pinned; never downloads a compiler or Node)
  CHRONOS_BUILD_TESTS=1        build the native ctest suite (default: off)
  CHRONOS_GUI=plain            force the plain go-build GUI path
  OpenVINO_DIR=DIR             directory containing OpenVINOConfig.cmake
USAGE
}

# ---------------------------------------------------------------------------
# args
# ---------------------------------------------------------------------------
while [ "$#" -gt 0 ]; do
  case "$1" in
    --profile=*)     PROFILE="${1#*=}" ;;
    --out=*)         OUT="${1#*=}" ;;
    --skip-frontend) SKIP_FRONTEND=1 ;;
    --skip-native)   SKIP_NATIVE=1 ;;
    --skip-gui)      SKIP_GUI=1 ;;
    --debug)         DEBUG=1 ;;
    --clean)         CLEAN=1 ;;
    -h|--help)       usage; exit 0 ;;
    *)               die "unknown argument: $1 (see --help)" ;;
  esac
  shift
done

case "${PROFILE}" in
  cpu|openvino|all) ;;
  *) die "invalid --profile='${PROFILE}' (expected cpu, openvino or all)" ;;
esac

if [ -z "${OUT}" ]; then
  die "--out requires a directory name (default: build/dist)"
fi

# ---------------------------------------------------------------------------
# platform / tool names
# ---------------------------------------------------------------------------
UNAME_S="$(uname -s)"
case "${UNAME_S}" in
  Linux*)                     OS=linux ;;
  Darwin*)                    OS=darwin ;;
  MINGW*|MSYS*|CYGWIN*)       OS=windows ;;
  *) die "unsupported platform '${UNAME_S}' (Linux, macOS and Windows Git Bash are supported)" ;;
esac

EXE=""
SHLIB="libchronos.so"
case "${OS}" in
  windows) EXE=".exe"; SHLIB="chronos.dll" ;;
  darwin)  SHLIB="libchronos.dylib" ;;
esac

AUTODEP="${CHRONOS_AUTODEP:-0}"
BUILD_TESTS_OPT="OFF"
if [ "${CHRONOS_BUILD_TESTS:-0}" = "1" ]; then BUILD_TESTS_OPT="ON"; fi
if [ "${DEBUG}" = "1" ]; then NATIVE_BUILD_TYPE="Debug"; fi

# pnpm can be a .cmd shim on Windows; Git Bash cannot execute it directly.
PNPM_MODE="direct"
PNPM_VER=""

have() { command -v "$1" >/dev/null 2>&1; }

pnpm_run() {
  if [ "${PNPM_MODE}" = "cmd" ]; then
    # --skip-frontend is the common case under Git Bash; this path is only used
    # when pnpm is a .cmd shim without a POSIX wrapper.
    cmd //c pnpm "$@"
  else
    pnpm "$@"
  fi
}

# Returns 0 when version $1 >= version $2 (dotted numeric versions).
version_ge() {
  awk -v a="$1" -v b="$2" 'BEGIN {
    na = split(a, A, "."); nb = split(b, B, ".");
    n = (na > nb) ? na : nb;
    for (i = 1; i <= n; i++) {
      x = (i <= na) ? A[i] + 0 : 0;
      y = (i <= nb) ? B[i] + 0 : 0;
      if (x > y) exit 0;
      if (x < y) exit 1;
    }
    exit 0;
  }'
}

path_is_ascii() {
  case "$(printf '%s' "$1" | LC_ALL=C tr -d '\000-\177')" in
    "") return 0 ;;
    *)  return 1 ;;
  esac
}

sha256_of() {
  if have sha256sum; then
    sha256sum "$1" | awk '{print $1}'
  elif have shasum; then
    shasum -a 256 "$1" | awk '{print $1}'
  else
    die "CHRONOS_AUTODEP needs sha256sum (Linux / Git Bash) or shasum (macOS)"
  fi
}

download_file() {
  url="$1"; dest="$2"; expected="$3"
  have curl || die "CHRONOS_AUTODEP needs curl (install curl or install cmake/ninja manually)"
  log "downloading ${url}"
  curl -fSL --retry 2 --connect-timeout 20 -o "${dest}" "${url}" \
    || die "download failed: ${url}"
  actual="$(sha256_of "${dest}")"
  if [ "${actual}" != "${expected}" ]; then
    rm -f "${dest}"
    die "sha256 mismatch for ${url} (expected ${expected}, got ${actual})"
  fi
  log "sha256 verified: ${expected}"
}

unzip_to() {
  src="$1"; dest="$2"
  local ps_exe=""
  mkdir -p "${dest}"
  if [ "${OS}" = "windows" ]; then
    have powershell || have powershell.exe || die "PowerShell is required to extract ZIP archives on Windows"
    if have powershell; then ps_exe="powershell"; else ps_exe="powershell.exe"; fi
    have cygpath || die "cygpath (Git Bash) is required for CHRONOS_AUTODEP on Windows"
    "${ps_exe}" -NoProfile -Command \
      "Expand-Archive -LiteralPath '$(cygpath -w "${src}")' -DestinationPath '$(cygpath -w "${dest}")' -Force"
    return 0
  fi
  if have unzip; then
    unzip -q -o "${src}" -d "${dest}"
    return 0
  fi
  if have python3; then
    python3 -m zipfile -e "${src}" "${dest}"
    return 0
  fi
  die "cannot extract ZIP: install 'unzip' or 'python3' (needed only for CHRONOS_AUTODEP)"
}

# ---------------------------------------------------------------------------
# optional auto-dependency download (cmake + ninja ONLY, sha256-pinned)
# ---------------------------------------------------------------------------
autodep_cmake() {
  [ "${AUTODEP}" = "1" ] || return 1
  local url="" sha="" kind=""
  case "${OS}" in
    windows)
      url="https://cmake.org/files/v3.31/cmake-3.31.6-windows-x86_64.zip"
      sha="d163cd3ab4959b0a53fa8988f2ddbd2e6c501658201e6a154386bad9dbe4f836"
      kind="zip"
      ;;
    linux)
      case "$(uname -m)" in
        x86_64|amd64)
          url="https://cmake.org/files/v3.31/cmake-3.31.6-linux-x86_64.tar.gz"
          sha="5a1133ff103c71eb5120e2cc3de922733e7d8a26a98ae716397e8676adb367bf"
          kind="tar.gz"
          ;;
        aarch64|arm64)
          url="https://cmake.org/files/v3.31/cmake-3.31.6-linux-aarch64.tar.gz"
          sha="b4cc788d63112b2749b40627e719eb5d3b8ed8f00c36d77189f4019cfe64bc9e"
          kind="tar.gz"
          ;;
        *) return 1 ;;
      esac
      ;;
    *) return 1 ;;
  esac

  local deps="native/build/.deps" archive="native/build/.deps/cmake-download"
  mkdir -p "${deps}/cmake"
  download_file "${url}" "${archive}" "${sha}"
  if [ "${kind}" = "zip" ]; then
    unzip_to "${archive}" "${deps}/cmake"
  else
    tar -xzf "${archive}" -C "${deps}/cmake"
  fi
  rm -f "${archive}"
  local bin
  bin="$(find "${deps}/cmake" -type f \( -name cmake -o -name cmake.exe \) 2>/dev/null | head -n 1)"
  [ -n "${bin}" ] || die "cmake archive extracted but no cmake executable was found under ${deps}/cmake"
  export PATH="$(dirname "${bin}"):${PATH}"
  log "cmake installed for this run: ${bin}"
}

autodep_ninja() {
  [ "${AUTODEP}" = "1" ] || return 1
  local url="" sha=""
  case "${OS}" in
    windows)
      url="https://files.pythonhosted.org/packages/3f/dd/3766b5f4d32e8a9b97d195496b0b01fbbe2e1a41669dab0cd6492a6ce199/ninja-1.13.2-py3-none-win_amd64.whl"
      sha="1293f4078278b70d0ee4b6cc8f3a9e030656c9b2f59909970343c4fe76070118"
      ;;
    linux)
      case "$(uname -m)" in
        x86_64|amd64)
          url="https://files.pythonhosted.org/packages/6e/53/ebfed7b689c338dd8ebeec9c0730c8d56821292f14e2536e5f3ef1a05744/ninja-1.13.2-py3-none-manylinux2014_x86_64.manylinux_2_17_x86_64.whl"
          sha="65a24341b5ac09fcadcc37082660be40a94174e51a937fabf6e2cae26225fa2c"
          ;;
        aarch64|arm64)
          url="https://files.pythonhosted.org/packages/35/54/7368ce188625e39acc03ee362bb86cc9bfa6ad15e25c50889ae36e2889b3/ninja-1.13.2-py3-none-manylinux2014_aarch64.manylinux_2_17_aarch64.whl"
          sha="d775a5e43e9088f507a6250d57fcf5678eb31268c545feb5064ffeee33735622"
          ;;
        *) return 1 ;;
      esac
      ;;
    *) return 1 ;;
  esac

  local deps="native/build/.deps" archive="native/build/.deps/ninja-download.zip"
  mkdir -p "${deps}/ninja"
  download_file "${url}" "${archive}" "${sha}"
  unzip_to "${archive}" "${deps}/ninja"
  rm -f "${archive}"
  local bin
  bin="$(find "${deps}/ninja" -type f \( -name ninja -o -name ninja.exe \) 2>/dev/null | head -n 1)"
  [ -n "${bin}" ] || die "ninja archive extracted but no ninja executable was found under ${deps}/ninja"
  chmod +x "${bin}" 2>/dev/null || true
  export PATH="$(dirname "${bin}"):${PATH}"
  log "ninja installed for this run: ${bin}"
}

# ---------------------------------------------------------------------------
# ASCII scratch dir for the MinGW link workaround:
# MinGW ld cannot resolve `-L<path>` when the repository path contains non-ASCII
# characters (it fails with "cannot find -lchronos"). Copying the import library
# to an ASCII path and adding it through CGO_LDFLAGS fixes the link.
# ---------------------------------------------------------------------------
make_ascii_link_stage() {
  local base win cand
  for base in "${TMPDIR:-/tmp}" /c/Users/Public /c/Windows/Temp; do
    [ -d "${base}" ] || continue
    cand="$(mktemp -d "${base}/chronos-link.XXXXXX" 2>/dev/null || true)"
    [ -n "${cand}" ] || continue
    win="$(cygpath -w "${cand}" 2>/dev/null || true)"
    if [ -n "${win}" ] && path_is_ascii "${win}" && [ "${win#* }" = "${win}" ]; then
      LINK_STAGE="${cand}"
      LINK_STAGE_WIN="${win}"
      return 0
    fi
    rm -rf "${cand}" 2>/dev/null || true
  done
  return 1
}

# ---------------------------------------------------------------------------
# banner
# ---------------------------------------------------------------------------
log "Project Chronos build"
log "platform      : ${OS} ($(uname -m), uname=${UNAME_S})"
log "repository    : ${REPO_ROOT}"
log "profile       : ${PROFILE}"
log "output        : ${OUT}"
log "native type   : ${NATIVE_BUILD_TYPE}"
log "skip frontend : ${SKIP_FRONTEND}"
log "skip native   : ${SKIP_NATIVE}"
log "skip gui      : ${SKIP_GUI}"
log "clean         : ${CLEAN}"
log "autodep       : ${AUTODEP}"

# ---------------------------------------------------------------------------
# STEP: clean
# ---------------------------------------------------------------------------
step_begin "clean"
if [ "${CLEAN}" = "1" ]; then
  if [ -e "${OUT}" ]; then
    rm -rf "${OUT}"
    log "removed ${OUT}"
  else
    log "${OUT} does not exist (nothing to remove)"
  fi
  if [ "${SKIP_NATIVE}" = "0" ] && [ -e "native/build" ]; then
    rm -rf "native/build"
    log "removed native/build"
  fi
else
  log "clean skipped (pass --clean to remove ${OUT} and native/build)"
fi
step_end

# ---------------------------------------------------------------------------
# STEP: preflight
# ---------------------------------------------------------------------------
step_begin "preflight"

log "checking required tools (missing tools are reported with install hints)"

have go || die "go is not on PATH. Install Go 1.26+ from https://go.dev/dl/ (or 'winget install GoLang.Go' on Windows)."
GO_FULL="$(go version 2>/dev/null || true)"
[ -n "${GO_FULL}" ] || die "'go version' failed; check your Go installation (https://go.dev/dl/)."
# "go version go1.26.0 linux/amd64" -> "1.26.0" (also copes with "go1.26.0-devel").
GO_VER="${GO_FULL##*go}"
GO_VER="${GO_VER%%[!0-9.]*}"
[ -n "${GO_VER}" ] || die "cannot parse the Go version from '${GO_FULL}'"
log "go            : ${GO_FULL}"
GO_REQ="$(awk '$1 == "go" { print $2; exit }' go.mod 2>/dev/null || true)"
if [ -n "${GO_REQ}" ]; then
  log "go.mod needs  : go ${GO_REQ}"
  version_ge "${GO_VER}" "${GO_REQ}" \
    || die "Go ${GO_VER} is older than go.mod requires (go ${GO_REQ}); upgrade Go from https://go.dev/dl/"
fi

[ -f go.mod ] || die "not a Chronos checkout: go.mod missing in ${REPO_ROOT}"
[ -f native/CMakeLists.txt ] || die "not a Chronos checkout: native/CMakeLists.txt missing"
[ -f frontend/package.json ] || die "not a Chronos checkout: frontend/package.json missing"

# --- cgo compiler ----------------------------------------------------------
NEED_CC=0
if [ "${SKIP_NATIVE}" = "0" ]; then NEED_CC=1; fi
if [ -f "native/build/${SHLIB}" ]; then NEED_CC=1; fi
if [ "${SKIP_GUI}" = "0" ] && [ "${OS}" = "linux" ]; then NEED_CC=1; fi
if [ "${NEED_CC}" = "1" ]; then
  GO_CC="$(go env CC 2>/dev/null || true)"
  GO_CXX="$(go env CXX 2>/dev/null || true)"
  [ -n "${GO_CC}" ] || die "go env CC is empty; cgo needs a C compiler"
  have "${GO_CC}" || die "cgo C compiler '${GO_CC}' not found on PATH.
  Windows: install MSYS2 UCRT64 and add C:\\msys64\\ucrt64\\bin to PATH
           (pacman -S mingw-w64-ucrt-x86_64-gcc) or use winlibs MinGW-w64.
  Linux  : sudo apt-get install build-essential g++
  macOS  : install Xcode command line tools: xcode-select --install"
  if [ -n "${GO_CXX}" ]; then have "${GO_CXX}" || die "cgo C++ compiler '${GO_CXX}' not found on PATH (needed by the native engine build)"; fi
  export CGO_ENABLED=1
  log "cgo compiler  : ${GO_CC} -> $(command -v "${GO_CC}")"
else
  log "cgo compiler  : not required for the selected steps"
fi

# --- native toolchain ------------------------------------------------------
CMAKE_MIN="3.16"
if [ "${SKIP_NATIVE}" = "0" ]; then
  if ! have cmake; then
    if [ "${AUTODEP}" = "1" ]; then
      autodep_cmake || log "AUTODEP: no pinned CMake available for this platform"
    fi
  fi
  have cmake || die "cmake not found on PATH (minimum ${CMAKE_MIN}).
  Windows: winget install Kitware.CMake, or set CHRONOS_AUTODEP=1 to fetch a pinned copy.
  Linux  : sudo apt-get install cmake
  macOS  : brew install cmake"
  CMAKE_VERSION_LINE="$(cmake --version 2>/dev/null | head -n 1)"
  CMAKE_VER="$(printf '%s' "${CMAKE_VERSION_LINE}" | awk '{print $3}')"
  log "cmake         : ${CMAKE_VERSION_LINE}"
  if [ -n "${CMAKE_VER}" ]; then
    version_ge "${CMAKE_VER}" "${CMAKE_MIN}" || die "cmake ${CMAKE_VER} is too old (need ${CMAKE_MIN}+)"
  fi

  if ! have ninja; then
    if [ "${AUTODEP}" = "1" ]; then
      autodep_ninja || log "AUTODEP: no pinned Ninja available for this platform"
    fi
  fi
  have ninja || die "ninja not found on PATH.
  Windows: winget install Ninja-build.Ninja, or set CHRONOS_AUTODEP=1 to fetch a pinned copy.
  Linux  : sudo apt-get install ninja-build
  macOS  : brew install ninja"
  log "ninja         : ninja $(ninja --version 2>/dev/null)"
fi

# --- frontend toolchain ----------------------------------------------------
if [ "${SKIP_FRONTEND}" = "0" ]; then
  have node || die "node is not on PATH. Install Node 22+ from https://nodejs.org/ (or 'winget install OpenJS.NodeJS.LTS' on Windows)."
  log "node          : node $(node --version 2>/dev/null)"
  [ -f frontend/pnpm-lock.yaml ] || die "frontend/pnpm-lock.yaml is missing; 'pnpm install --frozen-lockfile' requires it (restore it from git)."
  if have pnpm; then
    PNPM_MODE="direct"
  elif [ "${OS}" = "windows" ] && have pnpm.cmd; then
    PNPM_MODE="cmd"
  else
    die "pnpm is not on PATH. Install it with 'corepack enable pnpm' (Node 16.10+) or 'npm install -g pnpm@11.28.5'."
  fi
  if [ "${PNPM_MODE}" = "cmd" ]; then
    PNPM_VER="$(cmd //c pnpm --version 2>/dev/null || true)"
  else
    PNPM_VER="$(pnpm --version 2>/dev/null || true)"
  fi
  log "pnpm          : pnpm ${PNPM_VER:-unknown}"
  PM_SPEC="$(sed -n 's/.*"packageManager"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' frontend/package.json | head -n 1)"
  if [ -n "${PM_SPEC}" ] && [ -n "${PNPM_VER}" ] && [ "${PM_SPEC}" != "pnpm@${PNPM_VER}" ]; then
    log "WARNING: package.json pins '${PM_SPEC}' but the active pnpm is ${PNPM_VER};"
    log "WARNING: 'pnpm install --frozen-lockfile' may fail. Align the pnpm version if it does."
  fi
fi

# --- GUI dependencies (Linux) ---------------------------------------------
if [ "${SKIP_GUI}" = "0" ] && [ "${OS}" = "linux" ]; then
  have pkg-config || die "pkg-config not found; the Linux GUI build needs GTK3 + WebKit2GTK dev packages.
  sudo apt-get install -y pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev"
  pkg-config --exists gtk+-3.0 || die "GTK3 development headers not found (needed by the Linux GUI build).
  sudo apt-get install -y libgtk-3-dev"
  pkg-config --exists webkit2gtk-4.1 || die "WebKit2GTK 4.1 development headers not found (needed by the Linux GUI build).
  sudo apt-get install -y libwebkit2gtk-4.1-dev   # Ubuntu 24.04+ (the wails tag is webkit2_41)"
fi

# --- optional wails --------------------------------------------------------
if have wails; then
  log "wails         : $(command -v wails)"
else
  log "wails         : not found (the GUI step will use a plain 'go build' fallback)"
fi

step_end

# ---------------------------------------------------------------------------
# STEP: frontend
# ---------------------------------------------------------------------------
if [ "${SKIP_FRONTEND}" = "0" ]; then
  step_begin "frontend (pnpm install + build)"
  if [ ! -d frontend/node_modules ]; then
    log "installing frontend dependencies (node_modules missing)"
    ( cd frontend && pnpm_run install --frozen-lockfile )
  else
    log "frontend/node_modules already present; skipping install"
  fi
  ( cd frontend && pnpm_run build )
  [ -f frontend/dist/index.html ] \
    || die "frontend build did not produce frontend/dist/index.html; check the pnpm build output above"
  WEB_AVAILABLE=1
  log "artifact: frontend/dist/index.html"
  step_end
else
  log "frontend build skipped (--skip-frontend)"
fi

# ---------------------------------------------------------------------------
# STEP: native
# ---------------------------------------------------------------------------
NATIVE_LIB="native/build/${SHLIB}"

build_native_variant() {
  local dir="$1" with_ov="$2"
  local -a args
  # CMAKE_SUPPRESS_REGENERATION=ON: this script always runs the configure step
  # explicitly, so in-build CMake regeneration is redundant. Suppressing it also
  # prevents ninja's "manifest still dirty after 100 tries" loop when the
  # checkout contains files dated in the future (e.g. after a clock correction).
  args=(-S native -B "${dir}" -G Ninja -DCMAKE_BUILD_TYPE="${NATIVE_BUILD_TYPE}"
        -DCHRONOS_BUILD_TESTS="${BUILD_TESTS_OPT}" -DCMAKE_SUPPRESS_REGENERATION=ON)
  if [ "${with_ov}" = "1" ]; then
    args+=(-DCHRONOS_WITH_OPENVINO=ON -DOpenVINO_DIR="${OpenVINO_DIR}")
  fi
  if [ "${OS}" = "windows" ] && [ -z "${CC:-}" ] && [ -z "${CXX:-}" ]; then
    # Force the MinGW toolchain when CMake could otherwise pick MSVC from PATH.
    args+=(-DCMAKE_C_COMPILER=gcc -DCMAKE_CXX_COMPILER=g++)
  fi
  log "cmake ${args[*]}"
  cmake "${args[@]}"
  cmake --build "${dir}" --parallel
  [ -f "${dir}/${SHLIB}" ] || die "native build finished but ${dir}/${SHLIB} is missing; check the cmake output above"
}

if [ "${SKIP_NATIVE}" = "0" ]; then
  step_begin "native engine (CMake + Ninja, profile=${PROFILE})"
  if [ "${PROFILE}" = "openvino" ]; then
    if [ -z "${OpenVINO_DIR:-}" ]; then
      die "--profile=openvino requires OpenVINO_DIR (directory containing OpenVINOConfig.cmake), e.g.
  OpenVINO_DIR=/opt/intel/openvino/runtime/cmake bash scripts/build.sh --profile=openvino"
    fi
    [ -d "${OpenVINO_DIR}" ] || die "OpenVINO_DIR does not exist: ${OpenVINO_DIR}"
    log "OpenVINO_DIR  : ${OpenVINO_DIR}"
  fi
  if [ "${PROFILE}" = "all" ] && [ -n "${OpenVINO_DIR:-}" ]; then
    log "OpenVINO_DIR is set: the default profile will additionally build the OpenVINO variant"
  fi

  build_native_variant "native/build" "$([ "${PROFILE}" = "openvino" ] && echo 1 || echo 0)"

  if [ "${PROFILE}" = "all" ] && [ -n "${OpenVINO_DIR:-}" ]; then
    OV_VARIANT_DIR="native/build/openvino"
    log "building optional OpenVINO variant into ${OV_VARIANT_DIR}"
    build_native_variant "${OV_VARIANT_DIR}" 1
  fi

  log "artifact: ${NATIVE_LIB}"
  step_end
else
  log "native build skipped (--skip-native)"
fi

NATIVE_AVAILABLE=0
if [ -f "${NATIVE_LIB}" ]; then NATIVE_AVAILABLE=1; fi
if [ "${NATIVE_AVAILABLE}" = "0" ]; then
  if [ "${SKIP_NATIVE}" = "0" ]; then
    die "expected native library not found: ${NATIVE_LIB}"
  fi
  log "WARNING: ${NATIVE_LIB} not found and --skip-native was passed."
  log "WARNING: the Go binaries will be built without the native engine (pure Go fallback)."
fi

# --- non-ASCII repository path link workaround (Windows only) --------------
if [ "${NATIVE_AVAILABLE}" = "1" ] && [ "${OS}" = "windows" ] && ! path_is_ascii "${REPO_ROOT}"; then
  log "repository path contains non-ASCII characters; enabling the MinGW link-directory workaround"
  have cygpath || die "cygpath is required for the non-ASCII path link workaround (run inside Git Bash)"
  make_ascii_link_stage || die "could not create an ASCII scratch directory for the MinGW link workaround.
Set TMPDIR to an ASCII path and retry (the repository path contains non-ASCII characters)."
  cp -f "native/build/libchronos.dll.a" "${LINK_STAGE}/" 2>/dev/null \
    || die "expected import library native/build/libchronos.dll.a is missing"
  cp -f "native/build/chronos.dll" "${LINK_STAGE}/" 2>/dev/null || true
  export CGO_LDFLAGS="-L${LINK_STAGE_WIN}"
  log "CGO_LDFLAGS  : ${CGO_LDFLAGS}"
fi

# ---------------------------------------------------------------------------
# STEP: CLI
# ---------------------------------------------------------------------------
step_begin "CLI (cmd/chronos)"
mkdir -p "${OUT}/bin"
if [ "${NATIVE_AVAILABLE}" = "1" ]; then
  log "building with -tags chronos_native (native engine linked)"
  go build -tags chronos_native -o "${OUT}/bin/chronos${EXE}" ./cmd/chronos
else
  log "building without -tags chronos_native (pure Go fallback engine)"
  go build -o "${OUT}/bin/chronos${EXE}" ./cmd/chronos
fi
cp -f "${OUT}/bin/chronos${EXE}" "${OUT}/bin/chronos-cli${EXE}"
log "artifact: ${OUT}/bin/chronos${EXE}"
log "artifact: ${OUT}/bin/chronos-cli${EXE}"
step_end

# ---------------------------------------------------------------------------
# STEP: GUI
# ---------------------------------------------------------------------------
GUI_BIN=""
if [ "${SKIP_GUI}" = "0" ]; then
  step_begin "desktop GUI (Wails or plain go build)"
  GUI_TAGS=""
  if [ "${NATIVE_AVAILABLE}" = "1" ]; then GUI_TAGS="chronos_native"; fi
  if [ "${OS}" = "linux" ]; then
    # Ubuntu 24.04 ships only webkit2gtk-4.1, so the wails binding tag is required.
    GUI_TAGS="${GUI_TAGS:+${GUI_TAGS} }webkit2_41"
  fi

  USE_WAILS=0
  if [ "${CHRONOS_GUI:-auto}" != "plain" ] && have wails; then USE_WAILS=1; fi

  if [ "${USE_WAILS}" = "1" ]; then
    WAILS_OS="${OS}"
    WAILS_ARCH="$(go env GOARCH 2>/dev/null || echo amd64)"
    WAILS_ARGS=(build -s -skipbindings -platform "${WAILS_OS}/${WAILS_ARCH}")
    if [ -n "${GUI_TAGS}" ]; then WAILS_ARGS+=(-tags "${GUI_TAGS}"); fi
    if [ "${CLEAN}" = "1" ]; then WAILS_ARGS+=(-clean); fi
    if [ "${DEBUG}" = "1" ]; then WAILS_ARGS+=(-debug); fi
    log "wails ${WAILS_ARGS[*]}"
    wails "${WAILS_ARGS[@]}" \
      || die "wails build failed (see docs/CI.md section 6 'Wails desktop build'). Use --skip-gui to build only the CLI."
    if [ ! -f "build/bin/chronos${EXE}" ]; then
      err "wails build succeeded but build/bin/chronos${EXE} was not found; build/bin contents:"
      ls -1 build/bin 2>/dev/null >&2 || true
      die "unexpected wails output location"
    fi
    cp -f "build/bin/chronos${EXE}" "${OUT}/bin/chronos-desktop${EXE}"
    GUI_BIN="${OUT}/bin/chronos-desktop${EXE}"
    log "artifact: ${GUI_BIN} (wails)"
  else
    log "wails CLI not used (not installed or CHRONOS_GUI=plain); using plain 'go build'"
    log "note: this fallback builds the same Wails app without the Wails packaging step"
    if [ "${OS}" = "linux" ]; then
      log "note: the Linux GUI needs GTK3 + WebKit2GTK 4.1 runtime/dev packages (docs/CI.md)"
    fi
    if [ -n "${GUI_TAGS}" ]; then
      go build -tags "${GUI_TAGS}" -o "${OUT}/bin/chronos-desktop${EXE}" .
    else
      go build -o "${OUT}/bin/chronos-desktop${EXE}" .
    fi
    GUI_BIN="${OUT}/bin/chronos-desktop${EXE}"
    log "artifact: ${GUI_BIN} (plain go build)"
  fi
  step_end
else
  log "desktop GUI skipped (--skip-gui)"
fi

# ---------------------------------------------------------------------------
# STEP: assemble
# ---------------------------------------------------------------------------
step_begin "assemble dist tree"
mkdir -p "${OUT}/bin" "${OUT}/libs"

# native library: next to the binaries and in libs/
if [ "${NATIVE_AVAILABLE}" = "1" ]; then
  cp -f "${NATIVE_LIB}" "${OUT}/bin/${SHLIB}"
  cp -f "${NATIVE_LIB}" "${OUT}/libs/${SHLIB}"
  if [ "${OS}" = "linux" ]; then
    # The cgo link bakes -Wl,-rpath,$ORIGIN/libs; with binaries in bin/ this
    # resolves to bin/libs, so the CLI also runs without LD_LIBRARY_PATH.
    mkdir -p "${OUT}/bin/libs"
    cp -f "${NATIVE_LIB}" "${OUT}/bin/libs/${SHLIB}"
  fi
  log "artifact: ${OUT}/bin/${SHLIB}"
  log "artifact: ${OUT}/libs/${SHLIB}"
fi

# optional OpenVINO variant (profile=all + OpenVINO_DIR)
if [ -n "${OV_VARIANT_DIR}" ] && [ -f "${OV_VARIANT_DIR}/${SHLIB}" ]; then
  mkdir -p "${OUT}/libs/optional/openvino"
  cp -f "${OV_VARIANT_DIR}/${SHLIB}" "${OUT}/libs/optional/openvino/${SHLIB}"
  log "artifact: ${OUT}/libs/optional/openvino/${SHLIB}"
fi

# web assets
rm -rf "${OUT}/web"
mkdir -p "${OUT}/web"
if [ -f "frontend/dist/index.html" ]; then
  cp -R "frontend/dist/." "${OUT}/web/"
  WEB_AVAILABLE=1
  log "artifact: ${OUT}/web/index.html"
else
  log "WARNING: frontend/dist/index.html not found; ${OUT}/web will be empty."
  log "WARNING: browser mode needs UI assets - run without --skip-frontend, or pass '--assets DIR' to 'chronos serve'."
fi

# README.txt (ASCII only)
BUILT_AT="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
README_FILE="${OUT}/README.txt"
{
  printf 'Project Chronos - local build output\n'
  printf '=====================================\n'
  printf '\n'
  printf 'Generated : %s\n' "${BUILT_AT}"
  printf 'Script    : scripts/build.sh (%s)\n' "${OS}"
  printf 'Profile   : %s\n' "${PROFILE}"
  printf 'Native    : %s build\n' "${NATIVE_BUILD_TYPE}"
  printf '\n'
  printf 'Layout\n'
  if [ -n "${GUI_BIN}" ]; then
    printf '  bin/chronos-desktop      Desktop GUI app. UI assets are embedded.\n'
  else
    printf '  bin/chronos-desktop      NOT built in this run (--skip-gui)\n'
  fi
  printf '  bin/chronos              Headless CLI: upscale, info, models, inhibit, serve, bench.\n'
  printf '  bin/chronos-cli          Same CLI binary (release/package naming).\n'
  if [ "${NATIVE_AVAILABLE}" = "1" ]; then
    printf '  bin/%s           Native engine - loaded next to the binaries.\n' "${SHLIB}"
    printf '  libs/%s          Native engine library (copy).\n' "${SHLIB}"
  fi
  if [ -f "${OUT}/web/index.html" ]; then
    printf '  web/                     Built frontend (index.html + assets/) for browser mode.\n'
  else
    printf '  web/                     NOT populated - run without --skip-frontend.\n'
  fi
  printf '\n'
} > "${README_FILE}"

if [ -n "${GUI_BIN}" ]; then
  cat >> "${README_FILE}" <<'README'
Run the desktop app
  bin/chronos-desktop        (bin\chronos-desktop.exe on Windows)
  If WebView2 (Windows) or WebKitGTK (Linux) is missing, the desktop binary
  automatically falls back to browser mode (it then serves the embedded UI).

README
fi

cat >> "${README_FILE}" <<'README'
Run the CLI
  bin/chronos info            (bin\chronos.exe info on Windows)
  bin/chronos upscale -i in.png -o out.png -s 2 --no-model

Browser mode (no WebView needed)
  Open a shell in THIS directory first, so the CLI can find web/:
    bin/chronos serve
  The CLI serves the UI on 127.0.0.1 (random free port) and opens your default
  browser. Useful options:  serve --port 8787   serve --no-open
  If you start it from another directory, pass the assets directory explicitly:
    bin/chronos serve --assets web
  On Linux the CLI has an rpath for bin/libs, so no LD_LIBRARY_PATH is needed
  when the dist tree is kept intact. On Windows chronos.dll is loaded from the
  binary directory. If you move binaries around, keep the native library next to
  the CLI, or set LD_LIBRARY_PATH (Linux) to the directory containing it.

Notes
  - Models are downloaded on demand:  chronos models list
                                      chronos models pull MODEL_ID
  - The CLI and the desktop app share the same data/model directories.
  - This tree is produced by the repository build script. Re-running the
    script is idempotent and only overwrites files inside the output tree
    (plus the native CMake build tree).
README

if [ "${PROFILE}" = "openvino" ]; then
  cat >> "${README_FILE}" <<'README'

OpenVINO profile
  This engine was built with the OpenVINO backend. The OpenVINO runtime
  (openvino.dll / libopenvino.so) must be available at run time: add its
  runtime directory to PATH (Windows) or LD_LIBRARY_PATH (Linux), or run
  'setupvars' from the OpenVINO distribution.
README
fi

if [ -f "${OUT}/libs/optional/openvino/${SHLIB}" ]; then
  cat >> "${README_FILE}" <<'README'

Optional OpenVINO variant
  libs/optional/openvino/ contains a second native library built with
  -DCHRONOS_WITH_OPENVINO=ON. The default library in bin/ and libs/ is the
  CPU/stub build. To switch to the OpenVINO build, copy the optional library
  over bin/ and libs/ (keep the file name) and provide the OpenVINO runtime.
README
fi

# self-check: the CLI must start and report an engine backend
if [ -x "${OUT}/bin/chronos${EXE}" ] || [ -f "${OUT}/bin/chronos${EXE}" ]; then
  SELFCHECK_RC=0
  SELFCHECK_INFO=""
  if [ "${OS}" = "darwin" ]; then
    # macOS has no $ORIGIN rpath in the cgo flags, so point dyld at the tree.
    SELFCHECK_INFO="$(DYLD_LIBRARY_PATH="${OUT}/bin:${OUT}/libs" \
      "${OUT}/bin/chronos${EXE}" info 2>&1)" || SELFCHECK_RC=$?
  else
    SELFCHECK_INFO="$("${OUT}/bin/chronos${EXE}" info 2>&1)" || SELFCHECK_RC=$?
  fi
  if [ "${SELFCHECK_RC}" -ne 0 ]; then
    err "self-check failed: '${OUT}/bin/chronos${EXE} info' exited with ${SELFCHECK_RC}"
    printf '%s\n' "${SELFCHECK_INFO}" >&2
    die "the CLI does not start from the dist tree (missing native library?)"
  fi
  log "self-check: '${OUT}/bin/chronos${EXE} info' succeeded"
  printf '%s\n' "${SELFCHECK_INFO}" | sed -n '1,5p' | while IFS= read -r line; do log "  ${line}"; done
fi

log "dist tree:"
( cd "${OUT}" && find . -type f -print | sort | sed 's|^\./|  |' )
if have du; then
  log "dist size: $(du -sh "${OUT}" 2>/dev/null | awk '{print $1}')"
fi
step_end

# ---------------------------------------------------------------------------
# summary
# ---------------------------------------------------------------------------
TOTAL_ELAPSED=$((SECONDS - TOTAL_START))
printf '\n[build] ==================================================\n'
log "BUILD OK in ${TOTAL_ELAPSED}s"
log "output dir : ${OUT}"
log "binaries   : ${OUT}/bin"
if [ -n "${GUI_BIN}" ]; then log "gui        : ${GUI_BIN}"; fi
log "web assets : ${OUT}/web"
log "native lib : ${OUT}/libs"
printf '[build] ==================================================\n'
