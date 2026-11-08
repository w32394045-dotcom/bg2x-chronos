# Project Chronos — 浏览器模式（Browser Mode）

> 状态：随 `chronos serve`（`cmd/chronos`）与 `internal/browser` 实现；
> 最后更新：2026-11-08。HTTP/鉴权细节的契约见
> [CONTRACTS.md §7](CONTRACTS.md)（契约 v2 起包含浏览器模式）。

## 1. 为什么有浏览器模式

桌面版依赖平台 WebView：Windows 需要 WebView2 Evergreen Runtime，Linux 需要
WebKitGTK（`libwebkit2gtk-4.1`）。这两样在以下场景经常缺失：

- 精简版 Windows / 未安装 Evergreen Runtime 的机器；
- 无桌面环境的 Linux（CI、服务器、WSL）或没装 WebKitGTK 的发行版；
- 远程/容器环境（只有浏览器可用）。

浏览器模式把 UI 跑在用户自己的浏览器里，由 Go 进程提供本地 HTTP 服务与 JSON API。
它**不是**把前端另做一份：同一个 Vue 构建产物、同一套 `uiapp.Core` 业务逻辑、同一套
C++ 原生推理，只是传输层从 Wails bindings 换成 HTTP + SSE。

## 2. 启动方式

### 命令行（推荐，Linux 无 WebKit 场景）

```bash
./chronos serve                 # 默认 127.0.0.1 + 随机端口 + 自动打开浏览器
./chronos serve --no-open       # 只打印 URL，不打开浏览器
./chronos serve --port 8787     # 固定端口
./chronos serve --json          # stdout 输出一行 JSON（脚本/CI 用）
./chronos serve --assets DIR    # 显式指定前端构建目录（默认自动查找）
```

`--json` 时 stdout 只有一行，便于解析：

```json
{"addr":"127.0.0.1:55953","mode":"browser","pid":5244,"url":"http://127.0.0.1:55953/"}
```

`assets: <dir>`、普通日志与错误写 stderr。进程收到 Ctrl+C / SIGTERM 后关闭监听并退出
（清理 D-Bus/电源抑制等资源）。

### 打包产物里的启动器

- Linux 浏览器模式包（`chronos-<ver>-linux-amd64-web.tar.gz`）：

  ```
  chronos-web                 # #!/bin/sh: cd 到自身目录，exec ./chronos serve --open "$@"
  chronos                     # headless CLI
  web/                        # 前端构建（index.html + assets/）
  libs/libchronos.so
  models/registry.json
  README.md, LICENSE
  ```

  解压后直接 `./chronos-web`（或 `./chronos serve --open`）。`chronos-web` 会设置
  `LD_LIBRARY_PATH=<dir>/libs`，因为 CLI 没有 rpath。

- Windows zip：`chronos-web.cmd` 双击等于 `chronos-cli.exe serve --open %*`；
  `chronos-cli.exe`、`chronos.dll` 与 `web/` 必须在同一目录。GUI 的 `chronos.exe`
  在检测不到 WebView2 时也会自动进入浏览器模式（见 §4）。

### 前端资产查找顺序

`internal/browser.ResolveAssets` 依次尝试：

1. `--assets DIR`
2. `CHRONOS_ASSETS` 环境变量
3. `<exeDir>/web`
4. `<exeDir>/frontend/dist`
5. `<exeDir>/dist`
6. `cwd/frontend/dist`
7. `cwd/web`

目录里必须有 `index.html`，否则拒绝启动并报
`UI assets not found; build the frontend (cd frontend && pnpm build) or set CHRONOS_ASSETS/--assets`。
CI 的 `browser-smoke` 用 `frontend-dist` artifact 提供这些资产。

## 3. 安全模型

浏览器模式监听的是 **loopback HTTP**，安全边界与桌面模式不同，设计如下：

| 机制 | 实现 | 防的是什么 |
| --- | --- | --- |
| 只绑定 127.0.0.1 | `serve --addr` 默认 `127.0.0.1`，不监听 0.0.0.0 | 局域网/公网直接访问 |
| 随机端口 | `--port 0`（默认），内核分配空闲端口 | 固定端口被预占/猜测的成本 |
| 会话 token cookie | 启动时生成 32 字节随机 token；`GET /` 下发 `chronos_session`，`HttpOnly; SameSite=Strict; Path=/` | 第三方网页携带 cookie；JS 读取 token |
| Origin 白名单 | 请求带 `Origin` 时必须等于 `http://127.0.0.1:<port>` | 跨站请求 / DNS rebinding（攻击者域名的 Origin 不匹配） |
| 方法限制 | `/api/*` 只接受 `POST`；`/events` 需要 cookie | 简单 GET 表单跨站触发 |
| 响应头 | `X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、`X-Frame-Options: DENY` | MIME 嗅探、referer 泄露、被 iframe 嵌套 |
| 请求体上限 | 4 MiB | 内存放大攻击 |
| 资产路径 | `path.Clean` + `fs.Open`，越界路径 404 | 目录穿越 |
| UI 注入 | `GET /` 在 `</head>` 前注入 `window.__CHRONOS_HTTP__=true` | 前端据此选择 HTTP transport |

**诚实的边界**：

- 这是**本机单用户**模型。同机同用户的其它进程可以自己 `GET /` 拿到 cookie 再调用
  API——token 不是针对本机恶意软件的隔离机制（那需要 OS 级沙箱/签名）。
- 没有 TLS：loopback 明文 HTTP 是刻意的（本地回环，无需证书）。
- `GET /` 只用于取 UI 与 cookie，不暴露任何业务数据；所有敏感操作都在
  `POST /api/*` 后校验 cookie。
- 浏览器自身的进程/标签页隔离、扩展、以及用户计算机的完整性不在本项目威胁模型内。
- 不做 CORS 头：浏览器会阻止跨源 JS 读取响应；服务端再用 Origin + cookie 做纵深防御。
- `Origin` 只在请求携带该头时校验（浏览器发起的 `POST`/SSE 都会带）；不带 Origin 的
  本地客户端（curl/脚本）允许，但仍必须带 `GET /` 下发的会话 cookie——契约 §7 明确
  了这一点，`scripts/browser-smoke.sh` 正是按这个顺序做的。

## 4. GUI 自动回退

`main.go` 在启动 Wails 前判断：

1. 显式开关：环境变量 `CHRONOS_BROWSER=1` 或参数 `--browser`；
2. `desktopAvailable()`：
   - Windows：查找 WebView2 Runtime 目录（`Program Files (x86)` / `Program Files` /
     `%LOCALAPPDATA%` 下的 `Microsoft\EdgeWebView\Application`）；
   - Linux：`DISPLAY` / `WAYLAND_DISPLAY` 非空，或 `XDG_SESSION_TYPE=wayland`；
   - 其它平台按可用处理。
3. 若 `wails.Run` 仍然失败（驱动/运行时异常），捕获错误后自动改用浏览器模式。

回退时使用 **编译进二进制的前端资产**（根包 `go:embed all:frontend/dist`），因此
`chronos.exe` 单文件即可工作，不需要 `web/` 目录。

## 5. HTTP / SSE API

所有 App 方法都可通过 API 调用，等价于桌面模式的 Wails bindings。

```http
POST /api/GetAppInfo
Content-Type: application/json
Cookie: chronos_session=<token>

{"args":[]}
```

响应：

```json
{"result":{"version":"0.1.0","mode":"browser","browseSupported":true,...}}
```

错误响应是 HTTP 200 + `{"error":"..."}`（参数/业务错误），鉴权失败是 HTTP 401。

方法：`GetAppInfo`、`ListModels`、`RefreshModels`、`UpscaleBatch`、`CancelJob`、
`SetConcurrency`、`GetConcurrency`、`SetInhibit`、`GetInhibitStatus`、`PickImages`、
`PickOutputDir`、`Reveal`、`OpenDataDir`、`ListDirectory`。

- `ListDirectory(path)`：浏览器模式的路径选择器（浏览器拿不到本地绝对路径）。
  `path` 为空时返回用户主目录；返回 `{path, parent, entries:[{name,path,isDir,size,isImage}]}`。
- `PickImages` / `PickOutputDir` 在浏览器模式返回空（改用 `ListDirectory` 选择）；
  `AppInfo.browseSupported` 为 true 表示 UI 应使用服务端路径选择器。

事件（SSE）：

```http
GET /events
Cookie: chronos_session=<token>
Accept: text/event-stream
```

```
data: {"name":"chronos:progress","data":{"jobId":"...","done":3,"total":16,...}}
```

事件名与契约 §3.3 一致：`chronos:progress` / `chronos:job` / `chronos:log` /
`chronos:inhibit`。服务端每 20s 发一次 `: ping` 保活；慢客户端事件会被丢弃而不是阻塞调度。

手动调试（与 `scripts/browser-smoke.sh` 相同）：

```bash
./chronos serve --no-open --port 0 --json > serve.json 2> serve.err &
URL=$(sed -n 's/.*"url":"\([^"]*\)".*/\1/p' serve.json)
PORT=${URL%/}; PORT=${PORT##*:}
curl -s -c jar -o /dev/null "$URL"
curl -s -b jar -H "Origin: http://127.0.0.1:$PORT" \
     -H 'Content-Type: application/json' -d '{"args":[]}' "$URL/api/GetAppInfo"
curl -s -b jar -H "Origin: http://127.0.0.1:$PORT" \
     -H 'Content-Type: application/json' -d '{"args":[""]}' \
     "$URL/api/ListDirectory"
```

## 6. 健康检查与运维

- `GET /healthz` 返回 `ok`（无需 cookie），可用于脚本确认端口就绪。
- 浏览器模式进程与桌面版共享数据目录/模型目录/设置文件（`internal/config`、
  `internal/models`），两种模式可以交替使用。
- 睡眠/锁屏抑制在浏览器模式同样生效（服务端 `internal/inhibit`），
  见 [INHIBIT.md](INHIBIT.md)；`chronos inhibit selftest --json` 可在无桌面会话时
  检查能力位。
- 任务在浏览器关闭后继续运行；重新打开 URL（同一进程）可继续观察。进程退出会取消
  正在进行的任务（与桌面版一致，属于进程生命周期语义）。

## 7. 限制

- 浏览器模式无法获得拖拽文件的绝对路径，只能用服务端路径选择器；
  本地文件预览等依赖文件系统路径的功能以 UI 实际支持为准。
- 单进程单用户；没有多用户/远程访问设计，`--addr` 改成非 loopback 需要自担风险
  （鉴权模型只按本机设计）。
- 无 TLS、无登录页；不要把端口暴露到公网。
- 浏览器模式不替代桌面版：没有原生窗口、文件对话框、托盘等桌面集成。
- 前端资产不匹配（例如用旧 `web/` 配新二进制）时 API 字段可能新增，前端做可选处理
  （契约 §6 只增不删）。

## 8. CI 与冒烟

`scripts/browser-smoke.sh` 是唯一自动化入口（CI：`native-integration` 的 ubuntu 腿）：

```bash
# 需要先构建 CLI；资产可用 frontend/dist 或任意含 index.html 的目录
CHRONOS_ASSETS_DIR="$PWD/frontend-dist" bash scripts/browser-smoke.sh ./chronos
```

它检查：

1. `serve --no-open --port 0 --json` 输出可解析的 loopback URL；
2. `GET /` 返回 200 且注入 `window.__CHRONOS_HTTP__=true`；
3. `POST /api/GetAppInfo`（cookie + Origin）返回 `"mode":"browser"`；
4. `POST /api/ListDirectory` 返回 HTTP 200；
5. SIGTERM 后进程退出（无残留）。

Windows 的浏览器模式打包（zip 内含 `chronos-cli.exe` + `web/`）在 CI 的 `package`
job 里组装，但没有单独的 Windows HTTP 冒烟（Windows GUI 冒烟见
`scripts/windows-smoke.ps1`）。
