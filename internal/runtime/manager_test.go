package runtime

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// buildArchive builds a nupkg-shaped zip with the given entries.
func buildArchive(t *testing.T, entries map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)
	for name, data := range entries {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	payload := map[string][]byte{
		"runtimes/win-x64/native/onnxruntime.dll":                []byte("fake-runtime"),
		"runtimes/win-x64/native/onnxruntime_providers_shared.dll": []byte("fake-shared"),
		"runtimes/win-x64/native/onnxruntime.lib":               []byte("ignored"),
		"build/native/include/onnxruntime_c_api.h":              []byte("ignored"),
	}
	archive := buildArchive(t, payload)
	sum := sha256.Sum256(archive)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)

	m := NewManager(t.TempDir())
	m.SetCandidates([]Component{{
		Flavor:   FlavorDirectML,
		Version:  "1.24.4",
		Required: []string{"onnxruntime.dll"},
		Parts: []Part{{
			Name:   "onnxruntime-directml",
			URL:    server.URL + "/dml.nupkg",
			SHA256: hex.EncodeToString(sum[:]),
			SizeMB: 1,
			Extract: func(entry string) string {
				const prefix = "runtimes/win-x64/native/"
				if len(entry) > len(prefix) && entry[:len(prefix)] == prefix {
					name := entry[len(prefix):]
					if filepath.Ext(name) == ".dll" {
						return name
					}
				}
				return ""
			},
		}},
	}})
	return m
}
func TestManagerInstallAndRemove(t *testing.T) {
	m := newTestManager(t)
	if m.Installed(FlavorDirectML) {
		t.Fatal("fresh manager must not report an installed runtime")
	}
	dir := m.Dir(FlavorDirectML)
	if filepath.Base(filepath.Dir(dir)) != "ort" {
		t.Fatalf("unexpected runtime dir %s", dir)
	}

	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("install: %v", err)
	}
	if !m.Installed(FlavorDirectML) {
		t.Fatal("runtime should be installed after Install")
	}
	if _, err := os.Stat(filepath.Join(dir, "onnxruntime.dll")); err != nil {
		t.Fatalf("onnxruntime.dll missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "onnxruntime_providers_shared.dll")); err != nil {
		t.Fatalf("providers dll missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "onnxruntime.lib")); !os.IsNotExist(err) {
		t.Fatal(".lib must not be extracted")
	}
	phase, _, done, total, lastErr := m.Progress()
	if phase != "done" || done != 1 || total != 1 || lastErr != "" {
		t.Fatalf("progress = %q %d/%d %q", phase, done, total, lastErr)
	}

	// Idempotent: a second install is a no-op success.
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("second install: %v", err)
	}

	if err := m.Remove(FlavorDirectML); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if m.Installed(FlavorDirectML) {
		t.Fatal("runtime should be gone after Remove")
	}
}

func TestManagerRejectsChecksumMismatch(t *testing.T) {
	m := newTestManager(t)
	component := m.Candidate(FlavorDirectML)
	component.Parts[0].SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	m.SetCandidates([]Component{component})

	if err := m.Install(context.Background()); err == nil {
		t.Fatal("install with a wrong checksum must fail")
	}
	if m.Installed(FlavorDirectML) {
		t.Fatal("a failed install must not leave an installed runtime")
	}
	phase, _, _, _, lastErr := m.Progress()
	if phase != "failed" || lastErr == "" {
		t.Fatalf("expected failed phase with a message, got %q %q", phase, lastErr)
	}
}

func TestManagerActivate(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DirectML activation is Windows only")
	}
	m := newTestManager(t)
	if dir, ok := m.Activate(FlavorDirectML); ok || dir != "" {
		t.Fatal("activate must fail when the runtime is not installed")
	}
	if err := m.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	previous, hadPrevious := os.LookupEnv(EnvVar)
	t.Cleanup(func() {
		if hadPrevious {
			_ = os.Setenv(EnvVar, previous)
		} else {
			_ = os.Unsetenv(EnvVar)
		}
	})
	dir, ok := m.Activate(FlavorDirectML)
	if !ok || dir != m.Dir(FlavorDirectML) {
		t.Fatalf("activate = %q,%v", dir, ok)
	}
	if got := os.Getenv(EnvVar); got != dir {
		t.Fatalf("%s = %q, want %q", EnvVar, got, dir)
	}
}

func TestState(t *testing.T) {
	m := newTestManager(t)
	state := m.State(FlavorDirectML)
	if state.Flavor != FlavorDirectML || state.Installed || !state.Installable {
		t.Fatalf("state = %+v", state)
	}
	if state.Version != "1.24.4" || state.SizeMB <= 0 {
		t.Fatalf("state component info = %+v", state)
	}
	if cpu := m.State(FlavorCPU); cpu.Installable || cpu.Installed {
		t.Fatalf("cpu state = %+v", cpu)
	}
}

var _ = io.Discard
// TestPlatformFlavorGating pins the platform matrix: DirectML is Windows only,
// and Linux never offers it (the whole point of the runtime flavor split).
func TestPlatformFlavorGating(t *testing.T) {
	if got, want := platformSupportsFlavor(FlavorDirectML), runtime.GOOS == "windows"; got != want {
		t.Fatalf("DirectML supported = %v, want %v", got, want)
	}
	if runtime.GOOS != "linux" {
		if platformSupportsFlavor(FlavorCUDA) || platformSupportsFlavor(FlavorOpenVINO) {
			t.Fatal("CUDA/OpenVINO must not be offered outside Linux")
		}
	}
}
