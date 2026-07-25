package securedata

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCipherRoundTripAndAuthentication(t *testing.T) {
	cipher, err := New("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	protected, err := cipher.Protect("sensitive value")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(protected, "enc:v1:") || protected == "sensitive value" {
		t.Fatalf("protected value = %q", protected)
	}
	revealed, err := cipher.Reveal(protected)
	if err != nil {
		t.Fatal(err)
	}
	if revealed != "sensitive value" {
		t.Fatalf("revealed = %q", revealed)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(protected, "enc:v1:"))
	if err != nil {
		t.Fatal(err)
	}
	payload[len(payload)/2] ^= 1
	tampered := "enc:v1:" + base64.RawURLEncoding.EncodeToString(payload)
	if _, err := cipher.Reveal(tampered); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}
