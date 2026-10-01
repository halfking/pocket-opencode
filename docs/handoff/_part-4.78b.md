
### 4.78.8 收尾：并入并发提交后重建，APK 终于可归因

推送前 `ls-remote` 发现并发会话已把 `fa8078f`（email 功能）推上 main，
`c3b71df` 是它的祖先、且**与我的文件零重叠**（他们另建了 handoff 文档），
所以干净合并（`71fa8a7`），没有取舍任何一侧的改动。

但合并带进来**大量前端改动**（email views 等），于是刚建的 APK 立刻又不对应 HEAD
—— 正是 BUG-V2 要防的那件事。最终重建并复核：

```
commit  : 71fa8a730118d85590bd7f80ee0a976098db60d0 (dirty=0)
sha256  : 2B673E8EFD8F8FDBB0315514CE9E9E1848164F677DC9F79EF144E8429F9F3EF1
bundle  : dist\assets\index-CldiQnLq.js   (哈希变了 ⇒ email 前端确实进了包)
```

`dirty=0` 是关键：这份 APK 现在能落到一个**具体且干净的 commit** 上。
设备一回来即可直接 `adb install -r -g` 跑 `tasks-crud.yaml`。

### 4.78.9 顺带记一条环境事实：GitHub SSH 代理端口翻转了

本轮 `git fetch/push` 一度全部失败（`Could not read from remote repository`）。
根因**不在仓库**：本机代理进程从 **7900 挪回了 7897**，而 `~/.ssh/config` 的
`ProxyCommand` 仍硬编码 `-H 127.0.0.1:7900`。

| 端口 | 状态 |
|---|---|
| 7900 | **未监听**（ssh config 里写的就是它） |
| 7897 | 监听中（pid 15324） |

⇒ 单次命令覆盖即可（**没有改用户的全局 ssh config**，避免影响并发会话）：

```powershell
$env:GIT_SSH_COMMAND='ssh -o ProxyCommand="C:/Progra~1/Git/mingw64/bin/connect.exe -H 127.0.0.1:7897 %h %p" -o ServerAliveInterval=15 -o ServerAliveCountMax=20'
```

这类故障的表现极具误导性：`git ls-remote` 失败会让人以为是权限/密钥问题，
实际上只是本机代理换了端口。**下次再遇到先查端口在不在监听，别去动密钥。**
