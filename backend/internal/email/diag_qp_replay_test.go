package email

// diag_qp_replay_test.go — 需求 7「在邮件的窗口中可以查看收到的各类邮件」：
// 库里有一批**列表摘要仍是 quoted-printable 未解码**的邮件（18 封），判断它是
// 「代码仍会产出」还是「存量脏数据」。
//
// ## 为什么必须重放，不能靠时间推断
//
// 「10-03 入库的邮件里没有真 QP 形态」只是**没有反例**——那 45 封里可能压根
// 没有那类邮件（163 验证码、火山引擎开通通知）。「最近没有反例」推不出
// 「已修复」（absence of evidence ≠ evidence of absence）。唯一能定论的办法：
// 把**存下来的原文**喂给**当前的**解析器，看它现在算出来的摘要干不干净。
//
//	新摘要干净  ⇒ 代码已修，库里那 18 条是历史脏数据，只能存量修复
//	新摘要仍乱  ⇒ 活 bug，同步路径仍在产出乱码
//
// ## 结论有效性依赖两件事，都已核对
//
// 1. **重放用的是同步路径同一个函数**：fetcher.go:957 落库时调的就是
//    SnippetFromParsed(parsed, 500)（DeriveSnippet 只是空摘要时的兜底）。
//    若同步路径另有一条摘要来源，这里"干净"就证明不了任何事。
// 2. **喂的是同一种字节**：缓存里的完整 MIME 原文（format 0x01）就是同步时
//    解析的那份原文，不是二次加工的产物。
//
// ## 只读
//
// 只读 .bin 原文文件 + 只调解析器，**不连 PG、不碰 IMAP、不写任何数据**。
// 门禁：POCKET_DIAG_QP_REPLAY=1，且
//
//	POCKET_DIAG_QP_BODY=<单个 .bin 路径 | 整个目录>
//	POCKET_DIAG_QP_DATADIR=<含 email_master.key 的数据目录>
//
// ## qpHits 的判据用密度而不是「出现过 =XX」
//
// URL 查询串里就有 =20 / =DD，一个 501 字摘要里出现 1-4 次完全正常；
// 真 QP 编码的中文是**每个汉字 3 个 =XX**（500 字摘要几百个）。
// 阈值 20：低于它是噪声，高于它才是正文在传输层没被解码。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// qpThreshold 是「真 QP 形态」的密度阈值，见文件头注释。
const qpThreshold = 20

// qpHits 数摘要里 quoted-printable 转义（=XX）的个数。
func qpHits(s string) int {
	n, esc := 0, false
	for i := 0; i+2 < len(s); i++ {
		if esc {
			if isHexDigit(s[i]) && isHexDigit(s[i+1]) {
				n++
				i++
			}
			esc = false
			continue
		}
		if s[i] == '=' {
			esc = true
		}
	}
	return n
}

func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// qpOutcome 是一次重放的结果。
type qpOutcome struct {
	format   byte
	legacy   bool
	decBytes int
	fresh    string
	htmlQP   int
	textQP   int
	err      error
}

// looksLikeMIME 直接复用 snippet.go:487 的那份，不重复实现：
// 那份的判据是「头字段名 + 冒号」（reMIMEFieldName），并且注释里记着
// 一次真实的误判修复 —— 早先用 strings.Contains(head, ":")，
// 「会议改到下午三点：记得带季报」这种正文会被判成 MIME 源码、摘要直接空串。
// 这里若另写一份弱判据，得到的样本覆盖率会虚高。
//
// 旧格式缓存里混着「拍平后的展示文本」，那种东西没有头，喂给
// ParseMIMEMessage 会报 malformed header line —— 那个错说的是
// 「这不是一封 MIME 报文」，不是「解不开」，两者不能混。

func TestDiagQPReplay(t *testing.T) {
	if os.Getenv("POCKET_DIAG_QP_REPLAY") != "1" {
		t.Skip("set POCKET_DIAG_QP_REPLAY=1 and POCKET_DIAG_QP_BODY=<raw .bin path or dir> to run (read-only)")
	}
	target := os.Getenv("POCKET_DIAG_QP_BODY")
	if target == "" {
		t.Fatal("POCKET_DIAG_QP_BODY 未设置 —— 无法重放")
	}
	dataDir := os.Getenv("POCKET_DIAG_QP_DATADIR")
	if dataDir == "" {
		t.Fatal("POCKET_DIAG_QP_DATADIR 未设置（需要含 email_master.key 的数据目录）")
	}
	key, err := EnsureMasterKey("", dataDir)
	if err != nil {
		t.Fatalf("EnsureMasterKey(%s): %v", dataDir, err)
	}
	cr, err := NewCrypto(key)
	if err != nil {
		t.Fatalf("NewCrypto: %v", err)
	}

	files, err := collectBodyFiles(target)
	if err != nil {
		t.Fatalf("collect %s: %v", target, err)
	}
	if len(files) == 0 {
		t.Fatalf("%s 下没有 .bin 原文缓存", target)
	}
	t.Logf("重放 %d 个正文缓存文件，判据：fresh snippet 的 qp 密度 < %d 为干净", len(files), qpThreshold)

	var ok, okOld, legacy, failed int
	var dirty []string
	for _, f := range files {
		blob, readErr := os.ReadFile(f)
		if readErr != nil {
			failed++
			t.Logf("READ-ERR %-52s %v", filepath.Base(f), readErr)
			continue
		}
		out := replayOne(cr, blob)
		if out.err != nil {
			if out.legacy {
				legacy++
				t.Logf("NO-REPLAY %-52s %v", filepath.Base(f), out.err)
				continue
			}
			failed++
			t.Logf("FAIL     %-52s fmt=0x%02x %v", filepath.Base(f), out.format, out.err)
			continue
		}
		ok++
		if out.legacy {
			okOld++
		}
		hits := qpHits(out.fresh)
		flag := "clean"
		if out.legacy {
			flag = "clean/old"
		}
		if hits >= qpThreshold {
			flag = "DIRTY"
			dirty = append(dirty, filepath.Base(f))
		}
		t.Logf("%-11s %-52s fmt=0x%02x plain=%dB  html=%dc(qp=%d) text=%dc(qp=%d) -> %dc(qp=%d)",
			flag, filepath.Base(f), out.format, out.decBytes,
			len(out.fresh), hits, 0, out.textQP, len(out.fresh), hits)
		if hits >= qpThreshold {
			t.Logf("        head: %s", headRunes(out.fresh, 110))
		}
	}

	t.Logf("—— 汇总：可重放 %d（旧格式 %d），不可重放（旧格式非 MIME 原文）%d，读/解/解析失败 %d，其中新摘要仍乱 %d ——",
		ok, okOld, legacy, failed, len(dirty))
	if len(dirty) == 0 && ok > 0 {
		t.Log("VERDICT: 当前代码对这批原文产出的摘要**全部干净** ⇒ 库里那 18 条是历史脏数据，只能存量修复")
	} else if len(dirty) > 0 {
		t.Logf("VERDICT: **活 bug** —— %d 封经当前代码重放后摘要仍是 QP 未解码：%v", len(dirty), dirty)
	} else {
		t.Log("VERDICT: 无有效样本（全部 legacy/失败）⇒ 结论不成立，不能据此说已修")
	}
	if ok == 0 {
		t.Fatal("零个可重放样本 —— 判据从未在工作，结论不成立")
	}
}

func collectBodyFiles(target string) ([]string, error) {
	st, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return []string{target}, nil
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".bin") {
			continue
		}
		out = append(out, filepath.Join(target, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

// replayOne 解开一个正文缓存并用当前解析器算一遍摘要。
// 缓存格式（server_assistant.go:1835-1847 writeCachedEmailBody）：
//
//	8 字节大端 UID（写入时恒为 0，让读路径跳过 UID 校验）
//	+ 1 字节 format 版本（0x01=完整 MIME 原文，0x02=BODY[TEXT] 兜底纯文本）
//	+ AES-GCM 密文的 base64 文本
//
// 我先后栽了两次：第一次喂整个 blob → DecryptString 在第 0 字节报
// 「illegal base64 data」（前缀是二进制）；第二次喂 blob[8:] → **还是**
// 同一个错，因为我照 body_cache.go 的注释以为头部只有 8 字节，可那份管的是
// email-bodies-raw。server 层这一份在 UID 之后还有一个 format 字节，真正起点
// 是 9。先断言 format 是已知值再解 —— 否则错位喂进去，错误会被归到「钥匙不对」。
func replayOne(cr *Crypto, blob []byte) qpOutcome {
	if len(blob) < 8 {
		return qpOutcome{err: fmt.Errorf("blob 只有 %d 字节，不足 8 字节 UID 前缀", len(blob))}
	}
	format, payload, err := locateCiphertext(blob)
	if err != nil {
		return qpOutcome{err: err}
	}
	dec, err := cr.DecryptString(payload)
	if err != nil {
		// 用错钥匙时它会报错而不是返回垃圾。
		return qpOutcome{err: err}
	}
	if len(dec) == 0 {
		return qpOutcome{err: fmt.Errorf("解密后为空")}
	}
	if format == formatLegacy && !looksLikeMIME(dec) {
		return qpOutcome{format: format, legacy: true,
			err: fmt.Errorf("旧格式且不是 MIME 原文（多半是当年拍平的展示文本）——不能喂 ParseMIMEMessage")}
	}
	parsed, err := ParseMIMEMessage([]byte(dec))
	if err != nil {
		return qpOutcome{format: format, err: err}
	}
	fresh := SnippetFromParsed(parsed, 500)
	if fresh == "" {
		// 与 fetcher.go:958-962 的落库兜底保持一致，否则重放会比真实路径更宽容。
		fresh = DeriveSnippet([]byte(parsed.HTMLBody), 500)
	}
	return qpOutcome{
		format:   format,
		legacy:   format == formatLegacy,
		decBytes: len(dec),
		fresh:    fresh,
		htmlQP:   qpHits(parsed.HTMLBody),
		textQP:   qpHits(parsed.TextBody),
	}
}

// formatLegacy 标记「2026-10-01 之前、UID 之后没有版本字节」的旧格式。
const formatLegacy byte = 0x00

// locateCiphertext 定位 base64 密文在 blob 中的起点，返回 format 与 payload。
//
// 当前格式（0x01/0x02）：8 字节 UID + 1 字节 format + 密文 ⇒ 从 9 起。
// 旧格式：8 字节 UID + 密文（无 format 字节）⇒ 从 8 起。
//
// 为什么必须两条都试：旧格式占目录里 41/48 个文件，而**两种 QP 乱码形态恰好
// 分布在不同格式里**（=0D=0A 那批是新版可重放、=E4=BD=A0 那批 163 验证码
// 全在旧格式）。只解新版就会只对一种形态有结论，另一形态只能说「无证据」——
// 而「无证据」会被下一轮读成「已修复」。
//
// 旧格式的已知坑：它当年存的是 ExtractDisplayBody 拍平后的展示文本，不一定
// 是完整 MIME 原文。所以解出来还要先嗅探是不是 MIME（首行有 `字段: 值`），
// 不是就不能喂 ParseMIMEMessage——否则报出来的错会被误读成「这封解不开」。
func locateCiphertext(blob []byte) (format byte, payload string, err error) {
	if len(blob) > 8 {
		if f := blob[8]; f == 0x01 || f == 0x02 {
			return f, string(blob[9:]), nil
		}
	}
	return formatLegacy, string(blob[8:]), nil
}

func headRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
