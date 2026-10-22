package meeting

// §943 判据（D3）：墓碑（meeting_tombstones）的**归属守卫**。
//
// ⚠ 缺陷（修复前实测）：墓碑的 `ON CONFLICT (id) DO UPDATE` 的 SET 列表里
//   **有** owner_id 与 workspace_id ⇒ ⇒ ⇒ 撞上一个**别的作用域**的 id 时，
//   它会把**那一行墓碑整个搬走**（连归属一起换成提交者的）
//   ⇒ ⇒ ⇒⇒ 后果是**受害者自己**的墓碑清单里**再也没有**那条 id
//   （墓碑清单按 owner_id + workspace_id 过滤）
//   ⇒ ⇒ ⇒⇒⇒ **已删的会议在增量同步里「复活」**（客户端以为服务器上还有它）
//   ⇒ ⇒⇒⇒ 而且攻击者的租户里**多出一行**别人的墓碑（跨用户的删除记录被注入）。
//
// ⚠ 为什么这里**不**像会议本体那样「换号」：
//
//	墓碑的 id **必须等于被删会议的 id** ⇒ 换号会写出一条**指向不存在会议**的
//	墓碑 ⇒ ⇒ ⇒ 那是错的 ⇒ ⇒⇒ 「绝不覆盖别人的行」这条**不变量**是共通的，
//	但**不成立之后怎么办**要按表的语义分别定（本体换号、墓碑报错）。
//
// ⚠ 两个实现都要判（内存版当时**也**没有守卫 ⇒ 它也会搬走别人的墓碑）。
//
// ⚠ 需要真 PG：没有 PG 时本用例会 skip（退出码仍是 0）⇒ ⇒ 变异批次用
//   forbid_skip=True 把「被 skip」变成失败。
import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	s943TombID   = "meeting-s943-tombstone"
	s943TombSelf = "user-s943-self"
	s943TombOth  = "user-s943-other"
	s943TombWs   = "ws-s943-tomb"
)

// s943TombCase 是一个实现 + 「造一条别人的墓碑」/「读回墓碑」的能力。
//
// ⚠ 为什么要「造」：守卫在**正常调用链**上几乎不可达（DELETE 已按归属限定）
// ⇒ ⇒ ⇒ 只跑正常路径**证明不了**它 ⇒ ⇒⇒ ⇒ 必须**造出**那个不一致状态
// （墓碑属于别人、会议属于自己）⇒ ⇒⇒ ⇒ 守卫才真正被触发。
type s943TombCase struct {
	name  string
	store MeetingStore
	plant func(t *testing.T, id, owner, ws string, at time.Time)
	read  func(t *testing.T, id string) (owner, ws string, at time.Time, ok bool)
}

func s943TombCases(t *testing.T) []s943TombCase {
	t.Helper()
	pgStore, cleanup := newPGTestEnv(t)
	t.Cleanup(cleanup)
	mem := NewStore()
	return []s943TombCase{
		{
			name:  "内存版",
			store: mem,
			plant: func(t *testing.T, id, owner, ws string, at time.Time) {
				mem.tombstones[id] = meetingTombstone{ownerID: owner, workspaceID: ws, at: at}
			},
			read: func(t *testing.T, id string) (string, string, time.Time, bool) {
				tm, ok := mem.tombstones[id]
				if !ok {
					return "", "", time.Time{}, false
				}
				return tm.ownerID, tm.workspaceID, tm.at, true
			},
		},
		{
			name:  "PG版",
			store: pgStore,
			plant: func(t *testing.T, id, owner, ws string, at time.Time) {
				_, err := pgStore.pool.Exec(context.Background(),
					`INSERT INTO meeting_tombstones (id, owner_id, workspace_id, deleted_at)
					 VALUES ($1,$2,$3,$4)`, id, owner, ws, at)
				if err != nil {
					t.Fatalf("造墓碑失败: %v", err)
				}
			},
			read: func(t *testing.T, id string) (string, string, time.Time, bool) {
				var o, w string
				var at time.Time
				err := pgStore.pool.QueryRow(context.Background(),
					`SELECT owner_id, workspace_id, deleted_at FROM meeting_tombstones WHERE id=$1`, id).
					Scan(&o, &w, &at)
				if errors.Is(err, pgx.ErrNoRows) {
					return "", "", time.Time{}, false
				}
				if err != nil {
					t.Fatalf("读墓碑失败: %v", err)
				}
				return o, w, at, true
			},
		},
	}
}

// sameStoredInstant 判断两个时刻在 **PG 的存储精度（微秒）** 下是不是同一个。
//
// ⚠⚠ §963 实测（CI/Linux 上这条判据红，本机 macOS 上恒绿）：
//
//	PG 的 `timestamptz` **物理上只能存微秒** ⇒ 种进去的纳秒尾数会被**四舍五入**
//	（实测 .7µs 进位 / .3µs 舍去 / 恰好 .5µs 舍去 ⇒ round-half-to-even）。
//	⇒ 而 `time.Now()` 的**平台时钟粒度不同**：macOS 常是微秒级（尾数恒为 0），
//	  Linux 是纳秒级（尾数非 0）⇒ 按纳秒等值比，Linux 必红、macOS 必绿。
//	⇒ 那是「量具依赖环境属性」（§960.7.9 同族），不是被测行为变了。
//	⇒ 所以判据按**存储精度**比：容差 1µs（实测四舍五入的最大误差是 0.5µs）。
//
// ⚠ 这**不是**把判据放宽到「什么都算过」：
//
//	容差只有 1µs ⇒ 差 1ms 的真实改动照样被抓（见下面的负控用例）。
func sameStoredInstant(a, b time.Time) bool {
	d := a.Sub(b)
	return d <= time.Microsecond && d >= -time.Microsecond
}

// TestS943SameStoredInstantTolerance 钉住上面那个容差的**确切边界**：
// 负控 = 「差 1ms / 1µs 以外一律判为不同」。
func TestS943SameStoredInstantTolerance(t *testing.T) {
	base := time.Date(2026, 10, 7, 15, 4, 0, 0, time.UTC)
	cases := []struct {
		name string
		d    time.Duration
		want bool
	}{
		{"完全相同", 0, true},
		{"亚微秒尾数（PG 会四舍五入掉的那部分）", 123 * time.Nanosecond, true},
		{"刚好 1µs", time.Microsecond, true},
		{"超过 1µs", time.Microsecond + time.Nanosecond, false},
		{"差 1ms（真实改动，必须被抓）", time.Millisecond, false},
		{"差 1s（真实改动，必须被抓）", time.Second, false},
	}
	for _, c := range cases {
		if got := sameStoredInstant(base.Add(c.d), base); got != c.want {
			t.Errorf("%s：sameStoredInstant(+%v) = %v，期望 %v", c.name, c.d, got, c.want)
		}
	}
}

// TestS943TombstoneGuardRefusesToMoveAnotherScopesTombstone 不得搬走别人的墓碑。
//
// ⚠ 三条腿缺一不可：
//
//	① 删除必须**失败**（报 ErrMeetingTombstoneScopeConflict）—— 不是「成功但换号」
//	② 别人的那条墓碑必须**一字未改**（归属 + 时间）
//	③ 会议**必须还在**（不得出现「会议没了、墓碑没写成」的半处理状态）
func TestS943TombstoneGuardRefusesToMoveAnotherScopesTombstone(t *testing.T) {
	for _, tc := range s943TombCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			// 别人的墓碑（早于这次删除存在）。
			// ⚠ §963：**显式**带上亚微秒尾数。
			//   平台的时钟粒度不同（macOS 的 time.Now() 常是微秒级、Linux 是纳秒级）
			//   ⇒ 不显式带上，这条判据在 macOS 上恒绿、在 Linux CI 上恒红
			//   ⇒ 那是「量具依赖环境属性」，不是被测行为变了。
			plantedAt := time.Now().Add(-2 * time.Hour).Add(123 * time.Nanosecond)
			tc.plant(t, s943TombID, s943TombOth, s943TombWs, plantedAt)

			// 自己名下有一场**同 id** 的会议。
			if _, err := tc.store.CreateScoped(CreateMeetingRequest{
				ID: s943TombID, Title: "自己的会议",
			}, s943TombSelf, s943TombWs); err != nil {
				t.Fatalf("建会失败: %v", err)
			}

			err := tc.store.DeleteScoped(s943TombID, s943TombSelf, s943TombWs)
			if err == nil {
				t.Fatalf("删除成功了 ⇒ 它把别人的墓碑搬走了（D3：受害者的已删会议会复活）")
			}
			if !errors.Is(err, ErrMeetingTombstoneScopeConflict) {
				t.Fatalf("错误不是 ErrMeetingTombstoneScopeConflict：%v", err)
			}

			// ② 别人的墓碑必须一字未改。
			owner, ws, at, ok := tc.read(t, s943TombID)
			if !ok {
				t.Fatalf("别人的墓碑不见了 ⇒ 它被删了（应当是保留原样）")
			}
			if owner != s943TombOth || ws != s943TombWs {
				t.Errorf("别人的墓碑被搬走了：owner=%q ws=%q（want %q / %q）", owner, ws, s943TombOth, s943TombWs)
			}
			// ⚠ 按 **存储精度** 比（PG 只存微秒）⇒ 见 sameStoredInstant 的注释。
			if !sameStoredInstant(at, plantedAt) {
				t.Errorf("别人的墓碑时间被改了：%v（want %v，差 %v）", at, plantedAt, at.Sub(plantedAt))
			}

			// ③ 会议必须还在（没有半处理）。
			if _, err := tc.store.GetScoped(s943TombID, s943TombSelf, s943TombWs); err != nil {
				t.Errorf("会议不见了 ⇒ 出现了「会议没了、墓碑也没写成」的半处理状态: %v", err)
			}
		})
	}
}

// TestS943TombstoneGuardKeepsNormalDeleteWorking **负控**：正常删除必须照常。
//
// ⚠ 它证明「守卫没有把正常路径也挡掉」（守卫若写成「一律不写墓碑」，这条会红）。
func TestS943TombstoneGuardKeepsNormalDeleteWorking(t *testing.T) {
	for _, tc := range s943TombCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.store.CreateScoped(CreateMeetingRequest{
				ID: s943TombID, Title: "自己的会议",
			}, s943TombSelf, s943TombWs); err != nil {
				t.Fatalf("建会失败: %v", err)
			}
			if err := tc.store.DeleteScoped(s943TombID, s943TombSelf, s943TombWs); err != nil {
				t.Fatalf("正常删除失败: %v", err)
			}
			owner, ws, _, ok := tc.read(t, s943TombID)
			if !ok {
				t.Fatalf("墓碑没写进去 ⇒ 增量同步会以为这条会议还在")
			}
			if owner != s943TombSelf || ws != s943TombWs {
				t.Errorf("墓碑归属不对：owner=%q ws=%q（want %q / %q）", owner, ws, s943TombSelf, s943TombWs)
			}
			// 墓碑清单里必须有它（这是「删掉了」的对外承诺）。
			ids := tc.store.DeletedIDsSince(s943TombSelf, s943TombWs, time.Now().Add(-time.Hour))
			found := false
			for _, id := range ids {
				if id == s943TombID {
					found = true
				}
			}
			if !found {
				t.Errorf("墓碑清单里没有 %q（清单=%v）⇒ 增量同步会把已删的会议当成还在", s943TombID, ids)
			}
		})
	}
}
