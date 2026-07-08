package secret

import "testing"

func TestPlainBoxRoundTrip(t *testing.T) {
	var b PlainBox
	sealed, err := b.Seal([]byte("bot-token-123"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := b.Open(sealed)
	if err != nil || string(plain) != "bot-token-123" {
		t.Fatalf("open: %q %v", plain, err)
	}
	if _, err := b.Open("not-a-plainbox-blob"); err == nil {
		t.Fatal("expected error on foreign blob")
	}
}
