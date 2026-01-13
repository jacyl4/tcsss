# TCSSS - Traffic Control Smart Shaping Service

[简体中文](README.zh-CN.md)

> Adaptive network traffic shaping and system resource optimization daemon

[![Go Version](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

---

## Overview

- **Intelligent shaping**: Applies CAKE queues to physical and virtual interfaces and configures cubic for loopback.
- **Dynamic configuration tracking**: Watches topology changes in real time and reapplies policies automatically.
- **Adaptive system tuning**: Picks sysctl and rlimit templates according to detected memory tiers.
- **Route optimization**: Adjusts TCP congestion windows, congestion control, and per-route attributes.
- **Structured logging**: Emits JSON logs ready for centralized observability pipelines.

---

## Quick Start

### System Requirements

| Component | Version | Notes |
|-----------|---------|-------|
| Linux Kernel | 4.19+ | Must support the CAKE (`sch_cake`) module |
| iproute2 | 5.0+ | `tc` command must include CAKE support |
| ethtool | Any | Optional, used for NIC offload tuning |
| Go | 1.25+ | Build-time dependency only |

**Required kernel modules**: `ifb`, `sch_cake` (optional `nf_conntrack`)

**Required capabilities**: `CAP_NET_ADMIN` + `CAP_NET_RAW` or root

### Installation Steps

```bash
# 1. Fetch and build the project
git clone https://github.com/jacyl4/tcsss.git
cd tcsss
make build          # Build amd64 binary
make build-arm64    # Build ARM64 binary

# 2. Deploy template files
sudo mkdir -p /etc/tcsss
sudo cp templates/* /etc/tcsss/
# Keep exactly one 1-*.conf to determine operating mode

# 3. Install the binary
sudo install -m 0755 tcsss /usr/local/bin/tcsss

# 4. Configure systemd service (optional)
sudo cp systemd/tcsss.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now tcsss
```

### Verify Installation

```bash
# Validate CAKE support
tc qdisc add dev lo root cake && tc qdisc del dev lo root

# Check service status
sudo systemctl status tcsss
```

---

## Usage

### Configuration Directory

- Default location: `/etc/tcsss`
- Required files:
  - `common.conf`
  - At least one `limits_*.conf` (for example `limits_4gb.conf`)
  - Exactly one of `1-client.conf`, `1-server.conf`, or `1-aggregate.conf` to determine the traffic mode
- Resolution order: `--conf` flag > `TCSSS_CONFIG_DIR` environment variable > `/etc/tcsss` > `templates/` alongside the executable
- Memory tier selection: The scanner iterates over all filenames (case-insensitive) and only accepts those matching `limits_<value><mb|gb|tb>.conf` (decimals allowed, such as `limits_1.5gb.conf`). Each numeric value is converted to MB and added to a tier list. At runtime `/proc/meminfo` provides system memory, which is multiplied by `MemoryEffectivenessFactor = 0.8` to produce the “effective memory” value. The selector walks tiers from largest to smallest and chooses the greatest `MemoryMB` that does not exceed the effective memory; if every tier is larger, it falls back to the smallest tier. Invalid readings or values above `MaximumSupportedMemoryMB` (~100 TB) raise an error. The selected `limits_*.conf`, together with `common.conf` and other templates, renders sysctl/limits output, so at least one properly named template must be present.

### CLI Flags

```bash
tcsss [--conf <path>] [--mode <client|server|aggregate>]
```

- `--conf`: Override the configuration directory.
- `--mode`: Force a traffic mode instead of auto-detection (optional).
- Legacy shorthand `tcsss c|s|a` remains supported, but the flag format is recommended.

**Examples**

```bash
tcsss                                # Default directory with auto-detected mode
tcsss --conf /opt/tcsss              # Custom configuration directory
tcsss --conf /etc/tcsss --mode server# Custom directory with explicit mode
```

### systemd Management

```bash
sudo systemctl start tcsss     # Start service
sudo systemctl status tcsss    # Inspect service status
sudo journalctl -u tcsss -f    # Follow structured logs
sudo systemctl stop tcsss      # Stop service
sudo systemctl enable tcsss    # Enable at boot
```

### Validate Configuration

```bash
tc qdisc show                         # List all qdiscs
tc -s qdisc show dev eth0             # View per-interface statistics
ip link show | grep ifb4              # Confirm IFB mirror devices
tc filter show dev eth0 parent ffff:  # Inspect ingress filters
sysctl net.ipv4.tcp_sack net.core.rmem_max  # Inspect sysctl values
ip route show                         # Display routing table
```

---

## Architecture

### Code Structure

```
tcsss/
├── cmd/                               # CLI entry-point directory
│   └── tcsss/
│       └── main.go                     # Application entry and bootstrap logic
├── internal/                          # Internal business logic modules
│   ├── app/
│   │   └── daemon.go                   # Daemon lifecycle orchestration
│   ├── config/
│   │   ├── constants.go                # Configuration module constants
│   │   ├── selector.go                 # Template scanning and selection
│   │   └── types.go                    # Configuration data structures
│   ├── detector/
│   │   ├── memory.go                   # Memory capacity detection
│   │   ├── modules.go                  # Kernel module availability checks
│   │   └── runtime.go                  # Runtime capability evaluation
│   ├── errors/
│   │   ├── context.go                  # Error context helpers
│   │   ├── errors.go                   # Shared error type definitions
│   │   ├── logging.go                  # Error logging utilities
│   │   └── multierror.go               # Aggregated error handling
│   ├── route/
│   │   ├── config.go                   # Route-optimization configuration
│   │   ├── deps.go                     # Route module dependency wiring
│   │   ├── detection.go                # Route environment detection
│   │   └── optimizer.go                # Routing table optimization logic
│   ├── sysinfo/
│   │   └── memory.go                   # System memory information reader
│   ├── syslimit/
│   │   ├── limits.go                   # /etc/security/limits generator
│   │   ├── rlimit.go                   # Process rlimit applier
│   │   └── sysctlconf.go               # sysctl.conf renderer
│   └── traffic/
│       ├── classifier.go               # Interface classification entry point
│       ├── classifier_cache.go         # Classification cache layer
│       ├── classifier_detect.go        # Interface attribute detection
│       ├── classifier_patterns.go      # Classification patterns and rules
│       ├── constants.go                # Traffic module constants
│       ├── deps.go                     # Traffic module dependency wiring
│       ├── ethtool_manager.go          # NIC offload manager
│       ├── ifb_manager.go              # IFB mirror device manager
│       ├── netlink_watcher.go          # Netlink event watcher
│       ├── profiles.go                 # CAKE preset definitions
│       ├── settings.go                 # Traffic shaping configuration
│       ├── shaper.go                   # Shaping workflow coordinator
│       ├── shaper_apply.go             # Shaping apply logic
│       ├── shaper_cleanup.go           # Shaping cleanup routines
│       ├── shaper_errors.go            # Shaping error taxonomy
│       ├── shaper_steps.go             # Shaping step definitions
│       ├── signature.go                # Interface signature helpers
│       ├── tc_config.go                # tc configuration template builder
│       └── tc_executor.go              # tc command executor wrapper
├── systemd/                            # systemd unit directory
│   └── tcsss.service                   # Service unit file
├── templates/                          # Sample configuration templates
│   ├── 1-aggregate.conf                # Aggregate traffic-mode template
│   ├── 1-client.conf                   # Client traffic-mode template
│   ├── 1-server.conf                   # Server traffic-mode template
│   ├── common.conf                     # Shared sysctl/limits template
│   ├── limits_1gb.conf                 # 1 GB memory tier template
│   ├── limits_4gb.conf                 # 4 GB memory tier template
│   ├── limits_8gb.conf                 # 8 GB memory tier template
│   └── limits_12gb.conf                # 12 GB memory tier template
├── go.mod                              # Go module definition
├── go.sum                              # Module checksum file
├── Makefile                            # Build and tooling targets
├── README.md                           # English documentation
├── README.zh-CN.md                     # Chinese documentation
├── tcsss                                # amd64 build artifact
└── tcsss-arm64                          # ARM64 build artifact
```

### Initialization Flow

```
main()
  ↓
signalContext()               ── capture process signals
  ↓
resolveTemplateDir()          ── resolve configuration directory
  ↓
LoadTrafficInitConfig()       ── load traffic profile template
  ↓
NewDaemon()
  ↓
daemon.Run(ctx)
  ├─ SysctlApplier.Apply()    ── write /etc/sysctl.conf
  ├─ LimitsApplier.Apply()    ── write limits.conf and system.conf
  ├─ RlimitApplier.Apply()    ── call setrlimit() on current process
  └─ TrafficManager.Apply()
         ├─ RouteOptimizer.OptimizeRoutes()
         └─ applyInterfaces()
     TrafficManager.Watch()   ── monitor netlink events
```

### Traffic Shaping Strategy

- Interface classification groups loopback, external physical, and external virtual devices while skipping internal-only virtual interfaces by using name prefixes, sysfs paths, driver metadata, and vendor information.
- CAKE profiles set MTU, RTT, Diffserv, and ACK filtering presets per interface category.
- Ingress shaping is implemented with IFB mirror devices.

---

## System Optimization

### sysctl Tuning

| Category | Example | 1GB | 4GB | 8GB | 12GB+ |
|----------|---------|-----|-----|-----|-------|
| TCP buffers | `net.ipv4.tcp_rmem` | 4K-87K-4M | 4K-87K-16M | 4K-87K-32M | 4K-87K-64M |
| TCP backlog | `net.ipv4.tcp_max_syn_backlog` | 2048 | 4096 | 8192 | 16384 |
| Conntrack | `net.netfilter.nf_conntrack_max` | 65K | 262K | 524K | 1M |
| File descriptors | `fs.file-max` | 131072 | 524288 | 1048576 | 2097152 |

Common tweaks: enable `tcp_sack`, `tcp_timestamps`, and `tcp_window_scaling`; set `net.core.default_qdisc = cake`; configure `vm.swappiness = 10`.

### Resource Limits

- `rlimit` values for `nofile`, `nproc`, and `memlock` scale automatically with memory tiers.
- PAM/systemd templates populate `/etc/security/limits.conf` and `/etc/systemd/system.conf` with sensible defaults.
- `setrlimit` is applied immediately after startup to guarantee runtime resources.

### Route Optimization

- Adjusts `initcwnd`, `initrwnd`, and loopback windows according to the selected traffic mode.
- Auto-detects the primary NIC and pins the congestion control algorithm (for example `cubic`).
- Enables `fastopen_no_cookie` to reduce handshake latency.

---

## Troubleshooting

1. **Service fails to start**: Inspect `journalctl -u tcsss`, then verify binary permissions and capabilities.
2. **CAKE commands fail**: Ensure the `sch_cake` module is loaded and `tc -V` is at least 5.0.
3. **Interfaces skipped**: Confirm interfaces are not in the skip list and remain in the UP state.
4. **sysctl not applied**: Review `/etc/sysctl.conf` output and run `sysctl --system` if necessary.

---

## Development

```bash
make build          # Build amd64
make build-arm64    # Build ARM64
make clean          # Remove build artifacts
go vet ./...        # Static analysis
go fmt ./...        # Code formatting
```

Key dependencies: `github.com/vishvananda/netlink`, `golang.org/x/sys`.

---

## Notes

- Prefer Linux capabilities rather than root in production; back up `/etc/sysctl.conf` and `/etc/security/limits.conf` before first run.
- Containers must grant `--cap-add=NET_ADMIN`; some cloud vendors restrict NIC offload configuration.
- CAKE is a software shaper that may add <5% CPU overhead while significantly cutting queueing delay.
- Always validate configuration changes in staging before deploying to production.

---

## License

MIT License

---

## Authors

- [jacyl4](https://blog.seso.icu)
