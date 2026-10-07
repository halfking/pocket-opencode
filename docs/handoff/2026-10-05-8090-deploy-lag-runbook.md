# 8090 生产实例部署滞后 —— 修复 runbook

> 2026-10-05 实测定位。**本文件不含任何已执行的破坏性动作**，重建需显式授权。

## 结论（一句话）

`opencode-pocket-pocketd-local-openpocket` 跑的是 **2026-09-14 的部署快照**，
比磁盘上已生成的 **10-01 快照**落后两个部署周期 ⇒ 微信回调与闪卡全套路由在容器里不存在。

> ⚠️ **微信回调打不通是【两个】独立缺陷，不是一个。**
>
> | # | 缺陷 | 现状表现 | 修它需要 |
> | --- | --- | --- | --- |
> | ① | 容器版本旧，`/callback/weixin` 路由**不存在** | **404** | 重建容器 |
> | ② | 企微三项密钥**一个都没设** | **503** | 补 3 个环境变量 |
>
> 只修 ①，现象会从 404 变成 503，**看起来像"没修好"**。
> 闪卡则只有 ①（无配置依赖），重建即可。

## 证据（两种独立方法互相印证）

### ① 二进制字符串级（容器内 `strings`）

| 路由 | 容器内 `/app/pocketd` 命中 | 判定 |
| --- | --- | --- |
| `api/flashcards` | **0** | 缺失 |
| `callback/weixin` | **0** | 缺失 |
| `callback/feishu` | 3 | 存在 |
| `api/scheduled-tasks` | 2 | 存在 |
| `api/marketplace/packages` | 2 | 存在 |

### ② 活体 HTTP（不依赖 strings）

| 端点 | 状态码 | 判定 |
| --- | --- | --- |
| `GET :8090/callback/weixin` | **404** | 未注册 |
| `GET :8090/api/flashcards/decks` | **404** | 未注册 |
| `GET :8090/api/flashcards/cards` | **404** | 未注册 |
| `GET :8090/api/scheduled-tasks` | 200 有数据 | 正常 |
| `GET :8090/api/marketplace/packages` | 200 | 正常 |

### ③ 源码历史对照

| commit | `"/api/flashcards` 命中 | 说明 |
| --- | --- | --- |
| `p6e288b3`（容器在跑） | **0** | 无闪卡路由 |
| `0165200`（10-01 快照） | **2** | 已含 |
| 本地 HEAD `e2d6d8aa` | — | 最新 |

`callback/feishu` + `callback/weixin` 在 `0165200` 中命中 3 处 ⇒ **快照本身已包含修复**。

## 部署链断在哪

```
current -> pocket-opp-p0165200-20261001062904   (10-01, version.json: active=true)
              ↓  但从未 apply
运行容器   用的 compose labels: pocket-opp-p6e288b3-20260914092129  (9-14)
容器启动时间: 2026-09-28T16:47:34Z
```

⇒ 快照生成成功、**apply 失败或被跳过**，此后一直用旧镜像。
注意 `bin/current/pocketd-compose-snippet.yml` 的内容是**空占位**
（`services: {}`），它只是版本标记，不含实际服务定义 ——
所以「快照已存在」不等于「重建只需一条命令」。

## 重建前必须确认的三件事

1. **接口无破坏性变更 —— 已核对，可以重建**：
   | 版本 | `mux.HandleFunc(` 路由数 |
   | --- | --- |
   | `0165200`（10-01 快照） | 123 |
   | HEAD（当前源码） | **139** |
   
   取两者路由集合做差集：**被移除的路由 0 条**，只净增 16 条
   ⇒ 重建不会让前端 4175 调不到任何现有接口。
   （`0165200..HEAD` 之间有 1050 个 commit，但 `server.go` 的路由表只增不减。）

2. **数据卷与 PG 不受影响**：`POCKET_POSTGRES_DSN` 指向 `host.docker.internal:5432/pocket`，
   业务数据在 PG 不在容器内；`/app/data`、`/app/config` 是卷，`docker compose up -d` 复用即安全。

3. **回滚留底**：先给当前镜像打标签
   `docker tag opencode-pocket:pocket-opp opencode-pocket:pocket-opp-rollback-20261005`。
   回滚只需把标签改回再 `up -d --no-deps pocketd`。

## 重建命令（**需授权后执行**）

```bash
cd /Users/xutaohuang/workspace/official-deploy/services/opencode-pocket/deploy/bin

# 1) 回滚留底
docker tag opencode-pocket:pocket-opp opencode-pocket:pocket-opp-rollback-20261005

# 2) 重建（context 指向 official-deploy 仓根，Dockerfile.kx-base 从 openpocket 源码构建）
docker compose -f docker-compose.opp.yml -f docker-compose.disk-sessions.yml \
  build pocketd

# 3) 切流
docker compose -f docker-compose.opp.yml -f docker-compose.disk-sessions.yml \
  up -d --no-deps pocketd

# 4) 10s 内应转 healthy
docker ps --filter name=opencode-pocket-pocketd --format '{{.Status}}'
```

## 重建后的验收（照抄执行，不靠「看着好了」）

```bash
T=$(curl -s -X POST http://127.0.0.1:8090/api/auth/login \
      -H 'Content-Type: application/json' \
      -d "{\"username\":\"admin\",\"password\":\"$(docker exec opencode-pocket-pocketd-local-openpocket printenv POCKET_AUTH_PASS)\"}" \
    | python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')

# ① 这两条必须从 404 变成非 404
curl -s -o /dev/null -w 'weixin=%{http_code}\n' http://127.0.0.1:8090/callback/weixin
curl -s -o /dev/null -w 'cards=%{http_code}\n'  http://127.0.0.1:8090/api/flashcards/cards -H "Authorization: Bearer $T"

# ② 飞书 URL 验证挑战必须继续正确（回归守卫：别把能用的也弄坏了）
curl -s -X POST http://127.0.0.1:8090/callback/feishu -H 'Content-Type: application/json' \
  -d '{"type":"url_verification","challenge":"XYZ"}'
# 期望 {"challenge":"XYZ"}

# ③ 老端点不能回归（抽样）
for e in /api/scheduled-tasks /api/marketplace/packages /api/email/accounts /api/instances; do
  printf '%-32s %s\n' "$e" "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:8090$e" -H "Authorization: Bearer $T")"
done
```

## 仍需人工确认的部分（本轮无法自动判定）

- **企业微信三项密钥必须补齐，否则重建了也还是 503。**
  代码位置与要求（**已亲自复核，非转述**）：
  `backend/internal/config/config.go:98-102` 原文：

  > 三项**缺一不可**：任一为空时 `/callback/weixin` 对签名请求一律 503 拒绝，
  > 而不是放行 —— 放行等于把回调端点变成任何人可伪造的公开入口。

  环境变量名（`config.go:309-311`）：

  | 变量 | 内容 | 目标值来源 |
  | --- | --- | --- |
  | `POCKET_WECOM_TOKEN` | 自建应用「Token」 | 企微后台 |
  | `POCKET_WECOM_ENCODING_AES_KEY` | 43 字符（不含 `=`） | 企微后台 |
  | `POCKET_WECOM_CORP_ID` | 企业 ID | 企微后台 |

  实测：容器内 `grep -ciE "wecom|weixin"` = **0** ⇒ 三项一个都没设。
  ⇒ **即使重建容器，微信回调仍会 503**。这是**两个独立缺陷**，别当成一个：
     ① 容器版本旧（路由不存在 → 404）；② 配置缺（三项为空 → 503）。
     修 ① 只会把 404 变成 503。

- **飞书 appid**：容器内只有 `POCKET_FEISHU_APP_ID`，值无法从仓内核实
  （目标是 `cli_aac806f6bab89bd8`，仓内 grep 0 命中，属部署期注入值）。

- 回调地址 `https://m.kxpms.cn/callback/*` 经 56 nginx 转发到本机，
  重建 + 补配置后，还需从公网侧做一次**带真实签名**的事件投递，才算端到端打通。

