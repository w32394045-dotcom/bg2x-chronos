package engine

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// fallbackEngine is the always-available pure Go implementation: same tiling and
// feather blending contract as the C++ core, but with a Lanczos3/bilinear
// resampler instead of an AI model. It keeps the app useful on machines without
// the native library and gives `go test ./...` something real to run.
type fallbackEngine struct {
	cfg Config
}

func newFallbackEngine(cfg Config) Engine {
	return &fallbackEngine{cfg: cfg}
}

func (e *fallbackEngine) Info() Info {
	return Info{
		Backend:    "go-lanczos",
		EP:         "CPU",
		Device:     "CPU",
		Detail:     "pure Go fallback: tiled Lanczos3 (no AI model; build with the chronos_native tag for the C++ core)",
		ABIVersion: 0,
		Native:     false,
	}
}

type tileRect struct {
	X, Y, W, H int // in source pixels
}

func (t tileRect) OutW(scale int) int { return t.W * scale }
func (t tileRect) OutH(scale int) int { return t.H * scale }

func planTiles(width, height, tileSize, overlap int) []tileRect {
	if width <= 0 || height <= 0 {
		return nil
	}
	if tileSize <= 0 {
		tileSize = DefaultTile
	}
	if overlap <= 0 || overlap >= tileSize {
		overlap = DefaultOverlap
		if overlap >= tileSize {
			overlap = tileSize / 4
		}
	}
	xs := tilePositions(width, tileSize, overlap)
	ys := tilePositions(height, tileSize, overlap)
	tw, th := tileSize, tileSize
	if tw > width {
		tw = width
	}
	if th > height {
		th = height
	}
	tiles := make([]tileRect, 0, len(xs)*len(ys))
	for _, y := range ys {
		for _, x := range xs {
			tiles = append(tiles, tileRect{X: x, Y: y, W: tw, H: th})
		}
	}
	return tiles
}

func tilePositions(total, tileSize, overlap int) []int {
	if tileSize >= total {
		return []int{0}
	}
	step := tileSize - overlap
	if step <= 0 {
		step = tileSize / 2
	}
	if step <= 0 {
		step = 1
	}
	pos := make([]int, 0, total/step+2)
	for x := 0; x+tileSize < total; x += step {
		pos = append(pos, x)
	}
	pos = append(pos, total-tileSize)
	return pos
}

func (e *fallbackEngine) Process(ctx context.Context, req Request) (Result, error) {
	start := time.Now()
	if req.Input == "" || req.Output == "" {
		return Result{}, CodedError(CodeParam, "process", "input and output paths are required")
	}
	src, err := decodeImage(req.Input)
	if err != nil {
		return Result{}, CodedError(CodeIO, "decode", err.Error())
	}
	cfg := e.cfg
	scale := cfg.Scale
	if scale < 1 {
		scale = 1
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return Result{}, CodedError(CodeIO, "decode", "empty image")
	}
	tiles := planTiles(width, height, cfg.TileSize, cfg.Overlap)
	if len(tiles) == 0 {
		return Result{}, CodedError(CodeParam, "plan", "empty tile plan")
	}
	outW, outH := width*scale, height*scale
	acc := make([]float64, outW*outH*4)
	wsum := make([]float64, outW*outH)
	ramp := cfg.Overlap * scale
	total := len(tiles)

	for i, tile := range tiles {
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("%w: %v", ErrCancelled, err)
		}
		crop := cropClamped(src, tile)
		up := resampleNRGBA(crop, tile.OutW(scale), tile.OutH(scale), cfg.Fast)
		blendPatch(acc, wsum, outW, tile, up, scale, ramp)
		if req.OnProgress != nil {
			req.OnProgress(Progress{Done: i + 1, Total: total})
		}
	}

	out := image.NewNRGBA(image.Rect(0, 0, outW, outH))
	for i := 0; i < outW*outH; i++ {
		w := wsum[i]
		if w <= 0 {
			continue
		}
		for c := 0; c < 4; c++ {
			out.Pix[i*4+c] = clampByte(acc[i*4+c] / w)
		}
	}
	if err := writePNG(req.Output, out); err != nil {
		return Result{}, CodedError(CodeIO, "encode", err.Error())
	}
	elapsed := time.Since(start)
	return Result{
		Output:    req.Output,
		Width:     outW,
		Height:    outH,
		Tiles:     total,
		Backend:   "go-lanczos",
		Elapsed:   elapsed,
		ElapsedMs: elapsed.Milliseconds(),
	}, nil
}

func (e *fallbackEngine) Close() error { return nil }

func decodeImage(path string) (*image.NRGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst, nil
}

func writePNG(path string, img *image.NRGBA) error {
	if ext := strings.ToLower(filepath.Ext(path)); ext != "" && ext != ".png" {
		return fmt.Errorf("unsupported output format %q (png only)", ext)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func cropClamped(src *image.NRGBA, t tileRect) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, t.W, t.H))
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	for y := 0; y < t.H; y++ {
		sy := t.Y + y
		if sy < 0 {
			sy = 0
		} else if sy >= sh {
			sy = sh - 1
		}
		row := dst.Pix[y*dst.Stride : y*dst.Stride+t.W*4]
		srow := src.Pix[sy*src.Stride:]
		for x := 0; x < t.W; x++ {
			sx := t.X + x
			if sx < 0 {
				sx = 0
			} else if sx >= sw {
				sx = sw - 1
			}
			copy(row[x*4:x*4+4], srow[sx*4:sx*4+4])
		}
	}
	return dst
}

// blendPatch accumulates a resampled tile into the float canvas using a
// per-axis linear feather. Weights are normalized at the end, so constant
// images stay constant and no grid seams appear.
func blendPatch(acc, wsum []float64, outW int, t tileRect, patch *image.NRGBA, scale, ramp int) {
	pw, ph := patch.Bounds().Dx(), patch.Bounds().Dy()
	ox, oy := t.X*scale, t.Y*scale
	for y := 0; y < ph; y++ {
		oy2 := oy + y
		if oy2 < 0 || oy2 >= len(wsum)/outW {
			continue
		}
		wy := rampWeight(y, ph, ramp)
		for x := 0; x < pw; x++ {
			ox2 := ox + x
			if ox2 < 0 || ox2 >= outW {
				continue
			}
			w := wy * rampWeight(x, pw, ramp)
			pi := y*patch.Stride + x*4
			oi := oy2*outW + ox2
			wsum[oi] += w
			acc[oi*4+0] += float64(patch.Pix[pi+0]) * w
			acc[oi*4+1] += float64(patch.Pix[pi+1]) * w
			acc[oi*4+2] += float64(patch.Pix[pi+2]) * w
			acc[oi*4+3] += float64(patch.Pix[pi+3]) * w
		}
	}
}

func rampWeight(pos, size, ramp int) float64 {
	if ramp <= 0 || size <= 1 {
		return 1
	}
	d := pos + 1
	if size-pos < d {
		d = size - pos
	}
	w := float64(d) / float64(ramp)
	if w > 1 {
		w = 1
	}
	if w < 1e-3 {
		w = 1e-3
	}
	return w
}

type sampleWeight struct {
	idx int
	w   float64
}

func resampleNRGBA(src *image.NRGBA, dw, dh int, fast bool) *image.NRGBA {
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	if sw == 0 || sh == 0 {
		return image.NewNRGBA(image.Rect(0, 0, dw, dh))
	}
	if dw == sw && dh == sh {
		cp := image.NewNRGBA(image.Rect(0, 0, dw, dh))
		for y := 0; y < dh; y++ {
			copy(cp.Pix[y*cp.Stride:y*cp.Stride+dw*4], src.Pix[y*src.Stride:y*src.Stride+dw*4])
		}
		return cp
	}
	kernel, support := lanczos3, 3.0
	if fast {
		kernel, support = linearKernel, 1.0
	}

	// Horizontal pass -> float buffer.
	tmp := make([]float64, dw*sh*4)
	for y := 0; y < sh; y++ {
		for x := 0; x < dw; x++ {
			ws := sampleWeights(sw, dw, x, kernel, support)
			var r, g, b, a float64
			for _, s := range ws {
				p := y*src.Stride + s.idx*4
				r += float64(src.Pix[p+0]) * s.w
				g += float64(src.Pix[p+1]) * s.w
				b += float64(src.Pix[p+2]) * s.w
				a += float64(src.Pix[p+3]) * s.w
			}
			o := (y*dw + x) * 4
			tmp[o+0], tmp[o+1], tmp[o+2], tmp[o+3] = r, g, b, a
		}
	}

	// Vertical pass -> output image.
	out := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		ws := sampleWeights(sh, dh, y, kernel, support)
		for x := 0; x < dw; x++ {
			var r, g, b, a float64
			for _, s := range ws {
				o := (s.idx*dw + x) * 4
				r += tmp[o+0] * s.w
				g += tmp[o+1] * s.w
				b += tmp[o+2] * s.w
				a += tmp[o+3] * s.w
			}
			o := y*out.Stride + x*4
			out.Pix[o+0] = clampByte(r)
			out.Pix[o+1] = clampByte(g)
			out.Pix[o+2] = clampByte(b)
			out.Pix[o+3] = clampByte(a)
		}
	}
	return out
}

func sampleWeights(srcN, dstN, dstIdx int, kernel func(float64) float64, support float64) []sampleWeight {
	ratio := float64(srcN) / float64(dstN)
	f := ratio
	if f < 1 {
		f = 1
	}
	center := (float64(dstIdx)+0.5)*ratio - 0.5
	radius := support * f
	start := int(math.Floor(center - radius))
	end := int(math.Ceil(center + radius))
	if start < 0 {
		start = 0
	}
	if end > srcN-1 {
		end = srcN - 1
	}
	ws := make([]sampleWeight, 0, end-start+1)
	var sum float64
	for i := start; i <= end; i++ {
		w := kernel((float64(i) - center) / f)
		if w == 0 {
			continue
		}
		ws = append(ws, sampleWeight{idx: i, w: w})
		sum += w
	}
	if sum == 0 {
		idx := int(math.Round(center))
		if idx < 0 {
			idx = 0
		}
		if idx >= srcN {
			idx = srcN - 1
		}
		return []sampleWeight{{idx: idx, w: 1}}
	}
	for i := range ws {
		ws[i].w /= sum
	}
	return ws
}

func lanczos3(x float64) float64 {
	a := math.Abs(x)
	if a < 1e-8 {
		return 1
	}
	if a >= 3 {
		return 0
	}
	p := math.Pi * x
	return 3 * math.Sin(p) * math.Sin(p/3) / (p * p)
}

func linearKernel(x float64) float64 {
	a := math.Abs(x)
	if a >= 1 {
		return 0
	}
	return 1 - a
}

func clampByte(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}
