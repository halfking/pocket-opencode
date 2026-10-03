package email

// diag_rawbytes_dump_test.go — 一次性：把 49 封真实原文**解出来**落成明文，
// 供前端 email-body-format 的诊断脚本消费。只读 .bin + master key，不连 PG/IMAP。
//
// 门禁：POCKET_DIAG_RAWBYTES_DUMP=1 且 POCKET_DIAG_RAWBYTES_OUT=<目录>

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiagRawBytesDump(t *testing.T) {
	if os.Getenv("POCKET_DIAG_RAWBYTES_DUMP") != "1" {
		t.Skip("set POCKET_DIAG_RAWBYTES_DUMP=1 and POCKET_DIAG_RAWBYTES_OUT=<dir>")
	}
	out := os.Getenv("POCKET_DIAG_RAWBYTES_OUT")
	dataDir := os.Getenv("POCKET_DIAG_QP_DATADIR")
	if out == "" || dataDir == "" {
		t.Fatal("POCKET_DIAG_RAWBYTES_OUT / POCKET_DIAG_QP_DATADIR 未设置")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	key, err := EnsureMasterKey("", dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cr, err := NewCrypto(key)
	if err != nil {
		t.Fatal(err)
	}
	files, err := collectBodyFiles(filepath.Join(dataDir, "email-bodies-raw"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range files {
		blob, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		_, payload, err := locateCiphertext(blob)
		if err != nil {
			continue
		}
		dec, err := cr.DecryptString(payload)
		if err != nil || len(dec) == 0 {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f), ".bin") + ".eml"
		if err := os.WriteFile(filepath.Join(out, name), []byte(dec), 0o644); err != nil {
			t.Fatal(err)
		}
		n++
	}
	t.Logf("导出 %d/%d 封真实 MIME 原文到 %s", n, len(files), out)
}
