// Package artifact 提供统一的构件下载器，服务于模型文件与运行时组件
// （docs/PLAN-v0.3.md §6：模型、ORT CPU/DirectML 运行时都走同一个 Fetch）。
//
// 行为约定：
//   - 原子安装：先写 <dst>.part，校验通过并 fsync 后才 os.Rename 成 dst，
//     任何失败路径都不会留下半成品的 dst；
//   - 断点续传：.part 存在时发送 Range: bytes=N-；206 追加、200 表示服务端
//     忽略 Range 需要重下、416 表示 .part 与远端不一致需要清空重来；
//     续传时先把已有前缀喂给 SHA256 再追加，保证校验覆盖整份文件；
//   - 校验：File.SHA256 非空时校验，不匹配则删除 .part 并返回 ErrChecksum；
//   - 可取消：ctx 取消时返回 ctx.Err()，保留 .part 供下次续传；
//   - 多源回退：镜像前缀（MirrorPrefix+URL）优先，其次原始 URLs；单个 URL
//     对 5xx / 429 / 网络错误做小退避重试，其余 4xx 立即失败换下一个源。
//
// 并发：同一个 dst 不允许并发 Fetch（调用方按构件 ID 去重，见
// docs/CONTRACTS.md §3.4）。
package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultRetries 是 Retries == 0 时每个 URL 的重试次数（不含首次请求）。
	defaultRetries = 2
	// retryBase 是首次重试前的退避，之后指数增长，封顶 retryMax。
	retryBase = 100 * time.Millisecond
	retryMax  = 2 * time.Second
	// progressInterval 把进度回调限制在每秒最多 5 次（docs/CONTRACTS.md §3.3）。
	progressInterval = 200 * time.Millisecond
	copyBufferSize   = 64 << 10
)

// 导出错误，配合 errors.Is 判定。
var (
	// ErrChecksum 表示下载内容与 File.SHA256 不一致（.part 已删除）。
	ErrChecksum = errors.New("artifact: sha256 mismatch")
	// ErrHTTP 表示 HTTP 响应不可用；具体状态码用 errors.As 取 *HTTPError。
	ErrHTTP = errors.New("artifact: http status")
	// ErrNoURL 表示 File 里没有可用地址。
	ErrNoURL = errors.New("artifact: no download url")
)

// Progress 是一次下载进度。Total < 0 表示服务端与 SizeHint 都未给出总大小。
type Progress struct {
	Done  int64
	Total int64
}

// File 描述一个可下载构件（模型文件、ORT 运行时压缩包等）。
type File struct {
	// URLs 是按优先级排列的下载地址（例如 HuggingFace、ModelScope）。
	URLs []string
	// SHA256 是期望的十六进制摘要；为空表示不校验。
	SHA256 string
	// SizeHint 是期望大小（字节）；<= 0 表示未知，仅用于未知总大小时的进度。
	SizeHint int64
}

// Downloader 是可复用的下载器；零值可用（Client 走 http.DefaultClient，
// Retries 走默认值）。
type Downloader struct {
	// Client 用于发起请求；nil 表示 http.DefaultClient。
	Client *http.Client
	// MirrorPrefix 非空时，会先尝试 MirrorPrefix+URL，再尝试原始 URL。
	// 直接做字符串拼接，不做域名替换。
	MirrorPrefix string
	// Retries 是每个 URL 的最大重试次数（不含首次请求）；0 用默认值 2，
	// 负数表示不重试。
	Retries int
	// TempPrefix 是临时文件名前缀；为空时临时文件就是 dst+".part"，
	// 否则是 dir(dst)/TempPrefix+base(dst)+".part"。两种形式都与 dst 同目录，
	// 保证同一卷上的 rename 是原子的。
	TempPrefix string
}

// Fetch 把 f 下载到 dst：先写临时文件、校验、fsync，最后原子 rename。
//
// 若 dst 已存在：SHA256 为空或与 SHA256 一致时视为已安装，直接返回 nil
// （同时清掉残留 .part）；不一致则覆盖重下。onProgress 可为 nil，最多每秒
// 回调 5 次，安装成功后必定再回调一次最终进度。
//
// ctx 取消时返回 ctx.Err() 并保留 .part，下次调用可续传。返回的错误可用
// errors.Is 匹配 ErrChecksum / ErrHTTP / ErrNoURL。
func (d *Downloader) Fetch(ctx context.Context, f File, dst string, onProgress func(Progress)) error {
	if ctx == nil {
		return fmt.Errorf("artifact: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(dst) == "" {
		return fmt.Errorf("artifact: empty destination path")
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("artifact: create dir %s: %w", dir, err)
	}
	part := d.partPath(dst)

	// 幂等：已安装且校验通过就直接成功，并清掉可能残留的 .part。
	if installed(dst, f.SHA256) {
		_ = os.Remove(part)
		emitFinal(dst, onProgress)
		return nil
	}

	candidates := d.candidates(f.URLs)
	if len(candidates) == 0 {
		return ErrNoURL
	}
	var lastErr error
	for _, u := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		digest, err := d.fetchURL(ctx, u, part, f, onProgress)
		if err != nil {
			if ctx.Err() != nil {
				// 取消：保留 .part 供续传，错误就是 ctx.Err()。
				return ctx.Err()
			}
			lastErr = err
			continue
		}
		if err := verifyPart(part, f.SHA256, digest); err != nil {
			lastErr = err
			continue
		}
		if err := os.Rename(part, dst); err != nil {
			return fmt.Errorf("artifact: install %s: %w", dst, err)
		}
		emitFinal(dst, onProgress)
		return nil
	}
	if lastErr == nil {
		return ErrNoURL
	}
	return fmt.Errorf("artifact: fetch %s: %w", dst, lastErr)
}

// partPath 返回临时文件路径：默认 dst+".part"；设置 TempPrefix 时变成
// 同目录下的 TempPrefix+文件名+".part"，保证与 dst 同卷、可原子 rename。
func (d *Downloader) partPath(dst string) string {
	if d.TempPrefix == "" {
		return dst + ".part"
	}
	return filepath.Join(filepath.Dir(dst), d.TempPrefix+filepath.Base(dst)+".part")
}

// httpClient 返回实际使用的客户端。
func (d *Downloader) httpClient() *http.Client {
	if d.Client != nil {
		return d.Client
	}
	return http.DefaultClient
}

// retryCount 归一化 Retries：0 用默认值，负数表示不重试。
func (d *Downloader) retryCount() int {
	if d.Retries < 0 {
		return 0
	}
	if d.Retries == 0 {
		return defaultRetries
	}
	return d.Retries
}

// candidates 生成候选地址：镜像前缀版本优先，其次是原始地址，去重保序。
func (d *Downloader) candidates(urls []string) []string {
	out := make([]string, 0, len(urls)*2)
	seen := make(map[string]struct{}, len(urls)*2)
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" {
			return
		}
		if _, ok := seen[u]; ok {
			return
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	if prefix := strings.TrimSpace(d.MirrorPrefix); prefix != "" {
		for _, u := range urls {
			add(prefix + strings.TrimSpace(u))
		}
	}
	for _, u := range urls {
		add(u)
	}
	return out
}

// installed 判断 dst 是否已经是合格产物。
func installed(dst, wantSHA string) bool {
	st, err := os.Stat(dst)
	if err != nil || st.IsDir() || st.Size() <= 0 {
		return false
	}
	wantSHA = strings.TrimSpace(wantSHA)
	if wantSHA == "" {
		return true
	}
	got, err := hashFile(dst)
	if err != nil {
		return false
	}
	return strings.EqualFold(got, wantSHA)
}

// hashFile 返回文件的 SHA256 十六进制摘要。
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// emitFinal 在安装成功后无条件回调一次最终进度。
func emitFinal(dst string, onProgress func(Progress)) {
	if onProgress == nil {
		return
	}
	var size int64
	if st, err := os.Stat(dst); err == nil && !st.IsDir() {
		size = st.Size()
	}
	onProgress(Progress{Done: size, Total: size})
}

// fetchURL 对单个 URL 做带重试的下载。
func (d *Downloader) fetchURL(ctx context.Context, rawURL, part string, f File, onProgress func(Progress)) (string, error) {
	attempts := d.retryCount() + 1
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(retryBackoff(attempt)):
			}
		}
		digest, err := d.tryOnce(ctx, rawURL, part, f, onProgress)
		if err == nil {
			return digest, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		lastErr = err
		if !retryable(err) {
			break
		}
	}
	return "", lastErr
}

// retryBackoff 返回第 attempt 次重试前的等待时间（100ms、200ms、400ms… 封顶 2s）。
func retryBackoff(attempt int) time.Duration {
	delay := retryBase
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= retryMax {
			return retryMax
		}
	}
	return delay
}

// retryable 判断错误是否值得对同一 URL 重试。
func retryable(err error) bool {
	if err == nil {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.StatusCode >= 500 || he.StatusCode == http.StatusTooManyRequests
	}
	if errors.Is(err, ErrChecksum) || errors.Is(err, ErrNoURL) {
		return false
	}
	// 网络错误、body 截断、读写错误等：重试一次通常有用。
	return true
}

// tryOnce 完成一次「请求 + 落盘」，必要时处理 Range 语义。
func (d *Downloader) tryOnce(ctx context.Context, rawURL, part string, f File, onProgress func(Progress)) (string, error) {
	restarted := false
	for {
		offset, err := partSize(part)
		if err != nil {
			return "", err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return "", fmt.Errorf("artifact: build request %q: %w", rawURL, err)
		}
		if offset > 0 {
			req.Header.Set("Range", "bytes="+strconv.FormatInt(offset, 10)+"-")
		}
		resp, err := d.httpClient().Do(req)
		if err != nil {
			return "", err
		}
		switch resp.StatusCode {
		case http.StatusPartialContent:
			// 续传：offset 就是已落盘的前缀长度。
		case http.StatusOK:
			if offset > 0 {
				// 服务端忽略 Range：丢掉 .part 从头写。
				if err := truncate(part); err != nil {
					resp.Body.Close()
					return "", err
				}
				offset = 0
			}
		case http.StatusRequestedRangeNotSatisfiable:
			resp.Body.Close()
			if restarted {
				return "", &HTTPError{URL: rawURL, StatusCode: resp.StatusCode, Status: resp.Status}
			}
			// .part 与远端不一致（例如远端更新了文件）：清空后重来一次。
			restarted = true
			if err := truncate(part); err != nil {
				return "", err
			}
			continue
		default:
			// 其余状态码：不读 body，直接交给重试/换源逻辑。
			resp.Body.Close()
			return "", &HTTPError{URL: rawURL, StatusCode: resp.StatusCode, Status: resp.Status}
		}
		return d.writePart(ctx, rawURL, part, offset, resp, f, onProgress)
	}
}

// writePart 把响应 body 追加到 .part，返回整份 part 的 SHA256（需要校验时）。
func (d *Downloader) writePart(ctx context.Context, rawURL, part string, offset int64, resp *http.Response, f File, onProgress func(Progress)) (string, error) {
	defer resp.Body.Close()
	wantSHA := strings.TrimSpace(f.SHA256) != ""

	file, hasher, err := openPart(part, offset, wantSHA)
	if err != nil {
		return "", err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()

	total := totalSize(offset, resp, f)
	var writer io.Writer = file
	if wantSHA {
		writer = io.MultiWriter(file, hasher)
	}
	meter := &progressMeter{now: time.Now}
	done := offset
	meter.emit(done, total, onProgress)

	buf := make([]byte, copyBufferSize)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := writer.Write(buf[:n]); werr != nil {
				return "", werr
			}
			done += int64(n)
			meter.emit(done, total, onProgress)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			if err := ctx.Err(); err != nil {
				// 取消：保留 .part，下次续传。
				return "", err
			}
			return "", fmt.Errorf("artifact: read %s: %w", rawURL, readErr)
		}
	}
	if resp.ContentLength >= 0 && done-offset != resp.ContentLength {
		return "", fmt.Errorf("artifact: short body from %s: got %d of %d bytes", rawURL, done-offset, resp.ContentLength)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("artifact: fsync %s: %w", part, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("artifact: close %s: %w", part, err)
	}
	closed = true
	if !wantSHA {
		return "", nil
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// openPart 打开 .part 供追加写入；offset > 0 时先把已有前缀喂给哈希，
// 保证续传后的摘要覆盖整份文件。
func openPart(part string, offset int64, wantSHA bool) (*os.File, hash.Hash, error) {
	hasher := sha256.New()
	if wantSHA && offset > 0 {
		prefix, err := os.Open(part)
		if err != nil {
			return nil, nil, fmt.Errorf("artifact: hash existing prefix: %w", err)
		}
		n, copyErr := io.Copy(hasher, prefix)
		closeErr := prefix.Close()
		if copyErr != nil {
			return nil, nil, fmt.Errorf("artifact: hash existing prefix: %w", copyErr)
		}
		if closeErr != nil {
			return nil, nil, closeErr
		}
		if n != offset {
			return nil, nil, fmt.Errorf("artifact: part size changed while resuming (%d != %d)", n, offset)
		}
	}
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	file, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("artifact: open part %s: %w", part, err)
	}
	return file, hasher, nil
}

// totalSize 计算本次下载的完整总大小；未知时返回 -1。
func totalSize(offset int64, resp *http.Response, f File) int64 {
	if resp.StatusCode == http.StatusPartialContent {
		if total, ok := contentRangeTotal(resp.Header.Get("Content-Range")); ok {
			return total
		}
	}
	if resp.ContentLength >= 0 {
		return offset + resp.ContentLength
	}
	if f.SizeHint > 0 {
		return f.SizeHint
	}
	return -1
}

// contentRangeTotal 从 "bytes 100-199/200" 里解析出总大小 200。
func contentRangeTotal(v string) (int64, bool) {
	i := strings.LastIndexByte(v, '/')
	if i < 0 {
		return 0, false
	}
	s := strings.TrimSpace(v[i+1:])
	if s == "" || s == "*" {
		return 0, false
	}
	total, err := strconv.ParseInt(s, 10, 64)
	if err != nil || total <= 0 {
		return 0, false
	}
	return total, true
}

// partSize 返回 .part 当前大小；不存在算 0。
func partSize(part string) (int64, error) {
	st, err := os.Stat(part)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("artifact: stat part %s: %w", part, err)
	}
	if st.IsDir() {
		return 0, fmt.Errorf("artifact: part path %s is a directory", part)
	}
	return st.Size(), nil
}

// truncate 清空 .part；文件不存在也算成功。
func truncate(path string) error {
	if err := os.Truncate(path, 0); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("artifact: truncate %s: %w", path, err)
	}
	return nil
}

// verifyPart 校验摘要；不匹配时删除 .part 避免脏数据被续传复用。
func verifyPart(part, want, got string) error {
	want = strings.TrimSpace(want)
	if want == "" {
		return nil
	}
	if strings.EqualFold(got, want) {
		return nil
	}
	_ = os.Remove(part)
	return fmt.Errorf("%w: want %s, got %s", ErrChecksum, strings.ToLower(want), got)
}

// progressMeter 对进度回调做节流，结束时的 final 不受节流限制。
type progressMeter struct {
	now  func() time.Time // 便于测试注入；nil 表示 time.Now
	last time.Time
	sent bool
}

// emit 在距上次回调 >= progressInterval 时触发一次回调。
func (m *progressMeter) emit(done, total int64, cb func(Progress)) {
	if cb == nil {
		return
	}
	now := time.Now()
	if m.now != nil {
		now = m.now()
	}
	if m.sent && now.Sub(m.last) < progressInterval {
		return
	}
	m.sent = true
	m.last = now
	cb(Progress{Done: done, Total: total})
}

// HTTPError 描述一次不可用（或不该重试）的 HTTP 响应。
type HTTPError struct {
	URL        string // 请求地址
	StatusCode int    // 响应码
	Status     string // 响应状态文本，如 "404 Not Found"
}

// Error 实现 error。
func (e *HTTPError) Error() string {
	if e.URL == "" {
		return fmt.Sprintf("%v: %s", ErrHTTP, e.Status)
	}
	return fmt.Sprintf("%v: %s: %s", ErrHTTP, e.URL, e.Status)
}

// Unwrap 让 errors.Is(err, ErrHTTP) 成立。
func (e *HTTPError) Unwrap() error { return ErrHTTP }
