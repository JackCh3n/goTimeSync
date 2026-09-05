package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// withFakeClock 把 setSystemTimeFn 替换为记录器并返回读取函数，测试结束自动还原，
// 保证单测绝不触碰真实系统时钟。
func withFakeClock(t *testing.T) func() time.Time {
	t.Helper()
	var got time.Time
	orig := setSystemTimeFn
	setSystemTimeFn = func(tv time.Time) error {
		got = tv
		return nil
	}
	t.Cleanup(func() { setSystemTimeFn = orig })
	return func() time.Time { return got }
}

// newTestTimeServer 起一个返回指定时间的 HTTP 时间源（RFC3339Nano JSON）。
func newTestTimeServer(t *testing.T, ts time.Time) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"time":"%s"}`, ts.UTC().Format(time.RFC3339Nano))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestDoSyncSkipsRejectedSource 验证 -max-offset 拒绝异常源后会继续尝试下一个源
// （fallback 与 best 两种策略均覆盖）：链中第一个源返回 2020 年（偏移约 6 年），
// 第二个源返回当前时间；设置 10 分钟大跳阈值后应最终命中第二个源。
// 回归背景：旧实现 fallback/best 在拒绝后直接返回错误，从不尝试下一个源，
// 与 -max-offset 帮助文本“拒绝设置并尝试下一个”矛盾。
func TestDoSyncSkipsRejectedSource(t *testing.T) {
	for _, strategy := range []string{"fallback", "best"} {
		t.Run(strategy, func(t *testing.T) {
			bad := newTestTimeServer(t, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
			good := newTestTimeServer(t, time.Now().UTC())
			gotClock := withFakeClock(t)

			ec := effectiveConfig{
				Chain:      bad.URL + "," + good.URL,
				MaxOffset:  600000, // 10 分钟
				Strategy:   strategy,
				Timeout:    5,
				StatusFile: filepath.Join(t.TempDir(), "status.json"),
			}
			if err := doSync(ec); err != nil {
				t.Fatalf("doSync 失败: %v", err)
			}
			set := gotClock()
			if set.IsZero() {
				t.Fatal("未调用系统时间设置（异常源未被跳过或正常源未被命中）")
			}
			if drift := time.Since(set); drift < -10*time.Second || drift > 10*time.Second {
				t.Errorf("命中的应是正常源（偏移≈0），实际写入时间偏移 %v", drift)
			}
		})
	}
}

// TestDoSyncAllRejected 验证所有源都超过大跳阈值时返回错误且不写系统时间。
func TestDoSyncAllRejected(t *testing.T) {
	bad := newTestTimeServer(t, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	gotClock := withFakeClock(t)

	ec := effectiveConfig{
		Chain:      bad.URL,
		MaxOffset:  600000,
		Timeout:    5,
		StatusFile: filepath.Join(t.TempDir(), "status.json"),
	}
	if err := doSync(ec); err == nil {
		t.Fatal("所有源被拒时应返回错误")
	}
	if !gotClock().IsZero() {
		t.Error("被拒时不应写系统时间")
	}
}
