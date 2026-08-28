package secrets

import "testing"

func TestSealOpenRoundTrip(t *testing.T) {
	box, err := New("a-session-secret-that-is-long-enough")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !box.Configured() {
		t.Fatal("box should be configured")
	}
	const key = "sk-litellm-0123456789abcdef"
	sealed, err := box.Seal(key)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if string(sealed) == key {
		t.Fatal("sealed value must not equal the plaintext")
	}
	opened, err := box.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened != key {
		t.Fatalf("round trip mismatch: got %q want %q", opened, key)
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	box, _ := New("a-session-secret-that-is-long-enough")
	first, _ := box.Seal("same-value")
	second, _ := box.Seal("same-value")
	if string(first) == string(second) {
		t.Fatal("two seals of the same plaintext must differ")
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	box, _ := New("a-session-secret-that-is-long-enough")
	sealed, _ := box.Seal("sk-litellm-secret")
	sealed[len(sealed)-1] ^= 0xff
	if _, err := box.Open(sealed); err == nil {
		t.Fatal("Open must reject a tampered ciphertext")
	}
}

func TestOpenRejectsOtherSecret(t *testing.T) {
	original, _ := New("a-session-secret-that-is-long-enough")
	sealed, _ := original.Seal("sk-litellm-secret")
	rotated, _ := New("a-different-session-secret-entirely")
	if _, err := rotated.Open(sealed); err == nil {
		t.Fatal("a key sealed under another secret must not open")
	}
}

func TestUnconfiguredBoxRefuses(t *testing.T) {
	box, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if box.Configured() {
		t.Fatal("empty secret must not yield a configured box")
	}
	if _, err := box.Seal("value"); err != ErrNotConfigured {
		t.Fatalf("Seal: got %v want ErrNotConfigured", err)
	}
	if _, err := box.Open([]byte("value")); err != ErrNotConfigured {
		t.Fatalf("Open: got %v want ErrNotConfigured", err)
	}
}

func TestHintRevealsOnlyTail(t *testing.T) {
	if got := Hint("sk-abcdef1234"); got != "••••1234" {
		t.Fatalf("Hint: got %q", got)
	}
	if got := Hint("abc"); got != "••••" {
		t.Fatalf("Hint short: got %q", got)
	}
	if got := Hint(""); got != "" {
		t.Fatalf("Hint empty: got %q", got)
	}
}
