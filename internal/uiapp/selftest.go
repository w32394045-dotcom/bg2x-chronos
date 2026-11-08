package uiapp

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/engine"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/models"
)

// selfTestSize is the synthetic image edge length used by the self test.
const selfTestSize = 64

// SelfTestModel runs the "能用" checks for one installed model and persists the
// result as a marker file next to the weights (models.VerifiedMarker / FailedMarker).
// mark controls whether the marker is written (runPull passes true; a manual
// UI run also passes true).
func (c *Core) SelfTestModel(modelID string, mark bool) ModelSelfTest {
	res := ModelSelfTest{}
	add := func(name string, ok bool, detail string) {
		res.Checks = append(res.Checks, SelfTestCheck{Name: name, OK: ok, Detail: detail})
		if !ok {
			res.OK = false
		}
	}
	res.OK = true
	fail := func(name, detail string) ModelSelfTest {
		add(name, false, detail)
		res.OK = false
		res.Diagnostics = c.selfTestDiagnostics(modelID, res)
		if mark {
			c.markSelfTest(modelID, res)
		}
		return res
	}

	store := c.modelStore()
	if store == nil {
		return fail("load", "模型目录不可用")
	}
	path, installed, err := store.Resolve(modelID)
	if err != nil {
		return fail("load", err.Error())
	}
	if !installed {
		return fail("load", "模型未安装")
	}
	scale := c.modelScale(modelID)

	tmp, err := os.MkdirTemp("", "chronos-selftest-*")
	if err != nil {
		return fail("load", "无法创建临时目录: "+err.Error())
	}
	defer os.RemoveAll(tmp)
	inputPath := filepath.Join(tmp, "input.png")
	if err := writeTestImage(inputPath, selfTestSize); err != nil {
		return fail("load", "无法生成测试图: "+err.Error())
	}

	runOnce := func(tile int) (string, engine.Info, int64, error) {
		cfg := engine.Config{
			ModelPath: path,
			Scale:     scale,
			TileSize:  tile,
			Overlap:   defaultOverlap(tile),
			Threads:   2,
		}
		provider, err := engine.NewProvider(cfg, 1)
		if err != nil {
			return "", engine.Info{}, 0, err
		}
		defer provider.Close()
		eng, err := provider.Acquire(context.Background())
		if err != nil {
			return "", provider.Info(), 0, err
		}
		defer provider.Release(eng)
		out := filepath.Join(tmp, fmt.Sprintf("out-%d.png", tile))
		start := time.Now()
		_, perr := eng.Process(context.Background(), engine.Request{Input: inputPath, Output: out})
		elapsed := time.Since(start).Milliseconds()
		if perr != nil {
			return "", provider.Info(), elapsed, perr
		}
		return out, provider.Info(), elapsed, nil
	}

	out1, info, elapsed, err := runOnce(256)
	if err != nil {
		return fail("load", "加载推理失败: "+err.Error())
	}
	add("load", true, fmt.Sprintf("后端 %s · %s · %s", info.Backend, info.Device, info.Detail))

	// Second run with a different tile size: exercises tiling + blending and
	// gives the seam check a reference (fixed-size models repeat the same tile).
	tile2 := 512
	if t := info.Detail; strings.Contains(t, "fixed") {
		tile2 = 256
	}
	out2, _, _, err2 := runOnce(tile2)
	if err2 != nil {
		add("seam", false, "第二次推理失败: "+err2.Error())
		res.OK = false
	} else if tile2 == 256 {
		add("seam", true, "固定尺寸模型，跳过拼接对比")
	}

	img1, err := readPNG(out1)
	if err != nil {
		return fail("size", "输出不可读: "+err.Error())
	}
	want := selfTestSize * scale
	add("size", img1.Bounds().Dx() == want && img1.Bounds().Dy() == want,
		fmt.Sprintf("输出 %dx%d, 期望 %dx%d", img1.Bounds().Dx(), img1.Bounds().Dy(), want, want))

	mean, std := imageStats(img1)
	add("color", std > 1.0, fmt.Sprintf("均值 %.1f, 标准差 %.2f", mean, std))

	if err2 == nil && tile2 != 256 {
		img2, err := readPNG(out2)
		if err != nil {
			add("seam", false, "第二张输出不可读: "+err.Error())
			res.OK = false
		} else {
			maxDiff := maxPixelDiff(img1, img2)
			add("seam", maxDiff <= 2, fmt.Sprintf("tile 256 vs %d: 最大像素差 %d LSB", tile2, maxDiff))
		}
	}

	add("perf", true, fmt.Sprintf("%d ms/图 (%dx%d 输入)", elapsed, selfTestSize, selfTestSize))

	res.Diagnostics = c.selfTestDiagnostics(modelID, res)
	if mark {
		c.markSelfTest(modelID, res)
	}
	return res
}

// modelScale returns the effective scale for the model (sidecar first, then 4).
func (c *Core) modelScale(modelID string) int {
	store := c.modelStore()
	if store == nil {
		return 4
	}
	path, installed, err := store.Resolve(modelID)
	if err != nil || !installed {
		return 4
	}
	if sc, err := models.ParseSidecar(filepath.Join(filepath.Dir(path), "model.json")); err == nil {
		if len(sc.Outputs) > 0 && sc.Outputs[0].Scale > 0 {
			return sc.Outputs[0].Scale
		}
	}
	return 4
}

func defaultOverlap(tile int) int {
	if tile <= 16 {
		return 4
	}
	return 16
}

func (c *Core) markSelfTest(modelID string, res ModelSelfTest) {
	store := c.modelStore()
	if store == nil {
		return
	}
	dir := filepath.Join(store.Dir(), modelID)
	if _, err := os.Stat(dir); err != nil {
		return
	}
	verified := filepath.Join(dir, models.VerifiedMarker)
	failed := filepath.Join(dir, models.FailedMarker)
	if res.OK {
		_ = os.Remove(failed)
		_ = os.WriteFile(verified, []byte(time.Now().Format(time.RFC3339)+"\n"), 0o644)
		return
	}
	_ = os.Remove(verified)
	reason := "自检失败"
	for _, chk := range res.Checks {
		if !chk.OK {
			reason = chk.Name + ": " + chk.Detail
			break
		}
	}
	_ = os.WriteFile(failed, []byte(reason+"\n"), 0o644)
}

func (c *Core) selfTestDiagnostics(modelID string, res ModelSelfTest) string {
	var b strings.Builder
	store := c.modelStore()
	backend := "unknown"
	if c.engineInfo.Backend != "" {
		backend = c.engineInfo.Backend
	}
	fmt.Fprintf(&b, "Chronos model self-test\n")
	fmt.Fprintf(&b, "model: %s\n", modelID)
	if store != nil {
		if path, installed, err := store.Resolve(modelID); err == nil && installed {
			fmt.Fprintf(&b, "path: %s\n", path)
			if st, err := os.Stat(path); err == nil {
				fmt.Fprintf(&b, "size: %.1f MB\n", float64(st.Size())/(1024*1024))
			}
			if data, err := os.ReadFile(filepath.Join(filepath.Dir(path), "model.json")); err == nil {
				fmt.Fprintf(&b, "sidecar: %s\n", strings.Join(strings.Fields(string(data)), " "))
			}
		} else if err != nil {
			fmt.Fprintf(&b, "resolve error: %v\n", err)
		}
	}
	fmt.Fprintf(&b, "startup backend: %s\n", backend)
	fmt.Fprintf(&b, "ok: %v\n", res.OK)
	for _, chk := range res.Checks {
		status := "PASS"
		if !chk.OK {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "  [%s] %s: %s\n", status, chk.Name, chk.Detail)
	}
	return b.String()
}

// writeTestImage writes a deterministic 64x64 RGB gradient-with-noise PNG.
func writeTestImage(path string, size int) error {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			r := uint8(x * 255 / (size - 1))
			g := uint8(y * 255 / (size - 1))
			b := uint8((x*7 + y*13) % 251)
			img.Set(x, y, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

func imageStats(img image.Image) (mean, std float64) {
	b := img.Bounds()
	n := float64(b.Dx() * b.Dy() * 3)
	if n == 0 {
		return 0, 0
	}
	var sum, sumSq float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			for _, v := range []uint32{r, g, bl} {
				f := float64(v >> 8)
				sum += f
				sumSq += f * f
			}
		}
	}
	mean = sum / n
	variance := sumSq/n - mean*mean
	if variance < 0 {
		variance = 0
	}
	std = math.Sqrt(variance)
	return mean, std
}

func maxPixelDiff(a, b image.Image) int {
	ab, bb := a.Bounds(), b.Bounds()
	if ab.Dx() != bb.Dx() || ab.Dy() != bb.Dy() {
		return 255
	}
	maxDiff := 0
	for y := 0; y < ab.Dy(); y++ {
		for x := 0; x < ab.Dx(); x++ {
			ar, ag, abl, _ := a.At(ab.Min.X+x, ab.Min.Y+y).RGBA()
			br, bg, bbl, _ := b.At(bb.Min.X+x, bb.Min.Y+y).RGBA()
			for _, pair := range [][2]uint32{{ar, br}, {ag, bg}, {abl, bbl}} {
				d := int(pair[0]>>8) - int(pair[1]>>8)
				if d < 0 {
					d = -d
				}
				if d > maxDiff {
					maxDiff = d
				}
			}
		}
	}
	return maxDiff
}
