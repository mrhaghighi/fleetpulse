# 0011 — Platform support policy

## Status

Accepted

## Context

The exporter runs on the machines being monitored, not on the server. Those
machines may run Linux, macOS or Windows, and the host APIs available for the
seven sensors are not the same on each. In particular, fan speed is readable
from Linux sysfs but has no portable API, and disk-busy counters differ by
platform.

## Decision

Linux is fully supported: all seven sensors are read from gopsutil or sysfs.
macOS and Windows are best-effort: the exporter runs, but a sensor that cannot
be read is omitted from `values` rather than faked, and some values are
documented approximations.

`fan_speed` is collected only on Linux, from
`/sys/class/hwmon/hwmon*/fan*_input`. `disk_busy` is approximated on macOS and
Windows. NIC capacity, when the link speed cannot be read, comes from the
`--nic-speed-mbps` flag. Missing sensors are visible in `/v1/info` and in the
exporter's `sensor_errors` counter.

## Alternatives considered

- **Claim cross-platform parity.** Dishonest: there is no portable fan API, and
  a fake zero would look like a working sensor that reports no fans.
- **Support Linux only.** Simple and correct, but the tool would not run on a
  developer laptop or a Windows host, which is where a demo and many first
  evaluations happen.
- **Ship separate exporters per platform.** Three code paths to release and
  test, and still no fan sensor on two of them.

## Consequences

Operators know what to expect per platform because `docs/sensors.md` has a table
of sources and limits. A missing series is a real absence, not a zero, so charts
show gaps where a sensor is unavailable.

The rank order matters when interpreting results: a Linux host is the reference,
and a macOS or Windows reading of `disk_busy` is an estimate. We accept that
difference instead of hiding it.

Platform-specific collection is isolated in build-tagged files
(`fan_linux.go`, `fan_other.go`, `nic_speed_linux.go`, `nic_speed_other.go`), so
adding full support for a new platform is a change in one place, not a fork of
the collector.
