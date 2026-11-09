package main

import (
	"fmt"
	"os"
)

const toolVersion = "0.1.1"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "xuanyu: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	command := args[0]
	args = args[1:]
	switch command {
	case "install":
		return installCommand(args)
	case "uninstall":
		return uninstallCommand(args)
	case "status":
		return statusCommand(args)
	case "start", "stop", "restart":
		return controlCommand(command, args)
	case "logs":
		return logsCommand(args)
	case "version", "-version", "--version":
		fmt.Printf("xuanyu %s\n", toolVersion)
		return nil
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("未知命令 %q；运行 xuanyu help 查看帮助", command)
	}
}

func printUsage() {
	fmt.Print(`xuanyu - Headscale Console 部署工具

用法:
  xuanyu <命令> [选项]

命令:
  install     安装或更新 headscale-console systemd 服务
  uninstall   停止并卸载 systemd 服务
  status      显示安装位置和服务状态
  start       启动服务
  stop        停止服务
  restart     重启服务
  logs        持续查看服务日志
  version     显示版本
  help        显示帮助

示例:
  sudo xuanyu install -binary ./headscale-console -domain hs.example.com
  sudo xuanyu status
  sudo xuanyu uninstall -purge

运行 xuanyu install -h 查看完整选项。
`)
}
