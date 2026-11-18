# goacos SDK 仓内副本 —— 溯源

本目录是 `github.com/halfking/goacos` 的 `sdk/registry` 包的**逐字节副本**。
它由 `goacos/scripts/vendor-sdk-copy.sh` 生成，请勿手工编辑。

## 副本来自哪里

| 项 | 值 |
| --- | --- |
| goacos 仓 | `/Users/niuer/workspace/ai-native-tools/tools/goacos` |
| 消费方仓 | `/Users/niuer/workspace/ai-native-tools/client/openpocket/backend` |
| ref | `14d5f75` |
| commit | `14d5f7504dd8b60807c9749665ed35f4f4484b98` |
| 提交时间 | 2026-11-17T17:14:46+08:00 |
| 提交标题 | Merge remote-tracking branch 'origin/main' |
| 发布状态 | **未发布**（该 commit 上没有任何 tag；它比最新 tag `v1.2.0` 新 76 个提交） |
| 嵌套模块 go 指令 | `1.27.1`（取自**消费方** go.mod；上游是 `1.27.2`） |

## 逐文件 sha256

| 文件 | sha256 |
| --- | --- |
| `sdk/registry/README.md` | `83620d69a45e145b9a8b40555349f915eeeb8080ea2400b2edc2df37c6bfeda6` |
| `sdk/registry/app.go` | `c9dc420d77c931cbdc55191b21a0f48ed6c21dd54c9b82f280b3c4713bf5a053` |
| `sdk/registry/client.go` | `5c918cce93e5449db0180fb7d7eee21cace46ab4eed36e9f922c53a37ab94112` |
| `sdk/registry/discover.go` | `7dc8136cf03f9cecd891a387d47bedd941d9de873b7616eb6ed722317719f2af` |
| `sdk/registry/doc.go` | `ad3dff0b0942ef59139cecd87ae9c3f081a80667e2536b50b32725da9f8c8858` |
| `sdk/registry/service.go` | `3c1715c47bed11d0c0cc4119b026d65a866f2699fb23578a7d36fc91790f844f` |

## 重新生成

```bash
cd /Users/niuer/workspace/ai-native-tools/tools/goacos && scripts/vendor-sdk-copy.sh /Users/niuer/workspace/ai-native-tools/client/openpocket/backend 14d5f7504dd8b60807c9749665ed35f4f4484b98
```

ref 必须**显式**给出：默认取 HEAD 会让这份副本在没人注意的时候跟着上游走，
「这个仓用的是哪一版 SDK」就又不可知了。

## 为什么是副本而不是 `go mod vendor`

`service.go`（`ConfigFromEnv` / `Announce`）**不在任何已发布 tag 里** ——
v1.2.0 的 `sdk/registry` 只有 client.go / discover.go / doc.go。
消费方需要的正是这个 SDK 收进去的那五个决定（注册时机、端口口径、地址口径、
注销时机、失败不阻塞服务）；自己重写一遍就是「每个仓各推导一次」，
正是这个 SDK 要消灭的漂移。

## 发 tag 之后怎么收尾

1. owner 在 goacos 仓发 tag（包含 `sdk/registry/service.go` 的那一版）；
2. 消费方 `go.mod` 删掉 `replace github.com/halfking/goacos => ./third_party/goacos`，
   改成 `require github.com/halfking/goacos <新 tag>`；
3. 跑 `go mod vendor`，把 `vendor/github.com/halfking/goacos` 提交进仓；
4. 删掉本目录，并把漂移判据的对照基准从 commit 换成 tag
   （与 acc-go 的 `test_acc_vendor_matches_tag.py` 同一形状）。

在第 1 步完成之前，本副本是该消费方唯一的 SDK 来源 —— 
它不会自动跟着 goacos 仓更新，漂移由 myvpn 的判据盯着。
