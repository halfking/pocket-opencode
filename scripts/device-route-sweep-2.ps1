# Sweep part 2: the mobile routes the 2026-10-02 evening sweep never covered.
#
# Why this exists
# ---------------
# route-sweep.csv covered 35 hardcoded static routes. Cross-referencing that
# list against every `path:` declared in router-mobile.ts showed 28 more
# routes that were never visited AND are not behind the requiresLobster gate,
# so they were testable all along. They include the whole /gateway admin area,
# /flashcards/*, /cost, /agents/* and the auth entry points.
#
# Parametrised routes are filled with REAL ids taken from the API, not with
# invented ones, so a "renders" verdict means the page bound a real record.
# Routes with no real object left (no deck, no scheduled task, no session in
# this workspace) are visited with a sentinel id on purpose: the expectation
# is a graceful "not found / empty" state, and a blank page or an unhandled
# error is still a failure worth seeing.
#
# Why CDP and not Maestro: see scripts/device-route-sweep.ps1 -- the Capacitor
# WebView is opaque to uiautomator on this app.
#
# ASCII-ONLY ON PURPOSE: PowerShell 5.1 decodes a BOM-less .ps1 as ANSI, so
# UTF-8 CJK comments get mangled and can break string quoting. Keep this file
# ASCII. (scripts/wecom-live-check.ps1 documents the same trap with the exact
# symptom it produces.)
#
# Usage:
#   adb -s <serial> forward tcp:9333 localabstract:webview_devtools_remote_<appPid>
#   powershell -ExecutionPolicy Bypass -File scripts\device-route-sweep-2.ps1

$ErrorActionPreference = 'Stop'
$port = 9333
$out = 'logs\real-device-20261002-190414\route-sweep-2.csv'

# --- ids harvested from the live API on 2026-10-02 -----------------------------
# GET /api/tasks              -> task-1790943941540
# GET /api/llm-gateway/nodes  -> 2  (name=default, base=https://llm.kxpms.cn)
# GET /api/sessions           -> 0 rows
# GET /api/scheduled-tasks    -> 0 rows
# GET /api/flashcards/decks   -> 404 (endpoint not mounted)
$taskId   = 'task-1790943941540'
$nodeId   = '2'
$missing  = 'no-such-object'   # sentinel for objects that do not exist here

$routes = @(
  # static, no parameters
  '/cost',
  '/flashcards/io',
  '/flashcards/new',
  '/forgot-password',
  '/login',
  '/register',
  '/auth/sso/callback',
  '/agents/new',
  '/gateway',
  '/settings/scheduled-tasks/new',

  # bound to a REAL record
  "/tasks/$taskId",
  "/gateway/$nodeId",
  "/gateway/$nodeId/catalog",
  "/gateway/$nodeId/credentials",
  "/gateway/$nodeId/live",
  "/gateway/$nodeId/models",
  "/gateway/$nodeId/providers",
  "/gateway/$nodeId/routing-config",

  # no such object in this workspace: expect a graceful empty state
  "/gateway/$nodeId/credentials/$missing",
  "/opencode/sessions/$missing",
  "/agents/$missing",
  "/agents/$missing/edit",
  "/flashcards/decks/$missing",
  "/flashcards/decks/$missing/options",
  "/flashcards/decks/$missing/review",
  "/flashcards/notes/$missing/edit",
  "/settings/scheduled-tasks/$missing",
  "/settings/scheduled-tasks/$missing/edit"
)

# Verdict rules, and why they are NOT a fixed character count.
# The previous sweep called anything under 120 body characters "blank", which
# mislabelled 8 legitimately-empty pages as defects. The bottom nav alone
# contributes ~30 characters, so any fixed threshold is really a guess about
# the shell. Instead we measure the MAIN region (the shell nav is outside it)
# and we always emit the snippet, so a human can audit the call rather than
# trust it.
#   RENDERS     main region has route-specific text
#   EMPTY-STATE main region is nearly empty -> a real, intentional empty state
#   GATED       bounced to /login (auth or local-DB lock)
#   ERROR       the app surfaced an error boundary
$rows = @()
foreach ($r in $routes) {
    $null = & .\scripts\webview-eval.ps1 -Expression "location.hash='#$r';'go'" -Port $port 2>$null
    Start-Sleep -Seconds 2
    # ASCII-only error markers on purpose: a BOM-less .ps1 is decoded as ANSI
    # by PowerShell 5.1, so CJK literals here get mangled and can silently
    # break string quoting. A localised error page is still caught by eye,
    # because the snippet below is exported verbatim for audit.
    $expr = "(()=>{const m=document.querySelector('main');const b=document.body.innerText||'';" +
            "const t=(m?m.innerText:b).replace(/\s+/g,' ').trim();" +
            "return JSON.stringify({h:location.hash,main:t.length,body:b.length," +
            "err:/Something went wrong|Internal Server Error|failed to load|unhandled|status 5\d\d/i.test(t)," +
            "t:t.slice(0,140)})})()"
    $outText = & .\scripts\webview-eval.ps1 -Expression $expr -Port $port 2>$null | Select-Object -Last 1
    $j = $null
    try { $j = $outText | ConvertFrom-Json } catch { $j = $null }
    if ($null -eq $j) {
        $rows += [pscustomobject]@{ route = $r; verdict = 'EVAL-FAILED'; hash = ''; mainLen = 0; snippet = "$outText" }
        continue
    }
    $verdict =
        # A bounce is only a bounce when the hash actually moved somewhere
        # else. Comparing against the requested route matters: navigating TO
        # /login lands on #/login legitimately, and the previous version of
        # this rule reported that as GATED, i.e. as if the unlock door had
        # blocked us when we asked for the door.
        if ($j.h -ne ('#' + $r) -and $j.h -like '#/login*') { 'GATED' }
        elseif ($j.err)                            { 'ERROR' }
        # A very short, stable main region is not necessarily an empty state:
        # it can be a one-line error such as a localized "failed to load".
        # The error regex above is ASCII-only (see note), so a Chinese error
        # page lands here. SHORT-STATE exists to force a human to look at the
        # snippet instead of quietly filing it as "legitimately empty".
        elseif ($j.main -ge 25)                     { 'RENDERS' }
        elseif ($j.main -ge 12)                     { 'SHORT-STATE' }
        else                                        { 'EMPTY-STATE' }
    $rows += [pscustomobject]@{ route = $r; verdict = $verdict; hash = $j.h; mainLen = $j.main; snippet = $j.t }
}

$rows | Format-Table -AutoSize -Wrap
''
foreach ($v in 'RENDERS', 'EMPTY-STATE', 'GATED', 'ERROR', 'EVAL-FAILED') {
    '{0,-12} {1}' -f $v, (($rows | Where-Object verdict -eq $v).Count)
}
'total         ' + $rows.Count
$rows | Export-Csv -NoTypeInformation -Path $out
"written: $out"
