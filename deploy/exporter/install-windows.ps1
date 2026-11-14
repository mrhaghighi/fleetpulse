#Requires -RunAsAdministrator
<#
.SYNOPSIS
Installs the FleetPulse exporter as a Windows service through NSSM.

.DESCRIPTION
UNTESTED ON WINDOWS. A plain console executable is not a native Windows
service: the Service Control Manager expects a program that speaks the service
control protocol, and fleetpulse-exporter.exe does not. This script therefore
registers NSSM (the Non-Sucking Service Manager) as the service binary and
configures NSSM to run fleetpulse-exporter.exe as its child process.

The service itself is created with the built-in New-Service cmdlet. NSSM reads
its settings from HKLM\SYSTEM\CurrentControlSet\Services\<ServiceName>\Parameters,
so the script writes Application, AppParameters, AppDirectory, and log/restart
values there.

Install NSSM first (https://nssm.cc/download) and pass -NssmPath when nssm.exe
is not at C:\Program Files\NSSM\nssm.exe.

.PARAMETER BinaryPath
The fleetpulse-exporter.exe to install. Defaults to a file next to this script.

.PARAMETER InstallDir
Where the binary is copied. Defaults to C:\Program Files\FleetPulse.

.PARAMETER ServiceName
Windows service name. Defaults to FleetPulseExporter.

.PARAMETER Listen
Value passed to --listen. Defaults to :9100.

.PARAMETER NssmPath
Path to nssm.exe. Defaults to C:\Program Files\NSSM\nssm.exe.

.PARAMETER LogDir
Directory for NSSM's captured stdout/stderr. Defaults to C:\ProgramData\FleetPulse.

.PARAMETER OpenFirewall
Create an inbound Windows Firewall rule for the listen port.

.PARAMETER Uninstall
Stop and delete the service and its firewall rule. Installed files are kept.

.EXAMPLE
.\install-windows.ps1 -BinaryPath .\fleetpulse-exporter_0.1.0_windows_amd64.exe -OpenFirewall

.EXAMPLE
.\install-windows.ps1 -Uninstall
#>
[CmdletBinding()]
param(
    [string]$BinaryPath = (Join-Path $PSScriptRoot 'fleetpulse-exporter.exe'),
    [string]$InstallDir = 'C:\Program Files\FleetPulse',
    [string]$ServiceName = 'FleetPulseExporter',
    [string]$Listen = ':9100',
    [string]$NssmPath = 'C:\Program Files\NSSM\nssm.exe',
    [string]$LogDir = 'C:\ProgramData\FleetPulse',
    [switch]$OpenFirewall,
    [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'
$firewallRule = 'FleetPulse exporter'
$installedExe = Join-Path $InstallDir 'fleetpulse-exporter.exe'
$logFile = Join-Path $LogDir 'exporter.log'

$currentIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
$currentPrincipal = New-Object -TypeName Security.Principal.WindowsPrincipal -ArgumentList $currentIdentity
if (-not $currentPrincipal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this script from an elevated PowerShell window (Run as Administrator).'
}

Write-Warning 'This script is UNTESTED on Windows. Verify the service, logs, and firewall rule before relying on it in production.'

function Remove-FleetPulseService {
    param([string]$Name)

    if (-not (Get-Service -Name $Name -ErrorAction SilentlyContinue)) {
        return
    }

    Stop-Service -Name $Name -Force -ErrorAction SilentlyContinue
    for ($attempt = 1; $attempt -le 5; $attempt++) {
        & sc.exe delete $Name | Out-Null
        if ($LASTEXITCODE -eq 0) {
            return
        }
        Start-Sleep -Seconds 2
    }

    throw "Could not delete service $Name. Check sc.exe output and retry after a reboot."
}

if ($Uninstall) {
    Remove-FleetPulseService -Name $ServiceName
    if (Get-NetFirewallRule -DisplayName $firewallRule -ErrorAction SilentlyContinue) {
        Remove-NetFirewallRule -DisplayName $firewallRule
    }
    Write-Host "Removed service $ServiceName and firewall rule '$firewallRule'."
    Write-Host "Installed files in $InstallDir were left in place."
    return
}

if (-not (Test-Path -LiteralPath $BinaryPath -PathType Leaf)) {
    throw "Exporter binary not found: $BinaryPath. Pass -BinaryPath with the downloaded or built fleetpulse-exporter.exe."
}

if (-not (Test-Path -LiteralPath $NssmPath -PathType Leaf)) {
    throw "NSSM not found: $NssmPath. Download it from https://nssm.cc/download, extract win64\nssm.exe, and pass -NssmPath."
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
Copy-Item -LiteralPath $BinaryPath -Destination $installedExe -Force

Remove-FleetPulseService -Name $ServiceName

$serviceBinary = '"{0}"' -f $NssmPath
New-Service -Name $ServiceName `
    -BinaryPathName $serviceBinary `
    -DisplayName 'FleetPulse exporter' `
    -StartupType Automatic | Out-Null
& sc.exe description $ServiceName 'Samples host sensors and serves the FleetPulse exporter API.' | Out-Null

$parametersKey = "HKLM:\SYSTEM\CurrentControlSet\Services\$ServiceName\Parameters"
New-Item -Path $parametersKey -Force | Out-Null
New-ItemProperty -Path $parametersKey -Name Application -Value $installedExe -PropertyType String -Force | Out-Null
New-ItemProperty -Path $parametersKey -Name AppParameters -Value "--listen $Listen" -PropertyType String -Force | Out-Null
New-ItemProperty -Path $parametersKey -Name AppDirectory -Value $InstallDir -PropertyType String -Force | Out-Null
New-ItemProperty -Path $parametersKey -Name AppStdout -Value $logFile -PropertyType String -Force | Out-Null
New-ItemProperty -Path $parametersKey -Name AppStderr -Value $logFile -PropertyType String -Force | Out-Null
New-ItemProperty -Path $parametersKey -Name AppRotateFiles -Value 1 -PropertyType DWord -Force | Out-Null
New-ItemProperty -Path $parametersKey -Name AppRotateBytes -Value 10485760 -PropertyType DWord -Force | Out-Null
New-ItemProperty -Path $parametersKey -Name AppRestartDelay -Value 5000 -PropertyType DWord -Force | Out-Null

Start-Service -Name $ServiceName
Start-Sleep -Seconds 2

$service = Get-Service -Name $ServiceName
if ($service.Status -ne 'Running') {
    throw "Service $ServiceName is $($service.Status), not Running. Check $logFile and Get-WinEvent -LogName System."
}

$port = ($Listen -split ':')[-1]
if (-not $port) {
    $port = '9100'
}

if ($OpenFirewall) {
    if (-not (Get-NetFirewallRule -DisplayName $firewallRule -ErrorAction SilentlyContinue)) {
        New-NetFirewallRule `
            -DisplayName $firewallRule `
            -Direction Inbound `
            -Action Allow `
            -Protocol TCP `
            -LocalPort $port `
            -Program $installedExe | Out-Null
    }
    Write-Host "Opened inbound TCP port $port for $installedExe."
}

Write-Host "Service $ServiceName is running."
Write-Host "Exporter API: http://localhost:$port/v1/info"
Write-Host "Log file: $logFile"
