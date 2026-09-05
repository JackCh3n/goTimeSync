//go:build !windows

package main

import "os"

// isAdmin 在类 Unix 平台以 euid 判断是否具备 root 权限（设置系统时间需要）。
func isAdmin() bool {
	return os.Geteuid() == 0
}
