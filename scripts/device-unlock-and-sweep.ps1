# Unlock the local DB on the real device, then sweep EVERY mobile route.
#
# ASCII-ONLY on purpose: PowerShell 5.1 reads a BOM-less .ps1 as ANSI, so UTF-8
# Chinese comments get mangled and break string quoting
# (see scripts/install-apk-to-device.ps1 for the same trap).
#
# Why this exists: 12 data-heavy routes (/email*, /notes, /contacts, /vault,
# /pkm/today, /study, /meetings, /rss) are behind the `requiresLobster` guard
# in frontend/src/app/routeGuards.ts. That guard checks whether the local
# SQLCipher key is in memory, which only happens after a successful master
# password entry. The gate is real encryption, so it is NOT bypassed here --
# the master password is entered through the app's own unlock UI.
#
# Usage:
#   $env:POCKET_MASTER='<device master password>'
#   powershell -ExecutionPolicy Bypass -File scripts\device-unlock-and-sweep.ps1
#
# Env:
#   POCKET_SERIAL   device serial            (default: auto-detect the only one)
#   POCKET_CDP_PORT CDP forward port         (default 9333)
param(
    [string]$Serial = $(if ($env:POCKET_SERIAL) { $env:POCKET_SERIAL } else { '' }),
    [int]$Port = $(if ($env:POCKET_CDP_PORT) { [int]$env:POCKET_CDP_PORT } else { 9333 })
)
$ErrorActionPreference = 'Stop'
$env:PATH = "C:\Users\86133\AppData\Local\Android\platform-tools;$env:PATH"
$PKG = 'com.kaixuan.opencode.pocket'
$MASTER = $env:POCKET_MASTER

if (-not $MASTER) {
    Write-Host '[FAIL] POCKET_MASTER is not set.'
    Write-Host '  The unlock gate is real SQLCipher encryption, not a UI toggle, so it'
    Write-Host '  cannot be bypassed. Set the device master password and re-run:'
    Write-Host '    $env:POCKET_MASTER=''<password>'''
    exit 2
}

if (-not $Serial) {
    $devs = @(& adb devices | Select-Object -Skip 1 | Where-Object { $_.Trim() } |
        ForEach-Object { ($_ -split '\s+')[0] } | Where-Object { $_ })
    if ($devs.Count -ne 1) { Write-Host "[FAIL] expected exactly 1 device, found $($devs.Count)"; exit 2 }
    $Serial = $devs[0]
}
Write-Host "[device] $Serial"

$appPid = (& adb -s $Serial shell pidof $PKG).Trim()
if (-not $appPid) { Write-Host '[FAIL] app not running; start it first'; exit 2 }
& adb -s $Serial forward --remove-all 2>&1 | Out-Null
& adb -s $Serial forward "tcp:$Port" "localabstract:webview_devtools_remote_$appPid" | Out-Null
Start-Sleep -Seconds 2

function Eval([string]$e) {
    $out = & .\scripts\webview-eval.ps1 -Expression $e -Port $Port 2>$null
    ($out | Select-Object -Last 1)
}

# ---- 1. enter the master password through the app's own unlock UI --------------
# Vue needs the native input setter + an input event; assigning .value directly
# does not update the model (this is why `adb shell input text` never worked).
$fill = "(()=>{" +
        "const i=document.querySelector('input[type=password]');" +
        "if(!i)return 'NO_PASSWORD_INPUT';" +
        "const s=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;" +
        "s.call(i,'" + ($MASTER -replace "'", "\'") + "');" +
        "i.dispatchEvent(new Event('input',{bubbles:true}));" +
        "return 'len='+i.value.length})()"
Write-Host "[unlock] $(Eval $fill)"

$click = "(()=>{" +
         "const b=Array.from(document.querySelectorAll('button')).find(x=>/解锁|确定|确认/.test(x.textContent.trim()));" +
         "if(!b)return 'NO_UNLOCK_BUTTON';" +
         "if(b.disabled)return 'BUTTON_DISABLED';" +
         "b.click();return 'clicked:'+b.textContent.trim()})()"
Write-Host "[unlock] $(Eval $click)"
Start-Sleep -Seconds 6

$after = Eval "location.hash"
Write-Host "[unlock] hash now = $after"
if ($after -like '#/login*') {
    Write-Host '[WARN] still on the login route. The password may be wrong, or the app'
    Write-Host '       may be in the "create master password" dialog instead of "unlock".'
    Write-Host '       body: ' + (Eval "document.body.innerText.replace(/\s+/g,' ').slice(0,200)")
    exit 3
}

# ---- 2. sweep every route ------------------------------------------------------
$routes = @(
    '/', '/ai', '/ai-chat', '/agents', '/agents/new', '/local-agent',
    '/notes', '/notes/new', '/contacts', '/cost', '/gateway', '/instances',
    '/email', '/email/settings', '/email/accounts', '/email/accounts/new',
    '/email/invoices', '/email/summary', '/email/folders', '/email/cleanup',
    '/finance', '/meetings', '/meetings/new', '/rss', '/rss/add', '/pkm/today',
    '/study', '/servers', '/more', '/tasks', '/sessions', '/notifications',
    '/settings', '/settings/llm-gateway', '/settings/stt', '/settings/permissions',
    '/settings/scheduled-tasks', '/settings/scheduled-tasks/new',
    '/marketplace/skills', '/marketplace/agents', '/marketplace/workbuddies',
    '/flashcards', '/flashcards/new', '/flashcards/browser', '/flashcards/stats',
    '/flashcards/io'
)

$rows = @()
foreach ($r in $routes) {
    $null = Eval "location.hash='#$r';'go'"
    Start-Sleep -Milliseconds 1800
    $expr = "(()=>{const b=document.body.innerText||'';" +
            "return JSON.stringify({h:location.hash,n:b.length,t:b.replace(/\s+/g,' ').trim().slice(0,120)})})()"
    $j = $null
    try { $j = (Eval $expr) | ConvertFrom-Json } catch { $j = $null }
    if ($null -eq $j) { $rows += [pscustomobject]@{ route=$r; verdict='EVAL-FAILED'; len=0; snippet='' }; continue }
    if ($j.h -like '#/login*')      { $v = 'GATED->login' }
    elseif ($j.n -lt 120)           { $v = 'SHORT(可能是空状态，需人眼看)' }
    else                            { $v = 'RENDERS' }
    $rows += [pscustomobject]@{ route=$r; verdict=$v; len=$j.n; snippet=$j.t }
}
$rows | Format-Table -AutoSize -Wrap
''
'RENDERS  : ' + (($rows | Where-Object verdict -eq 'RENDERS').Count) + ' / ' + $rows.Count
'GATED    : ' + (($rows | Where-Object verdict -like 'GATED*').Count)
'SHORT    : ' + (($rows | Where-Object verdict -like 'SHORT*').Count)
'FAILED   : ' + (($rows | Where-Object verdict -eq 'EVAL-FAILED').Count)
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$out = "logs\real-device-route-sweep-$stamp.csv"
$rows | Export-Csv -NoTypeInformation -Path $out
Write-Host "written: $out"
