@echo off
chcp 936 >nul 2>nul
cd /d "%~dp0"

:: 版本号 = 1.00 + 0.01 * git 提交次数（每提交一次递增 0.01）
for /f %%c in ('git rev-list --count HEAD 2^>nul') do set COUNT=%%c
if not defined COUNT set COUNT=0
for /f %%v in ('powershell -NoProfile -Command "[math]::Round(1.0 + 0.01 * %COUNT%, 2)"') do set VER=%%v

echo 构建 goTimeSync v%VER%  (基于 %COUNT% 次提交)
go build -ldflags "-X main.Version=%VER%" -o goTimeSync.exe .
if errorlevel 1 (
    echo [错误] 编译失败，请确认已安装 Go 且 go 在 PATH 中。
    pause
    exit /b 1
)
echo 完成: goTimeSync.exe  (v%VER%)
