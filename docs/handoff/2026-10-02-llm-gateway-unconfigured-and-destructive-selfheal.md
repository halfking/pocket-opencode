# 审计发现：LLM 网关当前处于「未配置」状态，且自愈路径在 env 为空时会毁掉已有配置

日期：2026-10-02
对应用户诉求：*「网关设为默认并完成部署与测试验证」*，以及
*「录音…没有即时总结」*

## 现状（实测生产库）

```
 id |         base_url          | is_active |    workspace_id     | keylen
----+---------------------------+-----------+---------------------+--------
  3 | https://llmgo.kxpms.cn/v1 | t         | ws_user-admin_      |      0
 32 | https://llm.kxpms.cn/v1   | t         | default             |      0
 33 | https://llm.kxpms.cn/v1   | t         | ws_user-admin       |      0
```

**三条 active 配置的 `api_key_encrypted` 全是空串。** 而表里另有 30+ 条
`keylen=108`（有真实密文）的行，**全部 `is_active = f`**。

也就是说：**曾经配好过网关，现在生效的那条没有 key。**
所有走网关的功能（AI 对话、邮件智能分类、录音后的即时总结）都会 401。

## 为什么会变成这样

`defaultLLMGatewayState()`（llm_gateway_handler.go:65）：

```go
APIKey: strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_API_KEY")),
```

注释写明设计意图：

> APIKey 只认 POCKET_LLM_GATEWAY_API_KEY，**没有内置默认值**…
> 没有 env 时就是未配置，设置页提示填 key、integration_status 如实报 disabled。

**实测：`POCKET_LLM_GATEWAY_API_KEY` 在整个仓库的 deploy 配置和
`.env.example` 里都不存在。**（`grep` 覆盖 `deploy/`、`.env.example`
均 0 命中；它只出现在 Go 代码里。）

于是 `def.APIKey == ""`。

## 真正的缺陷：自愈路径在 env 为空时是破坏性的

`EnsureLLMGatewayDefaults`（llm_gateway_handler.go:119-129）：

```go
existing, err := s.llmGWStore.LoadConfig(ctx, wsID)
if err != nil {
    // 密文不可解（如 JWT secret 轮换后 cipher 校验失败）或行损坏：
    // 用 env 默认配置覆写毒化行
    SaveConfig(ctx, wsID, def)   // ← def.APIKey == ""
    continue
}
```

`SaveConfig` 做的事是：先把该 workspace **所有行 `is_active = false`**，
再插一条新的 active 行。

所以当**密文临时解不开**（如 JWT secret 轮换）**且 env 也没配 key** 时：

1. 一条**本来只是解不开**的行被判为"毒化"
2. 自愈用 `def`（key 为空）覆写它
3. 同时把原来那条**有真实 key 的 active 行**置为 inactive
4. 结果：配置从"能解密就能用"变成"永久没有 key"

**自愈把"暂时读不出来"改写成了"永久丢失"，而且顺手停用了本来可用的配置。**
这与库里 30+ 条有 key 的行全部 inactive、active 的反而无 key，完全吻合。

对比同文件的另一条路径——`handleGatewayConfigSave`（:288-294）就**做对了**：

```go
if req.APIKey != "" { current.APIKey = req.APIKey }   // 空 = 保留旧值
if current.APIKey == "" { 400 "apiKey required for first configuration" }
```

也就是说 **HTTP 保存路径有"空=保留"保护，自愈/播种路径没有**。两条路径对
空 key 的处理不一致，这就是 bug 的形状。

## 已排除的疑似问题（留档免得重查）

- **设置页会不会误报"已配置"？** 不会。`GET /api/llm-gateway/config` 返回
  `"apiKeySet": st.APIKey != ""`（:259），空 key 正确报 `false`，
  前端 `SettingsLLMGateway.vue:293` 的 `canSave` 也会正确禁用保存。
  显示是诚实的，问题在服务端状态本身。
- **前端会不会把空 key 发上去覆盖掉旧值？** 不会。
  `SettingsLLMGateway.vue:389` 是 `apiKey: form.apiKey || undefined`，
  留空就不带该字段，符合"留空 = 保留"的提示。

## 顺带清理了我自己留下的污染

我用 `scripts/verify-forgot-password-e2e.mjs` 造的 2 个一次性账号
（`probe-auth@invalid.test` / `probe-auth2@invalid.test`）注册时会自动建
workspace，进而生成了 2 条 **active** 网关配置（id 34/35）。

已在一个事务里删净（`.invalid.test` 是 RFC 2606 保留测试域，不可能对应真人）：

```
DELETE 2  llm_gateway_configs
DELETE 2  workspaces
DELETE 2  users
```

删前核对过这些账号名下**没有任何业务数据**
（email_accounts / emails / email_folders / email_ops_log / user_settings 全为 0），
删后复查残留 = 0。

## 需要你决定的事

1. **补上 `POCKET_LLM_GATEWAY_API_KEY` 的部署配置**（目前 `deploy/` 与
   `.env.example` 里都没有这个键）。你此前给过 key，但**没有任何部署路径会把它
   传进进程**——这正是"配了等于没配"的原因。
2. **`ws_user-admin_`（id 3，尾部多一个下划线）** 这个 workspace 是拼写错误残留，
   且仍 active。要不要连同它那 30 条历史行一起清理，等你点头（属删数据）。
3. 代码层面我可以补两处防御（都需你点头）：
   - `SaveConfig` 在 `APIKey == ""` 且该 workspace 已有带 key 的历史行时**拒绝**降级
   - `EnsureLLMGatewayDefaults` 的自愈分支在 `def.APIKey == ""` 时**只 log 不覆写**
     （宁可保持"读不出来"也不要毁掉配置）
