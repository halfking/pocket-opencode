# 邮箱账户输入（2026-09-07 / 08）

权威配置在本机 `envs/`，不入 openpocket 仓库：

| 账户 | envs 文件 | IMAP | SMTP | 凭证 KEY |
|---|---|---|---|---|
| 凯轩企业邮 | `emails/corporate/kaixuan.yaml` | imap.exmail.qq.com:993 SSL | smtp.exmail.qq.com:465 SSL | `KAIXUAN_EMAIL_AUTH_CODE`（客户端专用密码，不用网页密码） |
| QQ 私人 | `emails/personal/qq.yaml` | imap.qq.com:993 SSL | smtp.qq.com:465 SSL | `QQ_EMAIL_AUTH_CODE` |
| 163 ×3 | `emails/personal/163.yaml` | imap.163.com:993 SSL | smtp.163.com:465 SSL | `EMAIL_163_*_AUTH_CODE` |

写入 PG：`bash scripts/seed_email_accounts.sh`（从 envs loader 读授权码）。

## 163 必带头信息

网易官方要求：IMAP 登录成功后、`SELECT INBOX` 前发送 RFC 2971 `ID`，声明 `name` / `version` / `vendor` / `support-email`。否则返回 `NO SELECT Unsafe Login`。

实现：`backend/internal/email/fetcher.go` `sendClientID`（`pocketd` / `1.0.0` / `openpocket`）。ID 失败不阻断，再降级 POP3（`pop.163.com:995`）。

参考：<https://help.mail.163.com/faqDetail.do?code=d7a5dc8471cd0c0e8b4b8f4f8e49998b374173cfe9171305fa1ce630d7f67ac2eda07326646e6eb0>

## 启动同步

- PG `email_accounts` 是配置 SSOT；前端 `local_email_accounts` 是镜像（不含凭证）。
- `startEmailConfigSync()`：登录且本地库解锁后 LWW 对齐。
- 进收件箱会再拉一次账户 + 最近邮件。
