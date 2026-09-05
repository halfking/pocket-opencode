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
		{"sk-hMv1qInTUGjBTGRfjv5pzn9rbHspLN8S3LOQFhjLtOiq3QIv", "sk-******iq3QIv"},
	}
	for _, c := range cases {
		if got := maskKey(c.in); got != c.want {
			t.Errorf("maskKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
