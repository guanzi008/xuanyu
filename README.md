# xuanyu

`xuanyu` 是 [Headscale Console](https://github.com/guanzi008/headscale-console) 的独立部署工具，负责：

- 安装、更新、启动、停止和卸载 systemd 服务。
- 创建数据目录和受限 systemd unit，不创建专用系统用户，服务以 root 运行。
- 首次安装时生成可直接运行的 `config.yaml`。
- 查看安装状态、版本、SHA-256 和服务日志。

`xuanyu` 不安装、不修改、不重载 Nginx。Nginx 由你独立管理；域名、DNS、证书和反向代理配置请参考 [Headscale Console Nginx 文档](https://github.com/guanzi008/headscale-console/blob/main/docs/nginx.md)。

## 构建

```bash
make build
./dist/xuanyu version
```

生成自定义版本号：

```bash
VERSION=0.1.2 make build
```

## 安装

推荐先准备好 DNS 和 TLS 证书，并先把 Nginx 配置好，再启动服务。因为程序启动时会通过自己的 `server.base_url` 访问 `/idp` 做 OIDC discovery，所以该域名必须能从本机正常访问并验证 HTTPS 证书。

```bash
sudo ./dist/xuanyu install \
  -binary ./headscale-console-linux-amd64 \
  -domain hs.example.com \
  -base-domain tailnet.example.com \
  -allowed-email-domains example.com
```

如果想先安装但不启动：

```bash
sudo ./dist/xuanyu install \
  -binary ./headscale-console-linux-amd64 \
  -domain hs.example.com \
  -no-start
sudo ./dist/xuanyu start
```

默认路径：

```text
/opt/headscale-console/headscale-console
/etc/headscale-console/config.yaml
/var/lib/headscale-console
/etc/systemd/system/headscale-console.service
```

重复执行 `install` 会更新二进制和 unit，但默认保留已有配置；确实需要重建配置时加 `-force-config`。

## Nginx

Nginx 是独立组件，不由 `xuanyu` 管理。请按照 [Headscale Console Nginx 文档](https://github.com/guanzi008/headscale-console/blob/main/docs/nginx.md) 配置域名、DNS、TLS 证书和反向代理，再启动 `headscale-console`。

## 日常管理

```bash
sudo xuanyu status
sudo xuanyu restart
sudo xuanyu stop
sudo xuanyu start
sudo xuanyu logs
```

查看完整帮助：

```bash
xuanyu install -h
```

## 卸载

默认保留配置和数据：

```bash
sudo xuanyu uninstall
```

连同配置和数据一起删除：

```bash
sudo xuanyu uninstall -purge
```

## 安全说明

- 应用默认只监听 `127.0.0.1:8080`，公网入口由 Nginx 终止 TLS。
- 安装器不会创建专用系统用户；systemd unit 使用 root 运行，并启用 `ProtectSystem=strict`、`NoNewPrivileges=yes` 等限制。
- `-force-config` 会覆盖配置文件，请先备份。
- 自定义 `-data-dir`、`-config` 后，后续 `status`、`uninstall` 需要传入相同参数。
