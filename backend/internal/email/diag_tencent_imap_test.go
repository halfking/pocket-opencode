package email

// diag_tencent_imap_test.go — 腾讯系 IMAP「login 挂 80s」的裸连接对照实验。
//
// 现象（生产日志，多次复现）：只有腾讯系两个账户会挂，且每次都精确 ~80.2s，
// 卡在 `imap login ... cannot read tag: ... i/o timeout`；163 的三个账户一次
// 都没有。已在包外复现：连续跑流水线时，5 轮里有 2 轮会出现 80.5s /
// synced=4（一个账户失败），其余轮次 1.3~1.9s / synced=5。
//
// 已知排除（都有对照实验，不是推理）：凭证错、单个 IP 有问题、服务端限流、
// 并发压力、两实例抢连接。剩下的可能主要是两类：
//   A. 服务端/中间设备在特定条件下真的不回应 LOGIN；
//   B. 我们自己的连接处理（deadlineConn / TLS / go-imap 读响应）有问题，
//      比如**服务端的 greeting 迟到**，go-imap 把 greeting 当成了别的命令的
//      响应，于是报 "cannot read tag"。
//
// **本文件就是用来分辨 A 和 B 的**：它绕开 fetcher 的全部封装，用最朴素的
// tls.Dial + 逐行读 + 手写 LOGIN，重复多次并逐步打点。如果裸客户端也会挂，
// 那问题在服务端/网络（B 被排除）；如果裸客户端几十次都正常，那问题在我们
// 自己的连接层，值得继续往 deadlineConn / greeting 解析上查。
//
// 刻意**不用** imapDialWithIdle/deadlineConn：这个实验的全部意义就是
// 「去掉我们自己的那套封装」。
//
// 需要显式指定才跑（会拿真实账号做多次登录尝试）：
//
//	KXPMS_DIAG_USER=huangxutao@kxpms.cn KXPMS_DIAG_PASS=<密码> \
//	  go test ./internal/email/ -run TestDiagTencentIMAPBareLogin -v -count=1
//
// 不设环境变量就跳过。次数默认 15，可用 KXPMS_DIAG_N 调整。
// 刻意保持低频（默认每次间隔 1s）——这是在真实用户邮箱上做登录尝试，
// 频率高了有触发风控/锁定的风险，宁可样本少也不要影响账号可用性。

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDiagTencentIMAPBareLogin(t *testing.T) {
	user := os.Getenv("KXPMS_DIAG_USER")
	pass := os.Getenv("KXPMS_DIAG_PASS")
	if user == "" || pass == "" {
		t.Skip("KXPMS_DIAG_USER / KXPMS_DIAG_PASS not set; skipping bare IMAP login diagnostic")
	}
	host := "imap.exmail.qq.com"
	if v := os.Getenv("KXPMS_DIAG_HOST"); v != "" {
		host = v
	}
	addr := host + ":993"
	n := 15
	if v := os.Getenv("KXPMS_DIAG_N"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}

	slow := 0
	for i := 1; i <= n; i++ {
		dialStart := time.Now()
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Logf("#%02d dial FAILED after %s: %v", i, time.Since(dialStart).Round(time.Millisecond), err)
			slow++
			time.Sleep(time.Second)
			continue
		}
		dialMS := time.Since(dialStart).Round(time.Millisecond)

		// 关键：先单独读 greeting，并给它一个明确上限。生产里这一步是被
		// deadlineConn 的滚动 idle deadline 罩着的，出问题时看不出是
		// greeting 没来还是 LOGIN 没回 —— 这里把两者拆开。
		greetingStart := time.Now()
		_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		br := bufio.NewReader(conn)
		if _, err := br.ReadString('\n'); err != nil {
			greetingMS := time.Since(greetingStart).Round(time.Millisecond)
			t.Logf("#%02d dial=%s GREETING FAILED after %s: %v", i, dialMS, greetingMS, err)
			slow++
			_ = conn.Close()
			time.Sleep(time.Second)
			continue
		}
		greetingMS := time.Since(greetingStart).Round(time.Millisecond)

		loginStart := time.Now()
		_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
		if _, err := fmt.Fprintf(conn, "a1 LOGIN %s %s\r\n", user, pass); err != nil {
			t.Logf("#%02d dial=%s greeting=%s LOGIN WRITE FAILED: %v", i, dialMS, greetingMS, err)
			slow++
			_ = conn.Close()
			time.Sleep(time.Second)
			continue
		}
		resp, err := br.ReadString('\n')
		loginMS := time.Since(loginStart).Round(time.Millisecond)
		if err != nil {
			t.Logf("#%02d dial=%s greeting=%s LOGIN READ FAILED after %s: %v",
				i, dialMS, greetingMS, loginMS, err)
			slow++
			_ = conn.Close()
			time.Sleep(time.Second)
			continue
		}
		tagOK := strings.Contains(resp, "a1 ")
		if !tagOK {
			t.Logf("#%02d dial=%s greeting=%s LOGIN NO TAG after %s: %q",
				i, dialMS, greetingMS, loginMS, strings.TrimSpace(resp))
		}
		if loginMS >= 2*time.Second {
			slow++
			t.Logf("#%02d dial=%s greeting=%s LOGIN SLOW=%s resp=%q", i, dialMS, greetingMS, loginMS, strings.TrimSpace(resp))
		} else {
			t.Logf("#%02d dial=%s greeting=%s login=%s ok=%v", i, dialMS, greetingMS, loginMS, tagOK)
		}
		// 登出后关闭，尽量不给服务端留下悬挂会话。
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = fmt.Fprintf(conn, "a2 LOGOUT\r\n")
		_ = conn.Close()
		time.Sleep(time.Second)
	}
	t.Logf("=== 共 %d 次，异常/慢 %d 次 ===", n, slow)
	if slow == 0 {
		t.Logf("裸客户端 %d 次全部正常 → deadlineConn/go-imap 这条路(B)基本可以排除，"+
			"问题更可能在服务端或中间设备(A)。", n)
	}
	_ = pass
}

// TestDiagTencentIMAPStackAB 是上一个测试的**另一半**，也是真正能定位到
// 哪一层的那个：同一个进程、同样的节奏，把两种客户端各跑一遍。
//
//   - bare  模式：tls.Dial + 手写 LOGIN（上一个测试）
//   - stack 模式：生产用的 imapDialWithTimeout（TLS + deadlineConn 看门狗）
//   - go-imap 的 client.Login
//
// 2026-10-01 实测：bare 15/15 全正常（dial ~130ms / greeting ~130ms /
// login ~650ms），而同一时间段生产链路里腾讯系账户 5 轮流水线有 2 轮挂在
// `imap login ... cannot read tag` 上整整 80s。
//
// 所以关键问题变成：**差别在连接层，还是在 stack 这一层？**
//   - stack 也挂 → 问题在我们自己的封装（deadlineConn 的看门狗、TLS 配置、
//     或 go-imap 的响应解析），值得往这三处查；
//   - stack 也全正常 → 说明单独复现不了，得回到「并发 + 长时间持续运行」
//     这些生产才有的条件上查，也就是触发条件还没抓到。
//
// 无论哪种结果，都比只跑一个模式多排除一整类原因。
//
// 同样需要显式指定才跑，理由与默认次数见上。
func TestDiagTencentIMAPStackAB(t *testing.T) {
	user := os.Getenv("KXPMS_DIAG_USER")
	pass := os.Getenv("KXPMS_DIAG_PASS")
	if user == "" || pass == "" {
		t.Skip("KXPMS_DIAG_USER / KXPMS_DIAG_PASS not set; skipping IMAP stack A/B diagnostic")
	}
	host := "imap.exmail.qq.com"
	if v := os.Getenv("KXPMS_DIAG_HOST"); v != "" {
		host = v
	}
	addr := host + ":993"
	n := 10
	if v := os.Getenv("KXPMS_DIAG_N"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}

	// 阶段一：生产 stack（TLS + deadlineConn 看门狗 + go-imap）。
	stackSlow := 0
	for i := 1; i <= n; i++ {
		start := time.Now()
		// 10s 与生产 f.dial() 里的局部常量 dialTimeout 保持一致。
		client, err := imapDialWithTimeout(addr, true, 10*time.Second, &tls.Config{
			ServerName: host, MinVersion: tls.VersionTLS12,
		})
		if err != nil {
			stackSlow++
			t.Logf("stack #%02d dial failed after %s: %v", i, time.Since(start).Round(time.Millisecond), err)
			time.Sleep(time.Second)
			continue
		}
		if err := client.Login(user, pass).Wait(); err != nil {
			stackSlow++
			t.Logf("stack #%02d LOGIN failed after %s: %v", i, time.Since(start).Round(time.Millisecond), err)
			_ = client.Close()
			time.Sleep(time.Second)
			continue
		}
		d := time.Since(start).Round(time.Millisecond)
		if d >= 2*time.Second {
			stackSlow++
			t.Logf("stack #%02d SLOW %s", i, d)
		} else {
			t.Logf("stack #%02d ok %s", i, d)
		}
		_ = client.Close()
		time.Sleep(time.Second)
	}
	t.Logf("=== stack 模式：%d 次，异常/慢 %d 次 ===", n, stackSlow)
	if stackSlow == 0 {
		t.Logf("生产 stack %d 次也全正常 → 单独复现不了，"+
			"触发条件还缺「并发」或「长时间持续运行」这类生产特有的因素。", n)
	}
	_ = addr
}

// TestDiagTencentIMAPPerNodeColdStart 顺着「第一次会挂」的线索往下查：
// **是不是首次连上某个后端节点才会挂？**
//
// 依据：2026-10-01 stack A/B 实验里，stack 模式的**第 1 次**挂了 80.17s、
// 后面 9 次（间隔 1s）全部 774~884ms 正常；生产日志里挂起分散在各个时刻而不是
// 集中在启动后，说明是「首次接触某个节点」而不是「进程刚启动」。
//
// 腾讯企业邮的 IMAP 是多节点轮询（同一域名 DNS 出多个 IP，生产日志里同一个
// 账户先后连过 120.226.165.33 与 112.49.56.212），而 163 看起来是单节点——
// 这恰好能解释「只有腾讯系会挂、163 一次都没有」。
//
// 做法：把域名解析出来的每个 IP 都显式 dial 两遍（第一遍=冷、第二遍=热），
// 用生产 stack 打点。如果是「冷启动才挂」，第一遍慢/挂、第二遍正常，
// 根因就落在服务端按节点的冷启动/限流上，而**不是**我们的客户端；
// 缓解手段也随之明确（连接复用、或失败后换一个 IP 重试）。
//
// 同样需要显式指定才跑。
func TestDiagTencentIMAPPerNodeColdStart(t *testing.T) {
	user := os.Getenv("KXPMS_DIAG_USER")
	pass := os.Getenv("KXPMS_DIAG_PASS")
	if user == "" || pass == "" {
		t.Skip("KXPMS_DIAG_USER / KXPMS_DIAG_PASS not set; skipping per-node cold start diagnostic")
	}
	host := "imap.exmail.qq.com"
	if v := os.Getenv("KXPMS_DIAG_HOST"); v != "" {
		host = v
	}
	ips, err := net.LookupHost(host)
	if err != nil {
		t.Skipf("cannot resolve %s: %v", host, err)
	}
	t.Logf("=== %s 解析出 %d 个 IP: %v ===", host, len(ips), ips)

	for _, ip := range ips {
		addr := net.JoinHostPort(ip, "993")
		for round := 1; round <= 2; round++ {
			kind := "冷(首次)"
			if round == 2 {
				kind = "热(第二次)"
			}
			start := time.Now()
			client, derr := imapDialWithTimeout(addr, true, 10*time.Second, &tls.Config{
				// 显式指定 ServerName：按 IP 连但证书仍按域名校验。
				ServerName: host, MinVersion: tls.VersionTLS12,
			})
			if derr != nil {
				t.Logf("%s %s dial failed after %s: %v", ip, kind, time.Since(start).Round(time.Millisecond), derr)
				time.Sleep(2 * time.Second)
				continue
			}
			lerr := client.Login(user, pass).Wait()
			d := time.Since(start).Round(time.Millisecond)
			if lerr != nil {
				t.Logf("%s %s LOGIN FAILED after %s: %v", ip, kind, d, lerr)
			} else {
				t.Logf("%s %s ok %s", ip, kind, d)
			}
			_ = client.Close()
			time.Sleep(2 * time.Second)
		}
	}
}
