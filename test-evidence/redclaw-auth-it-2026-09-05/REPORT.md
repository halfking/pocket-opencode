# openpocket × RedClaw 认证联调报告

> 日期:2026-09-05  
> 结论:**10/10 测试通过**,认证链路打通并具备降级与一键回滚能力  
> 拓扑:pocketd 容器(arm64, port 18090) ⇄ host.docker.internal ⇄ RedClaw local-stack 容器组(dal:27080 / gateway:27081 / authagent:27092 / admin:27093)

## 1. 联调环境

| 组件 | 形态 | 说明 |
|---|---|---|
| RedClaw local-stack | docker compose,8 服务全部启动 | 本期构建 dal/gateway/authagent/admin 4 个 arm64 镜像(`Dockerfile.localstack`) |
| pocketd | `opencode-pocket:auth-it`(最小 alpine + 静态二进制) | `--env-file /tmp/pocket-redclaw-it.env`,无 PG(dev 模式) |
| 互信配置 | RedClaw `ADMIN_JWT_ISSUER=redclaw`、`ADMIN_JWT_AUDIENCE=pocket-api` | 与 openpocket `IDENTITY_SHARED_SECRET`(=ADMIN_JWT_SIGNING_KEY) + 默认 issuer allowlist 对齐 |
| 种子数据 | `dal.tenants(default)` + `dal.users(u-pocket-admin-001/admin/admin角色)` | postgres 超管插入(绕过 RLS) |

token claims 实测:`sub=u-pocket-admin-001, tenant_id=default, session_id=…, roles=[admin], iss=redclaw, aud=[pocket-api]` — 与 identity-go `VerifyMultiIssuer` 的 issuer allowlist + aud 匹配 + HS256 验签完全契合。

## 2. 测试矩阵(10/10 PASS)

| # | 用例 | 结果 | 证据 |
|---|---|---|---|
| R1 | `POST /api/auth/login` 正确密码 | ✅ | `auth_method=redclaw, user_id=u-pocket-admin-001, role=admin`,RedClaw token 原样透传 |
| R2 | `GET /api/auth/me` | ✅(修复后) | 200,返回扁平 employee 画像;修复前 `{"error":"empty employee"}`(响应结构不匹配) |
| R3 | 受保护 API `/api/instances` 带 RedClaw token | ✅ | 200——requireAuth 走 identity-go 多 issuer 路径接受 RedClaw token |
| R4 | 错误密码 | ✅ | 401 invalid credentials(不降级) |
| R5 | `GET /api/auth/sso/login?state=…` | ✅ | 返回 authagent SSO URL(sso=true) |
| R6 | **RedClaw 宕机降级**(docker stop admin) | ✅(本次修复) | dev 凭据 → `auth_method=dev-bypass`;修复前恒 503 全员锁死 |
| R7 | admin 容器恢复后 | ✅ | 登录自动复原 `auth_method=redclaw`,无需重启 pocketd |
| R8 | `POST /api/auth/logout` → `me` | ✅ | logout 200;me 401(RedClaw 服务端 session 撤销生效) |
| R9 | **legacy 一键回滚**(`POCKET_AUTH_LEGACY_ONLY=true`) | ✅ | 启动日志 WARN,登录走 `dev-bypass`,legacy token 过 requireAuth(iss=pocket 本地路径) |
| R10 | 恢复 RedClaw 模式重启 | ✅ | `auth_method=redclaw` |

## 3. 联调中发现并修复的缺陷

### 3.1 openpocket 侧(commit `8a467c2`)
| 严重度 | 缺陷 | 修复 |
|---|---|---|
| P1 | `/api/auth/me` 解析结构不匹配(RedClaw 返回顶层扁平) → 500 empty employee | `MeResult = EmployeeInfo`(扁平别名),handler 直写 |
| P0 | RedClaw 不可达时登录 fail-closed,全员锁死 | `ErrRedClawUnavailable && POCKET_DEV_AUTH=true` → dev 旁路降级;401/403 仍 fail-closed |
| P1 | SSO 回调空 employee id 塌缩到哨兵 `"sso-user"` | 改为 502 拒绝 |
| P1 | SSO 回调不回传 state → SsoCallbackView 的 CSRF 校验形同虚设 | state 加入 302 query;前端改为强制相等校验 |
| P2 | SSO 影子 provider 记成第三种 `"redclaw-sso"` | 统一为 `"redclaw"` |
| P2 | `.env.example` 缺镜像客户端变量 | 补 `POCKET_REDCLAW_AUTH_URL/SECRET/TIMEOUT_SEC/TENANT_ID` |

### 3.2 RedClaw 侧(commit `74e1efa`, branch feature/redclaw-pgbroker-delivery-lease)
| 严重度 | 缺陷 | 修复 |
|---|---|---|
| P0 | admin 密码登录不向 DAL 透传 `X-Tenant-ID` → DAL TenantResolver 401 tenant_required,**密码登录完全不可用** | dalclient 新增 `PostWithHeaders`;Login 转发 X-Tenant-ID(缺省 default) |
| P0 | local-stack compose 漏配 `OIDC_REDIRECT_URL`/`OIDC_TOKEN_ENDPOINT`/`OIDC_ID_TOKEN_SIGNING_KEY` → authagent 启动即 panic | compose 补三项(带默认值) |
| P1(运维) | `authagent` schema 表 platform_app 无权限 → session 持久化 42501 | psql GRANT(本地栈一次性授权) |
| P1(运维) | `scanUser` 将 position_id/department_id/agent_id/agent_status 扫进 `string`,NULL 行 500 | 种子行用 `''`(根治需 RedClaw 后续把扫描列改为 coalesce/`*string`) |

## 4. 已知边界(不阻塞,记录在案)

1. **SSO 完整回调链未测**:本机无 OIDC IdP 容器,`/sso/login` URL 生成已验证,IdP→callback→铸 token 链路需接真实 IdP(casdoor 容器在 252 库中存在,可作二期)。
2. **token 撤销的旁路**:pocketd `requireAuth` 只验签名+过期,登出后旧 token 打普通受保护 API 仍 200(仅 `/api/auth/me` 因代理 RedClaw 而 401)。前端以 me 判活即可兜底;彻底闭环需 pocketd 增加会话校验(RedClaw SessionValidator 同款)。
3. **RedClaw gateway `/api/v1/users/verify` 未启用**:local-stack compose 未配 `REDCLAW_SHARED_SECRET`(空则端点不注册),生物识别的 RedClaw 用户校验分支暂走本地。需要时在 compose 加 `REDCLAW_SHARED_SECRET`(≥32B)并同步 `POCKET_REDCLAW_SECRET`。
4. **JWT TTL 2h**:前端 1h50m 静默续期尚未做(迁移文档 §12 待办)。

## 5. 复现手册

```bash
# 1) RedClaw 侧
cd RedClaw && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go -C services/platform-go build -o .build-local/platform-go/<svc> ./cmd/<svc>   # 注意 -o 用绝对路径
docker build -f deploy/docker/Dockerfile.localstack --build-arg BINARY=<svc> -t redclaw-<svc>:local .
cd deploy/local-stack
ADMIN_JWT_ISSUER=redclaw ADMIN_JWT_AUDIENCE=pocket-api \
  OIDC_TOKEN_ENDPOINT=http://host.docker.internal:9999/oidc/token \
  docker compose -f docker-compose.local-stack.yml --env-file config/deploy.env up -d

# 2) 种子(llm-gateway-pg, 超管)
#    dal.tenants(default) + dal.users(u-pocket-admin-001/admin, 可空列填 '')

# 3) pocketd 侧
docker run -d --name pocket-auth-it -p 18090:8088 \
  --add-host host.docker.internal:host-gateway \
  --env-file /tmp/pocket-redclaw-it.env opencode-pocket:auth-it

# 4) 冒烟
curl -X POST localhost:18090/api/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"Pocket@Test2026"}'
```

关键环境变量(`POCKET_REDCLAW_ADMIN_SECRET` = `IDENTITY_SHARED_SECRET` = RedClaw `ADMIN_JWT_SIGNING_KEY`):
见 `/tmp/pocket-redclaw-it.env`(联调现场文件,密钥不入库)。

---

## 附录（2026-09-05 追记）：SSO 回调链修复后待补测项

R5 记录的"SSO 完整回调链未测"缺口已在 pocket 侧修复流程合约
（见 `docs/handoff/2026-09-05-sso-state-contract-mismatch.md` §6）：

- pocket `/api/auth/sso/callback` 现要求绑定 cookie（`/api/auth/sso/login`
  签发），302 只携带一次性 `sso_code`；token 经
  `POST /api/auth/sso/exchange` 交付，不再进 URL。
- 因此补测时 **RedClaw 本地栈必须把 `OIDC_REDIRECT_URL` 指向
  `http://localhost:18090/api/auth/sso/callback`**（IdP 浏览器重定向
  落 pocket），并部署 casdoor IdP 容器。
- handler 级同等断言已由 `backend/internal/server/auth_sso_test.go`
  （fake auth-agent 全链路）覆盖；IdP 实链路仍待容器环境补测归档。

---

## 附录（2026-09-05 追记 2）：SSO 全链路 IT 补测完成（IT-2）

R5 的"SSO 完整回调链未测"缺口已补测归档，**ALL PASSED**：
`it2-sso-chain-20260905/`（脚本与运行产物，含每步 HTTP 证据）。

### 环境

- pocket：`opencode-pocket:auth-it-0905`（openpocket main @ cfa9f20，
  含 external_state 端到端比对），docker run -p 18090:8088
- auth-agent：RedClaw `feat/authagent-sso-external-state` @ f606610
  （external_state 回显 + JWKS 验签 + AllowPlainHTTP），
  redclaw-local-stack compose，:27092
- IdP：`deploy/local-stack/scripts/mock-idp.py`（:9999，RS256 + JWKS，
  与 casdoor 同验证路径）；casdoor v2.63 容器同批部署（:28000，
  已种子 redclaw-local 应用/sso-it 用户/专用证书），见下"已知限制"

### 链路断言（全部通过）

1. pocket login → 64 hex 绑定 nonce + HttpOnly cookie；
2. auth-agent /sso/login 携带 external_state，**未泄漏进 IdP authorize URL**；
3. IdP authorize 302 → pocket callback（code + auth-agent 自发 state）；
4. pocket 消费绑定 cookie、透传换平台 JWT、**external_state 端到端比对通过**
   （auth-agent JWKS 验签 IdP 的 RS256 id_token 后原样回显）；
5. 302 仅携带一次性 sso_code（token 未进 URL，P1-2）；
6. exchange 换 {token, user_id=sso-it-user-001, workspace_id}；重放 401；
7. 平台 JWT：sub == user_id，session_id 存在（持久会话签发路径生效）；
8. callback 重放 / 冷启动回调 → `error=sso_session` 拒绝；
9. /sso/logout 撤销 session；撤销后同 token → 401。

### 已知限制与后续

- casdoor v2.63 `/api/login type=code` 的 grant-type 校验拿不到授权码
  （自动化脚本不可驱动），自动 IT 采用 JWKS 同路径的 mock IdP；
  casdoor 浏览器流可人工验证：本地栈已部署并种子完毕
  （`bootstrap-casdoor.sh`，应用回调已指向 pocket callback）。
- 真实 casdoor 若要过 auth-agent 的 tenant 断言，id_token 需携带
  `tenant` claim（casdoor 自定义 token claim 配置，待运维对齐）。
