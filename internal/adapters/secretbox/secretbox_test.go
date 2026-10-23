package secretbox

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The key file lives next to the database (issue #362, S9): the box is
// opened over a directory and names the file itself.
func newTestBox(t *testing.T) (*Box, string) {
	t.Helper()
	dir := t.TempDir()
	return New(dir), filepath.Join(dir, KeyFileName)
}

func TestSealCreatesOwnerOnlyKeyFileAndRoundTrips(t *testing.T) {
	box, keyPath := newTestBox(t)
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key file exists before the first seal: %v", err)
	}

	sealed, err := box.Seal([]byte("hunter2"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(sealed, "hunter2") {
		t.Fatalf("sealed value contains the plaintext: %q", sealed)
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("key file not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("key file mode = %o, want 600", perm)
	}
	if info.Size() != keySize {
		t.Fatalf("key file size = %d, want %d", info.Size(), keySize)
	}

	got, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if string(got) != "hunter2" {
		t.Fatalf("Open = %q, want hunter2", got)
	}
}

func TestSealIsRandomizedAndKeyIsReused(t *testing.T) {
	box, keyPath := newTestBox(t)
	a, err := box.Seal([]byte("same"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	key1, _ := os.ReadFile(keyPath)
	b, err := box.Seal([]byte("same"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	key2, _ := os.ReadFile(keyPath)
	if a == b {
		t.Fatal("two seals of the same plaintext are identical: nonce is not random")
	}
	if !bytes.Equal(key1, key2) {
		t.Fatal("the second seal replaced the key file")
	}
	// A fresh Box over the same directory (a restart) still opens it.
	got, err := New(filepath.Dir(keyPath)).Open(a)
	if err != nil || string(got) != "same" {
		t.Fatalf("Open after restart = %q, %v", got, err)
	}
}

func TestOpenWithoutKeyFileFailsAndCreatesNothing(t *testing.T) {
	sealer, _ := newTestBox(t)
	sealed, err := sealer.Seal([]byte("pw"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	// A database restored without its key file (S9): Open must report the
	// missing key and must never mint a new one.
	box, keyPath := newTestBox(t)
	if _, err := box.Open(sealed); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Open without key = %v, want ErrKeyUnavailable", err)
	}
	if _, err := os.Stat(keyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Open created a key file: %v", err)
	}
}

func TestOpenRejectsTamperedOrForeignValues(t *testing.T) {
	box, _ := newTestBox(t)
	sealed, err := box.Seal([]byte("pw"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	other, _ := newTestBox(t)
	foreign, err := other.Seal([]byte("pw"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	tampered := []byte(sealed)
	last := len(tampered) - 2
	if tampered[last] == 'A' {
		tampered[last] = 'B'
	} else {
		tampered[last] = 'A'
	}
	for name, value := range map[string]string{
		"tampered":   string(tampered),
		"other key":  foreign,
		"plain text": "pw",
		"no payload": "v1:",
		"empty":      "",
	} {
		if _, err := box.Open(value); !errors.Is(err, ErrInvalidSealed) {
			t.Errorf("%s: Open = %v, want ErrInvalidSealed", name, err)
		}
	}
}

func TestMalformedKeyFileIsRefused(t *testing.T) {
	box, keyPath := newTestBox(t)
	if err := os.WriteFile(keyPath, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := box.Seal([]byte("pw")); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Seal over a malformed key = %v, want ErrKeyUnavailable", err)
	}
	if got, _ := os.ReadFile(keyPath); string(got) != "short" {
		t.Fatal("a malformed key file was overwritten")
	}
}

func TestConcurrentFirstSealsShareOneKey(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	sealed := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Separate boxes model separate callers racing on the file.
			s, err := New(dir).Seal([]byte("pw"))
			if err != nil {
				t.Errorf("Seal: %v", err)
			}
			sealed[i] = s
		}(i)
	}
	wg.Wait()
	box := New(dir)
	for i, s := range sealed {
		if got, err := box.Open(s); err != nil || string(got) != "pw" {
			t.Errorf("seal %d does not open with the surviving key: %q, %v", i, got, err)
		}
	}
}

// Issue #419: the provider client secrets share the SMTP key file, so each
// purpose binds its values to its own additional data. A value sealed for one
// purpose — or by the purpose-less box the SMTP password uses — never opens
// as another, so a stored ciphertext cannot be moved between columns.
func TestPurposeBoundValuesOpenOnlyUnderTheirPurpose(t *testing.T) {
	box, _ := newTestBox(t)
	google := box.For("google-client-secret")
	microsoft := box.For("microsoft-client-secret")

	sealed, err := google.Seal([]byte("g-secret"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	got, err := google.Open(sealed)
	if err != nil || string(got) != "g-secret" {
		t.Fatalf("Open under its own purpose = %q, %v; want g-secret", got, err)
	}
	if _, err := microsoft.Open(sealed); !errors.Is(err, ErrInvalidSealed) {
		t.Fatalf("Open under another purpose err = %v, want ErrInvalidSealed", err)
	}
	if _, err := box.Open(sealed); !errors.Is(err, ErrInvalidSealed) {
		t.Fatalf("purpose-less Open of a purpose-bound value err = %v, want ErrInvalidSealed", err)
	}

	smtp, err := box.Seal([]byte("smtp-password"))
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if _, err := google.Open(smtp); !errors.Is(err, ErrInvalidSealed) {
		t.Fatalf("purpose-bound Open of the SMTP value err = %v, want ErrInvalidSealed", err)
	}
	if got, err := box.Open(smtp); err != nil || string(got) != "smtp-password" {
		t.Fatalf("the SMTP value no longer opens as before: %q, %v", got, err)
	}
}
