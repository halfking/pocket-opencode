// 定位用探针（默认 skip）：网关在 stream=true 下对本项目请求形态返回
// **200 + 零帧**，导致 auto 回退链烧完 90s 预算后必然失败。
// 本探针用**一次只变一个变量**的方式，把是哪一项把它弄死的分离出来。
//
// 为什么必须查清：这是 §12.4 第 7 条（精校长会超时）唯一的解法方向。
// 非流式下响应头要等生成全部完成才到，ResponseHeaderTimeout: 60s 因此变成
// 「整段生成必须 60s 内跑完」—— §48 实测长会 60.581s 正好撞线失败。
//
// 已知的两个候选（本探针要区分的）：
//
//	A 最小流式请求：model + messages + stream=true
//	B A + stream_options.include_usage=true（Go 客户端**固定**会加这一项）
//	C A + work_type 与 X-Gw-Work-Type 请求头（llmChatOnce 的 kind="meeting" 会带）
//
// 默认 skip：消耗真实额度。显式开关：
//
//	POCKET_LIVE_GATEWAY=1 POCKET_LLM_GATEWAY_URL=... POCKET_LLM_GATEWAY_API_KEY=... \
//	go test ./internal/server -run TestDiagStreamZeroFrames -v
package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/halfking/pocket-opencode/backend/internal/livetest"
)

func TestDiagStreamZeroFrames(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("设 POCKET_LIVE_GATEWAY=1 才跑（消耗真实额度）")
	}
	base := strings.TrimRight(os.Getenv("POCKET_LLM_GATEWAY_URL"), "/")
	key := os.Getenv("POCKET_LLM_GATEWAY_API_KEY")
	if base == "" || key == "" {
		t.Fatal("缺 POCKET_LLM_GATEWAY_URL / _API_KEY")
	}

	// 与真网关探针同一份**短会**精校提示词（产品真实发出形态，由
	// live_refine_prompt_dump_test.go 导出），避免长度把变量搅进来。
	promptBytes, err := os.ReadFile("/tmp/real-refine-prompt.txt")
	if err != nil {
		promptBytes = []byte("请回复两个字：好的")
		fmt.Printf("DIAG ⚠ 读不到 /tmp/real-refine-prompt.txt（先跑 TestDumpRefinePromptForLiveProbe），改用极短提示词\n")
	}

	arms := []struct {
		name    string
		extra   map[string]any
		headers map[string]string
	}{
		{"A 最小流式", map[string]any{}, nil},
		{"B A+stream_options", map[string]any{"stream_options": map[string]bool{"include_usage": true}}, nil},
		{"C A+work_type", map[string]any{"work_type": "meeting"}, map[string]string{"X-Gw-Work-Type": "meeting"}},
		{"D B+C 一起（=Go 客户端真实形态）",
			map[string]any{"stream_options": map[string]bool{"include_usage": true}, "work_type": "meeting"},
			map[string]string{"X-Gw-Work-Type": "meeting"}},
	}

	// ⭐ §492：4 个臂循环 + 请求失败 continue + 逐臂 Printf，而**末尾没有任何汇总**
	// ⇒ C/D 臂失败时读者只会看到 2 行，且退出码是 0 ——
	// 「少跑了」与「就这 2 个臂」在读数里**完全同形**。
	//
	// ⚠ total 用 `len(arms)` 而不是写死 4：加臂时它自动跟着涨
	// （写死的数字会在下次加臂时静默过期 —— 那正是量具自证的反面）。
	ledger := livetest.New("流式零帧诊断", len(arms))

	// ⚠⚠ §492：逐臂读数**先攒起来**，对账之后再打印。
	//
	// 原因与 §491 修 FullVsChunked 完全一样：那一行
	// `fmt.Printf("DIAG ... status=...")` 在循环**内**，
	// 而账本只有循环结束后才知道跑没跑完 ⇒ 任何循环后的 `Report()` 都必然太晚。
	// ⇒ 修法不是把 Report 往前挪（不可能），而是**推迟打印**。
	var lines []string

	for _, a := range arms {
		payload := map[string]any{
			"model":    "auto",
			"messages": []map[string]string{{"role": "user", "content": string(promptBytes)}},
			"stream":   true,
		}
		for k, v := range a.extra {
			payload[k] = v
		}
		body, _ := json.Marshal(payload)

		ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
		req, _ := http.NewRequestWithContext(ctx, "POST", base+"/chat/completions", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("Accept", "text/event-stream")
		for k, v := range a.headers {
			req.Header.Set(k, v)
		}

		st := time.Now()
		resp, err := (&http.Client{Timeout: 70 * time.Second}).Do(req)
		if err != nil {
			lines = append(lines, fmt.Sprintf("DIAG %-28s 请求失败 %s err=%v",
				a.name, time.Since(st).Round(time.Millisecond), err))
			cancel()
			continue
		}
		// ★ 关键量：响应头多久到。它决定 ResponseHeaderTimeout 会不会先砍一刀。
		hdrAt := time.Since(st)
		firstContent := time.Duration(0)
		frames, dataFrames := 0, 0
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			frames++
			if strings.HasPrefix(line, "data:") {
				dataFrames++
				if strings.TrimSpace(line[5:]) != "[DONE]" && firstContent == 0 {
					firstContent = time.Since(st)
				}
			}
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		cancel()

		lines = append(lines, fmt.Sprintf("DIAG %-28s status=%d 响应头=%s 首帧data=%s 总帧=%d data帧=%d",
			a.name, resp.StatusCode, hdrAt.Round(time.Millisecond),
			durStr(firstContent), frames, dataFrames))
		ledger.Complete(a.name)
	}

	// ⚠⚠ §492：逐臂 Printf 跑完之后**必须**对账。
	//
	// 没有它的话，失败臂只留一行「DIAG ... 请求失败」，
	// 而那行与「这一臂没有异常」的输出**长得不一样却同样安静** ——
	// 读者必须自己数行数才知道少跑了。
	//
	// ⚠ 本探针用 `fmt.Printf`（不是 t.Logf），所以它**不参与 -v 输出**，
	//   是给人眼看时间戳的诊断工具 ⇒ 汇总行必须显式打出臂数。
	if msg := ledger.Report(); msg != "" {
		t.Error(msg)
	}
	for _, line := range lines {
		fmt.Println(line)
	}
	fmt.Printf("DIAG 汇总：%d/%d 臂跑完（失败臂只留一行『请求失败』，不是结论）\n",
		ledger.Done(), ledger.Total())
}

func durStr(d time.Duration) string {
	if d == 0 {
		return "无(零帧)"
	}
	return d.Round(time.Millisecond).String()
}
