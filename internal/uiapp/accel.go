package uiapp

import (
	"context"
	"fmt"

	"github.com/w32394045-dotcom/bg2x-chronos/internal/config"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/engine"
	"github.com/w32394045-dotcom/bg2x-chronos/internal/runtime"
)

// AccelStatus is the acceleration view consumed by the UI badge
// (docs/CONTRACTS.md v3 §3.2). Active describes what the engine really uses;
// Available/GpuUsable/FallbackReason describe what the machine could use.
type AccelStatus struct {
	Mode           string      `json:"mode"` // "auto" | "cpu"
	Active         engine.Info `json:"active"`
	Available      []string    `json:"available"`
	GpuUsable      bool        `json:"gpuUsable"`
	FallbackReason string      `json:"fallbackReason"`

	// GPU runtime component (docs/CONTRACTS.md v3, P3).
	GpuRuntime      runtime.State `json:"gpuRuntime"`
	InstallPhase    string        `json:"installPhase"` // "" | resolving|downloading|installing|done|failed
	InstallPart     string        `json:"installPart"`  // 当前下载的组件包名
	InstallDone     int64         `json:"installDone"`
	InstallTotal    int64         `json:"installTotal"`
	InstallError    string        `json:"installError"`
	RestartRequired bool          `json:"restartRequired"`
}

// GetAccelStatus reports the persisted preference plus the live runtime probe.
func (c *Core) GetAccelStatus() AccelStatus {
	c.mu.Lock()
	settings, info := c.settings, c.engineInfo
	c.mu.Unlock()

	mode := config.AccelAuto
	if settings != nil && settings.AccelMode != "" {
		mode = settings.AccelMode
	}
	probe := engine.GetProbe()
	available := engine.AvailableAccelerators(probe)
	device, _, reason := engine.PickDevice(probe, mode == config.AccelCPU)
	if info.Device == "" {
		info.Device = "CPU"
	}
	if info.EP == "" {
		info.EP = "CPU"
	}
	if info.FallbackReason == "" {
		info.FallbackReason = reason
	}
	status := AccelStatus{
		Mode:           mode,
		Active:         info,
		Available:      available,
		GpuUsable:      device == "GPU",
		FallbackReason: reason,
	}
	if mgr := c.runtimeManager(); mgr != nil {
		status.GpuRuntime = mgr.State(mgr.GpuFlavor())
		phase, part, done, total, lastErr := mgr.Progress()
		status.InstallPhase, status.InstallPart = phase, part
		status.InstallDone, status.InstallTotal, status.InstallError = done, total, lastErr
	}
	return status
}

// runtimeManager returns the runtime component manager (nil in early startup).
func (c *Core) runtimeManager() *runtime.Manager {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runtimeMgr
}

// InstallAccelRuntime downloads and installs the GPU runtime component in the
// background. Progress is reported through chronos:accel events.
func (c *Core) InstallAccelRuntime() (AccelStatus, error) {
	mgr := c.runtimeManager()
	if mgr == nil {
		return AccelStatus{}, fmt.Errorf("运行时管理器不可用")
	}
	phase, _, _, _, _ := mgr.Progress()
	if phase == "downloading" || phase == "installing" || phase == "resolving" {
		return c.GetAccelStatus(), nil
	}
	if mgr.Installed(runtime.FlavorDirectML) {
		return c.GetAccelStatus(), nil
	}
	go func() {
		err := mgr.Install(context.Background())
		if err != nil {
			c.log("warn", "GPU 运行时安装失败: "+err.Error())
		} else {
			c.log("info", "GPU 运行时安装完成；重启应用后启用 GPU")
			c.mu.Lock()
			c.restartRequired = true
			c.mu.Unlock()
		}
		c.emit("chronos:accel", c.GetAccelStatus())
	}()
	// Emit a first "resolving" snapshot so the UI reacts immediately.
	return c.GetAccelStatus(), nil
}

// SetAccelMode persists "auto" or "cpu" and re-probes the engine. The new mode
// applies to newly created engines (running batches keep their provider).
func (c *Core) SetAccelMode(mode string) (AccelStatus, error) {
	if !config.ValidAccelMode(mode) {
		return AccelStatus{}, fmt.Errorf("未知加速方式 %q（可选 auto / cpu）", mode)
	}
	c.mu.Lock()
	settings := c.settings
	if settings != nil {
		settings.AccelMode = mode
	}
	dataDir := c.dataDir
	c.mu.Unlock()

	if settings != nil && dataDir != "" {
		if err := settings.Save(dataDir); err != nil {
			c.log("warn", "保存加速设置失败: "+err.Error())
		}
	}
	c.refreshEngineInfo()
	status := c.GetAccelStatus()
	c.emit("chronos:accel", status)
	if mode == config.AccelCPU {
		c.log("info", "加速方式已切换为：仅 CPU")
	} else {
		c.log("info", "加速方式已切换为：自动")
	}
	return status, nil
}

// refreshEngineInfo rebuilds the startup engine snapshot with the current
// acceleration preference (cheap: it creates and closes one provider).
func (c *Core) refreshEngineInfo() {
	preferCPU := false
	c.mu.Lock()
	if c.settings != nil {
		preferCPU = c.settings.AccelMode == config.AccelCPU
	}
	c.mu.Unlock()

	probe, err := engine.NewProvider(engine.Config{Scale: 2, PreferCPU: preferCPU}, 1)
	if err != nil {
		c.log("warn", "刷新引擎信息失败: "+err.Error())
		return
	}
	info := probe.Info()
	_ = probe.Close()
	c.mu.Lock()
	c.engineInfo = info
	c.mu.Unlock()
}
