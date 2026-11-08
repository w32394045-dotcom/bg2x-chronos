//go:build windows

package inhibit

import (
	"runtime"
	"sync"
	"syscall"
	"time"
)

// Windows has no supported API to block an explicit Win+L, but keeping the
// display awake prevents the idle screensaver / inactivity lock path. This is
// exactly what ES_DISPLAY_REQUIRED does.
const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
	refreshInterval   = 30 * time.Second
)

var (
	kernel32                   = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
)

type windowsDriver struct {
	mu   sync.Mutex
	stop chan struct{}
	done chan struct{}
}

func platformDriver() Driver { return &windowsDriver{} }

func (d *windowsDriver) Name() string { return "windows-execution-state" }

func (d *windowsDriver) Detail() string {
	return "Windows: SetThreadExecutionState(ES_CONTINUOUS|ES_SYSTEM_REQUIRED|ES_DISPLAY_REQUIRED) 保持系统与屏幕常亮，" +
		"同时抑制空闲自动锁屏；手动 Win+L 属于系统级操作，任何应用都无法阻止。"
}

func (d *windowsDriver) Capabilities() Capabilities {
	return Capabilities{Sleep: true, Display: true, Lock: true}
}

func (d *windowsDriver) Start(opts Options) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stop != nil {
		return nil
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	d.stop, d.done = stop, done
	go d.hold(opts, stop, done)
	return nil
}

// hold runs on a dedicated, locked OS thread: SetThreadExecutionState is
// thread-scoped, so the thread must not migrate while the inhibitor is active.
func (d *windowsDriver) hold(opts Options, stop <-chan struct{}, done chan<- struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(done)

	flags := uintptr(esContinuous)
	if opts.Sleep {
		flags |= esSystemRequired
	}
	if opts.Display || opts.Lock {
		flags |= esDisplayRequired
	}
	setState(flags)
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			// ES_CONTINUOUS keeps the previous requirement until we clear it.
			setState(esContinuous)
			return
		case <-ticker.C:
			setState(flags)
		}
	}
}

func (d *windowsDriver) Stop() error {
	d.mu.Lock()
	stop, done := d.stop, d.done
	d.stop, d.done = nil, nil
	d.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
	return nil
}

func setState(flags uintptr) {
	r1, _, _ := procSetThreadExecutionState.Call(flags)
	_ = r1
}
