// Package inhibit keeps the machine awake (and, where the platform allows it,
// keeps the session unlocked) while a long batch is running.
//
// The manager is a small state machine around a platform Driver so it can be
// tested everywhere; the OS specifics live in driver_*.go behind build tags.
package inhibit

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrUnsupported is returned when the platform cannot inhibit anything.
var ErrUnsupported = errors.New("chronos: sleep/lock inhibition not supported on this platform")

// Options is what the user asked for. AutoRelease is consumed by the app layer.
type Options struct {
	Sleep       bool   `json:"sleep"`
	Display     bool   `json:"display"`
	Lock        bool   `json:"lock"`
	AutoRelease bool   `json:"autoRelease"`
	Reason      string `json:"reason,omitempty"`
}

// Requested reports whether any inhibition bit is set.
func (o Options) Requested() bool { return o.Sleep || o.Display || o.Lock }

// Capabilities is what the current platform can actually do.
type Capabilities struct {
	Sleep   bool
	Display bool
	Lock    bool
}

// Driver is implemented per OS. Start must be idempotent enough for the manager
// (which always calls Stop before restarting with new options).
type Driver interface {
	Name() string
	Capabilities() Capabilities
	Start(Options) error
	Stop() error
}

// detailer optionally explains platform caveats to the UI.
type detailer interface{ Detail() string }

// Status is the JSON payload sent to the frontend (docs/CONTRACTS.md §3.2).
type Status struct {
	Active           bool   `json:"active"`
	Sleep            bool   `json:"sleep"`
	Display          bool   `json:"display"`
	Lock             bool   `json:"lock"`
	SleepSupported   bool   `json:"sleepSupported"`
	DisplaySupported bool   `json:"displaySupported"`
	LockSupported    bool   `json:"lockSupported"`
	Driver           string `json:"driver"`
	Detail           string `json:"detail"`
	SinceMs          int64  `json:"sinceMs"`
}

// Manager serializes driver transitions.
type Manager struct {
	mu     sync.Mutex
	driver Driver
	status Status
	opts   Options
	since  time.Time
}

// New builds a manager backed by the current platform driver.
func New() *Manager { return newWithDriver(platformDriver()) }

func newWithDriver(d Driver) *Manager {
	m := &Manager{driver: d}
	if d == nil {
		m.status = Status{Driver: "unsupported", Detail: "no platform driver"}
		return m
	}
	caps := d.Capabilities()
	m.status = m.idleStatus(caps)
	return m
}

// Set applies new inhibition options atomically.
func (m *Manager) Set(opts Options) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.driver == nil {
		return m.status, ErrUnsupported
	}
	if !opts.Requested() {
		m.stopLocked()
		return m.currentLocked(), nil
	}
	// Restart with the new mask so a changed toggle takes effect immediately.
	m.stopLocked()
	if err := safeStart(m.driver, opts); err != nil {
		m.status = m.idleStatus(m.driver.Capabilities())
		m.status.Detail = m.status.Detail + " | 启用失败: " + err.Error()
		return m.currentLocked(), fmt.Errorf("chronos: enable inhibition: %w", err)
	}
	caps := m.driver.Capabilities()
	m.opts = opts
	m.since = time.Now()
	m.status = Status{
		Active:           true,
		Sleep:            opts.Sleep && caps.Sleep,
		Display:          opts.Display && caps.Display,
		Lock:             opts.Lock && caps.Lock,
		SleepSupported:   caps.Sleep,
		DisplaySupported: caps.Display,
		LockSupported:    caps.Lock,
		Driver:           m.driver.Name(),
		Detail:           driverDetail(m.driver),
	}
	return m.currentLocked(), nil
}

// Status returns the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentLocked()
}

// Close releases the platform inhibitor. Safe to call twice.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopLocked()
	return nil
}

func (m *Manager) currentLocked() Status {
	s := m.status
	if s.Active && !m.since.IsZero() {
		s.SinceMs = m.since.UnixMilli()
	}
	return s
}

func (m *Manager) idleStatus(caps Capabilities) Status {
	return Status{
		SleepSupported:   caps.Sleep,
		DisplaySupported: caps.Display,
		LockSupported:    caps.Lock,
		Driver:           m.driver.Name(),
		Detail:           driverDetail(m.driver),
	}
}

func (m *Manager) stopLocked() {
	if m.driver == nil || !m.status.Active {
		m.status.Active = false
		m.status.Sleep = false
		m.status.Display = false
		m.status.Lock = false
		m.status.SinceMs = 0
		return
	}
	_ = safeStop(m.driver)
	caps := m.driver.Capabilities()
	m.status = m.idleStatus(caps)
	m.since = time.Time{}
}

func driverDetail(d Driver) string {
	if dd, ok := d.(detailer); ok {
		return dd.Detail()
	}
	return d.Name()
}

func safeStart(d Driver, opts Options) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("driver panic: %v", r)
		}
	}()
	return d.Start(opts)
}

func safeStop(d Driver) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("driver panic: %v", r)
		}
	}()
	return d.Stop()
}
