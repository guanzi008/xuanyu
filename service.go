package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	serviceName            = "headscale-console"
	serviceUnit            = serviceName + ".service"
	serviceUser            = "headscale-console"
	serviceGroup           = "headscale-console"
	installDir             = "/opt/headscale-console"
	installedBinary        = installDir + "/headscale-console"
	unitPath               = "/etc/systemd/system/" + serviceUnit
	defaultDataDir         = "/var/lib/headscale-console"
	defaultConfigDir       = "/etc/headscale-console"
	defaultConfigPath      = defaultConfigDir + "/config.yaml"
	defaultListen          = "127.0.0.1:8080"
	defaultBaseDomain      = "tailnet.internal"
	defaultIDPListen       = "127.0.0.1:18070"
	defaultHeadplaneListen = "127.0.0.1:18090"
	defaultHeadscaleHTTP   = "127.0.0.1:18081"
	defaultHeadscaleGRPC   = "127.0.0.1:18082"
	defaultHeadscaleMetric = "127.0.0.1:18083"
	defaultDERPURL         = "https://controlplane.tailscale.com/derpmap/default"
)

type installOptions struct {
	binaryPath string
	baseURL    string
	domain     string
	listen     string

	dataDir    string
	configPath string
	adminPath  string
	baseDomain string

	registration        bool
	allowedEmailDomains []string
	passwordMinLength   int
	defaultRole         string
	derpURLs            []string
	dnsNameservers      []string

	idpListen              string
	headplaneListen        string
	headscaleHTTPListen    string
	headscaleGRPCListen    string
	headscaleMetricsListen string

	forceConfig bool
	dryRun      bool
	noStart     bool
}

func installCommand(args []string) error {
	var options installOptions
	var allowedEmailDomains, derpURLs, dnsNameservers string

	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&options.binaryPath, "binary", "", "headscale-console 二进制路径（默认自动查找当前目录或 xuanyu 同目录）")
	fs.StringVar(&options.domain, "domain", "", "对外域名，例如 hs.example.com；自动生成 https://<domain>")
	fs.StringVar(&options.baseURL, "base-url", "", "对外完整 URL；与 -domain 二选一，优先级更高")
	fs.StringVar(&options.listen, "listen", defaultListen, "应用监听地址，通常只监听回环地址")
	fs.StringVar(&options.dataDir, "data-dir", defaultDataDir, "数据目录")
	fs.StringVar(&options.configPath, "config", defaultConfigPath, "配置文件路径")
	fs.StringVar(&options.adminPath, "admin-path", "/admin", "Headplane 管理路径")
	fs.StringVar(&options.baseDomain, "base-domain", defaultBaseDomain, "Headscale MagicDNS 基础域名")
	fs.BoolVar(&options.registration, "registration", true, "开放本地账号注册")
	fs.StringVar(&allowedEmailDomains, "allowed-email-domains", "", "允许注册的邮箱域名，逗号分隔；留空表示不限制")
	fs.IntVar(&options.passwordMinLength, "password-min-length", 10, "本地账号最小密码长度（至少 8）")
	fs.StringVar(&options.defaultRole, "default-role", "member", "Headplane 新用户默认角色")
	fs.StringVar(&derpURLs, "derp-urls", defaultDERPURL, "Headscale DERP 地图 URL，逗号分隔")
	fs.StringVar(&dnsNameservers, "dns-nameservers", "1.1.1.1,1.0.0.1", "Headscale DNS 上游，逗号分隔")
	fs.StringVar(&options.idpListen, "idp-listen", defaultIDPListen, "内置 OIDC 监听地址（仅回环）")
	fs.StringVar(&options.headplaneListen, "headplane-listen", defaultHeadplaneListen, "Headplane 监听地址（仅回环）")
	fs.StringVar(&options.headscaleHTTPListen, "headscale-http-listen", defaultHeadscaleHTTP, "Headscale HTTP 监听地址（仅回环）")
	fs.StringVar(&options.headscaleGRPCListen, "headscale-grpc-listen", defaultHeadscaleGRPC, "Headscale gRPC 监听地址（仅回环）")
	fs.StringVar(&options.headscaleMetricsListen, "headscale-metrics-listen", defaultHeadscaleMetric, "Headscale metrics 监听地址（仅回环）")
	fs.BoolVar(&options.forceConfig, "force-config", false, "覆盖已存在的配置文件（危险）")
	fs.BoolVar(&options.dryRun, "dry-run", false, "只显示安装计划，不修改系统")
	fs.BoolVar(&options.noStart, "no-start", false, "安装并开机启用，但暂不启动服务")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法: xuanyu install [选项]")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("install 不接受位置参数: %s", strings.Join(fs.Args(), " "))
	}

	options.allowedEmailDomains = splitCommaList(allowedEmailDomains)
	options.derpURLs = splitCommaList(derpURLs)
	options.dnsNameservers = splitCommaList(dnsNameservers)
	if options.binaryPath == "" {
		options.binaryPath = findDefaultBinary()
	}
	if options.binaryPath == "" {
		return errors.New("找不到 headscale-console 二进制；请使用 -binary 指定")
	}
	options.binaryPath = filepath.Clean(options.binaryPath)
	options.dataDir = filepath.Clean(options.dataDir)
	options.configPath = filepath.Clean(options.configPath)
	if options.baseURL == "" && options.domain == "" {
		return errors.New("必须指定 -domain 或 -base-url")
	}
	if options.baseURL == "" {
		options.baseURL = "https://" + strings.TrimSpace(options.domain)
	}
	options.baseURL = strings.TrimRight(strings.TrimSpace(options.baseURL), "/")
	if options.domain == "" {
		parsed, err := url.Parse(options.baseURL)
		if err == nil {
			options.domain = parsed.Hostname()
		}
	}
	if err := validateInstallOptions(options); err != nil {
		return err
	}
	if err := verifyBinary(options.binaryPath); err != nil {
		return err
	}

	configYAML := renderConfig(options)
	unit := renderServiceUnit(options)
	if options.dryRun {
		fmt.Println("[dry-run] 不会写入文件，也不会调用 systemctl")
		fmt.Printf("程序来源:       %s\n", options.binaryPath)
		fmt.Printf("程序安装位置:   %s\n", installedBinary)
		fmt.Printf("systemd unit:   %s\n", unitPath)
		fmt.Printf("配置文件:       %s\n", options.configPath)
		fmt.Printf("数据目录:       %s\n", options.dataDir)
		fmt.Printf("对外地址:       %s\n", options.baseURL)
		fmt.Println("\n----- config.yaml -----")
		fmt.Print(configYAML)
		fmt.Println("-----------------------")
		fmt.Println("\n----- systemd unit -----")
		fmt.Print(unit)
		fmt.Println("------------------------")
		return nil
	}

	if err := requireRoot(); err != nil {
		return err
	}
	if err := requireSystemd(); err != nil {
		return err
	}
	uid, gid, err := ensureServiceUser(options.dataDir)
	if err != nil {
		return err
	}
	if err := ensureDirectory(installDir, 0o755, 0, 0); err != nil {
		return err
	}
	if err := ensureDirectory(options.dataDir, 0o700, uid, gid); err != nil {
		return err
	}
	if err := ensureConfigParent(options.configPath, gid); err != nil {
		return err
	}
	if err := installBinary(options.binaryPath); err != nil {
		return err
	}

	configExists := pathExists(options.configPath)
	if !configExists || options.forceConfig {
		if err := writeAtomic(options.configPath, []byte(configYAML), 0o640); err != nil {
			return fmt.Errorf("写入配置文件: %w", err)
		}
		if err := os.Chown(options.configPath, 0, gid); err != nil {
			return fmt.Errorf("设置配置文件属组: %w", err)
		}
		if configExists {
			fmt.Printf("已覆盖配置文件: %s\n", options.configPath)
		} else {
			fmt.Printf("已创建配置文件: %s\n", options.configPath)
		}
	} else {
		fmt.Printf("配置文件已存在，保留原配置: %s\n", options.configPath)
	}

	if err := writeAtomic(unitPath, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("写入 systemd unit: %w", err)
	}
	if err := runSystemctl("daemon-reload"); err != nil {
		return err
	}
	if err := runSystemctl("enable", serviceUnit); err != nil {
		return err
	}
	if options.noStart {
		fmt.Println("服务已安装并设置开机启动；按 -no-start 要求暂未启动。")
	} else if err := runSystemctl("restart", serviceUnit); err != nil {
		printServiceStatus(options.configPath, options.dataDir)
		return fmt.Errorf("服务已安装，但启动失败；请运行 journalctl -u %s -n 100 --no-pager 查看日志: %w", serviceUnit, err)
	}

	fmt.Println("安装完成")
	printServiceStatus(options.configPath, options.dataDir)
	return nil
}

func uninstallCommand(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	purge := fs.Bool("purge", false, "同时删除配置和数据目录")
	dryRun := fs.Bool("dry-run", false, "只显示卸载计划，不修改系统")
	dataDir := fs.String("data-dir", defaultDataDir, "安装时使用的数据目录")
	configPath := fs.String("config", defaultConfigPath, "安装时使用的配置文件")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法: xuanyu uninstall [选项]")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("uninstall 不接受位置参数: %s", strings.Join(fs.Args(), " "))
	}
	*dataDir = filepath.Clean(*dataDir)
	*configPath = filepath.Clean(*configPath)
	if err := validateManagedPath(*dataDir); err != nil {
		return fmt.Errorf("无效的 -data-dir: %w", err)
	}
	if err := validateManagedPath(*configPath); err != nil {
		return fmt.Errorf("无效的 -config: %w", err)
	}

	if *dryRun {
		fmt.Println("[dry-run] 将执行：")
		fmt.Printf("  systemctl disable --now %s\n", serviceUnit)
		fmt.Printf("  删除 %s\n", unitPath)
		fmt.Printf("  删除 %s\n", installDir)
		if *purge {
			fmt.Printf("  删除 %s\n", *configPath)
			fmt.Printf("  删除 %s\n", *dataDir)
		} else {
			fmt.Println("  保留配置、数据和服务系统用户")
		}
		return nil
	}

	if err := requireRoot(); err != nil {
		return err
	}
	unitExists := pathExists(unitPath)
	if systemdRunning() && unitExists {
		if err := runSystemctlQuiet("disable", "--now", serviceUnit); err != nil {
			fmt.Fprintf(os.Stderr, "警告: 停止或禁用服务失败: %v\n", err)
		}
	}
	if err := os.Remove(unitPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除 systemd unit: %w", err)
	}
	_ = os.Remove("/etc/systemd/system/multi-user.target.wants/" + serviceUnit)
	if systemdRunning() {
		if err := runSystemctlQuiet("daemon-reload"); err != nil {
			fmt.Fprintf(os.Stderr, "警告: systemctl daemon-reload 失败: %v\n", err)
		}
		_ = runSystemctlQuiet("reset-failed", serviceUnit)
	}
	if err := os.RemoveAll(installDir); err != nil {
		return fmt.Errorf("删除安装目录 %s: %w", installDir, err)
	}
	if *purge {
		if err := os.Remove(*configPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("删除配置文件: %w", err)
		}
		removeEmptyDirectory(filepath.Dir(*configPath))
		if err := os.RemoveAll(*dataDir); err != nil {
			return fmt.Errorf("删除数据目录 %s: %w", *dataDir, err)
		}
	}

	fmt.Println("卸载完成")
	if !*purge {
		fmt.Printf("已保留配置文件: %s\n", *configPath)
		fmt.Printf("已保留数据目录: %s\n", *dataDir)
		fmt.Println("如需彻底删除，请运行: xuanyu uninstall -purge")
	}
	fmt.Printf("服务系统用户 %s 未删除，如有需要可手动执行 userdel %s\n", serviceUser, serviceUser)
	return nil
}

func statusCommand(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dataDir := fs.String("data-dir", defaultDataDir, "安装时使用的数据目录")
	configPath := fs.String("config", defaultConfigPath, "安装时使用的配置文件")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法: xuanyu status [选项]")
		fmt.Fprintln(os.Stderr)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("status 不接受位置参数: %s", strings.Join(fs.Args(), " "))
	}
	printServiceStatus(*configPath, *dataDir)
	return nil
}

func controlCommand(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, "用法: xuanyu %s\n", action) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("%s 不接受位置参数", action)
	}
	if !systemdRunning() {
		return errors.New("systemd 未运行，无法管理服务")
	}
	if !pathExists(unitPath) || !pathExists(installedBinary) {
		return errors.New("headscale-console 尚未安装；请先运行 xuanyu install")
	}
	if err := runSystemctl(action, serviceUnit); err != nil {
		return err
	}
	printServiceStatus(defaultConfigPath, defaultDataDir)
	return nil
}

func logsCommand(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprintln(os.Stderr, "用法: xuanyu logs") }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("logs 不接受位置参数")
	}
	if _, err := exec.LookPath("journalctl"); err != nil {
		return errors.New("未找到 journalctl")
	}
	cmd := exec.Command("journalctl", "-u", serviceUnit, "-n", "200", "--no-pager", "-f")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func validateInstallOptions(options installOptions) error {
	if _, err := os.Stat(options.binaryPath); err != nil {
		return fmt.Errorf("无法读取二进制 %s: %w", options.binaryPath, err)
	}
	if !filepath.IsAbs(options.dataDir) || !filepath.IsAbs(options.configPath) {
		return errors.New("-data-dir 和 -config 必须是绝对路径")
	}
	if err := validateManagedPath(options.dataDir); err != nil {
		return fmt.Errorf("无效的 -data-dir: %w", err)
	}
	if err := validateManagedPath(options.configPath); err != nil {
		return fmt.Errorf("无效的 -config: %w", err)
	}
	if err := validateBaseURL(options.baseURL); err != nil {
		return fmt.Errorf("无效的对外地址: %w", err)
	}
	if err := validateHostname(options.domain); err != nil {
		return fmt.Errorf("无效的 -domain: %w", err)
	}
	if err := validateListenAddress(options.listen); err != nil {
		return fmt.Errorf("无效的 -listen: %w", err)
	}
	if err := validateLoopbackListen(options.idpListen); err != nil {
		return fmt.Errorf("无效的 -idp-listen: %w", err)
	}
	internal := []struct {
		name   string
		listen string
	}{
		{"headplane", options.headplaneListen},
		{"headscale-http", options.headscaleHTTPListen},
		{"headscale-grpc", options.headscaleGRPCListen},
		{"headscale-metrics", options.headscaleMetricsListen},
	}
	seen := map[string]string{options.idpListen: "idp"}
	for _, item := range internal {
		if err := validateLoopbackListen(item.listen); err != nil {
			return fmt.Errorf("无效的内部监听地址 %s: %w", item.name, err)
		}
		if previous, ok := seen[item.listen]; ok {
			return fmt.Errorf("内部监听地址 %s 与 %s 重复: %s", item.name, previous, item.listen)
		}
		seen[item.listen] = item.name
	}
	if !strings.HasPrefix(options.adminPath, "/") || strings.HasSuffix(options.adminPath, "/") || strings.Contains(options.adminPath, "..") {
		return errors.New("-admin-path 必须以 / 开头，不能以 / 结尾或包含 ..")
	}
	if options.adminPath == "/idp" || strings.HasPrefix(options.adminPath+"/", "/idp/") {
		return errors.New("-admin-path 不能与 /idp 重叠")
	}
	if options.passwordMinLength < 8 {
		return errors.New("-password-min-length 至少为 8")
	}
	switch strings.ToLower(strings.TrimSpace(options.defaultRole)) {
	case "admin", "network_admin", "it_admin", "auditor", "viewer", "member":
	default:
		return errors.New("-default-role 必须是 admin、network_admin、it_admin、auditor、viewer 或 member")
	}
	if strings.TrimSpace(options.baseDomain) == "" {
		return errors.New("-base-domain 不能为空")
	}
	if err := validateHostname(options.baseDomain); err != nil {
		return fmt.Errorf("无效的 -base-domain: %w", err)
	}
	for _, domain := range options.allowedEmailDomains {
		if err := validateHostname(domain); err != nil {
			return fmt.Errorf("无效的邮箱域名 %q: %w", domain, err)
		}
	}
	if len(options.derpURLs) == 0 {
		return errors.New("-derp-urls 不能为空")
	}
	for _, endpoint := range options.derpURLs {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("无效的 DERP URL %q", endpoint)
		}
	}
	if len(options.dnsNameservers) == 0 {
		return errors.New("-dns-nameservers 不能为空")
	}
	for _, address := range options.dnsNameservers {
		if net.ParseIP(address) == nil {
			return fmt.Errorf("无效的 DNS 地址 %q", address)
		}
	}
	return nil
}

func validateBaseURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return errors.New("必须是绝对 http:// 或 https:// URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("只支持 http 或 https")
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("不能包含路径、查询参数或片段")
	}
	if parsed.User != nil {
		return errors.New("不能包含用户名或密码")
	}
	return nil
}

func validateHostname(value string) error {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 253 {
		return errors.New("不能为空且长度不能超过 253")
	}
	if net.ParseIP(value) != nil {
		return nil
	}
	value = strings.TrimSuffix(value, ".")
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 {
			return errors.New("域名标签为空或过长")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("域名标签不能以连字符开头或结尾")
		}
		for _, ch := range label {
			if (ch < 'a' || ch > 'z') && (ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') && ch != '-' {
				return fmt.Errorf("包含非法字符 %q", ch)
			}
		}
	}
	return nil
}

func validateListenAddress(value string) error {
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		return err
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("端口 %q 无效", port)
	}
	return nil
}

func validateLoopbackListen(value string) error {
	if err := validateListenAddress(value); err != nil {
		return err
	}
	host, _, _ := net.SplitHostPort(value)
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("必须使用回环 IP，例如 127.0.0.1:18070")
	}
	return nil
}

func validateManagedPath(path string) error {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || path == "/" {
		return errors.New("必须是安全的绝对路径")
	}
	if strings.ContainsAny(path, " \t\r\n\"'%") {
		return errors.New("不能包含空白、引号或百分号")
	}
	protected := map[string]bool{
		"/bin": true, "/boot": true, "/dev": true, "/etc": true, "/home": true,
		"/lib": true, "/lib64": true, "/opt": true, "/proc": true, "/root": true,
		"/run": true, "/sbin": true, "/srv": true, "/sys": true, "/tmp": true,
		"/usr": true, "/var": true,
	}
	if protected[path] {
		return errors.New("不能使用系统顶级目录")
	}
	return nil
}

func renderConfig(options installOptions) string {
	var b strings.Builder
	b.WriteString("# Generated by xuanyu install\n")
	b.WriteString("# Edit this file, then run: xuanyu restart\n\n")
	b.WriteString("server:\n")
	fmt.Fprintf(&b, "  listen: %s\n", yamlQuote(options.listen))
	fmt.Fprintf(&b, "  base_url: %s\n", yamlQuote(options.baseURL))
	fmt.Fprintf(&b, "  admin_path: %s\n", yamlQuote(options.adminPath))
	b.WriteString("  internal:\n")
	fmt.Fprintf(&b, "    idp: %s\n", yamlQuote(options.idpListen))
	fmt.Fprintf(&b, "    headplane: %s\n", yamlQuote(options.headplaneListen))
	fmt.Fprintf(&b, "    headscale_http: %s\n", yamlQuote(options.headscaleHTTPListen))
	fmt.Fprintf(&b, "    headscale_grpc: %s\n", yamlQuote(options.headscaleGRPCListen))
	fmt.Fprintf(&b, "    headscale_metrics: %s\n", yamlQuote(options.headscaleMetricsListen))
	b.WriteString("\nregistration:\n")
	fmt.Fprintf(&b, "  enabled: %t\n", options.registration)
	if len(options.allowedEmailDomains) > 0 {
		b.WriteString("  allowed_email_domains:\n")
		for _, domain := range options.allowedEmailDomains {
			fmt.Fprintf(&b, "    - %s\n", yamlQuote(domain))
		}
	}
	fmt.Fprintf(&b, "  password_min_length: %d\n", options.passwordMinLength)
	b.WriteString("\nheadplane:\n")
	fmt.Fprintf(&b, "  default_role: %s\n", yamlQuote(strings.ToLower(strings.TrimSpace(options.defaultRole))))
	b.WriteString("\nheadscale:\n")
	fmt.Fprintf(&b, "  base_domain: %s\n", yamlQuote(options.baseDomain))
	b.WriteString("  derp_urls:\n")
	for _, endpoint := range options.derpURLs {
		fmt.Fprintf(&b, "    - %s\n", yamlQuote(endpoint))
	}
	b.WriteString("  dns_nameservers:\n")
	for _, address := range options.dnsNameservers {
		fmt.Fprintf(&b, "    - %s\n", yamlQuote(address))
	}
	return b.String()
}

func renderServiceUnit(options installOptions) string {
	args := []string{
		installedBinary,
		"--config", options.configPath,
		"--data-dir", options.dataDir,
	}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, systemdQuote(arg))
	}
	return fmt.Sprintf(`# Generated by xuanyu install
[Unit]
Description=Headscale Console (Headscale, Headplane and OIDC account center)
Documentation=https://github.com/guanzi008/headscale-console
Wants=network-online.target nss-lookup.target
After=network-online.target nss-lookup.target
StartLimitIntervalSec=60
StartLimitBurst=5

[Service]
Type=simple
User=%s
Group=%s
		WorkingDirectory=%s
ExecStart=%s
Restart=on-failure
RestartSec=5s
TimeoutStartSec=240s
TimeoutStopSec=60s
LimitNOFILE=1048576
UMask=0077
NoNewPrivileges=yes
PrivateTmp=yes
ProtectHome=yes
ProtectSystem=strict
ReadWritePaths=%s
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
ProtectKernelLogs=yes
ProtectClock=yes
ProtectHostname=yes
RestrictSUIDSGID=yes
RestrictRealtime=yes
LockPersonality=yes
SystemCallArchitectures=native
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE

[Install]
WantedBy=multi-user.target
`, serviceUser, serviceGroup, options.dataDir, strings.Join(quoted, " "), options.dataDir)
}

func yamlQuote(value string) string {
	return strconv.Quote(value)
}

func systemdQuote(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		`%`, `%%`,
		"\n", `\n`,
		"\r", `\r`,
	)
	return `"` + replacer.Replace(value) + `"`
}

func splitCommaList(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			values = append(values, item)
		}
	}
	return values
}

func findDefaultBinary() string {
	var candidates []string
	if executable, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		candidates = append(candidates, filepath.Join(filepath.Dir(executable), serviceName))
	}
	if workingDirectory, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(workingDirectory, serviceName))
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return ""
}

func verifyBinary(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("读取二进制 %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("二进制路径不是普通文件: %s", path)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("文件不可执行: %s", path)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("运行 %s --version 失败（架构不匹配或文件损坏）: %s", path, message)
	}
	fmt.Printf("待安装程序版本: %s", string(output))
	if !strings.HasSuffix(string(output), "\n") {
		fmt.Println()
	}
	return nil
}

func requireRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("需要 root 权限，请使用 sudo 运行")
	}
	return nil
}

func requireSystemd() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("未找到 systemctl，当前系统不支持 systemd 安装")
	}
	if !systemdRunning() {
		return errors.New("systemd 未运行，无法安装为系统服务")
	}
	return nil
}

func systemdRunning() bool {
	info, err := os.Stat("/run/systemd/system")
	return err == nil && info.IsDir()
}

func ensureServiceUser(homeDir string) (int, int, error) {
	group, err := user.LookupGroup(serviceGroup)
	if err != nil {
		if err := runCommand("groupadd", "--system", serviceGroup); err != nil {
			return 0, 0, err
		}
		group, err = user.LookupGroup(serviceGroup)
		if err != nil {
			return 0, 0, fmt.Errorf("创建系统组后仍无法查询 %s: %w", serviceGroup, err)
		}
	}

	account, err := user.Lookup(serviceUser)
	if err != nil {
		if err := runCommand("useradd", "--system", "--gid", serviceGroup, "--home-dir", homeDir, "--no-create-home", "--shell", "/usr/sbin/nologin", serviceUser); err != nil {
			return 0, 0, err
		}
		account, err = user.Lookup(serviceUser)
		if err != nil {
			return 0, 0, fmt.Errorf("创建系统用户后仍无法查询 %s: %w", serviceUser, err)
		}
	} else {
		_ = runCommandQuiet("usermod", "--append", "--groups", serviceGroup, serviceUser)
	}

	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return 0, 0, fmt.Errorf("解析用户 UID %q: %w", account.Uid, err)
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return 0, 0, fmt.Errorf("解析用户组 GID %q: %w", group.Gid, err)
	}
	return uid, gid, nil
}

func ensureDirectory(path string, mode os.FileMode, uid, gid int) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("创建目录 %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("设置目录权限 %s: %w", path, err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("设置目录所有者 %s: %w", path, err)
	}
	return nil
}

func ensureConfigParent(configPath string, gid int) error {
	parent := filepath.Dir(configPath)
	if parent == defaultConfigDir {
		return ensureDirectory(parent, 0o750, 0, gid)
	}
	if pathExists(parent) {
		return nil
	}
	return ensureDirectory(parent, 0o750, 0, gid)
}

func installBinary(source string) error {
	resolved, err := filepath.EvalSymlinks(source)
	if err == nil {
		source = resolved
	}
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("打开二进制 %s: %w", source, err)
	}
	defer in.Close()

	tmp, err := os.CreateTemp(installDir, ".headscale-console-*")
	if err != nil {
		return fmt.Errorf("创建临时安装文件: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return fmt.Errorf("复制二进制: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return fmt.Errorf("设置二进制权限: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("同步二进制: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	if err := os.Chown(tmpName, 0, 0); err != nil {
		return fmt.Errorf("设置二进制所有者: %w", err)
	}
	if err := os.Rename(tmpName, installedBinary); err != nil {
		return fmt.Errorf("安装到 %s: %w", installedBinary, err)
	}
	fmt.Printf("已安装程序: %s\n", installedBinary)
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func printServiceStatus(configPath, dataDir string) {
	fmt.Println()
	fmt.Println("Headscale Console 服务状态")
	fmt.Println("---------------------------")

	binaryExists := pathExists(installedBinary)
	unitExists := pathExists(unitPath)
	configExists := pathExists(configPath)
	dataExists := pathExists(dataDir)
	installed := binaryExists && unitExists
	partial := binaryExists != unitExists
	state := "未安装"
	if installed {
		state = "已安装"
	} else if partial || configExists || dataExists {
		state = "部分安装或存在残留"
	}
	fmt.Printf("安装状态:       %s\n", state)
	fmt.Printf("程序位置:       %s%s\n", installedBinary, existenceLabel(binaryExists))
	if info, err := os.Stat(installedBinary); err == nil {
		fmt.Printf("程序大小:       %s\n", formatBytes(info.Size()))
		if sum, err := fileSHA256(installedBinary); err == nil {
			fmt.Printf("程序 SHA-256:   %s\n", sum)
		}
		if output, err := exec.Command(installedBinary, "--version").CombinedOutput(); err == nil {
			fmt.Printf("程序版本:       %s", strings.TrimSpace(string(output)))
			fmt.Println()
		}
	}
	fmt.Printf("systemd unit:   %s%s\n", unitPath, existenceLabel(unitExists))
	fmt.Printf("配置文件:       %s%s\n", configPath, existenceLabel(configExists))
	fmt.Printf("数据目录:       %s%s\n", dataDir, existenceLabel(dataExists))

	if !systemdRunning() {
		fmt.Println("服务运行状态:   systemd 未运行")
	} else if !unitExists {
		fmt.Println("服务运行状态:   未安装")
	} else {
		active := systemctlOutput("is-active", serviceUnit)
		enabled := systemctlOutput("is-enabled", serviceUnit)
		pid := systemctlOutput("show", "-p", "MainPID", "--value", serviceUnit)
		if active == "" {
			active = "unknown"
		}
		if enabled == "" {
			enabled = "unknown"
		}
		if pid == "" {
			pid = "0"
		}
		fmt.Printf("服务运行状态:   %s\n", active)
		fmt.Printf("开机启动:       %s\n", enabled)
		fmt.Printf("主进程 PID:     %s\n", pid)
	}
	if command := serviceExecStart(); command != "" {
		fmt.Printf("服务启动命令:   %s\n", command)
	}
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func runCommandQuiet(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(out))
	if message == "" {
		message = err.Error()
	}
	return fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), message)
}

func runSystemctl(args ...string) error {
	return runCommand("systemctl", args...)
}

func runSystemctlQuiet(args ...string) error {
	return runCommandQuiet("systemctl", args...)
}

func systemctlOutput(args ...string) string {
	out, _ := exec.Command("systemctl", args...).Output()
	return strings.TrimSpace(string(out))
}

func serviceExecStart() string {
	data, err := os.ReadFile(unitPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			return strings.TrimPrefix(line, "ExecStart=")
		}
	}
	return ""
}

func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func existenceLabel(exists bool) string {
	if exists {
		return " [存在]"
	}
	return " [不存在]"
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func formatBytes(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(size)/float64(div), "KMGTPE"[exp])
}

func removeEmptyDirectory(path string) {
	if path == "" || path == "/" || path == "/etc" || path == "/var" || path == "/opt" {
		return
	}
	_ = os.Remove(path)
}
