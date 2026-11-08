#!/usr/bin/env bash
#
# Project Chronos - browser-mode (headless HTTP) smoke test.
#
# Usage:
#   bash scripts/browser-smoke.sh ./chronos
#   CHRONOS_BIN=build/bin/chronos-cli bash scripts/browser-smoke.sh
#
# What it verifies (no display server, no WebView needed):
#   1. `chronos serve --no-open --port 0 --json` starts on a random loopback port
#      and prints one JSON line containing "url":"http://127.0.0.1:<port>/".
#   2. POST /api/GetAppInfo returns 200 and reports "mode":"browser".
#   3. POST /api/ListDirectory returns HTTP 200.
#   4. The server process can be stopped cleanly.
#
# Security model note: the server binds 127.0.0.1 only, sets a SameSite session
# cookie and checks the Origin header. The script bootstraps the cookie with
# `curl -c` against "/" and sends it (plus Origin) with every API call; that also
# documents the manual CLI debugging flow.
set -euo pipefail

BIN="${1:-${CHRONOS_BIN:-./chronos}}"
log() { printf '[browser-smoke] %s\n' "$*"; }
fail() { printf '[browser-smoke] FAIL: %s\n' "$*" >&2; exit 1; }

[ -f "${BIN}" ] || fail "CLI not found: ${BIN}"
[ -x "${BIN}" ] || chmod +x "${BIN}" 2>/dev/null || fail "cannot execute ${BIN}"
BIN="$(cd "$(dirname "${BIN}")" && pwd)/$(basename "${BIN}")"
command -v curl >/dev/null 2>&1 || fail "curl is required"

WORK="$(mktemp -d)"
SERVER_PID=""

# The server refuses to start without UI assets containing index.html. Prefer an
# explicit directory (CHRONOS_ASSETS_DIR), then a real built frontend in the
# checkout (CI downloads the frontend-dist artifact before this step), and
# finally a tiny fixture so the HTTP surface can be tested in isolation.
ASSETS="${CHRONOS_ASSETS_DIR:-}"
if [ -z "${ASSETS}" ] && [ -f "frontend/dist/index.html" ]; then
  ASSETS="frontend/dist"
  log "using built frontend assets from frontend/dist"
fi
if [ -z "${ASSETS}" ]; then
  mkdir -p "${WORK}/assets"
  cat > "${WORK}/assets/index.html" <<'HTML'
<!doctype html>
<html><head><title>Chronos browser smoke</title></head>
<body>chronos browser smoke fixture</body></html>
HTML
  ASSETS="${WORK}/assets"
  log "no frontend/dist found; using a fixture index.html"
fi
[ -f "${ASSETS}/index.html" ] || fail "assets directory must contain index.html: ${ASSETS}"

cleanup() {
  if [ -n "${SERVER_PID}" ] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    for _ in $(seq 1 25); do
      kill -0 "${SERVER_PID}" 2>/dev/null || break
      sleep 0.2
    done
    kill -9 "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${WORK}"
}
trap cleanup EXIT

log "starting: ${BIN} serve --no-open --port 0 --json --assets ${ASSETS}"
"${BIN}" serve --no-open --port 0 --json --assets "${ASSETS}" >"${WORK}/serve.log" 2>&1 &
SERVER_PID=$!

# --- 1. wait for the JSON url line (up to 20 s) -----------------------------
URL=""
for _ in $(seq 1 100); do
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    log "serve log:"
    cat "${WORK}/serve.log" >&2 || true
    fail "serve exited before printing a URL"
  fi
  URL="$(sed -n 's/.*"url"[[:space:]]*:[[:space:]]*"\(http:\/\/127\.0\.0\.1:[0-9][0-9]*\/\)".*/\1/p' \
    "${WORK}/serve.log" | head -n 1 || true)"
  [ -n "${URL}" ] && break
  sleep 0.2
done
if [ -z "${URL}" ]; then
  log "serve log:"
  cat "${WORK}/serve.log" >&2 || true
  fail "no \"url\":\"http://127.0.0.1:<port>/\" line in serve --json output within 20s"
fi
BASE="${URL%/}"
PORT="${BASE##*:}"
log "server ready: ${URL} (pid ${SERVER_PID})"

# --- 2. bootstrap the session cookie (GET /) --------------------------------
# GET / sets the SameSite session cookie that every /api call requires.
COOKIE="${WORK}/cookies.txt"
ROOT_CODE="$(curl -sS -m 10 -o "${WORK}/index.html" -w '%{http_code}' \
  -c "${COOKIE}" "${URL}" || true)"
[ "${ROOT_CODE}" = "200" ] || fail "GET / returned HTTP ${ROOT_CODE}"
grep -q '__CHRONOS_HTTP__' "${WORK}/index.html" \
  || fail "GET / did not inject window.__CHRONOS_HTTP__=true into index.html"
log "PASS GET / serves the UI and injects the browser transport marker"

api_post() {
  local method="$1" args="$2" out="$3" code
  code="$(curl -sS -m 15 -o "${out}" -w '%{http_code}' \
    -c "${COOKIE}" -b "${COOKIE}" \
    -H "Origin: ${BASE}" \
    -H "Content-Type: application/json" \
    --data "{\"args\":${args}}" \
    "${BASE}/api/${method}" || true)"
  printf '%s' "${code}"
}

api_get() {
  local method="$1" out="$2" code
  code="$(curl -sS -m 15 -o "${out}" -w '%{http_code}' \
    -c "${COOKIE}" -b "${COOKIE}" \
    -H "Origin: ${BASE}" \
    "${BASE}/api/${method}" || true)"
  printf '%s' "${code}"
}

# --- 3. GetAppInfo must report browser mode --------------------------------
CODE="$(api_post GetAppInfo '[]' "${WORK}/getappinfo.json")"
if [ "${CODE}" != "200" ]; then
  log "POST /api/GetAppInfo -> HTTP ${CODE}, retrying with GET"
  CODE="$(api_get GetAppInfo "${WORK}/getappinfo.json")"
fi
if [ "${CODE}" != "200" ]; then
  cat "${WORK}/getappinfo.json" >&2 2>/dev/null || true
  fail "GET/POST /api/GetAppInfo returned HTTP ${CODE}"
fi
grep -Eq '"mode"[[:space:]]*:[[:space:]]*"browser"' "${WORK}/getappinfo.json" \
  || fail "AppInfo.mode is not \"browser\": $(head -c 400 "${WORK}/getappinfo.json" 2>/dev/null)"
log "PASS /api/GetAppInfo mode=browser"

# --- 4. ListDirectory must answer over HTTP --------------------------------
CODE=""
for args in '[""]' '[]' '["/"]'; do
  CODE="$(api_post ListDirectory "${args}" "${WORK}/listdir.json")"
  if [ "${CODE}" = "200" ]; then
    break
  fi
  log "POST /api/ListDirectory args=${args} -> HTTP ${CODE}"
done
[ "${CODE}" = "200" ] || fail "POST /api/ListDirectory returned HTTP ${CODE}"
grep -q '"entries"' "${WORK}/listdir.json" 2>/dev/null \
  || log "note: ListDirectory body has no \"entries\" key (shape not asserted)"
log "PASS /api/ListDirectory HTTP 200"

# --- 5. clean shutdown ------------------------------------------------------
kill "${SERVER_PID}" 2>/dev/null || true
for _ in $(seq 1 25); do
  kill -0 "${SERVER_PID}" 2>/dev/null || break
  sleep 0.2
done
if kill -0 "${SERVER_PID}" 2>/dev/null; then
  fail "server did not stop after SIGTERM"
fi
wait "${SERVER_PID}" 2>/dev/null || true
SERVER_PID=""
log "PASS server stopped"

printf '\n[browser-smoke] ALL CHECKS PASSED\n'
