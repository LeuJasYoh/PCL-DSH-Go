#requires -Version 7.0
<#
  Start/stop the disposable M4 E2E stack: Go manager (WSL) serving built web/dist.
  Data root: /tmp/pcl-dsh-go-test/m4-data (fresh on start). Playwright runs on
  Windows against http://127.0.0.1:18080 and reads .e2e-data/e2e-initial-password.txt.
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('start', 'stop', 'restart', 'status')]
    [string]$Action = 'start',
    [switch]$KeepData,
    [int]$ManagerPort = 18080,
    [string]$Distro = 'Ubuntu',
    [string]$DataRootWsl = '/tmp/pcl-dsh-go-test/m4-data'
)

$ErrorActionPreference = 'Stop'
$nl = [Environment]::NewLine
$WebRoot = Split-Path -Parent $PSScriptRoot
$RepoRoot = Split-Path -Parent $WebRoot
$DataDir = Join-Path $WebRoot '.e2e-data'
$StateFile = Join-Path $DataDir 'e2e-stack.json'
$InitialPasswordCopy = Join-Path $DataDir 'e2e-initial-password.txt'
$HealthUrl = 'http://127.0.0.1:' + $ManagerPort + '/api/health'
$StartScriptWin = Join-Path $DataDir 'start-manager.sh'

function ConvertTo-WslPath([string]$p) {
    $full = [System.IO.Path]::GetFullPath($p)
    if ($full -match '^([A-Za-z]):\\(.*)$') {
        return '/mnt/' + $Matches[1].ToLowerInvariant() + '/' + $Matches[2].Replace('\', '/')
    }
    throw "Path is not on a Windows drive: $full"
}

$RepoWsl = ConvertTo-WslPath $RepoRoot
$DataDirWsl = ConvertTo-WslPath $DataDir
$DistWsl = ConvertTo-WslPath (Join-Path $WebRoot 'dist')
$StartScriptWsl = ConvertTo-WslPath $StartScriptWin
$PidFileWsl = $DataDirWsl + '/manager.pid'
$LogWsl = $DataDirWsl + '/logs/manager.log'

function Write-Lf([string]$Path, [string]$Content) {
    $cr = [string]([char]13)
    [System.IO.File]::WriteAllText($Path, $Content.Replace($cr, ''), [System.Text.UTF8Encoding]::new($false))
}

function State { if (Test-Path $StateFile) { try { return Get-Content $StateFile -Raw | ConvertFrom-Json } catch { return $null } } return $null }

function Healthy {
    try { return ((Invoke-WebRequest -UseBasicParsing -Uri $HealthUrl -TimeoutSec 3).StatusCode -eq 200) } catch { return $false }
}

function Alive([int]$ProcessId) {
    if (-not $ProcessId) { return $false }
    $null = & wsl.exe -d $Distro -- bash -c "kill -0 $ProcessId 2>/dev/null"
    return ($LASTEXITCODE -eq 0)
}

function Stop-Stack {
    $s = State
    if ($s -and $s.managerPid) {
        Write-Lf $StartScriptWin ('#!/usr/bin/env bash' + $nl + 'kill -TERM -- -' + $s.managerPid + ' 2>/dev/null || kill -TERM ' + $s.managerPid + ' 2>/dev/null || true' + $nl)
        & wsl.exe -d $Distro -- bash $StartScriptWsl | Out-Null
        Start-Sleep -Milliseconds 800
        if (Alive ([int]$s.managerPid)) {
            Write-Lf $StartScriptWin ('#!/usr/bin/env bash' + $nl + 'kill -KILL -- -' + $s.managerPid + ' 2>/dev/null || true' + $nl)
            & wsl.exe -d $Distro -- bash $StartScriptWsl | Out-Null
        }
    }
    Remove-Item $StateFile -Force -ErrorAction SilentlyContinue
    if (-not $KeepData) {
        Write-Lf $StartScriptWin ('#!/usr/bin/env bash' + $nl + "rm -rf '$DataRootWsl'" + $nl)
        & wsl.exe -d $Distro -- bash $StartScriptWsl | Out-Null
    }
    Remove-Item $StartScriptWin -Force -ErrorAction SilentlyContinue
    Write-Output 'E2E stack stopped.'
}

function Start-Stack {
    if (-not (Test-Path (Join-Path $WebRoot 'dist\index.html'))) {
        throw 'web/dist/index.html not found. Build first: cd web; npm ci; npm run type-check; npm run build'
    }
    $null = & wsl.exe -d $Distro -- bash -c "test -x '$RepoWsl/bin/pcl-dsh-go'"
    if ($LASTEXITCODE -ne 0) { throw "Go manager not found in WSL: $RepoWsl/bin/pcl-dsh-go" }
    if (State) { Stop-Stack }
    $listener = Get-NetTCPConnection -State Listen -LocalPort $ManagerPort -ErrorAction SilentlyContinue
    if ($listener) { throw "Port $ManagerPort is already in use; stop the other process first." }
    New-Item -ItemType Directory -Force -Path (Join-Path $DataDir 'logs') | Out-Null
    Write-Lf $StartScriptWin ('#!/usr/bin/env bash' + $nl + "rm -rf '$DataRootWsl'" + $nl)
    & wsl.exe -d $Distro -- bash $StartScriptWsl | Out-Null

    $body = @'
#!/usr/bin/env bash
set -euo pipefail
cd "__REPO__"
mkdir -p "__LOGDIR__" "__DATA_ROOT__"
setsid ./bin/pcl-dsh-go \
  --pcl.data-root="__DATA_ROOT__" \
  --pcl.tool-root=/opt/pcl-dsh-go/tools \
  --server.address=127.0.0.1 \
  --server.port=__PORT__ \
  --server.static-dir="__DIST__" \
  >> "__LOG__" 2>&1 &
echo $! > "__PIDFILE__"
'@
    Write-Lf $StartScriptWin $body.Replace('__REPO__', $RepoWsl).Replace('__LOGDIR__', $DataDirWsl + '/logs').Replace('__DATA_ROOT__', $DataRootWsl).Replace('__PORT__', [string]$ManagerPort).Replace('__DIST__', $DistWsl).Replace('__LOG__', $LogWsl).Replace('__PIDFILE__', $PidFileWsl)
    & wsl.exe -d $Distro -- bash $StartScriptWsl | Out-Null

    $deadline = (Get-Date).AddSeconds(120)
    while ((Get-Date) -lt $deadline -and -not (Healthy)) { Start-Sleep -Milliseconds 300 }
    if (-not (Healthy)) {
        $tail = (& wsl.exe -d $Distro -- bash -c "tail -n 40 '$LogWsl' 2>/dev/null") -join $nl
        throw ('Go manager did not become healthy on ' + $HealthUrl + '. ' + $tail)
    }
    $initial = $null
    $deadline = (Get-Date).AddSeconds(30)
    while ((Get-Date) -lt $deadline -and -not $initial) {
        $value = (& wsl.exe -d $Distro -- bash -c "cat '$DataRootWsl/initial-password' 2>/dev/null") -join ''
        if ($value.Trim()) { $initial = $value.Trim() } else { Start-Sleep -Milliseconds 250 }
    }
    if (-not $initial) { throw "initial-password not found under $DataRootWsl" }
    Set-Content -Path $InitialPasswordCopy -Value $initial -NoNewline
    $pidText = ((& wsl.exe -d $Distro -- bash -c "cat '$PidFileWsl' 2>/dev/null") -join '').Trim()
    $managerPid = 0
    [void][int]::TryParse($pidText, [ref]$managerPid)
    [ordered]@{
        managerPid = $managerPid
        managerPort = $ManagerPort
        dataRootWsl = $DataRootWsl
        managerLogWsl = $LogWsl
        distro = $Distro
        startedAt = (Get-Date).ToString('o')
    } | ConvertTo-Json | Set-Content -Path $StateFile -Encoding utf8
    Write-Output ('E2E stack is ready: http://127.0.0.1:' + $ManagerPort + ' (pid ' + $managerPid + ')')
    Write-Output ('  data root: ' + $DataRootWsl)
    Write-Output ('  log: ' + $LogWsl)
}

function Show-Status {
    $s = State
    if (-not $s) { Write-Output 'E2E stack is not running.'; return }
    Write-Output ('manager pid=' + $s.managerPid + ' alive=' + (Alive ([int]$s.managerPid)))
    Write-Output ('health: ' + $(if (Healthy) { 'ok' } else { 'down' }))
    Write-Output ('data root: ' + $s.dataRootWsl)
}

switch ($Action) {
    'start' { Start-Stack }
    'restart' { Stop-Stack; Start-Stack }
    'stop' { Stop-Stack }
    'status' { Show-Status }
}
