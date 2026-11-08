package scheduler

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/engine"
)

// DefaultConcurrency is used until the UI moves the slider.
const DefaultConcurrency = 4

// JobSpec is the model + tiling configuration for a batch. One batch uses one
// engine provider; switching models means a new batch (and a new model load).
type JobSpec struct {
	ModelPath string
	Scale     int
	TileSize  int
	Overlap   int
	Threads   int
	Fast      bool
}

// Job is one batch submission.
type Job struct {
	Files     []string
	OutputDir string // empty => write next to each input
	Spec      JobSpec
}

// BatchResult is returned by Submit.
type BatchResult struct {
	JobID   string   `json:"jobId"`
	Queued  int      `json:"queued"`
	Skipped []string `json:"skipped"`
}

// Event mirrors the frontend ProgressEvent / job summary (docs/CONTRACTS.md §3.3).
type Event struct {
	JobID     string `json:"jobId"`
	TaskID    string `json:"taskId"`
	File      string `json:"file"`
	Kind      string `json:"kind"` // queued|started|progress|done|failed|cancelled|job-done
	Index     int    `json:"index"`
	Done      int    `json:"done"`
	Total     int    `json:"total"`
	Output    string `json:"output"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Error     string `json:"error"`
	Backend   string `json:"backend"`
	ElapsedMs int64  `json:"elapsedMs"`

	// job-done summary fields
	Completed int  `json:"completed"`
	Failed    int  `json:"failed"`
	Cancelled bool `json:"cancelled"`
}

// Summary is the terminal state of one batch.
type Summary struct {
	JobID     string
	Total     int
	Completed int
	Failed    int
	Cancelled bool
	ElapsedMs int64
}

// Config wires the manager to an engine factory.
type Config struct {
	MaxConcurrency  int
	ProviderFactory func(spec JobSpec) (engine.Provider, error)
}

type taskState struct {
	job   *jobState
	id    string
	index int
	file  string
	out   string
}

type jobState struct {
	id       string
	spec     JobSpec
	provider engine.Provider
	ctx      context.Context
	cancel   context.CancelFunc
	started  time.Time
	done     chan struct{}

	mu        sync.Mutex
	total     int
	pending   int
	completed int
	failed    int
	cancelledCount int
	cancelled bool
	sumSent   bool
}

// Manager owns the dynamic worker-pool: a fixed number of goroutines is not
// enough because the UI slider must shrink/grow the active batch live, so a
// dispatcher starts up to `limit` concurrent tasks and parks the rest.
type Manager struct {
	cfg Config

	mu        sync.Mutex
	cond      *sync.Cond
	limit     int
	running   int
	pending   []*taskState
	jobs      map[string]*jobState
	order     []string
	reserved  map[string]struct{}
	events    chan Event
	closed    chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
	seq       atomic.Int64

	dispatched chan struct{}
}

// New starts the dispatcher.
func New(cfg Config) *Manager {
	if cfg.MaxConcurrency < 1 {
		cfg.MaxConcurrency = DefaultConcurrency
	}
	if cfg.MaxConcurrency > engine.MaxConcurrency {
		cfg.MaxConcurrency = engine.MaxConcurrency
	}
	m := &Manager{
		cfg:        cfg,
		limit:      cfg.MaxConcurrency,
		jobs:       make(map[string]*jobState),
		reserved:   make(map[string]struct{}),
		events:     make(chan Event, 4096),
		closed:     make(chan struct{}),
		dispatched: make(chan struct{}),
	}
	m.cond = sync.NewCond(&m.mu)
	go m.dispatch()
	return m
}

// Events is the stream consumed by the app layer. It is never closed; consumers
// stop when the Manager is closed and drained.
func (m *Manager) Events() <-chan Event { return m.events }

// Submit validates, reserves output names, and enqueues a batch.
func (m *Manager) Submit(job Job) (BatchResult, error) {
	if m.isClosed() {
		return BatchResult{}, errors.New("chronos: scheduler closed")
	}
	if len(job.Files) == 0 {
		return BatchResult{}, fmt.Errorf("%w: no input files", engine.ErrParam)
	}
	if m.cfg.ProviderFactory == nil {
		return BatchResult{}, fmt.Errorf("%w: no provider factory", engine.ErrParam)
	}
	provider, err := m.cfg.ProviderFactory(job.Spec)
	if err != nil {
		return BatchResult{}, err
	}

	jobID := fmt.Sprintf("job-%d-%d", time.Now().UnixMilli(), m.seq.Add(1))
	seen := make(map[string]struct{}, len(job.Files))
	tasks := make([]*taskState, 0, len(job.Files))
	skipped := make([]string, 0)

	for _, raw := range job.Files {
		file := filepath.Clean(raw)
		if _, dup := seen[file]; dup {
			skipped = append(skipped, raw)
			continue
		}
		seen[file] = struct{}{}
		st, statErr := os.Stat(file)
		if statErr != nil || st.IsDir() {
			skipped = append(skipped, raw)
			continue
		}
		if !isImageFile(file) {
			skipped = append(skipped, raw)
			continue
		}
		dir := job.OutputDir
		if dir == "" {
			dir = filepath.Dir(file)
		}
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			_ = provider.Close()
			return BatchResult{}, fmt.Errorf("%w: create output dir: %v", engine.ErrIO, mkErr)
		}
		tasks = append(tasks, &taskState{
			file: file,
			out:  m.reserveOutput(dir, file, job.Spec.Scale),
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	j := &jobState{
		id:       jobID,
		spec:     job.Spec,
		provider: provider,
		ctx:      ctx,
		cancel:   cancel,
		started:  time.Now(),
		done:     make(chan struct{}),
		total:    len(tasks),
		pending:  len(tasks),
	}
	for i, t := range tasks {
		t.job = j
		t.id = fmt.Sprintf("%s-%d", jobID, i)
		t.index = i
	}

	if len(tasks) > 0 {
		m.mu.Lock()
		m.jobs[jobID] = j
		m.order = append(m.order, jobID)
		m.pending = append(m.pending, tasks...)
		m.mu.Unlock()
		m.cond.Broadcast()
	} else {
		m.mu.Lock()
		m.jobs[jobID] = j
		m.order = append(m.order, jobID)
		m.mu.Unlock()
		m.finishJob(j)
	}

	return BatchResult{JobID: jobID, Queued: len(tasks), Skipped: skipped}, nil
}

// SetConcurrency changes the live concurrency limit (1..engine.MaxConcurrency).
func (m *Manager) SetConcurrency(n int) error {
	if n < 1 || n > engine.MaxConcurrency {
		return fmt.Errorf("%w: concurrency %d outside 1..%d", engine.ErrParam, n, engine.MaxConcurrency)
	}
	m.mu.Lock()
	m.limit = n
	m.mu.Unlock()
	m.cond.Broadcast()
	return nil
}

// Concurrency reports the current limit.
func (m *Manager) Concurrency() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.limit
}

// Running reports how many tasks are executing right now (diagnostics/tests).
func (m *Manager) Running() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// CancelJob cancels a batch: queued tasks are dropped, running tasks observe
// their context and return engine.ErrCancelled.
func (m *Manager) CancelJob(jobID string) bool {
	m.mu.Lock()
	j, ok := m.jobs[jobID]
	m.mu.Unlock()
	if !ok {
		return false
	}
	j.mu.Lock()
	if j.cancelled || j.sumSent {
		j.mu.Unlock()
		return false
	}
	j.cancelled = true
	j.mu.Unlock()
	j.cancel()

	m.mu.Lock()
	kept := m.pending[:0]
	var dropped []*taskState
	for _, t := range m.pending {
		if t.job == j {
			dropped = append(dropped, t)
			continue
		}
		kept = append(kept, t)
	}
	m.pending = kept
	m.mu.Unlock()

	j.mu.Lock()
	j.cancelledCount += len(dropped)
	j.mu.Unlock()
	for _, t := range dropped {
		m.emit(Event{JobID: j.id, TaskID: t.id, File: t.file, Kind: "cancelled", Index: t.index})
		m.taskDone(t)
	}
	m.maybeFinish(j)
	return true
}

// Wait blocks until the batch is finished or ctx is done.
func (m *Manager) Wait(ctx context.Context, jobID string) (Summary, error) {
	m.mu.Lock()
	j, ok := m.jobs[jobID]
	m.mu.Unlock()
	if !ok {
		return Summary{}, fmt.Errorf("chronos: unknown job %q", jobID)
	}
	select {
	case <-j.done:
	case <-ctx.Done():
		return Summary{}, ctx.Err()
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return Summary{
		JobID:     j.id,
		Total:     j.total,
		Completed: j.completed,
		Failed:    j.failed,
		Cancelled: j.cancelled,
		ElapsedMs: time.Since(j.started).Milliseconds(),
	}, nil
}

// Close cancels everything and waits for in-flight tasks.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		close(m.closed)
		m.cond.Broadcast()
		m.mu.Lock()
		jobs := make([]*jobState, 0, len(m.jobs))
		for _, j := range m.jobs {
			jobs = append(jobs, j)
		}
		m.mu.Unlock()
		for _, j := range jobs {
			j.cancel()
		}
		<-m.dispatched
		m.wg.Wait()
		m.mu.Lock()
		for _, j := range m.jobs {
			_ = j.provider.Close()
		}
		m.mu.Unlock()
	})
	return nil
}

func (m *Manager) dispatch() {
	defer close(m.dispatched)
	for {
		m.mu.Lock()
		for {
			if m.isClosedLocked() {
				m.mu.Unlock()
				return
			}
			if len(m.pending) > 0 && m.running < m.limit {
				break
			}
			m.cond.Wait()
		}
		t := m.pending[0]
		m.pending = m.pending[1:]
		m.running++
		m.wg.Add(1)
		m.mu.Unlock()
		go m.runTask(t)
	}
}

func (m *Manager) runTask(t *taskState) {
	defer m.wg.Done()
	job := t.job
	if job.ctx.Err() != nil {
		m.emit(Event{JobID: job.id, TaskID: t.id, File: t.file, Kind: "cancelled", Index: t.index})
		job.mu.Lock()
		job.cancelledCount++
		job.mu.Unlock()
		m.taskDone(t)
		return
	}
	m.emit(Event{JobID: job.id, TaskID: t.id, File: t.file, Kind: "started", Index: t.index})
	started := time.Now()

	eng, err := job.provider.Acquire(job.ctx)
	if err != nil {
		m.failTask(t, err, 0)
		return
	}
	defer job.provider.Release(eng)

	var last atomic.Int64
	// Allow the first progress event immediately, then throttle to 150ms.
	last.Store(time.Now().Add(-time.Second).UnixMilli())
	res, err := eng.Process(job.ctx, engine.Request{
		Input:  t.file,
		Output: t.out,
		OnProgress: func(p engine.Progress) {
			now := time.Now().UnixMilli()
			if now-last.Load() < 150 {
				return
			}
			last.Store(now)
			m.emit(Event{JobID: job.id, TaskID: t.id, File: t.file, Kind: "progress", Index: t.index, Done: p.Done, Total: p.Total})
		},
	})
	if err != nil {
		if errors.Is(err, engine.ErrCancelled) || job.ctx.Err() != nil {
			m.emit(Event{JobID: job.id, TaskID: t.id, File: t.file, Kind: "cancelled", Index: t.index})
			job.mu.Lock()
			job.cancelledCount++
			job.mu.Unlock()
			m.taskDone(t)
			return
		}
		m.failTask(t, err, time.Since(started).Milliseconds())
		return
	}
	m.emit(Event{
		JobID:     job.id,
		TaskID:    t.id,
		File:      t.file,
		Kind:      "done",
		Index:     t.index,
		Done:      res.Tiles,
		Total:     res.Tiles,
		Output:    res.Output,
		Width:     res.Width,
		Height:    res.Height,
		Backend:   res.Backend,
		ElapsedMs: res.ElapsedMs,
	})
	job.mu.Lock()
	job.completed++
	job.mu.Unlock()
	m.taskDone(t)
}

func (m *Manager) failTask(t *taskState, err error, elapsedMs int64) {
	msg := err.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	m.emit(Event{JobID: t.job.id, TaskID: t.id, File: t.file, Kind: "failed", Index: t.index, Error: msg, ElapsedMs: elapsedMs})
	t.job.mu.Lock()
	t.job.failed++
	t.job.mu.Unlock()
	m.taskDone(t)
}

func (m *Manager) taskDone(t *taskState) {
	m.mu.Lock()
	if m.running > 0 {
		m.running--
	}
	m.mu.Unlock()
	m.cond.Signal()

	j := t.job
	j.mu.Lock()
	if j.pending > 0 {
		j.pending--
	}
	left := j.pending
	j.mu.Unlock()
	if left == 0 {
		m.finishJob(j)
	}
}

func (m *Manager) maybeFinish(j *jobState) {
	j.mu.Lock()
	left := j.pending
	j.mu.Unlock()
	if left == 0 {
		m.finishJob(j)
	}
}

func (m *Manager) finishJob(j *jobState) {
	j.mu.Lock()
	if j.sumSent {
		j.mu.Unlock()
		return
	}
	j.sumSent = true
	completed, failed := j.completed, j.failed
	cancelledTasks := j.cancelledCount
	cancelled := j.cancelled
	j.mu.Unlock()
	_ = cancelledTasks

	_ = j.provider.Close()
	j.cancel()
	m.emit(Event{
		JobID:     j.id,
		Kind:      "job-done",
		Completed: completed,
		Failed:    failed,
		Cancelled: cancelled,
		Total:     completed + failed + cancelledTasks,
		ElapsedMs: time.Since(j.started).Milliseconds(),
	})
	close(j.done)
}

func (m *Manager) emit(ev Event) {
	select {
	case m.events <- ev:
	case <-m.closed:
	}
}

func (m *Manager) isClosed() bool {
	select {
	case <-m.closed:
		return true
	default:
		return false
	}
}

func (m *Manager) isClosedLocked() bool {
	select {
	case <-m.closed:
		return true
	default:
		return false
	}
}

// reserveOutput picks a collision-free output path and pins it for the process
// lifetime so two jobs never write the same file.
func (m *Manager) reserveOutput(dir, input string, scale int) string {
	base := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	base = sanitizeBase(base)
	if scale < 1 {
		scale = 1
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := 0; i < 1000; i++ {
		name := fmt.Sprintf("%s_chronos_x%d.png", base, scale)
		if i > 0 {
			name = fmt.Sprintf("%s_chronos_x%d-%d.png", base, scale, i)
		}
		full := filepath.Join(dir, name)
		if _, pinned := m.reserved[full]; pinned {
			continue
		}
		if _, err := os.Stat(full); err == nil {
			continue
		}
		m.reserved[full] = struct{}{}
		return full
	}
	return filepath.Join(dir, fmt.Sprintf("%s_chronos_x%d_%d.png", base, scale, time.Now().UnixNano()))
}

func sanitizeBase(s string) string {
	if s == "" {
		return "image"
	}
	repl := func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			return '_'
		}
		return r
	}
	out := strings.Map(repl, s)
	out = strings.TrimSpace(out)
	if out == "" {
		out = "image"
	}
	return out
}

func isImageFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png", ".jpg", ".jpeg", ".bmp", ".gif", ".webp", ".tif", ".tiff":
		return true
	}
	return false
}
