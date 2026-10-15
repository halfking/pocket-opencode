// 长会流式探针（默认 skip）。回答 §12.4 第 7 条**最后一个未决问题**：
// 消费流式到底能不能让一场 32 分钟会议的精校**跑完**。
//
// 为什么必须单独量：§48.4 的四臂里，**成功那次（D）用的是短会提示词**
// （/tmp/real-refine-prompt.txt，1842B），它的 55.667s 首帧 data 不代表长会。
// 我在 §48.5 里却把「长会总耗时仍会顶到 90s」写成了读数 —— 那是**推断**，
// 而这一轮我栽的两次跟头都栽在「读数不足却写成结论」。
//
// 对照臂就是 §12.4 第 7 条要解决的那个事实：
//
//	非流式长会  → 60.581s 撞 ResponseHeaderTimeout 失败（已实测）
//
// 本探针若能让长会流式**跑完并给出总耗时**，流式方向就是有希望的；
// 若它也越不过 90s，那两条路都不通，结论就变成「不是传输方式的问题」。
//
// 请求形态取 §48.4 的 D 臂（= Go 客户端真实形态，唯一一次成功的那组）。
// 默认 skip：消耗真实额度。
//
//	POCKET_LIVE_GATEWAY=1 POCKET_LLM_GATEWAY_URL=... POCKET_LLM_GATEWAY_API_KEY=... \
//	go test ./internal/server -run TestLiveLongStream -v
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
)

func TestLiveLongStream(t *testing.T) {
	if os.Getenv("POCKET_LIVE_GATEWAY") != "1" {
		t.Skip("设 POCKET_LIVE_GATEWAY=1 才跑（消耗真实额度）")
	}
	base := strings.TrimRight(os.Getenv("POCKET_LLM_GATEWAY_URL"), "/")
	key := os.Getenv("POCKET_LLM_GATEWAY_API_KEY")
	if base == "" || key == "" {
		t.Fatal("缺 POCKET_LLM_GATEWAY_URL / _API_KEY")
	}

	promptBytes, err := os.ReadFile("/tmp/real-refine-prompt-long.txt")
	if err != nil {
		t.Fatalf("读不到长会提示词（先跑 TestDumpRefinePromptForLiveProbe）：%v", err)
	}
	fmt.Printf("LONG_STREAM 提示词 %d 字节\n", len(promptBytes))

	payload := map[string]any{
		"model":          "auto",
		"messages":       []map[string]string{{"role": "user", "content": string(promptBytes)}},
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
		"work_type":      "meeting",
	}
	body, _ := json.Marshal(payload)

	// 超时给 150s：要把「跑完要多久」量出来，就不能被 90s 的产品上限截断，
	// 否则测到的是「被截断」而不是「本来要多久」。
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", base+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Gw-Work-Type", "meeting")

	st := time.Now()
	resp, err := (&http.Client{Timeout: 145 * time.Second}).Do(req)
	if err != nil {
		fmt.Printf("LONG_STREAM 请求失败 %s err=%v\n", time.Since(st).Round(time.Millisecond), err)
		return
	}
	hdrAt := time.Since(st)

	firstContent := time.Duration(0)
	lastContent := time.Duration(0)
	frames, dataFrames := 0, 0
	chars := 0
	finishReason := ""
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		frames++
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[5:])
		if payload == "[DONE]" {
			continue
		}
		dataFrames++
		var d struct {
			Model        string `json:"model"`
			FinishReason string `json:"finish_reason"`
			Choices      []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &d); err != nil {
			continue
		}
		if d.FinishReason != "" {
			finishReason = d.FinishReason
		}
		for _, c := range d.Choices {
			if c.Delta.Content != "" {
				if firstContent == 0 {
					firstContent = time.Since(st)
				}
				lastContent = time.Since(st)
				chars += len([]rune(c.Delta.Content))
			}
		}
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	total := time.Since(st)

	fmt.Printf("LONG_STREAM status=%d 响应头=%s 首帧正文=%s 末帧正文=%s 总耗时=%s data帧=%d 正文字符=%d finish_reason=%q\n",
		resp.StatusCode, hdrAt.Round(time.Millisecond), durOr(firstContent), durOr(lastContent),
		total.Round(time.Millisecond), dataFrames, chars, finishReason)

	// ⚠⚠ 判空流**必须看正文字符数，不能看 data 帧数**。
	// 第一版这里写的是 `dataFrames == 0`，结果把一次真实的空流判成了成功：
	// 那次网关只回了一帧 `data:`（stream_options.include_usage 的 usage-only 帧，
	// 无 choices、无 content），于是 dataFrames=1 通过了「非空」判定，
	// 打印出「跑完了…流式方向对长会是可行的」—— 而实际正文字符数是 0。
	// 「有 data 帧」与「模型说了话」是两件事：`[DONE]` 之外还有 usage-only 帧、
	// 空 delta、纯 tool_calls 帧，它们都不含正文。
	switch {
	case chars == 0:
		fmt.Printf("LONG_STREAM ⇒ 本次是**空流**（data 帧 %d 个、正文字符 0 个，与 §48.4 的 A/B/C 同形态），"+
			"本次读数**说明不了**长会耗时\n", dataFrames)
	case total > 90*time.Second:
		fmt.Printf("LONG_STREAM ⇒ 跑完了，但总耗时 %s **超过产品的 Timeout: 90s** ⇒ 流式也救不了长会\n",
			total.Round(time.Millisecond))
	default:
		fmt.Printf("LONG_STREAM ⇒ 跑完了，总耗时 %s **在 90s 内** ⇒ 流式方向对长会是可行的\n",
			total.Round(time.Millisecond))
	}
}

func durOr(d time.Duration) string {
	if d == 0 {
		return "无"
	}
	return d.Round(time.Millisecond).String()
}
