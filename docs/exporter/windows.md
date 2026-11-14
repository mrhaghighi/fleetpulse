# Install the exporter on Windows

> Best-effort support: see docs/sensors.md for which sensors are available

The exporter is a single static executable. On Windows it samples CPU, memory, disk, and network; `fan_speed` is omitted because fan data is only available through model-specific WMI classes.

**This guide and `deploy/exporter/install-windows.ps1` are untested on Windows.** Treat them as a best-effort recipe and verify the service, logs, and firewall rule before relying on them in production.

A plain console executable is **not** a native Windows service. The Service Control Manager expects a program that talks the service control protocol, and `fleetpulse-exporter.exe` does not. This guide uses [NSSM](https://nssm.cc/) (the Non-Sucking Service Manager) as a wrapper: Windows runs `nssm.exe` as the service, and NSSM runs the exporter as a child process. The older `srvany.exe` wrapper from the Windows Server 2003 Resource Kit is not recommended on current Windows.

## Get the binary

### Build from a checkout

Run these commands from the repository root in PowerShell:

```powershell
$Version = '0.1.0'
$Binary = "fleetpulse-exporter_${Version}_windows_amd64.exe"
go build -trimpath -ldflags "-s -w -X fleetpulse/internal/version.Version=$Version" -o $Binary .\cmd\exporter
& ".\$Binary" --version
```

The output is `exporter 0.1.0` (or the version you built). If you build on Linux or macOS, `make dist` produces `dist/fleetpulse-exporter_0.1.0_windows_amd64.exe`; copy that file to the Windows host and rename it to `$Binary` in the current directory.

### Use a prebuilt artifact

Download `fleetpulse-exporter_<version>_windows_amd64.exe` from the release page into the current directory, then set the variables so the rest of the guide can be pasted unchanged:

```powershell
$Version = '0.1.0'
$Binary = "fleetpulse-exporter_${Version}_windows_amd64.exe"
& ".\$Binary" --version
```

## Run it in the foreground

Open an elevated PowerShell window in the directory that contains the binary:

```powershell
& ".\$Binary" --listen :9100
```

Leave it running and verify from a second PowerShell window:

```powershell
curl.exe -s http://localhost:9100/healthz
curl.exe -s http://localhost:9100/v1/info
```

`/healthz` returns `ok`. `/v1/info` returns JSON like:

```json
{"device_id":"win-01","hostname":"win-01","os":"windows","arch":"amd64","version":"0.1.0","interval_seconds":1,"buffer_seconds":300,"sensors":["cpu_usage","memory_usage","disk_usage","disk_busy","net_in","net_out"]}
```

Stop the foreground process with `Ctrl+C`.

## Install NSSM

Download and extract NSSM:

```powershell
$NssmZip = Join-Path $env:TEMP 'nssm-2.24.zip'
Invoke-WebRequest -Uri 'https://nssm.cc/release/nssm-2.24.zip' -OutFile $NssmZip
Expand-Archive -Path $NssmZip -DestinationPath (Join-Path $env:TEMP 'nssm-2.24') -Force
New-Item -ItemType Directory -Force -Path 'C:\Program Files\NSSM' | Out-Null
Copy-Item (Join-Path $env:TEMP 'nssm-2.24\win64\nssm.exe') 'C:\Program Files\NSSM\nssm.exe' -Force
```

Check that NSSM is present:

```powershell
& 'C:\Program Files\NSSM\nssm.exe' version
```

## Install as a service

### Use the provided script

From an elevated PowerShell window in the repository root:

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
.\deploy\exporter\install-windows.ps1 -BinaryPath ".\$Binary" -OpenFirewall
```

The script copies the binary to `C:\Program Files\FleetPulse`, creates the `FleetPulseExporter` service with `New-Service`, configures NSSM through the service's registry parameters, starts the service, and optionally opens the firewall. It writes exporter output to `C:\ProgramData\FleetPulse\exporter.log`. The script is untested; read it before running it.

### Manual equivalent

If you prefer to see every step, run these commands from an elevated PowerShell window in the directory that contains the binary:

```powershell
New-Item -ItemType Directory -Force -Path 'C:\Program Files\FleetPulse' | Out-Null
New-Item -ItemType Directory -Force -Path 'C:\ProgramData\FleetPulse' | Out-Null
Copy-Item ".\$Binary" 'C:\Program Files\FleetPulse\fleetpulse-exporter.exe' -Force
New-Service -Name FleetPulseExporter -BinaryPathName '"C:\Program Files\NSSM\nssm.exe"' -DisplayName 'FleetPulse exporter' -StartupType Automatic
sc.exe description FleetPulseExporter 'Samples host sensors and serves the FleetPulse exporter API.'
$key = 'HKLM:\SYSTEM\CurrentControlSet\Services\FleetPulseExporter\Parameters'
New-Item -Path $key -Force | Out-Null
New-ItemProperty -Path $key -Name Application -Value 'C:\Program Files\FleetPulse\fleetpulse-exporter.exe' -PropertyType String -Force | Out-Null
New-ItemProperty -Path $key -Name AppParameters -Value '--listen :9100' -PropertyType String -Force | Out-Null
New-ItemProperty -Path $key -Name AppDirectory -Value 'C:\Program Files\FleetPulse' -PropertyType String -Force | Out-Null
New-ItemProperty -Path $key -Name AppStdout -Value 'C:\ProgramData\FleetPulse\exporter.log' -PropertyType String -Force | Out-Null
New-ItemProperty -Path $key -Name AppStderr -Value 'C:\ProgramData\FleetPulse\exporter.log' -PropertyType String -Force | Out-Null
New-ItemProperty -Path $key -Name AppRotateFiles -Value 1 -PropertyType DWord -Force | Out-Null
New-ItemProperty -Path $key -Name AppRotateBytes -Value 10485760 -PropertyType DWord -Force | Out-Null
Start-Service FleetPulseExporter
```

Verify the service and the API:

```powershell
Get-Service FleetPulseExporter
curl.exe -s http://localhost:9100/healthz
curl.exe -s http://localhost:9100/v1/info
Get-Content 'C:\ProgramData\FleetPulse\exporter.log' -Tail 20
```

The service runs as `LocalSystem` and starts automatically at boot. NSSM restarts the exporter if it exits.

## Open the firewall

If you did not pass `-OpenFirewall` to the script, create the inbound rule:

```powershell
New-NetFirewallRule -DisplayName 'FleetPulse exporter' -Direction Inbound -Action Allow -Protocol TCP -LocalPort 9100 -Program 'C:\Program Files\FleetPulse\fleetpulse-exporter.exe'
```

Check it:

```powershell
Get-NetFirewallRule -DisplayName 'FleetPulse exporter' | Format-List
```

Prefer restricting the rule to your scraper network with `-RemoteAddress`:

```powershell
New-NetFirewallRule -DisplayName 'FleetPulse exporter' -Direction Inbound -Action Allow -Protocol TCP -LocalPort 9100 -RemoteAddress 10.0.0.0/8 -Program 'C:\Program Files\FleetPulse\fleetpulse-exporter.exe'
```

## Add the host to the scraper

On the scraper host, add this Windows host's URL to `/etc/fleetpulse/scraper.yaml` (the full format is in [contract 05](../../contracts/05-repo-layout-and-runtime.md)):

```yaml
targets:
  - url: http://192.0.2.30:9100
```

Replace `192.0.2.30` with this host's address. Restart your scraper service after editing its config; for example:

```sh
sudo systemctl restart fleetpulse-scraper
```

Adjust the command to how the scraper runs in your environment.

## Upgrade

Stop the service, replace the binary, and start it again:

```powershell
Stop-Service FleetPulseExporter -Force
Copy-Item ".\$Binary" 'C:\Program Files\FleetPulse\fleetpulse-exporter.exe' -Force
Start-Service FleetPulseExporter
curl.exe -s http://localhost:9100/v1/info
```

## Uninstall

```powershell
Stop-Service FleetPulseExporter -Force -ErrorAction SilentlyContinue
sc.exe delete FleetPulseExporter
Remove-NetFirewallRule -DisplayName 'FleetPulse exporter' -ErrorAction SilentlyContinue
Remove-Item -Recurse -Force 'C:\Program Files\FleetPulse'
Remove-Item -Recurse -Force 'C:\ProgramData\FleetPulse'
```

If you also want to remove NSSM:

```powershell
Remove-Item -Recurse -Force 'C:\Program Files\NSSM'
```

## Troubleshooting

### The service does not start

Check its status and configuration:

```powershell
Get-Service FleetPulseExporter
sc.exe qc FleetPulseExporter
Get-WinEvent -LogName System -MaxEvents 50 | Where-Object ProviderName -eq 'Service Control Manager'
```

Read the NSSM-captured output:

```powershell
Get-Content 'C:\ProgramData\FleetPulse\exporter.log' -Tail 50
```

If the log is empty, run the binary in the foreground as the same account to see the error:

```powershell
& 'C:\Program Files\FleetPulse\fleetpulse-exporter.exe' --listen :9100
```

### `curl.exe` says connection refused

Check that the exporter is listening and that nothing else owns the port:

```powershell
Get-NetTCPConnection -LocalPort 9100 -ErrorAction SilentlyContinue
netstat -ano | findstr :9100
```

If another process owns port 9100, stop it or change `AppParameters` to a different `--listen` value, then restart the service:

```powershell
Stop-Service FleetPulseExporter -Force
Set-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Services\FleetPulseExporter\Parameters' -Name AppParameters -Value '--listen :9101'
Start-Service FleetPulseExporter
```

Remember to update the firewall rule and the scraper URL when you change the port.

### A remote host cannot connect

The exporter binds all interfaces with `--listen :9100`. Check the firewall rule, any network firewall, and reachability from the scraper host:

```powershell
Get-NetFirewallRule -DisplayName 'FleetPulse exporter' | Format-List
```

```sh
curl -v http://192.0.2.30:9100/healthz
```

### The service keeps restarting

NSSM restarts the child process when it exits. Look for a bind error or a missing binary in the log:

```powershell
Get-Content 'C:\ProgramData\FleetPulse\exporter.log' -Tail 100
```

Also confirm that the binary path in the service parameters is correct:

```powershell
Get-ItemProperty -Path 'HKLM:\SYSTEM\CurrentControlSet\Services\FleetPulseExporter\Parameters'
```

### `fan_speed` is missing

That is expected on Windows. Read [docs/sensors.md](../sensors.md) for the per-platform sensor matrix.

### Timestamps look wrong

The exporter reports UTC Unix seconds. Check the Windows time service:

```powershell
w32tm /query /status
```

### Show the version and flags

```powershell
& 'C:\Program Files\FleetPulse\fleetpulse-exporter.exe' --version
& 'C:\Program Files\FleetPulse\fleetpulse-exporter.exe' --help
```
