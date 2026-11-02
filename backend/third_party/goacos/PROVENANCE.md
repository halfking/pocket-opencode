# goacos SDK 仓内副本 —— 溯源

本目录是 `github.com/halfking/goacos` 的 `sdk/registry` 包的**逐字节副本**。
它由 `goacos/scripts/vendor-sdk-copy.sh` 生成，请勿手工编辑。

## 副本来自哪里

| 项 | 值 |
| --- | --- |
| goacos 仓 | `/Users/niuer/workspace/ai-native-tools/goacos` |
| 消费方仓 | `/Users/niuer/workspace/ai-native-tools/openpocket/backend` |
| ref | `4db8a14d35ed86a34e97def58435834815085a18` |
| commit | `4db8a14d35ed86a34e97def58435834815085a18` |
| 提交时间 | 2026-10-29T01:56:44+08:00 |
| 提交标题 | docs: 2 处订正 —— 环境变量表 ADMIN_PASSWORD → GOACOS_ADMIN_PASSWORD（裸名不生效） |
| 发布状态 | **未发布**（该 commit 上没有任何 tag；它比最新 tag `v1.2.0` 新 67 个提交） |
| 嵌套模块 go 指令 | `1.27.1`（取自**消费方** go.mod；上游是 `1.27.2`） |

## 逐文件 sha256

| 文件 | sha256 |
| --- | --- |
| `sdk/registry/README.md` | `10cc9a810f86a28d39e891a1ceca52896d13e7af4a4ed1ae1d5dbffbeab0ca87` |
| `sdk/registry/client.go` | `5c385c88f3344670e1d57d97074d0ca0eff4e6345c234d38f4f012a2e8aca8d4` |
| `sdk/registry/discover.go` | `7dc8136cf03f9cecd891a387d47bedd941d9de873b7616eb6ed722317719f2af` |
| `sdk/registry/doc.go` | `fef4270a4729d589689f41410b5684b0bd3b67af10b30c29df759788d1713278` |
| `sdk/registry/service.go` | `430d67d82402d588c056689a7ba0acae72854686949ebebce2a6ad62e528452d` |

## 重新生成

```bash
cd /Users/niuer/workspace/ai-native-tools/goacos && scripts/vendor-sdk-copy.sh /Users/niuer/workspace/ai-native-tools/openpocket/backend 4db8a14d35ed86a34e97def58435834815085a18
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
