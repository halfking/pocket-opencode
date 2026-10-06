// 夹具：为 notes-stt-error-visibility 制造 / 撤销 `stt_unavailable` 前提。
//
// ## 为什么需要它（2026-10-04 实测）
//
// 这条 flow 守的是「转写失败时用户能看到可行动原因」。前端 `api/stt-error.ts`
// 的闸门只放行带 `stt_unavailable:` 前缀的后端文案，其余一律走通用兜底。
// 而 ASR 开通后（mimo-v2.5-asr 有了上游 provider），后端**再也不会**返回
// `stt_unavailable:` —— 前提消失，flow 恒红，等于没有护栏。
//
// ## 怎么确定性造出这个前提
//
// `internal/server/server_stt_settings.go` 的 gatewayTarget() 里有一条：
//
//	// 手工指定的模型当前探测不通过：说清楚为什么，别静默换模型。
//	return nil, fmt.Errorf("stt_unavailable: 网关模型 %q 当前不可用（%s）", …)
//
// 也就是说：**把 STT 的 gatewayModel 指向一个不存在的模型**，后端就会确定性地
// 给出带错误码的可行动原因。实测返回：
//
//	stt_unavailable: 网关模型 "zz-no-such-model-for-test" 当前不可用
//	（网关 606 个模型里没有语音转写类模型）；外部语音转写服务未配置 API Key
//	（设置 → 语音转写）
//
// 这条消息里含「外部语音转写服务未配置」，**正好命中 flow 现有的断言正则**，
// 所以 flow 本身一行都不用改。
//
// ## 用法
//
//	node scripts/stt-error-fixture.mjs --induce    # 跑 flow 之前
//	node scripts/maestro-run.mjs .maestro/notes-stt-error-visibility.yaml
//	node scripts/stt-error-fixture.mjs --restore   # 跑完立刻还原（务必）
//	node scripts/stt-error-fixture.mjs --dry       # 只看状态
//
// ## 为什么 --restore 必须存在、且只删自己写的那一行
//
// gatewayModel 留着一个不存在的模型 = **用户的语音转写是坏的**。
// 所以还原不是可选的收尾，是这条夹具的一部分。
// DELETE 带上 `payload->>'gatewayModel' = SENTINEL` 条件：万一用户自己
// 配过真实模型，绝不会被这个夹具误删。

import { execFileSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { join } from 'node:path'

// ★ 2026-10-07 跨平台化：原来无条件写死某台 Windows 机器上的 psql.exe
// （`C:/workspace/openpocket/logs/pg/dist2/pgsql/bin/psql.exe`），
// macOS / Linux 上必然找不到。
//
// 现在按 ① POCKET_PSQL ② 本机常见位置 ③ PATH 上的 psql 依次找；
// 都找不到就**响亮退出并说明怎么配** —— 静默走到 execFileSync 才会
// 抛一个看不出「是路径没配」的 ENOENT。
const PSQL = (process.env.POCKET_PSQL
  || [
      '/opt/homebrew/opt/libpq/bin/psql',
      '/usr/local/opt/libpq/bin/psql',
      '/usr/bin/psql',
      '/opt/homebrew/bin/psql',
      join(process.env.LOCALAPPDATA || '', 'Programs/PostgreSQL/*/bin/psql.exe'),
    ].find((p) => p && !p.includes('*') && existsSync(p))
  || 'psql')
const SENTINEL = 'zz-no-such-model-for-test'
const USER = 'user-admin'
const WS = 'ws_user-admin'
const SCHEMA = 'opencode_pocket'

const mode = process.argv.includes('--restore') ? 'restore'
  : process.argv.includes('--dry') ? 'dry'
  : 'induce'

// 全部 ASCII：psql 遇到非 ASCII 的多行 -c 会报 invalid byte sequence。
const PAYLOAD = JSON.stringify({
  channel: 'auto', gatewayModel: SENTINEL,
  externalBaseURL: '', externalModel: '', externalTransport: '', language: 'zh',
})

const q = (sql) => String(execFileSync(
  PSQL, ['-h', '127.0.0.1', '-p', '5432', '-U', 'postgres', '-d', 'postgres', '-t', '-A', '-c', sql],
  { encoding: 'utf8', timeout: 60000, maxBuffer: 33554432 },
)).trim()

const countSql = `SELECT count(*) FROM ${SCHEMA}.user_settings WHERE namespace='stt' AND id='default'`
const currentSql = `SELECT coalesce(payload->>'gatewayModel','') FROM ${SCHEMA}.user_settings
  WHERE namespace='stt' AND id='default'`

console.log(`stt settings rows [${SCHEMA}.user_settings namespace='stt'] = ${q(countSql)}`)
const current = q(currentSql)
console.log(`current gatewayModel = ${current === '' ? '(unset / auto discovery)' : current}`)

if (mode === 'dry') {
  console.log(`\n--dry: no change. next step would be --${mode === 'dry' ? 'induce' : mode}`)
  process.exit(0)
}

if (mode === 'induce') {
  if (current === SENTINEL) {
    console.log('already induced (gatewayModel is the sentinel) -- nothing to do')
  } else {
    if (current !== '') {
      console.log(`WARNING: a REAL gatewayModel is configured ("${current}").`)
      console.log('  --restore will NOT delete it (it only removes the sentinel row).')
    }
    q(`INSERT INTO ${SCHEMA}.user_settings
        (user_id, workspace_id, namespace, id, payload, secret_encrypted, updated_at)
      VALUES ('${USER}', '${WS}', 'stt', 'default', '${PAYLOAD}', '', 0)
      ON CONFLICT (user_id, workspace_id, namespace, id)
      DO UPDATE SET payload = EXCLUDED.payload`)
    console.log(`induced: gatewayModel = ${SENTINEL}`)
    console.log('  now run: node scripts/maestro-run.mjs .maestro/notes-stt-error-visibility.yaml')
    console.log('  then  : node scripts/stt-error-fixture.mjs --restore   <-- do not skip')
  }
} else {
  const n = q(`WITH d AS (DELETE FROM ${SCHEMA}.user_settings
        WHERE namespace='stt' AND id='default' AND payload->>'gatewayModel'='${SENTINEL}'
        RETURNING 1) SELECT count(*) FROM d`)
  console.log(n === '0'
    ? 'restore: nothing removed (no sentinel row). A real gatewayModel, if any, was left alone.'
    : 'restore: sentinel row removed -- STT is back to auto discovery.')
  console.log(`stt settings rows now = ${q(countSql)}`)
}
