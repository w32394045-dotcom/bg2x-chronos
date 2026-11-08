package scheduler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/engine"
)

// ---- fakes -----------------------------------------------------------------

type fakeEngine struct {
	delay    time.Duration
	failOn   string
	active   *atomic.Int32
	maxSeen  *atomic.Int32
	failures int32
}

func (f *fakeEngine) Info() engine.Info {
	return engine.Info{Backend: "fake", Device: "CPU", Detail: "test"}
}

func (f *fakeEngine) Process(ctx context.Context, req engine.Request) (engine.Result, error) {
	cur := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		old := f.maxSeen.Load()
		if cur <= old || f.maxSeen.CompareAndSwap(old, cur) {
			break
		}
	}
	if req.OnProgress != nil {
		req.OnProgress(engine.Progress{Done: 1, Total: 2})
		req.OnProgress(engine.Progress{Done: 2, Total: 2})
	}
	timer := time.NewTimer(f.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return engine.Result{}, fmt.Errorf("%w: %v", engine.ErrCancelled, ctx.Err())
	case <-timer.C:
	}
	if f.failOn != "" && filepath.Base(req.Input) == f.failOn {
		return engine.Result{}, engine.CodedError(engine.CodeInfer, "fake", "boom")
	}
	if err := os.WriteFile(req.Output, []byte("fake"), 0o644); err != nil {
		return engine.Result{}, err
	}
	return engine.Result{Output: req.Output, Tiles: 2, Width: 4, Height: 4, Backend: "fake", ElapsedMs: f.delay.Milliseconds()}, nil
}

func (f *fakeEngine) Close() error { return nil }

type fakeProvider struct {
	active   *atomic.Int32
	maxSeen  *atomic.Int32
	delay    time.Duration
	failOn   string
	mu       sync.Mutex
	closed   bool
	acquires int
}

func (p *fakeProvider) Info() engine.Info { return engine.Info{Backend: "fake"} }
func (p *fakeProvider) Acquire(context.Context) (engine.Engine, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, engine.CodedError(engine.CodeBackend, "fake", "closed")
	}
	p.acquires++
	p.mu.Unlock()
	return &fakeEngine{delay: p.delay, failOn: p.failOn, active: p.active, maxSeen: p.maxSeen}, nil
}
func (p *fakeProvider) Release(engine.Engine) {}
func (p *fakeProvider) Close() error {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return nil
}

type harness struct {
	active  atomic.Int32
	maxSeen atomic.Int32
	manager *Manager
	events  chan Event
	done    chan struct{}
}

func newHarness(t *testing.T, delay time.Duration, failOn string) *harness {
	t.Helper()
	h := &harness{}
	h.manager = New(Config{
		MaxConcurrency: 4,
		ProviderFactory: func(JobSpec) (engine.Provider, error) {
			return &fakeProvider{active: &h.active, maxSeen: &h.maxSeen, delay: delay, failOn: failOn}, nil
		},
	})
	h.events = make(chan Event, 2048)
	h.done = make(chan struct{})
	go func() {
		defer close(h.done)
		for ev := range h.manager.Events() {
			h.events <- ev
			if ev.Kind == "job-done" {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = h.manager.Close() })
	return h
}

func (h *harness) waitJob(t *testing.T, jobID string) Summary {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	sum, err := h.manager.Wait(ctx, jobID)
	if err != nil {
		t.Fatalf("wait job: %v", err)
	}
	return sum
}

func makePNGs(t *testing.T, n int) []string {
	t.Helper()
	dir := t.TempDir()
	files := make([]string, n)
	for i := 0; i < n; i++ {
		files[i] = filepath.Join(dir, fmt.Sprintf("img%02d.png", i))
		// Minimal PNG signature is enough: the scheduler only stats the path and
		// the fake engine ignores content. The native/fallback engines are tested
		// separately with real PNGs.
		if err := os.WriteFile(files[i], []byte("\x89PNG\r\n\x1a\nfake"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// ---- tests -----------------------------------------------------------------

func TestDynamicConcurrencyRespected(t *testing.T) {
	h := newHarness(t, 40*time.Millisecond, "")
	files := makePNGs(t, 12)
	res, err := h.manager.Submit(Job{Files: files, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Queued != 12 {
		t.Fatalf("queued = %d, want 12", res.Queued)
	}
	if err := h.manager.SetConcurrency(1); err != nil {
		t.Fatal(err)
	}
	// Let some tasks run at limit 1.
	time.Sleep(120 * time.Millisecond)
	if got := h.maxSeen.Load(); got > 1 {
		t.Fatalf("max concurrency %d exceeded limit 1", got)
	}
	if err := h.manager.SetConcurrency(3); err != nil {
		t.Fatal(err)
	}
	sum := h.waitJob(t, res.JobID)
	if sum.Completed != 12 || sum.Failed != 0 {
		t.Fatalf("summary = %+v", sum)
	}
	if got := h.maxSeen.Load(); got != 3 {
		t.Fatalf("max concurrency after resize = %d, want 3", got)
	}
}

func TestSetConcurrencyBounds(t *testing.T) {
	h := newHarness(t, time.Millisecond, "")
	if err := h.manager.SetConcurrency(0); err == nil {
		t.Fatal("expected error for 0")
	}
	if err := h.manager.SetConcurrency(engine.MaxConcurrency + 1); err == nil {
		t.Fatal("expected error above max")
	}
	if err := h.manager.SetConcurrency(2); err != nil {
		t.Fatal(err)
	}
	if h.manager.Concurrency() != 2 {
		t.Fatalf("concurrency = %d", h.manager.Concurrency())
	}
}

func TestCancelJobDropsQueuedAndStopsRunning(t *testing.T) {
	h := newHarness(t, 150*time.Millisecond, "")
	files := makePNGs(t, 8)
	res, err := h.manager.Submit(Job{Files: files, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.manager.SetConcurrency(2); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	start := time.Now()
	if !h.manager.CancelJob(res.JobID) {
		t.Fatal("cancel returned false")
	}
	sum := h.waitJob(t, res.JobID)
	elapsed := time.Since(start)
	if !sum.Cancelled {
		t.Fatalf("summary not cancelled: %+v", sum)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("cancellation took %v, should be fast", elapsed)
	}
	if sum.Completed > 2 {
		t.Fatalf("completed %d tasks after immediate cancel", sum.Completed)
	}
}

func TestFailureIsolatedToTask(t *testing.T) {
	h := newHarness(t, 5*time.Millisecond, "img01.png")
	files := makePNGs(t, 3)
	res, err := h.manager.Submit(Job{Files: files, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	sum := h.waitJob(t, res.JobID)
	if sum.Completed != 2 || sum.Failed != 1 {
		t.Fatalf("summary = %+v, want 2 completed 1 failed", sum)
	}
}

func TestSkippedAndOutputNaming(t *testing.T) {
	h := newHarness(t, time.Millisecond, "")
	files := makePNGs(t, 1)
	missing := filepath.Join(t.TempDir(), "nope.png")
	notImage := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(notImage, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dup := files[0]
	out := t.TempDir()
	res, err := h.manager.Submit(Job{Files: []string{files[0], dup, missing, notImage}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	if res.Queued != 1 || len(res.Skipped) != 3 {
		t.Fatalf("queued=%d skipped=%v", res.Queued, res.Skipped)
	}
	h.waitJob(t, res.JobID)

	// Second batch with the same input must not overwrite the first output.
	res2, err := h.manager.Submit(Job{Files: []string{files[0]}, OutputDir: out})
	if err != nil {
		t.Fatal(err)
	}
	h.waitJob(t, res2.JobID)
	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 distinct outputs, got %d", len(entries))
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.Name()] {
			t.Fatalf("duplicate output name %s", e.Name())
		}
		seen[e.Name()] = true
	}
}

func TestCancelUnknownJob(t *testing.T) {
	h := newHarness(t, time.Millisecond, "")
	if h.manager.CancelJob("nope") {
		t.Fatal("cancel unknown returned true")
	}
	if _, err := h.manager.Wait(context.Background(), "nope"); err == nil {
		t.Fatal("wait unknown should fail")
	}
}

func TestEventsProgressSequence(t *testing.T) {
	h := newHarness(t, 5*time.Millisecond, "")
	files := makePNGs(t, 2)
	res, err := h.manager.Submit(Job{Files: files, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	h.waitJob(t, res.JobID)

	var started, progress, done int
	var lastKind string
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev := <-h.events:
			if ev.Kind == "job-done" {
				if started != 2 || done != 2 || progress == 0 {
					t.Fatalf("started=%d progress=%d done=%d last=%s", started, progress, done, lastKind)
				}
				return
			}
			lastKind = ev.Kind
			switch ev.Kind {
			case "started":
				started++
			case "progress":
				progress++
			case "done":
				done++
			}
		case <-timeout:
			t.Fatalf("timeout, last kind %q", lastKind)
		}
	}
}
