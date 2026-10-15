// 真网关探针（默认 skip）：量 `mimo-v2.5-asr` 的**真实字符错误率**。
//
// ⚠⚠ 2026-10-07 查明一件影响此前全部结论的事：
//
// **`/tmp/gt-voice-16k.wav` 是 TTS 合成语音，不是真实会议录音。**
//
// 证据：同网关的 `mimo-v2.5-tts` 用同一句话生成的音频时长
// **4.640000 秒，与它逐位相同**；且该 TTS 音频经 ASR 回读**逐字精确**。
// 而 §19–§26 全部基于那条音频，它的特点（连续讲话、零静音点、
// 语速均匀、无噪声、干净单说话）**全是 TTS 的属性，不是真人录音的属性**。
//
// ⇒ 那七节里「生产形状重复量只有 1–2 字」「3 秒窗口净差 0 字」
//
//	「SplitWAV 一个静音点都找不到」等结论**不能外推到真实会议录音**。
//	它们只在「干净 TTS 语音」这个条件下成立。§26.4 已把它们列为
//	「已证伪」，但要补一句：**证伪的范围也只是 TTS 条件下的**。
//
// 本探针做什么：既然网关自带 TTS，就用它造一批**带 ground truth**
// 的中文语料，量 ASR 的真实 CER。
//
// ★ 为什么 ground truth 是关键：此前所有对比都缺它 ——
//
//	§19-§26 反复做的是「模型 A 的输出 vs 模型 A 的另一个输出」，
//	那叫自一致，不叫准确率。有了 TTS，**输入什么就该读出什么**，
//	CER 才是真的。
//
// ⚠ 已知局限（必须一起读，否则会高估）：TTS 语音**没有**噪声、混响、
//
//	多人重叠、方言口音、背景人声。这些恰是真实会议里识别错误的主因。
//	⇒ 这里的 CER 是**上界**（最好情况），不是生产预期。
//	真实值只能靠真机长录音测，那是 §26.4 仍未验证的那一项。
//
// 运行：
//
//	POCKET_LIVE_GATEWAY=1 \
//	POCKET_LLM_GATEWAY_URL=https://llmgo.kxpms.cn/v1 \
//	POCKET_LLM_GATEWAY_API_KEY=... \
//	go test ./internal/stt -run TestLiveGatewayASRCer -v
package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/livetest"
)

// ttsCase 是一条测试语料。text 同时是 TTS 的输入与 CER 的基准。
type ttsCase struct {
	name  string
	kind  string // 语料类型，用于分组看错误集中在哪一类
	text  string
	voice string
}

func asrCases() []ttsCase {
	return []ttsCase{
		{"基础-短句", "基础", "今天下午三点开会。", "default"},
		{"基础-长句", "基础", "今天下午三点会议室开产品评审会，请提前十分钟到场。", "default"},
		{"数字-日期", "数字", "二〇二六年十月七号是周三，下周一就是十号。", "default"},
		{"数字-金额", "数字", "这个方案预算是三百五十万，分三期付款，每期一百二十万。", "default"},
		{"数字-版本号", "数字", "请把接口升级到 v2 点 5 版本，并同步更新 changelog。", "default"},
		{"英文混排-术语", "英文混排", "请检查一下 Redis 和 Kafka 的 consumer lag 是否正常。", "default"},
		{"英文混排-标识符", "英文混排", "把 user_id 改成 uid，然后跑一遍 CI 看有没有回归。", "default"},
		{"专名-人名", "专名", "张伟和李娜都会参加，王强负责做会议纪要。", "default"},
		{"专名-地名", "专名", "我们在深圳的办公室开会，杭州那边的同事远程参加。", "default"},
		{"同音易错-1", "同音易错", "这个功能的权限控制是白名单机制，不是黑名单。", "default"},
		{"同音易错-2", "同音易错", "部署的时候记得先备份数据库，别直接覆盖线上。", "default"},
		{"口语-停顿", "口语", "这个嘛……怎么说呢，我觉得吧，可以先这样，然后再看看。", "default"},
		{"长句-一口气", "长句", "我们今天讨论了三个问题，第一个是排期，第二个是预算，第三个是人力投入，我个人倾向于先把排期定下来，预算可以往后放一放，人力的话等下个季度再看。", "default"},

		// ── 中英混排加重区 ──
		// 2026-10-07 第一轮（13 条）里 CER 7.26% 的错误**几乎全部集中在这类**。
		// 13 条样本每类才 1–2 条，得不出可信的分类结论；这一组把它做成
		// 真正能回答问题的规模（16 条），顺带覆盖第一轮没测到的形态：
		// 缩写连读、字母混数字、路径/命令、技术名词、版本号、邮箱、URL。
		{"混排-缩写连读", "中英混排", "这批先合到 main 分支，CI 挂了再发版。", "default"},
		{"混排-单字母缩写", "中英混排", "这个问题记到 JIRA 上了，优先级是 P0。", "default"},
		{"混排-字母混数字", "中英混排", "请查一下 Q3 的 OKR 完成情况，还有 H1 的目标。", "default"},
		{"混排-技术名词", "中英混排", "缓存用 Redis，消息队列用 Kafka，配置中心用 Nacos。", "default"},
		{"混排-框架名", "中英混排", "前端用 Vue，后端用 Go，数据库是 PostgreSQL。", "default"},
		{"混排-命令", "中英混排", "先跑一下 npm install，然后 npm run build 看看能不能过。", "default"},
		{"混排-git命令", "中英混排", "记得先 git pull，然后 rebase 一下，别直接 force push。", "default"},
		{"混排-路径", "中英混排", "配置文件在 conf 目录下的 app yaml 文件里。", "default"},
		{"混排-版本号", "中英混排", "我们从 1.0 升级到 2.0，中间要兼容 1.5 的客户端。", "default"},
		{"混排-接口名", "中英混排", "这个字段叫 createdAt，不是 created_at，两个地方不一致。", "default"},
		{"混排-HTTP方法", "中英混排", "这个接口应该用 POST 而不是 GET，因为要传参数。", "default"},
		{"混排-状态码", "中英混排", "如果返回 401 就是 token 过期，500 才是服务端的问题。", "default"},
		{"混排-邮箱", "中英混排", "有问题发邮件到 support 那个邮箱，或者在群里 at 我。", "default"},
		{"混排-函数名", "中英混排", "把 calculateTotal 这个函数拆开，现在耦合太重了。", "default"},
		{"混排-布尔与判断", "中英混排", "这个开关默认是 false，只有 debug 模式才打开 trace。", "default"},
		{"混排-容器", "中英混排", "部署的时候用 Docker，最后跑在 Kubernetes 集群里。", "default"},
	}
}

// knownKindOrder 只是**排序提示**，不是白名单。
//
// ⚠ 2026-10-11（§474.4）：它曾经**就是**白名单，而 `中英混排` 被落在外面，
// 那 16 条的最大组从来没出现在分组表里。新增 kind 一律排在这些之后（字典序），
// 所以「忘了加进这个数组」不再是一个会静默丢数据的坑。
var knownKindOrder = []string{"基础", "数字", "英文混排", "中英混排", "专名", "同音易错", "口语", "长句"}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// TestLiveGatewayASRCer 造语料 → 转写 → 逐字算 CER。
func TestLiveGatewayASRCer(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("真网关 CER 探针：需 POCKET_LIVE_GATEWAY=1")
	}
	baseURL := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_URL"))
	apiKey := strings.TrimSpace(os.Getenv("POCKET_LLM_GATEWAY_API_KEY"))
	asrModel := strings.TrimSpace(os.Getenv("POCKET_LIVE_ASR_MODEL"))
	ttsModel := strings.TrimSpace(os.Getenv("POCKET_LIVE_TTS_MODEL"))
	if baseURL == "" || apiKey == "" {
		t.Fatal("缺少 POCKET_LLM_GATEWAY_URL / _API_KEY")
	}
	if asrModel == "" {
		asrModel = "mimo-v2.5-asr"
	}
	if ttsModel == "" {
		ttsModel = "mimo-v2.5-tts"
	}

	client := &http.Client{Timeout: 120 * time.Second}
	tr := NewTranscriber(apiKey, asrModel, baseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()

	type row struct {
		name, kind, ref, hyp   string
		sub, dele, ins, refLen int
		secs                   float64
	}
	var rows []row
	totRef, totErr, totSub, totDel, totIns := 0, 0, 0, 0, 0

	// ⭐ §489：本轮做完了几条必须对账，且**分母直接由语料长度算出**。
	//
	// ⚠⚠ 为什么这条特别要紧：本探针是**头条量具**——
	//   §474/§481 报给用户的每一个 CER 数字都出自这里。
	//   而它有两条 `continue`（TTS 失败 / ASR 失败）：
	//
	//	被 ctx 预算（写死 600s）砍掉的后半程 ⇒ 每一条都走 `continue`
	//	⇒ `rows` 少一半 ⇒ 分组表与「整体 CER」照旧打印、退出码照旧 0。
	//
	//   ⇒ **砍掉一半的读数与完整的读数完全同形**，而数字看起来毫无异常。
	//
	// ★ 2026-10-11 实测复核（不靠推断）：§481 那次读数**确实跑满了 29/29**
	//   （223.5s、0 条失败）⇒ **已报出的数字是有效的**。
	//   但那是**运气**：ctx 一旦被砍，探针不会出声。
	//   ⇒ 结论从「数字错了」改成「数字当时是对的、而它当时并不知道自己对不对」。
	//
	// ⚠ total 刻意写 `len(asrCases())` 而不是 29：
	//   写死的数字会在下次加语料时静默过期 —— 那正是「自证量具」的反面。
	ledger := livetest.New("CER 探针", len(asrCases()))

	for _, c := range asrCases() {
		wav, err := synthWav(ctx, client, baseURL, apiKey, ttsModel, c.text, c.voice)
		if err != nil {
			t.Logf("[%s] TTS 失败：%v", c.name, err)
			continue
		}
		sec := wavDurationSec(t, wav)
		res, err := tr.TranscribeFor(ctx, Scope{UserID: "p", WorkspaceID: "w"}, wav, "t.wav")
		if err != nil {
			t.Logf("[%s] ASR 失败：%v", c.name, err)
			continue
		}
		hyp := normalizeCER(strings.TrimSpace(res.Text))
		ref := normalizeCER(c.text)
		sub, dele, ins := diffCounts([]rune(ref), []rune(hyp))
		r := row{c.name, c.kind, ref, hyp, sub, dele, ins, len([]rune(ref)), sec}
		rows = append(rows, r)
		totRef += r.refLen
		totErr += sub + dele + ins
		totSub += sub
		totDel += dele
		totIns += ins
		t.Logf("[%-16s %-8s] %5.1fs 参考%3d字 → 替换%d 删%d 增%d | CER %.1f%%",
			r.name, r.kind, r.secs, r.refLen, r.sub, r.dele, r.ins, 100*float64(r.sub+r.dele+r.ins)/float64(r.refLen))
		if sub+r.dele+r.ins > 0 {
			t.Logf("        参考: %s", r.ref)
			t.Logf("        识别: %s", r.hyp)
		}
		// ⚠ 记账点在**成功之后**：`continue` 的那两条路（§489）不会走到这里，
		//   所以「跑了几条」与「跑了几条且成功」是**同一个数**，不混淆。
		ledger.Complete(c.name)
	}

	if len(rows) == 0 {
		t.Fatal("没有一条语料成功 —— 不能拿空统计下结论")
	}

	// ⚠⚠ 必须**在打印任何汇总之前**对账 —— §476.1 ① 的形态。
	//
	// ⛔ 而这条特别关键：本探针的**头条读数就是「整体 CER」**，
	// 而它由 `totRef`（成功条目的参考字数）除出来 ——
	// 少跑 10 条，分子分母同时变小，CER 仍然是个「看起来正常的百分比」。
	//   ⇒ 光看数字**没有任何异常**，必须由账本出声。
	if msg := ledger.Report(); msg != "" {
		t.Error(msg)
	}
	// 分母必须显式出现在读数里：光有 CER 百分比，读者无从知道它是几条语料上的值。
	// ⚠⚠ §492：这里的「本轮成功」改成 `ledger.Done()`。
	// 原先写的是 `len(rows)` —— 与账本是两个来源；一旦有人改了记账逻辑
	// 而忘了改这行，读数就会**声称**一个账本并不认可的分母。
	t.Logf("【分母自证】语料 %d 条，本轮成功 %d 条（账本 %d/%d）⇒ 下面的每个百分比都只对这 %d 条成立。",
		len(asrCases()), len(rows), ledger.Done(), ledger.Total(), len(rows))

	t.Log("")
	t.Log("┌─ 分组 CER ──────────────────────────────────────────────")
	byKind := map[string][2]int{}
	for _, r := range rows {
		v := byKind[r.kind]
		v[0] += r.sub + r.dele + r.ins
		v[1] += r.refLen
		byKind[r.kind] = v
	}
	// ⚠⚠ 2026-10-11（§474.4）：这里原先是**写死的**组别列表
	//   `[]string{"基础","数字","英文混排","专名","同音易错","口语","长句"}`，
	//   而 `asrCases()` 里的 kind 有 **8** 个 —— 多出来的是 `中英混排`（16 条）。
	//   ⇒ 那张表**从来没显示过最大的、也是最该看的那一组**。
	//
	//   讽刺的是语料注释原话就是「13 条样本每类才 1–2 条，得不出可信的分类结论；
	//   这一组把它做成**真正能回答问题的规模**」——
	//   而汇总表正好把答案藏起来了。逐条行还在，所以数据没丢；
	//   丢的是**决策者实际读的那一面**。
	//
	//   ⇒ 修法不是「把中英混排补进数组」（那只是把同一个坑留到下一类），
	//     而是**从数据推导**：已知顺序只当排序提示，其余的按字典序追加。
	//     ⇒ 今后新增任何 kind 都不可能再被静默丢掉。
	kinds := make([]string, 0, len(byKind))
	for _, k := range knownKindOrder {
		if _, ok := byKind[k]; ok {
			kinds = append(kinds, k)
		}
	}
	rest := make([]string, 0, len(byKind))
	for k := range byKind {
		if !contains(kinds, k) {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	kinds = append(kinds, rest...)
	for _, k := range kinds {
		v := byKind[k]
		if v[1] == 0 {
			continue
		}
		t.Logf("│ %-10s 错误 %2d / 参考 %3d 字 = %5.1f%%", k, v[0], v[1], 100*float64(v[0])/float64(v[1]))
	}
	// ⚠ 自证：**每一条跑过的语料都必须在表里出现一行**。
	//   没有这条断言，将来再加一个 kind 又被静默丢掉时，
	//   整张表照旧打印、退出码照旧是 0 —— 与「没跑」完全同形。
	if len(kinds) != len(byKind) {
		t.Errorf("分组表漏了 %d 个类别（表里 %d / 数据里 %d）", len(byKind)-len(kinds), len(kinds), len(byKind))
	}
	t.Log("└────────────────────────────────────────────────────────")
	t.Logf("合计：替换 %d / 删除 %d / 插入 %d，参考 %d 字", totSub, totDel, totIns, totRef)
	t.Logf("整体 CER = %.2f%%（{S}+{D}+{I} / N）", 100*float64(totErr)/float64(totRef))
	t.Log("⚠ 这是 **TTS 干净语音**上的值，是**最好情况**。")
	t.Log("  真实会议（噪声/多人/方言/远场）会显著更高 —— 那一项仍需真机长录音。")

	// ── ⛔ §481：整体 CER 把两件不同的事混在了一个分母里 ──
	//
	// 语料是用网关 TTS **从参考原文合成**的，而 TTS 念 ASCII 的方式是
	// 「念成汉字」：`say`/TTS 读 "1.0" 念的是「一点零」、读 "401" 念的是
	//「四百零一」、读 "P0" 念的是「P零」。
	//
	// ⇒ 对**含 ASCII 的语料**，书面参考（`我们从1.0升级到2.0`）与
	//   **音频里实际说出来的话**（「我们从一点零升级到二点零」）不是同一件事。
	//   ASR 输出「一点零」是**对音频正确**的，是参考当成了错的。
	//
	// 量具自证（§481.3）：随请求带的 `prompt` 字段是**固定常量**
	// `SimplifiedChineseBiasPrompt`（target.go:140「以下是普通话的句子，请用简体中文输出。」），
	// 参考原文只发给了 TTS、**没有**作为热词或提示发给 ASR
	//（provider_openai.go:84 / transcribe.go:526 两处都是这个常量）。
	// ⇒ 读数不是「ASR 拿到了答案」。
	//
	// 所以整体 CER 不能单读。按「参考是否含 ASCII」拆开才是可解释的量：
	//   · 纯汉字语料：参考 == 音频内容，CER 反映**真实识别质量**
	//   · 含 ASCII 语料：CER 混着「TTS 念法差异」，反映的是**混合排版**
	// ⚠ 含 ASCII 组里**仍有真错**（`createdAt`→`tried`、`changelog`→`Channelbug`、
	//   `consumerlag`→`consolelog`），别把它整个读成「量具噪声、可以忽略」。
	byASCII := map[bool][2]int{}
	for _, r := range rows {
		v := byASCII[refHasASCII(r.ref)]
		v[0] += r.sub + r.dele + r.ins
		v[1] += r.refLen
	}
	t.Log("")
	t.Logf("┌─ 按「参考是否含 ASCII」拆分（§481）──────────────────────")
	for _, tag := range []struct {
		label string
		k     bool
	}{{"参考含 ASCII", true}, {"参考纯汉字", false}} {
		v := byASCII[tag.k]
		if v[1] == 0 {
			t.Logf("│ %-10s （本轮无此类语料）", tag.label)
			continue
		}
		t.Logf("│ %-10s 错误 %2d / 参考 %3d 字 = %5.1f%%", tag.label, v[0], v[1], 100*float64(v[0])/float64(v[1]))
	}
	t.Log("└────────────────────────────────────────────────────────")
	if byASCII[false][1] == 0 {
		// 与「分组表漏类别」同一种形态：表照旧打印、退出码照旧是 0，
		// 与「没跑这组」完全同形。所以这条必须自己变成红灯。
		t.Errorf("没有任何「参考纯汉字」语料 —— 真实识别质量这一档本轮没量到（§481）")
	}
}

// refHasASCII 报告归一化后的参考文本里是否还有 ASCII 字母或数字。
//
// ⚠ 归一化会把全角字母数字折成半角（normalizeCER），所以这里要在
// **归一化之后**判断，否则「Ｑ３」这类全角写法会被误判成「纯汉字」。
func refHasASCII(ref string) bool {
	for _, r := range ref {
		if r < 0x80 && ((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return true
		}
	}
	return false
}

// synthWav 调网关的 TTS 端点造一条语料，返回 16k/单声道/16bit 的 WAV。
//
// 走 HTTP 而不是命令行工具：macOS 自带的中文语音包只有名字没装数据
// （`say -v Flo` 产出 0.016s 空音频，英文正常），所以 TTS 只能走网关。
func synthWav(ctx context.Context, client *http.Client, baseURL, apiKey, model, text, voice string) ([]byte, error) {
	body, _ := json.Marshal(map[string]string{"model": model, "input": text, "voice": voice})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errStatus(resp.StatusCode, raw)
	}
	if !strings.HasPrefix(string(raw[:min(4, len(raw))]), "RIFF") {
		return nil, errNotWAV(raw)
	}
	return raw, nil
}

// normalizeCER 归一化后再算错误率：去空白、全角转半角。
//
// 标点也**保留** —— 中文 ASR 常见的「的/地/得」与句末标点漂移
// 正是要计入 CER 的误差类型；把标点删掉会把错误藏起来。
func normalizeCER(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '　':
			continue
		}
		// 全角字母数字/标点 → 半角（UTF-8 逐字节对拷）
		if r >= 0xFF01 && r <= 0xFF5E {
			b.WriteRune(r - 0xFEE0)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// diffCounts 统计替换 / 删除 / 插入三种错误的数量（按 LCS 对齐）。
//
// 用 LCS 而不是 Levenshtein：仓里已有 lcsAlign，且本探针要报的
// 「参考有、识别没有」与「识别多出来的」正是 LCS 最直观能分开的两类。
func diffCounts(ref, hyp []rune) (sub, dele, ins int) {
	if len(ref) == 0 {
		return 0, 0, len(hyp)
	}
	if len(hyp) == 0 {
		return 0, len(ref), 0
	}
	_, matched := lcsAlign(ref, hyp)
	for j := 1; j <= len(hyp); j++ {
		if !matched[j] {
			ins++
		}
	}
	common := 0
	for j := 1; j <= len(hyp); j++ {
		if matched[j] {
			common++
		}
	}
	dele = len(ref) - common
	// 替换 = 插入数与删除数中的较小者（LCS 把它们配成对）
	sub = dele
	if ins < sub {
		sub = ins
	}
	if dele > ins {
		sub = ins
	}
	return sub, dele - sub, ins - sub
}

type errStatusT int

func (e errStatusT) Error() string { return "网关返回非 200" }

func errStatus(code int, raw []byte) error {
	return errStatusT(code)
}

type errNotWAVT struct{ b []byte }

func (e errNotWAVT) Error() string { return "响应不是 RIFF/WAV" }

func errNotWAV(raw []byte) error { return errNotWAVT{b: raw} }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
