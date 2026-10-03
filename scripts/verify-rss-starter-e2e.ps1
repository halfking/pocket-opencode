param(
  [int]$Port = 18190,
  [string]$User = 'admin',
  # Pass in at call time: -AuthPass '<pass>'. Never bake a real password into
  # a committed script. When empty, read it from the POCKET_DEMO_PASSWORD env
  # var, and fail loudly rather than silently sending an empty credential.
  [string]$AuthPass = $env:POCKET_DEMO_PASSWORD,
  [switch]$SkipRefresh
)
$ErrorActionPreference = 'Stop'
$port = $Port
if ([string]::IsNullOrEmpty($AuthPass)) {
  throw 'no password: pass -AuthPass or set POCKET_DEMO_PASSWORD (see scripts/start-rssdemo-backend.ps1)'
}
# Log in for real instead of trusting a token cached in .scratch/: a stale token
# file turns every later assertion into a mystery 401.
$login = Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:$port/api/auth/login" `
  -ContentType 'application/json' -TimeoutSec 30 `
  -Body (@{ username = $User; password = $AuthPass } | ConvertTo-Json -Compress)
if (-not $login.token) { throw "login as '$User' returned no token" }
$tok = $login.token
$h = @{ Authorization = "Bearer $tok" }

function Get-Json($url) { Invoke-RestMethod -Uri $url -Headers $h -TimeoutSec 60 }
function Post-Json($url, $body) {
  if ($null -eq $body) { Invoke-RestMethod -Method Post -Uri $url -Headers $h -TimeoutSec 120 }
  else { Invoke-RestMethod -Method Post -Uri $url -Headers $h -ContentType 'application/json' -Body $body -TimeoutSec 120 }
}
$base = "http://127.0.0.1:$port"

'=== 1. built-in catalog (no DB) ==='
$cat = Get-Json "$base/api/rss/sources/starter"
"categories=$($cat.categories -join ',') feeds=$($cat.feeds.Count)"
$byCat = $cat.feeds | Group-Object category | ForEach-Object { "$($_.Name)=$($_.Count)" }
"  " + ($byCat -join '  ')

'=== 2. one-tap import ==='
$imp = Post-Json "$base/api/rss/sources/import-starter" '{}'
"created=$($imp.created) skipped=$($imp.skipped) total=$($imp.total)"

'=== 3. import again (must be idempotent) ==='
$imp2 = Post-Json "$base/api/rss/sources/import-starter" '{}'
"created=$($imp2.created) skipped=$($imp2.skipped)"

'=== 4. real fetch: refresh every source once (real outbound HTTP) ==='
$srcs = (Get-Json "$base/api/rss/sources").sources
"subscribed=$($srcs.Count)"
$totalNew = 0; $okCount = 0; $errCount = 0
$detail = @()
if ($SkipRefresh) { '  (-SkipRefresh: reusing items already in DB)' }
foreach ($s in $(if ($SkipRefresh) { @() } else { $srcs })) {
  try {
    $r = Post-Json "$base/api/rss/sources/$($s.id)/refresh" $null
    $totalNew += [int]$r.newItems
    $okCount++
    $detail += [pscustomobject]@{ title = $s.title; new = $r.newItems; dup = $r.duplicates; err = '' }
  } catch {
    $errCount++
    $detail += [pscustomobject]@{ title = $s.title; new = 0; dup = 0; err = $_.Exception.Message }
  }
}
"refreshed_ok=$okCount failed=$errCount new_items_total=$totalNew"
$detail | Where-Object { $_.new -gt 0 -or $_.err -ne '' } | Select-Object -First 20 |
  ForEach-Object { "  {0,-22} new={1,-4} {2}" -f $_.title, $_.new, $_.err }

'=== 5. items landed in DB scope ==='
$items = Get-Json "$base/api/rss/items?limit=200"
"items_visible=$($items.items.Count)"

'=== 6. daily digest (run) ==='
$d = (Post-Json "$base/api/rss/digest/run" $null).digest
"date=$($d.date) itemCount=$($d.itemCount) sourceCount=$($d.sourceCount)"
"headline=$($d.headline)"
foreach ($sec in $d.sections) { "  [$($sec.label)] $($sec.items.Count) items" }
'--- shareable body (first 700 chars) ---'
$body = $d.body
if ($body.Length -gt 700) { $body.Substring(0,700) + "`n...[truncated]" } else { $body }

'=== 7. digest history ==='
$hst = Get-Json "$base/api/rss/digests?limit=5"
"history=$($hst.digests.Count) newest=$($hst.digests[0].date) count=$($hst.digests[0].itemCount)"

'=== 8. flashcards starter library ==='
$st = Get-Json "$base/api/flashcards/starter"
"decks=$($st.decks.Count) imported=$($st.imported)"
foreach ($d2 in $st.decks) { "  $($d2.deckId) cards=$($d2.cardCount) newPerDay=$($d2.newPerDay)" }
$fi = Post-Json "$base/api/flashcards/starter/import" '{}'
"imported_decks=$($fi.decks) cards_created=$($fi.cardsCreated) skipped=$($fi.cardsSkipped)"
$st2 = Get-Json "$base/api/flashcards/starter"
"imported_flag_after=$($st2.imported)"
$fc = Get-Json "$base/api/flashcards?limit=400"
"flashcards_visible=$($fc.cards.Count) decks_visible=$($fc.decks.Count)"
$sample = $fc.cards | Select-Object -First 1
if ($sample) { "sample_card_id=$($sample.id) state=$($sample.state) deck=$($sample.deckId)" }
