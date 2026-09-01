package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestWriteStatusHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gt.status.json")

	for i := 0; i < 15; i++ {
		writeStatus(path, syncStatus{
			LastSync: time.Now().Format(time.RFC3339),
			Source:   "ntp:test",
			OffsetMs: float64(i),
			DelayMs:  1.0,
			OK:       true,
			Action:   "synced",
		})
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取状态文件失败: %v", err)
	}
	var st syncStatus
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatalf("解析状态文件失败: %v", err)
	}
	if len(st.History) != maxHistory {
		t.Errorf("历史条数=%d，期望 %d", len(st.History), maxHistory)
	}
	// 新记录在前，且 offset 应为最近的 14..5（倒序）
	if st.History[0].OffsetMs != 14 {
		t.Errorf("最新记录 offset=%v，期望 14", st.History[0].OffsetMs)
	}
	if st.History[9].OffsetMs != 5 {
		t.Errorf("最旧记录 offset=%v，期望 5", st.History[9].OffsetMs)
	}
}

func TestDailyLogWriter(t *testing.T) {
	root := t.TempDir()
	w := &dailyLogWriter{dir: root}
	if err := w.open(); err != nil {
		t.Fatalf("open 失败: %v", err)
	}
	if _, err := w.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if w.file != nil {
		_ = w.file.Close()
	}

	now := time.Now()
	ym := now.Format("200601")
	day := now.Day()
	// 目录结构应为 根/年月/日.log
	expect := filepath.Join(root, ym, strconv.Itoa(day)+".log")
	if _, err := os.Stat(expect); err != nil {
		t.Fatalf("期望日志文件 %s 存在: %v", expect, err)
	}
	b, err := os.ReadFile(expect)
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	if string(b) != "hello\n" {
		t.Errorf("日志内容=%q，期望 hello\\n", string(b))
	}
}
