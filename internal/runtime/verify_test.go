package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// prepareInstalled returns a manager with the fake DirectML runtime installed and
// a command runner that reports the given probe payload.
func prepareInstalled(t *testing.T, payload string, runErr error) *Manager {
	t.Helper()
	m := newTestManager(t)
	if err := m.Install(context.Background()); err != nil {
		t.Fatalf("install: %v", err)
	}
	m.runCommand = func(ctx context.Context, exe string, args []string, env []string) ([]byte, error) {
		if runErr != nil {
			return nil, runErr
		}
		// The gate must pass the candidate runtime through CHRONOS_ORT_DIR.
		found := false
		for _, entry := range env {
			if strings.HasPrefix(entry, EnvVar+"=") && strings.Contains(entry, m.Dir(FlavorDirectML)) {
				found = true
			}
		}
		if !found {
			t.Errorf("verify did not export %s pointing at the candidate runtime", EnvVar)
		}
		if len(args) < 2 || args[0] != "probe" || args[1] != "--json" {
			t.Errorf("verify ran unexpected arguments %v", args)
		}
		return []byte(payload), nil
	}
	return m
}

func TestVerifyAcceptsUsableRuntime(t *testing.T) {
	m := prepareInstalled(t, `{"ort":true,"version":"1.24.1","providers":["CPU","OpenVINO"]}`, nil)
	if err := m.Verify(context.Background(), "chronos", FlavorDirectML); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyRejectsBrokenRuntime(t *testing.T) {
	// The child loaded ONNX Runtime but it is unusable (no providers).
	m := prepareInstalled(t, `{"ort":true,"providers":[]}`, nil)
	if err := m.Verify(context.Background(), "chronos", FlavorDirectML); err == nil {
		t.Fatal("Verify must reject a runtime with no execution providers")
	}

	// The child could not load ONNX Runtime at all.
	m2 := prepareInstalled(t, `{"ort":false,"providers":null}`, nil)
	if err := m2.Verify(context.Background(), "chronos", FlavorDirectML); err == nil {
		t.Fatal("Verify must reject ort:false")
	}

	// The child crashed / exited non-zero (the exact failure mode the gate exists
	// for: an incompatible runtime must not take the GUI down).
	m3 := prepareInstalled(t, "", errors.New("exit status 3"))
	if err := m3.Verify(context.Background(), "chronos", FlavorDirectML); err == nil {
		t.Fatal("Verify must reject a crashing probe")
	} else if !strings.Contains(err.Error(), "probe failed") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Garbage JSON must not be treated as success.
	m4 := prepareInstalled(t, "not json", nil)
	if err := m4.Verify(context.Background(), "chronos", FlavorDirectML); err == nil {
		t.Fatal("Verify must reject invalid JSON")
	}
}

func TestVerifyRequiresInstallationAndPath(t *testing.T) {
	m := newTestManager(t)
	if err := m.Verify(context.Background(), "chronos", FlavorDirectML); err == nil {
		t.Fatal("Verify must fail when the runtime is not installed")
	}
	if err := m.Verify(context.Background(), "", FlavorDirectML); err == nil {
		t.Fatal("Verify must fail without an executable path")
	}
}

func TestVerifyKeepsCandidateUpToDate(t *testing.T) {
	m := prepareInstalled(t, `{"ort":true,"providers":["CPU"]}`, nil)
	// A flavor that is not a candidate on this platform must never verify.
	if err := m.Verify(context.Background(), "chronos", "nope"); err == nil {
		t.Fatal("Verify must reject unknown flavors")
	}
}
