#requires -Version 7.0
<#
.SYNOPSIS
  Start/stop a disposable E2E stack for the PCL-DSH-Linux web UI:
  the Spring Boot jar with a temporary data directory plus the Vite dev server.

.DESCRIPTION
  Data lives in pcl-dsh-web/.e2e-data (git-ignored) so repository data is never
  touched. The initial admin password generated on first boot is copied to
  .e2e-data/e2e-initial-password.txt (Playwright reads it before login).

.EXAMPLE
  pwsh -File scripts/e2e-stack.ps1 start
  pwsh -File scripts/e2e-stack.ps1 status
  pwsh -File scripts/e2e-stack.ps1 stop        # stops and removes .e2e-data
  pwsh -File scripts/e2e-stack.ps1 stop -KeepData
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('start', 'stop', 'restart', 'status')]
    [string]$Action = 'start',

    # Keep .e2e-data when stopping (useful for post-mortem debugging).
    [switch]$KeepData
)

$ErrorActionPreference = 'Stop'

$WebRoot = Split-Path -Parent $PSScriptRoot
$RepoRoot = Split-Path -Parent $WebRoot
$DataDir = Join-Path $WebRoot '.e2e-data'
$StateFile = Join-Path $DataDir 'e2e-stack.json'
$Jar = Join-Path $RepoRoot 'pcl-dsh-server/target/pcl-dsh-linux.jar'
$ServerPort = 8080
$WebPort = 5173
$HealthUrl = "http://127.0.0.1:$ServerPort/api/health"
$WebUrl = "http://127.0.0.1:$WebPort/"
$ServerOut = Join-Path $DataDir 'logs/server.out.log'
$ServerErr = Join-Path $DataDir 'logs/server.err.log'
$ViteOut = Join-Path $DataDir 'logs/vite.out.log'
$ViteErr = Join-Path $DataDir 'logs/vite.err.log'
$InitialPasswordCopy = Join-Path $DataDir 'e2e-initial-password.txt'

function Get-StackState {
    if (Test-Path $StateFile) {
        try {
            return Get-Content $StateFile -Raw | ConvertFrom-Json
        } catch {
            return $null
        }
    }
    return $null
}

function Test-ProcessAlive([int]$ProcessId) {
    if (-not $ProcessId) { return $false }
    return [bool](Get-Process -Id $ProcessId -ErrorAction SilentlyContinue)
}

function Stop-ProcessTree([int]$ProcessId) {
    if (-not $ProcessId) { return }
    try {
        & taskkill.exe /PID $ProcessId /T /F 2>&1 | Out-Null
    } catch {
        # Process already gone.
    }
}

function Wait-Http([string]$Url, [int]$TimeoutSeconds) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $Url -TimeoutSec 3 -ErrorAction Stop
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 500) { return $true }
        } catch {
            # Not ready yet.
        }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

function Wait-ProcessHttp([int]$ProcessId, [string]$Url, [int]$TimeoutSeconds) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if (-not (Test-ProcessAlive $ProcessId)) { return 'exited' }
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $Url -TimeoutSec 3 -ErrorAction Stop
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 500) { return 'ready' }
        } catch {
            # Not ready yet.
        }
        Start-Sleep -Milliseconds 500
    }
    return 'timeout'
}

function Read-LogTail([string]$LogPath, [int]$Lines = 25) {
    if (Test-Path $LogPath) {
        return ((Get-Content $LogPath -Tail $Lines) -join [Environment]::NewLine)
    }
    return ''
}

function Get-PortOwners([int[]]$Ports) {
    try {
        return @(Get-NetTCPConnection -State Listen -LocalPort $Ports -ErrorAction SilentlyContinue |
            Select-Object -ExpandProperty OwningProcess -Unique)
    } catch {
        return @()
    }
}

function Stop-Stack([switch]$Clean) {
    $state = Get-StackState
    if ($state) {
        Stop-ProcessTree ([int]$state.vitePid)
        Stop-ProcessTree ([int]$state.serverPid)
    }
    Remove-Item $StateFile -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 400
    if ($Clean -and (Test-Path $DataDir)) {
        Remove-Item $DataDir -Recurse -Force -ErrorAction SilentlyContinue
    }
    Write-Output 'E2E stack stopped.'
}

function Start-Stack {
    if (-not (Test-Path $Jar)) {
        throw "Backend jar not found: $Jar. Build it first with scripts/mvn.ps1 -B -ntp package."
    }

    $java = if ($env:JAVA_HOME) { Join-Path $env:JAVA_HOME 'bin/java.exe' } else { 'D:/DevelopSoftware/Java/JDK-21/bin/java.exe' }
    if (-not (Test-Path $java)) {
        throw 'java.exe not found. Set JAVA_HOME to a JDK 21 installation.'
    }
    $node = (Get-Command node -ErrorAction Stop).Source
    $viteBin = Join-Path $WebRoot 'node_modules/vite/bin/vite.js'
    if (-not (Test-Path $viteBin)) {
        throw "Vite is not installed. Run 'npm install' in $WebRoot first."
    }

    # Always start fresh so the initial-password / forced-change flow is deterministic.
    if (Get-StackState) { Stop-Stack -Clean }
    $owners = Get-PortOwners @($ServerPort, $WebPort)
    if ($owners.Count -gt 0) {
        throw "Ports $ServerPort/$WebPort are already in use (PID: $($owners -join ', ')); stop the other process first."
    }
    if (Test-Path $DataDir) { Remove-Item $DataDir -Recurse -Force }
    New-Item -ItemType Directory -Force -Path (Join-Path $DataDir 'logs') | Out-Null

    Write-Output "Starting backend (temporary data: $DataDir)..."
    $serverArgs = @("-Dpcl.data=$DataDir", '-jar', $Jar)
    $server = Start-Process -FilePath $java -ArgumentList $serverArgs -WorkingDirectory $RepoRoot -RedirectStandardOutput $ServerOut -RedirectStandardError $ServerErr -PassThru -WindowStyle Hidden
    $newline = [Environment]::NewLine
    $backendState = Wait-ProcessHttp $server.Id $HealthUrl 120
    if ($backendState -ne 'ready') {
        Stop-ProcessTree $server.Id
        $tail = (Read-LogTail $ServerErr) + $newline + (Read-LogTail $ServerOut)
        throw "Backend failed to start ($backendState; port $ServerPort may already be used by another service).$newline$tail"
    }

    # A fresh data dir must produce initial-password. Wait for it while watching our
    # own process: a foreign service can otherwise answer /api/health first.
    $initialFile = Join-Path $DataDir 'initial-password'
    $initialDeadline = (Get-Date).AddSeconds(30)
    while (-not (Test-Path $initialFile) -and (Get-Date) -lt $initialDeadline) {
        if (-not (Test-ProcessAlive $server.Id)) { break }
        Start-Sleep -Milliseconds 250
    }
    if (-not (Test-Path $initialFile)) {
        Stop-ProcessTree $server.Id
        $tail = (Read-LogTail $ServerErr) + $newline + (Read-LogTail $ServerOut)
        throw "initial-password was not generated in $DataDir. Another service may be answering on port $ServerPort; stop it and retry.$newline$tail"
    }
    Write-Output "Backend ready: $HealthUrl (pid $($server.Id))"
    $initial = (Get-Content $initialFile -Raw).Trim()
    Set-Content -Path $InitialPasswordCopy -Value $initial -NoNewline

    Write-Output 'Starting Vite dev server...'
    $viteArgs = @($viteBin)
    $vite = Start-Process -FilePath $node -ArgumentList $viteArgs -WorkingDirectory $WebRoot -RedirectStandardOutput $ViteOut -RedirectStandardError $ViteErr -PassThru -WindowStyle Hidden
    $viteState = Wait-ProcessHttp $vite.Id $WebUrl 90
    if ($viteState -ne 'ready') {
        Stop-ProcessTree $vite.Id
        Stop-ProcessTree $server.Id
        $tail = (Read-LogTail $ViteErr) + $newline + (Read-LogTail $ViteOut)
        throw "Vite failed to start ($viteState; port $WebPort may already be used by another service).$newline$tail"
    }
    Write-Output "Vite ready: $WebUrl (pid $($vite.Id))"

    $state = [ordered]@{
        serverPid = [int]$server.Id
        vitePid = [int]$vite.Id
        dataDir = $DataDir
        serverPort = $ServerPort
        webPort = $WebPort
        initialPasswordFile = $InitialPasswordCopy
        startedAt = (Get-Date).ToString('o')
    }
    $state | ConvertTo-Json | Set-Content -Path $StateFile -Encoding utf8

    Write-Output ''
    Write-Output 'E2E stack is ready.'
    if ($initial) {
        Write-Output "  initial password: $initial"
    } else {
        Write-Output '  initial password: (initial-password file not found; password was already changed?)'
    }
    Write-Output "  data directory:   $DataDir"
    Write-Output "  logs:             $(Join-Path $DataDir 'logs')"
    Write-Output '  run tests:        npm run e2e'
    Write-Output '  stop + clean:     pwsh -File scripts/e2e-stack.ps1 stop'
}

function Show-Status {
    $state = Get-StackState
    if (-not $state) {
        Write-Output 'E2E stack is not running (no state file).'
        return
    }
    $healthy = $false
    try {
        $healthy = (Invoke-WebRequest -UseBasicParsing -Uri $HealthUrl -TimeoutSec 3).StatusCode -eq 200
    } catch { }
    Write-Output ("server pid={0} alive={1}" -f $state.serverPid, (Test-ProcessAlive ([int]$state.serverPid)))
    Write-Output ("vite   pid={0} alive={1}" -f $state.vitePid, (Test-ProcessAlive ([int]$state.vitePid)))
    Write-Output ("backend health: {0}" -f $(if ($healthy) { 'ok' } else { 'down' }))
    Write-Output ("data directory: {0}" -f $state.dataDir)
}

switch ($Action) {
    'start' { Start-Stack }
    'restart' { Start-Stack }
    'stop' { Stop-Stack -Clean:(-not $KeepData) }
    'status' { Show-Status }
}
