package secure

import (
	"bytes"
	"testing"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse battery staple", h) {
		t.Fatal("valid password rejected")
	}
	if VerifyPassword("wrong password", h) {
		t.Fatal("invalid password accepted")
	}
	h2, _ := HashPassword("correct horse battery staple")
	if h == h2 {
		t.Fatal("hash is not salted")
	}
}

func TestEncryptDecrypt(t *testing.T) {
	key := DeriveKey("unit-test-key")
	plain := []byte("-----BEGIN PKCS12-----\x00\x01\x02private key bytes")
	enc, err := Encrypt(plain, &key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(enc), plain[:20]) {
		t.Fatal("ciphertext leaks plaintext")
	}
	got, err := Decrypt(enc, &key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("decrypted plaintext mismatch")
	}
	other := DeriveKey("different-key")
	if _, err := Decrypt(enc, &other); err == nil {
		t.Fatal("decryption with wrong key succeeded")
	}
}

func TestDecryptRejectsTampering(t *testing.T) {
	key := DeriveKey("unit-test-key")
	enc, _ := Encrypt([]byte("secret"), &key)
	tampered := enc[:len(enc)-4] + "AAAA"
	if _, err := Decrypt(tampered, &key); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}
