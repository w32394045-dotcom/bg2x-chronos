package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/browser"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	// Browser mode: explicit switch, missing WebView2, or a headless Linux
	// session. The UI is then served on 127.0.0.1 and opened in the default
	// browser, using the exact same app core as the desktop shell.
	if forceBrowser() || !desktopAvailable() {
		if err := runBrowserMode(app); err != nil {
			fmt.Fprintln(os.Stderr, "chronos:", err)
			os.Exit(1)
		}
		return
	}

	err := wails.Run(&options.App{
		Title:     "Chronos 超分工作站",
		Width:     1240,
		Height:    840,
		MinWidth:  980,
		MinHeight: 680,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 12, G: 15, B: 22, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "chronos: 桌面窗口启动失败 (%v)，改用浏览器模式\n", err)
		if fbErr := runBrowserMode(app); fbErr != nil {
			fmt.Fprintln(os.Stderr, "chronos:", fbErr)
			os.Exit(1)
		}
	}
}

// runBrowserMode serves the embedded UI and opens the default browser.
func runBrowserMode(app *App) error {
	sub, err := fs.Sub(assets, "frontend/dist")
	if err != nil {
		return fmt.Errorf("chronos: embedded UI assets: %w", err)
	}
	return browser.Run(context.Background(), app.core, browser.Options{
		Addr:   "127.0.0.1",
		Port:   0,
		Open:   true,
		Assets: sub,
	})
}

// forceBrowser is the explicit opt-in (useful for shortcuts/tests).
func forceBrowser() bool {
	if os.Getenv("CHRONOS_BROWSER") == "1" {
		return true
	}
	for _, arg := range os.Args[1:] {
		if arg == "--browser" {
			return true
		}
	}
	return false
}
