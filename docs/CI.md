# Project Chronos — CI 与本地复现

> 最后更新：2026-11-08。工作流文件：
> [`.github/workflows/ci.yml`](../.github/workflows/ci.yml)、
> [`release.yml`](../.github/workflows/release.yml)、
> [`openvino.yml`](../.github/workflows/openvino.yml)。

## 1. 触发与并发

| 工作流 | 触发 | 说明 |
| --- | --- | --- |
| `ci.yml` | `push` 到 `main`、`pull_request`、`workflow_dispatch` | 全部平台构建与测试；`package` job 只在 main push 或手动触发时运行 |
| `release.yml` | `push` tag `v*`、`workflow_dispatch`（输入 version） | 构建 Linux/Windows 包并创建 GitHub Release |
| `openvino.yml` | 仅 `workflow_dispatch` | 可选 OpenVINO 后端的文档化验证，`continue-on-error`，不阻塞主 CI |

CI 的 concurrency 为 `ci-<workflow>-<ref>`，`cancel-in-progress: true`：同一分支
连续 push 会取消旧运行（PR 反馈更快、省 runner 分钟）。release 使用独立 group 并
`cancel-in-progress: false`，避免发布被半途取消。

## 2. Job 图与依赖

```
native-linux (ubuntu)  ──┐
                         ├──► native-integration (ubuntu + windows)
native-windows (win)   ──┘            │
go (ubuntu + windows) ────────────────┼──► package (linux + windows)   [main push / dispatch]
frontend (ubuntu) ────────────────────┘
```

- `package` 依赖前面所有 job；任何一个失败都不会出包（发布产物必须来自全绿构建）。
- `native-integration` 依赖两个 native job，确保 cgo 测试拿到对应平台的二进制。
- 所有 job 都 `actions/checkout@v4` + `actions/setup-go@v5`（`go-version-file: go.mod`，
  `cache: false`——本项目依赖规模小，关闭缓存避免 `go.sum` 缺失时 setup-go 直接失败）。
- 前端 job 使用 `pnpm/action-setup@v4`（version 11，与 `frontend/package.json` 的
  `packageManager: pnpm@11.28.5` 对齐）+ `setup-node@v4`（Node 20，pnpm cache）。

## 3. 各 Job 做什么

### 3.1 `native-linux`（ubuntu-latest）

```bash
sudo apt-get install -y cmake ninja-build g++ pkg-config
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release -DCHRONOS_BUILD_TESTS=ON
cmake --build native/build --parallel
ctest --test-dir native/build --output-on-failure

# ASan + UBSan 第二套构建
cmake -S native -B native/build-asan -G Ninja \
      -DCMAKE_BUILD_TYPE=RelWithDebInfo -DCHRONOS_BUILD_TESTS=ON -DCHRONOS_SANITIZE=ON
cmake --build native/build-asan --parallel
ASAN_OPTIONS=detect_leaks=1:abort_on_error=1:strict_string_checks=1 \
UBSAN_OPTIONS=print_stacktrace=1:halt_on_error=1 \
ctest --test-dir native/build-asan --output-on-failure
```

上传 artifact `native-linux`：`native/build/libchronos.so`、`native/build/chronos_native_cli`、
`native/include/**`（保留相对路径，供 integration job 直接落盘）。

### 3.2 `native-windows`（windows-latest，MSYS2 UCRT64）

`msys2/setup-msys2@v2` 安装 `mingw-w64-ucrt-x86_64-{gcc,cmake,ninja}` 和 `make`，
所有构建步骤用 `shell: msys2 {0}`：

```bash
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release -DCHRONOS_BUILD_TESTS=ON
cmake --build native/build
ctest --test-dir native/build --output-on-failure
```

上传 artifact `native-windows`：`chronos.dll`、`libchronos.dll.a`、`chronos_native_cli.exe`、
头文件。

### 3.3 `go`（matrix: ubuntu-latest / windows-latest）

**Linux 不能直接 `CGO_ENABLED=0 go test ./...`**：Wails 的 Linux webview 绑定是 cgo
（`pkg-config: gtk+-3.0`，Ubuntu 24.04 还需要 `-tags webkit2_41` 走 webkit2gtk-4.1），
`CGO_ENABLED=0` 时根包（Wails 应用）无法编译。因此 Linux 使用：

```bash
sudo apt-get install -y libgtk-3-dev libwebkit2gtk-4.1-dev pkg-config
CGO_ENABLED=1 go vet  -tags webkit2_41 ./...
CGO_ENABLED=1 go test -tags webkit2_41 ./... -count=1
CGO_ENABLED=1 go test -tags webkit2_41 -race ./... -count=1   # -race 需要 cgo
```

这里不传 `chronos_native`，所以走纯 Go 的 `go-lanczos` 回退，不需要原生库。
Windows 不需要 cgo 编译 Wails 应用，保持：

```powershell
$env:CGO_ENABLED = "0"
go vet ./...
go test ./... -count=1
```

> 说明：任务书最初写的是"`CGO_ENABLED=0 go test ./...`（含 Linux）"。在 Wails Linux
> 构建下该命令必然失败（gtk cgo 绑定），所以 Linux 采用上面的等价方案，仍然覆盖
> 纯 Go 逻辑；差异记录在此，避免后人误改。

### 3.4 `native-integration`（matrix: ubuntu + windows）

1. `actions/download-artifact@v4` 下载对应平台的 native artifact 到仓库根目录
   （`path: .`，恢复 `native/build/*`、`native/include/*`）。
2. 运行 cgo 测试（`CGO_ENABLED=1`，构建标签 `chronos_native`）：

   ```bash
   LD_LIBRARY_PATH=native/build go test -tags chronos_native ./internal/engine/... -count=1
   # Windows: chronos.dll 所在目录加入 PATH，cgo 需要 gcc（MSYS2 UCRT64 提供）
   ```

3. 构建 CLI 并冒烟：

   ```bash
   go build -tags chronos_native -o chronos ./cmd/chronos
   bash scripts/ubuntu-smoke.sh ./chronos
   ```

   Windows 对应 `go build -tags chronos_native -o chronos.exe ./cmd/chronos` +
   `pwsh -File scripts/windows-smoke.ps1 -CliPath .\chronos.exe`。

冒烟脚本不下载任何模型：用 python 标准库（zlib+struct）现场生成 64×48 RGB PNG，
`chronos upscale -i in.png -o out.png -s 2 --no-model` 后解析输出 PNG 的 IHDR，断言
128×96；再检查 `chronos inhibit status` 退出码为 0、`chronos info` 输出
`engine: <stub|openvino|go-lanczos>` 的 backend 行。

### 3.5 `frontend`（ubuntu-latest）

```bash
cd frontend
pnpm install --frozen-lockfile
pnpm test        # vitest run
pnpm build       # vue-tsc --noEmit && vite build
```

上传 artifact `frontend-dist`（`frontend/dist`）。`frontend/dist/.gitkeep` 必须保留，
否则根包 `go:embed all:frontend/dist` 会失败。

### 3.6 `package`（matrix: ubuntu + windows，仅 main push / dispatch）

Linux：

```bash
sudo apt-get install -y cmake ninja-build g++ pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
# CLI 版本必须与 go.mod 里的 wails 库一致（@latest 可能解析到 v3）
WAILS_VERSION="$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)"
go install "github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release -DCHRONOS_BUILD_TESTS=OFF
cmake --build native/build --parallel
cd frontend && pnpm install --frozen-lockfile && cd ..
wails build -clean -tags "webkit2_41 chronos_native" -ldflags "-X main.version=${VERSION}"
VERSION=0.1.0 OUT_DIR=dist-packages bash scripts/package-linux.sh
```

Windows：

```powershell
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release -DCHRONOS_BUILD_TESTS=OFF
cmake --build native/build
wails build -clean -tags "chronos_native" -platform windows/amd64 -ldflags "-X main.version=${VERSION}"
# 组装 zip：build/bin/chronos.exe + native/build/chronos.dll + internal/models/registry.json
#           + README.md/LICENSE（存在时）
Compress-Archive ... chronos-<ver>-windows-amd64.zip
```

重要：**发布/打包构建必须带 `chronos_native` 标签**。不带该标签时
`internal/engine/native_stub.go` 生效，应用退化为 `go-lanczos`，无法加载任何 AI 模型。
`chronos info` / UI 状态栏的 `backend` 字段是判断依据（期望 `openvino` 或 `stub`）。

产物与 artifact：

| artifact | 内容 |
| --- | --- |
| `chronos-linux` | `chronos_<ver>_amd64.deb`、`chronos-<ver>-linux-amd64.tar.gz`、`SHA256SUMS` |
| `chronos-windows` | `chronos-<ver>-windows-amd64.zip`、`SHA256SUMS` |

打包布局：

- `.tar.gz`：`chronos-<ver>-linux-amd64/{chronos, libs/libchronos.so, models/registry.json, README.md, LICENSE}`。
- `.deb`：payload 在 `/opt/chronos/`（二进制 + `libs/` + `models/`），`/usr/bin/chronos`
  是导出 `LD_LIBRARY_PATH=/opt/chronos/libs` 的 wrapper，`/usr/share/applications/chronos.desktop`
  提供桌面入口。`scripts/package-linux.sh` 需要 `dpkg-deb` 与 `tar`。
- zip：`chronos.exe` 与 `chronos.dll` 同目录（Go cgo 运行时按 exe 目录的 PATH 查找 DLL），
  `models/registry.json` 供用户查看内置清单。

## 4. Release

```bash
git tag v0.2.0
git push origin v0.2.0          # 触发 release.yml
```

或在 Actions 页面手动运行 `Release`，`version` 输入 `0.2.0`（不带 `v`）。工作流：

1. `meta` job 解析版本（tag 去掉 `v`，或 dispatch 输入），输出 `version` / `tag`。
2. `build-linux` / `build-windows` 重复 §3.6 的构建与打包，产物名带版本号。
3. `release` job 下载两个 artifact，合并生成一个 `SHA256SUMS`，用
   `softprops/action-gh-release@v2` 创建 Release（`generate_release_notes: true`），
   附带 `.deb`、`.tar.gz`、`.zip`、`SHA256SUMS`；含 `-` 的版本号（如 `0.2.0-beta.1`）
   自动标记 prerelease。工作流需要 `permissions: contents: write`。

`wails build` 的 `-ldflags "-X main.version=<ver>"` 是版本注入约定（根包变量名
`version`）。若 Lead 改为 ldflags 之外的方式（如 `wails.json` productVersion），
需同步修改两个工作流，否则安装包内的版本号仍是 `0.1.0`。

## 5. 可选 OpenVINO 工作流

`.github/workflows/openvino.yml` 只手动触发，job 级 `continue-on-error: true`，作用是
**文档化并实测** `-DCHRONOS_WITH_OPENVINO=ON` 的真实构建路径，而不是给主 CI 增加
数百 MB 的依赖：

```bash
# Intel APT 仓库（Ubuntu 24）
wget -q https://apt.repos.intel.com/intel-gpg-keys/GPG-PUB-KEY-INTEL-SW-PRODUCTS.PUB
sudo gpg --output /etc/apt/trusted.gpg.d/intel.gpg --dearmor GPG-PUB-KEY-INTEL-SW-PRODUCTS.PUB
echo "deb https://apt.repos.intel.com/openvino ubuntu24 main" \
  | sudo tee /etc/apt/sources.list.d/intel-openvino.list
sudo apt-get update && sudo apt-get install -y --no-install-recommends openvino

cmake -S native -B native/build-openvino -G Ninja \
      -DCMAKE_BUILD_TYPE=Release -DCHRONOS_BUILD_TESTS=OFF \
      -DCHRONOS_WITH_OPENVINO=ON -DOpenVINO_DIR=$(find /usr -name OpenVINOConfig.cmake ...)
cmake --build native/build-openvino --parallel --target chronos_native_cli
./native/build-openvino/chronos_native_cli --info | tee openvino-info.txt
# --info 是 JSON：{"backends":{"stub":true,"openvino":true}}；直接 grep openvino
# 会误匹配 "openvino":false，必须匹配 true。
grep -q '"openvino":true' openvino-info.txt
```

替代方案：从 `https://storage.openvinotoolkit.org/repositories/openvino/packages/`
下载 runtime tarball，`source setupvars.sh` 后把 `-DOpenVINO_DIR=<root>/runtime/cmake`
传给 CMake。该工作流失败只说明"这台 runner 上可选后端未验证通过"，不影响主 CI。

## 6. 本地复现

### Linux

```bash
sudo apt-get install -y cmake ninja-build g++ pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev

# native
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build native/build
ctest --test-dir native/build --output-on-failure

# Go（纯 Go 回退，无原生库）
CGO_ENABLED=1 go test -tags webkit2_41 ./... -count=1

# cgo + 原生核心
LD_LIBRARY_PATH=native/build go test -tags chronos_native ./internal/engine/... -count=1
CGO_ENABLED=1 go build -tags chronos_native -o chronos ./cmd/chronos
bash scripts/ubuntu-smoke.sh ./chronos

# 发布包（需要先 wails build；dpkg-deb 来自 dpkg）
VERSION=0.1.0 bash scripts/package-linux.sh
```

### Windows

推荐 MSYS2 UCRT64（与 CI 一致），或 winlibs 的 mingw64（把 `winlibs\mingw64\bin`
加入 PATH，以获得 gcc/cmake/ninja）。PowerShell 中：

```powershell
# 1) native（在 UCRT64 shell 或已配置 PATH 的 PowerShell 中）
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build native/build
ctest --test-dir native/build --output-on-failure

# 2) 纯 Go
$env:CGO_ENABLED = "0"
go vet ./...
go test ./... -count=1

# 3) cgo 集成（需要 gcc；DLL 目录加入 PATH）
$env:CGO_ENABLED = "1"
$env:PATH = "$PWD\native\build;$env:PATH"
go test -tags chronos_native ./internal/engine/... -count=1
go build -tags chronos_native -o chronos.exe ./cmd/chronos
pwsh -NoProfile -ExecutionPolicy Bypass -File scripts/windows-smoke.ps1 -CliPath .\chronos.exe
```

### Wails 桌面构建

```bash
# 版本与 go.mod 对齐，避免 @latest 解析到 Wails v3
WAILS_VERSION="$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v2)"
go install "github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}"
export PATH="$PATH:$(go env GOPATH)/bin"

# Linux（Ubuntu 24.04；webkit2gtk-4.1 必须加 webkit2_41）
wails build -clean -tags "webkit2_41 chronos_native" -ldflags "-X main.version=0.1.0"

# Windows（WebView2 运行时由 Win11 / 已更新的 Win10 提供；缺省时需安装 Evergreen Runtime）
wails build -clean -tags "chronos_native" -platform windows/amd64 -ldflags "-X main.version=0.1.0"
```

产物在 `build/bin/`（`wails.json` 的 `outputfilename` 为 `chronos`）。Linux 运行时
需要 `libs/libchronos.so`（cgo 链接时用 `-L native/build`，运行时用
`$ORIGIN/libs` rpath 或 `LD_LIBRARY_PATH`）；Windows 把 `chronos.dll` 放在
`chronos.exe` 同目录。

## 7. 故障排查

| 现象 | 原因 / 处理 |
| --- | --- |
| Linux `wails build` 报 `webkit2gtk-4.0` not found | Ubuntu 24.04 只有 4.1：加 `-tags webkit2_41` |
| `go build -tags chronos_native` 链接失败找不到 `-lchronos` | 先构建 `native/build`；cgo 路径是 `native/build`（`-L${SRCDIR}/../../native/build`） |
| 运行 CLI 报 `libchronos.so` 找不到 | `LD_LIBRARY_PATH=native/build`（CI 已设置） |
| Windows 运行 exe 报 DLL 找不到 | `chronos.dll` 必须在 exe 同目录，或目录在 PATH 中 |
| Windows cgo 报 `gcc: not found` | 安装 MSYS2 UCRT64 gcc / winlibs 并加入 PATH |
| `pnpm install --frozen-lockfile` 失败 | pnpm 版本必须与 `packageManager` 一致（pnpm 11）；CI 固定 `version: 11` |
| `go:embed frontend/dist` 失败 | `frontend/dist/.gitkeep` 被删；或前端构建未产出 index.html |
| `package` job 被跳过 | 预期行为：只在 push 到 `main` 或手动 dispatch 时运行 |
| `chronos info` 显示 `go-lanczos` | 构建时没带 `chronos_native`（或未链接原生库）；AI 模型无法加载 |
| UI 显示抑制 `unsupported` | 平台无驱动（macOS）或 Linux 无 systemd-inhibit/会话 D-Bus，见 `docs/INHIBIT.md` |

## 8. 与 Lead 负责文件的接口约定

工作流与脚本依赖以下由 Lead/其他 agent 维护的约定（2026-11-08 已对照仓库核对；
若实现变更，必须同步更新 `.github/workflows/*.yml`、`scripts/*` 与本文档）：

- 根包：`main.go` + `app.go`，Wails v2（go.mod 当前为 `wails/v2 v2.17.1`），
  `wails.json`（`outputfilename: chronos`，`frontend:install: pnpm install`，
  `frontend:build: pnpm build`）。
- 版本注入：`-X main.version`（根包变量 `version`；另有 `commit` / `buildTime`）。
  Wails CLI 由 `go list -m` 解析的版本安装，不写死 `@latest`。
- CLI：`./cmd/chronos`，子命令：
  `upscale -i/--input -o/--output -s/--scale -m/--model --model-id --tile --overlap
  --threads --fast --no-model --json`、`info [--json]`、`gen`、
  `models list|pull|path|import|dir`、
  `inhibit on|off|status --sleep --display --lock --auto-release --duration --reason --json`、
  `bench`。冒烟脚本只用 `upscale ... --no-model`、`inhibit status`、`info` 三者。
- 原生：`native/CMakeLists.txt` 目标 `chronos`（Linux `libchronos.so` 带
  `SOVERSION 1`；Windows `chronos.dll` + `libchronos.dll.a`）、`chronos_tests`
  （ctest 套件 `native_*`）、`chronos_native_cli`；选项 `CHRONOS_WITH_OPENVINO`、
  `CHRONOS_BUILD_TESTS`、`CHRONOS_SANITIZE`；产物在 `native/build/`。
- Go：模块 `github.com/w32394045-dotcom/bg2x-chronos`，cgo 实现只在
  `cgo && chronos_native` 下编译（`-L${SRCDIR}/../../native/build -lchronos`）；
  registry 单源是 `internal/models/registry.json`（`go:embed`）。
- 前端：`frontend/package.json`（`pnpm test`、`pnpm build`）、`pnpm-lock.yaml`、
  `frontend/dist/.gitkeep`；`packageManager` 为 pnpm 11。
- 冒烟测试用到的 CLI 行为：输出必须是 PNG（`-o` 显式 `.png` 或目录）、
  `inhibit status` 无头环境也返回退出码 0、`info` 打印 `engine: <backend>` 行。
