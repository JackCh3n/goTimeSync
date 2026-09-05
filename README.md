# goTimeSync

> 🔗 GitHub 仓库：https://github.com/JackCh3n/goTimeSync

轻量级跨平台时间同步小工具（Go 编写，**零外部依赖**），支持 **Windows / Linux / macOS**。

兼容 **NTP 协议** 与 **内网 HTTP 时间源** 两种方式，支持按秒级间隔定时同步系统时钟，并可注册为系统开机启动。

## 功能特性

- **NTP 客户端**：依据 RFC5905 用 4 个时间戳（t0/t1/t2/t3）计算时间偏移与网络延时，毫秒级校准。
- **内网 HTTP 时间源**：请求内网地址，按 `RTT/2` 估算偏移并校准系统时间。
- **内置 HTTP 时间服务器**（`server` 模式）：把本机变成内网时间源，对外提供 `/time` 接口（可选后台用 NTP 自校准）。
- **内置 NTP 服务器**（`server` 模式，UDP 123）：`server` 模式可同时启动 NTP 服务端与 HTTP 服务端，使本机**同时充当 NTP + HTTP 双协议时间源**，对内网其它机器提供时间（需管理员，且 123 端口未被占用）。
- **定时同步**：`-interval` 指定秒数，循环执行；同步失败自动退避重试（30s→2m→10m），成功后恢复正周期。
- **只读检查**：`-check` 只打印偏差，不修改系统时间。
- **多次采样防抖**：`-samples N` 连续采样取中位偏移，波动超 `-sample-tolerance-ms` 判为不稳定源并尝试下一个。
- **环境诊断**：`doctor` 一条命令体检管理员权限、端口占用、时间源连通性与配置文件。
- **事件 Webhook**：`-hook-url` 在同步成功/失败/大跳拒绝时 POST JSON 通知，便于接入告警。
- **Windows 原生服务**：`service install` 注册 SCM 服务（开机自启 + 崩溃自动重启），`run`/`server` 均支持 Ctrl+C/SIGTERM 优雅退出。
- **开机启动**：`install` 注册系统级计划任务（SYSTEM 账户，无需登录用户即运行）。

## 编译

需要 Go 1.22+。

- **推荐（自动注入版本号）**：双击 `build.bat`，版本号按 `1.00 + 0.01 × git 提交次数` 自动计算并通过 `-ldflags` 注入 `main.Version`。
- 手动编译：`go build -o goTimeSync.exe .`（此时 `version` 子命令显示 `dev`）。
- **CI 自动发版**：推送即自动构建并发布。`.cnb.yml`（cnb.cool）与 `.github/workflows/release.yml`（GitHub Actions）行为对齐：vet+test → 5 平台交叉编译（windows/linux/darwin × amd64/arm64）→ SHA256 校验和 → 创建 Release 并上传附件；master 推送与手动触发按提交数取版本号，推送 `v*` Tag 则以 Tag 名发版。

版本规则：每提交一次递增 0.01（如第 9 次提交为 `v1.09`），`version` 子命令与 `build.bat` 均遵循此规则。

## 使用

```bash
goTimeSync run                      持续运行，按 -interval 周期同步（失败自动退避重试）
goTimeSync once                     立即同步一次后退出
goTimeSync server                   启动 HTTP 时间服务器，对内网提供时间源
goTimeSync doctor                   环境诊断：管理员权限/端口占用/时间源连通性/配置检查
goTimeSync install                  注册为系统开机启动（计划任务，需管理员）
goTimeSync uninstall                移除开机启动
goTimeSync status                   查看是否已注册开机启动
goTimeSync service install|uninstall|start|stop|status
                                    Windows 原生服务（崩溃自动重启，需管理员，仅 Windows）
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
| `-samples` | 单源连续采样次数，N>1 时取中位偏移，抵御单次网络抖动 | `1` |
| `-sample-tolerance-ms` | 多次采样的偏移波动容差，超限判为源不稳定并尝试下一个 | `100` |
| `-hook-url` | 同步事件 Webhook 地址，事件后 POST JSON（`event/source/offset_ms/delay_ms/error/time`，仅 http/https） | 空 |
| `-hook-events` | 钩子触发事件：`synced,failed,rejected,skipped` | `synced,failed,rejected` |
| `-http-time-layout` | HTTP 源自定义时间布局（Go time layout，如 `"2006-01-02 15:04:05"`），优先于内置格式解析，无时区戳按本机时区解释 | 空 |

server 模式参数：`-server-addr`（监听地址，默认 `:8080`）、`-server-ntp`（后台用 NTP 校准本机时钟，默认 `true`）、`-server-ntp-serve`（同时启动 NTP 服务器 UDP，默认 `true`）、`-server-ntp-port`（NTP 端口，默认 `123` 需管理员，被 w32time 占用时可改其它端口）。server 模式的自校准同样受 `-min-offset` / `-max-offset` 守卫。

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

# 生产建议组合：多次采样防抖 + 大跳保护
goTimeSync.exe run -chain "ntp:pool.ntp.org:123" -samples 3 -max-offset 3600000

# 同步失败 / 大跳拒绝时回调告警接口
goTimeSync.exe run -chain "ntp:pool.ntp.org:123" -hook-url http://ops.local:9000/alert -hook-events failed,rejected

# 内网设备返回非标准时间格式（如纯文本 "2026-09-06 12:00:00"）
goTimeSync.exe run -source http -http-url http://10.0.0.5/time -http-time-layout "2006-01-02 15:04:05"

# 环境诊断（权限/端口/时间源连通性）
goTimeSync.exe doctor
```

## 主备模式（failover）

使用 `-chain` 可指定**按顺序尝试**的时间源列表，某项失败自动切换到下一项，直到成功或全部失败。每项格式为 `ntp:地址` 或 `http:地址`，用英文逗号分隔。

```bash
# 主用 NTP，备用 HTTP
goTimeSync.exe run -chain "ntp:pool.ntp.org:123,http://127.0.0.1:8080/time" -interval 60

# 主用 NTP A，备用 NTP B，备用 NTP C
goTimeSync.exe run -chain "ntp:time1.aliyun.com:123,ntp:time2.aliyun.com:123,ntp:time.windows.com:123" -interval 300

# 开机启动也支持主备链（安装时的参数会原样带入开机任务）
goTimeSync.exe install -chain "ntp:pool.ntp.org:123,http://127.0.0.1:8080/time" -interval 60
```

> 未指定 `-chain` 时回退到旧的 `-source` 单源模式，保持向后兼容。

## HTTP 时间接口约定

`source=http`（或 `-chain` 中的 `http:` 项）时，工具向目标地址发起 GET 请求，按以下优先级取时间：

1. **自定义布局**（若指定 `-http-time-layout`，Go time layout，如 `"2006-01-02 15:04:05"`）：适配返回特殊格式的内网设备；无时区戳按本机时区解释；布局解析失败自动回退下方内置格式。
2. **响应体**（亚秒精度）：本工具自带服务器、自定义 `/time` 接口返回的时间，支持以下格式：
   - JSON：`{"time":"2026-07-06T17:40:59.123Z","unix":1783331459,"unixMs":1783331459123}`（优先用 `time` 字段，含亚秒）
   - 纯 RFC3339 字符串：`2026-07-06T17:40:59.123Z`
   - 纯 Unix 时间戳（秒或毫秒）
3. **响应头 `Date`**（回退，整秒精度）：兼容普通 Web 服务器，如 nginx 返回的 `Date: Tue, 07 Jul 2026 02:41:52 GMT`。

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
goTimeSync.exe run -chain "ntp:<A的IP>:123,http://<A的IP>:8080/time" -interval 60
```

> 端口说明：UDP 123 是系统特权端口，启动 NTP 服务端**必须管理员**。若该端口已被 Windows 自带 `w32time` 占用，先停止它（`net stop w32time`）或改用其它端口（如 `-server-ntp-port 12345`，B 机相应用 `<A的IP>:12345`）。

### 一键配置脚本（config.bat）

项目附带 `config.bat`，面向小白：双击（建议**右键 → 以管理员身份运行**）后按中文提示选择「运行方式」与「时间源模式」即可完成配置并运行 / 注册开机启动，无需记忆命令行参数。

```bat
rem 典型流程：运行方式选 3（开机启动） + 时间源模式选 3（NTP 主 + HTTP 备）
```

#### 文件编码要求（重要）

`build.bat` 与 `config.bat` 必须保存为 **GBK（CP936/ANSI）编码、CRLF 换行、无 BOM**。cmd.exe 按系统 ANSI 代码页（中文 Windows 为 CP936/GBK）解析批处理文件：若保存为 UTF-8（无论有无 BOM），中文会被按 GBK 错误切分成乱码“命令”，文件头 BOM 还会把首行 `@echo off` 的 `@` 一并吞掉，出现 `'锘緻echo' 不是内部或外部命令`，整段脚本失效。

脚本开头两行保持纯 ASCII，保证在任意初始代码页下都能正确解析：

```bat
@echo off
chcp 936 >nul 2>nul
```

第二行把控制台代码页归一到 936，与文件的 GBK 编码保持一致：在中文 Windows 上是无操作；在代码页异常的控制台（含管理员窗口、被改成 65001 的会话）中可自愈。**不要**改回 `chcp 65001` + UTF-8 方案——UTF-8 代码页在管理员窗口等场景会触发 `The system cannot write to the specified device.`。

> 编辑这两个文件时：VS Code 请点右下角编码 → 「通过编码保存」选 **GBK**（GB 2312）；记事本「另存为」编码选 **ANSI**。若编辑器按默认 UTF-8 重新保存，脚本会再次乱码。

#### 以管理员身份运行

设置系统时间、注册开机启动（计划任务）都需要管理员权限。推荐**右键 `config.bat` → 以管理员身份运行**。脚本内已自带管理员检测：若未提权，会提示「当前不是管理员身份」并建议重开。提权后 cmd.exe 的控制台编码行为与非提权一致，`chcp 936` 归一方案在两种模式下均可正常显示中文。

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

### Windows 原生服务（service，可选）

除计划任务外，Windows 还可用 **SCM 原生服务**方式部署（需管理员）：

```bash
goTimeSync.exe service install -chain "ntp:pool.ntp.org:123" -interval 60   # 安装（参数固化为服务启动命令）
goTimeSync.exe service start | stop | status                                # 生命周期管理
goTimeSync.exe service uninstall                                            # 卸载
```

与 `install`（计划任务）的区别：原生服务注册为自动启动，并配置**崩溃自动重启**（5s/60s 两级恢复）；两种方式二选一即可，重复部署会同时运行两个同步进程。

## 环境诊断（doctor）

排障时先跑 `goTimeSync doctor`，一条命令完成体检并给出 `[✓]/[!]/[✗]` 结论：

- 管理员权限（设置系统时间 / 绑定 NTP 端口的前提）
- 配置文件是否存在且 JSON 合法、日志目录是否可写
- UDP NTP 端口与 HTTP 监听端口能否绑定（server 模式相关，含 w32time 占用提示）
- 链上每个时间源的连通性、当前偏移与延时（只查询，不改系统时间）

存在影响同步的问题时退出码为 `1`，可脚本化巡检。

## 事件 Webhook（-hook-url）

配置 `-hook-url` 后，同步事件发生时会向该地址 POST JSON（超时 10 秒，失败仅记日志，不影响同步）：

```json
{
  "event": "rejected",
  "source": "ntp:pool.ntp.org:123",
  "offset_ms": 7200000.000,
  "delay_ms": 12.500,
  "error": "偏移 2h0m0s 超过大跳阈值 3600000ms，疑似异常源，拒绝设置",
  "time": "2026-09-06T12:00:00+08:00"
}
```

触发事件由 `-hook-events` 过滤：`synced`（已写入系统时间）/ `skipped`（低于 min-offset 阈值）/ `rejected`（大跳保护拒绝）/ `failed`（全部源失败或写钟失败），默认 `synced,failed,rejected`。地址仅支持 http/https。

## 注意事项

- 修改系统时间与注册开机启动都需 **以管理员（Windows/root）权限**运行。
- 设置系统时间使用 UTC，工具内部已自动转换时区，无需手动处理。
- 跨平台支持：Windows（`kernel32.SetSystemTime`）/ Linux（`clock_settime`）/ macOS（`settimeofday`）；设置系统时间在各平台会加锁，避免 `run` 与 `server` 同时运行时的并发写竞态。
- NTP 服务器仅应答 **mode=3 且长度恰为 48 字节**的合法客户端请求，响应与请求等长、不放大流量，从机制上避免反射放大攻击。

## Roadmap（计划中）

- **Prometheus /metrics**：server 模式暴露偏移/延时/源健康指标，接入现有监控。
- **NTS（RFC 8915，NTP over TLS）**：面向公网单向时间同步的加密校验，内网工具暂列为后续方向。
- 更多平台（FreeBSD 等）的开机启动支持。
