package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// httpTimeResponse 是 HTTP 时间服务器返回的 JSON 结构（兼容多种字段）。
type httpTimeResponse struct {
	Time   string `json:"time"`   // RFC3339 格式，如 2026-07-06T17:40:59.123Z
	Unix   int64  `json:"unix"`   // 秒级时间戳
	UnixMs int64  `json:"unixMs"` // 毫秒级时间戳
}

// queryHTTPTime 请求内网 HTTP 时间服务器，测量往返耗时(RTT)，估算时间偏移并校准。
// 通过 t0(发请求前) / t3(收响应后) 与服务器返回的时间，按 (serverTime + RTT/2) 估算服务端当前时间。
// method 指定请求方法（"" 或 "get" 用 GET，"post" 用 POST），兼容只暴露 POST 接口的内网服务。
// layout 为 -http-time-layout 自定义时间布局（Go time layout；非空时优先于内置格式解析，
// 无时区戳按本机时区解释），用于适配返回特殊格式的内网设备。
// 显式校验：仅接受 2xx 状态码；不跟随重定向（遇到 3xx 直接判失败，交由主备链尝试下一个源）。
func queryHTTPTime(url string, timeout time.Duration, client *http.Client, method, layout string) (corrected time.Time, offset, delay time.Duration, err error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if client == nil {
		client = &http.Client{
			Timeout: timeout,
			// 禁止自动跟随重定向：把 3xx 交给上层判断，避免被重定向到登录页等无意义页面。
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	verb := "GET"
	if strings.EqualFold(method, "POST") {
		verb = "POST"
	}
	t0 := time.Now()
	req, err := http.NewRequest(verb, url, nil)
	if err != nil {
		return time.Time{}, 0, 0, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return time.Time{}, 0, 0, fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	// 显式校验 HTTP 状态码：仅接受 2xx
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return time.Time{}, 0, 0, fmt.Errorf("HTTP 状态码异常: %d %s", resp.StatusCode, resp.Status)
	}
	// 响应体限长 1MB：时间应答远小于此，防御异常/恶意源用超大响应耗尽内存。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return time.Time{}, 0, 0, fmt.Errorf("读取响应失败: %w", err)
	}
	t3 := time.Now()

	// 取时间优先级：
	//   ① 自定义布局 -http-time-layout（用户显式指定，适配内网设备的特殊格式）
	//   ② 响应体（JSON / RFC3339 / Unix 时间戳）
	//   ③ 响应头 Date（兼容普通 Web 服务器，整秒精度）
	var serverTime time.Time
	if layout != "" {
		// 无时区戳按本机时区解释（内网设备墙钟通常与客户端同时区）
		if ts, e := time.ParseInLocation(layout, strings.TrimSpace(string(body)), time.Local); e == nil {
			serverTime = ts
		}
	}
	if serverTime.IsZero() {
		if st, e := parseBodyTime(body); e == nil {
			serverTime = st
		} else if dateHdr := resp.Header.Get("Date"); dateHdr != "" {
			if ts, e2 := time.Parse(http.TimeFormat, dateHdr); e2 == nil {
				serverTime = ts.UTC()
			} else if ts2, e3 := time.Parse(time.RFC1123Z, dateHdr); e3 == nil {
				serverTime = ts2.UTC()
			}
		}
	}
	if serverTime.IsZero() {
		return time.Time{}, 0, 0, fmt.Errorf("无法从响应体或 Date 头解析时间: %q", strings.TrimSpace(string(body)))
	}

	rtt := t3.Sub(t0)
	// 假设网络对称，服务端在 t3 时刻的时间 ≈ serverTime + rtt/2
	offset = serverTime.Add(rtt / 2).Sub(t3)
	corrected = t3.Add(offset)
	delay = rtt

	return corrected, offset, delay, nil
}

// parseBodyTime 解析响应体中的时间，支持 JSON / RFC3339 / Unix 时间戳。
// 优先使用带亚秒精度的字段（time > unixMs > unix）；失败时返回错误，由调用方回退到 Date 头。
func parseBodyTime(body []byte) (time.Time, error) {
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return time.Time{}, fmt.Errorf("空响应体")
	}
	var r httpTimeResponse
	if jsonErr := json.Unmarshal([]byte(raw), &r); jsonErr == nil {
		switch {
		case r.Time != "":
			if ts, e := time.Parse(time.RFC3339Nano, r.Time); e == nil {
				return ts.UTC(), nil
			}
		case r.UnixMs > 0:
			return time.Unix(r.UnixMs/1000, (r.UnixMs%1000)*int64(time.Millisecond)).UTC(), nil
		case r.Unix > 0:
			return time.Unix(r.Unix, 0).UTC(), nil
		}
	}
	// 退化解析：把整个 body 当作 RFC3339 或 Unix 时间戳
	if ts, perr := time.Parse(time.RFC3339Nano, raw); perr == nil {
		return ts.UTC(), nil
	}
	if u, perr := strconv.ParseInt(raw, 10, 64); perr == nil {
		if u > 1e12 { // 毫秒
			return time.Unix(u/1000, (u%1000)*int64(time.Millisecond)).UTC(), nil
		}
		return time.Unix(u, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("无法解析时间响应: %q", raw)
}

// newTimeHTTPServer 构造 HTTP 时间服务器（含路由与超时加固）；
// selfSync 为真时另起协程按 ec.Interval 周期用 NTP(ec.NTPServer) 校准本机时钟。
// 返回的 *http.Server 由调用方 ListenAndServe / Shutdown（优雅退出）。
func newTimeHTTPServer(ec effectiveConfig, addr string, selfSync bool) *http.Server {
	if selfSync {
		go selfSyncLoop(ec)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/time", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"time":   now.Format(time.RFC3339Nano),
			"unix":   now.Unix(),
			"unixMs": now.UnixNano() / int64(time.Millisecond),
		})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
		// 超时加固：防止慢连接长期占用句柄（内网服务也做基本防护）
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	logf("HTTP 时间服务器已启动: http://%s/time", addr)
	return srv
}

// selfSyncLoop 是 server 模式的后台 NTP 自校准循环：按 ec.Interval 周期校准，
// 同样受 ec.MinOffset / ec.MaxOffset 守卫（跳过微小偏移、拒绝异常大跳），
// 避免异常上游把本机时钟带偏后继续对外提供错误时间。
func selfSyncLoop(ec effectiveConfig) {
	interval := time.Duration(ec.Interval) * time.Second
	if interval <= 0 {
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	selfCalibrate := func() {
		corrected, off, _, err := queryNTP(ec.NTPServer, time.Duration(ec.Timeout)*time.Second)
		if err != nil {
			logf("server: NTP 自校准失败: %v", err)
			return
		}
		switch offsetDecision(off.Abs(), ec.MinOffset, ec.MaxOffset) {
		case "skip":
			logf("server: 自校准偏移 %s 小于阈值 %dms，跳过", off.Round(time.Millisecond), ec.MinOffset)
		case "reject":
			logf("server: 自校准偏移 %s 超过大跳阈值 %dms，疑似异常源，拒绝设置",
				off.Abs().Round(time.Millisecond), ec.MaxOffset)
		default:
			if err := setSystemTimeFn(corrected); err != nil {
				logf("server: 自校准设置时间失败: %v", err)
				return
			}
			logf("server: 自身已用 NTP 校准, 偏移=%s", off.Round(time.Millisecond))
		}
	}
	// 启动即先校准一次，之后按周期执行
	selfCalibrate()
	for range ticker.C {
		selfCalibrate()
	}
}
