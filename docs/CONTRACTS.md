# Project Chronos 鈥?璺ㄨ瑷€鎺ュ彛濂戠害 (v1)

> **鐘舵€侊細鍐荤粨銆?* 鏈枃浠舵槸鍓嶇 / Go / C++ 涓夊眰涔嬮棿鍞竴鐨勬帴鍙ｄ簨瀹炴潵婧愩€?> 鏀瑰姩浠讳綍绛惧悕鍓嶅繀椤诲悓姝ユ洿鏂版湰鏂囦欢涓庝笁涓疄鐜帮紝骞堕€氱煡 Lead銆?> 浠ｇ爜浠撳簱锛歚w32394045-dotcom/bg2x-chronos`

## 0. 鍒嗗眰涓庢暟鎹祦

```
Vue 3 UI  鈹€鈹€Wails bindings (JSON RPC)鈹€鈹€鈻? Go 璋冨害灞? 鈹€鈹€CGO (chronos.h)鈹€鈹€鈻? C++ 绠楀姏灞?   鈻?                                       鈹?   鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€ Wails Events (JSON) 鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?```

閾佸緥锛堟潵鑷灦鏋勮璁′功绗竷鑺傦紝鍏ㄩ儴瀹炵幇閮介伒瀹堬級锛?
1. **璺ㄨ瑷€鍙紶鏂囦欢璺緞**锛岀粷涓嶄紶鍍忕礌 buffer銆傚浘鍍忚鍐欏湪 C++ 鍐呴儴瀹屾垚銆?2. C++ 浠讳綍寮傚父閮戒笉寰楄秺杩?`extern "C"` 杈圭晫锛涙瘡涓叆鍙?`try/catch(...)`銆?3. C++ 涓嶄緷璧?OpenCV锛圓DR-001锛夛細鍥惧儚 IO 鐢?vendored `stb_image`锛屾帹鐞嗗紶閲忕敤 OpenVINO 鍘熺敓 `ov::Tensor`銆?4. C++ 渚?`chronos_session` 闈炵嚎绋嬪畨鍏紱**姣忎釜 Go worker 鐙崰涓€涓?session**銆?5. 骞跺彂涓婇檺纭紪鐮?8锛堥厤缃眰 + UI 鍙岄噸绾︽潫锛夈€?
## 1. C++ 鈬?Go锛歚native/include/chronos/chronos.h`

瀹屾暣澶存枃浠跺凡鍐荤粨锛屽疄鐜板繀椤婚€愬瓧鑺傚尮閰嶃€傝鐐癸細

| API | 璇箟 |
| --- | --- |
| `chronos_abi_version()` | 杩斿洖 `CHRONOS_ABI_VERSION`(1)锛孏o 鍚姩鑷 |
| `chronos_session_create(model_path, scale, opts, err, err_len)` | `model_path` 涓虹┖ 鈬?stub锛圠anczos锛夊悗绔紱闈炵┖涓旂紪璇戜簡 OpenVINO 鈬?灏濊瘯鍔犺浇 `.xml`/`.onnx`锛屽け璐ヨ繑鍥?`NULL` 骞跺啓 err |
| `chronos_process(session, in, out, &w, &h, err, err_len)` | 璇诲浘鈫掑垏鐗団啋鎺ㄧ悊鈫掔窘鍖栨嫾鎺モ啋钀界洏锛岃繑鍥?0 / 璐熼敊璇爜 |
| `chronos_session_progress(session, &done, &total)` | 鍘熷瓙璇诲彇褰撳墠鍥剧墖鐨?tile 杩涘害锛孏o 渚?50ms 杞 |
| `chronos_session_cancel(session)` | 缃彇娑堜綅锛沺ipeline 鍦?tile 杈圭晫妫€鏌ュ苟灏藉揩杩斿洖 `CHRONOS_ERR_CANCELLED` |

閿欒鐮侊紙涓夌鍏辩敤锛孏o 鏄犲皠涓?sentinel error锛夛細

```
 0 OK | -1 IO | -2 MODEL | -3 INFER | -4 PARAM | -5 BACKEND | -6 CANCELLED | -99 UNKNOWN
```

鍚庣 id锛歚0 NONE | 1 STUB | 2 OPENVINO`銆?
`chronos_options.struct_size` 蹇呴』涓?`sizeof(chronos_options)`锛涗负 0 鏃舵寜榛樿鍊硷紙tile 256 / overlap 16 / threads=hw / device 0 / flags 0锛夈€?
## 2. 绠楀姏灞傚唴閮ㄥ绾︼紙C++锛?
- 鍒囩墖锛歚tile = 256`锛宍overlap = 16`锛宍step = tile - overlap = 240`銆傝緭鍑虹敾甯冧负 `(w*scale, h*scale)`銆?- 姣忎釜 tile 鍦ㄨ緭鍏ュ浘涓婂彇 `tile脳tile` 鍖哄煙锛堣秺鐣岀敤杈圭紭澶嶅埗 / replicate padding锛夛紝鎺ㄧ悊鍚庡緱鍒?`tile*scale` 鐨?patch銆?- 缇藉寲锛氭瘡缁存潈閲?= 涓ょ绾挎€ф笎鍙橈紙绔偣涓?`1/tile` 绾у皬鏉冮噸锛岄伩鍏?0 闄わ級锛屽啓鍥炲ぇ鍥炬椂鎸?*鏉冮噸绱姞鍐嶅綊涓€鍖?*锛屽洜姝ら噸鍙犲尯鏄姞鏉冨钩鍧囪€岄潪纭嫾鎺ワ紱閲嶅彔鍖轰腑涓や釜绾挎€ф枩鍧′箣鍜屼负甯告暟锛屼繚璇佹棤鎺ョ紳銆?- 杩涘害锛歚done/total` 浠?tile 鏁拌锛屽師瀛愯嚜澧炪€?- stub 鍚庣蹇呴』鏀寔 `scale 鈭?{1,2,3,4}`锛孫penVINO 鍚庣浠ユā鍨嬪師鐢?scale 涓哄噯锛堣姹?scale 浠呯敤浜庢牎楠岋級銆?
## 3. Go 鈬?鍓嶇锛欰pp 鏂规硶

鍓嶇缁熶竴閫氳繃 `window.go.main.App.<Method>(...)`锛圥romise锛夎皟鐢紝閫氳繃 `window.runtime.EventsOn("<event>", cb)` 璁㈤槄浜嬩欢銆?鍓嶇**涓嶆彁浜よ嚜鍔ㄧ敓鎴愮殑 wailsjs bindings**锛岀粺涓€璧?`src/lib/api.ts` 鐨勮杽灏佽 + 绫诲瀷瀹氫箟锛堣 搂5锛夛紝mock 妯″紡淇濊瘉 `vite dev` 鍙嫭绔嬮瑙堛€?
### 3.1 鏂规硶

```go
GetAppInfo() AppInfo
ListModels() []ModelInfo                    // 鍐呯疆 registry + 鏈湴宸蹭笅杞界姸鎬?RefreshModels() []ModelInfo                 // 閲嶆柊鎵弿妯″瀷鐩綍
UpscaleBatch(req BatchRequest) (BatchResult, error)
CancelJob(jobID string) bool
SetConcurrency(n int) error                 // 1..8锛岃秺鐣岃繑鍥?error
GetConcurrency() int
SetInhibit(req InhibitRequest) (InhibitStatus, error)
GetInhibitStatus() InhibitStatus
PickImages() []string
PickOutputDir() string
Reveal(path string) error
OpenDataDir() error
```

### 3.2 鏁版嵁缁撴瀯锛圝SON tag 鍗冲墠绔瓧娈靛悕锛宑amelCase锛?
```go
type AppInfo struct {
    Version       string      `json:"version"`        // e.g. "0.1.0"
    Commit        string      `json:"commit"`
    BuildTime     string      `json:"buildTime"`
    GoVersion     string      `json:"goVersion"`
    Platform      string      `json:"platform"`       // "windows/amd64"
    Engine        EngineInfo  `json:"engine"`
    DataDir       string      `json:"dataDir"`
    ModelDir      string      `json:"modelDir"`
    OutputDir     string      `json:"outputDir"`
    MaxConcurrency int        `json:"maxConcurrency"` // 8
    NativeLoaded  bool        `json:"nativeLoaded"`
}

type EngineInfo struct {
    Backend  string `json:"backend"`   // "openvino" | "stub" | "go-lanczos"
    Device   string `json:"device"`    // "GPU" | "CPU" | "n/a"
    Detail   string `json:"detail"`
    ABIVersion int  `json:"abiVersion"`
}

type ModelInfo struct {
    ID          string  `json:"id"`
    Name        string  `json:"name"`
    Scale       int     `json:"scale"`
    Kind        string  `json:"kind"`        // "photo" | "anime" | "compressed" | "general"
    Description string  `json:"description"`
    SizeMB      float64 `json:"sizeMb"`
    Installed   bool    `json:"installed"`
    Path        string  `json:"path"`
    URL         string  `json:"url"`         // 棣栭€変笅杞芥簮
}

type BatchRequest struct {
    Files        []string        `json:"files"`
    ModelID      string          `json:"modelId"`      // "" => stub 寮曟搸锛堟棤妯″瀷锛?    ModelPath    string          `json:"modelPath"`    // 鍙€夛紝鐩存帴鎸囧畾鏈湴 .xml/.onnx
    Scale        int             `json:"scale"`
    OutputDir    string          `json:"outputDir"`
    Concurrency  int             `json:"concurrency"`
    TileSize     int             `json:"tileSize"`
    Overlap      int             `json:"overlap"`
    Threads      int             `json:"threads"`
    Fast         bool            `json:"fast"`
    Inhibit      *InhibitRequest `json:"inhibit"`      // 闈炵┖涓斾换涓€浣嶄负鐪?=> 浠诲姟鎵ц鏈熼棿淇濇寔鍞ら啋
}

type BatchResult struct {
    JobID   string   `json:"jobId"`
    Queued  int      `json:"queued"`
    Skipped []string `json:"skipped"`
}

type ProgressEvent struct {
    JobID     string `json:"jobId"`
    TaskID    string `json:"taskId"`
    File      string `json:"file"`
    Kind      string `json:"kind"`     // queued|started|progress|done|failed|cancelled
    Index     int    `json:"index"`    // 0-based锛岄槦鍒楀簭鍙?    Done      int    `json:"done"`     // tile 杩涘害
    Total     int    `json:"total"`
    Output    string `json:"output"`
    Width     int    `json:"width"`
    Height    int    `json:"height"`
    Error     string `json:"error"`
    Backend   string `json:"backend"`
    ElapsedMs int64  `json:"elapsedMs"`
}

type JobSummary struct { // 涓€涓?job 缁撴潫锛堝叏閮?done/failed/cancelled锛?    JobID     string `json:"jobId"`
    Total     int    `json:"total"`
    Completed int    `json:"completed"`
    Failed    int    `json:"failed"`
    Cancelled bool   `json:"cancelled"`
    ElapsedMs int64  `json:"elapsedMs"`
}

type LogEvent struct {
    Level  string `json:"level"` // debug|info|warn|error
    Msg    string `json:"msg"`
    TimeMs int64  `json:"timeMs"`
}

type InhibitRequest struct {
    Sleep      bool `json:"sleep"`      // 闃绘绯荤粺鐫＄湢
    Display    bool `json:"display"`    // 闃绘鍏抽棴鏄剧ず鍣?    Lock       bool `json:"lock"`       // 闃绘绌洪棽閿佸睆锛圵indows 涓婃棤娉曢樆姝㈡墜鍔?Win+L锛?    AutoRelease bool `json:"autoRelease"` // 浠诲姟缁撴潫鍚庤嚜鍔ㄩ噴鏀?}

type InhibitStatus struct {
    Active           bool   `json:"active"`
    Sleep            bool   `json:"sleep"`            // 褰撳墠鏄惁鎸佹湁
    Display          bool   `json:"display"`
    Lock             bool   `json:"lock"`
    SleepSupported   bool   `json:"sleepSupported"`
    DisplaySupported bool   `json:"displaySupported"`
    LockSupported    bool   `json:"lockSupported"`
    Driver           string `json:"driver"`           // "windows-execution-state" | "linux-logind+screen-saver" | "unsupported" | ...
    Detail           string `json:"detail"`
    SinceMs          int64  `json:"sinceMs"`          // 0 = 鏈縺娲伙紱鍚﹀垯涓?unix ms 璧峰鏃跺埢
}
```

### 3.3 浜嬩欢

| 浜嬩欢鍚?| payload | 棰戠巼 |
| --- | --- | --- |
| `chronos:progress` | `ProgressEvent` | 姣?tile 鎵?姣?500ms 鑺傛祦 |
| `chronos:job` | `JobSummary` | 姣?job 涓€娆?|
| `chronos:log` | `LogEvent` | 鎸夐渶 |
| `chronos:inhibit` | `InhibitStatus` | 鐘舵€佸彉鍖栨椂 |

浠诲姟瀹屾垚鍚庤嫢 `BatchRequest.Inhibit.AutoRelease` 涓虹湡锛孏o 渚ц嚜鍔?`SetInhibit({})` 骞舵帹閫佷竴娆?`chronos:inhibit`銆?
### 3.4 鍛藉悕涓庤惤鐩樿鍒欙紙Go 灞傦級

- 杈撳嚭鐩綍涓虹┖ 鈬?涓庤緭鍏ュ悓鐩綍銆?- 鏂囦欢鍚嶏細`<鍘熷悕>_chronos_x<scale>.<ext>`锛屾墿灞曞悕榛樿 `png`锛圓PNG/JPEG 杈撳叆涔熺粺涓€杈撳嚭 PNG 浠ヤ繚璇佹棤鎹燂紱`Fast` 妯″紡涓嶆敼鍙樻墿灞曞悕绛栫暐锛夈€?- 鍚屽悕鍐茬獊杩藉姞 `-1`,`-2`鈥︼紙鐩村埌 999锛岃秴闄愭姤閿欙級銆?- 璺宠繃锛氫笉瀛樺湪 / 闈炲浘鐗?/ 宸插湪闃熷垪涓殑閲嶅璺緞锛涘啓鍏?`BatchResult.Skipped`銆?
## 4. Go 鈬?C++锛欳GO 鏋勫缓濂戠害

- C 澶存枃浠讹細`native/include/chronos/chronos.h`锛汫o 渚?`#cgo CFLAGS: -I${SRCDIR}/../../native/include`銆?- 閾炬帴锛歚-L${SRCDIR}/../../native/build -lchronos`锛圠inux `libchronos.so`锛學indows `chronos.dll` + 瀵煎叆搴擄級銆?- 鏋勫缓鏍囩锛?*浠呭綋** `cgo && chronos_native` 鏃剁紪璇?CGO 瀹炵幇锛涘惁鍒欒蛋绾?Go Lanczos 鍥為€€瀹炵幇锛堜繚璇?`go test ./...` 鍦ㄦ棤宸ュ叿閾剧幆澧冨彲璺戯級銆?- 杩愯鏃跺簱鏌ユ壘锛歐indows 灏?`chronos.dll` 鏀惧湪 exe 鍚岀洰褰曪紱Linux 鐢?`rpath=$ORIGIN/libs`锛圕I 閲岃缃?`LD_LIBRARY_PATH`锛夈€?- CGO 璋冪敤缁熶竴鍦?`internal/engine/native_cgo.go`锛屽寘涓€灞?`recover()` + 閿欒鐮佹槧灏勩€?
鏋勫缓鍛戒护锛圕I 涓庢湰鍦颁竴鑷达級锛?
```bash
cmake -S native -B native/build -G Ninja -DCMAKE_BUILD_TYPE=Release
cmake --build native/build
ctest --test-dir native/build --output-on-failure
go test -tags chronos_native ./internal/engine/...
```

## 5. 鍓嶇 `src/lib/api.ts` 濂戠害

```ts
export interface Api {
  GetAppInfo(): Promise<AppInfo>
  ListModels(): Promise<ModelInfo[]>
  RefreshModels(): Promise<ModelInfo[]>
  UpscaleBatch(req: BatchRequest): Promise<BatchResult>
  CancelJob(jobId: string): Promise<boolean>
  SetConcurrency(n: number): Promise<void>
  GetConcurrency(): Promise<number>
  SetInhibit(req: InhibitRequest): Promise<InhibitStatus>
  GetInhibitStatus(): Promise<InhibitStatus>
  PickImages(): Promise<string[]>
  PickOutputDir(): Promise<string>
  Reveal(path: string): Promise<void>
  OpenDataDir(): Promise<void>
}
```

- `isDesktop()`锛歚!!(window as any).go?.main?.App`銆?- 闈炴闈㈢幆澧冿紙`vite dev` 娴忚鍣ㄩ瑙堬級鑷姩杩涘叆 mock锛氳繑鍥炵ず渚嬫暟鎹€佹ā鎷?tile 杩涘害銆両nhibit 鐘舵€佸彲鐢ㄣ€?- 浜嬩欢璁㈤槄灏佽 `onEvent<T>(name, handler): () => void`锛屽苟鍋?payload 褰掍竴鍖栵紙`elapsedMs` 鍙兘涓?0锛夈€?
## 6. 鐗堟湰涓庡吋瀹?
- ABI 鐗堟湰 1锛汫o 鍚姩鏃?`chronos_abi_version()` 涓嶇鍗抽檷绾у埌 go-lanczos 骞跺啓鏃ュ織銆?- 鍓嶇鍙緷璧?JSON 瀛楁鍚嶏紝瀛楁鍙涓嶅垹锛涙柊澧炲瓧娈靛墠绔仛鍙€夊鐞嗐€?- 妯″瀷 registry锛歚models/registry.json`锛堝崟婧愶級锛孏o `embed`锛屽墠绔敱 `ListModels()` 鑾峰彇锛屼笉閲嶅缁存姢鍒楄〃銆?