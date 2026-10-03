# WeCom callback live check: builds ciphertext + signature with .NET (NOT with the
# Go code under test) and drives a real running pocketd process.
#
# ASCII-ONLY ON PURPOSE. PowerShell 5.1 decodes a BOM-less .ps1 as ANSI, so UTF-8
# Chinese comments get mangled and BREAK STRING QUOTING. The mangled bytes were
# observed to silently break parsing here: New-Sealed returned an empty string
# instead of the base64 ciphertext, and the only symptom was a 400 from the
# server -- which reads exactly like a server-side bug. scripts\install-apk-to-device.ps1
# documents the same trap. Keep this file ASCII.
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File scripts\wecom-live-check.ps1 http://127.0.0.1:18077

$ErrorActionPreference = 'Stop'
$base   = $args[0]
$aesKey = 'jWmYm7qr5nMoAUwZRjGtBxmz3KA1tkAJHu8vY6NnH2k'
$token  = 'QDG6eK'
$corp   = 'ww5823bf96d3bd5549'
$ts     = '1409659813'
$nonce  = '1372623149'
$key    = [Convert]::FromBase64String($aesKey + '=')

function New-Sealed([string]$msg) {
    $rand = [byte[]](0x11,0x22,0x33,0x44,0x55,0x66,0x77,0x88,0x99,0xaa,0xbb,0xcc,0xdd,0xee,0xff,0x00)
    $mb = [Text.Encoding]::UTF8.GetBytes($msg)
    $cb = [Text.Encoding]::UTF8.GetBytes($corp)
    # random(16) + msg_len(4, BE) + msg + receiveid
    $pl = New-Object byte[] (16 + 4 + $mb.Length + $cb.Length)
    [Array]::Copy($rand, 0, $pl, 0, 16)
    $lb = [BitConverter]::GetBytes([UInt32]$mb.Length); [Array]::Reverse($lb)
    [Array]::Copy($lb, 0, $pl, 16, 4)
    [Array]::Copy($mb, 0, $pl, 20, $mb.Length)
    [Array]::Copy($cb, 0, $pl, 20 + $mb.Length, $cb.Length)
    # PKCS#7 pad to the AES block size (16), NOT 32. A full extra block is
    # legal when len is already a multiple, so pad ranges over [1..16].
    # Using 32 here produces pad values Go correctly rejects as illegal
    # PKCS#7 -- which looks exactly like a wrong-key failure. Cost me a while.
    $pad = 16 - ($pl.Length % 16)
    $pd = New-Object byte[] ($pl.Length + $pad)
    [Array]::Copy($pl, 0, $pd, 0, $pl.Length)
    for ($i = $pl.Length; $i -lt $pd.Length; $i++) { $pd[$i] = [byte]$pad }
    $aes = [Security.Cryptography.Aes]::Create()
    $aes.Mode = 'CBC'; $aes.Padding = 'None'; $aes.Key = $key; $aes.IV = $key[0..15]
    return [Convert]::ToBase64String($aes.CreateEncryptor().TransformFinalBlock($pd, 0, $pd.Length))
}

function Get-Sig([string]$payload) {
    # sha1 over the four values sorted lexicographically, concatenated
    $parts = @($token, $ts, $nonce, $payload) | Sort-Object
    $sha = [Security.Cryptography.SHA1]::Create()
    return ([BitConverter]::ToString($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes(($parts -join ''))))).Replace('-', '').ToLower()
}

$fail = 0

# --- 1. URL verification (GET, safe mode) ---------------------------------------
# echostr MUST be URL-encoded: a bare '+' in base64 decodes to a space in a query
# string, so the server receives a different string and the signature never matches.
# That exact mistake produced a 403 here before the encoding was added.
$echo = New-Sealed '1616140317555161061'
if (-not $echo) { Write-Host 'FAIL: New-Sealed produced nothing'; exit 1 }
$sig = Get-Sig $echo
$url = "$base/callback/weixin?msg_signature=$sig&timestamp=$ts&nonce=$nonce&echostr=$([uri]::EscapeDataString($echo))"
try {
    $r = Invoke-WebRequest $url -UseBasicParsing -TimeoutSec 10
    if ($r.Content -eq '1616140317555161061' -and $r.StatusCode -eq 200) {
        Write-Host "PASS URL-VERIFY  $($r.StatusCode) body='$($r.Content)'"
    } else {
        Write-Host "FAIL URL-VERIFY  $($r.StatusCode) body='$($r.Content)' (expected 200 + plaintext, no quotes/newline)"; $fail++
    }
} catch {
    Write-Host "FAIL URL-VERIFY  $($_.Exception.Message)"; $fail++
    Get-Content (Join-Path (Split-Path $base -Parent) '..\.scratch-pocketd77err.txt') -ErrorAction SilentlyContinue | Out-Null
}

# --- 2. event push (POST) --------------------------------------------------------
$ev = "<xml><ToUserName><![CDATA[$corp]]></ToUserName><FromUserName><![CDATA[user1]]></FromUserName><MsgType>event</MsgType><Event>change_contact</Event><ChangeType>create_user</ChangeType></xml>"
$enc = New-Sealed $ev
$sig2 = Get-Sig $enc
$body = "<xml><ToUserName><![CDATA[$corp]]></ToUserName><Encrypt><![CDATA[$enc]]></Encrypt></xml>"
try {
    $r2 = Invoke-WebRequest -Method Post -Uri "$base/callback/weixin?msg_signature=$sig2&timestamp=$ts&nonce=$nonce" `
        -Body $body -ContentType 'text/xml; charset=utf-8' -UseBasicParsing -TimeoutSec 10
    if ($r2.Content -eq 'success') { Write-Host "PASS EVENT       $($r2.StatusCode) body='$($r2.Content)'" }
    else { Write-Host "FAIL EVENT       $($r2.StatusCode) body='$($r2.Content)' (expected success)"; $fail++ }
} catch {
    Write-Host "FAIL EVENT       $($_.Exception.Message)"; $fail++
}

# --- 3. negative control: a forged signature must be rejected --------------------
try {
    Invoke-WebRequest "$base/callback/weixin?msg_signature=$('0' * 40)&timestamp=$ts&nonce=$nonce&echostr=$([uri]::EscapeDataString($echo))" -UseBasicParsing -TimeoutSec 10 | Out-Null
    Write-Host 'FAIL NEGCTRL     forged signature was ACCEPTED (200)'
    $fail++
} catch {
    $code = $_.Exception.Response.StatusCode.value__
    if ($code -eq 403) { Write-Host 'PASS NEGCTRL     forged signature -> 403' }
    else { Write-Host "FAIL NEGCTRL     forged signature -> $code (expected 403)"; $fail++ }
}

exit $fail
