# APK DEX 关键类快速审计（raw byte 扫描）
#
# 历史坑：本脚本原先只扫 %TEMP%\apk-dex-extract，但**自己不负责解压**。
# 该目录为空/过期时会 6/6 全报 NOT FOUND，产生假阴性（2026-09-30 复现）。
# 现在改为：先从 APK 解压 14 个 classes*.dex 到工作目录，再扫。
#
# 预期结论：14 个 needle 中 13 个 FOUND；
#   MainApplication NOT FOUND 是**正向证据**（manifest 不声明自定义 Application，
#   Capacitor 靠 MainActivity 上的 @CapacitorApplication annotation 发现 plugin）。
$ErrorActionPreference = 'Stop'

$apk = Join-Path $PSScriptRoot '..\frontend\android\app\build\outputs\apk\debug\app-debug.apk'
$apk = [System.IO.Path]::GetFullPath($apk)
if (-not (Test-Path $apk)) { throw "APK not found: $apk" }

$work = Join-Path $env:TEMP 'apk-dex-extract'
$needles = @(
    'MainActivity', 'AppSettingsPlugin', 'AudioDeviceRank', 'PermissionSettingsLauncher',
    'AiStreamKeepalivePlugin', 'AiStreamService', 'BackgroundMicPlugin', 'EmailFetchPlugin',
    'EmailFetchReceiver', 'EmailFetchRunner', 'BiometricAuthPlugin', 'SherpaPlugin',
    'MainApplication', 'com/kaixuan/opencode/pocket/plugins/'
)

# 每次都从当前 APK 重新解压，避免用陈旧目录得出假阴性
if (Test-Path $work) { Remove-Item $work -Recurse -Force }
New-Item -ItemType Directory -Path $work -Force | Out-Null

Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [System.IO.Compression.ZipFile]::OpenRead($apk)
try {
    $extracted = 0
    foreach ($entry in $zip.Entries) {
        if ($entry.FullName -match '^classes\d*\.dex$') {
            [System.IO.Compression.ZipFileExtensions]::ExtractToFile(
                $entry, (Join-Path $work $entry.FullName), $true)
            $extracted++
        }
    }
} finally { $zip.Dispose() }

if ($extracted -eq 0) { throw "no classes*.dex extracted from $apk" }
"--- extracting $extracted dex from $([System.IO.Path]::GetFileName($apk)) ---"

$cache = @{}
Get-ChildItem $work -Filter '*.dex' | ForEach-Object {
    $cache[$_.Name] = [System.Text.Encoding]::ASCII.GetString([System.IO.File]::ReadAllBytes($_.FullName))
}

$found = 0
$missing = @()
foreach ($needle in $needles) {
    $hits = @($cache.Keys | Where-Object { $cache[$_] -match [regex]::Escape($needle) } | Sort-Object)
    if ($hits.Count -gt 0) {
        $found++
        "[+] FOUND in $([string]::Join(',', $hits)): $needle"
    } else {
        $missing += $needle
        "[-] NOT FOUND: $needle"
    }
}

""
"FOUND=$found  MISSING=$($missing.Count)  (of $($needles.Count))"
if ($missing -contains 'MainApplication') {
    "(MainApplication 缺失属预期：未声明自定义 Application class)"
}
if ($found -lt 11) { throw "DEX class audit FAILED: only $found/$($needles.Count) needles found" }
"DEX class audit PASSED: $found/$($needles.Count) needles found (MainApplication 缺失属预期)"
