package stt

// 诊断用：把 SplitWAV 切出来的每一段落盘，供外部用真实 ASR 引擎逐段验证。
//
// 为什么需要它（2026-10-01）：
// TranscribeFull 在「全部段失败」时只返回一个聚合 error，**每段的明细被丢掉了**
// （见 full.go:400）。于是黑盒只能看到「5 段全部失败」，看不到是哪一段、
// 为什么失败。这个 Test 把段写到临时目录里，就能：
//   1. 确认 buildWAV 产出的确实是合法 WAV（而不是「声明长度≠实际长度」的坏文件）
//   2. 拿真引擎逐段跑，区分「切分坏了」与「上游拒绝」
//
// 只在显式设置 STT_DUMP_SEGMENTS 环境变量时执行，避免污染普通 go test。

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestDumpSplitSegments(t *testing.T) {
	dir := os.Getenv("STT_DUMP_SEGMENTS")
	if dir == "" {
		t.Skip("设置 STT_DUMP_SEGMENTS=<目录> 才执行")
	}
	src := os.Getenv("STT_DUMP_SOURCE")
	if src == "" {
		t.Fatal("需要 STT_DUMP_SOURCE=<wav 路径>")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	segs, ok := SplitWAV(data, defaultSegmentSec)
	if !ok {
		t.Fatal("SplitWAV 拒绝了这个文件")
	}
	t.Logf("切出 %d 段", len(segs))
	for _, s := range segs {
		p := filepath.Join(dir, "seg-"+itoa(s.Index)+".wav")
		if err := os.WriteFile(p, s.Audio, 0o644); err != nil {
			t.Fatal(err)
		}
		// 自查：声明长度必须与实际长度一致，否则上游解码器拿到的是坏文件。
		if len(s.Audio) < 44 {
			t.Errorf("段 %d 只有 %d 字节，不可能是合法 WAV", s.Index, len(s.Audio))
			continue
		}
		if string(s.Audio[0:4]) != "RIFF" || string(s.Audio[8:12]) != "WAVE" {
			t.Errorf("段 %d 缺 RIFF/WAVE 标识", s.Index)
		}
		riffLen := int(binary.LittleEndian.Uint32(s.Audio[4:8]))
		if riffLen != len(s.Audio)-8 {
			t.Errorf("段 %d RIFF 长度 %d != 实际 %d", s.Index, riffLen, len(s.Audio)-8)
		}
		t.Logf("  seg-%d [%.2f–%.2f] %d 字节 useful=%v silenceCut=%v",
			s.Index, s.StartSec, s.EndSec, len(s.Audio), s.IsUsefulSegment(), s.SilenceCut)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [8]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
