#Requires -Version 7.0
<#
.SYNOPSIS
    PCL-DSH-Go WebUI 前端构建脚本（Windows / PowerShell 7）。

.DESCRIPTION
    在 web/ 下执行可复现构建：
      1) npm ci --no-audit --no-fund（可 -SkipInstall 跳过）
      2) npm run type-check（vue-tsc --noEmit）
      3) npm run build（vite build，产物 web/dist）
      4) 校验 dist 关键文件/引用/大小并打印清单（文件数、总大小）

    任一步失败都会以非零退出码结束。构建不修改 package-lock.json（脚本会前后比对 SHA-256），
    node_modules/ 与 dist/ 均被 web/.gitignore 忽略，不得提交。

.PARAMETER SkipInstall
    跳过 npm ci，复用已有的 web/node_modules（本地快速迭代用）。缺失 node_modules 时直接失败。

.PARAMETER WebRoot
    可选的 web 目录路径，默认 <repo>/web。

.EXAMPLE
    pwsh -File scripts/frontend-build.ps1
    pwsh -File scripts/frontend-build.ps1 -SkipInstall
#>
[CmdletBinding()]
param(
    [switch]$SkipInstall,
    [string]$WebRoot
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$RepoRoot = Split-Path -Parent $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($WebRoot)) {
    $WebRoot = Join-Path $RepoRoot 'web'
}
if (-not (Test-Path -LiteralPath $WebRoot -PathType Container)) {
    Write-Host "ERROR: 找不到 web 目录：$WebRoot" -ForegroundColor Red
    exit 1
}
$WebRoot = (Resolve-Path -LiteralPath $WebRoot).Path
$DistDir = Join-Path $WebRoot 'dist'
$PackagePath = Join-Path $WebRoot 'package.json'
$LockPath = Join-Path $WebRoot 'package-lock.json'

function Write-Step([string]$Message) {
    Write-Host ""
    Write-Host "==> $Message" -ForegroundColor Cyan
}

function Invoke-Npm {
    param([string[]]$NpmArguments)
    $display = 'npm ' + ($NpmArguments -join ' ')
    Write-Host "    $display" -ForegroundColor DarkGray
    Push-Location -LiteralPath $WebRoot
    try {
        & npm @NpmArguments
        if ($LASTEXITCODE -ne 0) {
            throw "$display 失败（退出码 $LASTEXITCODE）"
        }
    }
    finally {
        Pop-Location
    }
}

try {
    Write-Host "PCL-DSH-Go WebUI build" -ForegroundColor Green
    Write-Host "  仓库根：$RepoRoot"
    Write-Host "  web  ：$WebRoot"

    Write-Step '检查 Node/npm 环境'
    foreach ($tool in @('node', 'npm')) {
        if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
            throw "未找到 $tool：请安装 Node.js >= 20.19（推荐 v24.22.1）并加入 PATH"
        }
    }
    $nodeVersion = (& node --version).Trim()
    $npmVersion = (& npm --version).Trim()
    Write-Host "    node $nodeVersion / npm $npmVersion"
    $nodeMajor = [int]($nodeVersion.TrimStart('v').Split('.')[0])
    if ($nodeMajor -lt 20) {
        throw "Node.js 版本过低（$nodeVersion），web/package.json 要求 >= 20.19"
    }

    Write-Step '检查 web 工程文件'
    if (-not (Test-Path -LiteralPath $PackagePath -PathType Leaf)) {
        throw "缺少 package.json：$PackagePath"
    }
    if (-not (Test-Path -LiteralPath $LockPath -PathType Leaf)) {
        throw "缺少 package-lock.json：$LockPath（可复现构建必须提交锁文件）"
    }
    $package = Get-Content -LiteralPath $PackagePath -Raw -Encoding UTF8 | ConvertFrom-Json
    foreach ($scriptName in @('type-check', 'build')) {
        $scriptValue = $package.scripts.$scriptName
        if ($null -eq $scriptValue -or [string]::IsNullOrWhiteSpace([string]$scriptValue)) {
            throw "web/package.json 缺少 scripts.$scriptName"
        }
    }
    $lockBefore = (Get-FileHash -LiteralPath $LockPath -Algorithm SHA256).Hash
    Write-Host "    package-lock.json SHA-256：$($lockBefore.Substring(0, 16))..."

    if ($SkipInstall) {
        Write-Step '跳过依赖安装（-SkipInstall）'
        if (-not (Test-Path -LiteralPath (Join-Path $WebRoot 'node_modules') -PathType Container)) {
            throw "-SkipInstall 需要已有的 web/node_modules；请去掉该参数执行 npm ci"
        }
    }
    else {
        Write-Step '安装依赖（npm ci）'
        $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
        Invoke-Npm @('ci', '--no-audit', '--no-fund')
        $stopwatch.Stop()
        Write-Host ("    npm ci 完成，用时 {0:N1}s" -f $stopwatch.Elapsed.TotalSeconds)
    }

    $lockAfter = (Get-FileHash -LiteralPath $LockPath -Algorithm SHA256).Hash
    if ($lockAfter -ne $lockBefore) {
        throw "package-lock.json 在构建过程中被修改（$($lockBefore.Substring(0, 16))... -> $($lockAfter.Substring(0, 16))...）：请不要提交该改动，或由前端 owner 显式更新锁文件"
    }
    Write-Host "    package-lock.json 未被修改"

    Write-Step '类型检查（vue-tsc --noEmit）'
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    Invoke-Npm @('run', 'type-check')
    $stopwatch.Stop()
    Write-Host ("    类型检查通过，用时 {0:N1}s" -f $stopwatch.Elapsed.TotalSeconds)

    Write-Step '生产构建（vite build）'
    $stopwatch = [System.Diagnostics.Stopwatch]::StartNew()
    Invoke-Npm @('run', 'build')
    $stopwatch.Stop()
    Write-Host ("    vite build 完成，用时 {0:N1}s" -f $stopwatch.Elapsed.TotalSeconds)

    Write-Step '校验 web/dist'
    if (-not (Test-Path -LiteralPath $DistDir -PathType Container)) {
        throw "构建结束但 dist 不存在：$DistDir"
    }
    $distFiles = @(Get-ChildItem -LiteralPath $DistDir -Recurse -File)
    if ($distFiles.Count -eq 0) {
        throw "dist 为空：$DistDir"
    }
    $totalBytes = [long](($distFiles | Measure-Object -Property Length -Sum).Sum)
    $distBytes = $totalBytes

    $requiredFiles = @(
        @{ Path = 'index.html'; MinBytes = 200 },
        @{ Path = 'favicon.webp'; MinBytes = 1000 },
        @{ Path = 'licenses\GPL-3.0.txt'; MinBytes = 1000 },
        @{ Path = 'licenses\HMCL-ADDITIONAL-TERMS.md'; MinBytes = 100 }
    )
    foreach ($required in $requiredFiles) {
        $target = Join-Path $DistDir $required.Path
        if (-not (Test-Path -LiteralPath $target -PathType Leaf)) {
            throw "dist 缺少关键文件：$($required.Path)"
        }
        $size = (Get-Item -LiteralPath $target).Length
        if ($size -lt $required.MinBytes) {
            throw "dist 关键文件过小：$($required.Path)（$size 字节 < $($required.MinBytes)）"
        }
    }

    $assetsDir = Join-Path $DistDir 'assets'
    if (-not (Test-Path -LiteralPath $assetsDir -PathType Container)) {
        throw "dist 缺少 assets 目录"
    }
    $assetFiles = @(Get-ChildItem -LiteralPath $assetsDir -File)
    if (-not ($assetFiles | Where-Object { $_.Extension -eq '.js' })) {
        throw "dist/assets 缺少 JS 产物"
    }
    if (-not ($assetFiles | Where-Object { $_.Extension -eq '.css' })) {
        throw "dist/assets 缺少 CSS 产物"
    }

    $indexHtml = Get-Content -LiteralPath (Join-Path $DistDir 'index.html') -Raw -Encoding UTF8
    if ([string]::IsNullOrWhiteSpace($indexHtml)) {
        throw "dist/index.html 为空"
    }
    $assetReferences = [regex]::Matches($indexHtml, '(?:src|href)="(?<path>/assets/[^"]+)"')
    if ($assetReferences.Count -eq 0) {
        throw "index.html 未引用任何 /assets/ 产物，构建可能不完整"
    }
    foreach ($reference in $assetReferences) {
        $relative = $reference.Groups['path'].Value.TrimStart('/')
        $referencedPath = Join-Path $DistDir $relative
        if (-not (Test-Path -LiteralPath $referencedPath -PathType Leaf)) {
            throw "index.html 引用了不存在的资源：$($reference.Groups['path'].Value)"
        }
    }

    if ($distBytes -lt 50KB) {
        throw "dist 总大小异常偏小（$distBytes 字节）"
    }
    if ($distBytes -gt 50MB) {
        throw "dist 总大小异常偏大（$distBytes 字节）：请检查是否误把 node_modules/源文件打进产物"
    }

    Write-Host ""
    Write-Host ("dist 清单：{0} 个文件，合计 {1:N0} 字节（{2:N2} MB）" -f $distFiles.Count, $distBytes, ($distBytes / 1MB))
    Write-Host '----------------------------------------------------------------------'
    foreach ($file in ($distFiles | Sort-Object FullName)) {
        $relative = $file.FullName.Substring($DistDir.Length + 1)
        Write-Host ("  {0,10:N0} B  {1}" -f $file.Length, $relative)
    }
    Write-Host '----------------------------------------------------------------------'

    Write-Step 'BUILD OK'
    Write-Host "  产物目录：$DistDir" -ForegroundColor Green
    Write-Host "  下一步：WSL 内 go manager 加 --server.static-dir=/mnt/d/Projects/DSHL/PCL-DSH-Go/web/dist 运行；E2E 见 docs/M4_BUILD.md" -ForegroundColor Green
    exit 0
}
catch {
    Write-Host ""
    Write-Host "BUILD FAILED: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}
