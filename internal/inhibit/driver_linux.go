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
//   - org.freedesktop.ScreenSaver.Inhibit (session bus) stops screen locking on
//     GNOME/KDE/XFCE and other freedesktop-compliant desktops.
type linuxDriver struct {
	mu       sync.Mutex
	inhibit  *exec.Cmd
	bus      *dbus.Conn
	cookie   uint32
	lastErrs []string
}

func platformDriver() Driver { return &linuxDriver{} }

func (d *linuxDriver) Name() string { return "linux-logind+screen-saver" }

func (d *linuxDriver) Detail() string {
	return "Linux: systemd-logind Inhibit(sleep:idle:handle-lid-switch) 阻止睡眠与熄屏，" +
		"org.freedesktop.ScreenSaver.Inhibit 阻止空闲锁屏；无 systemd 或无会话 D-Bus 时自动降级。"
}

func (d *linuxDriver) Capabilities() Capabilities {
	caps := Capabilities{}
	if _, err := exec.LookPath("systemd-inhibit"); err == nil {
		caps.Sleep = true
		caps.Display = true
	}
	caps.Lock = sessionBusReachable()
	return caps
}

func (d *linuxDriver) Start(opts Options) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastErrs = nil

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
		if cookie, conn, err := inhibitScreenSaver("Chronos", "Image upscaling in progress"); err != nil {
			d.lastErrs = append(d.lastErrs, "screen saver inhibit: "+err.Error())
		} else {
			d.bus, d.cookie = conn, cookie
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
		if err := d.inhibit.Wait(); err != nil {
			// Killed by design; ignore "signal: terminated".
			if d.inhibit.ProcessState == nil || d.inhibit.ProcessState.ExitCode() != -1 {
				errs = append(errs, "wait systemd-inhibit: "+err.Error())
			}
		}
		d.inhibit = nil
	}
	if d.bus != nil {
		obj := d.bus.Object("org.freedesktop.ScreenSaver", "/org/freedesktop/ScreenSaver")
		if err := obj.Call("org.freedesktop.ScreenSaver.UnInhibit", 0, d.cookie).Err; err != nil {
			errs = append(errs, "uninhibit screen saver: "+err.Error())
		}
		_ = d.bus.Close()
		d.bus = nil
		d.cookie = 0
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func sessionBusReachable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func inhibitScreenSaver(app, reason string) (uint32, *dbus.Conn, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return 0, nil, err
	}
	obj := conn.Object("org.freedesktop.ScreenSaver", "/org/freedesktop/ScreenSaver")
	var cookie uint32
	if err := obj.Call("org.freedesktop.ScreenSaver.Inhibit", 0, app, reason).Store(&cookie); err != nil {
		_ = conn.Close()
		return 0, nil, err
	}
	return cookie, conn, nil
}
