//go:build !windows

package main

import "fmt"

// 非 Windows 平台不开机启动注册（schtasks 是 Windows 专属）。
// Linux 可通过 systemd 单元实现类似效果，此处仅给出可读的占位实现，保证命令在各平台均可编译与运行。
const taskName = "GoTimeSync"

func installStartup() error {
	return fmt.Errorf("开机启动注册仅支持 Windows 平台（Linux 可手动创建 systemd 单元）")
}

func uninstallStartup() error {
	return fmt.Errorf("开机启动移除仅支持 Windows 平台")
}

func isInstalled() bool {
	return false
}
