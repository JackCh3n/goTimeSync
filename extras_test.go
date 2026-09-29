package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// captureStdout 捕获 fn 期间写入 os.Stdout 的内容（logf 经 logOut 间接引用 os.Stdout）。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("创建管道失败: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	_ = w.Close()
	os.Stdout = old
	return <-done
}

// TestInitLoggerQuiet 验证安静模式仅写文件、非安静模式同时写控制台（回归：曾把 quiet 误接成含 stdout）。
func TestInitLoggerQuiet(t *testing.T) {
	dir := t.TempDir()
	out := captureStdout(t, func() {
		initLogger(dir, true) // 安静：仅文件
		logf("quiet-line")
		initLogger(dir, false) // 正常：文件+控制台
		logf("loud-line")
		initLogger("", false) // 还原默认
	})
	if strings.Contains(out, "quiet-line") {
		t.Error("安静模式的日志不应输出到控制台")
	}
	if !strings.Contains(out, "loud-line") {
		t.Error("非安静模式的日志应输出到控制台")
	}
	// 两种模式都应落盘（同目录同日文件）
	entries, err := os.ReadDir(filepath.Join(dir, time.Now().Format("200601")))
	if err != nil || len(entries) == 0 {
		t.Fatalf("日志文件未创建: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, time.Now().Format("200601"), entries[0].Name()))
	if err != nil {
		t.Fatalf("读取日志失败: %v", err)
	}
	for _, want := range []string{"quiet-line", "loud-line"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("日志文件缺少 %q", want)
		}
	}
}

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
