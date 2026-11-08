//go:build cgo && chronos_native

package engine

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

// These tests only exist when the native core is linked; they verify the C ABI
// path end-to-end (image in -> tiling -> inference stub -> PNG out).
func TestNativeStubProcessEndToEnd(t *testing.T) {
	in := writeTestPNG(t, 40, 30, func(x, y int) color.NRGBA {
		return color.NRGBA{R: uint8(x * 4), G: uint8(y * 6), B: 90, A: 255}
	})
	p, err := NewProvider(Config{Scale: 2, TileSize: 16, Overlap: 4}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !p.Info().Native {
		t.Fatalf("expected native core, got %+v", p.Info())
	}
	e, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release(e)

	out := filepath.Join(t.TempDir(), "out.png")
	var progress []Progress
	res, err := e.Process(context.Background(), Request{
		Input:  in,
		Output: out,
		OnProgress: func(pg Progress) {
			progress = append(progress, pg)
		},
	})
	if err != nil {
		t.Fatalf("native process: %v", err)
	}
	if res.Width != 80 || res.Height != 60 {
		t.Fatalf("dims = %dx%d, want 80x60", res.Width, res.Height)
	}
	if res.Tiles < 2 {
		t.Fatalf("tiles = %d, expected a multi-tile plan", res.Tiles)
	}
	// Progress is polled every 50ms; a tiny image may finish before the first
	// tick, so only sanity check the values that did arrive.
	for _, pg := range progress {
		if pg.Total <= 0 || pg.Done < 0 || pg.Done > pg.Total {
			t.Fatalf("invalid progress %+v", pg)
		}
	}
	img, err := decodeImage(out)
	if err != nil {
		t.Fatalf("decode native output: %v", err)
	}
	if img.Bounds().Dx() != 80 || img.Bounds().Dy() != 60 {
		t.Fatalf("output image bounds = %v", img.Bounds())
	}
	if st, err := os.Stat(out); err != nil || st.Size() == 0 {
		t.Fatalf("output file missing: %v", err)
	}
}

func TestNativeConstantImageNoSeam(t *testing.T) {
	const level = 123
	in := writeTestPNG(t, 48, 34, func(x, y int) color.NRGBA {
		return color.NRGBA{R: level, G: level, B: level, A: 255}
	})
	p, err := NewProvider(Config{Scale: 2, TileSize: 16, Overlap: 4}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	e, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release(e)
	out := filepath.Join(t.TempDir(), "const.png")
	if _, err := e.Process(context.Background(), Request{Input: in, Output: out}); err != nil {
		t.Fatal(err)
	}
	img, err := decodeImage(out)
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if r>>8 != level || g>>8 != level || b>>8 != level || a>>8 != 255 {
				t.Fatalf("seam at (%d,%d): %d,%d,%d,%d", x, y, r>>8, g>>8, b>>8, a>>8)
			}
		}
	}
}

func TestNativeInvalidInputReturnsCode(t *testing.T) {
	p, err := NewProvider(Config{Scale: 2}, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	e, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Release(e)
	_, err = e.Process(context.Background(), Request{
		Input:  filepath.Join(t.TempDir(), "missing.png"),
		Output: filepath.Join(t.TempDir(), "out.png"),
	})
	if err == nil || !isSentinel(err, ErrIO) {
		t.Fatalf("error = %v, want ErrIO", err)
	}
}
