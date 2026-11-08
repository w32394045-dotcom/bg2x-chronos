# Project Chronos — 睡眠 / 锁屏抑制（Inhibit）

> 状态：实现位于 [`internal/inhibit`](../internal/inhibit)，最后更新 2026-11-08。
> 本文档描述行为契约与平台边界；不承诺任何"绝对不锁屏"的能力。

## 1. 目标与非目标

**目标**：长时间批处理（几十张 4K、几十分钟）期间，避免机器因为空闲而休眠、
熄屏或进入空闲锁屏，任务结束后恢复系统默认行为。

**非目标**：

- 不阻止用户**手动**锁屏（Windows `Win+L`、GNOME `Super+L`）——这是操作系统级
  用户意图，普通应用无权也无力拦截。
- 不改写系统电源策略（注册表 / gsettings / systemd 配置）；不申请管理员权限。
- 不保证突破企业组策略（"Interactive logon: Machine inactivity limit"、屏保强制
  密码等策略可能仍然生效）。

## 2. CLI 与 UI

```bash
chronos inhibit status
chronos inhibit on --sleep --display --lock
chronos inhibit on --auto-release --duration 10m   # 脚本化：到时/收到信号自动释放
chronos inhibit off
chronos inhibit selftest --json                    # 能力自检：启用→采样→释放，输出 JSON
chronos inhibit selftest --strict                  # 无能力/未持有时以非 0 退出（CI 用）
```

- 任一 flag 为真即视为"请求抑制"；`inhibit off` 等价于 `Set({})`，立即释放。
- `status` 永远以退出码 0 返回，通过 `driver` / `detail` / 各 `*Supported` 字段表达
  平台能力；只有真正的操作失败（例如启用系统调用失败）才返回非 0 退出码。
- `--auto-release` 只是把 `InhibitRequest.autoRelease` 置位（契约 §3.2）；真正的
  "任务结束自动释放"由 app 层在 job 结束时调用 `Set({})` 并推送 `chronos:inhibit`。
  命令行自身则一直持有，直到 `--duration` 到期、Ctrl+C 或进程退出。
- `selftest` 会以 `sleep+display+lock` 全量请求一次，采样 1.5 秒，再释放，然后输出
  自检结果（JSON 字段：`ok / driver / platform / detail / supported / held / active /
  released / heldSamples / startError / releaseError`）。默认即使平台不支持也返回
  退出码 0（只报告），`--strict` 才把 `ok=false` 变成失败，适合本地/CI 判定。
- UI 的抑制卡片显示 `active / sleep / display / lock / driver / sinceMs / detail`，
  不支持的能力置灰并展示 `detail` 的原因。

> 命令行具体 flag（含 `--sleep/--display/--lock/--auto-release/--duration/--reason/
> --json/--strict`）以 `cmd/chronos --help` 为准；本文描述的是行为契约。

## 3. 状态与语义

`internal/inhibit.Status`（JSON 字段见契约 §3.2）是唯一状态来源：

| 字段 | 语义 |
| --- | --- |
| `active` | 当前是否持有至少一个抑制 |
| `sleep` / `display` / `lock` | 当前**实际生效**的位（请求了但平台不支持时为 false） |
| `sleepSupported` / `displaySupported` / `lockSupported` | 平台能力探测结果 |
| `driver` | `windows-execution-state` / `linux-logind+screen-saver` / `unsupported` |
| `detail` | 面向用户的中文说明（含平台注意事项） |
| `sinceMs` | 激活起始时间（unix ms）；0 表示未激活 |

状态机（`Manager.Set`）：

1. 新请求为空（所有位为 false）→ 释放当前驱动 → 返回空闲状态。
2. 新请求非空 → **先释放再重新启用**（这样切换勾选项时立即生效，不会累积旧位）。
3. 启用失败 → 状态回落到空闲，并在 `detail` 追加失败原因；CLI/UI 能看到。
4. `AutoRelease` 不是驱动关心的事：由 app 层在 job 结束后调用 `Set({})`，并推送
   一次 `chronos:inhibit` 事件。

## 4. Windows 实现

`internal/inhibit/driver_windows.go`

- 调用 `SetThreadExecutionState`（kernel32，`syscall.NewLazyDLL`，无外部依赖）。
- 标志位组合：
  - 始终 `ES_CONTINUOUS`（0x80000000）；
  - `--sleep` → 追加 `ES_SYSTEM_REQUIRED`（0x00000001）；
  - `--display` 或 `--lock` → 追加 `ES_DISPLAY_REQUIRED`（0x00000002）。
- **线程作用域**：`SetThreadExecutionState` 的状态绑定在调用线程上，因此驱动用一个
  `runtime.LockOSThread()` 锁定的专用 goroutine 持有状态；期间每 30 秒重设一次
  （`refreshInterval`），防止外部因素清除。
- 释放：向该 goroutine 发停止信号，它在退出前调用
  `SetThreadExecutionState(ES_CONTINUOUS)` 清除之前的请求。
- 能力矩阵：`sleep/display/lock` 都报告 supported=true，但 **`lock` 是间接的**：
  - `ES_DISPLAY_REQUIRED` 让系统认为有活动，从而抑制"空闲 → 屏保 → 自动锁屏"链路；
  - 如果用户配置了"机器不活动限制"策略或屏保强制锁定，仍可能锁屏；
  - 手动 `Win+L` 无法阻止。
- 该驱动没有提升权限要求；对电源计划无副作用；进程退出/Goroutine 异常时状态随线程
  结束自动失效（线程被销毁后其执行状态要求不再生效）。

## 5. Linux 实现

`internal/inhibit/driver_linux.go`（依赖 `github.com/godbus/dbus/v5`，仅 Linux）

两条相互独立的抑制通道：

1. **systemd-logind**：启动子进程
   `systemd-inhibit --what=sleep:idle:handle-lid-switch --who=Chronos --why=<reason>
   --mode=block sleep infinity`。
   - `sleep` 阻止挂起/休眠；`idle` 阻止因空闲进入睡眠/熄屏；`handle-lid-switch`
     让合盖不触发休眠（不会禁用合盖动作本身，也不会改变用户电源设置）。
   - 子进程放在独立进程组（`Setpgid`），释放时向整个进程组发 `SIGTERM`，保证
     包装的 `sleep infinity` 一并退出。
   - 能力探测：`exec.LookPath("systemd-inhibit")` 成功才报告 sleep/display 可用；
     无 systemd 的发行版/容器内为 false。
2. **org.freedesktop.ScreenSaver**（会话 D-Bus）：调用
   `Inhibit(app="Chronos", reason)` 拿到 cookie，释放时 `UnInhibit(cookie)`。
   - 用于阻止空闲锁屏；GNOME/KDE/XFCE 等实现了该接口的桌面有效。
   - 能力探测：会话总线可达（`dbus.SessionBus()`）即报告 `lock` 可用；实际接口
     不存在时 `Start` 会记录失败，`sleep/display` 仍可独立生效。
   - 启动失败不会让整体失败：只要有任意一条通道成功，`Start` 返回 nil，并在
     `detail`/日志中保留另一条的失败原因。
- 两条通道都失败（或系统既无 systemd-inhibit 也无 D-Bus）→ 返回 `ErrUnsupported`，
  UI 显示"当前平台不支持"。

**GNOME / Wayland 的边界**：

- GNOME 的 `org.freedesktop.ScreenSaver` 只是兼容名，实际由 GNOME Shell 实现；
  在 Wayland 下是否受理由合成器决定，某些版本/配置会忽略第三方锁屏抑制。
- `systemd-inhibit` 通过 logind 工作，与显示服务器无关，Wayland 下同样有效。
- 未实现 `org.freedesktop.portal.Inhibit`（portal 的 inhibit 语义是"应用正在展示
  内容"，与长任务不完全匹配）；若将来需要兼容纯 portal 环境（如 Flatpak），可加
  一条 portal 通道。
- 无会话 D-Bus（如 ssh/headless）时 `lock` 不可用；`systemd-inhibit` 在 headless
  session 下仍可能可用。

## 6. 其他平台

macOS 尚未构建支持。`driver_other.go`（`!windows && !linux`）返回一个空驱动：
`driver="unsupported"`，所有能力为 false，`Start` 返回 `ErrUnsupported`。UI 因此显示
不支持而不是报错。macOS 的等价物（`IOPMAssertionCreateWithName`）列入路线图。

## 7. 能力矩阵

| 平台 / 桌面 | 阻止睡眠 | 阻止熄屏 | 阻止空闲锁屏 | 备注 |
| --- | --- | --- | --- | --- |
| Windows 10/11 | 是 | 是 | 部分（间接） | `ES_CONTINUOUS` + 各 flag；手动 Win+L 与组策略无法阻止 |
| Linux + systemd + GNOME/KDE/X11 | 是 | 是 | 是（桌面实现 ScreenSaver 时） | systemd-inhibit + ScreenSaver cookie |
| Linux + systemd + GNOME Wayland | 是 | 是 | 视合成器实现 | logind 有效；ScreenSaver 可能被忽略 |
| Linux 无 systemd（容器等） | 否（除非另有 D-Bus） | 否 | 若桌面实现 ScreenSaver 则可 | `driver` 仍为 `linux-logind+screen-saver`，能力字段为 false |
| Linux 无会话 D-Bus | 是（若有 systemd-inhibit） | 是 | 否 | headless/ssh 场景 |
| macOS | 否 | 否 | 否 | `driver=unsupported`，planned |

## 8. 测试与排查

- 单元测试：`internal/inhibit/inhibit_test.go` 用假 Driver 覆盖状态机、部分能力、
  失败回落、幂等 Stop；不触碰真实 OS API。
- 能力自检（无需跑完整任务）：

  ```bash
  chronos inhibit selftest --json      # 报告 ok/driver/supported/held/released
  chronos inhibit selftest --strict    # 无能力时退出码非 0
  ```

- **CI 的边界**：GitHub Actions 的 runner 没有真实桌面会话（ubuntu 是 headless
  container，Windows runner 是服务会话），因此 CI 只跑单元测试，**不**调用
  `inhibit selftest --strict`（headless 下必然没有抑制能力，会误报失败）。真实
  抑制能力必须在带桌面会话的机器上手动验收：`selftest --json` + `powercfg /requests`
  （Windows）/ `systemd-inhibit --list`（Linux）。CI 的 `browser-smoke` 与此无关，
  它只验证 HTTP 层。
- 手动验证（Windows）：
  ```powershell
  chronos inhibit selftest --json
  chronos inhibit on --sleep --display --lock
  chronos inhibit status          # active=true, driver=windows-execution-state
  powercfg /requests              # 应能看到 Chronos 的 EXECUTION 请求
  chronos inhibit off
  powercfg /requests              # 请求消失
  ```
- 手动验证（Linux）：
  ```bash
  chronos inhibit selftest --json
  chronos inhibit on --sleep --display --lock
  systemd-inhibit --list | grep -i chronos
  chronos inhibit status
  chronos inhibit off
  ```
- 常见问题：
  - `inhibit status` 显示 `unsupported`：Windows 上几乎不会；Linux 上检查
    `systemd-inhibit` 是否在 PATH、`DBUS_SESSION_BUS_ADDRESS` 是否存在。
  - `active=true` 但系统仍休眠：多半是组策略/桌面电源管理覆盖，或（Linux）没有
    systemd-logind。
  - 任务结束后仍在抑制：检查 `AutoRelease` 是否为 true；UI 的"手动关闭"等价于
    `inhibit off`。
