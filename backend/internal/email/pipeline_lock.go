package email

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DailyPipelineLockKey 是每日定时流水线的跨进程锁键。
//
// 用 hashtextextended 而不是自己算 hash，与本包既有的 advisory 用法保持一致
// （见 store.go 的 vacation 领取锁），避免两处算出不同的 int64。
const DailyPipelineLockKey = "email:daily-pipeline"

// DailyPipelineLockState 描述取每日定时流水线跨进程锁的结果。
//
// 三态而不是两态，是因为「拿不到锁」有两种性质完全相反的成因，调用方
// 必须区别对待：
//
//   - Busy：另一个 pocketd 实例正持有锁并正在跑这一轮。**跳过**就是修复意图。
//   - Unavailable：锁机制本身不可用（没有连接池 / 取连接失败 / 查询报错）。
//     这时若也跳过，一次配置错误或数据库抖动就会让每日流水线永久静默，
//     而且日志里只有一行"跳过"，看不出是"别人在跑"还是"锁坏了"。
type DailyPipelineLockState int

const (
	// DailyPipelineLockAcquired 成功取到锁，调用方必须在返回前调用 release。
	DailyPipelineLockAcquired DailyPipelineLockState = iota
	// DailyPipelineLockBusy 锁被别的实例持有，调用方应当跳过本轮。
	DailyPipelineLockBusy
	// DailyPipelineLockUnavailable 锁机制不可用，调用方应当降级为照常执行。
	DailyPipelineLockUnavailable
)

func (s DailyPipelineLockState) String() string {
	switch s {
	case DailyPipelineLockAcquired:
		return "acquired"
	case DailyPipelineLockBusy:
		return "busy"
	case DailyPipelineLockUnavailable:
		return "unavailable"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// TryLockDailyPipeline 尝试取每日定时流水线的跨进程互斥锁。
//
// 为什么需要它：emailPipelineMu（server 包内）与 scheduler 的 sync.Once
// 都是**进程内**互斥。三个 pocketd 实例共享同一个 PG 库、各自排了同一点的
// 定时任务时，它们会同时跑同一轮流水线。实测的伤害不是"慢一点"，而是重复
// 推送：重要邮件提醒在推送循环跑完之后才写 MarkEmailsNotified 标记，而
// notifications 表除主键外没有唯一约束，于是同一封邮件被推 N 份通知。
//
// 为什么用 pg_try_advisory_lock（会话级）而不是 pg_advisory_xact_lock（事务级）：
// 一轮流水线最长 30 分钟，事务级锁要开一个 30 分钟的事务把连接钉住，而
// 流水线自身正用同一个连接池跑几十条查询——有把池子耗尽、把自己死锁的风险。
// 会话级锁只占**一条**连接，且用 Try 语义：取不到立刻返回，不排队。
//
// release 的契约：非 nil 时必须恰好调用一次，且必须在流水线结束之后。
func (s *Store) TryLockDailyPipeline(ctx context.Context) (release func(), state DailyPipelineLockState, err error) {
	if s == nil || s.pool == nil {
		// 没有连接池（单测构造器、纯离线部署）。不是故障，是"这套机制用不上"。
		return nil, DailyPipelineLockUnavailable, nil
	}
	// Acquire 拿到的是一条独占连接。advisory lock 绑在会话上，所以它必须
	// 整个生命周期独占这一条，不能放回池里给流水线的普通查询用。
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, DailyPipelineLockUnavailable, fmt.Errorf("email: acquire lock connection: %w", err)
	}
	var got bool
	if err := conn.QueryRow(ctx,
		`SELECT pg_try_advisory_lock(hashtextextended($1, 0))`, DailyPipelineLockKey,
	).Scan(&got); err != nil {
		conn.Release()
		return nil, DailyPipelineLockUnavailable, fmt.Errorf("email: try daily pipeline lock: %w", err)
	}
	if !got {
		conn.Release()
		return nil, DailyPipelineLockBusy, nil
	}
	return func() { releaseDailyPipelineLock(conn) }, DailyPipelineLockAcquired, nil
}

// releaseDailyPipelineLock 解锁并归还连接。
//
// 关键在失败分支：advisory lock 是**会话级**的。若带着锁把连接 Release 回池子，
// 下一个借用这条连接的查询会继承这把锁——而那可能是几小时后的另一轮流水线，
// 于是每日流水线被永久锁死，且没有任何错误日志指向真正的原因。
// 所以解锁失败时必须销毁连接（服务端随之自动释放会话锁），绝不归还。
func releaseDailyPipelineLock(conn *pgxpool.Conn) {
	if conn == nil {
		return
	}
	// 不能用调用方的 ctx：流水线那轮可能已经超时/取消，而解锁必须执行。
	unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var unlocked bool
	err := conn.QueryRow(unlockCtx,
		`SELECT pg_advisory_unlock(hashtextextended($1, 0))`, DailyPipelineLockKey,
	).Scan(&unlocked)
	if err == nil && unlocked {
		conn.Release()
		return
	}
	// 兜底：让连接连同它持有的会话锁一起消失。
	raw := conn.Hijack()
	_ = raw.Close(context.Background())
}
