//go:build !windows

package main

import "fmt"

// isWindowsService 在非 Windows 平台恒为 false（服务模式仅 Windows 实现）。
func isWindowsService() bool { return false }

// runServiceMode 非 Windows 不会进入（isWindowsService 恒为 false），提供编译占位。
func runServiceMode(ec effectiveConfig) error {
	return fmt.Errorf("原生服务模式仅支持 Windows")
}

// serviceCommand 非 Windows 平台的占位实现，引导使用 install（systemd/launchd）。
func serviceCommand(args []string) error {
	return fmt.Errorf("原生服务模式仅支持 Windows（Linux/macOS 请用 install 注册开机启动）")
}
