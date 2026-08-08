//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const taskName = "GoTimeSync"

// launchAgentDir 是当前用户的 LaunchAgents 目录。
var launchAgentDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/Library/LaunchAgents"
	}
	return filepath.Join(home, "Library", "LaunchAgents")
}()

// plistPath 返回 LaunchAgent plist 文件路径。
func plistPath() string {
	return filepath.Join(launchAgentDir, "com.gotimesync.plist")
}

// exePath 返回当前可执行文件的绝对路径。
func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}

// installStartup 生成一个用户级 LaunchAgent plist（不自动加载，打印 load 命令）。
// 这样无需 root 即可让 goTimeSync 随用户登录自启；root/系统级可放到 /Library/LaunchDaemons。
func installStartup() error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	runArgs := os.Args[2:]
	progArgs := []string{exe, "run"}
	progArgs = append(progArgs, runArgs...)

	if err := os.MkdirAll(launchAgentDir, 0755); err != nil {
		return fmt.Errorf("创建 LaunchAgents 目录失败: %w", err)
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.gotimesync</string>
    <key>ProgramArguments</key>
    <array>
`)
	for _, a := range progArgs {
		fmt.Fprintf(&b, "        <string>%s</string>\n", xmlEscape(a))
	}
	b.WriteString(`    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
`)
	if err := os.WriteFile(plistPath(), []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("写入 LaunchAgent plist 失败: %w", err)
	}
	fmt.Printf("已生成 LaunchAgent: %s\n", plistPath())
	fmt.Println("加载命令：launchctl load -w ~/Library/LaunchAgents/com.gotimesync.plist")
	fmt.Println("如需系统级自启，请把该文件放到 /Library/LaunchDaemons/ 并执行：launchctl load -w /Library/LaunchDaemons/com.gotimesync.plist")
	return nil
}

// uninstallStartup 移除 LaunchAgent plist。
func uninstallStartup() error {
	if err := os.Remove(plistPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("移除 LaunchAgent plist 失败: %w", err)
	}
	fmt.Printf("已移除 LaunchAgent: %s\n", plistPath())
	fmt.Println("若已加载，请执行：launchctl unload -w ~/Library/LaunchAgents/com.gotimesync.plist")
	return nil
}

// isInstalled 通过 launchctl 检查服务是否已加载。
func isInstalled() bool {
	cmd := exec.Command("launchctl", "list", "com.gotimesync")
	return cmd.Run() == nil
}

// xmlEscape 对 plist 中的字符串做 XML 转义。
func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}