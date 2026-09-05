package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// runDoctor 执行环境体检：管理员权限 / 配置文件 / 日志目录 / 端口占用 / 时间源连通性。
// 返回进程退出码：0=环境正常，1=存在影响同步功能的问题。
// 输出直接写 stdout（无时间戳），便于交互阅读；探测只查询不修改系统时间。
func runDoctor(ec effectiveConfig) int {
	fmt.Println("goTimeSync doctor 环境诊断")
	fmt.Println(strings.Repeat("=", 56))
	exit := 0

	// 1) 权限
	if isAdmin() {
		fmt.Println("[✓] 管理员权限: 有（可设置系统时间 / 绑定 NTP 端口）")
	} else {
		fmt.Println("[!] 管理员权限: 无 —— 设置系统时间会失败；请右键以管理员身份运行")
		exit = 1
	}

	// 2) 配置文件
	if path := configFilePath(); *configFile == "" && !fileExists(path) {
		fmt.Printf("[i] 配置文件: 未找到 %s（使用默认/命令行配置）\n", path)
	} else {
		path := configFilePath()
		b, err := os.ReadFile(path)
		if err != nil {
			fmt.Printf("[!] 配置文件不可读: %s: %v\n", path, err)
			exit = 1
		} else {
			var fc fileConfig
			if err := json.Unmarshal(b, &fc); err != nil {
				fmt.Printf("[!] 配置文件 JSON 解析失败: %s: %v\n", path, err)
				exit = 1
			} else {
				fmt.Printf("[✓] 配置文件: %s（JSON 合法）\n", path)
			}
		}
	}

	// 3) 日志目录可写性
	if ec.LogFile != "" {
		probe := filepath.Join(ec.LogFile, ".doctor-probe")
		if err := os.WriteFile(probe, []byte("probe"), 0644); err != nil {
			fmt.Printf("[!] 日志目录不可写: %s: %v\n", ec.LogFile, err)
			exit = 1
		} else {
			_ = os.Remove(probe)
			fmt.Printf("[✓] 日志目录可写: %s\n", ec.LogFile)
		}
	} else {
		fmt.Println("[i] 日志目录: 未启用（-log 可开启逐日落盘）")
	}

	// 4) 端口（server 模式相关，信息性，不计入退出码）
	checkUDPPort(":" + *serverNTPPort)
	checkTCPPort(*serverAddr)

	// 5) 时间源连通性
	sources, err := buildChain(ec)
	if err != nil {
		fmt.Printf("[✗] 时间源配置错误: %v\n", err)
		return 1
	}
	timeout := time.Duration(ec.Timeout) * time.Second
	okCount := 0
	for _, s := range sources {
		_, off, delay, err := querySource(s, timeout, ec.HTTPMethod, ec.HTTPTimeLayout)
		if err != nil {
			fmt.Printf("[✗] 源 %s 不可达: %v\n", s.label, err)
			continue
		}
		okCount++
		fmt.Printf("[✓] 源 %s 可达: 偏移=%s 延时=%s\n",
			s.label, off.Round(time.Millisecond), delay.Round(time.Millisecond))
	}
	if okCount == 0 {
		fmt.Println("[✗] 无任何可用时间源 —— 检查网络与地址书写")
		exit = 1
	} else {
		fmt.Printf("[✓] %d/%d 个时间源可用\n", okCount, len(sources))
	}

	fmt.Println(strings.Repeat("=", 56))
	if exit == 0 {
		fmt.Println("结论: 环境正常")
	} else {
		fmt.Println("结论: 存在问题，见上方 [!]/[✗] 条目")
	}
	return exit
}

func checkUDPPort(addr string) {
	c, err := net.ListenPacket("udp", addr)
	if err != nil {
		fmt.Printf("[!] UDP %s 不可绑定: %v", addr, err)
		if runtime.GOOS == "windows" {
			fmt.Print("（可能被 w32time 占用，管理员执行 net stop w32time 可释放）")
		}
		fmt.Println()
		return
	}
	_ = c.Close()
	fmt.Printf("[✓] UDP %s 可绑定（server 模式 NTP 可用）\n", addr)
}

func checkTCPPort(addr string) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("[!] TCP %s 不可绑定: %v\n", addr, err)
		return
	}
	_ = l.Close()
	fmt.Printf("[✓] TCP %s 可绑定（server 模式 HTTP 可用）\n", addr)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
