# chronos native — C++17 算力层

Project Chronos 的原生（C++17）计算层：无 OpenCV 依赖、图像 IO 用 vendored `stb_image`，
推理张量用 OpenVINO 原生 `ov::Tensor`。对外只暴露冻结的 C ABI
`native/include/chronos/chronos.h`（见 `docs/CONTRACTS.md` §1/§2/§4），像素数据永远不跨
Go/C++ 边界，只传文件路径。

## 目录

```
native/
  include/chronos/   public headers（chronos.h 为冻结 ABI；其余为内部 C++ API + 测试用）
  src/               实现（image / tiling / blending / resample / backend / pipeline / c_api / cli）
  tests/             自研极简测试框架 + 8 个测试套件（ctest 注册）
  third_party/       stb_image.h, stb_image_write.h（vendored，不修改）
  CMakeLists.txt
```

构建产物（全部落在 `native/build/`，便于 CI/Go 用 `-L native/build -lchronos`）：

| 产物 | 说明 |
| --- | --- |
| `chronos.dll` / `libchronos.so` | 共享库，target `chronos` |
| `libchronos.dll.a` / 无 | Windows 导入库 |
| `chronos_native_cli` | 阶段一手工验证 CLI |
| `chronos_tests` | 测试程序（按 suite 注册多次到 ctest） |

## 构建（Windows · MinGW-w64）

```powershell
$env:PATH = 'C:\Users\ptfm\tools\mingw64\bin;C:\Users\ptfm\tools\cmake-4.4.5-windows-x86_64\bin;C:\Users\ptfm\tools;' + $env:PATH
# 注意：PATH 里 mingw64 自带一个残缺的旧 cmake，务必用完整路径调用 tools 下的 cmake
& 'C:\Users\ptfm\tools\cmake-4.4.5-windows-x86_64\bin\cmake.exe' -S native -B native/build -G Ninja `
    -DCMAKE_BUILD_TYPE=Release -DCMAKE_C_COMPILER=gcc -DCMAKE_CXX_COMPILER=g++
& 'C:\Users\ptfm\tools\cmake-4.4.5-windows-x86_64\bin\cmake.exe' --build native/build
& 'C:\Users\ptfm\tools\cmake-4.4.5-windows-x86_64\bin\cmake.exe' --build native/build --target install
ctest --test-dir native/build --output-on-failure
```

Windows 上 `chronos` 以 `-static` 链接（自包含 DLL，不依赖 libstdc++/libgcc/libwinpthread
DLL），方便 Go 把 `chronos.dll` 拷到 exe 同目录。

## 构建（Linux · g++）

```bash
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build native/build
ctest --test-dir native/build --output-on-failure
cmake --install native/build --prefix native/install
```

Linux 下 `libchronos.so` 带 `SONAME libchronos.so.1`，Go 侧用 `rpath=$ORIGIN/libs` 或
`LD_LIBRARY_PATH`（CONTRACTS §4）。

## CMake 选项

| 选项 | 默认 | 说明 |
| --- | --- | --- |
| `CHRONOS_WITH_OPENVINO` | OFF | 打开后 `find_package(OpenVINO COMPONENTS Runtime)`，编译真正的 OpenVINO 后端并链接 `openvino::runtime` |
| `CHRONOS_BUILD_TESTS` | ON | 构建 `chronos_tests` 并注册 ctest |
| `CHRONOS_SANITIZE` | OFF | GCC/Clang（Linux/macOS）下追加 `-fsanitize=address,undefined`（含链接）；MinGW 无 libasan，会给出警告并跳过 |
| `CHRONOS_WARNINGS_AS_ERRORS` | OFF | `-Werror`（MSVC `/WX`） |

始终启用 `-Wall -Wextra`（MSVC `/W4 /permissive-`）；stb 实现单独 TU 静音编译。

## OpenVINO 构建

```bash
cmake -S native -B native/build -DCHRONOS_WITH_OPENVINO=ON -DCMAKE_BUILD_TYPE=Release
cmake --build native/build
# 运行时可用 CHRONOS_OPENVINO_DEVICE=CPU / GPU.0 / AUTO 覆盖设备选择
```

没有 `-DCHRONOS_WITH_OPENVINO=ON` 时 `backend_openvino.cpp` 只提供
`openvino_available() == false` 与一条明确报错的 factory，其它代码无需 `#ifdef`；
`chronos_backend_available(CHRONOS_BACKEND_OPENVINO)` 返回 0，Go 据此禁用“AI 模型”选项。

模型约定（OpenVINO 后端）：

* 输入 `[1,3,H,W]` 静态形状、H==W（tile 尺寸由模型决定，pipeline 自动适配）；元素类型
  `f32`（输入归一化到 `[0,1]`）/ `f16` / `u8`（原始 0..255）。
* 输出 `[1,3,H*s,W*s]`；`f32/f16` 输出会按 patch 最大值自动判断值域（`<=2` 视为 `[0,1]`
  并乘 255，否则视为已是 0..255）；`u8` 直接使用。
* 支持 `.xml`（IR）与 `.onnx`，设备优先 GPU（`device_index` 选第 N 块 GPU），否则 CPU。
* N 个 `ov::InferRequest` 异步流水：设备推理与宿主端 pre/post-process 重叠；
  取消时对所有 in-flight request 调 `cancel()`。

## 内部设计与算法

### 分块（tiling.h / tiling.cpp）

* 默认 `tile=256, overlap=16, step=240`（CONTRACTS §2）。
* 每轴 tile 原点：从 0 开始按 `step` 递增；最后一个原点被 clamp 到 `length - tile`
  以保证 tile 完全落在图内；`length <= tile` 时只产生一个 tile，缺的部分用**边缘复制**
  补齐。图像任意尺寸、任意 tile/overlap（`0 <= overlap < tile_size`）都保证输出画布
  `(w*scale, h*scale)` 的每个像素至少被一个 tile 覆盖（`tiles_cover_output` 可独立验证）。
* 每个 tile 读取 `tile_size²` RGB（RGBA 输入只取 RGB 做推理，alpha 单独处理）。

### 羽化混合（blending.h / blending.cpp）

* 每轴权重为“抬升线性斜坡 + 1px 地板”：
  `w(i) = max(1, min(i+1, size-i))` → `1,2,3,...,3,2,1`，端点权重恒 ≥1，不会出现除零。
* 累加：`acc += patch * wx * wy`，`wsum += wx * wy`（float，逐像素），最后
  `out = round(acc / wsum)`。因此重叠区是**加权平均**而非硬拼接：
  * 常量输入 → 输出 bit-exact 常量（零方差，测试覆盖）；
  * 相邻 tile 在缩放后坐标上恰好相差 `step*scale`，两个斜坡之和为常数
    `overlap*scale+1`（常规 overlap ≤ tile/2 时严格成立），梯度无接缝。
* 权重先在 scaled 坐标上计算（patch 尺寸），所以 scale 不影响权重形状。

### 重采样（resample.h / resample.cpp）

* 可分离两趟：水平 pass 写 float 中间缓冲，垂直 pass 量化到 8-bit。
* Lanczos3（`sinc(x)·sinc(x/3)`，支撑 |x|<3）与 bilinear（`CHRONOS_FLAG_FAST`）。
* 下采样时按 `scale = src/dst` 加宽核（正确的 prefilter），不会走样；权重逐输出像素归一化，
  常量输入严格不变；scale 相同直接 `memcpy`（stub scale=1 是精确恒等）。
* 权重表在构造时生成一次，`run()` 内零分配，可被多个线程并发调用（各自 scratch）。

### 后端（backend.h / backend_stub.cpp / backend_openvino.cpp）

* `IBackend` 两条路径：
  * `infer_tile()`：pipeline 线程池逐 tile 调用（stub 后端，线程安全）。
  * `run_batch()`：后端自己掌握并行（OpenVINO 的 N 深度异步 request 流水），
    pipeline 通过 `PatchSink` 消费 patch，避免为整图缓存 patch。
* stub 后端：无模型，Lanczos3/bilinear 上采样，支持 scale ∈ {1,2,3,4}，
  `describe()` 返回 `stub/Lanczos3` 或 `stub/Bilinear`。

### Pipeline（pipeline.h / pipeline.cpp）

单 session 串行处理多张图：`load → plan → pad/crop → 并行 tile 推理 → 羽化 → 写盘`。

* 线程池随 session 常驻（`options.threads`，`0 ⇒ hardware_concurrency`，硬上限 8），
  每张图 fork/join 一次；每个 worker 的 tile/patch 缓冲与 resampler scratch 在**每张图**
  开头分配一次并复用，tile 热循环内**零堆分配**。
* 进度：`done/total` 均以 tile 计，`std::atomic<int>`；Go 侧 50ms 轮询。
* 取消：`std::atomic<bool>`，在每个 tile 边界检查，尽快返回 `CHRONOS_ERR_CANCELLED(-6)`。
* alpha：`CHRONOS_FLAG_PRESERVE_ALPHA` 时输出 RGBA，alpha 平面整体按同样 filter 重采样后
  合并；未设置时输出 RGB（alpha 丢弃）。
* 输出格式按扩展名：`.png` / `.jpg|.jpeg`（质量 95） / `.bmp`，其它扩展名直接 IO 错误
  （不静默改格式）。UTF-8 路径在 Windows 上通过 `STBI_WINDOWS_UTF8` 支持。

### 峰值内存（重要）

设输出像素数 `P = (w*scale) * (h*scale)`，通道 `C = 3`（RGB）或 `4`（保留 alpha）：

```
羽化累加      P * (C_f * 4 + 4) 字节      C_f = 3（内部推理始终 RGB）
输出图像      P * C 字节
输入图像      w * h * channels 字节
每 worker     tile² * 3 + patch² * 3 字节 + resampler scratch(≈ patch*tile*3*4 字节)
alpha（可选） (w*h + P) 字节 + 重采样 scratch(out_w * h * 4 字节)
```

例：1080p (1920×1080) → 4x = 7680×4320，P≈33.2M：羽化 ≈ 531 MB + 权重 ≈ 133 MB、
输出 ≈ 100 MB、输入 ≈ 6 MB、8 个 worker 至多约 50 MB ⇒ **峰值约 0.8 GB**。
4K → 4x（P≈132M）峰值约 2.1 GB；`kMaxOutputPixels`（256M 像素）会把更大的请求直接
以参数错误拒绝，而不是让进程 OOM。后续优化方向：按 tile 行分带累加，把羽化缓冲从
`P` 降到 `out_w * patch_h` 量级。

## C ABI 行为（与 Go 对接）

* `chronos_abi_version()` = 1；`chronos_backend_name()` 精确返回 `"none"` / `"stub"` /
  `"openvino"`；`chronos_backend_available()`：stub=1，OpenVINO 仅在
  `-DCHRONOS_WITH_OPENVINO=ON` 构建里为 1。
* `chronos_options`：`struct_size` 必须为 `sizeof(chronos_options)`，**0 表示用默认值**；
  `tile_size<=0 ⇒ 256`，`overlap<=0 ⇒ 16`，`threads<=0 ⇒ hardware_concurrency`；
  `reserved[4]` 必须为 0；未知 flag 位、`tile_size<8`/`>8192`、`overlap>=tile_size`、
  `device_index<0` 均返回 `NULL` + err。`overlap<=0` 时按 16 处理，因此极小 tile
  （如 tile=8）需显式给 `overlap`（1..7）。
* `chronos_session_describe()` 返回写入的字节数（不含 NUL），buf 始终 NUL 结尾；
  `buf==NULL || buf_len<=0` 时返回所需长度。`chronos_last_error()` 是 thread-local 字符串，
  永不 NULL，成功调用后清空。
* `chronos_process()` 返回 0 或负错误码；`out_width/out_height` 可为 NULL。
* `chronos_session_progress/cancel/cancelled/reset_progress` 只碰 session 内原子量，
  可从 Go 的轮询/取消 goroutine 调用（Go 约定一个 session 只被一个 worker 用于
  `chronos_process`，进度/取消是唯一的跨 goroutine 访问，安全）。
* `chronos_session_free(NULL)` 安全；所有入口 `try/catch(...)`，异常绝不越过 ABI 边界。
* `chronos_session_create` 传入 model path 而当前构建没有 OpenVINO 时返回 NULL，
  err 中包含 “OpenVINO”。

## 测试

`chronos_tests` 使用 `native/tests/harness.h`（无第三方依赖），共 **51 个用例**，ctest 按 suite
注册为 10 个测试项：

```
native_tiling     分块网格/覆盖无缝隙/边缘 clamp/非法参数/边缘复制
native_blending   权重斜坡、重叠权重和恒定、常量图零方差、scale1 梯度精确还原、裁剪
native_resample   kernel 形状、尺寸、常量保持、scale1 恒等、2x 梯度、下采样、4 通道
native_image_io   PNG/BMP/JPEG roundtrip、灰度/RGBA 转换、扩展名识别、UTF-8 路径、IO 错误
native_pipeline   文档用例 64x48→128x96(tile32/overlap8/scale2)、常量无接缝、scale1 恒等、
                  fast/alpha/session 复用/progress/取消/参数校验/线程数一致性
native_c_api      ABI、backend 名称/可用性、last_error thread-local、options 校验、-1/-4/-6、
                  err 缓冲 NUL 结尾、目录/垃圾文件不抛异常
native_threads    2 线程 × 2 session 并发、last_error 线程隔离
native_all        全部跑一遍
native_cli_info   动态库可加载 + --info 输出
native_cli_process 通过 chronos.dll 端到端跑一次真实图片（fixture 由 native_c_api 生成）
```

注意：ctest 运行 Windows 版 `chronos_tests.exe` 需要 `C:\Users\ptfm\tools\mingw64\bin` 在 PATH
（MinGW 运行库；测试程序本身以 `-static` 链接以便无 PATH 也能跑，但 CLI/共享库仍依赖加载器行为）。
CI 上（Linux g++）无此要求。

## CLI

```
chronos_native_cli --in a.png --out b.png [--model m.xml] [--scale 4] [--tile 256]
                   [--overlap 16] [--threads N] [--fast] [--progress] [--info]
```

输出为逐行 JSON-ish 事件（`session` / `progress` / `done` / `error`），退出码即 C ABI 返回码。
`--info` 打印 ABI/version/backend 可用性，可用于 Ubuntu CI 手工验证。
Windows 上 CLI 先用 `GetCommandLineW` + `CommandLineToArgvW` 取 UTF-16 命令行再转 UTF-8，
因此中文路径可用（这是 native 树里唯一出现 `windows.h` 的地方，库本身完全可移植）。

## 已知限制 / Phase 1 范围

* 无 EXIF 方向校正（stb 不支持）；JPEG 输入按解码后的像素处理。
* alpha 通道不与 RGB 一起走 tile 推理，而是整图重采样后合并（stub/OV 一致）。
* OpenVINO 后端要求静态输入形状且 H==W；动态 shape / NHWC 模型需先转换。
  本机未安装 OpenVINO，该 TU 已用桩头文件做过 `-DCHRONOS_WITH_OPENVINO=1 -Wall -Wextra
  -Werror` 语法校验，但真实链接/推理仍需在带 OpenVINO 的 CI 上验证。
* 羽化缓冲区为整画布 float（见“峰值内存”），超大图会以参数错误拒绝；分带累加列为后续优化。
* session 的 cancel 位一旦置位对后续图片保持有效（Go 每个任务应各自 create/free session）。
