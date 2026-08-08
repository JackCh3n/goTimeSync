//go:build darwin

package main

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// setSystemTime 通过 settimeofday 设置 macOS 系统时钟为给定 UTC 时间。需要 root 权限。
// macOS 不提供 clock_settime，故使用 settimeofday（__APPLE__ 平台标准接口）。
func setSystemTime(t time.Time) error {
	timeSetMu.Lock()
	defer timeSetMu.Unlock()
	tv := syscall.NsecToTimeval(t.UnixNano())
	_, _, errno := syscall.Syscall(syscall.SYS_SETTIMEOFDAY, uintptr(unsafe.Pointer(&tv)), 0, 0)
	// KeepAlive 确保 tv 在 syscall 返回前不被 GC 回收（防御性安全）。
	runtime.KeepAlive(&tv)
	if errno != 0 {
		return fmt.Errorf("设置系统时间失败(settimeofday): %v（需要 root 权限）", errno)
	}
	return nil
}
