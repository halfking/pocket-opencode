$ErrorActionPreference = 'Stop'
# 把一张真格式发票 PDF 的 base64 注入 IMAP 夹具，并新增一封带该附件的
# 增值税发票邮件。放在脚本里是因为 base64 有 2KB 多，手写容易出错。
$path = 'C:\workspace\openpocket\scripts\imap-fixture-mails.mjs'
$b64 = (Get-Content -Raw "$env:TEMP\fixture-invoice.b64").TrimEnd()
$lines = Get-Content $path -Encoding UTF8
$anchor = ($lines | Select-String -Pattern '^export function buildMails' | Select-Object -First 1).LineNumber
if (-not $anchor) { throw 'buildMails anchor not found' }
$head = $lines[0..($anchor - 2)]
$tail = $lines[($anchor - 1)..($lines.Count - 1)]

$inject = @()
$inject += ''
$inject += '// 一张**真格式**的发票 PDF（base64 由 backend/internal/email/gen_fixture_invoice_test.go 生成）。'
$inject += '// 之前这里用的是退化 PDF（只有 Catalog、无页树）：采集器能落盘，但 A4 网格导出会被'
$inject += '// pdfcpu 拒绝，导致「发票导出」这条需求在夹具环境里永远拿不到真实产物。'
$inject += 'const validInvoicePdfB64 ='
$inject += $b64
$inject += "  ''"

$all = @($head) + @($inject) + @($tail)
[System.IO.File]::WriteAllLines($path, $all, (New-Object System.Text.UTF8Encoding $false))
Write-Host "injected, total lines = $($all.Count)"
