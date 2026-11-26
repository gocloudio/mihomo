<h1 align="center">
  <img src="Meta.png" alt="Meta Kennel" width="200">
  <br>Meta Kernel<br>
</h1>

<h3 align="center">Another Mihomo Kernel.</h3>

<p align="center">
  <a href="https://goreportcard.com/report/github.com/MetaCubeX/mihomo">
    <img src="https://goreportcard.com/badge/github.com/MetaCubeX/mihomo?style=flat-square">
  </a>
  <img src="https://img.shields.io/github/go-mod/go-version/MetaCubeX/mihomo/Alpha?style=flat-square">
  <a href="https://github.com/MetaCubeX/mihomo/releases">
    <img src="https://img.shields.io/github/release/MetaCubeX/mihomo/all.svg?style=flat-square">
  </a>
  <a href="https://github.com/MetaCubeX/mihomo">
    <img src="https://img.shields.io/badge/release-Meta-00b4f0?style=flat-square">
  </a>
</p>

## macOS TUN 按 PID 分流

`pid-routing-macos` 分支可以把某个 macOS 进程的 TUN 流量绑定到指定代理节点。**发布和运行时使用 macOS 原生二进制。** 内核需要查询本机进程持有的套接字。macOS 上的 Docker Desktop 在 Linux 虚拟机中运行容器，容器里的 TUN 只能用于测试虚拟机内的网络路径，无法识别 Mac 宿主机应用的 PID。详见 [Docker Desktop 网络说明](https://docs.docker.com/desktop/features/networking/)。

### 构建和打包

从 fork 的功能分支构建。在 Mac 上运行 `uname -m` 确认架构，选择对应的 Make 目标。

```sh
git clone --branch pid-routing-macos https://github.com/gocloudio/mihomo.git
cd mihomo
```

| Mac | 构建可执行文件 | 打包为 `.gz` |
| --- | --- | --- |
| Apple 芯片（`arm64`） | `make darwin-arm64` | `make darwin-arm64.gz` |
| Intel（`x86_64`） | `make darwin-amd64-compatible` | `make darwin-amd64-compatible.gz` |

构建产物在 `bin/` 下。`.gz` 适合分发，解压后给可执行文件添加执行权限，再直接运行。

### 配置和启动

将下面的配置保存为 `pid-routing.yaml`。替换控制器密钥和示例 SOCKS5 服务地址。也可以通过 `proxy-providers` 加载订阅中的节点。

```yaml
mode: rule
external-controller: 127.0.0.1:9097
secret: "replace-with-a-long-random-secret"

tun:
  enable: true
  stack: gvisor
  auto-route: true
  auto-detect-interface: true
  dns-mode: disabled

proxies:
  - name: node-a
    type: socks5
    server: proxy.example.com
    port: 1080
    udp: true

rules:
  - MATCH,DIRECT
```

`auto-route: true` 会接管匹配的系统路由。已有 TUN 在运行时，请在准备切换后再启动这份配置。并行验证时应关闭 `auto-route`，使用单独的 `utun`，并仅把测试连接显式路由到该接口。

以下命令以 Apple 芯片为例。Intel 使用 `bin/mihomo-darwin-amd64-compatible`。`-t` 只检查配置，启动 TUN 时需要管理员权限。

```sh
bin/mihomo-darwin-arm64 -t -d "$PWD/pid-routing-data" -f "$PWD/pid-routing.yaml"
sudo "$PWD/bin/mihomo-darwin-arm64" -d "$PWD/pid-routing-data" -f "$PWD/pid-routing.yaml"
```

### 从 CLI 绑定或删除 PID

在另一个终端运行。用 `pgrep -fl '进程名'` 查找 PID。如果应用有独立的网络子进程，使用实际发起连接的 PID。

```sh
controller=http://127.0.0.1:9097
secret='replace-with-a-long-random-secret'
pid=12345

# 查询配置中的代理和订阅节点
curl --noproxy '*' -fsS -H "Authorization: Bearer $secret" "$controller/proxies"
curl --noproxy '*' -fsS -H "Authorization: Bearer $secret" "$controller/providers/proxies"

# 绑定 PID。再次 PUT 同一 PID 可改绑到另一节点
curl --noproxy '*' -sS -i -X PUT "$controller/pid-routes/$pid" \
  -H "Authorization: Bearer $secret" -H 'Content-Type: application/json' \
  --data '{"proxy":"node-a"}'

# 删除绑定，后续新连接恢复按普通规则分流
curl --noproxy '*' -sS -i -X DELETE "$controller/pid-routes/$pid" \
  -H "Authorization: Bearer $secret"
```

绑定和删除成功均返回 `204 No Content`。PUT 的 `proxy` 填已加载的具体节点名称。若配置中有同名具体节点，优先使用配置中的节点。否则，订阅中的节点名称须唯一。添加代理地址请在配置的 `proxies` 或 `proxy-providers` 中操作。

绑定只作用于 `rule` 模式下进入该实例 TUN 的流量，适用于新 TCP 连接和已连接的 UDP 套接字。UDP 出站节点须支持 UDP。已有连接应在改绑后重新建立。绑定保存在内存中，应用或 Mihomo 重启后需要重新登记。可以在 Mihomo 日志中查看 `match PID(12345) using node-a` 来确认命中。

## Features

- Local HTTP/HTTPS/SOCKS server with authentication support
- VMess, VLESS, Shadowsocks, Trojan, Snell, TUIC, Hysteria protocol support
- Built-in DNS server that aims to minimize DNS pollution attack impact, supports DoH/DoT upstream and fake IP.
- Rules based off domains, GEOIP, IPCIDR or Process to forward packets to different nodes
- Remote groups allow users to implement powerful rules. Supports automatic fallback, load balancing or auto select node
  based off latency
- Remote providers, allowing users to get node lists remotely instead of hard-coding in config
- Netfilter TCP redirecting. Deploy Mihomo on your Internet gateway with `iptables`.
- Comprehensive HTTP RESTful API controller

## Dashboard

A web dashboard with first-class support for this project has been created; it can be checked out at [metacubexd](https://github.com/MetaCubeX/metacubexd).

## Configration example

Configuration example is located at [/docs/config.yaml](https://github.com/MetaCubeX/mihomo/blob/Alpha/docs/config.yaml).

## Docs

Documentation can be found in [mihomo Docs](https://wiki.metacubex.one/).

## For development

Requirements:
[Go 1.20 or newer](https://go.dev/dl/)

Build mihomo:

```shell
git clone https://github.com/MetaCubeX/mihomo.git
cd mihomo && go mod download
go build
```

Set go proxy if a connection to GitHub is not possible:

```shell
go env -w GOPROXY=https://goproxy.io,direct
```

Build with gvisor tun stack:

```shell
go build -tags with_gvisor
```

### IPTABLES configuration

Work on Linux OS which supported `iptables`

```yaml
# Enable the TPROXY listener
tproxy-port: 9898

iptables:
  enable: true # default is false
  inbound-interface: eth0 # detect the inbound interface, default is 'lo'
```

## Debugging

Check [wiki](https://wiki.metacubex.one/api/#debug) to get an instruction on using debug
API.

## Credits

- [Dreamacro/clash](https://github.com/Dreamacro/clash)
- [SagerNet/sing-box](https://github.com/SagerNet/sing-box)
- [riobard/go-shadowsocks2](https://github.com/riobard/go-shadowsocks2)
- [v2ray/v2ray-core](https://github.com/v2ray/v2ray-core)
- [WireGuard/wireguard-go](https://github.com/WireGuard/wireguard-go)
- [yaling888/clash-plus-pro](https://github.com/yaling888/clash)

## License

This software is released under the GPL-3.0 license.

**In addition, any downstream projects not affiliated with `MetaCubeX` shall not contain the word `mihomo` in their names.**