// Package runtime manages the optional ONNX Runtime binaries that the native
// core loads dynamically (see docs/PLAN-v0.3.md ADR-003).
//
// The C++ layer never links against ONNX Runtime; it resolves the library at run
// time through CHRONOS_ORT_DIR / <exe dir>/ort. This package owns download,
// verification and activation of those binaries so that:
//   - the shipped packages can carry the CPU runtime, and
//   - GPU support (DirectML on Windows) is an explicit, verified download.
//
// Windows GPU needs TWO upstream archives: the DirectML build of ONNX Runtime
// and the DirectML redistributable itself. The native loader takes the first
// CHRONOS_ORT_DIR entry, so a missing DirectML.dll would make the DML execution
// provider fail hard at session creation. Installed() therefore requires both
// files; the manager never activates a half-installed runtime.
package runtime

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/artifact"
)

// ORTVersion is the pinned ONNX Runtime version. DirectML builds stop at 1.24.4
// upstream (see ADR-003).
const ORTVersion = "1.24.4"

// DirectMLVersion is the pinned DirectML redistributable version.
const DirectMLVersion = "1.15.4"

// Flavors.
const (
	FlavorCPU      = "cpu"      // bundled with the packages (no download)
	FlavorDirectML = "directml" // optional GPU runtime (Windows only)
	FlavorCUDA     = "cuda"     // optional GPU runtime (Linux/NVIDIA only)
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
	Flavor  string
	Version string
	Parts   []Part
	// Required lists the files that must exist after extraction.
	Required []string
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
		if name == "" || strings.Contains(name, "/") || strings.Contains(name, `\`) {
			return ""
		}
		if !strings.HasSuffix(strings.ToLower(name), ".dll") {
			return ""
		}
		return name
	}
}

// CUDAComponent is the pinned Linux/NVIDIA runtime (official ORT release).
// The archive still needs the system CUDA 12 libraries; it only carries the
// ONNX Runtime CUDA execution provider itself.
func CUDAComponent() Component {
	return Component{
		Flavor:   FlavorCUDA,
		Version:  ORTVersion,
		Required: []string{"libonnxruntime.so"},
		Parts: []Part{
			{
				Name:   "onnxruntime-cuda",
				URL: "https://github.com/microsoft/onnxruntime/releases/download/v" + ORTVersion +
					"/onnxruntime-linux-x64-gpu-" + ORTVersion + ".tgz",
				SHA256: "", // filled in below once verified (see ORTCUDAArchiveSHA256)
				SizeMB: 195.9,
				Extract: func(entry string) string {
					const marker = "/lib/"
					index := strings.Index(entry, marker)
					if index < 0 {
						return ""
					}
					name := entry[index+len(marker):]
					if name == "" || strings.Contains(name, "/") || strings.Contains(name, `\`) {
						return ""
					}
					return name
				},
			},
		},
	}
}

// ORTCUDAArchiveSHA256 is the SHA256 of the pinned CUDA runtime archive.
const ORTCUDAArchiveSHA256 = "c5f804ff5d239b436fa59e9f2fb288a39f7eb9552f6a636c8b71e792e91a8808"

// defaultComponent returns the GPU runtime component for this platform. It is
// empty on platforms without a supported GPU path, and Install() refuses to run
// for a component that does not match the platform: DirectML is never offered on
// Linux and CUDA is never offered on Windows.
func defaultComponent() Component {
	if runtime.GOOS == "linux" {
		component := CUDAComponent()
		component.Parts[0].SHA256 = ORTCUDAArchiveSHA256
		return component
	}
	if runtime.GOOS == "windows" {
		return DirectMLComponent()
	}
	return Component{}
}
// SHA256 values were verified against nuget.org on 2026-11-08.
func DirectMLComponent() Component {
	return Component{
		Flavor:   FlavorDirectML,
		Version:  ORTVersion,
		Required: []string{"onnxruntime.dll", "DirectML.dll"},
		Parts: []Part{
			{
				Name:   "onnxruntime-directml",
				URL: "https://api.nuget.org/v3-flatcontainer/microsoft.ml.onnxruntime.directml/" +
					ORTVersion + "/microsoft.ml.onnxruntime.directml." + ORTVersion + ".nupkg",
				SHA256:  "57e9f11b73437bef7a309496135d4c1f96b1a8e9ddba60013fa27bfc1d788681",
				SizeMB:  11.9,
				Extract: basenameDL("runtimes/win-x64/native/"),
			},
			{
				Name:   "directml",
				URL: "https://api.nuget.org/v3-flatcontainer/microsoft.ai.directml/" +
					DirectMLVersion + "/microsoft.ai.directml." + DirectMLVersion + ".nupkg",
				SHA256:  "4e7cb7ddce8cf837a7a75dc029209b520ca0101470fcdf275c1f49736a3615b9",
				SizeMB:  192.9,
				Extract: basenameDL("bin/x64-win/"),
			},
		},
	}
}

// State is the UI facing view of one flavor.
type State struct {
	Flavor      string  `json:"flavor"`
	Installed   bool    `json:"installed"`
	Dir         string  `json:"dir"`
	Installable bool    `json:"installable"`
	Version     string  `json:"version"`
	SizeMB      float64 `json:"sizeMb"`
}

// Manager owns <dataDir>/runtime/ort/<flavor>-<version>/.
type Manager struct {
	root       string
	downloader *artifact.Downloader
	component  Component

	mu      sync.Mutex
	logf    func(format string, args ...any)
	phase   string
	part    string
	done    int64
	total   int64
	lastErr string
}

// NewManager creates the manager below <dataDir>/runtime.
func NewManager(dataDir string) *Manager {
	return &Manager{
		root:       filepath.Join(dataDir, "runtime"),
		downloader: &artifact.Downloader{},
		component:  defaultComponent(),
	}
}

// SetComponent overrides the downloadable component (tests / future flavors).
func (m *Manager) SetComponent(c Component) {
	m.mu.Lock()
	m.component = c
	m.mu.Unlock()
}

// SetLogger installs a log sink (optional).
func (m *Manager) SetLogger(logf func(format string, args ...any)) {
	m.mu.Lock()
	m.logf = logf
	m.mu.Unlock()
}

// Root is the runtime directory (<dataDir>/runtime).
func (m *Manager) Root() string { return m.root }

// Dir is the install directory of one flavor.
func (m *Manager) Dir(flavor string) string {
	m.mu.Lock()
	version := m.component.Version
	m.mu.Unlock()
	if version == "" {
		version = ORTVersion
	}
	return filepath.Join(m.root, "ort", flavor+"-"+version)
}

// Installed reports whether every required file of the flavor exists.
func (m *Manager) Installed(flavor string) bool {
	if flavor == FlavorCPU {
		return false
	}
	m.mu.Lock()
	component := m.component
	m.mu.Unlock()
	if component.Flavor != flavor || len(component.Required) == 0 {
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

// GpuFlavor is the GPU runtime flavor this platform would use ("" when the
// platform has no supported GPU path). Linux reports "cuda" only when an NVIDIA
// device is present; DirectML is never offered on Linux.
func (m *Manager) GpuFlavor() string {
	m.mu.Lock()
	flavor := m.component.Flavor
	m.mu.Unlock()
	if !platformSupportsFlavor(flavor) {
		return ""
	}
	return flavor
}

// State reports one flavor for the UI.
func (m *Manager) State(flavor string) State {
	m.mu.Lock()
	component := m.component
	m.mu.Unlock()
	installable := component.Flavor == flavor && platformSupportsFlavor(flavor)
	return State{
		Flavor:      flavor,
		Installed:   m.Installed(flavor),
		Dir:         m.Dir(flavor),
		Installable: installable,
		Version:     component.Version,
		SizeMB:      component.SizeMB(),
	}
}

// Progress is the current install progress. Done/Total refer to the part that
// is being downloaded; Part names it ("onnxruntime-directml", "directml", ...).
func (m *Manager) Progress() (phase, part string, done, total int64, lastErr string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.phase, m.part, m.done, m.total, m.lastErr
}

// Install downloads and extracts every part of the component. It is blocking;
// callers usually run it in a goroutine and poll Progress/State.
func (m *Manager) Install(ctx context.Context) error {
	m.mu.Lock()
	component := m.component
	logf := m.logf
	m.phase, m.part, m.done, m.total, m.lastErr = "resolving", "", 0, 0, ""
	m.mu.Unlock()

	log := func(format string, args ...any) {
		if logf != nil {
			logf(format, args...)
		}
	}
	if component.Flavor == "" || !platformSupportsFlavor(component.Flavor) {
		err := fmt.Errorf("no GPU runtime component for this platform (DirectML is Windows only, CUDA is Linux only)")
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
		archive := filepath.Join(cacheDir, fmt.Sprintf("%s-%s-%d.nupkg", component.Flavor, component.Version, index))
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

// Activate points the native loader at the installed runtime by setting
// CHRONOS_ORT_DIR. It must run before the first engine is created: the native
// loader caches a successfully loaded library for the process lifetime.
func (m *Manager) Activate(flavor string) (string, bool) {
	m.mu.Lock()
	componentFlavor := m.component.Flavor
	m.mu.Unlock()
	if flavor != componentFlavor || !platformSupportsFlavor(flavor) || !m.Installed(flavor) {
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

// platformSupportsFlavor reports whether the flavor makes sense on this OS.
// DirectML is Windows-only; CUDA is Linux-only and additionally gated on an
// NVIDIA-capable machine so we never push a 196 MB download at a machine that
// cannot use it.
func platformSupportsFlavor(flavor string) bool {
	switch flavor {
	case FlavorDirectML:
		return runtime.GOOS == "windows"
	case FlavorCUDA:
		return runtime.GOOS == "linux" && nvidiaPresent()
	}
	return false
}

// extractArchive unpacks matching zip or tar.gz entries into dir (no traversal).
func extractArchive(archivePath, dir string, match func(string) string) error {
	if strings.HasSuffix(strings.ToLower(archivePath), ".tgz") || strings.HasSuffix(strings.ToLower(archivePath), ".tar.gz") {
		return extractTarArchive(archivePath, dir, match)
	}
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
		dstPath := filepath.Join(dir, name)
		dst, err := os.Create(dstPath)
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

// extractTarArchive handles .tgz/.tar.gz archives. The official ONNX Runtime
// Linux release stores libonnxruntime.so as a symlink to the versioned file, and
// the native loader needs a real file, so symlinks are materialised as copies.
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
		target := filepath.Join(dir, entry.target)
		data, err := os.ReadFile(target)
		if err != nil {
			continue // dangling link: the required-file check reports it later
		}
		if err := os.WriteFile(filepath.Join(dir, entry.name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
