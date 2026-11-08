package inhibit

import (
	"errors"
	"sync/atomic"
	"testing"
)

type fakeDriver struct {
	caps      Capabilities
	name      string
	detail    string
	startErr  error
	stopErr   error
	starts    int32
	stops     int32
	lastOpts  Options
	panicNext bool
}

func (f *fakeDriver) Name() string {
	if f.name == "" {
		return "fake"
	}
	return f.name
}
func (f *fakeDriver) Detail() string          { return f.detail }
func (f *fakeDriver) Capabilities() Capabilities { return f.caps }
func (f *fakeDriver) Start(o Options) error {
	if f.panicNext {
		panic("driver exploded")
	}
	atomic.AddInt32(&f.starts, 1)
	f.lastOpts = o
	return f.startErr
}
func (f *fakeDriver) Stop() error {
	atomic.AddInt32(&f.stops, 1)
	return f.stopErr
}

func newFake(caps Capabilities) *fakeDriver {
	return &fakeDriver{caps: caps}
}

func TestSetAndClear(t *testing.T) {
	d := newFake(Capabilities{Sleep: true, Display: true, Lock: true})
	m := newWithDriver(d)
	if st := m.Status(); st.Active || st.SinceMs != 0 {
		t.Fatalf("initial status = %+v", st)
	}
	st, err := m.Set(Options{Sleep: true, Display: true, Lock: true})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Active || !st.Sleep || !st.Display || !st.Lock {
		t.Fatalf("status = %+v", st)
	}
	if st.SinceMs == 0 {
		t.Fatal("sinceMs should be set while active")
	}
	if st.Driver != "fake" {
		t.Fatalf("driver = %q", st.Driver)
	}
	m.Set(Options{})
	if st = m.Status(); st.Active || st.Sleep || st.Display || st.Lock || st.SinceMs != 0 {
		t.Fatalf("after clear = %+v", st)
	}
	if atomic.LoadInt32(&d.starts) != 1 || atomic.LoadInt32(&d.stops) != 1 {
		t.Fatalf("starts=%d stops=%d", d.starts, d.stops)
	}
}

func TestUnsupportedCapabilitiesAreReported(t *testing.T) {
	d := newFake(Capabilities{Sleep: true, Display: false, Lock: false})
	m := newWithDriver(d)
	st, err := m.Set(Options{Sleep: true, Display: true, Lock: true})
	if err != nil {
		t.Fatal(err)
	}
	if !st.Active || !st.Sleep || st.Display || st.Lock {
		t.Fatalf("status = %+v", st)
	}
	if st.DisplaySupported || st.LockSupported {
		t.Fatalf("capabilities = %+v", st)
	}
}

func TestDriverFailureSurfaces(t *testing.T) {
	d := newFake(Capabilities{Sleep: true, Display: true, Lock: true})
	d.startErr = errors.New("no dbus")
	m := newWithDriver(d)
	st, err := m.Set(Options{Sleep: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if st.Active {
		t.Fatalf("status should be inactive: %+v", st)
	}
	if st.Detail == "" {
		t.Fatal("detail should explain the failure")
	}
}

func TestDriverPanicIsContained(t *testing.T) {
	d := newFake(Capabilities{Sleep: true})
	d.panicNext = true
	m := newWithDriver(d)
	if _, err := m.Set(Options{Sleep: true}); err == nil {
		t.Fatal("panic should become an error")
	}
}

func TestRestartUsesLatestOptions(t *testing.T) {
	d := newFake(Capabilities{Sleep: true, Display: true, Lock: true})
	m := newWithDriver(d)
	if _, err := m.Set(Options{Sleep: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Set(Options{Display: true, Lock: true}); err != nil {
		t.Fatal(err)
	}
	if d.lastOpts.Sleep || !d.lastOpts.Display || !d.lastOpts.Lock {
		t.Fatalf("driver did not receive latest options: %+v", d.lastOpts)
	}
	// One stop for the restart; Close adds the second.
	if atomic.LoadInt32(&d.starts) != 2 || atomic.LoadInt32(&d.stops) != 1 {
		t.Fatalf("starts=%d stops=%d", d.starts, d.stops)
	}
	m.Close()
	if atomic.LoadInt32(&d.stops) != 2 {
		t.Fatalf("Close did not release: stops=%d", d.stops)
	}
}

func TestNilDriverIsUnsupported(t *testing.T) {
	m := newWithDriver(nil)
	if _, err := m.Set(Options{Sleep: true}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v", err)
	}
	m.Close()
}

func TestPlatformManagerSmoke(t *testing.T) {
	// On any platform the manager must be constructible and report a driver and
	// detail text without panicking (this is what `chronos inhibit status` uses).
	m := New()
	defer m.Close()
	st := m.Status()
	if st.Driver == "" {
		t.Fatal("driver must not be empty")
	}
	if st.Detail == "" {
		t.Fatal("detail must not be empty")
	}
}
