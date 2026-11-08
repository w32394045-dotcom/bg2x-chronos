package engine

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func writeTestPNG(t *testing.T, width, height int, paint func(x, y int) color.NRGBA) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, paint(x, y))
		}
	}
	path := filepath.Join(t.TempDir(), "in.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func readPNG(t *testing.T, path string) *image.NRGBA {
	t.Helper()
	img, err := decodeImage(path)
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return img
}

func TestPlanTilesCoverage(t *testing.T) {
	cases := []struct{ w, h, tile, overlap int }{
		{64, 48, 32, 8},
		{37, 23, 16, 4},
		{10, 10, 32, 8},
		{300, 200, 256, 16},
		{257, 257, 256, 16},
		{1, 1, 256, 16},
	}
	for _, c := range cases {
		tiles := planTiles(c.w, c.h, c.tile, c.overlap)
		if len(tiles) == 0 {
			t.Fatalf("%+v: no tiles", c)
		}
		covered := make([]int, c.w*c.h)
		for _, tr := range tiles {
			if tr.W <= 0 || tr.H <= 0 {
				t.Fatalf("%+v: bad tile %+v", c, tr)
			}
			if tr.X < 0 || tr.Y < 0 || tr.X+tr.W > c.w || tr.Y+tr.H > c.h {
				t.Fatalf("%+v: tile out of bounds %+v", c, tr)
			}
			for y := tr.Y; y < tr.Y+tr.H; y++ {
				for x := tr.X; x < tr.X+tr.W; x++ {
					covered[y*c.w+x]++
				}
			}
		}
		for i, n := range covered {
			if n < 1 {
				t.Fatalf("%+v: pixel %d uncovered", c, i)
			}
		}
	}
}

func TestFallbackDimensionsAndProgress(t *testing.T) {
	in := writeTestPNG(t, 37, 23, func(x, y int) color.NRGBA {
		return color.NRGBA{R: uint8(x * 5), G: uint8(y * 7), B: 128, A: 255}
	})
	eng := newFallbackEngine(Config{Scale: 3, TileSize: 16, Overlap: 4})
	if eng.Info().Backend != "go-lanczos" || eng.Info().Native {
		t.Fatalf("unexpected info: %+v", eng.Info())
	}
	var progress []Progress
	out := filepath.Join(t.TempDir(), "out.png")
	res, err := eng.Process(context.Background(), Request{Input: in, Output: out, OnProgress: func(p Progress) {
		progress = append(progress, p)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Width != 111 || res.Height != 69 {
		t.Fatalf("dims = %dx%d, want 111x69", res.Width, res.Height)
	}
	if res.Tiles != len(progress) {
		t.Fatalf("tiles = %d, progress calls = %d", res.Tiles, len(progress))
	}
	last := progress[len(progress)-1]
	if last.Done != last.Total {
		t.Fatalf("final progress = %+v", last)
	}
	got := readPNG(t, out)
	if got.Bounds().Dx() != 111 || got.Bounds().Dy() != 69 {
		t.Fatalf("output image = %v", got.Bounds())
	}
}

func TestFallbackConstantImageHasNoSeams(t *testing.T) {
	const level = 77
	in := writeTestPNG(t, 40, 30, func(x, y int) color.NRGBA {
		return color.NRGBA{R: level, G: level, B: level, A: 255}
	})
	out := filepath.Join(t.TempDir(), "out.png")
	eng := newFallbackEngine(Config{Scale: 2, TileSize: 16, Overlap: 4})
	if _, err := eng.Process(context.Background(), Request{Input: in, Output: out}); err != nil {
		t.Fatal(err)
	}
	img := readPNG(t, out)
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if r>>8 != level || g>>8 != level || b>>8 != level || a>>8 != 255 {
				t.Fatalf("pixel (%d,%d) = %d,%d,%d,%d; seam/blend bug", x, y, r>>8, g>>8, b>>8, a>>8)
			}
		}
	}
}

func TestFallbackCancellation(t *testing.T) {
	in := writeTestPNG(t, 64, 64, func(x, y int) color.NRGBA { return color.NRGBA{R: 1, A: 255} })
	eng := newFallbackEngine(Config{Scale: 2, TileSize: 8, Overlap: 2})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := eng.Process(ctx, Request{Input: in, Output: filepath.Join(t.TempDir(), "o.png")})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	if !isSentinel(err, ErrCancelled) {
		t.Fatalf("error = %v, want ErrCancelled", err)
	}
}

func TestFallbackBadInput(t *testing.T) {
	eng := newFallbackEngine(Config{Scale: 2})
	_, err := eng.Process(context.Background(), Request{Input: filepath.Join(t.TempDir(), "missing.png"), Output: filepath.Join(t.TempDir(), "o.png")})
	if err == nil || !isSentinel(err, ErrIO) {
		t.Fatalf("error = %v, want ErrIO", err)
	}
}

func TestProviderFallbackAndModelRequirement(t *testing.T) {
	p, err := NewProvider(Config{Scale: 2}, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	e, err := p.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	backend := e.Info().Backend
	p.Release(e)
	if p.Info().Native {
		// Built with `-tags chronos_native`: the native stub backend is expected.
		if backend != "stub" {
			t.Fatalf("native backend = %q, want stub", backend)
		}
	} else if backend != "go-lanczos" {
		t.Fatalf("fallback backend = %q, want go-lanczos", backend)
	}

	// A model needs the native core; without it this must be a clear error.
	if _, err := NewProvider(Config{ModelPath: "/models/x.onnx", Scale: 4}, 1); err == nil {
		t.Fatal("expected error for model without native core")
	}
}

func TestCodedErrorUnwrap(t *testing.T) {
	cases := []struct {
		code int
		want error
	}{
		{CodeIO, ErrIO},
		{CodeModel, ErrModel},
		{CodeInfer, ErrInfer},
		{CodeParam, ErrParam},
		{CodeBackend, ErrBackend},
		{CodeCancelled, ErrCancelled},
		{CodeUnknown, ErrUnknown},
	}
	for _, c := range cases {
		err := CodedError(c.code, "op", "msg")
		if !isSentinel(err, c.want) {
			t.Fatalf("code %d does not unwrap to %v", c.code, c.want)
		}
	}
}

func isSentinel(err error, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

var _ = fmt.Sprintf
