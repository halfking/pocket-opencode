#!/usr/bin/env node
/**
 * 补齐 settings.apiAddressNotSaved。
 *
 * 背景：自定义地址保存后若读回为空/缺失，旧实现直接整页重载，用户随后只看到
 * 「登录失败：用户名或密码错误」，完全无从判断是自己的地址没存住。
 * 现在显式报错，需要这条文案。
 */
import { readFileSync, writeFileSync } from 'node:fs'
import { join, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'

const DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'src', 'locales')

const ADDITIONS = {
  'zh-CN': { apiAddressNotSaved: '地址未能保存，请检查输入是否为空或格式不正确' },
  'zh-TW': { apiAddressNotSaved: '地址未能儲存，請檢查輸入是否為空或格式不正確' },
  'en-US': { apiAddressNotSaved: 'The address could not be saved. Check that it is not empty and is a valid URL.' },
  'ja-JP': { apiAddressNotSaved: 'アドレスを保存できませんでした。空でないか、URL 形式かを確認してください' },
  'ko-KR': { apiAddressNotSaved: '주소를 저장하지 못했습니다. 비어 있지 않은지, 올바른 URL 형식인지 확인하세요' },
  'de-DE': { apiAddressNotSaved: 'Die Adresse konnte nicht gespeichert werden. Bitte prüfen, ob sie nicht leer und eine gültige URL ist.' },
  'fr-FR': { apiAddressNotSaved: "L'adresse n'a pas pu être enregistrée. Vérifiez qu'elle n'est pas vide et qu'elle est une URL valide." },
  'es-ES': { apiAddressNotSaved: 'No se pudo guardar la dirección. Comprueba que no esté vacía y sea una URL válida.' },
  'pt-BR': { apiAddressNotSaved: 'Não foi possível salvar o endereço. Verifique se não está vazio e se é uma URL válida.' },
}

for (const [locale, add] of Object.entries(ADDITIONS)) {
  const file = join(DIR, `${locale}.json`)
  const raw = readFileSync(file, 'utf8')
  const eol = raw.includes('\r\n') ? '\r\n' : '\n'
  const data = JSON.parse(raw)
  data.settings = { ...data.settings, ...add }
  writeFileSync(file, JSON.stringify(data, null, 2).replace(/\n/g, eol) + eol, 'utf8')
  console.log(`${locale}: +${Object.keys(add).length}`)
}
console.log('done')
