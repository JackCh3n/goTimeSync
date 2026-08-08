package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

// ntpEpochOffset 是 1900-01-01 (NTP 纪元) 与 1970-01-01 (Unix 纪元) 之间的秒数差。
const ntpEpochOffset = 2208988800

// queryNTP 向 NTP 服务器发送请求，依据 RFC5905 的 4 个时间戳计算时间偏移与网络延时，
// 返回校准后的当前时间、offset（服务端 - 客户端）以及往返延时。
func queryNTP(server string, timeout time.Duration) (corrected time.Time, offset, delay time.Duration, err error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	raddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return time.Time{}, 0, 0, fmt.Errorf("解析地址失败: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return time.Time{}, 0, 0, fmt.Errorf("连接失败: %w", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))

	// 构造 48 字节请求包：LI=0, VN=3, Mode=3(client) => 0x1B
	req := make([]byte, 48)
	req[0] = 0x1B

	t0 := time.Now()
	secs, frac := timeToNTP(t0)
	binary.BigEndian.PutUint32(req[40:], secs)
	binary.BigEndian.PutUint32(req[44:], frac)

	if _, err = conn.Write(req); err != nil {
		return time.Time{}, 0, 0, fmt.Errorf("发送请求失败: %w", err)
	}

	resp := make([]byte, 48)
	n, err := conn.Read(resp)
	if err != nil {
		return time.Time{}, 0, 0, fmt.Errorf("读取响应失败: %w", err)
	}
	t3 := time.Now()
	if n < 48 {
		return time.Time{}, 0, 0, fmt.Errorf("响应包长度异常: %d 字节（应 48）", n)
	}
	// 校验响应：Mode 应为 4(server)；stratum=0 表示 Kiss-of-Death（拒绝并报错，避免用零时间戳算出荒谬偏移）
	if resp[0]&0x07 != 4 {
		return time.Time{}, 0, 0, fmt.Errorf("响应 Mode 异常: %d（应为 4/server）", resp[0]&0x07)
	}
	if resp[1] == 0 {
		return time.Time{}, 0, 0, fmt.Errorf("服务器返回 Kiss-of-Death (stratum=0), 参考标识: %q", resp[12:16])
	}

	// 响应包中：接收时间戳 t1 位于 [32:40]，发送时间戳 t2 位于 [40:48]
	t1 := ntpBytesToTime(resp[32:40])
	t2 := ntpBytesToTime(resp[40:48])

	t0n := t0.UnixNano()
	t1n := t1.UnixNano()
	t2n := t2.UnixNano()
	t3n := t3.UnixNano()

	// offset = ((t1 - t0) + (t2 - t3)) / 2  （服务端时间 - 客户端时间）
	offset = time.Duration(((t1n-t0n)+(t2n-t3n))/2) * time.Nanosecond
	// delay  = (t3 - t0) - (t2 - t1)
	delay = time.Duration((t3n-t0n)-(t2n-t1n)) * time.Nanosecond
	corrected = t3.Add(offset)

	return corrected, offset, delay, nil
}

func timeToNTP(t time.Time) (uint32, uint32) {
	t = t.UTC()
	secs := uint32(t.Unix() + ntpEpochOffset)
	frac := uint32(float64(t.Nanosecond()) * (1 << 32) / 1e9)
	return secs, frac
}

func ntpBytesToTime(b []byte) time.Time {
	sec := binary.BigEndian.Uint32(b[0:4])
	frac := binary.BigEndian.Uint32(b[4:8])
	return ntpToTime(sec, frac)
}

// secFixed16_16 把时长转换为 NTP 的 16.16 定点秒（用于 Root Delay / Root Dispersion 等字段）。
func secFixed16_16(d time.Duration) uint32 {
	if d <= 0 {
		return 0
	}
	secs := uint32(d / time.Second)
	frac := uint32((d % time.Second) << 16 / time.Second)
	return secs<<16 | frac
}

func ntpToTime(sec, frac uint32) time.Time {
	unix := int64(sec) - ntpEpochOffset
	nsec := int64(float64(frac) * 1e9 / (1 << 32))
	return time.Unix(unix, nsec).UTC()
}

// startNTPServer 启动一个 NTP 服务端（UDP），把本机当前系统时间作为时间源应答给客户端。
// 它读取 mode=3 的客户端请求，回复标准 mode=4 的服务器报文（RFC5905）。
// 注意：绑定 123 端口需要管理员权限，且该端口不能被其它程序（如 Windows 的 w32time）占用。
// 函数在独立 goroutine 中运行，不阻塞调用方；启动失败返回 error（调用方可选择仅保留 HTTP 服务）。
//
// 安全加固：仅应答 mode=3 且长度恰好 48 字节的合法客户端请求，其余一律丢弃。
// 响应与请求等长（48 字节对 48 字节），不放大流量，从机制上避免 NTP 反射/放大攻击。
func startNTPServer(addr string) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("启动 NTP 服务器失败(需管理员且端口未被占用): %w", err)
	}
	logf("NTP 服务器已启动: %s (UDP)", addr)
	go func() {
		buf := make([]byte, 1024)
		for {
			n, src, err := pc.ReadFrom(buf)
			if err != nil {
				logf("NTP 读取错误: %v", err)
				_ = pc.Close()
				return
			}
			// 来源校验：NTP 请求固定 48 字节，且应为客户端模式(mode=3)；否则丢弃。
			// 不做源端口 123 的强制校验，以兼容本工具自身（使用随机临时源端口）及其它标准客户端。
			if n != 48 || buf[0]&0x07 != 3 {
				continue
			}
			recv := time.Now()
			resp := buildNTPResponse(buf[:n], recv, time.Now())
			if _, err := pc.WriteTo(resp, src); err != nil {
				logf("NTP 写回错误: %v", err)
			}
		}
	}()
	return nil
}

// buildNTPResponse 依据客户端请求(req)构造标准 NTP 服务器响应(mode=4)。
// recv 为服务端收到请求的时刻，xmit 为构造响应的时刻（均取自本机系统时间）。
func buildNTPResponse(req []byte, recv, xmit time.Time) []byte {
	resp := make([]byte, 48)
	// LI=0(无警告) | VN=请求版本(默认3) | Mode=4(server)
	vn := (req[0] >> 3) & 0x07
	if vn == 0 {
		vn = 3
	}
	resp[0] = (0 << 6) | (vn << 3) | 4
	resp[1] = 2      // stratum 2：二级服务器（表示本机时钟已与上游同步）
	resp[2] = req[2] // poll：回显客户端轮询间隔
	resp[3] = 0xFA   // precision：约 -6（1/64 秒级别，仅作示意）
	// Root Delay / Root Dispersion 填充合理的小值（16.16 定点秒），让标准客户端能正确计算抖动。
	binary.BigEndian.PutUint32(resp[4:8], secFixed16_16(5*time.Millisecond))
	binary.BigEndian.PutUint32(resp[8:12], secFixed16_16(time.Millisecond))
	// Reference Identifier：LOCL 表示本地时钟（本机已自校准）
	resp[12], resp[13], resp[14], resp[15] = 'L', 'O', 'C', 'L'
	// Reference Timestamp：以发送时刻填充（简化实现）
	rs, rf := timeToNTP(xmit)
	binary.BigEndian.PutUint32(resp[16:], rs)
	binary.BigEndian.PutUint32(resp[20:], rf)
	// Originate Timestamp：回声客户端的 Transmit Timestamp（请求包 [40:48]）
	copy(resp[24:32], req[40:48])
	// Receive Timestamp：服务端收到请求的时刻
	rs, rf = timeToNTP(recv)
	binary.BigEndian.PutUint32(resp[32:], rs)
	binary.BigEndian.PutUint32(resp[36:], rf)
	// Transmit Timestamp：服务端发送响应的时刻
	rs, rf = timeToNTP(xmit)
	binary.BigEndian.PutUint32(resp[40:], rs)
	binary.BigEndian.PutUint32(resp[44:], rf)
	return resp
}
