//go:build linux

package inhibit

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/godbus/dbus/v5"
)

// Linux uses two independent inhibitors:
//   - systemd-logind via `systemd-inhibit` (idle + sleep + lid switch) stops the
//     machine from sleeping and the display from powering off.
//   - the desktop session's idle/lock inhibitor stops screen locking. Both
//     org.freedesktop.ScreenSaver (GNOME/KDE/XFCE) and GNOME's
//     org.gnome.SessionManager are tried; the first that works is kept.
//
// Headless sessions (SSH, CI) have no session bus, so lock inhibition is
// reported as unsupported there while sleep/display inhibition still works.
type linuxDriver struct {
	mu       sync.Mutex
	inhibit  *exec.Cmd
	bus      *dbus.Conn
	lockKind string // "screen-saver" | "gnome-session" | ""
	cookie   uint32
	lastErrs []string
}

func platformDriver() Driver { return &linuxDriver{} }

func (d *linuxDriver) Name() string { return "linux-logind+screen-saver" }

func (d *linuxDriver) Detail() string {
	return "Linux: systemd-logind Inhibit(sleep:idle:handle-lid-switch) 阻止睡眠与熄屏；" +
		"锁屏抑制依次尝试 org.freedesktop.ScreenSaver 与 org.gnome.SessionManager；" +
		"无 systemd 或无会话 D-Bus（SSH/CI）时自动降级并如实上报能力。"
}

func (d *linuxDriver) Capabilities() Capabilities {
	caps := Capabilities{}
	if _, err := exec.LookPath("systemd-inhibit"); err == nil {
		caps.Sleep = true
		caps.Display = true
	}
	caps.Lock = lockInhibitAvailable()
	return caps
}

func (d *linuxDriver) Start(opts Options) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if opts.Sleep || opts.Display {
		if path, err := exec.LookPath("systemd-inhibit"); err != nil {
			d.lastErrs = append(d.lastErrs, "systemd-inhibit not found")
		} else {
			reason := strings.TrimSpace(opts.Reason)
			if reason == "" {
				reason = "Chronos is upscaling images"
			}
			cmd := exec.Command(path,
				"--what=sleep:idle:handle-lid-switch",
				"--who=Chronos",
				"--why="+reason,
				"--mode=block",
				"sleep", "infinity",
			)
			// Put the inhibitor in its own process group so Stop can take down
			// the wrapped sleep as well.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			if err := cmd.Start(); err != nil {
				d.lastErrs = append(d.lastErrs, "start systemd-inhibit: "+err.Error())
			} else {
				d.inhibit = cmd
			}
		}
	}

	if opts.Lock {
		if kind, cookie, conn, err := inhibitSessionLock(); err != nil {
			d.lastErrs = append(d.lastErrs, "lock inhibit: "+err.Error())
		} else {
			d.bus, d.cookie, d.lockKind = conn, cookie, kind
		}
	}

	if d.inhibit == nil && d.bus == nil {
		if len(d.lastErrs) == 0 {
			return ErrUnsupported
		}
		return fmt.Errorf("%s", strings.Join(d.lastErrs, "; "))
	}
	return nil
}

func (d *linuxDriver) Stop() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var errs []string
	if d.inhibit != nil {
		if d.inhibit.Process != nil {
			// Negative pid targets the process group created above.
			_ = syscall.Kill(-d.inhibit.Process.Pid, syscall.SIGTERM)
		}
		_ = d.inhibit.Wait()
		d.inhibit = nil
	}
	if d.bus != nil {
		if err := releaseSessionLock(d.bus, d.lockKind, d.cookie); err != nil {
			errs = append(errs, "release lock inhibit: "+err.Error())
		}
		_ = d.bus.Close()
		d.bus = nil
		d.cookie = 0
		d.lockKind = ""
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

// lockInhibitAvailable probes the session bus for a usable lock inhibitor.
func lockInhibitAvailable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	defer conn.Close()
	for _, name := range []string{"org.freedesktop.ScreenSaver", "org.gnome.SessionManager"} {
		var has bool
		if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, name).Store(&has); err != nil {
			continue
		}
		if has {
			return true
		}
	}
	return false
}

func inhibitSessionLock() (kind string, cookie uint32, conn *dbus.Conn, err error) {
	conn, err = dbus.SessionBus()
	if err != nil {
		return "", 0, nil, err
	}
	// Order matters: ScreenSaver covers GNOME/KDE/XFCE; GNOME SessionManager is
	// the fallback for sessions without the ScreenSaver service.
	if e := conn.Object("org.freedesktop.ScreenSaver", "/org/freedesktop/ScreenSaver").
		Call("org.freedesktop.ScreenSaver.Inhibit", 0, "Chronos", "Image upscaling in progress").Store(&cookie); e == nil {
		return "screen-saver", cookie, conn, nil
	}
	// GNOME SessionManager: Inhibit(app, reason, toplevel, flags); flags=8 (idle)
	// suppresses idle detection, which is what triggers the lock screen.
	if e := conn.Object("org.gnome.SessionManager", "/org/gnome/SessionManager").
		Call("org.gnome.SessionManager.Inhibit", 0, "Chronos", "Image upscaling in progress", uint32(0), uint32(8)).
		Store(&cookie); e == nil {
		return "gnome-session", cookie, conn, nil
	}
	_ = conn.Close()
	return "", 0, nil, fmt.Errorf("no ScreenSaver/SessionManager lock inhibitor available")
}

func releaseSessionLock(conn *dbus.Conn, kind string, cookie uint32) error {
	switch kind {
	case "screen-saver":
		return conn.Object("org.freedesktop.ScreenSaver", "/org/freedesktop/ScreenSaver").
			Call("org.freedesktop.ScreenSaver.UnInhibit", 0, cookie).Err
	case "gnome-session":
		return conn.Object("org.gnome.SessionManager", "/org/gnome/SessionManager").
			Call("org.gnome.SessionManager.Uninhibit", 0, cookie).Err
	}
	return nil
}
