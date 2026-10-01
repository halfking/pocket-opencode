# 网关配置被写成分片状态：已修复，并留下一条复发路径

日期：2026-10-02
对象：`https://llm.kxpms.cn/v1`（用户指定的默认网关）

## 现象

admin 工作区（`ws_user-admin`）的网关配置在数据库里是**分片的**，
两个存储各持一半，合起来才勉强可用；`default` 工作区则完全没有 key。

修复前 `GET /api/llm-gateway/config` 实测：

| 工作区 | baseURL | apiKeySet | models |
|---|---|---|---|
| `ws_user-admin` | 正确 | **true** | **0** |
| `default` | 正确 | **false** | 9 |

`llm_gateway_configs`（每工作区只有一条 active）：

| id | workspace | active | keylen | models |
|---|---|---|---|---|
| 1 | default | true | 0 | 9 |
| 4 | ws_user-admin | true | 0 | 9 |

`user_settings`：

| workspace | secretlen | models |
|---|---|---|
| `default` | 0 | 9 |
| `ws_user-admin` | 51 | **0** |

## 为什么会坏：两个存储，读时叠加

生效值不是读某一张表，而是叠加出来的
（`llm_gateway_resolve.go:94` `effectiveGatewayState`）：

```
effective = pickGatewayState(workspace)          # 读 llm_gateway_configs 的 active 行
         ⊕ overlayGatewaySetting(user setting)  # 再用 user_settings 覆盖
```

`overlayGatewaySetting`（`llm_gateway_resolve.go:29`）的覆盖规则里有两条
不对称：

- `payload.Models != nil` 就**整体替换**模型列表 —— `[]` 也是非 nil，
  所以一个空数组足以把 9 个默认模型清成 0 个；
- `rec.Secret != ""` 才写 key —— 空 secret 是「不碰」，不是「清空」。

于是两行半成品各自看都「像配置」，合起来得到一个
**能通过鉴权、但一个模型都没有**的网关。设置页模型下拉会是空的，
对话侧也没有候选模型可回退。`default` 工作区更彻底：两处都无 key，必然 401。

成因是早前一次只带 `baseURL` + `apiKey` 的保存，把 `user_settings` 的
`models` 写成了 `[]`，同时另一条路径又把 `llm_gateway_configs` 的 active 行
写成了无 key。**没有任何一步报错，两步各自都"成功"。**

## 修复

走应用自身的保存路径 `POST /api/llm-gateway/config`（不直接写库），
对 `ws_user-admin` 与 `default` 各发一次，只带 `baseURL` + `apiKey`：

- handler 里 `req.APIKey != ""` 才覆盖，`req.Models == nil` 则保留 active 行
  原有的 9 个默认模型（`llm_gateway_handler.go:314-328`）；
- `SaveConfig` 按既有语义「全部行置 inactive + 插新 active 行」；
- `syncGatewayUserSetting` 顺带把 user setting 一起写成自洽状态。

修复后实测：

| 工作区 | baseURL | apiKeySet | models | preferred |
|---|---|---|---|---|
| `ws_user-admin` | 正确 | true | 9 | 9 |
| `default` | 正确 | true | 9 | 9 |

数据库：每个工作区恰好一条 active 行（id 5 / id 6），`keylen=108`，
9 个模型；两条 `user_settings` 均 `secretlen=51` + 9 个模型。
`GET /api/integration/status` 报 `llm_gateway: enabled/configured = true
(source: persisted)`。

## 凭证实测（不落盘、不进命令行）

库里的密文用 `C:\workspace\openpocket\data\email_master.key` 解开，
在进程内直接打网关，全程不打印明文：

```
GET  https://llm.kxpms.cn/v1/models            -> HTTP 200, 606 models
POST https://llm.kxpms.cn/v1/chat/completions  -> HTTP 200, content = "PONG"
```

默认 9 个模型名逐一核对，**9/9 都在该网关的 606 个模型里**，
不存在「下拉里选得到、实际 404」的项。

## 顺带查明的事实：5 个 data 目录有 5 把互不相同的密钥

拿真实密文逐把试解（只输出成功/失败，不输出明文）：

| 目录 | 网关 row 3 | 两条 IMAP 凭据 |
|---|---|---|
| `openpocket\data` | **可解**（51 字符 `sk-`） | 不可解 |
| `openpocket\backend\data` | 不可解 | 不可解 |
| `openpocket\wt3\backend\data` | 不可解 | **可解**（16 字符） |
| `openpocket-wt-maildeploy\backend\data` | 不可解 | 不可解 |
| `openpocket-wt-font\backend\data` | 不可解 | 不可解 |

即：**网关配置与 IMAP 凭据现在分属两把密钥**，任何单一 data 目录都无法同时
读通两者。这也解释了为什么必须显式设定 `POCKET_EMAIL_MASTER_KEY`——否则
换目录即等于换密钥（`internal/email/crypto.go:84-87` 已记录该陷阱）。

## 遗留（未做，需拍板）

1. **两把密钥的合并是运维决策**，不是代码问题：需要选定一把权威密钥并
   显式设进部署的 `POCKET_EMAIL_MASTER_KEY`，再用它重加密另一边。我没有
   动任何凭据——重加密需要明文 IMAP 密码，而它现在只存在于 wt3 那把密钥下。
2. `seedAdminGatewaySetting`（`server_user_settings.go:130`）在 env 没有
   `POCKET_LLM_GATEWAY_API_KEY` 时仍会写下 `Secret: def.APIKey`（空串）的
   user setting。本次已把两条都补齐，但**这条写入路径本身**仍可能再造一个
   空 secret 的 setting。是否改成「env 无 key 就不写」待定。
3. 本次修复只覆盖两个已知工作区。若将来出现带空 `workspace_id` 的令牌，
   `workspaceIDFromRequest` 会回退到 `default`，那条路径现在已被本次修复覆盖，
   但机制上依赖「default 有 key」这一巧合，值得后续收紧。
