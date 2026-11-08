package artifact

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testPayload 生成可辨识的测试载荷（内容由位置决定，便于比较）。
func testPayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

// sha256Hex 返回十六进制摘要。
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// rangeRecorder 记录服务端收到的 Range 头，用来断言续传确实发出了 Range。
type rangeRecorder struct {
	mu     sync.Mutex
	values []string
}

func (r *rangeRecorder) add(v string) {
	if v == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values = append(r.values, v)
}

func (r *rangeRecorder) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.values) == 0 {
		return ""
	}
	return r.values[len(r.values)-1]
}

// newHitsServer 启动一个统计请求次数的测试服务器。
func newHitsServer(t *testing.T, hits *int32, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(hits, 1)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// servePayload 用 http.ServeContent 提供支持 Range 的静态响应。
func servePayload(data []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "payload.bin", time.Time{}, bytes.NewReader(data))
	})
}

// defaultFile 构造带正确摘要的 File。
func defaultFile(base string, payload []byte) File {
	return File{URLs: []string{base + "/f.bin"}, SHA256: sha256Hex(payload), SizeHint: int64(len(payload))}
}

// fetchCase 是 TestFetchHTTPScenarios 的一个场景。
type fetchCase struct {
	name       string
	payload    []byte
	retries    int // 0 用默认值（2），-1 表示不重试
	file       func(base string, payload []byte) File
	prep       func(t *testing.T, part string) // 预置 .part
	handler    func(payload []byte, hits *int32, rec *rangeRecorder) http.Handler
	wantErr    bool
	wantErrIs  error
	wantHTTP   int
	wantHits   int32
	wantRange  string
	wantFile   bool // 成功：dst 内容等于 payload，且 .part 已清理
	wantNoPart bool
	wantPart   bool // 失败：.part 仍保留（可续传）
}

// TestFetchHTTPScenarios 覆盖 HTTP 状态、Range 语义与续传重启路径。
func TestFetchHTTPScenarios(t *testing.T) {
	payload := testPayload(3000)
	cases := []fetchCase{
		{
			name:    "404_fail_fast",
			payload: payload,
			handler: func(_ []byte, _ *int32, _ *rangeRecorder) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "not found", http.StatusNotFound)
				})
			},
			wantErr: true, wantErrIs: ErrHTTP, wantHTTP: http.StatusNotFound,
			wantHits: 1, wantNoPart: true,
		},
		{
			name:    "500_retries_exhausted",
			payload: payload,
			retries: 1,
			handler: func(_ []byte, _ *int32, _ *rangeRecorder) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "boom", http.StatusInternalServerError)
				})
			},
			wantErr: true, wantErrIs: ErrHTTP, wantHTTP: http.StatusInternalServerError,
			wantHits: 2, wantNoPart: true,
		},
		{
			name:    "500_once_then_success",
			payload: payload,
			retries: 1,
			handler: func(payload []byte, hits *int32, _ *rangeRecorder) http.Handler {
				serve := servePayload(payload)
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if atomic.LoadInt32(hits) == 1 {
						http.Error(w, "boom", http.StatusInternalServerError)
						return
					}
					serve.ServeHTTP(w, r)
				})
			},
			wantHits: 2, wantFile: true, wantNoPart: true,
		},
		{
			name:    "429_once_then_success",
			payload: payload,
			retries: 1,
			handler: func(payload []byte, hits *int32, _ *rangeRecorder) http.Handler {
				serve := servePayload(payload)
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if atomic.LoadInt32(hits) == 1 {
						http.Error(w, "slow down", http.StatusTooManyRequests)
						return
					}
					serve.ServeHTTP(w, r)
				})
			},
			wantHits: 2, wantFile: true, wantNoPart: true,
		},
		{
			name:    "resume_206_append",
			payload: payload,
			prep: func(t *testing.T, part string) {
				if err := os.WriteFile(part, payload[:1000], 0o644); err != nil {
					t.Fatal(err)
				}
			},
			handler: func(payload []byte, _ *int32, rec *rangeRecorder) http.Handler {
				serve := servePayload(payload)
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					rec.add(r.Header.Get("Range"))
					serve.ServeHTTP(w, r)
				})
			},
			wantHits: 1, wantRange: "bytes=1000-", wantFile: true, wantNoPart: true,
		},
		{
			name:    "server_ignores_range_restart",
			payload: payload,
			prep: func(t *testing.T, part string) {
				if err := os.WriteFile(part, payload[:1000], 0o644); err != nil {
					t.Fatal(err)
				}
			},
			handler: func(payload []byte, _ *int32, rec *rangeRecorder) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					rec.add(r.Header.Get("Range"))
					_, _ = w.Write(payload) // 无视 Range，总是 200 + 全量
				})
			},
			wantHits: 1, wantRange: "bytes=1000-", wantFile: true, wantNoPart: true,
		},
		{
			name:    "416_restart_from_zero",
			payload: payload,
			prep: func(t *testing.T, part string) {
				// .part 比远端文件还大，服务端会回 416。
				if err := os.WriteFile(part, bytes.Repeat([]byte{'z'}, 5000), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			handler: func(payload []byte, _ *int32, rec *rangeRecorder) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Range") != "" {
						rec.add(r.Header.Get("Range"))
						w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
						return
					}
					_, _ = w.Write(payload)
				})
			},
			wantHits: 2, wantRange: "bytes=5000-", wantFile: true, wantNoPart: true,
		},
		{
			name:    "truncated_body_keeps_part",
			payload: payload,
			retries: -1,
			handler: func(payload []byte, _ *int32, _ *rangeRecorder) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)+16))
					_, _ = w.Write(payload) // 少写 16 字节
				})
			},
			wantErr: true, wantHits: 1, wantPart: true,
		},
		{
			name:    "checksum_mismatch_cleans_part",
			payload: payload,
			file: func(base string, _ []byte) File {
				return File{URLs: []string{base + "/f.bin"}, SHA256: sha256Hex([]byte("other"))}
			},
			handler: func(payload []byte, _ *int32, _ *rangeRecorder) http.Handler {
				return servePayload(payload)
			},
			wantErr: true, wantErrIs: ErrChecksum,
			wantHits: 1, wantNoPart: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			dst := filepath.Join(dir, "model.onnx")
			d := &Downloader{Retries: tc.retries}
			part := d.partPath(dst)
			if tc.prep != nil {
				tc.prep(t, part)
			}
			var hits int32
			rec := &rangeRecorder{}
			handler := tc.handler(tc.payload, &hits, rec)
			srv := newHitsServer(t, &hits, handler)

			f := defaultFile(srv.URL, tc.payload)
			if tc.file != nil {
				f = tc.file(srv.URL, tc.payload)
			}
			err := d.Fetch(context.Background(), f, dst, nil)

			if tc.wantErr || tc.wantErrIs != nil {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			} else if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if tc.wantErrIs != nil && !errors.Is(err, tc.wantErrIs) {
				t.Fatalf("error %v does not match %v", err, tc.wantErrIs)
			}
			if tc.wantHTTP != 0 {
				var he *HTTPError
				if !errors.As(err, &he) || he.StatusCode != tc.wantHTTP {
					t.Fatalf("want HTTP %d in error chain, got %v", tc.wantHTTP, err)
				}
			}
			if got := atomic.LoadInt32(&hits); got != tc.wantHits {
				t.Fatalf("server hits = %d, want %d", got, tc.wantHits)
			}
			if tc.wantRange != "" && rec.last() != tc.wantRange {
				t.Fatalf("last Range header = %q, want %q", rec.last(), tc.wantRange)
			}
			if tc.wantFile {
				got, readErr := os.ReadFile(dst)
				if readErr != nil {
					t.Fatalf("read dst: %v", readErr)
				}
				if !bytes.Equal(got, tc.payload) {
					t.Fatalf("dst content mismatch: got %d bytes, want %d", len(got), len(tc.payload))
				}
			}
			if tc.wantNoPart {
				if _, statErr := os.Stat(part); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf(".part should be gone, stat err = %v", statErr)
				}
			}
			if tc.wantPart {
				st, statErr := os.Stat(part)
				if statErr != nil || st.Size() == 0 {
					t.Fatalf(".part should be kept with data, stat err = %v", statErr)
				}
			}
		})
	}
}

// TestFetchHappyPath 覆盖成功路径：内容、摘要、进度单调且最后一次是最终值。
func TestFetchHappyPath(t *testing.T) {
	payload := testPayload(7000)
	var hits int32
	srv := newHitsServer(t, &hits, servePayload(payload))

	dst := filepath.Join(t.TempDir(), "nested", "dir", "model.onnx") // 父目录应自动创建
	var mu sync.Mutex
	var progress []Progress
	err := (&Downloader{Retries: -1}).Fetch(context.Background(), defaultFile(srv.URL, payload), dst, func(p Progress) {
		mu.Lock()
		defer mu.Unlock()
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("dst content mismatch")
	}
	if _, err := os.Stat(dst + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".part should be gone, stat err = %v", err)
	}
	if len(progress) < 2 {
		t.Fatalf("want initial + final progress, got %d callbacks", len(progress))
	}
	for i, p := range progress {
		if p.Total != int64(len(payload)) {
			t.Fatalf("progress[%d].Total = %d, want %d", i, p.Total, len(payload))
		}
		if i > 0 && p.Done < progress[i-1].Done {
			t.Fatalf("progress not monotonic at %d: %d after %d", i, p.Done, progress[i-1].Done)
		}
	}
	last := progress[len(progress)-1]
	if last.Done != int64(len(payload)) || last.Total != int64(len(payload)) {
		t.Fatalf("final progress = %+v, want Done=Total=%d", last, len(payload))
	}
}

// TestFetchUnknownTotal 覆盖 Total < 0（未知总大小）与 SizeHint 兜底。
func TestFetchUnknownTotal(t *testing.T) {
	payload := testPayload(100 << 10)
	chunked := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, _ := w.(http.Flusher)
		for i := 0; i < len(payload); i += 4096 {
			end := i + 4096
			if end > len(payload) {
				end = len(payload)
			}
			_, _ = w.Write(payload[i:end])
			if fl != nil {
				fl.Flush()
			}
		}
	})
	cases := []struct {
		name      string
		sizeHint  int64
		wantFirst int64
	}{
		{"unknown_total", 0, -1},
		{"size_hint_fallback", int64(len(payload)), int64(len(payload))},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var hits int32
			srv := newHitsServer(t, &hits, chunked)
			dst := filepath.Join(t.TempDir(), "model.onnx")
			d := &Downloader{Retries: -1}
			var progress []Progress
			f := File{URLs: []string{srv.URL + "/f.bin"}, SHA256: sha256Hex(payload), SizeHint: tc.sizeHint}
			if err := d.Fetch(context.Background(), f, dst, func(p Progress) {
				progress = append(progress, p)
			}); err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if len(progress) < 2 {
				t.Fatalf("want initial + final progress, got %d", len(progress))
			}
			if progress[0].Done != 0 || progress[0].Total != tc.wantFirst {
				t.Fatalf("initial progress = %+v, want Done=0 Total=%d", progress[0], tc.wantFirst)
			}
			last := progress[len(progress)-1]
			if last.Done != int64(len(payload)) || last.Total != int64(len(payload)) {
				t.Fatalf("final progress = %+v, want Done=Total=%d", last, len(payload))
			}
		})
	}
}

// TestFetchFallbackURLAfter500 覆盖第一个源 500、第二个源成功的回退顺序。
func TestFetchFallbackURLAfter500(t *testing.T) {
	payload := testPayload(2000)
	var firstHits, secondHits int32
	first := newHitsServer(t, &firstHits, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	second := newHitsServer(t, &secondHits, servePayload(payload))

	dst := filepath.Join(t.TempDir(), "model.onnx")
	f := File{URLs: []string{first.URL + "/a.bin", second.URL + "/b.bin"}, SHA256: sha256Hex(payload)}
	if err := (&Downloader{Retries: -1}).Fetch(context.Background(), f, dst, nil); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got := atomic.LoadInt32(&firstHits); got != 1 {
		t.Fatalf("first URL hits = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&secondHits); got != 1 {
		t.Fatalf("second URL hits = %d, want 1", got)
	}
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("dst content mismatch: err=%v", err)
	}
}

// TestFetchMirrorPrefixOrdering 覆盖镜像前缀优先、镜像失败回退原始地址。
func TestFetchMirrorPrefixOrdering(t *testing.T) {
	payload := testPayload(1500)
	cases := []struct {
		name        string
		mirrorFails bool
		wantMirror  int32
		wantOrigin  int32
	}{
		{"mirror_serves_first", false, 1, 0},
		{"mirror_fails_falls_back", true, 1, 1},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var mirrorHits, originHits int32
			origin := newHitsServer(t, &originHits, servePayload(payload))
			mirrorHandler := servePayload(payload)
			if tc.mirrorFails {
				mirrorHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Error(w, "mirror down", http.StatusInternalServerError)
				})
			}
			mirror := newHitsServer(t, &mirrorHits, mirrorHandler)

			d := &Downloader{Retries: -1, MirrorPrefix: mirror.URL + "/"}
			dst := filepath.Join(t.TempDir(), "model.onnx")
			f := File{URLs: []string{origin.URL + "/m.bin"}, SHA256: sha256Hex(payload)}
			if err := d.Fetch(context.Background(), f, dst, nil); err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if got := atomic.LoadInt32(&mirrorHits); got != tc.wantMirror {
				t.Fatalf("mirror hits = %d, want %d", got, tc.wantMirror)
			}
			if got := atomic.LoadInt32(&originHits); got != tc.wantOrigin {
				t.Fatalf("origin hits = %d, want %d", got, tc.wantOrigin)
			}
		})
	}
}

// TestFetchNoURL 覆盖没有可用地址的情况。
func TestFetchNoURL(t *testing.T) {
	cases := []struct {
		name string
		file File
	}{
		{"empty_file", File{}},
		{"blank_urls", File{URLs: []string{"", "   "}}},
		{"only_blank_with_sha", File{URLs: []string{" "}, SHA256: "deadbeef"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dst := filepath.Join(t.TempDir(), "model.onnx")
			err := (&Downloader{}).Fetch(context.Background(), tc.file, dst, nil)
			if !errors.Is(err, ErrNoURL) {
				t.Fatalf("error = %v, want ErrNoURL", err)
			}
			if _, statErr := os.Stat(dst); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("dst should not exist, stat err = %v", statErr)
			}
		})
	}
}

// TestFetchAlreadyInstalled 覆盖幂等路径：已安装不重复下载，损坏则重下。
func TestFetchAlreadyInstalled(t *testing.T) {
	payload := testPayload(2048)
	cases := []struct {
		name      string
		writeSHA  string // 写进 dst 的 File.SHA256；空表示不校验
		dstData   []byte
		stalePart bool
		wantHits  int32
		wantData  []byte
	}{
		{
			name:      "sha_match_skips_download",
			writeSHA:  sha256Hex(payload),
			dstData:   payload,
			stalePart: true,
			wantHits:  0,
			wantData:  payload,
		},
		{
			name:     "no_sha_skips_download",
			dstData:  payload,
			wantHits: 0,
			wantData: payload,
		},
		{
			name:     "corrupt_dst_redownloads",
			writeSHA: sha256Hex(payload),
			dstData:  []byte("corrupt"),
			wantHits: 1,
			wantData: payload,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var hits int32
			srv := newHitsServer(t, &hits, servePayload(payload))
			dir := t.TempDir()
			dst := filepath.Join(dir, "model.onnx")
			if err := os.WriteFile(dst, tc.dstData, 0o644); err != nil {
				t.Fatal(err)
			}
			d := &Downloader{Retries: -1}
			if tc.stalePart {
				if err := os.WriteFile(d.partPath(dst), []byte("stale"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			f := File{URLs: []string{srv.URL + "/f.bin"}, SHA256: tc.writeSHA}
			if err := d.Fetch(context.Background(), f, dst, nil); err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			got, err := os.ReadFile(dst)
			if err != nil || !bytes.Equal(got, tc.wantData) {
				t.Fatalf("dst content mismatch: err=%v", err)
			}
			if gotHits := atomic.LoadInt32(&hits); gotHits != tc.wantHits {
				t.Fatalf("server hits = %d, want %d", gotHits, tc.wantHits)
			}
			if _, err := os.Stat(d.partPath(dst)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf(".part should be gone, stat err = %v", err)
			}
		})
	}
}

// TestFetchCancellationKeepsPart 覆盖取消：返回 ctx.Err() 并保留 .part。
func TestFetchCancellationKeepsPart(t *testing.T) {
	payload := testPayload(64 << 10)
	var once sync.Once
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl, _ := w.(http.Flusher)
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload[:4096])
		if fl != nil {
			fl.Flush()
		}
		once.Do(func() { close(started) })
		for i := 4096; i < len(payload); i += 4096 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
			end := i + 4096
			if end > len(payload) {
				end = len(payload)
			}
			_, _ = w.Write(payload[i:end])
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	dst := filepath.Join(dir, "model.onnx")
	d := &Downloader{Retries: -1}
	part := d.partPath(dst)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Fetch(ctx, File{URLs: []string{srv.URL + "/f.bin"}, SHA256: sha256Hex(payload)}, dst, nil)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("server never started streaming")
	}
	// 等 .part 有数据后再取消，确保取消发生在写入过程中。
	deadline := time.Now().Add(5 * time.Second)
	for {
		if st, err := os.Stat(part); err == nil && st.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(".part never received data")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Fetch error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Fetch did not return after cancel")
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dst should not exist after cancel, stat err = %v", err)
	}
	st, err := os.Stat(part)
	if err != nil || st.Size() == 0 {
		t.Fatalf(".part should be kept for resume, stat err = %v", err)
	}
	if st.Size() > int64(len(payload)) {
		t.Fatalf(".part larger than payload: %d", st.Size())
	}
}

// TestProgressMeterThrottle 用注入时钟确定性地验证 <= 5 次/秒的节流。
func TestProgressMeterThrottle(t *testing.T) {
	cases := []struct {
		name    string
		step    time.Duration
		n       int
		wantMin int
		wantMax int
	}{
		{"5_per_second", 10 * time.Millisecond, 100, 1, 6}, // 虚拟 1s 内最多 6 次
		{"slow_updates_not_throttled", 200 * time.Millisecond, 10, 10, 10},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(0, 0)
			meter := &progressMeter{now: func() time.Time { return now }}
			var got []Progress
			cb := func(p Progress) { got = append(got, p) }
			for i := 0; i < tc.n; i++ {
				meter.emit(int64(i+1), int64(tc.n), cb)
				now = now.Add(tc.step)
			}
			if len(got) < tc.wantMin || len(got) > tc.wantMax {
				t.Fatalf("emitted %d progress calls, want %d..%d", len(got), tc.wantMin, tc.wantMax)
			}
		})
	}
	// nil 回调不应 panic。
	meter := &progressMeter{now: func() time.Time { return time.Unix(0, 0) }}
	meter.emit(1, 2, nil)
}

// TestPartPath 覆盖默认 .part 命名与 TempPrefix 命名。
func TestPartPath(t *testing.T) {
	dst := filepath.Join("models", "real-esrgan-x4plus", "model.onnx")
	dir := filepath.Join("models", "real-esrgan-x4plus")
	cases := []struct {
		name   string
		prefix string
		want   string
	}{
		{"default_suffix", "", dst + ".part"},
		{"temp_prefix", ".chronos-", filepath.Join(dir, ".chronos-model.onnx.part")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			d := &Downloader{TempPrefix: tc.prefix}
			if got := d.partPath(dst); got != tc.want {
				t.Fatalf("partPath = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestFetchResumeAfterCancel 验证取消保留的 .part 能在下一次调用中真正续传：
// 第二次请求必须带 Range，且最终内容/摘要正确。
func TestFetchResumeAfterCancel(t *testing.T) {
	payload := testPayload(64 << 10)
	rec := &rangeRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rng := r.Header.Get("Range"); rng != "" {
			rec.add(rng)
			http.ServeContent(w, r, "f.bin", time.Time{}, bytes.NewReader(payload))
			return
		}
		// 第一次请求慢速发送全量，给测试留出取消窗口。
		fl, _ := w.(http.Flusher)
		for i := 0; i < len(payload); i += 4096 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
			end := i + 4096
			if end > len(payload) {
				end = len(payload)
			}
			if _, err := w.Write(payload[i:end]); err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	t.Cleanup(srv.Close)

	dst := filepath.Join(t.TempDir(), "model.onnx")
	d := &Downloader{Retries: -1}
	part := d.partPath(dst)
	f := File{URLs: []string{srv.URL + "/f.bin"}, SHA256: sha256Hex(payload)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Fetch(ctx, f, dst, nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if st, err := os.Stat(part); err == nil && st.Size() > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(".part never received data")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first Fetch error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first Fetch did not return after cancel")
	}
	st, err := os.Stat(part)
	if err != nil || st.Size() == 0 || st.Size() >= int64(len(payload)) {
		t.Fatalf(".part should hold a partial prefix, size err = %v", err)
	}
	before := st.Size()

	var progress []Progress
	if err := d.Fetch(context.Background(), f, dst, func(p Progress) {
		progress = append(progress, p)
	}); err != nil {
		t.Fatalf("resume Fetch: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("resumed content mismatch: err=%v got=%d want=%d", err, len(got), len(payload))
	}
	if rec.last() == "" {
		t.Fatal("resume Fetch did not send a Range header")
	}
	// 续传的进度必须从已有前缀开始，Total 仍是整份文件大小。
	if len(progress) == 0 || progress[0].Done != before || progress[0].Total != int64(len(payload)) {
		t.Fatalf("initial resume progress = %+v, want Done=%d Total=%d", progress, before, len(payload))
	}
	last := progress[len(progress)-1]
	if last.Done != int64(len(payload)) || last.Total != int64(len(payload)) {
		t.Fatalf("final resume progress = %+v, want Done=Total=%d", last, len(payload))
	}
	if _, err := os.Stat(part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".part should be gone after install, stat err = %v", err)
	}
}
