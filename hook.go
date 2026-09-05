package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// hookHTTPClient 供事件通知使用的独立 HTTP 客户端，10 秒超时。
var hookHTTPClient = &http.Client{Timeout: 10 * time.Second}

// hookEventEnabled 判断事件是否在 -hook-events 逗号分隔列表中。
func hookEventEnabled(events, event string) bool {
	for _, e := range strings.Split(events, ",") {
		if strings.TrimSpace(e) == event {
			return true
		}
	}
	return false
}

// hookPayload 是事件通知的 POST JSON 负载。
type hookPayload struct {
	Event    string  `json:"event"`
	Source   string  `json:"source"`
	OffsetMs float64 `json:"offset_ms"`
	DelayMs  float64 `json:"delay_ms"`
	Error    string  `json:"error,omitempty"`
	Time     string  `json:"time"`
}

// runHook 在同步事件后向 -hook-url 发送 POST JSON 通知（仅 http/https）。
// 触发事件由 -hook-events 过滤：synced(已同步) / skipped(低于阈值) /
// rejected(大跳拒绝) / failed(全部源失败或写钟失败)。
// 通知失败仅记录日志，不影响同步结果与退出码。
func runHook(ec effectiveConfig, event, source string, offset, delay time.Duration, errMsg string) {
	if ec.HookURL == "" || !hookEventEnabled(ec.HookEvents, event) {
		return
	}
	u, err := url.Parse(ec.HookURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		logf("钩子 [%s] 跳过: -hook-url 仅支持 http/https", event)
		return
	}
	body, err := json.Marshal(hookPayload{
		Event:    event,
		Source:   source,
		OffsetMs: float64(offset.Microseconds()) / 1000.0,
		DelayMs:  float64(delay.Microseconds()) / 1000.0,
		Error:    errMsg,
		Time:     time.Now().Format(time.RFC3339),
	})
	if err != nil {
		logf("钩子 [%s] 序列化失败: %v", event, err)
		return
	}
	req, err := http.NewRequest(http.MethodPost, ec.HookURL, bytes.NewReader(body))
	if err != nil {
		logf("钩子 [%s] 构造请求失败: %v", event, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "goTimeSync-hook/"+Version)
	resp, err := hookHTTPClient.Do(req)
	if err != nil {
		logf("钩子 [%s] 发送失败: %v", event, err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logf("钩子 [%s] 返回异常状态: %d", event, resp.StatusCode)
		return
	}
	logf("钩子 [%s] 已通知 %s（HTTP %d）", event, ec.HookURL, resp.StatusCode)
}
