import assert from 'node:assert/strict'
import { test } from 'node:test'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'
import { createServer } from 'vite'
import vue from '@vitejs/plugin-vue'

test('claim confirmation cancels safely and cannot survive account activation', async () => {
  const modules = {
    'claim-page': `
      import { createApp, h } from 'vue'
      import { createI18n } from 'vue-i18n'
      import Claim from '/src/features/notes/LegacyNoteClaim.vue'
      import ConfirmDialog from '/src/components/base/ConfirmDialog.vue'
      import { useAuthStore } from 'virtual:claim-auth'
      import zh from '/src/locales/zh-CN.json'
      const auth = useAuthStore()
      window.addEventListener('switch-account', () => { auth.activationVersion++; auth.activationStatus = 'switching' })
      window.addEventListener('activate-account', () => { auth.activationStatus = 'ready' })
      createApp({ render: () => [h(Claim, { source: 'note', noteId: 'note-1' }), h(ConfirmDialog)] })
        .use(createI18n({ legacy: false, locale: 'zh-CN', messages: { 'zh-CN': zh } })).mount('#app')
    `,
    'claim-auth': `
      import { reactive } from 'vue'
      const auth = reactive({ activationVersion: 0, activationStatus: 'ready' })
      export const useAuthStore = () => auth
    `,
    'claim-storage': `
      export async function canClaimLegacyNote() { return true }
      export async function prepareLegacyNoteClaim() {
        return { workspaceId: 'verified-workspace', async claim() {
          document.querySelector('#claims').value++
        } }
      }
    `,
  }
  const server = await createServer({
    root: fileURLToPath(new URL('../../../../', import.meta.url)), configFile: false,
    optimizeDeps: { noDiscovery: true },
    plugins: [vue(), {
      name: 'legacy-note-claim-fixture', enforce: 'pre',
      resolveId(id, importer) {
        if (id.startsWith('virtual:claim-')) return '\0' + id.slice('virtual:'.length)
        if (!importer?.endsWith('/LegacyNoteClaim.vue')) return
        if (id === '../../stores/auth') return '\0claim-auth'
        if (id === './legacy-note-claim') return '\0claim-storage'
      },
      load(id) { if (id.startsWith('\0claim-')) return modules[id.slice(1)] },
      configureServer(server) {
        server.middlewares.use((req, res, next) => {
          if (req.url !== '/__claim') return next()
          res.setHeader('Content-Type', 'text/html; charset=utf-8')
          res.end('<output id="claims">0</output><div id="app"></div><script type="module" src="/@id/__x00__claim-page"></script>')
        })
      },
    }],
    server: { host: '127.0.0.1', port: 0, hmr: false },
  })
  await server.listen()
  let browser
  try {
    browser = await chromium.launch()
    const page = await browser.newPage()
    await page.goto(`http://127.0.0.1:${server.httpServer.address().port}/__claim`)
    const button = page.getByTestId('legacy-note-claim')
    const dialog = page.getByRole('dialog')
    const confirm = dialog.getByRole('button', { name: '确定', exact: true })
    const claims = page.locator('#claims')

    await button.click()
    await dialog.getByText(/verified-workspace/).waitFor()
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(await claims.textContent(), '0')

    await button.click()
    await dialog.waitFor()
    await page.evaluate(() => window.dispatchEvent(new Event('switch-account')))
    await button.waitFor({ state: 'hidden' })
    await confirm.click()
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(await claims.textContent(), '0')

    await page.evaluate(() => window.dispatchEvent(new Event('activate-account')))
    await button.click()
    await confirm.click()
    await page.getByText('本机笔记已绑定到当前工作区。').waitFor()
    assert.equal(await claims.textContent(), '1')
  } finally {
    await browser?.close()
    await server.close()
  }
})
