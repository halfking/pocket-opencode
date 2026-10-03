# 2026-09 一次性探针归档

2026-10-01 审计轮归档（consolidation §5.2「scripts/ 探针清理」）。均为
2026-09 下旬邮件/CDP/网关排查期的一次性脚本，问题闭环后无日常用途；
脚本间无相互引用，仓库其他文件（package.json / CI / 文档）也不引用。
仍在 git 历史里，需要时可 `git show` 或直接在本目录运行。

| 脚本 | 当年用途 | 为何归档 |
|---|---|---|
| `probe-invoice-extract.mjs` / `probe-invoice-mail.mjs` | 发票抽取规则调试 | 回归已固化为 `backend/internal/email/invoice_realworld_test.go`（夹具已合成化） |
| `gw-audio-probe.mjs` | 网关音频链路探测 | 一次性探测 |
| `cdp-doc-open-intent.mjs` / `cdp-doc-open-probe.mjs` | CDP 打开文档意图排查 | 一次性探测 |
| `locales-add-fetchhint.mjs` | 9 语言包批量补 `fetchHintHttpError` | 已执行完毕；结果在 locales 里，脚本无用武之地 |
| `verify-real-invoice-e2e.mjs` / `verify-real-mailbox-readonly.mjs` | **真实**邮箱/发票端到端复验 | 含真实公司名、发票号、真实邮箱地址（隐私）；且需真邮箱凭据，不可重复执行 |

同轮**删除**（未归档）：`scripts/patch-export-grid-signature.mjs` ——
consolidation §6.4 已裁定「应删」（一次性签名修补，已进正式代码）。

保留在 `scripts/` 的 email 验证工具箱（email-audit-* / email-*-verify /
imap-stub-* / inject-fixture-* / verify-email-*）不属本类：成套、可复跑、
仍在随邮件线演进。
