#!/usr/bin/env node
/**
 * 补 errors.summaryFailed（2026-09-30 真机审计）。
 *
 * 笔记详情的「生成总结」原先兜底文案是硬编码中文，且把 LLM 网关的
 * 配置状态写死在句子里（'总结生成失败（需要已配置 LLM 网关）'）。
 * 网关未就绪时（真实遇到的 192.168.31.34:8080 ready=false），
 * 用户看到的到底是哪一类失败无从判断。
 * 改为按语义归类后的通用文案：网关类错误会由 error-message
 * 的 llm_unavailable / gatewayUnreachable 映射给出更精确的提示。
 *
 * Run: node scripts/add-summary-error-locale.mjs
 */
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')

const summaryFailed = {
  'zh-CN': '总结生成失败，请确认已配置并可用的模型网关',
  'zh-TW': '摘要產生失敗，請確認已設定且可用的模型閘道',
  'en-US': 'Could not generate a summary. Check that a model gateway is configured and reachable.',
  'ja-JP': '要約を生成できませんでした。モデルゲートウェイの設定と接続を確認してください。',
  'ko-KR': '요약을 생성하지 못했습니다. 모델 게이트웨이가 설정되어 있고 연결 가능한지 확인하세요.',
  'de-DE': 'Zusammenfassung konnte nicht erstellt werden. Prüfe, ob ein Modell-Gateway konfiguriert und erreichbar ist.',
  'fr-FR': "Impossible de générer le résumé. Vérifiez qu'une passerelle de modèle est configurée et joignable.",
  'es-ES': 'No se pudo generar el resumen. Comprueba que la pasarela de modelos esté configurada y accesible.',
  'pt-BR': 'Não foi possível gerar o resumo. Verifique se o gateway de modelos está configurado e acessível.',
}

for (const [locale, value] of Object.entries(summaryFailed)) {
  const file = path.join(DIR, `${locale}.json`)
  const json = JSON.parse(fs.readFileSync(file, 'utf8'))
  json.errors = { ...(json.errors || {}), summaryFailed: value }
  fs.writeFileSync(file, JSON.stringify(json, null, 2) + '\n')
  console.log(`${locale.padEnd(8)} errors.summaryFailed ✓`)
}
