// Package secure provides password hashing, symmetric encryption for stored
// signing credentials, and master-key management. Standard library only.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const passwordIterations = 120000

func pbkdf2Key(password, salt []byte, iterations, keyLen int) []byte {
	const hLen = 32
	blocks := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, blocks*hLen)
	var counter [4]byte
	for i := 1; i <= blocks; i++ {
		counter[0] = byte(i >> 24)
		counter[1] = byte(i >> 16)
		counter[2] = byte(i >> 8)
		counter[3] = byte(i)
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write(counter[:])
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for j := 1; j < iterations; j++ {
			mac = hmac.New(sha256.New, password)
			mac.Write(u)
			u = mac.Sum(nil)
			for k := range t {
				t[k] ^= u[k]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func RandomHex(n int) (string, error) {
	b, err := randomBytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// HashPassword returns a self-describing PBKDF2-HMAC-SHA256 hash.
func HashPassword(password string) (string, error) {
	salt, err := randomBytes(16)
	if err != nil {
		return "", err
	}
	dk := pbkdf2Key([]byte(password), salt, passwordIterations, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", passwordIterations,
		hex.EncodeToString(salt), hex.EncodeToString(dk)), nil
}

func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 1 || iterations > 10000000 {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got := pbkdf2Key([]byte(password), salt, iterations, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// DeriveKey derives a 32-byte AES key from a passphrase.
func DeriveKey(passphrase string) [32]byte {
	return sha256.Sum256([]byte("ios-auto-sign/master/v1\x00" + passphrase))
}

// LoadMasterKey prefers IOS_SIGN_MASTER_KEY; otherwise it reads or creates a
// hex-encoded 32-byte key file. The file must be backed up: losing it makes
// all stored p12 bundles undecryptable.
func LoadMasterKey(path string) (*[32]byte, error) {
	if p := os.Getenv("IOS_SIGN_MASTER_KEY"); p != "" {
		k := DeriveKey(p)
		return &k, nil
	}
	b, err := os.ReadFile(path)
	if err == nil {
		raw, decErr := hex.DecodeString(strings.TrimSpace(string(b)))
		if decErr != nil || len(raw) != 32 {
			return nil, errors.New("master key file is not 32 bytes of hex")
		}
		var key [32]byte
		copy(key[:], raw)
		return &key, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	raw, err := randomBytes(32)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(raw)), 0o600); err != nil {
		return nil, err
	}
	var key [32]byte
	copy(key[:], raw)
	return &key, nil
}

func Encrypt(plain []byte, key *[32]byte) (string, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce, err := randomBytes(gcm.NonceSize())
	if err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, plain, nil)
	return base64.StdEncoding.EncodeToString(append(nonce, sealed...)), nil
}

func Decrypt(encoded string, key *[32]byte) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(raw) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	return gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
}
