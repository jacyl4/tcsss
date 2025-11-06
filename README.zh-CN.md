# TCSSS - 流量控制智能整形服务

[English](README.md)

> 自适应网络流量整形与系统资源优化守护进程

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

---

## 概述

- **智能流量整形**：自动对物理、虚拟接口配置 CAKE 队列，对本地回环配置 cubic 队列。
- **动态配置追踪**：实时监听网络拓扑并复用配置。
- **系统参数自适应**：根据可用内存档位选择 sysctl/rlimit 模板。
- **路由优化**：调整 TCP 拥塞窗口、拥塞控制与路由条目。
- **结构化日志**：输出 JSON 便于集中化监控。

---

## 快速开始

### 系统要求

| 组件 | 版本 | 说明 |
|------|------|------|
| Linux Kernel | 4.19+ | 需支持 CAKE (`sch_cake`) 模块 |
| iproute2 | 5.0+ | `tc` 命令需带 CAKE 支持 |
| ethtool | 任意 | 可选用于 offload 配置 |
| Go | 1.25+ | 仅用于编译 |

**内核模块要求**：`ifb`、`sch_cake`，可选 `nf_conntrack`

**权限要求**：`CAP_NET_ADMIN` + `CAP_NET_RAW` 或 root

### 安装步骤

```bash
# 1. 获取并编译代码
git clone https://github.com/jacyl4/tcsss.git
cd tcsss
make build          # 构建 amd64
make build-arm64    # 构建 ARM64

# 2. 部署模板
sudo mkdir -p /etc/tcsss
sudo cp templates/* /etc/tcsss/
# 仅保留一个 1-*.conf 用以判断运行模式

# 3. 安装二进制
sudo install -m 0755 tcsss /usr/local/bin/tcsss

# 4. 配置 systemd 服务（可选）
sudo cp systemd/tcsss.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now tcsss
```

### 安装验证

```bash
# 验证 CAKE 支持
tc qdisc add dev lo root cake && tc qdisc del dev lo root

# 查看服务状态
sudo systemctl status tcsss
```

---

## 使用指南

### 配置目录

- 默认目录：`/etc/tcsss`
- 必备文件：
  - `common.conf`
  - 至少一个 `limits_*.conf`（如 `limits_4gb.conf`）
  - 保留一个 `1-client.conf`、`1-server.conf` 或 `1-aggregate.conf` 三个文件选用一个用以判断运行模式
- 寻址顺序：`--conf` 参数 > `TCSSS_CONFIG_DIR` 环境变量 > `/etc/tcsss` > 可执行文件同目录 `templates/`
- 内存配置匹配顺序: 模板扫描会遍历目录中所有文件名（大小写不敏感），仅当名称满足 limits_<数值><mb|gb|tb>.conf 正则（允许小数，如
   limits_1.5gb.conf）时才加入候选；其中数值会换算成 MB 存入内存阶梯列表。
   运行时读取 /proc/meminfo 获得系统内存，再乘以 MemoryEffectivenessFactor=0.8 得到“有效内存”，按候选表从大到小挑选
   MemoryMB <= 有效内存 的最大模板；若全部超出则退回到最小阶梯，若有效内存异常或超过 MaximumSupportedMemoryMB（约 100
   TB）则报错。
   最终选中的 limits_*.conf 将被读取并与 common.conf 等模板一起生成 sysctl/limits
   配置，因此必须至少保留一份满足命名规则的限额模板。

### 命令行参数

```bash
tcsss [--conf <路径>] [--mode <client|server|aggregate>]
```

- `--conf`：指定外部模板目录。
- `--mode`：覆盖自动模式检测（可选）。
- 兼容旧语法 `tcsss c|s|a`，推荐使用新参数。

**示例**

```bash
tcsss                                # 默认目录 + 自动模式
tcsss --conf /opt/tcsss              # 使用自定义目录
tcsss --conf /etc/tcsss --mode server# 指定目录与模式
```

### systemd 管理

```bash
sudo systemctl start tcsss     # 启动
sudo systemctl status tcsss    # 查看状态
sudo journalctl -u tcsss -f    # 实时日志
sudo systemctl stop tcsss      # 停止
sudo systemctl enable tcsss    # 开机自启
```

### 配置验证

```bash
tc qdisc show                         # 查看全部 qdisc
tc -s qdisc show dev eth0             # 查看接口统计
ip link show | grep ifb4              # 检查 IFB 设备
tc filter show dev eth0 parent ffff:  # ingress 规则
sysctl net.ipv4.tcp_sack net.core.rmem_max  # 查看 sysctl
ip route show                         # 路由表
```

---

## 架构设计

### 代码结构

```
tcsss/
├── cmd/                               # CLI 可执行入口目录
│   └── tcsss/
│       └── main.go                     # 程序入口与启动流程
├── internal/                          # 内部业务逻辑与子模块
│   ├── app/
│   │   └── daemon.go                   # 守护进程生命周期管理
│   ├── config/
│   │   ├── constants.go                # 配置模块常量定义
│   │   ├── selector.go                 # 模板扫描与选择逻辑
│   │   └── types.go                    # 配置相关结构体声明
│   ├── detector/
│   │   ├── memory.go                   # 内存容量探测实现
│   │   ├── modules.go                  # 内核模块加载检测
│   │   └── runtime.go                  # 运行环境能力评估
│   ├── errors/
│   │   ├── context.go                  # 错误上下文封装
│   │   ├── errors.go                   # 统一错误类型定义
│   │   ├── logging.go                  # 错误日志辅助工具
│   │   └── multierror.go               # 多错误聚合处理
│   ├── route/
│   │   ├── config.go                   # 路由优化配置项
│   │   ├── deps.go                     # 路由优化依赖注入
│   │   ├── detection.go                # 路由环境检测逻辑
│   │   └── optimizer.go                # 路由表调优实现
│   ├── sysinfo/
│   │   └── memory.go                   # 系统内存信息读取
│   ├── syslimit/
│   │   ├── limits.go                   # /etc/security/limits 生成器
│   │   ├── rlimit.go                   # 进程 rlimit 应用器
│   │   └── sysctlconf.go               # sysctl.conf 渲染器
│   └── traffic/
│       ├── classifier.go               # 接口分类入口
│       ├── classifier_cache.go         # 分类结果缓存层
│       ├── classifier_detect.go        # 接口属性探测逻辑
│       ├── classifier_patterns.go      # 分类规则与模式
│       ├── constants.go                # 流量模块常量
│       ├── deps.go                     # 流量模块依赖注入
│       ├── ethtool_manager.go          # NIC offload 配置管理
│       ├── ifb_manager.go              # IFB 镜像设备管理
│       ├── netlink_watcher.go          # Netlink 事件监听
│       ├── profiles.go                 # CAKE 预设档位定义
│       ├── settings.go                 # 整形参数配置项
│       ├── shaper.go                   # 整形流程调度入口
│       ├── shaper_apply.go             # 整形执行与应用逻辑
│       ├── shaper_cleanup.go           # 整形资源清理流程
│       ├── shaper_errors.go            # 整形错误分类
│       ├── shaper_steps.go             # 整形步骤定义
│       ├── signature.go                # 接口签名与唯一性
│       ├── tc_config.go                # tc 配置模板生成
│       └── tc_executor.go              # tc 命令执行封装
├── systemd/                            # systemd 单元目录
├── templates/                          # 样例配置模板目录
├── go.mod                              # Go 模块依赖声明
├── go.sum                              # 模块哈希锁定文件
├── Makefile                            # 构建与工具命令
├── README.md                           # 英文说明文档
├── README.zh-CN.md                     # 中文说明文档
├── tcsss                                # amd64 构建产物
└── tcsss-arm64                          # ARM64 构建产物
```

### 初始化流程

```
main()
  ↓
signalContext()               ── 捕获信号
  ↓
resolveTemplateDir()          ── 解析配置目录
  ↓
LoadTrafficInitConfig()       ── 加载模式模板
  ↓
NewDaemon()
  ↓
daemon.Run(ctx)
  ├─ SysctlApplier.Apply()    ── 写入 /etc/sysctl.conf
  ├─ LimitsApplier.Apply()    ── 写入 limits.conf 与 system.conf
  ├─ RlimitApplier.Apply()    ── setrlimit() 当前进程
  └─ TrafficManager.Apply()
         ├─ RouteOptimizer.OptimizeRoutes()
         └─ applyInterfaces()
     TrafficManager.Watch()   ── 监听 netlink 事件
```

### 流量整形策略

- 接口分类：loopback / 外部物理 / 外部虚拟 / 内部虚拟跳过，基于名称前缀、sysfs、驱动与供应商信息。
- CAKE 配置：针对不同接口设置 MTU、RTT、Diffserv、ACK 过滤等预设。
- Ingress 整形：通过 IFB 镜像设备实现。

---

## 系统优化

### sysctl 调优

| 参数组 | 示例 | 1GB | 4GB | 8GB | 12GB+ |
|--------|------|-----|-----|-----|-------|
| TCP 缓冲区 | `net.ipv4.tcp_rmem` | 4K-87K-4M | 4K-87K-16M | 4K-87K-32M | 4K-87K-64M |
| TCP Backlog | `net.ipv4.tcp_max_syn_backlog` | 2048 | 4096 | 8192 | 16384 |
| Conntrack | `net.netfilter.nf_conntrack_max` | 65K | 262K | 524K | 1M |
| 文件句柄 | `fs.file-max` | 131072 | 524288 | 1048576 | 2097152 |

通用设置：启用 `tcp_sack`、`tcp_timestamps`、`tcp_window_scaling`，默认 `net.core.default_qdisc = cake`，`vm.swappiness = 10`。

### 资源限制

- rlimit：`nofile`、`nproc`、`memlock` 等随内存档位自动设定。
- PAM/systemd：生成 `/etc/security/limits.conf` 与 `/etc/systemd/system.conf` 默认限制。
- setrlimit：守护进程启动后立即应用，确保运行时资源充足。

### 路由优化

- 根据运行模式设定 `initcwnd`、`initrwnd` 及 loopback 窗口。
- 自动探测主网卡并锁定拥塞控制算法（如 `cubic`）。
- 启用 `fastopen_no_cookie` 缩短握手时延。

---

## 故障排除

1. **服务无法启动**：使用 `journalctl -u tcsss` 查看日志，检查二进制权限与 capabilities。
2. **CAKE 命令失败**：确认 `sch_cake` 模块已加载，`tc -V` ≥ 5.0。
3. **接口未被配置**：确认接口不在跳过前缀列表且状态为 UP。
4. **sysctl 未生效**：检查 `/etc/sysctl.conf` 生成内容，必要时执行 `sysctl --system`。

---

## 开发指南

```bash
make build          # 构建
make build-arm64    # 构建 ARM64
make clean          # 清理产物
go vet ./...        # 静态检查
go fmt ./...        # 代码格式化
```

主要依赖：`github.com/vishvananda/netlink`、`golang.org/x/sys`。

---

## 注意事项

- 生产环境建议使用 capabilities 而非 root；首次运行前备份 `/etc/sysctl.conf` 与 `/etc/security/limits.conf`。
- 容器环境需要 `--cap-add=NET_ADMIN`；部分云厂商限制 offload 配置。
- CAKE 属软件实现，可能带来 <5% CPU 开销，但可显著降低排队延迟。
- 请先在测试环境验证配置，再在生产环境应用。

---

## 许可证

MIT License

---

## 作者

- [jacyl4](https://blog.seso.icu)
