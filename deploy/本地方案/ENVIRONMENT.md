# 本地环境变量契约

`local-db-init.sh` 生成的 `.env.local` 只用于本机，不提交到 Git。模板见 `.env.local.example`。

| 变量 | 默认值 | 用途 |
| --- | --- | --- |
| `POCKET_ENV` | `development` | 运行 profile |
| `POCKET_HTTP_PORT` | `8088` | Pocket API 宿主端口 |
| `POCKET_FRONTEND_PORT` | `4175` | Pocket frontend 宿主端口 |
| `POCKET_POSTGRES_DSN` | 由脚本生成 | 容器访问 `pocket_local` |
| `POCKET_TEST_POSTGRES_DSN` | 由脚本生成 | 宿主 Go 集成测试 |
| `POCKET_JWT_SECRET` | 本地固定测试值 | 仅本地开发，禁止生产复用 |
| `POCKET_DEV_AUTH` | `true` | 本地 `admin/admin` 登录 |
| `POCKET_ALLOWED_ORIGINS` | localhost:4175 | WebSocket/CORS 来源 |
| `POCKET_OPENCODE_INSTANCES` | host.docker.internal:4096 | 可选 OpenCode 实例目录 |
| `POCKET_EMAIL_FETCH_ENABLED` | `false` | 本地关闭 IMAP scheduler |

数据库脚本还支持 `POCKET_LOCAL_PG_HOST`、`POCKET_LOCAL_PG_PORT`、`POCKET_LOCAL_PG_USER`、`POCKET_LOCAL_PG_PASSWORD`、`POCKET_LOCAL_PG_DATABASE` 覆盖默认值。

## 宿主与容器 DSN

- 宿主测试：`127.0.0.1:15432`
- Pocket 容器：`host.docker.internal:15432`
- 共享 PG 容器内部：`r112_postgres:5432`

密码只通过环境变量传递，脚本输出不打印密码。
