package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// effectiveConfig 是运行时实际生效的配置（默认值 -> 配置文件 -> 命令行覆盖 合并后的结果）。
type effectiveConfig struct {
	Source     string
	Chain      string
	NTPServer  string
	HTTPURL    string
	HTTPMethod string
	Interval   int
	Timeout    int
	Check      bool
	Quiet      bool
	LogFile    string
	StatusFile string
	MinOffset  int64  // 偏移阈值(ms)：绝对值小于该值则跳过设置（0=不限制）
	MaxOffset  int64  // 大跳保护(ms)：绝对值大于该值视为异常源拒绝，并尝试下一个（0=不限制）
	Strategy   string // 多源策略: fallback(顺序试错) | best(并发择优取最小延时)

	Samples           int    // 单源连续采样次数（>1 时取中位偏移，抵御单次网络抖动）
	SampleToleranceMs int64  // 多次采样的偏移波动容差(ms)
	HookURL           string // 同步事件 Webhook 地址（POST JSON）
	HookEvents        string // 钩子触发事件: synced,failed,rejected,skipped
	HTTPTimeLayout    string // HTTP 源自定义时间布局（Go time layout，优先于内置格式）
}

// fileConfig 对应 goTimeSync.json 的字段（JSON 名称即对外约定）。
type fileConfig struct {
	Source     string `json:"source"`
	Chain      string `json:"chain"`
	NTPServer  string `json:"ntp_server"`
	HTTPURL    string `json:"http_url"`
	HTTPMethod string `json:"http_method"`
	Interval   int    `json:"interval"`
	Timeout    int    `json:"timeout"`
	Check      bool   `json:"check"`
	Quiet      bool   `json:"quiet"`
	LogFile    string `json:"log_file"`
	StatusFile string `json:"status_file"`
	MinOffset  int64  `json:"min_offset_ms"`
	MaxOffset  int64  `json:"max_offset_ms"`
	Strategy   string `json:"strategy"`

	Samples           int    `json:"samples"`
	SampleToleranceMs int64  `json:"sample_tolerance_ms"`
	HookURL           string `json:"hook_url"`
	HookEvents        string `json:"hook_events"`
	HTTPTimeLayout    string `json:"http_time_layout"`
}

// cliSetFlags 记录命令行中被显式设置的 flag（用于覆盖配置文件中的同名项）。
var cliSetFlags = map[string]bool{}

// logOut 是日志输出目标；默认 stdout，启用日志文件后为 文件(+stdout) 的多路写。
var (
	logOut io.Writer = os.Stdout
	logMu  sync.Mutex
	// 日志热重设状态：记录当前生效配置，initLogger 据此做幂等切换（run 模式每轮重读配置后调用）。
	logCurRoot  string          // 当前日志根目录（空=不落盘）
	logCurQuiet bool            // 当前是否静音
	logCurFile  *dailyLogWriter // 当前打开的日志 writer（未落盘时为 nil）
)

// timeSetMu 保护对系统时钟的写入。run 模式与 server 模式的后台 NTP 自校准可能并发
// 修改系统时间，若同时运行会造成竞态，故统一加锁串行化。
var timeSetMu sync.Mutex

// detectCLISetFlags 在 flag 解析后调用一次，记录哪些 flag 被显式设置。
func detectCLISetFlags() {
	cliSetFlags = map[string]bool{}
	flag.Visit(func(f *flag.Flag) { cliSetFlags[f.Name] = true })
}

// exeDir 返回当前可执行文件所在目录。
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

// configFilePath 返回默认配置文件路径（可执行文件同目录下的 goTimeSync.json）。
func configFilePath() string {
	if *configFile != "" {
		return *configFile
	}
	return filepath.Join(exeDir(), "goTimeSync.json")
}

// statusFilePath 返回默认状态文件路径（可执行文件同目录下的 goTimeSync.status.json）。
func statusFilePath() string {
	if *statusFile != "" {
		return *statusFile
	}
	return filepath.Join(exeDir(), "goTimeSync.status.json")
}

// dailyLogWriter 按「日志根目录/年月/日.log」组织文件，跨天自动切换到新文件。
// 例如 -log C:\logs 时，2026-08-08 的日志写入 C:\logs\202608\8.log。
type dailyLogWriter struct {
	dir  string
	ym   string // 当前打开的 年月（200601）
	day  int    // 当前打开的 日
	file *os.File
}

// open 打开（或切换）到今天对应的日志文件。
func (w *dailyLogWriter) open() error {
	now := time.Now()
	ym := now.Format("200601")
	day := now.Day()
	if w.file != nil && w.ym == ym && w.day == day {
		return nil
	}
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	dir := filepath.Join(w.dir, ym)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, strconv.Itoa(day)+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	w.ym, w.day, w.file = ym, day, f
	return nil
}

// Write 实现 io.Writer：写前确保文件属于当天，跨天则切换文件。
func (w *dailyLogWriter) Write(p []byte) (int, error) {
	if err := w.open(); err != nil {
		return 0, err
	}
	return w.file.Write(p)
}

// Close 关闭当前打开的日志文件（供日志配置热切换时释放句柄）。
func (w *dailyLogWriter) Close() error {
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// initLogger 配置日志输出（幂等，可在 run 循环中反复调用）：
// 配置未变化时无操作；变化时切换输出并关闭旧 writer，避免句柄泄漏（Windows 下占用文件）。
// logRoot 非空时按「根目录/年月/日.log」逐日落盘；为空则不写文件。
// quiet 模式不输出到控制台（但仍写文件）。
func initLogger(logRoot string, quiet bool) {
	logMu.Lock()
	defer logMu.Unlock()
	if logRoot == logCurRoot && quiet == logCurQuiet {
		return // 配置未变化
	}
	var w io.Writer = os.Stdout
	var dw *dailyLogWriter
	if logRoot != "" {
		d := &dailyLogWriter{dir: logRoot}
		if err := d.open(); err != nil {
			fmt.Fprintf(os.Stderr, "警告: 无法创建日志目录 %s: %v（仅输出到控制台）\n", logRoot, err)
			logRoot = "" // 落盘失败降级为仅控制台
			d = nil
		} else {
			dw = d
			w = d
			if !quiet {
				w = io.MultiWriter(d, os.Stdout)
			}
		}
	}
	if logCurFile != nil {
		_ = logCurFile.Close()
	}
	logOut = w
	logCurRoot = logRoot
	logCurQuiet = quiet
	logCurFile = dw
}

// closeLog 优雅退出前刷新并关闭日志文件句柄，避免落盘内容滞留缓冲或句柄占用。
func closeLog() {
	logMu.Lock()
	defer logMu.Unlock()
	if logCurFile != nil {
		_ = logCurFile.Close()
		logCurFile = nil
	}
	logOut = os.Stdout
	logCurRoot = ""
}

// logf 记录一条带时间戳的信息到日志目标。quiet 是否静音由 initLogger 决定（不静音则同时写控制台）。
func logf(format string, args ...interface{}) {
	logMu.Lock()
	defer logMu.Unlock()
	ts := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(logOut, "[%s] %s\n", ts, fmt.Sprintf(format, args...))
}

// resolveConfig 合并 默认值 -> 配置文件 -> 命令行覆盖，得到运行配置。
// run 模式每次循环都会调用本函数，因此改配置文件即可生效，无需重启进程。
func resolveConfig() effectiveConfig {
	ec := effectiveConfig{
		Source:     *source,
		Chain:      *chain,
		NTPServer:  *ntpServer,
		HTTPURL:    *httpURL,
		HTTPMethod: *httpMethod,
		Interval:   *interval,
		Timeout:    *timeoutSec,
		Check:      *check,
		Quiet:      *quiet,
		LogFile:    *logFile,
		StatusFile: *statusFile,
		MinOffset:  *minOffset,
		MaxOffset:  *maxOffset,
		Strategy:   *strategy,

		Samples:           *samples,
		SampleToleranceMs: *sampleTolMs,
		HookURL:           *hookURL,
		HookEvents:        *hookEvents,
		HTTPTimeLayout:    *httpTimeLayout,
	}
	// 配置文件覆盖（仅应用于非零/非空字段）
	if path := configFilePath(); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			var fc fileConfig
			if json.Unmarshal(b, &fc) == nil {
				if fc.Source != "" {
					ec.Source = fc.Source
				}
				if fc.Chain != "" {
					ec.Chain = fc.Chain
				}
				if fc.NTPServer != "" {
					ec.NTPServer = fc.NTPServer
				}
				if fc.HTTPURL != "" {
					ec.HTTPURL = fc.HTTPURL
				}
				if fc.HTTPMethod != "" {
					ec.HTTPMethod = fc.HTTPMethod
				}
				if fc.Interval != 0 {
					ec.Interval = fc.Interval
				}
				if fc.Timeout != 0 {
					ec.Timeout = fc.Timeout
				}
				if fc.Check {
					ec.Check = true
				}
				if fc.Quiet {
					ec.Quiet = true
				}
				if fc.LogFile != "" {
					ec.LogFile = fc.LogFile
				}
				if fc.StatusFile != "" {
					ec.StatusFile = fc.StatusFile
				}
				if fc.MinOffset != 0 {
					ec.MinOffset = fc.MinOffset
				}
				if fc.MaxOffset != 0 {
					ec.MaxOffset = fc.MaxOffset
				}
				if fc.Strategy != "" {
					ec.Strategy = fc.Strategy
				}
				if fc.Samples != 0 {
					ec.Samples = fc.Samples
				}
				if fc.SampleToleranceMs != 0 {
					ec.SampleToleranceMs = fc.SampleToleranceMs
				}
				if fc.HookURL != "" {
					ec.HookURL = fc.HookURL
				}
				if fc.HookEvents != "" {
					ec.HookEvents = fc.HookEvents
				}
				if fc.HTTPTimeLayout != "" {
					ec.HTTPTimeLayout = fc.HTTPTimeLayout
				}
			} else {
				logf("配置文件 %s 解析失败，使用命令行/默认配置", path)
			}
		}
	}
	// 命令行显式覆盖（优先级最高）
	if cliSetFlags["source"] {
		ec.Source = *source
	}
	if cliSetFlags["chain"] {
		ec.Chain = *chain
	}
	if cliSetFlags["ntp-server"] {
		ec.NTPServer = *ntpServer
	}
	if cliSetFlags["http-url"] {
		ec.HTTPURL = *httpURL
	}
	if cliSetFlags["http-method"] {
		ec.HTTPMethod = *httpMethod
	}
	if cliSetFlags["interval"] {
		ec.Interval = *interval
	}
	if cliSetFlags["timeout"] {
		ec.Timeout = *timeoutSec
	}
	// 显式传入的 -check=false / -quiet=false 也要生效（覆盖配置文件中的 true），
	// 故取 flag 实际值而非硬编码 true（flag.Visit 只记录“被显式设置”，不含值）。
	if cliSetFlags["check"] {
		ec.Check = *check
	}
	if cliSetFlags["quiet"] {
		ec.Quiet = *quiet
	}
	if cliSetFlags["log"] {
		ec.LogFile = *logFile
	}
	if cliSetFlags["status-file"] {
		ec.StatusFile = *statusFile
	}
	if cliSetFlags["min-offset"] {
		ec.MinOffset = *minOffset
	}
	if cliSetFlags["max-offset"] {
		ec.MaxOffset = *maxOffset
	}
	if cliSetFlags["strategy"] {
		ec.Strategy = *strategy
	}
	if cliSetFlags["samples"] {
		ec.Samples = *samples
	}
	if cliSetFlags["sample-tolerance-ms"] {
		ec.SampleToleranceMs = *sampleTolMs
	}
	if cliSetFlags["hook-url"] {
		ec.HookURL = *hookURL
	}
	if cliSetFlags["hook-events"] {
		ec.HookEvents = *hookEvents
	}
	if cliSetFlags["http-time-layout"] {
		ec.HTTPTimeLayout = *httpTimeLayout
	}

	if ec.Interval < 1 {
		ec.Interval = 1
	}
	if ec.Timeout < 1 {
		ec.Timeout = 1
	}
	if ec.Samples < 1 {
		ec.Samples = 1
	}
	if ec.SampleToleranceMs < 0 {
		ec.SampleToleranceMs = 0
	}
	if ec.Strategy == "" {
		ec.Strategy = "fallback"
	}
	if ec.HookEvents == "" {
		ec.HookEvents = "synced,failed,rejected"
	}
	return ec
}

// syncStatus 是写入状态文件的 JSON 结构。
type syncStatus struct {
	LastSync string       `json:"last_sync"`
	Source   string       `json:"source"`
	OffsetMs float64      `json:"offset_ms"`
	DelayMs  float64      `json:"delay_ms"`
	OK       bool         `json:"ok"`
	Action   string       `json:"action"`
	Error    string       `json:"error,omitempty"`
	History  []syncRecord `json:"history,omitempty"` // 最近 N 次同步历史（新记录在前）
}

// syncRecord 保存单次同步的摘要，用于历史回溯与健康监控。
type syncRecord struct {
	Time     string  `json:"time"`
	Source   string  `json:"source"`
	OffsetMs float64 `json:"offset_ms"`
	DelayMs  float64 `json:"delay_ms"`
	OK       bool    `json:"ok"`
	Action   string  `json:"action"`
}

// maxHistory 状态文件保留的历史同步记录条数。
const maxHistory = 10

// writeStatus 原子写入同步状态文件（先写临时文件再 rename），并保留最近 maxHistory 条历史。
func writeStatus(path string, st syncStatus) {
	if path == "" {
		return
	}
	cur := syncStatus{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &cur)
	}
	// 追加本次记录到历史头部，截断到 maxHistory 条
	rec := syncRecord{
		Time:     st.LastSync,
		Source:   st.Source,
		OffsetMs: st.OffsetMs,
		DelayMs:  st.DelayMs,
		OK:       st.OK,
		Action:   st.Action,
	}
	hist := append([]syncRecord{rec}, cur.History...)
	if len(hist) > maxHistory {
		hist = hist[:maxHistory]
	}
	st.History = hist

	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// writeStatusSafe 是 doSync 内部使用的便捷封装：优先使用配置解析后的状态文件路径，回退到默认路径。
func writeStatusSafe(ec effectiveConfig, src string, offset, delay time.Duration, ok bool, action string) {
	path := ec.StatusFile
	if path == "" {
		path = statusFilePath()
	}
	writeStatus(path, syncStatus{
		LastSync: time.Now().Format(time.RFC3339),
		Source:   src,
		OffsetMs: float64(offset.Microseconds()) / 1000.0,
		DelayMs:  float64(delay.Microseconds()) / 1000.0,
		OK:       ok,
		Action:   action,
	})
}

// offsetDecision 根据绝对偏移返回处置决定：apply(应用) | skip(小于阈值跳过) | reject(超过大跳阈值拒绝)。
func offsetDecision(absOff time.Duration, minMs, maxMs int64) string {
	if maxMs > 0 && absOff > time.Duration(maxMs)*time.Millisecond {
		return "reject"
	}
	if minMs > 0 && absOff < time.Duration(minMs)*time.Millisecond {
		return "skip"
	}
	return "apply"
}
