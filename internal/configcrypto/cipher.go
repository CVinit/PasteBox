package configcrypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

const Version = 1

type Cipher struct {
	aead cipher.AEAD
}

type EncryptedValue struct {
	Version    int
	Nonce      []byte
	Ciphertext []byte
}

func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("config encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create config cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create config AEAD: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(name string, plaintext string) (EncryptedValue, error) {
	if c == nil || c.aead == nil {
		return EncryptedValue{}, fmt.Errorf("config cipher is not initialized")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return EncryptedValue{}, fmt.Errorf("generate config nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, []byte(plaintext), []byte(name))
	return EncryptedValue{Version: Version, Nonce: nonce, Ciphertext: ciphertext}, nil
}

func (c *Cipher) Decrypt(name string, value EncryptedValue) (string, error) {
	if c == nil || c.aead == nil {
		return "", fmt.Errorf("config cipher is not initialized")
	}
	if value.Version != Version {
		return "", fmt.Errorf("unsupported config secret version %d", value.Version)
	}
	if len(value.Nonce) != c.aead.NonceSize() {
		return "", fmt.Errorf("invalid config secret nonce")
	}
	plaintext, err := c.aead.Open(nil, value.Nonce, value.Ciphertext, []byte(name))
	if err != nil {
		return "", fmt.Errorf("decrypt config secret %q: %w", name, err)
	}
	return string(plaintext), nil
}
