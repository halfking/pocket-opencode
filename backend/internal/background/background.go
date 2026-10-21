// Package background 起后台 goroutine，并把 panic 变成一条可定位的日志。
//
// # 为什么需要它（§949）
//
// Go 的 runtime 语义：**任何 goroutine 里未被 recover 的 panic 都会终止整个进程**
// —— 不是只终止那个 goroutine。
//
// 本仓原本只有两处 panic 边界：
//
//   - internal/server/middleware.go 的 recoveryMiddleware —— 只覆盖**请求 goroutine**；
//   - internal/scheduledtask/scheduler.go 的 executeSafely —— 只覆盖**任务执行器**。
//
// ⇒⇒⇒ 从请求里 `go` 出去的后台 goroutine **在那个边界之外**：
// panic 从另一个 goroutine 逃逸，请求中间件的 `defer recover()` 抓不到
// ⇒⇒⇒ 一次 panic = 整个 pocketd 进程死
// ⇒⇒⇒ ⇒ 正在录的会议、正在同步的邮件、全部 WebSocket 连接一起没。
//
// # 口径
//
//	后台 goroutine **必须**经由本包启动（Go / Go1 / Go2），
//	或自己带**顶层**的 `defer recover()`。
//
//	⇒ panic 被降级成一条 `[PANIC]` 日志（带栈）⇒ 进程活着，别的任务不受影响。
//
// # 为什么 recover 之后还要打栈
//
//	没有栈的 panic 日志等于把崩溃换成了「一句没头没尾的话」
//
// ⇒⇒⇒ 那正是本仓这一串审计（§945 起）在消灭的东西：**失败不许无声**。
//
// # 已知代价（如实登记，不藏）
//
//	recover 会**掩盖**「panic 之后锁还锁着」这类问题：进程活着但可能死锁。
//	⇒ 本仓已接受这个代价（请求中间件早就在 recover HTTP handler 的 panic），
//	⇒ 因为对一台「会议要录几小时」的随身设备来说，**进程死**比**某个任务卡住**更糟。
//	⇒ ⚠ 详见设计文档 §949.4 的待拍板登记。
//
// # 参数原样传递（为什么不是闭包）
//
//	Go1 / Go2 把参数**原样**传给目标函数（不是靠闭包捕获）
//	⇒ ⇒⇒ 改造前后的**捕获语义完全一致**
//	⇒ ⇒⇒ 尤其保住了「把循环变量当参数传进去」这种写法
//	⇒ ⇒⇒ ⇒ 不会因为「改成闭包」而改变 Go 版本相关的循环变量语义。
package background

import (
	"log"
	"runtime/debug"
)

// Go 起一个后台 goroutine（目标函数无参）。
//
//	name 只用于日志定位（哪个后台任务炸了）⇒ 不许留空。
func Go(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[PANIC] background goroutine %q panicked: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn()
	}()
}

// Go1 起一个后台 goroutine，把一个参数原样传给目标函数。
func Go1[A any](name string, fn func(A), a A) {
	Go(name, func() { fn(a) })
}

// Go2 起一个后台 goroutine，把两个参数原样传给目标函数。
func Go2[A, B any](name string, fn func(A, B), a A, b B) {
	Go(name, func() { fn(a, b) })
}

// Go3 起一个后台 goroutine，把三个参数原样传给目标函数。
func Go3[A, B, C any](name string, fn func(A, B, C), a A, b B, c C) {
	Go(name, func() { fn(a, b, c) })
}

// Go4 起一个后台 goroutine，把四个参数原样传给目标函数。
func Go4[A, B, C, D any](name string, fn func(A, B, C, D), a A, b B, c C, d D) {
	Go(name, func() { fn(a, b, c, d) })
}
