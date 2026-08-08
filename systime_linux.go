//go:build linux

package main

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// clockRealtime 对应 Linux 的 CLOCK_REALTIME。
const clockRealtime = 0

// setSystemTime 通过 clock_settime(CLOCK_REALTIME) 设置 Linux 系统时钟为给定 UTC 时间。需要 root 权限。
func setSystemTime(t time.Time) error {
	timeSetMu.Lock()
	defer timeSetMu.Unlock()
	ts := syscall.Timespec{Sec: t.Unix(), Nsec: int64(t.Nanosecond())}
	_, _, errno := syscall.Syscall(syscall.SYS_CLOCK_SETTIME, uintptr(clockRealtime), uintptr(unsafe.Pointer(&ts)), 0)
	// KeepAlive 确保 ts 在 syscall 返回前不被 GC 回收（防御性安全）。
	runtime.KeepAlive(&ts)
	if errno != 0 {
		return fmt.Errorf("设置系统时间失败(clock_settime): %v（需要 root 权限）", errno)
	}
	return nil
}
