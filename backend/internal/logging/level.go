// 级别化的 printf 风格辅助函数。
//
// -----------------------------------------------------------------------------
// Purpose:        让**存量 stdlib log 调用点**能带上真实级别。
//                 标准库 log 包没有级别概念——log.Printf 打出来的行
//                 级别一律是 INFO（slog.SetDefault 的接管结果），
//                 于是「ERROR: xxx」这种行在 level 字段上仍是 info，
//                 下游按 level=~"error|fatal" 过滤会**漏掉全部错误事件**。
//                 这几个辅助函数按 printf 语义输出，让级别字段可信。
// Replaces:       (无)
// Replaced-by:    (无)
// Status:         active
//
// Changelog:
//   2026-11-09  v1.0  首版。随 vibe-coding rule 71 落地。
// -----------------------------------------------------------------------------

package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
)

// Errorf 按 printf 语义输出 ERROR 级别日志。
//
// 与 slog.Error 的区别：slog.Error 的可变参数是**键值对**，不吃 %v；
// 存量代码里的 log.Printf("ERROR: x: %v", err) 迁移过来时用本函数，
// 消息文本与参数语义完全不变，只是级别变得可信。
func Errorf(format string, args ...any) {
	slog.Error(fmt.Sprintf(format, args...))
}

// Warnf 按 printf 语义输出 WARN 级别日志。
func Warnf(format string, args ...any) {
	slog.Warn(fmt.Sprintf(format, args...))
}

// Infof 按 printf 语义输出 INFO 级别日志。
func Infof(format string, args ...any) {
	slog.Info(fmt.Sprintf(format, args...))
}

// ⚠ 为什么没有 Panicf：
//
//	存量代码里的 `log.Printf("[PANIC] ...")` 记录的是**被 recover 掉的**
//	background goroutine panic——进程没退出，只是那个任务死了。
//	按 rule 71 §4.1 的口径，FATAL 的 use_for 是「进程即将退出/不可恢复」，
//	而这种 panic 恰恰**没有**让进程退出 ⇒ 归 ERROR（功能失败但服务仍可用）
//	才是对的级别。消息文本里的 "[PANIC]" 前缀保留，运维的 grep 习惯不变。
//
//	真正「进程要退出」的路径用 Fatalf（级别 FATAL + 退出码 1）。

// Fatalf 按 printf 语义输出 FATAL 级别日志并以退出码 1 结束进程。
//
// 与标准库 log.Fatalf 的行为差异只有一个：**级别是 FATAL 而不是 INFO**——
// 进程即将退出属于不可恢复事件，必须能被 level=~"fatal" 抓到。
// 退出码同样是 1，调用点的控制流不变。
func Fatalf(format string, args ...any) {
	// slog.LevelError+4 = FATAL（rule 71 §4.1 的数值表：ERROR 17-20 / FATAL 21-24）。
	slog.Log(context.Background(), slog.LevelError+4, fmt.Sprintf(format, args...))
	os.Exit(1)
}
