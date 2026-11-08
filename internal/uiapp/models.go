package uiapp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/models"
)

// Pull phases: docs/CONTRACTS.md v3, ModelPullState.phase.
const (
	PullQueued      = "queued"
	PullResolving   = "resolving"
	PullDownloading = "downloading"
	PullVerifying   = "verifying"
	PullInstalling  = "installing"
	PullSelfTest    = "selftest"
	PullDone        = "done"
	PullFailed      = "failed"
	PullCancelled   = "cancelled"
)

// PullModelRequest starts a model download (contract v3 §3.1).
type PullModelRequest struct {
	ModelID string `json:"modelId"`
	Mirror  string `json:"mirror"`
}

// ModelPullState is the payload of the chronos:model event and the PullModel result.
type ModelPullState struct {
	ModelID string `json:"modelId"`
	Phase   string `json:"phase"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total"` // -1 when the server did not send a length
	Error   string `json:"error"`
}

// ImportModelRequest imports a user supplied model (contract v3 §3.1).
type ImportModelRequest struct {
	Path       string `json:"path"` // a single .onnx, or a directory containing .onnx (+ .data)
	Name       string `json:"name"`
	AutoDetect bool   `json:"autoDetect"`
}

// TensorShape is one model input/output tensor description.
type TensorShape struct {
	Name  string `json:"name"`
	Dims  []int  `json:"dims"` // -1 = dynamic
	Dtype string `json:"dtype"`
}

// ModelDiagnostics is the copy-pasteable model report (contract v3 §3.1).
type ModelDiagnostics struct {
	ModelID  string        `json:"modelId"`
	Opset    int           `json:"opset"`
	Ops      []string      `json:"ops"`
	Inputs   []TensorShape `json:"inputs"`
	Outputs  []TensorShape `json:"outputs"`
	Warnings []string      `json:"warnings"`
	Error    string        `json:"error"`
}

// SelfTestCheck is one named verification step.
type SelfTestCheck struct {
	Name   string `json:"name"` // import|shape|size|seam|color|perf
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// ModelSelfTest is the result of the post-install / on-demand self test.
type ModelSelfTest struct {
	OK          bool            `json:"ok"`
	Checks      []SelfTestCheck `json:"checks"`
	Diagnostics string          `json:"diagnostics"`
}

// ---- pull bookkeeping -------------------------------------------------------

type pullJob struct {
	core    *Core
	modelID string
	cancel  context.CancelFunc

	mu       sync.Mutex
	st       ModelPullState
	lastEmit time.Time
}

func (j *pullJob) snapshot() ModelPullState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.st
}

func (j *pullJob) setPhase(phase string) {
	j.mu.Lock()
	j.st.Phase = phase
	j.mu.Unlock()
	j.publish(false)
}

func (j *pullJob) setProgress(done, total int64) {
	j.mu.Lock()
	j.st.Done = done
	j.st.Total = total
	j.mu.Unlock()
	j.publish(false)
}

func (j *pullJob) fail(err error) {
	j.mu.Lock()
	if errors.Is(err, context.Canceled) {
		j.st.Phase = PullCancelled
	} else {
		j.st.Phase = PullFailed
		j.st.Error = err.Error()
	}
	j.mu.Unlock()
	j.publish(true)
}

// publish emits at most 5 chronos:model events per second unless forced.
func (j *pullJob) publish(force bool) {
	j.mu.Lock()
	if !force && time.Since(j.lastEmit) < 200*time.Millisecond {
		j.mu.Unlock()
		return
	}
	j.lastEmit = time.Now()
	st := j.st
	j.mu.Unlock()
	j.core.emit("chronos:model", st)
}

func (c *Core) registerPull(job *pullJob) {
	c.pullMu.Lock()
	if c.pulls == nil {
		c.pulls = make(map[string]*pullJob)
	}
	c.pulls[job.modelID] = job
	c.pullMu.Unlock()
}

func (c *Core) unregisterPull(modelID string) {
	c.pullMu.Lock()
	delete(c.pulls, modelID)
	c.pullMu.Unlock()
}

func (c *Core) activePull(modelID string) *pullJob {
	c.pullMu.Lock()
	defer c.pullMu.Unlock()
	return c.pulls[modelID]
}

// ---- bindings ---------------------------------------------------------------

// PullModel starts (or resumes) a registry model download and returns immediately.
func (c *Core) PullModel(req PullModelRequest) (ModelPullState, error) {
	store := c.modelStore()
	if store == nil {
		return ModelPullState{}, errors.New("模型目录不可用")
	}
	if req.ModelID == "" {
		return ModelPullState{}, errors.New("modelId 不能为空")
	}
	if _, installed, _ := store.Resolve(req.ModelID); installed {
		return ModelPullState{ModelID: req.ModelID, Phase: PullDone, Total: -1}, nil
	}
	if job := c.activePull(req.ModelID); job != nil {
		return job.snapshot(), nil
	}
	if _, ok := store.Registry().Lookup(req.ModelID); !ok {
		return ModelPullState{}, fmt.Errorf("未知模型 %q", req.ModelID)
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := &pullJob{
		core:    c,
		modelID: req.ModelID,
		cancel:  cancel,
		st:      ModelPullState{ModelID: req.ModelID, Phase: PullQueued, Total: -1},
	}
	c.registerPull(job)
	go c.runPull(ctx, store, req.ModelID, job)
	return job.snapshot(), nil
}

func (c *Core) runPull(ctx context.Context, store *models.Store, id string, job *pullJob) {
	defer c.unregisterPull(id)

	job.setPhase(PullResolving)
	job.setPhase(PullDownloading)
	path, err := store.Pull(ctx, id, func(done, total int64) { job.setProgress(done, total) })
	if err != nil {
		job.fail(err)
		c.log("warn", fmt.Sprintf("模型 %s 下载失败: %v", id, err))
		return
	}
	job.setPhase(PullVerifying)
	job.setPhase(PullInstalling)
	c.log("info", fmt.Sprintf("模型 %s 已安装: %s", id, path))

	// Post-install self test (P2c/P4): only runs when the runtime can actually
	// load the model, otherwise the pull still succeeds and the UI shows
	// "未验证" instead of failing the download.
	if st := c.SelfTestModel(id, true); !st.OK {
		c.log("warn", fmt.Sprintf("模型 %s 自检未通过", id))
	}
	job.mu.Lock()
	job.st.Phase = PullDone
	job.mu.Unlock()
	job.publish(true)
}

// CancelPull cancels an in-flight download. It reports whether a job was active.
func (c *Core) CancelPull(modelID string) bool {
	job := c.activePull(modelID)
	if job == nil {
		return false
	}
	job.cancel()
	c.log("info", "已请求取消模型下载: "+modelID)
	return true
}

// RemoveModel deletes an installed model directory (registry or imported).
func (c *Core) RemoveModel(modelID string) error {
	if modelID == "" {
		return errors.New("modelId 不能为空")
	}
	if job := c.activePull(modelID); job != nil {
		job.cancel()
		return errors.New("下载进行中，请先取消")
	}
	store := c.modelStore()
	if store == nil {
		return errors.New("模型目录不可用")
	}
	dir := filepath.Join(store.Dir(), modelID)
	// Guard against path tricks: only delete a direct child of the model dir.
	if filepath.Dir(dir) != filepath.Clean(store.Dir()) {
		return fmt.Errorf("非法模型 id %q", modelID)
	}
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("模型 %q 未安装", modelID)
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	c.log("info", "已删除模型: "+modelID)
	return nil
}

// ImportModel copies a user supplied .onnx (optionally with .onnx.data) into the
// model cache as an imported model and writes a sidecar so the pipeline can use it.
func (c *Core) ImportModel(req ImportModelRequest) (ModelInfo, error) {
	store := c.modelStore()
	if store == nil {
		return ModelInfo{}, errors.New("模型目录不可用")
	}
	src := strings.TrimSpace(req.Path)
	if src == "" {
		return ModelInfo{}, errors.New("模型路径不能为空")
	}
	info, _ := os.Stat(src)
	if info == nil {
		return ModelInfo{}, fmt.Errorf("路径不存在: %s", src)
	}

	onnxPath := src
	companions := []string{}
	if info.IsDir() {
		entries, err := os.ReadDir(src)
		if err != nil {
			return ModelInfo{}, err
		}
		found := ""
		names := make([]string, 0, len(entries))
		for _, de := range entries {
			if de.IsDir() {
				continue
			}
			names = append(names, de.Name())
			lower := strings.ToLower(de.Name())
			if strings.HasSuffix(lower, ".onnx") {
				if found == "" || de.Name() == "model.onnx" {
					found = filepath.Join(src, de.Name())
				}
			}
		}
		if found == "" {
			return ModelInfo{}, errors.New("目录中没有 .onnx 文件")
		}
		onnxPath = found
		base := strings.TrimSuffix(filepath.Base(found), filepath.Ext(found))
		for _, name := range names {
			lower := strings.ToLower(name)
			if name == filepath.Base(found) {
				continue
			}
			if strings.HasSuffix(lower, ".data") || strings.HasPrefix(name, strings.TrimSuffix(filepath.Base(found), ".onnx")+".onnx_") {
				companions = append(companions, filepath.Join(src, name))
			}
			_ = base
		}
	}

	// Same-directory companions referenced by ONNX external data.
	if !info.IsDir() {
		matches, _ := filepath.Glob(onnxPath + "*.data")
		companions = append(companions, matches...)
	}

	id := importSlug(req.Name, onnxPath)
	id = c.uniqueModelID(store, id)
	dir := filepath.Join(store.Dir(), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ModelInfo{}, err
	}
	dst := filepath.Join(dir, "model.onnx")
	if err := copyFile(onnxPath, dst); err != nil {
		_ = os.RemoveAll(dir)
		return ModelInfo{}, err
	}
	for _, comp := range companions {
		name := "model.onnx.data"
		if base := filepath.Base(comp); strings.HasSuffix(strings.ToLower(base), ".data") {
			name = "model.onnx.data"
		}
		if err := copyFile(comp, filepath.Join(dir, name)); err != nil {
			_ = os.RemoveAll(dir)
			return ModelInfo{}, err
		}
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(onnxPath), filepath.Ext(onnxPath))
	}
	scale := 4
	sc := models.Sidecar{
		Inputs:  []models.SidecarInput{{Name: "", Source: "image"}},
		Outputs: []models.SidecarOutput{{Name: "", Scale: scale, Crop: 0}},
		Preprocess: models.Preprocess{
			Norm: "0-1", Channels: 3,
		},
		Constraints: models.Constraints{Batch: 1, DType: "fp32"},
		Meta: models.Meta{
			Notes: name,
		},
	}
	if req.AutoDetect {
		sc.Meta.Notes = name + " (scale/归一化待自动探测)"
	}
	for _, w := range c.ImportWarnings(dir) {
		sc.Meta.Notes += "\n" + w
	}
	sc.ApplyDefaults()
	if err := sc.Validate(); err != nil {
		_ = os.RemoveAll(dir)
		return ModelInfo{}, fmt.Errorf("生成的 sidecar 无效: %w", err)
	}
	if err := sc.Write(filepath.Join(dir, "model.json")); err != nil {
		_ = os.RemoveAll(dir)
		return ModelInfo{}, err
	}
	c.log("info", fmt.Sprintf("已导入模型 %s -> %s", id, dir))

	for _, m := range c.ListModels() {
		if m.ID == id {
			return m, nil
		}
	}
	return ModelInfo{ID: id, Name: name, Scale: scale, Installed: true, Path: dst, Origin: models.OriginImported, Sidecar: true}, nil
}

// ImportWarnings returns human readable hints about an imported model directory
// (e.g. missing .data companions). Currently used to annotate the sidecar.
func (c *Core) ImportWarnings(dir string) []string {
	return nil
}

// GetModelDiagnostics returns what we can report about a model today. ONNX graph
// introspection (opset/operators/shapes) is added in P4 by the native probe.
func (c *Core) GetModelDiagnostics(modelID string) ModelDiagnostics {
	out := ModelDiagnostics{ModelID: modelID}
	store := c.modelStore()
	if store == nil {
		out.Error = "模型目录不可用"
		return out
	}
	path, installed, err := store.Resolve(modelID)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	if !installed {
		out.Error = "模型未安装"
		return out
	}
	out.Warnings = append(out.Warnings, "ONNX 计算图解析（opset/算子/形状）将在 P4 提供")
	dir := filepath.Dir(path)
	if st, err := os.Stat(path); err == nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("权重文件: %s (%.1f MB)", filepath.Base(path), float64(st.Size())/(1024*1024)))
	}
	sidecarPath := filepath.Join(dir, "model.json")
	if sc, err := models.ParseSidecar(sidecarPath); err == nil {
		out.Opset = 0
		out.Inputs = append(out.Inputs, TensorShape{Name: firstInputName(sc.Inputs), Dims: []int{-1, sc.Preprocess.Channels, -1, -1}, Dtype: sc.Constraints.DType})
		for _, o := range sc.Outputs {
			out.Outputs = append(out.Outputs, TensorShape{Name: o.Name, Dims: []int{-1, sc.Preprocess.Channels, -1, -1}, Dtype: sc.Constraints.DType})
		}
		out.Warnings = append(out.Warnings, fmt.Sprintf("sidecar: scale=%d crop=%d norm=%s tileAlign=%d fixedSize=%d", firstOutputScale(sc.Outputs), firstOutputCrop(sc.Outputs), sc.Preprocess.Norm, sc.Constraints.TileAlign, sc.Constraints.FixedSize))
	} else {
		out.Warnings = append(out.Warnings, "缺少 model.json：将使用默认参数（scale=4, norm=0-1）")
	}
	if reason := readFirstLine(filepath.Join(dir, models.FailedMarker)); reason != "" {
		out.Error = reason
	}
	return out
}

func firstInputName(in []models.SidecarInput) string {
	if len(in) > 0 {
		return in[0].Name
	}
	return ""
}

func firstOutputScale(out []models.SidecarOutput) int {
	if len(out) > 0 {
		return out[0].Scale
	}
	return 0
}

func firstOutputCrop(out []models.SidecarOutput) int {
	if len(out) > 0 {
		return out[0].Crop
	}
	return 0
}

func readFirstLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

// ---- helpers ----------------------------------------------------------------

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

func importSlug(name, onnxPath string) string {
	base := strings.TrimSpace(name)
	if base == "" {
		base = strings.TrimSuffix(filepath.Base(onnxPath), filepath.Ext(onnxPath))
	}
	slug := slugUnsafe.ReplaceAllString(strings.ToLower(base), "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "imported-model"
	}
	if len(slug) > 48 {
		slug = slug[:48]
	}
	return slug
}

func (c *Core) uniqueModelID(store *models.Store, base string) string {
	exists := func(id string) bool {
		if _, err := os.Stat(filepath.Join(store.Dir(), id)); err == nil {
			return true
		}
		_, ok := store.Registry().Lookup(id)
		return ok
	}
	if !exists(base) {
		return base
	}
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !exists(cand) {
			return cand
		}
	}
	return fmt.Sprintf("%s-%d", base, time.Now().UnixNano())
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

// modelStore returns the store under lock.
func (c *Core) modelStore() *models.Store {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.models
}

// sortedIDs is used by tests and by the model list ordering.
func sortedIDs(list []ModelInfo) []string {
	ids := make([]string, 0, len(list))
	for _, m := range list {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	return ids
}
