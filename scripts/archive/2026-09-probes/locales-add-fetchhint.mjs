// 给 9 个语言包补 email.fetchHint* 键。
// 直接改 JSON 会因缩进/顺序产生巨大 diff，所以按键重写整个 email 段。
import { readFileSync, writeFileSync } from 'node:fs'

const DIR = 'frontend/src/locales'
const T = {
  'zh-CN': {
    fetchHintNetwork: '后台收信未完成，已显示已同步邮件',
    fetchHintClassifierOff: '自动归类服务未启用，邮件已同步但未自动分类',
    fetchHintAuth: '登录状态已失效，请重新登录',
    fetchHintUnavailable: '后台服务暂不可用，邮件已同步',
  },
  'zh-TW': {
    fetchHintNetwork: '背景收信未完成，已顯示已同步郵件',
    fetchHintClassifierOff: '自動歸類服務未啟用，郵件已同步但未自動分類',
    fetchHintAuth: '登入狀態已失效，請重新登入',
    fetchHintUnavailable: '背景服務暫不可用，郵件已同步',
  },
  'en-US': {
    fetchHintNetwork: 'Background fetching did not finish; showing synced messages',
    fetchHintClassifierOff: 'Auto-categorization is off; messages synced but not categorized',
    fetchHintAuth: 'Your session expired. Please sign in again.',
    fetchHintUnavailable: 'Background service unavailable; messages synced',
  },
  'de-DE': {
    fetchHintNetwork: 'Hintergrund-Abruf nicht abgeschlossen; synchronisierte Nachrichten werden angezeigt',
    fetchHintClassifierOff: 'Automatische Kategorisierung ist deaktiviert; Nachrichten synchronisiert, aber nicht kategorisiert',
    fetchHintAuth: 'Sitzung abgelaufen. Bitte erneut anmelden.',
    fetchHintUnavailable: 'Hintergrunddienst nicht verfügbar; Nachrichten synchronisiert',
  },
  'es-ES': {
    fetchHintNetwork: 'La descarga en segundo plano no terminó; se muestran los mensajes sincronizados',
    fetchHintClassifierOff: 'La categorización automática está desactivada; mensajes sincronizados sin categoría',
    fetchHintAuth: 'La sesión ha caducado. Inicia sesión de nuevo.',
    fetchHintUnavailable: 'Servicio en segundo plano no disponible; mensajes sincronizados',
  },
  'fr-FR': {
    fetchHintNetwork: 'Récupération en arrière-plan incomplète ; messages synchronisés affichés',
    fetchHintClassifierOff: 'Catégorisation automatique désactivée ; messages synchronisés mais non catégorisés',
    fetchHintAuth: 'Session expirée. Veuillez vous reconnecter.',
    fetchHintUnavailable: 'Service en arrière-plan indisponible ; messages synchronisés',
  },
  'ja-JP': {
    fetchHintNetwork: 'バックグラウンド取得が完了しませんでした。同期済みメールを表示しています',
    fetchHintClassifierOff: '自動分類が無効です。メールは同期されましたが分類されていません',
    fetchHintAuth: 'セッションの有効期限が切れました。再度ログインしてください。',
    fetchHintUnavailable: 'バックグラウンドサービスを利用できません。メールは同期されました',
  },
  'ko-KR': {
    fetchHintNetwork: '백그라운드 수신이 완료되지 않아 동기화된 메일을 표시합니다',
    fetchHintClassifierOff: '자동 분류가 꺼져 있어 메일은 동기화되었지만 분류되지 않았습니다',
    fetchHintAuth: '세션이 만료되었습니다. 다시 로그인해 주세요.',
    fetchHintUnavailable: '백그라운드 서비스를 사용할 수 없습니다. 메일은 동기화되었습니다',
  },
  'pt-BR': {
    fetchHintNetwork: 'O download em segundo plano não concluiu; exibindo as mensagens sincronizadas',
    fetchHintClassifierOff: 'A categorização automática está desativada; mensagens sincronizadas sem categoria',
    fetchHintAuth: 'Sua sessão expirou. Entre novamente.',
    fetchHintUnavailable: 'Serviço em segundo plano indisponível; mensagens sincronizadas',
  },
}

for (const [locale, add] of Object.entries(T)) {
  const p = `${DIR}/${locale}.json`
  const j = JSON.parse(readFileSync(p, 'utf8'))
  j.email = { ...(j.email || {}), ...add }
  writeFileSync(p, JSON.stringify(j, null, 2) + '\n', 'utf8')
  console.log(`${locale}: email 段 = ${JSON.stringify(j.email)}`)
}
