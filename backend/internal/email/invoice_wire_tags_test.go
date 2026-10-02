// invoice_wire_tags_test.go — 发票上线结构体的 json tag 护栏。
//
// 为什么需要它（2026-10-03 真机实测）
// ------------------------------------
// CurrencyTotal 的三个字段当初都漏了 json tag，线上发的是
//
//	{"Currency":"CNY","Amount":3500,"Count":1}
//
// 前端读 a.currency / a.amount 得到 undefined，round2(undefined) = NaN，
// 发票页合计显示成 ¥NaN。
//
// **关键在于它为什么能活到真机**：Go 的 json.Unmarshal 对键名大小写不敏感，
// 所以把响应体解进 struct 的服务端用例（TestInvoiceSummary_*）从头到尾全绿——
// 它证明的只是「Go 读得懂自己」。真正的消费者是大小写敏感的 JS/TS。
// 也就是说：**所有响应级 Go 判据在这一类缺陷上都是恒真的**，
// 只有在结构体这一层「你到底声明了键名没有」才拦得住。
//
// 本文件就是那一层。它不做业务判断，只问一件事：
// 真的会被序列化上线的发票结构体，它的每个导出字段都声明了自己的 json 键名吗？
package email

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// invoiceWireTagged 是**真的**被序列化到 HTTP 响应里的发票结构体。
//
// 刻意不含 InvoiceListPage：它一个 json tag 都没有，但**它不上线**——
// server 层的 handler 把它转成另一个带 tag 的结构体再写响应。
// 这一点是实测的，不是推断的：18099 真机 GET /api/emails/invoices
// 发出来的是 {"amount":…,"amounts":[…],"filed":0,"hasMore":true,"invoices":[…]}，
// 行内也是 emailId / invoiceNo / createdAt，全小驼峰。
//
// 把没上线的类型也塞进名单，这条判据会立刻误报；而一条会误报的判据
// 下一次就会被整体关掉——那比没有更糟。所以名单要窄，且每一条都要有实测背书。
var invoiceWireTagged = []struct {
	name string
	typ  reflect.Type
}{
	{"Invoice", reflect.TypeOf(Invoice{})},
	{"CurrencyTotal", reflect.TypeOf(CurrencyTotal{})},
}

// TestWireTagGuard_ExportedFieldsHaveJSONTags
// 每个导出字段都必须声明一个非空、非 "-",、小驼峰开头、且不与同结构体其他字段重名的 tag。
func TestWireTagGuard_ExportedFieldsHaveJSONTags(t *testing.T) {
	// 自检 1：名单不能是空的，否则「全部通过」等于什么都没查。
	if len(invoiceWireTagged) == 0 {
		t.Fatal("SELFCHECK: invoiceWireTagged 是空的，这条判据现在恒真")
	}

	seen := map[string]string{}
	for _, e := range invoiceWireTagged {
		// 自检 2：每个条目都要真的解析到一个 struct 类型。
		if e.typ == nil {
			t.Fatalf("SELFCHECK: %s 的 reflect.TypeOf 返回 nil", e.name)
		}
		if e.typ.Kind() != reflect.Struct {
			t.Fatalf("SELFCHECK: %s 不是 struct 而是 %s", e.name, e.typ.Kind())
		}
		if prev, dup := seen[e.typ.String()]; dup {
			t.Fatalf("SELFCHECK: %s 与名单里的 %s 是同一个类型，重复登记", e.name, prev)
		}
		seen[e.typ.String()] = e.name

		tagOwner := map[string]string{} // tag 键名 -> 字段名
		exported := 0
		for i := 0; i < e.typ.NumField(); i++ {
			f := e.typ.Field(i)
			if !f.IsExported() {
				continue
			}
			exported++

			tag := f.Tag.Get("json")
			if tag == "" {
				// 这就是 CurrencyTotal 当初的形状。
				t.Errorf("%s.%s 没有 json tag；线上会发出字段名 %q 本身，而前端读的是小驼峰 -> undefined",
					e.name, f.Name, f.Name)
				continue
			}
			key := strings.Split(tag, ",")[0]
			switch {
			case key == "-":
				t.Errorf("%s.%s 是 json:\"-\"：确认它真的不该出现在发票响应里", e.name, f.Name)
				continue
			case key == "":
				t.Errorf("%s.%s 的 tag %q 逗号前没有键名（encoding/json 会退回用字段名）", e.name, f.Name, tag)
				continue
			}
			if key[0] >= 'A' && key[0] <= 'Z' {
				t.Errorf("%s.%s 的 tag %q 是大驼峰；线上键名会与前端读的小驼峰对不上", e.name, f.Name, key)
			}
			if prev, dup := tagOwner[key]; dup {
				// encoding/json 对同名字段是「两个都别写」，不报错。
				t.Errorf("%s 里 %s 与 %s 的 tag 同为 %q，encoding/json 会静默丢掉一个",
					e.name, prev, f.Name, key)
			}
			tagOwner[key] = f.Name
		}
		// 自检 3：反射真的遍历到了字段。
		if exported == 0 {
			t.Errorf("SELFCHECK: %s 一个导出字段都没遍历到", e.name)
		}
	}
}

// TestWireTagGuard_MarshalledKeysAreLowerCamel
// 上一条是「声明层」，这条是「产物层」：真的 marshal 一次，看发出去的键名。
//
// 两条并存的意义：上一条靠读 tag，这条靠 encoding/json 的实际行为。
// 若哪天 struct 里加了匿名嵌入字段，只有这条能发现键名对不上。
// omitempty 的字段在零值时不出现，所以这里只断言「**出现**的键不许有大驼峰」，
// 而不是断言键的集合相等——后者会因为 omitempty 而脆弱。
func TestWireTagGuard_MarshalledKeysAreLowerCamel(t *testing.T) {
	if len(invoiceWireTagged) == 0 {
		t.Fatal("SELFCHECK: invoiceWireTagged 是空的，这条判据现在恒真")
	}
	for _, e := range invoiceWireTagged {
		// 必须 marshal **零值实例**，不是 e.typ 本身。
		// json.Marshal(reflect.TypeOf(Invoice{})) 序列化的是 reflect.Type
		// 那个值（内部字段全不导出），结果是 {}，断言会对着一个空对象空转。
		raw, err := json.Marshal(reflect.New(e.typ).Elem().Interface())
		if err != nil {
			// 零值 struct 一般不会 marshal 失败；真失败说明类型有问题，直接报。
			t.Fatalf("%s marshal 失败: %v", e.name, err)
		}
		if len(raw) == 0 {
			t.Fatalf("SELFCHECK: %s marshal 出 0 字节", e.name)
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatalf("SELFCHECK: %s 的 marshal 结果解不回 object: %v", e.name, err)
		}
		// 零值下只有不带 omitempty 的字段会露出来，所以这里至少有键才谈得上断言。
		if len(obj) == 0 {
			t.Fatalf("SELFCHECK: %s 零值 marshal 后一个键都没有，反射或类型有问题", e.name)
		}
		for k := range obj {
			if k == "" {
				t.Errorf("%s marshal 出了空键名", e.name)
				continue
			}
			if k[0] >= 'A' && k[0] <= 'Z' {
				t.Errorf("%s marshal 出了大驼峰键 %q；前端读小驼峰 -> undefined", e.name, k)
			}
		}
	}
}
