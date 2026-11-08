package uiapp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/config"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/engine"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/inhibit"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/models"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/scheduler"
)

// BuildInfo is injected from main via -ldflags.
type BuildInfo struct {
	Version   string
	Commit    string
	BuildTime string
}

// Modes reported to the UI.
const (
	ModeDesktop = "desktop"
	ModeBrowser = "browser"
)

// Emitter forwards events to the active shell (Wails events or SSE broadcast).
type Emitter func(name string, payload any)

// ---- JSON payloads (must stay in sync with docs/CONTRACTS.md §3.2) ----------

type AppInfo struct {
	Version         string      `json:"version"`
	Commit          string      `json:"commit"`
	BuildTime       string      `json:"buildTime"`
	GoVersion       string      `json:"goVersion"`
	Platform        string      `json:"platform"`
	Engine          engine.Info `json:"engine"`
	DataDir         string      `json:"dataDir"`
	ModelDir        string      `json:"modelDir"`
	OutputDir       string      `json:"outputDir"`
	MaxConcurrency  int         `json:"maxConcurrency"`
	NativeLoaded    bool        `json:"nativeLoaded"`
	Mode            string      `json:"mode"`
	BrowseSupported bool        `json:"browseSupported"`
}

type ModelInfo = models.Info

type InhibitRequest struct {
	Sleep       bool `json:"sleep"`
	Display     bool `json:"display"`
	Lock        bool `json:"lock"`
	AutoRelease bool `json:"autoRelease"`
}

type InhibitStatus struct {
	Active           bool   `json:"active"`
	Sleep            bool   `json:"sleep"`
	Display          bool   `json:"display"`
	Lock             bool   `json:"lock"`
	SleepSupported   bool   `json:"sleepSupported"`
	DisplaySupported bool   `json:"displaySupported"`
	LockSupported    bool   `json:"lockSupported"`
	Driver           string `json:"driver"`
	Detail           string `json:"detail"`
	SinceMs          int64  `json:"sinceMs"`
}

type BatchRequest struct {
	Files       []string        `json:"files"`
	ModelID     string          `json:"modelId"`
	ModelPath   string          `json:"modelPath"`
	Scale       int             `json:"scale"`
	OutputDir   string          `json:"outputDir"`
	Concurrency int             `json:"concurrency"`
	TileSize    int             `json:"tileSize"`
	Overlap     int             `json:"overlap"`
	Threads     int             `json:"threads"`
	Fast        bool            `json:"fast"`
	Inhibit     *InhibitRequest `json:"inhibit"`
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
	Kind      string `json:"kind"`
	Index     int    `json:"index"`
	Done      int    `json:"done"`
	Total     int    `json:"total"`
	Output    string `json:"output"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Error     string `json:"error"`
	Backend   string `json:"backend"`
	ElapsedMs int64  `json:"elapsedMs"`
}

type JobSummary struct {
	JobID     string `json:"jobId"`
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
	Failed    int    `json:"failed"`
	Cancelled bool   `json:"cancelled"`
	ElapsedMs int64  `json:"elapsedMs"`
}

type LogEvent struct {
	Level  string `json:"level"`
	Msg    string `json:"msg"`
	TimeMs int64  `json:"timeMs"`
}

// DirEntry / DirListing back the browser-mode server-side path picker: browsers
// cannot expose absolute file paths, so the Go side lists the filesystem.
type DirEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"isDir"`
	Size    int64  `json:"size"`
	IsImage bool   `json:"isImage"`
}

type DirListing struct {
	Path    string     `json:"path"`
	Parent  string     `json:"parent"`
	Entries []DirEntry `json:"entries"`
}

// ---- Core -------------------------------------------------------------------

// Core owns all app state and behaviour and is shared by the Wails shell and the
// browser-mode HTTP server. It deliberately has no Wails dependency.
type Core struct {
	mu         sync.Mutex
	emitter    Emitter
	build      BuildInfo
	mode       string
	settings   *config.Settings
	dataDir    string
	models     *models.Store
	manager    *scheduler.Manager
	inhibitor  *inhibit.Manager
	engineInfo engine.Info
	autoFree   bool
	logFile    *os.File
	started    bool
}

// New creates the core. Call SetEmitter before Startup to receive the boot
// events (log / inhibit state).
func New(build BuildInfo) *Core {
	if build.Version == "" {
		build.Version = "0.1.0"
	}
	if build.Commit == "" {
		build.Commit = "dev"
	}
	if build.BuildTime == "" {
		build.BuildTime = "unknown"
	}
	return &Core{build: build, mode: ModeDesktop}
}

// SetEmitter installs the shell-specific event sink.
func (c *Core) SetEmitter(fn Emitter) {
	c.mu.Lock()
	c.emitter = fn
	c.mu.Unlock()
}

// SetMode is desktop or browser; it changes only what the UI/dialogs do.
func (c *Core) SetMode(mode string) {
	c.mu.Lock()
	if mode == ModeBrowser {
		c.mode = ModeBrowser
	} else {
		c.mode = ModeDesktop
	}
	c.mu.Unlock()
}

// Mode reports the active mode.
func (c *Core) Mode() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode
}

// Startup initialises config, model store, engine probe, scheduler and inhibitor.
func (c *Core) Startup() {
	c.initialise()
}

// Shutdown releases platform resources (the inhibitor must never leak).
func (c *Core) Shutdown() {
	c.mu.Lock()
	mgr, inh, logFile := c.manager, c.inhibitor, c.logFile
	c.mu.Unlock()
	if mgr != nil {
		_ = mgr.Close()
	}
	if inh != nil {
		_ = inh.Close()
	}
	if logFile != nil {
		_ = logFile.Close()
	}
}

func (c *Core) initialise() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return
	}
	c.started = true

	dataDir, err := config.DataDir()
	if err != nil {
		dataDir = "."
	}
	c.dataDir = dataDir
	if f, err := os.OpenFile(filepath.Join(dataDir, "chronos.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		c.logFile = f
	}

	settings, loadErr := config.Load(dataDir)
	c.settings = settings
	if loadErr != nil {
		c.logLocked("warn", "读取设置失败，已使用默认值: "+loadErr.Error())
	}

	store, err := models.NewStore("", nil)
	if err != nil {
		c.logLocked("warn", "模型目录不可用: "+err.Error())
	}
	c.models = store

	probe, err := engine.NewProvider(engine.Config{Scale: 2}, 1)
	if err != nil {
		c.logLocked("error", "引擎初始化失败: "+err.Error())
	} else {
		c.engineInfo = probe.Info()
		_ = probe.Close()
	}

	c.manager = scheduler.New(scheduler.Config{
		MaxConcurrency:  settings.Concurrency,
		ProviderFactory: c.newProvider,
	})
	go c.forwardEvents()

	c.inhibitor = inhibit.New()
	c.logLocked("info", fmt.Sprintf("Chronos %s 启动 · 后端 %s · %s · 模式 %s", c.build.Version, c.engineInfo.Backend, runtime.GOOS+"/"+runtime.GOARCH, c.mode))
	c.emitInhibitLocked()
}

func (c *Core) newProvider(spec scheduler.JobSpec) (engine.Provider, error) {
	return engine.NewProvider(engine.Config{
		ModelPath: spec.ModelPath,
		Scale:     spec.Scale,
		TileSize:  spec.TileSize,
		Overlap:   spec.Overlap,
		Threads:   spec.Threads,
		Fast:      spec.Fast,
	}, engine.MaxConcurrency)
}

// ---- bindings --------------------------------------------------------------

// GetAppInfo returns everything the header/status bar needs.
func (c *Core) GetAppInfo() AppInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := AppInfo{
		Version:         c.build.Version,
		Commit:          c.build.Commit,
		BuildTime:       c.build.BuildTime,
		GoVersion:       runtime.Version(),
		Platform:        runtime.GOOS + "/" + runtime.GOARCH,
		Engine:          c.engineInfo,
		DataDir:         c.dataDir,
		MaxConcurrency:  engine.MaxConcurrency,
		NativeLoaded:    c.engineInfo.Native,
		Mode:            c.mode,
		BrowseSupported: c.mode == ModeBrowser,
	}
	if c.models != nil {
		out.ModelDir = c.models.Dir()
	}
	if c.settings != nil {
		out.OutputDir = c.settings.OutputDir
	}
	if out.OutputDir == "" {
		out.OutputDir = config.DefaultOutputDir()
	}
	return out
}

// ListModels returns the embedded registry plus installed state.
func (c *Core) ListModels() []ModelInfo {
	c.mu.Lock()
	store := c.models
	c.mu.Unlock()
	if store == nil {
		return nil
	}
	return store.List()
}

// RefreshModels re-scans the model cache directory.
func (c *Core) RefreshModels() []ModelInfo { return c.ListModels() }

// UpscaleBatch queues a batch and returns immediately with its job id.
func (c *Core) UpscaleBatch(req BatchRequest) (BatchResult, error) {
	c.mu.Lock()
	manager, store, settings := c.manager, c.models, c.settings
	if manager == nil {
		c.mu.Unlock()
		return BatchResult{}, fmt.Errorf("chronos: app not ready")
	}
	if req.Concurrency >= 1 {
		if err := manager.SetConcurrency(req.Concurrency); err != nil {
			c.mu.Unlock()
			return BatchResult{}, err
		}
	}
	spec := scheduler.JobSpec{
		Scale:    req.Scale,
		TileSize: req.TileSize,
		Overlap:  req.Overlap,
		Threads:  req.Threads,
		Fast:     req.Fast,
	}
	if req.ModelPath != "" {
		spec.ModelPath = req.ModelPath
	} else if req.ModelID != "" {
		if store == nil {
			c.mu.Unlock()
			return BatchResult{}, fmt.Errorf("chronos: model store unavailable")
		}
		path, installed, err := store.Resolve(req.ModelID)
		if err != nil {
			c.mu.Unlock()
			return BatchResult{}, err
		}
		if !installed {
			c.mu.Unlock()
			return BatchResult{}, fmt.Errorf("chronos: 模型 %s 尚未下载，请先在模型管理里下载", req.ModelID)
		}
		spec.ModelPath = path
		if e, ok := store.Registry().Lookup(req.ModelID); ok && spec.Scale <= 0 {
			spec.Scale = e.Scale
		}
	}
	if spec.Scale <= 0 {
		spec.Scale = 2
	}
	if req.Inhibit != nil && (req.Inhibit.Sleep || req.Inhibit.Display || req.Inhibit.Lock) {
		c.autoFree = req.Inhibit.AutoRelease
		if c.inhibitor != nil {
			st, err := c.inhibitor.Set(inhibit.Options{
				Sleep:   req.Inhibit.Sleep,
				Display: req.Inhibit.Display,
				Lock:    req.Inhibit.Lock,
				Reason:  "Chronos: 批量超分进行中",
			})
			if err != nil {
				c.logLocked("warn", "睡眠/锁屏抑制启用失败: "+err.Error())
			}
			c.emitInhibitStatusLocked(toInhibitStatus(st))
		}
	}
	if settings != nil {
		settings.Concurrency = manager.Concurrency()
		settings.LastModelID = req.ModelID
		settings.Scale = spec.Scale
		settings.TileSize = req.TileSize
		settings.Overlap = req.Overlap
		settings.Threads = req.Threads
		settings.Fast = req.Fast
		settings.OutputDir = req.OutputDir
	}
	c.mu.Unlock()

	res, err := manager.Submit(scheduler.Job{Files: req.Files, OutputDir: req.OutputDir, Spec: spec})
	if err != nil {
		c.log("error", "提交任务失败: "+err.Error())
		return BatchResult{}, err
	}
	if len(res.Skipped) > 0 {
		c.log("warn", fmt.Sprintf("%d 个文件被跳过（不存在/重复/非图片）", len(res.Skipped)))
	}
	c.persistSettings()
	return BatchResult{JobID: res.JobID, Queued: res.Queued, Skipped: res.Skipped}, nil
}

// CancelJob cancels a running batch.
func (c *Core) CancelJob(jobID string) bool {
	c.mu.Lock()
	manager := c.manager
	c.mu.Unlock()
	if manager == nil {
		return false
	}
	ok := manager.CancelJob(jobID)
	if ok {
		c.log("warn", "用户取消任务 "+jobID)
	}
	return ok
}

// SetConcurrency updates the live worker limit (1..8).
func (c *Core) SetConcurrency(n int) error {
	c.mu.Lock()
	manager := c.manager
	c.mu.Unlock()
	if manager == nil {
		return fmt.Errorf("chronos: app not ready")
	}
	if err := manager.SetConcurrency(n); err != nil {
		return err
	}
	c.mu.Lock()
	if c.settings != nil {
		c.settings.Concurrency = n
	}
	c.mu.Unlock()
	c.persistSettings()
	return nil
}

// GetConcurrency returns the live limit.
func (c *Core) GetConcurrency() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.manager == nil {
		return 0
	}
	return c.manager.Concurrency()
}

// SetInhibit enables/disables sleep, display and lock inhibition.
func (c *Core) SetInhibit(req InhibitRequest) (InhibitStatus, error) {
	c.mu.Lock()
	inh := c.inhibitor
	if c.settings != nil {
		c.settings.Inhibit = config.InhibitPrefs{Sleep: req.Sleep, Display: req.Display, Lock: req.Lock, AutoRelease: req.AutoRelease}
	}
	c.autoFree = req.AutoRelease
	c.mu.Unlock()

	if inh == nil {
		return InhibitStatus{}, fmt.Errorf("chronos: inhibitor not ready")
	}
	st, err := inh.Set(inhibit.Options{Sleep: req.Sleep, Display: req.Display, Lock: req.Lock, Reason: "Chronos 用户设置"})
	status := toInhibitStatus(st)
	c.log("info", fmt.Sprintf("睡眠抑制: sleep=%v display=%v lock=%v active=%v", status.Sleep, status.Display, status.Lock, status.Active))
	c.persistSettings()
	return status, err
}

// GetInhibitStatus reports the current inhibition state and capabilities.
func (c *Core) GetInhibitStatus() InhibitStatus {
	c.mu.Lock()
	inh := c.inhibitor
	c.mu.Unlock()
	if inh == nil {
		return InhibitStatus{Driver: "unavailable", Detail: "inhibitor not ready"}
	}
	return toInhibitStatus(inh.Status())
}

// PickImages is the browser-mode fallback: browsers cannot expose absolute
// paths, so the UI uses ListDirectory instead. The desktop shell overrides this
// with a native dialog.
func (c *Core) PickImages() []string { return nil }

// PickOutputDir is the browser-mode fallback (see PickImages).
func (c *Core) PickOutputDir() string { return "" }

// ListDirectory lists one directory for the browser-mode path picker. An empty
// path starts at the user's home directory.
func (c *Core) ListDirectory(path string) (DirListing, error) {
	if strings.TrimSpace(path) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return DirListing{}, fmt.Errorf("chronos: cannot resolve home dir: %w", err)
		}
		path = home
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return DirListing{}, fmt.Errorf("chronos: invalid path: %w", err)
	}
	infos, err := os.ReadDir(abs)
	if err != nil {
		return DirListing{}, fmt.Errorf("chronos: list %s: %w", abs, err)
	}
	listing := DirListing{Path: abs, Entries: make([]DirEntry, 0, len(infos))}
	if parent := filepath.Dir(abs); parent != abs {
		listing.Parent = parent
	}
	for _, info := range infos {
		name := info.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(abs, name)
		isDir := info.IsDir()
		if !isDir && !isImageFile(name) {
			continue
		}
		var size int64
		if info.Type().IsRegular() {
			if fi, statErr := info.Info(); statErr == nil {
				size = fi.Size()
			}
		}
		listing.Entries = append(listing.Entries, DirEntry{
			Name:    name,
			Path:    full,
			IsDir:   isDir,
			Size:    size,
			IsImage: !isDir,
		})
	}
	sort.Slice(listing.Entries, func(i, j int) bool {
		// Directories first, then case-insensitive name order.
		if listing.Entries[i].IsDir != listing.Entries[j].IsDir {
			return listing.Entries[i].IsDir
		}
		return strings.ToLower(listing.Entries[i].Name) < strings.ToLower(listing.Entries[j].Name)
	})
	return listing, nil
}

// Reveal opens the file manager at the given file/directory (same OS as the
// server, so it also works from browser mode when they share a machine).
func (c *Core) Reveal(path string) error {
	if path == "" {
		return fmt.Errorf("chronos: empty path")
	}
	return revealPath(path)
}

// OpenDataDir reveals the per-user data directory.
func (c *Core) OpenDataDir() error {
	c.mu.Lock()
	dir := c.dataDir
	c.mu.Unlock()
	if dir == "" {
		return fmt.Errorf("chronos: data dir unknown")
	}
	return revealPath(dir)
}

// ---- internals -------------------------------------------------------------

func (c *Core) forwardEvents() {
	manager := c.manager
	for ev := range manager.Events() {
		if ev.Kind == "job-done" {
			c.emit("chronos:job", JobSummary{
				JobID:     ev.JobID,
				Total:     ev.Total,
				Completed: ev.Completed,
				Failed:    ev.Failed,
				Cancelled: ev.Cancelled,
				ElapsedMs: ev.ElapsedMs,
			})
			c.log("info", fmt.Sprintf("任务完成: %d 成功 / %d 失败 / %d 合计 (取消=%v)", ev.Completed, ev.Failed, ev.Total, ev.Cancelled))
			c.releaseAutoInhibit()
			continue
		}
		c.emit("chronos:progress", ProgressEvent{
			JobID:     ev.JobID,
			TaskID:    ev.TaskID,
			File:      ev.File,
			Kind:      ev.Kind,
			Index:     ev.Index,
			Done:      ev.Done,
			Total:     ev.Total,
			Output:    ev.Output,
			Width:     ev.Width,
			Height:    ev.Height,
			Error:     ev.Error,
			Backend:   ev.Backend,
			ElapsedMs: ev.ElapsedMs,
		})
	}
}

func (c *Core) releaseAutoInhibit() {
	c.mu.Lock()
	auto, inh := c.autoFree, c.inhibitor
	c.mu.Unlock()
	if !auto || inh == nil {
		return
	}
	st, err := inh.Set(inhibit.Options{})
	if err != nil {
		c.log("warn", "释放抑制失败: "+err.Error())
	}
	c.mu.Lock()
	c.autoFree = false
	if c.settings != nil {
		c.settings.Inhibit = config.InhibitPrefs{}
	}
	c.mu.Unlock()
	c.emitInhibitStatus(toInhibitStatus(st))
	c.persistSettings()
}

func (c *Core) persistSettings() {
	c.mu.Lock()
	settings, dir := c.settings, c.dataDir
	c.mu.Unlock()
	if settings == nil || dir == "" {
		return
	}
	if err := settings.Save(dir); err != nil {
		c.log("warn", "保存设置失败: "+err.Error())
	}
}

func (c *Core) log(level, msg string) {
	c.mu.Lock()
	c.logLocked(level, msg)
	c.mu.Unlock()
}

func (c *Core) logLocked(level, msg string) {
	line := fmt.Sprintf("%s [%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), level, msg)
	if c.logFile != nil {
		_, _ = c.logFile.WriteString(line)
	}
	c.emitLocked("chronos:log", LogEvent{Level: level, Msg: msg, TimeMs: time.Now().UnixMilli()})
}

func (c *Core) emit(name string, payload any) {
	c.mu.Lock()
	fn := c.emitter
	c.mu.Unlock()
	if fn != nil {
		fn(name, payload)
	}
}

// emitLocked must be called while c.mu is held (it avoids re-locking).
func (c *Core) emitLocked(name string, payload any) {
	if c.emitter != nil {
		c.emitter(name, payload)
	}
}

func (c *Core) emitInhibitLocked() {
	if c.inhibitor == nil {
		return
	}
	c.emitInhibitStatusLocked(toInhibitStatus(c.inhibitor.Status()))
}

func (c *Core) emitInhibitStatus(status InhibitStatus) {
	c.emit("chronos:inhibit", status)
}

func (c *Core) emitInhibitStatusLocked(status InhibitStatus) {
	c.emitLocked("chronos:inhibit", status)
}

func toInhibitStatus(st inhibit.Status) InhibitStatus {
	return InhibitStatus{
		Active:           st.Active,
		Sleep:            st.Sleep,
		Display:          st.Display,
		Lock:             st.Lock,
		SleepSupported:   st.SleepSupported,
		DisplaySupported: st.DisplaySupported,
		LockSupported:    st.LockSupported,
		Driver:           st.Driver,
		Detail:           st.Detail,
		SinceMs:          st.SinceMs,
	}
}

func isImageFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".bmp", ".gif", ".webp", ".tif", ".tiff":
		return true
	}
	return false
}

func revealPath(path string) error {
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		switch runtime.GOOS {
		case "windows":
			return exec.Command("explorer.exe", path).Start()
		case "linux":
			return exec.Command("xdg-open", path).Start()
		default:
			return fmt.Errorf("chronos: reveal not supported on %s", runtime.GOOS)
		}
	}
	switch runtime.GOOS {
	case "windows":
		return exec.Command("explorer.exe", "/select,", path).Start()
	case "linux":
		return exec.Command("xdg-open", filepath.Dir(path)).Start()
	default:
		return fmt.Errorf("chronos: reveal not supported on %s", runtime.GOOS)
	}
}

// Context is unused today but keeps the exported surface stable for shells that
// want to pass a cancellation context (browser server shutdown, tests).
var _ = context.Background
