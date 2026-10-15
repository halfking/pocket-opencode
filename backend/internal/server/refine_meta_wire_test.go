// refine_meta_wire_test.go — §85：**前端 meta → 请求体 JSON → Go 结构体 → 提示词**
// 这一段线缆此前没有任何门。
//
// 为什么需要：术语表（§64/§80/§81 实测过「悬借→悬界 12/12」靠它）
// 全靠 `meta.title` 一路活到 `buildRefinePrompt`。而两侧各自都有门：
//
//	· 前端侧：roster-repair-wiring.test.ts 的 §84 组证明「title 取自 meeting.title」
//	· Go 侧：refine_prompt_meta_test.go 证明「buildRefinePrompt 会用上它」
//
// **中间那段没人验** —— 前端发 `title`、Go 结构体写成 `meeting_title`，
// 两边的门都照样全绿，而术语表当场蒸发。
//
// ⚠ 本文件只负责 **Go 这一半**（JSON 解码 → 结构体 → 提示词）。
//
//	「前端字段名与 Go tag 是否逐条对齐」由前端侧
//	src/api/__tests__/refine-meta-parity.test.mjs 负责 —— 两半合起来才闭环。
package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// refineRequestBody 的形状**逐字取自** frontend/src/api/meetings.ts 的 `refine()`
// （`JSON.stringify({ segments, target_langs: targetLangs, meta })`）。
// 改这里之前先改那边。
const frontendRefineBody = `{
  "segments": [{"speaker":"张伟","text":"跟章伟还有林兰开个客户评审会"}],
  "target_langs": ["en"],
  "meta": {
    "title": "客户评审会",
    "participants": ["张伟", "林岚"],
    "location": "深圳办公室",
    "topic": "悬界芯片客户评审会"
  }
}`

func TestRefineMetaSurvivesTheWire(t *testing.T) {
	// ★ 2026-10-07（§92）：用 handleMeetingRefine **本人**解出来的具名类型。
	//   原来这里是把 handler 内的匿名结构体**抄了一份**（连字段带 tag）——
	//   抄错一侧两边照样全绿，而「同一份形状抄两遍」正是 §80 记过的缺陷形状。
	var body meetingRefineBody
	if err := json.Unmarshal([]byte(frontendRefineBody), &body); err != nil {
		t.Fatalf("前端那份 body 解不出来 ⇒ 线缆已断：%v", err)
	}

	// ★ §143：title 与 topic 刻意用**不同**的值。
	//   旧夹具把「悬界芯片客户评审会」整个塞进 title —— 那正是
	//   「产品名只能靠 title 混进去」这个死路的化石。两条线缆要各验各的。
	if body.Meta.Title != "客户评审会" {
		t.Errorf("meta.title 在解码时丢了（实际 %q）⇒ 术语表会静默失效", body.Meta.Title)
	}
	if body.Meta.Topic != "悬界芯片客户评审会" {
		t.Errorf("meta.topic 在解码时丢了（实际 %q）⇒ 主题里的专有名词到不了精校", body.Meta.Topic)
	}
	if len(body.Meta.Participants) != 2 || body.Meta.Participants[0] != "张伟" {
		t.Errorf("meta.participants 在解码时丢了（实际 %v）", body.Meta.Participants)
	}
	if body.Meta.Location != "深圳办公室" {
		t.Errorf("meta.location 在解码时丢了（实际 %q）", body.Meta.Location)
	}
	if len(body.Segments) != 1 {
		t.Fatalf("segments 解码异常：%d 条", len(body.Segments))
	}

	// 终点断言：这三个值必须真的进了提示词，而不只是活在结构体里。
	prompt := buildRefinePrompt(segmentsToText(body.Segments), strings.Join(body.TargetLangs, ", "), body.Meta)
	for _, want := range []string{"悬界芯片客户评审会", "张伟", "林岚", "深圳办公室"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("提示词里没有「%s」⇒ 元数据没走到最后一步", want)
		}
	}
}

func TestRefineMetaWireNegativeControl(t *testing.T) {
	// ⚠ 这条是**防判据恒真**的：若改名一侧（例如 Go tag 改成 `meeting_title`），
	//   上面那条必须转红。若这里也转红，说明是构造样本时自己写坏了。
	const renamed = `{"meta":{"meeting_title":"悬界芯片客户评审会"}}`
	var got struct {
		Meta meetingMetaIn `json:"meta"`
	}
	if err := json.Unmarshal([]byte(renamed), &got); err != nil {
		t.Fatalf("样本本身解不出来：%v", err)
	}
	if got.Meta.Title != "" {
		t.Fatalf("样本构造有问题：改名的字段竟然解出了 Title=%q", got.Meta.Title)
	}
	if got.Meta.Title == "" {
		t.Log("改名后的字段确实解不出值 ⇒ 上面那条判据会在改名时转红，具备牙")
	}
}

// TestRefineMetaFieldNamesAreStable 把「四个字段名」本身钉住。
// 少一个字段、或改了名字，下游的术语表能力会无声退化，而没有任何行为门会响。
//
// ★ §143（主题接入）：`Topic` 是**有意新增**，所以改的是这张登记表本身，
// 不是判据。这道门的设计正是要让人在**加字段时必须来这里表态**
// （它的失败消息里已经写好了要连带改哪两处）—— 换成放宽判据
// 才是削弱它。
func TestRefineMetaFieldNamesAreStable(t *testing.T) {
	rt := reflect.TypeOf(meetingMetaIn{})
	want := map[string]bool{"Title": true, "Participants": true, "Location": true, "Topic": true}
	if rt.NumField() != len(want) {
		var got []string
		for i := 0; i < rt.NumField(); i++ {
			got = append(got, rt.Field(i).Name)
		}
		t.Fatalf("meetingMetaIn 的字段变了（%v）⇒ 前端侧 refine-meta-parity 与 §84 的门都要跟着改", got)
	}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !want[f.Name] {
			t.Errorf("多出字段 %s", f.Name)
		}
		// json tag 必须是小写蛇形，且与字段名对应 —— 前端侧按这个名字发。
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "" || tag != strings.ToLower(f.Name[:1])+f.Name[1:] {
			t.Errorf("%s 的 json tag 是 %q，与前端约定的字段名不符", f.Name, tag)
		}
	}
}
