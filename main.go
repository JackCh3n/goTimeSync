package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// Version 由构建脚本通过 -ldflags "-X main.Version=..." 注入；
// 直接 go build 未注入时显示 "dev"。版本规则：1.00 + 0.01 * git 提交次数。
var Version = "dev"

var (
	configFile     = flag.String("config", "", "配置文件路径（默认: 可执行文件同目录/goTimeSync.json）。run 模式每次循环重读")
	source         = flag.String("source", "ntp", "单源模式时间源: ntp | http（未指定 -chain 时生效）")
	chain          = flag.String("chain", "", "主备链：按顺序尝试，用逗号分隔。每项 ntp:地址 或 http:地址。例: ntp:pool.ntp.org:123,http:http://127.0.0.1:8080/time")
	ntpServer      = flag.String("ntp-server", "pool.ntp.org:123", "NTP 服务器地址 (source=ntp 时生效)")
	httpURL        = flag.String("http-url", "http://127.0.0.1:8080/time", "HTTP 时间服务器地址 (source=http 时生效)")
	httpMethod     = flag.String("http-method", "", "HTTP 时间源请求方法 (get|post，默认 get)")
	interval       = flag.Int("interval", 3600, "同步间隔（秒），run 模式生效")
	check          = flag.Bool("check", false, "仅检查时间偏差，不修改系统时间")
	timeoutSec     = flag.Int("timeout", 5, "单次请求超时（秒）")
	logFile        = flag.String("log", "", "日志根目录；日志按 该目录/年月/日.log 逐日写入（quiet 时不输出控制台）")
	statusFile     = flag.String("status-file", "", "同步状态文件路径（默认: 同目录/goTimeSync.status.json）")
	minOffset      = flag.Int64("min-offset", 0, "偏移阈值(ms)：绝对值小于该值则跳过设置（0=不限制）")
	maxOffset      = flag.Int64("max-offset", 0, "大跳保护(ms)：绝对值大于该值视为异常源，拒绝设置并尝试下一个（0=不限制）")
	strategy       = flag.String("strategy", "fallback", "多源策略: fallback(顺序试错) | best(并发择优，取最小延时)")
	serverAddr     = flag.String("server-addr", ":8080", "HTTP 时间服务器监听地址 (server 模式)")
	serverNTP      = flag.Bool("server-ntp", true, "server 模式下是否后台用 NTP 校准本机时钟")
	serverNTPServe = flag.Bool("server-ntp-serve", true, "server 模式下是否同时启动 NTP 服务器(UDP)，使本机兼作 NTP 时间源")
	serverNTPPort  = flag.String("server-ntp-port", "123", "NTP 服务器监听端口 (server 模式, 默认 123，需管理员)")
	quiet          = flag.Bool("quiet", false, "安静模式，仅输出错误（日志文件仍记录）")
)

// timeSource 表示主备链中的一个时间源。
type timeSource struct {
	kind   string // "ntp" 或 "http"
	target string // NTP 地址 或 HTTP URL
	label  string // 日志用标识
}

// buildChain 解析出本次同步要尝试的时间源顺序。
func buildChain(ec effectiveConfig) ([]timeSource, error) {
	if ec.Chain != "" {
		var out []timeSource
		for _, part := range strings.Split(ec.Chain, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			i := strings.Index(part, ":")
			if i <= 0 {
				return nil, fmt.Errorf("无法解析源: %q（格式应为 ntp:地址 或 http:地址）", part)
			}
			kind, target := part[:i], part[i+1:]
			switch kind {
			case "ntp":
				out = append(out, timeSource{kind: "ntp", target: target, label: "ntp:" + target})
			case "http":
				out = append(out, timeSource{kind: "http", target: target, label: "http:" + target})
			default:
				return nil, fmt.Errorf("未知源类型: %q（支持 ntp: / http:）", kind)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("-chain 为空或无法解析")
		}
		return out, nil
	}
	switch ec.Source {
	case "ntp":
		return []timeSource{{kind: "ntp", target: ec.NTPServer, label: "ntp:" + ec.NTPServer}}, nil
	case "http":
		return []timeSource{{kind: "http", target: ec.HTTPURL, label: "http:" + ec.HTTPURL}}, nil
	default:
		return nil, fmt.Errorf("未知时间源: %s（支持 ntp | http）", ec.Source)
	}
}

// chainLabels 返回链中各源的日志标签，用于启动信息。
func chainLabels(ec effectiveConfig) []string {
	ch, err := buildChain(ec)
	if err != nil {
		return []string{"?"}
	}
	labels := make([]string, len(ch))
	for i, s := range ch {
		labels[i] = s.label
	}
	return labels
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}
	cmd := os.Args[1]
	// 子命令之后的参数再交给 flag 解析
	if err := flag.CommandLine.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}
	detectCLISetFlags()

	switch cmd {
	case "run":
		ec := resolveConfig()
		if err := validateConfig(ec); err != nil {
			fmt.Fprintf(os.Stderr, "配置错误: %v\n", err)
			os.Exit(1)
		}
		initLogger(ec.LogFile, ec.Quiet)
		runLoop(ec)
	case "once":
		ec := resolveConfig()
		if err := validateConfig(ec); err != nil {
			fmt.Fprintf(os.Stderr, "配置错误: %v\n", err)
			os.Exit(1)
		}
		initLogger(ec.LogFile, ec.Quiet)
		if err := doSync(ec); err != nil {
			fmt.Fprintf(os.Stderr, "同步失败: %v\n", err)
			os.Exit(1)
		}
	case "server":
		ec := resolveConfig()
		if err := validateConfig(ec); err != nil {
			fmt.Fprintf(os.Stderr, "配置错误: %v\n", err)
			os.Exit(1)
		}
		initLogger(ec.LogFile, ec.Quiet)
		if *serverNTPServe {
			if err := startNTPServer(":" + *serverNTPPort); err != nil {
				fmt.Fprintf(os.Stderr, "警告: NTP 服务器启动失败，仅启用 HTTP 时间服务器: %v\n", err)
			}
		}
		if err := startTimeServer(*serverAddr, *serverNTP, *ntpServer, time.Duration(*interval)*time.Second); err != nil {
			fmt.Fprintf(os.Stderr, "HTTP 时间服务器启动失败: %v\n", err)
			os.Exit(1)
		}
	case "install":
		if err := installStartup(); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	case "uninstall":
		if err := uninstallStartup(); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	case "status":
		if isInstalled() {
			fmt.Println("已注册开机启动（" + taskName + "）")
		} else {
			fmt.Println("未注册开机启动")
		}
	case "version", "-v", "--version":
		fmt.Println("goTimeSync v" + Version)
	default:
		printUsage()
		os.Exit(1)
	}
}

func runLoop(first effectiveConfig) {
	ec := first
	logf("goTimeSync 启动 | 主备链=[%s] | 间隔=%d秒 | 检查=%v | 策略=%s",
		strings.Join(chainLabels(ec), " > "), ec.Interval, ec.Check, ec.Strategy)
	if err := doSync(ec); err != nil {
		fmt.Fprintf(os.Stderr, "初始同步失败: %v\n", err)
	}
	interval := time.Duration(ec.Interval) * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		ec = resolveConfig() // 每次循环重读配置，改文件即生效
		// 热更新：间隔变化时重置 ticker；日志配置变化时重设日志输出（幂等，未变化则无操作）
		if d := time.Duration(ec.Interval) * time.Second; d != interval {
			interval = d
			ticker.Reset(interval)
			logf("同步间隔已热更新为 %d 秒", ec.Interval)
		}
		initLogger(ec.LogFile, ec.Quiet)
		if err := doSync(ec); err != nil {
			fmt.Fprintf(os.Stderr, "[%s] 同步失败: %v\n",
				time.Now().Format("2006-01-02 15:04:05"), err)
		}
	}
}

// doSync 按策略完成一次同步：best 并发择优，fallback 顺序试错。
func doSync(ec effectiveConfig) error {
	timeout := time.Duration(ec.Timeout) * time.Second
	sources, err := buildChain(ec)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fmt.Errorf("无可用的同步源")
	}

	// best 策略：并发请求所有源，取最小延时者（延迟越低通常越近、越可信）
	if ec.Strategy == "best" && len(sources) > 1 {
		type res struct {
			src       timeSource
			corrected time.Time
			offset    time.Duration
			delay     time.Duration
			err       error
		}
		chs := make([]chan res, len(sources))
		for i, s := range sources {
			chs[i] = make(chan res, 1)
			go func(s timeSource, ch chan res) {
				c, o, d, e := querySource(s, timeout, ec.HTTPMethod)
				ch <- res{s, c, o, d, e}
			}(s, chs[i])
		}
		// 总时限：所有并发源共享一个截止时间，避免个别慢源把整体拖到 N*timeout。
		deadline := time.After(timeout)
		var best *res
	loop:
		for i := 0; i < len(chs); i++ {
			var r res
			// 先非阻塞取已就绪的结果，避免与 deadline 分支随机竞争而丢弃已成功返回的源。
			select {
			case r = <-chs[i]:
			default:
				select {
				case r = <-chs[i]:
				case <-deadline:
					logf("best 策略达总时限 %v，停止等待其余源", timeout)
					break loop
				}
			}
			if r.err != nil {
				logf("源 %s 失败: %v", r.src.label, r.err)
				continue
			}
			if best == nil || r.delay < best.delay {
				b := r
				best = &b
			}
		}
		if best == nil {
			writeStatusSafe(ec, "", 0, 0, false, "所有源失败")
			return fmt.Errorf("所有时间源均失败（best 策略）")
		}
		return applyCorrection(ec, best.src, best.corrected, best.offset, best.delay)
	}

	// fallback 策略：顺序尝试，首个成功者用于校准
	var lastErr error
	for _, s := range sources {
		c, o, d, e := querySource(s, timeout, ec.HTTPMethod)
		if e != nil {
			lastErr = e
			logf("源 %s 失败: %v，尝试下一个", s.label, e)
			continue
		}
		return applyCorrection(ec, s, c, o, d)
	}
	writeStatusSafe(ec, "", 0, 0, false, lastErr.Error())
	return fmt.Errorf("所有时间源均失败，最后错误: %w", lastErr)
}

// querySource 按源类型调用对应客户端。
func querySource(s timeSource, timeout time.Duration, httpMethod string) (time.Time, time.Duration, time.Duration, error) {
	switch s.kind {
	case "ntp":
		return queryNTP(s.target, timeout)
	case "http":
		return queryHTTPTime(s.target, timeout, nil, httpMethod)
	default:
		return time.Time{}, 0, 0, fmt.Errorf("未知源类型: %s", s.kind)
	}
}

// validateConfig 在启动时对配置做一次预校验，尽早暴露 chain/source 书写错误。
func validateConfig(ec effectiveConfig) error {
	if ec.Chain != "" {
		if _, err := buildChain(ec); err != nil {
			return err
		}
		return nil
	}
	switch ec.Source {
	case "ntp", "http":
		return nil
	default:
		return fmt.Errorf("未知时间源: %s（支持 ntp | http）", ec.Source)
	}
}

// applyCorrection 处理命中源后的偏移阈值、大跳保护与系统时间写入，并写状态文件。
func applyCorrection(ec effectiveConfig, src timeSource, corrected time.Time, offset, delay time.Duration) error {
	logf("命中源=%s 偏移=%s 延时=%s 校准后=%s",
		src.label,
		offset.Round(time.Millisecond),
		delay.Round(time.Millisecond),
		corrected.Format("2006-01-02 15:04:05.000 MST"),
	)
	if ec.Check {
		logf("仅检查模式，未修改系统时间")
		writeStatusSafe(ec, src.label, offset, delay, true, "check")
		return nil
	}
	switch offsetDecision(offset.Abs(), ec.MinOffset, ec.MaxOffset) {
	case "skip":
		logf("偏移 %s 小于阈值 %dms，跳过设置", offset.Abs().Round(time.Millisecond), ec.MinOffset)
		writeStatusSafe(ec, src.label, offset, delay, true, "skipped(min-offset)")
		return nil
	case "reject":
		msg := fmt.Sprintf("偏移 %s 超过大跳阈值 %dms，疑似异常源，拒绝设置", offset.Abs().Round(time.Millisecond), ec.MaxOffset)
		logf("%s", msg)
		writeStatusSafe(ec, src.label, offset, delay, false, "rejected(max-offset)")
		return fmt.Errorf("%s", msg)
	}
	if err := setSystemTime(corrected); err != nil {
		writeStatusSafe(ec, src.label, offset, delay, false, "set-failed: "+err.Error())
		return fmt.Errorf("设置系统时间失败: %w", err)
	}
	logf("已同步系统时间 -> %s", corrected.Format("2006-01-02 15:04:05 MST"))
	writeStatusSafe(ec, src.label, offset, delay, true, "synced")
	return nil
}

func printUsage() {
	fmt.Print(`goTimeSync - 轻量级时间同步工具（NTP / 内网 HTTP 双协议；server 模式可同时充当 NTP+HTTP 时间源，支持开机启动）

用法:
  goTimeSync run                      持续运行，按 -interval 周期同步（默认 3600 秒）
  goTimeSync once                     立即同步一次后退出
  goTimeSync server                   启动 HTTP 时间服务器，对内网提供时间源
  goTimeSync install                  注册为系统开机启动（Windows: 计划任务 / Linux: systemd / macOS: launchd）
  goTimeSync uninstall                移除开机启动
  goTimeSync status                   查看是否已注册开机启动
  goTimeSync version                  查看版本

通用参数:
  -config string       配置文件路径（默认 同目录/goTimeSync.json），run 模式每次循环重读
  -source string       单源模式时间源: ntp | http（未指定 -chain 时生效，默认 ntp）
  -chain string        主备链：按顺序尝试，逗号分隔。每项 ntp:地址 或 http:地址
  -ntp-server string   NTP 服务器（默认 pool.ntp.org:123）
  -http-url string     HTTP 时间地址（默认 http://127.0.0.1:8080/time）
  -http-method string  HTTP 时间源请求方法 get|post（默认 get）
  -interval int        同步间隔秒数（默认 3600）
  -timeout int         单次请求超时秒数（默认 5）
  -strategy string     多源策略: fallback(顺序试错) | best(并发择优取最小延时)（默认 fallback）
  -min-offset int      偏移阈值(ms)：绝对值小于该值则跳过设置（默认 0=不限制）
  -max-offset int      大跳保护(ms)：绝对值大于该值视为异常源，拒绝设置并尝试下一个（默认 0=不限制）
  -check               仅检查偏差，不修改系统时间
  -log string          日志根目录；按 该目录/年月/日.log 逐日落盘（如 logs\202608\8.log）
  -status-file string  同步状态文件路径（默认 同目录/goTimeSync.status.json，含最近10次历史）
  -quiet               安静模式，仅输出错误（日志文件仍记录）

server 模式参数:
  -server-addr string       HTTP 时间服务器监听地址（默认 :8080）
  -server-ntp bool          后台用 NTP 校准本机时钟（默认 true）
  -server-ntp-serve bool    同时启动 NTP 服务器(UDP)，使本机兼作 NTP 时间源（默认 true）
  -server-ntp-port string   NTP 服务器端口（默认 123，需管理员；若被占用可改用其它端口）

示例:
  # 单源
  goTimeSync run -source ntp -interval 600
  goTimeSync run -source http -http-url http://192.168.1.10:8080/time -interval 60

  # 主备：主用 NTP，备用 HTTP
  goTimeSync run -chain "ntp:pool.ntp.org:123,http:http://127.0.0.1:8080/time" -interval 60

  # 多上游择优（best）：并发请求全部，取最小延时
  goTimeSync run -strategy best -chain "ntp:time1.aliyun.com:123,ntp:time2.aliyun.com:123,http:http://10.0.0.1/time" -interval 60

  # 大跳保护：偏移超过 1 小时视为异常源拒绝
  goTimeSync run -chain "ntp:pool.ntp.org:123" -max-offset 3600000

  # 配置文件驱动（改 goTimeSync.json 即生效，无需重启/重装开机任务）
  goTimeSync run -config goTimeSync.json

  # A 机：同时作为 NTP(123) + HTTP 时间源，并后台用 NTP 自校准（需管理员，且 123 端口未被占用）
  goTimeSync server -server-addr :8080 -server-ntp-port 123

  # B 机：用 NTP 同步 A（假设 A 的 IP 为 192.168.1.10）
  goTimeSync run -source ntp -ntp-server 192.168.1.10:123 -interval 60

  goTimeSync once -chain "ntp:pool.ntp.org:123,http:http://127.0.0.1:8080/time" -check
  goTimeSync install -chain "ntp:pool.ntp.org:123,http:http://127.0.0.1:8080/time" -interval 60   （请以管理员身份运行）
`)
}
