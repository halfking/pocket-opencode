package mcp

import "testing"

// Capabilities 必须显式声明 read-only：当前生产代码只调 GetRemoteTasks，
// 任何对 Capabilities 的修改都要同步测试——这是 §5 「企业集成只读」验收
// 的最直接护栏。
func TestCapabilities_AccReadOnly(t *testing.T) {
	c := NewClient("http://example.test", "k", false)
	caps := c.Capabilities()
	if caps.Connector != "acc" {
		t.Fatalf("expected acc connector, got %q", caps.Connector)
	}
	if !caps.Read {
		t.Fatalf("Read must be true")
	}
	if caps.Write {
		t.Fatalf("Write must be false: P3 only allows read-only ACC connector")
	}
	if len(caps.Tools) == 0 || caps.Tools[0] != "acc_get_tasks" {
		t.Fatalf("Tools must include acc_get_tasks, got %+v", caps.Tools)
	}
}

func TestCapabilities_NilClientDoesNotPanic(t *testing.T) {
	var c *Client
	caps := c.Capabilities()
	if caps.Read || caps.Write {
		t.Fatalf("nil client must report zero capabilities, got %+v", caps)
	}
	if caps.Connector != "" {
		t.Fatalf("nil client must not declare connector, got %q", caps.Connector)
	}
}