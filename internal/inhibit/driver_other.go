//go:build !windows && !linux

package inhibit

// platformDriver returns a no-op driver for platforms without an inhibitor
// implementation (macOS). The UI shows the unsupported state instead of failing.
type otherDriver struct{}

func platformDriver() Driver { return otherDriver{} }

func (otherDriver) Name() string { return "unsupported" }

func (otherDriver) Detail() string {
	return "当前平台暂无睡眠/锁屏抑制实现；处理期间请手动调整电源设置。"
}

func (otherDriver) Capabilities() Capabilities { return Capabilities{} }

func (otherDriver) Start(Options) error { return ErrUnsupported }

func (otherDriver) Stop() error { return nil }
