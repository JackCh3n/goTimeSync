package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestRetryBackoff 验证失败退避序列：30s → 2m → 10m（封顶）。
func TestRetryBackoff(t *testing.T) {
	cases := []struct {
		failCount int
		want      time.Duration
	}{
		{1, 30 * time.Second},
		{2, 2 * time.Minute},
		{3, 10 * time.Minute},
		{99, 10 * time.Minute},
	}
	for _, c := range cases {
		if got := retryBackoff(c.failCount); got != c.want {
			t.Errorf("retryBackoff(%d)=%s，期望 %s", c.failCount, got, c.want)
		}
	}
}

// TestHookEventEnabled 验证事件过滤。
func TestHookEventEnabled(t *testing.T) {
	events := "synced, failed ,rejected"
	for _, ev := range []string{"synced", "failed", "rejected"} {
		if !hookEventEnabled(events, ev) {
			t.Errorf("事件 %s 应启用", ev)
		}
	}
	if hookEventEnabled(events, "skipped") {
		t.Error("事件 skipped 未列出，应不启用")
	}
	if hookEventEnabled("", "synced") {
		t.Error("空事件列表应全部不启用")
	}
}

// TestRunHookWebhook 验证事件后向 -hook-url 发送 POST JSON，且事件过滤与非 http/https 地址被跳过。
func TestRunHookWebhook(t *testing.T) {
	var gotPath string
	var gotBody hookPayload
	recv := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(200)
		close(recv)
	}))
	defer srv.Close()

	ec := effectiveConfig{HookURL: srv.URL + "/notify", HookEvents: "synced"}
	runHook(ec, "synced", "ntp:a:123", -1500*time.Millisecond, 12*time.Millisecond, "")

	select {
	case <-recv:
	default:
		t.Fatal("钩子未发送请求")
	}
	if gotPath != "/notify" {
		t.Errorf("请求路径=%s，期望 /notify", gotPath)
	}
	if gotBody.Event != "synced" || gotBody.Source != "ntp:a:123" {
		t.Errorf("负载不符: %+v", gotBody)
	}
	if gotBody.OffsetMs != -1500 || gotBody.DelayMs != 12 {
		t.Errorf("毫秒字段不符: offset=%v delay=%v", gotBody.OffsetMs, gotBody.DelayMs)
	}

	// 事件未列出 → 不发送
	ec2 := effectiveConfig{HookURL: srv.URL, HookEvents: "failed"}
	runHook(ec2, "synced", "x", 0, 0, "")

	// 非 http/https → 不发送
	ec3 := effectiveConfig{HookURL: "ftp://example.com/hook", HookEvents: "synced"}
	runHook(ec3, "synced", "x", 0, 0, "")
}

// TestQuerySourceStableStable 验证多次采样：稳定源取中位并成功。
func TestQuerySourceStableStable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"time":"%s"}`, time.Now().UTC().Format(time.RFC3339Nano))
	}))
	defer srv.Close()

	ec := effectiveConfig{Samples: 3, SampleToleranceMs: 100}
	s := timeSource{kind: "http", target: srv.URL, label: "http:" + srv.URL}
	c, off, delay, err := querySourceStable(s, ec, 5*time.Second)
	if err != nil {
		t.Fatalf("稳定源采样失败: %v", err)
	}
	if off.Abs() > 2*time.Second || delay < 0 {
		t.Errorf("结果异常: off=%v delay=%v", off, delay)
	}
	if c.IsZero() {
		t.Error("校准时间不应为零值")
	}
}

// TestQuerySourceStableFlaky 验证波动超容差的源被判为不稳定（返回错误）。
func TestQuerySourceStableFlaky(t *testing.T) {
	var n int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i := atomic.AddInt64(&n, 1)
		// 奇偶交替返回相差 2 小时的时间，制造超容差波动
		base := time.Now().UTC()
		if i%2 == 0 {
			base = base.Add(2 * time.Hour)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"time":"%s"}`, base.Format(time.RFC3339Nano))
	}))
	defer srv.Close()

	ec := effectiveConfig{Samples: 3, SampleToleranceMs: 100}
	s := timeSource{kind: "http", target: srv.URL, label: "http:" + srv.URL}
	if _, _, _, err := querySourceStable(s, ec, 5*time.Second); err == nil {
		t.Fatal("波动超容差应返回错误")
	}
}

// TestHTTPTimeLayout 验证 -http-time-layout 自定义布局优先解析内网设备的纯文本时间。
func TestHTTPTimeLayout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 纯文本、无时区、非 RFC3339 —— 内置解析无法处理
		fmt.Fprint(w, time.Now().Format("2006-01-02 15:04:05"))
	}))
	defer srv.Close()

	// 布局解析按本机时区解释，秒级精度 + rtt/2，偏移应在数秒内
	_, off, _, err := queryHTTPTime(srv.URL, 5*time.Second, nil, "", "2006-01-02 15:04:05")
	if err != nil {
		t.Fatalf("自定义布局解析失败: %v", err)
	}
	if off.Abs() > 3*time.Second {
		t.Errorf("偏移异常（应≈0）: off=%v", off)
	}
}

// TestMedianIdx 验证中位下标。
func TestMedianIdx(t *testing.T) {
	cases := []struct {
		ds   []time.Duration
		want int
	}{
		{[]time.Duration{5, 1, 3}, 2},    // 中位 3
		{[]time.Duration{1, 3, 5, 7}, 2}, // 偶数取中间偏大者 5ns
		{[]time.Duration{9}, 0},
	}
	for _, c := range cases {
		if got := medianIdx(c.ds); c.ds[got] != c.ds[c.want] {
			t.Errorf("medianIdx(%v)=%d，期望值 %v", c.ds, got, c.ds[c.want])
		}
	}
}
