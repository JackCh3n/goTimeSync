package main

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func TestTimeNTPRoundTrip(t *testing.T) {
	cases := []time.Time{
		time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
		time.Date(2035, 1, 1, 0, 0, 0, 123456789, time.UTC),
		time.Date(1972, 2, 29, 23, 59, 59, 500, time.UTC),
	}
	for _, in := range cases {
		secs, frac := timeToNTP(in)
		got := ntpToTime(secs, frac)
		// NTP 时间戳分数仅 32 位，分辨率约 0.23ns，往返存在亚纳秒级截断误差，属正常。
		if diff := got.Sub(in); diff < -time.Microsecond || diff > time.Microsecond {
			t.Errorf("NTP 时间戳往返偏差过大: in=%v out=%v diff=%v", in, got, diff)
		}
	}
}

func TestNtpBytesToTime(t *testing.T) {
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	secs, frac := timeToNTP(want)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint32(buf[0:4], secs)
	binary.BigEndian.PutUint32(buf[4:8], frac)
	if got := ntpBytesToTime(buf); !got.Equal(want) {
		t.Errorf("ntpBytesToTime = %v, want %v", got, want)
	}
}

// fakeNTPServer 起一个 UDP 假 NTP 服务器，respond 决定 48 字节应答内容。
func fakeNTPServer(t *testing.T, respond func(req []byte) []byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("起 UDP 服务器失败: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1024)
		for {
			n, src, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if _, err := pc.WriteTo(respond(buf[:n]), src); err != nil {
				return
			}
		}
	}()
	return pc.LocalAddr().String()
}

// TestQueryNTPRejectsZeroTimestamp 验证全零 Receive/Transmit 时间戳被拒绝
// （旧行为会算出约 -126 年的荒谬偏移）。
func TestQueryNTPRejectsZeroTimestamp(t *testing.T) {
	addr := fakeNTPServer(t, func(req []byte) []byte {
		resp := buildNTPResponse(req, time.Now(), time.Now())
		// Receive [32:40] 与 Transmit [40:48] 全部清零
		for i := 32; i < 48; i++ {
			resp[i] = 0
		}
		return resp
	})
	if _, _, _, err := queryNTP(addr, 2*time.Second); err == nil {
		t.Fatal("零时间戳应报错，实际成功")
	}
}

// TestQueryNTPRejectsWrongMode 验证非 server 模式（Mode≠4）的应答被拒绝。
func TestQueryNTPRejectsWrongMode(t *testing.T) {
	addr := fakeNTPServer(t, func(req []byte) []byte {
		resp := buildNTPResponse(req, time.Now(), time.Now())
		resp[0] = (resp[0] &^ 0x07) | 5 // Mode 改为 5(broadcast)
		return resp
	})
	if _, _, _, err := queryNTP(addr, 2*time.Second); err == nil {
		t.Fatal("Mode≠4 应报错，实际成功")
	}
}

// TestQueryNTPValidResponse 验证合法应答可正常取时且偏移在合理范围。
func TestQueryNTPValidResponse(t *testing.T) {
	addr := fakeNTPServer(t, func(req []byte) []byte {
		return buildNTPResponse(req, time.Now(), time.Now())
	})
	_, off, delay, err := queryNTP(addr, 2*time.Second)
	if err != nil {
		t.Fatalf("queryNTP 失败: %v", err)
	}
	if off.Abs() > 2*time.Second {
		t.Errorf("偏移过大: %v", off)
	}
	if delay < 0 {
		t.Errorf("延时应非负: %v", delay)
	}
}
