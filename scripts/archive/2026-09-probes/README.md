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

---

## 2026-10-17 删除：`scripts/append-handoff-*.mjs`（23 个）

同属一次性脚本，按上节「删除（未归档）」的先例处理，不入本目录：

| 脚本 | 当年用途 | 为何删除 |
|---|---|---|
| `append-handoff-4111…4118.mjs`（8 个） | 往 `docs/handoff/*.md` 追加当轮小节 | 追加动作早已完成；内容固化在文档里 |
| `append-handoff-480…490.mjs`（11 个） | 同上（另一批） | 同上 |
| `append-handoff-aw.mjs` / `-part.mjs` / `-v2.mjs` / `-v2b.mjs`（4 个） | 同上（含 CRLF/无 BOM 的追加变体） | 同上 |

**判定依据（2026-10-17 实测，不是推断）**：

- 23 个脚本**在 `package.json` / `gates.json` / 任何 CI workflow / 任何其他脚本里
  都没有被引用**（`grep -rn "append-handoff" --include=package.json --include=gates.json
  --include=*.yml` 返回空；脚本间也无 require/import）。
- 全部已被 git 跟踪 ⇒ 需要时 `git show <commit>:<path>` 随时可取，
  留在工作区只会让人以为它们还能用。
- `scripts/check-no-hardcoded-abs-paths.mjs` 的 `LIVE_SCRIPTS` **本来就不含它们**
  ⇒ 删除不会让那道门失去任何豁免或覆盖面。

⚠ **刻意保留** `scripts/unappend-handoff-last-section.mjs`：它不是一次性脚本，
而是「回退最后一段追加」的可复用工具，与被删的 23 个不同类。
`docs/handoff/_part-4.75.md` 记的「CRLF/无 BOM 的文档追加」**技法**也随之保留
（由该工具承载），删掉的只是当年执行它的那些一次性副本。
