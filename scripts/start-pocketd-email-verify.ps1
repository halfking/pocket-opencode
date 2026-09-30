$ErrorActionPreference = 'Continue'
# 邮件流水线验证实例：跑在 :8099，不碰 :8088 上另一个会话的服务。
# 与 start-pocketd-verify.ps1 的差别：scheduler 打开（要验证每日定时流水线
# 真的会排期），并显式设置 POCKET_EMAIL_PIPELINE_HOUR。
$repo = 'C:\workspace\openpocket'
$exe = 'C:\workspace\openpocket\logs\pocketd-email-v1.exe'
$port = 8099
$keyFile = 'C:\workspace\openpocket\logs\.gateway-key'

$env:POCKET_POSTGRES_DSN = 'postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable'
$env:POCKET_PG_SCHEMA    = 'opencode_pocket'
$env:POCKET_DEV_AUTH     = 'true'
$env:POCKET_AUTH_LEGACY_ONLY = 'true'
$env:POCKET_HTTP_PORT    = "$port"
$env:POCKET_SCHEDULER_ENABLED = 'true'
$env:POCKET_EMAIL_FETCH_ENABLED = 'true'
# 下一分钟触发：pipelineLoop 只认「小时」，所以设成当前小时即可看到排期日志；
# 真正到点执行由单测（注入时钟）保证。
$env:POCKET_EMAIL_PIPELINE_HOUR = '9'
$env:POCKET_DATA_DIR     = 'C:\workspace\openpocket\logs\email-verify-data'
New-Item -ItemType Directory -Force -Path $env:POCKET_DATA_DIR | Out-Null
if (Test-Path $keyFile) { $env:POCKET_LLM_GATEWAY_API_KEY = (Get-Content -Raw -Path $keyFile).Trim() }

Set-Location $repo
Start-Process -FilePath $exe -WindowStyle Hidden `
  -RedirectStandardOutput 'C:\workspace\openpocket\logs\pocketd-email.out.log' `
  -RedirectStandardError  'C:\workspace\openpocket\logs\pocketd-email.err.log'

Start-Sleep -Seconds 14
try {
  $hz = (Invoke-WebRequest -Uri "http://127.0.0.1:$port/healthz" -TimeoutSec 8).Content
  Write-Host "[OK] email-verify instance healthz = $hz on :$port" -ForegroundColor Green
} catch {
  Write-Warning "[FAIL] port $port not reachable: $($_.Exception.Message)"
  Get-Content 'C:\workspace\openpocket\logs\pocketd-email.err.log' -Tail 12
  exit 3
}
