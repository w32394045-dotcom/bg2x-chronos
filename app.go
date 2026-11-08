package main

import (
	"context"
	"os"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/uiapp"
)

// Linker-injected build metadata (see .github/workflows/ci.yml).
var (
	version   = "0.1.0"
	commit    = "dev"
	buildTime = "unknown"
)

// App is the thin Wails shell around uiapp.Core: it only adds the native file
// dialogs and forwards Wails events, so the exact same behaviour is available in
// browser mode (internal/browser) without a webview.
type App struct {
	core *uiapp.Core
	ctx  context.Context
}

// NewApp is called from main before the webview exists.
func NewApp() *App {
	return &App{core: uiapp.New(uiapp.BuildInfo{Version: version, Commit: commit, BuildTime: buildTime})}
}

// startup is the Wails lifecycle hook.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.core.SetMode(uiapp.ModeDesktop)
	a.core.SetEmitter(func(name string, payload any) {
		wailsruntime.EventsEmit(ctx, name, payload)
	})
	a.core.Startup()
}

// shutdown releases platform resources (the inhibitor must never leak).
func (a *App) shutdown(context.Context) {
	a.core.Shutdown()
}

// ---- bindings delegated to the core ---------------------------------------

func (a *App) GetAppInfo() uiapp.AppInfo { return a.core.GetAppInfo() }

func (a *App) ListModels() []uiapp.ModelInfo { return a.core.ListModels() }

func (a *App) RefreshModels() []uiapp.ModelInfo { return a.core.ListModels() }

func (a *App) UpscaleBatch(req uiapp.BatchRequest) (uiapp.BatchResult, error) {
	return a.core.UpscaleBatch(req)
}

func (a *App) CancelJob(jobID string) bool { return a.core.CancelJob(jobID) }

func (a *App) SetConcurrency(n int) error { return a.core.SetConcurrency(n) }

func (a *App) GetConcurrency() int { return a.core.GetConcurrency() }

func (a *App) SetInhibit(req uiapp.InhibitRequest) (uiapp.InhibitStatus, error) {
	return a.core.SetInhibit(req)
}

func (a *App) GetInhibitStatus() uiapp.InhibitStatus { return a.core.GetInhibitStatus() }

func (a *App) Reveal(path string) error { return a.core.Reveal(path) }

func (a *App) OpenDataDir() error { return a.core.OpenDataDir() }

func (a *App) ListDirectory(path string) (uiapp.DirListing, error) {
	return a.core.ListDirectory(path)
}

// ---- model manager (contract v3) -----------------------------------------

func (a *App) PullModel(req uiapp.PullModelRequest) (uiapp.ModelPullState, error) {
	return a.core.PullModel(req)
}

func (a *App) CancelPull(modelID string) bool { return a.core.CancelPull(modelID) }

func (a *App) RemoveModel(modelID string) error { return a.core.RemoveModel(modelID) }

func (a *App) ImportModel(req uiapp.ImportModelRequest) (uiapp.ModelInfo, error) {
	return a.core.ImportModel(req)
}

func (a *App) SelfTestModel(modelID string) uiapp.ModelSelfTest {
	return a.core.SelfTestModel(modelID, true)
}

func (a *App) GetModelDiagnostics(modelID string) uiapp.ModelDiagnostics {
	return a.core.GetModelDiagnostics(modelID)
}

// ---- acceleration (contract v3) ------------------------------------------

func (a *App) GetAccelStatus() uiapp.AccelStatus { return a.core.GetAccelStatus() }

func (a *App) SetAccelMode(mode string) (uiapp.AccelStatus, error) {
	return a.core.SetAccelMode(mode)
}
func (a *App) InstallAccelRuntime() (uiapp.AccelStatus, error) {
	return a.core.InstallAccelRuntime()
}

// ---- desktop-only native dialogs -----------------------------------------

// PickImages opens the native multi-file picker (browser mode uses ListDirectory).
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
	return dir
}

// PickModelFile opens the native single-file picker used by the model import
// dialog. Browser mode returns "" and the UI uses ListDirectory instead.
func (a *App) PickModelFile() string {
	if a.ctx == nil {
		return ""
	}
	file, err := wailsruntime.OpenFileDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "选择 ONNX 模型文件",
		Filters: []wailsruntime.FileFilter{{
			DisplayName: "ONNX 模型 (*.onnx;*.xml)",
			Pattern:     "*.onnx;*.xml",
		}},
	})
	if err != nil {
		return ""
	}
	return file
}

var _ = os.Getenv
