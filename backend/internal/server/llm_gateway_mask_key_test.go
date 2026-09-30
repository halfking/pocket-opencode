package server

import "testing"

// maskKey 需在「可识别」与「不泄漏」间平衡：末 6 位用于多 key 场景识别当前
// 绑定的 key；短 key 整体打码，避免前后缀重叠时把整把 key 暴露出来。
func TestMaskKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "******"},
		{"short", "******"},
		{"exactly12chr", "exa******y12chr"}, // len==12：前 3 + 末 6，中间仅遮 3 位仍可接受
		// 合成夹具：形状照抄真实 key（sk- 前缀 + 长 body），值全是明写的占位符。
		// 这里放真 key 只会让仓库替某把真 key 兜底，测试并不需要它。
		// maskKey 保留前 3 + 末 6 ⇒ "sk-" + "******" + "XYZ123"。
		{"sk-TESTKEYNOTREAL000000000000000000000000XYZ123", "sk-******XYZ123"},
	}
	for _, c := range cases {
		if got := maskKey(c.in); got != c.want {
			t.Errorf("maskKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
