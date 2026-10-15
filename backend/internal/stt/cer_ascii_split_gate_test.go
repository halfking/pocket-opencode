// cer_ascii_split_gate_test.go — §481 的**离线**门：
// CER 探针必须把「参考含 ASCII」与「参考纯汉字」分开报，不许只给一个整体数字。
//
// ── 为什么要有这道门（缺陷的完整来龙去脉）─────────────────────────
//
// §474 报出的头条数字是「英文混排 21.3% / 整体 CER 11.36%」。§481 回查原始
// 逐条读数后发现：**这两个数字把两件不同的事塞进了同一个分母。**
//
// 语料是用网关 TTS **从参考原文合成**的，而 TTS 念 ASCII 的方式是「念成汉字」：
//
//	参考（书面）: 我们从1.0升级到2.0,中间要兼容1.5的客户端。
//	音频里实际说: 「我们从一点零升级到二点零，中间要兼容一点五的客户端」
//	ASR 输出:      我们从一点零升级到二点零,中间要兼容一点五的客户端。
//
// ⇒ 对含 ASCII 的语料，ASR 输出「一点零」是**对音频正确**的，
//
//	是**书面参考当成了错的**。同一形状还有 `401`→`四百零一`、`P0`→`P零`。
//
// 量具自证（§481.3）：随请求带的 `prompt` 字段是**固定常量**
// `SimplifiedChineseBiasPrompt`（target.go:140），参考原文只发给了 TTS，
// **没有**作为热词或提示发给 ASR ⇒ 读数不是「ASR 拿到了答案」。
//
// 拆开之后的实测（§474 第二次跑，29 条中 21 条有逐条读数）：
//
//	组            条数  编辑距离  参考字数  CER
//	参考含 ASCII    16      93       455    20.4%
//	参考纯汉字       5       8       153     5.2%
//
// 而纯汉字那 8 个编辑距离**逐条人工核对**后：6 个是标点（缺句号 / 多逗号）、
// 1 个是 `〇`↔`零` 的字形变体、1 个是 `看看`→`看他` 的同音错。
// ⇒ **干净中文语音上，这个模型的真实字错率约 1% 量级。**
//
// ⚠⚠ 但这道门**不是**说「含 ASCII 那档可以忽略」。那档里仍有真错：
//
//	createdAt  → 「一个是顿,tried」      （§474 读到两次，§477 精校改错）
//	changelog  → Channelbug
//	consumerlag→ consolelog
//
// 它们与 §475 三层分析、#29 的前提完全一致：**错的不是中文，是标识符/术语。**
//
// ── 为什么这道门必须离线 ──
//
// 真 CER 探针要 `POCKET_LIVE_GATEWAY=1` + 租户 key + 29×2 次请求。§474.4 已经
// 吃过一次「只能在有凭据的环境里发现」的亏。能守住它的判据不能依赖网关。
//
// ⚠ 这道门守的是「**拆分有没有被报出来**」，不是「CER 高不高」。
// 它对 `20.4%` 还是 `1%` 完全无话可说。
package stt

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestRefHasASCIIClassifiesCorpus 是本门对**被测谓词本身**的判据。
//
// ⚠ 归一化必须走在判定**之前**：全角 `Ｑ３` 的每个 rune 都 ≥ 0x80，
//
//	而它显然是含 ASCII 的 —— 靠 `normalizeCER` 折成半角后才判得出来。
//	所以这两条样本量的是「**有没有先归一化**」，不是别的。
//
// ⛔ 顺带一条如实记录（§481 变异 M4 实测得出）：
//
//	`refHasASCII` 里那个 `r < 0x80 &&` 是**冗余**的 ——
//	后面三个范围检查（'a'-'z' / 'A'-'Z' / '0'-'9'）本身就把 r 限死在 ASCII 区间内。
//	删掉它语义完全不变（M4 因此是**等价变异**，不是门的胜利）。
//	留着无害，但它对上面那两条全角样本**没有贡献** —— 那两条是 normalizeCER 的功劳。
func TestRefHasASCIIClassifiesCorpus(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"纯汉字无 ASCII", "今天下午三点开会。", false},
		{"汉字夹标点", "这个嘛，怎么说呢，我觉得吧。", false},
		{"汉字含〇零", "二〇二六年十月七号是周三", false},
		{"含半角数字", "我们从1.0升级到2.0", true},
		{"含半角英文", "请检查一下Redis和Kafka", true},
		{"含小写连写", "把user_id改成uid", true},
		// ⚠ 下面两条是**朴素实现会答错**的：全角字符逐个都 ≥ 0x80。
		{"全角字母（归一化后）", normalizeCER("Ｑ３"), true},
		{"全角数字（归一化后）", normalizeCER("１.０"), true},
		// 归一化要处理全角标点，否则「今天下午三点，」里的全角逗号
		// 不影响判定，但下面这条验证的是标点**不会**被当成 ASCII。
		{"全角标点不应算 ASCII", "今天，下午。", false},
	}
	for _, c := range cases {
		if got := refHasASCII(normalizeCER(c.in)); got != c.want {
			t.Errorf("%s：refHasASCII(%q) = %v，期望 %v", c.name, c.in, got, c.want)
		}
	}
}

// TestRefHasASCIICorpusHasBothBuckets 守住「拆分非空」。
//
// ⚠ 为什么这条不能省：如果哪天有人把语料清一色改成纯中文，
//
//	拆分表会变成「纯汉字 = 一行、含 ASCII = （本轮无此类语料）」，
//	而 §481 想量的那一档（真实识别质量）就**又**没量到 ——
//	形态与 §474.4 的漏类别完全同形，且退出码照旧是 0。
func TestRefHasASCIICorpusHasBothBuckets(t *testing.T) {
	all := asrCases()
	if len(all) == 0 {
		t.Fatal("语料是空的，判据失去意义")
	}
	ascii, han := 0, 0
	for _, c := range all {
		if refHasASCII(normalizeCER(c.text)) {
			ascii++
		} else {
			han++
		}
	}
	if han == 0 {
		t.Errorf("%d 条语料里没有一条「参考纯汉字」⇒ §481 要量的真实识别质量这档没量到", len(all))
	}
	if ascii == 0 {
		t.Errorf("%d 条语料里没有一条「参考含 ASCII」⇒ §481 要量的混合排版这档没量到", len(all))
	}
	t.Logf("语料 %d 条：含 ASCII %d / 纯汉字 %d —— 两档都在", len(all), ascii, han)
}

// TestCERProbeReportsASCIISplit 守**探针本人**的打印路径。
//
// ⚠⚠ 为什么前面两条还不够：它们验的是语料与谓词，
//
//	而「探针到底打不打算把拆分报出来」是**另一件事** ——
//	有人把 §481 那段删掉、只留整体 CER 的话，两条全绿，
//	而 §474 的教训（头条数字被单读）就原样复发。
//	⇒ 必须直接读探针源码。
func TestCERProbeReportsASCIISplit(t *testing.T) {
	src, err := os.ReadFile("live_asr_cer_probe_test.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)

	// ① 探针必须**按 ASCII 判定分桶**并累加，而不是只累加总数。
	if !regexp.MustCompile(`byASCII\[refHasASCII\(`).MatchString(s) {
		t.Error("探针里找不到 `byASCII[refHasASCII(...)]` ⇒ §481 的分桶不在，判据可能恒假")
	}
	// ② 两档都要**真的被打出来**，且**指向不同的桶**。
	//
	// ⚠⚠ 只查字面量存在是不够的（§481 变异 M3 实测）：把
	// `{"参考纯汉字", false}` 改成 `{"参考纯汉字", true}`，
	// 两行照打、拆分表照旧好看，**而两行的数据变成同一份** ——
	// 形态与「没拆」完全同形，退出码还是 0。
	// ⇒ 必须断「label ↔ 桶」的**配对**，不是断「label 在不在」。
	for _, pair := range []struct {
		label  string
		bucket string
	}{
		{"参考含 ASCII", "true"},
		{"参考纯汉字", "false"},
	} {
		re := regexp.MustCompile(`"` + pair.label + `"\s*,\s*` + pair.bucket + `\s*\}`)
		if !re.MatchString(s) {
			t.Errorf("探针的拆分表里没有「%q → %s」这一档 ⇒ "+
				"要么这一行被删了，要么两行指向了同一个桶（后者等于没拆）", pair.label, pair.bucket)
		}
	}
	// ③ 纯汉字档为空时必须自己变红 —— 这是「量具坏掉要出声」的闸。
	if !regexp.MustCompile(`byASCII\[false\]\[1\] == 0`).MatchString(s) {
		t.Error("探针没有「纯汉字档为空就报错」的分支 ⇒ " +
			"拆分表可以整张印出来而其中一档根本没跑，与「没跑」完全同形")
	}
	// ④ 整体 CER 那行必须**带提醒**，不许再作为唯一读数呈现。
	if !strings.Contains(s, "混着") || !strings.Contains(s, "TTS 念 ASCII") {
		t.Error("探针没有说明「整体 CER 混着 TTS 念 ASCII 的差异」⇒ " +
			"§474 的头条数字还会被再次单读")
	}
}

// ⚠ 负控：判据必须能抓到「有人把整体 CER 又当成唯一读数」。
//
// 没有这一条，上面 ①~④ 都可能是恒真的 —— 而「恒真」与「正确」
// 在退出码上没有任何区别（§471.2 同族：量具自己坏掉时门会给出一个毫无
// 异常读数；§474.4 同族：表照旧打印、退出码照旧是 0）。
func TestCERProbeSplitGuardIsNotVacuous(t *testing.T) {
	// 造一份「假装探针已经被改回只报整体数字」的源码，看判据会不会放过它。
	regressed := `
	func main() {
		t.Logf("整体 CER = %.2f%%", x)
	}
	`
	gate := regexp.MustCompile(`byASCII\[refHasASCII\(`)
	if gate.MatchString(regressed) {
		t.Fatal("判据自检失效：这段明显没有分桶的源码竟然被判为通过")
	}
	// 反向自证：真源码必须通过同一判据。
	src, err := os.ReadFile("live_asr_cer_probe_test.go")
	if err != nil {
		t.Fatal(err)
	}
	if !gate.MatchString(string(src)) {
		t.Fatal("判据自检失效：当前探针源码反而不通过 ⇒ 锚点已漂移，先修锚点再谈门")
	}
	t.Log("负控通过：去掉分桶的源码会被判为不通过，当前源码通过同一判据")
}
