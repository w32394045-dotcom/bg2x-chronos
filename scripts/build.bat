@echo off
rem ===========================================================================
rem Project Chronos - one-shot build script (Windows cmd).
rem
rem Builds the whole product and assembles a ready-to-run dist tree:
rem   frontend (Vue 3 + Vite) -> native C++ engine (CMake + Ninja) -> Go CLI
rem   (headless / browser mode) -> optional desktop GUI (Wails, or a plain Go
rem   build when the Wails CLI is not installed) -> <out>.
rem
rem Usage:
rem   scripts\build.bat [flags]
rem   scripts\build.bat --help
rem
rem Flags:
rem   --profile=cpu^|openvino^|all   native engine profile, default: all
rem                                cpu      : stub/CPU engine only, always buildable
rem                                openvino : OpenVINO backend, requires OpenVINO_DIR
rem                                all      : CPU engine, plus the OpenVINO variant
rem                                           under libs\optional\openvino when
rem                                           OpenVINO_DIR is set
rem   --skip-frontend              do not run pnpm install / pnpm build
rem   --skip-native                do not run CMake; reuse native\build if present,
rem                                otherwise build the pure-Go fallback engine
rem   --skip-gui                   do not build the desktop GUI
rem   --debug                      Debug native build, and wails -debug
rem   --clean                      remove <out> and native\build before building
rem   --out=DIR                    output directory, default: build\dist
rem   -h, --help                   print this help
rem
rem Environment:
rem   CHRONOS_AUTODEP=1            if cmake/ninja are missing, download pinned
rem                                copies into native\build\.deps
rem                                (CMake 3.31.6 from cmake.org and the Ninja
rem                                1.13.2 wheel from PyPI, both sha256 pinned).
rem                                This NEVER downloads a compiler, Node or pnpm.
rem   CHRONOS_BUILD_TESTS=1        build the native ctest suite, default: off
rem   CHRONOS_GUI=plain            force the plain go build GUI path even when
rem                                the wails CLI is installed
rem   OpenVINO_DIR=DIR             directory containing OpenVINOConfig.cmake,
rem                                required by --profile=openvino
rem
rem Output layout (default build\dist):
rem   README.txt
rem   bin\chronos.exe              headless CLI: upscale/info/models/serve/bench
rem   bin\chronos-cli.exe          same CLI, release/package naming
rem   bin\chronos-desktop.exe      desktop GUI, when not --skip-gui
rem   bin\chronos.dll              native engine, next to the executables
rem   libs\chronos.dll             native engine library copy
rem   libs\optional\openvino\...   only for --profile=all with OpenVINO_DIR
rem   web\                         built frontend: index.html + assets
rem
rem Idempotent: re-running rebuilds incrementally and only overwrites files
rem inside <out> and native\build. The only other writes are:
rem   - build\bin\ when the Wails GUI is built (the Wails CLI hardcodes it), and
rem   - a short-lived ASCII scratch directory below TEMP/PUBLIC used to work
rem     around MinGW ld rejecting non-ASCII -L paths (it fails with
rem     "cannot find -lchronos" when the checkout path contains non-ASCII
rem     characters, e.g. C:\Users\me\Downloads\<non-ascii>).
rem ===========================================================================
setlocal enabledelayedexpansion

rem %~dp0 is only reliable at the top level, not inside a called label.
for %%I in ("%~dp0..") do set "ROOT=%%~fI"
call :main %*
set "RC=%ERRORLEVEL%"
call :cleanup
endlocal & exit /b %RC%

rem ===========================================================================
rem main
rem ===========================================================================
:main
set "PROFILE=all"
set "SKIP_FRONTEND=0"
set "SKIP_NATIVE=0"
set "SKIP_GUI=0"
set "DEBUG=0"
set "CLEAN=0"
set "OUT=build\dist"
set "LINK_STAGE="
set "LINK_STAGE_WIN="
set "OV_VARIANT_DIR="
set "NATIVE_BUILD_TYPE=Release"
set "NATIVE_AVAILABLE=0"
set "GUI_BIN="
set "WEB_AVAILABLE=0"
set "STEP_N=0"
set "STEP_NAME="
set "STEP_START=0"
set "BUILD_TESTS_OPT=OFF"
set "AUTODEP=0"
set "HAS_WAILS=0"
set "TOTAL_START=0"
if /i "%CHRONOS_BUILD_TESTS%"=="1" set "BUILD_TESTS_OPT=ON"
if /i "%CHRONOS_AUTODEP%"=="1" set "AUTODEP=1"
if "%DEBUG%"=="1" set "NATIVE_BUILD_TYPE=Debug"

rem --- parse arguments ------------------------------------------------------
:parse_args
if "%~1"=="" goto :args_done
set "ARG=%~1"
if /i "%ARG%"=="-h" ( set "HELP=1" & goto :next_arg )
if /i "%ARG%"=="--help" ( set "HELP=1" & goto :next_arg )
if /i "%ARG%"=="--skip-frontend" ( set "SKIP_FRONTEND=1" & goto :next_arg )
if /i "%ARG%"=="--skip-native" ( set "SKIP_NATIVE=1" & goto :next_arg )
if /i "%ARG%"=="--skip-gui" ( set "SKIP_GUI=1" & goto :next_arg )
if /i "%ARG%"=="--debug" ( set "DEBUG=1" & set "NATIVE_BUILD_TYPE=Debug" & goto :next_arg )
if /i "%ARG%"=="--clean" ( set "CLEAN=1" & goto :next_arg )
if /i "%ARG:~0,10%"=="--profile=" ( set "PROFILE=%ARG:~10%" & goto :next_arg )
if /i "%ARG:~0,6%"=="--out=" ( set "OUT=%ARG:~6%" & goto :next_arg )
set "ERRMSG=unknown argument: %ARG%  -  run scripts\build.bat --help"
goto :fail
:next_arg
shift
goto :parse_args
:args_done

if defined HELP goto :show_help
set "HELP="

if /i "%PROFILE%"=="cpu" goto :profile_ok
if /i "%PROFILE%"=="openvino" goto :profile_ok
if /i "%PROFILE%"=="all" goto :profile_ok
set "ERRMSG=invalid --profile=%PROFILE%  -  expected cpu, openvino or all"
goto :fail
:profile_ok
if not defined OUT set "OUT=build\dist"

rem --- repository root and absolute output path ----------------------------
if not defined ROOT (
  set "ERRMSG=cannot resolve the repository root from %~dp0"
  goto :fail
)
if not exist "%ROOT%\go.mod" (
  set "ERRMSG=not a Chronos checkout: %ROOT%\go.mod is missing"
  goto :fail
)
cd /d "%ROOT%"
if errorlevel 1 (
  set "ERRMSG=cannot change directory to %ROOT%"
  goto :fail
)
if "%OUT:~1,1%"==":" ( set "OUTABS=%OUT%" ) else ( set "OUTABS=%ROOT%\%OUT%" )
for %%I in ("%OUTABS%") do set "OUT=%%~fI"

call :timestamp
set "TOTAL_START=%TS%"
call :timestamp
set "BUILT_AT=%TS%"

echo [build] Project Chronos build
echo [build] platform      : windows
echo [build] repository    : %ROOT%
echo [build] profile       : %PROFILE%
echo [build] output        : %OUT%
echo [build] native type   : %NATIVE_BUILD_TYPE%
echo [build] skip frontend : %SKIP_FRONTEND%
echo [build] skip native   : %SKIP_NATIVE%
echo [build] skip gui      : %SKIP_GUI%
echo [build] clean         : %CLEAN%
echo [build] autodep       : %AUTODEP%

rem ===========================================================================
rem STEP: clean
rem ===========================================================================
call :banner "clean"
if not "%CLEAN%"=="1" goto :clean_skipped
if exist "%OUT%\" (
  rmdir /s /q "%OUT%"
  echo [build] removed %OUT%
) else (
  echo [build] %OUT% does not exist - nothing to remove
)
if not "%SKIP_NATIVE%"=="0" goto :clean_done
if exist "native\build\" (
  rmdir /s /q "native\build"
  echo [build] removed native\build
)
goto :clean_done
:clean_skipped
echo [build] clean skipped - pass --clean to remove %OUT% and native\build
:clean_done
call :step_done

rem ===========================================================================
rem STEP: preflight
rem ===========================================================================
call :banner "preflight"

where /q go
if errorlevel 1 (
  set "ERRMSG=go is not on PATH. Install Go 1.26+ from https://go.dev/dl/ or run: winget install GoLang.Go"
  goto :fail
)
set "GO_FULL="
for /f "delims=" %%v in ('go version 2^>nul') do if not defined GO_FULL set "GO_FULL=%%v"
echo [build] go            : %GO_FULL%
set "GO_REQ="
if exist go.mod for /f "tokens=2" %%v in ('findstr /b /c:"go " go.mod 2^>nul') do if not defined GO_REQ set "GO_REQ=%%v"
if defined GO_REQ echo [build] go.mod needs  : go %GO_REQ%
set "GOARCH="
for /f "delims=" %%a in ('go env GOARCH 2^>nul') do if not defined GOARCH set "GOARCH=%%a"
if not defined GOARCH set "GOARCH=amd64"
if not exist "native\CMakeLists.txt" (
  set "ERRMSG=not a Chronos checkout: native\CMakeLists.txt is missing"
  goto :fail
)
if not exist "frontend\package.json" (
  set "ERRMSG=not a Chronos checkout: frontend\package.json is missing"
  goto :fail
)

rem --- cgo compiler ---------------------------------------------------------
set "NEED_CC=0"
if "%SKIP_NATIVE%"=="0" set "NEED_CC=1"
if exist "native\build\chronos.dll" set "NEED_CC=1"
if "%NEED_CC%"=="1" goto :pf_cc_check
echo [build] cgo compiler  : not required for the selected steps
goto :pf_cc_done
:pf_cc_check
set "GO_CC="
for /f "delims=" %%c in ('go env CC 2^>nul') do if not defined GO_CC set "GO_CC=%%c"
if not defined GO_CC set "GO_CC=gcc"
where /q "%GO_CC%"
if errorlevel 1 (
  set "ERRMSG=cgo C compiler '%GO_CC%' not found on PATH. Install MSYS2 UCRT64 and add C:\msys64\ucrt64\bin to PATH - pacman -S mingw-w64-ucrt-x86_64-gcc - or install winlibs MinGW-w64 and add its bin directory to PATH. cgo is required to link the native chronos.dll engine."
  goto :fail
)
set "CGO_ENABLED=1"
echo [build] cgo compiler  : %GO_CC%
:pf_cc_done

rem --- native toolchain -----------------------------------------------------
if "%SKIP_NATIVE%"=="1" goto :pf_native_done
where /q cmake
if not errorlevel 1 goto :pf_cmake_ready
if "%AUTODEP%"=="1" call :autodep_cmake
where /q cmake
if errorlevel 1 (
  set "ERRMSG=cmake not found on PATH - minimum 3.16. Install it with: winget install Kitware.CMake - or add the CMake bin directory to PATH - or set CHRONOS_AUTODEP=1 to fetch a pinned CMake 3.31.6 automatically."
  goto :fail
)
:pf_cmake_ready
set "CMAKE_LINE="
for /f "delims=" %%l in ('cmake --version 2^>nul') do if not defined CMAKE_LINE set "CMAKE_LINE=%%l"
echo [build] cmake         : %CMAKE_LINE%
where /q ninja
if not errorlevel 1 goto :pf_ninja_ready
if "%AUTODEP%"=="1" call :autodep_ninja
where /q ninja
if errorlevel 1 (
  set "ERRMSG=ninja not found on PATH. Install it with: winget install Ninja-build.Ninja - or set CHRONOS_AUTODEP=1 to fetch a pinned Ninja 1.13.2 automatically."
  goto :fail
)
:pf_ninja_ready
set "NINJA_VER="
for /f "delims=" %%v in ('ninja --version 2^>nul') do if not defined NINJA_VER set "NINJA_VER=%%v"
echo [build] ninja         : ninja %NINJA_VER%
:pf_native_done

rem --- frontend toolchain ---------------------------------------------------
if "%SKIP_FRONTEND%"=="1" goto :pf_frontend_done
where /q node
if errorlevel 1 (
  set "ERRMSG=node is not on PATH. Install Node 22+ from https://nodejs.org/ or run: winget install OpenJS.NodeJS.LTS"
  goto :fail
)
set "NODE_VER="
for /f "delims=" %%v in ('node --version 2^>nul') do if not defined NODE_VER set "NODE_VER=%%v"
echo [build] node          : node %NODE_VER%
if not exist "frontend\pnpm-lock.yaml" (
  set "ERRMSG=frontend\pnpm-lock.yaml is missing; pnpm install --frozen-lockfile requires it. Restore it from git before building the frontend."
  goto :fail
)
where /q pnpm
if errorlevel 1 (
  set "ERRMSG=pnpm is not on PATH. Install it with: corepack enable pnpm - or: npm install -g pnpm@11.28.5"
  goto :fail
)
set "PNPM_VER="
for /f "delims=" %%v in ('call pnpm --version 2^>nul') do if not defined PNPM_VER set "PNPM_VER=%%v"
echo [build] pnpm          : pnpm %PNPM_VER%
set "PM_SPEC="
for /f "delims=" %%s in ('powershell -NoProfile -Command "(Get-Content -Raw frontend/package.json | ConvertFrom-Json).packageManager" 2^>nul') do if not defined PM_SPEC set "PM_SPEC=%%s"
if not defined PM_SPEC goto :pf_frontend_done
if not defined PNPM_VER goto :pf_frontend_done
if /i "pnpm@%PNPM_VER%"=="%PM_SPEC%" goto :pf_frontend_done
echo [build] WARNING: package.json pins %PM_SPEC% but the active pnpm is %PNPM_VER%
echo [build] WARNING: pnpm install --frozen-lockfile may fail - align the pnpm version if it does
:pf_frontend_done

rem --- optional wails -------------------------------------------------------
where /q wails
if errorlevel 1 goto :pf_no_wails
set "WAILS_PATH="
for /f "delims=" %%p in ('where wails 2^>nul') do if not defined WAILS_PATH set "WAILS_PATH=%%p"
set "HAS_WAILS=1"
echo [build] wails         : %WAILS_PATH%
goto :pf_wails_done
:pf_no_wails
set "HAS_WAILS=0"
echo [build] wails         : not found - the GUI step will use a plain go build fallback
:pf_wails_done

call :step_done

rem ===========================================================================
rem STEP: frontend
rem ===========================================================================
if "%SKIP_FRONTEND%"=="1" goto :frontend_skipped
call :banner "frontend - pnpm install + build"
if exist "frontend\node_modules\" goto :frontend_build
echo [build] installing frontend dependencies - node_modules missing
pushd frontend
call pnpm install --frozen-lockfile
if errorlevel 1 (
  popd
  set "ERRMSG=pnpm install --frozen-lockfile failed. Check frontend\pnpm-lock.yaml and the pnpm version."
  goto :fail
)
popd
:frontend_build
pushd frontend
call pnpm build
if errorlevel 1 (
  popd
  set "ERRMSG=pnpm build failed. See the frontend output above."
  goto :fail
)
popd
if not exist "frontend\dist\index.html" (
  set "ERRMSG=frontend build did not produce frontend\dist\index.html"
  goto :fail
)
set "WEB_AVAILABLE=1"
echo [build] artifact: frontend\dist\index.html
call :step_done
goto :frontend_done
:frontend_skipped
echo [build] frontend build skipped - --skip-frontend
:frontend_done

rem ===========================================================================
rem STEP: native engine
rem ===========================================================================
if "%SKIP_NATIVE%"=="1" goto :native_skipped
call :banner "native engine - CMake + Ninja, profile=%PROFILE%"
if /i "%PROFILE%"=="openvino" goto :native_profile_ov
if /i "%PROFILE%"=="all" goto :native_profile_all

rem cpu profile
call :build_native_variant "native\build" "0"
if errorlevel 1 goto :fail_native
goto :native_built

:native_profile_ov
if not defined OpenVINO_DIR (
  set "ERRMSG=--profile=openvino requires OpenVINO_DIR, the directory containing OpenVINOConfig.cmake. Example: set OpenVINO_DIR=C:\path\to\openvino\runtime\cmake"
  goto :fail
)
if not exist "%OpenVINO_DIR%\" (
  set "ERRMSG=OpenVINO_DIR does not exist: %OpenVINO_DIR%"
  goto :fail
)
echo [build] OpenVINO_DIR  : %OpenVINO_DIR%
call :build_native_variant "native\build" "1"
if errorlevel 1 goto :fail_native
goto :native_built

:native_profile_all
call :build_native_variant "native\build" "0"
if errorlevel 1 goto :fail_native
if not defined OpenVINO_DIR goto :native_all_no_ov
echo [build] OpenVINO_DIR is set - additionally building the OpenVINO variant
set "OV_VARIANT_DIR=native\build\openvino"
call :build_native_variant "%OV_VARIANT_DIR%" "1"
if errorlevel 1 goto :fail_native_ov
goto :native_built
:native_all_no_ov
echo [build] OpenVINO_DIR not set - skipping the optional OpenVINO variant
goto :native_built

:native_built
echo [build] artifact: native\build\chronos.dll
call :step_done
goto :native_done
:native_skipped
echo [build] native build skipped - --skip-native
:native_done

rem --- native availability --------------------------------------------------
set "NATIVE_AVAILABLE=0"
if exist "native\build\chronos.dll" set "NATIVE_AVAILABLE=1"
if "%NATIVE_AVAILABLE%"=="1" goto :native_avail_done
if "%SKIP_NATIVE%"=="1" goto :native_avail_warn
set "ERRMSG=expected native library not found: native\build\chronos.dll"
goto :fail
:native_avail_warn
echo [build] WARNING: native\build\chronos.dll not found and --skip-native was passed
echo [build] WARNING: Go binaries will be built without the native engine - pure Go fallback
:native_avail_done

rem --- non-ASCII checkout path link workaround ------------------------------
if not "%NATIVE_AVAILABLE%"=="1" goto :link_stage_done
call :probe_ascii_only "%ROOT%"
if "%PROBE_OK%"=="1" goto :link_stage_done
echo [build] repository path contains non-ASCII characters - enabling MinGW link workaround
set "LINK_STAGE="
set "STAGE_BASE="
call :probe_link_base "%TEMP%"
if "%PROBE_OK%"=="1" set "STAGE_BASE=%TEMP%"
if defined STAGE_BASE goto :link_stage_have_base
call :probe_link_base "%PUBLIC%"
if "%PROBE_OK%"=="1" set "STAGE_BASE=%PUBLIC%"
:link_stage_have_base
if not defined STAGE_BASE (
  set "ERRMSG=could not find an ASCII scratch directory for the MinGW link workaround. Set TEMP to an ASCII path without spaces and retry."
  goto :fail
)
set "LINK_STAGE=%STAGE_BASE%\chronos-build-link-%RANDOM%"
mkdir "%LINK_STAGE%"
if not exist "native\build\libchronos.dll.a" (
  set "ERRMSG=expected import library native\build\libchronos.dll.a is missing"
  goto :fail
)
copy /y "native\build\libchronos.dll.a" "%LINK_STAGE%\" >nul
if errorlevel 1 (
  set "ERRMSG=could not copy the native import library into %LINK_STAGE%"
  goto :fail
)
copy /y "native\build\chronos.dll" "%LINK_STAGE%\" >nul
set "CGO_LDFLAGS=-L%LINK_STAGE%"
echo [build] CGO_LDFLAGS  : %CGO_LDFLAGS%
:link_stage_done

rem ===========================================================================
rem STEP: CLI
rem ===========================================================================
call :banner "CLI - cmd/chronos"
if not exist "%OUT%\bin" mkdir "%OUT%\bin"
if "%NATIVE_AVAILABLE%"=="1" goto :cli_native
echo [build] building without -tags chronos_native - pure Go fallback engine
go build -o "%OUT%\bin\chronos.exe" ./cmd/chronos
if errorlevel 1 goto :fail_cli
goto :cli_built
:cli_native
echo [build] building with -tags chronos_native - native engine linked
go build -tags chronos_native -o "%OUT%\bin\chronos.exe" ./cmd/chronos
if errorlevel 1 goto :fail_cli
:cli_built
copy /y "%OUT%\bin\chronos.exe" "%OUT%\bin\chronos-cli.exe" >nul
if errorlevel 1 (
  set "ERRMSG=could not create the chronos-cli.exe alias in %OUT%\bin"
  goto :fail
)
echo [build] artifact: %OUT%\bin\chronos.exe
echo [build] artifact: %OUT%\bin\chronos-cli.exe
call :step_done

rem ===========================================================================
rem STEP: GUI
rem ===========================================================================
if "%SKIP_GUI%"=="1" goto :gui_skipped
call :banner "desktop GUI - Wails or plain go build"
set "GUI_TAGS="
if "%NATIVE_AVAILABLE%"=="1" set "GUI_TAGS=chronos_native"
set "USE_WAILS=0"
if /i "%CHRONOS_GUI%"=="plain" goto :gui_plain
if "%HAS_WAILS%"=="1" set "USE_WAILS=1"
goto :gui_choose
:gui_plain
echo [build] CHRONOS_GUI=plain - forcing the plain go build fallback
:gui_choose
if "%USE_WAILS%"=="1" goto :gui_wails
echo [build] wails CLI not used - building the desktop app with plain go build
if defined GUI_TAGS (
  go build -tags %GUI_TAGS% -o "%OUT%\bin\chronos-desktop.exe" .
) else (
  go build -o "%OUT%\bin\chronos-desktop.exe" .
)
if errorlevel 1 goto :fail_gui
set "GUI_BIN=%OUT%\bin\chronos-desktop.exe"
echo [build] artifact: %GUI_BIN% - plain go build
goto :gui_built

:gui_wails
set "WAILS_ARGS=build -s -skipbindings -platform windows/%GOARCH%"
if defined GUI_TAGS set "WAILS_ARGS=%WAILS_ARGS% -tags %GUI_TAGS%"
if "%CLEAN%"=="1" set "WAILS_ARGS=%WAILS_ARGS% -clean"
if "%DEBUG%"=="1" set "WAILS_ARGS=%WAILS_ARGS% -debug"
echo [build] wails %WAILS_ARGS%
wails %WAILS_ARGS%
if errorlevel 1 goto :fail_wails
if not exist "build\bin\chronos.exe" goto :fail_wails_output
copy /y "build\bin\chronos.exe" "%OUT%\bin\chronos-desktop.exe" >nul
if errorlevel 1 (
  set "ERRMSG=could not copy build\bin\chronos.exe to %OUT%\bin\chronos-desktop.exe"
  goto :fail
)
set "GUI_BIN=%OUT%\bin\chronos-desktop.exe"
echo [build] artifact: %GUI_BIN% - wails
goto :gui_built

:gui_built
call :step_done
goto :gui_done
:gui_skipped
echo [build] desktop GUI skipped - --skip-gui
:gui_done

rem ===========================================================================
rem STEP: assemble
rem ===========================================================================
call :banner "assemble dist tree"
if not exist "%OUT%\bin" mkdir "%OUT%\bin"
if not exist "%OUT%\libs" mkdir "%OUT%\libs"

if "%NATIVE_AVAILABLE%"=="0" goto :asm_after_native
copy /y "native\build\chronos.dll" "%OUT%\bin\chronos.dll" >nul
if errorlevel 1 (
  set "ERRMSG=could not copy chronos.dll to %OUT%\bin"
  goto :fail
)
copy /y "native\build\chronos.dll" "%OUT%\libs\chronos.dll" >nul
if errorlevel 1 (
  set "ERRMSG=could not copy chronos.dll to %OUT%\libs"
  goto :fail
)
echo [build] artifact: %OUT%\bin\chronos.dll
echo [build] artifact: %OUT%\libs\chronos.dll
:asm_after_native

if not defined OV_VARIANT_DIR goto :asm_after_ov
if not exist "%OV_VARIANT_DIR%\chronos.dll" goto :asm_after_ov
if not exist "%OUT%\libs\optional\openvino" mkdir "%OUT%\libs\optional\openvino"
copy /y "%OV_VARIANT_DIR%\chronos.dll" "%OUT%\libs\optional\openvino\chronos.dll" >nul
echo [build] artifact: %OUT%\libs\optional\openvino\chronos.dll
:asm_after_ov

if exist "%OUT%\web\" rmdir /s /q "%OUT%\web"
mkdir "%OUT%\web"
if not exist "frontend\dist\index.html" goto :asm_web_missing
xcopy "frontend\dist\*" "%OUT%\web\" /e /i /y /q >nul
if errorlevel 1 (
  set "ERRMSG=could not copy frontend\dist to %OUT%\web"
  goto :fail
)
set "WEB_AVAILABLE=1"
echo [build] artifact: %OUT%\web\index.html
goto :asm_web_done
:asm_web_missing
echo [build] WARNING: frontend\dist\index.html not found - %OUT%\web will be empty
echo [build] WARNING: browser mode needs UI assets - run without --skip-frontend
:asm_web_done

call :write_readme
if /i not "%PROFILE%"=="openvino" goto :asm_readme_ov_done
call :write_readme_openvino
:asm_readme_ov_done
if not exist "%OUT%\libs\optional\openvino\chronos.dll" goto :asm_readme_opt_done
call :write_readme_openvino_optional
:asm_readme_opt_done

rem --- self-check: the CLI must start from the dist tree --------------------
set "SELFCHECK=%OUT%\.selfcheck.txt"
if not exist "%OUT%\bin\chronos.exe" (
  set "ERRMSG=chronos.exe was not produced in %OUT%\bin"
  goto :fail
)
"%OUT%\bin\chronos.exe" info > "%SELFCHECK%" 2>&1
if errorlevel 1 goto :fail_selfcheck
echo [build] self-check: chronos info succeeded
type "%SELFCHECK%"
del /q "%SELFCHECK%" >nul 2>nul
echo.
echo [build] dist tree:
tree /f /a "%OUT%"
call :step_done

rem ===========================================================================
rem summary
rem ===========================================================================
call :timestamp
set /a TOTAL_ELAPSED=TS-TOTAL_START
if %TOTAL_ELAPSED% lss 0 set "TOTAL_ELAPSED=0"
echo.
echo [build] ==================================================
echo [build] BUILD OK in %TOTAL_ELAPSED%s
echo [build] output dir : %OUT%
echo [build] binaries   : %OUT%\bin
if defined GUI_BIN echo [build] gui        : %GUI_BIN%
echo [build] web assets : %OUT%\web
echo [build] native lib : %OUT%\libs
echo [build] ==================================================
exit /b 0

rem ===========================================================================
rem helper subroutines
rem ===========================================================================
:timestamp
set "TS=0"
for /f "delims=" %%t in ('powershell -NoProfile -Command "[int][DateTimeOffset]::UtcNow.ToUnixTimeSeconds()" 2^>nul') do set "TS=%%t"
exit /b 0

:banner
set /a STEP_N+=1
set "STEP_NAME=%~1"
call :timestamp
set "STEP_START=%TS%"
echo.
echo [build] ===== STEP %STEP_N%: %STEP_NAME% =====
exit /b 0

:step_done
call :timestamp
set /a STEP_ELAPSED=TS-STEP_START
if %STEP_ELAPSED% lss 0 set "STEP_ELAPSED=0"
echo [build] step '%STEP_NAME%' finished in %STEP_ELAPSED%s
exit /b 0

rem --- native build variant --------------------------------------------------
rem CMAKE_SUPPRESS_REGENERATION=ON: the script always runs the configure step
rem explicitly, so the in-build CMake regeneration rule is redundant. It is also
rem what keeps ninja from looping with "manifest still dirty after 100 tries"
rem when the checkout contains files with timestamps in the future (for example
rem after a system clock correction).
:build_native_variant
set "NBDIR=%~1"
set "NBOV=%~2"
set "NBARGS=-S native -B "%NBDIR%" -G Ninja -DCMAKE_BUILD_TYPE=%NATIVE_BUILD_TYPE% -DCHRONOS_BUILD_TESTS=%BUILD_TESTS_OPT% -DCMAKE_SUPPRESS_REGENERATION=ON"
if "%NBOV%"=="1" set "NBARGS=%NBARGS% -DCHRONOS_WITH_OPENVINO=ON -DOpenVINO_DIR="%OpenVINO_DIR%""
if defined CC goto :nb_no_ccforce
if defined CXX goto :nb_no_ccforce
set "NBARGS=%NBARGS% -DCMAKE_C_COMPILER=gcc -DCMAKE_CXX_COMPILER=g++"
:nb_no_ccforce
echo [build] cmake %NBARGS%
cmake %NBARGS%
if errorlevel 1 exit /b 1
cmake --build "%NBDIR%" --parallel
if errorlevel 1 exit /b 1
if not exist "%NBDIR%\chronos.dll" exit /b 1
exit /b 0

rem --- ASCII path probes -----------------------------------------------------
:probe_ascii_only
rem returns PROBE_OK=1 when %1 is pure 7-bit ASCII
set "PROBE_OK=0"
set "PROBE_RESULT="
set "PROBE_VALUE=%~1"
for /f "delims=" %%r in ('powershell -NoProfile -Command "if ($env:PROBE_VALUE -match '[^\x20-\x7E]') { 'no' } else { 'yes' }" 2^>nul') do set "PROBE_RESULT=%%r"
if not defined PROBE_RESULT set "PROBE_RESULT=no"
if /i "%PROBE_RESULT%"=="yes" set "PROBE_OK=1"
exit /b 0

:probe_link_base
rem returns PROBE_OK=1 when %1 is 7-bit ASCII with no spaces
set "PROBE_OK=0"
set "PROBE_RESULT="
set "PROBE_VALUE=%~1"
for /f "delims=" %%r in ('powershell -NoProfile -Command "if ($env:PROBE_VALUE -match '[^\x21-\x7E]') { 'no' } else { 'yes' }" 2^>nul') do set "PROBE_RESULT=%%r"
if not defined PROBE_RESULT set "PROBE_RESULT=no"
if /i "%PROBE_RESULT%"=="yes" set "PROBE_OK=1"
exit /b 0

rem --- sha256 verification ---------------------------------------------------
:verify_sha
set "SHA_FILE=%~1"
set "SHA_EXPECTED=%~2"
set "SHA_GOT="
for /f "delims=" %%h in ('powershell -NoProfile -Command "(Get-FileHash -LiteralPath '%SHA_FILE%' -Algorithm SHA256).Hash.ToLower()" 2^>nul') do set "SHA_GOT=%%h"
if not defined SHA_GOT (
  echo [build] ERROR: could not compute sha256 for %SHA_FILE%
  exit /b 1
)
if /i not "%SHA_GOT%"=="%SHA_EXPECTED%" (
  echo [build] ERROR: sha256 mismatch for %SHA_FILE%
  echo [build]   expected %SHA_EXPECTED%
  echo [build]   got      %SHA_GOT%
  exit /b 1
)
echo [build] sha256 verified: %SHA_EXPECTED%
exit /b 0

rem --- CHRONOS_AUTODEP: pinned CMake ----------------------------------------
:autodep_cmake
if not "%AUTODEP%"=="1" exit /b 1
set "DEPS=native\build\.deps"
if not exist "%DEPS%\cmake\" mkdir "%DEPS%\cmake"
set "CMAKE_ZIP=%DEPS%\cmake-download.zip"
set "CMAKE_URL=https://cmake.org/files/v3.31/cmake-3.31.6-windows-x86_64.zip"
set "CMAKE_SHA=d163cd3ab4959b0a53fa8988f2ddbd2e6c501658201e6a154386bad9dbe4f836"
echo [build] downloading pinned CMake 3.31.6 from cmake.org
curl.exe -fsSL --retry 2 --connect-timeout 20 -o "%CMAKE_ZIP%" "%CMAKE_URL%"
if errorlevel 1 (
  echo [build] ERROR: CMake download failed: %CMAKE_URL%
  exit /b 1
)
call :verify_sha "%CMAKE_ZIP%" "%CMAKE_SHA%"
if errorlevel 1 exit /b 1
powershell -NoProfile -Command "Expand-Archive -LiteralPath '%CMAKE_ZIP%' -DestinationPath '%DEPS%\cmake' -Force"
if errorlevel 1 (
  echo [build] ERROR: could not extract %CMAKE_ZIP%
  exit /b 1
)
del /q "%CMAKE_ZIP%" >nul 2>nul
set "CMAKE_EXE="
for /r "%DEPS%\cmake" %%f in (cmake.exe) do if not defined CMAKE_EXE set "CMAKE_EXE=%%~ff"
if not defined CMAKE_EXE (
  echo [build] ERROR: cmake.exe not found after extraction under %DEPS%\cmake
  exit /b 1
)
for %%d in ("%CMAKE_EXE%") do set "CMAKE_DIR=%%~dpd"
set "PATH=%CMAKE_DIR%;%PATH%"
echo [build] cmake installed for this run: %CMAKE_EXE%
exit /b 0

rem --- CHRONOS_AUTODEP: pinned Ninja ----------------------------------------
:autodep_ninja
if not "%AUTODEP%"=="1" exit /b 1
set "DEPS=native\build\.deps"
if not exist "%DEPS%\ninja\" mkdir "%DEPS%\ninja"
set "NINJA_ZIP=%DEPS%\ninja-download.zip"
set "NINJA_URL=https://files.pythonhosted.org/packages/3f/dd/3766b5f4d32e8a9b97d195496b0b01fbbe2e1a41669dab0cd6492a6ce199/ninja-1.13.2-py3-none-win_amd64.whl"
set "NINJA_SHA=1293f4078278b70d0ee4b6cc8f3a9e030656c9b2f59909970343c4fe76070118"
echo [build] downloading pinned Ninja 1.13.2 from PyPI
curl.exe -fsSL --retry 2 --connect-timeout 20 -o "%NINJA_ZIP%" "%NINJA_URL%"
if errorlevel 1 (
  echo [build] ERROR: Ninja download failed: %NINJA_URL%
  exit /b 1
)
call :verify_sha "%NINJA_ZIP%" "%NINJA_SHA%"
if errorlevel 1 exit /b 1
powershell -NoProfile -Command "Expand-Archive -LiteralPath '%NINJA_ZIP%' -DestinationPath '%DEPS%\ninja' -Force"
if errorlevel 1 (
  echo [build] ERROR: could not extract %NINJA_ZIP%
  exit /b 1
)
del /q "%NINJA_ZIP%" >nul 2>nul
set "NINJA_EXE="
for /r "%DEPS%\ninja" %%f in (ninja.exe) do if not defined NINJA_EXE set "NINJA_EXE=%%~ff"
if not defined NINJA_EXE (
  echo [build] ERROR: ninja.exe not found after extraction under %DEPS%\ninja
  exit /b 1
)
for %%d in ("%NINJA_EXE%") do set "NINJA_DIR=%%~dpd"
set "PATH=%NINJA_DIR%;%PATH%"
echo [build] ninja installed for this run: %NINJA_EXE%
exit /b 0

rem --- README.txt ------------------------------------------------------------
:write_readme
set "README=%OUT%\README.txt"
> "%README%" echo Project Chronos - local build output
>> "%README%" echo =====================================
>> "%README%" echo.
>> "%README%" echo Built with the repository build script scripts\build.bat
>> "%README%" echo Profile    : %PROFILE%
>> "%README%" echo Native type: %NATIVE_BUILD_TYPE%
>> "%README%" echo.
>> "%README%" echo Layout
if defined GUI_BIN (
  >> "%README%" echo   bin\chronos-desktop.exe   Desktop GUI app. UI assets are embedded.
) else (
  >> "%README%" echo   bin\chronos-desktop.exe   NOT built in this run - see --skip-gui
)
>> "%README%" echo   bin\chronos.exe           Headless CLI: upscale, info, models, serve.
>> "%README%" echo   bin\chronos-cli.exe       Same CLI binary, release/package naming.
>> "%README%" echo   bin\chronos.dll           Native engine, loaded from the exe directory.
>> "%README%" echo   libs\chronos.dll          Native engine library copy.
if "%WEB_AVAILABLE%"=="1" (
  >> "%README%" echo   web\                      Built frontend for browser mode.
) else (
  >> "%README%" echo   web\                      NOT populated - run without --skip-frontend
)
>> "%README%" echo.
if defined GUI_BIN (
  >> "%README%" echo Run the desktop app
  >> "%README%" echo   bin\chronos-desktop.exe
  >> "%README%" echo   If WebView2 is missing, the app falls back to browser mode.
  >> "%README%" echo.
)
>> "%README%" echo Run the CLI
>> "%README%" echo   bin\chronos.exe info
>> "%README%" echo   bin\chronos.exe upscale -i in.png -o out.png -s 2 --no-model
>> "%README%" echo.
>> "%README%" echo Browser mode, no WebView needed
>> "%README%" echo   Open a shell in THIS directory first so the CLI finds web:
>> "%README%" echo     bin\chronos.exe serve
>> "%README%" echo   The CLI serves the UI on 127.0.0.1 and opens your browser.
>> "%README%" echo   Useful options:
>> "%README%" echo     bin\chronos.exe serve --port 8787
>> "%README%" echo     bin\chronos.exe serve --no-open
>> "%README%" echo   From another directory, pass the assets directory explicitly:
>> "%README%" echo     bin\chronos.exe serve --assets web
>> "%README%" echo.
>> "%README%" echo Notes
>> "%README%" echo   Models are downloaded on demand:
>> "%README%" echo     bin\chronos.exe models list
>> "%README%" echo     bin\chronos.exe models pull MODEL_ID
>> "%README%" echo   The CLI and the desktop app share the same data directories.
>> "%README%" echo   Re-running the build script is idempotent.
exit /b 0

:write_readme_openvino
set "README=%OUT%\README.txt"
>> "%README%" echo.
>> "%README%" echo OpenVINO profile
>> "%README%" echo   This engine was built with the OpenVINO backend. The OpenVINO
>> "%README%" echo   runtime openvino.dll must be available at run time: add its
>> "%README%" echo   runtime directory to PATH, or run setupvars from the OpenVINO
>> "%README%" echo   distribution.
exit /b 0

:write_readme_openvino_optional
set "README=%OUT%\README.txt"
>> "%README%" echo.
>> "%README%" echo Optional OpenVINO variant
>> "%README%" echo   libs\optional\openvino\chronos.dll is a second native library
>> "%README%" echo   built with -DCHRONOS_WITH_OPENVINO=ON. The default library in
>> "%README%" echo   bin\ and libs\ is the CPU/stub build. To switch, copy the
>> "%README%" echo   optional library over bin\ and libs\, then provide the
>> "%README%" echo   OpenVINO runtime.
exit /b 0

rem --- cleanup --------------------------------------------------------------
:cleanup
if not defined LINK_STAGE exit /b 0
if not exist "%LINK_STAGE%\" exit /b 0
rmdir /s /q "%LINK_STAGE%" 2>nul
exit /b 0

rem ===========================================================================
rem help and error exits
rem ===========================================================================
:show_help
echo Project Chronos - one-shot build script for Windows.
echo.
echo Usage:
echo   scripts\build.bat [flags]
echo.
echo Flags:
echo   --profile=cpu ^| openvino ^| all  native engine profile, default: all
echo                                   cpu      : stub/CPU engine only
echo                                   openvino : OpenVINO backend, needs OpenVINO_DIR
echo                                   all      : CPU plus optional OpenVINO variant
echo   --skip-frontend               do not run pnpm install / pnpm build
echo   --skip-native                 do not run CMake, reuse native\build if present
echo   --skip-gui                    do not build the desktop GUI
echo   --debug                       Debug native build and wails -debug
echo   --clean                       remove the output dir and native\build first
echo   --out=DIR                     output directory, default: build\dist
echo   -h, --help                    print this help
echo.
echo Environment:
echo   CHRONOS_AUTODEP=1             fetch pinned CMake 3.31.6 + Ninja 1.13.2
echo                                 into native\build\.deps when missing
echo   CHRONOS_BUILD_TESTS=1         build the native ctest suite
echo   CHRONOS_GUI=plain             force the plain go build GUI path
echo   OpenVINO_DIR=DIR              OpenVINOConfig.cmake directory
echo.
echo Output layout: bin\chronos.exe, bin\chronos-cli.exe, bin\chronos-desktop.exe,
echo bin\chronos.dll, libs\chronos.dll, web\, README.txt
exit /b 0

:fail
echo [build] ERROR: %ERRMSG% 1>&2
exit /b 1

:fail_native
set "ERRMSG=the native CMake build failed. See the cmake output above. If the CMake cache is stale or was configured for another compiler, re-run with --clean to reconfigure from scratch."
goto :fail

:fail_native_ov
set "ERRMSG=the optional OpenVINO native build failed while OpenVINO_DIR is set. Re-run with --profile=cpu to build the CPU engine only."
goto :fail

:fail_cli
set "ERRMSG=go build -tags chronos_native ./cmd/chronos failed. See the go output above. cgo needs gcc on PATH plus native\build\chronos.dll and libchronos.dll.a from the native build step."
goto :fail

:fail_gui
set "ERRMSG=the plain go build of the desktop app failed. See the go output above. Use --skip-gui to build only the CLI."
goto :fail

:fail_wails
set "ERRMSG=wails build failed. See docs\CI.md section 6 for the Wails build notes. Use --skip-gui to build only the CLI, or uninstall wails and retry to use the plain go build fallback."
goto :fail

:fail_wails_output
set "ERRMSG=wails build succeeded but build\bin\chronos.exe was not found."
goto :fail

:fail_selfcheck
echo [build] self-check output: 1>&2
type "%SELFCHECK%" 1>&2
del /q "%SELFCHECK%" >nul 2>nul
set "ERRMSG=the CLI does not start from the dist tree. Most likely bin\chronos.dll is missing or does not match the binary. See the self-check output above."
goto :fail
