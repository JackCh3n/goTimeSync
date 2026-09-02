package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseBodyTime(t *testing.T) {
	cases := []struct {
		body    string
		want    time.Time
		wantErr bool
	}{
		{`{"time":"2026-07-10T12:00:00.000Z"}`, time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC), false},
		{`{"unix":1783331459}`, time.Unix(1783331459, 0).UTC(), false},
		{`{"unixMs":1783331459123}`, time.Unix(1783331459, 123000000).UTC(), false},
		{`2026-07-10T12:00:00Z`, time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC), false},
		{`1783331459`, time.Unix(1783331459, 0).UTC(), false},
		{`not-a-time`, time.Time{}, true},
	}
	for _, c := range cases {
		got, err := parseBodyTime([]byte(c.body))
		if c.wantErr {
			if err == nil {
				t.Errorf("body %q: 期望错误但未返回", c.body)
			}
			continue
		}
		if err != nil {
			t.Errorf("body %q: 意外错误 %v", c.body, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("body %q: got %v want %v", c.body, got, c.want)
		}
	}
}

func TestQueryHTTPTimeOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		fmt.Fprintf(w, `{"time":"%s"}`, now.Format(time.RFC3339Nano))
	}))
	defer srv.Close()

	c, off, d, err := queryHTTPTime(srv.URL, 5*time.Second, nil, "")
	if err != nil {
		t.Fatalf("queryHTTPTime 失败: %v", err)
	}
	if off.Abs() > 2*time.Second {
		t.Errorf("偏移过大，疑似解析错误: %v", off)
	}
	// Windows 上 nanotime 约 0.5ms 才更新一次，回环响应可能快于一个刻度而测得 rtt=0，
	// 故只要求非负（真实网络源必然为正；delay 仅用于日志与 best 策略排序，为 0 无害）。
	if d < 0 {
		t.Errorf("延时应非负: %v", d)
	}
	_ = c
}

func TestQueryHTTPTimePOST(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		now := time.Now().UTC()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"time":"%s"}`, now.Format(time.RFC3339Nano))
	}))
	defer srv.Close()

	if _, _, _, err := queryHTTPTime(srv.URL, 5*time.Second, nil, "POST"); err != nil {
		t.Fatalf("queryHTTPTime POST 失败: %v", err)
	}
	if gotMethod != "POST" {
		t.Errorf("期望 POST 请求，实际 %s", gotMethod)
	}
}

func TestQueryHTTPTimeStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	if _, _, _, err := queryHTTPTime(srv.URL, 5*time.Second, nil, ""); err == nil {
		t.Fatal("期望 500 状态码返回错误")
	}
}

func TestQueryHTTPTimeNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://example.com")
		w.WriteHeader(302)
	}))
	defer srv.Close()
	if _, _, _, err := queryHTTPTime(srv.URL, 5*time.Second, nil, ""); err == nil {
		t.Fatal("期望重定向（不跟随）返回错误")
	}
}
