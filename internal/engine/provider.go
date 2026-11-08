package engine

import (
	"context"
	"fmt"
	"sync"
)

// NewProvider validates the config by opening one engine (which also probes the
// native library), then pools up to max engines for concurrent workers.
func NewProvider(cfg Config, max int) (Provider, error) {
	cfg = cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if max < 1 {
		max = 1
	}
	if max > MaxConcurrency {
		max = MaxConcurrency
	}
	first, err := newEngine(cfg)
	if err != nil {
		return nil, err
	}
	return &pool{
		cfg:  cfg,
		max:  max,
		idle: []Engine{first},
		info: first.Info(),
		cond: sync.NewCond(&sync.Mutex{}),
	}, nil
}

// newEngine prefers the native core and falls back to pure Go only when no
// model was requested. A requested model without a native core is a hard error:
// the Go fallback cannot run ONNX/IR inference.
func newEngine(cfg Config) (Engine, error) {
	e, err := newNativeEngine(cfg)
	if err == nil {
		return e, nil
	}
	if cfg.ModelPath != "" {
		return nil, fmt.Errorf("%w: model %q requires the native OpenVINO core: %v", ErrModel, cfg.ModelPath, err)
	}
	return newFallbackEngine(cfg), nil
}

type pool struct {
	cfg Config

	mu     sync.Mutex
	cond   *sync.Cond
	idle   []Engine
	open   int
	max    int
	info   Info
	closed bool
}

func (p *pool) Info() Info { return p.info }

func (p *pool) Acquire(ctx context.Context) (Engine, error) {
	for {
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return nil, CodedError(CodeBackend, "acquire", "provider closed")
		}
		if n := len(p.idle); n > 0 {
			e := p.idle[n-1]
			p.idle = p.idle[:n-1]
			p.mu.Unlock()
			return e, nil
		}
		if p.open < p.max {
			p.open++
			p.mu.Unlock()
			e, err := newEngine(p.cfg)
			if err != nil {
				p.mu.Lock()
				p.open--
				p.cond.Broadcast()
				p.mu.Unlock()
				return nil, err
			}
			return e, nil
		}
		p.mu.Unlock()
		// Wait for a release, but respect cancellation.
		done := make(chan struct{})
		go func() {
			p.mu.Lock()
			p.cond.Wait()
			p.mu.Unlock()
			close(done)
		}()
		select {
		case <-ctx.Done():
			// The helper goroutine above may consume a later Broadcast; that is
			// harmless because it only holds/releases the lock briefly.
			return nil, fmt.Errorf("%w: waiting for engine: %v", ErrCancelled, ctx.Err())
		case <-done:
		}
	}
}

func (p *pool) Release(e Engine) {
	if e == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = e.Close()
		return
	}
	p.idle = append(p.idle, e)
	p.cond.Broadcast()
	p.mu.Unlock()
}

func (p *pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.open -= len(idle)
	p.cond.Broadcast()
	p.mu.Unlock()
	for _, e := range idle {
		_ = e.Close()
	}
	return nil
}
