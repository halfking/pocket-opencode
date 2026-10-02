// Package rss — digest_service.go
//
// 日报调度：每天在指定时刻为**每一个有订阅的用户作用域**生成日报，并交给
// 通知通道（app 内通知 / 通知中心）。通知是"每天收到一份摘要"的落点，
// 没有它就只是应用内一个要自己记得去看的页面。
package rss

import (
	"context"
	"errors"
	"log"
	"sort"
	"sync"
	"time"
)

// DigestNotifier 是一条日报的投递出口（生产实现走 notifycenter）。
// 抽象成接口是为了让调度逻辑可测，也为了不让 rss 包反向依赖 server 层。
type DigestNotifier interface {
	NotifyDigest(ctx context.Context, sc Scope, d *Digest) error
}

// DigestServiceOptions 控制日报调度。
type DigestServiceOptions struct {
	// At 是每天生成时刻（本地时区的小时/分钟）。
	AtHour   int
	AtMinute int
	// Location 用于解释 AtHour/AtMinute。
	Location *time.Location
	// Opts 是传给 BuildDigest 的默认选项。
	Opts DigestOptions
	// StartupRunOnStart 为真时，启动后立即补一次（覆盖进程重启跨过计划时刻的情况）。
	StartupRunOnStart bool
	// Now 可注入，便于测试。
	Now func() time.Time
}

// DigestService 是日报调度器。
type DigestService struct {
	store     DigestStore
	notifier  DigestNotifier
	fallback  Scope
	opt       DigestServiceOptions
	stop      chan struct{}
	stopOnce  sync.Once
	wg        sync.WaitGroup
	startOnce sync.Once
}

// DigestStore 是 DigestService 需要的存储面。
type DigestStore interface {
	ListActiveScopes(context.Context) ([]Scope, error)
	BuildDigest(context.Context, Scope, time.Time, DigestOptions) (*Digest, error)
	SaveDigest(context.Context, Scope, *Digest) (*Digest, error)
}

// NewDigestService 构造调度器。fallback 是存储层列不出作用域时使用的兜底
// 作用域（例如本地单用户部署）。
func NewDigestService(store DigestStore, notifier DigestNotifier, fallback Scope, opt DigestServiceOptions) *DigestService {
	if opt.AtHour < 0 || opt.AtHour > 23 {
		opt.AtHour = 8
	}
	if opt.AtMinute < 0 || opt.AtMinute > 59 {
		opt.AtMinute = 30
	}
	if opt.Location == nil {
		opt.Location = time.Local
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	return &DigestService{store: store, notifier: notifier, fallback: fallback, opt: opt, stop: make(chan struct{})}
}

func (d *DigestService) now() time.Time { return d.opt.Now().In(d.opt.Location) }

// Scopes 返回本次要生成日报的全部作用域。
func (d *DigestService) Scopes(ctx context.Context) []Scope {
	scopes, err := d.store.ListActiveScopes(ctx)
	if err != nil {
		log.Printf("[rss] digest: list scopes: %v", err)
		scopes = nil
	}
	if len(scopes) == 0 && d.fallback.valid() {
		scopes = []Scope{d.fallback}
	}
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].UserID != scopes[j].UserID {
			return scopes[i].UserID < scopes[j].UserID
		}
		return scopes[i].WorkspaceID < scopes[j].WorkspaceID
	})
	return scopes
}

// RunOnce 为每个作用域生成（并保存）指定日期的日报，然后通知。
// 返回成功生成的份数。单个作用域失败不会中断其它作用域。
func (d *DigestService) RunOnce(ctx context.Context, day time.Time) (int, error) {
	if d == nil || d.store == nil {
		return 0, ErrStoreUnavailable
	}
	opts := d.opt.Opts
	opts.Now = d.opt.Now
	generated := 0
	var firstErr error
	for _, sc := range d.Scopes(ctx) {
		dg, err := d.store.BuildDigest(ctx, sc, day, opts)
		if err != nil {
			log.Printf("[rss] digest build %s/%s: %v", sc.UserID, sc.WorkspaceID, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		saved, err := d.store.SaveDigest(ctx, sc, dg)
		if err != nil {
			log.Printf("[rss] digest save %s/%s: %v", sc.UserID, sc.WorkspaceID, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		generated++
		if d.notifier != nil {
			if err := d.notifier.NotifyDigest(ctx, sc, saved); err != nil {
				// 通知失败不影响日报本身已经生成并落库。
				log.Printf("[rss] digest notify %s/%s: %v", sc.UserID, sc.WorkspaceID, err)
			}
		}
	}
	return generated, firstErr
}

func (d *DigestService) loop(ctx context.Context) {
	defer d.wg.Done()
	if d.opt.StartupRunOnStart {
		if _, err := d.RunOnce(ctx, d.now()); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("[rss] digest startup run: %v", err)
		}
	}
	for {
		next := d.nextRun(d.now())
		timer := time.NewTimer(time.Until(next))
		select {
		case <-timer.C:
			if _, err := d.RunOnce(ctx, d.now()); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("[rss] digest run: %v", err)
			}
		case <-ctx.Done():
			timer.Stop()
			return
		case <-d.stop:
			timer.Stop()
			return
		}
	}
}

// nextRun 返回下一个计划时刻。已经过了今天的点，就顺延到明天。
func (d *DigestService) nextRun(from time.Time) time.Time {
	candidate := time.Date(from.Year(), from.Month(), from.Day(), d.opt.AtHour, d.opt.AtMinute, 0, 0, d.opt.Location)
	if !candidate.After(from) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

// Start 启动后台循环。
func (d *DigestService) Start(ctx context.Context) error {
	if d == nil || d.store == nil {
		return ErrStoreUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	d.startOnce.Do(func() { d.wg.Add(1); go d.loop(ctx) })
	return nil
}

// Stop 停止后台循环。
func (d *DigestService) Stop() {
	if d == nil {
		return
	}
	d.stopOnce.Do(func() { close(d.stop) })
	d.wg.Wait()
}
