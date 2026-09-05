//go:build windows

package main

import (
	"os/exec"
)

// isAdmin 通过 net session 探测当前是否具备管理员权限（与服务控制/设时所需的提权一致）。
func isAdmin() bool {
	return exec.Command("net", "session").Run() == nil
}
