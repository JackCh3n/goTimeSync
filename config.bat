@chcp 65001 >nul
@echo off
setlocal enabledelayedexpansion
cd /d "%~dp0"

echo ============================================================
echo          winTimeSync 时间同步 配置向导
echo      （面向小白，按提示选择即可）
echo ============================================================
echo.

:: 检查程序是否存在
if not exist winTimeSync.exe (
    echo [错误] 未找到 winTimeSync.exe，请先编译（go build -o winTimeSync.exe .）。
    echo.
    pause
    exit /b 1
)

:: 管理员权限检查（设置系统时间 / 注册开机启动需要）
net session >nul 2>&1
if %errorLevel% neq 0 (
    echo [提示] 当前不是管理员身份。
    echo         设置系统时间、注册开机启动会失败。
    echo         建议：右键本文件 - 以管理员身份运行。
    echo.
    pause
)

echo 请选择【运行方式】：
echo   1) 立即测试一次（仅查看时间偏差，不修改系统时间）
echo   2) 立即开始持续同步（前台运行，按 Ctrl+C 停止）
echo   3) 注册为开机启动（系统级计划任务，重启后自动运行，需管理员）
echo.
set /p RUNMODE=请输入数字 [1-3]：

echo.
echo 请选择【时间源模式】：
echo   1) 单一 NTP 服务器（如阿里云 / 国家授时中心）
echo   2) 单一 HTTP 时间地址（如内网 nginx 门户）
echo   3) 主备：NTP 主 + HTTP 备
echo   4) 主备：多个 NTP 服务器（A 主，B / C 备）
echo.
set /p SRCMODE=请输入数字 [1-4]：

set "ARGS="

if "%SRCMODE%"=="1" (
    set /p NTP=请输入 NTP 服务器[默认 pool.ntp.org:123]：
    if "!NTP!"=="" set "NTP=pool.ntp.org:123"
    set "ARGS=-source ntp -ntp-server !NTP!"
) else if "%SRCMODE%"=="2" (
    set /p URL=请输入 HTTP 时间地址[默认 http://127.0.0.1:8080/time]：
    if "!URL!"=="" set "URL=http://127.0.0.1:8080/time"
    set "ARGS=-source http -http-url !URL!"
) else if "%SRCMODE%"=="3" (
    set /p NTP=请输入主用 NTP 服务器[默认 pool.ntp.org:123]：
    if "!NTP!"=="" set "NTP=pool.ntp.org:123"
    set /p URL=请输入备用 HTTP 地址[默认 http://127.0.0.1:8080/time]：
    if "!URL!"=="" set "URL=http://127.0.0.1:8080/time"
    set "ARGS=-chain ntp:!NTP!,http:!URL!"
) else if "%SRCMODE%"=="4" (
    set /p N1=主用 NTP[默认 time1.aliyun.com:123]：
    if "!N1!"=="" set "N1=time1.aliyun.com:123"
    set /p N2=备用 NTP 1[默认 time2.aliyun.com:123]：
    if "!N2!"=="" set "N2=time2.aliyun.com:123"
    set /p N3=备用 NTP 2[默认 time.windows.com:123]：
    if "!N3!"=="" set "N3=time.windows.com:123"
    set "ARGS=-chain ntp:!N1!,ntp:!N2!,ntp:!N3!"
) else (
    echo 无效选择，使用默认单一 NTP。
    set "ARGS=-source ntp"
)

echo.
set /p INTERVAL=请输入同步间隔秒数[默认 3600]：
if "!INTERVAL!"=="" set "INTERVAL=3600"
set "ARGS=!ARGS! -interval !INTERVAL!"

echo.
echo ------------------- 配置预览 -------------------
echo   winTimeSync.exe !ARGS!
echo -------------------------------------------------

if "%RUNMODE%"=="1" (
    echo.
    echo 正在执行【一次测试（仅检查偏差）】...
    winTimeSync.exe once !ARGS! -check
) else if "%RUNMODE%"=="2" (
    echo.
    echo 开始【持续同步】（按 Ctrl+C 停止）...
    winTimeSync.exe run !ARGS!
) else if "%RUNMODE%"=="3" (
    echo.
    echo 正在【注册开机启动】...
    winTimeSync.exe install !ARGS!
    echo.
    winTimeSync.exe status
) else (
    echo 未选择有效的运行方式，已退出。
)

echo.
pause
