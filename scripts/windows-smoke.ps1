# Project Chronos - headless CLI smoke test (Windows / PowerShell 5.1+).
#
# Usage:
#   pwsh -NoProfile -File scripts/windows-smoke.ps1 -CliPath .\chronos.exe
#
# Same checks as scripts/ubuntu-smoke.sh:
#   1. `chronos upscale -i in.png -o out.png -s 2 --no-model` produces a valid
#      PNG whose IHDR is exactly 128x96 (64x48 input, 2x).
#   2. `chronos inhibit status` exits 0.
#   3. `chronos info` prints an engine/backend line.
#
# The PNG is generated with Python's standard library (zlib + struct) because
# Windows runners always ship python on PATH; no System.Drawing / GDI+ needed.
# The script intentionally does not fix the working directory: run it from the
# repository root (CI does).

[CmdletBinding()]
param(
    [string]$CliPath = ".\chronos.exe",
    [string]$Python = ""
)

$ErrorActionPreference = "Stop"
try {
    [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
    $env:PYTHONUTF8 = "1"
} catch {
    # Non-fatal: console encoding only.
}

function Write-Log([string]$Message) {
    Write-Host "[smoke] $Message"
}

function Fail([string]$Message) {
    Write-Host "[smoke] FAIL: $Message" -ForegroundColor Red
    exit 1
}

$repoRoot = Split-Path -Parent $PSScriptRoot
$cliFull = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($CliPath)

if (-not (Test-Path -LiteralPath $cliFull)) {
    Write-Log "CLI '$CliPath' not found, building it from $repoRoot"
    if (-not (Test-Path -LiteralPath (Join-Path $repoRoot "native/include"))) {
        Fail "not a Chronos checkout: $repoRoot"
    }
    Push-Location $repoRoot
    try {
        $env:CGO_ENABLED = "1"
        & go build -tags chronos_native -o $cliFull ./cmd/chronos
        if ($LASTEXITCODE -ne 0) { Fail "go build exited with $LASTEXITCODE" }
    } finally {
        Pop-Location
    }
}
Write-Log "using CLI: $cliFull"

function Resolve-Python([string]$Preferred) {
    if ($Preferred) {
        $cmd = Get-Command $Preferred -ErrorAction SilentlyContinue
        if ($cmd) { return $cmd.Source }
        Fail "requested Python '$Preferred' not found on PATH"
    }
    foreach ($candidate in @("python", "python3")) {
        $cmd = Get-Command $candidate -ErrorAction SilentlyContinue
        if ($cmd) { return $cmd.Source }
    }
    Fail "python (or python3) is required for PNG generation/assertions"
}

$pythonExe = Resolve-Python $Python
Write-Log "using python: $pythonExe"

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("chronos-smoke-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $work | Out-Null
$inPath = Join-Path $work "in.png"
$outPath = Join-Path $work "out.png"

try {
    # --- 1. generate a 64x48 RGB PNG (stdlib only) -------------------------
    # The helper code is written to a file instead of being passed to `python -c`:
    # Windows PowerShell 5.1 drops embedded double quotes in native arguments.
    $genScript = Join-Path $work "gen_png.py"
    $pngCode = @'
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


def chunk(tag, data):
    crc = zlib.crc32(tag + data) & 0xFFFFFFFF
    return struct.pack(">I", len(data)) + tag + data + struct.pack(">I", crc)


png = b"\x89PNG\r\n\x1a\n"
png += chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
png += chunk(b"IDAT", zlib.compress(raw, 9))
png += chunk(b"IEND", b"")
with open(path, "wb") as fh:
    fh.write(png)
print("[smoke] generated %s (%dx%d, %d bytes)" % (path, width, height, len(png)))
'@
    [System.IO.File]::WriteAllText($genScript, $pngCode, (New-Object System.Text.UTF8Encoding($false)))

    & $pythonExe $genScript $inPath
    if ($LASTEXITCODE -ne 0) { Fail "PNG generation exited with $LASTEXITCODE" }

    # --- 2. upscale with the stub backend (no model download) -------------
    Write-Log "running: chronos upscale -i in.png -o out.png -s 2 --no-model"
    & $cliFull upscale -i $inPath -o $outPath -s 2 --no-model
    if ($LASTEXITCODE -ne 0) { Fail "upscale exited with $LASTEXITCODE" }

    if (-not (Test-Path -LiteralPath $outPath)) {
        # tolerate an implementation that treats -o as an output directory.
        $alt = Get-ChildItem -Path $work -Recurse -Filter "*_chronos_x2.png" -File -ErrorAction SilentlyContinue |
            Select-Object -First 1
        if (-not $alt) { Fail "upscale produced no output file" }
        $outPath = $alt.FullName
        Write-Log "output found at $outPath"
    }

    $assertScript = Join-Path $work "assert_png.py"
    $assertCode = @'
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
'@
    [System.IO.File]::WriteAllText($assertScript, $assertCode, (New-Object System.Text.UTF8Encoding($false)))

    & $pythonExe $assertScript $outPath
    if ($LASTEXITCODE -ne 0) { Fail "output dimension assertion failed" }
    Write-Log "PASS upscale 64x48 -> 128x96 (--no-model)"

    # --- 3. inhibit status must be callable headless ----------------------
    Write-Log "running: chronos inhibit status"
    & $cliFull inhibit status
    if ($LASTEXITCODE -ne 0) { Fail "inhibit status exited with $LASTEXITCODE" }
    Write-Log "PASS inhibit status"

    # --- 4. info must describe the engine backend ------------------------
    Write-Log "running: chronos info"
    $info = (& $cliFull info 2>&1 | Out-String)
    if ($LASTEXITCODE -ne 0) { Fail "info exited with $LASTEXITCODE" }
    Write-Host $info
    if ($info -notmatch "backend") { Fail "info did not print a backend line" }
    Write-Log "PASS info reports an engine backend"

    Write-Host ""
    Write-Host "[smoke] ALL CHECKS PASSED"
} finally {
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}
