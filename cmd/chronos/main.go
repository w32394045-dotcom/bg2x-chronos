// Command chronos is the headless companion to the desktop app. It shares the
// exact same engine/scheduler configuration so CI can verify the pipeline on a
// machine without a webview.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/config"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/engine"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/inhibit"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/models"
)

// Set with -ldflags "-X main.version=... -X main.commit=... -X main.buildTime=..."
var (
	version   = "0.1.0"
	commit    = "dev"
	buildTime = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "upscale":
		err = runUpscale(ctx, os.Args[2:])
	case "info":
		err = runInfo(os.Args[2:])
	case "gen":
		err = runGen(os.Args[2:])
	case "models":
		err = runModels(ctx, os.Args[2:])
	case "inhibit":
		err = runInhibit(ctx, os.Args[2:])
	case "bench":
		err = runBench(ctx, os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("chronos %s (%s/%s, commit %s, built %s)\n", version, runtime.GOOS, runtime.GOARCH, commit, buildTime)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "cancelled")
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Chronos %s — native AI image super-resolution (headless CLI)

Usage:
  chronos upscale [flags] <input...>     upscale images
  chronos info [--json]                  engine/platform diagnostics
  chronos gen -o out.png                 generate a deterministic test image
  chronos models <list|pull|path|import|dir>
  chronos inhibit <on|off|status> [flags] sleep/display/lock inhibition
  chronos bench [flags]                  throughput micro-benchmark
  chronos version

Upscale flags:
  -i, --input <file>     input image (repeatable / comma separated)
  -o, --output <path>    output file or directory (default: next to input)
  -s, --scale <n>        output scale (default: model scale, else 4)
  -m, --model <file>     local .onnx/.xml model
      --model-id <id>    registered model id (downloads if missing)
      --no-model         force classic (Lanczos) resampling
      --tile <n>         tile size (default 256)
      --overlap <n>      tile overlap (default 16)
      --threads <n>      inference threads (default: auto)
      --fast             bilinear resample (stub backend only)
      --json             machine readable result lines
`, version)
}

// ---- upscale ---------------------------------------------------------------

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

func runUpscale(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("upscale", flag.ContinueOnError)
	var inputs stringList
	fs.Var(&inputs, "i", "input image (repeatable / comma separated)")
	fs.Var(&inputs, "input", "alias for -i")
	out := fs.String("o", "", "output file or directory")
	scale := fs.Int("s", 0, "output scale")
	scaleLong := fs.Int("scale", 0, "output scale (alias)")
	modelFile := fs.String("m", "", "local model file")
	modelFileLong := fs.String("model", "", "local model file (alias)")
	modelID := fs.String("model-id", "", "registered model id")
	tile := fs.Int("tile", 0, "tile size")
	overlap := fs.Int("overlap", 0, "tile overlap")
	threads := fs.Int("threads", 0, "inference threads")
	fast := fs.Bool("fast", false, "bilinear resample")
	noModel := fs.Bool("no-model", false, "classic resampling only")
	jsonOut := fs.Bool("json", false, "json output lines")
	if err := fs.Parse(args); err != nil {
		return err
	}
	inputs = append(inputs, fs.Args()...)
	if len(inputs) == 0 {
		return fmt.Errorf("no input images given")
	}

	modelPath := firstNonEmpty(*modelFile, *modelFileLong)
	scaleValue := firstNonZero(*scale, *scaleLong)
	modelEntry, haveEntry := models.Entry{}, false
	if modelPath == "" && *modelID != "" {
		store, err := models.NewStore("", nil)
		if err != nil {
			return err
		}
		e, ok := store.Registry().Lookup(*modelID)
		if !ok {
			return fmt.Errorf("unknown model id %q", *modelID)
		}
		modelEntry, haveEntry = e, true
		path, installed, err := store.Resolve(*modelID)
		if err != nil {
			return err
		}
		if !installed {
			fmt.Fprintf(os.Stderr, "downloading model %s (%.1f MB)...\n", e.ID, e.SizeMB)
			path, err = store.Pull(ctx, *modelID, func(done, total int64) {
				if total > 0 {
					fmt.Fprintf(os.Stderr, "\r  %5.1f%% %d/%d MB", float64(done)*100/float64(total), done>>20, total>>20)
				}
			})
			fmt.Fprintln(os.Stderr)
			if err != nil {
				return err
			}
		}
		modelPath = path
	}
	if *noModel {
		modelPath = ""
	}
	if scaleValue <= 0 {
		if haveEntry {
			scaleValue = modelEntry.Scale
		} else {
			scaleValue = engine.DefaultScale
		}
	}

	provider, err := engine.NewProvider(engine.Config{
		ModelPath: modelPath,
		Scale:     scaleValue,
		TileSize:  *tile,
		Overlap:   *overlap,
		Threads:   *threads,
		Fast:      *fast,
	}, 1)
	if err != nil {
		return err
	}
	defer provider.Close()
	eng, err := provider.Acquire(ctx)
	if err != nil {
		return err
	}
	defer provider.Release(eng)

	var errs []string
	for _, input := range inputs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		outPath, err := resolveOutput(*out, input, scaleValue, len(inputs) > 1)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if dir := filepath.Dir(outPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				errs = append(errs, err.Error())
				continue
			}
		}
		fmt.Fprintf(os.Stderr, "processing %s -> %s\n", input, outPath)
		res, err := eng.Process(ctx, engine.Request{
			Input:  input,
			Output: outPath,
			OnProgress: func(p engine.Progress) {
				fmt.Fprintf(os.Stderr, "\r  tiles %d/%d", p.Done, p.Total)
			},
		})
		fmt.Fprintln(os.Stderr)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", input, err))
			continue
		}
		if *jsonOut {
			payload, _ := json.Marshal(map[string]any{
				"input": input, "output": res.Output, "width": res.Width, "height": res.Height,
				"tiles": res.Tiles, "backend": res.Backend, "elapsedMs": res.ElapsedMs,
			})
			fmt.Println(string(payload))
		} else {
			fmt.Printf("OK %s %dx%d tiles=%d %dms backend=%s\n", res.Output, res.Width, res.Height, res.Tiles, res.ElapsedMs, res.Backend)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d image(s) failed: %s", len(errs), strings.Join(errs, " | "))
	}
	return nil
}

func resolveOutput(out, input string, scale int, multiple bool) (string, error) {
	if out == "" {
		base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
		return filepath.Join(filepath.Dir(input), fmt.Sprintf("%s_chronos_x%d.png", base, scale)), nil
	}
	if st, err := os.Stat(out); err == nil && st.IsDir() {
		base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
		return filepath.Join(out, fmt.Sprintf("%s_chronos_x%d.png", base, scale)), nil
	}
	if multiple {
		return "", fmt.Errorf("output %q must be an existing directory when multiple inputs are given", out)
	}
	ext := strings.ToLower(filepath.Ext(out))
	if ext == "" {
		out += ".png"
	} else if ext != ".png" {
		return "", fmt.Errorf("output must be .png (native core writes PNG), got %q", ext)
	}
	return out, nil
}

// ---- info / gen -------------------------------------------------------------

func runInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "json output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	probe, err := engine.NewProvider(engine.Config{Scale: 2}, 1)
	if err != nil {
		return err
	}
	info := probe.Info()
	probe.Close()
	dataDir, _ := config.DataDir()
	modelDir, _ := models.DefaultDir()
	reg, _ := models.Load()
	modelCount := 0
	if reg != nil {
		modelCount = len(reg.Models)
	}
	payload := map[string]any{
		"version":        version,
		"commit":         commit,
		"buildTime":      buildTime,
		"platform":       runtime.GOOS + "/" + runtime.GOARCH,
		"goVersion":      runtime.Version(),
		"engine":         info,
		"dataDir":        dataDir,
		"modelDir":       modelDir,
		"modelCount":     modelCount,
		"maxConcurrency": engine.MaxConcurrency,
		"nativeLoaded":   info.Native,
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(payload)
	}
	fmt.Printf("chronos %s (%s, commit %s, built %s)\n", version, runtime.GOOS+"/"+runtime.GOARCH, commit, buildTime)
	fmt.Printf("go:      %s\n", runtime.Version())
	fmt.Printf("engine:  %s device=%s native=%v abi=%d\n", info.Backend, info.Device, info.Native, info.ABIVersion)
	fmt.Printf("detail:  %s\n", info.Detail)
	fmt.Printf("data:    %s\n", dataDir)
	fmt.Printf("models:  %s (%d in registry)\n", modelDir, modelCount)
	fmt.Printf("limits:  max concurrency %d, tile %d, overlap %d, scale 1..%d\n", engine.MaxConcurrency, engine.DefaultTile, engine.DefaultOverlap, engine.MaxScale)
	return nil
}

func runGen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ContinueOnError)
	out := fs.String("o", "gen.png", "output path")
	w := fs.Int("w", 64, "width")
	h := fs.Int("h", 48, "height")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *w <= 0 || *h <= 0 || *w > 16384 || *h > 16384 {
		return fmt.Errorf("invalid dimensions %dx%d", *w, *h)
	}
	img := image.NewNRGBA(image.Rect(0, 0, *w, *h))
	for y := 0; y < *h; y++ {
		for x := 0; x < *w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 255 / max(1, *w-1)),
				G: uint8(y * 255 / max(1, *h-1)),
				B: uint8((x*y + 7) % 256),
				A: 255,
			})
		}
	}
	if err := os.MkdirAll(filepath.Dir(absOrDot(*out)), 0o755); err != nil {
		return err
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%dx%d)\n", *out, *w, *h)
	return nil
}

func absOrDot(path string) string {
	if filepath.Dir(path) == "" {
		return "."
	}
	return path
}

// ---- models -----------------------------------------------------------------

func runModels(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: chronos models <list|pull|path|import|dir>")
	}
	store, err := models.NewStore("", nil)
	if err != nil {
		return err
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "list":
		fs := flag.NewFlagSet("models list", flag.ContinueOnError)
		jsonOut := fs.Bool("json", false, "json output")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		list := store.List()
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(list)
		}
		fmt.Printf("%-32s %-6s %-6s %-10s %-7s %s\n", "ID", "SCALE", "KIND", "SIZE", "INSTALL", "NAME")
		for _, m := range list {
			installed := "-"
			if m.Installed {
				installed = "yes"
			}
			fmt.Printf("%-32s %-6d %-6s %-10s %-7s %s\n", m.ID, m.Scale, m.Kind, fmt.Sprintf("%.1fMB", m.SizeMB), installed, m.Name)
		}
		return nil
	case "pull":
		if len(rest) < 1 {
			return fmt.Errorf("usage: chronos models pull <id>")
		}
		path, err := store.Pull(ctx, rest[0], func(done, total int64) {
			if total > 0 {
				fmt.Fprintf(os.Stderr, "\r  %5.1f%% %d/%d MB", float64(done)*100/float64(total), done>>20, total>>20)
			}
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Println(path)
		return nil
	case "path":
		if len(rest) < 1 {
			return fmt.Errorf("usage: chronos models path <id>")
		}
		path, installed, err := store.Resolve(rest[0])
		if err != nil {
			return err
		}
		fmt.Printf("%s installed=%v\n", path, installed)
		return nil
	case "import":
		if len(rest) < 2 {
			return fmt.Errorf("usage: chronos models import <id> <file>")
		}
		path, err := store.Import(rest[0], rest[1])
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	case "dir":
		fmt.Println(store.Dir())
		return nil
	default:
		return fmt.Errorf("unknown models subcommand %q", sub)
	}
}

// ---- inhibit ----------------------------------------------------------------

func runInhibit(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: chronos inhibit <on|off|status> [--sleep --display --lock]")
	}
	sub := args[0]
	fs := flag.NewFlagSet("inhibit "+sub, flag.ContinueOnError)
	sleep := fs.Bool("sleep", false, "keep system awake")
	display := fs.Bool("display", false, "keep display on")
	lock := fs.Bool("lock", false, "suppress idle screen lock")
	autoRelease := fs.Bool("auto-release", false, "release automatically (app semantics)")
	duration := fs.Duration("duration", 0, "auto release after this duration (on only)")
	reason := fs.String("reason", "Chronos CLI", "human readable reason")
	jsonOut := fs.Bool("json", false, "json status output")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	m := inhibit.New()
	defer m.Close()

	switch sub {
	case "status":
		st := m.Status()
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(st)
		}
		fmt.Printf("active=%v sleep=%v display=%v lock=%v\n", st.Active, st.Sleep, st.Display, st.Lock)
		fmt.Printf("supported: sleep=%v display=%v lock=%v driver=%s\n", st.SleepSupported, st.DisplaySupported, st.LockSupported, st.Driver)
		fmt.Printf("detail: %s\n", st.Detail)
		return nil
	case "off":
		if _, err := m.Set(inhibit.Options{}); err != nil {
			return err
		}
		fmt.Println("inhibition released")
		return nil
	case "on":
		if !*sleep && !*display && !*lock {
			*sleep, *display = true, true
		}
		st, err := m.Set(inhibit.Options{Sleep: *sleep, Display: *display, Lock: *lock, AutoRelease: *autoRelease, Reason: *reason})
		if err != nil {
			return err
		}
		if *jsonOut {
			if err := json.NewEncoder(os.Stdout).Encode(st); err != nil {
				return err
			}
		} else {
			fmt.Printf("active=%v sleep=%v display=%v lock=%v driver=%s\n", st.Active, st.Sleep, st.Display, st.Lock, st.Driver)
			fmt.Printf("detail: %s\n", st.Detail)
			fmt.Println("holding; press Ctrl+C to release")
		}
		if *duration > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(*duration):
			}
		} else {
			<-ctx.Done()
		}
		return nil
	default:
		return fmt.Errorf("unknown inhibit subcommand %q", sub)
	}
}

// ---- bench ------------------------------------------------------------------

func runBench(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	size := fs.Int("size", 512, "square image size")
	iters := fs.Int("iters", 3, "iterations")
	scale := fs.Int("s", 2, "scale")
	modelPath := fs.String("m", "", "model file")
	tile := fs.Int("tile", 256, "tile size")
	overlap := fs.Int("overlap", 16, "overlap")
	threads := fs.Int("threads", 0, "threads")
	fast := fs.Bool("fast", false, "bilinear")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *size <= 0 || *iters <= 0 {
		return fmt.Errorf("invalid size/iterations")
	}
	dir, err := os.MkdirTemp("", "chronos-bench-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	in := filepath.Join(dir, "in.png")
	if err := runGen([]string{"-o", in, "-w", fmt.Sprint(*size), "-h", fmt.Sprint(*size)}); err != nil {
		return err
	}
	provider, err := engine.NewProvider(engine.Config{
		ModelPath: *modelPath, Scale: *scale, TileSize: *tile, Overlap: *overlap, Threads: *threads, Fast: *fast,
	}, 1)
	if err != nil {
		return err
	}
	defer provider.Close()
	eng, err := provider.Acquire(ctx)
	if err != nil {
		return err
	}
	defer provider.Release(eng)

	fmt.Printf("backend=%s size=%d scale=%d tile=%d overlap=%d\n", provider.Info().Backend, *size, *scale, *tile, *overlap)
	var total time.Duration
	for i := 0; i < *iters; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		out := filepath.Join(dir, fmt.Sprintf("out-%d.png", i))
		res, err := eng.Process(ctx, engine.Request{Input: in, Output: out})
		if err != nil {
			return err
		}
		total += res.Elapsed
		fmt.Printf("iter %d: %dms tiles=%d -> %dx%d\n", i+1, res.ElapsedMs, res.Tiles, res.Width, res.Height)
	}
	fmt.Printf("average: %dms\n", (total / time.Duration(*iters)).Milliseconds())
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstNonZero(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
