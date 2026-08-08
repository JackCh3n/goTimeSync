//go:build !windows && !linux && !darwin

package main

import "fmt"

// 仅在 Windows/Linux/macOS 之外出现的其它平台（如 freebsd）提供占位实现，
// 保证命令在各平台均可编译与运行。Linux 用 systemd，macOS 用 launchd，见对应文件。
const taskName = "GoTimeSync"

func installStartup() error {
	return fmt.Errorf("开机启动注册暂不支持当前平台（Windows: 计划任务；Linux: systemd；macOS: launchd）")
}

func uninstallStartup() error {
	return fmt.Errorf("开机启动移除暂不支持当前平台")
}

func isInstalled() bool {
	return false
}
