package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Version 由构建脚本通过 -ldflags "-X main.Version=..." 注入；
// 直接 go build 未注入时显示 "dev"。版本规则：1.00 + 0.01 * git 提交次数。
var Version = "dev"

var (
	configFile     = flag.String("config", "", "配置文件路径（默认: 可执行文件同目录/goTimeSync.json）。run 模式每次循环重读")
	source         = flag.String("source", "ntp", "单源模式时间源: ntp | http（未指定 -chain 时生效）")
	chain          = flag.String("chain", "", "主备链：按顺序尝试，用逗号分隔。每项如 ntp:pool.ntp.org:123 或 http://127.0.0.1:8080/time")
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
	samples        = flag.Int("samples", 1, "单源连续采样次数（N>1 时多次采样取中位偏移，抵御单次网络抖动）")
	sampleTolMs    = flag.Int64("sample-tolerance-ms", 100, "多次采样的偏移波动容差(ms)：最大-最小偏移超过该值则本次判为源不稳定")
	hookURL        = flag.String("hook-url", "", "同步事件 Webhook 地址，事件后 POST JSON（字段: event/source/offset_ms/delay_ms/error/time；仅 http/https）")
	hookEvents     = flag.String("hook-events", "synced,failed,rejected", "钩子触发事件，逗号分隔: synced,failed,rejected,skipped")
	httpTimeLayout = flag.String("http-time-layout", "", "HTTP 源自定义时间布局（Go time layout，如 \"2006-01-02 15:04:05\"），优先于内置格式解析")
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
				return nil, fmt.Errorf("无法解析源: %q（格式应为 ntp:地址、http://地址 或 https://地址）", part)
			}
			kind, rest := part[:i], part[i+1:]
			switch kind {
			case "ntp":
				// ntp:pool.ntp.org:123 与 ntp://pool.ntp.org:123 等价
				target := strings.TrimPrefix(rest, "//")
				out = append(out, timeSource{kind: "ntp", target: target, label: "ntp:" + target})
			case "http", "https":
				// 首个冒号会把 http://x 切成 rest="//x"，需还原完整 URL；
				// 旧写法 http:http://x 的 rest 本身含 ://，原样保留
				target := rest
				switch {
				case strings.HasPrefix(rest, "//"):
					target = kind + ":" + rest
				case !strings.Contains(rest, "://"):
					target = "http://" + rest
				}
				out = append(out, timeSource{kind: "http", target: target, label: "http:" + target})
			default:
				return nil, fmt.Errorf("未知源类型: %q（支持 ntp: / http:// / https://）", kind)
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
		if isWindowsService() {
			// 由 Windows 服务控制管理器启动：注册控制处理器后进入同步循环
			if err := runServiceMode(ec); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
			closeLog()
			return
		}
		installSignalHandler()
		runLoop(ec)
		closeLog()
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
		installSignalHandler()
		stopNTP := func() {}
		if *serverNTPServe {
			stop, err := startNTPServer(":" + *serverNTPPort)
			if err != nil {
				fmt.Fprintf(os.Stderr, "警告: NTP 服务器启动失败，仅启用 HTTP 时间服务器: %v\n", err)
			} else {
				stopNTP = stop
			}
		}
		srv := newTimeHTTPServer(ec, *serverAddr, *serverNTP)
		srvErr := make(chan error, 1)
		go func() { srvErr <- srv.ListenAndServe() }()
		select {
		case err := <-srvErr:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintf(os.Stderr, "HTTP 时间服务器启动失败: %v\n", err)
				stopNTP()
				closeLog()
				os.Exit(1)
			}
		case <-shutdownCh:
			logf("收到退出信号，正在停止 HTTP/NTP 服务...")
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = srv.Shutdown(ctx)
			cancel()
			stopNTP()
		}
		closeLog()
	case "doctor":
		ec := resolveConfig()
		os.Exit(runDoctor(ec))
	case "service":
		if err := serviceCommand(flag.Args()); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
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

// shutdownCh 关闭后通知 runLoop / server 模式优雅退出；由信号处理器或 Windows 服务控制处理器触发。
var (
	shutdownOnce sync.Once
	shutdownCh   = make(chan struct{})
)

func requestShutdown() {
	shutdownOnce.Do(func() { close(shutdownCh) })
}

// installSignalHandler 安装 Ctrl+C / SIGTERM 处理：第一次请求优雅退出，第二次强制退出。
func installSignalHandler() {
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		logf("收到退出信号，正在收尾（再按一次可强制退出）...")
		requestShutdown()
		<-sig
		os.Exit(130)
	}()
}

// retryBackoff 依据连续失败次数返回下次重试等待时长：30s → 2m → 10m（封顶）。
func retryBackoff(failCount int) time.Duration {
	switch {
	case failCount <= 1:
		return 30 * time.Second
	case failCount == 2:
		return 2 * time.Minute
	default:
		return 10 * time.Minute
	}
}

func runLoop(first effectiveConfig) {
	ec := first
	logf("goTimeSync 启动 | 主备链=[%s] | 间隔=%d秒 | 检查=%v | 策略=%s",
		strings.Join(chainLabels(ec), " > "), ec.Interval, ec.Check, ec.Strategy)

	prevInterval := time.Duration(ec.Interval) * time.Second
	failCount := 0
	for {
		err := doSync(ec)
		if err != nil {
			failCount++
			fmt.Fprintf(os.Stderr, "[%s] 同步失败: %v\n",
				time.Now().Format("2006-01-02 15:04:05"), err)
		} else {
			failCount = 0
		}

		// 每轮重读配置：改文件即生效；日志配置变化由 initLogger 幂等切换
		ec = resolveConfig()
		initLogger(ec.LogFile, ec.Quiet)

		var wait time.Duration
		if err != nil {
			// 失败退避：30s→2m→10m 重试，避免干等完整间隔；成功后恢复正周期
			wait = retryBackoff(failCount)
			logf("同步失败，%s 后重试（连续第 %d 次失败）", wait, failCount)
		} else {
			if d := time.Duration(ec.Interval) * time.Second; d != prevInterval {
				logf("同步间隔已热更新为 %d 秒", ec.Interval)
			}
			wait = time.Duration(ec.Interval) * time.Second
		}
		prevInterval = time.Duration(ec.Interval) * time.Second

		select {
		case <-time.After(wait):
		case <-shutdownCh:
			logf("goTimeSync 已停止")
			return
		}
	}
}

// doSync 按策略完成一次同步：best 并发择优，fallback 顺序试错。
// 两种策略下，偏移被大跳保护拒绝的源都会被跳过并尝试下一个源；
// 全部失败/被拒时触发 failed 钩子。
func doSync(ec effectiveConfig) error {
	timeout := time.Duration(ec.Timeout) * time.Second
	sources, err := buildChain(ec)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fmt.Errorf("无可用的同步源")
	}

	// best 策略：并发请求所有源，收集成功者按延时从低到高依次尝试；
	// 偏移超过大跳阈值的源视为异常，拒绝后自动尝试下一个（与 -max-offset 帮助文本一致）。
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
				c, o, d, e := querySourceStable(s, ec, timeout)
				ch <- res{s, c, o, d, e}
			}(s, chs[i])
		}
		// 总时限：所有并发源共享一个截止时间；多采样源按采样数放大预算（至少 1 个 timeout），
		// 避免个别慢源把整体拖到 N*timeout*samples。
		budget := timeout * time.Duration(ec.Samples)
		if budget < timeout {
			budget = timeout
		}
		deadline := time.After(budget)
		var ok []res
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
					logf("best 策略达总时限，停止等待其余源")
					break loop
				}
			}
			if r.err != nil {
				logf("源 %s 失败: %v", r.src.label, r.err)
				continue
			}
			ok = append(ok, r)
		}
		if len(ok) == 0 {
			runHook(ec, "failed", "", 0, 0, "所有源失败")
			writeStatusSafe(ec, "", 0, 0, false, "所有源失败")
			return fmt.Errorf("所有时间源均失败（best 策略）")
		}
		sort.Slice(ok, func(i, j int) bool { return ok[i].delay < ok[j].delay })
		var lastErr error
		for _, r := range ok {
			err := applyCorrection(ec, r.src, r.corrected, r.offset, r.delay)
			if err == nil {
				return nil
			}
			if errors.Is(err, errOffsetRejected) {
				lastErr = err
				continue
			}
			return err
		}
		runHook(ec, "failed", "", 0, 0, lastErr.Error())
		writeStatusSafe(ec, "", 0, 0, false, lastErr.Error())
		return lastErr
	}

	// fallback 策略：顺序尝试，首个成功且通过偏移守卫的源用于校准；
	// 偏移超过大跳阈值的源视为异常，拒绝后继续尝试下一个。
	var lastErr error
	for _, s := range sources {
		c, o, d, e := querySourceStable(s, ec, timeout)
		if e != nil {
			lastErr = e
			logf("源 %s 失败: %v，尝试下一个", s.label, e)
			continue
		}
		err := applyCorrection(ec, s, c, o, d)
		if err == nil {
			return nil
		}
		if errors.Is(err, errOffsetRejected) {
			lastErr = err
			continue
		}
		return err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("无可用的同步源")
	}
	runHook(ec, "failed", "", 0, 0, lastErr.Error())
	writeStatusSafe(ec, "", 0, 0, false, lastErr.Error())
	return fmt.Errorf("所有时间源均失败或被拒，最后错误: %w", lastErr)
}

// querySourceStable 对单个源连续采样 ec.Samples 次：全部成功且校准时间极差 ≤ 容差才视为有效，
// 取偏移/延时/校准时间的中位数作为结果。Samples=1 时等价于直接 querySource。
func querySourceStable(s timeSource, ec effectiveConfig, timeout time.Duration) (time.Time, time.Duration, time.Duration, error) {
	if ec.Samples <= 1 {
		return querySource(s, timeout, ec.HTTPMethod, ec.HTTPTimeLayout)
	}
	n := ec.Samples
	tol := time.Duration(ec.SampleToleranceMs) * time.Millisecond
	type sample struct {
		corrected time.Time
		offset    time.Duration
		delay     time.Duration
	}
	ss := make([]sample, 0, n)
	for i := 0; i < n; i++ {
		c, o, d, err := querySource(s, timeout, ec.HTTPMethod, ec.HTTPTimeLayout)
		if err != nil {
			return time.Time{}, 0, 0, fmt.Errorf("采样 %d/%d 失败: %w", i+1, n, err)
		}
		ss = append(ss, sample{c, o, d})
	}
	minC, maxC := ss[0].corrected, ss[0].corrected
	offs := make([]time.Duration, 0, n)
	delays := make([]time.Duration, 0, n)
	for _, x := range ss {
		if x.corrected.Before(minC) {
			minC = x.corrected
		}
		if x.corrected.After(maxC) {
			maxC = x.corrected
		}
		offs = append(offs, x.offset)
		delays = append(delays, x.delay)
	}
	if spread := maxC.Sub(minC); spread > tol {
		return time.Time{}, 0, 0, fmt.Errorf("%d 次采样偏移波动 %s 超过容差 %dms，疑似不稳定源",
			n, spread.Round(time.Millisecond), ec.SampleToleranceMs)
	}
	mi := medianIdx(offs)
	return ss[mi].corrected, offs[mi], delays[medianIdx(delays)], nil
}

// medianIdx 返回按值排序后中位元素的原下标（偶数个取中间偏大者）。
func medianIdx(ds []time.Duration) int {
	idx := make([]int, len(ds))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool { return ds[idx[a]] < ds[idx[b]] })
	return idx[len(idx)/2]
}

// querySource 按源类型调用对应客户端。
func querySource(s timeSource, timeout time.Duration, httpMethod, httpLayout string) (time.Time, time.Duration, time.Duration, error) {
	switch s.kind {
	case "ntp":
		return queryNTP(s.target, timeout)
	case "http":
		return queryHTTPTime(s.target, timeout, nil, httpMethod, httpLayout)
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

// errOffsetRejected 标记“源偏移超过大跳保护阈值被拒绝”。doSync 捕获后应跳过该源继续尝试下一个。
var errOffsetRejected = errors.New("偏移超过大跳保护阈值")

// setSystemTimeFn 指向实际的系统时间写入实现；作为包级变量便于测试注入，避免单测改动真实时钟。
var setSystemTimeFn = setSystemTime

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
		runHook(ec, "skipped", src.label, offset, delay, "")
		return nil
	case "reject":
		msg := fmt.Sprintf("偏移 %s 超过大跳阈值 %dms，疑似异常源，拒绝设置", offset.Abs().Round(time.Millisecond), ec.MaxOffset)
		logf("%s", msg)
		writeStatusSafe(ec, src.label, offset, delay, false, "rejected(max-offset)")
		runHook(ec, "rejected", src.label, offset, delay, msg)
		return fmt.Errorf("%w: %s", errOffsetRejected, msg)
	}
	if err := setSystemTimeFn(corrected); err != nil {
		writeStatusSafe(ec, src.label, offset, delay, false, "set-failed: "+err.Error())
		runHook(ec, "failed", src.label, offset, delay, err.Error())
		return fmt.Errorf("设置系统时间失败: %w", err)
	}
	logf("已同步系统时间 -> %s", corrected.Format("2006-01-02 15:04:05 MST"))
	writeStatusSafe(ec, src.label, offset, delay, true, "synced")
	runHook(ec, "synced", src.label, offset, delay, "")
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
  goTimeSync doctor                   环境诊断：管理员权限/端口占用/时间源连通性/配置检查
  goTimeSync service install|uninstall|start|stop|status
                                      Windows 原生服务（崩溃自动重启；需管理员，仅 Windows）
  goTimeSync version                  查看版本

通用参数:
  -config string       配置文件路径（默认 同目录/goTimeSync.json），run 模式每次循环重读
  -source string       单源模式时间源: ntp | http（未指定 -chain 时生效，默认 ntp）
  -chain string        主备链：按顺序尝试，逗号分隔。每项如 ntp:pool.ntp.org:123 或 http://127.0.0.1:8080/time
  -ntp-server string   NTP 服务器（默认 pool.ntp.org:123）
  -http-url string     HTTP 时间地址（默认 http://127.0.0.1:8080/time）
  -http-method string  HTTP 时间源请求方法 get|post（默认 get）
  -interval int        同步间隔秒数（默认 3600）
  -timeout int         单次请求超时秒数（默认 5）
  -strategy string     多源策略: fallback(顺序试错) | best(并发择优取最小延时)（默认 fallback）
  -min-offset int      偏移阈值(ms)：绝对值小于该值则跳过设置（默认 0=不限制）
  -max-offset int      大跳保护(ms)：绝对值大于该值视为异常源，拒绝设置并尝试下一个（默认 0=不限制）
  -samples int         单源连续采样次数（默认 1；N>1 取中位偏移，抵御单次网络抖动）
  -sample-tolerance-ms int
                       多次采样的偏移波动容差(ms)（默认 100；波动超限判为源不稳定）
  -hook-url string    同步事件 Webhook 地址，事件后 POST JSON（event/source/offset_ms/delay_ms/error/time；仅 http/https）
  -hook-events string  钩子触发事件，逗号分隔: synced,failed,rejected,skipped（默认 synced,failed,rejected）
  -http-time-layout string
                       HTTP 源自定义时间布局（Go time layout，如 "2006-01-02 15:04:05"），优先于内置格式解析
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
