// Package repohygiene 存放**跨仓库**的卫生卡口（不针对某个业务域）。
//
// 这里的检查不属于任何业务包：它们扫的是整个 git 工作树，而不是某个
// store / server / task 的内部不变量。之所以做成一个 Go 包而不是
// `frontend/scripts/check-*.mjs`，是因为后端 CI（.github/workflows/backend.yml）
// 会跑 `go test -race ./...`，把它放在这里意味着**每次 CI 必然执行**；
// 放进 npm gates 则只有本地手动跑才会执行——而本轮审计已经看到过一次
// 「护栏写好了但没有任何东西会执行它」的教训。
package repohygiene
