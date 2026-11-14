# Install the exporter on macOS

> Best-effort support: see docs/sensors.md for which sensors are available

The exporter is a single static binary. On macOS it samples CPU, memory, disk, and network; `fan_speed` is omitted because macOS exposes fans only through the SMC. This guide installs it as a launchd daemon.

## Get the binary

### Build from a checkout

Run these commands from the repository root:

```sh
VERSION=0.1.0
case "$(uname -m)" in
  arm64) ARCH=arm64 ;;
  x86_64) ARCH=amd64 ;;
  *) echo "unsupported architecture: $(uname -m)"; exit 1 ;;
esac
BIN="fleetpulse-exporter_${VERSION}_darwin_${ARCH}"
CGO_ENABLED=0 GOOS=darwin GOARCH="$ARCH" go build -trimpath -ldflags "-s -w -X fleetpulse/internal/version.Version=${VERSION}" -o "$BIN" ./cmd/exporter
```

`make dist` builds all five release targets and writes checksums:

```sh
make dist
cat dist/SHA256SUMS
```

### Use a prebuilt artifact

Download `fleetpulse-exporter_<version>_darwin_<arch>` from the release page into the current directory, then set the variables so the rest of the guide can be pasted unchanged:

```sh
VERSION=0.1.0
ARCH=arm64
BIN="fleetpulse-exporter_${VERSION}_darwin_${ARCH}"
```

Use `ARCH=amd64` on an Intel Mac and `ARCH=arm64` on Apple Silicon. If the file came from a browser, remove the quarantine flag:

```sh
xattr -d com.apple.quarantine "$BIN" 2>/dev/null || true
chmod +x "$BIN"
./"$BIN" --version
```

The output is `exporter 0.1.0` (or the version you built).

## Run it in the foreground

```sh
./"$BIN" --listen :9100
```

Leave it running and verify from a second terminal:

```sh
curl -s http://localhost:9100/healthz
curl -s http://localhost:9100/v1/info
```

`/healthz` returns `ok`. `/v1/info` returns JSON like:

```json
{"device_id":"mac-01","hostname":"mac-01","os":"darwin","arch":"arm64","version":"0.1.0","interval_seconds":1,"buffer_seconds":300,"sensors":["cpu_usage","memory_usage","disk_usage","disk_busy","net_in","net_out"]}
```

The exact sensor list depends on the host; see [docs/sensors.md](../sensors.md). Stop the foreground process with `Ctrl+C`.

## Install as a launchd daemon

Install the binary:

```sh
sudo install -m 0755 "$BIN" /usr/local/bin/fleetpulse-exporter
```

Install the LaunchDaemon. This is the same file as `deploy/exporter/com.fleetpulse.exporter.plist` in the repository:

```sh
sudo tee /Library/LaunchDaemons/com.fleetpulse.exporter.plist >/dev/null <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.fleetpulse.exporter</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/fleetpulse-exporter</string>
        <string>--listen</string>
        <string>:9100</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ProcessType</key>
    <string>Background</string>
    <key>ThrottleInterval</key>
    <integer>10</integer>
    <key>StandardOutPath</key>
    <string>/var/log/fleetpulse-exporter.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/fleetpulse-exporter.log</string>
</dict>
</plist>
EOF
```

Fix ownership and load it:

```sh
sudo chown root:wheel /Library/LaunchDaemons/com.fleetpulse.exporter.plist
sudo chmod 0644 /Library/LaunchDaemons/com.fleetpulse.exporter.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/com.fleetpulse.exporter.plist
sudo launchctl enable system/com.fleetpulse.exporter
```

Verify the daemon and the API:

```sh
sudo launchctl print system/com.fleetpulse.exporter | head -40
curl -s http://localhost:9100/healthz
curl -s http://localhost:9100/v1/info
```

Follow the log:

```sh
sudo tail -f /var/log/fleetpulse-exporter.log
```

The LaunchDaemon runs as `root` and `KeepAlive` restarts the exporter whenever it exits. The exporter needs no writable files; it buffers samples in memory. If you prefer an unprivileged account, add `UserName` and `GroupName` keys to the plist pointing at an existing user that can read the host sensors, then reload the daemon.

## Open the firewall

Check the application firewall state first:

```sh
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --getglobalstate
```

If it is enabled, allow the exporter to accept incoming connections:

```sh
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add /usr/local/bin/fleetpulse-exporter
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp /usr/local/bin/fleetpulse-exporter
```

If the firewall is disabled, there is nothing to open. Restrict access to your scraper network with your network firewall or a packet filter if the host is exposed beyond a trusted network.

## Add the host to the scraper

On the scraper host, add this Mac's URL to `/etc/fleetpulse/scraper.yaml` (the full format is in [contract 05](../../contracts/05-repo-layout-and-runtime.md)):

```yaml
targets:
  - url: http://192.0.2.20:9100
```

Replace `192.0.2.20` with this Mac's address. Restart your scraper service after editing its config; for example:

```sh
sudo systemctl restart fleetpulse-scraper
```

Adjust the command to how the scraper runs in your environment.

## Upgrade

Install the new binary over the old one and restart the daemon:

```sh
sudo install -m 0755 "$BIN" /usr/local/bin/fleetpulse-exporter
sudo launchctl kickstart -k system/com.fleetpulse.exporter
curl -s http://localhost:9100/v1/info
```

## Uninstall

```sh
sudo launchctl bootout system/com.fleetpulse.exporter 2>/dev/null || true
sudo rm /Library/LaunchDaemons/com.fleetpulse.exporter.plist
sudo rm /usr/local/bin/fleetpulse-exporter
sudo rm -f /var/log/fleetpulse-exporter.log
```

## Troubleshooting

### `launchctl print` says the service was not found

The plist is not loaded or has a syntax error. Validate it and load it again:

```sh
plutil -lint /Library/LaunchDaemons/com.fleetpulse.exporter.plist
sudo launchctl bootstrap system /Library/LaunchDaemons/com.fleetpulse.exporter.plist
sudo launchctl print system/com.fleetpulse.exporter
```

### The daemon starts and immediately exits

Read the log:

```sh
sudo tail -50 /var/log/fleetpulse-exporter.log
```

A `bind: address already in use` error means another process owns port 9100:

```sh
sudo lsof -nP -iTCP:9100 -sTCP:LISTEN
```

Stop that process or change the `--listen` argument in the plist, then reload:

```sh
sudo launchctl bootout system/com.fleetpulse.exporter 2>/dev/null || true
sudo launchctl bootstrap system /Library/LaunchDaemons/com.fleetpulse.exporter.plist
```

### macOS refuses to run the downloaded binary

Gatekeeper quarantines files downloaded by browsers. Remove the quarantine attribute:

```sh
sudo xattr -d com.apple.quarantine /usr/local/bin/fleetpulse-exporter 2>/dev/null || true
```

If macOS still blocks it, open System Settings, go to Privacy & Security, and allow the binary. Binaries built locally with `go build` are not quarantined.

### A remote host cannot connect

The exporter binds all interfaces with `--listen :9100`. Check the application firewall, any network firewall, and that the host is reachable:

```sh
curl -v http://192.0.2.20:9100/healthz
```

### `fan_speed` is missing

That is expected on macOS. Read [docs/sensors.md](../sensors.md) for the per-platform sensor matrix.

### Timestamps look wrong

The exporter reports UTC Unix seconds. Check the host clock:

```sh
sudo sntp -sS time.apple.com
date -u
```

### Show the version and flags

```sh
/usr/local/bin/fleetpulse-exporter --version
/usr/local/bin/fleetpulse-exporter --help
```
