// Package runtime manages the optional ONNX Runtime binaries that the native
// core loads dynamically (see docs/PLAN-v0.3.md ADR-003).
//
// The C++ layer never links ONNX Runtime; it resolves the library at run time
// through CHRONOS_ORT_DIR / <exe dir>/ort. This package owns download,
// verification and activation of those binaries.
//
// Platform matrix (DirectML is never offered on Linux):
//   - Windows:      DirectML (Intel/AMD/NVIDIA)  onnxruntime-directml + DirectML.dll
//   - Linux/NVIDIA: CUDA                         official ORT gpu tgz (+ system CUDA libs)
//   - Linux/Intel:  OpenVINO                     onnxruntime-openvino wheel (self-contained)
//
// Every candidate runtime is verified in a CHILD PROCESS (`chronos probe --json`)
// before the parent process points CHRONOS_ORT_DIR at it: the native loader
// caches a loaded library for the process lifetime, so activating an
// incompatible runtime would be unrecoverable.
package runtime

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/artifact"
)

// Pinned runtime versions.
const (
	ORTVersion        = "1.24.4" // CPU runtime + DirectML/CUDA components
	DirectMLVersion   = "1.15.4" // DirectML redistributable
	OpenVINORTVersion = "1.24.1" // version inside the onnxruntime-openvino wheel
)

// Flavors.
const (
	FlavorCPU      = "cpu"
	FlavorDirectML = "directml"
	FlavorCUDA     = "cuda"
	FlavorOpenVINO = "openvino"
)

// EnvVar is the environment variable the native loader reads first.
const EnvVar = "CHRONOS_ORT_DIR"

// Part is one downloadable archive inside a component.
type Part struct {
	Name    string
	URL     string
	SHA256  string
	SizeMB  float64
	Extract func(entry string) string
}

// Component is a flavor made of one or more archives.
type Component struct {
	Flavor   string
	Version  string
	Parts    []Part
	Required []string
	// Experimental marks runtimes not yet verified on real hardware by this
	// project's CI: the UI shows an "exp" badge and failures auto-disable.
	Experimental bool
	// Materialize runs after extraction (e.g. create libonnxruntime.so from a
	// versioned file, which the loader requires).
	Materialize func(dir string) error
}

// SizeMB is the total download size of the component.
func (c Component) SizeMB() float64 {
	var total float64
	for _, part := range c.Parts {
		total += part.SizeMB
	}
	return total
}

func basenameDL(prefix string) func(string) string {
	return func(entry string) string {
		if !strings.HasPrefix(entry, prefix) {
			return ""
		}
		name := strings.TrimPrefix(entry, prefix)
		if name == "" || strings.ContainsAny(name, `/\`) {
			return ""
		}
		if !strings.HasSuffix(strings.ToLower(name), ".dll") {
			return ""
		}
		return name
	}
}

// DirectMLComponent is the pinned Windows GPU runtime (hashes verified against
// nuget.org on 2026-11-08).
func DirectMLComponent() Component {
	return Component{
		Flavor:   FlavorDirectML,
		Version:  ORTVersion,
		Required: []string{"onnxruntime.dll", "DirectML.dll"},
		Parts: []Part{
			{
				Name: "onnxruntime-directml",
				URL: "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime.directml/" +
					ORTVersion + "/microsoft.ml.onnxruntime.directml." + ORTVersion + ".nupkg",
				SHA256:  "57e9f11b73437bef7a309496135d4c1f96b1a8e9ddba60013fa27bfc1d788681",
				SizeMB:  11.9,
				Extract: basenameDL("runtimes/win-x64/native/"),
			},
			{
				Name: "directml",
				URL: "https://api.nuget.org/v3-flatcontainer/microsoft.ai.directml/" +
					DirectMLVersion + "/microsoft.ai.directml." + DirectMLVersion + ".nupkg",
				SHA256:  "4e7cb7ddce8cf837a7a75dc029209b520ca0101470fcdf275c1f49736a3615b9",
				SizeMB:  192.9,
				Extract: basenameDL("bin/x64-win/"),
			},
		},
	}
}

// CUDAComponent is the pinned Linux/NVIDIA runtime. The system still needs the
// CUDA 12 user-space libraries; this archive only carries ORT's CUDA EP.
func CUDAComponent() Component {
	return Component{
		Flavor:       FlavorCUDA,
		Version:      ORTVersion,
		Experimental: true,
		Required:     []string{"libonnxruntime.so"},
		Parts: []Part{
			{
				Name: "onnxruntime-cuda",
				URL: "https://github.com/microsoft/onnxruntime/releases/download/v" + ORTVersion +
					"/onnxruntime-linux-x64-gpu-" + ORTVersion + ".tgz",
				SHA256: "c5f804ff5d239b436fa59e9f2fb288a39f7eb9552f6a636c8b71e792e91a8808",
				SizeMB: 195.9,
				Extract: func(entry string) string {
					const marker = "/lib/"
					index := strings.Index(entry, marker)
					if index < 0 {
						return ""
					}
					name := entry[index+len(marker):]
					if name == "" || strings.ContainsAny(name, `/\`) {
						return ""
					}
					return name
				},
			},
		},
		Materialize: func(dir string) error { return materializeVersionedLib(dir, "libonnxruntime.so") },
	}
}

// OpenVINOComponent is the pinned Linux/Intel runtime: a self-contained wheel
// with ONNX Runtime + the OpenVINO EP + Intel CPU/GPU/NPU plugins (no system
// OpenVINO installation required).
func OpenVINOComponent() Component {
	return Component{
		Flavor:       FlavorOpenVINO,
		Version:      OpenVINORTVersion,
		Experimental: true,
		Required:     []string{"libonnxruntime.so", "libonnxruntime_providers_openvino.so"},
		Parts: []Part{
			{
				Name: "onnxruntime-openvino",
				URL: "https://files.pythonhosted.org/packages/50/cf/17ba72de2df0fcba349937d2788f154397bbc2d1a2d67772a97e26f6bc5f/" +
					"onnxruntime_openvino-1.24.1-cp312-cp312-manylinux_2_28_x86_64.whl",
				SHA256: "d617fac2f59a6ab5ea59a788c3e1592240a129642519aaeaa774761dfe35150e",
				SizeMB: 80.5,
				Extract: func(entry string) string {
					const prefix = "onnxruntime/capi/"
					if !strings.HasPrefix(entry, prefix) {
						return ""
					}
					name := strings.TrimPrefix(entry, prefix)
					if name == "" || strings.ContainsAny(name, `/\`) {
						return ""
					}
					if !strings.HasPrefix(name, "lib") || !strings.Contains(name, ".so") {
						return ""
					}
					return name
				},
			},
		},
		Materialize: func(dir string) error { return materializeVersionedLib(dir, "libonnxruntime.so") },
	}
}

// materializeVersionedLib copies lib<name>.so.<ver> to the plain name the native
// loader opens.
func materializeVersionedLib(dir, plain string) error {
	target := filepath.Join(dir, plain)
	if st, err := os.Stat(target); err == nil && st.Size() > 0 {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), plain+".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	}
	return fmt.Errorf("no versioned %s.* found in %s", plain, dir)
}

// State is the UI facing view of one flavor.
type State struct {
	Flavor       string  `json:"flavor"`
	Installed    bool    `json:"installed"`
	Dir          string  `json:"dir"`
	Installable  bool    `json:"installable"`
	Version      string  `json:"version"`
	SizeMB       float64 `json:"sizeMb"`
	Experimental bool    `json:"experimental"`
}

// Manager owns <dataDir>/runtime/ort/<flavor>-<version>/.
type Manager struct {
	root       string
	downloader *artifact.Downloader

	mu         sync.Mutex
	candidates []Component
	component  Component
	logf       func(format string, args ...any)
	phase      string
	part       string
	done       int64
	total      int64
	lastErr    string
	forced     bool
	// runCommand is overridable in tests (defaults to os/exec).
	runCommand func(ctx context.Context, exe string, args []string, env []string) ([]byte, error)
}

// NewManager creates the manager below <dataDir>/runtime and selects the best
// GPU runtime candidate for this machine.
func NewManager(dataDir string) *Manager {
	m := &Manager{
		root:       filepath.Join(dataDir, "runtime"),
		downloader: &artifact.Downloader{},
	}
	m.candidates = candidatesForPlatform()
	if len(m.candidates) > 0 {
		m.component = m.candidates[0]
	}
	return m
}

// candidatesForPlatform lists the GPU runtimes worth offering, best first.
// Detection is advisory: a false positive only costs a download, and Verify()
// prevents an unusable runtime from being activated.
func candidatesForPlatform() []Component {
	switch runtime.GOOS {
	case "windows":
		return []Component{DirectMLComponent()}
	case "linux":
		var out []Component
		if nvidiaPresent() {
			out = append(out, CUDAComponent())
		}
		if intelPresent() {
			out = append(out, OpenVINOComponent())
		}
		return out
	}
	return nil
}

// SetCandidates overrides the candidate list (tests / future flavors).
func (m *Manager) SetCandidates(list []Component) {
	m.mu.Lock()
	m.candidates = list
	m.forced = true
	if len(list) > 0 {
		m.component = list[0]
	} else {
		m.component = Component{}
	}
	m.mu.Unlock()
}

// supported reports whether the flavor is allowed here (tests may force it).
func (m *Manager) supported(flavor string) bool {
	m.mu.Lock()
	forced := m.forced
	m.mu.Unlock()
	return forced || platformSupportsFlavor(flavor)
}

// SetLogger installs a log sink (optional).
func (m *Manager) SetLogger(logf func(format string, args ...any)) {
	m.mu.Lock()
	m.logf = logf
	m.mu.Unlock()
}

// Root is the runtime directory (<dataDir>/runtime).
func (m *Manager) Root() string { return m.root }

// Candidate returns the component for a flavor (zero when unknown here).
func (m *Manager) Candidate(flavor string) Component {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.component.Flavor == flavor {
		return m.component
	}
	for _, candidate := range m.candidates {
		if candidate.Flavor == flavor {
			return candidate
		}
	}
	return Component{}
}

// GpuFlavor is the GPU runtime flavor this machine would use ("" when none).
func (m *Manager) GpuFlavor() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.component.Flavor
}

// Dir is the install directory of one flavor.
func (m *Manager) Dir(flavor string) string {
	version := m.Candidate(flavor).Version
	if version == "" {
		version = ORTVersion
	}
	return filepath.Join(m.root, "ort", flavor+"-"+version)
}

// Installed reports whether every required file of the flavor exists.
func (m *Manager) Installed(flavor string) bool {
	component := m.Candidate(flavor)
	if component.Flavor == "" || len(component.Required) == 0 {
		return false
	}
	dir := m.Dir(flavor)
	for _, name := range component.Required {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}

// State reports one flavor for the UI.
func (m *Manager) State(flavor string) State {
	component := m.Candidate(flavor)
	return State{
		Flavor:       flavor,
		Installed:    m.Installed(flavor),
		Dir:          m.Dir(flavor),
		Installable:  component.Flavor != "" && m.supported(flavor),
		Version:      component.Version,
		SizeMB:       component.SizeMB(),
		Experimental: component.Experimental,
	}
}

// Progress is the current install progress (per part).
func (m *Manager) Progress() (phase, part string, done, total int64, lastErr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.phase, m.part, m.done, m.total, m.lastErr
}

// Install downloads and extracts the selected GPU runtime.
func (m *Manager) Install(ctx context.Context) error {
	m.mu.Lock()
	component := m.component
	m.mu.Unlock()
	return m.installComponent(ctx, component)
}

// InstallFlavor downloads and extracts a specific GPU runtime flavor.
func (m *Manager) InstallFlavor(ctx context.Context, flavor string) error {
	component := m.Candidate(flavor)
	if component.Flavor == "" {
		return fmt.Errorf("no %q runtime component for this platform", flavor)
	}
	return m.installComponent(ctx, component)
}

func (m *Manager) installComponent(ctx context.Context, component Component) error {
	m.mu.Lock()
	logf := m.logf
	m.phase, m.part, m.done, m.total, m.lastErr = "resolving", "", 0, 0, ""
	m.mu.Unlock()

	log := func(format string, args ...any) {
		if logf != nil {
			logf(format, args...)
		}
	}
	if !m.supported(component.Flavor) {
		err := fmt.Errorf("the %q runtime is not supported on this platform/hardware "+
			"(DirectML is Windows only; CUDA needs NVIDIA; OpenVINO needs Intel)", component.Flavor)
		m.fail(err)
		return err
	}
	dir := m.Dir(component.Flavor)
	if m.Installed(component.Flavor) {
		log("运行时已安装: %s", dir)
		m.mu.Lock()
		m.phase, m.part, m.done, m.total = "done", "", 1, 1
		m.mu.Unlock()
		return nil
	}
	cacheDir := filepath.Join(m.root, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		m.fail(err)
		return err
	}
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		m.fail(err)
		return err
	}
	for index, part := range component.Parts {
		suffix := ".nupkg"
		if strings.Contains(part.URL, ".tgz") {
			suffix = ".tgz"
		} else if strings.Contains(part.URL, ".whl") {
			suffix = ".whl"
		}
		archive := filepath.Join(cacheDir, fmt.Sprintf("%s-%s-%d%s", component.Flavor, component.Version, index, suffix))
		m.setPhase("downloading", part.Name)
		log("下载运行时组件 %s（%0.1f MB）", part.Name, part.SizeMB)
		file := artifact.File{URLs: []string{part.URL}, SHA256: part.SHA256}
		err := m.downloader.Fetch(ctx, file, archive, func(p artifact.Progress) {
			m.mu.Lock()
			m.done, m.total = p.Done, p.Total
			m.mu.Unlock()
		})
		if err != nil {
			_ = os.Remove(archive)
			_ = os.RemoveAll(tmp)
			m.fail(fmt.Errorf("%s: %w", part.Name, err))
			return err
		}
		m.setPhase("installing", part.Name)
		if err := extractArchive(archive, tmp, part.Extract); err != nil {
			_ = os.RemoveAll(tmp)
			m.fail(fmt.Errorf("%s: %w", part.Name, err))
			return err
		}
		_ = os.Remove(archive)
	}
	if component.Materialize != nil {
		if err := component.Materialize(tmp); err != nil {
			_ = os.RemoveAll(tmp)
			m.fail(err)
			return err
		}
	}
	for _, name := range component.Required {
		if st, err := os.Stat(filepath.Join(tmp, name)); err != nil || st.Size() == 0 {
			_ = os.RemoveAll(tmp)
			err := fmt.Errorf("runtime is missing %s after extraction", name)
			m.fail(err)
			return err
		}
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		_ = os.RemoveAll(tmp)
		m.fail(err)
		return err
	}
	m.mu.Lock()
	m.phase, m.part, m.done, m.total = "done", "", 1, 1
	m.mu.Unlock()
	log("GPU 运行时已安装: %s", dir)
	return nil
}

func (m *Manager) fail(err error) {
	m.mu.Lock()
	m.phase = "failed"
	m.lastErr = err.Error()
	m.mu.Unlock()
}

func (m *Manager) setPhase(phase, part string) {
	m.mu.Lock()
	m.phase, m.part = phase, part
	m.mu.Unlock()
}

// Verify runs the app's own `probe --json` in a CHILD process with
// CHRONOS_ORT_DIR pointing at the candidate runtime. This proves the runtime can
// be loaded before the parent process (which caches the library forever) uses it.
func (m *Manager) Verify(ctx context.Context, exePath, flavor string) error {
	if !m.Installed(flavor) {
		return fmt.Errorf("runtime %q is not installed", flavor)
	}
	if exePath == "" {
		return fmt.Errorf("cannot verify: unknown executable path")
	}
	dir := m.Dir(flavor)
	verifyCtx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	runner := m.runCommand
	if runner == nil {
		runner = func(ctx context.Context, exe string, args []string, env []string) ([]byte, error) {
			cmd := exec.CommandContext(ctx, exe, args...)
			cmd.Env = env
			return cmd.Output()
		}
	}
	env := append(os.Environ(), EnvVar+"="+dir)
	output, err := runner(verifyCtx, exePath, []string{"probe", "--json"}, env)
	if err != nil {
		if verifyCtx.Err() != nil {
			return fmt.Errorf("runtime probe timed out (the candidate runtime may be incompatible): %w", verifyCtx.Err())
		}
		return fmt.Errorf("runtime probe failed (incompatible or missing dependencies): %w", err)
	}
	var report struct {
		ORT       bool     `json:"ort"`
		Providers []string `json:"providers"`
		Version   string   `json:"version"`
	}
	if err := json.Unmarshal(output, &report); err != nil {
		return fmt.Errorf("runtime probe returned invalid JSON: %w", err)
	}
	if !report.ORT {
		return fmt.Errorf("runtime probe could not load ONNX Runtime from %s", dir)
	}
	if len(report.Providers) == 0 {
		return fmt.Errorf("runtime probe reported no execution providers")
	}
	if logf := m.logger(); logf != nil {
		logf("运行时自检通过: %s %s (%s)", flavor, report.Version, strings.Join(report.Providers, ","))
	}
	return nil
}

// Activate points the native loader at the runtime by setting CHRONOS_ORT_DIR.
// Callers must Verify() first; the loader caches the library for the process
// lifetime.
func (m *Manager) Activate(flavor string) (string, bool) {
	if !m.supported(flavor) || !m.Installed(flavor) {
		return "", false
	}
	dir := m.Dir(flavor)
	if err := os.Setenv(EnvVar, dir); err != nil {
		return "", false
	}
	if logf := m.logger(); logf != nil {
		logf("GPU 运行时已激活: %s", dir)
	}
	return dir, true
}

func (m *Manager) logger() func(string, ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.logf
}

// Remove deletes an installed flavor (UI "uninstall GPU runtime").
func (m *Manager) Remove(flavor string) error {
	return artifact.Remove(m.Dir(flavor))
}

// platformSupportsFlavor reports whether the flavor makes sense on this machine.
func platformSupportsFlavor(flavor string) bool {
	switch flavor {
	case FlavorDirectML:
		return runtime.GOOS == "windows"
	case FlavorCUDA:
		return runtime.GOOS == "linux" && nvidiaPresent()
	case FlavorOpenVINO:
		return runtime.GOOS == "linux" && intelPresent()
	}
	return false
}

// extractArchive unpacks matching zip or tar.gz entries into dir (no traversal).
func extractArchive(archivePath, dir string, match func(string) string) error {
	if strings.HasSuffix(strings.ToLower(archivePath), ".tgz") || strings.HasSuffix(strings.ToLower(archivePath), ".tar.gz") {
		return extractTarArchive(archivePath, dir, match)
	}
	return extractZipArchive(archivePath, dir, match)
}

// extractTarArchive handles .tgz/.tar.gz archives; symlinks become copies.
func extractTarArchive(archivePath, dir string, match func(string) string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open runtime archive: %w", err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	type link struct{ name, target string }
	var links []link
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read runtime archive: %w", err)
		}
		base := match(header.Name)
		if base == "" {
			continue
		}
		if strings.Contains(base, "..") || strings.ContainsAny(base, `/\`) {
			return fmt.Errorf("refusing suspicious archive entry %q", header.Name)
		}
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			target := match(header.Linkname)
			if target == "" {
				target = filepath.Base(header.Linkname)
			}
			links = append(links, link{name: base, target: target})
			continue
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		dst, err := os.Create(filepath.Join(dir, base))
		if err != nil {
			return err
		}
		if _, err := io.Copy(dst, reader); err != nil {
			dst.Close()
			return err
		}
		if err := dst.Close(); err != nil {
			return err
		}
	}
	for _, entry := range links {
		data, err := os.ReadFile(filepath.Join(dir, entry.target))
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, entry.name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// extractZipArchive handles zip/nupkg/whl archives.
func extractZipArchive(archivePath, dir string, match func(string) string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open runtime archive: %w", err)
	}
	defer reader.Close()
	for _, entry := range reader.File {
		name := match(entry.Name)
		if name == "" {
			continue
		}
		if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
			return fmt.Errorf("refusing suspicious archive entry %q", entry.Name)
		}
		src, err := entry.Open()
		if err != nil {
			return err
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			src.Close()
			return err
		}
		if _, err := io.Copy(dst, src); err != nil {
			dst.Close()
			src.Close()
			return err
		}
		if err := dst.Close(); err != nil {
			src.Close()
			return err
		}
		src.Close()
	}
	return nil
}
