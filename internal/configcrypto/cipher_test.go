package configcrypto

import (
	"bytes"
	"testing"
)

func TestCipherRoundTripUsesRandomNonceAndAAD(t *testing.T) {
	key := bytes.Repeat([]byte{0x2a}, 32)
	cipher, err := New(key)
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	first, err := cipher.Encrypt("smtp.password", "secret")
	if err != nil {
		t.Fatalf("encrypt first: %v", err)
	}
	second, err := cipher.Encrypt("smtp.password", "secret")
	if err != nil {
		t.Fatalf("encrypt second: %v", err)
	}
	if bytes.Equal(first.Nonce, second.Nonce) || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatal("expected independently randomized encrypted values")
	}
	plaintext, err := cipher.Decrypt("smtp.password", first)
	if err != nil || plaintext != "secret" {
		t.Fatalf("decrypt: plaintext=%q err=%v", plaintext, err)
	}
	if _, err := cipher.Decrypt("s3.secret_key", first); err == nil {
		t.Fatal("expected AAD mismatch to fail")
	}
}

func TestNewRejectsWrongKeyLength(t *testing.T) {
	if _, err := New([]byte("short")); err == nil {
		t.Fatal("expected wrong key length to fail")
	}
}
