package securedata

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const prefix = "enc:v1:"

type Cipher struct {
	aead cipher.AEAD
}

func New(encodedKey string) (*Cipher, error) {
	if strings.TrimSpace(encodedKey) == "" {
		return &Cipher{}, nil
	}
	key, err := base64.RawURLEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, errors.New("DATA_ENCRYPTION_KEY must be unpadded base64url")
	}
	if len(key) != 32 {
		return nil, errors.New("DATA_ENCRYPTION_KEY must decode to exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create data cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create data AEAD: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Protect(value string) (string, error) {
	if c == nil || c.aead == nil || value == "" {
		return value, nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate data nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nil, nonce, []byte(value), nil)
	payload := append(nonce, ciphertext...)
	return prefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (c *Cipher) Reveal(value string) (string, error) {
	if !strings.HasPrefix(value, prefix) {
		return value, nil
	}
	if c == nil || c.aead == nil {
		return "", errors.New("encrypted data cannot be read without DATA_ENCRYPTION_KEY")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil || len(payload) < c.aead.NonceSize() {
		return "", errors.New("encrypted data is malformed")
	}
	plain, err := c.aead.Open(
		nil,
		payload[:c.aead.NonceSize()],
		payload[c.aead.NonceSize():],
		nil,
	)
	if err != nil {
		return "", errors.New("encrypted data authentication failed")
	}
	return string(plain), nil
}
