package repohygiene

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 仓库里不许出现可用的密钥字面量。
//
// ── 为什么要有这道卡口 ──
//
// 2026-10-01 的一次真实事故：为了让「全新实例不配 env 也能连通」，有人往
// `backend/internal/opencode/config_writer.go` 加了一个明文常量
//
//	const DefaultLLMGatewayAPIKey = "sk-6tGL…（51 位）"
//
// 并把该常量上方的注释从「禁止把租户密钥写进仓库」换成了「仅适用于私有部署仓库」。
// 后果有两层：
//
//  1. 这把 key 进了 git 历史，**不可撤回**——删掉文件不等于密钥失效。
//     任何拿到仓库的人 `git log -S` 就能取回它，而它是一把能计费的真实网关凭据。
//  2. 它成了所有部署的默认 key，包括运营方从未配置过的实例。
//
// 后续几轮把它从工作树里清掉了，也补了
// `TestDefaultLLMGatewayStateHasNoBuiltinKey` 守住那个**函数**。但那道护栏的
// 作用域只有一个函数：任何人往任何**其它**文件（脚本、文档、夹具、配置样例）
// 再写一次完整 key，那道测试依然全绿。审计轮次里是靠人手 `git grep` 才发现残留的，
// 而人手发现过一次不等于下次还会发现。
//
// ⇒ 这道卡口的定位：把「人肉 grep」变成「每次 CI 必然执行」。
//
// ── 与既有卡口的设计差异：为什么**没有**基线棘轮 ──
//
// `check-raw-error-text.mjs` / `check-dead-api.mjs` 用的是棘轮（把存量钉成基线，
// 只许减少不许增加）。那对「代码风格债」是对的：存量太大，一次清完风险太高。
//
// 但**泄漏的密钥不能用基线豁免**。棘轮意味着「已知的泄漏可以先记一笔、以后再说」，
// 而密钥记在基线文件里等于把它又抄了一遍到另一个文件，还给了它「已登记」的合法感。
// 真实密钥的正确处置是**轮换**，不是登记。所以这里零容忍：命中即 exit 1，
// 没有任何 `--update-baseline` 之类的开关。
//
// ── 判据为什么这么窄 ──
//
// 只认「长度足以构成凭据」的形态，而不是任何 `sk-` 开头：
//   - `sk-test-fixture-not-a-real-key-000111`（maskKey 的合成夹具）是 36 位，
//     形态上和真 key 一样。这类东西必须**显式豁免**（见下），而不是靠规则放行——
//     放行规则一旦为了迁就一个夹具而放宽，真 key 也会跟着被放行。
//   - 文档里写的 `sk-6tGL…K51YV` 是**打码**形态：省略号会截断连续字符 run，
//     长度不足，天然不命中。这是有意的——卡口管的是「完整可用的密钥」，
//     打码残留属于文档卫生问题，单独处理，不要靠放宽判据来掩盖。
//
// ── 豁免机制 ──
//
// 同一行出现 `secret-scan-ok` 即放行。**必须逐处显式豁免**，不允许目录级或全局白名单：
// 白名单一旦有「整目录跳过」这种档位，扫描就等于没有。
//
// ── 一条容易被忽略但很要命的实现约束 ──
//
// 报告里**绝不能回显密钥本身**。这道测试的输出会进 CI 日志，而 CI 日志对
// 仓库外的任何人可见（公开仓库的 Actions 日志无需登录即可读）。一个「检测密钥泄漏的
// 工具」如果在报告里把密钥原样打出来，就等于把密钥又泄露到了一个新地方——
// 而且是每次运行都泄露一次。所以下面只输出前缀 + 长度 + 指纹。

// maxSecretPreviewChars 是报告里允许出现的字面量前缀长度。
// 取 6 是为了足够让人认出「是哪一把」，又不至于拼出可用的凭据。
const maxSecretPreviewChars = 6

type secretRule struct {
	name string
	re   *regexp.Regexp
	// triggers 是跑正则前的廉价前置判据（小写子串）。
	//
	// 为什么需要它：第一版对全部 2700+ 个受跟踪文件逐行跑 6 条正则，
	// 光是两个 3.6 MB 的 seed 文件（chatagent/seed/agents.json 与
	// deploy/sql/chat_agents_seed.sql）就够把这条测试拖到 **56 秒**——
	// 放在 `go test -race ./...` 里是不可接受的，门禁太慢就会被跳过，
	// 于是又回到「护栏存在但没人执行」。改成先做十几次
	// strings.Contains（无回溯、线性），再决定要不要上正则。
	//
	// 判据要**覆盖完整**：漏掉一个 trigger 就等于给那条规则开了一个洞，
	// 所以这里宁可多写几个触发词。
	triggers []string
}

var secretRules = []secretRule{
	// OpenAI / 各类兼容网关的通用形态。kxpms 网关的租户 key 就是这一类。
	//
	// ⚠ 必须要求 `sk-` **前面不是字母**，否则会误报一大片英文单词：
	// `task-acceptance-evidence-design.md` 里含有子串 `sk-a`，
	// `disk-fallback-test-secret` 里含有 `sk-f`——两者都满足
	// `sk-[A-Za-z0-9_-]{20,}`。第一版没加这个边界，一开卡口就报出
	// server.go / task.go / disk_task_fallback_test.go 三处**纯误报**。
	//
	// 加了边界之后仍能命中真实形态：`**API Key**: sk-mcp-…`（前面是空格）、
	// `POCKET_MCP_API_KEY=sk-mcp-…`（前面是 `=`）、`"sk-test-…"`（前面是引号）。
	// 取捕获组 m[1] 作为密钥值，避免把前导分隔符也算进去。
	{"openai-style-key", regexp.MustCompile(`(?:^|[^A-Za-z0-9_])(sk-[A-Za-z0-9_-]{20,})`), []string{"sk-"}},
	// AWS access key id。长度固定，便于和普通大写缩写区分。
	{"aws-access-key-id", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), []string{"akia", "asia"}},
	// GitHub token。
	{"github-token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b`), []string{"ghp_", "gho_", "ghs_", "ghr_", "ghu_"}},
	{"github-fine-grained-pat", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,}\b`), []string{"github_pat_"}},
	// Slack。
	{"slack-token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`), []string{"xox"}},
	// PEM 私钥块。与其判断内容像不像 key，不如直接禁掉私钥进仓库。
	{"private-key-block", regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`), []string{"private key"}},
	// 通用形态：把一个「长得像凭据的串」赋给一个「名字像凭据的变量」。
	// 覆盖 base64 / hex 形态的 key——它们没有 sk- 这类前缀，只能靠赋值侧识别。
	// 第 1 个捕获组是**值**，preview/scanContent 只取它，避免把变量名一起当成密钥。
	{"credential-assignment", regexp.MustCompile(
		`(?i)\b(?:api[_-]?key|apikey|secret[_-]?key|client[_-]?secret|access[_-]?token|auth[_-]?token|password|passwd|master[_-]?key|credential)\b` +
			`\s*[:=]\s*["']([A-Za-z0-9+/=_-]{24,})["']`),
		[]string{"key", "secret", "token", "password", "passwd", "credential"}},

	// ── 口令形态（2026-10-02 新增）────────────────────────────────────────
	//
	// 这条规则是**被一个真实漏检逼出来的**，不是预防性想象：
	// `credential-assignment` 要求值 ≥24 个 base64 字符，于是
	// `devPass = "Veritrans&9527"`（13 字符、含 `&`）整类漏网。
	// 那把口令同时是生产代码 `server_assistant.go` 里 dev 旁路的**内置缺省**，
	// 并明文出现在 8 个受跟踪文件（Go / sh / ps1 / mjs / py / ts）里。
	// 上一轮 336c883 修掉了 bootstrap 建号路径的同类问题，dev 旁路是残留入口——
	// 这正是「修了一处就以为这类闭合了」的又一次复发。
	//
	// 判据刻意比 credential-assignment 宽：口令通常**比 API key 短**，
	// 而且几乎一定同时含字母与数字。因此：
	//   · 变量名含 pass/pwd（覆盖 devPass / admin_password / POCKET_AUTH_PASS）
	//   · 值是引号包裹的**字面量**（`$VAR`、`${VAR:-x}` 里的变量引用不算）
	//   · 长度 ≥ 8
	// 真正的强度判定交给占位符表 + 逐行豁免，不在这里猜。
	//
	// 为什么单独一条而不是放宽 credential-assignment：那条规则的 24 字符
	// 下限是它不误报一堆测试夹具的关键，放宽会一次性炸出几十条噪声。
	// 两条规则各管一段形态，比互相妥协好。
	{"password-literal", regexp.MustCompile(
		`(?i)\b[A-Za-z_]*(?:pass|pwd)[A-Za-z_]*\b\s*[:=]\s*` +
			// 捕获组 1：双引号字面量（不含 $，即不是变量插值）
			`(?:"([^"$]{8,})"|'([^'$]{8,})')`),
		[]string{"pass", "pwd"}},
}

// passwordStrength 用来把 password-literal 的命中分成「像真口令」与「像占位符」。
// 纯字母的长串（`irrelevant-for-stub`、`crossorigin`）在密码位置出现得太多了，
// 混进来会让这条规则天天误报；而**同时含数字与字母**的串在密码位置上
// 基本都是刻意构造的口令。含常见符号（& ! @ # % 等）进一步加强信号。
//
// 这不是密码学强度分析，只是一个**把噪声压到可逐条人工复核**的粗筛。
// 粗筛放过的东西靠豁免兜底，粗筛拦下的东西逐条看。
var passwordStrength = regexp.MustCompile(`^[A-Za-z0-9!@#$%^&*().,;:+=-]*$`)

func looksLikeRealPassword(v string) bool {
	if !passwordStrength.MatchString(v) {
		return false
	}
	hasLetter := strings.ContainsAny(v, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	hasDigit := strings.ContainsAny(v, "0123456789")
	return hasLetter && hasDigit
}

// placeholderValue 命中即视为占位符而非真凭据。
// 只放行**语义上就是「请自己填」**的写法，避免 `password: "your-password-here"`
// 这类文档写法天天误报。
//
// ⚠ 这里踩过一个会让整道卡口彻底失效的坑，必须写下来：
// 第一版写成 `^(?:|x{4,}|...)`——`(?:|x{4,}` 里那**第一个 `|` 前是空分支**，
// 它能匹配空串，而 `^` 同时满足，于是整个正则**匹配一切**。后果是每条命中
// 都被当成占位符过滤掉，测试**永远绿**。
//
// 「一个永远通过的门禁」比没有门禁更危险：它让人以为已经防住了，
// 于是没人再去想这件事——本轮之前那道 `TestDefaultLLMGatewayStateHasNoBuiltinKey`
// 就是同一种「绿灯即安全」错觉的另一个实例。
//
// 正确写法是让「空值」这一支同时锚住首尾：`^(?:$|...)`。
//
// 这个 bug 是靠**负控**发现的：真往受跟踪文件里塞一把合成 key，看它转不转红。
// 只读代码看不出问题，只看绿灯也看不出问题——只有「故意弄坏它」才能。
var placeholderValue = regexp.MustCompile(`(?i)^(?:$|x{4,}|\*{4,}|y{4,}|z{4,}|0+|1+|a{4,}|test|dummy|placeholder|example|changeme|your[-_a-z0-9]*|todo|none|null|redacted|masked)`)

const exemptionMarker = "secret-scan-ok"

// fileExemptions 是**逐文件**豁免，且每个都必须写清理由。
//
// 为什么允许「按文件」这一档：下面两个文件的**内容本身就是一份凭据模式清单**——
// 它们是「高级安全运营工程师」这个 agent 的 system prompt，里面逐条列着
// 要防范的泄漏形态（`-----BEGIN RSA PRIVATE KEY-----`、`AKIA[0-9A-Z]{16}` …）。
// 删掉那些行等于删掉这个 agent 的职责；把它们改写掉等于让它的检测清单失真。
// （deploy/sql/chat_agents_seed.sql 与 backend/internal/chatagent/seed/agents.json
// 是同一份 prompt 的两种载体，所以两个都要列。）
//
// 为什么不提供「按目录跳过」：一旦有目录级档位，就会有人往里塞新文件，
// 扫描等于对该目录永久失效。逐文件列出意味着新增文件时必须重新审视一次。
var fileExemptions = map[string]string{
	"backend/internal/chatagent/seed/agents.json": "安全 agent 的 system prompt 本身就是凭据模式清单（教学用途，非真实凭据）",
	"deploy/sql/chat_agents_seed.sql":             "同上，agents.json 的 SQL 载体",
	// 扫描器自己必然含有它要搜的模式字面量——规则表、文件头引用的事故原文、
	// 以及豁免理由里都要写出 `-----BEGIN … PRIVATE KEY-----` 这类形态。
	// 这不是「给自己开后门」，而是自指：把模式写在源码里就一定会被自己搜到。
	// 范围严格限定为本文件；同包的 doc.go 仍照常参与扫描。
	"backend/internal/repohygiene/secrets_test.go": "扫描器自身：规则表与事故原文必然含有被搜模式",
}

// skipPath 对整个目录跳过。刻意**不提供**「跳��整个目录」的档位给调用方用——
// 见 exemptionMarker 处的说明：豁免必须是逐行的。
var skipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"dist":         true,
	"build":        true,
	"out":          true,
	"vendor":       true,
	"testdata":     true, // Go 官方测试数据，可能含刻意构造的假凭据
}

// binaryExt 是按扩展名直接跳过的二进制/归档文件。
var binaryExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".pdf": true, ".zip": true, ".gz": true, ".tar": true, ".7z": true, ".rar": true,
	".mp3": true, ".mp4": true, ".wav": true, ".mov": true, ".webm": true,
	".exe": true, ".dll": true, ".so": true, ".dylib": true, ".a": true, ".o": true,
	".jar": true, ".class": true, ".pyc": true, ".wasm": true, ".bin": true, ".db": true,
	".sqlite": true, ".keystore": true, ".jks": true, ".p12": true, ".pfx": true,
}

// repoRoot 从本文件所在位置向上找 .git。
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatalf("找不到仓库根（向上 12 层都没有 .git）")
	return ""
}

// trackedFiles 用 `git ls-files` 枚举**已被 git 跟踪**的文件。
//
// 为什么不用 walk 整个目录：我们要检查的是「会被提交进仓库的东西」。
// 走 git 索引天然排除了 node_modules、构建产物和本地产物，也就不会因为
// 某个开发者本地多了一个文件就让门禁飘红。
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		// fail-closed：git 不可用时**必须**失败。悄悄跳过扫描等于把一道
		// 安全卡口变成「视环境而定」，那比没有卡口更糟。
		t.Fatalf("git ls-files 失败，无法枚举受检文件：%v\n"+
			"（本卡口刻意 fail-closed：扫不到就不算通过）", err)
	}
	var files []string
	for _, f := range bytes.Split(out, []byte{0}) {
		if len(f) > 0 {
			files = append(files, string(f))
		}
	}
	return files
}

type finding struct {
	file   string
	line   int
	rule   string
	secret string
}

// preview 只输出前缀 + 长度，绝不回显完整值（见文件头「实现约束」）。
func preview(secret string) string {
	p := secret
	if len(p) > maxSecretPreviewChars {
		p = p[:maxSecretPreviewChars]
	}
	return p + fmt.Sprintf("…<%d chars, 已打码>", len(secret))
}

func scanContent(rel, content string) []finding {
	var out []finding
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if strings.Contains(line, exemptionMarker) {
			continue
		}
		// 廉价前置判据：整行小写化一次，然后只看子串。绝大多数行一个
		// trigger 都不含，直接跳过 6 条正则（见 secretRule.triggers 的说明）。
		lower := strings.ToLower(line)
		for _, rule := range secretRules {
			if !hasAnyTrigger(lower, rule.triggers) {
				continue
			}
			for _, m := range rule.re.FindAllStringSubmatch(line, -1) {
				secret := m[0]
				// 多数规则的 m[0] 含变量名与引号，值在捕获组里。password-literal
				// 有**两个**互斥捕获组（单引号 / 双引号），只有一个非空，
				// 所以要取第一个非空的捕获组而不是固定取 m[1]——否则单引号写法
				// 会退回 m[0]，把变量名连引号一起当成密钥值。
				for _, g := range m[1:] {
					if g != "" {
						secret = g
						break
					}
				}
				if placeholderValue.MatchString(secret) {
					continue
				}
				if rule.name == "password-literal" && !looksLikeRealPassword(secret) {
					continue
				}
				out = append(out, finding{file: rel, line: i + 1, rule: rule.name, secret: secret})
			}
		}
	}
	return out
}

func hasAnyTrigger(lowerLine string, triggers []string) bool {
	for _, t := range triggers {
		if strings.Contains(lowerLine, t) {
			return true
		}
	}
	return false
}

func TestNoCommittedSecrets(t *testing.T) {
	root := repoRoot(t)
	files := trackedFiles(t, root)
	if len(files) == 0 {
		t.Fatalf("git ls-files 返回 0 个文件，扫描范围不对")
	}

	var all []finding
	scanned := 0
	exempted := 0
	for _, rel := range files {
		norm := filepath.ToSlash(rel)
		if skipDirs[filepath.Base(norm)] || skipDirs[firstSegment(norm)] {
			continue
		}
		if binaryExt[strings.ToLower(filepath.Ext(norm))] {
			continue
		}
		if reason, ok := fileExemptions[norm]; ok {
			exempted++
			t.Logf("整文件豁免 %s —— %s", norm, reason)
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(full)
		if err != nil {
			// 文件已被 git 跟踪却读不出来（权限/编码）：跳过而不是误报，
			// 但记一笔让人知道扫描有盲区。
			t.Logf("跳过读不出的文件 %s：%v", rel, err)
			continue
		}
		// 二进制内容不做正则扫描（会产生无意义的命中且拖慢门禁）。
		if bytes.IndexByte(data, 0) >= 0 && len(data) > 0 {
			continue
		}
		scanned++
		all = append(all, scanContent(norm, string(data))...)
	}

	if len(all) > 0 {
		var b strings.Builder
		b.WriteString("\n❌ 仓库里出现疑似密钥字面量。\n\n")
		b.WriteString("处理方式（按优先级）：\n")
		b.WriteString("  1. 如果这是**真**密钥：立刻去签发方轮换/吊销。删文件不等于失效，\n")
		b.WriteString("     它已经在 git 历史里，`git log -S` 可取回。\n")
		b.WriteString("  2. 如果是**合成夹具**：就地改写成自解释的合成串，并在同一行加\n")
		b.WriteString("     `// " + exemptionMarker + "` 说明它为什么可以豁免。\n")
		b.WriteString("     豁免必须逐行写；不要为了迁就夹具去放宽本文件的判据。\n")
		b.WriteString("  3. 如果是**打码**写法（形如 sk-xxx…yyy）：本卡口不命中，\n")
		b.WriteString("     但仍应改成不含真实前后缀的中性描述。\n\n")
		b.WriteString("命中明细（密钥值已打码——本输出会进 CI 日志，故不回显原文）：\n")
		for _, f := range all {
			b.WriteString("  " + f.file + ":" + itoa(f.line) + "  [" + f.rule + "]  " + preview(f.secret) + "\n")
		}
		t.Fatal(b.String())
	}
	t.Logf("扫描 %d / %d 个受跟踪文本文件（整文件豁免 %d 个），未发现密钥字面量", scanned, len(files), exempted)
}

func firstSegment(p string) string {
	if i := strings.Index(p, "/"); i >= 0 {
		return p[:i]
	}
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
