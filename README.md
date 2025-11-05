# TCSSS - Traffic Control Smart Shaping Service / 流量控制智能整形服务

> **Adaptive network traffic shaping and system resource optimization daemon**  
> **自适应网络流量整形与系统资源优化守护进程**

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

---

## 概述 Overview

- **智能流量整形**：自动为物理、虚拟与环回接口配置 CAKE 队列；  
  **Intelligent shaping**: Automatically applies CAKE qdisc to physical, virtual, and loopback interfaces.
- **动态配置追踪**：实时监听网络拓扑并复用配置；  
  **Dynamic tracking**: Monitors topology changes and reapplies policies in real time.
- **系统参数自适应**：根据可用内存档位选择 sysctl/rlimit 模板；  
  **Adaptive tuning**: Selects sysctl/rlimit templates based on detected memory tiers.
- **路由优化**：调整 TCP 拥塞窗口、拥塞控制与路由条目；  
  **Route optimization**: Tunes TCP cwnd/rwnd, congestion control, and per-route attributes.
- **结构化日志**：输出 JSON 便于集中化监控。  
  **Structured logging**: Emits JSON logs for observability pipelines.

---

## 快速开始 Quick Start

### 系统要求 System Requirements

| 组件 Component | 版本 Version | 说明 Notes |
|----------------|-------------|-----------|
| Linux Kernel   | 4.19+       | 需支持 CAKE (`sch_cake`) 模块 / Requires CAKE module |
| iproute2       | 5.0+        | `tc` 命令需带 CAKE 支持 / `tc` must support CAKE |
| ethtool        | 任意 Any    | 可选用于 offload 配置 / Optional for NIC offloads |
| Go             | 1.25+       | 仅用于编译 / Build-time only |

**内核模块要求 Required kernel modules**: `ifb`, `sch_cake`, （可选 optional）`nf_conntrack`  
**权限 Permissions**: `CAP_NET_ADMIN` + `CAP_NET_RAW` 或 root / or root

### 安装 Installation

```bash
# 1. 获取并编译代码 Fetch & build
git clone https://github.com/jacyl4/tcsss.git
cd tcsss
make build          # 构建 amd64 / build for amd64
make build-arm64    # 构建 ARM64 / build for ARM64

# 2. 部署模板 Deploy templates
sudo mkdir -p /etc/tcsss
sudo cp templates/* /etc/tcsss/
# 根据运行模式仅保留一个 1-*.conf / Keep exactly one 1-*.conf file

# 3. 安装二进制 Install binary
sudo install -m 0755 tcsss /usr/local/bin/tcsss

# 4. 配置 systemd 服务（可选 optional）
sudo cp systemd/tcsss.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now tcsss
```

### 安装验证 Verify Installation

```bash
# 验证 CAKE 支持 / verify CAKE support
tc qdisc add dev lo root cake && tc qdisc del dev lo root

# 查看服务状态 / check service status
sudo systemctl status tcsss
```

---

## 使用方法 Usage

### 配置目录 Configuration Directory

- 默认目录 Default path: `/etc/tcsss`
- 至少需要的文件 Required files:
  - `common.conf`
  - 至少一个 `limits_*.conf`（如 `limits_4gb.conf`）/ at least one memory tier file
  - 至少一个 `1-client.conf`、`1-server.conf`、或 `1-aggregate.conf`
- 寻址顺序 Resolution order: `--conf` 参数 > `TCSSS_CONFIG_DIR` 环境变量 > `/etc/tcsss` > 可执行文件同目录 `templates/`

### 命令行参数 CLI Flags

```bash
tcsss [--conf <path>] [--mode <client|server|aggregate>]
```

- `--conf`：指定外部模板目录；  
  `--conf`: override configuration directory.
- `--mode`：覆盖自动模式检测，可选；  
  `--mode`: override auto-detected traffic mode (optional).
- 兼容旧语法 `tcsss c|s|a`；推荐使用新 flags。  
  Legacy shorthand `tcsss c|s|a` remains available.

**示例 Examples**

```bash
tcsss                                # 默认目录 + 自动模式 / default path & auto mode
tcsss --conf /opt/tcsss              # 自定义目录 / custom directory
tcsss --conf /etc/tcsss --mode server# 指定目录与模式 / custom path & explicit mode
```

### systemd 管理 Systemd Management

```bash
sudo systemctl start tcsss     # 启动 / start
sudo systemctl status tcsss    # 状态 / status
sudo journalctl -u tcsss -f    # 实时日志 / tail logs
sudo systemctl stop tcsss      # 停止 / stop
sudo systemctl enable tcsss    # 开机自启 / enable on boot
```

### 配置验证 Validate Configuration

```bash
tc qdisc show                         # 查看全部 qdisc / list all qdiscs
tc -s qdisc show dev eth0             # 查看接口统计 / interface stats
ip link show | grep ifb4              # 检查 IFB 设备 / verify IFB devices
tc filter show dev eth0 parent ffff:  # ingress 规则 / ingress filters
sysctl net.ipv4.tcp_sack net.core.rmem_max  # 查看 sysctl / check sysctl
ip route show                         # 路由表 / routing table
```

---

## 架构设计 Architecture

### 代码结构 Code Layout

```
tcsss/
├── cmd/tcsss/main.go                 # 程序入口 / entry point
├── internal/
│   ├── app/daemon.go                 # 守护进程协调器 / daemon orchestrator
│   ├── detector/                     # 内存/运行环境检测 / detectors
│   ├── errors/                       # 分类错误体系 / categorized errors
│   ├── config/                       # 模板扫描与默认配置 / template selectors & defaults
│   ├── syslimit/                     # sysctl/limits/rlimit 管理 / system limit appliers
│   └── traffic/                      # 流量整形核心 / traffic shaping core
│       ├── shaper.go                 # 协调器 / shaper orchestrator
│       ├── classifier.go             # 接口分类 / interface classifier
│       ├── netlink_watcher.go        # Netlink 监听 / netlink watcher
│       ├── ifb_manager.go            # IFB 管理 / IFB manager
│       ├── route_optimizer.go        # 路由优化 / route optimizer
│       └── ...
├── templates/                        # 样例模板 / sample templates
└── systemd/tcsss.service             # systemd 单元 / service unit
```

### 初始化流程 Initialization Flow

```
main()
  ↓
signalContext()               ── 捕获信号 / capture signals
  ↓
resolveTemplateDir()          ── 解析配置目录 / resolve template directory
  ↓
LoadTrafficInitConfig()       ── 加载模式模板 / load traffic template
  ↓
NewDaemon()
  ↓
daemon.Run(ctx)
  ├─ SysctlApplier.Apply()    ── 写入 /etc/sysctl.conf
  ├─ LimitsApplier.Apply()    ── 写入 limits.conf & system.conf
  ├─ RlimitApplier.Apply()    ── setrlimit() 当前进程 / current process
  └─ TrafficManager.Apply()
         ├─ RouteOptimizer.OptimizeRoutes()
         └─ applyInterfaces()
     TrafficManager.Watch()   ── 监听 netlink / watch netlink events
```

### 流量整形策略 Shaping Strategy

- 接口分类 Interface classification: loopback / external physical / external virtual / internal virtual skip.  
  基于名称前缀、sysfs、驱动与供应商信息 / Uses name prefixes, sysfs path, driver, vendor metadata.
- CAKE 配置 Profiles: 针对不同接口设置 MTU、RTT、Diffserv、ACK 过滤。  
  Provides MTU, RTT, diffserv, and ACK filtering presets per class.
- Ingress 整形 Ingress shaping: 通过 IFB 镜像设备实现 / uses IFB mirror devices.

---

## 系统优化 System Optimization

### sysctl 调优 Sysctl Tuning

| 参数组 Category | 示例 Example | 1GB | 4GB | 8GB | 12GB+ |
|-----------------|-------------|-----|-----|-----|-------|
| TCP 缓冲区 TCP buffers | `net.ipv4.tcp_rmem` | 4K-87K-4M | 4K-87K-16M | 4K-87K-32M | 4K-87K-64M |
| TCP Backlog     | `net.ipv4.tcp_max_syn_backlog` | 2048 | 4096 | 8192 | 16384 |
| Conntrack       | `net.netfilter.nf_conntrack_max` | 65K | 262K | 524K | 1M |
| 文件句柄 File descriptors | `fs.file-max` | 131072 | 524288 | 1048576 | 2097152 |

通用设置 Common tweaks: 启用 `tcp_sack`, `tcp_timestamps`, `tcp_window_scaling`，默认 `net.core.default_qdisc = cake`，`vm.swappiness = 10`。

### 资源限制 Resource Limits

- rlimit：`nofile`、`nproc`、`memlock` 等随内存档位自动设定。  
- PAM/systemd：生成 `/etc/security/limits.conf` 与 `/etc/systemd/system.conf` 默认限制。  
- setrlimit：守护进程启动后立即应用，保证运行时资源。

### 路由优化 Route Optimization

- 根据运行模式设定 `initcwnd` / `initrwnd` / loopback 窗口。  
- 自动探测主网卡并锁定拥塞控制算法（如 `cubic`）。  
- 启用 `fastopen_no_cookie` 以缩短握手时延。

---

## 故障排除 Troubleshooting

1. **服务无法启动 Service fails to start**：使用 `journalctl -u tcsss` 查看日志，检查二进制权限与 capabilities。  
2. **CAKE 命令失败 TC errors**：确认 `sch_cake` 模块已加载，`tc -V` ≥ 5.0。  
3. **接口未被配置 Interfaces skipped**：确认接口非 skip 前缀且状态为 UP。  
4. **sysctl 未生效 Sysctl not applied**：检查 `/etc/sysctl.conf` 生成内容，必要时 `sysctl --system`。

---

## 开发指南 Development

```bash
make build          # 构建 / build
make build-arm64    # 构建 ARM64 / build ARM64
make clean          # 清理产物 / clean artifacts
go vet ./...        # 静态检查 / static analysis
go fmt ./...        # 代码格式化 / code format
```

主要依赖 Key dependencies: `github.com/vishvananda/netlink`, `golang.org/x/sys`。

---

## 注意事项 Notes

- 生产环境建议使用 capabilities 而非 root；首次运行前备份 `/etc/sysctl.conf` 与 `/etc/security/limits.conf`。  
- 容器环境需要 `--cap-add=NET_ADMIN`；部分云厂商限制 offload 配置。  
- CAKE 属软件实现，可能带来 <5% CPU 开销，但显著降低排队延迟。  
- 请先在测试环境验证配置，再在生产环境应用。

---

## 许可证 License

MIT License

---

## 作者 Authors

- [jacyl4](https://blog.seso.icu)
