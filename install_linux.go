//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const taskName = "GoTimeSync"

// systemdUnitDir 是用户级 systemd 单元目录（无需 root 即可启用）。
var systemdUnitDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/etc/systemd/system"
	}
	return filepath.Join(home, ".config", "systemd", "user")
}()

// unitPath 返回 systemd 单元文件路径。
func unitPath() string {
	return filepath.Join(systemdUnitDir, "gotimesync.service")
}

// exePath 返回当前可执行文件的绝对路径。
func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}

// installStartup 生成一个用户级 systemd 单元文件（不自动启用，打印启用命令）。
// 这样无需 root 即可让 goTimeSync 随用户登录自启；root 下可直接复制到 /etc/systemd/system。
func installStartup() error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	runArgs := os.Args[2:]
	quoted := make([]string, 0, len(runArgs))
	for _, a := range runArgs {
		if strings.ContainsAny(a, " \t") {
			a = "\"" + a + "\""
		}
		quoted = append(quoted, a)
	}
	exec := fmt.Sprintf("%s run %s", exe, strings.Join(quoted, " "))

	if err := os.MkdirAll(systemdUnitDir, 0755); err != nil {
		return fmt.Errorf("创建 systemd 用户目录失败: %w", err)
	}
	unit := `[Unit]
Description=goTimeSync time sync service
After=network-online.target systemd-user-sessions.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=` + exec + `
Restart=always
RestartSec=60

[Install]
WantedBy=default.target
`
	if err := os.WriteFile(unitPath(), []byte(unit), 0644); err != nil {
		return fmt.Errorf("写入 systemd 单元失败: %w", err)
	}
	fmt.Printf("已生成 systemd 单元: %s\n", unitPath())
	fmt.Println("启用命令：systemctl --user daemon-reload && systemctl --user enable --now gotimesync")
	fmt.Printf("如需 root 系统级自启，请把该文件复制到 /etc/systemd/system/ 后执行：systemctl daemon-reload && systemctl enable --now gotimesync\n")
	return nil
}

// uninstallStartup 移除 systemd 单元文件。
func uninstallStartup() error {
	if err := os.Remove(unitPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("移除 systemd 单元失败: %w", err)
	}
	fmt.Printf("已移除 systemd 单元: %s\n", unitPath())
	fmt.Println("若已启用，请执行：systemctl --user disable --now gotimesync")
	return nil
}

// isInstalled 通过 systemctl 检查单元是否处于 enabled 状态。
func isInstalled() bool {
	cmd := exec.Command("systemctl", "--user", "is-enabled", "gotimesync")
	return cmd.Run() == nil
}