# Project Chronos 路 bg2x-chronos

> **鎶婂浘鍍忔竻鏅板害涓庡鐞嗘椂闂翠竴璧烽噸鍐欍€?* 鍘熺敓妗岄潰 AI 瓒呭垎宸ヤ綔绔欙細**Webview (UI) + Go (璋冨害) + C++ (绠楀姏)**銆?> 鎵挎帴 [isixe/bg2x](https://github.com/isixe/bg2x) 鐨勪骇鍝佺悊蹇碉紝褰诲簳鎶涘純娴忚鍣ㄦ矙绠辩殑鎬ц兘澶╄姳鏉裤€?
[![CI](https://github.com/w32394045-dotcom/bg2x-chronos/actions/workflows/ci.yml/badge.svg)](https://github.com/w32394045-dotcom/bg2x-chronos/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

---

## 涓轰粈涔堥噸鍐?bg2x

bg2x 鐢?WebAssembly / WebGPU 鍦ㄦ祻瑙堝櫒閲岃窇瓒呭垎锛屼紭鐐规槸闆跺畨瑁呫€侀殣绉佸ソ锛涗唬浠锋槸锛?
| 鐥涚偣 | 娴忚鍣ㄦ柟妗堬紙bg2x锛?| Chronos 鍘熺敓鏂规 |
| --- | --- | --- |
| 鍐呭瓨 | WASM 绾挎€у唴瀛?+ JS GC锛屽ぇ鍥?鎵归噺鏄?OOM | C++ 鑷湁缂撳啿鍖猴紝鎸?tile 娴佹按锛屽嘲鍊煎唴瀛樺彲棰勬祴 |
| 绾跨▼ | Web Worker 璋冨害銆佷富绾跨▼鏄撳崱椤?| Go worker pool + C++ 绾跨▼姹狅紝UI 姘镐笉闃诲 |
| GPU | WebGPU 鏀寔闈㈢獎銆侀┍鍔ㄥ吋瀹瑰樊 | Intel OpenVINO锛堟牳鏄?鐙樉锛夊紓姝ユ帹鐞?|
| 澶у浘 | 鏁村浘涓€娆￠€佽繘鏄惧瓨 | 256脳256 鍒囩墖 + 16px 缇藉寲閲嶅彔锛屾棤鎺ョ紳 |
| 璁¤垂/闅愮 | 绾墠绔紝鏃犲悗绔?| 鍚屾牱鍏ㄦ湰鍦帮紝涓嶈仈缃戯紙闄ゆā鍨嬩笅杞斤級 |

**鏂板鑳藉姏锛氬鐞嗘湡闂村彲閫夋嫨涓嶈繘鍏ョ潯鐪犮€佷笉鍏抽棴灞忓箷銆佷笉绌洪棽閿佸睆**锛圵indows / Linux锛岃 [`docs/INHIBIT.md`](docs/INHIBIT.md)锛夈€傛壒閲忚窇鍑犲崄寮犺€佺収鐗囨椂锛屾満鍣ㄤ笉浼氬湪涓€旂潯鐫€鎶婁换鍔℃墦鏂€?
## 鏋舵瀯

```text
鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹?鍓嶇灞?Vue 3 + Vite + Tailwind 鈹? drag&drop / 闃熷垪 / 骞跺彂婊戝潡 / 鍓嶅悗瀵规瘮 / 鎶戝埗寮€鍏?鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?               鈹?Wails bindings (JSON) + Events
鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈻尖攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹?璋冨害灞?Go 1.26 (Wails 澹?      鈹? 鍔ㄦ€佸苟鍙戞睜 / 浠诲姟闃熷垪 / 鍛藉悕钀界洏 / 妯″瀷涓嬭浇 / 鐫＄湢鎶戝埗
鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?               鈹?CGO 鈫?chronos.h锛堝彧浼犺矾寰勶級
鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈻尖攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹?绠楀姏灞?C++17 + OpenVINO        鈹? 鍒囩墖 鈫?寮傛鎺ㄧ悊 鈫?缇藉寲鎷兼帴 鈫?PNG 钀界洏
鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?```

璁捐绾︽潫锛堟潵鑷灦鏋勮璁′功锛屽叏閮ㄦ湁瀹炵幇涓庢祴璇曪級锛?
1. **璺ㄨ瑷€鍙紶鏂囦欢璺緞**锛屽儚绱犳暟鎹案杩滀笉璺?CGO 杈圭晫銆?2. **C++ 闆跺紓甯哥┛瓒?*锛氭瘡涓?`extern "C"` 鍏ュ彛 `try/catch(...)`锛岄敊璇爜杩斿洖锛孏o 渚ф槧灏勪负 sentinel error銆?3. **鍔ㄦ€佸苟鍙?*锛歎I 婊戝潡 1鈥? 瀹炴椂鏀剁缉/鎵╁紶浠诲姟姹狅紝姝ｅ湪璺戠殑浠诲姟涓嶄細琚矖鏆存帎鏂€?4. **鍒囩墖 + 缇藉寲**锛氶粯璁?256px tile / 16px overlap锛岀嚎鎬х窘鍖栨潈閲嶅綊涓€鍖栵紝娑堥櫎缃戞牸缂濄€?5. **纭笂闄愪繚鎶?*锛氬苟鍙戜笂闄?8锛坄engine.MaxConcurrency`锛夛紝UI 楂樺苟鍙戞椂鎻愮ず椋庨櫓銆?6. **娌℃湁鍘熺敓搴撲篃鑳借窇**锛歚chronos_native` 鏋勫缓鏍囩缂哄け鏃惰嚜鍔ㄩ檷绾т负绾?Go 鐨?Lanczos3 鍒囩墖瀹炵幇锛堥€傚悎鏃?OpenVINO 鐜鍜?`go test`锛夛紝鍙敤 `--no-model` 浣撻獙銆?
## 鍔熻兘

- 鎷栨嫿/澶氶€夋壒閲忚秴鍒嗭紝闃熷垪绾ц繘搴︼紙鎸?tile 瀹炴椂鎺ㄨ繘锛夈€佸彇娑堛€佸け璐ラ殧绂?- 鍔ㄦ€佸苟鍙戞粦鍧楋紙1鈥?锛? 楂樼骇閫夐」锛坱ile / overlap / threads / fast锛?- 妯″瀷绠＄悊锛氬唴缃?10 涓?Real-ESRGAN / Swin2SR / Real-CUGAN 妯″瀷娓呭崟锛屾寜闇€涓嬭浇銆丼HA256 鏍￠獙銆佸闀滃儚鍥為€€
- 鍓嶅悗瀵规瘮婊戝潡銆佹渶杩戠粨鏋溿€佹棩蹇楁帶鍒跺彴銆佸紩鎿庣姸鎬侊紙backend/device/ABI锛?- **鐫＄湢/鐔勫睆/閿佸睆鎶戝埗**锛歚sleep`銆乣display`銆乣lock` 涓夊紑鍏?+ 浠诲姟缁撴潫鑷姩閲婃斁锛沇indows 鐢?`SetThreadExecutionState`锛孡inux 鐢?`systemd-logind Inhibit` + `org.freedesktop.ScreenSaver`
- 鏃犲ご CLI锛氬彲涓庤剼鏈?鏈嶅姟绔祦姘寸嚎闆嗘垚锛屼篃鏄?CI 鍦?Ubuntu 涓婇獙鏀剁畻鍔涘眰鐨勬柟寮?
## 蹇€熷紑濮?
### 鐩存帴杩愯锛堟棤闇€ C++ 宸ュ叿閾撅紝鑷姩浣跨敤 Go 鍥為€€寮曟搸锛?
```bash
go run ./cmd/chronos info
go run ./cmd/chronos gen -o test.png -w 512 -h 512
go run ./cmd/chronos upscale -i test.png -s 2 --no-model
```

### 鏋勫缓鍘熺敓寮曟搸锛堟帹鑽愶級

```bash
# Linux
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build native/build
ctest --test-dir native/build --output-on-failure

# Windows (MinGW-w64 + Ninja)
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release -DCMAKE_C_COMPILER=gcc -DCMAKE_CXX_COMPILER=g++
cmake --build native/build
ctest --test-dir native/build --output-on-failure
```

鍚敤 AI 鎺ㄧ悊锛堥渶瑕?OpenVINO 杩愯鏃讹級锛?
```bash
cmake -S native -B native/build -G Ninja -DCHRONOS_WITH_OPENVINO=ON -DOpenVINO_DIR=<openvino>/runtime/cmake
cmake --build native/build
```

### 鏋勫缓妗岄潰搴旂敤

```bash
cd frontend && pnpm install && pnpm build && cd ..
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails build -tags chronos_native            # Windows: build/chronos.exe
wails build -tags "webkit2_41 chronos_native"   # Ubuntu 24.04
```

浜х墿锛歐indows `build/bin/chronos.exe`锛汱inux 鍚岀洰褰曚簩杩涘埗 + `libs/libchronos.so`锛堟垨鐢?`scripts/package-linux.sh` 鎵撳寘 .deb/.tar.gz锛夈€?
### 妯″瀷

```bash
chronos models list
chronos models pull swin2sr-compressed-x4     # 婕敾/鍘嬬缉淇锛?x锛?chronos upscale -i manga.png --model-id swin2sr-compressed-x4
```

ONNX 鈫?OpenVINO IR锛團P16锛夎浆鎹€佹ā鍨嬭鍙笌缂撳瓨浣嶇疆瑙?[`docs/MODELS.md`](docs/MODELS.md)銆?
## CLI 閫熸煡

```text
chronos upscale -i a.png,b.jpg -o out/ -s 4 [--model m.onnx | --model-id id] [--tile 256] [--overlap 16] [--threads 0] [--fast] [--no-model] [--json]
chronos info [--json]            # 寮曟搸/骞冲彴/鐩綍璇婃柇
chronos gen -o test.png -w 512 -h 512
chronos models list|pull|path|import|dir
chronos inhibit on --sleep --display --lock [--duration 1h] [--json]
chronos inhibit status --json
chronos bench --size 512 --iters 3 -s 2
```

## 鐩綍缁撴瀯

```text
main.go / app.go            Wails 妗岄潰鍏ュ彛涓庣粦瀹氾紙JSON 濂戠害瑙?docs/CONTRACTS.md锛?cmd/chronos/                鏃犲ご CLI
internal/engine/            寮曟搸鎺ュ彛銆丆GO 鍘熺敓缁戝畾锛坈hronos_native锛夈€佺函 Go 鍥為€€銆佸紩鎿庢睜
internal/scheduler/         鍔ㄦ€佸苟鍙戜换鍔￠槦鍒楋紙1鈥? 瀹炴椂璋冩暣銆佸彇娑堛€佸懡鍚嶃€佸け璐ラ殧绂伙級
internal/inhibit/           鐫＄湢/鐔勫睆/閿佸睆鎶戝埗锛圵indows/Linux 椹卞姩 + 鍙祴璇曠姸鎬佹満锛?internal/models/            妯″瀷娉ㄥ唽琛紙embed锛夈€佷笅杞?鏍￠獙/瀵煎叆
internal/config/            璁剧疆鎸佷箙鍖?native/                     C++17 鏍稿績锛氬垏鐗?缇藉寲/Lanczos/OpenVINO 鍚庣/C ABI/娴嬭瘯
frontend/                   Vue 3 + Vite + Tailwind 鐣岄潰
.github/workflows/          CI锛氬弻骞冲彴鍘熺敓鏋勫缓+ctest銆丟o 娴嬭瘯銆佸墠绔瀯寤恒€乄ails 鎵撳寘銆丷elease
docs/                       鏋舵瀯銆佸绾︺€佹ā鍨嬨€佹姂鍒躲€丆I銆佽矾绾垮浘
```

## 娴嬭瘯

| 灞?| 鍛戒护 | 瑕嗙洊 |
| --- | --- | --- |
| C++ | `ctest --test-dir native/build --output-on-failure` | 鍒囩墖瑕嗙洊/鏃犵紳闅欍€佺窘鍖栧綊涓€鍖栥€佸父閲忓浘涓嶅彉鎬с€丩anczos銆両O銆乸ipeline銆佸彇娑堛€丆 API 閿欒鐮?|
| Go | `go test ./...` | 鍔ㄦ€佸苟鍙戝疄娴嬨€佸彇娑堛€佸懡鍚嶅啿绐併€佸け璐ラ殧绂汇€佹姂鍒剁姸鎬佹満銆佹ā鍨嬩笅杞芥牎楠屻€侀厤缃?|
| Go+CGO | `go test -tags chronos_native ./internal/engine/...` | C ABI 杈圭晫銆佺湡瀹炲浘鍍忓鐞?|
| UI | `cd frontend && pnpm test` | 闃熷垪/杩涘害鍚堝苟绛夌函閫昏緫 |
| 绔埌绔?| `scripts/ubuntu-smoke.sh` / `scripts/windows-smoke.ps1` | 鏃犲ご CLI 鍥惧儚灏哄鏂█銆佹姂鍒剁姸鎬併€佸紩鎿庝俊鎭?|

CI 鍦?**ubuntu-latest + windows-latest** 涓婅窇瀹屾暣鐭╅樀锛孶buntu 浠诲姟鍚屾椂楠岃瘉 headless 绠楀姏灞備笌 `.deb` 鎵撳寘銆?
## 涓?bg2x 鐨勫叧绯?
- 浜у搧瀹氫綅涓庢ā鍨嬮€夊瀷缁ф壙 bg2x锛圡IT锛孋opyright (c) 2026 isixe锛夛紝鍥炬爣娌跨敤鍏惰祫婧愶紱
- 鏈粨搴撴槸**鐙珛瀹炵幇**锛歎I 閲嶅啓锛岀畻鍔涘眰浠?Web GPU/WASM 杩佸埌 C++/OpenVINO锛屾柊澧炴棤澶?CLI 涓庣郴缁熺數婧?閿佸睆鎶戝埗锛?- 濡傞渶绾祻瑙堝櫒銆佸厤瀹夎鐗堟湰锛岃浣跨敤鍘熼」鐩?[isixe/bg2x](https://github.com/isixe/bg2x)銆?
## License

MIT銆傛ā鍨嬫潈閲嶇増鏉冨綊鍚勮嚜涓婃父锛圧eal-ESRGAN / Swin2SR / Real-CUGAN锛夛紝璇﹁ [`docs/MODELS.md`](docs/MODELS.md)銆?