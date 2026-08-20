package email

// crypto_atomic_test.go asserts that master key materialisation uses an
// atomic rename (O_EXCL|O_CREATE) and refuses to clobber an existing key
// that is shorter than 32 bytes — that case used to mean "treat as
// missing and regenerate", but a half-written key from a crashed
// predecessor is exactly the file we should NOT overwrite, because
// any ciphertext already encrypted under it would be unrecoverable.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteKeyAtomic_CreatesFileWithCorrectMode(t *testing.T) {
	dir := t.TempDir()
	key := bytes.Repeat([]byte{0xab}, 32)

	if err := writeKeyAtomic(dir, key); err != nil {
		t.Fatalf("writeKeyAtomic: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "email_master.key"))
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Fatalf("key content mismatch")
	}
	info, err := os.Stat(filepath.Join(dir, "email_master.key"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("key file mode: got %o, want 0600", perm)
	}
}

func TestWriteKeyAtomic_RefusesToClobberPartialKey(t *testing.T) {
	// A 16-byte stub is exactly the failure mode the old code had: the
	// existence check `len(data) == 32` would say "no key", then
	// os.WriteFile would silently overwrite it. That loses the bytes the
	// previous process actually managed to flush.
	dir := t.TempDir()
	partial := bytes.Repeat([]byte{0xcd}, 16)
	partialPath := filepath.Join(dir, "email_master.key")
	if err := os.WriteFile(partialPath, partial, 0600); err != nil {
		t.Fatalf("seed partial key: %v", err)
	}

	fresh := bytes.Repeat([]byte{0xef}, 32)
	if err := writeKeyAtomic(dir, fresh); err == nil {
		t.Fatalf("writeKeyAtomic must refuse to overwrite a partial key, but it succeeded")
	}

	// The original partial bytes must be untouched.
	got, err := os.ReadFile(partialPath)
	if err != nil {
		t.Fatalf("read after refused write: %v", err)
	}
	if !bytes.Equal(got, partial) {
		t.Fatalf("partial key was overwritten: got %x, want %x", got, partial)
	}
}

func TestWriteKeyAtomic_RefusesToClobberFullLengthKey(t *testing.T) {
	// A full-length key already on disk is the recovery anchor for any
	// ciphertext that has ever been encrypted under it. writeKeyAtomic
	// must refuse to clobber it; rotation is the caller's job, not this
	// helper's.
	dir := t.TempDir()
	existing := bytes.Repeat([]byte{0x11}, 32)
	existingPath := filepath.Join(dir, "email_master.key")
	if err := os.WriteFile(existingPath, existing, 0600); err != nil {
		t.Fatalf("seed existing key: %v", err)
	}

	fresh := bytes.Repeat([]byte{0x22}, 32)
	if err := writeKeyAtomic(dir, fresh); err == nil {
		t.Fatalf("writeKeyAtomic must refuse to overwrite a full-length key, but it succeeded")
	}

	got, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatalf("read after refused write: %v", err)
	}
	if !bytes.Equal(got, existing) {
		t.Fatalf("existing key was overwritten: got %x, want %x", got, existing)
	}
}

func TestWriteKeyAtomic_RejectsWrongLength(t *testing.T) {
	// 31 bytes is one short of a real key. The helper must reject it
	// instead of silently padding or truncating; the caller should have
	// generated 32 bytes from crypto/rand.
	dir := t.TempDir()
	short := bytes.Repeat([]byte{0x33}, 31)
	if err := writeKeyAtomic(dir, short); err == nil {
		t.Fatalf("writeKeyAtomic must reject a 31-byte key")
	}
	if _, err := os.Stat(filepath.Join(dir, "email_master.key")); !os.IsNotExist(err) {
		t.Fatalf("no file should have been written, but stat returned %v", err)
	}
}
