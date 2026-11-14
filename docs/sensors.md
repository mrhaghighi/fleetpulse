# Sensors

FleetPulse samples seven host sensors every second and reports each as a percentage in `[0, 100]`. The exporter rounds values to two decimals and clamps them; a sensor that cannot be read is omitted from `values` instead of being reported as a fake `0` (contract 01).

CPU, memory, disk and network counters use [`github.com/shirou/gopsutil/v4`](https://github.com/shirou/gopsutil), which is pure Go (no cgo) and has implementations for Linux, macOS and Windows. On Linux, fan speed and NIC link speed are read directly from sysfs; the other platforms omit or fall back as described below.

| id | sensor | source |
|----|--------|--------|
| 1 | `cpu_usage` | `cpu.PercentWithContext(ctx, 0, false)` |
| 2 | `memory_usage` | `mem.VirtualMemoryWithContext(ctx).UsedPercent` |
| 3 | `disk_usage` | `disk.UsageWithContext(ctx, path).UsedPercent` |
| 4 | `disk_busy` | `disk.IOCountersWithContext(ctx)`, max over whole disks |
| 5 | `net_in` | `net.IOCountersWithContext(ctx, true).BytesRecv` |
| 6 | `net_out` | `net.IOCountersWithContext(ctx, true).BytesSent` |
| 7 | `fan_speed` | `<sysfs>/class/hwmon/hwmon*/fan*_input` (Linux only) |

## `cpu_usage`

CPU busy percentage for all cores combined, measured over the interval between two calls to gopsutil rather than over the lifetime of the process.

- The collector primes gopsutil once when it is constructed. The constructor call with interval `0` only stores the baseline, so the first sampling tick covers the interval since startup.
- Each tick calls `cpu.PercentWithContext(ctx, 0, false)`; `false` means "combined, not per core".
- gopsutil reads `/proc/stat` on Linux, `host_statistics`/`host_processor_info` on macOS and `NtQuerySystemInformation` on Windows.
- Approximation: the value is the busy share of the interval, not an instantaneous sample. A one-second interval smooths short spikes. CPU accounting details differ slightly per OS (for example steal time on Linux and parked cores on macOS/Windows), so values are comparable but not identical across platforms.

## `memory_usage`

RAM in use as a percentage of total physical memory, taken directly from gopsutil's `UsedPercent`.

- Linux uses `MemTotal` and `MemAvailable` from `/proc/meminfo`; macOS uses `vm_stat` and `sysctl`; Windows uses `GlobalMemoryStatusEx`.
- Approximation: "used" excludes reclaimable cache differently per OS. Linux `UsedPercent` follows gopsutil's notion of used memory, macOS treats some cached pages as used, and Windows reports the committed physical load. A value near 100% is normal on macOS and on Linux machines with large page caches.

## `disk_usage`

Space used on the file system that holds the monitored path.

- `disk.UsageWithContext(ctx, path)` is called with `--disk-path` (`FLEETPULSE_EXPORTER_DISK_PATH`).
- The default path is `/` on Linux and macOS and `C:\` on Windows. Both are chosen in `cmd/exporter`.
- On Windows, any path on the `C:` volume reports that volume. On Linux and macOS a path reports the file system mounted at that path, which may be a bind mount, an overlay (containers) or an APFS volume that shares free space with other volumes in the same container.
- Approximation: `UsedPercent` is `used / (used + free)` as reported by the OS; reserved blocks, snapshots and deduplication mean this is not always the same number `df` prints.

## `disk_busy`

Percentage of wall-clock time the busiest whole disk spent doing I/O. This is the maximum across disks, not an average.

For each disk, the collector keeps the previous counters and the previous wall-clock time, then computes:

```text
busy% = (current_time_counter - previous_time_counter) / wall_clock_delta × 100
```

- **Linux**: `IoTime` from `/proc/diskstats` (field 13), which is the time the device had I/O in progress in milliseconds. This is the accurate path.
- **macOS**: gopsutil synthesises `IoTime` as `ReadTime + WriteTime` in milliseconds from IOKit block-device statistics. It measures service time rather than wall-clock queue occupancy, so it is approximate and can under-report concurrent I/O.
- **Windows**: `IoTime` is not exposed. The fallback is `ReadTime + WriteTime` from `DISK_PERFORMANCE`, in milliseconds (the collector opts in to the millisecond unit; gopsutil otherwise reports seconds there).
- The collector prefers `IoTime`; when both readings have `IoTime == 0` (always on Windows, and on an idle Linux disk) it falls back to `ReadTime + WriteTime`.
- Because multiple requests can overlap, the computed value can exceed 100; it is clamped to `[0, 100]`.
- Only whole disks are considered. Devices whose names start with `loop` or `ram` are skipped, and partitions are skipped when their parent disk is present (`sda1` when `sda` exists, `nvme0n1p1` when `nvme0n1` exists). `dm-*` devices are kept.
- The first tick and any tick whose counters went backwards (reset) report `ok=false`, so the sensor is omitted rather than reported as `0`.

Known limits: the maximum hides a busy second disk; the counter covers the whole device, including I/O from other containers or users; macOS values are service-time based; Windows values depend on the drive supporting `IOCTL_DISK_PERFORMANCE` (virtual drives may expose no counters, in which case the sensor is omitted).

## `net_in` and `net_out`

Inbound and outbound traffic as a percentage of the summed capacity of the interfaces carrying that traffic.

For every tick the collector:

1. reads the interface list and keeps interfaces whose flags contain `up` and do not contain `loopback`;
2. reads per-interface byte counters with `net.IOCountersWithContext(ctx, true)`;
3. for each monitored interface present in both readings, takes `BytesRecv` for `net_in` or `BytesSent` for `net_out` and computes `rate_i = Δbytes_i × 8 / Δwall_seconds`;
4. looks up that interface's capacity: the detected link speed when available, otherwise `--nic-speed-mbps` (`FLEETPULSE_EXPORTER_NIC_SPEED_MBPS`, default `1000`);
5. computes `percent = Σ rate_i / Σ (capacity_i × 1_000_000) × 100`, clamped to `[0, 100]`.

- On Linux, capacity is detected from `<sysfs>/class/net/<iface>/speed` in Mb/s. A missing file, an error or the usual `-1` value means the speed is unknown and the configured fallback is used for that interface.
- On macOS and Windows there is no sysfs, so every counted interface uses the configured fallback.
- The first tick only records a baseline and reports `ok=false`.
- If any counted interface's byte counter went backwards (reset, driver reload, interface recreation) the whole tick reports `ok=false`; an interface that was not present on the previous tick is ignored for that tick and adds neither traffic nor capacity.
- On all three OSes gopsutil reads the same per-interface byte counters the OS exposes (`/proc/net/dev` on Linux, `getifaddrs` on macOS, `GetIfTable2` on Windows).

Approximations and limits:

- **Link speed is nominal, not measured throughput.** Autodetected sysfs speed describes the negotiated link rate (for example `10000` for 10 GbE), not what the link can currently sustain.
- Wi-Fi, VPN, bridge, veth and other virtual interfaces frequently report `-1`, `0` or a synthetic speed; those interfaces fall back to `--nic-speed-mbps`.
- Summing capacity assumes each counted interface has its own link. A VPN tunnel over the same physical NIC can double-count capacity, and a bond may expose either the bond speed or the member speeds. Set `--nic-speed-mbps` accordingly if the defaults are misleading.
- Traffic is bidirectional only in the sense that `net_in` and `net_out` are separate sensors; full-duplex links still use the same capacity for each direction.
- Because sampling is once per second, a short burst is averaged over the interval and very short spikes can be missed.

## `fan_speed`

Fan speed as a percentage of the fan's maximum RPM.

- **Linux only**: scans `<sysfs>/class/hwmon/hwmon*/fan*_input` for RPM values. For each fan, the maximum is `fanN_max` in the same directory when it exists and is greater than zero, otherwise `--fan-max-rpm` (`FLEETPULSE_EXPORTER_FAN_MAX_RPM`, default `5000`).
- Each fan's percentage is `rpm / max × 100`; the collector reports the highest percentage across all readable fans, clamped to `[0, 100]`.
- Unreadable files, garbage, non-finite and negative RPM values are skipped. If no fan files exist, or every fan is unreadable, the sensor reports `ok=false` and is omitted from `values`.
- **macOS and Windows**: always omitted. macOS fans are only reachable through the SMC and Windows through model-specific WMI classes; FleetPulse deliberately does not implement those vendor-specific, sometimes privileged paths. Reporting "unavailable" is more honest than a fake or partial value.
- No baseline is needed, so the first tick can already report a fan value.

### Why is `fan_speed` missing on my VM, container or laptop?

- **VMs and cloud hosts**: the hypervisor normally exposes no hwmon fan devices to the guest because there is no physical fan to read. Some hosts virtualise the entire platform, including a fake thermal device, but most do not.
- **Containers**: the collector only sees hwmon devices that the runtime and host bind into `/sys`. `/sys/class/hwmon` is often empty, or contains temperature sensors without `fan*_input`, and permissions may differ from the host.
- **Laptops and desktops**: many machines do not load a hwmon driver that exports `fan*_input`, even though a physical fan exists; vendors sometimes expose it only through their own tools or ACPI/WMI.
- **macOS and Windows**: omitted by design on every machine.

A missing `fan_speed` is normal and is not an error. The other sensors keep working.

## Common limits

- Values are sampled once per second and aligned to whole seconds. Counters are read once per tick; a tick that takes longer than the interval is skipped rather than queued.
- All counter-based sensors (`disk_busy`, `net_in`, `net_out`) need two readings. Expect them to be missing from the first sample after startup.
- Counter resets and counter wrap are treated as "no reading for this tick", never as a negative or fake value.
- The exporter runs on the host and reports that host's view. In a container, Linux counters come from the container's namespaces where available and from the host otherwise.
