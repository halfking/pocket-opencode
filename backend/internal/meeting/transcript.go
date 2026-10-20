// internal/meeting/transcript.go —— §937：转写**尾读**与**定向更新**的唯一口径。
//
// ⚠ 为什么这两个口径必须**只有一份**（§80 的教训：「同一段逻辑抄两遍 ⇒
//
//	  同一缺陷必然出现在两处」）：
//
//		尾读：PG 版用 SQL（substr 按**字符**定位），内存版用 Go 切片
//		⇒ ⇒ 两边的「字符」必须同义、边界必须同形 ⇒ 否则同一个 fromRune 在两个
//		  store 下给出不同的尾部 ⇒ ⇒ dev 环境（内存版）测不出生产（PG 版）的问题。
//
//		定向更新：SET 子句的拼法只有这一份 ⇒ ⇒ 「SQL 里绝不出现 transcript」这条
//		  不变式可以被判据直接验，而不是靠人肉读 SQL。
//
// ─────────────────────────────────────────────────────────────────────────
// §937 的两个缺陷（都属「服务端每次操作的成本 ∝ 会议长度，而不是 ∝ 新增量」）：
//
//	D1（读放大）：/api/meetings/{id}/analyze 每轮轮询都 GetScoped 拉**整份转写**
//	  ⇒ 每轮 DB→进程的字节数 ∝ 全文 ⇒ 按 P 秒轮询 ⇒ 累计 ∝ 时长²/P
//	  ⇒ ⇒ 这是 §924（客户端整场重发）的**服务端孪生**。
//
//	D2（写放大 + 丢失更新窗口）：只改 title/status 的两个写入点走的是
//	  「GetScoped（读整行）+ UpdateScoped（写整行）」⇒ 一场长会议每改一次标题
//	  就把**整份转写**重写一遍 ⇒ 写放大 ∝ 时长²；且「读-改-写整行」会把
//	  **并发写入的新转写覆盖回旧值**（lost update）。
package meeting

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// TranscriptTail 从整份转写里切出「第 fromRune 个字符起」的尾部，
// 并给出全文的字符数。
//
// ⚠ 按 **rune（字符）** 而不是字节切：中文一字 3 字节，按字节切会落在字的
//
//	中间 ⇒ 产出非法 UTF-8（同文件 truncStr 记的是同一个坑在**写库**方向的后果）。
//	PG 版用 substr(transcript, n) —— PG 的 substr 按**字符**定位 ⇒ 两边同义。
//
// ⚠ 边界口径（两个实现必须一致）：
//
//	fromRune <= 0        ⇒ 整份转写（fromRune=0 是「从头」）
//	fromRune >= 字符数   ⇒ 空串（**不报错**：游标越过末尾是「没有新增」，
//	                       不是「会议不存在」）
//
// ⚠ 这个函数是 O(全文) 的**扫描**（要找第 N 个字符的字节偏移就得走过前 N 个字符）。
//
//	它换来的是「**不复制**前 N 个字符」⇒ 内存占用从 O(全文) 降到 O(新增)。
//	PG 版同理：`char_length` 与 `substr` 都是 DB 进程内的 O(全文) 扫描，
//	但**不产生进程间搬运**（这才是长会议真正烧掉的东西）。
func TranscriptTail(transcript string, fromRune int) (tail string, totalRunes int) {
	totalRunes = utf8.RuneCountInString(transcript)
	if fromRune <= 0 {
		return transcript, totalRunes
	}
	if fromRune >= totalRunes {
		return "", totalRunes
	}
	seen := 0
	for idx := range transcript {
		if seen == fromRune {
			return transcript[idx:], totalRunes
		}
		seen++
	}
	return "", totalRunes
}

// buildMetaUpdateSQL 生成「只改 title / status」的 UPDATE 语句与参数。
//
// ⚠ **返回的 SQL 绝不包含 transcript** —— 这条不变式就是本函数存在的全部理由
//
//	（§937 的 D2）。判据直接断言它（见 transcript_test.go 的 SQL 形状守卫），
//	而不是靠人肉读 SQL。
//
// ⚠ nil = 该列**不动**（与 notes.NotePatch 的约定一致：「显式空串清空，
//
//	nil 表示留着不动」）。两个调用点都不会传「空标题」：
//	refine 只在 meta.Title 非空时改标题，create 的标题已被兜底成「未命名会议」。
//
// ⚠ updated_at 永远盖时间戳（与 UpdateScoped 同口径）：哪怕两列都没改，
//
//	这次写入也算一次「动了这条行」⇒ 增量同步要能看到它。
//
// ⚠ 纯函数（now 由调用方传入）⇒ 判据可以逐字断言 SQL 形状。
func buildMetaUpdateSQL(id, ownerID, workspaceID string, title, status *string, now time.Time) (string, []any) {
	args := []any{id, ownerID, workspaceID}
	sets := make([]string, 0, 3)
	if title != nil {
		args = append(args, *title)
		sets = append(sets, fmt.Sprintf("title=$%d", len(args)))
	}
	if status != nil {
		args = append(args, *status)
		sets = append(sets, fmt.Sprintf("status=$%d", len(args)))
	}
	args = append(args, now)
	sets = append(sets, fmt.Sprintf("updated_at=$%d", len(args)))
	q := fmt.Sprintf(
		"UPDATE meetings SET %s WHERE id=$1 AND owner_id=$2 AND workspace_id=$3",
		strings.Join(sets, ", "))
	return q, args
}
