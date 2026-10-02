# Sweep the mobile routes on the real device via CDP and record, per route,
# whether it renders real content, stays blank, or errors.
#
# Why CDP and not Maestro: the Capacitor WebView exposes nothing to
# uiautomator (verified 2026-10-02: a 102-node dump whose only non-empty
# text was the launcher's own icons), so Maestro text assertions cannot
# see any of this app. See docs/handoff and
# logs/real-device-20261002-190414/summary.md.
#
# "Blank" is measured as: body text shorter than the shell chrome, or no
# route-specific marker. The shell alone (Redclaw header + bottom nav) is
# ~4 labels, so a rendered page is expected to be much longer than that.
$ErrorActionPreference = 'Stop'
$port = 9333
$routes = @(
  '/', '/ai', '/ai-chat', '/notes', '/email', '/email/accounts', '/email/invoices',
  '/email/summary', '/email/folders', '/finance', '/contacts', '/vault', '/pkm/today',
  '/study', '/meetings', '/rss', '/servers', '/more', '/instances', '/tasks',
  '/sessions', '/notifications', '/settings', '/settings/llm-gateway', '/settings/stt',
  '/settings/permissions', '/settings/scheduled-tasks', '/marketplace/skills',
  '/marketplace/agents', '/marketplace/workbuddies', '/flashcards', '/flashcards/browser',
  '/flashcards/stats', '/agents', '/local-agent'
)

$rows = @()
foreach ($r in $routes) {
    $null = & .\scripts\webview-eval.ps1 -Expression "location.hash='#$r';'go'" -Port $port 2>$null
    Start-Sleep -Seconds 2
    $expr = "(()=>{const b=document.body.innerText||'';const t=b.replace(/\s+/g,' ').trim();" +
            "const nav=document.querySelector('nav,.bottom-nav,[class*=bottom]')!=null;" +
            "return JSON.stringify({h:location.hash,n:b.length,t:t.slice(0,110)})})()"
    $out = & .\scripts\webview-eval.ps1 -Expression $expr -Port $port 2>$null | Select-Object -Last 1
    $json = $null
    try { $json = $out | ConvertFrom-Json } catch { $json = $null }
    if ($null -eq $json) {
        $rows += [pscustomobject]@{ route = $r; verdict = 'EVAL-FAILED'; hash = ''; len = 0; snippet = $out }
        continue
    }
    # An unrouted hash bounces back to /login?...&unlock=1 (local DB locked) or /login.
    if ($json.h -like '#/login*') {
        $rows += [pscustomobject]@{ route = $r; verdict = 'GATED->login'; hash = $json.h; len = $json.n; snippet = $json.t }
    } elseif ($json.n -lt 120) {
        $rows += [pscustomobject]@{ route = $r; verdict = 'BLANK/SHELL-ONLY'; hash = $json.h; len = $json.n; snippet = $json.t }
    } else {
        $rows += [pscustomobject]@{ route = $r; verdict = 'RENDERS'; hash = $json.h; len = $json.n; snippet = $json.t }
    }
}
$rows | Format-Table -AutoSize -Wrap
''
'RENDERS : ' + (($rows | Where-Object verdict -eq 'RENDERS').Count) + ' / ' + $rows.Count
'GATED   : ' + (($rows | Where-Object verdict -like 'GATED*').Count)
'BLANK   : ' + (($rows | Where-Object verdict -like 'BLANK*').Count)
'FAILED  : ' + (($rows | Where-Object verdict -eq 'EVAL-FAILED').Count)
$rows | Export-Csv -NoTypeInformation -Path 'logs\real-device-20261002-190414\route-sweep.csv'
