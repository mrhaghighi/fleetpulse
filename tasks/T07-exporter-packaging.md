# T07 — Exporter packaging and per-OS install guides
**Depends on:** T06 · **Read:** `AGENTS.md`, `contracts/05-...`

## Goal
Someone can download one binary and run the exporter as a service on Linux, macOS or Windows.

## Files
- `scripts/dist.sh` (used by `make dist`): builds `dist/fleetpulse-exporter_<version>_<os>_<arch>[.exe]` for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64 with `CGO_ENABLED=0`, `-trimpath`, version ldflags, plus `dist/SHA256SUMS`.
- `deploy/exporter/fleetpulse-exporter.service` (systemd: dedicated user, `Restart=always`, `NoNewPrivileges`, `ProtectSystem=strict`, `ExecStart=/usr/local/bin/fleetpulse-exporter --listen :9100`).
- `deploy/exporter/com.fleetpulse.exporter.plist` (launchd LaunchDaemon, `KeepAlive`).
- `deploy/exporter/install-windows.ps1` (creates a service using the built-in `New-Service` or `sc.exe`; document that the binary is run via a wrapper because a plain console exe is not a native Windows service — recommend NSSM or `sc.exe` + `srvany`-style wrapper; keep it honest: **untested on Windows**).
- `docs/exporter/linux.md`, `docs/exporter/macos.md`, `docs/exporter/windows.md`: download/build, run in foreground, install as service, open the firewall port, verify with `curl`, add the host to `scraper.yaml`, uninstall, troubleshooting. Windows and macOS guides must contain a banner: "Best-effort support: see docs/sensors.md for which sensors are available".

## Acceptance
`make dist` produces 5 binaries + checksums; `file dist/*` shows correct formats; Linux binary starts and serves `/v1/info`. A reviewer follows `docs/exporter/linux.md` literally on a clean Linux VM and it works (note who verified in the PR).
