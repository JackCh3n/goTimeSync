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

// oldTaskName 是更名前的旧计划任务名。升级用户机器上可能残留，install 时自动清理迁移。
const oldTaskName = "WinTimeSync"

// exePath 返回当前可执行文件的绝对路径。
func exePath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(exe)
}

// taskExists 检查指定计划任务是否存在。
func taskExists(name string) bool {
	cmd := exec.Command("schtasks", "/query", "/tn", name)
	return cmd.Run() == nil
}

// installStartup 通过 schtasks 创建“系统启动时”以 SYSTEM 身份运行计划任务，实现开机启动。
// 需要管理员权限执行本命令。若检测到旧任务名 WinTimeSync，会自动删除以避免双任务。
func installStartup() error {
	// 迁移旧任务：更名前注册的计划任务名是 WinTimeSync，自动删除，避免与 GoTimeSync 重复开机同步。
	if taskExists(oldTaskName) {
		if err := exec.Command("schtasks", "/delete", "/tn", oldTaskName, "/f").Run(); err == nil {
			fmt.Printf("已迁移旧计划任务 [%s]（更名前残留）\n", oldTaskName)
		} else {
			fmt.Printf("警告: 存在旧计划任务 [%s] 但删除失败: %v\n", oldTaskName, err)
		}
	}

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

// uninstallStartup 移除开机启动计划任务（含旧任务名）。
func uninstallStartup() error {
	targets := []string{taskName}
	if taskExists(oldTaskName) {
		targets = append(targets, oldTaskName)
	}
	for _, name := range targets {
		cmd := exec.Command("schtasks", "/delete", "/tn", name, "/f")
		out, err := cmd.CombinedOutput()
		if err != nil {
			if name == oldTaskName {
				continue // 旧任务删除失败不阻塞
			}
			return fmt.Errorf("删除计划任务失败: %v\n%s", err, string(out))
		}
		fmt.Printf("已移除开机启动计划任务 [%s]\n", name)
	}
	return nil
}

// isInstalled 检查计划任务是否已存在（新任务为准，兼容旧任务）。
func isInstalled() bool {
	return taskExists(taskName) || taskExists(oldTaskName)
}
