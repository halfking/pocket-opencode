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

// s943PlantedAt 构造「别人的墓碑」的时刻。
//
// ⚠⚠ §964：它**显式带一个确定的亚微秒尾**（先 Truncate 到微秒，再 +123ns）。
//
//	为什么：墓碑那条判据用的是 `pgTimeEqual`（±1µs **容差带**）。
//	若被比较的两个值**逐字相同**，那条带比的就是「0 vs 0」
//	⇒ **把容差收到 0 也不会有人发现** ⇒ 判据在**最需要它的平台**上反而没牙。
//	⚠ 而这不是假设：macOS 上实测 `time.Now()` 的纳秒尾 **2000/2000 全为 0**
//	（µs 对齐的时钟）⇒ 不加干预时 PG 往返差**恰为 0**。
//	⇒ 先 Truncate 到微秒（让基准确定）再 +123ns
//	⇒ PG 的 round-half-to-even 在 `.123` 上是**舍去** ⇒ 往返差**恒为 123ns**
//	⇒ 容差带在**任何**平台上都**非空** ⇒ 它是**真的被用上了**。
//
// ★ 为什么抽成函数：**夹具与判据共用同一份构造** ⇒
//
//	「夹具被改回无亚微秒尾」这件事**立刻被 transcript_test.go 里那条判据逮住**
//	（否则这个约定只写在注释里 ⇒ 没人守 ⇒ 迟早被改掉而没有任何判据变红）。
func s943PlantedAt() time.Time {
	return time.Now().Add(-2 * time.Hour).Truncate(time.Microsecond).Add(123 * time.Nanosecond)
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
			//
			// ⚠⚠ §964：时刻由 s943PlantedAt() 统一构造 ⇒ 夹具与判据**共用同一份**
			//   ⇒ 「夹具被改回无亚微秒尾」这件事**立刻被那条判据逮住**
			//   （详见 s943PlantedAt 的注释与 transcript_test.go 里那条判据）。
			plantedAt := s943PlantedAt()
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
			// ⚠ PG 的 timestamptz 精度是**微秒**，而 plantedAt 是内存里的**纳秒**值
			//   ⇒ 直接比字面必然差几百纳秒（CI 实测差 7198ns）
			//   ⇒ 这条判据要比的是「同一个时刻在 PG 精度下是否相等」
			//   （容差口径见 transcript_test.go 的 pgTimeEqual 与它的负控）。
			if !pgTimeEqual(at, plantedAt) {
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
