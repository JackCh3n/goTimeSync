//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// joinQuoted 参数回显（含空格的参数加引号）。
func joinQuoted(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		if strings.ContainsAny(a, " \t") {
			a = "\"" + a + "\""
		}
		quoted = append(quoted, a)
	}
	return strings.Join(quoted, " ")
}

// isWindowsService 报告本进程是否由 Windows 服务控制管理器（SCM）启动。
func isWindowsService() bool {
	ok, _ := svc.IsWindowsService()
	return ok
}

// runServiceMode 以服务方式运行：注册 SCM 控制处理器，在 runLoop 中执行同步，
// 收到 Stop/Shutdown 后请求优雅退出并等待循环收尾。
func runServiceMode(ec effectiveConfig) error {
	return svc.Run(taskName, &timeSyncService{ec: ec})
}

type timeSyncService struct{ ec effectiveConfig }

// Execute 实现 svc.Handler，在 SCM 的独立线程上运行。
func (s *timeSyncService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const accepts = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}
	exit := make(chan struct{})
	go func() {
		runLoop(s.ec)
		close(exit)
	}()
	changes <- svc.Status{State: svc.Running, Accepts: accepts}
	for {
		select {
		case <-exit:
			return false, 0
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				changes <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				requestShutdown()
				select {
				case <-exit:
				case <-time.After(5 * time.Second):
				}
				return false, 0
			}
		}
	}
}

// serviceCommand 处理 `goTimeSync service install|uninstall|start|stop|status`（需管理员）。
// 与 install（计划任务）并存：原生服务具备崩溃自动重启与 SCM 生命周期管理。
func serviceCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: goTimeSync service install|uninstall|start|stop|status")
	}
	switch args[0] {
	case "install":
		return serviceInstall()
	case "uninstall":
		return serviceUninstall()
	case "start", "stop", "status":
		return serviceControl(args[0])
	default:
		return fmt.Errorf("未知 service 子命令: %s（支持 install|uninstall|start|stop|status）", args[0])
	}
}

// serviceInstall 注册原生服务。安装时的命令行参数（-chain/-interval 等）被固化为
// 服务启动参数（以 run 模式运行）；重复安装先删旧服务，保证参数可更新。
func serviceInstall() error {
	exe, err := exePath()
	if err != nil {
		return err
	}
	// os.Args: [exe service install <run 参数...>]
	runArgs := append([]string{"run"}, os.Args[3:]...)

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败(需要管理员): %w", err)
	}
	defer m.Disconnect()
	if s, err := m.OpenService(taskName); err == nil {
		_ = s.Delete()
		_ = s.Close()
	}
	s, err := m.CreateService(taskName, exe, mgr.Config{
		StartType:   mgr.StartAutomatic,
		DisplayName: "goTimeSync 时间同步服务",
		Description: "NTP/HTTP 时间同步（goTimeSync）：系统启动自动运行，异常退出自动重启。",
	}, runArgs...)
	if err != nil {
		return fmt.Errorf("创建服务失败: %w", err)
	}
	defer s.Close()
	// 崩溃自动重启：5 秒后第一次重启、60 秒后第二次，24 小时无故障则重置失败计数
	_ = s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 86400)
	fmt.Printf("已注册 Windows 服务 [%s]（开机自启 + 崩溃自动重启）\n", taskName)
	fmt.Printf("  运行参数: %s %s\n", exe, joinQuoted(runArgs))
	fmt.Println("启动服务: goTimeSync service start （或 net start GoTimeSync）")
	return nil
}

func serviceUninstall() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败(需要管理员): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(taskName)
	if err != nil {
		return fmt.Errorf("打开服务失败(可能未安装): %w", err)
	}
	defer s.Close()
	if err := s.Delete(); err != nil {
		return fmt.Errorf("删除服务失败: %w", err)
	}
	fmt.Printf("已移除 Windows 服务 [%s]\n", taskName)
	return nil
}

func serviceControl(action string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接服务管理器失败(需要管理员): %w", err)
	}
	defer m.Disconnect()
	s, err := m.OpenService(taskName)
	if err != nil {
		return fmt.Errorf("打开服务失败(可能未安装): %w", err)
	}
	defer s.Close()
	switch action {
	case "start":
		if err := s.Start(); err != nil {
			return fmt.Errorf("启动服务失败: %w", err)
		}
		fmt.Printf("服务 [%s] 已发送启动指令\n", taskName)
	case "stop":
		if _, err := s.Control(svc.Stop); err != nil {
			return fmt.Errorf("停止服务失败: %w", err)
		}
		fmt.Printf("服务 [%s] 已发送停止指令\n", taskName)
	case "status":
		st, err := s.Query()
		if err != nil {
			return fmt.Errorf("查询服务失败: %w", err)
		}
		fmt.Printf("服务 [%s] 状态: %s\n", taskName, svcStateName(st.State))
	}
	return nil
}

func svcStateName(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "已停止"
	case svc.StartPending:
		return "启动中"
	case svc.StopPending:
		return "停止中"
	case svc.Running:
		return "运行中"
	default:
		return fmt.Sprintf("未知(%d)", state)
	}
}
