//go:build windows

package main

import (
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

// systemTime 对应 Windows API 的 SYSTEMTIME 结构（SetSystemTime 使用 UTC）。
type systemTime struct {
	wYear         uint16
	wMonth        uint16
	wDayOfWeek    uint16
	wDay          uint16
	wHour         uint16
	wMinute       uint16
	wSecond       uint16
	wMilliseconds uint16
}

var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var procSetSystemTime = kernel32.NewProc("SetSystemTime")

// setSystemTime 设置 Windows 系统时钟为给定的 UTC 时间。需要管理员权限。
func setSystemTime(t time.Time) error {
	timeSetMu.Lock()
	defer timeSetMu.Unlock()
	t = t.UTC()
	st := systemTime{
		wYear:         uint16(t.Year()),
		wMonth:        uint16(t.Month()),
		wDay:          uint16(t.Day()),
		wHour:         uint16(t.Hour()),
		wMinute:       uint16(t.Minute()),
		wSecond:       uint16(t.Second()),
		wMilliseconds: uint16(t.Nanosecond() / int(time.Millisecond)),
	}
	r, _, err := procSetSystemTime.Call(uintptr(unsafe.Pointer(&st)))
	// KeepAlive 确保 st 在 syscall 返回前不被 GC 回收（防御性安全）。
	runtime.KeepAlive(&st)
	if r == 0 {
		return fmt.Errorf("SetSystemTime 失败: %v (需要管理员权限)", err)
	}
	return nil
}
