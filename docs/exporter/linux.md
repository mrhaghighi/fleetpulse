# Install the exporter on Linux

The FleetPulse exporter is a single static binary. It samples this host and serves the exporter API on TCP 9100. This guide covers 64-bit x86 and ARM Linux and assumes a systemd distribution.

## Get the binary

### Build from a checkout

Run these commands from the repository root:

```sh
VERSION=0.1.0
case "$(uname -m)" in
  x86_64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)"; exit 1 ;;
esac
BIN="fleetpulse-exporter_${VERSION}_linux_${ARCH}"
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath -ldflags "-s -w -X fleetpulse/internal/version.Version=${VERSION}" -o "$BIN" ./cmd/exporter
```

`make dist` builds all five release targets and writes checksums:

```sh
make dist
cat dist/SHA256SUMS
```

### Use a prebuilt artifact

Download `fleetpulse-exporter_<version>_linux_<arch>` from the release page into the current directory. Then set the same three variables so the rest of this guide can be pasted unchanged:

```sh
VERSION=0.1.0
ARCH=amd64
BIN="fleetpulse-exporter_${VERSION}_linux_${ARCH}"
```

Use `ARCH=arm64` on 64-bit ARM. Confirm the artifact:

```sh
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
{"device_id":"web-01","hostname":"web-01","os":"linux","arch":"amd64","version":"0.1.0","interval_seconds":1,"buffer_seconds":300,"sensors":["cpu_usage","memory_usage","disk_usage","disk_busy","net_in","net_out","fan_speed"]}
```

`fan_speed` is omitted on hosts without readable hwmon fans. That is normal; see [docs/sensors.md](../sensors.md). Stop the foreground process with `Ctrl+C`.

## Install as a systemd service

Create a dedicated system user:

```sh
id -u fleetpulse >/dev/null 2>&1 || sudo useradd --system --user-group --no-create-home --shell "$(command -v nologin)" fleetpulse
```

Install the binary:

```sh
sudo install -m 0755 "$BIN" /usr/local/bin/fleetpulse-exporter
```

Install the unit. This is the same file as `deploy/exporter/fleetpulse-exporter.service` in the repository:

```sh
sudo tee /etc/systemd/system/fleetpulse-exporter.service >/dev/null <<'EOF'
[Unit]
Description=FleetPulse exporter
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=fleetpulse
Group=fleetpulse
ExecStart=/usr/local/bin/fleetpulse-exporter --listen :9100
Restart=always
RestartSec=5s
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK
LockPersonality=true
CapabilityBoundingSet=
SyslogIdentifier=fleetpulse-exporter

[Install]
WantedBy=multi-user.target
EOF
```

Start it and enable it at boot:

```sh
sudo systemctl daemon-reload
sudo systemctl enable --now fleetpulse-exporter
sudo systemctl status fleetpulse-exporter --no-pager
```

Verify the API:

```sh
curl -s http://localhost:9100/healthz
curl -s http://localhost:9100/v1/info
```

Follow the logs:

```sh
sudo journalctl -u fleetpulse-exporter -f
```

The unit runs as `fleetpulse`, drops all capabilities, makes the system read-only, and restarts the exporter whenever it exits. The exporter keeps its sample buffer in memory and writes no files, so it needs no writable paths.

## Open the firewall

Allow the scraper to reach TCP 9100. With ufw:

```sh
sudo ufw allow 9100/tcp
sudo ufw status
```

With firewalld:

```sh
sudo firewall-cmd --permanent --add-port=9100/tcp
sudo firewall-cmd --reload
sudo firewall-cmd --list-ports
```

Prefer restricting the source to your scraper network instead of opening the port to the world. With ufw:

```sh
sudo ufw allow from 10.0.0.0/8 to any port 9100 proto tcp
```

With firewalld:

```sh
sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="10.0.0.0/8" port port="9100" protocol="tcp" accept'
sudo firewall-cmd --reload
```

## Add the host to the scraper

On the scraper host, add this host's URL to `/etc/fleetpulse/scraper.yaml` (the full format is in [contract 05](../../contracts/05-repo-layout-and-runtime.md)):

```yaml
targets:
  - url: http://192.0.2.10:9100
```

Replace `192.0.2.10` with this host's address. The scraper pulls each static target every `interval` and keeps per-device cursors, so no change is needed on the exporter side. Restart your scraper service after editing its config; for example:

```sh
sudo systemctl restart fleetpulse-scraper
```

Adjust the unit name if your deployment uses a different one.

## Upgrade

Install the new binary over the old one and restart:

```sh
sudo install -m 0755 "$BIN" /usr/local/bin/fleetpulse-exporter
sudo systemctl restart fleetpulse-exporter
curl -s http://localhost:9100/v1/info
```

## Uninstall

```sh
sudo systemctl disable --now fleetpulse-exporter
sudo rm /etc/systemd/system/fleetpulse-exporter.service
sudo systemctl daemon-reload
sudo rm /usr/local/bin/fleetpulse-exporter
sudo userdel fleetpulse
sudo groupdel fleetpulse
sudo ufw delete allow 9100/tcp
```

If you added the source-restricted ufw rule instead, remove it with:

```sh
sudo ufw delete allow from 10.0.0.0/8 to any port 9100 proto tcp
```

If you added the firewalld port, remove it with:

```sh
sudo firewall-cmd --permanent --remove-port=9100/tcp
sudo firewall-cmd --reload
```

## Troubleshooting

### The service does not start

```sh
sudo systemctl status fleetpulse-exporter --no-pager
sudo journalctl -u fleetpulse-exporter -n 50 --no-pager
```

`status=203/EXEC` means systemd cannot execute `/usr/local/bin/fleetpulse-exporter`. Reinstall the binary and check its permissions:

```sh
sudo install -m 0755 "$BIN" /usr/local/bin/fleetpulse-exporter
ls -l /usr/local/bin/fleetpulse-exporter
```

Run the exporter as the service user to see the real error in the foreground:

```sh
sudo -u fleetpulse /usr/local/bin/fleetpulse-exporter --listen :9100
```

### `curl` says connection refused

Check that the process is listening and that the unit is active:

```sh
sudo ss -ltnp | grep ':9100'
sudo systemctl is-active fleetpulse-exporter
```

If another process owns port 9100, stop it or change `--listen` in the unit and reload:

```sh
sudo systemctl edit --full fleetpulse-exporter
sudo systemctl daemon-reload
sudo systemctl restart fleetpulse-exporter
```

### A remote host cannot connect

The exporter binds all interfaces with `--listen :9100`. Check the host firewall and any cloud security group. Test from the scraper host:

```sh
curl -v http://192.0.2.10:9100/healthz
```

### `fan_speed` is missing

Read [docs/sensors.md](../sensors.md). Virtual machines, containers, and many laptops do not expose `fan*_input` in sysfs. The sensor is omitted rather than reported as a fake zero.

### Disk usage fails or shows the wrong filesystem

The default `--disk-path` is `/`. The unit sets `ProtectHome=true`, which hides `/home`, `/root`, and `/run/user`. If you monitor a path under those directories, remove `ProtectHome=true` from the unit, then reload and restart:

```sh
sudo systemctl edit --full fleetpulse-exporter
sudo systemctl daemon-reload
sudo systemctl restart fleetpulse-exporter
```

To monitor a different filesystem, change the `ExecStart` line to include `--disk-path /data`, then reload and restart.

### SELinux blocks the binary

If you install the binary somewhere other than `/usr/local/bin`, restore its SELinux label:

```sh
sudo restorecon -v /usr/local/bin/fleetpulse-exporter
```

Check for recent denials:

```sh
sudo ausearch -m avc -ts recent
```

### Timestamps look wrong

The exporter reports UTC Unix seconds. Check the host clock:

```sh
timedatectl status
```

### Show the version and flags

```sh
/usr/local/bin/fleetpulse-exporter --version
/usr/local/bin/fleetpulse-exporter --help
```
