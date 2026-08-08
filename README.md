# goTimeSync

> 🔗 GitHub 仓库：https://github.com/JackCh3n/goTimeSync

轻量级跨平台时间同步小工具（Go 编写，**零外部依赖**），支持 **Windows / Linux / macOS**。

兼容 **NTP 协议** 与 **内网 HTTP 时间源** 两种方式，支持按秒级间隔定时同步系统时钟，并可注册为系统开机启动。

## 功能特性

- **NTP 客户端**：依据 RFC5905 用 4 个时间戳（t0/t1/t2/t3）计算时间偏移与网络延时，毫秒级校准。
- **内网 HTTP 时间源**：请求内网地址，按 `RTT/2` 估算偏移并校准系统时间。
- **内置 HTTP 时间服务器**（`server` 模式）：把本机变成内网时间源，对外提供 `/time` 接口（可选后台用 NTP 自校准）。
- **内置 NTP 服务器**（`server` 模式，UDP 123）：`server` 模式可同时启动 NTP 服务端与 HTTP 服务端，使本机**同时充当 NTP + HTTP 双协议时间源**，对内网其它机器提供时间（需管理员，且 123 端口未被占用）。
- **定时同步**：`-interval` 指定秒数，循环执行。
- **只读检查**：`-check` 只打印偏差，不修改系统时间。
- **开机启动**：`install` 注册系统级计划任务（SYSTEM 账户，无需登录用户即运行）。

## 编译

需要 Go 1.22+。

- **推荐（自动注入版本号）**：双击 `build.bat`，版本号按 `1.00 + 0.01 × git 提交次数` 自动计算并通过 `-ldflags` 注入 `main.Version`。
- 手动编译：`go build -o goTimeSync.exe .`（此时 `version` 子命令显示 `dev`）。

版本规则：每提交一次递增 0.01（如第 9 次提交为 `v1.09`），`version` 子命令与 `build.bat` 均遵循此规则。

## 使用

```bash
goTimeSync run                      持续运行，按 -interval 周期同步（默认 3600 秒）
goTimeSync once                     立即同步一次后退出
goTimeSync server                   启动 HTTP 时间服务器，对内网提供时间源
goTimeSync install                  注册为系统开机启动（计划任务，需管理员）
goTimeSync uninstall                移除开机启动
goTimeSync status                   查看是否已注册开机启动
goTimeSync version                  查看版本
```

### 通用参数

| 参数 | 说明 | 默认 |
|------|------|------|
| `-source` | 单源模式时间源：`ntp` \| `http`（未指定 `-chain` 时生效） | `ntp` |
| `-chain` | 主备链：按顺序尝试，逗号分隔，每项 `ntp:地址` 或 `http:地址` | 空（用 `-source`） |
| `-ntp-server` | NTP 服务器地址（source=ntp 时生效） | `pool.ntp.org:123` |
| `-http-url` | HTTP 时间服务器地址（source=http 时生效） | `http://127.0.0.1:8080/time` |
| `-http-method` | HTTP 时间源请求方法：`get` \| `post`（内网服务若只暴露 POST 接口时用 post） | `get` |
| `-interval` | 同步间隔（秒），run 模式生效 | `3600` |
| `-timeout` | 单次请求超时（秒） | `5` |
| `-check` | 仅检查时间偏差，不修改系统时间 | `false` |
| `-quiet` | 安静模式，仅输出错误 | `false` |

server 模式参数：`-server-addr`（监听地址，默认 `:8080`）、`-server-ntp`（后台用 NTP 校准本机时钟，默认 `true`）。

### 示例

```bash
# NTP，每 10 分钟同步一次（单源模式）
goTimeSync.exe run -source ntp -interval 600

# 内网 HTTP 时间源，每 60 秒同步（单源模式）
goTimeSync.exe run -source http -http-url http://127.0.0.1:8080/time -interval 60

# 只检查偏差不改系统时间
goTimeSync.exe once -source ntp -check

# 把本机作为内网时间源
goTimeSync.exe server -server-addr :8080

# 注册 / 查看 / 移除开机启动（需管理员）
goTimeSync.exe install
goTimeSync.exe status
goTimeSync.exe uninstall
```

## 主备模式（failover）

使用 `-chain` 可指定**按顺序尝试**的时间源列表，某项失败自动切换到下一项，直到成功或全部失败。每项格式为 `ntp:地址` 或 `http:地址`，用英文逗号分隔。

```bash
# 主用 NTP，备用 HTTP
goTimeSync.exe run -chain "ntp:pool.ntp.org:123,http:http://127.0.0.1:8080/time" -interval 60

# 主用 NTP A，备用 NTP B，备用 NTP C
goTimeSync.exe run -chain "ntp:time1.aliyun.com:123,ntp:time2.aliyun.com:123,ntp:time.windows.com:123" -interval 300

# 开机启动也支持主备链（安装时的参数会原样带入开机任务）
goTimeSync.exe install -chain "ntp:pool.ntp.org:123,http:http://127.0.0.1:8080/time" -interval 60
```

> 未指定 `-chain` 时回退到旧的 `-source` 单源模式，保持向后兼容。

## HTTP 时间接口约定

`source=http`（或 `-chain` 中的 `http:` 项）时，工具向目标地址发起 GET 请求，按以下优先级取时间：

1. **响应体**（优先，亚秒精度）：本工具自带服务器、自定义 `/time` 接口返回的时间，支持以下格式：
   - JSON：`{"time":"2026-07-06T17:40:59.123Z","unix":1783331459,"unixMs":1783331459123}`（优先用 `time` 字段，含亚秒）
   - 纯 RFC3339 字符串：`2026-07-06T17:40:59.123Z`
   - 纯 Unix 时间戳（秒或毫秒）
2. **响应头 `Date`**（回退，整秒精度）：兼容普通 Web 服务器，如 nginx 返回的 `Date: Tue, 07 Jul 2026 02:41:52 GMT`。

内置 `server` 模式返回上述 JSON（路径 `/time`），可直接作为内网时间源使用，且对外提供亚秒级精度。借助 `Date` 头兼容，任意正常 Web 站点（如 `http://127.0.0.1:8080/time`）也能作为粗略时间源（整秒精度）。

### 用 nginx 直接提供时间源（无需运行本软件）

工具已兼容 HTTP `Date` 响应头，而 **nginx 对任何响应都会自动带上 `Date` 头**（例如 `Date: Tue, 07 Jul 2026 02:41:52 GMT`）。因此：

- **最简方案（零配置）**：直接把任意可达的 nginx 站点地址当作时间源即可，例如 `-http-url http://127.0.0.1:8080/time`，工具自动读取 `Date` 头取时（精度到秒）。
- **推荐方案（独立 `/time` 接口，返回 RFC3339）**：在 nginx 配置中增加一段，利用 nginx 内置变量 `$time_iso8601`（**无需任何第三方模块**）：

  ```nginx
  server {
      listen 8888;
      server_name _;

      # 独立时间接口，返回当前 RFC3339 时间（如 2026-07-07T02:41:52+00:00）
      location = /time {
          default_type application/json;
          add_header Access-Control-Allow-Origin "*" always;
          return 200 '{"time":"$time_iso8601"}';
      }

      # 健康检查
      location = /health {
          return 200 "ok";
      }
  }
  ```

  重载配置：`nginx -s reload`。随后使用 `goTimeSync.exe run -source http -http-url http://<服务器IP>:8888/time -interval 60` 即可。

> 说明：nginx 内置变量 `$msec` 可返回带毫秒的浮点时间（如 `1783331459.123`），但工具的 `unix` 字段要求整数，故示例只用 `$time_iso8601`（RFC3339 字符串）。若只需整秒精度，最简方案的 `Date` 头已足够。

### 让本机同时充当 NTP + HTTP 时间源（server 模式）

`server` 模式默认**同时**启动两个服务：

- **NTP 服务端**（UDP，默认端口 `123`）：按 RFC5905 应答 mode=3 客户端请求，报文时间戳取自本机系统时间。任何标准 NTP 客户端（含本工具的 `-source ntp`、Windows `w32tm`、Linux `chrony/ntpd`）都能直接同步到本机。
- **HTTP 时间服务端**（默认 `:8080`）：对外提供 `/time` 接口（见上）。

两个服务之间，本机时钟由 `-server-ntp true`（默认开启）**后台用 NTP 自校准**保持准确，因此对外提供的时间也是准确的。

```bash
# A 机（需管理员，且 123 端口未被 w32time 等占用）：
goTimeSync.exe server -server-addr :8080 -server-ntp-port 123

# B 机用标准 NTP 同步 A：
goTimeSync.exe run -source ntp -ntp-server <A的IP>:123 -interval 60

# 或 B 机主备：主用 A 的 NTP，备用 A 的 HTTP
goTimeSync.exe run -chain "ntp:<A的IP>:123,http:http://<A的IP>:8080/time" -interval 60
```

> 端口说明：UDP 123 是系统特权端口，启动 NTP 服务端**必须管理员**。若该端口已被 Windows 自带 `w32time` 占用，先停止它（`net stop w32time`）或改用其它端口（如 `-server-ntp-port 12345`，B 机相应用 `<A的IP>:12345`）。

### 一键配置脚本（config.bat）

项目附带 `config.bat`，面向小白：双击（建议**右键 → 以管理员身份运行**）后按中文提示选择「运行方式」与「时间源模式」即可完成配置并运行 / 注册开机启动，无需记忆命令行参数。

```bat
rem 典型流程：运行方式选 3（开机启动） + 时间源模式选 3（NTP 主 + HTTP 备）
```

#### 文件编码要求（重要）

`config.bat` 必须保存为 **UTF-8 with BOM** 编码。脚本首行 `@chcp 65001 >nul` 仅切换**控制台输出**代码页为 UTF-8，**不影响** cmd.exe 读取 .bat 文件的方式。若文件为 UTF-8 无 BOM，cmd.exe 会按系统 ANSI 代码页（中文 Windows 为 CP936/GBK）解析脚本中的中文，导致字节错位、控制台无法写入，出现以下错误：

```
============================================================
The system cannot write to the specified device.
The system cannot write to the specified device.
============================================================
```

> Windows 10+ 的 cmd.exe 识别 UTF-8 BOM 后会按 UTF-8 读取脚本内容，配合 `chcp 65001` 即可正确显示中文。**请勿用记事本“另存为 ANSI”**，否则在非中文系统或代码页不是 936 的机器上仍会乱码。

#### 以管理员身份运行

设置系统时间、注册开机启动（计划任务）都需要管理员权限。推荐**右键 `config.bat` → 以管理员身份运行**。脚本内已自带管理员检测：若未提权，会提示「当前不是管理员身份」并建议重开。提权后 cmd.exe 的控制台编码行为与非提权一致，UTF-8 BOM 方案在两种模式下均可正常显示中文。

## 修改运行参数

`run` 模式的参数在**启动时**读取：

- **手动运行**：直接 `Ctrl+C` 停止，再用新参数启动即可。
- **已注册开机启动（`install`）**：计划任务命令在注册时即固定，需重新 `install`（带 `/f` 覆盖同名任务）或先 `uninstall` 再 `install`，才能永久变更 source / 地址 / 间隔。

## 日志与状态文件

- **日志落盘**：`-log <目录>` 启用后，日志按 `目录/年月/日.log` 组织，跨天自动切换到新文件。例如 `-log C:\logs` 时，2026-08-08 的日志写入 `C:\logs\202608\8.log`。不传 `-log` 则仅输出到控制台。
- **状态文件**：`-status-file`（默认 `同目录/goTimeSync.status.json`）记录最近一次同步结果，并保留最近 **10 次**同步历史（`history` 数组，新记录在前），便于回溯与健康监控。

## 开机启动（跨平台）

- **Windows**：`install` 通过系统计划任务以 SYSTEM 身份在系统启动时运行（需管理员）。若检测到更名前残留的旧任务 `WinTimeSync`，会自动删除迁移到 `GoTimeSync`。
- **Linux**：`install` 生成用户级 **systemd 单元**（`~/.config/systemd/user/gotimesync.service`），并打印启用命令（`systemctl --user enable --now gotimesync`）。root 级可复制到 `/etc/systemd/system/`。
- **macOS**：`install` 生成用户级 **LaunchAgent**（`~/Library/LaunchAgents/com.gotimesync.plist`），并打印加载命令（`launchctl load -w`）。系统级可放 `/Library/LaunchDaemons/`。

## 注意事项

- 修改系统时间与注册开机启动都需 **以管理员（Windows/root）权限**运行。
- 设置系统时间使用 UTC，工具内部已自动转换时区，无需手动处理。
- 跨平台支持：Windows（`kernel32.SetSystemTime`）/ Linux（`clock_settime`）/ macOS（`settimeofday`）；设置系统时间在各平台会加锁，避免 `run` 与 `server` 同时运行时的并发写竞态。
- NTP 服务器仅应答 **mode=3 且长度恰为 48 字节**的合法客户端请求，响应与请求等长、不放大流量，从机制上避免反射放大攻击。

## Roadmap（计划中）

- **NTS（RFC 8915，NTP over TLS）**：面向公网单向时间同步的加密校验，内网工具暂列为后续方向。
- 更多平台（FreeBSD 等）的开机启动支持。
