//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const taskName = "GoTimeSync"

// exePath 返回当前可执行文件的绝对路径。
func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}

// installStartup 通过 schtasks 创建“系统启动时”以 SYSTEM 身份运行计划任务，实现开机启动。
// 需要管理员权限执行本命令。
func installStartup() error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	// /sc onstart : 系统启动时触发
	// /ru SYSTEM  : 以 SYSTEM 账户运行（无需登录用户）
	// /rl HIGHEST : 最高权限
	// /tr         : 运行的命令 "exe run <安装时的参数>"
	// 把 install 后面的参数原样传给 run，使开机任务复现当前的主备链/间隔等配置
	runArgs := os.Args[2:]
	quoted := make([]string, 0, len(runArgs))
	for _, a := range runArgs {
		if strings.ContainsAny(a, " \t") {
			a = "\"" + a + "\""
		}
		quoted = append(quoted, a)
	}
	tr := fmt.Sprintf("\"%s\" run %s", exe, strings.Join(quoted, " "))
	cmd := exec.Command("schtasks", "/create",
		"/sc", "onstart",
		"/tn", taskName,
		"/tr", tr,
		"/ru", "SYSTEM",
		"/rl", "HIGHEST",
		"/f",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("创建计划任务失败: %v\n%s", err, string(out))
	}
	fmt.Printf("已注册开机启动计划任务 [%s]，将以 SYSTEM 身份在系统启动时运行:\n  %s\n", taskName, tr)
	return nil
}

// uninstallStartup 移除开机启动计划任务。
func uninstallStartup() error {
	cmd := exec.Command("schtasks", "/delete", "/tn", taskName, "/f")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("删除计划任务失败: %v\n%s", err, string(out))
	}
	fmt.Printf("已移除开机启动计划任务 [%s]\n", taskName)
	return nil
}

// isInstalled 检查计划任务是否已存在。
func isInstalled() bool {
	cmd := exec.Command("schtasks", "/query", "/tn", taskName)
	return cmd.Run() == nil
}
