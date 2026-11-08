package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/config"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/engine"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/inhibit"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/models"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/scheduler"
)

// Linker-injected build metadata (see .github/workflows/ci.yml).
var (
	version   = "0.1.0"
	commit    = "dev"
	buildTime = "unknown"
)

// ---- JSON payloads (must stay in sync with docs/CONTRACTS.md §3.2) ----------

type AppInfo struct {
	Version        string      `json:"version"`
	Commit         string      `json:"commit"`
	BuildTime      string      `json:"buildTime"`
	GoVersion      string      `json:"goVersion"`
	Platform       string      `json:"platform"`
	Engine         engine.Info `json:"engine"`
	DataDir        string      `json:"dataDir"`
	ModelDir       string      `json:"modelDir"`
	OutputDir      string      `json:"outputDir"`
	MaxConcurrency int         `json:"maxConcurrency"`
	NativeLoaded   bool        `json:"nativeLoaded"`
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

// ---- App -------------------------------------------------------------------

// App is the Wails-bound facade. It owns no image data: everything below this
// layer is file paths, scheduler bookkeeping and OS inhibit calls.
type App struct {
	mu         sync.Mutex
	ctx        context.Context
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

// NewApp is called from main before the webview exists.
func NewApp() *App { return &App{} }

// startup is the Wails lifecycle hook.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.initialise()
}

// shutdown releases platform resources (the inhibitor must never leak).
func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	mgr, inh, logFile := a.manager, a.inhibitor, a.logFile
	a.mu.Unlock()
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

func (a *App) initialise() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		return
	}
	a.started = true

	dataDir, err := config.DataDir()
	if err != nil {
		dataDir = "."
	}
	a.dataDir = dataDir
	if f, err := os.OpenFile(filepath.Join(dataDir, "chronos.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		a.logFile = f
	}

	settings, loadErr := config.Load(dataDir)
	a.settings = settings
	if loadErr != nil {
		a.log("warn", "读取设置失败，已使用默认值: "+loadErr.Error())
	}

	store, err := models.NewStore("", nil)
	if err != nil {
		a.log("warn", "模型目录不可用: "+err.Error())
	}
	a.models = store

	probe, err := engine.NewProvider(engine.Config{Scale: 2}, 1)
	if err != nil {
		a.log("error", "引擎初始化失败: "+err.Error())
	} else {
		a.engineInfo = probe.Info()
		_ = probe.Close()
	}

	a.manager = scheduler.New(scheduler.Config{
		MaxConcurrency:  settings.Concurrency,
		ProviderFactory: a.newProvider,
	})
	go a.forwardEvents()

	a.inhibitor = inhibit.New()
	a.log("info", fmt.Sprintf("Chronos %s 启动 · 后端 %s · %s", version, a.engineInfo.Backend, runtime.GOOS+"/"+runtime.GOARCH))
	a.emitInhibitLocked()
}

func (a *App) newProvider(spec scheduler.JobSpec) (engine.Provider, error) {
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
func (a *App) GetAppInfo() AppInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := AppInfo{
		Version:        version,
		Commit:         commit,
		BuildTime:      buildTime,
		GoVersion:      runtime.Version(),
		Platform:       runtime.GOOS + "/" + runtime.GOARCH,
		Engine:         a.engineInfo,
		DataDir:        a.dataDir,
		MaxConcurrency: engine.MaxConcurrency,
		NativeLoaded:   a.engineInfo.Native,
	}
	if a.models != nil {
		out.ModelDir = a.models.Dir()
	}
	if a.settings != nil {
		out.OutputDir = a.settings.OutputDir
	}
	if out.OutputDir == "" {
		out.OutputDir = config.DefaultOutputDir()
	}
	return out
}

// ListModels returns the embedded registry plus installed state.
func (a *App) ListModels() []ModelInfo {
	a.mu.Lock()
	store := a.models
	a.mu.Unlock()
	if store == nil {
		return nil
	}
	return store.List()
}

// RefreshModels re-scans the model cache directory.
func (a *App) RefreshModels() []ModelInfo { return a.ListModels() }

// UpscaleBatch queues a batch and returns immediately with its job id.
func (a *App) UpscaleBatch(req BatchRequest) (BatchResult, error) {
	a.mu.Lock()
	manager, store, settings := a.manager, a.models, a.settings
	if manager == nil {
		a.mu.Unlock()
		return BatchResult{}, fmt.Errorf("chronos: app not ready")
	}
	if req.Concurrency >= 1 {
		if err := manager.SetConcurrency(req.Concurrency); err != nil {
			a.mu.Unlock()
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
			a.mu.Unlock()
			return BatchResult{}, fmt.Errorf("chronos: model store unavailable")
		}
		path, installed, err := store.Resolve(req.ModelID)
		if err != nil {
			a.mu.Unlock()
			return BatchResult{}, err
		}
		if !installed {
			a.mu.Unlock()
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
		a.autoFree = req.Inhibit.AutoRelease
		if a.inhibitor != nil {
			st, err := a.inhibitor.Set(inhibit.Options{
				Sleep:   req.Inhibit.Sleep,
				Display: req.Inhibit.Display,
				Lock:    req.Inhibit.Lock,
				Reason:  "Chronos: 批量超分进行中",
			})
			if err != nil {
				a.logLocked("warn", "睡眠/锁屏抑制启用失败: "+err.Error())
			}
			a.emitInhibitStatusLocked(toInhibitStatus(st))
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
	a.mu.Unlock()

	res, err := manager.Submit(scheduler.Job{Files: req.Files, OutputDir: req.OutputDir, Spec: spec})
	if err != nil {
		a.log("error", "提交任务失败: "+err.Error())
		return BatchResult{}, err
	}
	if len(res.Skipped) > 0 {
		a.log("warn", fmt.Sprintf("%d 个文件被跳过（不存在/重复/非图片）", len(res.Skipped)))
	}
	a.persistSettings()
	return BatchResult{JobID: res.JobID, Queued: res.Queued, Skipped: res.Skipped}, nil
}

// CancelJob cancels a running batch.
func (a *App) CancelJob(jobID string) bool {
	a.mu.Lock()
	manager := a.manager
	a.mu.Unlock()
	if manager == nil {
		return false
	}
	ok := manager.CancelJob(jobID)
	if ok {
		a.log("warn", "用户取消任务 "+jobID)
	}
	return ok
}

// SetConcurrency updates the live worker limit (1..8).
func (a *App) SetConcurrency(n int) error {
	a.mu.Lock()
	manager := a.manager
	a.mu.Unlock()
	if manager == nil {
		return fmt.Errorf("chronos: app not ready")
	}
	if err := manager.SetConcurrency(n); err != nil {
		return err
	}
	a.mu.Lock()
	if a.settings != nil {
		a.settings.Concurrency = n
	}
	a.mu.Unlock()
	a.persistSettings()
	return nil
}

// GetConcurrency returns the live limit.
func (a *App) GetConcurrency() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.manager == nil {
		return 0
	}
	return a.manager.Concurrency()
}

// SetInhibit enables/disables sleep, display and lock inhibition.
func (a *App) SetInhibit(req InhibitRequest) (InhibitStatus, error) {
	a.mu.Lock()
	inh := a.inhibitor
	if a.settings != nil {
		a.settings.Inhibit = config.InhibitPrefs{Sleep: req.Sleep, Display: req.Display, Lock: req.Lock, AutoRelease: req.AutoRelease}
	}
	a.autoFree = req.AutoRelease
	a.mu.Unlock()

	if inh == nil {
		return InhibitStatus{}, fmt.Errorf("chronos: inhibitor not ready")
	}
	st, err := inh.Set(inhibit.Options{Sleep: req.Sleep, Display: req.Display, Lock: req.Lock, Reason: "Chronos 用户设置"})
	status := toInhibitStatus(st)
	a.log("info", fmt.Sprintf("睡眠抑制: sleep=%v display=%v lock=%v active=%v", status.Sleep, status.Display, status.Lock, status.Active))
	a.persistSettings()
	return status, err
}

// GetInhibitStatus reports the current inhibition state and capabilities.
func (a *App) GetInhibitStatus() InhibitStatus {
	a.mu.Lock()
	inh := a.inhibitor
	a.mu.Unlock()
	if inh == nil {
		return InhibitStatus{Driver: "unavailable", Detail: "inhibitor not ready"}
	}
	return toInhibitStatus(inh.Status())
}

// PickImages opens the native multi-file picker.
func (a *App) PickImages() []string {
	if a.ctx == nil {
		return nil
	}
	files, err := wailsruntime.OpenMultipleFilesDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "选择要超分的图片",
		Filters: []wailsruntime.FileFilter{{
			DisplayName: "图片 (*.png;*.jpg;*.jpeg;*.bmp;*.gif;*.webp)",
			Pattern:     "*.png;*.jpg;*.jpeg;*.bmp;*.gif;*.webp",
		}},
	})
	if err != nil {
		return nil
	}
	return files
}

// PickOutputDir opens the native directory picker.
func (a *App) PickOutputDir() string {
	if a.ctx == nil {
		return ""
	}
	dir, err := wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{Title: "选择输出目录"})
	if err != nil {
		return ""
	}
	if dir != "" {
		a.mu.Lock()
		if a.settings != nil {
			a.settings.OutputDir = dir
		}
		a.mu.Unlock()
		a.persistSettings()
	}
	return dir
}

// Reveal opens the file manager at the given file/directory.
func (a *App) Reveal(path string) error {
	if path == "" {
		return fmt.Errorf("chronos: empty path")
	}
	return revealPath(path)
}

// OpenDataDir reveals the per-user data directory.
func (a *App) OpenDataDir() error {
	a.mu.Lock()
	dir := a.dataDir
	a.mu.Unlock()
	if dir == "" {
		return fmt.Errorf("chronos: data dir unknown")
	}
	return revealPath(dir)
}

// ---- internals -------------------------------------------------------------

func (a *App) forwardEvents() {
	manager := a.manager
	for ev := range manager.Events() {
		if ev.Kind == "job-done" {
			if a.ctx != nil {
				wailsruntime.EventsEmit(a.ctx, "chronos:job", JobSummary{
					JobID:     ev.JobID,
					Total:     ev.Total,
					Completed: ev.Completed,
					Failed:    ev.Failed,
					Cancelled: ev.Cancelled,
					ElapsedMs: ev.ElapsedMs,
				})
			}
			a.log("info", fmt.Sprintf("任务完成: %d 成功 / %d 失败 / %d 合计 (取消=%v)", ev.Completed, ev.Failed, ev.Total, ev.Cancelled))
			a.releaseAutoInhibit()
			continue
		}
		if a.ctx == nil {
			continue
		}
		wailsruntime.EventsEmit(a.ctx, "chronos:progress", ProgressEvent{
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

func (a *App) releaseAutoInhibit() {
	a.mu.Lock()
	auto, inh := a.autoFree, a.inhibitor
	a.mu.Unlock()
	if !auto || inh == nil {
		return
	}
	st, err := inh.Set(inhibit.Options{})
	if err != nil {
		a.log("warn", "释放抑制失败: "+err.Error())
	}
	a.mu.Lock()
	a.autoFree = false
	if a.settings != nil {
		a.settings.Inhibit = config.InhibitPrefs{}
	}
	a.mu.Unlock()
	a.emitInhibitStatus(toInhibitStatus(st))
	a.persistSettings()
}

func (a *App) persistSettings() {
	a.mu.Lock()
	settings, dir := a.settings, a.dataDir
	a.mu.Unlock()
	if settings == nil || dir == "" {
		return
	}
	if err := settings.Save(dir); err != nil {
		a.log("warn", "保存设置失败: "+err.Error())
	}
}

func (a *App) log(level, msg string) {
	a.mu.Lock()
	a.logLocked(level, msg)
	a.mu.Unlock()
}

func (a *App) logLocked(level, msg string) {
	line := fmt.Sprintf("%s [%s] %s\n", time.Now().Format("2006-01-02 15:04:05"), level, msg)
	if a.logFile != nil {
		_, _ = a.logFile.WriteString(line)
	}
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "chronos:log", LogEvent{Level: level, Msg: msg, TimeMs: time.Now().UnixMilli()})
	}
}

func (a *App) emitInhibitLocked() {
	if a.inhibitor == nil {
		return
	}
	status := toInhibitStatus(a.inhibitor.Status())
	a.emitInhibitStatusLocked(status)
}

func (a *App) emitInhibitStatus(status InhibitStatus) {
	a.emitInhibitStatusLocked(status)
}

func (a *App) emitInhibitStatusLocked(status InhibitStatus) {
	if a.ctx == nil {
		return
	}
	wailsruntime.EventsEmit(a.ctx, "chronos:inhibit", status)
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
